package server

// villager_test.go — the Villager port coverage (server/villager_data.go, villager_trades.go,
// brain_villager.go + the POI job-site wiring). It pins: (1) the VillagerData clamps + profession<-POI
// mapping; (2) the FARMER level-1 trade numbers; (3) boot-load as entity.Villager (brain mob, 0 goals);
// (4) THE PAYOFF — a villager claims a job-site POI via the ported CORE brain (AcquirePoi(JOB_SITE) ->
// take() -> IS_OCCUPIED), AssignProfessionFromJobSite assigns the matching profession, and the area's
// isVillage() then returns true (so a real raid can fire). All numbers are jar-verified (VillagerData
// static init; base villager_trade/farmer/1 datapack; PoiTypes.bootstrap; AcquirePoi.SCAN_RANGE).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestVillagerDataClampAndProfession pins clampVillagerLevel (MIN 1, MAX 5) + professionForJobSitePoi
// (a claimed FARMER job-site POI -> "farmer"; a non-job-site POI / nil -> "none") + the XP thresholds.
func TestVillagerDataClampAndProfession(t *testing.T) {
	if clampVillagerLevel(0) != 1 || clampVillagerLevel(-5) != 1 {
		t.Fatalf("clampVillagerLevel below MIN must clamp to 1")
	}
	if clampVillagerLevel(1) != 1 || clampVillagerLevel(5) != 5 {
		t.Fatalf("clampVillagerLevel within range must be identity")
	}
	if clampVillagerLevel(6) != 5 || clampVillagerLevel(99) != 5 {
		t.Fatalf("clampVillagerLevel above MAX must clamp to 5")
	}
	if got := professionForJobSitePoi(poiTypeFarmer); got != "farmer" {
		t.Fatalf("professionForJobSitePoi(FARMER poi) = %q, want farmer", got)
	}
	if got := professionForJobSitePoi(poiTypeLibrarian); got != "librarian" {
		t.Fatalf("professionForJobSitePoi(LIBRARIAN poi) = %q, want librarian", got)
	}
	if got := professionForJobSitePoi(poiTypeHome); got != villagerProfessionNone {
		t.Fatalf("professionForJobSitePoi(HOME, a non-job-site) = %q, want none", got)
	}
	if got := professionForJobSitePoi(nil); got != villagerProfessionNone {
		t.Fatalf("professionForJobSitePoi(nil) = %q, want none", got)
	}
	if nextLevelXpThresholds != [...]int{0, 10, 70, 150, 250} {
		t.Fatalf("nextLevelXpThresholds = %v, want {0,10,70,150,250}", nextLevelXpThresholds)
	}
}

// TestFarmerLevel1Offers pins the FARMER level-1 trade numbers against the base datapack (villager_trade/
// farmer/1/*.json): wheat 20 / potato 26 / carrot 22 / beetroot 15 -> 1 emerald (xp 2), and 1 emerald ->
// bread x6 (xp 0); max_uses 16, priceMultiplier 0.05 on every offer.
func TestFarmerLevel1Offers(t *testing.T) {
	offers := farmerLevel1Offers()
	if len(offers) != 5 {
		t.Fatalf("FARMER level 1 has %d offers, want 5", len(offers))
	}
	type want struct {
		cost   item.Item
		count  int
		result item.Item
		rcount int
		xp     int
	}
	wants := []want{
		{item.Wheat, 20, item.Emerald, 1, 2},
		{item.Potato, 26, item.Emerald, 1, 2},
		{item.Carrot, 22, item.Emerald, 1, 2},
		{item.Beetroot, 15, item.Emerald, 1, 2},
		{item.Emerald, 1, item.Bread, 6, 0},
	}
	for i, w := range wants {
		o := offers[i]
		if o.baseCostA.item != w.cost || o.baseCostA.count != w.count {
			t.Fatalf("offer %d cost = %v x%d, want %v x%d", i, o.baseCostA.item, o.baseCostA.count, w.cost, w.count)
		}
		if o.result.item != w.result || o.result.count != w.rcount {
			t.Fatalf("offer %d result = %v x%d, want %v x%d", i, o.result.item, o.result.count, w.result, w.rcount)
		}
		if o.maxUses != 16 {
			t.Fatalf("offer %d maxUses = %d, want 16", i, o.maxUses)
		}
		if o.xp != w.xp {
			t.Fatalf("offer %d xp = %d, want %d", i, o.xp, w.xp)
		}
		if o.priceMultiplier != 0.05 {
			t.Fatalf("offer %d priceMultiplier = %v, want 0.05", i, o.priceMultiplier)
		}
	}
	// villagerOffersFor routes FARMER/1 to the sample; every other (profession, level) is empty (deferred).
	if villagerOffersFor("farmer", 1).isEmpty() {
		t.Fatal("villagerOffersFor(farmer,1) must be non-empty")
	}
	if !villagerOffersFor("armorer", 1).isEmpty() {
		t.Fatal("villagerOffersFor(armorer,1) must be empty (deferred)")
	}
}

// TestVillagerBootLoadsAsBrainMob pins that the boot-loaded vanilla_villager declaration builds an
// entity.Villager with a NON-NIL brain, ZERO classic goals (a brain mob), and the VillagerData defaults
// (profession "none", level 1). Cite Villager.registerBrainGoals (empty goalSelector) + makeBrain.
func TestVillagerBootLoadsAsBrainMob(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	e := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 64, 8.5)
	if e == nil {
		t.Fatal("spawnVanillaMob(vanilla_villager) returned nil")
	}
	if e.typ != entity.Villager.ID {
		t.Fatalf("villager typ = %d, want %d (entity.Villager)", e.typ, entity.Villager.ID)
	}
	if e.brain == nil {
		t.Fatal("a spawned villager must have its brain attached (attachVillagerBrain)")
	}
	if e.ai == nil || len(e.ai.goals.goals) != 0 || len(e.ai.targetSelector.goals) != 0 {
		t.Fatalf("villager is a brain mob: want 0 classic goals, got goals=%d targets=%d",
			len(e.ai.goals.goals), len(e.ai.targetSelector.goals))
	}
	if e.villagerProfession != villagerProfessionNone {
		t.Fatalf("fresh villager profession = %q, want none", e.villagerProfession)
	}
	if e.villagerLevel != 1 {
		t.Fatalf("fresh villager level = %d, want 1", e.villagerLevel)
	}
	if e.villagerType != villagerTypeDefault {
		t.Fatalf("fresh villager type = %q, want plains", e.villagerType)
	}
}

// TestVillagerClaimsJobSiteMakesVillage is THE PAYOFF: a villager next to a COMPOSTER (the FARMER job-site
// block) runs the ported CORE brain — AcquirePoi(JOB_SITE) claims the POI (take() -> freeTickets 1->0 ->
// IS_OCCUPIED), AssignProfessionFromJobSite promotes POTENTIAL_JOB_SITE -> JOB_SITE and assigns the FARMER
// profession, and the composter's section is now an IS_OCCUPIED #village center so poiManager.isVillage()
// returns true (the exact predicate createOrExtendRaid keys the raid trigger on). VERIFIED chain
// VillagerGoalPackages.getCorePackage(AcquirePoi + AssignProfessionFromJobSite) + PoiManager.take +
// PoiRecord.isOccupied + ServerLevel.isVillage.
func TestVillagerClaimsJobSiteMakesVillage(t *testing.T) {
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())

	// Spawn the villager standing ON the composter cell center (well within AssignProfession's 2.0 range).
	vx, vy, vz := 8.5, 64.0, 8.5
	e := loop.spawnVanillaMob(vanillaVillagerMobName, vx, vy, vz)
	if e == nil {
		t.Fatal("spawnVanillaMob(vanilla_villager) returned nil")
	}
	composterPos := pk.Position{X: 8, Y: 64, Z: 8} // the block the villager stands in

	loop.withRegion(loop.only(), func() {
		composter := block.DefaultStateID["minecraft:composter"]
		air := block.ToStateID[block.Air{}]
		loop.updatePoiOnBlockStateChange(composterPos, air, composter)
		pm := loop.only().poiManager
		if pm == nil {
			t.Fatal("placing the composter must create the POI manager")
		}
		if rec := pm.recordAt(composterPos); rec == nil || rec.poiType != poiTypeFarmer || rec.isOccupied() {
			t.Fatalf("fresh composter POI: rec=%v (want a non-occupied FARMER POI)", rec)
		}

		// Precondition: an UNCLAIMED job-site POI is NOT yet a village.
		if pm.isVillage(composterPos) {
			t.Fatal("an unclaimed job-site POI must NOT form a village (IS_OCCUPIED gate)")
		}

		// Drive the brain. AcquirePoi first schedules (nextScheduledStart from 0), then claims on a later
		// gameTime; AssignProfessionFromJobSite fires the next tick (POTENTIAL_JOB_SITE now present). Advance
		// gametime generously and tick enough to pass the rate schedule + both OneShots.
		for i := 0; i < 50; i++ {
			loop.gametime = int64(i * 5) // stride past the AcquirePoi 20-tick rate + nextInt(20) jitter
			loop.villagerBrainTick(e)
			if e.villagerHasJobSite && e.villagerProfession == "farmer" {
				break
			}
		}

		if !e.villagerHasJobSite {
			t.Fatal("villager never claimed a JOB_SITE (AcquirePoi did not take the composter POI)")
		}
		if e.villagerProfession != "farmer" {
			t.Fatalf("villager profession = %q, want farmer (AssignProfessionFromJobSite)", e.villagerProfession)
		}
		if e.villagerJobSiteX != composterPos.X || e.villagerJobSiteY != composterPos.Y || e.villagerJobSiteZ != composterPos.Z {
			t.Fatalf("villager JOB_SITE = (%d,%d,%d), want the composter pos %v",
				e.villagerJobSiteX, e.villagerJobSiteY, e.villagerJobSiteZ, composterPos)
		}

		// THE PAYOFF: the claimed POI is now IS_OCCUPIED -> its section is a #village center -> isVillage true.
		rec := pm.recordAt(composterPos)
		if rec == nil || !rec.isOccupied() {
			t.Fatalf("claimed composter POI must be IS_OCCUPIED, got rec=%v", rec)
		}
		pm.villageDist = map[int64]int{} // a real claim invalidates the distance cache
		if !pm.isVillage(composterPos) {
			t.Fatal("after the villager claim, the job-site section must be a village (raid trigger unblocked)")
		}

		// villagerGetOffers now resolves the FARMER level-1 trades for this villager (lazy-built + cached).
		if villagerGetOffers(e).isEmpty() {
			t.Fatal("a FARMER level-1 villager must have non-empty offers (villagerGetOffers)")
		}
	})
}
