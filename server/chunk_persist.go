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
func (t *TickLoop) SetChunkSaver(s *world.ChunkSaver) {
	t.chunkSaver = s
}

// tickChunkSave is the periodic chunk-save phase (SUB-PERSIST), called from the world phase inside
// the fixed tick order (no new phase added — it lives inside an existing phase like the other
// gameplay seams). Every chunkSaveIntervalTicks it drains the manager's dirty set and flushes each
// dirty chunk column, then independently autosaves entities for every remaining Ready column. The
// latter mirrors PersistentEntitySectionManager.autoSave/getAllChunksToSave: entity state is saved
// for every loaded column, not only when block/chunk data happened to become dirty. Runs on owner.
func (t *TickLoop) tickChunkSave() {
	chunkSaverEnabled := t.chunkSaver != nil && t.chunkSaver.Enabled()
	if t.world() == nil || (!chunkSaverEnabled && t.persistDir == "") {
		return // no world wired or both persistence paths are off: nothing to save
	}
	t.chunkSaveTickCounter++
	if t.chunkSaveTickCounter < chunkSaveIntervalTicks {
		return
	}
	t.chunkSaveTickCounter = 0

	// Chunk data retains its dirty-only coalescing path. Entity persistence is deliberately not
	// coupled to this set: arbitrary live entity fields mutate without touching ChunkManager.
	flushedEntities := make(map[level.ChunkPos]struct{})
	if chunkSaverEnabled {
		dirty := t.world().DrainDirty()
		for _, pos := range dirty {
			if !t.flushColumn(pos) {
				// Queue full: re-mark dirty so the next pass retries (a dropped save is deferred,
				// never lost). MarkDirty is a no-op for a no-longer-Ready column (already unloaded).
				t.world().MarkDirty(pos)
			}
			// flushColumn snapshots entities before attempting the chunk queue, including on a
			// full queue, so the independent pass below need not write this column twice.
			flushedEntities[pos] = struct{}{}
		}
	}

	if t.persistDir != "" {
		// CITE 26.2 PersistentEntitySectionManager.autoSave -> getAllChunksToSave ->
		// storeChunkSections. Loaded columns are visited even when their entity list is empty;
		// the empty store is what clears a previously non-empty EntityStorage cell.
		t.world().ForEachReady(func(pos level.ChunkPos, _ *level.Chunk) {
			if _, already := flushedEntities[pos]; !already {
				t.flushColumnEntities(pos)
			}
		})
	}
}

// flushColumnNow flushes a single column IMMEDIATELY (the on-unload path), bypassing the periodic
// cadence so a chunk about to be unloaded is never dropped with un-saved edits. Returns true if the
// snapshot was enqueued (or there was nothing to save). It also clears the column's dirty flag (the
// flush supersedes any pending periodic save). Runs on the owner before Remove.
func (t *TickLoop) flushColumnNow(pos level.ChunkPos) bool {
	if t.world() == nil {
		return true
	}
	if t.chunkSaver != nil && t.chunkSaver.Enabled() {
		return t.flushColumn(pos)
	}
	if t.persistDir != "" {
		t.flushColumnEntities(pos)
	}
	return true
}

// flushColumn serializes the column at pos (folding in any rolled openChests) and enqueues the
// immutable bytes to the off-tick save loop. Returns false ONLY when the save queue is full (the
// caller re-marks dirty). A column that is not Ready (already unloaded) or a serialize error is a
// no-op-true (nothing more this tick can do). The serialization is a pure READ over tick-owned
// chunk state on the owner goroutine.
func (t *TickLoop) flushColumn(pos level.ChunkPos) bool {
	return t.flushColumnWithMode(pos, false)
}

func (t *TickLoop) flushColumnWithMode(pos level.ChunkPos, durable bool) bool {
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

	// DISPENSER/DROPPER FLUSH: fold any live dispenserBE in this column into the chunk's BlockEntity list
	// (DispenserBlockEntity.saveAdditional — the 9-slot Items list). The furnace-flush twin (REDSTONE TIER-4).
	t.flushDispenserItems(pos, ch)

	// HOPPER FLUSH: fold any live hopperBE in this column into the chunk's BlockEntity list
	// (HopperBlockEntity.saveAdditional — the 5-slot Items list + TransferCooldown). The dispenser-flush twin.
	t.flushHopperItems(pos, ch)

	// CRAFTER FLUSH: fold any live crafterBE in this column into the chunk BlockEntity list
	// (CrafterBlockEntity.saveAdditional -- Items + crafting_ticks_remaining + disabled_slots). The
	// dispenser-flush twin.
	t.flushCrafterItems(pos, ch)

	// CHISELED-BOOKSHELF FLUSH: fold any live chiseledBookshelfBE in this column into the chunk BlockEntity
	// list (ChiseledBookShelfBlockEntity.saveAdditional -- the 6-slot Items list + last_interacted_slot).
	// The dispenser-flush twin.
	t.flushChiseledBookshelfItems(pos, ch)

	// SHULKER-BOX FLUSH: fold any live shulkerBE in this column into the chunk BlockEntity list
	// (ShulkerBoxBlockEntity.saveAdditional -- the 27-slot Items list). The dispenser-flush twin.
	t.flushShulkerItems(pos, ch)

	// ENTITY FLUSH (SUB-PERSIST, Part C): snapshot the tick-owned entities in this column on the OWNER
	// and write them to the parallel entities/r.x.z.mca region (the modern EntityStorage path). The
	// snapshot is an IMMUTABLE []save.Entities value; only that crosses into the disk IO (no live
	// *Entity), so it is race-free by the same discipline as the chunk bytes. A column with no
	// persistable entities writes an empty cell (a later reload reads zero entities and respawns none). The
	// write is synchronous owner-side (the per-column entity set is small, like the raid/POI/level.dat
	// flushes). CITE EntityStorage.storeEntities.
	if t.persistDir != "" {
		t.flushColumnEntities(pos)
	}

	// SCHEDULED-TICK FLUSH: pack this column pending block + fluid ticks so a repeater mid-delay
	// or water mid-spread survives the unload/reload (SerializableChunkData block_ticks/fluid_ticks).
	// packChunkBlockTicks pulls the LevelChunkTicks container; packChunkFluidTicks filters the flat
	// fluid schedule to this chunk. Both pack the delay relative to the current game-time.
	blockTicks := t.packChunkBlockTicks(pos)
	fluidTicks := t.packChunkFluidTicks(pos, t.gametime)

	data, err := world.SerializeChunkData(t.worker().StructureCache(), pos, ch, t.worker().MinY(), blockTicks, fluidTicks)
	if err != nil {
		// A serialize error is an encode bug, not runtime input; skip this column (do not crash the
		// tick). It stays out of the dirty set (DrainDirty already cleared it); a later edit re-dirties.
		udebug("chunksave", "serialize %v: %v", pos, err)
		return true
	}

	snap := world.ChunkSaveSnapshot{Pos: pos, Data: data}
	if durable {
		t.chunkSaver.EnqueueDurable(snap)
		return true
	}
	return t.chunkSaver.Enqueue(snap)
}

// flushAllLoadedChunksForShutdown mirrors MinecraftServer.stopServer -> saveAllChunks(flush=true):
// snapshot every ready loaded chunk on the owner before allowing the off-tick IO loop to close.
func (t *TickLoop) flushAllLoadedChunksForShutdown() {
	if t.world() == nil {
		return
	}
	if t.chunkSaver != nil && t.chunkSaver.Enabled() {
		t.world().DrainDirty()
		t.world().ForEachReady(func(pos level.ChunkPos, _ *level.Chunk) {
			t.flushColumnWithMode(pos, true)
		})
		return
	}
	// EntityStorage is independent of chunk-region persistence. The default server may have the
	// .mca saver disabled while persistDir/entities is active; still snapshot every loaded column,
	// including empty cells that erase an older non-empty entity sector.
	if t.persistDir != "" {
		t.world().ForEachReady(func(pos level.ChunkPos, _ *level.Chunk) {
			t.flushColumnEntities(pos)
		})
	}
}

// flushColumnEntities snapshots and stores the persistable entities in pos, including an EMPTY
// list. The empty write is required: 26.2 PersistentEntitySectionManager.storeChunkSections calls
// EntityStorage.storeEntities with ChunkEntities(pos, []) for a loaded column that has become empty;
// EntityStorage then stores STORE_EMPTY, preventing an older non-empty sector from resurrecting.
// Our region backend represents the same observable state with an explicit empty Entities list.
func (t *TickLoop) flushColumnEntities(pos level.ChunkPos) {
	if err := saveEntities(t.persistDir, pos, t.snapshotColumnEntities(pos)); err != nil {
		udebug("chunksave", "entity save %v: %v", pos, err)
	}
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
			ID:               itemName(int32(s.ItemID)),
			Count:            int32(s.Count),
			HasComponents:    len(s.RawComponents) > 0,
			WireComponents:   s.RawComponents, // Phase B: SUPPORTED components transcoded to disk
			WireAddedCount:   int(s.AddedCount),
			WireRemovedCount: int(s.RemovedCount),
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

// hydrateTickingBlockEntities is the load-side ticker registration for a chunk becoming ready --
// the Go analogue of net.minecraft.world.level.chunk.LevelChunk.promotePendingBlockEntities /
// addAndRegisterBlockEntity, which register a TickingBlockEntity for every loaded block entity so a
// mid-cook furnace / running hopper / brewing stand / active crafter RESUMES ticking on reload
// WITHOUT a player interaction. Sulfur previously only populated the live BE maps lazily (on first
// menu open via resolveFurnace etc.), so a reloaded furnace mid-cook sat frozen until touched. This
// walks the chunk's persisted BlockEntity list and, for each block entity whose block still carries
// a server ticker (furnace-family, hopper, brewing stand, crafter -- the block-entity types with an
// AbstractFurnaceBlockEntity-style serverTick), calls the matching resolve* to decode its saved
// state and insert it into the live tick map. resolve* reads the persisted mid-cook progress
// (loadFurnaceBE etc.), so the resumed ticker continues exactly where it stopped. RNG-free
// reconstruction -- the pig oracle path (a generated chunk with no ticking BEs) hits nothing. Tick-
// owned; runs on the owner inside the owning region at chunk-ready. CITE: LevelChunk
// .promotePendingBlockEntities -> addAndRegisterBlockEntity -> updateBlockEntityTicker.
func (t *TickLoop) hydrateTickingBlockEntities(pos level.ChunkPos, ch *level.Chunk) {
	if ch == nil || t.world() == nil {
		return
	}
	baseX, baseZ := int(pos[0])<<4, int(pos[1])<<4
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		lx, lz := be.UnpackXZ()
		wp := pk.Position{X: baseX + lx, Y: int(be.Y), Z: baseZ + lz}
		state, ok := t.world().GetBlock(wp, dimMinY)
		if !ok {
			continue // the block-entity's cell is out of range / unloaded: skip (matches getBlockState air)
		}
		switch {
		case isAnyFurnaceBlock(state):
			t.resolveFurnace(wp, state) // AbstractFurnaceBlockEntity.serverTick
		case block.IsHopper(state):
			t.resolveHopper(wp, state) // HopperBlockEntity.pushItemsTick
		case isBrewingStandBlock(state):
			t.resolveBrewingStand(wp, state) // BrewingStandBlockEntity.serverTick
		case block.IsCrafter(state):
			t.resolveCrafter(wp, state) // CrafterBlockEntity.serverTick (crafting animation countdown)
		}
	}
}
