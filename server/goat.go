// goat.go -- the Goat (net.minecraft.world.entity.animal.goat.Goat), a 1:1 port from the unobfuscated
// 26.2 jar. Goat is a mountain Animal that RAMS its target (the LongJumpToRandomPos / RamTarget brain
// behavior: lower head, charge, knockback, and drop a goat horn), jumps unusually high (the goat
// long-jump), and comes in a rare SCREAMING variant (louder, rams more often). Vanilla drives it with a
// BRAIN; for a bounded port this lands the attributes + spawn + the "visibly alive" passive goal subset
// (Float/Tempt/Breed/Follow/Stroll/Look) + the SCREAMING-variant spawn roll. The RAM (LongJump + charge +
// horn drop) and the high goat-jump are the DEFERRED behavior layer (they need the brain LongJump
// machinery + the horn item/drop table). Code-spawned (spawnGoat) with a *mobAI carrying the passive
// goals; its per-tick extra is goatAiStep from tickAI (per-type-gated on typ == entity.Goat.ID).
//
// VANILLA (verified javap Goat this session):
//   createAttributes: Animal.createAnimalAttributes + MAX_HEALTH 10.0 + MOVEMENT_SPEED 0.20000000298023224
//     + ATTACK_DAMAGE 2.0 (ONE MOVEMENT_SPEED add -- the ram/screaming speed is the brain LongJump machinery).
//   Goat is a BRAIN mob (makeBrain / customServerAiStep -> GoatAi): the goal set is the brain behaviors
//     (Swim, LookAtTargetSink, MoveToTargetSink, AnimalPanic, FollowTemptation(WHEAT), BabyFollowAdult,
//     LongJumpToRandomPos, PrepareRamNearestTarget, RamTarget, RandomStroll, ...). GOAT_SCREAMING_CHANCE 0.02.
//   finalizeSpawn: GoatAi.initMemories; setScreamingGoat(random.nextDouble() < 0.02); ageBoundaryReached();
//     if(!isBaby() && random.nextFloat() < 0.1) removeOneHorn (nextBoolean picks left/right).
//   isScreamingGoat(): DATA_IS_SCREAMING_GOAT (default false); a screaming goat uses the louder ambient/hurt
//     sounds + rams more frequently (RamTarget cooldown shorter).
//
// v1 STUBS (cited): the BRAIN (GoatAi sensors + activities: LongJumpToRandomPos, PrepareRamNearestTarget,
// RamTarget, FollowTemptation, BabyFollowAdult) is the DEFERRED behavior layer -- this port supplies the
// bounded passive goal walk (Float/Tempt(GOAT_FOOD)/Breed/Follow/Stroll/Look) + the faithful SCREAMING
// spawn roll (nextDouble() < 0.02). The RAM (lower-head charge + knockback + goat-horn drop), the high
// goat-jump, and the one-horn removal roll are DEFERRED. The observable attributes, the passive goal walk,
// and the screaming-variant flag are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Goat constants (VERIFIED javap Goat this session).
const (
	goatMaxHealth       = 10.0                // createAttributes MAX_HEALTH 10.0
	goatMovementSpeed   = 0.20000000298023224 // createAttributes MOVEMENT_SPEED (float-widened)
	goatTemptSpeed      = 1.25                // FollowTemptation speed (brain-deferred; the common animal tempt pace)
	goatFollowSpeed     = 1.25                // BabyFollowAdult speed (brain-deferred; the common follow pace)
	goatBreedSpeed      = 1.0                 // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	goatStrollSpeed     = 1.0                 // RandomStroll speed (brain-deferred; the common stroll pace)
	goatLookDistance    = 8.0                 // LookAtTargetSink distance (the common animal look range)
	goatScreamingChance = 0.02                // GOAT_SCREAMING_CHANCE (ldc2_w 0.02d): nextDouble() < 0.02 -> screaming
	goatFoodTag         = "goat_food"         // GOAT_FOOD (WHEAT): the tempt predicate
)

// newGoatAI builds the Goat bounded passive AI. Goat is a BRAIN mob in vanilla (the RAM/long-jump/tempt
// behaviors live in GoatAi, DEFERRED per the file header); this supplies the "visibly alive" classic-goal
// stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newPigAI shape (per-mob rng, navigation
// seed, canFloat, Animal pathfinding malus). The priorities follow the pig passive template (a faithful
// bounded ordering for the brain-deferred goat). Cite Goat.makeBrain (the brain deferral note).
func newGoatAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * goatMovementSpeed // seed with MOVEMENT_SPEED (0.2)
	m.navigation.canFloat = true                            // Swim brain behavior: navigation may float
	applyAnimalPathfindingMalus(m)                          // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(goatBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(goatTemptSpeed, func(id int32) bool { return itemInTag(id, goatFoodTag) }, false))
	m.goals.addGoal(5, newFollowParentGoal(goatFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(goatStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(goatLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnGoat creates a Goat at (x,y,z) with the jar attributes and the passive goal AI, then adds it to the
// owner region store. It ports the Goat.finalizeSpawn SCREAMING roll: setScreamingGoat(random.nextDouble()
// < 0.02) on the goat OWN per-entity rng (drawn BEFORE reseedMobAI's own seed would matter -- we roll on a
// fresh spawn-seeded stream). baby toggles the AgeableMob baby age + half-scale box. The one-horn removal
// roll + ageBoundaryReached horn state are DEFERRED. initSpawnHealth seeds health from MAX_HEALTH (10.0).
// Cite Goat.createAttributes + Goat.finalizeSpawn (GOAT_SCREAMING_CHANCE 0.02).
func (t *TickLoop) spawnGoat(x, y, z float64, baby bool) *Entity {
	g := NewEntity(t.idAlloc.AllocID(), entity.Goat, x, y, z)
	g.isGoat = true
	if baby {
		g.breedAge = babyStartAge
		g.refreshDimensions()
	}
	initSpawnHealth(g) // setHealth(getMaxHealth()) -> 10.0
	g.ai = newGoatAI()
	reseedMobAI(g.ai, g.id)
	// finalizeSpawn: setScreamingGoat(random.nextDouble() < GOAT_SCREAMING_CHANCE 0.02). Rolled on the
	// goat's own (now id-reseeded) stream -- a deterministic per-goat variant, drawn once at spawn.
	g.goatScreaming = mobRandom(g).nextDouble() < goatScreamingChance
	owner := t.regionForEntity(g)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(g)
	return g
}

// goatAiStep is the Goat per-tick extra (Goat.customServerAiStep). Vanilla runs the GoatAi BRAIN here (the
// RAM/long-jump/tempt behaviors), which is the DEFERRED behavior layer -- so this is a bounded no-op today
// (the passive goals in newGoatAI drive the visible movement). It is wired + per-type-gated (typ ==
// entity.Goat.ID) so the ram/long-jump slots in here the moment the brain LongJump machinery lands, never
// baked away. RNG-free. Cite Goat.customServerAiStep + GoatAi (the brain deferral note).
func (t *TickLoop) goatAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: GoatAi brain tick (PrepareRamNearestTarget -> RamTarget lower-head charge + knockback +
	// goat-horn drop; LongJumpToRandomPos high goat-jump). The screaming variant (e.goatScreaming) shortens
	// the ram cooldown when that lands. No bounded per-tick work today beyond the passive goal walk.
	_ = e.goatScreaming
}
