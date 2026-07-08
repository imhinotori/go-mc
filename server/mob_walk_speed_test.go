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
//	fricSpeed = getSpeed * (0.21600002 / f^3)          // f = block friction 0.6 (widened to double, >0.6)
//	each tick: deltaMovement += fricSpeed; move(deltaMovement); deltaMovement *= f10 (= f*0.91 = 0.546)
//	steady:  R = (R + fricSpeed) * 0.546  =>  R = fricSpeed * 0.546/(1-0.546)
//	displacement/tick = R + fricSpeed = fricSpeed * (1 + 0.546/0.454)
//
// getSpeed for an idle stroll = speedModifier(1.0) * MOVEMENT_SPEED. For a pig MOVEMENT_SPEED = 0.25.
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

	// Expected vanilla terminal per-tick displacement (stroll speedModifier 1.0).
	const f = float32(0.6)
	fric := float64(float32(ms) * (0.21600002 / (f * f * f)))
	const f10 = 0.6 * 0.91 // grounded horizontal drag
	expected := fric * (1.0 + f10/(1.0-f10))

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
		t.Fatalf("strolling pig peak step = %.5f b/tick, want ~%.5f (vanilla getSpeed %.3f). A wrong walk "+
			"speed here is the 'always sprinting' / 'too fast' bug the pig oracle misses.", maxStep, expected, ms)
	}
	// Hard ceiling: a mob passing a raw speedModifier (1.0) as blocks/tick moved at ~2.2 b/tick — a 4x
	// sprint. Guard that regression explicitly.
	if maxStep > expected*1.5 {
		t.Fatalf("strolling pig peak step = %.5f b/tick is far above the vanilla %.5f — a raw-speedModifier "+
			"(missing x MOVEMENT_SPEED) regression.", maxStep, expected)
	}
	t.Logf("strolling pig peak step = %.5f b/tick, vanilla expected %.5f (getSpeed %.3f) — MATCH", maxStep, expected, ms)
}
