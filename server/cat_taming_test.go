package server

// cat_taming_test.go — MOB-NEUT-03 (Task #9): the Cat.mobInteract taming port (tryCatInteract /
// tryToTameCat) + the tamed-owner sit-toggle. The cat sibling of the wolf taming tests: SAME 1-in-3
// nextInt(3) roll, but tamed by FISH (cat_food = cod/salmon) and — unlike the wolf — NO health bump
// (Cat.applyTamingSideEffects is the base no-op, MAX_HEALTH stays 10). No client attached, so the
// broadcast paths short-circuit and these exercise the pure state mutation. Pig oracle untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/attribute"
)

// newTestCat builds a live untamed cat with the catSupplier map (MAX_HEALTH 10), spawn health 10, and a
// per-entity rng seeded so the single tryToTame nextInt(3) is deterministic (reusing the wolf seeds).
func newTestCat(seed uint64) *Entity {
	e := NewEntity(7200, entity.Cat, 0, 64, 0)
	initSpawnHealth(e) // setHealth(getMaxHealth()) -> 10.0
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(seed)
	return e
}

// TestCatTameSuccess: an untamed cat right-clicked with COD on a tame-SUCCESS roll (nextInt(3)==0) consumes
// the fish, becomes TAME, records the owner, is orderedToSit(true), and its health STAYS 10 (no wolf-style
// bump). tryCatInteract returns true.
func TestCatTameSuccess(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(wolfTameSuccessSeed)
	p := newTestPlayerHolding(loop, 42, int32(item.Cod.ID))

	if r := newEntityRandom(wolfTameSuccessSeed).nextInt(3); r != 0 {
		t.Fatalf("precondition: seed nextInt(3) = %d, want 0 (tame-success)", r)
	}
	if cat.tame {
		t.Fatal("precondition: a freshly-spawned cat must be UNTAMED")
	}
	if cat.health != 10.0 {
		t.Fatalf("precondition: untamed cat spawn health = %v, want 10.0", cat.health)
	}

	consumed := loop.tryCatInteract(p, cat)
	if !consumed {
		t.Fatal("tryCatInteract on a COD-fed untamed cat returned false, want true (interact consumed)")
	}
	if !cat.tame {
		t.Fatal("cat.tame = false after a tame-success COD feed, want true")
	}
	// The cat MUST NOT get a health bump (Cat.applyTamingSideEffects is the base no-op).
	if cat.health != 10.0 {
		t.Fatalf("tamed cat health = %v, want 10.0 (NO wolf-style bump — Cat side-effect is a no-op)", cat.health)
	}
	if mh := cat.getAttributeValue(attribute.MaxHealth); mh != 10.0 {
		t.Fatalf("tamed cat MAX_HEALTH = %v, want 10.0 (unchanged)", mh)
	}
	if cat.ownerUUID != p.entityID {
		t.Fatalf("tamed cat ownerUUID = %d, want %d (setOwner)", cat.ownerUUID, p.entityID)
	}
	if !cat.orderedToSit {
		t.Fatal("tamed cat orderedToSit = false, want true (tryToTame setOrderedToSit(true))")
	}
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("held slot after tame = %+v, want empty (the fish was consumed)", held)
	}
}

// TestCatTameFailure: on a tame-FAIL roll the fish is still consumed but the cat does NOT tame.
func TestCatTameFailure(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(wolfTameFailSeed)
	p := newTestPlayerHolding(loop, 42, int32(item.Cod.ID))

	if r := newEntityRandom(wolfTameFailSeed).nextInt(3); r == 0 {
		t.Fatalf("precondition: seed nextInt(3) = 0, want != 0 (tame-fail)")
	}
	consumed := loop.tryCatInteract(p, cat)
	if !consumed {
		t.Fatal("tryCatInteract on a fish-fed untamed cat returned false, want true (fish eaten)")
	}
	if cat.tame {
		t.Fatal("cat.tame = true after a tame-FAIL feed, want false")
	}
	if held := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)); !slotIsEmpty(held) {
		t.Fatalf("held slot after fail = %+v, want empty (the fish was still consumed)", held)
	}
}

// TestCatSitToggle: a tamed cat right-clicked by its OWNER with an empty hand toggles orderedToSit.
func TestCatSitToggle(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(1)
	cat.tame = true
	cat.ownerUUID = 42
	p := newTestPlayerHolding(loop, 42, -1) // empty hand

	if cat.orderedToSit {
		t.Fatal("precondition: the tamed cat starts NOT ordered to sit")
	}
	if !loop.tryCatInteract(p, cat) {
		t.Fatal("owner empty-hand interact on a tamed cat returned false, want true (sit-toggle)")
	}
	if !cat.orderedToSit {
		t.Fatal("cat did not sit after the owner's empty-hand toggle")
	}
	// Toggle again → stands.
	loop.tryCatInteract(p, cat)
	if cat.orderedToSit {
		t.Fatal("cat did not stand after the second owner toggle")
	}
}

// TestCatSitToggleNonOwner: a non-owner cannot toggle a tamed cat's sit.
func TestCatSitToggleNonOwner(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	cat := newTestCat(1)
	cat.tame = true
	cat.ownerUUID = 42
	stranger := newTestPlayerHolding(loop, 99, -1) // different entity id, empty hand

	if loop.tryCatInteract(stranger, cat) {
		t.Fatal("a non-owner toggled a tamed cat's sit — the isOwnedBy gate failed")
	}
	if cat.orderedToSit {
		t.Fatal("a non-owner made the cat sit — must not happen")
	}
}
