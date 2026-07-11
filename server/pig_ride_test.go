package server

// pig_ride_test.go -- deterministic pins for the Pig ItemSteerable ride/steer (net.minecraft.world.entity
// .animal.pig.Pig.mobInteract + getControllingPassenger + boost, 1:1 javap this task). Verifies: a SADDLE
// saddles the pig (tryPigInteract SADDLE branch); a saddled pig with a rider holding carrot_on_a_stick is
// the controlling passenger and a carrot USE boosts it (ItemBasedSteering.boost); an UN-saddled pig is inert
// (no mount, no controlling passenger) and draws NO RNG (the pig-oracle byte-identity gate).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// pigRideLoop builds a single-region loop with a flat floor and one ADULT pig at (8.5, floorY+1, 8.5). The
// pig gets a per-entity AI/rng (like the other passive mobs) so mobRandom(pig) is stable for the RNG-inert
// assertion.
func pigRideLoop(t *testing.T) (*TickLoop, *Entity, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	_ = mgr
	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, float64(floorY+1), 8.5)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.onGround = true
	pig.health = 10
	loop.only().entities.add(pig)
	return loop, pig, floorY
}

// pigRider registers a tickPlayer holding itemID (main hand) with a capture client + tracked set.
func pigRider(loop *TickLoop, entityID int32, x, y, z float64, itemID int32) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, entityID: entityID, client: captureClient(64)}
	p.tracked = make(map[int32]bool)
	inv := ensureInventory(p)
	if itemID != 0 {
		inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
	}
	loop.players = append(loop.players, p)
	return p
}

// TestPigSaddleSaddlesThePig: a SADDLE (id 865) right-clicked on an un-saddled pig equips the saddle
// (setChest-equivalent SADDLE-slot set -> pigIsSaddled true) and consumes 1 saddle. A second saddle click is
// a no-op (already saddled -> falls through to feed).
func TestPigSaddleSaddlesThePig(t *testing.T) {
	loop, pig, floorY := pigRideLoop(t)
	if pigIsSaddled(pig) {
		t.Fatal("a fresh pig is already saddled")
	}
	saddler := pigRider(loop, 1, 8.6, float64(floorY+1), 8.5, itemSaddle)
	if !loop.tryPigInteract(saddler, pig, false) {
		t.Fatal("a SADDLE on an un-saddled pig did not consume the interact (tryPigInteract false)")
	}
	if !pigIsSaddled(pig) {
		t.Fatal("after a SADDLE click the pig is not saddled (pigIsSaddled false)")
	}
	held := ensureInventory(saddler).get(heldWindowSlot(ensureInventory(saddler).heldSlot))
	if held.Count != 0 {
		t.Fatalf("saddle not consumed: held count = %d, want 0", held.Count)
	}
	// A second SADDLE click on an already-saddled, non-ridden pig takes the RIDE branch first (Pig.mobInteract
	// order: !isFood && isSaddled() && !isVehicle() && !secondary -> startRiding, BEFORE the saddle-equip
	// branch). So it MOUNTS the clicker (a saddle held is not pig_food, so it is not the feed path). The
	// interact is consumed by the pig.
	saddler2 := pigRider(loop, 2, 8.6, float64(floorY+1), 8.5, itemSaddle)
	if !loop.tryPigInteract(saddler2, pig, false) {
		t.Fatal("a second SADDLE on an already-saddled pig did not consume the interact (should mount via RIDE)")
	}
	if saddler2.vehicleID != pig.id {
		t.Fatalf("a second SADDLE click on a saddled pig did not mount: vehicleID=%d, want %d", saddler2.vehicleID, pig.id)
	}
}

// TestSaddledPigSteeredAndBoosted: a saddled pig mounted by a rider holding a carrot_on_a_stick (id 887) has
// that rider as its controlling passenger (client-authoritative steer). A carrot USE (pigBoost) starts an
// ItemBasedSteering boost (boostFactor rises above 1.0), and the boost timer advances via pigTickBoost.
func TestSaddledPigSteeredAndBoosted(t *testing.T) {
	loop, pig, floorY := pigRideLoop(t)
	pig.striderSaddled = true // saddled (the SADDLE-slot fold)

	// Mount a rider holding a carrot_on_a_stick.
	rider := pigRider(loop, 1, 8.5, float64(floorY+1), 8.5, itemCarrotOnAStick)
	if !loop.tryPigInteract(rider, pig, false) {
		t.Fatal("mounting a saddled pig did not consume the interact")
	}
	if rider.vehicleID != pig.id {
		t.Fatalf("rider did not mount the saddled pig: vehicleID = %d, want %d", rider.vehicleID, pig.id)
	}
	// getControllingPassenger: a saddled pig with a carrot-holding first-passenger player -> that player.
	if got := loop.getControllingPassenger(pig); got != rider.entityID {
		t.Fatalf("saddled-pig controlling passenger = %d, want rider %d", got, rider.entityID)
	}

	// A carrot USE boosts: pigBoost starts a boost (boostFactor climbs above 1.0 as the timer advances).
	if !pigBoost(pig) {
		t.Fatal("pigBoost did not start a boost on a fresh (non-boosting) pig")
	}
	if !pig.striderBoosting {
		t.Fatal("after pigBoost the pig is not boosting")
	}
	if pig.striderBoostTimeTotal < striderBoostMinTime {
		t.Fatalf("pig boost total = %d, want >= %d (nextInt(841)+140)", pig.striderBoostTimeTotal, striderBoostMinTime)
	}
	// A second boost while already boosting is a no-op (ItemBasedSteering.boost returns false).
	if pigBoost(pig) {
		t.Fatal("a second pigBoost while already boosting started a new boost (should be a no-op)")
	}
	// Advance the timer: pigTickBoost increments boostTime; boostFactor rises above 1.0 mid-curve.
	for i := 0; i < pig.striderBoostTimeTotal/2; i++ {
		pigTickBoost(pig)
	}
	if bf := striderBoostFactor(pig); bf <= 1.0 {
		t.Fatalf("mid-boost boostFactor = %v, want > 1.0 (1.0 + 1.15*sin curve)", bf)
	}
	// getRiddenSpeed reads MOVEMENT_SPEED * 0.225 * boostFactor -- a positive steer speed.
	if sp := pigGetRiddenSpeed(pig); sp <= 0 {
		t.Fatalf("pigGetRiddenSpeed = %v, want > 0", sp)
	}
}

// TestUnsaddledPigInertNoRNG: an UN-saddled, un-ridden pig (the pig oracle) is inert -- a right-click with a
// carrot_on_a_stick does NOT mount it (mount is gated on isSaddled), it has NO controlling passenger, and the
// interact path draws ZERO RNG from the pig's own stream (the byte-identity gate). We snapshot the pig's rng
// state before/after and require it unchanged.
func TestUnsaddledPigInertNoRNG(t *testing.T) {
	loop, pig, floorY := pigRideLoop(t)
	if pigIsSaddled(pig) {
		t.Fatal("a fresh pig is unexpectedly saddled")
	}

	// A reference clone reseeded to the SAME per-entity seed (reseedMobAI's id-derived seed): if the interact
	// draws ZERO from the pig's stream, the pig's rng and the clone produce the identical next value. Any draw
	// on the pig's stream would desync them -- the oracle byte-identity gate.
	clone := newEntityRandom(uint64(uint32(pig.id)) ^ defaultEntityRandomSeed)

	// A right-click with a carrot_on_a_stick on an un-saddled pig: not a mount (isSaddled false), not a saddle
	// item -> tryPigInteract returns false (falls through to feed; carrot_on_a_stick is not pig_food).
	rider := pigRider(loop, 1, 8.5, float64(floorY+1), 8.5, itemCarrotOnAStick)
	if loop.tryPigInteract(rider, pig, false) {
		t.Fatal("an un-saddled pig consumed a carrot_on_a_stick interact (should fall through -- no mount)")
	}
	if rider.vehicleID != 0 || len(pig.passengers) != 0 {
		t.Fatalf("an un-saddled pig was mounted: rider.vehicleID=%d passengers=%v", rider.vehicleID, pig.passengers)
	}
	// No controlling passenger for an un-saddled pig.
	if got := loop.getControllingPassenger(pig); got != 0 {
		t.Fatalf("un-saddled pig controlling passenger = %d, want 0 (super)", got)
	}
	// The pig's RNG stream must be UNPERTURBED (zero draws) -- the oracle byte-identity gate. The clone (same
	// seed, zero draws) must produce the identical next value as the pig's stream.
	if pigDraw, cloneDraw := mobRandom(pig).nextInt(1_000_000), clone.nextInt(1_000_000); pigDraw != cloneDraw {
		t.Fatalf("the un-saddled pig interact path drew RNG (pig stream desynced: %d vs clone %d) -- oracle NOT byte-identical", pigDraw, cloneDraw)
	}
}
