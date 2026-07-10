package server

// camel_test.go -- deterministic pins for the Camel (net.minecraft.world.entity.animal.camel.Camel, 1:1
// javap this session). Verifies the spawn attributes (MAX_HEALTH 32 / MOVEMENT_SPEED 0.09000000357627869 /
// STEP_HEIGHT 1.5 / SAFE_FALL_DISTANCE 6.0) folded from createBaseHorseAttributes + the Camel overrides.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

func camelLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestCamelSpawnDefaults: spawnCamel builds a camel rendering as entity.Camel.ID with the jar attributes,
// including the STEP_HEIGHT 1.5 tall-block step-up and the SAFE_FALL_DISTANCE 6.0 from the horse base.
func TestCamelSpawnDefaults(t *testing.T) {
	loop, floorY := camelLoop(t)
	c := loop.spawnCamel(8.5, float64(floorY+1), 8.5, false)
	if c.typ != entity.Camel.ID {
		t.Fatalf("camel typ = %d, want entity.Camel.ID %d", c.typ, entity.Camel.ID)
	}
	if !c.isCamel {
		t.Fatal("camel not marked isCamel")
	}
	if math.Abs(float64(c.health)-32.0) > 1e-6 {
		t.Fatalf("camel health = %v, want 32.0 (MAX_HEALTH)", c.health)
	}
	if got := c.getAttributeValue(attribute.MaxHealth); math.Abs(got-32.0) > 1e-9 {
		t.Fatalf("camel MAX_HEALTH = %v, want 32.0", got)
	}
	if got := c.getAttributeValue(attribute.MovementSpeed); math.Float64bits(got) != math.Float64bits(0.09000000357627869) {
		t.Fatalf("camel MOVEMENT_SPEED = %v, want 0.09000000357627869 (bit-exact)", got)
	}
	if got := c.getAttributeValue(attribute.StepHeight); math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("camel STEP_HEIGHT = %v, want 1.5 (override of the 0.6 base)", got)
	}
	if got := c.getAttributeValue(attribute.SafeFallDistance); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("camel SAFE_FALL_DISTANCE = %v, want 6.0 (horse base)", got)
	}
	if c.ai == nil || c.ai.rng == nil {
		t.Fatal("camel has no minimal AI / rng")
	}
}

// --- CAMEL BEHAVIOR PINS (dash / sit-stand / 2-seat ride / speeds), 1:1 javap this task ---------------

// camelRideLoop builds a single-region loop with a flat floor and one ADULT camel at (8.5, floorY+1, 8.5),
// on the ground. Returns the loop, the camel, and the floor Y. Mirrors rideTestLoop's shape.
func camelRideLoop(t *testing.T) (*TickLoop, *Entity, int) {
	t.Helper()
	loop, floorY := camelLoop(t)
	c := loop.spawnCamel(8.5, float64(floorY+1), 8.5, false)
	c.onGround = true
	return loop, c, floorY
}

// TestCamelSpeedsMatchCamelAi: the passive-goal speed multipliers are the CamelAi constants (panic 4.0,
// tempt 2.5, follow 2.5, stroll 2.0, breed 1.0), NOT the old Pig defaults.
func TestCamelSpeedsMatchCamelAi(t *testing.T) {
	if camelPanicSpeed != 4.0 {
		t.Fatalf("camelPanicSpeed = %v, want 4.0 (SPEED_MULTIPLIER_WHEN_PANICKING)", camelPanicSpeed)
	}
	if camelTemptSpeed != 2.5 {
		t.Fatalf("camelTemptSpeed = %v, want 2.5 (SPEED_MULTIPLIER_WHEN_TEMPTED)", camelTemptSpeed)
	}
	if camelFollowSpeed != 2.5 {
		t.Fatalf("camelFollowSpeed = %v, want 2.5 (SPEED_MULTIPLIER_WHEN_FOLLOWING_ADULT)", camelFollowSpeed)
	}
	if camelStrollSpeed != 2.0 {
		t.Fatalf("camelStrollSpeed = %v, want 2.0 (SPEED_MULTIPLIER_WHEN_IDLING)", camelStrollSpeed)
	}
	if camelBreedSpeed != 1.0 {
		t.Fatalf("camelBreedSpeed = %v, want 1.0 (SPEED_MULTIPLIER_WHEN_MAKING_LOVE)", camelBreedSpeed)
	}
}

// TestCamelDashImpulseAndCooldown: executeRidersJump applies the exact 1:1 velocity burst and sets
// dashCooldown = 55 + DASH true. The camel faces +Z (yaw 0), so the look vector is (0,0,1): the horizontal
// burst lands entirely on +Z, the X burst is 0, and the Y burst is 1.4285f*f*jumpPower.
func TestCamelDashImpulseAndCooldown(t *testing.T) {
	_, c, _ := camelRideLoop(t)
	c.yaw, c.pitch = 0, 0 // look +Z, level
	c.vx, c.vy, c.vz = 0, 0, 0

	const f = float32(1.0)
	c.camelExecuteRidersJump(f)

	if c.camelDashCooldown != 55 {
		t.Fatalf("dashCooldown after dash = %d, want 55 (DASH_COOLDOWN_TICKS)", c.camelDashCooldown)
	}
	if !c.camelIsDashing() {
		t.Fatal("camel not dashing after executeRidersJump")
	}
	// Expected burst (recompute the exact float chain): scale = float32(22.2222f*f) * MOVEMENT_SPEED * 1.0.
	ms := c.getAttributeValue(attribute.MovementSpeed)
	scale := float64(float32(camelDashHorizontalMomentum)*f) * ms * 1.0
	wantVZ := 1.0 * scale // look (0,0,1) -> nz = 1
	wantVY := float64(float32(camelDashVerticalMomentum)*f) * camelJumpStrength
	if math.Abs(c.vx-0.0) > 1e-12 {
		t.Fatalf("dash vx = %v, want 0 (look has no X component at yaw 0)", c.vx)
	}
	if math.Abs(c.vz-wantVZ) > 1e-9 {
		t.Fatalf("dash vz = %v, want %v (22.2222f*f*MOVEMENT_SPEED)", c.vz, wantVZ)
	}
	if math.Abs(c.vy-wantVY) > 1e-9 {
		t.Fatalf("dash vy = %v, want %v (1.4285f*f*jumpPower)", c.vy, wantVY)
	}
}

// TestCamelDashCooldownDecaysToZero: camelAiStep decrements dashCooldown by 1 per tick from 55 down to 0
// (the tick() `if (dashCooldown > 0) --dashCooldown`).
func TestCamelDashCooldownDecaysToZero(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	c.camelExecuteRidersJump(1.0) // dashCooldown = 55
	// Keep it standing/off the RandomSitting path is irrelevant; only the cooldown decay matters.
	for i := 0; i < 55; i++ {
		loop.camelAiStep(c)
	}
	if c.camelDashCooldown != 0 {
		t.Fatalf("after 55 camelAiStep ticks, dashCooldown = %d, want 0", c.camelDashCooldown)
	}
	// One more tick must not underflow below 0.
	loop.camelAiStep(c)
	if c.camelDashCooldown != 0 {
		t.Fatalf("dashCooldown underflowed to %d, want clamped at 0", c.camelDashCooldown)
	}
}

// TestCamelOnPlayerJumpArmsDash: camelOnPlayerJump on a grounded, off-cooldown camel arms the pending
// scale; camelAiStep then fires executeRidersJump (velocity burst) and clears the pending scale. A jump
// while dashCooldown > 0 is a no-op (the cooldown gate).
func TestCamelOnPlayerJumpArmsDash(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	c.yaw, c.pitch = 0, 0
	c.onGround = true
	c.camelOnPlayerJump(90) // full charge -> pending scale 1.0
	if c.horsePlayerJumpPendingScale != 1.0 {
		t.Fatalf("pending scale after onPlayerJump(90) = %v, want 1.0", c.horsePlayerJumpPendingScale)
	}
	loop.camelAiStep(c) // consumes the pending scale -> executeRidersJump
	if c.horsePlayerJumpPendingScale != 0 {
		t.Fatalf("pending scale not cleared after launch: %v", c.horsePlayerJumpPendingScale)
	}
	if c.camelDashCooldown != 55 || !c.camelIsDashing() {
		t.Fatalf("dash not launched from armed jump: cooldown=%d dashing=%v", c.camelDashCooldown, c.camelIsDashing())
	}
	// A second jump while on cooldown does nothing.
	c.horsePlayerJumpPendingScale = 0
	c.camelOnPlayerJump(90)
	if c.horsePlayerJumpPendingScale != 0 {
		t.Fatalf("onPlayerJump armed a dash while on cooldown: %v", c.horsePlayerJumpPendingScale)
	}
}

// TestCamelSitsAfterMinimalPoseTicks: an idle, grounded, un-ridden camel that has held its (standing) pose
// >= 400 ticks (minimalPoseTicks) sits down (LAST_POSE_CHANGE_TICK goes negative). Before 400 it stays
// standing.
func TestCamelSitsAfterMinimalPoseTicks(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	// spawnCamel seeded a fully-stood pose at gametime 0 (stamp = max(0, 0-52-1) = 0). Advance the loop's
	// gametime and run camelAiStep; before poseTime 400 the camel must still be standing.
	loop.gametime = 399
	loop.camelAiStep(c)
	if c.camelIsSitting() {
		t.Fatal("camel sat before minimalPoseTicks (getPoseTime 399 < 400)")
	}
	// At poseTime 400 the RandomSitting start condition passes and a standing, non-panicking camel sits.
	loop.gametime = 400
	loop.camelAiStep(c)
	if !c.camelIsSitting() {
		t.Fatalf("camel did not sit at getPoseTime 400 (minimalPoseTicks); stamp=%d", c.camelLastPoseChangeTick)
	}
}

// TestCamelSittingRefusesToMove: a sitting camel refuseToMove (isCamelSitting || isInPoseTransition), and a
// fully-stood camel does not.
func TestCamelSittingRefusesToMove(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	loop.gametime = 1000
	// Fully stood (spawn seeded stamp 0; poseTime 1000 >> 52) -> not refusing.
	if c.camelRefuseToMove(loop.gametime) {
		t.Fatal("a fully-stood camel refuseToMove == true")
	}
	// Sit it down; immediately after, it is sitting AND in the sit transition -> refuses.
	loop.camelSitDown(c)
	if !c.camelRefuseToMove(loop.gametime) {
		t.Fatal("a sitting camel refuseToMove == false")
	}
	if !c.camelIsSitting() {
		t.Fatal("sitDown did not set the sitting state (stamp not negative)")
	}
}

// TestCamelTwoRidersMount: two players mount the same camel (canAddPassenger size <= 2); a third is rejected.
func TestCamelTwoRidersMount(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	loop.gametime = 100 // fully stood (poseTime 100 > 52) so it is steerable
	p0 := ridePlayer(loop, 1, 8.5, float64(63), 8.5, 0)
	p1 := ridePlayer(loop, 2, 8.5, float64(63), 8.5, 0)
	p2 := ridePlayer(loop, 3, 8.5, float64(63), 8.5, 0)
	// tryCamelRide is the real mobInteract mount path (the getPassengers().size() < 2 cap). Two riders
	// mount; a third is refused (the interact still belongs to the camel, but no mount takes).
	if !loop.tryCamelRide(p0, c) || p0.vehicleID != c.id {
		t.Fatal("first rider failed to mount the camel via tryCamelRide")
	}
	if !loop.tryCamelRide(p1, c) || p1.vehicleID != c.id {
		t.Fatal("second rider failed to mount the camel via tryCamelRide")
	}
	if len(c.passengers) != 2 {
		t.Fatalf("camel passengers = %v, want 2 riders", c.passengers)
	}
	loop.tryCamelRide(p2, c)
	if p2.vehicleID != 0 || len(c.passengers) != 2 {
		t.Fatalf("third rider mounted a full (2-seat) camel: passengers=%v p2.vehicleID=%d", c.passengers, p2.vehicleID)
	}
}

// TestCamelControllingPassengerAndRiddenSpeed: the first player passenger is the controlling passenger of a
// STANDING camel (client-authoritative steer), and getRiddenSpeed reads MOVEMENT_SPEED. A SITTING camel has
// no controlling passenger (refuseToMove gate).
func TestCamelControllingPassengerAndRiddenSpeed(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	loop.gametime = 100 // fully stood (poseTime 100 > 52) so it is steerable
	p := ridePlayer(loop, 1, 8.5, float64(63), 8.5, 0)
	loop.playerStartRiding(p, c, false)
	if got := loop.getControllingPassenger(c); got != p.entityID {
		t.Fatalf("controlling passenger of a standing camel = %d, want %d", got, p.entityID)
	}
	// Ridden speed base is MOVEMENT_SPEED (0.09); the +0.1 sprint bonus only applies when sprinting off
	// cooldown, so the resting base equals the attribute.
	if got := c.getAttributeValue(attribute.MovementSpeed); math.Float64bits(got) != math.Float64bits(camelMovementSpeed) {
		t.Fatalf("camel MOVEMENT_SPEED (ridden base) = %v, want %v", got, camelMovementSpeed)
	}
	// Sit the camel: it now refuseToMove, so it has NO controlling passenger (un-steerable).
	loop.camelSitDown(c)
	if got := loop.getControllingPassenger(c); got != 0 {
		t.Fatalf("a sitting camel has controlling passenger %d, want 0 (refuseToMove)", got)
	}
}

// TestCamelDashViaPlayerInput: the ServerboundPlayerInput jump edge, while the player controls a standing
// camel, arms the dash (handlePlayerInput -> camelOnPlayerJump). A subsequent camelAiStep launches it.
func TestCamelDashViaPlayerInput(t *testing.T) {
	loop, c, _ := camelRideLoop(t)
	loop.gametime = 100 // fully stood (poseTime 100 > 52) so it is steerable
	c.yaw = 0
	p := ridePlayer(loop, 1, 8.5, float64(63), 8.5, 0)
	loop.playerStartRiding(p, c, false)

	// Jump-pressed input byte (FLAG_JUMP == 16). Encode a ServerboundPlayerInput packet body (a single
	// UnsignedByte) and feed it to handlePlayerInput.
	jumpPkt := pk.Marshal(int32(packetid.ServerboundPlayerInput), pk.UnsignedByte(16))
	loop.handlePlayerInput(p, jumpPkt)
	if !p.lastInputJump {
		t.Fatal("handlePlayerInput did not record the jump flag")
	}
	if c.horsePlayerJumpPendingScale != 1.0 {
		t.Fatalf("player jump input did not arm the camel dash: pending=%v", c.horsePlayerJumpPendingScale)
	}
	loop.camelAiStep(c)
	if c.camelDashCooldown != 55 || !c.camelIsDashing() {
		t.Fatalf("camel dash not launched from player input: cooldown=%d dashing=%v",
			c.camelDashCooldown, c.camelIsDashing())
	}
}
