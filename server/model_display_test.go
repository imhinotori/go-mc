package server

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// model_display_test.go covers MODEL-M1: the Display$ItemDisplay SynchedEntityData metadata encoder.
// It asserts the jar-derived indices + serializer ids AND the exact big-endian float32 wire bytes for
// the VECTOR3 translation and the QUATERNION left-rotation (the byte-identity gate). The encoder is
// tested DIRECTLY on a struct-literal Entity (no TickLoop needed) per the cleaner-unit-test route.

type parsedEntry struct {
	index uint8
	serID int32
	value []byte
}

// parseDisplayMetadata splits the encodeItemDisplayMetadata body (NO 0xFF terminator) into entries,
// reading Byte(index) + VarInt(serID) + a value whose width is fixed by the serializer id for the M1
// set (INT=VarInt, VECTOR3=12 bytes, QUATERNION=16 bytes, ITEM_STACK=SlotData, BYTE=1 byte).
func parseDisplayMetadata(t *testing.T, b []byte) []parsedEntry {
	t.Helper()
	r := bytes.NewReader(b)
	var out []parsedEntry
	for r.Len() > 0 {
		idxB, err := r.ReadByte()
		if err != nil {
			t.Fatalf("read index: %v", err)
		}
		var ser pk.VarInt
		if _, err := ser.ReadFrom(r); err != nil {
			t.Fatalf("read serID: %v", err)
		}
		var val []byte
		switch int32(ser) {
		case intSerializerID:
			start := len(b) - r.Len()
			var v pk.VarInt
			if _, err := v.ReadFrom(r); err != nil {
				t.Fatalf("read INT value: %v", err)
			}
			end := len(b) - r.Len()
			val = b[start:end]
		case byteSerializerID:
			bb, err := r.ReadByte()
			if err != nil {
				t.Fatalf("read BYTE value: %v", err)
			}
			val = []byte{bb}
		case vector3SerializerID:
			val = make([]byte, 12)
			if _, err := r.Read(val); err != nil {
				t.Fatalf("read VECTOR3 value: %v", err)
			}
		case quaternionSerializerID:
			val = make([]byte, 16)
			if _, err := r.Read(val); err != nil {
				t.Fatalf("read QUATERNION value: %v", err)
			}
		case itemStackSerializerID:
			start := len(b) - r.Len()
			var sd component.SlotData
			if _, err := sd.ReadFrom(r); err != nil {
				t.Fatalf("read ITEM_STACK value: %v", err)
			}
			end := len(b) - r.Len()
			val = b[start:end]
		default:
			t.Fatalf("unexpected serializer id %d", ser)
		}
		out = append(out, parsedEntry{index: idxB, serID: int32(ser), value: val})
	}
	return out
}

func findEntry(t *testing.T, entries []parsedEntry, index uint8) parsedEntry {
	t.Helper()
	for _, e := range entries {
		if e.index == index {
			return e
		}
	}
	t.Fatalf("no metadata entry at index %d", index)
	return parsedEntry{}
}

func beFloats(b []byte, n int) []float32 {
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.BigEndian.Uint32(b[i*4 : i*4+4]))
	}
	return out
}

// TestItemDisplayMetadataTransform: a Display ItemDisplay with a set translation encodes the jar-derived
// indices + serializer ids, and the VECTOR3 translation / QUATERNION left-rotation carry the EXACT
// big-endian float32 bytes (the byte-identity gate).
func TestItemDisplayMetadataTransform(t *testing.T) {
	stack := component.SlotData{Count: 1, ItemID: pk.VarInt(item.Diamond.ID)}
	e := &Entity{
		isItemDisplay:  true,
		displayItem:    stack,
		displayContext: 0,
		dispTransX:     1.5,
		dispTransY:     -2.25,
		dispTransZ:     0.75,
		dispScaleX:     1, dispScaleY: 1, dispScaleZ: 1,
		dispLeftRot:  [4]float32{0, 0, 0, 1},
		dispRightRot: [4]float32{0, 0, 0, 1},
	}
	meta := encodeItemDisplayMetadata(e)
	entries := parseDisplayMetadata(t, meta)

	item23 := findEntry(t, entries, dataItemDisplayStackIndex)
	if item23.serID != itemStackSerializerID {
		t.Fatalf("index 23 serID = %d, want ITEM_STACK %d", item23.serID, itemStackSerializerID)
	}
	var wantItem bytes.Buffer
	sc := stack
	_, _ = sc.WriteTo(&wantItem)
	if !bytes.Equal(item23.value, wantItem.Bytes()) {
		t.Fatalf("index 23 ITEM_STACK bytes = % x, want % x", item23.value, wantItem.Bytes())
	}

	tr := findEntry(t, entries, dataDisplayTranslationIndex)
	if tr.serID != vector3SerializerID {
		t.Fatalf("index 11 serID = %d, want VECTOR3 %d", tr.serID, vector3SerializerID)
	}
	var wantTr bytes.Buffer
	_, _ = pk.Float(1.5).WriteTo(&wantTr)
	_, _ = pk.Float(-2.25).WriteTo(&wantTr)
	_, _ = pk.Float(0.75).WriteTo(&wantTr)
	if !bytes.Equal(tr.value, wantTr.Bytes()) {
		t.Fatalf("index 11 TRANSLATION bytes = % x, want % x", tr.value, wantTr.Bytes())
	}
	if got := beFloats(tr.value, 3); got[0] != 1.5 || got[1] != -2.25 || got[2] != 0.75 {
		t.Fatalf("index 11 TRANSLATION floats = %v, want [1.5 -2.25 0.75]", got)
	}

	lr := findEntry(t, entries, dataDisplayLeftRotationIndex)
	if lr.serID != quaternionSerializerID {
		t.Fatalf("index 13 serID = %d, want QUATERNION %d", lr.serID, quaternionSerializerID)
	}
	var wantLr bytes.Buffer
	_, _ = pk.Float(0).WriteTo(&wantLr)
	_, _ = pk.Float(0).WriteTo(&wantLr)
	_, _ = pk.Float(0).WriteTo(&wantLr)
	_, _ = pk.Float(1).WriteTo(&wantLr)
	if !bytes.Equal(lr.value, wantLr.Bytes()) {
		t.Fatalf("index 13 LEFT_ROTATION bytes = % x, want % x", lr.value, wantLr.Bytes())
	}
	if got := beFloats(lr.value, 4); got[0] != 0 || got[1] != 0 || got[2] != 0 || got[3] != 1 {
		t.Fatalf("index 13 LEFT_ROTATION floats = %v, want identity [0 0 0 1]", got)
	}

	rr := findEntry(t, entries, dataDisplayRightRotationIndex)
	if rr.serID != quaternionSerializerID {
		t.Fatalf("index 14 serID = %d, want QUATERNION %d", rr.serID, quaternionSerializerID)
	}

	ctx := findEntry(t, entries, dataItemDisplayContextIndex)
	if ctx.serID != byteSerializerID {
		t.Fatalf("index 24 serID = %d, want BYTE %d", ctx.serID, byteSerializerID)
	}
	if len(ctx.value) != 1 || ctx.value[0] != 0 {
		t.Fatalf("index 24 ITEM_DISPLAY value = % x, want 00", ctx.value)
	}
}

// TestItemDisplayMetadataDefaultScale: a default-transform display encodes SCALE (index 12) as the
// VECTOR3 (1,1,1) - the Display defineSynchedData default seeded by spawnItemDisplay.
func TestItemDisplayMetadataDefaultScale(t *testing.T) {
	e := &Entity{
		isItemDisplay: true,
		displayItem:   component.SlotData{Count: 1, ItemID: pk.VarInt(item.Stone.ID)},
		dispScaleX:    1, dispScaleY: 1, dispScaleZ: 1,
		dispLeftRot:  [4]float32{0, 0, 0, 1},
		dispRightRot: [4]float32{0, 0, 0, 1},
	}
	meta := encodeItemDisplayMetadata(e)
	entries := parseDisplayMetadata(t, meta)

	sc := findEntry(t, entries, dataDisplayScaleIndex)
	if sc.serID != vector3SerializerID {
		t.Fatalf("index 12 serID = %d, want VECTOR3 %d", sc.serID, vector3SerializerID)
	}
	if got := beFloats(sc.value, 3); got[0] != 1 || got[1] != 1 || got[2] != 1 {
		t.Fatalf("index 12 SCALE floats = %v, want [1 1 1]", got)
	}
	var wantSc bytes.Buffer
	_, _ = pk.Float(1).WriteTo(&wantSc)
	_, _ = pk.Float(1).WriteTo(&wantSc)
	_, _ = pk.Float(1).WriteTo(&wantSc)
	if !bytes.Equal(sc.value, wantSc.Bytes()) {
		t.Fatalf("index 12 SCALE bytes = % x, want % x", sc.value, wantSc.Bytes())
	}
}
