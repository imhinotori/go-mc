package server

// loom_click.go -- the ContainerClick engine for an OPEN loom window, plus the RESULT-take chain
// (LoomMenu6.onTake -> consume 1 banner + 1 dye, clear the selection when either runs out). A sibling of
// grindstone_click.go over the LoomMenu slot layout (banner 0, dye 1, pattern 2, result 3, player main
// 4..30, hotbar 31..39). The result slot (3) is take-only (mayPlace==false); the three input slots gate
// placement via mayPlace (banner=BannerItem, dye=isDyeItem, pattern=isPatternItem). Supported
// ContainerInputs: PICKUP, QUICK_MOVE, SWAP, CLONE, THROW, PICKUP_ALL.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, javap this session):
//   onTakeLoom : net.minecraft.world.inventory.LoomMenu LoomMenu6.onTake (bannerSlot.remove(1);
//                dyeSlot.remove(1); if (!banner.hasItem || !dye.hasItem) selectedBannerPatternIndex.set(-1))
//   the click branches: net.minecraft.world.inventory.AbstractContainerMenu.clicked
//
// Tick-owned (TICK-05): every method runs on the tick goroutine, so the input/result mutation is race-clean.

import (
	"github.com/imhinotori/sulfur/level/component"
)

// loomSlotRef resolves a loom-WINDOW slot index (0..39) to its backing + role:
//
//	0     -> banner input (oc.loomBanner)
//	1     -> dye input (oc.loomDye)
//	2     -> pattern input (oc.loomPattern)
//	3     -> the result slot (oc.loomResult, take-only)
//	4..39 -> the player inventory window (main 9..35, hotbar 36..44)
type loomSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	input   int   // 0 banner, 1 dye, 2 pattern; -1 otherwise
	result  bool  // the result slot (3)
	invSlot int16 // player inventory window slot, -1 otherwise
	ok      bool
}

func loomResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) loomSlotRef {
	switch {
	case menuIdx == 0:
		return loomSlotRef{oc: oc, inv: inv, input: 0, invSlot: -1, ok: true}
	case menuIdx == 1:
		return loomSlotRef{oc: oc, inv: inv, input: 1, invSlot: -1, ok: true}
	case menuIdx == 2:
		return loomSlotRef{oc: oc, inv: inv, input: 2, invSlot: -1, ok: true}
	case menuIdx == 3:
		return loomSlotRef{oc: oc, inv: inv, input: -1, result: true, invSlot: -1, ok: true}
	case menuIdx >= 4 && menuIdx < 4+27:
		return loomSlotRef{oc: oc, inv: inv, input: -1, invSlot: int16(windowMainFirst + (menuIdx - 4)), ok: true}
	case menuIdx >= 4+27 && menuIdx < loomMenuSize:
		return loomSlotRef{oc: oc, inv: inv, input: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 4 - 27)), ok: true}
	}
	return loomSlotRef{input: -1}
}

func (r loomSlotRef) get() component.SlotData {
	switch {
	case r.result:
		return r.oc.loomResult
	case r.input == 0:
		return r.oc.loomBanner
	case r.input == 1:
		return r.oc.loomDye
	case r.input == 2:
		return r.oc.loomPattern
	default:
		return r.inv.get(r.invSlot)
	}
}

// set writes the slot backing. The RESULT slot is take-only (a set is ignored -- the result is owned by
// setupResultSlot, never client-set).
func (r loomSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch {
	case r.result:
		return
	case r.input == 0:
		r.oc.loomBanner = s
	case r.input == 1:
		r.oc.loomDye = s
	case r.input == 2:
		r.oc.loomPattern = s
	default:
		r.inv.set(r.invSlot, s)
	}
}

// mayPlace ports each input slot mayPlace: banner slot = BannerItem, dye slot = isDyeItem, pattern slot
// = isPatternItem; every other slot is unrestricted. An empty stack is always placeable (the removal path).
func (r loomSlotRef) mayPlace(s component.SlotData) bool {
	if stackEmpty(s) {
		return true
	}
	switch r.input {
	case 0:
		return isBannerItem(s)
	case 1:
		return isLoomDyeItem(s)
	case 2:
		return isLoomPatternItem(s)
	}
	return true
}

// clickedLoom ports AbstractContainerMenu.clicked for an open loom window: snapshot the inputs + result
// + player + cursor, run doLoomClick under a panic-recover, then -- if any INPUT changed -- re-run the
// slotsChanged reconcile (rebuild the selectable list + result), and re-send the authoritative content +
// sync the cursor.
func (t *TickLoop) clickedLoom(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	bBefore, dBefore, patBefore, resBefore := oc.loomBanner, oc.loomDye, oc.loomPattern, oc.loomResult
	selBefore, patsBefore := oc.loomSelected, oc.loomPatterns
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.loomBanner, oc.loomDye, oc.loomPattern, oc.loomResult = bBefore, dBefore, patBefore, resBefore
				oc.loomSelected, oc.loomPatterns = selBefore, patsBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doLoomClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged: if any input changed (place/move/take), re-run the reconcile. A result-take already
	// mutated banner/dye + re-ran this inside onTakeLoom, but an input-only change needs it here.
	if !slotDataEqual(bBefore, oc.loomBanner) || !slotDataEqual(dBefore, oc.loomDye) ||
		!slotDataEqual(patBefore, oc.loomPattern) {
		t.loomInputsChanged(oc)
	}

	t.sendLoomContent(p)
	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doLoomClick ports the AbstractContainerMenu.doClick branches over the loom window.
func (t *TickLoop) doLoomClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup, containerInputQuickMove:
		if inv.quickcraftStatus != 0 {
			inv.resetQuickCraft()
			return
		}
		if input == containerInputPickup {
			t.loomPickup(p, oc, inv, i, j)
		} else {
			t.loomQuickMove(p, oc, inv, i)
		}
	case containerInputSwap:
		t.menuDoSwap(p, t.loomMenuViewClick(oc, inv), i, j)
	case containerInputClone:
		t.menuDoClone(p, t.loomMenuViewClick(oc, inv), i)
	case containerInputThrow:
		t.loomThrow(p, oc, inv, i, j)
	case containerInputPickupAll:
		t.menuDoPickupAll(p, t.loomMenuViewClick(oc, inv), i, j)
	}
}

// loomPickup ports the PICKUP branch: left-click (j==0) whole, right-click (j==1) half/one. The RESULT
// slot (3) is take-only: a pickup takes the whole result onto the cursor (or merges) and fires onTakeLoom.
// Input slots gate placement via mayPlace.
func (t *TickLoop) loomPickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if j != 0 && j != 1 {
		return
	}
	if i < 0 {
		return
	}
	ref := loomResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	if ref.result {
		res := oc.loomResult
		if stackEmpty(res) {
			return
		}
		if stackEmpty(carried) {
			inv.setCarried(res)
		} else if stackSameItemSameComponents(res, carried) && int(carried.Count)+int(res.Count) <= stackMaxSize(carried) {
			c := carried
			c.Count += res.Count
			inv.setCarried(c)
		} else {
			return
		}
		t.onTakeLoom(oc)
		return
	}

	primary := j == 0
	slotItem := ref.get()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) && ref.mayPlace(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(loomSafeInsert(ref, &c, place))
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
		ref.set(loomSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if ref.mayPlace(carried) && int(carried.Count) <= stackMaxSize(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// loomSafeInsert ports Slot.safeInsert over a loomSlotRef (input/player cells; the result is never an
// insert target -- loomPickup guards it): place up to min(increment, stack.count, slotMax-existing).
func loomSafeInsert(ref loomSlotRef, stack *component.SlotData, increment int) component.SlotData {
	existing := ref.get()
	if stackEmpty(*stack) {
		return existing
	}
	add := min(increment, int(stack.Count))
	if room := stackMaxSize(*stack) - int(existing.Count); room < add {
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

// loomQuickMove ports LoomMenu.quickMoveStack (verified ranges): a RESULT shift moves the whole result
// into the player inventory + fires onTakeLoom; a shift FROM an input moves it into the player inventory;
// a shift from a player cell tries banner->0, dye->1, pattern->2 (per mayPlace), else main<->hotbar. The
// menu ranges map to player WINDOW slots (menu 4..40 == window 9..45). CITE LoomMenu.quickMoveStack.
func (t *TickLoop) loomQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := loomResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.result {
		res := oc.loomResult
		if stackEmpty(res) {
			return
		}
		work := res
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			return // no room: nothing crafted
		}
		t.onTakeLoom(oc)
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src
	if ref.input == 0 || ref.input == 1 || ref.input == 2 {
		// input -> player inventory.
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, false) {
			return
		}
		ref.set(work)
		return
	}

	// player cell -> the matching input slot (banner/dye/pattern), gated by mayPlace + emptiness.
	if !t.loomMoveIntoInputs(oc, &work) {
		return
	}
	ref.set(work)
}

// loomMoveIntoInputs moves *stack into the first matching empty input slot: a banner into slot 0, a dye
// into slot 1, a pattern into slot 2 (each mayPlace-gated + only into an empty cell -- a single-item
// slot). Returns true if moved. Mirrors grindstoneMoveIntoInputs over the typed loom slots.
func (t *TickLoop) loomMoveIntoInputs(oc *openContainer, stack *component.SlotData) bool {
	if stackEmpty(*stack) {
		return false
	}
	moved := false
	tryPlace := func(cur *component.SlotData, mayPlace bool) {
		if moved || !mayPlace || !stackEmpty(*cur) {
			return
		}
		*cur = stackCopyWithCount(*stack, 1) // single-item input
		stack.Count = toVar(int(stack.Count) - 1)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		moved = true
	}
	tryPlace(&oc.loomBanner, isBannerItem(*stack))
	tryPlace(&oc.loomDye, isLoomDyeItem(*stack))
	tryPlace(&oc.loomPattern, isLoomPatternItem(*stack))
	return moved
}

// loomThrow ports the THROW branch: with an empty cursor, Q drops 1 (j==0) or the whole stack (j==1). The
// result slot drops the whole result + fires the take.
func (t *TickLoop) loomThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	ref := loomResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.result {
		t.playerDrop(p, cur, true)
		t.onTakeLoom(oc)
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

// onTakeLoom ports LoomMenu6.onTake: consume 1 from the banner slot + 1 from the dye slot; if either
// slot is now empty, reset the selection (selectedBannerPatternIndex.set(-1)). Then re-run slotsChanged
// (rebuild the selectable list + result from the shrunk inputs). The take-sound is a cited no-op (no
// levelEvent wire).
//
// 1:1 net.minecraft.world.inventory.LoomMenu LoomMenu6.onTake
func (t *TickLoop) onTakeLoom(oc *openContainer) {
	// bannerSlot.remove(1); dyeSlot.remove(1).
	if !stackEmpty(oc.loomBanner) {
		_ = stackSplit(&oc.loomBanner, 1)
		if stackEmpty(oc.loomBanner) {
			oc.loomBanner = component.SlotData{Count: 0}
		}
	}
	if !stackEmpty(oc.loomDye) {
		_ = stackSplit(&oc.loomDye, 1)
		if stackEmpty(oc.loomDye) {
			oc.loomDye = component.SlotData{Count: 0}
		}
	}
	// if (!bannerSlot.hasItem() || !dyeSlot.hasItem()) selectedBannerPatternIndex.set(-1).
	if stackEmpty(oc.loomBanner) || stackEmpty(oc.loomDye) {
		oc.loomSelected = -1
	}
	// slotsChanged fires after the take (the container change) -> rebuild the list + result.
	t.loomInputsChanged(oc)
}

// loomMenuViewClick adapts the OPEN LOOM window (banner 0 / dye 1 / pattern 2 / result 3 / player 4..39)
// to the generic menuView. The RESULT slot (3) is take-only (mayPlace false) and fires onTakeLoom on take
// (consume banner+dye + reset selection); input slots gate mayPlace by type; player cells are plain. The
// clickedLoom wrapper re-runs slotsChanged after the click when an input changed.
func (t *TickLoop) loomMenuViewClick(oc *openContainer, inv *Inventory) menuView {
	return menuView{
		size: loomMenuSize,
		inv:  inv,
		slotAt: func(idx int) menuSlotView {
			ref := loomResolveSlot(oc, inv, idx)
			if !ref.ok {
				return menuSlotView{}
			}
			isResult := ref.result
			return menuSlotView{
				ok:            true,
				getItem:       func() component.SlotData { return ref.get() },
				setByPlayer:   func(s component.SlotData) { ref.set(s) },
				mayPickupFn:   func(*tickPlayer) bool { return true },
				mayPlaceFn:    func(s component.SlotData) bool { return ref.mayPlace(s) },
				maxStackFn:    func(s component.SlotData) int { return chestSlotMax(s) },
				onSwapCraftFn: func(int) {},
				onTakeFn: func(_ *tickPlayer, _ component.SlotData) {
					if isResult {
						t.onTakeLoom(oc)
					}
				},
			}
		},
		canTakeItemForPickAll: func(component.SlotData, int) bool { return true },
	}
}
