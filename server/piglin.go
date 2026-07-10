// piglin.go -- the Piglin (net.minecraft.world.entity.monster.piglin.Piglin), a 1:1 port from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p / CFR this session). The flagship nether
// mob: a BRAIN-driven hostile with gold-armor neutrality, gold-ingot bartering, and an off-nether
// zombification timer. Bounded to attributes + gold-neutrality + melee hostility + zombification + barter;
// the deep admire/avoid/hunt/celebrate/ride activity graph is CITE-DEFERRED. Code-spawned (spawnPiglin)
// with a minimal e.ai + a Brain attached (like the villager/happy-ghast), driven per-type from tickAI
// (piglinBrainTick), the sibling of blazeAiStep.
//
// VANILLA (verified javap Piglin + AbstractPiglin + PiglinAi + PiglinSpecificSensor this session):
//   Piglin.createAttributes: Monster.createMonsterAttributes + MAX_HEALTH 16.0 + MOVEMENT_SPEED
//     0.3499999940395355 (0.35f widened) + ATTACK_DAMAGE 5.0. Piglin ctor: xpReward = 5.
//   Zombification (AbstractPiglin): CONVERSION_TIME 300. off-nether ++timeInOverworld; GT 300 -> convertTo
//     ZOMBIFIED_PIGLIN. Gold neutrality: PiglinSpecificSensor + isWearingSafeArmor (PIGLIN_SAFE_ARMOR tag).
//   Barter: Piglin.mobInteract -> PiglinAi.mobInteract: canAdmire (adult + GOLD_INGOT) -> consume 1 +
//     stopHoldingOffHandItem -> throwItems(getBarterResponseItems == roll PIGLIN_BARTERING).
//
// CITE-DEFERRED: the full PiglinAi activity graph (idle/fight/celebrate/admire/retreat/ride + crossbow
// ranged attack + the throw-toward-player aim + the admire-hold delay). populateDefaultEquipmentSlots +
// the finalizeSpawn baby roll. The bounded behaviors (attrs/neutrality/melee/zombify/barter) land here.

package server

import (
	"bytes"
	"math"
	"math/rand/v2"

	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/loot"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// Piglin constants (VERIFIED javap Piglin + AbstractPiglin + PiglinAi this session).
const (
	piglinXpReward        = 5   // Piglin ctor: xpReward = 5
	piglinConversionTime  = 300 // AbstractPiglin.CONVERSION_TIME == 300 (the GT 300 finishConversion gate)
	piglinMeleeAttackTime = 20  // MeleeAttackGoal.resetAttackCooldown: adjustedTickDelay(20) between swings
	piglinBarterItemID    = 936 // PiglinAi.BARTERING_ITEM == Items.GOLD_INGOT (data/item id 936)

	piglinAdmiringDisabledTime = 400 // PiglinAi.wasHurtBy: ADMIRING_DISABLED setMemoryWithExpiry 400l (a player hit)
	piglinBabyAvoidTime        = 100 // PiglinAi.wasHurtBy: baby AVOID_TARGET setMemoryWithExpiry 100l
	piglinMaybeRetaliateRange  = 4.0 // PiglinAi.maybeRetaliate: isOtherTargetMuchFurtherAwayThanCurrentAttackTarget(4.0)
	piglinAngerBroadcastRange  = 16.0 // getAdultPiglins == NEARBY_ADULT_PIGLINS (NearestLivingEntities FOLLOW_RANGE 16 scan)
)


// spawnPiglin creates a Piglin at (x,y,z) and adds it to the owner region store (the tracker broadcasts
// AddEntity next tick). It attaches a minimal e.ai + a Brain (the PiglinAi core recipe, bounded) --
// mirroring attachVillagerBrain/attachHappyGhastBrain but for a CODE-DRIVEN nether hostile (the fight melee
// + the conversion tail run in piglinBrainTick, the blaze pattern). initSpawnHealth seeds health from the
// folded MAX_HEALTH (16.0). baby sets DATA_BABY_ID (a baby piglin does not hunt/attack players). Cite
// Piglin(EntityType, Level) + AbstractPiglin.<init>.
func (t *TickLoop) spawnPiglin(x, y, z float64, baby bool) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.Piglin, x, y, z)
	e.isPiglin = true
	// Piglin ctor: xpReward = 5 -- the xpReward field is not modeled on *Entity yet (death_mob.go computes
	// the per-type reward; a future per-type xpReward slots in there). Cited constant.
	_ = piglinXpReward
	// setBaby(baby): DATA_BABY_ID. A baby breedAge LT 0 drives isBaby() (the shared ageable model).
	if baby {
		e.breedAge = -1
	}
	initSpawnHealth(e) // LivingEntity.<init>: setHealth(getMaxHealth()) -> 16.0
	e.ai = &mobAI{}
	reseedMobAI(e.ai, e.id)
	// Attach the (bounded) Brain AFTER reseedMobAI (its per-mob RNG is e.ai.rng, now seeded), exactly like
	// the villager/happy-ghast. Cite Piglin.makeBrain.
	attachPiglinBrain(e)
	// A baby DATA_BABY_ID must render on the FIRST AddEntity/SetEntityData a tracker sends (the same
	// babyDataEntry carry seam the plugin spawn path uses). An adult (breedAge==0) is skipped entirely.
	if e.isBaby() {
		var buf bytes.Buffer
		_, _ = babyDataEntry(e.isBaby()).WriteTo(&buf)
		e.metadata = append(e.metadata, buf.Bytes()...)
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.entities != nil {
		owner.entities.add(e)
	}
	return e
}

// attachPiglinBrain builds and attaches the (bounded) ported Brain to a piglin at spawn (Piglin.makeBrain
// via Piglin.BRAIN_PROVIDER.makeBrain). The bounded provider registers the CORE activity the melee-pursuit
// steering uses (the deep graph is cite-deferred). Cite Piglin.makeBrain.
func attachPiglinBrain(e *Entity) {
	e.brain = newPiglinBrainProvider().makeBrain(e)
}

// newPiglinBrainProvider builds the bounded brainProvider for the piglin: the CORE activity. The full
// PiglinAi.getActivities graph is cite-deferred; the CORE Swim/LookAtTargetSink/MoveToTargetSink lead is
// the real slice ported (the same three the villager CORE ports).
//
//	[VERIFIED CFR Piglin.BRAIN_PROVIDER + PiglinAi.getActivities: [initCoreActivity, initIdleActivity,
//	 initFightActivity, initCelebrateActivity, initAdmireItemActivity, initRetreatActivity,
//	 initRideHoglinActivity]. The CORE Swim/LookAtTargetSink/MoveToTargetSink lead is the ported slice.]
func newPiglinBrainProvider() *brainProvider {
	p := newBrainProvider()
	p.addActivity(piglinInitCoreActivity())
	return p
}

// piglinInitCoreActivity ports the real lead of PiglinAi.initCoreActivity: [Swim(0.8), LookAtTargetSink(45,
// 90), MoveToTargetSink(), ...]. The anger/admire/eat/celebrate tail is cite-deferred (the deep graph).
//
//	[VERIFIED CFR PiglinAi.initCoreActivity: Pair.of(0, Swim(0.8f)); LookAtTargetSink(45, 90);
//	 MoveToTargetSink(); (the interact/anger/admire/celebrate tail cite-deferred).]
func piglinInitCoreActivity() activityData {
	pairs := []prioritizedBehavior{
		{priority: 0, behavior: newSwim(0.8)},
		{priority: 0, behavior: newLookAtTargetSink(45, 90)},
		{priority: 1, behavior: newMoveToTargetSink(150, 250)},
	}
	return activityDataCreatePairs(activityCore, pairs, nil, nil)
}

// piglinBrainTick ports Piglin.customServerAiStep: getBrain().tick(level, this); PiglinAi.updateActivity;
// super.customServerAiStep (== AbstractPiglin -> the zombification timer). Called from tickAI AFTER
// serverAiStep (the empty goalSelector no-op, like the villager). The FIGHT-activity melee is reduced to
// the code-driven acquisition+melee (blaze pattern), gated on adult (baby piglins do not hunt).
//
//	[VERIFIED CFR Piglin.customServerAiStep: getBrain().tick(level, this); PiglinAi.updateActivity(this);
//	 super.customServerAiStep. AbstractPiglin.customServerAiStep: if isConverting() ++timeInOverworld else
//	 0; if timeInOverworld GT 300 finishConversion.]
func (t *TickLoop) piglinBrainTick(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// getBrain().tick(level, this): the bounded brain. NON-nil for every spawned piglin; a bare test-loop
	// piglin with no brain still runs the melee + conversion (the nil-guard mirrors villagerBrainTick).
	if e.brain != nil {
		e.brain.tick(t, e, t.GameTime())
	}
	// Tick down the boolean-with-expiry memories (setMemoryWithExpiry decrements each brain tick; on 0 the
	// memory is erased). ADMIRING_DISABLED gates the barter; AVOID_TARGET gates the baby flee. Cite Brain.tick
	// expiring memories + PiglinAi.wasHurtBy (400l / 100l).
	if e.piglinAdmiringDisabled > 0 {
		e.piglinAdmiringDisabled--
	}
	if e.piglinAvoidTicks > 0 {
		e.piglinAvoidTicks--
		if e.piglinAvoidTicks == 0 {
			e.piglinAvoidTargetID = 0 // memory expired -> AVOID_TARGET erased
		}
	}
	if e.piglinAdmireTicks > 0 {
		e.piglinAdmireTicks--
	}
	// AVOID activity (reduced): while a baby is fleeing an avoid target (hit while a baby), it retreats and
	// does NOT hunt (RetreatFromNearestHostile / avoidZombified shape). Cite PiglinAi.initRetreatActivity.
	if e.piglinAvoidTicks > 0 {
		t.piglinAvoidTick(e)
		return
	}
	// The FIGHT-activity melee (reduced): an ADULT piglin acquires the nearest non-gold-armored player and
	// melees when adjacent. A baby does NOT hunt (canHunt / the adult gate), so this is adult-only. An
	// already-angry piglin (ANGRY_AT set by wasHurtBy) keeps its retaliation target even if that player wears
	// gold -- the anger overrides the sensor neutrality (piglinAcquireNearestPlayer preserves a live target).
	if e.piglinIsAdult() {
		t.piglinAcquireNearestPlayer(e)
		t.piglinMeleeGoalTick(e)
	}
	// IDLE-activity avoidZombified: arm the AVOID flee if a zombified piglin/zoglin is within 6 blocks (runs
	// before the pickup/idle work; if it arms, the next tick's AVOID branch takes over). Cite PiglinAi.avoidZombified.
	t.piglinAvoidZombifiedTick(e)
	// Mob.aiStep item-collection (canPickUpLoot): the piglin picks up dropped PIGLIN_LOVED gold, admires it,
	// then auto-barters when the admire timer expires. Runs for adults + babies (wantsToPickup gates babies).
	// Cite Mob.aiStep + PiglinAi.pickUpItem/wantsToPickup + StopHoldingItemIfNoLongerAdmiring.
	t.piglinItemPickupTick(e)
	// super.customServerAiStep (AbstractPiglin): the off-nether zombification timer.
	t.piglinZombificationTick(e)
}

// piglinIsAdult ports AbstractPiglin.isAdult(): not isBaby(). A baby piglin does not hunt/attack players.
//
//	[VERIFIED javap AbstractPiglin.isAdult: return !isBaby().]
func (e *Entity) piglinIsAdult() bool { return !e.isBaby() }

// piglinIsConverting ports AbstractPiglin.isConverting: not isImmuneToZombification(), not isNoAi(), and
// level.environmentAttributes().getValue(PIGLINS_ZOMBIFY, position()). PIGLINS_ZOMBIFY is TRUE everywhere
// EXCEPT the nether, so a piglin OUTSIDE the nether is converting. v1: isImmuneToZombification is the
// piglinImmuneToZombification flag (default false); isNoAi() is a cited const-false; the environment read
// is the region-dimension check (dimNether == in-nether == NOT converting). Cite AbstractPiglin.isConverting
// + EnvironmentAttributes.PIGLINS_ZOMBIFY.
func (t *TickLoop) piglinIsConverting(e *Entity) bool {
	if e.piglinImmuneToZombification { // isImmuneToZombification()
		return false
	}
	// isNoAi(): cited const-false (a /dbg or naturally-spawned piglin always has AI).
	// environmentAttributes().getValue(PIGLINS_ZOMBIFY, position()): TRUE off-nether, FALSE in the nether.
	// The regions are overworld-partitioned (the nether is a separate t.netherWorld manager, not a per-
	// region dimension tag), so a piglin's in-nether state is carried on the entity (piglinInNether, set at
	// a nether spawn). Default false == off-nether == converting (a /dbg overworld piglin zombifies, exactly
	// as vanilla). Structured to become a real per-region dimension read when regions carry a dimension.
	return !e.piglinInNether // off-nether -> PIGLINS_ZOMBIFY true -> converting
}

// piglinZombificationTick ports AbstractPiglin.customServerAiStep (the timer tail): if isConverting()
// ++timeInOverworld else 0; if (timeInOverworld GT 300) finishConversion. finishConversion (Piglin +
// AbstractPiglin) == convertTo(ZOMBIFIED_PIGLIN, ConversionParams.single(this,true,true), finalize), the
// type-swap reusing lightning_conversion.go thunderHitTypeSwap. The playConvertedSound + admire-cancel +
// inventory-drop are cite-deferred cues; the observable (300 ticks off-nether -> a zombified piglin) lands.
//
//	[VERIFIED javap AbstractPiglin.customServerAiStep: isConverting() ? ++timeInOverworld : 0;
//	 if (timeInOverworld GT 300) finishConversion(level).]
func (t *TickLoop) piglinZombificationTick(e *Entity) {
	if t.piglinIsConverting(e) {
		e.piglinTimeInOverworld++ // ++timeInOverworld
	} else {
		e.piglinTimeInOverworld = 0 // else timeInOverworld = 0 (reset on re-entering the nether)
	}
	if e.piglinTimeInOverworld > piglinConversionTime {
		// difficulty != PEACEFUL -> playConvertedSound() (cited client sound, no-op); the convert is the point.
		t.piglinFinishConversion(e)
	}
}

// piglinFinishConversion ports Piglin.finishConversion -> AbstractPiglin.finishConversion: convertTo(
// ZOMBIFIED_PIGLIN, ...). The type-swap spawns a fresh entity at the piglin position with default max
// health, then discards the piglin (== convertTo create+copyPosition+discard). The fresh entity is then
// wired as a REAL, functioning ZombifiedPiglin: its type flag is set and a minimal e.ai is attached so
// zombifiedPiglinAiStep drives it (neutral-until-provoked anger + pack spread). It starts NEUTRAL
// (angerEndTime 0). The afterConversion inventory-drop + client sound remain cite-deferred cues. Cite
// AbstractPiglin.finishConversion + Mob.convertTo + ConversionType.SINGLE + ZombifiedPiglin.
func (t *TickLoop) piglinFinishConversion(e *Entity) *Entity {
	if e.dead {
		return nil // convertTo isRemoved() guard: a removed mob does not convert
	}
	zp := t.thunderHitTypeSwap(e, entity.ZombifiedPiglin)
	if zp == nil {
		return nil
	}
	// Wire the swapped entity as a real zombified piglin (thunderHitTypeSwap leaves it bare): the type flag
	// + a minimal e.ai so the per-type aiStep runs. It starts NEUTRAL (angerEndTime 0, no target).
	zp.isZombifiedPiglin = true
	if zp.ai == nil {
		zp.ai = &mobAI{}
		reseedMobAI(zp.ai, zp.id)
	}
	// AbstractPiglin.finishConversion AfterConversion callback: the new ZombifiedPiglin gets NAUSEA 200t
	// (lambda$finishConversion$0: addEffect(new MobEffectInstance(NAUSEA, 200))). Applied for real via the
	// entity-effect seam. Cite AbstractPiglin.finishConversion + MobEffects.NAUSEA.
	t.addEntityEffect(zp, "minecraft:nausea", 200, 0)
	return zp
}

// piglinTarget reads the piglin current attack-target player (Mob.getTarget() via e.ai.attackTargetID), or
// nil. Tick-owned (mirrors blazeTarget/vexTarget).
func (t *TickLoop) piglinTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// piglinAcquireNearestPlayer ports the PiglinAi target chain (PiglinSpecificSensor -> NEAREST_TARGETABLE_
// PLAYER_NOT_WEARING_GOLD -> findNearestValidAttackTarget): acquire the nearest LIVE player within
// FOLLOW_RANGE that is NOT wearing gold armor. GOLD-ARMOR NEUTRALITY keystone -- a player wearing a
// PIGLIN_SAFE_ARMOR (gold) piece is skipped by the sensor, so the piglin never targets them; a bare player
// IS targeted. v1 reduces the sensor+memory chain to a direct nearest-non-gold scan (the blazeAcquire shape)
// with the isWearingSafeArmor filter. NO RNG. Cite PiglinSpecificSensor + PiglinAi.findNearestValidAttackTarget.
func (t *TickLoop) piglinAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // createMobAttributes default 16.0
	rangeSqr := followRange * followRange
	// ANGRY_AT (wasHurtBy retaliation) overrides the sensor neutrality: an angered piglin fights its attacker
	// even if that player wears gold. While the ANGRY_AT memory is live (piglinAngerEnd not yet reached) and the
	// angered player is alive, keep hunting it (StartHuntingBehavior reads ANGRY_AT, not the gold-safe memory).
	// Cite PiglinAi.setAngerTarget (ANGRY_AT 600l) + StartHuntingBehavior.
	if e.piglinAngeredAt != 0 {
		ap := t.playerByEntityID(e.piglinAngeredAt)
		if ap == nil || ap.dead || t.gametime >= e.piglinAngerEnd {
			e.piglinAngeredAt = 0 // ANGRY_AT expired / gone
			e.piglinAngerEnd = 0
			if e.ai.attackTargetID != 0 && (ap == nil || ap.dead || e.ai.attackTargetID == e.piglinAngeredAt) {
				e.ai.attackTargetID = 0
			}
		} else {
			e.ai.attackTargetID = ap.entityID // hunt the angered player regardless of gold armor
			return
		}
	}
	// Drop a current target that died, went out of range, OR started wearing gold (the neutrality applies
	// mid-fight too -- the sensor stops filling the not-wearing-gold memory).
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr || piglinPlayerWearsGold(p) {
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
		if piglinPlayerWearsGold(p) {
			continue // NEUTRALITY: the sensor never fills the memory for a gold-armored player
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq <= bestSq {
			bestSq = dsq
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID // Mob.setTarget(nearest non-gold player)
	}
}

// piglinPlayerWearsGold ports PiglinAi.isWearingSafeArmor(LivingEntity): for slot in EquipmentSlotGroup.ARMOR
// (FEET, LEGS, CHEST, HEAD) if getItemBySlot(slot).is(ItemTags.PIGLIN_SAFE_ARMOR) return true. The
// PIGLIN_SAFE_ARMOR tag is the four golden armor pieces (data/tag ids 1002-1005). Any gold armor piece
// makes the player "safe" (the piglin will not target them).
//
//	[VERIFIED javap PiglinAi.isWearingSafeArmor: EquipmentSlotGroup.ARMOR iterator; getItemBySlot(slot).is(
//	 ItemTags.PIGLIN_SAFE_ARMOR) -> true.]
func piglinPlayerWearsGold(p *tickPlayer) bool {
	for _, slot := range []int{eqSlotFeet, eqSlotLegs, eqSlotChest, eqSlotHead} {
		it := playerItemBySlot(p, slot)
		if slotIsEmpty(it) {
			continue
		}
		if itemInTag(int32(it.ItemID), "piglin_safe_armor") {
			return true
		}
	}
	return false
}

// piglinMeleeGoalTick ports the FIGHT-activity melee (reduced to the MeleeAttackGoal adjacency shape blaze
// uses for its d LT 4.0 branch): decrement the swing cooldown; if a target is within melee range and the
// cooldown elapsed, doHurtTarget for ATTACK_DAMAGE (5.0) and reset; otherwise pursue. The crossbow ranged
// attack is cite-deferred with the deep fight graph. NO RNG. Cite PiglinAi.initFightActivity + MeleeAttackGoal.tick.
func (t *TickLoop) piglinMeleeGoalTick(e *Entity) {
	if e.piglinAttackTime > 0 {
		e.piglinAttackTime-- // MeleeAttackGoal: --ticksUntilNextAttack
	}
	target := t.piglinTarget(e)
	if target == nil {
		// StopAttackingIfTargetInvalid also resets a mid-charge crossbow (stop(): stopUsingItem).
		e.piglinCrossbowState = 0
		e.piglinCrossbowCharge = 0
		return
	}
	// FIGHT-activity weapon routing: a crossbow piglin uses the CrossbowAttack behavior (charge + ranged fire
	// + BackUpIfTooClose), NOT the melee swing. Cite PiglinAi.initFightActivity (CrossbowAttack + MeleeAttack).
	if e.piglinIsCrossbow {
		t.piglinCrossbowAttackTick(e, target)
		return
	}
	// MeleeAttack.canAttack / Mob.isWithinMeleeAttackRange: the inflated-attack-box vs target-hitbox
	// intersection (the faithful reach hoglin/zombified_piglin already use), NOT a fixed 4.0 center
	// distance. Cite Mob.isWithinMeleeAttackRange + PiglinAi MeleeAttack behavior.
	if isWithinMeleeAttackRange(e, target) {
		if e.piglinAttackTime <= 0 {
			e.piglinAttackTime = piglinMeleeAttackTime // resetAttackCooldown: adjustedTickDelay(20)
			t.piglinDoHurtTarget(e, target)
		}
	} else {
		// pursue: MoveToTargetSink drives the nav; here the want-target proxy (the blaze/vex pattern).
		e.ai.setWantTargetMod(target.x, target.y, target.z, 1.0) // navigation.moveTo(target, 1.0) (seam x MOVEMENT_SPEED)
	}
}

// piglinDoHurtTarget ports Mob.doHurtTarget for the melee branch: deal ATTACK_DAMAGE (5.0) to the player
// through the shared player hurt path (the blazeDoHurtTarget shape). NO RNG. Cite Mob.doHurtTarget.
func (t *TickLoop) piglinDoHurtTarget(e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // (float) getAttributeValue(ATTACK_DAMAGE) == 5.0
	src := damageSourceMobAttack(e.id)                          // getWeaponItem().getDamageSource(this) -> mob_attack(this)
	t.applyDamage(target, src, dmg)                             // target.hurtServer(level, src, f) -- the PLAYER path
}

// piglinMobInteract ports Piglin.mobInteract -> PiglinAi.mobInteract (the barter path). Called from
// handleInteract (piglin-gated) BEFORE the feed fall-through (a piglin is never pig_food-fed). It returns
// true when the interact is CONSUMED by the piglin (a barter) so handleInteract does NOT fall through.
//
// PiglinAi.mobInteract: stack = player.getItemInHand(hand); if canAdmire(this, stack) { consumeAndReturn(1,
// player); holdInOffhand; admireGoldItem; stopWalking; SUCCESS } else PASS. canAdmire: not admiring-
// disabled, not admiring-item, isAdult(), isBarterCurrency(stack == GOLD_INGOT).
//
// v1 reduction (CITED): isAdmiringDisabled/isAdmiringItem are the admire-timer memories (deferred with the
// ADMIRE_ITEM activity), so canAdmire here == isAdult() and the held item is a GOLD_INGOT. On a match:
// shrink 1 gold ingot, then fire the barter DROP synchronously (stopHoldingOffHandItem -> throwItems).
//
//	[VERIFIED javap Piglin.mobInteract + PiglinAi.mobInteract + canAdmire + isBarterCurrency (GOLD_INGOT) +
//	 getBarterResponseItems (BuiltInLootTables.PIGLIN_BARTERING).]
func (t *TickLoop) piglinMobInteract(p *tickPlayer, e *Entity) bool {
	if e.dead || e.health <= 0 {
		return false
	}
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot)) // player.getItemInHand(hand): the selected main-hand item
	if slotIsEmpty(held) {
		return false // PASS: an empty hand is not a barter
	}
	// canAdmire: isAdult() and isBarterCurrency(stack == GOLD_INGOT). (isAdmiringDisabled/isAdmiringItem are
	// the deferred admire-timer memories -- cited const-false in v1.)
	if !e.piglinIsAdult() || int32(held.ItemID) != piglinBarterItemID {
		return false // PASS: a baby, or a non-gold-ingot item -> not a barter (fall through to no-feed)
	}
	// stack.consumeAndReturn(1, player): shrink one gold ingot (the established shrinkHeldItem seam). The
	// holdInOffhand + admireGoldItem + stopWalking cues are cite-deferred; the consume + drop are what land.
	t.shrinkHeldItem(p, inv)
	t.piglinBarter(e)
	return true // SUCCESS: the barter consumed the interact (no feed fall-through)
}

// piglinBarter ports PiglinAi.stopHoldingOffHandItem barter branch: throwItems(this, getBarterResponseItems
// (this)). getBarterResponseItems rolls BuiltInLootTables.PIGLIN_BARTERING at a server seed; throwItems
// spawns the rolled ItemStacks as item entities at the piglin (the throw-toward-player aim is cite-deferred).
// Reuses the loot-roll + item-entity-spawn seams (death_mob.go dropMobLoot). A missing table is a silent
// no-op. Returns the dropped stacks (the test asserts a non-empty barter drop).
//
//	[VERIFIED javap PiglinAi.getBarterResponseItems: getLootTable(PIGLIN_BARTERING); getRandomItems(params).
//	 throwItems -> throwItemsTowardPos -> BehaviorUtils.throwItem per stack.]
func (t *TickLoop) piglinBarter(e *Entity) []component.SlotData {
	tbl, err := loot.LoadTable("minecraft:gameplay/piglin_bartering")
	if err != nil {
		return nil // no embedded barter table (unported build) -> no drop (never panic)
	}
	// The barter loot seed: a fresh math/rand/v2 draw (server-generated, never client-supplied).
	seed := rand.Int64()
	ctx := loot.NewLootContext(seed, 0) // luck 0; PIGLIN_BARTER params (THIS_ENTITY) are v1-defaulted
	var dropped []component.SlotData
	for _, stack := range loot.Roll(tbl, seed, ctx) {
		if stack.Count <= 0 {
			continue
		}
		// throwItems -> throwItemsTowardPlayer/RandomPos -> throwItemsTowardPos -> BehaviorUtils.throwItem:
		// spawn at (piglin.x, eyeY - 0.3, piglin.z) with a throw velocity toward the target position + (0,1,0),
		// = normalize(targetPos - piglin.position()) * (0.3, 0.3, 0.3). OWNER-region routing (dropMobLoot seam).
		ie := t.piglinThrowItem(e, stack)
		owner := t.regionForEntity(e)
		if owner == nil {
			owner = t.cur()
		}
		if owner != nil && owner.entities != nil {
			owner.entities.add(ie)
		}
		dropped = append(dropped, stack)
	}
	return dropped
}

// piglinThrowItem ports throwItems -> throwItemsTowardPos -> BehaviorUtils.throwItem for one stack. The throw
// target is the NEAREST_VISIBLE_PLAYER position + (0,1,0) if a player is nearby (throwItemsTowardPlayer), else
// a random nearby pos (throwItemsTowardRandomPos, v1-reduced to the piglin's own position so the item plops with
// no aim). throwItem: spawn at (x, eyeY - 0.3, z), delta = normalize(targetPos - piglin.position) * 0.3 per
// axis, setDefaultPickUpDelay. Cite PiglinAi.throwItems/throwItemsTowardPos + BehaviorUtils.throwItem.
func (t *TickLoop) piglinThrowItem(e *Entity, stack component.SlotData) *Entity {
	spawnY := e.y + e.eyeHeightForArrow() - 0.30000001192092896 // getEyeY() - 0.3f
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, spawnY, e.z, stack)
	// throwItems: target the nearest visible player (position + (0,1,0)); else no aim (feet).
	target := t.piglinNearestVisiblePlayer(e)
	if target != nil {
		tx := target.x - e.x
		ty := (target.y + 1.0) - e.y // pos.add(0, 1, 0) relative to piglin.position() (feet y)
		tz := target.z - e.z
		nx, ny, nz := normalizeVec3(tx, ty, tz)
		ie.vx = nx * 0.30000001192092896 // multiply(0.3, 0.3, 0.3)
		ie.vy = ny * 0.30000001192092896
		ie.vz = nz * 0.30000001192092896
	}
	ie.pickupDelay = itemDefaultPickupDelay // setDefaultPickUpDelay
	return ie
}

// piglinNearestVisiblePlayer reduces the NEAREST_VISIBLE_PLAYER memory read to a nearest-live-player scan
// within FOLLOW_RANGE. Cite PiglinAi.throwItems (NEAREST_VISIBLE_PLAYER).
func (t *TickLoop) piglinNearestVisiblePlayer(e *Entity) *tickPlayer {
	followRange := e.getAttributeValue(attribute.FollowRange)
	rangeSqr := followRange * followRange
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
	return best
}

// piglinWasHurtBy ports Piglin.hurtServer -> PiglinAi.wasHurtBy(level, piglin, attacker). It runs from the
// applyDamageEntity piglin hook AFTER a landed hit (flag2) with a LivingEntity attacker (src.attacker != 0).
// Vanilla flow (javap PiglinAi.wasHurtBy): if attacker is a Piglin return; if holding offhand item ->
// stopHoldingOffHandItem(false); erase CELEBRATE_LOCATION/DANCING/ADMIRING_ITEM; if attacker is a Player ->
// ADMIRING_DISABLED true 400L; if avoid-target == attacker erase it; if isBaby() -> AVOID_TARGET attacker 100L
// + (attackable ? broadcastAngerTarget); else if HOGLIN outnumber -> retreat; else maybeRetaliate. The attacker
// is always a Player here (src.attacker maps to a tickPlayer). Cite PiglinAi.wasHurtBy.
func (t *TickLoop) piglinWasHurtBy(e *Entity, src damageSource) {
	attacker := t.playerByEntityID(src.attacker)
	if attacker == nil {
		return
	}
	if !slotIsEmpty(e.piglinOffhandItem) {
		t.piglinStopHoldingOffHandItem(e, false) // stopHoldingOffHandItem(level, piglin, false)
	}
	e.piglinAdmireTicks = 0                                  // erase ADMIRING_ITEM (celebrate/dance are cues)
	e.piglinAdmiringDisabled = piglinAdmiringDisabledTime    // Player attacker -> ADMIRING_DISABLED 400L
	if e.piglinAvoidTargetID == src.attacker {              // getAvoidTarget == attacker -> erase AVOID_TARGET
		e.piglinAvoidTicks = 0
		e.piglinAvoidTargetID = 0
	}
	if e.isBaby() {
		e.piglinAvoidTicks = piglinBabyAvoidTime // AVOID_TARGET attacker 100L
		e.piglinAvoidTargetID = src.attacker
		if t.piglinIsEntityAttackable(attacker) {
			t.piglinBroadcastAngerTarget(e, attacker) // rally the adult pack
		}
		return
	}
	t.piglinMaybeRetaliate(e, attacker)
}

// piglinMaybeRetaliate ports PiglinAi.maybeRetaliate: if AVOID active return; if !attackable return; if the new
// target is much further than the current attack target (> +4.0) return; UNIVERSAL_ANGER (default false) ->
// else branch: setAngerTarget + broadcastAngerTarget. Cite PiglinAi.maybeRetaliate.
func (t *TickLoop) piglinMaybeRetaliate(e *Entity, attacker *tickPlayer) {
	if e.piglinAvoidTicks > 0 {
		return
	}
	if !t.piglinIsEntityAttackable(attacker) {
		return
	}
	if cur := t.piglinTarget(e); cur != nil {
		curD := math.Sqrt(distanceToSqrPlayer(cur, e))
		newD := math.Sqrt(distanceToSqrPlayer(attacker, e))
		if newD > curD+piglinMaybeRetaliateRange {
			return
		}
	}
	t.piglinSetAngerTarget(e, attacker)
	t.piglinBroadcastAngerTarget(e, attacker)
}

// piglinSetAngerTarget ports PiglinAi.setAngerTarget: if !attackable return; setMemoryWithExpiry(ANGRY_AT,
// attacker, 600L). ANGRY_AT ignores the gold-safe armor -> a gold-armored attacker is still fought. Reduced to
// piglinAngeredAt + 600t expiry, seeding the FIGHT attackTargetID. Cite PiglinAi.setAngerTarget (ANGRY_AT 600l).
func (t *TickLoop) piglinSetAngerTarget(e *Entity, attacker *tickPlayer) {
	if !t.piglinIsEntityAttackable(attacker) {
		return
	}
	e.piglinAngeredAt = attacker.entityID
	e.piglinAngerEnd = t.gametime + 600
	if e.ai != nil {
		e.ai.attackTargetID = attacker.entityID
	}
}

// piglinBroadcastAngerTarget ports PiglinAi.broadcastAngerTarget: getAdultPiglins(piglin).forEach(other ->
// setAngerTargetIfCloserThanCurrent(other, attacker)). getAdultPiglins == NEARBY_ADULT_PIGLINS (the sensor's
// FOLLOW_RANGE 16 adult scan). Reduced to a 16-block region scan -- the anger PACK spread. Cite PiglinAi.broadcastAngerTarget.
func (t *TickLoop) piglinBroadcastAngerTarget(e *Entity, attacker *tickPlayer) {
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return
	}
	for _, other := range owner.entities.all() {
		if other == nil || other == e || other.dead || !other.isPiglin || !other.piglinIsAdult() {
			continue
		}
		if math.Abs(other.x-e.x) > piglinAngerBroadcastRange ||
			math.Abs(other.y-e.y) > piglinAngerBroadcastRange ||
			math.Abs(other.z-e.z) > piglinAngerBroadcastRange {
			continue
		}
		t.piglinSetAngerTargetIfCloserThanCurrent(other, attacker)
	}
}

// piglinSetAngerTargetIfCloserThanCurrent ports PiglinAi.setAngerTargetIfCloserThanCurrent: keep the current
// ANGRY_AT unless the candidate is strictly nearer, then setAngerTarget. Cite PiglinAi.setAngerTargetIfCloserThanCurrent.
func (t *TickLoop) piglinSetAngerTargetIfCloserThanCurrent(other *Entity, attacker *tickPlayer) {
	if other.piglinAngeredAt != 0 && t.gametime < other.piglinAngerEnd {
		cur := t.playerByEntityID(other.piglinAngeredAt)
		if cur != nil && !cur.dead {
			if distanceToSqrPlayer(cur, other) <= distanceToSqrPlayer(attacker, other) {
				return
			}
		}
	}
	t.piglinSetAngerTarget(other, attacker)
}

// piglinIsEntityAttackable ports Sensor.isEntityAttackableIgnoringLineOfSight for a player target: alive + a
// valid candidate. The gold-safe armor check is NOT part of this predicate (separate sensor memory) -- which is
// why a gold-armored player is still angered. v1 reduces to alive. Cite Sensor.isEntityAttackableIgnoringLineOfSight.
func (t *TickLoop) piglinIsEntityAttackable(p *tickPlayer) bool {
	return p != nil && !p.dead
}

// piglinAvoidTick ports the AVOID activity (RetreatFromNearestHostile / baby-flee): walk AWAY from the avoid
// target at retreat speed 1.0. Reduced to a want-target proxy pointing directly away. Cite PiglinAi.initRetreatActivity.
func (t *TickLoop) piglinAvoidTick(e *Entity) {
	if e.ai == nil || e.piglinAvoidTargetID == 0 {
		return
	}
	e.ai.attackTargetID = 0 // a fleeing piglin has no FIGHT target
	// The AVOID_TARGET is a player (baby-flee / retaliation) OR a mob (avoidZombified). Resolve either.
	var tx, tz float64
	var alive bool
	if p := t.playerByEntityID(e.piglinAvoidTargetID); p != nil && !p.dead {
		tx, tz, alive = p.x, p.z, true
	} else if owner := t.regionForEntity(e); owner != nil && owner.entities != nil {
		if m, ok := owner.entities.get(e.piglinAvoidTargetID); ok && m != nil && !m.dead {
			tx, tz, alive = m.x, m.z, true
		}
	}
	if !alive {
		e.piglinAvoidTicks = 0
		e.piglinAvoidTargetID = 0
		return
	}
	dx := e.x - tx
	dz := e.z - tz
	e.ai.setWantTargetMod(e.x+dx, e.y, e.z+dz, 1.0)
}

// piglinStopHoldingOffHandItem ports PiglinAi.stopHoldingOffHandItem(level, piglin, shouldBarter): drop or
// barter the held offhand item. shouldBarter=false throws it at the feet with no roll; true routes the barter.
// Cite PiglinAi.stopHoldingOffHandItem.
func (t *TickLoop) piglinStopHoldingOffHandItem(e *Entity, shouldBarter bool) {
	held := e.piglinOffhandItem
	e.piglinOffhandItem = component.SlotData{}
	if shouldBarter {
		t.piglinBarter(e)
		return
	}
	if slotIsEmpty(held) || held.Count <= 0 {
		return
	}
	ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, held)
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.entities != nil {
		owner.entities.add(ie)
	}
}

// Piglin finalizeSpawn item IDs (VERIFIED data/item/item.go).
const (
	piglinGoldenHelmet     = 1002 // Items.GOLDEN_HELMET
	piglinGoldenChestplate = 1003 // Items.GOLDEN_CHESTPLATE
	piglinGoldenLeggings   = 1004 // Items.GOLDEN_LEGGINGS
	piglinGoldenBoots      = 1005 // Items.GOLDEN_BOOTS
	piglinCrossbowItem     = 1370 // Items.CROSSBOW
	piglinGoldenSword      = 954  // Items.GOLDEN_SWORD
	piglinGoldenSpear      = 1330 // Items.GOLDEN_SPEAR
	piglinTimeBetweenHuntsMin  = 600  // TIME_BETWEEN_HUNTS rangeOfSeconds(30,120) -> UniformInt(600,2400)
	piglinTimeBetweenHuntsSpan = 1801 // 2400 - 600 + 1
)

// piglinFinalizeSpawn ports Piglin.finalizeSpawn 1:1 with the EXACT RNG draw order. It performs the natural-
// spawn baby/weapon/armor rolls that spawnPiglin(baby) skips (spawnPiglin takes an explicit baby with no
// rolls). Wire it at a natural/structure spawn seam AFTER spawnPiglin. The draw order (javap):
//   RandomSource random = level.getRandom();                                      // the LEVEL random
//   if (reason != STRUCTURE) {
//       if (random.nextFloat() < 0.2f) setBaby(true);                             // LEVEL nextFloat
//       else if (isAdult()) setItemSlot(MAINHAND, createSpawnWeapon());           // createSpawnWeapon -> ENTITY random
//   }
//   PiglinAi.initMemories(this, random);                                          // LEVEL nextInt(1801)
//   populateDefaultEquipmentSlots(random, difficulty);                            // if adult: 4x LEVEL nextFloat
//   populateDefaultEquipmentEnchantments(...);                                    // (cited no-op v1)
// createSpawnWeapon (Piglin.createSpawnWeapon) draws on THIS.random (the entity RNG), NOT the level random:
//   if (this.random.nextFloat() < 0.5) CROSSBOW; else (this.random.nextInt(10)==0 ? GOLDEN_SPEAR : GOLDEN_SWORD)
// maybeWearArmor: each slot rolls the LEVEL random.nextFloat() < 0.1f. Cite Piglin.finalizeSpawn +
// createSpawnWeapon + populateDefaultEquipmentSlots + maybeWearArmor + PiglinAi.initMemories.
func (t *TickLoop) piglinFinalizeSpawn(e *Entity, reasonStructure bool) {
	lr := t.regionForEntity(e)
	if lr == nil {
		lr = t.cur()
	}
	if lr == nil || lr.levelRandom == nil || e.ai == nil || e.ai.rng == nil {
		return
	}
	rand := lr.levelRandom
	if !reasonStructure {
		if rand.NextFloat() < 0.2 { // random.nextFloat() < 0.2f -> setBaby(true)
			e.breedAge = -1 // setBaby(true)
		} else if e.piglinIsAdult() {
			// setItemSlot(MAINHAND, createSpawnWeapon()) -- createSpawnWeapon draws the ENTITY random.
			e.setItemSlot(eqSlotMainHand, piglinCreateSpawnWeapon(e))
		}
	}
	// PiglinAi.initMemories: TIME_BETWEEN_HUNTS.sample(random) == 600 + nextInt(1801) (HUNTED_RECENTLY expiry).
	_ = piglinTimeBetweenHuntsMin + rand.NextIntN(piglinTimeBetweenHuntsSpan)
	// populateDefaultEquipmentSlots: if adult, roll each of HEAD/CHEST/LEGS/FEET at nextFloat() < 0.1f.
	if e.piglinIsAdult() {
		piglinMaybeWearArmor(e, rand, eqSlotHead, piglinGoldenHelmet)
		piglinMaybeWearArmor(e, rand, eqSlotChest, piglinGoldenChestplate)
		piglinMaybeWearArmor(e, rand, eqSlotLegs, piglinGoldenLeggings)
		piglinMaybeWearArmor(e, rand, eqSlotFeet, piglinGoldenBoots)
	}
	// If the mainhand is a crossbow, mark the piglin crossbow-armed (the FIGHT CrossbowAttack path).
	if int32(e.getItemBySlot(eqSlotMainHand).ItemID) == piglinCrossbowItem {
		e.piglinIsCrossbow = true
	}
	// A baby DATA_BABY_ID render entry (the same seam spawnPiglin uses) -- refresh if the baby roll flipped it.
	if e.isBaby() && len(e.metadata) == 0 {
		var buf bytes.Buffer
		_, _ = babyDataEntry(true).WriteTo(&buf)
		e.metadata = append(e.metadata, buf.Bytes()...)
	}
}

// piglinCreateSpawnWeapon ports Piglin.createSpawnWeapon (draws the ENTITY random): nextFloat() < 0.5 ->
// CROSSBOW; else nextInt(10) == 0 -> GOLDEN_SPEAR else GOLDEN_SWORD. Cite Piglin.createSpawnWeapon.
func piglinCreateSpawnWeapon(e *Entity) component.SlotData {
	if e.ai.rng.nextFloat() < 0.5 {
		return component.SlotData{Count: 1, ItemID: piglinCrossbowItem}
	}
	if e.ai.rng.nextInt(10) == 0 {
		return component.SlotData{Count: 1, ItemID: piglinGoldenSpear}
	}
	return component.SlotData{Count: 1, ItemID: piglinGoldenSword}
}

// piglinMaybeWearArmor ports Piglin.maybeWearArmor(slot, stack, random): if random.nextFloat() < 0.1f
// setItemSlot(slot, stack). Cite Piglin.maybeWearArmor.
func piglinMaybeWearArmor(e *Entity, rand *levelgen.LegacyRandomSource, slot int, itemID int32) {
	if rand.NextFloat() < 0.1 {
		e.setItemSlot(slot, component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
	}
}

// Piglin crossbow constants (VERIFIED javap CrossbowAttack + CrossbowItem + Piglin.performRangedAttack).
const (
	piglinCrossbowChargeDur     = 25  // CrossbowItem.getChargeDuration default (1.25f*20; no Quick Charge)
	piglinCrossbowRange         = 8.0 // isWithinAttackRange: crossbow getDefaultProjectileRange 8 - padding 0
	piglinBackUpIfTooCloseRange = 5   // BackUpIfTooClose.create(5, 0.75f) -- closerThan(5) triggers the back-up
	piglinBackUpSpeed           = 0.75
)

// piglinCrossbowAttackTick ports the CrossbowAttack behavior state machine (javap CrossbowAttack.crossbowAttack)
// plus the FIGHT BackUpIfTooClose(5, 0.75) that runs alongside it. Start condition (checkExtraStartConditions):
// isHolding(CROSSBOW) && canSee(target) && isWithinAttackRange(mob, target, 0). The state machine (drawn on the
// ENTITY random, the Mob.getRandom() the performCrossbowAttack shares):
//   UNCHARGED       -> startUsingItem; CHARGING.
//   CHARGING        -> ++ticksUsingItem; if >= chargeDuration(25) release; CHARGED; attackDelay = 20+nextInt(20).
//   CHARGED         -> --attackDelay; if 0 READY_TO_ATTACK.
//   READY_TO_ATTACK -> performRangedAttack(target, 1.0) [Piglin ignores power -> performCrossbowAttack(this,1.6)]; UNCHARGED.
// The lookAtTarget(mob, target) (LOOK_TARGET) each tick is reduced to the want-look proxy. Cite CrossbowAttack.
func (t *TickLoop) piglinCrossbowAttackTick(e *Entity, target *tickPlayer) {
	// BackUpIfTooClose(5, 0.75): if the attack target is within 5 blocks, walk directly away at 0.75 speed
	// (SetWalkTargetAwayFrom shape). Runs regardless of the charge state (a separate FIGHT behavior). Cite
	// BackUpIfTooClose.create(5, 0.75f).
	distSq := distanceToSqrPlayer(target, e)
	if distSq < float64(piglinBackUpIfTooCloseRange*piglinBackUpIfTooCloseRange) {
		dx := e.x - target.x
		dz := e.z - target.z
		if e.ai != nil {
			e.ai.setWantTargetMod(e.x+dx, e.y, e.z+dz, piglinBackUpSpeed)
		}
	}
	// checkExtraStartConditions: within crossbow range (closerThan(8)). Out of range -> keep the FIGHT
	// SetWalkTargetFromAttackTargetIfTargetOutOfReach(1.0) approach and hold the charge state.
	if distSq > piglinCrossbowRange*piglinCrossbowRange {
		if e.ai != nil {
			e.ai.setWantTargetMod(target.x, target.y, target.z, 1.0)
		}
		return
	}
	r := mobRandom(e)
	switch e.piglinCrossbowState {
	case 0: // UNCHARGED -> startUsingItem; CHARGING
		e.piglinCrossbowCharge = 0
		e.piglinCrossbowState = 1
	case 1: // CHARGING
		e.piglinCrossbowCharge++ // getTicksUsingItem()
		if e.piglinCrossbowCharge >= piglinCrossbowChargeDur {
			e.piglinCrossbowState = 2                        // release -> CHARGED
			e.piglinCrossbowAttackDelay = 20 + r.nextInt(20) // attackDelay = 20 + nextInt(20)
		}
	case 2: // CHARGED -> --attackDelay; ==0 -> READY_TO_ATTACK
		e.piglinCrossbowAttackDelay--
		if e.piglinCrossbowAttackDelay <= 0 {
			e.piglinCrossbowState = 3
		}
	case 3: // READY_TO_ATTACK -> fire; UNCHARGED
		t.performCrossbowAttack(e, target) // Piglin.performRangedAttack -> performCrossbowAttack(this, 1.6)
		e.piglinCrossbowState = 0
	}
}

// Piglin item-pickup / admire constants (VERIFIED javap PiglinAi + admireGoldItem + item ids).
const (
	piglinAdmireDuration = 119 // admireGoldItem: ADMIRING_ITEM setMemoryWithExpiry 119l (ADMIRE_DURATION)
	piglinGoldNugget     = 935 // Items.GOLD_NUGGET (isBarterCurrency is GOLD_INGOT; GOLD_NUGGET goes to inventory)
)

// piglinItemPickupTick ports Mob.aiStep's item-collection scan gated by PiglinAi.wantsToPickup + the pickUpItem
// handler for the GOLD path. An adult (or non-baby-ignored) piglin near a dropped PIGLIN_LOVED item that it
// wants to pick up takes it into its offhand and starts admiring (admireGoldItem: ADMIRING_ITEM 119t). When the
// admire timer expires, StopHoldingItemIfNoLongerAdmiring fires stopHoldingOffHandItem(true) -> the barter. This
// is the observable "drop gold near a piglin -> it picks it up, admires, then auto-barters" flow. The full
// wantsToPickup food/equip branches are cite-deferred; the LOVED (gold) path is ported. Cite Mob.aiStep item
// scan + PiglinAi.wantsToPickup + PiglinAi.pickUpItem + holdInOffhand + admireGoldItem + StopHoldingItemIfNoLongerAdmiring.
func (t *TickLoop) piglinItemPickupTick(e *Entity) {
	// StopHoldingItemIfNoLongerAdmiring (CORE): holding an offhand item AND no longer admiring -> barter it.
	if !slotIsEmpty(e.piglinOffhandItem) && e.piglinAdmireTicks <= 0 {
		t.piglinStopHoldingOffHandItem(e, true) // stopHoldingOffHandItem(level, piglin, true) -> throwItems barter
		return
	}
	// Already holding + admiring -> do not pick up another (isNotHoldingLovedItemInOffHand gate).
	if !slotIsEmpty(e.piglinOffhandItem) {
		return
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return
	}
	// The Mob item-collection AABB: the piglin box inflated (1.0, 0.5, 1.0). Cite Mob.aiStep.
	hw := e.width/2.0 + itemPickupInflateXZ
	loY, hiY := e.y-itemPickupInflateY, e.y+e.height+itemPickupInflateY
	for _, ie := range owner.entities.all() {
		if ie == nil || !ie.isItem || ie.dead || ie.pickupDelay > 0 {
			continue
		}
		if ie.itemStack.Count <= 0 {
			continue
		}
		if math.Abs(ie.x-e.x) > hw || math.Abs(ie.z-e.z) > hw || ie.y < loY || ie.y > hiY {
			continue
		}
		if !t.piglinWantsToPickup(e, ie.itemStack) {
			continue
		}
		t.piglinPickUpItem(e, ie)
		return // pick up one per tick (the vanilla scan handles the first collectible)
	}
}

// piglinWantsToPickup ports PiglinAi.wantsToPickup for the GOLD path: a baby ignores IGNORED_BY_PIGLIN_BABIES;
// PIGLIN_REPELLENTS are never wanted; if ADMIRING_DISABLED and has an ATTACK_TARGET, no; GOLD_INGOT
// (isBarterCurrency) is wanted iff not already holding a loved offhand item; a GOLD_NUGGET is wanted iff it fits
// the inventory (v1 always fits); an isLovedItem is wanted iff not holding a loved offhand item. The food/equip
// branches are cite-deferred. Cite PiglinAi.wantsToPickup.
func (t *TickLoop) piglinWantsToPickup(e *Entity, stack component.SlotData) bool {
	if slotIsEmpty(stack) {
		return false
	}
	id := int32(stack.ItemID)
	// PIGLIN_REPELLENTS (soul torch/soul lantern/soul campfire) are never picked up.
	if itemInTag(id, "piglin_repellents") {
		return false
	}
	// isAdmiringDisabled && has ATTACK_TARGET -> false (a hit, fighting piglin ignores gold).
	if e.piglinAdmiringDisabled > 0 && e.ai != nil && e.ai.attackTargetID != 0 {
		return false
	}
	// isBarterCurrency (GOLD_INGOT) -> isNotHoldingLovedItemInOffHand.
	if id == piglinBarterItemID {
		return slotIsEmpty(e.piglinOffhandItem)
	}
	// GOLD_NUGGET -> canAddToInventory (v1 always true; goes to the inventory, not admired).
	if id == piglinGoldNugget {
		return true
	}
	// isLovedItem (PIGLIN_LOVED) -> isNotHoldingLovedItemInOffHand.
	if itemInTag(id, "piglin_loved") {
		return slotIsEmpty(e.piglinOffhandItem)
	}
	return false
}

// piglinPickUpItem ports PiglinAi.pickUpItem for the GOLD path: a GOLD_NUGGET is taken whole into the inventory
// (putInInventory, v1 -> discard, no admire); any other picked item that isLovedItem is split 1 off, held in the
// offhand, and admired (admireGoldItem: ADMIRING_ITEM 119t). The take + item-entity shrink mirror the vanilla
// take(entity,count) + removeOneItemFromItemEntity. Cite PiglinAi.pickUpItem + holdInOffhand + admireGoldItem.
func (t *TickLoop) piglinPickUpItem(e *Entity, ie *Entity) {
	id := int32(ie.itemStack.ItemID)
	if id == piglinGoldNugget {
		// take(itemEntity, count) whole; putInInventory (v1: consume the whole stack, no admire).
		ie.itemStack.Count = 0
		ie.dead = true
		if owner := t.regionForEntity(ie); owner != nil && owner.entities != nil {
			owner.entities.remove(ie.id)
		}
		return
	}
	// removeOneItemFromItemEntity: split 1 off; discard the item entity if it empties.
	one := ie.itemStack
	one.Count = 1
	ie.itemStack.Count--
	if ie.itemStack.Count <= 0 {
		ie.dead = true
		if owner := t.regionForEntity(ie); owner != nil && owner.entities != nil {
			owner.entities.remove(ie.id)
		}
	}
	// isLovedItem -> holdInOffhand + admireGoldItem.
	if itemInTag(id, "piglin_loved") {
		e.piglinOffhandItem = one   // holdInOffhand (the offhand was empty -- guarded by wantsToPickup)
		e.piglinAdmireTicks = piglinAdmireDuration // admireGoldItem: ADMIRING_ITEM 119t
	}
}

// Piglin avoid-zombified constants (VERIFIED javap PiglinAi: isNearZombified closerThan 6.0;
// AVOID_ZOMBIFIED_DURATION rangeOfSeconds(5,7) -> UniformInt(100,140)).
const (
	piglinNearZombifiedDist       = 6.0 // isNearZombified: closerThan(nearestZombified, 6.0)
	piglinAvoidZombifiedDurMin    = 100 // AVOID_ZOMBIFIED_DURATION UniformInt(100,140): 100 + nextInt(41)
	piglinAvoidZombifiedDurSpan   = 41
)

// piglinAvoidZombifiedTick ports the IDLE-activity avoidZombified behavior + isNearZombified gate. avoidZombified
// (CopyMemoryWithExpiry): copy NEAREST_VISIBLE_ZOMBIFIED -> AVOID_TARGET for AVOID_ZOMBIFIED_DURATION (5-7s), so
// the AVOID activity's SetWalkTargetAwayFrom flees it at speed 1.0. The zombified-visible memory is a nearby
// ZombifiedPiglin or Zoglin; isNearZombified requires closerThan(6.0). Reduced to a region scan for the nearest
// zombified within 6 blocks -> arm the AVOID timer at it. NOT applied while already fleeing or fighting. Cite
// PiglinAi.avoidZombified + isNearZombified + AVOID_ZOMBIFIED_DURATION + SetWalkTargetAwayFrom(AVOID_TARGET,1.0).
func (t *TickLoop) piglinAvoidZombifiedTick(e *Entity) {
	if e.piglinAvoidTicks > 0 {
		return // already fleeing (AVOID active)
	}
	if e.ai != nil && e.ai.attackTargetID != 0 {
		return // FIGHT overrides IDLE/AVOID
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return
	}
	var nearest *Entity
	bestSq := piglinNearZombifiedDist * piglinNearZombifiedDist
	for _, other := range owner.entities.all() {
		if other == nil || other == e || other.dead {
			continue
		}
		if !other.isZombifiedPiglin && !other.isZoglin {
			continue // NEAREST_VISIBLE_ZOMBIFIED == a zombified piglin or a zoglin
		}
		dx := other.x - e.x
		dy := other.y - e.y
		dz := other.z - e.z
		dsq := dx*dx + dy*dy + dz*dz
		if dsq < bestSq { // closerThan(6.0) is a strict 3D distSq < 36
			bestSq = dsq
			nearest = other
		}
	}
	if nearest == nil {
		return
	}
	// CopyMemoryWithExpiry(NEAREST_VISIBLE_ZOMBIFIED -> AVOID_TARGET, AVOID_ZOMBIFIED_DURATION 100+nextInt(41)).
	e.piglinAvoidTicks = piglinAvoidZombifiedDurMin
	if e.ai != nil && e.ai.rng != nil {
		e.piglinAvoidTicks += e.ai.rng.nextInt(piglinAvoidZombifiedDurSpan)
	}
	e.piglinAvoidTargetID = nearest.id
}
