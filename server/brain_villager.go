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
	"bytes"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/entity"
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

	// Villager.tick() -> maybeDecayGossip() (VERIFIED CFR): the villager's per-tick entity tick decays the
	// gossip container once the 24000-tick (one-day) window elapses, so a player's reputation (and the
	// trade-price discount it drives) fades toward 0 over time instead of being permanent. Vanilla fires
	// this from Villager.tick() (the whole-entity tick, which also decrements unhappyCounter — that counter
	// is not tracked here yet), NOT customServerAiStep; villagerBrainTick is the villager-gated per-tick
	// entry that runs once per tick for a ticking villager, so calling maybeDecayGossip here reproduces the
	// exact once-per-tick cadence and the 24000-tick decay window. ADDITIVE + villager-gated at the call
	// site (tick_phases.go: typ == entity.Villager.ID), zero RNG (no mob-stream draw), so the pig oracle
	// stream is untouched. CITE Villager.tick + Villager.maybeDecayGossip + GossipContainer.decayGossip.
	villagerMaybeDecayGossip(e, t.GameTime())
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
// VALUE_PRESENT)}): WORK requires a claimed JOB_SITE and, at its core, walks the villager TO the job site
// (prio 2 SetWalkTargetFromBlockMemory) and, at the job site, RESTOCKS its trades (prio 5 WorkAtPoi -> the
// RESTOCK payoff, formerly a dead code path). The RunOne strolls (StrollAroundPoi/StrollToPoi/StrollToPoiList)
// and FARMER HarvestFarmland/UseBonemeal siblings, SetLookAndInteract, and GiveGiftToHero are cite-deferred;
// the landed behaviors are the walk-to-job-site + WorkAtPoi restock + UpdateActivityFromSchedule(99).
//
// In the jar getWorkPackage wraps WorkAtPoi (WorkAtComposter for FARMER) inside a RunOne at prio 5 alongside
// the not-yet-built strolls; WorkAtPoi is landed as a standalone prio-5 behavior (the RunOne only shuffles
// which sibling runs -- with the strolls deferred, WorkAtPoi is the sole live sibling, same observable restock).
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(WORK, getWorkPackage(profession),
//	 ImmutableSet.of(Pair.of(JOB_SITE, VALUE_PRESENT))). getWorkPackage: RunOne{WorkAtPoi w7, StrollAroundPoi,
//	 StrollToPoi, StrollToPoiList, HarvestFarmland, UseBonemeal} at prio 5; SetWalkTargetFromBlockMemory
//	 .create(JOB_SITE, 0.5f, 9, 100, 1200) at prio 2; UpdateActivityFromSchedule.create() at prio 99.]
func villagerInitWorkActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 2, behavior: newSetWalkTargetFromBlockMemory(memJobSite, 0.5, 9)},
		{priority: 5, behavior: newVillagerWorkAtPoi()},
		{priority: 99, behavior: newUpdateActivityFromSchedule()},
	}
	conditions := []memoryCondition{{key: memJobSite, status: memValuePresent}}
	return activityDataCreatePairs(activityWork, pairs, conditions, nil)
}

// workAtPoiCheckCooldown is WorkAtPoi.CHECK_COOLDOWN (ldc2_w 300L): the minimum gameTime gap between two
// checkExtraStartConditions passes (the lastCheck throttle).
//
//	[VERIFIED javap WorkAtPoi.checkExtraStartConditions: gameTime - lastCheck < 300L -> false.]
const workAtPoiCheckCooldown int64 = 300

// workAtPoiDistance is WorkAtPoi.DISTANCE (ldc2_w 1.73d): the villager must be within 1.73 blocks (center)
// of its JOB_SITE for the workstation check to pass.
//
//	[VERIFIED javap WorkAtPoi.checkExtraStartConditions: jobSite.pos().closerToCenterThan(position, 1.73).]
const workAtPoiDistance = 1.73

// newVillagerWorkAtPoi ports net.minecraft.world.entity.ai.behavior.WorkAtPoi -- THE RESTOCK BEHAVIOR that
// wires the already-ported villagerShouldRestock + villagerRestock (villager_reputation.go), which had ZERO
// callers before this. entryCondition: present(JOB_SITE), registered(LOOK_TARGET). checkExtraStartConditions
// throttles on lastCheck (300-tick gap) AND a coin flip (random.nextInt(2) != 0 -> false, i.e. a 1-in-2 pass),
// then requires the villager within DISTANCE(1.73) of the JOB_SITE. start: (LAST_WORKED_AT_POI stamp +
// LOOK_TARGET refresh + playWorkSound + useWorkstation cite-deferred) and THE PAYOFF -- if shouldRestock()
// then restock() (the twice-a-day trade restock: updateDemand + resetUses + lastRestockGameTime + count++).
//
// The random.nextInt(2) coin flip draws off the region levelRandom (level.getRandom()), like AcquirePoi's
// rate jitter -- NOT the mob stream, so the pig oracle stream is untouched. useWorkstation (WorkAtComposter's
// compost) + playWorkSound + the LAST_WORKED_AT_POI/LOOK_TARGET writes are cite-deferred (no compost/sound
// seam; those memory keys are not load-bearing for the restock); the restock is the load-bearing 1:1 part.
//
//	[VERIFIED javap WorkAtPoi: super(ImmutableMap.of(JOB_SITE, VALUE_PRESENT, LOOK_TARGET, REGISTERED));
//	 CHECK_COOLDOWN=300, DISTANCE=1.73; checkExtraStartConditions: gameTime-lastCheck<300 -> false;
//	 random.nextInt(2)!=0 -> false; lastCheck=gameTime; jobSite.dimension==level.dimension &&
//	 jobSite.pos().closerToCenterThan(position, 1.73). start: setMemory(LAST_WORKED_AT_POI, gameTime);
//	 jobSite.pos -> LOOK_TARGET; playWorkSound; useWorkstation; if shouldRestock(level) restock().]
func newVillagerWorkAtPoi() *behavior {
	var lastCheck int64
	cond := []memoryCondition{
		{key: memJobSite, status: memValuePresent},
		{key: memLookTarget, status: memRegistered},
	}
	b := newBehavior(cond, behaviorDuration, behaviorDuration)
	b.checkExtraStart = func(t *TickLoop, e *Entity) bool {
		gameTime := t.GameTime()
		if gameTime-lastCheck < workAtPoiCheckCooldown {
			return false
		}
		region := t.cur()
		if region == nil {
			return false
		}
		// random.nextInt(2) != 0 -> false; the check passes only on a 0 draw (1-in-2). Off the region
		// levelRandom (level.getRandom()), like AcquirePoi's rate jitter -- never the mob stream.
		if villagerWorkCoinFlip(region) != 0 {
			return false
		}
		lastCheck = gameTime
		v, ok := e.brain.getMemory(memJobSite)
		if !ok {
			return false
		}
		pos, ok := v.(pk.Position)
		if !ok {
			return false
		}
		// jobSite.pos().closerToCenterThan(position, 1.73) (the dimension check is same-dimension, cite-reduced
		// exactly like SetWalkTargetFromBlockMemory -- one overworld region per villager).
		return villagerCloserToCenterThan(pos, e.x, e.y, e.z, workAtPoiDistance)
	}
	b.start = func(t *TickLoop, e *Entity, _ int64) {
		// LAST_WORKED_AT_POI stamp + LOOK_TARGET refresh + playWorkSound + useWorkstation (WorkAtComposter
		// compost) are cite-deferred. THE PAYOFF: if shouldRestock() restock().
		if villagerShouldRestock(e, t.GameTime()) {
			villagerRestock(e, t.GameTime())
		}
	}
	return b
}

// villagerWorkCoinFlip is the WorkAtPoi random.nextInt(2) coin flip, drawn off the region levelRandom
// (level.getRandom()). Returns 0 (the pass value) when the region has no seeded levelRandom (a bare test
// loop) -- a cited graceful degrade so the check passes in a test, never a panic. Mirrors villagerRateJitter.
// VERIFIED javap WorkAtPoi: random.nextInt(2).
func villagerWorkCoinFlip(region *region) int {
	if region == nil || region.levelRandom == nil {
		return 0
	}
	return int(region.levelRandom.NextIntN(2))
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
// THE BREEDING PAYOFF lands here: the prio-2 InteractWith(VILLAGER, canBreed, canBreed, BREED_TARGET) finds
// a nearby willing adult partner and sets BREED_TARGET; the prio-1 GateBehavior(BREED_TARGET, RUN_ONE,
// VillagerMakeLove) then walks the two together and, once the birth timer elapses, spawns a baby villager
// (given a vacant bed). The RunOne{InteractWith(INTERACTION_TARGET), InteractWith(CAT), VillageBoundRandomStroll,
// SetWalkTargetFromLookTarget, JumpOnBed, DoNothing}, GiveGiftToHero, trade chains (ShowTradesToPlayer/
// SetLookAndInteract/TradeWithVillager) are cite-deferred (interaction/trade-brain subsystems). The landed
// slice is the breed chain + UpdateActivityFromSchedule(99) so IDLE keeps re-sampling the schedule. IDLE is
// Brain.defaultActivity, so it is always available.
//
//	[VERIFIED javap Villager ActivitySupplier: ActivityData.create(IDLE, getIdlePackage()). getIdlePackage:
//	 Pair.of(2, RunOne{..., InteractWith.of(VILLAGER, 8, canBreed, canBreed, BREED_TARGET, speed, 2), ...});
//	 Pair.of(1, new GateBehavior(of(), of(BREED_TARGET), ORDERED, RUN_ONE, [Pair.of(1, new VillagerMakeLove)]));
//	 Pair.of(3, GiveGiftToHero/trade gates deferred); Pair.of(99, UpdateActivityFromSchedule.create()).]
func villagerInitIdleActivity() activityData {
	// getIdlePackage: the BREED_TARGET-scoped GateBehavior (empty entry group, exitErase BREED_TARGET,
	// ORDERED, RUN_ONE, single sub VillagerMakeLove at weight 1) at prio 1; the BREED_TARGET-setting
	// InteractWith is one sibling of the prio-2 RunOne (its other siblings are cite-deferred).
	breedGate := newGateBehavior(nil, []memoryKey{memBreedTarget}, orderOrdered, runOne,
		[]gateWeighted{{behavior: newVillagerMakeLove(), weight: 1}})
	pairs := []prioritizedBehavior{
		{priority: 1, behavior: breedGate},
		{priority: 2, behavior: newVillagerInteractWithBreedTarget()},
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

// villagerInteractRange is the InteractWith interactionRange for the IDLE breed pairing: getIdlePackage
// passes 8, and InteractWith.of squares it (interactionRange*interactionRange = 64) for the findClosest
// distanceToSqr gate. VERIFIED javap InteractWith.of: `int i = interactionRange * interactionRange;`
// (interactionRange 8 -> 64) + lambda$of$6: `target.distanceToSqr(candidate) <= i`.
const villagerInteractRange = 8

// newVillagerInteractWithBreedTarget ports InteractWith.of(EntityType.VILLAGER, 8, Villager::canBreed,
// Villager::canBreed, BREED_TARGET, speed, 2) -- the IDLE-package pairing behavior that SETS BREED_TARGET.
// entryCondition (declarative group): registered(BREED_TARGET), registered(LOOK_TARGET), absent(WALK_TARGET),
// present(VISIBLE_MOBS). The trigger: selfFilter(self) [self canBreed] && the nearby list contains a
// targetFilter match [a villager that canBreed] && findClosest within interactionRange^2 (64) -> set
// BREED_TARGET = target, LOOK_TARGET = EntityTracker(target,true), WALK_TARGET = WalkTarget(EntityTracker
// (target,false), speed, closeEnough=2).
//
// The VISIBLE_MOBS (net.minecraft NEAREST_VISIBLE_LIVING_ENTITIES, renamed visible_mobs in 26.2) sensor is
// not wired for villagers, so the "nearby visible list" is CITE-REDUCED to a direct region entity scan for
// the closest OTHER villager within interactionRange (region.entities.near, the villagerHostilesSensor
// pattern) -- the same "the closest breedable villager in range becomes the breed target" observable. Both
// the self and target canBreed filters (Villager::canBreed) are the REAL villagerCanBreed gate (food>=12 +
// adult + awake). The WALK_TARGET/LOOK_TARGET writes use the ported EntityTracker/WalkTarget; the visible/
// line-of-sight test folds into "in range and alive". Cite InteractWith.of + Villager.canBreed +
// VillagerGoalPackages.getIdlePackage (the BREED_TARGET InteractWith).
func newVillagerInteractWithBreedTarget() *oneShot {
	cond := []memoryCondition{
		{key: memBreedTarget, status: memRegistered},
		{key: memLookTarget, status: memRegistered},
		{key: memWalkTarget, status: memValueAbsent},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		// selfFilter.test(self): the villager itself must be able to breed.
		if !villagerCanBreed(e) {
			return false
		}
		region := t.cur()
		if region == nil || region.entities == nil {
			return false
		}
		// nearbyEntities.contains(targetFilter) && findClosest(within interactionRange^2 && targetFilter):
		// the closest OTHER villager within range that canBreed. rangeSqr == interactionRange^2 (64).
		rangeSqr := float64(villagerInteractRange * villagerInteractRange)
		best := rangeSqr
		var partner *Entity
		for _, other := range region.entities.near(e.x, e.z, 1) {
			if other == e || !other.isAlive() {
				continue
			}
			if other.typ != entity.Villager.ID { // target.is(EntityType.VILLAGER)
				continue
			}
			if !villagerCanBreed(other) { // the target canBreed filter
				continue
			}
			dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
			d2 := dx*dx + dy*dy + dz*dz
			if d2 <= best {
				best = d2
				partner = other
			}
		}
		if partner == nil {
			return false
		}
		// set(target): BREED_TARGET = EntityTracker(target, true) — stored as the THIN entity id (Folia rule),
		// exactly as HURT_BY_ENTITY/NEAREST_HOSTILE are. LOOK_TARGET/WALK_TARGET point at the partner.
		e.brain.setMemory(memBreedTarget, partner.id)
		e.brain.setMemory(memLookTarget, newEntityTracker(partner.id, true, true))
		e.brain.setMemory(memWalkTarget, newWalkTarget(newEntityTracker(partner.id, false, false), 0.5, 2))
		return true
	})
}

// villagerMakeLoveMinDuration / villagerMakeLoveMaxDuration are the VillagerMakeLove Behavior(map, 350, 350)
// min/max duration (sipush 350 twice): a fixed 350-tick run window.
//
//	[VERIFIED javap VillagerMakeLove.<init>: super(ImmutableMap.of(BREED_TARGET, VALUE_PRESENT,
//	 NEAREST_VISIBLE_LIVING_ENTITIES, VALUE_PRESENT), 350, 350).]
const (
	villagerMakeLoveMinDuration = 350
	villagerMakeLoveMaxDuration = 350
)

// villagerMakeLoveBaseBirthDelay / villagerMakeLoveBirthJitter are the birthTimestamp offset in
// VillagerMakeLove.start: `birthTimestamp = timestamp + 275 + random.nextInt(50)` (sipush 275, bipush 50).
//
//	[VERIFIED javap VillagerMakeLove.start: i = 275 + villager.getRandom().nextInt(50); birthTimestamp =
//	 timestamp + i.]
const (
	villagerMakeLoveBaseBirthDelay = 275
	villagerMakeLoveBirthJitter    = 50
)

// villagerMakeLoveDistanceSqr is the VillagerMakeLove.tick proximity gate (ldc2_w 5.0d): the two villagers
// must be within distanceToSqr <= 5.0 for the birth timer to advance to birth.
//
//	[VERIFIED javap VillagerMakeLove.tick: if (villager.distanceToSqr(partner) > 5.0) return.]
const villagerMakeLoveDistanceSqr = 5.0

// villagerMakeLoveHeartChance is the VillagerMakeLove.tick pre-birth heart-particle chance (bipush 35):
// random.nextInt(35) == 0 fires the love-heart entity event while walking together.
//
//	[VERIFIED javap VillagerMakeLove.tick: if (villager.getRandom().nextInt(35) == 0) broadcastEntityEvent(12).]
const villagerMakeLoveHeartChance = 35

// newVillagerMakeLove ports net.minecraft.world.entity.ai.behavior.VillagerMakeLove -- THE BREEDING BEHAVIOR
// (a real baby villager). It runs while BREED_TARGET is set (the InteractWith above sets it). start rolls a
// birthTimestamp = ts + 275 + nextInt(50) and fires the love-particle event; tick walks the pair together,
// and once ts >= birthTimestamp eats-and-digests both parents' food (spending the breeding food) and
// tryToGiveBirth (take a vacant HOME bed -> spawn a baby villager at the parents; no bed -> sad particles).
//
// Reductions (cited): the entry NEAREST_VISIBLE_LIVING_ENTITIES(visible_mobs) present-requirement is dropped
// (that sensor is not wired -- the InteractWith already guaranteed a visible in-range partner when it set
// BREED_TARGET); BehaviorUtils.lockGazeAndWalkToEachOther is reduced to a WALK_TARGET toward the partner (the
// LOOK_TARGET+mutual-walk seam); broadcastEntityEvent(18/12/13) love/heart/sad particles are cite-deferred
// client visuals (no villager entity-event wire). The birth RNG (nextInt(50) start + nextInt(35) tick) draws
// off the MOB stream (villager.getRandom()) -- villager-gated, never the pig oracle stream. isBreedingPossible
// (canStillUse) re-checks BREED_TARGET is a Villager + self canBreed + target canBreed each run.
//
//	[VERIFIED javap VillagerMakeLove: <init>(BREED_TARGET PRESENT, NEAREST_VISIBLE_LIVING_ENTITIES PRESENT,
//	 350, 350); checkExtraStartConditions = isBreedingPossible; canStillUse = ts <= birthTimestamp &&
//	 isBreedingPossible; start: lockGazeAndWalkToEachOther(0.5, 2); broadcastEntityEvent(18)x2; birthTimestamp
//	 = ts + 275 + nextInt(50). tick: if distanceToSqr>5.0 return; lockGaze; if ts>=birthTimestamp {
//	 eatAndDigestFood x2; tryToGiveBirth } else if nextInt(35)==0 broadcastEntityEvent(12)x2. stop: erase
//	 BREED_TARGET. isBreedingPossible: BREED_TARGET is Villager && canBreed() && target.canBreed().]
func newVillagerMakeLove() *behavior {
	var birthTimestamp int64
	cond := []memoryCondition{
		{key: memBreedTarget, status: memValuePresent},
	}
	b := newBehavior(cond, villagerMakeLoveMinDuration, villagerMakeLoveMaxDuration)
	b.checkExtraStart = func(t *TickLoop, e *Entity) bool {
		return villagerMakeLoveIsBreedingPossible(t, e)
	}
	b.canStillUse = func(t *TickLoop, e *Entity, timestamp int64) bool {
		// canStillUse: timestamp <= birthTimestamp && isBreedingPossible.
		return timestamp <= birthTimestamp && villagerMakeLoveIsBreedingPossible(t, e)
	}
	b.start = func(t *TickLoop, e *Entity, timestamp int64) {
		partner := villagerMakeLoveTarget(t, e)
		if partner != nil {
			villagerLockGazeAndWalkToEachOther(e, partner)
		}
		// broadcastEntityEvent(18)x2 (love particles) — cite-deferred client visual.
		birthTimestamp = timestamp + villagerMakeLoveBaseBirthDelay + int64(mobRandom(e).nextInt(villagerMakeLoveBirthJitter))
	}
	b.tick = func(t *TickLoop, e *Entity, timestamp int64) {
		partner := villagerMakeLoveTarget(t, e)
		if partner == nil {
			return
		}
		dx, dy, dz := partner.x-e.x, partner.y-e.y, partner.z-e.z
		if dx*dx+dy*dy+dz*dz > villagerMakeLoveDistanceSqr {
			return // distanceToSqr(partner) > 5.0
		}
		villagerLockGazeAndWalkToEachOther(e, partner)
		if timestamp >= birthTimestamp {
			villagerEatAndDigestFood(e)       // villager.eatAndDigestFood()
			villagerEatAndDigestFood(partner) // partner.eatAndDigestFood()
			t.villagerTryToGiveBirth(e, partner)
		} else if mobRandom(e).nextInt(villagerMakeLoveHeartChance) == 0 {
			// broadcastEntityEvent(12)x2 (heart particles) — cite-deferred client visual.
		}
	}
	b.stop = func(_ *TickLoop, e *Entity, _ int64) {
		e.brain.eraseMemory(memBreedTarget) // stop: brain.eraseMemory(BREED_TARGET)
	}
	return b
}

// villagerMakeLoveTarget resolves the BREED_TARGET memory (a THIN villager id) to its live *Entity, or nil.
func villagerMakeLoveTarget(t *TickLoop, e *Entity) *Entity {
	id, ok := e.brain.getMemoryEntityID(memBreedTarget)
	if !ok {
		return nil
	}
	region := t.cur()
	if region == nil || region.entities == nil {
		return nil
	}
	other, ok := region.entities.get(id)
	if !ok || other == nil || !other.isAlive() {
		return nil
	}
	return other
}

// villagerMakeLoveIsBreedingPossible ports VillagerMakeLove.isBreedingPossible(villager): BREED_TARGET is
// present, is a Villager, and BehaviorUtils.targetIsValid holds, AND villager.canBreed() AND the
// AgeableMob (the partner villager) canBreed(). targetIsValid (present + alive + right type) folds into the
// resolve returning a live villager. Cite VillagerMakeLove.isBreedingPossible + Villager.canBreed.
//
//	[VERIFIED javap VillagerMakeLove.isBreedingPossible: BREED_TARGET filter isPresent; targetIsValid(brain,
//	 BREED_TARGET, VILLAGER); villager.canBreed(); ((AgeableMob)target).canBreed().]
func villagerMakeLoveIsBreedingPossible(t *TickLoop, e *Entity) bool {
	partner := villagerMakeLoveTarget(t, e)
	if partner == nil || partner.typ != entity.Villager.ID {
		return false
	}
	return villagerCanBreed(e) && villagerCanBreed(partner)
}

// villagerLockGazeAndWalkToEachOther ports BehaviorUtils.lockGazeAndWalkToEachOther(a, b, speed=0.5, close=2)
// -- REDUCED to setting each villager's WALK_TARGET (+ LOOK_TARGET) toward the other (the mutual walk). The
// full lockGaze also sets NEAREST_VISIBLE_LIVING_ENTITIES-scoped look; here it points LOOK_TARGET at the
// partner and WALK_TARGET to close within 2 blocks. Cite BehaviorUtils.lockGazeAndWalkToEachOther.
func villagerLockGazeAndWalkToEachOther(a, b *Entity) {
	a.brain.setMemory(memLookTarget, newEntityTracker(b.id, true, true))
	a.brain.setMemory(memWalkTarget, newWalkTarget(newEntityTracker(b.id, false, false), 0.5, 2))
	b.brain.setMemory(memLookTarget, newEntityTracker(a.id, true, true))
	b.brain.setMemory(memWalkTarget, newWalkTarget(newEntityTracker(a.id, false, false), 0.5, 2))
}

// villagerTryToGiveBirth ports VillagerMakeLove.tryToGiveBirth(level, parent, partner): take a vacant HOME
// bed; if none -> sad particles (cite-deferred, no baby); else spawn a baby villager at the parents (breed)
// and give it the bed. The bed is claimed via the POI manager (takeVacantBed == PoiManager.take(HOME,
// canReach, blockPos, 48)); a nil breed result releases the bed (never reached -- the villager species always
// yields a baby). giveBedToChild sets the child's HOME memory to the claimed bed pos.
//
//	[VERIFIED javap VillagerMakeLove.tryToGiveBirth: bedPos = takeVacantBed(level, parent); if empty ->
//	 broadcastEntityEvent(13)x2 (sad); else child = breed(level, parent, partner); if present giveBedToChild
//	 (child.HOME = bedPos) else poiManager.release(bedPos). takeVacantBed: PoiManager.take(HOME predicate,
//	 canReach biPred, parent.blockPosition, 48).]
func (t *TickLoop) villagerTryToGiveBirth(parent, partner *Entity) {
	bedPos, took := t.villagerTakeVacantBed(parent)
	if !took {
		// broadcastEntityEvent(13)x2 (no vacant bed -> sad particles) — cite-deferred client visual; NO baby.
		return
	}
	child := t.villagerBreed(parent, partner)
	if child == nil {
		// breed returned no offspring (unreached for a villager) -> release the bed, exactly as the jar.
		if pm := t.villagerPoiManagerFor(parent); pm != nil {
			if rec := pm.recordAt(bedPos); rec != nil {
				rec.releaseTicket()
				pm.setDirty()
			}
		}
		return
	}
	// giveBedToChild(level, child, bedPos): the baby claims the bed as its HOME memory.
	child.brain.setMemory(memHome, bedPos)
}

// villagerPoiManagerFor returns the POI manager of the parent's owning region (level.getPoiManager()), or nil.
func (t *TickLoop) villagerPoiManagerFor(e *Entity) *poiManager {
	region := t.regionForEntity(e)
	if region == nil {
		region = t.cur()
	}
	if region == nil {
		return nil
	}
	return region.poiManager
}

// villagerTakeVacantBed ports VillagerMakeLove.takeVacantBed(level, villager): PoiManager.take(HOME-type,
// canReach-biPredicate, villager.blockPosition(), 48) -- find and CLAIM the closest reachable vacant HOME
// (bed) POI within 48 blocks. The canReach reachability biPredicate is cite-reduced (no synchronous A* seam,
// exactly like AcquirePoi's canReach), so the CLOSEST HAS_SPACE HOME is taken directly (freeTickets-- ->
// IS_OCCUPIED). Returns the claimed bed pos, or (zero,false) when no vacant bed is in range.
//
//	[VERIFIED javap VillagerMakeLove.takeVacantBed: poiManager.take(r -> r.is(PoiTypes.HOME), this::canReach,
//	 villager.blockPosition(), 48); PoiManager.take -> findClosest HAS_SPACE + acquireTicket.]
func (t *TickLoop) villagerTakeVacantBed(e *Entity) (pk.Position, bool) {
	pm := t.villagerPoiManagerFor(e)
	if pm == nil {
		return pk.Position{}, false
	}
	center := pk.Position{X: floorInt(e.x), Y: floorInt(e.y), Z: floorInt(e.z)}
	pos, ok := pm.findClosest(poiTypeIsHome, center, villagerTakeVacantBedRange, poiOccupancyHasSpace)
	if !ok {
		return pk.Position{}, false
	}
	return pm.take(poiTypeIsHome, pos)
}

// villagerTakeVacantBedRange is the VillagerMakeLove.takeVacantBed scan radius (bipush 48).
//
//	[VERIFIED javap VillagerMakeLove.takeVacantBed: PoiManager.take(..., villager.blockPosition(), 48).]
const villagerTakeVacantBedRange = 48

// villagerBreed ports VillagerMakeLove.breed(level, parent, partner) == parent.spawnChildFromBreeding(level,
// partner) for the villager species: spawn a baby villager at the parent, set both parents to the breeding
// cooldown (setAge(6000)), reset both inLove, and fire the heart burst. The XP-orb draw of the generic
// Animal.finalizeSpawnChildFromBreeding is NOT part of the villager path (Villager overrides the breed to a
// plain baby-villager spawn with no XP orb), so it is intentionally omitted here. The child is a BABY
// (breedAge = BABY_START_AGE) with the half-scale hitbox + DATA_BABY_ID. Returns the child, or nil if the
// villager mob is not registered (never in a booted server).
//
//	[VERIFIED javap VillagerMakeLove.breed: ageableMob = parent.getBreedOffspring(level, partner); if null
//	 return empty; parent.setAge(6000); partner.setAge(6000); ageableMob.setAge(-24000);
//	 ageableMob.moveTo(parent pos); finalizeSpawn; level.addFreshEntityWithPassengers; broadcastEntityEvent(
//	 18); return ageableMob. Villager.getBreedOffspring: EntityType.VILLAGER.create + setVillagerData(type).]
func (t *TickLoop) villagerBreed(parent, partner *Entity) *Entity {
	if t.mobRegistry == nil {
		return nil
	}
	if _, ok := t.mobRegistry.byName[vanillaVillagerMobName]; !ok {
		return nil
	}
	// getBreedOffspring: spawn a baby villager at the parent position (moveTo(parent.getX/Y/Z)).
	child := t.spawnVanillaMob(vanillaVillagerMobName, parent.x, parent.y, parent.z)
	if child == nil {
		return nil
	}
	// setAge(-24000) == setBaby(true): the child is a baby; refresh to the half-scale hitbox + push
	// DATA_BABY_ID=true (mirroring ai_goals_breed.breed's baby init, incl. the CR-01 metadata splice so a
	// late-tracker builds AddEntity/SetEntityData with DATA_BABY_ID=true rather than a stale full-size box).
	child.breedAge = babyStartAge
	child.refreshDimensions()
	if child.isBaby() {
		var buf bytes.Buffer
		_, _ = babyDataEntry(child.isBaby()).WriteTo(&buf)
		child.metadata = append(child.metadata, buf.Bytes()...)
	}
	t.broadcastBabyFlag(child)
	// finalizeSpawnChildFromBreeding: both parents to the 6000-tick breeding cooldown (setAge(6000)); reset
	// both inLove (resetLove == inLove = 0); fire the heart-event burst (broadcastEntityEvent(18)).
	parent.breedAge = breedingCooldownAge
	partner.breedAge = breedingCooldownAge
	parent.inLove = 0
	partner.inLove = 0
	t.broadcastHearts(parent)
	return child
}
