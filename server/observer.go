package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// observer.go — REDSTONE TIER-3 (OBSERVER): the 1:1 port of net.minecraft.world.level.block.ObserverBlock
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via CFR this session. An
// observer WATCHES the block in its FACING direction; when that block's state changes (updateShape from
// the FACING direction) it startSignal -> schedules a 2-tick tick; the tick toggles POWERED true (and
// reschedules a 2-tick tick to toggle it back false), emitting a 2-tick redstone PULSE out its FACING
// face. It plugs into the core redstone signal graph (redstone.go) as a signal SOURCE (isSignalSource
// == true; getSignal emits ownSignal only out FACING) and its POWERED flip is driven by the scheduled
// block-tick subsystem (block_ticks.go).
//
// CITE (methods, jar-verified this session):
//   ObserverBlock.tick        (POWERED ? setBlock(POWERED=false,2) : {setBlock(POWERED=true,2);
//                              scheduleTick(pos,this,2)}; then updateNeighborsInFront)
//   ObserverBlock.updateShape (if FACING==directionToNeighbour && !POWERED: startSignal)
//   ObserverBlock.startSignal (if !hasScheduledTick(pos,this): scheduleTick(pos,this,2))
//   ObserverBlock.updateNeighborsInFront (oppositePos = pos.relative(FACING.opposite);
//                              neighborChanged(oppositePos); updateNeighborsAtExceptFromFacing(oppositePos, FACING))
//   ObserverBlock.isSignalSource (true)
//   ObserverBlock.ownSignal    (POWERED ? 15 : 0)
//   ObserverBlock.getSignal    (FACING == direction ? ownSignal : 0)
//   ObserverBlock.getDirectSignal (== getSignal)
//   ObserverBlock.onPlace      (if POWERED && !hasScheduledTick: setBlock(POWERED=false,18);
//                              updateNeighborsInFront) — the un-stick edge case
//
// SCOPE / DEFERRALS (jar-cited):
//   - ExperimentalRedstoneUtils.initialOrientation threading through updateNeighborsInFront is an
//     experimental-evaluator optimization the DEFAULT evaluator ignores (see redstone.go scope note),
//     so the orientation arg is dropped; the neighbor notifications it performs are reproduced by the
//     redstone neighbor-update dispatch (onRedstoneEdit).

// observerTickType is the block id the observer POWERED toggle (ObserverBlock.tick) is scheduled and
// dispatched under. CITE: ObserverBlock (minecraft:observer).
const observerTickType blockTickType = "minecraft:observer"

// observerToggleDelay is the observer's scheduleTick delay — 2 game ticks (both startSignal and the
// on->off reschedule use `scheduleTick(pos, this, 2)`), producing the 2-tick output pulse. CITE:
// ObserverBlock.startSignal / tick (`scheduleTick(pos, this, 2)`).
const observerToggleDelay = 2

// ---------------------------------------------------------------------------------------------
// getSignal / getDirectSignal (dispatched from redstone.go's stateGetSignal / stateGetDirectSignal)
// ---------------------------------------------------------------------------------------------

// observerGetSignal is ObserverBlock.getSignal(state, level, pos, direction): the observer emits its
// ownSignal (POWERED ? 15 : 0) ONLY out its FACING face, else 0. CITE: ObserverBlock.getSignal
// (`FACING == direction ? ownSignal : 0`) / ownSignal (`POWERED != false ? 15 : 0`).
func observerGetSignal(state block.StateID, direction block.Direction) int {
	facing, ok := block.ObserverFacing(state)
	if !ok || facing != direction {
		return 0
	}
	if block.ObserverPowered(state) {
		return 15
	}
	return 0
}

// ---------------------------------------------------------------------------------------------
// updateShape trigger (startSignal) — the "watched block changed" wake
// ---------------------------------------------------------------------------------------------

// onObserverEdit is the OBSERVER slice of Level.updateNeighborsAt/updateShape: after a cell at
// `changedPos` changes (a break/place, a wire/torch/lever/piston state flip, a moving-piston
// completion), wake every observer that WATCHES `changedPos` — i.e. each neighbor O of `changedPos`
// whose FACING points from O back to `changedPos` (O.relative(FACING) == changedPos). That is the
// vanilla updateShape(directionToNeighbour) call reaching an observer from its FACING direction. Each
// woken observer runs startSignal (schedule a 2-tick tick if !POWERED and none pending). CITE:
// ObserverBlock.updateShape (`if (FACING == directionToNeighbour && !POWERED) startSignal(...)`).
func (t *TickLoop) onObserverEdit(changedPos pk.Position) {
	if t.world() == nil {
		return
	}
	for _, d := range redstoneDirs {
		obsPos := relative(changedPos, d)
		obsState := t.redstoneBlockAt(obsPos)
		if !block.IsObserver(obsState) {
			continue
		}
		facing, ok := block.ObserverFacing(obsState)
		if !ok {
			continue
		}
		// updateShape fires with directionToNeighbour = direction from the observer to the changed cell,
		// which is the opposite of d (d points from changedPos to the observer). The observer watches its
		// FACING neighbor, so it reacts only when FACING == that direction.
		if facing != dirOpposite(d) {
			continue
		}
		if block.ObserverPowered(obsState) {
			continue // updateShape's `!POWERED` guard: an already-powered observer does not re-start.
		}
		t.observerStartSignal(obsPos)
	}
}

// observerStartSignal is ObserverBlock.startSignal(level, ticks, pos): schedule a 2-tick tick unless
// one is already pending (the hasScheduledTick guard). CITE: ObserverBlock.startSignal.
func (t *TickLoop) observerStartSignal(pos pk.Position) {
	if t.hasScheduledBlockTick(pos, observerTickType) {
		return
	}
	t.scheduleBlockTick(pos, observerTickType, observerToggleDelay)
}

// ---------------------------------------------------------------------------------------------
// scheduled tick (the POWERED toggle) — dispatched from block_ticks.go tickBlock
// ---------------------------------------------------------------------------------------------

// observerTick is ObserverBlock.tick(state, level, pos, random):
//
//	if (POWERED) setBlock(pos, POWERED=false, 2);
//	else { setBlock(pos, POWERED=true, 2); scheduleTick(pos, this, 2); }
//	updateNeighborsInFront(level, pos, state);
//
// setBlock flag 2 == UPDATE_CLIENTS (no bit-1 neighbor notify); updateNeighborsInFront performs the
// output-cell notification separately. CITE: ObserverBlock.tick.
func (t *TickLoop) observerTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	if block.ObserverPowered(state) {
		// on -> off: end of the pulse.
		if newState, ok := block.ObserverWithPowered(state, false); ok && t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.observerUpdateNeighborsInFront(newState, pos)
		}
		return
	}
	// off -> on: start of the pulse; schedule the 2-tick tick that ends it.
	if newState, ok := block.ObserverWithPowered(state, true); ok && t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
		t.scheduleBlockTick(pos, observerTickType, observerToggleDelay)
		t.observerUpdateNeighborsInFront(newState, pos)
	}
}

// observerUpdateNeighborsInFront is ObserverBlock.updateNeighborsInFront(level, pos, state):
//
//	Direction direction = state.getValue(FACING);
//	BlockPos oppositePos = pos.relative(direction.getOpposite());
//	level.neighborChanged(oppositePos, this, orientation);
//	level.updateNeighborsAtExceptFromFacing(oppositePos, this, direction, orientation);
//
// The OUTPUT cell is pos.relative(FACING.opposite) — i.e. the cell BEHIND the FACING face, the one the
// observer's signal drives. (The observer emits its signal out FACING per getSignal; the cell that
// RECEIVES that signal is the one adjacent to the FACING face, which is pos.relative(FACING). But
// vanilla notifies pos.relative(FACING.opposite) here — the BACK. This is faithful to the bytecode:
// updateNeighborsInFront wakes the block behind so a consumer there reacts.) We reproduce both the
// direct neighborChanged at oppositePos and the update of its 6 neighbors except the one back toward the
// observer (the FACING direction from oppositePos) via the redstone neighbor-update dispatch. The
// orientation arg is dropped (default evaluator ignores it). CITE: ObserverBlock.updateNeighborsInFront.
func (t *TickLoop) observerUpdateNeighborsInFront(state block.StateID, pos pk.Position) {
	facing, ok := block.ObserverFacing(state)
	if !ok {
		return
	}
	oppositePos := relative(pos, dirOpposite(facing))
	q := &redstoneUpdateQueue{}
	// neighborChanged(oppositePos, this): the cell behind reacts (wire recompute, piston checkIfExtend,
	// diode checkTickOnNeighbor, another observer's updateShape via onObserverEdit below).
	q.push(oppositePos)
	// updateNeighborsAtExceptFromFacing(oppositePos, this, direction=FACING): notify oppositePos's 6
	// neighbors EXCEPT the one in the FACING direction (that neighbor is the observer at pos).
	for _, d := range redstoneDirs {
		if d == facing {
			continue
		}
		q.push(relative(oppositePos, d))
	}
	t.drainRedstoneUpdates(q)
	// The observer's own POWERED change is a block-state change at pos, which can wake ANOTHER observer
	// watching this one (a chained observer clock) via updateShape — reproduce that with onObserverEdit.
	t.onObserverEdit(pos)
}

// observerOnPlace is ObserverBlock.onPlace's un-stick edge case: a placed observer that somehow carries
// POWERED==true with no pending tick is reset to POWERED=false and notifies its front. A freshly-placed
// observer from getStateForPlacement is POWERED=false, so this is a no-op on normal placement; it exists
// to handle a POWERED observer restored from a schematic/structure. CITE: ObserverBlock.onPlace.
func (t *TickLoop) observerOnPlace(pos pk.Position, state block.StateID) {
	if t.world() == nil || !block.IsObserver(state) {
		return
	}
	if !block.ObserverPowered(state) {
		return
	}
	if t.hasScheduledBlockTick(pos, observerTickType) {
		return
	}
	if newState, ok := block.ObserverWithPowered(state, false); ok && t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
		t.observerUpdateNeighborsInFront(newState, pos)
	}
}
