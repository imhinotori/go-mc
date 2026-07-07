package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// movement_effects_test.go — the three movement mob-effects that read the mob physics path
// (server/physics.go tickPhysics / jump.go): SLOW_FALLING, LEVITATION, JUMP_BOOST. Each is a
// 1:1 port of LivingEntity.getEffectiveGravity / travelInAir / getJumpBoostPower (javap-cited in
// tick_phases.go + jump.go). These tests drive a bare mob vs. an affected mob and assert the
// affected one moves as vanilla dictates, while confirming a NO-effect mob is unperturbed (the
// pig-oracle invariant — the effect branches are gated on entityHasEffect/entityEffectAmplifier).

// TestSlowFallingReducesFall: a falling mob with SLOW_FALLING loses far less downward speed per
// tick than a bare mob. getEffectiveGravity clamps gravity to min(0.08, 0.01) == 0.01 while the
// mob is falling (deltaMovement.y <= 0), so vy decreases by only ~0.01/tick (× airDrag) instead
// of ~0.08/tick. CITE LivingEntity.getEffectiveGravity (min(getGravity(), 0.01)).
func TestSlowFallingReducesFall(t *testing.T) {
	loop, _ := newPhysicsLoop() // no floor: both mobs free-fall in air

	bare := testEntity(1, entity.SulfurCube, 100.5, 200.0, 100.5)
	slow := testEntity(2, entity.SulfurCube, 120.5, 200.0, 120.5)
	loop.only().entities.add(bare)
	loop.only().entities.add(slow)
	slow.mobEffects = map[string]*activeEffect{
		effectSlowFalling: {id: effectSlowFalling, duration: 200, amplifier: 0},
	}

	if !entityHasEffect(slow, effectSlowFalling) {
		t.Fatal("slow-falling not attached")
	}

	// One physics tick: gravity + air drag are applied then the mob moves. Compare the resulting
	// downward velocities. The bare mob: vy = (0 - 0.08) * 0.98 = -0.0784. The slow mob: vy =
	// (0 - 0.01) * 0.98 = -0.0098. The slow mob must fall much slower (smaller magnitude vy).
	loop.tickPhysics()

	if bare.vy >= 0 || slow.vy >= 0 {
		t.Fatalf("both mobs should be falling: bare.vy=%v slow.vy=%v", bare.vy, slow.vy)
	}
	if slow.vy <= bare.vy {
		t.Fatalf("slow-falling mob should fall SLOWER (larger vy, less negative): slow.vy=%v bare.vy=%v", slow.vy, bare.vy)
	}
	// The slow mob dropped less far this tick than the bare mob.
	if (200.0 - slow.y) >= (200.0 - bare.y) {
		t.Fatalf("slow-falling mob dropped >= bare mob: slow dy=%v bare dy=%v", 200.0-slow.y, 200.0-bare.y)
	}
	// Exact vanilla values: bare -0.0784, slow -0.0098 (each = (-grav)*0.98).
	if d := bare.vy - (-0.08 * 0.98); d < -1e-9 || d > 1e-9 {
		t.Fatalf("bare vy = %v, want %v", bare.vy, -0.08*0.98)
	}
	if d := slow.vy - (-0.01 * 0.98); d < -1e-9 || d > 1e-9 {
		t.Fatalf("slow vy = %v, want %v", slow.vy, -0.01*0.98)
	}
}

// TestLevitationDriftsUpward: a mob with LEVITATION drifts UP instead of falling. The travelInAir
// levitation branch REPLACES the gravity subtraction with d5 += (0.05*(amp+1) - deltaMovement.y)*0.2,
// so from rest (vy=0) the first tick sets vy = (0.05*(amp+1) - 0)*0.2 * airDrag > 0 and the mob's Y
// increases. CITE LivingEntity.travelInAir (LEVITATION branch).
func TestLevitationDriftsUpward(t *testing.T) {
	loop, _ := newPhysicsLoop() // no floor

	e := testEntity(1, entity.SulfurCube, 50.5, 100.0, 50.5)
	loop.only().entities.add(e)
	e.mobEffects = map[string]*activeEffect{
		effectLevitation: {id: effectLevitation, duration: 200, amplifier: 0}, // Levitation I (amp 0)
	}

	loop.tickPhysics()

	// From rest: vy before drag = (0.05*(0+1) - 0)*0.2 = 0.01, then *0.98 = 0.0098 (upward).
	if e.vy <= 0 {
		t.Fatalf("levitation mob should have upward velocity, got vy=%v", e.vy)
	}
	if e.y <= 100.0 {
		t.Fatalf("levitation mob should rise, y=%v want > 100", e.y)
	}
	want := (0.05*1 - 0) * 0.2 * 0.98
	if d := e.vy - want; d < -1e-9 || d > 1e-9 {
		t.Fatalf("levitation vy = %v, want %v", e.vy, want)
	}
}

// TestJumpBoostRaisesJumpPower: JUMP_BOOST raises getJumpBoostPower() by 0.1*(amp+1), which adds to
// getJumpPower() (baseJumpPower 0.42). So jumpFromGround sets vy = 0.42 for a bare mob but 0.52 for a
// Jump Boost I mob. CITE LivingEntity.getJumpBoostPower / getJumpPower / jumpFromGround.
func TestJumpBoostRaisesJumpPower(t *testing.T) {
	bare := testEntity(1, entity.SulfurCube, 0, 0, 0)
	boosted := testEntity(2, entity.SulfurCube, 0, 0, 0)

	// getJumpBoostPower(): 0 with no effect, 0.1*(amp+1) with the effect.
	if p := entityJumpBoostPower(bare); p != 0 {
		t.Fatalf("bare mob jump-boost power = %v, want 0", p)
	}
	boosted.mobEffects = map[string]*activeEffect{
		effectJumpBoost: {id: effectJumpBoost, duration: 200, amplifier: 0}, // Jump Boost I
	}
	if p := entityJumpBoostPower(boosted); p != 0.1 {
		t.Fatalf("Jump Boost I power = %v, want 0.1", p)
	}
	// Jump Boost II (amp 1) -> 0.2.
	boosted.mobEffects[effectJumpBoost].amplifier = 1
	if p := entityJumpBoostPower(boosted); p != 0.2 {
		t.Fatalf("Jump Boost II power = %v, want 0.2", p)
	}
	boosted.mobEffects[effectJumpBoost].amplifier = 0 // back to I for the impulse check

	// jumpFromGround: bare mob jumps with vy = 0.42; boosted mob with vy = 0.42 + 0.1 = 0.52.
	jumpFromGround(bare)
	jumpFromGround(boosted)

	if float32(bare.vy) != float32(baseJumpPower) {
		t.Fatalf("bare jump vy = %v, want %v (float32 0.42)", float32(bare.vy), float32(baseJumpPower))
	}
	wantBoost := baseJumpPower + 0.1 // 0.42f + 0.1f folded (float32 in the port)
	if float64(float32(bare.vy)) >= float64(float32(boosted.vy)) {
		t.Fatalf("jump-boost mob should jump HIGHER: boosted.vy=%v bare.vy=%v", boosted.vy, bare.vy)
	}
	// Compare in float32 space (the port folds getJumpPower in float32 like the jar).
	if float32(boosted.vy) != float32(wantBoost) {
		t.Fatalf("boosted jump vy = %v, want %v (0.42 + 0.1)", float32(boosted.vy), float32(wantBoost))
	}
}
