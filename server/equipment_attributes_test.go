package server

// equipment_attributes_test.go — pins the E-1 item-attribute-modifier port (equipment_attributes.go):
// a held diamond sword raises ATTACK_DAMAGE to 7.0 and drops ATTACK_SPEED to 1.6 (cooldown 12.5
// ticks), worn armor raises ARMOR/ARMOR_TOUGHNESS/KNOCKBACK_RESISTANCE so getDamageAfterArmorAbsorb
// reduces incoming damage, and the modifiers FOLLOW the equipment (unequip restores the base,
// a held-slot switch swaps them and resets the attack-strength ticker — the Player.tick
// lastItemInMainHand check).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// setWindowItem places a plain (component-free) stack in a player-inventory window slot.
func setWindowItem(p *tickPlayer, slot int16, it item.Item) {
	inv := ensureInventory(p)
	inv.set(slot, component.SlotData{Count: 1, ItemID: pk.VarInt(it.ID)})
}

// TestDiamondSwordAttributeModifiers: holding a diamond sword folds base_attack_damage +6.0 into
// ATTACK_DAMAGE (1.0 -> 7.0) and base_attack_speed -2.4 into ATTACK_SPEED (4.0 -> 1.6, so
// getCurrentItemAttackStrengthDelay == 12.5 ticks); clearing the hand restores the bases (5-tick
// fist cooldown). Re-running the scan with unchanged equipment is a stable no-op.
func TestDiamondSwordAttributeModifiers(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	// Bare hand: the player defaults.
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 1.0 {
		t.Fatalf("bare-hand ATTACK_DAMAGE = %v, want 1.0", got)
	}
	if got := p.getCurrentItemAttackStrengthDelay(); got != 5.0 {
		t.Fatalf("bare-hand attack delay = %v, want 5.0", got)
	}

	// Equip the sword (detectEquipmentUpdates picks it up on the next scan).
	setHeldItem(p, item.DiamondSword.ID, 1)
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 7.0 {
		t.Fatalf("diamond-sword ATTACK_DAMAGE = %v, want 7.0 (base 1.0 + base_attack_damage 6.0)", got)
	}
	// ATTACK_SPEED = 4.0 + (-2.4000000953674316) — the float-widened jar double.
	if got := p.getAttributeValue(attrAttackSpeed); math.Abs(got-1.6) > 1e-6 {
		t.Fatalf("diamond-sword ATTACK_SPEED = %v, want ~1.6", got)
	}
	if got := p.getCurrentItemAttackStrengthDelay(); math.Abs(float64(got)-12.5) > 1e-4 {
		t.Fatalf("diamond-sword attack delay = %v, want ~12.5 ticks", got)
	}

	// Unchanged equipment: the scan is a stable no-op (same values, nothing re-applied).
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 7.0 {
		t.Fatalf("re-scan ATTACK_DAMAGE = %v, want 7.0 (idempotent)", got)
	}

	// Unequip: modifiers removed, bases restored.
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{})
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 1.0 {
		t.Fatalf("after unequip ATTACK_DAMAGE = %v, want 1.0", got)
	}
	if got := p.getCurrentItemAttackStrengthDelay(); got != 5.0 {
		t.Fatalf("after unequip attack delay = %v, want 5.0", got)
	}
}

// TestDiamondSwordHitDealsSeven: a fully-charged sword swing at a pig deals exactly 7.0 (the
// ATTACK_DAMAGE attribute × baseDamageScaleFactor 1.0, no crit at rest), where the identical
// bare-hand swing deals exactly 1.0 — the E-1 divergence this port closes.
func TestDiamondSwordHitDealsSeven(t *testing.T) {
	// Bare hand baseline: 1.0.
	{
		loop, _ := newN2Loop(t)
		attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
		loop.tickPlayerEquipment()
		attacker.attackStrengthTicker = 100 // fully charged for any weapon delay
		mob := newDamageRegionMob(loop, 1, 8.5, 64, 8.0, 20.0)
		loop.handleAttack(attacker, attackPacket(mob.id))
		if dealt := float32(20.0) - mob.health; dealt != 1.0 {
			t.Fatalf("bare-hand hit dealt %v, want 1.0", dealt)
		}
	}
	// Diamond sword: 7.0. NOTE the charge is set AFTER the equipment scan — equipping a new
	// weapon RESETS the attack-strength ticker (the Player.tick lastItemInMainHand swap check),
	// exactly as vanilla's weapon-swap cooldown does.
	{
		loop, _ := newN2Loop(t)
		attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
		setHeldItem(attacker, item.DiamondSword.ID, 1)
		loop.tickPlayerEquipment()          // the per-tick equipment scan applies the sword's modifiers
		attacker.attackStrengthTicker = 100 // fully charged (the equip swap reset it to 0)
		mob := newDamageRegionMob(loop, 1, 8.5, 64, 8.0, 20.0)
		loop.handleAttack(attacker, attackPacket(mob.id))
		if dealt := float32(20.0) - mob.health; dealt != 7.0 {
			t.Fatalf("diamond-sword hit dealt %v, want 7.0 (ATTACK_DAMAGE 7.0 × scale 1.0)", dealt)
		}
	}
}

// TestArmorAttributesReduceDamage: worn diamond armor raises the ARMOR/ARMOR_TOUGHNESS attributes
// (chestplate: +8/+2; netherite helmet also +0.1 KNOCKBACK_RESISTANCE) and an incoming melee hit
// is reduced through the already-ported CombatRules curve.
func TestArmorAttributesReduceDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)
	setWindowItem(victim, 6, item.DiamondChestplate) // window slot 6 == CHEST
	setWindowItem(victim, 5, item.NetheriteHelmet)   // window slot 5 == HEAD
	loop.tickPlayerEquipment()

	if got := victim.getAttributeValue(attrArmor); got != 11.0 {
		t.Fatalf("ARMOR = %v, want 11.0 (chestplate 8.0 + netherite helmet 3.0)", got)
	}
	if got := victim.getAttributeValue(attrArmorToughness); got != 5.0 {
		t.Fatalf("ARMOR_TOUGHNESS = %v, want 5.0 (chestplate 2.0 + netherite helmet 3.0)", got)
	}
	if got := victim.getAttributeValue(attrKnockbackResistance); math.Abs(got-0.1) > 1e-6 {
		t.Fatalf("KNOCKBACK_RESISTANCE = %v, want ~0.1 (netherite helmet)", got)
	}

	// A charged bare-hand hit lands through the armor curve: exactly
	// CombatRules.getDamageAfterAbsorb(1.0, armor=11, toughness=5) instead of the full 1.0.
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	want := combatRulesGetDamageAfterAbsorb(1.0, 11.0, 5.0)
	if want >= 1.0 || want <= 0.0 {
		t.Fatalf("test precondition: armor curve must reduce 1.0 to (0,1), got %v", want)
	}
	// Compare the resulting health directly (health = maxHealth - absorbed amount in float32) —
	// re-deriving `dealt = maxHealth - health` would round through a second subtraction (1 ULP).
	if wantHealth := float32(maxHealth) - want; victim.health != wantHealth {
		t.Fatalf("armored victim health = %v, want %v (armor-absorbed fist hit of %v)", victim.health, wantHealth, want)
	}
}

// TestHeldSlotSwitchSwapsModifiersAndResetsCooldown: switching the selected hotbar slot away from
// the sword removes its modifiers on the next scan AND resets the attack-strength ticker (the
// Player.tick lastItemInMainHand / ItemStack.isSameItem check), exactly like vanilla's weapon-swap
// cooldown. A count-only change of the SAME item (matches false, isSameItem true) must NOT reset.
func TestHeldSlotSwitchSwapsModifiersAndResetsCooldown(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	inv := ensureInventory(p)
	inv.heldSlot = 0
	setHeldItem(p, item.DiamondSword.ID, 1) // sword in hotbar slot 0
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 7.0 {
		t.Fatalf("slot-0 sword ATTACK_DAMAGE = %v, want 7.0", got)
	}

	// Switch to (empty) hotbar slot 1 — the ServerboundSetCarriedItem effect.
	p.attackStrengthTicker = 42
	inv.heldSlot = 1
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 1.0 {
		t.Fatalf("after slot switch ATTACK_DAMAGE = %v, want 1.0 (sword no longer in hand)", got)
	}
	if p.attackStrengthTicker != 0 {
		t.Fatalf("after item swap attackStrengthTicker = %d, want 0 (resetAttackStrengthTicker)", p.attackStrengthTicker)
	}

	// Back to the sword: modifiers re-apply, and the swap resets the ticker again.
	p.attackStrengthTicker = 42
	inv.heldSlot = 0
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 7.0 {
		t.Fatalf("back on slot 0 ATTACK_DAMAGE = %v, want 7.0", got)
	}
	if p.attackStrengthTicker != 0 {
		t.Fatalf("after swap back attackStrengthTicker = %d, want 0", p.attackStrengthTicker)
	}

	// SAME item, count-only change: ItemStack.matches false but isSameItem true -> NO reset.
	p.attackStrengthTicker = 42
	setHeldItem(p, item.DiamondSword.ID, 2)
	loop.tickPlayerEquipment()
	if p.attackStrengthTicker != 42 {
		t.Fatalf("count-only change reset attackStrengthTicker to %d, want 42 (isSameItem true)", p.attackStrengthTicker)
	}
}

// TestOffhandSwordGivesNoAttackDamage: a sword in the OFFHAND contributes nothing — its
// base_attack_damage entry is slot-group MAINHAND and EquipmentSlotGroup.MAINHAND.test(OFFHAND)
// is false (the vanilla slot filter).
func TestOffhandSwordGivesNoAttackDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	setWindowItem(p, offhandWindowSlot, item.DiamondSword)
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrAttackDamage); got != 1.0 {
		t.Fatalf("offhand sword ATTACK_DAMAGE = %v, want 1.0 (mainhand-only modifier)", got)
	}
	if got := p.getAttributeValue(attrAttackSpeed); got != 4.0 {
		t.Fatalf("offhand sword ATTACK_SPEED = %v, want 4.0 (mainhand-only modifier)", got)
	}
}

// TestArmorInWrongSlotGivesNothing: a chestplate stored in the HEAD window slot contributes
// nothing — its entries are slot-group CHEST and CHEST.test(HEAD) is false. (The server is
// authoritative: even if a client forged the placement, the attribute must not apply.)
func TestArmorInWrongSlotGivesNothing(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	setWindowItem(p, 5, item.DiamondChestplate) // HEAD slot holds a CHEST-group item
	loop.tickPlayerEquipment()
	if got := p.getAttributeValue(attrArmor); got != 0.0 {
		t.Fatalf("chestplate-in-head-slot ARMOR = %v, want 0.0 (slot-group filter)", got)
	}
}
