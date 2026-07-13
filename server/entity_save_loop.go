package server

import (
	"context"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save"
)

// entity_save_loop.go — the OFF-TICK entity-persistence consumer. It is the entity twin of
// world.ChunkSaver/RunChunkSaveLoop: the tick owner SNAPSHOTS a column's entities (a pure read over
// tick-owned buckets, producing an IMMUTABLE []save.Entities), then ENQUEUES the snapshot here; this
// loop drains the queue and does the MkdirAll + NBT encode + gzip + region WriteSector OFF the tick.
//
// WHY THIS EXISTS (perf, behavior-preserving): the periodic entity autosave (tickChunkSave ->
// ForEachReady -> flushColumnEntities -> saveEntities) previously ran SYNCHRONOUSLY on the tick
// goroutine for EVERY Ready column every save interval. As a player explores, the Ready set grows
// without bound, so that per-interval pass did hundreds of serialize + disk-write (+ MkdirAll)
// syscalls ON the tick, monotonically climbing MSPT and collapsing TPS. Routing the write off-tick —
// the SAME snapshot-on-owner / IO-off-tick discipline the chunk-data path already uses — removes the
// on-tick syscall storm while persisting the IDENTICAL observable state: every Ready column still
// gets its entities saved (including the empty-list write that clears a stale cell), and a snapshot
// that cannot be enqueued (queue full) is DEFERRED (re-saved next pass), never dropped.
//
// CONCURRENCY: only an IMMUTABLE []save.Entities value crosses the seam — no live *Entity, no
// tick-owned bucket — so the loop never touches tick-owned state and the IO is race-free by
// construction. saveEntities itself is unchanged (durable synchronous write); it is merely CALLED
// from this loop's goroutine instead of the tick's, and its MkdirAll is memoized (ensureEntityDir).

// entitySaveSnapshot is the immutable unit crossing the tick->entity-save-loop boundary: the target
// world directory, the owning column, and the FINISHED immutable entity records the owner snapshotted
// (snapshotColumnEntities). Nothing in it aliases tick-owned state, so the loop reads it freely. An
// empty/nil Ents is a valid snapshot: it writes STORE_EMPTY, clearing a previously non-empty cell
// (PersistentEntitySectionManager.storeChunkSections), so it must NOT be filtered out.
type entitySaveSnapshot struct {
	Dir  string
	Pos  level.ChunkPos
	Ents []save.Entities
}

// entitySaveQueueBuffer bounds the tick->loop snapshot channel. Sized like the chunk-save queue: well
// above a single autosave pass's column burst so the owner-side send never parks the tick on a busy
// disk. If it ever fills, the send is non-blocking (Enqueue returns false; the caller falls back to a
// durable inline write so no save is lost) rather than stalling the game loop.
const entitySaveQueueBuffer = 4096

// EntitySaver owns the off-tick entity-save plumbing: the bounded snapshot queue the tick feeds and
// the drain loop that performs the disk IO. It is wired by SetEntitySaver (production); tests that
// leave it nil keep the previous inline-on-tick behavior (the durable synchronous write path), so the
// existing synchronous persistence tests see no change.
type EntitySaver struct {
	queue chan entitySaveSnapshot
}

// NewEntitySaver builds an off-tick entity saver with a bounded queue. The drain goroutine is started
// by main (RunEntitySaveLoop), mirroring the chunk saver.
func NewEntitySaver() *EntitySaver {
	return &EntitySaver{queue: make(chan entitySaveSnapshot, entitySaveQueueBuffer)}
}

// Enqueue offers an immutable entity snapshot to the save loop WITHOUT blocking the tick. It returns
// true if queued, false if the queue is full (the caller then writes durably inline so the save is
// never lost, only occasionally paid on-tick under sustained backpressure). Called ON the tick.
func (s *EntitySaver) Enqueue(snap entitySaveSnapshot) bool {
	select {
	case s.queue <- snap:
		return true
	default:
		return false // queue full: caller falls back to a durable inline write (no lost save)
	}
}

// RunEntitySaveLoop drains the snapshot queue and writes each column's entities to its region file
// OFF the tick. It runs in its OWN goroutine (started by main, like RunChunkSaveLoop). On ctx cancel
// it performs a best-effort FINAL DRAIN of whatever is already queued before returning, so a graceful
// stop does not silently drop the last batch of periodic entity snapshots. (The shutdown FLUSH of all
// loaded columns is done durably+inline on the owner in flushAllLoadedChunksForShutdown, independent
// of this drain, so a full server stop persists every column even if this loop is already gone.)
func (s *EntitySaver) RunEntitySaveLoop(ctx context.Context, logf func(string, ...any)) {
	for {
		select {
		case <-ctx.Done():
			s.drainRemaining(logf)
			return
		case snap := <-s.queue:
			s.write(snap, logf)
		}
	}
}

// drainRemaining flushes every snapshot still buffered at shutdown (a non-blocking receive loop — it
// takes only what is already queued, never waits for more).
func (s *EntitySaver) drainRemaining(logf func(string, ...any)) {
	for {
		select {
		case snap := <-s.queue:
			s.write(snap, logf)
		default:
			return
		}
	}
}

// write performs the durable disk write for one immutable snapshot off-tick. It is the SAME
// saveEntities the inline path uses (durable, MkdirAll-memoized); only the goroutine differs. An IO
// error is logged and the snapshot skipped — one bad column never stalls the loop.
func (s *EntitySaver) write(snap entitySaveSnapshot, logf func(string, ...any)) {
	if err := saveEntities(snap.Dir, snap.Pos, snap.Ents); err != nil {
		logf("entity save %v: %v", snap.Pos, err)
	}
}
