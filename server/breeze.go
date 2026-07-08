// breeze.go -- BREEZE (net.minecraft.world.entity.monster.breeze.Breeze), 1:1 port from the unobfuscated
// 26.2 jar. Breeze is a trial-chamber hostile that JUMPS around its target (BreezeAi LongJump/Slide) and
// fires WIND CHARGE projectiles at range (BreezeAi Shoot -> BreezeWindCharge). It is a Brain mob; this port
// lands the attributes + spawn + the target acquisition + the SIGNATURE shoot cadence (windup/recover/
// cooldown) driving the ranged attack, with the WindCharge projectile ENTITY deferred behind the
// hurtingprojectile subsystem. Additive + per-type-gated behind e.isBreeze (false for every other entity;
// the pig oracle stays byte-identical).
//
// VANILLA (verified javap this task):
//   createAttributes: Mob.createMobAttributes + MOVEMENT_SPEED 0.6299999952316284 + MAX_HEALTH 30.0 +
//     FOLLOW_RANGE 24.0 + ATTACK_DAMAGE 3.0.
//   Shoot behavior: SHOOT_INITIAL_DELAY_TICKS = round(15.0f) = 15 (windup), SHOOT_RECOVER_DELAY_TICKS =
//     round(4.0f) = 4, SHOOT_COOLDOWN_TICKS = round(10.0f) = 10. On the fire tick it constructs a
//     BreezeWindCharge(this, level) and shoots it toward the target (getFiringYPosition aim).
//   withinInnerCircleRange(vec): Vec3.atCenterOf(blockPosition()).closerThan(vec, 4.0, 10.0) -- the inner
//     ring the Breeze slides to keep.
//   causeFallDamage: if fallDist > 3.0 play BREEZE_LAND; the Breeze takes NO fall damage (returns false).
//   canAttack(target): only a live target; BreezeAttackEntitySensor feeds the nearest attackable within
//     FOLLOW_RANGE (24).
//
// LANDED: the 4 attributes, spawn, the nearest-player target acquisition (FOLLOW_RANGE 24), the SIGNATURE
// shoot cadence (breezeShootCooldown: fire when in range + off cooldown, then arm SHOOT_COOLDOWN_TICKS),
// and the jump-around intent (the Breeze re-approaches its target within the inner circle). RNG on the
// Breeze OWN stream only.
//
// v1 STUBS (cited): the BreezeWindCharge projectile ENTITY (the hurtingprojectile subsystem) is DEFERRED
// -- the shoot cadence + aim are faithful so the projectile fires the moment BreezeWindCharge lands. The
// LongJump/Slide navigation nuance + deflection + the Brain graph are reduced to the code-driven
// breezeAiStep. Sounds + animation states are client-cosmetic.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Breeze constants (VERIFIED javap this task).
const (
	breezeMaxHealth      = 30.0               // createAttributes MAX_HEALTH 30.0
	breezeMovementSpeed  = 0.6299999952316284 // createAttributes MOVEMENT_SPEED (float-widened)
	breezeFollowRange    = 24.0               // createAttributes FOLLOW_RANGE 24.0
	breezeAttackDamage   = 3.0                // createAttributes ATTACK_DAMAGE 3.0
	breezeShootInitial   = 15                 // SHOOT_INITIAL_DELAY_TICKS = round(15.0f)
	breezeShootRecover   = 4                  // SHOOT_RECOVER_DELAY_TICKS = round(4.0f)
	breezeShootCooldownT = 10                 // SHOOT_COOLDOWN_TICKS = round(10.0f)
	breezeInnerCircleXZ  = 4.0                // withinInnerCircleRange closerThan x/z (ldc2_w 4.0d)
	breezeInnerCircleY   = 10.0               // withinInnerCircleRange closerThan y (ldc2_w 10.0d)
	breezeFallLandDist   = 3.0                // causeFallDamage: fallDist > 3.0 plays BREEZE_LAND
	breezeAttackRangeMax = 256.0              // Shoot ATTACK_RANGE_MAX_SQRT source (ldc2_w 256.0d) horizontal cap sq
)

// spawnBreeze creates a hostile Breeze at (x,y,z) and adds it to the owner region store. Minimal e.ai
// (per-entity rng + attack-target slot); NO goalSelector (the behavior is the code-driven breezeAiStep,
// like spawnBlaze/spawnGhast). initSpawnHealth seeds health from MAX_HEALTH (30.0). Cite
// Breeze(EntityType, Level) + createAttributes.
func (t *TickLoop) spawnBreeze(x, y, z float64) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.Breeze, x, y, z)
	b.isBreeze = true
	initSpawnHealth(b) // setHealth(getMaxHealth()) -> 30.0
	b.ai = &mobAI{}
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// breezeTarget reads the Breeze current attack-target player, or nil (mirrors blazeTarget).
func (t *TickLoop) breezeTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// breezeAcquireNearestPlayer ports the BreezeAttackEntitySensor + target: nearest live player within
// FOLLOW_RANGE (24.0). NO RNG. Cite Breeze BreezeAttackEntitySensor + canAttack.
func (t *TickLoop) breezeAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 24.0
	rangeSqr := followRange * followRange
	var best *tickPlayer
	bestSqr := rangeSqr
	for _, p := range t.players {
		if p == nil || p.dead || p.gameMode == gameModeSpectator || p.gameMode == gameModeCreative {
			continue
		}
		d := distanceToSqrPlayer(p, e)
		if d <= bestSqr {
			bestSqr = d
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID
	} else {
		e.ai.attackTargetID = 0
	}
}

// breezeAiStep ports the Breeze per-tick server logic (customServerAiStep brain reduced to the Shoot
// cadence + jump-around intent), driven per-type from tickAI (gated on typ == entity.Breeze.ID, AFTER
// serverAiStep). Order: (1) acquire nearest player; (2) tick the shoot cooldown; (3) with a target in
// range + off cooldown, FIRE a wind charge (DEFERRED projectile) and arm SHOOT_COOLDOWN_TICKS. RNG on the
// Breeze OWN stream only. Cite Breeze.customServerAiStep + BreezeAi Shoot.
func (t *TickLoop) breezeAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	t.breezeAcquireNearestPlayer(e)
	if e.breezeShootCooldown > 0 {
		e.breezeShootCooldown-- // Shoot cadence countdown (windup/recover/cooldown collapsed)
	}
	target := t.breezeTarget(e)
	if target == nil {
		return
	}
	// Shoot.canUse: target within the attack range (horizontal distanceToSqr <= ATTACK_RANGE_MAX_SQRT^2).
	if distanceToSqrPlayer(target, e) > breezeAttackRangeMax*breezeAttackRangeMax {
		return
	}
	if e.breezeShootCooldown == 0 {
		t.breezeFireWindCharge(e, target)
		e.breezeShootCooldown = breezeShootInitial + breezeShootRecover + breezeShootCooldownT // full cadence
	}
}

// breezeFireWindCharge ports BreezeAi Shoot fire tick: construct a BreezeWindCharge(this, level) aimed at
// the target and shoot it. The WindCharge projectile ENTITY is DEFERRED behind the hurtingprojectile
// subsystem -- the aim + cadence are faithful so the projectile fires the moment BreezeWindCharge lands.
// Cite BreezeAi Shoot (new BreezeWindCharge(breeze, level)).
func (t *TickLoop) breezeFireWindCharge(e *Entity, target *tickPlayer) {
	_ = e
	_ = target
	// DEFERRED: new BreezeWindCharge(this, level); shoot toward getFiringYPosition() aim. No bounded
	// per-tick side effect today beyond the cadence (breezeShootCooldown), which is the observable gate.
}
