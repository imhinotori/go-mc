package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// entity_equipment.go — the mob EQUIPMENT layer (the held-item/armor slots), a 1:1 port of the
// vanilla LivingEntity/EntityEquipment storage. This is the SERVER-SIDE half several deferred
// features need (the skeleton's real bow ITEM in MAINHAND, zombie/skeleton armor later, the
// enderman block-carry, and the client-visible equipment metadata via ClientboundSetEquipment).
//
// STORAGE MODEL (net.minecraft.world.entity.EntityEquipment): vanilla holds the slots in a
//
//	private final EnumMap<EquipmentSlot, ItemStack> items;
//
// with get(slot) == items.getOrDefault(slot, ItemStack.EMPTY) and set(slot, stack) == items.put.
// An absent slot reads back as the EMPTY stack. We represent that EnumMap as a fixed
// [equipmentSlotCount]component.SlotData array indexed by the EquipmentSlot ORDINAL (the same
// ordinal ClientboundSetEquipmentPacket writes on the wire): the zero-value SlotData (Count==0)
// IS the EMPTY stack, so an un-populated slot reads back empty exactly as getOrDefault does. A
// mob with no equipment (the pig, the cow) carries an all-zero array — no allocation beyond the
// inline array, no RNG draw, no wire bytes: the byte-identical default the oracle pig relies on.
//
//	[VERIFIED javap EntityEquipment: `private final EnumMap<EquipmentSlot,ItemStack> items;`
//	 get -> items.getOrDefault(slot, ItemStack.EMPTY); set -> items.put(slot, stack) (returns old
//	 or EMPTY). LivingEntity.getItemBySlot(slot) -> equipment.get(slot); setItemSlot(slot,stack) ->
//	 equipment.set(slot,stack). EquipmentSlot ordinals (declaration order): MAINHAND=0, OFFHAND=1,
//	 FEET=2, LEGS=3, CHEST=4, HEAD=5, BODY=6, SADDLE=7.]
//
// TICK-05: the equipment array is tick-owned Entity game state — read/mutated ONLY on the tick
// goroutine (the spawn populate + a goal read). Like ai/attributes it is NOT part of the
// snapshot-friendly value set the async tracker copies for the MOVEMENT diff; the tracker copies
// it separately (snapshotEntity) ONLY to emit the spawn-time ClientboundSetEquipment, and the
// worker only READS the copy — no live-store alias.
//
// DEFERRED (cite-recorded, see .planning/FINAL-MILESTONE-PARITY.md Phase-A):
//   - Mob.populateDefaultEquipmentSlots for the OTHER mobs (zombie armor, the full 6-slot
//     population + the difficulty-scaled roll) and populateDefaultEquipmentEnchantments (the
//     armor-enchant RNG): only the skeleton's MAINHAND bow (an unconditional, RNG-free equip)
//     lands here as the concrete first consumer.
//   - getDropChances / DropChances + the on-death equipment drop roll (dropEquipment): the
//     storage lands; the death-drop of a held/worn item is a Phase-A item.
//   - OFFHAND/armor WIRE-out for a live equip CHANGE (detectEquipmentUpdates per-tick compare for
//     mobs): the spawn-time SetEquipment lands (a skeleton spawns visibly holding its bow); a live
//     mob-side equipment SWAP broadcast reuses the same encodeSetEquipment when a swap path exists.

// EquipmentSlot ordinals — the declaration-order index into the equipment array AND the wire slot
// byte ClientboundSetEquipmentPacket writes (EquipmentSlot.ordinal()). Names/order jar-verified
// (EquipmentSlot enum: MAINHAND, OFFHAND, FEET, LEGS, CHEST, HEAD, BODY, SADDLE).
const (
	eqSlotMainHand = 0 // EquipmentSlot.MAINHAND.ordinal()
	eqSlotOffHand  = 1 // EquipmentSlot.OFFHAND.ordinal()
	eqSlotFeet     = 2 // EquipmentSlot.FEET.ordinal()
	eqSlotLegs     = 3 // EquipmentSlot.LEGS.ordinal()
	eqSlotChest    = 4 // EquipmentSlot.CHEST.ordinal()
	eqSlotHead     = 5 // EquipmentSlot.HEAD.ordinal()
	eqSlotBody     = 6 // EquipmentSlot.BODY.ordinal()
	eqSlotSaddle   = 7 // EquipmentSlot.SADDLE.ordinal()

	equipmentSlotCount = 8 // EquipmentSlot.values().length
)

// getItemBySlot is net.minecraft.world.entity.LivingEntity.getItemBySlot(EquipmentSlot) ->
// equipment.get(slot): the stack in the slot, or the EMPTY stack (a zero-value SlotData) for an
// un-populated slot (EnumMap.getOrDefault(slot, ItemStack.EMPTY)). slot is the ordinal (equip*).
//
//	[VERIFIED javap LivingEntity.getItemBySlot: getfield equipment; EntityEquipment.get(slot);
//	 EntityEquipment.get: items.getOrDefault(slot, ItemStack.EMPTY).]
func (e *Entity) getItemBySlot(slot int) component.SlotData {
	if slot < 0 || slot >= equipmentSlotCount {
		return component.SlotData{} // defensive: an out-of-range ordinal reads EMPTY
	}
	return e.equipment[slot]
}

// setItemSlot is net.minecraft.world.entity.LivingEntity.setItemSlot(EquipmentSlot, ItemStack) ->
// equipment.set(slot, stack): store the stack in the slot. (The vanilla onEquipItem event hook +
// the enchant-effect refresh are not modeled — the storage write is the observable half a mob
// needs; the equip-sound/effect surface lands with effects.) slot is the ordinal (equip*).
//
//	[VERIFIED javap LivingEntity.setItemSlot: EntityEquipment.set(slot, stack) (+ onEquipItem
//	 event, not modeled); EntityEquipment.set: items.put(slot, stack).]
func (e *Entity) setItemSlot(slot int, stack component.SlotData) {
	if slot < 0 || slot >= equipmentSlotCount {
		return // defensive: an out-of-range ordinal is dropped (never happens for a cited slot)
	}
	e.equipment[slot] = stack
}

// getMainHandItem is LivingEntity.getMainHandItem() -> getItemBySlot(MAINHAND).
//
//	[VERIFIED javap LivingEntity.getMainHandItem: getstatic EquipmentSlot.MAINHAND; getItemBySlot.]
func (e *Entity) getMainHandItem() component.SlotData { return e.getItemBySlot(eqSlotMainHand) }

// getOffhandItem is LivingEntity.getOffhandItem() -> getItemBySlot(OFFHAND).
//
//	[VERIFIED javap LivingEntity.getOffhandItem: getstatic EquipmentSlot.OFFHAND; getItemBySlot.]
func (e *Entity) getOffhandItem() component.SlotData { return e.getItemBySlot(eqSlotOffHand) }

// isHoldingItem is net.minecraft.world.entity.LivingEntity.isHolding(Item) ->
// isHolding(stack -> stack.is(item)) -> pred.test(getMainHandItem()) || pred.test(getOffhandItem()):
// true when the given item id is held in EITHER hand. RangedBowAttackGoal.isHoldingBow() ==
// mob.isHolding(Items.BOW) reads this — so once the skeleton carries a real bow in MAINHAND, the
// goal's isHoldingBow is backed by the actual held item (not a cited constant). An EMPTY stack
// (Count==0) tests false for any concrete item (ItemStack.is on EMPTY is false).
//
//	[VERIFIED javap LivingEntity.isHolding(Item item): isHolding(stack -> stack.is(item));
//	 isHolding(Predicate): pred.test(getMainHandItem()) || pred.test(getOffhandItem()).]
func (e *Entity) isHoldingItem(itemID int32) bool {
	main := e.getMainHandItem()
	if main.Count > 0 && int32(main.ItemID) == itemID {
		return true
	}
	off := e.getOffhandItem()
	return off.Count > 0 && int32(off.ItemID) == itemID
}

// itemStackOf builds a one-count ItemStack (component.SlotData) for the given item — the Go
// analogue of `new ItemStack(Items.X)` (default count 1, no components). Used by the spawn-time
// populateDefaultEquipmentSlots (the skeleton's `new ItemStack(Items.BOW)`).
//
//	[VERIFIED javap ItemStack.<init>(ItemLike): this(item, 1) — a single item, no components.]
func itemStackOf(it item.Item) component.SlotData {
	return component.SlotData{Count: 1, ItemID: pk.VarInt(it.ID)}
}

// populateSkeletonEquipment is the port of
// net.minecraft.world.entity.monster.skeleton.AbstractSkeleton.populateDefaultEquipmentSlots:
//
//	super.populateDefaultEquipmentSlots(random, difficulty);   // Monster/Mob: no default equip
//	this.setItemSlot(EquipmentSlot.MAINHAND, new ItemStack(Items.BOW));
//
// The bow equip is UNCONDITIONAL and draws NO RNG (the base Mob.populateDefaultEquipmentSlots is a
// difficulty-gated armor roll the skeleton's super-call does nothing observable for — a bare
// skeleton wears no armor; that armor/enchant roll is the deferred Phase-A item). So this is a
// pure store-state write: MAINHAND = a single bow. Called from spawnDeclaredMob for a skeleton,
// BEFORE the store add, so the tracker's first AddEntity carries the bow in the SetEquipment.
//
//	[VERIFIED javap AbstractSkeleton.populateDefaultEquipmentSlots: invokespecial
//	 Monster.populateDefaultEquipmentSlots(random, difficulty); getstatic EquipmentSlot.MAINHAND;
//	 new ItemStack(Items.BOW); invokevirtual setItemSlot(MAINHAND, stack).]
func populateSkeletonEquipment(e *Entity) {
	e.setItemSlot(eqSlotMainHand, itemStackOf(item.Bow))
}

// equipmentSpawnPackets builds the ClientboundSetEquipment packets a newly-tracking observer needs
// to render the mob's populated equipment slots at spawn. It emits ONE single-slot SetEquipment per
// NON-EMPTY slot (in ordinal order) — the vanilla synchronizer sends the mob's full equipment on
// the first send; we send only the populated slots (an empty slot needs no packet, the client
// defaults it to EMPTY). A mob with no equipment (the pig) yields NO packets — zero wire bytes,
// the byte-identical default. Called by the tracker right after AddEntity/SetEntityData.
//
//	[VERIFIED javap ClientboundSetEquipmentPacket.write: per (slot,stack) pair -> writeByte(
//	 isLast ? ordinal : ordinal | 0x80); ItemStack.OPTIONAL_STREAM_CODEC.encode(stack). encodeSet
//	 Equipment builds the single-entry (no-continuation) form.]
func equipmentSpawnPackets(e *Entity) []pk.Packet {
	var out []pk.Packet
	for slot := 0; slot < equipmentSlotCount; slot++ {
		stack := e.equipment[slot]
		if stack.Count <= 0 {
			continue // EMPTY slot: no packet (the client defaults an unsent slot to EMPTY)
		}
		out = append(out, encodeSetEquipment(e.id, slot, stack))
	}
	return out
}
