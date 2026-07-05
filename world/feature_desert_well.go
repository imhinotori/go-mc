package world

// feature_desert_well.go ports DesertWellFeature — the sandstone well + water pattern
// that generates in deserts (net.minecraft.world.level.levelgen.feature.DesertWellFeature,
// javap -c against temp/cache/26.2-inner.jar). The config is NoneFeatureConfiguration
// (empty {} — JAR-VERIFIED desert_well.json), so the whole structure is HARDCODED here.
//
// The placement geometry takes ZERO rng draws — it is a fixed block pattern. The ONLY
// draws are the TWO suspicious-sand position picks at the very end (Util.getRandom over a
// 5-element list = nextInt(5) each). Preserving those two draws in order is the
// determinism contract even though the placeSusSand loot-table stamp is a block-entity
// deferral (it consumes NO rng — the loot table is stamped, not rolled).
//
// All writes go through bctx.placeState -> Neighborhood.SetBlock (cross-chunk clip). Reads
// (isEmptyBlock / IS_SAND) use ctx.GetBlock (placementContext -> the live Neighborhood).
//
// Source mapping (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.DesertWellFeature.place / placeSusSand
//   - net.minecraft.util.Util.getRandom(List, RandomSource) = list.get(rng.nextInt(size))

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("desert_well", desertWellBody) }

// desertWellStates holds the block states DesertWellFeature places, resolved once from their
// default block states (Blocks.SAND/SANDSTONE_SLAB/SANDSTONE/WATER/SUSPICIOUS_SAND.defaultBlockState()).
var (
	desertWellSand      = block.ToStateID[block.FromID["minecraft:sand"]]
	desertWellSandSlab  = block.ToStateID[block.FromID["minecraft:sandstone_slab"]]
	desertWellSandstone = block.ToStateID[block.FromID["minecraft:sandstone"]]
	desertWellWater     = block.ToStateID[block.FromID["minecraft:water"]]
	desertWellSusSand   = block.ToStateID[block.FromID["minecraft:suspicious_sand"]]
)

// desertWellSandID is the block id IS_SAND (BlockStatePredicate.forBlock(Blocks.SAND))
// matches — every state of minecraft:sand (sand has no state variants, but the check is
// block-id, not state-id, faithful to BlockState.is(Block)).
const desertWellSandID = "minecraft:sand"

// desertWellBody ports DesertWellFeature.place (javap -c). Returns false on the guard
// rejections (no solid sand surface / a hole under the floor), true once the well is
// built.
func desertWellBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	minY := ctx.MinY()

	// pos = origin.above(); then while isEmptyBlock(pos) && pos.Y > minY+2: pos = pos.below().
	pos := placement.BlockPos{X: origin.X, Y: origin.Y + 1, Z: origin.Z}
	for desertWellIsEmpty(bctx, ctx, pos) && pos.Y > minY+2 {
		pos.Y--
	}

	// if !IS_SAND.test(getBlockState(pos)): return false.
	if desertWellBlockID(bctx, pos) != desertWellSandID {
		return false
	}

	// Guard: for i4 in [-2,2], i5 in [-2,2]: if isEmptyBlock(pos.offset(i4,-1,i5)) &&
	// isEmptyBlock(pos.offset(i4,-2,i5)): return false. (Reject a 2-deep hole under the
	// floor anywhere in the 5x5 footprint.)
	for i4 := -2; i4 <= 2; i4++ {
		for i5 := -2; i5 <= 2; i5++ {
			if desertWellIsEmpty(bctx, ctx, offsetPos(pos, i4, -1, i5)) &&
				desertWellIsEmpty(bctx, ctx, offsetPos(pos, i4, -2, i5)) {
				return false
			}
		}
	}

	// Sandstone bowl: for i4 in [-2,0], i5 in [-2,2], i6 in [-2,2]:
	//   setBlock(pos.offset(i5, i4, i6), sandstone).  (NOTE the offset order: x=i5,y=i4,z=i6.)
	for i4 := -2; i4 <= 0; i4++ {
		for i5 := -2; i5 <= 2; i5++ {
			for i6 := -2; i6 <= 2; i6++ {
				bctx.placeState(offsetPos(pos, i5, i4, i6), desertWellSandstone)
			}
		}
	}

	// Water at pos, then water at each horizontal neighbor.
	bctx.placeState(pos, desertWellWater)
	for _, d := range desertWellHorizontals {
		bctx.placeState(offsetPos(pos, d.dx, 0, d.dz), desertWellWater)
	}

	// below = pos.below(); sand at below, then sand at each horizontal neighbor of below.
	below := offsetPos(pos, 0, -1, 0)
	bctx.placeState(below, desertWellSand)
	for _, d := range desertWellHorizontals {
		bctx.placeState(offsetPos(below, d.dx, 0, d.dz), desertWellSand)
	}

	// Ring at y+1: for i5 in [-2,2], i6 in [-2,2]: if edge (i5 in {-2,2} || i6 in {-2,2}):
	//   setBlock(pos.offset(i5, 1, i6), sandstone).
	for i5 := -2; i5 <= 2; i5++ {
		for i6 := -2; i6 <= 2; i6++ {
			if i5 == -2 || i5 == 2 || i6 == -2 || i6 == 2 {
				bctx.placeState(offsetPos(pos, i5, 1, i6), desertWellSandstone)
			}
		}
	}

	// Four sandstone slabs at the ring's cardinal midpoints (y+1).
	bctx.placeState(offsetPos(pos, 2, 1, 0), desertWellSandSlab)
	bctx.placeState(offsetPos(pos, -2, 1, 0), desertWellSandSlab)
	bctx.placeState(offsetPos(pos, 0, 1, 2), desertWellSandSlab)
	bctx.placeState(offsetPos(pos, 0, 1, -2), desertWellSandSlab)

	// Top slab/sandstone cap at y+4: for i5 in [-1,1], i6 in [-1,1]:
	//   center (i5==0 && i6==0) -> sandstone; else -> sandstone slab.
	for i5 := -1; i5 <= 1; i5++ {
		for i6 := -1; i6 <= 1; i6++ {
			if i5 == 0 && i6 == 0 {
				bctx.placeState(offsetPos(pos, i5, 4, i6), desertWellSandstone)
			} else {
				bctx.placeState(offsetPos(pos, i5, 4, i6), desertWellSandSlab)
			}
		}
	}

	// Four sandstone corner pillars for i5 in [1,3]:
	//   offset(-1,i5,-1), offset(-1,i5,1), offset(1,i5,-1), offset(1,i5,1) -> sandstone.
	for i5 := 1; i5 <= 3; i5++ {
		bctx.placeState(offsetPos(pos, -1, i5, -1), desertWellSandstone)
		bctx.placeState(offsetPos(pos, -1, i5, 1), desertWellSandstone)
		bctx.placeState(offsetPos(pos, 1, i5, -1), desertWellSandstone)
		bctx.placeState(offsetPos(pos, 1, i5, 1), desertWellSandstone)
	}

	// Two suspicious-sand blocks under the well floor (the archaeology loot). The list is
	// [pos, pos.east(), pos.south(), pos.west(), pos.north()] (this exact order); each pick
	// is Util.getRandom(list, rng) = list[nextInt(5)] (1 draw). placeSusSand stamps the
	// DESERT_WELL_ARCHAEOLOGY loot table on the brushable block-entity (a DEFERRED block-
	// entity subsystem — it consumes NO rng, so its omission does not perturb determinism).
	susList := [5]placement.BlockPos{
		pos,                      // list[0]
		offsetPos(pos, 1, 0, 0),  // east
		offsetPos(pos, 0, 0, 1),  // south
		offsetPos(pos, -1, 0, 0), // west
		offsetPos(pos, 0, 0, -1), // north
	}
	// getRandom draw #1 -> that pos.below(1); getRandom draw #2 -> that pos.below(2).
	pick1 := susList[int(rng.NextIntN(5))]
	bctx.placeState(offsetPos(pick1, 0, -1, 0), desertWellSusSand)
	pick2 := susList[int(rng.NextIntN(5))]
	bctx.placeState(offsetPos(pick2, 0, -2, 0), desertWellSusSand)

	return true
}

// desertWellHorizontals is Direction.Plane.HORIZONTAL in the vanilla iteration order
// [NORTH, EAST, SOUTH, WEST] — the well's water/sand neighbor rings walk it. The order is
// immaterial to the placed set (each neighbor placed unconditionally) but kept faithful.
var desertWellHorizontals = [4]struct{ dx, dz int }{
	{dx: 0, dz: -1}, // NORTH
	{dx: 1, dz: 0},  // EAST
	{dx: 0, dz: 1},  // SOUTH
	{dx: -1, dz: 0}, // WEST
}

// offsetPos returns p offset by (dx,dy,dz) (BlockPos.offset).
func offsetPos(p placement.BlockPos, dx, dy, dz int) placement.BlockPos {
	return placement.BlockPos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
}

// desertWellIsEmpty ports WorldGenLevel.isEmptyBlock(pos): the block at pos is air. The
// live read goes through ctx (placementContext.GetBlock -> Neighborhood).
func desertWellIsEmpty(bctx *bodyContext, _ placement.PlacementContext, p placement.BlockPos) bool {
	return block.IsAir(bctx.getState(p))
}

// desertWellBlockID returns the block id at p (for the IS_SAND block-match check).
func desertWellBlockID(bctx *bodyContext, p placement.BlockPos) string {
	st := bctx.getState(p)
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return ""
	}
	b := block.StateList[st]
	if b == nil {
		return ""
	}
	return b.ID()
}
