package component

import (
	"bytes"
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestPatchRoundTrip: a stack carrying two known components round-trips through the SlotData wire form,
// then DecodePatch → ApplyTo reproduces the same wire bytes (decode∘encode is byte-inverse when every
// component type is known). Also proves Get/Set/Remove edits reflect on the re-encoded stack.
func TestPatchRoundTrip(t *testing.T) {
	// Build a stack with MAX_DAMAGE(1561) + DAMAGE(200) via the Patch encoder.
	var p Patch
	p.Set(2, &MaxDamage{VarInt: 1561}) // minecraft:max_damage
	p.Set(3, &Damage{VarInt: 200})     // minecraft:damage
	s := p.ApplyTo(SlotData{Count: 1, ItemID: 964})

	// Round-trip the SlotData through the wire to confirm the header + RawComponents are consistent.
	var buf bytes.Buffer
	if _, err := s.WriteTo(&buf); err != nil {
		t.Fatalf("SlotData.WriteTo: %v", err)
	}
	var back SlotData
	if _, err := back.ReadFrom(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("SlotData.ReadFrom: %v", err)
	}
	if back.AddedCount != 2 || back.RemovedCount != 0 {
		t.Fatalf("round-trip header = added %d removed %d, want added 2 removed 0", back.AddedCount, back.RemovedCount)
	}

	// DecodePatch reads the two components back with the right values.
	dp := DecodePatch(back)
	if md, ok := dp.Get(2).(*MaxDamage); !ok || md.VarInt != 1561 {
		t.Fatalf("decoded max_damage = %+v, want 1561", dp.Get(2))
	}
	if dm, ok := dp.Get(3).(*Damage); !ok || dm.VarInt != 200 {
		t.Fatalf("decoded damage = %+v, want 200", dp.Get(3))
	}

	// Encode∘Decode is byte-inverse for known components.
	_, _, raw := dp.Encode()
	if !bytes.Equal(raw, back.RawComponents) {
		t.Fatalf("re-encoded RawComponents differ from original: %x vs %x", raw, back.RawComponents)
	}

	// Remove one component; the re-encoded stack drops it.
	dp.Remove(3)
	added, removed, _ := dp.Encode()
	if added != 1 || removed != 0 {
		t.Fatalf("after Remove(3): added %d removed %d, want added 1 removed 0", added, removed)
	}
	if dp.Has(3) {
		t.Fatal("Has(3) still true after Remove(3)")
	}

	// Set a new component; it appends.
	dp.Set(19, &RepairCost{VarInt: pk.VarInt(3)}) // minecraft:repair_cost
	if rc, ok := dp.Get(19).(*RepairCost); !ok || rc.VarInt != 3 {
		t.Fatalf("Set(repair_cost) not reflected: %+v", dp.Get(19))
	}
}
