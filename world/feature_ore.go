package world

// feature_ore.go ports the OreFeature body — the UNDERGROUND_ORES decoration blob
// (net.minecraft.world.level.levelgen.feature.OreFeature, javap -c against
// temp/cache/26.2-inner.jar). It is DISTINCT from v1's noise OreVeinifier
// (research Pitfall #7): both exist in vanilla. OreFeature places the common
// scattered coal/iron/copper/etc. ore blobs of OreConfiguration{size, targets,
// discard_chance_on_air_exposure}; the noise OreVeinifier is the separate large-vein
// router path. This body registers under "ore" via registerFeatureBody from init().
//
// The blob math (place -> doPlace) is transcribed FROM THE BYTECODE; the exact rng
// draw sequence (the angle nextFloat, the two endpoint nextInt(3) y-jitters, the
// per-step nextDouble radius, the per-candidate discard-on-air nextFloat) IS the
// determinism contract — a reorder corrupts the blob shape AND the post-place rng
// state (T-12-05). Every write goes through bodyContext.placeState -> Neighborhood.
// SetBlock, so a blob straddling a chunk edge spills into the neighbor (T-12-08).
//
// Source mapping (all javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.OreFeature.place / doPlace /
//     canPlaceOre / shouldSkipAirCheck
//   - net.minecraft.world.level.levelgen.feature.Feature.isAdjacentToAir /
//     checkNeighbors (the 6-neighbor air scan)
//   - net.minecraft.world.level.levelgen.structure.templatesystem.{TagMatchTest,
//     BlockMatchTest,AlwaysTrueTest,RandomBlockMatchTest}.test (the RuleTest set)

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("ore", oreBody) }

// ---- OreConfiguration decode ----

// oreRuleTest is one RuleTest.test(existingState, rng) the ore target uses to decide
// whether the existing block at a candidate may be replaced. It returns whether the
// rule matches; tag_match / block_match / always_true take ZERO rng draws, while
// random_block_match / random_block_state_match take ONE nextFloat draw IFF the block
// matches (jar-exact, RandomBlockMatchTest.test).
type oreRuleTest func(existing block.StateID, rng levelgen.RandomSource) bool

// oreTarget is one OreConfiguration.TargetBlockState: the state to place + the
// RuleTest gating which existing block it replaces (the FIRST matching target wins).
type oreTarget struct {
	state block.StateID
	rule  oreRuleTest
}

// oreConfig is the decoded OreConfiguration: the blob size (point count along the
// lerp line), the target list, and the discard-on-air-exposure probability.
type oreConfig struct {
	size                       int
	discardChanceOnAirExposure float32
	targets                    []oreTarget
}

// jsonOreConfig is the on-disk OreConfiguration JSON shape (verified ore_coal.json).
type jsonOreConfig struct {
	Size                       int     `json:"size"`
	DiscardChanceOnAirExposure float32 `json:"discard_chance_on_air_exposure"`
	Targets                    []struct {
		State  json.RawMessage `json:"state"`
		Target json.RawMessage `json:"target"`
	} `json:"targets"`
}

// jsonRuleTest is the RuleTest envelope (predicate_type + the per-type fields).
type jsonRuleTest struct {
	PredicateType string          `json:"predicate_type"`
	Tag           string          `json:"tag"`         // tag_match
	Block         string          `json:"block"`       // block_match / random_block_match
	BlockState    json.RawMessage `json:"block_state"` // blockstate_match / random_blockstate_match
	Probability   float32         `json:"probability"` // random_block_match / random_blockstate_match
}

// decodeOreConfig decodes an OreConfiguration from the configured feature's raw config
// JSON, resolving each target.state to a StateID (via resolveOreState) and each
// target.target RuleTest to an oreRuleTest. An unported predicate_type errors LOUDLY
// (never a silent mis-target — T-12-07).
// oreConfigCache memoizes the decoded oreConfig per ConfiguredFeature. decodeOreConfig
// re-resolves the replaceable tag set by scanning all ~30K block states (resolveOreTagSet),
// which is far too expensive to run per ore placement (a chunk places many ore blobs, and
// the 3x3 decoration neighborhood multiplies it). Keying by the *ConfiguredFeature pointer
// is sound: the parser DAG returns one stable instance per feature id (registry dedup), and
// its config is immutable after parse. A sync.Map keeps it -race clean regardless of caller.
var oreConfigCache sync.Map // map[*feature.ConfiguredFeature]*oreConfig

// decodeOreConfigCached returns the memoized decoded config for cf, decoding (and caching)
// it on first use. The decode is pure over the config bytes, so the cached value is shared.
func decodeOreConfigCached(cf *feature.ConfiguredFeature) (*oreConfig, error) {
	if v, ok := oreConfigCache.Load(cf); ok {
		return v.(*oreConfig), nil
	}
	cfg, err := decodeOreConfig(configRaw(cf))
	if err != nil {
		return nil, err
	}
	oreConfigCache.Store(cf, cfg)
	return cfg, nil
}

func decodeOreConfig(raw json.RawMessage) (*oreConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("world: ore config is empty")
	}
	var j jsonOreConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("world: ore config: %w", err)
	}
	if j.Size <= 0 {
		return nil, fmt.Errorf("world: ore config has non-positive size %d", j.Size)
	}
	if len(j.Targets) == 0 {
		return nil, fmt.Errorf("world: ore config has no targets")
	}
	cfg := &oreConfig{size: j.Size, discardChanceOnAirExposure: j.DiscardChanceOnAirExposure}
	for i, t := range j.Targets {
		sid, err := resolveOreState(t.State)
		if err != nil {
			return nil, fmt.Errorf("world: ore target %d state: %w", i, err)
		}
		rule, err := parseOreRuleTest(t.Target)
		if err != nil {
			return nil, fmt.Errorf("world: ore target %d rule: %w", i, err)
		}
		cfg.targets = append(cfg.targets, oreTarget{state: sid, rule: rule})
	}
	return cfg, nil
}

// resolveOreState resolves a {Name,Properties} target state to a StateID through the
// feature package's resolver (the single shared block-state resolution path).
func resolveOreState(raw json.RawMessage) (block.StateID, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing state")
	}
	return feature.ResolveBlockStateJSON(raw)
}

// stripNS strips the "minecraft:" namespace so a predicate_type dispatch is
// namespace-agnostic (mirrors feature.stripNS, which is package-private).
func stripNS(t string) string {
	if i := indexByte(t, ':'); i >= 0 {
		return t[i+1:]
	}
	return t
}

// indexByte is a tiny strings.IndexByte to avoid an extra import in this file.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// parseOreRuleTest builds the oreRuleTest for a target's RuleTest envelope. The
// overworld ores use tag_match (43x) and block_match (6x); always_true,
// blockstate_match, random_block_match, and random_blockstate_match are ported for
// completeness (the full vanilla RuleTest set). Draw counts are jar-exact.
func parseOreRuleTest(raw json.RawMessage) (oreRuleTest, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing rule test")
	}
	var j jsonRuleTest
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("decoding rule test: %w", err)
	}
	switch stripNS(j.PredicateType) {
	case "tag_match":
		// TagMatchTest.test: existing.is(tag) — 0 draws. Resolve the tag to a StateID
		// set constant-for-constant (the carver Replaceables precedent).
		set, err := resolveOreTagSet(j.Tag)
		if err != nil {
			return nil, err
		}
		return func(existing block.StateID, _ levelgen.RandomSource) bool { return set[existing] }, nil
	case "block_match":
		// BlockMatchTest.test: existing.is(block) — 0 draws. Matches ALL states of the
		// named block.
		set, err := resolveOreBlockSet(j.Block)
		if err != nil {
			return nil, err
		}
		return func(existing block.StateID, _ levelgen.RandomSource) bool { return set[existing] }, nil
	case "blockstate_match":
		// BlockStateMatchTest.test: existing == the exact state — 0 draws.
		want, err := feature.ResolveBlockStateJSON(j.BlockState)
		if err != nil {
			return nil, fmt.Errorf("blockstate_match: %w", err)
		}
		return func(existing block.StateID, _ levelgen.RandomSource) bool { return existing == want }, nil
	case "always_true":
		// AlwaysTrueTest.test: true — 0 draws.
		return func(block.StateID, levelgen.RandomSource) bool { return true }, nil
	case "random_block_match":
		// RandomBlockMatchTest.test: existing.is(block) && nextFloat() < probability.
		// The nextFloat draw happens ONLY when the block matches (short-circuit).
		set, err := resolveOreBlockSet(j.Block)
		if err != nil {
			return nil, err
		}
		prob := j.Probability
		return func(existing block.StateID, rng levelgen.RandomSource) bool {
			if !set[existing] {
				return false
			}
			return rng.NextFloat() < prob
		}, nil
	case "random_blockstate_match":
		// RandomBlockStateMatchTest.test: existing == state && nextFloat() < probability.
		want, err := feature.ResolveBlockStateJSON(j.BlockState)
		if err != nil {
			return nil, fmt.Errorf("random_blockstate_match: %w", err)
		}
		prob := j.Probability
		return func(existing block.StateID, rng levelgen.RandomSource) bool {
			if existing != want {
				return false
			}
			return rng.NextFloat() < prob
		}, nil
	default:
		return nil, fmt.Errorf("world: unported ore rule test predicate_type %q "+
			"(add it constant-for-constant from the jar RuleTest def)", j.PredicateType)
	}
}

// oreReplaceableTags maps the #block tags the overworld+nether ore targets reference
// to their member block ids, resolved constant-for-constant from the vanilla 26.2
// block-tag defs (data/minecraft/tags/block/*.json), the carver nestedReplaceableTags
// precedent. Cited inline. An unlisted tag errors loudly (a jar bump that adds one
// fails rather than drifting).
var oreReplaceableTags = map[string][]string{
	// #minecraft:stone_ore_replaceables (verified jar tag def).
	"minecraft:stone_ore_replaceables": {
		"minecraft:stone", "minecraft:granite", "minecraft:diorite", "minecraft:andesite",
	},
	// #minecraft:deepslate_ore_replaceables (verified jar tag def).
	"minecraft:deepslate_ore_replaceables": {
		"minecraft:deepslate", "minecraft:tuff",
	},
	// #minecraft:base_stone_overworld (verified jar tag def; matches the carver's copy).
	"minecraft:base_stone_overworld": {
		"minecraft:stone", "minecraft:granite", "minecraft:diorite",
		"minecraft:andesite", "minecraft:tuff", "minecraft:deepslate",
	},
	// #minecraft:base_stone_nether (verified jar tag def).
	"minecraft:base_stone_nether": {
		"minecraft:netherrack", "minecraft:basalt", "minecraft:blackstone",
	},
}

// resolveOreTagSet resolves a #block tag id to the set of all member state ids. Every
// state of each member block is included (so a deepslate axis variant the fill placed
// is still replaceable), matching BlockState.is(tag) which is property-agnostic.
func resolveOreTagSet(tag string) (map[block.StateID]bool, error) {
	if tag == "" {
		return nil, fmt.Errorf("tag_match missing tag")
	}
	members, ok := oreReplaceableTags[tag]
	if !ok {
		return nil, fmt.Errorf("world: unported ore replaceable tag %q "+
			"(add it constant-for-constant from the jar tag def, carver precedent)", tag)
	}
	ids := make(map[string]bool, len(members))
	for _, m := range members {
		ids[m] = true
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if ids[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("world: ore tag %q resolved to an empty set", tag)
	}
	return set, nil
}

// resolveOreBlockSet resolves a single block id to the set of all its state ids
// (BlockMatchTest.is(block) is property-agnostic).
func resolveOreBlockSet(id string) (map[block.StateID]bool, error) {
	if id == "" {
		return nil, fmt.Errorf("block_match missing block")
	}
	if _, ok := block.FromID[id]; !ok {
		return nil, fmt.Errorf("world: block_match unknown block %q", id)
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if b.ID() == id {
			set[block.StateID(sid)] = true
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("world: block_match block %q resolved to an empty set", id)
	}
	return set, nil
}

// ---- OreFeature.place -> doPlace (the blob) ----

// oreBody ports OreFeature.place. It decodes the OreConfiguration, computes the blob
// line endpoints (jar draw order: 1 angle nextFloat, then 2 y-jitter nextInt(3)),
// then runs the doPlace ellipsoid fill. Every write is via bctx.placeState (the 3x3
// proxy). Returns true iff at least one block was placed.
func oreBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg, err := decodeOreConfigCached(cf)
	if err != nil {
		// A config decode failure is a build-data error; loudly panic so it surfaces in
		// generation rather than silently placing nothing (T-12-07).
		panic(err)
	}

	// place(): f = nextFloat() * PI (the blob angle — 1 draw).
	f := rng.NextFloat() * float32(math.Pi)
	// g = size/8.0 (the horizontal half-length of the lerp line).
	g := float32(cfg.size) / 8.0
	// i = ceil((size/16.0 * 2 + 1) / 2) (the radius padding in cells).
	i := mthCeil((float32(cfg.size)/16.0*2.0 + 1.0) / 2.0)

	px, pz := float64(pos.X), float64(pos.Z)
	sinf := math.Sin(float64(f))
	cosf := math.Cos(float64(f))
	gd := float64(g)
	// Line endpoints (x0,y0,z0)-(x1,y1,z1): X +/- sin*g, Z +/- cos*g; the two Y ends
	// each get pos.Y + nextInt(3) - 2 (the y-jitter draws, IN THIS ORDER).
	x0 := px + sinf*gd
	x1 := px - sinf*gd
	z0 := pz + cosf*gd
	z1 := pz - cosf*gd
	y0 := float64(pos.Y + int(rng.NextIntN(3)) - 2)
	y1 := float64(pos.Y + int(rng.NextIntN(3)) - 2)

	// The bounding corner + extents (jar: minX = X - ceil(g) - i, etc.).
	minX := pos.X - mthCeil(g) - i
	minY := pos.Y - 2 - i
	minZ := pos.Z - mthCeil(g) - i
	width := 2 * (mthCeil(g) + i)
	heightSpan := 2 * (2 + i)

	// OCEAN_FLOOR_WG guard (place's bbox-column scan): place loops every column
	// (x in [minX, minX+w1], z in [minZ, minZ+w1]) and at the FIRST column whose
	// OCEAN_FLOOR_WG height is >= minY (i.e. minY NOT above the floor) it runs doPlace
	// ONCE and returns. The column coords are NOT passed to doPlace (it always uses
	// minX/minZ). If NO column is low enough the feature places nothing. No rng draws
	// in this scan (the angle/y-jitter draws already happened above).
	belowFloor := false
	for cx := minX; cx <= minX+width && !belowFloor; cx++ {
		for cz := minZ; cz <= minZ+width; cz++ {
			if minY <= ctx.GetHeight(placement.OceanFloorWG, cx, cz) {
				belowFloor = true
				break
			}
		}
	}
	if !belowFloor {
		return false
	}

	// heightSpan is the jar's w2 bbox extent; it is only used to size the BitSet, which
	// here is a position-keyed map, so it is not threaded into doPlace.
	_ = heightSpan
	return oreDoPlace(bctx, ctx, rng, cfg, x0, x1, y0, y1, z0, z1, minX, minY, minZ)
}

// oreDoPlace ports OreFeature.doPlace: it lays `size` points along the lerp line, each
// with a per-step radius (1 nextDouble draw per point), then for every cell inside any
// point's ellipsoid (dedup'd via a visited set) reads the existing block and writes
// the first matching target's state (canPlaceOre). Returns true iff >=1 block placed.
func oreDoPlace(
	bctx *bodyContext,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	cfg *oreConfig,
	x0, x1, y0, y1, z0, z1 float64,
	minX, minY, minZ int,
) bool {
	placedCount := 0
	size := cfg.size

	// The per-point [x,y,z,radius] table (jar: double[size*4]).
	type orePoint struct{ x, y, z, r float64 }
	pts := make([]orePoint, size)
	for k := 0; k < size; k++ {
		t := float32(k) / float32(size)
		x := mthLerp(float64(t), x0, x1)
		y := mthLerp(float64(t), y0, y1)
		z := mthLerp(float64(t), z0, z1)
		// d = nextDouble() * size / 16.0  (the per-step base radius — 1 draw).
		d := rng.NextDouble() * float64(size) / 16.0
		// r = ((sin(PI*t) + 1) * d + 1) / 2  (the radius envelope).
		r := ((float64(mthSinF(float32(math.Pi)*t)) + 1.0) * d + 1.0) / 2.0
		pts[k] = orePoint{x: x, y: y, z: z, r: r}
	}

	// Prune dominated points (jar: the O(size^2) pass that zeroes a point's radius when
	// another point fully contains it — sets r = -1 to skip it).
	for k := 0; k < size-1; k++ {
		if pts[k].r <= 0 {
			continue
		}
		for l := k + 1; l < size; l++ {
			if pts[l].r <= 0 {
				continue
			}
			dx := pts[k].x - pts[l].x
			dy := pts[k].y - pts[l].y
			dz := pts[k].z - pts[l].z
			dr := pts[k].r - pts[l].r
			if dr*dr > dx*dx+dy*dy+dz*dz {
				if dr > 0 {
					pts[l].r = -1
				} else {
					pts[k].r = -1
				}
			}
		}
	}

	// visited dedups cells across overlapping ellipsoids (jar: a BitSet over the bbox,
	// keyed (bx-minX) + (by-minY)*w1 + (bz-minZ)*w1*w2). A position-keyed set is an
	// equivalent injective dedup (collision-proof regardless of how far a large-radius
	// ellipsoid runs past the bbox extents) and keeps the placement set jar-identical.
	visited := make(map[placement.BlockPos]bool)

	for k := 0; k < size; k++ {
		r := pts[k].r
		if r < 0 {
			continue
		}
		cx, cy, cz := pts[k].x, pts[k].y, pts[k].z
		x0i := max(mthFloor(cx-r), minX)
		y0i := max(mthFloor(cy-r), minY)
		z0i := max(mthFloor(cz-r), minZ)
		x1i := max(mthFloor(cx+r), x0i)
		y1i := max(mthFloor(cy+r), y0i)
		z1i := max(mthFloor(cz+r), z0i)

		for bx := x0i; bx <= x1i; bx++ {
			dxr := (float64(bx) + 0.5 - cx) / r
			if dxr*dxr >= 1.0 {
				continue
			}
			for by := y0i; by <= y1i; by++ {
				dyr := (float64(by) + 0.5 - cy) / r
				if dxr*dxr+dyr*dyr >= 1.0 {
					continue
				}
				for bz := z0i; bz <= z1i; bz++ {
					dzr := (float64(bz) + 0.5 - cz) / r
					if dxr*dxr+dyr*dyr+dzr*dzr >= 1.0 {
						continue
					}
					// isOutsideBuildHeight(by): the Neighborhood drops out-of-Y writes,
					// but match the jar's explicit guard so an out-of-range cell takes no
					// rng draws.
					if by < ctx.MinY() || by >= ctx.MinY()+ctx.Height() {
						continue
					}
					// BitSet dedup (position-keyed, see visited above).
					cell := placement.BlockPos{X: bx, Y: by, Z: bz}
					if visited[cell] {
						continue
					}
					visited[cell] = true

					existing := bctx.getState(cell)
					for _, tgt := range cfg.targets {
						if oreCanPlace(bctx, existing, rng, cfg, tgt, bx, by, bz) {
							bctx.placeState(cell, tgt.state)
							placedCount++
							break
						}
					}
				}
			}
		}
	}
	return placedCount > 0
}

// oreCanPlace ports OreFeature.canPlaceOre: the target's RuleTest must match the
// existing block (its own draws), then the discard-on-air roll
// (shouldSkipAirCheck): if it fires the candidate is kept (skips the air check);
// otherwise the candidate is kept only when NOT adjacent to air. The nextFloat for the
// discard roll happens ONLY when 0 < discard < 1 (shouldSkipAirCheck short-circuits at
// the 0 and 1 extremes), jar-exact.
func oreCanPlace(
	bctx *bodyContext,
	existing block.StateID,
	rng levelgen.RandomSource,
	cfg *oreConfig,
	tgt oreTarget,
	x, y, z int,
) bool {
	if !tgt.rule(existing, rng) {
		return false
	}
	if oreShouldSkipAirCheck(rng, cfg.discardChanceOnAirExposure) {
		return true
	}
	return !oreIsAdjacentToAir(bctx, x, y, z)
}

// oreShouldSkipAirCheck ports OreFeature.shouldSkipAirCheck: discard<=0 -> true (always
// skip the air check, i.e. never discard); discard>=1 -> false (always do the air
// check); else nextFloat() >= discard (1 draw). NOTE the jar returns nextFloat >=
// discard, so a LOW roll (< discard) does the air check (and thus may discard).
func oreShouldSkipAirCheck(rng levelgen.RandomSource, discard float32) bool {
	if discard <= 0 {
		return true
	}
	if discard >= 1 {
		return false
	}
	return rng.NextFloat() >= discard
}

// oreIsAdjacentToAir ports Feature.isAdjacentToAir/checkNeighbors: true iff any of the
// 6 axis neighbors is air. checkNeighbors iterates Direction.values() (DOWN, UP, NORTH,
// SOUTH, WEST, EAST) — the order is irrelevant here (any-match), but the 6 offsets are
// exact. No rng draws.
func oreIsAdjacentToAir(bctx *bodyContext, x, y, z int) bool {
	dirs := [6][3]int{
		{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0},
	}
	for _, d := range dirs {
		if block.IsAir(bctx.getState(placement.BlockPos{X: x + d[0], Y: y + d[1], Z: z + d[2]})) {
			return true
		}
	}
	return false
}

// ---- small Mth helpers (jar-exact rounding) ----

// mthCeil ports Mth.ceil(float): (int)Math.ceil.
func mthCeil(f float32) int { return int(math.Ceil(float64(f))) }

// mthFloor ports Mth.floor(double): (int)Math.floor.
func mthFloor(d float64) int { return int(math.Floor(d)) }

// mthLerp ports Mth.lerp(t, a, b): a + t*(b-a).
func mthLerp(t, a, b float64) float64 { return a + t*(b-a) }

// mthSinF ports Mth.sin(float)->float: Minecraft's Mth.sin is a table lookup in
// vanilla, but for the ore radius envelope the precision difference is immaterial to
// the cell membership (the radius is rounded into integer cells); use the exact sin.
func mthSinF(f float32) float32 { return float32(math.Sin(float64(f))) }

// configRaw returns the raw config JSON for a configured feature (nil-safe).
func configRaw(cf *feature.ConfiguredFeature) json.RawMessage {
	if cf == nil || cf.Config == nil {
		return nil
	}
	return cf.Config.Raw
}
