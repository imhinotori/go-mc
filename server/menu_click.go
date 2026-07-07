package server

// menu_click.go — the GENERIC SWAP / CLONE / PICKUP_ALL click branches of
// net.minecraft.world.inventory.AbstractContainerMenu.doClick, factored out so EVERY open non-player
// window (chest, furnace, crafting, hopper, dispenser, anvil, beacon, brewing stand, stonecutter,
// enchant, grindstone, smithing, merchant, minecart) gets them, not just the player window
// (inventory_doclick.go). Before this file only PICKUP / QUICK_MOVE / THROW (and, for the chest,
// QUICK_CRAFT / PICKUP_ALL) were dispatched on non-player menus; a number-key hotbar swap, a creative
// middle-click clone, or a double-click gather over a chest/furnace/etc. was silently dropped by the
// server, so the client's optimistic prediction diverged and the next authoritative resend rolled it
// back (the "clicks get cancelled" symptom).
//
// doClick is GENERIC in vanilla — one method over `slots` (NonNullList<Slot>) plus the player's
// Inventory. Each menu here already resolves a WINDOW index (0..menuSize-1) to a concrete backing cell
// through its own *SlotRef type; menuView/menuSlotView wrap that resolver behind the small set of Slot
// primitives the three branches touch, so the branch logic is written ONCE, 1:1 with the bytecode
// (doClick offsets: SWAP 1079-1382, CLONE 1385-1446, PICKUP_ALL 1611-1873), and re-expressed over the
// per-menu backing. The player window keeps its dedicated menuSlot engine; this covers the rest.
//
// The SWAP button j indexes the PLAYER INVENTORY (hotbar 0-8, offhand 40) via inv.invGetItem/invSetItem
// — that is menu-independent (it is player.getInventory(), which in Sulfur is the same *Inventory the
// menu's player cells alias). CLONE gates on player.hasInfiniteMaterials() (creative). PICKUP_ALL
// sweeps every window slot in the vanilla two-pass order honoring mayPickup + canItemQuickReplace +
// canTakeItemForPickAll + the stack-size guards.

import (
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// menuSlotView is the small subset of net.minecraft.world.inventory.Slot the generic SWAP/CLONE/
// PICKUP_ALL branches touch, over an arbitrary open window's backing. A per-menu adapter builds it from
// that menu's *SlotRef. ok reports whether the window index resolved to a real slot (a forged index →
// ok=false → the branch treats it as absent, never panics).
type menuSlotView struct {
	ok            bool
	getItem       func() component.SlotData
	setByPlayer   func(component.SlotData)
	mayPickupFn   func(*tickPlayer) bool
	mayPlaceFn    func(component.SlotData) bool
	maxStackFn    func(component.SlotData) int
	onSwapCraftFn func(int)
	onTakeFn      func(*tickPlayer, component.SlotData)
}

func (s menuSlotView) hasItem() bool                      { return s.ok && !stackEmpty(s.getItem()) }
func (s menuSlotView) mayPickup(p *tickPlayer) bool       { return s.ok && s.mayPickupFn(p) }
func (s menuSlotView) mayPlace(v component.SlotData) bool { return s.ok && s.mayPlaceFn(v) }
func (s menuSlotView) getMaxStackSize(v component.SlotData) int {
	if !s.ok {
		return 0
	}
	return s.maxStackFn(v)
}
func (s menuSlotView) onSwapCraft(n int) {
	if s.ok && s.onSwapCraftFn != nil {
		s.onSwapCraftFn(n)
	}
}
func (s menuSlotView) onTake(p *tickPlayer, v component.SlotData) {
	if s.ok && s.onTakeFn != nil {
		s.onTakeFn(p, v)
	}
}

// safeClone ports Slot.safeClone(player): a copy of the slot item with count = its max stack size (the
// creative middle-click clone). CITE net.minecraft.world.inventory.Slot.safeClone.
func (s menuSlotView) safeClone() component.SlotData {
	item := s.getItem()
	return stackCopyWithCount(item, stackMaxSize(item))
}

// tryRemove ports Slot.tryRemove(count, limit, player) over a menuSlotView: remove min(count,limit)
// (bounded by presence) WITHOUT firing onTake, honoring mayPickup + the allowModification limit clamp
// (allowModification = mayPickup && mayPlace(getItem)). Returns the removed stack + ok. Verified
// Slot.tryRemove bytecode.
func (s menuSlotView) tryRemove(count, limit int, p *tickPlayer) (component.SlotData, bool) {
	if !s.mayPickup(p) {
		return component.SlotData{Count: 0}, false
	}
	cur := s.getItem()
	allowMod := s.mayPickup(p) && s.mayPlace(cur)
	if !allowMod && limit < int(cur.Count) {
		return component.SlotData{Count: 0}, false
	}
	count = min(count, limit)
	if stackEmpty(cur) || count <= 0 {
		return component.SlotData{Count: 0}, false
	}
	removed := stackSplit(&cur, count)
	s.setByPlayer(cur)
	if stackEmpty(removed) {
		return component.SlotData{Count: 0}, false
	}
	return removed, true
}

// safeTake ports Slot.safeTake(count, limit, player): tryRemove, fire onTake on success, return the
// removed stack. CITE Slot.safeTake.
func (s menuSlotView) safeTake(count, limit int, p *tickPlayer) component.SlotData {
	taken, ok := s.tryRemove(count, limit, p)
	if ok {
		s.onTake(p, taken)
	}
	return taken
}

// menuCanItemQuickReplace ports AbstractContainerMenu.canItemQuickReplace(slot, stack, stackSizeMatters)
// over a menuSlotView (same body as canItemQuickReplace for menuSlot). Used by PICKUP_ALL's per-slot
// gather guard.
func menuCanItemQuickReplace(s menuSlotView, stack component.SlotData, stackSizeMatters bool) bool {
	if !s.hasItem() {
		return true
	}
	if stackSameItemSameComponents(stack, s.getItem()) {
		add := 0
		if !stackSizeMatters {
			add = int(stack.Count)
		}
		return int(s.getItem().Count)+add <= stackMaxSize(stack)
	}
	return false
}

// menuView is a whole open window: its slot count, a resolver from window index to menuSlotView, the
// player inventory (for SWAP's hotbar/offhand indexing), and canTakeItemForPickAll (the PICKUP_ALL
// per-slot filter). A per-menu adapter supplies all four. canTakeItemForPickAll defaults true (the base
// AbstractContainerMenu.canTakeItemForPickAll) — only InventoryMenu's result slot overrides it, and no
// non-player window here has a result slot that participates in a double-click gather.
type menuView struct {
	size                  int
	inv                   *Inventory
	slotAt                func(i int) menuSlotView
	canTakeItemForPickAll func(stack component.SlotData, i int) bool
}

// menuDoSwap ports the SWAP branch of doClick (offsets 1079-1382) over an arbitrary window. j indexes
// the PLAYER INVENTORY (hotbar 0-8 or offhand 40); i is the window slot the swap targets. Verified
// bytecode.
func (t *TickLoop) menuDoSwap(p *tickPlayer, v menuView, i, j int) {
	inv := v.inv
	// @1086: if (!((j>=0 && j<9) || j==40)) return.
	if !((j >= 0 && j < 9) || j == 40) {
		return
	}
	if i < 0 || i >= v.size {
		return
	}
	hotbar := inv.invGetItem(j) // inventory.getItem(j)
	s := v.slotAt(i)
	if !s.ok {
		return
	}
	slotItem := s.getItem()

	switch {
	case stackEmpty(hotbar) && stackEmpty(slotItem):
		// both empty: nothing (@1130-1146).

	case stackEmpty(hotbar):
		// hotbar empty & slot non-empty: move slot to hotbar, clear the slot (@1149-1202).
		if !s.mayPickup(p) {
			return
		}
		inv.invSetItem(j, slotItem)
		s.onSwapCraft(int(slotItem.Count))
		s.setByPlayer(component.SlotData{Count: 0})
		s.onTake(p, slotItem)

	case stackEmpty(slotItem):
		// slot empty & hotbar non-empty: move hotbar to slot (split if over the slot's max) (@1205-1273).
		if !s.mayPlace(hotbar) {
			return
		}
		slotMax := s.getMaxStackSize(hotbar)
		if int(hotbar.Count) > slotMax {
			h := hotbar
			s.setByPlayer(stackSplit(&h, slotMax)) // split slotMax INTO the slot; hotbar keeps the remainder
			inv.invSetItem(j, h)
		} else {
			inv.invSetItem(j, component.SlotData{Count: 0})
			s.setByPlayer(hotbar)
		}

	default:
		// both non-empty: swap, split-if-over-max with the inventory.add/drop fallback (@1276-1379).
		if !s.mayPickup(p) || !s.mayPlace(hotbar) {
			return
		}
		slotMax := s.getMaxStackSize(hotbar)
		if int(hotbar.Count) > slotMax {
			h := hotbar
			s.setByPlayer(stackSplit(&h, slotMax))
			inv.invSetItem(j, h)
			s.onTake(p, slotItem)
			si := slotItem
			if !t.invAdd(inv, &si) {
				t.playerDrop(p, si, true)
			}
		} else {
			inv.invSetItem(j, slotItem)
			s.setByPlayer(hotbar)
			s.onTake(p, slotItem)
		}
	}
}

// menuDoClone ports the CLONE branch of doClick (offsets 1385-1446) over an arbitrary window: a
// creative-only middle-click clones the slot's item to a max-size stack onto an empty cursor. Verified
// bytecode.
func (t *TickLoop) menuDoClone(p *tickPlayer, v menuView, i int) {
	inv := v.inv
	if p.gameMode != gameModeCreative { // player.hasInfiniteMaterials()
		return
	}
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 || i >= v.size {
		return
	}
	s := v.slotAt(i)
	if s.hasItem() {
		inv.setCarried(s.safeClone())
	}
}

// menuDoPickupAll ports the PICKUP_ALL branch of doClick (offsets 1611-1873) over an arbitrary window:
// a double-click gathers every matching same-item slot in the WHOLE window onto the cursor, up to its
// max, in two passes (pass 0 skips already-full source stacks, pass 1 includes them). j==0 sweeps
// forward from slot 0; j==1 sweeps backward from the last slot. Verified bytecode.
func (t *TickLoop) menuDoPickupAll(p *tickPlayer, v menuView, i, j int) {
	inv := v.inv
	if i < 0 || i >= v.size {
		return
	}
	s := v.slotAt(i)
	carried := inv.getCarried()
	if stackEmpty(carried) {
		return
	}
	// @1649: if (slot.hasItem() && slot.mayPickup(player)) return — a takeable matching slot was already
	// handled as a normal PICKUP.
	if s.hasItem() && s.mayPickup(p) {
		return
	}

	start := 0
	dir := 1
	if j != 0 {
		start = v.size - 1
		dir = -1
	}

	for pass := 0; pass < 2; pass++ {
		k := start
		for k >= 0 && k < v.size && int(inv.getCarried().Count) < stackMaxSize(inv.getCarried()) {
			c := v.slotAt(k)
			cur := inv.getCarried()
			if c.hasItem() && menuCanItemQuickReplace(c, cur, true) && c.mayPickup(p) &&
				v.canTakeItemForPickAll(cur, k) {
				it := c.getItem()
				// pass 0 skips already-full stacks (@1801-1819: pass != 0 || count != maxStackSize).
				if pass != 0 || int(it.Count) != stackMaxSize(it) {
					taken := c.safeTake(int(it.Count), stackMaxSize(cur)-int(cur.Count), p)
					cc := inv.getCarried()
					cc.Count += pk.VarInt(taken.Count) // carried.grow(taken.getCount())
					inv.setCarried(cc)
				}
			}
			k += dir
		}
	}
}
