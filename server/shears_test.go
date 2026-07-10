package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shears_test.go covers ShearsItem.useOn (shears.go): shearing a sub-max-age growing-plant head to
// MAX_AGE (25), and the guard branches (already max-age, non-shears item) that PASS.

// TestShearsSnapsCaveVinesToMaxAge: shears on age-0 cave_vines set it to age 25 (max) so it stops growing,
// preserving the berries property. CITE: ShearsItem.useOn -> GrowingPlantHeadBlock.getMaxAgeState.
func TestShearsSnapsCaveVinesToMaxAge(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Shears.ID, 1)

	youngID, ok := block.ToStateID[block.CaveVines{Age: 0, Berries: false}]
	if !ok {
		t.Fatalf("could not resolve cave_vines[age=0,berries=false]")
	}
	wantID, ok := block.ToStateID[block.CaveVines{Age: 25, Berries: false}]
	if !ok {
		t.Fatalf("could not resolve cave_vines[age=25,berries=false]")
	}
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, youngID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	got := mustGet(t, mgr, pos)
	if got != wantID {
		t.Fatalf("shears on cave_vines[0] gave %d, want cave_vines[25] (%d)", got, wantID)
	}
	if c := heldCount(p); c != 1 {
		t.Fatalf("shears count = %d, want 1 (durability, not shrink)", c)
	}
}

// TestShearsMaxAgeNoOp: shears on an already-max-age (25) cave_vines is a PASS (isMaxAge true), so the
// block is unchanged.
func TestShearsMaxAgeNoOp(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Shears.ID, 1)

	maxID, ok := block.ToStateID[block.CaveVines{Age: 25, Berries: false}]
	if !ok {
		t.Fatalf("could not resolve cave_vines[age=25]")
	}
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, maxID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != maxID {
		t.Fatalf("shears on max-age cave_vines changed it to %d, want unchanged (%d)", got, maxID)
	}
}

// TestShearsTwistingVines: shears on age-0 twisting_vines -> age 25.
func TestShearsTwistingVines(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Shears.ID, 1)

	youngID, ok := block.ToStateID[block.TwistingVines{Age: 0}]
	if !ok {
		t.Fatalf("could not resolve twisting_vines[age=0]")
	}
	wantID, ok := block.ToStateID[block.TwistingVines{Age: 25}]
	if !ok {
		t.Fatalf("could not resolve twisting_vines[age=25]")
	}
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, youngID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != wantID {
		t.Fatalf("shears on twisting_vines[0] gave %d, want twisting_vines[25] (%d)", got, wantID)
	}
}

// TestNonShearsOnCaveVinesNoOp: a stick on cave_vines does NOT snap it (item != shears -> PASS).
func TestNonShearsOnCaveVinesNoOp(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.Stick.ID, 1)

	youngID := block.ToStateID[block.CaveVines{Age: 0, Berries: false}]
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(pos, youngID, dimMinY)
	applyUseOn(loop, p, pos, 1)

	if got := mustGet(t, mgr, pos); got != youngID {
		t.Fatalf("a stick snapped cave_vines to %d, want unchanged (%d)", got, youngID)
	}
}
