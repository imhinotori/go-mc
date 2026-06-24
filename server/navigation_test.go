package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

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
	loop.entities.add(e)

	var nav groundNavigation
	nav.speed = 0.2
	nav.requestPath(loop, e, 10, floorY+1, 1) // path east along the floor

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
	loop.entities.add(e)

	var nav groundNavigation
	nav.speed = 0.25
	nav.requestPath(loop, e, 20, floorY+1, 8) // walk east across the chunk boundary (x=16)

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
	for _, n := range loop.entities.near(e.x, e.z, 0) {
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
	loop.entities.add(e)

	var nav groundNavigation
	nav.speed = 0.2
	nav.requestPath(loop, e, 9, floorY+1, 2)
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
	loop.entities.add(e)

	startX := e.x
	for i := 0; i < 400; i++ {
		ai.serverAiStep(loop, e)
		loop.tickPhysics()
	}

	if e.x <= startX+2.0 {
		t.Fatalf("serverAiStep did not walk the mob toward the goal's wantTarget: x=%v (start %v)", e.x, startX)
	}
}
