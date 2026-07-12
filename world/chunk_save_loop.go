package world

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save/region"
)

// chunk_save_loop.go — SUB-PERSIST: the OFF-TICK chunk-save consumer. It is the missing CONSUMER
// the SUMMARYs flagged: world.SerializeChunkData (chunk_save.go) already produces a region-ready
// per-chunk NBT blob, and save/region.WriteSector already writes a sector — what was absent is the
// drain-and-write loop that ties a dirty/unloaded chunk to its region file OFF the tick goroutine.
//
// CONCURRENCY MODEL (TICK-05, mirrors server/persistence.go's leaveSnapshots→RunSaveLoop):
//   - The tick goroutine OWNS the ChunkManager and every level.Chunk in it. It is the SOLE reader
//     of tick-owned chunk state.
//   - The tick's save phase (server.tickChunkSave) SERIALIZES each dirty/unloaded chunk ON THE
//     OWNER goroutine via SerializeChunkData (a pure READ over the chunk — no mutation), producing
//     an IMMUTABLE []byte. The bytes are the snapshot: no live tick-owned pointer (no *level.Chunk,
//     no *PaletteContainer) crosses the goroutine boundary — only the finished bytes do.
//   - It sends a ChunkSaveSnapshot{pos, data} on the bounded saveQueue. RunChunkSaveLoop (this file,
//     its OWN goroutine) drains the queue and does the region IO (open region, WriteSector, close).
//
// Because only IMMUTABLE bytes cross the seam, the loop NEVER touches tick-owned state and the save
// IO is provably race-free by construction (no shared mutable memory). This is the SAME proven seam
// the player .dat path uses (snapshot on owner → IO off-tick), applied to chunks.
//
// IO THROTTLE: a weighted semaphore caps concurrent region writes (the stack's golang.org/x/sync/
// semaphore) so a save burst (many chunks dirtied in one tick, or a mass unload) does not spawn an
// unbounded fan of disk writers. Region files are Not-MT-Safe, but each write opens+closes its OWN
// region handle (never shares one across goroutines), so the only contention is the OS file — the
// semaphore bounds that, it is not a correctness lock.

// ChunkSaveSnapshot is the immutable unit crossing the tick→save-loop boundary: a column position
// plus the FINISHED region-ready NBT bytes SerializeChunkData produced on the owner. Nothing in it
// aliases tick-owned state (the bytes are a fresh allocation), so the save loop reads it freely.
type ChunkSaveSnapshot struct {
	Pos  level.ChunkPos
	Data []byte
}

// ChunkSaveQueueBuffer bounds the tick→save-loop snapshot channel. Sized well above a single tick's
// dirty-chunk burst for a clamped view ring so the owner-side send (in the tick's save phase) never
// parks the tick on a busy disk; if it ever filled, the send is non-blocking (the tick drops the
// save and re-marks dirty next pass) rather than stalling the game loop.
const ChunkSaveQueueBuffer = 512

// maxConcurrentRegionWrites caps simultaneous region-file writes (the IO throttle). Region writes
// are short (one sector) but disk-bound; a small cap keeps a mass-unload save burst from thrashing
// the disk while still overlapping IO with compute. Tuned conservative (4) — raise only if profiling
// shows the save loop is IO-starved.
const maxConcurrentRegionWrites = 4

// ChunkSaver owns the off-tick chunk-save plumbing: the bounded snapshot queue the tick feeds and
// the IO-throttle semaphore. The tick gets the send end (Enqueue) and the loop drains it
// (RunChunkSaveLoop). regionDir is the world's region directory (worldDir/region) the .mca files
// live under — the SAME directory the worker reads from (tryRegion), so a saved chunk is reloaded
// by the existing load path with no extra wiring.
type ChunkSaver struct {
	regionDir       string
	queue           chan ChunkSaveSnapshot
	sem             *semaphore.Weighted
	producerStarted chan struct{}
	producerDone    chan struct{}
	startOnce       sync.Once
	doneOnce        sync.Once
}

// NewChunkSaver builds a saver writing region files under regionDir. An empty regionDir disables
// persistence (Enqueue becomes a no-op, mirroring an empty worldDir disabling player saves) so
// tests and ephemeral runs need no disk. The queue is bounded; the semaphore caps concurrent IO.
func NewChunkSaver(regionDir string) *ChunkSaver {
	return &ChunkSaver{
		regionDir:       regionDir,
		queue:           make(chan ChunkSaveSnapshot, ChunkSaveQueueBuffer),
		sem:             semaphore.NewWeighted(maxConcurrentRegionWrites),
		producerStarted: make(chan struct{}),
		producerDone:    make(chan struct{}),
	}
}

// MarkProducerStarted/MarkProducerDone bracket the tick owner's final snapshot pass. They let the
// IO loop distinguish a standalone cancellation from a real server shutdown and keep draining
// until all final loaded-chunk snapshots have been handed off.
func (s *ChunkSaver) MarkProducerStarted() {
	if s != nil {
		s.startOnce.Do(func() { close(s.producerStarted) })
	}
}
func (s *ChunkSaver) MarkProducerDone() {
	if s != nil {
		s.doneOnce.Do(func() { close(s.producerDone) })
	}
}

// Enabled reports whether persistence is on (a non-empty region dir). The tick checks it before
// doing the (non-trivial) serialize work so a no-persistence run skips serialization entirely.
func (s *ChunkSaver) Enabled() bool { return s != nil && s.regionDir != "" }

// Enqueue offers an immutable chunk snapshot to the save loop WITHOUT blocking the tick. It returns
// true if queued, false if the queue is full (the tick then leaves/re-marks the column dirty so the
// next save pass retries — a dropped save is never a lost save, just a deferred one). A no-op
// (returns true, "nothing to do") when persistence is disabled. Called ON the tick goroutine.
func (s *ChunkSaver) Enqueue(snap ChunkSaveSnapshot) bool {
	if !s.Enabled() {
		return true // persistence off: treat as "handled" so the caller clears its dirty flag
	}
	select {
	case s.queue <- snap:
		return true
	default:
		return false // queue full: caller re-marks dirty, retried next pass (no lost save)
	}
}

// EnqueueDurable is the shutdown-only blocking handoff. The consumer remains alive until
// MarkProducerDone, so a full bounded queue backpressures shutdown without losing the snapshot.
func (s *ChunkSaver) EnqueueDurable(snap ChunkSaveSnapshot) {
	if s.Enabled() {
		s.queue <- snap
	}
}

// RunChunkSaveLoop drains the snapshot queue and writes each chunk to its region file OFF the tick
// (SUB-PERSIST). It runs in its OWN goroutine (started by main, like RunSaveLoop) and returns when
// ctx is cancelled. Each write opens+closes its own region handle (region.Region is Not-MT-Safe and
// is NEVER shared across goroutines), under the IO-throttle semaphore. A write error is logged via
// the supplied logf and SKIPPED — a single bad chunk never stalls the loop or crashes the server.
//
// On ctx cancel it performs a best-effort FINAL DRAIN of whatever is already queued (a clean
// shutdown flushes pending saves) before returning, so a graceful stop does not silently drop the
// last batch of edits.
func (s *ChunkSaver) RunChunkSaveLoop(ctx context.Context, logf func(string, ...any)) {
	if !s.Enabled() {
		<-ctx.Done() // persistence off: nothing to drain, just wait for shutdown
		return
	}
	for {
		select {
		case <-ctx.Done():
			s.drainUntilProducerDone(logf)
			return
		case snap := <-s.queue:
			s.writeSnapshot(snap, logf)
		}
	}
}

func (s *ChunkSaver) drainUntilProducerDone(logf func(string, ...any)) {
	select {
	case <-s.producerStarted:
		for {
			select {
			case snap := <-s.queue:
				s.writeSnapshot(snap, logf)
			case <-s.producerDone:
				s.drainRemaining(logf)
				return
			}
		}
	default:
		s.drainRemaining(logf)
	}
}

// drainRemaining flushes every snapshot still buffered in the queue at shutdown (a non-blocking
// receive loop — it takes only what is already queued, never waits for more). Uses a background
// context for the semaphore so a cancelled ctx does not abort the final flush.
func (s *ChunkSaver) drainRemaining(logf func(string, ...any)) {
	for {
		select {
		case snap := <-s.queue:
			s.writeSnapshot(snap, logf)
		default:
			return
		}
	}
}

// writeSnapshot writes one immutable snapshot to its region file under the IO-throttle semaphore.
// It acquires a semaphore slot (bounding concurrent disk writers), opens/creates the region, writes
// the sector, and closes the region. All steps operate ONLY on the immutable snapshot bytes and a
// fresh region handle — no tick-owned state is touched (race-free by construction). Any IO error is
// logged and the snapshot is skipped (one bad chunk never stalls the loop).
//
// The semaphore acquire uses context.Background() (NOT the loop's ctx): the semaphore is a THROTTLE
// that always eventually frees, not a cancellation point. Aborting an in-hand write on ctx-cancel
// would DROP a snapshot the loop already dequeued (a lost save), so a write that has reached this
// point always completes — even during the shutdown drain. The slot count bounds concurrency, so
// the acquire blocks at most until another writer finishes (a few ms), never indefinitely.
func (s *ChunkSaver) writeSnapshot(snap ChunkSaveSnapshot, logf func(string, ...any)) {
	if err := s.sem.Acquire(context.Background(), 1); err != nil {
		return // background ctx never cancels: this is unreachable, but stays defensive
	}
	defer s.sem.Release(1)

	if err := WriteChunkRegion(s.regionDir, snap.Pos, snap.Data); err != nil {
		logf("chunk save %v: %v", snap.Pos, err)
	}
}

// WriteChunkRegion writes a single region-ready chunk blob into regionDir/r.<rx>.<rz>.mca at the
// in-region cell for pos, REUSING save/region wholesale (open-or-create + WriteSector + close). It
// is the one place the .mca write happens, so the worker's READ path (tryRegion, same naming) and
// this WRITE path agree on the file layout by construction. The region dir is created on demand.
//
// A fresh region handle is opened and closed per call — region.Region is Not-MT-Safe; the save loop
// never shares one across goroutines (each write is self-contained). Concurrency to the SAME region
// file is serialized by the OS (and bounded by the saver's semaphore); two chunks in one region
// written back-to-back each open the file fresh, read the current header, and write — the standard
// Anvil pattern.
func WriteChunkRegion(regionDir string, pos level.ChunkPos, data []byte) error {
	cx, cz := int(pos[0]), int(pos[1])
	rx, rz := region.At(cx, cz)
	ix, iz := region.In(cx, cz)

	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		return err
	}
	mcaName := filepath.Join(regionDir, "r."+strconv.Itoa(rx)+"."+strconv.Itoa(rz)+".mca")

	r, err := region.Open(mcaName)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		r, err = region.Create(mcaName)
		if err != nil {
			return err
		}
	}
	defer r.Close()

	return r.WriteSector(ix, iz, data)
}
