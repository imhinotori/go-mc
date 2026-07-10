package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shovel.go -- SHOVEL flatten (dirt-path) + campfire dowse, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.item.ShovelItem.useOn). A player right-clicking a flattenable block with a shovel
// converts it to dirt_path (if the clicked face != DOWN and the block above is air), and right-clicking a
// LIT campfire dowses it (LIT=false). This is a useOn-block action, so it hooks the handleUseItemOn (block)
// path BEFORE block placement -- a shovel is not a block item.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//	ShovelItem.useOn(ctx):
//	    state = getBlockState(pos);
//	    if (getClickedFace() == DOWN) return PASS;
//	    BlockState flattened = FLATTENABLES.get(state.getBlock());  // (getBlockState above)
//	    BlockState newState = null;
//	    if (flattened != null && getBlockState(pos.above()).isAir()) {
//	        playSound(player, pos, SHOVEL_FLATTEN, BLOCKS, 1, 1); newState = flattened;
//	    } else if (state.getBlock() instanceof CampfireBlock && state.getValue(LIT)) {
//	        if (!isClientSide) levelEvent(null, 1009, pos, 0);
//	        CampfireBlock.dowse(getPlayer(), level, pos, state);
//	        newState = state.setValue(LIT, false);
//	    }
//	    if (newState != null) {
//	        if (!isClientSide) { setBlock(pos, newState, 11); gameEvent(BLOCK_CHANGE); if (player != null)
//	            getItemInHand().hurtAndBreak(1, player, getHand().asEquipmentSlot()); }
//	        return SUCCESS;
//	    }
//	    return PASS;
//
//	static FLATTENABLES = HashMap(ImmutableMap.builder()
//	    .put(GRASS_BLOCK, DIRT_PATH.defaultBlockState())
//	    .put(DIRT,        DIRT_PATH.defaultBlockState())
//	    .put(PODZOL,      DIRT_PATH.defaultBlockState())
//	    .put(COARSE_DIRT, DIRT_PATH.defaultBlockState())
//	    .put(MYCELIUM,    DIRT_PATH.defaultBlockState())
//	    .put(ROOTED_DIRT, DIRT_PATH.defaultBlockState()).build());
//
// flag 11 == UPDATE_CLIENTS(2)|UPDATE_NEIGHBORS(1)|UPDATE_IMMEDIATE(8): mirrored as SetBlock +
// broadcastBlockUpdate. dowse (client particles + gameEvent) and playSound / levelEvent(1009) are cited
// no-ops (no sound/particle/gameEvent bus for a block edit); the observable LIT=false transition is the
// setBlock. hurtAndBreak is DURABILITY -- no item-durability subsystem yet (cited follow-up).
//
// Flatten/dowse perform NO RNG draw, so the pig oracle levelRandom stream is unperturbed.

// isShovelItem reports whether an item id is any shovel tier (ShovelItem spans
// wooden/copper/stone/golden/iron/diamond/netherite). Stand-in for instanceof ShovelItem. Tick-owned read.
func isShovelItem(itemID int32) bool {
	switch item.ID(itemID) {
	case item.WoodenShovel.ID, item.CopperShovel.ID, item.StoneShovel.ID, item.GoldenShovel.ID,
		item.IronShovel.ID, item.DiamondShovel.ID, item.NetheriteShovel.ID:
		return true
	}
	return false
}

// shovelFlattenable is FLATTENABLES.get(block) keyed by the clicked block id: all six dirt-family blocks
// map to dirt_path (default state). ok=false for any other block. CITE: ShovelItem.FLATTENABLES.
func shovelFlattenable(name string) bool {
	switch name {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:podzol",
		"minecraft:coarse_dirt", "minecraft:mycelium", "minecraft:rooted_dirt":
		return true
	}
	return false
}

// tryShovelPath is the ShovelItem.useOn port, hooked in handleUseItemOn BEFORE block placement. `direction`
// is the clicked-face 3D-data value (0 == DOWN -> PASS). Returns true when the held item is a shovel AND the
// clicked block yields a new state (flattened dirt_path, or a dowsed campfire) -- the use consumed the
// action. Returns false when the held item is not a shovel, the clicked face is DOWN, or no FLATTENABLES/
// campfire match applies (then placement continues -- a no-op for the non-block shovel, matching PASS).
func (t *TickLoop) tryShovelPath(p *tickPlayer, inv *Inventory, held component.SlotData, pos pk.Position, direction int) bool {
	if slotIsEmpty(held) || !isShovelItem(int32(held.ItemID)) {
		return false // not a shovel -> PASS
	}
	if direction == 0 { // getClickedFace() == DOWN -> PASS
		return false
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
	if !t.withinReach(p, pos) {
		return false
	}

	name := blockNameForState(state)
	var newState block.StateID
	haveNew := false

	// FLATTENABLES branch: a dirt-family block with air above flattens to dirt_path.
	if shovelFlattenable(name) {
		if above, aok := pmgr.GetBlock(pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}, pMinY); aok && block.IsAir(above) {
			if dp, dok := block.DefaultStateID["minecraft:dirt_path"]; dok {
				newState, haveNew = dp, true
			}
		}
	}
	// CAMPFIRE dowse branch (only when the FLATTENABLES branch did not fire): a LIT campfire -> LIT=false.
	if !haveNew && block.IsLitCampfire(state) {
		if ds, dok := shovelDowsedCampfire(state); dok {
			newState, haveNew = ds, true
		}
	}
	if !haveNew {
		// Neither branch matched: vanilla returns PASS. A shovel never places a block, so falling through
		// vs. returning here is observably identical; return false so placement (a no-op) continues.
		return false
	}

	t.withRegion(t.regionForColumn(columnOf(float64(pos.X)+0.5, float64(pos.Z)+0.5)), func() {
		if pmgr.SetBlock(pos, newState, pMinY) {
			t.broadcastBlockUpdate(pos, newState)
		}
	})
	// hurtAndBreak(1, player, ...): DURABILITY, not a stack shrink -- no item-durability subsystem yet.
	return true
}

// shovelDowsedCampfire returns the state.setValue(LIT, false) of a lit Campfire/SoulCampfire, preserving
// facing/signal_fire/waterlogged. ok=false for a non-campfire state. CITE: ShovelItem.useOn (LIT=false).
func shovelDowsedCampfire(s block.StateID) (block.StateID, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return 0, false
	}
	switch c := block.StateList[s].(type) {
	case block.Campfire:
		c.Lit = false
		if sid, ok := block.ToStateID[c]; ok {
			return sid, true
		}
	case block.SoulCampfire:
		c.Lit = false
		if sid, ok := block.ToStateID[c]; ok {
			return sid, true
		}
	}
	return 0, false
}

