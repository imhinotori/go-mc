package save

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/nbt"
)

// TestSaveLoadAllItems_RoundTripNoComponents proves the Phase-A {Slot,id,count} round-trip is exact
// for a sparse, multi-slot container of component-free stacks: empty slots are skipped on save and
// re-appear as empty on load; populated slots return to their exact (slot, id, count). CITE:
// ContainerHelper.saveAllItems/loadAllItems.
func TestSaveLoadAllItems_RoundTripNoComponents(t *testing.T) {
	const size = 27
	in := make([]DiskItem, size)
	in[0] = DiskItem{ID: "minecraft:stone", Count: 64}
	in[3] = DiskItem{ID: "minecraft:diamond_sword", Count: 1}
	in[26] = DiskItem{ID: "minecraft:cooked_beef", Count: 12}

	items, dropped := SaveAllItems(in, false)
	if dropped != 0 {
		t.Fatalf("dropped components = %d, want 0 (no component stacks)", dropped)
	}
	if len(items) != 3 {
		t.Fatalf("saved items = %d, want 3 (empty slots skipped)", len(items))
	}

	// Round-trip through NBT bytes (not just the in-memory slice) to exercise the byte codec.
	raw, err := encodeItemsList(items)
	if err != nil {
		t.Fatalf("encodeItemsList: %v", err)
	}
	decoded, err := decodeItemsList(raw)
	if err != nil {
		t.Fatalf("decodeItemsList: %v", err)
	}

	out := LoadAllItems(decoded, size)
	if len(out) != size {
		t.Fatalf("loaded size = %d, want %d", len(out), size)
	}
	for i := range in {
		if out[i].ID != in[i].ID || out[i].Count != in[i].Count {
			// empty in slots come back as empty out slots (ID "" Count 0)
			if in[i].IsEmpty() && out[i].IsEmpty() {
				continue
			}
			t.Errorf("slot %d: got {%q,%d}, want {%q,%d}", i, out[i].ID, out[i].Count, in[i].ID, in[i].Count)
		}
	}
}

// TestSaveAllItems_SlotIsByteAndCapitalS proves the on-disk element uses the capital-S "Slot" key
// encoded as an NBT byte (TagByte), and "id"/"count" with the exact lowercase keys. CITE:
// ItemStackWithSlot.lambda$static$0 (ldc "Slot", ExtraCodecs.UNSIGNED_BYTE) + ItemStack.MAP_CODEC.
func TestSaveAllItems_SlotIsByteAndCapitalS(t *testing.T) {
	in := make([]DiskItem, 5)
	in[4] = DiskItem{ID: "minecraft:stone", Count: 1}
	items, _ := SaveAllItems(in, false)
	if len(items) != 1 || items[0].Slot != 4 {
		t.Fatalf("items=%v, want one element at Slot 4", items)
	}

	raw, err := encodeItemsList(items)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Decode into a generic compound to assert the exact tag TYPES and KEYS on disk.
	var generic struct {
		Items []struct {
			Slot  nbt.RawMessage `nbt:"Slot"`
			ID    string         `nbt:"id"`
			Count int32          `nbt:"count"`
		} `nbt:"Items"`
	}
	if _, err := nbt.NewDecoder(bytes.NewReader(raw)).Decode(&generic); err != nil {
		t.Fatalf("decode generic: %v", err)
	}
	if len(generic.Items) != 1 {
		t.Fatalf("generic items = %d, want 1", len(generic.Items))
	}
	e := generic.Items[0]
	if e.Slot.Type != nbt.TagByte {
		t.Errorf("Slot tag type = %d, want TagByte(%d)", e.Slot.Type, nbt.TagByte)
	}
	if e.ID != "minecraft:stone" {
		t.Errorf("id = %q, want minecraft:stone", e.ID)
	}
	if e.Count != 1 {
		t.Errorf("count = %d, want 1", e.Count)
	}
}

// TestSaveAllItems_CountAlwaysWrittenIncludingOne proves "count" is written even when it equals 1
// (optionalAlwaysPresentFieldOf wraps in Optional.of unconditionally), and a stack with count 1
// round-trips to count 1. CITE: ExtraCodecs.optionalAlwaysPresentFieldOf.
func TestSaveAllItems_CountAlwaysWrittenIncludingOne(t *testing.T) {
	in := []DiskItem{{ID: "minecraft:apple", Count: 1}}
	items, _ := SaveAllItems(in, false)
	raw, err := encodeItemsList(items)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var generic struct {
		Items []struct {
			Count *int32 `nbt:"count"`
		} `nbt:"Items"`
	}
	if _, err := nbt.NewDecoder(bytes.NewReader(raw)).Decode(&generic); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(generic.Items) != 1 || generic.Items[0].Count == nil {
		t.Fatalf("count key absent for count==1; must always be written")
	}
	if *generic.Items[0].Count != 1 {
		t.Errorf("count = %d, want 1", *generic.Items[0].Count)
	}
}

// TestLoadAllItems_BoundsCheck proves an out-of-range Slot is silently skipped (isValidInContainer)
// and a valid one is placed. CITE: loadAllItems + isValidInContainer = slot>=0 && slot<size.
func TestLoadAllItems_BoundsCheck(t *testing.T) {
	const size = 9
	items := []ItemStackWithSlotDisk{
		{Slot: 0, ItemStackDisk: ItemStackDisk{ID: "minecraft:stone", Count: 1}},
		{Slot: 8, ItemStackDisk: ItemStackDisk{ID: "minecraft:dirt", Count: 1}},
		{Slot: 9, ItemStackDisk: ItemStackDisk{ID: "minecraft:gravel", Count: 1}}, // out of range -> skipped
		{Slot: 200, ItemStackDisk: ItemStackDisk{ID: "minecraft:sand", Count: 1}}, // out of range -> skipped
	}
	out := LoadAllItems(items, size)
	if out[0].ID != "minecraft:stone" || out[8].ID != "minecraft:dirt" {
		t.Fatalf("in-range slots not placed: %+v", out)
	}
	// Every other slot must be empty; the two OOB slots must have vanished (no panic, no growth).
	if len(out) != size {
		t.Fatalf("len(out)=%d, want %d", len(out), size)
	}
	nonEmpty := 0
	for _, it := range out {
		if !it.IsEmpty() {
			nonEmpty++
		}
	}
	if nonEmpty != 2 {
		t.Errorf("non-empty slots = %d, want 2 (OOB skipped)", nonEmpty)
	}
}

// TestSaveAllItems_KeepEmptyTag proves the keepEmptyTag flag: an all-empty container returns nil
// with keepEmptyTag=false (discard "Items") and a non-nil empty slice with keepEmptyTag=true.
// CITE: saveAllItems(...,boolean) — if (typedList.isEmpty() && !flag) out.discard("Items").
func TestSaveAllItems_KeepEmptyTag(t *testing.T) {
	empty := make([]DiskItem, 4)
	if items, _ := SaveAllItems(empty, false); items != nil {
		t.Errorf("keepEmptyTag=false on empty container: got %v, want nil (discard)", items)
	}
	items, _ := SaveAllItems(empty, true)
	if items == nil || len(items) != 0 {
		t.Errorf("keepEmptyTag=true on empty container: got %v, want non-nil empty slice", items)
	}
}

// TestSaveAllItems_DropsComponentsFlagged proves a component-bearing stack persists its id+count but
// is counted as a dropped-components stack (Phase A lossy-but-visible). CITE: file header Phase A.
func TestSaveAllItems_DropsComponentsFlagged(t *testing.T) {
	in := []DiskItem{
		{ID: "minecraft:diamond_sword", Count: 1, HasComponents: true}, // enchanted: components dropped
		{ID: "minecraft:stone", Count: 64},                             // plain: no drop
	}
	items, dropped := SaveAllItems(in, false)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1 (one component-bearing stack)", dropped)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (both still persist id+count)", len(items))
	}
	// The component-bearing stack must persist WITHOUT a components compound (Phase A).
	if items[0].Components != nil {
		t.Errorf("Phase A must not write a components compound; got %v", items[0].Components)
	}
	if items[0].ID != "minecraft:diamond_sword" || items[0].Count != 1 {
		t.Errorf("dropped-component stack lost id/count: %+v", items[0])
	}
}

// TestSaveLoadItemsCompound_RoundTrip proves the bare {Items:[...]} compound (a BlockEntity.Data
// payload) round-trips through SaveItemsCompound/LoadItemsCompound for a chest container.
func TestSaveLoadItemsCompound_RoundTrip(t *testing.T) {
	const size = 27
	in := make([]DiskItem, size)
	in[0] = DiskItem{ID: "minecraft:golden_apple", Count: 3}
	in[13] = DiskItem{ID: "minecraft:emerald", Count: 7}

	data, dropped, err := SaveItemsCompound(in, false)
	if err != nil {
		t.Fatalf("SaveItemsCompound: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if data.Type != nbt.TagCompound {
		t.Fatalf("compound type = %d, want TagCompound", data.Type)
	}

	out, err := LoadItemsCompound(data, size)
	if err != nil {
		t.Fatalf("LoadItemsCompound: %v", err)
	}
	if out[0].ID != "minecraft:golden_apple" || out[0].Count != 3 {
		t.Errorf("slot 0: %+v", out[0])
	}
	if out[13].ID != "minecraft:emerald" || out[13].Count != 7 {
		t.Errorf("slot 13: %+v", out[13])
	}
}

// TestLoadItemsCompound_MissingItemsKey proves a compound with no "Items" key yields an all-empty
// container (listOrEmpty semantics) without error — faithful to in.listOrEmpty tolerance.
func TestLoadItemsCompound_MissingItemsKey(t *testing.T) {
	// An empty compound (no Items).
	empty := nbt.RawMessage{Type: nbt.TagCompound}
	out, err := LoadItemsCompound(empty, 27)
	if err != nil {
		t.Fatalf("LoadItemsCompound(empty): %v", err)
	}
	if len(out) != 27 {
		t.Fatalf("len=%d, want 27", len(out))
	}
	for i, it := range out {
		if !it.IsEmpty() {
			t.Fatalf("slot %d not empty: %+v", i, it)
		}
	}
}

// TestClampCount proves the disk count clamp intRange(1,99). CITE: ExtraCodecs.intRange(1,99).
func TestClampCount(t *testing.T) {
	cases := []struct{ in, want int32 }{
		{0, 1}, {1, 1}, {64, 64}, {99, 99}, {100, 99}, {-5, 1},
	}
	for _, c := range cases {
		if got := clampCount(c.in); got != c.want {
			t.Errorf("clampCount(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
