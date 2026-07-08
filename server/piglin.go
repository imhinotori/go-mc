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
	"math/rand/v2"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/loot"
)

// Piglin constants (VERIFIED javap Piglin + AbstractPiglin + PiglinAi this session).
const (
	piglinXpReward        = 5   // Piglin ctor: xpReward = 5
	piglinConversionTime  = 300 // AbstractPiglin.CONVERSION_TIME == 300 (the GT 300 finishConversion gate)
	piglinMeleeAttackTime = 20  // MeleeAttackGoal.resetAttackCooldown: adjustedTickDelay(20) between swings
	piglinBarterItemID    = 936 // PiglinAi.BARTERING_ITEM == Items.GOLD_INGOT (data/item id 936)
)

// piglinMeleeRangeSqr is the MeleeAttackGoal getAttackReachSqr slack proxy sized to the piglin 0.6 width
// (the same adjacency proxy blaze d LT 4.0 melee branch uses). Cite MeleeAttackGoal.getAttackReachSqr.
const piglinMeleeRangeSqr = 4.0

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
	// The FIGHT-activity melee (reduced): an ADULT piglin acquires the nearest non-gold-armored player and
	// melees when adjacent. A baby does NOT hunt (canHunt / the adult gate), so this is adult-only.
	if e.piglinIsAdult() {
		t.piglinAcquireNearestPlayer(e)
		t.piglinMeleeGoalTick(e)
	}
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
// health, then discards the piglin (== convertTo create+copyPosition+discard). The afterConversion +
// zombified-piglin AI are DEFERRED (the SAME deferral lightning_conversion.go pig swap documents). Cite
// AbstractPiglin.finishConversion + Mob.convertTo + ConversionType.SINGLE.
func (t *TickLoop) piglinFinishConversion(e *Entity) *Entity {
	if e.dead {
		return nil // convertTo isRemoved() guard: a removed mob does not convert
	}
	return t.thunderHitTypeSwap(e, entity.ZombifiedPiglin)
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
		return
	}
	d := distanceToSqrPlayer(target, e)
	if d <= piglinMeleeRangeSqr {
		if e.piglinAttackTime <= 0 {
			e.piglinAttackTime = piglinMeleeAttackTime // resetAttackCooldown: adjustedTickDelay(20)
			t.piglinDoHurtTarget(e, target)
		}
	} else {
		// pursue: MoveToTargetSink drives the nav; here the want-target proxy (the blaze/vex pattern).
		e.ai.setWantTargetSpeed(target.x, target.y, target.z, 1.0)
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
		// BehaviorUtils.throwItem: spawn the item entity at the piglin. OWNER-region routing (dropMobLoot seam).
		ie := NewItemEntity(t.idAlloc.AllocID(), e.x, e.y+e.height/2.0, e.z, stack)
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
