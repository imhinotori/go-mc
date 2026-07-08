package server

// cooking_block.go — the STONECUTTER click engine + recipe-button selection (PLUGIN-05, Plan 25-03), and
// the build-or-defer OUTCOME for the COOKING blocks.
//
// BUILD vs DEFER (deferred-blocks.md, the evidence-based audit):
//   - STONECUTTING is BUILT: this file holds clickedStonecutter (the single-input picker click engine, a
//     clone of crafting_click.go over the StonecutterMenu slot layout: input 0, result 1, player 2..37),
//     handleContainerButtonClick + clickStonecutterButton (the recipe-PICK), setupStonecutterResult (the
//     selected-recipe assemble), and onTakeStonecut (the 1:1 input consume). No fuel, no tick, no
//     block-entity.
//   - SMELTING / BLASTING / SMOKING / CAMPFIRE_COOKING are DEFERRED with a cited reason: a furnace/campfire
//     BLOCK-ENTITY with a per-tick fuel+progress COOKING drive (AbstractFurnaceBlockEntity.serverTick) +
//     a FuelValues table — a large unbuilt subsystem (no per-tick block-entity drive seam exists; no fuel
//     table exists; the BE types are empty struct markers). The recipe MATCHER for every deferred type
//     SHIPPED + is tested in Plan 01 (the deferral is the BLOCK UI only). See deferred-blocks.md +
//     TestSmeltingMatcherShips (the matcher still resolves iron_ore -> iron_ingot headless).
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, javap'd this session, cited per function):
//   clickStonecutterButton  : net.minecraft.world.inventory.StonecutterMenu.clickMenuButton
//   setupStonecutterResult  : net.minecraft.world.inventory.StonecutterMenu.setupResultSlot
//   onTakeStonecut          : net.minecraft.world.inventory.ResultSlot.onTake (single-input: removeItem(input,1))
//   clickedStonecutter      : net.minecraft.world.inventory.AbstractContainerMenu.clicked (PICKUP/QUICK_MOVE/THROW)

import (
	"bytes"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// stonecutterMatchPayload builds the host matcher payload for a single-input (1x1) stonecutting/cooking
// check: the frozen {"w":1,"h":1,"cells":[(id,1)]} dict the plugin matcher reads (the SAME shape
// craftGridPayload builds for the grid). Used to GATE the stonecutter's result list through the plugin
// Match seam (the dogfood: a stonecuttable input must resolve through the plugin, not a private Go table)
// and to prove the deferred cooking matchers still resolve headless (TestSmeltingMatcherShips). The
// wantID param is unused by the payload (Match returns the FIRST 1x1 match) — it documents the caller's
// expectation; the specific stonecutter pick is the server-side selectByInput list.
func stonecutterMatchPayload(inputID int) starlark.Value {
	cell := starlark.Tuple{starlark.MakeInt(inputID), starlark.MakeInt(1)}
	d := starlark.NewDict(3)
	_ = d.SetKey(starlark.String("w"), starlark.MakeInt(1))
	_ = d.SetKey(starlark.String("h"), starlark.MakeInt(1))
	_ = d.SetKey(starlark.String("cells"), starlark.NewList([]starlark.Value{cell}))
	return d
}

// handleContainerButtonClick decodes ServerboundContainerButtonClick (containerId, buttonId — both
// VarInt) and routes a stonecutter recipe-pick. A forged/stale window id (not the player's open
// stonecutter) or a malformed payload is a silent no-op (T-25-10) — the authoritative content is the
// server's. CITE ServerGamePacketListenerImpl.handleContainerButtonClick → menu.clickMenuButton.
func (t *TickLoop) handleContainerButtonClick(p *tickPlayer, pkt pk.Packet) {
	if p == nil {
		return
	}
	r := bytes.NewReader(pkt.Data)
	var containerID, buttonID pk.VarInt
	if _, err := (pk.Tuple{&containerID, &buttonID}).ReadFrom(r); err != nil {
		return // malformed: no-op
	}
	oc := p.openContainer
	if oc == nil || int32(containerID) != int32(oc.windowID) {
		return // not the player's open window: no-op (a forged/stale id is ignored)
	}
	switch oc.kind {
	case containerKindStonecutter:
		t.clickStonecutterButton(p, oc, int(buttonID))
	case containerKindEnchant:
		// EnchantmentMenu.clickMenuButton: apply the chosen offer (button 0/1/2). CITE
		// ServerGamePacketListenerImpl.handleContainerButtonClick -> EnchantmentMenu.clickMenuButton.
		t.enchantClickButton(p, oc, int(buttonID))
	case containerKindLoom:
		// LoomMenu.clickMenuButton: PICK a selectable banner pattern (button = the pattern index). CITE
		// ServerGamePacketListenerImpl.handleContainerButtonClick -> LoomMenu.clickMenuButton.
		t.loomClickButton(p, oc, int(buttonID))
	}
}

// clickStonecutterButton ports StonecutterMenu.clickMenuButton: if buttonId is a valid index into the
// current recipe list, set the selectedRecipeIndex DataSlot and rebuild the result slot from the chosen
// recipe; then re-send the authoritative content + the DataSlot. An out-of-range index is ignored (the
// vanilla isValidRecipeIndex guard) — clickMenuButton always returns true (the click is consumed).
//
// 1:1 net.minecraft.world.inventory.StonecutterMenu.clickMenuButton
func (t *TickLoop) clickStonecutterButton(p *tickPlayer, oc *openContainer, buttonID int) {
	if oc.cutSelected == buttonID {
		return // already selected (clickMenuButton fast-returns when the index is unchanged)
	}
	if buttonID < 0 || buttonID >= len(oc.cutResults) {
		return // isValidRecipeIndex(buttonId) == false: ignore (selection unchanged)
	}
	oc.cutSelected = buttonID
	t.setupStonecutterResult(oc, buttonID)

	t.sendStonecutterContent(p)
	if p.client != nil {
		p.client.Send(containerSetData(int32(oc.windowID), stonecutterDataSelectedIndex, int16(buttonID)))
	}
}

// setupStonecutterResult ports StonecutterMenu.setupResultSlot: if the recipe list is non-empty and index
// is valid, set the result slot to the chosen recipe's assembled result; else clear it.
//
// 1:1 net.minecraft.world.inventory.StonecutterMenu.setupResultSlot
func (t *TickLoop) setupStonecutterResult(oc *openContainer, index int) {
	if index < 0 || index >= len(oc.cutResults) {
		oc.cutResult = component.SlotData{Count: 0} // resultSlot.set(EMPTY)
		return
	}
	r := oc.cutResults[index]
	cnt := r.Count
	if cnt <= 0 {
		cnt = 1 // ItemStackTemplate default
	}
	oc.cutResult = component.SlotData{ItemID: toItemID(r.ID), Count: toVar(cnt)}
}

// onTakeStonecut ports ResultSlot.onTake for the stonecutter (single input): consume exactly 1 from the
// input slot, then rebuild the recipe list from the (shrunk) input — a chained cut keeps the same
// selected index if the new input still offers it (vanilla re-runs setupRecipeList via slotsChanged after
// a take, which resets the selection; v1 mirrors that reset). GO applies the consume over the tick-owned
// input (TICK-05). The stonecutter consumes 1 input per result (StonecutterMenu's single-input transform
// — no per-cell footprint, no getRemainingItems bucket-back: stonecutting has no remainders).
//
// 1:1 net.minecraft.world.inventory.ResultSlot.onTake (single-input case)
func (t *TickLoop) onTakeStonecut(oc *openContainer) {
	if stackEmpty(oc.cutInput) {
		return
	}
	_ = stackSplit(&oc.cutInput, 1) // removeItem(INPUT_SLOT, 1)
	if stackEmpty(oc.cutInput) {
		oc.cutInput = component.SlotData{Count: 0}
	}
	// slotsChanged after the take rebuilds the list (selection reset to -1, result cleared). A chained
	// cut requires the player to re-select. Faithful to StonecutterMenu.slotsChanged firing post-take.
	t.stonecutterInputChanged(oc)
}

// stonecutterResolveSlot resolves a stonecutter-WINDOW slot index (0..37) to its backing:
//
//	0      -> the input slot (oc.cutInput)
//	1      -> the result slot (oc.cutResult, take-only)
//	2..37  -> the player inventory window (main 9..35, hotbar 36..44)
type stonecutterSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	input   bool
	result  bool
	invSlot int16
	ok      bool
}

func stonecutterResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) stonecutterSlotRef {
	switch {
	case menuIdx == 0:
		return stonecutterSlotRef{oc: oc, inv: inv, input: true, invSlot: -1, ok: true}
	case menuIdx == 1:
		return stonecutterSlotRef{oc: oc, inv: inv, result: true, invSlot: -1, ok: true}
	case menuIdx >= 2 && menuIdx < 2+27:
		// main: menu 2..28 → window slots 9..35
		return stonecutterSlotRef{oc: oc, inv: inv, invSlot: int16(windowMainFirst + (menuIdx - 2)), ok: true}
	case menuIdx >= 2+27 && menuIdx < stonecutterMenuSize:
		// hotbar: menu 29..37 → window slots 36..44
		return stonecutterSlotRef{oc: oc, inv: inv, invSlot: int16(windowHotbarFirst + (menuIdx - 2 - 27)), ok: true}
	}
	return stonecutterSlotRef{}
}

func (r stonecutterSlotRef) get() component.SlotData {
	switch {
	case r.input:
		return r.oc.cutInput
	case r.result:
		return r.oc.cutResult
	default:
		return r.inv.get(r.invSlot)
	}
}

// set writes the slot backing. The RESULT slot (1) is take-only: a set into it is ignored (the result is
// the chosen recipe, never client-set — T-25-10).
func (r stonecutterSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch {
	case r.input:
		r.oc.cutInput = s
	case r.result:
		return // result is take-only
	default:
		r.inv.set(r.invSlot, s)
	}
}

// clickedStonecutter ports AbstractContainerMenu.clicked for an open stonecutter window: snapshot the
// input/result/inventory/cursor, run the click under a panic-recover (no partial mutation leaks on a
// throw, T-6-04), rebuild the recipe list if the input changed, re-send the authoritative content + the
// cursor. PICKUP/QUICK_MOVE/THROW are the supported inputs; the result (1) is take-only and a take fires
// onTakeStonecut (the input consume).
func (t *TickLoop) clickedStonecutter(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	inputBefore := oc.cutInput
	resultBefore := oc.cutResult
	resultsBefore := oc.cutResults
	selectedBefore := oc.cutSelected
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.cutInput = inputBefore
				oc.cutResult = resultBefore
				oc.cutResults = resultsBefore
				oc.cutSelected = selectedBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doStonecutterClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged: if the input changed (a place/move into/out of slot 0), rebuild the recipe list. A
	// take already re-ran this inside onTakeStonecut; a result-take leaves the input shrunk + list rebuilt.
	if !slotDataEqual(inputBefore, oc.cutInput) {
		t.stonecutterInputChanged(oc)
	}

	t.sendStonecutterContent(p)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doStonecutterClick ports the AbstractContainerMenu.doClick branches over the stonecutter window. The
// result slot (1) is take-only — a place INTO it is rejected, a take fires onTakeStonecut. PICKUP,
// QUICK_MOVE, THROW are supported; an unsupported input is a no-op (content re-sent regardless).
func (t *TickLoop) doStonecutterClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.stonecutterPickup(p, oc, inv, i, j)
	case containerInputQuickMove:
		t.stonecutterQuickMove(p, oc, inv, i)
	case containerInputSwap:
		t.menuDoSwap(p, t.stonecutterMenuViewClick(oc, inv), i, j)
	case containerInputClone:
		t.menuDoClone(p, t.stonecutterMenuViewClick(oc, inv), i)
	case containerInputThrow:
		t.stonecutterThrow(p, oc, inv, i, j)
	case containerInputPickupAll:
		t.menuDoPickupAll(p, t.stonecutterMenuViewClick(oc, inv), i, j)
	}
}

// stonecutterPickup ports the PICKUP branch: left-click (j==0) takes/puts the whole stack, right-click
// (j==1) takes half / puts one. The RESULT slot (1) is take-only: a pickup takes the whole result onto
// the cursor (or merges) and fires onTakeStonecut (consume 1 input). The input + player slots behave like
// chest cells.
func (t *TickLoop) stonecutterPickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := stonecutterResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	if ref.result {
		res := oc.cutResult
		if stackEmpty(res) {
			return // nothing to take
		}
		if stackEmpty(carried) {
			inv.setCarried(res)
		} else if stackSameItemSameComponents(res, carried) && int(carried.Count)+int(res.Count) <= stackMaxSize(carried) {
			c := carried
			c.Count += res.Count
			inv.setCarried(c)
		} else {
			return // cursor occupied by a different/full item: cannot take the result
		}
		t.onTakeStonecut(oc) // consume 1 input + rebuild the list
		return
	}

	primary := j == 0
	slotItem := ref.get()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(stonecutterSafeInsert(ref, &c, place))
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
		ref.set(stonecutterSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// stonecutterSafeInsert ports Slot.safeInsert over a stonecutterSlotRef (input/player cells; the result is
// never an insert target — stonecutterPickup guards it). Mirrors craftSafeInsert.
func stonecutterSafeInsert(ref stonecutterSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// stonecutterQuickMove ports StonecutterMenu.quickMoveStack: shift-click the result moves the whole result
// into the player inventory and fires onTakeStonecut; from the input it moves into the player inventory;
// from a player cell it moves into the input slot (if stonecuttable). v1 ports the common single-cut shift
// of the result + the input↔player moves (the "cut as many as fit" multi-loop is a faithful follow-up).
// CITE StonecutterMenu.quickMoveStack.
func (t *TickLoop) stonecutterQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := stonecutterResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.result {
		res := oc.cutResult
		if stackEmpty(res) {
			return
		}
		work := res
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return // no room: nothing cut
		}
		t.onTakeStonecut(oc)
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src
	if ref.input {
		// Input → player inventory.
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
		ref.set(work)
		return
	}
	// Player → the input slot (only if the input is empty / same item with room).
	if !t.stonecutterMoveIntoInput(oc, &work) {
		return
	}
	ref.set(work)
}

// stonecutterMoveIntoInput moves *stack into the single input slot: merge into the same-item input (up to
// max stack) or fill an empty input. Mirrors craftMoveIntoGrid over a 1-cell destination.
func (t *TickLoop) stonecutterMoveIntoInput(oc *openContainer, stack *component.SlotData) bool {
	if stackEmpty(*stack) {
		return false
	}
	existing := oc.cutInput
	if stackEmpty(existing) {
		slotMax := chestSlotMax(*stack)
		place := int(stack.Count)
		if slotMax < place {
			place = slotMax
		}
		oc.cutInput = stackCopyWithCount(*stack, place)
		stack.Count = toVar(int(stack.Count) - place)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		return true
	}
	if stackSameItemSameComponents(*stack, existing) {
		slotMax := chestSlotMax(existing)
		sum := int(existing.Count) + int(stack.Count)
		if sum <= slotMax {
			existing.Count = toVar(sum)
			oc.cutInput = existing
			stack.Count = 0
			return true
		}
		if int(existing.Count) < slotMax {
			stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
			existing.Count = toVar(slotMax)
			oc.cutInput = existing
			return true
		}
	}
	return false
}

// stonecutterThrow ports the THROW branch: with an empty cursor, Q drops 1 (j==0) or the whole stack
// (j==1) from the slot under the cursor. The result slot drops the whole result + fires onTakeStonecut.
func (t *TickLoop) stonecutterThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := stonecutterResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.result {
		t.playerDrop(p, cur, true)
		t.onTakeStonecut(oc)
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

// stonecutterMenuViewClick adapts the OPEN STONECUTTER window (input 0 / result 1 / player 2..37) to the
// generic menuView. The RESULT slot (1) is take-only (mayPlace false) and fires onTakeStonecut on take
// (consume 1 input); input + player cells are plain. canTakeItemForPickAll is false for the result slot
// (StonecutterMenu.canTakeItemForPickAll). The clickedStonecutter wrapper rebuilds the recipe list after
// the click when the input changed.
func (t *TickLoop) stonecutterMenuViewClick(oc *openContainer, inv *Inventory) menuView {
	return menuView{
		size: stonecutterMenuSize,
		inv:  inv,
		slotAt: func(idx int) menuSlotView {
			ref := stonecutterResolveSlot(oc, inv, idx)
			if !ref.ok {
				return menuSlotView{}
			}
			isResult := ref.result
			return menuSlotView{
				ok:            true,
				getItem:       func() component.SlotData { return ref.get() },
				setByPlayer:   func(s component.SlotData) { ref.set(s) },
				mayPickupFn:   func(*tickPlayer) bool { return true },
				mayPlaceFn:    func(component.SlotData) bool { return !isResult },
				maxStackFn:    func(s component.SlotData) int { return chestSlotMax(s) },
				onSwapCraftFn: func(int) {},
				onTakeFn: func(_ *tickPlayer, _ component.SlotData) {
					if isResult {
						t.onTakeStonecut(oc)
					}
				},
			}
		},
		canTakeItemForPickAll: func(_ component.SlotData, i int) bool { return i != 1 },
	}
}
