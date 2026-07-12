package server

import (
	"bytes"
	"math/rand/v2"
	"strings"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/loot"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_drop.go — GAMEPLAY-06: spawn a pickable Item entity when a block is broken.
//
// handlePlayerAction (block_interact.go) sets the broken block to air, acks, and broadcasts
// BlockUpdate — then returns. This file adds the missing drop: after a successful break it
// rolls the block's loot table through the SHARED level/loot evaluator (loot.Roll over
// minecraft:blocks/<name>), spawns an entity.Item (ID 71) per rolled stack at the block
// center carrying the ITEM data-value (so it renders, not invisible — 06-RESEARCH Pitfall 5),
// and inserts each into the tick-owned entity store. The GAMEPLAY-01 tracker (tracker.go)
// then broadcasts ClientboundAddEntity + ClientboundSetEntityData to nearby clients on the
// next tick WITHOUT any new tracker code — the Item rides the same store-add path a mob does.
//
// SHARED EVALUATOR (STRUCT-POLISH-01 / 20-02): the v1 hardcoded blockDropTable map is GONE —
// drops now route through the single level/loot.Roll evaluator over the embedded
// minecraft:blocks/<name> tables (the same evaluator structure chests use). A v1 HAND break
// carries no tool and no explosion, so the block tables take their faithful no-tool defaults:
// match_tool's silk_touch alternative fails -> the survives_explosion child drops (stone ->
// cobblestone, grass_block -> dirt), and apply_bonus (fortune) / explosion_decay no-op. When
// the dig path later threads a held tool through the LootContext, silk-touch + fortune become
// real reads with NO change here (the context fields are cited stubs).
//
// All of this runs on the TICK goroutine (handlePlayerAction is drained by applyInput on the
// tick owner), so the store insert and id allocation are TICK-05 safe.

// blockTableName maps a broken block state to its loot-table NAME (the path under
// minecraft:blocks/, i.e. the Block.ID() with the namespace stripped — "minecraft:stone" ->
// "stone"). An air block (or an out-of-range state) returns ok=false (no table).
func blockTableName(broken block.StateID) (string, bool) {
	if int(broken) < 0 || int(broken) >= len(block.StateList) {
		return "", false
	}
	b := block.StateList[broken]
	if block.IsAirBlock(b) {
		return "", false // air drops nothing
	}
	id := b.ID()
	if i := strings.IndexByte(id, ':'); i >= 0 {
		id = id[i+1:]
	}
	return id, true
}

// blockDropsFor rolls a broken block's loot table through the shared level/loot evaluator and
// returns the rolled drop stacks (one entry per stack a vanilla break yields). seed is the
// block-break RNG seed (the per-break math/rand/v2 draw — see spawnBlockDrop): it seeds the
// loot LegacyRandomSource so the count-rolling functions (set_count) draw deterministically
// per break. An air/unknown block, or a block with no embedded loot table, returns nil (no
// drop — a no-op). The v1 LootContext carries no tool/explosion, so the block tables take
// their faithful no-tool defaults (cobblestone over the silk-touch alternative, base count).
//
// Source: STRUCT-POLISH-01 evaluator (level/loot.Roll over minecraft:blocks/<name>).
func blockDropsFor(broken block.StateID, seed int64, ctx *loot.LootContext) []component.SlotData {
	name, ok := blockTableName(broken)
	if !ok {
		return nil
	}
	tbl, err := loot.LoadTable("minecraft:blocks/" + name)
	if err != nil {
		// No embedded loot table for this block (e.g. a block whose table the datagen
		// tree omits) -> no drop. The vendored tree carries ~1100 block tables; a miss is
		// a block with no drop or an as-yet-unported one. No-op (never panic).
		return nil
	}
	return loot.Roll(tbl, seed, ctx)
}

// blockBreakLootContext builds the LootContext for a block-break roll, threading the breaking
// player's held TOOL into the LootContextParams.TOOL the block tables read — the port of the
// LootParams.Builder(...).withParameter(TOOL, tool) the block-drop path supplies
// (ServerPlayerGameMode.destroyBlock -> Block.getDrops(state, level, pos, be, player, tool)).
//
// A nil player (a support-cascade / piston / fluid break — the vanilla destroyBlock null-entity arg)
// supplies NO tool: HasTool stays false, so match_tool (silk_touch) fails and apply_bonus (fortune)
// no-ops — the faithful "broken by no one with no tool" default (e.g. a piston pushing an ore drops
// the base 1). A player break reads the held item's enchantments SERVER-side (never from a packet —
// the tryMilkCow/heldWindowSlot precedent, T-32/36-04) via stackEnchantments, so a forged client held
// item cannot fake silk-touch/fortune.
//
// Source: javap ServerPlayerGameMode.destroyBlock (getMainHandItem tool) + Block.getDrops ->
// LootParams.Builder.withParameter(LootContextParams.TOOL); EnchantmentHelper.getItemEnchantmentLevel.
func blockBreakLootContext(p *tickPlayer, seed int64) *loot.LootContext {
	ctx := loot.NewLootContext(seed, 0)
	if p == nil {
		return ctx // no breaker -> no TOOL param (HasTool false: match_tool fails, apply_bonus no-ops).
	}
	// getMainHandItem(): the held tool, read SERVER-side (inv.get(heldWindowSlot(heldSlot))).
	tool := playerItemBySlot(p, eqSlotMainHand)
	// LootContextParams.TOOL is set even for a bare/empty hand in vanilla (the fist is a "tool" whose
	// enchant levels are all 0), so match_tool's `TOOL == null` guard passes (HasTool true) and its
	// silk_touch level>=1 predicate then fails on the 0-level fist — the faithful bare-hand result.
	ctx.HasTool = true
	if stackEmpty(tool) {
		return ctx // empty hand: HasTool true, no enchantments (silk/fortune level 0).
	}
	// EnchantmentHelper.getItemEnchantmentLevel(SILK_TOUCH/FORTUNE, tool): read the tool's enchant map
	// SERVER-side. stackEnchantments returns the resource-id -> level map off the ENCHANTMENTS component.
	ench := stackEnchantments(tool)
	if len(ench) > 0 {
		ctx.ToolEnchantments = ench
		ctx.ToolSilkTouch = ench["minecraft:silk_touch"] >= 1
		ctx.ToolFortuneLevel = ench["minecraft:fortune"]
	}
	return ctx
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

	// Roll the block's loot table through the shared evaluator. The per-break loot seed is a
	// fresh math/rand/v2 draw (the existing block-break RNG path) — it seeds the loot
	// LegacyRandomSource so set_count etc. draw deterministically for this break. A block with
	// no drop (air/unknown/no-table) yields an empty list -> no-op.
	lootSeed := rand.Int64()
	// Thread the breaking player's held TOOL into the loot context (silk_touch / fortune reads). A nil
	// player (support-cascade/piston/fluid break) supplies no tool -> the faithful no-tool default.
	drops := blockDropsFor(brokenState, lootSeed, blockBreakLootContext(p, lootSeed))
	if len(drops) == 0 {
		return // no drop for this block (air/unknown/no-table)
	}

	// Spawn one Item entity per rolled stack (Block.popResource per drop). Each draws its OWN
	// position jitter + toss velocity (the existing per-item draw order is preserved per stack).
	for _, drop := range drops {
		if drop.Count <= 0 {
			continue // an explosion_decay'd-to-zero stack (never in a v1 break) — skip.
		}
		// FIX A — Block.popResource spawn position: block center + per-axis ±0.25 jitter, with
		// the Y additionally offset down by the item's half-height (d == 0.125) so it rests on
		// the lower face. Each axis draws an INDEPENDENT Mth.nextDouble(-0.25, 0.25) (three draws).
		x := float64(pos.X) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)
		y := float64(pos.Y) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter) - itemEntityHalfHeight
		z := float64(pos.Z) + 0.5 + mthNextDouble(-itemSpawnJitter, itemSpawnJitter)

		ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, drop)

		t.cur().entities.add(ie) // store insert -> the tracker broadcasts AddEntity + SetEntityData
	}
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
	// ItemEntity.<init> sets health = DEFAULT_HEALTH (5); it is persisted as putShort "Health".
	ie.health = itemEntityDefaultHealth

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
