package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// region_n2_regression_test.go — the N=2 regionization-bug-class regression gate (the hand-directed
// hotfix Step 7). Each test below targets ONE silently-skipped-region bug: before the fix, a
// coordinator-phase or dispatch-handler per-region access fell back to region 0 via only(), so
// scheduled blocks / fluids / movement-broadcast / placement-collision / block-drops were dead or
// wrong in region 1. Each test pins a REGION-1 column and asserts the effect lands in region 1.
//
// regionOf is the (X^Z)&1 checkerboard, so column {1,0} → region 1 (verified: (1^0)&1 == 1). World X
// in [16,32) maps to chunk X=1; the tests use X≈24.5 (chunk 1) so the touched column is region 1.

// n2RegionColumn is a column that maps to region 1 (the non-default region the old fallback skipped).
var n2RegionColumn = level.ChunkPos{1, 0}

// newN2Loop builds a TickLoop with the SHARED world wired into EVERY region (Phase-27 N=2 invariant)
// and ready all-air chunks at both a region-0 and the region-1 column, so block/fluid/entity ops in
// region 1 have a loaded column. installVanillaPigRegistry so any spawn path has its declaration.
func newN2Loop(t *testing.T) (*TickLoop, *world.ChunkManager) {
	t.Helper()
	if regionOf(n2RegionColumn) != 1 {
		t.Fatalf("test precondition: column %v must map to region 1, got region %d", n2RegionColumn, regionOf(n2RegionColumn))
	}
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	// Phase-27 N=2: the world is SHARED — wire the SAME manager into every region so a region-1 op
	// reads/writes the same blocks the coordinator does (the bug the whole fix addresses lives in the
	// PER-REGION stores, not the shared world).
	for _, r := range loop.regions {
		r.world = mgr
	}
	for _, col := range []level.ChunkPos{{0, 0}, n2RegionColumn} {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(col, ch)
	}
	installVanillaPigRegistry(loop)
	return loop, mgr
}

// region1Pos returns a world block position inside the region-1 column (chunk X=1).
func region1Pos(localX, y, localZ int) pk.Position {
	return pk.Position{X: 16 + localX, Y: y, Z: localZ}
}

// allRegionEntities counts entities across EVERY region's store (the cross-region total).
func allRegionEntities(loop *TickLoop) int {
	total := 0
	for _, r := range loop.regions {
		if r.entities != nil {
			total += r.entities.len()
		}
	}
	return total
}

// TestScheduledBlockTicksFireInRegion1 locks the tickWorld per-region scheduled-block drain: a
// sugar-cane destroy tick scheduled in a REGION-1 column must FIRE when tickWorld runs (forEachRegion
// drains every region's blockTicks). Before the fix tickWorld drained only region 0, so a region-1
// scheduled block never fired (the block stayed forever). FAILS on pre-fix code (the tick never
// fires; cane survives), PASSES after.
func TestScheduledBlockTicksFireInRegion1(t *testing.T) {
	loop, mgr := newN2Loop(t)

	// Register a block-tick container for the region-1 chunk in REGION 1's blockTicks manager, so a
	// scheduleBlockTick inside it is not dropped by LevelTicks.Schedule (vanilla registers a container
	// for every loaded chunk; ensureChunkBlockTicks is the generic registration). Route it via
	// withRegion so it lands in region 1's manager — exactly how chunkReady.applyTo now routes it.
	loop.withRegion(loop.regions[1], func() { loop.ensureChunkBlockTicks(n2RegionColumn) })

	base := region1Pos(4, 64, 4) // the support
	cane := region1Pos(4, 65, 4) // the sugar cane atop the support
	mgr.SetBlock(base, block.ToStateID[block.Sand{}], dimMinY)
	mgr.SetBlock(pk.Position{X: base.X + 1, Y: base.Y, Z: base.Z}, block.ToStateID[block.Water{}], dimMinY)
	mgr.SetBlock(cane, sugarCane(0), dimMinY)

	// Remove the support and reconcile: updateShape schedules the cane's destroy at gametime+1, into
	// the OWNING region's (region 1's) blockTicks queue (the break-path wrap routes it there).
	mgr.SetBlock(base, loop.airState(), dimMinY)
	loop.withRegion(loop.regions[1], func() { loop.onBlockTickEdit(base) })

	// The destroy tick must be queued in REGION 1, not region 0.
	if loop.regions[1].blockTicks == nil || loop.regions[1].blockTicks.Count() == 0 {
		t.Fatal("a destroy tick should be scheduled in region 1's blockTicks queue")
	}
	if loop.regions[0].blockTicks != nil && loop.regions[0].blockTicks.Count() != 0 {
		t.Fatal("region 0's queue must be empty — the region-1 schedule must not leak into region 0")
	}

	// Advance to the trigger tick and run tickWorld (the coordinator phase). forEachRegion must drain
	// region 1's queue, firing the cane's destroy.
	loop.gametime++
	loop.tickWorld()

	if s, _ := mgr.GetBlock(cane, dimMinY); !block.IsAir(s) {
		t.Fatalf("region-1 cane should be destroyed by the scheduled tick fired in tickWorld, got state %d", s)
	}
}

// TestFluidTicksFlowInRegion1 locks the tickWorld per-region fluid drain: a fluid tick scheduled in a
// REGION-1 column must process when tickWorld runs. A water source placed in region 1 with an
// adjacent air cell, kicked via scheduleFluidTick into region 1's queue, must spread on the tickWorld
// pass. Before the fix tickWorld drained only region 0's fluidSchedule, so region-1 water never
// flowed. FAILS pre-fix (no spread), PASSES after.
func TestFluidTicksFlowInRegion1(t *testing.T) {
	loop, mgr := newN2Loop(t)

	// A FLAT stone floor across the region-1 chunk at Y=63 so the source water spreads SIDEWAYS in all
	// directions (no drop-off bias steers it away from the side cell — FlowingFluid spreads down first
	// only where the cell below is air; over a flat floor it spreads horizontally evenly).
	ch, _ := mgr.Get(n2RegionColumn)
	fillFloor(ch, 63)

	src := region1Pos(8, 64, 8)
	side := pk.Position{X: src.X + 1, Y: src.Y, Z: src.Z} // adjacent air the water should flow into
	mgr.SetBlock(src, waterStateID(0), dimMinY)           // a water SOURCE (level 0)

	// Schedule the source's fluid re-evaluation into REGION 1's queue (the owning region).
	loop.withRegion(loop.regions[1], func() { loop.scheduleFluidTick(src) })

	if loop.regions[1].fluidSchedule == nil || loop.regions[1].fluidSchedule.empty() {
		t.Fatal("a fluid tick should be scheduled in region 1's fluidSchedule queue")
	}

	// Run tickWorld repeatedly (advancing gametime) so forEachRegion drains region 1's fluid queue and
	// the water spreads into the side cell. Cap to prove termination.
	flowed := false
	for i := 0; i < 50; i++ {
		loop.gametime++
		loop.tickWorld()
		if _, isWater := levelAt(mgr, side); isWater {
			flowed = true
			break
		}
	}
	if !flowed {
		t.Fatal("region-1 water never flowed into the adjacent cell — tickWorld did not drain region 1's fluid queue")
	}
}

// TestEntityMovementBroadcastInRegion1 LOCKS the already-fixed tickEntityMovement: a mob owned by
// REGION 1 that moves must broadcast a delta move packet to a tracking player. Before the
// already-applied fix tickEntityMovement iterated only region 0, so region-1 mobs appeared frozen.
// This pins that fix so a future refactor cannot regress it. FAILS on the original (pre-already-fixed)
// code; PASSES now.
func TestEntityMovementBroadcastInRegion1(t *testing.T) {
	loop, _ := newN2Loop(t)

	// Player observer standing in the region-1 column so the mob is in track range.
	observer := newTrackerPlayer(loop, 100000, 24.5, 8.5)

	// A pig owned by region 1 (its column maps to region 1). Add it to region 1's store via withRegion.
	e := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 25.5, 64, 8.5)
	loop.withRegion(loop.regions[1], func() { loop.cur().entities.add(e) })
	if _, ok := loop.regions[1].entities.get(e.id); !ok {
		t.Fatal("test precondition: the mob must be in region 1's store")
	}
	observer.tracked = map[int32]bool{e.id: true}

	// Seed moveInit (no packet on the first pass), then re-arm the capture client.
	loop.tickEntityMovement()
	_ = drainPackets(observer.client)
	observer.client = captureClient(64)
	loop.clientIndex[observer.client] = observer

	// Move the region-1 mob one block (a small delta → MoveEntityPos) and broadcast.
	loop.withRegion(loop.regions[1], func() { loop.cur().entities.move(e, e.x+1.0, e.y, e.z) })
	loop.tickEntityMovement()

	got := drainPackets(observer.client)
	if countID(got, packetid.ClientboundMoveEntityPos) != 1 {
		t.Fatalf("region-1 mob move: want 1 MoveEntityPos broadcast, got %d (mob appears frozen — region 1 skipped)",
			countID(got, packetid.ClientboundMoveEntityPos))
	}
}

// TestPlacementCollisionSeesRegion1 locks the cross-region placement obstruction scan: a mob standing
// in region 1's store occupying the target cell must BLOCK a block placement there. Before the fix
// placementObstructedByEntity scanned only one region's store, so a region-1 mob did not block a place
// (a block could be placed inside it). FAILS pre-fix (place succeeds, not obstructed), PASSES after.
func TestPlacementCollisionSeesRegion1(t *testing.T) {
	loop, _ := newN2Loop(t)

	// The target placement cell, in the region-1 column.
	placePos := region1Pos(8, 64, 8)

	// A mob whose feet-anchored AABB overlaps the unit cube at placePos, held in REGION 1's store.
	mob := NewEntity(loop.idAlloc.AllocID(), entity.Pig, float64(placePos.X)+0.5, float64(placePos.Y), float64(placePos.Z)+0.5)
	loop.withRegion(loop.regions[1], func() { loop.cur().entities.add(mob) })

	// The obstruction scan runs on the dispatch/coordinator goroutine (no region registered) — exactly
	// where the bug lived. It must see the region-1 mob across regions and report obstructed.
	if !loop.placementObstructedByEntity(placePos) {
		t.Fatal("a mob in region 1 occupying the cell must obstruct the placement (cross-region scan missing)")
	}

	// Sanity: an empty cell far from the mob is NOT obstructed.
	clear := region1Pos(0, 64, 0)
	if loop.placementObstructedByEntity(clear) {
		t.Fatal("an empty region-1 cell must not be reported obstructed")
	}
}

// TestBlockDropLandsInOwningRegion locks the break-path region routing: breaking a block in a REGION-1
// column must spawn the dropped item into REGION 1's entity store, not region 0's. destroyBlock (the
// single break funnel) wraps its drop in withRegion(regionForColumn). Before the fix the drop landed
// in region 0 via the only() fallback. FAILS pre-fix (drop in region 0), PASSES after.
func TestBlockDropLandsInOwningRegion(t *testing.T) {
	loop, mgr := newN2Loop(t)

	target := region1Pos(4, 64, 4)
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	region0Before := loop.regions[0].entities.len()
	region1Before := loop.regions[1].entities.len()

	// Break the block via the funnel (a nil player drops, like the cascade/sugar-cane path). This runs
	// with NO region registered (the dispatch/coordinator context the bug lived in); the destroyBlock
	// wrap must route the drop into the owning region (region 1).
	air := block.ToStateID[block.Air{}]
	loop.destroyBlock(nil, target, air, 0, false)

	if got := loop.regions[1].entities.len(); got != region1Before+1 {
		t.Fatalf("region-1 break: drop count in region 1 = %d, want %d (drop landed in the wrong region)",
			got, region1Before+1)
	}
	if got := loop.regions[0].entities.len(); got != region0Before {
		t.Fatalf("region-1 break must NOT add a drop to region 0: count %d -> %d", region0Before, got)
	}
	// The block is now air (broken on the shared world).
	if s, _ := mgr.GetBlock(target, dimMinY); !block.IsAir(s) {
		t.Fatalf("the broken block should be air, got state %d", s)
	}
}

// TestStrictRegionPanicsOnUnwrappedPerRegionAccess proves the strictRegion guard: with strictRegion
// armed, a PER-REGION access (cur()) from a goroutine with NO region registered PANICS — the loud
// catch for the systemic bug class. A per-region phase called directly on the test goroutine (no
// withRegion, not inside the fan-out) is exactly the unwrapped access the guard must reject.
func TestStrictRegionPanicsOnUnwrappedPerRegionAccess(t *testing.T) {
	loop, _ := newN2Loop(t)
	loop.strictRegion = true

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("cur() with strictRegion armed and no region registered must PANIC, but did not")
		}
	}()

	// A direct PER-REGION store access off the fan-out: this is the bug-class access pattern, and with
	// strictRegion on it must panic rather than silently resolve to region 0.
	_ = loop.cur().entities
	t.Fatal("unreachable: cur() should have panicked before this line")
}

// TestStrictRegionWorldReadNeverPanics proves the SHARED accessors are exempt from the guard: world()
// / worker() read globalRegion directly and must NEVER panic, even with strictRegion armed and no
// region registered — a coordinator/dispatch read of the shared world is legitimate. Also confirms
// cur() inside withRegion does not panic (a properly-wrapped per-region access).
func TestStrictRegionWorldReadNeverPanics(t *testing.T) {
	loop, _ := newN2Loop(t)
	loop.strictRegion = true

	// SHARED reads on the bare test goroutine (no region registered): must not panic.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("world()/worker() must never panic under strictRegion, got panic: %v", r)
			}
		}()
		_ = loop.world()
		_ = loop.worker()
	}()

	// A PROPERLY-wrapped per-region access (inside withRegion) must also not panic — the guard only
	// fires for UNWRAPPED access.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("cur() inside withRegion must not panic under strictRegion, got panic: %v", r)
			}
		}()
		loop.withRegion(loop.regions[1], func() {
			if loop.cur() != loop.regions[1] {
				t.Fatal("cur() inside withRegion(region 1) must resolve to region 1")
			}
		})
	}()
}
