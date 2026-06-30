package server

import (
	"runtime"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
)

// TestPigStrollsWithoutWedging is the Phase 30.1 regression: a pig on flat ground must amble
// freely, not walk a few blocks and then jam against a block forever. It drives the FULL tick
// (loop.advance via a fake clock) so the async A* path actually rejoins and applies — hand-calling
// tickPhysics alone never drains asyncIn2, so the mob would falsely look frozen. FAILS pre-fix
// (getPosition returns an unreachable underground Y), PASSES once LandRandomPos.getPos is ported.
func TestPigStrollsWithoutWedging(t *testing.T) {
	const (
		floorY = 63
		startY = 64.0
		x, z   = 8.5, 8.5
		ticks  = 600
	)
	clock := newFakeClock()
	loop := NewTickLoop(clock)
	mgr := world.NewChunkManager()
	for _, r := range loop.regions {
		r.world = mgr
	}
	installVanillaPigRegistry(loop)
	loop.start(clock.Now())

	for cx := -2; cx <= 2; cx++ {
		for cz := -2; cz <= 2; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, floorY)
		}
	}
	pig := NewEntity(4242, entity.Pig, x, startY, z)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.onGround = true
	loop.only().entities.add(pig)

	lastX, lastZ := pig.x, pig.z
	stuckRun, maxStuck := 0, 0
	seen := map[[2]int]bool{}
	for i := 0; i < ticks; i++ {
		runtime.Gosched() // let the off-tick pathPool goroutine run (real-time the server always does)
		clock.add(50 * time.Millisecond)
		loop.advance(clock.Now())

		seen[[2]int{int(pig.x * 4), int(pig.z * 4)}] = true
		moved := (pig.x-lastX)*(pig.x-lastX) + (pig.z-lastZ)*(pig.z-lastZ)
		if moved < 1e-6 {
			stuckRun++
			if stuckRun > maxStuck {
				maxStuck = stuckRun
			}
		} else {
			stuckRun = 0
		}
		if i < 80 || i%20 == 0 {
			nav := &pig.ai.navigation
			hasPath := nav.path != nil && !nav.path.done()
			t.Logf("tick %3d: pos=(%.3f,%.3f,%.3f) onGround=%v hasTarget=%v active=%v hasPath=%v want=(%.1f,%.1f,%.1f) stuckRun=%d",
				i, pig.x, pig.y, pig.z, pig.onGround, pig.ai.hasTarget, nav.active(), hasPath,
				pig.ai.wantX, pig.ai.wantY, pig.ai.wantZ, stuckRun)
		}
		lastX, lastZ = pig.x, pig.z
	}
	t.Logf("DONE: final pos=(%.3f,%.3f,%.3f) longest motionless run=%d distinct quarter-positions=%d",
		pig.x, pig.y, pig.z, maxStuck, len(seen))

	// REGRESSION ASSERTION (Phase 30.1): a pig strolling on flat ground must NOT wedge permanently.
	// Vanilla RandomStrollGoal re-rolls a reachable target every ~interval ticks, so the mob is never
	// motionless for hundreds of ticks. Pre-fix, getPosition returns an unreachable underground Y and
	// the mob jams forever (maxStuck == ticks). Post-fix, it ambles and visits many distinct columns.
	const maxAcceptableStuck = strollDefaultInterval * 3 // generous: ~3 re-roll intervals
	if maxStuck > maxAcceptableStuck {
		t.Fatalf("pig wedged: longest motionless run=%d ticks (> %d) — RandomStrollGoal picked an "+
			"unreachable target and never re-rolled (see Phase 30.1: LandRandomPos.getPos ground-snap)",
			maxStuck, maxAcceptableStuck)
	}
	if len(seen) < 8 {
		t.Fatalf("pig barely moved: only %d distinct quarter-positions visited over %d ticks — "+
			"expected free wandering", len(seen), ticks)
	}
}
