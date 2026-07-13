package world

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/level"
)

// boundedGen wraps a real generator and records the PEAK number of GenerateTerrain calls
// running concurrently. It briefly parks inside GenerateTerrain so many requests overlap in
// time -- without the park the calls finish too fast to observe overlap. The park does not
// change any chunk bytes (it is pure timing), so determinism is untouched.
type boundedGen struct {
	inner    Generator
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (g *boundedGen) GenerateTerrain(pos level.ChunkPos) *level.Chunk {
	n := g.inFlight.Add(1)
	for { // lock-free max
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond) // widen the overlap window so peak is observable
	g.inFlight.Add(-1)
	return g.inner.GenerateTerrain(pos)
}

func (g *boundedGen) Decorate(view *Neighborhood) { g.inner.Decorate(view) }
func (g *boundedGen) Dims() (minY, height int)    { return g.inner.Dims() }
func (g *boundedGen) HasSkyLight() bool           { return g.inner.HasSkyLight() }

// TestWorkerPoolBoundsConcurrency proves the fix for the "Loading terrain" stall: the worker
// must cap concurrent terrain generation at terrainWorkers() (NumCPU-2) instead of spawning
// one goroutine per request. It Requests a region far larger than the pool, then asserts the
// observed peak concurrency never exceeded the pool size. The OLD `go w.handleTerrain(...)`
// would drive peak to the full request count (441 at view distance), oversubscribing the CPU.
func TestWorkerPoolBoundsConcurrency(t *testing.T) {
	limit := terrainWorkers()
	g := &boundedGen{inner: NewSuperflat(24, -64, -1)}
	w := NewWorker(g, "", 1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// A 9x9 center block -> 81 wanted centers + their neighbor ring: hundreds of terrain
	// requests, dwarfing the pool. Drain until every wanted center is emitted.
	var region []level.ChunkPos
	want := map[level.ChunkPos]bool{}
	for x := int32(-4); x <= 4; x++ {
		for z := int32(-4); z <= 4; z++ {
			p := level.ChunkPos{x, z}
			region = append(region, p)
			want[p] = true
		}
	}
	for _, p := range region {
		w.Request(p)
	}

	remaining := len(want)
	deadline := time.After(60 * time.Second)
	for remaining > 0 {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("ChunkResult.Err = %v", res.Err)
			}
			if want[res.Pos] {
				delete(want, res.Pos)
				remaining--
			}
		case <-deadline:
			t.Fatalf("timed out: %d wanted centers not emitted", remaining)
		}
	}

	peak := int(g.peak.Load())
	if peak > limit {
		t.Fatalf("peak concurrent GenerateTerrain = %d, exceeds pool bound %d (NumCPU=%d)", peak, limit, runtime.NumCPU())
	}
	// Sanity: on a multi-core box the pool SHOULD have run several in parallel (not serialized
	// to 1), else the pool is misconfigured. Only assert this where the bound allows it.
	if limit > 1 && peak < 2 {
		t.Fatalf("peak concurrency = %d with bound %d: pool appears serialized, not parallel", peak, limit)
	}
}

// TestWorkerPoolReleaseOnCancel asserts the bounded pool is released when the worker's ctx is
// cancelled (Run's deferred Release), so a server restart does not leak the terrain-gen
// goroutines. After cancel the pool must report zero running workers.
func TestWorkerPoolReleaseOnCancel(t *testing.T) {
	w := NewWorker(NewSuperflat(24, -64, -1), "", 8)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); w.Run(ctx) }()

	w.Request(level.ChunkPos{0, 0})
	time.Sleep(50 * time.Millisecond) // let the request drain through the pool
	cancel()
	wg.Wait() // Run returns -> deferred Release ran

	// Release() closes the pool immediately (no new work accepted) but ants reclaims idle
	// worker goroutines on its purge timer, so Running() drains to 0 eventually, not instantly.
	// The load-bearing invariant is IsClosed (the leak is fixed: no new terrain gen can start);
	// the running count is then guaranteed to reach 0 as the purger runs.
	if !w.pool.IsClosed() {
		t.Fatalf("pool not closed after Run returned")
	}
	drained := false
	for i := 0; i < 200; i++ { // up to ~2s for the purge timer (default 1s expiry) to reclaim
		if w.pool.Running() == 0 {
			drained = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !drained {
		t.Fatalf("pool still has %d running workers 2s after Release", w.pool.Running())
	}
}

func TestWorkerNeighborRequestsSurviveSaturatedQueue(t *testing.T) {
	center := level.ChunkPos{0, 0}
	w := NewWorker(NewSuperflat(24, -64, -1), "", 1)

	// Before Run starts there is no scheduler goroutine, so this setup is race-free.
	// Filling requests makes every old nonblocking neighbor send fail deterministically.
	w.wanted[packPos(center)] = true
	w.requests <- center
	w.requestNeighbors(center)
	if got := len(w.pendingRequests); got != 8 {
		t.Fatalf("pending neighbor requests = %d, want 8", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Worker.Run did not stop after cancellation")
		}
	}()

	select {
	case res := <-w.Results():
		if res.Err != nil {
			t.Fatalf("ChunkResult.Err = %v", res.Err)
		}
		if res.Pos != center || res.Chunk == nil {
			t.Fatalf("result = (%v, %v), want completed center %v", res.Pos, res.Chunk, center)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wanted center never emitted after initially saturated request queue")
	}
}
