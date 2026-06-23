package server

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// msptRingSize is the number of recent tick durations kept for the rolling
// MSPT average / p99. 100 ticks = 5s of history at 20 TPS.
const msptRingSize = 100

// Loop timing constants shared by the production driver (Run) and the test seam
// (advance) so the accumulator/clamp/step logic is defined in exactly one place.
const (
	// tickStep is one logical Minecraft tick: 50ms => 20 logical ticks/second.
	tickStep = 50 * time.Millisecond
	// wakeInterval is how often Run wakes to sample the clock; faster than a tick
	// for fine-grained input. The LOGICAL tick is wake-rate-independent (TICK-02).
	wakeInterval = 5 * time.Millisecond
	// maxFrame is the spiral-of-death clamp: a single frame delta is capped here
	// before entering the accumulator, so a long stall degrades to slowdown
	// (TPS < 20) instead of an unbounded catch-up freeze. 250ms => <=5 catch-up
	// steps per frame.
	maxFrame = 250 * time.Millisecond
)

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

	// Accumulator state (TICK-02). last is the clock reading at the previous wake;
	// acc is unconsumed wall-clock time waiting to be turned into logical steps.
	// Owned by the tick goroutine (set up by start(), advanced by advance()/Run()).
	last time.Time
	acc  time.Duration

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

// start initializes the accumulator baseline. Run calls it with the first clock
// reading; tests call it before driving advance() so frame deltas are well-defined.
func (t *TickLoop) start(now time.Time) {
	t.last = now
	t.acc = 0
}

// advance feeds one wall-clock sample into the accumulator and consumes as many
// whole 50ms steps as are due, running drainInbound + tickOnce per step. It returns
// the number of logical steps consumed this call (0 on a sub-tick wake). This is the
// SINGLE place the accumulator/clamp/step logic lives — Run wraps it under a ticker;
// tests call it directly with fake-clock frames. drainInbound is invoked per step
// here only as a no-op fallback safety; Run drains once per wake before stepping (it
// passes a nil channel here so the per-step drain is skipped during tests).
func (t *TickLoop) advance(now time.Time) int {
	return t.advanceDraining(now, nil)
}

// advanceDraining is advance() with an explicit inbound channel drained once per
// consumed step. Run uses it so inbound is drained on the owner goroutine right
// before each logical tick; advance() (tests) passes nil to skip the drain.
func (t *TickLoop) advanceDraining(now time.Time, inbound <-chan Intent) int {
	frame := now.Sub(t.last)
	t.last = now
	if frame < 0 {
		frame = 0 // a non-monotonic source must never rewind the accumulator
	}
	if frame > maxFrame {
		frame = maxFrame // spiral-of-death clamp: drop backlog, slow not freeze
	}
	t.acc += frame

	steps := 0
	for t.acc >= tickStep {
		if inbound != nil {
			t.drainInbound(inbound) // non-blocking drain on the OWNER goroutine
		}
		t.tickOnce() // one logical tick: ordered phases + gametime++ + recordMSPT
		t.acc -= tickStep
		steps++
	}
	return steps
}

// Run is the production tick driver (TICK-01/TICK-02/TICK-05). It owns all game
// state and consumes the Phase-2 chan Intent. A time.Ticker wakes faster than the
// tick (self-correcting, no Sleep drift); each wake samples the injectable clock,
// feeds the accumulator, and runs whole logical steps. Returns when ctx is cancelled.
func (t *TickLoop) Run(ctx context.Context, inbound <-chan Intent) {
	ticker := time.NewTicker(wakeInterval)
	defer ticker.Stop()

	t.start(t.clock.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.advanceDraining(t.clock.Now(), inbound)
		}
	}
}

// drainInbound consumes every currently-queued Intent and returns immediately when
// the channel is empty (select-default) — it NEVER parks the tick (T-3-02). A closed
// channel (server shutdown) also returns. Dispatch runs on the owner goroutine, so
// no game-state pointer crosses the boundary; only Intent does.
func (t *TickLoop) drainInbound(inbound <-chan Intent) {
	for {
		select {
		case it, ok := <-inbound:
			if !ok {
				return // channel closed: shutting down
			}
			t.dispatch(it.Client, it.Packet) // decode + route on the owner goroutine
		default:
			return // nothing queued: return immediately, never block
		}
	}
}

// dispatch routes one inbound packet on the tick goroutine. It is total and cheap:
// every path is a no-op-or-cheap action, never blocks on IO, never panics on an
// unknown/malformed ID (T-3-02). Phase 3 handles only the boundary marker; full
// subtick wiring is 03-02 and keep-alive forwarding is 03-03.
func (t *TickLoop) dispatch(c *Client, p pk.Packet) {
	switch packetid.ServerboundPacketID(p.ID) {
	case packetid.ServerboundClientTickEnd:
		// Client input-batch boundary marker (new in 1.21.2 / present in 776).
		// Phase 3 may record it; full subtick use is Phase 6. No-op for now.
	case packetid.ServerboundKeepAlive:
		// Keep-alive responses are forwarded to the independent KeepAlive timer in
		// 03-03; nothing to do on the tick goroutine here.
	default:
		// Unknown / not-yet-handled IDs are cheap no-ops: never block, never panic.
	}
	_ = c // *Client is unused until per-player dispatch lands (Wave 2/3)
}

// recordMSPT writes one tick's duration into the ring and republishes the read-only
// telemetry snapshot (TICK-06). The tick goroutine is the SOLE writer; observers read
// via Stats(). p99/avg/TPS are recomputed cheaply over the valid ring window.
func (t *TickLoop) recordMSPT(d time.Duration) {
	t.ring[t.ringIx] = d
	t.ringIx = (t.ringIx + 1) % msptRingSize
	if t.ringN < msptRingSize {
		t.ringN++
	}

	n := t.ringN
	// Copy the valid window for a cheap p99 sort (n <= 100; allocation is trivial).
	window := make([]time.Duration, n)
	copy(window, t.ring[:n])

	var sum time.Duration
	for _, v := range window {
		sum += v
	}
	avg := sum / time.Duration(n)

	sort.Slice(window, func(i, j int) bool { return window[i] < window[j] })
	p99idx := (n * 99) / 100
	if p99idx >= n {
		p99idx = n - 1
	}
	p99 := window[p99idx]

	// Effective TPS from the rolling average tick cost, capped at the target 20.
	tps := 20.0
	if avg > tickStep {
		tps = float64(time.Second) / float64(avg)
	}

	t.stats.Store(&TickStats{
		MSPTavg:  float64(avg) / float64(time.Millisecond),
		MSPTp99:  float64(p99) / float64(time.Millisecond),
		TPS:      tps,
		GameTime: t.gametime,
	})
}
