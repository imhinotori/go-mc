package server

// ai_goal.go — AI-01: the ported vanilla mob goal-selection model.
//
// PORTED (the STANDING MANDATE, non-1:1 idiomatic Go, never a GPL paste) from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), read via javap this session:
//
//   - net.minecraft.world.entity.ai.goal.GoalSelector  (tick / tickRunningGoals + the
//     control-flag locking: lockedFlags Map<Goal$Flag,WrappedGoal>, availableGoals,
//     disabledFlags; goalContainsAnyFlags / goalCanBeReplacedForAllFlags)
//   - net.minecraft.world.entity.ai.goal.Goal          (the goal contract + defaults:
//     canContinueToUse defaults to canUse; isInterruptable defaults true)
//   - net.minecraft.world.entity.ai.goal.Goal$Flag     (EXACTLY MOVE/LOOK/JUMP/TARGET)
//   - net.minecraft.world.entity.ai.goal.WrappedGoal   (priority + isRunning wrapper;
//     canBeReplacedBy = isInterruptable() && other.priority < this.priority)
//
// THE LOAD-BEARING MECHANIC (07-RESEARCH Pitfall 4): a goal runs only when every control
// flag it needs can be claimed; on start it locks its flags, on stop it frees them. A
// not-running goal can PREEMPT a running interruptable goal of strictly lower precedence
// (larger priority number) that holds a flag it needs — and is blocked by a non-interruptable
// or higher-precedence holder. Without this, two goals that both claim MOVE (stroll + panic)
// jitter the mob. This is the exact `canBeReplacedBy` semantics read from the bytecode.
//
// PRECEDENCE NOTE (jar-confirmed): a SMALLER priority int = HIGHER precedence. addGoal(0, …)
// outranks addGoal(6, …). NO_GOAL (the empty holder) has priority Integer.MAX_VALUE and is
// interruptable, so a free flag is always claimable.
//
// SINGLE-OWNER (TICK-05): the goalSelector and every goal it holds are tick-owned game state,
// mutated ONLY by the tick goroutine. No goroutine, no xsync — plain Go structs. A goal's
// tick() SETS a navigation/look target (see ai_goals_passive.go); it never moves the mob and
// never crosses a goroutine.

// goalFlag is the ported Goal$Flag control-flag set — EXACTLY the four flags read from
// `javap Goal$Flag` (MOVE, LOOK, JUMP, TARGET), modeled as a 4-bit bitset so the arbitration
// uses bitwise free/lock/unlock (locked&flags==0 tests free; |= locks; &^= frees).
type goalFlag uint8

const (
	flagMove   goalFlag = 1 << iota // Goal$Flag.MOVE   — navigation / position control
	flagLook                        // Goal$Flag.LOOK   — head/look angle control
	flagJump                        // Goal$Flag.JUMP   — jump control
	flagTarget                      // Goal$Flag.TARGET — attack-target selection (targetSelector)
)

// Goal is the ported abstract Goal contract as a Go interface. Each method maps to the Java
// method of the same role; the (t *TickLoop, e *Entity) parameters replace Java's implicit
// `this.mob`/`this.level` — a goal reads the world and its mob through them rather than
// holding a back-pointer, keeping goals snapshot-friendly and tick-owned.
//
// The default-method shapes from `javap Goal` (canContinueToUse -> canUse; isInterruptable ->
// true; requiresUpdateEveryTick -> false; start/stop/tick -> no-op) are provided by the
// embeddable baseGoal below, so a concrete goal overrides only what it needs.
type Goal interface {
	// canUse reports whether the goal may START this tick (Java Goal.canUse, abstract).
	canUse(t *TickLoop, e *Entity) bool
	// canContinueToUse reports whether a RUNNING goal may keep running (Java
	// Goal.canContinueToUse — defaults to canUse via baseGoal).
	canContinueToUse(t *TickLoop, e *Entity) bool
	// isInterruptable reports whether a running instance may be PREEMPTED by a
	// higher-precedence goal that needs one of its flags (Java Goal.isInterruptable,
	// default true).
	isInterruptable() bool
	// start is called once when the goal transitions to running (Java Goal.start).
	start(t *TickLoop, e *Entity)
	// stop is called once when the goal transitions to not-running (Java Goal.stop).
	stop(t *TickLoop, e *Entity)
	// tick advances the running goal (Java Goal.tick) — it SETS a navigation/look target,
	// it does NOT move the mob.
	tick(t *TickLoop, e *Entity)
	// requiresUpdateEveryTick reports whether tick() must run even when the selector is
	// not simulating (Java Goal.requiresUpdateEveryTick, default false).
	requiresUpdateEveryTick() bool
	// flags returns the control-flag set this goal claims while running (Java
	// Goal.getFlags — the EnumSet<Goal$Flag> set via setFlags in each goal's ctor).
	flags() goalFlag
}

// baseGoal is the embeddable struct supplying Goal's default-method behavior so a concrete
// goal implements only canUse + its real start/stop/tick. It carries the goal's flag set
// (the Java `flags` EnumSet) and an interruptable bit.
//
// Go embedding cannot replicate Java's "canContinueToUse calls the OVERRIDDEN canUse"
// virtual dispatch (baseGoal has no view of the concrete canUse). So the chosen pattern is:
// a concrete goal whose continue-condition equals its use-condition implements
// canContinueToUse explicitly delegating to its own canUse; baseGoal's default returns true
// (a running goal keeps running until the concrete goal says otherwise). This is documented
// and matches the per-goal bytecode (every real goal here defines its own canContinueToUse).
type baseGoal struct {
	gflags        goalFlag
	interruptable bool
}

func newBaseGoal(flags goalFlag) baseGoal { return baseGoal{gflags: flags, interruptable: true} }

func (b *baseGoal) canContinueToUse(*TickLoop, *Entity) bool { return true }
func (b *baseGoal) isInterruptable() bool                    { return b.interruptable }
func (b *baseGoal) start(*TickLoop, *Entity)                 {}
func (b *baseGoal) stop(*TickLoop, *Entity)                  {}
func (b *baseGoal) tick(*TickLoop, *Entity)                  {}
func (b *baseGoal) requiresUpdateEveryTick() bool            { return false }
func (b *baseGoal) flags() goalFlag                          { return b.gflags }

// wrappedGoal is the ported WrappedGoal: the priority + running wrapper the selector stores.
// It delegates the Goal contract to the wrapped goal (the Java WrappedGoal forwards every
// method); running is the isRunning bit and start/stop are idempotent on it (Java
// WrappedGoal.start/stop guard on isRunning).
type wrappedGoal struct {
	priority int  // SMALLER = higher precedence (jar-confirmed)
	running  bool // isRunning
	g        Goal // the delegated goal
}

// canBeReplacedBy ports WrappedGoal.canBeReplacedBy exactly (bytecode):
//
//	return this.isInterruptable() && other.getPriority() < this.getPriority();
//
// i.e. a running goal yields a flag to `other` only if it is interruptable AND `other` has
// strictly higher precedence (smaller priority number). The empty holder (priority maxInt,
// interruptable) always yields, so a free flag is claimable.
func (w *wrappedGoal) canBeReplacedBy(other *wrappedGoal) bool {
	return w.g.isInterruptable() && other.priority < w.priority
}

// maxPriority is the priority of the empty flag-holder (Java NO_GOAL = Integer.MAX_VALUE):
// the largest possible value so any real goal outranks "nobody holds this flag", and the
// empty holder is treated as interruptable so a free flag always yields.
const maxPriority = int(^uint(0) >> 1)

// goalSelector is the ported GoalSelector: a priority-ordered set of wrappedGoals plus the
// control-flag lock state. lockedBy maps each held flag to the wrappedGoal holding it (Java
// lockedFlags Map<Goal$Flag,WrappedGoal>); locked is the derived bitset of held flags for the
// fast free-test (lock-set membership). disabled is the set of administratively-disabled
// flags (Java disabledFlags) — a goal needing a disabled flag never runs.
//
// goals is kept sorted ascending by priority (highest precedence first) so tick() walks it in
// the Java availableGoals iteration order. (Vanilla uses an insertion-ordered Set; iterating
// in priority order is the faithful, deterministic equivalent — the arbitration result is the
// same because preemption is decided per-flag by canBeReplacedBy, and a sorted walk lets a
// higher-precedence goal claim a flag before a lower one is considered.)
type goalSelector struct {
	goals    []*wrappedGoal
	lockedBy map[goalFlag]*wrappedGoal
	locked   goalFlag // derived: OR of all currently-held flags
	disabled goalFlag
}

// addGoal ports GoalSelector.addGoal(priority, goal): wrap the goal with its priority and
// insert keeping the slice sorted ascending by priority (highest precedence first).
func (gs *goalSelector) addGoal(priority int, g Goal) {
	wg := &wrappedGoal{priority: priority, g: g}
	// Insertion sort keeps the slice in priority order; the goal count per mob is tiny
	// (a Pig has ~9), so this is trivially cheap and keeps the tick walk deterministic.
	i := 0
	for i < len(gs.goals) && gs.goals[i].priority <= priority {
		i++
	}
	gs.goals = append(gs.goals, nil)
	copy(gs.goals[i+1:], gs.goals[i:])
	gs.goals[i] = wg
}

// eachFlag invokes fn for every control flag set in f (MOVE/LOOK/JUMP/TARGET). The four-flag
// fixed set keeps this a constant-bounded loop.
func eachFlag(f goalFlag, fn func(goalFlag)) {
	for _, fl := range [...]goalFlag{flagMove, flagLook, flagJump, flagTarget} {
		if f&fl != 0 {
			fn(fl)
		}
	}
}

// goalContainsAnyFlags ports the helper: does the goal claim ANY flag in the given set?
// Used to skip goals that need an administratively-disabled flag.
func goalContainsAnyFlags(wg *wrappedGoal, set goalFlag) bool {
	return wg.g.flags()&set != 0
}

// goalCanBeReplacedForAllFlags ports the helper: can `wg` claim EVERY flag it needs right now?
// For each needed flag, the current holder (or the implicit empty holder) must yield to wg via
// canBeReplacedBy. A free flag yields (empty holder, maxPriority, interruptable); a flag held
// by a non-interruptable or higher-precedence goal does NOT yield, so wg cannot start.
func (gs *goalSelector) goalCanBeReplacedForAllFlags(wg *wrappedGoal) bool {
	ok := true
	eachFlag(wg.g.flags(), func(fl goalFlag) {
		holder := gs.holderOf(fl)
		if !holder.canBeReplacedBy(wg) {
			ok = false
		}
	})
	return ok
}

// holderOf returns the wrappedGoal currently holding flag fl, or the implicit empty holder
// (Java NO_GOAL: priority maxInt, interruptable) when the flag is free.
func (gs *goalSelector) holderOf(fl goalFlag) *wrappedGoal {
	if gs.lockedBy != nil {
		if h, ok := gs.lockedBy[fl]; ok {
			return h
		}
	}
	return emptyHolder
}

// emptyHolder is the ported NO_GOAL: the sentinel that "holds" every free flag. priority is
// maxInt and emptyGoal.isInterruptable() is true, so canBeReplacedBy always yields a free flag
// to any real candidate.
var emptyHolder = &wrappedGoal{priority: maxPriority, g: emptyGoal{}}

// emptyGoal is NO_GOAL's inert goal: never usable, holds no flags, always interruptable.
type emptyGoal struct{}

func (emptyGoal) canUse(*TickLoop, *Entity) bool           { return false }
func (emptyGoal) canContinueToUse(*TickLoop, *Entity) bool { return false }
func (emptyGoal) isInterruptable() bool                    { return true }
func (emptyGoal) start(*TickLoop, *Entity)                 {}
func (emptyGoal) stop(*TickLoop, *Entity)                  {}
func (emptyGoal) tick(*TickLoop, *Entity)                  {}
func (emptyGoal) requiresUpdateEveryTick() bool            { return false }
func (emptyGoal) flags() goalFlag                          { return 0 }

// tick ports GoalSelector.tick (bytecode-faithful, two passes + the running-goal tick):
//
//  1. goalCleanup: for each RUNNING goal, stop it if it claims a disabled flag OR its
//     canContinueToUse() is false. Stopping frees its flags.
//  2. (Java removeIf on lockedFlags entries whose goal stopped running — here, freeing on
//     stop already drops the holder, so the lock map only ever maps to running holders.)
//  3. goalUpdate: for each NOT-running goal, skip if it needs a disabled flag, skip if it
//     cannot claim all its flags (goalCanBeReplacedForAllFlags), skip if !canUse(); else for
//     each needed flag stop the current holder and assign this goal, then start it.
//  4. tickRunningGoals(true) — vanilla calls it at the end of tick().
//
// Runs on the tick goroutine over tick-owned state (TICK-05).
func (gs *goalSelector) tick(t *TickLoop, e *Entity) {
	// Pass 1 — stop running goals that can no longer run, freeing their flags.
	for _, wg := range gs.goals {
		if !wg.running {
			continue
		}
		if goalContainsAnyFlags(wg, gs.disabled) || !wg.g.canContinueToUse(t, e) {
			gs.stopGoal(t, e, wg)
		}
	}

	// Pass 2 — start eligible not-running goals in precedence order, claiming (and possibly
	// preempting) their flags.
	for _, wg := range gs.goals {
		if wg.running {
			continue
		}
		if goalContainsAnyFlags(wg, gs.disabled) {
			continue // needs an administratively-disabled flag
		}
		if !gs.goalCanBeReplacedForAllFlags(wg) {
			continue // some needed flag is held by a goal that will not yield
		}
		if !wg.g.canUse(t, e) {
			continue
		}
		// Claim every needed flag: stop the current holder (Java NO_GOAL.stop is a no-op),
		// then record this goal as the holder. Then start it.
		eachFlag(wg.g.flags(), func(fl goalFlag) {
			if holder := gs.holderOf(fl); holder != emptyHolder {
				gs.stopGoal(t, e, holder)
			}
			gs.lock(fl, wg)
		})
		wg.running = true
		wg.g.start(t, e)
	}

	gs.tickRunningGoals(t, e, true)
}

// tickRunningGoals ports GoalSelector.tickRunningGoals(canSimulate): tick every running goal
// when canSimulate is true OR the goal requiresUpdateEveryTick (Java: `if (canSimulate ||
// requiresUpdateEveryTick()) tick()`).
func (gs *goalSelector) tickRunningGoals(t *TickLoop, e *Entity, canSimulate bool) {
	for _, wg := range gs.goals {
		if !wg.running {
			continue
		}
		if canSimulate || wg.g.requiresUpdateEveryTick() {
			wg.g.tick(t, e)
		}
	}
}

// stopGoal stops a running goal (idempotent, like Java WrappedGoal.stop) and frees every flag
// it held — but only the flags this goal actually holds in the lock map (preemption may have
// already reassigned one). Calls the goal's stop() exactly once on the running->stopped edge.
func (gs *goalSelector) stopGoal(t *TickLoop, e *Entity, wg *wrappedGoal) {
	if !wg.running {
		return
	}
	wg.running = false
	// Free only the flags this goal currently holds (another goal may have preempted one).
	for fl, holder := range gs.lockedBy {
		if holder == wg {
			delete(gs.lockedBy, fl)
			gs.locked &^= fl
		}
	}
	wg.g.stop(t, e)
}

// lock records wg as the holder of flag fl and sets the derived locked bit.
func (gs *goalSelector) lock(fl goalFlag, wg *wrappedGoal) {
	if gs.lockedBy == nil {
		gs.lockedBy = make(map[goalFlag]*wrappedGoal, 4)
	}
	gs.lockedBy[fl] = wg
	gs.locked |= fl
}
