package level

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
)

// chunkLootNBT builds a bare chest BE Data payload {LootTable,LootTableSeed} (the gen-time shape,
// root header stripped) — mirrors world.chestLootNBT so the test exercises the real on-disk format.
func chunkLootNBT(table string, seed int64) nbt.RawMessage {
	doc, err := nbt.Marshal(struct {
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}{LootTable: table, LootTableSeed: seed})
	if err != nil {
		panic(err)
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}
}

// TestChunkBlockEntityRoundTrip proves a generated chest block entity (its {LootTable,LootTableSeed}
// NBT) survives ChunkToSave -> ChunkFromSave: the full-metadata serialization (id/x/y/z merge) the
// loader's id/x/y/z decode requires. Before this fix ChunkToSave dropped block_entities entirely,
// so a saved structure chunk lost its chest loot tables / spawners on reload.
func TestChunkBlockEntityRoundTrip(t *testing.T) {
	const (
		chunkX, chunkZ = int32(2), int32(-3)
		localX, localZ = 5, 11
		beY            = 67
		table          = "minecraft:chests/simple_dungeon"
		seed           = int64(0x0123456789ABCDEF)
	)

	c := EmptyChunk(24)
	var be BlockEntity
	if !be.PackXZ(localX, localZ) {
		t.Fatalf("PackXZ(%d,%d) failed", localX, localZ)
	}
	be.Y = int16(beY)
	be.Type = block.EntityTypes["minecraft:chest"]
	be.Data = chunkLootNBT(table, seed)
	c.BlockEntity = []BlockEntity{be}

	var saved save.Chunk
	saved.XPos = chunkX
	saved.ZPos = chunkZ
	saved.YPos = -4 // minY -64 >> 4
	if err := ChunkToSave(c, &saved); err != nil {
		t.Fatalf("ChunkToSave: %v", err)
	}
	if len(saved.BlockEntities) != 1 {
		t.Fatalf("saved block_entities: got %d, want 1", len(saved.BlockEntities))
	}

	// The saved compound must carry the full metadata (id/x/y/z) AND the original loot fields.
	var full struct {
		ID            string `nbt:"id"`
		X             int32  `nbt:"x"`
		Y             int32  `nbt:"y"`
		Z             int32  `nbt:"z"`
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}
	if err := saved.BlockEntities[0].Unmarshal(&full); err != nil {
		t.Fatalf("unmarshal saved BE: %v", err)
	}
	wantX := int(chunkX)<<4 + localX
	wantZ := int(chunkZ)<<4 + localZ
	if full.ID != "minecraft:chest" {
		t.Errorf("id: got %q, want minecraft:chest", full.ID)
	}
	if int(full.X) != wantX || int(full.Y) != beY || int(full.Z) != wantZ {
		t.Errorf("coords: got (%d,%d,%d), want (%d,%d,%d)", full.X, full.Y, full.Z, wantX, beY, wantZ)
	}
	if full.LootTable != table {
		t.Errorf("LootTable: got %q, want %q", full.LootTable, table)
	}
	if full.LootTableSeed != seed {
		t.Errorf("LootTableSeed: got %#x, want %#x", full.LootTableSeed, seed)
	}

	// Full round-trip back to a level.Chunk: Type, Y, and local XZ must reconstruct exactly.
	loaded, err := ChunkFromSave(&saved)
	if err != nil {
		t.Fatalf("ChunkFromSave: %v", err)
	}
	if len(loaded.BlockEntity) != 1 {
		t.Fatalf("loaded BlockEntity: got %d, want 1", len(loaded.BlockEntity))
	}
	lbe := loaded.BlockEntity[0]
	gx, gz := lbe.UnpackXZ()
	if gx != localX || gz != localZ {
		t.Errorf("loaded local XZ: got (%d,%d), want (%d,%d)", gx, gz, localX, localZ)
	}
	if int(lbe.Y) != beY {
		t.Errorf("loaded Y: got %d, want %d", lbe.Y, beY)
	}
	if lbe.Type != block.EntityTypes["minecraft:chest"] {
		t.Errorf("loaded Type: got %d, want chest", lbe.Type)
	}
	var got struct {
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}
	if err := lbe.Data.Unmarshal(&got); err != nil {
		t.Fatalf("unmarshal loaded BE data: %v", err)
	}
	if got.LootTable != table || got.LootTableSeed != seed {
		t.Errorf("loaded loot: got (%q,%#x), want (%q,%#x)", got.LootTable, got.LootTableSeed, table, seed)
	}
}
