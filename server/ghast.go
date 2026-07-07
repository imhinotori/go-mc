package server

// ghast.go -- the hostile Ghast entity (net.minecraft.world.entity.monster.Ghast), a 1:1 port from the
// unobfuscated 26.2 jar. The Ghast is a FLOATING hostile Enemy that drifts on a random-float goal and,
// when it acquires a player, charges up and shoots a LargeFireball. Code-spawned (spawnGhast) with a
// minimal e.ai; its behavior is the per-type ghastAiStep driven from tickAI (sibling of vexAiStep /
// happyGhastAiStep). Its FLIGHT reuses the happy-ghast GhastMoveControl + RandomFloatAroundGoal helpers
// (ai_goals_happy_ghast.go) -- the same jar inner classes -- with FLYING_SPEED 0.06 + distanceToBlocks 0.
//
// VANILLA (verified javap Ghast + inner classes this session):
//   Ghast ctor: explosionPower = 1; xpReward = 5; moveControl = GhastMoveControl(this, false, ()->false).
//   createAttributes: Mob.createMobAttributes + MAX_HEALTH 10 + FOLLOW_RANGE 100 + CAMERA_DISTANCE 8 +
//     FLYING_SPEED 0.06 (supplier in level/attribute/defaults.go).
//   travel: travelFlying(input, 0.02f) -- NO gravity, deltaMovement *= 0.91 (the shared flyer branch).
//   registerGoals: @5 RandomFloatAroundGoal(this) [distanceToBlocks 0]; @7 GhastLookGoal; @7
//     GhastShootFireballGoal. target @1 NearestAttackableTargetGoal(Player, 10, true, false, selector),
//     selector = abs(target.getY() - getY()) <= 4.0.
//   DATA_IS_CHARGING: SynchedEntityData BOOLEAN default false; setCharging(chargeTime > 10).
//   GhastShootFireballGoal.tick: if (distanceToSqr(target) < 4096 && hasLineOfSight) { ++chargeTime;
//     at 10 levelEvent 1015; at 20 spawn LargeFireball(level, ghast, aim.normalize(), explosionPower),
//     setPos muzzle, addFreshEntity, chargeTime = -40 } else if (chargeTime > 0) --chargeTime;
//     setCharging(chargeTime > 10).
//   aim: view = getViewVector(1); d=4; dx = target.getX() - (ghast.getX()+view.x*4); dy = target.getY(0.5)
//     - (0.5 + ghast.getY(0.5)); dz = target.getZ() - (ghast.getZ()+view.z*4). muzzle = (ghast.getX()+
//     view.x*4, ghast.getY(0.5)+0.5, ghast.getZ()+view.z*4).
//
// v1 STUBS (cited): levelEvent 1015/1016 SOUNDS are client-visual (deferred like the happy-ghast port);
// the observable gameplay (charge timing, DATA_IS_CHARGING flip, fireball spawn+velocity, -40 cooldown)
// is EXACT. hasLineOfSight = the shared sensing raycast. The fireball reuses the hurting-projectile infra
// (hurtLargeFireball) whose LargeFireball.onHit explodes at explosionPower and discards.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
)

// Ghast constants (VERIFIED javap Ghast + Ghast$GhastShootFireballGoal this session).
const (
	ghastDefaultExplosionPower = 1 // Ghast ctor: explosionPower = 1 (DEFAULT_EXPLOSION_POWER byte 1)
	ghastXpReward              = 5 // Ghast ctor: xpReward = 5
	ghastFloatDistanceToBlocks = 0 // registerGoals @5 RandomFloatAroundGoal(this) -> this(mob, 0)
	ghastShootRangeSqr         = 4096.0
	ghastChargeShootTime       = 20
	ghastChargeSoundTime       = 10
	ghastCooldownTime          = -40
	ghastChargingFlagTime      = 10
	ghastFireballAimDist       = 4.0
	ghastTargetRange           = 100.0
	ghastTargetYWindow         = 4.0
)

// spawnGhast creates a hostile Ghast at (x,y,z) and adds it to the owner region's store (the tracker
// broadcasts AddEntity next tick). Minimal e.ai (per-entity rng + attack-target slot); NO goalSelector
// (the behavior is the code-driven ghastAiStep, exactly like spawnVex). initSpawnHealth seeds health from
// the folded MAX_HEALTH (10.0). Cite Ghast(EntityType, Level) + registerGoals.
func (t *TickLoop) spawnGhast(x, y, z float64) *Entity {
	g := NewEntity(t.idAlloc.AllocID(), entity.Ghast, x, y, z)
	g.isGhast = true
	g.ghastExplosionPower = ghastDefaultExplosionPower // Ghast ctor: explosionPower = 1
	// Ghast ctor: xpReward = 5 -- the xpReward field is not modeled on *Entity yet (death_mob.go
	// computes 1+nextInt(3); a future per-type xpReward slots in there). Cited constant below.
	_ = ghastXpReward
	initSpawnHealth(g) // setHealth(getMaxHealth()) -> 10.0
	g.ai = &mobAI{}
	reseedMobAI(g.ai, g.id)
	owner := t.regionForEntity(g)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(g)
	return g
}

// ghastAiStep ports the Ghast tick + its goals for ONE ghast, driven per-type from tickAI (gated on typ
// == entity.Ghast.ID, AFTER serverAiStep -- the empty goalSelector no-op, like vexAiStep). Vanilla order:
// targetSelector (NearestAttackableTargetGoal Player), then goals (RandomFloatAroundGoal fly-to pick ->
// GhastMoveControl kick -> GhastLookGoal face -> GhastShootFireballGoal charge+shoot). All RNG draws are
// on the ghast's OWN per-entity rng. Cite Ghast + inner goals.
func (t *TickLoop) ghastAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	t.ghastAcquireNearestPlayer(e)
	if t.ghastRandomFloatAroundCanUse(e) {
		t.ghastRandomFloatAroundStartHostile(e)
	}
	t.ghastMoveControlTick(e)
	t.ghastFaceMovementDirection(e)
	t.ghastShootFireball(e)
}

// ghastRandomFloatAroundStartHostile ports Ghast.RandomFloatAroundGoal.start for the HOSTILE ghast: the
// 1-arg ctor passes distanceToBlocks 0 (vs the happy ghast's 16), so getSuitableFlyToPosition's isGood
// Target short-circuits to true. Reuses the shared getSuitableFlyToPosition helper with distanceToBlocks
// 0. Cite Ghast$RandomFloatAroundGoal(Mob) -> this(mob, 0).
func (t *TickLoop) ghastRandomFloatAroundStartHostile(e *Entity) {
	x, y, z := t.ghastGetSuitableFlyToPosition(e, ghastFloatDistanceToBlocks)
	e.ghastWantedX, e.ghastWantedY, e.ghastWantedZ = x, y, z
	e.ghastHasWanted = true
}

// ghastTarget reads the ghast's current attack-target player (Mob.getTarget() via e.ai.attackTargetID),
// or nil. Tick-owned (mirrors vexTarget).
func (t *TickLoop) ghastTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// ghastAcquireNearestPlayer ports Ghast's @1 NearestAttackableTargetGoal(Player, ..., selector): acquire
// the nearest live player within FOLLOW_RANGE (100.0) whose Y is within +/-4.0 of the ghast (selector =
// abs(target.getY() - getY()) <= 4.0). v1 reduces to "nearest live player within range + y-window" (the
// shared NearestAttackable pattern; mustSee/reciprocalChance is the visible-target simplification). NO
// RNG. Cite Ghast.registerGoals targetSelector + lambda$registerGoals$0.
func (t *TickLoop) ghastAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead ||
			distanceToSqrPlayer(p, e) > ghastTargetRange*ghastTargetRange ||
			math.Abs(p.y-e.y) > ghastTargetYWindow {
			e.ai.attackTargetID = 0 // setTarget(null)
		} else {
			return
		}
	}
	var best *tickPlayer
	bestSq := ghastTargetRange * ghastTargetRange
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if math.Abs(p.y-e.y) > ghastTargetYWindow {
			continue
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq <= bestSq {
			bestSq = dsq
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID // Mob.setTarget(nearest)
	}
}

// ghastFaceMovementDirection ports Ghast.faceMovementDirection (GhastLookGoal.tick): no target -> face
// the delta-movement heading; else (target within 64) face the target. yaw = -atan2(dx, dz)*57.295776;
// yBodyRot = yaw. Only yaw is set (pitch 0). Cite Ghast.faceMovementDirection + Ghast$GhastLookGoal.tick.
func (t *TickLoop) ghastFaceMovementDirection(e *Entity) {
	target := t.ghastTarget(e)
	if target == nil {
		e.yaw = float32(-math.Atan2(e.vx, e.vz) * vexDegPerRad)
		e.headYaw = e.yaw
		return
	}
	if distanceToSqrPlayer(target, e) < ghastShootRangeSqr {
		dx := target.x - e.x
		dz := target.z - e.z
		e.yaw = float32(-math.Atan2(dx, dz) * vexDegPerRad)
		e.headYaw = e.yaw
	}
}

// ghastViewVector ports Ghast.getViewVector(1.0f): the unit LOOK direction from yaw+pitch. Reuses
// playerViewVector (Entity.calculateViewVector -- generic). Cite Entity.getViewVector.
func (t *TickLoop) ghastViewVector(e *Entity) (float64, float64, float64) {
	return playerViewVector(e.yaw, e.pitch)
}

// ghastShootFireball ports Ghast$GhastShootFireballGoal (canUse/start/tick folded, like vexChargeAttack).
// Target in range (<64) with LoS -> advance chargeTime; at 20 spawn a LargeFireball + reset to -40; out
// of range decay toward 0. setCharging(chargeTime > 10) each tick drives DATA_IS_CHARGING. RNG-free. Cite
// Ghast$GhastShootFireballGoal.tick.
func (t *TickLoop) ghastShootFireball(e *Entity) {
	target := t.ghastTarget(e)
	if target == nil {
		e.ghastChargeTime = 0 // start() resets chargeTime on the next acquisition
		if e.ghastCharging {
			e.ghastCharging = false // stop(): setCharging(false)
		}
		return
	}
	if distanceToSqrPlayer(target, e) < ghastShootRangeSqr && t.sensingHasLineOfSight(e, target) {
		e.ghastChargeTime++
		if e.ghastChargeTime == ghastChargeSoundTime {
			_ = ghastChargeSoundTime // levelEvent 1015 charge sound: client-visual, cite-deferred
		}
		if e.ghastChargeTime == ghastChargeShootTime {
			t.ghastFireLargeFireball(e, target)
			e.ghastChargeTime = ghastCooldownTime // chargeTime = -40 (fire cooldown)
		}
	} else if e.ghastChargeTime > 0 {
		e.ghastChargeTime--
	}
	e.ghastCharging = e.ghastChargeTime > ghastChargingFlagTime // setCharging(chargeTime > 10)
}

// ghastFireLargeFireball ports the chargeTime==20 branch: aim vector from the target relative to the
// view-offset muzzle, spawn a LargeFireball there heading along it (the hurting-projectile spawner
// normalizes the aim + scales by accelerationPower 0.1, == the LargeFireball ctor's assignDirectional
// Movement), explosion power = getExplosionPower() (1). Cite Ghast$GhastShootFireballGoal.tick + ctor.
func (t *TickLoop) ghastFireLargeFireball(e *Entity, target *tickPlayer) {
	vx, _, vz := t.ghastViewVector(e) // view = getViewVector(1.0f); d = 4.0
	ghastMidY := e.y + 0.5*e.height   // ghast.getY(0.5)
	spawnX := e.x + vx*ghastFireballAimDist
	spawnY := ghastMidY + 0.5
	spawnZ := e.z + vz*ghastFireballAimDist
	targetMidY := target.y + 0.5*playerHeight // target.getY(0.5)
	dx := target.x - (e.x + vx*ghastFireballAimDist)
	dy := targetMidY - (0.5 + ghastMidY)
	dz := target.z - (e.z + vz*ghastFireballAimDist)
	// levelEvent 1016 shoot sound: client-visual, cite-deferred.
	fb := t.spawnHurtingProjectile(e.id, hurtLargeFireball, spawnX, spawnY, spawnZ, dx, dy, dz)
	fb.hurtExplosion = e.ghastExplosionPower // getExplosionPower() (1)
}

// ghastIsFlyer reports whether an entity is a hostile Ghast (the tickPhysics flyer gate reads it to take
// the NO-gravity + 0.91-drift branch). Ghast.travel calls travelFlying, the same flyer physics the happy
// ghast uses. Cite Ghast.travel -> travelFlying.
func ghastIsFlyer(e *Entity) bool {
	return e.typ == entity.Ghast.ID
}
