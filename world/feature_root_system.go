package world

// feature_root_system.go ports the root_system feature body 1:1 from the unobfuscated
// Minecraft 26.2 jar (temp/cache/26.2-inner.jar, read via CFR + javap -c):
//
//   - net.minecraft.world.level.levelgen.feature.RootSystemFeature.{place,spaceForTree,
//        isAllowedTreeSpace,placeDirtAndTree,placeDirt,placeRootedDirt,placeRoots}
//   - net.minecraft.world.level.levelgen.feature.configurations.RootSystemConfiguration
//        (treeFeature Holder<PlacedFeature>, required_vertical_space_for_tree, level_test_distance,
//         max_level_deviation, root_radius, root_replaceable HolderSet, root_state_provider,
//         root_placement_attempts, root_column_max_height, hanging_root_radius,
//         hanging_roots_vertical_span, hanging_root_state_provider, hanging_root_placement_attempts,
//         allowed_vertical_water_for_tree, allowed_tree_position BlockPredicate)
//   - net.minecraft.world.level.block.HangingRootsBlock.canSurvive (above sturdy DOWN)
//   - net.minecraft.core.Direction.from2DDataValue (BY_2D_DATA = [SOUTH,WEST,NORTH,EAST])
//
// rooted_azalea_tree.json + rooted_sulfur_spring.json are the two configured features; before
// this port both were an unregistered no-op (the azalea root column under a lush-cave azalea
// tree never generated). The recursive tree placement reuses the selector recursion machinery
// (placeSubFeature / resolveSubFeature in feature_selector.go): treeFeature().value().place is a
// PlacedFeature.place, exactly what placeSubFeature binds + runs with the threaded rng.
//
// The RNG-DRAW ORDER is the determinism contract (research Pitfall 4). The draws are, in order:
//   1. placeDirtAndTree walks Y up to rootColumnMaxHeight; on the first Y that passes the
//      allowed_tree_position predicate + spaceForTree + a solid non-lava floor, it runs the
//      tree sub-feature (whose OWN modifier + body draws thread the same rng — recursion), then
//      placeDirt over the rooted column.
//   2. placeDirt → placeRootedDirt per column Y: rootPlacementAttempts × [nextInt(radius),
//      nextInt(radius) (X), nextInt(radius), nextInt(radius) (Z)] + provider.getState on a match.
//   3. placeRoots: hangingRootPlacementAttempts × [nextInt(hr),nextInt(hr) (X),
//      nextInt(vspan),nextInt(vspan) (Y), nextInt(hr),nextInt(hr) (Z)] + provider.getState when
//      the cell is empty (drawn before canSurvive). Every write goes through bctx.placeState.
//
// Conservative seams (documented): allowed_tree_position is a placement.BlockPredicate tested
// through the same Neighborhood-backed reads block_column uses (the azalea_grows_on /
// replaceable_by_trees / air tags resolve from the embedded jar tag JSONs). isSolid uses
// block.IsSolid (BlockStateBase.isSolid legacySolid). canSurvive for hanging_roots reduces to
// the above-sturdy-DOWN read the jar HangingRootsBlock.canSurvive performs.

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("root_system", rootSystemBody)
}

// ---- config ----

type rootSystemConfig struct {
	treeFeatureRaw            json.RawMessage
	requiredVerticalSpaceTree int
	levelTestDistance         int
	maxLevelDeviation         int
	rootRadius                int
	rootReplaceable           map[block.StateID]bool
	rootStateProvider         feature.BlockStateProvider
	rootPlacementAttempts     int
	rootColumnMaxHeight       int
	hangingRootRadius         int
	hangingRootsVerticalSpan  int
	hangingRootStateProvider  feature.BlockStateProvider
	hangingRootPlaceAttempts  int
	allowedVerticalWaterTree  int
	allowedTreePosition       placement.BlockPredicate
	err                       error
}

var rootSystemCache sync.Map // map[*feature.ConfiguredFeature]*rootSystemConfig

func decodeRootSystem(cf *feature.ConfiguredFeature) *rootSystemConfig {
	if v, ok := rootSystemCache.Load(cf); ok {
		return v.(*rootSystemConfig)
	}
	d := &rootSystemConfig{}
	var j struct {
		Feature                   json.RawMessage `json:"feature"`
		RequiredVerticalSpaceTree int             `json:"required_vertical_space_for_tree"`
		LevelTestDistance         int             `json:"level_test_distance"`
		MaxLevelDeviation         int             `json:"max_level_deviation"`
		RootRadius                int             `json:"root_radius"`
		RootReplaceable           string          `json:"root_replaceable"`
		RootStateProvider         json.RawMessage `json:"root_state_provider"`
		RootPlacementAttempts     int             `json:"root_placement_attempts"`
		RootColumnMaxHeight       int             `json:"root_column_max_height"`
		HangingRootRadius         int             `json:"hanging_root_radius"`
		HangingRootsVerticalSpan  int             `json:"hanging_roots_vertical_span"`
		HangingRootStateProvider  json.RawMessage `json:"hanging_root_state_provider"`
		HangingRootPlaceAttempts  int             `json:"hanging_root_placement_attempts"`
		AllowedVerticalWaterTree  int             `json:"allowed_vertical_water_for_tree"`
		AllowedTreePosition       json.RawMessage `json:"allowed_tree_position"`
	}
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: root_system config %q: %w", cf.ID, err)
		rootSystemCache.Store(cf, d)
		return d
	}
	d.treeFeatureRaw = j.Feature
	d.requiredVerticalSpaceTree = j.RequiredVerticalSpaceTree
	d.levelTestDistance = j.LevelTestDistance
	d.maxLevelDeviation = j.MaxLevelDeviation
	d.rootRadius = j.RootRadius
	d.rootReplaceable = rootReplaceableSet(j.RootReplaceable)
	d.rootPlacementAttempts = j.RootPlacementAttempts
	d.rootColumnMaxHeight = j.RootColumnMaxHeight
	d.hangingRootRadius = j.HangingRootRadius
	d.hangingRootsVerticalSpan = j.HangingRootsVerticalSpan
	d.hangingRootPlaceAttempts = j.HangingRootPlaceAttempts
	d.allowedVerticalWaterTree = j.AllowedVerticalWaterTree

	rootProv, err := feature.ParseProvider(j.RootStateProvider)
	if err != nil {
		d.err = fmt.Errorf("world: root_system root_state_provider %q: %w", cf.ID, err)
		rootSystemCache.Store(cf, d)
		return d
	}
	d.rootStateProvider = rootProv
	hangProv, err := feature.ParseProvider(j.HangingRootStateProvider)
	if err != nil {
		d.err = fmt.Errorf("world: root_system hanging_root_state_provider %q: %w", cf.ID, err)
		rootSystemCache.Store(cf, d)
		return d
	}
	d.hangingRootStateProvider = hangProv
	pred, err := placement.ParsePredicate(j.AllowedTreePosition)
	if err != nil {
		d.err = fmt.Errorf("world: root_system allowed_tree_position %q: %w", cf.ID, err)
		rootSystemCache.Store(cf, d)
		return d
	}
	d.allowedTreePosition = pred
	rootSystemCache.Store(cf, d)
	return d
}

// rootReplaceableSet resolves the root_replaceable HolderSet (a "#tag" ref) to its state set.
func rootReplaceableSet(ref string) map[block.StateID]bool {
	tag := ref
	if len(tag) > 0 && tag[0] == '#' {
		tag = tag[1:]
	}
	return resolveTagStateSet(tag)
}

// ---- body ----

// rootSystemBody ports RootSystemFeature.place. Draw order in the file header.
func rootSystemBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg := decodeRootSystem(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}
	if !block.IsAir(bctx.getState(origin)) {
		return false
	}
	if bctx.rootPlaceDirtAndTree(cfg, ctx, rng, origin) {
		bctx.rootPlaceRoots(cfg, rng, origin)
	}
	return true
}

// rootPlaceDirtAndTree ports RootSystemFeature.placeDirtAndTree. Returns true when a tree was
// placed (and the rooted column laid). workingPos starts at origin and moves UP each iteration.
func (b *bodyContext) rootPlaceDirtAndTree(cfg *rootSystemConfig, ctx placement.PlacementContext, rng levelgen.RandomSource, origin placement.BlockPos) bool {
	workingPos := origin
	for y := 0; y < cfg.rootColumnMaxHeight; y++ {
		workingPos.Y++ // move(UP)
		// getHeight(WORLD_SURFACE, workingPos) < workingPos.getY() → stop.
		if ctx.GetHeight(placement.WorldSurfaceWG, workingPos.X, workingPos.Z) < workingPos.Y {
			return false
		}
		if !b.allowedTreePositionTest(cfg, ctx, workingPos) || !b.rootSpaceForTree(cfg, workingPos) {
			continue
		}
		belowPos := placement.BlockPos{X: workingPos.X, Y: workingPos.Y - 1, Z: workingPos.Z}
		belowState := b.getState(belowPos)
		// getFluidState(below).is(LAVA) || !getBlockState(below).isSolid() → stop.
		if dripstoneIsLava(belowState) || !block.IsSolid(belowState) {
			return false
		}
		if !b.rootPlaceTree(cfg, ctx, rng, workingPos) {
			continue
		}
		b.rootPlaceDirt(cfg, rng, origin, origin.Y+y)
		return true
	}
	return false
}

// allowedTreePositionTest evaluates the allowed_tree_position BlockPredicate at pos through the
// Neighborhood-backed PlacementContext (the same read path block_column's allowed predicate
// uses). 0 rng draws.
func (b *bodyContext) allowedTreePositionTest(cfg *rootSystemConfig, ctx placement.PlacementContext, pos placement.BlockPos) bool {
	return cfg.allowedTreePosition.Test(ctx, pos.X, pos.Y, pos.Z)
}

// rootSpaceForTree ports RootSystemFeature.spaceForTree. 0 rng draws.
func (b *bodyContext) rootSpaceForTree(cfg *rootSystemConfig, pos placement.BlockPos) bool {
	columnUpPos := pos
	for i := 1; i <= cfg.requiredVerticalSpaceTree; i++ {
		columnUpPos.Y++ // move(UP)
		state := b.getState(columnUpPos)
		if rootIsAllowedTreeSpace(state, i, cfg.allowedVerticalWaterTree) {
			continue
		}
		return false
	}
	if cfg.levelTestDistance > 0 {
		cornerPos := pos
		for i := 0; i < 4; i++ {
			d := rootFrom2DDataValue(i)
			cornerPos.X += d.dx * cfg.levelTestDistance
			cornerPos.Z += d.dz * cfg.levelTestDistance
			below := b.getState(placement.BlockPos{X: cornerPos.X, Y: cornerPos.Y - cfg.maxLevelDeviation, Z: cornerPos.Z})
			above := b.getState(placement.BlockPos{X: cornerPos.X, Y: cornerPos.Y + cfg.maxLevelDeviation, Z: cornerPos.Z})
			if block.IsAir(below) || !block.IsAir(above) {
				return false
			}
			cornerPos = pos // cornerPos.set(pos)
		}
	}
	return true
}

// rootIsAllowedTreeSpace ports RootSystemFeature.isAllowedTreeSpace: air, OR within the allowed
// vertical water height with a WATER fluid. blocksAboveGround = blocksAboveOrigin + 1.
func rootIsAllowedTreeSpace(st block.StateID, blocksAboveOrigin, allowedVerticalWaterHeight int) bool {
	if block.IsAir(st) {
		return true
	}
	blocksAboveGround := blocksAboveOrigin + 1
	return blocksAboveGround <= allowedVerticalWaterHeight && isWaterFluid(st)
}

// rootPlaceTree ports treeFeature().value().place(level, generator, random, workingPos): resolve
// the inline tree PlacedFeature and run it via the selector recursion seam (placeSubFeature),
// so the tree body's draws thread the same rng. A resolve/bind failure is skipped defensively
// (the same convention placeSubFeature uses) — but the tree draws that ran still stand.
func (b *bodyContext) rootPlaceTree(cfg *rootSystemConfig, ctx placement.PlacementContext, rng levelgen.RandomSource, pos placement.BlockPos) bool {
	if b.reg == nil {
		return false
	}
	sub, err := resolveSubFeature(b, cfg.treeFeatureRaw)
	if err != nil || sub == nil {
		return false
	}
	return placeSubFeature(b, sub, b.subDepth+1, ctx, rng, pos)
}

// rootPlaceDirt ports RootSystemFeature.placeDirt: place rooted dirt from origin.getY() up to
// targetHeight-1, one placeRootedDirt per Y.
func (b *bodyContext) rootPlaceDirt(cfg *rootSystemConfig, rng levelgen.RandomSource, origin placement.BlockPos, targetHeight int) {
	originX, originZ := origin.X, origin.Z
	for y := origin.Y; y < targetHeight; y++ {
		b.rootPlaceRootedDirt(cfg, rng, originX, originZ, placement.BlockPos{X: originX, Y: y, Z: originZ})
	}
}

// rootPlaceRootedDirt ports RootSystemFeature.placeRootedDirt. workingPos is offset from itself
// per attempt (dy=0, so Y is fixed), then X/Z reset to origin. Draw order per attempt:
// nextInt(radius), nextInt(radius) (X), nextInt(radius), nextInt(radius) (Z); the provider
// getState draws only on a root_replaceable match.
func (b *bodyContext) rootPlaceRootedDirt(cfg *rootSystemConfig, rng levelgen.RandomSource, originX, originZ int, workingPos placement.BlockPos) {
	rootRadius := cfg.rootRadius
	for i := 0; i < cfg.rootPlacementAttempts; i++ {
		dx := int(rng.NextIntN(int32(rootRadius))) - int(rng.NextIntN(int32(rootRadius)))
		dz := int(rng.NextIntN(int32(rootRadius))) - int(rng.NextIntN(int32(rootRadius)))
		workingPos = placement.BlockPos{X: workingPos.X + dx, Y: workingPos.Y, Z: workingPos.Z + dz}
		if cfg.rootReplaceable[b.getState(workingPos)] {
			st := cfg.rootStateProvider.GetState(rng, workingPos.X, workingPos.Y, workingPos.Z)
			b.placeState(workingPos, st)
		}
		workingPos.X = originX
		workingPos.Z = originZ
	}
}

// rootPlaceRoots ports RootSystemFeature.placeRoots (hanging roots). Draw order per attempt:
// nextInt(hr),nextInt(hr) (X), nextInt(vspan),nextInt(vspan) (Y), nextInt(hr),nextInt(hr) (Z);
// getState drawn (when empty) before canSurvive.
func (b *bodyContext) rootPlaceRoots(cfg *rootSystemConfig, rng levelgen.RandomSource, pos placement.BlockPos) {
	rootRadius := cfg.hangingRootRadius
	verticalSpan := cfg.hangingRootsVerticalSpan
	for i := 0; i < cfg.hangingRootPlaceAttempts; i++ {
		dx := int(rng.NextIntN(int32(rootRadius))) - int(rng.NextIntN(int32(rootRadius)))
		dy := int(rng.NextIntN(int32(verticalSpan))) - int(rng.NextIntN(int32(verticalSpan)))
		dz := int(rng.NextIntN(int32(rootRadius))) - int(rng.NextIntN(int32(rootRadius)))
		workingPos := placement.BlockPos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
		if !block.IsAir(b.getState(workingPos)) {
			continue
		}
		targetState := cfg.hangingRootStateProvider.GetState(rng, workingPos.X, workingPos.Y, workingPos.Z)
		if !b.hangingRootsCanSurvive(workingPos) {
			continue
		}
		// getBlockState(workingPos.above()).isFaceSturdy(workingPos, DOWN) — the explicit
		// attachment re-check (canSurvive already tests the same block; both are ported).
		above := placement.BlockPos{X: workingPos.X, Y: workingPos.Y + 1, Z: workingPos.Z}
		if !block.IsFaceSturdy(b.getState(above), block.Down, block.SupportFull) {
			continue
		}
		b.placeState(workingPos, targetState)
	}
}

// hangingRootsCanSurvive ports HangingRootsBlock.canSurvive: the block ABOVE is face-sturdy on
// its DOWN face.
func (b *bodyContext) hangingRootsCanSurvive(pos placement.BlockPos) bool {
	above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	return block.IsFaceSturdy(b.getState(above), block.Down, block.SupportFull)
}

// rootFrom2DDataValue ports Direction.from2DDataValue: BY_2D_DATA = [SOUTH,WEST,NORTH,EAST]
// (sorted by data2d: SOUTH=0, WEST=1, NORTH=2, EAST=3).
func rootFrom2DDataValue(i int) geodeDir {
	switch i % 4 {
	case 0:
		return geodeDir{d: block.South, dx: 0, dy: 0, dz: 1}
	case 1:
		return geodeDir{d: block.West, dx: -1, dy: 0, dz: 0}
	case 2:
		return geodeDir{d: block.North, dx: 0, dy: 0, dz: -1}
	default:
		return geodeDir{d: block.East, dx: 1, dy: 0, dz: 0}
	}
}
