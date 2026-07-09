package server

// equipment_attributes.go — ITEM ATTRIBUTE MODIFIERS (divergence-audit E-1): the 1:1 port of the
// vanilla equipment→attribute pipeline that makes a held sword raise ATTACK_DAMAGE/lower
// ATTACK_SPEED and worn armor raise ARMOR/ARMOR_TOUGHNESS/KNOCKBACK_RESISTANCE. Ported
// method-for-method from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap this
// session). No GPL source is pasted — the algorithms are re-expressed in Go — but the structure
// and observable behavior are IDENTICAL.
//
// THE VANILLA CHAIN (all javap-verified this session):
//
//   - net.minecraft.world.entity.LivingEntity.tick(): after the arrow/stinger count-downs the
//     server side calls `this.detectEquipmentUpdates();` EVERY tick — equipment changes are
//     detected by a per-tick diff, NOT by hooking every inventory mutation. That is why this port
//     runs as a per-player scan in the tick pipeline (tickPlayerEquipment) rather than from the
//     dozens of container-click/creative-set/pickup/drop paths that can change a slot.
//
//   - LivingEntity.detectEquipmentUpdates():
//       Map<EquipmentSlot,ItemStack> changes = collectEquipmentChanges(this.lastEquipmentItems);
//       if (changes != null) { handleHandSwap(changes); if (!changes.isEmpty()) handleEquipmentChanges(changes); }
//
//   - LivingEntity.collectEquipmentChanges(lastEquipmentItems) — TWO passes:
//       PASS 1 (per EquipmentSlot in VALUES order): last = lastEquipmentItems.get(slot);
//         cur = getItemBySlot(slot); if (equipmentHasChanged(last, cur)) { changes.put(slot, cur);
//         if (!last.isEmpty()) stopLocationBasedEffects(last, slot, getAttributes()); }
//       PASS 2 (per changed entry): if (!cur.isEmpty() && !cur.isBroken())
//         cur.forEachModifier(slot, (holder, modifier) -> { AttributeInstance inst =
//         attributes.getInstance(holder); if (inst != null) { inst.removeModifier(modifier.id());
//         inst.addTransientModifier(modifier); } })   [lambda$collectEquipmentChanges$0]
//         + EnchantmentHelper.runLocationChangedEffects(...)  [enchant effects — E-3, cited seam]
//
//   - LivingEntity.stopLocationBasedEffects(last, slot, attributes):
//       last.forEachModifier(slot, (holder, modifier) -> { AttributeInstance inst =
//       attributes.getInstance(holder); if (inst != null) inst.removeModifier(modifier); })
//       [lambda$stopLocationBasedEffects$0 — removeModifier(AttributeModifier) removes by id]
//       + EnchantmentHelper.stopLocationBasedEffects(...)  [enchant effects — E-3, cited seam]
//
//   - LivingEntity.equipmentHasChanged(a, b) == !ItemStack.matches(a, b) — count + item +
//     component equality (slotDataEqual is the established ItemStack.matches port).
//
//   - net.minecraft.world.item.ItemStack.forEachModifier(EquipmentSlot, BiConsumer):
//       ItemAttributeModifiers c = getOrDefault(DataComponents.ATTRIBUTE_MODIFIERS, EMPTY);
//       c.forEach(slot, consumer);                       // component entries, slot-group filtered
//       EnchantmentHelper.forEachModifier(this, slot, consumer);  // enchant-driven — E-3 seam
//     ItemAttributeModifiers.forEach(slot, consumer) visits each entry whose
//     entry.slot().test(slot) accepts the equipment slot. The DEFAULT component map per item is
//     the generated data/item/attributemodifiers.go table (from the jar's items.json report). A
//     per-stack ATTRIBUTE_MODIFIERS OVERRIDE in RawComponents is a cited deferral — no survival
//     path creates one (only /give with explicit components could), so the default table is the
//     authoritative read today; the seam is itemAttributeModifiersFor.
//
//   - net.minecraft.world.entity.EquipmentSlotGroup — the per-entry slot filter (jar <clinit>):
//       ANY(any: slot -> true), MAINHAND, OFFHAND, HAND(slot.getType() == Type.HAND),
//       FEET, LEGS, CHEST, HEAD, ARMOR(slot.isArmor() == HUMANOID_ARMOR || ANIMAL_ARMOR),
//       BODY, SADDLE.  [VERIFIED javap EquipmentSlotGroup.<clinit> + lambdas + EquipmentSlot.isArmor]
//
//   - handleEquipmentChanges(changes): builds the ClientboundSetEquipmentPacket for tracking
//     players AND writes lastEquipmentItems.put(slot, stack.copy()) per changed slot. The
//     modifier bookkeeping happened entirely in collectEquipmentChanges; here we mirror the
//     lastEquipment update. The SetEquipment broadcast to tracking players is a CITED DEFERRAL
//     (player-visual only — other clients rendering your held item/armor; encodeSetEquipment
//     exists for mobs and slots in here when the player-tracker seam lands). handleHandSwap is
//     the swap-packet optimization (EntityEventPacket 55 instead of two SetEquipment) — also a
//     packet-only concern with no attribute effect, so it is covered by the same deferral.
//
//   - net.minecraft.world.entity.player.Player.tick() tail (javap offsets 228-291):
//       this.attackStrengthTicker++; this.itemSwapTicker++;
//       ItemStack main = getMainHandItem();
//       if (!ItemStack.matches(this.lastItemInMainHand, main)) {
//           if (!ItemStack.isSameItem(this.lastItemInMainHand, main)) resetAttackStrengthTicker();
//           this.lastItemInMainHand = main.copy();
//       }
//     The ticker++ lives in tickPlayerCombat (combat.go); the swap check is ported HERE
//     (playerTickHandSwap) and tickPlayerEquipment runs AFTER tickPlayerCombat in tick_phases.go
//     so the reset lands after the increment exactly as the bytecode orders it (reset→0 stays 0
//     at tick end, not 1).
//
// EFFECT ON THE EXISTING READ SITES (no formula change anywhere — the whole point of the holder):
//   - Player.attack damage = (float) getAttributeValue(ATTACK_DAMAGE) (attack_dispatch.go:130/296)
//     now folds the held weapon's base_attack_damage: diamond sword = 1.0 + 6.0 = 7.0.
//   - getCurrentItemAttackStrengthDelay = 1.0/getAttributeValue(ATTACK_SPEED)*20.0
//     (attack_dispatch.go:525) now folds base_attack_speed: sword 4.0-2.4=1.6 → 12.5 ticks.
//   - getDamageAfterArmorAbsorb reads ARMOR/ARMOR_TOUGHNESS (combat.go:531-534) — worn armor now
//     raises them (diamond chestplate: ARMOR +8, TOUGHNESS +2).
//   - knockback resistance (attack_dispatch.go:727) folds netherite armor.knockback_resistance.
//
// TICK-05: everything here is tick-owned per-player state mutated only on the tick goroutine.
// No RNG, no packets, no allocation for an unchanged (or empty) equipment set — the pig-oracle
// stream is byte-identical.

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
)

// playerEquipmentSlots are the six EquipmentSlots a player populates, in EquipmentSlot.VALUES
// declaration order (MAINHAND, OFFHAND, FEET, LEGS, CHEST, HEAD — collectEquipmentChanges' pass-1
// iteration order). BODY and SADDLE exist on the enum but never on a player (no player body/saddle
// slot), so the player diff skips them: their stacks are permanently EMPTY and equipmentHasChanged
// (EMPTY, EMPTY) is always false — skipping is behavior-identical and cheaper.
var playerEquipmentSlots = [6]int{eqSlotMainHand, eqSlotOffHand, eqSlotFeet, eqSlotLegs, eqSlotChest, eqSlotHead}

// playerEquipmentSlotCount is len(playerEquipmentSlots) — the size of tickPlayer.lastEquipment.
const playerEquipmentSlotCount = 6

// playerEquipmentIndex maps an EquipmentSlot ordinal (eqSlot*) to its index in
// playerEquipmentSlots / tickPlayer.lastEquipment. For the six player slots the ordinal IS the
// index (MAINHAND=0 … HEAD=5), kept as a named helper for readability.
func playerEquipmentIndex(slot int) int { return slot }

// playerItemBySlot is the player analogue of LivingEntity.getItemBySlot(EquipmentSlot): the stack
// currently in the given equipment slot, read from the 46-slot inventory window
// (net.minecraft.world.entity.player.Inventory.getItem via the equipment view):
// MAINHAND = the selected hotbar slot (36+heldSlot — Inventory.getSelectedItem), OFFHAND = 45,
// HEAD/CHEST/LEGS/FEET = window slots 5/6/7/8 (the worn-armor slots, durability.go's
// armorMenuSlots). A player with no inventory yet reads EMPTY everywhere (no allocation — the
// pig-oracle player never allocates an inventory from this path).
func playerItemBySlot(p *tickPlayer, slot int) component.SlotData {
	inv := p.inventory
	if inv == nil {
		return component.SlotData{}
	}
	switch slot {
	case eqSlotMainHand:
		return inv.get(heldWindowSlot(inv.heldSlot))
	case eqSlotOffHand:
		return inv.get(offhandWindowSlot)
	case eqSlotHead:
		return inv.get(5)
	case eqSlotChest:
		return inv.get(6)
	case eqSlotLegs:
		return inv.get(7)
	case eqSlotFeet:
		return inv.get(8)
	}
	return component.SlotData{} // BODY/SADDLE: no player slot — permanently EMPTY
}

// attributeKeyByResource maps an attribute resource id (the generated table's Attribute field) to
// the holder's attributeKey. It is the Go stand-in for AttributeMap.getInstance(holder): a
// resource id NOT in this map yields "no instance" and the modifier is skipped, exactly as
// vanilla's `if (inst != null)` guard skips an attribute the entity does not carry. (The one id
// the 26.2 item report uses that is absent here is minecraft:waypoint_transmit_range — the
// waypoint/locator-bar subsystem is not modeled, a cited deferral; skipping it has zero combat
// effect.)
var attributeKeyByResource = map[string]attributeKey{
	"minecraft:attack_damage":            attrAttackDamage,
	"minecraft:attack_speed":             attrAttackSpeed,
	"minecraft:attack_knockback":         attrAttackKnockback,
	"minecraft:armor":                    attrArmor,
	"minecraft:armor_toughness":          attrArmorToughness,
	"minecraft:knockback_resistance":     attrKnockbackResistance,
	"minecraft:sweeping_damage_ratio":    attrSweepingDamageRatio,
	"minecraft:max_health":               attrMaxHealth,
	"minecraft:movement_speed":           attrMovementSpeed,
	"minecraft:max_absorption":           attrMaxAbsorption,
	"minecraft:entity_interaction_range": attrEntityInteractionRange,
	"minecraft:step_height":              attrStepHeight,
	"minecraft:safe_fall_distance":       attrSafeFallDistance,
	"minecraft:mining_efficiency":       attrMiningEfficiency,
}

// equipmentSlotGroupTest ports EquipmentSlotGroup.test(EquipmentSlot) — the per-entry slot filter
// ItemAttributeModifiers.forEach applies. group is the serialized key (the generated table's Slot
// field); slot is the EquipmentSlot ordinal (eqSlot*).
//
//	[VERIFIED javap EquipmentSlotGroup.<clinit>: ANY("any", slot -> true — lambda$static$0);
//	 MAINHAND/OFFHAND/FEET/LEGS/CHEST/HEAD/BODY/SADDLE(single-slot predicates);
//	 HAND("hand", slot -> slot.getType() == Type.HAND — lambda$static$1, i.e. MAINHAND|OFFHAND);
//	 ARMOR("armor", EquipmentSlot::isArmor — type HUMANOID_ARMOR (FEET/LEGS/CHEST/HEAD) or
//	 ANIMAL_ARMOR (BODY)).]
func equipmentSlotGroupTest(group string, slot int) bool {
	switch group {
	case "any":
		return true
	case "mainhand":
		return slot == eqSlotMainHand
	case "offhand":
		return slot == eqSlotOffHand
	case "hand":
		return slot == eqSlotMainHand || slot == eqSlotOffHand
	case "feet":
		return slot == eqSlotFeet
	case "legs":
		return slot == eqSlotLegs
	case "chest":
		return slot == eqSlotChest
	case "head":
		return slot == eqSlotHead
	case "armor":
		// EquipmentSlot.isArmor(): HUMANOID_ARMOR (FEET/LEGS/CHEST/HEAD) or ANIMAL_ARMOR (BODY).
		return slot == eqSlotFeet || slot == eqSlotLegs || slot == eqSlotChest || slot == eqSlotHead || slot == eqSlotBody
	case "body":
		return slot == eqSlotBody
	case "saddle":
		return slot == eqSlotSaddle
	}
	return false // unknown group key: no slot matches (defensive; the report only emits the keys above)
}

// itemAttributeModifiersFor resolves a stack's ATTRIBUTE_MODIFIERS component — the
// `getOrDefault(DataComponents.ATTRIBUTE_MODIFIERS, ItemAttributeModifiers.EMPTY)` read of
// ItemStack.forEachModifier. Today it reads the item's DEFAULT component (the generated
// data/item/attributemodifiers.go table, id -> Item.Name -> "minecraft:<name>" like itemFood);
// a per-stack override in RawComponents is a cited deferral (see file header) and would be
// decoded here when a path that creates one exists. An unknown id / modifier-less item returns
// nil — the EMPTY component.
func itemAttributeModifiersFor(s component.SlotData) []item.ItemAttributeModifier {
	if stackEmpty(s) {
		return nil
	}
	it, ok := item.ByID[item.ID(s.ItemID)]
	if !ok {
		return nil
	}
	return item.AttributeModifiers["minecraft:"+it.Name]
}

// forEachItemModifier is the port of ItemStack.forEachModifier(EquipmentSlot, BiConsumer): visit
// every ATTRIBUTE_MODIFIERS entry whose EquipmentSlotGroup accepts the slot, in component list
// order, mapping each to (attributeKey, attribute.AttributeModifier). An entry whose attribute is
// not modeled in the holder is skipped (the vanilla `getInstance(holder) == null` guard — see
// attributeKeyByResource). The EnchantmentHelper.forEachModifier tail (enchant-driven attribute
// modifiers, e.g. a future armor enchant) is the E-3 SEAM: no enchantment carries an
// attribute-modifier effect in the current port, so it contributes nothing — it slots in here.
func forEachItemModifier(s component.SlotData, slot int, fn func(attributeKey, attribute.AttributeModifier)) {
	for _, m := range itemAttributeModifiersFor(s) {
		if !equipmentSlotGroupTest(m.Slot, slot) {
			continue
		}
		key, ok := attributeKeyByResource[m.Attribute]
		if !ok {
			continue // no AttributeInstance for this attribute on the player holder — vanilla null-guard
		}
		fn(key, attribute.AttributeModifier{
			ID:        m.ID,
			Amount:    m.Amount,
			Operation: attribute.Operation(m.Operation),
		})
	}
	// EnchantmentHelper.forEachModifier(stack, slot, consumer) — the enchant tail of
	// ItemStack.forEachModifier (E-3). Each enchantment's minecraft:attributes effect (Swift Sneak's
	// sneaking_speed, Efficiency's mining_efficiency, Sweeping Edge's sweeping_damage_ratio, ...),
	// gated on Enchantment.matchingSlot(slot), grants a per-slot AttributeModifier. The attribute
	// resource id maps through attributeKeyByResource; an id NOT modeled on the holder is skipped
	// (the vanilla getInstance(holder) == null guard) — exactly as the item-modifier loop above does.
	enchForEachAttributeModifier(s, slot, func(attributeID string, m attribute.AttributeModifier) {
		key, ok := attributeKeyByResource[attributeID]
		if !ok {
			return // no AttributeInstance for this attribute on the holder — vanilla null-guard
		}
		fn(key, m)
	})
}

// detectEquipmentUpdates is the 1:1 port of LivingEntity.detectEquipmentUpdates +
// collectEquipmentChanges + stopLocationBasedEffects + handleEquipmentChanges' lastEquipmentItems
// update, for the player (see the file header for the verified bytecode walk). Runs every tick on
// the tick goroutine. The unchanged path (every slot matches lastEquipment) is allocation-free
// and touches no holder state.
func (p *tickPlayer) detectEquipmentUpdates() {
	// PASS 1 (collectEquipmentChanges): diff each slot against lastEquipmentItems; for a changed
	// slot, drop the OLD stack's modifiers (stopLocationBasedEffects → forEachModifier →
	// AttributeInstance.removeModifier(modifier) — removal is by modifier id).
	var changed [playerEquipmentSlotCount]bool
	any := false
	for _, slot := range playerEquipmentSlots {
		i := playerEquipmentIndex(slot)
		last := p.lastEquipment[i]
		cur := playerItemBySlot(p, slot)
		// equipmentHasChanged(last, cur) == !ItemStack.matches(last, cur).
		if slotDataEqual(last, cur) {
			continue
		}
		changed[i] = true
		any = true
		if !stackEmpty(last) {
			// stopLocationBasedEffects(last, slot, attributes): remove the old stack's modifiers.
			h := p.playerAttributes()
			forEachItemModifier(last, slot, func(key attributeKey, m attribute.AttributeModifier) {
				h.removeModifier(key, m.ID)
			})
		}
	}
	if !any {
		return // collectEquipmentChanges returned null: nothing to handle
	}
	// PASS 2 (collectEquipmentChanges' changed-entry loop + handleEquipmentChanges' bookkeeping):
	// for each changed slot, attach the NEW stack's modifiers (removeModifier(id) then
	// addTransientModifier — the remove-then-add makes a same-id refresh idempotent) unless the
	// stack is empty or broken, then record the new stack as lastEquipmentItems[slot].
	for _, slot := range playerEquipmentSlots {
		i := playerEquipmentIndex(slot)
		if !changed[i] {
			continue
		}
		cur := playerItemBySlot(p, slot)
		if !stackEmpty(cur) && !stackIsBroken(cur) {
			h := p.playerAttributes()
			forEachItemModifier(cur, slot, func(key attributeKey, m attribute.AttributeModifier) {
				h.removeModifier(key, m.ID) // AttributeInstance.removeModifier(modifier.id())
				h.addModifier(key, m)       // AttributeInstance.addTransientModifier(modifier)
			})
		}
		// handleEquipmentChanges: this.lastEquipmentItems.put(slot, stack.copy()). (The
		// ClientboundSetEquipment broadcast to tracking players is the cited player-visual
		// deferral — see file header.)
		p.lastEquipment[i] = cur
	}
}

// playerTickHandSwap is the Player.tick() mainhand-swap tail (javap offsets 248-291, quoted in
// the file header): when the mainhand stack no longer matches lastItemInMainHand, reset the
// attack-strength ticker IF the item identity changed (ItemStack.isSameItem false — a component
// -only change, e.g. durability loss on the same sword, does NOT reset the cooldown), then
// remember the new stack. MUST run after tickPlayerCombat's attackStrengthTicker++ (vanilla
// increments first) so a swap-reset leaves the ticker at 0 at tick end, not 1.
func (p *tickPlayer) playerTickHandSwap() {
	main := playerItemBySlot(p, eqSlotMainHand)
	if slotDataEqual(p.lastItemInMainHand, main) { // ItemStack.matches
		return
	}
	if !isSameItem(p.lastItemInMainHand, main) {
		p.resetAttackStrengthTicker()
	}
	p.lastItemInMainHand = main // lastItemInMainHand = main.copy()
}

// tickPlayerEquipment runs the per-tick equipment scan for every connected player: the
// LivingEntity.tick detectEquipmentUpdates (item attribute modifiers follow the equipped items)
// and the Player.tick mainhand-swap cooldown reset. Ordered AFTER tickPlayerCombat in
// tick_phases.go (see playerTickHandSwap). Runs for dead players too — vanilla LivingEntity.tick
// keeps running while deathTime counts, equipment scan included. Tick goroutine only (TICK-05).
func (t *TickLoop) tickPlayerEquipment() {
	// NOTE: deliberately NOT traced — like tickPlayerCombat, this runs INSIDE the existing
	// tickEntities phase, so no new entry appears in the asserted phase order (TestTickPhaseOrder).
	for _, p := range t.players {
		if p == nil {
			continue
		}
		p.detectEquipmentUpdates()
		p.playerTickHandSwap()
	}
}
