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
	case containerInputPickup:
		t.chestPickup(p, cl, inv, i, j)
	case containerInputQuickMove:
		t.chestQuickMove(p, cl, inv, i)
	case containerInputThrow:
		t.chestThrow(p, cl, inv, i, j)
	}
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
