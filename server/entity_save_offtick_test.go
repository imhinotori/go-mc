package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/world"
)

// TestSaveEntitiesMkdirOncePerDir is the PERF gate for the dir-once fix: the first saveEntities for a
// world dir creates the entities/ directory; a SECOND save to the same dir must NOT MkdirAll again
// (the redundant per-column-per-interval MkdirAll syscall storm was the profiled tick stall). We
// assert this by removing the directory out from under the memoized cache after the first write and
// proving ensureEntityDir short-circuits (returns nil without recreating it).
func TestSaveEntitiesMkdirOncePerDir(t *testing.T) {
	dir := t.TempDir()
	loop, _ := newBlockLoop()
	t.Cleanup(loop.Close)
	pos := level.ChunkPos{0, 0}

	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, 64, 8.5)
	initSpawnHealth(pig)
	rec, ok := entityToDisk(pig)
	if !ok {
		t.Fatal("entityToDisk rejected pig")
	}

	// Clear any residue from other tests so this dir is uncached at the start.
	entsDir := filepath.Join(dir, entitiesDir)
	entityDirsCreated.Delete(entsDir)

	// First write: creates the entities/ dir and caches it.
	if err := saveEntities(dir, pos, []save.Entities{rec}); err != nil {
		t.Fatalf("first saveEntities: %v", err)
	}
	if _, cached := entityDirsCreated.Load(entsDir); !cached {
		t.Fatal("entities dir not memoized after first saveEntities")
	}

	// Prove the memoization skips MkdirAll on the 2nd+ call: ensureEntityDir must be a pure cache hit
	// even if the directory is gone (it must NOT touch the filesystem once cached).
	if err := os.RemoveAll(entsDir); err != nil {
		t.Fatalf("remove entities dir: %v", err)
	}
	if err := ensureEntityDir(entsDir); err != nil {
		t.Fatalf("ensureEntityDir on cached dir must be a no-op, got: %v", err)
	}
	if _, err := os.Stat(entsDir); !os.IsNotExist(err) {
		t.Fatal("ensureEntityDir recreated the dir on a cache hit (MkdirAll was NOT skipped)")
	}
}

// TestEntityAutosaveEnqueuesOffTick is the PERF gate for moving the autosave off-tick: when an
// EntitySaver is wired, the periodic autosave pass must ENQUEUE the column's snapshot to the off-tick
// queue rather than writing it inline on the tick. We assert (a) after tickChunkSave the file is NOT
// yet on disk (the enqueue is async), and (b) after draining the loop the file lands with the right
// state — proving the write moved off the tick without changing what gets persisted.
func TestEntityAutosaveEnqueuesOffTick(t *testing.T) {
	dir := t.TempDir()
	loop, _ := newBlockLoop()
	t.Cleanup(loop.Close)
	loop.SetPersistDir(dir)
	loop.SetChunkSaver(world.NewChunkSaver("")) // .mca off; entities-only autosave path
	saver := NewEntitySaver()
	loop.SetEntitySaver(saver)
	pos := level.ChunkPos{0, 0}

	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, 64, 8.5)
	initSpawnHealth(pig)
	pig.health = 9
	loop.regionForColumn(pos).entities.add(pig)

	// Run one periodic autosave pass on the tick. With the saver wired this must only snapshot+enqueue.
	loop.chunkSaveTickCounter = chunkSaveIntervalTicks - 1
	loop.tickChunkSave()

	// The write has NOT happened inline: the off-tick loop has not run yet, so disk is still a miss.
	if _, hit, err := loadEntities(dir, pos); err != nil || hit {
		t.Fatalf("autosave wrote INLINE on the tick (hit=%v err=%v); it must enqueue off-tick", hit, err)
	}

	// Drain the off-tick loop: cancel triggers the final drain of the queued snapshot.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { saver.RunEntitySaveLoop(ctx, t.Logf); close(done) }()
	cancel()
	waitDone(t, done, "entity save loop drain")

	recs, hit, err := loadEntities(dir, pos)
	if err != nil || !hit || len(recs) != 1 || recs[0].Health.FloatValue() != 9 {
		t.Fatalf("off-tick entity write: len=%d hit=%v err=%v recs=%+v", len(recs), hit, err, recs)
	}
}

// TestEntityAutosaveFullQueueFallsBackInline proves a save is DEFERRED-not-lost when the off-tick
// queue is full: autosaveColumnEntities must write durably inline rather than drop the snapshot.
func TestEntityAutosaveFullQueueFallsBackInline(t *testing.T) {
	dir := t.TempDir()
	loop, _ := newBlockLoop()
	t.Cleanup(loop.Close)
	loop.SetPersistDir(dir)
	saver := NewEntitySaver()
	loop.SetEntitySaver(saver)
	pos := level.ChunkPos{0, 0}

	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, 64, 8.5)
	initSpawnHealth(pig)
	pig.health = 4
	loop.regionForColumn(pos).entities.add(pig)

	// Saturate the queue so Enqueue returns false and the autosave must fall back to an inline write.
	for i := 0; i < entitySaveQueueBuffer; i++ {
		saver.queue <- entitySaveSnapshot{Dir: dir, Pos: level.ChunkPos{int32(i + 1), 0}}
	}

	loop.autosaveColumnEntities(pos)

	// With no off-tick consumer draining, the only way the pig landed is the inline fallback write.
	recs, hit, err := loadEntities(dir, pos)
	if err != nil || !hit || len(recs) != 1 || recs[0].Health.FloatValue() != 4 {
		t.Fatalf("full-queue inline fallback: len=%d hit=%v err=%v", len(recs), hit, err)
	}
}
