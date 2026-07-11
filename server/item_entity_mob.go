package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_entity_mob.go — the MOB side of item pickup: the looting scan a mob runs in aiStep to walk
// its bounding box and PICK UP nearby dropped items. This is the 1:1 port of the vanilla Mob.aiStep
// "looting" block + its helpers, all decompiled from temp/cache/26.2-inner.jar this session and cited
// at each call site:
//
//	net.minecraft.world.entity.Mob.aiStep()          -> mobPickupItems (the looting scan)
//	Mob.wantsToPickUp(ServerLevel, ItemStack)         -> mobWantsToPickUp
//	Mob.canHoldItem(ItemStack)                        -> mobCanHoldItem (Fox overrides)
//	Mob.pickUpItem(ServerLevel, ItemEntity)           -> mobPickUpItem (Fox overrides)
//	Mob.equipItemIfPossible(ServerLevel, ItemStack)   -> mobEquipItemIfPossible
//	Fox.canHoldItem / Fox.pickUpItem / Fox.isConsumableFood -> the fox mouth-food hold
//
// PIG ORACLE (CRITICAL): the scan is GATED on e.canPickUpLoot, which is FALSE for every Animal (the pig
// is an Animal, Mob's ctor sets canPickUpLoot=false and no Animal flips it). A non-pickup mob NEVER
// enters mobPickupItems past the first gate -> ZERO new RNG draws -> the pig oracle's pinned per-mob
// stream is BYTE-IDENTICALLY unperturbed. Only the Fox (Fox.<init> setCanPickUpLoot(true)) runs it in
// v1. The scan / canHoldItem / pickUpItem draw NO RNG at all; the only randomness is the re-drop
// ItemEntity toss velocity, drawn from the GLOBAL math/rand (NewItemEntity), never the per-mob stream.
//
// SINGLE-OWNER (TICK-05): mobPickupItems runs on the tick goroutine from tickAI (the per-type extras
// slot, AFTER serverAiStep), over the tick-owned entity store. It mutates the mob equipment + removes
// the collected item from the store -- pure owner work, no goroutine.

// mobPickupReachXZ / mobPickupReachY are Mob.getPickupReach() == ITEM_PICKUP_REACH == Vec3i(1, 0, 1):
// the per-axis AABB inflation the looting scan grows the mob box by (1 block X/Z, 0 Y).
//
//	[VERIFIED javap Mob static init: ITEM_PICKUP_REACH = new Vec3i(1, 0, 1); getPickupReach() returns it;
//	 aiStep inflates getBoundingBox() by (reach.getX(), reach.getY(), reach.getZ()).]
const (
	mobPickupReachXZ = 1.0
	mobPickupReachY  = 0.0
)

// mobPickupItems ports the "looting" block of net.minecraft.world.entity.Mob.aiStep(): for a mob that
// canPickUpLoot(), is alive, not dead, and under the MOB_GRIEFING gamerule, scan every ItemEntity in
// getBoundingBox().inflate(getPickupReach()) (== inflate(1.0, 0.0, 1.0)); skip removed / empty-stack /
// still-delayed items; and for each the mob wantsToPickUp, run pickUpItem. Fox-only in v1 (the only
// canPickUpLoot mob). Tick-owned; called from tickAI gated on e.canPickUpLoot.
//
// Vanilla bytecode (javap Mob.aiStep, the looting branch):
//
//	if (level instanceof ServerLevel sl && canPickUpLoot() && isAlive() && !dead
//	        && sl.getGameRules().get(MOB_GRIEFING)) {
//	    Vec3i reach = getPickupReach();                                 // ITEM_PICKUP_REACH = (1,0,1)
//	    for (ItemEntity ie : level.getEntitiesOfClass(ItemEntity.class,
//	                             getBoundingBox().inflate(reach.getX(), reach.getY(), reach.getZ()))) {
//	        if (ie.isRemoved() || ie.getItem().isEmpty() || ie.hasPickUpDelay()) continue;
//	        if (wantsToPickUp(sl, ie.getItem())) pickUpItem(sl, ie);
//	    }
//	}
func (t *TickLoop) mobPickupItems(e *Entity) {
	// canPickUpLoot() && isAlive() && !dead -- the aiStep looting gate. A non-pickup mob (the pig) or a
	// dead/dying mob collects nothing. (MOB_GRIEFING is a cited constant-true: v1 has no gamerule store
	// and vanilla's default is true, so the scan runs exactly as a default-rules world; structured so a
	// real gamerule read replaces the constant later.)
	if !e.canPickUpLoot || !e.isAlive() || e.dead {
		return
	}

	// getPickupReach() == ITEM_PICKUP_REACH == Vec3i(1, 0, 1) -> getBoundingBox().inflate(1.0, 0.0, 1.0).
	// The mob box is width x height feet-anchored at (e.x, e.y, e.z); inflate grows it by the reach on
	// each axis (Y by 0).
	hw := e.width/2 + mobPickupReachXZ
	mLoX, mHiX := e.x-hw, e.x+hw
	mLoY, mHiY := e.y-mobPickupReachY, e.y+e.height+mobPickupReachY
	mLoZ, mHiZ := e.z-hw, e.z+hw

	// Broad phase: near() returns the items in the columns around the mob (the SAME store the fox goals
	// scan). pickupMergeScanChunks columns comfortably covers the 1-block-inflated reach.
	for _, ie := range t.cur().entities.near(e.x, e.z, pickupMergeScanChunks) {
		if !ie.isItem {
			continue // getEntitiesOfClass(ItemEntity.class): only dropped items
		}
		// ie.isRemoved() -> a discarded item; ie.getItem().isEmpty() -> empty stack; ie.hasPickUpDelay()
		// -> pickupDelay != 0 (still delayed OR the INFINITE sentinel). Any of these skips the item.
		if ie.itemStack.Count <= 0 || ie.pickupDelay != 0 {
			continue
		}
		// Narrow phase: AABB intersection on all three axes (item box is ihw x height feet-anchored).
		ihw := ie.width / 2
		if mHiX <= ie.x-ihw || ie.x+ihw <= mLoX ||
			mHiY <= ie.y || ie.y+ie.height <= mLoY ||
			mHiZ <= ie.z-ihw || ie.z+ihw <= mLoZ {
			continue // boxes do not overlap on some axis: not in pickup reach
		}
		// wantsToPickUp(sl, stack) -> canHoldItem(stack); on true, pickUpItem(sl, ie).
		if t.mobWantsToPickUp(e, ie.itemStack) {
			t.mobPickUpItem(e, ie)
		}
	}
}

// mobWantsToPickUp ports net.minecraft.world.entity.Mob.wantsToPickUp(ServerLevel, ItemStack) ->
// canHoldItem(stack): a mob wants an item exactly when it can hold it.
//
//	[VERIFIED javap Mob.wantsToPickUp: return canHoldItem(stack).]
func (t *TickLoop) mobWantsToPickUp(e *Entity, stack component.SlotData) bool {
	return t.mobCanHoldItem(e, stack)
}

// mobCanHoldItem ports Mob.canHoldItem(ItemStack) (== true for the base Mob) with the Fox override.
//
// Base Mob.canHoldItem: `return true;` (any item). Fox.canHoldItem:
//
//	ItemStack main = getItemBySlot(MAINHAND);
//	return main.isEmpty() || (ticksSinceEaten > 0 && isConsumableFood(stack) && !isConsumableFood(main));
//
// i.e. a fox holds an item iff its mouth is empty, OR it has been chewing a while (ticksSinceEaten>0)
// and the new item is food while what it holds is NOT -- it upgrades to the food.
//
//	[VERIFIED javap Mob.canHoldItem: iconst_1/ireturn; Fox.canHoldItem: getItemBySlot(MAINHAND).isEmpty()
//	 || (ticksSinceEaten>0 && isConsumableFood(stack) && !isConsumableFood(main)).]
func (t *TickLoop) mobCanHoldItem(e *Entity, stack component.SlotData) bool {
	if e.typ == entity.Fox.ID {
		main := e.getMainHandItem()
		if main.Count <= 0 {
			return true // main.isEmpty(): the fox's mouth is free
		}
		return e.ticksSinceEaten > 0 && mobIsConsumableFood(stack) && !mobIsConsumableFood(main)
	}
	return true // base Mob.canHoldItem
}

// mobIsConsumableFood ports Fox.isConsumableFood(ItemStack): the item has BOTH the FOOD and the
// CONSUMABLE data component. v1's item.Food map is generated from exactly the items that carry a FOOD
// component AND their Consumable data (ConsumeSeconds) -- so itemFood(id) presence is the faithful
// stand-in for `has(FOOD) && has(CONSUMABLE)`. An empty stack is not food.
//
//	[VERIFIED javap Fox.isConsumableFood: stack.has(DataComponents.FOOD) && stack.has(DataComponents.CONSUMABLE).]
func mobIsConsumableFood(stack component.SlotData) bool {
	if stack.Count <= 0 {
		return false
	}
	_, ok := itemFood(int32(stack.ItemID))
	return ok
}

// mobPickUpItem ports Mob.pickUpItem(ServerLevel, ItemEntity) with the Fox override.
//
// Base Mob.pickUpItem:
//
//	ItemStack stack = ie.getItem();
//	ItemStack equipped = equipItemIfPossible(sl, stack.copy());
//	if (!equipped.isEmpty()) {
//	    onItemPickup(ie);
//	    take(ie, equipped.getCount());
//	    stack.shrink(equipped.getCount());
//	    if (stack.isEmpty()) ie.discard();
//	}
//
// Fox.pickUpItem (holds ONE food item in its mouth, drops any current mainhand + the extra count):
//
//	ItemStack stack = ie.getItem();
//	if (canHoldItem(stack)) {
//	    int n = stack.getCount();
//	    if (n > 1) dropItemStack(stack.split(n - 1));          // keep only 1, drop the rest
//	    spitOutItem(getItemBySlot(MAINHAND));                  // spit whatever it was holding
//	    onItemPickup(ie);
//	    setItemSlot(MAINHAND, stack.split(1));                 // put 1 into the mouth
//	    setGuaranteedDrop(MAINHAND);                           // dropChance 2.0 (always drops on death)
//	    take(ie, stack.getCount());
//	    ie.discard();
//	    ticksSinceEaten = 0;
//	}
//
//	[VERIFIED javap Mob.pickUpItem + Fox.pickUpItem -- full bytecode traced.] Tick-owned.
func (t *TickLoop) mobPickUpItem(e *Entity, ie *Entity) {
	if e.typ == entity.Fox.ID {
		t.foxPickUpItem(e, ie)
		return
	}

	stack := ie.itemStack // vanilla stack (mutated in place by shrink below)
	// equipItemIfPossible(sl, stack.copy()) -- returns the sub-stack actually equipped (or EMPTY).
	equipped := t.mobEquipItemIfPossible(e, stack)
	if equipped.Count <= 0 {
		return // nothing equipped: the item stays on the ground (vanilla EMPTY return)
	}
	// onItemPickup + take: broadcast the collect animation, then shrink the ground stack by the taken
	// count; discard the item entity if the whole stack was absorbed.
	t.mobTakeItem(e, ie, int(equipped.Count))
	ie.itemStack.Count -= equipped.Count // ItemStack.shrink(equipped.getCount())
	if ie.itemStack.Count <= 0 {
		if owner := t.owningRegion(ie.id); owner != nil {
			owner.entities.remove(ie.id) // ItemEntity.discard()
		}
	}
}

// foxPickUpItem ports net.minecraft.world.entity.animal.fox.Fox.pickUpItem -- the fox holds ONE food
// item in its MAINHAND (mouth). It spits out whatever it was carrying, drops any surplus count, puts a
// single item in the mouth, marks it a guaranteed on-death drop, plays the take animation, discards the
// item entity, and resets the mouth-food chew counter. canHoldItem was already checked by the scan's
// wantsToPickUp gate (Fox.pickUpItem re-checks it -- a redundant true here).
//
//	[VERIFIED javap Fox.pickUpItem.]
func (t *TickLoop) foxPickUpItem(e *Entity, ie *Entity) {
	if !t.mobCanHoldItem(e, ie.itemStack) {
		return // Fox.pickUpItem's `if (canHoldItem(stack))` guard
	}
	count := int(ie.itemStack.Count)
	// n > 1 -> drop the surplus (stack.split(n-1)) as a fresh ItemEntity: the fox keeps only 1.
	if count > 1 {
		surplus := ie.itemStack
		surplus.Count = pk.VarInt(count - 1)
		t.foxDropItemStack(e, surplus)
	}
	// spitOutItem(getItemBySlot(MAINHAND)): drop whatever was in the mouth (EMPTY -> no drop).
	if main := e.getMainHandItem(); main.Count > 0 {
		t.foxDropItemStack(e, main)
	}
	// setItemSlot(MAINHAND, stack.split(1)): a single item goes into the mouth; setGuaranteedDrop marks
	// its drop chance 2.0 (always drops on death -- the picked-up gear is a guaranteed drop). v1 has no
	// per-slot dropChances store yet, so the guaranteed-drop MARK is a cited no-op (the on-death drop is
	// a Phase-A item, entity_equipment.go); the MOUTH HOLD (the observable gameplay) lands.
	held := component.SlotData{Count: 1, ItemID: ie.itemStack.ItemID, RawComponents: ie.itemStack.RawComponents}
	e.setItemSlot(eqSlotMainHand, held)
	t.broadcastToTrackers(e.id, encodeSetEquipment(e.id, eqSlotMainHand, held)) // render the mouth item
	// take(ie, stack.getCount()) -- after the split, stack.getCount() is the remaining 1; discard the item.
	t.mobTakeItem(e, ie, 1)
	if owner := t.owningRegion(ie.id); owner != nil {
		owner.entities.remove(ie.id) // ItemEntity.discard()
	}
	e.ticksSinceEaten = 0 // reset the mouth-food chew counter (Fox.pickUpItem tail)
}

// foxDropItemStack ports net.minecraft.world.entity.animal.fox.Fox.dropItemStack / spitOutItem's
// re-drop: spawn a fresh ItemEntity carrying the given stack at the fox's position. v1 routes it through
// the shared NewItemEntity ctor (the faithful ItemEntity constructor -- its toss velocity is drawn from
// the GLOBAL math/rand, NOT the per-mob stream, so the pig oracle is untouched) and adds it to the fox's
// owning region store.
//
//	[VERIFIED javap Fox.dropItemStack / spitOutItem: new ItemEntity(level, x, y, z, stack); level.addFreshEntity.]
func (t *TickLoop) foxDropItemStack(e *Entity, stack component.SlotData) {
	if stack.Count <= 0 {
		return
	}
	drop := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y, e.z, stack)
	t.regionForEntity(drop).entities.add(drop)
}

// mobEquipItemIfPossible ports net.minecraft.world.entity.Mob.equipItemIfPossible(ServerLevel,
// ItemStack): find the item's equipment slot, and if the mob canHoldItem it and the slot is free (or
// the item is a strict upgrade), equip it -- returning the sub-stack placed (limited by the slot's max),
// else EMPTY. In v1 this base path is only reachable by a NON-fox canPickUpLoot mob (none today -- the
// fox fully overrides pickUpItem), so it is structurally faithful but currently dormant:
//
//	EquipmentSlot slot = getEquipmentSlotForItem(stack);
//	if (!isEquippableInSlot(stack, slot)) return EMPTY;
//	ItemStack cur = getItemBySlot(slot);
//	boolean replace = canReplaceCurrentItem(stack, cur, slot);
//	if (slot.isArmor() && !replace) { slot = MAINHAND; cur = getItemBySlot(slot); replace = cur.isEmpty(); }
//	if (replace && canHoldItem(stack)) {
//	    ... dropChance armor-drop roll (nextFloat) ...
//	    ItemStack limited = slot.limit(stack);
//	    setItemSlotAndDropWhenKilled(slot, limited);
//	    persistenceRequired = true;
//	    return limited;
//	}
//	return EMPTY;
//
// v1 reduction (CITED): equipmentSlotForItem is the existing classifier stub (inventory_click.go), which
// always classifies as MAINHAND (v1 has no armor/offhand classifier), so a base mob routes every picked
// item to the MAINHAND hand slot. canReplaceCurrentItem's base-case (cur.isEmpty() -> true) is honored;
// the armor-attribute upgrade comparator + the dropChance re-drop nextFloat are cite-deferred (no v1 mob
// reaches this path -- the ONE canPickUpLoot mob is the fox, which overrides pickUpItem). The empty-slot
// equip (the observable half a future hostile needs) lands.
//
//	[VERIFIED javap Mob.equipItemIfPossible -- full bytecode traced.]
func (t *TickLoop) mobEquipItemIfPossible(e *Entity, stack component.SlotData) component.SlotData {
	if stack.Count <= 0 {
		return component.SlotData{}
	}
	_, _, isOffhand := equipmentSlotForItem(stack)
	slot := eqSlotMainHand
	if isOffhand {
		slot = eqSlotOffHand
	}
	// canReplaceCurrentItem base case: an EMPTY current slot always accepts (Mob.canReplaceCurrentItem
	// returns true for cur.isEmpty()). An occupied slot is NOT replaced here (the armor/weapon attribute
	// comparator is cite-deferred; no v1 mob reaches it).
	cur := e.getItemBySlot(slot)
	if cur.Count > 0 {
		return component.SlotData{}
	}
	if !t.mobCanHoldItem(e, stack) {
		return component.SlotData{}
	}
	// slot.limit(stack): a hand slot limits to the stack's own max; v1 equips the whole hand stack.
	placed := stack
	e.setItemSlot(slot, placed)
	t.broadcastToTrackers(e.id, encodeSetEquipment(e.id, slot, placed))
	return placed
}

// mobTakeItem ports net.minecraft.world.entity.LivingEntity.take(Entity, int) for a MOB collector:
// broadcast ClientboundTakeItemEntity (the "item flies into the collector" animation) to every player
// tracking the ITEM. The packet carries (itemEntityId, collectorId, count).
//
//	[VERIFIED javap LivingEntity.take: getChunkSource().sendToTrackingPlayers(item,
//	 new ClientboundTakeItemEntityPacket(item.getId(), this.getId(), count)).]
func (t *TickLoop) mobTakeItem(e *Entity, ie *Entity, count int) {
	// broadcastToTrackers(item.id, pkt): every player tracking the item entity gets the collect
	// animation (the SAME sendToTrackingPlayers seam the player-side takeItem uses, keyed on the item).
	t.broadcastToTrackers(ie.id, encodeTakeItemEntity(ie.id, e.id, count))
}
