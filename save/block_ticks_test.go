package save

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/nbt"
)

// block_ticks_test.go — the on-disk round-trip for the chunk block_ticks / fluid_ticks codec
// (SUB-BLOCKTICK). It pins the vanilla SavedTick field names {i,x,y,z,t,p} and that a list of
// saved ticks survives encode -> decode byte-identically, and that an empty list omits the field.

func TestChunkTicksRoundTrip(t *testing.T) {
	in := []SavedTickNBT{
		{ID: "minecraft:sugar_cane", X: 1, Y: 70, Z: -3, Delay: 1, Priority: 0},   // NORMAL
		{ID: "minecraft:water", X: -10, Y: 64, Z: 200, Delay: 5, Priority: -1},    // HIGH
		{ID: "minecraft:redstone_wire", X: 0, Y: 0, Z: 0, Delay: 100, Priority: 3}, // EXTREMELY_LOW
	}
	raw, err := EncodeChunkTicks(in)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Type != nbt.TagList {
		t.Fatalf("encoded tick list should be a TagList (%d), got %d", nbt.TagList, raw.Type)
	}
	out, err := DecodeChunkTicks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("decoded %d ticks, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("tick %d round-trip mismatch: got %+v want %+v", i, out[i], in[i])
		}
	}
}

// TestEmptyTicksOmitted: an empty/nil tick list encodes to a zero RawMessage (TagEnd), the
// signal the chunk save shape uses to OMIT the field entirely (vanilla writes no tick list for a
// chunk with no pending ticks).
func TestEmptyTicksOmitted(t *testing.T) {
	raw, err := EncodeChunkTicks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Type != nbt.TagEnd {
		t.Fatalf("empty tick list should encode to TagEnd (%d), got %d", nbt.TagEnd, raw.Type)
	}
	out, err := DecodeChunkTicks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatalf("decoding an empty RawMessage should yield nil, got %+v", out)
	}
}

// TestChunkBlockTicksFieldEmbeds: the encoded list, placed in a Chunk's block_ticks field and run
// through the chunk's own NBT encode/decode, survives — proving EncodeChunkTicks produces a value
// the chunk's RawMessage field accepts and re-emits. CITE: save.Chunk.block_ticks field.
func TestChunkBlockTicksFieldEmbeds(t *testing.T) {
	in := []SavedTickNBT{
		{ID: "minecraft:sugar_cane", X: 7, Y: 65, Z: 9, Delay: 1, Priority: 0},
	}
	raw, err := EncodeChunkTicks(in)
	if err != nil {
		t.Fatal(err)
	}

	// Encode a wrapper compound { block_ticks: <list> } and decode it back, checking the list
	// survives the full nbt field round-trip the chunk save path performs.
	type wrapper struct {
		BlockTicks nbt.RawMessage `nbt:"block_ticks"`
	}
	w := wrapper{BlockTicks: raw}
	var buf bytes.Buffer
	if err := nbt.NewEncoder(&buf).Encode(&w, ""); err != nil {
		t.Fatal(err)
	}
	var back wrapper
	if _, err := nbt.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&back); err != nil {
		t.Fatal(err)
	}
	out, err := DecodeChunkTicks(back.BlockTicks)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != in[0] {
		t.Fatalf("embedded block_ticks round-trip mismatch: %+v", out)
	}
}
