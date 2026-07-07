package server

// enchant_table_click.go — the ENCHANTMENT-TABLE click engine + clickMenuButton (the offer APPLY),
// ported 1:1 from net.minecraft.world.inventory.EnchantmentMenu (temp/cache/26.2-inner.jar, CFR this
// session). The click engine mirrors the other transient-menu click ports (crafting/stonecutter/
// beacon) over the enchant slot layout: item 0 (max stack 1), lapis 1 (mayPlace = LAPIS_LAZULI),
// player 2..37. clickMenuButton applies the chosen offer, consuming levels + lapis and reseeding.

import (
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// enchantSlotRef resolves an enchant-WINDOW slot index (0..37) to its backing.
type enchantSlotRef struct {
	oc      *openContainer
	inv     *Inventory
	slot    int   // 0 item / 1 lapis, -1 for a player cell
	invSlot int16 // window slot for a player cell, -1 otherwise
	ok      bool
}

func enchantResolveSlot(oc *openContainer, inv *Inventory, menuIdx int) enchantSlotRef {
	switch {
	case menuIdx == enchantSlotItem:
		return enchantSlotRef{oc: oc, inv: inv, slot: enchantSlotItem, invSlot: -1, ok: true}
	case menuIdx == enchantSlotLapis:
		return enchantSlotRef{oc: oc, inv: inv, slot: enchantSlotLapis, invSlot: -1, ok: true}
	case menuIdx >= 2 && menuIdx < 2+27:
		return enchantSlotRef{oc: oc, inv: inv, slot: -1, invSlot: int16(windowMainFirst + (menuIdx - 2)), ok: true}
	case menuIdx >= 2+27 && menuIdx < enchantMenuSize:
		return enchantSlotRef{oc: oc, inv: inv, slot: -1, invSlot: int16(windowHotbarFirst + (menuIdx - 2 - 27)), ok: true}
	}
	return enchantSlotRef{}
}

func (r enchantSlotRef) get() component.SlotData {
	switch r.slot {
	case enchantSlotItem:
		return r.oc.enchantItem
	case enchantSlotLapis:
		return r.oc.enchantLapis
	default:
		return r.inv.get(r.invSlot)
	}
}

func (r enchantSlotRef) set(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	switch r.slot {
	case enchantSlotItem:
		r.oc.enchantItem = s
	case enchantSlotLapis:
		r.oc.enchantLapis = s
	default:
		r.inv.set(r.invSlot, s)
	}
}

// enchantSlotMayPlace ports the per-slot mayPlace + getMaxStackSize: item slot 0 accepts anything
// (max stack 1 — the Slot override); lapis slot 1 accepts only LAPIS_LAZULI. CITE EnchantmentMenu
// ctor slot overrides.
func enchantSlotMayPlace(slot int, stack component.SlotData) bool {
	if slot == enchantSlotLapis {
		return int32(stack.ItemID) == itemNameToID("lapis_lazuli")
	}
	return true // item slot 0: base Slot.mayPlace true
}

// enchantSlotMaxStack ports getMaxStackSize: the item slot (0) is capped at 1; every other slot uses
// the item's own max. CITE EnchantmentMenu item-slot Slot.getMaxStackSize override (return 1).
func enchantSlotMaxStack(slot int, stack component.SlotData) int {
	if slot == enchantSlotItem {
		return 1
	}
	return chestSlotMax(stack)
}

// clickedEnchant ports AbstractContainerMenu.clicked for an open enchant window: snapshot the item +
// lapis + costs/clues + inventory + cursor, run the click under a panic-recover, rebuild the offers if
// the enchant slots changed (slotsChanged), then re-send the authoritative content + data + cursor.
func (t *TickLoop) clickedEnchant(p *tickPlayer, oc *openContainer, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	itemBefore := oc.enchantItem
	lapisBefore := oc.enchantLapis
	costsBefore := oc.enchantCosts
	clueBefore := oc.enchantClueEnch
	levelBefore := oc.enchantClueLevel
	seedBefore := oc.enchantSeed
	invBefore := inv.snapshot()
	carriedBefore := inv.getCarried()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				oc.enchantItem = itemBefore
				oc.enchantLapis = lapisBefore
				oc.enchantCosts = costsBefore
				oc.enchantClueEnch = clueBefore
				oc.enchantClueLevel = levelBefore
				oc.enchantSeed = seedBefore
				copy(inv.slots, invBefore)
				inv.setCarried(carriedBefore)
			}
		}()
		t.doEnchantClick(p, oc, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged: if either enchant slot changed, recompute the offers.
	if !slotDataEqual(itemBefore, oc.enchantItem) || !slotDataEqual(lapisBefore, oc.enchantLapis) {
		t.enchantSlotsChanged(oc)
	}

	t.sendEnchantContent(p, oc)
	t.sendEnchantData(p, oc)
	if p.client != nil && !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// doEnchantClick dispatches the supported click inputs over the enchant window.
func (t *TickLoop) doEnchantClick(p *tickPlayer, oc *openContainer, inv *Inventory, i, j, input int) {
	switch input {
	case containerInputPickup:
		t.enchantPickup(p, oc, inv, i, j)
	case containerInputQuickMove:
		t.enchantQuickMove(p, oc, inv, i)
	case containerInputThrow:
		t.enchantThrow(p, oc, inv, i, j)
	}
}

// enchantPickup ports the PICKUP branch, honoring the item slot's max-stack-1 + the lapis slot's
// mayPlace. Player cells behave like chest cells.
func (t *TickLoop) enchantPickup(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if (j != 0 && j != 1) || i < 0 {
		return
	}
	ref := enchantResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}
	carried := inv.getCarried()
	primary := j == 0
	slotItem := ref.get()

	if stackEmpty(slotItem) {
		if !stackEmpty(carried) && enchantSlotMayPlace(ref.slot, carried) {
			place := 1
			if primary {
				place = int(carried.Count)
			}
			c := carried
			ref.set(enchantSafeInsert(ref, &c, place))
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
		if !enchantSlotMayPlace(ref.slot, carried) {
			return
		}
		add := 1
		if primary {
			add = int(carried.Count)
		}
		c := carried
		ref.set(enchantSafeInsert(ref, &c, add))
		inv.setCarried(c)
	} else if enchantSlotMayPlace(ref.slot, carried) && int(carried.Count) <= enchantSlotMaxStack(ref.slot, carried) {
		inv.setCarried(slotItem)
		ref.set(carried)
	}
}

// enchantSafeInsert ports Slot.safeInsert over an enchantSlotRef, honoring the slot's max stack (1
// for the item slot).
func enchantSafeInsert(ref enchantSlotRef, stack *component.SlotData, increment int) component.SlotData {
	existing := ref.get()
	if stackEmpty(*stack) {
		return existing
	}
	slotMax := enchantSlotMaxStack(ref.slot, *stack)
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

// enchantQuickMove ports EnchantmentMenu.quickMoveStack: slot 0/1 -> player inventory (2..37); a
// lapis stack -> lapis slot; else if item slot empty && mayPlace -> item slot (1 unit); else no-op.
// CITE EnchantmentMenu.quickMoveStack.
func (t *TickLoop) enchantQuickMove(p *tickPlayer, oc *openContainer, inv *Inventory, i int) {
	if i < 0 {
		return
	}
	ref := enchantResolveSlot(oc, inv, i)
	if !ref.ok {
		return
	}

	if ref.slot == enchantSlotItem || ref.slot == enchantSlotLapis {
		src := ref.get()
		if stackEmpty(src) {
			return
		}
		work := src
		// moveItemStackTo(stack, 2, 38, true): the enchant slots move to the player inventory window.
		if !t.moveItemStackTo(inv, &work, windowMainFirst, 45, true) {
			return
		}
		ref.set(work)
		return
	}

	// A player cell.
	src := ref.get()
	if stackEmpty(src) {
		return
	}
	// if (stack.is(LAPIS_LAZULI)) moveItemStackTo(stack, 1, 2, true) [the lapis slot].
	if int32(src.ItemID) == itemNameToID("lapis_lazuli") {
		work := src
		if t.enchantMoveIntoLapis(oc, &work) {
			ref.set(work)
		}
		return
	}
	// else if (!item slot hasItem && item slot mayPlace) { one = stack.copyWithCount(1); stack.shrink(1);
	//     item slot.setByPlayer(one) }.
	if stackEmpty(oc.enchantItem) && enchantSlotMayPlace(enchantSlotItem, src) {
		one := stackCopyWithCount(src, 1)
		s := src
		s.Count = toVar(int(s.Count) - 1)
		if s.Count <= 0 {
			s = component.SlotData{Count: 0}
		}
		ref.set(s)
		oc.enchantItem = one
	}
}

// enchantMoveIntoLapis merges *stack into the lapis slot (up to the item max stack).
func (t *TickLoop) enchantMoveIntoLapis(oc *openContainer, stack *component.SlotData) bool {
	if stackEmpty(*stack) {
		return false
	}
	existing := oc.enchantLapis
	if stackEmpty(existing) {
		slotMax := chestSlotMax(*stack)
		place := min(int(stack.Count), slotMax)
		oc.enchantLapis = stackCopyWithCount(*stack, place)
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
			oc.enchantLapis = stackCopyWithCount(existing, sum)
			stack.Count = 0
			return true
		}
		if int(existing.Count) < slotMax {
			stack.Count = toVar(int(stack.Count) - (slotMax - int(existing.Count)))
			oc.enchantLapis = stackCopyWithCount(existing, slotMax)
			return true
		}
	}
	return false
}

// enchantThrow ports the THROW branch (drop key): drop 1 (primary) or the whole stack from the
// clicked slot.
func (t *TickLoop) enchantThrow(p *tickPlayer, oc *openContainer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) || i < 0 {
		return
	}
	ref := enchantResolveSlot(oc, inv, i)
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

// enchantClickButton ports EnchantmentMenu.clickMenuButton(player, buttonId): validate; require lapis
// >= id+1 (or creative); require costs[id]>0 && item non-empty && (level >= id+1 && level >= costs[id]
// || creative). Apply: getEnchantmentList; if !empty { onEnchantmentPerformed(item, id+1); (BOOK ->
// ENCHANTED_BOOK); enchant each; consume lapis; awardStat (no-op); reseed enchantmentSeed;
// slotsChanged }. Returns whether the button was consumed. CITE EnchantmentMenu.clickMenuButton.
func (t *TickLoop) enchantClickButton(p *tickPlayer, oc *openContainer, buttonID int) {
	if buttonID < 0 || buttonID >= 3 {
		return // invalid button id (logAndPause) -> return false
	}
	item := oc.enchantItem
	lapis := oc.enchantLapis
	enchantmentCost := buttonID + 1

	// (currency.isEmpty() || currency.count < enchantmentCost) && !creative -> false.
	if (stackEmpty(lapis) || int(lapis.Count) < enchantmentCost) && !playerHasInfiniteMaterials(p) {
		return
	}
	// costs[id]>0 && !item.isEmpty && (level >= id+1 && level >= costs[id] || creative).
	levelOK := (int(p.experienceLevel) >= enchantmentCost && int(p.experienceLevel) >= oc.enchantCosts[buttonID]) ||
		playerHasInfiniteMaterials(p)
	if !(oc.enchantCosts[buttonID] > 0 && !stackEmpty(item) && levelOK) {
		return
	}

	// access.execute: newEnchantment = getEnchantmentList(item, id, costs[id]); if (!empty) { apply }.
	// getEnchantmentList reseeds the menu's this.random to enchantmentSeed+id; clickMenuButton reads no
	// clue after, so a fresh random reseeded to the same value reproduces the identical selection stream.
	newEnchant := enchantGetList(newLegacyRandom(0), oc.enchantSeed, item, buttonID, oc.enchantCosts[buttonID])
	if len(newEnchant) == 0 {
		return
	}

	// player.onEnchantmentPerformed(item, enchantmentCost): consume levels + reseed the player seed.
	t.onEnchantmentPerformed(p, enchantmentCost)

	// if (item.is(BOOK)) item = transmuteCopy(ENCHANTED_BOOK) [a plain book becomes an enchanted book].
	if int32(item.ItemID) == itemNameToID("book") {
		item = enchantTransmuteToEnchantedBook(item)
	}

	// for (ench : newEnchantment) item.enchant(ench.enchantment, ench.level): merge onto the item.
	item = enchantApplyOffers(item, newEnchant)
	oc.enchantItem = item

	// currency.consume(enchantmentCost, player): shrink the lapis by enchantmentCost.
	lapis.Count = toVar(int(lapis.Count) - enchantmentCost)
	if lapis.Count <= 0 {
		lapis = component.SlotData{Count: 0}
	}
	oc.enchantLapis = lapis

	// awardStat + CriteriaTriggers.ENCHANTED_ITEM: cited no-ops.

	// enchantmentSeed.set(player.getEnchantmentSeed()) [the seed was re-rolled by onEnchantmentPerformed];
	// then slotsChanged recomputes the (now item-changed) offers.
	oc.enchantSeed = int32(getEnchantmentSeed(p))
	t.enchantSlotsChanged(oc)

	// broadcastChanges: re-send the authoritative window + data (the offers/costs updated, lapis/levels
	// consumed).
	t.sendEnchantContent(p, oc)
	t.sendEnchantData(p, oc)
	if p.client != nil {
		inv := ensureInventory(p)
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// enchantTransmuteToEnchantedBook ports ItemStack.transmuteCopy(ENCHANTED_BOOK): a copy with the
// enchanted_book item id (components carried over — the book has none relevant). The new stack must
// carry the STORED_ENCHANTMENTS component type; enchantApplyOffers writes it.
func enchantTransmuteToEnchantedBook(item component.SlotData) component.SlotData {
	out := item
	out.ItemID = pk.VarInt(itemNameToID("enchanted_book"))
	return out
}

// enchantApplyOffers merges the offered (enchantment, level) pairs onto the item's active enchantment
// component (ENCHANTMENTS, or STORED_ENCHANTMENTS for an enchanted book), ItemStack.enchant per offer.
// CITE ItemStack.enchant (EnchantmentHelper.updateEnchantments -> mutable.upgrade/set).
func enchantApplyOffers(item component.SlotData, offers []enchantInstance) component.SlotData {
	stored := enchantComponentTypeIsStored(item)
	current := map[string]int{}
	if stored {
		current = stackStoredEnchantments(item)
	} else {
		current = stackEnchantments(item)
	}
	for _, o := range offers {
		// ItemStack.enchant(holder, level) -> Mutable.upgrade: set to max(current, level).
		if cur, ok := current[o.id]; !ok || o.level > cur {
			current[o.id] = o.level
		}
	}
	edit := editStack(item)
	if stored {
		edit.setStoredEnchantments(current)
	} else {
		edit.setEnchantments(current)
	}
	return edit.materialize()
}
