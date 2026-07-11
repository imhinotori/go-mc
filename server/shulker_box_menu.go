package server

// shulker_box_menu.go -- the SHULKER BOX MENU: the 27-slot container UI over the tick-owned shulkerBE, a
// 1:1 port of net.minecraft.world.inventory.ShulkerBoxMenu (menu type shulker_box) over the 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p this session). Opens on right-clicking a shulker box
// (chest_open.go useBlockInteraction, gated by ShulkerBoxBlock.canOpen). The engine mirrors dispenser_menu.go
// over a 27-slot container (the same shape as the chest) backed by the tick-owned shulkerBE (NOT a transient
// copy: a click mutates the same items the hopper/comparator read, and close just frees the window -- the
// items persist in the BE, like a chest/dispenser). The one difference from a plain chest is the SLOT
// mayPlace gate: a ShulkerBoxSlot rejects nesting a shulker box inside a shulker box.
//
// 1:1 jar chain (VERIFIED javap this session):
//
//	ShulkerBoxBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (level instanceof ServerLevel sl && be instanceof ShulkerBoxBlockEntity sbe && canOpen(state,level,pos,sbe)) {
//	        player.openMenu(sbe); player.awardStat(OPEN_SHULKER_BOX); PiglinAi.angerNearbyPiglins(sl,player,true);
//	    }
//	    return InteractionResult.SUCCESS;                                     // CONSUMES -> no place
//	ShulkerBoxMenu(id, inv, container): checkContainerSize(c,27); container.startOpen(inv.player);
//	    for row 0..2 for col 0..8: addSlot(new ShulkerBoxSlot(c, col+row*9, 8+col*18, 18+row*18));
//	    addStandardInventorySlots(inv, 8, 84);       // INV 27..53 (main) + hotbar 54..62
//	ShulkerBoxMenu.quickMoveStack(player, i):
//	    if (i < container.getContainerSize()) moveItemStackTo(s, containerSize, slots.size(), true);   // box -> player
//	    else                                  moveItemStackTo(s, 0, containerSize, false);             // player -> box
//	ShulkerBoxSlot.mayPlace(stack): stack.getItem().canFitInsideContainerItems();                     // reject nested box
//	ShulkerBoxMenu.removed(player): super.removed; container.stopOpen(player);
//
// v1 subset (cited): no stats/spectator, so awardStat (OPEN_SHULKER_BOX) + the piglin anger + the spectator
// branch are faithful no-ops; ContainerLevelAccess reach folds into the open-time reach gate. The 27 slots
// are the REAL block-entity items (NOT a transient copy).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shulkerMenuSize is the shulker-window slot count: 27 container + 27 main + 9 hotbar = 63 (ShulkerBoxMenu:
// container 0..26, INV 27..53, hotbar 54..62). Verified ctor.
const shulkerMenuSize = shulkerContainerSize + 27 + 9 // 63

// shulkerTitle is the shulker window display name. Vanilla ShulkerBoxBlockEntity.DEFAULT_NAME is the
// translatable "container.shulkerBox" -> "Shulker Box". v1 sends the plain literal (no client i18n
// dependence); structured to become a translatable Component when the chat layer grows one.
const shulkerTitle = "Shulker Box"

// stackIsShulkerBoxItem ports Item.canFitInsideContainerItems()==false for a shulker box item: the held
// stack resolves (Block.byItem) to a ShulkerBoxBlock. Every OTHER item can fit inside container items in
// v1's world (canFitInsideContainerItems defaults true; only shulker boxes and a couple of other special
// items opt out, and only shulker boxes are relevant to the shulker slot gate). CITE ShulkerBoxSlot.mayPlace
// (stack.getItem().canFitInsideContainerItems()); the shulker box item overrides it to false.
func stackIsShulkerBoxItem(s component.SlotData) bool {
	if stackEmpty(s) {
		return false
	}
	sid, ok := blockStateForItem(s)
	if !ok {
		return false
	}
	return block.IsShulkerBox(sid)
}

// openShulker ports ShulkerBoxBlock.useWithoutItem -> ServerPlayer.openMenu for a shulker box at pos:
// resolve (or create) the shulkerBE, run the canOpen blocked-above gate, start the opener (startOpen: bump
// openCount, play the open sound, begin the OPENING lid animation), close any prior window, allocate a
// windowId, send ClientboundOpenScreen(shulker_box) + the initial ContainerSetContent, and record the
// open-container state. Returns true (the interaction was consumed -- ShulkerBoxBlock ALWAYS returns
// SUCCESS, so placement is skipped) even when canOpen is false (the box is blocked; vanilla still consumes).
func (t *TickLoop) openShulker(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	s := t.resolveShulker(pos)
	if s == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	// canOpen(state, level, pos, sbe): the half-block the lid expands into must be free. When blocked, the
	// menu does NOT open, but useWithoutItem still returns SUCCESS (consumes the interaction -> no place).
	if !t.shulkerCanOpen(pos, state, s) {
		return true
	}

	// ShulkerBoxMenu ctor: container.startOpen(inv.player) -> ShulkerBoxBlockEntity.startOpen (bump the
	// opener count, play the OPEN sound on the first opener, begin the OPENING lid animation).
	t.shulkerStartOpen(pos, s)

	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindShulker, shulkerPos: pos}

	// connection.send(new ClientboundOpenScreenPacket(containerId, shulker_box, getDisplayName())).
	menuID := menuTypeID(registryid.Menu, "minecraft:shulker_box")
	p.client.Send(openScreen(int32(win), menuID, shulkerTitle))

	// initMenu -> broadcastChanges: push the full 63-slot list (container 0..26 + player 27..62).
	t.sendShulkerContent(p, s)
	return true
}

// shulkerMenuItems builds the 63-slot ContainerSetContent list: container 0..26, then the player inventory
// -- main 27..53 (window slots 9..35) then hotbar 54..62 (window slots 36..44). Mirrors ShulkerBoxMenu's
// container-slot grid + addStandardInventorySlots.
func shulkerMenuItems(s *shulkerBE, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, shulkerMenuSize)
	for i := 0; i < shulkerContainerSize; i++ {
		out[i] = s.items[i]
	}
	// main: menu 27..53 <- inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[shulkerContainerSize+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 54..62 <- inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[shulkerContainerSize+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendShulkerContent pushes the authoritative ContainerSetContent for the open shulker window (the initMenu
// -> broadcastChanges full-slot sync, and the resend after any click or hopper transfer). Bumps the player
// inventory state id so the client tracks the authoritative state.
func (t *TickLoop) sendShulkerContent(p *tickPlayer, s *shulkerBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindShulker {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		shulkerMenuItems(s, inv), inv.getCarried()))
}

// broadcastShulkerChange re-sends the authoritative content to any player whose open window is the shulker
// at pos -- the hopper-transfer setItem observable equivalent (an item moved into/out of the box, so a
// viewer must see the slot change). A shulker nobody is viewing changes silently (its state is still
// authoritative in the BE). Mirrors broadcastDispenserChange. Tick-owned.
func (t *TickLoop) broadcastShulkerChange(pos pk.Position, s *shulkerBE) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindShulker && p.openContainer.shulkerPos == pos {
			t.sendShulkerContent(p, s)
		}
	}
}

// shulkerSlotRef resolves a shulker-WINDOW slot index (0..62) to the concrete container backing: container
// slots 0..26 -> the shulkerBE items; player slots 27..62 -> the player inventory window slot (main 9..35,
// hotbar 36..44). The ShulkerBoxMenu slot->container mapping.
type shulkerSlotRef struct {
	s       *shulkerBE
	inv     *Inventory
	boxIdx  int   // index into s.items (container slot), -1 for a player slot
	invSlot int16 // player inventory window slot, -1 for a container slot
	ok      bool
}

func shulkerResolveSlot(s *shulkerBE, inv *Inventory, menuIdx int) shulkerSlotRef {
	switch {
	case menuIdx >= 0 && menuIdx < shulkerContainerSize:
		return shulkerSlotRef{s: s, inv: inv, boxIdx: menuIdx, invSlot: -1, ok: true}
	case menuIdx >= shulkerContainerSize && menuIdx < shulkerContainerSize+27:
		// main: menu 27..53 -> window slots 9..35
		return shulkerSlotRef{s: s, inv: inv, boxIdx: -1, invSlot: int16(windowMainFirst + (menuIdx - shulkerContainerSize)), ok: true}
	case menuIdx >= shulkerContainerSize+27 && menuIdx < shulkerMenuSize:
		// hotbar: menu 54..62 -> window slots 36..44
		return shulkerSlotRef{s: s, inv: inv, boxIdx: -1, invSlot: int16(windowHotbarFirst + (menuIdx - shulkerContainerSize - 27)), ok: true}
	}
	return shulkerSlotRef{}
}

func (r shulkerSlotRef) get() component.SlotData {
	if r.boxIdx >= 0 {
		return r.s.items[r.boxIdx]
	}
	return r.inv.get(r.invSlot)
}

func (r shulkerSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.boxIdx >= 0 {
		r.s.items[r.boxIdx] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// mayPlace ports ShulkerBoxSlot.mayPlace for the container slots (a player-inventory cell always accepts):
// a container slot rejects a shulker box item (canFitInsideContainerItems == false). CITE ShulkerBoxSlot.
func (r shulkerSlotRef) mayPlace(stack component.SlotData) bool {
	if r.boxIdx < 0 {
		return true // a player-inventory slot has no shulker gate
	}
	return !stackIsShulkerBoxItem(stack)
}

// clickedShulker ports AbstractContainerMenu.clicked for an open shulker window: snapshot the BE items +
// inventory + cursor, run the click under a panic-recover (no partial mutation on a throw), then re-send the
// authoritative content + cursor. PICKUP/QUICK_MOVE/SWAP/CLONE/THROW/PICKUP_ALL are supported (the same
// subset dispenser_menu.go covers), each gated by the shulker slot mayPlace so a nested box is rejected.
func (t *TickLoop) clickedShulker(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	s := t.shulkers[oc.shulkerPos]
	if s == nil {
		t.sendContent(p)
		return
	}
	inv := ensureInventory(p)

	itemsBefore := s.items
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.items = itemsBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doShulkerClick(p, oc, s, inv, int(slotNum), button, int(input))
	}()

	// A click may have moved items into/out of the shulker container (a persistent state change): dirty the
	// column so the shulker state flushes on the next save pass.
	t.markShulkerDirty(oc.shulkerPos)

	t.sendShulkerContent(p, s)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doShulkerClick dispatches the supported click inputs over the shulker window (PICKUP/QUICK_MOVE/SWAP/
// CLONE/THROW/PICKUP_ALL). An unsupported input is a no-op (authoritative content re-sent regardless).
// Mirrors doDispenserClick.
func (t *TickLoop) doShulkerClick(p *tickPlayer, oc *openContainer, s *shulkerBE, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.shulkerPickup(p, s, inv, i, j)
	case containerInputQuickMove:
		t.shulkerQuickMove(p, s, inv, i)
	case containerInputQuickCraft:
		t.menuDoQuickCraft(p, shulkerMenuView(s, inv), i, j)
	case containerInputSwap:
		t.menuDoSwap(p, shulkerMenuView(s, inv), i, j)
	case containerInputClone:
		t.menuDoClone(p, shulkerMenuView(s, inv), i)
	case containerInputThrow:
		t.shulkerThrow(p, s, inv, i, j)
	case containerInputPickupAll:
		t.menuDoPickupAll(p, shulkerMenuView(s, inv), i, j)
	}
}

// shulkerPickup ports the PICKUP branch over the shulker window: left-click (j==0) whole stack, right-click
// (j==1) half/one, merging same-item stacks and swapping different ones. Mirrors dispenserPickup over
// shulkerSlotRef, but a place INTO a container slot is gated by mayPlace (no nested shulker box).
func (t *TickLoop) shulkerPickup(p *tickPlayer, s *shulkerBE, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := shulkerResolveSlot(s, inv, i)
	if !ref.ok {
		return
	}
	primary := j == 0
	slotItem := ref.get()
	carried := inv.getCarried()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) {
			if !ref.mayPlace(carried) {
				return // Slot.mayPlace false (a shulker box into a shulker container slot): no-op.
			}
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(shulkerSafeInsert(ref, &c, place))
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
		if !ref.mayPlace(carried) {
			return
		}
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(shulkerSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		// swap different items -- but only if the carried may be placed into the destination slot.
		if !ref.mayPlace(carried) {
			return
		}
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// shulkerSafeInsert ports Slot.safeInsert over a shulkerSlotRef (container + player cells). The mayPlace
// gate is enforced by the caller before this runs; here it is a pure stack merge. Mirrors dispenserSafeInsert.
func shulkerSafeInsert(ref shulkerSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// shulkerQuickMove ports ShulkerBoxMenu.quickMoveStack: a container slot (0..26) moves into the player
// inventory (moveItemStackTo over window main+hotbar, reverse=true so hotbar fills first); a player slot
// moves into the container (0..26, gated by the nested-box reject). CITE ShulkerBoxMenu.quickMoveStack
// (i < containerSize ? moveTo(size,slots.size(),true) : moveTo(0,containerSize,false)).
func (t *TickLoop) shulkerQuickMove(p *tickPlayer, s *shulkerBE, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := shulkerResolveSlot(s, inv, i)
	if !ref.ok {
		return
	}
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	if ref.boxIdx >= 0 {
		// Container -> player inventory: moveItemStackTo(stack, containerSize, slots.size(), true) over the
		// player WINDOW main(9..35)+hotbar(36..44), EXCLUDING offhand 45 (the shulker menu has no offhand),
		// reverse=true (hotbar-first fill). CITE ShulkerBoxMenu.quickMoveStack (box branch).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
	} else {
		// Player -> container (0..26): moveItemStackTo(stack, 0, containerSize, false), gated by mayPlace.
		if stackIsShulkerBoxItem(work) {
			return // a shulker box cannot quick-move into a shulker container (ShulkerBoxSlot.mayPlace false)
		}
		if !t.shulkerMoveInto(s, &work) {
			return
		}
	}
	ref.set(work)
}

// shulkerMoveInto ports moveItemStackTo for the shulker container destination: pass 1 merges *stack into
// existing same-item container slots up to the slot max, pass 2 fills empty container slots. Mirrors
// dispenserMoveInto over the 27 container cells (forward, no reverse). MUTATES *stack. Returns whether
// anything moved.
func (t *TickLoop) shulkerMoveInto(s *shulkerBE, stack *component.SlotData) bool {
	moved := false
	if stackIsStackable(*stack) {
		for i := 0; i < shulkerContainerSize && !stackEmpty(*stack); i++ {
			existing := s.items[i]
			if stackEmpty(existing) || !stackSameItemSameComponents(*stack, existing) {
				continue
			}
			sum := int(existing.Count) + int(stack.Count)
			slotMax := chestSlotMax(existing)
			if sum <= slotMax {
				existing.Count = toVar(sum)
				s.items[i] = existing
				stack.Count = 0
				moved = true
			} else if int(existing.Count) < slotMax {
				stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
				existing.Count = toVar(slotMax)
				s.items[i] = existing
				moved = true
			}
		}
	}
	if !stackEmpty(*stack) {
		for i := 0; i < shulkerContainerSize; i++ {
			if !stackEmpty(s.items[i]) {
				continue
			}
			slotMax := chestSlotMax(*stack)
			place := int(stack.Count)
			if slotMax < place {
				place = slotMax
			}
			s.items[i] = stackCopyWithCount(*stack, place)
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

// shulkerThrow ports the THROW branch over a shulker window: with an empty cursor, Q drops 1 (j==0) or the
// whole stack (j==1) from the slot under the cursor into the world. Mirrors dispenserThrow.
func (t *TickLoop) shulkerThrow(p *tickPlayer, s *shulkerBE, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := shulkerResolveSlot(s, inv, i)
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

// closeShulkerWindow ports ShulkerBoxMenu.removed -> super.removed + container.stopOpen: the 27 slots ARE
// the block-entity container (already authoritative in the tick-owned shulkerBE), so close FREES the window
// (the caller clears p.openContainer) and runs stopOpen (decrement openCount, play the CLOSE sound on the
// last closer, begin the CLOSING lid animation). NOTHING is returned to the player (the items persist in the
// BE). The carried-item return is handled by the shared handleContainerClose path. CITE ShulkerBoxMenu.removed.
func (t *TickLoop) closeShulkerWindow(_ *tickPlayer, oc *openContainer) {
	s := t.shulkers[oc.shulkerPos]
	if s == nil {
		return
	}
	// container.stopOpen(player): decrement the opener count, play the CLOSE sound + begin CLOSING when the
	// last viewer leaves. The shulkerBE persists in t.shulkers (the chest/dispenser persist model).
	t.shulkerStopOpen(oc.shulkerPos, s)
	t.markShulkerDirty(oc.shulkerPos)
}

// shulkerMenuView adapts the OPEN SHULKER window (container 0..26 + player 27..62) to the generic menuView
// so the shared SWAP / CLONE / PICKUP_ALL branches (menu_click.go) drive it. A container cell is a
// ShulkerBoxSlot (mayPlace = canFitInsideContainerItems -> reject nested box); a player cell is a plain Slot.
// CITE AbstractContainerMenu.doClick over the ShulkerBoxMenu slot layout.
func shulkerMenuView(s *shulkerBE, inv *Inventory) menuView {
	return menuView{
		size: shulkerMenuSize,
		inv:  inv,
		slotAt: func(idx int) menuSlotView {
			ref := shulkerResolveSlot(s, inv, idx)
			if !ref.ok {
				return menuSlotView{}
			}
			return menuSlotView{
				ok:            true,
				getItem:       func() component.SlotData { return ref.get() },
				setByPlayer:   func(sd component.SlotData) { ref.set(sd) },
				mayPickupFn:   func(*tickPlayer) bool { return true },
				mayPlaceFn:    func(sd component.SlotData) bool { return ref.mayPlace(sd) },
				maxStackFn:    func(sd component.SlotData) int { return chestSlotMax(sd) },
				onSwapCraftFn: func(int) {},
				onTakeFn:      func(*tickPlayer, component.SlotData) {},
			}
		},
		canTakeItemForPickAll: func(component.SlotData, int) bool { return true },
	}
}
