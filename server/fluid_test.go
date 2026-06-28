package server

import (
	"bytes"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// fluid_test.go covers GAMEPLAY-05 Task 2 (the FlowingFluid algorithm) and Task 3 (player
// fluid physics). The algorithm is ported from temp/cache/26.2-inner.jar
// (net.minecraft.world.level.material.FlowingFluid: getNewLiquid, spread, spreadToSides,
// getSlopeDistance) — see the citations in fluid.go. The tests are PORT-EXACT behavior gates
// (level decrement, source conversion, termination, determinism), not feel checks.

// newFluidLoop wires a TickLoop with a tick-owned ChunkManager holding one ready, all-air
// chunk at column {0,0}, so SetBlock/GetBlock have a loaded column. Mirrors newBlockLoop.
func newFluidLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	return loop, mgr
}

// setWater writes water at pos with the given legacy level (0=source).
func setWater(mgr *world.ChunkManager, pos pk.Position, level int) {
	mgr.SetBlock(pos, waterStateID(level), dimMinY)
}

// setSolid writes a stone block at pos (a barrier the fluid cannot pass).
func setSolid(mgr *world.ChunkManager, pos pk.Position) {
	mgr.SetBlock(pos, block.ToStateID[block.Stone{}], dimMinY)
}

// levelAt reads the legacy water level at pos; isWater=false for non-water.
func levelAt(mgr *world.ChunkManager, pos pk.Position) (int, bool) {
	id, ok := mgr.GetBlock(pos, dimMinY)
	if !ok {
		return 0, false
	}
	return waterLevelOf(id)
}

// drainAll runs the fluid pass repeatedly, advancing gametime, until the schedule queue is
// empty or a safety cap is hit (proving termination — a non-terminating spread would hit the
// cap). Returns the number of passes run.
func drainAll(loop *TickLoop) int {
	const cap = 2000
	for i := 0; i < cap; i++ {
		if loop.fluidSchedule == nil || loop.scheduleEmpty() {
			return i
		}
		loop.tickFluids()
		loop.gametime++
	}
	return cap
}

// TestPostProcessChunkFluidsExpandsNaturalWater locks BUG-2: a freshly-generated chunk whose
// aquifer flagged unstable fluid borders (Chunk.PostProcessFluids) must, on going live via
// chunkReady.applyTo, run the one-shot FluidState.tick per flagged cell (LevelChunk.
// postProcessGeneration) — and that tick must SPREAD the water into the bordering air. This is
// "naturally-generated water expands on load" end-to-end, with NO manual block update.
func TestPostProcessChunkFluidsExpandsNaturalWater(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull

	// Build a one-cell water source on a flat stone floor, with bordering air the water should
	// flow into on load. Source at (4,64,4); a 3x3 stone floor at y=63 under the source and all
	// four horizontal neighbors, so the spread is sideways (a flat shelf, the ravine-floor case).
	src := pk.Position{X: 4, Y: 64, Z: 4}
	ch.Sections[(64+64)>>4].SetBlock(localIndex(src), waterStateID(0))
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			fl := pk.Position{X: 4 + dx, Y: 63, Z: 4 + dz}
			ch.Sections[(63+64)>>4].SetBlock(localIndex(fl), block.ToStateID[block.Stone{}])
		}
	}
	neighbors := []pk.Position{
		{X: 5, Y: 64, Z: 4}, {X: 3, Y: 64, Z: 4},
		{X: 4, Y: 64, Z: 5}, {X: 4, Y: 64, Z: 3},
	}

	// Flag the source as an aquifer post-process border cell (localY = worldY - minY = 64+64).
	localY := src.Y - dimMinY
	ch.PostProcessFluids = []uint32{uint32(localY)<<8 | uint32(src.Z&15)<<4 | uint32(src.X & 15)}

	// All four horizontal neighbors start AIR.
	for _, n := range neighbors {
		if _, isW := levelAt(mgr, n); isW {
			t.Fatalf("precondition: %v should be air before integration", n)
		}
	}

	// Integrate exactly like the worker rejoin: this calls postProcessChunkFluids.
	chunkReady{res: world.ChunkResult{Pos: level.ChunkPos{0, 0}, Chunk: ch}}.applyTo(loop)

	// The one-shot kick scheduled onward flow; drain the queue to the fixed point.
	drainAll(loop)

	// Water must have spread sideways onto the shelf (natural-water expansion on load, NO manual
	// block update). On a flat floor with no drop-off, getSpread keeps all four equal directions.
	spread := 0
	for _, n := range neighbors {
		if _, isW := levelAt(mgr, n); isW {
			spread++
		}
	}
	if spread == 0 {
		t.Fatal("natural water did NOT expand on load (postProcessGeneration kick failed); all neighbors still air")
	}
	t.Logf("water expanded to %d/4 horizontal neighbors on load", spread)
}

// localIndex maps a world position to the y-major in-section local index used by Section.SetBlock.
func localIndex(p pk.Position) int {
	return (p.Y&15)<<8 | (p.Z&15)<<4 | (p.X & 15)
}

// TestGetNewLiquid pins the jar-exact getNewLiquid level arithmetic (FlowingFluid.getNewLiquid):
// the highest reaching neighbor level minus dropOff, the >=2-source-neighbor source conversion,
// the fluid-above falling rule, and the no-neighbor empty result.
func TestGetNewLiquid(t *testing.T) {
	t.Run("single source neighbor decrements by dropOff", func(t *testing.T) {
		loop, mgr := newFluidLoop()
		center := pk.Position{X: 4, Y: 64, Z: 4}
		setWater(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 0) // a source to the east
		f := loop.getNewLiquid(center)
		if !f.isWater || f.source {
			t.Fatalf("expected flowing water, got %+v", f)
		}
		// source amount 8, minus dropOff 1 -> amount 7 -> legacy level 1.
		if getLegacyLevel(f.amount, f.falling, f.source) != 1 {
			t.Fatalf("expected legacy level 1 next to a source, got amount=%d falling=%v", f.amount, f.falling)
		}
	})

	t.Run("two source neighbors convert to a source", func(t *testing.T) {
		loop, mgr := newFluidLoop()
		center := pk.Position{X: 4, Y: 64, Z: 4}
		setSolid(mgr, pk.Position{X: 4, Y: 63, Z: 4}) // solid floor below: source-conversion needs a solid/source below
		setWater(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 0)
		setWater(mgr, pk.Position{X: 3, Y: 64, Z: 4}, 0)
		f := loop.getNewLiquid(center)
		if !f.isWater || !f.source {
			t.Fatalf("expected source (>=2 source neighbors, conversion on, solid below), got %+v", f)
		}
	})

	t.Run("fluid directly above -> falling full", func(t *testing.T) {
		loop, mgr := newFluidLoop()
		center := pk.Position{X: 4, Y: 64, Z: 4}
		setWater(mgr, pk.Position{X: 4, Y: 65, Z: 4}, 0) // source above
		f := loop.getNewLiquid(center)
		if !f.isWater || !f.falling {
			t.Fatalf("expected falling water under a fluid, got %+v", f)
		}
		if getLegacyLevel(f.amount, f.falling, f.source) != 8 {
			t.Fatalf("falling water should be legacy level 8, got amount=%d", f.amount)
		}
	})

	t.Run("no fluid neighbors -> empty", func(t *testing.T) {
		loop, _ := newFluidLoop()
		f := loop.getNewLiquid(pk.Position{X: 4, Y: 64, Z: 4})
		if f.isWater {
			t.Fatalf("expected empty (no fluid neighbors), got %+v", f)
		}
	})
}

// buildBasin makes a walled, flat-floored basin centered on (cx, y, cz): a solid floor at y-1
// across a (2r+1) square, and a solid wall at y around the perimeter, so water settles in
// uniform level-decrement rings with NO drop-off slope bias (the pure decrement gate).
func buildBasin(mgr *world.ChunkManager, cx, y, cz, r int) {
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			setSolid(mgr, pk.Position{X: cx + dx, Y: y - 1, Z: cz + dz}) // floor
			if dx == -r || dx == r || dz == -r || dz == r {
				setSolid(mgr, pk.Position{X: cx + dx, Y: y, Z: cz + dz}) // perimeter wall
			}
		}
	}
}

// TestWaterSettles: a source in a walled flat basin spreads outward, the level decrements by 1
// per ring, and the queue TERMINATES (no infinite spread — the dropOff bottoms out at 7).
func TestWaterSettles(t *testing.T) {
	loop, mgr := newFluidLoop()

	// Centered well inside the loaded column [0,15]^2 with the walled basin entirely in-bounds,
	// so the chunk boundary (an unloaded => "hole" edge) never biases the slope-find.
	const cx, y, cz, r = 8, 64, 8, 5
	buildBasin(mgr, cx, y, cz, r)

	src := pk.Position{X: cx, Y: y, Z: cz}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)

	passes := drainAll(loop)
	if passes >= 2000 {
		t.Fatalf("fluid spread did not terminate within 2000 passes (infinite spread)")
	}

	// The source is intact.
	if lv, ok := levelAt(mgr, src); !ok || lv != 0 {
		t.Fatalf("source cell = (%d, ok=%v), want 0", lv, ok)
	}

	// One ring out (cardinal neighbor) is level 1 (8 - dropOff -> amount 7 -> legacy 1).
	for _, p := range []pk.Position{
		{X: cx + 1, Y: y, Z: cz}, {X: cx - 1, Y: y, Z: cz},
		{X: cx, Y: y, Z: cz + 1}, {X: cx, Y: y, Z: cz - 1},
	} {
		if lv, ok := levelAt(mgr, p); !ok || lv != 1 {
			t.Fatalf("ring-1 cell %v = (%d, ok=%v), want 1", p, lv, ok)
		}
	}

	// Two rings out (straight cardinal line) is level 2.
	if lv, ok := levelAt(mgr, pk.Position{X: cx + 2, Y: y, Z: cz}); !ok || lv != 2 {
		t.Fatalf("ring-2 cell east = (%d, ok=%v), want 2", lv, ok)
	}

	// Determinism: a second identical run produces the same shape at sampled cells.
	loop2, mgr2 := newFluidLoop()
	buildBasin(mgr2, cx, y, cz, r)
	setWater(mgr2, src, 0)
	loop2.scheduleFluidTick(src)
	drainAll(loop2)
	for _, probe := range []pk.Position{
		{X: cx + 1, Y: y, Z: cz}, {X: cx + 2, Y: y, Z: cz}, {X: cx, Y: y, Z: cz + 2}, {X: cx + 3, Y: y, Z: cz},
	} {
		a, _ := levelAt(mgr, probe)
		b, _ := levelAt(mgr2, probe)
		if a != b {
			t.Fatalf("non-deterministic settle at %v: run1=%d run2=%d", probe, a, b)
		}
	}
}

// TestFlowDownColumn: a source over an open air column falls straight down (falling water) and
// does not spread sideways while it can still fall (vanilla: down-first in spread).
func TestFlowDownColumn(t *testing.T) {
	loop, mgr := newFluidLoop()

	src := pk.Position{X: 4, Y: 70, Z: 4}
	// Solid floor far below so the column eventually lands.
	setSolid(mgr, pk.Position{X: 4, Y: 64, Z: 4})
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)

	if drainAll(loop) >= 2000 {
		t.Fatalf("falling column did not terminate")
	}

	// The cell directly below the source is falling water (legacy level >= 8).
	below := pk.Position{X: 4, Y: 69, Z: 4}
	lv, ok := levelAt(mgr, below)
	if !ok {
		t.Fatalf("cell below source is not water")
	}
	if lv < 8 {
		t.Fatalf("cell below source = legacy %d, want falling (>=8)", lv)
	}

	// The source did NOT spread sideways into the open air at its own level (no floor under it,
	// so spread goes down, not to the sides).
	if _, ok := levelAt(mgr, pk.Position{X: 5, Y: 70, Z: 4}); ok {
		t.Fatalf("source spread sideways while it could still fall (should flow down first)")
	}
}

// --- Task 3: player fluid physics (EntityFluidInteraction port) ---

// fluidTestPlayer makes a tickPlayer at (x,y,z) with no client (physics is position-only).
func fluidTestPlayer(x, y, z float64) *tickPlayer {
	return &tickPlayer{x: x, y: y, z: z}
}

// TestPlayerMovePathDoesNotRewritePositionInWater is the BUG-1 regression: the player movement
// accept path must accept the client's submitted (collide-clamped) position VERBATIM and must NOT
// re-apply water drag/buoyancy. In vanilla ServerGamePacketListenerImpl.handleMovePlayer the
// server resolves collisions via ServerPlayer.move and then absSnapTo(x,y,z) — accepting the
// client position. Re-applying the 0.8/0.014 water physics server-side produced a position the
// client never predicted and disconnected it (jump-into-water quit). This test drives a confirmed
// player INTO a water column via applyInput and asserts the accepted position equals the submitted
// position (no 0.8 horizontal scale, no +0.014 buoyancy).
func TestPlayerMovePathDoesNotRewritePositionInWater(t *testing.T) {
	loop, mgr := newFluidLoop()
	// Water at the player's CURRENT feet cell, so playerInWater(p) — which the OLD
	// moveWithFluidPhysics gated on using the player's pre-move position — is TRUE. The OLD code
	// would therefore re-apply the 0.8 horizontal scale + 0.014 buoyancy to the submitted delta.
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	p := blockPlayer(loop, 8.5, 64.0, 8.5)

	// Submitted move: jump UP and slightly east (a jump-out-of-water arc). The destination box
	// (feet at y=65.5) does not overlap the water cell at y=64, so collidePlayer accepts it
	// verbatim — the accepted position must equal the submission exactly, proving no water rewrite
	// (OLD code would have scaled x by 0.8 and added +0.014 to y).
	const subX, subY, subZ = 8.7, 65.5, 8.5
	var b bytes.Buffer
	_, _ = pk.Double(subX).WriteTo(&b)
	_, _ = pk.Double(subY).WriteTo(&b)
	_, _ = pk.Double(subZ).WriteTo(&b)
	_, _ = pk.UnsignedByte(0).WriteTo(&b) // flags: not on ground
	in := SubtickInput{
		At:     time.Unix(0, 0),
		Packet: pk.Packet{ID: int32(packetid.ServerboundMovePlayerPos), Data: b.Bytes()},
	}

	loop.applyInput(p, in)

	if !floatNear(p.x, subX, 1e-9) {
		t.Fatalf("accepted x = %v, want %v (submitted verbatim — no 0.8 water scale)", p.x, subX)
	}
	if !floatNear(p.y, subY, 1e-9) {
		t.Fatalf("accepted y = %v, want %v (submitted verbatim — no +0.014 buoyancy)", p.y, subY)
	}
	if !floatNear(p.z, subZ, 1e-9) {
		t.Fatalf("accepted z = %v, want %v (submitted verbatim)", p.z, subZ)
	}
}

// TestInWaterDetection: a player whose AABB overlaps a water block is in water; a dry player is
// not.
func TestInWaterDetection(t *testing.T) {
	loop, mgr := newFluidLoop()

	// Water at the player's feet position.
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	wet := fluidTestPlayer(8.5, 64.0, 8.5) // feet at y=64, inside the water block
	if !loop.playerInWater(wet) {
		t.Fatalf("player standing in a water block should be in water")
	}

	dry := fluidTestPlayer(2.5, 64.0, 2.5) // far from any water
	if loop.playerInWater(dry) {
		t.Fatalf("player on dry land should not be in water")
	}
}

// TestWaterSlowdown: a horizontal movement delta applied through the in-water path is scaled by
// getWaterSlowDown (0.8); a dry player's delta is unchanged.
func TestWaterSlowdown(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	wet := fluidTestPlayer(8.5, 64.0, 8.5)
	dx, dy, dz := loop.applyFluidPhysics(wet, 1.0, -0.5, 0.0)
	if !floatNear(dx, 0.8, 1e-9) {
		t.Fatalf("in-water horizontal dx = %v, want 0.8 (getWaterSlowDown)", dx)
	}
	if !floatNear(dz, 0.0, 1e-9) {
		t.Fatalf("in-water horizontal dz = %v, want 0.0", dz)
	}
	_ = dy

	dry := fluidTestPlayer(2.5, 64.0, 2.5)
	ddx, _, ddz := loop.applyFluidPhysics(dry, 1.0, -0.5, 0.3)
	if !floatNear(ddx, 1.0, 1e-9) || !floatNear(ddz, 0.3, 1e-9) {
		t.Fatalf("dry player horizontal delta changed: dx=%v dz=%v, want 1.0/0.3", ddx, ddz)
	}
}

// TestBuoyancy: a submerged player's downward movement is reduced (buoyant push) vs a dry
// player whose vertical delta is unchanged.
func TestBuoyancy(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	wet := fluidTestPlayer(8.5, 64.0, 8.5)
	_, wetDy, _ := loop.applyFluidPhysics(wet, 0.0, -0.5, 0.0)
	// Buoyancy adds an upward (positive) component, so the net downward delta is LESS negative.
	if !(wetDy > -0.5) {
		t.Fatalf("submerged player dy = %v, want > -0.5 (buoyant reduction)", wetDy)
	}

	dry := fluidTestPlayer(2.5, 64.0, 2.5)
	_, dryDy, _ := loop.applyFluidPhysics(dry, 0.0, -0.5, 0.0)
	if !floatNear(dryDy, -0.5, 1e-9) {
		t.Fatalf("dry player dy = %v, want -0.5 (unchanged)", dryDy)
	}
}

// floatNear reports |a-b| <= eps.
func floatNear(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

// TestPostProcessChunkFluids gates the cave-gap fix: a generated chunk carries a
// PostProcessFluids mark on an aquifer-border water source that sits beside an air gap; when
// the chunk goes live, postProcessChunkFluids runs FluidState.tick ONCE on the marked cell,
// which must kick the water into flowing toward the gap (the normal queue carries it onward).
// This is the one-shot LevelChunk.postProcessGeneration behavior — NOT a recurring scan.
func TestPostProcessChunkFluids(t *testing.T) {
	loop, mgr := newFluidLoop()

	// A water SOURCE at (4,64,4) boxed on a solid floor with walls on three sides, leaving the
	// EAST cell (5,64,4) as the only open neighbor — an air gap over a solid floor. This is the
	// cave-water-bordering-air-gap shape: the source must spread sideways into that one gap.
	src := pk.Position{X: 4, Y: 64, Z: 4}
	setSolid(mgr, pk.Position{X: 4, Y: 63, Z: 4}) // floor under the source
	setSolid(mgr, pk.Position{X: 5, Y: 63, Z: 4}) // floor under the gap (so flow stays, spreads sideways)
	setSolid(mgr, pk.Position{X: 3, Y: 64, Z: 4}) // wall west
	setSolid(mgr, pk.Position{X: 4, Y: 64, Z: 3}) // wall north
	setSolid(mgr, pk.Position{X: 4, Y: 64, Z: 5}) // wall south
	setWater(mgr, src, 0)                         // the marked border source

	// The gap is air pre-tick (no water flowed in yet — the inert-load symptom).
	if _, isW := levelAt(mgr, pk.Position{X: 5, Y: 64, Z: 4}); isW {
		t.Fatal("precondition: the gap must be air before post-processing")
	}

	// Build a chunk that marks the source cell for post-processing (local pack: y-(-64)=128).
	ch := level.EmptyChunk(blockTestSecs)
	localY := uint32(src.Y - dimMinY)
	ch.PostProcessFluids = []uint32{localY<<8 | uint32(src.Z&15)<<4 | uint32(src.X&15)}

	loop.postProcessChunkFluids(level.ChunkPos{0, 0}, ch)

	// The one-shot tick must have scheduled flow; draining the queue must fill the gap.
	if loop.scheduleEmpty() {
		t.Fatal("postProcessChunkFluids must schedule onward flow from the marked border cell")
	}
	drainAll(loop)
	if _, isW := levelAt(mgr, pk.Position{X: 5, Y: 64, Z: 4}); !isW {
		t.Fatal("water must flow into the bordering air gap after post-processing")
	}
}

// TestPostProcessChunkFluidsNoMarksIsNoop proves the fix is bounded: a chunk with NO marks (the
// common case — most generated chunks have no unstable aquifer border) schedules nothing, so
// there is no cascade and no wasted work on chunk load.
func TestPostProcessChunkFluidsNoMarksIsNoop(t *testing.T) {
	loop, _ := newFluidLoop()
	ch := level.EmptyChunk(blockTestSecs) // no PostProcessFluids
	loop.postProcessChunkFluids(level.ChunkPos{0, 0}, ch)
	if !loop.scheduleEmpty() {
		t.Fatal("a chunk with no marks must schedule no fluid ticks")
	}
}
