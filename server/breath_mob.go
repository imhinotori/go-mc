package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// breath_mob.go generalizes the air-supply / drowning / suffocation branch of
// net.minecraft.world.entity.LivingEntity.baseTick from the player-only path (breath.go +
// suffocation.go) to run for MOBS too - closing divergence-audit gap B-A6 (mobs did not drown or
// suffocate because baseTick's environmental block only ran for players). It REUSES the player
// breath constants/helpers verbatim (maxAirSupply, airRefillPerTick, drowningThreshold, drownDamage,
// decreaseAirSupply, increaseAirSupply, shouldTakeDrowningDamage from breath.go; suffocationDamage /
// isSuffocating from suffocation.go) - the SAME jar-verified numbers - and adds only the mob-side
// glue: the per-type canBreatheUnderwater guard (the EntityTypeTags.CAN_BREATHE_UNDER_WATER tag),
// the mob effect OR-chain (MobEffectUtil.hasWaterBreathing over the mob's mobEffects), the mob eye
// sampling, and the mob metadata sync.
//
// Cited bytecode (javap temp/cache/26.2-inner.jar, this session):
//
//	LivingEntity.baseTick (isAlive + ServerLevel branch):
//	  if (isInWall())                 hurtServer(inWall(), 1.0F);         // IN_WALL, fconst_1
//	  if (isEyeInFluid(WATER) && !getBlockState(eyePos).is(BUBBLE_COLUMN)) {
//	      boolean flag = !canBreatheUnderwater() && !MobEffectUtil.hasWaterBreathing(this)
//	                     && !(player && abilities.invulnerable);
//	      if (flag) {
//	          setAirSupply(decreaseAirSupply(getAirSupply()));
//	          if (shouldTakeDrowningDamage()) {                          // air <= -20
//	              setAirSupply(0); broadcastEntityEvent(67); hurtServer(drown(), 2.0F);  // DROWN, fconst_2
//	          }
//	      } else if (getAirSupply() < getMaxAirSupply()
//	                 && MobEffectUtil.shouldEffectsRefillAirsupply(this)) {
//	          setAirSupply(increaseAirSupply(getAirSupply()));
//	      }
//	  } else if (getAirSupply() < getMaxAirSupply()) {
//	      setAirSupply(increaseAirSupply(getAirSupply()));               // out of water: refill
//	  }
//
//	LivingEntity.canBreatheUnderwater: is(EntityTypeTags.CAN_BREATHE_UNDER_WATER) (tag membership).
//	MobEffectUtil.hasWaterBreathing: hasEffect(WATER_BREATHING || CONDUIT_POWER || BREATH_OF_THE_NAUTILUS).
//	Entity.getMaxAirSupply: 300; getEyeY: y + eyeHeight (default eyeHeight == height*0.85).

// mobRunsBaseTickEnv reports whether a store entity is a LivingEntity that runs baseTick's air /
// drowning / suffocation block. Vanilla runs it for every LivingEntity; the non-living Entity
// subclasses (dropped items, XP orbs, arrows, thrown potions, evoker fangs, boats, minecarts, TNT,
// falling blocks, item frames, fishing hooks, lightning bolts, display entities) are Entity, NOT
// LivingEntity, so they never decrement air or suffocate. This is the v1 stand-in for
// instanceof LivingEntity (the same role isLivingMob plays for the fang/lightning scans); kept
// local so extending it can't perturb those unrelated predicates. A vex (isVex) and an armor stand
// (isArmorStand - a LivingEntity, and in CAN_BREATHE_UNDER_WATER so it never drowns) ARE living.
func mobRunsBaseTickEnv(e *Entity) bool {
	if e == nil {
		return false
	}
	if e.isItem || e.isOrb || e.isArrow || e.isPotion || e.isFangs ||
		e.isBoat || e.isMinecart || e.isTnt || e.isFalling || e.isFrame ||
		e.isFishingHook || e.isBolt || e.isItemDisplay {
		return false
	}
	return true
}

// mobCanBreatheUnderwater is the 1:1 port of LivingEntity.canBreatheUnderwater(): membership in
// EntityTypeTags.CAN_BREATHE_UNDER_WATER (no subclass overrides - Drowned/Guardian/WaterAnimal all
// inherit this tag-driven default; javap-confirmed none override canBreatheUnderwater). The tag
// (data/minecraft/tags/entity_type/can_breathe_under_water.json, this session) is #undead plus the
// water mobs + armor_stand + copper_golem + nautilus; #undead expands to #skeletons + #zombies +
// wither + phantom. Kept as an explicit, jar-cited type set - the SAME idiom isPowderSnowWalkableMob
// uses (v1 has no runtime entity-type tag subsystem); it becomes a real tag lookup when tags land. A
// mob in this set never loses air / drowns (the flag folds to false).
func mobCanBreatheUnderwater(typ entity.ID) bool {
	switch typ {
	// #minecraft:undead -> #minecraft:skeletons
	case entity.Skeleton.ID, entity.Stray.ID, entity.WitherSkeleton.ID,
		entity.SkeletonHorse.ID, entity.Bogged.ID, entity.Parched.ID:
		return true
	// #minecraft:undead -> #minecraft:zombies
	case entity.ZombieHorse.ID, entity.CamelHusk.ID, entity.Zombie.ID,
		entity.ZombieVillager.ID, entity.ZombifiedPiglin.ID, entity.Zoglin.ID,
		entity.Drowned.ID, entity.Husk.ID, entity.ZombieNautilus.ID:
		return true
	// #minecraft:undead -> direct members
	case entity.Wither.ID, entity.Phantom.ID:
		return true
	// direct members of can_breathe_under_water
	case entity.Axolotl.ID, entity.Frog.ID, entity.Guardian.ID, entity.ElderGuardian.ID,
		entity.Turtle.ID, entity.GlowSquid.ID, entity.Cod.ID, entity.Pufferfish.ID,
		entity.Salmon.ID, entity.Squid.ID, entity.TropicalFish.ID, entity.Tadpole.ID,
		entity.ArmorStand.ID, entity.CopperGolem.ID, entity.Nautilus.ID:
		return true
	default:
		return false
	}
}

// mobHasWaterBreathing ports MobEffectUtil.hasWaterBreathing(LivingEntity) over a mob's mobEffects:
// true if the mob holds WATER_BREATHING, CONDUIT_POWER, or BREATH_OF_THE_NAUTILUS (the exact 3-effect
// OR-chain, no RNG). It is the mob twin of breath.go's playerHasWaterBreathing (same effect ids). When
// true the submerged air-loss flag folds to false, so air neither drains nor drowns. A witch that
// self-drinks a Water Breathing potion (ai_goals_witch.go seeds effectWaterBreathing into mobEffects)
// is thereby immune, exactly as vanilla.
func mobHasWaterBreathing(e *Entity) bool {
	return entityHasEffect(e, effectWaterBreathing) ||
		entityHasEffect(e, effectConduitPower) ||
		entityHasEffect(e, effectBreathOfTheNautilus)
}

// mobShouldEffectsRefillAirsupply ports MobEffectUtil.shouldEffectsRefillAirsupply(LivingEntity):
// hasEffect(BREATH_OF_THE_NAUTILUS) && !(hasEffect(WATER_BREATHING) || hasEffect(CONDUIT_POWER)) -
// the sub-max refill-while-submerged gate for a mob whose air-loss flag was folded false. No v1
// source grants BREATH_OF_THE_NAUTILUS to a mob, so this is dormant today; ported as the exact chain
// so the else-if branch is faithful. Cite MobEffectUtil.shouldEffectsRefillAirsupply.
func mobShouldEffectsRefillAirsupply(e *Entity) bool {
	if !entityHasEffect(e, effectBreathOfTheNautilus) {
		return false
	}
	return !entityHasEffect(e, effectWaterBreathing) && !entityHasEffect(e, effectConduitPower)
}

// mobEyeY ports Entity.getEyeY() == y + eyeHeight for a mob. The default LivingEntity eyeHeight is
// height*0.85 (EntityDimensions.defaultEyeHeight: ldc 0.85f); this reuses the SAME height*0.85
// anchor witchEyeInWater uses so the eye sample is consistent across the mob code. Per-type
// withEyeHeight overrides (a cited v1 gap, as in witchEyeInWater) refine this later.
func mobEyeY(e *Entity) float64 {
	return e.y + e.height*0.85
}

// mobEyeInWater is the mob port of Entity.isEyeInFluid(FluidTags.WATER): sample water at the block
// containing getEyeY(). This intentionally mirrors witchEyeInWater's coarse cell test (the fluid-
// surface-height refinement eyeInWater does for players is a later refinement; the block-cell read is
// the dominant faithful case for a submerged mob head). A nil world (test loop) is not-in-water.
func (t *TickLoop) mobEyeInWater(e *Entity) bool {
	if t.world() == nil {
		return false
	}
	eyeY := mobEyeY(e)
	bx := int(math.Floor(e.x))
	by := int(math.Floor(eyeY))
	bz := int(math.Floor(e.z))
	return t.fluidAt(pk.Position{X: bx, Y: by, Z: bz}).isWater
}

// mobIsInWall is the mob port of Entity.isInWall() (the IN_WALL suffocation predicate), the sibling
// of isInWall (suffocation.go) but eye-anchored at the mob's height*0.85. It collapses the width*0.8
// eye-box stream to the single block containing the eye (the dominant head-in-solid case; the same
// documented v1 gap the player port carries - no VoxelShape engine yet), and reads the baked
// isSuffocating table (block.IsSuffocating). A nil world / air / non-suffocating block is false.
func (t *TickLoop) mobIsInWall(e *Entity) bool {
	if t.world() == nil {
		return false
	}
	bx := int(math.Floor(e.x))
	by := int(math.Floor(mobEyeY(e)))
	bz := int(math.Floor(e.z))
	s, ok := t.world().GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY)
	if !ok {
		return false
	}
	if block.IsAir(s) {
		return false // !s.isAir() guard
	}
	return t.isSuffocating(s)
}

// tickMobBreath runs the air-supply / drowning / suffocation block of LivingEntity.baseTick for ONE
// mob - the mob twin of tickBreath (breath.go) + tickSuffocation (suffocation.go). Called from the
// tickAI per-entity byID loop (tick_phases.go), the SAME OUTSIDE-serverAiStep environmental phase as
// tickMobIFrames / tickEntityFire / tickEntityLava. Self-gated on mobRunsBaseTickEnv (LivingEntity
// only) and !dead (baseTick's isAlive() guard), so a non-living entity (item/orb/arrow) and a corpse
// are no-ops.
//
// ORDER matches baseTick: suffocation (isInWall) FIRST, then the water air block. Both route damage
// through applyDamageEntity (Sulfur's hurtServer port) so IN_WALL/DROWN obey i-frames exactly as
// vanilla. decreaseAirSupply draws NO RNG for a mob (OXYGEN_BONUS attribute base 0 - no Respiration
// source in v1 - so the nextDouble() >= 1/(d+1) skip is unreachable; it falls to air-1), so this
// cannot perturb any mob's RNG stream. broadcastEntityEvent(67) (the drowning-particle client event)
// is omitted for parity with the player port (breath.go), which likewise defers the cosmetic 67 event
// - a cited deferral; the authoritative air/damage stream is unaffected.
func (t *TickLoop) tickMobBreath(e *Entity) {
	if !mobRunsBaseTickEnv(e) || e.dead {
		return
	}

	// IN_WALL suffocation (baseTick: if (isInWall()) hurtServer(inWall(), 1.0F)), FIRST.
	if t.mobIsInWall(e) {
		t.applyDamageEntity(e, damageSourceOf(damageTypeInWall), suffocationDamage)
		if e.health <= 0 { // a suffocation kill removes/kills the mob - do not then run the water block
			return
		}
	}

	// The water air-supply block. The BUBBLE_COLUMN guard is a faithful no-op in v1 (no bubble
	// columns), so it is omitted exactly as breath.go omits it for players.
	if t.mobEyeInWater(e) {
		// flag = !canBreatheUnderwater() && !hasWaterBreathing() (the player invulnerable sub-term is
		// N/A for a mob). A CAN_BREATHE_UNDER_WATER mob (undead / water mobs / armor_stand / copper_golem
		// / nautilus) or an effect-shielded mob folds flag to false and never drains.
		flag := !mobCanBreatheUnderwater(e.typ) && !mobHasWaterBreathing(e)
		if flag {
			e.airSupply = decreaseAirSupply(e.airSupply)
			if shouldTakeDrowningDamage(e.airSupply) { // air <= -20
				e.airSupply = 0
				t.applyDamageEntity(e, damageSourceOf(damageTypeDrown), drownDamage)
			}
		} else if e.airSupply < maxAirSupply && mobShouldEffectsRefillAirsupply(e) {
			// Refill-while-submerged: only under BREATH_OF_THE_NAUTILUS without WATER_BREATHING /
			// CONDUIT_POWER (dormant in v1; ported for faithfulness).
			e.airSupply = increaseAirSupply(e.airSupply)
		}
	} else if e.airSupply < maxAirSupply {
		// Out of water: refill toward max (min(air+4, 300)).
		e.airSupply = increaseAirSupply(e.airSupply)
	}

	t.syncMobAirSupply(e)
}

// syncMobAirSupply pushes the mob's DATA_AIR_SUPPLY_ID to its tracking observers when it changed
// since the last send (vanilla SynchedEntityData dirty-only semantics). Unlike the player twin
// (syncAirSupply) there is NO self-send - a mob has no own client; the ONLY targets are the players
// tracking this mob's entity, reached via broadcastToTrackers (the same tracker fan-out the other mob
// metadata pushes use, e.g. the baby/cat/wolf flags). A no-change tick sends nothing (the common
// full-air-out-of-water case), so this adds no wire spam. Tick-owned (TICK-05).
func (t *TickLoop) syncMobAirSupply(e *Entity) {
	if e.airSupply == e.lastAirSent {
		return
	}
	e.lastAirSent = e.airSupply
	t.broadcastToTrackers(e.id, encodeSetEntityDataByID(e.id, airDataEntry(e.airSupply)))
}
