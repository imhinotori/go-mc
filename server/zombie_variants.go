package server

// zombie_variants.go -- the SIGNATURE (Go-native) behavior for the 4 zombie/skeleton variants ported 1:1
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   - net.minecraft.world.entity.monster.zombie.Drowned      (extends Zombie, implements RangedAttackMob)
//   - net.minecraft.world.entity.monster.skeleton.Stray      (extends AbstractSkeleton)
//   - net.minecraft.world.entity.monster.skeleton.Bogged     (extends AbstractSkeleton, Shearable)
//   - net.minecraft.world.entity.monster.zombie.ZombieVillager (extends Zombie)
//
// The base HOSTILE AI + attributes are declared by the Starlark plugins (assets/vanilla_<variant>/main.star,
// base_type routing) -- verbatim Zombie/Skeleton goal sets. This file carries ONLY the per-type signature the
// jar overrides add:
//
//   - Drowned:        DrownedTridentAttackGoal + performRangedAttack (throw a ThrownTrident when holding a
//                     TRIDENT). Water-nav goals are DEFERRED (no fluid subsystem); Drowned still burns in
//                     daylight (Zombie.isSunSensitive == true, no Drowned override).
//   - Stray:          getArrow -> the fired Arrow carries SLOWNESS 600 (amp 0). (Applied via arrowEffects.)
//   - Bogged:         getArrow -> the fired Arrow carries POISON 100 (amp 0); MAX_HEALTH 16 (plugin attr);
//                     Shearable -> shear drops a RED_MUSHROOM (the shearing/bogged loot table is a cited stub).
//   - ZombieVillager: the CURE (right-click a GOLDEN_APPLE while it has WEAKNESS -> startConverting ->
//                     conversionTime countdown -> finishConversion to a Villager) and the villager infection
//                     helper (Zombie.killedEntity NORMAL/HARD -> convertVillagerToZombieVillager).
//
// THE PIG ORACLE IS UNTOUCHED: every hook is per-type-gated (isDrowned/isStray/isBogged/isZombieVillager or
// typ==), so a pig (a DIFFERENT mob) never reaches any of this code -- no new draw touches the pinned pig stream.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// --- constants (jar-confirmed) ------------------------------------------------------------------

const (
	// Stray.getArrow: arrow.addEffect(new MobEffectInstance(SLOWNESS, 600)). 600 ticks == 30s, amp 0.
	strayArrowSlownessDuration  = 600
	strayArrowSlownessAmplifier = 0
	// Bogged.getArrow: arrow.addEffect(new MobEffectInstance(POISON, 100)). 100 ticks == 5s, amp 0.
	boggedArrowPoisonDuration  = 100
	boggedArrowPoisonAmplifier = 0

	// DrownedTridentAttackGoal(this, 1.0, 40, 10.0f): attackInterval 40, attackRadius 10 (radiusSqr 100).
	drownedTridentAttackInterval = 40
	drownedTridentAttackRadius   = 10.0
	drownedTridentAttackRadiusSq = drownedTridentAttackRadius * drownedTridentAttackRadius
	// DrownedTridentAttackGoal(this, 1.0, 40, 10.0f): speedModifier 1.0 (the RangedAttackGoal chase speed).
	drownedTridentSpeedModifier = 1.0
	// Drowned.performRangedAttack: shoot(..., 1.6f, 14 - difficulty*4). velocity 1.6, inaccuracy 14-diff*4.
	drownedTridentVelocity = 1.6

	// ZombieVillager.startConverting: villagerConversionTime = random.nextInt(2401) + 3600 (3600..6000).
	zvConversionBaseTime = 3600
	zvConversionRandCap  = 2401
	// getConversionProgress(): base 1 per tick (the bed/iron-bars acceleration is a cited stub, see below).
	zvConversionProgressBase = 1

	// Zombie.getSpawnAsBabyOdds: return random.nextFloat() < 0.05F. A freshly-spawned zombie is a baby 5%
	// of the time. Cite Zombie.getSpawnAsBabyOdds.
	zombieSpawnAsBabyChance = float32(0.05)
	// Zombie.finalizeSpawn chicken-jockey rolls: both the ride-existing-chicken branch and the
	// spawn-a-new-chicken branch gate on `(double) random.nextFloat() < 0.05D` (ldc2_w 0.05d). Cite
	// Zombie.finalizeSpawn (the two CHICKEN_JOCKEY nextFloat gates).
	zombieChickenJockeyChance = 0.05

	// Drowned.finalizeSpawn nautilus-shell offhand roll: `if getItemBySlot(OFFHAND).isEmpty() &&
	// random.nextFloat() < 0.03F` -> OFFHAND = NAUTILUS_SHELL + setGuaranteedDrop(OFFHAND). Cite
	// Drowned.finalizeSpawn (NAUTILUS_SHELL_CHANCE == 0.03f).
	drownedNautilusShellChance = float32(0.03)

	// Zombie.hurtServer reinforcement offsets: `Mth.nextInt(random, 7, 40) * Mth.nextInt(random, -1, 1)`
	// per axis (x,y,z). Mth.nextInt(r,lo,hi) == lo>=hi ? lo : r.nextInt(hi-lo+1)+lo. Cite Zombie.hurtServer.
	zombieReinforceOffsetMin = 7
	zombieReinforceOffsetMax = 40
	// The reinforcement placement loop runs at most 50 iterations (i < 50). Cite Zombie.hurtServer.
	zombieReinforceMaxTries = 50
	// The caller/callee SPAWN_REINFORCEMENTS_CHANCE charge: addPermanentModifier(amount - 0.05) on the
	// parent, and a flat -0.05 ADD_VALUE on the child. Cite Zombie.hurtServer (REINFORCEMENT_CALLER_CHARGE_ID
	// / ZOMBIE_REINFORCEMENT_CALLEE_CHARGE, both amount -0.05).
	zombieReinforceCharge = -0.05
	// Zombie.startUnderWaterConversion(300): the drowning countdown starts at 300 ticks. Cite Zombie.tick.
	zombieUnderWaterConvertTime = 300
	// Zombie.tick: inWaterTime >= 600 triggers startUnderWaterConversion. Cite Zombie.tick (ldc 600).
	zombieInWaterConvertThreshold = 600
)

// zombieReinforceCallerChargeID is Zombie.REINFORCEMENT_CALLER_CHARGE_ID
// (Identifier.withDefaultNamespace("reinforcement_caller_charge")) -- the id of the parent's
// SPAWN_REINFORCEMENTS_CHANCE -0.05 charge (removed+re-added each successful reinforcement so it stacks
// down). Cite Zombie.hurtServer.
const zombieReinforceCallerChargeID = "minecraft:reinforcement_caller_charge"

// zombieReinforceCalleeChargeID is the id of the ZOMBIE_REINFORCEMENT_CALLEE_CHARGE static modifier the
// child reinforcement receives (a flat -0.05 ADD_VALUE on SPAWN_REINFORCEMENTS_CHANCE, so a reinforcement
// is less likely to summon further reinforcements). Cite Zombie.hurtServer (ZOMBIE_REINFORCEMENT_CALLEE_CHARGE).
const zombieReinforceCalleeChargeID = "minecraft:reinforcement_callee_charge"

// --- Stray / Bogged: the tipped-arrow tag ------------------------------------------------------

// variantArrowEffects returns the tipped-arrow MobEffectInstances the shooting mob's getArrow override adds,
// or nil for a plain skeleton/other shooter. Read by performRangedAttack (ai_goals_ranged.go) at arrow
// spawn. RNG-free. Cite Stray.getArrow (SLOWNESS 600) + Bogged.getArrow (POISON 100).
func variantArrowEffects(e *Entity) []splashEffect {
	switch {
	case e.isStray:
		// Stray.getArrow: (Arrow) super.getArrow(); arrow.addEffect(new MobEffectInstance(SLOWNESS, 600)).
		return []splashEffect{{id: effectSlowness, duration: strayArrowSlownessDuration, amplifier: strayArrowSlownessAmplifier}}
	case e.isBogged:
		// Bogged.getArrow: (Arrow) super.getArrow(); arrow.addEffect(new MobEffectInstance(POISON, 100)).
		return []splashEffect{{id: effectPoison, duration: boggedArrowPoisonDuration, amplifier: boggedArrowPoisonAmplifier}}
	}
	return nil
}

// --- Bogged: Shearable -------------------------------------------------------------------------

// boggedReadyForShearing ports Bogged.readyForShearing(): !isSheared(). A live, un-sheared bogged can be
// sheared. Cite Bogged.readyForShearing.
func boggedReadyForShearing(e *Entity) bool { return !e.boggedSheared }

// tryBoggedShear ports the shears-on-Bogged interact (Bogged implements Shearable; the item-side
// ShearsItem.interactLivingEntity -> Bogged.shear). Returns TRUE when the held item is SHEARS (the interact
// is consumed regardless of outcome), FALSE otherwise (fall through). On a ready shear it drops the sheared
// mushrooms and sets sheared. The held item is read SERVER-SIDE (never the payload). Cite Bogged.shear ->
// spawnShearedMushrooms + setSheared(true).
func (t *TickLoop) tryBoggedShear(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.Shears.ID) {
		return false // not shears -> no bogged interact
	}
	if !boggedReadyForShearing(mob) {
		return true // !readyForShearing() -> CONSUME (no shear)
	}
	t.spawnShearedMushrooms(mob) // Bogged.spawnShearedMushrooms
	mob.boggedSheared = true     // setSheared(true)
	t.hurtHeldItem(p, inv, 1)    // itemStack.hurtAndBreak(1, player, hand)
	return true
}

// spawnShearedMushrooms ports Bogged.spawnShearedMushrooms: dropFromShearingLootTable(BOGGED_SHEAR, ...)
// spawns the shearing/bogged drop. There is no loot-table subsystem for shearing/bogged in v1, so the drop
// is a CITED RED_MUSHROOM x1 stub (the vanilla shearing/bogged table drops a single red_mushroom), spawned
// via the shared ItemEntity toss. Structured so a real loot.Roll replaces it later. Cite
// Bogged.spawnShearedMushrooms -> dropFromShearingLootTable(BuiltInLootTables.BOGGED_SHEAR).
func (t *TickLoop) spawnShearedMushrooms(e *Entity) {
	drop := itemStackOf(item.RedMushroom) // new ItemStack(RED_MUSHROOM) (count 1)
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, drop)
	t.regionForEntity(e).entities.add(ie)
}

// --- Drowned: the trident throw ----------------------------------------------------------------

// drownedHoldsTrident ports Drowned.performRangedAttack's weapon check + DrownedTridentAttackGoal.canUse's
// getMainHandItem().is(Items.TRIDENT). A drowned throws its trident ONLY while holding one. Cite
// DrownedTridentAttackGoal.canUse.
func drownedHoldsTrident(e *Entity) bool {
	return e.isHoldingItem(int32(item.Trident.ID))
}

// initDrownedTridentEquip ports Drowned.populateDefaultEquipmentSlots: if random.nextFloat() > 0.9, roll
// nextInt(16); < 10 -> MAINHAND = TRIDENT, else MAINHAND = FISHING_ROD. Runs at spawn after the shared
// populateMonsterEquipment (which already applied the Zombie armor/iron-tool roll). Drowned-gated, so no
// other mob draws here (the pig oracle is a different mob -- unperturbed). Cite
// Drowned.populateDefaultEquipmentSlots.
func initDrownedTridentEquip(e *Entity, rng *entityRandom) {
	if rng.nextFloat() > 0.9 {
		if rng.nextInt(16) < 10 {
			e.setItemSlot(eqSlotMainHand, itemStackOf(item.Trident)) // new ItemStack(TRIDENT)
		} else {
			e.setItemSlot(eqSlotMainHand, itemStackOf(item.FishingRod)) // new ItemStack(FISHING_ROD)
		}
	}
}

// drownedAiStep is the Drowned per-tick signature drive: acquire the nearest player (reuse the shared
// target read), then -- when the drowned holds a TRIDENT and the target is in trident range with line-of-
// sight -- run the RangedAttackGoal cadence and THROW the trident (performRangedAttack). The base melee/
// stroll/look goals are the plugin declaration; this only adds the trident. Gated in tickAI on isDrowned,
// AFTER serverAiStep. RNG only on the drowned OWN stream (the throw spread), drawn only on a throw. Cite
// Drowned.registerGoals (DrownedTridentAttackGoal) + Drowned.performRangedAttack.
func (t *TickLoop) drownedAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	// DrownedTridentAttackGoal.canUse == RangedAttackGoal.canUse (target != null && alive) && getMainHandItem
	// ().is(TRIDENT). When it is false the goal is STOPPED -> RangedAttackGoal.stop: seeTime=0, attackTime=-1.
	if !drownedHoldsTrident(e) {
		e.drownedTridentSeeTime = 0
		e.drownedTridentTime = -1 // stop(): attackTime = -1
		return
	}
	target := t.variantTarget(e)
	if target == nil {
		e.drownedTridentSeeTime = 0
		e.drownedTridentTime = -1 // stop(): attackTime = -1
		return
	}

	// RangedAttackGoal.tick (VERIFIED javap net.minecraft.world.entity.ai.goal.RangedAttackGoal.tick), ported
	// 1:1. This is the SAME cadence the witch's rangedAttackGoal.tick runs -- the base RangedAttackGoal shared
	// by every RangedAttackMob. DrownedTridentAttackGoal does NOT override tick (only canUse/start/stop), so it
	// inherits this verbatim. The v1 hook drives it per-tick for a drowned holding a trident with a target.
	distSq := distanceToSqrPlayer(target, e)
	hasLineOfSight := t.sensingHasLineOfSight(e, target) // Sensing.hasLineOfSight (the cached raycast, C-4)
	// if hasLineOfSight: seeTime++ else seeTime = 0 (the BASE RangedAttackGoal RESETS to 0 -- it does NOT
	// decrement like RangedBowAttackGoal). VERIFIED javap offsets 44-63.
	if hasLineOfSight {
		e.drownedTridentSeeTime++
	} else {
		e.drownedTridentSeeTime = 0
	}
	// Chase while out of trident range OR not-yet-locked-on (seeTime < 5), else stop the nav. moveTo speed is
	// getAttributeValue(MOVEMENT_SPEED) * speedModifier (1.0). VERIFIED javap offsets 66-115. Both the melee
	// goal and this ranged goal share priority @2 in the jar (arbitrated) and both aim at the SAME target, so
	// re-issuing the want here is consistent with the drowned's melee chase.
	if e.ai != nil {
		if distSq > drownedTridentAttackRadiusSq || e.drownedTridentSeeTime < 5 {
			getSpeed := e.getAttributeValue(attribute.MovementSpeed) * drownedTridentSpeedModifier
			e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed) // navigation.moveTo(target, speed)
		} else {
			e.ai.clearWantTarget() // navigation.stop()
		}
	}
	// lookAt(target, 30, 30): head-only turn (the LOOK flag). VERIFIED javap offsets 116-131.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, meleeLookMaxYawStep)

	// attackTime = --attackTime; if it hits 0 with line-of-sight, THROW (performRangedAttack) and re-arm the
	// cooldown to floor(dist*(max-min)+min); if it drops below 0 (a fresh acquire from the -1 sentinel) arm it
	// to floor(lerp(sqrt(distSqr)/radius, min, max)). For the drowned min==max==40, so both branches give 40.
	// The fire does NOT re-check range -- only hasLineOfSight (VERIFIED javap offsets 134-256). This is the
	// fix: the old code reset attackTime to 0 when out of range/LoS and fired on the first eligible tick.
	e.drownedTridentTime--
	if e.drownedTridentTime == 0 {
		if !hasLineOfSight { // offset 148: iload_3 ifne -> return
			return
		}
		distF := math.Sqrt(distSq) / drownedTridentAttackRadius
		power := distF   // Mth.clamp(distF, 0.1f, 1.0f) -- Drowned.performRangedAttack ignores power, but the
		if power < 0.1 { // cadence still computes + clamps it (the arm formula reuses distF, not the clamp).
			power = 0.1
		} else if power > 1.0 {
			power = 1.0
		}
		_ = power // Drowned.performRangedAttack(target, power) ignores power (fixed trident throw); kept for cadence fidelity.
		t.drownedPerformRangedAttack(e, target)
		// attackTime = Mth.floor(distF*(max-min)+min) == 40 (max==min==40). VERIFIED javap offsets 190-213.
		e.drownedTridentTime = int(math.Floor(distF*float64(drownedTridentAttackInterval-drownedTridentAttackInterval) + float64(drownedTridentAttackInterval)))
	} else if e.drownedTridentTime < 0 {
		// attackTime = Mth.floor(Mth.lerp(sqrt(distSqr)/radius, min, max)) == 40. VERIFIED javap offsets 219-253.
		distF := math.Sqrt(distSq) / drownedTridentAttackRadius
		e.drownedTridentTime = int(math.Floor(mthLerpD(distF, float64(drownedTridentAttackInterval), float64(drownedTridentAttackInterval))))
	}
}

// drownedPerformRangedAttack ports Drowned.performRangedAttack(target, power): build a ThrownTrident aimed
// at target.getY(0.3333) with a ballistic lob (yd + dist*0.2), launched at velocity 1.6 with inaccuracy
// 14 - difficulty*4. Reuses the shared spawnThrownTrident (a ThrownTrident is an AbstractArrow subclass that
// flies via tickArrow + deals a flat 8 on hit). The thrown stack is the drowned's mainhand trident. Cite
// Drowned.performRangedAttack + Projectile.spawnProjectileUsingShoot.
func (t *TickLoop) drownedPerformRangedAttack(e *Entity, target *tickPlayer) {
	r := mobRandom(e)
	t.broadcastMobSwing(e) // startUsingItem/aggressive client cue (the swing)

	launchY := e.y + e.eyeHeightForArrow()
	xd := target.x - e.x
	yd := (target.y + float64(playerHeight)*0.3333333333333333) - launchY // target.getY(0.3333) - trident.getY()
	zd := target.z - e.z
	dist := math.Sqrt(xd*xd + zd*zd)
	ydLob := yd + dist*0.2 // + dist*0.20000000298023224

	diff := float64(serverDifficulty) // NORMAL == 2 (cited stub)
	inaccuracy := 14.0 - diff*4.0     // 14 - difficulty.getId()*4 (NORMAL == 6)

	// getMovementToShoot (shared with the bow path): normalize(dir) + triangle noise*(0.0172275*inacc), *1.6.
	vx, vy, vz := normalizeVec3(xd, ydLob, zd)
	spread := 0.0172275 * inaccuracy
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	vx *= drownedTridentVelocity
	vy *= drownedTridentVelocity
	vz *= drownedTridentVelocity

	stack := e.getMainHandItem() // the thrown trident stack (its enchants ride into spawnThrownTrident)
	t.spawnThrownTrident(e.id, e.x, launchY, e.z, vx, vy, vz, stack, false)
}

// variantTarget reads the current attack-target player (Mob.getTarget()) for a variant that runs a code-
// driven signature step, or nil. Mirrors witherSkeletonTarget/blazeTarget. NO RNG.
func (t *TickLoop) variantTarget(e *Entity) *tickPlayer {
	id := mobTarget(e)
	if id == 0 {
		return nil
	}
	p := t.playerByEntityID(id)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// --- ZombieVillager: the cure + conversion + the infection helper ------------------------------

// tryZombieVillagerCure ports ZombieVillager.mobInteract: if the held item is a GOLDEN_APPLE AND the zombie
// villager currently has WEAKNESS, consume 1 apple and startConverting(random.nextInt(2401)+3600); return
// true (SUCCESS_SERVER). A golden apple WITHOUT weakness returns true (CONSUME, no conversion). A non-golden
// -apple item returns false (fall through to the base zombie interact). The held item is read SERVER-SIDE.
// Cite ZombieVillager.mobInteract.
func (t *TickLoop) tryZombieVillagerCure(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	slot := heldWindowSlot(inv.heldSlot)
	held := inv.get(slot)
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.GoldenApple.ID) {
		return false // not a golden apple -> super.mobInteract (the base zombie interact)
	}
	// itemStack.is(GOLDEN_APPLE) is TRUE from here on -> the interact is consumed regardless of outcome.
	if !entityHasEffect(mob, effectWeakness) {
		return true // golden apple but no WEAKNESS -> CONSUME (no conversion)
	}
	// stack.consume(1, player): shrink the held golden apple by 1.
	t.consumeHeldOne(p, inv, slot)
	// startConverting(player.getUUID(), random.nextInt(2401) + 3600). The starter is the curer's entity id.
	t.zombieVillagerStartConverting(mob, p.entityID, mobRandom(mob).nextInt(zvConversionRandCap)+zvConversionBaseTime)
	return true
}

// consumeHeldOne shrinks the held stack in the given slot by 1 (ItemStack.consume(1, player)), writing back
// (empty when the last leaves) and broadcasting the slot -- the same shrink the arrow/food paths use.
func (t *TickLoop) consumeHeldOne(p *tickPlayer, inv *Inventory, slot int16) {
	before := inv.snapshot()
	s := inv.get(slot)
	s.Count--
	if s.Count <= 0 {
		s = component.SlotData{Count: 0}
	}
	inv.set(slot, s)
	t.broadcastInventoryChanges(p, inv, before)
}

// zombieVillagerStartConverting ports ZombieVillager.startConverting(UUID, int): record the starter + the
// conversion time, flip DATA_CONVERTING_ID true, remove WEAKNESS, and add STRENGTH for the whole conversion
// duration at amplifier min(difficulty.getId()-1, 0). Cite ZombieVillager.startConverting.
func (t *TickLoop) zombieVillagerStartConverting(e *Entity, starterID int32, time int) {
	e.zvConversionStarter = starterID
	e.zvConversionTime = time
	e.zvConverting = true // entityData DATA_CONVERTING_ID true
	// removeEffect(WEAKNESS).
	if e.mobEffects != nil {
		if _, ok := e.mobEffects[effectWeakness]; ok {
			t.removeEntityEffectModifiers(e, effectWeakness)
			delete(e.mobEffects, effectWeakness)
		}
	}
	// addEffect(new MobEffectInstance(STRENGTH, time, min(difficulty.getId()-1, 0))). NORMAL id 2 -> min(1,0)==0.
	amp := int(serverDifficulty) - 1
	if amp > 0 {
		amp = 0 // Math.min(id-1, 0)
	}
	t.addEntityEffectWithSource(e, starterID, effectStrength, time, amp, 1.0)
}

// zombieVillagerConversionProgress ports ZombieVillager.getConversionProgress(): base 1 per tick. The
// bed/iron-bars proximity acceleration (a 0.01-probability nearby-block scan that adds a bonus) is a CITED
// stub -- no block-scan-around-entity subsystem in v1, so the base rate (1/tick) is used, giving the vanilla
// 3600..6000-tick (3..5 min) cure with no nearby beds/bars. Structured so the scan slots in later. Cite
// ZombieVillager.getConversionProgress.
func zombieVillagerConversionProgress(e *Entity) int {
	return zvConversionProgressBase
}

// zombieVillagerAiStep ports the ZombieVillager.tick conversion block: while converting, subtract the
// per-tick getConversionProgress() from the timer and, once it reaches 0, finishConversion to a Villager.
// Gated in tickAI on isZombieVillager, AFTER serverAiStep. RNG-free here (the cure roll happened at
// startConverting). Cite ZombieVillager.tick + finishConversion.
func (t *TickLoop) zombieVillagerAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if !e.zvConverting {
		return
	}
	e.zvConversionTime -= zombieVillagerConversionProgress(e)
	if e.zvConversionTime <= 0 {
		t.zombieVillagerFinishConversion(e)
	}
}

// zombieVillagerFinishConversion ports ZombieVillager.finishConversion(ServerLevel): convertTo(VILLAGER,
// ...). v1 mutates the entity type in place (the store keeps the same id/pos), the minimal conversion the
// bounded port needs -- the resulting Villager is a REAL, functioning mob: the type flag flips to villager
// (so villagerMobInteract + the villager brain drive it), the ZombieVillager marks clear, and the villager
// brain is attached (Villager.makeBrain, the same seam spawnDeclaredMob uses). The villager profession/data
// carry-over + the cure sound/particles are cited follow-ups. Cite ZombieVillager.finishConversion.
func (t *TickLoop) zombieVillagerFinishConversion(e *Entity) {
	e.typ = entity.Villager.ID
	e.isZombieVillager = false
	e.zvConverting = false
	e.zvConversionTime = 0
	e.zvConversionStarter = 0
	if e.ai == nil {
		e.ai = &mobAI{}
		reseedMobAI(e.ai, e.id)
	}
	attachVillagerBrain(e) // Villager.makeBrain (via BRAIN_PROVIDER) -- the cured villager gets a real brain
}

// convertVillagerToZombieVillager ports Zombie.convertVillagerToZombieVillager(ServerLevel, Villager):
// villager.convertTo(ZOMBIE_VILLAGER, ...). v1 mutates the villager entity type in place to a ZombieVillager
// (the same in-place convert the finishConversion uses, inverted): the type flips to zombie_villager, the
// isZombieVillager mark is set, and the entity carries its id/pos/health. Returns true (the resulting
// zombie villager is non-null). The killedEntity difficulty/chance gate is the CALLER's. Cite
// Zombie.convertVillagerToZombieVillager.
func (t *TickLoop) convertVillagerToZombieVillager(v *Entity) bool {
	if v == nil {
		return false
	}
	v.typ = entity.ZombieVillager.ID
	v.isZombieVillager = true
	v.zvConverting = false
	v.zvConversionTime = 0
	v.zvConversionStarter = 0
	if v.ai == nil {
		v.ai = &mobAI{}
		reseedMobAI(v.ai, v.id)
	}
	// Re-seed the hostile attributes: convertTo builds a fresh ZombieVillager (Zombie.createAttributes).
	if v.attributes != nil {
		if inst := v.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			inst.SetBaseValue(3.0) // Zombie ATTACK_DAMAGE 3.0
		}
	}
	return true
}

// zombieKilledVillagerInfection ports the Zombie.killedEntity conversion gate: on NORMAL a nextBoolean()
// coin-flip (HARD always) decides whether a killed Villager is converted to a ZombieVillager (else it dies
// normally); EASY/PEACEFUL never convert. Returns true when the infection ran (the kill produced a zombie
// villager instead of a corpse). The MOB-vs-Villager KILL path (a zombie damaging a villager to death) is
// itself a cited v1 deferral (no zombie->villager target in v1), so this is the ready helper that path calls
// when villager-targeting lands. Cite Zombie.killedEntity + convertVillagerToZombieVillager.
func (t *TickLoop) zombieKilledVillagerInfection(zombie, villager *Entity) bool {
	if serverDifficulty == difficultyEasy {
		return false // EASY (and PEACEFUL) never convert
	}
	// NORMAL: if (difficulty != HARD && !random.nextBoolean()) return -> a 50% skip on NORMAL; HARD always.
	if serverDifficulty != difficultyHard && !mobRandom(zombie).nextBoolean() {
		return false
	}
	return t.convertVillagerToZombieVillager(villager)
}

// --- Zombie.finalizeSpawn: baby odds + chicken jockey ------------------------------------------

// zombieFinalizeSpawnBabyAndJockey ports the ZombieGroupData baby + CHICKEN_JOCKEY slice of
// Zombie.finalizeSpawn (VERIFIED javap Zombie.finalizeSpawn @58-296). On a fresh spawn (no inherited
// SpawnGroupData) vanilla builds `new ZombieGroupData(getSpawnAsBabyOdds(random), true)` then, if
// isBaby, setBaby(true) and -- when canSpawnJockey is true (it is, for a fresh group) -- rolls the
// chicken jockey:
//
//	if (getSpawnAsBabyOdds(random)) {                      // nextFloat() < 0.05F  (DRAW: baby odds)
//	    setBaby(true);
//	    if (canSpawnJockey) {                              // true for a fresh ZombieGroupData
//	        if ((double) random.nextFloat() < 0.05D) {     // DRAW: ride-existing-chicken gate
//	            <ride an existing nearby Chicken>           //   (no new RNG on the ride branch)
//	        } else if ((double) random.nextFloat() < 0.05D) { // DRAW: spawn-a-new-chicken gate
//	            <create+finalizeSpawn+ride a new Chicken>   //   (the new Chicken's own finalizeSpawn draws)
//	        }
//	    }
//	}
//
// THE DRAW ORDER IS LOAD-BEARING: getSpawnAsBabyOdds ALWAYS draws one nextFloat; only when it is a baby
// does the first jockey gate draw; only when that first gate FAILS does the second gate draw. This
// helper reproduces that exact conditional draw count on the mob's OWN stream. It runs BEFORE the
// equipment populate in the spawn seam (matching vanilla, where setBaby precedes
// populateDefaultEquipmentSlots) so a baby zombie's later baby-metadata carry + reduced dims are set.
//
// setBaby(true) is `breedAge = BABY_START_AGE` (the AgeableMob baby machine). The chicken JOCKEY MOUNT
// (startRiding) is a CITED deferral -- v1 has no passenger/vehicle subsystem (chicken_aistep.go's
// chickenIsChickenJockey is a const-false stub), so the ridden chicken is spawned as a REAL live mob
// via the shared spawnDeclaredMob path (its own finalizeSpawn draws land there) but the mount itself
// slots in when the passenger seam exists. The RNG DRAWS -- the observable spawn-stream contract -- are
// preserved exactly. Zombie-family-gated by the caller (typ == Zombie/Husk/Drowned/ZombieVillager); a
// pig (a different mob) never reaches this, so the pinned pig oracle stream is untouched.
//
// Cite Zombie.finalizeSpawn (ZombieGroupData baby + CHICKEN_JOCKEY 0.05 gates) + Zombie.getSpawnAsBabyOdds.
func (t *TickLoop) zombieFinalizeSpawnBabyAndJockey(e *Entity, rng *entityRandom) {
	// getSpawnAsBabyOdds(random): nextFloat() < 0.05F -> baby. ALWAYS one draw.
	if !(rng.nextFloat() < zombieSpawnAsBabyChance) {
		return
	}
	// setBaby(true): breedAge = BABY_START_AGE (the half-scale hitbox + wire baby flag carry off breedAge<0,
	// spliced by the spawn seam's baby-metadata block which runs after this).
	e.breedAge = babyStartAge
	// canSpawnJockey is TRUE for a fresh ZombieGroupData (the `true` ctor arg) -> the jockey rolls.
	// First gate: ride an existing nearby chicken. `(double) random.nextFloat() < 0.05D`.
	if float64(rng.nextFloat()) < zombieChickenJockeyChance {
		// getEntitiesOfClass(Chicken, inflate(5,3,5), ENTITY_NOT_BEING_RIDDEN): find a nearby un-ridden
		// chicken and mount it (setChickenJockey(true); startRiding). The nearby-chicken scan + the mount
		// are a CITED deferral (no passenger subsystem in v1); the branch consumes NO further RNG in vanilla
		// (the list build + get(0) + startRiding are RNG-free), so the stream is faithful with the mount deferred.
		return
	}
	// Second gate (only reached when the first FAILED): spawn a NEW chicken jockey.
	// `else if ((double) random.nextFloat() < 0.05D)`.
	if float64(rng.nextFloat()) < zombieChickenJockeyChance {
		// CHICKEN.create(level, JOCKEY); snapTo(getX,getY,getZ,getYRot,0); chicken.finalizeSpawn(...JOCKEY);
		// setChickenJockey(true); startRiding(chicken); addFreshEntity(chicken). v1: spawn the chicken as a
		// real declared mob at the zombie's position (its own finalizeSpawn draws land in spawnDeclaredMob);
		// the setChickenJockey mark + the mount are the CITED passenger-subsystem deferral.
		if t.mobRegistry != nil {
			if decl := t.mobRegistry.declByBaseType(entity.Chicken.ID); decl != nil {
				t.spawnDeclaredMob(decl, e.x, e.y, e.z)
			}
		}
	}
}

// --- Drowned.finalizeSpawn: nautilus-shell offhand roll ----------------------------------------

// drownedNautilusOffhandRoll ports the NAUTILUS_SHELL slice of Drowned.finalizeSpawn (VERIFIED javap
// Drowned.finalizeSpawn @11-64), which runs AFTER super.finalizeSpawn (Zombie.finalizeSpawn, incl. the
// baby/jockey + equipment above):
//
//	if (getItemBySlot(OFFHAND).isEmpty() && random.nextFloat() < 0.03F) {  // NAUTILUS_SHELL_CHANCE
//	    setItemSlot(OFFHAND, new ItemStack(NAUTILUS_SHELL));
//	    setGuaranteedDrop(OFFHAND);
//	}
//
// The nextFloat() gate is drawn ONLY when the offhand is empty (Java `&&` short-circuit); a drowned that
// already holds something offhand takes no draw. On success the shell goes into OFFHAND with a guaranteed
// on-death drop (setGuaranteedDrop -> dropChance 2.0). v1 has no per-slot dropChances store, so
// setGuaranteedDrop is a CITED no-op (the drop-chance override slot); the item placement + the RNG gate
// are the observable contract preserved. Drowned-gated by the caller; a pig never reaches it.
//
// NOTE (26.2 jar): the OLD audit placed this roll inside Drowned.populateDefaultEquipmentSlots -- that is
// WRONG for 26.2. Drowned.populateDefaultEquipmentSlots is ONLY the trident/fishing-rod roll (no super,
// no shell -- see initDrownedTridentEquip). The nautilus-shell roll lives here in Drowned.finalizeSpawn,
// after super. Cite Drowned.finalizeSpawn (NAUTILUS_SHELL_CHANCE 0.03f) + setGuaranteedDrop(OFFHAND).
func drownedNautilusOffhandRoll(e *Entity, rng *entityRandom) {
	// getItemBySlot(OFFHAND).isEmpty() && nextFloat() < 0.03F. The nextFloat is short-circuited by the
	// empty check exactly as the jar's `&&`.
	if e.getItemBySlot(eqSlotOffHand).Count > 0 {
		return // offhand non-empty -> no draw (Java && short-circuit)
	}
	if rng.nextFloat() < drownedNautilusShellChance {
		e.setItemSlot(eqSlotOffHand, itemStackOf(item.NautilusShell)) // new ItemStack(NAUTILUS_SHELL)
		// setGuaranteedDrop(OFFHAND): dropChance 2.0 (always drops). CITED no-op: no per-slot dropChances
		// store in v1 -- the shell is placed; the guaranteed-drop mark slots in when dropChances land.
	}
}

// --- Zombie.hurtServer: reinforcements ----------------------------------------------------------

// zombieHurtReinforcements ports the reinforcement-spawn tail of Zombie.hurtServer (VERIFIED javap
// Zombie.hurtServer @42-466), which runs AFTER super.hurtServer landed a hit:
//
//	LivingEntity target = getTarget();
//	if (target == null && source.getEntity() instanceof LivingEntity le) target = le;
//	if (target != null
//	        && level.getDifficulty() == HARD
//	        && (double) random.nextFloat() < getAttributeValue(SPAWN_REINFORCEMENTS_CHANCE)   // DRAW: gate
//	        && level.isSpawningMonsters()) {
//	    int x0=floor(getX), y0=floor(getY), z0=floor(getZ);
//	    Zombie reinf = (Zombie) getType().create(level, REINFORCEMENT);
//	    if (reinf == null) return true;
//	    for (int i = 0; i < 50; i++) {
//	        int x = x0 + Mth.nextInt(random,7,40) * Mth.nextInt(random,-1,1);   // 2 DRAWS
//	        int y = y0 + Mth.nextInt(random,7,40) * Mth.nextInt(random,-1,1);   // 2 DRAWS
//	        int z = z0 + Mth.nextInt(random,7,40) * Mth.nextInt(random,-1,1);   // 2 DRAWS
//	        BlockPos pos = new BlockPos(x,y,z);
//	        if (isSpawnPositionOk && checkSpawnRules && !hasNearbyAlivePlayer(x,y,z,7)
//	                && isUnobstructed && noCollision && !liquid-block) {
//	            reinf.setPos(x,y,z); setTarget(target); reinf.finalizeSpawn(...REINFORCEMENT);
//	            addFreshEntityWithPassengers(reinf);
//	            <parent SPAWN_REINFORCEMENTS_CHANCE -= 0.05 via CALLER_CHARGE>
//	            <child SPAWN_REINFORCEMENTS_CHANCE += CALLEE_CHARGE (-0.05)>
//	            break;
//	        }
//	    }
//	}
//	return true;
//
// THE 6-DRAW-PER-TRY ORDER IS LOAD-BEARING: per loop iteration the offsets draw
// Mth.nextInt(7,40) then Mth.nextInt(-1,1) for x, then the same pair for y, then for z -- SIX nextInt
// draws in that exact x,y,z / (magnitude, sign) order. Mth.nextInt(r,lo,hi) == lo>=hi ? lo :
// r.nextInt(hi-lo+1)+lo, so (7,40) is r.nextInt(34)+7 and (-1,1) is r.nextInt(3)-1. The gate nextFloat()
// is drawn ONCE, only after the target/HARD checks pass (the leading `&&` short-circuits mean an
// EASY/NORMAL zombie or a targetless one draws NOTHING here).
//
// v1 REDUCTIONS (cited, structured to become real reads): the placement guards isSpawnPositionOk /
// checkSpawnRules / isUnobstructed / noCollision / liquid have no v1 subsystem -- so a candidate offset
// is accepted when it is NOT within 7 blocks of a live player (the hasNearbyAlivePlayer(...,7.0) gate,
// which IS ported) -- the vanilla `!hasNearbyAlivePlayer` acceptance guard. isSpawningMonsters() is the
// doMobSpawning gamerule, a cited const-true (no gamerule store yet). getCurrentDifficultyAt(pos) for the
// child's finalizeSpawn reduces to the server difficulty (the DifficultyInstance stub food.go uses).
// The child spawns through spawnDeclaredMob (real attrs + goals), the same store path every summon uses.
//
// Zombie-family-gated by the caller (typ == Zombie/Husk/Drowned/ZombieVillager). NORMAL is the cited
// serverDifficulty, so the HARD gate makes this a dead path in production EXACTLY as vanilla (reinforcements
// are HARD-only) -- a NORMAL zombie draws NOTHING. A pig never reaches this; its oracle stream is untouched.
//
// Cite Zombie.hurtServer + the SPAWN_REINFORCEMENTS_CHANCE handling (REINFORCEMENT_CALLER_CHARGE_ID /
// ZOMBIE_REINFORCEMENT_CALLEE_CHARGE, both -0.05).
func (t *TickLoop) zombieHurtReinforcements(e *Entity, src damageSource) {
	// target = getTarget(); if null && source.getEntity() instanceof LivingEntity -> that entity.
	targetID := mobTarget(e)
	if targetID == 0 {
		// source.getEntity() instanceof LivingEntity: every v1 causing entity (player or mob) is a
		// LivingEntity; an environmental hit leaves attacker 0 (the shared proxy combat_mob.go uses).
		targetID = src.attacker
	}
	if targetID == 0 {
		return // target == null -> no reinforcement
	}
	// level.getDifficulty() == HARD. serverDifficulty is the cited NORMAL stub, so this is HARD-only.
	if serverDifficulty != difficultyHard {
		return
	}
	// (double) random.nextFloat() < getAttributeValue(SPAWN_REINFORCEMENTS_CHANCE): the gate DRAW.
	rng := mobRandom(e)
	if float64(rng.nextFloat()) >= e.getAttributeValue(attribute.SpawnReinforcementsChance) {
		return
	}
	// level.isSpawningMonsters(): the doMobSpawning gamerule, cited const-true (no gamerule store yet).
	t.zombieSpawnReinforcement(e, targetID, rng)
}

// zombieSpawnReinforcement is the placement + charge CORE of Zombie.hurtServer's reinforcement block
// (VERIFIED javap @85-457) -- the part that runs once the target/HARD/chance/isSpawningMonsters gates in
// zombieHurtReinforcements have all passed. It is a separate method so the exact 6-draw-per-try offset
// order + the -0.05 caller/callee charge math are directly test-drivable without the HARD-difficulty stub
// (serverDifficulty is a compile-time NORMAL const, so the gated wrapper's HARD branch is a dead path in
// v1 EXACTLY as vanilla reinforcements are HARD-only; the mechanism is proven here). `rng` is the zombie's
// own stream (the SAME source the gate nextFloat was drawn from). Cite Zombie.hurtServer.
func (t *TickLoop) zombieSpawnReinforcement(e *Entity, targetID int32, rng *entityRandom) {
	x0 := floorI(e.x)
	y0 := floorI(e.y)
	z0 := floorI(e.z)

	if t.mobRegistry == nil {
		return
	}
	decl := t.mobRegistry.declByBaseType(e.typ) // getType().create(level, REINFORCEMENT) == same type
	if decl == nil {
		return // create(...) == null analogue -> return true (no reinforcement)
	}

	for i := 0; i < zombieReinforceMaxTries; i++ {
		// x = x0 + Mth.nextInt(7,40) * Mth.nextInt(-1,1) -- the 2 draws (magnitude then sign), x,y,z order.
		x := x0 + mthNextIntZombie(rng, zombieReinforceOffsetMin, zombieReinforceOffsetMax)*mthNextIntZombie(rng, -1, 1)
		y := y0 + mthNextIntZombie(rng, zombieReinforceOffsetMin, zombieReinforceOffsetMax)*mthNextIntZombie(rng, -1, 1)
		z := z0 + mthNextIntZombie(rng, zombieReinforceOffsetMin, zombieReinforceOffsetMax)*mthNextIntZombie(rng, -1, 1)

		// Placement acceptance: the ported `!hasNearbyAlivePlayer(x,y,z,7.0)` guard (the other guards --
		// isSpawnPositionOk/checkSpawnRules/isUnobstructed/noCollision/liquid -- are cited v1 reductions).
		if t.zombieReinforceHasNearbyPlayer(float64(x), float64(y), float64(z), 7.0) {
			continue
		}

		// setPos + setTarget + finalizeSpawn(REINFORCEMENT) + addFreshEntity: the child is spawned as a real
		// declared mob at the offset (its own finalizeSpawn draws land inside spawnDeclaredMob).
		child := t.spawnDeclaredMob(decl, float64(x), float64(y), float64(z))
		if child != nil {
			child.lastHurtByMob = targetID // reinf.setTarget(target) (the retaliation seed; setTarget analogue)
			child.hasLastDamage = false
			// PARENT charge: chargeInst.getModifier(CALLER_CHARGE) amount, removeModifier, then
			// addPermanentModifier(new AttributeModifier(CALLER_CHARGE, amount - 0.05, ADD_VALUE)) -- the
			// caller charge STACKS DOWN each successful reinforcement.
			if e.attributes != nil {
				if inst := e.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name()); inst != nil {
					prev := 0.0
					if m, ok := inst.GetModifier(zombieReinforceCallerChargeID); ok {
						prev = m.Amount
					}
					inst.RemoveModifier(zombieReinforceCallerChargeID)
					inst.AddPermanentModifier(attribute.AttributeModifier{
						ID:        zombieReinforceCallerChargeID,
						Amount:    prev + zombieReinforceCharge,
						Operation: attribute.AddValue,
					})
				}
			}
			// CHILD charge: addPermanentModifier(ZOMBIE_REINFORCEMENT_CALLEE_CHARGE) -- a flat -0.05 ADD_VALUE.
			if child.attributes != nil {
				if inst := child.attributes.GetInstance(attribute.SpawnReinforcementsChance.Name()); inst != nil {
					inst.AddPermanentModifier(attribute.AttributeModifier{
						ID:        zombieReinforceCalleeChargeID,
						Amount:    zombieReinforceCharge,
						Operation: attribute.AddValue,
					})
				}
			}
		}
		break // vanilla breaks the loop on the first successful placement
	}
}

// mthNextIntZombie ports net.minecraft.util.Mth.nextInt(RandomSource, int, int): `lo >= hi ? lo :
// random.nextInt(hi - lo + 1) + lo` (inclusive-both-ends). VERIFIED javap Mth.nextInt(RandomSource,II).
func mthNextIntZombie(rng *entityRandom, lo, hi int) int {
	if lo >= hi {
		return lo
	}
	return rng.nextInt(hi-lo+1) + lo
}

// zombieReinforceHasNearbyPlayer ports ServerLevel.hasNearbyAlivePlayer(x,y,z,7.0): true iff some alive,
// non-spectator player is within 7 blocks of (x,y,z). v1 players are always alive+non-spectator (the
// spawnerIsNearPlayer reduction), so the distance test is the load-bearing gate. Cite
// EntityGetter.hasNearbyAlivePlayer.
func (t *TickLoop) zombieReinforceHasNearbyPlayer(x, y, z, r float64) bool {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dx := p.x - x
		dy := p.y - y
		dz := p.z - z
		if dx*dx+dy*dy+dz*dz < r*r {
			return true
		}
	}
	return false
}

// --- Zombie.tick: underwater (drowning) conversion ---------------------------------------------

// zombieWaterConversionTick ports the ServerLevel branch of Zombie.tick (VERIFIED javap Zombie.tick
// @16-119), the drowning-conversion state machine every zombie-family mob runs:
//
//	if (isAlive() && !isNoAi()) {
//	    if (isUnderWaterConverting()) {
//	        if (--conversionTime < 0) doUnderWaterConversion(level);
//	    } else if (convertsInWater()) {
//	        if (isEyeInFluid(WATER)) {
//	            if (++inWaterTime >= 600) startUnderWaterConversion(300);
//	        } else {
//	            inWaterTime = -1;
//	        }
//	    }
//	}
//
// convertsInWater() is TRUE for the base Zombie (and inherited by Husk) -- a submerged zombie starts a
// 300-tick drowning countdown at inWaterTime>=600, then converts. doUnderWaterConversion: base Zombie ->
// DROWNED; Husk -> ZOMBIE (Husk.doUnderWaterConversion overrides the target). isEyeInFluid(WATER) reuses
// the shared fluidAt(eye).isWater sample (the witch/breath pattern). RNG-FREE. Zombie-family-gated by the
// caller; a pig never reaches it. Called from tickAI AFTER serverAiStep (the sibling of drownedAiStep).
//
// Cite Zombie.tick + isUnderWaterConverting + startUnderWaterConversion + doUnderWaterConversion +
// convertsInWater + Husk.doUnderWaterConversion.
func (t *TickLoop) zombieWaterConversionTick(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	// A drowned/zombie-villager does NOT re-convert in water (Drowned.convertsInWater is a cited false --
	// a drowned is already aquatic; ZombieVillager likewise does not drown-convert). Only the base Zombie
	// and the Husk carry convertsInWater()==true. Gate on those two so a drowned in water is inert.
	if e.typ != entity.Zombie.ID && e.typ != entity.Husk.ID {
		return
	}
	if e.zombieUnderWaterConverting { // isUnderWaterConverting()
		e.zombieConversionTime-- // --conversionTime
		if e.zombieConversionTime < 0 {
			t.zombieDoUnderWaterConversion(e)
		}
		return
	}
	// else if (convertsInWater()) -- true for Zombie + Husk (gated above).
	if t.zombieEyeInWater(e) { // isEyeInFluid(WATER)
		e.zombieInWaterTime++
		if e.zombieInWaterTime >= zombieInWaterConvertThreshold {
			// startUnderWaterConversion(300): conversionTime = 300; DATA_DROWNED_CONVERSION_ID = true.
			e.zombieConversionTime = zombieUnderWaterConvertTime
			e.zombieUnderWaterConverting = true
		}
	} else {
		e.zombieInWaterTime = -1
	}
}

// zombieEyeInWater ports Zombie.tick's isEyeInFluid(FluidTags.WATER): sample the fluid at the zombie's
// EYE (y + height*0.85, the shared eye-Y the witch uses). A nil world (a bare test loop) is treated as
// not-in-water. Cite Entity.isEyeInFluid(WATER) (the fluidAt(eye).isWater pattern).
func (t *TickLoop) zombieEyeInWater(e *Entity) bool {
	if t.world() == nil {
		return false
	}
	eyeY := e.y + float64(e.height)*0.85
	return t.fluidAt(pk.Position{X: floorI(e.x), Y: floorI(eyeY), Z: floorI(e.z)}).isWater
}

// zombieDoUnderWaterConversion ports Zombie/Husk.doUnderWaterConversion(ServerLevel): convertToZombieType
// (convertTo) to the water-conversion target -- base Zombie -> DROWNED, Husk -> ZOMBIE -- then a levelEvent
// (1040 for the drowned conversion / 1041 for the husk) which is a CITED client-particle no-op. v1 mutates
// the entity type IN PLACE (the same in-place convert zombieVillagerFinishConversion uses): the wire type
// flips, the water-conversion state clears, and the mob keeps its id/pos/health with a re-attached AI. The
// full convertTo attribute re-roll (a fresh Drowned/Zombie createAttributes) is reduced to the type flip +
// the STEP_HEIGHT divergence for the drowned; structured so a real convertTo lands later. Cite
// Zombie.doUnderWaterConversion (-> DROWNED) + Husk.doUnderWaterConversion (-> ZOMBIE) + convertToZombieType.
func (t *TickLoop) zombieDoUnderWaterConversion(e *Entity) {
	var target entity.ID
	if e.typ == entity.Husk.ID {
		target = entity.Zombie.ID // Husk.doUnderWaterConversion: convertToZombieType(ZOMBIE)
	} else {
		target = entity.Drowned.ID // Zombie.doUnderWaterConversion: convertToZombieType(DROWNED)
	}
	e.typ = target
	// Clear the water-conversion state (convertTo builds a fresh mob; the marks reset).
	e.zombieUnderWaterConverting = false
	e.zombieConversionTime = 0
	e.zombieInWaterTime = 0
	// A DROWNED gets the isDrowned mark so its per-type aiStep (trident) + trident-throw seam fire.
	if target == entity.Drowned.ID {
		e.isDrowned = true
	} else {
		e.isDrowned = false
	}
	if e.ai == nil {
		e.ai = &mobAI{}
		reseedMobAI(e.ai, e.id)
	}
	// levelEvent(1040 drowned / 1041 husk): a client conversion-particle cue -- CITED no-op (no levelEvent
	// particle broadcast for this in v1); the type conversion is the observable gameplay preserved.
}
