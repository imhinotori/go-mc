package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// inventory_click.go ports net.minecraft.world.inventory.AbstractContainerMenu.doClick (the survival
// container-click engine) and net.minecraft.world.inventory.InventoryMenu.quickMoveStack 1:1 for the
// player's own inventory window (InventoryMenu, containerId 0). The whole engine rests on the
// net.minecraft.world.inventory.Slot primitives (safeInsert / safeTake / tryRemove / setByPlayer /
// getMaxStackSize / mayPickup / mayPlace / hasItem / getItem) — ported here over Sulfur's flat
// []component.SlotData backing (the 46 menu slots) instead of go-mc's Slot+Container object graph.
// Every branch, range bound, and numeric op mirrors temp/cache/26.2-inner.jar (orchestrator-verified
// bytecode) EXACTLY — re-expressed idiomatically in Go, NOT pasted. Tick-owned: every method here runs
// only on the tick goroutine (TICK-05 / T-6-08), so the in-place ItemStack mutations are race-clean by
// the same single-owner discipline as the rest of the inventory state.
//
// SCOPE: the 7 ContainerInput cases (PICKUP, QUICK_MOVE, SWAP, CLONE, THROW, QUICK_CRAFT, PICKUP_ALL)
// for window 0. The few subsystems not yet built in Sulfur are stubbed behind CITED constants equal to
// the vanilla default so they become real reads later (see the per-stub citations below):
//   - tryItemClickBehaviourOverride (bundle/feature items) → always false (ItemStack.overrideStackedOn*
//     for a plain stack returns false; out of v1 scope).
//   - getEquipmentSlotForItem (armor classification for shift-click auto-equip) → a non-armor/non-offhand
//     classification so normal shift-click routes main↔hotbar (the common case).
//   - ArmorSlot.mayPlace / isArmorForSlot → default true (any item placeable into an armor slot) — slot 0
//     (craft result) mayPlace is still hard false.
//   - ResultSlot.onTake / crafting consumption → WIRED (PLUGIN-05, Plan 25-02): a take of the result
//     slot (index 0) fires t.onTakeCraft (crafting_click.go) — the 1:1 per-cell consume through the
//     plugin matcher. See slotOnTake below.
//   - Player.canDropItems → true; Player.handleCreativeModeItemDrop → no-op (both are the vanilla bases).

// ItemStack-equivalent SlotData helpers — the ItemStack primitives the engine calls. SlotData is empty
// when Count <= 0. These mirror net.minecraft.world.item.ItemStack methods (verified bytecode):
//   isEmpty()                 → Count <= 0
//   getCount()                → Count
//   setCount(n)               → Count = n
//   grow(n)/shrink(n)         → Count += n / Count -= n (ItemStack.grow / shrink)
//   getMaxStackSize()         → maxStackSize(stack) (ItemStack.getMaxStackSize via max_stack_size component)
//   copy()                    → value copy (RawComponents shared; never mutated in place here)
//   copyWithCount(n)          → copy with Count = n; empty stays empty (ItemStack.copyWithCount)
//   split(n)                  → remove min(n,Count), return that as a stack, shrink source (ItemStack.split)
//   isSameItem(a,b)           → same ItemID only (ItemStack.isSameItem)
//   isSameItemSameComponents  → same ItemID AND same RawComponents (ItemStack.isSameItemSameComponents)
//   isStackable()             → getMaxStackSize() > 1 && !damaged (no durability in v1 → maxStackSize>1)

func stackEmpty(s component.SlotData) bool { return s.Count <= 0 }

// stackMaxSize ports ItemStack.getMaxStackSize() — the minecraft:max_stack_size data component (default
// 64). Reuses the existing maxStackSize(stack) helper (item_entity.go), which reads the generated item
// registry's StackSize and falls back to 64 (Item.DEFAULT_MAX_STACK_SIZE).
func stackMaxSize(s component.SlotData) int {
	if stackEmpty(s) {
		return 64
	}
	return maxStackSize(s)
}

// stackIsStackable ports ItemStack.isStackable(): getMaxStackSize() > 1 && (!isDamageableItem() ||
// !isDamaged()). v1 has no durability, so the damage guard is a CITED true (ItemStack.isDamageableItem
// not wired) — isStackable reduces to maxStackSize > 1.
func stackIsStackable(s component.SlotData) bool { return stackMaxSize(s) > 1 }

// stackCopyWithCount ports ItemStack.copyWithCount(n): empty stays EMPTY, else a copy with Count = n.
func stackCopyWithCount(s component.SlotData, n int) component.SlotData {
	if stackEmpty(s) {
		return component.SlotData{Count: 0}
	}
	out := s
	out.Count = pk.VarInt(n)
	return out
}

// stackSplit ports ItemStack.split(n): take min(n, Count), return a copy with that count, and shrink the
// receiver. Returns the taken stack; *s is mutated (count reduced).
func stackSplit(s *component.SlotData, n int) component.SlotData {
	take := n
	if int(s.Count) < take {
		take = int(s.Count)
	}
	taken := stackCopyWithCount(*s, take)
	s.Count -= pk.VarInt(take) // ItemStack.shrink(take)
	if s.Count <= 0 {
		s.Count = 0
	}
	return taken
}

// stackSameItem ports ItemStack.isSameItem(a,b): same item id (both non-empty).
func stackSameItem(a, b component.SlotData) bool {
	if stackEmpty(a) || stackEmpty(b) {
		return false
	}
	return a.ItemID == b.ItemID
}

// stackSameItemSameComponents ports ItemStack.isSameItemSameComponents(a,b): same item id AND identical
// components (the stacking identity). Two empties are NOT same-item (vanilla compares item then patch).
func stackSameItemSameComponents(a, b component.SlotData) bool {
	if stackEmpty(a) || stackEmpty(b) {
		return false
	}
	return a.ItemID == b.ItemID && bytes.Equal(a.RawComponents, b.RawComponents)
}

// menuSlot is the Sulfur analogue of net.minecraft.world.inventory.Slot bound to one index of the flat
// InventoryMenu window (inv.slots[index]). The Slot primitives are methods here; the "Container" is the
// inventory itself. index is the menu-slot index (0=result, 1-4 craft grid, 5-8 armor, 9-35 main,
// 36-44 hotbar, 45 offhand).
type menuSlot struct {
	inv   *Inventory
	index int
}

func (m menuSlot) getItem() component.SlotData { return m.inv.get(int16(m.index)) }
func (m menuSlot) hasItem() bool               { return !stackEmpty(m.getItem()) }

// setItem writes the slot directly (the Container.setItem the Slot delegates to). setChanged is a no-op
// for v1 (the authoritative re-send is the change broadcast).
func (m menuSlot) setItem(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	m.inv.set(int16(m.index), s)
}

// setByPlayer ports Slot.setByPlayer(stack) → set(stack) for the player-inventory container (the
// oldStack overload's container hook is a no-op here). Tick-owned.
func (m menuSlot) setByPlayer(s component.SlotData) { m.setItem(s) }

// mayPickup ports Slot.mayPickup(player). The base Slot returns true; InventoryMenu's main/hotbar/offhand
// and the craft RESULT slot 0 (ResultSlot) all use the base (pickable). The ARMOR slots (5-8) use
// ArmorSlot.mayPickup: an equipped, non-empty armor stack carrying a minecraft:prevent_armor_change enchant
// (Binding Curse) CANNOT be removed by a non-creative player -> return false. Gated on the enchant being
// present; an un-cursed armor piece (or a creative player) takes the base true, so the common path is
// unchanged. CITE Slot.mayPickup (base) + ArmorSlot.mayPickup (the PREVENT_ARMOR_CHANGE gate).
func (m menuSlot) mayPickup(p *tickPlayer) bool {
	if m.index >= 5 && m.index <= 8 {
		s := m.getItem()
		if !stackEmpty(s) && p != nil && p.gameMode != gameModeCreative && enchHasPreventArmorChange(s) {
			return false // ArmorSlot.mayPickup: !isEmpty && !isCreative && has(PREVENT_ARMOR_CHANGE)
		}
	}
	return true
}

// mayPlace ports Slot.mayPlace(stack). The base Slot returns true (CITE Slot.mayPlace). The craft RESULT
// slot 0 (ResultSlot.mayPlace) returns false — you cannot place INTO the result. Armor slots 5-8
// (ArmorSlot.mayPlace) gate on the item being valid armor for that EquipmentSlot; v1 has no armor-type
// data, so a CITED stub isArmorForSlot defaults true (any item placeable), structured to become a real
// check. Offhand 45 and main/hotbar use the base (true).
func (m menuSlot) mayPlace(stack component.SlotData) bool {
	switch {
	case m.index == 0:
		return false // ResultSlot.mayPlace: cannot place into the craft result
	case m.index >= 5 && m.index <= 8:
		return isArmorForSlot(stack, m.index) // ArmorSlot.mayPlace (CITED stub → true)
	default:
		return true // Slot.mayPlace base
	}
}

// getMaxStackSize() ports Slot.getMaxStackSize() = Container.getMaxStackSize() (default 64). Used by the
// no-arg merge cap.
func (m menuSlot) getMaxStackSizeBase() int { return 64 }

// getMaxStackSize(stack) ports Slot.getMaxStackSize(ItemStack) = min(getMaxStackSize(), stack max). For
// InventoryMenu all slots use the container default 64; armor/offhand are 1 only via mayPlace, not via
// stack limit — so this reduces to min(64, stack max) == stack max for stacks with max <= 64. Faithful
// to Slot.getMaxStackSize(stack).
func (m menuSlot) getMaxStackSize(stack component.SlotData) int {
	mx := m.getMaxStackSizeBase()
	if sm := stackMaxSize(stack); sm < mx {
		mx = sm
	}
	return mx
}

// remove(count) ports Slot.remove(count) → Container.removeItem(slot, count) = ContainerHelper.removeItem:
// split the slot's stack by count (removes min(count, present) and shrinks the slot). Returns the removed
// stack (EMPTY if the slot was empty or count <= 0).
func (m menuSlot) remove(count int) component.SlotData {
	cur := m.getItem()
	if stackEmpty(cur) || count <= 0 {
		return component.SlotData{Count: 0}
	}
	taken := stackSplit(&cur, count)
	m.setItem(cur)
	return taken
}

// tryRemove ports Slot.tryRemove(count, limit, player): returns the removed stack (ok=true) WITHOUT
// firing onTake (the caller fires it). Returns ok=false (empty) when not pickable or modification
// disallowed. allowModification = mayPickup && mayPlace(getItem); when modification is disallowed it only
// permits removal up to limit < count. The post-remove "if remaining empty, setByPlayer(EMPTY, taken)"
// restores an empty slot. Verified Slot.tryRemove bytecode.
func (m menuSlot) tryRemove(count, limit int, p *tickPlayer) (component.SlotData, bool) {
	if !m.mayPickup(p) {
		return component.SlotData{Count: 0}, false
	}
	if !m.allowModification(p) && limit < int(m.getItem().Count) {
		return component.SlotData{Count: 0}, false
	}
	count = min(count, limit)
	removed := m.remove(count)
	if stackEmpty(removed) {
		return component.SlotData{Count: 0}, false
	}
	if stackEmpty(m.getItem()) {
		// setByPlayer(EMPTY, removed): clears the slot (already empty after remove); the oldStack
		// hook is a no-op for the player container.
		m.setByPlayer(component.SlotData{Count: 0})
	}
	return removed, true
}

// allowModification ports Slot.allowModification(player) = mayPickup(player) && mayPlace(getItem()).
func (m menuSlot) allowModification(p *tickPlayer) bool {
	return m.mayPickup(p) && m.mayPlace(m.getItem())
}

// safeTake ports Slot.safeTake(count, limit, player): tryRemove, fire onTake on success, return the
// removed stack (EMPTY if none).
func (m menuSlot) safeTake(count, limit int, p *tickPlayer) component.SlotData {
	taken, ok := m.tryRemove(count, limit, p)
	if ok {
		m.onTake(p, taken)
	}
	return taken
}

// safeClone ports Slot.safeClone(player): a copy of the slot item with count = its max stack size (the
// creative middle-click clone).
func (m menuSlot) safeClone() component.SlotData {
	item := m.getItem()
	return stackCopyWithCount(item, stackMaxSize(item))
}

// safeInsert ports Slot.safeInsert(stack, increment): place up to min(increment, stack.count,
// getMaxStackSize(stack) - existing.count) of stack into the slot (merging if same item / filling if
// empty) and return the REMAINDER stack (the same logical stack, count reduced). Mutates *stack in place
// (shrink) exactly as the bytecode. Verified Slot.safeInsert(ItemStack, int).
func (m menuSlot) safeInsert(stack *component.SlotData, increment int) component.SlotData {
	if stackEmpty(*stack) || !m.mayPlace(*stack) {
		return *stack
	}
	existing := m.getItem()
	add := min(increment, int(stack.Count))
	if room := m.getMaxStackSize(*stack) - int(existing.Count); room < add {
		add = room
	}
	if add <= 0 {
		return *stack
	}
	if stackEmpty(existing) {
		// setByPlayer(stack.split(add)): place a fresh stack of `add`, shrinking the source.
		m.setByPlayer(stackSplit(stack, add))
	} else if stackSameItemSameComponents(existing, *stack) {
		stack.Count -= pk.VarInt(add) // ItemStack.shrink(add)
		if stack.Count <= 0 {
			stack.Count = 0
		}
		existing.Count += pk.VarInt(add) // ItemStack.grow(add)
		m.setByPlayer(existing)
	}
	return *stack
}

// onTake ports the BASE Slot.onTake(player, stack) = setChanged() (a no-op here). The RESULT slot's
// override (ResultSlot.onTake, the crafting consume) is NOT on this value-receiver method — it has no
// TickLoop/plugin access — so the engine routes a result-slot (index 0) take through t.slotOnTake
// (below), which dispatches to onTakeCraft. A non-result slot's onTake stays this base no-op.
func (m menuSlot) onTake(_ *tickPlayer, _ component.SlotData) {}

// slotOnTake is the TickLoop-bound onTake dispatch the player click engine calls instead of the bare
// menuSlot.onTake: for the RESULT slot (index 0) it fires the 1:1 ResultSlot.onTake crafting consume
// (t.onTakeCraft over the 2x2 player grid) so taking the crafted result removes 1-per-used-cell and
// recomputes the result (chained crafting); for any other slot it is the base setChanged no-op. This
// is the un-stub of the former ResultSlot.onTake no-op (the recipes are now wired through the matcher).
func (t *TickLoop) slotOnTake(p *tickPlayer, inv *Inventory, slotIndex int, _ component.SlotData) {
	if slotIndex == 0 {
		t.onTakeCraft(p, inv, playerCraftView(inv)) // ResultSlot.onTake (2x2 player grid)
	}
	// non-result slots: base Slot.onTake setChanged — no-op.
}

// onQuickCraft ports Slot.onQuickCraft(newStack, oldStack) → onQuickCraft(stack, count-delta). Base
// Slot.onQuickCraft(stack, int) is empty (only ResultSlot overrides). CITED no-op for v1.
func (m menuSlot) onQuickCraft(_ component.SlotData, _ component.SlotData) {}

// onSwapCraft ports Slot.onSwapCraft(numItemsCrafted) — base is empty. CITED no-op for v1.
func (m menuSlot) onSwapCraft(_ int) {}

// isArmorForSlot is the CITED stub for ArmorSlot.mayPlace's armor-type validity check (the item being
// the matching armor piece for the EquipmentSlot). v1 has no armor-type data, so it defaults true (any
// item placeable into an armor slot), structured so a real classifier slots in later. CITE
// net.minecraft.world.inventory.ArmorSlot.mayPlace.
func isArmorForSlot(_ component.SlotData, _ int) bool { return true }

// ---------------------------------------------------------------------------------------------------
// ClickAction (PRIMARY/SECONDARY) and ContainerInput ordinals — the on-wire VarInt is ContainerInput.id()
// which equals the enum ordinal (verified $VALUES order). The button byte j selects PRIMARY (j==0) vs
// SECONDARY (j==1) within PICKUP/QUICK_MOVE.

const (
	containerInputPickup     = 0 // ContainerInput.PICKUP
	containerInputQuickMove  = 1 // ContainerInput.QUICK_MOVE
	containerInputSwap       = 2 // ContainerInput.SWAP
	containerInputClone      = 3 // ContainerInput.CLONE
	containerInputThrow      = 4 // ContainerInput.THROW
	containerInputQuickCraft = 5 // ContainerInput.QUICK_CRAFT
	containerInputPickupAll  = 6 // ContainerInput.PICKUP_ALL
)

const (
	clickActionPrimary   = 0
	clickActionSecondary = 1
)

// menuSlotCount is the number of InventoryMenu slots (slots.size()). Equals playerInventorySize (46).
const menuSlotCount = playerInventorySize

// slotAt builds the menuSlot for index i. i is assumed in-range (callers guard i<0 / i>=size per the
// bytecode); an out-of-range index is clamped by inv.get/set (defensive no-op).
func (t *TickLoop) slotAt(inv *Inventory, i int) menuSlot { return menuSlot{inv: inv, index: i} }

// getCarried / setCarried port AbstractContainerMenu.getCarried / setCarried — the cursor item.
func (inv *Inventory) getCarried() component.SlotData {
	if stackEmpty(inv.carried) {
		return component.SlotData{Count: 0}
	}
	return inv.carried
}
func (inv *Inventory) setCarried(s component.SlotData) {
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	inv.carried = s
}

// incrementStateId ports AbstractContainerMenu.incrementStateId(): stateId = (stateId + 1) & 32767;
// return stateId. The client echoes this id in the NEXT ServerboundContainerClick; the server compares
// it to detect desync. Every authoritative container packet (ContainerSetContent / SetSlot for window 0
// AND every open non-player window) MUST carry an id from this single masked counter — a bare `stateId++`
// eventually overflows past 32767 and diverges from the client's `& 32767` expectation, so all senders
// route through here. CITE (VERIFIED javap): (stateId + 1) & 32767.
func (inv *Inventory) incrementStateId() int32 {
	inv.stateID = (inv.stateID + 1) & 0x7FFF
	return inv.stateID
}

// resetQuickCraft ports AbstractContainerMenu.resetQuickCraft(): status=0, clear the drag set.
func (inv *Inventory) resetQuickCraft() {
	inv.quickcraftStatus = 0
	inv.quickcraftSlots = inv.quickcraftSlots[:0]
}

// getQuickcraftType ports getQuickcraftType(eventByte) = (eventByte >> 2) & 3.
func getQuickcraftType(eventByte int) int { return (eventByte >> 2) & 3 }

// getQuickcraftHeader ports getQuickcraftHeader(eventByte) = eventByte & 3.
func getQuickcraftHeader(eventByte int) int { return eventByte & 3 }

// isValidQuickcraftType ports isValidQuickcraftType(type, player): type 0 (spread/left-drag) → true,
// type 1 (single/right-drag) → true, type 2 (creative clone-drag) → player.hasInfiniteMaterials(),
// else false.
func isValidQuickcraftType(t int, p *tickPlayer) bool {
	switch t {
	case 0:
		return true
	case 1:
		return true
	case 2:
		return p.gameMode == gameModeCreative // Player.hasInfiniteMaterials() == abilities.instabuild
	default:
		return false
	}
}

// canItemQuickReplace ports AbstractContainerMenu.canItemQuickReplace(slot, stack, stackSizeMatters):
//
//	bl = (slot == null || !slot.hasItem());
//	if (bl) return true;                                  // empty/absent slot always replaceable
//	if (isSameItemSameComponents(stack, slot.getItem()))  // same item: room check
//	    return slot.getItem().getCount() + (stackSizeMatters ? 0 : stack.getCount()) <= stack.getMaxStackSize();
//	return bl;                                            // different item → false
//
// (slot is never nil here — Sulfur passes a real menuSlot; the null branch folds into !hasItem.)
func canItemQuickReplace(s menuSlot, stack component.SlotData, stackSizeMatters bool) bool {
	bl := !s.hasItem()
	if bl {
		return true
	}
	if stackSameItemSameComponents(stack, s.getItem()) {
		add := 0
		if !stackSizeMatters {
			add = int(stack.Count)
		}
		return int(s.getItem().Count)+add <= stackMaxSize(stack)
	}
	return bl // false
}

// getQuickCraftPlaceCount ports getQuickCraftPlaceCount(setSize, type, stack):
//
//	type 0: Mth.floor((float)count / (float)setSize)   (integer-floor of the float division)
//	type 1: 1
//	type 2: stack.getMaxStackSize()
//	default: stack.getCount()
func getQuickCraftPlaceCount(setSize, t int, stack component.SlotData) int {
	switch t {
	case 0:
		if setSize <= 0 {
			return 0
		}
		// Mth.floor(float(count)/float(setSize)): both non-negative here, so integer division
		// equals the floor of the float quotient.
		return int(stack.Count) / setSize
	case 1:
		return 1
	case 2:
		return stackMaxSize(stack)
	default:
		return int(stack.Count)
	}
}

// canDragTo ports AbstractContainerMenu.canDragTo(slot) = true (base).
func canDragTo(_ menuSlot) bool { return true }

// ---------------------------------------------------------------------------------------------------
// Player.Inventory index mapping for SWAP (the j hotbar/offhand selector). SWAP's button j indexes the
// PLAYER INVENTORY (hotbar 0-8, offhand 40), NOT the menu slots — so invGetItem/invSetItem map the
// vanilla Inventory index space onto Sulfur's menu cells: hotbar 0-8 → menu 36-44, offhand 40 → menu 45.
// CITE net.minecraft.world.entity.player.Inventory.getItem / setItem index layout.
func invMenuIndex(invIndex int) int {
	if invIndex == 40 {
		return windowSlotOffhand // offhand: Inventory index 40 → menu slot 45
	}
	if invIndex >= 0 && invIndex < 9 {
		return windowHotbarFirst + invIndex // hotbar 0-8 → menu 36-44
	}
	return -1 // SWAP only ever passes 0-8 or 40
}

func (inv *Inventory) invGetItem(invIndex int) component.SlotData {
	mi := invMenuIndex(invIndex)
	if mi < 0 {
		return component.SlotData{Count: 0}
	}
	return inv.get(int16(mi))
}

func (inv *Inventory) invSetItem(invIndex int, s component.SlotData) {
	mi := invMenuIndex(invIndex)
	if mi < 0 {
		return
	}
	if stackEmpty(s) {
		s = component.SlotData{Count: 0}
	}
	inv.set(int16(mi), s)
}

// invAdd ports Inventory.add(stack): attempt to deposit the whole stack into main/hotbar storage; returns
// true iff fully placed. Reuses addResource (Inventory.addResource) in a loop until nothing more fits or
// the stack is empty. Used by SWAP's "both non-empty & over-max" fallback. CITE Inventory.add.
func (t *TickLoop) invAdd(inv *Inventory, stack *component.SlotData) bool {
	if stackEmpty(*stack) {
		return true
	}
	for !stackEmpty(*stack) {
		remaining := t.addResource(inv, stack)
		if remaining == int(stack.Count) {
			// addResource placed nothing this pass: inventory full for this item.
			stack.Count = pk.VarInt(remaining)
			return false
		}
		stack.Count = pk.VarInt(remaining)
		if remaining <= 0 {
			stack.Count = 0
			return true
		}
	}
	return true
}

// ---------------------------------------------------------------------------------------------------
// moveItemStackTo ports AbstractContainerMenu.moveItemStackTo(stack, startIndex, endIndex, reverse) — the
// shift-click engine. Two passes: pass 1 merges *stack into existing same-item stacks (honoring
// getMaxStackSize and isStackable); pass 2 fills empty slots (honoring mayPlace). reverse walks
// endIndex-1 downTo startIndex. MUTATES *stack in place (shrinks it). Returns whether anything moved.
// Verified bytecode (offsets 0-330).
func (t *TickLoop) moveItemStackTo(inv *Inventory, stack *component.SlotData, startIndex, endIndex int, reverse bool) bool {
	moved := false

	// Pass 1: merge into existing same-item stacks (only if the stack is stackable & non-empty).
	if stackIsStackable(*stack) && !stackEmpty(*stack) {
		var i int
		if reverse {
			i = endIndex - 1
		} else {
			i = startIndex
		}
		for !stackEmpty(*stack) {
			if reverse {
				if i < startIndex {
					break
				}
			} else {
				if i >= endIndex {
					break
				}
			}
			s := t.slotAt(inv, i)
			existing := s.getItem()
			if !stackEmpty(existing) && stackSameItemSameComponents(*stack, existing) {
				sum := int(existing.Count) + int(stack.Count)
				slotMax := s.getMaxStackSize(existing)
				if sum <= slotMax {
					stack.Count = 0 // setCount(0)
					existing.Count = pk.VarInt(sum)
					s.setItem(existing) // setChanged
					moved = true
				} else if int(existing.Count) < slotMax {
					stack.Count -= pk.VarInt(slotMax - int(existing.Count)) // shrink(slotMax - existing.count)
					existing.Count = pk.VarInt(slotMax)
					s.setItem(existing) // setChanged
					moved = true
				}
			}
			if reverse {
				i--
			} else {
				i++
			}
		}
	}

	// Pass 2: fill empty slots honoring mayPlace.
	if !stackEmpty(*stack) {
		var i int
		if reverse {
			i = endIndex - 1
		} else {
			i = startIndex
		}
		for {
			if reverse {
				if i < startIndex {
					break
				}
			} else {
				if i >= endIndex {
					break
				}
			}
			s := t.slotAt(inv, i)
			existing := s.getItem()
			if stackEmpty(existing) && s.mayPlace(*stack) {
				slotMax := s.getMaxStackSize(*stack)
				place := int(stack.Count)
				if slotMax < place {
					place = slotMax
				}
				s.setByPlayer(stackSplit(stack, place)) // setByPlayer(stack.split(min(count, slotMax)))
				// setChanged
				moved = true
				break
			}
			if reverse {
				i--
			} else {
				i++
			}
		}
	}

	return moved
}

// quickMoveStack ports InventoryMenu.quickMoveStack(player, i) — the survival shift-click routing for the
// player window. Returns the moved stack copy (EMPTY when nothing moved). MUTATES the slot (and *stack
// inside moveItemStackTo). Verified bytecode (InventoryMenu.quickMoveStack offsets 0-387).
//
// The armor/offhand auto-equip branches need getEquipmentSlotForItem (armor classification). v1 has no
// equipment classifier, so a CITED stub equipmentSlotForItem returns a non-armor/non-offhand
// classification — normal items therefore route main↔hotbar (the common case), and craft-result (0) /
// craft-grid (1-4) / armor (5-8) source slots still route to 9..45. Structured so a real classifier slots
// in. CITE net.minecraft.world.entity.player.Player.getEquipmentSlotForItem.
func (t *TickLoop) quickMoveStack(p *tickPlayer, inv *Inventory, i int) component.SlotData {
	empty := component.SlotData{Count: 0}
	s := t.slotAt(inv, i)
	if !s.hasItem() {
		return empty
	}
	item := s.getItem() // working stack (mutated by moveItemStackTo)
	copyOf := item      // ItemStack copy = item.copy()

	eqType, eqArmorIdx, isOffhand := equipmentSlotForItem(item) // getEquipmentSlotForItem

	switch {
	case i == 0:
		// craft result → main+hotbar (9..45), reverse
		if !t.moveItemStackTo(inv, &item, 9, 45, true) {
			return empty
		}
		s.onQuickCraft(item, copyOf)
	case i >= 1 && i < 5:
		// craft grid → main+hotbar (9..45)
		if !t.moveItemStackTo(inv, &item, 9, 45, false) {
			return empty
		}
	case i >= 5 && i < 9:
		// armor → main+hotbar (9..45)
		if !t.moveItemStackTo(inv, &item, 9, 45, false) {
			return empty
		}
	case eqType == equipHumanoidArmor && !t.slotAt(inv, 8-eqArmorIdx).hasItem():
		// → empty armor slot (8 - eq.getIndex())
		armorIdx := 8 - eqArmorIdx
		if !t.moveItemStackTo(inv, &item, armorIdx, armorIdx+1, false) {
			return empty
		}
	case isOffhand && !t.slotAt(inv, 45).hasItem():
		// → offhand
		if !t.moveItemStackTo(inv, &item, 45, 46, false) {
			return empty
		}
	case i >= 9 && i < 36:
		// main → hotbar (36..45)
		if !t.moveItemStackTo(inv, &item, 36, 45, false) {
			return empty
		}
	case i >= 36 && i < 45:
		// hotbar → main (9..36)
		if !t.moveItemStackTo(inv, &item, 9, 36, false) {
			return empty
		}
	default:
		// fallback → main+hotbar (9..45)
		if !t.moveItemStackTo(inv, &item, 9, 45, false) {
			return empty
		}
	}

	if stackEmpty(item) {
		s.setByPlayer(component.SlotData{Count: 0}) // setByPlayer(EMPTY, copy)
	} else {
		s.setItem(item) // setChanged
	}
	if int(item.Count) == int(copyOf.Count) {
		return empty // nothing actually moved
	}
	t.slotOnTake(p, inv, i, item) // ResultSlot.onTake for slot 0 (shift-click of the crafted result)
	if i == 0 {
		t.playerDrop(p, item, false) // player.drop(item, false)
	}
	return copyOf
}

// equipmentSlotForItem is the CITED stub for Player.getEquipmentSlotForItem (the armor/offhand
// classification used by shift-click auto-equip). v1 has no equipment classifier, so it returns a
// non-armor / non-offhand result — normal shift-clicks route main↔hotbar (the common case). Structured so
// a real classifier slots in. CITE net.minecraft.world.entity.player.Player.getEquipmentSlotForItem.
// Returns (equipType, armorIndex, isOffhand).
func equipmentSlotForItem(_ component.SlotData) (equipType int, armorIndex int, isOffhand bool) {
	return equipMainHand, 0, false
}

const (
	equipMainHand      = 0 // EquipmentSlot.MAINHAND (the non-armor/non-offhand default)
	equipHumanoidArmor = 1 // EquipmentSlot.Type.HUMANOID_ARMOR marker (unreached by the v1 stub)
)
