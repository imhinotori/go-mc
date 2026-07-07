package server

import (
	"math/rand"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// inventory_doclick.go ports AbstractContainerMenu.doClick (the 7-input survival click dispatcher) and
// the Player.drop spawn used by THROW / outside-drop / SWAP-overflow. doClick is called from clicked()
// (handleContainerClick), which wraps it in a panic-recover (the vanilla clicked() try/CrashReport
// equivalent — T-6-04) and then broadcasts changed slots + the carried item. Every branch mirrors
// temp/cache/26.2-inner.jar (orchestrator-verified bytecode) EXACTLY. Tick-owned.
//
// maxInt is Integer.MAX_VALUE, the `limit` passed to safeTake/tryRemove when the caller imposes no cap
// (the slot's own count is the real cap).
const maxInt = int(^uint32(0) >> 1) // 2147483647

// doClick ports AbstractContainerMenu.doClick(slotId i, button j, ContainerInput input, Player player).
// inv is player.getInventory(). Verified bytecode (doClick offsets 0-1873).
func (t *TickLoop) doClick(p *tickPlayer, inv *Inventory, i, j, input int) {
	switch input {
	case containerInputQuickCraft:
		t.doClickQuickCraft(p, inv, i, j)
		return
	case containerInputPickup, containerInputQuickMove:
		// Guard: a click of these types while a quick-craft drag is mid-flight resets the drag and
		// returns (bytecode @547-560).
		if inv.quickcraftStatus != 0 {
			inv.resetQuickCraft()
			return
		}
		t.doClickPickupOrQuickMove(p, inv, i, j, input)
		return
	case containerInputSwap:
		t.doClickSwap(p, inv, i, j)
		return
	case containerInputClone:
		t.doClickClone(p, inv, i)
		return
	case containerInputThrow:
		t.doClickThrow(p, inv, i, j)
		return
	case containerInputPickupAll:
		t.doClickPickupAll(p, inv, i, j)
		return
	}
}

// doClickQuickCraft ports the QUICK_CRAFT drag state machine (doClick offsets 14-544).
func (t *TickLoop) doClickQuickCraft(p *tickPlayer, inv *Inventory, i, j int) {
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

	case 1: // ADD the slot i to the drag set
		s := t.slotAt(inv, i)
		carried := inv.getCarried()
		if canItemQuickReplace(s, carried, true) && s.mayPlace(carried) &&
			(inv.quickcraftType == 2 || int(carried.Count) > len(inv.quickcraftSlots)) &&
			canDragTo(s) {
			inv.addQuickcraftSlot(s.index)
		}
		return

	case 2: // END — distribute the carried stack across the drag set
		if len(inv.quickcraftSlots) != 0 {
			if len(inv.quickcraftSlots) == 1 {
				idx := inv.quickcraftSlots[0]
				inv.resetQuickCraft()
				// doClick(idx, quickcraftType, PICKUP, player) — a single-slot drag is just a PICKUP.
				t.doClick(p, inv, idx, inv.quickcraftType, containerInputPickup)
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
				s := t.slotAt(inv, idx)
				carried := inv.getCarried()
				if canItemQuickReplace(s, carried, true) && s.mayPlace(carried) &&
					(inv.quickcraftType == 2 || int(carried.Count) >= setSize) && canDragTo(s) {
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

// doClickPickupOrQuickMove ports the PICKUP (outside-drop + real-slot) and QUICK_MOVE branches
// (doClick offsets 561-1076). j is 0 (PRIMARY) or 1 (SECONDARY); any other j falls through to nothing
// (the @575-581 guard).
func (t *TickLoop) doClickPickupOrQuickMove(p *tickPlayer, inv *Inventory, i, j, input int) {
	// @575: if (j != 0 && j != 1) return (fall through to nothing).
	if j != 0 && j != 1 {
		return
	}
	action := clickActionPrimary
	if j != 0 {
		action = clickActionSecondary
	}

	switch {
	case input == containerInputPickup && i == -999:
		// Click outside the window: drop the carried item.
		carried := inv.getCarried()
		if !stackEmpty(carried) {
			if action == clickActionPrimary {
				t.playerDrop(p, carried, true) // drop the whole cursor stack
				inv.setCarried(component.SlotData{Count: 0})
			} else {
				// drop one (getCarried().split(1)); the cursor keeps the remainder (split mutates it).
				c := inv.getCarried()
				one := stackSplit(&c, 1)
				inv.setCarried(c)
				t.playerDrop(p, one, true)
			}
		}

	case input == containerInputQuickMove:
		if i < 0 {
			return
		}
		s := t.slotAt(inv, i)
		if !s.mayPickup(p) {
			return
		}
		moved := t.quickMoveStack(p, inv, i)
		for !stackEmpty(moved) && stackSameItem(s.getItem(), moved) {
			moved = t.quickMoveStack(p, inv, i)
		}

	default:
		// PICKUP on a real slot.
		if i < 0 {
			return
		}
		s := t.slotAt(inv, i)
		slotItem := s.getItem()
		carried := inv.getCarried()

		// tryItemClickBehaviourOverride (bundle/feature items) is OUT of v1 scope (returns false).
		// CITE AbstractContainerMenu.tryItemClickBehaviourOverride.

		if stackEmpty(slotItem) {
			if !stackEmpty(carried) {
				place := 1
				if action == clickActionPrimary {
					place = int(carried.Count)
				}
				c := carried
				inv.setCarried(s.safeInsert(&c, place))
			}
		} else if s.mayPickup(p) {
			if stackEmpty(carried) {
				take := (int(slotItem.Count) + 1) / 2 // SECONDARY: ceil(count/2)
				if action == clickActionPrimary {
					take = int(slotItem.Count)
				}
				if taken, ok := s.tryRemove(take, maxInt, p); ok {
					inv.setCarried(taken)
					t.slotOnTake(p, inv, i, taken) // ResultSlot.onTake for slot 0 (the crafting consume)
				}
			} else if s.mayPlace(carried) {
				if stackSameItemSameComponents(slotItem, carried) {
					add := 1
					if action == clickActionPrimary {
						add = int(carried.Count)
					}
					c := carried
					inv.setCarried(s.safeInsert(&c, add))
				} else if int(carried.Count) <= s.getMaxStackSize(carried) {
					// swap cursor <-> slot
					inv.setCarried(slotItem)
					s.setByPlayer(carried)
				}
			} else if stackSameItemSameComponents(slotItem, carried) {
				// merge slot INTO carried (pick up to fill the cursor)
				room := int(slotItem.Count)
				headroom := stackMaxSize(carried) - int(carried.Count)
				if taken, ok := s.tryRemove(room, headroom, p); ok {
					c := inv.getCarried()
					c.Count += pk.VarInt(taken.Count) // carried.grow(taken.getCount())
					inv.setCarried(c)
					t.slotOnTake(p, inv, i, taken) // ResultSlot.onTake for slot 0 (the crafting consume)
				}
			}
		}
		// s.setChanged() — no-op for v1.
	}
}

// menuOutsideDrop ports the doClick PICKUP i==-999 arm (offsets 561-660) — a click OUTSIDE any container
// window drops the carried cursor into the world: PRIMARY (j==0) drops the whole cursor stack, SECONDARY
// (j==1) drops one. It is GENERIC over every menu (the drop only touches the cursor + the world), so each
// per-menu doXClick calls it up-front and returns when it handled the click. Returns true iff this was the
// outside-drop case (input==PICKUP && i==-999). CITE AbstractContainerMenu.doClick (the i==-999 branch).
func (t *TickLoop) menuOutsideDrop(p *tickPlayer, inv *Inventory, i, j, input int) bool {
	if input != containerInputPickup || i != -999 {
		return false
	}
	if j != 0 && j != 1 {
		return true // still the outside case, but an invalid button: no-op (the @575 guard)
	}
	carried := inv.getCarried()
	if stackEmpty(carried) {
		return true
	}
	if j == 0 {
		t.playerDrop(p, carried, true) // drop the whole cursor stack
		inv.setCarried(component.SlotData{Count: 0})
	} else {
		c := inv.getCarried()
		one := stackSplit(&c, 1) // drop one; the cursor keeps the remainder
		inv.setCarried(c)
		t.playerDrop(p, one, true)
	}
	return true
}

// doClickSwap ports the SWAP branch (doClick offsets 1079-1382). j indexes the PLAYER INVENTORY (hotbar
// 0-8 or offhand 40), mapped to menu cells via invGetItem/invSetItem.
func (t *TickLoop) doClickSwap(p *tickPlayer, inv *Inventory, i, j int) {
	// @1086: if (!((j>=0 && j<9) || j==40)) return.
	if !((j >= 0 && j < 9) || j == 40) {
		return
	}
	if i < 0 {
		return
	}
	hotbar := inv.invGetItem(j) // inv.getItem(j)
	s := t.slotAt(inv, i)
	slotItem := s.getItem()

	switch {
	case stackEmpty(hotbar) && stackEmpty(slotItem):
		// both empty: nothing.

	case stackEmpty(hotbar):
		// hotbar empty & slot non-empty: move slot → hotbar, clear the slot.
		if !s.mayPickup(p) {
			return
		}
		inv.invSetItem(j, slotItem)
		s.onSwapCraft(int(slotItem.Count))
		s.setByPlayer(component.SlotData{Count: 0})
		s.onTake(p, slotItem)

	case stackEmpty(slotItem):
		// slot empty & hotbar non-empty: move hotbar → slot (split if over the slot's max).
		if !s.mayPlace(hotbar) {
			return
		}
		slotMax := s.getMaxStackSize(hotbar)
		if int(hotbar.Count) > slotMax {
			h := hotbar
			s.setByPlayer(stackSplit(&h, slotMax)) // split slotMax INTO the slot; remainder stays
			inv.invSetItem(j, h)
		} else {
			inv.invSetItem(j, component.SlotData{Count: 0})
			s.setByPlayer(hotbar)
		}

	default:
		// both non-empty: swap (split-if-over-max with the inventory.add/drop fallback).
		if !s.mayPickup(p) || !s.mayPlace(hotbar) {
			return
		}
		slotMax := s.getMaxStackSize(hotbar)
		if int(hotbar.Count) > slotMax {
			// @1315-1355: s.setByPlayer(hotbar.split(slotMax)) puts slotMax of hotbar into the slot; in
			// Java `hotbar` is the live in-inventory reference, so split() leaves the hotbar cell holding
			// the remainder. Sulfur's invGetItem returned a value copy, so we write the post-split
			// remainder (h) back to the hotbar index explicitly. Then onTake, and the displaced slotItem
			// goes to inventory.add or, if that fails, player.drop.
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

// doClickClone ports the CLONE branch (doClick offsets 1385-1446). Creative-only middle-click: clone the
// slot's item to a max-size stack onto an empty cursor.
func (t *TickLoop) doClickClone(p *tickPlayer, inv *Inventory, i int) {
	if p.gameMode != gameModeCreative { // player.hasInfiniteMaterials()
		return
	}
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	s := t.slotAt(inv, i)
	if s.hasItem() {
		inv.setCarried(s.safeClone())
	}
}

// doClickThrow ports the THROW branch (doClick offsets 1449-1608). Drops from the slot under the cursor
// (empty cursor). j==0 drops 1; j==1 drops the whole slot (and keeps draining same-item splits).
func (t *TickLoop) doClickThrow(p *tickPlayer, inv *Inventory, i, j int) {
	if !stackEmpty(inv.getCarried()) {
		return
	}
	if i < 0 {
		return
	}
	s := t.slotAt(inv, i)
	amt := 1
	if j != 0 {
		amt = int(s.getItem().Count)
	}
	if !t.playerCanDropItems(p) { // Player.canDropItems() (base true)
		return
	}
	taken := s.safeTake(amt, maxInt, p)
	t.playerDrop(p, taken, true)
	t.handleCreativeModeItemDrop(p, taken) // no-op for survival

	if j == 1 {
		for !stackEmpty(taken) && stackSameItem(s.getItem(), taken) {
			if !t.playerCanDropItems(p) {
				return
			}
			taken = s.safeTake(amt, maxInt, p)
			t.playerDrop(p, taken, true)
			t.handleCreativeModeItemDrop(p, taken)
		}
	}
}

// doClickPickupAll ports the PICKUP_ALL branch (doClick offsets 1611-1873): double-click collects matching
// same-item slots into the carried stack up to its max. Two passes: pass 0 skips already-full source
// stacks, pass 1 includes them.
func (t *TickLoop) doClickPickupAll(p *tickPlayer, inv *Inventory, i, j int) {
	if i < 0 {
		return
	}
	s := t.slotAt(inv, i)
	carried := inv.getCarried()
	if stackEmpty(carried) {
		return
	}
	// @1649: if (s.hasItem() && s.mayPickup(player)) return — a takeable matching slot is handled as a
	// normal pickup elsewhere.
	if s.hasItem() && s.mayPickup(p) {
		return
	}

	start := 0
	dir := 1
	if j != 0 {
		start = menuSlotCount - 1
		dir = -1
	}

	for pass := 0; pass < 2; pass++ {
		k := start
		for k >= 0 && k < menuSlotCount && int(inv.getCarried().Count) < stackMaxSize(inv.getCarried()) {
			c := t.slotAt(inv, k)
			carried := inv.getCarried()
			if c.hasItem() && canItemQuickReplace(c, carried, true) && c.mayPickup(p) &&
				t.canTakeItemForPickAll(carried, c) {
				it := c.getItem()
				if pass != 0 || int(it.Count) != stackMaxSize(it) {
					taken := c.safeTake(int(it.Count), stackMaxSize(carried)-int(carried.Count), p)
					cc := inv.getCarried()
					cc.Count += pk.VarInt(taken.Count) // carried.grow(taken.getCount())
					inv.setCarried(cc)
				}
			}
			k += dir
		}
	}
}

// canTakeItemForPickAll ports InventoryMenu.canTakeItemForPickAll(stack, slot). For non-result slots it is
// true (the base AbstractContainerMenu.canTakeItemForPickAll). v1 has no crafting result container, so it
// is always true. CITE InventoryMenu.canTakeItemForPickAll / AbstractContainerMenu.canTakeItemForPickAll.
func (t *TickLoop) canTakeItemForPickAll(_ component.SlotData, _ menuSlot) bool { return true }

// playerCanDropItems ports Player.canDropItems() = true (base). CITE Player.canDropItems.
func (t *TickLoop) playerCanDropItems(_ *tickPlayer) bool { return true }

// handleCreativeModeItemDrop ports Player.handleCreativeModeItemDrop(stack) = no-op for survival (the
// base Player returns immediately; ServerPlayer's override only matters for creative-mode statistics).
// CITE Player.handleCreativeModeItemDrop.
func (t *TickLoop) handleCreativeModeItemDrop(_ *tickPlayer, _ component.SlotData) {}

// playerDrop ports Player.drop(stack, dropAround) → drop(stack, false, dropAround): spawn an ItemEntity
// carrying `stack` near the player (at roughly eye height, with a small toss velocity). v1 reuses the
// existing ItemEntity spawn (NewItemEntity / the tracker broadcast) rather than the full vanilla throw
// kinematics — the observable result (a dropped item entity appears) is the requirement; the exact toss
// vector is a refinement. An empty stack drops nothing. CITE net.minecraft.world.entity.player.Player.drop.
//
// Vanilla Player.drop spawns the ItemEntity at (x, eyeY - 0.3, z) and sets a velocity derived from the
// look vector (when not dropAround) or a small random scatter (when dropAround). v1 spawns at eye height
// with NewItemEntity's random toss; pickupDelay defaults to 10 via NewItemEntity. Structured so the real
// throw kinematics replace the velocity later without touching callers.
func (t *TickLoop) playerDrop(p *tickPlayer, stack component.SlotData, dropAround bool) {
	if stackEmpty(stack) {
		return
	}
	_ = dropAround // v1 uses the same random scatter for both; the look-vector throw is a refinement.

	// Eye height ~1.62 above feet for a standing player (Player.getEyeHeight default). Spawn a touch
	// below eye (vanilla uses eyeY - 0.3) so the item appears in front of/below the camera.
	const eyeHeight = 1.62
	x := p.x + (rand.Float64()-0.5)*0.1
	y := p.y + eyeHeight - 0.3
	z := p.z + (rand.Float64()-0.5)*0.1

	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, stack)
	t.cur().entities.add(ie) // store insert → the tracker broadcasts AddEntity + SetEntityData
}
