// water_mobs.go -- the eight AQUATIC mobs, a 1:1 port from the unobfuscated 26.2 jar (protocol 776):
// Squid + GlowSquid, Cod/Salmon/Pufferfish/TropicalFish, Dolphin, and Tadpole. All eight breathe
// underwater (EntityTypeTags.CAN_BREATHE_UNDER_WATER, wired in breath_mob.go) and INVERT the land-mob
// breathing: a WaterAnimal DROWNS ON LAND (WaterAnimal.handleAirSupply drains air out of water ->
// DROWN 2.0). Verified method-for-method against temp/cache/26.2-inner.jar via javap -c -p this session.
//
// VANILLA ATTRIBUTES (verified javap createAttributes; suppliers in level/attribute/defaults.go):
//   Squid/GlowSquid: Mob.createMobAttributes + MAX_HEALTH 10.0 (GlowSquid inherits Squid unchanged).
//   Cod/Salmon/Pufferfish/TropicalFish: AbstractFish.createAttributes = Mob.createMobAttributes + MAX_HEALTH 3.0.
//   Dolphin: Mob.createMobAttributes + MAX_HEALTH 10.0 + MOVEMENT_SPEED 1.2000000476837158 + ATTACK_DAMAGE 3.0.
//   Tadpole: Animal.createAnimalAttributes + MOVEMENT_SPEED 1.0 + MAX_HEALTH 6.0.
//
// SIGNATURE BEHAVIORS (per-type-gated in tickAI AFTER serverAiStep): Squid.aiStep tentacle accumulator;
// Pufferfish puff 0->1->2 / deflate 2->1->0 + sting (1+puffState dmg + POISON 60*puffState); Dolphin.tick
// MOISTNESS 2400 in water / -1 on land / dryOut 1.0 at <=0; Tadpole.aiStep age++ -> Frog at 24000;
// TropicalFish packed DATA_ID_TYPE_VARIANT (DEFAULT KOB/WHITE/WHITE packs to 0).
//
// PORTED GOALS: DolphinSwimWithPlayerGoal (@2) grants a swimming nearby player DOLPHINS_GRACE and trails
// them (Dolphin.registerGoals + DolphinSwimWithPlayerGoal, verified this session); FollowFlockLeaderGoal
// (@5) makes a leaderless Cod/Salmon/TropicalFish join / lead a nearby same-type school (AbstractSchoolingFish
// .registerGoals + FollowFlockLeaderGoal, verified this session). Pufferfish is NOT a schooling fish and is
// EXEMPT (no flock goal). The Pufferfish puff goal IS ported.
//
// v1 STUBS (cited): the remaining specialized swim navigation goals (FishSwimGoal/SquidRandomMovementGoal/
// RandomSwimmingGoal/DolphinJumpGoal/DolphinSwimToTreasureGoal/PlayWithItemsGoal/BreathAirGoal/TryFindWaterGoal)
// are DEFERRED; this port stands them in with the classic Float/Panic/Stroll/Look set over the canFloat swim
// navigation (mirroring newFrogAI/newAxolotlAI). The Dolphin treasure-find (needs structure-locate), the
// DolphinJumpGoal breach (needs the pathfinding-fling), the TropicalFish 2-pattern client render, and the
// GlowSquid glow (needs the glowing effect) are DEFERRED behind cited stubs = vanilla default. Attributes +
// drown-on-land inversion + the listed signature per-tick behaviors are EXACT.

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
)

// Pufferfish PUFF_STATE ids (Pufferfish.STATE_SMALL/STATE_MID/STATE_FULL; DEFAULT_PUFF_STATE small).
const (
	pufferStateSmall = 0 // STATE_SMALL (DEFAULT_PUFF_STATE): the deflated fish
	pufferStateMid   = 1 // STATE_MID: half-inflated
	pufferStateFull  = 2 // STATE_FULL: fully puffed
)

// Water-mob constants (VERIFIED javap this session).
const (
	// squidTentacleSpeedScale: Squid.aiStep tentacleSpeed re-roll (1.0/(nextFloat()+1.0))*0.2f. Cite Squid.aiStep.
	squidTentacleSpeedScale float32 = 0.2
	// squidTwoPi: Squid.aiStep tentacle wrap threshold 6.2831855f (== 2*PI). Cite Squid.aiStep.
	squidTwoPi float32 = 6.2831855
	// squidInkChanceBound: Squid.aiStep 1-in-10 tentacleSpeed re-roll gate (nextInt(10)==0). Cite Squid.aiStep.
	squidInkChanceBound = 10
	// pufferInflateThreshold: Pufferfish.tick inflate step (state==1 && inflateCounter>40 -> state 2, bipush 40).
	pufferInflateThreshold = 40
	// pufferDeflateMidThreshold/pufferDeflateSmallThreshold: Pufferfish.tick deflate (>60 -> 1, >100 -> 0).
	pufferDeflateMidThreshold   = 60
	pufferDeflateSmallThreshold = 100
	// pufferStingPoisonPerState: Pufferfish.touch POISON duration 60*puffState (bipush 60). Cite Pufferfish.touch.
	pufferStingPoisonPerState = 60
	// dolphinFullMoistness: Dolphin TOTAL_MOISTNESS_LEVEL 2400 (sipush 2400). Cite Dolphin.tick.
	dolphinFullMoistness = 2400
	// dolphinDryOutDamage: Dolphin.tick hurt(dryOut(), 1.0F) at moistness<=0 (fconst_1). Cite Dolphin.tick.
	dolphinDryOutDamage float32 = 1.0
	// tadpoleTicksToBeFrog: Tadpole.ticksToBeFrog == Math.abs(-24000) == 24000. Cite Tadpole.clinit + setAge.
	tadpoleTicksToBeFrog = 24000
	// tropicalDefaultVariant: TropicalFish DEFAULT_VARIANT (KOB/WHITE/WHITE) packs to 0. Cite TropicalFish.packVariant.
	tropicalDefaultVariant = 0
	// waterMobStrollSpeed/waterMobLookDistance: bounded visibly-alive stroll/look paces (swim goals DEFERRED).
	waterMobStrollSpeed  = 1.0
	waterMobLookDistance = 8.0
	// dolphinMovementSpeed: Dolphin.createAttributes MOVEMENT_SPEED 1.2000000476837158. Cite Dolphin.createAttributes.
	dolphinMovementSpeed = 1.2000000476837158
	// tadpoleMovementSpeed: Tadpole.createAttributes MOVEMENT_SPEED 1.0 (dconst_1). Cite Tadpole.createAttributes.
	tadpoleMovementSpeed = 1.0
	// dolphinSwimWithPlayerSpeed: Dolphin.registerGoals addGoal(2, new DolphinSwimWithPlayerGoal(this, 4.0))
	// -- the goal's speedModifier (ldc2_w double 4.0d). Cite Dolphin.registerGoals.
	dolphinSwimWithPlayerSpeed = 4.0
	// dolphinSwimTargetRange: SWIM_WITH_PLAYER_TARGETING = forNonCombat().range(10.0).ignoreLineOfSight()
	// -- the 10-block nearest-swimming-player scan radius. Cite Dolphin.<clinit> SWIM_WITH_PLAYER_TARGETING.
	dolphinSwimTargetRange = 10.0
	// dolphinGraceDuration: MobEffectInstance(DOLPHINS_GRACE, 100) -- the Dolphin's Grace duration in ticks
	// (bipush 100), applied on start() and re-applied 1-in-6 per tick while the player swims. Cite
	// DolphinSwimWithPlayerGoal.start + .tick.
	dolphinGraceDuration = 100
	// dolphinFollowRerollBound: DolphinSwimWithPlayerGoal.tick re-grace gate level.getRandom().nextInt(6)==0
	// (bipush 6). Cite DolphinSwimWithPlayerGoal.tick.
	dolphinFollowRerollBound = 6
	// dolphinSwimContinueDistSq: DolphinSwimWithPlayerGoal.canContinueToUse distanceToSqr < 256.0 (ldc2_w
	// double 256.0d) -- stop following past 16 blocks. Cite DolphinSwimWithPlayerGoal.canContinueToUse.
	dolphinSwimContinueDistSq = 256.0
	// dolphinSwimStopDistSq: DolphinSwimWithPlayerGoal.tick distanceToSqr < 6.25 (ldc2_w double 6.25d) ->
	// navigation.stop() (already at the player, hold); else moveTo. Cite DolphinSwimWithPlayerGoal.tick.
	dolphinSwimStopDistSq = 6.25
)

// effectDolphinsGrace is MobEffects.DOLPHINS_GRACE = register("dolphins_grace", new MobEffect(BENEFICIAL,
// color)) -- a PLAIN MobEffect with NO attribute modifier (the swim-speed boost is applied by the dedicated
// getWaterSlowdownFactor check in LivingEntity.travelInFluid, not by an attribute modifier on the effect),
// so addPlayerEffect inserts it as a bare duration effect exactly like conduit_power. Declared here (mob-
// owned) since it is a water-mob-only effect id, consistent with the existing effect-id const convention in
// mob_effect.go. VERIFIED javap MobEffects.<clinit>: ldc "dolphins_grace"; new MobEffect; MobEffectCategory
// .BENEFICIAL. Cite MobEffects.DOLPHINS_GRACE.
const effectDolphinsGrace = "minecraft:dolphins_grace"

// newWaterMobAI builds the bounded passive swim AI shared by every water mob: the classic Float/Panic/
// Stroll/Look set over the canFloat swim navigation (the specialized swim goals are DEFERRED, file header),
// mirroring newFrogAI/newAxolotlAI shape. movementSpeed is the type MOVEMENT_SPEED (0 for a fish/squid, a
// dolphin 1.2, a tadpole 1.0). Cite AbstractFish/Squid/Dolphin.registerGoals (swim-goal deferral note).
func newWaterMobAI(movementSpeed float64) *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * movementSpeed // seed with MOVEMENT_SPEED (0 for a fish/squid)
	m.navigation.canFloat = true                        // swim navigation: WATER is a standable node
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(waterMobStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(waterMobLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnSquid creates a Squid or GlowSquid (MAX_HEALTH 10.0) with the bounded swim AI. glow selects the
// GlowSquid subtype (inherits Squid supplier + aiStep; its glow DATA is DEFERRED). initSpawnHealth seeds
// health from MAX_HEALTH (10.0). Cite Squid/GlowSquid.createAttributes.
func (t *TickLoop) spawnSquid(x, y, z float64, glow bool) *Entity {
	typ := entity.Squid
	if glow {
		typ = entity.GlowSquid // GlowSquid extends Squid (no createAttributes override): MAX_HEALTH 10.0
	}
	s := NewEntity(t.idAlloc.AllocID(), typ, x, y, z)
	s.isWaterMob = true
	s.isSquid = true
	initSpawnHealth(s) // setHealth(getMaxHealth()) -> 10.0
	s.ai = newWaterMobAI(0)
	reseedMobAI(s.ai, s.id)
	owner := t.regionForEntity(s)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(s)
	return s
}

// spawnFish creates a Cod/Salmon/Pufferfish/TropicalFish (shared AbstractFish MAX_HEALTH 3.0) with the
// bounded swim AI. typ selects the species (its registry name resolves abstractFishSupplier). A Pufferfish
// starts STATE_SMALL (pufferfishAiStep drives the puff machine); a TropicalFish carries the packed DEFAULT
// variant (0). initSpawnHealth seeds health from MAX_HEALTH (3.0). Cite AbstractFish.createAttributes.
func (t *TickLoop) spawnFish(typ entity.Entity, x, y, z float64) *Entity {
	f := NewEntity(t.idAlloc.AllocID(), typ, x, y, z)
	f.isWaterMob = true
	if typ.ID == entity.Pufferfish.ID {
		f.isPufferfish = true
		f.pufferPuffState = pufferStateSmall // DEFAULT_PUFF_STATE
	}
	if typ.ID == entity.TropicalFish.ID {
		// TropicalFish.finalizeSpawn variant roll (drawn on level.getRandom()); DEFAULT until rolled.
		f.tropicalVariant = tropicalDefaultVariant // DEFAULT_VARIANT (KOB/WHITE/WHITE -> 0); 2-pattern render DEFERRED
		f.tropicalVariant = t.tropicalFishFinalizeVariant()
	}
	initSpawnHealth(f) // setHealth(getMaxHealth()) -> 3.0
	f.ai = newWaterMobAI(0)
	if isSchoolingFish(typ.ID) {
		f.schoolSize = 1 // AbstractSchoolingFish ctor: this.schoolSize = 1 (a lone fish is a school of one)
	}
	reseedMobAI(f.ai, f.id)
	// SCHOOLING FISH (Cod/Salmon/TropicalFish): AbstractSchoolingFish extends AbstractFish and adds
	// FollowFlockLeaderGoal@5 in its registerGoals (Pufferfish is a plain AbstractFish -> NO flock goal,
	// the vanilla EXEMPTION). The FollowFlockLeaderGoal ctor draws nextStartTick(mob) = nextInt(200) on the
	// mob's OWN stream, so the goal is built AFTER reseedMobAI (the per-entity seed the vanilla Mob ctor's
	// this.random equals) -- the ctor draw then lands on the correct stream, 1:1 with the jar. Cite
	// AbstractSchoolingFish.registerGoals + FollowFlockLeaderGoal.<init>.
	if isSchoolingFish(typ.ID) {
		f.ai.goals.addGoal(5, newFollowFlockLeaderGoal(f))
	}
	if typ.ID == entity.Salmon.ID {
		// Salmon.finalizeSpawn SIZE roll: this.random == the salmon OWN (id-reseeded) stream == mobRandom(f).
		// Rolled AFTER reseedMobAI so it reads the per-entity seed (a deterministic per-salmon size), 1:1 with
		// this.random in the bytecode.
		f.salmonVariant, f.salmonScale = salmonFinalizeVariant(f)
	}
	owner := t.regionForEntity(f)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(f)
	return f
}

// spawnDolphin creates a Dolphin (MAX_HEALTH 10.0 / MOVEMENT_SPEED 1.2 / ATTACK_DAMAGE 3.0) with the bounded
// swim AI. It starts fully moist (MOISTNESS 2400) so dolphinAiStep drains it only out of water. The jump/
// play/swim-with-player/treasure behaviors are DEFERRED (file header). Cite Dolphin.createAttributes + tick.
func (t *TickLoop) spawnDolphin(x, y, z float64) *Entity {
	d := NewEntity(t.idAlloc.AllocID(), entity.Dolphin, x, y, z)
	d.isWaterMob = true
	d.isDolphin = true
	d.dolphinMoistness = dolphinFullMoistness // TOTAL_MOISTNESS_LEVEL 2400 (a fresh dolphin is fully moist)
	initSpawnHealth(d)                        // setHealth(getMaxHealth()) -> 10.0
	d.ai = newWaterMobAI(dolphinMovementSpeed)
	// DolphinSwimWithPlayerGoal@2 (Dolphin.registerGoals): the swim-with-player + Dolphin's Grace behavior.
	// The other Dolphin-specific goals (BreathAir/TryFindWater @0, SwimToTreasure @1, DolphinJump/PlayWithItems)
	// remain DEFERRED (file header). Cite Dolphin.registerGoals addGoal(2, new DolphinSwimWithPlayerGoal(this, 4.0)).
	d.ai.goals.addGoal(2, newDolphinSwimWithPlayerGoal(dolphinSwimWithPlayerSpeed))
	reseedMobAI(d.ai, d.id)
	owner := t.regionForEntity(d)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(d)
	return d
}

// spawnTadpole creates a Tadpole (MOVEMENT_SPEED 1.0 / MAX_HEALTH 6.0) with the bounded swim AI. It starts
// age 0; tadpoleAiStep ++ages it and grows it into a Frog at ticksToBeFrog (24000). Cite Tadpole.createAttributes + aiStep.
func (t *TickLoop) spawnTadpole(x, y, z float64) *Entity {
	tp := NewEntity(t.idAlloc.AllocID(), entity.Tadpole, x, y, z)
	tp.isWaterMob = true
	tp.isTadpole = true
	tp.tadpoleAge = 0
	initSpawnHealth(tp) // setHealth(getMaxHealth()) -> 6.0
	tp.ai = newWaterMobAI(tadpoleMovementSpeed)
	reseedMobAI(tp.ai, tp.id)
	owner := t.regionForEntity(tp)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(tp)
	return tp
}

// tickWaterMobAirSupply is the 1:1 port of WaterAnimal.handleAirSupply(ServerLevel, int) -- the WATER-MOB
// BREATHING INVERSION. WaterAnimal.baseTick runs PathfinderMob.baseTick first (CAN_BREATHE_UNDER_WATER, wired
// in mobCanBreatheUnderwater, keeps a submerged mob from draining air), THEN handleAirSupply INVERTS the rule:
// OUT of water the mob drains air -1/tick and DROWNS (drown 2.0) once air <= -20; IN water it resets air to
// 300. Called from the tickAI env phase AFTER tickMobBreath. Self-gated on isWaterMob. RNG-free. Cite
// WaterAnimal.handleAirSupply.
func (t *TickLoop) tickWaterMobAirSupply(e *Entity) {
	if !e.isWaterMob || e.dead || !e.isAlive() {
		return
	}
	if !t.entityInWater(e) {
		e.airSupply--
		if shouldTakeDrowningDamage(e.airSupply) {
			e.airSupply = 0
			t.applyDamageEntity(e, damageSourceOf(damageTypeDrown), drownDamage) // DROWN 2.0
		}
	} else {
		e.airSupply = maxAirSupply // in water: setAirSupply(300)
	}
	t.syncMobAirSupply(e)
}

// squidAiStep is the server-side slice of Squid.aiStep: the tentacle-rotation accumulator + the 1-in-10
// tentacleSpeed re-roll. The jet-movement setDeltaMovement branch is driven by the DEFERRED
// SquidRandomMovementGoal (file header), so the bounded per-tick work is the accumulator vanilla runs on
// EVERY squid every tick. Per-type-gated (isSquid) AFTER serverAiStep. RNG only on the squid OWN stream, on
// the 1-in-N wrap tick. Cite Squid.aiStep.
func (t *TickLoop) squidAiStep(e *Entity) {
	if e.dead || !e.isAlive() {
		return
	}
	e.squidTentacleMovement += e.squidTentacleSpeed // tentacleMovement += tentacleSpeed (fadd)
	if e.squidTentacleMovement > squidTwoPi {
		e.squidTentacleMovement -= squidTwoPi
		if e.ai != nil && e.ai.rng != nil && e.ai.rng.nextInt(squidInkChanceBound) == 0 {
			e.squidTentacleSpeed = (1.0 / (e.ai.rng.nextFloat() + 1.0)) * squidTentacleSpeedScale
		}
	}
}

// squidSpawnInk ports Squid.hurtServer tail: after a landed hit, if getLastHurtByMob() != null, spawnInk().
// The INK particle burst + SQUID_INK sound is DEFERRED like the other cosmetic cues. Cite Squid.spawnInk.
func (t *TickLoop) squidSpawnInk(e *Entity) {
	if !e.isSquid || e.dead {
		return
	}
	// DEFERRED: the INK particle burst + SQUID_INK sound (Squid.spawnInk); the ink trigger fires here.
}

// pufferfishAiStep is the server-side slice of Pufferfish.tick (the puff/deflate state machine) + the
// Pufferfish.aiStep sting sweep. The PufferfishPuffGoal (a scary LivingEntity within bbox.inflate(2.0))
// arms the inflate; when none is near the goal stops and the deflate runs. tick() drives 0->1->2 up (counter,
// >40 -> full) and 2->1->0 down (deflateTimer, >60 -> mid, >100 -> small). Per-type-gated (isPufferfish)
// AFTER serverAiStep. RNG-free. Cite Pufferfish.tick + PufferfishPuffGoal + Pufferfish.aiStep.
func (t *TickLoop) pufferfishAiStep(e *Entity) {
	if e.dead || !e.isAlive() {
		return
	}
	if t.pufferfishScanScary(e) {
		if e.pufferInflateCounter == 0 {
			e.pufferDeflateTimer = 0
			e.pufferInflateCounter = 1 // start(): inflateCounter=1, deflateTimer=0
		}
	} else {
		e.pufferInflateCounter = 0 // stop(): inflateCounter=0
	}
	if e.pufferInflateCounter > 0 {
		if e.pufferPuffState == pufferStateSmall {
			e.pufferPuffState = pufferStateMid // 0 -> 1 (BLOW_UP)
		} else if e.pufferInflateCounter > pufferInflateThreshold && e.pufferPuffState == pufferStateMid {
			e.pufferPuffState = pufferStateFull // counter>40 && state==1 -> 2 (BLOW_UP)
		}
		e.pufferInflateCounter++
	} else if e.pufferPuffState != pufferStateSmall {
		if e.pufferDeflateTimer > pufferDeflateMidThreshold && e.pufferPuffState == pufferStateFull {
			e.pufferPuffState = pufferStateMid // deflateTimer>60 && state==2 -> 1 (BLOW_OUT)
		} else if e.pufferDeflateTimer > pufferDeflateSmallThreshold && e.pufferPuffState == pufferStateMid {
			e.pufferPuffState = pufferStateSmall // deflateTimer>100 && state==1 -> 0 (BLOW_OUT)
		}
		e.pufferDeflateTimer++
	}
	if e.pufferPuffState > pufferStateSmall {
		t.pufferfishStingSweep(e) // Pufferfish.aiStep: getPuffState()>0 -> sting adjacent mobs
	}
}

// pufferfishScanScary ports PufferfishPuffGoal.canUse: any LivingEntity within bbox.inflate(2.0) matching
// SCARY_MOB. v1 scans the nearby players (the mob-scan awaits the not-yet-built LivingEntity broad-phase, a
// cited gap). RNG-free. Cite Pufferfish.PufferfishPuffGoal.canUse.
func (t *TickLoop) pufferfishScanScary(e *Entity) bool {
	const scaryRadius = 2.35 // bbox.inflate(2.0): ~0.35-wide fish + 2.0
	_, _, _, ok := nearestPlayerWithin(t, e, scaryRadius)
	return ok
}

// pufferfishStingSweep ports Pufferfish.aiStep sting + Pufferfish.touch/playerTouch: for a player within
// bbox.inflate(0.3), deal (1 + puffState) mob-attack damage and, on a hit, apply POISON for 60*puffState
// ticks. v1 stings the nearby player (the mob-scan is the cited broad-phase gap); damage + poison EXACT.
// Cite Pufferfish.aiStep + Pufferfish.touch + Pufferfish.playerTouch.
func (t *TickLoop) pufferfishStingSweep(e *Entity) {
	const touchRadius = 0.65 // bbox.inflate(0.3): ~0.35-wide fish + 0.3
	p := t.nearestPlayerEntityWithin(e, touchRadius)
	if p == nil {
		return
	}
	state := e.pufferPuffState
	// Pufferfish.touch/playerTouch: hurt(mobAttack(this), 1 + puffState). applyDamage is void, so the
	// "did the hit land" gate mirrors the bee sting pattern (beeAiStep): a fresh hit or an over-lastHurt
	// hit lands (LivingEntity.hurtServer i-frame rule). On a landed hit apply POISON 60*puffState (amp 0),
	// exactly as Pufferfish.touch addEffect(new MobEffectInstance(POISON, 60*puffState, 0)).
	dmg := float32(1 + state)                                   // (i2f) 1 + puffState
	src := damageSourceByTypeName("minecraft:mob_attack", e.id) // damageSources().mobAttack(this)
	landed := !p.dead && (float32(p.invulnerableTime) <= hurtCooldownConst || dmg > p.lastHurt)
	t.applyDamage(p, src, dmg)
	if landed {
		t.addPlayerEffect(p, e.id, effectPoison, pufferStingPoisonPerState*state, 0, 1.0) // POISON 60*puffState
	}
}

// tadpoleAiStep ports Tadpole.aiStep server slice: age++ (setAge), and at age >= ticksToBeFrog (24000)
// ageUp() -> the tadpole GROWS INTO A FROG. isAgeLocked is a DEFERRED DATA flag (default false), so a v1
// tadpole always ages. Per-type-gated (isTadpole) AFTER serverAiStep. RNG-free. Cite Tadpole.aiStep + setAge + ageUp.
func (t *TickLoop) tadpoleAiStep(e *Entity) {
	if e.dead || !e.isAlive() {
		return
	}
	e.tadpoleAge++ // setAge(age+1)
	if e.tadpoleAge >= tadpoleTicksToBeFrog {
		t.tadpoleGrowIntoFrog(e) // age >= ticksToBeFrog -> ageUp()
	}
}

// tadpoleGrowIntoFrog ports Tadpole.ageUp: convertTo(EntityType.FROG) -- spawn a Frog at the tadpole
// position (spawnFrog attaches Frog attributes + AI + default temperate variant), then discard the tadpole
// (mark dead + remove from store). Mirrors the convertTo create+discard shape (thunderHitTypeSwap). Cite
// Tadpole.ageUp + Mob.convertTo.
func (t *TickLoop) tadpoleGrowIntoFrog(e *Entity) {
	t.spawnFrogRaw(e.x, e.y, e.z, false) // convertTo(FROG): copies data via finalizeConversion; NO finalizeSpawn re-roll
	e.dead = true                        // discard() the tadpole (convertTo removes the source)
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.entities != nil {
		owner.entities.remove(e.id)
	}
}

// dolphinAiStep ports Dolphin.tick MOISTNESS_LEVEL: in water/rain -> 2400; else -1/tick and at <= 0 ->
// hurt(dryOut, 1.0). isInWaterOrRain -> isInWater (rain is a cited world-weather gap). The onGround flop +
// jump/swim-with-player/treasure are DEFERRED (file header). Per-type-gated (isDolphin) AFTER serverAiStep.
// RNG-free. Cite Dolphin.tick.
func (t *TickLoop) dolphinAiStep(e *Entity) {
	if e.dead || !e.isAlive() {
		return
	}
	if t.entityInWater(e) {
		e.dolphinMoistness = dolphinFullMoistness // setMoistnessLevel(2400)
	} else {
		e.dolphinMoistness-- // setMoistnessLevel(getMoistnessLevel()-1)
		if e.dolphinMoistness <= 0 {
			t.applyDamageEntity(e, damageSourceOf(damageTypeDryOut), dolphinDryOutDamage) // hurt(dryOut, 1.0F)
		}
	}
}

// TropicalFish DyeColor ids used by COMMON_VARIANTS + packing (DyeColor.getId order, verified javap
// DyeColor.<clinit>: WHITE=0..BLACK=15). Only the referenced ids are named. Cite net.minecraft.world.item.DyeColor.
const (
	tfWhite     = 0
	tfOrange    = 1
	tfLightBlue = 3
	tfYellow    = 4
	tfLime      = 5
	tfPink      = 6
	tfGray      = 7
	tfCyan      = 9
	tfPurple    = 10
	tfBlue      = 11
	tfRed       = 14
)

// TropicalFish$Base ids (SMALL=0, LARGE=1). Cite TropicalFish$Base + TropicalFish$Pattern.<init>.
const (
	tropicalBaseSmallID = 0
	tropicalBaseLargeID = 1
)

// tropicalPatternPackedIds is TropicalFish$Pattern.values() (12) mapped to getPackedId() in enum order:
// KOB,SUNSTREAK,SNOOPER,DASHER,BRINELY,SPOTTY (base SMALL id 0, local 0..5) then FLOPPER,STRIPEY,GLITTER,
// BLOCKFISH,BETTY,CLAYFISH (base LARGE id 1, local 0..5); packedId = (base.id<<8)|local. Cite
// TropicalFish$Pattern.<init> + TropicalFish.finalizeSpawn (Util.getRandom(Pattern.values(), rng)).
var tropicalPatternPackedIds = [12]int{
	(tropicalBaseSmallID << 8) | 0, // KOB
	(tropicalBaseSmallID << 8) | 1, // SUNSTREAK
	(tropicalBaseSmallID << 8) | 2, // SNOOPER
	(tropicalBaseSmallID << 8) | 3, // DASHER
	(tropicalBaseSmallID << 8) | 4, // BRINELY
	(tropicalBaseSmallID << 8) | 5, // SPOTTY
	(tropicalBaseLargeID << 8) | 0, // FLOPPER
	(tropicalBaseLargeID << 8) | 1, // STRIPEY
	(tropicalBaseLargeID << 8) | 2, // GLITTER
	(tropicalBaseLargeID << 8) | 3, // BLOCKFISH
	(tropicalBaseLargeID << 8) | 4, // BETTY
	(tropicalBaseLargeID << 8) | 5, // CLAYFISH
}

// tropicalPackVariant is TropicalFish.packVariant(Pattern, base, patternColor): (pattern.getPackedId()&0xFFFF)
// | ((base.getId()&0xFF)<<16) | ((patternColor.getId()&0xFF)<<24). Cite TropicalFish.packVariant.
func tropicalPackVariant(patternPacked, baseColorID, patColorID int) int {
	return (patternPacked & 0xFFFF) | ((baseColorID & 0xFF) << 16) | ((patColorID & 0xFF) << 24)
}

// tropicalCommonVariants is TropicalFish.COMMON_VARIANTS: the 22-entry pre-packed List.of table from the
// clinit (index 0..21), each packed via tropicalPackVariant(pattern, baseColor, patternColor). Cite
// TropicalFish.<clinit> COMMON_VARIANTS.
var tropicalCommonVariants = [22]int{
	tropicalPackVariant(tropicalPatternPackedIds[7], tfOrange, tfGray),    // STRIPEY, ORANGE, GRAY
	tropicalPackVariant(tropicalPatternPackedIds[6], tfGray, tfGray),      // FLOPPER, GRAY, GRAY
	tropicalPackVariant(tropicalPatternPackedIds[6], tfGray, tfBlue),      // FLOPPER, GRAY, BLUE
	tropicalPackVariant(tropicalPatternPackedIds[11], tfWhite, tfGray),    // CLAYFISH, WHITE, GRAY
	tropicalPackVariant(tropicalPatternPackedIds[1], tfBlue, tfGray),      // SUNSTREAK, BLUE, GRAY
	tropicalPackVariant(tropicalPatternPackedIds[0], tfOrange, tfWhite),   // KOB, ORANGE, WHITE
	tropicalPackVariant(tropicalPatternPackedIds[5], tfPink, tfLightBlue), // SPOTTY, PINK, LIGHT_BLUE
	tropicalPackVariant(tropicalPatternPackedIds[9], tfPurple, tfYellow),  // BLOCKFISH, PURPLE, YELLOW
	tropicalPackVariant(tropicalPatternPackedIds[11], tfWhite, tfRed),     // CLAYFISH, WHITE, RED
	tropicalPackVariant(tropicalPatternPackedIds[5], tfWhite, tfYellow),   // SPOTTY, WHITE, YELLOW
	tropicalPackVariant(tropicalPatternPackedIds[8], tfWhite, tfGray),     // GLITTER, WHITE, GRAY
	tropicalPackVariant(tropicalPatternPackedIds[11], tfWhite, tfOrange),  // CLAYFISH, WHITE, ORANGE
	tropicalPackVariant(tropicalPatternPackedIds[3], tfCyan, tfPink),      // DASHER, CYAN, PINK
	tropicalPackVariant(tropicalPatternPackedIds[4], tfLime, tfLightBlue), // BRINELY, LIME, LIGHT_BLUE
	tropicalPackVariant(tropicalPatternPackedIds[10], tfRed, tfWhite),     // BETTY, RED, WHITE
	tropicalPackVariant(tropicalPatternPackedIds[2], tfGray, tfRed),       // SNOOPER, GRAY, RED
	tropicalPackVariant(tropicalPatternPackedIds[9], tfRed, tfWhite),      // BLOCKFISH, RED, WHITE
	tropicalPackVariant(tropicalPatternPackedIds[6], tfWhite, tfYellow),   // FLOPPER, WHITE, YELLOW
	tropicalPackVariant(tropicalPatternPackedIds[0], tfRed, tfWhite),      // KOB, RED, WHITE
	tropicalPackVariant(tropicalPatternPackedIds[1], tfGray, tfWhite),     // SUNSTREAK, GRAY, WHITE
	tropicalPackVariant(tropicalPatternPackedIds[3], tfCyan, tfYellow),    // DASHER, CYAN, YELLOW
	tropicalPackVariant(tropicalPatternPackedIds[6], tfYellow, tfYellow),  // FLOPPER, YELLOW, YELLOW
}

// tropicalFishFinalizeVariant is TropicalFish.finalizeSpawn variant pick, drawn on level.getRandom()
// (ServerLevelAccessor.getRandom == t.cur().levelRandom), 1:1 draw order: if nextFloat() < 0.9f ->
// Util.getRandom(COMMON_VARIANTS, rng) == COMMON_VARIANTS[nextInt(22)]; else new Variant(
// Util.getRandom(Pattern.values(), rng) [pattern nextInt(12)], DyeColor.values()[nextInt(16)] base,
// DyeColor.values()[nextInt(16)] pattern). super.finalizeSpawn draws no rng before this. Cite
// TropicalFish.finalizeSpawn + Util.getRandom.
func (t *TickLoop) tropicalFishFinalizeVariant() int {
	lr := t.cur().levelRandom
	if lr == nil {
		return tropicalDefaultVariant
	}
	if lr.NextFloat() < 0.9 {
		return tropicalCommonVariants[lr.NextIntN(22)]
	}
	patternPacked := tropicalPatternPackedIds[lr.NextIntN(12)]
	baseColorID := int(lr.NextIntN(16))
	patColorID := int(lr.NextIntN(16))
	return tropicalPackVariant(patternPacked, baseColorID, patColorID)
}

// Salmon$Variant ids + boundingBoxScale (SMALL 0/0.5f, MEDIUM 1/1.0f, LARGE 2/1.5f); finalizeSpawn
// WeightedList weights SMALL 30 / MEDIUM 50 / LARGE 15 (total 95). Cite Salmon$Variant.<clinit>.
const (
	salmonVariantSmall  = 0
	salmonVariantMedium = 1
	salmonVariantLarge  = 2

	salmonScaleSmall  float32 = 0.5
	salmonScaleMedium float32 = 1.0
	salmonScaleLarge  float32 = 1.5

	salmonWeightSmall  = 30
	salmonWeightMedium = 50
	salmonWeightLarge  = 15
)

// salmonFinalizeVariant is Salmon.finalizeSpawn SIZE pick, drawn on the salmon OWN stream (this.random ==
// mobRandom(e), getfield #209 random -- NOT level.getRandom). Builds WeightedList.builder().add(SMALL,30)
// .add(MEDIUM,50).add(LARGE,15).build().getRandom(random): ONE nextInt(95) then cumulative walk in builder
// order. Returns (variantID, boundingBoxScale) for getSalmonScale. Cite Salmon.finalizeSpawn +
// Salmon$Variant + WeightedList.getRandom + Salmon.getSalmonScale.
func salmonFinalizeVariant(e *Entity) (int, float32) {
	const total = salmonWeightSmall + salmonWeightMedium + salmonWeightLarge
	draw := mobRandom(e).nextInt(total)
	if draw < salmonWeightSmall {
		return salmonVariantSmall, salmonScaleSmall
	}
	draw -= salmonWeightSmall
	if draw < salmonWeightMedium {
		return salmonVariantMedium, salmonScaleMedium
	}
	return salmonVariantLarge, salmonScaleLarge
}

// =============================================================================================
// DOLPHIN -- DolphinSwimWithPlayerGoal (Dolphin.registerGoals @2)
// =============================================================================================

// dolphinSwimWithPlayerGoal is the 1:1 port of Dolphin$DolphinSwimWithPlayerGoal (flags {MOVE, LOOK}).
// It finds the nearest SWIMMING player within 10 blocks, grants them DOLPHINS_GRACE, and trails them at
// speedModifier 4.0 -- the headline dolphin behavior. The captured player id (0 == none) is the goal's
// `player` field; it is set in canUse and cleared in stop. Cite Dolphin$DolphinSwimWithPlayerGoal.
type dolphinSwimWithPlayerGoal struct {
	baseGoal
	speedModifier float64 // the ctor's speedModifier (4.0)
	playerID      int32   // the captured swimming player's entity id (0 == none), like the Java `player` field
}

// newDolphinSwimWithPlayerGoal builds the goal with EnumSet.of(MOVE, LOOK) (ctor setFlags) and the
// speedModifier from Dolphin.registerGoals (4.0). Cite Dolphin$DolphinSwimWithPlayerGoal.<init>.
func newDolphinSwimWithPlayerGoal(speedModifier float64) *dolphinSwimWithPlayerGoal {
	return &dolphinSwimWithPlayerGoal{baseGoal: newBaseGoal(flagMove | flagLook), speedModifier: speedModifier}
}

// canUse ports DolphinSwimWithPlayerGoal.canUse: player = getServerLevel(dolphin).getNearestPlayer(
// SWIM_WITH_PLAYER_TARGETING, dolphin); if player == null return false; else return player.isSwimming()
// && dolphin.getTarget() != player. SWIM_WITH_PLAYER_TARGETING = forNonCombat().range(10.0)
// .ignoreLineOfSight() -- the nearest non-dead player within 10 blocks. NO RNG draw. Cite
// DolphinSwimWithPlayerGoal.canUse + Dolphin.<clinit> SWIM_WITH_PLAYER_TARGETING.
func (g *dolphinSwimWithPlayerGoal) canUse(t *TickLoop, e *Entity) bool {
	p := t.nearestPlayerEntityWithin(e, dolphinSwimTargetRange) // getNearestPlayer(range 10, dolphin)
	if p == nil {
		g.playerID = 0
		return false // ifnull -> return 0
	}
	g.playerID = p.entityID
	// player.isSwimming() && dolphin.getTarget() != player. The dolphin's attack-target is an entity id
	// (mobAI.getTarget); a player id never collides with an entity id (shared allocator), so the
	// != player check is "the dolphin isn't already hunting THIS player" -- honored via the id compare.
	return p.swimming && (e.ai == nil || e.ai.getTarget() != p.entityID)
}

// canContinueToUse ports DolphinSwimWithPlayerGoal.canContinueToUse: player != null && player.isSwimming()
// && dolphin.distanceToSqr(player) < 256.0. NO RNG. Cite DolphinSwimWithPlayerGoal.canContinueToUse.
func (g *dolphinSwimWithPlayerGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	p := t.playerByEntityID(g.playerID)
	if p == nil { // ifnull -> false
		return false
	}
	return p.swimming && distanceToSqrPlayer(p, e) < dolphinSwimContinueDistSq
}

// requiresUpdateEveryTick: the goal has no requiresUpdateEveryTick override in the jar, so it inherits
// Goal's default (false) -- tick() runs only when the selector is simulating. Left as the baseGoal default.

// start ports DolphinSwimWithPlayerGoal.start: player.addEffect(new MobEffectInstance(DOLPHINS_GRACE, 100),
// dolphin) -- grant the swim-speed buff on the follow start. NO RNG. Cite DolphinSwimWithPlayerGoal.start.
func (g *dolphinSwimWithPlayerGoal) start(t *TickLoop, e *Entity) {
	p := t.playerByEntityID(g.playerID)
	if p == nil {
		return
	}
	t.addPlayerEffect(p, e.id, effectDolphinsGrace, dolphinGraceDuration, 0, 1.0) // DOLPHINS_GRACE, 100
}

// stop ports DolphinSwimWithPlayerGoal.stop: player = null; dolphin.getNavigation().stop(). Cite
// DolphinSwimWithPlayerGoal.stop.
func (g *dolphinSwimWithPlayerGoal) stop(_ *TickLoop, e *Entity) {
	g.playerID = 0 // this.player = null
	if e.ai != nil {
		e.ai.clearWantTarget() // getNavigation().stop()
	}
}

// tick ports DolphinSwimWithPlayerGoal.tick: setLookAt(player, maxHeadYRot+20, maxHeadXRot); if
// distanceToSqr(player) < 6.25 -> navigation.stop() else navigation.moveTo(player, speedModifier); then if
// player.isSwimming() && level.getRandom().nextInt(6) == 0 -> re-addEffect(DOLPHINS_GRACE, 100). The look
// is the yaw-toward write (the look-control seam other goals use). The re-grace draw is on level.getRandom()
// (the ServerLevel this.random == t.cur().levelRandom), NOT the dolphin's own stream -- 1:1 with the bytecode
// (invokevirtual Level.getRandom). Cite DolphinSwimWithPlayerGoal.tick.
func (g *dolphinSwimWithPlayerGoal) tick(t *TickLoop, e *Entity) {
	p := t.playerByEntityID(g.playerID)
	if p == nil {
		return
	}
	// setLookAt(player, ...): face the player (the yaw-toward write, as lookAtPlayerGoal does).
	yaw := yawTowardDeg(p.x-e.x, p.z-e.z)
	e.headYaw = yaw
	e.yaw = yaw
	if distanceToSqrPlayer(p, e) < dolphinSwimStopDistSq {
		if e.ai != nil {
			e.ai.clearWantTarget() // distanceToSqr < 6.25 -> navigation.stop()
		}
	} else if e.ai != nil {
		// navigation.moveTo(player, speedModifier): want the player's position (nav applies speedModifier).
		e.ai.setWantTargetMod(p.x, p.y, p.z, g.speedModifier)
	}
	// if player.isSwimming() && level.getRandom().nextInt(6) == 0 -> re-grant DOLPHINS_GRACE 100.
	if p.swimming {
		lr := t.cur().levelRandom
		if lr != nil && lr.NextIntN(dolphinFollowRerollBound) == 0 {
			t.addPlayerEffect(p, e.id, effectDolphinsGrace, dolphinGraceDuration, 0, 1.0)
		}
	}
}

// =============================================================================================
// SCHOOLING FISH -- FollowFlockLeaderGoal (AbstractSchoolingFish.registerGoals @5)
// =============================================================================================

// dolphinMaxSchoolSize / fishMaxSchoolSize: AbstractSchoolingFish.getMaxSchoolSize == AbstractFish
// .getMaxSpawnClusterSize == 8 (bipush 8). A leader accepts followers until schoolSize reaches this.
// Cite AbstractFish.getMaxSpawnClusterSize + AbstractSchoolingFish.getMaxSchoolSize.
const fishMaxSchoolSize = 8

// fishSchoolScatterBound: AbstractSchoolingFish.tick leader-scatter gate level.getRandom().nextInt(200)==1
// (sipush 200). Cite AbstractSchoolingFish.tick.
const fishSchoolScatterBound = 200

// fishFollowInRangeDistSq: AbstractSchoolingFish.inRangeOfLeader distanceToSqr(leader) <= 121.0 (ldc2_w
// double 121.0d) -- the follower keeps following while within 11 blocks. Cite AbstractSchoolingFish.inRangeOfLeader.
const fishFollowInRangeDistSq = 121.0

// fishSchoolScanInflate: FollowFlockLeaderGoal.canUse + AbstractSchoolingFish.tick both scan
// getBoundingBox().inflate(8.0, 8.0, 8.0) for same-class fish (ldc2_w double 8.0d). The bounded scan reuses
// the chunk-column near() query; 8-block inflate on a ~0.5-wide fish is well within one column band. Cite
// FollowFlockLeaderGoal.canUse + AbstractSchoolingFish.tick.
const fishSchoolScanInflate = 8.0

// isSchoolingFish reports whether typ is an AbstractSchoolingFish (Cod/Salmon/TropicalFish). Pufferfish
// extends AbstractFish directly (NOT AbstractSchoolingFish) so it is EXEMPT -- never schools. Cite the
// AbstractSchoolingFish subclass set (Cod/Salmon/TropicalFish) vs Pufferfish extends AbstractFish.
func isSchoolingFish(typ entity.ID) bool {
	return typ == entity.Cod.ID || typ == entity.Salmon.ID || typ == entity.TropicalFish.ID
}

// fishLeaderAlive resolves e's leader entity (schoolLeaderID) if it is still live, else nil.
func (t *TickLoop) fishLeaderAlive(e *Entity) *Entity {
	if e.schoolLeaderID == 0 {
		return nil
	}
	leader, ok := t.cur().entities.get(e.schoolLeaderID)
	if !ok || leader == nil || !leader.isAlive() {
		return nil
	}
	return leader
}

// fishIsFollower ports AbstractSchoolingFish.isFollower: leader != null && leader.isAlive().
func (t *TickLoop) fishIsFollower(e *Entity) bool { return t.fishLeaderAlive(e) != nil }

// fishHasFollowers ports AbstractSchoolingFish.hasFollowers: schoolSize > 1.
func fishHasFollowers(e *Entity) bool { return e.schoolSize > 1 }

// fishCanBeFollowed ports AbstractSchoolingFish.canBeFollowed: hasFollowers() && schoolSize < getMaxSchoolSize().
func fishCanBeFollowed(e *Entity) bool { return fishHasFollowers(e) && e.schoolSize < fishMaxSchoolSize }

// fishStartFollowing ports AbstractSchoolingFish.startFollowing(leader): this.leader = leader;
// leader.addFollower() (leader.schoolSize++). Cite AbstractSchoolingFish.startFollowing + addFollower.
func fishStartFollowing(follower, leader *Entity) {
	follower.schoolLeaderID = leader.id
	leader.schoolSize++ // addFollower(): schoolSize += 1
}

// fishStopFollowing ports AbstractSchoolingFish.stopFollowing: leader.removeFollower() (leader.schoolSize--);
// this.leader = null. Cite AbstractSchoolingFish.stopFollowing + removeFollower.
func (t *TickLoop) fishStopFollowing(e *Entity) {
	if leader := t.fishLeaderAlive(e); leader != nil {
		leader.schoolSize-- // removeFollower(): schoolSize -= 1
	} else if e.schoolLeaderID != 0 {
		// The leader is gone (dead/removed): still drop our dangling reference (Java calls
		// leader.removeFollower() unconditionally, but a removed leader's count is irrelevant).
	}
	e.schoolLeaderID = 0 // this.leader = null
}

// schoolingFishTick ports AbstractSchoolingFish.tick (the part beyond super.tick()): if hasFollowers() &&
// level.getRandom().nextInt(200) == 1 && getEntitiesOfClass(getClass(), inflate(8)).size() <= 1 ->
// schoolSize = 1 (the leader's school has scattered; reset so it can re-lead). The 200-gate draw is on
// level.getRandom() (t.cur().levelRandom), 1:1 with the bytecode. Per-type-gated by the caller. Cite
// AbstractSchoolingFish.tick.
func (t *TickLoop) schoolingFishTick(e *Entity) {
	if e.dead || !e.isAlive() {
		return
	}
	if !fishHasFollowers(e) { // ifeq -> return (only a leader scatters)
		return
	}
	lr := t.cur().levelRandom
	if lr == nil || lr.NextIntN(fishSchoolScatterBound) != 1 { // nextInt(200) == 1
		return
	}
	// getEntitiesOfClass(getClass(), getBoundingBox().inflate(8,8,8)).size() <= 1 -> schoolSize = 1.
	if t.fishSchoolNearbyCount(e) <= 1 {
		e.schoolSize = 1
	}
}

// fishSchoolNearbyCount counts same-type live fish within the inflate(8) box INCLUDING e itself (vanilla
// getEntitiesOfClass returns the querying mob too -- size() <= 1 means "only me left"). Cite
// AbstractSchoolingFish.tick getEntitiesOfClass(getClass(), inflate(8)).
func (t *TickLoop) fishSchoolNearbyCount(e *Entity) int {
	rangeChunks := int(math.Ceil(fishSchoolScanInflate / 16.0))
	if rangeChunks < 1 {
		rangeChunks = 1
	}
	count := 0
	for _, other := range t.cur().entities.near(e.x, e.z, rangeChunks) {
		if other.dead || other.typ != e.typ {
			continue // getEntitiesOfClass(getClass()) + isAlive
		}
		// inflate(8,8,8) is an AABB centered on the fish: a per-axis |d| < 8 membership test.
		dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
		if dx <= -fishSchoolScanInflate || dx >= fishSchoolScanInflate ||
			dy <= -fishSchoolScanInflate || dy >= fishSchoolScanInflate ||
			dz <= -fishSchoolScanInflate || dz >= fishSchoolScanInflate {
			continue // outside the inflate(8) box on some axis
		}
		count++
	}
	return count
}

// followFlockLeaderGoal is the 1:1 port of FollowFlockLeaderGoal (no control flags -- setFlags is never
// called in its ctor, so it holds NO flag and coexists with the swim/look goals). A leaderless fish with a
// nearby leadable school joins it (becomes a follower); a leaderless fish with nearby leaderless followers
// becomes their leader (addFollowers up to MAX_SCHOOL_SIZE). While a follower, it paths to its leader every
// ~10 ticks. Cite FollowFlockLeaderGoal.
type followFlockLeaderGoal struct {
	baseGoal
	timeToRecalcPath int // FollowFlockLeaderGoal.timeToRecalcPath
	nextStartTick    int // FollowFlockLeaderGoal.nextStartTick
}

// newFollowFlockLeaderGoal builds the goal. The jar ctor holds NO flag (setFlags is never called) and seeds
// nextStartTick = nextStartTick(mob), which draws mob.getRandom().nextInt(200) on the mob's OWN stream. The
// caller (spawnFish) builds this AFTER reseedMobAI and BEFORE the salmon SIZE roll, so this ctor draw lands
// on the correctly-seeded per-entity stream in the same relative order as vanilla (registerGoals before
// finalizeSpawn). Cite FollowFlockLeaderGoal.<init> + .nextStartTick.
func newFollowFlockLeaderGoal(e *Entity) *followFlockLeaderGoal {
	return &followFlockLeaderGoal{baseGoal: newBaseGoal(0), nextStartTick: fishNextFlockStartTick(e)}
}

// nextStartTick ports FollowFlockLeaderGoal.nextStartTick(mob): reducedTickDelay(200 + mob.getRandom()
// .nextInt(200) % 20). ONE nextInt(200) draw on the mob's OWN stream (mobRandom(e)). Cite
// FollowFlockLeaderGoal.nextStartTick.
func fishNextFlockStartTick(e *Entity) int {
	return reducedTickDelay(200 + int(mobRandom(e).nextInt(200))%20)
}

// canUse ports FollowFlockLeaderGoal.canUse: if mob.hasFollowers() return false (a leader doesn't seek one);
// if mob.isFollower() return true (already following -> keep the goal live); if --nextStartTick > 0 return
// false (cooldown); reset nextStartTick; scan getEntitiesOfClass(getClass(), inflate(8), fish ->
// canBeFollowed() || !isFollower()); pick the first !isFollower() as the leader (DataFixUtils.orElse(mob));
// leader.addFollowers(list.stream().filter(!isFollower())); return mob.isFollower(). Cite
// FollowFlockLeaderGoal.canUse.
func (g *followFlockLeaderGoal) canUse(t *TickLoop, e *Entity) bool {
	if fishHasFollowers(e) { // mob.hasFollowers() -> return false
		return false
	}
	if t.fishIsFollower(e) { // mob.isFollower() -> return true
		return true
	}
	if g.nextStartTick > 0 { // --nextStartTick > 0 -> return false (decrement then test)
		g.nextStartTick--
		return false
	}
	g.nextStartTick = fishNextFlockStartTick(e) // reset the cooldown (nextStartTick(mob) draw)
	// list = getEntitiesOfClass(getClass(), inflate(8), f -> f.canBeFollowed() || !f.isFollower()). The
	// 3-arg getEntitiesOfClass does NOT exclude the querying mob (only the Entity-except overload does), so
	// the querying fish IS in the list when it passes the predicate (a leaderless fish -> !isFollower true).
	// This is how a leaderless mob joins: it is its OWN candidate and the addFollowers filter makes it follow
	// whichever leader is picked. Cite FollowFlockLeaderGoal.canUse + Level.getEntitiesOfClass(Class,AABB,Predicate).
	candidates := t.fishSchoolCandidates(e)
	// leader = list.stream().filter(f -> !f.isFollower()).findAny().orElse(mob) -- the FIRST leaderless fish
	// in list order (or the mob itself if none is leaderless). Cite FollowFlockLeaderGoal.canUse (lambda$canUse$1
	// !isFollower + DataFixUtils.orElse(mob)).
	var leader *Entity
	for _, c := range candidates {
		if !t.fishIsFollower(c) {
			leader = c
			break
		}
	}
	if leader == nil {
		leader = e // DataFixUtils.orElse(mob)
	}
	// leader.addFollowers(list.stream().filter(f -> f != leader)): stream.limit(getMaxSchoolSize() -
	// schoolSize).filter(!= leader).forEach(startFollowing). Stream.limit caps how many list ELEMENTS are
	// consumed (evaluated ONCE at construction with the leader's schoolSize at that instant); the filter
	// (f != leader) then drops the leader itself. addFollowers is on the LEADER. Cite AbstractSchoolingFish
	// .addFollowers (limit + filter(!= this) + forEach(startFollowing)).
	limit := fishMaxSchoolSize - leader.schoolSize
	for _, c := range candidates {
		if limit <= 0 {
			break // Stream.limit exhausted (the FIXED count computed once above)
		}
		limit-- // limit() consumes a list element regardless of the downstream filter
		if c == leader {
			continue // filter(f -> f != leader): the leader never follows itself
		}
		fishStartFollowing(c, leader) // startFollowing(leader): c.leader = leader; leader.schoolSize++
	}
	return t.fishIsFollower(e) // return mob.isFollower()
}

// canContinueToUse ports FollowFlockLeaderGoal.canContinueToUse: mob.isFollower() && mob.inRangeOfLeader().
// NO RNG. Cite FollowFlockLeaderGoal.canContinueToUse + AbstractSchoolingFish.inRangeOfLeader.
func (g *followFlockLeaderGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	leader := t.fishLeaderAlive(e)
	if leader == nil { // !isFollower()
		return false
	}
	return entityDistSqr(e, leader) <= fishFollowInRangeDistSq // inRangeOfLeader: distanceToSqr <= 121.0
}

// start ports FollowFlockLeaderGoal.start: timeToRecalcPath = 0. Cite FollowFlockLeaderGoal.start.
func (g *followFlockLeaderGoal) start(_ *TickLoop, _ *Entity) { g.timeToRecalcPath = 0 }

// stop ports FollowFlockLeaderGoal.stop: mob.stopFollowing(). Cite FollowFlockLeaderGoal.stop.
func (g *followFlockLeaderGoal) stop(t *TickLoop, e *Entity) { t.fishStopFollowing(e) }

// tick ports FollowFlockLeaderGoal.tick: if --timeToRecalcPath > 0 return; timeToRecalcPath =
// adjustedTickDelay(10); mob.pathToLeader() (navigation.moveTo(leader, 1.0) while isFollower). NO RNG. Cite
// FollowFlockLeaderGoal.tick + AbstractSchoolingFish.pathToLeader.
func (g *followFlockLeaderGoal) tick(t *TickLoop, e *Entity) {
	g.timeToRecalcPath--
	if g.timeToRecalcPath > 0 { // pre-decrement then test (dup_x1 in the bytecode)
		return
	}
	g.timeToRecalcPath = adjustedTickDelay(10)
	// pathToLeader(): if isFollower() navigation.moveTo(leader, 1.0). The move seam is setWantTarget (the
	// navigation.moveTo(leader, speed) analogue), speedModifier 1.0 (dconst_1).
	leader := t.fishLeaderAlive(e)
	if leader != nil && e.ai != nil {
		e.ai.setWantTargetMod(leader.x, leader.y, leader.z, 1.0)
	}
}

// fishSchoolCandidates ports the FollowFlockLeaderGoal.canUse scan: getEntitiesOfClass(getClass(),
// getBoundingBox().inflate(8,8,8), f -> f.canBeFollowed() || !f.isFollower()) -- the same-type live fish
// within the inflate(8) box that are either leadable OR leaderless (the goal's predicate). The 3-arg
// getEntitiesOfClass(Class, AABB, Predicate) does NOT exclude the querying entity (only the Entity-except
// overload does), so the querying fish e IS a candidate when it passes the predicate -- this is how a
// leaderless fish becomes its own candidate and joins the picked leader. Cite FollowFlockLeaderGoal.canUse
// (lambda$canUse$0: canBeFollowed() || !isFollower()) + Level.getEntitiesOfClass(Class, AABB, Predicate).
func (t *TickLoop) fishSchoolCandidates(e *Entity) []*Entity {
	rangeChunks := int(math.Ceil(fishSchoolScanInflate / 16.0))
	if rangeChunks < 1 {
		rangeChunks = 1
	}
	var out []*Entity
	for _, other := range t.cur().entities.near(e.x, e.z, rangeChunks) {
		if other.dead || other.typ != e.typ {
			continue // getEntitiesOfClass(getClass()) + isAlive (the querying mob itself IS included)
		}
		dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
		if dx <= -fishSchoolScanInflate || dx >= fishSchoolScanInflate ||
			dy <= -fishSchoolScanInflate || dy >= fishSchoolScanInflate ||
			dz <= -fishSchoolScanInflate || dz >= fishSchoolScanInflate {
			continue // outside the inflate(8) box
		}
		if !(fishCanBeFollowed(other) || !t.fishIsFollower(other)) {
			continue // the goal predicate: canBeFollowed() || !isFollower()
		}
		out = append(out, other)
	}
	return out
}
