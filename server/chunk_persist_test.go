package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// TestChestItemsRoundTrip is the SUB-PERSIST chest flush: a ROLLED chest (LootTable cleared) has its
// container folded into the chunk BlockEntity as the disk "Items" NBT (ChestBlockEntity.saveAdditional
// → saveAllItems), and that BE Items list decodes back to the same {slot,id,count} container.
func TestChestItemsRoundTrip(t *testing.T) {
	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	// Place a chest block + a rolled chestLoot (LootTable already cleared, container filled).
	chestPos := pk.Position{X: 3, Y: 65, Z: 4}
	loop.only().world.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)
	be := level.BlockEntity{Y: int16(chestPos.Y), Type: block.EntityTypes["minecraft:chest"]}
	be.PackXZ(chestPos.X&15, chestPos.Z&15)
	ch.BlockEntity = append(ch.BlockEntity, be)

	cl := &chestLoot{LootTable: ""} // "" == already rolled (trySaveLootTable false → saveAllItems)
	cl.ensureContainer()
	// Stone item registry id: resolve via the same itemNameToID the loader uses.
	stoneID := itemNameToID("minecraft:stone")
	appleID := itemNameToID("minecraft:apple")
	cl.items[0] = component.SlotData{Count: 64, ItemID: pk.VarInt(stoneID)}
	cl.items[5] = component.SlotData{Count: 3, ItemID: pk.VarInt(appleID)}
	if loop.openChests == nil {
		loop.openChests = make(map[pk.Position]*chestLoot)
	}
	loop.openChests[chestPos] = cl

	// Flush the chest items into the chunk BE (the save path's chest fold).
	loop.flushChestItems(level.ChunkPos{0, 0}, ch)

	// The chest BE Data must now carry the {Items:[...]} compound; decode it back.
	beData := findChestBE(t, ch, chestPos)
	out, err := save.LoadItemsCompound(beData, chestContainerSize)
	if err != nil {
		t.Fatalf("LoadItemsCompound: %v", err)
	}
	if out[0].ID != "minecraft:stone" || out[0].Count != 64 {
		t.Errorf("slot 0 = %+v, want stone x64", out[0])
	}
	if out[5].ID != "minecraft:apple" || out[5].Count != 3 {
		t.Errorf("slot 5 = %+v, want apple x3", out[5])
	}
	// Every other slot empty.
	for i, it := range out {
		if i == 0 || i == 5 {
			continue
		}
		if !it.IsEmpty() {
			t.Errorf("slot %d not empty: %+v", i, it)
		}
	}
}

// TestChestUnrolledNotFlushed proves an UN-rolled chest (LootTable still set) keeps its loot-table BE
// (trySaveLootTable true → no Items written) — the items have not been generated yet.
func TestChestUnrolledNotFlushed(t *testing.T) {
	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	chestPos := pk.Position{X: 7, Y: 65, Z: 2}
	be := level.BlockEntity{Y: int16(chestPos.Y), Type: block.EntityTypes["minecraft:chest"], Data: testChestLootNBT("minecraft:chests/simple_dungeon", 999)}
	be.PackXZ(chestPos.X&15, chestPos.Z&15)
	ch.BlockEntity = append(ch.BlockEntity, be)

	cl := &chestLoot{LootTable: "minecraft:chests/simple_dungeon", LootTableSeed: 999} // NOT rolled
	if loop.openChests == nil {
		loop.openChests = make(map[pk.Position]*chestLoot)
	}
	loop.openChests[chestPos] = cl

	loop.flushChestItems(level.ChunkPos{0, 0}, ch)

	// The BE must STILL be the {LootTable,LootTableSeed} compound (no Items written).
	beData := findChestBE(t, ch, chestPos)
	var lt struct {
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}
	if err := beData.Unmarshal(&lt); err != nil {
		t.Fatalf("decode BE: %v", err)
	}
	if lt.LootTable != "minecraft:chests/simple_dungeon" || lt.LootTableSeed != 999 {
		t.Errorf("un-rolled chest BE = %+v, want loot-table preserved", lt)
	}
}

// TestInventoryRoundTrip_TickToDisk proves the player-inventory codec round-trips THROUGH the
// tick-owned slot form: inventoryToItems → save.ItemStackWithSlot list → itemsToInventory back.
func TestInventoryRoundTrip_TickToDisk(t *testing.T) {
	inv := newInventory()
	stoneID := itemNameToID("minecraft:stone")
	swordID := itemNameToID("minecraft:diamond_sword")
	inv.slots[0] = component.SlotData{Count: 64, ItemID: pk.VarInt(stoneID)}
	inv.slots[8] = component.SlotData{Count: 1, ItemID: pk.VarInt(swordID)}

	items := inventoryToItems(inv)
	if len(items) != 2 {
		t.Fatalf("saved items = %d, want 2", len(items))
	}

	restored := itemsToInventory(items, len(inv.slots))
	if restored[0].Count != 64 || int32(restored[0].ItemID) != stoneID {
		t.Errorf("slot 0 = %+v, want stone x64", restored[0])
	}
	if restored[8].Count != 1 || int32(restored[8].ItemID) != swordID {
		t.Errorf("slot 8 = %+v, want diamond_sword x1", restored[8])
	}
	// Other slots empty.
	for i, s := range restored {
		if i == 0 || i == 8 {
			continue
		}
		if s.Count > 0 {
			t.Errorf("slot %d not empty: %+v", i, s)
		}
	}
}

// TestChestItemComponentsRoundTrip is the SUB-ITEMNBT Phase B end-to-end chest flush: a chest holding a
// NAMED + DAMAGED sword (supported components) folds into the chunk BE disk NBT with its components
// compound, and decodeChestItems rebuilds the wire component span byte-stable. It also proves an item
// carrying an UNSUPPORTED component (minecraft:unbreakable) still round-trips id+count while the
// unsupported component is dropped (metered, not silent).
func TestChestItemComponentsRoundTrip(t *testing.T) {
	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	chestPos := pk.Position{X: 3, Y: 65, Z: 4}
	loop.only().world.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)
	be := level.BlockEntity{Y: int16(chestPos.Y), Type: block.EntityTypes["minecraft:chest"]}
	be.PackXZ(chestPos.X&15, chestPos.Z&15)
	ch.BlockEntity = append(ch.BlockEntity, be)

	cl := &chestLoot{LootTable: ""}
	cl.ensureContainer()
	swordID := itemNameToID("minecraft:diamond_sword")

	// Slot 0: a named + damaged sword (custom_name + damage — both supported, byte-stable round-trip).
	named := buildComponentSpanServer(t,
		&component.Damage{VarInt: 100},
		&component.CustomName{Name: chat.Message{Text: "Excalibur"}},
	)
	cl.items[0] = component.SlotData{Count: 1, ItemID: pk.VarInt(swordID), AddedCount: 2, RawComponents: named}

	// Slot 2: a sword with an UNSUPPORTED component (unbreakable, wire id 4) — dropped but item survives.
	unbuf := buildComponentSpanServerID(t, 4)
	cl.items[2] = component.SlotData{Count: 1, ItemID: pk.VarInt(swordID), AddedCount: 1, RawComponents: unbuf}

	if loop.openChests == nil {
		loop.openChests = make(map[pk.Position]*chestLoot)
	}
	loop.openChests[chestPos] = cl
	loop.flushChestItems(level.ChunkPos{0, 0}, ch)

	beData := findChestBE(t, ch, chestPos)
	got := decodeChestItems(beData)

	// Slot 0: components must round-trip. Re-decode both spans and compare the transcoded set.
	if got[0].Count != 1 || int32(got[0].ItemID) != swordID {
		t.Fatalf("slot 0 id/count lost: %+v", got[0])
	}
	if int(got[0].AddedCount) != 2 {
		t.Errorf("slot 0 added count = %d, want 2 (damage + custom_name)", got[0].AddedCount)
	}
	assertServerSpanHasComponents(t, got[0].RawComponents, int(got[0].AddedCount),
		map[string]bool{"minecraft:damage": true, "minecraft:custom_name": true})

	// Slot 2: item survives, unsupported component dropped (no components on the reconstructed span).
	if got[2].Count != 1 || int32(got[2].ItemID) != swordID {
		t.Fatalf("slot 2 id/count lost: %+v", got[2])
	}
	if got[2].AddedCount != 0 || len(got[2].RawComponents) != 0 {
		t.Errorf("slot 2 unsupported component should be dropped; got added=%d raw=%x", got[2].AddedCount, got[2].RawComponents)
	}
}

// buildComponentSpanServer encodes an added-component list (typeId + streamCodec body) into the raw
// wire span component.SlotData carries (test helper).
func buildComponentSpanServer(t *testing.T, comps ...component.DataComponent) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range comps {
		id := serverComponentWireID(t, c)
		if _, err := pk.VarInt(id).WriteTo(&buf); err != nil {
			t.Fatalf("write typeId: %v", err)
		}
		if _, err := c.WriteTo(&buf); err != nil {
			t.Fatalf("write component %T: %v", c, err)
		}
	}
	return buf.Bytes()
}

// buildComponentSpanServerID builds a 1-component added span for the given wire id (test helper).
func buildComponentSpanServerID(t *testing.T, id int32) []byte {
	t.Helper()
	c := component.NewComponent(id)
	if c == nil {
		t.Fatalf("NewComponent(%d) nil", id)
	}
	var buf bytes.Buffer
	if _, err := pk.VarInt(id).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serverComponentWireID resolves a component's wire id via NewComponent (test helper).
func serverComponentWireID(t *testing.T, c component.DataComponent) int32 {
	t.Helper()
	for id := int32(0); id < 256; id++ {
		if p := component.NewComponent(id); p != nil && p.ID() == c.ID() {
			return id
		}
	}
	t.Fatalf("no wire id for %s", c.ID())
	return -1
}

// assertServerSpanHasComponents decodes an added span and asserts it contains exactly the wanted
// component ids (test helper).
func assertServerSpanHasComponents(t *testing.T, span []byte, added int, want map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	r := bytes.NewReader(span)
	for i := 0; i < added; i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			t.Fatalf("read typeId: %v", err)
		}
		c := component.NewComponent(int32(typeID))
		if c == nil {
			t.Fatalf("unknown component id %d", int32(typeID))
		}
		if _, err := c.ReadFrom(r); err != nil {
			t.Fatalf("read component: %v", err)
		}
		seen[c.ID()] = true
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("component %s missing from round-tripped span; saw %v", id, seen)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("component count = %d, want %d (%v)", len(seen), len(want), seen)
	}
}

// findChestBE returns the Data compound of the chest BE at the local cell of pos in ch (test helper).
func findChestBE(t *testing.T, ch *level.Chunk, pos pk.Position) nbt.RawMessage {
	t.Helper()
	chestType := block.EntityTypes["minecraft:chest"]
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == pos.Y && be.Type == chestType {
			return be.Data
		}
	}
	t.Fatalf("no chest BE at %v", pos)
	return nbt.RawMessage{}
}
