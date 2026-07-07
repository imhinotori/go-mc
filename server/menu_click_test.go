package server

// menu_click_test.go — exercises the GENERIC SWAP / CLONE / PICKUP_ALL branches (menu_click.go) over a
// non-player window (the open chest), plus the unified stateId counter (incrementStateId). Every
// assertion checks EXACT slot counts + ids + carried + stateId so a regression in any guard, range
// bound, or numeric op is caught. Drives the click engine directly (deterministic, no wire framing).
//
// Chest window layout (chest_click.go): chest 0..26, player main 27..53 (window 9..35), hotbar 54..62
// (window 36..44). SWAP's button j indexes the PLAYER INVENTORY (hotbar 0-8 → window 36-44).

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// menuClickLoop builds a loop + survival player with an initialized inventory and an open chest window
// backed by a fresh 27-slot chestLoot (already rolled → 27 empty slots).
func menuClickLoop(t *testing.T) (*TickLoop, *tickPlayer, *Inventory, *chestLoot) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)
	cl := &chestLoot{LootTable: ""}
	cl.ensureContainer() // pad to 27 empty slots
	p.openContainer = &openContainer{windowID: 1, kind: containerKindChest, chestPos: pk.Position{X: 0, Y: 64, Z: 0}}
	loop.openChests = map[pk.Position]*chestLoot{p.openContainer.chestPos: cl}
	return loop, p, inv, cl
}

// TestMenuSwapHotbarEmptyTakesChestSlot: SWAP (number key) with an EMPTY hotbar cell over a filled chest
// slot moves the chest stack into the hotbar and empties the chest slot. Chest slot 0 → window 36 (hotbar 0).
func TestMenuSwapHotbarEmptyTakesChestSlot(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	cl.items[0] = stk(1, 20) // chest slot 0 holds 20 of item 1
	// hotbar 0 (window 36) is empty.

	loop.doChestClick(p, cl, inv, 0 /*chest slot 0*/, 0 /*hotbar key 0*/, containerInputSwap)

	if got := inv.get(36); got.Count != 20 || got.ItemID != 1 {
		t.Fatalf("hotbar 0 after swap = count=%d id=%d, want 20/1", got.Count, got.ItemID)
	}
	if cl.items[0].Count != 0 {
		t.Fatalf("chest slot 0 after swap = %d, want 0 (emptied)", cl.items[0].Count)
	}
	if c := inv.getCarried(); c.Count != 0 {
		t.Fatalf("cursor after swap = %d, want 0 (SWAP never touches cursor)", c.Count)
	}
}

// TestMenuSwapBothFilledSwaps: SWAP with BOTH the hotbar cell and the chest slot filled swaps the two
// (both under the slot max). Chest slot 2 <-> hotbar 3 (window 39).
func TestMenuSwapBothFilledSwaps(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	cl.items[2] = stk(1, 10) // chest slot 2: 10 of item 1
	inv.set(39, stk(2, 5))   // hotbar 3: 5 of item 2

	loop.doChestClick(p, cl, inv, 2, 3 /*hotbar key 3*/, containerInputSwap)

	if got := inv.get(39); got.Count != 10 || got.ItemID != 1 {
		t.Fatalf("hotbar 3 after swap = count=%d id=%d, want 10/1", got.Count, got.ItemID)
	}
	if cl.items[2].Count != 5 || cl.items[2].ItemID != 2 {
		t.Fatalf("chest slot 2 after swap = count=%d id=%d, want 5/2", cl.items[2].Count, cl.items[2].ItemID)
	}
}

// TestMenuSwapOffhandIndex: SWAP button j==40 targets the offhand (Inventory index 40 → window slot 45).
// An empty offhand over a filled chest slot moves the chest stack into the offhand.
func TestMenuSwapOffhandIndex(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	cl.items[5] = stk(1, 3)

	loop.doChestClick(p, cl, inv, 5, 40 /*offhand*/, containerInputSwap)

	if got := inv.get(45); got.Count != 3 || got.ItemID != 1 {
		t.Fatalf("offhand (window 45) after swap = count=%d id=%d, want 3/1", got.Count, got.ItemID)
	}
	if cl.items[5].Count != 0 {
		t.Fatalf("chest slot 5 after swap = %d, want 0", cl.items[5].Count)
	}
}

// TestMenuSwapInvalidButtonNoOp: SWAP with j==15 (not a hotbar 0-8 or offhand 40) is a no-op (the @1086
// guard). Nothing moves.
func TestMenuSwapInvalidButtonNoOp(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	cl.items[0] = stk(1, 20)

	loop.doChestClick(p, cl, inv, 0, 15 /*invalid*/, containerInputSwap)

	if cl.items[0].Count != 20 {
		t.Fatalf("chest slot 0 after invalid swap = %d, want 20 (untouched)", cl.items[0].Count)
	}
}

// TestMenuCloneCreativeFillsMaxStack: CLONE (creative middle-click) on a filled chest slot with an empty
// cursor puts a max-stack (64) copy of the item onto the cursor; the chest slot is UNCHANGED.
func TestMenuCloneCreativeFillsMaxStack(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	p.gameMode = gameModeCreative
	cl.items[4] = stk(1, 7) // 7 of item 1 (max stack 64)

	loop.doChestClick(p, cl, inv, 4, 2 /*middle button*/, containerInputClone)

	if c := inv.getCarried(); c.Count != 64 || c.ItemID != 1 {
		t.Fatalf("cursor after clone = count=%d id=%d, want 64/1 (max stack)", c.Count, c.ItemID)
	}
	if cl.items[4].Count != 7 {
		t.Fatalf("chest slot 4 after clone = %d, want 7 (unchanged)", cl.items[4].Count)
	}
}

// TestMenuCloneSurvivalNoOp: CLONE in SURVIVAL is a no-op (hasInfiniteMaterials false). Cursor stays empty.
func TestMenuCloneSurvivalNoOp(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	p.gameMode = gameModeSurvival
	cl.items[4] = stk(1, 7)

	loop.doChestClick(p, cl, inv, 4, 2, containerInputClone)

	if c := inv.getCarried(); c.Count != 0 {
		t.Fatalf("cursor after survival clone = %d, want 0 (no-op)", c.Count)
	}
}

// TestMenuCloneCursorOccupiedNoOp: CLONE with a NON-empty cursor is a no-op (the @1400 guard).
func TestMenuCloneCursorOccupiedNoOp(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	p.gameMode = gameModeCreative
	cl.items[4] = stk(1, 7)
	inv.setCarried(stk(2, 1)) // cursor already holds something

	loop.doChestClick(p, cl, inv, 4, 2, containerInputClone)

	if c := inv.getCarried(); c.Count != 1 || c.ItemID != 2 {
		t.Fatalf("cursor after clone-with-occupied = count=%d id=%d, want 1/2 (unchanged)", c.Count, c.ItemID)
	}
}

// TestMenuPickupAllGathersMatching: PICKUP_ALL (double-click) with a partial stack on the cursor gathers
// every matching same-item stack across the whole chest window onto the cursor, up to its max (64), in
// the vanilla two-pass order. The clicked slot i is a slot the cursor cannot pick from directly.
func TestMenuPickupAllGathersMatching(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	// Scatter item 1 across several chest slots + a hotbar slot.
	cl.items[0] = stk(1, 10)
	cl.items[1] = stk(1, 20)
	cl.items[2] = stk(2, 5) // a DIFFERENT item — must be left untouched
	inv.set(36, stk(1, 30)) // hotbar 0 (window 36) also holds item 1
	inv.setCarried(stk(1, 5))

	// Double-click on chest slot 0 (which has item 1). @1649: if the clicked slot has a takeable matching
	// item, a normal PICKUP handled it — so use a slot whose item cannot be taken by direct pickup. Vanilla
	// gathers regardless of which same-item slot is double-clicked; drive on an empty slot to isolate the
	// gather (empty clicked slot: hasItem false, so the @1649 early-return does not fire).
	loop.doChestClick(p, cl, inv, 26 /*empty chest slot*/, 0, containerInputPickupAll)

	// Cursor fills to 64: 5 (start) + 10 + 20 + 29 (from the 30-stack, capped at 64) = 64.
	if c := inv.getCarried(); c.Count != 64 || c.ItemID != 1 {
		t.Fatalf("cursor after pickup-all = count=%d id=%d, want 64/1", c.Count, c.ItemID)
	}
	// item 2 slot untouched.
	if cl.items[2].Count != 5 || cl.items[2].ItemID != 2 {
		t.Fatalf("different-item chest slot 2 = count=%d id=%d, want 5/2 (untouched)", cl.items[2].Count, cl.items[2].ItemID)
	}
	// Total of item 1 collected = 65 total (5+10+20+30); 64 on cursor → 1 left in the world of stacks.
	remaining := 0
	for _, s := range cl.items {
		if s.ItemID == 1 {
			remaining += int(s.Count)
		}
	}
	remaining += int(inv.get(36).Count) // any left in hotbar 0
	if remaining != 1 {
		t.Fatalf("item 1 left in chest+hotbar = %d, want 1 (65 total - 64 on cursor)", remaining)
	}
}

// TestMenuPickupAllTwoPassSkipsFullFirst: pass 0 skips already-full source stacks so half-stacks are
// consumed before full ones. Cursor starts at 1; a full 64-stack and a 10-stack both match. The gather
// caps the cursor at 64; the two-pass order takes the 10 (partial) before touching the full 64.
func TestMenuPickupAllTwoPassSkipsFullFirst(t *testing.T) {
	loop, p, inv, cl := menuClickLoop(t)
	cl.items[0] = stk(1, 64) // FULL stack (pass 0 must skip it)
	cl.items[1] = stk(1, 10) // partial (pass 0 takes it)
	inv.setCarried(stk(1, 1))

	loop.doChestClick(p, cl, inv, 26 /*empty*/, 0, containerInputPickupAll)

	// Cursor: 1 + 10 (partial, pass 0) + 53 (from the 64, pass 1, capped) = 64.
	if c := inv.getCarried(); c.Count != 64 {
		t.Fatalf("cursor after two-pass pickup-all = %d, want 64", c.Count)
	}
	// The partial 10-stack was consumed entirely (pass 0). The formerly-full 64 lost 53 → 11 left.
	if cl.items[1].Count != 0 {
		t.Fatalf("partial stack (slot 1) = %d, want 0 (consumed in pass 0)", cl.items[1].Count)
	}
	if cl.items[0].Count != 11 {
		t.Fatalf("formerly-full stack (slot 0) = %d, want 11 (64 - 53)", cl.items[0].Count)
	}
}

// TestMenuStateIdIncrementMasked: incrementStateId is (stateId + 1) & 32767 and wraps at 32767. Each
// authoritative send bumps it; the value is echoed in the ContainerSetContent packet.
func TestMenuStateIdIncrementMasked(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	inv := ensureInventory(p)

	if inv.stateID != 0 {
		t.Fatalf("fresh stateID = %d, want 0", inv.stateID)
	}
	if got := inv.incrementStateId(); got != 1 || inv.stateID != 1 {
		t.Fatalf("first incrementStateId = %d (field %d), want 1/1", got, inv.stateID)
	}
	if got := inv.incrementStateId(); got != 2 {
		t.Fatalf("second incrementStateId = %d, want 2", got)
	}

	// Wrap: at 32767, the next increment masks to 0.
	inv.stateID = 32767
	if got := inv.incrementStateId(); got != 0 {
		t.Fatalf("increment at 32767 = %d, want 0 (& 32767 wrap)", got)
	}
	_ = loop
}

// TestMenuStateIdEchoedInPacket: a container send (sendContent) bumps the stateId and carries it in the
// emitted ClientboundContainerSetContent. Two successive sends produce strictly-increasing echoed ids
// (1 then 2), proving the id is tracked AND echoed on every container packet.
func TestMenuStateIdEchoedInPacket(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	_ = ensureInventory(p)

	loop.sendContent(p)
	loop.sendContent(p)

	// drainPackets closes the queue, so drain once. Decode each SetContent's stateId (field 2 of the
	// composite: VarInt window, VarInt stateId, ...).
	var ids []int32
	for _, pkt := range drainPackets(p.client) {
		if pkt.ID != int32(packetid.ClientboundContainerSetContent) {
			continue
		}
		r := bytes.NewReader(pkt.Data)
		var win, state pk.VarInt
		_, _ = win.ReadFrom(r)
		_, _ = state.ReadFrom(r)
		ids = append(ids, int32(state))
	}
	if len(ids) < 2 {
		t.Fatalf("expected >=2 SetContent packets, got %d", len(ids))
	}
	if ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("echoed stateIds = %v, want [1 2] (masked increment per send)", ids[:2])
	}
}
