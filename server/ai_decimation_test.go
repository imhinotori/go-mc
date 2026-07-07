package server

// ai_decimation_test.go — C-1 regression: the Mob.serverAiStep AI tick DECIMATION.
//
// Vanilla net.minecraft.world.entity.Mob.serverAiStep does NOT re-evaluate the goal/target
// selectors every tick. It splits the work by the parity of (Entity.tickCount + getId()):
//
//	int i = this.tickCount + this.getId();
//	if (i % 2 != 0 && this.tickCount > 1) {          // LIGHT phase
//	    targetSelector.tickRunningGoals(false);
//	    goalSelector.tickRunningGoals(false);
//	} else {                                          // FULL phase (also the first tick)
//	    targetSelector.tick();
//	    goalSelector.tick();
//	}
//	// ... navigation / controls run EVERY tick (outside the branch)
//
//	[VERIFIED javap Mob.serverAiStep: istore_2 (i=tickCount+getId()); iload_2 iconst_2 irem ifeq
//	 ->FULL; iload tickCount iconst_1 if_icmpgt ->LIGHT; else ->FULL. GoalSelector.tick tail:
//	 tickRunningGoals(true). GoalSelector.tickRunningGoals(z): tick a running goal iff z ||
//	 requiresUpdateEveryTick().]
//
// These tests prove: (1) the FULL/LIGHT classification matches the exact javap modulus+phase for
// several ids; (2) a running goal WITHOUT requiresUpdateEveryTick ticks ONLY on FULL phases (so its
// per-tick work + any canUse RNG runs every OTHER tick, not every tick); (3) a running goal WITH
// requiresUpdateEveryTick ticks EVERY tick; (4) start/stop re-evaluation (canUse) happens ONLY on
// FULL phases; (5) navigation/jump (movement) runs EVERY tick, full-rate, regardless of the phase.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// wantFullPhase computes the vanilla FULL/LIGHT classification directly from the javap condition, so
// the test's oracle IS the bytecode formula (not a re-derived guess). aiTickCount is the value AFTER
// the top-of-serverAiStep increment (i.e. the count of serverAiStep calls so far, 1-based).
func wantFullPhase(aiTickCount int, id int32) bool {
	i := aiTickCount + int(id)
	light := i%2 != 0 && aiTickCount > 1
	return !light
}

// TestServerAiStepDecimationPhaseMatchesJavap drives serverAiStep and, for each tick, asserts the
// running goal ticked IFF the tick was a FULL phase (goal has no requiresUpdateEveryTick), matching
// the exact javap (tickCount+id)%2 modulus + the tickCount>1 phase guard — across several ids so the
// id-offset phase is exercised, not just id 0.
func TestServerAiStepDecimationPhaseMatchesJavap(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const ticks = 20

	for _, id := range []int32{0, 1, 2, 3, 7, 4242} {
		e := NewEntity(id, entity.Pig, 0, 0, 0)
		m := &mobAI{}
		m.rng = newEntityRandom(defaultEntityRandomSeed)
		// A single always-usable MOVE goal that does NOT require an every-tick update: on a LIGHT
		// phase tickRunningGoals(false) must SKIP it, so its tick() runs only on FULL phases.
		g := newTestGoal(flagMove)
		g.use = true
		g.everyTick = false
		m.goals.addGoal(1, g)
		e.ai = m

		prevTicks := 0
		for c := 1; c <= ticks; c++ {
			m.serverAiStep(loop, e)
			full := wantFullPhase(c, id) // c == m.aiTickCount after this step top-increment
			tickedThisStep := g.ticks - prevTicks
			prevTicks = g.ticks
			if full && tickedThisStep != 1 {
				t.Fatalf("id=%d tick=%d: FULL phase must tick the running non-everyTick goal once, got %d", id, c, tickedThisStep)
			}
			if !full && tickedThisStep != 0 {
				t.Fatalf("id=%d tick=%d: LIGHT phase must NOT tick a non-everyTick goal (tickRunningGoals(false) skips it), got %d", id, c, tickedThisStep)
			}
		}
	}
}

// TestServerAiStepDecimationCadence asserts the aggregate cadence: over N ticks a decimated goal ticks
// on exactly the FULL-phase count, and start()/canUse re-eval fires only on FULL phases — never LIGHT.
func TestServerAiStepDecimationCadence(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const ticks = 40
	const id int32 = 2 // i = aiTickCount+2 => same parity as aiTickCount => LIGHT on odd aiTickCount>1

	e := NewEntity(id, entity.Pig, 0, 0, 0)
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	g := newTestGoal(flagMove)
	g.use = true
	g.everyTick = false
	m.goals.addGoal(1, g)
	e.ai = m

	wantFull := 0
	for c := 1; c <= ticks; c++ {
		if wantFullPhase(c, id) {
			wantFull++
		}
		m.serverAiStep(loop, e)
	}
	if g.ticks != wantFull {
		t.Fatalf("decimated goal ticked %d times over %d ticks, want %d (the FULL-phase count for id=%d)", g.ticks, ticks, wantFull, id)
	}
	// The goal is always usable and never stops, so it starts EXACTLY once — on the first FULL phase.
	// If a LIGHT phase ever ran the full GoalSelector.tick (the C-1 bug), start could re-fire; it must not.
	if g.starts != 1 {
		t.Fatalf("goal started %d times, want exactly 1 (start/canUse re-eval must run only on FULL phases)", g.starts)
	}
	// Sanity: decimation means strictly fewer FULL phases than total ticks.
	if wantFull >= ticks {
		t.Fatalf("expected fewer FULL phases than total ticks (decimation), got wantFull=%d ticks=%d", wantFull, ticks)
	}
}

// TestServerAiStepEveryTickGoalRunsFullRate asserts a running goal whose requiresUpdateEveryTick is
// true (e.g. the pig @8 RandomLookAroundGoal) ticks EVERY tick — on both FULL and LIGHT phases —
// because tickRunningGoals(false) still ticks a requiresUpdateEveryTick goal.
func TestServerAiStepEveryTickGoalRunsFullRate(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const ticks = 20
	const id int32 = 5

	e := NewEntity(id, entity.Pig, 0, 0, 0)
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	g := newTestGoal(flagMove)
	g.use = true
	g.everyTick = true // requiresUpdateEveryTick == true
	m.goals.addGoal(1, g)
	e.ai = m

	for c := 1; c <= ticks; c++ {
		m.serverAiStep(loop, e)
	}
	if g.ticks != ticks {
		t.Fatalf("requiresUpdateEveryTick goal ticked %d times over %d ticks, want %d (full-rate, both phases)", g.ticks, ticks, ticks)
	}
}

// TestServerAiStepMovementRunsEveryTick asserts the movement half of serverAiStep (jumpControl.tick,
// which is outside the (tickCount+id)%2 branch in the jar) runs on EVERY tick, full-rate, regardless
// of the goal decimation phase. We arm the jumpControl each tick and confirm it is consumed every
// single tick, including LIGHT phases.
func TestServerAiStepMovementRunsEveryTick(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const ticks = 12
	const id int32 = 0 // ticks 3,5,7,9,11 are LIGHT phases for id=0

	e := NewEntity(id, entity.Pig, 0, 0, 0)
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	e.ai = m

	for c := 1; c <= ticks; c++ {
		// Arm the jump flag BEFORE the step; jumpControl.tick (movement half) must consume it EVERY
		// tick — a LIGHT phase does not skip the jump slot. If movement were (wrongly) gated behind the
		// decimation branch, e.jumping would not update on LIGHT ticks.
		m.jumpControl.doJump()
		e.jumping = false
		m.serverAiStep(loop, e)
		if !e.jumping {
			t.Fatalf("tick=%d (full=%v): jumpControl.tick did not run — movement must be full-rate, not decimated", c, wantFullPhase(c, id))
		}
		if m.jumpControl.jump {
			t.Fatalf("tick=%d: jumpControl.tick ran but did not clear the armed flag", c)
		}
	}
}
