package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// sugar_cane_test.go — SUB-BLOCKTICK end-to-end: the SugarCaneBlock scheduled-tick round-trip
// (schedule via updateShape -> drain at the target game-time via tickScheduledBlocks ->
// tickBlock -> sugarCaneTick -> canSurvive false -> destroyBlock + drop), plus canSurvive,
// growth, and the cascade. All against the ported vanilla logic.

// newSugarCaneLoop wires a TickLoop with one ready all-air chunk AND a registered block-tick
// container for column (0,0), so scheduleBlockTick has a container to route into.
func newSugarCaneLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

func sugarCane(age int) block.StateID {
	s, ok := block.SugarCaneState(age)
	if !ok {
		panic("no sugar cane state")
	}
	return s
}

func itemDropCount(loop *TickLoop) int {
	n := 0
	for _, e := range loop.entities.byID {
		if e.isItem {
			n++
		}
	}
	return n
}

// TestSugarCaneCanSurviveOnSandWithWater: cane on sand with adjacent water (to the cell below)
// survives; remove the water and it does not. CITE: SugarCaneBlock.canSurvive.
func TestSugarCaneCanSurviveOnSandWithWater(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	base := pk.Position{X: 4, Y: 64, Z: 4} // sand
	cane := pk.Position{X: 4, Y: 65, Z: 4} // cane on top of sand
	mgr.SetBlock(base, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(cane, sugarCane(0), dimMinY)

	// No water yet: cannot survive.
	if loop.sugarCaneCanSurvive(cane) {
		t.Fatal("cane on dry sand should NOT survive")
	}

	// Water adjacent to the BASE (the cell below the cane).
	mgr.SetBlock(pk.Position{X: 5, Y: 64, Z: 4}, block.ToStateID[block.Water{}], dimMinY)
	if !loop.sugarCaneCanSurvive(cane) {
		t.Fatal("cane on sand with adjacent water should survive")
	}
}

// TestSugarCaneOnSugarCaneSurvives: cane stacked on cane survives regardless of water. CITE:
// SugarCaneBlock.canSurvive (below.is(this) -> true).
func TestSugarCaneOnSugarCaneSurvives(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	lower := pk.Position{X: 4, Y: 65, Z: 4}
	upper := pk.Position{X: 4, Y: 66, Z: 4}
	mgr.SetBlock(lower, sugarCane(0), dimMinY)
	mgr.SetBlock(upper, sugarCane(0), dimMinY)
	if !loop.sugarCaneCanSurvive(upper) {
		t.Fatal("cane on cane should survive (no water needed)")
	}
}

// TestSugarCaneScheduleDrainDestroy: the full round-trip. Place cane on sand+water, then remove
// the support (set the sand to air). The edit reconciliation schedules a destroy tick (delay 1);
// draining at the target game-time fires tickBlock -> sugarCaneTick -> canSurvive false ->
// destroyBlock + drop. CITE: SugarCaneBlock.updateShape (schedule) + tick (destroy).
func TestSugarCaneScheduleDrainDestroy(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	base := pk.Position{X: 4, Y: 64, Z: 4}
	cane := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(base, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(pk.Position{X: 5, Y: 64, Z: 4}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(cane, sugarCane(0), dimMinY)

	// Sanity: currently survives.
	if !loop.sugarCaneCanSurvive(cane) {
		t.Fatal("precondition: cane should survive on sand+water")
	}
	dropsBefore := itemDropCount(loop)

	// Remove the support: turn the sand below into air, then run the edit reconciliation on the
	// changed cell (base). updateShape on the cane above schedules a destroy tick at gametime+1.
	mgr.SetBlock(base, loop.airState(), dimMinY)
	loop.onBlockTickEdit(base)

	if !loop.hasScheduledBlockTick(cane, sugarCaneTickType) {
		t.Fatal("a destroy tick should be scheduled for the unsupported cane")
	}

	// The cane is still present this tick (the destroy is deferred to the scheduled tick).
	if s, _ := mgr.GetBlock(cane, dimMinY); !block.IsSugarCane(s) {
		t.Fatal("cane should still exist before the scheduled tick fires")
	}

	// Advance one tick to the target game-time and drain.
	loop.gametime++ // now == scheduled triggerTick
	loop.tickScheduledBlocks()

	// Cane destroyed -> air.
	if s, _ := mgr.GetBlock(cane, dimMinY); !block.IsAir(s) {
		t.Fatalf("cane should be destroyed (air) after the scheduled tick, got state %d", s)
	}
	// And it dropped (destroyBlock dropBlock=true).
	if itemDropCount(loop) != dropsBefore+1 {
		t.Fatalf("expected one sugar-cane drop, drops went %d -> %d", dropsBefore, itemDropCount(loop))
	}
	// The queue is drained.
	if loop.blockTicks.Count() != 0 {
		t.Fatalf("after firing, queue should be empty; Count = %d", loop.blockTicks.Count())
	}
}

// TestSugarCaneStaleTickNoOp: if the scheduled block is gone (already broken) by the time the
// tick fires, tickBlock fires nothing — the stale-tick guard. CITE: ServerLevel.tickBlock
// (`if (state.is(block)) ...`).
func TestSugarCaneStaleTickNoOp(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	cane := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(cane, sugarCane(0), dimMinY)

	// Schedule a destroy tick directly, then REMOVE the cane before it fires.
	loop.scheduleBlockTick(cane, sugarCaneTickType, 1)
	mgr.SetBlock(cane, loop.airState(), dimMinY)
	dropsBefore := itemDropCount(loop)

	loop.gametime++
	loop.tickScheduledBlocks() // tickBlock sees air (not sugar cane) -> no-op

	if itemDropCount(loop) != dropsBefore {
		t.Fatal("a stale tick (block already gone) must drop nothing")
	}
}

// TestSugarCaneCascade: a 2-tall cane column whose base support is removed topples fully — the
// lower cane's scheduled destroy re-runs the edit reconciliation, which schedules the upper
// cane's destroy, cascading. CITE: SugarCaneBlock.tick -> destroyBlock -> updateNeighborsAt.
func TestSugarCaneCascade(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	base := pk.Position{X: 4, Y: 64, Z: 4}
	lower := pk.Position{X: 4, Y: 65, Z: 4}
	upper := pk.Position{X: 4, Y: 66, Z: 4}
	mgr.SetBlock(base, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(pk.Position{X: 5, Y: 64, Z: 4}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(lower, sugarCane(0), dimMinY)
	mgr.SetBlock(upper, sugarCane(0), dimMinY)

	// Remove the base support.
	mgr.SetBlock(base, loop.airState(), dimMinY)
	loop.onBlockTickEdit(base) // schedules the lower cane's destroy

	// Fire ticks until the queue drains (each destroy may schedule the next). Cap to avoid a
	// runaway in case of a bug.
	for i := 0; i < 10 && loop.blockTicks.Count() > 0; i++ {
		loop.gametime++
		loop.tickScheduledBlocks()
	}

	if s, _ := mgr.GetBlock(lower, dimMinY); !block.IsAir(s) {
		t.Fatal("lower cane should be destroyed")
	}
	if s, _ := mgr.GetBlock(upper, dimMinY); !block.IsAir(s) {
		t.Fatal("upper cane should cascade-destroy after the lower one")
	}
}

// TestSugarCaneGrowth: a cane below height 3 with an empty cell above advances AGE; at AGE 15 it
// places a new cane above and resets to AGE 0. CITE: SugarCaneBlock.randomTick.
func TestSugarCaneGrowth(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	base := pk.Position{X: 4, Y: 64, Z: 4}
	cane := pk.Position{X: 4, Y: 65, Z: 4}
	mgr.SetBlock(base, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(pk.Position{X: 5, Y: 64, Z: 4}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(cane, sugarCane(0), dimMinY)

	// AGE 0 -> 1 on a growth tick (cell above is air, column height 1 < 3).
	loop.sugarCaneRandomTick(sugarCane(0), cane)
	if got := block.SugarCaneAge(mustGet(t, mgr, cane)); got != 1 {
		t.Fatalf("AGE after one growth = %d, want 1", got)
	}

	// AGE 15 -> place new cane above, reset to 0.
	mgr.SetBlock(cane, sugarCane(15), dimMinY)
	loop.sugarCaneRandomTick(sugarCane(15), cane)
	if got := block.SugarCaneAge(mustGet(t, mgr, cane)); got != 0 {
		t.Fatalf("AGE after maturity reset = %d, want 0", got)
	}
	above := pk.Position{X: 4, Y: 66, Z: 4}
	if !block.IsSugarCane(mustGet(t, mgr, above)) {
		t.Fatal("a new cane should be placed above on maturity")
	}
}

// TestSugarCaneGrowthCappedAtHeight3: a cane already 3 tall does not grow. CITE:
// SugarCaneBlock.randomTick (`if (i < 3)`).
func TestSugarCaneGrowthCappedAtHeight3(t *testing.T) {
	loop, mgr := newSugarCaneLoop()
	mgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	c1 := pk.Position{X: 4, Y: 65, Z: 4}
	c2 := pk.Position{X: 4, Y: 66, Z: 4}
	c3 := pk.Position{X: 4, Y: 67, Z: 4}
	mgr.SetBlock(c1, sugarCane(0), dimMinY)
	mgr.SetBlock(c2, sugarCane(0), dimMinY)
	mgr.SetBlock(c3, sugarCane(5), dimMinY) // top cane, age 5, empty above

	loop.sugarCaneRandomTick(sugarCane(5), c3)
	if got := block.SugarCaneAge(mustGet(t, mgr, c3)); got != 5 {
		t.Fatalf("a 3-tall column must not grow; AGE = %d, want 5 (unchanged)", got)
	}
}

// TestChunkBlockTicksSaveLoadCycle: schedule a tick, PACK the chunk's ticks to the on-disk
// SavedTickNBT list (relative delays), then LOAD them into a FRESH loop at a different game-time
// and assert the unpacked tick fires at the correct re-anchored target. This exercises the full
// SUB-BLOCKTICK persistence round-trip (pack -> SavedTickNBT -> load -> unpack -> drain). CITE:
// LevelChunkTicks.pack/unpack via the save.SavedTickNBT bridge.
func TestChunkBlockTicksSaveLoadCycle(t *testing.T) {
	src, srcMgr := newSugarCaneLoop()
	cane := pk.Position{X: 4, Y: 65, Z: 4}
	srcMgr.SetBlock(pk.Position{X: 4, Y: 64, Z: 4}, block.ToStateID[block.Sand{}], dimMinY)
	srcMgr.SetBlock(cane, sugarCane(0), dimMinY)

	// At game-time 100, schedule a destroy tick 5 ticks out (triggerTick 105).
	src.gametime = 100
	src.scheduleBlockTick(cane, sugarCaneTickType, 5)

	saved := src.packChunkBlockTicks(level.ChunkPos{0, 0})
	if len(saved) != 1 {
		t.Fatalf("packed %d ticks, want 1", len(saved))
	}
	if saved[0].Delay != 5 || saved[0].ID != string(sugarCaneTickType) {
		t.Fatalf("packed tick = %+v, want delay 5 / sugar_cane", saved[0])
	}

	// Fresh loop, loaded at game-time 1000 (a different anchor). The unpacked tick should fire at
	// triggerTick 1005. Build the loop WITHOUT the auto-registered empty container so load installs
	// the seeded one.
	dst := NewTickLoop(newFakeClock())
	dstMgr := world.NewChunkManager()
	dst.world = dstMgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	dstMgr.Insert(level.ChunkPos{0, 0}, ch)
	dstMgr.SetBlock(cane, sugarCane(0), dimMinY) // no support -> will be destroyed when it fires
	dst.gametime = 1000
	dst.loadChunkBlockTicks(level.ChunkPos{0, 0}, saved)

	// Not due until 1005.
	dst.tickScheduledBlocks()
	if s, _ := dstMgr.GetBlock(cane, dimMinY); !block.IsSugarCane(s) {
		t.Fatal("loaded tick should not fire before its re-anchored target (1005)")
	}
	// Advance to the target and drain.
	for dst.gametime < 1005 {
		dst.gametime++
	}
	dst.tickScheduledBlocks()
	if s, _ := dstMgr.GetBlock(cane, dimMinY); !block.IsAir(s) {
		t.Fatal("loaded tick should fire at re-anchored target 1005 and destroy the unsupported cane")
	}
}

func mustGet(t *testing.T, mgr *world.ChunkManager, pos pk.Position) block.StateID {
	t.Helper()
	s, ok := mgr.GetBlock(pos, dimMinY)
	if !ok {
		t.Fatalf("GetBlock(%v) failed", pos)
	}
	return s
}
