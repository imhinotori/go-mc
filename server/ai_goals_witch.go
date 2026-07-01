package server

// ai_goals_witch.go — MOB-HOST-07 (Task #9): the Witch's generic RangedAttackGoal + performRangedAttack
// (splash-potion throw), ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this
// session):
//
//   - net.minecraft.world.entity.ai.goal.RangedAttackGoal: flags {MOVE, LOOK}, requiresUpdateEveryTick.
//     Witch builds it as new RangedAttackGoal(this, 1.0, 60, 10.0) → speedModifier 1.0, interval fixed 60,
//     radius 10 → radiusSqr 100. tick: chase while out of range / seeTime<5, else stop; look; on attackTime
//     hitting 0 with LoS → performRangedAttack; re-arm attackTime = floor(dist*(max-min)+min) (min==max==60).
//   - Witch.performRangedAttack: the potion-selection ladder (HARMING default; SLOWNESS if dist≥8 & !slow;
//     POISON if health≥8 & !poison; WEAKNESS if dist≤3 & !weak & rand<0.25) → spawn a ThrownSplashPotion.
//
// v1 STUBS (cited): line-of-sight always-true (no sensing); the Raider heal/regeneration branch is deferred
// (no raids in v1 — the witch only ever faces a player here). The WITCH_THROW sound + the drinking-potion
// self-buff are cite-deferred.

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// RangedAttackGoal constants (verified CFR — the witch's ctor args).
const (
	witchRangedSpeed      = 1.0
	witchAttackInterval   = 60   // Witch RangedAttackGoal(this, 1.0, 60, 10.0)
	witchAttackRadius     = 10.0 // → radiusSqr 100
	witchAttackRadiusSqr  = witchAttackRadius * witchAttackRadius
	witchLaunchUncertainty = 8.0 // performRangedAttack uncertainty arg
)

// rangedAttackGoal is the ported generic RangedAttackGoal (net.minecraft.world.entity.ai.goal.RangedAttackGoal).
type rangedAttackGoal struct {
	baseGoal
	attackTime int // RangedAttackGoal.attackTime (start -1)
	seeTime    int
}

func newWitchRangedAttackGoal() *rangedAttackGoal {
	return &rangedAttackGoal{baseGoal: newBaseGoal(flagMove | flagLook), attackTime: -1}
}

func (g *rangedAttackGoal) requiresUpdateEveryTick() bool { return true }

// canUse: target != null && target.isAlive().
func (g *rangedAttackGoal) canUse(t *TickLoop, e *Entity) bool {
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	p := t.playerByEntityID(id)
	return p != nil && !p.dead
}

// canContinueToUse: canUse || (target alive && !navigation.isDone()).
func (g *rangedAttackGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	return g.canUse(t, e) || e.ai.navigation.active()
}

func (g *rangedAttackGoal) start(t *TickLoop, e *Entity) {}

func (g *rangedAttackGoal) stop(t *TickLoop, e *Entity) {
	g.seeTime = 0
	g.attackTime = -1
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
}

// tick is the port of RangedAttackGoal.tick.
func (g *rangedAttackGoal) tick(t *TickLoop, e *Entity) {
	if e.ai == nil {
		return
	}
	id := mobTarget(e)
	if id == 0 {
		return
	}
	target := t.playerByEntityID(id)
	if target == nil {
		return
	}
	targetDistSqr := distanceToSqrPlayer(target, e)
	hasLineOfSight := true // v1 always-true stub
	if hasLineOfSight {
		g.seeTime++
	} else {
		g.seeTime = 0
	}

	if targetDistSqr > witchAttackRadiusSqr || g.seeTime < 5 {
		getSpeed := e.getAttributeValue(attribute.MovementSpeed) * witchRangedSpeed
		e.ai.setWantTargetSpeed(target.x, target.y, target.z, getSpeed) // navigation.moveTo(target, speed)
	} else {
		e.ai.clearWantTarget() // navigation.stop()
	}

	// lookAt(target, 30, 30): head-only turn.
	yRotD := yawTowardDeg(target.x-e.x, target.z-e.z)
	e.headYaw = rotlerpDeg(e.headYaw, yRotD, meleeLookMaxYawStep)

	g.attackTime--
	if g.attackTime == 0 {
		if !hasLineOfSight {
			return
		}
		dist := math.Sqrt(targetDistSqr) / witchAttackRadius
		power := dist
		if power < 0.1 {
			power = 0.1
		} else if power > 1.0 {
			power = 1.0
		}
		t.performWitchRangedAttack(e, target, power)
		// attackTime = floor(dist*(max-min)+min); min==max==60 → 60.
		g.attackTime = witchAttackInterval
	} else if g.attackTime < 0 {
		// attackTime = floor(lerp(sqrt(distSqr)/radius, min, max)); min==max==60 → 60.
		g.attackTime = witchAttackInterval
	}
}

// performWitchRangedAttack is the port of Witch.performRangedAttack: choose a harmful potion by the vanilla
// ladder and spawn a ThrownSplashPotion at the target. The Raider heal/regeneration branch is deferred
// (v1 target is always a player). Cite Witch.performRangedAttack + Projectile.getMovementToShoot.
func (t *TickLoop) performWitchRangedAttack(e *Entity, target *tickPlayer, power float64) {
	r := mobRandom(e)

	// Aim: target.getEyeY() - 1.1 - getY() (targetMovement lead omitted — v1 has no player delta cache).
	xd := target.x - e.x
	yd := (target.y + float64(playerHeight)*0.85 - 1.1) - e.y
	zd := target.z - e.z
	dist := math.Sqrt(xd*xd + zd*zd)

	// Potion-selection ladder (HARMING default; SLOWNESS if dist≥8 & !slow; POISON if health≥8 & !poison;
	// WEAKNESS if dist≤3 & !weak & rand<0.25). The RNG draw (WEAKNESS gate) is on the witch's own stream.
	effects := witchPotionHarming()
	if dist >= 8.0 && !playerHasEffect(target, effectSlowness) {
		effects = witchPotionSlowness()
	} else if target.health >= 8.0 && !playerHasEffect(target, effectPoison) {
		effects = witchPotionPoison()
	} else if dist <= 3.0 && !playerHasEffect(target, effectWeakness) && float64(r.nextFloat()) < 0.25 {
		effects = witchPotionWeakness()
	}

	// Launch: normalize(xd, yd+dist*0.2, zd) + triangle spread * (dist≤2?0.45:0.75).
	pow := 0.75
	if dist <= 2.0 {
		pow = 0.45
	}
	vx, vy, vz := normalizeVec3(xd, yd+dist*0.2, zd)
	spread := 0.0172275 * witchLaunchUncertainty
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	vx *= pow
	vy *= pow
	vz *= pow

	launchY := e.y + e.height*0.85
	t.spawnSplashPotion(e.id, e.x, launchY, e.z, vx, vy, vz, effects)
}

// witchPotion* build the effect payloads for the witch's potions (the base variants, amp 0, jar durations).
func witchPotionHarming() []splashEffect {
	return []splashEffect{{id: effectInstantDamage, duration: 1, amplifier: 0}} // Potions.HARMING
}
func witchPotionPoison() []splashEffect {
	return []splashEffect{{id: effectPoison, duration: 900, amplifier: 0}} // Potions.POISON
}
func witchPotionSlowness() []splashEffect {
	return []splashEffect{{id: effectSlowness, duration: 1800, amplifier: 0}} // Potions.SLOWNESS
}
func witchPotionWeakness() []splashEffect {
	return []splashEffect{{id: effectWeakness, duration: 1800, amplifier: 0}} // Potions.WEAKNESS
}

// ============================================================================================
// Witch.aiStep — the self-drink potion buff branch (MOB-HOST-07 heal branch), ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this session). This is the PER-TYPE aiStep hook
// (sibling of creeperAiStep/foxAiStep), called from tickAI for a live witch AFTER serverAiStep — it runs
// INDEPENDENTLY of the goals (Mob.aiStep -> customServerAiStep), so it fires every tick for a live witch.
//
// Witch.aiStep (VERIFIED CFR, server branch !isClientSide && isAlive):
//   healRaidersGoal.decrementCooldown(); setCanAttack(cooldown<=0)   <-- raid-cooldown gate: DEFERRED (no raid)
//   if isDrinkingPotion():
//     if --usingTime <= 0: setUsingItem(false); mainhand -> EMPTY; forEachEffect(addEffect, durScale);
//         gameEvent(DRINK); MOVEMENT_SPEED.removeModifier(drinking)
//   else:
//     potion = null
//     if rand<0.15 && isEyeInFluid(WATER) && !hasEffect(WATER_BREATHING): potion = WATER_BREATHING
//     elif rand<0.15 && (isOnFire() || lastDamageSource is IS_FIRE) && !hasEffect(FIRE_RESISTANCE): FIRE_RESISTANCE
//     elif rand<0.05 && getHealth()<getMaxHealth(): HEALING
//     elif rand<0.5 && getTarget()!=null && !hasEffect(SPEED) && target.distanceToSqr(this)>121.0: SWIFTNESS
//     if potion != null: mainhand = POTION(potion); usingTime = useDuration(=32); setUsingItem(true);
//         playSound(WITCH_DRINK) [deferred]; speed.removeModifier(drinking); speed.addTransientModifier(drinking)
//   if rand<7.5E-4: broadcastEntityEvent(15)   <-- the idle-particle event: cite-deferred (client visual)
//   super.aiStep()
//
// The RNG draw ORDER is EXACT (each ladder rung draws nextFloat() on the witch's per-mob stream, then the
// 7.5E-4 idle roll). The witch is NOT the pig oracle, so these draws are witch-gated (zero draws for any
// non-witch). The four self-potions apply to the witch ITSELF via addEntityEffect (the entity-side
// mobEffects map, entity.go) — HEALING is instant self-heal; WATER_BREATHING/FIRE_RESISTANCE/SWIFTNESS/
// (and the raid REGENERATION path) are duration effects ticked by tickMobEffects.
//
// v1 REDUCTIONS (cited): the raid heal-cooldown gate (healRaidersGoal.decrementCooldown / setCanAttack) is
// DEFERRED with the raid subsystem — the witch always attacks (cooldown treated <=0, its default). The
// WITCH_DRINK sound + the 7.5E-4 idle-particle broadcast are client-visual, cite-deferred. isEyeInFluid is
// the eye-Y water sample (breath.go pattern); getItemBySlot MAINHAND book-keeping is reduced to the
// drink state machine (witchDrinking/witchUsingTime) since a v1 witch carries no real mainhand inventory —
// the observable (the drink delay + the applied self-buff) is faithful.

// witchPotionUseDuration is the potion drink time in ticks: Item.getUseDuration -> Consumable.consumeTicks
// == (int)(DEFAULT_CONSUME_SECONDS 1.6f * 20) == 32. Cite Consumable.DEFAULT_CONSUME_SECONDS / consumeTicks.
const witchPotionUseDuration = 32

// witch self-drink draw gates (Witch.aiStep) + the drinking speed modifier (SPEED_MODIFIER_DRINKING).
const (
	witchWaterBreathingChance = 0.15   // rand < 0.15f (WATER_BREATHING rung)
	witchFireResistanceChance = 0.15   // rand < 0.15f (FIRE_RESISTANCE rung)
	witchHealingChance        = 0.05   // rand < 0.05f (HEALING rung)
	witchSwiftnessChance      = 0.5    // rand < 0.5f  (SWIFTNESS rung)
	witchSwiftnessDistSqr     = 121.0  // target.distanceToSqr(this) > 121.0
	witchIdleEventChance      = 7.5e-4 // rand < 7.5E-4f (broadcastEntityEvent 15 — deferred visual)

	witchDrinkingModifierID = "minecraft:drinking" // SPEED_MODIFIER_DRINKING_ID ("drinking")
	witchDrinkingSpeedAmt   = -0.25                // SPEED_MODIFIER_DRINKING amount, ADD_VALUE
)

// witch self-buff potion payloads (Potions.*, VERIFIED CFR alchemy.Potions).
const (
	effectWaterBreathing = "minecraft:water_breathing" // Potions.WATER_BREATHING (WATER_BREATHING, 3600)
	effectFireResistance = "minecraft:fire_resistance" // Potions.FIRE_RESISTANCE (FIRE_RESISTANCE, 3600)
	effectInstantHealth  = "minecraft:instant_health"  // Potions.HEALING (INSTANT_HEALTH, 1)
	effectSpeed          = "minecraft:speed"           // Potions.SWIFTNESS (SPEED, 3600)
	effectRegeneration   = "minecraft:regeneration"    // Potions.REGENERATION (REGENERATION, 900) — raid path
)

// witchAiStep ports Witch.aiStep's self-drink branch. Called from tickAI for a live witch AFTER
// serverAiStep (witch-gated in tick_phases.go). Advances the drink countdown, or (when idle) rolls the
// potion-selection ladder and starts a drink.
func (t *TickLoop) witchAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	r := mobRandom(e)
	// The raid heal-cooldown gate (healRaidersGoal.decrementCooldown / attackPlayersGoal.setCanAttack) is
	// DEFERRED with the raid subsystem (cooldown treated <=0 — the witch always attacks).
	if e.witchDrinking {
		e.witchUsingTime--
		if e.witchUsingTime <= 0 {
			e.witchDrinking = false
			// mainhand -> EMPTY (reduced: no real inventory) + forEachEffect(addEffect, durScale=1.0): apply
			// the drunk potion's effects to the witch itself. The pending potion id is stored on the Entity.
			t.witchFinishDrink(e)
			// gameEvent(DRINK): cite-deferred (no game-event bus). Remove the drinking speed modifier.
			t.witchRemoveDrinkingModifier(e)
		}
	} else {
		potion := ""
		effectID := ""
		duration := 0
		switch {
		case float64(r.nextFloat()) < witchWaterBreathingChance && t.witchEyeInWater(e) && !entityHasEffect(e, effectWaterBreathing):
			potion, effectID, duration = "water_breathing", effectWaterBreathing, 3600
		case float64(r.nextFloat()) < witchFireResistanceChance && (e.remainingFireTicks > 0 || (e.hasLastDamage && e.lastDamageSource.is("is_fire"))) && !entityHasEffect(e, effectFireResistance):
			potion, effectID, duration = "fire_resistance", effectFireResistance, 3600
		case float64(r.nextFloat()) < witchHealingChance && e.health < float32(e.getAttributeValue(attribute.MaxHealth)):
			potion, effectID, duration = "healing", effectInstantHealth, 1
		case float64(r.nextFloat()) < witchSwiftnessChance && t.witchTargetFartherThan(e, witchSwiftnessDistSqr) && !entityHasEffect(e, effectSpeed):
			potion, effectID, duration = "swiftness", effectSpeed, 3600
		}
		if potion != "" {
			// Start the drink: seed usingTime = useDuration, set the using-item flag, attach the drinking
			// speed modifier (removeModifier first — vanilla removes the stale one before re-adding).
			e.witchUsingTime = witchPotionUseDuration
			e.witchDrinking = true
			e.witchDrinkPending = effectID
			e.witchDrinkPendingDur = duration
			// playSound(WITCH_DRINK): cite-deferred (client sound).
			t.witchAddDrinkingModifier(e)
		}
	}
	// broadcastEntityEvent(15) idle particle roll: draw the RNG to keep the stream faithful, but the
	// particle broadcast is a client visual (cite-deferred). The draw MUST happen (order fidelity).
	_ = float64(r.nextFloat()) < witchIdleEventChance
	// super.aiStep() (Mob/PathfinderMob aiStep) is driven by the shared serverAiStep path already.
}

// witchFinishDrink applies the just-finished drink's potion effect to the witch (potion.forEachEffect(
// this::addEffect, 1.0)). HEALING is instant (INSTANT_HEALTH self-heal); the others are duration effects.
func (t *TickLoop) witchFinishDrink(e *Entity) {
	id := e.witchDrinkPending
	dur := e.witchDrinkPendingDur
	e.witchDrinkPending = ""
	e.witchDrinkPendingDur = 0
	if id == "" {
		return
	}
	t.addEntityEffect(e, id, dur, 0)
}

// witchEyeInWater ports Witch.aiStep's isEyeInFluid(FluidTags.WATER): sample the fluid at the witch's EYE
// (y + eyeHeight ~ height*0.85, the same eye-Y the launch uses), not the feet. A nil world (test loop) is
// treated as not-in-water. Cite Entity.isEyeInFluid(WATER) (breath.go eyeInWater pattern).
func (t *TickLoop) witchEyeInWater(e *Entity) bool {
	if t.world() == nil {
		return false
	}
	eyeY := e.y + float64(e.height)*0.85
	return t.fluidAt(pk.Position{X: int(math.Floor(e.x)), Y: int(math.Floor(eyeY)), Z: int(math.Floor(e.z))}).isWater
}

// witchTargetFartherThan ports the SWIFTNESS rung's getTarget()!=null && target.distanceToSqr(this)>d:
// there is a live target AND it is farther than d (121.0) blocks-squared away. The target may be a player.
func (t *TickLoop) witchTargetFartherThan(e *Entity, d float64) bool {
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	if p := t.playerByEntityID(id); p != nil && !p.dead {
		return distanceToSqrPlayer(p, e) > d
	}
	if other, ok := t.cur().entities.get(id); ok && !other.dead && other.isAlive() {
		dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
		return dx*dx+dy*dy+dz*dz > d
	}
	return false
}

// witchAddDrinkingModifier ports speed.removeModifier(drinking); speed.addTransientModifier(drinking):
// attach the -0.25 ADD_VALUE MOVEMENT_SPEED "drinking" modifier while the witch drinks (removing any
// stale one first — vanilla removes before re-adding to avoid the duplicate-id reject).
func (t *TickLoop) witchAddDrinkingModifier(e *Entity) {
	if e.attributes == nil {
		return
	}
	inst := e.attributes.GetInstance(attribute.MovementSpeed.Name())
	if inst == nil {
		return
	}
	inst.RemoveModifier(witchDrinkingModifierID)
	inst.AddTransientModifier(attribute.AttributeModifier{
		ID:        witchDrinkingModifierID,
		Amount:    witchDrinkingSpeedAmt,
		Operation: attribute.AddValue,
	})
}

// witchRemoveDrinkingModifier ports MOVEMENT_SPEED.removeModifier(SPEED_MODIFIER_DRINKING.id()) on drink
// finish: detach the drinking slowdown.
func (t *TickLoop) witchRemoveDrinkingModifier(e *Entity) {
	if e.attributes == nil {
		return
	}
	if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
		inst.RemoveModifier(witchDrinkingModifierID)
	}
}

// ============================================================================================
// NearestHealableRaiderTargetGoal — the witch's raid-heal TARGET goal STRUCTURE (targetSelector @2),
// ported 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.entity.ai.goal.target.
// NearestHealableRaiderTargetGoal, CFR this session). It extends NearestAttackableTargetGoal with a
// cooldown + a nextBoolean gate + a hasActiveRaid gate, and (when it acquires) makes the witch throw
// HEALING/REGENERATION at a hurt fellow raider (Witch.performRangedAttack's `target instanceof Raider`
// branch).
//
// NearestHealableRaiderTargetGoal.canUse (VERIFIED CFR):
//   if (cooldown > 0 || !mob.getRandom().nextBoolean()) return false;   // cooldown + coin-flip gate
//   if (!((Raider)mob).hasActiveRaid()) return false;                    // must be in an active raid
//   findTarget(); return target != null;                                // acquire the hurt raider
// start: cooldown = reducedTickDelay(200); super.start(). ctor super(..., 500 randomInterval, ...).
//
// INERT IN v1 (cite-deferred, NOT silently dropped): there is NO raid subsystem, so hasActiveRaid() is a
// cited constant-false — the goal is STRUCTURALLY present (it registers, arbitrates for the TARGET flag,
// and draws its nextBoolean coin-flip faithfully every eligible tick so the witch's RNG stream order is
// exact) but NEVER acquires a target, because (a) hasActiveRaid()==false short-circuits before findTarget,
// and (b) there are no other raiders to heal in v1. It becomes live when the raid EVENT lands (the
// hasActiveRaid read + the Raider heal-throw branch already ported inertly in performWitchRangedAttack's
// deferral). The healRaidersGoal.decrementCooldown() / attackPlayersGoal.setCanAttack coupling in
// Witch.aiStep is likewise raid-deferred (the witch always attacks). Cite Witch.registerGoals
// targetSelector @2 + NearestHealableRaiderTargetGoal.

// healableRaiderCooldown is NearestHealableRaiderTargetGoal.start's cooldown = reducedTickDelay(200).
const healableRaiderCooldown = 200

// nearestHealableRaiderTargetGoal is the ported NearestHealableRaiderTargetGoal STRUCTURE. It holds the
// {TARGET} flag (from its NearestAttackableTargetGoal base). cooldown starts 0.
type nearestHealableRaiderTargetGoal struct {
	baseGoal
	cooldown int
}

func newNearestHealableRaiderTargetGoal() *nearestHealableRaiderTargetGoal {
	return &nearestHealableRaiderTargetGoal{baseGoal: newBaseGoal(flagTarget)}
}

// witchHasActiveRaid ports Raider.hasActiveRaid() — CITED CONSTANT-FALSE (no raid subsystem in v1). The
// goal short-circuits on it before findTarget, so the goal is inert. UPGRADE PATH: read the witch's raid
// membership once the raid EVENT lands.
func witchHasActiveRaid(_ *Entity) bool { return false }

// canUse ports NearestHealableRaiderTargetGoal.canUse. The nextBoolean() coin-flip IS drawn (faithful RNG
// order) whenever cooldown<=0; the hasActiveRaid gate then keeps the goal inert in v1.
func (g *nearestHealableRaiderTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if g.cooldown > 0 || !mobRandom(e).nextBoolean() { // cooldown + coin-flip gate (DRAW: nextBoolean)
		return false
	}
	if !witchHasActiveRaid(e) { // !hasActiveRaid() — cited constant-false: the goal never proceeds in v1
		return false
	}
	// findTarget() + return target != null: unreachable in v1 (no active raid). When the raid subsystem
	// lands, this acquires the hurt fellow raider (the Raider heal-throw branch in performWitchRangedAttack).
	return false
}

// start ports NearestHealableRaiderTargetGoal.start: cooldown = reducedTickDelay(200); super.start().
// Unreachable in v1 (canUse never returns true), but ported for fidelity.
func (g *nearestHealableRaiderTargetGoal) start(_ *TickLoop, e *Entity) {
	g.cooldown = reducedTickDelay(healableRaiderCooldown)
	if e.ai != nil {
		// super.start() == NearestAttackableTargetGoal.start -> mob.setTarget(target). No live target in v1.
	}
}
