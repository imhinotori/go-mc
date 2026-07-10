package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chiseled_bookshelf_test.go -- behaviour gates for the CHISELED BOOKSHELF port (BOOKSHELF-01), ported
// 1:1 from the 26.2 jar (temp/cache/26.2-inner.jar): the 6-slot container, occupancy-boolean sync,
// lastInteractedSlot tracking, comparator == lastInteractedSlot+1, the #bookshelf_books tag filter, the
// hit-vec -> slot geometry, and the {Items, last_interacted_slot} NBT round-trip.

// bookBook / enchantedBook are convenience book stacks used across the tests.
func bookBook() component.SlotData {
	return component.SlotData{ItemID: toItemID(int(itemNameToID("book"))), Count: 1}
}
func enchantedBook() component.SlotData {
	return component.SlotData{ItemID: toItemID(int(itemNameToID("enchanted_book"))), Count: 1}
}

// TestChiseledBookshelfAddRemovePerSlot asserts setItem/removeItem drive the SLOT_N_OCCUPIED booleans and
// that a slot round-trips add -> occupied -> remove -> empty for every slot 0..5.
func TestChiseledBookshelfAddRemovePerSlot(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:chiseled_bookshelf"], dimMinY)
	b := loop.resolveChiseledBookshelf(pos)
	if b == nil {
		t.Fatal("resolveChiseledBookshelf returned nil for a placed bookshelf")
	}

	for slot := 0; slot < chiseledBookshelfMaxBooks; slot++ {
		// setItem(slot, book): the SLOT_<slot>_OCCUPIED flag turns true, others stay false.
		loop.chiseledBookshelfSetItem(pos, b, slot, bookBook())
		st, _ := mgr.GetBlock(pos, dimMinY)
		if !block.ChiseledBookshelfSlotOccupied(st, slot) {
			t.Fatalf("slot %d not occupied after setItem", slot)
		}
		for j := 0; j < chiseledBookshelfMaxBooks; j++ {
			if j == slot {
				continue
			}
			if block.ChiseledBookshelfSlotOccupied(st, j) {
				t.Fatalf("slot %d wrongly occupied after adding to slot %d", j, slot)
			}
		}
		// removeItem(slot): the returned stack is the book, the flag turns false again.
		got := loop.chiseledBookshelfRemoveItem(pos, b, slot)
		if int(got.ItemID) != int(bookBook().ItemID) || got.Count != 1 {
			t.Fatalf("removeItem slot %d = %+v, want one book", slot, got)
		}
		st2, _ := mgr.GetBlock(pos, dimMinY)
		if block.ChiseledBookshelfSlotOccupied(st2, slot) {
			t.Fatalf("slot %d still occupied after removeItem", slot)
		}
	}
}

// TestChiseledBookshelfLastInteractedSlotComparator asserts lastInteractedSlot tracking + the comparator
// output == getLastInteractedSlot()+1 (0 when untouched, slot+1 after an interaction), NOT an occupancy
// count.
func TestChiseledBookshelfLastInteractedSlotComparator(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 3, Y: 5, Z: 3}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:chiseled_bookshelf"], dimMinY)
	b := loop.resolveChiseledBookshelf(pos)

	// Untouched: getLastInteractedSlot() == -1 -> comparator 0.
	if sig, has := loop.chiseledBookshelfAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("untouched bookshelf analog = (%d,%v), want (0,true)", sig, has)
	}

	// Add to slot 4 -> lastInteractedSlot 4 -> comparator 5 (4+1), even though only ONE book is in.
	loop.chiseledBookshelfSetItem(pos, b, 4, bookBook())
	if b.lastInteractedSlot != 4 {
		t.Fatalf("lastInteractedSlot = %d, want 4", b.lastInteractedSlot)
	}
	if sig, has := loop.chiseledBookshelfAnalogOutputSignal(pos); !has || sig != 5 {
		t.Fatalf("after add-to-4 analog = (%d,%v), want (5,true) [slot+1, not fill count]", sig, has)
	}

	// Add a SECOND book to slot 1 -> lastInteractedSlot 1 -> comparator 2 (NOT 2-because-2-books; it is
	// slot 1 + 1). This is the exact vanilla "last interacted, not fullness" contract.
	loop.chiseledBookshelfSetItem(pos, b, 1, bookBook())
	if sig, has := loop.chiseledBookshelfAnalogOutputSignal(pos); !has || sig != 2 {
		t.Fatalf("after add-to-1 analog = (%d,%v), want (2,true)", sig, has)
	}

	// Remove from slot 4 -> lastInteractedSlot 4 -> comparator 5 again (remove also updates the slot).
	loop.chiseledBookshelfRemoveItem(pos, b, 4)
	if sig, has := loop.chiseledBookshelfAnalogOutputSignal(pos); !has || sig != 5 {
		t.Fatalf("after remove-from-4 analog = (%d,%v), want (5,true)", sig, has)
	}
}

// TestChiseledBookshelfBookFilter asserts acceptsItemType rejects non-#bookshelf_books items (setItem is a
// no-op for a non-book) and accepts every member of the tag.
func TestChiseledBookshelfBookFilter(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 4, Y: 5, Z: 4}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:chiseled_bookshelf"], dimMinY)
	b := loop.resolveChiseledBookshelf(pos)

	// A non-book (stone) is rejected: setItem is a no-op, the slot stays empty + unoccupied.
	stone := component.SlotData{ItemID: toItemID(int(itemNameToID("stone"))), Count: 1}
	if chiseledBookshelfAcceptsItemType(stone) {
		t.Fatal("acceptsItemType(stone) = true, want false")
	}
	loop.chiseledBookshelfSetItem(pos, b, 0, stone)
	st, _ := mgr.GetBlock(pos, dimMinY)
	if block.ChiseledBookshelfSlotOccupied(st, 0) {
		t.Fatal("slot 0 occupied after setItem(stone); the tag filter must reject it")
	}

	// Every #bookshelf_books member is accepted.
	for _, name := range []string{"book", "written_book", "enchanted_book", "writable_book", "knowledge_book"} {
		s := component.SlotData{ItemID: toItemID(int(itemNameToID(name))), Count: 1}
		if !chiseledBookshelfAcceptsItemType(s) {
			t.Fatalf("acceptsItemType(%s) = false, want true (in #bookshelf_books)", name)
		}
	}
}

// TestChiseledBookshelfHitSlotGeometry asserts the hit-vec -> slot mapping (2 rows x 3 cols) on the
// facing face, bit-exact with SelectableSlotContainer.getHitSlot. For a SOUTH-facing shelf clicked on its
// SOUTH face (Direction.South == 3): vx = x, vy = y; row = getSection(1-vy, 2); col = getSection(vx, 3);
// slot = col + row*3. Slot 0 is TOP-LEFT (high y, low x), slot 5 is BOTTOM-RIGHT (low y, high x).
func TestChiseledBookshelfHitSlotGeometry(t *testing.T) {
	south := block.South
	southFace := int(block.South)

	// Grid centers: columns at x in {1/6, 1/2, 5/6}, rows at y in {3/4 (top), 1/4 (bottom)}.
	cases := []struct {
		name     string
		x, y     float32
		wantSlot int
	}{
		{"top-left", 1.0 / 6, 0.75, 0},     // col0,row0
		{"top-mid", 0.5, 0.75, 1},          // col1,row0
		{"top-right", 5.0 / 6, 0.75, 2},    // col2,row0
		{"bottom-left", 1.0 / 6, 0.25, 3},  // col0,row1
		{"bottom-mid", 0.5, 0.25, 4},       // col1,row1
		{"bottom-right", 5.0 / 6, 0.25, 5}, // col2,row1
	}
	for _, c := range cases {
		slot, ok := chiseledBookshelfHitSlot(south, southFace, c.x, c.y, 0.5)
		if !ok || slot != c.wantSlot {
			t.Fatalf("hitSlot(%s x=%.3f y=%.3f) = (%d,%v), want (%d,true)", c.name, c.x, c.y, slot, ok, c.wantSlot)
		}
	}

	// A click on a face OTHER than FACING yields no slot (Optional.empty -> PASS).
	if _, ok := chiseledBookshelfHitSlot(south, int(block.North), 0.5, 0.5, 0.5); ok {
		t.Fatal("hitSlot on a non-facing face returned a slot; want empty")
	}
	// A non-horizontal face (UP) also yields no slot.
	if _, ok := chiseledBookshelfHitSlot(block.Up, int(block.Up), 0.5, 0.5, 0.5); ok {
		t.Fatal("hitSlot on UP (non-horizontal) returned a slot; want empty")
	}
}

// TestChiseledBookshelfUseAddRemove asserts the full useChiseledBookshelf interaction: a right-click with
// a book in hand ADDS it to the hit slot (shrinking the hand), and an empty-hand right-click on the same
// slot REMOVES it back into the inventory. Uses a SOUTH-facing shelf.
func TestChiseledBookshelfUseAddRemove(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 5, Y: 5, Z: 5}
	// Place a SOUTH-facing shelf explicitly so the hit geometry is deterministic.
	def := block.DefaultStateID["minecraft:chiseled_bookshelf"]
	southState, ok := stateSouthBookshelf(def)
	if !ok {
		t.Fatal("could not resolve a SOUTH-facing bookshelf state")
	}
	mgr.SetBlock(pos, southState, dimMinY)

	p := &tickPlayer{client: captureClient(64), gameMode: gameModeSurvival}
	loop.players = append(loop.players, p)
	setHeldItem(p, item.Book.ID, 3)

	// Right-click the top-left slot (slot 0) on the SOUTH face with a book in hand -> addBook.
	if !loop.useChiseledBookshelf(p, pos, southState, int(block.South), 1.0/6, 0.75, 0.5) {
		t.Fatal("useChiseledBookshelf(add) returned false; want consumed")
	}
	st, _ := mgr.GetBlock(pos, dimMinY)
	if !block.ChiseledBookshelfSlotOccupied(st, 0) {
		t.Fatal("slot 0 not occupied after use-add")
	}
	inv := ensureInventory(p)
	if held := inv.get(heldWindowSlot(inv.heldSlot)); held.Count != 2 {
		t.Fatalf("held count = %d after use-add, want 2 (consumeAndReturn(1))", held.Count)
	}
	// Comparator now reads slot0+1 = 1.
	if sig, has := loop.chiseledBookshelfAnalogOutputSignal(pos); !has || sig != 1 {
		t.Fatalf("after use-add analog = (%d,%v), want (1,true)", sig, has)
	}

	// Empty the hand, right-click the same slot -> removeBook (book goes back to the inventory).
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 0})
	st2, _ := mgr.GetBlock(pos, dimMinY)
	if !loop.useChiseledBookshelf(p, pos, st2, int(block.South), 1.0/6, 0.75, 0.5) {
		t.Fatal("useChiseledBookshelf(remove) returned false; want consumed")
	}
	st3, _ := mgr.GetBlock(pos, dimMinY)
	if block.ChiseledBookshelfSlotOccupied(st3, 0) {
		t.Fatal("slot 0 still occupied after use-remove")
	}
}

// TestChiseledBookshelfNBTRoundTrip asserts encode/decode preserves the 6-slot Items list + the
// last_interacted_slot int exactly.
func TestChiseledBookshelfNBTRoundTrip(t *testing.T) {
	orig := &chiseledBookshelfBE{lastInteractedSlot: 3}
	orig.items[0] = bookBook()
	orig.items[3] = enchantedBook()
	orig.items[5] = component.SlotData{ItemID: toItemID(int(itemNameToID("writable_book"))), Count: 1}

	data, dropped, err := encodeChiseledBookshelfBE(orig)
	if err != nil {
		t.Fatalf("encodeChiseledBookshelfBE: %v", err)
	}
	_ = dropped
	got := decodeChiseledBookshelfBE(data)

	if got.lastInteractedSlot != 3 {
		t.Fatalf("lastInteractedSlot round-trip = %d, want 3", got.lastInteractedSlot)
	}
	for i := 0; i < chiseledBookshelfMaxBooks; i++ {
		if int(got.items[i].ItemID) != int(orig.items[i].ItemID) || got.items[i].Count != orig.items[i].Count {
			t.Fatalf("slot %d round-trip = %+v, want %+v", i, got.items[i], orig.items[i])
		}
	}

	// A fresh (default) shelf round-trips to lastInteractedSlot -1 (the getIntOr default).
	empty := &chiseledBookshelfBE{lastInteractedSlot: chiseledBookshelfDefaultLastSlot}
	d2, _, err := encodeChiseledBookshelfBE(empty)
	if err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	g2 := decodeChiseledBookshelfBE(d2)
	if g2.lastInteractedSlot != -1 {
		t.Fatalf("empty shelf lastInteractedSlot round-trip = %d, want -1", g2.lastInteractedSlot)
	}
}

// stateSouthBookshelf resolves the SOUTH-facing, all-slots-empty variant of a chiseled bookshelf so the
// hit geometry in the interaction test is deterministic (the default state is NORTH-facing). It scans the
// state list for the matching (FACING=South, all occupied=false) state id.
func stateSouthBookshelf(def block.StateID) (block.StateID, bool) {
	for i := 0; i < len(block.StateList); i++ {
		s := block.StateID(i)
		if !block.IsChiseledBookshelf(s) || block.ChiseledBookshelfFacing(s) != block.South {
			continue
		}
		occ := false
		for j := 0; j < chiseledBookshelfMaxBooks; j++ {
			if block.ChiseledBookshelfSlotOccupied(s, j) {
				occ = true
				break
			}
		}
		if !occ {
			return s, true
		}
	}
	return def, false
}
