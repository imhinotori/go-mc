package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// axe_test.go covers AxeItem.useOn (axe.go): stripping a log to its stripped variant preserving AXIS,
// de-oxidizing copper one tier, and the guard branches (non-axe item, non-strippable block) that PASS.

// TestAxeStripsOakLogPreservingAxis: an axe on an oak_log[axis=x] becomes stripped_oak_log[axis=x]
// (AXIS preserved via withPropertiesOf). CITE: AxeItem.getStripped.
func TestAxeStripsOakLogPreservingAxis(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.IronAxe.ID, 1)

	logID, ok := block.ToStateID[block.OakLog{Axis: block.X}]
	if !ok {
		t.Fatalf("could not resolve oak_log[axis=x]")
	}
	wantID, ok := block.ToStateID[block.StrippedOakLog{Axis: block.X}]
	if !ok {
		t.Fatalf("could not resolve stripped_oak_log[axis=x]")
	}

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, logID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	got := mustGet(t, mgr, pos)
	if got != wantID {
		t.Fatalf("axe on oak_log[x] gave state %d, want stripped_oak_log[x] (%d)", got, wantID)
	}
	if c := heldCount(p); c != 1 {
		t.Fatalf("axe count = %d, want 1 (durability, not shrink)", c)
	}
}

// TestAxeStripsWoodAxisZ: oak_wood[axis=z] -> stripped_oak_wood[axis=z].
func TestAxeStripsWoodAxisZ(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.DiamondAxe.ID, 1)

	woodID, ok := block.ToStateID[block.OakWood{Axis: block.Z}]
	if !ok {
		t.Fatalf("could not resolve oak_wood[axis=z]")
	}
	wantID, ok := block.ToStateID[block.StrippedOakWood{Axis: block.Z}]
	if !ok {
		t.Fatalf("could not resolve stripped_oak_wood[axis=z]")
	}
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, woodID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != wantID {
		t.Fatalf("axe on oak_wood[z] gave %d, want stripped_oak_wood[z] (%d)", got, wantID)
	}
}

// TestAxeDeoxidizesCopper: an axe on oxidized_copper scrapes it to weathered_copper (one tier down).
// CITE: AxeItem.evaluateNewBlockState -> WeatheringCopper.getPrevious.
func TestAxeDeoxidizesCopper(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.StoneAxe.ID, 1)

	oxID, ok := block.DefaultStateID["minecraft:oxidized_copper"]
	if !ok {
		t.Fatalf("could not resolve oxidized_copper")
	}
	wantID, ok := block.DefaultStateID["minecraft:weathered_copper"]
	if !ok {
		t.Fatalf("could not resolve weathered_copper")
	}
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, oxID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != wantID {
		t.Fatalf("axe on oxidized_copper gave %d, want weathered_copper (%d)", got, wantID)
	}
}

// TestAxeOnNonStrippableFallsThrough: an axe on stone yields no transform (evaluateNewBlockState empty),
// so the block is unchanged.
func TestAxeOnNonStrippableFallsThrough(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.IronAxe.ID, 1)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	stone := block.ToStateID[block.Stone{}]
	mgr.SetBlock(pos, stone, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != stone {
		t.Fatalf("axe on stone changed it to %d, want unchanged stone (%d)", got, stone)
	}
}

// TestNonAxeDoesNotStrip: a stick on an oak_log does NOT strip it (isAxeItem false -> PASS).
func TestNonAxeDoesNotStrip(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Stick.ID, 1)

	logID := block.ToStateID[block.OakLog{Axis: block.Y}]
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, logID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != logID {
		t.Fatalf("a stick stripped the oak_log to %d, want unchanged (%d)", got, logID)
	}
}
