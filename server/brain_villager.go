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
	// registerBrainGoals: setSchedule(isBaby ? BABY_VILLAGER_ACTIVITY : VILLAGER_ACTIVITY). The spawn-time
	// updateActivityFromSchedule runs on the first CORE-active UpdateActivityFromSchedule behavior tick (the
	// 20-tick guard starts satisfied since lastScheduleUpdate=0). VERIFIED javap Villager.registerBrainGoals.
	if e.isBaby() {
		e.brain.setSchedule(villagerBabySchedule)
	} else {
		e.brain.setSchedule(villagerDefaultSchedule)
	}
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

	// Villager.customServerAiStep level-up-timer block (VERIFIED CFR this session), between brain.tick and
	// the TRADE-event block:
	//   if (!isTrading() && this.updateMerchantTimer > 0) {
	//       if (--this.updateMerchantTimer <= 0) {
	//           if (this.increaseProfessionLevelOnUpdate) {
	//               this.increaseMerchantCareer(level);
	//               this.increaseProfessionLevelOnUpdate = false;
	//           }
	//           this.addEffect(new MobEffectInstance(MobEffects.REGENERATION, 200, 0));
	//       }
	//   }
	// rewardTradeXp armed updateMerchantTimer=40 + increaseProfessionLevelOnUpdate=true when a trade pushed
	// villagerXp past the level threshold; the villager finishes trading (isTrading()==false once the menu
	// closes), the timer counts down, and on expiry the profession level bumps (increaseMerchantCareer adds
	// the next tier's trades) and a REGENERATION 200t/amp-0 buff plays. CITE Villager.customServerAiStep.
	if !villagerIsTrading(e) && e.updateMerchantTimer > 0 {
		e.updateMerchantTimer--
		if e.updateMerchantTimer <= 0 {
			if e.increaseProfessionLevelOnUpdate {
				villagerIncreaseMerchantCareer(e)
				e.increaseProfessionLevelOnUpdate = false
			}
			t.addEntityEffect(e, effectRegeneration, 200, 0) // MobEffectInstance(REGENERATION, 200, 0)
		}
	}

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
	// Sensors: the two the schedule PANIC leg needs (hurt_by -> HURT_BY/HURT_BY_ENTITY, villager_hostiles
	// -> NEAREST_HOSTILE). The larger villager sensor set (nearest_bed/players/babies/golem) is deferred;
	// their memories auto-register via the behaviors. Cite Villager.BRAIN_PROVIDER sensors.
	p.addSensor(sensorHurtBy, sensorVillagerHurtByTick, memHurtBy, memHurtByEntity)
	p.addSensor(sensorVillagerHostiles, sensorVillagerHostilesTick, memNearestHostile)
	// Activities (VERIFIED Villager BRAIN_PROVIDER ActivitySupplier): CORE, WORK{JOB_SITE present},
	// MEET{MEETING_POINT present}, REST, IDLE, PANIC (PLAY/PRE_RAID/RAID/HIDE deferred).
	p.addActivity(villagerInitCoreActivity())
	p.addActivity(villagerInitWorkActivity())
	p.addActivity(villagerInitMeetActivity())
	p.addActivity(villagerInitRestActivity())
	p.addActivity(villagerInitIdleActivity())
	p.addActivity(villagerInitPanicActivity())
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
		{priority: 0, behavior: newVillagerPanicTrigger()},
		{priority: 1, behavior: newMoveToTargetSink(150, 250)},
		{priority: 6, behavior: newVillagerAcquireJobSite()},
		{priority: 10, behavior: newAssignProfessionFromJobSite()},
		{priority: 10, behavior: newVillagerAcquirePoi(poiTypeIsHome, memHome, false)},
		{priority: 10, behavior: newVillagerAcquirePoi(poiTypeIsMeeting, memMeetingPoint, true)},
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

// villagerInitWorkActivity ports ActivityData.create(WORK, getWorkPackage(profession), {Pair(JOB_SITE,
// VALUE_PRESENT)}): WORK requires a claimed JOB_SITE and, at its core, walks the villager TO the job site.
// The workstation actions (WorkAtPoi, StrollToPoi/StrollToPoiList, SetLookAndInteract) are cite-deferred;
// the landed behaviors are the walk-to-job-site + UpdateActivityFromSchedule(99).
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(WORK, getWorkPackage(profession),
//	 ImmutableSet.of(Pair.of(JOB_SITE, VALUE_PRESENT))). getWorkPackage: SetWalkTargetFromBlockMemory
//	 .create(JOB_SITE, 0.5f, 9, ...) at prio 5; UpdateActivityFromSchedule.create() at prio 99.]
func villagerInitWorkActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 5, behavior: newSetWalkTargetFromBlockMemory(memJobSite, 0.5, 9)},
		{priority: 99, behavior: newUpdateActivityFromSchedule()},
	}
	conditions := []memoryCondition{{key: memJobSite, status: memValuePresent}}
	return activityDataCreatePairs(activityWork, pairs, conditions, nil)
}

// villagerInitMeetActivity ports ActivityData.create(MEET, getMeetPackage(), {Pair(MEETING_POINT,
// VALUE_PRESENT)}): MEET requires a claimed MEETING_POINT and walks the villager to the village bell.
// SocializeAtBell/InteractWith/SetLookAndInteract are cite-deferred; the landed behaviors are the
// walk-to-bell + UpdateActivityFromSchedule(99).
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(MEET, getMeetPackage(), ImmutableSet.of(
//	 Pair.of(MEETING_POINT, VALUE_PRESENT))). getMeetPackage: SetWalkTargetFromBlockMemory.create(
//	 MEETING_POINT, 0.4f, 40, ...) at prio 6; UpdateActivityFromSchedule.create() at prio 99.]
func villagerInitMeetActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 6, behavior: newSetWalkTargetFromBlockMemory(memMeetingPoint, 0.4, 40)},
		{priority: 99, behavior: newUpdateActivityFromSchedule()},
	}
	conditions := []memoryCondition{{key: memMeetingPoint, status: memValuePresent}}
	return activityDataCreatePairs(activityMeet, pairs, conditions, nil)
}

// villagerInitRestActivity ports ActivityData.create(REST, getRestPackage()): the REST (night) activity
// walks the villager to its HOME bed. SleepInBed pose + ValidateNearbyPoi/SetClosestHomeAsWalkTarget/
// InsideBrownianWalk/GoToClosestVillage fallbacks are cite-deferred (mob sleep-pose seam not built); the
// landed behaviors are the walk-to-bed + UpdateActivityFromSchedule(99). REST has no memory requirement.
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(REST, getRestPackage()). getRestPackage:
//	 SetWalkTargetFromBlockMemory.create(HOME, 0.6f, 1, 150, 1200) at prio 2; ValidateNearbyPoi(HOME);
//	 SleepInBed; RunOne{...} (deferred); UpdateActivityFromSchedule.create() at prio 99.]
func villagerInitRestActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 2, behavior: newSetWalkTargetFromBlockMemory(memHome, 0.6, 1)},
		{priority: 99, behavior: newUpdateActivityFromSchedule()},
	}
	return activityDataCreatePairs(activityRest, pairs, nil, nil)
}

// villagerInitIdleActivity ports ActivityData.create(IDLE, getIdlePackage()): the IDLE (default) activity.
// Its RunOne{InteractWith, VillageBoundRandomStroll, SetWalkTargetFromLookTarget, JumpOnBed, DoNothing},
// GiveGiftToHero, trade chains and VillagerMakeLove are cite-deferred (interaction/trade-brain subsystems).
// The landed slice is UpdateActivityFromSchedule(99) so IDLE keeps re-sampling the schedule (leaving IDLE
// for WORK/MEET/REST when the day-time turns). IDLE is Brain.defaultActivity, so it is always available.
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(IDLE, getIdlePackage()). getIdlePackage:
//	 RunOne{...}/GiveGiftToHero/trade gates/VillagerMakeLove (deferred); UpdateActivityFromSchedule at 99.]
func villagerInitIdleActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 99, behavior: newUpdateActivityFromSchedule()},
	}
	return activityDataCreatePairs(activityIdle, pairs, nil, nil)
}

// villagerInitPanicActivity ports ActivityData.create(PANIC, getPanicPackage()): PANIC runs while the
// villager is hurt / near a hostile. VillagerCalmDown(0) reverts it to the scheduled activity once the
// threat clears; SetWalkTargetAwayFrom.entity(HURT_BY_ENTITY / NEAREST_HOSTILE, 1.0, 6) flees the threat.
// RingBell/spawn-golem/VillageBoundRandomStroll/SetWalkTargetFromBlockMemory(bed)/ResetRaidStatus deferred.
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(PANIC, getPanicPackage()). getPanicPackage:
//	 Pair.of(0, VillagerCalmDown.create()); Pair.of(1, SetWalkTargetAwayFrom.entity(HURT_BY_ENTITY, 1.0f, 6,
//	 false)); Pair.of(1, SetWalkTargetAwayFrom.entity(NEAREST_HOSTILE, 1.0f, 6, false)); higher-prio
//	 VillageBoundRandomStroll/RingBell/ResetRaidStatus (deferred).]
func villagerInitPanicActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 0, behavior: newVillagerCalmDown()},
		{priority: 1, behavior: newSetWalkTargetAwayFromEntity(memHurtByEntity, 1.0, 6, false)},
		{priority: 1, behavior: newSetWalkTargetAwayFromEntity(memNearestHostile, 1.0, 6, false)},
	}
	return activityDataCreatePairs(activityPanic, pairs, nil, nil)
}

// newVillagerAcquirePoi ports AcquirePoi.create(typePredicate, acquireMemory, onlyIfAdult, empty[, biPred])
// for the single-target legs (HOME beds, MEETING_POINT bell) -- generalising newVillagerAcquireJobSite.
// entryCondition absent(acquireMemory); the trigger scans the region PoiManager for the CLOSEST HAS_SPACE
// POI matching typePredicate within SCAN_RANGE(48), take()s its ticket (freeTickets-- -> IS_OCCUPIED), and
// sets acquireMemory to the claimed pos. The reachability (findPathToPois.canReach) + JitteredLinearRetry
// backoff + the rate-jitter batch are cite-reduced exactly as newVillagerAcquireJobSite documents.
//
//	[VERIFIED javap AcquirePoi.create: group absent(acquireMemory) [+ registered validate memory]; skip if
//	 onlyIfAdult && body.isBaby(); nextInt(20) rate; getInRange(typePredicate, pos, 48, HAS_SPACE) closest
//	 first; poiManager.take -> acquireTicket; acquireMemory.set(GlobalPos.of(dimension, poiPos)). Cite
//	 AcquirePoi.create + AcquirePoi.SCAN_RANGE + PoiManager.take.]
func newVillagerAcquirePoi(typePred func(*poiType) bool, acquireMem memoryKey, onlyIfAdult bool) *oneShot {
	var nextScheduledStart int64
	cond := []memoryCondition{{key: acquireMem, status: memValueAbsent}}
	return newOneShot(cond, func(t *TickLoop, e *Entity, timestamp int64) bool {
		if onlyIfAdult && e.isBaby() {
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
		pos, ok := pm.findClosest(typePred, center, acquirePoiScanRange, poiOccupancyHasSpace)
		if !ok {
			return false
		}
		claimed, took := pm.take(typePred, pos)
		if !took {
			return false
		}
		e.brain.setMemory(acquireMem, claimed)
		return true
	})
}
