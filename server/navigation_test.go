package server

import (
	"runtime"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// drainAsyncPath spins the OWNER's applyAsyncResults drain until the off-tick pathPool worker has
// sent its pathReady onto asyncIn2 and the owner has applied it (nav.pending cleared) — or the
// bound is exhausted. The pool worker is a real goroutine, so we yield between drains to let it
// run and enqueue its result; the drain itself stays on this (owner) goroutine, mirroring how
// applyAsyncResults runs in the real pipeline. Returns true if pending cleared within the bound.
func drainAsyncPath(loop *TickLoop, nav *groundNavigation, bound int) bool {
	for i := 0; i < bound; i++ {
		loop.applyAsyncResults()
		if !nav.pending {
			return true
		}
		runtime.Gosched()
	}
	loop.applyAsyncResults()
	return !nav.pending
}

// navigation_test.go — AI-02: the ported GroundPathNavigation path-following + the serverAiStep
// wiring. The mob WALKS its A* path via the EXISTING moveEntity (per-axis swept collision +
// re-bucket), so it never clips a wall and the tracker's near() always sees it (Pitfall 5).
//
// The tests reuse the physics harness (newPhysicsLoop/putChunk/fillFloor/setBlock) to build a
// small loaded world with a flat stone floor, place a Pig on it, and drive ticks.

// TestNavigationFollow: a mob with a Path on a flat floor steps toward each waypoint via
// moveEntity, advancing the node index, until its x/z converge on the target.
func TestNavigationFollow(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY) // top surface at floorY+1 = 65

	e := testEntity(1, entity.Pig, 1.5, float64(floorY+1), 1.5)
	e.ai = &mobAI{} // the nav lives on the mob's AI so the async rejoin (applyTo) can reach it
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	nav.speed = 0.2
	nav.requestPath(loop, e, 10, floorY+1, 1) // path east along the floor (now computed OFF-tick)

	// OPT-01: the path is computed off-tick and rejoins via applyAsyncResults — drain until it lands.
	if !drainAsyncPath(loop, nav, 1000) {
		t.Fatalf("the async path never rejoined")
	}
	if nav.path == nil || len(nav.path.nodes) == 0 {
		t.Fatalf("requestPath produced no path on a flat floor")
	}

	for i := 0; i < 400 && !nav.path.done(); i++ {
		nav.tick(loop, e)
		loop.tickPhysics() // gravity settles the post-move y
	}

	// The mob must have walked east toward x≈10 (within a block) and not be stuck at spawn.
	if e.x < 8.0 {
		t.Fatalf("mob did not walk toward the target: x=%v (want ~10)", e.x)
	}
}

// TestNavigationMovesViaMoveEntity: navigation.tick moves the mob through moveEntity, so the
// entity re-buckets (the store's near() still sees it at its new column) and it does not clip a
// wall. Assert the post-move bucket is consistent (Pitfall 5 / T-7-05).
func TestNavigationMovesViaMoveEntity(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	// Two adjacent chunks so a walk can cross a column boundary and re-bucket.
	ch2 := putChunk(mgr, level.ChunkPos{1, 0})
	const floorY = 64
	fillFloor(ch, floorY)
	fillFloor(ch2, floorY)

	e := testEntity(1, entity.Pig, 14.5, float64(floorY+1), 8.5)
	e.ai = &mobAI{} // the nav lives on the mob's AI so the async rejoin (applyTo) can reach it
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	nav.speed = 0.25
	nav.requestPath(loop, e, 20, floorY+1, 8) // walk east across the chunk boundary (x=16)

	// OPT-01: the path rejoins off-tick via applyAsyncResults — drain until it lands before walking.
	if !drainAsyncPath(loop, nav, 1000) {
		t.Fatalf("the async path never rejoined")
	}

	startCol := columnOf(e.x, e.z)
	for i := 0; i < 400 && !nav.path.done(); i++ {
		nav.tick(loop, e)
		loop.tickPhysics()
	}

	// The mob crossed the chunk boundary; its bucket must reflect the NEW column (near() at the
	// new position must return it — the re-bucket contract moveEntity enforces).
	newCol := columnOf(e.x, e.z)
	if newCol == startCol {
		t.Fatalf("mob did not cross the chunk boundary (col still %v); cannot assert re-bucket", startCol)
	}
	found := false
	for _, n := range loop.only().entities.near(e.x, e.z, 0) {
		if n.id == e.id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("after moving via moveEntity, the mob is not in its current column's bucket (stale bucket)")
	}
}

// TestRequestPathBuildsSnapshot: requestPath builds a snapshot region from the world and sets a
// Path for a reachable target (and no path / a partial for an unreachable one) — the seam call.
func TestRequestPathBuildsSnapshot(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 2.5, float64(floorY+1), 2.5)
	e.ai = &mobAI{} // the nav lives on the mob's AI so the async rejoin (applyTo) can reach it
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	nav.speed = 0.2
	nav.requestPath(loop, e, 9, floorY+1, 2)
	// OPT-01: the path is built off-tick and rejoins via applyAsyncResults — drain until it lands.
	if !drainAsyncPath(loop, nav, 1000) {
		t.Fatalf("the async path never rejoined")
	}
	if nav.path == nil || len(nav.path.nodes) == 0 {
		t.Fatalf("requestPath did not set a path for a reachable target")
	}
	// The path's first node is the mob's current block column.
	if nav.path.nodes[0].x != 2 || nav.path.nodes[0].z != 2 {
		t.Fatalf("path must start at the mob block (2,2); got (%d,%d)", nav.path.nodes[0].x, nav.path.nodes[0].z)
	}
}

// fixedTargetGoal is a deterministic MOVE goal for the serverAiStep integration test: on start
// it sets a CONCRETE wantTarget and never changes it, so the goal→navigation→moveEntity chain can
// be asserted without the randomStrollGoal's random destination (which is non-deterministic and
// would otherwise overwrite a manually-set target each time it (re)starts). It mirrors what a real
// MOVE goal does — start() sets mobAI.wantTarget — but with a fixed, reachable point.
type fixedTargetGoal struct {
	baseGoal
	tx, ty, tz float64
}

func (g *fixedTargetGoal) canUse(_ *TickLoop, _ *Entity) bool { return true }
func (g *fixedTargetGoal) start(_ *TickLoop, e *Entity) {
	e.ai.setWantTarget(g.tx, g.ty, g.tz)
}

// TestServerAiStepWalksToGoalTarget: a mobAI whose (deterministic) MOVE goal sets a wantTarget
// drives serverAiStep to call navigation.requestPath then navigation.tick, so the mob WALKS toward
// the wantTarget over several ticks (goal + navigation integrated). A real Pig's stroll goal is
// random; this test uses fixedTargetGoal so the destination — and thus the assertion — is
// deterministic. The full chain under test is serverAiStep → goalSelector → setWantTarget →
// requestPath (snapshot→computePath) → navigation.tick → moveEntity.
func TestServerAiStepWalksToGoalTarget(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 2.5, float64(floorY+1), 8.5)
	ai := &mobAI{}
	ai.navigation.speed = 0.2
	ai.goals.addGoal(0, &fixedTargetGoal{
		baseGoal: newBaseGoal(flagMove),
		tx:       11.5, ty: float64(floorY + 1), tz: 8.5, // reachable point east along the floor
	})
	e.ai = ai
	loop.only().entities.add(e)

	startX := e.x
	for i := 0; i < 400; i++ {
		ai.serverAiStep(loop, e)
		loop.tickPhysics()
		// OPT-01: serverAiStep now SUBMITS the path off-tick; applyAsyncResults is the pipeline
		// phase that rejoins it (it runs after tickAI/tickPhysics each tick in the live loop). The
		// test drives it here so the late path lands and the mob follows it (paths 1+ ticks late).
		loop.applyAsyncResults()
	}

	if e.x <= startX+2.0 {
		t.Fatalf("serverAiStep did not walk the mob toward the goal's wantTarget: x=%v (start %v)", e.x, startX)
	}
}

// --- OPT-01: the async pathfinding swap (08-02) ---------------------------------------------
//
// requestPath now SUBMITS the pure computePath to the Wave-0 pathPool and returns immediately —
// the mob keeps its action while the A* runs off-tick. The result rejoins as pathReady via the
// UNCHANGED applyAsyncResults seam, where pathReady.applyTo re-validates the mob still exists and
// the target is unchanged before adopting the late path. These tests pin that behavior: a path
// rejoins and is followed, a despawned/retargeted late path is DROPPED (no crash, no wrong walk),
// a pool-overload submit leaves the mob on its last action, and the recompute cooldown survives.

// TestAsyncPathRejoins: requestPath submits the path compute off-tick and returns immediately
// (pending=true, path NOT set inline). After the pool worker runs and applyAsyncResults drains the
// result on the owner, the mob's nav.path is the valid computed path (the same computePath would
// have produced inline) and pending is cleared. The mob then follows it across ticks.
func TestAsyncPathRejoins(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 1.5, float64(floorY+1), 1.5)
	e.ai = &mobAI{}
	e.ai.navigation.speed = 0.2
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	nav.requestPath(loop, e, 10, floorY+1, 1) // path east along the floor

	// The compute moved OFF-tick: requestPath returned immediately with pending set and NO inline
	// path. (A nil path here is the contract — the path arrives via applyAsyncResults, 1+ ticks late.)
	if !nav.pending {
		t.Fatalf("requestPath must set pending=true after submitting the async compute")
	}
	if nav.path != nil {
		t.Fatalf("requestPath must NOT set the path inline (the executor moved off-tick); got a path")
	}

	if !drainAsyncPath(loop, nav, 1000) {
		t.Fatalf("the async path never rejoined (pending still true after draining)")
	}
	if nav.path == nil || len(nav.path.nodes) == 0 {
		t.Fatalf("after rejoin the mob has no path (the async compute produced nothing)")
	}
	if nav.path.nodes[0].x != 1 || nav.path.nodes[0].z != 1 {
		t.Fatalf("rejoined path must start at the mob block (1,1); got (%d,%d)", nav.path.nodes[0].x, nav.path.nodes[0].z)
	}

	// The mob follows the late path: walk it across ticks and assert it moves east.
	for i := 0; i < 400 && !nav.path.done(); i++ {
		nav.tick(loop, e)
		loop.tickPhysics()
	}
	if e.x < 8.0 {
		t.Fatalf("mob did not follow the rejoined async path east: x=%v (want ~10)", e.x)
	}
}

// TestAsyncPathDespawnedDropped: submit a path for a mob, then REMOVE the mob from the store, then
// drain the late result — pathReady.applyTo must DROP it (no panic / nil-deref) because the mob no
// longer exists. A path is never assigned to a despawned mob.
func TestAsyncPathDespawnedDropped(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(7, entity.Pig, 1.5, float64(floorY+1), 1.5)
	e.ai = &mobAI{}
	e.ai.navigation.speed = 0.2
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	nav.requestPath(loop, e, 10, floorY+1, 1)

	// The mob despawns while the path computes — remove it BEFORE the result lands.
	loop.only().entities.remove(e.id)

	// Drain: applyTo must take the despawn-drop path. No panic, and the (now-orphaned) nav.path
	// must remain nil (nothing was adopted onto a non-existent mob).
	for i := 0; i < 200; i++ {
		loop.applyAsyncResults()
		runtime.Gosched()
	}
	if nav.path != nil {
		t.Fatalf("a path for a despawned mob must be DROPPED, not adopted; got a path")
	}
}

// TestAsyncPathRetargetedDropped: submit a path for target A, then RETARGET the nav to B (a new
// requestPath), then drain the stale A result — applyTo must DROP it (target mismatch). The mob
// keeps the B request, never the stale A path.
func TestAsyncPathRetargetedDropped(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(3, entity.Pig, 1.5, float64(floorY+1), 1.5)
	e.ai = &mobAI{}
	e.ai.navigation.speed = 0.2
	loop.only().entities.add(e)

	nav := &e.ai.navigation

	// Manually craft a stale result for an OLD target A (10,_,1) and enqueue it directly, then set
	// the nav's current target to B (5,_,5) via the tracking fields. applyTo must drop the A result
	// because nav.lastT* now points at B.
	nav.lastTX, nav.lastTY, nav.lastTZ = 5, floorY+1, 5
	nav.hasTarget = true
	nav.pending = true
	staleA := &Path{nodes: []*node{newNode(1, floorY+1, 1)}, idx: 0}
	loop.asyncIn2 <- pathReady{mobID: e.id, target: [3]int{10, floorY + 1, 1}, path: staleA}

	loop.applyAsyncResults()

	if nav.path == staleA {
		t.Fatalf("a path for a stale (retargeted) goal must be DROPPED; the mob adopted the stale A path")
	}
	if nav.path != nil {
		t.Fatalf("retargeted-drop must not assign any path; got a non-nil path")
	}
}

// TestAsyncPathPoolOverloadDrops: with a SATURATED pathPool, requestPath leaves pending=false and
// does not touch the mob's existing path (the request is dropped, re-requested next tick) — and it
// never blocks. Mirrors world.Worker.Request's drop-on-full backpressure (Pitfall 4).
func TestAsyncPathPoolOverloadDrops(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	// Replace the path pool with a single-worker pool we can saturate deterministically.
	loop.pathPool.Release()
	loop.pathPool = newAsyncPool(1)
	block := make(chan struct{})
	started := make(chan struct{})
	if !submitOrDrop(loop.pathPool, func() { close(started); <-block }) {
		t.Fatal("priming submit into the empty single-worker pool should be accepted")
	}
	<-started // the only worker is now occupied; the pool is saturated

	e := testEntity(4, entity.Pig, 1.5, float64(floorY+1), 1.5)
	e.ai = &mobAI{}
	e.ai.navigation.speed = 0.2
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	existing := &Path{nodes: []*node{newNode(1, floorY+1, 1)}, idx: 0}
	nav.path = existing // the mob's last path

	nav.requestPath(loop, e, 10, floorY+1, 1) // pool saturated → must DROP

	if nav.pending {
		t.Fatalf("a dropped (pool-overloaded) submit must leave pending=false; got pending=true")
	}
	if nav.path != existing {
		t.Fatalf("a dropped submit must NOT touch the mob's existing path; it was replaced")
	}

	close(block) // release the occupying worker so Release drains cleanly
}

// TestAsyncPathCooldownPreserved: the recompute cooldown still throttles re-requests for the same
// unchanged target (the DoS guard survived the executor swap). Immediately after a submit, the
// same-target request is throttled (no new submit fires while the cooldown is hot / a request is
// in flight).
func TestAsyncPathCooldownPreserved(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(5, entity.Pig, 1.5, float64(floorY+1), 1.5)
	e.ai = &mobAI{}
	e.ai.navigation.speed = 0.2
	loop.only().entities.add(e)

	nav := &e.ai.navigation
	nav.requestPath(loop, e, 10, floorY+1, 1)

	// Same target, immediately after: the cooldown is hot AND a request is pending — shouldRecompute
	// must return false so the A* is not flooded for an unchanged (possibly unreachable) target.
	if nav.shouldRecomputePath(10, floorY+1, 1) {
		t.Fatalf("the recompute cooldown/pending gate must throttle a same-target re-request right after a submit")
	}
}
