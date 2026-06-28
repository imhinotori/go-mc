package server

// plugin_mob_ai.go — PLUGIN-03 (Wave 2): the starlarkGoal adapter + buildAIFromDecl. A Starlark-
// declared goal and a Go-native goal (ai_goals_passive.go) are the SAME interface (server.Goal) and
// are arbitrated by the SAME goalSelector (ai_goal.go) — a starlarkGoal is NOT a bypass and NOT a
// parallel AI tick. buildAIFromDecl mirrors newPigAI EXACTLY (a fresh *mobAI per spawned mob whose
// goals.addGoal registers starlarkGoals), so a declared mob ticks through the EXISTING serverAiStep
// -> goalSelector -> navigation -> moveEntity chain. The PLUGIN-03 architecture proof: the API can
// express real AI (Phase 24's claim) while the interpreter touches ONLY the active decision seam.
//
// THE INTERPRETER-ONLY-WHEN-RUNNING INVARIANT (23-CONTEXT, T-23-08): starlark.Call fires ONLY inside
// a RUNNING goal's tick/canUse/start/stop — gated by goalSelector arbitration. An IDLE declared mob
// (its goal not holding its claimed flags) makes ZERO starlark.Calls per tick. buildAIFromDecl adds
// the goal to the selector; it does NOT add an unconditional per-tick interpreter call. There is no
// starlark.Call in tickAI/tickPhysics/tickEntities — only inside the methods below, which the
// selector invokes only for a running goal.
//
// THE ISOLATION + DoS INVARIANTS (T-23-06/07): every callback runs on a FRESH budget-bounded thread
// (starlarkpkg.NewThread — the stepBudget runaway guard) and its error is ISOLATED (logged, the tick
// survives). One bad/hung/erroring callback never kills the tick goroutine (which would disconnect
// every player); the tickOnce recover backstop is the second line.
//
// SINGLE-OWNER (TICK-05): a starlarkGoal's methods run on the tick goroutine over tick-owned state;
// the handles they build (newEntityHandle/newWorldHandle/newNavHandle) carry an id + the owning
// plugin's capSet (Wave-1 thin-handle: NEVER a live *Entity), re-resolved per access on the owner —
// so no live *Entity escapes to another goroutine and the declared-mob tick path is -race clean.

import (
	"log"

	starlarkpkg "github.com/imhinotori/sulfur/plugin/starlark"
	"go.starlark.net/starlark"
)

// starlarkGoal is the server.Goal adapter over a captured goalDecl. It embeds baseGoal (so the
// flag set + the interruptable/requiresUpdateEveryTick defaults come from ai_goal.go) and holds the
// SHARED frozen callables + the owning plugin's caps + the mob name (for thread naming + error
// attribution). Each SPAWNED mob gets a FRESH starlarkGoal struct (per-mob state) referencing the
// shared frozen callables (Pattern 4) — buildAIFromDecl allocates fresh structs, it never re-parses.
type starlarkGoal struct {
	baseGoal       // flags from the declaration via newBaseGoal(gd.flags) + the defaults
	t        *TickLoop // the tick loop the handles re-resolve through (tick goroutine only)
	caps     capSet    // the owning plugin's capabilities, threaded into every handle
	name     string    // the declared mob name (thread name + error attribution)
	canUseFn starlark.Callable
	tickFn   starlark.Callable
	startFn  starlark.Callable
	stopFn   starlark.Callable
}

// Compile-time assertion: starlarkGoal IS a server.Goal (slots into goalSelector.addGoal unchanged).
var _ Goal = (*starlarkGoal)(nil)

// handles builds the three fresh handles a callback receives this call: the entity handle (the mob),
// the world handle, and the nav handle — each carrying the mob's id (from the *Entity the selector
// passed) + the owning plugin's capSet (NEVER a live *Entity). Fresh per call (no stashing) so a
// stashed handle can only ever re-resolve the store on the owner, never deref a stale pointer. The
// id comes from e (every Goal method receives the live *Entity from serverAiStep), so one
// starlarkGoal instance correctly drives whatever entity the selector ticks it for.
func (g *starlarkGoal) handles(e *Entity) starlark.Tuple {
	eh := newEntityHandle(g.t, e.id, g.caps)
	wh := newWorldHandle(g.t, g.caps)
	nh := newNavHandle(g.t, e.id, g.caps)
	return starlark.Tuple{eh, wh, nh}
}

// call invokes a captured callable on a FRESH budget-bounded thread with the three handles, ISOLATING
// any error (logged with the mob name, never propagated) — the load-bearing T-23-06/07 guard. It
// returns the raw result (the caller coerces it) and whether the call succeeded (a failed call is
// treated as the safe default by each caller). A runaway callback hits the thread's stepBudget and
// returns an EvalError here, which is logged and absorbed — the tick continues.
func (g *starlarkGoal) call(e *Entity, fn starlark.Callable) (starlark.Value, bool) {
	th := starlarkpkg.NewThread("ai:" + g.name)
	res, err := starlark.Call(th, fn, g.handles(e), nil)
	if err != nil {
		log.Printf("custom mob goal %q callback error: %v", g.name, err)
		return nil, false
	}
	return res, true
}

// canUse ports Goal.canUse for a declared goal: if no can_use callable was declared the goal is
// usable whenever the selector offers it its flags (return true — the common wander case). Else the
// callable decides, coerced to a truthy bool; an erroring/hung callback ISOLATES to false (the goal
// simply does not start this tick) so a bad predicate never crashes the tick. starlark.Call fires
// ONLY here, while the selector is evaluating THIS goal — not unconditionally per mob.
func (g *starlarkGoal) canUse(_ *TickLoop, e *Entity) bool {
	if g.canUseFn == nil {
		return true
	}
	res, ok := g.call(e, g.canUseFn)
	if !ok {
		return false
	}
	return bool(res.Truth())
}

// canContinueToUse: a running declared goal keeps running while its canUse still holds (a wander goal
// continues while usable). When no can_use was declared this delegates to canUse's default (true),
// matching the Go goals' "every real goal defines its own canContinueToUse" convention (ai_goal.go).
// This fires starlark.Call ONLY for a RUNNING goal (pass 1 of goalSelector.tick) — an idle goal is
// never asked, so an idle declared mob makes zero calls.
func (g *starlarkGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// start invokes the optional start callable when the goal transitions to running (the flag-claim
// edge), isolated. No start callable => no-op (baseGoal default). Fires only on the not-running ->
// running edge for a goal that just claimed its flags.
func (g *starlarkGoal) start(_ *TickLoop, e *Entity) {
	if g.startFn != nil {
		g.call(e, g.startFn)
	}
}

// stop invokes the optional stop callable when the goal transitions to not-running (the flag-free
// edge), isolated. No stop callable => no-op. Fires only on the running -> not-running edge.
func (g *starlarkGoal) stop(_ *TickLoop, e *Entity) {
	if g.stopFn != nil {
		g.call(e, g.stopFn)
	}
}

// tick advances the RUNNING goal: it invokes the declared tick callable on a fresh budget-bounded
// thread with the three fresh handles, ISOLATING any error. The callback SETS a nav target via the
// nav handle's path_to (-> setWantTarget -> the async A* requestPath, already wired) — it never moves
// the mob; serverAiStep's navigation.tick + moveEntity do that next. starlark.Call fires ONLY here,
// for a RUNNING goal (tickRunningGoals only ticks running goals) — the interpreter-only-when-running
// invariant (T-23-08): an idle declared mob makes ZERO calls per tick.
func (g *starlarkGoal) tick(_ *TickLoop, e *Entity) {
	if g.tickFn == nil {
		return
	}
	g.call(e, g.tickFn)
}

// buildAIFromDecl mirrors newPigAI EXACTLY (ai_mob.go): a fresh *mobAI per spawned mob with a
// starlarkGoal per declared goal, each referencing the SHARED frozen callables (the goalDecl
// callables) — the goal struct is per-mob state, the callables are shared frozen values (Pattern 4).
// It does NOT re-parse the plugin: it allocates fresh structs over the already-captured declaration.
//
// The walk speed derives from the declared movement_speed (scaled to a blocks/tick ground pace) when
// present, else the pigWalkSpeed default — a tunable, wire-irrelevant value (like newPigAI's). The
// caps come from decl.caps (the owning plugin's manifest capabilities, stamped at declare time), so
// every handle a goal callback builds carries the right least-privilege grant.
//
// Each goal is registered via m.goals.addGoal(priority, g) — the SAME goalSelector entry point the
// Go goals use — so a declared goal is arbitrated by the SAME flag-locking precedence logic. NOT a
// bypass, NOT a parallel tick.
func buildAIFromDecl(t *TickLoop, decl *mobDecl) *mobAI {
	m := &mobAI{}
	m.navigation.speed = declaredWalkSpeed(decl)
	// Per-mob seeded RandomSource (the Mob.getRandom() analogue) — same as newPigAI; the spawn site
	// reseeds it per entity id (reseedMobAI) so each declared mob has its own deterministic stream.
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	for _, gd := range decl.goals {
		m.goals.addGoal(gd.priority, &starlarkGoal{
			baseGoal: newBaseGoal(gd.flags),
			t:        t,
			caps:     decl.caps,
			name:     decl.name,
			canUseFn: gd.canUseFn,
			tickFn:   gd.tickFn,
			startFn:  gd.startFn,
			stopFn:   gd.stopFn,
		})
	}
	return m
}

// declaredWalkSpeed maps a declared movement_speed attribute (≈0.25 for a pig) to a blocks/tick walk
// pace. Vanilla's movement_speed attribute (~0.25) is NOT blocks/tick directly; newPigAI uses a
// fixed pigWalkSpeed (0.15) for a visibly-alive amble. A declared mob without a movement_speed
// override gets that same default; a declared override scales it proportionally to the pig baseline
// so a faster-declared mob walks faster. This is a tunable, wire-irrelevant value (gated by the
// real-client visual check, like the physics constants), NOT a 1:1 vanilla mapping (the gate mob is
// a CUSTOM mob, free of the 1:1 mandate — v4-PLAN).
func declaredWalkSpeed(decl *mobDecl) float64 {
	const pigMovementSpeed = 0.25 // the pig base movement_speed attribute (Wave-1 SUB-ATTRIB)
	if ms, ok := decl.attrs["movement_speed"]; ok && ms > 0 {
		return pigWalkSpeed * (ms / pigMovementSpeed)
	}
	return pigWalkSpeed
}
