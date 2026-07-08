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
	// Drowned.performRangedAttack: shoot(..., 1.6f, 14 - difficulty*4). velocity 1.6, inaccuracy 14-diff*4.
	drownedTridentVelocity = 1.6

	// ZombieVillager.startConverting: villagerConversionTime = random.nextInt(2401) + 3600 (3600..6000).
	zvConversionBaseTime = 3600
	zvConversionRandCap  = 2401
	// getConversionProgress(): base 1 per tick (the bed/iron-bars acceleration is a cited stub, see below).
	zvConversionProgressBase = 1
)

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
	if !drownedHoldsTrident(e) {
		e.drownedTridentTime = 0 // not holding a trident -> the ranged goal is inactive
		return
	}
	target := t.variantTarget(e)
	if target == nil {
		e.drownedTridentTime = 0
		return
	}
	// RangedAttackGoal.tick: gate on distance <= attackRadiusSqr && hasLineOfSight; decrement attackTime;
	// on <=0 perform the attack + reset to attackInterval. (v1: hasLineOfSight via the cached raycast.)
	distSq := distanceToSqrPlayer(target, e)
	if distSq > drownedTridentAttackRadiusSq || !t.sensingHasLineOfSight(e, target) {
		e.drownedTridentTime = 0
		return
	}
	if e.drownedTridentTime > 0 {
		e.drownedTridentTime--
		return
	}
	t.drownedPerformRangedAttack(e, target)
	e.drownedTridentTime = drownedTridentAttackInterval // resetAttackCooldown -> attackInterval 40
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
