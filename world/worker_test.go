package world

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/level"
)

// countingGen wraps a Generator and counts Generate calls (atomic) so tests can
// assert singleflight dedup. The blockUntil channel, when non-nil, holds the
// generation open so two concurrent requests overlap inside Do.
type countingGen struct {
	inner      Generator
	calls      int64
	enter      chan struct{} // signalled on each Generate entry
	blockUntil chan struct{} // Generate blocks on this until closed (if non-nil)
}

func (c *countingGen) Generate(pos level.ChunkPos) *level.Chunk {
	atomic.AddInt64(&c.calls, 1)
	if c.enter != nil {
		select {
		case c.enter <- struct{}{}:
		default:
		}
	}
	if c.blockUntil != nil {
		<-c.blockUntil
	}
	return c.inner.Generate(pos)
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

func TestWorkerSingleflightDedup(t *testing.T) {
	cg := &countingGen{
		inner:      newTestGen(),
		enter:      make(chan struct{}, 1),
		blockUntil: make(chan struct{}),
	}
	w := NewWorker(cg, "", 8)

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

	// Give the worker a moment to process the second request (it should either
	// dedup via singleflight or queue behind the first). Then release.
	time.Sleep(50 * time.Millisecond)
	close(cg.blockUntil)

	// Drain results: we must receive at least one; collect for a short window.
	var got int
	deadline := time.After(2 * time.Second)
	var mu sync.Mutex
loop:
	for {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("unexpected ChunkResult.Err = %v", res.Err)
			}
			mu.Lock()
			got++
			mu.Unlock()
			if got >= 2 {
				break loop
			}
		case <-time.After(300 * time.Millisecond):
			break loop
		case <-deadline:
			break loop
		}
	}

	if got == 0 {
		t.Fatal("no ChunkResult received")
	}
	if n := atomic.LoadInt64(&cg.calls); n != 1 {
		t.Fatalf("Generate called %d times, want exactly 1 (singleflight dedup failed)", n)
	}
}
