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
	baseGoal           // flags from the declaration via newBaseGoal(gd.flags) + the defaults
	t          *TickLoop // the tick loop the handles re-resolve through (tick goroutine only)
	caps       capSet    // the owning plugin's capabilities, threaded into every handle
	name       string    // the declared mob name (thread name + error attribution)
	canUseFn   starlark.Callable
	tickFn     starlark.Callable
	startFn    starlark.Callable
	stopFn     starlark.Callable
	continueFn starlark.Callable
	// updateEveryTick is the jar's requiresUpdateEveryTick threaded from the declaration (FIDELITY
	// GAP 1). RandomLookAroundGoal.requiresUpdateEveryTick()==true (jar-confirmed); a declared goal
	// carrying requires_update_every_tick=True overrides baseGoal's default-false so the selector
	// ticks it on the every-tick path (ai_goal.go tickRunningGoals: canSimulate || requiresUpdate...).
	updateEveryTick bool
	// scratch is the per-(mob,goal) mutable state the `entity.state` seam reads/writes — the analogue
	// of the Go goal's struct fields (lookAtPlayerGoal.lookTime/lookX/Y/Z, randomLookAroundGoal.relX/
	// relZ/lookTime) that a frozen Starlark module global cannot hold (FIDELITY GAP 2). Allocated per
	// SPAWNED mob (buildAIFromDecl), so two pigs never share a countdown; tick-owned (mutated only on
	// the tick goroutine via the entity handle the goal callback receives), -race clean by construction.
	scratch map[string]float64
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
	// Phase-27 STEP-3 (Pitfall 4): build REGION-BOUND handles. serverAiStep(t,e) runs inside the
	// OWNING region's fan-out tick, so the region that owns this mob is t.regionForEntity(e) (== the
	// calling region). Binding the handles to that region means every re-resolution a callback makes
	// (entity reads/mutates, nav, entities_near results) hits R's store explicitly — independent of
	// the goroutine-local fallback — so THE GATE holds: a hook for an entity in R resolves R's store.
	r := g.t.regionForEntity(e)
	eh := newEntityHandleInRegionWithScratch(g.t, r, e.id, g.caps, g.scratch)
	wh := newWorldHandleInRegion(g.t, r, g.caps)
	nh := newNavHandleInRegion(g.t, r, e.id, g.caps)
	return starlark.Tuple{eh, wh, nh}
}

// call invokes a captured callable on a FRESH budget-bounded thread with the three handles, ISOLATING
// any error (logged with the mob name, never propagated) — the load-bearing T-23-06/07 guard. It
// returns the raw result (the caller coerces it) and whether the call succeeded (a failed call is
// treated as the safe default by each caller). A runaway callback hits the thread's stepBudget and
// returns an EvalError here, which is logged and absorbed — the tick continues.
func (g *starlarkGoal) call(e *Entity, fn starlark.Callable) (starlark.Value, bool) {
	// Phase-27 STEP-3 test seam (nil in production): observe that this declared-goal callback fires
	// on the OWNING region's goroutine, for a mob resolvable in that region's store (THE GATE).
	if g.t.onGoalCall != nil {
		g.t.onGoalCall(e)
	}
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
	if g.continueFn == nil {
		return g.canUse(t, e)
	}
	res, ok := g.call(e, g.continueFn)
	if !ok {
		return false
	}
	return bool(res.Truth())
}

// requiresUpdateEveryTick overrides baseGoal's default-false from the declaration (FIDELITY GAP 1).
// A goal declared requires_update_every_tick=True (the ported RandomLookAroundGoal@8) ticks even when
// the selector is not simulating — matching Java RandomLookAroundGoal.requiresUpdateEveryTick()==true.
func (g *starlarkGoal) requiresUpdateEveryTick() bool { return g.updateEveryTick }

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
		// KIND-GOAL: route to the matching Go-NATIVE goal (the verbatim 35-01 jar port) instead of a
		// starlarkGoal. The hostiles' combat goals (NearestAttackableTargetGoal/HurtByTargetGoal/
		// MeleeAttackGoal/SpiderAttackGoal/LeapAtTargetGoal) are the SAME CLASS for every hostile — there
		// is nothing per-mob to re-express in .star — so a hostile DECLARES the goal's priority + flags +
		// kind and the Go-native goal does the work. The Go goal draws from THIS mob's m.rng (the same
		// per-entity seeded stream the starlarkGoal goals draw from, set above + reseeded per id by the
		// spawn site), so a kind-routed goal stays in lockstep with the .star goals. The pig declares NO
		// kind-goal, so this branch is NEVER taken for it → zero new draws → the pig oracle is unperturbed.
		if gd.nativeKind != "" {
			ng := buildNativeGoal(gd.nativeKind, gd, decl)
			if ng == nil {
				// An unknown kind is a LOUD failure (never a silent no-op): a declared hostile with a
				// typo'd combat-goal kind would otherwise boot with a MISSING combat goal (it would never
				// hunt/attack). spawnDeclaredMob runs on the tick goroutine; a panic here is isolated by
				// the tickOnce recover backstop, surfacing the bad declaration loudly rather than shipping
				// a silently-disarmed hostile. (The .star load already validated the rest of the mob.)
				panic("buildAIFromDecl: unknown goal kind " + gd.nativeKind + " (valid: nearest_attackable_target, hurt_by_target, melee_attack, spider_attack, leap_at_target, avoid_entity, float, climb_on_powder_snow, sit, follow_owner, owner_hurt_by, owner_hurt, angry_player_target, skeleton_target, enderman_look_for_player, enderman_freeze_when_looked_at, silverfish_merge_stone, silverfish_wake_friends)")
			}
			// The Go goal's OWN flags() must match the declared flags — a declaration that names, e.g.,
			// kind="melee_attack" but flags=["TARGET"] would route the goal into the WRONG selector AND
			// mis-claim the wrong control flag. The native goal's ctor sets its faithful flag set
			// (MeleeAttackGoal {MOVE}, NearestAttackableTargetGoal {TARGET}, ...), so assert the
			// declaration agrees before routing. A mismatch is a loud declaration bug.
			if ng.flags() != gd.flags {
				panic("buildAIFromDecl: goal kind " + gd.nativeKind + " declares flags that disagree with the Go-native goal's own flags")
			}
			// Route by the goal's flag EXACTLY as the starlarkGoal branch does: a TARGET goal locks the
			// TARGET flag in the independent targetSelector; every other goal arbitrates in goals.
			if gd.flags&flagTarget != 0 {
				m.targetSelector.addGoal(gd.priority, ng)
			} else {
				m.goals.addGoal(gd.priority, ng)
			}
			continue
		}
		g := &starlarkGoal{
			baseGoal:        newBaseGoal(gd.flags),
			t:               t,
			caps:            decl.caps,
			name:            decl.name,
			canUseFn:        gd.canUseFn,
			tickFn:          gd.tickFn,
			startFn:         gd.startFn,
			stopFn:          gd.stopFn,
			continueFn:      gd.continueFn,
			updateEveryTick: gd.requiresUpdateEveryTick,
			// Fresh per-(mob,goal) scratch (the Go goal's struct fields) — never shared between mobs.
			scratch: make(map[string]float64),
		}
		// TARGET-flag goals -> targetSelector (Mob.registerGoals routes HurtByTargetGoal /
		// NearestAttackableTargetGoal into mob.targetSelector, every other goal into mob.goalSelector).
		// The two selectors are independent flag-lock instances (ai_mob.go), exactly as vanilla holds a
		// separate goalSelector and targetSelector. A passive pig declares no TARGET goal, so this branch
		// is never taken for it — every pig goal still routes into m.goals (byte-identical).
		if gd.flags&flagTarget != 0 {
			m.targetSelector.addGoal(gd.priority, g)
		} else {
			m.goals.addGoal(gd.priority, g)
		}
	}
	return m
}

// buildNativeGoal instantiates the Go-NATIVE goal (the verbatim 35-01 jar port) a kind-goal names. It
// is the seam that lets a hostile .star reference the shared combat goals (the SAME class every hostile
// uses) by name instead of re-expressing their combat RNG in Starlark (a lockstep-drift risk). It
// returns nil for an unknown kind so the caller can fail loudly (never a silent no-op — a disarmed
// hostile). The melee speed derives from the declared movement_speed (declaredWalkSpeed) — the SAME
// blocks/tick pace the starlarkGoal navigation uses — so a faster-declared hostile chases faster; the
// speedModifier is stored on the goal (the want-multiplier, cited-deferred like PanicGoal's, matching
// ai_goals_attack.go's MeleeAttackGoal port). The Go goal's ctor sets its own faithful flag set, which
// the caller asserts against the declared flags before routing.
//
//   - "nearest_attackable_target" → newNearestAttackableTargetGoal()  {TARGET}; the nextInt(10) acquire
//   - "hurt_by_target"            → newHurtByTargetGoal()             {TARGET}; retaliate, NO RNG
//   - "melee_attack"              → newMeleeAttackGoal(speed)         {MOVE};   the doHurtTarget keystone
//   - "spider_attack"             → newSpiderAttackGoal(speed)        {MOVE};   melee + the daylight-flee
//   - "leap_at_target"            → newLeapAtTargetGoal(spiderLeapYd) {JUMP,MOVE}; the leap impulse
//   - "avoid_entity"              → newAvoidEntityGoal(gd.avoidType,...) {MOVE};   the generic flee (per-goal avoid_type)
//   - "float"                     → newFloatGoal()                    {JUMP};   the swim-jump
func buildNativeGoal(kind string, gd goalDecl, decl *mobDecl) Goal {
	switch kind {
	case "nearest_attackable_target":
		return newNearestAttackableTargetGoal()
	case "hurt_by_target":
		return newHurtByTargetGoal()
	case "melee_attack":
		return newMeleeAttackGoal(declaredWalkSpeed(decl))
	case "ranged_bow_attack":
		// PROJECTILE-01 (Task #8): AbstractSkeleton's RangedBowAttackGoal(this, 1.0, 20|40, 15.0) — the
		// bow goal that charges + fires an Arrow (ai_goals_ranged.go). Replaces the skeleton's melee goal
		// (the skeleton now shoots instead of swinging). Cite AbstractSkeleton.reassessWeaponGoal @4.
		return newRangedBowAttackGoal()
	case "creeper_swell":
		// MOB-HOST-06 (Task #9): Creeper's SwellGoal @2 — arms/disarms the fuse (setSwellDir) based on
		// target distance; the fuse advance + explosion live in creeperAiStep (ai_goals_creeper.go). Cite
		// Creeper.registerGoals @2 SwellGoal.
		return newSwellGoal()
	case "witch_ranged_attack":
		// MOB-HOST-07 (Task #9): the Witch's RangedAttackGoal(this, 1.0, 60, 10.0) — the generic ranged
		// goal that throws splash potions (ai_goals_witch.go). Cite Witch.registerGoals @2 RangedAttackGoal.
		return newWitchRangedAttackGoal()
	case "spider_attack":
		return newSpiderAttackGoal(declaredWalkSpeed(decl))
	case "leap_at_target":
		// Spider.registerGoals @3 LeapAtTargetGoal(this, 0.4) — the ONLY leap user in v1; the 0.4
		// vertical leap component is the Spider's literal ctor arg (35-JARNOTES.md:226-243).
		return newLeapAtTargetGoal(spiderLeapYd)
	case "avoid_entity":
		// net.minecraft.world.entity.ai.goal.AvoidEntityGoal<T> — the generic flee goal (Creeper avoids
		// Cat/Ocelot @3, Skeleton flees Wolf, Rabbit/Fox flee threats). The avoided class + maxDist + the
		// two speed modifiers are the per-goal ctor args (Creeper: AvoidEntityGoal<Cat>(this, 6.0f, 1.0,
		// 1.2)). The avoided TYPE is per-goal (gd.avoidType), NOT per-mob, so it threads from the goalDecl.
		// maxDist/speed modifiers use the Creeper's literal args as the v1 default (the ONE consumer wired
		// so far); a future declaration seam can carry per-goal overrides. Cite Creeper.registerGoals @3.
		return newAvoidEntityGoal(gd.avoidType, avoidDefaultMaxDist, avoidWalkSpeedModifier, avoidSprintSpeedModifier)
	case "float":
		return newFloatGoal()
	case "climb_on_powder_snow":
		// ClimbOnTopOfPowderSnowGoal(mob, level) {JUMP} — the powder-snow-walkable mob climbs on TOP of
		// powder snow instead of sinking (ai_goals_powder_snow.go). NO RNG (canUse is pure world reads,
		// tick arms the jump control). Registered on Rabbit @1, Fox @0, Silverfish @1 (jar-verified);
		// Creeper is NOT a walkable mob and does NOT register it. Cite ClimbOnTopOfPowderSnowGoal.
		return newClimbOnTopOfPowderSnowGoal()
	case "sit":
		// MOB-NEUT-01 (Phase 36): Wolf @2 SitWhenOrderedToGoal — {JUMP,MOVE}, NO RNG. Parks a tamed/
		// ordered wolf (sit subsystem).
		return newSitWhenOrderedToGoal()
	case "follow_owner":
		// MOB-NEUT-01 (Phase 36): Wolf @6 FollowOwnerGoal(this, 1.0, 10.0, 2.0) — {MOVE}, NO RNG. The
		// speed routes from the declared movement_speed (declaredWalkSpeed), the same want-multiplier the
		// melee goal uses.
		return newFollowOwnerGoal(declaredWalkSpeed(decl))
	case "owner_hurt_by":
		// MOB-NEUT-01 (Phase 36): Wolf targetSelector @1 OwnerHurtByTargetGoal — {TARGET}, NO RNG.
		return newOwnerHurtByTargetGoal()
	case "owner_hurt":
		// MOB-NEUT-01 (Phase 36): Wolf targetSelector @2 OwnerHurtTargetGoal — {TARGET}, NO RNG.
		return newOwnerHurtTargetGoal()
	case "angry_player_target":
		// MOB-NEUT-01 (Phase 36, B1): Wolf targetSelector @4 NearestAttackableTargetGoal<Player>(isAngryAt)
		// — {TARGET}, the PLAYER goal GATED on the wolf's anger (a wild un-hit wolf does NOT aggro players).
		// The bare nearest_attackable_target stays the un-gated hostile goal (UNCHANGED).
		return newAngryPlayerTargetGoal()
	case "silverfish_merge_stone":
		// MOB-HOST-05 (infest goals): Silverfish @5 SilverfishMergeWithStoneGoal — {MOVE}. The stone->
		// infested conversion goal; canUse RNG-gates (nextInt(reducedTickDelay(10))) then converts an
		// adjacent host block, falling back to a bare RandomStroll. Speed routes from movement_speed.
		return newSilverfishMergeStoneGoal(declaredWalkSpeed(decl))
	case "silverfish_wake_friends":
		// MOB-HOST-05 (infest goals): Silverfish @3 SilverfishWakeUpFriendsGoal — {} (NO flags). The
		// hurt-armed spiral that de-infests/summons nearby silverfish. notifyHurt is fired from the
		// per-type hurt hook (silverfishNotifyHurt, combat_mob.go). RNG-free ctor.
		return newSilverfishWakeFriendsGoal()
	case "skeleton_target":
		// MOB-NEUT-01 (Phase 36, B2): Wolf targetSelector @7 NearestAttackableTargetGoal<AbstractSkeleton>
		// — {TARGET}, NO anger gate (wolves attack skeletons on sight). findTarget scans entity.Skeleton.ID
		// within FOLLOW_RANGE.
		return newSkeletonTargetGoal()
	case "enderman_look_for_player":
		// MOB-HOST-08 (Task #9, gaze): EnderMan.EndermanLookForPlayerGoal — targetSelector @1, {TARGET}.
		// The REAL gaze-aggro (a player only angers the enderman by LOOKING at it, or by having already
		// angered it), REPLACING the v1 nearest_attackable_target substitution. Cite
		// EnderMan.registerGoals targetSelector @1 EndermanLookForPlayerGoal (ai_goals_enderman_gaze.go).
		return newEndermanLookForPlayerGoal()
	case "enderman_freeze_when_looked_at":
		// MOB-HOST-08 (Task #9, gaze): EnderMan.EndermanFreezeWhenLookedAt — goalSelector @1, {JUMP, MOVE}.
		// Freezes the enderman (stops its nav, stares back) while its player target is staring at it within
		// 16 blocks. Cite EnderMan.registerGoals @1 EndermanFreezeWhenLookedAt (ai_goals_enderman_gaze.go).
		return newEndermanFreezeWhenLookedAtGoal()
	default:
		return nil
	}
}

// spiderLeapYd is the vertical leap component Spider.registerGoals passes to LeapAtTargetGoal(this, 0.4)
// — the ONE caller of the leap goal in v1. Pinned here (rather than a magic literal in buildNativeGoal)
// so the kind="leap_at_target" route stays the verbatim Spider arg. Cite Spider.registerGoals @3.
const spiderLeapYd = 0.4

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
