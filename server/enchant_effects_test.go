package server

// enchant_effects_test.go — the E-3 enchantment-effect RUNTIME pinned against the jar-verified
// formulas (each cited at its implementation site in enchant_effects.go):
//   - Sharpness V melee bonus: +（1.0 + 0.5*(lvl-1)) = +3.0 through the FULL Player.attack path.
//   - Smite V: +（2.5*lvl) = +12.5 ONLY vs #minecraft:sensitive_to_smite (a zombie), never a pig.
//   - Protection IV: EPF 4 -> CombatRules.getDamageAfterMagicAbsorb 10*(1-4/25) = 8.4.
//   - Knockback II: LivingEntity.getKnockback = (ATTACK_KNOCKBACK 0 + 2)/2 = 1.0.
//   - Fire Aspect II: victim.igniteForSeconds(4+4*(lvl-1) = 8s) -> remainingFireTicks 160.
//   - Thorns III: random_chance 0.15*lvl gates a 1..5 reflect + 2 armor durability.
//   - Mending: ExperienceOrb.repairPlayerItems — value*2 durability, int-division remainder to XP.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/world/levelgen"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// enchEntry builds a wire EnchantmentEntry for a named enchantment at a level.
func enchTestEntry(t *testing.T, name string, level int) component.EnchantmentEntry {
	t.Helper()
	id := enchantWireID(name)
	if id < 0 {
		t.Fatalf("enchantment %q not found in the wire registry order", name)
	}
	return component.EnchantmentEntry{ID: pk.VarInt(id), Level: pk.VarInt(level)}
}

// holdEnchanted puts an enchanted 1-count stack of the given item id in the player's selected
// hotbar slot (the mainhand / getWeaponItem read).
func holdEnchanted(p *tickPlayer, stack component.SlotData) {
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), stack)
}

// TestEnchantSharpnessMeleeBonus: a fully-charged swing with a Sharpness V weapon deals
// base(1.0)*scale(1.0) + (modifyDamage(1+[1.0+0.5*4]) - 1)*scale = 1.0 + 3.0 = 4.0 — the
// Player.attack enchBonus term through the FULL dispatch path (E-3 seam A).
func TestEnchantSharpnessMeleeBonus(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)
	holdEnchanted(attacker, enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:sharpness", 5)))

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	// ATTACK_DAMAGE stays 1.0 (no equipment-attribute tick ran), so the hit isolates the enchant
	// term: total = 1.0 + (4.0 - 1.0)*1.0 = 4.0.
	want := float32(maxHealth) - 4.0
	if victim.health != want {
		t.Fatalf("sharpness V victim health = %v, want %v (4.0 damage: 1.0 base + 3.0 enchant)", victim.health, want)
	}
}

// TestEnchantSmiteEntityTypeGate: Smite's DAMAGE effect carries the entity_properties
// #minecraft:sensitive_to_smite condition — +2.5*lvl vs a zombie (undead), NOTHING vs a pig.
func TestEnchantSmiteEntityTypeGate(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	weapon := enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:smite", 5))
	src := damageSourcePlayerAttack(1)

	zombie := NewEntity(50, entity.Zombie, 0, 64, 0)
	got := loop.enchModifyDamage(weapon, enchEntityRef{mob: zombie}, src, 1.0)
	if got != 13.5 {
		t.Fatalf("smite V vs zombie modifyDamage = %v, want 13.5 (1.0 + 2.5+2.5*4)", got)
	}

	pig := NewEntity(51, entity.Pig, 0, 64, 0)
	got = loop.enchModifyDamage(weapon, enchEntityRef{mob: pig}, src, 1.0)
	if got != 1.0 {
		t.Fatalf("smite V vs pig modifyDamage = %v, want 1.0 (pig is not sensitive_to_smite)", got)
	}
}

// TestEnchantProtectionReducesDamage: a Protection IV piece in the chest slot sums EPF 4 across
// runIterationOnEquipment; getDamageAfterMagicAbsorb applies CombatRules.getDamageAfterMagicAbsorb:
// 10 * (1 - clamp(4,0,20)/25) = 8.4. Asserted through the FULL applyDamage pipeline (the armor
// curve passes 10 through unchanged — the enchanted stack carries no ARMOR attribute).
func TestEnchantProtectionReducesDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	victim := combatPlayer(loop, 1)
	inv := ensureInventory(victim)
	// A damage-less "chestplate" carrying Protection IV in the worn-chest window slot (6). The
	// EQUIPPABLE/armor-attribute components are deliberately absent so ONLY the enchant reduces.
	inv.set(6, enchantedStack(idBook, 1, enchTestEntry(t, "minecraft:protection", 4)))

	loop.applyDamage(victim, damageSourcePlayerAttack(99), 10.0)

	want := float32(maxHealth) - 8.4
	if !approxEq(victim.health, want, 1e-4) {
		t.Fatalf("protection IV victim health = %v, want %v (10 * (1 - 4/25) = 8.4)", victim.health, want)
	}
}

// TestEnchantProtectionSlotGate: Protection's definition slots are ["armor"] — the SAME enchanted
// stack held in the MAINHAND contributes NO EPF (Enchantment.matchingSlot fails for a hand slot).
func TestEnchantProtectionSlotGate(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	victim := combatPlayer(loop, 1)
	holdEnchanted(victim, enchantedStack(idBook, 1, enchTestEntry(t, "minecraft:protection", 4)))

	loop.applyDamage(victim, damageSourcePlayerAttack(99), 10.0)

	want := float32(maxHealth) - 10.0
	if victim.health != want {
		t.Fatalf("mainhand protection victim health = %v, want %v (armor-slot enchant never counts in hand)", victim.health, want)
	}
}

// TestEnchantKnockbackStrength: getKnockback = (ATTACK_KNOCKBACK 0.0 folded through the KNOCKBACK
// effect: +1.0+1.0*(lvl-1)) / 2.0F -> Knockback II = (0+2)/2 = 1.0 extra strength.
func TestEnchantKnockbackStrength(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := combatPlayer(loop, 1)
	victim := combatPlayer(loop, 2)
	holdEnchanted(attacker, enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:knockback", 2)))

	got := loop.getKnockback(attacker, enchEntityRef{player: victim}, damageSourcePlayerAttack(attacker.entityID))
	if got != 1.0 {
		t.Fatalf("knockback II getKnockback = %v, want 1.0 ((0 + 2) / 2)", got)
	}

	// A bare hand stays 0.0 — the pre-E-3 value.
	bare := combatPlayer(loop, 3)
	if got := loop.getKnockback(bare, enchEntityRef{player: victim}, damageSourcePlayerAttack(bare.entityID)); got != 0.0 {
		t.Fatalf("bare-hand getKnockback = %v, want 0.0", got)
	}
}

// TestEnchantFireAspectIgnites: a landed Fire Aspect II hit ignites the MOB victim for
// 4.0 + 4.0*(lvl-1) = 8 seconds -> remainingFireTicks = 160 (igniteForSeconds floor(8*20)),
// through the FULL handleAttack -> handleMobAttack -> doPostAttackEffectsWithItemSource path.
func TestEnchantFireAspectIgnites(t *testing.T) {
	loop, _ := newN2Loop(t)
	attacker := placeAttackPlayer(loop, 1000, 8.0, 64, 8.0)
	charge(attacker)
	holdEnchanted(attacker, enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:fire_aspect", 2)))
	mob := newDamageRegionMob(loop, 1, 8.5, 64, 8.0, 20.0) // same region, in reach

	loop.handleAttack(attacker, attackPacket(mob.id))

	if mob.remainingFireTicks != 160 {
		t.Fatalf("fire aspect II mob remainingFireTicks = %d, want 160 (8s * 20)", mob.remainingFireTicks)
	}
}

// TestEnchantThornsReflects: a mob melee hit on a player wearing a Thorns III chestplate rolls
// random_chance(0.15*3 = 0.45) off the LEVEL random; on success the attacker takes
// Mth.randomBetween(attackerRandom, 1.0, 5.0) thorns damage AND the armor piece loses 2
// durability (change_item_damage). The level random is seeded so the roll passes.
func TestEnchantThornsReflects(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	// Seed the region levelRandom with a seed whose FIRST nextFloat is < 0.45 (the chance draw).
	// Small CONSECUTIVE Java-LCG seeds all open near 0.7309 (the scrambled-seed correlation), so
	// the scan strides the seed space.
	seed := int64(-1)
	for s := int64(0); s < 1000; s++ {
		cand := s * 2654435761
		if levelgen.NewLegacyRandomSource(cand).NextFloat() < 0.45 {
			seed = cand
			break
		}
	}
	if seed < 0 {
		t.Fatal("no strided seed with nextFloat < 0.45 — impossible")
	}
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	victim := combatPlayer(loop, 1)
	inv := ensureInventory(victim)
	chest := damageableStack(idBook, 1, 100, 0, enchTestEntry(t, "minecraft:thorns", 3))
	inv.set(6, chest) // the worn-chest window slot

	mob := NewEntity(60, entity.Zombie, 0.5, 64, 0.5)
	mob.health = 20.0
	loop.only().entities.add(mob)

	g := &meleeAttackGoal{}
	g.doHurtTarget(loop, mob, victim)

	reflected := 20.0 - mob.health
	if reflected < 1.0 || reflected > 5.0 {
		t.Fatalf("thorns reflected %v damage to the attacker, want within [1.0, 5.0] (randomBetween)", reflected)
	}
	if got := stackDamageValue(inv.get(6)); got != 2 {
		t.Fatalf("thorns chestplate durability damage = %d, want 2 (change_item_damage amount 2.0)", got)
	}
	// The mob's lastDamageSource must be the thorns type attributed to the WEARER.
	if !mob.hasLastDamage || mob.lastDamageSource.typeTag != damageTypeThorns {
		t.Fatalf("thorns damage source = %+v (has=%v), want type minecraft:thorns", mob.lastDamageSource, mob.hasLastDamage)
	}
	if mob.lastDamageSource.attacker != victim.entityID {
		t.Fatalf("thorns causingEntity = %d, want the wearer %d", mob.lastDamageSource.attacker, victim.entityID)
	}
}

// TestEnchantThornsChanceFails: with a level random whose first draw is >= 0.45 the thorns roll
// fails — no reflect, no armor wear (the random_chance gate).
func TestEnchantThornsChanceFails(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	seed := int64(-1)
	for s := int64(0); s < 1000; s++ {
		if levelgen.NewLegacyRandomSource(s).NextFloat() >= 0.45 {
			seed = s
			break
		}
	}
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	victim := combatPlayer(loop, 1)
	inv := ensureInventory(victim)
	inv.set(6, damageableStack(idBook, 1, 100, 0, enchTestEntry(t, "minecraft:thorns", 3)))

	mob := NewEntity(61, entity.Zombie, 0.5, 64, 0.5)
	mob.health = 20.0
	loop.only().entities.add(mob)

	g := &meleeAttackGoal{}
	g.doHurtTarget(loop, mob, victim)

	if mob.health != 20.0 {
		t.Fatalf("failed thorns roll still reflected: mob health %v, want 20.0", mob.health)
	}
	if got := stackDamageValue(inv.get(6)); got != 0 {
		t.Fatalf("failed thorns roll still wore armor: durability damage %d, want 0", got)
	}
}

// TestEnchantMendingRepairsFromOrb: ExperienceOrb.repairPlayerItems with value 5 on a mending item
// at damage 6/100: toRepair = 5*2 = 10, repaired = min(10, 6) = 6, remainder = 5 - 6*5/10 = 2
// (int division), the recursion finds no further damaged mending item -> 2 XP left over; the item
// is fully repaired.
func TestEnchantMendingRepairsFromOrb(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	holdEnchanted(p, damageableStack(idDiamondSword, 1, 100, 6, enchTestEntry(t, "minecraft:mending", 1)))

	remaining := loop.repairPlayerItems(p, 5)

	if remaining != 2 {
		t.Fatalf("mending remaining XP = %d, want 2 (5 - 6*5/10)", remaining)
	}
	inv := ensureInventory(p)
	if got := stackDamageValue(inv.get(heldWindowSlot(inv.heldSlot))); got != 0 {
		t.Fatalf("mending item damage after repair = %d, want 0", got)
	}
}

// TestEnchantMendingNoCandidatePassthrough: with no damaged mending item the whole orb value is
// returned (the pre-E-3 path), drawing NO RNG.
func TestEnchantMendingNoCandidatePassthrough(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	if got := loop.repairPlayerItems(p, 7); got != 7 {
		t.Fatalf("no-mending repairPlayerItems = %d, want 7 (full pass-through)", got)
	}
}

// TestBaneOfArthropodsAppliesSlowness: a direct hit with a Bane-of-Arthropods weapon on an arthropod
// victim (spider) runs the apply_mob_effect post-attack effect (enchanted "attacker", affected
// "victim", gated on #sensitive_to_bane_of_arthropods AND is_direct). For Bane I the durations are
// min=1.5, max=linear(1.5,0.5)@lvl1=1.5 and amplifiers min=max=3.0, so Mth.randomBetween collapses to
// the endpoint regardless of the nextFloat draws: dur = round(1.5*20) = 30 ticks, amp = round(3.0) = 3.
// The single-element to_apply (minecraft:slowness) is picked via getRandomElement's nextInt(1) = 0.
func TestBaneOfArthropodsAppliesSlowness(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	weapon := enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:bane_of_arthropods", 1))

	attacker := combatPlayer(loop, 1)
	holdEnchanted(attacker, weapon)
	spider := NewEntity(70, entity.Spider, 0.5, 64, 0.5)
	spider.health = 16.0
	loop.only().entities.add(spider)

	// doPostAttackEffectsWithItemSource(victim=spider, src=direct player attack, weapon, attacker):
	// the ATTACKER's weapon POST_ATTACK entry (enchanted "attacker") targets the "victim".
	src := damageSourcePlayerAttack(attacker.entityID)
	_, weaponInUse := loop.enchWeaponInUse(enchEntityRef{player: attacker})
	loop.doPostAttackEffectsWithItemSource(enchEntityRef{mob: spider}, src, weapon, weaponInUse, enchEntityRef{player: attacker})

	amp, ok := entityEffectAmplifier(spider, effectSlowness)
	if !ok {
		t.Fatalf("bane on hit did not apply SLOWNESS to the arthropod victim")
	}
	if amp != 3 {
		t.Fatalf("bane SLOWNESS amplifier = %d, want 3 (min==max==3.0 -> round(3.0))", amp)
	}
	if got := spider.mobEffects[effectSlowness].duration; got != 30 {
		t.Fatalf("bane SLOWNESS duration = %d ticks, want 30 (round(1.5s * 20))", got)
	}
}

// TestBaneOfArthropodsNonArthropodNoEffect: the SAME weapon hitting a non-arthropod (pig) applies
// NOTHING — the entity_properties #sensitive_to_bane_of_arthropods requirement fails.
func TestBaneOfArthropodsNonArthropodNoEffect(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	weapon := enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:bane_of_arthropods", 1))

	attacker := combatPlayer(loop, 1)
	pig := NewEntity(71, entity.Pig, 0.5, 64, 0.5)
	pig.health = 10.0
	loop.only().entities.add(pig)

	src := damageSourcePlayerAttack(attacker.entityID)
	_, weaponInUse := loop.enchWeaponInUse(enchEntityRef{player: attacker})
	loop.doPostAttackEffectsWithItemSource(enchEntityRef{mob: pig}, src, weapon, weaponInUse, enchEntityRef{player: attacker})

	if _, ok := entityEffectAmplifier(pig, effectSlowness); ok {
		t.Fatalf("bane applied SLOWNESS to a non-arthropod (pig) — the sensitivity gate failed")
	}
}

// TestBreachReducesArmorEffectiveness: Breach IV's armor_effectiveness effect (add linear -0.15
// -0.15/level = -0.60 at IV) lowers the CombatRules armor ratio f3, so MORE damage penetrates. With
// armor=20, toughness=0, damage=10: f3 = clamp(20 - 10/2, 4, 20)/25 = 15/25 = 0.6. Breach folds
// 0.6 + (-0.60) then clamps to [0,1] ~= 0.0, so f5 = 1.0 and the full 10.0 lands (vs 4.0 unenchanted).
func TestBreachReducesArmorEffectiveness(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	weapon := enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:breach", 4))
	victim := combatPlayer(loop, 2)
	src := damageSourcePlayerAttack(1)

	// The armor_effectiveness closure the CombatRules port receives (source.getWeaponItem() fold).
	breachEff := func(f float32) float32 {
		return loop.enchModifyArmorEffectiveness(weapon, enchEntityRef{player: victim}, src, f)
	}

	// Baseline (no enchant): full armor curve.
	base := combatRulesGetDamageAfterAbsorb(10.0, 20.0, 0.0, nil)
	if !approxEq(base, 4.0, 1e-4) {
		t.Fatalf("baseline armor absorb = %v, want ~4.0 (armor=20 curve)", base)
	}

	// With Breach IV: recompute the exact expected value from the ported ops.
	got := combatRulesGetDamageAfterAbsorb(10.0, 20.0, 0.0, breachEff)
	f := float32(2.0) + 0.0/4.0
	armorClamp := mthClampF(20.0-10.0/f, 20.0*0.2, 20.0)
	f3 := armorClamp / 25.0
	f4 := mthClampF(f3+(-0.15+-0.15*3), 0.0, 1.0)
	want := float32(10.0) * (1.0 - f4)
	if got != want {
		t.Fatalf("breach IV armor absorb = %v, want %v (f3=%v folded by -0.60)", got, want, f3)
	}
	if got <= base {
		t.Fatalf("breach did not increase landed damage: got %v, baseline %v", got, base)
	}
}

// TestEnchantAttributeModifierEquipUnequip: an attributes-granting enchant (Sweeping Edge II on the
// mainhand, targeting the MODELED sweeping_damage_ratio) adds its AttributeModifier when the item is
// equipped (detectEquipmentUpdates) and removes it cleanly on unequip. Sweeping Edge II amount =
// fraction(num=linear(1,1)@2=2, den=linear(2,1)@2=3) = 2/3 add_value on a 0.0 base.
func TestEnchantAttributeModifierEquipUnequip(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	// Baseline: no weapon -> sweeping_damage_ratio at its 0.0 registration default.
	if v := p.getAttributeValue(attrSweepingDamageRatio); v != 0.0 {
		t.Fatalf("baseline sweeping_damage_ratio = %v, want 0.0", v)
	}

	// Equip a Sweeping Edge II sword and run the per-tick equipment diff (which applies the enchant's
	// attribute modifier via forEachItemModifier's EnchantmentHelper.forEachModifier tail).
	holdEnchanted(p, enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:sweeping_edge", 2)))
	p.detectEquipmentUpdates()

	wantF := float64(float32(2.0) / float32(3.0))
	got := p.getAttributeValue(attrSweepingDamageRatio)
	if got != wantF {
		t.Fatalf("sweeping_edge II sweeping_damage_ratio on equip = %v, want %v (2/3 add_value)", got, wantF)
	}

	// Unequip (empty the mainhand) and re-diff: the modifier is removed by id, reverting to 0.0.
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{})
	p.detectEquipmentUpdates()
	if v := p.getAttributeValue(attrSweepingDamageRatio); v != 0.0 {
		t.Fatalf("sweeping_damage_ratio after unequip = %v, want 0.0 (modifier removed by id)", v)
	}
}

// TestEnchantAttributeUnmodeledSkipped: an attributes enchant targeting an attribute NOT modeled on
// the player holder (Swift Sneak -> sneaking_speed) is a no-op on equip — the vanilla getInstance ==
// null guard skips it — so no panic and no phantom modifier lands.
func TestEnchantAttributeUnmodeledSkipped(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	// Swift Sneak's slot is "legs"; put it in the worn-legs window slot (7).
	inv := ensureInventory(p)
	inv.set(7, enchantedStack(idBook, 1, enchTestEntry(t, "minecraft:swift_sneak", 3)))
	// Must not panic (the unmodeled attribute is skipped); movement_speed stays untouched.
	before := p.getAttributeValue(attrMovementSpeed)
	p.detectEquipmentUpdates()
	if after := p.getAttributeValue(attrMovementSpeed); after != before {
		t.Fatalf("swift_sneak perturbed movement_speed: before %v after %v", before, after)
	}
}

// TestEnchantPowerDirectAttackerGate: Power's DAMAGE effect carries an entity_properties requirement on
// entity=direct_attacker == #minecraft:arrows. Fired from a bow (direct entity = the arrow) it adds
// 1.0+0.5*(lvl-1) = +3.0 at level 5; with a NON-arrow direct attacker (e.g. a melee weapon whose direct
// == a player) the requirement fails and Power adds nothing. Cite power.json + AbstractArrow.onHitEntity.
func TestEnchantPowerDirectAttackerGate(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bow := enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:power", 5))
	src := damageSourceArrow(1)
	victim := NewEntity(60, entity.Pig, 0, 64, 0)

	// direct_attacker == minecraft:arrow -> Power adds +3.0: 1.0 -> 4.0.
	got := loop.enchModifyDamageDirect(bow, enchEntityRef{mob: victim}, src, "minecraft:arrow", 1.0)
	if got != 4.0 {
		t.Fatalf("power V vs arrow direct = %v, want 4.0 (1.0 + 1.0+0.5*4)", got)
	}

	// A non-arrow direct attacker fails the requirement -> Power is a no-op: 1.0 stays 1.0.
	got = loop.enchModifyDamageDirect(bow, enchEntityRef{mob: victim}, src, "minecraft:player", 1.0)
	if got != 1.0 {
		t.Fatalf("power V with non-arrow direct = %v, want 1.0 (requirement fails)", got)
	}
}

// TestEnchantPunchDirectAttackerGate: Punch's KNOCKBACK effect (add linear 1.0 + 1.0*(lvl-1)) is gated on
// direct_attacker == #minecraft:arrows. Punch II fired from a bow adds +2.0 to the raw 0.0 knockback;
// a non-arrow direct attacker leaves it 0.0. Cite punch.json + AbstractArrow.doKnockback.
func TestEnchantPunchDirectAttackerGate(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	bow := enchantedStack(idDiamondSword, 1, enchTestEntry(t, "minecraft:punch", 2))
	src := damageSourceArrow(1)
	victim := NewEntity(61, entity.Pig, 0, 64, 0)

	got := loop.enchModifyKnockbackDirect(bow, enchEntityRef{mob: victim}, src, "minecraft:arrow", 0.0)
	if got != 2.0 {
		t.Fatalf("punch II vs arrow direct = %v, want 2.0 (0 + 1.0+1.0*1)", got)
	}

	got = loop.enchModifyKnockbackDirect(bow, enchEntityRef{mob: victim}, src, "minecraft:player", 0.0)
	if got != 0.0 {
		t.Fatalf("punch II with non-arrow direct = %v, want 0.0 (requirement fails)", got)
	}
}
