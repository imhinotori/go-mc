package server

import (
	"reflect"
	"testing"
	"time"
)

// fakeClock is a deterministic, caller-advanced Clock. Now() returns a base instant
// plus the accumulated synthetic offset; tests advance it with add(). It carries no
// real wall-clock dependency, so timing assertions never wait or rely on the OS
// scheduler (research Pitfall 3). The base is an arbitrary fixed instant so Sub()
// deltas are well-defined.
type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

// Now returns the current synthetic instant (monotonic non-decreasing).
func (f *fakeClock) Now() time.Time { return f.now }

// add advances the synthetic clock by d. The next Now() reflects it.
func (f *fakeClock) add(d time.Duration) { f.now = f.now.Add(d) }

// expectedPhaseOrder is the fixed pipeline order asserted by TestTickPhaseOrder.
// drainInbound is deliberately absent: Run drains inbound before tickOnce, kept out
// of the tickOnce trace.
var expectedPhaseOrder = []string{
	"resolveSubtickInputs",
	"tickWorld",
	"tickChunks",
	"tickEntities",
	"tickAI",
	"tickPhysics",
	"applyAsyncResults",
	"tracker.Tick",
	"flushOutbound",
}

// TestTickPhaseOrder asserts tickOnce runs the phases in EXACTLY the fixed order
// (TICK-01). The order is the load-bearing contract later phases fill into.
func TestTickPhaseOrder(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	var trace []string
	loop.traceTo(&trace)

	loop.tickOnce()

	if !reflect.DeepEqual(trace, expectedPhaseOrder) {
		t.Fatalf("phase order mismatch:\n got: %v\nwant: %v", trace, expectedPhaseOrder)
	}

	// drainInbound must NOT be part of tickOnce.
	for _, p := range trace {
		if p == "drainInbound" {
			t.Fatalf("drainInbound must not run inside tickOnce; it is a separate per-wake phase")
		}
	}
}

// TestApplyAsyncResultsNoop proves the async rejoin seam exists as a no-op when
// asyncIn is nil: it returns immediately, mutates nothing, and tickOnce still
// completes (TICK-05 seam).
func TestApplyAsyncResultsNoop(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	if loop.asyncIn != nil {
		t.Fatal("Phase 3 TickLoop must have a nil asyncIn (no-op seam)")
	}

	beforeGT := loop.gametime
	beforePlayers := len(loop.players)

	// applyAsyncResults alone must be a pure no-op (no panic, no mutation).
	loop.applyAsyncResults()

	if loop.gametime != beforeGT {
		t.Fatalf("applyAsyncResults mutated gametime: before=%d after=%d", beforeGT, loop.gametime)
	}
	if len(loop.players) != beforePlayers {
		t.Fatalf("applyAsyncResults mutated players: before=%d after=%d", beforePlayers, len(loop.players))
	}

	// And a full tick still completes with the seam present.
	loop.tickOnce()
}

// countingTracker records how many times Tick was called and whether it ran on the
// calling goroutine (synchronous stub — no goroutine spawned).
type countingTracker struct {
	calls int
}

func (c *countingTracker) Tick() { c.calls++ }

// TestTrackerTickStub asserts tracker.Tick() is called exactly once per tick and is
// a synchronous stub (the call returns before tickOnce continues; no goroutine).
func TestTrackerTickStub(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// The default tracker must be the synchronous no-op stub.
	if _, ok := loop.tracker.(noopTracker); !ok {
		t.Fatalf("default tracker must be noopTracker, got %T", loop.tracker)
	}

	// Swap in a counting tracker to prove synchronous, once-per-tick invocation.
	ct := &countingTracker{}
	loop.tracker = ct

	loop.tickOnce()
	if ct.calls != 1 {
		t.Fatalf("tracker.Tick called %d times in one tick, want 1", ct.calls)
	}

	loop.tickOnce()
	if ct.calls != 2 {
		t.Fatalf("tracker.Tick called %d times in two ticks, want 2", ct.calls)
	}
}
