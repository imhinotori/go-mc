// creaking.go -- CREAKING (net.minecraft.world.entity.monster.creaking.Creaking), 1:1 port from the
// unobfuscated 26.2 jar. Creaking is the pale-garden hostile that FREEZES when a player looks at it and
// moves only when NOT observed (checkCanMove); when it catches a player unobserved within 12 blocks it
// ACTIVATES and attacks. It is tied to a Creaking Heart (HOME_POS) that makes it near-invulnerable. It is
// a Brain mob; this port lands the attributes + spawn + the SIGNATURE observed-freeze (CAN_MOVE toggled by
// checkCanMove) + activation + the melee attack, with the Creaking Heart block-entity deferred. Additive +
// per-type-gated behind e.isCreaking (false for every other entity; the pig oracle stays byte-identical).
//
// VANILLA (verified javap this task):
//   createAttributes: Monster.createMonsterAttributes + MAX_HEALTH 1.0 + MOVEMENT_SPEED 0.4000000059604645
//     + ATTACK_DAMAGE 3.0 + FOLLOW_RANGE 32.0 + STEP_HEIGHT 1.0625.
//   constants: ATTACK_ANIMATION_DURATION 15, ATTACK_INTERVAL 40, ACTIVATION_RANGE_SQ 144.0 (12 blocks),
//     MOVEMENT_SPEED_WHEN_FIGHTING 0.4, SPEED_MULTIPLIER_WHEN_IDLING 0.3.
//   canMove(): CAN_MOVE (default true). aiStep(): compute checkCanMove(); if it differs from CAN_MOVE fire
//     ENTITY_ACTION + CREAKING_UNFREEZE (unfreeze) or stopInPlace + CREAKING_FREEZE (freeze); set CAN_MOVE.
//   checkCanMove(): for each NEAREST_PLAYERS (empty -> if active deactivate; return true): skip allied;
//     if active AND player wears a disguise -> skip; if isLookingAtMe(player, 0.5, false, true, [eyeY,
//     y+0.5*scale, (eyeY+y)/2]) -> if active return false else if distanceToSqr < 144 activate(player)
//     return false. After the loop, if no player considered AND active -> deactivate; return true.
//   activate(player): brain ATTACK_TARGET = player; ENTITY_ACTION; CREAKING_ACTIVATE; setIsActive(true).
//   doHurtTarget: attackAnimationRemainingTicks = 15; broadcastEntityEvent(4); Monster.doHurtTarget
//     (ATTACK_DAMAGE 3). isHeartBound(): getHomePos() != null.
//
// LANDED: the 5 attributes, spawn, the SIGNATURE checkCanMove observed-freeze (freeze while a player looks
// at the Creaking; move only when unobserved), activation within 12 blocks when unobserved, the melee
// attack (ATTACK_DAMAGE 3 + 15-tick anim), and the CAN_MOVE state toggle. RNG on the Creaking OWN stream.
//
// v1 STUBS (cited): the Creaking Heart BLOCK-ENTITY (HOME_POS + the heart-bound near-invulnerability +
// tearDown) is DEFERRED -- a v1 Creaking is unbound (isHeartBound false) so hurtServer is the normal path.
// The player-disguise gate is a cited constant (no disguise subsystem: a player is never disguised, so the
// look always counts). The Brain graph + navigation speed-when-fighting + sounds + animation are reduced
// to the code-driven creakingAiStep.

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Creaking constants (VERIFIED javap this task -- constant pool values).
const (
	creakingMaxHealth      = 1.0                // createAttributes MAX_HEALTH 1.0
	creakingMovementSpeed  = 0.4000000059604645 // createAttributes MOVEMENT_SPEED (float-widened)
	creakingAttackDamage   = 3.0                // createAttributes ATTACK_DAMAGE 3.0
	creakingFollowRange    = 32.0               // createAttributes FOLLOW_RANGE 32.0
	creakingStepHeight     = 1.0625             // createAttributes STEP_HEIGHT 1.0625
	creakingAttackAnimDur  = 15                 // ATTACK_ANIMATION_DURATION 15
	creakingAttackInterval = 40                 // ATTACK_INTERVAL 40
	creakingActivationSq   = 144.0              // ACTIVATION_RANGE_SQ 144.0 (12 blocks)
	creakingGazeCone       = 0.5                // checkCanMove isLookingAtMe coneSize 0.5 (ldc2_w 0.5d)
)

// creakingAttackCooldowns holds the per-entity CreakingAi MeleeAttack inter-attack cooldown, keyed by
// entity id. Creaking.ATTACK_INTERVAL == 40 is the interval arg to CreakingAi's MeleeAttack.create(pred,40):
// the melee fires at most once per 40 ticks. This is DISTINCT from the 15-tick attackAnimationRemainingTicks
// (Creaking.ATTACK_ANIMATION_DURATION == 15, an animation-only counter). The cooldown lives here (not on the
// shared *Entity, whose struct is owned by another domain); a Creaking is a rare code-spawned mob so the map
// stays tiny, and the key is the entity id. The tick loop is single-threaded over a region's mobs
// (creakingAiStep runs inside the region step), so a plain map is safe here.
//	[VERIFIED javap CreakingAi: bipush 40 -> MeleeAttack.create(Predicate,I); Creaking.ATTACK_INTERVAL
//	 ConstantValue int 40; Creaking.ATTACK_ANIMATION_DURATION ConstantValue int 15 (animation only).]
var creakingAttackCooldowns = map[int32]int{}

// spawnCreaking creates a hostile Creaking at (x,y,z) and adds it to the owner region store. CAN_MOVE
// starts true (defineSynchedData CAN_MOVE default); unbound (no Creaking Heart, isHeartBound false).
// Minimal e.ai (per-entity rng + attack-target slot); NO goalSelector (code-driven creakingAiStep).
// initSpawnHealth seeds health from MAX_HEALTH (1.0). Cite Creaking(EntityType, Level) + createAttributes
// + defineSynchedData.
func (t *TickLoop) spawnCreaking(x, y, z float64) *Entity {
	c := NewEntity(t.idAlloc.AllocID(), entity.Creaking, x, y, z)
	c.isCreaking = true
	c.creakingCanMove = true // CAN_MOVE default true
	initSpawnHealth(c)       // setHealth(getMaxHealth()) -> 1.0
	c.ai = &mobAI{}
	reseedMobAI(c.ai, c.id)
	owner := t.regionForEntity(c)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(c)
	return c
}

// creakingIsLookingAtMe ports LivingEntity.isLookingAtMe(player, 0.5, adjustForDistance=false,
// seeThrough=true, gazeHeights=[eyeY, y+0.5*scale, (eyeY+y)/2]) for the Creaking. Since adjustForDistance
// is false, d==1.0 (dot > 1.0 - coneSize). The three gaze heights collapse to a loop; ANY height that the
// player aims at counts. seeThroughTransparentBlocks + hasLineOfSight are CITE-DEFERRED (visible). NO RNG.
// Cite Creaking.checkCanMove isLookingAtMe call.
func creakingIsLookingAtMe(e *Entity, p *tickPlayer) bool {
	lx, ly, lz := playerViewVector(p.yaw, p.pitch) // player view vector, normalized
	eyeY := e.y + e.height*0.85                    // getEyeY() approx: feet + eyeHeight (0.85*height default)
	scale := 1.0                                    // getScale(): 1.0 for a non-baby (baby scale deferred)
	heights := [3]float64{eyeY, e.y + 0.5*scale, (eyeY + e.y) / 2.0}
	pex, pey, pez := p.x, p.y+playerStandingEyeHeight, p.z // player eye position
	for _, h := range heights {
		dx := e.x - pex
		dy := h - pey
		dz := e.z - pez
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist == 0 {
			continue
		}
		dot := lx*(dx/dist) + ly*(dy/dist) + lz*(dz/dist)
		if dot > 1.0-creakingGazeCone { // adjustForDistance=false -> d=1.0
			return true
		}
	}
	return false
}

// creakingActivate ports Creaking.activate(player): set ATTACK_TARGET + IS_ACTIVE. Cite Creaking.activate.
func (t *TickLoop) creakingActivate(e *Entity, p *tickPlayer) {
	if e.ai != nil {
		e.ai.attackTargetID = p.entityID // brain ATTACK_TARGET = player
	}
	e.creakingActive = true // setIsActive(true)
}

// creakingDeactivate ports Creaking.deactivate(): clear IS_ACTIVE (+ the attack target). Cite deactivate.
func (t *TickLoop) creakingDeactivate(e *Entity) {
	e.creakingActive = false
	if e.ai != nil {
		e.ai.attackTargetID = 0
	}
	delete(creakingAttackCooldowns, e.id) // drop the MeleeAttack cooldown state + reclaim the map slot
}

// creakingCheckCanMove ports Creaking.checkCanMove(): for each nearby player (empty -> if active
// deactivate; return true): if active AND looked-at return false (freeze); else if unobserved AND within
// 144 (12 blocks) AND looked-at activate + return false; if no player looks -> can move. Simplified to the
// observed-freeze core (the disguise gate is a cited constant-true). Cite Creaking.checkCanMove.
func (t *TickLoop) creakingCheckCanMove(e *Entity) bool {
	followRange := e.getAttributeValue(attribute.FollowRange) // 32.0 (NEAREST_PLAYERS sensor radius)
	rangeSqr := followRange * followRange
	considered := false
	for _, p := range t.players {
		if p == nil || p.dead || p.gameMode == gameModeSpectator {
			continue
		}
		if distanceToSqrPlayer(p, e) > rangeSqr {
			continue // outside the NEAREST_PLAYERS radius
		}
		considered = true
		if !creakingIsLookingAtMe(e, p) {
			continue // this player is not looking; check the next
		}
		// The player is looking at the Creaking.
		if e.creakingActive {
			return false // active + observed -> freeze (cannot move)
		}
		if distanceToSqrPlayer(p, e) < creakingActivationSq {
			t.creakingActivate(e, p) // unobserved-caught within 12 blocks -> activate
			return false
		}
	}
	if !considered && e.creakingActive {
		t.creakingDeactivate(e) // no players around -> deactivate
	}
	return true // no observer -> can move
}

// creakingAiStep ports Creaking.aiStep + customServerAiStep (brain reduced), driven per-type from tickAI
// (gated on typ == entity.Creaking.ID, AFTER serverAiStep). Order: (1) tick the attack-anim counter; (2)
// recompute checkCanMove and toggle CAN_MOVE (freeze/unfreeze); (3) when active + in melee range, attack.
// RNG on the Creaking OWN stream. Cite Creaking.aiStep + doHurtTarget.
func (t *TickLoop) creakingAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	if e.creakingAttackAnimTicks > 0 {
		e.creakingAttackAnimTicks-- // attackAnimationRemainingTicks-- (ANIMATION ONLY, 15 ticks)
	}
	// CreakingAi MeleeAttack cooldown (Creaking.ATTACK_INTERVAL == 40): the inter-attack timer. DISTINCT
	// from the 15-tick animation counter above — the melee may fire at most once per 40 ticks.
	if cd := creakingAttackCooldowns[e.id]; cd > 0 {
		creakingAttackCooldowns[e.id] = cd - 1
	}
	// aiStep: compute checkCanMove; on a change toggle CAN_MOVE (the freeze/unfreeze event + sound).
	can := t.creakingCheckCanMove(e)
	if can != e.creakingCanMove {
		e.creakingCanMove = can // set CAN_MOVE (ENTITY_ACTION + CREAKING_FREEZE/UNFREEZE deferred sound)
	}
	// While active + adjacent, melee the target. The FIRE gate is the 40-tick MeleeAttack cooldown
	// (CreakingAi MeleeAttack.create(pred, 40)) — NOT the 15-tick animation. broadcastEntityEvent(4) still
	// (re)arms the 15-tick animation each hit for the client swing (Creaking.doHurtTarget).
	if e.creakingActive && e.ai != nil && e.ai.attackTargetID != 0 {
		target := t.playerByEntityID(e.ai.attackTargetID)
		if target != nil && !target.dead && creakingAttackCooldowns[e.id] <= 0 && isWithinMeleeAttackRange(e, target) {
			t.creakingDoHurtTarget(e, target)
		}
	}
}

// creakingDoHurtTarget ports Creaking.doHurtTarget: attackAnimationRemainingTicks = 15;
// broadcastEntityEvent(4); Monster.doHurtTarget (ATTACK_DAMAGE 3). Cite Creaking.doHurtTarget.
func (t *TickLoop) creakingDoHurtTarget(e *Entity, target *tickPlayer) {
	e.creakingAttackAnimTicks = creakingAttackAnimDur         // = 15 (client swing animation)
	creakingAttackCooldowns[e.id] = creakingAttackInterval    // = 40 (CreakingAi MeleeAttack re-arm)
	t.broadcastMobSwing(e)                                    // broadcastEntityEvent(4) analogue
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // == 3.0
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg)
}
