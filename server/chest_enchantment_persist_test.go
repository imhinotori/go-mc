package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// chest_enchantment_persist_test.go — the server-level round-trip proving the ENCHANTMENT id→string
// resolver closes the item-component disk gap end to end: an enchanted diamond sword (sharpness 3 +
// unbreaking 2) placed in a chest persists its minecraft:enchantments component to disk NBT keyed by
// the RESOURCE ID (ItemEnchantments.CODEC = unboundedMap(Enchantment.CODEC (resource id), level)) and
// reloads to the IDENTICAL wire numeric ids through the real flushChestItems / resolveChest path.

// armServerEnchantmentRegistry injects the ordered ENCHANTMENT registry (the same content the server
// sends to the client) into the save layer for the duration of the test, and returns the ordered list
// so the test can map resource id -> wire numeric id without hardcoding the datapack index. Restores an
// empty resolver on cleanup.
func armServerEnchantmentRegistry(t *testing.T) []string {
	t.Helper()
	order, err := registrydata.EnchantmentOrder()
	if err != nil {
		t.Fatalf("EnchantmentOrder: %v", err)
	}
	save.SetEnchantmentRegistry(order)
	t.Cleanup(func() { save.SetEnchantmentRegistry(nil) })
	return order
}

func enchWireID(t *testing.T, order []string, id string) int32 {
	t.Helper()
	for i, n := range order {
		if n == id {
			return int32(i)
		}
	}
	t.Fatalf("enchantment %q not in registry order", id)
	return -1
}

// enchantmentsWireSpan encodes the minecraft:enchantments component wire body (VarInt typeId, then the
// ItemEnchantments.STREAM_CODEC: VarInt count, count×(VarInt id, VarInt level)) as one added component,
// returning the SlotData RawComponents span + its AddedCount(=1). It re-derives the wire typeId from
// component.NewComponent so it asserts against the real registry, not a hardcode.
func enchantmentsWireSpan(t *testing.T, entries []component.EnchantmentEntry) ([]byte, int32) {
	t.Helper()
	ench := &component.Enchantments{Enchantments: entries}
	var typeID int32 = -1
	for id := int32(0); id < 256; id++ {
		if p := component.NewComponent(id); p != nil && p.ID() == ench.ID() {
			typeID = id
			break
		}
	}
	if typeID < 0 {
		t.Fatal("no wire id for minecraft:enchantments")
	}
	var buf bytes.Buffer
	if _, err := pk.VarInt(typeID).WriteTo(&buf); err != nil {
		t.Fatalf("write typeId: %v", err)
	}
	if _, err := ench.WriteTo(&buf); err != nil {
		t.Fatalf("write enchantments body: %v", err)
	}
	return buf.Bytes(), 1
}

// TestChestEnchantedItemReloadsFromBE saves an enchanted diamond sword into a chest BE and reloads it,
// asserting (1) the persisted disk tag shape matches ItemEnchantments.CODEC (a "minecraft:enchantments"
// compound keyed by resource id with TAG_Int levels) and (2) the reloaded stack carries the IDENTICAL
// wire numeric ids + levels — the full runtime save→disk→load round-trip through flushChestItems /
// resolveChest with the enchantment resolver armed.
func TestChestEnchantedItemReloadsFromBE(t *testing.T) {
	order := armServerEnchantmentRegistry(t)
	sharpID := enchWireID(t, order, "minecraft:sharpness")
	unbrID := enchWireID(t, order, "minecraft:unbreaking")

	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	chestPos := pk.Position{X: 7, Y: 65, Z: 8}
	loop.only().world.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	swordID := itemNameToID("minecraft:diamond_sword")
	span, added := enchantmentsWireSpan(t, []component.EnchantmentEntry{
		{ID: pk.VarInt(sharpID), Level: 3},
		{ID: pk.VarInt(unbrID), Level: 2},
	})

	cl := &chestLoot{LootTable: ""} // rolled
	cl.ensureContainer()
	cl.items[0] = component.SlotData{
		Count:         1,
		ItemID:        pk.VarInt(swordID),
		AddedCount:    pk.VarInt(added),
		RawComponents: span,
	}
	loop.openChests = map[pk.Position]*chestLoot{chestPos: cl}

	// Record an empty chest BE cell so flushChestItems rewrites it in place, then SAVE.
	be := level.BlockEntity{Y: int16(chestPos.Y), Type: block.EntityTypes["minecraft:chest"]}
	be.PackXZ(chestPos.X&15, chestPos.Z&15)
	ch.BlockEntity = append(ch.BlockEntity, be)
	loop.flushChestItems(level.ChunkPos{0, 0}, ch)

	// Assert the persisted disk shape: Items[0].components["minecraft:enchantments"] is a compound keyed
	// by resource id with TAG_Int levels (ItemEnchantments.CODEC).
	beData := findChestBEData(t, ch, chestPos)
	var disk struct {
		Items []struct {
			Slot       byte                      `nbt:"Slot"`
			ID         string                    `nbt:"id"`
			Count      int32                     `nbt:"count"`
			Components map[string]nbt.RawMessage `nbt:"components"`
		} `nbt:"Items"`
	}
	if err := beData.Unmarshal(&disk); err != nil {
		t.Fatalf("decode chest BE: %v", err)
	}
	if len(disk.Items) != 1 {
		t.Fatalf("Items = %d, want 1", len(disk.Items))
	}
	ev, ok := disk.Items[0].Components["minecraft:enchantments"]
	if !ok {
		t.Fatalf("no minecraft:enchantments on disk; components=%v", disk.Items[0].Components)
	}
	if ev.Type != nbt.TagCompound {
		t.Fatalf("enchantments disk tag = %d, want TAG_Compound(%d)", ev.Type, nbt.TagCompound)
	}
	var levels map[string]nbt.RawMessage
	if err := ev.Unmarshal(&levels); err != nil {
		t.Fatalf("decode enchantment map: %v", err)
	}
	for _, want := range []string{"minecraft:sharpness", "minecraft:unbreaking"} {
		lv, ok := levels[want]
		if !ok {
			t.Errorf("missing %q on disk; keys=%v", want, levels)
			continue
		}
		if lv.Type != nbt.TagInt {
			t.Errorf("level for %q tag = %d, want TAG_Int", want, lv.Type)
		}
	}

	// Simulate a reload: drop the live chest, then re-resolve from the chunk BE.
	delete(loop.openChests, chestPos)
	got := loop.resolveChest(chestPos)
	if got == nil {
		t.Fatal("resolveChest returned nil for a persisted enchanted chest")
	}
	got.ensureContainer()
	slot := got.items[0]
	if int32(slot.ItemID) != swordID || slot.Count != 1 {
		t.Fatalf("reloaded slot 0 = %+v, want diamond_sword x1", slot)
	}
	if int(slot.AddedCount) != 1 {
		t.Fatalf("reloaded AddedCount = %d, want 1 (the enchantments component)", slot.AddedCount)
	}
	// Decode the reloaded wire span and assert the IDENTICAL enchantment ids + levels.
	r := bytes.NewReader(slot.RawComponents)
	var gotType pk.VarInt
	if _, err := gotType.ReadFrom(r); err != nil {
		t.Fatalf("read reloaded typeId: %v", err)
	}
	var gotEnch component.Enchantments
	if _, err := gotEnch.ReadFrom(r); err != nil {
		t.Fatalf("read reloaded enchantments: %v", err)
	}
	byID := map[int32]int32{}
	for _, e := range gotEnch.Enchantments {
		byID[int32(e.ID)] = int32(e.Level)
	}
	if byID[sharpID] != 3 {
		t.Errorf("reloaded sharpness (id %d) level = %d, want 3", sharpID, byID[sharpID])
	}
	if byID[unbrID] != 2 {
		t.Errorf("reloaded unbreaking (id %d) level = %d, want 2", unbrID, byID[unbrID])
	}
	if len(gotEnch.Enchantments) != 2 {
		t.Errorf("reloaded enchantment count = %d, want 2", len(gotEnch.Enchantments))
	}
}

// findChestBEData returns the Data compound of the chest BE at the local cell of pos in ch.
func findChestBEData(t *testing.T, ch *level.Chunk, pos pk.Position) nbt.RawMessage {
	t.Helper()
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == pos.Y {
			return be.Data
		}
	}
	t.Fatalf("no chest BE at %v", pos)
	return nbt.RawMessage{}
}
