package server

import (
	"bytes"
	"math/rand/v2"

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

// itemEntityHalfHeight is Block.popResource's local `d`: EntityType.ITEM.getHeight() / 2.0.
// entity.Item.Height is 0.25, so d == 0.125 — the Y offset popResource subtracts so the spawned
// item sits centered on the block's lower-face rather than the block center. A var (not const)
// because entity.Item.Height is a generated struct FIELD, not a compile-time constant.
//   [VERIFIED javap: Block.popResource → ITEM.getHeight() f2d / 2.0; entity.Item.Height==0.25.]
var itemEntityHalfHeight = entity.Item.Height / 2.0 // 0.125

// itemSpawnJitter is the ±range Block.popResource applies to each spawn axis via
// Mth.nextDouble(random, -0.25, 0.25). Decompiled verbatim (javap Block.popResource: ldc2_w
// -0.25d / 0.25d on all three axes).
const itemSpawnJitter = 0.25

// itemTossVelocity is the per-axis toss magnitude in ItemEntity.<init>: each component is
// random.nextDouble()*0.2 - 0.1, i.e. uniform in [-0.1, 0.1). Decompiled verbatim
// (javap ItemEntity.<init>: nextDouble dmul 0.2d dsub 0.1d, three times).
const itemTossVelocity = 0.1

// mthNextDouble ports net.minecraft.util.Mth.nextDouble(RandomSource, double, double):
// returns lo when lo >= hi (the degenerate guard), else rng.nextDouble()*(hi-lo) + lo. The Go
// math/rand/v2 rand.Float64() is the RandomSource.nextDouble() analogue (uniform [0,1)).
//   [VERIFIED javap Mth.nextDouble: dcmpl; iflt → return lo; else nextDouble * (hi-lo) + lo.]
func mthNextDouble(lo, hi float64) float64 {
	if lo >= hi {
		return lo
	}
	return rand.Float64()*(hi-lo) + lo
}

// spawnBlockDrop spawns the dropped Item entity for a just-broken block and adds it to the
// tick-owned store, where the GAMEPLAY-01 tracker broadcasts it next tick. brokenState is the
// block state read BEFORE SetBlock wrote air (block_interact.go captures it). Tick-owned
// (TICK-05): runs on the tick goroutine.
//
// GATE (Plan 17-14 FIX E — ServerPlayerGameMode.destroyBlock): a CREATIVE player's break drops
// NOTHING. v1 hardcodes survival so the gate always passes today, but the check is present and
// correct so a future creative toggle drops nothing for free. A block with no v1 drop
// (air/unknown) is likewise a no-op.
//
// POSITION + VELOCITY (Plan 17-14 FIX A/B — Block.popResource + ItemEntity.<init>, ported
// verbatim from temp/cache/26.2-inner.jar):
//
//	double d = ITEM.getHeight()/2.0;                         // 0.125
//	x = pos.getX()+0.5 + Mth.nextDouble(rng, -0.25, 0.25);
//	y = pos.getY()+0.5 + Mth.nextDouble(rng, -0.25, 0.25) - d;
//	z = pos.getZ()+0.5 + Mth.nextDouble(rng, -0.25, 0.25);
//	setDeltaMovement(rng.nextDouble()*0.2-0.1, ...y, ...z);  // random toss in [-0.1,0.1)
//	item.setDefaultPickUpDelay();                            // pickupDelay = 10
func (t *TickLoop) spawnBlockDrop(p *tickPlayer, pos pk.Position, brokenState block.StateID) {
	// FIX E — creative drops nothing (ServerPlayerGameMode.destroyBlock).
	if p != nil && p.gameMode == gameModeCreative {
		return
	}

	drop, ok := blockDropFor(brokenState)
	if !ok {
		return // no drop for this block (air/unknown)
	}

	// FIX A — Block.popResource spawn position: block center + per-axis ±0.25 jitter, with the
	// Y additionally offset down by the item's half-height (d == 0.125) so it rests on the
	// lower face. Each axis draws an INDEPENDENT Mth.nextDouble(-0.25, 0.25) (three draws).
	x := float64(pos.X) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
	y := float64(pos.Y) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter) - itemEntityHalfHeight
	z := float64(pos.Z) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)

	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, drop)

	t.entities.add(ie) // store insert -> the tracker broadcasts AddEntity + SetEntityData
}

// NewItemEntity constructs a dropped Item entity at (x,y,z) carrying stack — the Go port of
// net.minecraft.world.entity.item.ItemEntity.<init>(Level, double, double, double, ItemStack).
// It mirrors the vanilla constructor's two side effects: a random toss velocity
// (setDeltaMovement(nextDouble()*0.2-0.1, ...) per axis) and the ITEM render metadata, plus
// Block.popResource's setDefaultPickUpDelay() (pickupDelay = 10). age starts at DEFAULT_AGE (0).
// Tick-owned (called only on the tick goroutine).
func NewItemEntity(id int32, x, y, z float64, stack component.SlotData) *Entity {
	ie := NewEntity(id, entity.Item, x, y, z)
	ie.isItem = true
	ie.itemStack = stack

	// FIX B — ItemEntity.<init> setDeltaMovement: a random toss in [-0.1, 0.1) per axis
	// (rng.nextDouble()*0.2 - 0.1). Three independent draws.
	ie.vx = rand.Float64()*2*itemTossVelocity - itemTossVelocity
	ie.vy = rand.Float64()*2*itemTossVelocity - itemTossVelocity
	ie.vz = rand.Float64()*2*itemTossVelocity - itemTossVelocity

	// Block.popResource → item.setDefaultPickUpDelay() == 10 ticks (the freshly-dropped item is
	// not pickable until the item tick counts this down to 0).
	ie.pickupDelay = itemDefaultPickupDelay

	// Populate the ITEM metadata (the Pitfall-5 fix). encodeSetEntityData splices
	// Entity.metadata VERBATIM (already in DataValue framing) and appends the 0xFF
	// terminator itself — so metadata must hold ONLY the entry bytes, no terminator.
	ie.metadata = encodeItemMetadata(stack)
	return ie
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
