package server

// armadillo_combat_test.go -- deterministic pins for the two Armadillo COMBAT hooks (1:1 javap this task):
//   Fix 2  Armadillo.hurtServer: a ROLLED-UP (scared) armadillo halves incoming damage
//          amount = (amount - 1.0F) / 2.0F BEFORE the shared hurt pipeline.
//   Fix 3  Armadillo.actuallyHurt: a hit from a LivingEntity arms the 80-tick DANGER_DETECTED_RECENTLY
//          memory and (if canStayRolledUp) rolls the armadillo up; a PANIC_ENVIRONMENTAL_CAUSES source
//          rolls it out.

import (
	"math"
	"testing"
)

// TestArmadilloBalledHalvesDamage: a SCARED armadillo takes (amount-1)/2 damage; an IDLE one takes full.
func TestArmadilloBalledHalvesDamage(t *testing.T) {
	loop, floorY := armadilloLoop(t)

	// IDLE armadillo takes the full hit (baseline).
	idle := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)
	idle.armadilloState = armadilloStateIdle
	beforeIdle := idle.health
	loop.applyDamageEntity(idle, damageSourcePlayerAttack(7001), 5.0)
	if math.Abs(float64(beforeIdle-idle.health)-5.0) > 1e-4 {
		t.Fatalf("idle armadillo damage = %v, want 5.0 (full)", beforeIdle-idle.health)
	}

	// SCARED armadillo halves: (5.0 - 1.0) / 2.0 = 2.0.
	scared := loop.spawnArmadillo(2.5, float64(floorY+1), 2.5, false)
	scared.armadilloState = armadilloStateScared
	beforeScared := scared.health
	loop.applyDamageEntity(scared, damageSourcePlayerAttack(7002), 5.0)
	got := beforeScared - scared.health
	if math.Abs(float64(got)-2.0) > 1e-4 {
		t.Fatalf("scared armadillo damage = %v, want 2.0 == (5.0-1.0)/2.0", got)
	}
}

// TestArmadilloRollsUpOnHit: a hit from a LivingEntity (player attacker) arms the 80-tick danger memory
// and rolls a grounded armadillo up (canStayRolledUp true).
func TestArmadilloRollsUpOnHit(t *testing.T) {
	loop, floorY := armadilloLoop(t)
	a := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)
	a.onGround = true
	if a.armadilloState != armadilloStateIdle {
		t.Fatalf("precondition: armadillo not IDLE (state=%d)", a.armadilloState)
	}

	loop.applyDamageEntity(a, damageSourcePlayerAttack(7001), 3.0)

	if a.armadilloDangerExpiry != armadilloDangerMemoryTicks {
		t.Fatalf("danger memory after hit = %d, want %d (setMemoryWithExpiry 80L)", a.armadilloDangerExpiry, armadilloDangerMemoryTicks)
	}
	if a.armadilloState != armadilloStateRolling {
		t.Fatalf("armadillo state after living-attacker hit = %d, want ROLLING %d (rollUp)", a.armadilloState, armadilloStateRolling)
	}
}

// TestArmadilloEnvironmentalPanicRollsOut: a PANIC_ENVIRONMENTAL_CAUSES source (on_fire, no attacker)
// unrolls a scared armadillo (rollOut).
func TestArmadilloEnvironmentalPanicRollsOut(t *testing.T) {
	loop, floorY := armadilloLoop(t)
	a := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)
	a.armadilloState = armadilloStateScared // already balled

	// on_fire is a panic_environmental_causes member (id 31) and has NO causing entity (attacker 0),
	// so it takes the `else if source.is(PANIC_ENVIRONMENTAL_CAUSES) -> rollOut()` branch.
	loop.applyDamageEntity(a, damageSourceOf(damageTypeOnFire), 1.0)

	if a.armadilloState != armadilloStateIdle {
		t.Fatalf("armadillo state after environmental panic = %d, want IDLE %d (rollOut)", a.armadilloState, armadilloStateIdle)
	}
}
