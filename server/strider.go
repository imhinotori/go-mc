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
// RIDING + BREEDING + GOALS (this session, 1:1 javap-verified):
//   getControllingPassenger(): isSaddled() && firstPassenger instanceof Player p && p.isHolding(
//     WARPED_FUNGUS_ON_A_STICK) -> p; else super. Wired into passenger.go getControllingPassenger (strider branch).
//   getRiddenSpeed(player) = getAttributeValue(MOVEMENT_SPEED) * (isSuffocating() ? 0.35f : 0.55f) * boostFactor().
//   getRiddenInput(player, in) = new Vec3(0,0,1). tickRidden: setRot + steering.tickBoost() + super.
//   ItemBasedSteering.boost(rng): boostTimeTotal = nextInt(841)+140; tickBoost/boostFactor curve
//     (1.0 + 1.15f*sin(boostTime/total * PI)) -- striderBoost/striderBoostFactor.
//   mobInteract: !isFood && isSaddled && !isVehicle && !isSecondaryUseActive -> startRiding + SUCCESS;
//     else super feed; a SADDLE item equips the SADDLE slot (v1: striderSaddled flag). STRIDER_EAT sound draw.
//   isFood(stack) = stack.is(STRIDER_FOOD) (tag: [warped_fungus]). getBreedOffspring -> a STRIDER.
//   registerGoals: PanicGoal(1.65)@1, BreedGoal(1.0)@2, TemptGoal(1.4, STRIDER_FOOD)@3,
//     StriderGoToLavaGoal(1.0)@4, FollowParentGoal(1.0)@5, RandomStrollGoal(1.0,60)@7, LookAtPlayer(8.0)@8,
//     RandomLookAround@8, LookAtPlayer(Strider,8.0)@9. (newStriderAI.)
//
// v1 STUBS (cite-deferred): the StriderPathNavigation lava-pathfinding + StriderGoToLavaGoal target-seek + the
// STRIDER_HAPPY/RETREAT ambient sounds are the deferred nav layer. The SADDLE/MAINHAND equipment SLOT item sets
// (finalizeSpawn jockey + mobInteract saddle) fold to the striderSaddled bool until the equipment-slot API exists.

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

	// striderFoodTag is the ItemTags.STRIDER_FOOD tag (value: [minecraft:warped_fungus]) -- isFood(stack)
	// == stack.is(STRIDER_FOOD). Cite Strider.isFood.
	striderFoodTag = "strider_food"
	// itemWarpedFungusOnAStick is Items.WARPED_FUNGUS_ON_A_STICK (item id 888): the control item a rider must
	// hold for getControllingPassenger to steer the strider. Cite Strider.getControllingPassenger.
	itemWarpedFungusOnAStick = 888
	// itemSaddle is Items.SADDLE (item id 865) -- equipping it into the SADDLE slot saddles the strider.
	itemSaddle = 865

	// Strider.getRiddenSpeed steering factors (VERIFIED javap Strider.getRiddenSpeed):
	//   getAttributeValue(MOVEMENT_SPEED) * (isSuffocating() ? 0.35f : 0.55f) * boostFactor().
	striderSuffocateSteerModifier = float32(0.35) // SUFFOCATE_STEERING_MODIFIER (ldc 0.35f)
	striderSteerModifier          = float32(0.55) // STEERING_MODIFIER          (ldc 0.55f)

	// ItemBasedSteering boost timer bounds (VERIFIED javap ItemBasedSteering.boost): boostTimeTotal =
	// rng.nextInt(841) + 140. MIN_BOOST_TIME 140, MAX_BOOST_TIME (140 + 840) == 980.
	striderBoostMinTime  = 140 // + nextInt(841) -> [140, 980]
	striderBoostRandSpan = 841 // nextInt(841)
	// striderBoostFactorAmp is ItemBasedSteering.boostFactor's amplitude (ldc 1.15f): factor = 1.0 +
	// 1.15f * sin(boostTime/boostTimeTotal * PI). Cite ItemBasedSteering.boostFactor.
	striderBoostFactorAmp = float32(1.15)
)

// spawnStriderRaw is the bare Strider create (no finalizeSpawn) at (x,y,z): the tracker broadcasts
// AddEntity next tick. Minimal e.ai (per-entity rng); NO goalSelector (the lava-walk + cold-state drive is
// the code-driven striderAiStep, like spawnBlaze). initSpawnHealth seeds health from the folded MAX_HEALTH
// (20.0). The variant roll (jockey/baby/saddle) is spawnStrider -> striderFinalizeSpawn. Cite Strider(EntityType, Level).
func (t *TickLoop) spawnStriderRaw(x, y, z float64) *Entity {
	s := NewEntity(t.idAlloc.AllocID(), entity.Strider, x, y, z)
	s.isStrider = true
	initSpawnHealth(s) // setHealth(getMaxHealth()) -> 20.0
	s.ai = newStriderAI() // registerGoals: Panic/Breed/Tempt/GoToLava/FollowParent/Stroll/Look
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

// striderBoostFactor ports ItemBasedSteering.boostFactor(): while boosting, 1.0f + 1.15f *
// sin((boostTime / boostTimeTotal) * PI); otherwise 1.0f. The sin curve ramps the boost up to a
// peak (~2.15x at the midpoint) then back to 1.0 as the timer runs out. NO RNG. Cite
// ItemBasedSteering.boostFactor.
func striderBoostFactor(e *Entity) float32 {
	if !e.striderBoosting {
		return 1.0 // fconst_1
	}
	total := e.striderBoostTimeTotal
	if total == 0 {
		return 1.0 // guard div-by-zero (boostTimeTotal is >=140 once boost() ran)
	}
	// 1.0f + 1.15f * Mth.sin((boostTime/boostTimeTotal) * PI) -- float math mirroring the jar (i2f then fdiv).
	frac := float32(e.striderBoostTime) / float32(total)
	return 1.0 + striderBoostFactorAmp*float32(math.Sin(float64(frac)*math.Pi))
}

// striderTickBoost ports ItemBasedSteering.tickBoost(): if boosting, boostTime++ and if it passes
// boostTimeTotal, clear boosting. Called from tickRidden each tick the strider is ridden. NO RNG.
// Cite ItemBasedSteering.tickBoost.
func striderTickBoost(e *Entity) {
	if !e.striderBoosting {
		return
	}
	e.striderBoostTime++
	if e.striderBoostTime > e.striderBoostTimeTotal {
		e.striderBoosting = false
	}
}

// striderBoost ports ItemBasedSteering.boost(RandomSource): if already boosting, no-op (returns false);
// else start a boost -- boosting=true, boostTime=0, boostTimeTotal = rng.nextInt(841) + 140 (DATA_BOOST_TIME
// set). Returns true iff a new boost started. The warped_fungus_on_a_stick item calls this when a rider uses
// it. Cite ItemBasedSteering.boost.
func striderBoost(e *Entity, r *entityRandom) bool {
	if e.striderBoosting {
		return false // iconst_0 ireturn
	}
	e.striderBoosting = true
	e.striderBoostTime = 0
	e.striderBoostTimeTotal = r.nextInt(striderBoostRandSpan) + striderBoostMinTime // nextInt(841)+140
	return true
}

// striderGetRiddenSpeed ports Strider.getRiddenSpeed(player): getAttributeValue(MOVEMENT_SPEED) *
// (isSuffocating() ? 0.35f : 0.55f) * steering.boostFactor(). The MOVEMENT_SPEED attribute value already
// folds the SUFFOCATING_MODIFIER (-0.34 ADD_MULTIPLIED_BASE) when suffocating, and the 0.35/0.55 steering
// factor is applied ON TOP -- both stack, exactly as in the jar. Returns a float32 (the jar's d2f). Cite
// Strider.getRiddenSpeed.
func striderGetRiddenSpeed(e *Entity) float32 {
	base := e.getAttributeValue(attribute.MovementSpeed) // getAttributeValue(MOVEMENT_SPEED) (double)
	var steer float32
	if e.striderSuffocating { // isSuffocating() ? 0.35f : 0.55f
		steer = striderSuffocateSteerModifier
	} else {
		steer = striderSteerModifier
	}
	// (double base) * (float steer widened to double) * (float boostFactor widened to double), then d2f.
	return float32(base * float64(steer) * float64(striderBoostFactor(e)))
}

// striderIsSaddled ports Strider.isSaddled() (EquipmentSlot.SADDLE presence). v1 has no equipment-slot item
// store, so the SADDLE slot is folded to the striderSaddled bool (set by finalizeSpawn's jockey saddle and by
// mobInteract equipping a SADDLE). Structured to become a real hasItemInSlot(SADDLE) read. Cite
// AbstractHorse.isSaddled (the shared isSaddled seam) + Strider.getControllingPassenger's isSaddled() gate.
func striderIsSaddled(e *Entity) bool { return e.striderSaddled }

// striderIsFood ports Strider.isFood(stack): stack.is(ItemTags.STRIDER_FOOD) (tag value [warped_fungus]).
// Cite Strider.isFood.
func striderIsFood(itemID int32) bool { return itemInTag(itemID, striderFoodTag) }

// newStriderAI builds the Strider goalSelector 1:1 from Strider.registerGoals (VERIFIED javap):
//
//	addGoal(1, new PanicGoal(this, 1.65));
//	addGoal(2, new BreedGoal(this, 1.0));
//	this.temptGoal = new TemptGoal(this, 1.4, stack -> stack.is(STRIDER_TEMPT_ITEMS), false);
//	addGoal(3, this.temptGoal);
//	addGoal(4, new StriderGoToLavaGoal(this, 1.0));
//	addGoal(5, new FollowParentGoal(this, 1.0));
//	addGoal(7, new RandomStrollGoal(this, 1.0, 60));
//	addGoal(8, new LookAtPlayerGoal(this, Player.class, 8.0f));
//	addGoal(8, new RandomLookAroundGoal(this));
//	addGoal(9, new LookAtPlayerGoal(this, Strider.class, 8.0f));
//
// TemptGoal predicate uses STRIDER_TEMPT_ITEMS (== STRIDER_FOOD tag [warped_fungus] + warped_fungus_on_a_stick).
// The StriderGoToLavaGoal target-seek is bounded to a stroll placeholder (the lava-nav layer is deferred): it
// still contributes the priority-4 goal SLOT so the goal set is complete. LookAtPlayer(Strider) is a second
// look goal at priority 9. NO RNG at construction. Cite Strider.registerGoals.
func newStriderAI() *mobAI {
	m := &mobAI{}
	m.goals.addGoal(1, newPanicGoal(striderPanicSpeed))
	m.goals.addGoal(2, newBreedGoal(striderBreedSpeed))
	m.goals.addGoal(3, newTemptGoal(striderTemptSpeed, func(id int32) bool { return striderTemptItem(id) }, false, nil))
	m.goals.addGoal(4, newRandomStrollGoal(striderGoToLavaSpeed, striderStrollInterval)) // StriderGoToLavaGoal(1.0) SLOT (lava-seek nav deferred)
	m.goals.addGoal(5, newFollowParentGoal(striderFollowSpeed))
	m.goals.addGoal(7, newRandomStrollGoal(striderStrollSpeed, striderStrollInterval)) // bare RandomStrollGoal(1.0, 60)
	m.goals.addGoal(8, newLookAtPlayerGoal(striderLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	m.goals.addGoal(9, newLookAtPlayerGoal(striderLookDistance)) // LookAtPlayerGoal(Strider.class, 8.0)
	return m
}

// striderTemptItem reports whether an item is in STRIDER_TEMPT_ITEMS: the STRIDER_FOOD tag [warped_fungus]
// PLUS warped_fungus_on_a_stick (the control item also tempts). Cite Strider.registerGoals temptGoal predicate
// (STRIDER_TEMPT_ITEMS = STRIDER_FOOD + WARPED_FUNGUS_ON_A_STICK).
func striderTemptItem(id int32) bool {
	return striderIsFood(id) || id == itemWarpedFungusOnAStick
}

// Strider goal priorities/speeds (VERIFIED javap Strider.registerGoals).
const (
	striderPanicSpeed     = 1.65 // PanicGoal(this, 1.65)
	striderBreedSpeed     = 1.0  // BreedGoal(this, 1.0)
	striderTemptSpeed     = 1.4  // TemptGoal(this, 1.4, ...)
	striderGoToLavaSpeed  = 1.0  // StriderGoToLavaGoal(this, 1.0)
	striderFollowSpeed    = 1.0  // FollowParentGoal(this, 1.0)
	striderStrollSpeed    = 1.0  // RandomStrollGoal(this, 1.0, 60)
	striderStrollInterval = 60   // RandomStrollGoal interval
	striderLookDistance   = float32(8.0)
)

// tryStriderInteract ports Strider.mobInteract's ride branch for the v1 right-click mount. Vanilla:
//
//	boolean isFood = isFood(getItemInHand(hand));
//	if (!isFood && isSaddled() && !isVehicle() && !player.isSecondaryUseActive()) {
//	    if (!level().isClientSide()) player.startRiding(this);
//	    return SUCCESS;
//	}
//	InteractionResult r = super.mobInteract(player, hand);   // Animal feed/breed path
//	if (!r.consumesAction()) {
//	    ItemStack stack = getItemInHand(hand);
//	    return isEquippableInSlot(stack, SADDLE) ? stack.interactLivingEntity(...) : PASS;
//	}
//	if (isFood && !isSilent()) level().playSound(..., STRIDER_EAT, ...);   // eat sound draw
//	return r;
//
// Returns true when the interact belongs to the strider (a mount OR a saddle-equip); false to fall through
// to the shared feed path (handleInteract's tryFeedAnimal) for the isFood case. The mount is the load-bearing
// v1 ride; the SADDLE-equip folds to the striderSaddled bool (no equipment-slot item store). Cite
// Strider.mobInteract.
func (t *TickLoop) tryStriderInteract(p *tickPlayer, mob *Entity, usingSecondaryAction bool) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot)) // player.getItemInHand(hand)
	isFood := !slotIsEmpty(held) && striderIsFood(int32(held.ItemID))

	// RIDE branch: !isFood && isSaddled() && !isVehicle() && !isSecondaryUseActive().
	if !isFood && striderIsSaddled(mob) && !mob.isVehicle() && !usingSecondaryAction {
		// doPlayerRide: if (!isClientSide) player.startRiding(this). Broadcast the passenger list so the
		// rider's client attaches and every tracker renders the seated player.
		if t.playerStartRiding(p, mob, false) {
			t.broadcastSetPassengers(mob)
		}
		return true // SUCCESS -- the mount belongs to the strider
	}
	// SADDLE-equip branch (after super.mobInteract does not consume): a SADDLE item saddles the strider. v1
	// folds the SADDLE equipment slot to the striderSaddled bool. isFood falls through to the shared feed path.
	if !isFood && !slotIsEmpty(held) && int32(held.ItemID) == itemSaddle && !striderIsSaddled(mob) {
		mob.striderSaddled = true
		// stack.interactLivingEntity == SaddleItem equip: consume 1 from the stack (server-side).
		held.Count--
		inv.set(heldWindowSlot(inv.heldSlot), held)
		return true // the saddle-equip belongs to the strider
	}
	// isFood -> fall through to tryFeedAnimal (the super.mobInteract Animal feed/breed path). The STRIDER_EAT
	// eat-sound + its 2 nextFloat draws are DEFERRED with the sound subsystem (cited); the feed RNG (setInLove)
	// is on the shared feed path unchanged.
	return false
}

// striderRiderHoldingControlItem ports the Player.isHolding(WARPED_FUNGUS_ON_A_STICK) check inside
// Strider.getControllingPassenger: the strider is steerable ONLY while its rider holds the control item.
// isHolding checks MAINHAND (v1 reads the selected hotbar slot, the established held read). Cite
// Strider.getControllingPassenger + Player.isHolding.
func (t *TickLoop) striderRiderHoldingControlItem(p *tickPlayer) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	return !slotIsEmpty(held) && int32(held.ItemID) == itemWarpedFungusOnAStick
}
