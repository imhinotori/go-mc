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

	// GEN2-02 cross-chunk seam. carved is the parallel->serial handoff: handleTerrain
	// goroutines send carved chunks here and the SINGLE scheduler goroutine
	// (runScheduler) drains it. staging + requested are OWNED EXCLUSIVELY by the
	// scheduler goroutine — NO lock, -race clean by construction (the same single-owner
	// discipline as the tick-owned manager). Never touch them off the scheduler goroutine.
	carved    chan *carvedChunk
	staging   map[int64]*stagedChunk // carved-but-(maybe)-not-decorated chunks, packPos keyed
	requested map[int64]bool         // neighbor auto-request dedup (scheduler-owned)
}

// carvedChunk is the handleTerrain -> scheduler handoff: a freshly carved chunk
// (StatusCarvers) plus its pos. It crosses goroutines exactly once, over w.carved.
type carvedChunk struct {
	pos level.ChunkPos
	ch  *level.Chunk
}

// stagedChunk is a chunk held by the scheduler until its 3x3 neighborhood is carved.
// carved=true once GenerateTerrain has produced its blocks; decorated=true after the
// (no-op in Phase 10) Decorate pass promotes it to StatusFull and it is emitted. The
// decorated flag guards double-decoration / double-emit.
type stagedChunk struct {
	pos       level.ChunkPos
	chunk     *level.Chunk
	carved    bool
	decorated bool
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
		carved:    make(chan *carvedChunk, buf),
		staging:   make(map[int64]*stagedChunk),
		requested: make(map[int64]bool),
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
	// The scheduler is the SINGLE owner of staging/requested + all decoration.
	go w.runScheduler(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case pos := <-w.requests:
			go w.handleTerrain(ctx, pos)
		}
	}
}

// handleTerrain runs one load/terrain-generation through singleflight (so concurrent
// same-key requests collapse to one GenerateTerrain — threat T-4-02) and hands the
// result to the scheduler. A region-loaded chunk is already StatusFull -> it bypasses
// staging and is emitted directly (do not re-decorate a saved world). A region MISS
// produces a carved (StatusCarvers) chunk -> it is sent to the scheduler for staging.
//
// This is the parallel half of the seam: terrain stays parallel + footprint-guarded;
// the parallel->serial handoff is the w.carved channel.
func (w *Worker) handleTerrain(ctx context.Context, pos level.ChunkPos) {
	key := chunkKey(pos)
	v, err, _ := w.sf.Do(key, func() (any, error) {
		return w.loadOrGenerate(pos)
	})

	if err != nil {
		w.emit(ctx, ChunkResult{Pos: pos, Err: err})
		return
	}

	ch := v.(*level.Chunk)
	if ch.Status == level.StatusFull {
		// Region hit (already decorated/saved): emit directly, never stage.
		w.emit(ctx, ChunkResult{Pos: pos, Chunk: ch})
		return
	}

	// Region miss -> carved chunk -> hand to the scheduler for staging + decoration.
	select {
	case <-ctx.Done():
	case w.carved <- &carvedChunk{pos: pos, ch: ch}:
	}
}

// runScheduler is the NEW single goroutine that owns staging + requested + all
// decoration (no locks -> -race clean by construction; threat T-10-05). It drains
// w.carved: stages each carved chunk, auto-requests its 8 neighbors (guarded by the
// requested set so a single Request(C) pulls C's 3x3 into existence exactly once),
// and scans the up-to-9 centers this chunk could newly complete, decorating each.
func (w *Worker) runScheduler(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cc := <-w.carved:
			key := packPos(cc.pos)
			s := w.staging[key]
			if s == nil {
				s = &stagedChunk{pos: cc.pos}
				w.staging[key] = s
			}
			s.chunk, s.carved = cc.ch, true

			// Auto-request the 8 neighbors so a single Request(C) pulls C's neighborhood
			// in. The requested set makes each neighbor request once; the bounded requests
			// channel + singleflight dedup the terrain gen (threat T-10-07: convergence is
			// validated by the 5x5 region test completing).
			for dx := -1; dx <= 1; dx++ {
				for dz := -1; dz <= 1; dz++ {
					if dx == 0 && dz == 0 {
						continue
					}
					np := level.ChunkPos{cc.pos[0] + int32(dx), cc.pos[1] + int32(dz)}
					nk := packPos(np)
					if !w.requested[nk] && w.staging[nk] == nil {
						w.requested[nk] = true
						w.Request(np)
					}
				}
			}

			// Scan the up-to-9 centers this newly carved chunk could have completed.
			for dx := -1; dx <= 1; dx++ {
				for dz := -1; dz <= 1; dz++ {
					cp := level.ChunkPos{cc.pos[0] + int32(dx), cc.pos[1] + int32(dz)}
					w.tryDecorate(ctx, cp)
				}
			}
		}
	}
}

// tryDecorate decorates `center` iff it is staged + carved + NOT decorated AND all 9 of
// its neighborhood are staged + carved. It builds a Neighborhood (sized by w.gen.Dims())
// over the 9 chunks, runs Decorate (Phase 10 no-op: promote-to-full, no writes), marks
// the center decorated, and emits it EXACTLY ONCE. Order-independent (a SCAN, not a
// counter), so the same seed produces identical seam results regardless of request order.
//
// Runs ONLY on the scheduler goroutine (threat T-10-05 / Pitfall 2: decoration touches 9
// chunks and must never run from a handle goroutine).
func (w *Worker) tryDecorate(ctx context.Context, center level.ChunkPos) {
	cs := w.staging[packPos(center)]
	if cs == nil || !cs.carved || cs.decorated {
		return
	}

	chunks := make(map[int64]*level.Chunk, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			np := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
			ns := w.staging[packPos(np)]
			if ns == nil || !ns.carved {
				return // neighborhood incomplete -> wait for the missing neighbor to carve
			}
			chunks[packPos(np)] = ns.chunk
		}
	}

	minY, height := w.gen.Dims()
	view := newNeighborhood(center, chunks, minY, height)
	w.gen.Decorate(view) // Phase 10: NO-OP body that promotes the center to StatusFull
	cs.decorated = true
	cs.chunk.Status = level.StatusFull
	w.emit(ctx, ChunkResult{Pos: center, Chunk: cs.chunk})

	// Keep cs in staging — center is still a neighbor of un-decorated centers. Phase 10's
	// no-op Decorate writes nothing, so emit-and-keep is safe by construction (T-10-06).
	// The feature phase (Phase 11+) MUST revisit the late-neighbor-write-after-emit rule
	// (vanilla re-sends; Sulfur will choose resend-vs-hold) — Open Decision 2, DEFERRED.
}

// emit is the ctx-guarded send to w.results (the immutable single-owner handoff to the
// tick). Once a chunk is emitted, the worker mutates it no further (Phase 10's no-op
// Decorate keeps emit-and-keep safe).
func (w *Worker) emit(ctx context.Context, res ChunkResult) {
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
	return w.gen.GenerateTerrain(pos), nil // region miss -> carved chunk (staged by the scheduler)
}

// tryRegion attempts to read pos from disk, format-aware and OPT-IN: it PREFERS
// the .linear codec (whole-region zstd, OPT-05) when r.<rx>.<rz>.linear exists,
// FALLS BACK to the Anvil .mca codec when only r.<rx>.<rz>.mca exists, and
// otherwise reports a miss so loadOrGenerate generates. This keeps existing
// .mca (vanilla) worlds permanently readable while .linear is only written when
// configured — there is no forced conversion (08-RESEARCH Pitfall 6).
//
//	(nil, false, nil) -> miss (no file / ErrNoSector / ErrNoData): fall through to gen
//	(nil, false, err) -> corrupt region (bad signature / oversized / negative len): surface
//	(ch,  true,  nil) -> hit
//
// In all cases the per-chunk NBT blob (identical between the two codecs) is fed
// into the unchanged save.Chunk.Load -> level.ChunkFromSave path. A fresh region
// file is opened/decoded and closed per call — region.Region and
// region.LinearRegion are Not MT-Safe and are never shared across goroutines.
// This is a codec-only change: the off-tick handle goroutine and the chunkReady
// rejoin are untouched (08-RESEARCH Seam Map OPT-05).
func (w *Worker) tryRegion(pos level.ChunkPos) (*level.Chunk, bool, error) {
	cx, cz := int(pos[0]), int(pos[1])
	rx, rz := region.At(cx, cz)
	ix, iz := region.In(cx, cz)
	base := "r." + strconv.Itoa(rx) + "." + strconv.Itoa(rz)

	// Prefer .linear when present (OPT-05 opt-in read path).
	linearName := filepath.Join(w.regionDir, base+".linear")
	if _, statErr := os.Stat(linearName); statErr == nil {
		data, ok, rerr := readLinearSector(linearName, ix, iz)
		if rerr != nil {
			return nil, false, rerr // corrupt .linear — surface, do not regenerate over it
		}
		if ok {
			return decodeChunk(data)
		}
		// .linear exists but this cell is absent -> miss (fall through to gen).
		return nil, false, nil
	}

	// Fall back to the .mca codec (vanilla compatibility, permanent).
	mcaName := filepath.Join(w.regionDir, base+".mca")
	r, err := region.Open(mcaName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no region file yet -> miss
		}
		return nil, false, err
	}
	defer r.Close()

	data, err := r.ReadSector(ix, iz)
	if err != nil {
		if errors.Is(err, region.ErrNoSector) || errors.Is(err, region.ErrNoData) {
			return nil, false, nil // not-yet-saved chunk -> miss
		}
		// ErrSectorNegativeLength / ErrTooLarge and any IO error: corrupt/real error.
		return nil, false, err
	}
	return decodeChunk(data)
}

// readLinearSector opens a .linear region file, decodes it, and returns the raw
// per-chunk NBT blob for the in-region cell (ix, iz). A fresh decode happens per
// call (Not MT-Safe). An absent cell returns (nil, false, nil); a corrupt file
// (bad signature / decompression bomb / size mismatch) returns an error.
func readLinearSector(name string, ix, iz int) ([]byte, bool, error) {
	lr, err := region.OpenLinear(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err // corrupt .linear surfaced like a corrupt .mca
	}
	data, err := lr.ReadSectorLinear(ix, iz)
	if err != nil {
		if errors.Is(err, region.ErrNoSector) {
			return nil, false, nil // absent cell -> miss
		}
		return nil, false, err
	}
	return data, true, nil
}

// decodeChunk turns a raw per-chunk NBT blob (the SAME blob both codecs return)
// into an in-memory chunk via the unchanged save.Chunk.Load -> ChunkFromSave
// path.
func decodeChunk(data []byte) (*level.Chunk, bool, error) {
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
	return strconv.FormatInt(packPos(pos), 10)
}

// packPos packs (cx,cz) into a stable int64 staging/neighborhood key — the SAME
// packing chunkKey uses, but as the int64 itself (not its decimal string). The
// staging map + the Neighborhood's 3x3 lookup are both keyed by this.
func packPos(p level.ChunkPos) int64 {
	return int64(p[0])<<32 | int64(uint32(p[1]))
}
