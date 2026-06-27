package structure

import (
	"bytes"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
)

// persistence.go — STRUCT-POLISH-04 (20-03): the cache <-> region structures.Starts seam.
//
// WRITE: when a chunk is saved, serialize the cache's OWN starts for that chunk (StartsForOwner)
// into the chunk's `structures` compound (save.StructuresData) — each start's createTag keyed by
// its structure id, plus the References long-array.
//
// READ: on chunk load, if the chunk carries a `structures.starts` tag, decode it via
// LoadStaticStart and SEED the cache (StoreStarts) so the PLACE/REFERENCES path reads the
// persisted starts INSTEAD of recomputing. If the tag is ABSENT or DECODE FAILS, the caller falls
// back to ComputeStarts — recompute is the SOURCE OF TRUTH (StructureStarts are pure over
// (seed,pos), cache.go Assumption A6), so persistence is a PURE OPTIMIZATION and a missing/garbled
// tag is a robustness no-op, never a panic (T-20-07 Tampering).
//
// This file owns the cache seeder (StoreStarts) + the two-way save.StructuresData <-> cache
// conversion. The actual wire-up into the worker's decodeChunk/save path is additive: the worker
// calls ReadChunkStructures after decoding a region chunk (seed-or-recompute) and
// WriteChunkStructures before saving — neither changes the off-tick discipline (region IO is
// already off-tick; this is additive to the existing decode/save).
//
// Source: javap-derived layout (start_nbt.go / piece_nbt.go) + SerializableChunkData
// packStructureData/unpackStructureStart keys (save/structure.go).

// StoreStarts SEEDS the cache's owned starts for pos WITHOUT recompute — the load-from-NBT entry
// point (the counterpart to ComputeStarts). After StoreStarts, ComputeStarts(seed,pos) returns
// the seeded slice (a cache hit) and StartsForChunk reads them. Idempotent: storing the same pos
// twice overwrites with the (deterministically identical) slice.
//
// Coherence (Pitfall 4): the caller MUST seed only starts that EQUAL what ComputeStarts would
// produce — start_nbt.go's round-trip + recompute-coherence tests prove the NBT format preserves
// that equality, so a faithful WriteChunkStructures -> ReadChunkStructures round-trip seeds a
// recompute-equal slice.
func (c *Cache) StoreStarts(pos level.ChunkPos, starts []*StructureStart) {
	c.starts.Store(packPos(pos), starts)
}

// StartsForOwner returns the starts OWNED by pos (this chunk's own starts, not the neighbor
// starts reaching in) — the set WriteChunkStructures persists into structures.starts. It reads
// the cache populated by ComputeStarts; an un-computed pos returns nil (nothing to persist).
func (c *Cache) StartsForOwner(pos level.ChunkPos) []*StructureStart {
	if v, ok := c.starts.Load(packPos(pos)); ok {
		return v
	}
	return nil
}

// WriteChunkStructures builds the chunk `structures` compound (save.StructuresData) from the
// cache's OWN starts for pos: each VALID start's CreateTag keyed by its structure id, plus the
// chunk's References long-array (the owner keys reaching into pos). An INVALID/empty start is
// skipped (vanilla writes it as id="INVALID", but a chunk that owns no structure simply has no
// `starts` entry — equivalent on reload). Returns the encoded nbt.RawMessage for Chunk.Structures.
//
// This is additive: a chunk with no owned starts and no references produces an empty compound
// (harmless; DecodeStructures reads it back as empty -> recompute fallback).
func WriteChunkStructures(c *Cache, pos level.ChunkPos) (nbt.RawMessage, error) {
	sd := save.StructuresData{
		Starts:     map[string]nbt.RawMessage{},
		References: map[string][]int64{},
	}

	for _, st := range c.StartsForOwner(pos) {
		if !st.IsValid() {
			continue
		}
		tag, err := st.CreateTag(pos)
		if err != nil {
			return nbt.RawMessage{}, err
		}
		raw, err := encodeStartTag(tag)
		if err != nil {
			return nbt.RawMessage{}, err
		}
		sd.Starts[st.Structure] = raw
	}

	// References: the packed owner keys reaching into pos, grouped by the owner start's structure
	// id (vanilla groups References by Structure). We read the cache's References list for pos and
	// resolve each owner's structure id from its cached starts.
	if refs, ok := c.references.Load(packPos(pos)); ok {
		for _, ownerKey := range refs {
			owned, ok := c.starts.Load(ownerKey)
			if !ok {
				continue
			}
			for _, st := range owned {
				if !st.IsValid() {
					continue
				}
				sd.References[st.Structure] = append(sd.References[st.Structure], ownerKey)
			}
		}
	}

	return save.EncodeStructures(sd)
}

// ReadChunkStructures decodes a chunk's `structures` compound and SEEDS the cache for pos, so the
// PLACE/REFERENCES path reads the persisted starts instead of recomputing. It returns:
//
//	seeded=true  -> the cache was seeded from a present, well-formed `starts` tag.
//	seeded=false -> the tag was ABSENT or DECODE FAILED; the caller MUST fall back to recompute
//	                (ComputeStarts) — recompute is the source of truth. NEVER a panic, never an
//	                error propagated to the caller as fatal (a garbled tag is a recompute, T-20-07).
//
// The err return is reserved for a PROGRAMMING error (it is currently always nil — a malformed
// tag yields seeded=false, not err). This keeps the seam infallible from the worker's view: a
// corrupt save degrades to recompute, the worst case being a recompute that was already the
// fallback path.
func ReadChunkStructures(c *Cache, pos level.ChunkPos, raw nbt.RawMessage) (seeded bool) {
	sd, err := save.DecodeStructures(raw)
	if err != nil {
		// Garbled `structures` compound (corrupt/untrusted save) -> recompute (T-20-07).
		return false
	}
	if len(sd.Starts) == 0 {
		// Absent/empty `starts` (older save or a chunk that owns no structure) -> recompute.
		return false
	}

	var starts []*StructureStart
	for _, rawStart := range sd.Starts {
		tag, err := decodeStartTag(rawStart)
		if err != nil {
			// A garbled start compound -> recompute the WHOLE chunk's starts (don't seed a
			// partial/inconsistent set). recompute is the source of truth.
			return false
		}
		st, err := LoadStaticStart(tag)
		if err != nil {
			return false
		}
		if st.IsValid() {
			starts = append(starts, st)
		}
	}
	if len(starts) == 0 {
		// Every start decoded to INVALID/empty -> nothing useful to seed -> recompute.
		return false
	}

	c.StoreStarts(pos, starts)
	return true
}

// encodeStartTag serializes a startTag into an nbt.RawMessage (the opaque per-start compound the
// save.StructuresData.Starts map holds). NetworkFormat (no root name) so the payload is the bare
// compound body, matching nbt.RawMessage.Data semantics (the type byte is stripped + stored
// separately).
func encodeStartTag(tag startTag) (nbt.RawMessage, error) {
	var buf bytes.Buffer
	enc := nbt.NewEncoder(&buf)
	enc.NetworkFormat(true)
	if err := enc.Encode(tag, ""); err != nil {
		return nbt.RawMessage{}, err
	}
	data := buf.Bytes()
	if len(data) == 0 {
		return nbt.RawMessage{}, fmt.Errorf("structure: encodeStartTag produced empty NBT")
	}
	return nbt.RawMessage{Type: data[0], Data: data[1:]}, nil
}

// decodeStartTag parses an opaque per-start RawMessage back into a startTag (the inverse of
// encodeStartTag). A malformed compound surfaces an error so ReadChunkStructures recomputes.
func decodeStartTag(raw nbt.RawMessage) (startTag, error) {
	if raw.Type == nbt.TagEnd || len(raw.Data) == 0 {
		return startTag{}, fmt.Errorf("structure: decodeStartTag: empty start compound")
	}
	var tag startTag
	if err := raw.Unmarshal(&tag); err != nil {
		return startTag{}, err
	}
	return tag, nil
}
