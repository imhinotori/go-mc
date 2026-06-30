package server

// ai_goals_owner.go — MOB-NEUT-01 (Phase 36-01): the Go-native OWNER goals — FollowOwnerGoal (the @6
// goalSelector follow) + OwnerHurtByTargetGoal (@1 targetSelector) + OwnerHurtTargetGoal (@2
// targetSelector). PORTED 1:1 (the STANDING MANDATE, idiomatic non-1:1 Go, no GPL paste) from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). ALL THREE have NO RNG.
//
//   - net.minecraft.world.entity.ai.goal.FollowOwnerGoal: flags {MOVE}; canUse/canContinueToUse gate on
//     the owner distance band (startDistance 10 → 100.0, stopDistance 2 → 4.0); tick lookAt + moveTo.
//   - net.minecraft.world.entity.ai.goal.target.OwnerHurtByTargetGoal: flags {TARGET}; targets whoever
//     last hit the wolf's OWNER (owner.getLastHurtByMob), once per fresh hit (the timestamp guard).
//   - net.minecraft.world.entity.ai.goal.target.OwnerHurtTargetGoal: flags {TARGET}; the mirror —
//     targets whatever the OWNER last ATTACKED (owner.getLastHurtMob).
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares none of these goals, and !isTame() short-circuits
// every canUse to false anyway (the pig's tame field is the zero-value false) — they never tick on it.
// Every field read is a wolf/owner field, zero for the pig. NO RNG in any path.

// --- followOwnerGoal -----------------------------------------------------------------------------

// followOwnerStartDistanceSqr / followOwnerStopDistanceSqr are the Wolf @6 FollowOwnerGoal(this, 1.0,
// 10.0, 2.0) distance band: it STARTS following when distanceToSqr(owner) >= startDistance² (10² = 100.0)
// and STOPS when distanceToSqr(owner) <= stopDistance² (2² = 4.0). The 10.0/2.0 are the Wolf ctor's
// literal startDistance/stopDistance args; the goal squares them (getfield startDistance; fmul).
//
//	[VERIFIED javap Wolf.registerGoals @6: new FollowOwnerGoal(this, 1.0d, 10.0f, 2.0f). FollowOwnerGoal
//	 .canUse: distanceToSqr(owner) < startDistance*startDistance → false. canContinueToUse:
//	 distanceToSqr(owner) > stopDistance*stopDistance.]
const (
	followOwnerStartDistanceSqr = 100.0 // 10.0² (startDistance)
	followOwnerStopDistanceSqr  = 4.0   // 2.0²  (stopDistance)
)

// followOwnerRecalcPath is FollowOwnerGoal.tick's path-recalc cadence: adjustedTickDelay(10) == 10
// (IDENTITY in our full-rate-tick driver — the same rule adjustedTickDelay/reducedTickDelay follow,
// 34-JARNOTES). The follow goal re-issues its moveTo toward the owner at most once per 10 ticks.
//
//	[VERIFIED javap FollowOwnerGoal.tick: if (--timeToRecalcPath > 0) return; timeToRecalcPath =
//	 adjustedTickDelay(10); ... navigation.moveTo(owner, speedModifier).]
const followOwnerRecalcPath = 10

// followOwnerGoal ports net.minecraft.world.entity.ai.goal.FollowOwnerGoal (flag {MOVE}). It SETS a nav
// want toward the owner (the goal SETS a want; the async nav steps the mob — it NEVER moves the mob),
// gated on the start/stop distance band. NO RNG.
type followOwnerGoal struct {
	baseGoal
	speedModifier   float64 // FollowOwnerGoal.speedModifier — the navigation.moveTo speed
	timeToRecalcPath int    // FollowOwnerGoal.timeToRecalcPath — the RNG-free recalc countdown
}

// newFollowOwnerGoal builds the follow goal with the MOVE flag (FollowOwnerGoal ctor:
// setFlags(EnumSet.of(MOVE))). The speed is the Wolf ctor's 1.0 (declaredWalkSpeed routes the .star's
// movement_speed here, the SAME want-multiplier posture as MeleeAttackGoal).
//
//	[VERIFIED javap FollowOwnerGoal.<init>: putfield speedModifier; putfield startDistance; putfield
//	 stopDistance; setFlags(EnumSet.of(Goal$Flag.MOVE)); navigation = mob.getNavigation().]
func newFollowOwnerGoal(speed float64) *followOwnerGoal {
	return &followOwnerGoal{baseGoal: newBaseGoal(flagMove), speedModifier: speed}
}

// canUse ports FollowOwnerGoal.canUse (bytecode-verified this session), NO RNG:
//
//	LivingEntity owner = tamable.getOwner();
//	if (owner == null) return false;
//	if (tamable.unableToMoveToOwner()) return false;
//	if (tamable.distanceToSqr(owner) < startDistance²) return false;   // too close → don't follow
//	this.owner = owner; return true;
//
// CITE-DEFERRED: unableToMoveToOwner() (a leashed/passenger/sitting/owner-spectator guard) — v1 has no
// leash/passenger/spectator subsystem, so it is a cited constant-FALSE (the wolf is always able to move
// to its owner). The owner-null + the start-distance band are the live gates.
//
//	[VERIFIED javap FollowOwnerGoal.canUse: getOwner ifnull → 0; unableToMoveToOwner ifne → 0;
//	 distanceToSqr(owner) < startDistance*startDistance → 0; putfield owner; iconst_1.]
func (g *followOwnerGoal) canUse(t *TickLoop, e *Entity) bool {
	owner := t.playerByEntityID(e.ownerUUID) // getOwner()
	if owner == nil {
		return false
	}
	// unableToMoveToOwner(): cited constant-false (no leash/passenger/spectator in v1).
	dx, dy, dz := owner.x-e.x, owner.y-e.y, owner.z-e.z
	if dx*dx+dy*dy+dz*dz < followOwnerStartDistanceSqr { // < startDistance² → too close, don't follow
		return false
	}
	return true
}

// canContinueToUse ports FollowOwnerGoal.canContinueToUse (bytecode-verified this session), NO RNG:
//
//	if (navigation.isDone()) return false;
//	if (tamable.unableToMoveToOwner()) return false;
//	return !(distanceToSqr(owner) <= stopDistance²);   // keep following while still beyond stopDistance
//
// CITE-DEFERRED: navigation.isDone() — v1's nav is the async setWantTarget path (no per-tick Path
// object), so "the path completed" is approximated by the live distance band (keep following while the
// owner is beyond stopDistance²; the want is re-issued each recalc). unableToMoveToOwner() is the same
// cited constant-false as canUse. NOTE the jar canContinueToUse has NO !isOrderedToSit() term (the
// pre-decompile guess was wrong) — the verified bytecode is the three checks above; a sitting wolf is
// parked by the SIT goal claiming MOVE at higher precedence (@2 < @6), not by this method.
//
//	[VERIFIED javap FollowOwnerGoal.canContinueToUse: navigation.isDone ifne → 0; unableToMoveToOwner
//	 ifne → 0; distanceToSqr(owner) > stopDistance*stopDistance → 1 else 0.]
func (g *followOwnerGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	owner := t.playerByEntityID(e.ownerUUID)
	if owner == nil {
		return false // owner gone → the navigation can never complete toward it (the isDone analog)
	}
	// unableToMoveToOwner(): cited constant-false.
	dx, dy, dz := owner.x-e.x, owner.y-e.y, owner.z-e.z
	return dx*dx+dy*dy+dz*dz > followOwnerStopDistanceSqr // keep following while beyond stopDistance²
}

// start ports FollowOwnerGoal.start: timeToRecalcPath = 0; oldWaterCost = getPathfindingMalus(WATER);
// setPathfindingMalus(WATER, 0.0). v1 resets the recalc countdown; the WATER-malus save/restore (follow
// the owner THROUGH water) is a cited deferral — v1 has no per-PathType pathfinding malus, so the
// save/zero/restore is a no-op (the wolf's async nav already crosses water via FloatGoal). NO RNG.
//
//	[VERIFIED javap FollowOwnerGoal.start: timeToRecalcPath = 0; oldWaterCost = getPathfindingMalus(WATER);
//	 setPathfindingMalus(WATER, 0.0F).]
func (g *followOwnerGoal) start(_ *TickLoop, _ *Entity) {
	g.timeToRecalcPath = 0
	// (oldWaterCost = getPathfindingMalus(WATER); setPathfindingMalus(WATER, 0) — cited no-op, no malus
	// subsystem in v1.)
}

// stop ports FollowOwnerGoal.stop: owner = null; navigation.stop(); setPathfindingMalus(WATER,
// oldWaterCost). v1 stops the navigation (clearWantTarget); the owner field is re-resolved per call (not
// cached), and the WATER-malus restore is the cited no-op of start's save. NO RNG.
//
//	[VERIFIED javap FollowOwnerGoal.stop: owner = null; navigation.stop(); setPathfindingMalus(WATER,
//	 oldWaterCost).]
func (g *followOwnerGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
}

// tick ports FollowOwnerGoal.tick (bytecode-verified this session), NO RNG:
//
//	boolean shouldTeleport = tamable.shouldTryTeleportToOwner();
//	if (!shouldTeleport) lookControl.setLookAt(owner, 10.0F, maxHeadXRot);
//	if (--timeToRecalcPath > 0) return;
//	timeToRecalcPath = adjustedTickDelay(10);
//	if (shouldTeleport) tamable.tryToTeleportToOwner();
//	else navigation.moveTo(owner, speedModifier);
//
// DEFERRED (recorded + in the SUMMARY): tryToTeleportToOwner (the safe-pos scan that yanks a far wolf
// to its owner) — v1 ships ONLY the moveTo path-follow (the core observable: the wolf walks to its
// owner). shouldTryTeleportToOwner() is therefore a cited constant-FALSE (never teleport), so tick
// always takes the lookAt + moveTo branch. getMaxHeadXRot is the look-pitch clamp (v1 sets headYaw
// only, the same posture as MeleeAttackGoal.tick). NO RNG.
//
//	[VERIFIED javap FollowOwnerGoal.tick: shouldTryTeleportToOwner; !it → getLookControl().setLookAt(
//	 owner, 10.0F, getMaxHeadXRot()); if (--timeToRecalcPath > 0) return; timeToRecalcPath =
//	 adjustedTickDelay(10); teleport? tryToTeleportToOwner() : navigation.moveTo(owner, speedModifier).]
func (g *followOwnerGoal) tick(t *TickLoop, e *Entity) {
	owner := t.playerByEntityID(e.ownerUUID)
	if owner == nil {
		return
	}
	// shouldTryTeleportToOwner(): cited constant-false (the teleport defers) → always lookAt + moveTo.
	// setLookAt(owner, 10, maxHeadXRot): face the owner (headYaw, the lookAtPlayerGoal/MeleeAttackGoal seam).
	yaw := yawTowardDeg(owner.x-e.x, owner.z-e.z)
	e.headYaw = yaw
	e.yaw = yaw

	// if (--timeToRecalcPath > 0) return; — the RNG-free recalc countdown.
	g.timeToRecalcPath--
	if g.timeToRecalcPath > 0 {
		return
	}
	g.timeToRecalcPath = followOwnerRecalcPath // adjustedTickDelay(10) == 10 (identity)

	// navigation.moveTo(owner, speedModifier): SET the nav want toward the owner (the goal SETS a want;
	// the async nav steps the mob — NEVER moveEntity). The speedModifier is stored (the same want-only
	// posture as MeleeAttackGoal). The teleport branch (tryToTeleportToOwner) is the cited deferral.
	if e.ai != nil {
		e.ai.setWantTarget(owner.x, owner.y, owner.z)
	}
}

// Compile-time assertion: followOwnerGoal IS a server.Goal.
var _ Goal = (*followOwnerGoal)(nil)

// --- ownerHurtByTargetGoal -----------------------------------------------------------------------

// ownerHurtByTargetGoal ports net.minecraft.world.entity.ai.goal.target.OwnerHurtByTargetGoal (flags
// {TARGET}), NO RNG — the wolf targets whoever last hit its OWNER (owner.getLastHurtByMob), once per
// fresh hit (the timestamp guard). The owner-defense goal.
type ownerHurtByTargetGoal struct {
	baseGoal
	// timestamp is OwnerHurtByTargetGoal.timestamp — the owner.getLastHurtByMobTimestamp captured on the
	// last start(), so canUse fires only ONCE per fresh attack on the owner (ts == this.timestamp →
	// already responded → false).
	timestamp int32
	// ownerLastHurtBy caches the candidate resolved in canUse so start() can commit it (the jar's
	// ownerLastHurtBy field). 0 == none.
	ownerLastHurtBy int32
}

// newOwnerHurtByTargetGoal builds the goal with the TARGET flag (OwnerHurtByTargetGoal ctor:
// setFlags(EnumSet.of(TARGET))).
func newOwnerHurtByTargetGoal() *ownerHurtByTargetGoal {
	return &ownerHurtByTargetGoal{baseGoal: newBaseGoal(flagTarget)}
}

// canUse ports OwnerHurtByTargetGoal.canUse (bytecode-verified this session), NO RNG:
//
//	if (!isTame() || isOrderedToSit()) return false;
//	LivingEntity owner = getOwner();
//	if (owner == null) return false;
//	this.ownerLastHurtBy = owner.getLastHurtByMob();
//	int ts = owner.getLastHurtByMobTimestamp();
//	return ts != this.timestamp && canAttack(ownerLastHurtBy, DEFAULT) && wantsToAttack(ownerLastHurtBy, owner);
//
// CITE-DEFERRED: owner.getLastHurtByMob()/getLastHurtByMobTimestamp() — players carry no INBOUND
// lastHurtByMob bookkeeping in v1 (only mobs record their attacker at the combat store-point). So the
// owner-was-hit candidate is a cited constant-NONE (ownerLastHurtBy == 0), and this goal never acquires
// a target until player inbound-combat bookkeeping lands. The structure (the tame/sit gate, the owner
// resolve, the timestamp-once guard, the canAttack/wantsToAttack validity) is ported faithfully so the
// upgrade is a one-line read swap. canAttack/wantsToAttack reduce to "the candidate is a present, valid
// combat target" (the same existence gate hurtByTargetGoal uses) — cited no-ops for the TargetingConditions
// LoS/team filters v1 lacks.
//
//	[VERIFIED javap OwnerHurtByTargetGoal.canUse: isTame ifeq → 0 / isOrderedToSit ifne → 0; getOwner
//	 ifnull → 0; ownerLastHurtBy = owner.getLastHurtByMob(); ts = owner.getLastHurtByMobTimestamp();
//	 ts != this.timestamp && canAttack(ownerLastHurtBy, DEFAULT) && wantsToAttack(ownerLastHurtBy, owner).]
func (g *ownerHurtByTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if !e.tame || e.orderedToSit { // !isTame() || isOrderedToSit()
		return false
	}
	owner := t.playerByEntityID(e.ownerUUID) // getOwner()
	if owner == nil {
		return false
	}
	// owner.getLastHurtByMob() / getLastHurtByMobTimestamp(): cited constant-NONE (players have no inbound
	// bookkeeping in v1). ownerLastHurtBy stays 0 → the canAttack guard below fails → no target acquired.
	g.ownerLastHurtBy = ownerLastHurtByMobID(owner)
	ts := ownerLastHurtByMobTimestamp(owner)
	if ts == g.timestamp || g.ownerLastHurtBy == 0 {
		return false
	}
	// canAttack(ownerLastHurtBy) && wantsToAttack(...): the candidate must be a present, valid target.
	return t.playerByEntityID(g.ownerLastHurtBy) != nil || mobCandidateAlive(t, g.ownerLastHurtBy)
}

// start ports OwnerHurtByTargetGoal.start: mob.setTarget(ownerLastHurtBy); timestamp =
// owner.getLastHurtByMobTimestamp(); super.start(). It commits the attacker and stamps its OWN timestamp
// so canUse won't re-fire on the same hit. NO RNG.
//
//	[VERIFIED javap OwnerHurtByTargetGoal.start: mob.setTarget(ownerLastHurtBy); owner = getOwner();
//	 if (owner != null) timestamp = owner.getLastHurtByMobTimestamp(); TargetGoal.start().]
func (g *ownerHurtByTargetGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.ownerLastHurtBy)
	}
	if owner := t.playerByEntityID(e.ownerUUID); owner != nil {
		g.timestamp = ownerLastHurtByMobTimestamp(owner)
	}
}

// canContinueToUse ports the TargetGoal.canContinueToUse base: keep targeting while the target is a
// present, valid combat target. NO RNG.
func (g *ownerHurtByTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return targetStillValid(t, e)
}

// stop ports TargetGoal.stop: mob.setTarget(null). NO RNG.
func (g *ownerHurtByTargetGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// Compile-time assertion: ownerHurtByTargetGoal IS a server.Goal.
var _ Goal = (*ownerHurtByTargetGoal)(nil)

// --- ownerHurtTargetGoal -------------------------------------------------------------------------

// ownerHurtTargetGoal ports net.minecraft.world.entity.ai.goal.target.OwnerHurtTargetGoal (flags
// {TARGET}), NO RNG — the MIRROR of OwnerHurtByTargetGoal: the wolf targets whatever the OWNER last
// ATTACKED (owner.getLastHurtMob), once per fresh attack (the timestamp guard).
type ownerHurtTargetGoal struct {
	baseGoal
	// timestamp is OwnerHurtTargetGoal.timestamp — owner.getLastHurtMobTimestamp captured on the last
	// start(), gating the once-per-attack fire.
	timestamp int32
	// ownerLastHurt caches the candidate (the jar's ownerLastHurt field). 0 == none.
	ownerLastHurt int32
}

// newOwnerHurtTargetGoal builds the goal with the TARGET flag (OwnerHurtTargetGoal ctor:
// setFlags(EnumSet.of(TARGET))).
func newOwnerHurtTargetGoal() *ownerHurtTargetGoal {
	return &ownerHurtTargetGoal{baseGoal: newBaseGoal(flagTarget)}
}

// canUse ports OwnerHurtTargetGoal.canUse (bytecode-verified this session), NO RNG:
//
//	if (!isTame() || isOrderedToSit()) return false;
//	LivingEntity owner = getOwner();
//	if (owner == null) return false;
//	this.ownerLastHurt = owner.getLastHurtMob();         // what the OWNER ATTACKED (vs hurt-by)
//	int ts = owner.getLastHurtMobTimestamp();
//	return ts != this.timestamp && canAttack(ownerLastHurt, DEFAULT) && wantsToAttack(ownerLastHurt, owner);
//
// THE LIVE PATH: owner.getLastHurtMob()/getLastHurtMobTimestamp() ARE tracked in v1 — the OWNER-SIDE
// attack bookkeeping on tickPlayer, set when a player hits a mob (handleMobAttack, Task 1). So a tamed
// wolf retaliates against whatever its owner is fighting. canAttack/wantsToAttack reduce to "the
// candidate is a present, valid combat target" (the cited LoS/team no-ops). The candidate is a MOB id
// (the player attacked a mob) — resolved through the entity store.
//
//	[VERIFIED javap OwnerHurtTargetGoal.canUse: isTame ifeq → 0 / isOrderedToSit ifne → 0; getOwner
//	 ifnull → 0; ownerLastHurt = owner.getLastHurtMob(); ts = owner.getLastHurtMobTimestamp();
//	 ts != this.timestamp && canAttack(ownerLastHurt, DEFAULT) && wantsToAttack(ownerLastHurt, owner).]
func (g *ownerHurtTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if !e.tame || e.orderedToSit { // !isTame() || isOrderedToSit()
		return false
	}
	owner := t.playerByEntityID(e.ownerUUID) // getOwner()
	if owner == nil {
		return false
	}
	g.ownerLastHurt = owner.lastHurtMob // owner.getLastHurtMob() (the player's attack-side bookkeeping)
	ts := owner.lastHurtMobTimestamp     // owner.getLastHurtMobTimestamp()
	if ts == g.timestamp || g.ownerLastHurt == 0 {
		return false
	}
	// canAttack(ownerLastHurt) && wantsToAttack(...): the candidate (a mob id) must be present + alive +
	// not the wolf itself (a wolf does not turn on itself). The owner attacked a MOB, resolved via the store.
	if g.ownerLastHurt == e.id {
		return false
	}
	return mobCandidateAlive(t, g.ownerLastHurt)
}

// start ports OwnerHurtTargetGoal.start: mob.setTarget(ownerLastHurt); timestamp =
// owner.getLastHurtMobTimestamp(); super.start(). NO RNG.
//
//	[VERIFIED javap OwnerHurtTargetGoal.start: mob.setTarget(ownerLastHurt); owner = getOwner();
//	 if (owner != null) timestamp = owner.getLastHurtMobTimestamp(); TargetGoal.start().]
func (g *ownerHurtTargetGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.ownerLastHurt)
	}
	if owner := t.playerByEntityID(e.ownerUUID); owner != nil {
		g.timestamp = owner.lastHurtMobTimestamp
	}
}

// canContinueToUse ports the TargetGoal.canContinueToUse base. NO RNG.
func (g *ownerHurtTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return targetStillValid(t, e)
}

// stop ports TargetGoal.stop: mob.setTarget(null). NO RNG.
func (g *ownerHurtTargetGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// Compile-time assertion: ownerHurtTargetGoal IS a server.Goal.
var _ Goal = (*ownerHurtTargetGoal)(nil)

// --- shared owner-goal helpers -------------------------------------------------------------------

// ownerLastHurtByMobID / ownerLastHurtByMobTimestamp are the CITED constant-NONE stubs for a player
// owner's INBOUND lastHurtByMob bookkeeping (OwnerHurtByTargetGoal): players track no inbound
// lastHurtByMob in v1 (only mobs do, at the combat store-point), so the "who hit the owner" candidate is
// always absent. Named predicates (not inline 0) so the upgrade — adding lastHurtByMob to tickPlayer —
// slots in here with no canUse edit. The owner ATTACK side (getLastHurtMob, OwnerHurtTargetGoal) IS live
// (tickPlayer.lastHurtMob, set in handleMobAttack).
func ownerLastHurtByMobID(_ *tickPlayer) int32        { return 0 }
func ownerLastHurtByMobTimestamp(_ *tickPlayer) int32 { return 0 }

// mobCandidateAlive resolves a candidate MOB id (a target the owner attacked) through the OWNING-region
// entity store and reports whether it is present + alive — the canAttack(candidate) existence gate for a
// mob target (the mob-vs-mob analog of playerByEntityID != nil). Uses t.cur() (the region running the
// goal, the same store the goal's scans use, the v5 same-region cut). NO RNG.
func mobCandidateAlive(t *TickLoop, id int32) bool {
	if id == 0 {
		return false
	}
	other, ok := t.cur().entities.get(id)
	return ok && !other.dead
}

// targetStillValid ports the TargetGoal.canContinueToUse base shared by the owner-hurt goals: keep
// targeting while the acquired target (a player OR a mob) still resolves + is alive. A player target
// resolves via playerByEntityID; a mob target via the entity store. NO RNG.
func targetStillValid(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	if t.playerByEntityID(id) != nil {
		return true // a present player target
	}
	return mobCandidateAlive(t, id) // a present, alive mob target
}
