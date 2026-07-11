package server

// ender_chest_menu.go -- the ENDER CHEST MENU: a GENERIC_9x3 (27-slot) container UI over the PER-PLAYER
// ender inventory (PlayerEnderChestContainer), a 1:1 port of EnderChestBlock.useWithoutItem ->
// player.openMenu(new SimpleMenuProvider(ChestMenu.threeRows(...), "container.enderchest")) over the 26.2
// jar (temp/cache/26.2-inner.jar, javap -c -p this session). The inventory follows the PLAYER, NOT the
// block: p.enderItems is the 27-slot PlayerEnderChestContainer, shared across every ender chest the player
// opens. The engine mirrors dispenser_menu.go over 27 slots, but the container backing is p.enderItems.
//
// 1:1 jar chain (VERIFIED javap this session):
//
//	EnderChestBlock.useWithoutItem(state, level, pos, player, hit):
//	    PlayerEnderChestContainer inv = player.getEnderChestInventory();
//	    BlockEntity be = level.getBlockEntity(pos);
//	    if (inv == null || !(be instanceof EnderChestBlockEntity ec)) return SUCCESS;
//	    if (level.getBlockState(pos.above()).isRedstoneConductor(level, pos.above())) return SUCCESS;  // blocked
//	    if (level instanceof ServerLevel sl) {
//	        inv.setActiveChest(ec);
//	        player.openMenu(new SimpleMenuProvider((id,pinv,p) -> ChestMenu.threeRows(id, pinv, inv), CONTAINER_TITLE));
//	        player.awardStat(Stats.OPEN_ENDERCHEST);
//	        PiglinAi.angerNearbyPiglins(sl, player, true);
//	    }
//	    return SUCCESS;
//	PlayerEnderChestContainer.startOpen(user): if(activeChest!=null) activeChest.startOpen(user); super.startOpen.
//	PlayerEnderChestContainer.stopOpen(user):  if(activeChest!=null) activeChest.stopOpen(user); super.stopOpen; activeChest=null.
//	ChestMenu.threeRows: a GENERIC_9x3 over the 27-slot container + standard inventory slots.
//
// v1 subset (cited): no stats/piglin/spectator. The setActiveChest binding + startOpen/stopOpen drive the
// EnderChestBlockEntity opener count (the sound + lid animation) at the OPENED block (enderChestPos).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// enderChestContainerSize is PlayerEnderChestContainer's size (27 = ChestMenu.threeRows). CITE
// PlayerEnderChestContainer() -> super(27).
const enderChestContainerSize = 27

// enderChestMenuSize is the ender-chest-window slot count: 27 container + 27 main + 9 hotbar = 63 (the same
// GENERIC_9x3 layout as a chest). CITE ChestMenu.threeRows (three rows) + addStandardInventorySlots.
const enderChestMenuSize = enderChestContainerSize + 27 + 9 // 63

// enderChestTitle is the ender-chest window display name. Vanilla EnderChestBlock.CONTAINER_TITLE is the
// translatable "container.enderchest" -> "Ender Chest". v1 sends the plain literal.
const enderChestTitle = "Ender Chest"

// ensureEnderItems lazily allocates the player's 27-slot PlayerEnderChestContainer backing (p.enderItems).
// A fresh/never-opened player has a nil slice; this pads it to exactly 27 empty slots. Idempotent. CITE
// PlayerEnderChestContainer() -> super(27) (NonNullList.withSize(27, EMPTY)).
func ensureEnderItems(p *tickPlayer) []component.SlotData {
	if len(p.enderItems) >= enderChestContainerSize {
		p.enderItems = p.enderItems[:enderChestContainerSize]
		return p.enderItems
	}
	padded := make([]component.SlotData, enderChestContainerSize)
	copy(padded, p.enderItems)
	p.enderItems = padded
	return p.enderItems
}

// openEnderChest ports EnderChestBlock.useWithoutItem -> ServerPlayer.openMenu over the per-player ender
// inventory: check the above-conductor lid gate, bind the block-entity as the active chest, run startOpen
// (bump the openersCounter -> ENDER_CHEST_OPEN sound + lid), allocate a windowId, send OpenScreen +
// ContainerSetContent. Returns true (SUCCESS -> placement skipped) even when blocked (still consumes).
func (t *TickLoop) openEnderChest(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	// getBlockState(pos.above()).isRedstoneConductor -> the lid cannot open, return SUCCESS (consume, no menu).
	above := pk.Position{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if as, ok := t.world().GetBlock(above, dimMinY); ok && block.IsRedstoneConductor(as) {
		return true
	}
	b := t.resolveEnderChest(pos)
	if b == nil {
		return false
	}
	ensureEnderItems(p)

	// PlayerEnderChestContainer.setActiveChest(ec) + startOpen -> EnderChestBlockEntity opener increment
	// (the ENDER_CHEST_OPEN sound + lid opening at the opened block).
	t.enderChestIncrementOpeners(pos, b)

	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindEnderChest, enderChestPos: pos}

	// connection.send(new ClientboundOpenScreenPacket(containerId, generic_9x3, CONTAINER_TITLE)).
	menuID := menuTypeID(registryid.Menu, "minecraft:generic_9x3")
	p.client.Send(openScreen(int32(win), menuID, enderChestTitle))

	t.sendEnderChestContent(p)
	return true
}

// enderChestMenuItems builds the 63-slot ContainerSetContent list: ender inventory 0..26, then the player
// inventory -- main 27..53 (window 9..35) then hotbar 54..62 (window 36..44). Same layout as the chest.
func enderChestMenuItems(p *tickPlayer, inv *Inventory) []component.SlotData {
	ei := ensureEnderItems(p)
	out := make([]component.SlotData, enderChestMenuSize)
	for i := 0; i < enderChestContainerSize; i++ {
		out[i] = ei[i]
	}
	for i := 0; i < 27; i++ {
		out[enderChestContainerSize+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[enderChestContainerSize+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendEnderChestContent pushes the authoritative ContainerSetContent for the open ender-chest window. Bumps
// the player inventory state id so the client tracks the authoritative state.
func (t *TickLoop) sendEnderChestContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindEnderChest {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		enderChestMenuItems(p, inv), inv.getCarried()))
}

// enderChestSlotRef resolves an ender-chest-WINDOW slot index (0..62) to the concrete backing: container
// slots 0..26 -> p.enderItems; player slots 27..62 -> the player inventory window slot (main 9..35, hotbar
// 36..44). The ender-chest ChestMenu slot->container mapping.
type enderChestSlotRef struct {
	p       *tickPlayer
	inv     *Inventory
	eiIdx   int   // index into p.enderItems, -1 for a player slot
	invSlot int16 // player inventory window slot, -1 for an ender slot
	ok      bool
}

func enderChestResolveSlot(p *tickPlayer, inv *Inventory, menuIdx int) enderChestSlotRef {
	switch {
	case menuIdx >= 0 && menuIdx < enderChestContainerSize:
		return enderChestSlotRef{p: p, inv: inv, eiIdx: menuIdx, invSlot: -1, ok: true}
	case menuIdx >= enderChestContainerSize && menuIdx < enderChestContainerSize+27:
		return enderChestSlotRef{p: p, inv: inv, eiIdx: -1, invSlot: int16(windowMainFirst + (menuIdx - enderChestContainerSize)), ok: true}
	case menuIdx >= enderChestContainerSize+27 && menuIdx < enderChestMenuSize:
		return enderChestSlotRef{p: p, inv: inv, eiIdx: -1, invSlot: int16(windowHotbarFirst + (menuIdx - enderChestContainerSize - 27)), ok: true}
	}
	return enderChestSlotRef{}
}

func (r enderChestSlotRef) get() component.SlotData {
	if r.eiIdx >= 0 {
		return r.p.enderItems[r.eiIdx]
	}
	return r.inv.get(r.invSlot)
}

func (r enderChestSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.eiIdx >= 0 {
		r.p.enderItems[r.eiIdx] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// clickedEnderChest ports AbstractContainerMenu.clicked for an open ender-chest window: snapshot the ender
// items + inventory + cursor, run the click under a panic-recover, then re-send the authoritative content +
// cursor. PICKUP/QUICK_MOVE/SWAP/CLONE/THROW/PICKUP_ALL are supported (the dispenser subset). Mirrors
// clickedDispenser over the per-player ender container.
func (t *TickLoop) clickedEnderChest(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)
	ei := ensureEnderItems(p)

	itemsBefore := make([]component.SlotData, len(ei))
	copy(itemsBefore, ei)
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				copy(p.enderItems, itemsBefore)
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doEnderChestClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	t.sendEnderChestContent(p)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
	_ = oc
}

// doEnderChestClick dispatches the supported click inputs over the ender-chest window. An unsupported input
// is a no-op. Mirrors doDispenserClick.
func (t *TickLoop) doEnderChestClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
	_ = oc
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.enderChestPickup(p, inv, i, j)
	case containerInputQuickMove:
		t.enderChestQuickMove(p, inv, i)
	case containerInputQuickCraft:
		t.menuDoQuickCraft(p, enderChestMenuView(p, inv), i, j)
	case containerInputSwap:
		t.menuDoSwap(p, enderChestMenuView(p, inv), i, j)
	case containerInputClone:
		t.menuDoClone(p, enderChestMenuView(p, inv), i)
	case containerInputThrow:
		t.enderChestThrow(p, inv, i, j)
	case containerInputPickupAll:
		t.menuDoPickupAll(p, enderChestMenuView(p, inv), i, j)
	}
}

// enderChestPickup ports the PICKUP branch over the ender-chest window: left-click (j==0) whole stack,
// right-click (j==1) half/one, merging same-item stacks and swapping different ones. Mirrors dispenserPickup.
func (t *TickLoop) enderChestPickup(p *tickPlayer, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := enderChestResolveSlot(p, inv, i)
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
			ref.set(enderChestSafeInsert(ref, &c, place))
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
		ref.set(enderChestSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// enderChestSafeInsert ports Slot.safeInsert over an enderChestSlotRef (every ender + player cell accepts
// any item -- Slot.mayPlace is true). Mirrors dispenserSafeInsert.
func enderChestSafeInsert(ref enderChestSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// enderChestQuickMove ports ChestMenu.quickMoveStack: an ender slot (0..26) moves into the player inventory
// (moveItemStackTo over window 9..44, reverse=true); a player slot moves into the ender inventory (0..26).
func (t *TickLoop) enderChestQuickMove(p *tickPlayer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := enderChestResolveSlot(p, inv, i)
	if !ref.ok {
		return
	}
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	if ref.eiIdx >= 0 {
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
	} else {
		if !t.enderChestMoveInto(p, &work) {
			return
		}
	}
	ref.set(work)
}

// enderChestMoveInto ports moveItemStackTo for the ender container destination: pass 1 merges *stack into
// existing same-item ender slots up to the slot max, pass 2 fills empty ender slots. Mirrors dispenserMoveInto.
func (t *TickLoop) enderChestMoveInto(p *tickPlayer, stack *component.SlotData) bool {
	ei := ensureEnderItems(p)
	moved := false
	if stackIsStackable(*stack) {
		for i := 0; i < enderChestContainerSize && !stackEmpty(*stack); i++ {
			existing := ei[i]
			if stackEmpty(existing) || !stackSameItemSameComponents(*stack, existing) {
				continue
			}
			sum := int(existing.Count) + int(stack.Count)
			slotMax := chestSlotMax(existing)
			if sum <= slotMax {
				existing.Count = toVar(sum)
				ei[i] = existing
				stack.Count = 0
				moved = true
			} else if int(existing.Count) < slotMax {
				stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
				existing.Count = toVar(slotMax)
				ei[i] = existing
				moved = true
			}
		}
	}
	if !stackEmpty(*stack) {
		for i := 0; i < enderChestContainerSize; i++ {
			if !stackEmpty(ei[i]) {
				continue
			}
			slotMax := chestSlotMax(*stack)
			place := int(stack.Count)
			if slotMax < place {
				place = slotMax
			}
			ei[i] = stackCopyWithCount(*stack, place)
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

// enderChestThrow ports the THROW branch over an ender-chest window: with an empty cursor, Q drops 1 (j==0)
// or the whole stack (j==1) from the slot under the cursor into the world. Mirrors dispenserThrow.
func (t *TickLoop) enderChestThrow(p *tickPlayer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := enderChestResolveSlot(p, inv, i)
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

// closeEnderChestWindow ports PlayerEnderChestContainer.stopOpen + the SimpleMenuProvider ChestMenu.removed:
// the 27 ender slots ARE the per-player inventory (already authoritative in p.enderItems -- every click
// mutated it in place), so close FREES the window and runs stopOpen on the active EnderChestBlockEntity
// (decrement the openersCounter -> ENDER_CHEST_CLOSE sound + lid closing), then unbinds the active chest
// (activeChest = null). NOTHING is returned to the player (the items are the player's ender inventory). The
// carried-item return is handled by the shared handleContainerClose path. CITE PlayerEnderChestContainer.stopOpen.
func (t *TickLoop) closeEnderChestWindow(_ *tickPlayer, oc *openContainer) {
	b := t.enderChests[oc.enderChestPos]
	if b == nil {
		return
	}
	t.enderChestDecrementOpeners(oc.enderChestPos, b)
	// activeChest = null: the binding is released implicitly (the openContainer is cleared by the caller).
}

// enderChestMenuView adapts the OPEN ENDER-CHEST window to the generic menuView so the shared SWAP / CLONE /
// PICKUP_ALL branches drive it. Every ender + player cell is a plain Slot (mayPickup/mayPlace true). CITE
// ChestMenu doClick over the GENERIC_9x3 slot layout.
func enderChestMenuView(p *tickPlayer, inv *Inventory) menuView {
	return menuView{
		size: enderChestMenuSize,
		inv:  inv,
		slotAt: func(idx int) menuSlotView {
			ref := enderChestResolveSlot(p, inv, idx)
			if !ref.ok {
				return menuSlotView{}
			}
			return menuSlotView{
				ok:            true,
				getItem:       func() component.SlotData { return ref.get() },
				setByPlayer:   func(sd component.SlotData) { ref.set(sd) },
				mayPickupFn:   func(*tickPlayer) bool { return true },
				mayPlaceFn:    func(component.SlotData) bool { return true },
				maxStackFn:    func(sd component.SlotData) int { return chestSlotMax(sd) },
				onSwapCraftFn: func(int) {},
				onTakeFn:      func(*tickPlayer, component.SlotData) {},
			}
		},
		canTakeItemForPickAll: func(component.SlotData, int) bool { return true },
	}
}
