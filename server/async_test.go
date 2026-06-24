package server

import (
	"sync"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

// async_test.go is the Phase-8 Wave-0 -race scaffold (OPT-06). It proves the async SUBSTRATE is
// sound BEFORE any real subsystem (OPT-01/02/03) is swapped in:
//
//   - TestAsyncPoolNonBlocking: a saturated non-blocking pool DROPS a Submit (ErrPoolOverload)
//     instead of blocking — the Pitfall-4 backpressure that keeps a busy pool from stalling the
//     tick.
//   - TestAsyncRejoinRaceClean: pool workers send asyncResults onto asyncIn2 WHILE the tick
//     advances and drains them via applyAsyncResults — exercising the pool -> channel -> owner
//     rejoin boundary so the Docker -race gate has something to catch from day one.
//   - TestSubmitOrDropDropsWhenSaturated / TestSubmitOrDropNilPool: the centralized drop-on-
//     overload discipline (submitOrDrop) returns false (drops) on overload and on a nil pool.

// TestAsyncPoolNonBlocking proves newAsyncPool builds a NON-BLOCKING bounded pool: with all
// workers occupied, a further Submit returns ants.ErrPoolOverload (the work is DROPPED) rather
// than blocking the caller. This is the load-bearing Pitfall-4 property — a blocking Submit on
// the tick owner would stall the tick under load; the non-blocking pool degrades to "compute a
// tick later" instead. (08-RESEARCH Pitfall 4 / world.Worker.Request drop-on-full parallel.)
func TestAsyncPoolNonBlocking(t *testing.T) {
	pool := newAsyncPool(1) // a single worker so we can deterministically saturate it
	defer pool.Release()

	// Occupy the only worker with a task that blocks until we release it, so the pool is full.
	block := make(chan struct{})
	started := make(chan struct{})
	if err := pool.Submit(func() {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("first submit (should fill the single worker) failed: %v", err)
	}
	<-started // ensure the worker is actually running before we try to overflow it

	// The pool is now saturated. A second Submit MUST return ErrPoolOverload (drop), not block.
	err := pool.Submit(func() { t.Error("overflow task must NOT run — the pool is saturated") })
	if err != ants.ErrPoolOverload {
		close(block)
		t.Fatalf("saturated non-blocking pool: want ants.ErrPoolOverload (drop), got %v", err)
	}

	close(block) // release the occupying worker so the pool can drain and Release cleanly
}

// TestSubmitOrDropDropsWhenSaturated proves the centralized submitOrDrop helper reports a drop
// (returns false) when the underlying non-blocking pool is saturated — the discipline every
// OPT-01/02/03 submit site shares so a saturated pool never blocks the tick.
func TestSubmitOrDropDropsWhenSaturated(t *testing.T) {
	pool := newAsyncPool(1)
	defer pool.Release()

	block := make(chan struct{})
	started := make(chan struct{})
	if !submitOrDrop(pool, func() { close(started); <-block }) {
		t.Fatal("first submitOrDrop into an empty pool should be ACCEPTED (true)")
	}
	<-started

	if submitOrDrop(pool, func() { t.Error("overflow work must not run") }) {
		close(block)
		t.Fatal("submitOrDrop into a saturated pool should DROP (return false)")
	}
	close(block)
}

// TestSubmitOrDropNilPool proves submitOrDrop treats a nil pool as a safe no-op drop (returns
// false), so a Phase-3-style TickLoop that never wired the Phase-8 pools is robust.
func TestSubmitOrDropNilPool(t *testing.T) {
	if submitOrDrop(nil, func() { t.Error("nil-pool work must never run") }) {
		t.Fatal("submitOrDrop with a nil pool should DROP (return false)")
	}
}

// TestAsyncRejoinRaceClean exercises the FULL Phase-8 rejoin boundary under concurrency: many
// pool workers compute off-tick and send an asyncResult onto asyncIn2, WHILE the tick goroutine
// advances and drains them via applyAsyncResults on the OWNER. This is the -race target — it
// proves the pool -> channel -> owner crossing is clean (TICK-05 / OPT-06) before a real
// subsystem rides it. The submitted result is a pathReady for a NON-EXISTENT mob id, so applyTo
// cleanly hits its despawn drop path (Pitfall 2) — no live entity is needed and no tick-owned
// state is mutated, isolating the test to the rejoin plumbing itself.
//
// Run under: go test -race -run TestAsyncRejoinRaceClean (Docker golang:1.26 on this host).
func TestAsyncRejoinRaceClean(t *testing.T) {
	clock := newFakeClock()
	loop := NewTickLoop(clock)
	defer loop.Close()

	if loop.asyncIn2 == nil {
		t.Fatal("NewTickLoop must construct asyncIn2 (the Phase-8 rejoin channel)")
	}
	if loop.pathPool == nil || loop.trackerPool == nil || loop.spawnPool == nil {
		t.Fatal("NewTickLoop must construct the per-subsystem ants pools")
	}

	loop.start(clock.Now())

	const submissions = 200
	var wg sync.WaitGroup
	wg.Add(submissions)

	// Producer: a goroutine that submits work to the path pool. Each worker computes nothing real
	// (the rejoin plumbing is what is under test) and rejoins by sending a pathReady — for a mob
	// id that does not exist — onto asyncIn2. A dropped submit (saturated non-blocking pool) still
	// counts down the WaitGroup so the test never hangs; the dropped work simply never sends, which
	// is the correct drop-on-overload behavior.
	go func() {
		for i := 0; i < submissions; i++ {
			i := i
			accepted := submitOrDrop(loop.pathPool, func() {
				defer wg.Done()
				// Rejoin on asyncIn2. Use a non-existent mob id so applyTo takes the despawn-drop
				// path on the owner (Pitfall 2) without needing a live entity. The bounded buffer
				// + this send/owner-drain interplay is exactly what -race must vet.
				loop.asyncIn2 <- pathReady{mobID: int32(-1 - i), target: [3]int{i, 0, i}, path: nil}
			})
			if !accepted {
				wg.Done() // pool saturated: the work was dropped, so balance the WaitGroup here
			}
		}
	}()

	// Consumer (the OWNER): advance the tick repeatedly, draining asyncIn2 via applyAsyncResults
	// each tick, until every submitted closure has either rejoined or been dropped. A bounded loop
	// guards against a hang. advance() consumes whole 50ms steps from the fake clock.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		clock.add(tickStep) // one logical tick's worth of synthetic time
		loop.advance(clock.Now())

		select {
		case <-done:
			// All producers finished; drain any final results the last advance may have missed.
			loop.advance(clock.Now())
			loop.applyAsyncResults()
			return
		case <-deadline.C:
			t.Fatal("timed out waiting for async rejoins to drain — the pool->channel->owner boundary stalled")
		default:
			// Keep advancing; the owner drains asyncIn2 on each tick.
		}
	}
}
