package server

// strider.go -- the Strider (net.minecraft.world.entity.monster.Strider), a 1:1 port from the unobfuscated
// 26.2 jar. The Strider is a nether Animal that WALKS ON LAVA (canStandOnFluid(LAVA)=true): it floats on the
// lava surface instead of sinking, and OFF a warm block / out of lava it enters the cold "suffocating" state
// (DATA_SUFFOCATING) that shivers + slows it via a transient MOVEMENT_SPEED modifier. Code-spawned
// (spawnStrider) with a minimal e.ai; its per-tick drive is striderAiStep from tickAI (sibling of blazeAiStep,
// per-type-gated on typ == entity.Strider.ID). Its lava-walk float is applied in tickPhysics BEFORE the
// generic in-lava sink branch (striderIsLavaWalker gates it, like ghastIsFlyer gates the flyer branch).
//
// VANILLA (verified javap Strider + inner classes this session):
//   Strider.createAttributes: Animal.createAnimalAttributes + MOVEMENT_SPEED 0.17499999701976776. MAX_HEALTH
//     is the createLivingAttributes default 20.0 (no override); FOLLOW_RANGE the createMobAttributes 16.0.
//   ctor: blocksBuilding=true; setPathfindingMalus(WATER,-1); (LAVA,0); (FIRE_IN_NEIGHBOR,0); (FIRE,0).
//   canStandOnFluid(FluidState): fluidState.is(LAVA) -> true (the lava-walk).
//   isSensitiveToWater(): true. isOnFire(): false (fire-immune display; a strider is a fire-immune nether mob).
//   DATA_SUFFOCATING (BOOLEAN default false). setSuffocating(b): set(DATA_SUFFOCATING,b); if(b)
//     MOVEMENT_SPEED.addOrUpdateTransientModifier(SUFFOCATING_MODIFIER) else removeModifier(id).
//     SUFFOCATING_MODIFIER = AttributeModifier("suffocating", -0.3400000035762787, ADD_MULTIPLIED_BASE).
//   tick(): ... if(!isNoAi()){ BlockState bs=level.getBlockState(blockPosition()); BlockState legacy=
//     getBlockStateOnLegacy(); boolean warm = bs.is(STRIDER_WARM_BLOCKS) || legacy.is(STRIDER_WARM_BLOCKS)
//     || getFluidHeight(LAVA) > 0.0; boolean ridingWarmStrider = (getVehicle() instanceof Strider s &&
//     !s.isSuffocating()); setSuffocating(!(warm || ridingWarmStrider)); } super.tick(); floatStrider().
//   floatStrider(): if(isInLava()){ CollisionContext ctx=of(this); if(ctx.isAbove(getLiquidCollisionShape(),
//     blockPosition(), true) && !level.getFluidState(blockPosition().above()).is(LAVA)){ setDeltaMovement(
//     getDeltaMovement().scale(0.5).add(0, 0.05, 0)); } else { setOnGround(true); } }.
//   getRiddenSpeed(player): getAttributeValue(MOVEMENT_SPEED) * (isSuffocating() ? 0.35f : 0.55f) * boostFactor.
//   checkFallDamage: if(isInLava()) resetFallDistance(); return; else super (no fall damage over lava).
//   createNavigation: StriderPathNavigation (lava-pathfinding). getWalkTargetValue: LAVA cell -> 10.0f.
//
// FIRE IMMUNITY: Strider.isOnFire const-false; a strider is fire+lava immune (entityFireImmune gates it out
// of fire.go tickEntityFire/tickEntityLava). Cite Strider.isOnFire + EntityType.fireImmune.
//
// v1 STUBS (cite-deferred, recorded): the full riding (getControllingPassenger warped-fungus-on-a-stick +
// tickRidden + getRiddenSpeed + saddle) and breeding (BreedGoal/FollowParentGoal/TemptGoal + isFood
// STRIDER_FOOD) -- the task scopes the lava-walk + cold-state + attributes; riding/breeding are cited-deferred.
// The StriderPathNavigation lava-pathfinding + the PanicGoal/StriderGoToLavaGoal + the STRIDER_HAPPY/RETREAT
// ambient sounds are the deferred goal/nav layer (the strider still lava-walks + cold-shivers + slows). The
// getWalkTargetValue lava-preference is the nav layer.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Strider constants (VERIFIED javap Strider this session).
const (
	// striderSuffocatingModifierID is Strider.SUFFOCATING_MODIFIER_ID (Identifier "minecraft:suffocating").
	striderSuffocatingModifierID = "minecraft:suffocating"
	// striderSuffocatingAmount is the SUFFOCATING_MODIFIER amount: -0.3400000035762787, ADD_MULTIPLIED_BASE.
	// A suffocating strider moves at base * (1 + (-0.34)) = base * 0.66 (the cold-shiver slowdown).
	striderSuffocatingAmount = -0.3400000035762787
	// striderLavaFloatUp is floatStrider's vertical lift (add(0, 0.05, 0)) when riding the lava surface.
	striderLavaFloatUp = 0.05
	// striderLavaFloatScale is floatStrider's deltaMovement.scale(0.5) when riding the lava surface.
	striderLavaFloatScale = 0.5
)

// spawnStriderRaw is the bare Strider create (no finalizeSpawn) at (x,y,z): the tracker broadcasts
// AddEntity next tick. Minimal e.ai (per-entity rng); NO goalSelector (the lava-walk + cold-state drive is
// the code-driven striderAiStep, like spawnBlaze). initSpawnHealth seeds health from the folded MAX_HEALTH
// (20.0). The variant roll (jockey/baby/saddle) is spawnStrider -> striderFinalizeSpawn. Cite Strider(EntityType, Level).
func (t *TickLoop) spawnStriderRaw(x, y, z float64) *Entity {
	s := NewEntity(t.idAlloc.AllocID(), entity.Strider, x, y, z)
	s.isStrider = true
	initSpawnHealth(s) // setHealth(getMaxHealth()) -> 20.0
	s.ai = &mobAI{}
	reseedMobAI(s.ai, s.id)
	owner := t.regionForEntity(s)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(s)
	return s
}

// setStriderSuffocating ports Strider.setSuffocating(boolean): flip DATA_SUFFOCATING and add/remove the
// SUFFOCATING_MODIFIER (-0.34 ADD_MULTIPLIED_BASE) transient modifier on MOVEMENT_SPEED. When suffocating,
// MOVEMENT_SPEED base 0.175 folds to 0.175 * (1 - 0.34) = 0.1155 (the cold-shiver slowdown). NO RNG. Cite
// Strider.setSuffocating.
func (t *TickLoop) setStriderSuffocating(e *Entity, suffocating bool) {
	e.striderSuffocating = suffocating
	if e.attributes == nil {
		return
	}
	inst := e.attributes.GetInstance(attribute.MovementSpeed.Name())
	if inst == nil {
		return
	}
	if suffocating {
		// addOrUpdateTransientModifier(SUFFOCATING_MODIFIER): remove-then-add (the vanilla update semantics
		// -- AttributeInstance.addOrUpdateTransientModifier drops any existing modifier with this id first,
		// then adds, so re-calling it each suffocating tick is safe and never double-applies). RemoveModifier
		// is a no-op when absent (the first suffocating tick).
		inst.RemoveModifier(striderSuffocatingModifierID)
		inst.AddTransientModifier(attribute.AttributeModifier{
			ID:        striderSuffocatingModifierID,
			Amount:    striderSuffocatingAmount,
			Operation: attribute.AddMultipliedBase,
		})
	} else {
		inst.RemoveModifier(striderSuffocatingModifierID) // removeModifier(SUFFOCATING_MODIFIER_ID)
	}
}

// striderWarmBlockAt reports whether the block state at (x,y,z) is in the STRIDER_WARM_BLOCKS tag (vanilla
// value: [minecraft:lava]). The BlockState.is(BlockTags.STRIDER_WARM_BLOCKS) analogue via blockInTag.
func (t *TickLoop) striderWarmBlockAt(x, y, z int) bool {
	return blockInTag(t.blockStateAt(x, y, z), "strider_warm_blocks")
}

// striderTickSuffocation ports Strider.tick's cold-state block: warm = block at pos is warm OR block-on-
// legacy (feet-1) is warm OR getFluidHeight(LAVA) > 0; setSuffocating(!warm). A strider ON a warm block /
// in lava stays warm (not suffocating, full speed); OFF it goes cold (suffocating, slowed + shivering). The
// riding-warm-strider branch is cite-deferred (no strider-riding population in v1). NO RNG. Cite Strider.tick.
func (t *TickLoop) striderTickSuffocation(e *Entity) {
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	// bs = level.getBlockState(blockPosition()); legacy = getBlockStateOnLegacy() (the block one below feet).
	warm := t.striderWarmBlockAt(bx, by, bz) ||
		t.striderWarmBlockAt(bx, by-1, bz) ||
		t.mobFluidHeight(e, fluidLava) > 0.0
	t.setStriderSuffocating(e, !warm) // setSuffocating(!(warm || ridingWarmStrider)) -- riding deferred
}

// striderFloat ports Strider.floatStrider(): while in lava, if the strider sits ABOVE the lava collision
// surface (and the cell above is not itself lava), it rides the surface -- deltaMovement = deltaMovement
// .scale(0.5).add(0, 0.05, 0) (a gentle bob that keeps it on top); otherwise it is fully submerged and is
// treated as onGround (walks along the lava floor-top). v1 uses the fluid-height surface as the "above the
// liquid collision shape" test: if the strider base sits at/above the lava surface top, it rides; else it
// stands. Cite Strider.floatStrider + getLiquidCollisionShape + CollisionContext.isAbove.
func (t *TickLoop) striderFloat(e *Entity) {
	if !t.mobInLava(e) { // if (isInLava())
		return
	}
	// isAbove(getLiquidCollisionShape(), blockPosition(), true) && !level.getFluidState(above()).is(LAVA):
	// the strider is riding the lava SURFACE (its feet are at/above the lava top and there is no lava above).
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	aboveIsLava := t.fluidAt(pk.Position{X: bx, Y: by + 1, Z: bz}).isLava
	lavaSurface := float64(by) + t.mobFluidHeight(e, fluidLava) // approx surface top (== e.y + height above base)
	ridingSurface := !aboveIsLava && e.y+0.001 >= lavaSurface-1e-9

	if ridingSurface {
		// setDeltaMovement(getDeltaMovement().scale(0.5).add(0, 0.05, 0)): bob on the surface.
		e.vx = e.vx * striderLavaFloatScale
		e.vy = e.vy*striderLavaFloatScale + striderLavaFloatUp
		e.vz = e.vz * striderLavaFloatScale
	} else {
		e.onGround = true // setOnGround(true): submerged -> stand on the lava floor-top (no sink)
	}
}

// striderIsLavaWalker reports whether an entity is a Strider (the tickPhysics lava branch reads it to take
// the striderFloat surface-ride path INSTEAD of the generic in-lava sink -- like ghastIsFlyer gates the
// flyer branch). Strider.canStandOnFluid(LAVA) is true, so a strider never sinks in lava. Cite
// Strider.canStandOnFluid.
func striderIsLavaWalker(e *Entity) bool {
	return e.typ == entity.Strider.ID
}

// striderAiStep is the Strider per-tick drive: the cold-state suffocation toggle (Strider.tick block) then
// the lava-surface float (floatStrider). Called from tickAI for a live strider (typ == entity.Strider.ID),
// AFTER serverAiStep. The generic tickPhysics lava branch is bypassed for a strider (striderIsLavaWalker),
// so striderFloat owns its in-lava vertical motion. NO RNG (the STRIDER_HAPPY/RETREAT ambient rolls are
// deferred). Cite Strider.tick + floatStrider.
func (t *TickLoop) striderAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	t.striderTickSuffocation(e) // setSuffocating(!warm) -- the cold-shiver slowdown toggle
	t.striderFloat(e)           // floatStrider() -- ride/stand on the lava surface (never sink)
}

// spawnStrider creates a Strider at (x,y,z) then runs the Strider.finalizeSpawn variant roll (jockey /
// baby / saddle) on level.getRandom(). spawnStriderRaw is the bare create (no finalize) used both here and
// by the baby-jockey child inside striderFinalizeSpawn so the child never re-rolls. Cite Strider.finalizeSpawn.
func (t *TickLoop) spawnStrider(x, y, z float64) *Entity {
	s := t.spawnStriderRaw(x, y, z)
	t.striderFinalizeSpawn(s)
	return s
}

// striderFinalizeSpawn ports Strider.finalizeSpawn 1:1, drawn on level.getRandom() (ServerLevelAccessor
// .getRandom == t.cur().levelRandom). Draw order:
//
//	if (isBaby()) return super.finalizeSpawn(...);                     // no variant draws for a baby
//	RandomSource r = level.getRandom();
//	if (r.nextInt(30) == 0) {                                          // 1-in-30 zombified-piglin jockey
//	    Mob rider = ZOMBIFIED_PIGLIN.create(...); ...
//	    spawnJockey(..., new Zombie$ZombieGroupData(Zombie.getSpawnAsBabyOdds(r), false));  // nextFloat()<0.05
//	    rider.setItemSlot(MAINHAND, WARPED_FUNGUS_ON_A_STICK);
//	    this.setItemSlot(SADDLE, SADDLE); this.setGuaranteedDrop(SADDLE);
//	} else if (r.nextInt(10) == 0) {                                   // 1-in-10 baby jockey strider
//	    AgeableMob rider = STRIDER.create(...); rider.setAge(-24000); spawnJockey(..., null);
//	} else groupData = new AgeableMob$AgeableMobGroupData(0.5f);
//	return super.finalizeSpawn(...);
//
// The DRAWS (nextInt(30); then either getSpawnAsBabyOdds nextFloat() or nextInt(10)) are the lockstep-
// critical part and happen 1:1. The rider MOUNT (Mob.startRiding via spawnJockey) + the rider/saddle
// EQUIPMENT slot sets are DEFERRED (no passenger/equipment-slot subsystem in v1); the strider still spawns
// the jockey/baby entity, sets its baby age, and records striderSaddled. The AgeableMobGroupData / super
// .finalizeSpawn (Animal) carry no further strider variant draws. striderFinalized guards the one-shot roll;
// a baby (breedAge < 0) early-returns exactly like isBaby(). Cite Strider.finalizeSpawn + Strider.spawnJockey
// + Zombie.getSpawnAsBabyOdds (nextFloat() < 0.05f).
func (t *TickLoop) striderFinalizeSpawn(e *Entity) {
	if e.striderFinalized {
		return
	}
	e.striderFinalized = true
	if e.breedAge < 0 { // isBaby(): a baby jockey strider (JOCKEY reason) draws no variant
		return
	}
	lr := t.cur().levelRandom
	if lr == nil {
		return
	}
	if lr.NextIntN(30) == 0 { // r.nextInt(30) == 0 -> zombified-piglin jockey
		_ = t.spawnZombifiedPiglin(e.x, e.y, e.z) // ZOMBIFIED_PIGLIN.create(JOCKEY); MOUNT via startRiding DEFERRED
		_ = striderZombieSpawnAsBabyOdds(lr)      // Zombie.getSpawnAsBabyOdds(r): nextFloat() < 0.05f (the draw)
		// rider MAINHAND WARPED_FUNGUS_ON_A_STICK + this SADDLE slot set DEFERRED (no equipment-slot subsystem);
		// record the saddle + guaranteed-drop intent on the strider so it lands when the slot API exists.
		e.striderSaddled = true
		return
	}
	if lr.NextIntN(10) == 0 { // else r.nextInt(10) == 0 -> baby jockey strider
		baby := t.spawnStriderRaw(e.x, e.y, e.z) // STRIDER.create(JOCKEY) via the non-finalizing raw create
		if baby != nil {
			baby.striderFinalized = true // JOCKEY-reason baby: isBaby() early-return, no re-roll
			baby.breedAge = babyStartAge // setAge(-24000)
			baby.refreshDimensions()
		}
		// spawnJockey MOUNT (startRiding) DEFERRED.
		return
	}
	// else: AgeableMobGroupData(0.5f) -- no draw, no strider state.
}

// striderZombieSpawnAsBabyOdds is Zombie.getSpawnAsBabyOdds(RandomSource): rng.nextFloat() < 0.05f. Drawn
// on level.getRandom() inside Strider.finalizeSpawn when constructing the zombified-piglin jockey's
// ZombieGroupData; the draw MUST be consumed for lockstep. Cite Zombie.getSpawnAsBabyOdds.
func striderZombieSpawnAsBabyOdds(lr interface{ NextFloat() float32 }) bool {
	return lr.NextFloat() < 0.05 // ldc 0.05f; fcmpg < -> true
}
