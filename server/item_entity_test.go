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

// TestItemNoDoublePhysics: a dropped item run through the FULL tick pipeline (tickEntities, which
// invokes tickItem, then tickPhysics) must apply its 0.04 gravity EXACTLY ONCE — tickPhysics is the
// mob-only LivingEntity.aiStep->travel path and MUST skip non-mob entities, else the item gets a second
// gravity+drag+move pass and its trajectory diverges from vanilla. Fable audit B-C1 regression guard.
func TestItemNoDoublePhysics(t *testing.T) {
	loop, _ := newDropLoop()
	// High up over the empty (all-air) chunk so it free-falls without hitting the floor this tick.
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 200.0, 8.5, dropStack())
	ie.vx, ie.vy, ie.vz = 0, 0, 0 // cancel the random spawn toss for a deterministic single-axis check
	loop.only().entities.add(ie)

	// Expected vy after ONE item tick (tickItem): (0 - itemGravity) * airDrag. The double-physics bug
	// would then run tickPhysics's (vy - 0.08)*0.98 a second time, yielding a much larger fall.
	wantVY := (0 - itemGravity) * airDrag

	loop.tickEntities() // runs tickItem (the item's own 0.04 gravity + move)
	loop.tickPhysics()  // must SKIP the item (isItem) — no second integration

	if diff := ie.vy - wantVY; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("item vy after full tick = %v, want %v (single 0.04 gravity pass; a mismatch means tickPhysics double-integrated)", ie.vy, wantVY)
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

// --- B-A7: ItemEntity physics (buoyancy / merge / despawn / max-stack) ---------------------
//
// These gate the divergence-audit B-A7 port of net.minecraft.world.entity.item.ItemEntity.tick:
// water buoyancy (setUnderwaterMovement), same-item merge (mergeWithNeighbours/tryToMerge/merge
// respecting max stack size), gravity in air, and the age-6000 despawn. Each asserts the exact
// vanilla observable (velocity direction, combined count, entity removal).

// dropStackN is a cobblestone stack of count n (the merge tests need non-1 counts).
func dropStackN(n int) component.SlotData {
	return component.SlotData{Count: pk.VarInt(n), ItemID: pk.VarInt(item.Cobblestone.ID)}
}

// TestItemMergeSameItem: two same-item stacks within the (0.5,0,0.5) merge box combine — the
// counts sum onto the LARGER stack and the drained (smaller) item is removed from the store.
// Vanilla mergeWithNeighbours -> tryToMerge -> merge (the smaller pours into the larger).
func TestItemMergeSameItem(t *testing.T) {
	loop, _ := newDropLoop()
	// A 5-stack and a 3-stack at the same spot (well inside the 0.5-block merge box).
	big := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(5))
	small := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(3))
	big.pickupDelay, small.pickupDelay = 0, 0 // both mergable (isMergable requires pickupDelay != 32767, not 0)
	loop.only().entities.add(big)
	loop.only().entities.add(small)

	loop.mergeItemWithNeighbours(big)

	// big absorbed small: 5 + 3 = 8 on big; small emptied and discarded.
	if big.itemStack.Count != 8 {
		t.Fatalf("merged count = %d, want 8 (5 + 3 combined onto the larger stack)", big.itemStack.Count)
	}
	if _, ok := loop.only().entities.get(small.id); ok {
		t.Fatalf("the drained (smaller) item was NOT removed after merging into the larger stack")
	}
	if _, ok := loop.only().entities.get(big.id); !ok {
		t.Fatalf("the surviving (larger) item must remain in the store")
	}
}

// TestItemMergeYoungerAgeKept: merge keeps the YOUNGER age (min) and the LARGER pickupDelay (max)
// on the surviving stack. Vanilla merge(dest, ..., src): age = min(dest.age, src.age);
// pickupDelay = max(dest.pickupDelay, src.pickupDelay).
func TestItemMergeYoungerAgeKept(t *testing.T) {
	loop, _ := newDropLoop()
	big := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(5))
	small := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(3))
	big.pickupDelay, small.pickupDelay = 4, 9 // max -> 9 kept
	big.age, small.age = 100, 20              // min -> 20 kept
	loop.only().entities.add(big)
	loop.only().entities.add(small)

	loop.mergeItemWithNeighbours(big)

	if big.age != 20 {
		t.Fatalf("merged age = %d, want 20 (min of 100 and 20 — the younger age)", big.age)
	}
	if big.pickupDelay != 9 {
		t.Fatalf("merged pickupDelay = %d, want 9 (max of 4 and 9)", big.pickupDelay)
	}
}

// TestItemNoMergeDifferentItems: a cobblestone stack and a dirt stack never merge (different item
// id). areMergable -> isSameItemSameComponents is false, so both survive with their own counts.
func TestItemNoMergeDifferentItems(t *testing.T) {
	loop, _ := newDropLoop()
	cobble := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(5))
	dirt := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, component.SlotData{Count: 5, ItemID: pk.VarInt(item.Dirt.ID)})
	cobble.pickupDelay, dirt.pickupDelay = 0, 0
	loop.only().entities.add(cobble)
	loop.only().entities.add(dirt)

	loop.mergeItemWithNeighbours(cobble)

	if cobble.itemStack.Count != 5 || dirt.itemStack.Count != 5 {
		t.Fatalf("different items merged (cobble=%d dirt=%d), want both 5 (no merge across item types)", cobble.itemStack.Count, dirt.itemStack.Count)
	}
	if _, ok := loop.only().entities.get(dirt.id); !ok {
		t.Fatalf("the dirt item was removed — different items must NOT merge")
	}
}

// TestItemMergeRespectsMaxStack: two stacks whose combined count would EXCEED the max stack size
// do NOT merge at all — ItemEntity never does a partial top-off between two item entities. Vanilla
// areMergable: `if source.getCount() + destination.getCount() > getMaxStackSize() return false`, so a
// 62 + 10 pair (sum 72 > 64) is left entirely untouched, both entities surviving with their counts.
func TestItemMergeRespectsMaxStack(t *testing.T) {
	loop, _ := newDropLoop()
	dst := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(62))
	src := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(10))
	dst.pickupDelay, src.pickupDelay = 0, 0
	loop.only().entities.add(dst)
	loop.only().entities.add(src)

	loop.mergeItemWithNeighbours(dst)

	// 62 + 10 = 72 > 64: not mergable, so NEITHER count changes and BOTH entities survive.
	if dst.itemStack.Count != 62 {
		t.Fatalf("destination count = %d, want 62 (over-max sum -> no merge, unchanged)", dst.itemStack.Count)
	}
	if src.itemStack.Count != 10 {
		t.Fatalf("source count = %d, want 10 (over-max sum -> no merge, unchanged)", src.itemStack.Count)
	}
	if _, ok := loop.only().entities.get(src.id); !ok {
		t.Fatalf("the source item must NOT be removed (no merge occurred)")
	}
}

// TestItemMergeExactlyToMax: when the combined count fits EXACTLY at the max stack size, the two
// entities DO merge (sum == max is allowed: the guard is strictly `> max`). 60 + 4 -> a single 64
// stack, the drained source removed.
func TestItemMergeExactlyToMax(t *testing.T) {
	loop, _ := newDropLoop()
	dst := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(60))
	src := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStackN(4))
	dst.pickupDelay, src.pickupDelay = 0, 0
	loop.only().entities.add(dst)
	loop.only().entities.add(src)

	loop.mergeItemWithNeighbours(dst)

	if dst.itemStack.Count != 64 {
		t.Fatalf("destination count = %d, want 64 (60 + 4 == max, exact merge)", dst.itemStack.Count)
	}
	if _, ok := loop.only().entities.get(src.id); ok {
		t.Fatalf("the drained source (emptied into the 64-stack) must be removed")
	}
}

// TestItemGravityInAir: an airborne item (not in water/lava) gets its own 0.04 gravity then the
// 0.98 air drag on the vertical: vy = (0 - 0.04) * 0.98 = -0.0392 (a downward velocity).
func TestItemGravityInAir(t *testing.T) {
	loop, _ := newDropLoop()
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 200.0, 8.5, dropStack())
	ie.vx, ie.vy, ie.vz = 0, 0, 0 // cancel the random toss for a deterministic single-axis check
	loop.only().entities.add(ie)

	loop.tickItem(ie)

	want := (0 - itemGravity) * itemAirDrag
	if diff := ie.vy - want; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("airborne item vy = %v, want %v (0.04 gravity then 0.98 air drag)", ie.vy, want)
	}
	if ie.vy >= 0 {
		t.Fatalf("airborne item vy = %v, want a NEGATIVE (downward) velocity", ie.vy)
	}
}

// TestItemBuoyancyInWater: an item submerged in water (getFluidHeight > 0.1) takes the buoyancy
// branch, NOT gravity — its horizontal velocity is scaled by 0.99 and, while vy < 0.06, it gets a
// +0.0005 upward bob (so a resting item rises toward the surface). Vanilla setUnderwaterMovement.
func TestItemBuoyancyInWater(t *testing.T) {
	loop, mgr := newDropLoop()
	// Fill a 1x3x1 water column around the item so its whole AABB is submerged and getFluidHeight
	// (surface above feet) comfortably exceeds the 0.1 threshold.
	for dy := 0; dy <= 2; dy++ {
		mgr.SetBlock(pk.Position{X: 8, Y: 63 + dy, Z: 8}, waterStateID(0), dimMinY)
	}
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	ie.vx, ie.vy, ie.vz = 0.5, 0.0, 0.0 // a horizontal drift + zero vertical (below the 0.06 bob cutoff)
	loop.only().entities.add(ie)

	// Sanity: the item must actually read as submerged past 0.1, else this asserts nothing.
	if !loop.mobInWater(ie) || loop.mobFluidHeight(ie, fluidWater) <= itemFluidHeightThreshold {
		t.Fatalf("test setup: item not submerged past the 0.1 threshold (inWater=%v height=%v)", loop.mobInWater(ie), loop.mobFluidHeight(ie, fluidWater))
	}

	loop.tickItem(ie)

	// vy took the +0.0005 bob (buoyancy), NOT the -0.04 gravity: it must be a POSITIVE (upward)
	// velocity — the item floats up rather than sinking. (Any downward-gravity path would be < 0.)
	if ie.vy <= 0 {
		t.Fatalf("submerged item vy = %v, want > 0 (the +0.0005 buoyancy bob, not gravity)", ie.vy)
	}
	// The horizontal velocity was scaled by the 0.99 water drag (then integrated), so it is
	// strictly less than its starting 0.5 and still positive.
	if !(ie.vx > 0 && ie.vx < 0.5) {
		t.Fatalf("submerged item vx = %v, want 0 < vx < 0.5 (scaled by the 0.99 water drag)", ie.vx)
	}
}

// TestItemDespawnsAtLifetime: the item tick increments age and DISCARDS the item once age reaches
// LIFETIME (6000). Driven to one tick short, the next tick removes it from the store.
func TestItemDespawnsAtLifetime(t *testing.T) {
	loop, _ := newDropLoop()
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 200.0, 8.5, dropStack())
	ie.age = itemLifetime - 1
	loop.only().entities.add(ie)

	loop.tickItem(ie) // age -> 6000, age >= LIFETIME -> discard
	if _, ok := loop.only().entities.get(ie.id); ok {
		t.Fatalf("item still present at age %d, want despawned at LIFETIME %d", ie.age, itemLifetime)
	}
}

// TestItemFireImmuneStub pins the fireproof-immunity port: a normal stack is NOT fire-immune
// (defers to the base Entity.fireImmune == false); the fire-resistant read is the cited stub.
func TestItemFireImmuneStub(t *testing.T) {
	loop, _ := newDropLoop()
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64.0, 8.5, dropStack())
	if loop.itemFireImmune(ie) {
		t.Fatalf("a normal (non-fire-resistant) item must NOT be fire-immune (defers to base false)")
	}
}
