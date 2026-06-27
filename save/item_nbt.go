package save

import (
	"bytes"

	"github.com/imhinotori/sulfur/nbt"
)

// item_nbt.go — SUB-ITEMNBT: the DISK (NBT) codec of ItemStack {id,count(,components)},
// ItemStackWithSlot {Slot, <flattened stack>}, and ContainerHelper.saveAllItems/loadAllItems
// (the "Items" list). This is the format Sulfur did NOT yet have on disk: it had only the WIRE
// component codec (level/component.SlotData, registry-id-indexed streamCodec bytes) and the
// legacy save.Item ({Count,Slot,id,Tag}) used by the player .dat path.
//
// 1:1 jar port (temp/cache/26.2-inner.jar, javap -c -p, verified this session). Sources:
//   - net.minecraft.world.item.ItemStack.MAP_CODEC / lambda$static$1 (the record builder):
//       "id"        = Item.CODEC_WITH_BOUND_COMPONENTS               (Holder<Item> id string, REQUIRED)
//       "count"     = optionalAlwaysPresentFieldOf(intRange(1,99),"count",1)  (always written, default 1)
//       "components"= DataComponentPatch.CODEC.optionalFieldOf("components",EMPTY)  (omitted when empty)
//   - net.minecraft.world.ItemStackWithSlot.lambda$static$0:
//       "Slot" (CAPITAL S) = ExtraCodecs.UNSIGNED_BYTE, default 0, optionalAlwaysPresentFieldOf
//                            (always written); the ItemStack.MAP_CODEC is FLATTENED into the SAME
//                            compound (no nested "stack" wrapper).
//   - net.minecraft.world.ItemStackWithSlot.isValidInContainer = slot >= 0 && slot < size.
//   - net.minecraft.world.ContainerHelper.saveAllItems(out,list,boolean keepEmptyTag):
//       out.list("Items", ItemStackWithSlot.CODEC); for i in 0..size: if !stack.isEmpty()
//       typedList.add(new ItemStackWithSlot(i, stack)); if (typedList.isEmpty() && !keepEmptyTag)
//       out.discard("Items"). The 2-arg form delegates with keepEmptyTag=true.
//   - net.minecraft.world.ContainerHelper.loadAllItems(in,list):
//       in.listOrEmpty("Items", ItemStackWithSlot.CODEC); for each e: if e.isValidInContainer(
//       list.size()) list.set(e.slot(), e.stack()).
//
// ============================ PHASE A vs PHASE B ============================
// PHASE A (THIS file, fully 1:1 faithful): persist {Slot, id, count}. This round-trips PERFECTLY
// any stack WITHOUT components (the common case: plain blocks, food, un-enchanted/un-named tools).
//
// COMPONENTS (the WIRE↔DISK GAP, see SUB-ITEMNBT-jar-spec.md §WIRE-VS-DISK GAP): Sulfur holds a
// component-bearing stack only as opaque WIRE bytes (level/component.SlotData.RawComponents —
// registry-id-indexed, streamCodec-encoded). The DISK "components" compound is string-keyed
// (PatchKey: "namespace:path", "!"-prefixed removals) with each value encoded by the component's
// type.codec() (the DATA codec), a DIFFERENT serializer from the wire type.streamCodec(). The raw
// wire bytes therefore CANNOT be written into disk NBT — a wire→disk transcoder is required.
//
// THIS file does NOT fake that transcoder. A stack flagged as carrying components (DiskItem.
// HasComponents) is persisted as {Slot, id, count} ONLY — the item & count survive, the
// components (enchants/custom_name/damage/...) are DROPPED. The drop is NEVER silent: SaveAllItems
// returns a DroppedComponents count so the caller can log/meter the fidelity gap. The Components
// field is a *DiskComponents pointer left nil in Phase A; Phase B fills it via the per-
// DataComponentType streamCodec→value→codec transcoder, and ItemStackWithSlotDisk already carries
// the omitempty "components" slot so Phase B enchant-NBT plugs in WITHOUT reshaping this codec.
// TODO(SUB-ITEMNBT Phase B): build the per-DataComponentType codec()/streamCodec() transcoder
//   (DataComponentPatch.CODEC, PatchKey, type.codecOrThrow) so component-bearing stacks round-trip
//   on disk. Until then component-bearing stacks are lossy-but-structurally-vanilla.
// ===========================================================================

// TagItems is ContainerHelper.TAG_ITEMS — the NBT list key for a container's contents. CITE:
// ContainerHelper.TAG_ITEMS == "Items".
const TagItems = "Items"

// itemCountMin / itemCountMax are the disk count clamp ExtraCodecs.intRange(1,99) applies on
// decode (ItemStack.lambda$static$1: ExtraCodecs.intRange(1, 99) for "count"). A stack never
// persists with count<=0 (empty stacks are skipped, §5); on read an out-of-range count is clamped.
const (
	itemCountMin = 1
	itemCountMax = 99
)

// DiskComponents is the on-disk "components" compound: a string-keyed map (component registry id,
// with a "!" prefix for removals) to each component's type.codec() (DATA/NBT) value. It is the
// Phase-B carrier; Phase A always leaves it nil (components dropped). CITE: DataComponentPatch.CODEC
// = dispatchedMap(PatchKey.CODEC, PatchKey::valueCodec).
//
// nbt.RawMessage values keep the per-component delegation opaque to this container codec (the
// transcoder, Phase B, produces them); the map shape (string keys, "!"-prefix) is the only
// structural commitment this type makes — matching the jar's dispatchedMap exactly.
type DiskComponents map[string]nbt.RawMessage

// ItemStackDisk is the flattened ItemStack.MAP_CODEC record: "id" (the Holder<Item> identifier
// string, e.g. "minecraft:diamond_sword"), "count" (int 1..99, ALWAYS written, default 1), and the
// optional "components" compound (omitted when empty, Phase A always nil). The fields are FLATTENED
// (no wrapper) so this struct embeds directly into ItemStackWithSlotDisk's compound — matching
// ItemStack.MAP_CODEC being used via forGetter inside ItemStackWithSlot.CODEC.
type ItemStackDisk struct {
	// ID is the namespaced item id ("minecraft:<name>"). REQUIRED (no default). CITE: "id" =
	// Item.CODEC_WITH_BOUND_COMPONENTS, a Codec<Holder<Item>> whose on-disk shape is the id string.
	ID string `nbt:"id"`
	// Count is the NBT int stack count, 1..99, ALWAYS present (optionalAlwaysPresentFieldOf wraps
	// the value in Optional.of unconditionally), default 1 on a missing key. CITE: ItemStack
	// lambda$static$1 "count".
	Count int32 `nbt:"count"`
	// Components is the optional disk component compound. omitempty drops the key when nil — exactly
	// optionalFieldOf("components", EMPTY) omitting an empty patch. Phase A: always nil (Phase B fills).
	Components *DiskComponents `nbt:"components,omitempty"`
}

// ItemStackWithSlotDisk is one element of the "Items" list: the "Slot" byte (UNSIGNED_BYTE, capital
// S, default 0, ALWAYS present) with the ItemStack record FLATTENED into the SAME compound (id/count
// /components are siblings of Slot, not nested). CITE: ItemStackWithSlot.lambda$static$0.
type ItemStackWithSlotDisk struct {
	// Slot is the container index this stack occupies, encoded as an NBT byte (UNSIGNED_BYTE), the
	// Go field name "Slot" giving the capital-S key the jar uses. CITE: ldc "Slot",
	// ExtraCodecs.UNSIGNED_BYTE.
	Slot byte
	// The flattened ItemStack.MAP_CODEC record (id/count/components in the SAME compound as Slot).
	ItemStackDisk
}

// DiskItem is the codec's INPUT/OUTPUT abstraction for one container slot, decoupling the disk codec
// from any particular in-memory item type (the wire component.SlotData, the player Inventory slot,
// the chest chestLoot slot). The server translates its tick-owned item into a DiskItem (resolving
// the numeric wire id → "minecraft:<name>" string id via the item registry) before SaveAllItems,
// and back after LoadAllItems. Keeping the codec id-string-based (not numeric) is faithful: the disk
// format is a registry IDENTIFIER string, and the numeric↔string mapping is a registry concern, not
// a serialization one.
type DiskItem struct {
	// ID is the namespaced item id ("minecraft:<name>"). An empty ID denotes an EMPTY slot (skipped
	// on save; never produced on load — load only yields present stacks).
	ID string
	// Count is the stack size; <=0 denotes EMPTY (skipped on save, ContainerHelper.saveAllItems
	// stack.isEmpty()). On load it is the clamped (1..99) disk count.
	Count int32
	// HasComponents flags that the source stack carried wire components (SlotData.RawComponents
	// non-empty) which Phase A CANNOT persist. SaveAllItems counts these as DroppedComponents so the
	// loss is measurable. Phase B replaces this flag with the transcoded DiskComponents.
	HasComponents bool
}

// IsEmpty reports whether the DiskItem is an empty container slot (no id, or count<=0). CITE:
// ItemStack.isEmpty() (an empty stack is ItemStack.EMPTY: air / count 0). saveAllItems skips it.
func (d DiskItem) IsEmpty() bool {
	return d.ID == "" || d.ID == "minecraft:air" || d.Count <= 0
}

// isValidInContainer ports ItemStackWithSlot.isValidInContainer(int size) = slot >= 0 && slot <
// size. Plain half-open bounds; an out-of-range slot is silently skipped on load. CITE bytecode:
// iflt / if_icmpge. The slot is a byte (0..255) so slot>=0 is always true, but the guard is kept
// verbatim for a faithful port (and so the function is correct for any int caller).
func isValidInContainer(slot, size int) bool {
	return slot >= 0 && slot < size
}

// clampCount applies ExtraCodecs.intRange(1,99) the disk "count" codec enforces on DECODE: a count
// below 1 becomes 1, above 99 becomes 99. CITE: ItemStack lambda$static$1 intRange(1,99) for "count".
func clampCount(c int32) int32 {
	if c < itemCountMin {
		return itemCountMin
	}
	if c > itemCountMax {
		return itemCountMax
	}
	return c
}

// SaveAllItems ports ContainerHelper.saveAllItems(out, list, keepEmptyTag): build the "Items" list
// of ItemStackWithSlotDisk, skipping EMPTY stacks, with each element's Slot = its index in list.
// Returns the encoded list (nil if no items AND !keepEmptyTag — the discard("Items") path; a
// non-nil empty slice if keepEmptyTag) plus droppedComponents, the count of stacks whose components
// were NOT persisted (Phase A loss — see the file header). The caller writes the returned list under
// the "Items" key (or omits it when nil) and may log droppedComponents.
//
// CITE: saveAllItems(out,list,boolean) bytecode — for i in 0..size, if !stack.isEmpty()
// typedList.add(new ItemStackWithSlot(i, stack)); if (typedList.isEmpty() && !keepEmptyTag)
// out.discard("Items").
func SaveAllItems(list []DiskItem, keepEmptyTag bool) (items []ItemStackWithSlotDisk, droppedComponents int) {
	for i, it := range list {
		if it.IsEmpty() {
			continue // ItemStack.isEmpty() -> not added (sparse, slot-keyed list)
		}
		if it.HasComponents {
			// Phase A: components cannot be transcoded to disk NBT yet — drop them (item & count
			// survive). Counted so the fidelity gap is measurable, never silent. TODO Phase B.
			droppedComponents++
		}
		items = append(items, ItemStackWithSlotDisk{
			Slot: byte(i), // ItemStackWithSlot(i, stack): Slot = the NonNullList index
			ItemStackDisk: ItemStackDisk{
				ID:    it.ID,
				Count: clampCount(it.Count),
				// Components: nil in Phase A (Phase B: transcoded DiskComponents).
			},
		})
	}
	// if (typedList.isEmpty() && !flag) out.discard("Items"): an empty list with keepEmptyTag=false
	// returns nil so the caller discards the key entirely. keepEmptyTag=true leaves an (empty) list.
	if len(items) == 0 && !keepEmptyTag {
		return nil, droppedComponents
	}
	if items == nil {
		// keepEmptyTag && no items: a non-nil empty slice so the caller still writes an empty "Items".
		items = []ItemStackWithSlotDisk{}
	}
	return items, droppedComponents
}

// LoadAllItems ports ContainerHelper.loadAllItems(in, list): for each ItemStackWithSlotDisk in the
// "Items" list, if isValidInContainer(slot, size) place its stack at list[slot]. The caller passes
// the decoded items and the container size; LoadAllItems returns a freshly-allocated, EMPTY-padded
// []DiskItem of length size with the present stacks placed at their slots (out-of-range slots
// silently skipped). This mirrors NonNullList.withSize(size, EMPTY) then list.set(slot, stack).
//
// CITE: loadAllItems — listOrEmpty("Items"); for each e: if e.isValidInContainer(list.size())
// list.set(e.slot(), e.stack()).
func LoadAllItems(items []ItemStackWithSlotDisk, size int) []DiskItem {
	out := make([]DiskItem, size) // NonNullList.withSize(size, EMPTY): all slots empty (zero DiskItem)
	for _, e := range items {
		slot := int(e.Slot)
		if !isValidInContainer(slot, size) {
			continue // out-of-range slot: silently skipped (no throw), slot keeps EMPTY
		}
		out[slot] = DiskItem{
			ID:    e.ID,
			Count: clampCount(e.Count),
			// HasComponents: a Phase-A-saved item carries no "components" tag; a Phase-B item would
			// decode e.Components here. Left false (no components round-tripped in Phase A).
			HasComponents: e.Components != nil,
		}
	}
	return out
}

// itemsListShape is the wrapper compound the "Items" list serializes inside — a single NBT compound
// {Items: [ItemStackWithSlot...]}. It exists so SaveItemsCompound/LoadItemsCompound can round-trip
// the bare {Items:...} payload (e.g. a chest BlockEntity's contents compound) via the nbt encoder.
// omitempty drops the key when the list is nil (the saveAllItems discard path).
type itemsListShape struct {
	Items []ItemStackWithSlotDisk `nbt:"Items,omitempty"`
}

// SaveItemsCompound encodes a container's items as the bare {Items:[...]} NBT compound payload (the
// 3-byte document root header stripped, the chestLootNBT/BlockEntity.Data convention) so it can be
// embedded directly as a BlockEntity.Data compound (a chest's contents). keepEmptyTag follows
// SaveAllItems. Returns the bare compound bytes + droppedComponents. A nil/empty result (no items,
// !keepEmptyTag) still encodes as an empty compound (TagEnd-terminated) so BlockEntity.Data is valid.
func SaveItemsCompound(list []DiskItem, keepEmptyTag bool) (nbt.RawMessage, int, error) {
	items, dropped := SaveAllItems(list, keepEmptyTag)
	doc, err := nbt.Marshal(itemsListShape{Items: items})
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	// nbt.Marshal emits [0x0A tag][0x00 0x00 name-len][payload...]; strip the 3-byte root header so
	// the result is the bare compound payload (the BlockEntity.Data convention, chestLootNBT).
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// LoadItemsCompound decodes a bare {Items:[...]} compound (a BlockEntity.Data payload or any
// container NBT) back into an EMPTY-padded []DiskItem of length size. A compound with no "Items"
// key (or a non-compound / garbled payload) yields an all-empty container (listOrEmpty semantics) —
// never an error from a missing list, faithful to loadAllItems' tolerant in.listOrEmpty. A genuine
// NBT decode error (truncated payload) is surfaced.
func LoadItemsCompound(data nbt.RawMessage, size int) ([]DiskItem, error) {
	var shape itemsListShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		if err := data.Unmarshal(&shape); err != nil {
			return nil, err
		}
	}
	return LoadAllItems(shape.Items, size), nil
}

// encodeItemsList / decodeItemsList are the round-trip helpers the tests and the player-inventory
// path use to (de)serialize a bare "Items" list to/from NBT bytes without the compound wrapper
// indirection — the same itemsListShape, exposed for direct list round-trips.
func encodeItemsList(items []ItemStackWithSlotDisk) ([]byte, error) {
	var buf bytes.Buffer
	if err := nbt.NewEncoder(&buf).Encode(itemsListShape{Items: items}, ""); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeItemsList(data []byte) ([]ItemStackWithSlotDisk, error) {
	var shape itemsListShape
	if _, err := nbt.NewDecoder(bytes.NewReader(data)).Decode(&shape); err != nil {
		return nil, err
	}
	return shape.Items, nil
}
