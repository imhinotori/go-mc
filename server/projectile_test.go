package server

// projectile_test.go — PROJECTILE-01 (Task #8): pins the AbstractArrow physics port independently of the
// skeleton goal. Verifies the per-tick order (move → drag 0.99 → gravity 0.05), the ground-stick latch,
// the 1200-tick despawn, and that an arrow whose flight segment passes through a player deals damage.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// arrowLoop builds a physics loop with a stone floor and a chunk, ready to spawn + tick arrows.
func arrowLoop(t *testing.T) (*TickLoop, int, *fakeClock) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, floorY, clock
}

// TestArrowDragAndGravity: a flying arrow (no block, no target) has drag 0.99 then gravity 0.05 applied
// each tick, in that order. After one tick from a purely horizontal launch: vx' = vx*0.99, vy' = -0.05.
func TestArrowDragAndGravity(t *testing.T) {
	loop, floorY, _ := arrowLoop(t)
	// Spawn high above the floor so it never touches ground during the test.
	a := loop.spawnArrow(0, 8.5, float64(floorY+50), 8.5, 1.0, 0.0, 0.0, 2.0)

	loop.tickArrow(a)

	// move happened (x advanced by the pre-drag vx=1.0), then drag then gravity.
	if math.Abs(a.vx-0.99) > 1e-9 {
		t.Fatalf("vx after one tick = %v, want 0.99 (drag 0.99 applied)", a.vx)
	}
	if math.Abs(a.vy-(-0.05)) > 1e-9 {
		t.Fatalf("vy after one tick = %v, want -0.05 (gravity 0.05 applied AFTER drag on a 0 vy start)", a.vy)
	}
	// The arrow advanced one full pre-drag step in x (moved BEFORE drag).
	if math.Abs(a.x-(8.5+1.0)) > 1e-9 {
		t.Fatalf("x after one tick = %v, want 9.5 (moved by pre-drag vx=1.0)", a.x)
	}
}

// TestArrowSticksInGround: an arrow launched into the solid floor latches (inGround, velocity zeroed) and
// stops flying. It then despawns at the 1200-tick lifetime.
func TestArrowSticksInGround(t *testing.T) {
	loop, floorY, _ := arrowLoop(t)
	// Spawn just above the floor moving straight down so it enters the solid floor cell.
	a := loop.spawnArrow(0, 8.5, float64(floorY+1)+0.1, 8.5, 0.0, -2.0, 0.0, 2.0)

	loop.tickArrow(a)
	if !a.arrowInGround {
		t.Fatalf("arrow did not stick in the floor (inGround=%v)", a.arrowInGround)
	}
	if a.vx != 0 || a.vy != 0 || a.vz != 0 {
		t.Fatalf("stuck arrow velocity = (%v,%v,%v), want zero", a.vx, a.vy, a.vz)
	}

	// A grounded arrow counts down to despawn. Fast-forward life to the cap and tick once.
	a.arrowLife = arrowDespawnTicks - 1
	loop.tickArrow(a)
	if _, ok := loop.only().entities.byID[a.id]; ok {
		t.Fatal("grounded arrow was not despawned at life>=1200")
	}
}

// TestArrowHitsPlayer: an arrow whose flight segment passes through a player's AABB deals damage (the
// onHitEntity port) and is consumed. The shooter is a different id so the self-skip does not apply.
func TestArrowHitsPlayer(t *testing.T) {
	loop, floorY, _ := arrowLoop(t)

	p := combatTestPlayer(loop, 10.5, float64(floorY+1), 8.5, 4242)
	start := p.health

	// Launch an arrow toward the player from 3 blocks away at chest height, fast enough that the segment
	// crosses the player's box this tick.
	a := loop.spawnArrow(999, 7.5, float64(floorY+1)+1.0, 8.5, 4.0, 0.0, 0.0, 3.0)
	loop.tickArrow(a)

	if p.health >= start {
		t.Fatalf("player took no arrow damage (health %v >= %v)", p.health, start)
	}
	if _, ok := loop.only().entities.byID[a.id]; ok {
		t.Fatal("arrow was not consumed after hitting the player")
	}
}

// TestArrowNeverHitsShooter: an arrow attributed to a shooter id passes through that shooter's position
// without a self-hit (the checkLeftOwner guard). Modeled by making the ONLY player the shooter.
func TestArrowNeverHitsShooter(t *testing.T) {
	loop, floorY, _ := arrowLoop(t)

	shooter := combatTestPlayer(loop, 10.5, float64(floorY+1), 8.5, 4242)
	start := shooter.health

	// The arrow's shooterID == the player's entityID → the self-skip must apply.
	a := loop.spawnArrow(shooter.entityID, 7.5, float64(floorY+1)+1.0, 8.5, 4.0, 0.0, 0.0, 3.0)
	loop.tickArrow(a)

	if shooter.health < start {
		t.Fatalf("arrow hit its own shooter (health %v < %v) — the self-skip failed", shooter.health, start)
	}
}

// TestArrowCritExtraDamage: a crit arrow (isCritArrow) deals the base ceil damage PLUS a random
// nextInt(damage/2 + 2) bonus (AbstractArrow.onHitEntity offsets 193-230). With the same launch as a
// non-crit arrow, the crit hit must deal STRICTLY MORE (the bonus is 0..damage/2+1, and the +2 floor
// guarantees nextInt(>=2) can exceed 0; over the fixed per-arrow-id stream the bonus here is > 0).
func TestArrowCritExtraDamage(t *testing.T) {
	// Non-crit baseline.
	loopN, floorYN, _ := arrowLoop(t)
	pN := combatTestPlayer(loopN, 10.5, float64(floorYN+1), 8.5, 4242)
	startN := pN.health
	aN := loopN.spawnArrow(999, 7.5, float64(floorYN+1)+1.0, 8.5, 4.0, 0.0, 0.0, 3.0)
	loopN.tickArrow(aN)
	dealtN := startN - pN.health

	// Crit shot, identical launch.
	loopC, floorYC, _ := arrowLoop(t)
	pC := combatTestPlayer(loopC, 10.5, float64(floorYC+1), 8.5, 4242)
	startC := pC.health
	aC := loopC.spawnArrow(999, 7.5, float64(floorYC+1)+1.0, 8.5, 4.0, 0.0, 0.0, 3.0)
	aC.arrowCrit = true
	loopC.tickArrow(aC)
	dealtC := startC - pC.health

	if dealtC <= dealtN {
		t.Fatalf("crit arrow dealt %v, want > non-crit %v (crit bonus nextInt(damage/2 + 2))", dealtC, dealtN)
	}
}
