package server

// stonecutter_menu.go — the STONECUTTER block + its single-input recipe-picker MENU (PLUGIN-05, Plan
// 25-03). The ONE cooking/stonecutting block the build-or-defer audit (deferred-blocks.md) marked BUILD:
// stonecutting needs no fuel, no cooking tick, and no block-entity — just a single INPUT slot, a result
// PICKER (the input selects a LIST of stonecutter outputs; a button-click chooses one), and a take that
// consumes exactly 1 input. It clones the Plan-25-02 chest/crafting menu scaffolding
// (openContainer.kind, nextContainerCounter, openScreen, containerSetContent, the close-returns-input).
//
// 1:1 jar chain (temp/cache/26.2-inner.jar, javap'd this session):
//
//	StonecutterBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (level instanceof ServerLevel) player.openMenu(getMenuProvider(state, level, pos));
//	    return InteractionResult.SUCCESS;                                  // CONSUMES → no place
//	the MenuProvider is `new StonecutterMenu(id, inventory, ContainerLevelAccess.create(level, pos))`,
//	getDisplayName() = "container.stonecutter" ("Stonecutter").
//	StonecutterMenu(id, inv, access): DataSlot selectedRecipeIndex (standalone, -1); addSlot(inputSlot=0);
//	    addSlot(resultSlot=1, take-only via the lambda); addStandardInventorySlots (27 main 2..28 + 9
//	    hotbar 29..37); addDataSlot(selectedRecipeIndex). A 2-slot Container + a virtual ResultContainer.
//	StonecutterMenu.slotsChanged(container): if input item changed -> input=copy; setupRecipeList(input).
//	StonecutterMenu.setupRecipeList(stack): selectedRecipeIndex=-1; resultSlot=EMPTY; if !stack.isEmpty()
//	    recipesForInput = stonecutterRecipes().selectByInput(stack); else empty.
//	StonecutterMenu.clickMenuButton(player, buttonId): if isValidRecipeIndex(buttonId) {
//	    selectedRecipeIndex=buttonId; setupResultSlot(buttonId); } return true.
//	StonecutterMenu.setupResultSlot(index): if !recipesForInput.isEmpty() && isValidRecipeIndex(index)
//	    resultSlot = recipesForInput.entries[index].recipe().assemble(...); else EMPTY; broadcastChanges.
//	StonecutterMenu.removed(player): super.removed; resultContainer.removeItemNoUpdate(1);
//	    access.execute((lvl,pos) -> clearContainer(player, container));   // input returned to the player
//
// v1 subset (cited): no stats/sound/spectator, so awardStat + the take-sound + the spectator branch are
// faithful no-ops; the ContainerLevelAccess reach re-check folds into the open-time reach gate. The
// selectByInput list comes from the SAME parsed recipe tree the embedded plugin uses (stonecutterRecipes,
// recipe_embed.go) — vanilla reads it server-side from RecipeAccess.stonecutterRecipes() the same way.

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/recipe"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// stonecutterMenuSize is the stonecutter-window slot count: 1 input + 1 result + 27 main + 9 hotbar = 38.
// (StonecutterMenu: INPUT_SLOT=0, RESULT_SLOT=1, INV 2..28, USE_ROW 29..37.) Verified the ctor bytecode.
const stonecutterMenuSize = 1 + 1 + 27 + 9 // 38

// stonecutterTitle is the stonecutter window's display name. Vanilla MenuProvider.getDisplayName is the
// translatable "container.stonecutter" → "Stonecutter". v1 sends the plain literal (structured to become
// a translatable Component when chat grows one).
const stonecutterTitle = "Stonecutter"

// stonecutterDataSelectedIndex is the DataSlot id of the selectedRecipeIndex (the single addDataSlot).
const stonecutterDataSelectedIndex = 0

// isStonecutterBlock reports whether a block state is a stonecutter (StonecutterBlock). CITE
// StonecutterBlock instanceof.
func isStonecutterBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.StateList[s].ID() == "minecraft:stonecutter"
}

// openStonecutter ports StonecutterBlock.useWithoutItem → ServerPlayer.openMenu for a stonecutter at pos:
// close any prior window, allocate a windowId, send ClientboundOpenScreen(minecraft:stonecutter) + the
// initial ContainerSetContent (an empty input + result + the player inventory) + the initial DataSlot
// (selectedRecipeIndex = -1), and record the open-container state. Returns true (the action was consumed).
func (t *TickLoop) openStonecutter(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindStonecutter, cutSelected: -1}

	menuID := menuTypeID(registryid.Menu, "minecraft:stonecutter")
	p.client.Send(openScreen(int32(win), menuID, stonecutterTitle))

	// initMenu → broadcastChanges: push the full 38-slot list + the DataSlot (selectedRecipeIndex = -1).
	t.sendStonecutterContent(p)
	p.client.Send(containerSetData(int32(win), stonecutterDataSelectedIndex, -1))
	return true
}

// stonecutterMenuItems builds the 38-slot ContainerSetContent list: input 0 (cutInput), result 1
// (cutResult), then the player inventory — main 2..28 (window slots 9..35) then hotbar 29..37 (window
// slots 36..44). Mirrors StonecutterMenu's addSlot(input)+addSlot(result)+addStandardInventorySlots.
func stonecutterMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, stonecutterMenuSize)
	out[0] = oc.cutInput
	out[1] = oc.cutResult
	// main: menu 2..28 ← inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[2+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 29..37 ← inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[2+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendStonecutterContent pushes the authoritative ContainerSetContent for the open stonecutter window
// (the initMenu → broadcastChanges full-slot sync, and the resend after any click). Bumps the player
// inventory's state id, exactly like sendChestContent/sendCraftingContent.
func (t *TickLoop) sendStonecutterContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindStonecutter {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		stonecutterMenuItems(p.openContainer, inv), inv.getCarried()))
}

// stonecutterInputChanged ports StonecutterMenu.slotsChanged → setupRecipeList: rebuild the per-input
// recipe list from the current input, reset the selection to -1, and clear the result. Called whenever
// the input slot changes (a place/take/move into slot 0). The list is selectByInput over the parsed
// stonecutting recipes (RecipeAccess.stonecutterRecipes().selectByInput).
//
// 1:1 net.minecraft.world.inventory.StonecutterMenu.setupRecipeList
func (t *TickLoop) stonecutterInputChanged(oc *openContainer) {
	oc.cutSelected = -1                         // selectedRecipeIndex.set(-1)
	oc.cutResult = component.SlotData{Count: 0} // resultSlot.set(EMPTY)
	oc.cutResults = oc.cutResults[:0]
	if stackEmpty(oc.cutInput) {
		return // empty input → SingleInputSet.empty()
	}
	// selectByInput(input): the stonecutter recipes whose ingredient accepts this input item, in parse
	// order (the same order the client's picker renders). The plugin Match seam confirms 1x1-matchability
	// (the dogfood); the per-result list is the server-side selectByInput (vanilla selects server-side).
	inID := int(oc.cutInput.ItemID)
	for i := range stonecutterRecipes() {
		r := &stonecutterRecipes()[i]
		if recipe.MatchStonecutting(r, recipe.Stack{ID: inID, Count: int(oc.cutInput.Count)}) {
			oc.cutResults = append(oc.cutResults, r.Result)
		}
	}
}

// closeStonecutterWindow ports StonecutterMenu.removed → AbstractContainerMenu.clearContainer over the
// INPUT slot: the input is returned to the player inventory (invAdd; overflow → playerDrop as an
// ItemEntity), NOT persisted. The result is virtual (a ResultContainer entry — removeItemNoUpdate, never
// returned). Called from handleContainerClose when the open window is a stonecutter.
//
// 1:1 net.minecraft.world.inventory.StonecutterMenu.removed
func (t *TickLoop) closeStonecutterWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	s := oc.cutInput
	oc.cutInput = component.SlotData{Count: 0} // clearContainer over the input
	oc.cutResult = component.SlotData{Count: 0}
	oc.cutResults = nil
	if stackEmpty(s) {
		return
	}
	stack := s
	if !t.invAdd(inv, &stack) {
		t.playerDrop(p, stack, false)
	}
}
