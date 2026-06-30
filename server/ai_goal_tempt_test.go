package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// ai_goal_tempt_test.go — MOB-SUB-06/07 (Phase 32-01): the TemptGoal@4 regression set. TemptGoal is
// the first goal that reads PLAYER state (held item) — a pig follows a player holding a tempt item
// (the carrot_on_a_stick literal id 887 OR any pig_food tag item {1257,1258,1317}) within TEMPT_RANGE
// (10.0), and does NOT follow a player holding a non-tempt item or one beyond range.
//
//   - TestTemptGoalFollowsPlayerHoldingPigFood   — main-hand pig_food (id 1257) within range: canUse
//     true, tick acquires a want-target toward the player AND aims the head at it.
//   - TestTemptGoalFollowsPlayerHoldingCarrot     — off-hand carrot_on_a_stick (id 887) within range:
//     same (exercises the off-hand read at offhandWindowSlot 45).
//   - TestTemptGoalIgnoresNonTemptItem            — a non-tempt item (dirt, id 1) → canUse false
//     directly (the deterministic non-trigger, drawing ZERO RNG — TemptGoal draws none).
//   - TestTemptGoalIgnoresOutOfRange              — pig_food but the player is >10 blocks away →
//     canUse false directly.
//
// NO RNG anywhere (TemptGoal.canUse is an int gate + a held-item player scan), so these assert canUse
// directly — there is no probability roll to exhaust.

// temptTestLoop builds a one-chunk floor TickLoop and a live pig at (8.5, floorY+1, 8.5). The pig
// carries TemptRange=10.0 via its supplier (pigSupplier → createAnimalAttributes.AddValue(TemptRange,
// 10.0), defaults.go:79), so the scan radius is the jar default.
func temptTestLoop(t *testing.T) (*TickLoop, *Entity) {
	t.Helper()
	clock := newFakeClock()
	loop := NewTickLoop(clock)
	mgr := world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = mgr
	}
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(clock.Now())

	pig := NewEntity(5151, entity.Pig, 8.5, float64(floorY+1), 8.5)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.onGround = true
	loop.only().entities.add(pig)

	// The pig must carry TemptRange (Animal.createAnimalAttributes adds it at 10.0) so the scan radius
	// is non-zero — assert the jar default so a regression in the supplier collapses loudly, not silently.
	if r := pig.getAttributeValue(attribute.TemptRange); r != 10.0 {
		t.Fatalf("pig TemptRange = %v, want jar default 10.0 (Animal.createAnimalAttributes)", r)
	}
	return loop, pig
}

// addPlayerHolding adds a tickPlayer at (x,y,z) holding itemID in the given hand (main = the selected
// hotbar slot via heldWindowSlot, off = offhandWindowSlot 45). Count 1 so slotIsEmpty is false.
func addPlayerHolding(loop *TickLoop, x, y, z float64, itemID int32, offHand bool) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z}
	inv := ensureInventory(p)
	slot := heldWindowSlot(inv.heldSlot)
	if offHand {
		slot = offhandWindowSlot
	}
	inv.set(slot, component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
	loop.players = append(loop.players, p)
	return p
}

// pigFoodCarrotPred is the pig_food TAG predicate (one of the two @4 goals); carrotPred is the
// carrot_on_a_stick LITERAL predicate (id 887). These match newPigAI's two goal ctors exactly.
func pigFoodPred(id int32) bool { return itemInTag(id, "pig_food") }
func carrotPred(id int32) bool  { return id == 887 }

// TestTemptGoalFollowsPlayerHoldingPigFood: a player holding pig_food (carrot id 1257) in the MAIN
// hand within TEMPT_RANGE makes the pig_food TemptGoal canUse, and tick acquires a want-target toward
// the player AND aims the head at it (mob.distanceToSqr >= 6.25 so it navigates rather than stopping).
func TestTemptGoalFollowsPlayerHoldingPigFood(t *testing.T) {
	loop, pig := temptTestLoop(t)
	// A player 4 blocks east (+X) — within range, and >2.5 away so tick navigates (distSqr 16 >= 6.25).
	p := addPlayerHolding(loop, pig.x+4, pig.y, pig.z, 1257, false) // 1257 = carrot, a pig_food member

	g := newTemptGoal(1.2, pigFoodPred, false)
	if !g.canUse(loop, pig) {
		t.Fatal("temptGoal.canUse false for a player holding pig_food within range — the pig should be tempted")
	}
	g.start(loop, pig)
	g.tick(loop, pig)

	if !pig.ai.hasTarget {
		t.Fatal("after tick the tempted pig has no want-target (navigateTowards(player) did not set one)")
	}
	if pig.ai.wantX != p.x || pig.ai.wantY != p.y || pig.ai.wantZ != p.z {
		t.Fatalf("want-target (%v,%v,%v) is not the player's pos (%v,%v,%v)",
			pig.ai.wantX, pig.ai.wantY, pig.ai.wantZ, p.x, p.y, p.z)
	}
	want := yawTowardDeg(p.x-pig.x, p.z-pig.z) // facing +X
	if math.Abs(float64(pig.headYaw-want)) > 0.001 {
		t.Fatalf("pig head yaw %v not aimed at the player (want %v)", pig.headYaw, want)
	}
}

// TestTemptGoalFollowsPlayerHoldingCarrot: a player holding carrot_on_a_stick (id 887) in the OFF hand
// (offhandWindowSlot 45) within range makes the carrot TemptGoal canUse + navigate + look. Exercises
// the off-hand read (shouldFollow = main || off).
func TestTemptGoalFollowsPlayerHoldingCarrot(t *testing.T) {
	loop, pig := temptTestLoop(t)
	// A player 3 blocks north (+Z) holding the carrot in the OFF hand (distSqr 9 >= 6.25 → navigate).
	p := addPlayerHolding(loop, pig.x, pig.y, pig.z+3, 887, true)

	g := newTemptGoal(1.2, carrotPred, false)
	if !g.canUse(loop, pig) {
		t.Fatal("temptGoal.canUse false for a player holding carrot_on_a_stick in the OFF hand within range")
	}
	g.start(loop, pig)
	g.tick(loop, pig)

	if !pig.ai.hasTarget {
		t.Fatal("after tick the carrot-tempted pig has no want-target")
	}
	if pig.ai.wantX != p.x || pig.ai.wantZ != p.z {
		t.Fatalf("want-target (%v,_,%v) is not the player's pos (%v,_,%v)",
			pig.ai.wantX, pig.ai.wantZ, p.x, p.z)
	}
	want := yawTowardDeg(p.x-pig.x, p.z-pig.z) // facing +Z
	if math.Abs(float64(pig.headYaw-want)) > 0.001 {
		t.Fatalf("pig head yaw %v not aimed at the carrot player (want %v)", pig.headYaw, want)
	}
}

// TestTemptGoalIgnoresNonTemptItem: a player holding a non-tempt item (dirt, id 1) does NOT tempt the
// pig — canUse returns false DIRECTLY (the deterministic non-trigger, like the panic non-trigger test).
func TestTemptGoalIgnoresNonTemptItem(t *testing.T) {
	loop, pig := temptTestLoop(t)
	addPlayerHolding(loop, pig.x+2, pig.y, pig.z, 1, false) // id 1 = dirt, not a tempt item

	if newTemptGoal(1.2, pigFoodPred, false).canUse(loop, pig) {
		t.Fatal("pig_food temptGoal.canUse true for a player holding dirt — shouldFollow must gate on the item")
	}
	if newTemptGoal(1.2, carrotPred, false).canUse(loop, pig) {
		t.Fatal("carrot temptGoal.canUse true for a player holding dirt — shouldFollow must gate on the item")
	}
}

// TestTemptGoalIgnoresOutOfRange: a player holding pig_food but >10 blocks away (beyond TEMPT_RANGE)
// does NOT tempt the pig — canUse returns false DIRECTLY (the scan rejects the out-of-range player).
func TestTemptGoalIgnoresOutOfRange(t *testing.T) {
	loop, pig := temptTestLoop(t)
	// 15 blocks east — outside the 10.0 TEMPT_RANGE.
	addPlayerHolding(loop, pig.x+15, pig.y, pig.z, 1257, false)

	if newTemptGoal(1.2, pigFoodPred, false).canUse(loop, pig) {
		t.Fatal("temptGoal.canUse true for a pig_food player 15 blocks away — TEMPT_RANGE (10.0) must gate it out")
	}
}
