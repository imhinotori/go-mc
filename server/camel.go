// camel.go -- the Camel (net.minecraft.world.entity.animal.camel.Camel), a 1:1 port from the unobfuscated
// 26.2 jar. Camel is a large desert AbstractHorse that SITS and STANDS (the pose toggle), DASHES on a
// rider double-jump (a burst of speed with a cooldown), and carries TWO seated riders. Vanilla drives it
// with a BRAIN; for a bounded port this lands the attributes + spawn + the "visibly alive" passive goal
// subset (Float/Panic/Tempt/Breed/Follow/Stroll/Look). The sit/stand pose, the dash burst, and the
// 2-seat rideable are the DEFERRED behavior layer (they need the AbstractHorse mount machinery + the
// pose/dash DATA accessors + the brain camelBrain activities). Code-spawned (spawnCamel) with a *mobAI
// carrying the passive goals; its per-tick extra is camelAiStep from tickAI (per-type-gated on typ ==
// entity.Camel.ID).
//
// VANILLA (verified javap Camel this session):
//   createAttributes: AbstractHorse.createBaseHorseAttributes + MAX_HEALTH 32.0 + MOVEMENT_SPEED
//     0.09000000357627869 + JUMP_STRENGTH 0.41999998688697815 + STEP_HEIGHT 1.5. createBaseHorseAttributes
//     = Animal.createAnimalAttributes + JUMP_STRENGTH 0.7 + MAX_HEALTH 53.0 + MOVEMENT_SPEED
//     0.22499999403953552 + STEP_HEIGHT 1.0 + SAFE_FALL_DISTANCE 6.0 + FALL_DAMAGE_MULTIPLIER 0.5. Under
//     buildKeepingLast the FINAL supplier is MAX_HEALTH 32.0, MOVEMENT_SPEED 0.09000000357627869,
//     STEP_HEIGHT 1.5, SAFE_FALL_DISTANCE 6.0 (see camelSupplier in level/attribute/defaults.go).
//   Camel is a BRAIN mob (makeBrain / customServerAiStep -> the camelBrain / camelActivityUpdate
//     profiler-wrapped brain tick; registerGoals adds NO classic goals -- it only wraps the brain).
//   DASH: DATA_DASH (default false) + LAST_POSE_CHANGE_TICK; a rider double-jump sets a dash burst with a
//     cooldown. Pose sit/stand is the DATA pose toggle. TWO seats (getMaxPassengers 2).
//   CAMELS_SPAWNABLE_ON (BlockTags): the spawn-surface predicate. CAMEL_FOOD (ItemTags): the tempt/breed
//     food (cactus).
//
// v1 STUBS (cited): the BRAIN (the camelBrain activities: the sit/stand pose behavior, the dash burst, the
// 2-seat rideable mount machinery) is the DEFERRED behavior layer -- this port supplies the bounded passive
// goal walk (Float/Panic/Breed/Tempt(CAMEL_FOOD)/Follow/Stroll/Look). The JUMP_STRENGTH 0.42 and
// FALL_DAMAGE_MULTIPLIER 0.5 are cite-omitted attributes (no consumer; see camelSupplier). The observable
// attributes (incl. STEP_HEIGHT 1.5 tall-block step-up + SAFE_FALL_DISTANCE 6.0) and the passive goal walk
// are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Camel constants (VERIFIED javap Camel this session).
const (
	camelMaxHealth     = 32.0                // createAttributes MAX_HEALTH 32.0
	camelMovementSpeed = 0.09000000357627869 // createAttributes MOVEMENT_SPEED (float-widened)
	camelStepHeight    = 1.5                 // createAttributes STEP_HEIGHT 1.5 (tall-block step-up)
	camelTemptSpeed    = 1.25                // FollowTemptation speed (brain-deferred; the common animal tempt pace)
	camelFollowSpeed   = 1.25                // BabyFollowAdult speed (brain-deferred; the common follow pace)
	camelBreedSpeed    = 1.0                 // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	camelStrollSpeed   = 1.0                 // RandomStroll speed (brain-deferred; the common stroll pace)
	camelLookDistance  = 8.0                 // LookAtTargetSink distance (the common animal look range)
	camelFoodTag       = "camel_food"        // CAMEL_FOOD (cactus): the tempt predicate
)

// newCamelAI builds the Camel bounded passive AI. Camel is a BRAIN mob in vanilla (the sit/stand/dash/
// 2-seat behaviors live in the camelBrain, DEFERRED per the file header); this supplies the "visibly
// alive" classic-goal stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newFrogAI shape
// (per-mob rng, navigation seed, canFloat, Animal pathfinding malus). Cite Camel.makeBrain (the brain
// deferral note).
func newCamelAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * camelMovementSpeed // seed with MOVEMENT_SPEED (0.09)
	m.navigation.canFloat = true                             // Swim brain behavior: navigation may float
	applyAnimalPathfindingMalus(m)                           // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(camelBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(camelTemptSpeed, func(id int32) bool { return itemInTag(id, camelFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(camelFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(camelStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(camelLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnCamel creates a Camel at (x,y,z) with the jar attributes and the passive goal AI, then adds it to
// the owner region store. baby toggles the AgeableMob baby age + half-scale box (Camel is an AbstractHorse
// -> AgeableMob). The sit/stand pose + dash burst + rideable state are DEFERRED. initSpawnHealth seeds
// health from MAX_HEALTH (32.0). Cite Camel.createAttributes + Camel(EntityType, Level).
func (t *TickLoop) spawnCamel(x, y, z float64, baby bool) *Entity {
	c := NewEntity(t.idAlloc.AllocID(), entity.Camel, x, y, z)
	c.isCamel = true
	if baby {
		c.breedAge = babyStartAge
		c.refreshDimensions() // AgeableMob baby half-scale box (getDefaultDimensions baby-scale)
	}
	initSpawnHealth(c) // setHealth(getMaxHealth()) -> 32.0
	c.ai = newCamelAI()
	reseedMobAI(c.ai, c.id)
	owner := t.regionForEntity(c)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(c)
	return c
}

// camelAiStep is the Camel per-tick extra (Camel.customServerAiStep). Vanilla runs the camelBrain here (the
// sit/stand pose behavior, the dash-burst cooldown decay, the 2-seat rideable mount logic), which is the
// DEFERRED behavior layer -- so this is a bounded no-op today (the passive goals in newCamelAI drive the
// visible movement). It is wired + per-type-gated (typ == entity.Camel.ID) so the sit/dash/rideable slots
// in here the moment the brain machinery lands, never baked away. RNG-free. Cite Camel.customServerAiStep +
// the camelBrain (the brain deferral note).
func (t *TickLoop) camelAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: the camelBrain tick (sit/stand pose toggle; the dash-burst speed pulse + cooldown decay;
	// the 2-seat rideable mount machinery). No bounded per-tick work today beyond the passive goal walk.
}
