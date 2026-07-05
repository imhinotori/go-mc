package world

// feature_multiface.go ports the multiface_growth feature body AND the shared
// MultifaceBlock / MultifaceSpreader machinery it needs, 1:1 from the unobfuscated
// Minecraft 26.2 jar (temp/cache/26.2-inner.jar, read via CFR + javap -c):
//
//   - net.minecraft.world.level.levelgen.feature.MultifaceGrowthFeature.place
//       + placeGrowthIfPossible + isAirOrWater                       → "multiface_growth"
//   - net.minecraft.world.level.levelgen.feature.configurations.MultifaceGrowthConfiguration
//       (block, search_range, can_place_on_floor/ceiling/wall, chance_of_spreading,
//        can_be_placed_on HolderSet; validDirections build order + getShuffledDirections
//        / getShuffledDirectionsExcept)
//   - net.minecraft.world.level.block.MultifaceBlock.{getStateForPlacement,
//        isValidStateForPlacement, canAttachTo, hasFace, getFaceProperty}
//   - net.minecraft.world.level.block.MultifaceSpreader.{spreadFromFaceTowardRandomDirection,
//        spreadFromFaceTowardDirection, getSpreadFromFaceTowardDirection, spreadToFace,
//        DefaultSpreaderConfig.{canSpreadInto,stateCanBeReplaced,placeBlock,getStateForPlacement},
//        SpreadType.{SAME_POSITION,SAME_PLANE,WRAP_AROUND}.getSpreadPos, SpreadPos}
//   - net.minecraft.Util.shuffle (Fisher-Yates from the end, swap(i, nextInt(i+1))) +
//       Direction.allShuffled (shuffledCopy of Direction.values())
//
// glow_lichen.json + sculk_vein.json are the two multiface_growth configured features;
// before this port both were an unregistered no-op (glow_lichen / sculk_vein never
// generated over cave walls). The SAME MultifaceSpreader machinery is reused by the
// sculk_patch body (feature_sculk.go) for the sculk_vein spread, so it lives here and is
// exported package-internally.
//
// The RNG-DRAW ORDER is the determinism contract (research Pitfall 4). The draws are:
//   1. getShuffledDirections(random)  — one Util.shuffle over validDirections.
//   2. per search direction that is TRIED: getShuffledDirectionsExcept(random, opp) — one
//      Util.shuffle over the filtered validDirections.
//   3. inside placeGrowthIfPossible, ONLY on a successful placement: nextFloat() for the
//      chance_of_spreading roll, and if it passes, spreadFromFaceTowardRandomDirection's
//      Direction.allShuffled(random) draw (one Util.shuffle over 6 directions).
// Every reproduce here is jar-exact; every write goes through bctx.placeState.
//
// Conservative seam (documented, never a silent skip): MultifaceBlock.canAttachTo reads
// getBlockSupportShape / getCollisionShape face-fullness. The worldgen Neighborhood exposes
// the no-context sturdy + collision-full caches (block.IsFaceSturdy / IsCollisionShapeFullBlock),
// identical to vanilla for every static ground/wall block a cave wall is made of. The
// SculkVein-specific spread config (distManhattan==2 back-face guard, sculk/catalyst/piston
// exclusions) is ported in feature_sculk.go where the vein spreader is built.

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
	registerFeatureBody("multiface_growth", multifaceGrowthBody)
}

// ---- config ----

// multifaceGrowthConfig is the decoded MultifaceGrowthConfiguration. validDirections is
// built in the codec-constructor order (ceiling→UP, floor→DOWN, wall→HORIZONTAL[N,E,S,W]),
// which the two shuffles preserve pre-shuffle (the determinism-critical element order).
type multifaceGrowthConfig struct {
	placeBlock        multifaceKind
	searchRange       int
	chanceOfSpreading float32
	canBePlacedOn     map[block.StateID]bool
	validDirections   []geodeDir
	err               error
}

// multifaceKind identifies the concrete MultifaceSpreadeableBlock the config's "block" is,
// which selects the getStateForPlacement target state family + the spreader config. The two
// configs are glow_lichen and sculk_vein.
type multifaceKind int

const (
	multifaceGlowLichen multifaceKind = iota
	multifaceSculkVein
)

var multifaceGrowthCache sync.Map // map[*feature.ConfiguredFeature]*multifaceGrowthConfig

func decodeMultifaceGrowth(cf *feature.ConfiguredFeature) *multifaceGrowthConfig {
	if v, ok := multifaceGrowthCache.Load(cf); ok {
		return v.(*multifaceGrowthConfig)
	}
	d := &multifaceGrowthConfig{
		searchRange:       10,
		chanceOfSpreading: 0.5,
	}
	var j struct {
		Block             string   `json:"block"`
		SearchRange       *int     `json:"search_range"`
		CanPlaceOnFloor   *bool    `json:"can_place_on_floor"`
		CanPlaceOnCeiling *bool    `json:"can_place_on_ceiling"`
		CanPlaceOnWall    *bool    `json:"can_place_on_wall"`
		ChanceOfSpreading *float32 `json:"chance_of_spreading"`
		CanBePlacedOn     []string `json:"can_be_placed_on"`
	}
	if err := json.Unmarshal(configRaw(cf), &j); err != nil {
		d.err = fmt.Errorf("world: multiface_growth config %q: %w", cf.ID, err)
		multifaceGrowthCache.Store(cf, d)
		return d
	}
	switch stripBlockNS(j.Block) {
	case "glow_lichen":
		d.placeBlock = multifaceGlowLichen
	case "sculk_vein":
		d.placeBlock = multifaceSculkVein
	default:
		// validateBlock in the codec errors when the block is not a
		// MultifaceSpreadeableBlock; only glow_lichen + sculk_vein are, and both are the
		// only vanilla multiface_growth configs. An unknown one is a data regression.
		d.err = fmt.Errorf("world: multiface_growth %q: block %q is not a known multiface spreadeable block", cf.ID, j.Block)
		multifaceGrowthCache.Store(cf, d)
		return d
	}
	if j.SearchRange != nil {
		d.searchRange = *j.SearchRange
	}
	if j.ChanceOfSpreading != nil {
		d.chanceOfSpreading = *j.ChanceOfSpreading
	}
	// validDirections build order (MultifaceGrowthConfiguration constructor): ceiling→UP,
	// then floor→DOWN, then wall→HORIZONTAL forEach [NORTH,EAST,SOUTH,WEST].
	if j.CanPlaceOnCeiling != nil && *j.CanPlaceOnCeiling {
		d.validDirections = append(d.validDirections, geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0})
	}
	if j.CanPlaceOnFloor != nil && *j.CanPlaceOnFloor {
		d.validDirections = append(d.validDirections, geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0})
	}
	if j.CanPlaceOnWall != nil && *j.CanPlaceOnWall {
		d.validDirections = append(d.validDirections, multifaceHorizontal[:]...)
	}
	d.canBePlacedOn = idListToStateSet(j.CanBePlacedOn)
	multifaceGrowthCache.Store(cf, d)
	return d
}

// multifaceHorizontal is Direction.Plane.HORIZONTAL.forEach order = [NORTH,EAST,SOUTH,WEST].
var multifaceHorizontal = [4]geodeDir{
	{d: block.North, dx: 0, dy: 0, dz: -1},
	{d: block.East, dx: 1, dy: 0, dz: 0},
	{d: block.South, dx: 0, dy: 0, dz: 1},
	{d: block.West, dx: -1, dy: 0, dz: 0},
}

// ---- body ----

// multifaceGrowthBody ports MultifaceGrowthFeature.place (CFR + javap -c). Draw order in
// the file header. Returns true iff a growth was placed.
func multifaceGrowthBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	cfg := decodeMultifaceGrowth(cf)
	if cfg.err != nil {
		panic(cfg.err.Error())
	}
	if !multifaceIsAirOrWater(bctx.getState(origin)) {
		return false
	}
	// getShuffledDirections(random): shuffledCopy of validDirections.
	searchDirections := multifaceShuffledCopy(cfg.validDirections, rng)
	// Try placing at the origin first.
	if bctx.multifacePlaceGrowthIfPossible(cfg, origin, rng, searchDirections) {
		return true
	}
	// Then walk each search direction up to searchRange steps. NOTE (jar-faithful): the
	// inner loop re-tests origin+searchDirection every iteration (setWithOffset(origin,
	// searchDirection) does not accumulate — MultifaceGrowthFeature.place bytecode @172), so
	// for a given searchDirection all searchRange iterations look at the same neighbour cell.
	for _, searchDir := range searchDirections {
		placementDirections := multifaceShuffledCopyExcept(cfg.validDirections, oppositeGeode(searchDir), rng)
		pos := placement.BlockPos{X: origin.X + searchDir.dx, Y: origin.Y + searchDir.dy, Z: origin.Z + searchDir.dz}
		for i := 0; i < cfg.searchRange; i++ {
			st := bctx.getState(pos)
			if !multifaceIsAirOrWater(st) && !multifaceIsPlaceBlock(cfg.placeBlock, st) {
				break // continue outer search-direction loop (label block0 in the jar)
			}
			if bctx.multifacePlaceGrowthIfPossible(cfg, pos, rng, placementDirections) {
				return true
			}
		}
	}
	return false
}

// multifacePlaceGrowthIfPossible ports MultifaceGrowthFeature.placeGrowthIfPossible. For each
// placementDirection whose neighbour is in canBePlacedOn, build the placement state via
// getStateForPlacement; a null result returns false (no more directions tried), else place +
// roll chance_of_spreading (nextFloat) → spreadFromFaceTowardRandomDirection.
func (b *bodyContext) multifacePlaceGrowthIfPossible(cfg *multifaceGrowthConfig, pos placement.BlockPos, rng levelgen.RandomSource, placementDirections []geodeDir) bool {
	oldState := b.getState(pos)
	for _, pd := range placementDirections {
		neighbour := placement.BlockPos{X: pos.X + pd.dx, Y: pos.Y + pd.dy, Z: pos.Z + pd.dz}
		if !cfg.canBePlacedOn[b.getState(neighbour)] {
			continue
		}
		newState, ok := b.multifaceGetStateForPlacement(cfg.placeBlock, oldState, pos, pd)
		if !ok {
			return false
		}
		b.placeState(pos, newState)
		// markPosForPostProcessing: no worldgen block effect (post-processing queue).
		if rng.NextFloat() < cfg.chanceOfSpreading {
			b.multifaceSpreadFromFaceTowardRandomDirection(cfg.placeBlock, newState, pos, pd, rng)
		}
		return true
	}
	return false
}

// ---- MultifaceBlock.getStateForPlacement / isValidStateForPlacement / canAttachTo ----

// multifaceGetStateForPlacement ports MultifaceBlock.getStateForPlacement(oldState, level,
// placementPos, placementDirection). Returns (state, false) when isValidStateForPlacement is
// false (the jar returns null). The new state is oldState (if already this block) else the
// default (waterlogged when the old cell is a water source), with the placementDirection face
// set true.
func (b *bodyContext) multifaceGetStateForPlacement(kind multifaceKind, oldState block.StateID, placementPos placement.BlockPos, placementDir geodeDir) (block.StateID, bool) {
	if !b.multifaceIsValidStateForPlacement(kind, oldState, placementPos, placementDir) {
		return 0, false
	}
	waterlogged := false
	var faces multifaceFaces
	if multifaceIsPlaceBlock(kind, oldState) {
		// oldState.is(this): keep its existing faces + waterlogged.
		faces = multifaceUnpackFaces(kind, oldState)
		waterlogged = multifaceWaterlogged(kind, oldState)
	} else if isWaterSource(oldState) {
		// oldState.getFluidState().isSourceOfType(WATER) → default with WATERLOGGED=true.
		waterlogged = true
	}
	faces.set(placementDir.d, true)
	return multifaceStateID(kind, faces, waterlogged), true
}

// multifaceIsValidStateForPlacement ports MultifaceBlock.isValidStateForPlacement:
// isFaceSupported(dir) (always true for these blocks) && !(oldState.is(this) &&
// hasFace(oldState, dir)) && canAttachTo(level, dir, neighbourPos, neighbourState).
func (b *bodyContext) multifaceIsValidStateForPlacement(kind multifaceKind, oldState block.StateID, placementPos placement.BlockPos, placementDir geodeDir) bool {
	if multifaceIsPlaceBlock(kind, oldState) && multifaceUnpackFaces(kind, oldState).has(placementDir.d) {
		return false
	}
	neighbour := placement.BlockPos{X: placementPos.X + placementDir.dx, Y: placementPos.Y + placementDir.dy, Z: placementPos.Z + placementDir.dz}
	return b.multifaceCanAttachTo(placementDir.d, neighbour)
}

// multifaceCanAttachTo ports MultifaceBlock.canAttachTo(level, dir, neighbourPos,
// neighbourState) = isFaceFull(supportShape, dir.opposite) || isFaceFull(collisionShape,
// dir.opposite). Reduced to the no-context sturdy + collision-full caches (identical to
// vanilla for a static wall/floor/ceiling block).
func (b *bodyContext) multifaceCanAttachTo(dir block.Direction, neighbour placement.BlockPos) bool {
	st := b.getState(neighbour)
	opp := oppositeDirection(dir)
	return block.IsFaceSturdy(st, opp, block.SupportFull) || block.IsCollisionShapeFullBlock(st)
}

// ---- MultifaceSpreader (shared with sculk vein spread) ----

// multifaceFaces is the set of six boolean face flags a multiface block state carries. It is
// the Go form of the DIRECTION→BooleanProperty face map (PipeBlock.PROPERTY_BY_DIRECTION);
// indexing by block.Direction ordinal (Down=0..East=5) matches the enum order.
type multifaceFaces [6]bool

func (f *multifaceFaces) set(d block.Direction, v bool) { f[d] = v }
func (f multifaceFaces) has(d block.Direction) bool     { return f[d] }
func (f multifaceFaces) any() bool {
	for _, v := range f {
		if v {
			return true
		}
	}
	return false
}

// multifaceSpreadType is MultifaceSpreader.SpreadType.
type multifaceSpreadType int

const (
	spreadSamePosition multifaceSpreadType = iota
	spreadSamePlane
	spreadWrapAround
)

// multifaceDefaultSpreadOrder is MultifaceSpreader.DEFAULT_SPREAD_ORDER.
var multifaceDefaultSpreadOrder = []multifaceSpreadType{spreadSamePosition, spreadSamePlane, spreadWrapAround}

// spreadPos is MultifaceSpreader.SpreadPos(pos, face).
type spreadPos struct {
	pos  placement.BlockPos
	face block.Direction
}

// multifaceSpreadPosFor ports SpreadType.getSpreadPos(pos, spreadDirection, fromFace).
func multifaceSpreadPosFor(t multifaceSpreadType, pos placement.BlockPos, spreadDir, fromFace geodeDir) spreadPos {
	switch t {
	case spreadSamePosition:
		return spreadPos{pos: pos, face: spreadDir.d}
	case spreadSamePlane:
		return spreadPos{pos: placement.BlockPos{X: pos.X + spreadDir.dx, Y: pos.Y + spreadDir.dy, Z: pos.Z + spreadDir.dz}, face: fromFace.d}
	default: // spreadWrapAround
		p := placement.BlockPos{X: pos.X + spreadDir.dx + fromFace.dx, Y: pos.Y + spreadDir.dy + fromFace.dy, Z: pos.Z + spreadDir.dz + fromFace.dz}
		return spreadPos{pos: p, face: oppositeDirection(spreadDir.d)}
	}
}

// multifaceSpreadFromFaceTowardRandomDirection ports
// MultifaceSpreader.spreadFromFaceTowardRandomDirection: for each of Direction.allShuffled,
// try spreadFromFaceTowardDirection; the FIRST that places wins (findFirst). ONE shuffle draw.
// The postProcess flag is true here (matching the feature call site).
func (b *bodyContext) multifaceSpreadFromFaceTowardRandomDirection(kind multifaceKind, state block.StateID, pos placement.BlockPos, startingFace geodeDir, rng levelgen.RandomSource) bool {
	for _, spreadDir := range multifaceAllShuffled(rng) {
		if b.multifaceSpreadFromFaceTowardDirection(kind, state, pos, startingFace, spreadDir) {
			return true
		}
	}
	return false
}

// multifaceSpreadFromFaceTowardDirection ports
// MultifaceSpreader.spreadFromFaceTowardDirection = getSpreadFromFaceTowardDirection(...).flatMap(spreadToFace).
// spreadTypes are the kind's configured order (DEFAULT for glow_lichen; the SculkVein vein
// spreader uses DEFAULT too — feature_sculk.go passes the same order for spreadAll). Returns
// true iff a face was placed. 0 rng draws.
func (b *bodyContext) multifaceSpreadFromFaceTowardDirection(kind multifaceKind, state block.StateID, pos placement.BlockPos, fromFace, spreadDir geodeDir) bool {
	sp, ok := b.multifaceGetSpreadFromFaceTowardDirection(kind, state, pos, fromFace, spreadDir, multifaceDefaultSpreadOrder)
	if !ok {
		return false
	}
	return b.multifaceSpreadToFace(kind, sp)
}

// multifaceGetSpreadFromFaceTowardDirection ports
// MultifaceSpreader.getSpreadFromFaceTowardDirection: reject same-axis; require
// (isOtherBlockValidAsSource || (hasFace(startingFace) && !hasFace(spreadDirection))); then
// return the first spreadType whose canSpreadInto passes. isOtherBlockValidAsSource is false
// for the default config (glow_lichen); for sculk_vein it is !state.is(SCULK_VEIN) — but the
// state passed IS the just-placed vein/lichen, so hasFace(startingFace) drives it.
func (b *bodyContext) multifaceGetSpreadFromFaceTowardDirection(kind multifaceKind, state block.StateID, pos placement.BlockPos, startingFace, spreadDir geodeDir, spreadTypes []multifaceSpreadType) (spreadPos, bool) {
	if directionAxis(spreadDir.d) == directionAxis(startingFace.d) {
		return spreadPos{}, false
	}
	faces := multifaceUnpackFaces(kind, state)
	otherValidAsSource := multifaceIsOtherBlockValidAsSource(kind, state)
	if !(otherValidAsSource || (faces.has(startingFace.d) && !faces.has(spreadDir.d))) {
		return spreadPos{}, false
	}
	for _, t := range spreadTypes {
		sp := multifaceSpreadPosFor(t, pos, spreadDir, startingFace)
		if b.multifaceCanSpreadInto(kind, pos, sp) {
			return sp, true
		}
	}
	return spreadPos{}, false
}

// multifaceCanSpreadInto ports the config canSpreadInto: stateCanBeReplaced(existing) &&
// isValidStateForPlacement(existing, spreadPos.pos, spreadPos.face). For the DEFAULT config
// stateCanBeReplaced = existing air || is(this) || (water source). The SculkVein config adds
// extra guards (feature_sculk.go supplies them via the kind switch in
// multifaceStateCanBeReplaced).
func (b *bodyContext) multifaceCanSpreadInto(kind multifaceKind, sourcePos placement.BlockPos, sp spreadPos) bool {
	existing := b.getState(sp.pos)
	if !b.multifaceStateCanBeReplaced(kind, sourcePos, sp, existing) {
		return false
	}
	return b.multifaceIsValidStateForPlacement(kind, existing, sp.pos, geodeForDir(sp.face))
}

// multifaceStateCanBeReplaced ports DefaultSpreaderConfig.stateCanBeReplaced (and, for
// sculk_vein, SculkVeinSpreaderConfig.stateCanBeReplaced which prepends extra guards then
// falls back to super). DEFAULT: existing air || is(this) || water-source.
func (b *bodyContext) multifaceStateCanBeReplaced(kind multifaceKind, sourcePos placement.BlockPos, sp spreadPos, existing block.StateID) bool {
	if kind == multifaceSculkVein {
		if !b.sculkVeinStateCanBeReplacedGuards(sourcePos, sp, existing) {
			return false
		}
		// super.stateCanBeReplaced OR existing.canBeReplaced() — the SculkVein override ORs
		// canBeReplaced() (replaceable plants) with the DEFAULT check. Fall through to the
		// DEFAULT check below (the canBeReplaced() replaceable set is covered by air/water in
		// the worldgen cave context — documented conservative in feature_sculk.go).
	}
	return block.IsAir(existing) || multifaceIsPlaceBlock(kind, existing) || isWaterSource(existing)
}

// multifaceSpreadToFace ports MultifaceSpreader.spreadToFace → config.placeBlock: build the
// spread state via getStateForPlacement at spreadPos.pos/face over the existing block; place
// it when non-null. Returns true iff placed.
func (b *bodyContext) multifaceSpreadToFace(kind multifaceKind, sp spreadPos) bool {
	oldState := b.getState(sp.pos)
	newState, ok := b.multifaceGetStateForPlacement(kind, oldState, sp.pos, geodeForDir(sp.face))
	if !ok {
		return false
	}
	b.placeState(sp.pos, newState)
	return true
}

// ---- block-state (de)construction per kind ----

// multifaceIsPlaceBlock reports state.is(this block) for the kind (any face configuration).
func multifaceIsPlaceBlock(kind multifaceKind, st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	switch kind {
	case multifaceGlowLichen:
		_, ok := block.StateList[st].(block.GlowLichen)
		return ok
	case multifaceSculkVein:
		_, ok := block.StateList[st].(block.SculkVein)
		return ok
	}
	return false
}

// multifaceIsOtherBlockValidAsSource ports isOtherBlockValidAsSource: false for the DEFAULT
// (glow_lichen) config; for sculk_vein it is !state.is(SCULK_VEIN).
func multifaceIsOtherBlockValidAsSource(kind multifaceKind, st block.StateID) bool {
	if kind == multifaceSculkVein {
		return !multifaceIsPlaceBlock(multifaceSculkVein, st)
	}
	return false
}

// multifaceUnpackFaces reads the six face flags off a multiface state (0 flags for a
// non-multiface state).
func multifaceUnpackFaces(kind multifaceKind, st block.StateID) multifaceFaces {
	var f multifaceFaces
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return f
	}
	switch v := block.StateList[st].(type) {
	case block.GlowLichen:
		if kind != multifaceGlowLichen {
			return f
		}
		f[block.Down] = bool(v.Down)
		f[block.Up] = bool(v.Up)
		f[block.North] = bool(v.North)
		f[block.South] = bool(v.South)
		f[block.West] = bool(v.West)
		f[block.East] = bool(v.East)
	case block.SculkVein:
		if kind != multifaceSculkVein {
			return f
		}
		f[block.Down] = bool(v.Down)
		f[block.Up] = bool(v.Up)
		f[block.North] = bool(v.North)
		f[block.South] = bool(v.South)
		f[block.West] = bool(v.West)
		f[block.East] = bool(v.East)
	}
	return f
}

// multifaceWaterlogged reads the waterlogged flag off a multiface state.
func multifaceWaterlogged(kind multifaceKind, st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	switch v := block.StateList[st].(type) {
	case block.GlowLichen:
		return bool(v.Waterlogged)
	case block.SculkVein:
		return bool(v.Waterlogged)
	}
	return false
}

// multifaceStateID builds the multiface block state with the given faces + waterlogged.
func multifaceStateID(kind multifaceKind, f multifaceFaces, waterlogged bool) block.StateID {
	switch kind {
	case multifaceGlowLichen:
		return block.ToStateID[block.GlowLichen{
			Down: block.Boolean(f[block.Down]), Up: block.Boolean(f[block.Up]),
			North: block.Boolean(f[block.North]), South: block.Boolean(f[block.South]),
			West: block.Boolean(f[block.West]), East: block.Boolean(f[block.East]),
			Waterlogged: block.Boolean(waterlogged),
		}]
	case multifaceSculkVein:
		return block.ToStateID[block.SculkVein{
			Down: block.Boolean(f[block.Down]), Up: block.Boolean(f[block.Up]),
			North: block.Boolean(f[block.North]), South: block.Boolean(f[block.South]),
			West: block.Boolean(f[block.West]), East: block.Boolean(f[block.East]),
			Waterlogged: block.Boolean(waterlogged),
		}]
	}
	return 0
}

// ---- shared small helpers ----

// multifaceIsAirOrWater ports MultifaceGrowthFeature.isAirOrWater: air || is(WATER).
func multifaceIsAirOrWater(st block.StateID) bool {
	return block.IsAir(st) || isWaterBlock(st)
}

// isWaterSource reports state.getFluidState().isSourceOfType(WATER): a WATER block at level 0
// (the source). The worldgen Neighborhood water is placed as a source (Water{Level:0}).
func isWaterSource(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	w, ok := block.StateList[st].(block.Water)
	return ok && w.Level == 0
}

// multifaceShuffledCopy ports Util.shuffledCopy: copy then Util.shuffle (Fisher-Yates from
// the end, swap(i, nextInt(i+1))). One rng draw per i in [size-1..1].
func multifaceShuffledCopy(src []geodeDir, rng levelgen.RandomSource) []geodeDir {
	out := make([]geodeDir, len(src))
	copy(out, src)
	multifaceShuffle(out, rng)
	return out
}

// multifaceShuffledCopyExcept ports getShuffledDirectionsExcept: filter(≠exclude) preserving
// order, THEN Util.shuffle.
func multifaceShuffledCopyExcept(src []geodeDir, exclude geodeDir, rng levelgen.RandomSource) []geodeDir {
	out := make([]geodeDir, 0, len(src))
	for _, d := range src {
		if d.d != exclude.d {
			out = append(out, d)
		}
	}
	multifaceShuffle(out, rng)
	return out
}

// multifaceAllShuffled ports Direction.allShuffled: shuffledCopy of Direction.values()
// (geodeDirections = [DOWN,UP,NORTH,SOUTH,WEST,EAST]).
func multifaceAllShuffled(rng levelgen.RandomSource) []geodeDir {
	out := make([]geodeDir, len(geodeDirections))
	copy(out, geodeDirections[:])
	multifaceShuffle(out, rng)
	return out
}

// multifaceShuffle ports Util.shuffle(list, rng): for i = size-1 downto 1, swap(i, nextInt(i+1)).
func multifaceShuffle(s []geodeDir, rng levelgen.RandomSource) {
	for i := len(s) - 1; i > 0; i-- {
		j := int(rng.NextIntN(int32(i + 1)))
		s[i], s[j] = s[j], s[i]
	}
}

// oppositeGeode returns the geodeDir for the opposite of d.
func oppositeGeode(d geodeDir) geodeDir { return geodeForDir(oppositeDirection(d.d)) }

// geodeForDir returns the geodeDir (with normal step) for a block.Direction.
func geodeForDir(d block.Direction) geodeDir {
	switch d {
	case block.Down:
		return geodeDir{d: block.Down, dx: 0, dy: -1, dz: 0}
	case block.Up:
		return geodeDir{d: block.Up, dx: 0, dy: 1, dz: 0}
	case block.North:
		return geodeDir{d: block.North, dx: 0, dy: 0, dz: -1}
	case block.South:
		return geodeDir{d: block.South, dx: 0, dy: 0, dz: 1}
	case block.West:
		return geodeDir{d: block.West, dx: -1, dy: 0, dz: 0}
	default: // East
		return geodeDir{d: block.East, dx: 1, dy: 0, dz: 0}
	}
}

// directionAxis ports Direction.getAxis(): X for WEST/EAST, Y for DOWN/UP, Z for NORTH/SOUTH.
func directionAxis(d block.Direction) int {
	switch d {
	case block.Down, block.Up:
		return 1 // Y
	case block.North, block.South:
		return 2 // Z
	default: // West, East
		return 0 // X
	}
}

// stripBlockNS strips a "minecraft:" namespace from a block id.
func stripBlockNS(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[i+1:]
		}
	}
	return id
}

// idListToStateSet expands a slice of block ids to the set of ALL their state ids (the
// can_be_placed_on HolderSet membership; resolved once at decode time).
func idListToStateSet(ids []string) map[block.StateID]bool {
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
	return set
}
