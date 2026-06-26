package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// inventory_click_test.go exercises the 1:1 port of AbstractContainerMenu.clicked → doClick
// (server/inventory_doclick.go) and InventoryMenu.quickMoveStack / moveItemStackTo
// (server/inventory_click.go). Every assertion checks EXACT counts so a regression in any branch,
// range bound, or numeric op is caught. The tests drive doClick directly (deterministic, no RNG); the
// carried-sync test drives the full clicked() path to assert the SetSlot(-1,-1) emission.
//
// Item id 1 is a plain stackable (StackSize 64, generated registry); slots are the InventoryMenu window
// indices (9-35 main, 36-44 hotbar).

// stk is a count-of-itemID stack helper.
func stk(itemID, count int32) component.SlotData {
	return component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(itemID)}
}

// clickLoop builds a loop + survival player with an initialized inventory.
func clickLoop(t *testing.T) (*TickLoop, *tickPlayer, *Inventory) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)
	return loop, p, inv
}

// TestClickPickupEmptyToCarriedToDeposit: PICKUP primary on a filled slot moves the whole stack to the
// cursor; PICKUP primary on an empty slot deposits the whole cursor back.
func TestClickPickupEmptyToCarriedToDeposit(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(36, stk(1, 32)) // hotbar slot 0 holds 32 of item 1

	// PICKUP primary (j=0) on slot 36 → 32 onto the cursor, slot empty.
	loop.doClick(p, inv, 36, 0, containerInputPickup)
	if c := inv.getCarried(); c.Count != 32 || c.ItemID != 1 {
		t.Fatalf("after pickup: carried = count=%d id=%d, want 32/1", c.Count, c.ItemID)
	}
	if s := inv.get(36); s.Count != 0 {
		t.Fatalf("after pickup: slot 36 count=%d, want 0", s.Count)
	}

	// PICKUP primary on empty slot 9 → deposit all 32, cursor empty.
	loop.doClick(p, inv, 9, 0, containerInputPickup)
	if s := inv.get(9); s.Count != 32 || s.ItemID != 1 {
		t.Fatalf("after deposit: slot 9 = count=%d id=%d, want 32/1", s.Count, s.ItemID)
	}
	if c := inv.getCarried(); c.Count != 0 {
		t.Fatalf("after deposit: carried count=%d, want 0", c.Count)
	}
}

// TestClickPickupSecondaryHalfAndPlaceOne: secondary pickup takes ceil(count/2); secondary deposit on a
// matching slot places exactly one.
func TestClickPickupSecondaryHalfAndPlaceOne(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(36, stk(1, 7)) // odd count → ceil(7/2)=4

	loop.doClick(p, inv, 36, 1, containerInputPickup) // secondary
	if c := inv.getCarried(); c.Count != 4 {
		t.Fatalf("secondary pickup carried=%d, want 4 (ceil(7/2))", c.Count)
	}
	if s := inv.get(36); s.Count != 3 {
		t.Fatalf("secondary pickup left slot=%d, want 3", s.Count)
	}

	// Secondary deposit onto same-item slot 36 (now 3) → places one: slot 4, cursor 3.
	loop.doClick(p, inv, 36, 1, containerInputPickup)
	if s := inv.get(36); s.Count != 4 {
		t.Fatalf("secondary place-one slot=%d, want 4", s.Count)
	}
	if c := inv.getCarried(); c.Count != 3 {
		t.Fatalf("secondary place-one carried=%d, want 3", c.Count)
	}
}

// TestClickQuickMoveMainToHotbarMergeThenFill: shift-click (QUICK_MOVE) of a main-slot stack routes into
// the hotbar via moveItemStackTo — first merging into a partial same-item hotbar slot, then filling an
// empty one for the remainder.
func TestClickQuickMoveMainToHotbarMergeThenFill(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(9, stk(1, 40))  // main slot 0: 40 of item 1 (to shift up)
	inv.set(36, stk(1, 50)) // hotbar slot 0: partial 50 → can take 14
	// hotbar slots 37..44 empty.

	loop.doClick(p, inv, 9, 0, containerInputQuickMove)

	// Source main slot 9 must be emptied (40 fully moved: 14 merged + 26 to a fresh hotbar slot).
	if s := inv.get(9); s.Count != 0 {
		t.Fatalf("quick-move source slot 9 = %d, want 0 (fully moved)", s.Count)
	}
	// Hotbar slot 36 filled to max 64.
	if s := inv.get(36); s.Count != 64 {
		t.Fatalf("quick-move merge target 36 = %d, want 64", s.Count)
	}
	// Remainder 26 landed in the first empty hotbar slot (37).
	if s := inv.get(37); s.Count != 26 || s.ItemID != 1 {
		t.Fatalf("quick-move fill target 37 = count=%d id=%d, want 26/1", s.Count, s.ItemID)
	}
}

// TestClickSwap: SWAP (number-key) exchanges a slot with the hotbar index j. With an empty target hotbar
// slot and a filled main slot, the stack moves to the hotbar; a second swap exchanges back.
func TestClickSwap(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(9, stk(1, 20)) // main slot to swap
	// hotbar index 0 (j=0 → menu slot 36) empty.

	loop.doClick(p, inv, 9, 0, containerInputSwap)
	if s := inv.get(36); s.Count != 20 || s.ItemID != 1 {
		t.Fatalf("swap to empty hotbar: menu36 = count=%d id=%d, want 20/1", s.Count, s.ItemID)
	}
	if s := inv.get(9); s.Count != 0 {
		t.Fatalf("swap left source 9 = %d, want 0", s.Count)
	}

	// Put a different item in the main slot and swap again → exchange.
	inv.set(9, stk(2, 5))
	loop.doClick(p, inv, 9, 0, containerInputSwap)
	if s := inv.get(9); s.Count != 20 || s.ItemID != 1 {
		t.Fatalf("swap exchange: slot 9 = count=%d id=%d, want 20/1", s.Count, s.ItemID)
	}
	if s := inv.get(36); s.Count != 5 || s.ItemID != 2 {
		t.Fatalf("swap exchange: menu36 = count=%d id=%d, want 5/2", s.Count, s.ItemID)
	}
}

// TestClickThrowSingleAndFull: THROW j=0 drops one item; THROW j=1 drains the whole slot. Asserts the
// resulting slot counts (the dropped ItemEntity spawn is exercised but not asserted).
func TestClickThrowSingleAndFull(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(36, stk(1, 5))

	loop.doClick(p, inv, 36, 0, containerInputThrow) // drop 1
	if s := inv.get(36); s.Count != 4 {
		t.Fatalf("throw j=0: slot = %d, want 4", s.Count)
	}

	loop.doClick(p, inv, 36, 1, containerInputThrow) // drop the rest
	if s := inv.get(36); s.Count != 0 {
		t.Fatalf("throw j=1: slot = %d, want 0 (drained)", s.Count)
	}
}

// TestClickPickupAll: double-click (PICKUP_ALL) collects matching same-item slots into the carried stack
// up to its max stack size. Sets up a partial cursor + several matching slots; asserts the cursor fills
// to 64 and the source slots are drained in order.
func TestClickPickupAll(t *testing.T) {
	loop, p, inv := clickLoop(t)
	// Cursor holds 10; matching item slots scattered. PICKUP_ALL on an empty clicked slot collects.
	inv.setCarried(stk(1, 10))
	inv.set(9, stk(1, 30))
	inv.set(10, stk(1, 30))
	inv.set(11, stk(1, 30))
	// Clicked slot 20 is empty (so the @1649 early-return does not fire).

	loop.doClick(p, inv, 20, 0, containerInputPickupAll)

	// Cursor must fill to max 64: needed 54 → drains 30 (slot9) + 24 (slot10), leaving slot10=6, slot11
	// untouched.
	if c := inv.getCarried(); c.Count != 64 {
		t.Fatalf("pickup-all carried = %d, want 64 (filled to max)", c.Count)
	}
	if s := inv.get(9); s.Count != 0 {
		t.Fatalf("pickup-all slot 9 = %d, want 0 (drained first)", s.Count)
	}
	if s := inv.get(10); s.Count != 6 {
		t.Fatalf("pickup-all slot 10 = %d, want 6 (partially drained)", s.Count)
	}
	if s := inv.get(11); s.Count != 30 {
		t.Fatalf("pickup-all slot 11 = %d, want 30 (untouched)", s.Count)
	}
}

// TestClickQuickCraftEvenSplit: a left-drag (type 0, even-split) across 3 empty slots from a 9-stack
// cursor places floor(9/3)=3 in each and leaves the cursor empty. Drives the START/ADD×3/END sequence.
func TestClickQuickCraftEvenSplit(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.setCarried(stk(1, 9))

	// START: button = (type<<2)|0 with type 0 → button 0, header 0 (status 0 START).
	loop.doClick(p, inv, -999, 0, containerInputQuickCraft)
	if inv.quickcraftStatus != 1 || inv.quickcraftType != 0 {
		t.Fatalf("quickcraft START: status=%d type=%d, want 1/0", inv.quickcraftStatus, inv.quickcraftType)
	}

	// ADD slots 9, 10, 11: button = (type<<2)|1 = 1 (header 1 = ADD).
	for _, idx := range []int{9, 10, 11} {
		loop.doClick(p, inv, idx, 1, containerInputQuickCraft)
	}
	if n := len(inv.quickcraftSlots); n != 3 {
		t.Fatalf("quickcraft ADD: drag set size=%d, want 3", n)
	}

	// END: button = (type<<2)|2 = 2 (header 2 = END distribute).
	loop.doClick(p, inv, -999, 2, containerInputQuickCraft)

	for _, idx := range []int16{9, 10, 11} {
		if s := inv.get(idx); s.Count != 3 || s.ItemID != 1 {
			t.Fatalf("quickcraft even-split slot %d = count=%d id=%d, want 3/1", idx, s.Count, s.ItemID)
		}
	}
	if c := inv.getCarried(); c.Count != 0 {
		t.Fatalf("quickcraft even-split carried = %d, want 0 (9 = 3*3 distributed)", c.Count)
	}
}

// TestClickCloneCreativeOnly: CLONE (creative middle-click) clones a slot to a max stack onto an empty
// cursor — only in creative. In survival it is a no-op.
func TestClickCloneCreativeOnly(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(36, stk(1, 5))

	// Survival: no-op.
	loop.doClick(p, inv, 36, 2, containerInputClone)
	if c := inv.getCarried(); c.Count != 0 {
		t.Fatalf("clone in survival carried=%d, want 0 (no-op)", c.Count)
	}

	// Creative: clone → carried = max stack (64) of item 1.
	p.gameMode = gameModeCreative
	loop.doClick(p, inv, 36, 2, containerInputClone)
	if c := inv.getCarried(); c.Count != 64 || c.ItemID != 1 {
		t.Fatalf("clone in creative carried = count=%d id=%d, want 64/1", c.Count, c.ItemID)
	}
	// Source slot is unchanged (clone copies, does not consume).
	if s := inv.get(36); s.Count != 5 {
		t.Fatalf("clone source slot=%d, want 5 (unchanged)", s.Count)
	}
}

// TestClickCarriedSyncEmitsSetSlotMinus1: a full clicked() that changes the carried item emits a
// ClientboundContainerSetSlot with containerId -1, slot -1 (synchronizeCarriedToRemote).
func TestClickCarriedSyncEmitsSetSlotMinus1(t *testing.T) {
	loop, p, inv := clickLoop(t)
	inv.set(36, stk(1, 16))

	// clicked() PICKUP primary on slot 36 → carried becomes 16, which must sync.
	loop.clicked(p, playerContainerID, 36, 0, containerInputPickup)

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundContainerSetSlot); n < 1 {
		t.Fatalf("carried-change clicked sent %d SetSlot, want >=1", n)
	}

	// Confirm at least one SetSlot is the carried sync: containerId -1, slot -1.
	found := false
	for _, pkt := range got {
		if pkt.ID != int32(packetid.ClientboundContainerSetSlot) {
			continue
		}
		cid, slot, ok := decodeSetSlotHeader(pkt.Data)
		if ok && cid == -1 && slot == -1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no SetSlot(-1, -1) carried-sync emitted")
	}
}

// decodeSetSlotHeader reads the ClientboundContainerSetSlot header (VarInt containerId, VarInt stateId,
// Short slot) and returns containerId, slot. Used to identify the carried-sync packet.
func decodeSetSlotHeader(data []byte) (containerID int32, slot int16, ok bool) {
	r := bytes.NewReader(data)
	var cid, sid pk.VarInt
	var sl pk.Short
	if _, err := cid.ReadFrom(r); err != nil {
		return 0, 0, false
	}
	if _, err := sid.ReadFrom(r); err != nil {
		return 0, 0, false
	}
	if _, err := sl.ReadFrom(r); err != nil {
		return 0, 0, false
	}
	return int32(cid), int16(sl), true
}
