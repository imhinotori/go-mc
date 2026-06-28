package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_entity_test.go covers Plan 17-14 (ITEM-PICKUP): the dropped-item tick (pickupDelay
// countdown, age, despawn) and the player pickup scan (inventory grows, item removed, take-item
// animation), plus the creative-gamemode no-drop gate.

// dropStack is the v1 cobblestone drop used across these tests.
func dropStack() component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(item.Cobblestone.ID)}
}

// TestItemPickupDelayDecrements: a freshly spawned item starts at pickupDelay 10
// (setDefaultPickUpDelay) and the item tick decrements it by exactly one each tick toward 0.
func TestItemPickupDelayDecrements(t *testing.T) {
	loop, _ := newDropLoop()
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	loop.only().entities.add(ie)

	if ie.pickupDelay != itemDefaultPickupDelay {
		t.Fatalf("fresh pickupDelay = %d, want %d (setDefaultPickUpDelay)", ie.pickupDelay, itemDefaultPickupDelay)
	}

	// Ten item ticks should walk pickupDelay 10 -> 0, one per tick.
	for i := 1; i <= itemDefaultPickupDelay; i++ {
		loop.tickItem(ie)
		want := itemDefaultPickupDelay - i
		if ie.pickupDelay != want {
			t.Fatalf("after %d item ticks pickupDelay = %d, want %d (one decrement/tick)", i, ie.pickupDelay, want)
		}
	}

	// At 0 it stays at 0 (never goes negative).
	loop.tickItem(ie)
	if ie.pickupDelay != 0 {
		t.Fatalf("pickupDelay = %d after reaching 0, want it to stay 0", ie.pickupDelay)
	}
}

// TestItemAgesAndDespawns: the item tick increments age each tick and DISCARDS the item from
// the store once age reaches LIFETIME (6000). Driving age to the threshold removes the entity.
func TestItemAgesAndDespawns(t *testing.T) {
	loop, _ := newDropLoop()
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	loop.only().entities.add(ie)

	// Jump age to one tick short of LIFETIME so the test is fast; one more item tick crosses it.
	ie.age = itemLifetime - 1
	if _, ok := loop.only().entities.get(ie.id); !ok {
		t.Fatalf("item missing from store before despawn")
	}

	loop.tickItem(ie) // age -> 6000, age >= LIFETIME -> discard
	if _, ok := loop.only().entities.get(ie.id); ok {
		t.Fatalf("item still in store at age %d, want despawned at LIFETIME %d", ie.age, itemLifetime)
	}
}

// TestItemPickedUpAfterDelay: a player standing on a pickable item (pickupDelay already 0)
// collects it — the inventory grows by the dropped stack, the item is removed from the store,
// and the take-item animation packet is sent. While pickupDelay > 0 the item is NOT collected.
func TestItemPickedUpAfterDelay(t *testing.T) {
	loop, mgr := newDropLoop()
	// Give the column a floor so the item does not free-fall out of pickup range during ticks.
	mgr.SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000 // a distinct id (not the item's) so the tracker treats it as a real player
	ensureInventory(p)

	// Spawn the item right at the player's feet so it is inside the (1.0,0.5,1.0)-inflated box.
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	loop.only().entities.add(ie)

	// While the pickup delay is still running, the scan must NOT collect the item.
	loop.scanItemPickup(p)
	if _, ok := loop.only().entities.get(ie.id); !ok {
		t.Fatalf("item collected while pickupDelay=%d, want it to stay (not yet pickable)", ie.pickupDelay)
	}

	// Mark the player as tracking the item so takeItem's sendToTrackingPlayers reaches it.
	p.tracked = map[int32]bool{ie.id: true}

	// Make it pickable (delay elapsed) and scan again: now it is collected.
	ie.pickupDelay = 0
	beforeCount := totalInventoryCount(p.inventory)
	loop.scanItemPickup(p)

	if _, ok := loop.only().entities.get(ie.id); ok {
		t.Fatalf("item NOT picked up by a player within range with pickupDelay=0 — the reported bug")
	}
	afterCount := totalInventoryCount(p.inventory)
	if afterCount != beforeCount+1 {
		t.Fatalf("inventory total = %d, want %d (the dropped stack of 1 added)", afterCount, beforeCount+1)
	}

	// The take-item animation must have been broadcast to the tracking collector.
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundTakeItemEntity); n < 1 {
		t.Fatalf("ClientboundTakeItemEntity emitted %d times, want >= 1 (the pickup animation)", n)
	}
}

// TestItemPickupOutOfRange: an item far from the player (outside the inflated pickup box) is
// NOT collected even when pickable.
func TestItemPickupOutOfRange(t *testing.T) {
	loop, _ := newDropLoop()
	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000
	ensureInventory(p)

	// 5 blocks away on X — well outside the player box inflated by 1.0 on X.
	ie := NewItemEntity(loop.idAlloc.AllocID(), 13.5, 64.0, 8.5, dropStack())
	ie.pickupDelay = 0
	loop.only().entities.add(ie)

	loop.scanItemPickup(p)
	if _, ok := loop.only().entities.get(ie.id); !ok {
		t.Fatalf("item 5 blocks away was collected, want it left on the ground (out of range)")
	}
}

// TestCreativeNoDrop: a creative-gamemode player's break drops NOTHING
// (ServerPlayerGameMode.destroyBlock). A survival player's identical break DOES drop.
func TestCreativeNoDrop(t *testing.T) {
	loop, mgr := newDropLoop()
	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	// Creative player: spawnBlockDrop must early-return, spawning no Item entity.
	creative := blockPlayer(loop, 1.5, 65.0, 1.5)
	creative.gameMode = gameModeCreative

	before := loop.only().entities.len()
	loop.spawnBlockDrop(creative, target, block.ToStateID[block.Stone{}])
	if got := loop.only().entities.len(); got != before {
		t.Fatalf("creative break spawned %d items, want 0 (creative drops nothing)", got-before)
	}

	// Survival player: the same call DOES spawn one Item entity (the gate passes).
	survival := blockPlayer(loop, 1.5, 65.0, 1.5)
	survival.gameMode = gameModeSurvival
	before = loop.only().entities.len()
	loop.spawnBlockDrop(survival, target, block.ToStateID[block.Stone{}])
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("survival break spawned %d items, want 1 (survival drops)", got-before)
	}
}

// TestInventoryAddStacksAndFree: inventoryAdd ports Inventory.add — it merges into an existing
// matching stack up to the max stack size, then overflows into a free slot.
func TestInventoryAddStacksAndFree(t *testing.T) {
	loop, _ := newDropLoop()
	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	inv := ensureInventory(p)

	// Seed slot 9 with 60 cobblestone (a partial stack, max 64).
	inv.set(9, component.SlotData{Count: 60, ItemID: pk.VarInt(item.Cobblestone.ID)})

	// Add 10 cobblestone: 4 fill slot 9 to 64, the remaining 6 land in the first free slot.
	stack := component.SlotData{Count: 10, ItemID: pk.VarInt(item.Cobblestone.ID)}
	if !loop.inventoryAdd(p, inv, &stack) {
		t.Fatalf("inventoryAdd returned false, want true (10 cobblestone absorbed)")
	}
	if stack.Count != 0 {
		t.Fatalf("leftover stack count = %d, want 0 (all 10 fit)", stack.Count)
	}
	if inv.get(9).Count != 64 {
		t.Fatalf("slot 9 count = %d, want 64 (filled to max)", inv.get(9).Count)
	}
	if total := totalInventoryCount(inv); total != 70 {
		t.Fatalf("inventory total = %d, want 70 (60 + 10 added)", total)
	}
}

// TestPickupLandsInStorageNeverCraftingOrArmor is the BUG-2 regression: a picked-up item must
// land ONLY in the main/hotbar storage window slots (9-44), NEVER in the crafting result/grid
// (window 0-4), armor (5-8), or offhand (45). Vanilla Inventory.getFreeSlot / addResource iterate
// items[0..35] only; the previous Sulfur code iterated all 46 window slots and leaked pickups into
// the crafting grid.
func TestPickupLandsInStorageNeverCraftingOrArmor(t *testing.T) {
	loop, mgr := newDropLoop()
	mgr.SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000
	inv := ensureInventory(p)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	ie.pickupDelay = 0
	loop.only().entities.add(ie)
	p.tracked = map[int32]bool{ie.id: true}

	loop.scanItemPickup(p)

	// The crafting slots (0-4) and armor slots (5-8) must remain empty.
	for s := int16(0); s <= 8; s++ {
		if inv.get(s).Count > 0 {
			t.Fatalf("pickup landed in window slot %d (crafting/armor), want only storage 9-44", s)
		}
	}
	// The offhand (45) must remain empty (getFreeSlot never targets it).
	if inv.get(45).Count > 0 {
		t.Fatalf("pickup landed in the offhand (window 45), want only storage 9-44")
	}
	// It must be somewhere in the storage range 9-44.
	found := false
	for s := int16(9); s <= 44; s++ {
		if inv.get(s).Count > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("picked-up item not found in any storage slot 9-44")
	}
}

// TestPickupPrefersSelectedHotbarSlot pins getSlotWithRemainingSpace's SELECTED-slot priority: a
// partial stack of the same item in the selected hotbar slot (window 36+heldSlot) receives the
// pickup before any other slot. Vanilla checks getItem(selected) first.
func TestPickupPrefersSelectedHotbarSlot(t *testing.T) {
	loop, mgr := newDropLoop()
	mgr.SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000
	inv := ensureInventory(p)
	inv.heldSlot = 2 // selected hotbar slot -> window 38
	// Seed a partial cobblestone stack in BOTH the selected hotbar slot and a main slot.
	inv.set(38, component.SlotData{Count: 10, ItemID: pk.VarInt(item.Cobblestone.ID)})
	inv.set(9, component.SlotData{Count: 10, ItemID: pk.VarInt(item.Cobblestone.ID)})

	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	ie.pickupDelay = 0
	loop.only().entities.add(ie)
	p.tracked = map[int32]bool{ie.id: true}

	loop.scanItemPickup(p)

	if inv.get(38).Count != 11 {
		t.Fatalf("selected hotbar slot 38 = %d, want 11 (pickup merges into selected first)", inv.get(38).Count)
	}
	if inv.get(9).Count != 10 {
		t.Fatalf("main slot 9 = %d, want 10 (untouched — selected had priority)", inv.get(9).Count)
	}
}

// TestPickupSendsSetSlot is the BUG-3 regression: after a successful pickup the server must send an
// authoritative ClientboundContainerSetSlot for the changed slot (vanilla broadcastChanges →
// synchronizeSlotToRemote), so the client always reflects the picked-up stack. The packet targets
// containerId 0 (the player inventory window).
func TestPickupSendsSetSlot(t *testing.T) {
	loop, mgr := newDropLoop()
	mgr.SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.Stone{}], dimMinY)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)
	p.entityID = 1000
	ensureInventory(p)

	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	ie.pickupDelay = 0
	loop.only().entities.add(ie)
	p.tracked = map[int32]bool{ie.id: true}

	loop.scanItemPickup(p)

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundContainerSetSlot); n < 1 {
		t.Fatalf("ClientboundContainerSetSlot emitted %d times, want >= 1 (authoritative slot update)", n)
	}
}

// totalInventoryCount sums the counts across all inventory slots — a test helper for asserting
// "the inventory grew by N" without depending on which slot received the item.
func totalInventoryCount(inv *Inventory) int {
	if inv == nil {
		return 0
	}
	total := 0
	for _, s := range inv.slots {
		if s.Count > 0 {
			total += int(s.Count)
		}
	}
	return total
}
