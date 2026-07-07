package server

// crafting_menu.go — the crafting_table BLOCK + the 3x3 CRAFTING MENU (PLUGIN-05, Plan 25-02). A clone
// of the Phase-20 chest-open subsystem (chest_open.go): right-click a crafting_table ->
// nextContainerCounter -> ClientboundOpenScreen(minecraft:crafting) -> ContainerSetContent (the 46-slot
// layout) -> the click engine (crafting_click.go) -> close (return the grid to the player). The 3x3 grid
// feeds the SAME plugin matcher (slotChangedCraftingGrid) the 2x2 player grid uses — w=h=3, gridBase =
// the transient openContainer.craftGrid.
//
// 1:1 jar chain (temp/cache/26.2-inner.jar, javap'd this session):
//
//	CraftingTableBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (level instanceof ServerLevel) { player.openMenu(getMenuProvider(...)); player.awardStat(...); }
//	    return InteractionResult.SUCCESS;                       // CONSUMES → no place
//	the MenuProvider is `new CraftingMenu(id, inventory, ContainerLevelAccess.create(level, pos))`,
//	getDisplayName() = "container.crafting" ("Crafting Table").
//	CraftingMenu(id, inv, access): addResultSlot(0), addCraftingGridSlots (1..9), addStandardInventorySlots
//	    (27 main 10..36 + 9 hotbar 37..45) — a 3x3 TransientCraftingContainer (no block-entity).
//
// v1 subset (cited): no stats/spectator, so awardStat + the spectator branch are faithful no-ops; the
// ContainerLevelAccess (the pos the menu binds to, used only by recipe-book auto-place + the
// stillValid reach re-check) is folded into the open-time reach gate.

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// craftingMenuSize is the 3x3 crafting-window slot count: 1 result + 9 grid + 27 main + 9 hotbar = 46.
// (CraftingMenu: RESULT_SLOT=0, CRAFT_SLOT 1..9, INV 10..36, USE_ROW 37..45.) Verified CraftingMenu ctor.
const craftingMenuSize = 1 + 9 + 27 + 9 // 46

// craftingTitle is the crafting window's display name. Vanilla MenuProvider.getDisplayName for a
// crafting_table is the translatable "container.crafting" → "Crafting". v1 sends the plain literal (no
// client-side i18n dependence); structured to become a translatable Component when chat grows one.
const craftingTitle = "Crafting"

// isCraftingTableBlock reports whether a block state is a crafting_table (CraftingTableBlock). v1 opens
// the plain minecraft:crafting_table; the crafter block is out of the v1 open subset. CITE
// CraftingTableBlock instanceof.
func isCraftingTableBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:crafting_table"
}

// openCraftingTable ports CraftingTableBlock.useWithoutItem → ServerPlayer.openMenu for a crafting_table
// at pos: close any prior window, allocate a windowId, send ClientboundOpenScreen(minecraft:crafting) +
// the initial ContainerSetContent (an empty 3x3 grid + the player inventory), and record the
// open-container state with an empty transient craftGrid. Returns true (the action was consumed) — a
// crafting_table always opens (no resolution can fail; the grid is transient).
func (t *TickLoop) openCraftingTable(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil {
		return false
	}

	// ServerPlayer.openMenu: close any previously-open window first (containerMenu != inventoryMenu →
	// closeContainer). v1 tracks one window at a time. A stale crafting window's grid is NOT auto-returned
	// here (close handles that); a re-open just allocates a fresh empty grid.
	if p.openContainer != nil {
		p.openContainer = nil
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindCrafting}

	menuID := menuTypeID(registryid.Menu, "minecraft:crafting")
	p.client.Send(openScreen(int32(win), menuID, craftingTitle))

	// initMenu → broadcastChanges: push the full 46-slot list (result 0 empty, grid 1-9 empty, player
	// 10-45). The crafting-menu state id is the player inventory's state counter (one open window).
	t.sendCraftingContent(p)
	return true
}

// craftingTableView builds the craftView over an open crafting window: the 3x3 grid backs onto
// oc.craftGrid (cells 0..8 row-major), the result onto oc.craftResult. Reused by slotChangedCraftingGrid
// + onTakeCraft (crafting_click.go) — the SAME engine as the 2x2 player grid, just w=h=3 over the
// transient grid. The closures capture oc (tick-owned), so all reads/writes stay on the tick goroutine.
func craftingTableView(oc *openContainer) craftView {
	return craftView{
		w: 3, h: 3,
		getCell: func(i int) component.SlotData { return oc.craftGrid[i] },
		setCell: func(i int, s component.SlotData) {
			if stackEmpty(s) {
				s = component.SlotData{Count: 0}
			}
			oc.craftGrid[i] = s
		},
		getRes: func() component.SlotData { return oc.craftResult },
		setRes: func(s component.SlotData) {
			if stackEmpty(s) {
				s = component.SlotData{Count: 0}
			}
			oc.craftResult = s
		},
	}
}

// craftingMenuItems builds the 46-slot ContainerSetContent list for the crafting window: result 0 (from
// oc.craftResult, recomputed by the matcher), grid 1-9 (oc.craftGrid), then the player inventory — main
// 10..36 (window slots 9..35) then hotbar 37..45 (window slots 36..44). Mirrors CraftingMenu's
// addResultSlot + addCraftingGridSlots + addStandardInventorySlots order. The slot ORDER is the wire
// contract the client renders + the ContainerClick slot index maps back through (craftingResolveSlot).
func craftingMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, craftingMenuSize)
	out[0] = oc.craftResult
	for i := 0; i < 9; i++ {
		out[1+i] = oc.craftGrid[i]
	}
	// main: menu 10..36 ← inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[10+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 37..45 ← inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[37+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendCraftingContent pushes the authoritative ContainerSetContent for the open crafting window (the
// initMenu → broadcastChanges full-slot sync, and the resend after any crafting click). Bumps the player
// inventory's state id so the client tracks the authoritative state, exactly like sendChestContent.
func (t *TickLoop) sendCraftingContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindCrafting {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		craftingMenuItems(p.openContainer, inv), inv.getCarried()))
}
