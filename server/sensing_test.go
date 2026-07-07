package server

// sensing_test.go — behavior tests for the Sensing line-of-sight port (sensing.go, divergence C-4):
//   - LoS is TRUE across open air between two eye positions.
//   - LoS is FALSE through a solid block wall placed between the eyes.
//   - the per-tick cache returns the SAME result WITHOUT recomputing the raycast within one tick,
//     and recomputes across ticks (gametime epoch invalidation).
//   - a melee attack goal does NOT swing when LoS is blocked (canPerformAttack gate).
//
// The world is the physics harness (newPhysicsLoop + putChunk/setBlock): a real ChunkManager whose
// blocks we place by hand, so the DDA/VoxelShape clip runs against actual stone collision shapes.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// sensingTestMob builds a live goal-bearing mob (ai attached, deterministic rng) at (x,y,z), added to
// the loop's region entity store so the tick-owned reads resolve.
func sensingTestMob(loop *TickLoop, id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Zombie, x, y, z)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(defaultEntityRandomSeed)
	reseedMobAI(e.ai, e.id)
	e.health = 20.0
	loop.only().entities.add(e)
	return e
}

// TestLineOfSightOpenAir: with nothing between the mob and the player, hasLineOfSight is TRUE.
func TestLineOfSightOpenAir(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY) // floor at y=64, top 65 — below both eye lines, never on the ray

	e := sensingTestMob(loop, 1, 8.5, 65, 8.5)
	p := addTestPlayer(loop, 9000, 8.5, 65, 12.5) // 4 blocks north, clear air between

	if !loop.sensingHasLineOfSight(e, p) {
		t.Fatal("open-air line of sight must be TRUE (no block between the eye positions)")
	}
}

// TestLineOfSightBlockedByWall: a solid stone wall between the mob and the player blocks LoS (FALSE).
func TestLineOfSightBlockedByWall(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := sensingTestMob(loop, 1, 8.5, 65, 8.5)
	p := addTestPlayer(loop, 9000, 8.5, 65, 12.5)

	// A solid stone column spanning the eye band at z=10 (between mob z=8.5 and player z=12.5),
	// full height across the ray's y range (both eyes near y=66). This is the wall the segment
	// from mob-eye -> player-eye must cross.
	fillWall(ch, 8, 10, 64, 68)

	if loop.sensingHasLineOfSight(e, p) {
		t.Fatal("line of sight through a solid wall must be FALSE (the block clip hits the wall)")
	}
}

// TestSensingPerTickCache: the per-tick memo returns the SAME result WITHOUT recomputing the raycast
// within one tick, and recomputes across ticks. Proven by MUTATING the world between two same-tick
// queries: the first query (open air) memoizes TRUE; placing a wall then querying again in the SAME
// tick must STILL return TRUE (served from the cache, the raycast did not re-run); advancing the
// gametime (Sensing.tick clear analogue) then querying re-runs the raycast and now returns FALSE.
func TestSensingPerTickCache(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := sensingTestMob(loop, 1, 8.5, 65, 8.5)
	p := addTestPlayer(loop, 9000, 8.5, 65, 12.5)

	loop.gametime = 1

	// First query: open air -> TRUE, memoized in `seen` for gametime 1.
	if !loop.sensingHasLineOfSight(e, p) {
		t.Fatal("first query (open air) must be TRUE")
	}

	// Mutate the world: drop a wall between them. If the cache is honored, the SAME-tick query still
	// returns the memoized TRUE (the raycast is NOT re-run despite the wall now existing).
	fillWall(ch, 8, 10, 64, 68)
	if !loop.sensingHasLineOfSight(e, p) {
		t.Fatal("same-tick query after placing a wall must STILL be TRUE (served from the per-tick cache; the raycast must NOT recompute)")
	}

	// Advance the tick (Sensing.tick clears seen/unseen). Now the query recomputes against the wall -> FALSE.
	loop.gametime = 2
	if loop.sensingHasLineOfSight(e, p) {
		t.Fatal("next-tick query must recompute the raycast and see the wall -> FALSE (the per-tick memo was invalidated by the gametime epoch)")
	}
}

// TestAttackGoalRequiresLineOfSight: meleeAttackGoal.canPerformAttack (the swing gate) is FALSE when a
// wall blocks line of sight to an in-reach target, and TRUE when the line is clear — the divergence C-4
// fix (a mob no longer swings through a wall). The cooldown is elapsed (ticksUntilNextAttack 0) and the
// target is inside melee reach in both cases, so LoS is the ONLY differentiator.
func TestAttackGoalRequiresLineOfSight(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := sensingTestMob(loop, 1, 8.5, 65, 8.5)
	// A player just inside melee reach (~1.1 blocks north; reach ≈ 0.828 + half-widths).
	p := addTestPlayer(loop, 9000, 8.5, 65, 9.6)

	g := newMeleeAttackGoal(1.0)
	g.ticksUntilNextAttack = 0 // isTimeToAttack() true

	// Sanity: in-reach precondition holds (so any FALSE below is the LoS gate, not the reach gate).
	if !isWithinMeleeAttackRange(e, p) {
		t.Fatal("test setup: player must be within melee reach so LoS is the sole differentiator")
	}

	// (1) Clear line -> canPerformAttack TRUE (the mob may swing).
	loop.gametime = 1
	if !g.canPerformAttack(loop, e, p) {
		t.Fatal("canPerformAttack must be TRUE with a clear line to an in-reach target")
	}

	// (2) Drop a wall in the cell the eye segment crosses (x=8, z=9, spanning the eye band) and advance
	// the tick so the cache re-runs. Now canPerformAttack must be FALSE — the mob will NOT swing through it.
	fillWall(ch, 8, 9, 64, 68)
	loop.gametime = 2
	if g.canPerformAttack(loop, e, p) {
		t.Fatal("canPerformAttack must be FALSE when a wall blocks line of sight (no swinging through walls, C-4)")
	}
}
