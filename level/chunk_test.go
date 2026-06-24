package level

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// NOTE ON TEST SCOPE (read before trusting a green run):
// These tests are CHEAP REGRESSION, necessary but NOT sufficient. The fork's
// Section read/write are symmetric, so a self-round-trip PASSES even on a
// byte-misaligned wire (the very bug we are fixing here was invisible to a
// round-trip). The AUTHORITATIVE WORLD-02/WORLD-03 correctness gate is the
// vanilla capture-diff in Plan 04-04. Do NOT read a green round-trip as
// "fluid-count short is vanilla-correct" — it only proves read==write.

// freshSection builds a Section whose States/Biomes are initialized exactly
// like EmptyChunk's section init, so it can be a ReadFrom target.
func freshSection() Section {
	return Section{
		States: NewStatesPaletteContainer(16*16*16, 0),
		Biomes: NewBiomesPaletteContainer(4*4*4, 0),
	}
}

// TestSectionRoundTrip proves Section.WriteTo and Section.ReadFrom are
// SYMMETRIC across BlockCount, FluidCount, and a non-trivial states palette.
// (Symmetric, not vanilla — see the file-level note.)
func TestSectionRoundTrip(t *testing.T) {
	stone, ok := block.ToStateID[block.Stone{}]
	if !ok {
		t.Fatal("block.ToStateID[block.Stone{}] not found — block registry not loaded")
	}

	src := freshSection()
	src.BlockCount = 4096
	src.FluidCount = 7 // non-zero to prove the SECOND short survives, not just defaults
	// Fill a non-trivial set of indices with stone so the states palette is
	// no longer a single-value container (forces real bits + palette + longs).
	for i := 0; i < 4096; i++ {
		src.States.Set(i, BlocksState(stone))
	}

	var buf bytes.Buffer
	if _, err := src.WriteTo(&buf); err != nil {
		t.Fatalf("Section.WriteTo: %v", err)
	}

	dst := freshSection()
	if _, err := dst.ReadFrom(&buf); err != nil {
		t.Fatalf("Section.ReadFrom: %v", err)
	}

	if dst.BlockCount != src.BlockCount {
		t.Errorf("BlockCount: got %d, want %d", dst.BlockCount, src.BlockCount)
	}
	if dst.FluidCount != src.FluidCount {
		t.Errorf("FluidCount: got %d, want %d (the second short was dropped/misaligned)", dst.FluidCount, src.FluidCount)
	}
	for i := 0; i < 4096; i++ {
		if dst.GetBlock(i) != BlocksState(stone) {
			t.Fatalf("states palette did not round-trip at index %d: got %d, want %d", i, dst.GetBlock(i), stone)
			break
		}
	}
}

// TestSectionByteLength locks the EXACT encoded length of an empty (all-air,
// single-value) section so that a dropped or extra short fails immediately.
//
// Length formula (jar: LevelChunkSection.write):
//
//	2  bytes  nonEmptyBlockCount short
//	2  bytes  fluidCount short            <- the fix; total 4 bytes of shorts
//	S  bytes  states PaletteContainer (no VarInt length prefix, LSB longs)
//	B  bytes  biomes PaletteContainer (no VarInt length prefix, LSB longs)
//
// For an all-air single-value container S and B are the single-value encoding
// (1 bits byte + VarInt palette value + VarInt 0 data-len). We compute S and B
// from the actual container encoders rather than hard-coding, then assert the
// total is exactly 4 + S + B — i.e. precisely the two shorts of overhead.
func TestSectionByteLength(t *testing.T) {
	sec := freshSection() // BlockCount=0, FluidCount=0, single-value air states/biomes

	// Measure the states and biomes container encodings directly.
	var sBuf, bBuf bytes.Buffer
	if _, err := sec.States.WriteTo(&sBuf); err != nil {
		t.Fatalf("States.WriteTo: %v", err)
	}
	if _, err := sec.Biomes.WriteTo(&bBuf); err != nil {
		t.Fatalf("Biomes.WriteTo: %v", err)
	}

	const shortsBytes = 2 + 2 // blockCount short + fluidCount short
	want := shortsBytes + sBuf.Len() + bBuf.Len()

	var secBuf bytes.Buffer
	n, err := sec.WriteTo(&secBuf)
	if err != nil {
		t.Fatalf("Section.WriteTo: %v", err)
	}
	if int(n) != secBuf.Len() {
		t.Fatalf("WriteTo returned n=%d but wrote %d bytes", n, secBuf.Len())
	}
	if secBuf.Len() != want {
		t.Errorf("section encoded length: got %d, want %d (= 4 shorts-bytes + %d states + %d biomes); a missing/extra short shifts this",
			secBuf.Len(), want, sBuf.Len(), bBuf.Len())
	}
}

// TestChunkHeightmapsClientSet locks WORLD-03: Chunk.WriteTo must emit EXACTLY
// the 3 Usage.CLIENT heightmaps — WORLD_SURFACE (1), MOTION_BLOCKING (4),
// MOTION_BLOCKING_NO_LEAVES (5) — and drop the WORLDGEN/LIVE_WORLD ids 0/2/3.
func TestChunkHeightmapsClientSet(t *testing.T) {
	c := EmptyChunk(24) // all 6 heightmap BitStorages non-nil

	var buf bytes.Buffer
	if _, err := c.WriteTo(&buf); err != nil {
		t.Fatalf("Chunk.WriteTo: %v", err)
	}

	// The chunk packet body leads with pk.Array(hmEntries). Decode just that
	// prefix to inspect exactly which heightmap types were written.
	var hmEntries []heightMapEntry
	r := bytes.NewReader(buf.Bytes())
	if _, err := pk.Array(&hmEntries).ReadFrom(r); err != nil {
		t.Fatalf("decode heightmap array: %v", err)
	}

	if len(hmEntries) != 3 {
		t.Fatalf("heightmap entries: got %d, want 3 (only the Usage.CLIENT set)", len(hmEntries))
	}

	got := map[int32]bool{}
	for _, e := range hmEntries {
		got[e.Type] = true
	}
	for _, want := range []int32{1, 4, 5} {
		if !got[want] {
			t.Errorf("missing required CLIENT heightmap id %d", want)
		}
	}
	for _, forbidden := range []int32{0, 2, 3} {
		if got[forbidden] {
			t.Errorf("forbidden non-CLIENT heightmap id %d was written (WORLDGEN/LIVE_WORLD must be dropped)", forbidden)
		}
	}
}
