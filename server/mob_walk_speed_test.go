package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// TestStrollingMobWalkSpeedMatchesVanilla is the regression guard for the "always sprinting" class of
// bug the pig oracle (TestPluginPigEqualsGoNativePig) is BLIND to: the oracle compares a Go-native pig
// against a plugin pig (both run the SAME travel code), so a wrong shared walk-speed passes it silently.
// This test instead pins the ABSOLUTE per-tick horizontal displacement of a strolling pig against the
// value COMPUTED from the vanilla travel formula, catching a wrong MOVEMENT_SPEED scaling / a dropped
// speedModifier / a doubled getSpeed.
//
// Vanilla straight-line ground travel (LivingEntity.travelInAir, 1:1):
//
//	moveRelative(getFrictionInfluencedSpeed(f3), travelVector)  // travelVector = (xxa, yya, zza)
//	getInputVector: lengthSqr<1e-7 -> 0; scale by frictionInfluencedSpeed; rotate by yaw
//	fricSpeed = getFrictionInfluencedSpeed(f3)  // f3 = block friction (0.6 default stone)
//	          = onGround && float64(f3)>0.6d  -> getSpeed() * (0.21600002 / f^3)
//	          = onGround && f<=0.6d          -> getSpeed()
//	          = airborne                    -> getFlyingSpeed()
//
// On the default 0.6 stone floor float64(0.6f) > 0.6d is TRUE (the jar's f2d before the compare), so
// fricSpeed = getSpeed() * (0.21600002 / 0.216) ≈ getSpeed() = 1.0*MOVEMENT_SPEED for a strolling pig.
//
// The MoveControl.tick MOVE_TO branch commits getSpeed = n.speed (= speedModifier x MOVEMENT_SPEED) on
// BOTH paths: LivingEntity.setSpeed(f) AND Mob.setZza(f) (javap, this session). The travel input the
// travelInAir receives is therefore (0, 0, n.speed) — the zza value — not a unit (0,0,1). The per-tick
// forward impulse added to deltaMovement is therefore (input z) x fricSpeed = n.speed x n.speed — the
// faithful vanilla DOUBLE SCALING the regression pins (n.speed squared; pre-fix the input z was the
// constant 1.0, so the effective impulse was just n.speed and a pig sprinted ~4x too fast).
//
// Steady-state terminal horizontal displacement per tick, per vanilla:
//
//	forward_impulse_per_tick = n.speed * n.speed                                  (zza x fricSpeed)
//	horizontal_drag          = f10 = f3 * 0.91 = 0.546 (grounded stone)
//	terminal_velocity        = forward_impulse_per_tick * f10 / (1 - f10)
//	displacement_per_tick    = forward_impulse_per_tick + terminal_velocity
//	                        = forward_impulse_per_tick * (1 + f10/(1-f10))
//
// For a Pig MOVEMENT_SPEED = 0.25, strolling with speedModifier 1.0:
//
//	n.speed = 0.25; forward_impulse = 0.0625; terminal_velocity ≈ 0.075; displacement ≈ 0.1377 b/tick.
//
// (Pre-fix the regression asserted the wrong value ~0.5506 b/tick — the sin of treating input z as
// the constant 1 and computing the terminal as if forward_impulse were n.speed. The faithful formula
// is the squared-impulse terminal above.)
func TestStrollingMobWalkSpeedMatchesVanilla(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, floorY)
		}
	}
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)
	pig.onGround = true

	ms := pig.getAttributeValue(attribute.MovementSpeed)
	if ms != 0.25 {
		t.Fatalf("pig MOVEMENT_SPEED = %v, want 0.25 (Pig.createAttributes)", ms)
	}

	// Expected vanilla terminal per-tick displacement, derived from the DOUBLE-SCALING impulse the
	// navigation commits (zza = n.speed carried through travelInAir, fricSpeed = getFrictionInfluencedSpeed
	// = n.speed on the default 0.6 floor — f2d widens 0.6f to 0.6000000238418579d which IS > 0.6d).
	const f = float32(0.6)
	const blockFracFactor = float32(0.21600002) / (f * f * f) // ≈ 1.0 on the default stone floor
	fricSpeed := float64(float32(ms) * blockFracFactor)       // n.speed * 1.0 = n.speed
	forwardImpulsePerTick := fricSpeed * fricSpeed            // zza * fricSpeed = n.speed x n.speed
	const f10 = 0.6 * 0.91                                    // grounded horizontal drag = 0.546
	expected := forwardImpulsePerTick * (1.0 + f10/(1.0-f10)) // ≈ 0.1377 b/tick for a strolling pig

	// Drive the loop; the pig strolls (WaterAvoidingRandomStrollGoal). Capture the peak per-tick step,
	// which converges to the straight-line terminal displacement while it walks toward a stroll target.
	prevX, prevZ := pig.x, pig.z
	var maxStep float64
	for i := 0; i < 400; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		dx, dz := pig.x-prevX, pig.z-prevZ
		if s := math.Sqrt(dx*dx + dz*dz); s > maxStep {
			maxStep = s
		}
		prevX, prevZ = pig.x, pig.z
	}

	if maxStep == 0 {
		t.Fatal("the pig never moved over 400 ticks — the stroll goal did not drive the shared travel path")
	}
	// The peak straight-line step must match the vanilla terminal within a small tolerance (turning /
	// corner-cutting keeps some ticks below terminal; the peak reaches it on straight segments).
	const tol = 0.02
	if math.Abs(maxStep-expected) > tol {
		t.Fatalf("strolling pig peak step = %.5f b/tick, want ~%.5f (vanilla n.speed x n.speed scaling, "+
			"getSpeed %.3f). A wrong walk speed here is the 'always sprinting' / 'too fast' bug the pig "+
			"oracle misses.", maxStep, expected, ms)
	}
	// Hard ceiling: a mob passing a raw speedModifier (1.0) as the travel input zza AND omitting the
	// frictionInfluencedSpeed re-multiply still gets the unit-input n.speed terminal (~0.55 b/tick for a
	// pig) — i.e. a 4x sprint, the pre-fix regression. Guard it explicitly so the wrong formula fails
	// LOUDLY (instead of just "barely outside the small tolerance").
	if maxStep > expected*1.5 {
		t.Fatalf("strolling pig peak step = %.5f b/tick is far above the vanilla %.5f — a raw speedModifier "+
			"as zza / missing x getSpeed() regression (the 'input z=1' bug that accelerated the pig 4x).",
			maxStep, expected)
	}
	t.Logf("strolling pig peak step = %.5f b/tick, vanilla expected %.5f (n.speed %.3f x n.speed; zza x "+
		"fricSpeed double scaling) — MATCH", maxStep, expected, ms)
}
