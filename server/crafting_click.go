package server

// crafting_click.go — the recipe ENGINE wired to the menu: the 1:1 ResultSlot.onTake consume + the
// CraftingMenu.slotChangedCraftingGrid result recompute, both routed through the plugin matcher
// (Manager.Match). This is the Phase-25 dogfood point made observable: the 2x2 player grid AND the 3x3
// crafting_table menu both compute their result + consume THROUGH the plugin, never a hardcoded Go
// table. Tick-owned (TICK-05): every method runs only on the tick goroutine, so the GO-applies-deltas
// consume over the grid is race-clean.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, javap -c -p this session, cited per function):
//   slotChangedCraftingGrid : net.minecraft.world.inventory.CraftingMenu.slotChangedCraftingGrid
//                             (asCraftInput -> getRecipeFor -> assemble -> setItem(0,result|EMPTY))
//   onTakeCraft             : net.minecraft.world.inventory.ResultSlot.onTake
//                             (asPositionedCraftInput -> getRemainingItems -> per-cell removeItem(slot,1)
//                              + the remaining bucket-back)
// The MATCH (getRecipeFor) is the plugin call; the SHRINK (asPositionedCraftInput) reuses the same 1:1
// level/recipe.OfPositioned the matcher used, so the consume footprint is identical to the match.

import (
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/recipe"
	pk "github.com/imhinotori/sulfur/net/packet"
	"go.starlark.net/starlark"
)

// craftView is the tick-owned view of a crafting grid + its result slot, abstracting the two backings:
// the 2x2 PLAYER grid (the InventoryMenu — result at window slot 0, grid at window slots 1-4) and the
// 3x3 crafting_table grid (the transient openContainer.craftGrid + its menu result). cell indices are
// 0..w*h-1 (row-major); getCell/setCell address the grid, getResult/setResult the result slot. This is
// the Sulfur analogue of CraftingMenu.craftSlots + ResultContainer narrowed to what the engine needs.
type craftView struct {
	w, h    int
	getCell func(i int) component.SlotData
	setCell func(i int, s component.SlotData)
	getRes  func() component.SlotData
	setRes  func(s component.SlotData)
}

// playerCraftView builds the 2x2 player-grid view over the InventoryMenu window: result = window slot
// 0 (ResultSlot), grid cells 0..3 = window slots 1..4 (the 2x2 CraftSlots, row-major). CITE
// InventoryMenu: addResultSlot (slot 0) then the 2x2 CraftingContainer (slots 1-4).
func playerCraftView(inv *Inventory) craftView {
	return craftView{
		w: 2, h: 2,
		getCell: func(i int) component.SlotData { return inv.get(int16(1 + i)) },
		setCell: func(i int, s component.SlotData) { inv.set(int16(1+i), s) },
		getRes:  func() component.SlotData { return inv.get(0) },
		setRes:  func(s component.SlotData) { inv.set(0, s) },
	}
}

// craftGridPayload builds the host matcher payload for the grid: a frozen dict
// {"w","h","cells":[(id,count),...]} (row-major), the (id,count) scalars the plugin reads. Matches the
// assets/crafting/main.star contract. The values are plain scalars (the Phase-22 frozen-payload
// discipline — no live handle), so the matcher Call carries no tick-owned mutable state.
func craftGridPayload(v craftView) starlark.Value {
	cells := make([]starlark.Value, v.w*v.h)
	for i := 0; i < v.w*v.h; i++ {
		s := v.getCell(i)
		id := 0
		cnt := 0
		if !stackEmpty(s) {
			id = int(s.ItemID)
			cnt = int(s.Count)
		}
		cells[i] = starlark.Tuple{starlark.MakeInt(id), starlark.MakeInt(cnt)}
	}
	d := starlark.NewDict(3)
	_ = d.SetKey(starlark.String("w"), starlark.MakeInt(v.w))
	_ = d.SetKey(starlark.String("h"), starlark.MakeInt(v.h))
	_ = d.SetKey(starlark.String("cells"), starlark.NewList(cells))
	return d
}

// slotChangedCraftingGrid ports CraftingMenu.slotChangedCraftingGrid: recompute the result slot from
// the current grid via the plugin matcher (getRecipeFor -> assemble). On a match it sets the result
// slot to the assembled stack; on no match (or no plugin) it CLEARS the result slot (setItem(0, EMPTY)).
// Called on every grid change AND after every take (the chained-crafting re-match, Pitfall 6).
//
// 1:1 net.minecraft.world.inventory.CraftingMenu.slotChangedCraftingGrid
func (t *TickLoop) slotChangedCraftingGrid(v craftView) {
	if t.plugins == nil {
		v.setRes(component.SlotData{Count: 0}) // no matcher: empty result (faithful no-recipe path)
		return
	}
	id, count, ok := t.plugins.Match(craftGridPayload(v))
	if !ok {
		v.setRes(component.SlotData{Count: 0}) // Optional.isEmpty() -> setItem(0, EMPTY)
		return
	}
	// assemble() -> the result stack. Sulfur builds the (id,count) SlotData; the result is take-only
	// (the menu result slot has mayPlace=false), so a bare (id,count) is the faithful assembled stack.
	v.setRes(component.SlotData{ItemID: toItemID(id), Count: toVar(count)})
}

// onTakeCraft ports ResultSlot.onTake: when the player TAKES the crafted result, consume exactly 1 from
// each non-empty cell in the POSITIONED footprint (asPositionedCraftInput shrink) and apply the
// per-cell remaining bucket-back, then re-run slotChangedCraftingGrid (chained crafting). GO applies the
// consume over the tick-owned grid (the locked CONTEXT decision — NOT a whole-new-grid return).
//
// The jar loop (verified bytecode):
//
//	positioned = craftSlots.asPositionedCraftInput(); input=positioned.input(); left; top
//	remaining  = getRemainingItems(input, level)               // indexed by input row-major (input.width)
//	for j(row) in 0..input.height():
//	  for k(col) in 0..input.width():
//	    slot      = (col+left) + (top+row)*craftSlots.getWidth()   // REAL grid index
//	    itemstack = craftSlots.getItem(slot)
//	    remItem   = remaining.get(col + row*input.width())
//	    if !itemstack.isEmpty(): craftSlots.removeItem(slot, 1); itemstack = craftSlots.getItem(slot)
//	    if !remItem.isEmpty():
//	      if itemstack.isEmpty():                       craftSlots.setItem(slot, remItem)
//	      elif isSameItemSameComponents(remItem,itemstack): remItem.grow(itemstack.count); setItem(slot,remItem)
//	      elif !player.inventory.add(remItem):          player.drop(remItem, false)
//
// 1:1 net.minecraft.world.inventory.ResultSlot.onTake
func (t *TickLoop) onTakeCraft(p *tickPlayer, inv *Inventory, v craftView) {
	// asPositionedCraftInput(): the bounding-box SHRINK over the w*h grid (the SAME 1:1
	// CraftingInput.ofPositioned the matcher used). Build the recipe.Stack cells from the grid.
	cells := make([]recipe.Stack, v.w*v.h)
	for i := 0; i < v.w*v.h; i++ {
		s := v.getCell(i)
		if !stackEmpty(s) {
			cells[i] = recipe.Stack{ID: int(s.ItemID), Count: int(s.Count)}
		}
	}
	pos := recipe.OfPositioned(cells, v.w, v.h)
	if pos.Empty {
		return // an all-empty grid has nothing to consume (no take should reach here).
	}

	// getRemainingItems(input, level): the per-cell leftover, indexed by the INPUT (positioned) grid
	// row-major (input.width). The gate recipes default to all-empty (defaultCraftingReminder); a real
	// per-item bucket-back is the structured follow-up via Manager.Remaining (kept all-empty here).
	rem := make([]component.SlotData, pos.Width*pos.Height)

	for row := 0; row < pos.Height; row++ {
		for col := 0; col < pos.Width; col++ {
			slot := (col + pos.Left) + (pos.Top+row)*v.w // REAL grid index (craftSlots.getWidth()==v.w)
			item := v.getCell(slot)
			remItem := rem[col+row*pos.Width]

			if !stackEmpty(item) {
				// removeItem(slot, 1): consume exactly 1 from this cell (ContainerHelper.removeItem).
				item = craftRemoveOne(v, slot)
			}

			if !stackEmpty(remItem) {
				switch {
				case stackEmpty(item):
					v.setCell(slot, remItem) // setItem(slot, remItem)
				case stackSameItemSameComponents(remItem, item):
					remItem.Count += item.Count // remItem.grow(item.getCount())
					v.setCell(slot, remItem)    // setItem(slot, remItem)
				default:
					if !t.invAdd(inv, &remItem) { // player.getInventory().add(remItem)
						t.playerDrop(p, remItem, false) // player.drop(remItem, false)
					}
				}
			}
		}
	}

	// The chained re-match (Pitfall 6): after consuming, recompute the result so a still-matching grid
	// repopulates slot 0 (the CraftingMenu.slotsChanged that fires after a take). Vanilla recomputes via
	// the container-changed callback; Sulfur calls it explicitly here.
	t.slotChangedCraftingGrid(v)
}

// craftRemoveOne ports Container.removeItem(slot, 1) for a craft grid cell: split 1 off the cell's
// stack, write the shrunk remainder back, and return the (shrunk) cell contents — the `itemstack =
// craftSlots.getItem(slot)` re-read after removeItem in the jar loop. Reuses the 1:1 stackSplit.
func craftRemoveOne(v craftView, slot int) component.SlotData {
	cur := v.getCell(slot)
	if stackEmpty(cur) {
		return component.SlotData{Count: 0}
	}
	_ = stackSplit(&cur, 1) // removes min(1, count); cur shrinks by 1
	v.setCell(slot, cur)
	return cur
}

// toItemID converts an int item id to the component.SlotData ItemID field type (pk.VarInt).
// Centralized so the (id,count) -> SlotData construction is consistent across the crafting engine.
func toItemID(id int) pk.VarInt { return pk.VarInt(id) }

// ---------------------------------------------------------------------------------------------------
// The 3x3 crafting-table window CLICK ENGINE (Task 2) — a clone of chest_click.go over the CraftingMenu
// slot layout (result 0, grid 1-9, player main 10-36, hotbar 37-45). The result slot (0) is take-only
// (ResultSlot.mayPlace == false) and a take fires onTakeCraft (the SAME 1:1 consume as the 2x2, w=h=3
// over the transient craftGrid); a grid change re-runs slotChangedCraftingGrid (re-match). Supported
// ContainerInputs: PICKUP, QUICK_MOVE, THROW — the operations a crafting menu actually receives. Jar
// cites: AbstractContainerMenu.doClick (PICKUP/QUICK_MOVE/THROW) + CraftingMenu.quickMoveStack.

// craftSlotRef resolves a crafting-WINDOW slot index (0..45) to its backing + local index:
//
//	0      -> the result slot (oc.craftResult, take-only)
//	1..9   -> the 3x3 grid (oc.craftGrid[0..8])
//	10..45 -> the player inventory window (main 9..35, hotbar 36..44)
//
// An out-of-range index returns ok=false. This is the CraftingMenu slot→container mapping.
type craftSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	result  bool  // the result slot (0)
	gridIdx int   // index into oc.craftGrid (grid slot), -1 otherwise
	invSlot int16 // player inventory window slot (player slot), -1 otherwise
	ok      bool
}

func craftingResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) craftSlotRef {
	switch {
	case menuIdx == 0:
		return craftSlotRef{oc: oc, inv: inv, result: true, gridIdx: -1, invSlot: -1, ok: true}
	case menuIdx >= 1 && menuIdx <= 9:
		return craftSlotRef{oc: oc, inv: inv, gridIdx: menuIdx - 1, invSlot: -1, ok: true}
	case menuIdx >= 10 && menuIdx < 10+27:
		// main: menu 10..36 → window slots 9..35
		return craftSlotRef{oc: oc, inv: inv, gridIdx: -1, invSlot: int16(windowMainFirst + (menuIdx - 10)), ok: true}
	case menuIdx >= 10+27 && menuIdx < craftingMenuSize:
		// hotbar: menu 37..45 → window slots 36..44
		return craftSlotRef{oc: oc, inv: inv, gridIdx: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 10 - 27)), ok: true}
	}
	return craftSlotRef{}
}

func (r craftSlotRef) get() component.SlotData {
	switch {
	case r.result:
		return r.oc.craftResult
	case r.gridIdx >= 0:
		return r.oc.craftGrid[r.gridIdx]
	default:
		return r.inv.get(r.invSlot)
	}
}

// set writes the slot backing. The RESULT slot is take-only: a set into it is ignored (the result is
// owned by the matcher, never client-set — T-25-06).
func (r craftSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch {
	case r.result:
		return // result is take-only (mayPlace == false)
	case r.gridIdx >= 0:
		r.oc.craftGrid[r.gridIdx] = s
	default:
		r.inv.set(r.invSlot, s)
	}
}

// clickedCrafting ports AbstractContainerMenu.clicked for an open crafting window: snapshot the grid +
// result + player + cursor, run doCraftingClick under a panic-recover (the vanilla clicked() try block —
// no partial mutation leaks on a throw, T-6-04), recompute the result through the matcher, then re-send
// the authoritative content + sync the carried cursor. The whole window is re-sent (the client snaps to
// authoritative), like the chest engine.
func (t *TickLoop) clickedCrafting(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	gridBefore := oc.craftGrid
	resultBefore := oc.craftResult
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.craftGrid = gridBefore
				oc.craftResult = resultBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doCraftingClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged: recompute the result from the (post-click) grid. A take already re-ran this inside
	// onTakeCraft, but a grid-only change (place/move) needs it here. Idempotent.
	t.slotChangedCraftingGrid(craftingTableView(oc))

	// broadcastChanges: re-send the full authoritative crafting window so the client reflects the move.
	t.sendCraftingContent(p)

	// synchronizeCarriedToRemote: sync the cursor on change (ClientboundContainerSetSlot(-1, ...)).
	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doCraftingClick ports the AbstractContainerMenu.doClick branches over the crafting window. PICKUP,
// QUICK_MOVE, THROW are the supported inputs; an unsupported/forged input is a no-op (the authoritative
// content is re-sent regardless). The result slot (0) is take-only — a place INTO it is rejected, and a
// take fires onTakeCraft.
func (t *TickLoop) doCraftingClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
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
			t.craftPickup(p, oc, inv, i, j)
		} else {
			// doClick QUICK_MOVE loop (bytecode offsets 699-741): move once, then while the source slot has
			// re-populated with the SAME item (the result re-assembles from the grid, or a stack keeps
			// feeding), move again — so shift-clicking a craftable result crafts as many as fit / ingredients
			// allow, and a stack keeps draining. slotChangedCraftingGrid re-assembles the result each pass.
			ref := craftingResolveSlot(oc, inv, i)
			moved := t.craftQuickMove(p, oc, inv, i)
			for !stackEmpty(moved) && ref.ok {
				t.slotChangedCraftingGrid(craftingTableView(oc)) // re-assemble the result from the grid
				if !stackSameItem(ref.get(), moved) {
					break
				}
				moved = t.craftQuickMove(p, oc, inv, i)
			}
		}
	case containerInputSwap:
		t.menuDoSwap(p, t.craftingMenuViewClick(oc, inv), i, j)
	case containerInputClone:
		t.menuDoClone(p, t.craftingMenuViewClick(oc, inv), i)
	case containerInputThrow:
		t.craftThrow(p, oc, inv, i, j)
	case containerInputPickupAll:
		t.menuDoPickupAll(p, t.craftingMenuViewClick(oc, inv), i, j)
	}
}

// craftPickup ports the PICKUP branch over the crafting window: left-click (j==0) takes/puts the whole
// stack, right-click (j==1) takes half / puts one. The RESULT slot (0) is take-only: a pickup of it takes
// the WHOLE result onto the cursor (or merges) and fires onTakeCraft (the consume); you cannot put INTO
// it. The grid + player slots behave like chest cells.
func (t *TickLoop) craftPickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if j != 0 && j != 1 {
		return
	}
	if i < 0 {
		return
	}
	ref := craftingResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	// The RESULT slot: take-only. ResultSlot.mayPlace == false, so you can only TAKE.
	if ref.result {
		res := oc.craftResult
		if stackEmpty(res) {
			return // nothing to take
		}
		// Take onto the cursor: empty cursor -> take whole result; same-item cursor -> merge if room;
		// different item -> no-op (you cannot swap into a take-only slot). Vanilla: the result is taken
		// only when it can be picked up (mayPickup) and placed on the cursor.
		if stackEmpty(carried) {
			inv.setCarried(res)
		} else if stackSameItemSameComponents(res, carried) && int(carried.Count)+int(res.Count) <= stackMaxSize(carried) {
			c := carried
			c.Count += res.Count
			inv.setCarried(c)
		} else {
			return // cursor occupied by a different/full item: cannot take the result
		}
		// onTake: clear the result + apply the 1:1 per-cell consume + chained re-match.
		t.onTakeCraft(p, inv, craftingTableView(oc))
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
			ref.set(craftSafeInsert(ref, &c, place))
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
		ref.set(craftSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// craftSafeInsert ports Slot.safeInsert over a craftSlotRef (grid/player cells; the result is never an
// insert target — craftPickup guards it): place up to min(increment, stack.count, slotMax-existing) of
// *stack, shrink *stack, and return the slot's new contents. Mirrors chestSafeInsert.
func craftSafeInsert(ref craftSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// craftQuickMove ports CraftingMenu.quickMoveStack: shift-click moves a stack between the grid/result and
// the player inventory. It returns the MOVED stack copy (EMPTY when nothing moved) so the doClick QUICK_MOVE
// loop can repeat (craft-as-many-as-fit). Branches (verified CraftingMenu.quickMoveStack bytecode):
//   - result (0):    moveItemStackTo(player 10..46, reverse=true) + onTakeCraft (the consume). Repeated by
//     the caller's loop until the grid runs dry (each iteration re-assembles the result).
//   - grid (1..9):   moveItemStackTo(player 10..46, reverse=FALSE) — fill main-storage-first.
//   - player (10..): FIRST moveItemStackTo(grid 1..10, false); if that does not fully move, fall through to
//     the main<->hotbar cross-move (main->hotbar, hotbar->main).
//
// CITE CraftingMenu.quickMoveStack (offsets 47-172).
func (t *TickLoop) craftQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) component.SlotData {
	empty := component.SlotData{Count: 0}
	if i < 0 {
		return empty
	}
	ref := craftingResolveSlot(oc, inv, i)
	if !ref.ok {
		return empty
	}

	if ref.result {
		// Shift-take the result: deposit the whole result into the player inventory, then consume.
		res := oc.craftResult
		if stackEmpty(res) {
			return empty
		}
		work := res
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return empty // no room: nothing crafted (quickMoveStack returns EMPTY)
		}
		oc.craftResult = work
		if stackEmpty(work) {
			oc.craftResult = component.SlotData{Count: 0}
		}
		// The result moved (fully or partly); fire the consume (one craft). The caller loops while the
		// re-assembled result still matches, so this crafts as many as fit / as ingredients allow.
		t.onTakeCraft(p, inv, craftingTableView(oc))
		// Return the moved item so the loop's isSameItem(ref.get(), moved) can re-fire.
		moved := res
		moved.Count = res.Count - work.Count
		if moved.Count <= 0 {
			return empty
		}
		return moved
	}

	src := ref.get()
	if stackEmpty(src) {
		return empty
	}
	work := src
	if ref.gridIdx >= 0 {
		// Grid → player inventory, reverse=FALSE (main-storage-first), per bytecode offset 154.
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, false) {
			return empty
		}
	} else {
		// Player cell: FIRST try the 3x3 grid; if it does not fully move, fall through to main<->hotbar.
		gridMoved := t.craftMoveIntoGrid(oc, &work)
		if !gridMoved || !stackEmpty(work) {
			// main storage (window 9..35) -> hotbar (36..45); hotbar (36..44) -> main (9..36). The grid
			// fill above already shrank `work` if it took part; the cross-move handles the remainder.
			if ref.invSlot >= windowMainFirst && ref.invSlot < windowHotbarFirst {
				if !t.moveItemStackTo(inv, &work, windowHotbarFirst, 45, false) && !gridMoved {
					return empty
				}
			} else {
				if !t.moveItemStackTo(inv, &work, windowMainFirst, windowHotbarFirst, false) && !gridMoved {
					return empty
				}
			}
		}
	}
	ref.set(work)
	moved := src
	moved.Count = src.Count - work.Count
	if moved.Count <= 0 {
		return empty
	}
	return moved
}

// craftMoveIntoGrid ports moveItemStackTo for the 3x3 grid destination: pass 1 merges *stack into
// existing same-item grid cells, pass 2 fills empty grid cells. Mirrors chestMoveInto over the 9 cells.
func (t *TickLoop) craftMoveIntoGrid(oc *openContainer, stack *component.SlotData) bool {
	moved := false
	if stackIsStackable(*stack) {
		for i := 0; i < 9 && !stackEmpty(*stack); i++ {
			existing := oc.craftGrid[i]
			if stackEmpty(existing) || !stackSameItemSameComponents(*stack, existing) {
				continue
			}
			sum := int(existing.Count) + int(stack.Count)
			slotMax := chestSlotMax(existing)
			if sum <= slotMax {
				existing.Count = toVar(sum)
				oc.craftGrid[i] = existing
				stack.Count = 0
				moved = true
			} else if int(existing.Count) < slotMax {
				stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
				existing.Count = toVar(slotMax)
				oc.craftGrid[i] = existing
				moved = true
			}
		}
	}
	if !stackEmpty(*stack) {
		for i := 0; i < 9; i++ {
			if !stackEmpty(oc.craftGrid[i]) {
				continue
			}
			slotMax := chestSlotMax(*stack)
			place := int(stack.Count)
			if slotMax < place {
				place = slotMax
			}
			oc.craftGrid[i] = stackCopyWithCount(*stack, place)
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

// craftThrow ports the THROW branch over a crafting window: with an empty cursor, Q drops 1 (j==0) or the
// whole stack (j==1) from the slot under the cursor. The result slot drops the whole result + fires the
// consume (a Q on the result crafts-and-drops).
func (t *TickLoop) craftThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	ref := craftingResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.result {
		// Q on the result: drop the whole result, then consume (a craft).
		t.playerDrop(p, cur, true)
		t.onTakeCraft(p, inv, craftingTableView(oc))
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

// closeCraftingWindow ports CraftingMenu.removed → AbstractContainerMenu.clearContainer(player,
// craftSlots): every grid cell is returned to the player inventory (invAdd; overflow -> playerDrop as an
// ItemEntity), NOT persisted (the transient grid, Pitfall 7). Called from handleContainerClose when the
// open window is a crafting_table. The result slot is NOT returned (it is a virtual assembled stack, not
// real items — vanilla clears only craftSlots).
//
// 1:1 net.minecraft.world.inventory.AbstractContainerMenu.clearContainer (over craftSlots)
func (t *TickLoop) closeCraftingWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	for i := 0; i < 9; i++ {
		s := oc.craftGrid[i]
		oc.craftGrid[i] = component.SlotData{Count: 0} // removeItemNoUpdate(i)
		if stackEmpty(s) {
			continue
		}
		// dropOrPlaceInInventory: add to the inventory, else drop as an ItemEntity.
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.craftResult = component.SlotData{Count: 0}
}

// craftingMenuViewClick adapts the OPEN CRAFTING-TABLE window (result 0 / grid 1..9 / player 10..45) to
// the generic menuView so the shared SWAP / CLONE / PICKUP_ALL branches (menu_click.go) drive it. The
// RESULT slot (0) is take-only (mayPlace false) and fires onTakeCraft on take (the 3x3 consume); the grid
// + player cells are plain. canTakeItemForPickAll returns false for the result slot (CraftingMenu.
// canTakeItemForPickAll: slot.container != resultSlots). The clickedCrafting wrapper re-runs
// slotChangedCraftingGrid after the click, so a SWAP into a grid cell re-assembles the result.
func (t *TickLoop) craftingMenuViewClick(oc *openContainer, inv *Inventory) menuView {
	return menuView{
		size: craftingMenuSize,
		inv:  inv,
		slotAt: func(idx int) menuSlotView {
			ref := craftingResolveSlot(oc, inv, idx)
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
				onTakeFn: func(pl *tickPlayer, _ component.SlotData) {
					if isResult {
						t.onTakeCraft(pl, inv, craftingTableView(oc)) // ResultSlot.onTake (3x3 consume)
					}
				},
			}
		},
		canTakeItemForPickAll: func(_ component.SlotData, i int) bool { return i != 0 },
	}
}
