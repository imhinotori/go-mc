package server

import "testing"

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
