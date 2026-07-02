package server

// brewing_stand_persist.go — BREWING STAND block-entity NBT persistence (the furnace twin), ported 1:1 from
// the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). Folds each live
// brewingStandBE (t.brewingStands) into the chunk's BlockEntity list as the {Items, BrewTime, Fuel} disk
// compound on save, and decodes that compound back into a brewingStandBE on first access (chunk load).
//
// 1:1 ANCHORS (VERIFIED javap net.minecraft.world.level.block.entity.BrewingStandBlockEntity this session):
//
//	saveAdditional(ValueOutput out):
//	    super.saveAdditional(out);                                   // BaseContainerBlockEntity: CustomName (v1: none)
//	    out.putShort("BrewTime", (short) brewTime);
//	    ContainerHelper.saveAllItems(out, items);                    // "Items"
//	    out.putByte("Fuel", (byte) fuel);
//
//	loadAdditional(ValueInput in):
//	    super.loadAdditional(in);
//	    this.items = NonNullList.withSize(getContainerSize(), EMPTY);
//	    ContainerHelper.loadAllItems(in, items);                     // "Items"
//	    this.brewTime = in.getShortOr("BrewTime", 0);
//	    if (brewTime > 0) this.ingredient = items.get(3).getItem(); // re-cache the ingredient mid-brew
//	    this.fuel = in.getByteOr("Fuel", 0);
//
// COMPONENTS: the potion bottles carry a minecraft:potion_contents component (the brewed potion id). The
// Phase-A item-save (save.SaveItemsCompound) drops components (id + count survive; components metered as
// droppedComponents) — the SAME cited Phase-A limitation the furnace + chest persistence share (save/
// item_nbt.go PHASE A/B header). So a mid-brew stand's bottle potion ids are NOT yet round-tripped through
// disk (the live in-memory drive is fully faithful; the disk potion-component round-trip is the Phase-B
// follow-up the whole item-persistence layer shares). CITE save/item_nbt.go.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// brewingStandStateShape is the on-disk brewing-stand block-entity compound: the "Items" list + BrewTime
// (short) + Fuel (byte). BrewTime is int16 so the NBT encoder emits TAG_Short (out.putShort); Fuel is int8
// so it emits TAG_Byte (out.putByte).
//
// CITE: BrewingStandBlockEntity.saveAdditional field set (BrewTime, Items, Fuel).
type brewingStandStateShape struct {
	Items    []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
	BrewTime int16                        `nbt:"BrewTime"`
	Fuel     int8                         `nbt:"Fuel"`
}

// brewingItemsToDisk translates a brewingStandBE's 5-slot items into the []save.DiskItem the disk codec
// consumes (the furnace twin of furnaceItemsToDisk): each non-empty slot's numeric wire item id → its
// "minecraft:<name>" string id, flagging component-bearing stacks so SaveItemsCompound meters the Phase-A
// drop (the potion bottles carry potion_contents).
func brewingItemsToDisk(items [brewContainerSize]component.SlotData) []save.DiskItem {
	out := make([]save.DiskItem, len(items))
	for i, s := range items {
		if s.Count <= 0 {
			continue
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

// encodeBrewingStandBE builds the bare BlockEntity.Data compound for a brewingStandBE — saveAdditional's
// field set (Items + BrewTime + Fuel). saveAllItems is the 2-arg (keepEmptyTag=true) form. Returns the
// compound payload + the count of component-bearing stacks whose components were dropped (Phase A).
//
// CITE: BrewingStandBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func encodeBrewingStandBE(b *brewingStandBE) (nbt.RawMessage, int, error) {
	items, dropped := save.SaveAllItems(brewingItemsToDisk(b.items), true)
	shape := brewingStandStateShape{
		Items:    items,
		BrewTime: int16(b.brewTime), // (short) brewTime
		Fuel:     int8(b.fuel),      // (byte) fuel
	}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	// Strip the 3-byte root header so the result is the bare compound payload (BlockEntity.Data convention).
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeBrewingStandBE inverts encodeBrewingStandBE: decode a brewing-stand BlockEntity.Data compound into a
// fresh brewingStandBE carrying its 5 items, BrewTime, and Fuel, re-caching the ingredient from slot 3 when
// brewTime > 0 (the loadAdditional `if (brewTime > 0) ingredient = items.get(3).getItem()`). A garbled/empty
// compound yields an empty stand (listOrEmpty tolerance) — never a panic (the furnace-decode twin).
//
// CITE: BrewingStandBlockEntity.loadAdditional (temp/cache/26.2-inner.jar).
func decodeBrewingStandBE(data nbt.RawMessage) *brewingStandBE {
	b := &brewingStandBE{}

	var shape brewingStandStateShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape) // tolerant decode
	}

	loaded := save.LoadAllItems(shape.Items, brewContainerSize)
	for i := 0; i < brewContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		b.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),   // Phase B: rebuilt from the disk components compound
			RemovedCount:  toVar(it.WireRemovedCount), //
			RawComponents: it.WireComponents,          //
		}
	}

	b.brewTime = int(shape.BrewTime)
	// if (brewTime > 0) this.ingredient = items.get(3).getItem();
	if b.brewTime > 0 {
		ing := b.items[brewSlotIngredient]
		if !stackEmpty(ing) {
			b.hasIngr = true
			b.ingredID = int32(ing.ItemID)
		}
	}
	b.fuel = int(shape.Fuel)
	return b
}

// flushBrewingStandItems ports BrewingStandBlockEntity.saveAdditional for every live brewingStandBE in the
// column at pos (the furnace-flush twin): each t.brewingStands entry inside this column has its full state
// (Items + BrewTime + Fuel) folded into the chunk's BlockEntity list, rewriting the matching brewing-stand
// BE.Data in place (or appending a fresh BE for a placed-but-unrecorded stand). Runs on the owner.
//
// CITE: BrewingStandBlockEntity.saveAdditional (temp/cache/26.2-inner.jar).
func (t *TickLoop) flushBrewingStandItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.brewingStands == nil {
		return
	}
	for bpos, b := range t.brewingStands {
		if bpos.X>>4 != int(pos[0]) || bpos.Z>>4 != int(pos[1]) {
			continue
		}
		data, dropped, err := encodeBrewingStandBE(b)
		if err != nil {
			udebug("chunksave", "brewing stand state %v: %v", bpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "brewing stand %v: %d component-bearing stacks persisted without components (Phase A)", bpos, dropped)
		}
		lx, lz := bpos.X&15, bpos.Z&15
		setBrewingStandBEData(ch, lx, bpos.Y, lz, data)
	}
}

// setBrewingStandBEData rewrites the brewing-stand BlockEntity's Data compound at the LOCAL (lx,y,lz) cell in
// ch to the supplied state payload, setting the BE type. If no brewing-stand BE exists at that cell, it
// appends a fresh one (a placed stand whose BE was not in the gen list). The furnace twin (setFurnaceBEData).
func setBrewingStandBEData(ch *level.Chunk, lx, y, lz int, data nbt.RawMessage) {
	typ := block.EntityTypes["minecraft:brewing_stand"]
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

// loadBrewingStandBE looks up the brewing-stand BlockEntity at pos in the owning chunk's BlockEntity list and
// decodes its state into a fresh brewingStandBE, or returns nil if no brewing-stand BE is recorded there (a
// freshly-placed stand → the caller synthesizes an empty one). The furnace twin (loadFurnaceBE). Tolerant
// decode. Tick-owned.
func (t *TickLoop) loadBrewingStandBE(pos pk.Position) *brewingStandBE {
	if t.world() == nil {
		return nil
	}
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	ch, ok := t.world().Get(col)
	if !ok {
		return nil
	}
	typ := block.EntityTypes["minecraft:brewing_stand"]
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		be := ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx != lx || bz != lz || int(be.Y) != pos.Y {
			continue
		}
		if be.Type != typ {
			return nil
		}
		return decodeBrewingStandBE(be.Data)
	}
	return nil
}

// markBrewingStandDirty flags the column owning a brewing-stand position as dirty so its state is flushed on
// the next save pass (the markFurnaceDirty twin). Runs on the owner (tick-owned). No-op when persistence is
// off or the column is not Ready.
func (t *TickLoop) markBrewingStandDirty(bpos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(bpos.X >> 4), int32(bpos.Z >> 4)})
}
