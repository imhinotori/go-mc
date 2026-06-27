package server

// chest_loot.go — the lazy chest-loot roll (STRUCT-POLISH-01 / 20-02 Task 3): the port of
// net.minecraft.world.level.block.entity.RandomizableContainerBlockEntity.unpackLootTable.
// A generated structure chest stores ONLY {LootTable, LootTableSeed} at gen
// (world/structure/piece.go createChest); the items are rolled LAZILY on FIRST open (vanilla
// rolls on the first container access — getItem/setItem/isEmpty all call unpackLootTable),
// then the table is CLEARED so a re-open never re-rolls.
//
// W2 SPLIT NOTE (the plan's Case B fallback ceiling, RESOLVED here): Sulfur has NO
// block-entity chest-OPEN container path today — there is no runtime block-entity resolution
// from a world position, no windowId allocator, no ClientboundOpenScreen usage, and no
// non-player container menu (the ENT-04 InventoryMenu is the PLAYER inventory only). Wiring
// the full open seam (UseItemOn a chest block -> resolve the chest BE -> allocate a windowId
// -> OpenScreen + a chest container menu backed by the BE -> ContainerSetContent slot sync ->
// container-close) is a NET-NEW interaction subsystem far larger than "a menu hookup", so per
// the plan's explicit W2 split instruction it is SPLIT OUT to a follow-up plan. THIS plan
// ships the gen-time store (Task 2) + the unpackLootTable roll seam (here) — the pure,
// tick-side, fully-tested function the future open path calls. The roll runs ON THE TICK (the
// container-open path is tick-owned), so chestLoot is tick-owned state (no off-tick mutation).
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.RandomizableContainer.unpackLootTable(Player):
//       table = getLootTable(); if (table == null || level == null) return;
//       setLootTable(null);                                   // CLEAR (one-shot)
//       LootParams params = builder(ORIGIN=pos).withLuck(player.getLuck()).create(CHEST);
//       lootTable.fill(this, params, lootTableSeed);          // roll into the container
//   - net.minecraft.world.level.storage.loot.LootTable.fill -> getRandomItems(params, seed)
//     then ContainerHelper-style placement into the container slots.

import (
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/loot"
)

// chestLoot is the tick-side runtime state of a loot-bearing chest block entity: the stored
// {LootTable, LootTableSeed} (read from the chest BE NBT at chunk load / set at gen) plus the
// rolled item slots (empty until the first unpackLootTable). It is the minimal carrier the
// future chest-open path resolves from a world position; the lazy roll lives on it.
//
// Tick-owned (TICK-05): unpackLootTable runs on the tick goroutine (the container-open path is
// tick-owned), so the items mutation + the table clear are single-owner race-free.
type chestLoot struct {
	// LootTable is the loot-table id (e.g. "minecraft:chests/simple_dungeon"); "" once rolled
	// (or for a plain chest). The CLEAR-on-roll is the one-shot that prevents a re-roll.
	LootTable string
	// LootTableSeed is the gen-time piece-RNG nextLong() draw — the SERVER-stored seed (never
	// client-supplied, T-20-05) that makes the roll reproduce vanilla per (worldseed, chunk).
	LootTableSeed int64
	// items is the chest container contents — the 27-slot backing of a single chest (vanilla
	// ChestBlockEntity.items is a NonNullList of size 27, the inventory.Generic9x3 container).
	// Empty (count 0) slots are the unfilled cells. Filled lazily by unpackLootTable; the
	// chest-open path (chest_open.go) ensures it is a 27-slot list before serving the menu.
	items []component.SlotData
}

// chestContainerSize is the slot count of a single chest container — ChestBlockEntity holds a
// NonNullList<ItemStack> of size 27 (generic_9x3 = 3 rows × 9). The lazy roll places stacks into
// this fixed list; the chest-open menu serves all 27 slots (empty cells included).
const chestContainerSize = 27

// ensureContainer normalizes items to exactly chestContainerSize slots (empty-padded). A freshly
// loaded chest may have a short/nil items list (the lazy roll appends only the non-empty stacks);
// the open path needs the full 27-slot backing so menu-slot indices 0..26 always resolve. Idempotent.
func (c *chestLoot) ensureContainer() {
	if len(c.items) >= chestContainerSize {
		c.items = c.items[:chestContainerSize]
		return
	}
	padded := make([]component.SlotData, chestContainerSize)
	copy(padded, c.items)
	c.items = padded
}

// unpackLootTable ports RandomizableContainer.unpackLootTable: on the first access of a chest
// that carries a LootTable, roll the table at the stored LootTableSeed through the shared
// level/loot evaluator, place the rolled stacks into the container, and CLEAR the table (so a
// re-open is a no-op — the one-shot). Returns true iff a roll happened.
//
// Robustness (T-20-04 Tampering): a chest with no LootTable, or a garbled/non-existent table
// id (a corrupt save), opens EMPTY and clears the table — never trust-and-crash, never retry
// forever. The seed is the SERVER-stored LootTableSeed; a client cannot influence the contents
// (T-20-05 Elevation — no client-supplied seed).
//
// The LootContext carries luck 0 (the vanilla chest default — LootParams.getLuck() is the
// opening player's luck, 0 without a luck potion; v1 has no luck system, so 0 is the faithful
// default, structured to read the player's real luck when that lands). Source: javap
// RandomizableContainer.unpackLootTable.
func (c *chestLoot) unpackLootTable() bool {
	if c.LootTable == "" {
		return false // plain chest (no loot table) — nothing to roll
	}
	// setLootTable(null): CLEAR first (one-shot). Vanilla clears before fill; clearing up front
	// also guarantees a garbled-table chest does not retry on every access.
	table := c.LootTable
	c.LootTable = ""

	tbl, err := loot.LoadTable(table)
	if err != nil {
		// Garbled/absent table id (corrupt save / removed table): open EMPTY, never panic
		// (T-20-04). The table is already cleared, so this is a one-shot empty.
		return false
	}
	// LootTable.fill(this, params, lootTableSeed): roll at the SERVER-stored seed; luck 0.
	c.items = loot.Roll(tbl, c.LootTableSeed, loot.NewLootContext(c.LootTableSeed, 0))
	return true
}
