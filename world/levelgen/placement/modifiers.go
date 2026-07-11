package placement

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// ---- BIOME_INFO_NOISE (Biome.BIOME_INFO_NOISE) ----
//
// Both noise_based_count and noise_threshold_count read
// net.minecraft.world.level.biome.Biome.BIOME_INFO_NOISE, a static-final
// PerlinSimplexNoise seeded from a FIXED legacy seed (world-seed-independent), CFR/javap
// -verified from Biome's static initializer:
//
//	BIOME_INFO_NOISE = new PerlinSimplexNoise(new WorldgenRandom(new LegacyRandomSource(2345L)), of(0));
//
// (Confirmed vs javap -c: ldc2_w 2345l, single octave iconst_0.) Built ONCE lazily into
// a package global -- the same instance world/levelgen/surface builds for the FROZEN
// temperature modifier, replicated here so the placement package stays import-cycle clean
// (it must NOT import world/levelgen/surface).
var (
	biomeInfoNoise     *synth.PerlinSimplexNoise
	biomeInfoNoiseOnce sync.Once
)

// biomeInfoNoiseValue ports Biome.BIOME_INFO_NOISE.getValue(x, z, false), the 2D
// PerlinSimplexNoise read both noise-count modifiers gate on. useNoiseStart is false
// (the jar's third arg is always iconst_0 at these call sites).
func biomeInfoNoiseValue(x, z float64) float64 {
	biomeInfoNoiseOnce.Do(func() {
		biomeInfoNoise = synth.NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(2345), []int{0})
	})
	return biomeInfoNoise.GetValue(x, z, false)
}

// This file ports the 8 load-bearing placement modifiers with JAR-exact RNG-draw
// bodies (each transcribed from `javap -c`, cited inline). The draw sequence per
// modifier IS the determinism contract (research Pitfall 4): a wrong draw count
// desyncs the whole decoration stream vs vanilla.
//
// Sources (javap -c, 26.2-inner.jar, package
// net.minecraft.world.level.levelgen.placement):
//   - InSquarePlacement / HeightmapPlacement / HeightRangePlacement (direct getPositions)
//   - RarityFilter / BiomeFilter / SurfaceWaterDepthFilter (extend PlacementFilter)
//   - CountPlacement (extends RepeatingPlacement)

// ---- in_square ----

// InSquare ports InSquarePlacement (a stateless singleton in vanilla). getPositions
// jitters the input pos by nextInt(16) on X then Z, leaving Y. JAR-CONFIRMED draw
// order: X is drawn FIRST, then Z (two NextIntN(16) draws).
type InSquare struct{}

func (InSquare) getPositions(_ PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	x := int(rng.NextIntN(16)) + p.X
	z := int(rng.NextIntN(16)) + p.Z
	return []BlockPos{{X: x, Y: p.Y, Z: z}}
}

// ---- heightmap ----

// Heightmap ports HeightmapPlacement: project (x,z) to the configured live worldgen
// heightmap top. JAR-CONFIRMED: emit {x, GetHeight(type,x,z), z} iff that height >
// MinY, else empty. Consumes 0 rng draws.
type Heightmap struct {
	heightmap HeightmapType
}

func (h Heightmap) getPositions(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) []BlockPos {
	y := ctx.GetHeight(h.heightmap, p.X, p.Z)
	if y <= ctx.MinY() {
		return nil
	}
	return []BlockPos{{X: p.X, Y: y, Z: p.Z}}
}

// ---- count ----

// Count ports CountPlacement (extends RepeatingPlacement): repeat the input pos
// count.Sample(rng) times. The IntProvider draws happen BEFORE the copies are
// emitted (RepeatingPlacement.getPositions evaluates count() first).
type Count struct {
	repeatingPlacement
	countProvider *intProvider
}

// count implements the RepeatingPlacement `counter`: CountPlacement.count(rng,pos) =
// the IntProvider's Sample(rng).
func (c *Count) count(rng levelgen.RandomSource, _ BlockPos) int {
	return c.countProvider.Sample(rng)
}

func newCount(p *intProvider) *Count {
	c := &Count{countProvider: p}
	c.repeatingPlacement.counter = c
	return c
}

// ---- rarity_filter ----

// RarityFilter ports RarityFilter (extends PlacementFilter): keep p iff
// nextFloat() < 1.0/chance. JAR-CONFIRMED: exactly one NextFloat per input pos.
type RarityFilter struct {
	placementFilter
	chance int
}

func (r *RarityFilter) shouldPlace(_ PlacementContext, rng levelgen.RandomSource, _ BlockPos) bool {
	return rng.NextFloat() < 1.0/float32(r.chance)
}

func newRarityFilter(chance int) *RarityFilter {
	r := &RarityFilter{chance: chance}
	r.placementFilter.predicate = r
	return r
}

// ---- biome ----

// BiomeFilter ports BiomeFilter (extends PlacementFilter): keep p iff the
// configured feature is allowed in the biome AT the candidate pos (re-checked per
// position — prevents a feature seeded in biome A from spilling into biome B).
// JAR-CONFIRMED: shouldPlace reads level.getBiome(pos) then checks
// biome.generationSettings.hasFeature(topFeature). Consumes 0 rng draws.
//
// For 11-02's self-contained design the allowed-biome check is a func(biome.Type)
// bool field (the placed_feature's biome allowance), supplied at bind time by
// 11-03/11-01. A nil predicate keeps every position (vanilla's BiomeFilter always
// has a registered feature; the nil-permissive default only matters to the
// in-package fold tests).
type BiomeFilter struct {
	placementFilter
	allowed func(biome.Type) bool
}

func (b *BiomeFilter) shouldPlace(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) bool {
	if b.allowed == nil {
		return true
	}
	return b.allowed(ctx.BiomeAt(p.X, p.Y, p.Z))
}

func newBiomeFilter(allowed func(biome.Type) bool) *BiomeFilter {
	b := &BiomeFilter{allowed: allowed}
	b.placementFilter.predicate = b
	return b
}

// ---- height_range ----

// HeightRange ports HeightRangePlacement: replace the input Y with
// heightProvider.Sample(rng, ctx), keeping X/Z (BlockPos.atY). JAR-CONFIRMED: one
// HeightProvider draw; never empty.
type HeightRange struct {
	height heightProvider
}

func (h HeightRange) getPositions(ctx PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	y := h.height.sample(rng, ctx)
	return []BlockPos{{X: p.X, Y: y, Z: p.Z}}
}

// ---- surface_water_depth_filter ----

// SurfaceWaterDepthFilter ports SurfaceWaterDepthFilter (extends PlacementFilter):
// keep p iff the water-column depth at (x,z) <= maxWaterDepth.
//
// JAR-CONFIRMED: the depth is NOT a block scan — it is the difference of two
// heightmap reads: depth = GetHeight(WORLD_SURFACE,x,z) - GetHeight(OCEAN_FLOOR,x,z)
// (worldSurface counts the water top; oceanFloor is the solid floor beneath it).
// Consumes 0 rng draws. (Research described a block-scan; the bytecode is the
// authoritative heightmap-difference form ported here.)
type SurfaceWaterDepthFilter struct {
	placementFilter
	maxWaterDepth int
}

func (s *SurfaceWaterDepthFilter) shouldPlace(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) bool {
	oceanFloor := ctx.GetHeight(OceanFloor, p.X, p.Z)
	worldSurface := ctx.GetHeight(WorldSurface, p.X, p.Z)
	return worldSurface-oceanFloor <= s.maxWaterDepth
}

func newSurfaceWaterDepthFilter(maxWaterDepth int) *SurfaceWaterDepthFilter {
	s := &SurfaceWaterDepthFilter{maxWaterDepth: maxWaterDepth}
	s.placementFilter.predicate = s
	return s
}

// ---- random_offset ----

// RandomOffset ports RandomOffsetPlacement (JAR-CONFIRMED javap -c getPositions):
// it scatters the input pos by sampling two IntProviders —
//
//	x = p.X + xzSpread.Sample(rng)   (draw 1)
//	y = p.Y + ySpread.Sample(rng)    (draw 2)
//	z = p.Z + xzSpread.Sample(rng)   (draw 3)
//
// THREE draws in order x, y, z; xz_spread is sampled TWICE (x then z), y_spread once.
// The spreads are IntProviders, dominantly the trapezoid (62/75 random_offset
// occurrences). It emits exactly one offset position (never empty).
type RandomOffset struct {
	xzSpread *intProvider
	ySpread  *intProvider
}

func (r RandomOffset) getPositions(_ PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	x := p.X + r.xzSpread.Sample(rng)
	y := p.Y + r.ySpread.Sample(rng)
	z := p.Z + r.xzSpread.Sample(rng)
	return []BlockPos{{X: x, Y: y, Z: z}}
}

// ---- block_predicate_filter ----

// BlockPredicateFilter ports BlockPredicateFilter (extends PlacementFilter): keep p
// iff the bound BlockPredicate holds at p (BlockPredicateFilter.shouldPlace evaluates
// predicate.test(level, pos)). 0 rng draws (predicates are positional).
type BlockPredicateFilter struct {
	placementFilter
	predicateImpl BlockPredicate
}

func (b *BlockPredicateFilter) shouldPlace(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) bool {
	return b.predicateImpl.Test(ctx, p.X, p.Y, p.Z)
}

func newBlockPredicateFilter(pred BlockPredicate) *BlockPredicateFilter {
	f := &BlockPredicateFilter{predicateImpl: pred}
	f.placementFilter.predicate = f
	return f
}

// ---- count_on_every_layer ----

// CountOnEveryLayer ports CountOnEveryLayerPlacement (extends PlacementModifier).
// getPositions (JAR-CONFIRMED javap -c): for layer = 0, 1, 2, ... it draws
// count.sample(rng) times PER layer; each draw picks a random (x,z) within the 16x16
// chunk (nextInt(16)+X, then nextInt(16)+Z), reads the MOTION_BLOCKING height there, and
// walks DOWN from that height with findOnGroundYPosition to find the layer-th
// air-over-solid boundary. A found boundary (!= MAX_INT) emits {x, y, z} and marks the
// layer non-empty; the outer layer loop continues WHILE the last layer produced >=1
// position (a layer with zero hits stops the whole modifier).
//
// Draw order per outer layer: count.Sample(rng) is called ONCE at the loop condition
// (RepeatingPlacement is NOT the base here -- this is a plain PlacementModifier), then
// per inner iteration two nextInt(16) draws (x then z). findOnGroundYPosition draws 0.
type CountOnEveryLayer struct {
	countProvider *intProvider
}

// isEmptyForLayer ports CountOnEveryLayerPlacement.isEmpty(BlockState): air, water, or
// lava (the scan treats a fluid cell as "open" so it can find the solid boundary beneath).
func isEmptyForLayer(s block.StateID) bool {
	return block.IsAir(s) || block.IsWaterBlock(s) || isLavaBlock(s)
}

// findOnGroundYPosition ports CountOnEveryLayerPlacement.findOnGroundYPosition(ctx, x,
// startY, z, layerIndex). Walking DOWN from startY: at each step it looks at (state below,
// state at cursor); when the below block is NON-empty AND the current cursor block is
// empty (an air-over-solid boundary) AND that below block is not bedrock, it counts a
// boundary. When the counted boundary index equals layerIndex it returns cursorY+1;
// otherwise it keeps descending until minY+1. If no matching boundary is found it returns
// MAX_INT (Integer.MAX_VALUE), the sentinel getPositions filters on.
func findOnGroundYPosition(ctx PlacementContext, x, startY, z, targetLayer int) int {
	countedLayers := 0
	// prev = state at the initial cursor (startY); the loop reads the block BELOW then
	// shifts prev down, matching the jar's blockState2 = ...; ... blockState2 = blockState.
	prev := ctx.GetBlock(x, startY, z)
	for cy := startY; cy >= ctx.MinY()+1; cy-- {
		below := ctx.GetBlock(x, cy-1, z)
		if !isEmptyForLayer(below) && isEmptyForLayer(prev) && !isBedrock(below) {
			if countedLayers == targetLayer {
				return cy + 1
			}
			countedLayers++
		}
		prev = below
	}
	return math.MaxInt32
}

func (c CountOnEveryLayer) getPositions(ctx PlacementContext, rng levelgen.RandomSource, p BlockPos) []BlockPos {
	var out []BlockPos
	layer := 0
	nonEmpty := true
	for nonEmpty {
		nonEmpty = false
		for i := 0; i < c.countProvider.Sample(rng); i++ {
			x := int(rng.NextIntN(16)) + p.X
			z := int(rng.NextIntN(16)) + p.Z
			top := ctx.GetHeight(MotionBlocking, x, z)
			y := findOnGroundYPosition(ctx, x, top, z, layer)
			if y != math.MaxInt32 {
				out = append(out, BlockPos{X: x, Y: y, Z: z})
				nonEmpty = true
			}
		}
		layer++
	}
	return out
}

func newCountOnEveryLayer(p *intProvider) CountOnEveryLayer {
	return CountOnEveryLayer{countProvider: p}
}

// ---- environment_scan ----

// EnvironmentScan ports EnvironmentScanPlacement (extends PlacementModifier).
// getPositions (JAR-CONFIRMED javap -c): starting at p, if allowedSearchCondition fails
// at the origin -> empty. Else scan up to maxSteps in directionOfSearch: at each cursor
// if targetCondition holds -> emit {cursor}; else move one step, bail empty if the new
// cursor is outside build height, then require allowedSearchCondition to still hold to
// keep scanning (else break). After the loop, one final targetCondition test at the last
// cursor -> emit it or empty. 0 rng draws (predicates are positional).
type EnvironmentScan struct {
	direction     scanDirection
	target        BlockPredicate
	allowedSearch BlockPredicate
	maxSteps      int
}

// scanDirection is the vertical search axis (net.minecraft.core.Direction, restricted to
// the VERTICAL_CODEC set the codec allows: UP / DOWN). move applies one step to Y.
type scanDirection int

const (
	scanUp scanDirection = iota
	scanDown
)

func (c EnvironmentScan) getPositions(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) []BlockPos {
	x, y, z := p.X, p.Y, p.Z
	if !c.allowedSearch.Test(ctx, x, y, z) {
		return nil
	}
	for i := 0; i < c.maxSteps; i++ {
		if c.target.Test(ctx, x, y, z) {
			return []BlockPos{{X: x, Y: y, Z: z}}
		}
		// move(direction): UP -> y+1, DOWN -> y-1.
		if c.direction == scanUp {
			y++
		} else {
			y--
		}
		// WorldGenLevel.isOutsideBuildHeight(y): y < minY || y >= minY+genDepth.
		if y < ctx.MinY() || y >= ctx.MinY()+ctx.Height() {
			return nil
		}
		if !c.allowedSearch.Test(ctx, x, y, z) {
			break
		}
	}
	if c.target.Test(ctx, x, y, z) {
		return []BlockPos{{X: x, Y: y, Z: z}}
	}
	return nil
}

// ---- noise_threshold_count ----

// NoiseThresholdCount ports NoiseThresholdCountPlacement (extends RepeatingPlacement).
// count(rng, pos) (JAR-CONFIRMED javap -c): sample BIOME_INFO_NOISE at (x/200, z/200) and
// return belowNoise if the value < noiseLevel, else aboveNoise. 0 rng draws (positional
// noise). The RepeatingPlacement base then emits that many copies of pos.
type NoiseThresholdCount struct {
	repeatingPlacement
	noiseLevel float64
	belowNoise int
	aboveNoise int
}

func (c *NoiseThresholdCount) count(_ levelgen.RandomSource, p BlockPos) int {
	v := biomeInfoNoiseValue(float64(p.X)/200.0, float64(p.Z)/200.0)
	if v < c.noiseLevel {
		return c.belowNoise
	}
	return c.aboveNoise
}

func newNoiseThresholdCount(noiseLevel float64, below, above int) *NoiseThresholdCount {
	c := &NoiseThresholdCount{noiseLevel: noiseLevel, belowNoise: below, aboveNoise: above}
	c.repeatingPlacement.counter = c
	return c
}

// ---- noise_based_count ----

// NoiseBasedCount ports NoiseBasedCountPlacement (extends RepeatingPlacement).
// count(rng, pos) (JAR-CONFIRMED javap -c): v = BIOME_INFO_NOISE.getValue(x/noiseFactor,
// z/noiseFactor, false); return (int) Math.ceil((v + noiseOffset) * noiseToCountRatio).
// 0 rng draws (positional noise). The d2i cast truncates the ceil'd double toward zero.
type NoiseBasedCount struct {
	repeatingPlacement
	noiseToCountRatio int
	noiseFactor       float64
	noiseOffset       float64
}

func (c *NoiseBasedCount) count(_ levelgen.RandomSource, p BlockPos) int {
	v := biomeInfoNoiseValue(float64(p.X)/c.noiseFactor, float64(p.Z)/c.noiseFactor)
	return int(math.Ceil((v + c.noiseOffset) * float64(c.noiseToCountRatio)))
}

func newNoiseBasedCount(ratio int, factor, offset float64) *NoiseBasedCount {
	c := &NoiseBasedCount{noiseToCountRatio: ratio, noiseFactor: factor, noiseOffset: offset}
	c.repeatingPlacement.counter = c
	return c
}

// ---- surface_relative_threshold_filter ----

// SurfaceRelativeThresholdFilter ports SurfaceRelativeThresholdFilter (extends
// PlacementFilter). shouldPlace (JAR-CONFIRMED javap -c): h = getHeight(heightmap, x, z);
// keep iff (h + minInclusive) <= pos.Y <= (h + maxInclusive). Vanilla widens each term to
// long to avoid overflow with the MIN_INT/MAX_INT defaults; int64 comparisons preserve
// that here. 0 rng draws.
type SurfaceRelativeThresholdFilter struct {
	placementFilter
	heightmap    HeightmapType
	minInclusive int
	maxInclusive int
}

func (s *SurfaceRelativeThresholdFilter) shouldPlace(ctx PlacementContext, _ levelgen.RandomSource, p BlockPos) bool {
	h := int64(ctx.GetHeight(s.heightmap, p.X, p.Z))
	lo := h + int64(s.minInclusive)
	hi := h + int64(s.maxInclusive)
	y := int64(p.Y)
	return lo <= y && y <= hi
}

func newSurfaceRelativeThresholdFilter(hm HeightmapType, minInc, maxInc int) *SurfaceRelativeThresholdFilter {
	f := &SurfaceRelativeThresholdFilter{heightmap: hm, minInclusive: minInc, maxInclusive: maxInc}
	f.placementFilter.predicate = f
	return f
}

// ---- fixed_placement ----

// FixedPlacement ports FixedPlacement (extends PlacementModifier). getPositions
// (JAR-CONFIRMED javap -c): if ANY configured position shares the section (chunk) column
// with pos, return the SUBSET of positions in pos's section; else empty. isSameChunk
// compares blockToSectionCoord (x >> 4) of the position vs pos. 0 rng draws.
type FixedPlacement struct {
	positions []BlockPos
}

// blockToSectionCoord ports SectionPos.blockToSectionCoord(int): arithmetic shift x >> 4.
func blockToSectionCoord(v int) int { return v >> 4 }

func (f FixedPlacement) getPositions(_ PlacementContext, _ levelgen.RandomSource, p BlockPos) []BlockPos {
	sx := blockToSectionCoord(p.X)
	sz := blockToSectionCoord(p.Z)
	any := false
	for _, q := range f.positions {
		if blockToSectionCoord(q.X) == sx && blockToSectionCoord(q.Z) == sz {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	var out []BlockPos
	for _, q := range f.positions {
		if blockToSectionCoord(q.X) == sx && blockToSectionCoord(q.Z) == sz {
			out = append(out, q)
		}
	}
	return out
}

// ---- compile-time interface assertions ----

var (
	_ PlacementModifier = InSquare{}
	_ PlacementModifier = Heightmap{}
	_ PlacementModifier = (*Count)(nil)
	_ PlacementModifier = (*RarityFilter)(nil)
	_ PlacementModifier = (*BiomeFilter)(nil)
	_ PlacementModifier = HeightRange{}
	_ PlacementModifier = (*SurfaceWaterDepthFilter)(nil)
	_ PlacementModifier = RandomOffset{}
	_ PlacementModifier = (*BlockPredicateFilter)(nil)
	_ PlacementModifier = CountOnEveryLayer{}
	_ PlacementModifier = EnvironmentScan{}
	_ PlacementModifier = (*NoiseThresholdCount)(nil)
	_ PlacementModifier = (*NoiseBasedCount)(nil)
	_ PlacementModifier = (*SurfaceRelativeThresholdFilter)(nil)
	_ PlacementModifier = FixedPlacement{}
)

// isLavaBlock ports BlockState.is(Blocks.LAVA) -- the strict LAVA block identity, matching
// KelpBlock/IsWaterBlock precedent (a distinct block type, not a waterlogged/flowing proxy).
func isLavaBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[s].(block.Lava)
	return ok
}

// isBedrock ports BlockState.is(Blocks.BEDROCK) -- the CountOnEveryLayer boundary guard so
// the layer scan never counts the world-floor bedrock as ground.
func isBedrock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:bedrock"
}

// ---- JSON binding ----

// parseHeightmapType maps the placed_feature `heightmap` string to a HeightmapType,
// erroring loudly on an unknown id.
func parseHeightmapType(s string) (HeightmapType, error) {
	switch s {
	case "WORLD_SURFACE_WG":
		return WorldSurfaceWG, nil
	case "OCEAN_FLOOR_WG":
		return OceanFloorWG, nil
	case "MOTION_BLOCKING":
		return MotionBlocking, nil
	case "MOTION_BLOCKING_NO_LEAVES":
		return MotionBlockingNoLeaves, nil
	case "WORLD_SURFACE":
		return WorldSurface, nil
	case "OCEAN_FLOOR":
		return OceanFloor, nil
	default:
		return 0, fmt.Errorf("placement: unsupported heightmap type %q", s)
	}
}

// ModifierDeps carries the per-placed-feature dependencies a modifier needs at bind
// time that are NOT in its own JSON. Currently only the biome filter's allowed-biome
// predicate (supplied by 11-03 from the placed_feature's configured-feature biome
// allowance). A nil predicate makes the biome filter permissive.
type ModifierDeps struct {
	BiomeAllowed func(biome.Type) bool
}

// BindModifier turns one parsed PlacementModifierRaw envelope (type + raw config)
// into a concrete PlacementModifier. 11-03 calls this per modifier when binding a
// placed_feature. It errors loudly on an unported modifier type (the Nether/cave set
// is deferred per research) — never silently dropping a modifier (T-11-05).
func BindModifier(modType string, raw json.RawMessage, deps ModifierDeps) (PlacementModifier, error) {
	switch modType {
	case "minecraft:in_square", "in_square":
		return InSquare{}, nil

	case "minecraft:heightmap", "heightmap":
		var cfg struct {
			Heightmap string `json:"heightmap"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: heightmap modifier: %w", err)
		}
		ht, err := parseHeightmapType(cfg.Heightmap)
		if err != nil {
			return nil, err
		}
		return Heightmap{heightmap: ht}, nil

	case "minecraft:count", "count":
		var cfg struct {
			Count json.RawMessage `json:"count"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: count modifier: %w", err)
		}
		ip, err := parseIntProvider(cfg.Count)
		if err != nil {
			return nil, fmt.Errorf("placement: count modifier: %w", err)
		}
		return newCount(ip), nil

	case "minecraft:rarity_filter", "rarity_filter":
		var cfg struct {
			Chance int `json:"chance"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: rarity_filter modifier: %w", err)
		}
		if cfg.Chance <= 0 {
			return nil, fmt.Errorf("placement: rarity_filter chance must be positive, got %d", cfg.Chance)
		}
		return newRarityFilter(cfg.Chance), nil

	case "minecraft:biome", "biome":
		return newBiomeFilter(deps.BiomeAllowed), nil

	case "minecraft:height_range", "height_range":
		var cfg struct {
			Height json.RawMessage `json:"height"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: height_range modifier: %w", err)
		}
		hp, err := parseHeightProvider(cfg.Height)
		if err != nil {
			return nil, fmt.Errorf("placement: height_range modifier: %w", err)
		}
		return HeightRange{height: hp}, nil

	case "minecraft:surface_water_depth_filter", "surface_water_depth_filter":
		var cfg struct {
			MaxWaterDepth int `json:"max_water_depth"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: surface_water_depth_filter modifier: %w", err)
		}
		return newSurfaceWaterDepthFilter(cfg.MaxWaterDepth), nil

	case "minecraft:random_offset", "random_offset":
		// 11-02 deferred random_offset (its xz/y spreads needed the trapezoid
		// IntProvider, ported in this plan). RandomOffsetPlacement draws x,y,z.
		var cfg struct {
			XZSpread json.RawMessage `json:"xz_spread"`
			YSpread  json.RawMessage `json:"y_spread"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: random_offset modifier: %w", err)
		}
		xz, err := parseIntProvider(cfg.XZSpread)
		if err != nil {
			return nil, fmt.Errorf("placement: random_offset xz_spread: %w", err)
		}
		y, err := parseIntProvider(cfg.YSpread)
		if err != nil {
			return nil, fmt.Errorf("placement: random_offset y_spread: %w", err)
		}
		return RandomOffset{xzSpread: xz, ySpread: y}, nil

	case "minecraft:block_predicate_filter", "block_predicate_filter":
		var cfg struct {
			Predicate json.RawMessage `json:"predicate"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: block_predicate_filter modifier: %w", err)
		}
		pred, err := ParsePredicate(cfg.Predicate)
		if err != nil {
			return nil, fmt.Errorf("placement: block_predicate_filter predicate: %w", err)
		}
		return newBlockPredicateFilter(pred), nil

	case "minecraft:count_on_every_layer", "count_on_every_layer":
		// CountOnEveryLayerPlacement: `count` is an IntProvider (bare int accepted).
		var cfg struct {
			Count json.RawMessage `json:"count"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: count_on_every_layer modifier: %w", err)
		}
		ip, err := parseIntProvider(cfg.Count)
		if err != nil {
			return nil, fmt.Errorf("placement: count_on_every_layer modifier: %w", err)
		}
		return newCountOnEveryLayer(ip), nil

	case "minecraft:environment_scan", "environment_scan":
		// EnvironmentScanPlacement: direction_of_search (VERTICAL: up/down), target_condition
		// (BlockPredicate), allowed_search_condition (optional BlockPredicate, default alwaysTrue),
		// max_steps (int, codec range [1,32]).
		var cfg struct {
			DirectionOfSearch      string          `json:"direction_of_search"`
			TargetCondition        json.RawMessage `json:"target_condition"`
			AllowedSearchCondition json.RawMessage `json:"allowed_search_condition"`
			MaxSteps               int             `json:"max_steps"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: environment_scan modifier: %w", err)
		}
		dir, err := parseScanDirection(cfg.DirectionOfSearch)
		if err != nil {
			return nil, err
		}
		target, err := ParsePredicate(cfg.TargetCondition)
		if err != nil {
			return nil, fmt.Errorf("placement: environment_scan target_condition: %w", err)
		}
		// allowed_search_condition defaults to BlockPredicate.alwaysTrue() when absent.
		var allowed BlockPredicate = truePredicate{}
		if len(cfg.AllowedSearchCondition) > 0 {
			allowed, err = ParsePredicate(cfg.AllowedSearchCondition)
			if err != nil {
				return nil, fmt.Errorf("placement: environment_scan allowed_search_condition: %w", err)
			}
		}
		if cfg.MaxSteps < 1 || cfg.MaxSteps > 32 {
			return nil, fmt.Errorf("placement: environment_scan max_steps %d out of codec range [1,32]", cfg.MaxSteps)
		}
		return EnvironmentScan{direction: dir, target: target, allowedSearch: allowed, maxSteps: cfg.MaxSteps}, nil

	case "minecraft:noise_threshold_count", "noise_threshold_count":
		// NoiseThresholdCountPlacement: noise_level (double), below_noise (int), above_noise (int).
		var cfg struct {
			NoiseLevel float64 `json:"noise_level"`
			BelowNoise int     `json:"below_noise"`
			AboveNoise int     `json:"above_noise"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: noise_threshold_count modifier: %w", err)
		}
		return newNoiseThresholdCount(cfg.NoiseLevel, cfg.BelowNoise, cfg.AboveNoise), nil

	case "minecraft:noise_based_count", "noise_based_count":
		// NoiseBasedCountPlacement: noise_to_count_ratio (int), noise_factor (double),
		// noise_offset (optional double, default 0.0 per the codec optionalFieldOf).
		var cfg struct {
			NoiseToCountRatio int      `json:"noise_to_count_ratio"`
			NoiseFactor       float64  `json:"noise_factor"`
			NoiseOffset       *float64 `json:"noise_offset"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: noise_based_count modifier: %w", err)
		}
		offset := 0.0
		if cfg.NoiseOffset != nil {
			offset = *cfg.NoiseOffset
		}
		return newNoiseBasedCount(cfg.NoiseToCountRatio, cfg.NoiseFactor, offset), nil

	case "minecraft:surface_relative_threshold_filter", "surface_relative_threshold_filter":
		// SurfaceRelativeThresholdFilter: heightmap (Heightmap$Types), min_inclusive (optional
		// int, default Integer.MIN_VALUE), max_inclusive (optional int, default Integer.MAX_VALUE).
		var cfg struct {
			Heightmap    string `json:"heightmap"`
			MinInclusive *int   `json:"min_inclusive"`
			MaxInclusive *int   `json:"max_inclusive"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: surface_relative_threshold_filter modifier: %w", err)
		}
		hm, err := parseHeightmapType(cfg.Heightmap)
		if err != nil {
			return nil, err
		}
		minInc := math.MinInt32
		if cfg.MinInclusive != nil {
			minInc = *cfg.MinInclusive
		}
		maxInc := math.MaxInt32
		if cfg.MaxInclusive != nil {
			maxInc = *cfg.MaxInclusive
		}
		return newSurfaceRelativeThresholdFilter(hm, minInc, maxInc), nil

	case "minecraft:fixed_placement", "fixed_placement":
		// FixedPlacement: positions is a list of [x,y,z] BlockPos arrays.
		var cfg struct {
			Positions [][3]int `json:"positions"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("placement: fixed_placement modifier: %w", err)
		}
		pts := make([]BlockPos, 0, len(cfg.Positions))
		for _, p := range cfg.Positions {
			pts = append(pts, BlockPos{X: p[0], Y: p[1], Z: p[2]})
		}
		return FixedPlacement{positions: pts}, nil

	default:
		return nil, fmt.Errorf("placement: unported placement modifier type %q", modType)
	}
}

// parseScanDirection maps the environment_scan direction_of_search string (VERTICAL_CODEC:
// only up/down are valid) to a scanDirection, erroring loudly on any other value.
func parseScanDirection(s string) (scanDirection, error) {
	switch s {
	case "up":
		return scanUp, nil
	case "down":
		return scanDown, nil
	default:
		return 0, fmt.Errorf("placement: environment_scan direction_of_search %q is not vertical (up/down)", s)
	}
}
