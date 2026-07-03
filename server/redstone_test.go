package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// redstone_test.go — CORE REDSTONE validation gates. Each asserts the ported signal values / tick
// delays against the unobfuscated 26.2 jar behaviour:
//   - a lever powering an adjacent wire spreads 15 -> 0 over distance (max-neighbor-minus-1 decrement);
//   - a redstone torch inverts its attachment's signal (lit when unpowered, unlit when powered) on the
//     2-tick scheduled toggle;
//   - a redstone_block is a constant-15 source;
//   - a button powers, then unpresses after its jar-verified delay (20 stone / 30 wood).

// newRedstoneLoop wires a TickLoop with one ready all-air chunk and a registered block-tick
// container for column (0,0), so scheduled ticks (torch toggle, button unpress) route into a live
// container. Mirrors newSugarCaneLoop.
func newRedstoneLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

func wirePower(mgr *world.ChunkManager, pos pk.Position) int {
	s, _ := mgr.GetBlock(pos, dimMinY)
	return block.RedstoneWirePower(s)
}

// TestRedstoneBlockIsConstant15 locks PoweredBlock: isSignalSource + ownSignal == 15 out every face,
// and getDirectSignal == 0 (a block source is weak-only). CITE: PoweredBlock.isSignalSource/ownSignal.
func TestRedstoneBlockIsConstant15(t *testing.T) {
	loop, mgr := newRedstoneLoop()
	pos := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(pos, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	for _, d := range redstoneDirs {
		if got := loop.stateGetSignal(loop.redstoneBlockAt(pos), pos, d); got != 15 {
			t.Fatalf("redstone_block getSignal(%v) = %d, want 15", d, got)
		}
		if got := loop.stateGetDirectSignal(loop.redstoneBlockAt(pos), pos, d); got != 0 {
			t.Fatalf("redstone_block getDirectSignal(%v) = %d, want 0", d, got)
		}
	}
	// A wire adjacent to a redstone_block reads block signal 15.
	wirePos := pk.Position{X: 5, Y: 64, Z: 4}
	mgr.SetBlock(pk.Position{X: 5, Y: 63, Z: 4}, stoneState(), dimMinY) // wire needs a sturdy floor
	wire0, _ := block.RedstoneWireStateWith(0, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(wirePos, wire0, dimMinY)
	if bs := loop.getBlockSignal(wirePos); bs != 15 {
		t.Fatalf("wire next to redstone_block getBlockSignal = %d, want 15", bs)
	}
}

// TestLeverPowersWireSpread15to0 is the headline wire-decrement gate: a lever (floor, so it powers
// UP, but its weak getSignal is 15 on ALL faces) placed next to a straight wire run drives the run
// 15,14,13,... one less per step (getIncomingWireSignal == max neighbor wire - 1). CITE:
// RedstoneWireEvaluator.getIncomingWireSignal + DefaultRedstoneWireEvaluator.calculateTargetStrength.
func TestLeverPowersWireSpread15to0(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	// A stone floor along y=63 so each wire canSurvive; the wire run + lever along y=64. The single
	// ready chunk covers x=0..15, so the run stays inside it (x=16 would read an unloaded column ->
	// air below -> the wire would not survive, which is a chunk-boundary artifact, not the spread).
	y := 64
	floorY := 63
	for x := 0; x <= 15; x++ {
		mgr.SetBlock(pk.Position{X: x, Y: floorY, Z: 8}, stoneState(), dimMinY)
	}

	// Lever at x=0 (unpowered initially), wire run x=1..15.
	leverPos := pk.Position{X: 0, Y: y, Z: 8}
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(leverPos, lever, dimMinY)

	wire0, _ := block.RedstoneWireStateWith(0, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	for x := 1; x <= 15; x++ {
		mgr.SetBlock(pk.Position{X: x, Y: y, Z: 8}, wire0, dimMinY)
	}

	// Toggle the lever ON (LeverBlock.pull) — this recomputes the whole run to its fixpoint.
	loop.useLever(leverPos, lever)

	if !block.LeverPowered(func() block.StateID { s, _ := mgr.GetBlock(leverPos, dimMinY); return s }()) {
		t.Fatal("lever did not become POWERED")
	}

	// Expected: wire[1]=15 (adjacent to the 15-source lever's block signal), then -1 per step.
	want := []int{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	for i, w := range want {
		x := i + 1
		got := wirePower(mgr, pk.Position{X: x, Y: y, Z: 8})
		if got != w {
			t.Fatalf("wire at x=%d POWER = %d, want %d", x, got, w)
		}
	}

	// Toggle the lever OFF — the whole run drops back to 0.
	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)
	for x := 1; x <= 15; x++ {
		if got := wirePower(mgr, pk.Position{X: x, Y: y, Z: 8}); got != 0 {
			t.Fatalf("after lever OFF, wire x=%d POWER = %d, want 0", x, got)
		}
	}
}

// TestRedstoneTorchInvertsAttachmentSignal: a standing redstone torch on a block reads that block's
// signal (hasSignal(below, DOWN)). Lit when the attachment is UNpowered; when the attachment becomes
// powered, neighborChanged schedules a 2-tick toggle that flips it to unlit (and back when the power
// is removed). CITE: RedstoneTorchBlock.tick/neighborChanged/hasNeighborSignal, TOGGLE_DELAY=2.
func TestRedstoneTorchInvertsAttachmentSignal(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	// Attachment block at (4,64,4); torch on top at (4,65,4). The attachment sits on stone below.
	attach := pk.Position{X: 4, Y: 64, Z: 4}
	torchPos := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	mgr.SetBlock(attach, stoneState(), dimMinY)
	torchLit := block.ToStateID[block.RedstoneTorch{Lit: true}]
	mgr.SetBlock(torchPos, torchLit, dimMinY)

	// Initially the attachment (plain stone) carries no signal, so the torch stays lit and emits 15
	// out its sides (NORTH here) and 0 up.
	if loop.torchAttachmentHasSignal(torchPos, torchLit) {
		t.Fatal("precondition: plain stone attachment should carry no signal")
	}
	if got := loop.stateGetSignal(torchLit, torchPos, block.North); got != 15 {
		t.Fatalf("lit torch getSignal(NORTH) = %d, want 15", got)
	}
	if got := loop.stateGetSignal(torchLit, torchPos, block.Up); got != 0 {
		t.Fatalf("torch getSignal(UP) = %d, want 0 (never emits up)", got)
	}

	// Strong-power the attachment so hasSignal(attach, DOWN) is true. Only a DIRECT (strong) source
	// charges a solid block's pass-through — a redstone_block is weak-only (getDirectSignal==0), so it
	// would NOT power the attachment (vanilla-correct). A lever DOES emit strong signal in its
	// getConnectedDirection: a WALL lever east of the attachment, facing EAST, has connectedDirection
	// EAST, so getDirectSignalTo(attach) queries the lever with direction EAST and gets 15 — the
	// attachment (a solid conductor) then re-emits that strong signal, and the torch below sees it.
	source := pk.Position{X: 5, Y: 64, Z: 4} // east of the attachment
	mgr.SetBlock(source, block.ToStateID[block.Lever{Face: block.AttachFaceWall, Facing: block.East, Powered: true}], dimMinY)

	// Now the attachment (solid) receives direct signal 15 from the adjacent lever, so the torch's
	// hasNeighborSignal (hasSignal(below, DOWN)) is true.
	if !loop.torchAttachmentHasSignal(torchPos, torchLit) {
		t.Fatal("attachment strong-powered by an adjacent lever should carry signal (torch sees it)")
	}

	// neighborChanged: LIT(true) == hasNeighborSignal(true) -> schedule a 2-tick toggle.
	loop.torchNeighborChanged(torchPos, torchLit)
	if !loop.hasScheduledBlockTick(torchPos, redstoneTorchTickType) {
		t.Fatal("torch should schedule a toggle tick when its attachment becomes powered")
	}

	// Advancing ONE tick must NOT fire it yet (delay is 2).
	loop.gametime++
	loop.tickScheduledBlocks()
	if s, _ := mgr.GetBlock(torchPos, dimMinY); !block.RedstoneTorchLit(s) {
		t.Fatal("torch flipped after only 1 tick — TOGGLE_DELAY must be 2")
	}

	// The second tick fires the toggle: the torch unlights.
	loop.gametime++
	loop.tickScheduledBlocks()
	s, _ := mgr.GetBlock(torchPos, dimMinY)
	if block.RedstoneTorchLit(s) {
		t.Fatal("torch should be UNLIT after its 2-tick toggle fired (attachment powered)")
	}
	if got := loop.stateGetSignal(s, torchPos, block.North); got != 0 {
		t.Fatalf("unlit torch getSignal(NORTH) = %d, want 0", got)
	}

	// Remove the source: attachment loses signal, neighborChanged reschedules the toggle, and after
	// 2 ticks the torch relights (the inverter output goes back high).
	mgr.SetBlock(source, loop.airState(), dimMinY)
	unlit, _ := mgr.GetBlock(torchPos, dimMinY)
	loop.torchNeighborChanged(torchPos, unlit)
	if !loop.hasScheduledBlockTick(torchPos, redstoneTorchTickType) {
		t.Fatal("torch should reschedule a toggle when its attachment loses power")
	}
	loop.gametime++
	loop.tickScheduledBlocks()
	loop.gametime++
	loop.tickScheduledBlocks()
	relit, _ := mgr.GetBlock(torchPos, dimMinY)
	if !block.RedstoneTorchLit(relit) {
		t.Fatal("torch should RELIGHT after the attachment loses power (2-tick toggle)")
	}
}

// TestStoneButtonPressUnpressAfter20Ticks: pressing a stone button sets POWERED and schedules the
// unpress 20 ticks out (BlockSetType.STONE ticksToStayPressed == 20). The button stays powered until
// exactly tick 20, then unpresses. CITE: ButtonBlock.press/tick, Blocks.<clinit> bipush 20.
func TestStoneButtonPressUnpressAfter20Ticks(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY) // sturdy floor
	buttonPos := pk.Position{X: 4, Y: 64, Z: 4}
	button := block.ToStateID[block.StoneButton{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(buttonPos, button, dimMinY)

	// Press it.
	loop.pressButton(buttonPos, button)
	pressed, _ := mgr.GetBlock(buttonPos, dimMinY)
	if !block.ButtonPowered(pressed) {
		t.Fatal("button should be POWERED after press")
	}
	if !loop.hasScheduledBlockTick(buttonPos, blockTickType(block.StateList[pressed].ID())) {
		t.Fatal("press should schedule the unpress tick")
	}

	// A pressed button emits 15 (weak, all faces) and 15 direct into its connected direction (UP for
	// a floor button).
	if got := loop.stateGetSignal(pressed, buttonPos, block.North); got != 15 {
		t.Fatalf("pressed button getSignal(NORTH) = %d, want 15", got)
	}
	if got := loop.stateGetDirectSignal(pressed, buttonPos, block.Up); got != 15 {
		t.Fatalf("pressed button getDirectSignal(UP) = %d, want 15", got)
	}

	// Drain 19 ticks: still pressed.
	for i := 0; i < 19; i++ {
		loop.gametime++
		loop.tickScheduledBlocks()
	}
	if s, _ := mgr.GetBlock(buttonPos, dimMinY); !block.ButtonPowered(s) {
		t.Fatal("button should still be pressed at tick 19 (unpress is at tick 20)")
	}

	// Tick 20: the unpress fires.
	loop.gametime++
	loop.tickScheduledBlocks()
	if s, _ := mgr.GetBlock(buttonPos, dimMinY); block.ButtonPowered(s) {
		t.Fatal("button should UNPRESS at tick 20 (ticksToStayPressed == 20 for stone)")
	}
}

// TestWoodenButtonStayPressed30 locks the wood/stone delay difference: an oak button unpresses at 30
// ticks, not 20. CITE: Blocks.<clinit> bipush 30 for wooden buttons.
func TestWoodenButtonStayPressed30(t *testing.T) {
	loop, mgr := newRedstoneLoop()
	mgr.SetBlock(pk.Position{X: 4, Y: 63, Z: 4}, stoneState(), dimMinY)
	buttonPos := pk.Position{X: 4, Y: 64, Z: 4}
	button := block.ToStateID[block.OakButton{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	if got := block.ButtonStayPressedTicks(button); got != 30 {
		t.Fatalf("oak button ticksToStayPressed = %d, want 30", got)
	}
	mgr.SetBlock(buttonPos, button, dimMinY)
	loop.pressButton(buttonPos, button)

	for i := 0; i < 29; i++ {
		loop.gametime++
		loop.tickScheduledBlocks()
	}
	if s, _ := mgr.GetBlock(buttonPos, dimMinY); !block.ButtonPowered(s) {
		t.Fatal("oak button should still be pressed at tick 29")
	}
	loop.gametime++
	loop.tickScheduledBlocks()
	if s, _ := mgr.GetBlock(buttonPos, dimMinY); block.ButtonPowered(s) {
		t.Fatal("oak button should unpress at tick 30")
	}
}

// TestWireStepUpAndDownConnections locks getIncomingWireSignal's vertical step: a wire can carry
// signal up over a solid block (when the block above the source is not solid) and down under a
// non-solid block. Here we build a 2-level staircase and assert the power steps across it. CITE:
// RedstoneWireEvaluator.getIncomingWireSignal (up/down step branches).
func TestWireStepDownConnection(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	// Layout (side view, all along z=8):
	//   x=0: redstone_block source at y=64.
	//   x=1: wire at y=64 (adjacent to source -> 15).
	//   x=2: air at y=64, wire at y=63 (steps DOWN under the air) on stone floor y=62.
	// The wire at x=1 sees the source; wire at x=2 (one level down) picks it up via the step-down
	// branch (neighbor x=2,y=64 is air/non-conductor -> read wire at x=2,y=63).
	mgr.SetBlock(pk.Position{X: 0, Y: 63, Z: 8}, stoneState(), dimMinY)
	mgr.SetBlock(pk.Position{X: 0, Y: 64, Z: 8}, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	mgr.SetBlock(pk.Position{X: 1, Y: 63, Z: 8}, stoneState(), dimMinY)
	wire0, _ := block.RedstoneWireStateWith(0, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone, block.RedstoneSideNone)
	mgr.SetBlock(pk.Position{X: 1, Y: 64, Z: 8}, wire0, dimMinY)

	mgr.SetBlock(pk.Position{X: 2, Y: 62, Z: 8}, stoneState(), dimMinY)
	mgr.SetBlock(pk.Position{X: 2, Y: 63, Z: 8}, wire0, dimMinY)
	// x=2,y=64 stays air so the step-down applies from x=1's perspective going to x=2.

	// Wake the graph from the source cell.
	loop.onRedstoneEdit(pk.Position{X: 0, Y: 64, Z: 8})

	if got := wirePower(mgr, pk.Position{X: 1, Y: 64, Z: 8}); got != 15 {
		t.Fatalf("wire adjacent to source POWER = %d, want 15", got)
	}
	if got := wirePower(mgr, pk.Position{X: 2, Y: 63, Z: 8}); got != 14 {
		t.Fatalf("stepped-down wire POWER = %d, want 14", got)
	}
}
