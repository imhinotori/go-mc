package server

// schedule_test.go -- coverage for the villager day-cycle ACTIVITY SCHEDULE + the POI-driven activity
// relocation (schedule.go + brain_villager*.go). Pins: (1) the villager_schedule.json keyframe timeline
// maps each day-time to the exact vanilla Activity (the 10/2000/9000/11000/12000 boundaries, wrapping);
// (2) updateActivityFromSchedule activates the scheduled activity through the brain; (3) REST at night
// walks the villager to a claimed HOME bed. All numbers are jar-verified (villager_schedule.json).

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestVillagerScheduleKeyframes pins schedule.sample against the villager_activity timeline: the exact
// day-time -> Activity boundaries (VERIFIED data/minecraft/timeline/villager_schedule.json):
//
//	[0,10) -> REST (wrap from the 12000 keyframe), [10,2000) -> IDLE, [2000,9000) -> WORK,
//	[9000,11000) -> MEET, [11000,12000) -> IDLE, [12000,24000) -> REST.
func TestVillagerScheduleKeyframes(t *testing.T) {
	cases := []struct {
		dayTime int64
		want    activity
		name    string
	}{
		{0, activityRest, "midnight-wrap-before-first-keyframe"},
		{9, activityRest, "just-before-IDLE-keyframe"},
		{10, activityIdle, "IDLE-keyframe-exact"},
		{1999, activityIdle, "just-before-WORK"},
		{2000, activityWork, "WORK-keyframe-exact"},
		{5000, activityWork, "mid-work-day"},
		{8999, activityWork, "just-before-MEET"},
		{9000, activityMeet, "MEET-keyframe-exact"},
		{10999, activityMeet, "just-before-IDLE-evening"},
		{11000, activityIdle, "IDLE-evening-keyframe"},
		{11999, activityIdle, "just-before-REST"},
		{12000, activityRest, "REST-keyframe-exact"},
		{18000, activityRest, "deep-night"},
		{23999, activityRest, "end-of-day"},
		{24000, activityRest, "wrap-to-next-day-midnight"},
		{24010, activityIdle, "wrap-to-next-day-IDLE"},
	}
	for _, c := range cases {
		if got := villagerDefaultSchedule.sample(c.dayTime); got != c.want {
			t.Errorf("%s: sample(%d) = %d, want %d", c.name, c.dayTime, got, c.want)
		}
	}
}

// TestVillagerBabyScheduleKeyframes pins the baby timeline (PLAY cite-reduced to IDLE): [0,10)->REST,
// [10,12000)->IDLE (10/3000/6000/10000 all resolve to IDLE), [12000,24000)->REST. VERIFIED baby track.
func TestVillagerBabyScheduleKeyframes(t *testing.T) {
	cases := []struct {
		dayTime int64
		want    activity
	}{
		{0, activityRest}, {10, activityIdle}, {3000, activityIdle}, {6000, activityIdle},
		{10000, activityIdle}, {11999, activityIdle}, {12000, activityRest}, {20000, activityRest},
	}
	for _, c := range cases {
		if got := villagerBabySchedule.sample(c.dayTime); got != c.want {
			t.Errorf("baby sample(%d) = %d, want %d", c.dayTime, got, c.want)
		}
	}
}

// TestFloorModI64 pins the sampler loopTicks wrap (Math.floorMod semantics) over the 24000 period.
func TestFloorModI64(t *testing.T) {
	cases := []struct{ v, m, want int64 }{
		{0, 24000, 0}, {10, 24000, 10}, {24000, 24000, 0}, {24010, 24000, 10},
		{-1, 24000, 23999}, {-24000, 24000, 0}, {-24001, 24000, 23999}, {48000, 24000, 0},
	}
	for _, c := range cases {
		if got := floorModI64(c.v, c.m); got != c.want {
			t.Errorf("floorModI64(%d,%d) = %d, want %d", c.v, c.m, got, c.want)
		}
	}
}

// TestVillagerScheduleActivatesWork drives updateActivityFromSchedule through a real villager brain and
// pins that, once a JOB_SITE is claimed and the day-time is in the WORK window, the brain activates WORK;
// and that a day-time in the REST window (no bed) falls back to the default IDLE (setActiveActivityIfPossible
// -> useDefaultActivity when the requirements are not met). Cite Brain.updateActivityFromSchedule.
func TestVillagerScheduleActivatesWork(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	e := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 64, 8.5)
	if e == nil || e.brain == nil {
		t.Fatal("villager spawn/brain nil")
	}

	// Give the villager a claimed JOB_SITE so WORK requirements are met.
	e.brain.setMemory(memJobSite, pk.Position{X: 8, Y: 64, Z: 8})

	// WORK window (2000..9000): sampling must select WORK, and since JOB_SITE is present it activates.
	e.brain.lastScheduleUpdate = -100 // clear the 20-tick guard
	e.brain.updateActivityFromSchedule(3000)
	if !e.brain.isActive(activityWork) {
		t.Fatalf("WORK window with JOB_SITE present: brain must be active in WORK, active=%v", e.brain.activeActivities)
	}

	// REST window (12000..24000) with NO home: REST has no memory requirement so it activates directly.
	e.brain.lastScheduleUpdate = -100
	e.brain.updateActivityFromSchedule(13000)
	if !e.brain.isActive(activityRest) {
		t.Fatalf("REST window: brain must be active in REST, active=%v", e.brain.activeActivities)
	}

	// MEET window (9000..11000) with NO meeting point: requirements unmet -> useDefaultActivity (IDLE).
	e.brain.lastScheduleUpdate = -100
	e.brain.updateActivityFromSchedule(10000)
	if !e.brain.isActive(activityIdle) || e.brain.isActive(activityMeet) {
		t.Fatalf("MEET window without MEETING_POINT must fall back to IDLE, active=%v", e.brain.activeActivities)
	}
}

// TestVillagerRestWalksToBed is the REST payoff: a villager with a claimed HOME bed, when REST activates
// at night, walks to the bed (the SetWalkTargetFromBlockMemory(HOME) behavior sets WALK_TARGET at the bed).
// Cite VillagerGoalPackages.getRestPackage + SetWalkTargetFromBlockMemory.
func TestVillagerRestWalksToBed(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	// Villager standing away from the bed so it must walk to it.
	e := loop.spawnVanillaMob(vanillaVillagerMobName, 20.5, 64, 20.5)
	if e == nil || e.brain == nil {
		t.Fatal("villager spawn/brain nil")
	}
	bedPos := pk.Position{X: 8, Y: 64, Z: 8}

	loop.withRegion(loop.only(), func() {
		// Place a bed so the POI manager holds a HOME record; claim it as this villager's HOME memory.
		bed := block.DefaultStateID["minecraft:red_bed"]
		air := block.ToStateID[block.Air{}]
		loop.updatePoiOnBlockStateChange(bedPos, air, bed)
		pm := loop.only().poiManager
		if pm == nil {
			t.Fatal("placing the bed must create the POI manager")
		}
		if rec := pm.recordAt(bedPos); rec == nil || rec.poiType != poiTypeHome {
			t.Fatalf("bed POI: rec=%v (want a HOME POI)", rec)
		}
		e.brain.setMemory(memHome, bedPos)

		// Drive REST at a night day-time; tick the brain so the REST walk-to-bed behavior runs.
		e.brain.lastScheduleUpdate = -100
		e.brain.updateActivityFromSchedule(13000)
		if !e.brain.isActive(activityRest) {
			t.Fatalf("REST must be active at night, active=%v", e.brain.activeActivities)
		}
		for i := 0; i < 5; i++ {
			loop.gametime = int64(13000 + i)
			loop.villagerBrainTick(e)
			if wt, ok := e.brain.getMemoryWalkTarget(memWalkTarget); ok {
				// The walk target must point at the bed block center.
				tx, ty, tz := wt.target.currentPosition(loop)
				if int(tx) != bedPos.X || int(tz) != bedPos.Z {
					t.Fatalf("REST walk target = (%.1f,%.1f,%.1f), want the bed %v", tx, ty, tz, bedPos)
				}
				return // success: REST set a WALK_TARGET at the bed
			}
		}
		t.Fatal("REST never set a WALK_TARGET toward the HOME bed (SetWalkTargetFromBlockMemory did not fire)")
	})
}
