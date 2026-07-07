package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// entity_collision_test.go — B-A3: the entity-entity collision (push shove + cramming) tests. They
// pin the 1:1 port of net.minecraft LivingEntity.pushEntities / Entity.push(Entity):
//
//   - TestPushTwoMobsApart      — two overlapping pigs shove apart by the EXACT vanilla impulse
//                                 (0.05 normalized), symmetrically (equal-and-opposite vx deltas).
//   - TestPushLoneMobNoImpulse  — a lone pig with no neighbour takes ZERO push (the oracle property).
//   - TestCrammingDamage        — 24+ overlapping pigs -> a crowded pig takes 6.0 cramming damage.
//
// All run on the tick-owned store over a hand-built flat world (physics_test.go harness).

// makeLivePig builds a live, alive pig at (x,y,z) with a fresh AI (so mobRandom draws its own stream)
// and full health, added to the loop's only region store. The AI is seeded deterministically off the
// id so the cramming RNG roll is reproducible.
func makeLivePig(loop *TickLoop, id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Pig, x, y, z)
	e.health = 10 // > 0 so isAlive()/isPushable() hold
	e.ai = newPigAI()
	reseedMobAI(e.ai, e.id)
	loop.only().entities.add(e)
	return e
}

// TestPushTwoMobsApart: two pigs overlapping on the X axis (0.2 apart) shove apart by the exact
// Entity.push(Entity) impulse. The pushed mob A (query) gets vx -= impulse and the neighbour gets
// vx += impulse (symmetry). The impulse is recomputed here from the SAME formula the port uses so
// the assertion pins the numeric ops (absMax -> sqrt -> normalize -> clamp -> *0.05).
func TestPushTwoMobsApart(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	// A at x=8.5, other at x=8.7 (dz=0). Boxes (half-width 0.45) span [8.05,8.95] and [8.25,9.15] -> overlap.
	a := makeLivePig(loop, 1, 8.5, float64(floorY+1), 8.5)
	other := makeLivePig(loop, 2, 8.7, float64(floorY+1), 8.5)

	loop.pushNearbyEntities(a)

	// Expected impulse (Entity.push(Entity), the exact double ops):
	dx := other.x - a.x // 0.2
	dz := other.z - a.z // 0.0
	d := math.Max(math.Abs(dx), math.Abs(dz))
	if d < 0.009999999776482582 {
		t.Fatalf("test setup: entities too close (d=%v) — no push would occur", d)
	}
	d = math.Sqrt(d)
	ndx := dx / d
	ndz := dz / d
	inv := 1.0 / d
	if inv > 1.0 {
		inv = 1.0
	}
	ndx *= inv
	ndz *= inv
	ndx *= 0.05000000074505806
	ndz *= 0.05000000074505806

	// A is pushed AWAY from other (negative x, since other is +x of A).
	if a.vx != -ndx || a.vz != -ndz {
		t.Fatalf("A push vector = (%v, %v), want (%v, %v)", a.vx, a.vz, -ndx, -ndz)
	}
	// other is pushed away from A (positive x).
	if other.vx != ndx || other.vz != ndz {
		t.Fatalf("other push vector = (%v, %v), want (%v, %v)", other.vx, other.vz, ndx, ndz)
	}
	// Symmetry: equal-and-opposite horizontal impulse.
	if a.vx != -other.vx || a.vz != -other.vz {
		t.Fatalf("push not symmetric: A=(%v,%v) other=(%v,%v)", a.vx, a.vz, other.vx, other.vz)
	}
	// No vertical impulse (push is horizontal only).
	if a.vy != 0 || other.vy != 0 {
		t.Fatalf("push applied vertical impulse: A.vy=%v other.vy=%v, want 0", a.vy, other.vy)
	}
}

// TestPushLoneMobNoImpulse: a lone pig with NO overlapping pushable neighbour takes ZERO push — the
// pig-oracle property (pushEntities early-outs on the empty list; no impulse, no RNG draw).
func TestPushLoneMobNoImpulse(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	pig := makeLivePig(loop, 1, 8.5, float64(floorY+1), 8.5)
	beforeVX, beforeVY, beforeVZ := pig.vx, pig.vy, pig.vz

	loop.pushNearbyEntities(pig)

	if pig.vx != beforeVX || pig.vy != beforeVY || pig.vz != beforeVZ {
		t.Fatalf("lone mob got pushed: velocity changed to (%v,%v,%v), want (%v,%v,%v)",
			pig.vx, pig.vy, pig.vz, beforeVX, beforeVY, beforeVZ)
	}
}

// TestCrammingDamage: 25 pigs (> maxEntityCramming default 24) piled at the same spot -> a crowded
// pig takes exactly 6.0 cramming damage (LivingEntity.pushEntities: count > max-1 -> hurtServer(
// cramming(), 6.0F)). The cramming roll is random.nextInt(4)==0; driving pushNearbyEntities in a
// bounded loop over the fixed-seed rng deterministically lands the roll within a few calls. The
// invulnerableTime i-frame ensures only ONE 6.0 hit lands (subsequent equal hits apply no excess).
func TestCrammingDamage(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	// 25 pigs at the SAME position (fully overlapping) — list for any query pig = the other 24 (> 23).
	const n = 25
	pigs := make([]*Entity, n)
	for i := 0; i < n; i++ {
		pigs[i] = makeLivePig(loop, int32(i+1), 8.5, float64(floorY+1), 8.5)
	}
	victim := pigs[0]
	startHealth := victim.health

	// The victim sees 24 pushable non-passenger neighbours (> max-1 == 23). Drive the aiStep push slot
	// until the nextInt(4)==0 cramming roll lands (bounded; the fixed seed makes it deterministic).
	landed := false
	for i := 0; i < 100 && !landed; i++ {
		loop.pushNearbyEntities(victim)
		if victim.health < startHealth {
			landed = true
		}
	}
	if !landed {
		t.Fatalf("cramming never dealt damage over 100 push slots (health still %v)", victim.health)
	}
	// Exactly one 6.0 hit: the i-frame window blocks a second equal hit, so health dropped by 6.0.
	if got := startHealth - victim.health; got != crammingDamage {
		t.Fatalf("cramming damage = %v, want %v (one 6.0 hit, i-frame blocks the rest)", got, crammingDamage)
	}
}

// TestCrammingBelowThresholdNoDamage: a small crowd (< maxEntityCramming) never takes cramming
// damage no matter how many push slots run — the count <= max-1 guard holds. Two overlapping pigs
// push apart (velocity) but neither is ever hurt.
func TestCrammingBelowThresholdNoDamage(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	a := makeLivePig(loop, 1, 8.5, float64(floorY+1), 8.5)
	b := makeLivePig(loop, 2, 8.5, float64(floorY+1), 8.5)
	start := a.health

	for i := 0; i < 100; i++ {
		loop.pushNearbyEntities(a)
	}
	if a.health != start {
		t.Fatalf("a took cramming damage below threshold: health %v -> %v", start, a.health)
	}
	_ = b
}