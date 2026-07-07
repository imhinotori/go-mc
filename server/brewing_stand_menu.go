package server

// brewing_stand_menu.go — the BREWING STAND menu: the 5-slot (3 bottles / ingredient / fuel) container UI
// over the tick-owned brewingStandBE. A 1:1 port of net.minecraft.world.inventory.BrewingStandMenu (+ its
// PotionSlot / IngredientsSlot / FuelSlot inner slots) over the 26.2 jar (temp/cache/26.2-inner.jar,
// CFR this session). Opens on right-clicking a brewing_stand block (chest_open.go useBlockInteraction).
// The furnace menu twin (furnace_menu.go) — same shape (a BE-backed container, not a transient grid).
//
// 1:1 jar chain (VERIFIED CFR BrewingStandMenu):
//
//	BrewingStandBlock.useWithoutItem -> player.openMenu(the BrewingStandBlockEntity as MenuProvider).
//	BrewingStandMenu(id, inventory, container(5), data(2)):
//	    addSlot(new PotionSlot(container, 0, 56, 51));
//	    addSlot(new PotionSlot(container, 1, 79, 58));
//	    addSlot(new PotionSlot(container, 2, 102, 51));
//	    ingredientSlot = addSlot(new IngredientsSlot(potionBrewing, container, 3, 79, 17));
//	    addSlot(new FuelSlot(container, 4, 17, 17));
//	    addDataSlots(data);                       // 2 data slots: [0]=brewTime, [1]=fuel
//	    addStandardInventorySlots(inventory, 8, 84);   // INV 5..31, USE_ROW 32..40
//	PotionSlot.mayPlace(stack) = stack.is(POTION|SPLASH_POTION|LINGERING_POTION|GLASS_BOTTLE); getMaxStackSize=1.
//	IngredientsSlot.mayPlace(stack) = potionBrewing.isIngredient(stack).
//	FuelSlot.mayPlace(stack) = stack.is(ItemTags.BREWING_FUEL).
//	quickMoveStack(player, i) — the exact moveItemStackTo target chain (ported in brewQuickMove).
//	getBrewingTicks() = data.get(0) = brewTime; getFuel() = data.get(1) = fuel.
//
// v1 subset (cited): PotionSlot.onTake fires the BREWED_POTION advancement criterion — no v1 advancement
// system, so the take-trigger is a faithful no-op (the same subset the furnace's stat/sound calls cite).
// The 5 slots are the REAL block-entity items (NOT a transient copy) — a click mutates the same
// brewingStandBE.items the brew drive ticks (BE-backed, like the furnace/chest, unlike the crafting grid).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// brewMenuSize is the brewing-stand-window slot count: 5 (3 bottles + ingredient + fuel) + 27 main + 9
// hotbar = 41. (BrewingStandMenu: SLOT 0..4, INV 5..31, USE_ROW 32..40.) VERIFIED ctor.
const brewMenuSize = brewContainerSize + 27 + 9 // 41

// Brewing-stand data-slot ids (BrewingStandMenu data(2), VERIFIED CFR getBrewingTicks/getFuel):
const (
	brewDataBrewTime = 0 // data.get(0) = brewTime
	brewDataFuel     = 1 // data.get(1) = fuel
	brewDataValues   = 2 // DATA_COUNT
)

// resolveBrewingStand returns the tick-owned brewingStandBE for pos, creating an EMPTY one on first access
// (a freshly-placed brewing stand's default BrewingStandBlockEntity). Returns nil only when pos is not a
// brewing-stand block (or the world is unloaded). Tick-owned (t.brewingStands, the furnace twin).
func (t *TickLoop) resolveBrewingStand(pos pk.Position, state block.StateID) *brewingStandBE {
	if t.brewingStands == nil {
		t.brewingStands = make(map[pk.Position]*brewingStandBE)
	}
	if b, ok := t.brewingStands[pos]; ok {
		return b
	}
	if !isBrewingStandBlock(state) {
		return nil
	}
	// Restore persisted state from the chunk's BlockEntity list first (loadBrewingStandBE); a stand with no
	// recorded BE (freshly placed) synthesizes an EMPTY one — the default BrewingStandBlockEntity ctor state.
	b := t.loadBrewingStandBE(pos)
	if b == nil {
		b = &brewingStandBE{}
	}
	t.brewingStands[pos] = b
	return b
}

// openBrewingStand ports BrewingStandBlock.useWithoutItem -> ServerPlayer.openMenu for a brewing_stand at
// pos: resolve (or create) the brewingStandBE, close any prior window, allocate a windowId, send
// ClientboundOpenScreen(minecraft:brewing_stand) + the initial ContainerSetContent + the 2 data slots, and
// record the open-container state. Returns true (the action was consumed) once the menu is sent.
func (t *TickLoop) openBrewingStand(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil || t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isBrewingStandBlock(state) {
		return false
	}
	b := t.resolveBrewingStand(pos, state)
	if b == nil {
		return false
	}

	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}
	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindBrewingStand, brewingStandPos: pos}

	menuID := menuTypeID(registryid.Menu, "minecraft:brewing_stand")
	p.client.Send(openScreen(int32(win), menuID, "Brewing Stand"))

	t.sendBrewingStandContent(p, b)
	t.sendBrewingStandData(p, b)
	return true
}

// brewMenuItems builds the 41-slot ContainerSetContent list: bottle 0/1/2, ingredient 3, fuel 4, then the
// player inventory — main 5..31 (window slots 9..35) then hotbar 32..40 (window slots 36..44).
func brewMenuItems(b *brewingStandBE, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, brewMenuSize)
	out[0] = b.items[brewSlotBottle0]
	out[1] = b.items[brewSlotBottle1]
	out[2] = b.items[brewSlotBottle2]
	out[3] = b.items[brewSlotIngredient]
	out[4] = b.items[brewSlotFuel]
	for i := 0; i < 27; i++ {
		out[brewContainerSize+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[brewContainerSize+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendBrewingStandContent pushes the authoritative ContainerSetContent for the open brewing-stand window.
func (t *TickLoop) sendBrewingStandContent(p *tickPlayer, b *brewingStandBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindBrewingStand {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		brewMenuItems(b, inv), inv.getCarried()))
}

// sendBrewingStandData pushes the 2 data slots (brewTime, fuel) — the ContainerData the client's arrow +
// bubbles render from (BrewingStandMenu.addDataSlots + broadcastChanges' data-slot diff).
func (t *TickLoop) sendBrewingStandData(p *tickPlayer, b *brewingStandBE) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindBrewingStand {
		return
	}
	win := int32(p.openContainer.windowID)
	p.client.Send(containerSetData(win, brewDataBrewTime, int16(b.brewTime)))
	p.client.Send(containerSetData(win, brewDataFuel, int16(b.fuel)))
}

// broadcastBrewingStandChange re-sends the authoritative content + data to any player whose open window is
// the brewing stand at pos (the serverTick `if (changed) setChanged` observable equivalent). Tick-owned.
func (t *TickLoop) broadcastBrewingStandChange(pos pk.Position, b *brewingStandBE) {
	for _, p := range t.players {
		if p == nil || p.openContainer == nil {
			continue
		}
		if p.openContainer.kind == containerKindBrewingStand && p.openContainer.brewingStandPos == pos {
			t.sendBrewingStandContent(p, b)
			t.sendBrewingStandData(p, b)
		}
	}
}

// brewSlotRef resolves a brewing-stand-WINDOW slot index (0..40) to its backing brewingStandBE item slot or
// the player inventory window slot.
//
//	0/1/2 -> bottles (b.items[0/1/2])
//	3     -> ingredient (b.items[3])
//	4     -> fuel (b.items[4])
//	5..40 -> player inventory (main 9..35, hotbar 36..44)
type brewSlotRef struct {
	b       *brewingStandBE
	inv     *Inventory
	beSlot  int   // 0..4 for a BE slot, -1 for a player slot
	invSlot int16 // window slot for a player cell, -1 for a BE slot
	ok      bool
}

func brewResolveSlot(b *brewingStandBE, inv *Inventory, menuIdx int) brewSlotRef {
	switch {
	case menuIdx >= 0 && menuIdx < brewContainerSize:
		return brewSlotRef{b: b, inv: inv, beSlot: menuIdx, invSlot: -1, ok: true}
	case menuIdx >= brewContainerSize && menuIdx < brewContainerSize+27:
		return brewSlotRef{b: b, inv: inv, beSlot: -1, invSlot: int16(windowMainFirst + (menuIdx - brewContainerSize)), ok: true}
	case menuIdx >= brewContainerSize+27 && menuIdx < brewMenuSize:
		return brewSlotRef{b: b, inv: inv, beSlot: -1, invSlot: int16(windowHotbarFirst + (menuIdx - brewContainerSize - 27)), ok: true}
	}
	return brewSlotRef{}
}

func (r brewSlotRef) get() component.SlotData {
	if r.beSlot >= 0 {
		return r.b.items[r.beSlot]
	}
	return r.inv.get(r.invSlot)
}

func (r brewSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	if r.beSlot >= 0 {
		r.b.items[r.beSlot] = s
		return
	}
	r.inv.set(r.invSlot, s)
}

// brewSlotMax ports Slot.getMaxStackSize() per brewing-stand slot: the 3 PotionSlots cap at 1
// (PotionSlot.getMaxStackSize == 1); the ingredient/fuel/player slots use the container default (min with
// the item's own max — chestSlotMax).
func brewSlotMax(beSlot int, stack component.SlotData) int {
	if beSlot == brewSlotBottle0 || beSlot == brewSlotBottle1 || beSlot == brewSlotBottle2 {
		return 1 // PotionSlot.getMaxStackSize
	}
	return chestSlotMax(stack)
}

// brewMayPlace ports the per-slot mayPlace: PotionSlot (0/1/2) accepts potion/splash/lingering/glass_bottle;
// IngredientsSlot (3) accepts any brewing ingredient (potionBrewing.isIngredient); FuelSlot (4) accepts a
// BREWING_FUEL item; player cells accept anything.
func brewMayPlace(beSlot int, stack component.SlotData) bool {
	switch beSlot {
	case brewSlotBottle0, brewSlotBottle1, brewSlotBottle2:
		id := int32(stack.ItemID)
		return id == itemNameToID("potion") || id == itemNameToID("splash_potion") ||
			id == itemNameToID("lingering_potion") || id == itemNameToID("glass_bottle")
	case brewSlotIngredient:
		return vanillaPotionBrewing().isIngredient(int32(stack.ItemID))
	case brewSlotFuel:
		return itemInTag(int32(stack.ItemID), "brewing_fuel")
	default:
		return true // player cells
	}
}

// clickedBrewingStand ports AbstractContainerMenu.clicked for an open brewing-stand window: snapshot the BE
// items + inventory + cursor, run the click under a panic-recover (no partial mutation on a throw), then
// re-send the authoritative content + cursor. PICKUP/QUICK_MOVE/THROW are supported. The furnace twin.
func (t *TickLoop) clickedBrewingStand(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	b := t.brewingStands[oc.brewingStandPos]
	if b == nil {
		t.sendContent(p)
		return
	}
	inv := ensureInventory(p)

	itemsBefore := b.items
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				b.items = itemsBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doBrewingStandClick(p, oc, b, inv, int(slotNum), button, int(input))
	}()

	t.markBrewingStandDirty(oc.brewingStandPos)

	t.sendBrewingStandContent(p, b)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doBrewingStandClick dispatches the supported click inputs over the brewing-stand window.
func (t *TickLoop) doBrewingStandClick(p *tickPlayer, oc *openContainer, b *brewingStandBE, inv *Inventory, i, j, input int) {
	if t.menuOutsideDrop(p, inv, i, j, input) {
		return
	}
	switch input {
	case containerInputPickup:
		t.brewPickup(p, oc, b, inv, i, j)
	case containerInputQuickMove:
		t.brewQuickMove(p, oc, b, inv, i)
	case containerInputThrow:
		t.brewThrow(p, oc, b, inv, i, j)
	}
}

// brewPickup ports the PICKUP branch: left-click (j==0) whole stack, right-click (j==1) half/one, honoring
// brewMayPlace + the per-slot max stack (PotionSlots cap at 1). Mirrors furnacePickup for the non-result
// slots (a brewing stand has no take-only result slot).
func (t *TickLoop) brewPickup(p *tickPlayer, oc *openContainer, b *brewingStandBE, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := brewResolveSlot(b, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()
	primary := j == 0
	slotItem := ref.get()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) && ref.beSlot != -1 && !brewMayPlace(ref.beSlot, carried) {
			return
		}
		if !stackEmpty(carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(brewSafeInsert(ref, &c, place))
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
		if ref.beSlot != -1 && !brewMayPlace(ref.beSlot, carried) {
			return
		}
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(brewSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if (ref.beSlot == -1 || brewMayPlace(ref.beSlot, carried)) && int(carried.Count) <= brewSlotMaxRef(ref, carried) {
		// swap cursor <-> slot (only when the slot accepts the cursor item and the count fits the slot max).
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// brewSlotMaxRef is brewSlotMax over a brewSlotRef (a player cell uses the item's own max).
func brewSlotMaxRef(ref brewSlotRef, stack component.SlotData) int {
	if ref.beSlot >= 0 {
		return brewSlotMax(ref.beSlot, stack)
	}
	return chestSlotMax(stack)
}

// brewSafeInsert ports Slot.safeInsert over a brewSlotRef, honoring the slot's max stack (PotionSlots cap at
// 1). Mirrors furnaceSafeInsert.
func brewSafeInsert(ref brewSlotRef, stack *component.SlotData, increment int) component.SlotData {
	existing := ref.get()
	if stackEmpty(*stack) {
		return existing
	}
	slotMax := brewSlotMaxRef(ref, *stack)
	add := min(increment, int(stack.Count))
	if room := slotMax - int(existing.Count); room < add {
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

// brewQuickMove ports BrewingStandMenu.quickMoveStack (VERIFIED CFR): the BE slots (0..4) move into the
// player inventory (5..40, reverse); a player item routes to fuel (4) if a fuel, else ingredient (3) if an
// ingredient, else a potion (0..2) if a potion, else inv<->hotbar. The exact moveItemStackTo chain.
func (t *TickLoop) brewQuickMove(p *tickPlayer, oc *openContainer, b *brewingStandBE, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := brewResolveSlot(b, inv, i)
	if !ref.ok {
		return
	}
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	work := src

	// slotIndex 0..4 (BE slots): moveItemStackTo(stack, 5, 41, true) -> player inventory (window 9..44), reverse.
	if ref.beSlot >= 0 {
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
		ref.set(work)
		return
	}

	// A PLAYER cell. FuelSlot.mayPlaceItem(clicked) ? try fuel (4) then ingredient (3) : ingredient? potion?
	clicked := src
	isFuel := itemInTag(int32(clicked.ItemID), "brewing_fuel")
	isIngr := vanillaPotionBrewing().isIngredient(int32(clicked.ItemID))
	isPotion := brewMayPlace(brewSlotBottle0, clicked)

	if isFuel {
		if t.brewMoveIntoBESlot(b, brewSlotFuel, &work) {
			ref.set(work)
			return
		}
		if isIngr {
			if t.brewMoveIntoBESlot(b, brewSlotIngredient, &work) {
				ref.set(work)
				return
			}
		}
	} else if isIngr {
		if t.brewMoveIntoBESlot(b, brewSlotIngredient, &work) {
			ref.set(work)
			return
		}
	} else if isPotion {
		// moveItemStackTo(stack, 0, 3, false): fill the three bottle slots (each caps at 1).
		if t.brewMoveIntoBottles(b, &work) {
			ref.set(work)
			return
		}
	} else {
		// inv<->hotbar shuffle is a client-visible convenience; v1 leaves the item in place (no-op).
	}
}

// brewMoveIntoBESlot merges *stack into a single BE slot (ingredient 3 / fuel 4), honoring brewMayPlace +
// max stack. Returns whether anything moved. Mirrors furnaceMoveIntoBESlot.
func (t *TickLoop) brewMoveIntoBESlot(b *brewingStandBE, beSlot int, stack *component.SlotData) bool {
	if stackEmpty(*stack) || !brewMayPlace(beSlot, *stack) {
		return false
	}
	ref := brewSlotRef{b: b, beSlot: beSlot, invSlot: -1, ok: true}
	existing := b.items[beSlot]
	if stackEmpty(existing) {
		slotMax := brewSlotMax(beSlot, *stack)
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
		slotMax := brewSlotMax(beSlot, existing)
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

// brewMoveIntoBottles ports moveItemStackTo(stack, 0, 3, false) for a potion item: fill the three bottle
// slots in order, each capped at 1. Because the PotionSlots hold at most 1 and potions do not stack (max 1),
// each bottle takes at most one item. Returns whether anything moved.
func (t *TickLoop) brewMoveIntoBottles(b *brewingStandBE, stack *component.SlotData) bool {
	moved := false
	for i := brewSlotBottle0; i <= brewSlotBottle2; i++ {
		if stackEmpty(*stack) {
			break
		}
		if !brewMayPlace(i, *stack) {
			continue
		}
		ref := brewSlotRef{b: b, beSlot: i, invSlot: -1, ok: true}
		existing := b.items[i]
		if stackEmpty(existing) {
			ref.set(stackCopyWithCount(*stack, 1)) // PotionSlot cap 1
			stack.Count = toVar(int(stack.Count) - 1)
			if stack.Count <= 0 {
				stack.Count = 0
			}
			moved = true
		}
		// an occupied bottle (cap 1) has no room; skip (potions never same-item-merge past 1).
	}
	return moved
}

// brewThrow ports the THROW branch: with an empty cursor, Q drops 1 (j==0) or the whole stack (j==1) from
// the slot under the cursor. Mirrors furnaceThrow (a brewing stand has no take-only result slot).
func (t *TickLoop) brewThrow(p *tickPlayer, oc *openContainer, b *brewingStandBE, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := brewResolveSlot(b, inv, i)
	if !ref.ok {
		return
	}
	cur := ref.get()
	if stackEmpty(cur) {
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

// closeBrewingStandWindow ports BrewingStandMenu.removed -> super.removed: the 5 slots ARE the block-entity
// container (authoritative in the tick-owned brewingStandBE), so close just FREES the window. Nothing is
// returned to the player (the items persist in the BE). The furnace twin (closeFurnaceWindow).
func (t *TickLoop) closeBrewingStandWindow(_ *tickPlayer, _ *openContainer) {}
