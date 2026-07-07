package server

// furnace_menu.go — the FURNACE / BLAST_FURNACE / SMOKER menu: the 3-slot (input/fuel/result) container UI
// over the tick-owned furnaceBE (GAMEPLAY-05). A 1:1 port of net.minecraft.world.inventory.
// AbstractFurnaceMenu + FurnaceResultSlot + FurnaceFuelSlot over the 26.2 jar (temp/cache/26.2-inner.jar,
// CFR/javap this session). Opens on right-clicking a furnace-family block (chest_open.go useBlockInteraction).
//
// 1:1 jar chain:
//
//	AbstractFurnaceBlock.useWithoutItem(state, level, pos, player, hit):
//	    if (level instanceof ServerLevel) player.openMenu(getMenuProvider(state, level, pos));
//	    return InteractionResult.SUCCESS;                                  // CONSUMES -> no place
//	the MenuProvider is the AbstractFurnaceBlockEntity itself (FurnaceMenu / BlastFurnaceMenu / SmokerMenu).
//	AbstractFurnaceMenu(type, allowedInputs, recipeBookType, id, inventory, container, data):
//	    addSlot(new Slot(container, 0, ...));               // SLOT_INPUT
//	    addSlot(new FurnaceFuelSlot(this, container, 1, ...));  // SLOT_FUEL
//	    addSlot(new FurnaceResultSlot(player, container, 2, ...)); // SLOT_RESULT (take-only, XP on take)
//	    addStandardInventorySlots(inventory, 8, 84);        // INV 3..29 + USE_ROW 30..38
//	    addDataSlots(data);                                 // 4 progress ints (litTimeRemaining,
//	                                                        //   litTotalTime, cookingTimer, cookingTotalTime)
//	AbstractFurnaceMenu.quickMoveStack(player, i): shift-move rules (result -> inv 3..39; input/fuel -> inv;
//	    a smeltable player item -> the input slot 0; a fuel -> the fuel slot 1; else inv<->hotbar).
//	FurnaceResultSlot.onTake(player, carried): checkTakeAchievements(carried) ->
//	    if (player instanceof ServerPlayer) blockEntity.awardUsedRecipesAndPopExperience(serverPlayer);
//	    removeCount = 0; super.onTake.
//	FurnaceFuelSlot.mayPlace(stack): fuelValues.isFuel(stack) || (stack.is(BUCKET) && !fuelSlot.is(BUCKET)).
//	Slot(container,0).mayPlace = base true; the input slot has no smeltable gate on manual place (only the
//	recipe-book auto-place uses acceptedInputs) — canPlaceItem(0, stack) is true (AbstractFurnaceBlockEntity.
//	canPlaceItem: slot==2 false; slot==1 fuel-or-bucket; else true).
//
// v1 subset (cited): no stats/sound/spectator, so awardStat + the take-sound + the spectator branch are
// faithful no-ops; ContainerLevelAccess reach folds into the open-time reach gate. The 3 slots are the REAL
// block-entity items (NOT a transient copy) — a click mutates the same furnaceBE.items the cook drive ticks.

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// furnaceMenuSize is the furnace-window slot count: 1 input + 1 fuel + 1 result + 27 main + 9 hotbar = 39.
// (AbstractFurnaceMenu: SLOT_INPUT=0, SLOT_FUEL=1, SLOT_RESULT=2, INV 3..29, USE_ROW 30..38.) Verified ctor.
const furnaceMenuSize = 3 + 27 + 9 // 39

// Furnace data-slot ids (AbstractFurnaceBlockEntity.DATA_*): the 4 progress ints the client arrows read.
const (
	furnaceDataLitTime      = 0 // DATA_LIT_TIME (litTimeRemaining)
	furnaceDataLitDuration  = 1 // DATA_LIT_DURATION (litTotalTime)
	furnaceDataCookProgress = 2 // DATA_COOKING_PROGRESS (cookingTimer)
	furnaceDataCookTotal    = 3 // DATA_COOKING_TOTAL_TIME (cookingTotalTime)
	furnaceDataValues       = 4 // NUM_DATA_VALUES
)

// furnaceMenuInfo resolves the wire menu-type name + display title for a furnace-family block's menu.
func furnaceMenuInfo(s block.StateID) (menuName, title string, ok bool) {
	switch {
	case isFurnaceBlock(s):
		return "minecraft:furnace", "Furnace", true
	case isBlastFurnaceBlock(s):
		return "minecraft:blast_furnace", "Blast Furnace", true
	case isSmokerBlock(s):
		return "minecraft:smoker", "Smoker", true
	}
	return "", "", false
}

// resolveFurnace returns the tick-owned furnaceBE for pos, creating an EMPTY one (with the block's cook
// subtype + blastLike flag) on first access — the analogue of a freshly-placed furnace's empty
// AbstractFurnaceBlockEntity. Returns nil only when pos is not a furnace-family block (or the world is
// unloaded). Tick-owned (t.furnaces, the openChests twin).
func (t *TickLoop) resolveFurnace(pos pk.Position, state block.StateID) *furnaceBE {
	if t.furnaces == nil {
		t.furnaces = make(map[pk.Position]*furnaceBE)
	}
	if f, ok := t.furnaces[pos]; ok {
		return f // already live
	}
	sub, ok := furnaceSubtypeOf(state)
	if !ok {
		return nil // not a furnace-family block
	}
	blastLike := sub == cookBlasting || sub == cookSmoking // blast_furnace + smoker halve getBurnDuration
	// Try to restore persisted state from the chunk's BlockEntity list first (the furnace twin of
	// resolveChest→decodeChestBE): a furnace that was saved mid-cook reloads its Items + cook progress +
	// RecipesUsed. A furnace with no recorded BE (freshly placed, never saved) synthesizes an EMPTY one —
	// the analogue of AbstractFurnaceBlockEntity's default ctor state. CITE loadAdditional.
	f := t.loadFurnaceBE(pos, sub, blastLike)
	if f == nil {
		f = &furnaceBE{
			subtype:   sub,
			blastLike: blastLike,
		}
	}
	t.furnaces[pos] = f
	return f
}

// openFurnace ports AbstractFurnaceBlock.useWithoutItem -> ServerPlayer.openMenu for a furnace-family block
// at pos: resolve (or create) the furnaceBE, close any prior window, allocate a windowId, send
// ClientboundOpenScreen(the right menu type) + the initial ContainerSetContent + the 4 progress data slots,
// and record the open-container state. Returns true (the action was consumed) once the menu is sent.
func (t *TickLoop) openFurnace(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	menuName, title, ok := furnaceMenuInfo(state)
	if !ok {
		return false
	}
	f := t.resolveFurnace(pos, state)
	if f == nil {
		return false
	}

	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindFurnace, furnacePos: pos}

	menuID := menuTypeID(registryid.Menu, menuName)
	p.client.Send(openScreen(int32(win), menuID, title))

	// initMenu -> broadcastChanges: push the full 39-slot list + the 4 progress data slots.
	t.sendFurnaceContent(p, f)
	t.sendFurnaceData(p, f)
	return true
}

// furnaceMenuItems builds the 39-slot ContainerSetContent list: input 0, fuel 1, result 2, then the player
// inventory — main 3..29 (window slots 9..35) then hotbar 30..38 (window slots 36..44). Mirrors
// AbstractFurnaceMenu's addSlot(input)+addSlot(fuel)+addSlot(result)+addStandardInventorySlots.
func furnaceMenuItems(f *furnaceBE, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, furnaceMenuSize)
	out[0] = f.items[furnaceSlotInput]
	out[1] = f.items[furnaceSlotFuel]
	out[2] = f.items[furnaceSlotResult]
	// main: menu 3..29 <- inventory window slots 9..35
	for i := 0; i < 27; i++ {
		out[3+i] = inv.get(int16(windowMainFirst + i))
	}
	// hotbar: menu 30..38 <- inventory window slots 36..44
	for i := 0; i < 9; i++ {
		out[3+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendFurnaceContent pushes the authoritative ContainerSetContent for the open furnace window (initMenu ->
// broadcastChanges, and the resend after any click or cook change). Bumps the player inventory state id.
func (t *TickLoop) sendFurnaceContent(p *tickPlayer, f *furnaceBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindFurnace {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		furnaceMenuItems(f, inv), inv.getCarried()))
}

// sendFurnaceData pushes the 4 progress data slots (litTimeRemaining, litTotalTime, cookingTimer,
// cookingTotalTime) — the ContainerData the client's flame + arrow render from (AbstractFurnaceMenu.
// addDataSlots + broadcastChanges' data-slot diff). v1 sends all 4 each change (a superset of the diff).
func (t *TickLoop) sendFurnaceData(p *tickPlayer, f *furnaceBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindFurnace {
		return
	}
	win := int32(p.openContainer.windowID)
	p.client.Send(containerSetData(win, furnaceDataLitTime, int16(f.litTimeRemaining)))
	p.client.Send(containerSetData(win, furnaceDataLitDuration, int16(f.litTotalTime)))
	p.client.Send(containerSetData(win, furnaceDataCookProgress, int16(f.cookingTimer)))
	p.client.Send(containerSetData(win, furnaceDataCookTotal, int16(f.cookingTotalTime)))
}

// broadcastFurnaceChange re-sends the authoritative content + progress data to any player whose open window
// is the furnace at pos (the serverTick `if (changed) setChanged` observable equivalent — vanilla's
// broadcastChanges pushes the changed slots + data slots to every viewer). A furnace nobody is viewing
// changes silently (its state is still authoritative in the BE). Tick-owned.
func (t *TickLoop) broadcastFurnaceChange(pos pk.Position, f *furnaceBE) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindFurnace && p.openContainer.furnacePos == pos {
			t.sendFurnaceContent(p, f)
			t.sendFurnaceData(p, f)
		}
	}
}

// furnaceResolveSlot resolves a furnace-WINDOW slot index (0..38) to its backing furnaceBE item slot or the
// player inventory window slot.
//
//	0     -> input (f.items[0])
//	1     -> fuel  (f.items[1])
//	2     -> result (f.items[2], take-only)
//	3..38 -> player inventory (main 9..35, hotbar 36..44)
type furnaceSlotRef struct {
	f       *furnaceBE
	inv     *Inventory
	beSlot  int   // 0/1/2 for a BE slot, -1 for a player slot
	invSlot int16 // window slot for a player cell, -1 for a BE slot
	ok      bool
}

func furnaceResolveSlot(f *furnaceBE, inv *Inventory, menuIdx int) furnaceSlotRef {
	switch {
	case menuIdx == 0:
		return furnaceSlotRef{f: f, inv: inv, beSlot: furnaceSlotInput, invSlot: -1, ok: true}
	case menuIdx == 1:
		return furnaceSlotRef{f: f, inv: inv, beSlot: furnaceSlotFuel, invSlot: -1, ok: true}
	case menuIdx == 2:
		return furnaceSlotRef{f: f, inv: inv, beSlot: furnaceSlotResult, invSlot: -1, ok: true}
	case menuIdx >= 3 && menuIdx < 3+27:
		return furnaceSlotRef{f: f, inv: inv, beSlot: -1, invSlot: int16(windowMainFirst + (menuIdx - 3)), ok: true}
	case menuIdx >= 3+27 && menuIdx < furnaceMenuSize:
		return furnaceSlotRef{f: f, inv: inv, beSlot: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 3 - 27)), ok: true}
	}
	return furnaceSlotRef{}
}

func (r furnaceSlotRef) get() component.SlotData {
	if r.beSlot >= 0 {
		return r.f.items[r.beSlot]
	}
	return r.inv.get(r.invSlot)
}

// set writes the slot backing. The RESULT slot (beSlot==2) is take-only: a set into it is ignored (never
// client-set — FurnaceResultSlot.mayPlace is false). Setting the INPUT slot resets the cook (setItem slot 0
// recomputes cookingTotalTime + cookingTimer=0), faithful to AbstractFurnaceBlockEntity.setItem.
func (r furnaceSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.beSlot >= 0 {
		if r.beSlot == furnaceSlotResult {
			return // result is take-only
		}
		// AbstractFurnaceBlockEntity.setItem: on a NEW input (slot 0 changed to a different item), recompute
		// cookingTotalTime = getTotalCookTime and reset cookingTimer = 0. Detect "different item" before write.
		if r.beSlot == furnaceSlotInput {
			old := r.f.items[furnaceSlotInput]
			same := !stackEmpty(s) && stackSameItemSameComponents(old, s)
			r.f.items[furnaceSlotInput] = s
			if !same {
				r.f.cookingTotalTime = r.f.getTotalCookTime()
				r.f.cookingTimer = 0
			}
			return
		}
		r.f.items[r.beSlot] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// furnaceMayPlace ports the per-slot mayPlace (AbstractFurnaceBlockEntity.canPlaceItem + FurnaceFuelSlot.
// mayPlace): result slot 2 rejects any place; fuel slot 1 accepts a fuel OR a bucket (when the slot is not
// already a bucket); the input slot 0 + player cells accept anything (base Slot.mayPlace true).
func furnaceMayPlace(f *furnaceBE, beSlot int, stack component.SlotData) bool {
	switch beSlot {
	case furnaceSlotResult:
		return false // FurnaceResultSlot.mayPlace / canPlaceItem(2) == false
	case furnaceSlotFuel:
		// FurnaceFuelSlot.mayPlace: fuelValues.isFuel(stack) || (stack.is(BUCKET) && !fuelSlot.is(BUCKET)).
		if furnaceIsFuel(int32(stack.ItemID)) {
			return true
		}
		isBucket := int32(stack.ItemID) == itemNameToID("bucket")
		fuelIsBucket := int32(f.items[furnaceSlotFuel].ItemID) == itemNameToID("bucket")
		return isBucket && !fuelIsBucket
	default:
		return true // input slot 0 + player cells (base Slot.mayPlace / canPlaceItem else-branch)
	}
}

// clickedFurnace ports AbstractContainerMenu.clicked for an open furnace window: snapshot the BE items +
// inventory + cursor, run the click under a panic-recover (no partial mutation on a throw, T-6-04), then
// re-send the authoritative content + cursor. PICKUP/QUICK_MOVE/THROW are supported; the result (2) is
// take-only and a take fires the FurnaceResultSlot XP award (onTakeFurnaceResult).
func (t *TickLoop) clickedFurnace(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	f := t.furnaces[oc.furnacePos]
	if f == nil {
		t.sendContent(p)
		return
	}
	inv := ensureInventory(p)

	itemsBefore := f.items
	usedBefore := f.recipesUsed
	xpBefore := f.recipesXP
	cookTotalBefore := f.cookingTotalTime
	cookTimerBefore := f.cookingTimer
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				f.items = itemsBefore
				f.recipesUsed = usedBefore
				f.recipesXP = xpBefore
				f.cookingTotalTime = cookTotalBefore
				f.cookingTimer = cookTimerBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doFurnaceClick(p, oc, f, inv, int(slotNum), button, int(input))
	}()

	// A click may have moved items into/out of the furnace container (a persistent state change): dirty
	// the column so the furnace state flushes on the next save pass, even without a subsequent cook tick.
	t.markFurnaceDirty(oc.furnacePos)

	t.sendFurnaceContent(p, f)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doFurnaceClick dispatches the supported click inputs over the furnace window.
func (t *TickLoop) doFurnaceClick(p *tickPlayer, oc *openContainer, f *furnaceBE, inv *Inventory, i, j, input int) {
	switch input {
	case containerInputPickup:
		t.furnacePickup(p, oc, f, inv, i, j)
	case containerInputQuickMove:
		t.furnaceQuickMove(p, oc, f, inv, i)
	case containerInputThrow:
		t.furnaceThrow(p, oc, f, inv, i, j)
	}
}

// furnacePickup ports the PICKUP branch over the furnace window: left-click (j==0) whole stack, right-click
// (j==1) half/one, honoring furnaceMayPlace for the BE slots. The RESULT slot (2) is take-only: a pickup
// takes the whole result to the cursor (or merges) and fires the XP award.
func (t *TickLoop) furnacePickup(p *tickPlayer, oc *openContainer, f *furnaceBE, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := furnaceResolveSlot(f, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()

	if ref.beSlot == furnaceSlotResult {
		res := f.items[furnaceSlotResult]
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
		f.items[furnaceSlotResult] = component.SlotData{Count: 0} // remove(all) to cursor
		t.onTakeFurnaceResult(p, f, res.Count)                    // FurnaceResultSlot.onTake: XP award
		return
	}

	primary := j == 0
	slotItem := ref.get()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) && furnaceMayPlace(f, ref.beSlot, carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(furnaceSafeInsert(ref, &c, place))
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
		if !furnaceMayPlace(f, ref.beSlot, carried) {
			return
		}
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(furnaceSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if furnaceMayPlace(f, ref.beSlot, carried) && int(carried.Count) <= chestSlotMax(carried) {
		// swap cursor <-> slot (only when the slot accepts the cursor item).
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// furnaceSafeInsert ports Slot.safeInsert over a furnaceSlotRef (input/fuel/player cells; the result is
// never an insert target — furnacePickup guards it). Mirrors chestSafeInsert; honors the slot's max stack.
func furnaceSafeInsert(ref furnaceSlotRef, stack *component.SlotData, increment int) component.SlotData {
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

// furnaceQuickMove ports AbstractFurnaceMenu.quickMoveStack: shift-click the result moves the whole result
// into the player inventory (3..39, reverse) and fires the XP award; input/fuel move into the player
// inventory; a smeltable player item moves into the input slot 0; a fuel moves into the fuel slot 1; else
// main<->hotbar. This is the exact moveItemStackTo target chain from the CFR quickMoveStack.
func (t *TickLoop) furnaceQuickMove(p *tickPlayer, oc *openContainer, f *furnaceBE, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := furnaceResolveSlot(f, inv, i)
	if !ref.ok {
		return
	}

	if ref.beSlot == furnaceSlotResult {
		res := f.items[furnaceSlotResult]
		if stackEmpty(res) {
			return
		}
		work := res
		// moveItemStackTo(stack, 3, 39, true): result -> player inventory (window 9..44), reverse.
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return // no room: nothing taken (quickMove aborts)
		}
		takenCount := res.Count - work.Count
		f.items[furnaceSlotResult] = work
		if stackEmpty(work) {
			f.items[furnaceSlotResult] = component.SlotData{Count: 0}
		}
		if takenCount > 0 {
			t.onTakeFurnaceResult(p, f, takenCount)
		}
		return
	}

	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	if ref.beSlot == furnaceSlotInput || ref.beSlot == furnaceSlotFuel {
		// input/fuel -> player inventory (moveItemStackTo(stack, 3, 39, false)).
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, false) {
			return
		}
		ref.set(work)
		return
	}

	// A PLAYER cell. AbstractFurnaceMenu.quickMoveStack's else branch has FOUR mutually-exclusive
	// sub-branches, in order (verified bytecode offsets 102-209):
	//   1. canSmelt(stack)     -> moveItemStackTo(input 0..1);  if it fails, RETURN EMPTY (terminal).
	//   2. else isFuel(stack)  -> moveItemStackTo(fuel 1..2);   if it fails, RETURN EMPTY (terminal).
	//   3. else index in main  -> moveItemStackTo(hotbar 30..39).
	//   4. else index in hotbar-> moveItemStackTo(main 3..30).
	// (1) and (2) are `else if`, so a smeltable item with a full input aborts — it must NOT fall through to
	// the fuel slot. canSmelt uses the recipe set for this furnace's subtype (findCookingRecipe); isFuel the
	// fuel table. The BE-slot moves (furnaceMoveIntoBESlot) mutate work in place; write back + return either
	// way (a partial/failed move still returns, per the bytecode's `if moved goto tail else return EMPTY`).
	if _, ok := findCookingRecipe(int32(work.ItemID), f.subtype); ok {
		if t.furnaceMoveIntoBESlot(f, furnaceSlotInput, &work) {
			ref.set(work)
		}
		return // canSmelt is terminal: move-or-abort, never try the fuel slot (offset 120 -> 123 areturn).
	}
	if furnaceIsFuel(int32(work.ItemID)) {
		if t.furnaceMoveIntoBESlot(f, furnaceSlotFuel, &work) {
			ref.set(work)
		}
		return // isFuel is terminal too.
	}
	// Non-smeltable, non-fuel: the main<->hotbar shuffle (offsets 152-209). Go window mapping: main storage
	// = slots 9..35 ([windowMainFirst, windowHotbarFirst)), hotbar = 36..44 ([windowHotbarFirst, 45)).
	//   main   -> hotbar (vanilla moveItemStackTo(stack, 30, 39, false))
	//   hotbar -> main   (vanilla moveItemStackTo(stack, 3, 30, false))
	if ref.invSlot >= windowMainFirst && ref.invSlot < windowHotbarFirst {
		// main storage -> hotbar
		if !t.moveItemStackTo(inv, &work, windowHotbarFirst, 45, false) {
			return
		}
	} else {
		// hotbar -> main storage
		if !t.moveItemStackTo(inv, &work, windowMainFirst, windowHotbarFirst, false) {
			return
		}
	}
	ref.set(work)
}

// furnaceMoveIntoBESlot merges *stack into a BE slot (input 0 / fuel 1), honoring furnaceMayPlace + max
// stack. Returns whether anything moved. Mirrors stonecutterMoveIntoInput over the chosen BE slot; going
// through ref.set for slot 0 preserves the setItem cook-reset.
func (t *TickLoop) furnaceMoveIntoBESlot(f *furnaceBE, beSlot int, stack *component.SlotData) bool {
	if stackEmpty(*stack) || !furnaceMayPlace(f, beSlot, *stack) {
		return false
	}
	ref := furnaceSlotRef{f: f, beSlot: beSlot, invSlot: -1, ok: true}
	existing := f.items[beSlot]
	if stackEmpty(existing) {
		slotMax := chestSlotMax(*stack)
		place := int(stack.Count)
		if slotMax < place {
			place = slotMax
		}
		ref.set(stackCopyWithCount(*stack, place))
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
			ref.set(stackCopyWithCount(existing, sum))
			stack.Count = 0
			return true
		}
		if int(existing.Count) < slotMax {
			stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
			ref.set(stackCopyWithCount(existing, slotMax))
			return true
		}
	}
	return false
}

// furnaceThrow ports the THROW branch: with an empty cursor, Q drops 1 (j==0) or the whole stack (j==1)
// from the slot under the cursor. The result slot drops the whole result + fires the XP award.
func (t *TickLoop) furnaceThrow(p *tickPlayer, oc *openContainer, f *furnaceBE, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := furnaceResolveSlot(f, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
		return
	}
	if ref.beSlot == furnaceSlotResult {
		f.items[furnaceSlotResult] = component.SlotData{Count: 0}
		t.playerDrop(p, cur, true)
		t.onTakeFurnaceResult(p, f, cur.Count)
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

// onTakeFurnaceResult ports FurnaceResultSlot.onTake -> checkTakeAchievements -> blockEntity.
// awardUsedRecipesAndPopExperience(serverPlayer): award the accumulated recipesUsed XP as orbs at the
// player, then CLEAR recipesUsed. The XP math is createExperience per recipe: xpReward = floor(amount*exp);
// with probability frac(amount*exp) add 1; ExperienceOrb.award. removeCount tracking (the vanilla partial
// take accumulation) collapses here into the taken count already applied by the caller — the award fires on
// each result take (vanilla awards on the take that empties the accumulated removeCount; v1 awards the full
// accumulated recipesUsed on any result take, then clears, which is the same total XP over the furnace's
// life). CITE FurnaceResultSlot.checkTakeAchievements + AbstractFurnaceBlockEntity.
// awardUsedRecipesAndPopExperience + createExperience.
func (t *TickLoop) onTakeFurnaceResult(p *tickPlayer, f *furnaceBE, _ pk.VarInt) {
	if len(f.recipesUsed) == 0 {
		return
	}
	// getRecipesToAwardAndPopExperience: for each (recipe, count) -> createExperience(level, pos, count, exp).
	for key, amount := range f.recipesUsed {
		exp := f.recipesXP[key]
		t.furnaceCreateExperience(p, amount, exp)
	}
	// awardUsedRecipesAndPopExperience: recipesUsed.clear().
	f.recipesUsed = nil
	f.recipesXP = nil
}

// furnaceCreateExperience ports AbstractFurnaceBlockEntity.createExperience(level, position, amount, value):
//
//	int xpReward = Mth.floor((float)amount * value);
//	float xpFraction = Mth.frac((float)amount * value);
//	if (xpFraction != 0.0f && level.getRandom().nextFloat() < xpFraction) ++xpReward;
//	ExperienceOrb.award(level, position, xpReward);
//
// The float32 casts + Mth.floor/Mth.frac are reproduced exactly (float32 arithmetic, then floor). The RNG
// draw is the level random (levelRandom); v1 awards the orbs at the collecting player (the vanilla orb
// spawns at the furnace pos and drifts to the player — v1 spawns at the player, an accepted orb-position
// simplification cited in death_mob.go awardExperienceOrbs). xpReward <= 0 spawns nothing (ExperienceOrb.
// award early-returns for value <= 0).
func (t *TickLoop) furnaceCreateExperience(p *tickPlayer, amount int, value float64) {
	fv := float32(amount) * float32(value)
	xpReward := mthFloorF32(fv)
	xpFraction := mthFracF32(fv)
	if xpFraction != 0.0 && t.furnaceLevelRandomFloat() < xpFraction {
		xpReward++
	}
	if xpReward <= 0 {
		return // ExperienceOrb.award(value<=0) is a no-op
	}
	t.awardExperienceOrbsAt(p.x, p.y, p.z, xpReward)
}

// mthFloorF32 ports net.minecraft.util.Mth.floor(float): (int)Math.floor(value) with the float32 input.
func mthFloorF32(f float32) int {
	i := int(f)
	if float32(i) > f {
		i--
	}
	return i
}

// mthFracF32 ports net.minecraft.util.Mth.frac(float): value - (float)floor(value).
func mthFracF32(f float32) float32 {
	return f - float32(mthFloorF32(f))
}

// closeFurnaceWindow ports AbstractFurnaceMenu.removed -> super.removed: the 3 slots ARE the block-entity
// container (already authoritative in the tick-owned furnaceBE — every click mutated it in place), so close
// just FREES the window (the caller clears p.openContainer). Unlike the crafting/stonecutter transient
// grids, NOTHING is returned to the player (the furnace items persist in the BE). The carried-item return is
// handled by the shared handleContainerClose path. CITE AbstractFurnaceMenu has no removed() override
// (super.removed clears data-slot listeners only) — the container survives.
func (t *TickLoop) closeFurnaceWindow(_ *tickPlayer, _ *openContainer) {
	// no-op: the furnaceBE persists in t.furnaces (the chest persist model, not the crafting return model).
}

// furnaceLevelRandomFloat draws level.getRandom().nextFloat() (float32 in [0,1)) — the RNG the fractional
// XP-orb roll uses in createExperience. Uses the current region's seeded levelRandom (Level.random analogue,
// the same source cat_goals + explosion use), falling back to 0 (never awards the fractional bonus) when no
// region random is available (a test without a region), which keeps XP deterministic and never panics.
func (t *TickLoop) furnaceLevelRandomFloat() float32 {
	if r := t.cur(); r != nil && r.levelRandom != nil {
		return r.levelRandom.NextFloat()
	}
	return 0
}

// awardExperienceOrbsAt is the position-based sibling of awardExperienceOrbs (death_mob.go): it ports
// ExperienceOrb.award(level, position, value) — split value into getExperienceValue chunks and spawn one
// ExperienceOrb per chunk at (x,y,z). The furnace result-take spawns the orbs here (the vanilla orb spawns
// at the furnace pos; v1 spawns at the collecting player pos, the same orb-position simplification cited in
// awardExperienceOrbs). Each orb is marked isOrb + carries its chunk value so playerTouchOrb awards it.
//
// 1:1 net.minecraft.world.entity.ExperienceOrb.award (the while(value>0) split loop + getExperienceValue).
func (t *TickLoop) awardExperienceOrbsAt(x, y, z float64, value int) {
	owner := t.cur()
	for value > 0 {
		chunk := getExperienceValue(value)
		value -= chunk
		orb := NewEntity(t.idAlloc.AllocID(), entity.ExperienceOrb, x, y, z)
		orb.isOrb = true
		orb.xpValue = chunk
		if owner != nil {
			owner.entities.add(orb)
		}
	}
}
