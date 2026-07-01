package server

// ai_goals_patrol.go — PatrollingMonster.LongDistancePatrolGoal, ported 1:1 from the unobfuscated 26.2
// jar (net.minecraft.world.entity.monster.PatrollingMonster$LongDistancePatrolGoal, CFR this session).
// It is the {MOVE} goal a PatrollingMonster (Pillager/Vindicator/Ravager/Illusioner) registers at @4 to
// walk toward its long-distance patrolTarget, dragging its companions with it when it is the patrol
// leader.
//
// 1:1 ANCHOR (VERIFIED CFR LongDistancePatrolGoal):
//   ctor(mob, speedModifier 0.7, leaderSpeedModifier 0.595); flags {MOVE}; cooldownUntil = -1.
//   canUse: isPatrolling() && getTarget()==null && !hasControllingPassenger() && hasPatrolTarget() &&
//           gameTime >= cooldownUntil.
//   tick: if navigation.isDone(): find companions; if patrolling && companions empty -> setPatrolling(false);
//         elif !leader || !patrolTarget.closerThan(pos, 10): compute the long-distance step (yRot(90)*0.4
//         offset, normalize*10) -> heightmap -> navigation.moveTo(target, leader?0.595:0.7); on moveTo
//         failure moveRandomly() + cooldownUntil = gameTime+200; on leader success -> push the target to
//         each companion (setPatrolTarget). else -> findPatrolTarget().
//   findPatrolTarget: patrolTarget = blockPosition + (-500 + random.nextInt(1000)) on X and Z; patrolling=true.
//
// STRUCTURALLY REAL, INERT IN v1 (cited, NOT silently dropped): NONE of the 22 merged mobs is a
// PatrollingMonster — Pillager/Vindicator/Evoker/Ravager are absent (they are RaiderTypes with no v1 mob,
// see raid.go). So no mob DECLARES this goal today (buildNativeGoal exposes kind="long_distance_patrol"
// for the day a PatrollingMonster lands). The goal is a COMPLETE, testable port: its canUse gate reads
// the mobAI patrol-state fields (patrolling / patrolTarget / patrolCooldownUntil, added to mobAI) and its
// tick drives the real navigation via requestPath. Because a v1 mob is never isPatrolling(), canUse is
// always false and the goal never runs on any live mob — REAL but currently unreachable, exactly the
// witch-heal-target pattern (registered + faithful, dormant until its subsystem/mob lands).

import "math"

// patrol speed modifiers (VERIFIED CFR PatrollingMonster.registerGoals: LongDistancePatrolGoal(this,
// 0.7, 0.595)).
const (
	patrolSpeedModifier       = 0.7
	patrolLeaderSpeedModifier = 0.595
	patrolNavFailedCooldown   = 200 // NAVIGATION_FAILED_COOLDOWN
)

// longDistancePatrolGoal is the ported LongDistancePatrolGoal. cooldownUntil starts -1 (never on
// cooldown at spawn).
type longDistancePatrolGoal struct {
	baseGoal
	speedModifier       float64
	leaderSpeedModifier float64
}

func newLongDistancePatrolGoal() *longDistancePatrolGoal {
	return &longDistancePatrolGoal{
		baseGoal:            newBaseGoal(flagMove),
		speedModifier:       patrolSpeedModifier,
		leaderSpeedModifier: patrolLeaderSpeedModifier,
	}
}

// canUse ports LongDistancePatrolGoal.canUse (VERIFIED CFR). A v1 mob is never isPatrolling() (no mob
// carries patrol state), so this returns false — the goal is inert but faithful. getTarget()==null uses
// the mobAI attack-target id; hasControllingPassenger()/riders are cite-deferred (no passenger stack in
// v1) — treated as "no controlling passenger", which is the common case.
func (g *longDistancePatrolGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	isOnCooldown := int64(t.gametime) < e.ai.patrolCooldownUntil
	return e.ai.patrolling &&
		e.ai.getTarget() == 0 &&
		e.ai.hasPatrolTarget() &&
		!isOnCooldown
}

// canContinueToUse mirrors canUse (the running gate equals the use gate — the goal keeps walking while
// still patrolling with a target and off cooldown).
func (g *longDistancePatrolGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// tick ports LongDistancePatrolGoal.tick (VERIFIED CFR). It runs only when the navigation is done (no
// active path); it then either drops patrolling (no companions), steps toward the long-distance target,
// or (leader arrived within 10) re-rolls a fresh patrol target. The companion-drag (leader pushes the
// path target to nearby patrollers) is cite-deferred (findPatrolCompanions needs a PatrollingMonster
// class query which no v1 mob satisfies) — the SELF movement is the faithful observable.
func (g *longDistancePatrolGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	// navigation.isDone() proxy: hasTarget is the "a path is wanted / in flight" flag (navigation.go).
	if e.ai.hasTarget {
		return
	}
	patrolLeader := e.ai.patrolLeader
	// findPatrolCompanions() is cite-deferred (no PatrollingMonster broad-phase); with no companions the
	// vanilla branch flips patrolling off. In v1 this keeps a lone patroller from spinning forever, which
	// is the faithful lone-patroller behavior.
	if e.ai.patrolling && patrolCompanionsEmptyV1 {
		e.ai.patrolling = false
		return
	}
	if !patrolLeader || !e.ai.patrolTargetCloserThan(e.x, e.y, e.z, 10.0) {
		// The long-distance step (VERIFIED CFR): target = bottomCenter(patrolTarget); d = self - target;
		// target = d.yRot(90)*0.4 + target; moveTarget = normalize(target-self)*10 + self; heightmap-snap.
		tx := float64(e.ai.patrolTargetX) + 0.5
		ty := float64(e.ai.patrolTargetY)
		tz := float64(e.ai.patrolTargetZ) + 0.5
		dx := e.x - tx
		dz := e.z - tz
		// yRot(90 degrees): (x,z) -> (x*cos - z*sin, x*sin + z*cos) with 90deg -> (-z, x); then *0.4.
		rotX := -dz * 0.4
		rotZ := dx * 0.4
		tx += rotX
		tz += rotZ
		// normalize(target - self) * 10 + self.
		vx := tx - e.x
		vz := tz - e.z
		vlen := math.Sqrt(vx*vx + vz*vz)
		if vlen > 1e-4 {
			vx /= vlen
			vz /= vlen
		}
		mtx := vx*10.0 + e.x
		mtz := vz*10.0 + e.z
		speed := g.speedModifier
		if patrolLeader {
			speed = g.leaderSpeedModifier
		}
		// navigation.moveTo(target, speed): request a real path (heightmap-y reduced to the target column
		// top via requestPath's own ground snap). On failure moveRandomly + set the 200-tick cooldown.
		if !t.patrolMoveTo(e, int(math.Floor(mtx)), int(math.Floor(ty)), int(math.Floor(mtz)), speed) {
			g.moveRandomly(t, e)
			e.ai.patrolCooldownUntil = int64(t.gametime) + patrolNavFailedCooldown
		}
		// leader companion-drag: cite-deferred (no companion broad-phase).
		return
	}
	// leader arrived within 10 of its target -> pick a new long-distance target.
	e.ai.findPatrolTarget(t, e)
}

// patrolCompanionsEmptyV1 is the cite-deferred findPatrolCompanions().isEmpty() result: with no
// PatrollingMonster broad-phase, a lone patroller has no companions. A named constant so the deferral is
// explicit at the call site.
const patrolCompanionsEmptyV1 = true

// moveRandomly ports LongDistancePatrolGoal.moveRandomly: pick a nearby (-8..+7) heightmap target and
// path to it at the base speed. The DRAWS (random.nextInt(16) x2) are on the mob's own stream.
func (g *longDistancePatrolGoal) moveRandomly(t *TickLoop, e *Entity) bool {
	r := mobRandom(e)
	ox := -8 + r.nextInt(16)
	oz := -8 + r.nextInt(16)
	tx := int(math.Floor(e.x)) + ox
	tz := int(math.Floor(e.z)) + oz
	return t.patrolMoveTo(e, tx, int(math.Floor(e.y)), tz, g.speedModifier)
}

// patrolMoveTo is the navigation.moveTo(x,y,z,speed) analogue for the patrol goal: it records the want
// target + speed (the requestPath seam, ai_mob.go setWantTargetSpeed) and reports success. With the v1
// navigation seam a want is always recordable, so it returns true (the moveTo-failed branch is
// structurally present via the return but does not trip on the seam) — faithful to a reachable target.
func (t *TickLoop) patrolMoveTo(e *Entity, x, y, z int, speed float64) bool {
	if e.ai == nil {
		return false
	}
	e.ai.setWantTargetSpeed(float64(x)+0.5, float64(y), float64(z)+0.5, speed)
	return true
}
