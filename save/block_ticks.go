package save

import (
	"bytes"

	"github.com/imhinotori/sulfur/nbt"
)

// block_ticks.go — the on-disk codec for a chunk's scheduled block/fluid ticks
// (SUB-BLOCKTICK). The Chunk struct already carries `block_ticks` and `fluid_ticks` as raw
// nbt.RawMessage fields; this file gives them a structured shape matching the vanilla
// SavedTick codec so the tick subsystem can round-trip a chunk's pending ticks through region
// NBT.
//
// On-disk shape (net.minecraft.world.ticks.SavedTick.codec): each chunk-ticks field is a
// TAG_List of compounds, one per pending tick, with fields:
//
//	i : String  — the tick's TYPE id (block/fluid resource location, e.g. "minecraft:sugar_cane")
//	x : Int     — block X
//	y : Int     — block Y
//	z : Int     — block Z
//	t : Int     — delay = triggerTick - chunkSaveGameTime (relative, signed)
//	p : Int     — TickPriority.value (-3..3); via TickPriority.CODEC = Codec.INT.xmap(byValue, getValue)
//
// CITE: SavedTick.codec field names {i,x,y,z,t,p}; SavedTick record (type,pos,delay,priority);
// TickPriority.CODEC (INT xmap on getValue/byValue).

// SavedTickNBT is the structured form of one persisted tick — the concrete, string-typed
// counterpart of ticks.SavedTick[T] used at the disk boundary (the generic T is realized as
// the resource-location string `i`). Field tags are the exact vanilla codec keys.
type SavedTickNBT struct {
	ID       string `nbt:"i"` // SavedTick.type, serialized as its resource location
	X        int32  `nbt:"x"`
	Y        int32  `nbt:"y"`
	Z        int32  `nbt:"z"`
	Delay    int32  `nbt:"t"` // SavedTick.delay (triggerTick - gameTime)
	Priority int32  `nbt:"p"` // TickPriority.value
}

// EncodeChunkTicks serializes a list of saved ticks into a nbt.RawMessage holding a TAG_List of
// SavedTick compounds — the on-disk `block_ticks` / `fluid_ticks` value. An EMPTY list encodes
// to a zero RawMessage (Type == TagEnd): the chunk save shape omits the field entirely (vanilla
// writes no tick list for a chunk with no pending ticks), so an empty result signals "omit".
// CITE: SavedTick.codec applied over the per-chunk pending-tick list.
func EncodeChunkTicks(ticksList []SavedTickNBT) (nbt.RawMessage, error) {
	if len(ticksList) == 0 {
		return nbt.RawMessage{}, nil // TagEnd: caller omits the field
	}
	// Marshal as a top-level list, then strip the leading (type | name-length | name) header so
	// the RawMessage.Data is the bare list payload (RawMessage stores tag-type separately + raw
	// body, matching how the nbt encoder re-emits it under the field name). The simplest faithful
	// path: marshal a wrapper compound holding the list under a known key, decode it back into a
	// RawMessage via the chunk struct's own field — but to keep this self-contained we hand-encode
	// the list as a RawMessage of Type TagList.
	var buf bytes.Buffer
	enc := nbt.NewEncoder(&buf)
	// Encode the list as the UNNAMED root value. nbt.Encoder.Encode writes
	// [type][nameLen][name][payload]; with an empty name the header is [type][0x00 0x00]. We then
	// slice off that 3-byte header to leave just [payload], and record the tag type for RawMessage.
	if err := enc.Encode(ticksList, ""); err != nil {
		return nbt.RawMessage{}, err
	}
	full := buf.Bytes()
	if len(full) < 3 {
		return nbt.RawMessage{}, nil
	}
	tagType := full[0]
	payload := full[3:] // drop [type][0x00][0x00] name header
	data := make([]byte, len(payload))
	copy(data, payload)
	return nbt.RawMessage{Type: tagType, Data: data}, nil
}

// DecodeChunkTicks parses a chunk's `block_ticks` / `fluid_ticks` RawMessage back into a list of
// saved ticks. A zero/empty RawMessage (TagEnd, or an empty list) yields a nil slice. CITE: the
// read half of SavedTick.codec over the per-chunk list.
func DecodeChunkTicks(raw nbt.RawMessage) ([]SavedTickNBT, error) {
	if raw.Type == nbt.TagEnd || len(raw.Data) == 0 {
		return nil, nil
	}
	var out []SavedTickNBT
	if err := raw.Unmarshal(&out); err != nil {
		return nil, err
	}
	return out, nil
}
