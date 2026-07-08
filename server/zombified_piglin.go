package server

// zombified_piglin.go -- the ZombifiedPiglin (net.minecraft.world.entity.monster.zombie.ZombifiedPiglin),
// a 1:1 port from the unobfuscated 26.2 jar. It extends Zombie AND implements NeutralMob: a NEUTRAL nether
// undead that ignores players until PROVOKED (hit by a player, or ALERTED by an already-angry pack member),
// then goes angry and RETALIATES, spreading its anger to nearby zombified piglins (the anger pack). It is
// fire/lava immune (a nether type) and, unlike a normal Zombie, does NOT burn in daylight. It is the
// conversion target of a Piglin zombifying off-nether (piglinFinishConversion -> spawnZombifiedPiglin).
// Per-tick drive is zombifiedPiglinAiStep from tickAI (per-type-gated on typ == entity.ZombifiedPiglin.ID),
// reusing the shared NeutralMob anger model (angerEndTime/angerTarget) the wolf/iron_golem already use.
//
// VANILLA (verified javap ZombifiedPiglin + Zombie this session):
//   createAttributes = Zombie.createAttributes + SPAWN_REINFORCEMENTS_CHANCE 0.0 + MOVEMENT_SPEED
//     0.23000000417232513 + ATTACK_DAMAGE 5.0. Zombie base folds MAX_HEALTH 20, FOLLOW_RANGE 35,
//     MOVEMENT_SPEED 0.23, ATTACK_DAMAGE 3 (overridden to 5), ARMOR 2, SPAWN_REINFORCEMENTS_CHANCE 0.
//   Constants: FIRST_ANGER_SOUND_DELAY (0,20); PERSISTENT_ANGER_TIME (400,780) sample 400 + nextInt(381)
//     (IDENTICAL to Wolf/IronGolem); ALERT_INTERVAL (80,120); ALERT_RANGE_Y 10.0.
//   setTarget(le): fresh target seeds the anger-sound + alert timers; super.setTarget(le).
//   customServerAiStep: maybe add/remove SPEED_MODIFIER_ATTACKING; updatePersistentAnger(level,true);
//     if getTarget non-nil maybeAlertOthers(); super.customServerAiStep.
//   maybeAlertOthers(): throttle by ticksUntilNextAlert; on 0 + LOS(target) alertOthers(); reset throttle.
//   alertOthers(): getEntitiesOfClass(ZombifiedPiglin, AABB inflated (FOLLOW_RANGE,10.0,FOLLOW_RANGE)),
//     filter(o != this), filter(o.getTarget nil), filter(NOT allied to target), forEach setTarget(target).
//   startPersistentAngerTimer(): 400 + nextInt(381). NeutralMob.isAngry(): endTime>0 AND endTime-gameTime>0.
//   isSunSensitive INHERITED true, BUT fireImmune EntityType makes the daylight-burn a no-op (no sun-burn).
//
// v1 STUBS (cited): the goal graph + SPEED_MODIFIER_ATTACKING boost + anger sound + spear/sword roll are
// goal-style/client cite-deferred. Observable gameplay (attributes, neutral-until-provoked, retaliate +
// pack anger-spread within FOLLOW_RANGE, fire/lava immunity, no sun-burn, melee 5.0, conversion) is EXACT.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// ZombifiedPiglin constants (VERIFIED javap this session).
const (
	zombifiedPiglinAlertRangeY         = 10.0 // ALERT_RANGE_Y (alertOthers AABB inflate Y)
	zombifiedPiglinAlertIntervalMin    = 80   // ALERT_INTERVAL UniformInt(80,120): 80 + nextInt(41)
	zombifiedPiglinAlertIntervalSpan   = 41
	zombifiedPiglinPersistentAngerBase = 400 // PERSISTENT_ANGER_TIME (400,780): 400 + nextInt(381)
	zombifiedPiglinPersistentAngerSpan = 381
)

// spawnZombifiedPiglin creates a ZombifiedPiglin at (x,y,z) with the jar attributes; it starts NEUTRAL
// (angerEndTime 0). Minimal e.ai; NO goalSelector. initSpawnHealth seeds MAX_HEALTH 20.0. Cite createAttributes.
func (t *TickLoop) spawnZombifiedPiglin(x, y, z float64) *Entity {
	zp := NewEntity(t.idAlloc.AllocID(), entity.ZombifiedPiglin, x, y, z)
	zp.isZombifiedPiglin = true
	initSpawnHealth(zp)
	zp.ai = &mobAI{}
	reseedMobAI(zp.ai, zp.id)
	owner := t.regionForEntity(zp)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return zp
	}
	owner.entities.add(zp)
	return zp
}

// zombifiedPiglinIsAngry ports NeutralMob.isAngry(): angerEndTime > 0 AND (angerEndTime - gameTime) > 0.
func (t *TickLoop) zombifiedPiglinIsAngry(e *Entity) bool {
	return e.angerEndTime > 0 && (e.angerEndTime-t.gametime) > 0
}

// zombifiedPiglinTarget reads the current attack-target player, or nil.
func (t *TickLoop) zombifiedPiglinTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// zombifiedPiglinAcquireAngryTarget ports the anger-gated NearestAttackableTargetGoal<Player>: a zombified
// piglin targets a player only while its anger is LIVE and points at that player. Neutral -> no target (the
// neutral-until-provoked keystone). NO RNG. Cite NearestAttackableTargetGoal + isAngryAt.
func (t *TickLoop) zombifiedPiglinAcquireAngryTarget(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange)
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr || !isAngryAt(t, e, e.ai.attackTargetID) {
			e.ai.attackTargetID = 0
		} else {
			return
		}
	}
	if !t.zombifiedPiglinIsAngry(e) {
		return
	}
	p := t.playerByEntityID(e.angerTarget)
	if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
		return
	}
	t.zombifiedPiglinSetTarget(e, p)
}

// zombifiedPiglinSetTarget ports ZombifiedPiglin.setTarget(le): on a FRESH acquisition seed the alert
// throttle; adopt/extend the player-anger so isAngryAt holds (needed for a spread target not itself hit).
// Cite ZombifiedPiglin.setTarget + startPersistentAngerTimer.
func (t *TickLoop) zombifiedPiglinSetTarget(e *Entity, p *tickPlayer) {
	if e.ai == nil {
		return
	}
	fresh := e.ai.attackTargetID == 0
	if fresh {
		e.zombifiedPiglinAlertCooldown = zombifiedPiglinAlertIntervalMin + mobRandom(e).nextInt(zombifiedPiglinAlertIntervalSpan)
	}
	e.ai.attackTargetID = p.entityID
	if !isAngryAt(t, e, p.entityID) {
		e.angerEndTime = t.gametime + int64(zombifiedPiglinPersistentAngerBase+mobRandom(e).nextInt(zombifiedPiglinPersistentAngerSpan))
		e.angerTarget = p.entityID
	}
}

// zombifiedPiglinMaybeAlertOthers ports ZombifiedPiglin.maybeAlertOthers: throttle, then on LOS to the
// target spread anger to the pack; reset the throttle. Cite ZombifiedPiglin.maybeAlertOthers.
func (t *TickLoop) zombifiedPiglinMaybeAlertOthers(e *Entity) {
	if e.zombifiedPiglinAlertCooldown > 0 {
		e.zombifiedPiglinAlertCooldown--
		return
	}
	if target := t.zombifiedPiglinTarget(e); target != nil && t.sensingHasLineOfSight(e, target) {
		t.zombifiedPiglinAlertOthers(e, target)
	}
	e.zombifiedPiglinAlertCooldown = zombifiedPiglinAlertIntervalMin + mobRandom(e).nextInt(zombifiedPiglinAlertIntervalSpan)
}

// zombifiedPiglinAlertOthers ports ZombifiedPiglin.alertOthers: the ANGER-PACK spread. Every OTHER zombified
// piglin in the (FOLLOW_RANGE, 10.0, FOLLOW_RANGE) box with NO target adopts this mob target. Filter chain is
// EXACT (o != this; o.getTarget nil; NOT allied to target). Cite ZombifiedPiglin.alertOthers.
func (t *TickLoop) zombifiedPiglinAlertOthers(e *Entity, target *tickPlayer) {
	followRange := e.getAttributeValue(attribute.FollowRange)
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return
	}
	for _, other := range owner.entities.all() {
		if other == nil || other == e || other.dead || !other.isZombifiedPiglin {
			continue
		}
		if math.Abs(other.x-e.x) > followRange || math.Abs(other.y-e.y) > zombifiedPiglinAlertRangeY || math.Abs(other.z-e.z) > followRange {
			continue
		}
		if other.ai == nil || other.ai.attackTargetID != 0 {
			continue
		}
		t.zombifiedPiglinSetTarget(other, target)
	}
}

// zombifiedPiglinDoHurtTarget ports the melee doHurtTarget: swing + apply ATTACK_DAMAGE (5.0) as a mobAttack.
func (t *TickLoop) zombifiedPiglinDoHurtTarget(e *Entity, target *tickPlayer) {
	t.broadcastMobSwing(e)
	dmg := float32(e.getAttributeValue(attribute.AttackDamage))
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg)
}

// zombifiedPiglinAiStep is the per-tick drive (ports customServerAiStep): the anger endpoint auto-expires;
// drop a stale target when neutral, acquire the angry target, alert the pack, then goal-style melee. Gated
// in tickAI on typ == entity.ZombifiedPiglin.ID, AFTER serverAiStep. RNG only on the mob OWN stream.
func (t *TickLoop) zombifiedPiglinAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.ai != nil && e.ai.attackTargetID != 0 && !t.zombifiedPiglinIsAngry(e) {
		e.ai.attackTargetID = 0
	}
	t.zombifiedPiglinAcquireAngryTarget(e)
	target := t.zombifiedPiglinTarget(e)
	if target != nil {
		t.zombifiedPiglinMaybeAlertOthers(e)
		if e.meleeCooldown > 0 {
			e.meleeCooldown--
		} else if isWithinMeleeAttackRange(e, target) && t.sensingHasLineOfSight(e, target) {
			e.meleeCooldown = meleeAttackResetCooldown
			t.zombifiedPiglinDoHurtTarget(e, target)
		}
	} else if e.meleeCooldown > 0 {
		e.meleeCooldown--
	}
}
