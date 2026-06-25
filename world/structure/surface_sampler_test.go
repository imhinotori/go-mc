package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// TestSampleSurfaceYDeterministic: the router-backed column sampler returns the SAME Y
// for the same (x,z) (pure over the bound router) and does so WITHOUT any chunk fill —
// it only computes the PreliminarySurfaceLevel density node. Two samplers over the same
// seed agree, and quart-snapping means all four blocks in a quart cell share a Y.
func TestSampleSurfaceYDeterministic(t *testing.T) {
	r, err := router.NewRouter(123456789)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	s := NewRouterSurfaceSampler(r)

	// Same (x,z) -> same Y, repeatedly.
	y1 := s.SampleSurfaceY(100, 200)
	y2 := s.SampleSurfaceY(100, 200)
	if y1 != y2 {
		t.Fatalf("SampleSurfaceY non-deterministic: %d != %d", y1, y2)
	}

	// A second sampler over a freshly-built router with the same seed agrees (pure).
	r2, err := router.NewRouter(123456789)
	if err != nil {
		t.Fatalf("NewRouter(2): %v", err)
	}
	s2 := NewRouterSurfaceSampler(r2)
	if y3 := s2.SampleSurfaceY(100, 200); y3 != y1 {
		t.Fatalf("two samplers over the same seed disagree: %d != %d", y3, y1)
	}

	// Quart-snapping: blocks in the same 4x4 quart cell share a Y (x in [100..103]
	// snap to qx=100).
	for x := 100; x <= 103; x++ {
		for z := 200; z <= 203; z++ {
			if s.SampleSurfaceY(x, z) != y1 {
				t.Fatalf("quart cell not uniform: (%d,%d) Y differs from (100,200)", x, z)
			}
		}
	}
}
