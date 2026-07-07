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
		// 2000 ticks: the C-1 Mob.serverAiStep decimation runs RandomStrollGoal.canUse (the re-roll
		// gate) only every OTHER tick, so the mean real-tick gap between stroll fires roughly DOUBLES
		// (nextInt(60) rolled every 2 ticks). A 600-tick window is then too short to reliably clear a
		// legitimate (vanilla-faithful) dry spell; 2000 ticks gives many re-roll fires so a PERMANENT
		// wedge (the bug under test) is unambiguously distinguishable from an unlucky dry spell.
		ticks  = 2000
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
			// A REALISTIC ground column (surface floorY down through several strata), not a single
			// floating plane. Phase 30.1: the faithful vanilla stroll target selection
			// (LandRandomPos/DefaultRandomPos.getPos) requires terrain DEPTH — a candidate is only a
			// valid destination when the block directly BELOW it is solid (PathNavigation
			// .isStableDestination). A 1-block floating floor makes that true only at the single surface
			// Y, so almost every candidate is rejected and the pig (faithfully) rarely finds a stroll
			// target — an artifact of an unrealistic world, not the wedge bug under test. A multi-layer
			// ground column (as any real/superflat world has) lets the snap find+ground-snap candidates
			// the way vanilla does, so this test exercises the actual fix (reachable walkable target +
			// arrival re-roll) rather than the thin-floor artifact.
			for y := floorY - 8; y <= floorY; y++ {
				fillFloor(ch, y)
			}
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
	// Vanilla RandomStrollGoal re-rolls a reachable target periodically, so the mob is never motionless
	// for the whole run. Pre-fix (the Phase-30.1 bug), getPosition returned an unreachable underground Y
	// and the mob jammed FOREVER (maxStuck == ticks). Post-fix, it ambles and visits many distinct
	// columns. The threshold is keyed to the C-1 decimated cadence: with Mob.serverAiStep running the
	// re-roll gate every OTHER tick, the mean real-tick gap between fires is ~2× the raw interval, and a
	// legitimate dry spell can reach several hundred ticks. strollDefaultInterval*6 (720) comfortably
	// clears the longest observed vanilla-faithful dry spell yet is FAR below a permanent wedge over the
	// 2000-tick window — so a true "never re-rolls" regression (maxStuck→2000) still trips it.
	const maxAcceptableStuck = strollDefaultInterval * 6 // ~6 raw intervals (decimation ~2×s the gap)
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
