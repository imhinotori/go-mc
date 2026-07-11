package server

// hopper_menu.go — the HOPPER MENU (the 5-slot container UI over the tick-owned hopperBE), a 1:1 port of
// net.minecraft.world.inventory.HopperMenu over the 26.2 jar (temp/cache/26.2-inner.jar, CFR this
// session). Opens on right-clicking a hopper (chest_open.go useBlockInteraction). The engine mirrors
// dispenser_menu.go (PICKUP/QUICK_MOVE/THROW) over the flat 5-slot container backed by the tick-owned
// hopperBE (NOT a transient copy: a click mutates the same items the transfer drive moves, and close just
// frees the window — the items persist in the BE).
//
// 1:1 jar chain (VERIFIED CFR this session):
//
//	HopperBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (!isClientSide && getBlockEntity(pos) instanceof HopperBlockEntity hopper) {
//	        player.openMenu(hopper); player.awardStat(Stats.INSPECT_HOPPER);
//	    }
//	    return InteractionResult.SUCCESS;                                   // CONSUMES -> no place
//	HopperMenu(id, inventory, container):
//	    for (x = 0..5) addSlot(new Slot(hopper, x, 44 + x*18, 20));        // grid 0..4
//	    addStandardInventorySlots(inventory, 8, 51);                       // INV 5..31 (main) + USE_ROW 32..40 (hotbar)
//	HopperMenu.quickMoveStack(player, i):
//	    if (i < 5) moveItemStackTo(stack, 5, slots.size(), true);          // grid -> player inv (reverse)
//	    else       moveItemStackTo(stack, 0, 5, false);                    // player -> grid
//
// v1 subset (cited): awardStat (INSPECT_HOPPER) is a faithful no-op (no stats); the 5 slots are the REAL
// block-entity items (NOT a transient copy).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// hopperMenuSize is the hopper-window slot count: 5 grid + 27 main + 9 hotbar = 41 (HopperMenu: grid 0..4,
// INV_SLOT 5..31, USE_ROW 32..40). Verified ctor.
const hopperMenuSize = hopperContainerSize + 27 + 9 // 41

// openHopper ports HopperBlock.useWithoutItem -> ServerPlayer.openMenu for a hopper at pos: resolve (or
// create) the hopperBE, close any prior window, allocate a windowId, send ClientboundOpenScreen(hopper) +
// the initial ContainerSetContent, and record the open-container state. Returns true (consumed) once sent;
// false only if the block/world could not be resolved (then placement is NOT skipped).
func (t *TickLoop) openHopper(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsHopper(state) {
		return false
	}
	h := t.resolveHopper(pos, state)
	if h == nil {
		return false
	}

	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindHopper, hopperPos: pos}

	// connection.send(new ClientboundOpenScreenPacket(containerId, hopper, getDisplayName())).
	menuID := menuTypeID(registryid.Menu, "minecraft:hopper")
	p.client.Send(openScreen(int32(win), menuID, "Hopper"))

	t.sendHopperContent(p, h)
	return true
}

// hopperMenuItems builds the 41-slot ContainerSetContent list: grid 0..4, then the player inventory — main
// 5..31 (window slots 5..31) then hotbar 32..40 (window slots 32..40). Mirrors HopperMenu's slot layout.
func hopperMenuItems(h *hopperBE, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, hopperMenuSize)
	for i := 0; i < hopperContainerSize; i++ {
		out[i] = h.items[i]
	}
	for i := 0; i < 27; i++ {
		out[hopperContainerSize+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[hopperContainerSize+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendHopperContent pushes the authoritative ContainerSetContent for the open hopper window (the initMenu
// -> broadcastChanges full-slot sync, and the resend after any click or transfer). Bumps the inventory
// state id. Mirrors sendDispenserContent.
func (t *TickLoop) sendHopperContent(p *tickPlayer, h *hopperBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindHopper {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		hopperMenuItems(h, inv), inv.getCarried()))
}

// broadcastHopperChange re-sends the authoritative content to any player whose open window is the hopper
// at pos — the transfer-drive `setChanged` observable equivalent (an item left/entered a slot, so a viewer
// must see it). A hopper nobody is viewing changes silently. Mirrors broadcastDispenserChange. Tick-owned.
func (t *TickLoop) broadcastHopperChange(pos pk.Position, h *hopperBE) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindHopper && p.openContainer.hopperPos == pos {
			t.sendHopperContent(p, h)
		}
	}
}

// hopperSlotRef resolves a hopper-WINDOW slot index (0..40) to the concrete container backing: grid slots
// 0..4 -> the hopperBE items; player slots 5..40 -> the player inventory window slot. Mirrors dispenserSlotRef.
type hopperSlotRef struct {
	h       *hopperBE
	inv     *Inventory
	gridIdx int
	invSlot int16
	ok      bool
}

func hopperResolveSlot(h *hopperBE, inv *Inventory, menuIdx int) hopperSlotRef {
	switch {
	case menuIdx >= 0 && menuIdx < hopperContainerSize:
		return hopperSlotRef{h: h, inv: inv, gridIdx: menuIdx, invSlot: -1, ok: true}
	case menuIdx >= hopperContainerSize && menuIdx < hopperContainerSize+27:
		return hopperSlotRef{h: h, inv: inv, gridIdx: -1, invSlot: int16(windowMainFirst + (menuIdx - hopperContainerSize)), ok: true}
	case menuIdx >= hopperContainerSize+27 && menuIdx < hopperMenuSize:
		return hopperSlotRef{h: h, inv: inv, gridIdx: -1, invSlot: int16(windowHotbarFirst + (menuIdx - hopperContainerSize - 27)), ok: true}
	}
	return hopperSlotRef{}
}

func (r hopperSlotRef) get() component.SlotData {
	if r.gridIdx >= 0 {
		return r.h.items[r.gridIdx]
	}
	return r.inv.get(r.invSlot)
}

func (r hopperSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.gridIdx >= 0 {
		r.h.items[r.gridIdx] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// clickedHopper ports AbstractContainerMenu.clicked for an open hopper window: snapshot the BE items +
// inventory + cursor, run the click under a panic-recover, then re-send the authoritative content +
// cursor. PICKUP/QUICK_MOVE/THROW supported. Mirrors clickedDispenser.
func (t *TickLoop) clickedHopper(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	h := t.hoppers[oc.hopperPos]
	if h == nil {
		t.sendContent(p)
		return
	}
	inv := ensureInventory(p)

	itemsBefore := h.items
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				h.items = itemsBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doHopperClick(p, oc, h, inv, int(slotNum), button, int(input))
	}()

	t.markHopperDirty(oc.hopperPos)

	t.sendHopperContent(p, h)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doHopperClick dispatches the supported click inputs over the hopper window (PICKUP/QUICK_MOVE/THROW).
// Mirrors doDispenserClick.
func (t *TickLoop) doHopperClick(p *tickPlayer, oc *openContainer, h *hopperBE, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.hopperPickup(p, h, inv, i, j)
	case containerInputQuickMove:
		t.hopperQuickMove(p, h, inv, i)
	case containerInputQuickCraft:
		t.menuDoQuickCraft(p, hopperMenuView(h, inv), i, j)
	case containerInputSwap:
		t.menuDoSwap(p, hopperMenuView(h, inv), i, j)
	case containerInputClone:
		t.menuDoClone(p, hopperMenuView(h, inv), i)
	case containerInputThrow:
		t.hopperThrow(p, h, inv, i, j)
	case containerInputPickupAll:
		t.menuDoPickupAll(p, hopperMenuView(h, inv), i, j)
	}
}

// hopperPickup ports the PICKUP branch over the hopper window. Mirrors dispenserPickup over hopperSlotRef.
func (t *TickLoop) hopperPickup(p *tickPlayer, h *hopperBE, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := hopperResolveSlot(h, inv, i)
	if !ref.ok {
		return
	}
	primary := j == 0
	slotItem := ref.get()
	carried := inv.getCarried()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(hopperSafeInsert(ref, &c, place))
			inv.setCarried(c)
		}
		return
	}
	if stackEmpty(carried) {
		take := (int(slotItem.Count) + 1) / 2
		if primary {
			take = int(slotItem.Count)
		}
		rem := slotItem
		taken := stackSplit(&rem, take)
		ref.set(rem)
		inv.setCarried(taken)
		return
	}
	if stackSameItemSameComponents(slotItem, carried) {
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(hopperSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// hopperSafeInsert ports Slot.safeInsert over a hopperSlotRef. Mirrors dispenserSafeInsert.
func hopperSafeInsert(ref hopperSlotRef, stack *component.SlotData, increment int) component.SlotData {
	existing := ref.get()
	if stackEmpty(*stack) {
		return existing
	}
	add := min(increment, int(stack.Count))
	if room := chestSlotMax(*stack) - int(existing.Count); room < add {
		add = room
	}
	if add <= 0 {
		return existing
	}
	if stackEmpty(existing) {
		return stackSplit(stack, add)
	}
	if stackSameItemSameComponents(existing, *stack) {
		stack.Count = toVar(int(stack.Count) - add)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		existing.Count = toVar(int(existing.Count) + add)
		return existing
	}
	return existing
}

// hopperQuickMove ports HopperMenu.quickMoveStack: a grid slot (0..4) moves into the player inventory
// (reverse=true, hotbar first); a player slot moves into the grid (0..4). Mirrors dispenserQuickMove.
func (t *TickLoop) hopperQuickMove(p *tickPlayer, h *hopperBE, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := hopperResolveSlot(h, inv, i)
	if !ref.ok {
		return
	}
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	if ref.gridIdx >= 0 {
		// Grid -> player inventory: moveItemStackTo(stack, 5, slots.size(), true) over main(9..35)+hotbar(36..44).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
	} else {
		// Player -> grid container (0..4): moveItemStackTo(stack, 0, 5, false).
		if !t.hopperMoveInto(h, &work) {
			return
		}
	}
	ref.set(work)
}

// hopperMoveInto ports moveItemStackTo for the hopper grid destination: pass 1 merges *stack into existing
// same-item grid slots up to the slot max, pass 2 fills empty grid slots. Mirrors dispenserMoveInto over
// the 5 grid cells. MUTATES *stack. Returns whether anything moved.
func (t *TickLoop) hopperMoveInto(h *hopperBE, stack *component.SlotData) bool {
	moved := false
	if stackIsStackable(*stack) {
		for i := 0; i < hopperContainerSize && !stackEmpty(*stack); i++ {
			existing := h.items[i]
			if stackEmpty(existing) || !stackSameItemSameComponents(*stack, existing) {
				continue
			}
			sum := int(existing.Count) + int(stack.Count)
			slotMax := chestSlotMax(existing)
			if sum <= slotMax {
				existing.Count = toVar(sum)
				h.items[i] = existing
				stack.Count = 0
				moved = true
			} else if int(existing.Count) < slotMax {
				stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
				existing.Count = toVar(slotMax)
				h.items[i] = existing
				moved = true
			}
		}
	}
	if !stackEmpty(*stack) {
		for i := 0; i < hopperContainerSize; i++ {
			if !stackEmpty(h.items[i]) {
				continue
			}
			slotMax := chestSlotMax(*stack)
			place := int(stack.Count)
			if slotMax < place {
				place = slotMax
			}
			h.items[i] = stackCopyWithCount(*stack, place)
			stack.Count = toVar(int(stack.Count) - place)
			if stack.Count <= 0 {
				stack.Count = 0
			}
			moved = true
			break
		}
	}
	return moved
}

// hopperThrow ports the THROW branch over a hopper window. Mirrors dispenserThrow.
func (t *TickLoop) hopperThrow(p *tickPlayer, h *hopperBE, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := hopperResolveSlot(h, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	amt := 1
	if j != 0 {
		amt = int(cur.Count)
	}
	taken := stackCopyWithCount(cur, amt)
	cur.Count = toVar(int(cur.Count) - amt)
	if cur.Count <= 0 {
		cur = component.SlotData{Count: 0}
	}
	ref.set(cur)
	t.playerDrop(p, taken, true)
}

// closeHopperWindow ports HopperMenu.removed -> super.removed + hopper.stopOpen: the 5 slots ARE the
// block-entity container (already authoritative in the tick-owned hopperBE), so close just FREES the
// window. Mirrors closeDispenserWindow.
func (t *TickLoop) closeHopperWindow(_ *tickPlayer, _ *openContainer) {}

// hopperMenuView adapts the OPEN HOPPER window (grid 0..4 + player 5..40) to the generic menuView so the
// shared SWAP / CLONE / PICKUP_ALL branches (menu_click.go) drive it. Every hopper + player cell is a
// plain Slot (mayPickup / mayPlace true, getMaxStackSize = chestSlotMax, onSwapCraft / onTake no-ops).
// CITE AbstractContainerMenu.doClick over the HopperMenu slot layout.
func hopperMenuView(h *hopperBE, inv *Inventory) menuView {
	return menuView{
		size: hopperMenuSize,
		inv:  inv,
		slotAt: func(idx int) menuSlotView {
			ref := hopperResolveSlot(h, inv, idx)
			if !ref.ok {
				return menuSlotView{}
			}
			return menuSlotView{
				ok:            true,
				getItem:       func() component.SlotData { return ref.get() },
				setByPlayer:   func(s component.SlotData) { ref.set(s) },
				mayPickupFn:   func(*tickPlayer) bool { return true },
				mayPlaceFn:    func(component.SlotData) bool { return true },
				maxStackFn:    func(s component.SlotData) int { return chestSlotMax(s) },
				onSwapCraftFn: func(int) {},
				onTakeFn:      func(*tickPlayer, component.SlotData) {},
			}
		},
		canTakeItemForPickAll: func(component.SlotData, int) bool { return true },
	}
}
