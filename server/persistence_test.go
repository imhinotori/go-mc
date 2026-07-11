package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save"

	"github.com/google/uuid"
)

// persistence_test.go covers ENT-06: the Anvil persistence round-trip. Player state persists
// via save.PlayerData -> world/playerdata/<uuid>.dat (gzip NBT, the vanilla path); entity
// state persists via save.Entities -> a parallel entities/r.x.z.mca region through save/region
// (ReadSector/WriteSector) — REUSING the fork's Anvil IO, NOT a new format. The save path takes
// an immutable VALUE snapshot on the owner (TICK-05 / T-6-15) so the off-tick IO never reads a
// live tick-owned player; a missing/corrupt .dat falls back to spawn defaults (no crash,
// T-6-16). Disk is NBT (save.Item.Tag) — NEVER the wire component codec (Pitfall 6).

// TestPlayerDataRoundTrip asserts savePlayer writes a snapshot to a .dat and loadPlayer reads
// it back recovering pos/health/food/saturation/held-slot.
func TestPlayerDataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()

	want := save.PlayerData{
		Dimension:           overworldDimensionName,
		Pos:                 [3]float64{12.5, 70.0, -33.25},
		Health:              17.5,
		FoodLevel:           18,
		FoodSaturationLevel: 4.5,
		SelectedItemSlot:    3,
	}

	if err := savePlayer(dir, id, want); err != nil {
		t.Fatalf("savePlayer: %v", err)
	}

	got, ok := loadPlayer(dir, id)
	if !ok {
		t.Fatal("loadPlayer reported miss for a just-saved player")
	}
	if got.Pos != want.Pos {
		t.Fatalf("Pos = %v, want %v", got.Pos, want.Pos)
	}
	if got.Health != want.Health {
		t.Fatalf("Health = %v, want %v", got.Health, want.Health)
	}
	if got.FoodLevel != want.FoodLevel {
		t.Fatalf("FoodLevel = %v, want %v", got.FoodLevel, want.FoodLevel)
	}
	if got.FoodSaturationLevel != want.FoodSaturationLevel {
		t.Fatalf("FoodSaturationLevel = %v, want %v", got.FoodSaturationLevel, want.FoodSaturationLevel)
	}
	if got.SelectedItemSlot != want.SelectedItemSlot {
		t.Fatalf("SelectedItemSlot = %v, want %v", got.SelectedItemSlot, want.SelectedItemSlot)
	}
}

// TestPlayerInventoryRoundTrip asserts a snapshot's inventory (disk NBT save.Item, NOT the wire
// component codec) round-trips through the .dat — proving Pitfall 6 separation is honored.
func TestPlayerInventoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()

	want := save.PlayerData{
		Dimension: overworldDimensionName,
		Health:    maxHealth,
		Inventory: []save.ItemStackWithSlotDisk{
			{Slot: 0, ItemStackDisk: save.ItemStackDisk{ID: "minecraft:stone", Count: 64}},
			{Slot: 8, ItemStackDisk: save.ItemStackDisk{ID: "minecraft:diamond_sword", Count: 1}},
		},
	}
	if err := savePlayer(dir, id, want); err != nil {
		t.Fatalf("savePlayer: %v", err)
	}
	got, ok := loadPlayer(dir, id)
	if !ok {
		t.Fatal("loadPlayer miss")
	}
	if len(got.Inventory) != 2 {
		t.Fatalf("Inventory len = %d, want 2", len(got.Inventory))
	}
	if got.Inventory[0].ID != "minecraft:stone" || got.Inventory[0].Count != 64 {
		t.Fatalf("Inventory[0] = %+v, want stone x64", got.Inventory[0])
	}
	if got.Inventory[1].ID != "minecraft:diamond_sword" || got.Inventory[1].Slot != 8 {
		t.Fatalf("Inventory[1] = %+v, want diamond_sword at slot 8", got.Inventory[1])
	}
}

// TestLoadMissingDefaults asserts loadPlayer on an ABSENT .dat returns spawn defaults (full
// health, ok=false) and never errors/crashes — the first-join path (T-6-16).
func TestLoadMissingDefaults(t *testing.T) {
	dir := t.TempDir()

	got, ok := loadPlayer(dir, uuid.New())
	if ok {
		t.Fatal("loadPlayer must report a MISS (ok=false) for an absent .dat")
	}
	if got.Health != maxHealth {
		t.Fatalf("missing-defaults Health = %v, want full %v", got.Health, maxHealth)
	}
	if got.FoodLevel != maxFood {
		t.Fatalf("missing-defaults FoodLevel = %v, want %v", got.FoodLevel, maxFood)
	}

	// A corrupt .dat must ALSO fall back to defaults, not crash.
	id := uuid.New()
	if err := os.MkdirAll(filepath.Join(dir, "playerdata"), 0o755); err != nil {
		t.Fatalf("mkdir playerdata: %v", err)
	}
	bad := filepath.Join(dir, "playerdata", id.String()+".dat")
	if err := os.WriteFile(bad, []byte("not gzip not nbt"), 0o644); err != nil {
		t.Fatalf("seed corrupt .dat: %v", err)
	}
	got2, ok2 := loadPlayer(dir, id)
	if ok2 {
		t.Fatal("loadPlayer must report a MISS for a corrupt .dat")
	}
	if got2.Health != maxHealth {
		t.Fatalf("corrupt-defaults Health = %v, want full %v", got2.Health, maxHealth)
	}
}

// TestEntityRegionRoundTrip asserts saveEntities writes an entity snapshot to
// entities/r.0.0.mca via save/region and loadEntities reads it back, recovering the entity —
// reusing save.Entities + region, not a new format.
func TestEntityRegionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pos := level.ChunkPos{0, 0}

	ents := []save.Entities{
		{
			Pos:      [3]float64{8.5, 64.0, 8.5},
			Motion:   [3]float64{0, 0, 0},
			Rotation: [2]float32{90, 0},
			UUID:     [4]int32{1, 2, 3, 4},
		},
		{
			Pos:        [3]float64{100.25, 65.0, -7.5},
			CustomName: "Bob",
			UUID:       [4]int32{5, 6, 7, 8},
		},
	}

	if err := saveEntities(dir, pos, ents); err != nil {
		t.Fatalf("saveEntities: %v", err)
	}

	got, ok, err := loadEntities(dir, pos)
	if err != nil {
		t.Fatalf("loadEntities error: %v", err)
	}
	if !ok {
		t.Fatal("loadEntities reported a miss for a just-saved region cell")
	}
	if len(got) != 2 {
		t.Fatalf("recovered %d entities, want 2", len(got))
	}
	if got[0].Pos != ents[0].Pos {
		t.Fatalf("entity[0] Pos = %v, want %v", got[0].Pos, ents[0].Pos)
	}
	if got[1].CustomName != "Bob" {
		t.Fatalf("entity[1] CustomName = %q, want Bob", got[1].CustomName)
	}
	if got[1].Pos != ents[1].Pos {
		t.Fatalf("entity[1] Pos = %v, want %v", got[1].Pos, ents[1].Pos)
	}

	// A region cell that was never written is a clean miss (no error, no crash).
	_, ok2, err2 := loadEntities(dir, level.ChunkPos{5, 5})
	if err2 != nil {
		t.Fatalf("loadEntities of an unwritten cell errored: %v", err2)
	}
	if ok2 {
		t.Fatal("loadEntities of an unwritten cell must report a miss")
	}
}

// TestSnapshotIsValueCopy asserts the save path takes an IMMUTABLE value snapshot on the owner
// (TICK-05 / T-6-15): mutating the live player AFTER the snapshot does not change what was
// captured, so the off-tick IO sees the snapshot, not the live tick-owned state.
func TestSnapshotIsValueCopy(t *testing.T) {
	p := &tickPlayer{
		x: 1, y: 2, z: 3,
		yaw: 45, pitch: 10,
		health:     maxHealth,
		food:       maxFood,
		saturation: defaultSaturation,
	}
	ensureInventory(p)

	snap := snapshotPlayer(p)

	// Mutate the LIVE player after snapshotting (the tick keeps running while IO happens).
	p.x, p.y, p.z = 999, 888, 777
	p.health = 1
	p.food = 0
	ensureInventory(p).heldSlot = 7

	if snap.Pos != [3]float64{1, 2, 3} {
		t.Fatalf("snapshot Pos = %v, want the value at snapshot time {1,2,3}", snap.Pos)
	}
	if snap.Health != maxHealth {
		t.Fatalf("snapshot Health = %v, want the value at snapshot time %v", snap.Health, maxHealth)
	}
	if snap.FoodLevel != maxFood {
		t.Fatalf("snapshot FoodLevel = %v, want %v", snap.FoodLevel, maxFood)
	}
}
