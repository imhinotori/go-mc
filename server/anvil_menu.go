package server

// anvil_menu.go — the ANVIL menu (AnvilMenu extends ItemCombinerMenu): the 2-input + 1-result
// transient container UI where two items are combined/repaired/renamed for a level cost. A 1:1 port
// of net.minecraft.world.inventory.AnvilMenu + ItemCombinerMenu over the 26.2 jar
// (temp/cache/26.2-inner.jar, CFR/javap this session). Opens on right-clicking any anvil-family
// block (chest_open.go useBlockInteraction).
//
// 1:1 jar chain:
//
//	AnvilBlock.useWithoutItem -> player.openMenu(AnvilMenu); return SUCCESS (CONSUMES -> no place).
//	ItemCombinerMenu(ANVIL, id, inv, access, slotDefs): inputSlots = SimpleContainer(2) [slots 0,1],
//	    resultSlots = ResultContainer [slot 2], then addStandardInventorySlots(inv, 8, 84)
//	    -> main 3..29, hotbar 30..38 (39 slots). AnvilMenu ctor: addDataSlot(cost).
//	ItemCombinerMenu.slotsChanged(container): if (container == inputSlots) createResult().
//	AnvilMenu.createResult(): the full cost math (see createResult below) — cost DataSlot + result slot.
//	AnvilMenu.onTake(player, carried): consume `cost` levels (giveExperienceLevels(-cost)); shrink the
//	    additional slot by repairItemCountCost (or clear it unless pure-rename); clear the input slot;
//	    12% chance to damage/break the anvil (non-creative); reset cost.
//	AnvilMenu.setItemName(name) [from ServerboundRenameItem]: validate (<=50), store, re-createResult.
//	ItemCombinerMenu.removed(player): super.removed -> clearContainer over inputSlots (return 0,1);
//	    the result (2) is virtual (never returned).
//
// v1 subset (cited): no stats/criteria/sound; the anvil break's levelEvent(1029/1030) client SFX is a
// faithful no-op (cite AnvilMenu.onTake access.execute — the level.levelEvent break/use particle is a
// client cosmetic); the text-filter (processStreamMessage) is a no-op (no chat filter). The
// ContainerLevelAccess reach folds into the open-time reach gate. The 2 inputs are a TRANSIENT
// container (like the crafting grid) returned to the player on close — NOT a block-entity.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// anvilMenuSize is the AnvilMenu slot count: input 0 + additional 1 + result 2 + 27 main + 9 hotbar
// = 39. (ItemCombinerMenu: INPUT_SLOT=0, ADDITIONAL_SLOT=1, RESULT_SLOT=2, INV 3..29, USE_ROW 30..38.)
const anvilMenuSize = 3 + 27 + 9 // 39

// anvil slot indices (AnvilMenu constants).
const (
	anvilSlotInput  = 0 // INPUT_SLOT
	anvilSlotAdd    = 1 // ADDITIONAL_SLOT
	anvilSlotResult = 2 // RESULT_SLOT (take-only)
)

// anvil cost constants (AnvilMenu ConstantValue attributes, VERIFIED CFR).
const (
	anvilCostRename            = 1  // COST_RENAME
	anvilMaximumCost           = 40 // the too-expensive gate + the naming clamp threshold
	anvilMaxNameLength         = 50 // MAX_NAME_LENGTH
	anvilBreakChance   float32 = 0.12
)

// isAnvilBlock reports whether a block state is in the ANVIL tag (anvil / chipped_anvil /
// damaged_anvil). AnvilMenu.isValidBlock == state.is(BlockTags.ANVIL). CITE AnvilBlock.
func isAnvilBlock(s block.StateID) bool {
	return blockInTag(s, "anvil")
}

// isAnyAnvilBlock is the chest_open dispatch predicate (kept parallel to isAnyFurnaceBlock).
func isAnyAnvilBlock(s block.StateID) bool { return isAnvilBlock(s) }

// openAnvil ports AnvilBlock.useWithoutItem -> ServerPlayer.openMenu(AnvilMenu): close any prior
// window, allocate a windowId, send ClientboundOpenScreen(anvil) + the initial ContainerSetContent +
// the cost data slot, and record the transient anvil window state (empty inputs, cost 0). Returns
// true (the interaction was consumed).
func (t *TickLoop) openAnvil(p *tickPlayer, pos pk.Position) bool {
	if p == nil || p.client == nil || t.world() == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindAnvil, anvilPos: pos}

	menuID := menuTypeID(registryid.Menu, "minecraft:anvil")
	p.client.Send(openScreen(int32(win), menuID, "Repair & Name"))

	t.sendAnvilContent(p, p.openContainer)
	t.sendAnvilCost(p, p.openContainer)
	return true
}

// anvilMenuItems builds the 39-slot ContainerSetContent list: input 0, additional 1, result 2, then
// the player inventory (main 3..29 <- window 9..35, hotbar 30..38 <- window 36..44).
func anvilMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, anvilMenuSize)
	out[anvilSlotInput] = oc.anvilInput
	out[anvilSlotAdd] = oc.anvilAdd
	out[anvilSlotResult] = oc.anvilResult
	for i := 0; i < 27; i++ {
		out[3+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[3+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendAnvilContent pushes the authoritative ContainerSetContent for the open anvil window. Bumps the
// player inventory state id.
func (t *TickLoop) sendAnvilContent(p *tickPlayer, oc *openContainer) {
	if p.client == nil || oc == nil || oc.kind != containerKindAnvil {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(oc.windowID), inv.stateID, anvilMenuItems(oc, inv), inv.getCarried()))
}

// sendAnvilCost pushes the single cost data slot (AnvilMenu.cost — the green level-cost the client's
// XP-bar preview reads). CITE AnvilMenu.addDataSlot(cost).
func (t *TickLoop) sendAnvilCost(p *tickPlayer, oc *openContainer) {
	if p.client == nil || oc == nil || oc.kind != containerKindAnvil {
		return
	}
	p.client.Send(containerSetData(int32(oc.windowID), 0, int16(oc.anvilCost)))
}

// anvilResolveSlot resolves an anvil-WINDOW slot index (0..38) to its backing (input/additional/
// result virtual slots or the player inventory window slot).
type anvilSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	slot    int   // 0/1/2 for a virtual slot, -1 for a player slot
	invSlot int16 // window slot for a player cell, -1 otherwise
	ok      bool
}

func anvilResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) anvilSlotRef {
	switch {
	case menuIdx == anvilSlotInput:
		return anvilSlotRef{oc: oc, inv: inv, slot: anvilSlotInput, invSlot: -1, ok: true}
	case menuIdx == anvilSlotAdd:
		return anvilSlotRef{oc: oc, inv: inv, slot: anvilSlotAdd, invSlot: -1, ok: true}
	case menuIdx == anvilSlotResult:
		return anvilSlotRef{oc: oc, inv: inv, slot: anvilSlotResult, invSlot: -1, ok: true}
	case menuIdx >= 3 && menuIdx < 3+27:
		return anvilSlotRef{oc: oc, inv: inv, slot: -1, invSlot: int16(windowMainFirst + (menuIdx - 3)), ok: true}
	case menuIdx >= 3+27 && menuIdx < anvilMenuSize:
		return anvilSlotRef{oc: oc, inv: inv, slot: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 3 - 27)), ok: true}
	}
	return anvilSlotRef{}
}

func (r anvilSlotRef) get() component.SlotData {
	switch r.slot {
	case anvilSlotInput:
		return r.oc.anvilInput
	case anvilSlotAdd:
		return r.oc.anvilAdd
	case anvilSlotResult:
		return r.oc.anvilResult
	default:
		return r.inv.get(r.invSlot)
	}
}

// set writes the slot backing. The RESULT slot (2) is take-only (a set is ignored — ResultSlot
// rejects a client place). Setting either INPUT slot triggers createResult on the caller (slotsChanged).
func (r anvilSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch r.slot {
	case anvilSlotInput:
		r.oc.anvilInput = s
	case anvilSlotAdd:
		r.oc.anvilAdd = s
	case anvilSlotResult:
		return // take-only
	default:
		r.inv.set(r.invSlot, s)
	}
}

// clickedAnvil ports AbstractContainerMenu.clicked for an open anvil window: snapshot input/add/
// result/cost/inventory/cursor, run the click under a panic-recover, rebuild the result if an input
// changed (slotsChanged -> createResult), then re-send the authoritative content + cost + cursor.
// PICKUP/QUICK_MOVE/THROW are supported; the result (2) is take-only and a take fires onTakeAnvil
// (consume levels, shrink the additional, clear input, maybe break the anvil).
func (t *TickLoop) clickedAnvil(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	inputBefore := oc.anvilInput
	addBefore := oc.anvilAdd
	resultBefore := oc.anvilResult
	costBefore := oc.anvilCost
	nameBefore := oc.anvilName
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.anvilInput = inputBefore
				oc.anvilAdd = addBefore
				oc.anvilResult = resultBefore
				oc.anvilCost = costBefore
				oc.anvilName = nameBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doAnvilClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged: if either input changed (a place/move/take into/out of slot 0 or 1), rebuild the
	// result. A result-take already ran createResult inside onTakeAnvil (via the input/add mutation).
	if !slotDataEqual(inputBefore, oc.anvilInput) || !slotDataEqual(addBefore, oc.anvilAdd) {
		t.anvilCreateResult(p, oc)
	}

	t.sendAnvilContent(p, oc)
	t.sendAnvilCost(p, oc)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doAnvilClick dispatches the supported click inputs over the anvil window.
func (t *TickLoop) doAnvilClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.anvilPickup(p, oc, inv, i, j)
	case containerInputQuickMove:
		t.anvilQuickMove(p, oc, inv, i)
	case containerInputThrow:
		t.anvilThrow(p, oc, inv, i, j)
	}
}

// anvilMayPickupResult ports AnvilMenu.mayPickup(player, hasItem): (creative || experienceLevel >=
// cost) && cost > 0. A non-creative player without enough levels cannot take the result.
func (t *TickLoop) anvilMayPickupResult(p *tickPlayer, oc *openContainer) bool {
	if oc.anvilCost <= 0 {
		return false
	}
	if playerHasInfiniteMaterials(p) {
		return true
	}
	return int(p.experienceLevel) >= oc.anvilCost
}

// anvilPickup ports the PICKUP branch. The RESULT slot (2) is take-only + gated by mayPickup; the
// input/additional/player slots behave like chest cells (place/take/merge/swap).
func (t *TickLoop) anvilPickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := anvilResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	if ref.slot == anvilSlotResult {
		res := oc.anvilResult
		if stackEmpty(res) {
			return
		}
		if !t.anvilMayPickupResult(p, oc) {
			return // cannot afford / no result
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
		oc.anvilResult = component.SlotData{Count: 0}
		t.onTakeAnvil(p, oc) // consume levels + shrink additional + clear input + maybe break
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
			ref.set(anvilSafeInsert(ref, &c, place))
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
		ref.set(anvilSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if int(carried.Count) <= chestSlotMax(carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// anvilSafeInsert ports Slot.safeInsert over an anvilSlotRef (input/additional/player cells; the
// result is never an insert target).
func anvilSafeInsert(ref anvilSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// anvilQuickMove ports AnvilMenu/ItemCombinerMenu.quickMoveStack: the result (2) shift-moves into the
// player inventory (gated by mayPickup) + fires onTakeAnvil; the input/additional (0/1) shift-move
// into the player inventory; a player item shift-moves into the first empty input slot; else
// main<->hotbar. CITE ItemCombinerMenu.quickMoveStack.
func (t *TickLoop) anvilQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := anvilResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.slot == anvilSlotResult {
		res := oc.anvilResult
		if stackEmpty(res) || !t.anvilMayPickupResult(p, oc) {
			return
		}
		work := res
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
		oc.anvilResult = component.SlotData{Count: 0}
		t.onTakeAnvil(p, oc)
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	if ref.slot == anvilSlotInput || ref.slot == anvilSlotAdd {
		// input/additional -> player inventory (moveItemStackTo over the standard inventory window).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, false) {
			return
		}
		ref.set(work)
		return
	}

	// A PLAYER cell: move into the first empty/mergeable input slot (0 then 1). ItemCombinerMenu's
	// quickMoveStack moves an inventory item into the input slots [0, 2).
	if t.anvilMoveIntoInput(oc, anvilSlotInput, &work) || t.anvilMoveIntoInput(oc, anvilSlotAdd, &work) {
		ref.set(work)
	}
}

// anvilMoveIntoInput merges *stack into an input slot (0/1), honoring the item max stack. Returns
// whether anything moved.
func (t *TickLoop) anvilMoveIntoInput(oc *openContainer, slot int, stack *component.SlotData) bool {
	if stackEmpty(*stack) {
		return false
	}
	var existing component.SlotData
	if slot == anvilSlotInput {
		existing = oc.anvilInput
	} else {
		existing = oc.anvilAdd
	}
	setSlot := func(s component.SlotData) {
		if slot == anvilSlotInput {
			oc.anvilInput = s
		} else {
			oc.anvilAdd = s
		}
	}
	if stackEmpty(existing) {
		slotMax := chestSlotMax(*stack)
		place := min(int(stack.Count), slotMax)
		setSlot(stackCopyWithCount(*stack, place))
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
			setSlot(stackCopyWithCount(existing, sum))
			stack.Count = 0
			return true
		}
		if int(existing.Count) < slotMax {
			stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
			setSlot(stackCopyWithCount(existing, slotMax))
			return true
		}
	}
	return false
}

// anvilThrow ports the THROW branch (drop key). The result slot drops the whole result + fires
// onTakeAnvil (gated by mayPickup); input/additional/player cells drop 1 or the whole stack.
func (t *TickLoop) anvilThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := anvilResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.slot == anvilSlotResult {
		if !t.anvilMayPickupResult(p, oc) {
			return
		}
		oc.anvilResult = component.SlotData{Count: 0}
		t.playerDrop(p, cur, true)
		t.onTakeAnvil(p, oc)
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

// onTakeAnvil ports AnvilMenu.onTake(player, carried):
//
//	if (!creative) player.giveExperienceLevels(-cost);
//	if (repairItemCountCost > 0) { addition = input1; if (!empty && count > repairItemCountCost)
//	    addition.shrink(repairItemCountCost) else input1 = EMPTY }
//	else if (!onlyRenaming) input1 = EMPTY;
//	cost = 0; input0 = EMPTY;
//	access.execute(damage the anvil 12% (non-creative)).
//
// CITE AnvilMenu.onTake.
func (t *TickLoop) onTakeAnvil(p *tickPlayer, oc *openContainer) {
	if !playerHasInfiniteMaterials(p) {
		t.giveExperienceLevels(p, -oc.anvilCost) // AnvilMenu.onTake -> giveExperienceLevels(-cost)
	}
	if oc.anvilRepairUnits > 0 {
		add := oc.anvilAdd
		if !stackEmpty(add) && int(add.Count) > oc.anvilRepairUnits {
			add.Count = toVar(int(add.Count) - oc.anvilRepairUnits) // addition.shrink(repairItemCountCost)
			oc.anvilAdd = add
		} else {
			oc.anvilAdd = component.SlotData{Count: 0}
		}
	} else if !oc.anvilOnlyRenaming {
		oc.anvilAdd = component.SlotData{Count: 0}
	}
	oc.anvilCost = 0
	oc.anvilInput = component.SlotData{Count: 0}

	// access.execute: 12% chance (non-creative) to damage/break the anvil. The break's client SFX
	// (levelEvent 1029/1030) is a cited no-op. Uses the LEVEL random (getRandom().nextFloat()).
	t.maybeDamageAnvil(p, oc.anvilPos)

	// The take mutated both inputs -> createResult reruns (slotsChanged), clearing the result.
	t.anvilCreateResult(p, oc)
}

// maybeDamageAnvil ports AnvilMenu.onTake's access.execute break block: if non-creative and the
// block is still an anvil and getRandom().nextFloat() < 0.12, damage it (anvil -> chipped ->
// damaged -> destroyed). CITE AnvilBlock.damage + AnvilMenu.onTake.
func (t *TickLoop) maybeDamageAnvil(p *tickPlayer, pos pk.Position) {
	if playerHasInfiniteMaterials(p) || t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isAnvilBlock(state) {
		return
	}
	if t.anvilLevelRandomFloat() >= anvilBreakChance {
		return // levelEvent(1030) use-only: no break
	}
	newState, destroyed := anvilDamageState(state)
	if destroyed {
		// AnvilBlock.damage returned null -> removeBlock + levelEvent(1029) (destroy SFX, cited no-op).
		t.world().SetBlock(pos, block.ToStateID[block.Air{}], dimMinY)
		return
	}
	// setBlock(newState) + levelEvent(1030) (cited no-op).
	t.world().SetBlock(pos, newState, dimMinY)
}

// anvilDamageState ports AnvilBlock.damage(state): anvil -> chipped_anvil, chipped -> damaged_anvil,
// damaged -> null (destroyed). Preserves FACING. Returns (newState, destroyed).
func anvilDamageState(s block.StateID) (block.StateID, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return s, false
	}
	switch b := block.StateList[s].(type) {
	case block.Anvil:
		if sid, ok := block.ToStateID[block.ChippedAnvil{Facing: b.Facing}]; ok {
			return sid, false
		}
	case block.ChippedAnvil:
		if sid, ok := block.ToStateID[block.DamagedAnvil{Facing: b.Facing}]; ok {
			return sid, false
		}
	case block.DamagedAnvil:
		return s, true // AnvilBlock.damage returns null -> destroyed
	}
	return s, false
}

// anvilLevelRandomFloat draws level.getRandom().nextFloat() for the anvil break roll (the same level
// random cat/furnace draws). Falls back to 1.0 (never breaks) when no region random is available
// (a test without a region), keeping the anvil intact deterministically.
func (t *TickLoop) anvilLevelRandomFloat() float32 {
	if r := t.cur(); r != nil && r.levelRandom != nil {
		return r.levelRandom.NextFloat()
	}
	return 1.0
}

// handleRenameItem resolves a ServerboundRenameItem on-tick. Ports
// ServerGamePacketListenerImpl.handleRenameItem: if the open window is an AnvilMenu, menu.setItemName
// (validate <=50, store, re-createResult). A stale/forged window or an oversize name is a silent
// no-op (setItemName returns false). CITE ServerGamePacketListenerImpl.handleRenameItem ->
// AnvilMenu.setItemName.
func (t *TickLoop) handleRenameItem(p *tickPlayer, pkt pk.Packet) {
	if p == nil || p.client == nil {
		return
	}
	var name pk.String
	if err := pkt.Scan(&name); err != nil {
		return // malformed: no-op
	}
	oc := p.openContainer
	if oc == nil || oc.kind != containerKindAnvil {
		return
	}
	t.anvilSetItemName(p, oc, string(name))
}

// anvilSetItemName ports AnvilMenu.setItemName(name): validate (filterText, length <= 50); if changed,
// store + re-createResult; if the result slot has an item, apply/remove the custom_name on it (v1
// folds this into createResult's rebuild). CITE AnvilMenu.setItemName + validateName.
func (t *TickLoop) anvilSetItemName(p *tickPlayer, oc *openContainer, name string) {
	// validateName: filterText then reject length > 50. v1's filter is identity (no chat filter).
	if len(name) > anvilMaxNameLength {
		return // validateName returns null -> setItemName returns false
	}
	if name == oc.anvilName {
		return // unchanged -> setItemName returns false
	}
	oc.anvilName = name
	oc.anvilNameSet = true
	t.anvilCreateResult(p, oc)
	t.sendAnvilContent(p, oc)
	t.sendAnvilCost(p, oc)
}

// closeAnvilWindow ports ItemCombinerMenu.removed(player) -> super.removed -> clearContainer over the
// input slots (return slots 0,1 to the player). The result (2) is virtual (never returned). Called
// from handleContainerClose when the open window is an anvil.
func (t *TickLoop) closeAnvilWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	for _, s := range []component.SlotData{oc.anvilInput, oc.anvilAdd} {
		if stackEmpty(s) {
			continue
		}
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.anvilInput = component.SlotData{Count: 0}
	oc.anvilAdd = component.SlotData{Count: 0}
	oc.anvilResult = component.SlotData{Count: 0}
	oc.anvilCost = 0
}

// renameItemPacketID is the ServerboundRenameItem id (re-exported for the subtick dispatch).
var _ = packetid.ServerboundRenameItem
