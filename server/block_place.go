package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
)

// block_place.go — GAMEPLAY (Plan 17-17): the held-item -> placed-block resolution that
// replaces handleUseItemOn's hardcoded-stone v1 stand-in. This is the Go port of the vanilla
// chain Block.byItem(stack.getItem()) -> BlockItem.getBlock() -> Block.defaultBlockState(),
// plus BlockBehaviour.canBeReplaced (the replaceability gate that BlockPlaceContext.canPlace
// consults). Decompiled 1:1 from temp/cache/26.2-inner.jar this session:
//
//   net.minecraft.world.level.block.Block.byItem(Item):
//       if (item instanceof BlockItem bi) return bi.getBlock(); else return Blocks.AIR;
//   net.minecraft.world.item.BlockItem.getBlock(): return this.block;        // the bound Block
//   net.minecraft.world.item.BlockItem.getPlacementState -> Block.getStateForPlacement(ctx),
//       which for a plain block returns the block's defaultBlockState() (orientation/facing is
//       layered there for stateful blocks — the FAITHFUL extension point for v1).
//
// In Sulfur the BlockItem<->Block binding is encoded by a SHARED resource name: a block item is
// registered under the same "minecraft:<name>" id as its block (e.g. the `stone` item binds the
// `minecraft:stone` block). So Block.byItem is reproduced by keying block.FromID with the held
// item's name: a hit means the item is a BlockItem bound to that Block; a miss means the item is
// NOT a BlockItem (a sword, food, AIR, ...) and Block.byItem would have returned Blocks.AIR — i.e.
// nothing to place. block.FromID[name] yields the block's zero-value struct (all properties at
// their default), and block.ToStateID of that is exactly the block's defaultBlockState id.

// blockStateForItem ports Block.byItem(stack.getItem()) -> getPlacementState(default) for the
// held stack. It returns the default StateID of the Block bound to the held item, and ok=true,
// when the item is a block item; otherwise ok=false (the held item is empty/AIR or is not a
// BlockItem — placement is a no-op, matching Block.byItem returning Blocks.AIR).
//
// FAITHFUL flow (BlockItem.place -> getPlacementState -> Block.getStateForPlacement): v1 uses the
// plain defaultBlockState (block.FromID returns the zero-value/default-property block). Facing/
// orientation lives in Block.getStateForPlacement(ctx) — the documented extension point for
// stateful blocks (stairs, logs, doors) in a later plan; the default state is correct for the
// full-cube blocks v1 generates.
func blockStateForItem(stack component.SlotData) (block.StateID, bool) {
	// ItemStack.isEmpty(): a Count <= 0 stack carries no item. The faithful empty-hand guard —
	// an empty main hand resolves to AIR in vanilla and BlockItem.useOn is never reached, so
	// nothing is placed. THIS is the empty-hand-stone bugfix.
	if stack.Count <= 0 {
		return 0, false
	}
	it, ok := item.ByID[item.ID(stack.ItemID)]
	if !ok {
		return 0, false // unknown item id: treat as non-placeable (defensive)
	}
	// Block.byItem: only a BlockItem maps to a Block. The shared-name binding means a block item
	// resolves through block.FromID; a non-block item (no matching block id) is Blocks.AIR -> no
	// placement.
	name := "minecraft:" + it.Name
	b, ok := block.FromID[name]
	if !ok {
		return 0, false // not a BlockItem (Block.byItem -> Blocks.AIR)
	}
	// AIR is never a placeable result (Block.byItem -> Blocks.AIR is the "no block" sentinel).
	if block.IsAirBlock(b) {
		return 0, false
	}
	// getPlacementState defaults to Block.defaultBlockState() (registerDefaultState). The Go
	// ZERO-VALUE struct (block.FromID) is NOT a valid state for blocks whose default props are
	// non-zero — e.g. a chest's default facing is NORTH, but Chest{}'s Facing is the zero
	// Direction (down), which is not a registered chest state, so ToStateID would MISS and the
	// block would never place. DefaultStateID is the faithful defaultBlockState() id.
	if sid, ok := block.DefaultStateID[name]; ok {
		return sid, true
	}
	// Fallback for any block not in the default map: the zero-value state id (legacy path).
	sid, ok := block.ToStateID[b]
	if !ok {
		return 0, false // block has no registered state (should not happen for real blocks)
	}
	return sid, true
}

// isConsumableBlockItem reports whether the item is a BlockItem that ALSO carries a CONSUMABLE
// component -- the two vanilla `createBlockItemWithCustomItemName(block).food(...)` items whose
// item name differs from their block name (so blockStateForItem cannot resolve the block from the
// shared-name binding), and which therefore fall to BlockItem.useOn's CONSUMABLE-eat fallback when
// their place() fails:
//
//	sweet_berries -> Blocks.SWEET_BERRY_BUSH, .food(Foods.SWEET_BERRIES)
//	glow_berries  -> Blocks.CAVE_VINES,       .food(Foods.GLOW_BERRIES)
//
// (Verified in net.minecraft.world.item.Items via javap: both registered with
// createBlockItemWithCustomItemName + Item.Properties.food.) Every other consumable is a plain
// (non-block) Item whose default Item.useOn returns PASS -- it does NOT eat on the UseItemOn path,
// so it must NOT be included here. CITE BlockItem.useOn CONSUMABLE fallback + Items registration.
func isConsumableBlockItem(itemID int32) bool {
	switch item.ID(itemID) {
	case item.SweetBerries.ID, item.GlowBerries.ID:
		return true
	}
	return false
}

// isReplaceableState ports BlockState.canBeReplaced() — the per-state `replaceable` material
// flag that BlockBehaviour.canBeReplaced(state, ctx) consults (decompiled this session):
//
//   BlockBehaviour.canBeReplaced(state, ctx):
//       return state.canBeReplaced()                                   // the `replaceable` field
//           && (ctx.getItemInHand().isEmpty()
//               || !ctx.getItemInHand().is(this.asItem()));            // not the same block
//
// The `replaceable` material flag is set on a small vanilla set: AIR (all three variants) and the
// fluids WATER and LAVA are the members Sulfur's world actually produces and that v1 placement can
// land into. Other vanilla replaceables (short grass, ferns, snow_layer, fire, ...) are not part
// of the v1 world, so this set is the faithful subset for the blocks that exist. Stateful blocks
// (a placed full cube like stone/dirt/planks) are NOT replaceable, so a click on a solid target
// places on the adjacent face, never inside the solid.
func isReplaceableState(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	switch block.StateList[s].(type) {
	case block.Air, block.CaveAir, block.VoidAir, block.Water, block.Lava:
		return true
	default:
		return false
	}
}
