package server

// grindstone_click.go — the ContainerClick engine for an OPEN grindstone window, plus the RESULT-take
// chain (ResultSlot.onTake -> award disenchant XP orbs + clear the two inputs). A sibling of
// merchant_click.go over the GrindstoneMenu slot layout (input 0/1, result 2, player main 3..29, hotbar
// 30..38). The result slot (2) is take-only (mayPlace==false); the two input slots gate placement via
// mayPlace (isDamageableItem || hasAnyEnchantments). Supported ContainerInputs: PICKUP, QUICK_MOVE, THROW.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, CFR this session):
//   onTakeGrindstone : net.minecraft.world.inventory.GrindstoneMenu ResultSlot.onTake
//   quickMove        : net.minecraft.world.inventory.GrindstoneMenu.quickMoveStack
//
// Tick-owned (TICK-05): every method runs on the tick goroutine, so the input/result mutation is race-clean.

import (
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// grindstoneSlotRef resolves a grindstone-WINDOW slot index (0..38) to its backing + local index:
//
//	0      -> input slot 0 (oc.grind0)
//	1      -> input slot 1 (oc.grind1)
//	2      -> the result slot (oc.grindResult, take-only)
//	3..38  -> the player inventory window (main 9..35, hotbar 36..44)
type grindstoneSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	input   int   // 0 or 1 for an input slot, -1 otherwise
	result  bool  // the result slot (2)
	invSlot int16 // player inventory window slot, -1 otherwise
	ok      bool
}

func grindstoneResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) grindstoneSlotRef {
	switch {
	case menuIdx == 0:
		return grindstoneSlotRef{oc: oc, inv: inv, input: 0, invSlot: -1, ok: true}
	case menuIdx == 1:
		return grindstoneSlotRef{oc: oc, inv: inv, input: 1, invSlot: -1, ok: true}
	case menuIdx == 2:
		return grindstoneSlotRef{oc: oc, inv: inv, input: -1, result: true, invSlot: -1, ok: true}
	case menuIdx >= 3 && menuIdx < 3+27:
		return grindstoneSlotRef{oc: oc, inv: inv, input: -1, invSlot: int16(windowMainFirst + (menuIdx - 3)), ok: true}
	case menuIdx >= 3+27 && menuIdx < grindstoneMenuSize:
		return grindstoneSlotRef{oc: oc, inv: inv, input: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 3 - 27)), ok: true}
	}
	return grindstoneSlotRef{input: -1}
}

func (r grindstoneSlotRef) get() component.SlotData {
	switch {
	case r.result:
		return r.oc.grindResult
	case r.input == 0:
		return r.oc.grind0
	case r.input == 1:
		return r.oc.grind1
	default:
		return r.inv.get(r.invSlot)
	}
}

// set writes the slot backing. The RESULT slot is take-only (a set is ignored — the result is owned by
// createResult, never client-set).
func (r grindstoneSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch {
	case r.result:
		return
	case r.input == 0:
		r.oc.grind0 = s
	case r.input == 1:
		r.oc.grind1 = s
	default:
		r.inv.set(r.invSlot, s)
	}
}

// mayPlace reports whether stack may be placed into this slot: an input slot gates on
// grindstoneMayPlace (isDamageableItem || hasAnyEnchantments); every other slot is unrestricted.
func (r grindstoneSlotRef) mayPlace(s component.SlotData) bool {
	if r.input == 0 || r.input == 1 {
		return grindstoneMayPlace(s)
	}
	return true
}

// clickedGrindstone ports AbstractContainerMenu.clicked for an open grindstone window: snapshot the
// inputs + result + player + cursor, run the doClick under a panic-recover, recompute the result via
// createResult, then re-send the authoritative content + sync the cursor.
func (t *TickLoop) clickedGrindstone(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	g0Before, g1Before, resBefore := oc.grind0, oc.grind1, oc.grindResult
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.grind0, oc.grind1, oc.grindResult = g0Before, g1Before, resBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doGrindstoneClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged -> createResult: recompute the result from the (post-click) inputs. A take already
	// re-ran this inside onTakeGrindstone, but an input-only change (place/move) needs it here.
	t.grindstoneCreateResult(oc)

	t.sendGrindstoneContent(p)

	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doGrindstoneClick ports the AbstractContainerMenu.doClick branches over the grindstone window.
func (t *TickLoop) doGrindstoneClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
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
			t.grindstonePickup(p, oc, inv, i, j)
		} else {
			t.grindstoneQuickMove(p, oc, inv, i)
		}
	case containerInputThrow:
		t.grindstoneThrow(p, oc, inv, i, j)
	}
}

// grindstonePickup ports the PICKUP branch: left-click (j==0) whole, right-click (j==1) half/one. The
// RESULT slot (2) is take-only: a pickup takes the whole result onto the cursor (or merges) and fires
// onTakeGrindstone. Input slots gate placement via mayPlace.
func (t *TickLoop) grindstonePickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if j != 0 && j != 1 {
		return
	}
	if i < 0 {
		return
	}
	ref := grindstoneResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	if ref.result {
		res := oc.grindResult
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
		t.onTakeGrindstone(p, oc, inv)
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
			ref.set(grindstoneSafeInsert(ref, &c, place))
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
		ref.set(grindstoneSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if ref.mayPlace(carried) && int(carried.Count) <= stackMaxSize(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// grindstoneSafeInsert ports Slot.safeInsert over a grindstoneSlotRef (input/player cells; the result is
// never an insert target): place up to min(increment, stack.count, slotMax-existing) of *stack.
func grindstoneSafeInsert(ref grindstoneSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// grindstoneQuickMove ports GrindstoneMenu.quickMoveStack. Ranges (jar, verified):
//
//	slotIndex == 2 (result): moveItemStackTo(item, 3, 39, true); onQuickCraft; slot.onTake  (the disenchant/repair XP take)
//	slotIndex == 0|1 (input): moveItemStackTo(item, 3, 39, false)
//	else (both inputs empty): moveItemStackTo(item, 0, 2, false)         (player -> an input slot)
//	else slotIndex 3..29 (main):  moveItemStackTo(item, 30, 39, false)   (main -> hotbar)
//	else slotIndex 30..38 (hotbar): moveItemStackTo(item, 3, 30, false)  (hotbar -> main)
//
// The menu ranges map to player inv WINDOW slots: menu [3,39) == window [9,45); [30,39)==window[36,45);
// [3,30)==window[9,36). For the RESULT shift, the whole result is deposited into the inventory, THEN the
// take fires. The player->input branch runs ONLY when both inputs are empty (the jar's input.isEmpty() ||
// additional.isEmpty() guard). CITE GrindstoneMenu.quickMoveStack.
func (t *TickLoop) grindstoneQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := grindstoneResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.result {
		res := oc.grindResult
		if stackEmpty(res) {
			return
		}
		work := res
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			return // no room: nothing taken
		}
		t.onTakeGrindstone(p, oc, inv)
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src
	switch {
	case ref.input == 0 || ref.input == 1:
		// input -> player inventory: moveItemStackTo(item, 3, 39, false) == window [9,45).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, false) {
			return
		}
	case stackEmpty(oc.grind0) || stackEmpty(oc.grind1):
		// player cell, an input slot free (the jar's input.isEmpty()||additional.isEmpty() branch):
		// try to move into the two input slots (menu 0..2, exclusive of result). mayPlace gates each.
		if !t.grindstoneMoveIntoInputs(oc, &work) {
			return
		}
	case ref.invSlot >= int16(windowMainFirst) && ref.invSlot < int16(windowMainFirst+27):
		// main -> hotbar: moveItemStackTo(item, 30, 39, false) == window [36,45).
		if !t.moveItemStackTo(inv, &work, windowHotbarFirst, offhandWindowSlot, false) {
			return
		}
	default:
		// hotbar -> main: moveItemStackTo(item, 3, 30, false) == window [9,36).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, windowHotbarFirst, false) {
			return
		}
	}
	ref.set(work)
}

// grindstoneMoveIntoInputs ports moveItemStackTo(item, 0, 2, false) over the two input slots: fill the
// first empty input slot the item may be placed into (mayPlace gate). The grindstone inputs are not
// stackable targets (single-item slots), so this only fills an empty slot. Returns true if moved.
func (t *TickLoop) grindstoneMoveIntoInputs(oc *openContainer, stack *component.SlotData) bool {
	if !grindstoneMayPlace(*stack) {
		return false
	}
	moved := false
	for slot := 0; slot < 2 && !stackEmpty(*stack); slot++ {
		var cur component.SlotData
		if slot == 0 {
			cur = oc.grind0
		} else {
			cur = oc.grind1
		}
		if !stackEmpty(cur) {
			continue
		}
		// Place ONE item (the grindstone input holds a single item; count>1 yields no result anyway).
		place := stackCopyWithCount(*stack, 1)
		if slot == 0 {
			oc.grind0 = place
		} else {
			oc.grind1 = place
		}
		stack.Count = toVar(int(stack.Count) - 1)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		moved = true
	}
	return moved
}

// grindstoneThrow ports the THROW branch: with an empty cursor, Q drops 1 (j==0) or the whole stack
// (j==1). The result slot drops the whole result + fires the take.
func (t *TickLoop) grindstoneThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	ref := grindstoneResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.result {
		t.playerDrop(p, cur, true)
		t.onTakeGrindstone(p, oc, inv)
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

// onTakeGrindstone ports the GrindstoneMenu ResultSlot.onTake: award the disenchant XP as orbs at the
// grindstone position (getExperienceAmount) + the levelEvent(1042) sound, then clear the two input slots.
// Then recompute the (now empty-input) result.
//
//	access.execute((level, pos) -> { if serverLevel: ExperienceOrb.award(level, atCenterOf(pos), getExperienceAmount(level));
//	                                 level.levelEvent(1042, pos, 0); });
//	repairSlots.setItem(0, EMPTY); repairSlots.setItem(1, EMPTY);
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu (ResultSlot.onTake)
func (t *TickLoop) onTakeGrindstone(p *tickPlayer, oc *openContainer, inv *Inventory) {
	_ = inv
	// getExperienceAmount reads the CURRENT inputs (before they are cleared) — compute FIRST.
	xp := t.grindstoneExperienceAmount(oc)
	if xp > 0 {
		// ExperienceOrb.award(level, Vec3.atCenterOf(pos), xp): spawn the disenchant XP at the block center.
		t.awardExperienceOrbsAt(float64(oc.grindPos.X)+0.5, float64(oc.grindPos.Y)+0.5, float64(oc.grindPos.Z)+0.5, xp)
	}
	// level.levelEvent(1042, pos, 0): the grindstone "use" sound — a faithful no-op seam (no levelEvent wire yet).
	t.grindstoneLevelEvent(oc.grindPos, 1042)

	oc.grind0 = component.SlotData{Count: 0}
	oc.grind1 = component.SlotData{Count: 0}

	// slotsChanged -> createResult: recompute the result from the now-empty inputs (-> EMPTY).
	t.grindstoneCreateResult(oc)
}

// grindstoneLevelEvent is the cited faithful no-op seam for GrindstoneMenu's onTake level.levelEvent(1042)
// (the grindstone-use sound), mirroring dispenserLevelEvent/brewLevelEvent — no ClientboundLevelEvent wire
// is emitted yet, so the sound is a documented no-op the take still fires.
func (t *TickLoop) grindstoneLevelEvent(_ pk.Position, _ int) {}
