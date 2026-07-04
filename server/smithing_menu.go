package server

// smithing_menu.go — the SMITHING_TABLE block + its 3-input TRANSFORM menu (SmithingMenu, an
// ItemCombinerMenu). Three INPUT slots (TEMPLATE 0, BASE 1, ADDITION 2), a virtual RESULT slot (a
// ResultContainer entry, the matched SmithingRecipe's assemble()), and a take that consumes the 3
// inputs. It clones the container-menu scaffolding (openContainer.kind, nextContainerCounter,
// openScreen, containerSetContent, the close-returns-inputs).
//
// 1:1 jar chain (temp/cache/26.2-inner.jar, CFR this session):
//
//	SmithingTableBlock.useWithoutItem: !clientSide -> player.openMenu(getMenuProvider(...)) + awardStat; SUCCESS.
//	  getMenuProvider = new SmithingMenu(id, inv, ContainerLevelAccess.create(level, pos));
//	  CONTAINER_TITLE = "container.upgrade" ("Upgrade Gear").
//	SmithingMenu (extends ItemCombinerMenu): slot defs TEMPLATE 0, BASE 1, ADDITION 2, RESULT 3, each
//	  input gated by its RecipePropertySet.test (SMITHING_TEMPLATE / SMITHING_BASE / SMITHING_ADDITION);
//	  addStandardInventorySlots (inv 4..30 + hotbar 31..39).
//	ItemCombinerMenu.slotsChanged(inputSlots) -> createResult.
//	SmithingMenu.createResult: input = SmithingRecipeInput(getItem 0/1/2); recipe = getRecipeFor(SMITHING,
//	  input); present -> resultSlots.setItem(0, recipe.assemble(input)); absent -> setItem(0, EMPTY).
//	SmithingTransformRecipe.assemble = TransmuteRecipe.createWithOriginalComponents(result, base) =
//	  result.apply(result.count, base.getComponentsPatch()) -> the RESULT item id carrying the BASE stack's
//	  component patch (enchants + custom name + damage PRESERVED; the result templates carry no own components).
//	SmithingMenu.onTake: shrinkStackInSlot(0/1/2) (each input -1) + levelEvent(1044).
//	ItemCombinerMenu.removed -> clearContainer(player, inputSlots)  (inputs returned; result virtual).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/recipe"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// smithingMenuSize is the smithing-window slot count: 3 inputs + 1 result + 27 main + 9 hotbar = 40.
// (SmithingMenu: TEMPLATE 0, BASE 1, ADDITION 2, RESULT 3, inv 4..30, use-row 31..39.) Verified the ctor.
const smithingMenuSize = 3 + 1 + 27 + 9 // 40

// smithingResultSlot is the ItemCombinerMenu result slot index for the smithing menu (RESULT_SLOT=3).
const smithingResultSlot = 3

// smithingTitle is the smithing window's display name. Vanilla CONTAINER_TITLE = "container.upgrade" →
// "Upgrade Gear". v1 sends the plain literal (structured to become a translatable Component later).
const smithingTitle = "Upgrade Gear"

// openSmithing ports SmithingTableBlock.useWithoutItem → ServerPlayer.openMenu for a smithing table at
// pos: close any prior window, allocate a windowId, send OpenScreen(smithing) + the initial content
// (empty inputs + result + the player inventory), and record the open-container state. Returns true.
func (t *TickLoop) openSmithing(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindSmithing, smithPos: pos}

	menuID := menuTypeID(registryid.Menu, "minecraft:smithing")
	p.client.Send(openScreen(int32(win), menuID, smithingTitle))

	// initMenu → broadcastChanges: push the full 40-slot list + the hasRecipeError DataSlot (0). The
	// SmithingMenu adds one DataSlot (hasRecipeError) set to 0 at ctor; send it so the client tracks it.
	t.sendSmithingContent(p)
	p.client.Send(containerSetData(int32(win), 0, 0)) // hasRecipeError DataSlot = 0 (no error at open)
	return true
}

// smithingMenuItems builds the 40-slot ContainerSetContent list: template (0), base (1), addition (2),
// result (3), then the player inventory — main 4..30 (window slots 9..35), hotbar 31..39 (window slots
// 36..44). Mirrors ItemCombinerMenu's createInputSlots+createResultSlot+addStandardInventorySlots order.
func smithingMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, smithingMenuSize)
	out[0] = oc.smithTemplate
	out[1] = oc.smithBase
	out[2] = oc.smithAddition
	out[3] = oc.smithResult
	for i := 0; i < 27; i++ {
		out[4+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[4+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendSmithingContent pushes the authoritative ContainerSetContent for the open smithing window. Bumps
// the player inventory's state id, exactly like sendGrindstoneContent.
func (t *TickLoop) sendSmithingContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindSmithing {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		smithingMenuItems(p.openContainer, inv), inv.getCarried()))
}

// smithingMatch finds the first smithing_transform recipe that matches the 3 input slots. Returns the
// recipe pointer (or nil) — the createResult / onTake path re-resolves the recipe the same way.
//
// 1:1 RecipeAccess.getRecipeFor(RecipeType.SMITHING, SmithingRecipeInput(template, base, addition)).
func smithingMatch(oc *openContainer) *recipe.SmithingTransform {
	template := smithingStack(oc.smithTemplate)
	base := smithingStack(oc.smithBase)
	addition := smithingStack(oc.smithAddition)
	rs := smithingTransformRecipes()
	for i := range rs {
		if recipe.MatchSmithingTransform(&rs[i], template, base, addition) {
			return &rs[i]
		}
	}
	return nil
}

// smithingStack converts a menu SlotData to a recipe.Stack (id+count) for the matcher; an empty slot
// yields an empty Stack (ID 0, Count 0) which testOptionalIngredient reads as "empty".
func smithingStack(s component.SlotData) recipe.Stack {
	if stackEmpty(s) {
		return recipe.Stack{}
	}
	return recipe.Stack{ID: int(s.ItemID), Count: int(s.Count)}
}

// smithingCreateResult ports SmithingMenu.createResult: match the 3 inputs against the smithing recipes;
// on a match set the result to assemble() (the transform preserving the base's components); on no match
// clear the result. Called after every input change.
//
// 1:1 net.minecraft.world.inventory.SmithingMenu.createResult
func (t *TickLoop) smithingCreateResult(oc *openContainer) {
	r := smithingMatch(oc)
	if r == nil {
		oc.smithResult = component.SlotData{Count: 0} // setItem(0, EMPTY)
		return
	}
	oc.smithResult = smithingAssemble(r, oc.smithBase)
}

// smithingAssemble ports SmithingTransformRecipe.assemble = TransmuteRecipe.createWithOriginalComponents(
// result, base) = result.apply(result.count, base.getComponentsPatch()). The result item id + count come
// from the recipe; the COMPONENT PATCH is copied verbatim from the BASE stack (enchants + custom name +
// damage preserved). The netherite transform result templates carry no own components, so no override is
// applied on top (applyComponents(EMPTY) is a no-op).
//
// 1:1 net.minecraft.world.item.crafting.SmithingTransformRecipe.assemble + TransmuteRecipe.
// createWithOriginalComponents + ItemStackTemplate.apply
func smithingAssemble(r *recipe.SmithingTransform, base component.SlotData) component.SlotData {
	count := r.Result.Count
	if count <= 0 {
		count = 1 // ItemStackTemplate default count.
	}
	// new ItemStack(resultItem, count, base.componentsPatch): the result carries the base's component
	// patch verbatim (AddedCount/RemovedCount/RawComponents copied), only the item id + count change.
	out := base
	out.ItemID = pk.VarInt(r.Result.ID)
	out.Count = pk.VarInt(count)
	return out
}

// closeSmithingWindow ports ItemCombinerMenu.removed → clearContainer(player, inputSlots): the 3 inputs
// are returned to the player inventory (invAdd; overflow → playerDrop as an ItemEntity), NOT persisted.
// The result is virtual (never returned). Called from handleContainerClose.
//
// 1:1 net.minecraft.world.inventory.ItemCombinerMenu.removed
func (t *TickLoop) closeSmithingWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	for _, s := range []component.SlotData{oc.smithTemplate, oc.smithBase, oc.smithAddition} {
		if stackEmpty(s) {
			continue
		}
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.smithTemplate = component.SlotData{Count: 0}
	oc.smithBase = component.SlotData{Count: 0}
	oc.smithAddition = component.SlotData{Count: 0}
	oc.smithResult = component.SlotData{Count: 0}
}
