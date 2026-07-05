package world

// feature_geode.go ports GeodeFeature 1:1 from the unobfuscated Minecraft 26.2 jar
// (temp/cache/26.2-inner.jar, read via CFR / javap -c):
//
//   - net.minecraft.world.level.levelgen.feature.GeodeFeature.place
//   - net.minecraft.world.level.levelgen.feature.configurations.GeodeConfiguration (record + codec defaults)
//   - net.minecraft.world.level.levelgen.GeodeBlockSettings / GeodeLayerSettings / GeodeCrackSettings
//   - net.minecraft.world.level.block.BuddingAmethystBlock.canClusterGrowAtState
//   - net.minecraft.util.Mth.invSqrt (= org.joml.Math.invsqrt(double) = 1.0/Math.sqrt)
//   - net.minecraft.util.Util.getRandom(List, RandomSource) = list.get(rng.nextInt(list.size()))
//
// The amethyst geode is a distance-field shell: `distributionPoints` scattered anchor points
// define an implicit surface via a sum of inverse-sqrt distances plus a per-cell NormalNoise
// wobble; each cell inside minGenOffset..maxGenOffset is classified into filling / inner /
// middle / outer layers by thresholds derived from GeodeLayerSettings. Registers under
// "geode" via registerFeatureBody in init() (was an unregistered no-op before).
//
// DETERMINISM CONTRACT (Pitfall 4) — the exact draw sequence from GeodeFeature.place:
//   1. numPoints = distributionPoints.sample(random)          [UniformInt(3,4): 1 draw]
//   2. the per-level NormalNoise is seeded from a SEPARATE rng (new WorldgenRandom(new
//      LegacyRandomSource(level.getSeed()))) — it consumes NO draws from `random`.
//   3. crackSize base:   random.nextDouble()                  [1 draw]
//   4. shouldGenerateCrack: random.nextFloat()                [1 draw]
//   5. for each point:  outerWallDistance.sample ×3 (x,y,z) + pointOffset.sample
//                       [UniformInt: 3 + 1 = 4 draws per point]
//   6. if shouldGenerateCrack: random.nextInt(4)              [the crack offsetIndex]
//   7. shell loop (x-inner, y-mid, z-outer per BlockPos.betweenClosed): per inner-layer cell
//      random.nextFloat() (useAlternateLayer0) then possibly random.nextFloat()
//      (usePotentialPlacements).
//   8. crystal placements: Util.getRandom(innerPlacements, random) per candidate [1 draw each].
//
// Every write goes through bctx.placeState (Neighborhood.SetBlock — cross-chunk + live
// worldgen heightmaps). The canReplace / invalid / cannot_replace gates read the live view.

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

func init() { registerFeatureBody("geode", geodeBody) }

// ---- GeodeConfiguration decode (with codec defaults) ----

// geodeConfig is the decoded GeodeConfiguration. All optional fields carry the exact codec
// defaults (GeodeConfiguration.CODEC): use_potential_placements_chance=0.35,
// use_alternate_layer0_chance=0.0, placements_require_layer0_alternate=true,
// outer_wall_distance=UniformInt(4,5), distribution_points=UniformInt(3,4),
// point_offset=UniformInt(1,2), min_gen_offset=-16, max_gen_offset=16, noise_multiplier=0.05.
// Layer defaults: filling=1.7, inner=2.2, middle=3.2, outer=4.2. Crack defaults:
// generate_crack_chance=1.0, base_crack_size=2.0, crack_point_offset=2.
type geodeConfig struct {
	filling         feature.BlockStateProvider
	innerLayer      feature.BlockStateProvider
	alternateInner  feature.BlockStateProvider
	middleLayer     feature.BlockStateProvider
	outerLayer      feature.BlockStateProvider
	innerPlacements []block.StateID
	cannotReplace   map[block.StateID]bool
	invalidBlocks   map[block.StateID]bool

	usePotentialPlacementsChance float64
	useAlternateLayer0Chance     float64
	placementsRequireLayer0Alt   bool
	outerWallDistance            geodeIntProvider
	distributionPoints           geodeIntProvider
	pointOffset                  geodeIntProvider
	minGenOffset                 int
	maxGenOffset                 int
	noiseMultiplier              float64
	invalidBlocksThreshold       int

	layerFilling float64
	layerInner   float64
	layerMiddle  float64
	layerOuter   float64

	crackGenerateChance float64
	crackBaseSize       float64
	crackPointOffset    int

	err error
}

// jsonGeodeConfig is the on-disk GeodeConfiguration shape (verified amethyst_geode.json).
// Optional numeric fields are pointers so an absent field falls back to the codec default.
type jsonGeodeConfig struct {
	Blocks struct {
		FillingProvider             json.RawMessage   `json:"filling_provider"`
		InnerLayerProvider          json.RawMessage   `json:"inner_layer_provider"`
		AlternateInnerLayerProvider json.RawMessage   `json:"alternate_inner_layer_provider"`
		MiddleLayerProvider         json.RawMessage   `json:"middle_layer_provider"`
		OuterLayerProvider          json.RawMessage   `json:"outer_layer_provider"`
		InnerPlacements             []json.RawMessage `json:"inner_placements"`
		CannotReplace               string            `json:"cannot_replace"`
		InvalidBlocks               string            `json:"invalid_blocks"`
	} `json:"blocks"`
	Layers struct {
		Filling *float64 `json:"filling"`
		Inner   *float64 `json:"inner_layer"`
		Middle  *float64 `json:"middle_layer"`
		Outer   *float64 `json:"outer_layer"`
	} `json:"layers"`
	Crack struct {
		GenerateCrackChance *float64 `json:"generate_crack_chance"`
		BaseCrackSize       *float64 `json:"base_crack_size"`
		CrackPointOffset    *int     `json:"crack_point_offset"`
	} `json:"crack"`
	UsePotentialPlacementsChance *float64        `json:"use_potential_placements_chance"`
	UseAlternateLayer0Chance     *float64        `json:"use_alternate_layer0_chance"`
	PlacementsRequireLayer0Alt   *bool           `json:"placements_require_layer0_alternate"`
	OuterWallDistance            json.RawMessage `json:"outer_wall_distance"`
	DistributionPoints           json.RawMessage `json:"distribution_points"`
	PointOffset                  json.RawMessage `json:"point_offset"`
	MinGenOffset                 *int            `json:"min_gen_offset"`
	MaxGenOffset                 *int            `json:"max_gen_offset"`
	NoiseMultiplier              *float64        `json:"noise_multiplier"`
	InvalidBlocksThreshold       int             `json:"invalid_blocks_threshold"`
}

// geodeConfigCache memoizes the decoded config per ConfiguredFeature (oreConfigCache
// pattern): the tag resolutions scan all block states, far too costly per placement.
var geodeConfigCache sync.Map // map[*feature.ConfiguredFeature]*geodeConfig

func decodeGeodeCached(cf *feature.ConfiguredFeature) *geodeConfig {
	if v, ok := geodeConfigCache.Load(cf); ok {
		return v.(*geodeConfig)
	}
	d := decodeGeodeConfig(configRaw(cf), cf.ID)
	geodeConfigCache.Store(cf, d)
	return d
}

func decodeGeodeConfig(raw json.RawMessage, id string) *geodeConfig {
	d := &geodeConfig{
		// Codec defaults.
		usePotentialPlacementsChance: 0.35,
		useAlternateLayer0Chance:     0.0,
		placementsRequireLayer0Alt:   true,
		outerWallDistance:            geodeIntProvider{constant: false, min: 4, max: 5}, // UniformInt(4,5)
		distributionPoints:           geodeIntProvider{constant: false, min: 3, max: 4}, // UniformInt(3,4)
		pointOffset:                  geodeIntProvider{constant: false, min: 1, max: 2}, // UniformInt(1,2)
		minGenOffset:                 -16,
		maxGenOffset:                 16,
		noiseMultiplier:              0.05,
		layerFilling:                 1.7,
		layerInner:                   2.2,
		layerMiddle:                  3.2,
		layerOuter:                   4.2,
		crackGenerateChance:          1.0,
		crackBaseSize:                2.0,
		crackPointOffset:             2,
	}
	var j jsonGeodeConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		d.err = fmt.Errorf("world: geode config %q: %w", id, err)
		return d
	}

	providers := []struct {
		raw json.RawMessage
		dst *feature.BlockStateProvider
	}{
		{j.Blocks.FillingProvider, &d.filling},
		{j.Blocks.InnerLayerProvider, &d.innerLayer},
		{j.Blocks.AlternateInnerLayerProvider, &d.alternateInner},
		{j.Blocks.MiddleLayerProvider, &d.middleLayer},
		{j.Blocks.OuterLayerProvider, &d.outerLayer},
	}
	for _, p := range providers {
		prov, err := feature.ParseProvider(p.raw)
		if err != nil {
			d.err = fmt.Errorf("world: geode provider %q: %w", id, err)
			return d
		}
		*p.dst = prov
	}
	for i, ip := range j.Blocks.InnerPlacements {
		sid, err := feature.ResolveBlockStateJSON(ip)
		if err != nil {
			d.err = fmt.Errorf("world: geode inner_placement %d %q: %w", i, id, err)
			return d
		}
		d.innerPlacements = append(d.innerPlacements, sid)
	}
	if len(d.innerPlacements) == 0 {
		d.err = fmt.Errorf("world: geode %q has empty inner_placements", id)
		return d
	}

	cannot, err := geodeResolveBlockTag(j.Blocks.CannotReplace)
	if err != nil {
		d.err = fmt.Errorf("world: geode cannot_replace %q: %w", id, err)
		return d
	}
	d.cannotReplace = cannot
	invalid, err := geodeResolveBlockTag(j.Blocks.InvalidBlocks)
	if err != nil {
		d.err = fmt.Errorf("world: geode invalid_blocks %q: %w", id, err)
		return d
	}
	d.invalidBlocks = invalid

	// Optional numeric overrides.
	if j.UsePotentialPlacementsChance != nil {
		d.usePotentialPlacementsChance = *j.UsePotentialPlacementsChance
	}
	if j.UseAlternateLayer0Chance != nil {
		d.useAlternateLayer0Chance = *j.UseAlternateLayer0Chance
	}
	if j.PlacementsRequireLayer0Alt != nil {
		d.placementsRequireLayer0Alt = *j.PlacementsRequireLayer0Alt
	}
	if p, err := parseGeodeIntProvider(j.OuterWallDistance); err != nil {
		d.err = fmt.Errorf("world: geode outer_wall_distance %q: %w", id, err)
		return d
	} else if p != nil {
		d.outerWallDistance = *p
	}
	if p, err := parseGeodeIntProvider(j.DistributionPoints); err != nil {
		d.err = fmt.Errorf("world: geode distribution_points %q: %w", id, err)
		return d
	} else if p != nil {
		d.distributionPoints = *p
	}
	if p, err := parseGeodeIntProvider(j.PointOffset); err != nil {
		d.err = fmt.Errorf("world: geode point_offset %q: %w", id, err)
		return d
	} else if p != nil {
		d.pointOffset = *p
	}
	if j.MinGenOffset != nil {
		d.minGenOffset = *j.MinGenOffset
	}
	if j.MaxGenOffset != nil {
		d.maxGenOffset = *j.MaxGenOffset
	}
	if j.NoiseMultiplier != nil {
		d.noiseMultiplier = *j.NoiseMultiplier
	}
	d.invalidBlocksThreshold = j.InvalidBlocksThreshold

	if j.Layers.Filling != nil {
		d.layerFilling = *j.Layers.Filling
	}
	if j.Layers.Inner != nil {
		d.layerInner = *j.Layers.Inner
	}
	if j.Layers.Middle != nil {
		d.layerMiddle = *j.Layers.Middle
	}
	if j.Layers.Outer != nil {
		d.layerOuter = *j.Layers.Outer
	}
	if j.Crack.GenerateCrackChance != nil {
		d.crackGenerateChance = *j.Crack.GenerateCrackChance
	}
	if j.Crack.BaseCrackSize != nil {
		d.crackBaseSize = *j.Crack.BaseCrackSize
	}
	if j.Crack.CrackPointOffset != nil {
		d.crackPointOffset = *j.Crack.CrackPointOffset
	}
	return d
}

// ---- geode body (GeodeFeature.place) ----

// geodePoint is one distribution anchor: its world position + its per-point offset (added
// to the distance in the shell sum).
type geodePoint struct {
	x, y, z int
	offset  int
}

// geodeBody ports GeodeFeature.place. Draw order is documented at the file head and mirrored
// exactly. Returns true when the geode generated, false when it aborted on too many invalid
// anchor points (matching the bytecode's early `return false`).
func geodeBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg := decodeGeodeCached(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}

	minGenOffset := cfg.minGenOffset
	maxGenOffset := cfg.maxGenOffset

	// numPoints = distributionPoints.sample(random).
	numPoints := cfg.distributionPoints.sample(rng)

	// noise = NormalNoise.create(new WorldgenRandom(new LegacyRandomSource(level.getSeed())),
	// -4, 1.0). Seeded from the WORLD SEED (a separate rng — no draws from `rng`). Cached
	// per-seed globally (pure over the seed).
	noise := geodeLevelNoise(bctx.worldSeed())

	crackSizeAdjustment := float64(numPoints) / float64(cfg.outerWallDistance.maxInclusive())

	innerAir := 1.0 / math.Sqrt(cfg.layerFilling)
	innermostBlockLayer := 1.0 / math.Sqrt(cfg.layerInner+crackSizeAdjustment)
	innerCrust := 1.0 / math.Sqrt(cfg.layerMiddle+crackSizeAdjustment)
	outerCrust := 1.0 / math.Sqrt(cfg.layerOuter+crackSizeAdjustment)

	// crackSize base draw: random.nextDouble()/2.0; the +crackSizeAdjustment only when
	// numPoints > 3.
	crackExtra := 0.0
	if numPoints > 3 {
		crackExtra = crackSizeAdjustment
	}
	crackSize := 1.0 / math.Sqrt(cfg.crackBaseSize+rng.NextDouble()/2.0+crackExtra)
	// shouldGenerateCrack = random.nextFloat() < generateCrackChance.
	shouldGenerateCrack := float64(rng.NextFloat()) < cfg.crackGenerateChance

	// Anchor point loop: 3 outerWallDistance draws (x,y,z) + 1 pointOffset per point; abort
	// when > invalidBlocksThreshold anchors land in air / invalid blocks.
	points := make([]geodePoint, 0, numPoints)
	numInvalidPoints := 0
	for i := 0; i < numPoints; i++ {
		x := cfg.outerWallDistance.sample(rng)
		y := cfg.outerWallDistance.sample(rng)
		z := cfg.outerWallDistance.sample(rng)
		p := placement.BlockPos{X: origin.X + x, Y: origin.Y + y, Z: origin.Z + z}
		st := bctx.getState(p)
		if block.IsAir(st) || cfg.invalidBlocks[st] {
			numInvalidPoints++
			if numInvalidPoints > cfg.invalidBlocksThreshold {
				return false
			}
		}
		off := cfg.pointOffset.sample(rng)
		points = append(points, geodePoint{x: p.X, y: p.Y, z: p.Z, offset: off})
	}

	// Crack points (a small vertical stack in one of 4 corners), chosen with random.nextInt(4).
	var crackPoints []placement.BlockPos
	if shouldGenerateCrack {
		offsetIndex := int(rng.NextIntN(4))
		crackOffset := numPoints*2 + 1
		switch offsetIndex {
		case 0:
			crackPoints = append(crackPoints,
				placement.BlockPos{X: origin.X + crackOffset, Y: origin.Y + 7, Z: origin.Z},
				placement.BlockPos{X: origin.X + crackOffset, Y: origin.Y + 5, Z: origin.Z},
				placement.BlockPos{X: origin.X + crackOffset, Y: origin.Y + 1, Z: origin.Z})
		case 1:
			crackPoints = append(crackPoints,
				placement.BlockPos{X: origin.X, Y: origin.Y + 7, Z: origin.Z + crackOffset},
				placement.BlockPos{X: origin.X, Y: origin.Y + 5, Z: origin.Z + crackOffset},
				placement.BlockPos{X: origin.X, Y: origin.Y + 1, Z: origin.Z + crackOffset})
		case 2:
			crackPoints = append(crackPoints,
				placement.BlockPos{X: origin.X + crackOffset, Y: origin.Y + 7, Z: origin.Z + crackOffset},
				placement.BlockPos{X: origin.X + crackOffset, Y: origin.Y + 5, Z: origin.Z + crackOffset},
				placement.BlockPos{X: origin.X + crackOffset, Y: origin.Y + 1, Z: origin.Z + crackOffset})
		default:
			crackPoints = append(crackPoints,
				placement.BlockPos{X: origin.X, Y: origin.Y + 7, Z: origin.Z},
				placement.BlockPos{X: origin.X, Y: origin.Y + 5, Z: origin.Z},
				placement.BlockPos{X: origin.X, Y: origin.Y + 1, Z: origin.Z})
		}
	}

	var potentialCrystalPlacements []placement.BlockPos

	// Shell loop: BlockPos.betweenClosed(origin+min, origin+max) — cursor order is x-inner,
	// y-mid, z-outer (the index = x + width*(y + height*z) decomposition), so the loops are
	// z outer, y mid, x inner.
	for cz := minGenOffset; cz <= maxGenOffset; cz++ {
		for cy := minGenOffset; cy <= maxGenOffset; cy++ {
			for cx := minGenOffset; cx <= maxGenOffset; cx++ {
				pi := placement.BlockPos{X: origin.X + cx, Y: origin.Y + cy, Z: origin.Z + cz}
				noiseOffset := noise.GetValue(float64(pi.X), float64(pi.Y), float64(pi.Z)) * cfg.noiseMultiplier

				distSumShell := 0.0
				for _, p := range points {
					distSumShell += geodeInvSqrt(geodeDistSqr(pi, p.x, p.y, p.z)+float64(p.offset)) + noiseOffset
				}
				distSumCrack := 0.0
				for _, cp := range crackPoints {
					distSumCrack += geodeInvSqrt(geodeDistSqr(pi, cp.X, cp.Y, cp.Z)+float64(cfg.crackPointOffset)) + noiseOffset
				}

				if distSumShell < outerCrust {
					continue
				}
				if shouldGenerateCrack && distSumCrack >= crackSize && distSumShell < innerAir {
					// AIR + schedule fluid ticks on adjacent fluids (we place air; the fluid
					// re-tick is a runtime schedule with no worldgen effect on the emitted
					// chunk, so it is a no-op here — documented).
					bctx.geodeSafeSet(cfg, pi, bctx.airState())
					continue
				}
				if distSumShell >= innerAir {
					bctx.geodeSafeSet(cfg, pi, cfg.filling.GetState(rng, pi.X, pi.Y, pi.Z))
					continue
				}
				if distSumShell >= innermostBlockLayer {
					// useAlternateLayer0 = random.nextFloat() < useAlternateLayer0Chance.
					useAlternate := float64(rng.NextFloat()) < cfg.useAlternateLayer0Chance
					if useAlternate {
						bctx.geodeSafeSet(cfg, pi, cfg.alternateInner.GetState(rng, pi.X, pi.Y, pi.Z))
					} else {
						bctx.geodeSafeSet(cfg, pi, cfg.innerLayer.GetState(rng, pi.X, pi.Y, pi.Z))
					}
					// if (placementsRequireLayer0Alternate && !useAlternate) OR
					//    !(random.nextFloat() < usePotentialPlacementsChance): continue.
					if cfg.placementsRequireLayer0Alt && !useAlternate {
						continue
					}
					if !(float64(rng.NextFloat()) < cfg.usePotentialPlacementsChance) {
						continue
					}
					potentialCrystalPlacements = append(potentialCrystalPlacements, pi)
					continue
				}
				if distSumShell >= innerCrust {
					bctx.geodeSafeSet(cfg, pi, cfg.middleLayer.GetState(rng, pi.X, pi.Y, pi.Z))
					continue
				}
				if distSumShell >= outerCrust {
					bctx.geodeSafeSet(cfg, pi, cfg.outerLayer.GetState(rng, pi.X, pi.Y, pi.Z))
				}
			}
		}
	}

	// Crystal (bud/cluster) placement: for each candidate, pick a random inner_placement,
	// then try each of the 6 DIRECTIONS (DOWN,UP,NORTH,SOUTH,WEST,EAST) — setting FACING +
	// WATERLOGGED — placing at the FIRST direction whose relative block can grow a cluster.
	for _, crystalPos := range potentialCrystalPlacements {
		baseState := cfg.innerPlacements[int(rng.NextIntN(int32(len(cfg.innerPlacements))))]
		for _, dir := range geodeDirections {
			// setValue(FACING, direction) if the placement has a facing property.
			state := geodeSetFacing(baseState, dir.d)
			placePos := placement.BlockPos{X: crystalPos.X + dir.dx, Y: crystalPos.Y + dir.dy, Z: crystalPos.Z + dir.dz}
			placeState := bctx.getState(placePos)
			// setValue(WATERLOGGED, placeState.getFluidState().isSource()).
			state = geodeSetWaterlogged(state, bctx.isWaterSource(placeState))
			if !geodeCanClusterGrowAtState(bctx, placeState) {
				continue
			}
			bctx.geodeSafeSet(cfg, placePos, state)
			break
		}
	}
	return true
}

// geodeSafeSet ports Feature.safeSetBlock with the canReplace = !state.is(cannotReplace)
// predicate: place `st` only when the EXISTING block is not in #features_cannot_replace.
func (b *bodyContext) geodeSafeSet(cfg *geodeConfig, pos placement.BlockPos, st block.StateID) {
	if cfg.cannotReplace[b.getState(pos)] {
		return
	}
	b.placeState(pos, st)
}

// worldSeed returns the world seed recorded on the view (0 for a test view).
func (b *bodyContext) worldSeed() int64 {
	if b.view == nil {
		return 0
	}
	return b.view.worldSeed
}

// isWaterSource reads the resolved water state off the view. (airState is the shared
// helper defined in feature_body.go.)
//
// isWaterSource ports FluidState.isSource() for the worldgen view: true iff the state is a
// water source (block.Water{Level:0}). The geode uses it only to set the crystal's
// WATERLOGGED property (a source below/beside the cluster). Flowing water (level>0) is not a
// source. Lava is irrelevant to the WATERLOGGED bool.
func (b *bodyContext) isWaterSource(st block.StateID) bool {
	return st == geodeWaterSourceState
}

// ---- BuddingAmethystBlock.canClusterGrowAtState ----

// geodeCanClusterGrowAtState ports BuddingAmethystBlock.canClusterGrowAtState(state):
// state.isAir() || (state.is(WATER) && state.getFluidState().isFull()). In worldgen a water
// SOURCE block is full; there is no partial-water block placed pre-decoration, so "is water
// and full" reduces to "is a water source" for the states the view can hold.
func geodeCanClusterGrowAtState(_ *bodyContext, st block.StateID) bool {
	return block.IsAir(st) || st == geodeWaterSourceState
}

// ---- crystal FACING / WATERLOGGED reassignment ----

// geodeDir is one of the 6 vanilla Direction.values() in order DOWN,UP,NORTH,SOUTH,WEST,EAST
// with its unit offset (matching BlockPos.relative(direction)).
type geodeDir struct {
	d          block.Direction
	dx, dy, dz int
}

// geodeDirections is Direction.values() in enum order (the DIRECTIONS field the geode
// iterates). The crystal is placed at the FIRST direction whose relative block can grow.
var geodeDirections = [6]geodeDir{
	{block.Down, 0, -1, 0},
	{block.Up, 0, 1, 0},
	{block.North, 0, 0, -1},
	{block.South, 0, 0, 1},
	{block.West, -1, 0, 0},
	{block.East, 1, 0, 0},
}

// geodeSetFacing sets the FACING property of an inner-placement state to `dir` when the state
// is one of the amethyst bud / cluster blocks (which carry a `facing` Direction property).
// A state without a facing property is returned unchanged (matching hasProperty(FACING)).
func geodeSetFacing(st block.StateID, dir block.Direction) block.StateID {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return st
	}
	switch b := block.StateList[st].(type) {
	case block.SmallAmethystBud:
		b.Facing = dir
		return block.ToStateID[b]
	case block.MediumAmethystBud:
		b.Facing = dir
		return block.ToStateID[b]
	case block.LargeAmethystBud:
		b.Facing = dir
		return block.ToStateID[b]
	case block.AmethystCluster:
		b.Facing = dir
		return block.ToStateID[b]
	default:
		return st
	}
}

// geodeSetWaterlogged sets the WATERLOGGED property when the state carries it (the amethyst
// bud / cluster blocks do). A state without it is returned unchanged.
func geodeSetWaterlogged(st block.StateID, wet bool) block.StateID {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return st
	}
	switch b := block.StateList[st].(type) {
	case block.SmallAmethystBud:
		b.Waterlogged = block.Boolean(wet)
		return block.ToStateID[b]
	case block.MediumAmethystBud:
		b.Waterlogged = block.Boolean(wet)
		return block.ToStateID[b]
	case block.LargeAmethystBud:
		b.Waterlogged = block.Boolean(wet)
		return block.ToStateID[b]
	case block.AmethystCluster:
		b.Waterlogged = block.Boolean(wet)
		return block.ToStateID[b]
	default:
		return st
	}
}

// ---- distance-field math ----

// geodeDistSqr ports Vec3i.distToLowCornerSqr: (pi - p) squared, as doubles.
func geodeDistSqr(pi placement.BlockPos, x, y, z int) float64 {
	dx := float64(pi.X - x)
	dy := float64(pi.Y - y)
	dz := float64(pi.Z - z)
	return dx*dx + dy*dy + dz*dz
}

// geodeInvSqrt ports Mth.invSqrt(double) = org.joml.Math.invsqrt(double). JOML's DOUBLE
// invsqrt is exactly 1.0/Math.sqrt(x) (the fast-inverse bit hack is only in some FLOAT
// paths); it is NOT Mth.fastInvSqrt. So this is 1.0/sqrt, byte-identical to the jar.
func geodeInvSqrt(x float64) float64 { return 1.0 / math.Sqrt(x) }

// ---- per-level NormalNoise cache ----

// geodeNoiseCache caches the per-world-seed NormalNoise (GeodeFeature builds it fresh from
// `new WorldgenRandom(new LegacyRandomSource(level.getSeed()))` each place, but it depends
// only on the world seed, so it is memoized — pure over the seed, race-safe via sync.Map).
var geodeNoiseCache sync.Map // map[int64]*synth.NormalNoise

func geodeLevelNoise(seed int64) *synth.NormalNoise {
	if v, ok := geodeNoiseCache.Load(seed); ok {
		return v.(*synth.NormalNoise)
	}
	// new WorldgenRandom(new LegacyRandomSource(seed)) — the WorldgenRandom wraps a fresh
	// LegacyRandomSource seeded with the raw world seed; NormalNoise.create(rng, -4, 1.0)
	// then draws the two PerlinNoise from it in order.
	rng := levelgen.NewWorldgenRandom(seed)
	n := synth.NewNormalNoise(rng, -4, []float64{1.0})
	geodeNoiseCache.Store(seed, n)
	return n
}

// ---- geode IntProvider (constant + uniform — the only kinds geode uses) ----

// geodeIntProvider is the minimal IntProvider for the geode config: outer_wall_distance /
// distribution_points / point_offset are all UniformInt (or an inline constant int). A
// richer kind errors loudly at parse.
type geodeIntProvider struct {
	constant bool
	value    int
	min, max int
}

func (p geodeIntProvider) sample(rng levelgen.RandomSource) int {
	if p.constant {
		return p.value
	}
	// UniformInt.sample = Mth.randomBetweenInclusive = min + nextInt(max-min+1). 1 draw.
	return p.min + int(rng.NextIntN(int32(p.max-p.min+1)))
}

// maxInclusive is the config's outerWallDistance().maxInclusive() the crackSizeAdjustment
// divides by (the UniformInt upper bound, or the constant value).
func (p geodeIntProvider) maxInclusive() int {
	if p.constant {
		return p.value
	}
	return p.max
}

// parseGeodeIntProvider decodes a constant (bare int) or {"type":"uniform",min,max}. A nil
// raw returns (nil,nil) so the caller keeps its codec default.
func parseGeodeIntProvider(raw json.RawMessage) (*geodeIntProvider, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if jsonFirstByte(raw) != '{' {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("constant int: %w", err)
		}
		return &geodeIntProvider{constant: true, value: v}, nil
	}
	var obj struct {
		Type         string `json:"type"`
		Value        *int   `json:"value"`
		MinInclusive int    `json:"min_inclusive"`
		MaxInclusive int    `json:"max_inclusive"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("int provider: %w", err)
	}
	switch stripNS(obj.Type) {
	case "constant":
		v := 0
		if obj.Value != nil {
			v = *obj.Value
		}
		return &geodeIntProvider{constant: true, value: v}, nil
	case "uniform":
		if obj.MaxInclusive < obj.MinInclusive {
			return nil, fmt.Errorf("uniform int: max %d < min %d", obj.MaxInclusive, obj.MinInclusive)
		}
		return &geodeIntProvider{min: obj.MinInclusive, max: obj.MaxInclusive}, nil
	default:
		return nil, fmt.Errorf("world: unported geode IntProvider type %q", obj.Type)
	}
}

// ---- geode block-tag resolution (constant-for-constant from the jar tag defs) ----

// geodeResolveBlockTag resolves a #block tag id (cannot_replace / invalid_blocks) to its
// member state set. The two geode tags are flattened constant-for-constant from the jar
// tag defs (data/minecraft/tags/block/*.json, 26.2), the ore/carver precedent.
func geodeResolveBlockTag(tag string) (map[block.StateID]bool, error) {
	ids, ok := geodeTagBlockIDs[tag]
	if !ok {
		return nil, fmt.Errorf("world: unported geode block tag %q "+
			"(add it constant-for-constant from the jar tag def)", tag)
	}
	want := idSet(ids)
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set, nil
}

// geodeTagBlockIDs maps the geode block tags to their members (JAR-CONFIRMED, 26.2):
//
//	#features_cannot_replace = bedrock, spawner, chest, end_portal_frame,
//	                           reinforced_deepslate, trial_spawner, vault
//	#geode_invalid_blocks    = bedrock, water, lava, ice, packed_ice, blue_ice
var geodeTagBlockIDs = map[string][]string{
	"#minecraft:features_cannot_replace": {
		"minecraft:bedrock", "minecraft:spawner", "minecraft:chest",
		"minecraft:end_portal_frame", "minecraft:reinforced_deepslate",
		"minecraft:trial_spawner", "minecraft:vault",
	},
	"#minecraft:geode_invalid_blocks": {
		"minecraft:bedrock", "minecraft:water", "minecraft:lava",
		"minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice",
	},
}

// geodeWaterSourceState is the water-source state id (block.Water{Level:0}) the WATERLOGGED /
// canClusterGrow reads compare against.
var geodeWaterSourceState = block.ToStateID[block.Water{Level: 0}]
