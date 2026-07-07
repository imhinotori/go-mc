package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// leaf_decay.go - the UPDATE-SHAPE + SCHEDULED-TICK half of LeavesBlock DISTANCE maintenance (D-B3),
// counterpart to the random-tick DECAY (growth_block.go leavesRandomTick) and the DISTANCE recompute
// helper (growth_block.go leavesUpdateDistance). Together the 1:1 port of
// net.minecraft.world.level.block.LeavesBlock.
//
// VANILLA CALL CHAIN (verified against temp/cache/26.2-inner.jar via javap -c -p this session):
//   LeavesBlock.updateShape(state, level, tickAccess, pos, dir, neighborPos, neighborState, random):
//       int i = getDistanceAt(neighborState) + 1;
//       if (i != 1 || state.getValue(DISTANCE) != i) tickAccess.scheduleTick(pos, this, TICK_DELAY);
//       return state;   // updateShape returns the state UNCHANGED
//   LeavesBlock.tick(state, serverLevel, pos, random):
//       serverLevel.setBlock(pos, updateDistance(state, level, pos), 3);  // flag 3 UPDATE_NEIGHBORS|UPDATE_CLIENTS
//   LeavesBlock.updateDistance(state, level, pos):  (ported: growth_block.go leavesUpdateDistance)
//       int i = 7; for each Direction d: i = min(i, getDistanceAt(getBlockState(pos+d)) + 1); if (i==1) break;
//       return state.setValue(DISTANCE, i);
//
// updateShape NEVER mutates the leaf; it only SCHEDULES a 1-tick tick, and the tick then recomputes
// DISTANCE and writes it. This two-step is what makes a chopped log propagate: breaking a log runs
// updateShape on each neighbour leaf (getDistanceAt(air)==7 -> i=8 -> schedule), the tick recomputes
// the leaf DISTANCE from its live neighbours (now higher, the log anchor gone), and over successive
// ticks the rising-DISTANCE wavefront reaches 7, at which point the leaf is randomly-ticking
// (isRandomlyTicking == DISTANCE==7 && !PERSISTENT) and decays. CITE: LeavesBlock.
// {updateShape,tick,updateDistance,getDistanceAt}; Block.UPDATE_ALL flag 3.
//
// PIG-ORACLE SAFETY: none of this touches an entity or levelRandom. updateShape/tick draw NO
// levelRandom (updateDistance is a deterministic neighbour scan; the scheduled-tick order draws the
// level sub-tick counter, a separate deterministic stream). The pig oracle drives serverAiStep
// directly and never edits a block, so onLeavesEdit never runs on the oracle path; the byte-identical
// gate is unaffected.

// leavesTickDelay is LeavesBlock.TICK_DELAY (1): the delay the updateShape schedule uses before the
// DISTANCE-recompute tick fires. Compile-time inlined in the jar. CITE: LeavesBlock.TICK_DELAY.
const leavesTickDelay = 1

// leavesTickTypes is the set of block ids leaves DISTANCE-recompute ticks are scheduled/dispatched
// under - one per LeavesBlock variant. A leaf schedules under its OWN block id (updateShape ->
// scheduleTick(pos, this, TICK_DELAY)), so tickBlock routes any of them to leavesTick. All eleven share
// the same tick handler. CITE: LeavesBlock.updateShape; the eleven LeavesBlock subclass block ids.
var leavesTickTypes = map[blockTickType]struct{}{
	"minecraft:oak_leaves":              {},
	"minecraft:spruce_leaves":           {},
	"minecraft:birch_leaves":            {},
	"minecraft:jungle_leaves":           {},
	"minecraft:acacia_leaves":           {},
	"minecraft:cherry_leaves":           {},
	"minecraft:dark_oak_leaves":         {},
	"minecraft:pale_oak_leaves":         {},
	"minecraft:mangrove_leaves":         {},
	"minecraft:azalea_leaves":           {},
	"minecraft:flowering_azalea_leaves": {},
}

// isLeavesTickType reports whether a scheduled tick type is one of the leaves block ids.
func isLeavesTickType(typ blockTickType) bool {
	_, ok := leavesTickTypes[typ]
	return ok
}

// onLeavesEdit is the LEAVES slice of Level.updateNeighborsAt/updateNeighbourShapes: after a cell at
// pos changed (a break or place; for a break newState is air), each of the six neighbours that is a
// LeavesBlock runs LeavesBlock.updateShape given the changed cell as its neighborState. updateShape
// only (conditionally) SCHEDULES a 1-tick DISTANCE-recompute tick on that leaf; it never mutates the
// leaf directly. This wakes neighbour leaves when a log is chopped: the removed log flips
// getDistanceAt(neighborState) from 0 to 7, so i=8 differs from the leaf current DISTANCE and a tick
// is scheduled. Mirrors onFallingBlockEdit / onBlockTickEdit. Tick-owned. CITE: LeavesBlock.updateShape.
func (t *TickLoop) onLeavesEdit(pos pk.Position, newState block.StateID) {
	if t.world() == nil {
		return
	}
	// For each of the six orthogonal neighbours np, run its updateShape with the changed cell as
	// neighborState. updateShape scans only that ONE neighbour: i = getDistanceAt(newState) + 1.
	i := block.LeafDistanceAt(newState) + 1
	for _, d := range sixDirections {
		np := pk.Position{X: pos.X + d.dx, Y: pos.Y + d.dy, Z: pos.Z + d.dz}
		ns, ok := t.world().GetBlock(np, dimMinY)
		if !ok || !block.IsLeaves(ns) {
			continue // updateShape leaves override only applies to a LeavesBlock at np
		}
		// if (i != 1 || state.getValue(DISTANCE) != i) scheduleTick(pos, this, TICK_DELAY);
		if i != 1 || block.LeavesDistance(ns) != i {
			t.scheduleLeavesTick(np, ns)
		}
	}
}

// scheduleLeavesTick schedules the leaf DISTANCE-recompute tick TICK_DELAY (1) ticks out under the
// leaf own block id, dedup'd (updateShape may fire for several neighbours of one edit; a leaf touched
// twice must not pile up duplicate ticks, mirroring the hasScheduledTick guard the falling-block and
// redstone-torch seams use). CITE: LeavesBlock.updateShape scheduleTick; LevelTicks.hasScheduledTick.
func (t *TickLoop) scheduleLeavesTick(pos pk.Position, state block.StateID) {
	typ := blockTickType(block.StateList[state].ID())
	if !isLeavesTickType(typ) {
		return
	}
	if t.hasScheduledBlockTick(pos, typ) {
		return
	}
	t.scheduleBlockTick(pos, typ, leavesTickDelay)
}

// leavesTick is the port of LeavesBlock.tick(state, serverLevel, pos, random): recompute the leaf
// DISTANCE from its six live neighbours (leavesUpdateDistance == updateDistance) and write it back with
// setBlock flag 3 (UPDATE_NEIGHBORS|UPDATE_CLIENTS). A leaf recomputed UP to 7 (its nearest log gone)
// becomes decaying and is then sampled by the random-tick driver, which removes it. Dispatched from
// tickBlock after the ServerLevel.tickBlock state.is(block) stale guard. It draws NO levelRandom
// (updateDistance is deterministic). CITE: LeavesBlock.tick (setBlock(updateDistance(...), 3)).
func (t *TickLoop) leavesTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	updated, ok := t.leavesUpdateDistance(state, pos)
	if !ok {
		return
	}
	// setBlock(pos, updateDistance(...), 3): flag 3 == UPDATE_NEIGHBORS|UPDATE_CLIENTS. SetBlock writes
	// the recomputed state; broadcastBlockUpdate is the UPDATE_CLIENTS half. A no-op recompute (distance
	// unchanged) returns changed=false: nothing broadcast, no cascade. The UPDATE_NEIGHBORS half is the
	// neighbour cascade: re-run onLeavesEdit so a leaf whose DISTANCE just ROSE propagates the rise to its
	// neighbours (the vanilla wavefront that walks a chopped tree canopy out to distance 7).
	if updated != state && t.world().SetBlock(pos, updated, dimMinY) {
		t.broadcastBlockUpdate(pos, updated)
		t.onLeavesEdit(pos, updated)
	}
}
