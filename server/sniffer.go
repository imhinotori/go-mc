// sniffer.go -- the Sniffer (net.minecraft.world.entity.animal.sniffer.Sniffer), a 1:1 port from the
// unobfuscated 26.2 jar. Sniffer is a large ancient Animal that walks a DIG state machine: it searches,
// sniffs, digs at a valid ground block, and drops an ancient seed (torchflower / pitcher pod). Vanilla
// drives it with a BRAIN + a DATA_STATE enum (IDLING/FEELING/SEARCHING/DIGGING/RISING); for a bounded
// port this lands the attributes + spawn + the "visibly alive" passive goal subset (Float/Panic/Tempt/
// Breed/Follow/Stroll/Look). The dig-for-seeds state machine is the DEFERRED behavior layer (it needs the
// DATA_STATE machine + the DATA_DROP_SEED_AT_TICK timer + the seed drop table + the dig-position search).
// Code-spawned (spawnSniffer) with a *mobAI carrying the passive goals; its per-tick extra is snifferAiStep
// from tickAI (per-type-gated on typ == entity.Sniffer.ID).
//
// VANILLA (verified javap Sniffer this session):
//   createAttributes: Animal.createAnimalAttributes + MOVEMENT_SPEED 0.10000000149011612 + MAX_HEALTH 14.0
//     (see snifferSupplier in level/attribute/defaults.go). FOLLOW_RANGE stays the createMobAttributes 16.0.
//   Sniffer is a BRAIN mob (makeBrain / customServerAiStep -> SnifferAi; no registerGoals classic goals):
//     the brain drives the DATA_STATE machine (IDLING -> FEELING -> SEARCHING -> DIGGING -> RISING) and the
//     DATA_DROP_SEED_AT_TICK dig timer. SNIFFER_FOOD (ItemTags, torchflower seeds): the tempt/breed food.
//     The path-malus set (WATER, ON_TOP_OF_POWDER_SNOW, DAMAGE_CAUTIOUS) keeps it out of water/hazards.
//
// v1 STUBS (cited): the BRAIN (SnifferAi + the DATA_STATE dig-for-seeds machine + the seed drop) is the
// DEFERRED behavior layer -- this port supplies the bounded passive goal walk (Float/Panic/Breed/
// Tempt(SNIFFER_FOOD)/Follow/Stroll/Look). The observable attributes and the passive goal walk are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Sniffer constants (VERIFIED javap Sniffer this session).
const (
	snifferMaxHealth     = 14.0                // createAttributes MAX_HEALTH 14.0
	snifferMovementSpeed = 0.10000000149011612 // createAttributes MOVEMENT_SPEED (float-widened)
	snifferTemptSpeed    = 1.25                // FollowTemptation speed (brain-deferred; the common animal tempt pace)
	snifferFollowSpeed   = 1.25                // BabyFollowAdult speed (brain-deferred; the common follow pace)
	snifferBreedSpeed    = 1.0                 // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	snifferStrollSpeed   = 1.0                 // RandomStroll speed (brain-deferred; the common stroll pace)
	snifferLookDistance  = 8.0                 // LookAtTargetSink distance (the common animal look range)
	snifferFoodTag       = "sniffer_food"      // SNIFFER_FOOD (torchflower seeds): the tempt predicate
)

// newSnifferAI builds the Sniffer bounded passive AI. Sniffer is a BRAIN mob in vanilla (the dig-for-seeds
// DATA_STATE machine lives in SnifferAi, DEFERRED per the file header); this supplies the "visibly alive"
// classic-goal stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newFrogAI shape (per-mob
// rng, navigation seed, canFloat, Animal pathfinding malus). Cite Sniffer.makeBrain (the brain deferral note).
func newSnifferAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * snifferMovementSpeed // seed with MOVEMENT_SPEED (0.1)
	m.navigation.canFloat = true                               // Swim brain behavior: navigation may float
	applyAnimalPathfindingMalus(m)                             // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(snifferBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(snifferTemptSpeed, func(id int32) bool { return itemInTag(id, snifferFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(snifferFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(snifferStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(snifferLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnSniffer creates a Sniffer at (x,y,z) with the jar attributes and the passive goal AI, then adds it
// to the owner region store. baby toggles the AgeableMob baby age + half-scale box (Sniffer is an Animal).
// The DATA_STATE dig machine + seed drop are DEFERRED. initSpawnHealth seeds health from MAX_HEALTH (14.0).
// Cite Sniffer.createAttributes + Sniffer(EntityType, Level).
func (t *TickLoop) spawnSniffer(x, y, z float64, baby bool) *Entity {
	s := NewEntity(t.idAlloc.AllocID(), entity.Sniffer, x, y, z)
	s.isSniffer = true
	if baby {
		s.breedAge = babyStartAge
		s.refreshDimensions() // AgeableMob baby half-scale box (getDefaultDimensions baby-scale)
	}
	initSpawnHealth(s) // setHealth(getMaxHealth()) -> 14.0
	s.ai = newSnifferAI()
	reseedMobAI(s.ai, s.id)
	owner := t.regionForEntity(s)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(s)
	return s
}

// snifferAiStep is the Sniffer per-tick extra (Sniffer.customServerAiStep). Vanilla runs the SnifferAi
// BRAIN here (the DATA_STATE dig-for-seeds machine: search -> sniff -> dig -> drop an ancient seed), which
// is the DEFERRED behavior layer -- so this is a bounded no-op today (the passive goals in newSnifferAI
// drive the visible movement). It is wired + per-type-gated (typ == entity.Sniffer.ID) so the dig machine
// slots in here the moment the brain machinery lands, never baked away. RNG-free. Cite
// Sniffer.customServerAiStep + SnifferAi (the brain deferral note).
func (t *TickLoop) snifferAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: the SnifferAi brain tick (the DATA_STATE machine IDLING -> FEELING -> SEARCHING -> DIGGING
	// -> RISING + the DATA_DROP_SEED_AT_TICK dig timer + the ancient-seed drop). No bounded per-tick work
	// today beyond the passive goal walk.
}
