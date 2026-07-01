package server

// ai_goals_crossbow.go — RAIDER (Task): the Pillager's RangedCrossbowAttackGoal + performCrossbowAttack,
// PORTED 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.entity.monster.illager.Pillager +
// RangedCrossbowAttackGoal, CFR/javap this session):
//
//   - RangedCrossbowAttackGoal(this, 1.0, 8.0f): flags {MOVE, LOOK}, requiresUpdateEveryTick.
//     speedModifier 1.0, attackRadius 8.0 -> attackRadiusSqr 64.0. CrossbowState UNCHARGED/CHARGING/
//     CHARGED/READY_TO_ATTACK (init UNCHARGED). NO strafing in 26.2 (verified: zero nextFloat draws).
//   - tick: seeTime run-length; needsToMove = (distSqr>64 || seeTime<5) && attackDelay==0; if needsToMove
//     re-path on updatePathDelay<=0 (speed = canRun()? 1.0 : 0.5, canRun == UNCHARGED) then
//     updatePathDelay = sample(20..40); else stop. lookAt(30,30). State machine:
//       UNCHARGED: !needsToMove -> startUsingItem; CHARGING.
//       CHARGING: getTicksUsingItem>=chargeDuration(25) -> release; CHARGED; attackDelay = 20+nextInt(20).
//       CHARGED: --attackDelay; ==0 -> READY_TO_ATTACK.
//       READY_TO_ATTACK && LoS -> performRangedAttack(target, 1.0); UNCHARGED.
//   - Pillager.performRangedAttack(target, power) IGNORES power -> performCrossbowAttack(this, 1.6).
//     Fires an Arrow at velocity 1.6, inaccuracy 14 - difficulty*4 (NORMAL 6), aim getY(1/3) + dist*0.2 lob,
//     3 triangle spread draws x,y,z (0.0172275*inaccuracy). The crossbow arrow baseDamage is the Arrow
//     default (2.0) — Pillager does NOT setBaseDamageFromMob (that is skeleton-bow only).
//
// RNG draws (in order, VERIFIED CFR): (1) sample(20..40) = nextInt(21)+20 on repath; (2) nextInt(20) on
// CHARGING->CHARGED; (3) the 3 arrow-spread triangles (x,y,z) in performRangedAttack. NO strafe draws.
//
// v1 STUBS (cited): LoS always-true (no sensing) -> seeTime only counts up; the crossbow ITEM + isUsingItem
// /getTicksUsingItem is modeled by a per-goal chargeTicks counter (v1 has no mob item-use subsystem) — the
// pillager always holds a crossbow (isHolding stub true), releasing at chargeDuration 25 (getChargeDuration
// default, no Quick Charge). The CHARGED_PROJECTILES component + the WITHDRAW/LOAD sounds are cite-deferred.

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
)

// crossbowState is RangedCrossbowAttackGoal.CrossbowState.
type crossbowState int

const (
	crossbowUncharged crossbowState = iota
	crossbowCharging
	crossbowCharged
	crossbowReadyToAttack
)

// RangedCrossbowAttackGoal constants (VERIFIED CFR — the pillager's ctor args + jar literals).
const (
	crossbowSpeedModifier = 1.0
	crossbowAttackRadius  = 8.0
	crossbowRadiusSqr     = crossbowAttackRadius * crossbowAttackRadius // 64.0
	crossbowChargeDur     = 25                                          // CrossbowItem.getChargeDuration default (1.25f*20; no Quick Charge)
	crossbowSeeTimeReady  = 5                                           // seeTime < 5 -> keep closing
	crossbowMobArrowPower = 1.6                                         // MOB_ARROW_POWER (performCrossbowAttack power)
	crossbowArrowBaseDmg  = 2.0                                         // Arrow default baseDamage (Pillager does NOT setBaseDamageFromMob)
	crossbowLookMaxStep   = 30.0                                        // lookAt(target, 30, 30)
)

// rangedCrossbowAttackGoal ports RangedCrossbowAttackGoal state. chargeTicks is the v1 stand-in for
// getTicksUsingItem() (-1 == not using; else 0..chargeDuration). updatePathDelay/attackDelay/seeTime mirror
// the jar fields.
type rangedCrossbowAttackGoal struct {
	baseGoal
	state           crossbowState
	seeTime         int
	attackDelay     int
	updatePathDelay int
	chargeTicks     int // -1 == not using item; else the draw counter
}

func newRangedCrossbowAttackGoal() *rangedCrossbowAttackGoal {
	return &rangedCrossbowAttackGoal{baseGoal: newBaseGoal(flagMove | flagLook), state: crossbowUncharged, chargeTicks: -1}
}

func (g *rangedCrossbowAttackGoal) requiresUpdateEveryTick() bool { return true }

// canUse: isValidTarget() && isHolding(CROSSBOW). isValidTarget == target!=null && alive. The pillager
// always holds a crossbow (v1 stub true).
func (g *rangedCrossbowAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	p := t.playerByEntityID(id)
	return p != nil && !p.dead
}

// canContinueToUse: isValidTarget() && (canUse() || !navigation.isDone()) && isHolding(CROSSBOW).
func (g *rangedCrossbowAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	return g.canUse(t, e) || e.ai.navigation.active()
}

func (g *rangedCrossbowAttackGoal) start(t *TickLoop, e *Entity) {}

// stop: setAggressive(false); setTarget(null) via the goal (nav clear); seeTime=0; state->UNCHARGED.
func (g *rangedCrossbowAttackGoal) stop(t *TickLoop, e *Entity) {
	g.seeTime = 0
	g.state = crossbowUncharged
	g.chargeTicks = -1
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// canRun == state == UNCHARGED (the full-speed move; charging/charged move at half speed).
func (g *rangedCrossbowAttackGoal) canRun() bool { return g.state == crossbowUncharged }

// tick ports RangedCrossbowAttackGoal.tick.
func (g *rangedCrossbowAttackGoal) tick(t *TickLoop, e *Entity) {
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
	hasLineOfSight := true // v1 stub (no sensing)
	hadLineOfSight := g.seeTime > 0
	if hasLineOfSight != hadLineOfSight {
		g.seeTime = 0
	}
	if hasLineOfSight {
		g.seeTime++
	} else {
		g.seeTime--
	}
	distSqr := distanceToSqrPlayer(target, e)
	needsToMove := (distSqr > crossbowRadiusSqr || g.seeTime < crossbowSeeTimeReady) && g.attackDelay == 0
	if needsToMove {
		g.updatePathDelay--
		if g.updatePathDelay <= 0 {
			speedMod := crossbowSpeedModifier
			if !g.canRun() {
				speedMod = crossbowSpeedModifier * 0.5
			}
			getSpeed := e.getAttributeValue(attribute.MovementSpeed) * speedMod
			e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed) // navigation.moveTo(target, speed)
			// updatePathDelay = PATHFINDING_DELAY_RANGE.sample(random) = nextInt(21)+20. DRAW 1.
			g.updatePathDelay = 20 + int(mobRandom(e).nextInt(21))
		}
	} else {
		g.updatePathDelay = 0
		e.ai.clearWantTarget() // navigation.stop()
	}
	// lookAt(target, 30, 30): head-only turn.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, crossbowLookMaxStep)

	switch g.state {
	case crossbowUncharged:
		if !needsToMove {
			g.chargeTicks = 0 // startUsingItem(CROSSBOW)
			g.state = crossbowCharging
		}
	case crossbowCharging:
		if g.chargeTicks < 0 {
			g.state = crossbowUncharged // !isUsingItem
			break
		}
		g.chargeTicks++
		if g.chargeTicks >= crossbowChargeDur { // getTicksUsingItem() >= getChargeDuration()
			g.chargeTicks = -1 // releaseUsingItem()
			g.state = crossbowCharged
			// attackDelay = 20 + nextInt(20). DRAW 2.
			g.attackDelay = 20 + int(mobRandom(e).nextInt(20))
		}
	case crossbowCharged:
		g.attackDelay--
		if g.attackDelay == 0 {
			g.state = crossbowReadyToAttack
		}
	case crossbowReadyToAttack:
		if hasLineOfSight {
			t.performCrossbowAttack(e, target)
			g.state = crossbowUncharged
		}
	}
}

// performCrossbowAttack ports Pillager.performRangedAttack -> performCrossbowAttack(this, 1.6): fire one
// Arrow at the target with velocity 1.6, inaccuracy 14 - difficulty*4, the mob-aim ballistic vector
// (getY(1/3) + dist*0.2 lob) and the 3 triangle spread draws (x,y,z). The arrow baseDamage is the Arrow
// default 2.0 (Pillager does NOT setBaseDamageFromMob). onCrossbowAttackPerformed -> noActionTime=0 (a
// cited no-op; no despawn-timer read). Cite CrossbowAttackMob.performCrossbowAttack + CrossbowItem.
// shootProjectile + Projectile.getMovementToShoot.
func (t *TickLoop) performCrossbowAttack(e *Entity, target *tickPlayer) {
	r := mobRandom(e)
	launchY := e.y + e.eyeHeightForArrow()
	xd := target.x - e.x
	yd := (target.y + float64(playerHeight)*0.3333333333333333) - launchY // target.getY(1/3)
	zd := target.z - e.z
	dist := math.Sqrt(xd*xd + zd*zd)
	ydLob := yd + dist*0.2 // dist * 0.2f lob (float-widened)

	// inaccuracy = 14 - difficulty.getId()*4 (NORMAL == 2 -> 6).
	diff := float64(serverDifficulty)
	inaccuracy := 14.0 - diff*4.0

	// getMovementToShoot: normalize(dir) + triangle(0, 0.0172275*inaccuracy) per axis (x,y,z), scaled 1.6.
	vx, vy, vz := normalizeVec3(xd, ydLob, zd)
	spread := 0.0172275 * inaccuracy
	vx += arrowTriangle(r, 0, spread) // DRAW 3a
	vy += arrowTriangle(r, 0, spread) // DRAW 3b
	vz += arrowTriangle(r, 0, spread) // DRAW 3c
	vx *= crossbowMobArrowPower
	vy *= crossbowMobArrowPower
	vz *= crossbowMobArrowPower

	t.spawnArrow(e.id, e.x, launchY, e.z, vx, vy, vz, crossbowArrowBaseDmg)
}
