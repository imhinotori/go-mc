package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// copper_bulb_test.go — GAP 2 validation: CopperBulbBlock.checkAndFlip is a redstone T-flip-flop. A
// RISING edge on POWERED toggles LIT; a HELD signal does not re-toggle; a falling edge only clears
// POWERED. The comparator reads LIT?15:0. Locked against the 26.2 jar (CopperBulbBlock.checkAndFlip /
// getAnalogOutputSignal).

func bulbState(mgr interface {
	GetBlock(pk.Position, int) (block.StateID, bool)
}, pos pk.Position) (lit, powered bool) {
	s, _ := mgr.GetBlock(pos, dimMinY)
	return block.BulbLit(s), block.BulbPowered(s)
}

// TestCopperBulbRisingEdgeTogglesLitOnce: a single rising edge flips LIT once and latches POWERED; a
// held signal (re-running the reaction while still powered) does NOT re-toggle. CITE:
// CopperBulbBlock.checkAndFlip (T-flip-flop).
func TestCopperBulbRisingEdgeTogglesLitOnce(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	y := 64
	bulbPos := pk.Position{X: 5, Y: y, Z: 8}
	// A fresh copper_bulb: LIT=false, POWERED=false.
	bulb := block.ToStateID[block.CopperBulb{Lit: false, Powered: false}]
	mgr.SetBlock(bulbPos, bulb, dimMinY)

	// Place a lever beside it (WEST) and turn it ON — a rising edge.
	leverPos := pk.Position{X: 4, Y: y, Z: 8}
	mgr.SetBlock(pk.Position{X: 4, Y: y - 1, Z: 8}, stoneState(), dimMinY)
	lever := block.ToStateID[block.Lever{Face: block.AttachFaceFloor, Facing: block.North, Powered: false}]
	mgr.SetBlock(leverPos, lever, dimMinY)
	loop.useLever(leverPos, lever)

	lit, powered := bulbState(mgr, bulbPos)
	if !lit || !powered {
		t.Fatalf("after rising edge: LIT=%v POWERED=%v, want both true", lit, powered)
	}

	// A held signal: re-run the reaction (as repeated neighbor updates would). LIT must NOT flip again.
	for i := 0; i < 5; i++ {
		loop.onRedstoneEdit(bulbPos)
	}
	lit, powered = bulbState(mgr, bulbPos)
	if !lit || !powered {
		t.Fatalf("held signal re-toggled the bulb: LIT=%v POWERED=%v, want both true", lit, powered)
	}

	// Falling edge (lever OFF): LIT stays, POWERED clears.
	leverOn, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOn)
	lit, powered = bulbState(mgr, bulbPos)
	if !lit || powered {
		t.Fatalf("after falling edge: LIT=%v POWERED=%v, want LIT true POWERED false", lit, powered)
	}

	// Second rising edge: LIT flips back off, POWERED latches true again.
	leverOff, _ := mgr.GetBlock(leverPos, dimMinY)
	loop.useLever(leverPos, leverOff)
	lit, powered = bulbState(mgr, bulbPos)
	if lit || !powered {
		t.Fatalf("after second rising edge: LIT=%v POWERED=%v, want LIT false POWERED true", lit, powered)
	}
}

// TestCopperBulbComparatorReadsLit: CopperBulbBlock.getAnalogOutputSignal == LIT ? 15 : 0. CITE:
// CopperBulbBlock.hasAnalogOutputSignal/getAnalogOutputSignal.
func TestCopperBulbComparatorReadsLit(t *testing.T) {
	loop, mgr := newRedstoneLoop()

	pos := pk.Position{X: 6, Y: 64, Z: 6}

	// Unlit bulb -> 0.
	mgr.SetBlock(pos, block.ToStateID[block.CopperBulb{Lit: false, Powered: false}], dimMinY)
	if sig, has := loop.copperBulbAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("unlit copper_bulb analog = (%d, %v), want (0, true)", sig, has)
	}

	// Lit bulb -> 15.
	mgr.SetBlock(pos, block.ToStateID[block.CopperBulb{Lit: true, Powered: false}], dimMinY)
	if sig, has := loop.copperBulbAnalogOutputSignal(pos); !has || sig != 15 {
		t.Fatalf("lit copper_bulb analog = (%d, %v), want (15, true)", sig, has)
	}

	// A non-bulb -> (0, false) so a comparator keeps its super value.
	mgr.SetBlock(pos, stoneState(), dimMinY)
	if sig, has := loop.copperBulbAnalogOutputSignal(pos); has || sig != 0 {
		t.Fatalf("non-bulb analog = (%d, %v), want (0, false)", sig, has)
	}
}
