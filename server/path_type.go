package server

import "github.com/imhinotori/sulfur/level/block"

// path_type.go -- AI-02 / C-2: the ported WalkNodeEvaluator BLOCK -> PathType classification and the
// neighbour-hazard upgrade. This is the "danger types" layer the divergence audit (C-2) flagged as
// AUSENTE: without it every non-solid block reads as OPEN and every solid block as WALKABLE, so a mob
// paths straight through lava/fire/cactus and gets stuck on fences/doors/rails.
//
// PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p this session):
//   net.minecraft.world.level.pathfinder.WalkNodeEvaluator.getPathTypeFromState -- the block -> PathType
//   map (VERIFIED bytecode branch order, classifyBlockPathType below); .checkNeighbourBlocks -- the
//   3x3x3 hazard upgrade; .getPathTypeStatic -- the feet-cell classification + the below-block
//   tableswitch; and net.minecraft.world.level.pathfinder.NodeEvaluator.isBurningBlock.
//
// Idiom: the jar reads a live BlockGetter; the pure off-tick A* cannot. So classifyBlockPathType runs
// ON the tick (snapshotRegion, where the StateID is in hand) and freezes the per-cell PathType into
// pathRegion.blockType; checkNeighbourBlocks + getPathTypeStatic then read that frozen copy off-tick.

// classifyBlockPathType ports WalkNodeEvaluator.getPathTypeFromState for a single block state id -- the
// block -> PathType map, in the jar exact branch order (VERIFIED bytecode this session):
//
//	air; TRAPDOORS tag / LILY_PAD / BIG_DRIPLEAF; POWDER_SNOW; CACTUS / SWEET_BERRY_BUSH; HONEY_BLOCK;
//	COCOA; WITHER_ROSE / SPELEOTHEMS; LAVA fluid; isBurningBlock; DoorBlock (OPEN -> DOOR_OPEN, else
//	canOpenByHand -> DOOR_WOOD_CLOSED else DOOR_IRON_CLOSED); BaseRailBlock; LeavesBlock; FENCES /
//	WALLS / closed FenceGate; !isPathfindable(LAND) -> BLOCKED; WATER fluid; else OPEN. Order matters.
//
// isPathfindable(LAND): the jar returns BLOCKED for a block not LAND-pathfindable. v1 reuses the
// established solidity primitive (block.IsSolid == a motion-blocking collider) as the isPathfindable
// proxy -- checked AFTER the fence/door/rail special cases (exactly the jar order), so the
// WALKABLE/OPEN/BLOCKED core stays byte-identical and only the hazard classes are newly distinguished.
func classifyBlockPathType(s block.StateID) pathType {
	if block.IsAir(s) {
		return pathOpen
	}
	if block.IsTrapdoor(s) || block.IsLilyPad(s) || block.IsBigDripleaf(s) {
		return pathTrapdoor
	}
	if block.IsPowderSnow(s) {
		return pathPowderSnow
	}
	if block.IsCactus(s) || block.IsSweetBerryBush(s) {
		return pathDamaging
	}
	if block.IsHoneyBlock(s) {
		return pathStickyHoney
	}
	if block.IsCocoa(s) {
		return pathCocoa
	}
	if block.IsWitherRose(s) || block.IsSpeleothem(s) {
		return pathDamageCautious
	}
	if _, isLava := lavaLevelOf(s); isLava {
		return pathLava
	}
	if isBurningBlockState(s) {
		return pathFire
	}
	if block.IsDoor(s) {
		if block.DoorOpen(s) {
			return pathDoorOpen
		}
		if block.DoorOpenableByHand(s) { // canOpenByHand: only iron is false
			return pathDoorWoodClosed
		}
		return pathDoorIronClosed
	}
	if block.IsRail(s) {
		return pathRail
	}
	if block.IsLeaves(s) {
		return pathLeaves
	}
	if block.IsFence(s) || block.IsWall(s) || (block.IsFenceGate(s) && !block.DoorOpen(s)) {
		return pathFence
	}
	if block.IsSolid(s) {
		return pathBlocked
	}
	if _, isWater := waterLevelOf(s); isWater {
		return pathWater
	}
	return pathOpen
}

// isBurningBlockState ports NodeEvaluator.isBurningBlock(BlockState): FIRE tag || LAVA block ||
// MAGMA_BLOCK || lit CampfireBlock || LAVA_CAULDRON. VERIFIED bytecode this session.
func isBurningBlockState(s block.StateID) bool {
	if block.IsFire(s) { // BlockTags.FIRE (fire / soul_fire)
		return true
	}
	if _, isLava := lavaLevelOf(s); isLava { // Blocks.LAVA
		return true
	}
	return block.IsMagmaBlock(s) || block.IsLitCampfire(s) || block.IsLavaCauldron(s)
}

// checkNeighbourBlocks ports WalkNodeEvaluator.checkNeighbourBlocks: scan the 3x3x3 neighbourhood
// (skipping the centre column dx==0 && dz==0), and upgrade the passed type to the matching DANGER type
// for the FIRST hazardous neighbour (DAMAGING -> DAMAGING_IN_NEIGHBOR; FIRE/LAVA -> FIRE_IN_NEIGHBOR;
// WATER -> WATER_BORDER; DAMAGE_CAUTIOUS -> DAMAGE_CAUTIOUS). VERIFIED bytecode this session. Reads only
// the frozen pathRegion.blockType snapshot; the neighbour type is the raw getPathTypeFromState result
// (blockTypeAt), not the folded standing type -- exactly the jar (it re-reads on each neighbour). The
// observable effect: a WALKABLE cell next to a hazard gets a positive-but-costly malus (8), so the mob
// prefers to route one cell away from the danger.
func checkNeighbourBlocks(r *pathRegion, x, y, z int, t pathType) pathType {
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				if dx == 0 && dz == 0 {
					continue // skip the centre column (the node own x,z)
				}
				switch r.blockTypeAt(x+dx, y+dy, z+dz) {
				case pathDamaging:
					return pathDamagingInNeighbor
				case pathFire, pathLava:
					return pathFireInNeighbor
				case pathWater:
					return pathWaterBorder
				case pathDamageCautious:
					return pathDamageCautious
				}
			}
		}
	}
	return t
}

// getPathTypeStatic ports WalkNodeEvaluator.getPathTypeStatic(ctx, pos) over the frozen snapshot: the
// FEET-cell classification, and -- when the feet cell is OPEN -- the tableswitch on the BLOCK BELOW that
// decides WALKABLE (via checkNeighbourBlocks) vs the ON_TOP_OF_* / stacked-hazard standing types.
// VERIFIED bytecode (WalkNodeEvaluator inner switch-map decoded this session):
//
//	below OPEN | WATER | LAVA | WALKABLE  -> OPEN   (no solid floor: a drop node)
//	below FIRE -> FIRE; DAMAGING -> DAMAGING; STICKY_HONEY -> STICKY_HONEY;
//	below POWDER_SNOW -> ON_TOP_OF_POWDER_SNOW; DAMAGE_CAUTIOUS -> DAMAGE_CAUTIOUS;
//	below TRAPDOOR -> ON_TOP_OF_TRAPDOOR; below default (a normal solid floor) ->
//	checkNeighbourBlocks(x, y, z, WALKABLE).
//
// The y < minY+1 guard mirrors the jar y < ctx.level().getMinY()+1 (never read below the world).
func getPathTypeStatic(r *pathRegion, x, y, z int) pathType {
	t := r.blockTypeAt(x, y, z)
	if t != pathOpen || y < r.minY+1 {
		return t
	}
	switch r.blockTypeAt(x, y-1, z) {
	case pathOpen, pathWater, pathLava, pathWalkable:
		return pathOpen
	case pathFire:
		return pathFire
	case pathDamaging:
		return pathDamaging
	case pathStickyHoney:
		return pathStickyHoney
	case pathPowderSnow:
		return pathOnTopOfPowderSnow
	case pathDamageCautious:
		return pathDamageCautious
	case pathTrapdoor:
		return pathOnTopOfTrapdoor
	default:
		return checkNeighbourBlocks(r, x, y, z, pathWalkable)
	}
}

// newUnclassified allocates the pathRegion.blockType backing slice filled with -1 (UNCLASSIFIED). A
// -1 cell means "no explicit classification" -- blockTypeAt then derives the type from the solid+fluid
// bits (the pre-C-2 solidity model), so a test region that only sets solid/fluid is byte-identical to
// before. snapshotRegion overwrites every in-range cell with the real classifyBlockPathType result.
func newUnclassified(n int) []int16 {
	b := make([]int16, n)
	for i := range b {
		b[i] = -1
	}
	return b
}

// setBlockType stores the ported getPathTypeFromState result for a cell (builder/test only -- the
// region is immutable thereafter). Out-of-box writes are dropped (same policy as set/setFluid).
func (r *pathRegion) setBlockType(x, y, z int, t pathType) {
	if !r.inBox(x, y, z) {
		return
	}
	r.blockType[r.idx(x, y, z)] = int16(t)
}

// blockTypeAt returns the ported getPathTypeFromState PathType of the cell -- the frozen classification
// snapshotRegion stored, or, when the cell was never explicitly classified (a solid/fluid-only test
// region, sentinel -1), the type DERIVED from the solid+fluid bits. The derivation mirrors the
// classifyBlockPathType core exactly (LAVA fluid -> LAVA; WATER fluid -> WATER; solid -> BLOCKED; else
// OPEN), so both paths agree and the pre-C-2 solidity behaviour is preserved byte-for-byte.
//
// OUT-OF-BOX derives BLOCKED (solidAt treats the edge as a solid barrier), so a node beyond the
// snapshot is never standable-into -- the documented PathNavigationRegion edge policy.
func (r *pathRegion) blockTypeAt(x, y, z int) pathType {
	if r.inBox(x, y, z) {
		if bt := r.blockType[r.idx(x, y, z)]; bt >= 0 {
			return pathType(bt)
		}
	}
	// Unclassified (or out-of-box): derive from the solid+fluid model.
	switch r.fluidAt(x, y, z) {
	case pathFluidLava:
		return pathLava
	case pathFluidWater:
		return pathWater
	}
	if r.solidAt(x, y, z) {
		return pathBlocked
	}
	return pathOpen
}
