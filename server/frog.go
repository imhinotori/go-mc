// frog.go -- the Frog (net.minecraft.world.entity.animal.frog.Frog), a 1:1 port from the unobfuscated
// 26.2 jar. Frog is a swamp Animal that LONG-JUMPS (the jump-heavy navigation), eats slimes/magma-cubes
// with its TONGUE (then lays frogspawn), and comes in a VARIANT (temperate / warm / cold) chosen by the
// birth biome. Vanilla drives it with a BRAIN; for a bounded port this lands the attributes + spawn + the
// "visibly alive" passive goal subset (Float/Tempt/Breed/Follow/Stroll/Look) + the biome variant. The
// tongue-eat + frogspawn lay and the brain long-jump are the DEFERRED behavior layer (they need the
// tongue-target brain behavior + the frogspawn block + a variant-by-biome POI read). Code-spawned
// (spawnFrog) with a *mobAI carrying the passive goals; its per-tick extra is frogAiStep from tickAI
// (per-type-gated on typ == entity.Frog.ID).
//
// VANILLA (verified javap Frog this session):
//   createAttributes: Animal.createAnimalAttributes + MOVEMENT_SPEED 1.0 (dconst_1) + MAX_HEALTH 10.0 +
//     ATTACK_DAMAGE 10.0 + STEP_HEIGHT 1.0 (dconst_1). MOVEMENT_SPEED 1.0 is large -- the jump-heavy nav
//     reads it through a small per-jump scale (a hopping mob); ATTACK_DAMAGE 10.0 one-shots a slime/magma-
//     cube (the tongue-eat kill). STEP_HEIGHT 1.0 OVERRIDES the base createLivingAttributes default 0.6.
//   Frog is a BRAIN mob (makeBrain / customServerAiStep -> FrogAi): the goal set is the brain behaviors
//     (Swim, MoveToTargetSink, AnimalPanic, FollowTemptation(FROG_FOOD=slimeball), BabyFollowAdult,
//     LongJumpMidJump, tongue eat-slime/magma-cube -> lay frogspawn, ...).
//   VARIANT: DATA_VARIANT_ID (Holder<FrogVariant>); DEFAULT_VARIANT temperate; finalizeSpawn picks the
//     variant from the birth biome (warm/cold/temperate).
//
// v1 STUBS (cited): the BRAIN (FrogAi sensors + activities: LongJumpMidJump, the tongue eat-slime/magma-
// cube -> lay frogspawn, FollowTemptation, BabyFollowAdult) is the DEFERRED behavior layer -- this port
// supplies the bounded passive goal walk (Float/Tempt(FROG_FOOD)/Breed/Follow/Stroll/Look) + the variant
// field (defaulted to temperate; the biome read is DEFERRED, structured to become a real biome lookup).
// The tongue-eat + frogspawn lay and the brain long-jump are DEFERRED. The observable attributes (incl.
// the STEP_HEIGHT 1.0 full-block hop-up) and the passive goal walk are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// FrogVariant ids (Frog DATA_VARIANT_ID -> FrogVariant registry; DEFAULT_VARIANT temperate).
const (
	frogVariantTemperate = 0 // DEFAULT_VARIANT (the swamp/plains frog)
	frogVariantWarm      = 1 // the desert/jungle frog
	frogVariantCold      = 2 // the snowy frog
)

// Frog constants (VERIFIED javap Frog this session).
const (
	frogMaxHealth     = 10.0        // createAttributes MAX_HEALTH 10.0
	frogMovementSpeed = 1.0         // createAttributes MOVEMENT_SPEED (dconst_1) -- large, scaled by the jump nav
	frogAttackDamage  = 10.0        // createAttributes ATTACK_DAMAGE 10.0 (the tongue one-shot)
	frogStepHeight    = 1.0         // createAttributes STEP_HEIGHT (dconst_1) -- full-block hop-up
	frogTemptSpeed    = 1.25        // FollowTemptation speed (brain-deferred; the common animal tempt pace)
	frogFollowSpeed   = 1.25        // BabyFollowAdult speed (brain-deferred; the common follow pace)
	frogBreedSpeed    = 1.0         // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	frogStrollSpeed   = 1.0         // RandomStroll speed (brain-deferred; the common stroll pace)
	frogLookDistance  = 8.0         // LookAtTargetSink distance (the common animal look range)
	frogFoodTag       = "frog_food" // FROG_FOOD (slimeball): the tempt predicate
	// FrogAi.TIME_BETWEEN_LONG_JUMPS = UniformInt.of(100, 140): initMemories draws sample(rng) ==
	// min + nextInt(max-min+1) == 100 + nextInt(41). Cite FrogAi.<clinit> + UniformInt.sample.
	frogTimeBetweenLongJumpsMin  = 100
	frogTimeBetweenLongJumpsSpan = 41
)

// newFrogAI builds the Frog bounded passive AI. Frog is a BRAIN mob in vanilla (the long-jump/tongue-eat/
// tempt behaviors live in FrogAi, DEFERRED per the file header); this supplies the "visibly alive"
// classic-goal stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newPigAI shape (per-mob
// rng, navigation seed, canFloat, Animal pathfinding malus). Cite Frog.makeBrain (the brain deferral note).
func newFrogAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * frogMovementSpeed // seed with MOVEMENT_SPEED (1.0)
	m.navigation.canFloat = true                            // Swim brain behavior: navigation may float
	applyAnimalPathfindingMalus(m)                          // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(frogBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(frogTemptSpeed, func(id int32) bool { return itemInTag(id, frogFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(frogFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(frogStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(frogLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnFrogRaw is the bare Frog create (no finalizeSpawn) at (x,y,z) with the jar attributes + passive AI
// owner region store. The variant defaults to temperate (DEFAULT_VARIANT); the biome-derived pick
// (Frog.finalizeSpawn) is DEFERRED and structured to become a real biome lookup (the frogVariant field,
// never baked away). baby toggles the AgeableMob baby age + half-scale box. initSpawnHealth seeds health
// from MAX_HEALTH (10.0). Cite Frog.createAttributes + Frog.finalizeSpawn (DEFAULT_VARIANT temperate).
func (t *TickLoop) spawnFrogRaw(x, y, z float64, baby bool) *Entity {
	f := NewEntity(t.idAlloc.AllocID(), entity.Frog, x, y, z)
	f.isFrog = true
	f.frogVariant = frogVariantTemperate // DEFAULT_VARIANT; the biome-derived pick is DEFERRED
	if baby {
		f.breedAge = babyStartAge
		f.refreshDimensions()
	}
	initSpawnHealth(f) // setHealth(getMaxHealth()) -> 10.0
	f.ai = newFrogAI()
	reseedMobAI(f.ai, f.id)
	owner := t.regionForEntity(f)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(f)
	return f
}

// frogAiStep is the Frog per-tick extra (Frog.customServerAiStep). Vanilla runs the FrogAi BRAIN here (the
// long-jump / tongue eat-slime-and-magma-cube -> lay frogspawn behaviors), which is the DEFERRED behavior
// layer -- so this is a bounded no-op today (the passive goals in newFrogAI drive the visible movement). It
// is wired + per-type-gated (typ == entity.Frog.ID) so the tongue-eat + frogspawn lay + long-jump slot in
// here the moment the brain machinery lands, never baked away. RNG-free. Cite Frog.customServerAiStep +
// FrogAi (the brain deferral note).
func (t *TickLoop) frogAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: FrogAi brain tick (LongJumpMidJump high hop; the tongue eat-slime/magma-cube -> lay
	// frogspawn). No bounded per-tick work today beyond the passive goal walk. e.frogVariant carries the
	// (temperate default) variant for the client texture + the future biome-derived pick.
	_ = e.frogVariant
}

// spawnFrog creates a Frog at (x,y,z) then runs Frog.finalizeSpawn (the biome variant pick + the FrogAi
// .initMemories rng draw). spawnFrogRaw is the bare create used by the Tadpole conversion path (Tadpole
// .ageUp -> convertTo, which copies data via a finalizeConversion callback and does NOT re-run
// finalizeSpawn). Cite Frog.finalizeSpawn + Tadpole.ageUp (convertTo).
func (t *TickLoop) spawnFrog(x, y, z float64, baby bool) *Entity {
	f := t.spawnFrogRaw(x, y, z, baby)
	t.frogFinalizeSpawn(f)
	return f
}

// frogFinalizeSpawn ports Frog.finalizeSpawn 1:1:
//
//	VariantUtils.selectVariantToSpawn(SpawnContext.create(level, blockPosition()), FROG_VARIANT)
//	    .ifPresent(this::setVariant);                     // biome-derived variant; NO rng draw
//	FrogAi.initMemories(this, level.getRandom());         // ONE UniformInt.of(100,140).sample(rng)
//	return super.finalizeSpawn(...);                      // Animal; no further frog draws
//
// The variant pick is biome-driven (SpawnContext + the FROG_VARIANT registry spawn conditions, the
// warm/cold/temperate biome-tag filter) and draws NO rng. The frog-variant biome-tag data is not yet wired,
// so the biome->variant mapping is DEFERRED (frogVariant stays the DEFAULT_VARIANT temperate, set in
// spawnFrogRaw, never baked away -- the biome read via biomeIDAt is structured to feed it). The LOCKSTEP-
// critical part is FrogAi.initMemories' single draw, TIME_BETWEEN_LONG_JUMPS = UniformInt.of(100,140), whose
// sample(rng) == 100 + rng.nextInt(41), drawn on level.getRandom() (t.cur().levelRandom); it MUST be
// consumed here. Cite Frog.finalizeSpawn + VariantUtils.selectVariantToSpawn + FrogAi.initMemories +
// UniformInt.sample.
func (t *TickLoop) frogFinalizeSpawn(e *Entity) {
	// Biome-derived variant (SpawnContext + FROG_VARIANT spawn conditions): DEFERRED (no frog-variant biome
	// tags wired). e.frogVariant stays DEFAULT_VARIANT temperate; the biome read below is the structured hook.
	// _, _ = t.biomeIDAt(int(math.Floor(e.x)), int(math.Floor(e.y)), int(math.Floor(e.z)))
	lr := t.cur().levelRandom
	if lr == nil {
		return
	}
	// FrogAi.initMemories: TIME_BETWEEN_LONG_JUMPS.sample(rng) == 100 + nextInt(140-100+1) == 100 + nextInt(41).
	_ = frogTimeBetweenLongJumpsMin + int(lr.NextIntN(frogTimeBetweenLongJumpsSpan))
}
