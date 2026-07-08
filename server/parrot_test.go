package server

// parrot_test.go -- deterministic pins for the Parrot (net.minecraft.world.entity.animal.parrot.Parrot,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 6 / FLYING_SPEED 0.4 / MOVEMENT_SPEED
// 0.2 / ATTACK_DAMAGE 3 / FOLLOW_RANGE 16), the 5-variant plumage roll, and the SIGNATURE seed-tame
// (1-in-10 nextInt(10)==0 on the parrot own per-entity stream). The pig oracle is untouched.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// parrotLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick parrots.
func parrotLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestParrotSpawnDefaults: spawnParrot builds a parrot rendering as entity.Parrot.ID with the jar
// attributes and a valid one-of-five plumage variant.
func TestParrotSpawnDefaults(t *testing.T) {
	loop, floorY := parrotLoop(t)
	pr := loop.spawnParrot(8.5, float64(floorY+1), 8.5)
	if pr.typ != entity.Parrot.ID {
		t.Fatalf("parrot typ = %d, want entity.Parrot.ID %d", pr.typ, entity.Parrot.ID)
	}
	if !pr.isParrot {
		t.Fatal("parrot not marked isParrot")
	}
	if math.Abs(float64(pr.health)-6.0) > 1e-6 {
		t.Fatalf("parrot health = %v, want 6.0 (MAX_HEALTH)", pr.health)
	}
	if got := pr.getAttributeValue(attribute.MaxHealth); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("parrot MAX_HEALTH = %v, want 6.0", got)
	}
	if got := pr.getAttributeValue(attribute.FlyingSpeed); math.Abs(got-0.4000000059604645) > 1e-12 {
		t.Fatalf("parrot FLYING_SPEED = %v, want 0.4000000059604645", got)
	}
	if got := pr.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.20000000298023224) > 1e-12 {
		t.Fatalf("parrot MOVEMENT_SPEED = %v, want 0.20000000298023224", got)
	}
	if got := pr.getAttributeValue(attribute.AttackDamage); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("parrot ATTACK_DAMAGE = %v, want 3.0", got)
	}
	if got := pr.getAttributeValue(attribute.FollowRange); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("parrot FOLLOW_RANGE = %v, want 16.0 (createMobAttributes, no override)", got)
	}
	if pr.parrotVariant < 0 || pr.parrotVariant >= parrotVariantCount {
		t.Fatalf("parrot variant = %d, want one of 0..4", pr.parrotVariant)
	}
	if pr.tame {
		t.Fatal("a freshly-spawned parrot must be UNTAMED")
	}
	if pr.ai == nil || pr.ai.rng == nil {
		t.Fatal("parrot has no minimal AI / rng")
	}
}

// TestParrotSeedTameSuccess: an untamed parrot right-clicked with WHEAT_SEEDS on a tame-SUCCESS roll
// (nextInt(10)==0) consumes 1 seed, becomes TAME (no health bump -- MAX_HEALTH stays 6), and records the
// owner. tryParrotInteract returns true (the interact was consumed).
func TestParrotSeedTameSuccess(t *testing.T) {
	loop, floorY := parrotLoop(t)
	pr := loop.spawnParrot(0, float64(floorY+1), 0)
	// Force the tame roll deterministically: reseed the parrot rng to a seed whose first nextInt(10)==0.
	pr.ai.rng = newEntityRandom(20)
	if r := newEntityRandom(20).nextInt(10); r != 0 {
		t.Fatalf("precondition: seed 20 nextInt(10) = %d, want 0 (tame-success)", r)
	}
	p := newTestPlayerHolding(loop, 42, int32(item.WheatSeeds.ID))

	consumed := loop.tryParrotInteract(p, pr)
	if !consumed {
		t.Fatal("tryParrotInteract did not consume the seed interact on an untamed parrot")
	}
	if !pr.tame {
		t.Fatal("parrot did not become tame on the nextInt(10)==0 roll")
	}
	if pr.ownerUUID != p.entityID {
		t.Fatalf("parrot owner = %d, want the player %d", pr.ownerUUID, p.entityID)
	}
	if math.Abs(float64(pr.health)-6.0) > 1e-6 {
		t.Fatalf("parrot health changed on tame: %v, want 6.0 (no health bump)", pr.health)
	}
	// The held seed was consumed (Count decremented from 1 to 0).
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 0 {
		t.Fatalf("seed not consumed: held count = %d, want 0", got.Count)
	}
}

// TestParrotSeedTameFail: an untamed parrot right-clicked with WHEAT_SEEDS on a tame-FAIL roll
// (nextInt(10)!=0) still consumes the seed but does NOT tame. tryParrotInteract returns true (consumed).
func TestParrotSeedTameFail(t *testing.T) {
	loop, floorY := parrotLoop(t)
	pr := loop.spawnParrot(0, float64(floorY+1), 0)
	pr.ai.rng = newEntityRandom(1) // first nextInt(10) != 0 (tame-fail)
	if r := newEntityRandom(1).nextInt(10); r == 0 {
		t.Fatalf("precondition: seed 1 nextInt(10) = 0, want != 0 (tame-fail)")
	}
	p := newTestPlayerHolding(loop, 43, int32(item.WheatSeeds.ID))

	consumed := loop.tryParrotInteract(p, pr)
	if !consumed {
		t.Fatal("tryParrotInteract did not consume the seed interact")
	}
	if pr.tame {
		t.Fatal("parrot tamed on a tame-FAIL roll (nextInt(10)!=0)")
	}
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 0 {
		t.Fatalf("seed not consumed on tame-fail: held count = %d, want 0", got.Count)
	}
}

// TestParrotShoulderPerchFlag: a tamed, grounded, non-sitting parrot with an owner is marked perched by
// parrotAiStep (the LandOnOwnersShoulderGoal gameplay intent); a wild / flying / sitting parrot is not.
func TestParrotShoulderPerchFlag(t *testing.T) {
	loop, floorY := parrotLoop(t)
	pr := loop.spawnParrot(0, float64(floorY+1), 0)
	pr.onGround = true

	// Wild parrot: never perched.
	loop.parrotAiStep(pr)
	if pr.parrotPerched {
		t.Fatal("a wild (untamed) parrot must not be marked perched")
	}

	// Tamed + owned + grounded + not sitting: perched.
	pr.tame = true
	pr.ownerUUID = 42
	pr.orderedToSit = false
	loop.parrotAiStep(pr)
	if !pr.parrotPerched {
		t.Fatal("a tamed, grounded, non-sitting owned parrot should be marked perched")
	}

	// Ordered to sit: not perched (a sitting parrot does not perch).
	pr.orderedToSit = true
	loop.parrotAiStep(pr)
	if pr.parrotPerched {
		t.Fatal("a sitting parrot must not be marked perched")
	}

	// Flying (off ground): not perched.
	pr.orderedToSit = false
	pr.onGround = false
	loop.parrotAiStep(pr)
	if pr.parrotPerched {
		t.Fatal("a flying parrot must not be marked perched")
	}
	if !parrotIsFlying(pr) {
		t.Fatal("parrotIsFlying should be true off the ground")
	}
}
