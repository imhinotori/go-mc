package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// movement_effects_test.go -- the three movement mob-effects that read the mob physics path
// (server/physics.go travelInAir via tick_phases.go tickPhysics / jump.go): SLOW_FALLING,
// LEVITATION, JUMP_BOOST. Each is a 1:1 port of LivingEntity.getEffectiveGravity / travelInAir /
// getJumpBoostPower. These tests drive a bare mob vs. an affected mob and assert the affected one
// moves as vanilla dictates, while confirming a NO-effect mob is unperturbed (the pig-oracle
// invariant -- the effect branches are gated on entityHasEffect/entityEffectAmplifier).
//
// ORDER NOTE (B-A1): travelInAir does moveRelative -> move -> gravity -> drag. From REST, tick 1
// moves with the current deltaMovement (0) and only THEN gains velocity; the position changes on
// tick 2 once that velocity carries into the move. So these tests assert the VELOCITY after tick 1
// and the POSITION after tick 2 -- the vanilla move-then-gravity semantics.

// TestSlowFallingReducesFall: a falling mob with SLOW_FALLING loses far less downward speed per
// tick than a bare mob. getEffectiveGravity clamps gravity to min(0.08, 0.01) == 0.01 while the
// mob is falling (deltaMovement.y <= 0), so vy decreases by only ~0.01/tick (x airDrag) instead of
// ~0.08/tick. CITE LivingEntity.getEffectiveGravity (min(getGravity(), 0.01)).
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

	// TICK 1: travelInAir moves with the current deltaMovement (0 from rest), THEN applies gravity +
	// drag -- so tick 1 sets the velocity but does NOT move the position yet (move-then-gravity order,
	// B-A1). The bare mob: vy = (0 - 0.08) * 0.98 = -0.0784; the slow mob: vy = (0 - 0.01) * 0.98 =
	// -0.0098. The slow mob must fall slower (smaller magnitude vy).
	loop.tickPhysics()

	if bare.vy >= 0 || slow.vy >= 0 {
		t.Fatalf("both mobs should be falling: bare.vy=%v slow.vy=%v", bare.vy, slow.vy)
	}
	if slow.vy <= bare.vy {
		t.Fatalf("slow-falling mob should fall SLOWER (larger vy, less negative): slow.vy=%v bare.vy=%v", slow.vy, bare.vy)
	}
	// Exact vanilla velocities after tick 1: bare (-0.08)*0.98, slow (-0.01)*0.98. Drag is float32.
	wantBare := (0.0 - 0.08) * float64(computeModifiedFriction(0.98, airDragModifierDefault))
	wantSlow := (0.0 - 0.01) * float64(computeModifiedFriction(0.98, airDragModifierDefault))
	if d := bare.vy - wantBare; d < -1e-9 || d > 1e-9 {
		t.Fatalf("bare vy = %v, want %v", bare.vy, wantBare)
	}
	if d := slow.vy - wantSlow; d < -1e-9 || d > 1e-9 {
		t.Fatalf("slow vy = %v, want %v", slow.vy, wantSlow)
	}

	// TICK 2: now the tick-1 velocity carries into the move, so both descend -- the slow mob by less.
	loop.tickPhysics()
	if (200.0 - slow.y) >= (200.0 - bare.y) {
		t.Fatalf("slow-falling mob dropped >= bare mob after 2 ticks: slow dy=%v bare dy=%v", 200.0-slow.y, 200.0-bare.y)
	}
	if slow.y >= 200.0 || bare.y >= 200.0 {
		t.Fatalf("both mobs should have descended after 2 ticks: slow.y=%v bare.y=%v", slow.y, bare.y)
	}
}

// TestLevitationDriftsUpward: a mob with LEVITATION drifts UP instead of falling. The travelInAir
// levitation branch REPLACES the gravity subtraction with d5 += (0.05*(amp+1) - deltaMovement.y)*0.2,
// so from rest (vy=0) tick 1 sets vy = (0.05*(amp+1) - 0)*0.2 * airDrag > 0 (upward), and the mob
// RISES on tick 2 once that upward velocity carries into the move. CITE LivingEntity.travelInAir.
func TestLevitationDriftsUpward(t *testing.T) {
	loop, _ := newPhysicsLoop() // no floor

	e := testEntity(1, entity.SulfurCube, 50.5, 100.0, 50.5)
	loop.only().entities.add(e)
	e.mobEffects = map[string]*activeEffect{
		effectLevitation: {id: effectLevitation, duration: 200, amplifier: 0}, // Levitation I (amp 0)
	}

	// TICK 1: sets the upward velocity (move happens first with 0, so no position change yet).
	loop.tickPhysics()

	if e.vy <= 0 {
		t.Fatalf("levitation mob should have upward velocity after tick 1, got vy=%v", e.vy)
	}
	// From rest: vy before drag = (0.05*(0+1) - 0)*0.2 = 0.01, then * 0.98 (float32) upward.
	want := (0.05*1 - 0) * 0.2 * float64(computeModifiedFriction(0.98, airDragModifierDefault))
	if d := e.vy - want; d < -1e-9 || d > 1e-9 {
		t.Fatalf("levitation vy = %v, want %v", e.vy, want)
	}

	// TICK 2: the upward velocity now carries into the move, so the mob rises above its start Y.
	loop.tickPhysics()
	if e.y <= 100.0 {
		t.Fatalf("levitation mob should rise after 2 ticks, y=%v want > 100", e.y)
	}
}

// TestJumpBoostRaisesJumpPower: JUMP_BOOST raises getJumpBoostPower() by 0.1*(amp+1), which adds to
// getJumpPower() (baseJumpPower 0.42). So jumpFromGround sets vy = 0.42 for a bare mob but 0.52 for a
// Jump Boost I mob. CITE LivingEntity.getJumpBoostPower / getJumpPower / jumpFromGround.
func TestJumpBoostRaisesJumpPower(t *testing.T) {
	bare := testEntity(1, entity.SulfurCube, 0, 0, 0)
	boosted := testEntity(2, entity.SulfurCube, 0, 0, 0)

	if p := entityJumpBoostPower(bare); p != 0 {
		t.Fatalf("bare mob jump-boost power = %v, want 0", p)
	}
	boosted.mobEffects = map[string]*activeEffect{
		effectJumpBoost: {id: effectJumpBoost, duration: 200, amplifier: 0}, // Jump Boost I
	}
	if p := entityJumpBoostPower(boosted); p != 0.1 {
		t.Fatalf("Jump Boost I power = %v, want 0.1", p)
	}
	boosted.mobEffects[effectJumpBoost].amplifier = 1
	if p := entityJumpBoostPower(boosted); p != 0.2 {
		t.Fatalf("Jump Boost II power = %v, want 0.2", p)
	}
	boosted.mobEffects[effectJumpBoost].amplifier = 0 // back to I for the impulse check

	jumpFromGround(bare)
	jumpFromGround(boosted)

	if float32(bare.vy) != float32(baseJumpPower) {
		t.Fatalf("bare jump vy = %v, want %v (float32 0.42)", float32(bare.vy), float32(baseJumpPower))
	}
	wantBoost := baseJumpPower + 0.1 // 0.42f + 0.1f folded (float32 in the port)
	if float64(float32(bare.vy)) >= float64(float32(boosted.vy)) {
		t.Fatalf("jump-boost mob should jump HIGHER: boosted.vy=%v bare.vy=%v", boosted.vy, bare.vy)
	}
	if float32(boosted.vy) != float32(wantBoost) {
		t.Fatalf("boosted jump vy = %v, want %v (0.42 + 0.1)", float32(boosted.vy), float32(wantBoost))
	}
}
