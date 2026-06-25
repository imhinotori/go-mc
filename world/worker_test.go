package world

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/level"
)

// countingGen wraps a Generator and counts GenerateTerrain calls so tests can assert
// singleflight dedup under the split (GEN2-02) interface. It counts both globally
// (calls, atomic) and PER POS (perPos, mutex-guarded) — the per-pos count is what the
// re-scoped dedup test asserts, since the scheduler's neighbor auto-request adds
// other-key GenerateTerrain calls that make a global "exactly 1" false.
//
// The blockUntil channel, when non-nil, holds the generation open so two concurrent
// SAME-KEY requests overlap inside singleflight.Do; enter is signalled on each
// GenerateTerrain entry so a test can wait until the first generation is in-flight.
type countingGen struct {
	inner      Generator
	calls      int64
	enter      chan struct{} // signalled on each GenerateTerrain entry
	blockUntil chan struct{} // GenerateTerrain blocks on this until closed (if non-nil)

	mu     sync.Mutex
	perPos map[level.ChunkPos]int
}

func (c *countingGen) GenerateTerrain(pos level.ChunkPos) *level.Chunk {
	atomic.AddInt64(&c.calls, 1)
	c.mu.Lock()
	if c.perPos == nil {
		c.perPos = make(map[level.ChunkPos]int)
	}
	c.perPos[pos]++
	c.mu.Unlock()
	if c.enter != nil {
		select {
		case c.enter <- struct{}{}:
		default:
		}
	}
	if c.blockUntil != nil {
		<-c.blockUntil
	}
	return c.inner.GenerateTerrain(pos)
}

// Decorate / Dims delegate to the wrapped generator so the staged center is promoted to
// StatusFull (the no-op decoration) and the worker can size the Neighborhood.
func (c *countingGen) Decorate(view *Neighborhood) { c.inner.Decorate(view) }
func (c *countingGen) Dims() (int, int)            { return c.inner.Dims() }

// posCalls returns the GenerateTerrain call count for a single pos.
func (c *countingGen) posCalls(pos level.ChunkPos) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.perPos[pos]
}

func newTestGen() Generator {
	return NewSuperflat(24, -64, -1)
}

func TestWorkerEmitsResult(t *testing.T) {
	gen := newTestGen()
	w := NewWorker(gen, "", 8) // regionDir="" => always generate

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	pos := level.ChunkPos{0, 0}
	w.Request(pos)

	select {
	case res := <-w.Results():
		if res.Err != nil {
			t.Fatalf("ChunkResult.Err = %v, want nil", res.Err)
		}
		if res.Pos != pos {
			t.Fatalf("ChunkResult.Pos = %v, want %v", res.Pos, pos)
		}
		if res.Chunk == nil {
			t.Fatalf("ChunkResult.Chunk is nil")
		}
		if got := len(res.Chunk.Sections); got != 24 {
			t.Fatalf("generated chunk has %d sections, want 24", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ChunkResult")
	}
}

// TestWorkerSingleflightDedup proves CONCURRENT SAME-KEY dedup under the split (GEN2-02)
// lifecycle: two Request(pos) for the SAME pos while a generation is held open collapse to
// ONE GenerateTerrain(pos) for THAT key. It is re-scoped from the old global "calls == 1"
// assertion, which the scheduler's neighbor auto-request now invalidates (a single
// Request(C) pulls C's 8 neighbors -> 8 more GenerateTerrain calls for OTHER keys). The
// dedup guarantee is per-key, so the assertion is on the count for the requested key.
func TestWorkerSingleflightDedup(t *testing.T) {
	cg := &countingGen{
		inner:      newTestGen(),
		enter:      make(chan struct{}, 1),
		blockUntil: make(chan struct{}),
	}
	// Generous buffer so the scheduler's neighbor auto-requests never drop (convergence).
	w := NewWorker(cg, "", 256)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	pos := level.ChunkPos{4, 4}

	// Fire the first request and wait until generation is actually in-flight
	// (inside singleflight.Do), so the second request collides on the same key.
	w.Request(pos)
	select {
	case <-cg.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("first generation never started")
	}

	// Second request for the same key while the first is held open.
	w.Request(pos)

	// Give the worker a moment to process the second request (it should dedup via
	// singleflight on the same key). Then release ALL held generations (the first
	// same-key gen + every auto-requested neighbor, which also block on blockUntil).
	time.Sleep(50 * time.Millisecond)
	close(cg.blockUntil)

	// Drain results until the requested center is emitted (it completes once its 8
	// auto-requested neighbors carve), collecting for a bounded window.
	var got int
	var sawCenter bool
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("unexpected ChunkResult.Err = %v", res.Err)
			}
			got++
			if res.Pos == pos {
				sawCenter = true
				break loop
			}
		case <-time.After(300 * time.Millisecond):
			if got > 0 {
				break loop
			}
		case <-deadline:
			break loop
		}
	}

	if got == 0 {
		t.Fatal("no ChunkResult received")
	}
	if !sawCenter {
		t.Fatalf("requested center %v never emitted (got %d results)", pos, got)
	}
	// The dedup guarantee: the requested KEY was terrain-generated exactly once despite
	// two concurrent same-pos Requests. Neighbor auto-requests (other keys) are expected
	// and NOT asserted on.
	if n := cg.posCalls(pos); n != 1 {
		t.Fatalf("GenerateTerrain(%v) called %d times for the requested key, want exactly 1 (singleflight dedup failed)", pos, n)
	}
}
