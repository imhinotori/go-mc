package server

// boat_container.go — the CHEST-BOAT container surface + the boat right-click interact: the containerView
// adapter that makes a chest boat/raft a Container a block hopper can pull from / push into (the
// getEntityContainer seam, container.go), plus the right-click handlers that MOUNT a boat and OPEN a chest
// boat's menu. A 1:1 port of net.minecraft.world.entity.vehicle.boat.AbstractChestBoat (+ AbstractBoat.interact)
// over temp/cache/26.2-inner.jar (CFR this session).
//
// AbstractChestBoat IS a Container (extends AbstractBoat implements ContainerEntity): its itemStacks
// NonNullList is the backing store (getContainerSize()==27) — the SAME 27-slot single-chest shape a chest
// minecart / block chest use, so the identical chest click engine + the getEntityContainer hopper machinery
// serve it with no new transfer code. The chest boat reuses the shared Entity.minecartItems slice as its
// itemStacks backing (both are []component.SlotData of size 27). CITE AbstractChestBoat (ContainerEntity).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// isChestBoat reports whether an entity is a chest boat/raft (an AbstractChestBoat) — the CONTAINER_ENTITY_
// SELECTOR membership test for getEntityContainer. A plain boat/raft is NOT a container. CITE
// EntitySelector.CONTAINER_ENTITY_SELECTOR (ContainerEntity instanceof).
func isChestBoat(e *Entity) bool {
	if e == nil || !e.isBoat || e.minecartItems == nil {
		return false
	}
	return boatIsChestType(e.typ)
}

// boatContainerAt ports the getEntityContainer fallback for a CHEST BOAT: the block cube (pos..pos+1) is
// searched for the first chest boat whose feet-anchored box intersects it, returned as a containerView. It
// mirrors minecartContainerAt exactly (same first-match RNG-safe pick — a boat's float physics must not
// perturb the mob/pig RNG stream). CITE HopperBlockEntity.getEntityContainer.
func (t *TickLoop) boatContainerAt(pos pk.Position) containerView {
	loX := float64(pos.X)
	loY := float64(pos.Y)
	loZ := float64(pos.Z)
	hiX := loX + 1.0
	hiY := loY + 1.0
	hiZ := loZ + 1.0
	cx := loX + 0.5
	cz := loZ + 0.5
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if !isChestBoat(e) {
			continue
		}
		ihw := e.width / 2
		if hiX <= e.x-ihw || e.x+ihw <= loX ||
			hiY <= e.y || e.y+e.height <= loY ||
			hiZ <= e.z-ihw || e.z+ihw <= loZ {
			continue
		}
		return &minecartContainer{t: t, e: e} // the shared 27-slot vehicle-container adapter (minecart_container.go)
	}
	return nil
}

// -------------------------------------------------------------------------------------------------
// RIGHT-CLICK: mount a boat / open a chest boat ----------------------------------------------------
// -------------------------------------------------------------------------------------------------

// tryBoatInteract ports the AbstractBoat.interact chain (with the AbstractChestBoat.interact override) for a
// right-click on a boat. Vanilla:
//
//	AbstractBoat.interact: r = super.interact(...) (PASS); if (!secondary && outOfControlTicks<60 &&
//	    (clientSide || player.startRiding(this))) return SUCCESS; return PASS.
//	AbstractChestBoat.interact: r = super.interact(...) (== AbstractBoat.interact, the ride); if (r != PASS)
//	    return r; if (!canAddPassenger(player) || secondary) { open container }; return PASS.
//
// So the RESOLVED behavior (server side):
//   - PLAIN boat/raft: a non-secondary right-click on a non-capsized boat MOUNTS the player (startRiding).
//   - CHEST boat/raft: a non-secondary right-click MOUNTS if there is a free seat (canAddPassenger); a
//     SECONDARY (shift) click OR a full boat OPENS the 27-slot container menu.
//
// Returns true when the interact belongs to the boat (a mount, a container open, or a consumed no-op), so
// handleInteract does NOT fall through to the feed path (a boat is not fed). Boat-gated (isBoat), a zero-cost
// no-op for every mob — the pig oracle stream is unperturbed. CITE AbstractBoat.interact / AbstractChestBoat.interact.
func (t *TickLoop) tryBoatInteract(p *tickPlayer, boat *Entity, usingSecondaryAction bool) bool {
	// AbstractBoat.interact ride gate: a non-secondary click on a non-capsized boat mounts. For a chest boat
	// the ride only happens when a seat is free AND the click is not secondary (AbstractChestBoat routes a
	// secondary/full click to the container instead).
	rideGate := !usingSecondaryAction && boat.boatOutOfControlTicks < boatOutOfControlEjectTicks
	if isChestBoat(boat) {
		// AbstractChestBoat: super.interact (the ride) runs first, but the container branch is taken when
		// !canAddPassenger || secondary. So: ride only when a seat is free AND not secondary; else open container.
		if rideGate && boat.canAddPassengerVehicle() {
			if t.playerStartRiding(p, boat, false) {
				t.broadcastSetPassengers(boat)
			}
			return true
		}
		// !canAddPassenger (full) OR secondary: open the 27-slot container menu (interactWithContainerVehicle).
		t.tryBoatChestOpen(p, boat)
		return true
	}
	// PLAIN boat/raft: the ride gate mounts; a secondary/capsized click is a consumed no-op (PASS still
	// belongs to the boat — a boat is not fed).
	if rideGate {
		if t.playerStartRiding(p, boat, false) {
			t.broadcastSetPassengers(boat)
		}
	}
	return true
}

// tryBoatChestOpen ports AbstractChestBoat.interactWithContainerVehicle → openCustomInventoryScreen: open the
// chest boat's 27-slot container menu (player.openMenu(this)). The window is backed by the boat ENTITY (reusing
// the containerKindMinecartChest machinery — the shared vehicle-container window kind — over the boat's thin
// entity id), so a ContainerClick/Close resolves back to the boat. CITE AbstractChestBoat.openCustomInventoryScreen.
func (t *TickLoop) tryBoatChestOpen(p *tickPlayer, boat *Entity) bool {
	if p.client == nil || !isChestBoat(boat) {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil
	}
	win := p.nextContainerCounter()
	size := len(boat.minecartItems) // 27
	p.openContainer = &openContainer{
		windowID:          win,
		kind:              containerKindMinecartChest, // the shared vehicle-container window kind
		minecartEntityID:  boat.id,
		minecartSlotCount: size,
	}
	menuID := menuTypeID(registryid.Menu, "minecraft:generic_9x3")
	p.client.Send(openScreen(int32(win), menuID, "Chest Boat"))
	t.sendMinecartChestContent(p, boat) // the shared 27-slot vehicle content sync (minecart_container.go)
	return true
}

// (The container adapter methods — getItem/setItem/setChanged/getSlotsForFace/etc. — are the shared
// minecartContainer (minecart_container.go), which reads/writes the entity's minecartItems slice directly. A
// chest boat reuses that adapter verbatim: it IS a plain 27-slot Container exactly like a chest minecart, and
// the setChanged rebroadcast + the clickedMinecartChest engine both re-resolve the vehicle by its thin entity
// id and check `len(minecartItems) == chestContainerSize`, which a 27-slot chest boat satisfies identically.)
