package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// piston_test.go — REDSTONE TIER-3 (PISTON + OBSERVER) validation gates. Each asserts the ported
// behaviour / values against the unobfuscated 26.2 jar:
//   - a powered piston extends: places a piston_head in front + moves the block ahead by 1
//     (PistonBaseBlock.triggerEvent/moveBlocks; PistonStructureResolver.resolve);
//   - unpowering a sticky piston retracts: pulls the moved block back by 1 (sticky moveBlocks retract);
//   - a push of >12 blocks fails to move (PistonStructureResolver.MAX_PUSH_DEPTH == 12);
//   - an observer pulses POWERED for 2 ticks out its FACING face when the watched block changes
//     (ObserverBlock.startSignal/tick, scheduleTick delay 2).

// newPistonLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container for
// column (0,0), so observer scheduled ticks route into a live container. Mirrors newRedstoneLoop.
func newPistonLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// completeMovingPistons ticks the moving-piston block-entities until none remain (bounded), completing
// every in-flight animation. Returns the number of BE-ticks it took. CITE: PistonMovingBlockEntity.tick.
func (t *TickLoop) completeMovingPistons() int {
	n := 0
	for t.cur().movingPistons != nil && len(t.cur().movingPistons) > 0 && n < 16 {
		t.tickMovingPistons()
		n++
	}
	return n
}

// TestPistonExtendsPlacesHeadAndMovesBlock is the headline extend gate: a piston FACING east with a
// stone block directly in front, powered by a lever on its back, posts an extend block event; draining
// it places a piston_head at pos+east and starts moving the stone to pos+2*east; after the animation
// completes the stone has moved by exactly 1. CITE: PistonBaseBlock.triggerEvent (b0=0) / moveBlocks.
func TestPistonExtendsPlacesHeadAndMovesBlock(t *testing.T) {
	loop, mgr := newPistonLoop()

	pistonPos := pk.Position{X: 4, Y: 64, Z: 4}
	frontPos := pk.Position{X: 5, Y: 64, Z: 4} // pos + east: the block to push
	headPos := frontPos                        // the head lands where the front block was
	destPos := pk.Position{X: 6, Y: 64, Z: 4}  // pos + 2*east: where the pushed block lands
	leverPos := pk.Position{X: 3, Y: 64, Z: 4} // pos + west (behind): powers the piston

	mgr.SetBlock(pistonPos, block.ToStateID[block.Piston{Facing: block.East, Extended: false}], dimMinY)
	mgr.SetBlock(frontPos, stoneState(), dimMinY)
	// A wall lever behind the piston, powered — its strong signal into the piston cell powers it (the
	// piston's getNeighborSignal hasSignal check reads the lever's direct output).
	mgr.SetBlock(leverPos, block.ToStateID[block.Lever{Face: block.AttachFaceWall, Facing: block.West, Powered: true}], dimMinY)

	state := loop.redstoneBlockAt(pistonPos)
	if !loop.pistonGetNeighborSignal(pistonPos, block.East) {
		t.Fatal("piston should read power from the adjacent powered lever (getNeighborSignal)")
	}
	loop.pistonCheckIfExtend(pistonPos, state)
	if len(loop.cur().pistonBlockEvents) == 0 {
		t.Fatal("powered un-extended piston should post an extend block event")
	}
	loop.drainPistonBlockEvents()

	// After the event: the piston is EXTENDED, the head cell holds a moving_piston (the arm animating),
	// and the pushed block's destination holds a moving_piston (the stone animating).
	if s := loop.redstoneBlockAt(pistonPos); !block.PistonExtended(s) {
		t.Fatal("piston should be EXTENDED after the extend event")
	}
	if s := loop.redstoneBlockAt(headPos); !block.IsMovingPiston(s) {
		t.Fatalf("head cell should be moving_piston during animation, got %d", s)
	}
	if s := loop.redstoneBlockAt(destPos); !block.IsMovingPiston(s) {
		t.Fatalf("destination cell should be moving_piston during animation, got %d", s)
	}
	// One BE-tick must NOT complete the move yet (progress 0 -> 0.5; the delay is >1 tick).
	loop.tickMovingPistons()
	if s := loop.redstoneBlockAt(destPos); !block.IsMovingPiston(s) {
		t.Fatal("moving_piston completed after only 1 tick — the animation must take multiple ticks")
	}

	loop.completeMovingPistons()
	// End state: the pushed stone landed at destPos; the head arm settled to a real piston_head; the
	// original front cell is now the head (the arm), NOT stone.
	if s := loop.redstoneBlockAt(destPos); s != stoneState() {
		t.Fatalf("pushed stone should have moved by 1 to destPos, got %d", s)
	}
	if s := loop.redstoneBlockAt(headPos); !block.IsPistonHead(s) {
		t.Fatalf("head cell should settle to a piston_head, got %d", s)
	}
	if f, ok := block.PistonHeadFacing(loop.redstoneBlockAt(headPos)); !ok || f != block.East {
		t.Fatalf("piston_head FACING = %v (ok=%v), want East", f, ok)
	}
}

// TestStickyPistonRetractsPullsBlock is the sticky retract gate: a sticky piston already EXTENDED (head
// in front, a stone block one further) loses power; the retract event pulls the head back and drags the
// stuck block back by 1 (sticky moveBlocks extending=false). CITE: PistonBaseBlock.triggerEvent (b0=1)
// sticky branch / moveBlocks retract.
func TestStickyPistonRetractsPullsBlock(t *testing.T) {
	loop, mgr := newPistonLoop()

	pistonPos := pk.Position{X: 4, Y: 64, Z: 4}
	headPos := pk.Position{X: 5, Y: 64, Z: 4}  // pos + east: the extended head
	stuckPos := pk.Position{X: 6, Y: 64, Z: 4} // pos + 2*east: the block stuck to the head

	// A sticky piston already extended, head in front, a stone block against the head. No power (the
	// lever is absent), so getNeighborSignal is false -> the retract event fires.
	mgr.SetBlock(pistonPos, block.ToStateID[block.StickyPiston{Facing: block.East, Extended: true}], dimMinY)
	mgr.SetBlock(headPos, block.ToStateID[block.PistonHead{Facing: block.East, Type: block.PistonTypeSticky, Short: false}], dimMinY)
	mgr.SetBlock(stuckPos, stoneState(), dimMinY)

	state := loop.redstoneBlockAt(pistonPos)
	if loop.pistonGetNeighborSignal(pistonPos, block.East) {
		t.Fatal("unpowered piston should read NO neighbor signal")
	}
	loop.pistonCheckIfExtend(pistonPos, state)
	if len(loop.cur().pistonBlockEvents) == 0 {
		t.Fatal("un-powered EXTENDED piston should post a retract block event")
	}
	loop.drainPistonBlockEvents()
	loop.completeMovingPistons()

	// End state: the piston is no longer EXTENDED; the head cell is where the stone was pulled TO (the
	// stone dragged back by 1 from stuckPos to headPos); the far stuck cell is now air.
	if s := loop.redstoneBlockAt(pistonPos); block.IsPiston(s) && block.PistonExtended(s) {
		t.Fatal("piston should be retracted (not EXTENDED) after the retract event")
	}
	if s := loop.redstoneBlockAt(headPos); s != stoneState() {
		t.Fatalf("sticky retract should pull the stone back by 1 to the head cell, got %d", s)
	}
	if s := loop.redstoneBlockAt(stuckPos); !block.IsAir(s) {
		t.Fatalf("the far cell should be air after the block was pulled back, got %d", s)
	}
}

// TestPistonPushLimitTwelve locks PistonStructureResolver.MAX_PUSH_DEPTH == 12: a line of 13 pushable
// blocks in front of a piston cannot be moved, so resolve() fails and NO extend event is posted (a line
// of 12 succeeds). CITE: PistonStructureResolver.addBlockLine (`toPush.size() >= 12 return false`).
func TestPistonPushLimitTwelve(t *testing.T) {
	loop, mgr := newPistonLoop()
	pistonPos := pk.Position{X: 1, Y: 64, Z: 4}

	setLine := func(count int) {
		// Clear the row then place `count` stone blocks starting at pos+east.
		for x := 2; x <= 15; x++ {
			mgr.SetBlock(pk.Position{X: x, Y: 64, Z: 4}, loop.airState(), dimMinY)
		}
		for i := 0; i < count; i++ {
			mgr.SetBlock(pk.Position{X: 2 + i, Y: 64, Z: 4}, stoneState(), dimMinY)
		}
	}

	// 12 movable blocks: resolve succeeds.
	setLine(12)
	r := &pistonStructureResolver{t: loop, pistonPos: pistonPos, extending: true}
	r.init(block.East)
	if !r.resolve() {
		t.Fatal("a 12-block line must resolve (MAX_PUSH_DEPTH == 12)")
	}
	if len(r.toPush) != 12 {
		t.Fatalf("resolve of a 12-block line pushed %d blocks, want 12", len(r.toPush))
	}

	// 13 movable blocks: resolve fails (exceeds the 12 limit).
	setLine(13)
	r2 := &pistonStructureResolver{t: loop, pistonPos: pistonPos, extending: true}
	r2.init(block.East)
	if r2.resolve() {
		t.Fatal("a 13-block line must FAIL to resolve (MAX_PUSH_DEPTH == 12)")
	}

	// And end-to-end: a piston facing a 13-block line, powered, posts NO extend event (checkIfExtend's
	// resolve() gate fails, so no blockEvent).
	mgr.SetBlock(pistonPos, block.ToStateID[block.Piston{Facing: block.East, Extended: false}], dimMinY)
	mgr.SetBlock(pk.Position{X: 0, Y: 64, Z: 4}, block.ToStateID[block.Lever{Face: block.AttachFaceWall, Facing: block.West, Powered: true}], dimMinY)
	loop.cur().pistonBlockEvents = nil
	loop.pistonCheckIfExtend(pistonPos, loop.redstoneBlockAt(pistonPos))
	if len(loop.cur().pistonBlockEvents) != 0 {
		t.Fatal("a piston facing a 13-block line must NOT post an extend event (resolve fails)")
	}
}

// TestObserverPulsesTwoTicks locks the observer 2-tick pulse: an observer FACING east watches the cell
// to its east; changing that cell schedules a 2-tick tick that toggles POWERED true (emitting 15 out
// FACING), and a further 2-tick tick toggles it back false. CITE: ObserverBlock.startSignal/tick
// (scheduleTick delay 2) / getSignal (ownSignal only out FACING).
func TestObserverPulsesTwoTicks(t *testing.T) {
	loop, mgr := newPistonLoop()

	obsPos := pk.Position{X: 4, Y: 64, Z: 4}
	watchPos := pk.Position{X: 5, Y: 64, Z: 4} // pos + east (FACING): the watched cell
	mgr.SetBlock(obsPos, block.ToStateID[block.Observer{Facing: block.East, Powered: false}], dimMinY)

	// Change the watched block: place a stone there and wake the observer via the updateShape hook.
	mgr.SetBlock(watchPos, stoneState(), dimMinY)
	loop.onObserverEdit(watchPos)
	if !loop.hasScheduledBlockTick(obsPos, observerTickType) {
		t.Fatal("observer should schedule a 2-tick tick when its watched block changes (startSignal)")
	}

	// One tick must NOT fire it (delay is 2).
	loop.gametime++
	loop.tickScheduledBlocks()
	if block.ObserverPowered(loop.redstoneBlockAt(obsPos)) {
		t.Fatal("observer powered after only 1 tick — startSignal delay must be 2")
	}

	// Second tick: POWERED flips true (pulse start) and it emits 15 out its FACING face.
	loop.gametime++
	loop.tickScheduledBlocks()
	on := loop.redstoneBlockAt(obsPos)
	if !block.ObserverPowered(on) {
		t.Fatal("observer should be POWERED after its 2-tick startSignal fired")
	}
	if got := loop.stateGetSignal(on, obsPos, block.East); got != 15 {
		t.Fatalf("powered observer getSignal(East/FACING) = %d, want 15", got)
	}
	if got := loop.stateGetSignal(on, obsPos, block.West); got != 0 {
		t.Fatalf("observer getSignal(West/not-FACING) = %d, want 0 (emits only out FACING)", got)
	}
	// The on->off tick was scheduled 2 ticks out.
	if !loop.hasScheduledBlockTick(obsPos, observerTickType) {
		t.Fatal("observer should schedule the off tick when it turns on")
	}

	// One tick: still POWERED (the off delay is 2).
	loop.gametime++
	loop.tickScheduledBlocks()
	if !block.ObserverPowered(loop.redstoneBlockAt(obsPos)) {
		t.Fatal("observer un-powered after only 1 tick — the pulse must last 2 ticks")
	}
	// Second tick: POWERED flips back false (pulse end).
	loop.gametime++
	loop.tickScheduledBlocks()
	if block.ObserverPowered(loop.redstoneBlockAt(obsPos)) {
		t.Fatal("observer should be UN-powered after the 2-tick pulse ends")
	}
}
