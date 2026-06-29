package server

// combat_mob_test.go — Wave 0 (TDD RED) scaffold for the mob (*Entity) hurt pipeline keystone
// (Phase 29 Plan 02, MOB-SUB-01/02). The 7 behaviors below pin the jar-exact float32 values for
// applyDamageEntity (LivingEntity.hurtServer), actuallyHurtEntity (LivingEntity.actuallyHurt — NOT
// Player), the damageSource value type + is(tag), and the baseTick i-frame decrement
// (LivingEntity.baseTick). RED until Tasks 1-3 land the pipeline.
//
// JAR AUTHORITY (javap -c -p temp/cache/26.2-inner.jar, this session):
//   - LivingEntity.hurtServer: i-frame gate `(float)invulnerableTime > 10.0F && !is(BYPASSES_COOLDOWN)`;
//     amount<=lastHurt -> no-op; else lastHurt=amount, invulnerableTime=20, actuallyHurt(amount),
//     hurtDuration=10, hurtTime=hurtDuration; flag2 block sets lastDamageSource=source (bytecode 449-451).
//   - LivingEntity.actuallyHurt: getDamageAfterArmorAbsorb -> getDamageAfterMagicAbsorb -> absorption
//     fold (Math.max) -> if amount==0 return -> setHealth(getHealth()-amount). NO causeFoodExhaustion,
//     NO SetHealth client send (the 3 mob-vs-player diffs).
//   - CombatRules.getDamageAfterAbsorb(10, armor=20, toughness=0): f=2.0; clamp(20-10/2,[4,20])=15;
//     f3=15/25=0.6; f5=1-0.6=0.4; 10*0.4 = 4.0 (the exact vanilla-reduced value).
//   - LivingEntity.baseTick: if(hurtTime>0)hurtTime--; if(invulnerableTime>0 && !(this instanceof
//     ServerPlayer))invulnerableTime-- — for a mob the instanceof guard is false so it always decrements.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/tag"
	"github.com/imhinotori/sulfur/level/attribute"
)

// newDamageMob builds a fresh Pig *Entity with the given health for a hurt-pipeline test, added to
// the loop's single region store so actuallyHurtEntity's region-scoped on_damage Emit can resolve the
// owning region (the same-region synchronous fast path this plan ships). The Pig's *attribute.Map is
// the real seeded map (armor/armor_toughness base 0 from createLivingAttributes), so attribute reads
// are genuine — tests that need armor override the instance base directly.
func newDamageMob(loop *TickLoop, id int32, health float32) *Entity {
	e := NewEntity(id, entity.Pig, 8.5, 64, 8.5)
	e.health = health
	loop.only().entities.add(e)
	return e
}

// TestApplyDamageEntity_FreshHit: a 20-health, 0-armor mob hit for 6.0 -> health 14.0,
// invulnerableTime=20, lastHurt=6.0, hurtDuration=hurtTime=10 (the fresh-hit flag2 arm).
func TestApplyDamageEntity_FreshHit(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)

	src := damageSourcePlayerAttack(0)
	loop.applyDamageEntity(e, src, 6.0)

	if e.health != 14.0 {
		t.Fatalf("fresh hit health = %v, want 14.0 (20 - 6, armor 0 pass-through)", e.health)
	}
	if e.invulnerableTime != hurtInvulnerableTicks {
		t.Fatalf("fresh hit invulnerableTime = %d, want %d", e.invulnerableTime, hurtInvulnerableTicks)
	}
	if e.lastHurt != 6.0 {
		t.Fatalf("fresh hit lastHurt = %v, want 6.0", e.lastHurt)
	}
	if e.hurtDuration != hurtDurationTicks {
		t.Fatalf("fresh hit hurtDuration = %d, want %d", e.hurtDuration, hurtDurationTicks)
	}
	if e.hurtTime != hurtDurationTicks {
		t.Fatalf("fresh hit hurtTime = %d, want %d", e.hurtTime, hurtDurationTicks)
	}
}

// TestApplyDamageEntity_IFrameExcessGate: within i-frames (invulnerableTime>10) a smaller-or-equal
// follow-up is a no-op; a larger one applies (amount-lastHurt) only.
func TestApplyDamageEntity_IFrameExcessGate(t *testing.T) {
	t.Run("equal spam hit within window is fully absorbed", func(t *testing.T) {
		loop, _ := newBlockLoop()
		e := newDamageMob(loop, 1, 20.0)
		src := damageSourcePlayerAttack(0)

		loop.applyDamageEntity(e, src, 6.0) // fresh: health 14, lastHurt 6, window 20
		loop.applyDamageEntity(e, src, 6.0) // 6 <= lastHurt 6 -> no-op
		if e.health != 14.0 {
			t.Fatalf("equal spam hit health = %v, want 14.0 (absorbed)", e.health)
		}
	})

	t.Run("larger second hit applies only the excess", func(t *testing.T) {
		loop, _ := newBlockLoop()
		e := newDamageMob(loop, 2, 20.0)
		src := damageSourcePlayerAttack(0)

		loop.applyDamageEntity(e, src, 4.0)  // fresh: health 16, lastHurt 4, window 20
		loop.applyDamageEntity(e, src, 10.0) // 10 > 4 -> applies 10-4 = 6: health 10
		if e.health != 10.0 {
			t.Fatalf("larger second hit health = %v, want 10.0 (only the 6 excess)", e.health)
		}
		if e.lastHurt != 10.0 {
			t.Fatalf("after larger second hit lastHurt = %v, want 10.0", e.lastHurt)
		}
	})
}

// TestActuallyHurtEntity_Armor: a mob with armor on its *attribute.Map takes reduced damage matching
// combatRulesGetDamageAfterAbsorb exactly (float32). armor=20, hit 10 -> 4.0 landed -> health 16.
func TestActuallyHurtEntity_Armor(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	// Override ARMOR to a synthetic 20 (a full iron set) on the REAL *attribute.Map.
	e.attributes.GetInstance(attribute.Armor.Name()).SetBaseValue(20.0)

	src := damageSourcePlayerAttack(0)
	loop.applyDamageEntity(e, src, 10.0) // armor curve: 10 -> 4.0: health 20-4 = 16
	if e.health != 16.0 {
		t.Fatalf("armor-20 hit health = %v, want 16.0 (10 reduced to 4)", e.health)
	}
}

// TestDamageSourceIs_BypassesArmor: the bypass branch skips the armor fold for a bypasses_armor source
// (full damage), folds for player_attack. "minecraft:fall" (id in bypasses_armor) bypasses armor;
// "minecraft:player_attack" does not.
func TestDamageSourceIs_BypassesArmor(t *testing.T) {
	fallID := tag.DamageTypeIDs["minecraft:fall"]
	playerID := tag.DamageTypeIDs["minecraft:player_attack"]

	fall := damageSource{typeTag: damageTypeID(fallID)}
	player := damageSource{typeTag: damageTypeID(playerID)}

	if !fall.is("bypasses_armor") {
		t.Fatalf("fall.is(bypasses_armor) = false, want true (fall is a bypasses_armor member)")
	}
	if player.is("bypasses_armor") {
		t.Fatalf("player_attack.is(bypasses_armor) = true, want false")
	}

	// Behavioral proof: with armor 20, a fall hit (bypass) lands FULL; a player hit folds.
	t.Run("fall bypasses the armor fold", func(t *testing.T) {
		loop, _ := newBlockLoop()
		e := newDamageMob(loop, 1, 20.0)
		e.attributes.GetInstance(attribute.Armor.Name()).SetBaseValue(20.0)
		loop.applyDamageEntity(e, fall, 10.0) // bypass -> full 10: health 10
		if e.health != 10.0 {
			t.Fatalf("fall (bypass) hit health = %v, want 10.0 (armor skipped, full damage)", e.health)
		}
	})
	t.Run("player_attack folds the armor curve", func(t *testing.T) {
		loop, _ := newBlockLoop()
		e := newDamageMob(loop, 2, 20.0)
		e.attributes.GetInstance(attribute.Armor.Name()).SetBaseValue(20.0)
		loop.applyDamageEntity(e, player, 10.0) // folds -> 4.0: health 16
		if e.health != 16.0 {
			t.Fatalf("player_attack hit health = %v, want 16.0 (armor folded)", e.health)
		}
	})
}

// TestLastDamageSource: after a hit, e.lastDamageSource == {typeTag, attacker}.
func TestLastDamageSource(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)

	src := damageSourcePlayerAttack(77)
	loop.applyDamageEntity(e, src, 6.0)

	if e.lastDamageSource != src {
		t.Fatalf("lastDamageSource = %+v, want %+v", e.lastDamageSource, src)
	}
	if e.lastDamageSource.attacker != 77 {
		t.Fatalf("lastDamageSource.attacker = %d, want 77", e.lastDamageSource.attacker)
	}
}

// TestMobIFrameDecrement: tickMobIFrames decrements hurtTime (unconditional) and invulnerableTime
// (>0), integer-only (LivingEntity.baseTick).
func TestMobIFrameDecrement(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.hurtTime = 10
	e.invulnerableTime = 20

	loop.tickMobIFrames(e)
	if e.hurtTime != 9 {
		t.Fatalf("after tickMobIFrames hurtTime = %d, want 9", e.hurtTime)
	}
	if e.invulnerableTime != 19 {
		t.Fatalf("after tickMobIFrames invulnerableTime = %d, want 19", e.invulnerableTime)
	}

	// At zero, neither goes negative.
	e.hurtTime = 0
	e.invulnerableTime = 0
	loop.tickMobIFrames(e)
	if e.hurtTime != 0 || e.invulnerableTime != 0 {
		t.Fatalf("tickMobIFrames at zero = (%d,%d), want (0,0) (no underflow)", e.hurtTime, e.invulnerableTime)
	}
}

// TestMobOnDamageEmit: actuallyHurtEntity fires host.EventDamage with the post-mitigation amount.
// Uses the shared events-manager harness (loadEventsManager) — the same on_damage capture the player
// path's TestDamageIsPostMitigation uses.
func TestMobOnDamageEmit(t *testing.T) {
	loop, _ := newBlockLoop()
	m, ec := loadEventsManager(t)
	loop.SetPlugins(m)
	e := newDamageMob(loop, 1, 20.0)

	const raw float32 = 6
	src := damageSourcePlayerAttack(0)
	loop.applyDamageEntity(e, src, raw)

	if got := ec.get("on_damage"); got != 1 {
		t.Fatalf("on_damage fired %d times, want 1 (from actuallyHurtEntity post-mitigation)", got)
	}
	// v1 pass-through armor (base 0): post-mitigation amount == raw.
	if got := ec.lastDamage(); got != float64(raw) {
		t.Fatalf("on_damage amount = %v, want %v (post-mitigation, v1 pass-through armor)", got, raw)
	}
}
