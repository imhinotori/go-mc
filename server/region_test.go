package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// TestRegionStructHasPerRegionFields is the Task-1 compile-anchor for the Phase-27 STEP-1
// extraction: it proves a newRegion is constructed with its per-region store + per-region RNG
// non-nil from construction, and that two regions get DISTINCT levelRandom pointers — the
// never-shared-RNG invariant (27-RESEARCH anti-pattern / threat T-27-EXT-2). Only ONE region
// exists at N=1 today, but locking the distinct-pointer invariant here protects Plan 03 (N=2).
func TestRegionStructHasPerRegionFields(t *testing.T) {
	r := newRegion(globalRegion, nil)

	if r.entities == nil {
		t.Fatal("newRegion: entities store is nil; the per-region store must be non-nil from construction")
	}
	if r.levelRandom == nil {
		t.Fatal("newRegion: levelRandom is nil; the per-region RNG must be non-nil from construction")
	}

	// Two regions must NOT share a levelRandom: a shared LegacyRandomSource across regions is a
	// data race AND diverges the RNG stream non-deterministically (the never-shared invariant).
	r2 := newRegion(globalRegion, nil)
	if r2.levelRandom == nil {
		t.Fatal("newRegion: second region levelRandom is nil")
	}
	if r.levelRandom == r2.levelRandom {
		t.Fatal("newRegion: two regions share the SAME levelRandom pointer; per-region RNG must never be shared")
	}
}

// TestRegionExtractionBehaviorNeutral is the Task-2 load-bearing proof that the field move is
// BEHAVIOR-NEUTRAL: a TickLoop built by NewTickLoop has exactly ONE region (globalRegion), the
// per-region store is the SAME store every phase loop ranges (no duplicate/aliased store —
// T-27-EXT-1), and driving physics with a spawned entity produces the IDENTICAL landing the
// pre-extraction loop produced (reusing TestEntityLands's expectations). It is the N=1 == today
// proof the whole phase rests on.
func TestRegionExtractionBehaviorNeutral(t *testing.T) {
	loop, mgr := newPhysicsLoop()

	// At N=1 there is exactly ONE region (globalRegion) holding everything the old TickLoop held.
	if len(loop.regions) != 1 {
		t.Fatalf("N=1 extraction: want exactly 1 region, got %d", len(loop.regions))
	}
	if loop.only() != loop.region(globalRegion) {
		t.Fatal("only() must return the globalRegion")
	}

	// The store the phase loops range must be SINGLE-SOURCED on the region (no second store).
	if loop.only().entities == nil {
		t.Fatal("the single region must own a non-nil entity store")
	}

	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY) // top surface y=65

	// Spawn through the region's store; the same store tickPhysics ranges.
	e := testEntity(1, entity.SulfurCube, 8.5, 70.0, 8.5)
	loop.only().entities.add(e)

	// The spawned entity is reachable through the region's store accessor.
	if got, ok := loop.only().entities.get(e.id); !ok || got != e {
		t.Fatalf("entity not reachable through region store: ok=%v got=%v", ok, got)
	}

	// Drive physics: identical to TestEntityLands — the entity falls and settles on the floor top.
	for i := 0; i < 200; i++ {
		loop.tickPhysics()
		if e.y < float64(floorY+1) {
			t.Fatalf("tick %d: entity sank below floor top (y=%v < %v)", i, e.y, floorY+1)
		}
	}
	const floorTop = floorY + 1 // 65
	if d := e.y - float64(floorTop); d < -1e-6 || d > 1e-3 {
		t.Fatalf("entity did not settle on the floor: y=%v want ~%v (behavior drift!)", e.y, floorTop)
	}
	if !e.onGround {
		t.Fatalf("entity resting on the floor must have onGround=true (y=%v)", e.y)
	}

	// The store the physics loop mutated is STILL the region's store (no aliased copy drifted).
	if got, _ := loop.only().entities.get(e.id); got == nil || got.y != e.y {
		t.Fatal("the physics loop and the region store diverged — duplicate store (T-27-EXT-1)")
	}
}

// TestExtractionRaceClean pins the Task-3 invariant that the N=1 extraction preserves the exact
// single-owner discipline: driving the loop's advance() seam while spawning into the region's store
// (all on the owner, the way the pipeline does) stays race-free. It mirrors the existing -race test
// structure (newPhysicsLoop + advance) so the Docker -race gate keeps catching any future
// per-region field that gets read off the owning goroutine (T-27-01 / T-27-EXT-1). Under -race this
// is the regression backstop; without -race it is a fast smoke test of the advance+spawn path.
func TestExtractionRaceClean(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, 64)

	clk := loop.clock.(*fakeClock)
	loop.start(clk.Now())

	// Interleave: spawn an entity into the region store, then advance logical ticks (which runs the
	// full per-region pipeline over t.only()'s store). The single region == today's single owner, so
	// every access is on the tick goroutine and the race detector stays quiet.
	for i := int32(1); i <= 20; i++ {
		e := testEntity(i, entity.SulfurCube, 8.5, 70.0, 8.5)
		loop.only().entities.add(e)
		clk.add(tickStep)
		loop.advance(clk.Now())
	}

	if loop.only().entities.len() == 0 {
		t.Fatal("expected entities to remain in the region store after advancing")
	}
}
