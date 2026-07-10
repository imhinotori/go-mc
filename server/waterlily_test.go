package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// waterlily_test.go covers PlaceOnWaterBlockItem.use (waterlily.go): a lily_pad right-clicked in air over
// a water SOURCE places on the cell ABOVE the source and consumes 1 (survival); creative keeps the item;
// a lily_pad aimed at no source is a no-op.

// TestWaterlilyPlacesOnWaterSurface: a survival player holding a lily_pad, aiming down at a water source
// with air above, places a lily_pad on the cell above the source and the stack shrinks by 1.
func TestWaterlilyPlacesOnWaterSurface(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	// Water source at (1,64,1); air above it (1,65,1) is where the lily pad lands. The SOURCE_ONLY ray
	// marches down through air and stops at the source cell (1,64,1); place at above == (1,65,1).
	src := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(src, waterStateID(0), dimMinY)

	setHeldItem(p, item.LilyPad.ID, 3)
	loop.useItemInHand(p, interactionHandMain)

	place := pk.Position{X: 1, Y: 65, Z: 1}
	got := mustGet(t, mgr, place)
	if want := block.DefaultStateID["minecraft:lily_pad"]; got != want {
		t.Fatalf("after lily use: cell %v = state %d, want lily_pad (%d)", place, got, want)
	}
	if c := heldCount(p); c != 2 {
		t.Fatalf("lily_pad count = %d, want 2 (shrink by 1)", c)
	}
}

// TestWaterlilyCreativeKeepsItem: in creative the lily_pad places but the stack is not consumed.
func TestWaterlilyCreativeKeepsItem(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeCreative
	aimDown(p)

	src := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(src, waterStateID(0), dimMinY)

	setHeldItem(p, item.LilyPad.ID, 3)
	loop.useItemInHand(p, interactionHandMain)

	place := pk.Position{X: 1, Y: 65, Z: 1}
	if got := mustGet(t, mgr, place); got != block.DefaultStateID["minecraft:lily_pad"] {
		t.Fatalf("creative lily use did not place a lily_pad at %v (state %d)", place, got)
	}
	if c := heldCount(p); c != 3 {
		t.Fatalf("creative lily_pad count = %d, want 3 (not consumed)", c)
	}
}

// TestWaterlilyNoSourceNoOp: a lily_pad aimed at a non-source cell (no water) places nothing and is not
// consumed (the SOURCE_ONLY raycast misses).
func TestWaterlilyNoSourceNoOp(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	aimDown(p)

	// Stone floor (not a fluid source) at (1,64,1); the SOURCE_ONLY ray stops on no source before the
	// solid, and the useOn is a PASS no-op (the lily consumes the use but places nothing).
	floor := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(floor, block.ToStateID[block.Stone{}], dimMinY)

	setHeldItem(p, item.LilyPad.ID, 3)
	loop.useItemInHand(p, interactionHandMain)

	place := pk.Position{X: 1, Y: 65, Z: 1}
	if got, _ := mgr.GetBlock(place, dimMinY); !block.IsAir(got) {
		t.Fatalf("lily placed over a non-source floor at %v (state %d), want no placement", place, got)
	}
	if c := heldCount(p); c != 3 {
		t.Fatalf("lily_pad count = %d after a no-op, want 3 (not consumed)", c)
	}
}
