package server

import (
	"sync/atomic"
	"testing"
)

// region_coordinator_test.go proves the Phase-27 STEP-2 coordinator invariants (the conc
// fan-out/barrier at N=1): the cross-region post-phase runs AFTER every region tick joins
// (TestCoordinatorBarrier), the shared gametime advances EXACTLY once per tick with the region
// observing the pre-advance value (TestSharedGameTimeAdvancedOnce), and a region whose tick panics
// is isolated — recovered+logged, the tick survives, gametime still advances (TestRegionPanicIsolated,
// the T-27-02 DoS-isolation proof). These join the UNCHANGED full suite that proves behavior-neutral
// at N=1 (TestTickPhaseOrder et al.).

// TestSharedGameTimeAdvancedOnce asserts the coordinator advances the shared 50ms gametime anchor
// EXACTLY once per tickOnce (T-27-02-GT / Pitfall 3), and that the region observes `gt` == the
// PRE-advance gametime during its tick (the region reads the anchor read-only and never advances
// it). Run several ticks to confirm a strict +1 per tick with no drift.
func TestSharedGameTimeAdvancedOnce(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Capture the gt the region observed during its tick via the test hook, and the gametime
	// before the advance, to assert: region sees pre-advance value, coordinator advances by 1.
	for i := int64(0); i < 5; i++ {
		before := loop.gametime
		var observed int64 = -1
		loop.only().tickHook = func() { observed = loop.gametime }

		loop.tickOnce()

		if observed != before {
			t.Fatalf("tick %d: region observed gametime=%d during its tick; want the PRE-advance value %d (the region must read the shared anchor read-only)", i, observed, before)
		}
		if loop.gametime != before+1 {
			t.Fatalf("tick %d: gametime advanced by %d; want EXACTLY 1 per tick (the coordinator is the single advancer)", i, loop.gametime-before)
		}
	}
	loop.only().tickHook = nil
}

// TestCoordinatorBarrier asserts the cross-region post-phase runs AFTER every region's tick returns
// (the barrier holds — wg.Wait joins before the post-phase reads across regions). A flag is set at
// the end of the region's tick (via the hook firing inside r.tick, on the region goroutine) and the
// post-phase (the tracker) asserts the flag is set before it runs. At N=1 this proves the join
// precedes the post-phase; the same wg.Wait enforces it for N>1 (T-27-02-BAR).
func TestCoordinatorBarrier(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	var regionTicked atomic.Bool
	// The region hook records that the region tick body ran. Because r.tick runs the per-region
	// phases AFTER the hook and wg.Wait joins before the post-phase, observing this flag set from
	// a post-phase probe proves the barrier held.
	loop.only().tickHook = func() { regionTicked.Store(true) }

	// A tracker probe stands in for the cross-region post-phase: tracker.Tick runs AFTER wg.Wait
	// in the coordinator, so when it fires the region must already have ticked (the barrier).
	probe := &barrierProbeTracker{regionTicked: &regionTicked}
	loop.tracker = probe

	loop.tickOnce()

	if !probe.sawRegionTicked {
		t.Fatal("the cross-region post-phase (tracker.Tick) ran BEFORE the region tick joined — the barrier did not hold (wg.Wait must join all regions before the post-phase)")
	}
	loop.only().tickHook = nil
}

// barrierProbeTracker is a test tracker that, when its Tick runs (the cross-region post-phase,
// after wg.Wait), records whether the region had already ticked — proving the barrier ordering.
type barrierProbeTracker struct {
	regionTicked    *atomic.Bool
	sawRegionTicked bool
}

func (p *barrierProbeTracker) Tick() { p.sawRegionTicked = p.regionTicked.Load() }

// TestRegionPanicIsolated is the T-27-02 proof: a region whose tick panics is recovered (conc
// re-raises the panic on the coordinator goroutine via wg.Wait, where the hoisted recoverTick
// backstop catches it). The tick does NOT hang — it returns — the loop survives, and gametime
// STILL advances by exactly 1 (the anchor never stalls). One bad region must never freeze the
// world / disconnect every player.
func TestRegionPanicIsolated(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	before := loop.gametime
	// Inject a region whose tick panics (via the test hook, on the region goroutine). conc's
	// WaitGroup recovers it per region and re-raises on Wait(); recoverTick catches it.
	loop.only().tickHook = func() { panic("simulated region tick panic") }

	// tickOnce must NOT propagate the panic (it is recovered) and must NOT hang.
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.tickOnce() // recoverTick swallows the re-raised region panic
	}()
	<-done // if this blocked forever the tick hung — the test would time out (the DoS we prevent)

	loop.only().tickHook = nil

	// The anchor still advanced exactly once despite the region panic (the loop survived).
	if loop.gametime != before+1 {
		t.Fatalf("after a region panic: gametime=%d, want %d (the anchor must still advance EXACTLY once — the panic path must not stall NOR double-advance)", loop.gametime, before+1)
	}

	// And the loop is still usable: a subsequent clean tick advances normally.
	loop.tickOnce()
	if loop.gametime != before+2 {
		t.Fatalf("the loop did not survive the region panic: gametime=%d, want %d", loop.gametime, before+2)
	}
}
