package server

// ai_goals_ravager.go — RAIDER (Task): the Ravager.aiStep roar/stun/attack state machine + roar() AoE,
// PORTED 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.entity.monster.Ravager, CFR this
// session). Ravager uses the plain MeleeAttackGoal (NO RavagerMeleeAttackGoal inner class in 26.2, NO
// charge). doHurtTarget sets attackTick=10 + event 4. aiStep: speed lerp (immobile->0 / target?0.35:0.3),
// leaf-trample (DEFERRED, no block-break), then roar/attack/stun countdowns (roar() fires at roarTick==10;
// stun end re-arms roarTick=20). roar(): LivingEntity in BB.inflate(4.0) -> non-illager hurt 6.0 (mobAttack),
// non-player strongKnockback (dd=max(xd2+zd2,0.001); push(xd/dd*4, 0.2, zd/dd*4)). NO RNG. v1 DEFERRALS
// (cited): stun TRIGGER (shield-block, no subsystem), leaf-trample block-break, stunEffect particle (client).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Ravager constants (VERIFIED CFR Ravager).
const (
	ravagerAttackDuration = 10   // ATTACK_DURATION
	ravagerStunDuration   = 40   // STUN_DURATION (trigger deferred)
	ravagerRoarArmTicks   = 20   // roarTick set when the stun ends
	ravagerRoarFireTick   = 10   // roarTick value at which roar() fires
	ravagerBaseSpeed      = 0.3  // BASE_MOVEMENT_SPEED (no target)
	ravagerAttackSpeed    = 0.35 // ATTACK_MOVEMENT_SPEED (has target)
	ravagerSpeedLerpT     = 0.1  // Mth.lerp(0.1, base, maxSpeed)
	ravagerRoarInflate    = 4.0  // roar() AoE box inflate on all axes
	ravagerRoarDamage     = 6.0  // roar() hurtServer(mobAttack, 6.0)
	ravagerStrongKbHoriz  = 4.0  // strongKnockback horizontal scale
	ravagerStrongKbVert   = 0.2  // strongKnockback vertical
)

// Ravager entity-event bytes. handleEntityEvent(4)=attack swing; (69)=roar poof + client knockback.
const (
	entityEventRavagerAttack byte = 4
	entityEventRavagerRoar   byte = 69
)

// ravagerIsImmobile ports Ravager.isImmobile: health<=0 || attackTick>0 || stunnedTick>0 || roarTick>0.
func ravagerIsImmobile(e *Entity) bool {
	return e.health <= 0 || e.ravagerAttackTick > 0 || e.ravagerStunnedTick > 0 || e.ravagerRoarTick > 0
}

// ravagerDidHurt ports Ravager.doHurtTarget pre-super: attackTick=10; broadcastEntityEvent(4). Called from
// the melee-hit path for a ravager BEFORE the shared doHurtTarget deals damage. RAVAGER_ATTACK sound deferred.
func (t *TickLoop) ravagerDidHurt(e *Entity) {
	e.ravagerAttackTick = ravagerAttackDuration
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventRavagerAttack))
}

// ravagerAiStep ports Ravager.aiStep server branch (per-type hook, sibling of creeperAiStep), AFTER
// serverAiStep. Speed lerp, (leaf-trample DEFERRED), then roar/attack/stun countdowns.
func (t *TickLoop) ravagerAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
			if ravagerIsImmobile(e) {
				inst.SetBaseValue(0.0)
			} else {
				maxSpeed := ravagerBaseSpeed
				if mobTarget(e) != 0 {
					maxSpeed = ravagerAttackSpeed
				}
				base := inst.BaseValue()
				inst.SetBaseValue(base + (maxSpeed-base)*ravagerSpeedLerpT) // Mth.lerp(0.1, base, maxSpeed)
			}
		}
	}
	// leaf-trample block-break (horizontalCollision && mobGriefing): DEFERRED (no block-break); NO RNG.
	if e.ravagerRoarTick > 0 {
		e.ravagerRoarTick--
		if e.ravagerRoarTick == ravagerRoarFireTick {
			t.ravagerRoar(e)
		}
	}
	if e.ravagerAttackTick > 0 {
		e.ravagerAttackTick--
	}
	if e.ravagerStunnedTick > 0 {
		e.ravagerStunnedTick--
		if e.ravagerStunnedTick == 0 {
			// playSound(RAVAGER_ROAR): cite-deferred client sound.
			e.ravagerRoarTick = ravagerRoarArmTicks
		}
	}
}

// ravagerRoar ports Ravager.roar(): every LivingEntity in BB.inflate(4.0) -> non-illager hurt 6.0, non-
// player strongKnockback. NO RNG. Players take 6.0 but are NOT knocked back server-side (client event 69).
func (t *TickLoop) ravagerRoar(e *Entity) {
	box := e.AABB()
	lx, ly, lz := box.Lower[0]-ravagerRoarInflate, box.Lower[1]-ravagerRoarInflate, box.Lower[2]-ravagerRoarInflate
	ux, uy, uz := box.Upper[0]+ravagerRoarInflate, box.Upper[1]+ravagerRoarInflate, box.Upper[2]+ravagerRoarInflate
	src := damageSourceMobAttack(e.id)
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if !pointInBox(p.x, p.y, p.z, lx, ly, lz, ux, uy, uz) {
			continue
		}
		t.applyDamage(p, src, float32(ravagerRoarDamage))
	}
	for _, other := range t.cur().entities.all() {
		if other == nil || other.id == e.id || other.dead || !other.isAlive() {
			continue
		}
		if other.typ == e.typ { // exclude other ravagers
			continue
		}
		if !pointInBox(other.x, other.y, other.z, lx, ly, lz, ux, uy, uz) {
			continue
		}
		if !isAbstractIllager(other.typ) {
			t.applyDamageEntity(other, src, float32(ravagerRoarDamage))
		}
		ravagerStrongKnockback(e, other)
	}
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventRavagerRoar))
}

// ravagerStrongKnockback ports Ravager.strongKnockback: dd=max(xd2+zd2,0.001); push(xd/dd*4, 0.2, zd/dd*4).
func ravagerStrongKnockback(e, victim *Entity) {
	xd := victim.x - e.x
	zd := victim.z - e.z
	dd := math.Max(xd*xd+zd*zd, 0.001)
	victim.vx += xd / dd * ravagerStrongKbHoriz
	victim.vy += ravagerStrongKbVert
	victim.vz += zd / dd * ravagerStrongKbHoriz
}

// pointInBox reports whether the feet point is inside the [l,u] box (the reduced getEntitiesOfClass; a
// full AABB-intersect is the exact-parity refinement once the entity broad-phase lands).
func pointInBox(px, py, pz, lx, ly, lz, ux, uy, uz float64) bool {
	return px >= lx && px <= ux && py >= ly && py <= uy && pz >= lz && pz <= uz
}

// isAbstractIllager reports whether a wire type is an AbstractIllager subclass (Pillager/Vindicator/Evoker).
// Ravager itself is NOT one (extends Raider directly). Cite AbstractIllager.
func isAbstractIllager(typ entity.ID) bool {
	switch typ {
	case entity.Pillager.ID, entity.Vindicator.ID, entity.Evoker.ID:
		return true
	default:
		return false
	}
}
