package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// startsEqualByPlacement asserts two starts are observably equal: same Structure id, ChunkPos,
// References, BBox, piece count, AND every piece draws byte-identical PostProcess output. This
// is the coherence definition (Pitfall 4) — a loaded start that places identical blocks to the
// recomputed one IS the recomputed one for all gameplay purposes.
func startsEqualByPlacement(t *testing.T, want, got *StructureStart) {
	t.Helper()
	if got.Structure != want.Structure {
		t.Fatalf("Structure: got %q want %q", got.Structure, want.Structure)
	}
	if got.ChunkPos != want.ChunkPos {
		t.Fatalf("ChunkPos: got %v want %v", got.ChunkPos, want.ChunkPos)
	}
	if got.References != want.References {
		t.Fatalf("References: got %d want %d", got.References, want.References)
	}
	if got.BBox != want.BBox {
		t.Fatalf("BBox: got %+v want %+v", got.BBox, want.BBox)
	}
	if len(got.Pieces) != len(want.Pieces) {
		t.Fatalf("piece count: got %d want %d", len(got.Pieces), len(want.Pieces))
	}
	for i := range want.Pieces {
		assertPiecePostProcessEqual(t, want.Pieces[i], got.Pieces[i])
	}
}

// assertPiecePostProcessEqual draws both pieces into fresh views over an inflated box and asserts
// byte-identical output (same draw order via the same seeded rng).
func assertPiecePostProcessEqual(t *testing.T, want, got Piece) {
	t.Helper()
	bb := want.BoundingBox()
	box := BoundingBox{bb.MinX - 4, bb.MinY - 4, bb.MinZ - 4, bb.MaxX + 4, bb.MaxY + 4, bb.MaxZ + 4}
	ra := levelgen.NewWorldgenRandom(0)
	ra.SetLargeFeatureSeed(123, 4, 5)
	rb := levelgen.NewWorldgenRandom(0)
	rb.SetLargeFeatureSeed(123, 4, 5)
	va, vb := newMapView(), newMapView()
	want.PostProcess(va, box, level.ChunkPos{0, 0}, ra)
	got.PostProcess(vb, box, level.ChunkPos{0, 0}, rb)
	if len(va.blocks) != len(vb.blocks) {
		t.Fatalf("%T PostProcess block count: want=%d got=%d", want, len(va.blocks), len(vb.blocks))
	}
	for k, v := range va.blocks {
		if vb.blocks[k] != v {
			t.Fatalf("%T PostProcess diverged at %v: want=%d got=%d", want, k, v, vb.blocks[k])
		}
	}
}

// makeSwampHutStart builds the swamp-hut start at the fixed test anchor (seed 5, chunk (0,2)).
func makeSwampHutStart(t *testing.T) (*StructureStart, level.ChunkPos) {
	t.Helper()
	gen, err := NewSwampHutStartGen()
	if err != nil {
		t.Fatal(err)
	}
	pos := level.ChunkPos{swampChunkX, swampChunkZ}
	starts := gen.GenerateStarts(swampSeed, pos, swampSurfaceSampler{}, swampBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected 1 swamp hut start, got %d", len(starts))
	}
	starts[0].References = 3 // a non-zero reference count to prove the field round-trips
	return starts[0], pos
}

// TestStartNBTRoundTrip (MANDATORY): a StructureStart -> CreateTag -> LoadStaticStart yields an
// EQUAL start (id, ChunkX/Z, references, Children pieces all match).
func TestStartNBTRoundTrip(t *testing.T) {
	want, pos := makeSwampHutStart(t)

	tag, err := want.CreateTag(pos)
	if err != nil {
		t.Fatal(err)
	}
	if tag.ID != "minecraft:swamp_hut" {
		t.Fatalf("tag id: got %q want minecraft:swamp_hut", tag.ID)
	}
	if tag.ChunkX != pos[0] || tag.ChunkZ != pos[1] {
		t.Fatalf("tag chunk: got (%d,%d) want (%d,%d)", tag.ChunkX, tag.ChunkZ, pos[0], pos[1])
	}
	if tag.References != 3 {
		t.Fatalf("tag references: got %d want 3", tag.References)
	}
	if len(tag.Children) != len(want.Pieces) {
		t.Fatalf("tag Children: got %d want %d", len(tag.Children), len(want.Pieces))
	}

	got, err := LoadStaticStart(tag)
	if err != nil {
		t.Fatal(err)
	}
	startsEqualByPlacement(t, want, got)
}

// TestStartNBTRoundTripThroughBytes round-trips the startTag through actual NBT bytes (marshal +
// unmarshal) — proving the struct tags encode/decode as the vanilla flat compound, not just the
// in-memory copy.
func TestStartNBTRoundTripThroughBytes(t *testing.T) {
	want, pos := makeSwampHutStart(t)
	tag, err := want.CreateTag(pos)
	if err != nil {
		t.Fatal(err)
	}
	got, err := startTagThroughNBT(t, tag)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadStaticStart(got)
	if err != nil {
		t.Fatal(err)
	}
	startsEqualByPlacement(t, want, loaded)
}

// TestStartNBTInvalid: an empty (no-pieces) start writes id="INVALID" and loads back empty.
func TestStartNBTInvalid(t *testing.T) {
	empty := &StructureStart{} // no pieces -> !IsValid()
	tag, err := empty.CreateTag(level.ChunkPos{9, 9})
	if err != nil {
		t.Fatal(err)
	}
	if tag.ID != "INVALID" {
		t.Fatalf("empty start id: got %q want INVALID", tag.ID)
	}
	if len(tag.Children) != 0 {
		t.Fatalf("empty start Children: got %d want 0", len(tag.Children))
	}
	got, err := LoadStaticStart(tag)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsValid() {
		t.Fatal("INVALID start loaded as a valid start")
	}
	if len(got.Pieces) != 0 {
		t.Fatalf("INVALID start loaded with %d pieces, want 0", len(got.Pieces))
	}
}

// TestStartNBTRecomputeCoherence (the Pitfall-4 / T-20-09 gate): a start loaded from NBT EQUALS
// what ComputeStarts(seed,pos) produces for the same (seed,pos) — because starts are pure over
// (seed,pos). This is the safety net that proves the NBT format does not diverge from recompute.
func TestStartNBTRecomputeCoherence(t *testing.T) {
	gen, err := NewSwampHutStartGen()
	if err != nil {
		t.Fatal(err)
	}
	pos := level.ChunkPos{swampChunkX, swampChunkZ}
	cache := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))

	// recompute is the source of truth.
	recomputed := cache.ComputeStarts(swampSeed, pos, gen)
	if len(recomputed) != 1 {
		t.Fatalf("recompute: got %d starts want 1", len(recomputed))
	}

	// Persist + reload the recomputed start.
	tag, err := recomputed[0].CreateTag(pos)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadStaticStart(tag)
	if err != nil {
		t.Fatal(err)
	}

	// The loaded start must EQUAL the recomputed one (coherence).
	startsEqualByPlacement(t, recomputed[0], loaded)
}

// TestStartNBTGarbledChildrenBound: a startTag claiming an absurd Children count errors (V5 /
// T-20-08 DoS bound), so the seam recomputes rather than OOMs.
func TestStartNBTGarbledChildrenBound(t *testing.T) {
	tag := startTag{ID: "minecraft:swamp_hut", Children: make([]pieceTag, maxChildrenPieces+1)}
	if _, err := LoadStaticStart(tag); err == nil {
		t.Fatal("LoadStaticStart: expected an error on an over-bound Children count, got nil")
	}
}

// TestStartNBTGarbledPiece: a Children entry with an unknown piece id errors (the seam recomputes,
// T-20-07), never panics or silently drops.
func TestStartNBTGarbledPiece(t *testing.T) {
	tag := startTag{ID: "minecraft:swamp_hut", Children: []pieceTag{{ID: "minecraft:garbage"}}}
	if _, err := LoadStaticStart(tag); err == nil {
		t.Fatal("LoadStaticStart: expected an error on an unknown Children piece id, got nil")
	}
}

// biomeFn adapts a func to the BiomeAt type (NewCache wants a BiomeAt; the test stub is a plain
// func with the same signature).
func biomeFn(f func(int, int, int) levelbiome.Type) BiomeAt {
	return func(wx, wy, wz int) levelbiome.Type { return f(wx, wy, wz) }
}

// startTagThroughNBT marshals a startTag to NBT bytes and unmarshals it back — proving the
// struct tags round-trip as the vanilla flat compound on the actual wire.
func startTagThroughNBT(t *testing.T, in startTag) (startTag, error) {
	t.Helper()
	data, err := nbt.Marshal(in)
	if err != nil {
		return startTag{}, err
	}
	var out startTag
	if err := nbt.Unmarshal(data, &out); err != nil {
		return startTag{}, err
	}
	return out, nil
}
