package server

// ai_goals_target.go — MOB-SUB-10 (Phase 35-01): the shared targetSelector goals, PORTED (the
// STANDING MANDATE, idiomatic non-1:1 Go, no GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, read via javap -c -p this session):
//
//   - net.minecraft.world.entity.ai.goal.target.NearestAttackableTargetGoal: flags {TARGET};
//     canUse rolls 1-in-randomInterval then scans for the nearest valid player target bounded by
//     FOLLOW_RANGE; start() = mob.setTarget(target). The acquired target sets mobAI.attackTargetID
//     (Mob.getTarget), which the MeleeAttackGoal (ai_goals_attack.go) canUse-gates on.
//   - net.minecraft.world.entity.ai.goal.target.HurtByTargetGoal: flags {TARGET}; canUse reads the
//     entity's lastHurtByMob/lastHurtByMobTimestamp bookkeeping (entity.go, set in combat_mob.go) and
//     retaliates against a fresh attacker; NO RNG. start() = mob.setTarget(getLastHurtByMob()).
//
// These are built ONCE here so the wolf (Phase 36) and every hostile (zombie/skeleton/spider) reuse
// them — the targetSelector is a SECOND independent goalSelector on mobAI (ai_mob.go), so a TARGET
// goal locks the TARGET flag among the targetSelector's goals, never touching the goalSelector's
// MOVE/LOOK locks.
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares ZERO TARGET goals, so its targetSelector is
// empty and neither goal ever ticks on it — no new RNG draw reaches the pinned pig stream.
//
//   ⚠ LOCKSTEP-CRITICAL (NearestAttackableTargetGoal): the jar ctor does randomInterval =
//   reducedTickDelay(10) = ceilDiv(10,2) = 5, to compensate for vanilla evaluating TARGET goals every
//   OTHER server tick (the Mob.serverAiStep (tickCount+id)%2 decimation). The Go driver ticks
//   serverAiStep EVERY tick (tick_phases.go — no decimation), so to fire at the vanilla real-world
//   rate the Go gate MUST use the FULL interval = nextInt(10), NOT the halved nextInt(5) — exactly the
//   same identity rule adjustedTickDelay follows. DO NOT call reducedTickDelay here; use the raw 10.
//   (35-JARNOTES.md:149-164; pinned by TestNearestAttackableTargetGateUsesTen.)

import (
	"github.com/imhinotori/sulfur/level/attribute"
)

// --- nearestAttackableTargetGoal -------------------------------------------------------------

// nearestTargetRandomInterval is the FAITHFUL Go RNG-gate bound for NearestAttackableTargetGoal:
// the FULL net.minecraft.world.entity.ai.goal.target.NearestAttackableTargetGoal.DEFAULT_RANDOM_INTERVAL
// (== 10), NOT the jar ctor's reducedTickDelay(10) == 5. See the LOCKSTEP-CRITICAL note in the file
// header: the jar halves it to compensate for its every-other-tick TARGET evaluation; our full-rate
// serverAiStep does not decimate, so the raw 10 is the 1:1-faithful value run every tick.
//
//	[VERIFIED javap NearestAttackableTargetGoal.<init>(...,int,...): randomInterval =
//	 reducedTickDelay(10); DEFAULT_RANDOM_INTERVAL == 10. canUse: getRandom().nextInt(randomInterval).]
const nearestTargetRandomInterval = 10

// nearestAttackableTargetGoal ports NearestAttackableTargetGoal<Player> (flags {TARGET}). v1 only
// acquires a PLAYER target (the hostile-vs-player case), so findTarget reuses the position-based
// nearestPlayerAt scan (ai_goals_passive.go) bounded by FOLLOW_RANGE — exactly
// findTarget's Player branch: level.getNearestPlayer(targetConditions, mob, x, eyeY, z).
type nearestAttackableTargetGoal struct {
	baseGoal
	randomInterval int

	// target is NearestAttackableTargetGoal.target, captured in canUse(findTarget) and committed to
	// mobAI.attackTargetID in start(). For a v1 player target it carries the player's entity id; 0 ==
	// no target found.
	target int32

	// forceTrigger skips the RNG gate once (test seam, mirroring randomStrollGoal.forceTrigger — it
	// bypasses the ROLL, not the source, so a unit test can make findTarget run deterministically).
	forceTrigger bool
}

// newNearestAttackableTargetGoal builds the goal with the FULL randomInterval (10) and the TARGET
// flag (NearestAttackableTargetGoal ctor: setFlags(EnumSet.of(TARGET))).
func newNearestAttackableTargetGoal() *nearestAttackableTargetGoal {
	return &nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: nearestTargetRandomInterval,
	}
}

// canUse ports NearestAttackableTargetGoal.canUse (bytecode-verified this session):
//
//	if (randomInterval > 0 && mob.getRandom().nextInt(randomInterval) != 0) return false;  // RNG GATE
//	findTarget();
//	return target != null;
//
// The RNG gate draws EXACTLY ONE nextInt(randomInterval) per canUse, with randomInterval == 10 (the
// faithful full interval — see the header LOCKSTEP note; NOT reducedTickDelay's 5). When the gate
// fails (nextInt(10) != 0) it returns BEFORE the scan (no findTarget call). The scan is a pure world
// read (no RNG).
//
//	[VERIFIED javap NearestAttackableTargetGoal.canUse: getfield randomInterval; ifle; getRandom();
//	 nextInt(randomInterval); ifeq -> findTarget; else iconst_0 ireturn; findTarget; target ifnull?0:1.]
func (g *nearestAttackableTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if !g.forceTrigger {
		// DRAW 1 (the gate): getRandom().nextInt(randomInterval). randomInterval == 10 (the FULL
		// DEFAULT_RANDOM_INTERVAL, our full-rate-tick compensation for the jar's reducedTickDelay(10)=5).
		if g.randomInterval > 0 && mobRandom(e).nextInt(g.randomInterval) != 0 {
			return false
		}
	}
	g.forceTrigger = false
	g.findTarget(t, e)
	return g.target != 0
}

// findTarget ports NearestAttackableTargetGoal.findTarget — the Player branch (the only target type
// v1 acquires): target = level.getNearestPlayer(targetConditions, mob, x, eyeY, z), where
// targetConditions.range == getFollowDistance() == getAttributeValue(FOLLOW_RANGE)
// (TargetGoal.getFollowDistance). The search is bounded by the FOLLOW_RANGE attribute, matching the
// jar's getTargetSearchArea(followDistance) = boundingBox.inflate(followDistance) — nearestPlayerAt
// applies the SAME radius as a sphere bound (the player scan the lookAtPlayerGoal/world.nearest_player
// seam already uses). NO RNG.
//
// CITE-DEFERRED sub-behavior (recorded, NEVER silently dropped):
//   - mustSee line-of-sight: TargetingConditions.forCombat() carries a LoS check; no raycast/LoS
//     subsystem exists in v1 (the passive goals have no LoS check either), so the LoS gate is a cited
//     stub equal to "visible" — every player in FOLLOW_RANGE is acquirable. Upgrade: gate on a real
//     hasLineOfSight raycast when sensing lands (35-JARNOTES OPEN: TargetingConditions).
//   - the TargetingConditions team/invisibility/selector filters: v1 has no teams/invisibility; the
//     range bound is the only active filter, the rest are cited no-ops.
//
//	[VERIFIED javap NearestAttackableTargetGoal.findTarget Player branch: getServerLevel(mob)
//	 .getNearestPlayer(getTargetConditions(), mob, getX(), getEyeY(), getZ()); TargetGoal.getFollowDistance
//	 == getAttributeValue(FOLLOW_RANGE).]
func (g *nearestAttackableTargetGoal) findTarget(t *TickLoop, e *Entity) {
	follow := e.getAttributeValue(attribute.FollowRange)
	// getNearestPlayer is anchored at (mob.getX(), mob.getEyeY(), mob.getZ()); v1 has no eye-height
	// field (refreshDimensions notes the cited eye-height gap), so the scan anchors at the mob feet y
	// — the same anchor lookAtPlayerGoal/nearestPlayerWithin use. The FOLLOW_RANGE bound is faithful.
	if id, ok := nearestPlayerIDAt(t, e.x, e.y, e.z, follow); ok {
		g.target = id
		return
	}
	g.target = 0
}

// canContinueToUse ports the TargetGoal.canContinueToUse base: a running target goal keeps running
// while the acquired target is still a valid, in-range, alive player. The jar walks
// canContinueToUse -> the target is alive, within getFollowDistance(), and passes targetConditions;
// v1 checks the player still exists (resolvable) + within FOLLOW_RANGE (the live distance bound).
// NO RNG.
//
//	[VERIFIED javap TargetGoal.canContinueToUse: target = mob.getTarget(); if null return false; if
//	 !target.isAlive() return false; distSqr > followDistance² -> false; else canAttack.]
func (g *nearestAttackableTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	p := t.playerByEntityID(id)
	if p == nil {
		return false // the target left / disconnected (TargetGoal: target not alive/absent)
	}
	follow := e.getAttributeValue(attribute.FollowRange)
	dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
	return dx*dx+dy*dy+dz*dz <= follow*follow
}

// start ports NearestAttackableTargetGoal.start: mob.setTarget(this.target); super.start(). It
// commits the acquired target id to mobAI.attackTargetID (the MeleeAttackGoal canUse-gate reads it).
// NO RNG beyond the canUse gate.
//
//	[VERIFIED javap NearestAttackableTargetGoal.start: mob.setTarget(target); super.start().]
func (g *nearestAttackableTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.target)
	}
}

// stop ports TargetGoal.stop: clear the target (mob.setTarget(null)). The jar's TargetGoal.stop sets
// mob.setTarget(null) and clears targetMob. v1 clears attackTargetID so a dropped target stops the
// downstream MeleeAttackGoal.
//
//	[VERIFIED javap TargetGoal.stop: mob.setTarget((LivingEntity) null); targetMob = null.]
func (g *nearestAttackableTargetGoal) stop(_ *TickLoop, e *Entity) {
	g.target = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// --- hurtByTargetGoal ------------------------------------------------------------------------

// hurtByTargetGoal ports HurtByTargetGoal (flags {TARGET}), NO RNG — it retaliates against whoever
// last hit the mob, read from the entity's lastHurtByMob/lastHurtByMobTimestamp bookkeeping
// (entity.go, set at the combat_mob.go flag2 store-point).
type hurtByTargetGoal struct {
	baseGoal

	// timestamp is HurtByTargetGoal.timestamp — the lastHurtByMobTimestamp value captured on the LAST
	// start(), so canUse fires only ONCE per fresh hit (timestamp == this.timestamp -> already
	// retaliated -> false).
	timestamp int32
}

// newHurtByTargetGoal builds the goal with the TARGET flag (HurtByTargetGoal ctor:
// setFlags(EnumSet.of(TARGET))).
func newHurtByTargetGoal() *hurtByTargetGoal {
	return &hurtByTargetGoal{baseGoal: newBaseGoal(flagTarget)}
}

// canUse ports HurtByTargetGoal.canUse (bytecode-verified this session), NO RNG:
//
//	int timestamp = mob.getLastHurtByMobTimestamp();
//	LivingEntity last = mob.getLastHurtByMob();
//	if (timestamp == this.timestamp || last == null) return false;
//	if (last.is(PLAYER) && UNIVERSAL_ANGER gamerule) return false;
//	for (Class c : toIgnoreDamage) if (c.isAssignableFrom(last.getClass())) return false;
//	return canAttack(last, HURT_BY_TARGETING);
//
// CITE-DEFERRED sub-behavior (recorded, NEVER silently dropped):
//   - UNIVERSAL_ANGER gamerule: no gamerule subsystem in v1 — the gamerule defaults FALSE in vanilla,
//     so this guard is a cited no-op (never skips retaliation). Upgrade: read the gamerule when one
//     lands (35-JARNOTES.md:89).
//   - toIgnoreDamage class filter: the base HurtByTargetGoal has an EMPTY toIgnoreDamage array (only
//     subclasses set one, e.g. ZombifiedPiglin); the shared goal carries no ignore set, so this loop
//     is a cited empty no-op for the v1 hostiles.
//
//	[VERIFIED javap HurtByTargetGoal.canUse: getLastHurtByMobTimestamp; getLastHurtByMob; (ts ==
//	 this.timestamp || last == null) -> false; UNIVERSAL_ANGER guard; toIgnoreDamage loop; canAttack.]
func (g *hurtByTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	timestamp := e.lastHurtByMobTimestamp
	last := e.lastHurtByMob
	if timestamp == g.timestamp || last == 0 {
		return false
	}
	// (UNIVERSAL_ANGER gamerule + toIgnoreDamage filter — cited no-ops above.)
	// canAttack(last, HURT_BY_TARGETING): the attacker must be a valid combat target. v1's attacker is a
	// player id; validity == the player still exists (resolvable on the loop). The full TargetingConditions
	// (range/LoS) of HURT_BY_TARGETING is a cited no-op in v1 (no LoS subsystem) — the existence check is
	// the active validity gate, mirroring the passive goals' player-exists discipline.
	return g.canAttack(t, last)
}

// canAttack ports the canAttack(last, HURT_BY_TARGETING) validity check for v1: the attacker (a
// player id) must still be present on the loop (the player has not disconnected). HURT_BY_TARGETING ==
// TargetingConditions.forCombat().ignoreLineOfSight().ignoreInvisibilityTesting() — so it carries NO
// LoS/invisibility filter (cited), leaving "the target exists and is a live combat candidate" as the
// active gate. NO RNG.
func (g *hurtByTargetGoal) canAttack(t *TickLoop, id int32) bool {
	return t.playerByEntityID(id) != nil
}

// start ports HurtByTargetGoal.start (bytecode-verified):
//
//	mob.setTarget(mob.getLastHurtByMob());
//	this.targetMob = mob.getTarget();
//	this.timestamp = mob.getLastHurtByMobTimestamp();
//	this.unseenMemoryTicks = 300;
//	if (alertSameType) alertOthers();
//	super.start();
//
// It commits the attacker as the target and stamps its OWN timestamp so canUse won't re-fire on the
// same hit. unseenMemoryTicks=300 + alertOthers (setAlertOthers, a Zombie-only alert burst) are
// CITE-DEFERRED: the v1 shared goal carries no alertSameType, and the unseenMemoryTicks
// lose-sight-grace is a cited no-op (no LoS subsystem). NO RNG.
//
//	[VERIFIED javap HurtByTargetGoal.start: setTarget(getLastHurtByMob()); targetMob = getTarget();
//	 timestamp = getLastHurtByMobTimestamp(); unseenMemoryTicks = 300; alertSameType? alertOthers();
//	 super.start().]
func (g *hurtByTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(e.lastHurtByMob)
	}
	g.timestamp = e.lastHurtByMobTimestamp
	// (unseenMemoryTicks = 300 + alertOthers — cited deferrals above.)
}

// canContinueToUse ports the TargetGoal.canContinueToUse base (shared with the nearest-target goal):
// keep retaliating while the target is a present, in-range player. v1 reuses the same existence +
// FOLLOW_RANGE distance gate. NO RNG.
//
//	[VERIFIED javap TargetGoal.canContinueToUse: target alive + within followDistance² + canAttack.]
func (g *hurtByTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	return t.playerByEntityID(id) != nil
}

// stop ports TargetGoal.stop: mob.setTarget(null). Clears the attack target so the downstream attack
// goal stops. NO RNG.
//
//	[VERIFIED javap TargetGoal.stop: mob.setTarget((LivingEntity) null).]
func (g *hurtByTargetGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// nearestPlayerIDAt is the entity-id-returning sibling of nearestPlayerAt (ai_goals_passive.go): the
// id of the nearest player within maxDist of (cx,cy,cz), or ok=false if none. NearestAttackableTargetGoal
// needs the player's ENTITY id (to set as the attack target), where lookAtPlayerGoal only needs its
// POSITION — so this returns p.entityID instead of the position. It scans the SAME tick-owned t.players
// set (the player seam; players are not entityStore entries) with the SAME distance discipline.
// Tick-owned read (TICK-05). NO RNG.
func nearestPlayerIDAt(t *TickLoop, cx, cy, cz, maxDist float64) (id int32, ok bool) {
	best := maxDist * maxDist
	for _, p := range t.players {
		if p == nil {
			continue
		}
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best {
			best = d2
			id, ok = p.entityID, true
		}
	}
	return id, ok
}
