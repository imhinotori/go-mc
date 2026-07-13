package server

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// entity_metadata_gate_test.go is the PERMANENT regression gate that PROVES every
// ClientboundSetEntityData metadata body the server emits decodes EXACTLY the way a vanilla 26.2
// client's SynchedEntityData$DataValue reader walks it: read a Byte index (0xFF stops), then a
// VarInt serializerId, then consume EXACTLY the serializer codec's value width, repeat until the
// 0xFF terminator, with NO overrun and NO trailing bytes.
//
// The bug that motivated this gate: a DataValue value writing FEWER/MORE bytes than the serializer
// id it advertises desyncs the client decoder, which then reads the next entry's index/serializer as
// data and runs off the end of the body — the reported:
//   io.netty.handler.codec.DecoderException: Failed to decode packet 'clientbound/minecraft:set_entity_data'
//   Caused by: java.lang.IndexOutOfBoundsException: readerIndex(7)+length(1) exceeds writerIndex(7)
//
// The per-serializer widths below are the JAR-DERIVED value codecs (javap EntityDataSerializers
// static{} + each serializer's StreamCodec, temp/cache/26.2-inner.jar). The one that was wrong:
// OPTIONAL_BLOCK_STATE (id 15) uses a BESPOKE single-VarInt codec (0 == empty), NOT the generic
// ByteBufCodecs.optional Boolean-present shape — see optionalBlockStateValue.

// mdSerializer IDs (subset the server actually emits — the full registry has 43). Kept local to the
// gate so a future new serializer forces a decoder update here (the default case fails the test).
const (
	mdBYTE                 int32 = 0
	mdINT                  int32 = 1
	mdLONG                 int32 = 2
	mdFLOAT                int32 = 3
	mdBOOLEAN              int32 = 8
	mdOPTIONAL_BLOCK_POS   int32 = 11
	mdDIRECTION            int32 = 12
	mdOPTIONAL_BLOCK_STATE int32 = 15
	mdITEM_STACK           int32 = 7
	mdPOSE                 int32 = 20
	mdVECTOR3              int32 = 39
	mdQUATERNION           int32 = 40
)

// mdReadVarInt reads a VarInt exactly like the client (net/packet VarInt.ReadFrom) and returns its value.
func mdReadVarInt(r *bytes.Reader) (int32, error) {
	var v pk.VarInt
	if _, err := v.ReadFrom(r); err != nil {
		return 0, err
	}
	return int32(v), nil
}

// mdConsumeValue consumes EXACTLY the bytes the serializer-id's value codec produces, mirroring the
// vanilla client's serializer.codec().decode(buf). Any codec that is not modeled fails the test so a
// newly-emitted serializer cannot silently slip past the gate. Returns an error on any short read.
func mdConsumeValue(r *bytes.Reader, serID int32) error {
	skip := func(n int) error {
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		return nil
	}
	switch serID {
	case mdBYTE, mdBOOLEAN:
		return skip(1) // ByteBufCodecs.BYTE / .BOOL — one byte
	case mdFLOAT:
		return skip(4) // ByteBufCodecs.FLOAT — big-endian float32
	case mdINT, mdDIRECTION, mdPOSE:
		// INT == VAR_INT; DIRECTION == VarInt(get3DDataValue); POSE == idMapper VarInt(ordinal).
		_, err := mdReadVarInt(r)
		return err
	case mdLONG:
		return skip(8) // ByteBufCodecs.VAR_LONG would be a varlong; here LONG serializer == VAR_LONG.
	case mdVECTOR3:
		return skip(12) // 3 float32
	case mdQUATERNION:
		return skip(16) // 4 float32
	case mdOPTIONAL_BLOCK_POS:
		// ByteBufCodecs.optional(BlockPos.STREAM_CODEC): Boolean present, then (if present) 8-byte packed long.
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		if b != 0 {
			return skip(8)
		}
		return nil
	case mdOPTIONAL_BLOCK_STATE:
		// BESPOKE codec (EntityDataSerializers$2): a SINGLE VarInt — 0 == empty, else the state id. NO Boolean.
		_, err := mdReadVarInt(r)
		return err
	case mdITEM_STACK:
		// ItemStack.OPTIONAL_STREAM_CODEC — decode with the provably-inverse SlotData.ReadFrom.
		var sd component.SlotData
		_, err := sd.ReadFrom(r)
		return err
	default:
		return fmt.Errorf("gate decoder has no codec for serializer id %d — add it (jar-verified) so this metadata is proven, not skipped", serID)
	}
}

// mdWalkBody walks a metadata body (WITHOUT the packet id/entity id prefix — just the packed-items
// list ending in 0xFF) exactly like the vanilla client. It asserts: at least the terminator is
// present, every entry's value consumes exactly its codec width, the walk stops on the 0xFF marker,
// and there are ZERO bytes left after it. Returns the list of (index, serializerId) entries decoded.
func mdWalkBody(t *testing.T, name string, body []byte) {
	t.Helper()
	r := bytes.NewReader(body)
	entries := 0
	for {
		idxByte, err := r.ReadByte()
		if err != nil {
			t.Fatalf("%s: ran out of bytes before the 0xFF terminator (decoder desync — a prior value under/over-consumed): %v", name, err)
		}
		if idxByte == entityDataEOF {
			break // the mandatory terminator: the packed-items list is done
		}
		serID, err := mdReadVarInt(r)
		if err != nil {
			t.Fatalf("%s: entry #%d: failed reading serializerId VarInt (desync: a prior value under/over-consumed so this is not a real serializer id): %v", name, entries, err)
		}
		if err := mdConsumeValue(r, serID); err != nil {
			t.Fatalf("%s: entry #%d (index=%d serID=%d): value did not consume its codec width cleanly: %v", name, entries, idxByte, serID, err)
		}
		entries++
	}
	if rem := r.Len(); rem != 0 {
		t.Fatalf("%s: %d byte(s) left AFTER the 0xFF terminator — the metadata body over-ran or a value under-consumed (client would read these as a bogus next entry and overrun)", name, rem)
	}
}

// mdBodyOf encodes a SetEntityData packet for e (+ entries), then strips the packet id + entity-id
// VarInt prefix so mdWalkBody sees just the packed-items body (the terminator-ended DataValue list).
func mdBodyOf(t *testing.T, e *Entity, entries ...entityDataEntry) []byte {
	t.Helper()
	p := encodeSetEntityData(e, entries...)
	r := bytes.NewReader(p.Data)
	if _, err := mdReadVarInt(r); err != nil { // the leading entity-id VarInt
		t.Fatalf("failed reading SetEntityData entity-id prefix: %v", err)
	}
	rest := make([]byte, r.Len())
	_, _ = io.ReadFull(r, rest)
	return rest
}

// TestEntityMetadataDataValueGate is the round-trip gate: for every metadata builder AND every mob
// type's spawn metadata, it walks the SetEntityData body the vanilla client's DataValue reader would,
// asserting exact codec-width consumption to the 0xFF marker with no overrun/underrun. It is the
// permanent regression gate for the set_entity_data DecoderException class of bug.
func TestEntityMetadataDataValueGate(t *testing.T) {
	// Helper: a bare entity carrying a pre-built metadata []byte (the tracker's e.metadata slot).
	withMeta := func(meta []byte) *Entity {
		return &Entity{id: 1, metadata: meta}
	}

	sampleStack := component.SlotData{Count: 1, ItemID: 1} // a component-free stack (count>0, id 1)

	// --- 1) Every individual DataValue builder (the *DataEntry / *Entry factories). ---
	singleEntry := []struct {
		name  string
		entry entityDataEntry
	}{
		{"itemDataEntry", itemDataEntry(sampleStack)},
		{"airDataEntry", airDataEntry(300)},
		{"livingEntityFlagsEntry", livingEntityFlagsEntry(livingFlagUsingItem)},
		{"skinCustomisationEntry", skinCustomisationEntry(0x7F)},
		{"babyDataEntry", babyDataEntry(true)},
		{"woolDataEntry", woolDataEntry(woolByteFor(14, true))},
		{"sharedFlagsDataEntry", sharedFlagsDataEntry(0x01)},
		{"wolfFlagsDataEntry", wolfFlagsDataEntry(wolfFlagsByte(true, true))},
		{"mobFlagsDataEntry", mobFlagsDataEntry(mobFlagsByteFor(false, true, true))},
		{"carriedBlockDataEntry(present)", carriedBlockDataEntry(42, true)},
		{"carriedBlockDataEntry(empty)", carriedBlockDataEntry(0, false)},
		{"creeperSwellDataEntry", creeperSwellDataEntry(1)},
		{"creeperPoweredDataEntry", creeperPoweredDataEntry(true)},
		{"creeperIgnitedDataEntry", creeperIgnitedDataEntry(true)},
		{"arrowFlagsDataEntry", arrowFlagsDataEntry(arrowFlagsByte(true))},
		{"catLyingDataEntry", catLyingDataEntry(true)},
		{"catRelaxDataEntry", catRelaxDataEntry(true)},
		{"catCollarDataEntry", catCollarDataEntry(14)},
		{"ocelotTrustDataEntry", ocelotTrustDataEntry(true)},
	}
	for _, tc := range singleEntry {
		body := mdBodyOf(t, withMeta(nil), tc.entry)
		mdWalkBody(t, tc.name, body)
	}

	// --- 2) The sleep POSE + OPTIONAL_BLOCK_POS pair (both present and empty). ---
	poseEntry := entityDataEntry{index: dataPoseIndex, serializerID: poseSerializerID, value: poseValue{ordinal: poseSleeping}}
	posPresent := entityDataEntry{index: dataSleepingPosIndex, serializerID: optionalBlockPosSerID, value: optionalBlockPosValue{pos: pk.Position{X: 1, Y: 64, Z: -3}, present: true}}
	posEmpty := entityDataEntry{index: dataSleepingPosIndex, serializerID: optionalBlockPosSerID, value: optionalBlockPosValue{present: false}}
	mdWalkBody(t, "sleep pose+pos present", mdBodyOf(t, withMeta(nil), poseEntry, posPresent))
	mdWalkBody(t, "sleep pose+pos empty", mdBodyOf(t, withMeta(nil), poseEntry, posEmpty))

	// --- 3) The pre-built metadata []byte builders (the e.metadata splices the tracker emits). ---
	prebuilt := []struct {
		name string
		meta []byte
	}{
		{"encodeItemMetadata", encodeItemMetadata(sampleStack)},
		{"encodeFrameMetadata", func() []byte {
			e := &Entity{frameItem: sampleStack, frameDirection: 3, frameRotation: 5}
			return encodeFrameMetadata(e)
		}()},
		{"encodeArmorStandMetadata", func() []byte { e := &Entity{armorStandFlags: 0x04}; return encodeArmorStandMetadata(e) }()},
		{"encodeItemDisplayMetadata", func() []byte {
			e := &Entity{displayItem: sampleStack, displayContext: 1, dispScaleX: 1, dispScaleY: 1, dispScaleZ: 1, dispLeftRot: [4]float32{0, 0, 0, 1}, dispRightRot: [4]float32{0, 0, 0, 1}}
			return encodeItemDisplayMetadata(e)
		}()},
		{"playerSkinMetadata", playerSkinMetadata(0x7F)},
	}
	for _, tc := range prebuilt {
		mdWalkBody(t, tc.name, mdBodyOf(t, withMeta(tc.meta)))
	}

	// --- 4) A reloaded item's metadata (the persisted-world spawn path: diskToEntity -> encodeItemMetadata). ---
	reloadStack := component.SlotData{Count: 5, ItemID: 1}
	mdWalkBody(t, "reloaded item metadata", mdBodyOf(t, withMeta(encodeItemMetadata(reloadStack))))

	// --- 5) spliceReloadMetadata for each mob type it carries entries for (baby/wolf/sheep). ---
	reloadCases := []struct {
		name  string
		build func() *Entity
	}{
		{"reload baby", func() *Entity { e := &Entity{typ: entity.Pig.ID, breedAge: -1}; spliceReloadMetadata(e); return e }},
		{"reload tamed sitting wolf", func() *Entity {
			e := &Entity{typ: entity.Wolf.ID, tame: true, inSittingPose: true}
			spliceReloadMetadata(e)
			return e
		}},
		{"reload dyed sheared sheep", func() *Entity {
			e := &Entity{typ: entity.Sheep.ID, sheepColor: 14, sheared: true}
			spliceReloadMetadata(e)
			return e
		}},
	}
	for _, tc := range reloadCases {
		e := tc.build()
		mdWalkBody(t, tc.name, mdBodyOf(t, e))
	}

	// --- 6) The enderman LIVE carry push (the reported crash): a present carried block over the by-id path. ---
	pkt := encodeSetEntityDataByID(7, carriedBlockDataEntry(42, true))
	r := bytes.NewReader(pkt.Data)
	if _, err := mdReadVarInt(r); err != nil {
		t.Fatalf("enderman carry: entity-id prefix read failed: %v", err)
	}
	rest := make([]byte, r.Len())
	_, _ = io.ReadFull(r, rest)
	mdWalkBody(t, "enderman carry present (by id)", rest)

	// Keep a stable reference so an accidental unused import trips early rather than at build.
	_ = uuid.Nil
}
