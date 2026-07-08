package server

// variant_mobs2.go -- Go-native SIGNATURE behavior for CaveSpider (poison-on-hit), PiglinBrute
// (always-hostile melee 7.0, Go-native spawn), and Illusioner (blindness + casting invisibility). Ported
// 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). Every hook is per-type-gated
// so the pig oracle is untouched (a pig never reaches this code).
//
// Cite: net.minecraft.world.entity.monster.spider.CaveSpider.doHurtTarget (POISON i*20);
// net.minecraft.world.entity.monster.piglin.PiglinBrute + PiglinBruteAi (always-hostile fight, melee 7.0);
// net.minecraft.world.entity.monster.illager.Illusioner IllusionerBlindnessSpellGoal (BLINDNESS 400,
// interval 180, warmup 20) + Illusioner.aiStep (invisible while casting).

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

const (
	caveSpiderPoisonSecondsNormal = 7  // CaveSpider.doHurtTarget: NORMAL -> i=7
	caveSpiderPoisonSecondsHard   = 15 // CaveSpider.doHurtTarget: HARD -> i=15 (EASY/PEACEFUL -> 0)
	caveSpiderPoisonAmplifier     = 0  // MobEffectInstance(POISON, i*20) single-arg ctor -> amplifier 0

	piglinBruteXpReward        = 20  // PiglinBrute ctor: xpReward = 20 (cited constant; not modeled on *Entity)
	piglinBruteMeleeAttackTime = 20  // MeleeAttackGoal resetAttackCooldown adjustedTickDelay(20)
	piglinBruteMeleeRangeSqr   = 4.0 // MeleeAttackGoal getAttackReachSqr adjacency proxy (brute width 0.6)

	illusionerBlindnessInterval  = 180 // IllusionerBlindnessSpellGoal.getCastingInterval() == 180
	illusionerBlindnessWarmup    = 20  // SpellcasterUseSpellGoal spellWarmup 20 (invisible while casting)
	illusionerBlindnessDuration  = 400 // performSpellCasting: MobEffectInstance(BLINDNESS, 400)
	illusionerBlindnessAmplifier = 0   // single-arg ctor -> amplifier 0
)

// caveSpiderApplyPoison ports CaveSpider.doHurtTarget tail: after super.doHurtTarget lands, add
// MobEffectInstance(POISON, i*20, 0) to the LivingEntity target (i=7 NORMAL, i=15 HARD, i=0 else). The
// victim is a player, so POISON routes through addPlayerEffect. CaveSpider-gated in checkAndPerformAttack.
//
//	[VERIFIED javap CaveSpider.doHurtTarget: super.doHurtTarget; if target instanceof LivingEntity: i=0;
//	 NORMAL -> i=7; HARD -> i=15; if i>0 target.addEffect(new MobEffectInstance(POISON, i*20), this).]
func (t *TickLoop) caveSpiderApplyPoison(e *Entity, target *tickPlayer) {
	i := 0
	switch serverDifficulty {
	case difficultyNormal:
		i = caveSpiderPoisonSecondsNormal // NORMAL -> 7
	case difficultyHard:
		i = caveSpiderPoisonSecondsHard // HARD -> 15
	}
	if i <= 0 {
		return // EASY / PEACEFUL -> no poison (i == 0)
	}
	t.addPlayerEffect(target, e.id, effectPoison, i*20, caveSpiderPoisonAmplifier, 1.0)
}

// spawnPiglinBrute creates an always-hostile PiglinBrute at (x,y,z) and adds it to the owner region store
// (the tracker broadcasts AddEntity next tick). Mirrors spawnPiglin/spawnBlaze but renders as
// entity.PiglinBrute and runs PLAIN hostile combat (piglinBruteAiStep): acquire the nearest player + melee
// for ATTACK_DAMAGE (7.0). Attributes come from the piglin_brute supplier (NewEntity attaches by registry
// name): MAX_HEALTH 50 / MOVEMENT_SPEED 0.35 / ATTACK_DAMAGE 7 / FOLLOW_RANGE 12. NO gold neutrality, NO
// barter, NO zombify (piglinImmuneToZombification true). initSpawnHealth seeds health from MAX_HEALTH (50).
// The golden-axe mainhand visual is cite-deferred (no trivial equip seam here; ATTACK_DAMAGE 7 -- the
// observable maul -- is fully wired). Cite PiglinBrute(EntityType, Level) + PiglinBrute.createAttributes.
func (t *TickLoop) spawnPiglinBrute(x, y, z float64) *Entity {
	e := NewEntity(t.idAlloc.AllocID(), entity.PiglinBrute, x, y, z)
	e.isPiglin = true // AbstractPiglin subtype (shared piglin marks)
	// PiglinBrute does NOT override isImmuneToZombification (verified: no such method on PiglinBrute; the
	// AbstractPiglin default reads DATA_IMMUNE_TO_ZOMBIFICATION, false for a natural/dbg spawn). So a brute
	// off-nether DOES zombify like any piglin -- piglinImmuneToZombification stays false (default). It is
	// always HOSTILE (no gold neutrality) but still converts. Cite PiglinBrute (no isImmuneToZombification
	// override) + AbstractPiglin.isImmuneToZombification (DATA default false).
	_ = piglinBruteXpReward // PiglinBrute ctor xpReward 20 (cited; not modeled on *Entity yet)
	initSpawnHealth(e)                   // setHealth(getMaxHealth()) -> 50.0
	e.ai = &mobAI{}
	reseedMobAI(e.ai, e.id)
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.entities != nil {
		owner.entities.add(e)
	}
	return e
}

// piglinBruteAiStep drives one PiglinBrute (gated in tickAI on typ == entity.PiglinBrute.ID, AFTER
// serverAiStep): the ALWAYS-ON fight (acquire nearest player + melee 7.0). Cite PiglinBruteAi.initFightActivity.
func (t *TickLoop) piglinBruteAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	t.piglinBruteAcquireNearestPlayer(e)
	t.piglinBruteMeleeGoalTick(e)
	// super.customServerAiStep (AbstractPiglin): the off-nether zombification timer. PiglinBrute.
	// customServerAiStep ends with invokespecial AbstractPiglin.customServerAiStep, so a brute increments
	// timeInOverworld off-nether and converts to a ZombifiedPiglin at > 300. Reuses the piglin.go machinery.
	//	[VERIFIED javap PiglinBrute.customServerAiStep: ... invokespecial AbstractPiglin.customServerAiStep;
	//	 AbstractPiglin.customServerAiStep: isConverting() ? ++timeInOverworld : 0; if GT 300 finishConversion.]
	t.piglinBruteZombificationTick(e)
}

// piglinBruteZombificationTick routes the brute's zombification through the shared piglin.go timer
// (piglinZombificationTick): a brute is not immune, so off-nether it accumulates timeInOverworld and
// converts to a ZombifiedPiglin past 300. Cite AbstractPiglin.customServerAiStep tail.
func (t *TickLoop) piglinBruteZombificationTick(e *Entity) {
	t.piglinZombificationTick(e)
}

// piglinBruteTarget reads the brute current attack-target player, or nil. Tick-owned.
func (t *TickLoop) piglinBruteTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// piglinBruteAcquireNearestPlayer acquires the nearest LIVE player within FOLLOW_RANGE (12) WITHOUT the
// gold-armor neutrality filter (a PiglinBrute attacks EVERY player -- always hostile). NO RNG. Cite
// PiglinBruteAi.findNearestValidAttackTarget (no not-wearing-gold gate).
func (t *TickLoop) piglinBruteAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // FOLLOW_RANGE 12.0
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
			e.ai.attackTargetID = 0
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
	}
}

// piglinBruteMeleeGoalTick ports the FIGHT-activity melee (the MeleeAttackGoal adjacency shape): decrement
// the swing cooldown; on an in-range target with the cooldown elapsed, doHurtTarget 7.0 + reset; else
// pursue. NO RNG. Cite PiglinBruteAi.initFightActivity + MeleeAttackGoal.tick.
func (t *TickLoop) piglinBruteMeleeGoalTick(e *Entity) {
	if e.piglinAttackTime > 0 {
		e.piglinAttackTime--
	}
	target := t.piglinBruteTarget(e)
	if target == nil {
		return
	}
	d := distanceToSqrPlayer(target, e)
	if d <= piglinBruteMeleeRangeSqr {
		if e.piglinAttackTime <= 0 {
			e.piglinAttackTime = piglinBruteMeleeAttackTime
			t.piglinBruteDoHurtTarget(e, target)
		}
	} else {
		e.ai.setWantTargetMod(target.x, target.y, target.z, 1.0)
	}
}

// piglinBruteDoHurtTarget ports Mob.doHurtTarget: deal ATTACK_DAMAGE (7.0) to the player. NO RNG.
func (t *TickLoop) piglinBruteDoHurtTarget(e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // == 7.0
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg)
}

// illusionerAiStep ports the observable gameplay of Illusioner IllusionerBlindnessSpellGoal +
// Illusioner.aiStep (invisible while casting) -- a CITED collapse of the full SpellcasterUseSpellGoal state
// machine to its observable effects. While the illusioner has a target within FOLLOW_RANGE (18), a cast
// fires every getCastingInterval() (180) ticks applying BLINDNESS 400 to the target, and for the spellWarmup
// (20) after each cast the illusioner is INVISIBLE (INVISIBILITY on itself). The mirror-image clones are
// cite-deferred (no clone-spawn seam). Gated in tickAI on typ == entity.Illusioner.ID, AFTER serverAiStep
// (the .star goals ran). RNG-free (fixed-cadence timer). Cite Illusioner IllusionerBlindnessSpellGoal
// (getCastingInterval 180, spellWarmup 20, BLINDNESS 400) + Illusioner.aiStep (setInvisible while casting).
func (t *TickLoop) illusionerAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	target := t.illusionerTarget(e)
	if e.illusionerCastTicks > 0 {
		e.illusionerCastTicks-- // wind down the warmup-invisibility (the INVISIBILITY effect lapses with it)
	}
	if target == nil {
		e.illusionerBlindnessCooldown = 0 // no target -> the casting goal is inactive
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 18.0 acquire/cast bound
	if distanceToSqrPlayer(target, e) > followRange*followRange {
		e.illusionerBlindnessCooldown = 0
		return
	}
	if e.illusionerBlindnessCooldown > 0 {
		e.illusionerBlindnessCooldown--
		return
	}
	e.illusionerBlindnessCooldown = illusionerBlindnessInterval
	e.illusionerCastTicks = illusionerBlindnessWarmup
	t.addPlayerEffect(target, e.id, effectBlindness, illusionerBlindnessDuration, illusionerBlindnessAmplifier, 1.0)
	t.addEntityEffectWithSource(e, e.id, effectInvisibility, illusionerBlindnessWarmup, 0, 1.0)
}

// illusionerTarget reads the illusioner current attack-target player, or nil (acquired by the .star
// Go-native target goals). Tick-owned (mirrors blazeTarget).
func (t *TickLoop) illusionerTarget(e *Entity) *tickPlayer {
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
