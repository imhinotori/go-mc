package server

import (
	"testing"

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
