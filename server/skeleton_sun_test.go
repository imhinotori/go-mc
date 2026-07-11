package server

// skeleton_sun_test.go — the AbstractSkeleton daytime burn-avoidance goals (ai_goals_skeleton_sun.go):
// RestrictSunGoal (@2) + FleeSunGoal (@3). Verifies the jar gates: FleeSunGoal fires ONLY when it is bright
// out, the skeleton is on fire, sky-exposed, has no target, AND has no HEAD armor; RestrictSunGoal flips
// the navigation avoid-sun bit while bright AND with no HEAD armor. The post-A* avoid-sun TRIM is the
// jar-faithful mechanism (GroundPathNavigation.trimPath, NOT a per-node costMalus — verified via
// javap -c -p net.minecraft.world.level.pathfinder.WalkNodeEvaluator.getPathType this session, which
// has no isSunSensitive/getLightLevel/SkyLight reference). Deterministic per-mob RNG (seeded like the
// fox behavior tests).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
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
	// restrict_sun@2 is the sole @2 goalSelector goal; @3 holds BOTH flee_sun AND avoid_entity<wolf>
	// (AbstractSkeleton.registerGoals adds FleeSunGoal@3 and AvoidEntityGoal<Wolf>@3 at the same priority).
	if seen[2] != 1 {
		t.Fatalf("skeleton missing restrict_sun@2 (goalSelector priority 2 count = %d)", seen[2])
	}
	if seen[3] != 2 {
		t.Fatalf("skeleton @3 goalSelector count = %d, want 2 (flee_sun@3 + avoid_entity<wolf>@3)", seen[3])
	}
}

// --- HEAD helmet gate (the 1:1 ItemStack.isEmpty() head-armor read in both goals) ---

// TestRestrictSunGoalSetsAvoidSunFlag: the Entity.isAvoidSun wrapper mirrors e.ai.navigation.avoidSun.
// RestrictSunGoal.start -> setAvoidSun(true) flips BOTH the navigation flag AND the e.isAvoidSun()
// accessor; .stop mirrors with false. The wrapper is a thin forward over the nav object (the canonical
// state lives where the jar puts it: GroundPathNavigation.avoidSun, VERIFIED CFR).
//
//	Cite: net.minecraft.world.entity.ai.goal.RestrictSunGoal.start -> GroundPathNavigation.setAvoidSun(true);
//	      .stop -> setAvoidSun(false).
func TestRestrictSunGoalSetsAvoidSunFlag(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.gametime = 6000 // bright daytime

	e := sunTestMob(7030, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	if e.isAvoidSun() {
		t.Fatal("a freshly spawned skeleton is already in avoidSun mode (the goal must NOT have pre-fired)")
	}

	g := newRestrictSunGoal()
	if !g.canUse(loop, e) {
		t.Fatal("RestrictSunGoal.canUse did not fire during daytime")
	}
	g.start(loop, e)
	if !e.isAvoidSun() {
		t.Fatal("start did not flip Entity.isAvoidSun to true (the entity accessor mirrors the nav flag)")
	}
	if !e.ai.navigation.avoidSun {
		t.Fatal("start did not flip the underlying nav.avoidSun to true")
	}
}

// TestRestrictSunGoalClearsAvoidSunOnStop: the stop path flips the bit back via e.setAvoidSun(false).
//
//	Cite: RestrictSunGoal.stop -> GroundPathNavigation.setAvoidSun(false).
func TestRestrictSunGoalClearsAvoidSunOnStop(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.gametime = 6000 // bright daytime

	e := sunTestMob(7031, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	// Direct API: setAvoidSun(true/false) flips the accessor (and the underlying nav.avoidSun).
	e.setAvoidSun(true)
	if !e.isAvoidSun() {
		t.Fatal("setAvoidSun(true) did not flip the accessor")
	}
	e.setAvoidSun(false)
	if e.isAvoidSun() {
		t.Fatal("setAvoidSun(false) did not clear the accessor")
	}
	if e.ai.navigation.avoidSun {
		t.Fatal("setAvoidSun(false) did not clear the underlying nav.avoidSun flag")
	}

	// Start/stop sequence clears as well.
	g := newRestrictSunGoal()
	g.start(loop, e)
	if !e.isAvoidSun() {
		t.Fatal("start did not flip avoidSun via the goal")
	}
	g.stop(loop, e)
	if e.isAvoidSun() {
		t.Fatal("stop did not clear avoidSun via the goal")
	}
}

// TestSkeletonWithHeadBlockAvoidsSun: a skeleton wearing a leather helmet has its HEAD slot Count>0.
// Both RestrictSunGoal.canUse and FleeSunGoal.canUse must gate off (the jar ItemStack.isEmpty() guard).
// Cite: Skeleton$RestrictSunGoal.canUse && Zombie$RestrictSunGoal.canUse: getItemBySlot(HEAD).isEmpty()
// must be true for the goal to fire — a non-empty HEAD slot means a helmet or carved pumpkin, which
// blocks the day-time burn path in vanilla.
func TestSkeletonWithHeadBlockAvoidsSun(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 64
	loop.gametime = 6000 // bright daytime, sky-exposed via the superflat stub

	e := sunTestMob(7040, 8.5, 64, 8.5)
	loop.only().entities.add(e)
	// Equip HEAD with a real helmet (leather cap, Count=1, non-empty).
	e.setItemSlot(eqSlotHead, component.SlotData{ItemID: pk.VarInt(item.LeatherHelmet.ID), Count: 1})
	e.remainingFireTicks = 200

	// RestrictSunGoal gates off when HEAD is non-empty.
	gRestrict := newRestrictSunGoal()
	if gRestrict.canUse(loop, e) {
		t.Fatal("RestrictSunGoal.canUse fired for a helmeted skeleton (the getItemBySlot(HEAD).isEmpty() guard is broken)")
	}

	// FleeSunGoal gates off when HEAD is non-empty (burning + sky-exposed otherwise would set it off).
	gFlee := newFleeSunGoal(skeletonFleeSunSpeed)
	for i := 0; i < 100; i++ {
		if gFlee.canUse(loop, e) {
			t.Fatal("FleeSunGoal.canUse fired for a helmeted burning skeleton (the getItemBySlot(HEAD).isEmpty() guard is broken)")
		}
	}
}

// TestSkeletonWithoutHeadBlockFleesSun: the canonical bare-skeleton case still fires the sun goals.
// RestrictSunGoal.canUse returns true, then start flips avoidSun=true; FleeSunGoal fires when the
// burning guard is added (it KEEPS the isOnFire() guard FleeSunGoal drops in Fox.SeekShelter —
// faithful). Cite: Skeleton$RestrictSunGoal.canUse && Skeleton$FleeSunGoal.canUse.
func TestSkeletonWithoutHeadBlockFleesSun(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 64
	loop.gametime = 6000 // bright + sky-exposed

	// BARE skeleton: no HEAD equipment.
	eBare := sunTestMob(7050, 8.5, 64, 8.5)
	loop.only().entities.add(eBare)
	gRestrict := newRestrictSunGoal()
	if !gRestrict.canUse(loop, eBare) {
		t.Fatal("RestrictSunGoal.canUse did not fire for a bare day-time skeleton")
	}
	gRestrict.start(loop, eBare)
	if !eBare.isAvoidSun() {
		t.Fatal("RestrictSunGoal.start did not flip Entity.isAvoidSun for a bare day-time skeleton")
	}

	// FleeSunGoal: bare + on-fire + bright + sky-exposed -> fires.
	eOnFire := sunTestMob(7051, 8.5, 64, 8.5)
	loop.only().entities.add(eOnFire)
	eOnFire.remainingFireTicks = 200
	gFlee := newFleeSunGoal(skeletonFleeSunSpeed)
	fired := false
	for i := 0; i < 200 && !fired; i++ {
		if gFlee.canUse(loop, eOnFire) {
			fired = true
		}
	}
	if !fired {
		t.Fatal("FleeSunGoal.canUse never fired for a bare, burning, day-time, sky-exposed skeleton (regression — the bare-skeleton default path)")
	}
}

// TestWalkNodeEvaluatorSunExposedMalus: the post-A* avoid-sun TRIM is the jar-faithful sun-exposure
// behavior, NOT a per-node path-type malus (WalkNodeEvaluator.getPathType has NO isSunSensitive /
// getLightLevel / SkyLight reference — verified via javap -c -p this session). The observable
// effect for a shaded day-time skeleton: a path whose MIDDLE/END nodes are sky-exposed is TRUNCATED
// at the first sky-exposed node via trimPathAvoidSun (VERIFIED CFR GroundPathNavigation.trimPath).
//
// This test asserts the practical jar-faithful behavior: a multi-node path through a partly-shaded
// world is truncated at the first sky-exposed node when avoidSun is set. The "node cost" the
// pathfinder sees is NOT inflated by a SUN_EXPOSED malus — that variant would be a non-jar
// behavioral deviation (the 26.2 jar does not have such a variant in PathType — verified via the
// full 27-value enum export).
//
// Stub: the path is synthetic (no live A*), the canSeeSky read uses the superflat stub
// (y >= spawnSurfaceY). A node at y >= spawnSurfaceY is sky-exposed; below is shade.
//
//	Cite: net.minecraft.world.entity.ai.navigation.GroundPathNavigation.trimPath
//	      (VERIFIED CFR, this session: super.trimPath; if (avoidSun) { if (canSeeSky(mobPos))
//	      return; for (Node n : path.nodes) if (canSeeSky(n)) { path.truncateNodes(i); return; } }).
func TestWalkNodeEvaluatorSunExposedMalus(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 64 // cells at y>=64 are sky-exposed; y<64 are shaded (the superflat stub)
	loop.gametime = 6000    // bright daytime so the canSeeSky stub still reads the y-projection

	// Synthetic path factory (fresh copy each call — truncateNodes mutates the slice in place, so
	// reusing one Path across the 3 phases would tangle the assertions).
	makePath := func() *Path {
		return &Path{nodes: []*node{
			{x: 8, y: 63, z: 8},  // shade
			{x: 9, y: 63, z: 8},  // shade
			{x: 10, y: 63, z: 8}, // shade
			{x: 11, y: 64, z: 8}, // SUN (first canSeeSky at spawnSurfaceY=64)
			{x: 12, y: 64, z: 8}, // SUN
			{x: 13, y: 64, z: 8}, // SUN
		}}
	}

	// Phase 1: WITHOUT avoidSun, the path is left whole (the seek-shade effect is OFF for non-
	// restricted mobs — every mob but a day-time restricted skeleton).
	e := sunTestMob(7060, 8.5, 63, 8.5) // the mob itself is at y=63 -> SHADED
	loop.only().entities.add(e)
	nav := &e.ai.navigation
	nav.avoidSun = false
	nav.path = makePath()
	nav.trimPathAvoidSun(loop, e)
	if len(nav.path.nodes) != 6 {
		t.Fatalf("avoidSun=false must NOT trim the path: got %d nodes, want 6", len(nav.path.nodes))
	}

	// Phase 2: WITH avoidSun + the mob currently SHADED -> truncate at the FIRST sky-exposed node.
	nav.avoidSun = true
	nav.path = makePath()
	nav.trimPathAvoidSun(loop, e)
	if len(nav.path.nodes) != 3 {
		t.Fatalf("avoidSun=true + shaded mob must truncate at the first sky-exposed node: got %d nodes, want 3", len(nav.path.nodes))
	}
	for i, nd := range nav.path.nodes {
		if nd.y >= loop.spawnSurfaceY {
			t.Fatalf("node[%d] at y=%d is sky-exposed — the trim should have removed it", i, nd.y)
		}
	}

	// Phase 3: WITH avoidSun + the mob CURRENTLY sky-exposed (no shade to retreat to — the mob is
	// already burning in the open), the path is LEFT WHOLE (VERIFIED CFR early-return branch).
	e2 := sunTestMob(7061, 8.5, 64, 8.5) // mob at y=64 -> SKY-EXPOSED itself
	loop.only().entities.add(e2)
	nav2 := &e2.ai.navigation
	nav2.avoidSun = true
	nav2.path = makePath()
	nav2.trimPathAvoidSun(loop, e2)
	if len(nav2.path.nodes) != 6 {
		t.Fatalf("avoidSun=true + sky-exposed mob must leave the path WHOLE (the 'already burning' short-circuit): got %d nodes, want 6", len(nav2.path.nodes))
	}
}
