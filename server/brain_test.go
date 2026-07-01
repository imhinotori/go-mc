package server

// brain_test.go — deterministic exercises of the ported Brain subsystem (brain.go / brain_behavior.go /
// brain_memory.go) and the HappyGhast baby wiring (brain_happy_ghast.go). Pins the tick pipeline ORDER,
// the memory slot expiry, the activity requirement gating, the Behavior lifecycle, and the baby-only
// brain drive. Fully deterministic (fixed-seed mob RNG; no wall clock).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestMemorySlotExpiry pins MemorySlot.tick: a permanent slot never counts down; an expiring slot counts
// to 0 then clears; an empty slot is inert.
func TestMemorySlotExpiry(t *testing.T) {
	s := newMemorySlot()
	if s.hasValue() || s.canExpire() {
		t.Fatalf("fresh slot: hasValue=%v canExpire=%v, want false,false", s.hasValue(), s.canExpire())
	}
	s.set("v", memNeverExpire)
	s.tick()
	if !s.hasValue() {
		t.Fatalf("permanent slot cleared on tick")
	}
	s.set("v", 2)
	s.tick() // ttl 2 -> 1
	if !s.hasValue() {
		t.Fatalf("expiring slot cleared early (ttl was 2)")
	}
	s.tick() // ttl 1 -> 0
	if !s.hasValue() {
		t.Fatalf("expiring slot cleared at ttl 1 (should reach 0 first)")
	}
	s.tick() // ttl 0 -> expired -> clear
	if s.hasValue() {
		t.Fatalf("expired slot not cleared (ttl reached 0)")
	}
}

// TestCheckMemoryStatuses pins Brain.checkMemory across REGISTERED/VALUE_PRESENT/VALUE_ABSENT for an
// unregistered, registered-empty, and registered-valued slot.
func TestCheckMemoryStatuses(t *testing.T) {
	b := newBrain()
	if b.checkMemory(memWalkTarget, memRegistered) || b.checkMemory(memWalkTarget, memValuePresent) || b.checkMemory(memWalkTarget, memValueAbsent) {
		t.Fatalf("unregistered slot passed a checkMemory")
	}
	b.registerMemory(memWalkTarget)
	if !b.checkMemory(memWalkTarget, memRegistered) {
		t.Fatalf("registered slot failed REGISTERED")
	}
	if b.checkMemory(memWalkTarget, memValuePresent) {
		t.Fatalf("empty slot passed VALUE_PRESENT")
	}
	if !b.checkMemory(memWalkTarget, memValueAbsent) {
		t.Fatalf("empty slot failed VALUE_ABSENT")
	}
	b.setMemory(memWalkTarget, 42)
	if !b.checkMemory(memWalkTarget, memValuePresent) {
		t.Fatalf("valued slot failed VALUE_PRESENT")
	}
	if b.checkMemory(memWalkTarget, memValueAbsent) {
		t.Fatalf("valued slot passed VALUE_ABSENT")
	}
}

// TestActivityRequirementsGate pins setActiveActivityToFirstValid: PANIC requires IS_PANICKING present, so
// with it absent IDLE wins; with it present PANIC wins. Mirrors HappyGhastAi.updateActivity([PANIC,IDLE]).
func TestActivityRequirementsGate(t *testing.T) {
	b := newBrain()
	b.registerMemory(memIsPanicking)
	b.activityRequirements[activityIdle] = nil
	b.activityRequirements[activityPanic] = []memoryCondition{{key: memIsPanicking, status: memValuePresent}}
	b.setCoreActivities(activityCore)

	b.setActiveActivityToFirstValid(activityPanic, activityIdle)
	if b.isActive(activityPanic) || !b.isActive(activityIdle) {
		t.Fatalf("IS_PANICKING absent: want IDLE active, got panic=%v idle=%v", b.isActive(activityPanic), b.isActive(activityIdle))
	}
	if !b.isActive(activityCore) {
		t.Fatalf("CORE must always be active (coreActivities union)")
	}
	b.setMemory(memIsPanicking, true)
	b.setActiveActivityToFirstValid(activityPanic, activityIdle)
	if !b.isActive(activityPanic) || b.isActive(activityIdle) {
		t.Fatalf("IS_PANICKING present: want PANIC active, got panic=%v idle=%v", b.isActive(activityPanic), b.isActive(activityIdle))
	}
}

// TestBehaviorLifecycle pins the Behavior FINAL tryStart/tickOrStop/doStop: start (RUNNING) with satisfied
// memories, tick while canStillUse, stop (STOPPED) when canStillUse goes false.
func TestBehaviorLifecycle(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.brain = newBrain()
	e.brain.registerMemory(memWalkTarget)
	e.brain.setMemory(memWalkTarget, 1)

	var started, ticked, stopped int
	alive := true
	b := newBehavior([]memoryCondition{{key: memWalkTarget, status: memValuePresent}}, behaviorDuration, behaviorDuration)
	b.checkExtraStart = func(_ *TickLoop, _ *Entity) bool { return true }
	b.canStillUse = func(_ *TickLoop, _ *Entity, _ int64) bool { return alive }
	b.start = func(_ *TickLoop, _ *Entity, _ int64) { started++ }
	b.tick = func(_ *TickLoop, _ *Entity, _ int64) { ticked++ }
	b.stop = func(_ *TickLoop, _ *Entity, _ int64) { stopped++ }

	if !b.tryStart(loop, e, 0) {
		t.Fatalf("tryStart returned false with satisfied memories + checkExtraStart true")
	}
	if b.getStatus() != statusRunning || started != 1 {
		t.Fatalf("after tryStart: status=%v started=%d, want RUNNING,1", b.getStatus(), started)
	}
	b.tickOrStop(loop, e, 1)
	if ticked != 1 || b.getStatus() != statusRunning {
		t.Fatalf("first tickOrStop: ticked=%d status=%v, want 1,RUNNING", ticked, b.getStatus())
	}
	alive = false
	b.tickOrStop(loop, e, 2)
	if stopped != 1 || b.getStatus() != statusStopped {
		t.Fatalf("second tickOrStop: stopped=%d status=%v, want 1,STOPPED", stopped, b.getStatus())
	}
}

// TestBehaviorTimedOut pins the default timedOut (ts > endTimestamp): a min==max==0 behavior started at
// ts=0 has endTimestamp 0, so tickOrStop at ts=1 times out and stops immediately (never ticks).
func TestBehaviorTimedOut(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.brain = newBrain()

	ticked := 0
	b := newBehavior(nil, 0, 0)
	b.checkExtraStart = func(_ *TickLoop, _ *Entity) bool { return true }
	b.canStillUse = func(_ *TickLoop, _ *Entity, _ int64) bool { return true }
	b.tick = func(_ *TickLoop, _ *Entity, _ int64) { ticked++ }
	b.tryStart(loop, e, 0)
	b.tickOrStop(loop, e, 1)
	if ticked != 0 || b.getStatus() != statusStopped {
		t.Fatalf("timed-out behavior: ticked=%d status=%v, want 0,STOPPED", ticked, b.getStatus())
	}
}

// TestBrainTickOrder pins the pipeline ORDER: a sensor runs before a behavior start within one tick.
func TestBrainTickOrder(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)

	var order []string
	p := newBrainProvider()
	p.addSensor(sensorNearestPlayers, func(_ *TickLoop, _ *Entity, _ int64) { order = append(order, "sensor") })
	os := newOneShot(nil, func(_ *TickLoop, _ *Entity, _ int64) bool { order = append(order, "start"); return false })
	p.addActivity(activityDataCreate(activityCore, 0, []behaviorControl{os}))
	e.brain = p.makeBrain(e)

	e.brain.tick(loop, e, 0)
	if len(order) < 2 || order[0] != "sensor" || order[len(order)-1] != "start" {
		t.Fatalf("tick order = %v, want sensor before behavior start", order)
	}
}

// TestForgetRunsBeforeSensors pins forgetOutdatedMemories running as the pipeline entry: a slot with ttl 1
// survives one tick (1 -> 0) then is cleared the next (0 -> expired).
func TestForgetRunsBeforeSensors(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	p := newBrainProvider()
	p.addSensor(sensorNearestPlayers, func(_ *TickLoop, _ *Entity, _ int64) {})
	e.brain = p.makeBrain(e)
	e.brain.registerMemory(memWalkTarget)
	e.brain.setMemoryWithExpiry(memWalkTarget, 7, 1)
	e.brain.tick(loop, e, 0)
	if !e.brain.hasMemoryValue(memWalkTarget) {
		t.Fatalf("slot cleared too early (ttl 1 -> 0 keeps the value)")
	}
	e.brain.tick(loop, e, 1)
	if e.brain.hasMemoryValue(memWalkTarget) {
		t.Fatalf("slot not forgotten after ttl reached 0")
	}
}

// TestHappyGhastBrainProviderShape pins the ported HappyGhast BRAIN_PROVIDER: five sensors, CORE/IDLE/PANIC
// activities, and a fresh state of {CORE, IDLE}; updateActivity flips to PANIC only when IS_PANICKING is set.
func TestHappyGhastBrainProviderShape(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.typ = entity.HappyGhast.ID
	attachHappyGhastBrain(e)
	b := e.brain
	if b == nil {
		t.Fatalf("attachHappyGhastBrain left brain nil")
	}
	if len(b.sensorOrder) != 5 {
		t.Fatalf("sensor count = %d, want 5", len(b.sensorOrder))
	}
	if !b.isActive(activityCore) || !b.isActive(activityIdle) {
		t.Fatalf("fresh brain: want {CORE, IDLE}, got core=%v idle=%v panic=%v", b.isActive(activityCore), b.isActive(activityIdle), b.isActive(activityPanic))
	}
	happyGhastUpdateActivity(e)
	if b.isActive(activityPanic) {
		t.Fatalf("PANIC active with IS_PANICKING absent")
	}
	b.setMemory(memIsPanicking, true)
	happyGhastUpdateActivity(e)
	if !b.isActive(activityPanic) || b.isActive(activityIdle) {
		t.Fatalf("IS_PANICKING present: want PANIC (not IDLE), got panic=%v idle=%v", b.isActive(activityPanic), b.isActive(activityIdle))
	}
}

// TestHappyGhastBabyOnlyBrain pins the baby gate: happyGhastBabyBrainTick ticks the brain ONLY for a baby.
func TestHappyGhastBabyOnlyBrain(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.typ = entity.HappyGhast.ID
	ran := 0
	p := newBrainProvider()
	p.addSensor(sensorNearestPlayers, func(_ *TickLoop, _ *Entity, _ int64) { ran++ })
	p.addActivity(happyGhastInitCoreActivity())
	p.addActivity(happyGhastInitIdleActivity())
	p.addActivity(happyGhastInitPanicActivity())
	e.brain = p.makeBrain(e)

	e.breedAge = 0
	loop.happyGhastBabyBrainTick(e)
	if ran != 0 {
		t.Fatalf("adult happy ghast: brain ticked (%d), want 0", ran)
	}
	e.breedAge = -1
	loop.happyGhastBabyBrainTick(e)
	if ran != 1 {
		t.Fatalf("baby happy ghast: sensor ran %d times, want 1", ran)
	}
}

// TestBabyGhastBrainStrollNoPanic pins the full baby tick path: 5 ticks of a world-less baby brain must not
// panic and must keep the brain in {CORE, IDLE}.
func TestBabyGhastBrainStrollNoPanic(t *testing.T) {
	loop, _ := newBlockLoop()
	e := newDamageMob(loop, 1, 20.0)
	e.typ = entity.HappyGhast.ID
	e.breedAge = -1
	attachHappyGhastBrain(e)
	for i := 0; i < 5; i++ {
		loop.happyGhastBabyBrainTick(e)
	}
	if !e.brain.isActive(activityCore) || !e.brain.isActive(activityIdle) {
		t.Fatalf("after 5 baby ticks: want {CORE, IDLE}, got core=%v idle=%v panic=%v", e.brain.isActive(activityCore), e.brain.isActive(activityIdle), e.brain.isActive(activityPanic))
	}
}
