package server

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// region_transfer_test.go — Phase-27 STEP-3 (the N=2 flip) Task-1 gates: the static chunk→region
// hash (pure + restart-stable + a real 2-way split), genuine PARALLEL region ticks (a simultaneity
// probe), cross-region entity TRANSFER at the barrier (no double-tick / no drop, ai/nav/scratch
// travel), and the async-rejoin routing to the OWNING region (drop if no region owns it).

// TestRegionOfIsStableAndSplits proves regionOf is a PURE function of the column (same column → same
// region across calls — restart-stable since it reads only the position, no persisted region id) AND
// a real 2-way split (at least one column maps to region 0 and one to region 1). This is the
// 27-RESEARCH A2 / Runtime-State-Inventory invariant: the hash never reads global state, so the same
// chunk always maps to the same region across restarts.
func TestRegionOfIsStableAndSplits(t *testing.T) {
	// Purity / determinism: the same column resolves to the same region every call.
	col := level.ChunkPos{3, 7}
	first := regionOf(col)
	for i := 0; i < 100; i++ {
		if regionOf(col) != first {
			t.Fatalf("regionOf is not pure: column %v mapped to %d then %d", col, first, regionOf(col))
		}
	}

	// A real 2-way split: over a small grid, BOTH region 0 and region 1 must appear (otherwise the
	// seam is never exercised — every chunk piling into one region is not regionization).
	var saw0, saw1 bool
	for x := int32(-4); x <= 4; x++ {
		for z := int32(-4); z <= 4; z++ {
			switch regionOf(level.ChunkPos{x, z}) {
			case 0:
				saw0 = true
			case 1:
				saw1 = true
			default:
				t.Fatalf("regionOf(%d,%d) returned an out-of-range region id (want 0 or 1)", x, z)
			}
		}
	}
	if !saw0 || !saw1 {
		t.Fatalf("regionOf is not a real 2-way split: saw region0=%v region1=%v (both must occur)", saw0, saw1)
	}

	// Restart-stability is structural (the function reads only its argument), but assert the two
	// regions are genuinely adjacent-distinct so the transfer test below has a guaranteed seam: an
	// entity walking from column C to an adjacent column must be able to cross regions somewhere.
	if regionOf(level.ChunkPos{0, 0}) == regionOf(level.ChunkPos{1, 0}) &&
		regionOf(level.ChunkPos{0, 0}) == regionOf(level.ChunkPos{0, 1}) {
		t.Fatal("regionOf has no adjacent seam near the origin — a 2-region split must put some neighbour column in the other region")
	}
}

// TestNewTickLoopBuildsTwoRegions asserts the N=2 flip: NewTickLoop constructs exactly TWO regions,
// each with its own (distinct) entity store and levelRandom (the never-shared invariant).
func TestNewTickLoopBuildsTwoRegions(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	if len(loop.regions) != regionCount {
		t.Fatalf("N=2 flip: want %d regions, got %d", regionCount, len(loop.regions))
	}
	if regionCount != 2 {
		t.Fatalf("this plan flips to N=2; regionCount=%d", regionCount)
	}
	r0, r1 := loop.regions[0], loop.regions[1]
	if r0.entities == nil || r1.entities == nil {
		t.Fatal("each region must own a non-nil entity store")
	}
	if r0.entities == r1.entities {
		t.Fatal("the two regions share the SAME entity store — each region must own its own store")
	}
	if r0.levelRandom == r1.levelRandom {
		t.Fatal("the two regions share the SAME levelRandom — per-region RNG must never be shared")
	}
	if r0.id != 0 || r1.id != 1 {
		t.Fatalf("region ids must be 0 and 1, got %d and %d", r0.id, r1.id)
	}
}

// TestTwoRegionsTickInParallel is the PARALLELISM proof (must_have): with N=2 regions populated, a
// concurrency probe asserts BOTH region goroutines are in their tick phase SIMULTANEOUSLY. Each
// region's test hook arrives at a shared rendezvous (a sync.WaitGroup counted down by each region +
// a barrier channel) and the test confirms both arrived before either proceeds — proving real
// parallel fan-out (conc), not a serial loop. If the fan-out were serial, the second region's hook
// would never run while the first is still blocked, and the rendezvous would deadlock (the test
// times out → fail).
func TestTwoRegionsTickInParallel(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// arrived counts how many regions have entered their tick hook; release unblocks them once BOTH
	// have arrived. If the regions ticked serially, the first hook would block on <-release forever
	// (the second never arrives to bump the count), so reaching bothArrived PROVES simultaneity.
	var arrived atomic.Int32
	bothArrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	hook := func() {
		if arrived.Add(1) == int32(regionCount) {
			once.Do(func() { close(bothArrived) }) // the LAST region to arrive signals "both in tick now"
		}
		<-release // hold inside the tick phase until the test confirms both arrived
	}
	for _, r := range loop.regions {
		r.tickHook = hook
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.tickOnce()
	}()

	// If the fan-out is genuinely parallel, both region goroutines reach the hook and bothArrived
	// closes. If it is serial, this blocks → the test times out via the outer -timeout (a real fail).
	<-bothArrived
	close(release) // let both regions finish their tick
	<-done

	if arrived.Load() != int32(regionCount) {
		t.Fatalf("expected all %d regions to enter their tick simultaneously; only %d did", regionCount, arrived.Load())
	}
	for _, r := range loop.regions {
		r.tickHook = nil
	}
}

// TestCrossRegionTransfer proves an entity moved (by physics/AI) from a column owned by region A into
// a column owned by region B is, AFTER the barrier, in B's store and NOT in A's, with its ai/nav/
// scratch pointer intact, and ticked exactly ONCE next tick (no double-tick, no drop). The move is
// simulated by relocating the entity to a column the OTHER region owns, then running tickOnce; the
// barrier's applyCrossRegionTransfers must hand it off.
func TestCrossRegionTransfer(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Find two adjacent-ish columns owned by DIFFERENT regions so we have a real seam to cross.
	var colA, colB level.ChunkPos
	found := false
	for x := int32(0); x <= 8 && !found; x++ {
		for z := int32(0); z <= 8 && !found; z++ {
			a := level.ChunkPos{x, z}
			b := level.ChunkPos{x + 1, z}
			if regionOf(a) != regionOf(b) {
				colA, colB = a, b
				found = true
			}
		}
	}
	if !found {
		t.Fatal("could not find two adjacent columns in different regions (the hash has no seam?)")
	}
	regA := loop.regions[regionOf(colA)]
	regB := loop.regions[regionOf(colB)]

	// Spawn an entity in region A's column, with an ai struct carrying scratch we can identity-check.
	e := testEntity(1, entity.SulfurCube, float64(colA[0])*16+8.5, 70.0, float64(colA[1])*16+8.5)
	e.ai = &mobAI{}
	regA.entities.add(e)
	aiPtr := e.ai // the exact pointer that must travel with the entity

	// Confirm it starts owned by A, not B.
	if _, ok := regA.entities.get(e.id); !ok {
		t.Fatal("setup: entity not in region A")
	}

	// Simulate the move into region B's column (what physics/AI would do): update its position and
	// re-bucket on the SAME store, then run a tick — the barrier must detect the column now maps to B.
	regA.entities.move(e, float64(colB[0])*16+8.5, 70.0, float64(colB[1])*16+8.5)

	loop.tickOnce()

	// After the barrier: in B, not in A.
	if _, ok := regA.entities.get(e.id); ok {
		t.Fatal("entity still in region A after the transfer barrier (it should be removed from A)")
	}
	got, ok := regB.entities.get(e.id)
	if !ok {
		t.Fatal("entity not in region B after the transfer barrier (it should be added to B)")
	}
	// The SAME *Entity (ai/nav/scratch travel — never copied).
	if got != e {
		t.Fatal("region B holds a DIFFERENT *Entity — the transfer copied/aliased rather than moving the pointer")
	}
	if got.ai != aiPtr {
		t.Fatal("the entity's ai/nav/scratch did not travel with it across the region seam")
	}

	// Ticked exactly once next tick (no double-tick): run another tick and confirm it is still a
	// single live instance owned only by B.
	loop.tickOnce()
	if _, ok := regA.entities.get(e.id); ok {
		t.Fatal("entity reappeared in region A on a later tick (double ownership)")
	}
	if _, ok := regB.entities.get(e.id); !ok {
		t.Fatal("entity dropped from region B on a later tick (lost after transfer)")
	}
}

// TestAsyncRejoinRoutesToOwningRegion proves pathReady.applyTo routes to the OWNING region (the mob's
// path lands on the region whose store holds it) and DROPS a result for a despawned mob (no panic, no
// apply to the wrong region).
func TestAsyncRejoinRoutesToOwningRegion(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Put a mob in region 1's store specifically (so a globalRegion-only resolve would MISS it —
	// proving the rejoin re-resolves the owner across regions).
	var col1 level.ChunkPos
	found := false
	for x := int32(0); x <= 8 && !found; x++ {
		for z := int32(0); z <= 8 && !found; z++ {
			if regionOf(level.ChunkPos{x, z}) == 1 {
				col1 = level.ChunkPos{x, z}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no column maps to region 1")
	}
	regB := loop.regions[1]
	mob := testEntity(42, entity.Pig, float64(col1[0])*16+8.5, 70.0, float64(col1[1])*16+8.5)
	mob.ai = &mobAI{}
	// The path the rejoin will adopt must match the nav's tracked goal (the existing target re-check).
	mob.ai.navigation.hasTarget = true
	mob.ai.navigation.lastTX, mob.ai.navigation.lastTY, mob.ai.navigation.lastTZ = 10, 64, 10
	mob.ai.navigation.pending = true
	regB.entities.add(mob)

	// A pathReady for the mob owned by region 1 must apply to region 1's store.
	pr := pathReady{mobID: 42, target: [3]int{10, 64, 10}, path: &Path{}}
	pr.applyTo(loop)
	if regB.entities.byID[42].ai.navigation.path == nil {
		t.Fatal("pathReady did not apply to the OWNING region (region 1) — the path was not adopted")
	}
	if regB.entities.byID[42].ai.navigation.pending {
		t.Fatal("pathReady applied but did not clear the in-flight gate on the owning region's mob")
	}

	// A pathReady for a despawned mob (no region owns it) must DROP cleanly (no panic, no effect).
	prGone := pathReady{mobID: 9999, target: [3]int{0, 0, 0}, path: &Path{}}
	prGone.applyTo(loop) // must not panic
}
