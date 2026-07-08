// guardian.go -- the aquatic Guardian + its boss-tier ElderGuardian, a 1:1 port from the unobfuscated
// 26.2 jar (net.minecraft.world.entity.monster.Guardian and ElderGuardian). A Guardian is a water hostile
// that LOCKS a laser beam onto a target and, after an 80-tick charge (Elder: 60), deals indirect-magic
// damage + a melee follow-up. It also THORNS a melee attacker for 2.0 while stationary with spikes out. An
// ElderGuardian additionally pulses MINING_FATIGUE III onto every survival player within 50 blocks once
// every 1200 ticks (offset by its entity id), flashing the ghost/curse screen.
//
// Both are code-spawned with a minimal e.ai; the per-tick drive is guardianAiStep from tickAI (sibling of
// blazeAiStep/ghastAiStep, per-type-gated on e.guardian != nil). All guardian state is grouped behind ONE
// pointer (e.guardian) so a plain mob (the pig oracle) pays exactly one nil pointer -- additive-minimal,
// zero new RNG, byte-identical for every non-guardian.
//
// VANILLA (verified javap Guardian + ElderGuardian + GuardianAttackGoal + GuardianAttackSelector):
//
//	Guardian.createAttributes: Monster.createMonsterAttributes + ATTACK_DAMAGE 6.0 + MOVEMENT_SPEED 0.5 +
//	  MAX_HEALTH 30.0 (FOLLOW_RANGE default 16.0). ElderGuardian: + MOVEMENT_SPEED 0.30000001192092896 +
//	  ATTACK_DAMAGE 8.0 + MAX_HEALTH 80.0. bbox ELDER_SIZE_SCALE 1.9975/0.85 ~= 2.35.
//	getAttackDuration: Guardian 80, ElderGuardian 60 (the beam charge length).
//	GuardianAttackGoal.tick (LASER BEAM): with line of sight ++attackTime; at 0 setActiveAttackTarget; at
//	  attackTime >= getAttackDuration() f = 1.0F (+2.0 HARD)(+2.0 elder), target.hurtServer(indirectMagic
//	  (guardian,guardian), f) then guardian.doHurtTarget(target) (ATTACK_DAMAGE melee), setTarget(null).
//	  start() attackTime = -10; stop() setActiveAttackTarget(0).
//	Guardian.hurtServer (THORNS): if not moving and not source.is(AVOIDS_GUARDIAN_THORNS/THORNS) the direct
//	  LivingEntity attacker takes damageSources().thorns(this) 2.0F.
//	ElderGuardian.customServerAiStep (AoE): if (tickCount+getId())%1200==0 apply MobEffectInstance(
//	  MINING_FATIGUE, 6000, 2) via addEffectToPlayersAround(level,this,position(),50.0,mfi,1200).
//
// v1 STUBS (cited): the GuardianMoveControl swim locomotion + WaterBoundPathNavigation are DEFERRED behind
// the shared water-mob swim nav (newWaterMobAI); the beam charge particles / DATA_ID_ATTACK_TARGET client
// metadata + broadcastEntityEvent 21 + the GUARDIAN_ELDER_EFFECT screen packet are client-visual, DEFERRED.
// The OBSERVABLE gameplay -- the 80/60-tick charge, the end-of-charge damage (1 + HARD:2 + elder:2) with the
// i-frame-stacked melee follow-up, the 2.0 spike thorns, the 1200-tick MINING_FATIGUE III/6000/50 AoE via
// the real addPlayerEffect, and the out-of-water flop -- is EXACT.
package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

const (
	guardianAttackDuration              = 80
	elderGuardianAttackDuration         = 60
	guardianAttackStart                 = -10
	guardianBeamBaseDamage      float32 = 1.0
	guardianBeamHardBonus       float32 = 2.0
	guardianBeamElderBonus      float32 = 2.0
	guardianThornsDamage        float32 = 2.0
	guardianContinueRangeSqr            = 9.0
	elderEffectInterval                 = 1200
	elderEffectDuration                 = 6000
	elderEffectAmplifier                = 2
	elderEffectRadius                   = 50.0
	elderEffectDisplayLimit             = 1200
)

// guardianState is the tick-owned beam state behind ONE pointer. elder marks the ElderGuardian; attackTime
// mirrors GuardianAttackGoal.attackTime (-10..duration). Cite Guardian.GuardianAttackGoal.
type guardianState struct {
	elder      bool
	attackTime int32
}

// attackDuration ports Guardian.getAttackDuration() (80) / ElderGuardian.getAttackDuration() (60).
func (g *guardianState) attackDuration() int32 {
	if g.elder {
		return elderGuardianAttackDuration
	}
	return guardianAttackDuration
}

// spawnGuardian creates a Guardian (MAX_HEALTH 30 / ATTACK_DAMAGE 6 / MOVEMENT_SPEED 0.5) with the shared
// water-mob swim AI. Cite Guardian ctor + createAttributes.
func (t *TickLoop) spawnGuardian(x, y, z float64) *Entity {
	return t.spawnGuardianType(entity.Guardian, x, y, z, false)
}

// spawnElderGuardian creates an ElderGuardian (MAX_HEALTH 80 / ATTACK_DAMAGE 8 / MOVEMENT_SPEED 0.3; bbox
// ~2.35x) elder=true (60-tick charge + the +2 bonus + the AoE). Cite ElderGuardian ctor + createAttributes.
func (t *TickLoop) spawnElderGuardian(x, y, z float64) *Entity {
	return t.spawnGuardianType(entity.ElderGuardian, x, y, z, true)
}

// spawnGuardianType marks the entity a water mob (drown inversion + flop), attaches the beam state, seeds
// health, wires the swim AI at MOVEMENT_SPEED (GuardianMoveControl DEFERRED), adds it to the region store.
func (t *TickLoop) spawnGuardianType(typ entity.Entity, x, y, z float64, elder bool) *Entity {
	g := NewEntity(t.idAlloc.AllocID(), typ, x, y, z)
	g.isWaterMob = true
	g.guardian = &guardianState{elder: elder}
	initSpawnHealth(g)
	g.ai = newWaterMobAI(g.getAttributeValue(attribute.MovementSpeed))
	reseedMobAI(g.ai, g.id)
	owner := t.regionForEntity(g)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(g)
	return g
}

func (t *TickLoop) guardianAiStep(e *Entity) {
	if e.dead || e.health <= 0 || e.guardian == nil {
		return
	}
	t.guardianAcquireNearestPlayer(e)
	t.guardianAttackGoalTick(e)
	if e.guardian.elder {
		t.elderGuardianEffectPulse(e)
	}
}

// guardianTarget reads the current attack-target player (Mob.getTarget via e.ai.attackTargetID).
func (t *TickLoop) guardianTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// guardianAcquireNearestPlayer ports Guardian @1 NearestAttackableTargetGoal + GuardianAttackSelector: the
// nearest live player within FOLLOW_RANGE (16). v1 players only (squid/axolotl prey is a cited gap). On loss
// GuardianAttackGoal.stop resets the beam. Cite Guardian.registerGoals targetSelector.
func (t *TickLoop) guardianAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange)
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
			e.ai.attackTargetID = 0
			guardianAttackGoalStop(e)
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
		e.ai.attackTargetID = best.entityID
		guardianAttackGoalStart(e)
	}
}

// guardianAttackGoalStart ports GuardianAttackGoal.start: attackTime = -10. Cite GuardianAttackGoal.start.
func guardianAttackGoalStart(e *Entity) {
	if e.guardian == nil {
		return
	}
	e.guardian.attackTime = guardianAttackStart
}

// guardianAttackGoalStop ports GuardianAttackGoal.stop: setActiveAttackTarget(0) -- reset the beam charge.
// Cite GuardianAttackGoal.stop.
func guardianAttackGoalStop(e *Entity) {
	if e.guardian == nil {
		return
	}
	e.guardian.attackTime = guardianAttackStart
}

func (t *TickLoop) guardianAttackGoalTick(e *Entity) {
	g := e.guardian
	if g == nil {
		return
	}
	target := t.guardianTarget(e)
	if target == nil {
		return
	}
	if !t.mobHasLineOfSight(e, target) {
		e.ai.attackTargetID = 0
		guardianAttackGoalStop(e)
		return
	}
	g.attackTime++
	switch {
	case g.attackTime == 0:
	case g.attackTime >= g.attackDuration():
		f := guardianBeamBaseDamage
		if t.difficultyIsHard() {
			f += guardianBeamHardBonus
		}
		if g.elder {
			f += guardianBeamElderBonus
		}
		t.applyDamage(target, damageSourceIndirectMagic(e.id), f)
		t.guardianDoHurtTarget(e, target)
		e.ai.attackTargetID = 0
		guardianAttackGoalStop(e)
	}
}

// guardianDoHurtTarget ports guardian.doHurtTarget: deal ATTACK_DAMAGE (6 guardian / 8 elder) via the shared
// player hurt path (mob_attack, attacker = e.id). Cite Guardian.doHurtTarget (== Mob.doHurtTarget).
func (t *TickLoop) guardianDoHurtTarget(e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage))
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg)
}

// difficultyIsHard ports level.getDifficulty() == HARD reading t.levelDifficulty (init NORMAL). Cite GuardianAttackGoal.tick.
func (t *TickLoop) difficultyIsHard() bool {
	return t.levelDifficulty == difficultyHard
}

func (t *TickLoop) guardianHurtThorns(e *Entity, src damageSource) {
	if e.guardian == nil {
		return
	}
	if t.guardianIsMoving(e) {
		return
	}
	if src.is("avoids_guardian_thorns") {
		return
	}
	if src.typeTag == damageTypeThorns {
		return
	}
	attackerID := src.attacker
	if attackerID == 0 {
		return
	}
	src2 := damageSourceByTypeName("minecraft:thorns", e.id)
	if p := t.playerByEntityID(attackerID); p != nil {
		t.applyDamage(p, src2, guardianThornsDamage)
		return
	}
	if attacker := t.entityByIDAnyRegion(attackerID); attacker != nil {
		t.applyDamageEntity(attacker, src2, guardianThornsDamage)
	}
}

// guardianIsMoving ports Guardian.isMoving() (DATA_ID_MOVING). GuardianMoveControl DEFERRED; a v1 guardian
// reports moving iff its navigation has a committed want-target (hasTarget). Cite Guardian.isMoving.
func (t *TickLoop) guardianIsMoving(e *Entity) bool {
	if e.ai == nil {
		return false
	}
	return e.ai.hasTarget
}

func (t *TickLoop) elderGuardianEffectPulse(e *Entity) {
	if (t.gametime+int64(e.id))%elderEffectInterval != 0 {
		return
	}
	radiusSqr := elderEffectRadius * elderEffectRadius
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if !guardianPlayerIsSurvival(p) {
			continue
		}
		if distanceToSqrPlayer(p, e) > radiusSqr {
			continue
		}
		t.addPlayerEffect(p, e.id, effectMiningFatigue, elderEffectDuration, elderEffectAmplifier, 1.0)
	}
}

// guardianPlayerIsSurvival ports ServerPlayerGameMode.isSurvival() (the addEffectToPlayersAround gate).
func guardianPlayerIsSurvival(p *tickPlayer) bool {
	return p.gameMode == gameModeSurvival
}
