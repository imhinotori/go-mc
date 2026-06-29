package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_survival.go — BLOCK-SURVIVAL (vegetation): the 1:1 port of the generic
// Level.updateNeighborsAt -> BlockState.updateShape -> VegetationBlock.updateShape ->
// Block.updateOrDestroy chain, SCOPED to the support-needing vegetation family (flowers,
// saplings, short_grass/fern, bushes, and the 2-tall double plants). This closes the Phase-17
// deferred gate finding: "rompe un bloque con flores arriba, las flores no se rompen" — vanilla
// destroys an unsupported plant the instant its ground support is removed.
//
// Cited bytecode (temp/cache/26.2-inner.jar, decompiled `javap -c -p` this session):
//
//	net.minecraft.server.level.ServerLevel.updateNeighborsAt(pos, block, orientation):
//	    -> CollectingNeighborUpdater.updateNeighborsAtExceptFromFacing -> for each of the 6
//	       neighbors, BlockState.updateShape(direction-from-neighbor-to-changed-cell, ...).
//	net.minecraft.world.level.block.VegetationBlock.updateShape(state, ..., dir, neighborPos, neighborState, ...):
//	    if (!state.canSurvive(level, pos)) return Blocks.AIR.defaultBlockState();   // else super.updateShape
//	net.minecraft.world.level.block.VegetationBlock.canSurvive(state, level, pos):
//	    return mayPlaceOn(level.getBlockState(pos.below()), level, pos.below());
//	net.minecraft.world.level.block.VegetationBlock.mayPlaceOn(belowState, ...):
//	    return belowState.is(BlockTags.SUPPORTS_VEGETATION);                         // == block.IsVegetationGround
//	net.minecraft.world.level.block.DoublePlantBlock.canSurvive(state, level, pos):
//	    HALF==UPPER ? (belowState.is(this) && belowState.HALF==LOWER) : super.canSurvive(...);
//	net.minecraft.world.level.block.Block.updateOrDestroy(old, newState, level, pos, flags, recursionLeft):
//	    if (newState != old && newState.isAir() && !level.isClientSide())
//	        level.destroyBlock(pos, /*dropBlock=*/(flags & 32) == 0, null, recursionLeft);
//	net.minecraft.world.level.block.Block.updateOrDestroy(old, newState, level, pos, flags):
//	    recursionLeft = 512.
//
// Only the DOWN-neighbor change matters for this family: a vegetation block's canSurvive depends
// solely on the cell BELOW it (single plants + double-plant lower half) or the cell below being the
// matching lower half (double-plant upper half). So when a cell at `pos` changes (a break/place),
// the ONLY potentially-unsupported vegetation is the block directly ABOVE (`pos.above`). Destroying
// it is itself a cell change, so its own `above` is re-checked — that recursion (bounded at the
// vanilla 512) is what cascades a 2-tall plant (lower half destroyed -> upper half loses its lower
// half -> destroyed) or a stacked column.
//
// All access is on the TICK goroutine over the tick-owned ChunkManager (TICK-05): world reads/writes
// go through t.world().GetBlock/SetBlock, and the drop reuses the GAMEPLAY-06 spawnBlockDrop path.

// vegetationRecursionLimit is Block.updateOrDestroy's recursionLeft seed (512): the cap on how many
// nested updateOrDestroy -> destroyBlock -> updateNeighborsAt re-entries a single edit may trigger,
// so a tall/stacked column cascades fully but a pathological loop cannot run forever. CITE:
// Block.updateOrDestroy(old, newState, level, pos, flags) passing `sipush 512` as recursionLeft.
const vegetationRecursionLimit = 512

// vegetationCanSurvive is the support predicate for the in-scope vegetation family at `pos`, given
// the world state. It ports VegetationBlock.canSurvive (single plants + double-plant lower half ->
// IsVegetationGround(below)) and DoublePlantBlock.canSurvive's UPPER branch (below must be the SAME
// double plant in its lower half). `state` is the vegetation state at `pos`; `below` is the state
// directly underneath. A vegetation block whose support is unreadable (unloaded column) is treated
// as NOT surviving only when we positively know the below state is invalid — see the caller, which
// only acts on a successfully-read below state, so an unloaded neighbor never spuriously destroys.
// CITE: VegetationBlock.canSurvive / DoublePlantBlock.canSurvive.
func vegetationCanSurvive(state, below block.StateID) bool {
	switch {
	case block.IsVegetation(state):
		// Single-cell vegetation: survives iff below is SUPPORTS_VEGETATION ground.
		return block.IsVegetationGround(below)
	case block.IsDoublePlant(state):
		if block.DoublePlantLowerHalf(state) {
			// LOWER half: super.canSurvive == IsVegetationGround(below).
			return block.IsVegetationGround(below)
		}
		// UPPER half: survives iff below is the SAME double plant in its LOWER half.
		return block.SameDoublePlant(state, below) && block.DoublePlantLowerHalf(below)
	default:
		// Not an in-scope vegetation block: nothing to check (it is not our concern this pass).
		return true
	}
}

// updateVegetationOnEdit is the vegetation slice of Level.updateNeighborsAt run after a cell at
// `pos` changed (a break or place — reconcileEdit calls this alongside the fluid neighbor
// notification). It checks the block directly ABOVE `pos`: if that block is an in-scope vegetation
// that can no longer survive on the (now-changed) cell at `pos`, it is destroyed to air, drops its
// loot via the GAMEPLAY-06 path, and broadcasts the air to trackers — then the destroy recurses to
// re-check the cell above the destroyed plant (so 2-tall plants and stacked columns cascade). The
// recursion is bounded at the vanilla 512. Tick-owned. CITE: ServerLevel.updateNeighborsAt ->
// VegetationBlock.updateShape -> Block.updateOrDestroy.
func (t *TickLoop) updateVegetationOnEdit(pos pk.Position) {
	t.destroyUnsupportedVegetationAbove(pos, vegetationRecursionLimit)
}

// destroyUnsupportedVegetationAbove implements the recursive updateShape/updateOrDestroy step for
// the cell ABOVE `pos`. recursionLeft is the remaining nested-destroy budget (Block.updateOrDestroy's
// recursionLeft); at 0 the cascade stops (vanilla's recursion guard). Tick-owned.
func (t *TickLoop) destroyUnsupportedVegetationAbove(pos pk.Position, recursionLeft int) {
	if recursionLeft <= 0 || t.world() == nil {
		return
	}

	abovePos := above(pos)

	// Read the block above and the new state at `pos` (its support). An unloaded/unreadable column
	// reads nothing -> no-op (vanilla's getBlockState on an unloaded chunk returns air, which is not
	// vegetation, so it would not destroy anything either — treating an unreadable read as "skip" is
	// the faithful, non-destructive outcome).
	aboveState, ok := t.world().GetBlock(abovePos, dimMinY)
	if !ok {
		return
	}
	// Only the in-scope vegetation family is subject to this check; everything else is untouched
	// (torches/rails/redstone/doors are a separate survival class — a documented follow-up).
	if !block.IsVegetation(aboveState) && !block.IsDoublePlant(aboveState) {
		return
	}

	belowState, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return // support cell unreadable: do not destroy (non-destructive on an unloaded read)
	}

	if vegetationCanSurvive(aboveState, belowState) {
		return // still supported: VegetationBlock.updateShape returns the state unchanged
	}

	// updateShape returned AIR && !isClientSide -> Block.updateOrDestroy -> level.destroyBlock(
	// abovePos, dropBlock=true, null, recursionLeft). The default setBlock flags carry no bit-32, so
	// dropBlock is true: the destroyed vegetation drops its own loot. We mirror Sulfur's destroyBlock
	// seam: capture the broken state, SetBlock air, broadcast the air to trackers, and drop.
	if !t.world().SetBlock(abovePos, t.airState(), dimMinY) {
		return // already air / unloaded: nothing destroyed (destroyBlock returns false)
	}
	t.broadcastBlockUpdate(abovePos, t.airState())
	// spawnBlockDrop with a nil player: vanilla destroyBlock(pos, dropBlock=true, entity=null, ...)
	// drops regardless of who triggered the support removal (a CREATIVE player breaking the ground
	// still makes the flower above drop — the entity arg is null, not the breaker). spawnBlockDrop's
	// creative gate is `p != nil && p.gameMode == creative`, so a nil player always drops.
	t.spawnBlockDrop(nil, abovePos, aboveState)

	// destroyBlock -> setBlock(air) -> updateNeighborsAt(abovePos): recurse to re-check the cell
	// above the just-destroyed plant. This cascades a 2-tall double plant (lower half destroyed here
	// -> its upper half above loses support) and any stacked supported column. recursionLeft - 1
	// mirrors Block.updateOrDestroy threading recursionLeft down.
	t.destroyUnsupportedVegetationAbove(abovePos, recursionLeft-1)
}

// airState returns the air state id (cached lookup mirror of block.ToStateID[block.Air{}], used by
// the survival destroy path so it does not re-resolve the map on every cascade step).
func (t *TickLoop) airState() block.StateID {
	return block.ToStateID[block.Air{}]
}
