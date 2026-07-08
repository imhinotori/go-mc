// blaze.go -- the hostile Blaze entity (net.minecraft.world.entity.monster.Blaze), a 1:1 port from the
// unobfuscated 26.2 jar. A nether GROUND/HOVER hostile that drops blaze rods (gating brewing). It acquires
// a player, meleees when adjacent, and at range fires a 3-SmallFireball burst on an attack-step cadence
// with a pre-shoot glow + the CHARGED on-fire flag. Code-spawned (spawnBlaze) with a minimal e.ai; its
// behavior is the code-driven blazeAiStep from tickAI (sibling of ghastAiStep / vexAiStep). It is a normal
// GROUND mob for physics (NOT a flyer): its MoveControl pursuit + ground gravity ride the shared paths.
//
// VANILLA (verified javap Blaze + Blaze BlazeAttackGoal this session):
//   Blaze ctor: allowedHeightOffset 0.5; WATER malus -1; LAVA malus 8; FIRE/FIRE_IN_NEIGHBOR malus 0;
//     xpReward 10.
//   createAttributes: Monster.createMonsterAttributes + ATTACK_DAMAGE 6.0 + MOVEMENT_SPEED
//     0.23000000417232513 + FOLLOW_RANGE 48.0 (MAX_HEALTH is the createLivingAttributes default 20.0).
//   registerGoals: @4 BlazeAttackGoal(this); @5 MoveTowardsRestrictionGoal(1.0); @7
//     WaterAvoidingRandomStrollGoal(1.0, 0.0); @8 LookAtPlayerGoal(Player, 8.0); @8 RandomLookAroundGoal.
//     target @1 HurtByTargetGoal + @2 NearestAttackableTargetGoal(Player, true).
//   DATA_FLAGS_ID: BYTE default 0; isCharged() = (flags AND 1) != 0; setCharged sets/clears bit 1.
//   isSensitiveToWater() = true -> LivingEntity.aiStep tail: if (isSensitiveToWater AND isInWaterOrRain)
//     hurtServer(drown(), 1.0F).
//   BlazeAttackGoal.tick (bytecode-verified): decrement attackTime; get target; hasLineOfSight -> flag;
//     d = distanceToSqr. d LT 4.0 -> MELEE (attackTime<=0: attackTime=20, doHurtTarget) + move-to-target.
//     d LT followDist^2 AND flag -> RANGED BURST: dx=tX-x, dy=tY(0.5)-Y(0.5), dz=tZ-z; on attackTime<=0
//     ++attackStep: step1 attackTime=60 setCharged(true); step2..4 attackTime=6; step>4 attackTime=100
//     attackStep=0 setCharged(false); if step>1 d11=sqrt(sqrt(d))*0.5, ONE SmallFireball with aim
//     triangle(dx,2.297*d11)/dy/triangle(dz,2.297*d11), normalized, spawned at Y(0.5)+0.5. setLookAt.
//     else lastSeen LT 5 -> pursue. start: attackStep=0. stop: setCharged(false), lastSeen=0.
//
// v1 STUBS (cited): levelEvent 1018 is the client pre-shoot glow/sound (deferred like the ghast events);
// the observable gameplay (cadence, CHARGED flip, the 3-fireball burst with the triangle x/z spread, the
// SmallFireball spawn+velocity, the melee doHurtTarget) is EXACT. The SmallFireball reuses the hurting-
// projectile infra (hurtSmallFireball: 5.0 fire damage + igniteForSeconds on hit).

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Blaze constants (VERIFIED javap Blaze + Blaze BlazeAttackGoal this session).
const (
	blazeXpReward         = 10    // Blaze ctor: xpReward = 10
	blazeMeleeRangeSqr    = 4.0   // BlazeAttackGoal.tick: d LT 4.0 -> melee (ldc2_w 4.0d)
	blazeMeleeAttackTime  = 20    // attackTime = 20 after a melee doHurtTarget
	blazeChargeAttackTime = 60    // attackStep == 1: attackTime = 60 (the charge-up)
	blazeBurstAttackTime  = 6     // attackStep in 2..4: attackTime = 6 (between the burst shots)
	blazeCooldownTime     = 100   // attackStep GT 4: attackTime = 100, attackStep = 0 (cooldown)
	blazeMaxSeenGap       = 5     // else-if lastSeen LT 5 -> pursue the recently-seen target
	blazeSpreadFactor     = 2.297 // the triangle deviation multiplier on the aim x/z (ldc2_w 2.297d)
	blazeWaterDamage      = 1.0   // isSensitiveToWater aiStep tail: hurtServer(drown(), 1.0F) (fconst_1)
)

// spawnBlaze creates a hostile Blaze at (x,y,z) and adds it to the owner region store (the tracker
// broadcasts AddEntity next tick). Minimal e.ai (per-entity rng + attack-target slot); NO goalSelector
// (the behavior is the code-driven blazeAiStep, like spawnGhast/spawnVex). initSpawnHealth seeds health
// from the folded MAX_HEALTH (20.0). Cite Blaze(EntityType, Level) + registerGoals.
func (t *TickLoop) spawnBlaze(x, y, z float64) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.Blaze, x, y, z)
	b.isBlaze = true
	// Blaze ctor: xpReward = 10 -- the xpReward field is not modeled on *Entity yet (death_mob.go
	// computes 1+nextInt(3); a future per-type xpReward slots in there). Cited constant below.
	_ = blazeXpReward
	initSpawnHealth(b) // setHealth(getMaxHealth()) -> 20.0
	b.ai = &mobAI{}
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// blazeAiStep ports the Blaze tick + goals for ONE blaze, driven per-type from tickAI (gated on typ ==
// entity.Blaze.ID, AFTER serverAiStep -- the empty goalSelector no-op, like ghastAiStep). Order:
// targetSelector, the BlazeAttackGoal (melee-or-burst), then the LivingEntity.aiStep water tail. All RNG
// is on the blaze OWN per-entity rng. Cite Blaze + Blaze BlazeAttackGoal.
func (t *TickLoop) blazeAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	t.blazeAcquireNearestPlayer(e)
	t.blazeAttackGoalTick(e)
	t.blazeWaterSensitivity(e)
}

// blazeTarget reads the blaze current attack-target player (Mob.getTarget() via e.ai.attackTargetID), or
// nil. Tick-owned (mirrors ghastTarget/vexTarget).
func (t *TickLoop) blazeTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// blazeAcquireNearestPlayer ports Blaze @2 NearestAttackableTargetGoal(Player, true) + @1 HurtByTargetGoal:
// the nearest live player within FOLLOW_RANGE (48.0). v1 reduces to nearest live player within range (the
// shared NearestAttackable pattern, like ghastAcquireNearestPlayer minus the ghast y-window). NO RNG.
// Cite Blaze.registerGoals targetSelector.
func (t *TickLoop) blazeAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 48.0
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
			e.ai.attackTargetID = 0 // setTarget(null)
			blazeAttackGoalStop(e)  // BlazeAttackGoal.stop(): setCharged(false); lastSeen = 0
		} else {
			return
		}
	}
	var best *tickPlayer
	bestSq := rangeSqr
	for _, p := range t.players {
		if p == nil || p.dead {
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
		e.blazeAttackStep = 0               // BlazeAttackGoal.start(): attackStep = 0
	}
}

// blazeAttackGoalStop ports Blaze BlazeAttackGoal.stop: setCharged(false); lastSeen = 0. Called when the
// target is lost. Cite Blaze BlazeAttackGoal.stop.
func blazeAttackGoalStop(e *Entity) {
	e.blazeCharged = false // setCharged(false)
	e.blazeLastSeen = 0
}

// blazeAttackGoalTick ports Blaze BlazeAttackGoal.tick (canUse/start/stop folded, like ghastShootFireball).
// Decrement attackTime, check line-of-sight, then branch: melee (d LT 4.0), the ranged 3-fireball burst
// (d LT followDist^2 with LoS, driven by the attackStep cadence), or the recently-seen pursue (lastSeen LT
// 5). RNG (the triangle x/z spread) is on the blaze OWN stream. Cite Blaze BlazeAttackGoal.tick.
func (t *TickLoop) blazeAttackGoalTick(e *Entity) {
	e.blazeAttackTime-- // --attackTime
	target := t.blazeTarget(e)
	if target == nil {
		return
	}
	flag := t.sensingHasLineOfSight(e, target) // getSensing().hasLineOfSight(target)
	if flag {
		e.blazeLastSeen = 0
	} else {
		e.blazeLastSeen++
	}
	d := distanceToSqrPlayer(target, e) // distanceToSqr(target)
	followDist := e.getAttributeValue(attribute.FollowRange)

	switch {
	case d < blazeMeleeRangeSqr:
		// MELEE: if(!flag) return; if(attackTime LE 0){attackTime=20; doHurtTarget;} setWantedPosition.
		if !flag {
			return
		}
		if e.blazeAttackTime <= 0 {
			e.blazeAttackTime = blazeMeleeAttackTime
			t.blazeDoHurtTarget(e, target)
		}
		e.ai.setWantTargetMod(target.x, target.y, target.z, 1.0) // navigation.moveTo(target, 1.0) (seam x MOVEMENT_SPEED)
	case d < followDist*followDist && flag:
		// RANGED BURST: aim components then the attack-step cadence + the per-shooting-step SmallFireball.
		dx := target.x - e.x                                                         // target.getX() - getX()
		dy := blazeGetY(target.y, playerHeight, 0.5) - blazeGetY(e.y, e.height, 0.5) // target.getY(0.5) - getY(0.5)
		dz := target.z - e.z                                                         // target.getZ() - getZ()
		if e.blazeAttackTime <= 0 {
			e.blazeAttackStep++ // ++attackStep
			switch {
			case e.blazeAttackStep == 1:
				e.blazeAttackTime = blazeChargeAttackTime // attackTime = 60
				e.blazeCharged = true                     // setCharged(true)
			case e.blazeAttackStep <= 4:
				e.blazeAttackTime = blazeBurstAttackTime // attackTime = 6
			default:
				e.blazeAttackTime = blazeCooldownTime // attackTime = 100
				e.blazeAttackStep = 0                 // attackStep = 0
				e.blazeCharged = false                // setCharged(false)
			}
			if e.blazeAttackStep > 1 {
				d11 := math.Sqrt(math.Sqrt(d)) * 0.5 // d11 = sqrt(sqrt(d)) * 0.5
				// levelEvent(null, 1018, ...): the pre-shoot glow/sound (client-visual, deferred). The loop runs
				// exactly once (i LT 1) -> ONE SmallFireball per shooting step (steps 2,3,4 = the 3-fireball burst).
				spread := blazeSpreadFactor * d11
				aimX := arrowTriangle(e.ai.rng, dx, spread) // random.triangle(dx, 2.297*d11)
				aimY := dy                                  // dy (NO spread on the vertical)
				aimZ := arrowTriangle(e.ai.rng, dz, spread) // random.triangle(dz, 2.297*d11)
				t.blazeShootSmallFireball(e, aimX, aimY, aimZ)
			}
		}
		t.blazeFaceTarget(e, target) // setLookAt(target, 10, 10)
	case e.blazeLastSeen < blazeMaxSeenGap:
		// PURSUE: recently seen (lastSeen LT 5) -> move toward the last-known target position.
		e.ai.setWantTargetMod(target.x, target.y, target.z, 1.0) // navigation.moveTo(target, 1.0) (seam x MOVEMENT_SPEED)
	}
}

// blazeShootSmallFireball ports the burst per-step fireball spawn: new SmallFireball(level, this,
// aim.normalize()) then setPos(sf.getX(), getY(0.5)+0.5, sf.getZ()). The SmallFireball ctor assigns the
// directional movement from the normalized aim scaled by accelerationPower -- the shared hurting-projectile
// spawner does exactly that. setPos overrides ONLY Y (keeps the ctor X/Z == the muzzle at the blaze origin)
// to getY(0.5)+0.5. Cite Blaze BlazeAttackGoal.tick + SmallFireball(Level, LivingEntity, Vec3).
func (t *TickLoop) blazeShootSmallFireball(e *Entity, aimX, aimY, aimZ float64) {
	muzzleY := blazeGetY(e.y, e.height, 0.5) + 0.5 // getY(0.5) + 0.5
	t.spawnHurtingProjectile(e.id, hurtSmallFireball, e.x, muzzleY, e.z, aimX, aimY, aimZ)
}

// blazeDoHurtTarget ports the melee branch doHurtTarget(getServerLevel(this), target): deal ATTACK_DAMAGE
// (6.0) to the player through the shared player hurt path (the same shape as meleeAttackGoal.doHurtTarget).
// The damageSource carries attacker = e.id (host-set). NO RNG. Cite Blaze.doHurtTarget (== Mob.doHurtTarget).
func (t *TickLoop) blazeDoHurtTarget(e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // (float) getAttributeValue(ATTACK_DAMAGE) == 6.0
	src := damageSourceMobAttack(e.id)                          // getWeaponItem().getDamageSource(this) -> mob_attack(this)
	t.applyDamage(target, src, dmg)                             // target.hurtServer(level, src, f) -- the PLAYER path
}

// blazeFaceTarget ports getLookControl().setLookAt(target, 10.0f, 10.0f): face the target. v1 sets yaw
// (and headYaw) toward the target; pitch is left as-is (the 10-deg/tick cap is a smoothing detail with no
// observable-gameplay effect on the fireball, which aims off the raw dx/dy/dz above). Cite setLookAt.
func (t *TickLoop) blazeFaceTarget(e *Entity, target *tickPlayer) {
	dx := target.x - e.x
	dz := target.z - e.z
	e.yaw = float32(-math.Atan2(dx, dz) * vexDegPerRad)
	e.headYaw = e.yaw
}

// blazeWaterSensitivity ports the LivingEntity.aiStep tail for a sensitive-to-water mob: if
// (isSensitiveToWater() AND isInWaterOrRain()) hurtServer(drown(), 1.0F). Blaze overrides
// isSensitiveToWater() to true, so water OR rain deals 1.0 drown damage every tick. Reuses the shared
// entityIsInWaterOrRain (conduit_be.go) + applyDamageEntity. Cite LivingEntity.aiStep tail +
// Blaze.isSensitiveToWater.
func (t *TickLoop) blazeWaterSensitivity(e *Entity) {
	if !t.entityIsInWaterOrRain(e) { // isInWaterOrRain()
		return
	}
	t.applyDamageEntity(e, damageSourceOf(damageTypeDrown), blazeWaterDamage) // hurtServer(drown(), 1.0F)
}

// blazeGetY ports Entity.getY(double d) = getY() + getBbHeight() * d -- the fractional-height point on the
// entity bounding box (getY(0.5) == the vertical midpoint). Cite Entity.getY(double).
func blazeGetY(y, height, frac float64) float64 {
	return y + height*frac
}
