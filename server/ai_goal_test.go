package server

// ai_goal_test.go — AI-01 goal-arbitration tests. These exercise the ported GoalSelector
// flag-lock arbitration (Pitfall 4) and the Goal$Flag bitset against the jar-confirmed
// semantics read from net.minecraft.world.entity.ai.goal.GoalSelector / Goal / Goal$Flag /
// WrappedGoal (javap, temp/cache/26.2-inner.jar, this session).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// testGoal is a controllable fake goal for the arbitration tests: settable canUse + flags,
// and start/stop/tick counters so a test can assert start()/stop() fire exactly once on a
// transition and tick() runs only while the goal is running. canContinue defaults to canUse
// (the vanilla Goal.canContinueToUse default) unless an explicit override is set.
type testGoal struct {
	use            bool
	cont           *bool // nil => canContinueToUse defaults to canUse
	gflags         goalFlag
	interruptable  bool
	everyTick      bool
	starts, stops  int
	ticks          int
}

func newTestGoal(f goalFlag) *testGoal {
	return &testGoal{gflags: f, interruptable: true}
}

func (g *testGoal) canUse(_ *TickLoop, _ *Entity) bool { return g.use }
func (g *testGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if g.cont != nil {
		return *g.cont
	}
	return g.canUse(t, e)
}
func (g *testGoal) isInterruptable() bool        { return g.interruptable }
func (g *testGoal) start(_ *TickLoop, _ *Entity) { g.starts++ }
func (g *testGoal) stop(_ *TickLoop, _ *Entity)  { g.stops++ }
func (g *testGoal) tick(_ *TickLoop, _ *Entity)  { g.ticks++ }
func (g *testGoal) requiresUpdateEveryTick() bool { return g.everyTick }
func (g *testGoal) flags() goalFlag              { return g.gflags }

// TestGoalFlagBitset proves Goal$Flag is the exact 4-bit set MOVE/LOOK/JUMP/TARGET and the
// bitwise free/lock/unlock ops the GoalSelector arbitration relies on.
func TestGoalFlagBitset(t *testing.T) {
	all := []goalFlag{flagMove, flagLook, flagJump, flagTarget}
	// 4 distinct, single-bit, non-overlapping flags.
	for i := range all {
		if all[i] == 0 || all[i]&(all[i]-1) != 0 {
			t.Fatalf("flag %d is not a single power-of-two bit: %08b", i, all[i])
		}
		for j := i + 1; j < len(all); j++ {
			if all[i]&all[j] != 0 {
				t.Fatalf("flags %d and %d overlap: %08b & %08b", i, j, all[i], all[j])
			}
		}
	}
	// locked&flags==0 tests free; |= locks; &^= frees.
	var locked goalFlag
	if locked&flagMove != 0 {
		t.Fatal("MOVE should start free")
	}
	locked |= flagMove
	if locked&flagMove == 0 {
		t.Fatal("MOVE should be locked after |=")
	}
	if locked&flagLook != 0 {
		t.Fatal("locking MOVE must not lock LOOK")
	}
	locked &^= flagMove
	if locked&flagMove != 0 {
		t.Fatal("MOVE should be free after &^=")
	}
}

// TestGoalSelectorRunsEligible: a single eligible goal is started once, ticked each
// tickRunningGoals while running, and stopped once when canContinueToUse goes false (freeing
// its flag).
func TestGoalSelectorRunsEligible(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 0, 0)

	g := newTestGoal(flagMove)
	g.everyTick = true // so tickRunningGoals ticks it every tick regardless of canSimulate
	g.use = true

	var gs goalSelector
	gs.addGoal(0, g)

	gs.tick(loop, e)
	if g.starts != 1 {
		t.Fatalf("start() should fire once on activation, got %d", g.starts)
	}
	if gs.locked&flagMove == 0 {
		t.Fatal("a running MOVE goal must lock MOVE")
	}
	// Vanilla tick() ends by calling tickRunningGoals(true), so the goal ticks once during
	// the activation tick itself. Baseline from here, then assert each explicit
	// tickRunningGoals adds exactly one tick.
	base := g.ticks
	gs.tickRunningGoals(loop, e, true)
	gs.tickRunningGoals(loop, e, true)
	if g.ticks-base != 2 {
		t.Fatalf("running goal should tick once per tickRunningGoals call, got %d extra", g.ticks-base)
	}

	// canContinueToUse AND canUse go false -> the goal stops on the next tick and frees its
	// flag (if canUse stayed true the goal would restart the same tick, vanilla-faithfully).
	no := false
	g.cont = &no
	g.use = false
	gs.tick(loop, e)
	if g.stops != 1 {
		t.Fatalf("stop() should fire once when canContinueToUse goes false, got %d", g.stops)
	}
	if gs.locked&flagMove != 0 {
		t.Fatal("a stopped goal must free its flag")
	}
}

// TestGoalFlagLockPreemption: two goals both claim MOVE. The higher-precedence one (lower
// priority number) runs and locks MOVE; the lower one does NOT start while MOVE is held by a
// non-interruptable-preemptable holder. When the holder stops, the lower may start. This is
// the Pitfall-4 arbitration: no jitter from two MOVE goals stomping each other.
func TestGoalFlagLockPreemption(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 0, 0)

	hi := newTestGoal(flagMove) // priority 0 (highest precedence)
	hi.use = true
	hi.interruptable = false // cannot be preempted while running -> the lower one must wait
	lo := newTestGoal(flagMove) // priority 5 (lower precedence)
	lo.use = true

	var gs goalSelector
	gs.addGoal(0, hi)
	gs.addGoal(5, lo)

	gs.tick(loop, e)
	if hi.starts != 1 {
		t.Fatalf("high-precedence MOVE goal should start, got %d", hi.starts)
	}
	if lo.starts != 0 {
		t.Fatalf("low-precedence MOVE goal must NOT start while MOVE is locked, got %d", lo.starts)
	}

	// The holder stops being usable -> frees MOVE -> the lower goal may now start. Both
	// canUse AND canContinueToUse must go false: in vanilla a goal whose canContinueToUse
	// fails but whose canUse still holds simply restarts the same tick (and would re-lock
	// MOVE, re-blocking lo) — so a real yield requires canUse to drop too.
	no := false
	hi.cont = &no
	hi.use = false
	gs.tick(loop, e) // stops hi (frees MOVE), then starts lo
	if hi.stops != 1 {
		t.Fatalf("holder should stop once, got %d", hi.stops)
	}
	if lo.starts != 1 {
		t.Fatalf("low-precedence goal should start once MOVE is free, got %d", lo.starts)
	}
}

// TestGoalFlagLockPreemptionInterruptable proves the canBeReplacedBy semantics ported from
// the bytecode: a running INTERRUPTABLE goal holding MOVE is preempted (stopped) by a
// not-yet-running goal of strictly higher precedence (smaller priority number) that needs the
// same flag, within a single tick.
func TestGoalFlagLockPreemptionInterruptable(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 0, 0)

	lo := newTestGoal(flagMove) // priority 5, interruptable, starts running first
	lo.use = true
	hi := newTestGoal(flagMove) // priority 0, higher precedence, becomes eligible later
	hi.use = false

	var gs goalSelector
	gs.addGoal(5, lo)
	gs.addGoal(0, hi)

	gs.tick(loop, e) // only lo is eligible -> it runs, locks MOVE
	if lo.starts != 1 {
		t.Fatalf("lo should start, got %d", lo.starts)
	}

	hi.use = true   // hi is now eligible and outranks lo
	gs.tick(loop, e) // hi preempts lo for the MOVE flag (canBeReplacedBy: lo interruptable & hi.priority<lo.priority)
	if lo.stops != 1 {
		t.Fatalf("interruptable lo should be preempted (stopped once), got %d", lo.stops)
	}
	if hi.starts != 1 {
		t.Fatalf("hi should start after preempting lo, got %d", hi.starts)
	}
	if gs.locked&flagMove == 0 {
		t.Fatal("MOVE must remain locked (now by hi)")
	}
}

// TestGoalDisjointFlagsCoexist: a MOVE goal and a LOOK goal run SIMULTANEOUSLY because their
// flags are disjoint — proving the lock is PER-FLAG, not a single global lock.
func TestGoalDisjointFlagsCoexist(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 0, 0)

	move := newTestGoal(flagMove)
	move.use = true
	look := newTestGoal(flagLook)
	look.use = true

	var gs goalSelector
	gs.addGoal(0, move)
	gs.addGoal(1, look)

	gs.tick(loop, e)
	if move.starts != 1 || look.starts != 1 {
		t.Fatalf("disjoint-flag goals must both run: move.starts=%d look.starts=%d", move.starts, look.starts)
	}
	if gs.locked&(flagMove|flagLook) != (flagMove | flagLook) {
		t.Fatalf("both MOVE and LOOK must be locked, got %08b", gs.locked)
	}
}
