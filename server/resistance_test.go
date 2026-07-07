package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/tag"
)

// resistance_test.go covers E-7: the RESISTANCE mob-effect damage reduction in
// LivingEntity.getDamageAfterMagicAbsorb. Beacon (BEACON-01) grants RESISTANCE to a player in range;
// the effect reduces incoming (post-armor) damage by an amplifier-scaled curve:
//
//	k = (getEffect(RESISTANCE).getAmplifier() + 1) * 5
//	j = 25 - k
//	amount = Math.max(amount * (float) j / 25.0F, 0.0F)
//
// Resistance I (amp 0) -> k=5,  j=20 -> 20% off; Resistance IV (amp 3) -> k=20, j=5  -> 80% off;
// Resistance V (amp 4) -> k=25, j=0  -> full immunity. A BYPASSES_RESISTANCE source (out_of_world /
// generic_kill) ignores the effect entirely. Verified against the 26.2 jar bytecode
// (net.minecraft.world.entity.LivingEntity.getDamageAfterMagicAbsorb).
//
// ARMOR base is 0 for a fresh combatPlayer, so getDamageAfterArmorAbsorb is a pass-through and the
// only reduction observed here is the magic-absorb resistance curve. Each case uses a FRESH player so
// the hurtServer invulnerableTime i-frame gate never interferes.

func TestResistanceDamageReduction(t *testing.T) {
	// Resistance I (amplifier 0): a 10-damage hit is reduced 20% to 8 -> health 20-8 = 12.
	t.Run("Resistance I reduces 20 percent", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 1)
		loop.addPlayerEffect(p, 0, effectResistance, 200, 0, 1.0)
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10)
		if p.health != 12 {
			t.Fatalf("Resistance I health = %v, want 12 (10 dmg -> 8)", p.health)
		}
	})

	// Resistance IV (amplifier 3): a 10-damage hit is reduced 80% to 2 -> health 20-2 = 18.
	t.Run("Resistance IV reduces 80 percent", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 2)
		loop.addPlayerEffect(p, 0, effectResistance, 200, 3, 1.0)
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10)
		if p.health != 18 {
			t.Fatalf("Resistance IV health = %v, want 18 (10 dmg -> 2)", p.health)
		}
	})

	// Resistance V (amplifier 4): k=25, j=0 -> full immunity. amount clamps to 0, so actuallyHurt sees
	// 0 damage and health is unchanged.
	t.Run("Resistance V is full immunity", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 3)
		loop.addPlayerEffect(p, 0, effectResistance, 200, 4, 1.0)
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10)
		if p.health != maxHealth {
			t.Fatalf("Resistance V health = %v, want %v (full immunity)", p.health, float32(maxHealth))
		}
	})

	// A BYPASSES_RESISTANCE source (out_of_world) ignores the effect: even with Resistance IV the full
	// 10 lands -> health 20-10 = 10. (out_of_world also bypasses invulnerability, so the creative check
	// is irrelevant here — a survival player takes it in full.)
	t.Run("bypasses_resistance ignores the effect", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 4)
		loop.addPlayerEffect(p, 0, effectResistance, 200, 3, 1.0) // Resistance IV
		outOfWorld := damageTypeID(tag.DamageTypeIDs["minecraft:out_of_world"])
		loop.applyDamage(p, damageSourceOf(outOfWorld), 10)
		if p.health != 10 {
			t.Fatalf("bypasses_resistance health = %v, want 10 (full 10, effect ignored)", p.health)
		}
	})

	// A player with NO effect is unchanged by the resistance path: full 10 lands -> health 10.
	t.Run("no effect takes full damage", func(t *testing.T) {
		loop := NewTickLoop(newFakeClock())
		p := combatPlayer(loop, 5)
		loop.applyDamage(p, damageSourceOf(damageTypeGeneric), 10)
		if p.health != 10 {
			t.Fatalf("no-effect health = %v, want 10 (full damage)", p.health)
		}
	})
}
