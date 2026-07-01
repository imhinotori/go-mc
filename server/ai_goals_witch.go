package server

// ai_goals_witch.go — MOB-HOST-07 (Task #9): the Witch's generic RangedAttackGoal + performRangedAttack
// (splash-potion throw), ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this
// session):
//
//   - net.minecraft.world.entity.ai.goal.RangedAttackGoal: flags {MOVE, LOOK}, requiresUpdateEveryTick.
//     Witch builds it as new RangedAttackGoal(this, 1.0, 60, 10.0) → speedModifier 1.0, interval fixed 60,
//     radius 10 → radiusSqr 100. tick: chase while out of range / seeTime<5, else stop; look; on attackTime
//     hitting 0 with LoS → performRangedAttack; re-arm attackTime = floor(dist*(max-min)+min) (min==max==60).
//   - Witch.performRangedAttack: the potion-selection ladder (HARMING default; SLOWNESS if dist≥8 & !slow;
//     POISON if health≥8 & !poison; WEAKNESS if dist≤3 & !weak & rand<0.25) → spawn a ThrownSplashPotion.
//
// v1 STUBS (cited): line-of-sight always-true (no sensing); the Raider heal/regeneration branch is deferred
// (no raids in v1 — the witch only ever faces a player here). The WITCH_THROW sound + the drinking-potion
// self-buff are cite-deferred.

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
)

// RangedAttackGoal constants (verified CFR — the witch's ctor args).
const (
	witchRangedSpeed      = 1.0
	witchAttackInterval   = 60   // Witch RangedAttackGoal(this, 1.0, 60, 10.0)
	witchAttackRadius     = 10.0 // → radiusSqr 100
	witchAttackRadiusSqr  = witchAttackRadius * witchAttackRadius
	witchLaunchUncertainty = 8.0 // performRangedAttack uncertainty arg
)

// rangedAttackGoal is the ported generic RangedAttackGoal (net.minecraft.world.entity.ai.goal.RangedAttackGoal).
type rangedAttackGoal struct {
	baseGoal
	attackTime int // RangedAttackGoal.attackTime (start -1)
	seeTime    int
}

func newWitchRangedAttackGoal() *rangedAttackGoal {
	return &rangedAttackGoal{baseGoal: newBaseGoal(flagMove | flagLook), attackTime: -1}
}

func (g *rangedAttackGoal) requiresUpdateEveryTick() bool { return true }

// canUse: target != null && target.isAlive().
func (g *rangedAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	p := t.playerByEntityID(id)
	return p != nil && !p.dead
}

// canContinueToUse: canUse || (target alive && !navigation.isDone()).
func (g *rangedAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	return g.canUse(t, e) || e.ai.navigation.active()
}

func (g *rangedAttackGoal) start(t *TickLoop, e *Entity) {}

func (g *rangedAttackGoal) stop(t *TickLoop, e *Entity) {
	g.seeTime = 0
	g.attackTime = -1
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// tick is the port of RangedAttackGoal.tick.
func (g *rangedAttackGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	id := mobTarget(e)
	if id == 0 {
		return
	}
	target := t.playerByEntityID(id)
	if target == nil {
		return
	}
	targetDistSqr := distanceToSqrPlayer(target, e)
	hasLineOfSight := true // v1 always-true stub
	if hasLineOfSight {
		g.seeTime++
	} else {
		g.seeTime = 0
	}

	if targetDistSqr > witchAttackRadiusSqr || g.seeTime < 5 {
		getSpeed := e.getAttributeValue(attribute.MovementSpeed) * witchRangedSpeed
		e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed) // navigation.moveTo(target, speed)
	} else {
		e.ai.clearWantTarget() // navigation.stop()
	}

	// lookAt(target, 30, 30): head-only turn.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, meleeLookMaxYawStep)

	g.attackTime--
	if g.attackTime == 0 {
		if !hasLineOfSight {
			return
		}
		dist := math.Sqrt(targetDistSqr) / witchAttackRadius
		power := dist
		if power < 0.1 {
			power = 0.1
		} else if power > 1.0 {
			power = 1.0
		}
		t.performWitchRangedAttack(e, target, power)
		// attackTime = floor(dist*(max-min)+min); min==max==60 → 60.
		g.attackTime = witchAttackInterval
	} else if g.attackTime < 0 {
		// attackTime = floor(lerp(sqrt(distSqr)/radius, min, max)); min==max==60 → 60.
		g.attackTime = witchAttackInterval
	}
}

// performWitchRangedAttack is the port of Witch.performRangedAttack: choose a harmful potion by the vanilla
// ladder and spawn a ThrownSplashPotion at the target. The Raider heal/regeneration branch is deferred
// (v1 target is always a player). Cite Witch.performRangedAttack + Projectile.getMovementToShoot.
func (t *TickLoop) performWitchRangedAttack(e *Entity, target *tickPlayer, power float64) {
	r := mobRandom(e)

	// Aim: target.getEyeY() - 1.1 - getY() (targetMovement lead omitted — v1 has no player delta cache).
	xd := target.x - e.x
	yd := (target.y + float64(playerHeight)*0.85 - 1.1) - e.y
	zd := target.z - e.z
	dist := math.Sqrt(xd*xd + zd*zd)

	// Potion-selection ladder (HARMING default; SLOWNESS if dist≥8 & !slow; POISON if health≥8 & !poison;
	// WEAKNESS if dist≤3 & !weak & rand<0.25). The RNG draw (WEAKNESS gate) is on the witch's own stream.
	effects := witchPotionHarming()
	if dist >= 8.0 && !playerHasEffect(target, effectSlowness) {
		effects = witchPotionSlowness()
	} else if target.health >= 8.0 && !playerHasEffect(target, effectPoison) {
		effects = witchPotionPoison()
	} else if dist <= 3.0 && !playerHasEffect(target, effectWeakness) && float64(r.nextFloat()) < 0.25 {
		effects = witchPotionWeakness()
	}

	// Launch: normalize(xd, yd+dist*0.2, zd) + triangle spread * (dist≤2?0.45:0.75).
	pow := 0.75
	if dist <= 2.0 {
		pow = 0.45
	}
	vx, vy, vz := normalizeVec3(xd, yd+dist*0.2, zd)
	spread := 0.0172275 * witchLaunchUncertainty
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	vx *= pow
	vy *= pow
	vz *= pow

	launchY := e.y + e.height*0.85
	t.spawnSplashPotion(e.id, e.x, launchY, e.z, vx, vy, vz, effects)
}

// witchPotion* build the effect payloads for the witch's potions (the base variants, amp 0, jar durations).
func witchPotionHarming() []splashEffect {
	return []splashEffect{{id: effectInstantDamage, duration: 1, amplifier: 0}} // Potions.HARMING
}
func witchPotionPoison() []splashEffect {
	return []splashEffect{{id: effectPoison, duration: 900, amplifier: 0}} // Potions.POISON
}
func witchPotionSlowness() []splashEffect {
	return []splashEffect{{id: effectSlowness, duration: 1800, amplifier: 0}} // Potions.SLOWNESS
}
func witchPotionWeakness() []splashEffect {
	return []splashEffect{{id: effectWeakness, duration: 1800, amplifier: 0}} // Potions.WEAKNESS
}
