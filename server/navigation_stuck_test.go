package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// navigation_stuck_test.go - C-3: PathNavigation.doStuckDetection + timeoutPath, ported 1:1.
//
// These tests drive doStuckDetection directly (not through the async pipeline) so the two
// independent behaviors - the per-node timeout and the 100-tick progress check - are exercised in
// isolation with exact, deterministic inputs matched against the javap formulas:
//
//	timeoutLimit = distanceToNextNode / getSpeed * 20.0    (fires when timeoutTimer > timeoutLimit*3)
//	progress stuck iff distanceMovedSqr < (getSpeedFactor * 100 * 0.25)^2  (every 100 ticks)
//
// doStuckDetection does NOT bump tickCount (tick does), so a test that never advances tickCount
// past navStuckCheckInterval keeps the progress check dormant and isolates the timeout path.

func TestNavigationTimeoutLimitFormula(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 0.5, float64(floorY+1), 0.5)
	e.ai = &mobAI{}
	loop.only().entities.add(e)
	nav := &e.ai.navigation
	nav.speed = 0.2

	// Next node at (10, floorY+1, 0) -> atBottomCenterOf = (10.5, floorY+1, 0.5); mob at
	// (0.5, floorY+1, 0.5) -> distance exactly 10.
	nav.path = &Path{nodes: []*node{newNode(10, floorY+1, 0)}, idx: 0}

	loop.gametime = 0
	nav.doStuckDetection(loop, e)

	wantDist := 10.0
	wantLimit := wantDist / nav.speed * navTimeoutSpeedScale // 10/0.2*20 = 1000
	if nav.timeoutLimit != wantLimit {
		t.Fatalf("timeoutLimit=%v, want %v", nav.timeoutLimit, wantLimit)
	}
	if nav.timeoutTimer != 0 {
		t.Fatalf("timeoutTimer=%d on a fresh node, want 0", nav.timeoutTimer)
	}
	if nav.timeoutCachedNode != [3]int{10, floorY + 1, 0} {
		t.Fatalf("timeoutCachedNode=%v", nav.timeoutCachedNode)
	}
}

func TestNavigationTimeoutStopsBlockedPath(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 0.5, float64(floorY+1), 0.5)
	e.ai = &mobAI{}
	loop.only().entities.add(e)
	nav := &e.ai.navigation
	nav.speed = 0.2
	nav.path = &Path{nodes: []*node{newNode(10, floorY+1, 0)}, idx: 0} // distance 10 -> limit 1000

	loop.gametime = 0
	nav.doStuckDetection(loop, e)
	limit := nav.timeoutLimit
	if limit != 1000 {
		t.Fatalf("precondition: timeoutLimit=%v, want 1000", limit)
	}

	// The mob never moves (same next node every call) so timeoutTimer accumulates the game-time
	// delta each call. Advance gametime in 500-tick steps; the path times out once the accumulator
	// passes limit*3 = 3000, and not before.
	threshold := limit * navTimeoutMultiplier // 3000
	timedOut := false
	for step := 1; step <= 20 && !timedOut; step++ {
		loop.gametime = int64(step) * 500
		nav.doStuckDetection(loop, e)
		if nav.path == nil {
			timedOut = true
			if int64(step)*500 <= int64(threshold) {
				t.Fatalf("timed out too early at gametime %d (threshold %v)", step*500, threshold)
			}
		}
	}
	if !timedOut {
		t.Fatalf("a blocked path never timed out")
	}
	if nav.path != nil {
		t.Fatalf("timeoutPath did not stop() the path")
	}
	if nav.timeoutTimer != 0 || nav.timeoutLimit != 0 || nav.timeoutCachedNode != [3]int{} {
		t.Fatalf("timeoutPath did not resetStuckTimeout: timer=%d limit=%v cached=%v",
			nav.timeoutTimer, nav.timeoutLimit, nav.timeoutCachedNode)
	}
}

func TestNavigationTimeoutAccumulatesOnlyWhileNodeUnchanged(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 0.5, float64(floorY+1), 0.5)
	e.ai = &mobAI{}
	loop.only().entities.add(e)
	nav := &e.ai.navigation
	nav.speed = 0.2

	// Multi-node path; the mob reaches each node (advance the index between calls) so the cached
	// node changes every call and the timer never runs away.
	nav.path = &Path{nodes: []*node{
		newNode(1, floorY+1, 0), newNode(2, floorY+1, 0), newNode(3, floorY+1, 0),
		newNode(4, floorY+1, 0), newNode(5, floorY+1, 0),
	}, idx: 0}

	for i := 0; i < 4; i++ {
		loop.gametime = int64(i) * 100000 // huge deltas - would blow any single-node budget
		cur := nav.path.nextNode()
		e.x = float64(cur.x) + 0.5
		e.z = float64(cur.z) + 0.5
		nav.doStuckDetection(loop, e)
		if nav.path == nil {
			t.Fatalf("path timed out on iteration %d despite the next node changing every call", i)
		}
		nav.path.advance()
	}
}

func TestNavigationProgressCheckStuck(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 5.0, float64(floorY+1), 5.0)
	e.ai = &mobAI{}
	loop.only().entities.add(e)
	nav := &e.ai.navigation
	nav.speed = 0.2 // <1 so f = speed*speed = 0.04; threshold f2 = 0.04*100*0.25 = 1.0 block
	nav.path = &Path{nodes: []*node{newNode(20, floorY+1, 5)}, idx: 0}

	// lastStuckCheckPos starts at {0,0,0} (Vec3.ZERO, faithful). The mob at (5,65,5) is far from
	// ZERO, so the FIRST check does NOT flag stuck; it just records the current pos as baseline.
	nav.tickCount = navStuckCheckInterval + 1 // > 100 so the gate opens
	loop.gametime = 1
	nav.doStuckDetection(loop, e)
	if nav.isStuck {
		t.Fatalf("first progress check flagged stuck against the ZERO baseline")
	}
	if nav.path == nil {
		t.Fatalf("first progress check stopped the path (should only baseline)")
	}
	if nav.lastStuckCheckPos != [3]float64{5.0, float64(floorY + 1), 5.0} {
		t.Fatalf("progress check did not record the baseline pos: got %v", nav.lastStuckCheckPos)
	}

	// The mob does NOT move. After another interval, distanceMovedSqr = 0 < threshold^2 = 1.0 ->
	// STUCK: isStuck set and stop() nulls the path.
	nav.tickCount += navStuckCheckInterval + 1
	nav.doStuckDetection(loop, e)
	if !nav.isStuck {
		t.Fatalf("a mob that made no progress in 100 ticks was not flagged stuck")
	}
	if nav.path != nil {
		t.Fatalf("stuck detection did not stop() the path")
	}
}

func TestNavigationProgressCheckMoving(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 5.0, float64(floorY+1), 5.0)
	e.ai = &mobAI{}
	loop.only().entities.add(e)
	nav := &e.ai.navigation
	nav.speed = 0.2 // threshold = 1.0 block
	nav.path = &Path{nodes: []*node{newNode(40, floorY+1, 5)}, idx: 0}

	nav.tickCount = navStuckCheckInterval + 1
	loop.gametime = 1
	nav.doStuckDetection(loop, e)
	base := nav.lastStuckCheckPos

	// The mob moves 5 blocks (>> the 1-block threshold) before the next check.
	e.x = base[0] + 5.0
	nav.tickCount += navStuckCheckInterval + 1
	nav.doStuckDetection(loop, e)
	if nav.isStuck {
		t.Fatalf("a mob that moved 5 blocks in 100 ticks was wrongly flagged stuck")
	}
	if nav.path == nil {
		t.Fatalf("a progressing mob had its path stopped")
	}
}

func TestNavigationResetStuckTimeout(t *testing.T) {
	nav := &groundNavigation{
		timeoutCachedNode: [3]int{3, 4, 5},
		timeoutTimer:      1234,
		timeoutLimit:      99.5,
		isStuck:           true,
	}
	nav.resetStuckTimeout()
	if nav.timeoutCachedNode != [3]int{} || nav.timeoutTimer != 0 || nav.timeoutLimit != 0 || nav.isStuck {
		t.Fatalf("resetStuckTimeout left state dirty: cached=%v timer=%d limit=%v stuck=%v",
			nav.timeoutCachedNode, nav.timeoutTimer, nav.timeoutLimit, nav.isStuck)
	}
}
