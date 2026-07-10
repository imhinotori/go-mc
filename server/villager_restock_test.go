package server

// villager_restock_test.go -- coverage for GAP 2 (VILLAGERS NEVER RESTOCK): the WORK-activity WorkAtPoi
// behavior (brain_villager.go newVillagerWorkAtPoi) now WIRES the previously-caller-less villagerShouldRestock
// + villagerRestock (villager_reputation.go). A villager with a depleted trade (uses > 0) standing at its
// claimed job-site POI restocks it (uses -> 0, numberOfRestocksToday ++). All numbers are jar-verified against
// net.minecraft.world.entity.ai.behavior.WorkAtPoi + Villager.shouldRestock/restock. The pig oracle
// (TestPluginPigEqualsGoNativePig) is UNTOUCHED -- WorkAtPoi is villager-gated and its coin flip draws off the
// region levelRandom, never the mob stream.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestVillagerWorkAtPoiRestocksDepletedTrade drives WorkAtPoi.start on a FARMER villager standing on its
// composter job-site with a depleted offer (uses > 0). Expect: the offer's uses reset to 0 and
// numberOfRestocksToday increments (Villager.restock via WorkAtPoi.start's shouldRestock() gate).
func TestVillagerWorkAtPoiRestocksDepletedTrade(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	vx, vy, vz := 8.5, 64.0, 8.5
	e := loop.spawnVanillaMob(vanillaVillagerMobName, vx, vy, vz)
	if e == nil {
		t.Fatal("spawnVanillaMob(vanilla_villager) returned nil")
	}
	if e.typ != entity.Villager.ID {
		t.Fatalf("villager typ = %d, want %d", e.typ, entity.Villager.ID)
	}

	// A FARMER with a claimed JOB_SITE at the villager's own cell -> its offers resolve to farmer/1.
	e.villagerProfession = "farmer"
	e.villagerLevel = 1
	jobSite := pk.Position{X: 8, Y: 64, Z: 8}
	e.brain.setMemory(memJobSite, jobSite)

	// Deplete the first offer (a trade was used) so needsToRestock() is true.
	offers := villagerGetOffers(e)
	if offers.isEmpty() {
		t.Fatal("a FARMER level-1 villager must have non-empty offers")
	}
	offers[0].uses = 5
	if !villagerNeedsToRestock(e) {
		t.Fatal("a villager with a used offer must needToRestock (precondition)")
	}

	// Build the WorkAtPoi behavior and start it (checkExtraStartConditions is throttled/coin-flipped; drive
	// start directly to exercise the restock wiring deterministically -- the throttle is covered separately).
	wap := newVillagerWorkAtPoi()

	// Sanity: the entry condition (JOB_SITE present, LOOK_TARGET registered) must be satisfiable.
	if !wap.hasRequiredMemories(e) {
		t.Fatal("WorkAtPoi entry memories (JOB_SITE present, LOOK_TARGET registered) not satisfied")
	}

	loop.gametime = 20000 // well past the 12000-tick shouldRestock window from lastRestockGameTime 0
	wap.start(loop, e, loop.gametime)

	if offers[0].uses != 0 {
		t.Fatalf("after WorkAtPoi.start, offer uses = %d, want 0 (restocked)", offers[0].uses)
	}
	if e.numberOfRestocksToday != 1 {
		t.Fatalf("numberOfRestocksToday = %d, want 1 (one restock)", e.numberOfRestocksToday)
	}
	if e.lastRestockGameTime != loop.gametime {
		t.Fatalf("lastRestockGameTime = %d, want %d (stamped on restock)", e.lastRestockGameTime, loop.gametime)
	}
}

// TestVillagerWorkAtPoiCheckConditions pins the WorkAtPoi checkExtraStartConditions gates: the 300-tick
// lastCheck cooldown, the random.nextInt(2) coin flip (1-in-2, off the region levelRandom), and the
// DISTANCE(1.73) proximity to the JOB_SITE. A villager standing ON its job-site can pass (allowing for the
// coin flip); a villager far away always fails; a re-check within 300 ticks always fails.
func TestVillagerWorkAtPoiCheckConditions(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	e := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 64.0, 8.5)
	if e == nil {
		t.Fatal("spawnVanillaMob(vanilla_villager) returned nil")
	}
	e.villagerProfession = "farmer"
	e.brain.setMemory(memJobSite, pk.Position{X: 8, Y: 64, Z: 8})

	wap := newVillagerWorkAtPoi()

	// Standing on the job-site cell center -> within 1.73. The 300-tick cooldown is satisfied when a check
	// passes; the coin flip is 1-in-2, so advance the gametime (past the cooldown each time) until a check
	// passes -- it must pass within a handful of tries (proving the on-site check CAN pass).
	passed := false
	passTime := int64(0)
	for i := 0; i < 20; i++ {
		loop.gametime = 1000 + int64(i)*(workAtPoiCheckCooldown+1)
		if wap.checkExtraStart(loop, e) {
			passed = true
			passTime = loop.gametime
			break
		}
	}
	if !passed {
		t.Fatal("villager on its job-site must be able to pass WorkAtPoi.checkExtraStartConditions within a few coin flips")
	}

	// Immediately re-check with the SAME gametime -> the 300-tick lastCheck cooldown blocks it (regardless
	// of the coin), since gameTime - lastCheck == 0 < 300.
	if wap.checkExtraStart(loop, e) {
		t.Fatal("a second check within 300 ticks must fail (lastCheck cooldown)")
	}

	// Advance past the cooldown but move the villager far from the job site -> the DISTANCE(1.73) gate fails
	// on every coin outcome.
	e.x, e.z = 8.5+10, 8.5+10 // ~14 blocks away, well beyond 1.73
	for i := 0; i < 20; i++ {
		loop.gametime = passTime + workAtPoiCheckCooldown + 1 + int64(i)*(workAtPoiCheckCooldown+1)
		if wap.checkExtraStart(loop, e) {
			t.Fatal("a villager far from its job-site must fail the DISTANCE(1.73) gate")
		}
	}
}

// TestVillagerRestockTwicePerDayCap pins the Villager.allowedToRestock 2x/day cap via repeated WorkAtPoi
// restocks: a third restock in the same day (numberOfRestocksToday == 2) is refused.
func TestVillagerRestockTwicePerDayCap(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	e := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 64.0, 8.5)
	e.villagerProfession = "farmer"
	e.brain.setMemory(memJobSite, pk.Position{X: 8, Y: 64, Z: 8})
	offers := villagerGetOffers(e)

	// First restock at t=1000.
	offers[0].uses = 3
	loop.gametime = 1000
	if !villagerShouldRestock(e, loop.gametime) {
		t.Fatal("first restock of the day must be allowed")
	}
	villagerRestock(e, loop.gametime)
	if e.numberOfRestocksToday != 1 {
		t.Fatalf("after 1st restock numberOfRestocksToday = %d, want 1", e.numberOfRestocksToday)
	}

	// Second restock >2400 ticks later, still same day (< 12000 window from lastRestockGameTime).
	offers[0].uses = 3
	loop.gametime = 1000 + 2500
	if !villagerShouldRestock(e, loop.gametime) {
		t.Fatal("second restock (>2400 ticks after first, same day) must be allowed")
	}
	villagerRestock(e, loop.gametime)
	if e.numberOfRestocksToday != 2 {
		t.Fatalf("after 2nd restock numberOfRestocksToday = %d, want 2", e.numberOfRestocksToday)
	}

	// Third restock in the same day -> refused (allowedToRestock false at count 2).
	offers[0].uses = 3
	loop.gametime = 1000 + 5000 // still < lastRestockGameTime + 12000 and same day index
	if villagerShouldRestock(e, loop.gametime) {
		t.Fatal("a third restock in the same day must be refused (2x/day cap)")
	}
}
