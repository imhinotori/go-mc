package server

// chest_loot_test.go — Task 3: the lazy unpackLootTable seam. A chest block entity that
// carries {LootTable, LootTableSeed} rolls its loot ONCE on first open via the shared
// level/loot.Roll evaluator, then CLEARS the table so a re-open does not re-roll (vanilla
// RandomizableContainerBlockEntity.unpackLootTable). A chest with no LootTable opens empty.
//
// Source: javap RandomizableContainer.unpackLootTable (LootTable.fill(container, params,
// lootTableSeed) then setLootTable(null)).

import (
	"testing"

	"github.com/imhinotori/sulfur/level/loot"
)

// TestChestLazyRoll: a chest BE with a LootTable + LootTableSeed fills via loot.Roll and
// clears the table; a second unpack is a no-op (no re-roll).
func TestChestLazyRoll(t *testing.T) {
	c := &chestLoot{LootTable: "minecraft:chests/simple_dungeon", LootTableSeed: 123456789}

	rolled := c.unpackLootTable()
	if !rolled {
		t.Fatal("unpackLootTable returned false for a chest carrying a LootTable")
	}
	if len(c.items) == 0 {
		t.Fatal("chest filled with 0 items — simple_dungeon@123456789 must roll a non-empty list")
	}
	if c.LootTable != "" {
		t.Fatalf("LootTable not cleared after roll (= %q) — a re-open would re-roll", c.LootTable)
	}

	// Snapshot the rolled contents, then unpack again: must be a no-op (same items, no re-roll).
	first := append([][2]int(nil), itemPairs(c)...)
	if c.unpackLootTable() {
		t.Fatal("second unpackLootTable returned true — the table was not cleared (re-roll bug)")
	}
	second := itemPairs(c)
	if len(first) != len(second) {
		t.Fatalf("re-open changed the item count: %d -> %d (re-roll)", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("re-open changed slot %d: %v -> %v (re-roll)", i, first[i], second[i])
		}
	}
}

// TestChestLazyRollMatchesGolden: the rolled contents match the level/loot golden for
// simple_dungeon@123456789 (ties unpackLootTable to 20-01's seed-reproduction proof).
func TestChestLazyRollMatchesGolden(t *testing.T) {
	const table = "minecraft:chests/simple_dungeon"
	const seed = int64(123456789)

	c := &chestLoot{LootTable: table, LootTableSeed: seed}
	c.unpackLootTable()

	// The independent oracle: Roll the same table+seed directly through the evaluator.
	tbl, err := loot.LoadTable(table)
	if err != nil {
		t.Fatalf("LoadTable: %v", err)
	}
	want := loot.Roll(tbl, seed, loot.NewLootContext(seed, 0))

	if len(c.items) != len(want) {
		t.Fatalf("unpack rolled %d stacks, want %d (golden)", len(c.items), len(want))
	}
	for i := range want {
		if c.items[i].ItemID != want[i].ItemID || c.items[i].Count != want[i].Count {
			t.Fatalf("slot %d = (item %d x%d), want (item %d x%d)",
				i, c.items[i].ItemID, c.items[i].Count, want[i].ItemID, want[i].Count)
		}
	}
}

// TestChestNoTableOpensEmpty: a chest BE with no LootTable (a plain/older chest) unpacks
// to nothing — no panic, no items (backward tolerant, T-20-04 garbled/absent table).
func TestChestNoTableOpensEmpty(t *testing.T) {
	c := &chestLoot{} // no LootTable
	if c.unpackLootTable() {
		t.Fatal("unpackLootTable returned true for a chest with no LootTable")
	}
	if len(c.items) != 0 {
		t.Fatalf("a no-table chest filled %d items, want 0", len(c.items))
	}

	// A garbled (non-existent) table id must also open empty, never panic.
	bad := &chestLoot{LootTable: "minecraft:chests/does_not_exist", LootTableSeed: 1}
	if bad.unpackLootTable() {
		t.Fatal("unpackLootTable returned true for a non-existent table (should tolerate + open empty)")
	}
	if len(bad.items) != 0 {
		t.Fatalf("a garbled-table chest filled %d items, want 0", len(bad.items))
	}
	if bad.LootTable != "" {
		t.Fatal("a garbled-table chest did not clear its LootTable (would retry forever)")
	}
}

// itemPairs flattens a chest's rolled items to (itemID, count) pairs for comparison.
func itemPairs(c *chestLoot) [][2]int {
	out := make([][2]int, 0, len(c.items))
	for _, s := range c.items {
		out = append(out, [2]int{int(s.ItemID), int(s.Count)})
	}
	return out
}
