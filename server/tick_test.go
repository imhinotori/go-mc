package server

import (
	"reflect"
	"testing"
	"time"

	pk "github.com/imhinotori/sulfur/net/packet"
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
	"tickWeather",
	"tickSleep",
	"tickWorld",
	"tickChunks",
	"tickEntities",
	"tickAI",
	"tickPhysics",
	"applyAsyncResults",
	"tickEntityMovement",
	"tickEquipment",
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

	if loop.only().asyncIn != nil {
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
// synchronous (the call returns before tickOnce continues; no goroutine). The seam shape
// (one-method interface, fixed call site) is the load-bearing contract; the EXECUTOR behind
// it is swappable.
func TestTrackerTickStub(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// Plan 06-02 FILLED the Phase-3 seam with the synchronous entityTracker; OPT-02 (08-04) then
	// SWAPPED the executor to the async tracker behind the UNCHANGED interface + call site. The
	// default is therefore now the *asyncTracker, and it must still satisfy the unchanged
	// one-method `tracker interface{ Tick() }` (the load-bearing seam contract — the executor
	// behind it is swappable, which is exactly what this test proves below).
	if _, ok := loop.tracker.(*asyncTracker); !ok {
		t.Fatalf("default tracker must be the *asyncTracker (the OPT-02 swap), got %T", loop.tracker)
	}

	// Swap in a counting tracker to prove synchronous, once-per-tick invocation through the
	// unchanged seam (exactly the swap Phase 8 performs to move the executor off-tick).
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

// TestGameTimeAnchor proves TICK-02: feeding 60s of synthetic frames in arbitrary
// chunk sizes yields EXACTLY 1200 game-time increments (60s / 50ms), and game-time
// never advances on a wake that consumes zero steps. Driven entirely by the fake
// clock — no real waiting.
func TestGameTimeAnchor(t *testing.T) {
	clk := newFakeClock()
	loop := NewTickLoop(clk)
	loop.start(clk.Now())

	// A mix of sub-tick, supra-tick, and exact-tick frame deltas summing to exactly 60s.
	// 5ms*10 (50ms) + 17ms + 33ms (50ms) + 50ms repeated. Build a deterministic plan
	// that sums to 60_000ms.
	frames := []time.Duration{}
	total := time.Duration(0)
	pattern := []time.Duration{5 * time.Millisecond, 17 * time.Millisecond, 33 * time.Millisecond, 50 * time.Millisecond, 7 * time.Millisecond, 28 * time.Millisecond}
	pi := 0
	for total < 60*time.Second {
		d := pattern[pi%len(pattern)]
		if total+d > 60*time.Second {
			d = 60*time.Second - total // final frame lands exactly on 60s
		}
		frames = append(frames, d)
		total += d
		pi++
	}
	if total != 60*time.Second {
		t.Fatalf("frame plan does not sum to 60s: got %v", total)
	}

	zeroStepWakes := 0
	for _, f := range frames {
		clk.add(f)
		gtBefore := loop.gametime
		steps := loop.advance(clk.Now())
		if steps == 0 {
			zeroStepWakes++
			if loop.gametime != gtBefore {
				t.Fatalf("game-time advanced on a zero-step wake: before=%d after=%d", gtBefore, loop.gametime)
			}
		}
	}

	if loop.gametime != 1200 {
		t.Fatalf("game-time anchor broken: 60s of frames yielded %d ticks, want exactly 1200", loop.gametime)
	}
	if zeroStepWakes == 0 {
		t.Fatal("expected at least one sub-tick (zero-step) wake in the frame plan to exercise the no-advance invariant")
	}
}

// TestSpiralClamp proves the spiral-of-death clamp: a single 2s frame is clamped to
// 250ms before entering the accumulator, so catch-up is bounded (<=5 steps for that
// frame, not 40), the accumulator stays bounded, and a normal frame afterward
// advances exactly one tick.
func TestSpiralClamp(t *testing.T) {
	clk := newFakeClock()
	loop := NewTickLoop(clk)
	loop.start(clk.Now())

	clk.add(2 * time.Second) // a 2s stall (e.g. GC pause)
	steps := loop.advance(clk.Now())

	if steps > 5 {
		t.Fatalf("spiral clamp failed: a 2s frame produced %d catch-up steps, want <=5 (clamped to 250ms)", steps)
	}
	if steps != 5 {
		t.Fatalf("expected exactly 5 steps from a 250ms-clamped frame, got %d", steps)
	}
	if loop.gametime != 5 {
		t.Fatalf("game-time after clamped 2s frame = %d, want 5", loop.gametime)
	}

	// The accumulator must be bounded (< one step) after consuming the clamped frame.
	if loop.acc >= 50*time.Millisecond {
		t.Fatalf("accumulator not bounded after clamp: acc=%v", loop.acc)
	}

	// A normal 50ms frame afterward advances exactly one more tick (loop recovered).
	clk.add(50 * time.Millisecond)
	more := loop.advance(clk.Now())
	if more != 1 || loop.gametime != 6 {
		t.Fatalf("loop did not recover after clamp: steps=%d gametime=%d, want steps=1 gametime=6", more, loop.gametime)
	}
}

// TestMSPTObservable proves TICK-06: after ticks run, the published atomic TickStats
// snapshot is readable (off the tick goroutine), reports non-zero MSPT/TPS and the
// live gametime, and an injected slow tick is reflected in the snapshot.
func TestMSPTObservable(t *testing.T) {
	clk := newFakeClock()
	loop := NewTickLoop(clk)
	loop.start(clk.Now())

	// Before any tick, no snapshot is published.
	if loop.Stats() != nil {
		t.Fatal("Stats() should be nil before the first tick")
	}

	// Make each tickOnce consume a synthetic 2ms via the clock so MSPT is non-zero.
	// The tracker hook advances the fake clock during the tick body, which
	// tickOnce measures as the per-tick duration.
	loop.tracker = &clockAdvancingTracker{clk: clk, by: 2 * time.Millisecond}

	for i := 0; i < 10; i++ {
		clk.add(50 * time.Millisecond)
		loop.advance(clk.Now())
	}

	snap := loop.Stats()
	if snap == nil {
		t.Fatal("Stats() nil after 10 ticks")
	}
	if snap.GameTime != loop.gametime {
		t.Fatalf("snapshot gametime %d != loop gametime %d", snap.GameTime, loop.gametime)
	}
	if snap.MSPTavg <= 0 {
		t.Fatalf("MSPTavg should be > 0 (each tick consumed 2ms), got %v", snap.MSPTavg)
	}
	if snap.TPS <= 0 {
		t.Fatalf("TPS should be > 0, got %v", snap.TPS)
	}

	// An injected slow tick (20ms) raises the observed p99.
	prevP99 := snap.MSPTp99
	loop.tracker = &clockAdvancingTracker{clk: clk, by: 20 * time.Millisecond}
	clk.add(50 * time.Millisecond)
	loop.advance(clk.Now())
	snap2 := loop.Stats()
	if snap2.MSPTp99 < prevP99 {
		t.Fatalf("injected slow tick not reflected in p99: before=%v after=%v", prevP99, snap2.MSPTp99)
	}
	if snap2.MSPTp99 < 19.0 {
		t.Fatalf("p99 should reflect the 20ms slow tick, got %v", snap2.MSPTp99)
	}
}

// clockAdvancingTracker advances the fake clock by a fixed amount during Tick(),
// simulating a tick body that consumes wall-clock time so MSPT is measurable.
type clockAdvancingTracker struct {
	clk *fakeClock
	by  time.Duration
}

func (c *clockAdvancingTracker) Tick() { c.clk.add(c.by) }

// TestDrainInboundNonBlocking proves drainInbound consumes all currently-queued
// Intents and returns immediately when the channel is empty (select-default),
// never parking the tick (T-3-02).
func TestDrainInboundNonBlocking(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	inbound := make(chan Intent, 8)
	// Queue a few Intents (nil client is fine; dispatch must be total and not deref it
	// for unknown/no-handler IDs).
	for i := 0; i < 3; i++ {
		inbound <- Intent{Client: nil, Packet: pk.Packet{ID: 999, Data: nil}}
	}

	done := make(chan struct{})
	go func() {
		loop.drainInbound(inbound) // must return promptly on empty, not block
		close(done)
	}()

	select {
	case <-done:
		// returned without parking — correct
	case <-time.After(2 * time.Second):
		t.Fatal("drainInbound parked on an empty (but open) channel — it must be non-blocking")
	}

	// Draining again on an empty channel must also return immediately.
	loop.drainInbound(inbound)
}
