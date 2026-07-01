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

	// targetSelector is the SECOND ported GoalSelector instance (net.minecraft.world.entity.Mob
	// .targetSelector) — the combat-targeting GoalSelector ticked BEFORE goalSelector in the
	// Mob.serverAiStep order. It is the SAME goalSelector type as goals but a fresh, INDEPENDENT
	// instance (its own lockedBy map), exactly as vanilla holds two separate GoalSelectors whose
	// lockedFlags do not share — TARGET goals (HurtByTargetGoal/NearestAttackableTargetGoal) lock the
	// TARGET flag among themselves here, MOVE/LOOK goals lock theirs in goals. buildAIFromDecl routes
	// a TARGET-flagged declared goal into this selector (plugin_mob_ai.go). A passive Pig declares ZERO
	// TARGET goals, so this selector is empty for it and its tick adds NO new RNG draw (the pig oracle
	// stays byte-identical). Cite Mob.serverAiStep (the targetSelector.tick BEFORE goalSelector.tick).
	targetSelector goalSelector

	// navigation is the mob's ported GroundPathNavigation (navigation.go, Plan 07-02): the per-
	// mob path follower. serverAiStep CONSUMES wantTarget below — when a MOVE goal sets a new
	// wantTarget, it calls navigation.requestPath (snapshot -> computePath -> Path), then
	// navigation.tick advances the active path and steps the mob via moveEntity. Tick-owned.
	navigation groundNavigation

	// wantX/wantY/wantZ are the navigation target a MOVE goal (randomStrollGoal) sets via the
	// goal's start() — the analogue of vanilla navigation.moveTo(wantedX,Y,Z). hasTarget is
	// the "a path is wanted" flag (the navigation.isDone() analogue: a stroll keeps running
	// while hasTarget is set). Plan 07-02's groundNavigation reads wantX/Y/Z + hasTarget,
	// computes a path, steps the mob, and clears hasTarget on arrival.
	wantX, wantY, wantZ float64
	hasTarget           bool
	// wantSpeed is the per-request move speed (blocks/tick) a CHASE goal sets via setWantTargetSpeed;
	// 0 means "use the navigation default amble" (pigWalkSpeed) — the stroll/passive path leaves it 0.
	wantSpeed float64

	// attackTargetID is the thin-id analogue of net.minecraft.world.entity.Mob's current attack target
	// (the Mob.getTarget() id; 0 == null/no target). The targetSelector goals (HurtByTargetGoal,
	// NearestAttackableTargetGoal) SET it on a successful acquire (their start() == Mob.setTarget); the
	// attack goals (MeleeAttackGoal) canUse-gate on it being non-zero. It lives here on mobAI alongside
	// the wantX/Y/Z nav-want fields (35-CONTEXT:74): AI state, tick-owned, single-owner (TICK-05). A
	// THIN id (never a live *Entity / *tickPlayer pointer — the Folia rule, mirroring damageSource
	// .attacker). For a v1 player target it carries the player's entity id. Cite Mob.getTarget/setTarget.
	attackTargetID int32

	// wantCands / hasWantCands are the Phase-30.1 stroll candidate carrier (CONTEXT <decisions>
	// architecture split). The stroll goal's start() emits 10 RAW candidate offsets here via
	// setWantCandidates (the RandomPos.generateRandomPos supplier results); the RNG-free runtime
	// snap (snapStrollWant in serverAiStep) validates + ground-snaps them to the first reachable
	// walkable column and commits the winner via setWantTarget — so hasWantCands does NOT set
	// hasTarget (the snap does, only on a valid commit). Fixed 10 (the for-i<10 supplier loop).
	// Tick-owned (TICK-05). The snap is RNG-free, so it is identical for the Go-native and plugin
	// pig and the bit-fragile pig oracle stays byte-identical.
	wantCands    [10][3]float64
	hasWantCands bool
	// wantLandMode selects the snap validation (jar-verified, see ai_goals_passive.go getPosition):
	// true => the COMMON (~99.9%, nextFloat() >= probability) LandRandomPos.getPos path (validate
	// isOutsideLimits/isRestricted/isNotStable, THEN moveUpOutOfSolid, THEN isWater/hasMalus); false =>
	// the RARE (~0.1%, nextFloat() < probability) DefaultRandomPos.getPos path (validate isOutsideLimits/
	// isRestricted/isNotStable/hasMalus, NO up-snap, NO water). Set by the stroll goal's probability
	// nextFloat() draw (CONTEXT split). CONSUMED-AS-LAND today: snapStrollWant always applies the
	// LandRandomPos (up-snap) validation — see its doc — so the rare DefaultRandomPos no-up-snap branch
	// is a deferred-but-correctly-computed flag, NOT silently ignored.
	wantLandMode bool

	// rng is the per-mob seeded RandomSource (ai_random.go) — the Mob.getRandom() analogue every
	// ported goal draws from (canUse's chance roll, getPosition's offset, start's lookTime). It
	// REPLACES the shared package math/rand/v2 the goals used before, making the AI 1:1-faithful
	// (the vanilla draw ORDER) AND deterministic for a fixed seed (the TestTickAIDrivesMobs flake
	// fix). Tick-owned (TICK-05): created at AI build, drawn only on the tick goroutine. Never nil
	// for a goal-bearing mob (newPigAI / buildAIFromDecl always set it).
	rng *entityRandom

	// jumpControl is the mob's ported net.minecraft.world.entity.ai.control.JumpControl (MOB-SUB-04).
	// A goal claiming the JUMP flag calls jumpControl.doJump() (the JumpControl.jump() analogue), and
	// the serverAiStep JUMP slot runs jumpControl.tick(e) AFTER navigation.tick (jar order) — which
	// pushes the armed flag into e.jumping (setJumping) then clears it. PLAIN VALUE, RNG-FREE,
	// tick-owned: the slot draws no random (the only new RNG in this subsystem is FloatGoal.tick in
	// Plan 03, inside the goal callback). Cite JumpControl.
	jumpControl jumpControl

	// noJumpDelay is net.minecraft.world.entity.LivingEntity.noJumpDelay — the per-mob land-jump
	// rate-limiter. The aiStep jump branch sets it to 10 after a jumpFromGround (so a mob jumps at
	// most once per ~10 ticks on land), and it is decremented toward 0 at the TOP of each serverAiStep
	// step (clamped at 0, never negative). A plain int, RNG-FREE, tick-owned. Cite LivingEntity.aiStep
	// (the `if (noJumpDelay > 0) noJumpDelay--;` at the top + the `noJumpDelay = 10` after a land jump).
	noJumpDelay int
}

// jumpControl is the ported net.minecraft.world.entity.ai.control.JumpControl — the per-mob jump
// coalescer. A goal claiming the JUMP flag calls doJump() (arming the flag); the serverAiStep JUMP
// slot calls tick(e) once per tick, which pushes the flag into the mob (setJumping) then clears it.
// Repeated doJump() calls within a tick are idempotent (coalesce to one bool) — the T-30-06 DoS
// bound. PLAIN VALUE, RNG-FREE, tick-owned (TICK-05).
//
//	[VERIFIED javap JumpControl: jump(){ this.jump = true; }  tick(){ mob.setJumping(jump); jump = false; }.]
type jumpControl struct {
	// jump is JumpControl.jump — the "a goal wants a jump this tick" flag. Armed by doJump(), consumed
	// + cleared by tick(). Plain bool.
	jump bool
}

// doJump is the JumpControl.jump() analogue: arm the jump flag. Named doJump (not jump) to avoid
// colliding with the `jump` field. A goal's tick callback / nav.jump() calls it.
//
//	[VERIFIED javap JumpControl.jump(): iconst_1; putfield jump:Z — `this.jump = true;`.]
func (j *jumpControl) doJump() { j.jump = true }

// tick is the JumpControl.tick() analogue: push the armed flag into the mob (setJumping) then clear
// it, so a jump intent lasts exactly one tick. Runs in the serverAiStep JUMP slot after navigation.
//
//	[VERIFIED javap JumpControl.tick(): mob.setJumping(jump); this.jump = false;.]
func (j *jumpControl) tick(e *Entity) {
	e.setJumping(j.jump)
	j.jump = false
}

// setWantTarget records a navigation target (the randomStrollGoal start() seam). Setting a
// target is the v1 stand-in for navigation.moveTo; 07-02 consumes it. The want SPEED is left at the
// navigation's default amble (pigWalkSpeed) — the stroll/passive pace the pig oracle pins.
func (m *mobAI) setWantTarget(x, y, z float64) {
	m.wantX, m.wantY, m.wantZ = x, y, z
	m.hasTarget = true
	m.wantSpeed = 0 // 0 == "use the navigation default" (pigWalkSpeed); see serverAiStep's speed route
}

// setWantTargetSpeed is setWantTarget carrying an explicit per-request move speed (blocks/tick) — the
// navigation.moveTo(target, speedModifier) overload a CHASE goal uses. The melee goal passes its
// speedModifier-scaled chase speed so a hunting zombie moves at its real pace, not the passive amble.
// A 0 speed means "use the navigation default" (the pig stroll path, oracle-pinned, never routes here).
func (m *mobAI) setWantTargetSpeed(x, y, z, speed float64) {
	m.wantX, m.wantY, m.wantZ = x, y, z
	m.hasTarget = true
	m.wantSpeed = speed
}

// clearWantTarget drops the navigation target (the navigation.stop() seam, used by the stroll
// goal's stop()). With no navigation yet, "done" simply means no target is pending.
func (m *mobAI) clearWantTarget() { m.hasTarget = false }

// getTarget is the Mob.getTarget() id analogue: the current attack-target entity id (0 == no
// target). The attack goals (MeleeAttackGoal.canUse) read it; the targetSelector goals set it
// via setTarget. Cite Mob.getTarget.
func (m *mobAI) getTarget() int32 { return m.attackTargetID }

// setTarget is the Mob.setTarget(LivingEntity) id analogue: record the acquired attack target's
// entity id (0 clears it). The targetSelector goals' start() call it (NearestAttackableTargetGoal
// .start = mob.setTarget(target); HurtByTargetGoal.start = mob.setTarget(getLastHurtByMob())).
// Cite Mob.setTarget.
func (m *mobAI) setTarget(id int32) { m.attackTargetID = id }

// setWantCandidates records the 10 RAW stroll candidates the goal emitted (RandomPos.generateRandomPos's
// supplier results — BlockPos.containing(xt+x, yt+y, zt+z), NOT yet ground-snapped). It does NOT set
// hasTarget — the RNG-free runtime snap (snapStrollWant, serverAiStep) validates + ground-snaps them to
// the first reachable walkable column and commits the winner via setWantTarget. Per the Phase-30.1
// architecture split (CONTEXT <decisions>): the goal/.star draw only the direction (the per-mob RNG is
// the single lockstep source); the shared Go runtime owns the world reads. Fixed 10 candidates (the
// generateRandomPos loop is for i<10). Tick-owned (TICK-05). Both the Go-native pig's start() and the
// plugin pig's overloaded path_to(31 floats = 10 candidates + landMode) reach this setter with the SAME
// wantLandMode for the same probability roll, so both pigs run the SAME snap with identical state.
func (m *mobAI) setWantCandidates(c [10][3]float64, landMode bool) {
	m.wantCands = c
	m.wantLandMode = landMode
	m.hasWantCands = true
}

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
	// MOB-SUB-04 — the noJumpDelay decrement at the TOP of LivingEntity.aiStep (`if (noJumpDelay > 0)
	// noJumpDelay--;`). It is PURE INTEGER MATH (no RNG draw), so it cannot perturb the per-mob RNG
	// stream the pig oracle pins. Clamped at 0 — never negative.
	//	[VERIFIED javap LivingEntity.aiStep top: getfield noJumpDelay; ifle skip; iconst_1; isub;
	//	 putfield noJumpDelay — i.e. `if (noJumpDelay > 0) noJumpDelay--;`.]
	if m.noJumpDelay > 0 {
		m.noJumpDelay--
	}

	// (sensing.tick — skipped: the v1 goals probe the world directly in their canUse.)
	// Mob.serverAiStep order (bytecode-confirmed, the file header doc lines 7-8): the targetSelector
	// (combat-target goals) ticks BEFORE the goalSelector (action goals), then BOTH explicit
	// tickRunningGoals run in the same order. Each goalSelector.tick already runs its OWN
	// tickRunningGoals at its tail (ai_goal.go), and the existing single-selector code then called an
	// explicit tickRunningGoals AFTER tick — matching vanilla's goalSelector.tick() then
	// tickRunningGoals(canSimulate). The SAME shape is applied to BOTH selectors here. For a passive
	// Pig the targetSelector is empty (zero TARGET goals declared), so its tick/tickRunningGoals are
	// no-ops that draw NO RNG — the pig oracle stays byte-identical.
	// jar order: target goals FIRST (combat targeting), then the action goals. Each goalSelector.tick()
	// ENDS WITH its own tickRunningGoals(true) (the last line of vanilla GoalSelector.tick — javap-
	// confirmed), so calling tick() already ticks every running goal exactly ONCE. The earlier code ALSO
	// called an explicit tickRunningGoals(true) after each tick() — that DOUBLE-ticked every running goal
	// per serverAiStep (e.g. MeleeAttackGoal decremented ticksUntilNextAttack twice + checkAndPerformAttack
	// ran twice → the zombie attacked ~2× too fast). In vanilla, tick() and the explicit tickRunningGoals
	// are MUTUALLY EXCLUSIVE per the Mob.serverAiStep (tickCount+id)%2 decimation: even-tick → tick()
	// (which internally ticks once); odd-tick → tickRunningGoals(false). Our driver runs every tick with no
	// decimation, so the faithful single-tick-per-step is just tick() alone (its built-in tickRunningGoals).
	//	[VERIFIED javap GoalSelector.tick: ... ; this.tickRunningGoals(true);  // the final line.
	//	 Mob.serverAiStep: if ((tickCount+id)%2==0) goalSelector.tick(); else goalSelector.tickRunningGoals(false).]
	m.targetSelector.tick(t, e) // target goals (combat targeting) — ticks its running goals once at its tail
	m.goals.tick(t, e)          // action goals — ticks its running goals once at its tail

	// Phase 30.1 — the RNG-FREE stroll snap: a MOVE goal (Go stroll start() or the plugin pig's
	// overloaded path_to(31 floats: 10 candidates + landMode)) emitted 10 RAW candidates this tick (hasWantCands). Validate +
	// ground-snap them to the first reachable walkable column (RandomPos.generateRandomPos first-valid,
	// LandRandomPos.getPos snap) and commit the winner — or leave hasTarget false if none survive
	// (vanilla generateRandomPos null). This draws ZERO randoms, so it does not perturb the per-mob RNG
	// stream the pig oracle pins, and it runs BEFORE requestPath so the floored want fed to the A* (and
	// the wantX/Y/Z the oracle observes) is the SNAPPED reachable column — the wedge-bug root-cause fix.
	if m.hasWantCands {
		if wx, wy, wz, ok := m.snapStrollWant(t, e); ok {
			m.setWantTarget(wx, wy, wz) // commit the snapped, reachable target (sets hasTarget)
		} else {
			m.hasTarget = false // no valid candidate (generateRandomPos null) — no want this roll
		}
		m.hasWantCands = false // consumed
	}

	// navigation (Plan 07-02): consume the wantTarget a MOVE goal set this/last tick. When the
	// goal wants a (new) target, ask the navigation to (re)compute a path — throttled by
	// shouldRecomputePath so an unreachable target cannot flood the A* (Pitfall 6 / T-7-04).
	// The target is the floor block under the wanted position (the A* works in block coords).
	if m.hasTarget {
		// Route the want SPEED into the navigation: a CHASE goal (setWantTargetSpeed) sets a faster pace
		// than the passive amble; the stroll/passive path leaves wantSpeed 0 → keep the default (the pig
		// oracle's pinned pigWalkSpeed). navigation.moveTo(target, speedModifier) — the speed half.
		if m.wantSpeed > 0 {
			m.navigation.speed = m.wantSpeed
		} else {
			m.navigation.speed = pigWalkSpeed
		}
		tx, ty, tz := floorI(m.wantX), floorI(m.wantY), floorI(m.wantZ)
		if m.navigation.shouldRecomputePath(tx, ty, tz) {
			m.navigation.requestPath(t, e, tx, ty, tz) // snapshot -> computePath -> Path (the seam)
		}
	}
	// Advance the active path one step (Pattern 3: desired Δ -> the EXISTING moveEntity, which
	// re-buckets; the unchanged tracker auto-broadcasts). Runs inline on the tick (TICK-05).
	m.navigation.tick(t, e)

	// MOB-SUB-04 — the JUMP slot, in the jar-confirmed Mob.serverAiStep order: moveControl/lookControl
	// (folded into navigation.tick's yaw + moveEntity above) THEN jumpControl.tick. jumpControl.tick
	// pushes the goal-armed jump flag into e.jumping (setJumping) then clears it; entityJumpStep is the
	// LivingEntity.aiStep jump branch that consumes e.jumping into the real vy impulse. BOTH are PURE
	// (no RNG draw) — the slot cannot perturb the pig oracle's RNG stream; the only new RNG is
	// FloatGoal.tick (Plan 03), confined to the goal callback above. The impulse lands HERE, in tickAI,
	// BEFORE tickPhysics integrates gravity (tick_phases.go: tickAI → tickPhysics), so the jump is not
	// cancelled the same tick.
	m.jumpControl.tick(e)
	t.entityJumpStep(e)
}

// newPigAI builds the Pig AI: FloatGoal@0 (Phase 30-03) + PanicGoal@1 (Phase 31-01) plus the three
// "visibly alive" passive goals, registered at the EXACT priorities read from javap animal.pig.Pig
// .registerGoals.
//
//	0  FloatGoal(mob)                           -> floatGoal           [JUMP]
//	1  PanicGoal(mob, 1.25)                      -> panicGoal           [MOVE]
//	3  BreedGoal(mob, 1.0)                        -> breedGoal           [MOVE|LOOK]
//	4  TemptGoal(mob, 1.2, …) ×2                 -> temptGoal           [MOVE|LOOK]
//	5  FollowParentGoal(mob, 1.1)                -> followParentGoal    [] (EMPTY)
//	6  WaterAvoidingRandomStrollGoal(mob, 1.0)  -> randomStrollGoal  [MOVE]
//	7  LookAtPlayerGoal(mob, Player, 6.0)       -> lookAtPlayerGoal  [LOOK]
//	8  RandomLookAroundGoal(mob)                -> randomLookAroundGoal [MOVE|LOOK]
//
// FloatGoal@0 is the JUMP-flag consumer (MOB-SUB-04): in water/lava its canUse is true and tick()
// draws nextFloat()<0.8 → jumpControl.doJump → the serverAiStep JUMP slot's +0.04 swim impulse keeps
// the pig afloat. PanicGoal@1 is the MOB-GATE-01 flee consumer: on a panic_causes hit its canUse draws
// the DefaultRandomPos(5,4) flee selection and routes 10 candidates through the SAME
// setWantCandidates -> snapStrollWant path (preempting stroll@6's MOVE flag, priority 1 < 6). Both are
// added in LOCKSTEP with the plugin (plugins/vanilla_pig/main.star) in their own plan (the oracle
// contract — never split). FloatGoal's ctor mob.getNavigation().setCanFloat(true) is applied below
// (navigation.canFloat = true).
//
// TemptGoal@4 ×2 (carrot_on_a_stick literal + pig_food tag, speed 1.2, canScare=false) is WIRED
// (Phase 32 — the S4 held-item read landed). BreedGoal@3 + FollowParentGoal@5 are now PORTED
// (Phase 33 — the aging + breeding subsystem landed: Plan 01 aging/half-scale hitbox, Plan 02
// inLove/feed, this plan the two goals). The Go-native pig now has all 9 goals {0,1,3,4,4,5,6,7,8}.
//
// LOCKSTEP NOTE (the oracle contract): this plan adds BreedGoal@3 + FollowParentGoal@5 to the
// GO-NATIVE pig ONLY (the C1/C2 split). Until 33-04 mirrors them onto vanilla_pig/main.star, the Go
// pig has 9 goals and the plugin pig has 7 — so the plugin-vs-native gate (TestPluginPigEqualsGoNativePig)
// diverges by the two added goals. That divergence is EXPECTED and is closed by 33-04 (the .star
// mirror) + 33-05 (the full 9v9 gate). The breed/follow RNG draws (variant nextBoolean() + XP
// nextInt(7)) fire ONLY mid-breeding — dormant on the un-fed lone-adult oracle pig.
func newPigAI() *mobAI {
	m := &mobAI{}
	// Per-mob seeded RandomSource (the Mob.getRandom() analogue) — deterministic for the default
	// seed; spawn sites may reseed per entity id (reseedMobAI) for per-mob variety. This is the
	// determinism fix (TestTickAIDrivesMobs) AND the 1:1 faithful draw-order source.
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	// navigation.speed is the mob's walk speed in blocks/tick (the stroll speedModifier 1.0
	// scaled to a vanilla-ish ground speed). A Pig's movement speed attribute ≈ 0.25, walk pace
	// ≈ 0.1-0.2 blocks/tick; v1 uses 0.15 for a visibly-alive amble (the tunable knob, like the
	// physics constants — wire-irrelevant, gated by the real-client visual check).
	m.navigation.speed = pigWalkSpeed
	// FloatGoal ctor: mob.getNavigation().setCanFloat(true) — the mob may path over water (the float
	// PATHING node-evaluator behavior is deferred + cited on the field; the flag set is the 1:1 port).
	m.navigation.canFloat = true
	// @0 FloatGoal [JUMP] — added FIRST (priority 0 = highest precedence: it runs first in the goal
	// walk, ai_goal.go "smaller priority = higher"). LOCKSTEP with vanilla_pig/main.star's @0 FloatGoal.
	// Cite Pig.registerGoals @0 FloatGoal (javap: iconst_0; new FloatGoal; FloatGoal.<init>).
	m.goals.addGoal(0, newFloatGoal())
	// @1 PanicGoal(mob, 1.25) [MOVE] — the MOB-GATE-01 flee consumer, preempting stroll@6's MOVE flag
	// (priority 1 < 6). LOCKSTEP with vanilla_pig/main.star's @1 PanicGoal. Cite Pig.registerGoals @1
	// PanicGoal (javap: iconst_1; new PanicGoal; ldc2_w 1.25d; PanicGoal.<init>(PathfinderMob, double)).
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	// @3 BreedGoal(mob, 1.0) [MOVE, LOOK] — the GO-NATIVE breed seeker (Phase 33). canUse gates on
	// isInLove (dormant on the un-fed oracle pig), then getFreePartner scans the same-region store for
	// the nearest in-love non-panicking same-class partner; tick navigates + courts + breed()s. LOCKSTEP
	// with vanilla_pig/main.star's @3 BreedGoal — added there in 33-04 (the C1/C2 split; the Go pig leads).
	// Cite Pig.registerGoals @3 BreedGoal (javap: iconst_3; new BreedGoal; dconst_1 1.0d; BreedGoal.<init>).
	m.goals.addGoal(3, newBreedGoal(1.0))
	// @4 TemptGoal ×2 [MOVE, LOOK] — Pig.registerGoals adds two: the CARROT_ON_A_STICK literal FIRST,
	// then the PIG_FOOD tag, both speed 1.2, canScare=false (32-CONTEXT.md, javap Pig.registerGoals).
	// Carrot MUST be added first: addGoal's insertion-sort keeps it before pig_food among the equal-
	// priority @4 pair, so the carrot goal wins the shared {MOVE,LOOK} flags (the faithful "first-added
	// wins" arbitration — ai_goal.go canBeReplacedBy needs other.priority < this.priority, 4<4 false).
	// LOCKSTEP with vanilla_pig/main.star's two @4 TemptGoals (the oracle contract).
	m.goals.addGoal(4, newTemptGoal(1.2, func(id int32) bool { return id == 887 }, false))                  // Items.CARROT_ON_A_STICK (id 887), canScare=false
	m.goals.addGoal(4, newTemptGoal(1.2, func(id int32) bool { return itemInTag(id, "pig_food") }, false)) // ItemTags.PIG_FOOD, canScare=false
	// @5 FollowParentGoal(mob, 1.1) [] EMPTY flags — the GO-NATIVE baby follower (Phase 33). canUse gates
	// on isBaby (false on the adult oracle), then trails the nearest adult same-class; NO RNG, EMPTY flags
	// (the ctor never setFlags, so it never locks MOVE/LOOK — the selector handles an empty-flag goal,
	// ai_goal.go). LOCKSTEP with vanilla_pig/main.star's @5 FollowParentGoal — added there in 33-04.
	// Cite Pig.registerGoals @5 FollowParentGoal (javap: iconst_5; new FollowParentGoal; ldc2_w 1.1d;
	// FollowParentGoal.<init>(Animal, double)).
	m.goals.addGoal(5, newFollowParentGoal(1.1))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
	m.goals.addGoal(7, newLookAtPlayerGoal(6.0))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// pigWalkSpeed is the v1 Pig's path-following speed in blocks/tick (a tunable, wire-irrelevant
// value — the stroll goal's speedModifier 1.0 mapped to a vanilla-ish ground pace).
const pigWalkSpeed = 0.15

// reseedMobAI derives a per-entity deterministic seed from the entity id and reseeds the mob's
// RandomSource, so each spawned mob has its own reproducible stream (the per-entity getRandom()
// analogue). Called at spawn after e.ai is attached. A nil rng (a mob built before this plan, or a
// non-goal mob) is created on demand so the call is always safe. Tick-owned (TICK-05).
func reseedMobAI(m *mobAI, id int32) {
	if m == nil {
		return
	}
	// Mix the id into a 64-bit seed (uint32 widen + the nothing-up-my-sleeve constant) so adjacent
	// ids give well-separated streams. Deterministic for a fixed id.
	seed := uint64(uint32(id)) ^ defaultEntityRandomSeed
	if m.rng == nil {
		m.rng = newEntityRandom(seed)
		return
	}
	m.rng.reseed(seed)
}
