package server

// ai_mob.go — AI-01: per-mob AI state + the serverAiStep-order driver.
//
// PORTED (the STANDING MANDATE) from the unobfuscated 26.2 jar (javap, this session):
//   - net.minecraft.world.entity.Mob.serverAiStep  — the tick ORDER + DECIMATION (bytecode-confirmed):
//       sensing.tick (every tick) -> the (tickCount+id)%2 goal-DECIMATION branch (C-1):
//         FULL phase  ((i%2==0) || tickCount<=1): targetSelector.tick()  -> goalSelector.tick()
//         LIGHT phase (i%2!=0 && tickCount>1):    targetSelector.tickRunningGoals(false) -> goalSelector.tickRunningGoals(false)
//       -> navigation.tick -> customServerAiStep -> moveControl/lookControl/jumpControl (all EVERY tick).
//     So goal START/STOP re-eval + canUse (and any RNG canUse draws) run every OTHER tick, while
//     movement/controls run full-rate. A running goal ticks on FULL phases via tick()'s tail
//     tickRunningGoals(true); on LIGHT phases only if it requiresUpdateEveryTick() (e.g. @8
//     RandomLookAroundGoal). See serverAiStep below for the exact javap condition + the (C-1) cite.
//     For a v1 PASSIVE Pig the targetSelector is empty (no attack goals), so its branch is a no-op.
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

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

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

	// sense is the mob's ported net.minecraft.world.entity.ai.sensing.Sensing (sensing.go): the
	// per-tick line-of-sight memo the attack/target/ranged goals consult before firing (divergence
	// C-4). Zero value is valid (lazily initialized on the first hasLineOfSight query); a pig never
	// queries it, so it stays untouched on the pig oracle path. Tick-owned (TICK-05).
	sense sensing

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
	// wantSpeed is the per-request move speed (blocks/tick) a CHASE goal sets via setWantTargetSpeed
	// (the FINAL getSpeed = speedModifier x MOVEMENT_SPEED, computed by the caller); 0 means "use the
	// stroll path" whose speed is wantSpeedMod x MOVEMENT_SPEED (the setWantTarget/setWantCandidates seam).
	wantSpeed float64
	// wantSpeedMod is the vanilla navigation.moveTo(target, speedModifier) UNITLESS modifier carried by
	// the setWantTarget/setWantCandidates (stroll/panic/breed/follow) path — the MOVEMENT_SPEED multiply
	// happens ONCE at the MoveControl.tick seam (serverAiStep), mirroring MoveControl.tick's
	// setSpeed(speedModifier x getAttributeValue(MOVEMENT_SPEED)). Default 1.0 (RandomStrollGoal's 1.0).
	// This replaces the fabricated pigWalkSpeed constant so an idle mob strolls at its real
	// MOVEMENT_SPEED (a pig 0.25, a zombie 0.23) instead of a hardcoded 0.15. Cite MoveControl.tick.
	wantSpeedMod float64

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
	// wantCandsSpeedMod is the vanilla navigation.moveTo(pos, speedModifier) modifier the stroll/panic
	// goal carries alongside its candidates (RandomStrollGoal 1.0, PanicGoal 1.25). snapStrollWant
	// commits the winning candidate at this modifier so an idle stroll and a panic flee move at their
	// distinct real paces (modifier x MOVEMENT_SPEED), not a single fabricated constant. Default 1.0.
	wantCandsSpeedMod float64
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

	// aiTickCount is the mob's net.minecraft.world.entity.Entity.tickCount as read by
	// Mob.serverAiStep for the AI tick-DECIMATION gate (C-1). Vanilla increments Entity.tickCount
	// once per Entity.tick() (in baseTick, BEFORE aiStep -> serverAiStep), so by the time
	// serverAiStep reads it the counter has already advanced this tick. We increment aiTickCount at
	// the TOP of serverAiStep (before the decimation read) to reproduce that ordering exactly: a
	// freshly built mob's first serverAiStep sees aiTickCount==1 (vanilla: tickCount==1 after the
	// first baseTick), which the `tickCount > 1` guard turns into a FULL goal re-eval on tick 1.
	// The decimation is `i = tickCount + getId(); if (i%2 != 0 && tickCount > 1)` — so the phase is
	// the mob's OWN tickCount (NOT the world gametime): a mob spawned mid-game starts its cadence
	// from 1, exactly like vanilla. PURE INTEGER MATH (no RNG draw), tick-owned (TICK-05). Cite
	// Mob.serverAiStep (Entity.tickCount + getId()) % 2.
	aiTickCount int

	// noActionTime is net.minecraft.world.entity.Mob.noActionTime — the "how long since a player was
	// near" counter that gates the random despawn (checkDespawn: `noActionTime > 600 &&
	// random.nextInt(800) == 0 && ...`). Incremented by 1 at the top of Mob.serverAiStep and reset to 0
	// by checkDespawn whenever a player is within noDespawnDistance² (or the mob is persistence-required).
	// A plain int, RNG-FREE, tick-owned. Cite Mob.serverAiStep (`++noActionTime`) + Mob.checkDespawn.
	noActionTime int

	// persistenceRequired is net.minecraft.world.entity.Mob.persistenceRequired — the "never despawn"
	// flag (name-tagged, bucketed, /summon Persistent, spawner-forbidden). checkDespawn returns early
	// (resetting noActionTime) when it is set. v1 has no source that SETS it yet (no name tags / buckets),
	// so it defaults false — but the read is the genuine isPersistenceRequired() so a future setter slots
	// in with no call-site change. Cite Mob.isPersistenceRequired.
	persistenceRequired bool

	// rabbit holds the per-mob Rabbit hop state machine (RabbitJumpControl + RabbitMoveControl +
	// Rabbit.aiStep/customServerAiStep counters). NON-NIL only for a rabbit (rabbitAiStep lazily
	// allocates it, gated on typ == entity.Rabbit.ID); nil for every other mob so the pig oracle stream
	// is untouched. Cite net.minecraft.world.entity.animal.rabbit.Rabbit. See ai_goals_rabbit.go.
	rabbit *rabbitHopState

	// silverfishLookForFriends is the per-mob analogue of Silverfish$SilverfishWakeUpFriendsGoal's
	// `int lookForFriends` field (MOB-HOST-05 infest goals). It lives here (not on the goal struct) so
	// the hurt-path hook (silverfishNotifyHurt, combat_mob.go) can arm it — the goal itself is stateless.
	// notifyHurt sets it to adjustedTickDelay(20) (once, only when it is 0); the wake goal's canUse gates
	// on it > 0 and its tick decrements it, firing the spiral at <= 0. A plain int, tick-owned, silverfish-
	// only (every other mob leaves it 0 — a zero-cost skip; the pig oracle is unperturbed). Cite
	// Silverfish$SilverfishWakeUpFriendsGoal.lookForFriends.
	silverfishLookForFriends int

	// --- RAID membership (Raider fields, RAID subsystem — raid.go/raids.go) ------------------
	//
	// currentRaid is Raider.currentRaid: the back-pointer to the Raid this mob was spawned into (nil
	// for a mob not in a raid). Raider.hasActiveRaid() reads currentRaid != nil && currentRaid.isActive()
	// (witchHasActiveRaid, ai_goals_witch.go). raidJoinRaid sets it; raidRemoveRaider/stop clears it.
	// A pointer to the Raid (coordinator-owned, single-owner TICK-05) — set/read only on the tick.
	currentRaid *Raid
	// raidWave is Raider.wave (the 1-based group number); canJoinRaid is Raider.canJoinRaid;
	// ticksOutsideRaid is Raider.ticksOutsideRaid (the strayed-out counter). Plain ints/bool, tick-owned,
	// zero for a non-raider. Cite Raider (setWave/setCanJoinRaid/setTicksOutsideRaid).
	raidWave         int
	canJoinRaid      bool
	ticksOutsideRaid int

	// --- PATROL state (PatrollingMonster fields — ai_goals_patrol.go) -------------------------
	//
	// patrolling is PatrollingMonster.patrolling; patrolLeader is PatrollingMonster.patrolLeader;
	// patrolTarget{X,Y,Z} + patrolHasTarget are the @Nullable BlockPos patrolTarget; patrolCooldownUntil
	// is LongDistancePatrolGoal.cooldownUntil (init -1 conceptually; 0 here == "never on cooldown" since
	// gameTime starts at 0 and the check is gameTime < cooldownUntil). As of the RAIDER task the 4
	// PatrollingMonsters (Pillager/Vindicator/Evoker/Ravager) carry these fields; they stay zero until a
	// PatrolSpawner sets patrolling=true (cite-deferred), so a raid-spawned raider stays a hunter, not a
	// patroller — faithful. Cite PatrollingMonster / LongDistancePatrolGoal.
	patrolling          bool
	patrolLeader        bool
	patrolHasTarget     bool
	patrolTargetX       int
	patrolTargetY       int
	patrolTargetZ       int
	patrolCooldownUntil int64

	// malus is the mob ported net.minecraft.world.entity.Mob per-mob pathfinding-malus map
	// (node_evaluator.go mobMalus): a sparse override of the PathType defaults. Animal sets FIRE_IN_NEIGHBOR
	// 16 / FIRE -1 (both == the PathType defaults, so no observable change — structured so a real
	// override lands per-mob). navigation.requestPath threads a COPY into the A* request so the off-tick
	// pathfinder re-costs WATER/LAVA/FIRE nodes per this mob. Tick-owned; the zero value is pure defaults.
	// Cite Mob.getPathfindingMalus/setPathfindingMalus.
	malus mobMalus
}

// hasPatrolTarget ports PatrollingMonster.hasPatrolTarget: patrolTarget != null.
func (m *mobAI) hasPatrolTarget() bool { return m.patrolHasTarget }

// setPatrolTarget ports PatrollingMonster.setPatrolTarget(BlockPos): patrolTarget = target; patrolling = true.
func (m *mobAI) setPatrolTarget(x, y, z int) {
	m.patrolTargetX, m.patrolTargetY, m.patrolTargetZ = x, y, z
	m.patrolHasTarget = true
	m.patrolling = true
}

// patrolTargetCloserThan ports BlockPos.closerToCenterThan(pos, dist): the patrol target's block center
// is within dist of (x,y,z). Used by LongDistancePatrolGoal.tick's leader "arrived" check.
func (m *mobAI) patrolTargetCloserThan(x, y, z, dist float64) bool {
	if !m.patrolHasTarget {
		return false
	}
	dx := (float64(m.patrolTargetX) + 0.5) - x
	dy := (float64(m.patrolTargetY) + 0.5) - y
	dz := (float64(m.patrolTargetZ) + 0.5) - z
	return dx*dx+dy*dy+dz*dz < dist*dist
}

// findPatrolTarget ports PatrollingMonster.findPatrolTarget: patrolTarget = blockPosition + (-500 +
// random.nextInt(1000)) on X and Z; patrolling = true. The two nextInt(1000) DRAWS are on the mob's own
// stream (draw-order-faithful).
func (m *mobAI) findPatrolTarget(t *TickLoop, e *Entity) {
	r := mobRandom(e)
	bx := raidFloor(e.x)
	bz := raidFloor(e.z)
	m.patrolTargetX = bx + (-500 + r.nextInt(1000))
	m.patrolTargetY = raidFloor(e.y)
	m.patrolTargetZ = bz + (-500 + r.nextInt(1000))
	m.patrolHasTarget = true
	m.patrolling = true
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
	m.setWantTargetMod(x, y, z, 1.0)
}

// setWantTargetMod is setWantTarget carrying the vanilla navigation.moveTo(target, speedModifier)
// UNITLESS modifier (RandomStrollGoal 1.0, PanicGoal 1.25, TemptGoal 1.2, FollowParentGoal 1.1, ...).
// wantSpeed stays 0 so serverAiStep takes the stroll branch, where n.speed = wantSpeedMod x
// getAttributeValue(MOVEMENT_SPEED) -- the MoveControl.tick setSpeed(speedModifier x MOVEMENT_SPEED)
// port. This restores the per-goal speedModifier the old pigWalkSpeed path silently dropped.
func (m *mobAI) setWantTargetMod(x, y, z, speedModifier float64) {
	m.wantX, m.wantY, m.wantZ = x, y, z
	m.hasTarget = true
	m.wantSpeed = 0 // 0 == "use the stroll path" (wantSpeedMod x MOVEMENT_SPEED); see serverAiStep
	m.wantSpeedMod = speedModifier
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
func (m *mobAI) setWantCandidates(c [10][3]float64, landMode bool, speedModifier float64) {
	m.wantCands = c
	m.wantLandMode = landMode
	m.wantCandsSpeedMod = speedModifier
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
	// Mob.serverAiStep top: `++this.noActionTime;` (the very first statement, before LivingEntity.aiStep).
	// PURE INTEGER MATH (no RNG draw) — cannot perturb the per-mob RNG stream the pig oracle pins. It is
	// the despawn idle counter checkDespawn reads/resets. Cite Mob.serverAiStep (bytecode offset 0-9).
	m.noActionTime++

	// C-1 — the mob's Entity.tickCount, advanced here (once per serverAiStep) so the decimation gate
	// below reads the SAME already-incremented value vanilla reads (Entity.tickCount is bumped in
	// baseTick, ahead of aiStep -> serverAiStep). PURE INTEGER MATH (no RNG draw). The first call
	// sees aiTickCount==1 (vanilla tickCount==1 after the first baseTick) -> the `> 1` guard forces a
	// full goal re-eval on tick 1. Cite Mob.serverAiStep (Entity.tickCount).
	m.aiTickCount++

	// MOB-SUB-04 — the noJumpDelay decrement at the TOP of LivingEntity.aiStep (`if (noJumpDelay > 0)
	// noJumpDelay--;`). It is PURE INTEGER MATH (no RNG draw), so it cannot perturb the per-mob RNG
	// stream the pig oracle pins. Clamped at 0 — never negative.
	//	[VERIFIED javap LivingEntity.aiStep top: getfield noJumpDelay; ifle skip; iconst_1; isub;
	//	 putfield noJumpDelay — i.e. `if (noJumpDelay > 0) noJumpDelay--;`.]
	if m.noJumpDelay > 0 {
		m.noJumpDelay--
	}

	// B-M3 - the LivingEntity.aiStep small-velocity clamp: zero each deltaMovement axis whose abs is
	// below 0.003 so a mob does not drift with sub-threshold residual velocity. Ported 1:1 from
	// net.minecraft.world.entity.LivingEntity.aiStep. It runs EARLY in aiStep (after the noJumpDelay
	// decrement above, BEFORE applyInput/the goal+navigation work below) so downstream physics
	// integrates the clamped velocity - exactly vanilla's position (javap offsets 92-199, ahead of
	// applyInput at 217 and the serverAiStep body at 273). PURE FLOAT MATH, no RNG draw, so it cannot
	// perturb the per-mob RNG stream the pig oracle pins.
	//
	// Vanilla splits by entity type: a PLAYER zeroes BOTH x and z together when horizontalDistanceSqr
	// (x*x + z*z) < 9.0E-6; every OTHER entity (the pig, all mobs) clamps x and z INDEPENDENTLY at the
	// 0.003 epsilon. The y axis is clamped at 0.003 on BOTH paths. An AI mob here is never a player, so
	// this is the non-player (per-axis independent) branch plus the common y clamp.
	//	[VERIFIED javap LivingEntity.aiStep offsets 143-193: (else, non-PLAYER) abs(dm.x)<0.003 -> x=0;
	//	 abs(dm.z)<0.003 -> z=0; (common) abs(dm.y)<0.003 -> y=0; then setDeltaMovement(x,y,z). The
	//	 epsilon 0.003d is ldc2_w #3158; the compare is Math.abs(...) dcmpg ifge - i.e. abs < 0.003.]
	if math.Abs(e.vx) < 0.003 {
		e.vx = 0
	}
	if math.Abs(e.vz) < 0.003 {
		e.vz = 0
	}
	if math.Abs(e.vy) < 0.003 {
		e.vy = 0
	}

	// Mob.aiStep daylight-burn (fire.go tickMobSunBurn): a sun-sensitive mob in daylight with enough
	// local brightness and an empty HEAD protection slot ignites for 8s. The helper gates to the bare
	// Skeleton/Zombie path, so a passive Pig never reaches it — zero new draws on the pig's per-mob RNG
	// stream (the oracle is unperturbed; PITFALLS Pitfall 5). Placed in aiStep like vanilla.
	if e.isSunSensitive() {
		t.tickMobSunBurn(e)
	}

	// (sensing.tick — skipped: the v1 goals probe the world directly in their canUse.)
	//
	// C-1 TICK DECIMATION — ported 1:1 from net.minecraft.world.entity.Mob.serverAiStep. Vanilla does
	// NOT re-evaluate the goal/target selectors every tick: it splits the work by an odd/even parity of
	// (Entity.tickCount + getId()). The full GoalSelector.tick() (goalCleanup start/stop re-eval + a
	// tickRunningGoals(true) at its tail) runs on the "even" phase (or the first tick); the "odd" phase
	// runs only tickRunningGoals(false) — no start/stop re-eval, and only goals that
	// requiresUpdateEveryTick() get ticked. So a running goal's canUse/canContinueToUse (and any RNG it
	// draws there) fires every OTHER tick, not every tick. Movement (navigation/controls, below) stays
	// FULL rate — it is outside this branch. Order within each branch: targetSelector FIRST, then the
	// action goalSelector (jar order). For a passive Pig the targetSelector is empty (zero TARGET goals),
	// so its tick/tickRunningGoals are no-ops drawing NO RNG.
	//
	// The exact bytecode condition (javap Mob.serverAiStep offsets 36-153):
	//   int i = this.tickCount + this.getId();
	//   if (i % 2 != 0 && this.tickCount > 1) { targetSelector.tickRunningGoals(false); goalSelector.tickRunningGoals(false); }
	//   else                                   { targetSelector.tick();                  goalSelector.tick(); }
	// i.e. FULL tick when (i%2==0) OR (tickCount<=1); LIGHT (tickRunningGoals(false)) otherwise. The
	// `tickCount > 1` guard makes the mob's first two AI steps always do a full re-eval so a freshly
	// spawned mob acquires its first goals immediately. GoalSelector.tick() ends with its OWN
	// tickRunningGoals(true) (javap-confirmed), so the FULL branch ticks each running goal exactly once;
	// the two branches are MUTUALLY EXCLUSIVE, so no goal is ever double-ticked in a step.
	//	[VERIFIED javap Mob.serverAiStep: istore_2 (i=tickCount+getId()); iload_2 iconst_2 irem ifeq ->tick;
	//	 iload tickCount iconst_1 if_icmpgt ->light; else ->tick. GoalSelector.tick tail: tickRunningGoals(true).
	//	 GoalSelector.tickRunningGoals(z): tick a running goal iff z || requiresUpdateEveryTick().]
	i := m.aiTickCount + int(e.id)
	if i%2 != 0 && m.aiTickCount > 1 {
		// ODD phase (past the first tick): tick already-running goals only, no start/stop re-eval and no
		// canUse draws. Only goals whose requiresUpdateEveryTick() is true actually tick (e.g. the pig's
		// @8 RandomLookAroundGoal); the rest idle this tick. targetSelector FIRST (jar order).
		m.targetSelector.tickRunningGoals(t, e, false)
		m.goals.tickRunningGoals(t, e, false)
	} else {
		// EVEN phase (or the first tick): full re-eval. GoalSelector.tick() stops/starts goals by priority
		// + flag locks and ticks every running goal once at its tail. targetSelector FIRST (jar order).
		m.targetSelector.tick(t, e)
		m.goals.tick(t, e)
	}

	// Phase 30.1 — the RNG-FREE stroll snap: a MOVE goal (Go stroll start() or the plugin pig's
	// overloaded path_to(31 floats: 10 candidates + landMode)) emitted 10 RAW candidates this tick (hasWantCands). Validate +
	// ground-snap them to the first reachable walkable column (RandomPos.generateRandomPos first-valid,
	// LandRandomPos.getPos snap) and commit the winner — or leave hasTarget false if none survive
	// (vanilla generateRandomPos null). This draws ZERO randoms, so it does not perturb the per-mob RNG
	// stream the pig oracle pins, and it runs BEFORE requestPath so the floored want fed to the A* (and
	// the wantX/Y/Z the oracle observes) is the SNAPPED reachable column — the wedge-bug root-cause fix.
	if m.hasWantCands {
		if wx, wy, wz, ok := m.snapStrollWant(t, e); ok {
			// commit the snapped, reachable target AT THE GOAL'S speedModifier (stroll 1.0 / panic 1.25);
			// the seam below multiplies it by MOVEMENT_SPEED (the MoveControl.tick setSpeed port).
			m.setWantTargetMod(wx, wy, wz, m.wantCandsSpeedMod) // sets hasTarget
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
		// Route the want SPEED into the navigation. Two seams, both == vanilla getSpeed (blocks/tick):
		//   - a CHASE goal (setWantTargetSpeed) already carries the FINAL getSpeed = speedModifier x
		//     MOVEMENT_SPEED (its caller did the multiply, e.g. MeleeAttackGoal getSpeed).
		//   - the stroll/panic/follow path (setWantTarget/setWantTargetMod, wantSpeed==0) carries only the
		//     UNITLESS speedModifier in wantSpeedMod; the MOVEMENT_SPEED multiply happens HERE, once, the
		//     port of MoveControl.tick's setSpeed(speedModifier x getAttributeValue(MOVEMENT_SPEED)). An
		//     idle pig now strolls at 1.0 x 0.25 = 0.25 getSpeed (vanilla), NOT the fabricated 0.15.
		//	[VERIFIED javap MoveControl.tick MOVE_TO: mob.setSpeed((float)(speedModifier x
		//	 getAttributeValue(MOVEMENT_SPEED))).]
		if m.wantSpeed > 0 {
			m.navigation.speed = m.wantSpeed
		} else {
			m.navigation.speed = m.wantSpeedMod * e.getAttributeValue(attribute.MovementSpeed)
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

	// B-A3 — the LivingEntity.aiStep pushEntities() slot: shove overlapping pushable entities apart
	// (the 0.05 vanilla impulse) and apply cramming damage when a crowd exceeds maxEntityCramming. In
	// vanilla this is the tail of LivingEntity.aiStep (after checkAutoSpinAttack, before the profiler
	// pop). A single minimal hook (entity_collision.go). PIG ORACLE: a lone pig has no overlapping
	// pushable neighbour, so pushNearbyEntities early-outs on the empty list with ZERO new RNG draw
	// and ZERO impulse — byte-identical. Cite LivingEntity.aiStep (offset 817: pushEntities()).
	t.pushNearbyEntities(e)
}

// applyAnimalPathfindingMalus ports net.minecraft.world.entity.animal.Animal.<init>'s two
// setPathfindingMalus calls (VERIFIED CFR Animal ctor): FIRE_IN_NEIGHBOR = 16.0 (the default is 8,
// so an Animal is MORE fire-averse near fire) and FIRE = -1.0 (the default is 16, so an Animal treats
// a fire node as IMPASSABLE, not merely costly). Every Animal subclass (Pig/Cow/Sheep/Chicken/Wolf/
// Cat/Fox/Rabbit/Mooshroom/Turtle/Ocelot/HappyGhast) inherits these from the base ctor. In a world
// with no fire (every current test world, incl. the pig oracle) these overrides never change a path —
// but they are stamped faithfully so an Animal near lava/fire routes as vanilla does. Cite Animal.<init>.
func applyAnimalPathfindingMalus(m *mobAI) {
	if m == nil {
		return
	}
	m.malus.setPathfindingMalus(pathFireInNeighbor, 16.0)
	m.malus.setPathfindingMalus(pathFire, -1.0)
}

// isAnimalType reports whether an entity wire type is a net.minecraft.world.entity.animal.Animal
// subclass (so applyAnimalPathfindingMalus applies its FIRE overrides). Vanilla's Animal hierarchy —
// the overworld passives/tameables that extend Animal (directly or via AgeableMob->Animal). This is
// the jar's class hierarchy (Pig/Cow/... extends Animal), mirrored here so a declared mob gets the
// SAME malus its Go-native twin does (the pig oracle parity contract). Non-animals (zombies, skeletons,
// endermen, raiders, sulfur cube) are NOT Animals and get no FIRE override (their own classes may set
// their own malus, cited-deferred). Cite the Animal class hierarchy.
func isAnimalType(t entity.ID) bool {
	switch t {
	case entity.Pig.ID, entity.Cow.ID, entity.Sheep.ID, entity.Chicken.ID,
		entity.Wolf.ID, entity.Cat.ID, entity.Fox.ID, entity.Rabbit.ID,
		entity.Mooshroom.ID, entity.Turtle.ID, entity.Ocelot.ID, entity.HappyGhast.ID:
		return true
	default:
		return false
	}
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
	// wantSpeedMod is the vanilla navigation.moveTo speedModifier for the stroll/passive path; the
	// RandomStrollGoal default is 1.0. The MoveControl.tick seam (serverAiStep) turns this into the real
	// getSpeed = 1.0 x getAttributeValue(MOVEMENT_SPEED) each tick, so navigation.speed below is only the
	// pre-first-want seed. Default 1.0 so a mob never strolls at getSpeed 0 before its first want.
	m.wantSpeedMod = 1.0
	// navigation.speed seed (overwritten every tick by the MoveControl.tick seam once a want exists):
	// the stroll speedModifier 1.0 x MOVEMENT_SPEED. Read the attribute so a pig seeds 0.25, not a
	// fabricated 0.15. Kept explicit so the first pre-want tick has a sane pace.
	m.navigation.speed = m.wantSpeedMod * 0.25 // pig MOVEMENT_SPEED default (seedAttributes folds 0.25)
	// FloatGoal ctor: mob.getNavigation().setCanFloat(true) — the mob may path over water (the float
	// PATHING node-evaluator behavior is deferred + cited on the field; the flag set is the 1:1 port).
	m.navigation.canFloat = true
	// Animal.<init> pathfinding malus: FIRE_IN_NEIGHBOR 16 / FIRE -1 (a pig is an Animal). No-op on a
	// fire-free world (the pig oracle), but the plugin pig gets the IDENTICAL malus (buildAIFromDecl ->
	// applyAnimalPathfindingMalus), so the byte-identical oracle stays byte-identical. Cite Animal.<init>.
	applyAnimalPathfindingMalus(m)
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
	m.goals.addGoal(4, newTemptGoal(1.2, func(id int32) bool { return id == 887 }, false, nil))                 // Items.CARROT_ON_A_STICK (id 887), canScare=false
	m.goals.addGoal(4, newTemptGoal(1.2, func(id int32) bool { return itemInTag(id, "pig_food") }, false, nil)) // ItemTags.PIG_FOOD, canScare=false
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
