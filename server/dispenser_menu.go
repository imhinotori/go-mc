package server

// dispenser_menu.go — the DISPENSER / DROPPER MENU (redstone tier-4): the 9-slot (3x3 grid) container UI
// over the tick-owned dispenserBE, a 1:1 port of net.minecraft.world.inventory.DispenserMenu (generic_3x3)
// over the 26.2 jar (temp/cache/26.2-inner.jar, CFR this session). Opens on right-clicking a dispenser/
// dropper (chest_open.go useBlockInteraction). The engine mirrors chest_click.go (PICKUP/QUICK_MOVE/THROW)
// over the flat 9-slot container — the same shape as the chest, just 9 slots instead of 27 and backed by
// the tick-owned dispenserBE (NOT a transient copy: a click mutates the same items the dispense drive
// shoots from, and close just frees the window — the items persist in the BE, like a chest/furnace).
//
// 1:1 jar chain (VERIFIED CFR this session):
//
//	DispenserBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (!isClientSide && getBlockEntity(pos) instanceof DispenserBlockEntity be) {
//	        player.openMenu(be); player.awardStat(be instanceof DropperBlockEntity ? INSPECT_DROPPER : INSPECT_DISPENSER);
//	    }
//	    return InteractionResult.SUCCESS;                                   // CONSUMES -> no place
//	DispenserMenu(id, inventory, container):
//	    add3x3GridSlots(container, 62, 17);   // grid 0..8
//	    addStandardInventorySlots(inventory, 8, 84);   // INV 9..35 (main) + USE_ROW 36..44 (hotbar)
//	DispenserMenu.quickMoveStack(player, i):
//	    if (i < 9) moveItemStackTo(stack, 9, 45, true);   // grid -> player inv (reverse: hotbar first)
//	    else       moveItemStackTo(stack, 0, 9, false);   // player -> grid
//
// v1 subset (cited): no stats/spectator, so awardStat (INSPECT_DISPENSER/DROPPER) + the spectator branch
// are faithful no-ops; ContainerLevelAccess reach folds into the open-time reach gate. The 9 slots are the
// REAL block-entity items (NOT a transient copy).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dispenserMenuSize is the dispenser-window slot count: 9 grid + 27 main + 9 hotbar = 45 (DispenserMenu:
// grid 0..8, INV_SLOT 9..35, USE_ROW 36..44). Verified ctor.
const dispenserMenuSize = dispenserContainerSize + 27 + 9 // 45

// dispenserMenuInfo resolves the wire menu-type name + display title for a dispenser/dropper block's menu.
// Both use generic_3x3; the title differs (the translatable container.dispenser / container.dropper).
func dispenserMenuInfo(s block.StateID) (menuName, title string, ok bool) {
	switch {
	case block.IsDispenser(s):
		return "minecraft:generic_3x3", "Dispenser", true
	case block.IsDropper(s):
		return "minecraft:generic_3x3", "Dropper", true
	}
	return "", "", false
}

// openDispenser ports DispenserBlock.useWithoutItem -> ServerPlayer.openMenu for a dispenser/dropper at
// pos: resolve (or create) the dispenserBE, close any prior window, allocate a windowId, send
// ClientboundOpenScreen(generic_3x3) + the initial ContainerSetContent, and record the open-container
// state. Returns true (the action was consumed) once the menu is sent; false only if the block/world could
// not be resolved (then placement is NOT skipped).
func (t *TickLoop) openDispenser(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	_, title, ok := dispenserMenuInfo(state)
	if !ok {
		return false
	}
	d := t.resolveDispenser(pos, state)
	if d == nil {
		return false
	}

	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindDispenser, dispenserPos: pos}

	// connection.send(new ClientboundOpenScreenPacket(containerId, generic_3x3, getDisplayName())).
	menuID := menuTypeID(registryid.Menu, "minecraft:generic_3x3")
	p.client.Send(openScreen(int32(win), menuID, title))

	// initMenu -> broadcastChanges: push the full 45-slot list (grid 0..8 + player 9..44).
	t.sendDispenserContent(p, d)
	return true
}

// dispenserMenuItems builds the 45-slot ContainerSetContent list: grid 0..8, then the player inventory —
// main 9..35 (window slots 9..35) then hotbar 36..44 (window slots 36..44). Mirrors DispenserMenu's
// add3x3GridSlots + addStandardInventorySlots.
func dispenserMenuItems(d *dispenserBE, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, dispenserMenuSize)
	for i := 0; i < dispenserContainerSize; i++ {
		out[i] = d.items[i]
	}
	// main: menu 9..35 <- inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[dispenserContainerSize+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 36..44 <- inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[dispenserContainerSize+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendDispenserContent pushes the authoritative ContainerSetContent for the open dispenser window (the
// initMenu -> broadcastChanges full-slot sync, and the resend after any click or dispense). Bumps the
// player inventory state id so the client tracks the authoritative state.
func (t *TickLoop) sendDispenserContent(p *tickPlayer, d *dispenserBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindDispenser {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		dispenserMenuItems(d, inv), inv.getCarried()))
}

// broadcastDispenserChange re-sends the authoritative content to any player whose open window is the
// dispenser at pos — the dispense-drive `setItem` observable equivalent (the shot item left the slot, so
// a viewer must see the slot empty/shrunk). A dispenser nobody is viewing changes silently (its state is
// still authoritative in the BE). Tick-owned.
func (t *TickLoop) broadcastDispenserChange(pos pk.Position, d *dispenserBE) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindDispenser && p.openContainer.dispenserPos == pos {
			t.sendDispenserContent(p, d)
		}
	}
}

// dispenserSlotRef resolves a dispenser-WINDOW slot index (0..44) to the concrete container backing: grid
// slots 0..8 -> the dispenserBE items; player slots 9..44 -> the player inventory window slot (main 9..35,
// hotbar 36..44). The DispenserMenu slot->container mapping.
type dispenserSlotRef struct {
	d       *dispenserBE
	inv     *Inventory
	gridIdx int   // index into d.items (grid slot), -1 for a player slot
	invSlot int16 // player inventory window slot, -1 for a grid slot
	ok      bool
}

func dispenserResolveSlot(d *dispenserBE, inv *Inventory, menuIdx int) dispenserSlotRef {
	switch {
	case menuIdx >= 0 && menuIdx < dispenserContainerSize:
		return dispenserSlotRef{d: d, inv: inv, gridIdx: menuIdx, invSlot: -1, ok: true}
	case menuIdx >= dispenserContainerSize && menuIdx < dispenserContainerSize+27:
		// main: menu 9..35 -> window slots 9..35
		return dispenserSlotRef{d: d, inv: inv, gridIdx: -1, invSlot: int16(windowMainFirst + (menuIdx - dispenserContainerSize)), ok: true}
	case menuIdx >= dispenserContainerSize+27 && menuIdx < dispenserMenuSize:
		// hotbar: menu 36..44 -> window slots 36..44
		return dispenserSlotRef{d: d, inv: inv, gridIdx: -1, invSlot: int16(windowHotbarFirst + (menuIdx - dispenserContainerSize - 27)), ok: true}
	}
	return dispenserSlotRef{}
}

func (r dispenserSlotRef) get() component.SlotData {
	if r.gridIdx >= 0 {
		return r.d.items[r.gridIdx]
	}
	return r.inv.get(r.invSlot)
}

func (r dispenserSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.gridIdx >= 0 {
		r.d.items[r.gridIdx] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// clickedDispenser ports AbstractContainerMenu.clicked for an open dispenser window: snapshot the BE items
// + inventory + cursor, run the click under a panic-recover (no partial mutation on a throw, T-6-04), then
// re-send the authoritative content + cursor. PICKUP/QUICK_MOVE/THROW are supported (the operations a
// dispenser receives) — the same subset chest_click.go covers.
func (t *TickLoop) clickedDispenser(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	d := t.dispensers[oc.dispenserPos]
	if d == nil {
		t.sendContent(p)
		return
	}
	inv := ensureInventory(p)

	itemsBefore := d.items
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				d.items = itemsBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doDispenserClick(p, oc, d, inv, int(slotNum), button, int(input))
	}()

	// A click may have moved items into/out of the dispenser container (a persistent state change): dirty
	// the column so the dispenser state flushes on the next save pass.
	t.markDispenserDirty(oc.dispenserPos)

	t.sendDispenserContent(p, d)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doDispenserClick dispatches the supported click inputs over the dispenser window (PICKUP/QUICK_MOVE/
// THROW) — the operations a dispenser receives. An unsupported input is a no-op (authoritative content
// re-sent regardless). Mirrors doChestClick.
func (t *TickLoop) doDispenserClick(p *tickPlayer, oc *openContainer, d *dispenserBE, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.dispenserPickup(p, d, inv, i, j)
	case containerInputQuickMove:
		t.dispenserQuickMove(p, d, inv, i)
	case containerInputThrow:
		t.dispenserThrow(p, d, inv, i, j)
	}
}

// dispenserPickup ports the PICKUP branch over the dispenser window: left-click (j==0) whole stack,
// right-click (j==1) half/one, merging same-item stacks and swapping different ones. Mirrors chestPickup
// over dispenserSlotRef.
func (t *TickLoop) dispenserPickup(p *tickPlayer, d *dispenserBE, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := dispenserResolveSlot(d, inv, i)
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
			ref.set(dispenserSafeInsert(ref, &c, place))
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
		ref.set(dispenserSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// dispenserSafeInsert ports Slot.safeInsert over a dispenserSlotRef (grid + player cells; every slot
// accepts any item — Slot.mayPlace is true for a plain container/inventory cell). Mirrors chestSafeInsert.
func dispenserSafeInsert(ref dispenserSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// dispenserQuickMove ports DispenserMenu.quickMoveStack: a grid slot (0..8) moves into the player
// inventory (moveItemStackTo over window 9..44, reverse=true so hotbar fills first); a player slot moves
// into the grid (0..8). Reuses moveItemStackTo for the player destination and a grid-local fill for the
// grid destination. CITE: DispenserMenu.quickMoveStack (i < 9 ? moveTo(9,45,true) : moveTo(0,9,false)).
func (t *TickLoop) dispenserQuickMove(p *tickPlayer, d *dispenserBE, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := dispenserResolveSlot(d, inv, i)
	if !ref.ok {
		return
	}
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	if ref.gridIdx >= 0 {
		// Grid -> player inventory: moveItemStackTo(stack, 9, 45, true) over the player WINDOW main(9..35)+
		// hotbar(36..44), EXCLUDING offhand 45 (the dispenser menu has no offhand slot), reverse=true
		// (hotbar-first fill). CITE: DispenserMenu.quickMoveStack (i < 9 branch).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
	} else {
		// Player -> grid container (0..8): moveItemStackTo(stack, 0, 9, false).
		if !t.dispenserMoveInto(d, &work) {
			return
		}
	}
	ref.set(work)
}

// dispenserMoveInto ports moveItemStackTo for the dispenser grid destination: pass 1 merges *stack into
// existing same-item grid slots up to the slot max, pass 2 fills empty grid slots. Mirrors chestMoveInto
// over the 9 grid cells (forward, no reverse). MUTATES *stack. Returns whether anything moved.
func (t *TickLoop) dispenserMoveInto(d *dispenserBE, stack *component.SlotData) bool {
	moved := false
	if stackIsStackable(*stack) {
		for i := 0; i < dispenserContainerSize && !stackEmpty(*stack); i++ {
			existing := d.items[i]
			if stackEmpty(existing) || !stackSameItemSameComponents(*stack, existing) {
				continue
			}
			sum := int(existing.Count) + int(stack.Count)
			slotMax := chestSlotMax(existing)
			if sum <= slotMax {
				existing.Count = toVar(sum)
				d.items[i] = existing
				stack.Count = 0
				moved = true
			} else if int(existing.Count) < slotMax {
				stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
				existing.Count = toVar(slotMax)
				d.items[i] = existing
				moved = true
			}
		}
	}
	if !stackEmpty(*stack) {
		for i := 0; i < dispenserContainerSize; i++ {
			if !stackEmpty(d.items[i]) {
				continue
			}
			slotMax := chestSlotMax(*stack)
			place := int(stack.Count)
			if slotMax < place {
				place = slotMax
			}
			d.items[i] = stackCopyWithCount(*stack, place)
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

// dispenserThrow ports the THROW branch over a dispenser window: with an empty cursor, Q drops 1 (j==0)
// or the whole stack (j==1) from the slot under the cursor into the world. Mirrors chestThrow.
func (t *TickLoop) dispenserThrow(p *tickPlayer, d *dispenserBE, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := dispenserResolveSlot(d, inv, i)
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

// closeDispenserWindow ports DispenserMenu.removed -> super.removed + dispenser.stopOpen: the 9 slots ARE
// the block-entity container (already authoritative in the tick-owned dispenserBE — every click mutated it
// in place), so close just FREES the window (the caller clears p.openContainer). Unlike the crafting/
// stonecutter transient grids, NOTHING is returned to the player (the items persist in the BE). The
// carried-item return is handled by the shared handleContainerClose path. CITE: DispenserMenu.removed
// (super.removed clears listeners; the container survives).
func (t *TickLoop) closeDispenserWindow(_ *tickPlayer, _ *openContainer) {
	// no-op: the dispenserBE persists in t.dispensers (the chest/furnace persist model).
}
