package server

import (
	"testing"

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
