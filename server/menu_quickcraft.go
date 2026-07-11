package server

// menu_quickcraft.go -- the GENERIC QUICK_CRAFT (mouse-drag) branch of
// net.minecraft.world.inventory.AbstractContainerMenu.doClick, factored out over menuView so EVERY open
// non-player window reaches the drag state machine -- not just the player inventory (inventory_doclick.go
// doClickQuickCraft) and the chest (chest_click.go doChestQuickCraft). Before this, anvil / furnace /
// brewing / hopper / dispenser / beacon / grindstone / smithing / stonecutter / loom / merchant /
// crafting / shulker / ender-chest / minecart all silently DROPPED a drag: the client optimistic
// prediction diverged and the next authoritative resend rolled it back.
//
// The drag logic is MENU-AGNOSTIC in vanilla -- doClick QUICK_CRAFT branch reads only slot.mayPlace,
// canItemQuickReplace, getMaxStackSize, and canDragTo, then distributes the carried stack even (type 0),
// single (type 1), or clone (type 2, creative) across the dragged set. It is written ONCE here over
// menuView (which every menu already builds for SWAP/CLONE/PICKUP_ALL), 1:1 with the bytecode (doClick
// offsets 14-544), re-expressed over the per-menu backing. The drag STATE
// (quickcraftStatus/quickcraftType/quickcraftSlots) is the player inventory -- AbstractContainerMenu owns
// ONE drag at a time and the open window IS that one menu.

import (
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// menuDoQuickCraft ports the QUICK_CRAFT drag state machine (doClick offsets 14-544) over an arbitrary
// open window. i is the window slot the current sub-event targets; j is the drag event byte (header =
// j&3 is the phase START(0)/ADD(1)/END(2); type = (j>>2)&3). A single-slot drag END reduces to a PICKUP
// on that slot (doClick(idx, quickcraftType, PICKUP) in vanilla) via menuDoSingleSlotPickup. A result
// slot has mayPlace==false, so the ADD phase never admits it -- the single-slot fallback only ever
// targets a placeable slot, matching the vanilla PICKUP a single-slot drag reduces to. CITE
// AbstractContainerMenu.doClick QUICK_CRAFT branch.
func (t *TickLoop) menuDoQuickCraft(p *tickPlayer, v menuView, i, j int) {
	inv := v.inv
	header := getQuickcraftHeader(j) // j & 3
	prevStatus := inv.quickcraftStatus
	inv.quickcraftStatus = header

	// if (!((prev==1 && status==2) || (prev==status))) { resetQuickCraft(); return; }
	if !((prevStatus == 1 && inv.quickcraftStatus == 2) || (prevStatus == inv.quickcraftStatus)) {
		inv.resetQuickCraft()
		return
	}
	if stackEmpty(inv.getCarried()) {
		inv.resetQuickCraft()
		return
	}

	switch inv.quickcraftStatus {
	case 0: // START
		inv.quickcraftType = getQuickcraftType(j) // (j>>2)&3
		if isValidQuickcraftType(inv.quickcraftType, p) {
			inv.quickcraftStatus = 1
			inv.quickcraftSlots = inv.quickcraftSlots[:0]
		} else {
			inv.resetQuickCraft()
		}
		return

	case 1: // ADD slot i to the drag set
		if i < 0 || i >= v.size {
			return
		}
		s := v.slotAt(i)
		if !s.ok {
			return
		}
		carried := inv.getCarried()
		// canItemQuickReplace(slot, carried, true) && slot.mayPlace(carried) && (type==2 ||
		// carried.count > dragSlots.size()) && canDragTo(slot). canDragTo is the base true.
		if menuCanItemQuickReplace(s, carried, true) && s.mayPlace(carried) &&
			(inv.quickcraftType == 2 || int(carried.Count) > len(inv.quickcraftSlots)) {
			inv.addQuickcraftSlot(i)
		}
		return

	case 2: // END -- distribute the carried stack across the drag set
		if len(inv.quickcraftSlots) != 0 {
			if len(inv.quickcraftSlots) == 1 {
				idx := inv.quickcraftSlots[0]
				inv.resetQuickCraft()
				t.menuDoSingleSlotPickup(p, v, idx, inv.quickcraftType)
				return
			}
			carriedCopy := inv.getCarried() // getCarried().copy()
			if stackEmpty(carriedCopy) {
				inv.resetQuickCraft()
				return
			}
			remaining := int(inv.getCarried().Count)
			setSize := len(inv.quickcraftSlots)
			for _, idx := range inv.quickcraftSlots {
				if idx < 0 || idx >= v.size {
					continue
				}
				s := v.slotAt(idx)
				if !s.ok {
					continue
				}
				carried := inv.getCarried()
				if menuCanItemQuickReplace(s, carried, true) && s.mayPlace(carried) &&
					(inv.quickcraftType == 2 || int(carried.Count) >= setSize) {
					existing := 0
					if s.hasItem() {
						existing = int(s.getItem().Count)
					}
					slotMax := stackMaxSize(carriedCopy)
					if sm := s.getMaxStackSize(carriedCopy); sm < slotMax {
						slotMax = sm
					}
					give := getQuickCraftPlaceCount(setSize, inv.quickcraftType, carriedCopy) + existing
					if slotMax < give {
						give = slotMax
					}
					remaining -= give - existing
					s.setByPlayer(stackCopyWithCount(carriedCopy, give))
				}
			}
			carriedCopy.Count = pk.VarInt(remaining) // carriedCopy.setCount(remaining)
			inv.setCarried(carriedCopy)
		}
		inv.resetQuickCraft()
		return
	}

	inv.resetQuickCraft()
}

// menuDoSingleSlotPickup ports the PICKUP real-slot arm of doClick (offsets 660-1076) over an arbitrary
// window slot -- the single-slot QUICK_CRAFT reduction (doClick(idx, type, PICKUP)). j is the drag type
// (0 spread / 1 single / 2 clone) reduced to PRIMARY (j==0 -> whole) vs SECONDARY (else -> one), exactly
// as the PICKUP branch reads its button. Only ever called on a placeable (non-result) slot the ADD phase
// admitted, so it is the empty/same-item merge + swap path. Verified bytecode.
func (t *TickLoop) menuDoSingleSlotPickup(p *tickPlayer, v menuView, i, j int) {
	if i < 0 || i >= v.size {
		return
	}
	s := v.slotAt(i)
	if !s.ok {
		return
	}
	inv := v.inv
	cur := inv.getCarried()
	slotItem := s.getItem()
	primary := j == 0

	if stackEmpty(slotItem) {
		if !stackEmpty(cur) && s.mayPlace(cur) {
			place := 1
			if primary {
				place = int(cur.Count)
			}
			c := cur
			inv.setCarried(menuSafeInsert(s, &c, place))
		}
		return
	}
	if stackEmpty(cur) {
		take := (int(slotItem.Count) + 1) / 2
		if primary {
			take = int(slotItem.Count)
		}
		if taken, ok := s.tryRemove(take, maxInt, p); ok {
			inv.setCarried(taken)
			s.onTake(p, taken)
		}
		return
	}
	if s.mayPlace(cur) {
		if stackSameItemSameComponents(slotItem, cur) {
			add := 1
			if primary {
				add = int(cur.Count)
			}
			c := cur
			inv.setCarried(menuSafeInsert(s, &c, add))
		} else if int(cur.Count) <= s.getMaxStackSize(cur) {
			inv.setCarried(slotItem)
			s.setByPlayer(cur)
		}
	} else if stackSameItemSameComponents(slotItem, cur) {
		room := int(slotItem.Count)
		headroom := stackMaxSize(cur) - int(cur.Count)
		if taken, ok := s.tryRemove(room, headroom, p); ok {
			c := inv.getCarried()
			c.Count += pk.VarInt(taken.Count)
			inv.setCarried(c)
			s.onTake(p, taken)
		}
	}
}

// menuSafeInsert ports Slot.safeInsert(stack, increment) over a menuSlotView: place up to min(increment,
// stack.count, getMaxStackSize(stack)-existing) of *stack into the slot (fill if empty / merge if same
// item), shrink *stack, write the slot, and return the slot NEW contents. *stack is mutated to the
// remainder. Verified Slot.safeInsert bytecode.
func menuSafeInsert(s menuSlotView, stack *component.SlotData, increment int) component.SlotData {
	existing := s.getItem()
	if stackEmpty(*stack) {
		return existing
	}
	add := min(increment, int(stack.Count))
	slotMax := s.getMaxStackSize(*stack)
	if room := slotMax - int(existing.Count); room < add {
		add = room
	}
	if add <= 0 {
		return existing
	}
	if stackEmpty(existing) {
		out := stackSplit(stack, add)
		s.setByPlayer(out)
		return out
	}
	if stackSameItemSameComponents(existing, *stack) {
		stack.Count = pk.VarInt(int(stack.Count) - add)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		existing.Count = pk.VarInt(int(existing.Count) + add)
		s.setByPlayer(existing)
		return existing
	}
	return existing
}
