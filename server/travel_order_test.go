package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// travel_order_test.go -- B-A1 / B-A2 regression: the ported LivingEntity.travelInAir applies
// moveRelative -> move -> gravity -> drag in the EXACT vanilla order, with the correct block
// friction (blockBelow*0.91 grounded, 0.91 airborne) and the (f, 0.98, f) drag multiply. Each
// expectation is HAND-COMPUTED via the SAME float32 drag helpers the port uses (computeModified
// Friction), so the float32-to-double promotion matches the jar exactly. Verified against the 26.2
// bytecode (javap LivingEntity.travelInAir / handleRelativeFrictionAndCalculateMovement /
// getFrictionInfluencedSpeed / computeModifiedFriction / getEffectiveGravity; Entity.getInputVector).

const travelEps = 1e-9

func travelApproxEq(a, b float64) bool { return math.Abs(a-b) <= travelEps }

// dragH is the horizontal drag multiplier f10 = f3 * computeModifiedFriction(0.91, airDragMod),
// computed as float32 then promoted -- exactly as travelInAir does.
func dragH(f3 float32) float64 {
	return float64(f3 * computeModifiedFriction(0.91, airDragModifierDefault))
}

// dragV is the vertical drag multiplier f11 = computeModifiedFriction(0.98, airDragMod), float32
// promoted to double -- exactly as travelInAir does.
func dragV() float64 {
	return float64(computeModifiedFriction(0.98, airDragModifierDefault))
}

// TestTravelInAirGravityDragOrderAirborne: a mob in open AIR, at rest, NO input, after one tick
// has vy = (0 - 0.08) * f11 (f11 = 0.98 as float32). This pins the ORDER: gravity is applied AFTER
// the move (a no-op here), THEN the vertical drag multiplies -- vy = (0 - 0.08) * 0.98, NOT
// (0 * 0.98) - 0.08. Zero horizontal input leaves x/z at 0.
func TestTravelInAirGravityDragOrderAirborne(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0}) // all-air: move() is a no-op

	e := testEntity(1, entity.Pig, 8.5, 200.0, 8.5)
	e.onGround = false
	loop.only().entities.add(e)

	loop.travelInAir(e, 0, 0, 0, 0, navAirFlyingSpeed)

	wantVY := (0.0 - 0.08) * dragV()
	if !travelApproxEq(e.vy, wantVY) {
		t.Fatalf("airborne vy after 1 tick = %.12f, want %.12f (gravity-then-drag order)", e.vy, wantVY)
	}
	if !travelApproxEq(e.vx, 0) || !travelApproxEq(e.vz, 0) {
		t.Fatalf("airborne horizontal drift with zero input: vx=%.12f vz=%.12f, want 0,0", e.vx, e.vz)
	}
}

// TestTravelInAirForwardInputAirborne: open AIR, at rest, yaw=0, forward input (0,0,1) speed 0.15.
// AIRBORNE so getFrictionInfluencedSpeed returns getFlyingSpeed() (0.02), NOT the ground speed.
// getInputVector((0,0,1), 0.02, yaw=0) = (0,0,0.02). Then move (no-op), gravity, drag:
//
//	vx = 0 * (1.0*0.91), vy = (0 - 0.08) * 0.98, vz = 0.02 * (1.0*0.91).
func TestTravelInAirForwardInputAirborne(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})

	e := testEntity(1, entity.Pig, 8.5, 200.0, 8.5)
	e.onGround = false
	e.yaw = 0
	loop.only().entities.add(e)

	loop.travelInAir(e, 0, 0, 1, 0.15, navAirFlyingSpeed)

	ivX, ivY, ivZ := getInputVector(0, 0, 1, navAirFlyingSpeed, 0)
	wantVX := float64(ivX) * dragH(1.0)
	wantVY := (float64(ivY) - 0.08) * dragV()
	wantVZ := float64(ivZ) * dragH(1.0)

	if !travelApproxEq(e.vx, wantVX) {
		t.Fatalf("airborne fwd vx = %.12f, want %.12f", e.vx, wantVX)
	}
	if !travelApproxEq(e.vy, wantVY) {
		t.Fatalf("airborne fwd vy = %.12f, want %.12f", e.vy, wantVY)
	}
	if !travelApproxEq(e.vz, wantVZ) {
		t.Fatalf("airborne fwd vz = %.12f, want %.12f (flyingSpeed 0.02 * f3*0.91)", e.vz, wantVZ)
	}
}

// TestTravelInAirGroundFrictionMultiply: mob standing ON a solid floor, yaw=0, forward input
// (0,0,1) speed 0.15. Grounded so f3 = computeModifiedFriction(0.6, 1.0) = 0.6 and the friction-
// influenced speed is the bare getSpeed() (0.15, since f == 0.6 is NOT > 0.6). The horizontal drag
// multiply is f3*0.91 = 0.546 -- the (f, 0.98, f) form with f = blockBelow friction * 0.91, proving
// the ground horizontal drag uses the block-below friction, not a bare 0.91.
func TestTravelInAirGroundFrictionMultiply(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY) // top surface y = 65

	e := testEntity(1, entity.Pig, 8.5, float64(floorY+1), 8.5)
	e.onGround = true
	e.yaw = 0
	loop.only().entities.add(e)

	loop.travelInAir(e, 0, 0, 1, 0.15, navAirFlyingSpeed)

	// f3 captured BEFORE the move (onGround true), so drag uses 0.6*0.91 regardless of the post-move
	// onGround. getFrictionInfluencedSpeed(true, 0.6, 0.15) == 0.15 (f == 0.6 not > 0.6).
	ivX2, _, ivZ2 := getInputVector(0, 0, 1, 0.15, 0)
	f3 := computeModifiedFriction(travelBlockFrictionDefault, frictionModifierDefault) // 0.6
	f10 := dragH(f3)
	wantVX := float64(ivX2) * f10
	wantVZ := float64(ivZ2) * f10

	if !travelApproxEq(e.vx, wantVX) {
		t.Fatalf("ground fwd vx = %.12f, want %.12f (input.x * 0.6*0.91)", e.vx, wantVX)
	}
	if !travelApproxEq(e.vz, wantVZ) {
		t.Fatalf("ground fwd vz = %.12f, want %.12f (0.15 * 0.6*0.91)", e.vz, wantVZ)
	}
}

// TestComputeModifiedFrictionDefaults: computeModifiedFriction(f, 1.0) == f for the attribute
// default modifier, and it clamps to [0,1]. The (f, 0.98, f) drag constants depend on this identity
// (0.91 and 0.98 pass through unchanged with the default modifier 1.0).
func TestComputeModifiedFrictionDefaults(t *testing.T) {
	cases := []struct{ f, mod, want float32 }{
		{0.6, 1.0, 0.6},
		{0.91, 1.0, 0.91},
		{0.98, 1.0, 0.98},
		{0.6, 0.0, 1.0}, // mod 0 -> 1 - (1-f)*0 = 1
		{0.6, 2.0, 0.2}, // 1 - (1-0.6)*2 = 1 - 0.8 = 0.2
		{0.6, 3.0, 0.0}, // 1 - 0.4*3 = -0.2 -> clamp 0
		{1.5, 1.0, 1.0}, // f > 1 -> clamp 1
	}
	for _, c := range cases {
		got := computeModifiedFriction(c.f, c.mod)
		if math.Abs(float64(got-c.want)) > 1e-6 {
			t.Fatalf("computeModifiedFriction(%v, %v) = %v, want %v", c.f, c.mod, got, c.want)
		}
	}
}

// TestFrictionInfluencedSpeedBranches: airborne returns flyingSpeed; grounded f <= 0.6 returns the
// bare speed; grounded f > 0.6 scales by 0.21600002 / f^3. Pins the 26.2 f>0.6 guard + the literal.
func TestFrictionInfluencedSpeedBranches(t *testing.T) {
	if got := frictionInfluencedSpeed(false, 1.0, 0.15, 0.02); math.Abs(float64(got-0.02)) > 1e-7 {
		t.Fatalf("airborne frictionInfluencedSpeed = %v, want 0.02 (flyingSpeed)", got)
	}
	if got := frictionInfluencedSpeed(true, 0.6, 0.15, 0.02); math.Abs(float64(got-0.15)) > 1e-7 {
		t.Fatalf("grounded f=0.6 frictionInfluencedSpeed = %v, want 0.15 (bare speed)", got)
	}
	f := float32(0.98)
	want := float32(0.15) * (0.21600002 / (f * f * f))
	if got := frictionInfluencedSpeed(true, f, 0.15, 0.02); math.Abs(float64(got-want)) > 1e-7 {
		t.Fatalf("grounded f=0.98 frictionInfluencedSpeed = %v, want %v (speed*0.216/f^3)", got, want)
	}
}

// TestTravelInAirTwoTicksAirborne: two airborne ticks (no input) compound the gravity-then-drag
// recurrence vy' = (vy - 0.08) * f11. Pins the ORDER holds across ticks (momentum carries).
func TestTravelInAirTwoTicksAirborne(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})

	e := testEntity(1, entity.Pig, 8.5, 200.0, 8.5)
	e.onGround = false
	loop.only().entities.add(e)

	loop.travelInAir(e, 0, 0, 0, 0, navAirFlyingSpeed)
	tick1 := e.vy
	loop.travelInAir(e, 0, 0, 0, 0, navAirFlyingSpeed)
	tick2 := e.vy

	want1 := (0.0 - 0.08) * dragV()
	want2 := (want1 - 0.08) * dragV()
	if !travelApproxEq(tick1, want1) {
		t.Fatalf("tick1 vy = %.12f, want %.12f", tick1, want1)
	}
	if !travelApproxEq(tick2, want2) {
		t.Fatalf("tick2 vy = %.12f, want %.12f (compound gravity-then-drag)", tick2, want2)
	}
}
