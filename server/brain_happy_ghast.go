package server

// brain_happy_ghast.go — wires the ported Brain subsystem for the BABY HappyGhast, 1:1 from the jar
// (temp/cache/26.2-inner.jar). This ports HappyGhastAi (makeBrain/getActivities/updateActivity), the
// HappyGhast.BRAIN_PROVIDER sensor+activity set, and the behaviors HappyGhastAi registers, then hooks
// HappyGhast.customServerAiStep's baby-only brain.tick.
//
// THE JAR CHAIN (all javap/CFR-cited):
//   - HappyGhast.BRAIN_PROVIDER = Brain.provider(List.of(SensorType.NEAREST_LIVING_ENTITIES, HURT_BY,
//     FOOD_TEMPTATIONS, NEAREST_ADULT_ANY_TYPE, NEAREST_PLAYERS), happyGhast -> HappyGhastAi.getActivities()).
//   - HappyGhastAi.getActivities = [initCoreActivity, initIdleActivity, initPanicActivity].
//   - initCoreActivity  = ActivityData.create(CORE, 0, [Swim(0.8), AnimalPanic(2.0,0), LookAtTargetSink(45,90),
//     MoveToTargetSink(), CountDownCooldownTicks(TEMPTATION_COOLDOWN_TICKS)]).
//   - initIdleActivity  = ActivityData.create(IDLE, [ (1, FollowTemptation(spd=1.25, closeEnough=3.0, eyes=true)),
//     (2, BabyFollowAdult(range 3..16, spd=1.1, NEAREST_VISIBLE_PLAYER, eye=true)),
//     (3, BabyFollowAdult(range 3..16, spd=1.1, NEAREST_VISIBLE_ADULT,  eye=true)),
//     (4, RunOne([ (RandomStroll.fly(1.0),1), (SetWalkTargetFromLookTarget(1.0,3),1) ])) ]).
//   - initPanicActivity = ActivityData.create(PANIC, [], {(IS_PANICKING, VALUE_PRESENT)}).
//   - HappyGhastAi.updateActivity = brain.setActiveActivityToFirstValid([PANIC, IDLE]).
//   - HappyGhast.customServerAiStep: if isBaby() { brain.tick(level, this); HappyGhastAi.updateActivity(this); }
//     then checkRestriction(); super.customServerAiStep(). (VERIFIED — the brain drives ONLY the baby.)
//
// Cite: HappyGhast, HappyGhastAi, Brain.provider.

// happyGhastFollowMin / happyGhastFollowMax are HappyGhastAi.ADULT_FOLLOW_RANGE = UniformInt.of(3, 16)
// (the BabyFollowAdult follow band: closerThan(max+1) && !closerThan(min), closeEnough = min-1).
//
//	[VERIFIED CFR HappyGhastAi: ADULT_FOLLOW_RANGE = UniformInt.of(3, 16).]
const (
	happyGhastFollowMin = 3
	happyGhastFollowMax = 16
)

// happyGhast tempt/follow speed multipliers (HappyGhastAi constants).
//
//	[VERIFIED CFR HappyGhastAi: SPEED_MULTIPLIER_WHEN_TEMPTED=1.25f; SPEED_MULTIPLIER_WHEN_FOLLOWING_ADULT
//	 =1.1f; SPEED_MULTIPLIER_WHEN_IDLING=1.0f; BABY_GHAST_CLOSE_ENOUGH_DIST=3.0.]
const (
	happyGhastTemptedSpeed     = 1.25
	happyGhastFollowAdultSpeed = 1.1
	happyGhastIdleSpeed        = 1.0
	happyGhastTemptCloseEnough = 3.0
)

// attachHappyGhastBrain builds and attaches the ported Brain to a happy ghast at spawn. Vanilla ALWAYS
// makes the brain (Mob.brain in the ctor via makeBrain -> BRAIN_PROVIDER.makeBrain); the brain only
// DRIVES movement for the baby (customServerAiStep gate). We mirror that: attach for every happy ghast so
// the state is present, and gate the tick on isBaby() at the call site (happyGhastBabyBrainTick).
//
//	[VERIFIED CFR HappyGhast.makeBrain: BRAIN_PROVIDER.makeBrain(this, packedBrain).]
func attachHappyGhastBrain(e *Entity) {
	e.brain = newHappyGhastBrainProvider().makeBrain(e)
}

// happyGhastBabyBrainTick ports HappyGhast.customServerAiStep's baby branch: if isBaby() tick the brain,
// then updateActivity. Called from tickAI for a live happy ghast AFTER serverAiStep (the same slot the
// classic ghast flight uses). The ADULT keeps the classic goal-driven flight (happyGhastAiStep) — the
// brain is dormant for it (never ticked), exactly as vanilla runs the brain only for the baby.
//
//	[VERIFIED CFR HappyGhast.customServerAiStep: if (isBaby()) { getBrain().tick(level, this);
//	 HappyGhastAi.updateActivity(this); } checkRestriction(); super.customServerAiStep(level).]
func (t *TickLoop) happyGhastBabyBrainTick(e *Entity) {
	if e.brain == nil || !e.isBaby() {
		return
	}
	gameTime := t.GameTime()
	e.brain.tick(t, e, gameTime)
	happyGhastUpdateActivity(e)
}

// happyGhastUpdateActivity ports HappyGhastAi.updateActivity: setActiveActivityToFirstValid([PANIC, IDLE]).
//
//	[VERIFIED CFR HappyGhastAi.updateActivity: getBrain().setActiveActivityToFirstValid(
//	 ImmutableList.of(Activity.PANIC, Activity.IDLE)).]
func happyGhastUpdateActivity(e *Entity) {
	e.brain.setActiveActivityToFirstValid(activityPanic, activityIdle)
}

// newHappyGhastBrainProvider builds the brainProvider for the happy ghast: the five sensors + the three
// activities HappyGhastAi registers, in the exact order. The provider registers each sensor's required
// memories + the activities' behaviors' required memories (Brain constructor), so every memory the
// behaviors read/write is registered before the first tick.
//
// SENSOR SCAN-RATE: vanilla Sensor.tick has a randomlyDelayStart + scanRate (doTick every N ticks); the
// ported sensors here scan EVERY tick (scanRate 1 — a cited perf-neutral simplification: fresher memories,
// identical observable steering; the scanRate jitter draw is deferred and cited).
//
//	[VERIFIED CFR HappyGhast.BRAIN_PROVIDER + HappyGhastAi.getActivities (see file header).]
func newHappyGhastBrainProvider() *brainProvider {
	p := newBrainProvider()
	// Sensors, in the provider's List order (Brain.tickSensors walks this order).
	p.addSensor(sensorNearestLivingEntities, sensorNearestLivingEntitiesTick, memMobs, memVisibleMobs)
	p.addSensor(sensorHurtBy, sensorHurtByTick, memHurtBy)
	p.addSensor(sensorFoodTemptations, sensorFoodTemptationsTick, memTemptingPlayer)
	p.addSensor(sensorNearestAdultAnyType, sensorNearestAdultAnyTypeTick, memNearestVisibleAdult)
	p.addSensor(sensorNearestPlayers, sensorNearestPlayersTick, memNearestPlayers, memNearestVisiblePlayer)
	// Activities.
	p.addActivity(happyGhastInitCoreActivity())
	p.addActivity(happyGhastInitIdleActivity())
	p.addActivity(happyGhastInitPanicActivity())
	return p
}

// happyGhastInitCoreActivity ports HappyGhastAi.initCoreActivity: ActivityData.create(CORE, 0, [Swim(0.8),
// AnimalPanic(2.0,0), LookAtTargetSink(45,90), MoveToTargetSink(), CountDownCooldownTicks(TEMPTATION_
// COOLDOWN_TICKS)]). Priorities 0..4 (createPriorityPairs from 0).
//
//	[VERIFIED CFR HappyGhastAi.initCoreActivity: ActivityData.create(Activity.CORE, 0, ImmutableList.of(
//	 new Swim(0.8f), new AnimalPanic(2.0f, 0), new LookAtTargetSink(45, 90), new MoveToTargetSink(),
//	 new CountDownCooldownTicks(MemoryModuleType.TEMPTATION_COOLDOWN_TICKS))).]
func happyGhastInitCoreActivity() activityData {
	return activityDataCreate(activityCore, 0, []behaviorControl{
		newSwim(0.8),
		newAnimalPanic(2.0),
		newLookAtTargetSink(45, 90),
		newMoveToTargetSink(150, 250),
		newCountDownCooldownTicks(memTemptationCooldownTicks),
	})
}

// happyGhastInitIdleActivity ports HappyGhastAi.initIdleActivity: explicit (priority, behavior) pairs:
// (1, FollowTemptation spd=1.25 closeEnough=3.0 eyes=true), (2, BabyFollowAdult NEAREST_VISIBLE_PLAYER),
// (3, BabyFollowAdult NEAREST_VISIBLE_ADULT), (4, RunOne[ RandomStroll.fly(1.0) w1, SetWalkTargetFrom
// LookTarget(1.0,3) w1 ]).
//
//	[VERIFIED CFR HappyGhastAi.initIdleActivity: Pair.of(1, new FollowTemptation(mob->1.25f, mob->3.0,
//	 true)); Pair.of(2, BabyFollowAdult.create(ADULT_FOLLOW_RANGE, mob->1.1f, NEAREST_VISIBLE_PLAYER,
//	 true)); Pair.of(3, BabyFollowAdult.create(ADULT_FOLLOW_RANGE, mob->1.1f, NEAREST_VISIBLE_ADULT,
//	 true)); Pair.of(4, new RunOne(ImmutableList.of(Pair.of(RandomStroll.fly(1.0f), 1),
//	 Pair.of(SetWalkTargetFromLookTarget.create(1.0f, 3), 1)))).]
func happyGhastInitIdleActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 1, behavior: newFollowTemptation(happyGhastTemptedSpeed, happyGhastTemptCloseEnough, true)},
		{priority: 2, behavior: newBabyFollowAdult(happyGhastFollowMin, happyGhastFollowMax, happyGhastFollowAdultSpeed, memNearestVisiblePlayer, true)},
		{priority: 3, behavior: newBabyFollowAdult(happyGhastFollowMin, happyGhastFollowMax, happyGhastFollowAdultSpeed, memNearestVisibleAdult, true)},
		{priority: 4, behavior: newRunOne([]gateWeighted{
			{behavior: newRandomStrollFly(happyGhastIdleSpeed), weight: 1},
			{behavior: newSetWalkTargetFromLookTarget(happyGhastIdleSpeed, 3), weight: 1},
		})},
	}
	return activityDataCreatePairs(activityIdle, pairs, nil, nil)
}

// happyGhastInitPanicActivity ports HappyGhastAi.initPanicActivity: ActivityData.create(PANIC, [],
// {(IS_PANICKING, VALUE_PRESENT)}). No behaviors of its own — the panic MOVEMENT is AnimalPanic in CORE;
// PANIC is a gate activity that (a) is only valid while IS_PANICKING is set and (b) by NOT being IDLE,
// suppresses the IDLE behaviors while panicking.
//
//	[VERIFIED CFR HappyGhastAi.initPanicActivity: ActivityData.create(Activity.PANIC, ImmutableList.of(),
//	 Set.of(Pair.of(MemoryModuleType.IS_PANICKING, MemoryStatus.VALUE_PRESENT))).]
func happyGhastInitPanicActivity() activityData {
	return activityDataCreatePairs(activityPanic, nil,
		[]memoryCondition{{key: memIsPanicking, status: memValuePresent}}, nil)
}
