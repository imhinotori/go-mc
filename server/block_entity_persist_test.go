package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestFurnaceStateRoundTrip proves a furnace mid-cook (Items in all 3 slots + cook progress shorts +
// RecipesUsed) serializes into the chunk BlockEntity list via flushFurnaceItems
// (AbstractFurnaceBlockEntity.saveAdditional) and decodes back via decodeFurnaceBE
// (loadAdditional) to the SAME items + cook state + recipes-used counts, and that the persisted disk tag
// NAMES match the 26.2 jar exactly (cooking_time_spent / cooking_total_time / lit_time_remaining /
// lit_total_time / Items / RecipesUsed).
func TestFurnaceStateRoundTrip(t *testing.T) {
	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	// Place a real furnace block + a mid-cook furnaceBE.
	fpos := pk.Position{X: 6, Y: 70, Z: 9}
	loop.only().world.SetBlock(fpos, block.ToStateID[block.Furnace{Facing: block.North, Lit: block.Boolean(true)}], dimMinY)

	potatoID := itemNameToID("minecraft:potato")   // smelting input
	coalID := itemNameToID("minecraft:coal")       // fuel
	bakedID := itemNameToID("minecraft:baked_potato")

	f := &furnaceBE{
		subtype:   cookSmelting,
		blastLike: false,
	}
	f.items[furnaceSlotInput] = component.SlotData{Count: 5, ItemID: pk.VarInt(potatoID)}
	f.items[furnaceSlotFuel] = component.SlotData{Count: 8, ItemID: pk.VarInt(coalID)}
	f.items[furnaceSlotResult] = component.SlotData{Count: 2, ItemID: pk.VarInt(bakedID)}
	f.cookingTimer = 137     // cooking_time_spent
	f.cookingTotalTime = 200 // cooking_total_time
	f.litTimeRemaining = 900 // lit_time_remaining
	f.litTotalTime = 1600    // lit_total_time
	// RecipesUsed: one completed baked_potato cook (the synthetic key + its experience).
	f.recipesUsed = map[string]int{"smelting:minecraft:baked_potato": 3}
	f.recipesXP = map[string]float64{"smelting:minecraft:baked_potato": 0.35}

	if loop.furnaces == nil {
		loop.furnaces = make(map[pk.Position]*furnaceBE)
	}
	loop.furnaces[fpos] = f

	// SAVE: fold the furnace into the chunk BE list.
	loop.flushFurnaceItems(level.ChunkPos{0, 0}, ch)

	// Assert the persisted tag NAMES match the jar exactly.
	beData := findFurnaceBEData(t, ch, fpos)
	var disk struct {
		Items []struct {
			Slot  byte   `nbt:"Slot"`
			ID    string `nbt:"id"`
			Count int32  `nbt:"count"`
		} `nbt:"Items"`
		CookingTimeSpent int16            `nbt:"cooking_time_spent"`
		CookingTotalTime int16            `nbt:"cooking_total_time"`
		LitTimeRemaining int16            `nbt:"lit_time_remaining"`
		LitTotalTime     int16            `nbt:"lit_total_time"`
		RecipesUsed      map[string]int32 `nbt:"RecipesUsed"`
	}
	if err := beData.Unmarshal(&disk); err != nil {
		t.Fatalf("decode furnace BE with jar tag names: %v", err)
	}
	if disk.CookingTimeSpent != 137 || disk.CookingTotalTime != 200 ||
		disk.LitTimeRemaining != 900 || disk.LitTotalTime != 1600 {
		t.Errorf("cook shorts = %+v, want 137/200/900/1600", disk)
	}
	if disk.RecipesUsed["smelting:minecraft:baked_potato"] != 3 {
		t.Errorf("RecipesUsed = %+v, want baked_potato:3", disk.RecipesUsed)
	}
	if len(disk.Items) != 3 {
		t.Fatalf("Items list = %d entries, want 3 (all slots filled)", len(disk.Items))
	}

	// LOAD: decode the BE back into a fresh furnaceBE (subtype/blastLike from the current block).
	got := decodeFurnaceBE(beData, cookSmelting, false)
	if int32(got.items[furnaceSlotInput].ItemID) != potatoID || got.items[furnaceSlotInput].Count != 5 {
		t.Errorf("input = %+v, want potato x5", got.items[furnaceSlotInput])
	}
	if int32(got.items[furnaceSlotFuel].ItemID) != coalID || got.items[furnaceSlotFuel].Count != 8 {
		t.Errorf("fuel = %+v, want coal x8", got.items[furnaceSlotFuel])
	}
	if int32(got.items[furnaceSlotResult].ItemID) != bakedID || got.items[furnaceSlotResult].Count != 2 {
		t.Errorf("result = %+v, want baked_potato x2", got.items[furnaceSlotResult])
	}
	if got.cookingTimer != 137 || got.cookingTotalTime != 200 ||
		got.litTimeRemaining != 900 || got.litTotalTime != 1600 {
		t.Errorf("loaded cook state = timer %d total %d lit %d litTotal %d, want 137/200/900/1600",
			got.cookingTimer, got.cookingTotalTime, got.litTimeRemaining, got.litTotalTime)
	}
	if got.recipesUsed["smelting:minecraft:baked_potato"] != 3 {
		t.Errorf("loaded RecipesUsed = %+v, want baked_potato:3", got.recipesUsed)
	}
	// experience() re-derived from the recipe book (baked_potato smelting = 0.35).
	if xp := got.recipesXP["smelting:minecraft:baked_potato"]; xp != 0.35 {
		t.Errorf("re-derived experience = %v, want 0.35", xp)
	}
}

// TestFurnaceResolveReloadsPersistedState proves the OPEN path (resolveFurnace) restores a persisted
// furnace from the chunk BE list — the full round-trip through the runtime seam: a saved furnace BE in the
// chunk decodes into t.furnaces on first open, not a fresh empty furnace.
func TestFurnaceResolveReloadsPersistedState(t *testing.T) {
	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	fpos := pk.Position{X: 2, Y: 66, Z: 3}
	state := block.ToStateID[block.Furnace{Facing: block.North, Lit: block.Boolean(false)}]
	loop.only().world.SetBlock(fpos, state, dimMinY)

	ironOreID := itemNameToID("minecraft:raw_iron")

	// Pre-seed a persisted furnace BE (as if loaded from disk): fold a live BE, then drop it from the map.
	seed := &furnaceBE{subtype: cookSmelting}
	seed.items[furnaceSlotInput] = component.SlotData{Count: 12, ItemID: pk.VarInt(ironOreID)}
	seed.cookingTimer = 44
	loop.furnaces = map[pk.Position]*furnaceBE{fpos: seed}
	loop.flushFurnaceItems(level.ChunkPos{0, 0}, ch)
	delete(loop.furnaces, fpos) // simulate a chunk unload/reload: BE is on disk, not in the live map

	// resolveFurnace must restore from the chunk BE (not build an empty furnace).
	got := loop.resolveFurnace(fpos, state)
	if got == nil {
		t.Fatal("resolveFurnace returned nil for a persisted furnace")
	}
	if int32(got.items[furnaceSlotInput].ItemID) != ironOreID || got.items[furnaceSlotInput].Count != 12 {
		t.Errorf("restored input = %+v, want raw_iron x12", got.items[furnaceSlotInput])
	}
	if got.cookingTimer != 44 {
		t.Errorf("restored cookingTimer = %d, want 44", got.cookingTimer)
	}
}

// TestChestItemsReloadFromBE proves the CHEST LOAD gap is closed: a rolled chest saved as the "Items" disk
// list (no LootTable) decodes back through decodeChestBE into the chestLoot container, so a reloaded rolled
// chest keeps its contents (ChestBlockEntity.loadAdditional: if !tryLoadLootTable loadAllItems).
func TestChestItemsReloadFromBE(t *testing.T) {
	loop, _ := newBlockLoop()
	ch, _ := loop.only().world.Get(level.ChunkPos{0, 0})

	chestPos := pk.Position{X: 4, Y: 64, Z: 5}
	loop.only().world.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	// Roll + flush a chest into the chunk BE list (the SAVE side, exercised by chunk_persist_test).
	stoneID := itemNameToID("minecraft:stone")
	diamondID := itemNameToID("minecraft:diamond")
	cl := &chestLoot{LootTable: ""} // rolled
	cl.ensureContainer()
	cl.items[2] = component.SlotData{Count: 32, ItemID: pk.VarInt(stoneID)}
	cl.items[26] = component.SlotData{Count: 1, ItemID: pk.VarInt(diamondID)}
	loop.openChests = map[pk.Position]*chestLoot{chestPos: cl}

	// Record an empty chest BE cell so flushChestItems rewrites it in place.
	be := level.BlockEntity{Y: int16(chestPos.Y), Type: block.EntityTypes["minecraft:chest"]}
	be.PackXZ(chestPos.X&15, chestPos.Z&15)
	ch.BlockEntity = append(ch.BlockEntity, be)
	loop.flushChestItems(level.ChunkPos{0, 0}, ch)

	// Simulate a reload: drop the live openChests entry, then re-resolve from the chunk BE.
	delete(loop.openChests, chestPos)
	got := loop.resolveChest(chestPos)
	if got == nil {
		t.Fatal("resolveChest returned nil for a persisted chest")
	}
	got.ensureContainer()
	if int32(got.items[2].ItemID) != stoneID || got.items[2].Count != 32 {
		t.Errorf("reloaded slot 2 = %+v, want stone x32", got.items[2])
	}
	if int32(got.items[26].ItemID) != diamondID || got.items[26].Count != 1 {
		t.Errorf("reloaded slot 26 = %+v, want diamond x1", got.items[26])
	}
	if got.LootTable != "" {
		t.Errorf("reloaded LootTable = %q, want empty (rolled chest)", got.LootTable)
	}
	// Every other slot empty.
	for i := 0; i < chestContainerSize; i++ {
		if i == 2 || i == 26 {
			continue
		}
		if got.items[i].Count > 0 {
			t.Errorf("slot %d not empty: %+v", i, got.items[i])
		}
	}
}

// findFurnaceBEData returns the Data compound of the furnace BE at the local cell of pos in ch (test helper).
func findFurnaceBEData(t *testing.T, ch *level.Chunk, pos pk.Position) nbt.RawMessage {
	t.Helper()
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == pos.Y && isFurnaceEntityType(be.Type) {
			return be.Data
		}
	}
	t.Fatalf("no furnace BE at %v", pos)
	return nbt.RawMessage{}
}
