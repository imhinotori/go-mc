// bee.go -- the Bee (net.minecraft.world.entity.animal.bee.Bee), a 1:1 port from the unobfuscated 26.2
// jar. Bee is a FLYING passive/neutral Animal that pollinates flowers and returns to a hive; when angered
// it STINGS a player (doHurtTarget) which sets HasStung + POISON on the victim and then, over the
// following ~1200 ticks, the bee dies from its own sting (customServerAiStep generic self-damage). This
// port lands the attributes + spawn + the "visibly alive" passive goal subset (Float/Tempt/Breed/Follow/
// Wander-as-stroll) + the SIGNATURE sting-death countdown. The hive/pollination goals and the anger
// target-selector are the DEFERRED behavior layer (they need the hive block-entity + a flower/POI
// subsystem + the neutral-anger machinery). Code-spawned (spawnBee) with a *mobAI carrying the passive
// goals; its per-tick extra is beeAiStep from tickAI (per-type-gated on typ == entity.Bee.ID), which runs
// the sting-death countdown.
//
// VANILLA (verified javap Bee this session):
//   createAttributes: Animal.createAnimalAttributes + MAX_HEALTH 10.0 + FLYING_SPEED 0.6000000238418579 +
//     MOVEMENT_SPEED 0.30000001192092896 + ATTACK_DAMAGE 2.0 (FOLLOW_RANGE stays createMobAttributes 16.0).
//   registerGoals goalSelector: @0 BeeAttackGoal(1.4, true); @1 BeeEnterHiveGoal; @2 BreedGoal(1.0);
//     @3 TemptGoal(1.25, BEE_FOOD, false); @3 ValidateHiveGoal; @3 ValidateFlowerGoal; @4 BeePollinateGoal;
//     @5 FollowParentGoal(1.25); @5 BeeLocateHiveGoal; @5 BeeGoToHiveGoal; @6 BeeGoToKnownFlowerGoal;
//     @7 BeeGrowCropGoal; @8 BeeWanderGoal; @9 FloatGoal. targetSelector: @1 BeeHurtByOtherGoal;
//     @2 BeeBecomeAngryTargetGoal; @3 ResetUniversalAngerTargetGoal(true).
//   doHurtTarget(level, target): src = damageSources().sting(this); flag = target.hurtServer(level, src,
//     (float)(int)getAttributeValue(ATTACK_DAMAGE)); if(flag){ doPostAttackEffects; if(target instanceof
//     LivingEntity le){ le.setStingerCount(+1); int p=(NORMAL)?10:(HARD)?18:0; if(p>0) le.addEffect(new
//     MobEffectInstance(POISON, p*20, 0), this); } setHasStung(true); stopBeingAngry(); playSound(BEE_STING); }.
//   customServerAiStep: boolean s=hasStung(); if(isInWater()) ++underWaterTicks; else underWaterTicks=0;
//     if(underWaterTicks>20) hurtServer(drown(),1.0F);  // UNCONDITIONAL -- an un-stung bee drowns too
//     if(s){ ++timeSinceSting; if(timeSinceSting % 5 == 0 && random.nextInt(Mth.clamp(1200-timeSinceSting,
//     1,1200)) == 0) hurtServer(generic(), getHealth()); } then nectar/updatePersistentAnger bookkeeping.
//
// PORTED 1:1 in this file: the createAttributes, the passive goal walk, the underwater-drown (underWaterTicks
// > 20 -> drown 1.0F, UNCONDITIONAL of sting state -- the un-stung-bee-never-drowns bug is fixed), and the
// SIGNATURE sting-then-die-over-1200-ticks countdown are EXACT.
//
// v1 STUBS (cited, DEFERRED behavior layer -- no hive block-entity / flower-POI / neutral-anger machinery
// yet): the hive/flower/pollination goal cluster (BeeGoToHiveGoal, BeeGoToKnownFlowerGoal, BeePollinateGoal,
// BeeEnterHiveGoal, BeeLocateHiveGoal, hasNectar/ticksWithoutNectarSinceExitingHive) + BeehiveBlockEntity
// (MAX_OCCUPANTS 3, MIN_OCCUPATION_TICKS 2400/600, honey_level 0-5); the neutral-anger target-selector
// (PERSISTENT_ANGER_TIME UniformInt 400-780, BeeAttackGoal, BeeHurtByOtherGoal, BeeBecomeAngryTargetGoal,
// updatePersistentAnger) so beeDoSting is not yet auto-fired; the stinger-count client cue + BEE_STING sound.
// The POISON victim effect is wired where the machinery exists (beeDoSting, NORMAL 10s; HARD 18s is DEFERRED
// with the anger goal that fires it).

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Bee constants (VERIFIED javap Bee this session).
const (
	beeMaxHealth      = 10.0                // createAttributes MAX_HEALTH 10.0
	beeMovementSpeed  = 0.30000001192092896 // createAttributes MOVEMENT_SPEED (float-widened)
	beeTemptSpeed     = 1.25                // @3 TemptGoal speedModifier 1.25 (ldc2_w 1.25d)
	beeFollowSpeed    = 1.25                // @5 FollowParentGoal speed 1.25 (ldc2_w 1.25d)
	beeBreedSpeed     = 1.0                 // @2 BreedGoal speed 1.0
	beeWanderSpeed    = 1.0                 // BeeWanderGoal ~ WaterAvoidingRandomStroll 1.0 (stroll analogue)
	beeStingDeathBase = 1200                // Mth.clamp(1200 - timeSinceSting, 1, 1200) window
	beeStingDeathMod  = 5                   // timeSinceSting % 5 == 0 death-roll cadence
	beeFoodTag           = "bee_food"       // ItemTags.BEE_FOOD (the tempt predicate)
	beePoisonSeconds     = 10               // NORMAL difficulty POISON duration (seconds); *20 = ticks (bipush 10)
	beePoisonSecondsHard = 18               // HARD difficulty POISON duration (seconds); *20 = ticks (bipush 18)
	beeDrownThreshold    = 20               // customServerAiStep: underWaterTicks > 20 -> drown (bipush 20, if_icmple)
	beeDrownDamage       = 1.0              // hurtServer(drown(), 1.0F) (fconst_1)
	// BeeBecomeAngryTargetGoal / startPersistentAngerTimer: PERSISTENT_ANGER_TIME = TimeUtil.rangeOfSeconds(
	// 20,39) = UniformInt(400,780); sample = Mth.randomBetweenInclusive(random,400,780) = 400 + nextInt(381)
	// (IDENTICAL to Wolf/IronGolem/ZombifiedPiglin). Cite Bee.PERSISTENT_ANGER_TIME + startPersistentAngerTimer.
	beePersistentAngerBase = 400
	beePersistentAngerSpan = 381
)

// newBeeAI builds the Bee passive AI: the "visibly alive" subset of Bee.registerGoals (the hive/flower
// cluster + the anger target-selector are DEFERRED, cited in the file header). Mirrors newPigAI shape
// (per-mob rng, navigation seed, canFloat, Animal pathfinding malus) with the Bee goal priorities:
//
//	2  BreedGoal(1.0)                      [MOVE|LOOK]
//	3  TemptGoal(1.25, BEE_FOOD, false)    [MOVE|LOOK]
//	5  FollowParentGoal(1.25)              [] (EMPTY)
//	8  WaterAvoidingRandomStrollGoal(1.0)  [MOVE]   (the BeeWanderGoal analogue)
//	9  FloatGoal                           [JUMP]
//
// The @0 BeeAttackGoal (the sting pursuit) is target-selector-driven; its MELEE + setHasStung is the
// code-driven sting (beeDoSting), DEFERRED as an autonomous pursuit goal (no wired neutral-anger target
// selector). The sting-death countdown is beeAiStep. Cite Bee.registerGoals.
func newBeeAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * beeMovementSpeed // seed with MOVEMENT_SPEED (0.3)
	m.navigation.canFloat = true                           // FloatGoal ctor: getNavigation().setCanFloat(true)
	applyAnimalPathfindingMalus(m)                         // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(2, newBreedGoal(beeBreedSpeed))
	m.goals.addGoal(3, newTemptGoal(beeTemptSpeed, func(id int32) bool { return itemInTag(id, beeFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(beeFollowSpeed))
	m.goals.addGoal(8, newWaterAvoidingRandomStrollGoal(beeWanderSpeed))
	m.goals.addGoal(9, newFloatGoal())
	return m
}

// spawnBee creates a Bee at (x,y,z) with the jar attributes and the passive goal AI, then adds it to the
// owner region store. baby toggles the AgeableMob baby age + half-scale box (Bee is an Animal). NO nectar/
// hive state (pollination is DEFERRED). initSpawnHealth seeds health from MAX_HEALTH (10.0). Cite
// Bee.createAttributes + Bee(EntityType, Level).
func (t *TickLoop) spawnBee(x, y, z float64, baby bool) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.Bee, x, y, z)
	b.isBee = true
	if baby {
		b.breedAge = babyStartAge
		b.refreshDimensions() // AgeableMob baby half-scale box (getDefaultDimensions baby-scale)
	}
	initSpawnHealth(b) // setHealth(getMaxHealth()) -> 10.0
	b.ai = newBeeAI()
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// beeDoSting ports Bee.doHurtTarget: hurt the target for (int)ATTACK_DAMAGE via the sting damage source,
// then -- on a landed hit -- setStingerCount(+1) (client cue DEFERRED), apply POISON scaled by the LIVE
// difficulty (NORMAL 10s, HARD 18s, otherwise 0 -> no add), setHasStung(true), and stopBeingAngry (clear
// the persistent anger endpoint + target). The BEE_STING sound is DEFERRED. After this the bee begins to
// die (beeAiStep countdown). RNG-free. Cite Bee.doHurtTarget (bytecode: level().getDifficulty() == NORMAL
// -> 10, == HARD -> 18, else 0; if p>0 addEffect(POISON, p*20, 0); setHasStung(true); stopBeingAngry()).
func (t *TickLoop) beeDoSting(e *Entity, target *tickPlayer) {
	dmg := float32(int(e.getAttributeValue(attribute.AttackDamage))) // (float)(int) ATTACK_DAMAGE == 2.0
	src := damageSourceByTypeName("minecraft:sting", e.id)           // damageSources().sting(this)
	hurt := !target.dead && (float32(target.invulnerableTime) <= hurtCooldownConst || dmg > target.lastHurt)
	t.applyDamage(target, src, dmg)
	if !hurt {
		return // if(flag){...} -- nothing on a rejected (i-frame) hit
	}
	// int p = (difficulty==NORMAL)?10:(difficulty==HARD)?18:0; the read is level().getDifficulty() (LIVE),
	// so it tracks a /difficulty change (t.levelDifficulty), not the scope-locked serverDifficulty const.
	p := 0
	switch t.levelDifficulty {
	case difficultyNormal:
		p = beePoisonSeconds
	case difficultyHard:
		p = beePoisonSecondsHard
	}
	if p > 0 {
		// addEffect(new MobEffectInstance(POISON, p*20, 0), this). addPlayerEffect(victim, ownerID, id,
		// durationTicks, amplifier, scale). EASY/PEACEFUL -> p==0 -> no add (a 0-tick effect, the ifle skip).
		t.addPlayerEffect(target, e.id, effectPoison, p*20, 0, 1.0)
	}
	e.beeHasStung = true // setHasStung(true) -> the bee now dies over ~1200 ticks (beeAiStep)
	t.beeStopBeingAngry(e) // stopBeingAngry(): setPersistentAngerEndTime(0) + setPersistentAngerTarget(null)
}

// beeStopBeingAngry ports NeutralMob.stopBeingAngry(): setRemainingPersistentAngerTime(0) (angerEndTime 0)
// + setPersistentAngerTarget(null) (angerTarget 0), and drop the live attack target so the stung bee stops
// pursuing. Called from beeDoSting after a landed sting (a bee stings ONCE, then dies). Cite Bee(NeutralMob)
// .stopBeingAngry + BeeAttackGoal.canUse (!bee.hasStung() -> no more attacks). RNG-free.
func (t *TickLoop) beeStopBeingAngry(e *Entity) {
	e.angerEndTime = 0
	e.angerTarget = 0
	if e.ai != nil {
		e.ai.attackTargetID = 0
	}
}

// beeIsAngry ports NeutralMob.isAngry(): angerEndTime > 0 AND (angerEndTime - gameTime) > 0.
func (t *TickLoop) beeIsAngry(e *Entity) bool {
	return e.angerEndTime > 0 && (e.angerEndTime-t.gametime) > 0
}

// beeTarget reads the current attack-target player, or nil.
func (t *TickLoop) beeTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// beeSetTarget ports BeeBecomeAngryTargetGoal's acquisition (a NearestAttackableTargetGoal<Player>): adopt
// the player as the attack target and, if not already, seed the persistent anger so isAngryAt holds. NO RNG
// beyond the single startPersistentAngerTimer draw on a fresh anger. Cite BeeBecomeAngryTargetGoal + Bee
// .startPersistentAngerTimer (400 + nextInt(381)).
func (t *TickLoop) beeSetTarget(e *Entity, p *tickPlayer) {
	if e.ai == nil {
		return
	}
	e.ai.attackTargetID = p.entityID
	if !isAngryAt(t, e, p.entityID) {
		e.angerEndTime = t.gametime + int64(beePersistentAngerBase+mobRandom(e).nextInt(beePersistentAngerSpan))
		e.angerTarget = p.entityID
	}
}

// beeAcquireAngryTarget ports the anger-gated BeeBecomeAngryTargetGoal: a bee targets a player only while its
// anger is LIVE and points at that player (and it has NOT already stung -- a stung bee is done attacking).
// Neutral (or already-stung) -> no target. NO RNG (the acquire itself; beeSetTarget draws only on a fresh
// anger). Cite BeeBecomeAngryTargetGoal + BeeAttackGoal.canUse (bee.isAngry() && !bee.hasStung()) + isAngryAt.
func (t *TickLoop) beeAcquireAngryTarget(e *Entity) {
	if e.ai == nil || e.beeHasStung {
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
	if !t.beeIsAngry(e) {
		return
	}
	p := t.playerByEntityID(e.angerTarget)
	if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
		return
	}
	t.beeSetTarget(e, p)
}

// beeAiStep is the Bee per-tick extra (Bee.customServerAiStep + the BeeAttackGoal melee pursuit), driven
// per-type from tickAI (gated on typ == entity.Bee.ID, AFTER serverAiStep). It runs, in order:
//
//  1. BeeAttackGoal (goalSelector @0, MeleeAttackGoal(1.4,true), canUse = super && isAngry && !hasStung):
//     while angry and NOT yet stung, pursue the anger target and, on the melee cooldown + in-range + LOS,
//     doHurtTarget == beeDoSting (which applies POISON, setHasStung, stopBeingAngry -> the sting-once).
//  2. the sting-death countdown (customServerAiStep hasStung branch): after a sting, ++timeSinceSting and on
//     the (% 5 == 0) cadence with the rising-probability nextInt gate take generic getHealth() self-damage.
//  3. updatePersistentAnger(level, FALSE) (customServerAiStep tail): in the gametime-endpoint anger model the
//     observable effect of the false flag is a bee whose anger has EXPIRED drops its target (no target-retain
//     without live anger, and no ResetUniversalAngerTargetGoal). beeAcquireAngryTarget already drops a stale
//     target when !isAngry, so the tail is the same drop expressed at acquire time.
//
// The underwater-drown + pollination/nectar bookkeeping are DEFERRED. All RNG is on the bee OWN per-entity
// rng: zero draws on a never-provoked, never-stung passive bee (both the acquire and the sting-death block
// are dormant); the anger-timer draw is player-provoke-gated, and the sting-death draw is post-sting-gated.
// Cite Bee.customServerAiStep + BeeAttackGoal + Bee.doHurtTarget + Bee(NeutralMob).updatePersistentAnger.
func (t *TickLoop) beeAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	hasStung := e.beeHasStung // boolean hasStung = hasStung();
	// isInWater() ? ++underWaterTicks : underWaterTicks = 0 -- UNCONDITIONAL, independent of sting state.
	if t.entityInWater(e) {
		e.beeUnderWaterTicks++
	} else {
		e.beeUnderWaterTicks = 0
	}
	if e.beeUnderWaterTicks > beeDrownThreshold {
		// hurtServer(damageSources().drown(), 1.0F) -- runs regardless of sting state (an un-stung bee STILL drowns).
		t.applyDamageEntity(e, damageSourceOf(damageTypeDrown), beeDrownDamage)
	}
	// (3, expressed at acquire) drop a stale target when the anger endpoint has passed (updatePersistentAnger
	// false: no target without live anger). Mirrors zombifiedPiglinAiStep's leading drop.
	if e.ai != nil && e.ai.attackTargetID != 0 && !t.beeIsAngry(e) {
		e.ai.attackTargetID = 0
	}
	// (1) BeeAttackGoal: acquire the anger target (anger-gated, !hasStung-gated) then melee it. A landed
	// beeDoSting sets hasStung + stopBeingAngry, so from the NEXT tick the acquire is inert and only the
	// sting-death countdown runs (a bee stings ONCE, then dies).
	t.beeAcquireAngryTarget(e)
	if target := t.beeTarget(e); target != nil && !e.beeHasStung {
		if e.meleeCooldown > 0 {
			e.meleeCooldown--
		} else if isWithinMeleeAttackRange(e, target) && t.sensingHasLineOfSight(e, target) {
			e.meleeCooldown = meleeAttackResetCooldown
			t.beeDoSting(e, target)
		}
	} else if e.meleeCooldown > 0 {
		e.meleeCooldown--
	}
	// (2) the sting-death countdown: only runs once the bee has stung (no RNG draw otherwise).
	if !hasStung {
		return // never stung: no death roll, no RNG draw (the passive/pursuing bee)
	}
	e.beeTimeSinceSting++ // ++timeSinceSting
	if e.beeTimeSinceSting%beeStingDeathMod != 0 {
		return // if(timeSinceSting % 5 == 0) gate -- no roll off-cadence
	}
	// random.nextInt(Mth.clamp(1200 - timeSinceSting, 1, 1200)) == 0 -> the death roll.
	bound := beeStingDeathBase - e.beeTimeSinceSting
	if bound < 1 {
		bound = 1
	} else if bound > beeStingDeathBase {
		bound = beeStingDeathBase
	}
	if mobRandom(e).nextInt(bound) == 0 {
		// hurtServer(generic(), getHealth()) -- self-damage equal to current health => death.
		t.applyDamageEntity(e, damageSourceOf(damageTypeGeneric), e.health)
	}
}
