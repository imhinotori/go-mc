// parrot.go -- the Parrot (net.minecraft.world.entity.animal.parrot.Parrot), a 1:1 port from the
// unobfuscated 26.2 jar. Parrot is a FLYING TamableAnimal (ShoulderRidingEntity -> TamableAnimal ->
// Animal) with FIVE plumage variants; it is tamed with seeds (ItemTags.PARROT_FOOD) at a 1-in-10
// chance, follows/sits for its owner, and -- once tamed -- can land on the owners shoulder. It flies
// via a FlyingMoveControl + FlyingPathNavigation (a SOFT flyer: normal gravity, but the navigation
// paths through air), so its physics are the ordinary Mob travel (like the bee) -- NOT the no-gravity
// ghast branch. This port lands the attributes + spawn (with the 5-variant roll) + the flying-nav
// passive goal subset (Panic/Float/LookAtPlayer/SitWhenOrdered/FollowOwner/Wander) + the SIGNATURE
// tame-with-seeds + the shoulder-perch flag. Code-spawned (spawnParrot) with a *mobAI carrying the
// passive goals; its per-tick extra is parrotAiStep from tickAI (per-type-gated on typ ==
// entity.Parrot.ID).
//
// VANILLA (verified javap Parrot this task):
//   createAttributes: Animal.createAnimalAttributes + MAX_HEALTH 6.0 + FLYING_SPEED 0.4000000059604645 +
//     MOVEMENT_SPEED 0.20000000298023224 + ATTACK_DAMAGE 3.0 (FOLLOW_RANGE stays createMobAttributes 16).
//   ctor: moveControl = new FlyingMoveControl(this, 10, false).
//   createNavigation: new FlyingPathNavigation(this, level); setCanOpenDoors(false); setCanFloat(true).
//   isFlying(): !onGround().
//   registerGoals goalSelector: @0 TamableAnimalPanicGoal(1.25); @0 FloatGoal; @1 LookAtPlayerGoal(Player,
//     8.0); @2 SitWhenOrderedToGoal; @2 FollowOwnerGoal(1.0, 5.0, 1.0); @2 ParrotWanderGoal(1.0);
//     @3 LandOnOwnersShoulderGoal; @3 FollowMobGoal(1.0, 3.0, 7.0).
//   Variant enum: RED_BLUE 0, BLUE 1, GREEN 2, YELLOW_BLUE 3, GRAY 4 (Parrot.Variant.byId).
//   mobInteract: if (!isTame() and stack.is(PARROT_FOOD)) usePlayerItem; if(!clientSide and nextInt(10)==0)
//     tame(player) [byte 7 hearts] else [byte 6 smoke]; return SUCCESS;  (the seed tame, 1-in-10).
//   LandOnOwnersShoulderGoal.canUse: not-silent (deferred sound) ... and mob.isTame() ... it perches
//     when the tamed parrot reaches its standing owner (setEntityOnShoulder), a client-visual perch.
//
// v1 STUBS (cited): the imitate-nearby-hostile ambient sound (MOB_SOUND_MAP), the dance-near-jukebox
// client cue, and the FollowMobGoal @3 idle-following are DEFERRED client-visual / low-signal goals. The
// shoulder RIDE itself (setEntityOnShoulder writing the players ShoulderLeft/Right NBT + the client
// perch render) is a cited client-visual deferral -- parrotPerched marks the GAMEPLAY perch intent so it
// lands when the shoulder-ride NBT sync exists. The observable attributes, the 5-variant plumage, the
// flying passive walk, and the SIGNATURE seed-tame are EXACT.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Parrot constants (VERIFIED javap Parrot this task).
const (
	parrotMaxHealth            = 6.0                     // createAttributes MAX_HEALTH 6.0
	parrotPanicSpeed           = 1.25                    // @0 TamableAnimalPanicGoal(this, 1.25) speedModifier (ldc2_w 1.25d)
	parrotLookDistance         = 8.0                     // @1 LookAtPlayerGoal(Player, 8.0f) lookDistance (ldc 8.0f)
	parrotFollowSpeed          = 1.0                     // @2 FollowOwnerGoal(this, 1.0, 5.0, 1.0) speedModifier (dconst_1)
	parrotWanderSpeed          = 1.0                     // @2 ParrotWanderGoal(this, 1.0) speedModifier (dconst_1)
	parrotTameChance           = 10                      // mobInteract: nextInt(10) == 0 tame roll (bipush 10)
	parrotFoodTag              = "parrot_food"           // ItemTags.PARROT_FOOD (the seed tame predicate)
	parrotPoisonousFoodTag     = "parrot_poisonous_food" // ItemTags.PARROT_POISONOUS_FOOD (cookie -> poison + death)
	parrotCookiePoisonDuration = 900                     // MobEffectInstance(POISON, 900) applied by the cookie feed (sipush 900)
	parrotVariantCount         = 5                       // Parrot.Variant: RED_BLUE/BLUE/GREEN/YELLOW_BLUE/GRAY (byId 0..4)
	// parrotFlyingSpeed is the Parrot FLYING_SPEED attribute base (0.4000000059604645), the fly-nav seed.
	parrotFlyingSpeed = 0.4000000059604645
)

// newParrotAI builds the Parrot passive FLYING AI: the "visibly alive" subset of Parrot.registerGoals
// (the imitate-sound / dance / FollowMob are DEFERRED, cited in the file header). Mirrors newBeeAI shape
// (per-mob rng, navigation seed, canFloat, Animal pathfinding malus) with the Parrot goal priorities:
//
//	0  TamableAnimalPanicGoal(1.25)        [MOVE]   (v1 panicGoal analogue)
//	0  FloatGoal                           [JUMP]
//	1  LookAtPlayerGoal(Player, 8.0)       [LOOK]
//	2  SitWhenOrderedToGoal                [JUMP|MOVE]
//	2  FollowOwnerGoal(1.0)                [MOVE]
//	2  ParrotWanderGoal(1.0)               [MOVE]   (WaterAvoidingRandomStroll flying analogue)
//
// The navigation is seeded with FLYING_SPEED as the fly nav base (createNavigation = FlyingPathNavigation
// + setCanFloat(true)); the parrots physics stay the ordinary Mob travel (a SOFT flyer, gravity applies).
// Cite Parrot.registerGoals + Parrot.createNavigation.
func newParrotAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * parrotFlyingSpeed // FlyingPathNavigation base (FLYING_SPEED 0.4)
	m.navigation.canFloat = true                            // createNavigation: setCanFloat(true)
	applyAnimalPathfindingMalus(m)                          // Animal.<init> FIRE malus (no-op on a fire-free world)
	m.goals.addGoal(0, newPanicGoal(parrotPanicSpeed))      // TamableAnimalPanicGoal analogue
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newLookAtPlayerGoal(parrotLookDistance))
	m.goals.addGoal(2, newSitWhenOrderedToGoal())
	m.goals.addGoal(2, newFollowOwnerGoal(parrotFollowSpeed))
	m.goals.addGoal(2, newWaterAvoidingRandomStrollGoal(parrotWanderSpeed))
	return m
}

// spawnParrot creates a Parrot at (x,y,z) with the jar attributes, a random one of the 5 plumage
// variants (setVariant(Variant.byId(random.nextInt(5))) at finalizeSpawn), and the passive flying goal
// AI, then adds it to the owner region store. NOT tamed (a wild parrot). initSpawnHealth seeds health
// from MAX_HEALTH (6.0). Cite Parrot.createAttributes + Parrot.finalizeSpawn (the variant roll) +
// Parrot(EntityType, Level).
func (t *TickLoop) spawnParrot(x, y, z float64) *Entity {
	pr := NewEntity(t.idAlloc.AllocID(), entity.Parrot, x, y, z)
	pr.isParrot = true
	initSpawnHealth(pr) // setHealth(getMaxHealth()) -> 6.0
	pr.ai = newParrotAI()
	reseedMobAI(pr.ai, pr.id)
	// finalizeSpawn: setVariant(Util.getRandom(Variant.values(), level.getRandom())) ==
	// Variant.values()[level.getRandom().nextInt(5)]. The draw is on level.getRandom() (the
	// ServerLevelAccessor.getRandom stream == t.cur().levelRandom) -- NOT the parrot's per-entity
	// stream. This is load-bearing for co-spawn RNG lockstep. Cite Parrot.finalizeSpawn + Util.getRandom.
	pr.parrotVariant = int32(t.cur().levelRandom.NextIntN(int32(parrotVariantCount)))
	owner := t.regionForEntity(pr)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(pr)
	return pr
}

// parrotIsFlying ports Parrot.isFlying(): !onGround(). A parrot off the ground is flying (drives the
// wing-flap client anim; the GAMEPLAY reads it for the LandOnOwnersShoulderGoal perch gate). No RNG.
func parrotIsFlying(e *Entity) bool { return !e.onGround }

// tryParrotInteract ports Parrot.mobInteract seed-tame branch: an UNTAMED parrot right-clicked with a
// PARROT_FOOD (seed) item consumes 1 seed then rolls a 1-in-10 tame (nextInt(10) == 0 -> tame + hearts;
// else smoke). Returns true when the interact belongs to the parrot (a seed was consumed) so handle
// Interact does NOT fall through to the feed path; false otherwise (fall through). A TAMED parrot
// owner empty-hand click toggles the sit order (super.mobInteract sit-toggle tail). Parrot-gated so it
// is a zero-cost no-op for every other mob; the lone nextInt(10) tame draw is on the parrots OWN
// per-entity stream (the pig oracle is unperturbed). Cite Parrot.mobInteract.
func (t *TickLoop) tryParrotInteract(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))

	// Parrot.mobInteract cookie branch (reached for BOTH tamed and untamed parrots when the held item is
	// PARROT_POISONOUS_FOOD == cookie, AFTER the !isTame&&PARROT_FOOD seed-tame branch): usePlayerItem;
	// addEffect(new MobEffectInstance(POISON, 900)); if (!player.isCreative() && !isInvulnerable())
	// hurt(damageSources().playerAttack(player), Float.MAX_VALUE) -> instant death. Return SUCCESS. The
	// isInvulnerable() guard is always false for a normally-spawned parrot (no invulnerable NBT flag ported),
	// so the observable behavior collapses to the !isCreative() gate. RNG-free. Cite Parrot.mobInteract.
	if !slotIsEmpty(held) && itemInTag(int32(held.ItemID), parrotPoisonousFoodTag) {
		t.shrinkHeldItem(p, inv) // usePlayerItem: consume 1 cookie (survival) BEFORE the effect+damage
		t.addEntityEffect(mob, effectPoison, parrotCookiePoisonDuration, 0)
		if p.gameMode != gameModeCreative {
			// hurt(playerAttack(player), Float.MAX_VALUE): the finite-clamped huge value is an instant kill.
			t.applyDamageEntity(mob, damageSourcePlayerAttack(p.entityID), maxFloat32)
		}
		return true
	}

	if mob.tame {
		// A non-owner cannot command a tamed parrot (isOwnedBy gate). Fall through to the super feed.
		if mob.ownerUUID != p.entityID {
			return false
		}
		// A PARROT_FOOD held over a tamed parrot: vanilla tame branch is !isTame(), so a tamed parrot
		// is NOT re-fed here (the POISONOUS_FOOD cookie-death branch is a cited deferral). Any held food
		// falls through so the owner empty-hand click reaches the sit-toggle. NO RNG.
		if !slotIsEmpty(held) && itemInTag(int32(held.ItemID), parrotFoodTag) {
			return false
		}
		// super.mobInteract sit-toggle tail: setOrderedToSit(!isOrderedToSit()) on a consumed owner action.
		mob.orderedToSit = !mob.orderedToSit
		mob.setJumping(false)
		if mob.ai != nil {
			mob.ai.setTarget(0)
		}
		return true
	}

	// UNTAMED: if (stack.is(PARROT_FOOD)) usePlayerItem; tame roll; return SUCCESS;.
	if slotIsEmpty(held) || !itemInTag(int32(held.ItemID), parrotFoodTag) {
		return false // not a seed -> fall through to super feed
	}
	// usePlayerItem: consume 1 seed (survival) BEFORE the roll, exactly as the bytecode orders it.
	t.shrinkHeldItem(p, inv)
	t.tryToTameParrot(p, mob)
	return true
}

// tryToTameParrot ports Parrot.mobInteract tame roll VERBATIM: ONE nextInt(10) on the parrots
// per-entity stream; on 0 -> tame(player) (setTame(true,true) [base no-op side-effect, so NO health
// change] + setOwner) + hearts (byte 7); else smoke (byte 6). Cite Parrot.mobInteract.
func (t *TickLoop) tryToTameParrot(p *tickPlayer, mob *Entity) {
	if mobRandom(mob).nextInt(parrotTameChance) == 0 { // nextInt(10) == 0
		// tame(player) = setTame(true, true) + setOwner. Parrot.applyTamingSideEffects is the base no-op,
		// so MAX_HEALTH stays 6 (no health bump). Flip the tame flag + record the owner.
		mob.tame = true
		mob.ownerUUID = p.entityID
		// ADVANCEMENTS (advancements.go): TamableAnimal.tame -> CriteriaTriggers.TAME_ANIMAL
		// .trigger((ServerPlayer)player, this). tame_animal has an empty predicate -> grants
		// husbandry/tame_an_animal. CITE: TamableAnimal.tame.
		t.triggerTameAnimal(p)
		// broadcastEntityEvent(this, (byte)7): the taming-SUCCESS HEART burst.
		t.broadcastToTrackers(mob.id, encodeEntityEvent(mob.id, entityEventWolfTameHearts))
		return
	}
	// broadcastEntityEvent(this, (byte)6): the taming-FAIL SMOKE puff. The seed was still consumed.
	t.broadcastToTrackers(mob.id, encodeEntityEvent(mob.id, entityEventWolfTameSmoke))
}

// parrotAiStep is the Parrot per-tick extra, driven per-type from tickAI (gated on typ ==
// entity.Parrot.ID, AFTER serverAiStep). Parrot has NO customServerAiStep override in the jar (its
// behavior is entirely goal-driven); the per-tick extra here is the SHOULDER-PERCH bookkeeping (the
// LandOnOwnersShoulderGoal gameplay). A tamed, grounded, not-sitting parrot with an owner is marked
// perched (parrotPerched) -- the observable perch intent. The actual shoulder-ride NBT sync + the
// client perch render are cite-deferred (see the file header). RNG-free (no vanilla RNG in this limb).
// Cite Parrot (no customServerAiStep) + LandOnOwnersShoulderGoal.
func (t *TickLoop) parrotAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	// LandOnOwnersShoulderGoal intent: a TAMED parrot that is grounded (!isFlying) and NOT ordered to sit
	// can perch when close to its owner. The perch flag is the GAMEPLAY landing; the shoulder-ride NBT
	// write is the cited client deferral. A wild / flying / sitting parrot is never perched.
	e.parrotPerched = e.tame && !parrotIsFlying(e) && !e.orderedToSit && e.ownerUUID != 0
}
