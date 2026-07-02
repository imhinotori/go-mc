package server

// brain_villager.go — wires the ported Brain subsystem for the Villager, 1:1 from the unobfuscated 26.2
// jar (temp/cache/26.2-inner.jar, CFR + javap this session). It ports the LOAD-BEARING core of the
// villager brain: the job-site POI acquisition (AcquirePoi(JOB_SITE)) plus the profession assignment
// (AssignProfessionFromJobSite) — the chain that, when a villager CLAIMS a job-site POI, flips that POI to
// IS_OCCUPIED so the area isVillage() returns true and a REAL raid can be created/extended.
//
// THE JAR CHAIN (all CFR-cited):
//   - Villager is a BRAIN mob: NO registerGoals override; makeBrain(Brain$Packed) -> Villager.BRAIN_PROVIDER
//     -> registerBrainGoals adds ActivityData.create(CORE, VillagerGoalPackages.getCorePackage(profession)).
//   - VillagerGoalPackages.getCorePackage (VERIFIED CFR) is a big CORE package. The pieces that need
//     not-yet-built subsystems (doors, bells, raid status, trading, gossip/gift, GoToPotentialJobSite/
//     YieldJobSite/PoiCompetitorScan nav, the WORK/MEET/PANIC/REST activity packages, the HOME/MEETING
//     AcquirePoi legs) are CITE-DEFERRED. The pieces REAL and 1:1 here:
//       @0 Swim(0.8)                         -> newSwim(0.8)
//       @0 LookAtTargetSink(45, 90)          -> newLookAtTargetSink
//       @1 MoveToTargetSink()                -> newMoveToTargetSink
//       @6 AcquirePoi(acquirableJobSite, JOB_SITE, POTENTIAL_JOB_SITE, onlyIfAdult=true, empty, (l,p)->true)
//       @10 AssignProfessionFromJobSite()    -> newAssignProfessionFromJobSite
//   - Villager.customServerAiStep: getBrain().tick(level, this) (VERIFIED — the brain drives the villager).
//
// Cite: Villager (makeBrain/registerBrainGoals/customServerAiStep), VillagerGoalPackages.getCorePackage,
// AcquirePoi, AssignProfessionFromJobSite, VillagerProfession.ALL_ACQUIRABLE_JOBS.

import (
	"github.com/google/uuid"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// acquirePoiScanRange is AcquirePoi.SCAN_RANGE = 48 (the block radius the job-site scan searches).
//
//	[VERIFIED CFR AcquirePoi: public static final int SCAN_RANGE = 48; batchSize=5, rate=20, nextInt(20).]
const acquirePoiScanRange = 48

// attachVillagerBrain builds and attaches the ported Brain to a villager at spawn. Vanilla makes the brain
// in Mob.<init> via BRAIN_PROVIDER.makeBrain; here it is attached in the shared spawn path (villager-gated).
//
//	[VERIFIED CFR Villager.makeBrain: Villager.BRAIN_PROVIDER.makeBrain(this, packedBrain).]
func attachVillagerBrain(e *Entity) {
	if e.villagerType == "" {
		e.villagerType = villagerTypeDefault
	}
	if e.villagerProfession == "" {
		e.villagerProfession = villagerProfessionNone
	}
	e.villagerLevel = clampVillagerLevel(e.villagerLevel)
	e.brain = newVillagerBrainProvider().makeBrain(e)
}

// villagerBrainTick ports Villager.customServerAiStep: getBrain().tick(level, this). Called from tickAI for
// a live villager AFTER serverAiStep (the villager classic goalSelector is empty, so serverAiStep is a
// no-op and the brain is the sole AI driver). Villager-gated at the call site.
//
//	[VERIFIED CFR Villager.customServerAiStep: this.getBrain().tick(level, this); super.customServerAiStep.]
func (t *TickLoop) villagerBrainTick(e *Entity) {
	if e.brain == nil {
		return
	}
	e.brain.tick(t, e, t.GameTime())

	// Villager.customServerAiStep TRADE-event block (VERIFIED CFR this session):
	//   if (this.lastTradedPlayer != null) {
	//       level.onReputationEvent(ReputationEventType.TRADE, this.lastTradedPlayer, this);
	//       level.broadcastEntityEvent(this, (byte)14);   // "yes"/happy particles — v1 cite-deferred
	//       this.lastTradedPlayer = null;
	//   }
	// rewardTradeXp set lastTradedPlayerUUID to the trading player on the last trade; drain it here (one
	// TRADE reputation event per villager-tick that saw a trade). onReputationEvent is a pass-through to
	// onReputationEventFrom (ServerLevel.onReputationEvent -> target.onReputationEventFrom), so this raises
	// the trading player's TRADING gossip (+2), which lowers future prices via updateSpecialPrices. The
	// broadcastEntityEvent(14) happy-particle status is a cite-deferred client visual (no entity-event
	// wire for the villager here yet). CITE Villager.customServerAiStep + ServerLevel.onReputationEvent.
	if e.lastTradedPlayerUUID != (uuid.UUID{}) {
		villagerOnReputationEventFrom(e, reputationTrade, e.lastTradedPlayerUUID)
		e.lastTradedPlayerUUID = uuid.UUID{}
	}
}

// newVillagerBrainProvider builds the brainProvider for the villager: the CORE activity holding the real
// ported behaviors. The DEFERRED behaviors + the WORK/MEET/PANIC/REST activity packages are cited in the
// file header; the CORE activity here drives the job-site acquisition + profession assignment.
func newVillagerBrainProvider() *brainProvider {
	p := newBrainProvider()
	p.addActivity(villagerInitCoreActivity())
	return p
}

// villagerInitCoreActivity ports the real slice of ActivityData.create(CORE, VillagerGoalPackages
// .getCorePackage(...)). Priorities are the jar values (0 Swim/LookAtTargetSink, 1 MoveToTargetSink, 6
// AcquirePoi(JOB_SITE), 10 AssignProfessionFromJobSite).
//
//	[VERIFIED CFR VillagerGoalPackages.getCorePackage: Pair.of(0, new Swim(0.8f)); Pair.of(0, new
//	 LookAtTargetSink(45, 90)); Pair.of(1, new MoveToTargetSink()); Pair.of(6, AcquirePoi.create(
//	 acquirableJobSite, JOB_SITE, POTENTIAL_JOB_SITE, true, empty, (l,p)->true)); Pair.of(10,
//	 AssignProfessionFromJobSite.create()).]
func villagerInitCoreActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 0, behavior: newSwim(0.8)},
		{priority: 0, behavior: newLookAtTargetSink(45, 90)},
		{priority: 1, behavior: newMoveToTargetSink(150, 250)},
		{priority: 6, behavior: newVillagerAcquireJobSite()},
		{priority: 10, behavior: newAssignProfessionFromJobSite()},
	}
	return activityDataCreatePairs(activityCore, pairs, nil, nil)
}

// newVillagerAcquireJobSite ports AcquirePoi.create(profession.acquirableJobSite(), JOB_SITE (validate),
// POTENTIAL_JOB_SITE (acquire), onlyIfAdult=true, empty, (l,p)->true) at CORE priority 6. It is a OneShot
// whose entryCondition is absent(POTENTIAL_JOB_SITE). The trigger: skip if isBaby; rate-limit (first run
// schedules nextStart = gameTime + nextInt(20); thereafter run once gameTime >= nextStart, then reschedule
// nextStart = ts + 20 + nextInt(20)); scan the region PoiManager for the CLOSEST HAS_SPACE
// #acquirable_job_site within SCAN_RANGE(48); take() its ticket (freeTickets-- -> IS_OCCUPIED -> village
// center -> raid unblocked); set POTENTIAL_JOB_SITE to the claimed pos.
//
// The findPathToPois(...).canReach() reachability gate is CITE-REDUCED: there is no synchronous A*-to-
// target reachability query seam (groundNavigation.requestPath is async), so the CLOSEST-first POI (the
// order AcquirePoi findAllClosestFirstWithType uses) is claimed directly. Same observable outcome in a
// reachable layout; the JitteredLinearRetry batch cache (retry backoff for UNreachable POIs) is deferred
// with it. Cite AcquirePoi.create + AcquirePoi.SCAN_RANGE + PoiManager.take.
func newVillagerAcquireJobSite() *oneShot {
	var nextScheduledStart int64
	cond := []memoryCondition{{key: memPotentialJobSite, status: memValueAbsent}}
	return newOneShot(cond, func(t *TickLoop, e *Entity, timestamp int64) bool {
		if e.isBaby() { // onlyIfAdult && body.isBaby() -> false
			return false
		}
		region := t.cur()
		if region == nil {
			return false
		}
		gameTime := t.GameTime()
		if nextScheduledStart == 0 {
			nextScheduledStart = gameTime + int64(villagerRateJitter(region))
			return false
		}
		if gameTime < nextScheduledStart {
			return false
		}
		nextScheduledStart = timestamp + 20 + int64(villagerRateJitter(region))
		pm := region.poiManager
		if pm == nil {
			return false
		}
		center := pk.Position{X: floorInt(e.x), Y: floorInt(e.y), Z: floorInt(e.z)}
		pos, ok := pm.findClosest(poiTypeIsAcquirableJobSite, center, acquirePoiScanRange, poiOccupancyHasSpace)
		if !ok {
			return false
		}
		claimed, took := pm.take(poiTypeIsAcquirableJobSite, pos)
		if !took {
			return false
		}
		e.brain.setMemory(memPotentialJobSite, claimed)
		return true
	})
}

// villagerRateJitter is the AcquirePoi random.nextInt(20) rate jitter, drawn off the region levelRandom
// (level.getRandom()). Returns 0 when the region has no seeded levelRandom (a bare test loop) — a cited
// graceful degrade (no jitter), never a panic. VERIFIED CFR AcquirePoi: random.nextInt(20).
func villagerRateJitter(region *region) int {
	if region == nil || region.levelRandom == nil {
		return 0
	}
	return int(region.levelRandom.NextIntN(20))
}

// newAssignProfessionFromJobSite ports AssignProfessionFromJobSite.create(): a OneShot with entryCondition
// present(POTENTIAL_JOB_SITE) + registered(JOB_SITE). The trigger reads pos = POTENTIAL_JOB_SITE; guards on
// pos.closerToCenterThan(body.position(), 2.0) (or assignProfessionWhenSpawned, a spawn-egg fast-path not
// built -> const false); erases POTENTIAL_JOB_SITE; sets JOB_SITE = pos; broadcastEntityEvent((byte)14) (a
// client render event, server no-op here); if the profession is already non-NONE returns true (no
// reassign); else looks up the POI type at pos and assigns the matching profession (profession key path ==
// POI key path, VERIFIED VillagerProfession.register); refreshBrain is deferred (WORK package not built).
//
//	[VERIFIED CFR AssignProfessionFromJobSite.create: group(present(POTENTIAL_JOB_SITE), registered(
//	 JOB_SITE)); if (!pos.closerToCenterThan(body.position(),2.0) && !body.assignProfessionWhenSpawned())
//	 return false; potentialJobSite.erase(); jobSite.set(pos); broadcastEntityEvent(14); if (!profession
//	 .is(NONE)) return true; poiManager.getType(pos) -> matching VillagerProfession -> setVillagerData(
//	 withProfession) + refreshBrain.]
func newAssignProfessionFromJobSite() *oneShot {
	cond := []memoryCondition{
		{key: memPotentialJobSite, status: memValuePresent},
		{key: memJobSite, status: memRegistered},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		v, ok := e.brain.getMemory(memPotentialJobSite)
		if !ok {
			return false
		}
		pos, ok := v.(pk.Position)
		if !ok {
			return false
		}
		if !villagerCloserToCenterThan(pos, e.x, e.y, e.z, 2.0) {
			return false
		}
		e.brain.eraseMemory(memPotentialJobSite)
		e.brain.setMemory(memJobSite, pos)
		e.villagerJobSiteX, e.villagerJobSiteY, e.villagerJobSiteZ = pos.X, pos.Y, pos.Z
		e.villagerHasJobSite = true
		if e.villagerProfession != villagerProfessionNone {
			return true // already has a profession: JOB_SITE set, no reassign.
		}
		region := t.cur()
		if region == nil || region.poiManager == nil {
			return true
		}
		rec := region.poiManager.recordAt(pos)
		if rec == nil {
			return true
		}
		e.villagerProfession = professionForJobSitePoi(rec.poiType)
		return true
	})
}

// villagerCloserToCenterThan ports BlockPos.closerToCenterThan(Position, double): the block CENTER
// (x+0.5,y+0.5,z+0.5) distanceToSqr(pos) < dist*dist. VERIFIED Vec3i.closerToCenterThan.
func villagerCloserToCenterThan(b pk.Position, x, y, z, dist float64) bool {
	dx := (float64(b.X) + 0.5) - x
	dy := (float64(b.Y) + 0.5) - y
	dz := (float64(b.Z) + 0.5) - z
	return dx*dx+dy*dy+dz*dz < dist*dist
}
