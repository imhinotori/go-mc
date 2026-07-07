package server

// minecart_container.go — the CONTAINER-MINECART surface: the containerView adapter that makes a
// chest/hopper minecart a Container a block hopper can pull from / push into (the getEntityContainer
// seam, container.go), plus the right-click handlers that mount a plain minecart and open a chest
// minecart's menu. A 1:1 port of net.minecraft.world.entity.vehicle.minecart.AbstractMinecartContainer
// (+ MinecartChest / MinecartHopper) over temp/cache/26.2-inner.jar (CFR this session).
//
// AbstractMinecartContainer IS a Container (extends VehicleEntity implements ContainerEntity): its
// itemStacks NonNullList is the backing store, getContainerSize is the type's slot count (27 chest / 5
// hopper), and the hopper transfer machinery drives it through the exact same containerView interface a
// block chest/hopper uses — so filling getEntityContainer makes hopper-from-minecart work with no new
// transfer code. CITE AbstractMinecartContainer (Container) + ContainerEntity.

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/data/registryid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// isContainerMinecart reports whether an entity is a chest/hopper minecart (an AbstractMinecartContainer)
// — the CONTAINER_ENTITY_SELECTOR membership test for getEntityContainer. A plain/furnace/tnt minecart is
// NOT a container. CITE EntitySelector.CONTAINER_ENTITY_SELECTOR (Container instanceof).
func isContainerMinecart(e *Entity) bool {
	if e == nil || !e.isMinecart || e.minecartItems == nil {
		return false
	}
	return e.typ == entity.ChestMinecart.ID || e.typ == entity.HopperMinecart.ID
}

// minecartContainerAt ports HopperBlockEntity.getEntityContainer(x, y, z): the AABB it searches is
// centered on the block cell (x±0.5, y±0.5, z±0.5 around the cell center = the (pos..pos+1) cube). It
// returns the FIRST container-minecart whose feet-anchored box intersects that cube as a containerView.
// (Vanilla picks a RANDOM one of the intersecting entities via level.getRandom().nextInt(size); with the
// common single-cart case the pick is deterministic, and using the first found never draws off the
// gameplay RNG — the minecart physics must not perturb the mob/pig RNG stream, so a first-match pick is
// the RNG-safe choice. CITE getEntityContainer's random pick as the documented v1 reduction.)
func (t *TickLoop) minecartContainerAt(pos pk.Position) containerView {
	// The block cube (pos..pos+1) the getEntityContainer AABB covers.
	loX := float64(pos.X)
	loY := float64(pos.Y)
	loZ := float64(pos.Z)
	hiX := loX + 1.0
	hiY := loY + 1.0
	hiZ := loZ + 1.0
	cx := loX + 0.5
	cz := loZ + 0.5
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if !isContainerMinecart(e) {
			continue
		}
		// Feet-anchored AABB (width × height centered on x/z) intersects the block cube on all 3 axes.
		ihw := e.width / 2
		if hiX <= e.x-ihw || e.x+ihw <= loX ||
			hiY <= e.y || e.y+e.height <= loY ||
			hiZ <= e.z-ihw || e.z+ihw <= loZ {
			continue
		}
		return &minecartContainer{t: t, e: e}
	}
	return nil
}

// -------------------------------------------------------------------------------------------------
// minecartContainer — AbstractMinecartContainer (a plain Container; 27 chest / 5 hopper slots)
// -------------------------------------------------------------------------------------------------

// minecartContainer adapts a container-minecart entity to the containerView the hopper transfer + the
// comparator analog drive. It reads/writes the entity's minecartItems slice directly (tick-owned). A
// container-minecart is a PLAIN Container (not a WorldlyContainer — no face restrictions), like a chest.
//	[VERIFIED CFR AbstractMinecartContainer implements Container (not WorldlyContainer); getItem/setItem/
//	 getContainerSize/isEmpty over itemStacks.]
type minecartContainer struct {
	t *TickLoop
	e *Entity
}

func (c *minecartContainer) getContainerSize() int { return len(c.e.minecartItems) }
func (c *minecartContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.e.minecartItems) {
		return component.SlotData{Count: 0}
	}
	return c.e.minecartItems[slot]
}
func (c *minecartContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.e.minecartItems) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.e.minecartItems[slot] = stack
}
func (c *minecartContainer) isEmpty() bool {
	for _, s := range c.e.minecartItems {
		if !stackEmpty(s) {
			return false
		}
	}
	return true
}
func (c *minecartContainer) setChanged() {
	// AbstractMinecartContainer.setChanged is a no-op on the entity (the container IS the entity state; a
	// viewer resync is driven by the open-menu path, broadcastMinecartChestChange). Rebroadcast to any
	// player viewing this minecart so a hopper pull/push updates their open window.
	c.t.broadcastMinecartChestChange(c.e)
}
func (c *minecartContainer) getSlotsForFace(block.Direction) []int {
	return createFlatSlots(len(c.e.minecartItems))
}
func (c *minecartContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (c *minecartContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *minecartContainer) canTakeItem(int, component.SlotData) bool { return true }
func (c *minecartContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *minecartContainer) isWorldly() bool     { return false }
func (c *minecartContainer) asHopper() *hopperBE { return nil }

// -------------------------------------------------------------------------------------------------
// RIGHT-CLICK: mount a plain minecart / open a chest minecart --------------------------------------
// -------------------------------------------------------------------------------------------------

// tryMinecartRide ports Minecart.interact / AbstractMinecart ride branch for a plain RIDEABLE minecart:
// a right-click (not a secondary/shift action) mounts the player as a passenger (player.startRiding(this)).
// Returns true when the interact belongs to the minecart (the mount took OR the cart was already full),
// false to fall through. Only a plain Minecart is rideable (isRideable()); chest/hopper/furnace carts are
// not (their right-click opens the container / does nothing).
//	[VERIFIED CFR Minecart.isRideable: return true (the base AbstractMinecart.isRideable returns false; only
//	 the plain Minecart overrides to true); the ride is player.startRiding(minecart) via the interact.]
func (t *TickLoop) tryMinecartRide(p *tickPlayer, cart *Entity, usingSecondaryAction bool) bool {
	if cart.typ != entity.Minecart.ID {
		return false // only the plain minecart is rideable
	}
	if usingSecondaryAction {
		return false // a shift-right-click does not mount (falls through)
	}
	if t.playerStartRiding(p, cart, false) {
		t.broadcastSetPassengers(cart)
	}
	return true // the interact belongs to the minecart (mounted or already occupied)
}

// tryMinecartChestOpen ports MinecartChest.interact → AbstractMinecartContainer.interact: a right-click on
// a chest minecart opens its 27-slot container menu (player.openMenu(this)). Returns true when the menu was
// sent (the interact is consumed), false otherwise. A hopper minecart opens its 5-slot menu the same way.
//	[VERIFIED CFR AbstractMinecartContainer.interact: player.openMenu(this); return SUCCESS (server side).]
func (t *TickLoop) tryMinecartChestOpen(p *tickPlayer, cart *Entity) bool {
	if p.client == nil || !isContainerMinecart(cart) {
		return false
	}
	// ServerPlayer.openMenu: free any previously-open window, allocate a fresh window id, send OpenScreen +
	// the initial ContainerSetContent. The window is backed by the minecart ENTITY (kind minecart chest), so
	// a ContainerClick/Close resolves back to the cart by its thin entity id.
	if p.openContainer != nil {
		p.openContainer = nil
	}
	win := p.nextContainerCounter()
	size := len(cart.minecartItems)
	kindMenu := "minecraft:generic_9x3" // 27-slot chest minecart
	if cart.typ == entity.HopperMinecart.ID {
		kindMenu = "minecraft:hopper" // 5-slot hopper minecart
	}
	p.openContainer = &openContainer{
		windowID:          win,
		kind:              containerKindMinecartChest,
		minecartEntityID:  cart.id,
		minecartSlotCount: size,
	}
	title := "Minecart with Chest"
	if cart.typ == entity.HopperMinecart.ID {
		title = "Minecart with Hopper"
	}
	menuID := menuTypeID(registryid.Menu, kindMenu)
	p.client.Send(openScreen(int32(win), menuID, title))
	t.sendMinecartChestContent(p, cart)
	return true
}

// sendMinecartChestContent pushes the authoritative ContainerSetContent for an open minecart-chest window
// (the initMenu → broadcastChanges full-slot sync, and the resend after any change). The wire layout is the
// minecart container slots first (0..size-1), then the player inventory in the generic_9x3 / hopper order.
func (t *TickLoop) sendMinecartChestContent(p *tickPlayer, cart *Entity) {
	if p.client == nil || p.openContainer == nil {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	size := len(cart.minecartItems)
	out := make([]component.SlotData, size+27+9)
	for i := 0; i < size; i++ {
		out[i] = cart.minecartItems[i]
	}
	// main: ← inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[size+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: ← inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[size+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID, out, inv.getCarried()))
}

// broadcastMinecartChestChange re-sends the authoritative content to any player whose open window is this
// minecart (the observable equivalent of setChanged when a hopper pushes/pulls an item through it), so a
// viewer sees the slot change. A minecart nobody is viewing changes silently (its state is authoritative in
// minecartItems). Mirrors broadcastChestChange. Tick-owned.
func (t *TickLoop) broadcastMinecartChestChange(cart *Entity) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindMinecartChest && p.openContainer.minecartEntityID == cart.id {
			t.sendMinecartChestContent(p, cart)
		}
	}
}

// clickedMinecartChest routes a ContainerClick on an open minecart-chest window to the existing container
// click engine over the entity's minecartItems. A 27-slot CHEST minecart reuses the chest click engine
// (clickedChest) by wrapping the entity's item slice in a chestLoot that SHARES the same backing array —
// the chest engine mutates cl.items[i] in place, which mutates minecartItems[i] directly (no copy). A
// 5-slot HOPPER minecart drives the hopper menu slot layout the same way. The cart is re-resolved by its
// thin entity id (the Folia rule); a despawned cart just resends authoritative player content.
//	[VERIFIED CFR AbstractMinecartContainer wraps itemStacks in the same AbstractContainerMenu.doClick
//	 (ChestMenu for MinecartChest, HopperMenu for MinecartHopper) the block chest/hopper drive.]
func (t *TickLoop) clickedMinecartChest(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	cart := t.entityByIDAnyRegion(oc.minecartEntityID)
	if cart == nil || cart.minecartItems == nil {
		t.sendContent(p)
		return
	}
	// The 27-slot chest minecart reuses the chest click engine over a shared-backing chestLoot. (The 5-slot
	// hopper minecart's menu-click engine is a cited follow-up — the hopper minecart's PRIMARY behavior, the
	// block-hopper transfer + its own auto-suck, works through the getEntityContainer seam without the menu.)
	if len(cart.minecartItems) == chestContainerSize {
		cl := &chestLoot{items: cart.minecartItems} // SHARES the backing array (both []SlotData)
		t.clickedChest(p, cl, slotNum, button, input)
		cart.minecartItems = cl.items // re-bind in case the engine ever reassigns (it mutates in place)
		t.broadcastMinecartChestChange(cart)
		return
	}
	// Non-27-slot (hopper minecart) menu click: resend authoritative content (menu-click engine deferred).
	t.sendMinecartChestContent(p, cart)
}
