package server

// enderman_carry_visible.go — MOB-HOST-08 (Enderman block-carry VISIBLE-STATE 1/1, the audit-flagged
// 1:1 expansion): the per-tick MAINHAND held-item surface that lets a client render the carried
// block as a held item in the enderman's hand. The jar-faithful DATA_CARRY_STATE synched data
// (entity_encode.go:708 carriedBlockDataEntry) already pushes the carried BlockState — this file
// ADDS the MAINHAND item-frame surface so a client that doesn't honor DATA_CARRY_STATE still
// renders the carried block as a held-item (the audit-flagged deviation from the strict jar
// contract, kept strictly additive so the pig oracle's RNG stream is unperturbed).
//
// 1:1 with temp/cache/26.2-inner.jar (READ-ONLY jar cite, no behavioral claim):
//
//	[VERIFIED javap net.minecraft.world.entity.monster.EnderMan.getCarriedBlock:
//	  entityData.get(DATA_CARRY_STATE).orElse(null) — returns the Optional<BlockState> or null
//	  (the not-carrying state). DATA_CARRY_STATE is registered in EnderMan's static{} as
//	  index 16 with EntityDataSerializers.OPTIONAL_BLOCK_STATE (id 15).
//	[VERIFIED javap net.minecraft.world.entity.monster.EnderMan.setCarriedBlock(BlockState):
//	  entityData.set(DATA_CARRY_STATE, Optional.ofNullable(state)) — Optional.ofNullable
//	  encodes the empty/present split (null -> empty Optional; non-null -> Optional.of).]
//
// IMPORTANT 1:1 NOTE: the vanilla 26.2 EnderMan does NOT push the carried block into MAINHAND
// equipment. The client renders the carried block ENTIRELY from the synched DATA_CARRY_STATE.
// The MAINHAND plumbing in this file + entity_equipment.go's detectEndermanCarryUpdates + the
// equipmentSpawnPackets enderman hook is a SULFUR-SIDE ADDITION (the audit-flagged expansion),
// kept strictly additive: enderman-gated at every reader/writer, so a non-enderman (the pig
// oracle) draws ZERO new RNG and TestPluginPigEqualsGoNativePig stays byte-identical.

import (
	"sync"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// carriedBlockSnapshot mirrors the carried state AT THE LAST detectEndermanCarryUpdates broadcast.
// Lives next to the carriedBlockState / carriedBlockSet fields in entity.go. Parallel to
// equipmentLastBroadcast (the equipment-layer snapshot) but kept SEPARATE because the carried
// MAINHAND surface is SYNTHETIC — it does NOT write e.equipment[MAINHAND], so the existing
// equipment snapshot is free for the equipment layer's use.
//
// On each tick, detectEndermanCarryUpdates diffs the live carriedBlockSet/carriedBlockState
// against this snapshot and broadcasts a single SetEquipment for MAINHAND on a change. The
// first call SEEDS the snapshot WITHOUT broadcasting (the spawn-time equipmentSpawnPackets
// path is the authoritative initial wire). The fields are enderman-gated: a non-enderman (the
// oracle pig) NEVER reaches this code path, so its RNG stream gains ZERO new draws.
//
//	[CITE EnderMan.setCarriedBlock / getCarriedBlock: vanilla does NOT broadcast a MAINHAND
//	 SetEquipment when the carried state changes — the MAINHAND surface here is a Sulfur-side
//	 visible-state addition (audit-flagged, see this file's header).]

// endermanCarryCountDefault is ItemStack.<init>(ItemLike) default Count == 1 (the jar: a fresh
// ItemStack carries ONE of its item). Mirrors itemStackOf(item.X) -> Count 1 (entity_equipment.go:133).
//
//	[VERIFIED javap ItemStack.<init>(ItemLike): this(item, 1) — single-count default.]
const endermanCarryCountDefault = 1

// itemIDByBlockNameOnce guards the lazy build of itemIDByBlockName (a "minecraft:<name>" ->
// item.ID map). The block.name == item.name default 1:1 map is built ONCE under sync.Once so
// a join never races the build (mirroring the itemIDByName pattern in persistence.go:139).
// The map covers all jar-generated items; a forged block name falls through to AIR.
var (
	itemIDByBlockNameOnce sync.Once
	itemIDByBlockName     map[string]item.ID
)

// carriedBlockItemID resolves a block resource id (block.Block.ID(), e.g. "minecraft:stone")
// to the item id the carried block's MAINHAND surface carries. The v1 default is a 1:1 map:
// the carried item's name == the block's name (most blocks have this item form: STONE ->
// minecraft:stone (id 1), DIRT -> minecraft:dirt (id 10)). The map is built lazily on first
// use from item.ByID with a "minecraft:" prefix. An unknown block name falls through to 0
// (Air) — the same "unknown id -> air" defensive return the persistence.go itemNameToID uses.
//
//	[CITE EnderMan.getCarriedBlock / setCarriedBlock: vanilla does NOT map a BlockState to an
//	 item form. The 1:1 block.name == item.name default here is a Sulfur-side stub for the
//	 audit's "held-item MAINHAND" expansion. Special overrides (vanilla has a few: REDSTONE_WIRE
//	 -> REDSTONE, COARSE_DIRT -> DIRT) defer to a future per-name map; v1 covers the common
//	 case by the lookup-miss -> 0 (Air) fall-through.]
func carriedBlockItemID(b block.Block) pk.VarInt {
	itemIDByBlockNameOnce.Do(func() {
		itemIDByBlockName = make(map[string]item.ID, len(item.ByID))
		for id, it := range item.ByID {
			itemIDByBlockName["minecraft:"+it.Name] = id
		}
	})
	if id, ok := itemIDByBlockName[b.ID()]; ok {
		return pk.VarInt(id)
	}
	return pk.VarInt(0) // unknown block: AIR (defensive — never reached for an EndermanHoldable block)
}

// carriedBlockItem returns the MAINHAND SlotData an enderman SHOULD hold while carrying the
// optional BlockState (the getCarriedBlock() result). Empty SlotData (Count==0) when not
// carrying or when the carried block lacks a 1:1 item form. The carried block's item form
// is resolved via carriedBlockItemID (the 1:1 block.name -> item.id map).
//
//	[CITE EnderMan.getCarriedBlock(): entityData.get(DATA_CARRY_STATE).orElse(null). The
//	 non-null get returns the carried BlockState; null maps to the EMPTY SlotData. Carrying
//	 a block with no resolvable item form yields EMPTY (a defensive guard for an unknown
//	 state id; never a real canUse outcome — EndermanTakeBlockGoal only takes HOLDABLE
//	 blocks, which are all resolved).]
func (e *Entity) carriedBlockItem() component.SlotData {
	if e == nil || !e.carriedBlockSet {
		return component.SlotData{} // getCarriedBlock() == null -> the EMPTY stack
	}
	sid := e.carriedBlockState
	if int(sid) < 0 || int(sid) >= len(block.StateList) {
		return component.SlotData{} // defensive: out-of-range state id -> EMPTY
	}
	id := carriedBlockItemID(block.StateList[sid])
	if id == 0 {
		return component.SlotData{} // unknown carried block: no item form -> silent (no SetEquipment)
	}
	return component.SlotData{Count: endermanCarryCountDefault, ItemID: id}
}
