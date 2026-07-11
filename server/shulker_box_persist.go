package server

// shulker_box_persist.go -- SHULKER BOX block-entity NBT persistence (the dispenser twin), ported 1:1 from
// the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar). Folds each live shulkerBE (t.shulkers) into the
// chunk's BlockEntity list as the {Items} disk compound on save, and decodes that compound back into a
// shulkerBE on first access (chunk load).
//
// 1:1 ANCHORS (VERIFIED net.minecraft.world.level.block.entity.ShulkerBoxBlockEntity this session):
//
//	saveAdditional(out): super.saveAdditional(out); if (!trySaveLootTable(out)) saveAllItems(out, itemStacks, false);
//	loadFromTag(in):     itemStacks = withSize(getContainerSize(), EMPTY); if (!tryLoadLootTable(in)) loadAllItems(in, itemStacks);
//
// A placed shulker box carries NO loot table (only structure-placed ones do, and v1 places none), so
// trySaveLootTable/tryLoadLootTable are always false and the container round-trips through the plain
// "Items" list -- the same seam the dispenser uses. The lid animation (animationStatus/progress/openCount)
// is NOT persisted (vanilla resets it to CLOSED on load; a reloaded box always starts closed), matching
// ShulkerBoxBlockEntity which persists ONLY the Items list.
//
// COMPONENTS: item components share the Phase-A drop of save.SaveAllItems (item id + count survive;
// components metered as dropped). CITE save/item_nbt.go PHASE A/B header.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// shulkerStateShape is the on-disk shulker_box block-entity compound: just the "Items" list (the 27-slot
// container). No lid animation state -- ShulkerBoxBlockEntity.saveAdditional persists only the container.
// CITE ShulkerBoxBlockEntity.saveAdditional (saveAllItems only, animation not saved).
type shulkerStateShape struct {
	Items []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
}

// shulkerItemsToDisk translates a shulkerBE's 27-slot items into the []save.DiskItem the disk codec
// consumes (the dispenser twin of dispenserItemsToDisk): each non-empty slot's numeric wire item id -> its
// "minecraft:<name>" string id, flagging component-bearing stacks so SaveAllItems meters the Phase-A drop.
func shulkerItemsToDisk(items [shulkerContainerSize]component.SlotData) []save.DiskItem {
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

// encodeShulkerBE builds the bare BlockEntity.Data compound for a shulkerBE -- saveAdditional's field set
// (just the "Items" list). saveAllItems here is the keepEmptyTag=true form so an all-empty shulker still
// emits an (empty) "Items" list. Returns the compound payload + the count of component-bearing stacks whose
// components were dropped (Phase A). CITE ShulkerBoxBlockEntity.saveAdditional.
func encodeShulkerBE(s *shulkerBE) (nbt.RawMessage, int, error) {
	items, dropped := save.SaveAllItems(shulkerItemsToDisk(s.items), true)
	shape := shulkerStateShape{Items: items}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	// Strip the 3-byte root header so the result is the bare compound payload (the dispenser/chest twin).
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeShulkerBE inverts encodeShulkerBE: decode a shulker_box BlockEntity.Data compound into a fresh
// shulkerBE carrying its 27 items (CLOSED lid -- the animation is not persisted). A garbled/empty compound
// yields an empty shulker (loadAllItems listOrEmpty tolerance) -- never a panic (the dispenser-decode twin).
// CITE ShulkerBoxBlockEntity.loadFromTag.
func decodeShulkerBE(data nbt.RawMessage) *shulkerBE {
	s := &shulkerBE{animationStatus: shulkerClosed}

	var shape shulkerStateShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape) // tolerant decode: a corrupt BE loads empty, never panics
	}

	loaded := save.LoadAllItems(shape.Items, shulkerContainerSize)
	for i := 0; i < shulkerContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		s.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),
			RemovedCount:  toVar(it.WireRemovedCount),
			RawComponents: it.WireComponents,
		}
	}
	return s
}

// flushShulkerItems ports ShulkerBoxBlockEntity.saveAdditional for every live shulkerBE in the column at pos
// (the dispenser-flush twin): each t.shulkers entry inside this column has its Items folded into the chunk's
// BlockEntity list, rewriting the matching shulker BE.Data in place (or appending a fresh BE for a
// placed-but-unrecorded shulker). Runs on the owner over tick-owned state; mutates only the chunk's own BE
// slice. CITE ShulkerBoxBlockEntity.saveAdditional.
func (t *TickLoop) flushShulkerItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.shulkers == nil {
		return
	}
	for spos, s := range t.shulkers {
		if spos.X>>4 != int(pos[0]) || spos.Z>>4 != int(pos[1]) {
			continue // a shulker in a different column
		}
		data, dropped, err := encodeShulkerBE(s)
		if err != nil {
			udebug("chunksave", "shulker state %v: %v", spos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "shulker %v: %d component-bearing stacks persisted without components (Phase A)", spos, dropped)
		}
		lx, lz := spos.X&15, spos.Z&15
		setShulkerBEData(ch, lx, spos.Y, lz, data)
	}
}

// shulkerBEType is the shulker_box block-entity type id. All 16 dyed variants + the undyed base share the
// single minecraft:shulker_box block-entity type. CITE BlockEntityTypes.SHULKER_BOX.
func shulkerBEType() block.EntityType {
	return block.EntityTypes["minecraft:shulker_box"]
}

// isShulkerEntityType reports whether a block-entity type is the shulker_box BE type.
func isShulkerEntityType(typ block.EntityType) bool {
	return typ == block.EntityTypes["minecraft:shulker_box"]
}

// setShulkerBEData rewrites the shulker_box BlockEntity's Data compound at the LOCAL (lx,y,lz) cell in ch to
// the supplied Items payload. If no shulker BE exists at that cell, it appends a fresh one (a placed shulker
// whose BE was not in the gen list). The dispenser setDispenserBEData twin.
func setShulkerBEData(ch *level.Chunk, lx, y, lz int, data nbt.RawMessage) {
	typ := shulkerBEType()
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && isShulkerEntityType(be.Type) {
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

// loadShulkerBE looks up the shulker_box BlockEntity at pos in the owning chunk's BlockEntity list and
// decodes its Items into a fresh shulkerBE, or returns nil if no shulker BE is recorded there (a
// freshly-placed shulker with no persisted state -> the caller synthesizes an empty one). The dispenser
// loadDispenserBE twin. Tolerant decode. Tick-owned.
func (t *TickLoop) loadShulkerBE(pos pk.Position) *shulkerBE {
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
		if !isShulkerEntityType(be.Type) {
			return nil // a non-shulker BE at this exact cell is not shulker state
		}
		return decodeShulkerBE(be.Data)
	}
	return nil
}

// markShulkerDirty flags the column owning a shulker position as dirty so its state is flushed on the next
// save pass (the dispenser markDispenserDirty twin). The menu clicks + hopper transfers call it after
// mutating a shulkerBE. A no-op when persistence is off or the column is not Ready. Tick-owned.
func (t *TickLoop) markShulkerDirty(shulkerPos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(shulkerPos.X >> 4), int32(shulkerPos.Z >> 4)})
}
