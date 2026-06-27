package structure

// chest_be_test.go — Task 2 RED-then-GREEN: createChest draws rng.NextLong() ONCE per
// chest and emits a chest BlockEntity whose NBT carries {LootTable, LootTableSeed} —
// the lazy-roll keystone (Pitfall 2: NO item list at gen, only table + seed). The seed
// is the PIECE-RNG nextLong() so chest contents are deterministic per (worldseed,chunk).
//
// Source: javap StructurePiece.createChest -> setLootTable(key, random.nextLong());
// RandomizableContainerBlockEntity NBT keys LootTable / LootTableSeed.

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// recordingView is a mapView that ALSO captures SetBlockEntity calls, so a test can
// assert the chest BE (type + NBT) createChest emits.
type recordingView struct {
	*mapView
	bes []capturedBE
}

type capturedBE struct {
	wx, wy, wz int
	typ        block.EntityType
	lootTable  string
	lootSeed   int64
}

func newRecordingView() *recordingView {
	return &recordingView{mapView: newMapView()}
}

func (r *recordingView) SetBlockEntity(wx, wy, wz int, typ block.EntityType, lootTable string, lootSeed int64) {
	r.bes = append(r.bes, capturedBE{wx, wy, wz, typ, lootTable, lootSeed})
}

// TestCreateChestEmitsBE: createChest places the chest block AND emits a chest
// BlockEntity carrying LootTable (the id) + LootTableSeed (the rng.NextLong() draw),
// with NO item list (lazy roll). The BE type is minecraft:chest.
func TestCreateChestEmitsBE(t *testing.T) {
	view := newRecordingView()
	box := BoundingBox{MinX: 0, MinY: 0, MinZ: 0, MaxX: 31, MaxY: 64, MaxZ: 31}
	p := newTestPiece(box)

	rng := levelgen.NewLegacyRandomSource(123456789)
	// Peek the expected seed: a fresh source at the same start state draws the same long.
	expectSeed := levelgen.NewLegacyRandomSource(123456789).NextLong()

	const lootTable = "minecraft:chests/simple_dungeon"
	if !p.createChest(view, box, rng, 5, 10, 5, lootTable, nil) {
		t.Fatal("createChest returned false for an in-box position")
	}

	if len(view.bes) != 1 {
		t.Fatalf("createChest emitted %d block entities, want exactly 1", len(view.bes))
	}
	be := view.bes[0]
	if be.typ != block.EntityTypes["minecraft:chest"] {
		t.Fatalf("chest BE type = %d, want chest (%d)", be.typ, block.EntityTypes["minecraft:chest"])
	}
	if be.lootTable != lootTable {
		t.Fatalf("chest BE LootTable = %q, want %q", be.lootTable, lootTable)
	}
	if be.lootSeed != expectSeed {
		t.Fatalf("chest BE LootTableSeed = %d, want rng.NextLong() %d (deterministic per piece RNG)", be.lootSeed, expectSeed)
	}
	if be.wx != 5 || be.wy != 10 || be.wz != 5 {
		t.Fatalf("chest BE at (%d,%d,%d), want (5,10,5)", be.wx, be.wy, be.wz)
	}

	// The chest BLOCK must also be placed (the visible chest).
	if block.IsAir(view.GetBlock(5, 10, 5)) {
		t.Fatal("createChest left air at the chest position — the chest block was not placed")
	}
}

// TestCreateChestSeedDeterministic: the same piece-RNG state yields the same
// LootTableSeed (deterministic per (worldseed,chunk)).
func TestCreateChestSeedDeterministic(t *testing.T) {
	box := BoundingBox{MinX: 0, MinY: 0, MinZ: 0, MaxX: 31, MaxY: 64, MaxZ: 31}
	seedOf := func() int64 {
		view := newRecordingView()
		p := newTestPiece(box)
		rng := levelgen.NewLegacyRandomSource(777)
		p.createChest(view, box, rng, 5, 10, 5, "minecraft:chests/simple_dungeon", nil)
		return view.bes[0].lootSeed
	}
	if a, b := seedOf(), seedOf(); a != b {
		t.Fatalf("LootTableSeed not deterministic: %d != %d", a, b)
	}
}

// TestCreateChestNoItemsAtGen: createChest emits ONLY table+seed — no rolled item list
// is stored at gen time (Pitfall 2: lazy roll). The captured BE has no items field; this
// asserts the emit carries the table id + seed and nothing rolled.
func TestCreateChestNoItemsAtGen(t *testing.T) {
	view := newRecordingView()
	box := BoundingBox{MinX: 0, MinY: 0, MinZ: 0, MaxX: 31, MaxY: 64, MaxZ: 31}
	p := newTestPiece(box)
	rng := levelgen.NewLegacyRandomSource(1)
	p.createChest(view, box, rng, 1, 1, 1, "minecraft:chests/igloo_chest", nil)
	if len(view.bes) != 1 {
		t.Fatalf("want 1 BE, got %d", len(view.bes))
	}
	if view.bes[0].lootTable == "" {
		t.Fatal("chest BE carries no LootTable — the lazy-roll seam needs the table id stored")
	}
}
