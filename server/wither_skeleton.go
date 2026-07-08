package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
)

const (
	witherSkeletonAttackDamage    = 4.0
	witherSkeletonWitherDuration  = 200
	witherSkeletonWitherAmplifier = 0
	witherSkeletonMeleeCooldown   = meleeAttackResetCooldown
)

// spawnWitherSkeleton creates a WitherSkeleton at (x,y,z), holds a STONE_SWORD (populateDefaultEquipmentSlots)
// and sets ATTACK_DAMAGE base 4.0 (finalizeSpawn). Minimal e.ai; NO goalSelector (code-driven witherSkeletonAiStep,
// like spawnBlaze). Cite WitherSkeleton ctor + finalizeSpawn + populateDefaultEquipmentSlots.
func (t *TickLoop) spawnWitherSkeleton(x, y, z float64) *Entity {
	w := NewEntity(t.idAlloc.AllocID(), entity.WitherSkeleton, x, y, z)
	w.isWitherSkeleton = true
	// WitherSkeleton.finalizeSpawn: getAttribute(ATTACK_DAMAGE).setBaseValue(4.0) -- BEFORE initSpawnHealth.
	if w.attributes != nil {
		if inst := w.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			inst.SetBaseValue(witherSkeletonAttackDamage) // ldc2_w 4.0d
		}
	}
	populateWitherSkeletonEquipment(w) // setItemSlot(MAINHAND, new ItemStack(STONE_SWORD))
	initSpawnHealth(w)                 // setHealth(getMaxHealth()) -> 20.0
	w.ai = &mobAI{}
	reseedMobAI(w.ai, w.id)
	owner := t.regionForEntity(w)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(w)
	return w
}

// witherSkeletonTarget reads the current attack-target player (Mob.getTarget()), or nil. Mirrors blazeTarget.
func (t *TickLoop) witherSkeletonTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// witherSkeletonAcquireNearestPlayer ports AbstractSkeleton targetSelector: nearest live player within
// FOLLOW_RANGE (createMonsterAttributes 16.0). NO RNG. Cite AbstractSkeleton.registerGoals targetSelector.
func (t *TickLoop) witherSkeletonAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 16.0
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
			e.ai.attackTargetID = 0 // setTarget(null)
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
	}
}

// witherSkeletonMeleeAttack ports MeleeAttackGoal swing + WitherSkeleton.doHurtTarget: in reach, with LoS,
// swing cooldown elapsed -> swing + deal ATTACK_DAMAGE 4.0 through the player hurt path, then on a LANDED
// hit apply WITHER 200 (amp 0). The WITHER add is gated on super.doHurtTarget returning true. NO RNG.
// Cite MeleeAttackGoal.checkAndPerformAttack + WitherSkeleton.doHurtTarget.
func (t *TickLoop) witherSkeletonMeleeAttack(e *Entity, target *tickPlayer) {
	if e.meleeCooldown > 0 { // not time to attack yet
		e.meleeCooldown--
		return
	}
	if !isWithinMeleeAttackRange(e, target) {
		return
	}
	if !t.sensingHasLineOfSight(e, target) {
		return
	}
	e.meleeCooldown = witherSkeletonMeleeCooldown // resetAttackCooldown()
	t.broadcastMobSwing(e)                        // mob.swing(MAIN_HAND)

	// super.doHurtTarget: deal ATTACK_DAMAGE 4.0. Snapshot the landed flag (i-frame excess gate) BEFORE
	// applyDamage mutates state, so the WITHER add gates exactly like the vanilla super-hit guard.
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // == 4.0
	src := damageSourceMobAttack(e.id)
	hurt := !target.dead
	if hurt && float32(target.invulnerableTime) > hurtCooldownConst {
		amt := dmg
		if amt < 0 {
			amt = 0
		}
		hurt = amt > target.lastHurt
	}
	t.applyDamage(target, src, dmg) // the PLAYER hurt path
	if !hurt {
		return // super.doHurtTarget returned false -> no WITHER
	}
	// le.addEffect(new MobEffectInstance(WITHER, 200), this): WITHER 200 (amp 0) attributed to this mob.
	t.addPlayerEffect(target, e.id, effectWither, witherSkeletonWitherDuration, witherSkeletonWitherAmplifier, 1.0)
}

// witherSkeletonAiStep is the WitherSkeleton per-tick drive: acquire nearest player, then melee + WITHER.
// Gated in tickAI on typ == entity.WitherSkeleton.ID, AFTER serverAiStep. NO RNG. Cite AbstractSkeleton
// .registerGoals + WitherSkeleton.doHurtTarget.
func (t *TickLoop) witherSkeletonAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	t.witherSkeletonAcquireNearestPlayer(e)
	if target := t.witherSkeletonTarget(e); target != nil {
		t.witherSkeletonMeleeAttack(e, target)
	} else if e.meleeCooldown > 0 {
		e.meleeCooldown-- // keep the swing countdown draining with no target
	}
}

// populateWitherSkeletonEquipment ports WitherSkeleton.populateDefaultEquipmentSlots: MAINHAND = STONE_SWORD.
// It does NOT call super (no armor roll). RNG-FREE. Cite WitherSkeleton.populateDefaultEquipmentSlots.
func populateWitherSkeletonEquipment(e *Entity) {
	e.setItemSlot(eqSlotMainHand, itemStackOf(item.StoneSword)) // new ItemStack(Items.STONE_SWORD)
}
