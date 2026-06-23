package server

import (
	"sync/atomic"
	"time"
)

// msptRingSize is the number of recent tick durations kept for the rolling
// MSPT average / p99. 100 ticks = 5s of history at 20 TPS.
const msptRingSize = 100

// TickStats is an immutable, read-only snapshot of tick telemetry. The tick
// goroutine is the SOLE writer; observers read it lock-free via TickLoop.Stats().
// Never a backdoor to mutate game state off-thread — it is pure telemetry.
type TickStats struct {
	MSPTavg  float64 // rolling average tick duration, milliseconds
	MSPTp99  float64 // rolling p99 tick duration, milliseconds
	TPS      float64 // effective ticks per second (capped at 20)
	GameTime int64   // current game-time counter (age of the world in ticks)
}

// asyncResult is the immutable message an async worker (Phase 8) returns to the
// tick goroutine, applied on-thread inside applyAsyncResults. The seam exists from
// day one so Phase 8 attaches concrete result types without reordering the pipeline.
type asyncResult interface{ applyTo(*TickLoop) }

// tracker is the entity/chunk tracking executor. Today it is a synchronous stub
// (noopTracker); Phase 8 swaps the executor behind this interface without changing
// the tickOnce call site.
type tracker interface{ Tick() }

// noopTracker is the Phase-3 synchronous stub: Tick() does nothing, spawns no
// goroutine, never panics.
type noopTracker struct{}

// Tick is the synchronous no-op tracking step.
func (noopTracker) Tick() {}

// TickLoop is the single-owner authoritative game loop. Exactly one goroutine —
// the one running Run — owns and mutates every field below. The only legal crossing
// of the boundary is an immutable channel message (the Phase-2 chan Intent inbound;
// the asyncIn results channel in Phase 8). This single-owner discipline (TICK-05)
// makes the loop -race clean by construction.
type TickLoop struct {
	clock Clock // injectable time source; the loop driver never calls time.Now() directly

	gametime int64 // pure tick counter: ++ exactly once per consumed 50ms step (TICK-02)

	// MSPT ring buffer (TICK-06). Written only by the tick goroutine inside recordMSPT.
	ring   [msptRingSize]time.Duration
	ringN  int // total ticks recorded (so we know how much of the ring is valid)
	ringIx int // next write index

	// stats is the published read-only telemetry snapshot. Sole writer = tick
	// goroutine; observers read via Stats(). atomic.Pointer keeps it off the
	// critical path and -race clean.
	stats atomic.Pointer[TickStats]

	// asyncIn is the Phase-8 async-result rejoin channel. It is nil in Phase 3, so
	// applyAsyncResults is a genuine no-op — the seam just EXISTS in the right slot.
	asyncIn <-chan asyncResult

	// tracker is the (synchronous stub) tracking executor; Phase 8 swaps it.
	tracker tracker

	// players is the per-player collection owned by the tick goroutine. A plain
	// slice is correct and faster than a concurrent map under single ownership —
	// do NOT use xsync here (that is Phase 8). Empty until players join (Wave 2/3).
	players []*tickPlayer

	// phaseTrace, when non-nil, records the name of each phase as it runs. It is a
	// test-only observability hook (set by tests via traceTo) used to assert the
	// fixed phase order; in production it stays nil and costs nothing.
	phaseTrace *[]string
}

// tickPlayer is the per-player game state owned by the tick goroutine. It is an
// empty placeholder in Phase 3; Wave 2/3 and later phases fill it (subtick buffer,
// position, the *Client handle for flush). Defined here so players has a concrete
// element type and the ownership boundary is explicit.
type tickPlayer struct {
	client *Client // the Phase-2 connection handle; flushOutbound enqueues via client.Send
}

// NewTickLoop constructs a TickLoop over the given injectable clock with a
// synchronous no-op tracker and a nil async channel (so applyAsyncResults is a
// no-op). asyncIn and a real tracker are wired in Phase 8 with no pipeline change.
func NewTickLoop(clock Clock) *TickLoop {
	return &TickLoop{
		clock:   clock,
		tracker: noopTracker{},
		// asyncIn stays nil (no-op seam); ring is zero-valued; gametime starts at 0.
	}
}

// Stats returns the latest published telemetry snapshot, readable off the tick
// goroutine without locking (TICK-06). Returns nil before the first tick records
// stats.
func (t *TickLoop) Stats() *TickStats { return t.stats.Load() }

// GameTime returns the current game-time counter. Intended for the tick goroutine
// and tests; off-thread observers should read Stats().GameTime.
func (t *TickLoop) GameTime() int64 { return t.gametime }

// traceTo installs a test-only phase-order recorder. Each phase appends its name to
// *dst as it runs, letting TestTickPhaseOrder assert the fixed pipeline order. Pass
// nil to disable. Not used in production.
func (t *TickLoop) traceTo(dst *[]string) { t.phaseTrace = dst }

// trace records a phase name when the test hook is installed; a no-op otherwise.
func (t *TickLoop) trace(name string) {
	if t.phaseTrace != nil {
		*t.phaseTrace = append(*t.phaseTrace, name)
	}
}
