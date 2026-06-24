package world

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sync/singleflight"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/save/region"
)

// ChunkResult is the IMMUTABLE handoff from the off-tick worker to the tick
// (Plan 04-03). Once emitted, the worker retains no reference to Chunk and never
// mutates it — the tick takes sole ownership. This is the same single-owner
// discipline as the Phase-3 register/intent messages and is what keeps the
// off-tick rejoin -race clean (threat T-4-05).
type ChunkResult struct {
	Pos   level.ChunkPos
	Chunk *level.Chunk
	Err   error
}

// Worker loads-or-generates chunks off the tick. It reads bounded requests, runs
// each load through singleflight (so concurrent same-key requests collapse to one
// generation — threat T-4-02), and emits an immutable ChunkResult on Results().
//
// The request reader is a single goroutine that owns the requests channel; each
// request's load runs in its own short-lived goroutine so the reader never blocks
// and so two concurrent same-key loads actually meet inside singleflight.Do.
// region.Region is "Not MT-Safe", so a fresh Region is opened per region-load and
// is never shared across goroutines.
type Worker struct {
	gen       Generator
	regionDir string              // world region/ dir; "" => always generate (v1 superflat)
	requests  chan level.ChunkPos // BOUNDED -> backpressure, never unbounded goroutines
	results   chan ChunkResult    // buffered; drained by the tick (Plan 04-03)
	sf        singleflight.Group
}

// NewWorker builds a worker. buf sizes both the bounded request channel and the
// buffered results channel.
func NewWorker(gen Generator, regionDir string, buf int) *Worker {
	if buf < 1 {
		buf = 1
	}
	return &Worker{
		gen:       gen,
		regionDir: regionDir,
		requests:  make(chan level.ChunkPos, buf),
		results:   make(chan ChunkResult, buf),
	}
}

// Results is the read-only channel the tick drains for immutable ChunkResults.
func (w *Worker) Results() <-chan ChunkResult { return w.results }

// Request enqueues pos for load/generation. Non-blocking: if the bounded request
// channel is full it drops the request (the tick re-requests next tick), which
// applies backpressure instead of spawning unbounded work.
func (w *Worker) Request(pos level.ChunkPos) {
	select {
	case w.requests <- pos:
	default:
	}
}

// Run is the request reader. It owns the requests channel and dispatches each
// load in its own goroutine so the reader stays responsive and concurrent
// same-key loads collapse inside singleflight. It returns when ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case pos := <-w.requests:
			go w.handle(ctx, pos)
		}
	}
}

// handle runs one load through singleflight and emits the result.
func (w *Worker) handle(ctx context.Context, pos level.ChunkPos) {
	key := chunkKey(pos)
	v, err, _ := w.sf.Do(key, func() (any, error) {
		return w.loadOrGenerate(pos)
	})

	var res ChunkResult
	if err != nil {
		res = ChunkResult{Pos: pos, Err: err}
	} else {
		res = ChunkResult{Pos: pos, Chunk: v.(*level.Chunk)}
	}

	select {
	case <-ctx.Done():
	case w.results <- res:
	}
}

// loadOrGenerate tries a region load (if a region dir is configured) and falls
// through to generation on a miss. A corrupt-region error is surfaced (not
// silently regenerated).
func (w *Worker) loadOrGenerate(pos level.ChunkPos) (*level.Chunk, error) {
	if w.regionDir != "" {
		ch, ok, rerr := w.tryRegion(pos)
		if rerr != nil {
			return nil, rerr // corrupt region (threat T-4-03) — do not regenerate over it
		}
		if ok {
			return ch, nil
		}
	}
	return w.gen.Generate(pos), nil // region miss -> deterministic superflat
}

// tryRegion attempts to read pos from the Anvil region files, reusing the fork's
// region.Region + save.Chunk + level.ChunkFromSave wholesale.
//
//	(nil, false, nil) -> miss (no .mca / ErrNoSector / ErrNoData): fall through to gen
//	(nil, false, err) -> corrupt region (ErrSectorNegativeLength / ErrTooLarge): surface
//	(ch,  true,  nil) -> hit
//
// A fresh Region is opened and closed per call — region.Region is Not MT-Safe.
func (w *Worker) tryRegion(pos level.ChunkPos) (*level.Chunk, bool, error) {
	cx, cz := int(pos[0]), int(pos[1])
	rx, rz := region.At(cx, cz)
	name := filepath.Join(w.regionDir, "r."+strconv.Itoa(rx)+"."+strconv.Itoa(rz)+".mca")

	r, err := region.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no region file yet -> miss
		}
		return nil, false, err
	}
	defer r.Close()

	ix, iz := region.In(cx, cz)
	data, err := r.ReadSector(ix, iz)
	if err != nil {
		if errors.Is(err, region.ErrNoSector) || errors.Is(err, region.ErrNoData) {
			return nil, false, nil // not-yet-saved chunk -> miss
		}
		// ErrSectorNegativeLength / ErrTooLarge and any IO error: corrupt/real error.
		return nil, false, err
	}

	var sc save.Chunk
	if err := sc.Load(data); err != nil {
		return nil, false, err
	}
	ch, err := level.ChunkFromSave(&sc)
	if err != nil {
		return nil, false, err
	}
	return ch, true, nil
}

// chunkKey packs (cx,cz) into a stable singleflight key. Two callers with the
// same pos share one Do invocation (the dedup'd caller sees shared=true).
func chunkKey(pos level.ChunkPos) string {
	return strconv.FormatInt(int64(pos[0])<<32|int64(uint32(pos[1])), 10)
}
