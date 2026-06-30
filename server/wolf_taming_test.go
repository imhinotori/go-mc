package server

// wolf_taming_test.go — MOB-NEUT-02 (Phase 36-02): the focused tests for the Wolf.mobInteract taming
// port (tryWolfInteract / tryToTameWolf / applyWolfTamingSideEffects) and the tamed-owner sit-toggle.
// The taming roll is a single mobRandom(e).nextInt(3); the wolf's per-entity rng is seeded directly so
// a reference entityRandom with the SAME seed predicts the outcome (the lockstep draw-count vehicle the
// Phase-35 target tests use). NO client is attached — the broadcast/inventory-sync paths short-circuit
// on a nil client, so these exercise the pure state mutation (isTame / HP 8->40 / owner / orderedToSit /
// bone consume) directly.
//
// The pig oracle (TestPluginPigEqualsGoNativePig) is a SEPARATE, untouched mob — these tests build wolves
// only, so the oracle stream is unperturbed.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// wolfTameSuccessSeed makes mobRandom(e).nextInt(3) == 0 (tame SUCCESS); wolfTameFailSeed makes it != 0
// (tame FAIL). Both are confirmed against newEntityRandom(seed) by the in-test reference rng below, so the
// constants are self-validating (a regression in the rng implementation fails the precondition assert, not
// silently the behavior assert).
const (
	wolfTameSuccessSeed uint64 = 9
	wolfTameFailSeed    uint64 = 1
)

// newTestWolf builds a live wolf at the origin with the wolfSupplier-backed AttributeMap (MAX_HEALTH 8.0),
// spawn health initialized to 8, and a goal-less mobAI carrying a per-entity rng seeded with `seed` so the
// single tryToTame nextInt(3) draw is deterministic. The wolf is UNTAMED, non-angry, ownerless (the zero
// state == an untamed wild wolf).
func newTestWolf(seed uint64) *Entity {
	e := NewEntity(7100, entity.Wolf, 0, 64, 0)
	initSpawnHealth(e) // setHealth(getMaxHealth()) -> 8.0 (the untamed wolf base)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(seed)
	return e
}

// newTestPlayerHolding registers a player with entity id `eid` at the origin holding `itemID` (count 1) in
// the selected main-hand slot. A count-0 / itemID-0 slot models an empty hand (pass itemID < 0 to leave the
// hand empty).
func newTestPlayerHolding(loop *TickLoop, eid int32, itemID int32) *tickPlayer {
	p := &tickPlayer{x: 0, y: 64, z: 0, entityID: eid}
	inv := ensureInventory(p)
	if itemID >= 0 {
		inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
	}
	loop.players = append(loop.players, p)
	return p
}

// TestWolfTameSuccess: an untamed non-angry wolf right-clicked with a BONE on a tame-SUCCESS roll
// (nextInt(3)==0) consumes the bone, becomes TAME, gets MAX_HEALTH bumped 8->40 + full heal to 40, records
// the owner, and is set orderedToSit(true). tryWolfInteract returns true (the interact was consumed).
func TestWolfTameSuccess(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(wolfTameSuccessSeed)
	p := newTestPlayerHolding(loop, 42, int32(item.Bone.ID))

	// Precondition: the seed yields nextInt(3)==0 (tame). A reference rng with the SAME seed must roll 0.
	if r := newEntityRandom(wolfTameSuccessSeed).nextInt(3); r != 0 {
		t.Fatalf("precondition: wolfTameSuccessSeed nextInt(3) = %d, want 0 (tame-success seed)", r)
	}
	if wolf.tame {
		t.Fatalf("precondition: a freshly-spawned wolf must be UNTAMED")
	}
	if wolf.health != 8.0 {
		t.Fatalf("precondition: untamed wolf spawn health = %v, want 8.0", wolf.health)
	}

	consumed := loop.tryWolfInteract(p, wolf)
	if !consumed {
		t.Fatalf("tryWolfInteract on a BONE-fed untamed wolf returned false, want true (interact consumed)")
	}
	if !wolf.tame {
		t.Fatalf("wolf.tame = false after a tame-success BONE feed, want true (isTame)")
	}
	if wolf.health != 40.0 {
		t.Fatalf("tamed wolf health = %v, want 40.0 (applyTamingSideEffects full heal)", wolf.health)
	}
	if mh := wolf.getAttributeValue(attribute.MaxHealth); mh != 40.0 {
		t.Fatalf("tamed wolf MAX_HEALTH = %v, want 40.0 (setBaseValue(40.0) 8->40 bump)", mh)
	}
	if wolf.ownerUUID != p.entityID {
		t.Fatalf("tamed wolf ownerUUID = %d, want %d (setOwner(player))", wolf.ownerUUID, p.entityID)
	}
	if !wolf.orderedToSit {
		t.Fatalf("tamed wolf orderedToSit = false, want true (tryToTame setOrderedToSit(true))")
	}
	// The bone was consumed (shrink 1 -> empty hand).
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("held slot after tame = %+v, want empty (stack.consume(1) ate the bone)", held)
	}
}

// TestWolfTameFailure: on a tame-FAIL roll (nextInt(3)!=0) the BONE is STILL consumed but the wolf does NOT
// tame — no isTame, no HP bump (stays 8), no owner. tryWolfInteract still returns true (the interact was
// consumed: the bone was eaten + tryToTame ran the smoke branch).
func TestWolfTameFailure(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(wolfTameFailSeed)
	p := newTestPlayerHolding(loop, 42, int32(item.Bone.ID))

	if r := newEntityRandom(wolfTameFailSeed).nextInt(3); r == 0 {
		t.Fatalf("precondition: wolfTameFailSeed nextInt(3) = 0, want != 0 (tame-fail seed)")
	}

	consumed := loop.tryWolfInteract(p, wolf)
	if !consumed {
		t.Fatalf("tryWolfInteract on a BONE feed returned false, want true (interact consumed even on tame fail)")
	}
	if wolf.tame {
		t.Fatalf("wolf.tame = true after a tame-FAIL feed, want false (smoke branch, no tame)")
	}
	if wolf.health != 8.0 {
		t.Fatalf("untamed (tame-fail) wolf health = %v, want 8.0 (no applyTamingSideEffects)", wolf.health)
	}
	if wolf.ownerUUID != 0 {
		t.Fatalf("tame-fail wolf ownerUUID = %d, want 0 (no setOwner)", wolf.ownerUUID)
	}
	if wolf.orderedToSit {
		t.Fatalf("tame-fail wolf orderedToSit = true, want false (the sit only fires on tame success)")
	}
	// The bone was consumed regardless (stack.consume(1) precedes tryToTame in mobInteract).
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("held slot after tame-fail = %+v, want empty (the bone is consumed before tryToTame)", held)
	}
}

// TestWolfTameDrawsExactlyOneNextInt3: tryToTameWolf draws EXACTLY ONE nextInt(3). Proven by lockstep with
// a reference rng seeded identically: the reference draws ONE nextInt(3) (the tame roll); after the feed,
// the wolf's rng and the reference must agree on the NEXT draw iff tryToTame consumed exactly one nextInt(3).
// A second draw (or a different bound) would desynchronize the follow-up.
func TestWolfTameDrawsExactlyOneNextInt3(t *testing.T) {
	const seed = wolfTameFailSeed // any seed works; the fail seed keeps the wolf untamed (no side effects)
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(seed)
	p := newTestPlayerHolding(loop, 42, int32(item.Bone.ID))

	ref := newEntityRandom(seed)
	_ = ref.nextInt(3) // the ONE tame roll tryToTame draws

	loop.tryWolfInteract(p, wolf)

	// After the feed, the wolf's rng and the reference must produce the SAME next value iff the goal
	// consumed exactly one nextInt(3) (the lockstep proof; the bound is irrelevant, any matching draw works).
	const probeBound = 1000
	got := mobRandom(wolf).nextInt(probeBound)
	want := ref.nextInt(probeBound)
	if got != want {
		t.Fatalf("post-feed rng desync: wolf nextInt(%d)=%d, reference=%d — tryToTame did NOT draw exactly one nextInt(3)", probeBound, got, want)
	}
}

// TestWolfBoneRequiredToTame: a non-BONE held item on an untamed wolf is NOT a taming feed — tryWolfInteract
// returns false (falls through to tryFeedAnimal), the wolf stays untamed, and NO RNG is drawn (the bone gate
// precedes the nextInt(3)). Proven by the rng being undisturbed (a reference seeded identically still leads).
func TestWolfBoneRequiredToTame(t *testing.T) {
	const seed = wolfTameSuccessSeed
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(seed)
	// A stick (id 887 carrot_on_a_stick would be wolf food? no — use a plain non-bone, non-food item: dirt id 1).
	p := newTestPlayerHolding(loop, 42, 1)

	ref := newEntityRandom(seed)

	consumed := loop.tryWolfInteract(p, wolf)
	if consumed {
		t.Fatalf("tryWolfInteract with a non-BONE held item returned true, want false (falls through to feed)")
	}
	if wolf.tame {
		t.Fatalf("wolf tamed on a non-BONE interact, want untamed")
	}
	// The bone gate precedes nextInt(3): the wolf's rng must be UNTOUCHED (lockstep with a fresh reference).
	if got, want := mobRandom(wolf).nextInt(3), ref.nextInt(3); got != want {
		t.Fatalf("non-BONE interact drew RNG: wolf nextInt(3)=%d, fresh reference=%d — the bone gate must precede the draw", got, want)
	}
	// The held item is untouched (no consume).
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); slotIsEmpty(held) || int32(held.ItemID) != 1 {
		t.Fatalf("held slot after a non-BONE interact = %+v, want the dirt unchanged (no consume)", held)
	}
}

// TestWolfAngryCannotTame: an ANGRY wolf (angerEndTime live) right-clicked with a BONE does NOT tame —
// tryWolfInteract returns false (the !isAngry() gate fails -> super.mobInteract), no RNG is drawn, the bone
// is NOT consumed.
func TestWolfAngryCannotTame(t *testing.T) {
	const seed = wolfTameSuccessSeed
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(seed)
	p := newTestPlayerHolding(loop, 42, int32(item.Bone.ID))

	// Make the wolf angry: angerEndTime > gametime (NeutralMob.isAngry true). loop.gametime starts at 0.
	wolf.angerEndTime = loop.gametime + 100
	if !loop.wolfIsAngry(wolf) {
		t.Fatalf("precondition: wolf must be angry (angerEndTime %d > gametime %d)", wolf.angerEndTime, loop.gametime)
	}

	ref := newEntityRandom(seed)

	consumed := loop.tryWolfInteract(p, wolf)
	if consumed {
		t.Fatalf("tryWolfInteract on an ANGRY wolf returned true, want false (isAngry gate -> super.mobInteract)")
	}
	if wolf.tame {
		t.Fatalf("angry wolf tamed, want untamed (an angry wolf cannot be tamed)")
	}
	// The isAngry gate precedes nextInt(3): the rng must be untouched.
	if got, want := mobRandom(wolf).nextInt(3), ref.nextInt(3); got != want {
		t.Fatalf("angry interact drew RNG: wolf nextInt(3)=%d, reference=%d — the isAngry gate must precede the draw", got, want)
	}
	// The bone is NOT consumed (the gate returns before stack.consume).
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); slotIsEmpty(held) || int32(held.ItemID) != int32(item.Bone.ID) {
		t.Fatalf("held slot after an angry interact = %+v, want the BONE unchanged (no consume)", held)
	}
}

// TestWolfSitToggleByOwner: a TAMED wolf right-clicked with an EMPTY hand by its OWNER toggles orderedToSit
// each time (false->true->false), and tryWolfInteract returns true (the sit-toggle consumed the interact).
func TestWolfSitToggleByOwner(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(wolfTameFailSeed) // seed irrelevant: the sit-toggle path draws NO rng
	// Make it a tamed wolf owned by player 42 (the post-tame state), starting NOT ordered to sit.
	wolf.tame = true
	wolf.ownerUUID = 42
	wolf.orderedToSit = false
	p := newTestPlayerHolding(loop, 42, -1) // empty hand (no food)

	if consumed := loop.tryWolfInteract(p, wolf); !consumed {
		t.Fatalf("owner empty-hand interact on a tamed wolf returned false, want true (sit-toggle consumes)")
	}
	if !wolf.orderedToSit {
		t.Fatalf("first owner interact: orderedToSit = false, want true (toggle false->true)")
	}
	if consumed := loop.tryWolfInteract(p, wolf); !consumed {
		t.Fatalf("second owner interact returned false, want true")
	}
	if wolf.orderedToSit {
		t.Fatalf("second owner interact: orderedToSit = true, want false (toggle true->false)")
	}
}

// TestWolfSitToggleNonOwnerDenied: a TAMED wolf right-clicked with an EMPTY hand by a NON-owner does NOT
// sit-toggle — tryWolfInteract returns false (the !isOwnedBy gate -> super result stands; T-36-05). The
// orderedToSit state is unchanged.
func TestWolfSitToggleNonOwnerDenied(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(wolfTameFailSeed)
	wolf.tame = true
	wolf.ownerUUID = 42 // owned by player 42
	wolf.orderedToSit = false
	stranger := newTestPlayerHolding(loop, 999, -1) // a DIFFERENT player, empty hand

	if consumed := loop.tryWolfInteract(stranger, wolf); consumed {
		t.Fatalf("non-owner interact on a tamed wolf returned true, want false (owner gate denies the sit-toggle)")
	}
	if wolf.orderedToSit {
		t.Fatalf("non-owner interact toggled orderedToSit, want unchanged false (a non-owner cannot command the wolf)")
	}
}

// TestWolfTamedFoodFallsThroughToFeed: a TAMED wolf right-clicked with WOLF_FOOD does NOT sit-toggle — the
// food path (super.mobInteract) handles it, so tryWolfInteract returns false (falls through to tryFeedAnimal)
// and orderedToSit is unchanged. This pins the `if (r.consumesAction()) return r` (no sit-toggle when the
// feed consumed) modeling.
func TestWolfTamedFoodFallsThroughToFeed(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	wolf := newTestWolf(wolfTameFailSeed)
	wolf.tame = true
	wolf.ownerUUID = 42
	wolf.orderedToSit = false
	// A WOLF_FOOD item (id 1011 is the first wolf_food tag entry, data/tag/tags.go:375).
	p := newTestPlayerHolding(loop, 42, 1011)
	if !itemInTag(1011, "wolf_food") {
		t.Fatalf("precondition: item 1011 must be in the wolf_food tag")
	}

	if consumed := loop.tryWolfInteract(p, wolf); consumed {
		t.Fatalf("tamed owner interact with WOLF_FOOD returned true, want false (the feed path handles it)")
	}
	if wolf.orderedToSit {
		t.Fatalf("a food interact triggered the sit-toggle, want unchanged false (food -> super.mobInteract, no toggle)")
	}
}
