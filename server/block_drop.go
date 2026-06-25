package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_drop.go — GAMEPLAY-06: spawn a pickable Item entity when a block is broken.
//
// handlePlayerAction (block_interact.go) sets the broken block to air, acks, and broadcasts
// BlockUpdate — then returns. This file adds the missing drop: after a successful break it
// looks up the block's drop (a v1 1:1 block->item map), spawns an entity.Item (ID 71) at the
// block center carrying the ITEM data-value (so it renders, not invisible — 06-RESEARCH
// Pitfall 5), and inserts it into the tick-owned entity store. The GAMEPLAY-01 tracker
// (tracker.go) then broadcasts ClientboundAddEntity + ClientboundSetEntityData to nearby
// clients on the next tick WITHOUT any new tracker code — the Item rides the same store-add
// path a mob does.
//
// All of this runs on the TICK goroutine (handlePlayerAction is drained by applyInput on the
// tick owner), so the store insert and id allocation are TICK-05 safe.

// blockDropFor maps a broken block state to its v1 drop stack: a minimal 1:1 block->item
// table (stone->cobblestone, dirt->dirt, etc.; identity where a block drops itself). Air and
// any block NOT in the table return (_, false) — no drop. The lookup keys on the broken
// block's resource id (Block.ID(), e.g. "minecraft:stone") so a block's many states all map
// to one drop.
//
// SCOPE (Assumption A1): this is the MINIMAL drop map, deliberately NOT a loot-table
// evaluator. Full loot tables (tool requirements, fortune, silk-touch, multi-drop, drop
// chances) are STRUCT-POLISH-01 (Phase 20), which replaces this map with a shared loot
// evaluator. v1 just makes broken blocks drop *something* pickable so the visual gate passes.
func blockDropFor(broken block.StateID) (component.SlotData, bool) {
	if int(broken) < 0 || int(broken) >= len(block.StateList) {
		return component.SlotData{}, false
	}
	b := block.StateList[broken]
	if block.IsAirBlock(b) {
		return component.SlotData{}, false // air drops nothing
	}
	drop, ok := blockDropTable[b.ID()]
	if !ok {
		return component.SlotData{}, false
	}
	return component.SlotData{Count: 1, ItemID: pk.VarInt(drop)}, true
}

// blockDropTable is the v1 block-resource-id -> dropped-item-id map. Keyed by Block.ID()
// (the "minecraft:<name>" resource id) and valued by the item registry id (data/item). Most
// blocks drop themselves; stone drops cobblestone, grass drops dirt — the few non-identity
// vanilla cases the map encodes explicitly. Superseded by STRUCT-POLISH-01's loot evaluator.
var blockDropTable = map[string]item.ID{
	"minecraft:stone":       item.Cobblestone.ID, // mined stone yields cobblestone
	"minecraft:grass_block": item.Dirt.ID,        // grass yields dirt (no silk touch in v1)
	"minecraft:cobblestone": item.Cobblestone.ID,
	"minecraft:dirt":        item.Dirt.ID,
	"minecraft:sand":        item.Sand.ID,
	"minecraft:oak_log":     item.OakLog.ID,
	"minecraft:oak_planks":  item.OakPlanks.ID,
}

// spawnBlockDrop spawns the dropped Item entity for a just-broken block and adds it to the
// tick-owned store, where the GAMEPLAY-01 tracker broadcasts it next tick. brokenState is the
// block state read BEFORE SetBlock wrote air (block_interact.go captures it). A block with no
// v1 drop (air/unknown) is a no-op. Tick-owned (TICK-05): runs on the tick goroutine.
func (t *TickLoop) spawnBlockDrop(pos pk.Position, brokenState block.StateID) {
	drop, ok := blockDropFor(brokenState)
	if !ok {
		return // no drop for this block (air/unknown)
	}

	// The Item sits at the block's lower-face center. Vanilla ItemEntity spawns at
	// pos + 0.5 on X/Z and a small +0.25-ish Y nudge inside the block; v1 uses the block
	// center on X/Z and the block's lower Y (good enough — the client renders the item on
	// the ground at this column). Velocity stays zero for a deterministic v1 drop (no pop);
	// a random pop is a later cosmetic and the tracker's motion encode already supports it.
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y)
	cz := float64(pos.Z) + 0.5

	ie := NewEntity(t.idAlloc.AllocID(), entity.Item, cx, cy, cz)

	// Populate the ITEM metadata (the Pitfall-5 fix). encodeSetEntityData splices
	// Entity.metadata VERBATIM (already in DataValue framing) and appends the 0xFF
	// terminator itself — so metadata must hold ONLY the entry bytes, no terminator.
	ie.metadata = encodeItemMetadata(drop)

	t.entities.add(ie) // store insert -> the tracker broadcasts AddEntity + SetEntityData
}

// encodeItemMetadata builds the verbatim SynchedEntityData DataValue bytes for a dropped
// stack — the single ITEM entry (Byte index, VarInt serializerId, ItemStack body) WITHOUT
// the 0xFF terminator (encodeSetEntityData appends that). The result goes into Entity.metadata
// so the tracker's encodeSetEntityData splices it unchanged.
func encodeItemMetadata(stack component.SlotData) []byte {
	var buf bytes.Buffer
	_, _ = itemDataEntry(stack).WriteTo(&buf)
	return buf.Bytes()
}
