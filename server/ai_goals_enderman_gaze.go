package server

// ai_goals_enderman_gaze.go — MOB-HOST-08 (Task #9, gaze): the EnderMan GAZE aggro subsystem, ported
// 1:1 (idiomatic non-1:1 Go, no GPL paste) from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar,
// read via javap -c -p / CFR this session). This REPLACES the v1 substitution (which used the base
// kind="nearest_attackable_target" hunt) with the REAL "aggro when a player looks at the enderman"
// mechanic, plus the freeze-while-being-stared-at.
//
// Ported classes (all inner classes of net.minecraft.world.entity.monster.EnderMan):
//
//   - EnderMan.isBeingStaredBy(Player): the gaze predicate. CFR:
//         if (!LivingEntity.PLAYER_NOT_WEARING_DISGUISE_ITEM.test(player)) return false;
//         return isLookingAtMe(player, 0.025, true, false, this.getEyeY());
//     PLAYER_NOT_WEARING_DISGUISE_ITEM (the carved-pumpkin-head check) is a CITED no-op here (no
//     equipment/disguise subsystem in v1 — vanilla default: a player without a pumpkin head, so the
//     predicate is true). The real work is LivingEntity.isLookingAtMe.
//
//   - LivingEntity.isLookingAtMe(target, coneSize, adjustForDistance, seeThroughTransparentBlocks,
//     gazeHeights...): the ANGLE math. CFR (verbatim shape):
//         Vec3 look = target.getViewVector(1.0).normalize();
//         for (double gazeHeight : gazeHeights) {
//             Vec3 dir = new Vec3(this.getX()-target.getX(), gazeHeight-target.getEyeY(), this.getZ()-target.getZ());
//             double dist = dir.length();
//             dir = dir.normalize();
//             double dot = look.dot(dir);
//             double d = adjustForDistance ? dist : 1.0;
//             if (dot > 1.0 - coneSize/d && target.hasLineOfSight(this, ...)) return true;
//         }
//         return false;
//     Here `this` == the enderman, `target` == the player. isBeingStaredBy passes coneSize=0.025,
//     adjustForDistance=true, gazeHeights=[enderman.getEyeY()]. The hasLineOfSight raycast is
//     CITE-DEFERRED (no LoS/raycast subsystem in v1, exactly as the target goals defer it) -> gated at
//     "visible", so the pure ANGLE test is the active gate.
//
//   - Entity.getViewVector(1.0f) -> Entity.calculateViewVector(getViewXRot(1f), getViewYRot(1f)): the
//     player LOOK vector. CFR calculateViewVector(xRot, yRot):
//         float rx = xRot * (PI/180);  float ry = -yRot * (PI/180);
//         float yCos = cos(ry), ySin = sin(ry), xCos = cos(rx), xSin = sin(rx);
//         return new Vec3(ySin*xCos, -xSin, yCos*xCos);
//     xRot == pitch, yRot == yaw (both degrees). We mirror combat's attack_dispatch.go pattern for
//     Mth.sin/cos: float32(math.Sin(float64(deg*degToRad))) — the same faithful approximation the
//     knockback direction uses (Mth's lookup table vs math.Sin is the documented project-wide sin/cos
//     stub; identical observable direction for the dot-product gate).
//
//   - EnderMan.EndermanFreezeWhenLookedAt (goalSelector @1, flags {JUMP, MOVE}): freeze while a
//     looking player is the current target. canUse: target instanceof Player && distSqr<=256.0 &&
//     isBeingStaredBy(player). start: navigation.stop(). tick: lookControl.setLookAt(player eye).
//
//   - EnderMan.EndermanLookForPlayerGoal (targetSelector @1, extends NearestAttackableTargetGoal<Player>):
//     the gaze-AGGRO. isAngerInducing = (t) -> isBeingStaredBy(t) || isAngryAt(t). canUse:
//     getNearestPlayer(startAggroTargetConditions.range(followDistance)) != null (the nearest player
//     that satisfies isAngerInducing within FOLLOW_RANGE). start: aggroTime=adjustedTickDelay(5),
//     teleportTime=0, setBeingStaredAt(). tick: if pendingTarget != null, count aggroTime down and on
//     <=0 COMMIT it as the target (super.start()); else the teleport-in-combat management (already
//     Go-native via endermanAiStep/hurt hooks; the target-committed enderman then teleports through
//     the existing seams). The DATA_CREEPY/DATA_STARED_AT synched flags are set at setTarget /
//     setBeingStaredAt — CITE-DEFERRED (no entity-data synch for the enderman's creepy/stared bits in
//     v1; they drive only the client render + the stare sound, not the aggro logic). The teleport-toward
//     / teleport-when-far management inside LookForPlayerGoal.tick is CITE-DEFERRED to the existing
//     Go-native teleport (endermanTeleport/endermanHurtTeleport) — the aggro (setTarget) is the piece
//     this file builds; the melee goal then engages the committed target.

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
)

// endermanGazeConeSize is the coneSize EnderMan.isBeingStaredBy passes to isLookingAtMe: the literal
// 0.025 (javap ldc2_w double 0.025d). It is distance-adjusted (the threshold is 1.0 - 0.025/dist), so
// the further the player, the TIGHTER the required aim — vanilla's "stare directly at the enderman".
//
//	[VERIFIED javap EnderMan.isBeingStaredBy: ldc2_w #367 // double 0.025d; iconst_1 (adjustForDistance);
//	 iconst_0 (seeThroughTransparentBlocks); gazeHeights = { getEyeY() }; invokevirtual isLookingAtMe.]
const endermanGazeConeSize = 0.025

// endermanFreezeMaxDistSqr is EndermanFreezeWhenLookedAt.canUse's distance-squared cap: 256.0 (16 sq).
// A staring player farther than 16 blocks does not freeze the enderman.
//
//	[VERIFIED CFR EndermanFreezeWhenLookedAt.canUse: if (this.target.distanceToSqr(this.enderman) > 256.0) return false.]
const endermanFreezeMaxDistSqr = 256.0

// endermanEyeHeightFactor is EntityDimensions.defaultEyeHeight's 0.85f (javap EntityDimensions.<init>:
// defaultEyeHeight = height * 0.85f). The enderman's getEyeY() == y + Height*0.85 (Height 2.9 -> 2.465).
const endermanEyeHeightFactor = 0.85

// endermanHeight is the enderman bounding-box height (data/entity Enderman.Height 2.9), used for
// getEyeY(). Pinned here so the gaze anchor is the real vanilla eye height, not a feet-anchored stub —
// isLookingAtMe's dir y-component is (gazeHeight - player.getEyeY()), so both eye heights are load-bearing.
const endermanHeight = 2.9

// endermanEyeY ports EnderMan(Entity).getEyeY() for the gaze anchor: y + Height*0.85 (the sole
// gazeHeight isBeingStaredBy feeds isLookingAtMe). Cite EntityDimensions.defaultEyeHeight * height.
func endermanEyeY(e *Entity) float64 {
	return e.y + endermanHeight*endermanEyeHeightFactor
}

// playerViewVector ports Entity.getViewVector(1.0).normalize() for a player: the unit direction the
// player is LOOKING, from its yaw+pitch (degrees). It mirrors Entity.calculateViewVector EXACTLY:
//
//	rx = pitch*(PI/180);  ry = -yaw*(PI/180);
//	return Vec3(sin(ry)*cos(rx), -sin(rx), cos(ry)*cos(rx));
//
// The result is already unit-length (a rotation of a unit vector), so .normalize() is a no-op (vanilla
// still calls it; we skip the redundant divide — an OPTIMIZATION that preserves the value). Uses the
// combat degToRad + the float32(math.Sin(...)) Mth.sin/cos stub the knockback direction uses.
//
//	[VERIFIED CFR Entity.calculateViewVector: rx=xRot*(PI/180); ry=-yRot*(PI/180); yCos=cos(ry);
//	 ySin=sin(ry); xCos=cos(rx); xSin=sin(rx); return new Vec3(ySin*xCos, -xSin, yCos*xCos).]
func playerViewVector(yaw, pitch float32) (vx, vy, vz float64) {
	rx := pitch * degToRad
	ry := -yaw * degToRad
	yCos := float32(math.Cos(float64(ry)))
	ySin := float32(math.Sin(float64(ry)))
	xCos := float32(math.Cos(float64(rx)))
	xSin := float32(math.Sin(float64(rx)))
	return float64(ySin * xCos), float64(-xSin), float64(yCos * xCos)
}

// endermanIsLookingAtMe ports LivingEntity.isLookingAtMe for the enderman gaze (this == enderman e,
// target == player p, coneSize == 0.025, adjustForDistance == true, gazeHeights == [enderman.getEyeY()]).
// It computes the player's view vector, the (enderman - player) direction at the enderman's eye height,
// and returns whether the player is aiming inside the distance-adjusted cone. NO RNG.
//
// The single-gazeHeight loop of the jar collapses to one iteration (isBeingStaredBy passes exactly one
// gazeHeight). hasLineOfSight is CITE-DEFERRED (visible) — same as the target goals' LoS deferral.
//
//	[VERIFIED CFR LivingEntity.isLookingAtMe: look = target.getViewVector(1).normalize(); dir = new Vec3(
//	 getX()-target.getX(), gazeHeight-target.getEyeY(), getZ()-target.getZ()); dist = dir.length();
//	 dir = dir.normalize(); dot = look.dot(dir); d = adjustForDistance? dist : 1.0;
//	 return dot > 1.0 - coneSize/d && hasLineOfSight(...).]
func endermanIsLookingAtMe(e *Entity, p *tickPlayer) bool {
	// look = player.getViewVector(1.0).normalize()
	lx, ly, lz := playerViewVector(p.yaw, p.pitch)

	// dir = (enderman.getX() - player.getX(), gazeHeight - player.getEyeY(), enderman.getZ() - player.getZ())
	// where gazeHeight == enderman.getEyeY(); player.getEyeY() == p.y + playerStandingEyeHeight.
	dx := e.x - p.x
	dy := endermanEyeY(e) - (p.y + playerStandingEyeHeight)
	dz := e.z - p.z

	// dist = dir.length()
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist == 0 {
		return false // degenerate (co-located): normalize would divide by zero; not "looking at me"
	}
	// dir = dir.normalize()
	nx, ny, nz := dx/dist, dy/dist, dz/dist

	// dot = look.dot(dir)
	dot := lx*nx + ly*ny + lz*nz

	// d = adjustForDistance ? dist : 1.0  (adjustForDistance == true for the gaze)
	d := dist
	// return dot > 1.0 - coneSize/d  [&& hasLineOfSight — CITE-DEFERRED visible]
	return dot > 1.0-endermanGazeConeSize/d
}

// endermanIsBeingStaredBy ports EnderMan.isBeingStaredBy(Player): the PLAYER_NOT_WEARING_DISGUISE_ITEM
// gate (a cited no-op — no disguise/equipment subsystem; vanilla default is "not wearing a pumpkin head"
// -> true) then the isLookingAtMe angle test at coneSize 0.025, distance-adjusted, at the enderman eye.
//
//	[VERIFIED CFR EnderMan.isBeingStaredBy: if (!PLAYER_NOT_WEARING_DISGUISE_ITEM.test(player)) return false;
//	 return isLookingAtMe(player, 0.025, true, false, this.getEyeY()).]
func endermanIsBeingStaredBy(e *Entity, p *tickPlayer) bool {
	// PLAYER_NOT_WEARING_DISGUISE_ITEM.test(player): a cited constant-true no-op (no pumpkin-head disguise
	// subsystem in v1 — vanilla default, so the predicate holds and we fall through to the angle test).
	return endermanIsLookingAtMe(e, p)
}

// --- endermanFreezeWhenLookedAtGoal (goalSelector @1, flags {JUMP, MOVE}) --------------------------

// endermanFreezeWhenLookedAtGoal ports EnderMan.EndermanFreezeWhenLookedAt: while the enderman's
// current target is a player who is staring at it (within 16 blocks), the enderman FREEZES (stops
// navigating) and stares back. It holds {JUMP, MOVE} so it out-arbitrates the stroll/melee movement,
// pinning the enderman in place. NO RNG.
type endermanFreezeWhenLookedAtGoal struct {
	baseGoal
	// target is EndermanFreezeWhenLookedAt.target: the current player target captured in canUse (the
	// enderman's getTarget resolved to a player), re-read for the stare-back in tick. 0 == none.
	target int32
}

// newEndermanFreezeWhenLookedAtGoal builds the freeze goal with flags {JUMP, MOVE} (the ctor's
// setFlags(EnumSet.of(JUMP, MOVE))).
//
//	[VERIFIED CFR EndermanFreezeWhenLookedAt.<init>: setFlags(EnumSet.of(Goal.Flag.JUMP, Goal.Flag.MOVE)).]
func newEndermanFreezeWhenLookedAtGoal() *endermanFreezeWhenLookedAtGoal {
	return &endermanFreezeWhenLookedAtGoal{baseGoal: newBaseGoal(flagJump | flagMove)}
}

// canUse ports EndermanFreezeWhenLookedAt.canUse:
//
//	this.target = enderman.getTarget();
//	if (!(target instanceof Player)) return false;
//	if (target.distanceToSqr(enderman) > 256.0) return false;
//	return enderman.isBeingStaredBy((Player) target);
//
// v1: getTarget() is the enderman's attackTargetID resolved via playerByEntityID (a mob target is not a
// player -> the instanceof Player fails -> false). NO RNG.
//
//	[VERIFIED CFR EndermanFreezeWhenLookedAt.canUse: target = enderman.getTarget(); if (!(target
//	 instanceof Player)) return false; if (target.distanceToSqr(enderman) > 256.0) return false;
//	 return enderman.isBeingStaredBy((Player) target).]
func (g *endermanFreezeWhenLookedAtGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	// target instanceof Player: the current target must resolve to a PLAYER (a mob target -> not a player -> false).
	p := t.playerByEntityID(id)
	if p == nil {
		return false
	}
	g.target = id
	// distanceToSqr(enderman) > 256.0 -> false.
	dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
	if dx*dx+dy*dy+dz*dz > endermanFreezeMaxDistSqr {
		return false
	}
	// isBeingStaredBy(player): the gaze angle test.
	return endermanIsBeingStaredBy(e, p)
}

// canContinueToUse: the base Goal.canContinueToUse re-checks canUse (EndermanFreezeWhenLookedAt does
// not override it, so it defaults to Goal.canContinueToUse == canUse). NO RNG.
//
//	[VERIFIED CFR EndermanFreezeWhenLookedAt has no canContinueToUse override -> Goal.canContinueToUse
//	 delegates to canUse.]
func (g *endermanFreezeWhenLookedAtGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// start ports EndermanFreezeWhenLookedAt.start: enderman.getNavigation().stop(). Park the enderman —
// it freezes in place while being stared at. NO RNG.
//
//	[VERIFIED CFR EndermanFreezeWhenLookedAt.start: this.enderman.getNavigation().stop().]
func (g *endermanFreezeWhenLookedAtGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget() // navigation.stop()
	}
}

// tick ports EndermanFreezeWhenLookedAt.tick: enderman.getLookControl().setLookAt(target.getX(),
// target.getEyeY(), target.getZ()). The enderman stares back at the player's eyes (headYaw only, the
// LookControl seam — same as the melee/look goals). NO RNG.
//
//	[VERIFIED CFR EndermanFreezeWhenLookedAt.tick: enderman.getLookControl().setLookAt(target.getX(),
//	 target.getEyeY(), target.getZ()).]
func (g *endermanFreezeWhenLookedAtGoal) tick(t *TickLoop, e *Entity) {
	if g.target == 0 {
		return
	}
	p := t.playerByEntityID(g.target)
	if p == nil {
		return
	}
	// setLookAt(target eye): aim the head toward the player. headYaw only (the LookControl seam); the
	// body/navigation is stopped by start (frozen). The setLookAt(x,y,z) overload uses the default
	// head-turn, so v1 snaps the headYaw toward the target (the melee/look goals' headYaw seam).
	e.headYaw = yawTowardDeg(p.x-e.x, p.z-e.z)
}

// stop clears the captured target (no jar stop override; TargetGoal-style cleanup so a stale id does
// not stare-back after the freeze ends). NO RNG.
func (g *endermanFreezeWhenLookedAtGoal) stop(_ *TickLoop, _ *Entity) {
	g.target = 0
}

// --- endermanLookForPlayerGoal (targetSelector @1, extends NearestAttackableTargetGoal<Player>) -----

// endermanLookAggroDelay is EndermanLookForPlayerGoal.start's aggroTime = adjustedTickDelay(5): the
// enderman "winds up" for 5 ticks after spotting a staring player before it COMMITS the aggro (the
// menacing pause before it attacks). adjustedTickDelay is identity in v1 (ai_goals_breed.go).
//
//	[VERIFIED CFR EndermanLookForPlayerGoal.start: this.aggroTime = this.adjustedTickDelay(5).]
const endermanLookAggroDelay = 5

// endermanLookForPlayerGoal ports EnderMan.EndermanLookForPlayerGoal (extends
// NearestAttackableTargetGoal<Player>, flags {TARGET}). It scans for the nearest player who is either
// STARING at the enderman (isBeingStaredBy) or the enderman is already angry at (isAngryAt), within
// FOLLOW_RANGE. canUse acquires that player as pendingTarget; after a 5-tick wind-up (aggroTime) tick
// commits it as the real attack target (setTarget), which the MeleeAttackGoal then engages.
//
// This is the REAL gaze-aggro that REPLACES the v1 substitution (the plain nearest-target hunt): a
// player only aggroes the enderman by looking at it (or by having already angered it).
type endermanLookForPlayerGoal struct {
	baseGoal
	// pendingTarget is EndermanLookForPlayerGoal.pendingTarget: the player acquired in canUse, held
	// during the aggroTime wind-up, then committed to the attack target in tick. 0 == none.
	pendingTarget int32
	// aggroTime is EndermanLookForPlayerGoal.aggroTime: the wind-up countdown set in start(); on <=0
	// tick commits pendingTarget as the attack target.
	aggroTime int
	// forceTrigger is a test seam mirroring nearestAttackableTargetGoal.forceTrigger — it is a no-op
	// here (this goal has NO RNG gate; the jar's EnderMan override REPLACES the base randomInterval
	// canUse with a plain nearest-player scan), kept only for the shared native-goal harness shape.
	forceTrigger bool
}

// newEndermanLookForPlayerGoal builds the gaze-aggro goal with flags {TARGET}
// (NearestAttackableTargetGoal ctor: setFlags(EnumSet.of(TARGET))).
//
//	[VERIFIED CFR EndermanLookForPlayerGoal extends NearestAttackableTargetGoal<Player>(enderman,
//	 Player.class, 10, false, false, isAngryAt); NearestAttackableTargetGoal.<init> -> setFlags(TARGET).]
func newEndermanLookForPlayerGoal() *endermanLookForPlayerGoal {
	return &endermanLookForPlayerGoal{baseGoal: newBaseGoal(flagTarget)}
}

// isAngerInducing ports EndermanLookForPlayerGoal.isAngerInducing:
//
//	(target, level) -> (enderman.isBeingStaredBy(target) || enderman.isAngryAt(target, level))
//	                   && !enderman.hasIndirectPassenger(target)
//
// The player induces aggro iff they are STARING at the enderman OR the enderman is already angry at
// them. hasIndirectPassenger is a CITED no-op (no riding/passenger subsystem in v1; a player is never
// the enderman's passenger). NO RNG.
//
//	[VERIFIED CFR EndermanLookForPlayerGoal ctor: isAngerInducing = (target, level) ->
//	 (enderman.isBeingStaredBy((Player) target) || enderman.isAngryAt(target, level))
//	 && !enderman.hasIndirectPassenger(target).]
func (g *endermanLookForPlayerGoal) isAngerInducing(t *TickLoop, e *Entity, p *tickPlayer) bool {
	if endermanIsBeingStaredBy(e, p) {
		return true
	}
	// isAngryAt(target, level): the enderman is already angry at this player (NeutralMob.isAngryAt —
	// live persistent anger targeting this id). Reuses the shared isAngryAt port (ai_goals_target.go).
	return isAngryAt(t, e, p.entityID)
}

// canUse ports EndermanLookForPlayerGoal.canUse:
//
//	this.pendingTarget = getServerLevel(enderman).getNearestPlayer(
//	    startAggroTargetConditions.range(getFollowDistance()), enderman);
//	return this.pendingTarget != null;
//
// startAggroTargetConditions = forCombat().range(followDistance).selector(isAngerInducing). We scan the
// players within FOLLOW_RANGE and pick the NEAREST that satisfies isAngerInducing (the selector filter).
// NO RNG (unlike the base NearestAttackableTargetGoal, EnderMan's override REPLACES canUse with a plain
// nearest-player scan — there is NO randomInterval gate here; verified by the CFR: canUse does not draw).
//
//	[VERIFIED CFR EndermanLookForPlayerGoal.canUse: pendingTarget = getServerLevel(enderman)
//	 .getNearestPlayer(startAggroTargetConditions.range(getFollowDistance()), enderman); return
//	 pendingTarget != null. (No mob.getRandom().nextInt gate — the base canUse is overridden.)]
func (g *endermanLookForPlayerGoal) canUse(t *TickLoop, e *Entity) bool {
	follow := e.getAttributeValue(attribute.FollowRange)
	g.pendingTarget = 0
	best := follow * follow
	for _, p := range t.players {
		if p == nil {
			continue
		}
		dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 > best {
			continue
		}
		// selector(isAngerInducing): only a staring / already-angered player is a candidate.
		if !g.isAngerInducing(t, e, p) {
			continue
		}
		best = d2
		g.pendingTarget = p.entityID
	}
	return g.pendingTarget != 0
}

// canContinueToUse ports EndermanLookForPlayerGoal.canContinueToUse:
//
//	if (pendingTarget != null) {
//	    if (!isAngerInducing.test(pendingTarget, level)) return false;
//	    enderman.lookAt(pendingTarget, 10, 10);
//	    return true;
//	}
//	if (target != null) { ... continueAggroTargetConditions ... }
//	return super.canContinueToUse();
//
// v1: while a pendingTarget is still winding up, keep the goal running iff it is still anger-inducing
// (still staring / still angered), staring back at it. Once committed (pendingTarget cleared, the
// attack target set), the goal keeps running while the attack target remains a valid in-range player
// (the shared TargetGoal.canContinueToUse shape). NO RNG.
//
//	[VERIFIED CFR EndermanLookForPlayerGoal.canContinueToUse per the excerpt above.]
func (g *endermanLookForPlayerGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if g.pendingTarget != 0 {
		p := t.playerByEntityID(g.pendingTarget)
		if p == nil || !g.isAngerInducing(t, e, p) {
			return false
		}
		// enderman.lookAt(pendingTarget, 10, 10): stare toward the winding-up player (headYaw seam).
		e.headYaw = yawTowardDeg(p.x-e.x, p.z-e.z)
		return true
	}
	// Committed: keep running while the attack target is a present, in-range player (TargetGoal base).
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	p := t.playerByEntityID(id)
	if p == nil {
		return false
	}
	follow := e.getAttributeValue(attribute.FollowRange)
	dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
	return dx*dx+dy*dy+dz*dz <= follow*follow
}

// start ports EndermanLookForPlayerGoal.start:
//
//	this.aggroTime = adjustedTickDelay(5);
//	this.teleportTime = 0;
//	this.enderman.setBeingStaredAt();
//
// It starts the 5-tick wind-up. teleportTime (the in-combat teleport cadence) is CITE-DEFERRED to the
// Go-native teleport hooks. setBeingStaredAt() sets DATA_STARED_AT (a client-render/stare-sound synched
// flag) — CITE-DEFERRED (no entity-data synch for the stared bit in v1; it does not gate the aggro). NO RNG.
//
//	[VERIFIED CFR EndermanLookForPlayerGoal.start: aggroTime = adjustedTickDelay(5); teleportTime = 0;
//	 enderman.setBeingStaredAt().]
func (g *endermanLookForPlayerGoal) start(_ *TickLoop, _ *Entity) {
	g.aggroTime = adjustedTickDelay(endermanLookAggroDelay)
	// teleportTime = 0 (cite-deferred to Go-native teleport); setBeingStaredAt() (cite-deferred synched flag).
}

// tick ports EndermanLookForPlayerGoal.tick's pendingTarget path (the aggro COMMIT):
//
//	if (enderman.getTarget() == null) super.setTarget(null);
//	if (pendingTarget != null) {
//	    if (--aggroTime <= 0) { this.target = pendingTarget; pendingTarget = null; super.start(); }
//	} else { ... in-combat teleport management ... super.tick(); }
//
// v1 builds the COMMIT (super.start() == NearestAttackableTargetGoal.start == mob.setTarget(target)):
// after the 5-tick wind-up, the pending staring player becomes the enderman's real attack target, which
// the MeleeAttackGoal then engages. The else-branch's teleport-toward / teleport-when-far management is
// CITE-DEFERRED to the Go-native endermanAiStep/endermanHurtTeleport (the aggro is this file's job). NO RNG.
//
//	[VERIFIED CFR EndermanLookForPlayerGoal.tick pendingTarget path: if (--aggroTime <= 0) { target =
//	 pendingTarget; pendingTarget = null; super.start(); } — super.start() == mob.setTarget(target).]
func (g *endermanLookForPlayerGoal) tick(_ *TickLoop, e *Entity) {
	if g.pendingTarget != 0 {
		g.aggroTime--
		if g.aggroTime <= 0 {
			// COMMIT: this.target = pendingTarget; super.start() == mob.setTarget(target).
			if e.ai != nil {
				e.ai.setTarget(g.pendingTarget)
			}
			g.pendingTarget = 0
		}
	}
	// else-branch (in-combat teleport-toward / teleport-when-far): CITE-DEFERRED to the Go-native
	// endermanAiStep daylight-flee + endermanHurtTeleport dodge (ai_goals_enderman.go).
}

// stop ports NearestAttackableTargetGoal.stop via EndermanLookForPlayerGoal.stop (pendingTarget = null;
// super.stop() == TargetGoal.stop == mob.setTarget(null)). Clear the wind-up + the attack target so the
// downstream melee goal disengages when the player stops staring / leaves. NO RNG.
//
//	[VERIFIED CFR EndermanLookForPlayerGoal.stop: this.pendingTarget = null; super.stop().
//	 NearestAttackableTargetGoal has no stop override -> TargetGoal.stop: mob.setTarget(null).]
func (g *endermanLookForPlayerGoal) stop(_ *TickLoop, e *Entity) {
	g.pendingTarget = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}
