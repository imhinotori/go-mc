package server

import (
	"math"
	"sort"
)

// brain_behavior.go — the behavior/sensor/target layer of the ported Brain subsystem
// (temp/cache/26.2-inner.jar, javap -c -p / CFR this task). Ports 1:1:
//
//   - net.minecraft.world.entity.ai.behavior.BehaviorControl<E> (the interface: getStatus/
//     requiredMemories/tryStart/tickOrStop/doStop) + Behavior.Status {STOPPED, RUNNING}.
//   - net.minecraft.world.entity.ai.behavior.Behavior<E> (the abstract base: entryCondition,
//     min/max duration, endTimestamp; the FINAL tryStart/tickOrStop/doStop; hasRequiredMemories;
//     the overridable start/tick/stop/canStillUse/timedOut/checkExtraStartConditions).
//   - net.minecraft.world.entity.ai.behavior.OneShot<E> / the BehaviorBuilder trigger lambda shape.
//   - net.minecraft.world.entity.ai.behavior.GateBehavior<E> + RunOne (the SHUFFLED/RUN_ONE gate).
//   - net.minecraft.world.entity.ai.behavior.declarative.MemoryAccessor / PositionTracker / WalkTarget /
//     EntityTracker / BlockPosTracker (reduced to positionTracker + walkTarget here).
//   - net.minecraft.world.entity.ai.sensing.Sensor / SensorType (the per-tick memory writers).
//
// Cite: BehaviorControl, Behavior, OneShot, GateBehavior, RunOne, WalkTarget, PositionTracker, Sensor.

// behaviorStatus ports net.minecraft.world.entity.ai.behavior.Behavior$Status.
//
//	[VERIFIED CFR Behavior.Status: enum { STOPPED, RUNNING }.]
type behaviorStatus uint8

const (
	statusStopped behaviorStatus = iota // STOPPED
	statusRunning                       // RUNNING
)

// behaviorControl ports net.minecraft.world.entity.ai.behavior.BehaviorControl<E>.
//
//	[VERIFIED CFR BehaviorControl: getStatus(); getRequiredMemories(); tryStart(level,body,ts) bool;
//	 tickOrStop(level,body,ts); doStop(level,body,ts); debugString().]
type behaviorControl interface {
	getStatus() behaviorStatus
	requiredMemories() []memoryKey
	tryStart(t *TickLoop, e *Entity, timestamp int64) bool
	tickOrStop(t *TickLoop, e *Entity, timestamp int64)
	doStop(t *TickLoop, e *Entity, timestamp int64)
}

// behaviorDuration is Behavior.DEFAULT_DURATION (60): the default min==max tick lifetime a Behavior runs.
//
//	[VERIFIED CFR Behavior: public static final int DEFAULT_DURATION = 60.]
const behaviorDuration = 60

// behavior ports the net.minecraft.world.entity.ai.behavior.Behavior<E> abstract base. The overridable
// hooks are function fields (Go has no method-override): a concrete behavior fills the ones it needs and
// leaves the rest nil (the nil default == the vanilla protected default).
//
//	[VERIFIED CFR Behavior: fields entryCondition, status=STOPPED, endTimestamp, minDuration, maxDuration;
//	 final tryStart/tickOrStop/doStop; default start/tick/stop no-op; default canStillUse=false;
//	 default timedOut = ts > endTimestamp; default checkExtraStartConditions = true.]
type behavior struct {
	entryCondition []memoryCondition
	status         behaviorStatus
	endTimestamp   int64
	minDuration    int
	maxDuration    int

	// overridable hooks (nil == the vanilla protected default)
	checkExtraStart func(t *TickLoop, e *Entity) bool                  // default true
	start           func(t *TickLoop, e *Entity, timestamp int64)      // default no-op
	tick            func(t *TickLoop, e *Entity, timestamp int64)      // default no-op
	stop            func(t *TickLoop, e *Entity, timestamp int64)      // default no-op
	canStillUse     func(t *TickLoop, e *Entity, timestamp int64) bool // default false
	timedOut        func(timestamp int64) bool                         // default ts > endTimestamp
}

// newBehavior builds a behavior with the (entryCondition, min, max) constructor. Passing min==max is the
// (entryCondition, timeOutDuration) ctor; passing behaviorDuration for both is the (entryCondition) ctor.
//
//	[VERIFIED CFR Behavior ctors: (cond)->(cond,60,60); (cond,t)->(cond,t,t); (cond,min,max).]
func newBehavior(cond []memoryCondition, minDur, maxDur int) *behavior {
	return &behavior{entryCondition: cond, status: statusStopped, minDuration: minDur, maxDuration: maxDur}
}

func (b *behavior) getStatus() behaviorStatus { return b.status }

// requiredMemories ports Behavior.getRequiredMemories: entryCondition.keySet().
func (b *behavior) requiredMemories() []memoryKey {
	out := make([]memoryKey, 0, len(b.entryCondition))
	for _, c := range b.entryCondition {
		out = append(out, c.key)
	}
	return out
}

// hasRequiredMemories ports Behavior.hasRequiredMemories: every entryCondition (type,status) must
// checkMemory true against the body brain.
//
//	[VERIFIED CFR Behavior.hasRequiredMemories: for (type,status) in entryCondition:
//	 if !brain.checkMemory(type,status) return false; return true.]
func (b *behavior) hasRequiredMemories(e *Entity) bool {
	br := e.brain
	if br == nil {
		return false
	}
	for _, c := range b.entryCondition {
		if !br.checkMemory(c.key, c.status) {
			return false
		}
	}
	return true
}

// tryStart ports Behavior.tryStart (FINAL): if hasRequiredMemories && checkExtraStartConditions then set
// RUNNING, roll the endTimestamp = ts + min + random(max+1-min), run start(), return true.
//
//	[VERIFIED CFR Behavior.tryStart: if (hasRequiredMemories(body) && checkExtraStartConditions(level,
//	 body)) { status=RUNNING; duration=minDuration + level.getRandom().nextInt(maxDuration+1-minDuration);
//	 endTimestamp = timestamp + duration; start(level, body, timestamp); return true; } return false.]
func (b *behavior) tryStart(t *TickLoop, e *Entity, timestamp int64) bool {
	if !b.hasRequiredMemories(e) {
		return false
	}
	if b.checkExtraStart != nil && !b.checkExtraStart(t, e) {
		return false
	}
	b.status = statusRunning
	span := b.maxDuration + 1 - b.minDuration
	dur := b.minDuration
	if span > 0 {
		dur += mobRandom(e).nextInt(span)
	}
	b.endTimestamp = timestamp + int64(dur)
	if b.start != nil {
		b.start(t, e, timestamp)
	}
	return true
}

// tickOrStop ports Behavior.tickOrStop (FINAL): if !timedOut && canStillUse then tick, else doStop.
//
//	[VERIFIED CFR Behavior.tickOrStop: if (!timedOut(timestamp) && canStillUse(level,body,timestamp))
//	 tick(level,body,timestamp); else doStop(level,body,timestamp).]
func (b *behavior) tickOrStop(t *TickLoop, e *Entity, timestamp int64) {
	if !b.callTimedOut(timestamp) && b.callCanStillUse(t, e, timestamp) {
		if b.tick != nil {
			b.tick(t, e, timestamp)
		}
	} else {
		b.doStop(t, e, timestamp)
	}
}

// doStop ports Behavior.doStop (FINAL): status = STOPPED; stop().
//
//	[VERIFIED CFR Behavior.doStop: this.status = Status.STOPPED; stop(level, body, timestamp).]
func (b *behavior) doStop(t *TickLoop, e *Entity, timestamp int64) {
	b.status = statusStopped
	if b.stop != nil {
		b.stop(t, e, timestamp)
	}
}

// callTimedOut applies the timedOut hook or the default (ts > endTimestamp).
//
//	[VERIFIED CFR Behavior.timedOut(default): return timestamp > this.endTimestamp.]
func (b *behavior) callTimedOut(timestamp int64) bool {
	if b.timedOut != nil {
		return b.timedOut(timestamp)
	}
	return timestamp > b.endTimestamp
}

// callCanStillUse applies the canStillUse hook or the default (false).
//
//	[VERIFIED CFR Behavior.canStillUse(default): return false.]
func (b *behavior) callCanStillUse(t *TickLoop, e *Entity, timestamp int64) bool {
	if b.canStillUse != nil {
		return b.canStillUse(t, e, timestamp)
	}
	return false
}

// prioritizedBehavior pairs a priority with a behavior — the Pair<Integer, BehaviorControl> an
// ActivityData carries. addActivity consumes these.
type prioritizedBehavior struct {
	priority int
	behavior behaviorControl
}

// --- targets (PositionTracker / WalkTarget / EntityTracker / BlockPosTracker) --------------------

// positionTracker ports net.minecraft.world.entity.ai.behavior.PositionTracker (the target a WalkTarget/
// LookTarget points at). Vanilla has two impls: BlockPosTracker (a fixed point) and EntityTracker (a live
// entity, tracked by eye height). Go folds them into one value: if entityID != 0 it is an EntityTracker
// (resolved via the tick store — the THIN id, per the Folia rule, never a live *Entity pointer); else a
// BlockPosTracker at (x,y,z). trackEye/targetEye mirror EntityTracker.trackEyeHeight/targetEyeHeight.
//
//	[VERIFIED CFR PositionTracker: currentPosition()/currentBlockPosition()/isVisibleBy(body).
//	 EntityTracker.currentPosition: trackEyeHeight ? pos + eyeHeight : pos. BlockPosTracker: the fixed pos.]
type positionTracker struct {
	x, y, z   float64
	entityID  int32
	trackEye  bool
	targetEye bool
}

// blockPosTracker ports new BlockPosTracker(pos) / WalkTarget(Vec3): a fixed world point.
func blockPosTracker(x, y, z float64) positionTracker { return positionTracker{x: x, y: y, z: z} }

// entityTracker ports new EntityTracker(entity, trackEye, targetEye): a live-entity target by thin id.
func newEntityTracker(id int32, trackEye, targetEye bool) positionTracker {
	return positionTracker{entityID: id, trackEye: trackEye, targetEye: targetEye}
}

// currentPosition ports PositionTracker.currentPosition: for an entity tracker resolve the live position
// (+ eye height when trackEye); for a block tracker the fixed point. A stale entity id (gone from the
// store) falls back to the last-known/fixed (x,y,z), which for a fresh entity tracker is (0,0,0) — the
// caller guards on the memory being present, and a running behavior re-reads the tracker each tick.
//
//	[VERIFIED CFR EntityTracker.currentPosition: trackEyeHeight ? entity.position()+eyeHeight : position.]
func (p positionTracker) currentPosition(t *TickLoop) (x, y, z float64) {
	if p.entityID == 0 {
		return p.x, p.y, p.z
	}
	if tx, ty, tz, h, ok := t.resolveTargetPos(p.entityID); ok {
		if p.trackEye {
			// getEyeHeight: LivingEntity default ≈ 0.85*height; the exact per-type EyeHeight lives in
			// the dimensions table (cited default — the ghast's steering is unaffected by the small
			// eye offset, and the resolver hands the live height so this becomes exact when the table
			// eye-height read lands).
			ty += h * 0.85
		}
		return tx, ty, tz
	}
	return p.x, p.y, p.z
}

// walkTarget ports net.minecraft.world.entity.ai.memory.WalkTarget: a (target, speedModifier,
// closeEnoughDist) triple the MoveToTargetSink consumes.
//
//	[VERIFIED CFR WalkTarget: fields target(PositionTracker), speedModifier(float), closeEnoughDist(int).]
type walkTarget struct {
	target          positionTracker
	speedModifier   float32
	closeEnoughDist int
}

// newWalkTarget ports the WalkTarget(PositionTracker, float, int) ctor.
func newWalkTarget(target positionTracker, speed float32, closeEnough int) walkTarget {
	return walkTarget{target: target, speedModifier: speed, closeEnoughDist: closeEnough}
}

// --- OneShot (the BehaviorBuilder trigger) -------------------------------------------------------

// oneShot ports net.minecraft.world.entity.ai.behavior.OneShot<E> as produced by BehaviorBuilder.create:
// a behavior with an entryCondition (the group of present/absent/registered memory accessors) whose
// trigger runs ONCE per start and returns whether it fired. A OneShot never "keeps running" — its
// canStillUse is the default false, so tryStart -> (trigger true) -> RUNNING for zero ticks -> next
// tickEachRunningBehavior calls tickOrStop -> canStillUse false -> doStop. Net: a per-eligible-tick fire.
//
//	[VERIFIED CFR OneShot/BehaviorBuilder: an OneShot is a Behavior whose start runs the trigger; the
//	 declarative accessors form the entryCondition (present->VALUE_PRESENT, absent->VALUE_ABSENT,
//	 registered->REGISTERED); canStillUse defaults false so it stops the tick after it starts.]
type oneShot struct {
	*behavior
	trigger func(t *TickLoop, e *Entity, timestamp int64) bool
}

// newOneShot builds a OneShot from its entryCondition (the declarative group) and the trigger closure.
func newOneShot(cond []memoryCondition, trigger func(t *TickLoop, e *Entity, timestamp int64) bool) *oneShot {
	return &oneShot{behavior: newBehavior(cond, behaviorDuration, behaviorDuration), trigger: trigger}
}

// tryStart OVERRIDES behavior.tryStart for a OneShot: check the entryCondition group (hasRequiredMemories),
// then run the trigger with the real timestamp; only a true trigger transitions to RUNNING (and rolls the
// duration). This is the BehaviorBuilder OneShot activation shape — a false trigger means the OneShot did
// not run this tick, so it stays STOPPED and is retried next tick.
//
//	[VERIFIED CFR OneShot/BehaviorBuilder: the group must be satisfied AND the Trigger.trigger(level,body,
//	 ts) must return true for RUNNING; the declarative group maps present->VALUE_PRESENT, absent->
//	 VALUE_ABSENT, registered->REGISTERED (the entryCondition).]
func (os *oneShot) tryStart(t *TickLoop, e *Entity, timestamp int64) bool {
	if !os.behavior.hasRequiredMemories(e) {
		return false
	}
	if !os.trigger(t, e, timestamp) {
		return false
	}
	os.behavior.status = statusRunning
	span := os.behavior.maxDuration + 1 - os.behavior.minDuration
	dur := os.behavior.minDuration
	if span > 0 {
		dur += mobRandom(e).nextInt(span)
	}
	os.behavior.endTimestamp = timestamp + int64(dur)
	return true
}

// --- GateBehavior + RunOne (the RUN_ONE / SHUFFLED gate) -----------------------------------------

// gateOrderPolicy ports GateBehavior$OrderPolicy.
type gateOrderPolicy uint8

const (
	orderOrdered  gateOrderPolicy = iota // ORDERED  — leave the list order as-is
	orderShuffled                        // SHUFFLED — weighted-shuffle the list before running
)

// gateRunningPolicy ports GateBehavior$RunningPolicy.
type gateRunningPolicy uint8

const (
	runOne gateRunningPolicy = iota // RUN_ONE — tryStart the first STOPPED behavior that starts
	tryAll                          // TRY_ALL — tryStart every STOPPED behavior
)

// gateWeighted is one (behavior, weight) entry of the ShufflingList a GateBehavior holds.
type gateWeighted struct {
	behavior behaviorControl
	weight   int
}

// gateBehavior ports net.minecraft.world.entity.ai.behavior.GateBehavior<E>. It is itself a
// behaviorControl (nestable inside the Brain's priority table) that owns a weighted list of sub-behaviors
// and starts/ticks them per the order+running policy.
//
//	[VERIFIED CFR GateBehavior: fields entryCondition, exitErasedMemories, orderPolicy, runningPolicy,
//	 behaviors(ShufflingList), status=STOPPED. tryStart: if hasRequiredMemories -> RUNNING; orderPolicy
//	 .apply; runningPolicy.apply. tickOrStop: tick each RUNNING sub; if none RUNNING doStop. doStop:
//	 STOPPED; doStop each RUNNING sub; erase exitErasedMemories.]
type gateBehavior struct {
	entryCondition     []memoryCondition
	exitErasedMemories []memoryKey
	orderPolicy        gateOrderPolicy
	runningPolicy      gateRunningPolicy
	entries            []gateWeighted
	status             behaviorStatus
	shuffleRNG         *entityRandom // the ShufflingList's OWN RandomSource (independent of the mob stream)
}

// newGateBehavior builds a gate. The shuffleRNG is a self-seeded source — vanilla's ShufflingList holds
// `RandomSource.create()` (a fresh, entropy-seeded source SEPARATE from the mob getRandom()), so it never
// perturbs the mob's pinned stream; we seed it deterministically for reproducibility.
//
//	[VERIFIED CFR ShufflingList: private final RandomSource random = RandomSource.create() — a distinct
//	 source, not the mob's getRandom(). shuffle(): each entry randWeight = -pow(random.nextFloat(),
//	 1/weight); sort ascending by randWeight.]
func newGateBehavior(cond []memoryCondition, exitErase []memoryKey, order gateOrderPolicy, running gateRunningPolicy, entries []gateWeighted) *gateBehavior {
	return &gateBehavior{
		entryCondition:     cond,
		exitErasedMemories: exitErase,
		orderPolicy:        order,
		runningPolicy:      running,
		entries:            append([]gateWeighted(nil), entries...),
		status:             statusStopped,
		shuffleRNG:         newEntityRandom(defaultEntityRandomSeed),
	}
}

func (g *gateBehavior) getStatus() behaviorStatus { return g.status }

// requiredMemories ports GateBehavior.getRequiredMemories: entryCondition keys ∪ every sub-behavior's
// required memories.
//
//	[VERIFIED CFR GateBehavior.getRequiredMemories: new HashSet(entryCondition.keySet()); for behavior
//	 addAll(behavior.getRequiredMemories()).]
func (g *gateBehavior) requiredMemories() []memoryKey {
	seen := map[memoryKey]bool{}
	var out []memoryKey
	add := func(k memoryKey) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, c := range g.entryCondition {
		add(c.key)
	}
	for _, e := range g.entries {
		for _, k := range e.behavior.requiredMemories() {
			add(k)
		}
	}
	return out
}

// hasRequiredMemories ports GateBehavior.hasRequiredMemories: the entryCondition group.
func (g *gateBehavior) hasRequiredMemories(e *Entity) bool {
	br := e.brain
	if br == nil {
		return false
	}
	for _, c := range g.entryCondition {
		if !br.checkMemory(c.key, c.status) {
			return false
		}
	}
	return true
}

// applyOrder ports OrderPolicy.apply: SHUFFLED weighted-shuffles the entries in place.
//
//	[VERIFIED CFR OrderPolicy: ORDERED -> no-op; SHUFFLED -> ShufflingList.shuffle (randWeight =
//	 -pow(nextFloat(),1/weight); sort ascending).]
func (g *gateBehavior) applyOrder() {
	if g.orderPolicy != orderShuffled {
		return
	}
	type rw struct {
		idx    int
		weight float64
	}
	weights := make([]rw, len(g.entries))
	for i := range g.entries {
		// draw one nextFloat per entry, in list order (the ShufflingList.forEach draw order)
		f := float64(g.shuffleRNG.nextFloat())
		w := g.entries[i].weight
		if w <= 0 {
			w = 1
		}
		weights[i] = rw{idx: i, weight: -math.Pow(f, 1.0/float64(w))}
	}
	// stable sort ascending by randWeight (Comparator.comparingDouble)
	sort.SliceStable(weights, func(i, j int) bool { return weights[i].weight < weights[j].weight })
	reordered := make([]gateWeighted, len(g.entries))
	for i, x := range weights {
		reordered[i] = g.entries[x.idx]
	}
	g.entries = reordered
}

// tryStart ports GateBehavior.tryStart (FINAL): if hasRequiredMemories -> RUNNING; orderPolicy.apply;
// runningPolicy.apply.
//
//	[VERIFIED CFR GateBehavior.tryStart: if hasRequiredMemories(body) { status=RUNNING;
//	 orderPolicy.apply(behaviors); runningPolicy.apply(behaviors.stream(), level, body, timestamp);
//	 return true; } return false.]
func (g *gateBehavior) tryStart(t *TickLoop, e *Entity, timestamp int64) bool {
	if !g.hasRequiredMemories(e) {
		return false
	}
	g.status = statusRunning
	g.applyOrder()
	switch g.runningPolicy {
	case runOne:
		// RUN_ONE: the first STOPPED sub that tryStarts wins (findFirst short-circuits).
		for i := range g.entries {
			if g.entries[i].behavior.getStatus() == statusStopped {
				if g.entries[i].behavior.tryStart(t, e, timestamp) {
					break
				}
			}
		}
	case tryAll:
		for i := range g.entries {
			if g.entries[i].behavior.getStatus() == statusStopped {
				g.entries[i].behavior.tryStart(t, e, timestamp)
			}
		}
	}
	return true
}

// tickOrStop ports GateBehavior.tickOrStop (FINAL): tick each RUNNING sub; if none RUNNING, doStop.
//
//	[VERIFIED CFR GateBehavior.tickOrStop: behaviors.filter(RUNNING).forEach(tickOrStop); if
//	 noneMatch(RUNNING) doStop(level, body, timestamp).]
func (g *gateBehavior) tickOrStop(t *TickLoop, e *Entity, timestamp int64) {
	for i := range g.entries {
		if g.entries[i].behavior.getStatus() == statusRunning {
			g.entries[i].behavior.tickOrStop(t, e, timestamp)
		}
	}
	anyRunning := false
	for i := range g.entries {
		if g.entries[i].behavior.getStatus() == statusRunning {
			anyRunning = true
			break
		}
	}
	if !anyRunning {
		g.doStop(t, e, timestamp)
	}
}

// doStop ports GateBehavior.doStop (FINAL): STOPPED; doStop each RUNNING sub; erase exitErasedMemories.
//
//	[VERIFIED CFR GateBehavior.doStop: status=STOPPED; behaviors.filter(RUNNING).forEach(doStop);
//	 exitErasedMemories.forEach(brain::eraseMemory).]
func (g *gateBehavior) doStop(t *TickLoop, e *Entity, timestamp int64) {
	g.status = statusStopped
	for i := range g.entries {
		if g.entries[i].behavior.getStatus() == statusRunning {
			g.entries[i].behavior.doStop(t, e, timestamp)
		}
	}
	if e.brain != nil {
		for _, k := range g.exitErasedMemories {
			e.brain.eraseMemory(k)
		}
	}
}

// newRunOne ports net.minecraft.world.entity.ai.behavior.RunOne: a GateBehavior with an empty
// entryCondition, empty exitErasedMemories, SHUFFLED order, RUN_ONE running.
//
//	[VERIFIED CFR RunOne: super(ImmutableMap.of(), ImmutableSet.of(), OrderPolicy.SHUFFLED,
//	 RunningPolicy.RUN_ONE, weightedBehaviors).]
func newRunOne(entries []gateWeighted) *gateBehavior {
	return newGateBehavior(nil, nil, orderShuffled, runOne, entries)
}

// --- Sensor / SensorType -------------------------------------------------------------------------

// sensorType ports net.minecraft.world.entity.ai.sensing.SensorType, keyed by its registry index
// (data/registryid/sensortype.go order). A comparable map key (the Map<SensorType,Sensor> key).
//
//	[VERIFIED data/registryid/sensortype.go — indices match minecraft:<id> order in registries.json.]
type sensorType int

const (
	sensorNearestLivingEntities sensorType = 2  // minecraft:nearest_living_entities
	sensorNearestPlayers        sensorType = 3  // minecraft:nearest_players
	sensorHurtBy                sensorType = 5  // minecraft:hurt_by
	sensorNearestAdultAnyType   sensorType = 15 // minecraft:nearest_adult_any_type
	sensorFoodTemptations       sensorType = 17 // minecraft:food_temptations
)

// sensor ports net.minecraft.world.entity.ai.sensing.Sensor<E>.tick(ServerLevel, E) — a per-tick memory
// writer. Vanilla's Sensor has an internal scanRate cooldown (randomlyDelayStart + doTick every scanRate
// ticks); the HappyGhast sensors here scan every tick (scanRate reduced to 1 — a cited perf-neutral
// simplification that only makes the memories fresher, never changes observable steering; the scanRate
// jitter is deferred and cited on newBrainProvider).
type sensor func(t *TickLoop, e *Entity, gameTime int64)
