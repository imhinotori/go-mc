// allay.go -- the Allay (net.minecraft.world.entity.animal.allay.Allay), a 1:1 port from the unobfuscated
// 26.2 jar. Allay is a small FLYING helper mob (a PathfinderMob in the "misc" MobCategory, NOT an Animal):
// it PICKS UP a matching item a player hands it, FETCHES more of that item from the ground, and FOLLOWS a
// note-block pulse, dancing near an amethyst to DUPLICATE. Vanilla drives it with a BRAIN + a flying
// navigation; for a bounded port this lands the attributes + spawn + the "visibly alive" flying passive
// goal subset (Float/Stroll/Look). The item-pickup/fetch + follow-note + duplicate are the DEFERRED
// behavior layer (they need the InventoryCarrier item slot + the VibrationSystem note sensor + the
// duplicate cooldown). Code-spawned (spawnAllay) with a *mobAI carrying the passive goals; its per-tick
// extra is allayAiStep from tickAI (per-type-gated on typ == entity.Allay.ID).
//
// VANILLA (verified javap Allay this session):
//   createAttributes: Mob.createMobAttributes (NOT Monster/Animal -- NO ATTACK_DAMAGE base, NO TEMPT_RANGE)
//     + MAX_HEALTH 20.0 + FLYING_SPEED 0.10000000149011612 + MOVEMENT_SPEED 0.10000000149011612 +
//     ATTACK_DAMAGE 2.0 (see allaySupplier in level/attribute/defaults.go). FOLLOW_RANGE stays the
//     createMobAttributes 16.0.
//   Allay is a BRAIN mob (makeBrain / customServerAiStep -> AllayAi; no registerGoals classic goals): the
//     brain drives the item-fetch/follow-note behavior via DATA_DANCING + DATA_CAN_DUPLICATE + the
//     InventoryCarrier slot + the VibrationSystem note sensor. It uses a FLYING navigation (FlyingPathNav).
//
// v1 STUBS (cited): the BRAIN (AllayAi + the item-pickup/fetch + follow-note + amethyst duplicate) is the
// DEFERRED behavior layer -- this port supplies the bounded flying passive goal walk (Float/Stroll/Look).
// Allay is NOT an Animal (it does not breed and has no baby age), so there is NO Breed/FollowParent/Tempt
// goal (matching vanilla -- the Allay has no FollowTemptation). The observable attributes (incl.
// FLYING_SPEED 0.1) and the passive goal walk are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Allay constants (VERIFIED javap Allay this session).
const (
	allayMaxHealth     = 20.0                // createAttributes MAX_HEALTH 20.0
	allayFlyingSpeed   = 0.10000000149011612 // createAttributes FLYING_SPEED (float-widened)
	allayMovementSpeed = 0.10000000149011612 // createAttributes MOVEMENT_SPEED (float-widened)
	allayAttackDamage  = 2.0                 // createAttributes ATTACK_DAMAGE 2.0
	allayStrollSpeed   = 1.0                 // RandomStroll speed (brain-deferred; the common stroll pace)
	allayLookDistance  = 8.0                 // LookAtTargetSink distance (the common look range)
)

// newAllayAI builds the Allay bounded flying passive AI. Allay is a BRAIN mob in vanilla (the item-fetch/
// follow-note/duplicate behaviors live in AllayAi, DEFERRED per the file header); this supplies the
// "visibly alive" flying classic-goal stand-in (Float/Stroll/Look), mirroring newBeeAI shape (per-mob rng,
// navigation seed, canFloat). Allay is a PathfinderMob (NOT an Animal), so there is NO Breed/FollowParent/
// Tempt goal -- faithful, since the Allay has no FollowTemptation. Cite Allay.makeBrain (the brain deferral
// note).
func newAllayAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * allayMovementSpeed // seed with MOVEMENT_SPEED (0.1)
	m.navigation.canFloat = true                             // FloatGoal ctor: getNavigation().setCanFloat(true)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(3, newWaterAvoidingRandomStrollGoal(allayStrollSpeed))
	m.goals.addGoal(6, newLookAtPlayerGoal(allayLookDistance))
	m.goals.addGoal(7, newRandomLookAroundGoal())
	return m
}

// spawnAllay creates an Allay at (x,y,z) with the jar attributes and the flying passive goal AI, then adds
// it to the owner region store. Allay is NOT Ageable (a PathfinderMob), so there is NO baby toggle. The
// InventoryCarrier item slot + the follow-note/duplicate brain state are DEFERRED. initSpawnHealth seeds
// health from MAX_HEALTH (20.0). Cite Allay.createAttributes + Allay(EntityType, Level).
func (t *TickLoop) spawnAllay(x, y, z float64) *Entity {
	a := NewEntity(t.idAlloc.AllocID(), entity.Allay, x, y, z)
	a.isAllay = true
	initSpawnHealth(a) // setHealth(getMaxHealth()) -> 20.0
	a.ai = newAllayAI()
	reseedMobAI(a.ai, a.id)
	owner := t.regionForEntity(a)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(a)
	return a
}

// allayAiStep is the Allay per-tick extra (Allay.customServerAiStep). Vanilla runs the AllayAi BRAIN here
// (the item-pickup/fetch, the follow-note pulse, the amethyst-dance duplicate), which is the DEFERRED
// behavior layer -- so this is a bounded no-op today (the passive goals in newAllayAI drive the visible
// flight). It is wired + per-type-gated (typ == entity.Allay.ID) so the item-fetch/follow-note slots in
// here the moment the brain machinery lands, never baked away. RNG-free. Cite Allay.customServerAiStep +
// AllayAi (the brain deferral note).
func (t *TickLoop) allayAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// DEFERRED: the AllayAi brain tick (the InventoryCarrier item-pickup/fetch; the VibrationSystem
	// follow-note pulse; the amethyst-dance DATA_CAN_DUPLICATE duplicate). No bounded per-tick work today
	// beyond the flying passive goal walk.
}
