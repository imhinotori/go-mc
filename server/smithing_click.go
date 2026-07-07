package server

// smithing_click.go — the ContainerClick engine for an OPEN smithing window, plus the RESULT-take chain
// (SmithingMenu.onTake -> shrink each of the 3 inputs by 1 + levelEvent(1044)). A sibling of
// grindstone_click.go over the SmithingMenu slot layout (template 0, base 1, addition 2, result 3,
// player main 4..30, hotbar 31..39). The result slot (3) is take-only (mayPlace==false); the 3 input
// slots gate placement via their RecipePropertySet.test. Supported ContainerInputs: PICKUP, QUICK_MOVE,
// THROW.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, CFR this session):
//   onTakeSmithing : net.minecraft.world.inventory.SmithingMenu.onTake
//   quickMove      : net.minecraft.world.inventory.ItemCombinerMenu.quickMoveStack
//   mayPlace       : SmithingMenu.createInputSlotDefinitions (RecipePropertySet.test per slot)

import (
	"sync"

	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/recipe"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// smithingSlotRef resolves a smithing-WINDOW slot index (0..39) to its backing + local index:
//
//	0      -> template (oc.smithTemplate)
//	1      -> base     (oc.smithBase)
//	2      -> addition (oc.smithAddition)
//	3      -> the result slot (oc.smithResult, take-only)
//	4..39  -> the player inventory window (main 9..35, hotbar 36..44)
type smithingSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	input   int   // 0/1/2 for an input slot, -1 otherwise
	result  bool  // the result slot (3)
	invSlot int16 // player inventory window slot, -1 otherwise
	ok      bool
}

func smithingResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) smithingSlotRef {
	switch {
	case menuIdx == 0:
		return smithingSlotRef{oc: oc, inv: inv, input: 0, invSlot: -1, ok: true}
	case menuIdx == 1:
		return smithingSlotRef{oc: oc, inv: inv, input: 1, invSlot: -1, ok: true}
	case menuIdx == 2:
		return smithingSlotRef{oc: oc, inv: inv, input: 2, invSlot: -1, ok: true}
	case menuIdx == 3:
		return smithingSlotRef{oc: oc, inv: inv, input: -1, result: true, invSlot: -1, ok: true}
	case menuIdx >= 4 && menuIdx < 4+27:
		return smithingSlotRef{oc: oc, inv: inv, input: -1, invSlot: int16(windowMainFirst + (menuIdx - 4)), ok: true}
	case menuIdx >= 4+27 && menuIdx < smithingMenuSize:
		return smithingSlotRef{oc: oc, inv: inv, input: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 4 - 27)), ok: true}
	}
	return smithingSlotRef{input: -1}
}

func (r smithingSlotRef) get() component.SlotData {
	switch {
	case r.result:
		return r.oc.smithResult
	case r.input == 0:
		return r.oc.smithTemplate
	case r.input == 1:
		return r.oc.smithBase
	case r.input == 2:
		return r.oc.smithAddition
	default:
		return r.inv.get(r.invSlot)
	}
}

func (r smithingSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch {
	case r.result:
		return // result is take-only (mayPlace == false)
	case r.input == 0:
		r.oc.smithTemplate = s
	case r.input == 1:
		r.oc.smithBase = s
	case r.input == 2:
		r.oc.smithAddition = s
	default:
		r.inv.set(r.invSlot, s)
	}
}

// mayPlace gates the 3 input slots via their RecipePropertySet.test (SMITHING_TEMPLATE / SMITHING_BASE /
// SMITHING_ADDITION — the union of every smithing recipe's ingredient at that slot). Every other slot is
// unrestricted.
func (r smithingSlotRef) mayPlace(s component.SlotData) bool {
	if r.input < 0 {
		return true
	}
	return smithingSlotTest(r.input, s)
}

// smithingPropertyOnce lazily builds the 3 per-slot RecipePropertySet membership sets (template/base/
// addition) as the UNION of every parsed smithing_transform recipe's ingredient at that slot — the
// analogue of RecipeAccess.propertySet(SMITHING_*). An absent optional (empty Ingredient) contributes
// nothing (a slot whose recipes never require it accepts nothing, matching the empty property set).
var (
	smithingPropertyOnce sync.Once
	smithingTemplateSet  map[int]bool
	smithingBaseSet      map[int]bool
	smithingAdditionSet  map[int]bool
)

func smithingProperties() (tmpl, base, add map[int]bool) {
	smithingPropertyOnce.Do(func() {
		smithingTemplateSet = map[int]bool{}
		smithingBaseSet = map[int]bool{}
		smithingAdditionSet = map[int]bool{}
		for _, r := range smithingTransformRecipes() {
			addIngredientIDs(smithingTemplateSet, r.Template)
			addIngredientIDs(smithingBaseSet, r.Base)
			addIngredientIDs(smithingAdditionSet, r.Addition)
		}
	})
	return smithingTemplateSet, smithingBaseSet, smithingAdditionSet
}

// addIngredientIDs unions an ingredient's member ids into set (a no-op for an empty ingredient).
func addIngredientIDs(set map[int]bool, in recipe.Ingredient) {
	for _, id := range in.IDs() {
		set[id] = true
	}
}

// smithingSlotTest reports whether stack passes the RecipePropertySet.test for input slot (0 template /
// 1 base / 2 addition): membership in that slot's union set.
func smithingSlotTest(slot int, s component.SlotData) bool {
	if stackEmpty(s) {
		return true // an empty stack is placeable (removal path)
	}
	tmpl, base, add := smithingProperties()
	switch slot {
	case 0:
		return tmpl[int(s.ItemID)]
	case 1:
		return base[int(s.ItemID)]
	case 2:
		return add[int(s.ItemID)]
	}
	return false
}

// clickedSmithing ports AbstractContainerMenu.clicked for an open smithing window: snapshot the inputs +
// result + player + cursor, run the doClick under a panic-recover, recompute the result via createResult,
// then re-send the authoritative content + sync the cursor.
func (t *TickLoop) clickedSmithing(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	tBefore, bBefore, aBefore, rBefore := oc.smithTemplate, oc.smithBase, oc.smithAddition, oc.smithResult
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.smithTemplate, oc.smithBase, oc.smithAddition, oc.smithResult = tBefore, bBefore, aBefore, rBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doSmithingClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged -> createResult: recompute from the (post-click) inputs. A take already re-ran this
	// inside onTakeSmithing, but an input-only change (place/move) needs it here.
	t.smithingCreateResult(oc)

	t.sendSmithingContent(p)

	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doSmithingClick ports the AbstractContainerMenu.doClick branches over the smithing window.
func (t *TickLoop) doSmithingClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
	switch input {
	case containerInputPickup, containerInputQuickMove:
		if inv.quickcraftStatus != 0 {
			inv.resetQuickCraft()
			return
		}
		if input == containerInputPickup {
			t.smithingPickup(p, oc, inv, i, j)
		} else {
			t.smithingQuickMove(p, oc, inv, i)
		}
	case containerInputThrow:
		t.smithingThrow(p, oc, inv, i, j)
	}
}

// smithingPickup ports the PICKUP branch: left-click (j==0) whole, right-click (j==1) half/one. The
// RESULT slot (3) is take-only: a pickup takes the whole result onto the cursor (or merges) and fires
// onTakeSmithing. Input slots gate placement via mayPlace.
func (t *TickLoop) smithingPickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if j != 0 && j != 1 {
		return
	}
	if i < 0 {
		return
	}
	ref := smithingResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	if ref.result {
		res := oc.smithResult
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
		t.onTakeSmithing(p, oc, inv)
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
			ref.set(smithingSafeInsert(ref, &c, place))
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
		ref.set(smithingSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if ref.mayPlace(carried) && int(carried.Count) <= stackMaxSize(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// smithingSafeInsert ports Slot.safeInsert over a smithingSlotRef (input/player cells; the result is
// never an insert target): place up to min(increment, stack.count, slotMax-existing) of *stack.
func smithingSafeInsert(ref smithingSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// smithingQuickMove ports ItemCombinerMenu.quickMoveStack. Ranges (jar, verified; inventorySlotStart=4,
// useRowEnd=40, resultSlot=3):
//
//	slotIndex == 3 (result): moveItemStackTo(item, 4, 40, true); onQuickCraft; slot.onTake (the transform take)
//	slotIndex 0..2 (input):  moveItemStackTo(item, 4, 40, false)
//	else canMoveIntoInputSlots && slotIndex 4..39: moveItemStackTo(item, 0, 3, false)  (player -> an input slot)
//	else slotIndex 4..30 (main):  moveItemStackTo(item, 31, 40, false)  (main -> hotbar)
//	else slotIndex 31..39 (hotbar): moveItemStackTo(item, 4, 31, false) (hotbar -> main)
//
// The menu ranges map to player inv WINDOW slots: menu [4,40) == window [9,45); [31,40)==window[36,45);
// [4,31)==window[9,36). CITE ItemCombinerMenu.quickMoveStack + SmithingMenu.canMoveIntoInputSlots.
func (t *TickLoop) smithingQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := smithingResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.result {
		res := oc.smithResult
		if stackEmpty(res) {
			return
		}
		work := res
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, true) {
			return // no room: nothing taken
		}
		t.onTakeSmithing(p, oc, inv)
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src
	switch {
	case ref.input >= 0:
		// input -> player inventory: moveItemStackTo(item, 4, 40, false) == window [9,45).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, offhandWindowSlot, false) {
			return
		}
	case t.smithingCanMoveIntoInputs(oc, work):
		// player cell, an input slot free (SmithingMenu.canMoveIntoInputSlots): move into the first empty
		// input slot the item tests into (menu 0..2). moveItemStackTo(item, 0, 3, false) analogue.
		if !t.smithingMoveIntoInputs(oc, &work) {
			return
		}
	case ref.invSlot >= int16(windowMainFirst) && ref.invSlot < int16(windowMainFirst+27):
		// main -> hotbar: moveItemStackTo(item, 31, 40, false) == window [36,45).
		if !t.moveItemStackTo(inv, &work, windowHotbarFirst, offhandWindowSlot, false) {
			return
		}
	default:
		// hotbar -> main: moveItemStackTo(item, 4, 31, false) == window [9,36).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, windowHotbarFirst, false) {
			return
		}
	}
	ref.set(work)
}

// smithingCanMoveIntoInputs ports SmithingMenu.canMoveIntoInputSlots(stack): true if the stack tests
// into an EMPTY template/base/addition slot.
//
// 1:1 net.minecraft.world.inventory.SmithingMenu.canMoveIntoInputSlots
func (t *TickLoop) smithingCanMoveIntoInputs(oc *openContainer, s component.SlotData) bool {
	if smithingSlotTest(0, s) && stackEmpty(oc.smithTemplate) {
		return true
	}
	if smithingSlotTest(1, s) && stackEmpty(oc.smithBase) {
		return true
	}
	return smithingSlotTest(2, s) && stackEmpty(oc.smithAddition)
}

// smithingMoveIntoInputs moves the stack into the first empty input slot it tests into (template, then
// base, then addition — the moveItemStackTo(0,3) ascending order). One item is placed per empty slot;
// the smithing inputs are effectively single-item targets for the transform (count>1 stays as the input
// count — the recipe base ingredient tests item id, and shrinkStackInSlot removes 1 on take). Returns
// true if any move happened.
func (t *TickLoop) smithingMoveIntoInputs(oc *openContainer, stack *component.SlotData) bool {
	moved := false
	// template (0)
	if !stackEmpty(*stack) && stackEmpty(oc.smithTemplate) && smithingSlotTest(0, *stack) {
		oc.smithTemplate = mergeIntoSmithingSlot(oc.smithTemplate, stack)
		moved = true
	}
	if !stackEmpty(*stack) && stackEmpty(oc.smithBase) && smithingSlotTest(1, *stack) {
		oc.smithBase = mergeIntoSmithingSlot(oc.smithBase, stack)
		moved = true
	}
	if !stackEmpty(*stack) && stackEmpty(oc.smithAddition) && smithingSlotTest(2, *stack) {
		oc.smithAddition = mergeIntoSmithingSlot(oc.smithAddition, stack)
		moved = true
	}
	return moved
}

// mergeIntoSmithingSlot moves the whole *stack into an EMPTY smithing input slot (the moveItemStackTo
// fill-empty step over a 1-capacity input container: place up to the slot's max, which for a smithing
// input is the item's max stack size). Returns the slot's new contents; *stack is shrunk.
func mergeIntoSmithingSlot(existing component.SlotData, stack *component.SlotData) component.SlotData {
	place := int(stack.Count)
	if maxS := stackMaxSize(*stack); place > maxS {
		place = maxS
	}
	out := stackCopyWithCount(*stack, place)
	stack.Count = toVar(int(stack.Count) - place)
	if stack.Count <= 0 {
		stack.Count = 0
	}
	_ = existing
	return out
}

// smithingThrow ports the THROW branch: with an empty cursor, Q drops 1 (j==0) or the whole stack
// (j==1). The result slot drops the whole result + fires the take.
func (t *TickLoop) smithingThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	ref := smithingResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.result {
		t.playerDrop(p, cur, true)
		t.onTakeSmithing(p, oc, inv)
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

// onTakeSmithing ports SmithingMenu.onTake: shrink each of the 3 input slots by 1 (shrinkStackInSlot 0/1/2)
// + levelEvent(1044). awardUsedRecipes + onCraftedBy are faithful no-ops (no recipe-book / crafted-by stat
// subsystem). Then recompute the (now-shrunk-input) result.
//
//	carried.onCraftedBy(player, count);                 // v1 no-op
//	resultSlots.awardUsedRecipes(player, relevantItems);// v1 no-op (no recipe book)
//	shrinkStackInSlot(0); shrinkStackInSlot(1); shrinkStackInSlot(2);
//	access.execute((level, pos) -> level.levelEvent(1044, pos, 0));
//
// 1:1 net.minecraft.world.inventory.SmithingMenu.onTake
func (t *TickLoop) onTakeSmithing(p *tickPlayer, oc *openContainer, inv *Inventory) {
	_ = p
	_ = inv
	oc.smithTemplate = smithingShrinkOne(oc.smithTemplate)
	oc.smithBase = smithingShrinkOne(oc.smithBase)
	oc.smithAddition = smithingShrinkOne(oc.smithAddition)

	// level.levelEvent(1044, pos, 0): the smithing-table "use" sound — a faithful no-op seam.
	t.smithingLevelEvent(oc.smithPos, 1044)

	// slotsChanged -> createResult: recompute from the shrunk inputs.
	t.smithingCreateResult(oc)
}

// smithingShrinkOne ports SmithingMenu.shrinkStackInSlot(slot): if !empty, stack.shrink(1); setItem.
func smithingShrinkOne(s component.SlotData) component.SlotData {
	if stackEmpty(s) {
		return s
	}
	s.Count = toVar(int(s.Count) - 1)
	if s.Count <= 0 {
		return component.SlotData{Count: 0}
	}
	return s
}

// smithingLevelEvent is the cited faithful no-op seam for SmithingMenu.onTake's level.levelEvent(1044)
// (the smithing-table use sound), mirroring grindstoneLevelEvent — no ClientboundLevelEvent wire yet.
func (t *TickLoop) smithingLevelEvent(_ pk.Position, _ int) {}
