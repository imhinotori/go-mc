package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// fluid_lava_test.go covers the lava FLOW simulation (D-F1): lava reuses the shared FlowingFluid
// geometry parameterized by the LavaFluid overridden constants -- getDropOff=2, getTickDelay=30,
// getSlopeFindDistance=2 (overworld/non-fast), canConvertToSource=false -- all javap-verified from
// temp/cache/26.2-inner.jar (net.minecraft.world.level.material.LavaFluid). These tests pin the
// DIFFERENCES from water: shorter 3-block reach, the 30-tick delay, no two-source conversion, and
// the lava-onto-water -> stone interaction (LavaFluid.spreadTo DOWN override).

// lavaLevelAt reads the legacy lava level at pos; isLava=false for non-lava.
func lavaLevelAt(mgr *world.ChunkManager, pos pk.Position) (int, bool) {
	id, ok := mgr.GetBlock(pos, dimMinY)
	if !ok {
		return 0, false
	}
	return lavaLevelOf(id)
}

// TestLavaConstants pins the four javap-verified LavaFluid overrides via the fluidState accessors.
func TestLavaConstants(t *testing.T) {
	lava := fluidState{isLava: true, amount: waterSourceAmount}
	if lava.dropOff() != 2 {
		t.Fatalf("lava getDropOff = %d, want 2 (LavaFluid overworld)", lava.dropOff())
	}
	if lava.tickDelay() != 30 {
		t.Fatalf("lava getTickDelay = %d, want 30 (LavaFluid overworld)", lava.tickDelay())
	}
	if lava.slopeFindDistance() != 2 {
		t.Fatalf("lava getSlopeFindDistance = %d, want 2 (LavaFluid overworld)", lava.slopeFindDistance())
	}
	if lava.sourceConversion() {
		t.Fatalf("lava canConvertToSource = true, want false (LavaFluid gamerule default)")
	}
	water := fluidState{isWater: true, amount: waterSourceAmount}
	if water.dropOff() != 1 || water.tickDelay() != 5 || water.slopeFindDistance() != 4 || !water.sourceConversion() {
		t.Fatalf("water constants perturbed: dropOff=%d tickDelay=%d slope=%d conv=%v",
			water.dropOff(), water.tickDelay(), water.slopeFindDistance(), water.sourceConversion())
	}
}

// TestLavaGetNewLiquidDropOff2: a cell next to a lava source computes amount = 8 - 2 = 6 -> legacy
// level 2 (vs water's level 1). This is the getDropOff=2 arithmetic.
func TestLavaGetNewLiquidDropOff2(t *testing.T) {
	loop, mgr := newFluidLoop()
	center := pk.Position{X: 4, Y: 64, Z: 4}
	setLava(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 0)
	f := loop.getNewLiquid(center)
	if !f.isLava || f.source {
		t.Fatalf("expected flowing lava, got %+v", f)
	}
	if getLegacyLevel(f.amount, f.falling, f.source) != 2 {
		t.Fatalf("lava next to source = legacy %d, want 2 (8-dropOff2)", getLegacyLevel(f.amount, f.falling, f.source))
	}
}

// TestLavaSpreads3BlocksNot7: a lava source on a flat walled basin reaches only 3 cells sideways
// (levels 2,4,6 -> 8->6->4->2->0), NOT water's 7. The 4th ring must be dry.
func TestLavaSpreads3BlocksNot7(t *testing.T) {
	loop, mgr := newFluidLoop()
	const cx, y, cz, r = 8, 64, 8, 6
	buildBasin(mgr, cx, y, cz, r)

	src := pk.Position{X: cx, Y: y, Z: cz}
	setLava(mgr, src, 0)
	loop.scheduleFluidTick(src)

	if drainAll(loop) >= 2000 {
		t.Fatalf("lava spread did not terminate")
	}

	if lv, ok := lavaLevelAt(mgr, src); !ok || lv != 0 {
		t.Fatalf("lava source = (%d, ok=%v), want 0", lv, ok)
	}
	east := func(n int) pk.Position { return pk.Position{X: cx + n, Y: y, Z: cz} }
	for n, want := range map[int]int{1: 2, 2: 4, 3: 6} {
		if lv, ok := lavaLevelAt(mgr, east(n)); !ok || lv != want {
			t.Fatalf("lava ring-%d east = (%d, ok=%v), want legacy %d", n, lv, ok, want)
		}
	}
	if _, ok := lavaLevelAt(mgr, east(4)); ok {
		t.Fatalf("lava reached ring-4 (should stop at 3 blocks with dropOff 2)")
	}
}

// TestLavaTickDelay30: spreadTo schedules the target on the LAVA delay (30), not water's 5.
func TestLavaTickDelay30(t *testing.T) {
	loop, mgr := newFluidLoop()
	target := pk.Position{X: 5, Y: 64, Z: 4}
	setSolid(mgr, pk.Position{X: 5, Y: 63, Z: 4})
	loop.gametime = 100
	loop.spreadToDir(target, dirDown, fluidState{isLava: true, amount: 6})
	q := loop.only().fluidSchedule
	if q == nil || q.empty() {
		t.Fatalf("spreadTo did not schedule a lava tick")
	}
	due := q.drainDue(100 + 30)
	found := false
	for _, st := range due {
		if st.pos == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("lava spreadTo did not schedule target at gametime+30 (LavaFluid.getTickDelay)")
	}
	loop2, mgr2 := newFluidLoop()
	setSolid(mgr2, pk.Position{X: 5, Y: 63, Z: 4})
	loop2.gametime = 100
	loop2.spreadToDir(target, dirDown, fluidState{isLava: true, amount: 6})
	early := loop2.only().fluidSchedule.drainDue(100 + 5)
	for _, st := range early {
		if st.pos == target {
			t.Fatalf("lava was due at +5 (water delay); must wait 30 ticks")
		}
	}
}

// TestLavaDoesNotConvertToSource: two lava sources with a solid floor do NOT make the middle cell a
// source (canConvertToSource=false). Water in the same shape WOULD convert.
func TestLavaDoesNotConvertToSource(t *testing.T) {
	loop, mgr := newFluidLoop()
	center := pk.Position{X: 4, Y: 64, Z: 4}
	setSolid(mgr, pk.Position{X: 4, Y: 63, Z: 4})
	setLava(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 0)
	setLava(mgr, pk.Position{X: 3, Y: 64, Z: 4}, 0)
	f := loop.getNewLiquid(center)
	if f.source {
		t.Fatalf("lava with 2 source neighbors became a source; LavaFluid.canConvertToSource is false")
	}
	if !f.isLava {
		t.Fatalf("expected flowing lava, got %+v", f)
	}
	loopW, mgrW := newFluidLoop()
	setSolid(mgrW, pk.Position{X: 4, Y: 63, Z: 4})
	setWater(mgrW, pk.Position{X: 5, Y: 64, Z: 4}, 0)
	setWater(mgrW, pk.Position{X: 3, Y: 64, Z: 4}, 0)
	if fw := loopW.getNewLiquid(center); !fw.source {
		t.Fatalf("water control did NOT convert to source; test shape is wrong")
	}
}

// TestLavaOntoWaterMakesStone: lava spreading DOWN onto a water cell replaces it with stone and
// fizzes (LavaFluid.spreadTo DOWN override).
func TestLavaOntoWaterMakesStone(t *testing.T) {
	loop, mgr := newFluidLoop()
	pos := pk.Position{X: 4, Y: 64, Z: 4}
	setWater(mgr, pos, 0)
	loop.spreadToDir(pos, dirDown, fluidState{isLava: true, falling: true, amount: waterSourceAmount})

	if _, isW := levelAt(mgr, pos); isW {
		t.Fatalf("water was not consumed by lava-onto-water")
	}
	if _, isL := lavaLevelAt(mgr, pos); isL {
		t.Fatalf("lava wrote itself instead of stone onto water")
	}
	id, ok := mgr.GetBlock(pos, dimMinY)
	if !ok || id != block.ToStateID[block.Stone{}] {
		t.Fatalf("lava-onto-water cell = %v (ok=%v), want STONE", id, ok)
	}
}

// TestLavaFallsDownColumn: a lava source over an open column falls straight down as FALLING lava
// (spread's down-first branch), mirroring water's TestFlowDownColumn for the lava kind. The column
// sits inside a walled, floored basin so the landing cell's sideways spread is contained and the
// queue reaches a fixed point (an isolated 1-block pillar floor would leave the falling foot
// perpetually spilling off its edges into open air -- a test-geometry artifact, not a sim bug).
func TestLavaFallsDownColumn(t *testing.T) {
	loop, mgr := newFluidLoop()
	const cx, cz = 8, 8
	// A basin floor+walls at y=64 so the landed lava settles instead of spilling off a pillar.
	buildBasin(mgr, cx, 65, cz, 3)
	src := pk.Position{X: cx, Y: 70, Z: cz}
	setLava(mgr, src, 0)
	loop.scheduleFluidTick(src)
	if drainAll(loop) >= 2000 {
		t.Fatalf("falling lava column did not terminate")
	}
	// The cell directly below the source is FALLING lava (legacy >= 8), proving down-first spread.
	below := pk.Position{X: cx, Y: 69, Z: cz}
	lv, ok := lavaLevelAt(mgr, below)
	if !ok {
		t.Fatalf("cell below lava source is not lava")
	}
	if lv < 8 {
		t.Fatalf("cell below lava source = legacy %d, want falling (>=8)", lv)
	}
	// The source did NOT spill sideways while it could still fall (vanilla down-first).
	if _, ok := lavaLevelAt(mgr, pk.Position{X: cx + 1, Y: 70, Z: cz}); ok {
		t.Fatalf("lava source spread sideways while it could still fall (should flow down first)")
	}
}


// --- D-F2: horizontal lava+water solidification (LiquidBlock.shouldSpreadLiquid) ---

// TestLavaSourceBesideWaterMakesObsidian: a SOURCE lava cell horizontally adjacent to water
// solidifies IN PLACE to OBSIDIAN and fires the fizzle levelEvent (1501), instead of spreading.
// Port gate for LiquidBlock.shouldSpreadLiquid (isSource -> Blocks.OBSIDIAN).
func TestLavaSourceBesideWaterMakesObsidian(t *testing.T) {
	loop, mgr := newFluidLoop()
	lavaPos := pk.Position{X: 4, Y: 64, Z: 4}
	setLava(mgr, lavaPos, 0)                                   // SOURCE lava (legacy 0)
	setWater(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 0)           // water to the EAST (horizontal)
	var fizzes []pk.Position
	var fizzEvent int
	loop.fizzHook = func(pos pk.Position, event int) { fizzes = append(fizzes, pos); fizzEvent = event }

	loop.fluidTick(lavaPos)

	if _, isL := lavaLevelAt(mgr, lavaPos); isL {
		t.Fatalf("source lava beside water was not solidified (still lava)")
	}
	id, ok := mgr.GetBlock(lavaPos, dimMinY)
	if !ok || id != block.ToStateID[block.Obsidian{}] {
		t.Fatalf("source lava beside water = %v (ok=%v), want OBSIDIAN %v", id, ok, block.ToStateID[block.Obsidian{}])
	}
	if len(fizzes) != 1 || fizzes[0] != lavaPos {
		t.Fatalf("fizz not fired at the lava cell: %+v", fizzes)
	}
	if fizzEvent != 1501 {
		t.Fatalf("fizz levelEvent = %d, want 1501 (LiquidBlock.fizz)", fizzEvent)
	}
}

// TestFlowingLavaBesideWaterMakesCobblestone: a FLOWING lava cell horizontally adjacent to water
// solidifies IN PLACE to COBBLESTONE and fizzes (LiquidBlock.shouldSpreadLiquid, non-source ->
// Blocks.COBBLESTONE). The flowing lava is placed via a source neighbour so getNewLiquid keeps it
// flowing (level 2), then the shouldSpreadLiquid gate solidifies it against the water.
func TestFlowingLavaBesideWaterMakesCobblestone(t *testing.T) {
	loop, mgr := newFluidLoop()
	lavaPos := pk.Position{X: 4, Y: 64, Z: 4}
	setLava(mgr, lavaPos, 2)                                   // FLOWING lava (legacy 2 -> amount 6)
	setLava(mgr, pk.Position{X: 3, Y: 64, Z: 4}, 0)           // a lava SOURCE to the WEST feeds it
	setWater(mgr, pk.Position{X: 4, Y: 64, Z: 5}, 0)          // water to the SOUTH (horizontal)
	var fizzed bool
	loop.fizzHook = func(pos pk.Position, event int) { fizzed = fizzed || (pos == lavaPos && event == 1501) }

	loop.fluidTick(lavaPos)

	if _, isL := lavaLevelAt(mgr, lavaPos); isL {
		t.Fatalf("flowing lava beside water was not solidified (still lava)")
	}
	id, ok := mgr.GetBlock(lavaPos, dimMinY)
	if !ok || id != block.ToStateID[block.Cobblestone{}] {
		t.Fatalf("flowing lava beside water = %v (ok=%v), want COBBLESTONE %v", id, ok, block.ToStateID[block.Cobblestone{}])
	}
	if !fizzed {
		t.Fatalf("flowing lava solidification did not fire the 1501 fizzle at the lava cell")
	}
}

// TestLavaBesideWaterAboveMakesObsidian: the neighbour set includes UP (DOWN.getOpposite()), not
// below — a source lava with water directly ABOVE solidifies to obsidian.
func TestLavaBesideWaterAboveMakesObsidian(t *testing.T) {
	loop, mgr := newFluidLoop()
	lavaPos := pk.Position{X: 4, Y: 64, Z: 4}
	setLava(mgr, lavaPos, 0)
	setWater(mgr, pk.Position{X: 4, Y: 65, Z: 4}, 0) // water directly above
	loop.fluidTick(lavaPos)
	id, ok := mgr.GetBlock(lavaPos, dimMinY)
	if !ok || id != block.ToStateID[block.Obsidian{}] {
		t.Fatalf("lava with water above = %v, want OBSIDIAN (UP is in the neighbour set)", id)
	}
}

// TestLavaBesideWaterBelowDoesNotSolidify: below is NOT in shouldSpreadLiquid's neighbour set, so a
// lava cell with water ONLY below is not solidified by the HORIZONTAL rule (the vertical case is
// LavaFluid.spreadTo, exercised separately). The lava should stay lava and flow normally.
func TestLavaBesideWaterBelowDoesNotSolidify(t *testing.T) {
	loop, mgr := newFluidLoop()
	lavaPos := pk.Position{X: 4, Y: 64, Z: 4}
	setLava(mgr, lavaPos, 0)
	setWater(mgr, pk.Position{X: 4, Y: 63, Z: 4}, 0) // water directly below (not a shouldSpreadLiquid neighbour)
	if loop.shouldSpreadLiquid(lavaPos, fluidState{isLava: true, source: true, amount: waterSourceAmount}) == false {
		t.Fatalf("water only-below wrongly triggered horizontal solidification (below is not a neighbour)")
	}
	if _, isL := lavaLevelAt(mgr, lavaPos); !isL {
		t.Fatalf("lava was solidified by water below via the horizontal rule; it must not be")
	}
}

// TestWaterBesideLavaDoesNotSolidifyViaShouldSpread: shouldSpreadLiquid is a no-op for water (only
// lava solidifies). A water cell next to lava returns true and is untouched by this gate.
func TestWaterBesideLavaDoesNotSolidifyViaShouldSpread(t *testing.T) {
	loop, mgr := newFluidLoop()
	waterPos := pk.Position{X: 4, Y: 64, Z: 4}
	setWater(mgr, waterPos, 0)
	setLava(mgr, pk.Position{X: 5, Y: 64, Z: 4}, 0)
	if !loop.shouldSpreadLiquid(waterPos, fluidState{isWater: true, source: true, amount: waterSourceAmount}) {
		t.Fatalf("shouldSpreadLiquid returned false for water; it must be a no-op for non-lava")
	}
}
