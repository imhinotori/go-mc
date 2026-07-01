package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// attack_dispatch_test.go covers the Plan 17-11 1:1 melee-attack port (Player.attack(Entity)):
// applyInput routes ServerboundAttack (the 26.2 single-VarInt entityId packet, split out of the
// old ServerboundInteract{Action}) into handleAttack, which runs the full vanilla sequence —
// ATTACK_DAMAGE base, the attack-strength cooldown ramp, the crit ×1.5, the i-frame-gated
// hurtServer application, knockback, sweep, and food exhaustion. The server is authoritative: the
// packet only NAMES a target (T-6-05 / V4). A forged/out-of-reach/self/malformed input is a silent
// no-op (T-6-04 / V5).
//
// The damage a bare-hand swing deals depends on the attack-strength scale:
//   delay        = (1.0 / ATTACK_SPEED(4.0)) * 20.0 = 5.0 ticks
//   scale        = clamp((attackStrengthTicker + 0.5) / 5.0, 0, 1)
//   baseScale    = 0.2 + scale*scale*0.8
//   damage       = ATTACK_DAMAGE(1.0) * baseScale   (no crit / no enchant in v1)
// A fresh swing (ticker 0): scale 0.1, baseScale 0.208, damage 0.208.
// A charged swing (ticker >= 5): scale 1.0, baseScale 1.0, damage 1.0.

// attackPacket builds a ServerboundAttack carrying the VarInt target entityId — the entire
// 26.2 wire body (ServerboundAttackPacket = VarInt entityId).
func attackPacket(targetID int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundAttack), pk.VarInt(targetID))
}

// placeAttackPlayer registers a confirmed combat player at the given position so reach can be
// exercised. It reuses combatPlayer (full health, capturing client, confirmedTeleport) and
// then positions it.
func placeAttackPlayer(loop *TickLoop, entityID int32, x, y, z float64) *tickPlayer {
	p := combatPlayer(loop, entityID)
	p.x, p.y, p.z = x, y, z
	return p
}

// charge fully recharges the attacker's attack-strength ticker so the next swing deals full
// (1.0x) damage — scale clamps to 1.0 once ticker + 0.5 >= delay (5.0).
func charge(p *tickPlayer) {
	p.attackStrengthTicker = 10
}

// TestAttackChargedDealsFullDamage: a fully-charged bare-hand swing in reach deals exactly 1.0
// damage (ATTACK_DAMAGE 1.0 * baseScale 1.0) and sends one SetHealth to the victim.
func TestAttackChargedDealsFullDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0) // 1 block away, well within reach

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	want := float32(maxHealth) - 1.0
	if victim.health != want {
		t.Fatalf("charged attack victim health = %v, want %v", victim.health, want)
	}
	ps := drainPackets(victim.client)
	if n := countID(ps, packetid.ClientboundSetHealth); n != 1 {
		t.Fatalf("attack sent %d SetHealth to victim, want 1", n)
	}
}

// TestAttackStrengthRamp: a FRESH swing (ticker 0) deals ~0.2x (the just-attacked floor); a
// CHARGED swing deals 1.0x. Asserts the baseDamageScaleFactor ramp (0.2 + scale^2*0.8).
func TestAttackStrengthRamp(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	t.Run("fresh swing deals 0.2x floor", func(t *testing.T) {
		attacker := placeAttackPlayer(loop, 10, 0, 64, 0)
		attacker.attackStrengthTicker = 0 // just attacked: scale = (0+0.5)/5 = 0.1
		victim := placeAttackPlayer(loop, 11, 1, 64, 0)

		loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

		// scale 0.1 -> baseScale 0.2 + 0.01*0.8 = 0.208 -> damage 1.0*0.208 = 0.208
		const wantDamage = float32(0.208)
		got := float32(maxHealth) - victim.health
		if !approxEq(got, wantDamage, 1e-4) {
			t.Fatalf("fresh swing dealt %v damage, want ~%v (0.2x floor)", got, wantDamage)
		}
	})

	t.Run("charged swing deals 1.0x", func(t *testing.T) {
		attacker := placeAttackPlayer(loop, 12, 0, 64, 0)
		charge(attacker)
		victim := placeAttackPlayer(loop, 13, 1, 64, 0)

		loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

		got := float32(maxHealth) - victim.health
		if !approxEq(got, 1.0, 1e-4) {
			t.Fatalf("charged swing dealt %v damage, want ~1.0 (full)", got)
		}
	})
}

// TestAttackResetsAttackStrengthTicker: the swing accompanying the attack resets the attacker's
// attackStrengthTicker to 0 (ServerPlayer.swing) so the next swing starts from the floor.
func TestAttackResetsAttackStrengthTicker(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if attacker.attackStrengthTicker != 0 {
		t.Fatalf("after attack attackStrengthTicker = %d, want 0 (swing reset)", attacker.attackStrengthTicker)
	}
}

// TestAttackCritMultiplier: a charged swing while airborne+falling+not-sprinting crits for ×1.5.
// canCriticalAttack requires fallDistance>0 && !onGround && !inWater && !sprinting && fullStrength.
func TestAttackCritMultiplier(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	attacker.fallDistance = 1.0 // descending
	attacker.onGround = false   // airborne
	attacker.sprinting = false  // not sprinting
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	// damage = 1.0 (charged) * 1.5 (crit) = 1.5
	got := float32(maxHealth) - victim.health
	if !approxEq(got, 1.5, 1e-4) {
		t.Fatalf("crit swing dealt %v damage, want ~1.5 (×1.5 crit)", got)
	}
}

// TestAttackNoCritOnGround: a charged swing while on the ground does NOT crit (fallDistance>0 but
// onGround true fails canCriticalAttack) — deals the plain 1.0.
func TestAttackNoCritOnGround(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	attacker.fallDistance = 1.0
	attacker.onGround = true // on the ground: NOT a crit
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	got := float32(maxHealth) - victim.health
	if !approxEq(got, 1.0, 1e-4) {
		t.Fatalf("on-ground swing dealt %v damage, want ~1.0 (no crit)", got)
	}
}

// TestAttackOutOfReach: A attacks B beyond reach -> no damage (silent no-op).
func TestAttackOutOfReach(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	victim := placeAttackPlayer(loop, 2, 100, 64, 0) // far away

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if victim.health != maxHealth {
		t.Fatalf("out-of-reach attack changed health to %v, want %v (no-op)", victim.health, float32(maxHealth))
	}
	if n := countID(drainPackets(victim.client), packetid.ClientboundSetHealth); n != 0 {
		t.Fatalf("out-of-reach attack sent %d SetHealth, want 0", n)
	}
}

// TestAttackForgedTarget: ATTACK an unknown entity id -> nil victim, no panic, no damage.
func TestAttackForgedTarget(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)

	// 9999 is not a registered entity id.
	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(9999)})

	if victim.health != maxHealth {
		t.Fatalf("forged-target attack changed an unrelated victim's health to %v", victim.health)
	}
}

// TestAttackSelf: A attacks its own entityID -> no damage (self-attack guard).
func TestAttackSelf(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(attacker.entityID)})

	if attacker.health != maxHealth {
		t.Fatalf("self-attack changed health to %v, want %v (guarded)", attacker.health, float32(maxHealth))
	}
	if n := countID(drainPackets(attacker.client), packetid.ClientboundSetHealth); n != 0 {
		t.Fatalf("self-attack sent %d SetHealth, want 0", n)
	}
}

// TestAttackLethalDeath: B at low HP attacked with a charged swing -> health 0, die() called.
func TestAttackLethalDeath(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)
	victim.health = 1.0 // a charged 1.0 swing is exactly lethal

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if victim.health != 0 {
		t.Fatalf("lethal attack health = %v, want 0", victim.health)
	}
	if !victim.dead {
		t.Fatal("lethal attack must set the dead flag (die() called)")
	}
	if n := countID(drainPackets(victim.client), packetid.ClientboundPlayerCombatKill); n != 1 {
		t.Fatal("lethal attack must send PlayerCombatKill (the death screen)")
	}
}

// TestAttackMalformedNoPanic: an empty-body ServerboundAttack (Scan fails) is a silent no-op,
// never a panic (T-6-04 / V5 defensive decode).
func TestAttackMalformedNoPanic(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)

	// must not panic
	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: pk.Packet{ID: int32(packetid.ServerboundAttack)}})
}

// TestAttackKnockbackApplied: a charged sprint attack (sprintKb adds 0.5 extra knockback) imparts a
// non-zero velocity to the victim's playerEntity and sends it exactly ONE SetEntityMotion reflecting
// the combined base (dealDefaultKnockback 0.4) + extra (causeExtraKnockback, sprint 0.5) impulse — the
// base path mutates velocity via knockbackNoSend, and causeExtraKnockback emits the single send.
func TestAttackKnockbackApplied(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	attacker.sprinting = true // sprintKb -> +0.5 knockback strength
	attacker.yaw = 90         // facing +X-ish; the impulse direction is (sin, -cos) of yaw
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)
	// Give the victim a store Entity so knockback can write velocity and send SetEntityMotion.
	victim.playerEntity = &Entity{id: victim.entityID}
	victim.onGround = true // so the vertical pop branch (min(0.4, ...)) is exercised

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if victim.playerEntity.vx == 0 && victim.playerEntity.vy == 0 && victim.playerEntity.vz == 0 {
		t.Fatalf("knockback imparted no velocity: v=(%v,%v,%v)", victim.playerEntity.vx, victim.playerEntity.vy, victim.playerEntity.vz)
	}
	if n := countID(drainPackets(victim.client), packetid.ClientboundSetEntityMotion); n != 1 {
		t.Fatalf("knockback sent %d SetEntityMotion to victim, want 1", n)
	}
}

// TestAttackNoSprintBaseKnockback: a charged NON-sprint attack has causeExtraKnockback strength 0
// (ATTACK_KNOCKBACK base 0, no sprint bonus), so the EXTRA knockback is skipped — but vanilla's
// hurtServer STILL runs dealDefaultKnockback(0.4) for the (non-NO_KNOCKBACK) player-attack source, so
// the victim gets the BASE 0.4 horizontal impulse away from the attacker and exactly ONE SetEntityMotion.
// (Attacker at x=0, victim at x=1 -> pushed +X: vx = -normalize(-1,0)*0.4 = +0.4.)
//
//	[VERIFIED javap LivingEntity.hurtServer: if(!source.is(NO_KNOCKBACK)) dealDefaultKnockback(source,
//	 damage, blocked) -> knockback(0.4, sp.x-getX(), sp.z-getZ(), source, damage). Runs BEFORE/independent
//	 of Player.attack's causeExtraKnockback, so a non-sprint melee still knocks the victim back.]
func TestAttackNoSprintBaseKnockback(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	attacker := placeAttackPlayer(loop, 1, 0, 64, 0)
	charge(attacker)
	attacker.sprinting = false
	victim := placeAttackPlayer(loop, 2, 1, 64, 0)
	victim.playerEntity = &Entity{id: victim.entityID}

	loop.applyInput(attacker, SubtickInput{At: loop.clock.Now(), Packet: attackPacket(victim.entityID)})

	if victim.playerEntity.vx != knockbackDefaultPower || victim.playerEntity.vy != 0 || victim.playerEntity.vz != 0 {
		t.Fatalf("non-sprint base knockback velocity = (%v,%v,%v), want (%v,0,0)", victim.playerEntity.vx, victim.playerEntity.vy, victim.playerEntity.vz, knockbackDefaultPower)
	}
	if n := countID(drainPackets(victim.client), packetid.ClientboundSetEntityMotion); n != 1 {
		t.Fatalf("non-sprint attack sent %d SetEntityMotion, want 1 (base knockback)", n)
	}
}

// approxEq reports whether two float32 values are within eps — used for the damage-ramp assertions
// where the product carries single-precision rounding.
func approxEq(a, b, eps float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}
