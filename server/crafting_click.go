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
					v.setCell(slot, remItem)     // setItem(slot, remItem)
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
