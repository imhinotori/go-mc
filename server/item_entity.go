package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_entity.go — Plan 17-14 (ITEM-PICKUP): the dropped-item lifecycle that makes a
// ground item actually fall/settle, age out, and — the reported bug — get PICKED UP.
//
// Three ported vanilla surfaces, all decompiled from temp/cache/26.2-inner.jar this session
// and cited at each call site:
//
//	FIX C  net.minecraft.world.entity.item.ItemEntity.tick()       → tickItems / tickItem
//	FIX D  ItemEntity.playerTouch(Player) + LivingEntity.take(...)  → pickup scan + takeItem
//	       net.minecraft.world.entity.player.Inventory.add(ItemStack) → inventoryAdd
//
// SINGLE-OWNER (TICK-05): every function here runs on the tick goroutine over the tick-owned
// entityStore / tickPlayer state. The item tick mutates velocity/age/pickupDelay and moves the
// item via t.moveEntity (the bucket-consistent path); the pickup scan mutates the player
// inventory and removes the item from the store. No goroutine, no xsync/ants — pure owner work.

const (
	// itemDefaultPickupDelay is ItemEntity.setDefaultPickUpDelay()'s value: a freshly dropped
	// item is NOT pickable for 10 ticks (0.5 s). The item tick counts it down to 0.
	//   [VERIFIED javap ItemEntity.setDefaultPickUpDelay: bipush 10; putfield pickupDelay.]
	itemDefaultPickupDelay = 10

	// itemInfinitePickupDelay is ItemEntity.INFINITE_PICKUP_DELAY (32767): a sentinel the tick
	// never decrements and playerTouch treats as "never pickable". Ported for completeness so a
	// future setNeverPickUp() drop behaves vanilla-correctly.
	//   [VERIFIED javap: private static final int INFINITE_PICKUP_DELAY = 32767.]
	itemInfinitePickupDelay = 32767

	// itemLifetime is ItemEntity.LIFETIME (6000 ticks == 5 minutes): the item DESPAWNS once its
	// age reaches this. The item tick discards at age >= 6000.
	//   [VERIFIED javap: private static final int LIFETIME = 6000.]
	itemLifetime = 6000

	// itemInfiniteLifetime is ItemEntity.INFINITE_LIFETIME (-32768): the age sentinel the tick
	// never increments (an unlimited-lifetime item never despawns). Ported for completeness.
	//   [VERIFIED javap: private static final int INFINITE_LIFETIME = -32768.]
	itemInfiniteLifetime = -32768
)

const (
	// itemGravity is ItemEntity.getDefaultGravity() == 0.04 — the per-tick downward acceleration
	// a dropped item gets (HALF a living entity's 0.08). The generic tickPhysics uses 0.08, so
	// items are stepped HERE with their own 0.04 so the tossed item falls at the vanilla rate.
	//   [VERIFIED javap ItemEntity.getDefaultGravity: ldc2_w 0.04d; dreturn.]
	itemGravity = 0.04

	// itemPickupInflateXZ / itemPickupInflateY are Player.aiStep's item-collection AABB inflation:
	// getBoundingBox().inflate(1.0, 0.5, 1.0) — the player's box grown by 1 block on X/Z and 0.5
	// on Y, and any item entity intersecting it is touched (picked up if pickable). Decompiled
	// verbatim from Player.aiStep (dconst_1 / 0.5d / dconst_1 → AABB.inflate(DDD)).
	//   [VERIFIED javap Player.aiStep: getBoundingBox().inflate(1.0, 0.5, 1.0) → getEntities → touch.]
	itemPickupInflateXZ = 1.0
	itemPickupInflateY  = 0.5
)

// tickItems is the Plan 17-14 per-tick item pass: it steps every dropped Item entity in the
// tick-owned store (gravity + age + despawn) and then scans each player for nearby pickable
// items. It is wired into tickEntities (alongside tickFallDamage / tickBreath) so no new tick
// phase is added (TestTickPhaseOrder stays green). Runs on the tick goroutine.
//
// Ordering: items are TICKED first (so pickupDelay decrements and the toss settles), THEN the
// pickup scan runs — mirroring vanilla, where ItemEntity.tick() (which decrements pickupDelay)
// runs in the entity tick BEFORE Player.aiStep collects items in the same server tick.
func (t *TickLoop) tickItems() {
	// Phase-27 STEP-3 (N=2): item entities live across BOTH regions (an item dropped near the seam),
	// and tickItems runs on the COORDINATOR (quiescent — every region joined). Process each region's
	// items WITH that region registered as the current region (withRegion), so tickItem's t.only()
	// (the moveEntity re-bucket + the despawn remove) resolves to the item's OWN store rather than
	// globalRegion. The snapshot per region keeps the loop stable across an in-loop discard.
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		snapshot := make([]*Entity, 0, len(r.entities.byID))
		for _, e := range r.entities.byID {
			if e.isItem {
				snapshot = append(snapshot, e)
			}
		}
		t.withRegion(r, func() {
			for _, e := range snapshot {
				t.tickItem(e)
			}
		})
	}

	// Pickup scan AFTER the item step (vanilla: ItemEntity.tick precedes Player.aiStep's touch).
	for _, p := range t.players {
		if p == nil || p.dead {
			continue // a dead player (death screen) collects nothing
		}
		t.scanItemPickup(p)
	}
}

// tickItem ports the load-bearing body of ItemEntity.tick() (FIX C): decrement pickupDelay,
// apply the item's 0.04 gravity (then air drag + horizontal friction), integrate the velocity
// via the per-axis swept resolver so the tossed item FALLS, lands (onGround), and is blocked by
// walls, then increment age and DESPAWN at LIFETIME (6000). The two vanilla sentinels are
// honored: INFINITE_PICKUP_DELAY (32767) is never decremented; INFINITE_LIFETIME (-32768) is
// never aged. An item whose stack went empty discards immediately (ItemEntity.tick's first
// branch). Tick-owned.
//
// Vanilla bytecode (javap ItemEntity.tick, the parts that change observable state):
//
//	if getItem().isEmpty() { discard(); return }
//	super.tick()  // Entity.tick — bookkeeping only for v1
//	if pickupDelay > 0 && pickupDelay != 32767 { pickupDelay-- }
//	... applyGravity() (getDefaultGravity()==0.04) ...
//	move(SELF, getDeltaMovement()); apply air drag; if onGround multiply Δy by -0.5 (bounce) ...
//	if age != -32768 { age++ }
//	if !level.isClientSide && age >= 6000 { discard() }
func (t *TickLoop) tickItem(e *Entity) {
	// ItemEntity.tick first branch: an empty stack discards the entity (it carries nothing).
	if e.itemStack.Count <= 0 {
		t.only().entities.remove(e.id)
		return
	}

	// pickupDelay countdown (skipping the INFINITE sentinel). A fresh drop is pickable after
	// itemDefaultPickupDelay (10) ticks of this decrement.
	if e.pickupDelay > 0 && e.pickupDelay != itemInfinitePickupDelay {
		e.pickupDelay--
	}

	// applyGravity + drag: the item's OWN 0.04 gravity (NOT the 0.08 in tickPhysics), then the
	// same vertical air drag and horizontal friction the generic entity step uses so the toss
	// converges instead of sliding/accelerating forever. (v1 reuses the shared airDrag /
	// horizontalFriction tunables; vanilla's exact air-drag is item-specific but wire-irrelevant
	// — the visible behavior, a tossed item that falls and settles, is the requirement.)
	e.vy -= itemGravity
	e.vy *= airDrag
	e.vx *= horizontalFriction
	e.vz *= horizontalFriction

	// Integrate via the per-axis swept resolver (the anti-tunneling discipline shared with
	// tickPhysics). This re-buckets through entities.move and sets onGround / zeroes blocked
	// velocity so the item lands on the floor instead of falling through it.
	t.moveEntity(e, e.vx, e.vy, e.vz)

	// age++ (skipping the INFINITE_LIFETIME sentinel), then DESPAWN at LIFETIME. Removing the
	// item from the store makes the tracker emit RemoveEntities to every tracking player next
	// tick (it no longer appears in near()).
	if e.age != itemInfiniteLifetime {
		e.age++
	}
	if e.age >= itemLifetime {
		t.only().entities.remove(e.id)
	}
}

// scanItemPickup ports Player.aiStep's item-collection loop (the touch path) + ItemEntity
// .playerTouch (FIX D): build the player's pickup AABB (the collision box inflated by
// (1.0, 0.5, 1.0)), and for every Item entity whose box intersects it, attempt the vanilla
// playerTouch — pick the stack up if pickupDelay == 0 and it fits the inventory, send the
// take-item animation, and remove the now-collected item. Tick-owned.
//
// Broad phase: the store's near() returns the items in the columns around the player, so the
// scan never walks all entities; the AABB intersection is the narrow phase on that candidate
// set. trackRange columns comfortably covers the (1-block-inflated) pickup reach.
func (t *TickLoop) scanItemPickup(p *tickPlayer) {
	// Player pickup AABB: the player collision box (playerWidth × playerHeight, feet at p.y)
	// inflated by (1.0, 0.5, 1.0) — Player.aiStep's getBoundingBox().inflate(1.0, 0.5, 1.0).
	hw := playerWidth/2 + itemPickupInflateXZ
	pLoX, pHiX := p.x-hw, p.x+hw
	pLoY, pHiY := p.y-itemPickupInflateY, p.y+playerHeight+itemPickupInflateY
	pLoZ, pHiZ := p.z-hw, p.z+hw

	for _, e := range t.entitiesNearAcrossRegions(p.x, p.z, trackRange) {
		if !e.isItem {
			continue // only dropped items are collectible here
		}

		// Item entity AABB (feet-anchored, width × height centered on x/z). Narrow-phase
		// intersection test on ALL THREE axes (half-open is fine for a pickup proximity check).
		ihw := e.width / 2
		if pHiX <= e.x-ihw || e.x+ihw <= pLoX ||
			pHiY <= e.y || e.y+e.height <= pLoY ||
			pHiZ <= e.z-ihw || e.z+ihw <= pLoZ {
			continue // boxes do not overlap on some axis: not in pickup range
		}

		t.playerTouchItem(p, e)
	}
}

// playerTouchItem ports ItemEntity.playerTouch(Player) (FIX D): if the item is pickable
// (pickupDelay == 0) and the stack fits the player's inventory (Inventory.add succeeds), the
// stack is taken — the take-item animation packet is broadcast (takeItem → LivingEntity.take →
// ClientboundTakeItemEntity) and, when the whole stack was absorbed, the item entity is
// discarded. A partial pickup (inventory filled some but not all) leaves the item with its
// remaining count, exactly as vanilla's leftover ItemStack does. Tick-owned.
//
// Vanilla (javap ItemEntity.playerTouch, server side):
//
//	if pickupDelay == 0 && (target == null || target == player.getUUID()) {
//	    int count = stack.getCount();
//	    if (player.getInventory().add(stack)) {
//	        player.take(this, count);          // sends ClientboundTakeItemEntity to trackers
//	        if (stack.isEmpty()) { discard(); stack.setCount(count); }
//	        ... awardStat / onItemPickup ...
//	    }
//	}
func (t *TickLoop) playerTouchItem(p *tickPlayer, e *Entity) {
	if e.pickupDelay != 0 {
		return // not yet pickable (delay still running, or the INFINITE sentinel)
	}

	count := int(e.itemStack.Count)
	if count <= 0 {
		return // empty stack: nothing to pick up (discarded by tickItem next tick anyway)
	}

	inv := ensureInventory(p)
	// Snapshot BEFORE the add so we can diff the slots that actually changed and broadcast each
	// one (vanilla AbstractContainerMenu.broadcastChanges → synchronizeSlotToRemote per changed
	// slot). This is the authoritative slot update the client needs to render the pickup (BUG-3).
	before := inv.snapshot()

	// Inventory.add mutates the stack's Count in place to the LEFTOVER that did not fit. add
	// returns true iff it absorbed at least one item (vanilla's `count < startCount`).
	if !t.inventoryAdd(p, inv, &e.itemStack) {
		return // inventory full / nothing fit: the item stays on the ground (vanilla behavior)
	}

	// player.take(this, count): broadcast the ClientboundTakeItemEntity animation (the item
	// flies into the player), then send the authoritative ClientboundContainerSetSlot for every
	// slot the pickup changed so the client always reflects the picked-up stack (BUG-3).
	t.takeItem(p, e, count)
	t.broadcastInventoryChanges(p, inv, before)

	// If the whole stack was absorbed (leftover empty), discard the item entity now — the
	// tracker emits RemoveEntities next tick (it leaves near()). A partial pickup leaves the
	// item with its remaining count on the ground.
	if e.itemStack.Count <= 0 {
		// Phase-27 STEP-3 (N=2): remove from the item's OWNING region (it may live in either region;
		// scanItemPickup ran cross-region). owningRegion(nil) → skip (already gone — a safe no-op).
		if owner := t.owningRegion(e.id); owner != nil {
			owner.entities.remove(e.id)
		}
	}
}

// takeItem ports LivingEntity.take(Entity, int) (FIX D): broadcast ClientboundTakeItemEntity —
// the "item flies into the collector" animation — to every player tracking the item (here: the
// collecting player and any other nearby player). The packet carries (itemEntityId, collectorId,
// count). Tick-owned.
//
// Vanilla bytecode (javap LivingEntity.take): on the server, getChunkSource().sendToTrackingPlayers(
// item, new ClientboundTakeItemEntityPacket(item.getId(), this.getId(), count)).
func (t *TickLoop) takeItem(collector *tickPlayer, e *Entity, count int) {
	pkt := encodeTakeItemEntity(e.id, collector.entityID, count)
	// sendToTrackingPlayers: every player that currently TRACKS this item (p.tracked[e.id]) gets
	// the animation. The collector itself tracks the item (it was in its near() set to be picked
	// up), so its own client plays the pickup animation too. A player who never saw the item
	// (not tracking) does not need the packet.
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		if p.tracked != nil && p.tracked[e.id] {
			p.client.Send(pkt)
		}
	}
}

// inventoryAdd ports net.minecraft.world.entity.player.Inventory.add(ItemStack) (== add(-1, stack))
// for the v1 server-owned slot array (FIX D). It merges the dropped stack into existing matching
// stacks up to their max stack size, then drops any remainder into the first free slot, mutating
// stack.Count in place to the LEFTOVER and returning true iff it absorbed at least one item
// (vanilla's `stack.getCount() < startCount`). v1 items are never "damaged", so only the
// stackable branch of add(int, ItemStack) is ported (the damaged-tool single-slot branch is a
// later refinement and never reached by a block drop). Tick-owned.
//
// Vanilla add(int=-1, stack) stackable branch (javap Inventory.add):
//
//	startCount = stack.getCount();
//	do { stack.setCount(addResource(stack)); } while (!stack.isEmpty() && stack.getCount() < startCount);
//	return stack.getCount() < startCount;   // (ignoring the creative-infinite-materials path)
func (t *TickLoop) inventoryAdd(p *tickPlayer, inv *Inventory, stack *component.SlotData) bool {
	if stack.Count <= 0 {
		return false // empty stack: nothing to add (Inventory.add(int) isEmpty guard)
	}

	// Vanilla add(int, ItemStack) stackable loop (the do-while): each iteration captures the
	// current count as startCount, deposits one slot's worth via addResource (which mutates
	// stack.Count to the leftover), and repeats WHILE the stack is non-empty AND made progress
	// (count strictly dropped). The OUTERMOST startCount is what `return count < startCount`
	// compares against to report whether ANY item was absorbed.
	outerStart := int(stack.Count)
	for {
		startCount := int(stack.Count)
		stack.Count = pk.VarInt(t.addResource(inv, stack))
		if stack.Count <= 0 || int(stack.Count) >= startCount {
			break // emptied, or this pass placed nothing (no space): stop
		}
	}
	// add(int, ItemStack) returns stack.getCount() < startCount — i.e. the count dropped, so at
	// least one item was absorbed. (The creative hasInfiniteMaterials short-circuit is not a v1
	// path: survival player, finite materials.)
	return int(stack.Count) < outerStart
}

// VANILLA STORAGE-SLOT MAPPING (BUG-2). In 26.2 Inventory.items is the 36-entry
// getNonEquipmentItems() list — items[0..8] = hotbar, items[9..35] = main storage. getFreeSlot()
// and getSlotWithRemainingSpace() iterate items[0..35] ONLY; pickups never land in armor or the
// crafting grid. getItem(40) is the offhand (via EQUIPMENT_SLOT_MAPPING). [VERIFIED javap
// net.minecraft.world.entity.player.Inventory: getFreeSlot iterates items; getSlotWithRemainingSpace
// checks getItem(selected), then getItem(40), then iterates items; getItem(i<size) returns items[i].]
//
// Sulfur's inv.slots is the 46-entry WINDOW array (0=craft-result, 1-4=craft-grid, 5-8=armor,
// 9-35=main, 36-44=hotbar, 45=offhand) — the InventoryMenu slot layout. So vanilla's items-index
// space maps onto window slots as: items[0..8] (hotbar) -> window 36..44; items[9..35] (main) ->
// window 9..35; offhand (getItem(40)) -> window 45; selected (items index heldSlot) -> window
// 36+heldSlot. storageWindowSlots lists the 36 window slots that back items[0..35] IN ITEMS-INDEX
// ORDER (hotbar first, then main), so getFreeSlot / getSlotWithRemainingSpace iterate them in the
// exact vanilla order. The previous code iterated ALL 46 window slots, so pickups leaked into the
// crafting grid (slots 0-4) and armor (5-8) — the reported bug.
const (
	windowSlotOffhand = 45 // window slot for getItem(40) (offhand)
	windowHotbarFirst = 36 // window slot for items[0] (hotbar slot 0)
	windowMainFirst   = 9  // window slot for items[9] (main storage slot 0)
	windowMainCount   = 27 // items[9..35] -> window 9..35 (main storage)
	windowHotbarCount = 9  // items[0..8]  -> window 36..44 (hotbar)
)

// storageWindowSlots returns the 36 window-slot indices backing vanilla items[0..35], in
// items-index order (hotbar items[0..8] -> window 36..44, then main items[9..35] -> window 9..35).
// This is the precise iteration order of Inventory.getFreeSlot / getSlotWithRemainingSpace.
func storageWindowSlots() []int16 {
	out := make([]int16, 0, windowHotbarCount+windowMainCount)
	for i := 0; i < windowHotbarCount; i++ { // items[0..8] (hotbar)
		out = append(out, int16(windowHotbarFirst+i))
	}
	for i := 0; i < windowMainCount; i++ { // items[9..35] (main)
		out = append(out, int16(windowMainFirst+i))
	}
	return out
}

// addResource ports Inventory.addResource(ItemStack) → addResource(int, ItemStack): find a slot
// with remaining space for the stack's item (else the first free slot), deposit as much as the
// slot's max-stack-size headroom allows, and return the COUNT STILL UNPLACED. The deposited
// slot's count grows by the placed amount; a free slot is initialized to the item with the
// placed amount. Returns the full count when there is no slot at all (inventory full). Tick-owned.
//
// Vanilla (javap Inventory.addResource(int, ItemStack)):
//
//	slot = getSlotWithRemainingSpace(stack); if (slot == -1) slot = getFreeSlot();
//	if (slot == -1) return stack.getCount();
//	existing = getItem(slot); if existing.isEmpty() existing = stack.copyWithCount(0), setItem(...);
//	headroom = getMaxStackSize(existing) - existing.getCount();
//	placed = min(stack.getCount(), headroom);
//	if (placed == 0) return stack.getCount();
//	existing.grow(placed); return stack.getCount() - placed;
func (t *TickLoop) addResource(inv *Inventory, stack *component.SlotData) int {
	count := int(stack.Count)

	slot := slotWithRemainingSpace(inv, *stack)
	if slot == -1 {
		slot = freeSlot(inv)
	}
	if slot == -1 {
		return count // inventory full: nothing placed, the whole stack is unplaced
	}

	existing := inv.slots[slot]
	if existing.Count <= 0 {
		// Initialize a free slot to the item with count 0 (copyWithCount(0) + setItem), so the
		// grow below brings it to `placed`. ItemID + components mirror the dropped stack.
		existing = component.SlotData{ItemID: stack.ItemID, RawComponents: stack.RawComponents}
	}

	headroom := maxStackSize(existing) - int(existing.Count)
	placed := min(count, headroom)
	if placed == 0 {
		return count // the found slot is full at the type's max: nothing placed here
	}

	existing.Count = pk.VarInt(int(existing.Count) + placed) // ItemStack.grow(placed)
	inv.slots[slot] = existing
	return count - placed
}

// hasRemainingSpaceForItem ports Inventory.hasRemainingSpaceForItem(existing, toAdd): the existing
// slot can take more of toAdd iff it is non-empty, the same stackable item, and below its max
// stack size. v1 stacks by item id alone (component-aware stacking — e.g. enchanted tools never
// merging — is a later refinement; a block drop is a plain stackable). Tick-owned read.
//
// Vanilla (javap Inventory.hasRemainingSpaceForItem): !existing.isEmpty() &&
// ItemStack.isSameItemSameComponents(existing, toAdd) && existing.isStackable() &&
// existing.getCount() < getMaxStackSize(existing).
func hasRemainingSpaceForItem(existing, toAdd component.SlotData) bool {
	return existing.Count > 0 && existing.ItemID == toAdd.ItemID && int(existing.Count) < maxStackSize(existing)
}

// slotWithRemainingSpace ports Inventory.getSlotWithRemainingSpace(ItemStack) EXACTLY (BUG-2): it
// checks the SELECTED hotbar slot first, then the OFFHAND, then iterates items[0..35] (the 36 main
// + hotbar storage slots) IN ITEMS-INDEX ORDER. It NEVER returns a crafting (window 0-4) or armor
// (window 5-8) slot. Returns the window-slot index of the first slot with room for the item, or -1.
// Tick-owned read.
//
// Vanilla (javap Inventory.getSlotWithRemainingSpace):
//
//	if (hasRemainingSpaceForItem(getItem(selected), stack)) return selected;        // window 36+heldSlot
//	if (hasRemainingSpaceForItem(getItem(40),       stack)) return 40;              // window 45 (offhand)
//	for (i = 0; i < items.size(); i++)                                              // items[0..35]
//	    if (hasRemainingSpaceForItem(items.get(i), stack)) return i;
//	return -1;
func slotWithRemainingSpace(inv *Inventory, stack component.SlotData) int {
	// 1) selected hotbar slot (vanilla items index heldSlot -> window 36+heldSlot).
	selectedWindow := windowHotbarFirst + int(inv.heldSlot)
	if inv.heldSlot >= 0 && inv.heldSlot < windowHotbarCount &&
		hasRemainingSpaceForItem(inv.get(int16(selectedWindow)), stack) {
		return selectedWindow
	}
	// 2) offhand (vanilla getItem(40) -> Sulfur window slot 45).
	if hasRemainingSpaceForItem(inv.get(windowSlotOffhand), stack) {
		return windowSlotOffhand
	}
	// 3) items[0..35] in items-index order (hotbar 36..44, then main 9..35).
	for _, w := range storageWindowSlots() {
		if hasRemainingSpaceForItem(inv.get(w), stack) {
			return int(w)
		}
	}
	return -1
}

// freeSlot ports Inventory.getFreeSlot() EXACTLY (BUG-2): the window-slot index of the first EMPTY
// items[0..35] storage slot (in items-index order: hotbar 36..44, then main 9..35), or -1 when the
// 36 storage slots are full. It NEVER returns a crafting (0-4), armor (5-8), or offhand (45) slot —
// a pickup overflow can only land in main/hotbar storage, matching vanilla. Tick-owned read.
//
// Vanilla (javap Inventory.getFreeSlot): for (i=0; i<items.size(); i++) if (items.get(i).isEmpty())
// return i; return -1.
func freeSlot(inv *Inventory) int {
	for _, w := range storageWindowSlots() {
		if inv.get(w).Count <= 0 {
			return int(w)
		}
	}
	return -1
}

// maxStackSize ports Inventory.getMaxStackSize(ItemStack) for v1: the item registry's StackSize
// (data/item, generated from the jar — Stone/Cobblestone/etc. == 64). An unknown item id falls
// back to the vanilla default of 64 (Item.DEFAULT_MAX_STACK_SIZE). Tick-owned read.
func maxStackSize(stack component.SlotData) int {
	if it, ok := item.ByID[item.ID(stack.ItemID)]; ok && it.StackSize > 0 {
		return int(it.StackSize)
	}
	return 64
}
