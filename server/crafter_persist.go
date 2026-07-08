package server

// crafter_persist.go -- CRAFTER block-entity NBT persistence (the dispenser twin), ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap this session). Folds each live crafterBE
// (t.crafters) into the chunk BlockEntity list on save and decodes it back on first access.
//
// 1:1 ANCHORS (VERIFIED javap CrafterBlockEntity this session):
//   saveAdditional(out): super.saveAdditional; out.putInt("crafting_ticks_remaining", craftingTicksRemaining);
//       if (!trySaveLootTable(out)) ContainerHelper.saveAllItems(out, items); addDisabledSlots(out); addTriggered(out).
//   addDisabledSlots(out): IntArrayList of the slot indices where isSlotDisabled(i); out.putIntArray("disabled_slots", ...).
//   loadAdditional(in): craftingTicksRemaining = in.getIntOr("crafting_ticks_remaining", 0);
//       loadAllItems("Items"); for each i in disabled_slots setSlotState(i, false); triggered = in.getBooleanOr("triggered", false).
//
// A placed crafter carries NO loot table (only structure-placed ones do; v1 places none), so the container
// round-trips through the plain "Items" list -- the dispenser seam. The TRIGGERED animation flag round-trips
// through the block-state TRIGGERED property (not re-stored here), matching how the resolve path reads it.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// crafterStateShape is the on-disk crafter block-entity compound: the "Items" list, the crafting-ticks
// countdown, and the disabled-slot index array. CITE: CrafterBlockEntity.saveAdditional.
type crafterStateShape struct {
	Items                  []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
	CraftingTicksRemaining int32                        `nbt:"crafting_ticks_remaining"`
	DisabledSlots          []int32                      `nbt:"disabled_slots,omitempty"`
}

// crafterItemsToDisk translates a crafterBE 9-slot items into the []save.DiskItem the disk codec consumes
// (the dispenser twin of dispenserItemsToDisk).
func crafterItemsToDisk(items [crafterContainerSize]component.SlotData) []save.DiskItem {
	out := make([]save.DiskItem, len(items))
	for i, s := range items {
		if s.Count <= 0 {
			continue
		}
		out[i] = save.DiskItem{
			ID:               itemName(int32(s.ItemID)),
			Count:            int32(s.Count),
			HasComponents:    len(s.RawComponents) > 0,
			WireComponents:   s.RawComponents,
			WireAddedCount:   int(s.AddedCount),
			WireRemovedCount: int(s.RemovedCount),
		}
	}
	return out
}

// encodeCrafterBE builds the bare BlockEntity.Data compound for a crafterBE (Items + crafting-ticks +
// disabled_slots). Returns the compound payload + the count of component-bearing stacks whose components
// were dropped (Phase A). CITE: CrafterBlockEntity.saveAdditional.
func encodeCrafterBE(c *crafterBE) (nbt.RawMessage, int, error) {
	items, dropped := save.SaveAllItems(crafterItemsToDisk(c.items), true)
	var disabled []int32
	for i := 0; i < crafterContainerSize; i++ {
		if c.disabled[i] {
			disabled = append(disabled, int32(i))
		}
	}
	shape := crafterStateShape{
		Items:                  items,
		CraftingTicksRemaining: int32(c.craftingTicksRemaining),
		DisabledSlots:          disabled,
	}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeCrafterBE inverts encodeCrafterBE: decode a crafter BlockEntity.Data compound into a fresh
// crafterBE. A garbled/empty compound yields an empty crafter (never a panic). CITE:
// CrafterBlockEntity.loadAdditional.
func decodeCrafterBE(data nbt.RawMessage) *crafterBE {
	c := &crafterBE{}
	var shape crafterStateShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape)
	}
	c.craftingTicksRemaining = int(shape.CraftingTicksRemaining)
	loaded := save.LoadAllItems(shape.Items, crafterContainerSize)
	for i := 0; i < crafterContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		c.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),
			RemovedCount:  toVar(it.WireRemovedCount),
			RawComponents: it.WireComponents,
		}
	}
	for _, idx := range shape.DisabledSlots {
		if idx >= 0 && int(idx) < crafterContainerSize {
			c.disabled[idx] = true
		}
	}
	return c
}

// flushCrafterItems folds every live crafterBE in the column at pos into the chunk BlockEntity list (the
// dispenser-flush twin). CITE: CrafterBlockEntity.saveAdditional.
func (t *TickLoop) flushCrafterItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.crafters == nil {
		return
	}
	for cpos, c := range t.crafters {
		if cpos.X>>4 != int(pos[0]) || cpos.Z>>4 != int(pos[1]) {
			continue
		}
		data, dropped, err := encodeCrafterBE(c)
		if err != nil {
			udebug("chunksave", "crafter state %v: %v", cpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "crafter %v: %d component-bearing stacks persisted without components (Phase A)", cpos, dropped)
		}
		lx, lz := cpos.X&15, cpos.Z&15
		setCrafterBEData(ch, lx, cpos.Y, lz, data)
	}
}

// isCrafterEntityType reports whether a block-entity type is the crafter BE type.
func isCrafterEntityType(typ block.EntityType) bool {
	return typ == block.EntityTypes["minecraft:crafter"]
}

// setCrafterBEData rewrites the crafter BlockEntity Data compound at the LOCAL (lx,y,lz) cell in ch, or
// appends a fresh one (a placed crafter whose BE was not in the gen list). The dispenser setDispenserBEData
// twin.
func setCrafterBEData(ch *level.Chunk, lx, y, lz int, data nbt.RawMessage) {
	typ := block.EntityTypes["minecraft:crafter"]
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && isCrafterEntityType(be.Type) {
			be.Type = typ
			be.Data = data
			return
		}
	}
	var be level.BlockEntity
	be.PackXZ(lx, lz)
	be.Y = int16(y)
	be.Type = typ
	be.Data = data
	ch.BlockEntity = append(ch.BlockEntity, be)
}

// loadCrafterBE looks up the crafter BlockEntity at pos in the owning chunk and decodes it, or returns nil
// if none is recorded there (a freshly-placed crafter -> the caller synthesizes an empty one). The
// dispenser loadDispenserBE twin. Tolerant decode. Tick-owned.
func (t *TickLoop) loadCrafterBE(pos pk.Position) *crafterBE {
	if t.world() == nil {
		return nil
	}
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	ch, ok := t.world().Get(col)
	if !ok {
		return nil
	}
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx != lx || bz != lz || int(be.Y) != pos.Y {
			continue
		}
		if !isCrafterEntityType(be.Type) {
			return nil
		}
		return decodeCrafterBE(be.Data)
	}
	return nil
}

// markCrafterDirty flags the column owning a crafter position as dirty so its state flushes on the next
// save pass (the dispenser markDispenserDirty twin). Runs on the owner (tick-owned).
func (t *TickLoop) markCrafterDirty(crafterPos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(crafterPos.X >> 4), int32(crafterPos.Z >> 4)})
}

// tickCrafters drives CrafterBlockEntity.serverTick for every live crafter (the tickFurnaces twin): read
// each crafter current block state; drop the BE if the block is gone; else run the CRAFTING countdown.
// Tick-owned. Called from tick_phases.go.
func (t *TickLoop) tickCrafters() {
	if len(t.crafters) == 0 {
		return
	}
	w := t.world()
	for pos, c := range t.crafters {
		if w == nil {
			continue
		}
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsCrafter(state) {
			delete(t.crafters, pos)
			continue
		}
		t.crafterServerTick(pos, state, c)
	}
}
