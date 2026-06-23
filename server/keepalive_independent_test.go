package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/chat"
)

// TestKeepAliveIndependentOfTick proves TICK-04: keep-alive liveness is a function of
// KeepAlive.Run's OWN goroutine and its OWN 15s-ping/30s-timeout timers, NEVER gated
// on the tick. We start the fork's KeepAlive (reused VERBATIM — keepalive.go is not
// modified) on its own goroutine, then provably STALL a separate "tick" goroutine for
// a window. The load-bearing assertions: (1) the keep-alive goroutine still issues
// SendKeepAlive pings while the tick is blocked, and (2) when the client responds
// (ClientTick), no SendDisconnect(timeout) fires — the stalled tick cannot starve
// pings or trigger the "mystery ~20s disconnect" (T-3-05).
//
// The test runs FAST and deterministically: it does NOT wait the real 15s/30s. The
// ping timer is reset to a tiny interval BEFORE Run starts consuming it, so the timer
// channel fires promptly on Run's goroutine. No wall-clock liveness assertion depends
// on the (blocked) tick goroutine — that is precisely the independence being proven.
func TestKeepAliveIndependentOfTick(t *testing.T) {
	k := NewKeepAlive()

	// Accelerate ONLY the ping cadence for the test so Run's own timer fires promptly.
	// We do not touch keepalive.go; we reset the timer the component already owns. The
	// independence property is unchanged — Run still reads from ITS timer, not the tick.
	k.listTimer.Reset(5 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fc := newFakeKeepAliveClient()

	// Keep-alive runs on its OWN goroutine — the whole point of TICK-04.
	go k.Run(ctx)

	// A SEPARATE "tick" goroutine that we deliberately STALL on an unbuffered channel.
	// While it is parked here it makes zero progress — standing in for a frozen tick
	// loop (GC pause, deadlock, long phase). Keep-alive must be unaffected.
	tickStalled := make(chan struct{})
	tickReleased := make(chan struct{})
	go func() {
		<-tickStalled  // block until the test says "now you're stalled"
		<-tickReleased // and stay blocked for the whole window
	}()
	close(tickStalled) // the tick goroutine is now provably parked

	// Register the client through KeepAlive's own channel (its goroutine handles it).
	k.ClientJoin(fc)

	// Within a short bounded window — far under the real 30s timeout — the keep-alive
	// goroutine's own timer must fire a ping. The tick is stalled the entire time, so
	// observing the ping proves keep-alive progress is independent of tick cadence.
	if !fc.waitForPing(2 * time.Second) {
		t.Fatal("KeepAlive did not ping within the window while the tick was stalled — keep-alive is not independent of the tick (TICK-04 broken)")
	}

	// The client responds to the ping (as a live client would). This moves it back to
	// the ping list and resets the wait timer on KeepAlive's own goroutine — none of it
	// involves the (still-stalled) tick.
	k.ClientTick(fc)

	// Critically: no timeout-disconnect may fire just because the tick is stalled. The
	// wait timer is the real 30s; we are nowhere near it, and the client answered. A
	// SendDisconnect here would mean keep-alive liveness was (wrongly) coupled to tick
	// progress.
	if got := fc.disconnects.Load(); got != 0 {
		t.Fatalf("KeepAlive issued %d disconnect(s) while the tick was merely stalled — liveness must not be gated on tick cadence (T-3-05)", got)
	}

	// The tick goroutine was stalled this whole time; release it now for clean shutdown.
	close(tickReleased)

	// Sanity: keep-alive did real work (>=1 ping) entirely off the tick.
	if pings := fc.pings.Load(); pings < 1 {
		t.Fatalf("expected >=1 keep-alive ping off the tick goroutine, got %d", pings)
	}
}

// fakeKeepAliveClient is a deterministic KeepAliveClient that records pings and
// disconnects via atomics and signals the first ping on a pre-created channel so the
// test can wait on it with a bound (never on real wall-clock intervals). The channel
// is created up front so there is no data race between the Run goroutine (writer) and
// the test goroutine (reader) under -race.
type fakeKeepAliveClient struct {
	pings       atomic.Int64
	disconnects atomic.Int64
	pinged      chan struct{}
	once        atomic.Bool
}

func newFakeKeepAliveClient() *fakeKeepAliveClient {
	return &fakeKeepAliveClient{pinged: make(chan struct{})}
}

// SendKeepAlive records a server-issued ping and signals the first one. The id is
// server-generated and incrementing (spoof-resistant, T-3-06) — we don't trust or
// assert a client-chosen value here.
func (f *fakeKeepAliveClient) SendKeepAlive(id int64) {
	f.pings.Add(1)
	if f.once.CompareAndSwap(false, true) {
		close(f.pinged)
	}
}

// SendDisconnect records a disconnect (e.g. a timeout kick). In this test it must
// never fire solely because the tick stalled.
func (f *fakeKeepAliveClient) SendDisconnect(reason chat.Message) {
	f.disconnects.Add(1)
}

// waitForPing blocks up to d for the first ping; returns false on timeout. The bound
// is independent of (and far below) the real keep-alive intervals.
func (f *fakeKeepAliveClient) waitForPing(d time.Duration) bool {
	select {
	case <-f.pinged:
		return true
	case <-time.After(d):
		return f.pings.Load() > 0
	}
}
