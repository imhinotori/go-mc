package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// hoe.go -- HOE till, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.item.HoeItem.useOn). A player right-clicking a tillable block with a hoe
// converts it per the vanilla TILLABLES map and plays HOE_TILL (a cited no-op -- no sound bus for a
// block edit). This is a useOn-block action, so it hooks the handleUseItemOn (block) path BEFORE block
// placement -- a hoe is not a block item.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//	HoeItem.useOn(ctx):
//	    Pair<Predicate,Consumer> pair = TILLABLES.get(getBlockState(pos).getBlock());
//	    if (pair == null) return PASS;
//	    if (pair.getFirst().test(ctx)) {
//	        level.playSound(player, pos, HOE_TILL, BLOCKS, 1, 1);
//	        if (!level.isClientSide) { pair.getSecond().accept(ctx); if (player != null)
//	            getItemInHand().hurtAndBreak(1, player, getHand().asEquipmentSlot()); }
//	        return SUCCESS;
//	    }
//	    return PASS;
//
//	static TILLABLES = HashMap(ImmutableMap.of(
//	    GRASS_BLOCK,  Pair.of(onlyIfAirAbove, changeIntoState(FARMLAND.defaultBlockState())),
//	    DIRT_PATH,    Pair.of(onlyIfAirAbove, changeIntoState(FARMLAND.defaultBlockState())),
//	    DIRT,         Pair.of(onlyIfAirAbove, changeIntoState(FARMLAND.defaultBlockState())),
//	    COARSE_DIRT,  Pair.of(onlyIfAirAbove, changeIntoState(DIRT.defaultBlockState())),
//	    ROOTED_DIRT,  Pair.of(alwaysTrue,     changeIntoStateAndDropItem(DIRT.defaultBlockState(), HANGING_ROOTS))));
//	    // lambda for alwaysTrue: iconst_1 ireturn.
//
//	HoeItem.onlyIfAirAbove(ctx): getClickedFace() != DOWN && getLevel().getBlockState(getClickedPos().above()).isAir().
//	HoeItem.changeIntoState(state) -> level.setBlock(clickedPos, state, 11) + gameEvent(BLOCK_CHANGE).
//	HoeItem.changeIntoStateAndDropItem(state, item) -> setBlock(pos, state, 11) + gameEvent +
//	    Block.popResourceFromFace(level, pos, getClickedFace(), new ItemStack(item)).
//
// flag 11 == UPDATE_CLIENTS(2)|UPDATE_NEIGHBORS(1)|UPDATE_IMMEDIATE(8): mirrored as SetBlock +
// broadcastBlockUpdate (the flag-shape the crop/growth ports use).
//
// The consume of the hoe is DURABILITY (hurtAndBreak), not a stack shrink, and there is no item-durability
// subsystem in Sulfur yet -- so the hoe is neither shrunk nor damaged (a cited follow-up: hurtHeldItem is
// the seam). Till performs NO RNG draw, so the pig oracle levelRandom stream is unperturbed.

// isHoeItem reports whether an item id is any hoe tier (the tool tiers HoeItem spans:
// wooden/copper/stone/golden/iron/diamond/netherite). Vanilla dispatches HoeItem.useOn by the item Java
// class; Sulfur has no per-item class, so the tier set stands in for instanceof HoeItem. Tick-owned read.
func isHoeItem(itemID int32) bool {
	switch item.ID(itemID) {
	case item.WoodenHoe.ID, item.CopperHoe.ID, item.StoneHoe.ID, item.GoldenHoe.ID,
		item.IronHoe.ID, item.DiamondHoe.ID, item.NetheriteHoe.ID:
		return true
	}
	return false
}

// hoeTillResult is a TILLABLES entry collapsed to (onlyIfAirAbove flag, target block name, optional drop).
type hoeTillResult struct {
	onlyIfAirAbove bool
	target         string
	dropItem       item.ID // 0 == no drop
}

// hoeTillablesFor is TILLABLES.get(block) keyed by the clicked block id (getBlock()), ok=false for a
// non-tillable block (the null map entry -> PASS). CITE: HoeItem.TILLABLES.
func hoeTillablesFor(name string) (hoeTillResult, bool) {
	switch name {
	case "minecraft:grass_block", "minecraft:dirt_path", "minecraft:dirt":
		return hoeTillResult{onlyIfAirAbove: true, target: "minecraft:farmland"}, true
	case "minecraft:coarse_dirt":
		return hoeTillResult{onlyIfAirAbove: true, target: "minecraft:dirt"}, true
	case "minecraft:rooted_dirt":
		return hoeTillResult{onlyIfAirAbove: false, target: "minecraft:dirt", dropItem: item.HangingRoots.ID}, true
	}
	return hoeTillResult{}, false
}

// tryHoeTill is the HoeItem.useOn port, hooked in handleUseItemOn BEFORE block placement. `direction` is
// the clicked-face 3D-data value (needed by onlyIfAirAbove and popResourceFromFace). Returns true when the
// held item is a hoe AND the clicked block is in TILLABLES (the use is consumed -- a hoe never places a
// block, so a failed onlyIfAirAbove predicate and a SUCCESS till are both observably "no placement").
// Returns false when the held item is not a hoe OR the clicked block is not tillable, so placement
// continues (a no-op for the non-block hoe, matching vanilla PASS on the null lookup).
func (t *TickLoop) tryHoeTill(p *tickPlayer, inv *Inventory, held component.SlotData, pos pk.Position, direction int) bool {
	if slotIsEmpty(held) || !isHoeItem(int32(held.ItemID)) {
		return false // not a hoe -> PASS; placement continues
	}
	pmgr := t.dimWorld(p)
	pMinY := dimMinYFor(p.dimension)
	if pmgr == nil {
		return false
	}
	state, ok := pmgr.GetBlock(pos, pMinY)
	if !ok {
		return false // unloaded: treat as null map entry -> PASS
	}
	res, ok := hoeTillablesFor(blockNameForState(state))
	if !ok {
		return false // TILLABLES.get == null -> PASS
	}
	if !t.withinReach(p, pos) {
		return false
	}
	if res.onlyIfAirAbove && !t.hoeOnlyIfAirAbove(pmgr, pMinY, pos, direction) {
		return true // predicate failed: no till; the hoe consumed the interaction (no placement)
	}
	targetState, tok := block.DefaultStateID[res.target]
	if !tok {
		return true
	}
	t.withRegion(t.regionForColumn(columnOf(float64(pos.X)+0.5, float64(pos.Z)+0.5)), func() {
		if pmgr.SetBlock(pos, targetState, pMinY) {
			t.broadcastBlockUpdate(pos, targetState)
		}
		if res.dropItem != 0 {
			t.popResourceAt(int(pos.X), int(pos.Y), int(pos.Z),
				component.SlotData{Count: 1, ItemID: pk.VarInt(res.dropItem)})
		}
	})
	// hurtAndBreak(1, player, ...): DURABILITY, not a stack shrink. No item-durability subsystem yet -> the
	// hoe is neither shrunk nor damaged (cited follow-up). CITE: HoeItem.useOn hurtAndBreak.
	return true
}

// hoeOnlyIfAirAbove is HoeItem.onlyIfAirAbove(ctx): getClickedFace() != DOWN && the block ABOVE the
// clicked cell is air. `direction` is the clicked-face 3D-data value (0 == DOWN). CITE: HoeItem.onlyIfAirAbove.
func (t *TickLoop) hoeOnlyIfAirAbove(pmgr *world.ChunkManager, minY int, pos pk.Position, direction int) bool {
	if direction == 0 { // Direction.DOWN
		return false
	}
	above := pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	s, ok := pmgr.GetBlock(above, minY)
	if !ok {
		return false
	}
	return block.IsAir(s)
}

// blockNameForState resolves a state id to its block registry id ("minecraft:...") via the StateList
// type-switch layer (StateList[s].ID() == the block id). An out-of-range/unknown state yields "" (matched
// by no vanilla map key). This is the Sulfur stand-in for BlockState.getBlock() used as a map key.
func blockNameForState(s block.StateID) string {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return ""
	}
	b := block.StateList[s]
	if b == nil {
		return ""
	}
	return b.ID()
}
