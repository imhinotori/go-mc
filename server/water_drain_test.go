package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// water_drain_test.go pins the source-removal drain cascade: when a water SOURCE is broken, the
// FLOWING water that spread from it must re-evaluate and drain back to air. Ported 1:1 from
// net.minecraft.world.level.material.FlowingFluid.tick: a non-source cell recomputes getNewLiquid
// and writes the result with setBlock(pos, state, flag 3). Flag 3's UPDATE_NEIGHBORS bit runs
// Level.updateNeighborsAt -> LiquidBlock.neighborChanged -> scheduleTick on all 6 neighbours, so a
// diminishing cell wakes its downstream neighbours and the drain cascades outward to the fixed
// point. The break itself wires the initial wake through reconcileEdit -> scheduleFluidNeighborsOnEdit
// (Level.updateNeighborsAt for the edited cell). See fluid.go fluidTick / scheduleNeighbors.

// TestWaterDrainsWhenSourceRemoved: a source settles across a walled basin (source + rings of
// flowing water), the source is then removed exactly as a player break does (SetBlock->air +
// scheduleFluidNeighborsOnEdit), and after draining EVERY flowing cell in the basin returns to
// air. Before the fix, the diminish branch of fluidTick rescheduled only the cell itself (never
// its neighbours), so the outer rings froze at their stale level and the water was stranded.
func TestWaterDrainsWhenSourceRemoved(t *testing.T) {
	loop, mgr := newFluidLoop()

	const cx, y, cz, r = 8, 64, 8, 5
	buildBasin(mgr, cx, y, cz, r)

	src := pk.Position{X: cx, Y: y, Z: cz}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)

	// Precondition: the source spread to at least one ring of flowing water.
	settled := 0
	for dx := -r + 1; dx <= r-1; dx++ {
		for dz := -r + 1; dz <= r-1; dz++ {
			if _, isW := levelAt(mgr, pk.Position{X: cx + dx, Y: y, Z: cz + dz}); isW {
				settled++
			}
		}
	}
	if settled < 5 {
		t.Fatalf("precondition: expected the source to spread into flowing water, got %d water cells", settled)
	}

	// Remove the source exactly as destroyBlock -> reconcileEdit does: write air, then run the
	// edit-neighbour fluid kick (Level.updateNeighborsAt on the edited cell).
	mgr.SetBlock(src, block.ToStateID[block.Air{}], dimMinY)
	loop.scheduleFluidNeighborsOnEdit(src)

	if passes := drainAll(loop); passes >= 2000 {
		t.Fatalf("drain did not terminate within 2000 passes (infinite oscillation)")
	}

	// Every flowing cell in the basin interior must now be air.
	remaining := 0
	for dx := -r + 1; dx <= r-1; dx++ {
		for dz := -r + 1; dz <= r-1; dz++ {
			p := pk.Position{X: cx + dx, Y: y, Z: cz + dz}
			if lv, isW := levelAt(mgr, p); isW {
				remaining++
				t.Logf("stranded water at (%d,%d,%d) legacy=%d", p.X, p.Y, p.Z, lv)
			}
		}
	}
	if remaining != 0 {
		t.Fatalf("source removed but %d flowing water cells remain (must all drain to air)", remaining)
	}
}

// TestFallingColumnDrainsWhenSourceRemoved: a source over an open column falls straight down; when
// the source is removed the whole falling column must drain to air (the below-neighbour wake in
// scheduleNeighbors carries the drain down the column). Guards the vertical drain path.
func TestFallingColumnDrainsWhenSourceRemoved(t *testing.T) {
	loop, mgr := newFluidLoop()

	src := pk.Position{X: 4, Y: 70, Z: 4}
	setSolid(mgr, pk.Position{X: 4, Y: 64, Z: 4}) // floor the column lands on
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)

	// Precondition: the column below the source is falling water.
	if lv, ok := levelAt(mgr, pk.Position{X: 4, Y: 69, Z: 4}); !ok || lv < 8 {
		t.Fatalf("precondition: cell below source should be falling water, got (%d, ok=%v)", lv, ok)
	}

	mgr.SetBlock(src, block.ToStateID[block.Air{}], dimMinY)
	loop.scheduleFluidNeighborsOnEdit(src)
	drainAll(loop)

	for yy := 64; yy <= 70; yy++ {
		if lv, isW := levelAt(mgr, pk.Position{X: 4, Y: yy, Z: 4}); isW {
			t.Fatalf("falling column not drained: water at y=%d legacy=%d after source removal", yy, lv)
		}
	}
}
