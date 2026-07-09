package server

// ocelot_leap_test.go — the Ocelot LeapAtTargetGoal vy=0.3 port-exact test. The 26.2
// Ocelot.registerGoals @7 is LeapAtTargetGoal(this, 0.3) — the Ocelot's vertical leap component is
// 0.3 (ldc 0.3f in the ctor arg), NOT the Spider's 0.4 (ldc 0.4f in Spider.registerGoals @3). The
// kind="leap_at_target" route threads the vy= kwarg through the goal() builtin into goalDecl.leapYd,
// and buildNativeGoal reads it (defaulting to spiderLeapYd=0.4 when vy= is omitted).
//
// The pig oracle (TestPluginPigEqualsGoNativePig) is a SEPARATE, untouched mob — the spider/ocelot
// leap code never runs on the pig.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// --- TestOcelotLeapHeightIs0_3 ---------------------------------------------------------------

// TestOcelotLeapHeightIs0_3 pins the Ocelot's vertical leap component: LeapAtTargetGoal(this, 0.3)
// — the vy=0.3 kwarg on kind="leap_at_target" flows through the goal() builtin into goalDecl.leapYd
// and is threaded into newLeapAtTargetGoal by buildNativeGoal. A spider declaration WITHOUT vy=
// gets the 0.4 default (spiderLeapYd); an ocelot declaration WITH vy=0.3 gets 0.3.
//
//	[VERIFIED javap Ocelot.registerGoals @7: ldc 0.3f; invokespecial LeapAtTargetGoal.<init>(Mob, F).]
func TestOcelotLeapHeightIs0_3(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// (1) The Ocelot's vy=0.3: build the goal through the SAME path the .star uses (goalDecl ->
	// buildNativeGoal -> newLeapAtTargetGoal), asserting the yd is 0.3.
	ocelotDecl := &mobDecl{
		name:     "ocelot_test",
		baseType: entity.Ocelot,
		goals: []goalDecl{
			{priority: 7, flags: flagJump | flagMove, nativeKind: "leap_at_target", leapYdSet: true, leapYd: 0.3},
		},
	}
	m := buildAIFromDecl(loop, ocelotDecl)
	ocelotLeap, ok := m.goals.goals[0].g.(*leapAtTargetGoal)
	if !ok {
		t.Fatalf("ocelot @7 is %T, want *leapAtTargetGoal", m.goals.goals[0].g)
	}
	if ocelotLeap.yd != 0.3 {
		t.Fatalf("ocelot leap yd = %v, want 0.3 (Ocelot.registerGoals @7 LeapAtTargetGoal(this, 0.3))", ocelotLeap.yd)
	}

	// (2) The Spider's default (no vy=): the goalDecl with leapYdSet=false gets spiderLeapYd=0.4.
	spiderDecl := &mobDecl{
		name:     "spider_test",
		baseType: entity.Spider,
		goals: []goalDecl{
			{priority: 3, flags: flagJump | flagMove, nativeKind: "leap_at_target"}, // leapYdSet=false -> default
		},
	}
	m2 := buildAIFromDecl(loop, spiderDecl)
	spiderLeap, ok := m2.goals.goals[0].g.(*leapAtTargetGoal)
	if !ok {
		t.Fatalf("spider @3 is %T, want *leapAtTargetGoal", m2.goals.goals[0].g)
	}
	if spiderLeap.yd != spiderLeapYd {
		t.Fatalf("spider leap yd = %v, want %v (Spider.registerGoals @3 LeapAtTargetGoal(this, 0.4), the default when vy= is omitted)", spiderLeap.yd, spiderLeapYd)
	}
	if spiderLeap.yd == ocelotLeap.yd {
		t.Fatalf("ocelot yd (%v) must differ from spider yd (%v) — the faithful 26.2 ports use different ctor args", ocelotLeap.yd, spiderLeap.yd)
	}

	// (3) The actual impulse: start() sets vy=0.3 for the ocelot's leap (the .star's vy= kwarg lands
	// as the yd field; setDeltaMovement y = yd). This is the same impulse the spider test exercises
	// with yd=0.4.
	e := NewEntity(7201, entity.Ocelot, 8.5, 64, 8.5)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 10.0
	e.onGround = true
	p := addTestPlayer(loop, 9201, 8.5+2.0, 64, 8.5) // d² = 4.0, in the leap band
	e.ai.setTarget(p.entityID)
	leap := newLeapAtTargetGoal(0.3)
	if !leap.canUse(loop, e) {
		t.Fatal("canUse must pass for an ocelot in the leap band (on-ground, target in [4,16], nextInt(3)==0)")
	}
	leap.start(loop, e)
	if e.vy != 0.3 {
		t.Fatalf("ocelot leap vy = %v, want 0.3 (the Ocelot's 0.3F ctor arg, setDeltaMovement y = yd)", e.vy)
	}
}
