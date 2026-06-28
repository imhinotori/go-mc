package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world"
)

// --- ENT-02 physics test harness -------------------------------------------------------
//
// The tests build a tiny tick-owned world by hand: a ChunkManager with one ready chunk
// whose blocks we set directly (a flat floor / a wall). The TickLoop's minY is the
// overworld floor (-64), threaded into the block-at-pos read. Entities come from the
// generated data/entity table (Width/Height drive the AABB).

// physicsSecs is the dimension section count used by the test world (overworld 24).
const physicsSecs = 24

// newPhysicsLoop wires a TickLoop with a tick-owned ChunkManager (no off-tick worker —
// physics never needs one). The manager is the same plain map the production loop owns.
func newPhysicsLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	// Wire the manager directly (SetWorld also starts a worker bridge we don't need for
	// physics-only tests; assign the tick-owned field straight so there is no goroutine).
	loop.only().world = mgr
	// PLUGIN-04 (Plan 24-02): the SWAP routes the natural/debug pig spawn through spawnVanillaPig,
	// which needs the boot-loaded vanilla_pig registry. Install it on every physics loop so any test
	// driving the spawn paths (spawner/async-stress/debug) has the declaration — the same boot-load
	// the server runs, just at test setup. A load failure here fails the test loudly (it would mean
	// the embedded plugin is broken).
	installVanillaPigRegistry(loop)
	return loop, mgr
}

// installVanillaPigRegistry boot-loads the embedded vanilla_pig plugin and installs it on the loop so
// the SWAP's spawnVanillaPig works in tests. Panics on a load failure (a broken embedded plugin is a
// hard error, not a skippable test condition). Idempotent-safe to call once per loop.
func installVanillaPigRegistry(loop *TickLoop) {
	reg, err := loadVanillaPigRegistry()
	if err != nil {
		panic("test setup: vanilla_pig boot-load failed: " + err.Error())
	}
	loop.SetMobRegistry(reg)
}

// putChunk inserts an empty (all-air) ready chunk at column col so blocks can be placed.
func putChunk(mgr *world.ChunkManager, col level.ChunkPos) *level.Chunk {
	ch := level.EmptyChunk(physicsSecs)
	ch.Status = level.StatusFull
	mgr.Insert(col, ch)
	return ch
}

// setBlock places a solid (stone) block at world (x,y,z) in the chunk, mirroring the
// generator's section/local mapping. minY is the overworld floor.
func setBlock(ch *level.Chunk, x, y, z int) {
	sec := (y - dimMinY) >> 4
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	if sec < 0 || sec >= len(ch.Sections) {
		panic("test block out of section range")
	}
	ch.Sections[sec].SetBlock(local, block.ToStateID[block.Stone{}])
}

// fillFloor lays a solid stone slab across the whole 16x16 chunk at world-Y = y.
func fillFloor(ch *level.Chunk, y int) {
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			setBlock(ch, x, y, z)
		}
	}
}

// fillWall lays a solid stone column (full height range) at a single (x,z) across yLo..yHi.
func fillWall(ch *level.Chunk, x, z, yLo, yHi int) {
	for y := yLo; y <= yHi; y++ {
		setBlock(ch, x, y, z)
	}
}

// testEntity builds a live Entity from a data/entity record at (x,y,z).
func testEntity(id int32, t entity.Entity, x, y, z float64) *Entity {
	return NewEntity(id, t, x, y, z)
}

// TestEntityLands: an entity above a solid floor falls under gravity and LANDS on the
// floor surface — onGround becomes true and it never sinks below the floor top.
func TestEntityLands(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY) // solid block occupies [64,65); its top surface is y=65

	// Sulfur cube spawned a few blocks above the floor, at rest.
	e := testEntity(1, entity.SulfurCube, 8.5, 70.0, 8.5)
	loop.only().entities.add(e)

	// Drive enough physics ticks for it to fall ~5 blocks and settle.
	for i := 0; i < 200; i++ {
		loop.tickPhysics()
		if e.y < float64(floorY+1) {
			t.Fatalf("tick %d: entity sank below floor top (y=%v < %v)", i, e.y, floorY+1)
		}
	}

	const floorTop = floorY + 1 // 65
	if d := e.y - float64(floorTop); d < -1e-6 || d > 1e-3 {
		t.Fatalf("entity did not settle on the floor: y=%v want ~%v", e.y, floorTop)
	}
	if !e.onGround {
		t.Fatalf("entity resting on the floor must have onGround=true (y=%v)", e.y)
	}
}

// TestEntityBlockedByWall: an entity moving horizontally into a solid wall is BLOCKED —
// its X stops at the wall face and does not enter the wall.
func TestEntityBlockedByWall(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	// A wall block at x=10 (occupies [10,11)) across the entity's height band.
	fillWall(ch, 10, 8, 65, 67)

	e := testEntity(1, entity.SulfurCube, 8.5, 65.0, 8.5) // width 0.49 => half 0.245
	loop.only().entities.add(e)
	e.vx = 0.4 // walk east toward the wall at x=10

	for i := 0; i < 100; i++ {
		loop.tickPhysics()
	}

	// The entity's east face is x + halfW; it must not pass the wall's west face at x=10.
	halfW := entity.SulfurCube.Width / 2
	if e.x+halfW > 10.0+1e-6 {
		t.Fatalf("entity entered the wall: x=%v (east face %v) > wall face 10", e.x, e.x+halfW)
	}
	// And it must have actually advanced toward the wall (not stuck at spawn).
	if e.x <= 8.5 {
		t.Fatalf("entity did not move toward the wall at all: x=%v", e.x)
	}
}

// TestNoTunnel: an entity given a LARGE single-tick horizontal velocity toward a
// 1-block-thick wall does NOT end up on the far side — per-axis sweep clips it at the wall.
func TestNoTunnel(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	fillWall(ch, 12, 8, 65, 67) // 1-block-thick wall at x=12

	e := testEntity(1, entity.SulfurCube, 8.5, 65.0, 8.5)
	loop.only().entities.add(e)
	// One huge horizontal velocity that, moved as a full vector, would land PAST the wall.
	e.vx = 10.0

	loop.tickPhysics() // single tick

	halfW := entity.SulfurCube.Width / 2
	if e.x+halfW > 12.0+1e-6 {
		t.Fatalf("entity tunneled through the wall: x=%v (east face %v) past wall at 12", e.x, e.x+halfW)
	}
}

// TestGravityTunable: in open air an entity accelerates downward (vy grows negative,
// bounded by drag) and does not collide. Confirms the gravity/drag constants drive motion.
func TestGravityTunable(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0}) // all-air chunk: nothing to collide with

	e := testEntity(1, entity.SulfurCube, 8.5, 200.0, 8.5)
	loop.only().entities.add(e)

	prevVy := e.vy
	for i := 0; i < 5; i++ {
		loop.tickPhysics()
		if e.vy >= prevVy {
			t.Fatalf("tick %d: vy did not decrease under gravity: vy=%v prev=%v", i, e.vy, prevVy)
		}
		prevVy = e.vy
	}
	if e.onGround {
		t.Fatal("an entity in open air must not be onGround")
	}
	if e.y >= 200.0 {
		t.Fatalf("entity did not fall: y=%v (expected < 200)", e.y)
	}
	// Terminal-velocity sanity: drag must keep vy bounded (it never free-falls unbounded
	// within a few ticks). gravity 0.08 / drag 0.98 => |vy| stays small for a while.
	if e.vy < -10.0 {
		t.Fatalf("vy unbounded by drag: vy=%v", e.vy)
	}
}

// TestTickPhysicsRunsGravity: with an entity in the store above a floor, the physics
// PHASE (tickPhysics, driven repeatedly) lands it — proving the phase drives moveEntity
// for every store entity each tick.
func TestTickPhysicsRunsGravity(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.SulfurCube, 8.5, 80.0, 8.5)
	loop.only().entities.add(e)

	for i := 0; i < 300; i++ {
		loop.tickPhysics()
	}

	const floorTop = floorY + 1
	if d := e.y - float64(floorTop); d < -1e-6 || d > 1e-3 {
		t.Fatalf("tickPhysics did not land the store entity: y=%v want ~%v", e.y, floorTop)
	}
	if !e.onGround {
		t.Fatal("landed store entity must have onGround=true")
	}
	// The store bucket must reflect the post-physics position (move() re-buckets).
	col := columnOf(e.x, e.z)
	found := false
	for _, be := range loop.only().entities.near(e.x, e.z, 0) {
		if be == e {
			found = true
		}
	}
	if !found {
		t.Fatalf("entity not found in its bucket %v after physics (stale bucket)", col)
	}
}

// TestPlayerClipRejected: a player whose client-sent position is INSIDE a solid block is
// corrected by collidePlayer — the accepted tick-owned position is clamped OUT of the
// block (the client cannot sit inside / clip through stone).
func TestPlayerClipRejected(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	// A solid 3-high stone pillar at (x=8,z=8) occupying y=64..66.
	fillWall(ch, 8, 8, 64, 66)

	p := confirmedPlayer()
	// Player starts standing just west of the pillar at x=6, on the ground.
	p.x, p.y, p.z = 6.0, 67.0, 8.5

	// Client claims it walked EAST straight into the middle of the solid pillar (x=8.5).
	in := movePlayerPos(8.5, 65.0, 8.5, 0x01)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: in})

	// The accepted position must NOT be inside the solid pillar block at (8,*,8).
	// The pillar block occupies x in [8,9), z in [8,9), y in [64,67). The player box
	// (width ~0.6) centered at the accepted (x,z) must not overlap it.
	if insideSolidColumn(loop, p.x, p.y, p.z, playerWidth, playerHeight) {
		t.Fatalf("player clipped into solid block: accepted pos=(%v,%v,%v)", p.x, p.y, p.z)
	}
}

// insideSolidColumn reports whether the player AABB centered at (x,y,z) overlaps any solid
// block — a test-side oracle independent of the physics implementation.
func insideSolidColumn(loop *TickLoop, x, y, z, w, h float64) bool {
	hw := w / 2
	minX, maxX := x-hw, x+hw
	minZ, maxZ := z-hw, z+hw
	minY, maxY := y, y+h
	for bx := floorI(minX); bx <= floorI(maxX-1e-9); bx++ {
		for by := floorI(minY); by <= floorI(maxY-1e-9); by++ {
			for bz := floorI(minZ); bz <= floorI(maxZ-1e-9); bz++ {
				if loop.blockSolidAt(bx, by, bz) {
					// overlap on all axes already implied by the iteration bounds
					return true
				}
			}
		}
	}
	return false
}
