package world

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/save/region"
)

// realChunkBlob builds a current-format (protocol-776) per-chunk NBT blob by
// generating a superflat chunk, converting it via level.ChunkToSave, and
// encoding ONLY the fields level.ChunkFromSave consumes (Sections, Heightmaps,
// Status, YPos). This exercises the real save.Chunk.Load -> level.ChunkFromSave
// round-trip in the loader. (We avoid save.Chunk.Data here because it also
// encodes empty nbt.RawMessage tick fields the nbt encoder rejects — a
// save-layer quirk unrelated to the .linear codec under test; the loader's
// decode path only needs the consumed fields.) The blob uses compression tag 3
// (no compression), which save.Chunk.Load accepts. It returns the blob plus the
// in-region cell (ix, iz) it should be written at.
func realChunkBlob(t *testing.T) (blob []byte, ix, iz int) {
	t.Helper()
	gen := NewSuperflat(24, -64, -1)
	ch := gen.Generate(level.ChunkPos{0, 0})
	var full save.Chunk
	full.YPos = -4 // -64 >> 4
	if err := level.ChunkToSave(ch, &full); err != nil {
		t.Fatalf("ChunkToSave: %v", err)
	}

	// Minimal on-disk shape: only the fields ChunkFromSave reads, so the empty
	// RawMessage tick fields never reach the nbt encoder.
	minimal := struct {
		Sections   []save.Section      `nbt:"sections"`
		Heightmaps map[string][]uint64 `nbt:"Heightmaps"`
		Status     string              `nbt:"Status"`
		YPos       int32               `nbt:"yPos"`
		XPos       int32               `nbt:"xPos"`
		ZPos       int32               `nbt:"zPos"`
	}{
		Sections:   full.Sections,
		Heightmaps: full.Heightmaps,
		Status:     full.Status,
		YPos:       full.YPos,
	}

	var buf bytes.Buffer
	buf.WriteByte(3) // compression tag 3 = none
	if err := nbt.NewEncoder(&buf).Encode(&minimal, ""); err != nil {
		t.Fatalf("encode minimal chunk: %v", err)
	}
	return buf.Bytes(), 0, 0
}

// writeTestMca writes blob into a fresh r.0.0.mca at cell (ix, iz) inside dir.
func writeTestMca(t *testing.T, dir string, ix, iz int, blob []byte) {
	t.Helper()
	name := filepath.Join(dir, "r.0.0.mca")
	r, err := region.Create(name)
	if err != nil {
		t.Fatalf("region.Create: %v", err)
	}
	if err := r.WriteSector(ix, iz, blob); err != nil {
		t.Fatalf("WriteSector: %v", err)
	}
	if err := r.PadToFullSector(); err != nil {
		t.Fatalf("PadToFullSector: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close mca: %v", err)
	}
}

// writeTestLinear writes blob into a fresh r.0.0.linear at cell (ix, iz) inside
// dir.
func writeTestLinear(t *testing.T, dir string, ix, iz int, blob []byte) {
	t.Helper()
	name := filepath.Join(dir, "r.0.0.linear")
	lr := &region.LinearRegion{}
	lr.WriteSectorLinear(ix, iz, blob)
	if err := region.SaveLinear(name, lr); err != nil {
		t.Fatalf("SaveLinear: %v", err)
	}
}

// posForCell maps an in-region cell (ix, iz) of region (0,0) to a ChunkPos.
func posForCell(ix, iz int) level.ChunkPos { return level.ChunkPos{int32(ix), int32(iz)} }

// TestWorkerPrefersLinear: with both r.0.0.linear and r.0.0.mca present, the
// loader reads the .linear (preferred codec).
func TestWorkerPrefersLinear(t *testing.T) {
	blob, ix, iz := realChunkBlob(t)
	dir := t.TempDir()

	// .mca holds the cell too, but we corrupt its on-disk bytes so that IF the
	// loader fell back to .mca it would error — proving .linear was chosen.
	writeTestMca(t, dir, ix, iz, blob)
	corruptMcaPayload(t, dir)
	writeTestLinear(t, dir, ix, iz, blob)

	w := NewWorker(newTestGen(), dir, 8)
	ch, err := w.loadOrGenerate(posForCell(ix, iz))
	if err != nil {
		t.Fatalf("loadOrGenerate returned error (should have read .linear): %v", err)
	}
	if ch == nil {
		t.Fatal("loadOrGenerate returned nil chunk")
	}
}

// TestCrossFormatMcaStillLoads: with ONLY r.0.0.mca present, the format-aware
// loader still loads it (vanilla compatibility permanent).
func TestCrossFormatMcaStillLoads(t *testing.T) {
	blob, ix, iz := realChunkBlob(t)
	dir := t.TempDir()
	writeTestMca(t, dir, ix, iz, blob)

	w := NewWorker(newTestGen(), dir, 8)
	ch, err := w.loadOrGenerate(posForCell(ix, iz))
	if err != nil {
		t.Fatalf("loadOrGenerate(.mca only) error: %v", err)
	}
	if ch == nil {
		t.Fatal("loadOrGenerate(.mca only) returned nil chunk")
	}
}

// TestWorkerLinearLoads: with ONLY r.0.0.linear present, the loader reads it.
func TestWorkerLinearLoads(t *testing.T) {
	blob, ix, iz := realChunkBlob(t)
	dir := t.TempDir()
	writeTestLinear(t, dir, ix, iz, blob)

	w := NewWorker(newTestGen(), dir, 8)
	ch, err := w.loadOrGenerate(posForCell(ix, iz))
	if err != nil {
		t.Fatalf("loadOrGenerate(.linear only) error: %v", err)
	}
	if ch == nil {
		t.Fatal("loadOrGenerate(.linear only) returned nil chunk")
	}
}

// TestWorkerLinearMissGenerates: with neither file present, the loader falls
// through to gen.Generate (the v1 superflat) — the regionDir="" default is
// unaffected.
func TestWorkerLinearMissGenerates(t *testing.T) {
	dir := t.TempDir() // empty: no .linear, no .mca
	w := NewWorker(newTestGen(), dir, 8)

	ch, err := w.loadOrGenerate(level.ChunkPos{0, 0})
	if err != nil {
		t.Fatalf("loadOrGenerate(empty dir) error: %v", err)
	}
	if ch == nil {
		t.Fatal("loadOrGenerate(empty dir) returned nil chunk")
	}
	if got := len(ch.Sections); got != 24 {
		t.Fatalf("generated chunk has %d sections, want 24 (superflat fallback)", got)
	}

	// regionDir="" default: always generate, never touches disk.
	w2 := NewWorker(newTestGen(), "", 8)
	ch2, err := w2.loadOrGenerate(level.ChunkPos{0, 0})
	if err != nil || ch2 == nil {
		t.Fatalf("regionDir=\"\" default: ch=%v err=%v", ch2, err)
	}
}

// TestWorkerCorruptLinearSurfaces: a present-but-corrupt .linear is surfaced as
// an error, never silently regenerated over (same discipline as a corrupt .mca).
func TestWorkerCorruptLinearSurfaces(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "r.0.0.linear")
	if err := os.WriteFile(name, []byte("not a real linear file at all"), 0o666); err != nil {
		t.Fatalf("write corrupt linear: %v", err)
	}
	w := NewWorker(newTestGen(), dir, 8)
	if _, err := w.loadOrGenerate(level.ChunkPos{0, 0}); err == nil {
		t.Fatal("corrupt .linear: loadOrGenerate err = nil, want surfaced error")
	}
}

// corruptMcaPayload overwrites the chunk payload region of the test .mca (after
// the 8KB header) with garbage so any .mca read attempt fails — used to prove
// .linear was preferred.
func corruptMcaPayload(t *testing.T, dir string) {
	t.Helper()
	name := filepath.Join(dir, "r.0.0.mca")
	f, err := os.OpenFile(name, os.O_RDWR, 0o666)
	if err != nil {
		t.Fatalf("open mca for corruption: %v", err)
	}
	defer f.Close()
	// Overwrite the first data sector (offset 8192) with bytes that decode to a
	// huge declared length, tripping ErrTooLarge / a decode failure.
	garbage := make([]byte, 256)
	for i := range garbage {
		garbage[i] = 0xFF
	}
	if _, err := f.WriteAt(garbage, 8192); err != nil {
		t.Fatalf("corrupt mca payload: %v", err)
	}
}
