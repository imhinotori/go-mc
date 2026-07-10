package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shears.go -- SHEARS on a growing-plant HEAD (cave_vines/weeping_vines/twisting_vines), ported 1:1 from
// the unobfuscated 26.2 jar (net.minecraft.world.item.ShearsItem.useOn). Shearing a non-max-age vine head
// snaps it to MAX_AGE (AGE=25) so it stops growing. This is a useOn-block action, so it hooks the
// handleUseItemOn (block) path BEFORE block placement -- shears is not a block item.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//	ShearsItem.useOn(ctx):
//	    state = getBlockState(pos); block = state.getBlock();
//	    if (block instanceof GrowingPlantHeadBlock head && !head.isMaxAge(state)) {
//	        ... (advancement trigger) ...
//	        level.playSound(player, pos, GROWING_PLANT_CROP, BLOCKS, 1, 1);
//	        BlockState maxed = head.getMaxAgeState(state);
//	        level.setBlockAndUpdate(pos, maxed);              // flag 3 (UPDATE_ALL)
//	        gameEvent(BLOCK_CHANGE);
//	        if (player != null) getItemInHand().hurtAndBreak(1, player, getHand().asEquipmentSlot());
//	        return SUCCESS;
//	    }
//	    return super.useOn(ctx);   // PASS (the other shears interactions -- pumpkin carve, beehive,
//	                               // tripwire -- are the BLOCK's useItemOn, not the item's useOn).
//
// setBlockAndUpdate == setBlock(pos, state, 3): flag 3 == UPDATE_CLIENTS(2)|UPDATE_NEIGHBORS(1), mirrored
// as SetBlock + broadcastBlockUpdate. playSound / gameEvent are cited no-ops. hurtAndBreak is DURABILITY --
// no item-durability subsystem (cited follow-up). No RNG draw -- the pig oracle levelRandom is unperturbed.
//
// NOTE: the pumpkin->carved_pumpkin carve, beehive/bee_nest shear (honeycomb drop), and tripwire cut are
// BLOCK-side useItemOn interactions (PumpkinBlock/BeehiveBlock/TripWireBlock.useItemOn), reached at
// ServerPlayerGameMode.useItemOn step 1 (useBlockInteraction), NOT the shears item's useOn -- they are
// separate ports (cited follow-ups). Leaf/wool "drop when sheared" is the break-path (ShearsItem.mineBlock
// + loot), also separate.

// tryShearsUseOn is the ShearsItem.useOn port, hooked in handleUseItemOn BEFORE block placement. Returns
// true when the held item is shears AND the clicked block is a non-max-age growing-plant head (the use
// consumed the action). Returns false when the held item is not shears OR the clicked block is not a
// sub-max-age vine head (then placement continues -- a no-op for the non-block shears, matching PASS).
func (t *TickLoop) tryShearsUseOn(p *tickPlayer, inv *Inventory, held component.SlotData, pos pk.Position) bool {
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.Shears.ID) {
		return false // not shears -> PASS
	}
	pmgr := t.dimWorld(p)
	pMinY := dimMinYFor(p.dimension)
	if pmgr == nil {
		return false
	}
	state, ok := pmgr.GetBlock(pos, pMinY)
	if !ok {
		return false
	}
	// block instanceof GrowingPlantHeadBlock && !isMaxAge(state).
	age := block.GrowingPlantHeadAge(state)
	if age < 0 || age >= 25 { // not a head, or already MAX_AGE (25) -> super.useOn == PASS
		return false
	}
	if !t.withinReach(p, pos) {
		return false
	}
	maxed, ok := block.GrowingPlantHeadMaxAgeState(state)
	if !ok {
		return false
	}
	t.withRegion(t.regionForColumn(columnOf(float64(pos.X)+0.5, float64(pos.Z)+0.5)), func() {
		if pmgr.SetBlock(pos, maxed, pMinY) {
			t.broadcastBlockUpdate(pos, maxed)
		}
	})
	// hurtAndBreak(1, player, ...): DURABILITY, not a stack shrink -- no item-durability subsystem yet.
	return true
}
