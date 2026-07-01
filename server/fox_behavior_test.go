package server

// fox_behavior_test.go - the Fox CHARACTER LAYER behavior tests (ai_goals_fox.go). Each fox goal is a
// Go-native goal reading/writing the fox DATA_FLAGS state; these tests drive the goal methods directly
// with a deterministic per-entity rng (reseeded by id) and assert the jar-faithful gating + transitions.
// The pig oracle (TestPluginPigEqualsGoNativePig) is untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

func foxTestMob(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Fox, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 10.0
	e.onGround = true
	return e
}

func TestFoxFlagRoundTrip(t *testing.T) {
	e := foxTestMob(1, 0, 64, 0)
	foxSetSitting(e, true)
	foxSetCrouching(e, true)
	foxSetInterested(e, true)
	foxSetPouncing(e, true)
	foxSetSleeping(e, true)
	foxSetFaceplanted(e, true)
	foxSetDefending(e, true)
	if !(foxIsSitting(e) && foxIsCrouching(e) && foxIsInterested(e) && foxIsPouncing(e) && foxIsSleeping(e) && foxIsFaceplanted(e) && foxIsDefending(e)) {
		t.Fatalf("not all flags set: foxFlags=%d", e.foxFlags)
	}
	// Fox.clearStates drops interested/crouching/sitting/sleeping/defending/faceplanted but NOT pouncing
	// (jar-verified: clearStates has no setIsPouncing call) — so only the POUNCING bit (16) survives.
	foxClearStates(e)
	if e.foxFlags != foxFlagPouncing {
		t.Fatalf("clearStates should leave only POUNCING set, got foxFlags=%d", e.foxFlags)
	}
	foxSetPouncing(e, false)
	foxSetSleeping(e, true)
	if !foxIsSleeping(e) || e.foxFlags != foxFlagSleeping {
		t.Fatalf("setSleeping bit wrong: foxFlags=%d", e.foxFlags)
	}
	foxWakeUp(e)
	if foxIsSleeping(e) {
		t.Fatal("wakeUp did not clear sleeping")
	}
}

func TestFoxSleepGoalDayShelter(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200

	e := foxTestMob(4040, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	loop.gametime = 6000
	g := newFoxSleepGoal()
	fired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, e) {
			fired = true
			break
		}
	}
	if !fired {
		t.Fatal("SleepGoal.canUse never fired in daytime under shelter with nothing alertable")
	}

	loop.gametime = 18000
	gNight := newFoxSleepGoal()
	e2 := foxTestMob(4041, 8.5, 64, 8.5)
	loop.only().entities.add(e2)
	nightFired := false
	for i := 0; i < 300; i++ {
		if gNight.canUse(loop, e2) {
			nightFired = true
			break
		}
	}
	if nightFired {
		t.Fatal("SleepGoal.canUse fired at NIGHT - the isBrightOutside gate is broken")
	}
}

func TestFoxSleepGoalStartSetsSleeping(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	e := foxTestMob(4042, 0, 64, 0)
	foxSetSitting(e, true)
	foxSetCrouching(e, true)
	foxSetInterested(e, true)
	g := newFoxSleepGoal()
	g.start(loop, e)
	if !foxIsSleeping(e) {
		t.Fatal("start did not set sleeping")
	}
	if foxIsSitting(e) || foxIsCrouching(e) || foxIsInterested(e) {
		t.Fatalf("start did not clear postures: foxFlags=%d", e.foxFlags)
	}
	if e.ai.hasTarget {
		t.Fatal("start did not park navigation")
	}
}

func TestFoxAiStepSleepImmobile(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	e := foxTestMob(4043, 0, 64, 0)
	loop.only().entities.add(e)
	foxSetSleeping(e, true)
	e.vx, e.vz = 0.5, -0.5
	e.jumping = true
	before := e.ticksSinceEaten
	loop.foxAiStep(e)
	if e.vx != 0 || e.vz != 0 {
		t.Fatalf("sleeping fox horizontal velocity not zeroed: vx=%v vz=%v", e.vx, e.vz)
	}
	if e.jumping {
		t.Fatal("sleeping fox jump intent not cleared")
	}
	if e.ticksSinceEaten != before+1 {
		t.Fatalf("ticksSinceEaten not advanced: got %d want %d", e.ticksSinceEaten, before+1)
	}
}

func TestFoxAiStepCrouchAnimation(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	e := foxTestMob(4044, 0, 64, 0)
	loop.only().entities.add(e)
	// A LIVE target must exist: Fox.aiStep uncrouches a target-less fox each tick (the target-gone clear),
	// so the crouch animation only progresses while the fox is stalking a live prey.
	prey := NewEntity(4045, entity.Chicken, 2, 64, 0)
	prey.health = 4.0
	loop.only().entities.add(prey)
	e.ai.setTarget(prey.id)
	foxSetCrouching(e, true)
	for i := 0; i < 40; i++ {
		loop.foxAiStep(e)
	}
	if !foxIsFullyCrouched(e) {
		t.Fatalf("crouchAmount did not reach 5.0: got %v", e.crouchAmount)
	}
	foxSetCrouching(e, false)
	loop.foxAiStep(e)
	if e.crouchAmount != 0 {
		t.Fatalf("crouchAmount not reset to 0: got %v", e.crouchAmount)
	}
}

func TestFoxStalkGoalCrouchesOnApproach(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	fox := foxTestMob(4050, 0, 64, 0)
	loop.only().entities.add(fox)
	chicken := NewEntity(4051, entity.Chicken, 3, 64, 0)
	chicken.health = 4.0
	loop.only().entities.add(chicken)
	fox.ai.setTarget(chicken.id)

	if newFoxStalkPreyGoal().canUse(loop, fox) {
		t.Fatal("StalkPreyGoal.canUse fired within 6 blocks (needs distSqr > 36)")
	}
	g := newFoxStalkPreyGoal()
	g.tick(loop, fox)
	if !foxIsCrouching(fox) || !foxIsInterested(fox) {
		t.Fatalf("stalk tick within range did not crouch/interest: foxFlags=%d", fox.foxFlags)
	}

	far := NewEntity(4052, entity.Chicken, 10, 64, 0)
	far.health = 4.0
	loop.only().entities.add(far)
	fox2 := foxTestMob(4053, 0, 64, 0)
	loop.only().entities.add(fox2)
	fox2.ai.setTarget(far.id)
	if !newFoxStalkPreyGoal().canUse(loop, fox2) {
		t.Fatal("StalkPreyGoal.canUse did not fire on a distant stalkable prey")
	}
	g2 := newFoxStalkPreyGoal()
	g2.tick(loop, fox2)
	if !fox2.ai.hasTarget {
		t.Fatal("stalk tick out of range did not set a chase want-target")
	}
}

func TestFoxPounceGoalLeapsAndHurts(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	fox := foxTestMob(4060, 0, 64, 0)
	fox.crouchAmount = foxMaxCrouchAmount
	loop.only().entities.add(fox)
	rabbit := NewEntity(4061, entity.Rabbit, 1.5, 64, 0)
	rabbit.health = 3.0
	loop.only().entities.add(rabbit)
	fox.ai.setTarget(rabbit.id)

	g := newFoxPounceGoal()
	if !g.canUse(loop, fox) {
		t.Fatal("FoxPounceGoal.canUse did not fire while fully crouched with a clear path")
	}
	beforeVY := fox.vy
	g.start(loop, fox)
	if fox.vy <= beforeVY {
		t.Fatalf("pounce start did not add the up impulse: vy %v -> %v", beforeVY, fox.vy)
	}
	if !foxIsPouncing(fox) {
		t.Fatal("pounce start did not set pouncing")
	}
	hp := rabbit.health
	g.tick(loop, fox)
	if rabbit.health >= hp {
		t.Fatalf("pounce tick did not hurt the prey within 2 blocks: hp %v -> %v", hp, rabbit.health)
	}
}

func TestFoxFaceplantGoalCountdown(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	e := foxTestMob(4070, 0, 64, 0)
	g := newFoxFaceplantGoal()
	if g.canUse(loop, e) {
		t.Fatal("FaceplantGoal.canUse fired without the faceplanted flag")
	}
	foxSetFaceplanted(e, true)
	if !g.canUse(loop, e) {
		t.Fatal("FaceplantGoal.canUse did not fire with the faceplanted flag")
	}
	g.start(loop, e)
	if g.countdown != foxFaceplantTicks {
		t.Fatalf("start countdown = %d, want %d", g.countdown, foxFaceplantTicks)
	}
	g.tick(loop, e)
	if g.countdown != foxFaceplantTicks-1 {
		t.Fatalf("tick did not decrement countdown: got %d", g.countdown)
	}
	g.stop(loop, e)
	if foxIsFaceplanted(e) {
		t.Fatal("stop did not clear the faceplanted flag")
	}
}

func TestFoxLandTargetAcquiresPrey(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	fox := foxTestMob(4080, 0, 64, 0)
	loop.only().entities.add(fox)
	chicken := NewEntity(4081, entity.Chicken, 4, 64, 0)
	chicken.health = 4.0
	loop.only().entities.add(chicken)

	g := newFoxLandTargetGoal()
	acquired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			acquired = true
			break
		}
	}
	if !acquired {
		t.Fatal("fox_land_target never acquired a nearby chicken")
	}
	// canUse acquires into g.target; start() commits it to mobAI.setTarget (the NearestAttackableTargetGoal
	// canUse-then-start contract). Drive start() then assert the committed target.
	g.start(loop, fox)
	if fox.ai.getTarget() != chicken.id {
		t.Fatalf("fox_land_target committed the wrong target: got %d want %d", fox.ai.getTarget(), chicken.id)
	}
}

func TestFoxDefendTrustedGoal(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	fox := foxTestMob(4090, 0, 64, 0)
	foxSetSleeping(fox, true)
	loop.only().entities.add(fox)

	trusted := foxTestMob(4091, 1, 64, 0)
	loop.only().entities.add(trusted)
	fox.foxTrusted0 = trusted.id

	attacker := NewEntity(4092, entity.Zombie, 2, 64, 0)
	attacker.health = 20.0
	loop.only().entities.add(attacker)
	trusted.lastHurtByMob = attacker.id
	trusted.lastHurtByMobTimestamp = 55

	g := newFoxDefendTrustedGoal()
	fired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, fox) {
			fired = true
			break
		}
	}
	if !fired {
		t.Fatal("DefendTrustedTargetGoal.canUse never fired for a hurt trusted mob")
	}
	g.start(loop, fox)
	if fox.ai.getTarget() != attacker.id {
		t.Fatalf("defend start did not target the attacker: got %d want %d", fox.ai.getTarget(), attacker.id)
	}
	if !foxIsDefending(fox) {
		t.Fatal("defend start did not set defending")
	}
	if foxIsSleeping(fox) {
		t.Fatal("defend start did not wake the fox")
	}
}
