package server

import "testing"

// pathfinder_test.go — AI-02: the request->snapshot->result A* seam, tested PURELY over a
// hand-built immutable pathRegion snapshot (no world, no *TickLoop). The purity of these
// tests is the proof of the Phase-8 hinge: computePath closes over no tick-owned state, so
// the same pathRequest can be handed to an off-tick ants pool in OPT-01 with ZERO change.
//
// Snapshots are built by hand: newTestRegion lays a flat solid floor at a given Y over a box,
// and optional walls are set with setSolid. A mob stands on the floor (feet at floorY+1) and
// paths to a target on the same floor. The evaluator treats a node as standable iff the block
// BELOW is solid and the mob's height of air is clear above (node_evaluator.go).

// newTestRegion builds an immutable pathRegion spanning the box [minX,maxX]×[minY,maxY]×
// [minZ,maxZ], with a solid floor filling y==floorY across the whole horizontal extent and
// everything else air. The floor's TOP surface is floorY+1, so a mob standing on it has its
// feet at floorY+1 and the node it occupies is (x, floorY+1, z) with the solid floor below.
func newTestRegion(minX, minY, minZ, maxX, maxY, maxZ, floorY int) *pathRegion {
	r := newPathRegion(minX, minY, minZ, maxX, maxY, maxZ)
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			r.set(x, floorY, z, true)
		}
	}
	return r
}

// setSolid marks a single cell solid (a wall block) in the snapshot. Used to build obstacles.
func (r *pathRegion) setSolid(x, y, z int) { r.set(x, y, z, true) }

// flatRequest builds a pathRequest for a mob standing on the floor at (sx, floorY+1, sz)
// pathing to (tx, floorY+1, tz), with a generous visited budget.
func flatRequest(r *pathRegion, sx, sz, tx, tz, floorY, maxVisited int) pathRequest {
	return pathRequest{
		startX: sx, startY: floorY + 1, startZ: sz,
		targetX: tx, targetY: floorY + 1, targetZ: tz,
		region:     r,
		mobW:       0.9, // a Pig
		mobH:       0.9,
		followRange: 24,
		reachRange:  1,
		maxVisited:  maxVisited,
	}
}

// TestComputePathFlat: on a flat-floor snapshot, computePath from A to a reachable B returns
// a Path whose nodes step from A toward B and reach it.
func TestComputePathFlat(t *testing.T) {
	const floorY = 64
	r := newTestRegion(-2, floorY, -2, 12, floorY+4, 12, floorY)
	req := flatRequest(r, 0, 0, 8, 0, floorY, 1024)

	p := computePath(req)
	if p == nil || len(p.nodes) == 0 {
		t.Fatalf("expected a path on flat ground, got nil/empty")
	}
	first := p.nodes[0]
	if first.x != 0 || first.z != 0 {
		t.Fatalf("path must start at the mob node (0,0); got (%d,%d)", first.x, first.z)
	}
	last := p.nodes[len(p.nodes)-1]
	// The last node must be at (or within reachRange of) the target.
	if abs(last.x-8) > 1 || abs(last.z-0) > 1 {
		t.Fatalf("path did not reach the target (8,0); ended at (%d,%d)", last.x, last.z)
	}
}

// TestComputePathAroundObstacle: a wall between A and B forces a path that ROUTES AROUND it —
// no node sits inside the wall column, and the path still reaches B.
func TestComputePathAroundObstacle(t *testing.T) {
	const floorY = 64
	r := newTestRegion(-2, floorY, -4, 12, floorY+4, 4, floorY)
	// A wall at x=4 spanning z=-3..2 (leaving a gap at z=3..4 to route around), full mob
	// height above the floor (floorY+1, floorY+2) so the mob cannot pass through it.
	for z := -3; z <= 2; z++ {
		r.setSolid(4, floorY+1, z)
		r.setSolid(4, floorY+2, z)
	}
	req := flatRequest(r, 0, 0, 8, 0, floorY, 4096)

	p := computePath(req)
	if p == nil || len(p.nodes) == 0 {
		t.Fatalf("expected a path around the wall, got nil/empty")
	}
	// No node may sit in the wall column at a blocked z.
	for _, n := range p.nodes {
		if n.x == 4 && n.z >= -3 && n.z <= 2 {
			t.Fatalf("path went THROUGH the wall at node (%d,%d,%d)", n.x, n.y, n.z)
		}
	}
	last := p.nodes[len(p.nodes)-1]
	if abs(last.x-8) > 1 || abs(last.z-0) > 1 {
		t.Fatalf("path around the wall did not reach the target (8,0); ended at (%d,%d)", last.x, last.z)
	}
}

// TestPathNodeBudget: with a small maxVisited and a far/unreachable target, computePath
// returns WITHOUT visiting unbounded nodes — the visited count is capped (Pitfall 6 / T-7-04).
func TestPathNodeBudget(t *testing.T) {
	const floorY = 64
	// A large open floor and a far target; the budget must stop the search early.
	r := newTestRegion(-40, floorY, -40, 40, floorY+4, 40, floorY)
	const budget = 20
	req := flatRequest(r, 0, 0, 39, 39, floorY, budget)

	p, visited := computePathDebug(req)
	if visited > budget {
		t.Fatalf("A* visited %d nodes, exceeding the maxVisited budget of %d", visited, budget)
	}
	// With such a tight budget the far target is not reached; a partial/best-effort path (or
	// nil) is acceptable — the assertion is the BUDGET, not reaching the target.
	_ = p
}

// TestComputePathPure: the Phase-8 readiness assertion. computePath is called with ONLY a
// pathRequest (a hand-built snapshot, no *TickLoop, no live world) and returns a Path —
// proving it closes over no tick-owned state. If this test compiles and runs, the seam is
// pure by construction (computePath's signature takes only pathRequest).
func TestComputePathPure(t *testing.T) {
	const floorY = 0
	r := newPathRegion(0, floorY, 0, 6, floorY+3, 2)
	for x := 0; x <= 6; x++ {
		for z := 0; z <= 2; z++ {
			r.set(x, floorY, z, true) // a tiny hand-built floor — no world involved
		}
	}
	req := pathRequest{
		startX: 0, startY: floorY + 1, startZ: 1,
		targetX: 5, targetY: floorY + 1, targetZ: 1,
		region:      r,
		mobW:        0.9,
		mobH:        0.9,
		followRange: 24,
		reachRange:  1,
		maxVisited:  1024,
	}
	p := computePath(req) // no t.only().world, no TickLoop — purity proven by the call shape
	if p == nil || len(p.nodes) == 0 {
		t.Fatalf("pure computePath over a hand-built snapshot returned no path")
	}
	if p.nodes[0].x != 0 {
		t.Fatalf("pure path must start at the mob node x=0; got %d", p.nodes[0].x)
	}
}

// TestPathUnreachable: a target fully enclosed by solid blocks yields no path (or a partial
// best-effort) WITHOUT panicking.
func TestPathUnreachable(t *testing.T) {
	const floorY = 64
	r := newTestRegion(-2, floorY, -2, 12, floorY+4, 12, floorY)
	// Box the target at (8,0) in: solid walls on all 4 sides at the mob's height band.
	for _, d := range [][2]int{{7, 0}, {9, 0}, {8, -1}, {8, 1}} {
		r.setSolid(d[0], floorY+1, d[1])
		r.setSolid(d[0], floorY+2, d[1])
	}
	req := flatRequest(r, 0, 0, 8, 0, floorY, 4096)

	// Must not panic; an enclosed target yields nil or a best-effort partial that never
	// enters the enclosure.
	p := computePath(req)
	if p != nil {
		for _, n := range p.nodes {
			if n.x == 8 && n.z == 0 && n.y == floorY+1 {
				t.Fatalf("path reached an enclosed target it should not be able to enter")
			}
		}
	}
}

// abs is a tiny int helper for the tests.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
