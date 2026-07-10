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
	beeFoodTag        = "bee_food"          // ItemTags.BEE_FOOD (the tempt predicate)
	beePoisonSeconds  = 10                  // NORMAL difficulty POISON duration (seconds); *20 = ticks
	beeDrownThreshold = 20                  // customServerAiStep: underWaterTicks > 20 -> drown (bipush 20, if_icmple)
	beeDrownDamage    = 1.0                 // hurtServer(drown(), 1.0F) (fconst_1)
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
// then -- on a landed hit -- apply POISON, set HasStung, and (deferred) stop being angry. The stinger-
// count client cue + BEE_STING sound are deferred. After this the bee begins to die (beeAiStep countdown).
// RNG-free. Cite Bee.doHurtTarget. Exposed so the sting is wired the moment a neutral-anger target lands.
func (t *TickLoop) beeDoSting(e *Entity, target *tickPlayer) {
	dmg := float32(int(e.getAttributeValue(attribute.AttackDamage))) // (float)(int) ATTACK_DAMAGE == 2.0
	src := damageSourceByTypeName("minecraft:sting", e.id)           // damageSources().sting(this)
	hurt := !target.dead && (float32(target.invulnerableTime) <= hurtCooldownConst || dmg > target.lastHurt)
	t.applyDamage(target, src, dmg)
	if !hurt {
		return
	}
	// POISON p seconds (NORMAL 10). addPlayerEffect(victim, ownerID, id, durationTicks, amplifier, scale).
	t.addPlayerEffect(target, e.id, effectPoison, beePoisonSeconds*20, 0, 1.0)
	e.beeHasStung = true // setHasStung(true) -> the bee now dies over ~1200 ticks (beeAiStep)
}

// beeAiStep is the Bee per-tick extra (Bee.customServerAiStep), driven per-type from tickAI (gated on
// typ == entity.Bee.ID, AFTER serverAiStep). It ports the method 1:1 up to the sting-death branch:
//
//	boolean hasStung = hasStung();
//	if (isInWater()) ++underWaterTicks; else underWaterTicks = 0;   // UNCONDITIONAL (runs stung or not)
//	if (underWaterTicks > 20) hurtServer(drown(), 1.0F);           // an un-stung bee STILL drowns
//	if (hasStung) { ++timeSinceSting; if (timeSinceSting % 5 == 0 &&
//	    random.nextInt(Mth.clamp(1200 - timeSinceSting, 1, 1200)) == 0) hurtServer(generic(), getHealth()); }
//
// The trailing nectar / updatePersistentAnger bookkeeping (ticksWithoutNectarSinceExitingHive++,
// updatePersistentAnger) is the DEFERRED hive/anger layer (no hive block-entity / neutral-anger machinery
// yet). The sting-death RNG is on the bee OWN per-entity rng, drawn ONLY after a sting (a never-stung bee
// draws zero). Cite Bee.customServerAiStep.
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
		// hurtServer(damageSources().drown(), 1.0F) -- the un-stung bee bug fix: this ALWAYS runs.
		t.applyDamageEntity(e, damageSourceOf(damageTypeDrown), beeDrownDamage)
	}
	if !hasStung {
		return // sting-death countdown only runs once the bee has stung (no RNG draw otherwise)
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
