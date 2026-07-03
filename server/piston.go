package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// piston.go — REDSTONE TIER-3 (PISTON): the 1:1 port of the vanilla piston blocks
// (PistonBaseBlock + PistonStructureResolver + MovingPistonBlock + PistonMovingBlockEntity) from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via CFR this session. A piston reads
// its power through the core redstone signal graph (redstone.go) — including the quasi-connectivity
// ("bud") rule vanilla 26.2 keeps — and, when its powered state crosses its EXTENDED state, posts a
// same-tick BLOCK EVENT (Level.blockEvent) that runBlockEvents fires as triggerEvent -> moveBlocks:
// extend places a piston_head + moving_piston(s) and shoves the push-list by 1; retract pulls the head
// (and, for sticky, the front block) back by 1. The transient moving_piston animation is carried by a
// PistonMovingBlockEntity that ticks progress 0 -> 1 over TICKS_TO_EXTEND(2) ticks and completes the
// move.
//
// CITE (methods, jar-verified this session):
//   PistonBaseBlock.checkIfExtend / getNeighborSignal (QC bud rule: neighbors of pos except pushDir,
//       DOWN of pos, and all neighbors of pos.above() except DOWN) / neighborChanged / onPlace / setPlacedBy
//   PistonBaseBlock.triggerEvent (b0=0 extend, b0=1|2 retract) / moveBlocks / isPushable
//   PistonBaseBlock TRIGGER_EXTEND=0 TRIGGER_CONTRACT=1 TRIGGER_DROP=2
//   PistonStructureResolver.resolve / addBlockLine / addBranchingBlocks / MAX_PUSH_DEPTH=12
//   MovingPistonBlock.newMovingBlockEntity
//   PistonMovingBlockEntity TICKS_TO_EXTEND=2 / tick (progress += 0.5) / finalTick
//
// SCOPE / DEFERRALS (each jar-cited):
//   - The moving-piston ANIMATION interpolation (the client-side smooth slide, getProgress lerp,
//     moveCollidedEntities / moveStuckEntities entity shove, slime bounce) is cited-SIMPLIFIED to a
//     faithful 2-tick block-state completion: the movingPistonBE ticks progress 0->0.5->1.0 exactly as
//     PistonMovingBlockEntity.tick, and on completion writes the same END STATE (block moved by 1, head
//     placed/removed). The ENTITY-shove seam (moveCollidedEntities) is DEFERRED — pushing/dragging
//     mobs/players is a heavy collision seam; the BLOCK move (the target) is 1:1. CITE:
//     PistonMovingBlockEntity.getProgress / moveCollidedEntities / moveStuckEntities.
//   - Slime/honey adjacent-drag in the push RESOLVER is ported (addBranchingBlocks + isSticky). The
//     slime bounce / honey stick ENTITY effects are separate and DEFERRED. CITE: PistonStructureResolver.
//   - The client blockEntity render packet for the moving_piston (getUpdateTag) is DEFERRED (cosmetic);
//     the moving_piston BLOCK state is broadcast so the client shows the transient block. CITE:
//     PistonMovingBlockEntity.getUpdateTag.
//   - levelEvent(2001) break-particles, playSound (PISTON_EXTEND/CONTRACT), gameEvent are client
//     cosmetics, DEFERRED. CITE: PistonBaseBlock.moveBlocks/triggerEvent.

// ---------------------------------------------------------------------------------------------
// constants (PistonBaseBlock / PistonStructureResolver / PistonMovingBlockEntity)
// ---------------------------------------------------------------------------------------------

const (
	// pistonTriggerExtend / Contract / Drop are PistonBaseBlock.TRIGGER_EXTEND=0, TRIGGER_CONTRACT=1,
	// TRIGGER_DROP=2 — the b0 of the block event. CITE: PistonBaseBlock.TRIGGER_*.
	pistonTriggerExtend   = 0
	pistonTriggerContract = 1
	pistonTriggerDrop     = 2

	// pistonMaxPushDepth is PistonStructureResolver.MAX_PUSH_DEPTH — the 12-block push limit. CITE:
	// PistonStructureResolver.MAX_PUSH_DEPTH.
	pistonMaxPushDepth = 12

	// pistonTicksToExtend is PistonMovingBlockEntity.TICKS_TO_EXTEND (2) — the moving BE completes when
	// progress reaches 1.0 after two `progress += 0.5` ticks. CITE: PistonMovingBlockEntity.TICKS_TO_EXTEND.
	pistonTicksToExtend = 2
)

// pistonTickTypes is the set of block ids the piston checkIfExtend is scheduled/dispatched under (the
// piston has no scheduled tick of its own — its reaction is neighborChanged/onPlace — but the moving
// BE animation is driven by tickMovingPistons, not the scheduled-tick subsystem). This set is used only
// by tickBlock's stale guard should a future path schedule a piston tick; kept for symmetry with the
// diode/torch tick-type constants. CITE: PistonBaseBlock (minecraft:piston / minecraft:sticky_piston).
var pistonTickTypes = map[blockTickType]struct{}{
	"minecraft:piston":        {},
	"minecraft:sticky_piston": {},
}

// ---------------------------------------------------------------------------------------------
// moving-piston block-entity (PistonMovingBlockEntity) — region-owned animation state
// ---------------------------------------------------------------------------------------------

// movingPistonBE is the tick-owned PistonMovingBlockEntity animation state (redstone.go's region store):
// the moved block-state, the push direction, the extending/source flags, and the 0.0..1.0 progress the
// BE ticks over 2 ticks before finalTick completes the move. CITE: PistonMovingBlockEntity fields
// (movedState / direction / extending / isSourcePiston / progress / progressO).
type movingPistonBE struct {
	movedState     block.StateID // the block-state being carried (piston_head for the source arm)
	direction      block.Direction
	extending      bool
	isSourcePiston bool
	progress       float32
	progressO      float32
}

// ensureMovingPistons lazily builds the region's moving-piston store. Tick-owned.
func (t *TickLoop) ensureMovingPistons() map[pk.Position]*movingPistonBE {
	if t.cur().movingPistons == nil {
		t.cur().movingPistons = make(map[pk.Position]*movingPistonBE)
	}
	return t.cur().movingPistons
}

// newMovingBlockEntity is MovingPistonBlock.newMovingBlockEntity(pos, blockState, movedState, direction,
// extending, isSourcePiston): create + register the moving BE at pos (progress 0). CITE:
// MovingPistonBlock.newMovingBlockEntity / PistonMovingBlockEntity ctor.
func (t *TickLoop) newMovingBlockEntity(pos pk.Position, movedState block.StateID, direction block.Direction, extending, isSourcePiston bool) {
	t.ensureMovingPistons()[pos] = &movingPistonBE{
		movedState:     movedState,
		direction:      direction,
		extending:      extending,
		isSourcePiston: isSourcePiston,
		progress:       0,
		progressO:      0,
	}
}

// tickMovingPistons ticks every live PistonMovingBlockEntity once per tick (PistonMovingBlockEntity.tick):
//
//	entity.progressO = entity.progress;
//	if (progressO >= 1.0f) { removeBlockEntity; if (block is MOVING_PISTON) place the final movedState; }
//	else { progress += 0.5f; if (progress >= 1.0f) progress = 1.0f; }
//
// The moveCollidedEntities/moveStuckEntities entity-shove is DEFERRED (see file header). On completion
// the moving_piston block becomes the movedState (an air-source arm becomes air; a pushed block becomes
// its moved state), then the surrounding graph is re-notified so redstone reacts to the moved block.
// Tick-owned (TICK-05); called from tickWorld after the block/fluid drains (the tickFurnaces twin).
// CITE: PistonMovingBlockEntity.tick / finalTick.
func (t *TickLoop) tickMovingPistons() {
	if t.world() == nil || t.cur().movingPistons == nil || len(t.cur().movingPistons) == 0 {
		return
	}
	// Snapshot the keys so a completion that mutates the map does not disturb this iteration; vanilla
	// ticks each block-entity once from the chunk's ticker list.
	positions := make([]pk.Position, 0, len(t.cur().movingPistons))
	for p := range t.cur().movingPistons {
		positions = append(positions, p)
	}
	for _, pos := range positions {
		be := t.cur().movingPistons[pos]
		if be == nil {
			continue
		}
		be.progressO = be.progress
		if be.progressO >= 1.0 {
			t.movingPistonComplete(pos, be)
			continue
		}
		be.progress += 0.5
		if be.progress >= 1.0 {
			be.progress = 1.0
		}
	}
}

// movingPistonComplete is the progressO>=1.0 branch of PistonMovingBlockEntity.tick (== finalTick's
// completion): remove the BE and, if the block at pos is still MOVING_PISTON, replace it with the moved
// state. For a source-piston arm the moved state is the piston_head (isSourcePiston, movedState holds the
// head); for a pushed block it is the block's own moved state. Then re-notify neighbors so redstone /
// observers react to the settled block. CITE: PistonMovingBlockEntity.tick (removeBlockEntity + setBlock)
// / finalTick.
func (t *TickLoop) movingPistonComplete(pos pk.Position, be *movingPistonBE) {
	delete(t.cur().movingPistons, pos)
	cur := t.redstoneBlockAt(pos)
	if !block.IsMovingPiston(cur) {
		return // the transient block was already replaced (a re-triggered piston finalTick'd it early)
	}
	final := be.movedState
	if block.IsAir(final) {
		// An air moved-state (a retract that pulled nothing / a source arm that vacated) settles to air.
		if t.world().SetBlock(pos, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(pos, t.airState())
		}
	} else if t.world().SetBlock(pos, final, dimMinY) {
		t.broadcastBlockUpdate(pos, final)
	}
	// level.neighborChanged(pos, newState, ...) — wake the redstone graph + observers around the settled
	// block so a wire/torch/piston/observer next to the moved block recomputes.
	t.onRedstoneEdit(pos)
	t.onObserverEdit(pos)
}

// finalTickMovingPistonAt is PistonMovingBlockEntity.finalTick invoked out-of-band (triggerEvent's
// retract path calls finalTick on the arm/front BE to force-complete an in-flight animation before
// starting a new move). It completes the BE immediately at its current position. CITE:
// PistonBaseBlock.triggerEvent (pistonMovingBlockEntity.finalTick()).
func (t *TickLoop) finalTickMovingPistonAt(pos pk.Position) {
	if t.cur().movingPistons == nil {
		return
	}
	be := t.cur().movingPistons[pos]
	if be == nil {
		return
	}
	be.progressO = 1.0
	be.progress = 1.0
	t.movingPistonComplete(pos, be)
}

// ---------------------------------------------------------------------------------------------
// checkIfExtend (PistonBaseBlock.checkIfExtend) — the neighborChanged/onPlace trigger
// ---------------------------------------------------------------------------------------------

// pistonCheckIfExtend is PistonBaseBlock.checkIfExtend(level, pos, state):
//
//	Direction direction = state.getValue(FACING);
//	boolean extend = getNeighborSignal(level, pos, direction);
//	if (extend && !EXTENDED) { if (new PistonStructureResolver(...).resolve()) blockEvent(pos, this, 0, dir3D); }
//	else if (!extend && EXTENDED) { ... blockEvent(pos, this, event, dir3D); }
//
// The retract-event selection (event 1 vs 2) mirrors the vanilla check for a still-extending moving
// piston in front; with our simplified 2-tick BE, the fast-cancel (event 2) collapses to the normal
// retract (event 1) whenever no in-flight extend-arm is present, which is the common case. CITE:
// PistonBaseBlock.checkIfExtend.
func (t *TickLoop) pistonCheckIfExtend(pos pk.Position, state block.StateID) {
	facing, ok := block.PistonFacing(state)
	if !ok {
		return
	}
	extend := t.pistonGetNeighborSignal(pos, facing)
	extended := block.PistonExtended(state)
	if extend && !extended {
		r := &pistonStructureResolver{t: t, pistonPos: pos, extending: true}
		r.init(facing)
		if r.resolve() {
			t.pistonBlockEventPush(pos, pistonTriggerExtend, facing)
		}
		return
	}
	if !extend && extended {
		event := pistonTriggerContract
		// The event-2 fast-cancel path checks for an isExtending moving-piston 2 cells in front whose
		// progress < 0.5. Our BE completes in 2 ticks; if such an in-flight extend-arm exists we use
		// event 2 (drop) exactly as vanilla, else the normal retract (event 1).
		pushedPos := relativeN(pos, facing, 2)
		if pushedState := t.redstoneBlockAt(pushedPos); block.IsMovingPiston(pushedState) {
			if mf, okf := block.MovingPistonFacing(pushedState); okf && mf == facing {
				if be := t.movingPistonBEAt(pushedPos); be != nil && be.extending && be.progress < 0.5 {
					event = pistonTriggerDrop
				}
			}
		}
		t.pistonBlockEventPush(pos, event, facing)
	}
}

// movingPistonBEAt returns the moving-piston BE at pos, or nil.
func (t *TickLoop) movingPistonBEAt(pos pk.Position) *movingPistonBE {
	if t.cur().movingPistons == nil {
		return nil
	}
	return t.cur().movingPistons[pos]
}

// pistonGetNeighborSignal is PistonBaseBlock.getNeighborSignal(level, pos, pushDirection) — the
// quasi-connectivity ("bud") power read vanilla 26.2 KEEPS:
//
//	for (Direction d : Direction.values()) if (d != pushDirection && hasSignal(pos.relative(d), d)) return true;
//	if (hasSignal(pos, DOWN)) return true;
//	BlockPos above = pos.above();
//	for (Direction d : Direction.values()) if (d != DOWN && hasSignal(above.relative(d), d)) return true;
//	return false;
//
// The second loop (over the block ABOVE the piston) is the quasi-connectivity that lets a redstone
// signal reaching the block above the piston power it. Verified present in 26.2 bytecode. CITE:
// PistonBaseBlock.getNeighborSignal.
func (t *TickLoop) pistonGetNeighborSignal(pos pk.Position, pushDirection block.Direction) bool {
	for _, d := range redstoneDirs {
		if d == pushDirection {
			continue
		}
		if t.hasSignal(relative(pos, d), d) {
			return true
		}
	}
	if t.hasSignal(pos, block.Down) {
		return true
	}
	above := relative(pos, block.Up)
	for _, d := range redstoneDirs {
		if d == block.Down {
			continue
		}
		if t.hasSignal(relative(above, d), d) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------
// block-event queue (Level.blockEvent / ServerLevel.runBlockEvents -> triggerEvent)
// ---------------------------------------------------------------------------------------------

// pistonBlockEvent is one enqueued Level.blockEvent(pos, block, b0, b1) for a piston: the piston's
// position, the trigger id (b0: 0 extend / 1 contract / 2 drop), and the facing (b1 = direction 3D
// data value; carried as the Direction). isSticky is captured so triggerEvent knows the piston kind
// even if the block state changed. CITE: ServerLevel.BlockEventData / Level.blockEvent.
type pistonBlockEvent struct {
	pos      pk.Position
	trigger  int
	facing   block.Direction
	isSticky bool
}

// pistonBlockEventPush is Level.blockEvent(pos, this, b0, direction.get3DDataValue()): enqueue a piston
// block event onto the region's ServerLevel.blockEvents queue. Drained at the end of tickWorld by
// drainPistonBlockEvents. CITE: Level.blockEvent.
func (t *TickLoop) pistonBlockEventPush(pos pk.Position, trigger int, facing block.Direction) {
	sticky := block.IsStickyPiston(t.redstoneBlockAt(pos))
	t.cur().pistonBlockEvents = append(t.cur().pistonBlockEvents, pistonBlockEvent{
		pos: pos, trigger: trigger, facing: facing, isSticky: sticky,
	})
}

// drainPistonBlockEvents is ServerLevel.runBlockEvents(): fire every queued piston block event this
// tick as triggerEvent. A block event whose piston is no longer there (state changed since the event
// was posted) fires nothing (the `getBlockState(pos).is(block)` guard inside triggerEvent). New events
// posted DURING a triggerEvent (a chain reaction) are appended and drained in the same pass, mirroring
// vanilla's single-tick drain of the blockEvents set. Tick-owned; called once per tick from tickWorld
// AFTER the scheduled block/fluid drains. CITE: ServerLevel.runBlockEvents / BlockState.triggerEvent.
func (t *TickLoop) drainPistonBlockEvents() {
	if t.world() == nil {
		return
	}
	// Process FIFO; new events append to the tail. Bounded to avoid a pathological self-retriggering
	// loop (a converging piston chain settles well within this).
	budget := 1 << 16
	for len(t.cur().pistonBlockEvents) > 0 {
		budget--
		if budget < 0 {
			return
		}
		ev := t.cur().pistonBlockEvents[0]
		t.cur().pistonBlockEvents = t.cur().pistonBlockEvents[1:]
		t.pistonTriggerEvent(ev)
	}
}

// ---------------------------------------------------------------------------------------------
// triggerEvent (PistonBaseBlock.triggerEvent) — the actual extend / retract
// ---------------------------------------------------------------------------------------------

// pistonTriggerEvent is PistonBaseBlock.triggerEvent(state, level, pos, b0, b1):
//
//	Direction direction = FACING; BlockState extendedState = state.setValue(EXTENDED, true);
//	boolean extend = getNeighborSignal(...);
//	if (extend && (b0==1||b0==2)) { setBlock(pos, extendedState, 2); return false; }   // re-powered mid-retract
//	if (!extend && b0==0) return false;                                                 // un-powered mid-extend
//	if (b0 == 0) { if (!moveBlocks(extending=true)) return; setBlock(pos, extendedState, 67); }
//	else /* 1|2 */ { finalTick front arm; place moving_piston(source head) BE; setBlock(pos, moving, ...);
//	                 if sticky pull front block via moveBlocks(extending=false) else removeBlock(front); }
//
// CITE: PistonBaseBlock.triggerEvent.
func (t *TickLoop) pistonTriggerEvent(ev pistonBlockEvent) {
	state := t.redstoneBlockAt(ev.pos)
	if !block.IsPiston(state) {
		return // triggerEvent's implicit `getBlockState(pos).is(this)` guard: stale event, fire nothing.
	}
	direction := ev.facing
	extendedState, _ := block.PistonWithExtended(state, true)
	extend := t.pistonGetNeighborSignal(ev.pos, direction)
	if extend && (ev.trigger == pistonTriggerContract || ev.trigger == pistonTriggerDrop) {
		// Re-powered before the retract executes: just re-assert EXTENDED (setBlock flag 2), no move.
		if t.world().SetBlock(ev.pos, extendedState, dimMinY) {
			t.broadcastBlockUpdate(ev.pos, extendedState)
		}
		return
	}
	if !extend && ev.trigger == pistonTriggerExtend {
		return // un-powered before the extend executes: nothing happens.
	}

	if ev.trigger == pistonTriggerExtend {
		if !t.pistonMoveBlocks(ev.pos, direction, true) {
			return
		}
		// setBlock(pos, extendedState, 67): mark the piston EXTENDED. The head arm was placed by moveBlocks.
		if t.world().SetBlock(ev.pos, extendedState, dimMinY) {
			t.broadcastBlockUpdate(ev.pos, extendedState)
		}
		t.onRedstoneEdit(ev.pos)
		t.onObserverEdit(ev.pos)
		return
	}

	// Retract (b0 == 1 or 2).
	armPos := relative(ev.pos, direction)
	// finalTick any in-flight extend animation on the arm cell.
	t.finalTickMovingPistonAt(armPos)
	// setBlock(pos, movingPistonState, 276) + the source-piston moving BE: the piston base itself becomes
	// a moving_piston carrying the retracted piston (its defaultBlockState with FACING) as the source.
	pistonType := block.PistonTypeNormal
	if ev.isSticky {
		pistonType = block.PistonTypeSticky
	}
	movingState, okm := block.MovingPistonState(direction, pistonType)
	// The moved (source) state the base carries: the piston's own default (un-extended) block.
	baseDefault, _ := block.PistonWithExtended(state, false)
	if okm && t.world().SetBlock(ev.pos, movingState, dimMinY) {
		t.broadcastBlockUpdate(ev.pos, movingState)
		t.newMovingBlockEntity(ev.pos, baseDefault, direction, false /*extending*/, true /*source*/)
		t.onRedstoneEdit(ev.pos)
	}

	if ev.isSticky {
		// Sticky: pull the block 2 cells in front (pos + 2*dir) back toward the piston via
		// moveBlocks(extending=false), unless that cell holds an in-flight extend arm (pistonPiece).
		twoPos := relativeN(ev.pos, direction, 2)
		movingState2 := t.redstoneBlockAt(twoPos)
		pistonPiece := false
		if block.IsMovingPiston(movingState2) {
			if be := t.movingPistonBEAt(twoPos); be != nil && be.direction == direction && be.extending {
				t.finalTickMovingPistonAt(twoPos)
				pistonPiece = true
			}
		}
		if !pistonPiece {
			pushable := !block.IsAir(movingState2) &&
				pistonIsPushable(movingState2, twoPos, dirOpposite(direction), false, direction, t) &&
				(block.PistonPushReaction(movingState2) == block.PushReactionNormal ||
					block.IsPiston(movingState2))
			if ev.trigger == pistonTriggerContract && pushable {
				t.pistonMoveBlocks(ev.pos, direction, false)
			} else {
				// removeBlock(pos.relative(direction)): clear the head arm cell (the sticky head retracts
				// with nothing to pull).
				t.pistonRemoveHead(relative(ev.pos, direction))
			}
		}
	} else {
		// Non-sticky: just remove the head arm.
		t.pistonRemoveHead(relative(ev.pos, direction))
	}
}

// pistonRemoveHead is Level.removeBlock(pos.relative(direction), false) for the head-arm cell during
// retract: clear the piston_head (or its moving stand-in) so the arm vanishes. CITE:
// PistonBaseBlock.triggerEvent (level.removeBlock(pos.relative(direction), false)).
func (t *TickLoop) pistonRemoveHead(pos pk.Position) {
	cur := t.redstoneBlockAt(pos)
	if block.IsPistonHead(cur) || block.IsMovingPiston(cur) {
		if t.world().SetBlock(pos, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(pos, t.airState())
		}
		if block.IsMovingPiston(cur) && t.cur().movingPistons != nil {
			delete(t.cur().movingPistons, pos)
		}
		t.onRedstoneEdit(pos)
		t.onObserverEdit(pos)
	}
}

// ---------------------------------------------------------------------------------------------
// moveBlocks (PistonBaseBlock.moveBlocks) — the actual block shove
// ---------------------------------------------------------------------------------------------

// pistonMoveBlocks is PistonBaseBlock.moveBlocks(level, pistonPos, direction, extending): resolve the
// push list, then move every pushed block by 1 in the push direction (placing a moving_piston + BE at
// the DESTINATION and clearing the SOURCE), destroy every to-destroy block, and (when extending) place
// the head arm. Returns false if the resolver fails (the push is blocked / exceeds 12). The moving
// animation is cited-simplified: the destination gets a moving_piston block whose BE completes to the
// moved state in 2 ticks; the observable END STATE is identical to vanilla. CITE:
// PistonBaseBlock.moveBlocks.
func (t *TickLoop) pistonMoveBlocks(pistonPos pk.Position, direction block.Direction, extending bool) bool {
	armPos := relative(pistonPos, direction)
	if !extending {
		// A retract first clears a leftover piston_head at the arm cell (setBlock air, flag 276).
		if block.IsPistonHead(t.redstoneBlockAt(armPos)) {
			if t.world().SetBlock(armPos, t.airState(), dimMinY) {
				t.broadcastBlockUpdate(armPos, t.airState())
			}
		}
	}
	r := &pistonStructureResolver{t: t, pistonPos: pistonPos, extending: extending}
	r.init(direction)
	if !r.resolve() {
		return false
	}
	sticky := block.IsStickyPiston(t.redstoneBlockAt(pistonPos))
	pistonType := block.PistonTypeNormal
	if sticky {
		pistonType = block.PistonTypeSticky
	}
	pushDirection := direction
	if !extending {
		pushDirection = dirOpposite(direction)
	}

	toPush := r.toPush
	toDestroy := r.toDestroy

	// deleteAfterMove starts as every source cell of a pushed block; cells that become a move DESTINATION
	// are removed from it, leaving only the cells that must be set to air (the tails of each moved line).
	// CITE: PistonBaseBlock.moveBlocks (Map<BlockPos,BlockState> deleteAfterMove).
	deleteAfterMove := make(map[pk.Position]bool, len(toPush))
	pushedStates := make([]block.StateID, len(toPush))
	for i, p := range toPush {
		pushedStates[i] = t.redstoneBlockAt(p)
		deleteAfterMove[p] = true
	}

	// Destroy pass (reverse order): drop + set air for each to-destroy block. CITE: moveBlocks toDestroy loop.
	for i := len(toDestroy) - 1; i >= 0; i-- {
		p := toDestroy[i]
		st := t.redstoneBlockAt(p)
		t.spawnBlockDrop(nil, p, st)
		if t.world().SetBlock(p, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(p, t.airState())
		}
	}

	// Push pass (reverse order): each pushed block's DESTINATION cell (p + pushDir) becomes a moving_piston
	// carrying the pushed block's state. CITE: moveBlocks toPush loop.
	for i := len(toPush) - 1; i >= 0; i-- {
		src := toPush[i]
		dst := relative(src, pushDirection)
		delete(deleteAfterMove, dst) // a destination is not a to-air tail
		movingState, ok := block.MovingPistonState(direction, block.PistonTypeNormal)
		if !ok {
			continue
		}
		if t.world().SetBlock(dst, movingState, dimMinY) {
			t.broadcastBlockUpdate(dst, movingState)
		}
		t.newMovingBlockEntity(dst, pushedStates[i], direction, extending, false /*not source*/)
	}

	// Extending: place the head arm as a moving_piston carrying the piston_head (source). CITE: moveBlocks
	// extending branch (armPos <- moving_piston BE(head, isSource=true)).
	if extending {
		headState, okh := block.PistonHeadState(direction, pistonType, false)
		movingArm, okm := block.MovingPistonState(direction, pistonType)
		if okh && okm {
			delete(deleteAfterMove, armPos)
			if t.world().SetBlock(armPos, movingArm, dimMinY) {
				t.broadcastBlockUpdate(armPos, movingArm)
			}
			t.newMovingBlockEntity(armPos, headState, direction, true /*extending*/, true /*source*/)
		}
	}

	// Every remaining source cell (a moved-line tail) is set to air. CITE: moveBlocks deleteAfterMove loop.
	for p := range deleteAfterMove {
		if t.world().SetBlock(p, t.airState(), dimMinY) {
			t.broadcastBlockUpdate(p, t.airState())
		}
	}

	// updateNeighborsAt for each destroyed/pushed cell + the arm — wake the redstone graph + observers
	// around every touched cell. CITE: moveBlocks trailing updateNeighborsAt loops.
	for _, p := range toDestroy {
		t.onRedstoneEdit(p)
		t.onObserverEdit(p)
	}
	for _, p := range toPush {
		t.onRedstoneEdit(p)
		t.onObserverEdit(p)
		dst := relative(p, pushDirection)
		t.onRedstoneEdit(dst)
		t.onObserverEdit(dst)
	}
	if extending {
		t.onRedstoneEdit(armPos)
		t.onObserverEdit(armPos)
	}
	return true
}

// ---------------------------------------------------------------------------------------------
// isPushable (PistonBaseBlock.isPushable)
// ---------------------------------------------------------------------------------------------

// pistonIsPushable is PistonBaseBlock.isPushable(state, level, pos, direction, allowDestroyable,
// connectionDirection):
//
//	if (out of world / border) return false;
//	if (isAir) return true;
//	if (obsidian|crying_obsidian|respawn_anchor|reinforced_deepslate) return false;
//	if (direction==DOWN && y==minY) return false;
//	if (direction==UP   && y==maxY) return false;
//	if (piston|sticky_piston) { if (EXTENDED) return false; }
//	else { if (destroySpeed == -1) return false;
//	       switch (pushReaction) { BLOCK -> false; DESTROY -> allowDestroyable; PUSH_ONLY -> dir==connectionDirection; } }
//	return !hasBlockEntity();
//
// CITE: PistonBaseBlock.isPushable. hasBlockEntity is approximated by "is one of the block-entity blocks
// Sulfur tracks" — for the piston push scope the relevant BE blocks are chests/furnaces/etc, which are
// not pushable; a plain block has no BE. The world-border check is DEFERRED (no world border in v1; the
// isWithinBounds always-true). CITE header notes.
func pistonIsPushable(state block.StateID, pos pk.Position, direction block.Direction, allowDestroyable bool, connectionDirection block.Direction, t *TickLoop) bool {
	if pos.Y < dimMinY || pos.Y > dimMaxY {
		return false
	}
	if block.IsAir(state) {
		return true
	}
	if pistonIsImmovableSpecial(state) {
		return false
	}
	if direction == block.Down && pos.Y == dimMinY {
		return false
	}
	if direction == block.Up && pos.Y == dimMaxY {
		return false
	}
	if block.IsPiston(state) {
		if block.PistonExtended(state) {
			return false
		}
		return true // a non-extended piston has no relevant BE; pushable.
	}
	// destroySpeed == -1 (unbreakable: bedrock, barrier) -> not pushable. CITE: state.getDestroySpeed == -1.
	if id := block.StateList[state].ID(); block.Hardness[id].DestroySpeed == -1.0 {
		return false
	}
	switch block.PistonPushReaction(state) {
	case block.PushReactionBlock:
		return false
	case block.PushReactionDestroy:
		return allowDestroyable
	case block.PushReactionPushOnly:
		return direction == connectionDirection
	}
	// NORMAL: pushable iff no block-entity. Sulfur's BE-carrying blocks (chest/furnace/etc) are not
	// pushable; a plain block is. CITE: return !state.hasBlockEntity().
	return !pistonHasBlockEntity(state)
}

// pistonIsImmovableSpecial is the obsidian family hard-block list from isPushable: obsidian,
// crying_obsidian, respawn_anchor, reinforced_deepslate. CITE: PistonBaseBlock.isPushable.
func pistonIsImmovableSpecial(state block.StateID) bool {
	switch block.StateList[state].ID() {
	case "minecraft:obsidian", "minecraft:crying_obsidian",
		"minecraft:respawn_anchor", "minecraft:reinforced_deepslate":
		return true
	default:
		return false
	}
}

// pistonHasBlockEntity approximates BlockState.hasBlockEntity() for the isPushable NORMAL tail: a block
// that carries a block-entity is NOT pushable. Sulfur has no per-state hasBlockEntity flag baked yet, so
// this checks the block-entity-bearing block ids in the piston push scope (containers/mechanisms). A
// block absent from this set has no BE and is pushable. CITE: BlockState.hasBlockEntity (structured to
// become a baked per-state read when the block-entity flag table lands).
func pistonHasBlockEntity(state block.StateID) bool {
	switch block.StateList[state].ID() {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand", "minecraft:hopper", "minecraft:dropper", "minecraft:dispenser",
		"minecraft:barrel", "minecraft:beacon", "minecraft:comparator", "minecraft:moving_piston",
		"minecraft:jukebox", "minecraft:lectern", "minecraft:bell", "minecraft:conduit",
		"minecraft:enchanting_table", "minecraft:sign", "minecraft:campfire", "minecraft:soul_campfire":
		return true
	default:
		return false
	}
}

// dimMaxY is the dimension max build height (overworld -64..319). Used by isPushable's DOWN/UP edge
// checks. CITE: Level.getMaxY.
const dimMaxY = 319

// relativeN offsets pos by n steps in direction d (BlockPos.relative(direction, n)). CITE:
// BlockPos.relative(Direction, int).
func relativeN(p pk.Position, d block.Direction, n int) pk.Position {
	dx, dy, dz := dirVec(d)
	return pk.Position{X: p.X + dx*n, Y: p.Y + dy*n, Z: p.Z + dz*n}
}
