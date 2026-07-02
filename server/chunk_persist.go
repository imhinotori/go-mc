package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/world"
)

// chunk_persist.go — SUB-PERSIST: the TICK-side half of chunk persistence (the off-tick half is
// world.ChunkSaver/RunChunkSaveLoop). It serializes dirty/unloaded chunks ON the owner goroutine
// (a pure read over tick-owned state) into IMMUTABLE region bytes and hands them off-tick — the
// SAME snapshot-on-owner/IO-off-tick discipline the player .dat path (persistence.go) uses
// (TICK-05 / T-6-15). No live tick-owned pointer (no *level.Chunk, no chestLoot) ever crosses the
// goroutine boundary — only the finished bytes do — so the save IO is race-free by construction.
//
// CHEST FLUSH (the previously-blocked HANDOFF item): before serializing a column, any rolled
// openChests in that column have their items folded INTO the chunk's BlockEntity list as the disk
// "Items" NBT, ported from ChestBlockEntity.saveAdditional (trySaveLootTable XOR saveAllItems):
//   - an UN-rolled chest (LootTable still set) keeps its {LootTable,LootTableSeed} BE (trySaveLootTable
//     returns true → no Items written);
//   - a ROLLED chest (LootTable cleared by unpackLootTable) writes its container as the "Items" list
//     (trySaveLootTable returns false → saveAllItems).
// CITE: ChestBlockEntity.saveAdditional, RandomizableContainer.trySaveLootTable (26.2-inner.jar).

// chunkSaveIntervalTicks is the periodic chunk-flush cadence (SUB-PERSIST). At 20 TPS this is a
// save pass every ~5s, so a stream of edits to one chunk coalesces into a single serialize+write
// per interval instead of one per edit. On-unload flushes are immediate (flushColumnNow), bypassing
// this cadence so a chunk is never unloaded with un-saved edits.
const chunkSaveIntervalTicks = 100

// SetChunkSaver wires the off-tick chunk-save consumer (SUB-PERSIST). main() calls it before Run
// with a ChunkSaver targeting worldDir/region. A nil/disabled saver makes the save phase a cheap
// no-op (tests/ephemeral runs need no disk). Set-once at setup; read only on the tick goroutine.
func (t *TickLoop) SetChunkSaver(s *world.ChunkSaver) { t.chunkSaver = s }

// tickChunkSave is the periodic chunk-save phase (SUB-PERSIST), called from the world phase inside
// the fixed tick order (no new phase added — it lives inside an existing phase like the other
// gameplay seams). Every chunkSaveIntervalTicks it drains the manager's dirty set and flushes each
// dirty column. A nil/disabled saver or nil world makes it a cheap no-op. Runs on the owner.
func (t *TickLoop) tickChunkSave() {
	if t.world() == nil || !t.chunkSaver.Enabled() {
		return // no world wired or persistence off: nothing to save
	}
	t.chunkSaveTickCounter++
	if t.chunkSaveTickCounter < chunkSaveIntervalTicks {
		return
	}
	t.chunkSaveTickCounter = 0

	dirty := t.world().DrainDirty()
	for _, pos := range dirty {
		if !t.flushColumn(pos) {
			// Queue full: re-mark dirty so the next pass retries (a dropped save is deferred,
			// never lost). MarkDirty is a no-op for a no-longer-Ready column (already unloaded).
			t.world().MarkDirty(pos)
		}
	}
}

// flushColumnNow flushes a single column IMMEDIATELY (the on-unload path), bypassing the periodic
// cadence so a chunk about to be unloaded is never dropped with un-saved edits. Returns true if the
// snapshot was enqueued (or there was nothing to save). It also clears the column's dirty flag (the
// flush supersedes any pending periodic save). Runs on the owner before Remove.
func (t *TickLoop) flushColumnNow(pos level.ChunkPos) bool {
	if t.world() == nil || !t.chunkSaver.Enabled() {
		return true
	}
	return t.flushColumn(pos)
}

// flushColumn serializes the column at pos (folding in any rolled openChests) and enqueues the
// immutable bytes to the off-tick save loop. Returns false ONLY when the save queue is full (the
// caller re-marks dirty). A column that is not Ready (already unloaded) or a serialize error is a
// no-op-true (nothing more this tick can do). The serialization is a pure READ over tick-owned
// chunk state on the owner goroutine.
func (t *TickLoop) flushColumn(pos level.ChunkPos) bool {
	ch, ok := t.world().Get(pos)
	if !ok {
		return true // not Ready (unloaded / not yet generated): nothing to serialize
	}

	// CHEST FLUSH: fold any rolled openChests in this column into the chunk's BlockEntity list
	// (ChestBlockEntity.saveAdditional). This mutates a SNAPSHOT decision only — flushChestItems
	// rewrites the matching BE.Data in the live chunk's BlockEntity slice on the OWNER (tick-owned,
	// safe), so the subsequent serialize captures the rolled items. Done before SerializeChunkData.
	t.flushChestItems(pos, ch)

	// FURNACE FLUSH: fold any live furnaceBE in this column into the chunk's BlockEntity list
	// (AbstractFurnaceBlockEntity.saveAdditional — Items + cook shorts + RecipesUsed). Same owner-side
	// mutation of the live chunk's BE slice as flushChestItems, before SerializeChunkData.
	t.flushFurnaceItems(pos, ch)

	// BREWING-STAND FLUSH: fold any live brewingStandBE in this column into the chunk's BlockEntity list
	// (BrewingStandBlockEntity.saveAdditional — Items + BrewTime + Fuel). The furnace-flush twin.
	t.flushBrewingStandItems(pos, ch)

	data, err := world.SerializeChunkData(t.worker().StructureCache(), pos, ch, t.worker().MinY())
	if err != nil {
		// A serialize error is an encode bug, not runtime input; skip this column (do not crash the
		// tick). It stays out of the dirty set (DrainDirty already cleared it); a later edit re-dirties.
		udebug("chunksave", "serialize %v: %v", pos, err)
		return true
	}

	return t.chunkSaver.Enqueue(world.ChunkSaveSnapshot{Pos: pos, Data: data})
}

// flushChestItems ports ChestBlockEntity.saveAdditional for every rolled openChest in the column at
// pos: trySaveLootTable XOR saveAllItems. For each tick-owned chestLoot whose LootTable has been
// CLEARED (rolled, unpackLootTable ran), it rewrites that chest's BlockEntity.Data in ch's
// BlockEntity slice to the {Items:[...]} disk compound (the rolled container). An UN-rolled chest
// (LootTable still set) is left as its {LootTable,LootTableSeed} BE (trySaveLootTable true → no
// Items). Runs on the owner over tick-owned state; mutates only the chunk's own BE slice.
//
// CITE: ChestBlockEntity.saveAdditional — trySaveLootTable(out); if !that saveAllItems(out, items).
func (t *TickLoop) flushChestItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.openChests == nil {
		return
	}
	baseX, baseZ := int(pos[0])<<4, int(pos[1])<<4
	for cpos, cl := range t.openChests {
		// Only chests inside THIS column.
		if cpos.X>>4 != int(pos[0]) || cpos.Z>>4 != int(pos[1]) {
			continue
		}
		// trySaveLootTable: an un-rolled chest (LootTable still set) keeps its loot-table BE — the
		// items have not been generated yet, so there is nothing to flush (true → no saveAllItems).
		if cl.LootTable != "" {
			continue
		}
		// Rolled chest: write the container as the "Items" disk NBT (saveAllItems). Translate the
		// tick-owned []component.SlotData into []save.DiskItem (numeric wire id → "minecraft:<name>"
		// string id; flag component-bearing stacks so Phase A drops are metered).
		items := chestItemsToDisk(cl.items)
		data, dropped, err := save.SaveItemsCompound(items, false)
		if err != nil {
			udebug("chunksave", "chest items %v: %v", cpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "chest %v: %d component-bearing stacks persisted without components (Phase A)", cpos, dropped)
		}
		lx, lz := cpos.X&15, cpos.Z&15
		setChestBEData(ch, lx, cpos.Y, lz, baseX, baseZ, data)
	}
}

// chestItemsToDisk translates a chest's tick-owned []component.SlotData container into the
// []save.DiskItem the disk codec consumes: each non-empty slot's numeric wire item id is resolved
// to its "minecraft:<name>" string id (itemName, the SAME registry helper inventoryToItems uses),
// and a stack carrying wire components (RawComponents non-empty) is flagged so SaveItemsCompound
// meters the Phase-A component drop. Empty (Count<=0) slots stay empty (DiskItem zero value), so
// the slot indexing matches (saveAllItems skips empties by index).
func chestItemsToDisk(slots []component.SlotData) []save.DiskItem {
	out := make([]save.DiskItem, len(slots))
	for i, s := range slots {
		if s.Count <= 0 {
			continue // empty slot: zero DiskItem (skipped on save by index)
		}
		out[i] = save.DiskItem{
			ID:            itemName(int32(s.ItemID)),
			Count:         int32(s.Count),
			HasComponents: len(s.RawComponents) > 0, // Phase A: components dropped, metered
		}
	}
	return out
}

// setChestBEData rewrites the chest BlockEntity's Data compound at the given LOCAL (lx,y,lz) cell in
// ch to the supplied {Items:[...]} payload, preserving the BE's type/coords. If no chest BE exists
// at that cell (a placed-but-unrecorded chest), it appends a new one. The BE list is the chunk's
// own tick-owned slice (mutated on the owner). baseX/baseZ are unused for the LOCAL packing but kept
// in the signature for clarity at the call site (the BE stores local XZ + absolute-section Y).
func setChestBEData(ch *level.Chunk, lx, y, lz, baseX, baseZ int, data nbt.RawMessage) {
	_ = baseX
	_ = baseZ
	chestType := block.EntityTypes["minecraft:chest"]
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && be.Type == chestType {
			be.Data = data // rewrite in place: the rolled items replace the {LootTable,...} compound
			return
		}
	}
	// No recorded chest BE at this cell: append a fresh one carrying the items (a placed chest whose
	// BE was not in the gen list). PackXZ encodes the in-chunk local coords (0..15).
	var be level.BlockEntity
	be.PackXZ(lx, lz)
	be.Y = int16(y)
	be.Type = chestType
	be.Data = data
	ch.BlockEntity = append(ch.BlockEntity, be)
}

// markChestDirty flags the column owning a chest position as dirty so its rolled items are flushed
// on the next save pass (SUB-PERSIST). The chest-click path calls it after mutating a chest's
// container so an item move/roll is persisted. A no-op when persistence is off or the column is not
// Ready. Runs on the owner (the chest path is tick-owned).
func (t *TickLoop) markChestDirty(chestPos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(chestPos.X >> 4), int32(chestPos.Z >> 4)})
}
