package world

// feature_spring.go ports SpringFeature.place — the water/lava spring "trickle" feature
// (net.minecraft.world.level.levelgen.feature.SpringFeature, javap -c against
// temp/cache/26.2-inner.jar). SpringConfiguration carries the fluid `state`, the
// `requiresBlockBelow` flag (default true), the `rockCount` (default 4) and `holeCount`
// (default 1) match counts, and the `validBlocks` HolderSet (a block id or list). The
// spring places its fluid source at `origin` iff the surrounding rock/hole geometry
// matches exactly — producing the small cave-wall waterfalls/lavafalls.
//
// SpringFeature.place takes ZERO rng draws (it is a pure geometric gate + one setBlock),
// so its only determinism contribution is whether it writes — the draw stream is
// untouched, matching the bytecode (no RandomSource.next* call anywhere in place).
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.SpringFeature.place
//   - net.minecraft.world.level.levelgen.feature.configurations.SpringConfiguration (codec
//     defaults: requires_block_below=true, rock_count=4, hole_count=1)
//   - net.minecraft.world.level.material.FluidState.createLegacyBlock ->
//     Fluid.createLegacyBlock (WaterFluid/LavaFluid: WATER/LAVA.defaultBlockState with
//     LEVEL=getLegacyLevel; a SOURCE fluid has legacyLevel 0, i.e. the source block) — the
//     data `state` is the source water/lava fluid, so the placed block is the source block.
//
// The scheduleTick(pos, fluid, 0) call the jar makes after setBlock is a TICK-side fluid
// update the worldgen Neighborhood has no scheduled-tick queue for; the placed SOURCE block
// is stable (a source fluid does not flow away), so omitting the schedule is behavior-identical
// for the placed result (the fluid stays put). Documented as a worldgen seam.

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("spring_feature", springBody) }

// springConfigCache memoizes the decoded SpringConfiguration per ConfiguredFeature
// (oreConfigCache pattern): the validBlocks set is resolved by scanning all block states, far
// too expensive to redo per placement. Keyed by the stable *ConfiguredFeature pointer.
var springConfigCache sync.Map // map[*feature.ConfiguredFeature]*springConfig

// springConfig is the decoded SpringConfiguration.
type springConfig struct {
	fluid              block.StateID          // state.createLegacyBlock() — the source fluid block
	requiresBlockBelow bool                   // requires_block_below (default true)
	rockCount          int                    // rock_count (default 4)
	holeCount          int                    // hole_count (default 1)
	validBlocks        map[block.StateID]bool // validBlocks HolderSet, expanded to all states
}

// jsonSpringConfig is the on-disk SpringConfiguration shape. requires_block_below/rock_count/
// hole_count are optional (pointers so an absent field takes the codec default). valid_blocks
// is a block id or a list of block ids (RegistryCodecs.homogeneousList).
type jsonSpringConfig struct {
	State              json.RawMessage `json:"state"`
	RequiresBlockBelow *bool           `json:"requires_block_below"`
	RockCount          *int            `json:"rock_count"`
	HoleCount          *int            `json:"hole_count"`
	ValidBlocks        json.RawMessage `json:"valid_blocks"`
}

// decodeSpringConfigCached returns the memoized decoded config for cf.
func decodeSpringConfigCached(cf *feature.ConfiguredFeature) (*springConfig, error) {
	if v, ok := springConfigCache.Load(cf); ok {
		return v.(*springConfig), nil
	}
	cfg, err := decodeSpringConfig(configRaw(cf))
	if err != nil {
		return nil, err
	}
	springConfigCache.Store(cf, cfg)
	return cfg, nil
}

func decodeSpringConfig(raw json.RawMessage) (*springConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("world: spring config is empty")
	}
	var j jsonSpringConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("world: spring config: %w", err)
	}
	// state.createLegacyBlock(): for the data's SOURCE fluid the legacy block is the source
	// water/lava block (LEVEL=0). ResolveBlockStateJSON resolves {Name,Properties} to that
	// block state (it drops the fluid-only `falling` property and keeps the source default),
	// which is exactly createLegacyBlock's result for a source fluid.
	fluid, err := feature.ResolveBlockStateJSON(j.State)
	if err != nil {
		return nil, fmt.Errorf("world: spring config state: %w", err)
	}
	cfg := &springConfig{
		fluid:              fluid,
		requiresBlockBelow: true, // codec default
		rockCount:          4,    // codec default
		holeCount:          1,    // codec default
	}
	if j.RequiresBlockBelow != nil {
		cfg.requiresBlockBelow = *j.RequiresBlockBelow
	}
	if j.RockCount != nil {
		cfg.rockCount = *j.RockCount
	}
	if j.HoleCount != nil {
		cfg.holeCount = *j.HoleCount
	}
	set, err := springValidBlockSet(j.ValidBlocks)
	if err != nil {
		return nil, fmt.Errorf("world: spring config valid_blocks: %w", err)
	}
	cfg.validBlocks = set
	return cfg, nil
}

// springValidBlockSet resolves the valid_blocks field (a single block id or a list) to the
// set of ALL member state ids (BlockState.is(HolderSet) is property-agnostic).
func springValidBlockSet(raw json.RawMessage) (map[block.StateID]bool, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing valid_blocks")
	}
	var ids []string
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		ids = []string{one}
	} else if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("decoding valid_blocks: %w", err)
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("world: spring valid_blocks resolved to an empty set")
	}
	return set, nil
}

// springBody ports SpringFeature.place (javap -c), ZERO rng draws:
//
//	if !above.is(validBlocks):                     return false           // getBlockState(above)
//	if requiresBlockBelow && !below.is(validBlocks): return false          // getBlockState(below)
//	here := getBlockState(origin)
//	if !here.isAir() && !here.is(validBlocks):     return false
//	rockCount = count of {west,east,north,south,below} that .is(validBlocks)
//	holeCount = count of {west,east,north,south,below} that isEmptyBlock (air)
//	if rockCount == cfg.rockCount && holeCount == cfg.holeCount:
//	    setBlock(origin, fluid.createLegacyBlock, flag 2); scheduleTick(...) [worldgen seam]; i++
//	return i > 0
//
// The above/below/here reads + the {west,east,north,south,below} scans go through the 3x3
// view (air outside / out-of-Y). The neighbor scan uses the 5 non-UP neighbors exactly
// (the jar counts west,east,north,south,below — never above — for BOTH the rock and hole tallies).
func springBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	_ levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg, err := decodeSpringConfigCached(cf)
	if err != nil {
		// A config decode failure is a build-data error; surface it loudly (T-12-07).
		panic(err)
	}

	above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if !cfg.validBlocks[bctx.getState(above)] {
		return false
	}
	below := placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if cfg.requiresBlockBelow && !cfg.validBlocks[bctx.getState(below)] {
		return false
	}
	here := bctx.getState(pos)
	if !block.IsAir(here) && !cfg.validBlocks[here] {
		return false
	}

	// The 5 non-UP neighbors, in jar order: west, east, north, south, below.
	neighbors := [5]placement.BlockPos{
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, // west
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, // east
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, // north
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, // south
		below,                              // below
	}
	rock := 0
	for _, n := range neighbors {
		if cfg.validBlocks[bctx.getState(n)] {
			rock++
		}
	}
	hole := 0
	for _, n := range neighbors {
		if block.IsAir(bctx.getState(n)) {
			hole++
		}
	}

	if rock == cfg.rockCount && hole == cfg.holeCount {
		bctx.placeState(pos, cfg.fluid)
		// scheduleTick(origin, fluid, 0) is a worldgen seam (no scheduled-tick queue); the
		// placed SOURCE block is stable so the result is behavior-identical (see file header).
		return true
	}
	return false
}
