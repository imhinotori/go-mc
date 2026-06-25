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
	// (runScheduler) drains it. staging + requested + wanted are OWNED EXCLUSIVELY by the
	// scheduler goroutine — NO lock, -race clean by construction (the same single-owner
	// discipline as the tick-owned manager). Never touch them off the scheduler goroutine.
	carved    chan *carvedChunk
	wantedCh  chan level.ChunkPos    // public Request -> scheduler: "this pos is externally wanted"
	staging   map[int64]*stagedChunk // carved-but-(maybe)-not-decorated chunks, packPos keyed
	requested map[int64]bool         // neighbor auto-request dedup (scheduler-owned)
	wanted    map[int64]bool         // externally-requested centers (scheduler-owned)
}

// carvedChunk is the handleTerrain -> scheduler handoff: a freshly carved chunk
// (StatusCarvers) plus its pos. It crosses goroutines exactly once, over w.carved.
type carvedChunk struct {
	pos level.ChunkPos
	ch  *level.Chunk
}

// stagedChunk is a chunk held by the scheduler until its 3x3 neighborhood is carved
// and (D2 Option Y) every wanted neighbor that holds it is decorated. The three flags
// are the Option-Y lifecycle:
//
//	carved=true    once GenerateTerrain has produced its blocks.
//	decorated=true once the LIVE Decorate pass has run over its 3x3 (writing features
//	               into THIS center AND into its neighbors). A decorated center may still
//	               be written into by a neighbor center that has not yet decorated.
//	emitted=true   once it is decorated AND every WANTED neighbor (a wanted center that
//	               holds this center in its 3x3) is decorated — so NO further write can
//	               land. Emit happens exactly once; after emit the chunk is the immutable
//	               single-owner copy the tick holds and is never mutated again.
//
// Splitting decorated from emitted is the whole of Option Y (hold-until-neighborhood-
// complete, complete-on-first-send, no re-send): the INVARIANT is a staged chunk is
// written (decorated-into) only while decorated && !emitted.
type stagedChunk struct {
	pos       level.ChunkPos
	chunk     *level.Chunk
	carved    bool
	decorated bool
	emitted   bool
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
		wantedCh:  make(chan level.ChunkPos, buf),
		staging:   make(map[int64]*stagedChunk),
		requested: make(map[int64]bool),
		wanted:    make(map[int64]bool),
	}
}

// Results is the read-only channel the tick drains for immutable ChunkResults.
func (w *Worker) Results() <-chan ChunkResult { return w.results }

// Request enqueues pos for load/generation. Non-blocking: if the bounded request
// channel is full it drops the request (the tick re-requests next tick), which
// applies backpressure instead of spawning unbounded work.
//
// Request is the EXTERNAL (streamer/tick) entry point: it records pos as a "wanted"
// center, so the scheduler auto-requests pos's 8 neighbors to decorate it. The
// scheduler's own neighbor auto-requests use the internal requestInternal path, which
// does NOT mark the neighbor wanted — this BOUNDS the auto-request frontier to one ring
// around the externally-requested set (a neighbor-ring chunk does not recursively pull
// in ITS neighbors), matching vanilla's "generate the 8 neighbors to decorate a wanted
// chunk" gating instead of expanding outward forever.
func (w *Worker) Request(pos level.ChunkPos) {
	select {
	case w.wantedCh <- pos:
	default:
	}
	w.requestInternal(pos)
}

// requestInternal enqueues pos for terrain generation WITHOUT marking it wanted. Used by
// the scheduler to pull in the neighbor ring of a wanted center. Non-blocking (bounded
// backpressure, same as Request).
func (w *Worker) requestInternal(pos level.ChunkPos) {
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
		ch, fromRegion, e := w.loadOrGenerateEx(pos)
		if e != nil {
			return nil, e
		}
		return loadResult{ch: ch, fromRegion: fromRegion}, nil
	})

	if err != nil {
		w.emit(ctx, ChunkResult{Pos: pos, Err: err})
		return
	}

	lr := v.(loadResult)
	if lr.fromRegion {
		// Region hit (already StatusFull / decorated / saved): emit directly, never stage,
		// never re-decorate. The fromRegion provenance — not the chunk's Status — is the
		// discriminator (see loadOrGenerateEx).
		w.emit(ctx, ChunkResult{Pos: pos, Chunk: lr.ch})
		return
	}

	// Region miss -> carved chunk -> hand to the scheduler for staging + decoration.
	select {
	case <-ctx.Done():
	case w.carved <- &carvedChunk{pos: pos, ch: lr.ch}:
	}
}

// loadResult is the singleflight payload: the chunk plus its provenance (region load vs
// terrain generation). It crosses concurrent same-key waiters, so it must carry the
// provenance explicitly rather than letting waiters re-inspect the (scheduler-mutable)
// chunk Status.
type loadResult struct {
	ch         *level.Chunk
	fromRegion bool
}

// runScheduler is the single goroutine that owns staging + requested + wanted + all
// decoration AND the Option-Y emit gate (no locks -> -race clean by construction; threats
// T-10-05 / T-11-07). It drains w.carved: stages each carved chunk, auto-requests its 8
// neighbors (guarded by the requested set so a single Request(C) pulls C's 3x3 into
// existence exactly once), and runs processRing — decorating the up-to-9 centers this chunk
// could newly complete and emitting each only once all its wanted neighbors are decorated.
func (w *Worker) runScheduler(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case pos := <-w.wantedCh:
			// An externally-requested center: record interest + auto-request its 8 neighbors
			// so the 3x3 it needs to decorate gets generated. Bounded to one ring (neighbors
			// are requested via requestInternal, which does NOT mark them wanted, so they do
			// not recursively expand). If the center is already carved+staged, re-scan it.
			key := packPos(pos)
			if !w.wanted[key] {
				w.wanted[key] = true
				w.requestNeighbors(pos)
			}
			// pos becoming wanted can both let it decorate AND change the emit-gate of its
			// neighbors (it is now a wanted neighbor they must wait on), so process the ring.
			w.processRing(ctx, pos)

		case cc := <-w.carved:
			key := packPos(cc.pos)
			s := w.staging[key]
			if s == nil {
				s = &stagedChunk{pos: cc.pos}
				w.staging[key] = s
			}
			// A pos can be carved more than once: singleflight only dedups CONCURRENT
			// same-key terrain gen, so a directly-requested pos that ALSO gets
			// auto-requested after its first gen completed produces a second carved chunk.
			// Once a chunk is decorated + emitted it is the immutable single-owner copy the
			// tick holds — NEVER overwrite it (that would resurrect it at carvers status and
			// let a later neighborhood-complete scan re-decorate + re-emit it, a double-emit;
			// threat T-10-08). Drop the redundant re-carve. If not yet decorated, record the
			// (deterministically identical) carved chunk and mark it carved.
			if s.decorated {
				break
			}
			s.chunk, s.carved = cc.ch, true

			// If this carved chunk is itself a WANTED center, auto-request its neighbor ring.
			// Ring chunks are NOT wanted, so they do not expand further — the frontier is
			// bounded to one ring around the externally-requested set (threat T-10-07).
			if w.wanted[key] {
				w.requestNeighbors(cc.pos)
			}

			// Scan the up-to-9 centers this newly carved chunk could have completed, but only
			// decorate WANTED centers (the tick asked for them); ring chunks are generated
			// solely to satisfy a wanted center's 3x3 and are never decorated/emitted.
			w.processRing(ctx, cc.pos)
		}
	}
}

// processRing tries to decorate every WANTED center in the 3x3 ring around pos (the
// up-to-9 centers a newly-carved-or-wanted pos could have completed the 3x3 of), then runs
// the emit gate over every center a decoration this turn could have unblocked. It is the
// Option-Y driver: decorate-as-soon-as-carved, emit-once-all-wanted-neighbors-decorated.
//
// A decoration of center D unblocks the emit gate of D AND of D's wanted neighbors (D is a
// wanted neighbor THEY were waiting on). So the emit pass must scan D's OWN 3x3 ring — which
// reaches up to pos±2, beyond the decorate ring. tryDecorate records which centers newly
// decorated this turn; the emit pass then scans each newly-decorated center's 3x3 (a
// superset that includes the centers it could unblock). Both passes are idempotent (guarded
// by the decorated/emitted flags). Scheduler-goroutine-only (no locks).
func (w *Worker) processRing(ctx context.Context, pos level.ChunkPos) {
	var newlyDecorated []level.ChunkPos
	// Decorate pass: a wanted center whose own 3x3 is fully carved decorates now (writing
	// features into its neighbors). Ring chunks are never decorated.
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			cp := level.ChunkPos{pos[0] + int32(dx), pos[1] + int32(dz)}
			if w.wanted[packPos(cp)] && w.tryDecorate(cp) {
				newlyDecorated = append(newlyDecorated, cp)
			}
		}
	}
	// Emit pass: for each center that decorated this turn, re-check the emit gate of it AND
	// its wanted neighbors (its decoration may have been the last write they were waiting on).
	for _, d := range newlyDecorated {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				cp := level.ChunkPos{d[0] + int32(dx), d[1] + int32(dz)}
				if w.wanted[packPos(cp)] {
					w.tryEmit(ctx, cp)
				}
			}
		}
	}
}

// requestNeighbors auto-requests the 8 neighbors of pos (the ring needed to decorate it),
// each once (the requested set dedups; the bounded requests channel + singleflight dedup
// the terrain gen). Uses requestInternal so the neighbors are NOT marked wanted — this is
// what bounds the auto-request frontier to a single ring. Scheduler-goroutine-only.
func (w *Worker) requestNeighbors(pos level.ChunkPos) {
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			np := level.ChunkPos{pos[0] + int32(dx), pos[1] + int32(dz)}
			nk := packPos(np)
			if !w.requested[nk] && w.staging[nk] == nil {
				w.requested[nk] = true
				w.requestInternal(np)
			}
		}
	}
}

// tryDecorate decorates `center` iff it is staged + carved + NOT decorated AND all 9 of
// its neighborhood are staged + carved. It builds a Neighborhood (sized by w.gen.Dims())
// over the 9 chunks, runs the LIVE Decorate (which WRITES features into the center AND its
// neighbors via the 3x3 proxy), and marks the center decorated. It does NOT emit — the
// emit is gated separately by tryEmit (Option Y hold-until-neighborhood-complete).
//
// D2 RESOLVED — Option Y: a center decorates as soon as its own 3x3 is carved, so it can
// write into its neighbors, but it is HELD (not emitted) until every wanted neighbor that
// holds it is also decorated. Splitting decorate from emit is what makes the hold work;
// the decorated && !emitted window is the only time a chunk is mutated. (See the objective:
// each feature's rng is pure over (seed,origin,idx,step), so decorate order does not affect
// the bytes — hold-then-emit is byte-deterministic regardless of which center decorated
// first; the 5x5 reorder test pins it.)
//
// Order-independent (a SCAN, not a counter). Runs ONLY on the scheduler goroutine (threat
// T-10-05 / Pitfall 2: decoration touches 9 chunks and must never run from a handle
// goroutine).
// It returns true iff it decorated center THIS call (so processRing can run the emit gate
// over the centers this decoration could unblock); false if center was not ready or was
// already decorated.
func (w *Worker) tryDecorate(center level.ChunkPos) bool {
	cs := w.staging[packPos(center)]
	if cs == nil || !cs.carved || cs.decorated {
		return false
	}

	chunks := make(map[int64]*level.Chunk, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			np := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
			ns := w.staging[packPos(np)]
			if ns == nil || !ns.carved {
				return false // neighborhood incomplete -> wait for the missing neighbor to carve
			}
			chunks[packPos(np)] = ns.chunk
		}
	}

	minY, height := w.gen.Dims()
	view := newNeighborhood(center, chunks, minY, height)
	w.gen.Decorate(view) // LIVE: writes features into the 3x3, promotes the center to StatusFull
	cs.decorated = true
	cs.chunk.Status = level.StatusFull
	// Do NOT emit here — the emit is gated by tryEmit until every wanted neighbor that holds
	// this center is also decorated (no write lands after the immutable handoff).
	return true
}

// tryEmit emits `center` iff it is decorated, NOT yet emitted, and every WANTED neighbor
// of center (a wanted center in center's |dx|<=1,|dz|<=1 ring) is also decorated — meaning
// no further wanted center will write into center (Option Y's hold-until-complete gate).
// It emits EXACTLY ONCE (the emitted flag) and after emit the chunk is the immutable
// single-owner copy the tick holds — never mutated again (the decorated && !emitted
// invariant from stagedChunk).
//
// A center with NO wanted neighbors (an isolated request) emits as soon as it is itself
// decorated. A non-wanted ring chunk never decorates, so it never gates any emit — the hold
// cannot wedge on an un-requested chunk (threat T-11-09: no hold deadlock). Scheduler-only.
func (w *Worker) tryEmit(ctx context.Context, center level.ChunkPos) {
	cs := w.staging[packPos(center)]
	if cs == nil || !cs.decorated || cs.emitted {
		return
	}
	// Hold until every WANTED neighbor that holds this center in its 3x3 is decorated.
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			np := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
			nk := packPos(np)
			if !w.wanted[nk] {
				continue // a non-wanted ring chunk never decorates -> never gates emit
			}
			ns := w.staging[nk]
			if ns == nil || !ns.decorated {
				return // a wanted neighbor still owes us its cross-border writes -> hold
			}
		}
	}
	cs.emitted = true
	w.emit(ctx, ChunkResult{Pos: center, Chunk: cs.chunk})
	// Keep cs in staging so it still serves as a carved/decorated neighbor for the remaining
	// held centers' tryDecorate/tryEmit scans. It is emitted (immutable) now — never written
	// again: every wanted neighbor that could write into it is already decorated (the gate
	// above), and a non-wanted neighbor never decorates. This is the Option-Y no-write-after-
	// emit guarantee (threat T-11-07).
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
	ch, _, err := w.loadOrGenerateEx(pos)
	return ch, err
}

// loadOrGenerateEx is the discriminating form handleTerrain uses: it reports whether the
// chunk came from a REGION load (fromRegion=true -> already StatusFull, emit directly) or
// from terrain GENERATION (fromRegion=false -> StatusCarvers, stage for decoration).
//
// The fromRegion flag is the CORRECT discriminator — NOT the chunk's Status. The scheduler
// mutates a STAGED (generated) chunk's Status to StatusFull during decoration, and that
// chunk pointer is the one singleflight caches under the pos key. A concurrent same-key
// handleTerrain waiter that read Status would therefore see Full on a GENERATED chunk and
// wrongly emit it directly (a second, un-staged emit -> double-emit; threat T-10-08).
// Keying on the load PROVENANCE instead of the (mutable) status closes that race.
func (w *Worker) loadOrGenerateEx(pos level.ChunkPos) (*level.Chunk, bool, error) {
	if w.regionDir != "" {
		ch, ok, rerr := w.tryRegion(pos)
		if rerr != nil {
			return nil, false, rerr // corrupt region (threat T-4-03) — do not regenerate over it
		}
		if ok {
			return ch, true, nil // region hit -> already decorated/saved
		}
	}
	return w.gen.GenerateTerrain(pos), false, nil // region miss -> carved chunk (staged by the scheduler)
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
