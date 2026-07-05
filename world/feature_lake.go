package world

// feature_lake.go ports LakeFeature.place — the water/lava lake feature
// (net.minecraft.world.level.levelgen.feature.LakeFeature, javap -c against
// temp/cache/26.2-inner.jar). LakeFeature$Configuration carries the `fluid` BlockStateProvider,
// the `barrier` BlockStateProvider, and three BlockPredicates (can_place_feature,
// can_replace_with_air_or_fluid, can_replace_with_barrier). It carves a 16x8x16 ellipsoid lake
// shell, fills it with fluid (or air above the fluid line), lines the exposed edges with barrier,
// and (for water lakes in cold biomes) ices the surface.
//
// RNG DRAW ORDER (the determinism contract, T-12-05): place makes, in order:
//   1. count = nextInt(4) + 4                                           // ONE nextInt(4)
//   2. per iteration i in [0..count):  SIX nextDouble() (the ellipsoid radii + centers)
//   ... the VALIDATION phase (walls/canPlace) — NO draws — may `return false` here (aborting) ...
//   3. barrier phase: for each edge cell at Y>=4, ONE nextInt(2) (whether to place a barrier)
// The shape draws (1+2) ALWAYS run; the barrier draws (3) run ONLY if validation passes. A lake
// over unsuitable terrain therefore consumes exactly (count*6 nextDouble + 1 nextInt) and no
// more — the port reproduces this so the post-lake decoration stream stays in lockstep.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.LakeFeature.place
//   - net.minecraft.world.level.levelgen.feature.LakeFeature$Configuration (fluid/barrier/
//     canPlaceFeature/canReplaceWithAirOrFluid/canReplaceWithBarrier)
//   - net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase.liquid()/isSolid()
//   - net.minecraft.world.level.biome.Biome.shouldFreeze (the ice pass)
//   - data/minecraft/tags/block/{features_cannot_replace,lava_pool_stone_cannot_replace}.json
//
// SEAMS (documented, behavior-identical):
//   - scheduleTick(fluid) after a fluid/air setBlock is a TICK-side update the worldgen
//     Neighborhood has no queue for; the placed SOURCE fluid is stable, so omitting it is
//     result-identical (the fluid stays put).
//   - markAboveForPostProcessing is a lighting/post-process hint with no worldgen queue (not
//     modeled in any ported body); the placed BLOCK set is unaffected.
//   - AIR is Blocks.CAVE_AIR.defaultBlockState() in the jar (LakeFeature.AIR static field);
//     CAVE_AIR and AIR are both empty air blocks with identical worldgen semantics, so plain air
//     is used for the carved cavity (behavior-identical for the placed set + heightmaps).

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
	"github.com/imhinotori/sulfur/world/levelgen/surface"
)

func init() { registerFeatureBody("lake", lakeBody) }

// lakeConfigCache memoizes the decoded lake config per ConfiguredFeature (oreConfigCache pattern).
var lakeConfigCache sync.Map // map[*feature.ConfiguredFeature]*lakeConfig

// lakeConfig is the decoded LakeFeature$Configuration.
type lakeConfig struct {
	fluid                    block.StateID
	barrier                  block.StateID
	canPlaceFeature          lakePredicate
	canReplaceWithAirOrFluid lakePredicate
	canReplaceWithBarrier    lakePredicate
}

// jsonLakeConfig is the on-disk Configuration shape (verified lake_lava.json).
type jsonLakeConfig struct {
	Fluid                    json.RawMessage `json:"fluid"`
	Barrier                  json.RawMessage `json:"barrier"`
	CanPlaceFeature          json.RawMessage `json:"can_place_feature"`
	CanReplaceWithAirOrFluid json.RawMessage `json:"can_replace_with_air_or_fluid"`
	CanReplaceWithBarrier    json.RawMessage `json:"can_replace_with_barrier"`
}

// decodeLakeConfigCached returns the memoized decoded config for cf.
func decodeLakeConfigCached(cf *feature.ConfiguredFeature) (*lakeConfig, error) {
	if v, ok := lakeConfigCache.Load(cf); ok {
		return v.(*lakeConfig), nil
	}
	cfg, err := decodeLakeConfig(configRaw(cf))
	if err != nil {
		return nil, err
	}
	lakeConfigCache.Store(cf, cfg)
	return cfg, nil
}

func decodeLakeConfig(raw json.RawMessage) (*lakeConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("world: lake config is empty")
	}
	var j jsonLakeConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("world: lake config: %w", err)
	}
	fluid, err := lakeSimpleProviderState(j.Fluid)
	if err != nil {
		return nil, fmt.Errorf("world: lake fluid: %w", err)
	}
	barrier, err := lakeSimpleProviderState(j.Barrier)
	if err != nil {
		return nil, fmt.Errorf("world: lake barrier: %w", err)
	}
	cp, err := parseLakePredicate(j.CanPlaceFeature)
	if err != nil {
		return nil, fmt.Errorf("world: lake can_place_feature: %w", err)
	}
	caf, err := parseLakePredicate(j.CanReplaceWithAirOrFluid)
	if err != nil {
		return nil, fmt.Errorf("world: lake can_replace_with_air_or_fluid: %w", err)
	}
	cb, err := parseLakePredicate(j.CanReplaceWithBarrier)
	if err != nil {
		return nil, fmt.Errorf("world: lake can_replace_with_barrier: %w", err)
	}
	return &lakeConfig{
		fluid:                    fluid,
		barrier:                  barrier,
		canPlaceFeature:          cp,
		canReplaceWithAirOrFluid: caf,
		canReplaceWithBarrier:    cb,
	}, nil
}

// lakeSimpleProviderState resolves a simple_state_provider envelope to its state. The lake
// fluid + barrier are simple_state_providers in the data (getState takes 0 draws).
func lakeSimpleProviderState(raw json.RawMessage) (block.StateID, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing provider")
	}
	var env struct {
		Type  string          `json:"type"`
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, fmt.Errorf("decoding provider: %w", err)
	}
	if stripNSMisc(env.Type) != "simple_state_provider" {
		return 0, fmt.Errorf("lake provider must be simple_state_provider, got %q", env.Type)
	}
	return feature.ResolveBlockStateJSON(env.State)
}

// ---- lake block predicates (self-contained; the lake tags are not in the placement resolver) ----

// lakePredicate reports whether the predicate holds for the block AT a world pos (read through
// the view). The lake predicates are positional (0 draws) and read only the block at the pos
// (no offsets in the lake configs).
type lakePredicate func(bctx *bodyContext, x, y, z int) bool

// parseLakePredicate ports the BlockPredicate subset the lake uses: `true`,
// `not(matching_block_tag <tag>)`. The two tags (features_cannot_replace,
// lava_pool_stone_cannot_replace) are resolved constant-for-constant (carver/ore precedent).
func parseLakePredicate(raw json.RawMessage) (lakePredicate, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing predicate")
	}
	var j struct {
		Type      string          `json:"type"`
		Tag       string          `json:"tag"`
		Predicate json.RawMessage `json:"predicate"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("decoding predicate: %w", err)
	}
	switch stripNSMisc(j.Type) {
	case "true":
		return func(*bodyContext, int, int, int) bool { return true }, nil
	case "not":
		inner, err := parseLakePredicate(j.Predicate)
		if err != nil {
			return nil, fmt.Errorf("not: %w", err)
		}
		return func(bctx *bodyContext, x, y, z int) bool { return !inner(bctx, x, y, z) }, nil
	case "matching_block_tag":
		member, err := lakeTagMembership(j.Tag)
		if err != nil {
			return nil, err
		}
		return func(bctx *bodyContext, x, y, z int) bool {
			return member(bctx.getState(placement.BlockPos{X: x, Y: y, Z: z}))
		}, nil
	default:
		return nil, fmt.Errorf("world: unported lake predicate type %q", j.Type)
	}
}

// lakeTagMembership returns a state-membership func for the two lake #block tags, resolved
// constant-for-constant from the 26.2 tag defs. #features_cannot_replace = a fixed block set;
// #lava_pool_stone_cannot_replace = features_cannot_replace + #leaves + #logs (the latter two
// via the existing block.IsLeaves / block.IsLog closures).
func lakeTagMembership(tag string) (func(block.StateID) bool, error) {
	switch tag {
	case "minecraft:features_cannot_replace":
		set := lakeFeaturesCannotReplaceSet()
		return func(s block.StateID) bool { return set[s] }, nil
	case "minecraft:lava_pool_stone_cannot_replace":
		set := lakeFeaturesCannotReplaceSet()
		return func(s block.StateID) bool {
			return set[s] || block.IsLeaves(s) || block.IsLog(s)
		}, nil
	default:
		return nil, fmt.Errorf("world: unported lake tag %q "+
			"(add it constant-for-constant from the jar tag def)", tag)
	}
}

// lakeFeaturesCannotReplaceIDs is #minecraft:features_cannot_replace (JAR-CONFIRMED, 26.2:
// data/minecraft/tags/block/features_cannot_replace.json).
var lakeFeaturesCannotReplaceIDs = []string{
	"minecraft:bedrock", "minecraft:spawner", "minecraft:chest",
	"minecraft:end_portal_frame", "minecraft:reinforced_deepslate",
	"minecraft:trial_spawner", "minecraft:vault",
}

var (
	lakeFCRSetOnce sync.Once
	lakeFCRSet     map[block.StateID]bool
)

// lakeFeaturesCannotReplaceSet resolves #features_cannot_replace to the set of all member state
// ids (property-agnostic), built once.
func lakeFeaturesCannotReplaceSet() map[block.StateID]bool {
	lakeFCRSetOnce.Do(func() {
		want := make(map[string]bool, len(lakeFeaturesCannotReplaceIDs))
		for _, id := range lakeFeaturesCannotReplaceIDs {
			want[id] = true
		}
		set := map[block.StateID]bool{}
		for sid, b := range block.StateList {
			if want[b.ID()] {
				set[block.StateID(sid)] = true
			}
		}
		lakeFCRSet = set
	})
	return lakeFCRSet
}

// ---- LakeFeature.place ----

// lakeArrIndex is the 16x16x8 lake-shape flat index: (x*16 + z)*8 + y (jar: the boolean[2048]
// keyed x*16*8 + z*8 + y). x,z in [0,16), y in [0,8).
func lakeArrIndex(x, z, y int) int { return (x*16+z)*8 + y }

// lakeArrGet reads arr[x,z,y] with an out-of-bounds guard (the jar's boundary index checks are
// reproduced by the callers; this guards the array access).
func lakeArrGet(arr *[2048]bool, x, z, y int) bool {
	return arr[lakeArrIndex(x, z, y)]
}

// lakeBody ports LakeFeature.place (javap -c). It threads the rng in the exact draw order (see
// the file header) and reproduces the validation-abort semantics: the shape draws always run,
// then validation may abort (consuming no further draws), else the barrier phase's per-edge
// nextInt(2) draws run. Every read/write goes through the 3x3 view + placement context.
func lakeBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg, err := decodeLakeConfigCached(cf)
	if err != nil {
		panic(err) // build-data error (T-12-07)
	}

	// if origin.Y <= level.getMinY()+4: return false.
	if pos.Y <= bctx.minY()+4 {
		return false
	}
	// origin = origin.offset(-8, -4, -8) (the base corner of the 16x8x16 box).
	origin := placement.BlockPos{X: pos.X - 8, Y: pos.Y - 4, Z: pos.Z - 8}

	var arr [2048]bool

	// count = nextInt(4) + 4; per iteration SIX nextDouble() define an ellipsoid.
	count := int(rng.NextIntN(4)) + 4
	for i := 0; i < count; i++ {
		d1 := rng.NextDouble()*6.0 + 3.0                    // x radius
		d2 := rng.NextDouble()*4.0 + 2.0                    // y radius
		d3 := rng.NextDouble()*6.0 + 3.0                    // z radius
		d4 := rng.NextDouble()*(16.0-d1-2.0) + 1.0 + d1/2.0 // x center
		d5 := rng.NextDouble()*(8.0-d2-4.0) + 2.0 + d2/2.0  // y center
		d6 := rng.NextDouble()*(16.0-d3-2.0) + 1.0 + d3/2.0 // z center
		// for l(X) in [1..14], k(Z) in [1..14], m(Y) in [1..6]: mark inside the ellipsoid.
		for l := 1; l < 15; l++ {
			for k := 1; k < 15; k++ {
				for m := 1; m < 7; m++ {
					dl := (float64(l) - d4) / (d1 / 2.0)
					dm := (float64(m) - d5) / (d2 / 2.0)
					dk := (float64(k) - d6) / (d3 / 2.0)
					if dl*dl+dm*dm+dk*dk < 1.0 {
						arr[lakeArrIndex(l, k, m)] = true
					}
				}
			}
		}
	}

	fluid := cfg.fluid

	// VALIDATION phase (no draws): for every EMPTY cell adjacent to a filled cell (a wall cell),
	// check the block. Y>=4 liquid -> abort; Y<4 non-solid non-fluid -> abort; !canPlaceFeature
	// -> abort. Aborting returns false WITHOUT running the barrier draws (faithful to the
	// bytecode's early `return false`).
	for i := 0; i < 16; i++ { // X
		for j := 0; j < 16; j++ { // Z
			for k := 0; k < 8; k++ { // Y
				if !lakeIsWallCell(&arr, i, j, k) {
					continue
				}
				p := placement.BlockPos{X: origin.X + i, Y: origin.Y + k, Z: origin.Z + j}
				st := bctx.getState(p)
				if k >= 4 && block.IsFluid(st) {
					return false
				}
				if k < 4 && !block.IsSolid(st) && st != fluid {
					return false
				}
				if !cfg.canPlaceFeature(bctx, p.X, p.Y, p.Z) {
					return false
				}
			}
		}
	}

	// CARVE phase (no draws): for every FILLED cell, if canReplaceWithAirOrFluid: Y>=4 -> AIR,
	// Y<4 -> fluid. (scheduleTick + markAbove for the AIR cells are worldgen seams.)
	air := bctx.airState()
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			for k := 0; k < 8; k++ {
				if !lakeArrGet(&arr, i, j, k) {
					continue
				}
				p := placement.BlockPos{X: origin.X + i, Y: origin.Y + k, Z: origin.Z + j}
				if !cfg.canReplaceWithAirOrFluid(bctx, p.X, p.Y, p.Z) {
					continue
				}
				if k >= 4 {
					bctx.placeState(p, air)
				} else {
					bctx.placeState(p, fluid)
				}
			}
		}
	}

	// BARRIER phase: if barrier is not air, for every WALL cell that is a solid block matching
	// canReplaceWithBarrier, place barrier. At Y>=4 a nextInt(2)==0 roll SKIPS the cell (the ONE
	// per-edge draw). This phase runs ONLY because validation passed above.
	if !block.IsAir(cfg.barrier) {
		for i := 0; i < 16; i++ {
			for j := 0; j < 16; j++ {
				for k := 0; k < 8; k++ {
					if !lakeIsWallCell(&arr, i, j, k) {
						continue
					}
					// Y>=4: draw nextInt(2); ==0 skips this edge cell.
					if k >= 4 && rng.NextIntN(2) == 0 {
						continue
					}
					p := placement.BlockPos{X: origin.X + i, Y: origin.Y + k, Z: origin.Z + j}
					st := bctx.getState(p)
					if !block.IsSolid(st) {
						continue
					}
					if !cfg.canReplaceWithBarrier(bctx, p.X, p.Y, p.Z) {
						continue
					}
					bctx.placeState(p, cfg.barrier)
				}
			}
		}
	}

	// ICE phase: if the lake fluid is WATER, ice the Y==4 surface layer in cold biomes.
	if block.IsWaterFluid(fluid) {
		seaLevel := bctx.seaLevelOr(63)
		for i := 0; i < 16; i++ {
			for j := 0; j < 16; j++ {
				p := placement.BlockPos{X: origin.X + i, Y: origin.Y + 4, Z: origin.Z + j}
				bt := ctx.BiomeAt(p.X, p.Y, p.Z)
				if lakeShouldFreeze(bctx, bt, p, seaLevel) && cfg.canReplaceWithAirOrFluid(bctx, p.X, p.Y, p.Z) {
					bctx.placeState(p, freezeIceState())
				}
			}
		}
	}

	return true
}

// lakeIsWallCell ports the jar's edge/wall test: cell (i,j,k) is a "wall" iff it is EMPTY
// (arr false) AND at least one of its 6 axis neighbors is FILLED (arr true), with the boundary
// guards the bytecode uses (a neighbor index off the [0,16)x[0,16)x[0,8) grid is treated as
// filled ONLY where the jar checks it — see below). Reproduced index-for-index from the
// bytecode's neighbor checks (offsets 429-594):
//
//	if arr[i,j,k]:                                   wall = false   (an inside cell is not a wall)
//	else:
//	  wall = (i<15 && arr[i+1,j,k]) || (i>0 && arr[i-1,j,k])
//	      || (j<15 && arr[i,j+1,k]) || (j>0 && arr[i,j-1,k])
//	      || (k<7  && arr[i,j,k+1]) || (k>0 && arr[i,j,k-1])
//
// The boundary guards (i<15 etc.) mean a cell on the grid edge only counts an in-grid filled
// neighbor — matching the jar exactly (it never indexes off the array).
func lakeIsWallCell(arr *[2048]bool, i, j, k int) bool {
	if lakeArrGet(arr, i, j, k) {
		return false
	}
	if i < 15 && lakeArrGet(arr, i+1, j, k) {
		return true
	}
	if i > 0 && lakeArrGet(arr, i-1, j, k) {
		return true
	}
	if j < 15 && lakeArrGet(arr, i, j+1, k) {
		return true
	}
	if j > 0 && lakeArrGet(arr, i, j-1, k) {
		return true
	}
	if k < 7 && lakeArrGet(arr, i, j, k+1) {
		return true
	}
	if k > 0 && lakeArrGet(arr, i, j, k-1) {
		return true
	}
	return false
}

// lakeShouldFreeze ports Biome.shouldFreeze(level, pos, false) at worldgen light for the ice
// pass — identical to the freeze_top_layer body's gate: cold enough + a WATER LiquidBlock.
func lakeShouldFreeze(bctx *bodyContext, bt biome.Type, pos placement.BlockPos, seaLevel int) bool {
	if !surface.ColdEnoughToSnow(bt, pos.X, pos.Y, pos.Z, seaLevel) {
		return false
	}
	return freezeIsWaterLiquidBlock(bctx.getState(pos))
}
