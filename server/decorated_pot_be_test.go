package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// decorated_pot_be_test.go -- validation gates for the DECORATED_POT block-entity (decorated_pot_be.go)
// against the 26.2 jar: an empty pot accepts one item (setTheItem); a same-item click grows the stored
// stack by 1; a full/mismatched click is rejected (PASS); the comparator reads the single-slot fill.

// newDecoratedPotTest wires a dispenser-style loop (one ready air chunk) with a decorated_pot at pos and a
// player holding `held`, returns the loop, pos, and player.
func newDecoratedPotTest(t *testing.T, held component.SlotData) (*TickLoop, pk.Position, *tickPlayer) {
	t.Helper()
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 4, Y: 64, Z: 4}
	pot := block.ToStateID[block.DecoratedPot{Facing: block.North}]
	mgr.SetBlock(pos, pot, dimMinY)
	loop.resolveDecoratedPot(pos)

	p := &tickPlayer{x: 4, y: 64, z: 4, entityID: 1, gameMode: gameModeSurvival}
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), held)
	return loop, pos, p
}

// TestDecoratedPotInsertsOneItem locks DecoratedPotBlock.useItemOn on an EMPTY pot: a right-click with a
// stack sets the pot item to a copy of count 1 and shrinks the held stack by 1. CITE DecoratedPotBlock.useItemOn.
func TestDecoratedPotInsertsOneItem(t *testing.T) {
	held := component.SlotData{Count: 5, ItemID: pk.VarInt(item.Emerald.ID)}
	loop, pos, p := newDecoratedPotTest(t, held)
	pot, _ := loop.only().world.GetBlock(pos, dimMinY)

	if ok := loop.useDecoratedPot(p, pos, pot); !ok {
		t.Fatal("useDecoratedPot returned false on an empty pot with a valid held item (want SUCCESS)")
	}
	d := loop.resolveDecoratedPot(pos)
	if stackEmpty(d.item) || int32(d.item.ItemID) != int32(item.Emerald.ID) || d.item.Count != 1 {
		t.Fatalf("pot item = %+v after insert, want 1 emerald", d.item)
	}
	// Held shrank by 1 (survival).
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)).Count; got != 4 {
		t.Fatalf("held count = %d after insert, want 4 (consumeAndReturn(1))", got)
	}
}

// TestDecoratedPotGrowsSameItem locks the same-item grow branch: a second right-click with the SAME item
// grows the stored stack to 2 (existing.grow(1)). CITE DecoratedPotBlock.useItemOn (grow branch).
func TestDecoratedPotGrowsSameItem(t *testing.T) {
	held := component.SlotData{Count: 5, ItemID: pk.VarInt(item.Emerald.ID)}
	loop, pos, p := newDecoratedPotTest(t, held)
	pot, _ := loop.only().world.GetBlock(pos, dimMinY)

	loop.useDecoratedPot(p, pos, pot) // 1st -> count 1
	loop.useDecoratedPot(p, pos, pot) // 2nd -> grow to 2
	d := loop.resolveDecoratedPot(pos)
	if d.item.Count != 2 {
		t.Fatalf("pot item count = %d after two inserts, want 2 (grow)", d.item.Count)
	}
}

// TestDecoratedPotRejectsMismatch locks the mismatched-hand PASS: clicking a pot holding emeralds with a
// DIFFERENT item does not insert and returns false (placement continues). CITE DecoratedPotBlock.useItemOn.
func TestDecoratedPotRejectsMismatch(t *testing.T) {
	held := component.SlotData{Count: 5, ItemID: pk.VarInt(item.Emerald.ID)}
	loop, pos, p := newDecoratedPotTest(t, held)
	pot, _ := loop.only().world.GetBlock(pos, dimMinY)
	loop.useDecoratedPot(p, pos, pot) // pot now holds 1 emerald

	// Swap the held item to a diamond.
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 3, ItemID: pk.VarInt(item.Diamond.ID)})
	if ok := loop.useDecoratedPot(p, pos, pot); ok {
		t.Fatal("useDecoratedPot inserted a mismatched item (want PASS -> false)")
	}
	d := loop.resolveDecoratedPot(pos)
	if int32(d.item.ItemID) != int32(item.Emerald.ID) || d.item.Count != 1 {
		t.Fatalf("pot item = %+v after mismatched click, want unchanged 1 emerald", d.item)
	}
}

// TestDecoratedPotComparator locks DecoratedPotBlock.getAnalogOutputSignal: an empty pot reads 0; a pot
// with a full 64-stack of a maxStackSize-64 item reads 15. CITE getRedstoneSignalFromContainer (single slot).
func TestDecoratedPotComparator(t *testing.T) {
	loop, pos, _ := newDecoratedPotTest(t, component.SlotData{Count: 0})
	if sig, has := loop.decoratedPotAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("empty pot comparator = (%d, %v), want (0, true)", sig, has)
	}
	d := loop.resolveDecoratedPot(pos)
	d.item = component.SlotData{Count: 64, ItemID: pk.VarInt(item.Cobblestone.ID)}
	if sig, _ := loop.decoratedPotAnalogOutputSignal(pos); sig != 15 {
		t.Fatalf("full pot comparator = %d, want 15", sig)
	}
}
