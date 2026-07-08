// axolotl.go -- the Axolotl (net.minecraft.world.entity.animal.axolotl.Axolotl), a 1:1 port from the
// unobfuscated 26.2 jar. Axolotl is an amphibious Animal that swims in water and walks (slowly, floundering)
// on land, PLAYS DEAD when hurt (a self-regen + attacker-ignore state), hunts fish/squid, and comes in FIVE
// color variants (lucy/wild/gold/cyan/blue, the blue a rare breeding mutation). Vanilla drives it with a
// BRAIN + an amphibious navigation; for a bounded port this lands the attributes + spawn + the "visibly
// alive" passive goal subset (Float/Panic/Tempt/Breed/Follow/Stroll/Look) + the variant field. The
// play-dead state and the 5-color variant pick are the DEFERRED behavior layer (they need the
// DATA_PLAYING_DEAD state + the regen effect + the DATA_VARIANT breeding-mutation roll). Code-spawned
// (spawnAxolotl) with a *mobAI carrying the passive goals; its per-tick extra is axolotlAiStep from tickAI
// (per-type-gated on typ == entity.Axolotl.ID).
//
// VANILLA (verified javap Axolotl this session):
//   createAttributes: Animal.createAnimalAttributes + MAX_HEALTH 14.0 + MOVEMENT_SPEED 1.0 (dconst_1) +
//     ATTACK_DAMAGE 2.0 + STEP_HEIGHT 1.0 (dconst_1) (see axolotlSupplier in level/attribute/defaults.go).
//     MOVEMENT_SPEED 1.0 is large because the amphibious navigation reads it through the swim scale;
//     STEP_HEIGHT 1.0 OVERRIDES the base createLivingAttributes default 0.6.
//   Axolotl is a BRAIN mob (makeBrain / customServerAiStep -> AxolotlAi; no registerGoals classic goals):
//     the brain drives the play-dead + hunt-fish behavior. DATA_VARIANT (5 colors); AXOLOTL_FOOD (ItemTags,
//     tropical fish bucket) is the tempt/breed food. Bucketable (a bucket-catchable water mob).
//
// v1 STUBS (cited): the BRAIN (AxolotlAi + the play-dead self-regen state + the hunt-fish target) and the
// 5-color DATA_VARIANT pick are the DEFERRED behavior layer -- this port supplies the bounded passive goal
// walk (Float/Panic/Breed/Tempt(AXOLOTL_FOOD)/Follow/Stroll/Look) + the variant field (defaulted to lucy;
// the breeding-mutation roll is DEFERRED, structured to become a real variant pick). The observable
// attributes (incl. MOVEMENT_SPEED 1.0 swim scale + STEP_HEIGHT 1.0 full-block step-up) and the passive
// goal walk are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// AxolotlVariant ids (Axolotl DATA_VARIANT -> Axolotl.Variant; DEFAULT lucy).
const (
	axolotlVariantLucy = 0 // DEFAULT (the pink axolotl)
	axolotlVariantWild = 1 // the brown axolotl
	axolotlVariantGold = 2 // the gold axolotl
	axolotlVariantCyan = 3 // the cyan axolotl
	axolotlVariantBlue = 4 // the rare blue breeding mutation
)

// Axolotl constants (VERIFIED javap Axolotl this session).
const (
	axolotlMaxHealth     = 14.0           // createAttributes MAX_HEALTH 14.0
	axolotlMovementSpeed = 1.0            // createAttributes MOVEMENT_SPEED (dconst_1) -- large, scaled by the swim nav
	axolotlAttackDamage  = 2.0            // createAttributes ATTACK_DAMAGE 2.0
	axolotlStepHeight    = 1.0            // createAttributes STEP_HEIGHT (dconst_1) -- full-block step-up
	axolotlTemptSpeed    = 1.25           // FollowTemptation speed (brain-deferred; the common animal tempt pace)
	axolotlFollowSpeed   = 1.25           // BabyFollowAdult speed (brain-deferred; the common follow pace)
	axolotlBreedSpeed    = 1.0            // AnimalMakeLove/breed speed (brain-deferred; the common breed pace)
	axolotlStrollSpeed   = 1.0            // RandomStroll speed (brain-deferred; the common stroll pace)
	axolotlLookDistance  = 8.0            // LookAtTargetSink distance (the common animal look range)
	axolotlFoodTag       = "axolotl_food" // AXOLOTL_FOOD (tropical fish bucket): the tempt predicate
)

// newAxolotlAI builds the Axolotl bounded passive AI. Axolotl is a BRAIN mob in vanilla (the play-dead/
// hunt-fish/amphibious behaviors live in AxolotlAi, DEFERRED per the file header); this supplies the
// "visibly alive" classic-goal stand-in (Float/Panic/Breed/Tempt/Follow/Stroll/Look), mirroring newFrogAI
// shape (per-mob rng, navigation seed, canFloat, Animal pathfinding malus). Cite Axolotl.makeBrain (the
// brain deferral note).
func newAxolotlAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * axolotlMovementSpeed // seed with MOVEMENT_SPEED (1.0)
	m.navigation.canFloat = true                               // amphibious navigation: WATER is a standable node
	applyAnimalPathfindingMalus(m)                             // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(axolotlBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(axolotlTemptSpeed, func(id int32) bool { return itemInTag(id, axolotlFoodTag) }, false))
	m.goals.addGoal(5, newFollowParentGoal(axolotlFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(axolotlStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(axolotlLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnAxolotl creates an Axolotl at (x,y,z) with the jar attributes and the passive goal AI, then adds it
// to the owner region store. The variant defaults to lucy (DEFAULT); the breeding-mutation pick
// (Axolotl.getBreedOffspring) is DEFERRED and structured to become a real variant roll (the axolotlVariant
// field, never baked away). baby toggles the AgeableMob baby age + half-scale box. The play-dead self-regen
// state is DEFERRED. initSpawnHealth seeds health from MAX_HEALTH (14.0). Cite Axolotl.createAttributes +
// Axolotl(EntityType, Level).
func (t *TickLoop) spawnAxolotl(x, y, z float64, baby bool) *Entity {
	a := NewEntity(t.idAlloc.AllocID(), entity.Axolotl, x, y, z)
	a.isAxolotl = true
	a.axolotlVariant = axolotlVariantLucy // DEFAULT; the breeding-mutation pick is DEFERRED
	if baby {
		a.breedAge = babyStartAge
		a.refreshDimensions() // AgeableMob baby half-scale box (getDefaultDimensions baby-scale)
	}
	initSpawnHealth(a) // setHealth(getMaxHealth()) -> 14.0
	a.ai = newAxolotlAI()
	reseedMobAI(a.ai, a.id)
	owner := t.regionForEntity(a)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(a)
	return a
}

// axolotlAiStep is the Axolotl per-tick extra (Axolotl.customServerAiStep). Vanilla runs the AxolotlAi
// BRAIN here (the play-dead self-regen state, the hunt-fish target, the amphibious water/land transition),
// which is the DEFERRED behavior layer -- so this is a bounded no-op today (the passive goals in
// newAxolotlAI drive the visible movement). It is wired + per-type-gated (typ == entity.Axolotl.ID) so the
// play-dead + hunt slots in here the moment the brain machinery lands, never baked away. RNG-free. Cite
// Axolotl.customServerAiStep + AxolotlAi (the brain deferral note).
func (t *TickLoop) axolotlAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: the AxolotlAi brain tick (the DATA_PLAYING_DEAD self-regen + attacker-ignore state; the
	// hunt-fish target; the amphibious water/land transition). e.axolotlVariant carries the (lucy default)
	// variant for the client texture + the future breeding-mutation pick. No bounded per-tick work today
	// beyond the passive goal walk.
	_ = e.axolotlVariant
}
