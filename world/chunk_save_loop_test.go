package world

import (
	"context"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestDirtyTracking_SetBlockMarksDirty proves a CHANGED block dirties its column, an unchanged
// SetBlock does not, and DrainDirty returns-and-clears (SUB-PERSIST). Tick-owned single-owner set.
func TestDirtyTracking_SetBlockMarksDirty(t *testing.T) {
	m := NewChunkManager()
	col := level.ChunkPos{0, 0}
	readyChunk(m, col)

	stone := block.ToStateID[block.Stone{}]
	pos := pk.Position{X: 5, Y: 70, Z: 9}

	if m.IsDirty(col) {
		t.Fatal("fresh column should not be dirty")
	}
	if !m.SetBlock(pos, stone, blockTestMinY) {
		t.Fatal("SetBlock(stone) should change")
	}
	if !m.IsDirty(col) {
		t.Fatal("a changed block must dirty its column")
	}

	// An unchanged SetBlock (same state) does not re-dirty after a drain.
	drained := m.DrainDirty()
	if len(drained) != 1 || drained[0] != col {
		t.Fatalf("DrainDirty = %v, want [%v]", drained, col)
	}
	if m.IsDirty(col) {
		t.Fatal("DrainDirty must clear the dirty set")
	}
	if m.SetBlock(pos, stone, blockTestMinY) {
		t.Fatal("SetBlock to the same state must not change")
	}
	if m.IsDirty(col) {
		t.Fatal("an unchanged SetBlock must not dirty")
	}
}

// TestDirtyTracking_RemoveClearsDirty proves unloading a column drops its dirty flag (no stale-flag
// re-save), and MarkDirty is a no-op for a non-Ready column.
func TestDirtyTracking_RemoveClearsDirty(t *testing.T) {
	m := NewChunkManager()
	col := level.ChunkPos{1, 2}
	readyChunk(m, col)
	m.MarkDirty(col)
	if !m.IsDirty(col) {
		t.Fatal("MarkDirty on a Ready column should dirty it")
	}
	m.Remove(col)
	if m.IsDirty(col) {
		t.Fatal("Remove must clear the dirty flag")
	}
	// MarkDirty on an absent/non-Ready column is a no-op.
	m.MarkDirty(col)
	if m.IsDirty(col) {
		t.Fatal("MarkDirty on a non-Ready column must be a no-op")
	}
}

// TestChunkRoundTrip_EditSurvivesReload is the headline SUB-PERSIST round-trip: generate a chunk,
// edit a block, SerializeChunkData, WriteChunkRegion, then reload via the worker's tryRegion path —
// the edited block must survive. This proves the WRITE consumer agrees with the existing READ path.
func TestChunkRoundTrip_EditSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	gen := NewSuperflat(blockTestSecs, blockTestMinY, -1)
	pos := level.ChunkPos{0, 0}

	// Generate a chunk and edit a block in it (the tick-owned mutation).
	ch := gen.Generate(pos)
	stone := block.ToStateID[block.Stone{}]
	// Pick a cell well inside the chunk at a known section/local.
	editPos := pk.Position{X: 3, Y: 70, Z: 11}
	sec, local := sectionLocal(editPos.X, editPos.Y, editPos.Z, blockTestMinY)
	if sec < 0 || sec >= len(ch.Sections) {
		t.Fatalf("edit section %d out of range", sec)
	}
	before := ch.Sections[sec].GetBlock(local)
	if before == stone {
		// Ensure the edit is an actual change so the round-trip is meaningful.
		stone = block.ToStateID[block.Dirt{}]
	}
	ch.Sections[sec].SetBlock(local, stone)

	// Serialize ON the (test stand-in for the) owner, write to region, reload via the worker.
	data, err := SerializeChunkData(nil, pos, ch, blockTestMinY)
	if err != nil {
		t.Fatalf("SerializeChunkData: %v", err)
	}
	if err := WriteChunkRegion(dir, pos, data); err != nil {
		t.Fatalf("WriteChunkRegion: %v", err)
	}

	w := NewWorker(NewSuperflat(blockTestSecs, blockTestMinY, -1), dir, 8)
	reloaded, ok, err := w.tryRegion(pos)
	if err != nil {
		t.Fatalf("tryRegion: %v", err)
	}
	if !ok || reloaded == nil {
		t.Fatal("tryRegion miss: the written chunk must reload")
	}
	got := reloaded.Sections[sec].GetBlock(local)
	if got != stone {
		t.Fatalf("reloaded block at edit cell = %d, want %d (edit lost across save/reload)", got, stone)
	}
}

// TestChunkSaver_Disabled proves an empty region dir disables persistence: Enqueue is a no-op-true
// and RunChunkSaveLoop just waits for ctx (no disk IO).
func TestChunkSaver_Disabled(t *testing.T) {
	s := NewChunkSaver("")
	if s.Enabled() {
		t.Fatal("empty region dir must disable the saver")
	}
	if !s.Enqueue(ChunkSaveSnapshot{Pos: level.ChunkPos{0, 0}, Data: []byte{1}}) {
		t.Fatal("disabled Enqueue must return true (handled, no-op)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunChunkSaveLoop(ctx, t.Logf); close(done) }()
	cancel()
	<-done // returns promptly on cancel
}

// TestChunkSaver_EnqueueWritesRegion proves the off-tick loop drains a snapshot and writes it to a
// region file that the worker can then reload — the full ChunkSaver path end to end.
func TestChunkSaver_EnqueueWritesRegion(t *testing.T) {
	dir := t.TempDir()
	gen := NewSuperflat(blockTestSecs, blockTestMinY, -1)
	pos := level.ChunkPos{2, -3}
	ch := gen.Generate(pos)
	stone := block.ToStateID[block.Stone{}]
	editPos := pk.Position{X: 2<<4 + 4, Y: 72, Z: -3<<4 + 6}
	sec, local := sectionLocal(editPos.X, editPos.Y, editPos.Z, blockTestMinY)
	ch.Sections[sec].SetBlock(local, stone)

	data, err := SerializeChunkData(nil, pos, ch, blockTestMinY)
	if err != nil {
		t.Fatalf("SerializeChunkData: %v", err)
	}

	s := NewChunkSaver(dir)
	if !s.Enabled() {
		t.Fatal("non-empty region dir must enable the saver")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.RunChunkSaveLoop(ctx, t.Logf); close(done) }()

	if !s.Enqueue(ChunkSaveSnapshot{Pos: pos, Data: data}) {
		t.Fatal("Enqueue should accept the snapshot")
	}
	// Stop the loop (triggers the final drain) and wait for it to flush.
	cancel()
	<-done

	// The worker must reload the written chunk with the edit intact.
	w := NewWorker(NewSuperflat(blockTestSecs, blockTestMinY, -1), dir, 8)
	reloaded, ok, err := w.tryRegion(pos)
	if err != nil || !ok || reloaded == nil {
		t.Fatalf("reload after save loop: ok=%v err=%v", ok, err)
	}
	if got := reloaded.Sections[sec].GetBlock(local); got != stone {
		t.Fatalf("reloaded edit = %d, want %d", got, stone)
	}
}
