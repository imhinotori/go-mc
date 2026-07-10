package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// potion_contents_test.go covers the DRINK-a-potion subsystem (potion_contents.go): the Potions
// effect table + PotionContents.onConsume -> applyToLivingEntity -> forEachEffect apply path, plus the
// USE_REMAINDER=glass_bottle finish result. Verified against the 26.2 jar (Potions static{},
// PotionContents.onConsume/applyToLivingEntity/forEachEffect, PotionItem.finishUsingItem chain).

// potionStackFor builds a count-1 minecraft:potion whose POTION_CONTENTS names the given potion index
// (the makePotionBottle port of PotionContents.createItemStack) -- the same wire shape a brewed bottle
// carries.
func potionStackFor(potionIdx int32) component.SlotData {
	return makePotionBottle(int32(itemID("minecraft:potion")), potionIdx)
}

// TestPotionSwiftnessGrantsSpeed: drinking a Swiftness potion (index 13: SPEED 3600 amp0) grants the
// SPEED effect with duration 3600 and amplifier 0 (scale 1.0, so duration unchanged).
func TestPotionSwiftnessGrantsSpeed(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	const swiftness = 13
	loop.applyPotionContents(p, potionStackFor(swiftness))

	if !playerHasEffect(p, effectSpeed) {
		t.Fatalf("drinking swiftness: player has no SPEED effect")
	}
	e := p.activeEffects[effectSpeed]
	if e.duration != 3600 {
		t.Fatalf("SPEED duration = %d, want 3600", e.duration)
	}
	if e.amplifier != 0 {
		t.Fatalf("SPEED amplifier = %d, want 0", e.amplifier)
	}
}

// TestPotionStrongSwiftnessAmplifier: Strong Swiftness (index 15: SPEED 1800 amp1) -> amplifier 1.
func TestPotionStrongSwiftnessAmplifier(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	const strongSwiftness = 15
	loop.applyPotionContents(p, potionStackFor(strongSwiftness))

	e := p.activeEffects[effectSpeed]
	if e == nil {
		t.Fatalf("drinking strong_swiftness: no SPEED effect")
	}
	if e.duration != 1800 || e.amplifier != 1 {
		t.Fatalf("strong_swiftness SPEED = (dur %d, amp %d), want (1800, 1)", e.duration, e.amplifier)
	}
}

// TestPotionHealingHeals: drinking a Healing potion (index 24: INSTANT_HEALTH 1 amp0) heals a damaged
// player by 4<<0 = 4 (HealOrHarmMobEffect.applyInstantaneousEffect, scale 1.0).
func TestPotionHealingHeals(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.health = 10 // damaged below max so the heal is observable (not clamped at 20)

	const healing = 24
	loop.applyPotionContents(p, potionStackFor(healing))

	if p.health != 14 {
		t.Fatalf("healing potion: health = %v, want 14 (10 + 4)", p.health)
	}
	// INSTANT_HEALTH is instantaneous -> applied one-shot, never inserted as a duration effect.
	if playerHasEffect(p, effectInstantHealth) {
		t.Fatalf("healing potion: instant_health should not persist as a duration effect")
	}
}

// TestPotionFinishYieldsGlassBottleSurvival: completing the drink of a potion in survival empties the
// potion and returns a glass_bottle (USE_REMAINDER=glass_bottle, convertIntoRemainder isEmpty branch).
func TestPotionFinishYieldsGlassBottleSurvival(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.gameMode = gameModeSurvival

	const swiftness = 13
	stack := potionStackFor(swiftness)
	result := loop.finishUsingItem(p, stack)

	wantGlass := pk.VarInt(itemID("minecraft:glass_bottle"))
	if result.ItemID != wantGlass || result.Count != 1 {
		t.Fatalf("survival finish: result = (item %d, count %d), want (glass_bottle %d, 1)",
			result.ItemID, result.Count, wantGlass)
	}
	// The effect still applied on finish.
	if !playerHasEffect(p, effectSpeed) {
		t.Fatalf("survival finish: SPEED effect not applied")
	}
}

// TestPotionFinishKeepsPotionCreative: in creative the potion is NOT consumed and no glass bottle is
// produced (ItemStack.consume no-ops under hasInfiniteMaterials, convertIntoRemainder returns the
// unshrunk stack), but the effect still applies.
func TestPotionFinishKeepsPotionCreative(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.gameMode = gameModeCreative

	const swiftness = 13
	stack := potionStackFor(swiftness)
	result := loop.finishUsingItem(p, stack)

	wantPotion := pk.VarInt(itemID("minecraft:potion"))
	if result.ItemID != wantPotion || result.Count != 1 {
		t.Fatalf("creative finish: result = (item %d, count %d), want (potion %d, 1)",
			result.ItemID, result.Count, wantPotion)
	}
	if !playerHasEffect(p, effectSpeed) {
		t.Fatalf("creative finish: SPEED effect not applied")
	}
}

// TestPotionTurtleMasterBothEffects: Turtle Master (index 19) grants BOTH slowness (400 amp3) and
// resistance (400 amp2) -- verifies the multi-effect potion array + forEachEffect iterating all.
func TestPotionTurtleMasterBothEffects(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)

	const turtleMaster = 19
	loop.applyPotionContents(p, potionStackFor(turtleMaster))

	slow := p.activeEffects[effectSlowness]
	res := p.activeEffects[effectResistance]
	if slow == nil || slow.duration != 400 || slow.amplifier != 3 {
		t.Fatalf("turtle_master slowness = %+v, want (400, amp3)", slow)
	}
	if res == nil || res.duration != 400 || res.amplifier != 2 {
		t.Fatalf("turtle_master resistance = %+v, want (400, amp2)", res)
	}
}
