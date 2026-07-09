package server

// fox_eat_berries_test.go — the Fox CHARACTER-LAYER FoxEatBerriesGoal tests (1:1 jar port).
// Each test drives the goal methods directly with a deterministic per-entity rng (reseeded by id)
// and asserts the jar-faithful gating + transitions. The pig oracle is byte-identically untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// foxEatBerriesWorldWithBush is a test fixture: a tick loop with a single SWEET_BERRY_BUSH at (bx, by,
// bz) with the given age (0..3) and a fox entity registered. The bush is a real placed block in
// the world (so the goal's validTarget + onReachedTarget can read/mutate it via the world
// ChunkManager). The fox is at (fx, fy, fz).
func foxEatBerriesWorldWithBush(t *testing.T, bushAge int, bx, by, bz, fx, fy, fz int) (*TickLoop, *Entity) {
	t.Helper()
	loop, _ := newPhysicsLoop()
	loop.start(loop.clock.(*fakeClock).Now())
	loop.spawnSurfaceY = 200
	// Insert a loaded chunk at (0,0) so SetBlock/GetBlock work on the bush position.
	if w := loop.world(); w != nil {
		readyOverworldChunk(w)
		// Place the bush. block.ToStateID[SweetBerryBush{Age: Integer(age)}] is the canonical state id.
		sid, ok := block.ToStateID[block.SweetBerryBush{Age: block.Integer(bushAge)}]
		if !ok {
			t.Fatalf("SweetBerryBush age=%d has no state id", bushAge)
		}
		if !w.SetBlock(pk.Position{X: bx, Y: by, Z: bz}, sid, dimMinY) {
			t.Fatalf("failed to place sweet_berry_bush at (%d,%d,%d)", bx, by, bz)
		}
	}
	fox := foxTestMob(int32(fx*1000+bx*100+by*10+bz), float64(fx), float64(fy), float64(fz))
	fox.typ = entity.Fox.ID // make sure the fox is a Fox wire type (foxTestMob sets it)
	loop.only().entities.add(fox)
	return loop, fox
}

func TestFoxEatBerriesTargetsRipeBush(t *testing.T) {
	loop, fox := foxEatBerriesWorldWithBush(t, 2, 0, 64, 0, 5, 64, 0)
	g := newFoxEatBerriesGoal()
	// canUse should fire: there's a ripe bush (AGE 2) within searchRange=12 blocks, the fox is
	// at (5, 64, 0) — within 12 blocks of the bush at (0, 64, 0).
	if !g.canUse(loop, fox) {
		t.Fatal("FoxEatBerriesGoal.canUse did not fire on a ripe (AGE 2) bush within 12 blocks")
	}
	// start() should commit the bush pos as the nav want.
	g.start(loop, fox)
	if !fox.ai.hasTarget {
		t.Fatal("FoxEatBerriesGoal.start should set a want-target toward the bush")
	}
}

func TestFoxEatBerriesRejectsUnripeBush(t *testing.T) {
	// AGE 0 (no berries) + AGE 1 (just flowered) are both NOT valid targets — the jar's
	// isValidTarget gate rejects age < 2.
	for _, age := range []int{0, 1} {
		loop, fox := foxEatBerriesWorldWithBush(t, age, 0, 64, 0, 5, 64, 0)
		g := newFoxEatBerriesGoal()
		if g.canUse(loop, fox) {
			t.Fatalf("FoxEatBerriesGoal.canUse fired on an unripe (AGE %d) bush", age)
		}
	}
}

func TestFoxEatBerriesOutOfRangeBush(t *testing.T) {
	// Bush at (0, 64, 0), fox at (50, 64, 0) — 50 blocks away, beyond the 12-block search range.
	loop, fox := foxEatBerriesWorldWithBush(t, 2, 0, 64, 0, 50, 64, 0)
	g := newFoxEatBerriesGoal()
	// canUse's findNearestBlock scans the searchRange ring; a 50-block gap has no candidate.
	if g.canUse(loop, fox) {
		t.Fatal("FoxEatBerriesGoal.canUse fired for a bush 50 blocks away (out of 12-block range)")
	}
}

func TestFoxEatBerriesDamageAfter(t *testing.T) {
	// Fox reaches the bush, the WAIT_TICKS (40) elapses, the fox eats — the bush AGE drops to 1.
	loop, fox := foxEatBerriesWorldWithBush(t, 3, 0, 64, 0, 1, 64, 0) // AGE 3 ripe
	g := newFoxEatBerriesGoal()
	if !g.canUse(loop, fox) {
		t.Fatal("canUse should fire for a ripe (AGE 3) bush")
	}
	g.start(loop, fox)
	// The MoveToBlockGoal.moveMobToBlock sets the nav want to (blockPos.X+0.5, blockPos.Y+1,
	// blockPos.Z+0.5) — the fox stands ON the bush (feet at blockPos.above()). Place the fox
	// at that exact stand position to flip reachedTarget.
	fox.x = 0.5
	fox.y = 65.0 // feet at blockPos.above() == y=65 (the bush top is y=64, stand cell y=65)
	fox.z = 0.5
	// Drive the tick to flip reachedTarget. The base moveToBlockGoal.tick reads the center
	// (mt.X+0.5, mt.Y+0.5, mt.Z+0.5) = (0.5, 65.5, 0.5); the fox is at (0.5, 65, 0.5). The
	// squared distance is 0.25 < 1.0 — reachedTarget flips true.
	g.tick(loop, fox)
	if !g.reachedTarget {
		t.Fatalf("tick did not flip reachedTarget for a fox standing on the bush: pos=(%v,%v,%v)", fox.x, fox.y, fox.z)
	}
	// Drive 40 more ticks to elapse WAIT_TICKS, then verify AGE 1.
	for i := 0; i < 40; i++ {
		g.tick(loop, fox)
	}
	s, ok := loop.world().GetBlock(pk.Position{X: 0, Y: 64, Z: 0}, dimMinY)
	if !ok {
		t.Fatal("could not read bush state after eat")
	}
	b, ok := block.StateList[s].(block.SweetBerryBush)
	if !ok {
		t.Fatalf("block at bush pos is not a SweetBerryBush (state=%d)", s)
	}
	if int(b.Age) != foxEatBerriesBerryAge {
		t.Fatalf("after eat, bush AGE = %d, want %d (FoxEatBerriesGoal.pickSweetBerries setValue(AGE, 1))", int(b.Age), foxEatBerriesBerryAge)
	}
	// The fox's mainhand now has a placeholder slot (the pickSweetBerries main-hand-off).
	main := fox.getMainHandItem()
	if main.Count == 0 {
		t.Fatal("fox mainhand should hold a count-1 slot after eating (FoxEatBerriesGoal.pickSweetBerries setMainHand)")
	}
}
