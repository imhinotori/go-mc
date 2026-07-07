package server

// falling_block_test.go — FALLING BLOCK end-to-end: the FallingBlock scheduled-tick round-trip
// (schedule via onFallingBlockEdit / a direct schedule -> drain at the target game-time via
// tickScheduledBlocks -> fallingBlockTick -> isFree(below) true -> spawnFallingBlock: entity spawns +
// source block becomes air), the FallingBlockEntity fall (tickFallingBlocks: y decreases each tick) and
// land (GetBlock at the landing cell == the carried state), and the supported-block no-op (below solid ->
// no fall). All against the ported vanilla logic (falling_block.go). Also verifies the isFree predicate
// and the block-tick-type routing.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// sandStateID / gravelStateID are the default state ids of the in-scope FallingBlock kinds.
func sandStateID() block.StateID   { return block.ToStateID[block.Sand{}] }
func gravelStateID() block.StateID { return block.ToStateID[block.Gravel{}] }
func stoneStateID() block.StateID  { return block.ToStateID[block.Stone{}] }

// newFallingBlockLoop wires a physics-capable TickLoop (solid-block collision for landing) whose region 0
// world has a ready all-air chunk AND a registered block-tick container for column (0,0), so the
// scheduled FallingBlock.tick has a container to route into.
func newFallingBlockLoop() (*TickLoop, *world.ChunkManager) {
	loop, mgr := newPhysicsLoop()
	putChunk(mgr, level.ChunkPos{0, 0})
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// countFalling returns how many FallingBlockEntity entities live across all region stores.
func countFalling(loop *TickLoop) (n int, first *Entity) {
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isFalling {
				n++
				if first == nil {
					first = e
				}
			}
		}
	}
	return n, first
}

// TestFallingBlockIsFree locks the FallingBlock.isFree predicate: air/water/lava/fire are FREE (a block
// above them falls), a solid block (stone/sand/gravel) is NOT free (a block above it is supported).
func TestFallingBlockIsFree(t *testing.T) {
	air := block.ToStateID[block.Air{}]
	if !fallingBlockIsFree(air) {
		t.Fatal("isFree(air) = false, want true (air is replaceable)")
	}
	if fallingBlockIsFree(stoneStateID()) {
		t.Fatal("isFree(stone) = true, want false (solid support)")
	}
	if fallingBlockIsFree(sandStateID()) {
		t.Fatal("isFree(sand) = true, want false (sand is a solid support)")
	}
}

// TestFallingBlockTickTypeRouting locks the block-tick-type mapping: sand/red_sand/gravel route to their
// own ids; a non-FallingBlock (stone) does not route.
func TestFallingBlockTickTypeRouting(t *testing.T) {
	if typ, ok := fallingBlockTickType(sandStateID()); !ok || typ != sandTickType {
		t.Fatalf("sand -> (%q, %v), want (%q, true)", typ, ok, sandTickType)
	}
	if typ, ok := fallingBlockTickType(gravelStateID()); !ok || typ != gravelTickType {
		t.Fatalf("gravel -> (%q, %v), want (%q, true)", typ, ok, gravelTickType)
	}
	if _, ok := fallingBlockTickType(stoneStateID()); ok {
		t.Fatal("stone routed to a falling-block tick type, want no route")
	}
	if !isFallingBlockKind(sandStateID()) || !isFallingBlockKind(gravelStateID()) {
		t.Fatal("sand/gravel not recognized as FallingBlock kinds")
	}
	if isFallingBlockKind(stoneStateID()) {
		t.Fatal("stone recognized as a FallingBlock kind, want false")
	}
}

// TestScheduledSandWithAirBelowSpawnsEntity: a sand block at (2,70,2) with air below, whose FallingBlock
// tick is scheduled and then drained at the target game-time, spawns exactly one FallingBlockEntity
// (carrying the sand state, centered at 2.5/70/2.5) and turns the SOURCE block to air. The port of
// FallingBlock.tick -> FallingBlockEntity.fall.
func TestScheduledSandWithAirBelowSpawnsEntity(t *testing.T) {
	loop, mgr := newFallingBlockLoop()

	src := pk.Position{X: 2, Y: 70, Z: 2}
	mgr.SetBlock(src, sandStateID(), dimMinY)

	// Schedule the FallingBlock.tick (the onPlace/updateShape half) and drain it at the trigger tick.
	loop.scheduleFallingBlockTick(src, sandStateID())
	if !loop.hasScheduledBlockTick(src, sandTickType) {
		t.Fatal("FallingBlock tick was not scheduled")
	}
	loop.gametime += fallingBlockDelayAfterPlace // advance to the scheduled trigger tick
	loop.tickScheduledBlocks()

	// The source block is now air (FallingBlockEntity.fall removed it).
	if got, ok := mgr.GetBlock(src, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("after fall, source GetBlock = (%v, ok=%v), want air", got, ok)
	}
	// Exactly one FallingBlockEntity spawned, carrying sand, centered on the source block.
	n, fe := countFalling(loop)
	if n != 1 || fe == nil {
		t.Fatalf("FallingBlockEntity count = %d, want exactly 1", n)
	}
	if fe.fallingBlockState != sandStateID() {
		t.Fatalf("entity carries state %d, want sand %d", fe.fallingBlockState, sandStateID())
	}
	if fe.x != 2.5 || fe.y != 70.0 || fe.z != 2.5 {
		t.Fatalf("entity pos = (%v,%v,%v), want (2.5,70,2.5) (block-centered)", fe.x, fe.y, fe.z)
	}
}

// TestFallingBlockFallsAndLandsAsSand: a sand FallingBlockEntity spawned above a stone floor FALLS (y
// strictly decreases across tickFallingBlocks calls) and LANDS as sand on top of the floor (GetBlock at
// the resting cell == sand, entity discarded). The port of FallingBlockEntity.tick's gravity+move +
// onGround setBlock(blockState).
func TestFallingBlockFallsAndLandsAsSand(t *testing.T) {
	loop, mgr := newFallingBlockLoop()

	// Stone floor at y=64; the sand entity spawns at y=70 (cells 65..69 are air to fall through).
	floorY := 64
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	setBlock(ch, 2, floorY, 2)

	// Spawn the entity directly (the fall() half) above the floor.
	src := pk.Position{X: 2, Y: 70, Z: 2}
	fe := loop.spawnFallingBlock(src, sandStateID())
	if fe == nil {
		t.Fatal("spawnFallingBlock returned nil")
	}
	startY := fe.y

	// Tick the fall to completion (entity lands or the loop bounds out). Each tick applies gravity+move.
	landed := false
	prevY := startY
	for i := 0; i < 200; i++ {
		loop.tickFallingBlocks()
		n, cur := countFalling(loop)
		if n == 0 {
			landed = true
			break
		}
		// While falling, y must be non-increasing and generally decreasing (gravity).
		if cur.y > prevY+1e-9 {
			t.Fatalf("tick %d: entity y INCREASED %v -> %v (gravity should pull it down)", i, prevY, cur.y)
		}
		prevY = cur.y
	}
	if !landed {
		t.Fatalf("entity never landed/discarded after 200 ticks (startY=%v, lastY=%v)", startY, prevY)
	}
	if prevY >= startY {
		t.Fatalf("entity did not fall: startY=%v, lastY=%v", startY, prevY)
	}

	// It landed as sand on top of the floor (y=65, directly above the stone at y=64).
	landPos := pk.Position{X: 2, Y: floorY + 1, Z: 2}
	got, ok := mgr.GetBlock(landPos, dimMinY)
	if !ok || !isFallingBlockKind(got) || got != sandStateID() {
		t.Fatalf("landing cell (2,%d,2) GetBlock = (%v, ok=%v), want sand %d", floorY+1, got, ok, sandStateID())
	}
}

// TestSupportedSandDoesNotFall: a sand block with a SOLID block directly below (not free) does NOT fall
// when its FallingBlock tick fires — no entity spawns and the source block stays sand. The port of
// FallingBlock.tick's "if (isFree(below)) ..." guard failing.
func TestSupportedSandDoesNotFall(t *testing.T) {
	loop, mgr := newFallingBlockLoop()

	// Solid stone directly below the sand: the sand is supported.
	src := pk.Position{X: 3, Y: 70, Z: 3}
	belowPos := pk.Position{X: 3, Y: 69, Z: 3}
	mgr.SetBlock(belowPos, stoneStateID(), dimMinY)
	mgr.SetBlock(src, sandStateID(), dimMinY)

	loop.scheduleFallingBlockTick(src, sandStateID())
	loop.gametime += fallingBlockDelayAfterPlace
	loop.tickScheduledBlocks()

	// No entity spawned.
	if n, _ := countFalling(loop); n != 0 {
		t.Fatalf("supported sand spawned %d FallingBlockEntity, want 0", n)
	}
	// The source block is still sand (untouched).
	if got, ok := mgr.GetBlock(src, dimMinY); !ok || got != sandStateID() {
		t.Fatalf("supported sand: source GetBlock = (%v, ok=%v), want sand %d (unchanged)", got, ok, sandStateID())
	}
}
