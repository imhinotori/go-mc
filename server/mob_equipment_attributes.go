package server

// mob_equipment_attributes.go -- the MOB half of the equipment->attribute pipeline
// (divergence-audit E-1, mob side). A mob that holds a weapon in its mainhand must have that
// weapon's ATTRIBUTE_MODIFIERS folded into its AttributeMap, exactly as a player's held weapon is
// (server/equipment_attributes.go). This is the general vanilla seam -- a mob's mainhand weapon
// raises its ATTACK_DAMAGE (a stone_sword +4) / lowers ATTACK_SPEED -- ported method-for-method
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap this session). No GPL source is
// pasted; the algorithm is re-expressed in Go.
//
// THE VANILLA CHAIN (mob side, all javap-verified):
//   - net.minecraft.world.entity.LivingEntity.tick() calls detectEquipmentUpdates() EVERY tick for
//     a mob too (it is a LivingEntity method, not a Player method). On the tick a slot changes,
//     collectEquipmentChanges runs the SAME two-pass modifier bookkeeping the player port does:
//     PASS 1 removes the OLD stack's modifiers, PASS 2 (per changed non-empty slot)
//     cur.forEachModifier(slot, (holder, modifier) -> { AttributeInstance inst =
//     attributes.getInstance(holder); if (inst != null) { inst.removeModifier(modifier.id());
//     inst.addTransientModifier(modifier); } }).
//   - net.minecraft.world.item.ItemStack.forEachModifier(EquipmentSlot, BiConsumer): visits each
//     ATTRIBUTE_MODIFIERS component entry whose EquipmentSlotGroup accepts the slot -- the SAME
//     generated data/item/attributemodifiers.go table + equipmentSlotGroupTest the player port uses.
//
// WHY AT SPAWN (not a per-tick mob detect): vanilla applies the mainhand weapon's modifier via the
// first-tick detectEquipmentUpdates, BEFORE the mob's melee goal reads getAttributeValue(ATTACK_DAMAGE)
// in doHurtTarget. spawnWitherSkeleton sets MAINHAND = STONE_SWORD via setItemSlot, then this helper
// folds the modifier immediately -- the SAME observable result (the modifier is present before the
// first combat read) with no new per-tick scan. A mob that never swaps its mainhand (the wither
// skeleton keeps its sword for life) needs the fold ONCE, at equip time. The remove-then-add makes a
// re-application idempotent, exactly as collectEquipmentChanges' lambda does.
//
// THE PIG ORACLE IS UNTOUCHED: a pig carries no mainhand item (its MAINHAND slot is EMPTY), so
// itemAttributeModifiersFor returns nil and this helper folds nothing -- zero modifiers, zero RNG,
// zero wire bytes. It is only ever called from a per-species spawn path that equips a weapon.
//
//	[VERIFIED javap LivingEntity.detectEquipmentUpdates / collectEquipmentChanges (mob path
//	 identical to the player path); ItemStack.forEachModifier; the stone_sword ATTRIBUTE_MODIFIERS
//	 component: attack_damage +4.0 (ADD_VALUE, mainhand), attack_speed -2.4 (ADD_VALUE, mainhand).]

import (
	"strings"

	"github.com/imhinotori/sulfur/level/attribute"
)

// applyMainHandAttributeModifiers folds the mob's mainhand item's ATTRIBUTE_MODIFIERS into its
// AttributeMap -- the mob analogue of the player's detectEquipmentUpdates PASS 2 for the MAINHAND
// slot. It reads the item's default ATTRIBUTE_MODIFIERS component (itemAttributeModifiersFor -> the
// generated table), keeps only the entries whose EquipmentSlotGroup accepts MAINHAND
// (equipmentSlotGroupTest), maps each entry's attribute resource id ("minecraft:attack_damage") to
// the mob AttributeMap's registry name ("attack_damage"), and does removeModifier(id) then
// addTransientModifier -- idempotent by id, exactly as the vanilla lambda. An attribute the mob's
// supplier does not carry (GetInstance == nil) is skipped, mirroring the vanilla `if (inst != null)`
// guard. RNG-free; tick-owned (called on the spawn path, TICK-05).
func applyMainHandAttributeModifiers(e *Entity) {
	if e == nil || e.attributes == nil {
		return // no AttributeMap (an unported type) -> nothing to fold, exactly as inst==null skips
	}
	main := e.getMainHandItem()
	for _, m := range itemAttributeModifiersFor(main) {
		if !equipmentSlotGroupTest(m.Slot, eqSlotMainHand) {
			continue // the entry's EquipmentSlotGroup does not accept MAINHAND -- forEachModifier skips it
		}
		// Map "minecraft:attack_damage" -> "attack_damage" (the mob AttributeMap registry name).
		name := strings.TrimPrefix(m.Attribute, "minecraft:")
		inst := e.attributes.GetInstance(name)
		if inst == nil {
			continue // getInstance(holder) == null: the mob does not carry this attribute -- vanilla skip
		}
		inst.RemoveModifier(m.ID) // AttributeInstance.removeModifier(modifier.id())
		inst.AddTransientModifier(attribute.AttributeModifier{
			ID:        m.ID,
			Amount:    m.Amount,
			Operation: attribute.Operation(m.Operation),
		}) // AttributeInstance.addTransientModifier(modifier)
	}
}
