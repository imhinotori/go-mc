package world

// feature_coral.go ports the three coral configured-feature bodies and their shared
// CoralFeature base logic, 1:1 with Minecraft Java 26.2 (verified via javap -c against
// temp/cache/26.2-inner.jar):
//
//   - net.minecraft.world.level.levelgen.feature.CoralFeature.{place,placeCoralBlock}
//   - net.minecraft.world.level.levelgen.feature.CoralTreeFeature.placeFeature
//   - net.minecraft.world.level.levelgen.feature.CoralClawFeature.placeFeature
//   - net.minecraft.world.level.levelgen.feature.CoralMushroomFeature.placeFeature
//
// RNG DRAW ORDER is the determinism contract; every nextInt/nextFloat is reproduced in the
// exact jar order. CoralFeature.place picks a random coral BLOCK family from BlockTags.
// CORAL_BLOCKS (one nextInt over the 5 families via HolderSet.getRandomElement ->
// Util.getRandomSafe = list.get(nextInt(size))), then delegates to the subclass placeFeature,
// which grows the structure by repeatedly calling placeCoralBlock. placeCoralBlock places the
// coral block (if pos is water/coral AND above is water), then decorates: a coral fan on top
// (nextFloat < 0.25 -> pick from CORALS; else nextFloat < 0.05 -> a sea pickle), then a wall
// coral fan on each of the 4 horizontal neighbours that are water (nextFloat < 0.2 gate).
//
// TAG ORDER (authoritative: data/minecraft/tags/block/ JSON in 26.2-inner.jar; the runtime
// server/registrydata copies are byte-identical). Tags flatten nested tags in declared order
// with dedup (LinkedHashSet), so:
//   CORAL_BLOCKS = [tube_coral_block, brain_coral_block, bubble_coral_block,
//                   fire_coral_block, horn_coral_block]                       (size 5)
//   CORALS       = coral_plants ++ [tube_coral_fan..horn_coral_fan]
//                = [tube_coral, brain_coral, bubble_coral, fire_coral, horn_coral,
//                   tube_coral_fan, brain_coral_fan, bubble_coral_fan,
//                   fire_coral_fan, horn_coral_fan]                           (size 10)
//   WALL_CORALS  = [tube_coral_wall_fan, brain_coral_wall_fan, bubble_coral_wall_fan,
//                   fire_coral_wall_fan, horn_coral_wall_fan]                 (size 5)
// The families are hard-coded here in that exact order (deterministic ordered slices, never a
// range-over-map) so nextInt(N) selects the same element the jar tag registry would.
//
// Coral plant/fan/wall-fan Block.defaultBlockState() has WATERLOGGED=true (verified via javap
// BaseCoralPlantTypeBlock init iconst_1), so the placed fans are waterlogged=true. SeaPickle
// default is PICKLES=1, WATERLOGGED=true; the feature overrides PICKLES = nextInt(4)+1.
//
// All writes go through bctx.placeState (Neighborhood.SetBlock -- cross-chunk + live heightmap),
// matching WorldGenLevel.setBlock(pos, state, flag) (the flag byte is a client-notify mask the
// worldgen writer does not model).

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("coral_tree", coralTreeBody)
	registerFeatureBody("coral_claw", coralClawBody)
	registerFeatureBody("coral_mushroom", coralMushroomBody)
}

// coralBlockFamilies is BlockTags.CORAL_BLOCKS in tag order (the 5 live coral blocks). The
// place() pick indexes this with nextInt(5).
var coralBlockFamilies = [5]block.StateID{
	block.ToStateID[block.TubeCoralBlock{}],
	block.ToStateID[block.BrainCoralBlock{}],
	block.ToStateID[block.BubbleCoralBlock{}],
	block.ToStateID[block.FireCoralBlock{}],
	block.ToStateID[block.HornCoralBlock{}],
}

// coralFanFamilies is BlockTags.CORALS in tag order (5 plants then 5 fans). placeCoralBlock's
// on-top fan pick indexes this with nextInt(10). Fans/plants default WATERLOGGED=true.
var coralFanFamilies = [10]block.StateID{
	block.ToStateID[block.TubeCoral{Waterlogged: true}],
	block.ToStateID[block.BrainCoral{Waterlogged: true}],
	block.ToStateID[block.BubbleCoral{Waterlogged: true}],
	block.ToStateID[block.FireCoral{Waterlogged: true}],
	block.ToStateID[block.HornCoral{Waterlogged: true}],
	block.ToStateID[block.TubeCoralFan{Waterlogged: true}],
	block.ToStateID[block.BrainCoralFan{Waterlogged: true}],
	block.ToStateID[block.BubbleCoralFan{Waterlogged: true}],
	block.ToStateID[block.FireCoralFan{Waterlogged: true}],
	block.ToStateID[block.HornCoralFan{Waterlogged: true}],
}

// coralCorals is the set of state ids in BlockTags.CORALS (the on-top fan pick pool AND the
// membership test placeCoralBlock uses for pos-is-a-coral -- state.is(BlockTags.CORALS)).
// It is exactly the 10 coralFanFamilies members; kept as a set for O(1) membership.
var coralCorals = func() map[block.StateID]bool {
	m := make(map[block.StateID]bool, len(coralFanFamilies))
	for _, id := range coralFanFamilies {
		m[id] = true
	}
	return m
}()

// coralWallFanFor returns the wall coral fan state (BlockTags.WALL_CORALS[family]) for a
// horizontal facing. family is the nextInt(5) index chosen from WALL_CORALS; the facing is set
// via BaseCoralWallFanBlock.FACING (lambda in placeCoralBlock). Default WATERLOGGED=true.
func coralWallFanFor(family int, facing block.Direction) block.StateID {
	switch family {
	case 0:
		return block.ToStateID[block.TubeCoralWallFan{Facing: facing, Waterlogged: true}]
	case 1:
		return block.ToStateID[block.BrainCoralWallFan{Facing: facing, Waterlogged: true}]
	case 2:
		return block.ToStateID[block.BubbleCoralWallFan{Facing: facing, Waterlogged: true}]
	case 3:
		return block.ToStateID[block.FireCoralWallFan{Facing: facing, Waterlogged: true}]
	default:
		return block.ToStateID[block.HornCoralWallFan{Facing: facing, Waterlogged: true}]
	}
}

// coralIsWater ports BlockState.is(Blocks.WATER): a property-agnostic block-id match (any
// water level). In worldgen water is always Level 0, but the match is by block type.
func coralIsWater(st block.StateID) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[st].(block.Water)
	return ok
}

// coralPickBlockFamily ports the CoralFeature.place random block pick:
// BuiltInRegistries.BLOCK.getRandomElementOf(CORAL_BLOCKS, random) -> HolderSet.
// getRandomElement -> Util.getRandomSafe(contents, rng) = contents.get(rng.nextInt(size)).
// One nextInt(5) draw. The Holder-value unwrap is a no-op on the state.
func coralPickBlockFamily(rng levelgen.RandomSource) block.StateID {
	return coralBlockFamilies[int(rng.NextIntN(int32(len(coralBlockFamilies))))]
}

// coralPlace ports CoralFeature.place shared entry: pick a random coral BLOCK family (1 draw),
// then run the subclass placeFeature with that family default state. The Optional.isEmpty
// short-circuit never triggers (the tag is non-empty), so the pick always yields a state.
func coralPlace(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos, placeFeature func(*bodyContext, levelgen.RandomSource, placement.BlockPos, block.StateID) bool) bool {
	familyState := coralPickBlockFamily(rng)
	return placeFeature(bctx, rng, pos, familyState)
}

// placeCoralBlock ports CoralFeature.placeCoralBlock (javap -c). Returns false (placing
// nothing) when the position/above are not both water-compatible; otherwise it sets the coral
// block, decorates the top (fan or sea pickle), and adds wall fans on watery neighbours, then
// returns true.
//
//	above = pos.above()
//	posState = getBlockState(pos)
//	if !(posState.is(WATER) || posState.is(CORALS)):        return false
//	if !getBlockState(above).is(WATER):                     return false
//	setBlock(pos, coralBlockState, flag 3)
//	if nextFloat() < 0.25:
//	    getRandomElementOf(CORALS).map(defaultBlockState).ifPresent(s -> setBlock(above, s, 2))
//	else if nextFloat() < 0.05:
//	    setBlock(above, SEA_PICKLE.default.setValue(PICKLES, nextInt(4)+1), flag 2)
//	for dir in Direction.Plane.HORIZONTAL:   // ordinal order [N,E,S,W]
//	    if nextFloat() < 0.2:
//	        rel = pos.relative(dir)
//	        if getBlockState(rel).is(WATER):
//	            getRandomElementOf(WALL_CORALS).map(defaultBlockState)
//	                .ifPresent(s -> setBlock(rel, s.trySetValue(FACING, dir), flag 2))
func placeCoralBlock(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos, coralBlockState block.StateID) bool {
	above := placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	posState := bctx.getState(pos)
	if !(coralIsWater(posState) || coralCorals[posState]) {
		return false
	}
	if !coralIsWater(bctx.getState(above)) {
		return false
	}
	bctx.placeState(pos, coralBlockState)

	if rng.NextFloat() < 0.25 {
		// getRandomElementOf(CORALS): nextInt(10) over the fan/plant pool.
		fan := coralFanFamilies[int(rng.NextIntN(int32(len(coralFanFamilies))))]
		bctx.placeState(above, fan)
	} else if rng.NextFloat() < 0.05 {
		// SEA_PICKLE with PICKLES = nextInt(4)+1 (WATERLOGGED default true).
		pickles := int(rng.NextIntN(4)) + 1
		bctx.placeState(above, block.ToStateID[block.SeaPickle{Pickles: block.Integer(pickles), Waterlogged: true}])
	}

	// Direction.Plane.HORIZONTAL iterator: ordinal order [NORTH, EAST, SOUTH, WEST].
	for _, d := range coralHorizontals {
		if rng.NextFloat() < 0.2 {
			rel := placement.BlockPos{X: pos.X + d.dx, Y: pos.Y, Z: pos.Z + d.dz}
			if coralIsWater(bctx.getState(rel)) {
				// getRandomElementOf(WALL_CORALS): nextInt(5) over the wall-fan families.
				family := int(rng.NextIntN(int32(len(coralBlockFamilies))))
				bctx.placeState(rel, coralWallFanFor(family, d.facing))
			}
		}
	}
	return true
}

// coralHorizontals is Direction.Plane.HORIZONTAL in iterator (ordinal) order: NORTH, EAST,
// SOUTH, WEST. placeCoralBlock iterates this; coral_tree/coral_claw shuffle copies of it.
var coralHorizontals = [4]coralDir{
	{facing: block.North, dx: 0, dz: -1},
	{facing: block.East, dx: 1, dz: 0},
	{facing: block.South, dx: 0, dz: 1},
	{facing: block.West, dx: -1, dz: 0},
}

type coralDir struct {
	facing block.Direction
	dx, dz int
}

// coralClockWise / coralCounterClockWise port Direction.getClockWise / getCounterClockWise for
// the 4 horizontal facings (the compass cycle N -> E -> S -> W -> N is clockwise).
func coralClockWise(d coralDir) coralDir {
	switch d.facing {
	case block.North:
		return coralHorizontals[1] // EAST
	case block.East:
		return coralHorizontals[2] // SOUTH
	case block.South:
		return coralHorizontals[3] // WEST
	default: // WEST
		return coralHorizontals[0] // NORTH
	}
}

func coralCounterClockWise(d coralDir) coralDir {
	switch d.facing {
	case block.North:
		return coralHorizontals[3] // WEST
	case block.West:
		return coralHorizontals[2] // SOUTH
	case block.South:
		return coralHorizontals[1] // EAST
	default: // EAST
		return coralHorizontals[0] // NORTH
	}
}

func coralOpposite(d coralDir) coralDir {
	switch d.facing {
	case block.North:
		return coralHorizontals[2] // SOUTH
	case block.South:
		return coralHorizontals[0] // NORTH
	case block.East:
		return coralHorizontals[3] // WEST
	default: // WEST
		return coralHorizontals[1] // EAST
	}
}

// coralShuffle ports Util.shuffle(list, rng): Fisher-Yates from the end, swap(i, nextInt(i+1))
// for i in [size-1 .. 1]. One draw per i.
func coralShuffle(s []coralDir, rng levelgen.RandomSource) {
	for i := len(s) - 1; i > 0; i-- {
		j := int(rng.NextIntN(int32(i + 1)))
		s[i], s[j] = s[j], s[i]
	}
}

// coralUpDir is the UP direction as a coralDir (the claw vertical moveDir option).
var coralUpDir = coralDir{facing: block.Up, dx: 0, dz: 0}

// coralMove applies MutableBlockPos.move(dir): horizontal step for a horizontal facing, +Y for
// UP, -Y for DOWN. Only UP and the 4 horizontals occur in the claw port.
func coralMove(p placement.BlockPos, d coralDir) placement.BlockPos {
	switch d.facing {
	case block.Up:
		p.Y++
	case block.Down:
		p.Y--
	default:
		p.X += d.dx
		p.Z += d.dz
	}
	return p
}

// coralMoveOpposite returns move(dir.getOpposite()) as a coralDir for coralMove. UP opposite is
// DOWN; horizontals use coralOpposite.
func coralMoveOpposite(d coralDir) coralDir {
	if d.facing == block.Up {
		return coralDir{facing: block.Down}
	}
	if d.facing == block.Down {
		return coralUpDir
	}
	return coralOpposite(d)
}

// ---- coral_tree (CoralTreeFeature) ----

func coralTreeBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	return coralPlace(bctx, rng, pos, coralTreePlaceFeature)
}

// coralTreePlaceFeature ports CoralTreeFeature.placeFeature (javap -c). Trunk column of
// nextInt(3)+1 coral blocks (abort early if any fails), then nextInt(3)+2 branches over a
// shuffled copy of HORIZONTAL, each branch a run of nextInt(5)+2 that steps UP and occasionally
// (o==0, or count>=2 with nextFloat()<0.25) steps outward, resetting count.
func coralTreePlaceFeature(bctx *bodyContext, rng levelgen.RandomSource, origin placement.BlockPos, coralBlockState block.StateID) bool {
	mutable := origin
	i := int(rng.NextIntN(3)) + 1
	for k := 0; k < i; k++ {
		if !placeCoralBlock(bctx, rng, mutable, coralBlockState) {
			return true
		}
		mutable.Y++
	}
	base := mutable

	m := int(rng.NextIntN(3)) + 2

	dirs := make([]coralDir, len(coralHorizontals))
	copy(dirs, coralHorizontals[:])
	coralShuffle(dirs, rng)
	dirs = dirs[:m]

	for _, dir := range dirs {
		mutable = base
		mutable.X += dir.dx
		mutable.Z += dir.dz
		n := int(rng.NextIntN(5)) + 2
		count := 0
		for o := 0; o < n; o++ {
			if !placeCoralBlock(bctx, rng, mutable, coralBlockState) {
				break
			}
			count++
			mutable.Y++
			if o == 0 || (count >= 2 && rng.NextFloat() < 0.25) {
				mutable.X += dir.dx
				mutable.Z += dir.dz
				count = 0
			}
		}
	}
	return true
}

// ---- coral_claw (CoralClawFeature) ----

func coralClawBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	return coralPlace(bctx, rng, pos, coralClawPlaceFeature)
}

// coralClawPlaceFeature ports CoralClawFeature.placeFeature (javap -c). places the base coral
// (abort if it fails), picks a random horizontal dir (nextInt(4)), makes nextInt(2)+2 claws
// over a shuffled [dir, dir.clockwise, dir.counterclockwise]. Each claw: a horizontal run of
// nextInt(2)+1 along moveDir, then a diagonal run of k that steps dir and occasionally UP.
func coralClawPlaceFeature(bctx *bodyContext, rng levelgen.RandomSource, origin placement.BlockPos, coralBlockState block.StateID) bool {
	if !placeCoralBlock(bctx, rng, origin, coralBlockState) {
		return false
	}
	dir := coralHorizontals[int(rng.NextIntN(4))]
	i := int(rng.NextIntN(2)) + 2

	dirs := []coralDir{dir, coralClockWise(dir), coralCounterClockWise(dir)}
	coralShuffle(dirs, rng)
	dirs = dirs[:i]

	for _, dir2 := range dirs {
		mutable := origin
		j := int(rng.NextIntN(2)) + 1
		mutable.X += dir2.dx
		mutable.Z += dir2.dz

		var moveDir coralDir
		var k int
		if dir2.facing == dir.facing {
			moveDir = dir
			k = int(rng.NextIntN(3)) + 2
		} else {
			mutable.Y++
			if int(rng.NextIntN(2)) == 0 {
				moveDir = dir2
			} else {
				moveDir = coralUpDir
			}
			k = int(rng.NextIntN(3)) + 3
		}

		for mm := 0; mm < j; mm++ {
			if !placeCoralBlock(bctx, rng, mutable, coralBlockState) {
				break
			}
			mutable = coralMove(mutable, moveDir)
		}
		mutable = coralMove(mutable, coralMoveOpposite(moveDir))
		mutable.Y++

		for n := 0; n < k; n++ {
			mutable.X += dir.dx
			mutable.Z += dir.dz
			if !placeCoralBlock(bctx, rng, mutable, coralBlockState) {
				break
			}
			if rng.NextFloat() < 0.25 {
				mutable.Y++
			}
		}
	}
	return true
}

// ---- coral_mushroom (CoralMushroomFeature) ----

func coralMushroomBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	return coralPlace(bctx, rng, pos, coralMushroomPlaceFeature)
}

// coralMushroomPlaceFeature ports CoralMushroomFeature.placeFeature (javap -c). Draws x/y/z
// extents (nextInt(3)+3 each) and a downward drop l (nextInt(3)+1). Iterates a box (y OUTER,
// x MIDDLE, z INNER, all inclusive), dropping each cell DOWN by l, and on every SHELL cell
// (any face/edge/corner; strict interior skipped) places a coral block with nextFloat()<0.1.
func coralMushroomPlaceFeature(bctx *bodyContext, rng levelgen.RandomSource, origin placement.BlockPos, coralBlockState block.StateID) bool {
	i := int(rng.NextIntN(3)) + 3
	j := int(rng.NextIntN(3)) + 3
	k := int(rng.NextIntN(3)) + 3
	l := int(rng.NextIntN(3)) + 1

	for p := 0; p <= j; p++ {
		for q := 0; q <= i; q++ {
			for r := 0; r <= k; r++ {
				mutable := placement.BlockPos{
					X: p + origin.X,
					Y: q + origin.Y,
					Z: r + origin.Z,
				}
				mutable.Y -= l

				// Place predicate ported EXACTLY from the bytecode branch cascade
				// (offsets 118-235). A cell reaches the nextFloat()<0.1 place (offset 238)
				// only when it passes all four guards; any guard sends it to 266 (skip, NO
				// draw). Let pEdge=(p==0||p==j), qEdge=(q==0||q==i), rEdge=(r==0||r==k):
				//   A (118-142): skip if pEdge && qEdge
				//   B (145-169): skip if rEdge && qEdge
				//   C (172-196): skip if pEdge && rEdge
				//   D (199-235): skip if strict interior (no edge at all)
				// The survivors are exactly the FACE-CENTER cells (exactly ONE coordinate at an
				// extreme), never an edge/corner (two/three extremes) nor the interior. The
				// nextFloat() draw sits at offset 238, AFTER every guard, so it happens ONLY
				// for a surviving cell.
				pEdge := p == 0 || p == j
				qEdge := q == 0 || q == i
				rEdge := r == 0 || r == k
				if pEdge && qEdge {
					continue
				}
				if rEdge && qEdge {
					continue
				}
				if pEdge && rEdge {
					continue
				}
				if !pEdge && !qEdge && !rEdge {
					continue
				}
				if rng.NextFloat() < 0.1 {
					placeCoralBlock(bctx, rng, mutable, coralBlockState)
				}
			}
		}
	}
	return true
}
