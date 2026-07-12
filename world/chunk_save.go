package world

import (
	"bytes"
	"fmt"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/world/structure"
)

// chunk_save.go — STRUCT-POLISH-04 (gap closure): the chunk SAVE seam that emits a chunk's
// computed StructureStarts into the persisted `structures` compound, the WRITE half of the
// persistence round-trip (the READ half is worker.decodeAndSeed -> ReadChunkStructures).
//
// There is no production chunk-flush caller yet (the worker is region-READ + always-generate),
// so this seam is the function any future chunk-flush (a RunSaveLoop chunk consumer) calls to
// serialize a decorated chunk back to region bytes WITH its starts. Wiring it here — rather than
// in level.ChunkToSave — keeps the save layer free of a world/structure import (structure imports
// level; the cycle lives only at the world layer). It is the symmetric counterpart the 20-03
// SUMMARY documented as the one-line additive write call site.

// SerializeChunkData builds the region-ready per-chunk NBT blob for ch at pos, populating the
// `structures` compound from the structure cache's OWN starts for pos (WriteChunkStructures) so a
// saved structure chunk carries its starts and a reload SEEDS the cache instead of recomputing
// (STRUCT-POLISH-04). cache may be nil (a structure-free generator) — then no `structures` tag is
// written and a reload simply recomputes (the always-valid fallback).
//
// minY is the generator's world-bottom (from Dims()); the chunk's bottom section index is
// minY>>4 (the YPos the loader subtracts to map section Y -> slice index). level.Chunk does not
// store minY, so the caller (which has it via gen.Dims()) threads it in.
//
// The blob is a compression-tag-3 (no compression) NBT compound the worker's decodeChunk reads
// back via save.Chunk.Load. It serializes ONLY the fields ChunkFromSave consumes plus the
// `structures` compound, avoiding the empty-RawMessage tick fields the nbt encoder rejects (the
// same minimal shape the .linear codec round-trip uses). The structures write is a PURE
// optimization layered on the unchanged chunk bytes: it does not touch sections/heightmaps/status.
func SerializeChunkData(cache *structure.Cache, pos level.ChunkPos, ch *level.Chunk, minY int, blockTicks, fluidTicks []save.SavedTickNBT) ([]byte, error) {
	var full save.Chunk
	full.XPos = pos[0]
	full.ZPos = pos[1]
	full.YPos = int32(minY >> 4)
	if err := level.ChunkToSave(ch, &full); err != nil {
		return nil, fmt.Errorf("world: SerializeChunkData: ChunkToSave: %w", err)
	}

	// STRUCT-POLISH-04 WRITE: emit the chunk's OWN starts (+ References) into the `structures`
	// compound from the cache. A structure-free chunk (or a nil cache) yields an empty compound
	// that reads back as recompute — harmless. A WriteChunkStructures error is a programming/encode
	// bug (not runtime input), surfaced so the caller does not persist a malformed tag.
	var structures nbt.RawMessage
	if cache != nil {
		raw, err := structure.WriteChunkStructures(cache, pos)
		if err != nil {
			return nil, fmt.Errorf("world: SerializeChunkData: WriteChunkStructures: %w", err)
		}
		structures = raw
	}

	// Minimal on-disk shape: only the fields ChunkFromSave reads, plus the `structures` compound.
	// This avoids the empty RawMessage tick fields (block_ticks/fluid_ticks/...) the nbt encoder
	// rejects when save.Chunk.Data encodes the full struct — the same quirk worker_linear_test
	// works around. The `structures` field is omitted when empty (TagEnd) so a structure-free
	// chunk encodes cleanly.
	minimal := chunkSaveShape{
		Sections:      full.Sections,
		Heightmaps:    full.Heightmaps,
		Status:        full.Status,
		YPos:          full.YPos,
		XPos:          full.XPos,
		ZPos:          full.ZPos,
		BlockEntities: full.BlockEntities,
		// DataVersion + InhabitedTime + isLightOn mirror the SerializableChunkData.write header:
		// NbtUtils.addCurrentDataVersion (4903 for 26.2), putLong "InhabitedTime", putBoolean
		// "isLightOn". They are carried through save.Chunk (ChunkToSave) so the values survive a
		// reload; DataVersion is stamped here since it is a global constant, not chunk state.
		DataVersion:   chunkSaveDataVersion,
		InhabitedTime: full.InhabitedTime,
		IsLightOn:     full.IsLightOn != 0,
	}
	// block_ticks / fluid_ticks: encode the server-provided pending scheduled ticks (packed to a
	// delay relative to the save game-time). An empty list encodes to a zero RawMessage (TagEnd) so
	// the pointer stays nil and the field is omitted -- exactly vanilla's "no tick list for a chunk
	// with no pending ticks". A repeater mid-delay or water mid-spread now survives an unload/reload
	// instead of dying. CITE: SerializableChunkData.write (block_ticks/fluid_ticks via SavedTick.codec).
	if bt, err := save.EncodeChunkTicks(blockTicks); err != nil {
		return nil, fmt.Errorf("world: SerializeChunkData: encode block_ticks: %w", err)
	} else if bt.Type != nbt.TagEnd && len(bt.Data) > 0 {
		minimal.BlockTicks = &bt
	}
	if ft, err := save.EncodeChunkTicks(fluidTicks); err != nil {
		return nil, fmt.Errorf("world: SerializeChunkData: encode fluid_ticks: %w", err)
	} else if ft.Type != nbt.TagEnd && len(ft.Data) > 0 {
		minimal.FluidTicks = &ft
	}
	if structures.Type != nbt.TagEnd && len(structures.Data) > 0 {
		minimal.Structures = &structures
	}
	// Carry the PostProcessing fluid marks ChunkToSave produced (the aquifer/carver water-flow
	// flags) so a reloaded chunk's water flows. Omitted when the chunk has no marks.
	if full.PostProcessing.Type != nbt.TagEnd && len(full.PostProcessing.Data) > 0 {
		pp := full.PostProcessing
		minimal.PostProcessing = &pp
	}

	var buf bytes.Buffer
	buf.WriteByte(3) // compression tag 3 = none (save.Chunk.Load accepts it)
	if err := nbt.NewEncoder(&buf).Encode(&minimal, ""); err != nil {
		return nil, fmt.Errorf("world: SerializeChunkData: encode chunk: %w", err)
	}
	return buf.Bytes(), nil
}

// chunkSaveShape is the minimal serialized chunk: the fields ChunkFromSave consumes plus the
// optional `structures` compound. `structures` is omitempty so a structure-free chunk does not
// emit an empty (TagEnd) RawMessage the encoder would reject.
type chunkSaveShape struct {
	Sections   []save.Section      `nbt:"sections"`
	Heightmaps map[string][]uint64 `nbt:"Heightmaps"`
	Status     string              `nbt:"Status"`
	YPos       int32               `nbt:"yPos"`
	XPos       int32               `nbt:"xPos"`
	ZPos       int32               `nbt:"zPos"`
	// BlockEntities carries the chunk's block entities (chest loot tables, spawners) so a saved
	// structure chunk round-trips them. omitempty so a BE-free chunk emits no (empty-list) tag.
	BlockEntities []nbt.RawMessage `nbt:"block_entities,omitempty"`
	// Structures is a POINTER so a structure-free chunk (nil) is dropped by omitempty — an
	// empty nbt.RawMessage value would still try to encode (TagEnd) and the encoder rejects it.
	Structures *nbt.RawMessage `nbt:"structures,omitempty"`
	// PostProcessing carries the aquifer/carver fluid marks (ListTag of per-section ShortList) so a
	// reloaded chunk's cave/ravine water still flows. POINTER + omitempty so a mark-free chunk drops
	// the (empty) tag the encoder would otherwise reject.
	PostProcessing *nbt.RawMessage `nbt:"PostProcessing,omitempty"`
	// DataVersion / InhabitedTime / isLightOn are the SerializableChunkData header fields the earlier
	// minimal shape omitted. DataVersion is the current data version; InhabitedTime is the local-
	// difficulty accumulator; isLightOn is the computed-light flag. All plain scalars (always written).
	DataVersion   int32 `nbt:"DataVersion"`
	InhabitedTime int64 `nbt:"InhabitedTime"`
	IsLightOn     bool  `nbt:"isLightOn"`
	// BlockTicks / FluidTicks are the pending scheduled block/fluid ticks (SavedTick.codec TAG_List).
	// POINTER + omitempty so a chunk with no pending ticks drops the (empty) tag the encoder rejects,
	// matching vanilla (no tick list written for a tick-free chunk). A mid-delay repeater / mid-spread
	// water block persists its pending tick here so it resumes on reload. CITE: SerializableChunkData
	// block_ticks / fluid_ticks (BLOCK_TICKS_CODEC / FLUID_TICKS_CODEC over List<SavedTick>).
	BlockTicks *nbt.RawMessage `nbt:"block_ticks,omitempty"`
	FluidTicks *nbt.RawMessage `nbt:"fluid_ticks,omitempty"`
}

// chunkSaveDataVersion is the 26.2 data version (NbtUtils.addCurrentDataVersion writes it as
// "DataVersion"). It matches the level.dat / playerdata / poi data versions used elsewhere.
// CITE: SharedConstants.getCurrentVersion().dataVersion() == 4903 for 26.2.
const chunkSaveDataVersion int32 = 4903
