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

// minecart_test.go — MINECART + RAILS validation gates, each asserting the ported OldMinecartBehavior /
// AbstractMinecart behaviour against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar):
//   - a cart on a straight rail with initial velocity ROLLS along the rail axis, snapped to the rail centre;
//   - getMaxSpeed == 0.4 (land) and the slowdown factors 0.997 (ridden) / 0.96 (empty) match the jar;
//   - a POWERED powered_rail ACCELERATES a moving cart; an UNPOWERED one BRAKES it;
//   - a DetectorRail POWERS while a cart is on it (a redstone source) and clears when it leaves;
//   - a hopper below a chest-minecart PULLS an item (the filled getEntityContainer seam);
//   - the EXITS table matches AbstractMinecart.EXITS.

// newMinecartLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container for
// column (0,0). Mirrors newHopperLoop.
func newMinecartLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// TestMinecartRollsAlongStraightRail: a minecart on a straight EAST_WEST rail with an initial +X velocity
// advances along the X axis and stays snapped to the rail centre (Z ≈ 8.5), the load-bearing rail follow.
func TestMinecartRollsAlongStraightRail(t *testing.T) {
	loop, mgr := newMinecartLoop()

	railPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(railPos, block.ToStateID[block.Rail{Shape: block.RailShapeEastWest}], dimMinY)

	// Spawn the cart at the rail centre (x+0.5, y+0.0625, z+0.5) with a +X push.
	cart := loop.spawnMinecart(entity.Minecart.ID, 8.5, 64.0625, 8.5)
	cart.vx = 0.1

	startX := cart.x
	for i := 0; i < 5; i++ {
		loop.tickMinecart(cart)
	}

	if cart.x <= startX {
		t.Fatalf("cart X did not advance along the rail: start %.4f, now %.4f (want > start)", startX, cart.x)
	}
	// The rail-snap keeps the cart on the centre-line of an EAST_WEST rail (Z fixed at the block centre 8.5).
	if math.Abs(cart.z-8.5) > 1e-6 {
		t.Fatalf("cart drifted off the rail centre: Z = %.6f, want 8.5 (snapped)", cart.z)
	}
	// It must stay recognised as on-rails (not fall through).
	if !block.IsRail(func() block.StateID { s, _ := mgr.GetBlock(loop.minecartCurrentBlockPosOrRailBelow(cart), dimMinY); return s }()) {
		t.Fatalf("cart left the rail cell while rolling straight")
	}
}

// TestMinecartMaxSpeedAndSlowdown locks the jar constants: getMaxSpeed 0.4 on land; the slowdown factor
// 0.96 empty, 0.997 with a passenger. CITE OldMinecartBehavior.getMaxSpeed / getSlowdownFactor.
func TestMinecartMaxSpeedAndSlowdown(t *testing.T) {
	loop, _ := newMinecartLoop()
	cart := loop.spawnMinecart(entity.Minecart.ID, 8.5, 64.0625, 8.5)

	if got := loop.minecartGetMaxSpeed(cart); got != 0.4 {
		t.Fatalf("getMaxSpeed(land) = %v, want 0.4 (MAX_SPEED_ON_LAND)", got)
	}
	if got := cart.minecartSlowdownFactor(); got != 0.96 {
		t.Fatalf("empty slowdown = %v, want 0.96", got)
	}
	// Give it a passenger (a non-zero passenger id) → isVehicle → 0.997.
	cart.passengers = []int32{999}
	if got := cart.minecartSlowdownFactor(); got != 0.997 {
		t.Fatalf("ridden slowdown = %v, want 0.997", got)
	}
}

// TestMinecartExitsTable locks AbstractMinecart.EXITS against the jar unit vectors for every RailShape.
func TestMinecartExitsTable(t *testing.T) {
	want := map[block.RailShape][2]railExit{
		block.RailShapeNorthSouth:     {{0, 0, -1}, {0, 0, 1}},
		block.RailShapeEastWest:       {{-1, 0, 0}, {1, 0, 0}},
		block.RailShapeAscendingEast:  {{-1, -1, 0}, {1, 0, 0}},
		block.RailShapeAscendingWest:  {{-1, 0, 0}, {1, -1, 0}},
		block.RailShapeAscendingNorth: {{0, 0, -1}, {0, -1, 1}},
		block.RailShapeAscendingSouth: {{0, -1, -1}, {0, 0, 1}},
		block.RailShapeSouthEast:      {{0, 0, 1}, {1, 0, 0}},
		block.RailShapeSouthWest:      {{0, 0, 1}, {-1, 0, 0}},
		block.RailShapeNorthWest:      {{0, 0, -1}, {-1, 0, 0}},
		block.RailShapeNorthEast:      {{0, 0, -1}, {1, 0, 0}},
	}
	for shape, w := range want {
		if got := minecartExitsOf(shape); got != w {
			t.Fatalf("exits(%v) = %v, want %v (AbstractMinecart.EXITS)", shape, got, w)
		}
	}
}

// TestMinecartPoweredRailAccelerates: a moving cart on a POWERED powered_rail gains speed (the 0.06 boost),
// while the SAME cart on an UNPOWERED powered_rail loses speed (the halt-track brake). CITE
// OldMinecartBehavior.moveAlongTrack powered/halt branches.
func TestMinecartPoweredRailAccelerates(t *testing.T) {
	// POWERED: the cart accelerates.
	loopP, mgrP := newMinecartLoop()
	mgrP.SetBlock(pk.Position{X: 8, Y: 64, Z: 8},
		block.ToStateID[block.PoweredRail{Powered: true, Shape: block.RailShapeEastWest}], dimMinY)
	// A straight run of powered rail so the cart stays on power as it rolls.
	for x := 5; x <= 12; x++ {
		mgrP.SetBlock(pk.Position{X: x, Y: 64, Z: 8},
			block.ToStateID[block.PoweredRail{Powered: true, Shape: block.RailShapeEastWest}], dimMinY)
	}
	cartP := loopP.spawnMinecart(entity.Minecart.ID, 8.5, 64.0625, 8.5)
	cartP.vx = 0.05
	speedP0 := math.Hypot(cartP.vx, cartP.vz)
	for i := 0; i < 3; i++ {
		loopP.tickMinecart(cartP)
	}
	speedP1 := math.Hypot(cartP.vx, cartP.vz)
	if speedP1 <= speedP0 {
		t.Fatalf("powered rail did not accelerate: speed %.5f -> %.5f (want increase)", speedP0, speedP1)
	}

	// UNPOWERED: the same run brakes the cart toward a stop.
	loopU, mgrU := newMinecartLoop()
	for x := 5; x <= 12; x++ {
		mgrU.SetBlock(pk.Position{X: x, Y: 64, Z: 8},
			block.ToStateID[block.PoweredRail{Powered: false, Shape: block.RailShapeEastWest}], dimMinY)
	}
	cartU := loopU.spawnMinecart(entity.Minecart.ID, 8.5, 64.0625, 8.5)
	cartU.vx = 0.2
	speedU0 := math.Hypot(cartU.vx, cartU.vz)
	for i := 0; i < 3; i++ {
		loopU.tickMinecart(cartU)
	}
	speedU1 := math.Hypot(cartU.vx, cartU.vz)
	if speedU1 >= speedU0 {
		t.Fatalf("unpowered powered_rail did not brake: speed %.5f -> %.5f (want decrease)", speedU0, speedU1)
	}
}

// TestDetectorRailPowersWithCart: a detector rail with a minecart on it flips POWERED=true (a redstone
// source with getSignal 15); with no cart the scheduled re-check clears it. CITE DetectorRailBlock.checkPressed.
func TestDetectorRailPowersWithCart(t *testing.T) {
	loop, mgr := newMinecartLoop()

	railPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(railPos, block.ToStateID[block.DetectorRail{Powered: false, Shape: block.RailShapeEastWest}], dimMinY)

	cart := loop.spawnMinecart(entity.Minecart.ID, 8.5, 64.0625, 8.5)
	// Tick the cart once: it is on the detector rail → checkPressed powers it.
	loop.tickMinecart(cart)

	after, _ := mgr.GetBlock(railPos, dimMinY)
	powered, ok := block.RailPowered(after)
	if !ok || !powered {
		t.Fatalf("detector rail with a cart on it is not POWERED (ok=%v powered=%v)", ok, powered)
	}
	// It must be a redstone weak-signal source of 15 while powered (getSignal/ownSignal).
	if sig := loop.stateGetSignal(after, railPos, block.Up); sig != 15 {
		t.Fatalf("powered detector rail weak signal = %d, want 15", sig)
	}

	// Move the cart far away and run the scheduled re-check: with no cart present, POWERED clears.
	loop.only().entities.move(cart, 200.5, 64.0, 200.5)
	loop.detectorRailCheckPressed(railPos, after)
	cleared, _ := mgr.GetBlock(railPos, dimMinY)
	stillPowered, _ := block.RailPowered(cleared)
	if stillPowered {
		t.Fatalf("detector rail stayed POWERED after the cart left (want cleared)")
	}
}

// TestHopperPullsFromChestMinecart is the FILLED getEntityContainer seam: a hopper directly below a
// chest-minecart PULLS one item per 8-tick cooldown from the cart into itself (suckInItems ->
// getContainerAt(above) -> getEntityContainer). CITE HopperBlockEntity.suckInItems + getEntityContainer.
func TestHopperPullsFromChestMinecart(t *testing.T) {
	loop, mgr := newMinecartLoop()

	hopperPos := pk.Position{X: 5, Y: 64, Z: 5}
	hopperState := block.ToStateID[block.Hopper{Facing: block.Down, Enabled: true}]
	mgr.SetBlock(hopperPos, hopperState, dimMinY)
	h := loop.resolveHopper(hopperPos, hopperState)

	// A chest-minecart occupying the block cell DIRECTLY ABOVE the hopper (its box intersects that cell).
	cart := loop.spawnMinecart(entity.ChestMinecart.ID, 5.5, 65.0, 5.5)
	if len(cart.minecartItems) != 27 {
		t.Fatalf("chest minecart container size = %d, want 27", len(cart.minecartItems))
	}
	cart.minecartItems[0] = cobble(3)

	// getContainerAt(above) must resolve the chest-minecart via the entity-container seam.
	if cv := loop.getContainerAt(above(hopperPos)); cv == nil {
		t.Fatalf("getContainerAt(above hopper) returned nil — the chest-minecart entity-container seam is not filled")
	}

	loop.tickHoppers()
	if got := countHopper(h); got != 1 {
		t.Fatalf("after 1 tick hopper holds %d items, want 1 (single-item pull from the chest-minecart)", got)
	}
	cartTotal := 0
	for _, s := range cart.minecartItems {
		cartTotal += int(s.Count)
	}
	if cartTotal != 2 {
		t.Fatalf("after 1 tick chest-minecart holds %d items, want 2 (one pulled out)", cartTotal)
	}
}

// TestMinecartOffRailFalls: a minecart NOT on a rail falls under gravity (comeOffTrack), the off-rail
// normal physics. CITE AbstractMinecart.comeOffTrack + applyGravity.
func TestMinecartOffRailFalls(t *testing.T) {
	loop, _ := newMinecartLoop()
	cart := loop.spawnMinecart(entity.Minecart.ID, 8.5, 80.0, 8.5) // no rail, no floor: free air
	startY := cart.y
	loop.tickMinecart(cart)
	if cart.y >= startY {
		t.Fatalf("off-rail cart did not fall: Y %.4f -> %.4f (want decrease under gravity)", startY, cart.y)
	}
	if cart.vy >= 0 {
		t.Fatalf("off-rail cart has non-negative Y velocity %.4f (want falling)", cart.vy)
	}
}
