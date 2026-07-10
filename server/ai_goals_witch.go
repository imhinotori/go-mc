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
// (no raids in v1 — the witch only ever faces a player here). The drinking-potion self-buff is
// cite-deferred.

import (
	"math"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// RangedAttackGoal constants (verified CFR — the witch's ctor args).
const (
	witchRangedSpeed       = 1.0
	witchAttackInterval    = 60   // Witch RangedAttackGoal(this, 1.0, 60, 10.0)
	witchAttackRadius      = 10.0 // → radiusSqr 100
	witchAttackRadiusSqr   = witchAttackRadius * witchAttackRadius
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
	hasLineOfSight := t.sensingHasLineOfSight(e, target) // real per-tick-cached raycast (C-4)
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
//
// The opening bytecode guard (offsets 0-7) is `if (isDrinkingPotion()) return;` -- a drinking witch
// never throws a splash potion, so the WITCH_THROW sound is also skipped in that case. The existing
// v1 redux did not include this guard, so the splash potion was spawning even mid-drink (a 1:1
// deviation). The guard closes both deviations at once: the splash spawn is suppressed AND the
// WITCH_THROW sound (played AFTER the splash spawn in the jar) cannot fire on a drinking witch.
//
//	[VERIFIED javap Witch.performRangedAttack offsets 0-7: isDrinkingPotion ifeq -> return.]
func (t *TickLoop) performWitchRangedAttack(e *Entity, target *tickPlayer, power float64) {
	// isDrinkingPotion() guard (bytecode 0-7): a drinking witch does not throw.
	if e.witchDrinking {
		return
	}
	r := mobRandom(e)

	// Aim: target.getEyeY() - 1.100000023841858 - getY() (targetMovement lead omitted -- v1 has no player
	// delta cache). target.getEyeY() is the PLAYER's real standing eye height (1.62 == getEyeY), NOT
	// height*0.85 (a player carries a custom 1.62 eye offset, not the 0.85 default). The 1.100000023841858
	// is the double-widened 1.1f literal the jar subtracts. VERIFIED javap Witch.performRangedAttack offsets
	// 30-38: target.getEyeY(); ldc2_w 1.100000023841858; dsub; getY(); dsub. Cite Witch.performRangedAttack.
	xd := target.x - e.x
	yd := (target.y + playerStandingEyeHeight - 1.100000023841858) - e.y
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

	// The ThrownSplashPotion spawns at shooter.getX(), shooter.getEyeY() - 0.10000000149011612, shooter.getZ()
	// (ThrowableItemProjectile(EntityType, LivingEntity, Level, ItemStack) ctor: getEyeY() minus the widened
	// 0.1f). getEyeY() == y + defaultEyeHeight(height) == y + (float)(height*0.85f). VERIFIED javap
	// ThrowableItemProjectile ctor: getX(); getEyeY(); ldc2_w 0.10000000149011612; dsub; getZ(); setPos.
	launchY := e.y + float64(float32(e.height)*0.85) - 0.10000000149011612
	t.spawnSplashPotion(e.id, e.x, launchY, e.z, vx, vy, vz, effects)

	// playSound(WITCH_THROW, x, y, z, HOSTILE, 1.0, 0.8 + nextFloat()*0.4) -- the throw's client feedback
	// (bytecode offsets 293-342). The pitch jitter nextFloat() draw is on the WITCH own stream (r), so it
	// MUST fire here in order (witch-gated, never the pig oracle) AFTER the splash spawn -- the
	// soundSeedGenerator.nextLong() analogue seed is a dedicated non-gameplay draw (rand.Int64) so it
	// never perturbs the witch stream. Broadcast to every player within getRange(1.0)==16 blocks. isSilent
	// is a v1 constant-false (no DATA_SILENT mob wired), so the sound always plays -- cited.
	//
	//	[VERIFIED javap Witch.performRangedAttack: isSilent ifeq -> ; level() ; aconst_null ; getX/getY/getZ ;
	//	 getstatic SoundEvents.WITCH_THROW ; getSoundSource() ; fconst_1 (volume 1.0) ; ldc 0.8f +
	//	 random.nextFloat()*0.4f (pitch) ; Level.playSound(this, x, y, z, sound, source, vol, pitch).
	//	 SoundEvents.WITCH_THROW id 1779 in data/registryid/soundevent.go.]
	throwPitch := r.nextFloat()*witchDrinkPitchJitter + witchDrinkPitchBase
	t.playSound(witchThrowSoundID, soundSourceHostile, e.x, e.y, e.z, witchDrinkSoundVolume, throwPitch, rand.Int64())
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

// witchDrinkSoundID / witchThrowSoundID / witchDrinkSoundVolume are the WITCH_DRINK / WITCH_THROW
// positional sounds Witch.aiStep / Witch.performRangedAttack play: SoundEvents.WITCH_DRINK
// ("entity.witch.drink", registry id 1777) on a self-drink and SoundEvents.WITCH_THROW
// ("entity.witch.throw", registry id 1779) on a thrown splash potion. Both at volume 1.0 on the witch's
// own SoundSource (Monster.getSoundSource() == HOSTILE). The pitch is a per-event jitter
// random.nextFloat()*0.4 + 0.8 drawn from the WITCH's per-mob stream (r) -- a witch-gated draw (never
// the pig oracle), which MUST fire in order right after setUsingItem(true) for the drink / after the
// splash spawn for the throw. The per-sound seed is a dedicated non-gameplay draw (the
// Level.soundSeedGenerator analogue), never the witch stream.
//
//	[VERIFIED javap Witch.aiStep: getX/getY/getZ ; getstatic SoundEvents.WITCH_DRINK ; getSoundSource() ;
//	 fconst_1 (volume 1.0) ; ldc 0.8f + random.nextFloat()*0.4f (pitch) ; Level.playSound(this, x,y,z,
//	 sound, source, vol, pitch). SoundEvents.WITCH_DRINK id 1777 in data/registryid/soundevent.go.
//	 javap Witch.performRangedAttack offsets 293-342: identical shape, sound = SoundEvents.WITCH_THROW
//	 (id 1779 in data/registryid/soundevent.go, "entity.witch.throw"); Monster.getSoundSource ->
//	 SoundSource.HOSTILE.]
const (
	witchDrinkSoundID     int32   = 1777 // SoundEvents.WITCH_DRINK ("entity.witch.drink")
	witchThrowSoundID     int32   = 1779 // SoundEvents.WITCH_THROW ("entity.witch.throw")
	witchDrinkSoundVolume float32 = 1.0  // Witch.aiStep / Witch.performRangedAttack playSound volume (fconst_1)
	witchDrinkPitchBase   float32 = 0.8  // 0.8f + nextFloat()*0.4f -> the [0.8,1.2) pitch jitter base
	witchDrinkPitchJitter float32 = 0.4  // the 0.4f jitter span
)

// witch self-drink draw gates (Witch.aiStep) + the drinking speed modifier (SPEED_MODIFIER_DRINKING).
const (
	witchWaterBreathingChance = 0.15   // rand < 0.15f (WATER_BREATHING rung)
	witchFireResistanceChance = 0.15   // rand < 0.15f (FIRE_RESISTANCE rung)
	witchHealingChance        = 0.05   // rand < 0.05f (HEALING rung)
	witchSwiftnessChance      = 0.5    // rand < 0.5f  (SWIFTNESS rung)
	witchSwiftnessDistSqr     = 121.0  // target.distanceToSqr(this) > 121.0
	witchIdleEventChance      = 7.5e-4 // rand < 7.5E-4f (broadcastEntityEvent 15 -- the idle WITCH-particle event)

	witchDrinkingModifierID = "minecraft:drinking" // SPEED_MODIFIER_DRINKING_ID ("drinking")
	witchDrinkingSpeedAmt   = -0.25                // SPEED_MODIFIER_DRINKING amount, ADD_VALUE
)

// entityEventWitchIdleParticles is the byte status Witch.aiStep broadcasts on the 7.5E-4 idle roll
// (Level.broadcastEntityEvent(this, (byte)15)). The client's Witch.handleEntityEvent(15) spawns the
// WITCH particle burst locally; the server only fires the EntityEvent. Cite Witch.aiStep / .handleEntityEvent.
//
//	[VERIFIED javap Witch.aiStep: `bipush 15; Level.broadcastEntityEvent(this, 15)`.]
const entityEventWitchIdleParticles byte = 15

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
			// playSound(WITCH_DRINK) at the witch position on its HOSTILE SoundSource, volume 1.0,
			// pitch 0.8f + random.nextFloat()*0.4f. The nextFloat() draw is on the WITCH own stream
			// (r), so it MUST fire here in order (witch-gated, never the pig oracle). The per-sound
			// seed is a dedicated non-gameplay draw (soundSeedGenerator.nextLong analogue) so it never
			// perturbs the witch stream. Broadcast to every player within getRange(1.0)==16 blocks.
			pitch := r.nextFloat()*witchDrinkPitchJitter + witchDrinkPitchBase
			t.playSound(witchDrinkSoundID, soundSourceHostile, e.x, e.y, e.z, witchDrinkSoundVolume, pitch, rand.Int64())
			t.witchAddDrinkingModifier(e)
		}
	}
	// broadcastEntityEvent(15) idle particle roll (Witch.aiStep tail, offset 484): draw the RNG (order
	// fidelity -- the draw MUST fire on the witch stream regardless of outcome), and on rand < 7.5E-4f
	// broadcast ClientboundEntityEvent status 15 to every tracking player. This is the FAITHFUL wire path
	// for the witch idle WITCH-particle burst: vanilla does NOT call ServerLevel.sendParticles here -- it
	// fires Level.broadcastEntityEvent(this, (byte)15), and the CLIENT's Witch.handleEntityEvent(15) spawns
	// the (10 + nextInt(35)) WITCH particles locally (gaussian offsets, all client-side). So the server's
	// job is exactly this EntityEvent, on the SAME encodeEntityEvent / broadcastToTrackers seam the death
	// poof (60), spawnAnim (20), wolf-tame (7/6) and in-love hearts (18) ride -- NOT a LevelParticles packet.
	//	[VERIFIED javap Witch.aiStep: `ldc_w 7.5E-4f; fcmpg; ifge; level(); aload_0; bipush 15;
	//	 Level.broadcastEntityEvent(this, 15)`. Witch.handleEntityEvent(15): loop i < 10 + random.nextInt(35)
	//	 -> Level.addParticle(ParticleTypes.WITCH, getX()+gauss*0.13, maxY+0.5+gauss*0.13, getZ()+gauss*0.13,
	//	 0,0,0). ServerLevel.broadcastEntityEvent -> ClientboundEntityEventPacket to tracking players.]
	if float64(r.nextFloat()) < witchIdleEventChance {
		t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventWitchIdleParticles))
	}
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

// witchHasActiveRaid ports Raider.hasActiveRaid(): getCurrentRaid() != null && getCurrentRaid().isActive().
// NOW A REAL QUERY (the RAID EVENT landed, raid.go/raids.go): it reads the witch's raid membership back-
// pointer (mobAI.currentRaid, set by raidJoinRaid when the witch spawns in a wave). A witch NOT in a raid
// has currentRaid==nil and stays inert (the healable-raider goal never acquires); a witch spawned into an
// ACTIVE raid reads true, so the NearestHealableRaiderTargetGoal proceeds past the hasActiveRaid gate.
// Cite Raider.hasActiveRaid.
func witchHasActiveRaid(e *Entity) bool {
	if e == nil || e.ai == nil || e.ai.currentRaid == nil {
		return false
	}
	return e.ai.currentRaid.isActive()
}

// canUse ports NearestHealableRaiderTargetGoal.canUse (VERIFIED CFR): cooldown/nextBoolean gate, then
// !hasActiveRaid() short-circuit, then findTarget() + return target != null. The nextBoolean() coin-flip
// IS drawn (faithful RNG order) whenever cooldown<=0. hasActiveRaid is NOW REAL (witchHasActiveRaid reads
// the witch's raid membership). findTarget acquires the nearest matching raider per the witch's subselector
// (Witch.registerGoals: `(target, level) -> hasActiveRaid() && !target.is(WITCH)` — a hurt NON-WITCH
// raider). In v1 the ONLY spawnable raider is the WITCH itself, so the `!target.is(WITCH)` filter excludes
// every candidate and findTarget acquires NOTHING — the goal is REAL (hasActiveRaid can be true) yet
// faithfully never targets, exactly matching the jar in a witch-only wave. Cite NearestHealableRaiderTargetGoal.canUse.
func (g *nearestHealableRaiderTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if g.cooldown > 0 || !mobRandom(e).nextBoolean() { // cooldown + coin-flip gate (DRAW: nextBoolean)
		return false
	}
	if !witchHasActiveRaid(e) { // !hasActiveRaid() — NOW REAL: false unless the witch is in an active raid
		return false
	}
	// findTarget(): the nearest hurt raider matching the subselector (!is(WITCH)) within range 500. In a
	// witch-only v1 wave there are no non-witch raiders, so target stays nil (return false). g.acquire sets
	// the witch's attack target when a real non-witch raider ever exists.
	return g.findHealTarget(t, e)
}

// findHealTarget ports NearestAttackableTargetGoal.findTarget for the witch's heal subselector: scan the
// witch's raid membership for the nearest ALIVE, HURT, NON-WITCH raider and (if found) set it as the
// witch's target (super.start's setTarget). Returns whether a target was acquired. In v1 this always
// returns false (no non-witch raiders exist) — the faithful witch-only-wave result — but it is a REAL scan
// over the live raid roster, not a stub. Cite NearestAttackableTargetGoal.findTarget + the witch subselector.
func (g *nearestHealableRaiderTargetGoal) findHealTarget(t *TickLoop, e *Entity) bool {
	if e.ai == nil || e.ai.currentRaid == nil {
		return false
	}
	raid := e.ai.currentRaid
	var best *Entity
	bestDistSqr := 500.0 * 500.0 // NearestAttackableTargetGoal followRange 500 (the ctor arg)
	for _, set := range raid.groupRaiderMap {
		for _, raider := range set {
			if raider.id == e.id || raider.dead || raider.health <= 0 {
				continue
			}
			// subselector: !target.is(WITCH). The acquiring mob e IS a witch (this goal only registers on the
			// witch), and every v1 raider is likewise a witch, so `raider.typ == e.typ` is the `is(WITCH)`
			// filter — it excludes every v1 raider (import-free: compares against the witch's own type id).
			if raider.typ == e.typ {
				continue
			}
			// heal target must be HURT (a full-health raider is not worth a REGENERATION splash) —
			// NearestAttackableTargetGoal itself has no hurt gate, but the witch only THROWS regen at a
			// hurt raider (performRangedAttack's target.getHealth() branch); acquiring the nearest is faithful.
			dx := raider.x - e.x
			dy := raider.y - e.y
			dz := raider.z - e.z
			d := dx*dx + dy*dy + dz*dz
			if d < bestDistSqr {
				best = raider
				bestDistSqr = d
			}
		}
	}
	if best == nil {
		return false
	}
	e.ai.attackTargetID = best.id // super.start() == setTarget(target)
	return true
}

// start ports NearestHealableRaiderTargetGoal.start: cooldown = reducedTickDelay(200); super.start().
// Unreachable in v1 (canUse never returns true), but ported for fidelity.
func (g *nearestHealableRaiderTargetGoal) start(_ *TickLoop, e *Entity) {
	g.cooldown = reducedTickDelay(healableRaiderCooldown)
	if e.ai != nil {
		// super.start() == NearestAttackableTargetGoal.start -> mob.setTarget(target). No live target in v1.
	}
}
