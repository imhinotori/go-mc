package server

// rabbit_hop_test.go — MOB-PASS-05 (rabbit hop): deterministic regression of the Rabbit hop state machine
// (RabbitJumpControl + RabbitMoveControl + Rabbit.customServerAiStep/aiStep + Rabbit.jumpFromGround),
// ported 1:1 from net.minecraft.world.entity.animal.rabbit.Rabbit. Mirrors chicken_test.go's pattern of
// driving the per-type hook (rabbitAiStep / rabbitJumpFromGround) directly on a hand-built rabbit Entity
// (no plugin spawn needed for the state-machine assertions). The per-mob rng is seeded like the other mob
// tests (newEntityRandom(defaultEntityRandomSeed) + reseedMobAI), though the hop path draws NO RNG (all
// the state-machine transitions are pure integer/float math — a deferred nextInt(3) moreCarrotTicks decay
// never fires), so the assertions are fully deterministic.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// newTestRabbit builds a live, on-ground rabbit Entity with a fresh mobAI (seeded rng), ready to drive
// rabbitAiStep. Seeded deterministically like newTestCat/the target-goal tests.
func newTestRabbit(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Rabbit, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 3.0 // Rabbit MAX_HEALTH (a live rabbit — isAlive requires health > 0)
	e.onGround = true
	return e
}

// TestRabbitLandingDelayNormalVsPanic asserts Rabbit.setLandingDelay: a strolling rabbit
// (speedModifier < FLEE_SPEED_MOD 2.2) rests JUMP_DELAY_TICKS (10) between hops; a fleeing rabbit
// (speedModifier >= 2.2) rests only PANIC_JUMP_DELAY_TICKS (3).
func TestRabbitLandingDelayNormalVsPanic(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Stroll speed (0.6 < 2.2) -> the 10-tick normal delay.
	rb := &rabbitHopState{speedModifier: rabbitStrollSpeedMod}
	e := newTestRabbit(7001, 8.5, 64, 8.5)
	e.ai.rabbit = rb
	loop.rabbitSetLandingDelay(e, rb)
	if rb.jumpDelayTicks != rabbitJumpDelayTicks {
		t.Fatalf("stroll landing delay = %d, want %d (JUMP_DELAY_TICKS)", rb.jumpDelayTicks, rabbitJumpDelayTicks)
	}

	// Flee speed (2.2 >= 2.2) -> the 3-tick panic delay.
	rb2 := &rabbitHopState{speedModifier: rabbitFleeSpeedMod}
	loop.rabbitSetLandingDelay(e, rb2)
	if rb2.jumpDelayTicks != rabbitPanicJumpDelayTicks {
		t.Fatalf("flee landing delay = %d, want %d (PANIC_JUMP_DELAY_TICKS)", rb2.jumpDelayTicks, rabbitPanicJumpDelayTicks)
	}
}

// TestRabbitFreshLandingChecksDelay asserts Rabbit.customServerAiStep's on-ground fresh-landing branch:
// onGround && !wasOnGround -> setJumping(false) + checkLandingDelay (setLandingDelay + disableJumpControl).
// A rabbit whose wasOnGround was false (it just landed) must clear jumping, set jumpDelayTicks, and
// disable the jump control.
func TestRabbitFreshLandingChecksDelay(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := newTestRabbit(7002, 8.5, 64, 8.5)
	rb := &rabbitHopState{}
	e.ai.rabbit = rb
	rb.wasOnGround = false // it just landed this tick
	rb.jumpControlCanJump = true
	e.jumping = true
	rb.speedModifier = rabbitStrollSpeedMod // -> the 10-tick normal delay

	loop.rabbitCustomServerAiStep(e, rb)

	if e.jumping {
		t.Fatal("fresh landing did not setJumping(false)")
	}
	if rb.jumpDelayTicks != rabbitJumpDelayTicks {
		t.Fatalf("fresh landing jumpDelayTicks = %d, want %d (checkLandingDelay -> setLandingDelay)", rb.jumpDelayTicks, rabbitJumpDelayTicks)
	}
	if rb.jumpControlCanJump {
		t.Fatal("fresh landing did not disableJumpControl (canJump still true)")
	}
	if !rb.wasOnGround {
		t.Fatal("customServerAiStep did not refresh wasOnGround = onGround() (true)")
	}
}

// TestRabbitStartsJumpingWhenWanted asserts the RabbitJumpControl arbitration: on ground, with a MOVE
// goal want committed (hasWanted) and the ground rest elapsed (jumpDelayTicks==0) and no jump yet wanted,
// customServerAiStep calls startJumping — setJumping(true), jumpDuration=15, jumpTicks=0.
func TestRabbitStartsJumpingWhenWanted(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := newTestRabbit(7003, 8.5, 64, 8.5)
	rb := &rabbitHopState{}
	e.ai.rabbit = rb
	rb.wasOnGround = true // NOT a fresh landing (so the checkLandingDelay branch is skipped)
	rb.jumpDelayTicks = 0 // the ground rest has elapsed
	e.ai.hasTarget = true // a MOVE goal committed a want (hasWanted)

	loop.rabbitCustomServerAiStep(e, rb)

	if !e.jumping {
		t.Fatal("customServerAiStep did not startJumping (jumping still false) despite hasWanted && jumpDelayTicks==0")
	}
	if rb.jumpDuration != rabbitJumpDurationInTicks {
		t.Fatalf("startJumping jumpDuration = %d, want %d (JUMP_DURATION_IN_TICKS)", rb.jumpDuration, rabbitJumpDurationInTicks)
	}
	if rb.jumpTicks != 0 {
		t.Fatalf("startJumping jumpTicks = %d, want 0", rb.jumpTicks)
	}
}

// TestRabbitNoJumpDuringLandingDelay asserts the jumpDelayTicks gate: while the ground rest is still
// counting down (jumpDelayTicks > 0), a wanting rabbit does NOT startJumping — it waits. customServerAiStep
// decrements jumpDelayTicks by 1 each call (the countdown), and only at 0 may a hop start.
func TestRabbitNoJumpDuringLandingDelay(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := newTestRabbit(7004, 8.5, 64, 8.5)
	rb := &rabbitHopState{}
	e.ai.rabbit = rb
	rb.wasOnGround = true
	rb.jumpDelayTicks = 2 // still resting
	e.ai.hasTarget = true

	loop.rabbitCustomServerAiStep(e, rb)
	if e.jumping {
		t.Fatal("rabbit hopped while jumpDelayTicks > 0 (should wait out the landing delay)")
	}
	if rb.jumpDelayTicks != 1 {
		t.Fatalf("jumpDelayTicks after one tick = %d, want 1 (decrement by 1)", rb.jumpDelayTicks)
	}

	// Next tick: still resting (1 -> 0 this tick), so this call decrements to 0 but does not yet hop
	// (the hop check reads jumpDelayTicks AFTER the decrement — at 0 the SAME tick may hop).
	loop.rabbitCustomServerAiStep(e, rb)
	if rb.jumpDelayTicks != 0 {
		t.Fatalf("jumpDelayTicks = %d, want 0 after the second tick", rb.jumpDelayTicks)
	}
	// At 0 with a want, the hop fires on this same tick (jumpDelayTicks was decremented 1->0 first).
	if !e.jumping {
		t.Fatal("rabbit did not hop once jumpDelayTicks reached 0 with a want")
	}
}

// TestRabbitAiStepCounterArc asserts the Rabbit.aiStep tail counter: jumpTicks advances toward
// jumpDuration each tick, and at the arc end (jumpTicks == jumpDuration != 0) it resets both to 0 and
// clears jumping — the render hop-arc lifetime (getJumpCompletion goes 0..1 then resets).
func TestRabbitAiStepCounterArc(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := newTestRabbit(7005, 8.5, 64, 8.5)
	rb := &rabbitHopState{}
	e.ai.rabbit = rb

	// Start an arc: jumpDuration=15, jumpTicks=0, jumping=true (startJumping state).
	rb.jumpDuration = rabbitJumpDurationInTicks
	rb.jumpTicks = 0
	e.jumping = true

	// Advance jumpDuration ticks: jumpTicks climbs 0->15, then the arc-end tick clears everything.
	for i := 0; i < rabbitJumpDurationInTicks; i++ {
		loop.rabbitAiStepCounter(e, rb)
		wantTicks := i + 1
		if rb.jumpTicks != wantTicks {
			t.Fatalf("tick %d: jumpTicks = %d, want %d (advance toward jumpDuration)", i, rb.jumpTicks, wantTicks)
		}
	}
	// jumpTicks now == jumpDuration (15). The NEXT counter tick is the arc end: reset + setJumping(false).
	loop.rabbitAiStepCounter(e, rb)
	if rb.jumpTicks != 0 || rb.jumpDuration != 0 {
		t.Fatalf("arc end: jumpTicks=%d jumpDuration=%d, want 0/0 (reset)", rb.jumpTicks, rb.jumpDuration)
	}
	if e.jumping {
		t.Fatal("arc end did not setJumping(false)")
	}
}

// TestRabbitJumpFromGroundAdultVsBaby asserts Rabbit.jumpFromGround: the land jump sets vy to the rabbit
// getJumpPower (stroll speed -> 0.2), and — from rest with an active move speed — adds the horizontal
// moveRelative(0.1, Vec3(0, jumpHeight, 1)) launch, taller for an adult (1.5) than a baby (0.5). With yaw
// 0 the launch is purely +z (south): dz = jumpHeight * (0.1 / |input|) where |input| = sqrt(jh^2 + 1).
func TestRabbitJumpFromGroundAdultVsBaby(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Adult rabbit at rest, strolling (speedModifier 0.6 -> getJumpPower f=0.2), yaw 0.
	e := newTestRabbit(7006, 8.5, 64, 8.5)
	rb := &rabbitHopState{speedModifier: rabbitStrollSpeedMod}
	e.ai.rabbit = rb
	e.breedAge = 0 // an ADULT (isBaby == age < 0)
	e.yaw = 0
	e.vx, e.vy, e.vz = 0, 0, 0

	loop.rabbitJumpFromGround(e)

	// Vertical: getJumpPower at stroll speed = baseJumpPower*(0.2/0.42) == 0.2 (the 0.42 cancels).
	wantVY := baseJumpPower * (0.2 / 0.42)
	// The horizontal launch adds getInputVector(0, jumpHeight, 1, 0.1, yaw=0).y to vy as well.
	ldx, ldy, ldz := getInputVectorRotated(0.0, rabbitAdultJumpHeight, 1.0, rabbitJumpMoveRelativeSpeed, 0)
	wantVY += ldy
	if math.Abs(e.vy-wantVY) > 1e-9 {
		t.Fatalf("adult jumpFromGround vy = %v, want %v (getJumpPower 0.2 + launch y)", e.vy, wantVY)
	}
	// Horizontal: yaw 0 -> the launch is +z only (dx ~ 0, dz = ldz > 0).
	if math.Abs(e.vz-ldz) > 1e-9 {
		t.Fatalf("adult jumpFromGround vz = %v, want %v (moveRelative +z launch)", e.vz, ldz)
	}
	if math.Abs(e.vx-ldx) > 1e-9 {
		t.Fatalf("adult jumpFromGround vx = %v, want %v (~0 at yaw 0)", e.vx, ldx)
	}

	// A BABY rabbit launches with the SHORTER 0.5 jump height (a smaller horizontal push).
	baby := newTestRabbit(7007, 8.5, 64, 8.5)
	rbb := &rabbitHopState{speedModifier: rabbitStrollSpeedMod}
	baby.ai.rabbit = rbb
	baby.breedAge = -24000 // a fresh baby (isBaby true)
	baby.yaw = 0
	baby.vx, baby.vy, baby.vz = 0, 0, 0

	loop.rabbitJumpFromGround(baby)

	_, babyDY, babyDZ := getInputVectorRotated(0.0, rabbitBabyJumpHeight, 1.0, rabbitJumpMoveRelativeSpeed, 0)
	if math.Abs(baby.vz-babyDZ) > 1e-9 {
		t.Fatalf("baby jumpFromGround vz = %v, want %v (BABY_JUMP_HEIGHT 0.5 launch)", baby.vz, babyDZ)
	}
	// getInputVector NORMALIZES the (0, jh, 1) launch, so a TALLER adult hop (jh 1.5) puts MORE of the
	// fixed-length 0.1 launch into the VERTICAL (dy) and LESS into the horizontal (dz), while the shorter
	// baby hop (jh 0.5) travels FARTHER horizontally. This is the vanilla-faithful trade-off (Rabbit.
	// jumpFromGround moveRelative(0.1, Vec3(0, jh, 1))): the adult bounds higher, the baby scoots farther.
	if !(baby.vz > e.vz) {
		t.Fatalf("baby launch dz (%v) not larger than adult (%v) — the shorter baby hop travels farther horizontally", baby.vz, e.vz)
	}
	// And the adult puts MORE of the launch into the vertical than the baby (taller bound).
	adultLaunchDY := ldy
	if !(adultLaunchDY > babyDY) {
		t.Fatalf("adult launch dy (%v) not larger than baby (%v) — the taller adult hop bounds higher", adultLaunchDY, babyDY)
	}
}

// TestRabbitJumpFromGroundNoLaunchWhenMoving asserts the horizontalDistanceSqr < 0.01 gate: a rabbit
// already gliding horizontally (horizSq >= 0.01) gets the vertical land jump but NO extra moveRelative
// launch (the launch is only a from-rest kick).
func TestRabbitJumpFromGroundNoLaunchWhenMoving(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := newTestRabbit(7008, 8.5, 64, 8.5)
	rb := &rabbitHopState{speedModifier: rabbitStrollSpeedMod}
	e.ai.rabbit = rb
	e.breedAge = 0
	e.yaw = 0
	// Already moving fast horizontally: horizSq = 0.2^2 + 0.2^2 = 0.08 >= 0.01 -> no launch.
	e.vx, e.vz = 0.2, 0.2
	e.vy = 0
	beforeVX, beforeVZ := e.vx, e.vz

	loop.rabbitJumpFromGround(e)

	// Vertical still applies (getJumpPower 0.2), but NO moveRelative launch -> x/z unchanged.
	if e.vx != beforeVX || e.vz != beforeVZ {
		t.Fatalf("moving rabbit got a launch (vx=%v vz=%v), want unchanged (%v/%v) — the horizontalDistanceSqr gate", e.vx, e.vz, beforeVX, beforeVZ)
	}
	wantVY := baseJumpPower * (0.2 / 0.42)
	if math.Abs(e.vy-wantVY) > 1e-9 {
		t.Fatalf("moving rabbit vy = %v, want %v (the vertical land jump still applies)", e.vy, wantVY)
	}
}

// TestRabbitAiStepIsRabbitOnly asserts the per-type gate: rabbitAiStep lazily allocates the hop state on
// the first call for a rabbit, and a non-rabbit entity is never touched (the tick_phases gate ensures
// only entity.Rabbit.ID reaches the hook — this test proves the hook itself is a safe no-op guard).
func TestRabbitAiStepAllocatesState(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := newTestRabbit(7009, 8.5, 64, 8.5)
	if e.ai.rabbit != nil {
		t.Fatal("fresh rabbit already had hop state (should lazily alloc in rabbitAiStep)")
	}
	loop.rabbitAiStep(e)
	if e.ai.rabbit == nil {
		t.Fatal("rabbitAiStep did not lazily allocate the hop state")
	}
	// A dead rabbit is a safe no-op (no panic, no alloc side effects beyond the guard).
	dead := NewEntity(7010, entity.Rabbit, 8.5, 64, 8.5)
	dead.ai = &mobAI{}
	dead.dead = true
	loop.rabbitAiStep(dead) // must not panic
}
