package server

// chiseled_bookshelf_persist.go -- CHISELED BOOKSHELF block-entity NBT persistence (BOOKSHELF-01, the
// dispenser twin), ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this
// session). Folds each live chiseledBookshelfBE (t.bookshelves) into the chunk's BlockEntity list as the
// {Items, last_interacted_slot} disk compound on save, and decodes that compound back into a
// chiseledBookshelfBE on first access (chunk load).
//
// 1:1 ANCHORS (VERIFIED javap net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity this
// session):
//
//	saveAdditional(ValueOutput out):
//	    super.saveAdditional(out);                                       // BlockEntity: no extra fields
//	    ContainerHelper.saveAllItems(out, this.items, true);            // "Items" (keepEmptyTag=true)
//	    out.putInt("last_interacted_slot", this.lastInteractedSlot);    // "last_interacted_slot"
//
//	loadAdditional(ValueInput in):
//	    super.loadAdditional(in);
//	    this.items.clear();
//	    ContainerHelper.loadAllItems(in, this.items);                   // "Items"
//	    this.lastInteractedSlot = in.getIntOr("last_interacted_slot", -1);
//
// saveAllItems here is the 3-arg (keepEmptyTag=true) form so an all-empty shelf still emits an (empty)
// "Items" list. The block-entity TYPE (minecraft:chiseled_bookshelf) is fixed by the block state, not
// persisted. COMPONENTS share the Phase-A drop of save.SaveAllItems (item id + count survive; components
// metered as droppedComponents). CITE save/item_nbt.go PHASE A/B header.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// chiseledBookshelfStateShape is the on-disk chiseled_bookshelf block-entity compound: the 6-slot "Items"
// list + the "last_interacted_slot" int. CITE ChiseledBookShelfBlockEntity.saveAdditional.
type chiseledBookshelfStateShape struct {
	Items              []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
	LastInteractedSlot int32                        `nbt:"last_interacted_slot"`
}

// chiseledBookshelfItemsToDisk translates a chiseledBookshelfBE's 6-slot items into the []save.DiskItem
// the disk codec consumes (the dispenser twin): each non-empty slot's numeric wire item id -> its
// "minecraft:<name>" string id, flagging component-bearing stacks so SaveAllItems meters the Phase-A drop.
func chiseledBookshelfItemsToDisk(items [chiseledBookshelfMaxBooks]component.SlotData) []save.DiskItem {
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

// encodeChiseledBookshelfBE builds the bare BlockEntity.Data compound for a chiseledBookshelfBE --
// saveAdditional's field set (the "Items" list + "last_interacted_slot"). Returns the compound payload +
// the count of component-bearing stacks whose components were dropped (Phase A). CITE
// ChiseledBookShelfBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func encodeChiseledBookshelfBE(b *chiseledBookshelfBE) (nbt.RawMessage, int, error) {
	items, dropped := save.SaveAllItems(chiseledBookshelfItemsToDisk(b.items), true)
	shape := chiseledBookshelfStateShape{Items: items, LastInteractedSlot: int32(b.lastInteractedSlot)}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	// Strip the 3-byte root header so the result is the bare compound payload (the dispenser/chest twin).
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeChiseledBookshelfBE inverts encodeChiseledBookshelfBE: decode a chiseled_bookshelf
// BlockEntity.Data compound into a fresh chiseledBookshelfBE carrying its 6 items + last_interacted_slot.
// A garbled/empty compound yields an empty shelf with lastInteractedSlot -1 (loadAllItems listOrEmpty
// tolerance + the getIntOr default) -- never a panic (the dispenser-decode twin). CITE
// ChiseledBookShelfBlockEntity.loadAdditional (temp/cache/26.2-inner.jar).
func decodeChiseledBookshelfBE(data nbt.RawMessage) *chiseledBookshelfBE {
	b := &chiseledBookshelfBE{lastInteractedSlot: chiseledBookshelfDefaultLastSlot}

	var shape chiseledBookshelfStateShape
	shape.LastInteractedSlot = chiseledBookshelfDefaultLastSlot // getIntOr("last_interacted_slot", -1) default
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape) // tolerant decode: a corrupt BE loads empty, never panics
	}
	b.lastInteractedSlot = int(shape.LastInteractedSlot)

	loaded := save.LoadAllItems(shape.Items, chiseledBookshelfMaxBooks)
	for i := 0; i < chiseledBookshelfMaxBooks && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		b.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),
			RemovedCount:  toVar(it.WireRemovedCount),
			RawComponents: it.WireComponents,
		}
	}
	return b
}

// chiseledBookshelfEntityType is the block-entity type id for a chiseled bookshelf.
func chiseledBookshelfEntityType() block.EntityType {
	return block.EntityTypes["minecraft:chiseled_bookshelf"]
}

// flushChiseledBookshelfItems ports ChiseledBookShelfBlockEntity.saveAdditional for every live
// chiseledBookshelfBE in the column at pos (the dispenser-flush twin): each t.bookshelves entry inside
// this column has its Items + last_interacted_slot folded into the chunk's BlockEntity list, rewriting the
// matching chiseled_bookshelf BE.Data in place (or appending a fresh BE for a placed-but-unrecorded
// shelf). Runs on the owner over tick-owned state; mutates only the chunk's own BE slice. CITE
// ChiseledBookShelfBlockEntity.saveAdditional.
func (t *TickLoop) flushChiseledBookshelfItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.bookshelves == nil {
		return
	}
	for bpos, b := range t.bookshelves {
		if bpos.X>>4 != int(pos[0]) || bpos.Z>>4 != int(pos[1]) {
			continue // a bookshelf in a different column
		}
		data, dropped, err := encodeChiseledBookshelfBE(b)
		if err != nil {
			udebug("chunksave", "chiseled_bookshelf state %v: %v", bpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "chiseled_bookshelf %v: %d component-bearing stacks persisted without components (Phase A)", bpos, dropped)
		}
		lx, lz := bpos.X&15, bpos.Z&15
		setChiseledBookshelfBEData(ch, lx, bpos.Y, lz, data)
	}
}

// setChiseledBookshelfBEData rewrites the chiseled_bookshelf BlockEntity's Data compound at the LOCAL
// (lx,y,lz) cell in ch to the supplied payload, preserving/setting the BE type. If no chiseled_bookshelf
// BE exists at that cell, it appends a fresh one (a placed shelf whose BE was not in the gen list). The
// dispenser setDispenserBEData twin.
func setChiseledBookshelfBEData(ch *level.Chunk, lx, y, lz int, data nbt.RawMessage) {
	typ := chiseledBookshelfEntityType()
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && be.Type == typ {
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

// loadChiseledBookshelfBE looks up the chiseled_bookshelf BlockEntity at pos in the owning chunk's
// BlockEntity list and decodes its Items + last_interacted_slot into a fresh chiseledBookshelfBE, or
// returns nil if no chiseled_bookshelf BE is recorded there (a freshly-placed shelf with no persisted
// state -> the caller synthesizes an empty one). The dispenser loadDispenserBE twin. Tolerant decode.
// Tick-owned.
func (t *TickLoop) loadChiseledBookshelfBE(pos pk.Position) *chiseledBookshelfBE {
	if t.world() == nil {
		return nil
	}
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	ch, ok := t.world().Get(col)
	if !ok {
		return nil
	}
	typ := chiseledBookshelfEntityType()
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx != lx || bz != lz || int(be.Y) != pos.Y {
			continue
		}
		if be.Type != typ {
			return nil // a non-bookshelf BE at this exact cell is not a bookshelf state
		}
		return decodeChiseledBookshelfBE(be.Data)
	}
	return nil
}

// markBookshelfDirty flags the column owning a bookshelf position as dirty so its state is flushed on the
// next save pass (the dispenser markDispenserDirty twin). The add/remove-book paths call it after
// mutating a chiseledBookshelfBE so an inserted/removed book is persisted. A no-op when persistence is off
// or the column is not Ready. Runs on the owner (the bookshelf path is tick-owned).
func (t *TickLoop) markBookshelfDirty(pos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)})
}
