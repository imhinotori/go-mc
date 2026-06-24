package server

// ai_mob.go — AI-01: per-mob AI state + the serverAiStep-order driver.
//
// PORTED (the STANDING MANDATE) from the unobfuscated 26.2 jar (javap, this session):
//   - net.minecraft.world.entity.Mob.serverAiStep  — the tick ORDER (bytecode-confirmed):
//       sensing.tick -> targetSelector.tick -> goalSelector.tick
//         -> targetSelector.tickRunningGoals(true) -> goalSelector.tickRunningGoals(true)
//         -> navigation.tick -> customServerAiStep -> moveControl/lookControl/jumpControl.
//     For a v1 PASSIVE Pig there is no targetSelector (no attack targets) and no sensing
//     beyond the goals' own probes, so the driver runs goalSelector.tick THEN
//     goalSelector.tickRunningGoals. navigation.tick + the move/look controls are added in
//     Plan 07-02 (a goal here only SETS a target; it never moves the mob).
//   - net.minecraft.world.entity.animal.pig.Pig.registerGoals — the exact passive goal set +
//     priorities (the MOVED package animal/pig/Pig.class):
//       0 FloatGoal, 1 PanicGoal(1.25), 3 BreedGoal(1.0), 4 TemptGoal(1.2, PIG_FOOD)×2,
//       5 FollowParentGoal(1.1), 6 WaterAvoidingRandomStrollGoal(1.0),
//       7 LookAtPlayerGoal(Player, 6.0), 8 RandomLookAroundGoal.
//     v1 ports the three "visibly alive" passive goals (stroll@6, lookAtPlayer@7,
//     lookAround@8); Float/Panic/Breed/Tempt/FollowParent are DEFERRED (no water hazard,
//     damage source, breeding, or items in v1 — see the deferral note below).
//
// SINGLE-OWNER (TICK-05): mobAI is tick-owned game state mutated ONLY on the tick goroutine.
// No goroutine, no xsync — plain Go.

// mobAI is the per-mob AI state that hangs off an Entity (entity.ai). It holds the mob's
// goalSelector and the navigation/look TARGETS a goal writes — the wantTarget that Plan
// 07-02's navigation will CONSUME (this plan only SETS it; it never moves the mob).
type mobAI struct {
	// goals is the mob's ported GoalSelector (ai_goal.go). serverAiStep drives it.
	goals goalSelector

	// wantX/wantY/wantZ are the navigation target a MOVE goal (randomStrollGoal) sets via the
	// goal's start() — the analogue of vanilla navigation.moveTo(wantedX,Y,Z). hasTarget is
	// the "a path is wanted" flag (the navigation.isDone() analogue: a stroll keeps running
	// while hasTarget is set). Plan 07-02's groundNavigation reads wantX/Y/Z + hasTarget,
	// computes a path, steps the mob, and clears hasTarget on arrival. THIS plan only SETS
	// them; nothing consumes them yet.
	wantX, wantY, wantZ float64
	hasTarget           bool
}

// setWantTarget records a navigation target (the randomStrollGoal start() seam). Setting a
// target is the v1 stand-in for navigation.moveTo; 07-02 consumes it.
func (m *mobAI) setWantTarget(x, y, z float64) {
	m.wantX, m.wantY, m.wantZ = x, y, z
	m.hasTarget = true
}

// clearWantTarget drops the navigation target (the navigation.stop() seam, used by the stroll
// goal's stop()). With no navigation yet, "done" simply means no target is pending.
func (m *mobAI) clearWantTarget() { m.hasTarget = false }

// serverAiStep drives one AI step for the mob, in the jar-confirmed Mob.serverAiStep ORDER
// (minus the v1-skipped targetSelector + sensing, and minus navigation/controls which land in
// Plan 07-02). It runs goalSelector.tick (start/stop goals by priority + flag locks, which
// internally ticks running goals once) then an explicit goalSelector.tickRunningGoals(true) —
// matching vanilla, which calls goalSelector.tick() and then tickRunningGoals(canSimulate)
// for the goal selector. Runs on the tick goroutine over tick-owned mob state (TICK-05).
//
// targetSelector is intentionally SKIPPED: a v1 passive Pig has no attack-target goals, so
// there is no second GoalSelector to tick (07-RESEARCH AI-01 row 5). Plan 07-03 calls this
// from the tickAI() slot for every AI mob; this plan delivers the driver, not the call site.
func (m *mobAI) serverAiStep(t *TickLoop, e *Entity) {
	// (sensing.tick — skipped: the v1 goals probe the world directly in their canUse.)
	// (targetSelector.tick + tickRunningGoals — skipped: no attack targets for a passive Pig.)
	m.goals.tick(t, e)                      // start/stop goals by priority + per-flag locking
	m.goals.tickRunningGoals(t, e, true)    // tick every running goal (canSimulate = true)
	// (navigation.tick — Plan 07-02 consumes wantTarget and steps the mob via moveEntity.)
	// (moveControl/lookControl/jumpControl.tick — Plan 07-02.)
}

// newPigAI builds the v1 passive Pig AI: the three "visibly alive" goals registered at the
// EXACT priorities read from javap animal.pig.Pig.registerGoals.
//
//	6  WaterAvoidingRandomStrollGoal(mob, 1.0)  -> randomStrollGoal  [MOVE]
//	7  LookAtPlayerGoal(mob, Player, 6.0)       -> lookAtPlayerGoal  [LOOK]
//	8  RandomLookAroundGoal(mob)                -> randomLookAroundGoal [MOVE|LOOK]
//
// DEFERRED for v1 (documented, faithful-scope): FloatGoal@0 (no water hazard), PanicGoal@1
// (no damage source), BreedGoal@3 + FollowParentGoal@5 + TemptGoal@4 (no breeding/items).
// They are added when their preconditions exist (water, combat, items). The v1 set is the
// faithful PASSIVE-AMBIENT subset that makes a Pig amble + look around exactly like vanilla.
func newPigAI() *mobAI {
	m := &mobAI{}
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
	m.goals.addGoal(7, newLookAtPlayerGoal(6.0))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}
