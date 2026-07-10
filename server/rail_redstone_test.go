package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// rail_redstone_test.go — GAP 1 validation: PoweredRailBlock.updateState drives the POWERED bit of a
// powered_rail / activator_rail from a direct neighbor signal AND from a same-orientation powered-rail
// run up to 8 cells. Locked against the 26.2 jar (PoweredRailBlock.updateState /
// findPoweredRailSignal / isSameRailWithPower).

func railPowered(mgr interface {
	GetBlock(pk.Position, int) (block.StateID, bool)
}, pos pk.Position) bool {
	s, _ := mgr.GetBlock(pos, dimMinY)
	p, _ := block.RailPowered(s)
	return p
}

// TestPoweredRailPoweredByAdjacentSource: a redstone source (a lever) directly beside a powered rail
// flips its POWERED true; removing the source flips it back false. CITE: PoweredRailBlock.updateState
// (hasNeighborSignal branch).
func TestPoweredRailPoweredByAdjacentSource(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	y := 64
	// Stone floor so the rail's context is a normal world (updateState reads neighbors only).
	railPos := pk.Position{X: 5, Y: y, Z: 8}
	mgr.SetBlock(pk.Position{X: 5, Y: y - 1, Z: 8}, stoneState(), dimMinY)

	// A NORTH_SOUTH powered rail, initially unpowered.
	rail := block.ToStateID[block.PoweredRail{Shape: block.RailShapeNorthSouth, Powered: false}]
	mgr.SetBlock(railPos, rail, dimMinY)

	// Place a lever (a constant 15 source out all weak faces) directly WEST of the rail, on the stone
	// beside it. Lever floor mount at x=4.
	leverPos := pk.Position{X: 4, Y: y, Z: 8}
	mgr.SetBlock(pk.Position{X: 4, Y: y - 1, Z: 8}, stoneState(), dimMinY)
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(leverPos, lever, dimMinY)

	// Toggle the lever ON — this wakes the rail (updateNeighborsAt) and updateState powers it.
	loop.useLever(leverPos, lever)
	if !railPowered(mgr, railPos) {
		t.Fatal("powered rail beside a lit lever did not become POWERED")
	}

	// Toggle the lever OFF — the rail drops back to unpowered.
	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)
	if railPowered(mgr, railPos) {
		t.Fatal("powered rail stayed POWERED after the lever was turned off")
	}
}

// TestPoweredRailRunPropagates8: an 8-long straight powered-rail run with a source at one end powers
// every rail in the run (each rail is powered by the run, not a direct neighbor). CITE:
// findPoweredRailSignal / isSameRailWithPower (distance up to 8).
func TestPoweredRailRunPropagates8(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	y := 64
	z := 8
	// Stone floor under the whole run + the lever.
	for x := 0; x <= 9; x++ {
		mgr.SetBlock(pk.Position{X: x, Y: y - 1, Z: z}, stoneState(), dimMinY)
	}

	// Lever at x=0. Powered rails x=1..9 (a run of 9; the rail at x=1 is directly powered, the run then
	// carries power to distance 8 — rails x=2..9). All EAST_WEST so the run follows the x axis.
	leverPos := pk.Position{X: 0, Y: y, Z: z}
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(leverPos, lever, dimMinY)

	rail := block.ToStateID[block.PoweredRail{Shape: block.RailShapeEastWest, Powered: false}]
	for x := 1; x <= 9; x++ {
		mgr.SetBlock(pk.Position{X: x, Y: y, Z: z}, rail, dimMinY)
	}

	// Turn the lever ON. Then drive updateState across the whole run: the lever's neighbor update powers
	// x=1 directly; each powered rail's updateState (dispatched through the worklist as its POWERED write
	// enqueues pos.below and re-notifies) propagates along the run. To be robust to iteration order we
	// run one redstone edit at each rail cell (BaseRailBlock.onPlace/neighborChanged equivalent), exactly
	// as vanilla re-runs updateState as neighbors settle.
	loop.useLever(leverPos, lever)
	for pass := 0; pass < 9; pass++ {
		for x := 1; x <= 9; x++ {
			loop.onRedstoneEdit(pk.Position{X: x, Y: y, Z: z})
		}
	}

	// The rail directly adjacent to the lever (x=1) plus the run out to distance 8 (x=2..9) are all
	// powered. isSameRailWithPower walks up to 8 steps from a rail; x=9 is 8 cells from x=1.
	for x := 1; x <= 9; x++ {
		if !railPowered(mgr, pk.Position{X: x, Y: y, Z: z}) {
			t.Fatalf("powered rail at x=%d not POWERED in the run", x)
		}
	}

	// Turn the lever OFF and re-settle: the whole run drops to unpowered.
	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)
	for pass := 0; pass < 9; pass++ {
		for x := 1; x <= 9; x++ {
			loop.onRedstoneEdit(pk.Position{X: x, Y: y, Z: z})
		}
	}
	for x := 1; x <= 9; x++ {
		if railPowered(mgr, pk.Position{X: x, Y: y, Z: z}) {
			t.Fatalf("powered rail at x=%d still POWERED after source removed", x)
		}
	}
}

// TestActivatorRailPoweredByAdjacentSource: activator_rail is `new PoweredRailBlock(...)`, so it uses
// the identical updateState. A lit lever beside it powers it. CITE: ActivatorRailBlock ==
// new PoweredRailBlock(...).
func TestActivatorRailPoweredByAdjacentSource(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	y := 64
	railPos := pk.Position{X: 5, Y: y, Z: 8}
	mgr.SetBlock(pk.Position{X: 5, Y: y - 1, Z: 8}, stoneState(), dimMinY)
	rail := block.ToStateID[block.ActivatorRail{Shape: block.RailShapeNorthSouth, Powered: false}]
	mgr.SetBlock(railPos, rail, dimMinY)

	leverPos := pk.Position{X: 4, Y: y, Z: 8}
	mgr.SetBlock(pk.Position{X: 4, Y: y - 1, Z: 8}, stoneState(), dimMinY)
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(leverPos, lever, dimMinY)

	loop.useLever(leverPos, lever)
	if !railPowered(mgr, railPos) {
		t.Fatal("activator rail beside a lit lever did not become POWERED")
	}

	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)
	if railPowered(mgr, railPos) {
		t.Fatal("activator rail stayed POWERED after the lever was turned off")
	}
}
