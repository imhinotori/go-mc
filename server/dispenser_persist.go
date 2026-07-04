package server

// dispenser_persist.go — DISPENSER / DROPPER block-entity NBT persistence (the furnace twin, REDSTONE
// TIER-4), ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this session). Folds
// each live dispenserBE (t.dispensers) into the chunk's BlockEntity list as the {Items} disk compound on
// save, and decodes that compound back into a dispenserBE on first access (chunk load).
//
// 1:1 ANCHORS (VERIFIED CFR net.minecraft.world.level.block.entity.DispenserBlockEntity this session):
//
//	saveAdditional(ValueOutput out):
//	    super.saveAdditional(out);                                    // BaseContainerBlockEntity: CustomName (v1: none)
//	    if (!trySaveLootTable(out)) ContainerHelper.saveAllItems(out, items);   // "Items"
//
//	loadAdditional(ValueInput in):
//	    super.loadAdditional(in);
//	    this.items = NonNullList.withSize(getContainerSize(), EMPTY);
//	    if (!tryLoadLootTable(in)) ContainerHelper.loadAllItems(in, items);     // "Items"
//
// A placed dispenser/dropper carries NO loot table (only structure-placed ones do, and v1 places none),
// so trySaveLootTable/tryLoadLootTable are always false and the container round-trips through the plain
// "Items" list — the same seam the furnace uses. The block-entity TYPE (dispenser vs dropper) is fixed by
// the CURRENT block state (DispenserBlock.newBlockEntity / DropperBlock.newBlockEntity), threaded in by the
// resolve path (isDropper), NOT persisted — vanilla's BlockEntityType fixes it.
//
// COMPONENTS: item components share the Phase-A drop of save.SaveAllItems (item id + count survive;
// components metered as droppedComponents). CITE save/item_nbt.go PHASE A/B header.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// dispenserStateShape is the on-disk dispenser/dropper block-entity compound: just the "Items" list (the
// 9-slot container). No cook progress / no RecipesUsed (unlike the furnace) — the dispenser is a plain
// container. CITE: DispenserBlockEntity.saveAdditional (saveAllItems only).
type dispenserStateShape struct {
	Items []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
}

// dispenserItemsToDisk translates a dispenserBE's 9-slot items into the []save.DiskItem the disk codec
// consumes (the furnace twin of furnaceItemsToDisk): each non-empty slot's numeric wire item id -> its
// "minecraft:<name>" string id, flagging component-bearing stacks so SaveAllItems meters the Phase-A drop.
func dispenserItemsToDisk(items [dispenserContainerSize]component.SlotData) []save.DiskItem {
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

// encodeDispenserBE builds the bare BlockEntity.Data compound for a dispenserBE — saveAdditional's field
// set (just the "Items" list). saveAllItems here is the 2-arg (keepEmptyTag=true) form so an all-empty
// dispenser still emits an (empty) "Items" list. Returns the compound payload + the count of
// component-bearing stacks whose components were dropped (Phase A).
//
// CITE: DispenserBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func encodeDispenserBE(d *dispenserBE) (nbt.RawMessage, int, error) {
	items, dropped := save.SaveAllItems(dispenserItemsToDisk(d.items), true)
	shape := dispenserStateShape{Items: items}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	// Strip the 3-byte root header so the result is the bare compound payload (the BlockEntity.Data
	// convention, the furnace/chest twin).
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeDispenserBE inverts encodeDispenserBE: decode a dispenser/dropper BlockEntity.Data compound into a
// fresh dispenserBE carrying its 9 items. isDropper comes from the CURRENT block state (the ctor's
// BlockEntityType, not persisted), threaded in by the caller. A garbled/empty compound yields an empty
// dispenser (loadAllItems listOrEmpty tolerance) — never a panic (the furnace-decode twin).
//
// CITE: DispenserBlockEntity.loadAdditional (temp/cache/26.2-inner.jar).
func decodeDispenserBE(data nbt.RawMessage, isDropper bool) *dispenserBE {
	d := &dispenserBE{isDropper: isDropper}

	var shape dispenserStateShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape) // tolerant decode: a corrupt BE loads empty, never panics
	}

	loaded := save.LoadAllItems(shape.Items, dispenserContainerSize)
	for i := 0; i < dispenserContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		d.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),
			RemovedCount:  toVar(it.WireRemovedCount),
			RawComponents: it.WireComponents,
		}
	}
	return d
}

// flushDispenserItems ports DispenserBlockEntity.saveAdditional for every live dispenserBE in the column at
// pos (the furnace-flush twin): each t.dispensers entry inside this column has its Items folded into the
// chunk's BlockEntity list, rewriting the matching dispenser/dropper BE.Data in place (or appending a fresh
// BE for a placed-but-unrecorded dispenser). Runs on the owner over tick-owned state; mutates only the
// chunk's own BE slice.
//
// CITE: DispenserBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func (t *TickLoop) flushDispenserItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.dispensers == nil {
		return
	}
	for dpos, d := range t.dispensers {
		if dpos.X>>4 != int(pos[0]) || dpos.Z>>4 != int(pos[1]) {
			continue // a dispenser in a different column
		}
		data, dropped, err := encodeDispenserBE(d)
		if err != nil {
			udebug("chunksave", "dispenser state %v: %v", dpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "dispenser %v: %d component-bearing stacks persisted without components (Phase A)", dpos, dropped)
		}
		lx, lz := dpos.X&15, dpos.Z&15
		setDispenserBEData(ch, lx, dpos.Y, lz, dispenserBEType(d.isDropper), data)
	}
}

// dispenserBEType maps a dispenserBE to its block-entity type id (the ctor's BlockEntityType):
// dropper -> minecraft:dropper, else minecraft:dispenser.
func dispenserBEType(isDropper bool) block.EntityType {
	if isDropper {
		return block.EntityTypes["minecraft:dropper"]
	}
	return block.EntityTypes["minecraft:dispenser"]
}

// isDispenserEntityType reports whether a block-entity type is a dispenser or dropper BE type.
func isDispenserEntityType(typ block.EntityType) bool {
	return typ == block.EntityTypes["minecraft:dispenser"] ||
		typ == block.EntityTypes["minecraft:dropper"]
}

// setDispenserBEData rewrites the dispenser/dropper BlockEntity's Data compound at the LOCAL (lx,y,lz) cell
// in ch to the supplied Items payload, preserving/setting the BE type. If no dispenser-family BE exists at
// that cell, it appends a fresh one (a placed dispenser whose BE was not in the gen list). The furnace
// setFurnaceBEData twin.
func setDispenserBEData(ch *level.Chunk, lx, y, lz int, typ block.EntityType, data nbt.RawMessage) {
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && isDispenserEntityType(be.Type) {
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

// loadDispenserBE looks up the dispenser/dropper BlockEntity at pos in the owning chunk's BlockEntity list
// and decodes its Items into a fresh dispenserBE (isDropper from the passed-in current block state), or
// returns nil if no dispenser-family BE is recorded there (a freshly-placed dispenser with no persisted
// state -> the caller synthesizes an empty one). The furnace loadFurnaceBE twin. Tolerant decode.
// Tick-owned.
func (t *TickLoop) loadDispenserBE(pos pk.Position, isDropper bool) *dispenserBE {
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
		if !isDispenserEntityType(be.Type) {
			return nil // a non-dispenser BE at this exact cell is not a dispenser state
		}
		return decodeDispenserBE(be.Data, isDropper)
	}
	return nil
}

// markDispenserDirty flags the column owning a dispenser position as dirty so its state is flushed on the
// next save pass (the furnace markFurnaceDirty twin). The dispense drive + menu clicks call it after
// mutating a dispenserBE so a shot / item move is persisted. A no-op when persistence is off or the column
// is not Ready. Runs on the owner (the dispenser path is tick-owned).
func (t *TickLoop) markDispenserDirty(dispenserPos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(dispenserPos.X >> 4), int32(dispenserPos.Z >> 4)})
}
