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
// v1 STUBS (cited): the specialized swim navigation goals (FishSwimGoal/SquidRandomMovementGoal/
// RandomSwimmingGoal/DolphinJumpGoal/DolphinSwimWithPlayerGoal/DolphinSwimToTreasureGoal/PlayWithItemsGoal/
// BreathAirGoal/TryFindWaterGoal) are DEFERRED; this port stands them in with the classic Float/Panic/
// Stroll/Look set over the canFloat swim navigation (mirroring newFrogAI/newAxolotlAI), plus the Pufferfish
// puff goal which IS ported. The Dolphin treasure-find (needs structure-locate), the TropicalFish 2-pattern
// client render, and the GlowSquid glow (needs the glowing effect) are DEFERRED behind cited stubs = vanilla
// default. Attributes + drown-on-land inversion + the listed signature per-tick behaviors are EXACT.

package server

import (
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
)

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
		f.tropicalVariant = tropicalDefaultVariant // DEFAULT_VARIANT (KOB/WHITE/WHITE -> 0); 2-pattern render DEFERRED
	}
	initSpawnHealth(f) // setHealth(getMaxHealth()) -> 3.0
	f.ai = newWaterMobAI(0)
	reseedMobAI(f.ai, f.id)
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
	dmg := float32(1 + state) // (i2f) 1 + puffState
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
	t.spawnFrog(e.x, e.y, e.z, false) // convertTo(FROG): a fresh adult Frog at the tadpole position
	e.dead = true                     // discard() the tadpole (convertTo removes the source)
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
