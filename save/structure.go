package save

import (
	"bytes"

	"github.com/imhinotori/sulfur/nbt"
)

// structure.go — STRUCT-POLISH-04 (20-03): the chunk-level `structures` NBT compound seam.
//
// A literal 1:1 port of the vanilla chunk `structures` compound layout (verified vs
// temp/cache/26.2-inner.jar SerializableChunkData.packStructureData):
//
//	structures: {
//	  starts:      { <structure id>: <StructureStart.createTag compound> },   // lowercase key
//	  References:  { <structure id>: <LongArray of owner chunk keys> },        // capital R
//	}
//
// This save-layer type keeps each start compound OPAQUE (an nbt.RawMessage) — the start NBT
// schema (StructureStart.createTag / per-piece) lives in world/structure, a HIGHER layer that
// imports save. Keeping the opaque seam here avoids an import cycle (save must not depend on
// world/structure) while still letting save.Chunk.Structures round-trip the compound losslessly.
//
// The compound rides save.Chunk.Structures (an nbt.RawMessage that already round-trips through
// region IO). EncodeStructures/DecodeStructures convert between the typed StructuresData and that
// raw field.
//
// Source: javap -c net.minecraft.world.level.chunk.storage.SerializableChunkData.packStructureData
// (keys "starts" + "References") + StructureStart.createTag.

// StructuresData is the typed chunk `structures` compound: the per-structure-id start compounds
// (opaque RawMessages) + the per-structure-id References long-arrays.
type StructuresData struct {
	// Starts maps a structure id (e.g. "minecraft:swamp_hut") to its StructureStart.createTag
	// compound (opaque at this layer — world/structure encodes/decodes it). Vanilla key "starts".
	Starts map[string]nbt.RawMessage `nbt:"starts"`
	// References maps a structure id to the packed owner-chunk keys reaching into this chunk.
	// Vanilla key "References" (capital R). Empty sets are omitted (matching packStructureData's
	// isEmpty skip).
	References map[string][]int64 `nbt:"References"`
}

// EncodeStructures serializes a StructuresData into an nbt.RawMessage suitable for
// Chunk.Structures. An empty StructuresData (no starts, no references) encodes as an empty
// compound — harmless on reload (DecodeStructures reads it back as empty -> recompute fallback).
func EncodeStructures(sd StructuresData) (nbt.RawMessage, error) {
	if sd.Starts == nil {
		sd.Starts = map[string]nbt.RawMessage{}
	}
	if sd.References == nil {
		sd.References = map[string][]int64{}
	}
	var buf bytes.Buffer
	// NetworkFormat (no root name) so the payload is the bare compound body — the form
	// nbt.RawMessage.Data holds (matching how the chunk loader stores raw compound payloads).
	enc := nbt.NewEncoder(&buf)
	enc.NetworkFormat(true)
	if err := enc.Encode(sd, ""); err != nil {
		return nbt.RawMessage{}, err
	}
	data := buf.Bytes()
	// The first byte is the tag type (TagCompound); RawMessage.Data is the payload AFTER the
	// type byte (UnmarshalNBT tees the body, not the type). Strip the leading type byte.
	return nbt.RawMessage{Type: data[0], Data: data[1:]}, nil
}

// DecodeStructures parses a Chunk.Structures RawMessage into a StructuresData. A nil/empty/absent
// field (TagEnd or no data) decodes as the zero StructuresData (no starts) — the caller then
// falls back to recompute (the on-disk `structures` tag is a pure optimization). A malformed
// compound surfaces an error so the caller recomputes, never panics (T-20-07 Tampering).
func DecodeStructures(raw nbt.RawMessage) (StructuresData, error) {
	if raw.Type == nbt.TagEnd || len(raw.Data) == 0 {
		return StructuresData{}, nil
	}
	var sd StructuresData
	if err := raw.Unmarshal(&sd); err != nil {
		return StructuresData{}, err
	}
	return sd, nil
}
