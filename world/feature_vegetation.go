package world

// feature_vegetation.go ports the water/land VEGETATION feature bodies that were
// previously unregistered (a silent no-op that never generated): seagrass, kelp,
// sea_pickle, vines, bamboo. Each is a method-for-method port of its
// net.minecraft.world.level.levelgen.feature.* class's place(FeaturePlaceContext),
// transcribed against temp/cache/26.2-inner.jar (CFR + javap -c). The RNG-DRAW ORDER is
// the determinism contract — the draws are reproduced in jar order (research Pitfall 4 /
// T-12-12), including the draws consumed BEFORE a survival/water guard rejects a
// placement (e.g. sea_pickle draws its pickle count before the water test).
//
// Sources (CFR, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.SeagrassFeature.place
//   - net.minecraft.world.level.levelgen.feature.KelpFeature.place
//   - net.minecraft.world.level.levelgen.feature.SeaPickleFeature.place
//   - net.minecraft.world.level.levelgen.feature.VinesFeature.place
//   - net.minecraft.world.level.levelgen.feature.BambooFeature.place
//
// Survival/attachment gates are ported 1:1 from the corresponding block classes
// (SeagrassBlock.mayPlaceOn, GrowingPlantBlock.canSurvive (kelp), SeaPickleBlock.canSurvive,
// VineBlock.isAcceptableNeighbour -> MultifaceBlock.canAttachTo) reduced to the
// server-authoritative no-context face-sturdiness read (block.IsFaceSturdy) the worldgen
// Neighborhood exposes — identical to vanilla for every non-dynamic ground/wall block.
//
// All writes go ONLY through bctx.placeState (Neighborhood.SetBlock — cross-chunk +
// live-heightmap). Heightmap reads (getHeight OCEAN_FLOOR / WORLD_SURFACE) read the WG
// heightmaps the generator populated, so these features actually place during real
// generation (ocean floor under water for the aquatic set; the surface for bamboo).

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("seagrass", seagrassBody)
	registerFeatureBody("kelp", kelpBody)
	registerFeatureBody("sea_pickle", seaPickleBody)
	registerFeatureBody("vines", vinesBody)
	registerFeatureBody("bamboo", bambooBody)
}

// ---- shared heightmap + water helpers ----

// getHeightOceanFloorWG ports WorldGenLevel.getHeight(Heightmap.Types.OCEAN_FLOOR, x, z):
// the world-Y of the first block ABOVE the highest ocean-floor (motion-blocking, no-fluid)
// block in the column — the sea floor's top open cell. It reads the live OCEAN_FLOOR_WG
// heightmap the generator built (water counts as open for OCEAN_FLOOR, so this is the
// water cell just above the sea floor). Outside the 3x3 (or with no heightmap) it returns
// minY (an unloaded column reads as void), matching HeightmapMBNL's out-of-window contract.
func (b *bodyContext) getHeightOceanFloorWG(wx, wz int) int {
	if b.view == nil {
		return -64
	}
	ch, ok := b.view.chunkAt(wx, wz)
	if !ok || ch == nil || ch.HeightMaps.OceanFloorWG == nil {
		return b.view.minY
	}
	col := (wz&15)<<4 | (wx & 15)
	return ch.HeightMaps.OceanFloorWG.Get(col) + b.view.minY
}

// getHeightWorldSurfaceWG ports WorldGenLevel.getHeight(Heightmap.Types.WORLD_SURFACE, x, z):
// the world-Y of the first block above the highest non-air block. It reads the live
// WORLD_SURFACE_WG heightmap. Out-of-window -> minY (as above). BambooFeature uses it to
// find the surface (podzol goes at WORLD_SURFACE-1; the stalk starts at the origin).
func (b *bodyContext) getHeightWorldSurfaceWG(wx, wz int) int {
	if b.view == nil {
		return -64
	}
	ch, ok := b.view.chunkAt(wx, wz)
	if !ok || ch == nil || ch.HeightMaps.WorldSurfaceWG == nil {
		return b.view.minY
	}
	col := (wz&15)<<4 | (wx & 15)
	return ch.HeightMaps.WorldSurfaceWG.Get(col) + b.view.minY
}

// isWaterBlock ports BlockState.is(Blocks.WATER): the block at the position is the WATER
// block (source or any flowing level), NOT a waterlogged block. The aquatic feature bodies
// gate on the placement cell (and the cell above) being water. Vanilla's `.is(Blocks.WATER)`
// is a block-identity check, so a waterlogged block (kelp/seagrass are not) is excluded.
func isWaterBlock(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.Water)
	return ok
}

// isAir reports whether the state is air (WorldGenLevel.isEmptyBlock). It reuses block.IsAir,
// the same predicate the misc/patch bodies use.
func isAir(st block.StateID) bool { return block.IsAir(st) }

// ---- shared plant states (defaultBlockState + property overrides), resolved once ----

var (
	seagrassStateID     = mustState("minecraft:seagrass")
	tallSeagrassLowerID = block.ToStateID[block.TallSeagrass{Half: block.DoubleBlockHalfLower}]
	tallSeagrassUpperID = block.ToStateID[block.TallSeagrass{Half: block.DoubleBlockHalfUpper}]
	kelpPlantStateID    = block.ToStateID[block.KelpPlant{}]
	podzolStateID       = mustState("minecraft:podzol")
	waterStateID        = mustState("minecraft:water")
)

// mustState resolves a block id's defaultBlockState() id (registerDefaultState), the
// authoritative default the *.defaultBlockState() calls in the jar produce. A missing id is
// a build regression (the block table is generated), so panic rather than silently placing air.
func mustState(id string) block.StateID {
	sid, ok := block.DefaultStateID[id]
	if !ok {
		panic("world: no default state for " + id)
	}
	return sid
}

// kelpAgeState returns the KELP (head) state with age set — Blocks.KELP.defaultBlockState()
// .setValue(KelpBlock.AGE, age). KELP carries only the age property, so the struct key with
// Age is the exact state (KelpBlock.AGE range is 0..25).
func kelpAgeState(age int) block.StateID {
	sid, ok := block.ToStateID[block.Kelp{Age: block.Integer(age)}]
	if !ok {
		return waterStateID // unreachable for age in [20,24]; never place a bad state
	}
	return sid
}

// seaPickleState returns SEA_PICKLE.defaultBlockState().setValue(PICKLES, pickles). The
// default sea pickle is waterlogged=false; the feature never sets waterlogged, so the
// struct key carries Waterlogged:false (the default).
func seaPickleState(pickles int) block.StateID {
	sid, ok := block.ToStateID[block.SeaPickle{Pickles: block.Integer(pickles), Waterlogged: false}]
	if !ok {
		return 0
	}
	return sid
}

// ---- SeagrassFeature ----

// seagrassProbConfig is ProbabilityFeatureConfiguration: a single float `probability`.
type seagrassProbConfig struct {
	Probability float32 `json:"probability"`
}

var seagrassCache sync.Map // map[*feature.ConfiguredFeature]seagrassProbConfig

func decodeSeagrass(cf *feature.ConfiguredFeature) seagrassProbConfig {
	if v, ok := seagrassCache.Load(cf); ok {
		return v.(seagrassProbConfig)
	}
	var c seagrassProbConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			panic(fmt.Errorf("world: seagrass config %q: %w", cf.ID, err))
		}
	}
	seagrassCache.Store(cf, c)
	return c
}

// seagrassBody ports SeagrassFeature.place (CFR, 26.2-inner.jar). Draw order:
//
//	x = nextInt(8) - nextInt(8)
//	z = nextInt(8) - nextInt(8)
//	y = getHeight(OCEAN_FLOOR, ox+x, oz+z)
//	grassPos = (ox+x, y, oz+z)
//	if level.getBlockState(grassPos).is(WATER):
//	    isTall = nextDouble() < probability          // the draw happens INSIDE the water branch
//	    state = isTall ? TALL_SEAGRASS : SEAGRASS
//	    if state.canSurvive(level, grassPos):
//	        if isTall:
//	            upper = state.setValue(HALF, UPPER)
//	            if getBlockState(grassPos.above()).is(WATER):
//	                setBlock(grassPos, state); setBlock(above, upper)
//	        else: setBlock(grassPos, state)
//	        placedAny = true
//	return placedAny
//
// canSurvive (SeagrassBlock.mayPlaceOn on the block BELOW): isFaceSturdy(below, UP) &&
// !below.is(#cannot_support_seagrass). (BushBlock.canSurvive delegates to mayPlaceOn(below).)
func seagrassBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg := decodeSeagrass(cf)

	x := int(rng.NextIntN(8)) - int(rng.NextIntN(8))
	z := int(rng.NextIntN(8)) - int(rng.NextIntN(8))
	y := bctx.getHeightOceanFloorWG(pos.X+x, pos.Z+z)
	grassPos := placement.BlockPos{X: pos.X + x, Y: y, Z: pos.Z + z}

	if !isWaterBlock(bctx.getState(grassPos)) {
		return false
	}
	// nextDouble() is drawn ONLY when the placement cell is water (jar: inside the branch).
	isTall := rng.NextDouble() < float64(cfg.Probability)
	var state block.StateID
	if isTall {
		state = tallSeagrassLowerID
	} else {
		state = seagrassStateID
	}
	if !bctx.seagrassCanSurvive(grassPos) {
		return false
	}
	if isTall {
		above := placement.BlockPos{X: grassPos.X, Y: grassPos.Y + 1, Z: grassPos.Z}
		if isWaterBlock(bctx.getState(above)) {
			bctx.placeState(grassPos, state)
			bctx.placeState(above, tallSeagrassUpperID)
		}
	} else {
		bctx.placeState(grassPos, state)
	}
	return true
}

// seagrassCanSurvive ports SeagrassBlock.mayPlaceOn(below) (via BushBlock.canSurvive):
// isFaceSturdy(below, UP) && !below.is(#cannot_support_seagrass). The above-water and
// in-water conditions are handled by the feature body's explicit water checks.
func (b *bodyContext) seagrassCanSurvive(pos placement.BlockPos) bool {
	below := b.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if !block.IsFaceSturdy(below, block.Up, block.SupportFull) {
		return false
	}
	return !cannotSupportSeagrass(below)
}

// ---- KelpFeature ----

// kelpBody ports KelpFeature.place (CFR, 26.2-inner.jar). NoneFeatureConfiguration (no
// config fields). Draw order:
//
//	y = getHeight(OCEAN_FLOOR, ox, oz); kelpPos = (ox, y, oz)
//	if getBlockState(kelpPos).is(WATER):
//	    height = 1 + nextInt(10)
//	    for h in 0..height:
//	        if getBlockState(kelpPos).is(WATER) && getBlockState(kelpPos.above()).is(WATER)
//	           && kelpPlant.canSurvive(level, kelpPos):
//	            if h == height: setBlock(kelpPos, KELP.setValue(AGE, nextInt(4)+20)); placed++
//	            else:           setBlock(kelpPos, KELP_PLANT)
//	        else if h > 0:
//	            below = kelpPos.below()
//	            if !kelp(head).canSurvive(level, below) || getBlockState(below.below()).is(KELP): break
//	            setBlock(below, KELP.setValue(AGE, nextInt(4)+20)); placed++; break
//	        kelpPos = kelpPos.above()
//	return placed > 0
//
// The nextInt(4) AGE draw happens for each KELP-head setBlock (jar order). canSurvive is
// GrowingPlantBlock.canSurvive: below.is(kelp) || below.is(kelp_plant) || isFaceSturdy(below, UP).
func kelpBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	placed := 0
	y := bctx.getHeightOceanFloorWG(pos.X, pos.Z)
	kelpPos := placement.BlockPos{X: pos.X, Y: y, Z: pos.Z}

	if !isWaterBlock(bctx.getState(kelpPos)) {
		return false
	}
	height := 1 + int(rng.NextIntN(10))
	for h := 0; h <= height; h++ {
		above := placement.BlockPos{X: kelpPos.X, Y: kelpPos.Y + 1, Z: kelpPos.Z}
		if isWaterBlock(bctx.getState(kelpPos)) && isWaterBlock(bctx.getState(above)) && bctx.kelpCanSurvive(kelpPos) {
			if h == height {
				bctx.placeState(kelpPos, kelpAgeState(int(rng.NextIntN(4))+20))
				placed++
			} else {
				bctx.placeState(kelpPos, kelpPlantStateID)
			}
		} else if h > 0 {
			below := placement.BlockPos{X: kelpPos.X, Y: kelpPos.Y - 1, Z: kelpPos.Z}
			belowBelow := placement.BlockPos{X: kelpPos.X, Y: kelpPos.Y - 2, Z: kelpPos.Z}
			if !bctx.kelpCanSurvive(below) || isKelpBlock(bctx.getState(belowBelow)) {
				break
			}
			bctx.placeState(below, kelpAgeState(int(rng.NextIntN(4))+20))
			placed++
			break
		}
		kelpPos = above
	}
	return placed > 0
}

// kelpCanSurvive ports GrowingPlantBlock.canSurvive for kelp (growthDirection UP,
// canAttachTo always true): the block BELOW (pos.relative(DOWN)) is kelp OR kelp_plant OR
// isFaceSturdy(below, UP). (The head/body distinction does not change the below check.)
func (b *bodyContext) kelpCanSurvive(pos placement.BlockPos) bool {
	below := b.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if isKelpBlock(below) || below == kelpPlantStateID {
		return true
	}
	return block.IsFaceSturdy(below, block.Up, block.SupportFull)
}

// ---- SeaPickleFeature ----

// seaPickleCountConfig is CountConfiguration: an IntProvider `count` (a bare int in the
// data -> ConstantInt). The parser accepts the bare-int / {type:...} IntProvider forms.
type seaPickleCountConfig struct {
	count *vegIntProvider
}

var seaPickleCache sync.Map // map[*feature.ConfiguredFeature]seaPickleCountConfig

func decodeSeaPickle(cf *feature.ConfiguredFeature) seaPickleCountConfig {
	if v, ok := seaPickleCache.Load(cf); ok {
		return v.(seaPickleCountConfig)
	}
	var envelope struct {
		Count json.RawMessage `json:"count"`
	}
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			panic(fmt.Errorf("world: sea_pickle config %q: %w", cf.ID, err))
		}
	}
	ip, err := parseVegIntProvider(envelope.Count)
	if err != nil {
		panic(fmt.Errorf("world: sea_pickle count %q: %w", cf.ID, err))
	}
	c := seaPickleCountConfig{count: ip}
	seaPickleCache.Store(cf, c)
	return c
}

// seaPickleBody ports SeaPickleFeature.place (CFR, 26.2-inner.jar). Draw order:
//
//	count = config.count().sample(random)             // constant int -> 0 draws
//	for i in 0..count:
//	    x = nextInt(8) - nextInt(8)
//	    z = nextInt(8) - nextInt(8)
//	    y = getHeight(OCEAN_FLOOR, ox+x, oz+z); picklePos = (ox+x, y, oz+z)
//	    pickleState = SEA_PICKLE.setValue(PICKLES, nextInt(4)+1)   // the pickle-count draw
//	    if !getBlockState(picklePos).is(WATER) || !pickleState.canSurvive(level, picklePos): continue
//	    setBlock(picklePos, pickleState); placed++
//	return placed > 0
//
// The nextInt(4) PICKLES draw is consumed BEFORE the water/canSurvive test (the setValue is
// evaluated first) — a determinism-critical ordering. canSurvive: mayPlaceOn(below) =
// !collisionFaceShape(below, UP).isEmpty() || isFaceSturdy(below, UP). Reduced to
// isCollisionShapeFullBlock(below) || isFaceSturdy(below, UP) (the no-context proxy the
// Neighborhood exposes; a full collision cube has a non-empty UP face shape).
func seaPickleBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg := decodeSeaPickle(cf)
	placed := 0
	count := cfg.count.sample(rng)
	for i := 0; i < count; i++ {
		x := int(rng.NextIntN(8)) - int(rng.NextIntN(8))
		z := int(rng.NextIntN(8)) - int(rng.NextIntN(8))
		y := bctx.getHeightOceanFloorWG(pos.X+x, pos.Z+z)
		picklePos := placement.BlockPos{X: pos.X + x, Y: y, Z: pos.Z + z}
		pickleState := seaPickleState(int(rng.NextIntN(4)) + 1)
		if !isWaterBlock(bctx.getState(picklePos)) || !bctx.seaPickleCanSurvive(picklePos) {
			continue
		}
		bctx.placeState(picklePos, pickleState)
		placed++
	}
	return placed > 0
}

// seaPickleCanSurvive ports SeaPickleBlock.canSurvive -> mayPlaceOn(below):
// !getCollisionShape(below).getFaceShape(UP).isEmpty() || isFaceSturdy(below, UP). The
// worldgen Neighborhood exposes the no-context collision-full-block + face-sturdy caches;
// a full collision cube has a non-empty UP face shape, so the OR reduces to
// isCollisionShapeFullBlock(below) || isFaceSturdy(below, UP).
func (b *bodyContext) seaPickleCanSurvive(pos placement.BlockPos) bool {
	below := b.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	return block.IsCollisionShapeFullBlock(below) || block.IsFaceSturdy(below, block.Up, block.SupportFull)
}

// ---- VinesFeature ----

// vinesBody ports VinesFeature.place (CFR, 26.2-inner.jar). NoneFeatureConfiguration.
// ZERO rng draws (vines are purely positional). Algorithm:
//
//	if !level.isEmptyBlock(origin): return false
//	for direction in Direction.values():        // [DOWN, UP, NORTH, SOUTH, WEST, EAST]
//	    if direction == DOWN: continue
//	    if !VineBlock.isAcceptableNeighbour(level, origin.relative(direction), direction): continue
//	    setBlock(origin, VINE.setValue(getPropertyForFace(direction), true)); return true
//	return false
//
// isAcceptableNeighbour(level, neighbourPos, dir) = MultifaceBlock.canAttachTo(level, dir,
// neighbourPos, neighbourState) = isFaceFull(support, dir.opposite) || isFaceFull(collision,
// dir.opposite). Reduced to the no-context isFaceSturdy(neighbourState, dir.opposite, FULL)
// || isCollisionShapeFullBlock(neighbour) — a full cube is sturdy on every face; a dynamic
// support shape reduces to its sturdy cache. Places on the FIRST acceptable direction
// (Direction.values() order), returning true immediately (jar early-return).
func vinesBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	_ levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	if !isAir(bctx.getState(pos)) {
		return false
	}
	for _, d := range directionValues {
		if d.dir == block.Down {
			continue
		}
		neighbour := placement.BlockPos{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		if !bctx.vineAcceptableNeighbour(neighbour, d.dir) {
			continue
		}
		st, ok := vineStateForFace(d.dir)
		if !ok {
			continue
		}
		bctx.placeState(pos, st)
		return true
	}
	return false
}

// vineAcceptableNeighbour ports VineBlock.isAcceptableNeighbour -> MultifaceBlock.canAttachTo:
// the block toward `dir` is full-faced on the face pointing back at the vine (dir.opposite).
// Support-shape OR collision-shape full-face; the no-context caches give both.
func (b *bodyContext) vineAcceptableNeighbour(neighbour placement.BlockPos, dir block.Direction) bool {
	st := b.getState(neighbour)
	opp := oppositeDirection(dir)
	return block.IsFaceSturdy(st, opp, block.SupportFull) || block.IsCollisionShapeFullBlock(st)
}

// vineStateForFace returns VINE.defaultBlockState().setValue(getPropertyForFace(dir), true).
// VineBlock.PROPERTY_BY_DIRECTION maps UP/NORTH/SOUTH/EAST/WEST -> the boolean face property
// (there is no DOWN face — VinesFeature already skips DOWN). Only the single face for `dir`
// is set true (the default vine has all faces false).
func vineStateForFace(dir block.Direction) (block.StateID, bool) {
	var v block.Vine
	switch dir {
	case block.Up:
		v.Up = true
	case block.North:
		v.North = true
	case block.South:
		v.South = true
	case block.West:
		v.West = true
	case block.East:
		v.East = true
	default:
		return 0, false
	}
	sid, ok := block.ToStateID[v]
	return sid, ok
}

// ---- BambooFeature ----

// bambooProbConfig is ProbabilityFeatureConfiguration: `probability` (the podzol-patch
// chance).
type bambooProbConfig struct {
	Probability float32 `json:"probability"`
}

var bambooCache sync.Map // map[*feature.ConfiguredFeature]bambooProbConfig

func decodeBamboo(cf *feature.ConfiguredFeature) bambooProbConfig {
	if v, ok := bambooCache.Load(cf); ok {
		return v.(bambooProbConfig)
	}
	var c bambooProbConfig
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			panic(fmt.Errorf("world: bamboo config %q: %w", cf.ID, err))
		}
	}
	bambooCache.Store(cf, c)
	return c
}

// The four BambooFeature constant states (jar static fields):
//
//	BAMBOO_TRUNK       = BAMBOO.default.setValue(AGE,1).setValue(LEAVES,NONE).setValue(STAGE,0)
//	BAMBOO_FINAL_LARGE = BAMBOO_TRUNK.setValue(LEAVES,LARGE).setValue(STAGE,1)
//	BAMBOO_TOP_LARGE   = BAMBOO_TRUNK.setValue(LEAVES,LARGE)
//	BAMBOO_TOP_SMALL   = BAMBOO_TRUNK.setValue(LEAVES,SMALL)
var (
	bambooTrunkID      = block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesNone, Stage: 0}]
	bambooFinalLargeID = block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesLarge, Stage: 1}]
	bambooTopLargeID   = block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesLarge, Stage: 0}]
	bambooTopSmallID   = block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesSmall, Stage: 0}]
	// BAMBOO.defaultBlockState() (age0/none/stage0) for the isEmptyBlock canSurvive gate.
	bambooDefaultID = mustState("minecraft:bamboo")
)

// bambooBody ports BambooFeature.place (CFR, 26.2-inner.jar). Draw order:
//
//	if isEmptyBlock(origin):
//	    if BAMBOO.default.canSurvive(level, origin):
//	        height = nextInt(12) + 5
//	        if nextFloat() < probability:
//	            r = nextInt(4) + 1
//	            for xx in [ox-r..ox+r], zz in [oz-r..oz+r] (xx outer, zz inner):
//	                if (xx-ox)^2 + (zz-oz)^2 > r*r: continue
//	                podzolPos = (xx, getHeight(WORLD_SURFACE, xx, zz)-1, zz)
//	                if !getBlockState(podzolPos).is(#beneath_bamboo_podzol_replaceable): continue
//	                setBlock(podzolPos, PODZOL)
//	        for i in 0..height while isEmptyBlock(bambooPos):
//	            setBlock(bambooPos, BAMBOO_TRUNK); bambooPos.move(UP)
//	        if bambooPos.getY() - origin.getY() >= 3:
//	            setBlock(bambooPos, BAMBOO_FINAL_LARGE)
//	            setBlock(bambooPos.move(DOWN), BAMBOO_TOP_LARGE)
//	            setBlock(bambooPos.move(DOWN), BAMBOO_TOP_SMALL)
//	    placed++       // incremented whenever the origin was empty (regardless of canSurvive)
//	return placed > 0
//
// canSurvive (BambooStalkBlock -> BambooSaplingBlock.mayPlaceOn semantics is
// isFaceSturdy(below, UP) over #bamboo_plantable_on; but the feature uses BAMBOO.default's
// canSurvive which is BushBlock.canSurvive -> mayPlaceOn(below).is(#bamboo_plantable_on)).
// See bambooCanSurvive below. The `placed++` sits OUTSIDE the canSurvive block, so the
// feature reports success whenever the origin cell was air.
func bambooBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg := decodeBamboo(cf)
	if !isAir(bctx.getState(pos)) {
		return false
	}
	if bctx.bambooCanSurvive(pos) {
		height := int(rng.NextIntN(12)) + 5
		if rng.NextFloat() < cfg.Probability {
			r := int(rng.NextIntN(4)) + 1
			for xx := pos.X - r; xx <= pos.X+r; xx++ {
				for zz := pos.Z - r; zz <= pos.Z+r; zz++ {
					xd := xx - pos.X
					zd := zz - pos.Z
					if xd*xd+zd*zd > r*r {
						continue
					}
					py := bctx.getHeightWorldSurfaceWG(xx, zz) - 1
					podzolPos := placement.BlockPos{X: xx, Y: py, Z: zz}
					if !beneathBambooPodzolReplaceable(bctx.getState(podzolPos)) {
						continue
					}
					bctx.placeState(podzolPos, podzolStateID)
				}
			}
		}
		// Grow the stalk upward while the cell is empty.
		bambooY := pos.Y
		for i := 0; i < height && isAir(bctx.getState(placement.BlockPos{X: pos.X, Y: bambooY, Z: pos.Z})); i++ {
			bctx.placeState(placement.BlockPos{X: pos.X, Y: bambooY, Z: pos.Z}, bambooTrunkID)
			bambooY++
		}
		if bambooY-pos.Y >= 3 {
			bctx.placeState(placement.BlockPos{X: pos.X, Y: bambooY, Z: pos.Z}, bambooFinalLargeID)
			bambooY--
			bctx.placeState(placement.BlockPos{X: pos.X, Y: bambooY, Z: pos.Z}, bambooTopLargeID)
			bambooY--
			bctx.placeState(placement.BlockPos{X: pos.X, Y: bambooY, Z: pos.Z}, bambooTopSmallID)
		}
	}
	// placed++ is unconditional inside the isEmptyBlock branch: success whenever origin was air.
	return true
}

// bambooCanSurvive ports BAMBOO.defaultBlockState().canSurvive (BambooStalkBlock ->
// BushBlock.canSurvive -> mayPlaceOn(below)). BambooStalkBlock.mayPlaceOn tests
// below.is(#bamboo_plantable_on). That tag (dirt/grass/sand/gravel/mud/podzol/...) is not
// among the tiny embedded set; the conservative faithful proxy the Neighborhood exposes is
// isFaceSturdy(below, UP), which every plantable-on ground block satisfies (and air/water
// do not) — so a bamboo stalk never floats. Documented conservative: the exact
// #bamboo_plantable_on membership is deferred (it only narrows, never widens, the ground
// set, and the surface under a bamboo-jungle decoration is always plantable ground).
func (b *bodyContext) bambooCanSurvive(pos placement.BlockPos) bool {
	below := b.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	return block.IsFaceSturdy(below, block.Up, block.SupportFull)
}

// ---- tag-membership helpers (resolved once from the embedded jar tag JSONs) ----

var (
	cannotSupportSeagrassOnce sync.Once
	cannotSupportSeagrassSet  map[block.StateID]bool

	beneathBambooPodzolOnce sync.Once
	beneathBambooPodzolSet  map[block.StateID]bool
)

// cannotSupportSeagrass reports st.is(#minecraft:cannot_support_seagrass) (magma_block).
// Resolved once from the embedded tag JSON (data.BlockTag), never hand-listed.
func cannotSupportSeagrass(st block.StateID) bool {
	cannotSupportSeagrassOnce.Do(func() {
		cannotSupportSeagrassSet = resolveTagStateSet("cannot_support_seagrass")
	})
	return cannotSupportSeagrassSet[st]
}

// beneathBambooPodzolReplaceable reports st.is(#minecraft:beneath_bamboo_podzol_replaceable)
// (== #substrate_overworld: dirt/grass/mud/moss/... blocks). Resolved once from the embedded
// tag chain (data.BlockTag), never hand-listed.
func beneathBambooPodzolReplaceable(st block.StateID) bool {
	beneathBambooPodzolOnce.Do(func() {
		beneathBambooPodzolSet = resolveTagStateSet("beneath_bamboo_podzol_replaceable")
	})
	return beneathBambooPodzolSet[st]
}

// resolveTagStateSet expands a block tag id (data.BlockTag) to the set of every StateID of
// each member block — the same mechanism supportsVegetation uses in feature_patch.go. A
// resolution failure is a build/extraction regression (the tag JSONs are embedded), so it
// panics rather than silently degrading a survival gate.
func resolveTagStateSet(tag string) map[block.StateID]bool {
	ids, err := data.BlockTag(tag)
	if err != nil {
		panic(fmt.Errorf("world: resolving #minecraft:%s: %w", tag, err))
	}
	set := make(map[block.StateID]bool)
	for sid, b := range block.StateList {
		if ids[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// ---- Direction.values() table + opposite (block.Direction ordinals match vanilla) ----

// dirEntry pairs a block.Direction with its unit (dx,dy,dz) step. The block.Direction
// ordinal order (DOWN,UP,NORTH,SOUTH,WEST,EAST) equals vanilla Direction.values(), so this
// table IS Direction.values() with each direction's normal vector.
type dirEntry struct {
	dir        block.Direction
	dx, dy, dz int
}

// directionValues is Direction.values() in ordinal order with the vanilla normals:
// DOWN(0,-1,0) UP(0,1,0) NORTH(0,0,-1) SOUTH(0,0,1) WEST(-1,0,0) EAST(1,0,0).
var directionValues = [6]dirEntry{
	{block.Down, 0, -1, 0},
	{block.Up, 0, 1, 0},
	{block.North, 0, 0, -1},
	{block.South, 0, 0, 1},
	{block.West, -1, 0, 0},
	{block.East, 1, 0, 0},
}

// oppositeDirection ports Direction.getOpposite() for the six faces.
func oppositeDirection(d block.Direction) block.Direction {
	switch d {
	case block.Down:
		return block.Up
	case block.Up:
		return block.Down
	case block.North:
		return block.South
	case block.South:
		return block.North
	case block.West:
		return block.East
	case block.East:
		return block.West
	}
	return d
}

// isKelpBlock reports st.is(Blocks.KELP) (the KELP head block, any age). Used by the kelp
// body's "kelp already below" break test. It checks block identity (any Kelp state).
func isKelpBlock(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.Kelp)
	return ok
}
