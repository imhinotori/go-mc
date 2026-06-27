package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
)

// newSwampCache builds a cache + the swamp-hut generator over the fixed test stubs.
func newSwampCache(t *testing.T) (*Cache, StartGenerator, level.ChunkPos) {
	t.Helper()
	gen, err := NewSwampHutStartGen()
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	return cache, gen, level.ChunkPos{swampChunkX, swampChunkZ}
}

// TestStructurePersistRoundTrip: compute starts, WriteChunkStructures into a region NBT, then
// ReadChunkStructures into a FRESH cache (no recompute) — the seeded cache's StartsForChunk must
// match the computed start.
func TestStructurePersistRoundTrip(t *testing.T) {
	cache, gen, pos := newSwampCache(t)

	// Compute + cache the start (the WRITE source).
	computed := cache.ComputeStarts(swampSeed, pos, gen)
	if len(computed) != 1 {
		t.Fatalf("compute: got %d starts want 1", len(computed))
	}

	raw, err := WriteChunkStructures(cache, pos)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Type == nbt.TagEnd || len(raw.Data) == 0 {
		t.Fatal("WriteChunkStructures produced an empty structures compound for a chunk with a start")
	}

	// Round-trip the raw compound through actual NBT bytes (region IO stores it as a RawMessage).
	raw2 := rawThroughNBT(t, raw)

	// READ into a FRESH cache — no generator, no recompute. Seeding must succeed.
	fresh := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	if !ReadChunkStructures(fresh, pos, raw2) {
		t.Fatal("ReadChunkStructures: expected to seed the cache from a present starts tag, got false")
	}

	// The fresh cache now serves the start from NBT (no recompute path was taken).
	seeded, ok := fresh.StartsCachedFor(pos)
	if !ok || len(seeded) != 1 {
		t.Fatalf("seeded cache: ok=%v len=%d want 1", ok, len(seeded))
	}
	startsEqualByPlacement(t, computed[0], seeded[0])
}

// TestStructurePersistNoTagRecomputes: a chunk with NO structures.starts tag (older save) ->
// ReadChunkStructures returns false (the caller recomputes), never panics.
func TestStructurePersistNoTagRecomputes(t *testing.T) {
	fresh := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	pos := level.ChunkPos{swampChunkX, swampChunkZ}

	// An absent/empty structures field (TagEnd) -> recompute fallback (seeded=false).
	if ReadChunkStructures(fresh, pos, nbt.RawMessage{}) {
		t.Fatal("ReadChunkStructures on an empty tag: expected false (recompute), got true")
	}
	if _, ok := fresh.StartsCachedFor(pos); ok {
		t.Fatal("an empty tag must NOT seed the cache")
	}
}

// TestStructurePersistGarbledRecomputes: a garbled structures compound -> ReadChunkStructures
// tolerates + returns false (recompute), never panics (Tampering mitigation T-20-07).
func TestStructurePersistGarbledRecomputes(t *testing.T) {
	fresh := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	pos := level.ChunkPos{swampChunkX, swampChunkZ}

	// A RawMessage claiming to be a compound but holding garbage bytes.
	garbled := nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{0x07, 0xff, 0xff, 0xff, 0xff}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ReadChunkStructures panicked on a garbled tag: %v", r)
		}
	}()
	if ReadChunkStructures(fresh, pos, garbled) {
		t.Fatal("ReadChunkStructures on a garbled tag: expected false (recompute), got true")
	}
}

// TestStructurePersistGarbledPieceRecomputes: a well-formed structures compound whose start
// carries an unknown piece id -> recompute (false), not a partial seed.
func TestStructurePersistGarbledPieceRecomputes(t *testing.T) {
	fresh := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	pos := level.ChunkPos{swampChunkX, swampChunkZ}

	badStart := startTag{ID: "minecraft:swamp_hut", ChunkX: pos[0], ChunkZ: pos[1], Children: []pieceTag{{ID: "minecraft:garbage"}}}
	raw, err := encodeStartTag(badStart)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := save.EncodeStructures(save.StructuresData{
		Starts: map[string]nbt.RawMessage{"minecraft:swamp_hut": raw},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ReadChunkStructures(fresh, pos, sd) {
		t.Fatal("ReadChunkStructures with an unknown piece id: expected false (recompute), got true")
	}
	if _, ok := fresh.StartsCachedFor(pos); ok {
		t.Fatal("a garbled-piece start must NOT seed the cache (no partial seed)")
	}
}

// TestStructurePersistEmptyChunk: a chunk that owns NO starts writes an empty compound, which
// reads back as recompute (false) — a structure-free chunk costs nothing on reload.
func TestStructurePersistEmptyChunk(t *testing.T) {
	cache := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	pos := level.ChunkPos{100, 100} // a non-structure chunk
	// Seed an empty start set (the generator owns nothing here).
	cache.StoreStarts(pos, nil)

	raw, err := WriteChunkStructures(cache, pos)
	if err != nil {
		t.Fatal(err)
	}
	fresh := NewCache(swampSurfaceSampler{}, biomeFn(swampBiomeAt))
	if ReadChunkStructures(fresh, pos, raw) {
		t.Fatal("a structure-free chunk should read back as recompute (false), not seed")
	}
}

// rawThroughNBT round-trips an nbt.RawMessage through an enclosing compound (the way region IO
// stores Chunk.Structures), proving the encode form survives a real marshal/unmarshal.
func rawThroughNBT(t *testing.T, in nbt.RawMessage) nbt.RawMessage {
	t.Helper()
	type wrap struct {
		S nbt.RawMessage `nbt:"structures"`
	}
	data, err := nbt.Marshal(wrap{S: in})
	if err != nil {
		t.Fatal(err)
	}
	var out wrap
	if err := nbt.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out.S
}
