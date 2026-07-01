package server

// skeleton_sun_test.go — the AbstractSkeleton daytime burn-avoidance goals (ai_goals_skeleton_sun.go):
// RestrictSunGoal (@2) + FleeSunGoal (@3). Verifies the jar gates: FleeSunGoal fires ONLY when it is bright
// out, the skeleton is on fire, sky-exposed, and has no target; RestrictSunGoal flips the navigation
// avoid-sun bit while bright. Deterministic per-mob RNG (seeded like the fox behavior tests).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// sunTestMob builds a seeded skeleton test mob with a live AI + attributes (mirrors foxTestMob).
func sunTestMob(id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Skeleton, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 20.0
	e.onGround = true
	return e
}

// TestFleeSunGoalGates: FleeSunGoal.canUse requires bright + on-fire + sky-exposed + no-target. It fires
// for a burning day-time sky-exposed skeleton, and each guard independently blocks it.
func TestFleeSunGoalGates(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 64 // e.y (64) >= surfaceY -> canSeeSky true; a candidate a few blocks DOWN (cy<64) is shaded
	loop.gametime = 6000    // midday -> !isDarkEnoughToSpawn (bright)

	e := sunTestMob(7001, 8.5, 64, 8.5)
	loop.only().entities.add(e)
	e.remainingFireTicks = 200 // on fire

	g := newFleeSunGoal(skeletonFleeSunSpeed)
	fired := false
	for i := 0; i < 200; i++ {
		if g.canUse(loop, e) {
			fired = true
			break
		}
	}
	if !fired {
		t.Fatal("FleeSunGoal.canUse never fired for a burning, day-time, sky-exposed, target-less skeleton")
	}

	// Guard 1: not on fire -> never fires.
	e2 := sunTestMob(7002, 8.5, 64, 8.5)
	loop.only().entities.add(e2)
	e2.remainingFireTicks = 0
	gNoFire := newFleeSunGoal(skeletonFleeSunSpeed)
	for i := 0; i < 100; i++ {
		if gNoFire.canUse(loop, e2) {
			t.Fatal("FleeSunGoal fired for a skeleton that is NOT on fire (the isOnFire guard is broken)")
		}
	}

	// Guard 2: night -> never fires (isBrightOutside gate).
	loop.gametime = 18000 // deep night -> isDarkEnoughToSpawn true
	e3 := sunTestMob(7003, 8.5, 64, 8.5)
	loop.only().entities.add(e3)
	e3.remainingFireTicks = 200
	gNight := newFleeSunGoal(skeletonFleeSunSpeed)
	for i := 0; i < 100; i++ {
		if gNight.canUse(loop, e3) {
			t.Fatal("FleeSunGoal fired at NIGHT (the isBrightOutside gate is broken)")
		}
	}

	// Guard 3: has a target -> never fires.
	loop.gametime = 6000
	e4 := sunTestMob(7004, 8.5, 64, 8.5)
	loop.only().entities.add(e4)
	e4.remainingFireTicks = 200
	e4.ai.setTarget(999)
	gTarget := newFleeSunGoal(skeletonFleeSunSpeed)
	for i := 0; i < 100; i++ {
		if gTarget.canUse(loop, e4) {
			t.Fatal("FleeSunGoal fired while the skeleton has a target (the getTarget()==null gate is broken)")
		}
	}
}

// TestFleeSunGoalStartSetsWant: after a firing canUse, start() commits a navigation want-target (the mob
// moves toward the getHidePos shade tile).
func TestFleeSunGoalStartSetsWant(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 64
	loop.gametime = 6000

	e := sunTestMob(7010, 8.5, 64, 8.5)
	loop.only().entities.add(e)
	e.remainingFireTicks = 200

	g := newFleeSunGoal(skeletonFleeSunSpeed)
	if !g.canUse(loop, e) {
		t.Fatal("canUse did not fire (precondition for start test)")
	}
	if !g.haveWant {
		t.Fatal("canUse did not cache a wanted hide position")
	}
	g.start(loop, e)
	if !e.ai.hasTarget {
		t.Fatal("start did not commit a navigation want-target")
	}
}

// TestRestrictSunGoalTogglesAvoidSun: RestrictSunGoal fires while bright, and start/stop flip the
// navigation avoidSun bit.
func TestRestrictSunGoalTogglesAvoidSun(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.gametime = 6000 // bright

	e := sunTestMob(7020, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	g := newRestrictSunGoal()
	if !g.canUse(loop, e) {
		t.Fatal("RestrictSunGoal.canUse did not fire in daytime")
	}
	if g.flags() != 0 {
		t.Fatalf("RestrictSunGoal must hold NO flags, got %d", g.flags())
	}
	g.start(loop, e)
	if !e.ai.navigation.avoidSun {
		t.Fatal("start did not set avoidSun")
	}
	g.stop(loop, e)
	if e.ai.navigation.avoidSun {
		t.Fatal("stop did not clear avoidSun")
	}

	// Night: the goal does not fire.
	loop.gametime = 18000
	if g.canUse(loop, e) {
		t.Fatal("RestrictSunGoal fired at NIGHT (the isBrightOutside gate is broken)")
	}
}

// TestSkeletonSunGoalsBootLoad: the boot-loaded skeleton carries the two sun goals at their jar priorities
// (restrict_sun@2 has no flags -> goals.goals; flee_sun@3 {MOVE} -> goals.goals). This is redundant with
// TestSkeletonBootLoads' count but pins the sun-goal-specific priorities so a future edit that drops one
// fails loudly here.
func TestSkeletonSunGoalsBootLoad(t *testing.T) {
	loop, floorY, _ := skeletonLoop(t)
	skel := spawnSkeleton(loop, 8.5, float64(floorY+1), 8.5)
	seen := map[int]int{}
	for _, wg := range skel.ai.goals.goals {
		seen[wg.priority]++
	}
	// restrict_sun@2 + flee_sun@3 are the only @2/@3 goalSelector goals on the skeleton.
	if seen[2] != 1 {
		t.Fatalf("skeleton missing restrict_sun@2 (goalSelector priority 2 count = %d)", seen[2])
	}
	if seen[3] != 1 {
		t.Fatalf("skeleton missing flee_sun@3 (goalSelector priority 3 count = %d)", seen[3])
	}
}
