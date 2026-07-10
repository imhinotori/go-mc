package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bell_redstone_test.go — GAP 3 validation: BellBlock.neighborChanged rings the bell on a redstone
// RISING edge and writes POWERED; a falling edge only writes POWERED. Locked against the 26.2 jar
// (BellBlock.neighborChanged / attemptToRing(level, pos, null)).

// TestBellRingsOnRisingEdge: a lever turned ON beside a bell sets the bell POWERED and rings it
// (BellBlockEntity.shaking becomes true); turning the lever OFF clears POWERED and does not re-ring.
func TestBellRingsOnRisingEdge(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	y := 64
	bellPos := pk.Position{X: 5, Y: y, Z: 8}
	// A FLOOR bell facing NORTH, initially unpowered.
	bell := block.ToStateID[block.Bell{Attachment: block.BellAttachTypeFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(bellPos, bell, dimMinY)

	// Lever beside it (WEST), on a stone mount.
	leverPos := pk.Position{X: 4, Y: y, Z: 8}
	mgr.SetBlock(pk.Position{X: 4, Y: y - 1, Z: 8}, stoneState(), dimMinY)
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(leverPos, lever, dimMinY)

	// Rising edge: turn the lever ON.
	loop.useLever(leverPos, lever)

	// POWERED latched true on the bell state.
	if !bellPowered(func() block.StateID { s, _ := mgr.GetBlock(bellPos, dimMinY); return s }()) {
		t.Fatal("bell did not become POWERED on the rising edge")
	}
	// The bell rang: its block-entity is shaking.
	b := loop.resolveBell(bellPos)
	if b == nil || !b.shaking {
		t.Fatalf("bell did not ring on the rising edge (shaking=%v)", b != nil && b.shaking)
	}

	// Reset the shake to observe that the falling edge does NOT re-ring.
	b.shaking = false

	// Falling edge: turn the lever OFF.
	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)

	if bellPowered(func() block.StateID { s, _ := mgr.GetBlock(bellPos, dimMinY); return s }()) {
		t.Fatal("bell stayed POWERED after the lever was turned off")
	}
	if b.shaking {
		t.Fatal("bell rang on the falling edge (should only ring on a rising edge)")
	}
}
