package server

// brain.go — the core of the ported net.minecraft.world.entity.ai.Brain<E> subsystem
// (temp/cache/26.2-inner.jar, javap -c -p / CFR this task). A FAITHFUL port of the Brain framework
// Mojang uses for its behavior-driven mobs, structured so future brain mobs slot in beside the
// HappyGhast baby wired here.
//
// PORTED 1:1 (javap/CFR-cited inline): the four maps (memories, sensors, availableBehaviorsByPriority,
// activityRequirements) + activityMemoriesToEraseWhenStopped/coreActivities/activeActivities/
// defaultActivity(=IDLE); tick = forgetOutdatedMemories -> tickSensors -> startEachNonRunningBehavior ->
// tickEachRunningBehavior (VERIFIED bytecode order); checkMemory/hasMemoryValue/setMemory*/eraseMemory/
// getMemory; addActivity/setActiveActivity/setActiveActivityToFirstValid/activityRequirementsAreMet/
// eraseMemoriesForOtherActivitesThan/setCoreActivities/useDefaultActivity/getRunningBehaviors.
//
// THE BODY (E extends LivingEntity) is *Entity; ServerLevel is threaded as *TickLoop. Cite: Brain.

import (
	"math"
	"sort"
)

// activity is net.minecraft.world.entity.schedule.Activity, reduced to the fixed set the ported mobs
// use. A plain comparable value (map key), like the enum.
type activity uint8

const (
	activityCore  activity = iota // Activity.CORE
	activityIdle                  // Activity.IDLE  — Brain.defaultActivity = IDLE
	activityPanic                 // Activity.PANIC
	activityWork
	activityRest
	activityMeet
	activityFight
	activityAvoid
)

// memoryCondition is one (MemoryModuleType, MemoryStatus) pair — the unit of an Activity requirement set
// and a Behavior entryCondition. Ports Pair<MemoryModuleType,MemoryStatus>.
type memoryCondition struct {
	key    memoryKey
	status memoryStatus
}

// brainPriorityEntry is one (priority, activity, behavior) row of availableBehaviorsByPriority, flattened
// into a slice sorted by (priority asc, insertion order) so the start/tick walks visit behaviors in the
// EXACT vanilla order (priority buckets, then the activity's insertion-ordered LinkedHashSet).
type brainPriorityEntry struct {
	priority int
	act      activity
	seq      int
	behavior behaviorControl
}

// brain ports net.minecraft.world.entity.ai.Brain<E extends LivingEntity>. Single-owner tick state
// (TICK-05): built once at spawn, ticked only on the tick goroutine.
type brain struct {
	memories map[memoryKey]*memorySlot

	sensors     map[sensorType]sensor
	sensorOrder []sensorType

	behaviors []brainPriorityEntry
	nextSeq   int

	activityRequirements               map[activity][]memoryCondition
	activityMemoriesToEraseWhenStopped map[activity][]memoryKey

	coreActivities   map[activity]bool
	activeActivities map[activity]bool
	defaultActivity  activity

	// schedule is Brain.schedule (net.minecraft.world.attribute.EnvironmentAttribute<Activity>): the
	// day-cycle timeline the villager samples each schedule tick. nil for a mob with no schedule (the
	// HappyGhast baby), so updateActivityFromSchedule then samples IDLE. VERIFIED Brain.schedule field.
	schedule *schedule

	// lastScheduleUpdate is Brain.lastScheduleUpdate: the day-time of the last updateActivityFromSchedule
	// re-sample; the 20-tick SCHEDULE_UPDATE_DELAY guard reads it. VERIFIED Brain.lastScheduleUpdate.
	lastScheduleUpdate int64
}

// newBrain constructs an empty brain with the constructor-tail defaults: coreActivities={CORE},
// defaultActivity=IDLE. The provider (brainProvider.makeBrain) registers memories/sensors/activities and
// then calls setCoreActivities({CORE}) + useDefaultActivity(), matching the Brain constructor body.
//
//	[VERIFIED CFR Brain.<init>: register memoryTypes; per sensorType create+randomlyDelayStart+put +
//	 register requires(); per ActivityData addActivity; restore packed memories; setCoreActivities({CORE});
//	 useDefaultActivity(). defaultActivity field init = Activity.IDLE.]
func newBrain() *brain {
	return &brain{
		memories:                           map[memoryKey]*memorySlot{},
		sensors:                            map[sensorType]sensor{},
		activityRequirements:               map[activity][]memoryCondition{},
		activityMemoriesToEraseWhenStopped: map[activity][]memoryKey{},
		coreActivities:                     map[activity]bool{},
		activeActivities:                   map[activity]bool{},
		defaultActivity:                    activityIdle,
	}
}

// registerMemory ports Brain.registerMemory: memories.putIfAbsent(type, MemorySlot.create()).
//
//	[VERIFIED CFR Brain.registerMemory: this.memories.putIfAbsent(memoryType, MemorySlot.create()).]
func (b *brain) registerMemory(k memoryKey) {
	if _, ok := b.memories[k]; !ok {
		b.memories[k] = newMemorySlot()
	}
}

// getMemorySlotIfPresent ports Brain.getMemorySlotIfPresent: memories.get(type).
func (b *brain) getMemorySlotIfPresent(k memoryKey) *memorySlot { return b.memories[k] }

// hasMemoryValue ports Brain.hasMemoryValue: checkMemory(type, VALUE_PRESENT).
//
//	[VERIFIED CFR Brain.hasMemoryValue: return checkMemory(type, MemoryStatus.VALUE_PRESENT).]
func (b *brain) hasMemoryValue(k memoryKey) bool { return b.checkMemory(k, memValuePresent) }

// checkMemory ports Brain.checkMemory: unregistered -> false; REGISTERED -> true; VALUE_PRESENT iff the
// slot holds a value; VALUE_ABSENT iff it does not.
//
//	[VERIFIED CFR Brain.checkMemory: slot=getMemorySlotIfPresent(type); if slot==null return false;
//	 return status==REGISTERED || (status==VALUE_PRESENT && slot.hasValue()) ||
//	        (status==VALUE_ABSENT && !slot.hasValue()).]
func (b *brain) checkMemory(k memoryKey, status memoryStatus) bool {
	s := b.getMemorySlotIfPresent(k)
	if s == nil {
		return false
	}
	return status == memRegistered ||
		(status == memValuePresent && s.hasValue()) ||
		(status == memValueAbsent && !s.hasValue())
}

// getMemory ports Brain.getMemory: the slot value, or (nil,false) when unregistered/empty.
//
//	[VERIFIED CFR Brain.getMemory: slot=getMemorySlotIfPresent(type); if slot==null return empty;
//	 return Optional.ofNullable(slot.value()).]
func (b *brain) getMemory(k memoryKey) (any, bool) {
	s := b.getMemorySlotIfPresent(k)
	if s == nil || !s.hasValue() {
		return nil, false
	}
	return s.value, true
}

// setMemory ports Brain.setMemory(type,U): setMemoryWithExpiry(type, value, NEVER_EXPIRE).
//
//	[VERIFIED CFR Brain.setMemory(type,U): setMemoryInternal(type, value, NEVER_EXPIRE).]
func (b *brain) setMemory(k memoryKey, v any) { b.setMemoryWithExpiry(k, v, memNeverExpire) }

// setMemoryWithExpiry ports Brain.setMemoryWithExpiry: write into the registered slot with a ttl; a
// non-registered slot is skipped (vanilla getMemorySlotIfPresent==null path).
//
//	[VERIFIED CFR Brain.setMemoryWithExpiry: setMemoryInternal(type, value, timeToLive).]
func (b *brain) setMemoryWithExpiry(k memoryKey, v any, ttl int64) {
	if s := b.getMemorySlotIfPresent(k); s != nil {
		s.set(v, ttl)
	}
}

// eraseMemory ports Brain.eraseMemory: clear the slot.
//
//	[VERIFIED CFR Brain.eraseMemory: setMemory(type, Optional.empty()) -> slot.clear().]
func (b *brain) eraseMemory(k memoryKey) {
	if s := b.getMemorySlotIfPresent(k); s != nil {
		s.clear()
	}
}

// setMemoryOrErase ports the setOrErase accessor (present -> setMemory, absent -> eraseMemory), used by
// RandomStroll walkTarget.setOrErase.
//
//	[VERIFIED CFR Brain.setMemory(type,Optional): present ? setMemoryInternal : erase.]
func (b *brain) setMemoryOrErase(k memoryKey, v any, present bool) {
	if present {
		b.setMemory(k, v)
	} else {
		b.eraseMemory(k)
	}
}

// setCoreActivities ports Brain.setCoreActivities(Set<Activity>).
func (b *brain) setCoreActivities(acts ...activity) {
	b.coreActivities = map[activity]bool{}
	for _, a := range acts {
		b.coreActivities[a] = true
	}
}

// setDefaultActivity ports Brain.setDefaultActivity(Activity).
func (b *brain) setDefaultActivity(a activity) { b.defaultActivity = a }

// useDefaultActivity ports Brain.useDefaultActivity: setActiveActivity(defaultActivity).
func (b *brain) useDefaultActivity() { b.setActiveActivity(b.defaultActivity) }

// isActive ports Brain.isActive: activeActivities.contains(activity).
func (b *brain) isActive(a activity) bool { return b.activeActivities[a] }

// addActivity ports Brain.addActivity: record requirement/erase sets, then per (priority, behavior)
// register the behavior required memories and file it under (priority -> activity -> insertion-ordered
// set). We keep a flattened slice sorted by (priority asc, insertion order).
//
//	[VERIFIED CFR Brain.addActivity: activityRequirements.put(activity, conditions);
//	 if !memoriesToEraseWhenStopped.isEmpty() activityMemoriesToEraseWhenStopped.put(activity, ...);
//	 for each pair: for each requiredMemory registerMemory; availableBehaviorsByPriority
//	 .computeIfAbsent(prio).computeIfAbsent(activity, LinkedHashSet).add(behavior).]
func (b *brain) addActivity(a activity, pairs []prioritizedBehavior, conditions []memoryCondition, erase []memoryKey) {
	b.activityRequirements[a] = conditions
	if len(erase) > 0 {
		b.activityMemoriesToEraseWhenStopped[a] = erase
	}
	for _, p := range pairs {
		for _, req := range p.behavior.requiredMemories() {
			b.registerMemory(req)
		}
		b.behaviors = append(b.behaviors, brainPriorityEntry{
			priority: p.priority,
			act:      a,
			seq:      b.nextSeq,
			behavior: p.behavior,
		})
		b.nextSeq++
	}
	sort.SliceStable(b.behaviors, func(i, j int) bool {
		if b.behaviors[i].priority != b.behaviors[j].priority {
			return b.behaviors[i].priority < b.behaviors[j].priority
		}
		return b.behaviors[i].seq < b.behaviors[j].seq
	})
}

// activityRequirementsAreMet ports Brain.activityRequirementsAreMet: false if unrecorded; else every
// (memory,status) condition must checkMemory true.
//
//	[VERIFIED CFR Brain.activityRequirementsAreMet: if !activityRequirements.containsKey(activity)
//	 return false; for each (type,status) if !checkMemory(type,status) return false; return true.]
func (b *brain) activityRequirementsAreMet(a activity) bool {
	conds, ok := b.activityRequirements[a]
	if !ok {
		return false
	}
	for _, c := range conds {
		if !b.checkMemory(c.key, c.status) {
			return false
		}
	}
	return true
}

// setActiveActivity ports Brain.setActiveActivity: no-op if already active; else erase stale-activity
// memories, then rebuild activeActivities = coreActivities ∪ {activity}.
//
//	[VERIFIED CFR Brain.setActiveActivity: if isActive(activity) return;
//	 eraseMemoriesForOtherActivitesThan(activity); activeActivities.clear();
//	 activeActivities.addAll(coreActivities); activeActivities.add(activity).]
func (b *brain) setActiveActivity(a activity) {
	if b.isActive(a) {
		return
	}
	b.eraseMemoriesForOtherActivitesThan(a)
	b.activeActivities = map[activity]bool{}
	for c := range b.coreActivities {
		b.activeActivities[c] = true
	}
	b.activeActivities[a] = true
}

// eraseMemoriesForOtherActivitesThan ports Brain.eraseMemoriesForOtherActivitesThan: for each active
// activity != the incoming one, erase its memoriesToEraseWhenStopped.
//
//	[VERIFIED CFR Brain.eraseMemoriesForOtherActivitesThan: for oldActivity in activeActivities:
//	 if oldActivity==activity continue; types=activityMemoriesToEraseWhenStopped.get(oldActivity);
//	 if types==null continue; for each type eraseMemory(type).]
func (b *brain) eraseMemoriesForOtherActivitesThan(a activity) {
	for old := range b.activeActivities {
		if old == a {
			continue
		}
		types, ok := b.activityMemoriesToEraseWhenStopped[old]
		if !ok {
			continue
		}
		for _, k := range types {
			b.eraseMemory(k)
		}
	}
}

// setActiveActivityToFirstValid ports Brain.setActiveActivityToFirstValid(List<Activity>): activate the
// first activity whose requirements are met.
//
//	[VERIFIED CFR Brain.setActiveActivityToFirstValid: for activity in activities:
//	 if !activityRequirementsAreMet(activity) continue; setActiveActivity(activity); break.]
func (b *brain) setActiveActivityToFirstValid(acts ...activity) {
	for _, a := range acts {
		if !b.activityRequirementsAreMet(a) {
			continue
		}
		b.setActiveActivity(a)
		break
	}
}

// getRunningBehaviors ports Brain.getRunningBehaviors: the behaviors (in priority/activity order) with
// status RUNNING.
//
//	[VERIFIED CFR Brain.getRunningBehaviors: iterate availableBehaviorsByPriority.values() ->
//	 behavioursByActivities.values() -> behaviors; add those with getStatus()==RUNNING.]
func (b *brain) getRunningBehaviors() []behaviorControl {
	var out []behaviorControl
	for i := range b.behaviors {
		if b.behaviors[i].behavior.getStatus() == statusRunning {
			out = append(out, b.behaviors[i].behavior)
		}
	}
	return out
}

// tick ports Brain.tick(ServerLevel, E): forgetOutdatedMemories -> tickSensors ->
// startEachNonRunningBehavior -> tickEachRunningBehavior. THE ORDER IS BYTECODE-VERIFIED: forget FIRST.
//
//	[VERIFIED javap Brain.tick bytecode: invokevirtual forgetOutdatedMemories; tickSensors;
//	 startEachNonRunningBehavior; tickEachRunningBehavior; return.]
func (b *brain) tick(t *TickLoop, e *Entity, gameTime int64) {
	b.forgetOutdatedMemories()
	b.tickSensors(t, e, gameTime)
	b.startEachNonRunningBehavior(t, e, gameTime)
	b.tickEachRunningBehavior(t, e, gameTime)
}

// forgetOutdatedMemories ports Brain.forgetOutdatedMemories: memories.values().forEach(MemorySlot::tick).
//
//	[VERIFIED javap Brain.forgetOutdatedMemories: memories.values().forEach(MemorySlot::tick).]
func (b *brain) forgetOutdatedMemories() {
	for _, s := range b.memories {
		s.tick()
	}
}

// tickSensors ports Brain.tickSensors: for each sensor in sensors.values() sensor.tick(level, body). We
// walk sensorOrder (the provider registration order) for a deterministic insertion-ordered pass.
//
//	[VERIFIED javap Brain.tickSensors: for sensor in sensors.values() sensor.tick(level, entity).]
func (b *brain) tickSensors(t *TickLoop, e *Entity, gameTime int64) {
	for _, st := range b.sensorOrder {
		if s := b.sensors[st]; s != nil {
			s(t, e, gameTime)
		}
	}
}

// startEachNonRunningBehavior ports Brain.startEachNonRunningBehavior: over each priority bucket, for each
// ACTIVE activity behavior set, tryStart every STOPPED behavior. The flattened+sorted slice visits
// (priority asc, insertion order); we gate on the behavior activity being active.
//
//	[VERIFIED javap Brain.startEachNonRunningBehavior: for map in availableBehaviorsByPriority.values():
//	 for (activity,behaviors) in map.entrySet(): if activeActivities.contains(activity): for behavior:
//	 if behavior.getStatus()==STOPPED behavior.tryStart(level, body, gameTime).]
func (b *brain) startEachNonRunningBehavior(t *TickLoop, e *Entity, gameTime int64) {
	for i := range b.behaviors {
		ent := &b.behaviors[i]
		if !b.activeActivities[ent.act] {
			continue
		}
		if ent.behavior.getStatus() == statusStopped {
			ent.behavior.tryStart(t, e, gameTime)
		}
	}
}

// tickEachRunningBehavior ports Brain.tickEachRunningBehavior: for each running behavior, tickOrStop.
//
//	[VERIFIED javap Brain.tickEachRunningBehavior: for behavior in getRunningBehaviors():
//	 behavior.tickOrStop(level, body, gameTime).]
func (b *brain) tickEachRunningBehavior(t *TickLoop, e *Entity, gameTime int64) {
	for _, bc := range b.getRunningBehaviors() {
		bc.tickOrStop(t, e, gameTime)
	}
}

// --- typed memory helpers (the Go stand-in for vanilla generic getMemory<U>) ---------------------

// getMemoryInt reads an int-valued memory (e.g. TEMPTATION_COOLDOWN_TICKS).
func (b *brain) getMemoryInt(k memoryKey) (int, bool) {
	v, ok := b.getMemory(k)
	if !ok {
		return 0, false
	}
	n, ok := v.(int)
	return n, ok
}

// getMemoryTarget reads a positionTracker-valued memory (LOOK_TARGET).
func (b *brain) getMemoryTarget(k memoryKey) (positionTracker, bool) {
	v, ok := b.getMemory(k)
	if !ok {
		return positionTracker{}, false
	}
	pt, ok := v.(positionTracker)
	return pt, ok
}

// getMemoryWalkTarget reads a walkTarget-valued memory (WALK_TARGET).
func (b *brain) getMemoryWalkTarget(k memoryKey) (walkTarget, bool) {
	v, ok := b.getMemory(k)
	if !ok {
		return walkTarget{}, false
	}
	wt, ok := v.(walkTarget)
	return wt, ok
}

// getMemoryEntityID reads a thin-entity-id memory (NEAREST_VISIBLE_PLAYER/_ADULT/TEMPTING_PLAYER; stored
// as int32 per the Folia rule — never a live pointer).
func (b *brain) getMemoryEntityID(k memoryKey) (int32, bool) {
	v, ok := b.getMemory(k)
	if !ok {
		return 0, false
	}
	id, ok := v.(int32)
	return id, ok
}

// distManhattanBlocks ports BlockPos.distManhattan on the floored block positions of two world points —
// the MoveToTargetSink.reachedTarget metric.
func distManhattanBlocks(ax, ay, az, bx, by, bz float64) int {
	dx := int(math.Abs(math.Floor(ax) - math.Floor(bx)))
	dy := int(math.Abs(math.Floor(ay) - math.Floor(by)))
	dz := int(math.Abs(math.Floor(az) - math.Floor(bz)))
	return dx + dy + dz
}

// --- brainProvider (net.minecraft.world.entity.ai.Brain$Provider) -------------------------------

// activityData ports net.minecraft.world.entity.ai.ActivityData<E>: the (activity, prioritized behaviors,
// conditions, memoriesToEraseWhenStopped) tuple addActivity consumes.
//
//	[VERIFIED CFR ActivityData: record (activityType, behaviorPriorityPairs, conditions,
//	 memoriesToEraseWhenStopped).]
type activityData struct {
	act        activity
	behaviors  []prioritizedBehavior
	conditions []memoryCondition
	erase      []memoryKey
}

// activityDataCreate ports ActivityData.create(activity, priorityOfFirstBehavior, behaviorList): assign
// ascending priorities starting at priorityOfFirstBehavior (createPriorityPairs), empty conditions/erase.
//
//	[VERIFIED CFR ActivityData.create(activity,int,list): createPriorityPairs(prio, list); conditions
//	 ImmutableSet.of(); memoriesToEraseWhenStopped newHashSet(). createPriorityPairs: nextPrio=prio;
//	 for behavior add Pair.of(nextPrio++, behavior).]
func activityDataCreate(act activity, priorityOfFirst int, behaviors []behaviorControl) activityData {
	pairs := make([]prioritizedBehavior, len(behaviors))
	prio := priorityOfFirst
	for i, b := range behaviors {
		pairs[i] = prioritizedBehavior{priority: prio, behavior: b}
		prio++
	}
	return activityData{act: act, behaviors: pairs}
}

// activityDataCreatePairs ports ActivityData.create(activity, behaviorPriorityPairs[, conditions[, erase]]):
// the explicit-priority form (the IDLE/PANIC activities use Pair.of(priority, behavior) directly).
//
//	[VERIFIED CFR ActivityData.create(activity, pairs, conditions, erase): new ActivityData(...).]
func activityDataCreatePairs(act activity, pairs []prioritizedBehavior, conditions []memoryCondition, erase []memoryKey) activityData {
	return activityData{act: act, behaviors: pairs, conditions: conditions, erase: erase}
}

// brainProvider ports net.minecraft.world.entity.ai.Brain$Provider<E>: the recipe (sensors + activities)
// that makeBrain instantiates into a fresh brain per entity. Built once per mob type, reused per spawn.
//
//	[VERIFIED CFR Brain.Provider: fields memoryTypes, sensorTypes, activities(ActivitySupplier);
//	 makeBrain(body, packed): new Brain(memoryTypes, sensorTypes, activities.createActivities(body),
//	 packed.memories, body.getRandom()).]
type brainProvider struct {
	sensorTypes  []sensorType
	sensorFns    map[sensorType]sensor
	sensorMems   map[sensorType][]memoryKey
	memoryTypes  []memoryKey
	activityList []activityData
}

// newBrainProvider builds an empty provider.
func newBrainProvider() *brainProvider {
	return &brainProvider{sensorFns: map[sensorType]sensor{}, sensorMems: map[sensorType][]memoryKey{}}
}

// addSensor registers a sensor type + its tick fn + the memories it requires (Sensor.requires()). A type
// is registered once (the Map<SensorType,Sensor> de-dup), keeping the provider's List order.
func (p *brainProvider) addSensor(st sensorType, fn sensor, requires ...memoryKey) {
	if _, ok := p.sensorFns[st]; !ok {
		p.sensorTypes = append(p.sensorTypes, st)
	}
	p.sensorFns[st] = fn
	p.sensorMems[st] = requires
}

// addMemory registers an extra memory type (the deprecated Brain.provider(memoryTypes, ...) form).
func (p *brainProvider) addMemory(k memoryKey) { p.memoryTypes = append(p.memoryTypes, k) }

// addActivity appends an ActivityData to the provider's activity list.
func (p *brainProvider) addActivity(a activityData) { p.activityList = append(p.activityList, a) }

// makeBrain ports Brain.Provider.makeBrain + the Brain constructor body: register memoryTypes; per sensor
// create+register its required memories+put; per activity addActivity; then the constructor tail
// setCoreActivities({CORE}) + useDefaultActivity(). (No packed memories to restore for a fresh spawn.)
//
//	[VERIFIED CFR Brain.<init>: register memoryTypes; per sensorType create+randomlyDelayStart+put +
//	 register requires(); per ActivityData addActivity; restore packed; setCoreActivities({CORE});
//	 useDefaultActivity().]
func (p *brainProvider) makeBrain(e *Entity) *brain {
	b := newBrain()
	for _, k := range p.memoryTypes {
		b.registerMemory(k)
	}
	for _, st := range p.sensorTypes {
		b.sensors[st] = p.sensorFns[st]
		b.sensorOrder = append(b.sensorOrder, st)
		for _, k := range p.sensorMems[st] {
			b.registerMemory(k)
		}
	}
	for _, a := range p.activityList {
		b.addActivity(a.act, a.behaviors, a.conditions, a.erase)
	}
	b.setCoreActivities(activityCore)
	b.useDefaultActivity()
	return b
}
