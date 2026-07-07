package server

// collision_test.go — behavior tests for the VoxelShape collision engine (collision.go +
// physics.go moveEntity): real partial shapes (slab/fence/stairs) and the Entity.collide
// auto step-up (phase 3). The expected numbers are exact — the engine clamps flush against
// shape faces (no bisection slack).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// setStateBlock places an arbitrary block state at world (x,y,z) in the chunk (the generic
// sibling of physics_test.go's stone-only setBlock).
func setStateBlock(ch *level.Chunk, x, y, z int, sid block.StateID) {
	sec := (y - dimMinY) >> 4
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	if sec < 0 || sec >= len(ch.Sections) {
		panic("test block out of section range")
	}
	ch.Sections[sec].SetBlock(local, sid)
}

// mustState resolves a Block literal to its StateID.
func mustState(t *testing.T, b block.Block) block.StateID {
	t.Helper()
	sid, ok := block.ToStateID[b]
	if !ok {
		t.Fatalf("no StateID for %#v", b)
	}
	return sid
}

// TestMoveEntityStepUpSlab: a grounded mob walking into a bottom slab auto-steps onto it —
// the Entity.collide maxUpStep branch (0.5 <= 0.6) — instead of being wall-blocked.
func TestMoveEntityStepUpSlab(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY) // floor top at y=65
	setStateBlock(ch, 10, 65, 8, mustState(t, block.StoneSlab{Type: block.SlabTypeBottom})) // top at 65.5

	e := testEntity(1, entity.SulfurCube, 9.5, 65.0, 8.5) // width 0.49 → east face 9.745
	loop.only().entities.add(e)
	e.onGround = true // grounded (the step-up gate reads the PRE-move flag)

	// One walking move: +0.5 east with gravity. Unstepped it would clamp at the slab's west
	// face (x face 10.0); the step-up candidate h=0.5 clears it and advances the full 0.5.
	loop.moveEntity(e, 0.5, -0.0784, 0)

	if e.x != 10.0 {
		t.Fatalf("step-up onto slab: x=%v, want 10.0 (full advance)", e.x)
	}
	if e.y != 65.5 {
		t.Fatalf("step-up onto slab: y=%v, want 65.5 (slab top)", e.y)
	}
	if !e.onGround {
		t.Fatal("stepped mob must be onGround (verticalCollisionBelow)")
	}
}

// TestMoveEntityFullBlockNotStepped: a full 1-block ledge exceeds maxUpStep (1.0 > 0.6) —
// the mob is wall-blocked exactly at the block face (vanilla clears this by JUMPING).
func TestMoveEntityFullBlockNotStepped(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	setBlock(ch, 10, 65, 8) // full stone: top at 66 → step 1.0 > 0.6

	e := testEntity(1, entity.SulfurCube, 9.5, 65.0, 8.5)
	loop.only().entities.add(e)
	e.onGround = true

	loop.moveEntity(e, 0.5, -0.0784, 0)

	halfW := entity.SulfurCube.Width / 2
	if got := e.x + halfW; got != 10.0 {
		t.Fatalf("full block: east face %v, want exactly 10.0 (flush)", got)
	}
	if e.y != 65.0 {
		t.Fatalf("full block: y=%v, want 65.0 (no step)", e.y)
	}
	if !e.horizontalCollision {
		t.Fatal("full block: horizontalCollision must be true")
	}
}

// TestMoveEntityFenceNotStepped: a fence is 1.5 blocks tall — its top Y coord exceeds
// maxUpStep, so a mob is blocked at the POST face (0.375), not the cell edge, and never
// steps up. The signature fence behavior the old full-cube stub got wrong twice over.
func TestMoveEntityFenceNotStepped(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	setStateBlock(ch, 10, 65, 8, mustState(t, block.OakFence{}))

	e := testEntity(1, entity.SulfurCube, 9.9, 65.0, 8.5) // east face 10.145, inside the cell but west of the post
	loop.only().entities.add(e)
	e.onGround = true

	loop.moveEntity(e, 0.5, -0.0784, 0)

	halfW := entity.SulfurCube.Width / 2
	if got := e.x + halfW; got != 10.375 {
		t.Fatalf("fence: east face %v, want exactly 10.375 (the post face)", got)
	}
	if e.y != 65.0 {
		t.Fatalf("fence: y=%v, want 65.0 (1.5 > maxUpStep, no step-up)", e.y)
	}
}

// TestMoveEntityStairsAscend: walking into bottom stairs steps the riser like vanilla
// (both halves are 0.5 steps).
func TestMoveEntityStairsAscend(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	// Stairs at x=10 facing east: the riser (full half) is on the east half x∈[10.5,11],
	// the 0.5 tread on the west half — the ascending approach for a mob walking east.
	setStateBlock(ch, 10, 65, 8, mustState(t, block.OakStairs{
		Facing: block.East, Half: block.Bottom, Shape: block.StairsShapeStraight,
	}))

	e := testEntity(1, entity.SulfurCube, 9.5, 65.0, 8.5)
	loop.only().entities.add(e)
	e.onGround = true

	// First step: onto the low tread (y 65.5).
	loop.moveEntity(e, 0.5, -0.0784, 0)
	if e.y != 65.5 || e.x != 10.0 {
		t.Fatalf("stairs first step: (x,y)=(%v,%v), want (10.0, 65.5)", e.x, e.y)
	}
	// Second step: onto the riser top (y 66).
	loop.moveEntity(e, 0.5, -0.0784, 0)
	if e.y != 66.0 || e.x != 10.5 {
		t.Fatalf("stairs second step: (x,y)=(%v,%v), want (10.5, 66.0)", e.x, e.y)
	}
}

// TestMoveEntityNoStepUpForNonLiving: a non-living entity (item) has maxUpStep()==0 — it is
// blocked by the slab edge a mob would step.
func TestMoveEntityNoStepUpForNonLiving(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	setStateBlock(ch, 10, 65, 8, mustState(t, block.StoneSlab{Type: block.SlabTypeBottom}))

	e := testEntity(1, entity.SulfurCube, 9.5, 65.0, 8.5)
	e.isItem = true // base Entity.maxUpStep() == 0.0F
	loop.only().entities.add(e)
	e.onGround = true

	loop.moveEntity(e, 0.5, -0.0784, 0)

	halfW := entity.SulfurCube.Width / 2
	if got := e.x + halfW; got != 10.0 {
		t.Fatalf("item vs slab: east face %v, want exactly 10.0 (blocked, no step)", got)
	}
	if e.y != 65.0 {
		t.Fatalf("item vs slab: y=%v, want 65.0", e.y)
	}
}

// TestMoveEntityWalksUnderFenceGap: the space from y=66.5 (fence top) upward is clear — an
// entity flying above the fence passes over it (the old full-cube stub blocked the whole
// cell up to 66).
func TestMoveEntityWalksUnderFenceGap(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	setStateBlock(ch, 10, 65, 8, mustState(t, block.OakFence{}))

	e := testEntity(1, entity.SulfurCube, 9.5, 66.5, 8.5) // feet exactly at the fence top
	loop.only().entities.add(e)

	loop.moveEntity(e, 1.0, 0, 0)
	if e.x != 10.5 {
		t.Fatalf("over-fence pass: x=%v, want 10.5 (unobstructed above 1.5)", e.x)
	}
}

// TestCollidePlayerAgainstSlab: a client claiming a position whose box clips INTO a slab is
// corrected against the slab's REAL half-box (the old stub corrected against a full cube).
func TestCollidePlayerAgainstSlab(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	setStateBlock(ch, 8, 64, 8, mustState(t, block.StoneSlab{Type: block.SlabTypeBottom})) // occupies y 64..64.5

	p := &tickPlayer{x: 8.5, y: 66.0, z: 8.5}
	// Claim: dropped INSIDE the slab (feet at y=64.2 → box overlaps the slab volume).
	x, y, z := loop.collidePlayer(p, 8.5, 64.2, 8.5)
	if x != 8.5 || z != 8.5 {
		t.Fatalf("slab clip: horizontal moved (%v,%v), want (8.5,8.5)", x, z)
	}
	if y != 64.5 {
		t.Fatalf("slab clip: y=%v, want 64.5 (clamped onto the slab top, not the cell top)", y)
	}
	// A claim standing exactly ON the slab top is clear and accepted verbatim.
	x, y, z = loop.collidePlayer(p, 8.5, 64.5, 8.5)
	if x != 8.5 || y != 64.5 || z != 8.5 {
		t.Fatalf("slab stand: (%v,%v,%v), want (8.5,64.5,8.5) verbatim", x, y, z)
	}
}
