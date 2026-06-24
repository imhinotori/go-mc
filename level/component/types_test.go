package component

import (
	"bytes"
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestSlotEncode proves SlotData.WriteTo is inverse to ReadFrom for the three
// shapes that matter for the component-slot ItemStack wire (ENT-04):
//   - an empty stack (count <= 0) writes only the count;
//   - a component-free stack (count+id, 0 added/0 removed) round-trips;
//   - a stack carrying one added component round-trips, recovering the component bytes.
func TestSlotEncode(t *testing.T) {
	t.Run("empty stack writes only count", func(t *testing.T) {
		s := SlotData{Count: 0}
		var buf bytes.Buffer
		if _, err := s.WriteTo(&buf); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		// VarInt(0) is a single byte.
		if got := buf.Bytes(); len(got) != 1 || got[0] != 0 {
			t.Fatalf("empty stack: want [0x00], got %v", got)
		}
		var back SlotData
		if _, err := back.ReadFrom(bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
		if back.Count != 0 {
			t.Fatalf("empty round-trip: want Count 0, got %d", back.Count)
		}
	})

	t.Run("component-free stack round-trips", func(t *testing.T) {
		// count=1, itemId=1 (stone), no components.
		s := SlotData{Count: 1, ItemID: 1, AddedCount: 0, RemovedCount: 0}
		var buf bytes.Buffer
		if _, err := s.WriteTo(&buf); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		// count+id+0+0 = exactly 4 single-byte VarInts.
		if got := buf.Bytes(); len(got) != 4 {
			t.Fatalf("component-free: want 4 bytes (count+id+0+0), got %d (%v)", len(got), got)
		}
		var back SlotData
		if _, err := back.ReadFrom(bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
		if back.Count != 1 || back.ItemID != 1 || back.AddedCount != 0 || back.RemovedCount != 0 {
			t.Fatalf("component-free round-trip mismatch: %+v", back)
		}
		if len(back.RawComponents) != 0 {
			t.Fatalf("component-free: want no RawComponents, got %v", back.RawComponents)
		}
	})

	t.Run("one added component round-trips", func(t *testing.T) {
		// Build the wire bytes for: count=1, itemId=1, addedCount=1, removedCount=0,
		// then one added component (typeId=1 max_stack_size, value VarInt(64)).
		var wire bytes.Buffer
		if _, err := (pk.Tuple{
			pk.VarInt(1), // count
			pk.VarInt(1), // itemId
			pk.VarInt(1), // addedCount
			pk.VarInt(0), // removedCount
			pk.VarInt(1), // component typeId = max_stack_size
			pk.VarInt(64),
		}).WriteTo(&wire); err != nil {
			t.Fatalf("build wire: %v", err)
		}
		original := append([]byte(nil), wire.Bytes()...)

		// ReadFrom must consume the whole stack and capture the component bytes.
		var s SlotData
		r := bytes.NewReader(wire.Bytes())
		if _, err := s.ReadFrom(r); err != nil {
			t.Fatalf("ReadFrom: %v", err)
		}
		if s.Count != 1 || s.ItemID != 1 || s.AddedCount != 1 || s.RemovedCount != 0 {
			t.Fatalf("header mismatch: %+v", s)
		}
		if r.Len() != 0 {
			t.Fatalf("ReadFrom left %d unconsumed bytes (mis-framed)", r.Len())
		}
		if len(s.RawComponents) == 0 {
			t.Fatalf("expected captured RawComponents for one added component")
		}

		// WriteTo must reproduce the original bytes exactly (provably inverse).
		var out bytes.Buffer
		if _, err := s.WriteTo(&out); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		if !bytes.Equal(out.Bytes(), original) {
			t.Fatalf("WriteTo not inverse to ReadFrom:\n  want %v\n  got  %v", original, out.Bytes())
		}

		// And a second decode of the re-encoded bytes recovers the same stack.
		var back SlotData
		if _, err := back.ReadFrom(bytes.NewReader(out.Bytes())); err != nil {
			t.Fatalf("re-decode: %v", err)
		}
		if back.Count != s.Count || back.ItemID != s.ItemID ||
			back.AddedCount != s.AddedCount || back.RemovedCount != s.RemovedCount ||
			!bytes.Equal(back.RawComponents, s.RawComponents) {
			t.Fatalf("re-decode mismatch:\n  first %+v\n  back  %+v", s, back)
		}
	})
}
