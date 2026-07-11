package server

// menu_stillvalid.go -- the per-tick ServerPlayer.tick container-validity sweep. Vanilla ServerPlayer.tick
// runs, every tick, right after containerMenu.broadcastChanges():
//
//	if (!this.containerMenu.stillValid(this)) { this.closeContainer(); this.containerMenu = this.inventoryMenu; }
//
// Without it, an open menu SURVIVES teleporting away / walking out of range / breaking the backing block --
// a hacked client keeps sending ContainerClick on that windowId and interacts with a chest (or furnace,
// hopper, ...) from ANY distance (SECURITY: remote container access + item exfiltration). This ports the
// per-tick stillValid check + the auto-close it drives.
//
// stillValid per menu (VERIFIED javap this session, temp/cache/26.2-inner.jar):
//   - a BLOCK-backed menu with a ContainerLevelAccess (anvil/enchant/grindstone/smithing/loom + the block
//     containers that use it): AbstractContainerMenu.stillValid(access, player, block) ==
//     level.getBlockState(pos).is(block) && player.isWithinBlockInteractionRange(pos, 4.0).
//   - a BLOCK-ENTITY container (chest/furnace/dispenser/hopper/beacon/brewing/shulker/ender_chest, whose
//     menu delegates to Container.stillValid): Container.stillValidBlockEntity(be, player, 4.0) ==
//     level != null && level.getBlockEntity(pos) == be && player.isWithinBlockInteractionRange(pos, 4.0).
//     Ender's block containers are keyed by world position (one BE per cell), so "the BE at pos is still
//     this container" reduces to "the block at pos is still the right block" -- identical observable gate.
//   - MERCHANT: AbstractVillager.stillValid == getTradingPlayer()==player && isAlive() &&
//     player.isWithinEntityInteractionRange(villager, 4.0).
//   - a CONTAINER ENTITY (chest/hopper minecart, chest boat): ContainerEntity.isChestVehicleStillValid ==
//     !isRemoved() && player.isWithinEntityInteractionRange(entity.getBoundingBox(), 4.0).
//
// isWithinBlockInteractionRange(pos, eps=4.0): let range = BLOCK_INTERACTION_RANGE(default 4.5) + eps = 8.5;
// AABB(pos).distanceToSqr(getEyePosition()) < range*range (== 72.25). isWithinEntityInteractionRange(box,
// eps=4.0): range = ENTITY_INTERACTION_RANGE(default 3.0) + eps = 7.0; box.distanceToSqr(eye) < 49.0.
// AABB(BlockPos) is the unit cube [x,x+1]x[y,y+1]x[z,z+1]; distanceToSqr clamps the point per-axis to the
// box and sums the squared deltas (AABB.distanceToSqr(Vec3)).
//
// PIG-ORACLE SAFETY (TestPluginPigEqualsGoNativePig): this sweep draws ZERO RNG and only touches players
// with a non-nil openContainer. The oracle pig is a mob (no openContainer), so sweepContainerStillValid
// short-circuits at the nil check for every non-menu entity -- the pinned per-mob RNG stream is untouched.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// The reach constants (VERIFIED javap: RangedAttribute defaults + the fixed +4.0 epsilon the stillValid
// callers pass). blockReachSq == (4.5 + 4.0)^2; entityReachSq == (3.0 + 4.0)^2.
const (
	blockInteractionRangeDefault  = 4.5
	entityInteractionRangeDefault = 3.0
	menuReachEpsilon              = 4.0
	blockReachSq                  = (blockInteractionRangeDefault + menuReachEpsilon) * (blockInteractionRangeDefault + menuReachEpsilon)
	entityReachSq                 = (entityInteractionRangeDefault + menuReachEpsilon) * (entityInteractionRangeDefault + menuReachEpsilon)
)

// NOTE: getEyePosition() == (x, y + playerStandingEyeHeight, z); playerStandingEyeHeight (1.62, the
// standing Player.getEyeHeight()) is declared in breath.go and reused here for the reach eye anchor.

// clampAxisDist ports the per-axis term of AABB.distanceToSqr(Vec3): max(min - p, p - max, 0.0).
func clampAxisDist(min, max, p float64) float64 {
	d := min - p
	if e := p - max; e > d {
		d = e
	}
	if d < 0 {
		d = 0
	}
	return d
}

// blockAabbDistToEyeSq ports AABB(pos).distanceToSqr(player.getEyePosition()) for the unit block cube at
// pos. The eye is (x, y + eyeHeight, z).
func blockAabbDistToEyeSq(p *tickPlayer, pos pk.Position) float64 {
	ex := p.x
	ey := p.y + playerStandingEyeHeight
	ez := p.z
	minX, minY, minZ := float64(pos.X), float64(pos.Y), float64(pos.Z)
	dx := clampAxisDist(minX, minX+1, ex)
	dy := clampAxisDist(minY, minY+1, ey)
	dz := clampAxisDist(minZ, minZ+1, ez)
	return dx*dx + dy*dy + dz*dz
}

// entityAabbDistToEyeSq ports entity.getBoundingBox().distanceToSqr(player.getEyePosition()). The entity
// box is centered horizontally on (e.x, e.z) with half-width e.width/2, vertical [e.y, e.y+e.height]
// (Entity.makeBoundingBox / getBoundingBox).
func entityAabbDistToEyeSq(p *tickPlayer, e *Entity) float64 {
	ex := p.x
	ey := p.y + playerStandingEyeHeight
	ez := p.z
	hw := e.width / 2
	dx := clampAxisDist(e.x-hw, e.x+hw, ex)
	dy := clampAxisDist(e.y, e.y+e.height, ey)
	dz := clampAxisDist(e.z-hw, e.z+hw, ez)
	return dx*dx + dy*dy + dz*dz
}

// blockStillValidAt ports AbstractContainerMenu.stillValid(access, player, block) / the
// Container.stillValidBlockEntity gate, unified: the block at pos still satisfies isBlock AND the player
// is within block-interaction range of pos. A nil world fails (like stillValidBlockEntity's level==null).
func (t *TickLoop) blockStillValidAt(p *tickPlayer, pos pk.Position, isBlock func(block.StateID) bool) bool {
	if t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	if !isBlock(state) {
		return false // level.getBlockState(pos).is(block) == false: the block was broken/replaced
	}
	return blockAabbDistToEyeSq(p, pos) < blockReachSq
}

// isEnderChestBlock reports whether a block state is an ender_chest (EnderChestBlock). CITE
// EnderChestBlock instanceof. (No package predicate exists; matched by registry id like isChestBlock.)
func isEnderChestBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:ender_chest"
}

// menuStillValid ports the per-menu stillValid(player) for the player currently-open non-inventory
// window. It is the exact gate ServerPlayer.tick tests each tick. A nil openContainer is trivially valid
// (window 0, the InventoryMenu, is always stillValid). CITE the per-menu stillValid overrides.
func (t *TickLoop) menuStillValid(p *tickPlayer) bool {
	oc := p.openContainer
	if oc == nil {
		return true // only the InventoryMenu (window 0) is open; InventoryMenu.stillValid == true
	}
	switch oc.kind {
	case containerKindChest:
		return t.blockStillValidAt(p, oc.chestPos, isChestBlock)
	case containerKindFurnace:
		return t.blockStillValidAt(p, oc.furnacePos, isAnyFurnaceBlock)
	case containerKindDispenser:
		return t.blockStillValidAt(p, oc.dispenserPos, block.IsDispenserFamily)
	case containerKindHopper:
		return t.blockStillValidAt(p, oc.hopperPos, block.IsHopper)
	case containerKindBeacon:
		return t.blockStillValidAt(p, oc.beaconPos, isBeaconBlock)
	case containerKindBrewingStand:
		return t.blockStillValidAt(p, oc.brewingStandPos, isBrewingStandBlock)
	case containerKindShulker:
		return t.blockStillValidAt(p, oc.shulkerPos, block.IsShulkerBox)
	case containerKindEnderChest:
		return t.blockStillValidAt(p, oc.enderChestPos, isEnderChestBlock)
	case containerKindAnvil:
		return t.blockStillValidAt(p, oc.anvilPos, isAnvilBlock)
	case containerKindEnchant:
		return t.blockStillValidAt(p, oc.enchantPos, isEnchantingTableBlock)
	case containerKindGrindstone:
		return t.blockStillValidAt(p, oc.grindPos, isGrindstoneBlock)
	case containerKindSmithing:
		return t.blockStillValidAt(p, oc.smithPos, isSmithingTableBlock)
	case containerKindLoom:
		return t.blockStillValidAt(p, oc.loomPos, isLoomBlock)
	case containerKindMerchant:
		// AbstractVillager.stillValid: getTradingPlayer()==player and isAlive() and
		// isWithinEntityInteractionRange(villager, 4.0). The villager is re-resolved by its thin id.
		v := t.entityByIDAnyRegion(oc.merchantVillagerID)
		if v == nil || v.dead {
			return false
		}
		if v.villagerTradingPlayer != p.entityID {
			return false // getTradingPlayer() != this player
		}
		return entityAabbDistToEyeSq(p, v) < entityReachSq
	case containerKindMinecartChest:
		// ContainerEntity.isChestVehicleStillValid: not removed and
		// isWithinEntityInteractionRange(entity.getBoundingBox(), 4.0). The cart is re-resolved by id.
		c := t.entityByIDAnyRegion(oc.minecartEntityID)
		if c == nil || c.dead {
			return false
		}
		return entityAabbDistToEyeSq(p, c) < entityReachSq
	case containerKindCrafting, containerKindStonecutter:
		// CraftingMenu/StonecutterMenu.stillValid == stillValid(access, player, CRAFTING_TABLE/STONECUTTER).
		// Ender's crafting + stonecutter windows are TRANSIENT and store NO block position (their contents
		// return to the player on close, so there is no persistent/remote container to exfiltrate), so the
		// reach re-check cannot be evaluated. Cited deferral: treated as always-valid. No security exposure,
		// unlike a block chest, nothing survives close.
		return true
	}
	return true
}

// closeOpenContainer runs the AbstractContainerMenu.removed() teardown for the player open window (the
// per-kind clearContainer/placeItemBack + the carried-cursor return + the CONTAINER_CLOSE game event) and
// clears p.openContainer. It is the shared body handleContainerClose (the client-initiated close) and
// serverCloseContainer (the stillValid auto-close) both call, so the two paths run IDENTICAL teardown.
func (t *TickLoop) closeOpenContainer(p *tickPlayer) {
	if p == nil {
		return
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindCrafting {
		t.closeCraftingWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindStonecutter {
		t.closeStonecutterWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindMerchant {
		t.closeMerchantWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindFurnace {
		t.closeFurnaceWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindDispenser {
		t.closeDispenserWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindHopper {
		t.closeHopperWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindBeacon {
		t.closeBeaconWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindAnvil {
		t.closeAnvilWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindEnchant {
		t.closeEnchantWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindGrindstone {
		t.closeGrindstoneWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindSmithing {
		t.closeSmithingWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindLoom {
		t.closeLoomWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindShulker {
		t.closeShulkerWindow(p, p.openContainer)
	}
	if p.openContainer != nil && p.openContainer.kind == containerKindEnderChest {
		t.closeEnderChestWindow(p, p.openContainer)
	}
	// The CARRIED (cursor) item: AbstractContainerMenu.removed places a left-on-cursor item back into the
	// inventory (drop if full) and clears the cursor. Same body as handleContainerClose.
	inv := ensureInventory(p)
	if carried := inv.getCarried(); !stackEmpty(carried) {
		add := carried
		if !t.inventoryAdd(p, inv, &add) {
			t.playerDrop(p, add, false)
		}
		inv.setCarried(component.SlotData{})
		t.sendContent(p)
	}
	// ChestBlockEntity.stopOpen CONTAINER_CLOSE game event (1 to 0 edge; single viewer in v1).
	if p.openContainer != nil && p.openContainer.kind == containerKindChest {
		t.gameEventAt(geContainerClose, p.openContainer.chestPos, gameEventContext{sourceEntityID: p.entityID})
	}
	p.openContainer = nil
}

// serverCloseContainer ports ServerPlayer.closeContainer(): send the client a ClientboundContainerClose
// for the open window (so the client tears down its screen; WITHOUT this the auto-close would desync the
// client, which would keep the window rendered) THEN run doCloseContainer (menu.removed via
// closeOpenContainer, resetting containerMenu to the InventoryMenu). CITE ServerPlayer.closeContainer /
// doCloseContainer.
func (t *TickLoop) serverCloseContainer(p *tickPlayer) {
	if p == nil || p.openContainer == nil {
		return
	}
	if p.client != nil {
		// ClientboundContainerClosePacket: a single CONTAINER_ID (VarInt) = the window id. VERIFIED javap.
		p.client.Send(pk.Marshal(int32(packetid.ClientboundContainerClose), pk.VarInt(p.openContainer.windowID)))
	}
	t.closeOpenContainer(p)
}

// sweepContainerStillValid ports the ServerPlayer.tick container-validity block, run once per tick for
// every online player. For each player with an open window, if menuStillValid is false the window is
// auto-closed (serverCloseContainer). Draws ZERO RNG and skips every player without an open window, so the
// pig oracle (a mob with no menu) is unaffected. CITE ServerPlayer.tick container stillValid + close.
func (t *TickLoop) sweepContainerStillValid() {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if !t.menuStillValid(p) {
			t.serverCloseContainer(p)
		}
	}
}
