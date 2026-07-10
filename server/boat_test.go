package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// boat_test.go — BOAT validation gates, each asserting the ported AbstractBoat float physics + the ride +
// the chest-boat container against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar):
//   - a boat placed on a water column FLOATS at the surface (its Y settles to the water level, not sinking);
//   - a boat in air FALLS with gravity 0.04 (getDefaultGravity);
//   - the jar constants (getMaxPassengers 2/1, rideHeight height/3, getStatus classification) match;
//   - a player right-clicks a boat and MOUNTS (a passenger is set); a chest boat OPENS its 27-slot container.

// newBoatLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container for column
// (0,0). Mirrors newMinecartLoop.
func newBoatLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// fillBoatWaterColumn writes a source-water column (Water{Level:0}) filling the block cells [y0..y1] at (x,z).
func fillBoatWaterColumn(mgr *world.ChunkManager, x, z, y0, y1 int) {
	src := block.ToStateID[block.Water{Level: 0}]
	for y := y0; y <= y1; y++ {
		mgr.SetBlock(pk.Position{X: x, Y: y, Z: z}, src, dimMinY)
	}
}

// TestBoatFloatsOnWaterSurface: a boat sitting on a source-water column reaches IN_WATER status and its Y
// stabilises near the water surface instead of sinking through the water (the floatBoat buoyancy). CITE
// AbstractBoat.floatBoat / getStatus / checkInWater.
func TestBoatFloatsOnWaterSurface(t *testing.T) {
	loop, mgr := newBoatLoop()

	// A deep source-water column at (8,z=8), water top at y=64 (cells 60..63 are water, so the surface is 64).
	fillBoatWaterColumn(mgr, 8, 8, 55, 63)

	// Spawn the boat resting on the water surface (top of the water column at y=64).
	boat := loop.spawnBoat(entity.OakBoat.ID, 8.5, 63.9, 8.5, 0)

	// Tick it a number of times; a server-authoritative (unridden) boat runs floatBoat + move each tick.
	for i := 0; i < 40; i++ {
		loop.tickBoat(boat)
	}

	// The boat must be classified as floating on water (IN_WATER), NOT submerged and NOT in free air.
	status := loop.boatGetStatus(boat)
	if status != boatStatusInWater {
		t.Fatalf("boat on a water column has status %v, want IN_WATER (%v)", status, boatStatusInWater)
	}

	// It must NOT have sunk far below the water surface — a floating boat settles at (near) the surface. The
	// water surface here is y=64; a sinking (non-buoyant) boat would keep falling well below it.
	if boat.y < 62.0 {
		t.Fatalf("boat sank through the water: Y = %.4f, want it floating near the surface (>= 62)", boat.y)
	}
	// And it must not have launched above the water either (a runaway buoyancy).
	if boat.y > 66.0 {
		t.Fatalf("boat launched above the water: Y = %.4f, want it near the surface (<= 66)", boat.y)
	}
}

// TestBoatDoesNotSinkOverManyTicks: over a long run a floating boat's Y stays bounded around the surface
// (the buoyancy holds it up) — it neither sinks to the bottom nor floats away. CITE AbstractBoat.floatBoat.
func TestBoatDoesNotSinkOverManyTicks(t *testing.T) {
	loop, mgr := newBoatLoop()
	fillBoatWaterColumn(mgr, 8, 8, 40, 63) // water surface at y=64

	boat := loop.spawnBoat(entity.OakBoat.ID, 8.5, 63.5, 8.5, 0)
	for i := 0; i < 200; i++ {
		loop.tickBoat(boat)
	}
	// After 200 ticks a NON-buoyant object would have fallen many blocks. A floating boat stays near y=64.
	if boat.y < 61.0 || boat.y > 66.0 {
		t.Fatalf("boat did not hold at the surface over 200 ticks: Y = %.4f, want ~64 (61..66)", boat.y)
	}
}

// TestBoatInAirFallsWithGravity: a boat with NO water/ground below is IN_AIR and floatBoat applies
// -getDefaultGravity() (0.04) to its vertical velocity (from rest, vy == -0.04 after one floatBoat). CITE
// AbstractBoat.floatBoat (vspeed = -getGravity()); AbstractBoat.getDefaultGravity == 0.04.
func TestBoatInAirFallsWithGravity(t *testing.T) {
	loop, _ := newBoatLoop()

	// No water, no floor: free air. Force the status directly to isolate the gravity delta (getStatus would
	// classify IN_AIR here anyway — no water below, no solid friction block).
	boat := loop.spawnBoat(entity.OakBoat.ID, 8.5, 80.0, 8.5, 0)

	if status := loop.boatGetStatus(boat); status != boatStatusInAir {
		t.Fatalf("boat in free air has status %v, want IN_AIR (%v)", status, boatStatusInAir)
	}

	// Run one floatBoat from rest: IN_AIR -> vy += -0.04 (invFriction 0.9 multiplies vx/vz only).
	boat.boatOldStatus = boatStatusInAir
	boat.boatStatus = boatStatusInAir
	boat.vx, boat.vy, boat.vz = 0, 0, 0
	loop.boatFloat(boat)
	if math.Abs(boat.vy-(-boatDefaultGravity)) > 1e-9 {
		t.Fatalf("IN_AIR floatBoat vy = %.6f, want -0.04 (getDefaultGravity)", boat.vy)
	}

	// And a full tick makes the boat actually descend under that gravity.
	startY := boat.y
	loop.tickBoat(boat)
	if boat.y >= startY {
		t.Fatalf("boat in air did not fall: Y %.4f -> %.4f (want decrease under gravity)", startY, boat.y)
	}
	if boat.vy >= 0 {
		t.Fatalf("boat in air has non-negative Y velocity %.4f (want falling)", boat.vy)
	}
}

// TestBoatConstants locks the jar constants: getDefaultGravity 0.04; getMaxPassengers 2 (boat) / 1 (chest
// boat); rideHeight height/3 (boat) vs height*0.8888889 (raft). CITE AbstractBoat / Boat / Raft / AbstractChestBoat.
func TestBoatConstants(t *testing.T) {
	loop, _ := newBoatLoop()

	if boatDefaultGravity != 0.04 {
		t.Fatalf("boatDefaultGravity = %v, want 0.04 (AbstractBoat.getDefaultGravity)", boatDefaultGravity)
	}

	boat := loop.spawnBoat(entity.OakBoat.ID, 0, 0, 0, 0)
	if got := boatMaxPassengersOf(boat); got != 2 {
		t.Fatalf("plain boat getMaxPassengers = %d, want 2", got)
	}
	chest := loop.spawnBoat(entity.OakChestBoat.ID, 0, 0, 0, 0)
	if got := boatMaxPassengersOf(chest); got != 1 {
		t.Fatalf("chest boat getMaxPassengers = %d, want 1", got)
	}

	// Boat.rideHeight == height/3.0; the oak boat's height is 0.5625, so rideHeight == 0.1875.
	wantBoatRide := boat.height / 3.0
	if got := boatRideHeight(boat); math.Abs(got-wantBoatRide) > 1e-9 {
		t.Fatalf("boat rideHeight = %.6f, want %.6f (height/3)", got, wantBoatRide)
	}
	// Raft.rideHeight == height*0.8888889.
	raft := loop.spawnBoat(entity.BambooRaft.ID, 0, 0, 0, 0)
	if !raft.boatIsRaft {
		t.Fatalf("bamboo raft boatIsRaft = false, want true")
	}
	wantRaftRide := raft.height * float64(float32(0.8888889))
	if got := boatRideHeight(raft); math.Abs(got-wantRaftRide) > 1e-9 {
		t.Fatalf("raft rideHeight = %.6f, want %.6f (height*0.8888889)", got, wantRaftRide)
	}
}

// TestBoatPlayerMounts: a right-click on a plain boat mounts the player (playerStartRiding via
// tryBoatInteract), setting the passenger on the boat and the vehicle on the player. CITE AbstractBoat.interact.
func TestBoatPlayerMounts(t *testing.T) {
	loop, _ := newBoatLoop()
	boat := loop.spawnBoat(entity.OakBoat.ID, 8.5, 64.0, 8.5, 0)

	p := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 7001, client: captureClient(64)}
	p.tracked = make(map[int32]bool)
	loop.players = append(loop.players, p)

	// A non-secondary right-click mounts.
	if !loop.tryBoatInteract(p, boat, false) {
		t.Fatal("tryBoatInteract returned false for a plain boat right-click (want consumed)")
	}
	if p.vehicleID != boat.id {
		t.Fatalf("after mount, player vehicleID = %d, want %d", p.vehicleID, boat.id)
	}
	if len(boat.passengers) != 1 || boat.passengers[0] != p.entityID {
		t.Fatalf("after mount, boat passengers = %v, want [%d]", boat.passengers, p.entityID)
	}
	if !boat.isVehicle() {
		t.Fatal("after mount, boat.isVehicle() is false")
	}
	// A ridden boat becomes client-authoritative: getControllingPassenger returns the player id.
	if got := loop.getControllingPassenger(boat); got != p.entityID {
		t.Fatalf("ridden boat getControllingPassenger = %d, want %d (the player)", got, p.entityID)
	}
}

// TestBoatCarriesTwoPassengers: a plain boat seats up to 2 (getMaxPassengers 2); the third mount is
// rejected (canAddPassenger). CITE AbstractBoat.getMaxPassengers.
func TestBoatCarriesTwoPassengers(t *testing.T) {
	loop, _ := newBoatLoop()
	boat := loop.spawnBoat(entity.OakBoat.ID, 8.5, 64.0, 8.5, 0)

	p1 := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 7101, client: captureClient(64)}
	p1.tracked = make(map[int32]bool)
	p2 := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 7102, client: captureClient(64)}
	p2.tracked = make(map[int32]bool)
	loop.players = append(loop.players, p1, p2)

	if !loop.playerStartRiding(p1, boat, false) {
		t.Fatal("first mount failed")
	}
	if !loop.playerStartRiding(p2, boat, false) {
		t.Fatal("second mount failed (a boat seats 2)")
	}
	if len(boat.passengers) != 2 {
		t.Fatalf("boat passengers = %d, want 2", len(boat.passengers))
	}
	// canAddPassenger is now false (full).
	if boat.canAddPassengerVehicle() {
		t.Fatal("a full 2-seat boat still reports canAddPassenger true")
	}
}

// TestChestBoatOpensContainer: a right-click on a chest boat (already full of a passenger, or via the
// secondary action) opens its 27-slot container menu. Here we assert the container-open path + the 27-slot
// backing directly. CITE AbstractChestBoat.interact / openCustomInventoryScreen / getContainerSize == 27.
func TestChestBoatOpensContainer(t *testing.T) {
	loop, _ := newBoatLoop()
	chest := loop.spawnBoat(entity.OakChestBoat.ID, 8.5, 64.0, 8.5, 0)

	if len(chest.minecartItems) != boatChestContainerSize {
		t.Fatalf("chest boat container size = %d, want 27", len(chest.minecartItems))
	}
	if !isChestBoat(chest) {
		t.Fatal("isChestBoat(oak_chest_boat) = false, want true")
	}

	p := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 7201, client: captureClient(256)}
	p.tracked = make(map[int32]bool)
	loop.players = append(loop.players, p)

	// A SECONDARY-action right-click routes a chest boat to the container (not the ride).
	if !loop.tryBoatInteract(p, chest, true) {
		t.Fatal("tryBoatInteract(secondary) on a chest boat returned false (want consumed)")
	}
	if p.openContainer == nil {
		t.Fatal("chest boat secondary right-click did not open a container window")
	}
	if p.openContainer.kind != containerKindMinecartChest {
		t.Fatalf("chest boat window kind = %v, want the shared vehicle-container kind", p.openContainer.kind)
	}
	if p.openContainer.minecartEntityID != chest.id {
		t.Fatalf("chest boat window entity id = %d, want %d", p.openContainer.minecartEntityID, chest.id)
	}
	// The player should NOT have mounted (the secondary click opens the container instead).
	if p.vehicleID != 0 {
		t.Fatalf("chest boat secondary click also mounted the player (vehicleID %d), want 0", p.vehicleID)
	}
}

// TestChestBoatNonSecondaryRidesWhenEmpty: a NON-secondary right-click on an EMPTY chest boat MOUNTS the
// player (a free seat), matching AbstractChestBoat.interact (super.interact ride runs first). CITE
// AbstractChestBoat.interact.
func TestChestBoatNonSecondaryRidesWhenEmpty(t *testing.T) {
	loop, _ := newBoatLoop()
	chest := loop.spawnBoat(entity.OakChestBoat.ID, 8.5, 64.0, 8.5, 0)

	p := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: 7301, client: captureClient(64)}
	p.tracked = make(map[int32]bool)
	loop.players = append(loop.players, p)

	if !loop.tryBoatInteract(p, chest, false) {
		t.Fatal("tryBoatInteract on an empty chest boat returned false")
	}
	// An empty chest boat has a free seat -> the non-secondary click mounts (does not open the container).
	if p.vehicleID != chest.id {
		t.Fatalf("empty chest boat non-secondary click did not mount: vehicleID %d, want %d", p.vehicleID, chest.id)
	}
	if p.openContainer != nil {
		t.Fatal("empty chest boat non-secondary click opened a container (want a ride)")
	}
}

// TestBoatItemMapping locks the boat item -> entity-type map for every wood variant + the raft.
func TestBoatItemMapping(t *testing.T) {
	cases := []struct {
		itemID int32
		want   entity.ID
	}{
		{891, entity.OakBoat.ID}, {892, entity.OakChestBoat.ID},
		{909, entity.BambooRaft.ID}, {910, entity.BambooChestRaft.ID},
		{907, entity.MangroveBoat.ID}, {906, entity.PaleOakChestBoat.ID},
	}
	for _, c := range cases {
		got, ok := boatItemToEntityType(c.itemID)
		if !ok || got != c.want {
			t.Fatalf("boatItemToEntityType(%d) = (%d, %v), want (%d, true)", c.itemID, got, ok, c.want)
		}
	}
	if _, ok := boatItemToEntityType(1); ok {
		t.Fatal("boatItemToEntityType(non-boat) reported ok=true")
	}
}

// TestBoatBlockFrictionPerBlock: boatBlockFriction reads the vanilla per-block friction table
// (blockFrictionAt) — a boat resting ON LAND on ice/blue_ice/slime carries the slippery friction, every
// other block the 0.6 default. CITE Block.getFriction / Blocks.<clinit> (ICE/PACKED_ICE/FROSTED_ICE
// 0.98f, BLUE_ICE 0.989f, SLIME_BLOCK 0.8f, default 0.6f).
func TestBoatBlockFrictionPerBlock(t *testing.T) {
	loop, mgr := newBoatLoop()
	set := func(x, y, z int, name string) {
		mgr.SetBlock(pk.Position{X: x, Y: y, Z: z}, block.DefaultStateID[name], dimMinY)
	}
	set(1, 64, 1, "minecraft:stone")
	set(2, 64, 2, "minecraft:ice")
	set(3, 64, 3, "minecraft:packed_ice")
	set(4, 64, 4, "minecraft:frosted_ice")
	set(5, 64, 5, "minecraft:blue_ice")
	set(6, 64, 6, "minecraft:slime_block")

	cases := []struct {
		x, y, z int
		want    float32
	}{
		{1, 64, 1, 0.6},
		{2, 64, 2, 0.98},
		{3, 64, 3, 0.98},
		{4, 64, 4, 0.98},
		{5, 64, 5, 0.989},
		{6, 64, 6, 0.8},
		{9, 64, 9, 0.6}, // air -> default
	}
	for _, c := range cases {
		if got := loop.boatBlockFriction(c.x, c.y, c.z); got != c.want {
			t.Fatalf("boatBlockFriction(%d,%d,%d) = %v, want %v", c.x, c.y, c.z, got, c.want)
		}
	}
}
