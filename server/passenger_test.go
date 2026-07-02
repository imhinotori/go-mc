package server

import (
	"bytes"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// rideTestLoop builds a single-region loop with a flat floor and one ADULT happy ghast at (8.5,64,8.5),
// hovering (no gravity). Returns the loop and the ghast. Mirrors temptTestLoop's shape for the ride tests.
func rideTestLoop(t *testing.T) (*TickLoop, *Entity) {
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

	ghast := NewEntity(9001, entity.HappyGhast, 8.5, 80.0, 8.5) // hovering well above the floor
	ghast.breedAge = 0                                          // adult (isBaby() == false)
	ghast.health = 10                                           // alive (isAlive() == !dead && health > 0)
	loop.only().entities.add(ghast)
	return loop, ghast
}

// ridePlayer registers a tickPlayer with a capture client + a tracked set so broadcastSetPassengers can
// send to it, at the given position and yaw, with the given entityID.
func ridePlayer(loop *TickLoop, entityID int32, x, y, z float64, yaw float32) *tickPlayer {
	p := &tickPlayer{x: x, y: y, z: z, yaw: yaw, entityID: entityID, client: captureClient(64)}
	p.tracked = make(map[int32]bool)
	loop.players = append(loop.players, p)
	return p
}

// TestStartRidingMountsAndStopRidingDismounts is the core startRiding/stopRiding contract: a successful
// mount records the vehicle on the player and prepends the player to the ghast's passenger list; a
// stopRiding clears both and arms the boarding cooldown (Entity.removePassenger sets it to 60).
func TestStartRidingMountsAndStopRidingDismounts(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p := ridePlayer(loop, 1, 8.5, 64, 8.5, 0)

	if !loop.playerStartRiding(p, ghast, false) {
		t.Fatal("playerStartRiding returned false for an adult harnessed ghast with a free seat")
	}
	if p.vehicleID != ghast.id {
		t.Fatalf("after mount, player vehicleID = %d, want %d", p.vehicleID, ghast.id)
	}
	if len(ghast.passengers) != 1 || ghast.passengers[0] != p.entityID {
		t.Fatalf("after mount, ghast passengers = %v, want [%d]", ghast.passengers, p.entityID)
	}
	if !ghast.isVehicle() {
		t.Fatal("after mount, ghast.isVehicle() is false")
	}

	loop.playerStopRiding(p)
	if p.vehicleID != 0 {
		t.Fatalf("after dismount, player vehicleID = %d, want 0", p.vehicleID)
	}
	if len(ghast.passengers) != 0 {
		t.Fatalf("after dismount, ghast passengers = %v, want empty", ghast.passengers)
	}
	if p.boardingCooldown != removePassengerBoardingCooldown {
		t.Fatalf("after dismount, boardingCooldown = %d, want %d (Entity.removePassenger)",
			p.boardingCooldown, removePassengerBoardingCooldown)
	}
}

// TestBoardingCooldownBlocksRemount: a just-dismounted player cannot immediately re-mount (canRide gates
// on boardingCooldown <= 0), and the cooldown decrements each ride-tick until re-mount is allowed.
func TestBoardingCooldownBlocksRemount(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p := ridePlayer(loop, 1, 8.5, 64, 8.5, 0)

	loop.playerStartRiding(p, ghast, false)
	loop.playerStopRiding(p) // arms boardingCooldown = 60

	if loop.playerStartRiding(p, ghast, false) {
		t.Fatal("re-mount succeeded while boardingCooldown > 0 — canRide must block it")
	}
	// Drain the cooldown via the ride tick (60 ticks).
	for i := 0; i < removePassengerBoardingCooldown; i++ {
		loop.rideTickVehicles()
	}
	if p.boardingCooldown != 0 {
		t.Fatalf("after %d ride-ticks, boardingCooldown = %d, want 0", removePassengerBoardingCooldown, p.boardingCooldown)
	}
	if !loop.playerStartRiding(p, ghast, false) {
		t.Fatal("re-mount failed after the boarding cooldown elapsed")
	}
}

// TestHappyGhastFourSeatCap: a happy ghast seats up to 4 players (canAddPassenger: size < 4); the 5th
// mount is rejected. It also asserts the Player-prepend order stays consistent (players fill seats 0..3).
func TestHappyGhastFourSeatCap(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	players := make([]*tickPlayer, 5)
	for i := 0; i < 5; i++ {
		players[i] = ridePlayer(loop, int32(10+i), 8.5, 64, 8.5, 0)
	}
	mounted := 0
	for i := 0; i < 5; i++ {
		if loop.playerStartRiding(players[i], ghast, false) {
			mounted++
		}
	}
	if mounted != happyGhastMaxPassengers {
		t.Fatalf("mounted %d players, want %d (HappyGhast.canAddPassenger size < 4)", mounted, happyGhastMaxPassengers)
	}
	if len(ghast.passengers) != happyGhastMaxPassengers {
		t.Fatalf("ghast passengers = %d, want %d", len(ghast.passengers), happyGhastMaxPassengers)
	}
	if players[4].vehicleID != 0 {
		t.Fatalf("the 5th player mounted (vehicleID=%d) — the seat cap must reject it", players[4].vehicleID)
	}
}

// TestGetControllingPassenger: the FIRST passenger of a harnessed adult happy ghast is the controlling
// passenger (the steerer); a non-ghast vehicle has none.
func TestGetControllingPassenger(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p0 := ridePlayer(loop, 1, 8.5, 64, 8.5, 0)
	p1 := ridePlayer(loop, 2, 8.5, 64, 8.5, 0)
	loop.playerStartRiding(p0, ghast, false)
	loop.playerStartRiding(p1, ghast, false)

	if got := loop.getControllingPassenger(ghast); got != p0.entityID {
		t.Fatalf("controlling passenger = %d, want the first passenger %d", got, p0.entityID)
	}

	// A non-ghast vehicle (a pig) has no controlling passenger even with a passenger seated.
	pig := NewEntity(7777, entity.Pig, 1, 64, 1)
	loop.only().entities.add(pig)
	pig.passengers = []int32{p0.entityID}
	if got := loop.getControllingPassenger(pig); got != 0 {
		t.Fatalf("a pig vehicle has a controlling passenger %d, want 0 (default Entity.getControllingPassenger)", got)
	}
}

// TestPositionRiderSeatOffset asserts the seat-offset math (Entity.positionRider): the sole passenger of a
// happy ghast lands at ghast_pos + seat[0] - playerVehicleAttachment, with the ghast at yaw 0 (no
// rotation). Seat 0 == (0, 4, 1.7); the player attachment == (0, 0.6, 0). So the player's feet sit at
// (ghast.x, ghast.y + 4 - 0.6, ghast.z + 1.7).
func TestPositionRiderSeatOffset(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	ghast.x, ghast.y, ghast.z = 100.0, 70.0, 200.0
	ghast.yaw = 0 // no rotation: transformPoint is identity
	p := ridePlayer(loop, 1, 0, 0, 0, 0)
	loop.playerStartRiding(p, ghast, false)

	loop.positionRider(ghast)

	wantX := ghast.x + happyGhastPassengerSeats[0].X - playerVehicleAttachment.X
	wantY := ghast.y + happyGhastPassengerSeats[0].Y - playerVehicleAttachment.Y
	wantZ := ghast.z + happyGhastPassengerSeats[0].Z - playerVehicleAttachment.Z
	if !ridePosEq(p.x, wantX) || !ridePosEq(p.y, wantY) || !ridePosEq(p.z, wantZ) {
		t.Fatalf("passenger seat position = (%v,%v,%v), want (%v,%v,%v)", p.x, p.y, p.z, wantX, wantY, wantZ)
	}
	// The playerEntity snapshot must track the seat too (so the tracker broadcasts the ridden position).
	if p.playerEntity != nil && (!ridePosEq(p.playerEntity.x, p.x) || !ridePosEq(p.playerEntity.z, p.z)) {
		t.Fatalf("playerEntity not synced to the seat: entity=(%v,%v) player=(%v,%v)",
			p.playerEntity.x, p.playerEntity.z, p.x, p.z)
	}
}

// TestPositionRiderRotatesSeat: with the ghast turned 90° (yaw=90), seat 0 (0,4,1.7) rotates around Y —
// transformPoint(seat, 90) with r = -90*PI/180: x' = x*cos + z*sin, z' = z*cos - x*sin. For (0,_,1.7):
// cos(-pi/2)=0, sin(-pi/2)=-1 => x' = 1.7*(-1) = -1.7, z' = 0. So the seat swings to the ghast's -X side.
func TestPositionRiderRotatesSeat(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	ghast.x, ghast.y, ghast.z = 0, 0, 0
	ghast.yaw = 90
	p := ridePlayer(loop, 1, 0, 0, 0, 0) // player yaw 0 so its own attachment is unrotated (0,0.6,0)
	loop.playerStartRiding(p, ghast, false)

	loop.positionRider(ghast)

	seat := transformPoint(happyGhastPassengerSeats[0], ghast.yaw)
	if !ridePosEq(seat.X, -1.7) || !ridePosEq(seat.Z, 0.0) {
		t.Fatalf("rotated seat = (%v,%v,%v), want (-1.7, 4, 0)", seat.X, seat.Y, seat.Z)
	}
	wantX := seat.X - playerVehicleAttachment.X
	wantZ := seat.Z - playerVehicleAttachment.Z
	if !ridePosEq(p.x, wantX) || !ridePosEq(p.z, wantZ) {
		t.Fatalf("rotated passenger pos = (%v,%v), want (%v,%v)", p.x, p.z, wantX, wantZ)
	}
}

// TestEncodeSetPassengersWire byte-checks ClientboundSetPassengers: VarInt vehicleId, then a VarIntArray
// (VarInt count + N VarInt ids). The packet id is ClientboundSetPassengers; the body is rebuilt
// field-by-field in the jar's write order and byte-compared.
func TestEncodeSetPassengersWire(t *testing.T) {
	vehicleID := int32(9001)
	passengers := []int32{1, 2, 3}
	pkt := encodeSetPassengers(vehicleID, passengers)

	if pkt.ID != int32(packetid.ClientboundSetPassengers) {
		t.Fatalf("packet id = %d, want ClientboundSetPassengers %d", pkt.ID, int32(packetid.ClientboundSetPassengers))
	}

	var want bytes.Buffer
	for _, f := range []pk.FieldEncoder{
		pk.VarInt(vehicleID),                     // vehicle id
		pk.VarInt(int32(len(passengers))),        // array count
		pk.VarInt(1), pk.VarInt(2), pk.VarInt(3), // passenger ids
	} {
		if _, err := f.WriteTo(&want); err != nil {
			t.Fatalf("building expected body: %v", err)
		}
	}
	if !bytes.Equal(pkt.Data, want.Bytes()) {
		t.Fatalf("encodeSetPassengers body mismatch:\n got %x\nwant %x", pkt.Data, want.Bytes())
	}

	// An empty passenger list (a dismount) emits vehicleId + a zero-count array.
	empty := encodeSetPassengers(vehicleID, nil)
	var wantEmpty bytes.Buffer
	pk.VarInt(vehicleID).WriteTo(&wantEmpty)
	pk.VarInt(0).WriteTo(&wantEmpty)
	if !bytes.Equal(empty.Data, wantEmpty.Bytes()) {
		t.Fatalf("empty SetPassengers body = %x, want %x", empty.Data, wantEmpty.Bytes())
	}
}

// TestHappyGhastMountThroughInteract exercises the full mobInteract ride path: a ServerboundInteract
// naming an adult ghast, NO secondary action, mounts the player (tryHappyGhastRide -> startRiding) and
// the ghast's client + trackers receive a ClientboundSetPassengers. A shift (secondary) interact does NOT
// mount.
func TestHappyGhastMountThroughInteract(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p := ridePlayer(loop, 1, ghast.x, 64, ghast.z, 0)

	// A non-secondary interact mounts.
	pkt := pk.Marshal(int32(packetid.ServerboundInteract),
		pk.VarInt(ghast.id),                                        // entityId
		pk.VarInt(0),                                               // InteractionHand = MAIN_HAND
		pk.Double(ghast.x), pk.Double(ghast.y), pk.Double(ghast.z), // Vec3 location
		pk.Boolean(false), // usingSecondaryAction = false
	)
	loop.handleInteract(p, pkt)

	if p.vehicleID != ghast.id {
		t.Fatalf("after a non-secondary interact, player vehicleID = %d, want %d (mounted)", p.vehicleID, ghast.id)
	}
	if len(ghast.passengers) != 1 {
		t.Fatalf("after the interact mount, ghast passengers = %v, want one", ghast.passengers)
	}
	// The rider's client must have received a ClientboundSetPassengers so it attaches itself.
	pkts := drainPackets(p.client)
	sawSetPassengers := false
	for _, q := range pkts {
		if q.ID == int32(packetid.ClientboundSetPassengers) {
			sawSetPassengers = true
		}
	}
	if !sawSetPassengers {
		t.Fatal("the mounted rider never received a ClientboundSetPassengers")
	}
}

// TestHappyGhastSecondaryInteractDoesNotMount: a shift-right-click (usingSecondaryAction=true) on the
// ghast must NOT mount (HappyGhast.mobInteract: `!player.isSecondaryUseActive()`).
func TestHappyGhastSecondaryInteractDoesNotMount(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p := ridePlayer(loop, 1, ghast.x, 64, ghast.z, 0)

	pkt := pk.Marshal(int32(packetid.ServerboundInteract),
		pk.VarInt(ghast.id),
		pk.VarInt(0),
		pk.Double(ghast.x), pk.Double(ghast.y), pk.Double(ghast.z),
		pk.Boolean(true), // usingSecondaryAction = true (shift)
	)
	loop.handleInteract(p, pkt)

	if p.vehicleID != 0 {
		t.Fatalf("a shift interact mounted the player (vehicleID=%d) — it must not", p.vehicleID)
	}
}

// TestBabyGhastNotRideable: a baby happy ghast (breedAge<0) cannot be ridden (mobInteract returns super
// for a baby → the interact falls through to the feed path, not the ride).
func TestBabyGhastNotRideable(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	ghast.breedAge = -1 // baby (isBaby() == true)
	p := ridePlayer(loop, 1, ghast.x, 64, ghast.z, 0)

	if loop.tryHappyGhastRide(p, ghast, false) {
		t.Fatal("tryHappyGhastRide returned true for a baby ghast — a ghastling is not rideable")
	}
	if p.vehicleID != 0 {
		t.Fatalf("a baby ghast was mounted (vehicleID=%d)", p.vehicleID)
	}
}

// TestSteerThroughMoveVehicle: the controlling passenger drives the ghast via ServerboundMoveVehicle —
// the server applies the client's absolute vehicle position + rotation and re-positions the passenger. A
// non-controlling passenger's MoveVehicle is ignored (the anti-spoof gate).
func TestSteerThroughMoveVehicle(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	ghast.x, ghast.y, ghast.z, ghast.yaw = 0, 80, 0, 0
	p := ridePlayer(loop, 1, 0, 80, 0, 0)
	loop.playerStartRiding(p, ghast, false)

	// The controlling passenger reports the ghast moved to (10, 85, 20), yaw 45.
	pkt := pk.Marshal(int32(packetid.ServerboundMoveVehicle),
		pk.Double(10), pk.Double(85), pk.Double(20), // position
		pk.Float(45), pk.Float(0), // yRot, xRot
		pk.Boolean(false), // onGround
	)
	loop.handleMoveVehicle(p, pkt)

	if !ridePosEq(ghast.x, 10) || !ridePosEq(ghast.y, 85) || !ridePosEq(ghast.z, 20) {
		t.Fatalf("after MoveVehicle, ghast pos = (%v,%v,%v), want (10,85,20)", ghast.x, ghast.y, ghast.z)
	}
	if ghast.yaw != 45 {
		t.Fatalf("after MoveVehicle, ghast yaw = %v, want 45", ghast.yaw)
	}
	// The passenger was repositioned to the new (rotated) seat: seat[0] is rotated by the ghast yaw (45).
	seat := transformPoint(happyGhastPassengerSeats[0], ghast.yaw)
	wantX := ghast.x + seat.X - playerVehicleAttachment.X
	wantZ := ghast.z + seat.Z - playerVehicleAttachment.Z
	if !ridePosEq(p.x, wantX) || !ridePosEq(p.z, wantZ) {
		t.Fatalf("passenger not re-seated after steer: pos = (%v,%v), want (%v,%v)", p.x, p.z, wantX, wantZ)
	}

	// A NON-controlling passenger (a second player, seat 1) cannot steer.
	p2 := ridePlayer(loop, 2, 0, 0, 0, 0)
	loop.playerStartRiding(p2, ghast, false)
	before := ghast.x
	spoof := pk.Marshal(int32(packetid.ServerboundMoveVehicle),
		pk.Double(999), pk.Double(999), pk.Double(999),
		pk.Float(0), pk.Float(0), pk.Boolean(false),
	)
	loop.handleMoveVehicle(p2, spoof)
	if ghast.x != before {
		t.Fatalf("a non-controlling passenger steered the ghast (x moved to %v from %v)", ghast.x, before)
	}
}

// TestEjectPassengersDismountsAll: ejectPassengers dismounts every rider (Entity.ejectPassengers /
// setRemoved) so no player is orphaned on a removed vehicle.
func TestEjectPassengersDismountsAll(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p0 := ridePlayer(loop, 1, 8.5, 64, 8.5, 0)
	p1 := ridePlayer(loop, 2, 8.5, 64, 8.5, 0)
	loop.playerStartRiding(p0, ghast, false)
	loop.playerStartRiding(p1, ghast, false)

	loop.ejectPassengers(ghast)

	if len(ghast.passengers) != 0 {
		t.Fatalf("after ejectPassengers, ghast passengers = %v, want empty", ghast.passengers)
	}
	if p0.vehicleID != 0 || p1.vehicleID != 0 {
		t.Fatalf("after eject, riders still mounted: p0=%d p1=%d", p0.vehicleID, p1.vehicleID)
	}
}

// TestRiddenGhastAiSuppressed: while a controlling passenger steers the ghast, happyGhastAiStep is a
// no-op — the server-side RandomFloatAroundGoal must not fight the client-driven motion. We assert the
// ghast's wanted-fly-to state is NOT set after an AI step while ridden.
func TestRiddenGhastAiSuppressed(t *testing.T) {
	loop, ghast := rideTestLoop(t)
	p := ridePlayer(loop, 1, 8.5, 64, 8.5, 0)
	loop.playerStartRiding(p, ghast, false)

	ghast.ghastHasWanted = false
	loop.happyGhastAiStep(ghast)
	if ghast.ghastHasWanted {
		t.Fatal("a ridden ghast picked a fly-to target — its AI moveControl must be suppressed while steered")
	}

	// After dismount the AI resumes: it picks a wanted target again.
	loop.playerStopRiding(p)
	loop.happyGhastAiStep(ghast)
	if !ghast.ghastHasWanted {
		t.Fatal("after dismount the ghast AI did not resume (no fly-to target picked)")
	}
}

// ridePosEq compares two float64s within a small epsilon (the seat math involves float32 trig).
func ridePosEq(a, b float64) bool { return math.Abs(a-b) < 1e-4 }
