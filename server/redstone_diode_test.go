package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// redstone_diode_test.go — REDSTONE TIER-2 validation gates (repeater + comparator). Each asserts the
// ported diode logic against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar):
//   - a repeater delays + repeats a signal after DELAY*2 ticks and outputs ONLY out its FACING;
//   - a repeater is LOCKED by a powered repeater facing it from the side (getAlternateSignal > 0);
//   - a comparator in compare mode passes input when input >= side, else 0;
//   - a comparator in subtract mode outputs max(input - side, 0).
// The delays + signal math are asserted against jar bytecode:
//   RepeaterBlock.getDelay = DELAY*2 (2/4/6/8), DiodeBlock.tick/getSignal(FACING-only),
//   RepeaterBlock.isLocked (getAlternateSignal>0), ComparatorBlock.calculateOutputSignal.

// diodeReadSignal is a small helper: read the live state at pos and return its getSignal toward dir.
func (t *TickLoop) diodeReadSignal(pos pk.Position, dir block.Direction) int {
	s := t.redstoneBlockAt(pos)
	return t.stateGetSignal(s, pos, dir)
}

// TestRepeaterDelaysRepeatsAndOutputsOnlyFacing: a repeater with DELAY=1 (2-tick delay), FACING=South,
// fed a 15 input on its input face (pos+FACING), turns POWERED after exactly 2 ticks and then emits 15
// ONLY out its FACING face (getSignal(FACING)==15, every other face 0). CITE: RepeaterBlock.getDelay
// (DELAY*2), DiodeBlock.tick (POWERED flip), DiodeBlock.getSignal (FACING-only ownSignal).
func TestRepeaterDelaysRepeatsAndOutputsOnlyFacing(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	// Repeater at P, FACING=South. Its INPUT is read from pos+South; its OUTPUT is emitted so the cell
	// at pos+North sees it. A sturdy stone floor below (RIGID) lets the repeater survive.
	p := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	rep := block.ToStateID[block.Repeater{Delay: 1, Facing: block.South, Locked: false, Powered: false}]
	mgr.SetBlock(p, rep, dimMinY)

	// getDelay must be DELAY*2 == 2 for DELAY=1.
	if got := diodeGetDelay(rep); got != 2 {
		t.Fatalf("repeater(DELAY=1) getDelay = %d, want 2", got)
	}

	// Feed the input face (pos+South) with a constant-15 source (redstone_block emits weak 15 all faces).
	inputPos := pk.Position{X: 4, Y: 64, Z: 5} // South of P
	mgr.SetBlock(inputPos, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	// shouldTurnOn reads getInputSignal at the FACING face -> 15 > 0.
	if !loop.diodeShouldTurnOn(rep, p) {
		t.Fatal("repeater with a 15 input should shouldTurnOn")
	}

	// Wake the graph at the repeater cell: onRedstoneEdit enqueues pos, drainRedstoneUpdates dispatches
	// the diode's neighborChanged -> checkTickOnNeighbor, which schedules the delayed flip.
	loop.onRedstoneEdit(p)
	if !loop.hasScheduledBlockTick(p, repeaterTickType) {
		t.Fatal("repeater should schedule its output-flip tick when its input turns on")
	}

	// Not yet powered before the delay elapses.
	if block.RepeaterPowered(func() block.StateID { s, _ := mgr.GetBlock(p, dimMinY); return s }()) {
		t.Fatal("repeater must not be POWERED before its delay tick fires")
	}

	// One tick: delay is 2, so still unpowered.
	loop.gametime++
	loop.tickScheduledBlocks()
	if block.RepeaterPowered(func() block.StateID { s, _ := mgr.GetBlock(p, dimMinY); return s }()) {
		t.Fatal("repeater flipped after only 1 tick — DELAY*2 must be 2")
	}

	// Second tick: the flip fires, repeater becomes POWERED.
	loop.gametime++
	loop.tickScheduledBlocks()
	on, _ := mgr.GetBlock(p, dimMinY)
	if !block.RepeaterPowered(on) {
		t.Fatal("repeater should be POWERED after its 2-tick delay")
	}

	// Output ONLY out FACING (South): getSignal(South)==15, every other face 0.
	if got := loop.diodeReadSignal(p, block.South); got != 15 {
		t.Fatalf("powered repeater getSignal(FACING=South) = %d, want 15", got)
	}
	for _, d := range []block.Direction{block.North, block.East, block.West, block.Up, block.Down} {
		if got := loop.diodeReadSignal(p, d); got != 0 {
			t.Fatalf("powered repeater getSignal(%v) = %d, want 0 (emits only out FACING)", d, got)
		}
	}

	// Remove the input source: the repeater turns off after its delay again.
	mgr.SetBlock(inputPos, loop.airState(), dimMinY)
	loop.onRedstoneEdit(inputPos)
	if !loop.hasScheduledBlockTick(p, repeaterTickType) {
		t.Fatal("repeater should reschedule its flip when the input turns off")
	}
	loop.gametime++
	loop.tickScheduledBlocks()
	loop.gametime++
	loop.tickScheduledBlocks()
	off, _ := mgr.GetBlock(p, dimMinY)
	if block.RepeaterPowered(off) {
		t.Fatal("repeater should turn OFF after the input is removed (2-tick delay)")
	}
	if got := loop.diodeReadSignal(p, block.South); got != 0 {
		t.Fatalf("unpowered repeater getSignal(South) = %d, want 0", got)
	}
}

// TestRepeaterDelay4Is8Ticks locks the DELAY->tick mapping at the top of the range: a DELAY=4 repeater
// flips after DELAY*2 == 8 ticks, not fewer. CITE: RepeaterBlock.getDelay (DELAY*2 => 8 for DELAY=4).
func TestRepeaterDelay4Is8Ticks(t *testing.T) {
	loop, mgr := newRedstoneLoop()
	p := pk.Position{X: 6, Y: 64, Z: 6}
	mgr.SetBlock(pk.Position{X: 6, Y: 63, Z: 6}, stoneState(), dimMinY)
	rep := block.ToStateID[block.Repeater{Delay: 4, Facing: block.South, Locked: false, Powered: false}]
	mgr.SetBlock(p, rep, dimMinY)
	if got := diodeGetDelay(rep); got != 8 {
		t.Fatalf("repeater(DELAY=4) getDelay = %d, want 8", got)
	}
	mgr.SetBlock(pk.Position{X: 6, Y: 64, Z: 7}, block.ToStateID[block.RedstoneBlock{}], dimMinY) // South input

	loop.onRedstoneEdit(p)
	// Ticks 1..7: still unpowered.
	for i := 0; i < 7; i++ {
		loop.gametime++
		loop.tickScheduledBlocks()
		if block.RepeaterPowered(func() block.StateID { s, _ := mgr.GetBlock(p, dimMinY); return s }()) {
			t.Fatalf("repeater(DELAY=4) flipped at tick %d — must wait 8", i+1)
		}
	}
	// Tick 8: flips.
	loop.gametime++
	loop.tickScheduledBlocks()
	on, _ := mgr.GetBlock(p, dimMinY)
	if !block.RepeaterPowered(on) {
		t.Fatal("repeater(DELAY=4) should be POWERED at tick 8")
	}
}

// TestRepeaterLockedBySideRepeater: a powered repeater facing INTO the side of a target repeater locks
// it (RepeaterBlock.isLocked == getAlternateSignal > 0). A locked repeater's tick and checkTickOnNeighbor
// are no-ops, so it never flips. CITE: RepeaterBlock.isLocked / DiodeBlock.getAlternateSignal /
// getControlInputSignal (diodesOnly=true for a repeater).
func TestRepeaterLockedBySideRepeater(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	// Target repeater T at (4,64,4), FACING=South (input South, side faces East/West).
	tPos := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	target := block.ToStateID[block.Repeater{Delay: 1, Facing: block.South, Locked: false, Powered: false}]
	mgr.SetBlock(tPos, target, dimMinY)

	// A side (locking) repeater L to the WEST of T at (3,64,4). getAlternateSignal reads the two side
	// cells (clockwise/counter-clockwise of FACING=South = West and East) via getControlInputSignal with
	// diodesOnly=true: getControlInputSignal(West cell, West, true) == getDirectSignal(L, West), which is
	// L's ownSignal iff West == L.FACING. So the West-side locking repeater must have FACING=West for its
	// output to be seen by T's West-side query (jar-verified: DiodeBlock.getSignal FACING-only ==
	// DiodeBlock.getAlternateSignal via getControlInputSignal). A POWERED such diode locks T.
	lPos := pk.Position{X: 3, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 3, Y: 63, Z: 4}, stoneState(), dimMinY)
	lockRep := block.ToStateID[block.Repeater{Delay: 1, Facing: block.West, Locked: false, Powered: true}]
	mgr.SetBlock(lPos, lockRep, dimMinY)

	// Verify getAlternateSignal > 0 directly (the powered side repeater is seen as a side input of 15).
	if got := loop.diodeGetAlternateSignal(target, tPos); got <= 0 {
		t.Fatalf("target getAlternateSignal = %d, want > 0 (a powered side repeater locks it)", got)
	}
	if !loop.diodeIsLocked(target, tPos) {
		t.Fatal("target repeater should be LOCKED by the powered side repeater")
	}

	// Now feed T a real input on its South face. Because it is LOCKED, checkTickOnNeighbor is a no-op:
	// no tick is scheduled and it never turns on.
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY)
	loop.onRedstoneEdit(tPos)
	if loop.hasScheduledBlockTick(tPos, repeaterTickType) {
		t.Fatal("a LOCKED repeater must not schedule a flip tick")
	}
	// Even if we force a tick, the locked guard in repeaterTick prevents the flip.
	loop.gametime++
	loop.tickScheduledBlocks()
	if block.RepeaterPowered(func() block.StateID { s, _ := mgr.GetBlock(tPos, dimMinY); return s }()) {
		t.Fatal("a LOCKED repeater must stay unpowered")
	}
}

// TestComparatorCompareMode: in COMPARE mode the comparator outputs the input signal when input >=
// side, else 0. Verified via calculateOutputSignal directly across a few input/side combinations, then
// end-to-end through a scheduled tick. CITE: ComparatorBlock.calculateOutputSignal (compare returns
// input) / shouldTurnOn (input >= side in compare mode).
func TestComparatorCompareMode(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	// Comparator C at (4,64,4), FACING=South. Input from South (pos+FACING). Side inputs from East/West
	// (clockwise/counter-clockwise of South). Sturdy floor below.
	c := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)

	setComp := func(mode block.ComparatorMode) block.StateID {
		s := block.ToStateID[block.Comparator{Facing: block.South, Mode: mode, Powered: false}]
		mgr.SetBlock(c, s, dimMinY)
		return s
	}

	// Case 1: input 15 (redstone_block South), no side -> compare passes 15.
	comp := setComp(block.ComparatorModeCompare)
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY) // South input 15
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 15 {
		t.Fatalf("compare: input=15 side=0 output = %d, want 15", got)
	}

	// Case 2: add a WEAK side input of 9 via a wire on the West side (wire POWER 9). getControlInputSignal
	// (diodesOnly=false) reads a wire's POWER directly. input 15 >= side 9 -> compare passes 15.
	mgr.SetBlock(pk.Position{X: 3, Y: 63, Z: 4}, stoneState(), dimMinY)
	wire9, _ := block.RedstoneWireStateWith(9, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 4}, wire9, dimMinY) // West side
	if got := loop.diodeGetAlternateSignal(comp, c); got != 9 {
		t.Fatalf("comparator side input = %d, want 9 (west wire POWER)", got)
	}
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 15 {
		t.Fatalf("compare: input=15 side=9 output = %d, want 15 (input passes)", got)
	}

	// Case 3: raise the side ABOVE the input. Swap the South source to a wire of POWER 5 and keep side 9.
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, loop.airState(), dimMinY)
	wire5, _ := block.RedstoneWireStateWith(5, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, wire5, dimMinY) // South input now 5
	if got := loop.diodeGetInputSignal(comp, c); got != 5 {
		t.Fatalf("comparator input = %d, want 5", got)
	}
	// input 5 < side 9 -> compare output 0.
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 0 {
		t.Fatalf("compare: input=5 side=9 output = %d, want 0 (side exceeds input)", got)
	}

	// Case 4: input == side. Raise the South input wire to 9. compare passes (input >= side, equal).
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, wire9, dimMinY)
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 9 {
		t.Fatalf("compare: input=9 side=9 output = %d, want 9 (equal passes in compare)", got)
	}

	// End-to-end: with input 15 > side 9, drive a scheduled tick and assert POWERED + output out FACING.
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY) // South input 15
	comp = setComp(block.ComparatorModeCompare)
	loop.onRedstoneEdit(c)
	if !loop.hasScheduledBlockTick(c, comparatorTickType) {
		t.Fatal("comparator should schedule a tick when its output changes")
	}
	loop.gametime++
	loop.tickScheduledBlocks()
	loop.gametime++
	loop.tickScheduledBlocks()
	on, _ := mgr.GetBlock(c, dimMinY)
	if !block.ComparatorPowered(on) {
		t.Fatal("comparator should be POWERED (input 15 >= side 9 in compare mode)")
	}
	if got := loop.comparatorStoredOutput(c); got != 15 {
		t.Fatalf("comparator stored output = %d, want 15", got)
	}
	// Emits its output ONLY out FACING (South).
	if got := loop.diodeReadSignal(c, block.South); got != 15 {
		t.Fatalf("powered comparator getSignal(South) = %d, want 15", got)
	}
	if got := loop.diodeReadSignal(c, block.North); got != 0 {
		t.Fatalf("powered comparator getSignal(North) = %d, want 0 (emits only out FACING)", got)
	}
}

// TestComparatorSubtractMode: in SUBTRACT mode the comparator outputs max(input - side, 0). CITE:
// ComparatorBlock.calculateOutputSignal (subtract returns input - side).
func TestComparatorSubtractMode(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	c := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	comp := block.ToStateID[block.Comparator{Facing: block.South, Mode: block.ComparatorModeSubtract, Powered: false}]
	mgr.SetBlock(c, comp, dimMinY)

	// Input 15 from the South (redstone_block).
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	// Side input 9 from a West wire.
	mgr.SetBlock(pk.Position{X: 3, Y: 63, Z: 4}, stoneState(), dimMinY)
	wire9, _ := block.RedstoneWireStateWith(9, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 4}, wire9, dimMinY)

	// subtract: 15 - 9 == 6.
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 6 {
		t.Fatalf("subtract: input=15 side=9 output = %d, want 6", got)
	}

	// Raise the side ABOVE the input: side 15 (redstone_block West) beats input... but here keep input 15
	// and set side to a wire of POWER 15 -> 15 - 15 == 0. First, side > input short-circuits to 0 in
	// calculateOutputSignal (side > input -> 0); side == input -> 15 - 15 == 0. Both land on 0.
	wire15, _ := block.RedstoneWireStateWith(15, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 4}, wire15, dimMinY)
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 0 {
		t.Fatalf("subtract: input=15 side=15 output = %d, want 0 (max(input-side,0))", got)
	}

	// Lower the side below the input again (side 3) -> 15 - 3 == 12.
	wire3, _ := block.RedstoneWireStateWith(3, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 4}, wire3, dimMinY)
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 12 {
		t.Fatalf("subtract: input=15 side=3 output = %d, want 12", got)
	}

	// End-to-end: remove the side wire so the graph-recompute cannot clobber a hand-set POWER (an
	// unsourced wire recomputes to 0 under onRedstoneEdit). With side 0, subtract output == input 15,
	// which turns the comparator ON and stores 15 through the scheduled tick.
	mgr.SetBlock(pk.Position{X: 3, Y: 64, Z: 4}, loop.airState(), dimMinY)
	if got := loop.comparatorCalculateOutputSignal(comp, c); got != 15 {
		t.Fatalf("subtract: input=15 side=0 output = %d, want 15", got)
	}
	loop.onRedstoneEdit(c)
	loop.gametime++
	loop.tickScheduledBlocks()
	loop.gametime++
	loop.tickScheduledBlocks()
	if got := loop.comparatorStoredOutput(c); got != 15 {
		t.Fatalf("comparator (subtract) stored output = %d, want 15", got)
	}
	on, _ := mgr.GetBlock(c, dimMinY)
	if !block.ComparatorPowered(on) {
		t.Fatal("comparator should be POWERED with a positive subtract output")
	}
}

// TestComparatorUseTogglesMode: right-clicking a comparator cycles MODE compare<->subtract
// (ComparatorBlock.useWithoutItem -> state.cycle(MODE)). CITE: ComparatorBlock.useWithoutItem.
func TestComparatorUseTogglesMode(t *testing.T) {
	loop, mgr := newRedstoneLoop()
	c := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	comp := block.ToStateID[block.Comparator{Facing: block.South, Mode: block.ComparatorModeCompare, Powered: false}]
	mgr.SetBlock(c, comp, dimMinY)

	loop.useComparator(c, comp)
	after, _ := mgr.GetBlock(c, dimMinY)
	if m, _ := block.ComparatorGetMode(after); m != block.ComparatorModeSubtract {
		t.Fatalf("comparator MODE after one use = %v, want subtract", m)
	}
	loop.useComparator(c, after)
	after2, _ := mgr.GetBlock(c, dimMinY)
	if m, _ := block.ComparatorGetMode(after2); m != block.ComparatorModeCompare {
		t.Fatalf("comparator MODE after two uses = %v, want compare", m)
	}
}

// TestRepeaterUseCyclesDelay: right-clicking a repeater cycles DELAY 1->2->3->4->1
// (RepeaterBlock.useWithoutItem -> state.cycle(DELAY)). CITE: RepeaterBlock.useWithoutItem;
// BlockStateProperties.DELAY = IntegerProperty.create(1, 4).
func TestRepeaterUseCyclesDelay(t *testing.T) {
	loop, mgr := newRedstoneLoop()
	p := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	rep := block.ToStateID[block.Repeater{Delay: 1, Facing: block.South, Locked: false, Powered: false}]
	mgr.SetBlock(p, rep, dimMinY)

	want := []int{2, 3, 4, 1}
	cur := rep
	for i, w := range want {
		loop.useRepeater(p, cur)
		cur, _ = mgr.GetBlock(p, dimMinY)
		if got := block.RepeaterDelay(cur); got != w {
			t.Fatalf("repeater DELAY after %d uses = %d, want %d", i+1, got, w)
		}
	}
}
