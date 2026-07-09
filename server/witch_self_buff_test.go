package server

// witch_self_buff_test.go — the FOUR SERVER-SIDE OBSERVABLE ACTIONS of the witch's self-buffs
// (MOB-HOST-07), ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this
// session). The PRESENCE gate (hasEffect) was already wired (mob_effect.go entityHasEffect +
// ai_goals_witch.go witchAiStep); this file gates the OBSERVABLE effect each attached potion has
// on the witch's own game state.
//
// Verified behavior (per effect, jar-cited):
//
//	[1] SPEED (Swiftness, line 873-874 mob_effect.go applyEntityEffectModifiers)
//	    net.minecraft.world.effect.MobEffects.SPEED registration (MobEffects <clinit>):
//	      addAttributeModifier(MOVEMENT_SPEED, "effect.speed", 0.20000000298023224d, ADD_MULTIPLIED_TOTAL)
//	    AttributeInstance.calculateValue folds the modifier into the final value:
//	      base = 0.25 (witch MOVEMENT_SPEED); +0% ADD_VALUE; ADD_MULTIPLIED_TOTAL * (1+0.2*1) = 0.30
//	    Cite MobEffects.SPEED + LivingEntity.getAttributeValue + AttributeInstance.calculateValue.
//
//	[2] WATER_BREATHING (breath_mob.go tickMobBreath)
//	    LivingEntity.baseTick (line 244-279): `boolean flag = !canBreatheUnderwater() &&
//	    !MobEffectUtil.hasWaterBreathing(this)` — when flag is false, air neither decrements nor
//	    refills. A witch that self-drinks water_breathing holds its air supply EXACTLY (no decrement).
//	    Cite LivingEntity.baseTick + MobEffectUtil.hasWaterBreathing.
//
//	[3] FIRE_RESISTANCE (combat_mob.go applyDamageEntity)
//	    LivingEntity.hurtServer (line 2811-2820): `if (source.is(IS_FIRE) && hasEffect(FIRE_RESISTANCE))
//	    return false;` — a fire-resistant witch takes ZERO on_fire damage (the hurtServer early-out
//	    fires BEFORE the amount<0 clamp). Cite LivingEntity.hurtServer (the IS_FIRE + FIRE_RESISTANCE
//	    early-return).
//
//	[4] HEALING (instant_health, mob_effect.go applyInstantEntityEffect)
//	    HealOrharmMobEffect.applyInstantaneousEffect (MobEffects <clinit>): `p_37635_.heal(
//	    Mth.floor(Math.max(4 << p_37636_, 0)))` — INSTANT heal of (4 << amp). At amp0 = 4 HP, self
//	    scale 1.0 -> the round-to-int applies: floor(1.0*4+0.5) = 4. Heal clamps to maxHealth.
//	    Cite HealOrharmMobEffect.applyInstantaneousEffect (NOT a periodic tick — it is INSTANT).

import (
	"testing"

	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestWitchSwiftnessSpeedBoost: attach SPEED (Swiftness) amp0 to a witch and verify the folded
// MOVEMENT_SPEED value is base*(1+0.2*1) = 0.25 * 1.2 = 0.30 (vanilla ADD_MULTIPLIED_TOTAL fold).
// Cite MobEffects.SPEED addAttributeModifier(MOVEMENT_SPEED, "effect.speed", 0.20000000298023224d,
// ADD_MULTIPLIED_TOTAL) + AttributeInstance.calculateValue.
func TestWitchSwiftnessSpeedBoost(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)

	base := w.getAttributeValue(attribute.MovementSpeed)
	if base != 0.25 {
		t.Fatalf("witch base MOVEMENT_SPEED = %v, want 0.25 (witchSupplier)", base)
	}

	loop.addEntityEffect(w, effectSpeed, 3600, 0)

	got := w.getAttributeValue(attribute.MovementSpeed)
	want := 0.25 * (1.0 + 0.20000000298023224) // base * (1 + 0.2 * (amp+1)) at amp0
	// Loose tolerance: the float64 fold is exact for these literals.
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-12 {
		t.Fatalf("witch Swiftness MOVEMENT_SPEED = %v, want %v (0.25 * 1.2)", got, want)
	}

	// Removing the effect detaches the modifier; the value returns to base.
	loop.removeEntityEffectModifiers(w, effectSpeed)
	if got2 := w.getAttributeValue(attribute.MovementSpeed); got2 != base {
		t.Fatalf("after remove modifier MOVEMENT_SPEED = %v, want base %v", got2, base)
	}
}

// TestWitchSwiftnessScalesWithAmplifier: amp1 -> base * (1 + 0.2*2) = 0.25 * 1.4 = 0.35.
func TestWitchSwiftnessScalesWithAmplifier(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)

	loop.addEntityEffect(w, effectSpeed, 3600, 1)

	got := w.getAttributeValue(attribute.MovementSpeed)
	want := 0.25 * (1.0 + 0.20000000298023224*2.0) // 0.35
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-12 {
		t.Fatalf("witch Swiftness II MOVEMENT_SPEED = %v, want %v (0.25 * 1.4)", got, want)
	}
}

// TestWitchBreathUnderwater: a witch holding WATER_BREATHING does not decrement air over many
// underwater ticks (LivingEntity.baseTick's `flag = !canBreatheUnderwater() && !hasWaterBreathing`
// folds to false). Cite LivingEntity.baseTick line 244-279 + MobEffectUtil.hasWaterBreathing.
func TestWitchBreathUnderwater(t *testing.T) {
	// We need BOTH the witch registry (for spawnWitch) AND a water block (for mobEyeInWater), so
	// build on the witchDrinkLoop and inject a water block at the witch's eye position.
	loop, floorY := witchDrinkLoop(t)
	mgr := loop.only().world
	if mgr == nil {
		t.Fatal("witchDrinkLoop must wire a world")
	}
	// Witch height 1.95 -> eye at y+1.6575 -> block 65 at y=64.
	setWater(mgr, pk.Position{X: 8, Y: floorY + 2, Z: 8}, 0)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	if !loop.mobEyeInWater(w) {
		t.Fatalf("witch eyes must be in water for this test")
	}

	loop.addEntityEffect(w, effectWaterBreathing, 3600, 0)
	if !mobHasWaterBreathing(w) {
		t.Fatal("witch holding WATER_BREATHING should report hasWaterBreathing")
	}

	startAir := w.airSupply
	for i := 0; i < 600; i++ {
		loop.tickMobBreath(w)
	}
	if w.airSupply != startAir {
		t.Fatalf("witch with WATER_BREATHING air changed: %d -> %d (want unchanged over 600 ticks)",
			startAir, w.airSupply)
	}
}

// TestWitchFireResistance: attach FIRE_RESISTANCE to a witch, ignite for ticks, then route a
// 1.0 on_fire damage through applyDamageEntity. LivingEntity.hurtServer's
// `if (source.is(IS_FIRE) && hasEffect(FIRE_RESISTANCE)) return false;` short-circuits BEFORE the
// damage lands — health is unchanged. Cite LivingEntity.hurtServer (line 2811-2820 of javap).
func TestWitchFireResistance(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)

	startHealth := w.health
	loop.addEntityEffect(w, effectFireResistance, 3600, 0)
	if !entityHasEffect(w, effectFireResistance) {
		t.Fatal("witch FIRE_RESISTANCE not stored")
	}

	// Drive the on-fire damage directly (the same seam fire.go's tickEntityFire uses).
	loop.applyDamageEntity(w, damageSourceOf(damageTypeOnFire), 1.0)
	if w.health != startHealth {
		t.Fatalf("witch with FIRE_RESISTANCE took on_fire damage: health %v -> %v (want unchanged)",
			startHealth, w.health)
	}

	// A control: the same damage on a witch WITHOUT FIRE_RESISTANCE must land (proves the gate
	// actually fires, not that the path is broken in general).
	w2 := spawnWitch(loop, 9.5, float64(floorY+1), 9.5)
	h0 := w2.health
	loop.applyDamageEntity(w2, damageSourceOf(damageTypeOnFire), 1.0)
	if w2.health >= h0 {
		t.Fatalf("control: witch WITHOUT FIRE_RESISTANCE did not take on_fire damage (health %v -> %v)", h0, w2.health)
	}
}

// TestWitchHealingInstant: addEntityEffect(instant_health, amp0) is INSTANT (HealOrHarmMobEffect):
// heals (int)(1.0*(4<<0)+0.5) == 4 HP at self-drink scale 1.0, never stored in the duration map.
// Cite HealOrharmMobEffect.applyInstantaneousEffect (MobEffects.<clinit>): `heal(Mth.floor(
// Math.max(4<<amp,0)))`. The 4<<0 = 4; self-drink scale 1.0 -> heals 4.
func TestWitchHealingInstant(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 10.0

	loop.addEntityEffect(w, effectInstantHealth, 1, 0)
	if w.health != 14.0 {
		t.Fatalf("witch HEALING healed to %v, want 14 (10 + 4<<0)", w.health)
	}
	if entityHasEffect(w, effectInstantHealth) {
		t.Fatal("instant_health should NOT be stored in the duration map (it is an InstantaneousMobEffect)")
	}
}

// TestWitchHealingScalesWithAmplifier: amp1 -> (4 << 1) = 8 HP instant heal. Cite the same
// HealOrharmMobEffect formula.
func TestWitchHealingScalesWithAmplifier(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 5.0

	loop.addEntityEffect(w, effectInstantHealth, 1, 1)
	if w.health != 13.0 {
		t.Fatalf("witch HEALING amp1 healed to %v, want 13 (5 + 4<<1 = 8)", w.health)
	}
}

// TestWitchHealingClampsToMax: heal clamps to maxHealth (LivingEntity.heal clamps
// `setHealth(Mth.clamp(getHealth()+heal, 0.0F, getMaxHealth()))`).
func TestWitchHealingClampsToMax(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 25.0 // maxHealth is 26

	loop.addEntityEffect(w, effectInstantHealth, 1, 0)
	maxH := float32(w.getAttributeValue(attribute.MaxHealth))
	if w.health != maxH {
		t.Fatalf("witch HEALING at near-max clamped to %v, want maxHealth %v", w.health, maxH)
	}
}

// TestWitchDrinkFinishAppliesInstantHeal: driving witchAiStep with a pending HEALING drink applies
// the instant self-heal on finish. Cite Witch.aiStep's `forEachEffect(addEffect, durScale=1.0)`
// which applies HEALING (instant_health) to the witch itself.
func TestWitchDrinkFinishAppliesInstantHeal(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)
	w.health = 10.0

	w.witchDrinking = true
	w.witchUsingTime = witchPotionUseDuration
	w.witchDrinkPending = effectInstantHealth
	w.witchDrinkPendingDur = 1

	for i := 0; i < witchPotionUseDuration+2; i++ {
		loop.witchAiStep(w)
	}
	if w.health != 14.0 {
		t.Fatalf("witch HEALING drink did not heal: health %v, want 14", w.health)
	}
}

// TestWitchSwiftnessPersistenceUntilRemoved: the MOVEMENT_SPEED modifier persists across
// tickMobEffects ticks (it's NOT a per-tick re-attach — vanilla's onEffectAdded attaches it once
// and onEffectsRemoved detaches it). Cite MobEffect.addAttributeModifiers /
// removeAttributeModifiers (called once per add/remove, NOT per tick).
func TestWitchSwiftnessPersistenceUntilRemoved(t *testing.T) {
	loop, floorY := witchDrinkLoop(t)
	w := spawnWitch(loop, 8.5, float64(floorY+1), 8.5)

	loop.addEntityEffect(w, effectSpeed, 3600, 0)
	boosted := w.getAttributeValue(attribute.MovementSpeed)

	// Tick many ticks — the modifier must persist (no per-tick churn).
	for i := 0; i < 100; i++ {
		loop.tickMobEffects(w)
	}
	if got := w.getAttributeValue(attribute.MovementSpeed); got != boosted {
		t.Fatalf("Speed modifier churned across ticks: %v -> %v", boosted, got)
	}
}