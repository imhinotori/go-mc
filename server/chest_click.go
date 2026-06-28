package server

// chest_click.go — the ContainerClick + ContainerSetContent slot engine for an OPEN chest window
// (STRUCT-POLISH-01). The player-inventory window (id 0) keeps its full doClick engine
// (inventory_doclick.go) untouched; this is the chest-window analogue covering the operations a
// player actually uses on a chest: PICKUP (left/right click to take/put a stack via the cursor),
// QUICK_MOVE (shift-click to move a stack between the chest and the player inventory), and THROW
// (Q to drop). Each ports the matching AbstractContainerMenu.doClick branch over the ChestMenu slot
// layout (chest 0..26, player main 27..53, hotbar 54..62), reusing the same ItemStack primitives
// (stackSplit/safeInsert-style merge/moveItemStackTo) the player engine uses — re-expressed over the
// two-container chest view, NOT pasted. The carried cursor is the player's inv.carried (vanilla
// AbstractContainerMenu.carried — one open menu at a time, so the single cursor is faithful).
//
// Jar cites: net.minecraft.world.inventory.AbstractContainerMenu.doClick (PICKUP/QUICK_MOVE/THROW
// branches) + ChestMenu.quickMoveStack (the 0..containerSize ↔ player-inventory shift routing).

import (
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chestSlotRef resolves a chest-WINDOW slot index (0..62) to the concrete container backing and its
// local index: chest slots 0..26 → the chest container; player slots 27..62 → the player inventory
// window slot (main 9..35, hotbar 36..44). An out-of-range index returns ok=false. This is the
// ChestMenu slot→container mapping (addChestGrid then addStandardInventorySlots).
type chestSlotRef struct {
	chest    *chestLoot // non-nil when the slot is a chest slot
	inv      *Inventory // non-nil when the slot is a player slot
	chestIdx int        // index into chest.items   (chest slot)
	invSlot  int16      // player inventory window slot (player slot)
	ok       bool
}

func chestResolveSlot(cl *chestLoot, inv *Inventory, menuIdx int) chestSlotRef {
	switch {
	case menuIdx >= 0 && menuIdx < chestContainerSize:
		return chestSlotRef{chest: cl, chestIdx: menuIdx, ok: true}
	case menuIdx >= chestContainerSize && menuIdx < chestContainerSize+27:
		// main: menu 27..53 → window slots 9..35
		return chestSlotRef{inv: inv, invSlot: int16(windowMainFirst + (menuIdx - chestContainerSize)), ok: true}
	case menuIdx >= chestContainerSize+27 && menuIdx < chestMenuSize:
		// hotbar: menu 54..62 → window slots 36..44
		return chestSlotRef{inv: inv, invSlot: int16(windowHotbarFirst + (menuIdx - chestContainerSize - 27)), ok: true}
	}
	return chestSlotRef{}
}

func (r chestSlotRef) get() component.SlotData {
	if r.chest != nil {
		return r.chest.items[r.chestIdx]
	}
	return r.inv.get(r.invSlot)
}

func (r chestSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.chest != nil {
		r.chest.items[r.chestIdx] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// clickedChest ports AbstractContainerMenu.clicked for an open chest window: snapshot the chest +
// player + cursor, run the chest doClick under a panic-recover (the vanilla clicked() try block —
// no partial mutation leaks on a throw, T-6-04), then re-send the authoritative chest content
// (broadcastChanges) and sync the carried cursor. The whole chest window is re-sent (not a per-slot
// diff) — simpler than the player engine's slot diff and correct (the client snaps to authoritative).
func (t *TickLoop) clickedChest(p *tickPlayer, cl *chestLoot, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	chestBefore := append([]component.SlotData(nil), cl.items...)
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				copy(cl.items, chestBefore)
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doChestClick(p, cl, inv, int(slotNum), button, int(input))
	}()

	// SUB-PERSIST: a chest click may have moved/taken items, so dirty the chest's column so the save
	// loop flushes the rolled container to the chunk BE on the next save pass (a no-op when
	// persistence is off / the column is unloaded). The first open already cleared the LootTable
	// (unpackLootTable), so the chest now persists its Items (saveAllItems), not its loot table.
	if p.openContainer != nil {
		t.markChestDirty(p.openContainer.chestPos)
	}

	// broadcastChanges: re-send the full authoritative chest window so the client reflects the move.
	t.sendChestContent(p, cl)

	// synchronizeCarriedToRemote: sync the cursor on change (ClientboundContainerSetSlot(-1, ...)).
	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(containerSetSlot(-1, inv.stateID, -1, inv.getCarried()))
	}
}

// doChestClick ports the AbstractContainerMenu.doClick branches that operate on a chest window. The
// supported ContainerInputs are PICKUP, QUICK_MOVE, and THROW — the operations a chest actually
// receives. QUICK_CRAFT/SWAP/CLONE/PICKUP_ALL on a chest are out of the v1 chest subset (a forged
// or unsupported input is a no-op, never a panic — the authoritative content is re-sent regardless).
func (t *TickLoop) doChestClick(p *tickPlayer, cl *chestLoot, inv *Inventory, i, j, input int) {
	switch input {
	case containerInputQuickCraft:
		t.doChestQuickCraft(p, cl, inv, i, j)
	case containerInputPickup, containerInputQuickMove:
		// A PICKUP/QUICK_MOVE while a drag is mid-flight resets the drag and returns (doClick
		// @547-560). Mirrors the player engine's guard so a chest drag cancels cleanly.
		if inv.quickcraftStatus != 0 {
			inv.resetQuickCraft()
			return
		}
		if input == containerInputPickup {
			t.chestPickup(p, cl, inv, i, j)
		} else {
			t.chestQuickMove(p, cl, inv, i)
		}
	case containerInputThrow:
		t.chestThrow(p, cl, inv, i, j)
	case containerInputPickupAll:
		t.chestPickupAll(p, cl, inv, i, j)
	}
}

// chestPickupAll ports the PICKUP_ALL branch of doClick (double-click: collect every matching item
// in the WHOLE window onto the cursor) over the chest window — the operator's "doble click en un
// item me deberia tomar todos los del cofre". It sweeps all 63 chest-window slots (chest 0..26 +
// player 27..62) in two passes (non-full stacks first, then full ones), filling the cursor up to
// its max stack. CITE AbstractContainerMenu.doClick PICKUP_ALL (offsets 1640-1840).
func (t *TickLoop) chestPickupAll(p *tickPlayer, cl *chestLoot, inv *Inventory, i, j int) {
	if i < 0 {
		return
	}
	carried := inv.getCarried()
	if stackEmpty(carried) {
		return
	}
	// @1649: if the clicked slot itself has a takeable matching item, a normal pickup handled it.
	clicked := chestResolveSlot(cl, inv, i)
	if clicked.ok && !stackEmpty(clicked.get()) && stackSameItemSameComponents(clicked.get(), carried) {
		return
	}

	start, dir := 0, 1
	if j != 0 {
		start, dir = chestMenuSize-1, -1
	}
	for pass := 0; pass < 2; pass++ {
		k := start
		for k >= 0 && k < chestMenuSize && int(inv.getCarried().Count) < stackMaxSize(inv.getCarried()) {
			ref := chestResolveSlot(cl, inv, k)
			cur := inv.getCarried()
			if ref.ok {
				slot := ref.get()
				if !stackEmpty(slot) && stackSameItemSameComponents(slot, cur) {
					// pass 0: skip already-full stacks (leave them for pass 1), matching the vanilla
					// "pass != 0 || count != maxStackSize" guard, so half-stacks merge first.
					if pass != 0 || int(slot.Count) != stackMaxSize(slot) {
						room := stackMaxSize(cur) - int(cur.Count)
						take := int(slot.Count)
						if room < take {
							take = room
						}
						if take > 0 {
							slot.Count = toVar(int(slot.Count) - take)
							if slot.Count <= 0 {
								slot = component.SlotData{Count: 0}
							}
							ref.set(slot)
							cur.Count += toVar(take)
							inv.setCarried(cur)
						}
					}
				}
			}
			k += dir
		}
	}
}

// chestQuickRef is the chest-window analogue of menuSlot for the QUICK_CRAFT (mouse-drag) engine:
// it wraps a resolved chest-window slot (chest container OR player inventory cell) so the drag
// state machine can read/write/space-check it uniformly. mayPlace is always true (a chest/inv slot
// accepts any item — there is no armor/result restriction in this window), matching Slot.mayPlace
// for a ChestMenu/Inventory slot.
type chestQuickRef struct {
	ref chestSlotRef
}

func (q chestQuickRef) hasItem() bool                     { return !stackEmpty(q.ref.get()) }
func (q chestQuickRef) getItem() component.SlotData       { return q.ref.get() }
func (q chestQuickRef) set(s component.SlotData)          { q.ref.set(s) }
func (q chestQuickRef) maxStack(s component.SlotData) int { return chestSlotMax(s) }

// doChestQuickCraft ports the QUICK_CRAFT drag state machine (doClick offsets 14-544) over the OPEN
// CHEST window, distributing the carried stack across the dragged set of chest+inventory slots —
// the operation the operator reported missing ("no puedo dejar varios items arrastrando"). The drag
// STATE (quickcraftStatus/Type/Slots) is the player inventory's, exactly as the player engine uses
// it (AbstractContainerMenu owns one drag at a time; the open chest IS that one menu). Each slot id
// is resolved through chestResolveSlot so it addresses either a chest cell (0..26) or a player cell
// (27..62). 1:1 with doClickQuickCraft, re-expressed over the two-container chest view.
func (t *TickLoop) doChestQuickCraft(p *tickPlayer, cl *chestLoot, inv *Inventory, i, j int) {
	header := getQuickcraftHeader(j)
	prevStatus := inv.quickcraftStatus
	inv.quickcraftStatus = header

	if !((prevStatus == 1 && inv.quickcraftStatus == 2) || (prevStatus == inv.quickcraftStatus)) {
		inv.resetQuickCraft()
		return
	}
	if stackEmpty(inv.getCarried()) {
		inv.resetQuickCraft()
		return
	}

	switch inv.quickcraftStatus {
	case 0: // START
		inv.quickcraftType = getQuickcraftType(j)
		if isValidQuickcraftType(inv.quickcraftType, p) {
			inv.quickcraftStatus = 1
			inv.quickcraftSlots = inv.quickcraftSlots[:0]
		} else {
			inv.resetQuickCraft()
		}
		return

	case 1: // ADD slot i to the drag set
		ref := chestResolveSlot(cl, inv, i)
		if !ref.ok {
			return
		}
		q := chestQuickRef{ref: ref}
		carried := inv.getCarried()
		if chestCanQuickReplace(q, carried, true) &&
			(inv.quickcraftType == 2 || int(carried.Count) > len(inv.quickcraftSlots)) {
			inv.addQuickcraftSlot(i)
		}
		return

	case 2: // END — distribute the carried stack across the drag set
		if len(inv.quickcraftSlots) != 0 {
			if len(inv.quickcraftSlots) == 1 {
				idx := inv.quickcraftSlots[0]
				inv.resetQuickCraft()
				// A single-slot drag is just a PICKUP on that chest-window slot.
				t.doChestClick(p, cl, inv, idx, inv.quickcraftType, containerInputPickup)
				return
			}
			carriedCopy := inv.getCarried()
			if stackEmpty(carriedCopy) {
				inv.resetQuickCraft()
				return
			}
			remaining := int(inv.getCarried().Count)
			setSize := len(inv.quickcraftSlots)
			for _, idx := range inv.quickcraftSlots {
				ref := chestResolveSlot(cl, inv, idx)
				if !ref.ok {
					continue
				}
				q := chestQuickRef{ref: ref}
				carried := inv.getCarried()
				if chestCanQuickReplace(q, carried, true) &&
					(inv.quickcraftType == 2 || int(carried.Count) >= setSize) {
					existing := 0
					if q.hasItem() {
						existing = int(q.getItem().Count)
					}
					slotMax := stackMaxSize(carriedCopy)
					if sm := q.maxStack(carriedCopy); sm < slotMax {
						slotMax = sm
					}
					give := getQuickCraftPlaceCount(setSize, inv.quickcraftType, carriedCopy) + existing
					if slotMax < give {
						give = slotMax
					}
					remaining -= give - existing
					q.set(stackCopyWithCount(carriedCopy, give))
				}
			}
			carriedCopy.Count = pk.VarInt(remaining)
			inv.setCarried(carriedCopy)
		}
		inv.resetQuickCraft()
		return
	}

	inv.resetQuickCraft()
}

// chestCanQuickReplace ports canItemQuickReplace over a chest-window slot (mayPlace is implicitly
// true for chest/inventory cells): an empty slot is always replaceable; a same-item slot is
// replaceable iff there is room (count + (stackSizeMatters?0:stack.count) <= max); a different item
// is not.
func chestCanQuickReplace(q chestQuickRef, stack component.SlotData, stackSizeMatters bool) bool {
	if !q.hasItem() {
		return true
	}
	if stackSameItemSameComponents(stack, q.getItem()) {
		add := 0
		if !stackSizeMatters {
			add = int(stack.Count)
		}
		return int(q.getItem().Count)+add <= q.maxStack(stack)
	}
	return false
}

// chestPickup ports the PICKUP branch of doClick (offsets 561-1076, the real-slot arm) over the
// chest window: left-click (j==0) takes/puts the whole stack, right-click (j==1) takes half / puts
// one, merging same-item stacks and swapping different ones. Mirrors the player-engine logic in
// doClickPickupOrQuickMove's default arm, re-expressed over chestSlotRef (the two-container view).
func (t *TickLoop) chestPickup(p *tickPlayer, cl *chestLoot, inv *Inventory, i, j int) {
	if j != 0 && j != 1 { // @575 guard
		return
	}
	primary := j == 0
	if i < 0 {
		return // outside-drop on a chest window is not in the v1 subset
	}
	ref := chestResolveSlot(cl, inv, i)
	if !ref.ok {
		return
	}
	slotItem := ref.get()
	carried := inv.getCarried()

	if stackEmpty(slotItem) {
		// Empty slot: deposit from the cursor (whole stack on primary, one on secondary).
		if !stackEmpty(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(chestSafeInsert(ref, &c, place))
			inv.setCarried(c)
		}
		return
	}
	// Non-empty slot.
	if stackEmpty(carried) {
		// Take from the slot onto the cursor (whole on primary, ceil(half) on secondary).
		take := (int(slotItem.Count) + 1) / 2
		if primary {
			take = int(slotItem.Count)
		}
		rem := slotItem
		taken := stackSplit(&rem, take) // split `take` off, rem keeps the remainder
		ref.set(rem)
		inv.setCarried(taken)
		return
	}
	// Cursor non-empty + slot non-empty.
	if stackSameItemSameComponents(slotItem, carried) {
		// Merge cursor INTO slot (add whole on primary, one on secondary) up to the slot max.
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(chestSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		// Different item: swap cursor ↔ slot.
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// chestSlotMax ports Slot.getMaxStackSize(stack) for a chest/inventory slot = min(64, stack max).
// Every chest + player main/hotbar slot uses the container default 64.
func chestSlotMax(stack component.SlotData) int {
	mx := 64
	if sm := stackMaxSize(stack); sm < mx {
		mx = sm
	}
	return mx
}

// chestSafeInsert ports Slot.safeInsert(stack, increment) over a chestSlotRef: place up to
// min(increment, stack.count, slotMax - existing) of *stack into the slot (fill if empty / merge if
// same item), shrink *stack, and return the slot's NEW contents. *stack is mutated to the remainder.
func chestSafeInsert(ref chestSlotRef, stack *component.SlotData, increment int) component.SlotData {
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
		// setByPlayer(stack.split(add)): place a fresh stack of `add`, shrinking the source.
		return stackSplit(stack, add)
	}
	if stackSameItemSameComponents(existing, *stack) {
		stack.Count = toVar(int(stack.Count) - add) // ItemStack.shrink(add)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		existing.Count = toVar(int(existing.Count) + add) // ItemStack.grow(add)
		return existing
	}
	return existing
}

// toVar converts an int to the pk.VarInt the SlotData.Count field uses.
func toVar(n int) pk.VarInt { return pk.VarInt(n) }

// chestQuickMove ports ChestMenu.quickMoveStack: shift-click moves a stack between the chest and the
// player inventory. From a chest slot (0..26) the stack moves into the player main+hotbar
// (moveItemStackTo over window slots 9..44); from a player slot it moves into the chest container
// (0..26). Mirrors ChestMenu.quickMoveStack's `i < containerRows*9 ? moveTo(playerInv) :
// moveTo(container)` two-region routing. Reuses the player engine's moveItemStackTo for the
// player-inventory destination and a chest-local fill for the chest destination.
func (t *TickLoop) chestQuickMove(p *tickPlayer, cl *chestLoot, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := chestResolveSlot(cl, inv, i)
	if !ref.ok {
		return
	}
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src // the working stack moveItemStackTo shrinks

	if ref.chest != nil {
		// Chest → player inventory. ChestMenu.quickMoveStack moves a chest slot into the menu's
		// player region [containerRows*9, slots.size()) with reverse=true. That region is main then
		// hotbar, so reverse walks hotbar-first then main — exactly vanilla's hotbar-priority deposit.
		// Over Sulfur's player WINDOW the equivalent region is main(9..35)+hotbar(36..44) — slots
		// [9,45), EXCLUDING offhand 45 (the chest menu has no offhand slot). Reverse=true reproduces
		// the hotbar-first fill order. CITE ChestMenu.quickMoveStack (i < containerRows*9 branch).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
	} else {
		// Player → chest container (0..26).
		if !t.chestMoveInto(cl, &work) {
			return
		}
	}
	ref.set(work) // the source slot keeps the remainder (empty if fully moved)
}

// chestMoveInto ports moveItemStackTo for the chest container destination: pass 1 merges *stack into
// existing same-item chest slots up to the slot max, pass 2 fills empty chest slots. Mirrors
// AbstractContainerMenu.moveItemStackTo over the chest's 27 cells (forward, no reverse). MUTATES
// *stack (shrinks it). Returns whether anything moved.
func (t *TickLoop) chestMoveInto(cl *chestLoot, stack *component.SlotData) bool {
	moved := false
	// Pass 1: merge into existing same-item stacks.
	if stackIsStackable(*stack) {
		for i := 0; i < chestContainerSize && !stackEmpty(*stack); i++ {
			existing := cl.items[i]
			if stackEmpty(existing) || !stackSameItemSameComponents(*stack, existing) {
				continue
			}
			sum := int(existing.Count) + int(stack.Count)
			slotMax := chestSlotMax(existing)
			if sum <= slotMax {
				existing.Count = toVar(sum)
				cl.items[i] = existing
				stack.Count = 0
				moved = true
			} else if int(existing.Count) < slotMax {
				stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
				existing.Count = toVar(slotMax)
				cl.items[i] = existing
				moved = true
			}
		}
	}
	// Pass 2: fill empty slots.
	if !stackEmpty(*stack) {
		for i := 0; i < chestContainerSize; i++ {
			if !stackEmpty(cl.items[i]) {
				continue
			}
			slotMax := chestSlotMax(*stack)
			place := int(stack.Count)
			if slotMax < place {
				place = slotMax
			}
			cl.items[i] = stackCopyWithCount(*stack, place)
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

// chestThrow ports the THROW branch of doClick over a chest window: with an empty cursor, Q drops
// 1 (j==0) or the whole stack (j==1) from the slot under the cursor into the world as an ItemEntity.
func (t *TickLoop) chestThrow(p *tickPlayer, cl *chestLoot, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	ref := chestResolveSlot(cl, inv, i)
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
