package server

// hopper_persist.go — HOPPER block-entity NBT persistence + the resolveHopper store accessor (the
// dispenser twin), ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this
// session). Folds each live hopperBE (t.hoppers) into the chunk's BlockEntity list as the
// {Items, TransferCooldown} disk compound on save, and decodes that compound back into a hopperBE on
// first access (chunk load).
//
// 1:1 ANCHORS (VERIFIED CFR net.minecraft.world.level.block.entity.HopperBlockEntity this session):
//
//	saveAdditional(ValueOutput out):
//	    super.saveAdditional(out);                                   // BaseContainerBlockEntity: CustomName (v1: none)
//	    if (!trySaveLootTable(out)) ContainerHelper.saveAllItems(out, items);   // "Items"
//	    out.putInt("TransferCooldown", cooldownTime);
//	loadAdditional(ValueInput in):
//	    super.loadAdditional(in);
//	    this.items = NonNullList.withSize(getContainerSize(), EMPTY);
//	    if (!tryLoadLootTable(in)) ContainerHelper.loadAllItems(in, items);     // "Items"
//	    this.cooldownTime = in.getIntOr("TransferCooldown", -1);
//
// A placed hopper carries NO loot table (only structure-placed ones do, and v1 places none), so
// trySaveLootTable/tryLoadLootTable are always false and the container round-trips through the plain
// "Items" list — the same seam the dispenser uses.

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// hopperStateShape is the on-disk hopper block-entity compound: the "Items" list (5 slots) + the
// "TransferCooldown" int. CITE: HopperBlockEntity.saveAdditional.
type hopperStateShape struct {
	Items            []save.ItemStackWithSlotDisk `nbt:"Items,omitempty"`
	TransferCooldown int32                        `nbt:"TransferCooldown"`
}

// hopperItemsToDisk translates a hopperBE's 5-slot items into the []save.DiskItem the disk codec
// consumes (the dispenser twin of dispenserItemsToDisk).
func hopperItemsToDisk(items [hopperContainerSize]component.SlotData) []save.DiskItem {
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

// encodeHopperBE builds the bare BlockEntity.Data compound for a hopperBE — saveAdditional's field set
// (the "Items" list + "TransferCooldown"). Returns the compound payload + the count of component-bearing
// stacks whose components were dropped (Phase A). CITE: HopperBlockEntity.saveAdditional.
func encodeHopperBE(h *hopperBE) (nbt.RawMessage, int, error) {
	items, dropped := save.SaveAllItems(hopperItemsToDisk(h.items), true)
	shape := hopperStateShape{Items: items, TransferCooldown: int32(h.cooldownTime)}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, dropped, err
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, dropped, nil
}

// decodeHopperBE inverts encodeHopperBE: decode a hopper BlockEntity.Data compound into a fresh hopperBE
// carrying its 5 items + cooldown. A garbled/empty compound yields an empty hopper with the default
// cooldown -1 (the dispenser-decode twin, tolerant). CITE: HopperBlockEntity.loadAdditional.
func decodeHopperBE(data nbt.RawMessage) *hopperBE {
	h := &hopperBE{cooldownTime: hopperNoCooldown}

	var shape hopperStateShape
	shape.TransferCooldown = int32(hopperNoCooldown) // getIntOr(..., -1) default
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape) // tolerant decode
	}
	h.cooldownTime = int(shape.TransferCooldown)

	loaded := save.LoadAllItems(shape.Items, hopperContainerSize)
	for i := 0; i < hopperContainerSize && i < len(loaded); i++ {
		it := loaded[i]
		if it.IsEmpty() {
			continue
		}
		h.items[i] = component.SlotData{
			ItemID:        toItemID(int(itemNameToID(it.ID))),
			Count:         toVar(int(it.Count)),
			AddedCount:    toVar(it.WireAddedCount),
			RemovedCount:  toVar(it.WireRemovedCount),
			RawComponents: it.WireComponents,
		}
	}
	return h
}

// resolveHopper returns the tick-owned hopperBE for pos, creating a fresh one (cooldown -1) on first
// access — the analogue of a freshly-placed HopperBlockEntity. Tries to restore persisted state from the
// chunk's BlockEntity list first (the dispenser resolveDispenser twin). Returns nil only when pos is not a
// hopper block (or the world is unloaded). Tick-owned (t.hoppers).
func (t *TickLoop) resolveHopper(pos pk.Position, state block.StateID) *hopperBE {
	if t.hoppers == nil {
		t.hoppers = make(map[pk.Position]*hopperBE)
	}
	if h, ok := t.hoppers[pos]; ok {
		return h
	}
	if !block.IsHopper(state) {
		return nil
	}
	h := t.loadHopperBE(pos)
	if h == nil {
		h = &hopperBE{cooldownTime: hopperNoCooldown}
	}
	t.hoppers[pos] = h
	return h
}

// isHopperEntityType reports whether a block-entity type is a hopper BE type.
func isHopperEntityType(typ block.EntityType) bool {
	return typ == block.EntityTypes["minecraft:hopper"]
}

// setHopperBEData rewrites the hopper BlockEntity's Data compound at the LOCAL (lx,y,lz) cell in ch to the
// supplied payload. If no hopper BE exists at that cell, it appends a fresh one. The dispenser
// setDispenserBEData twin.
func setHopperBEData(ch *level.Chunk, lx, y, lz int, data nbt.RawMessage) {
	for i := range ch.BlockEntity {
		be := &ch.BlockEntity[i]
		bx, bz := be.UnpackXZ()
		if bx == lx && bz == lz && int(be.Y) == y && isHopperEntityType(be.Type) {
			be.Data = data
			return
		}
	}
	var be level.BlockEntity
	be.PackXZ(lx, lz)
	be.Y = int16(y)
	be.Type = block.EntityTypes["minecraft:hopper"]
	be.Data = data
	ch.BlockEntity = append(ch.BlockEntity, be)
}

// flushHopperItems folds every live hopperBE in the column at pos into the chunk's BlockEntity list (the
// dispenser flushDispenserItems twin). Runs on the owner over tick-owned state. CITE:
// HopperBlockEntity.saveAdditional.
func (t *TickLoop) flushHopperItems(pos level.ChunkPos, ch *level.Chunk) {
	if t.hoppers == nil {
		return
	}
	for hpos, h := range t.hoppers {
		if hpos.X>>4 != int(pos[0]) || hpos.Z>>4 != int(pos[1]) {
			continue
		}
		data, dropped, err := encodeHopperBE(h)
		if err != nil {
			udebug("chunksave", "hopper state %v: %v", hpos, err)
			continue
		}
		if dropped > 0 {
			udebug("chunksave", "hopper %v: %d component-bearing stacks persisted without components (Phase A)", hpos, dropped)
		}
		lx, lz := hpos.X&15, hpos.Z&15
		setHopperBEData(ch, lx, hpos.Y, lz, data)
	}
}

// loadHopperBE looks up the hopper BlockEntity at pos in the owning chunk's BlockEntity list and decodes
// its Items + cooldown into a fresh hopperBE, or returns nil if no hopper BE is recorded there (a
// freshly-placed hopper -> the caller synthesizes an empty one). The dispenser loadDispenserBE twin.
// Tolerant decode. Tick-owned.
func (t *TickLoop) loadHopperBE(pos pk.Position) *hopperBE {
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
		if !isHopperEntityType(be.Type) {
			return nil
		}
		return decodeHopperBE(be.Data)
	}
	return nil
}

// markHopperDirty flags the column owning a hopper position as dirty so its state is flushed on the next
// save pass (the dispenser markDispenserDirty twin). Called after the transfer drive + menu clicks mutate
// a hopperBE. A no-op when persistence is off. Runs on the owner (the hopper path is tick-owned).
func (t *TickLoop) markHopperDirty(hopperPos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(hopperPos.X >> 4), int32(hopperPos.Z >> 4)})
}
