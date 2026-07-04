package server

// grindstone_menu.go — the GRINDSTONE block + its 2-input REPAIR/DISENCHANT menu (GrindstoneMenu). A
// sibling of the merchant/stonecutter transient menus: two INPUT slots (repairSlots 0/1), a virtual
// RESULT slot (a ResultContainer entry recomputed from the inputs), and a take that consumes the inputs
// + returns disenchant XP. It clones the container-menu scaffolding (openContainer.kind,
// nextContainerCounter, openScreen, containerSetContent, the close-returns-inputs).
//
// 1:1 jar chain (temp/cache/26.2-inner.jar, CFR this session):
//
//	GrindstoneBlock.useWithoutItem: !clientSide -> player.openMenu(getMenuProvider(...)) + awardStat; SUCCESS.
//	  getMenuProvider = new GrindstoneMenu(id, inv, ContainerLevelAccess.create(level, pos));
//	  getDisplayName() = "container.grindstone" ("Repair & Disenchant").
//	GrindstoneMenu: repairSlots (SimpleContainer size 2, slots 0/1, mayPlace = isDamageableItem ||
//	  hasAnyEnchantments); resultSlots (ResultContainer, slot 2, mayPlace=false, take-only);
//	  addStandardInventorySlots (inv 3..29 + hotbar 30..38).
//	GrindstoneMenu.slotsChanged(repairSlots) -> createResult -> resultSlots.setItem(0,
//	  computeResult(getItem(0), getItem(1))) + broadcastChanges.
//	computeResult(input, additional):
//	  if both empty -> EMPTY; if input.count>1 || additional.count>1 -> EMPTY;
//	  if only one non-empty: if !hasAnyEnchantments -> EMPTY; else removeNonCursesFrom(item.copy())  (DISENCHANT)
//	  else -> mergeItems(input, additional)                                                          (REPAIR)
//	mergeItems: input.is(additional.item)? no -> EMPTY; durability = max(maxDamage1, maxDamage2);
//	  remaining = (maxDmg1-dmg1) + (maxDmg2-dmg2) + durability*5/100; count=1 (or 2 for a stackable pair);
//	  newItem = input.copyWithCount(count); if damageable { set MAX_DAMAGE=durability; setDamageValue(
//	  max(durability-remaining,0)) }; mergeEnchantsFrom(newItem, additional); return removeNonCursesFrom(newItem).
//	ResultSlot(2).onTake: award getExperienceAmount XP orbs at pos + levelEvent(1042); clear repairSlots 0,1.
//	  getExperienceAmount = sum getExperienceFromItem over the two inputs; if >0 -> half=ceil(sum/2);
//	  return half + random.nextInt(half). getExperienceFromItem = sum non-curse enchant getMinCost(level).
//	GrindstoneMenu.removed -> clearContainer(player, repairSlots)  (inputs returned; result virtual).

import (
	"math"

	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// grindstoneMenuSize is the grindstone-window slot count: 2 inputs + 1 result + 27 main + 9 hotbar = 39.
// (GrindstoneMenu: INPUT 0, ADDITIONAL 1, RESULT 2, inv 3..29, use-row 30..38.) Verified the ctor.
const grindstoneMenuSize = 2 + 1 + 27 + 9 // 39

// grindstoneTitle is the grindstone window's display name. Vanilla getDisplayName = "container.grindstone"
// → "Repair & Disenchant". v1 sends the plain literal (structured to become a translatable Component later).
const grindstoneTitle = "Repair & Disenchant"

// grindstoneInvStart / grindstoneUseRowEnd are the GrindstoneMenu inventory ranges the quick-move loop
// uses (INV_SLOT_START=3, USE_ROW_SLOT_END=39). Verified GrindstoneMenu static fields.
const (
	grindstoneInvStart  = 3
	grindstoneUseRowEnd = 39
)

// openGrindstone ports GrindstoneBlock.useWithoutItem → ServerPlayer.openMenu for a grindstone at pos:
// close any prior window, allocate a windowId, send OpenScreen(grindstone) + the initial content (empty
// inputs + result + the player inventory), and record the open-container state. Returns true (consumed).
func (t *TickLoop) openGrindstone(p *tickPlayer, pos pk.Position) bool {
	if p.client == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil // ServerPlayer.openMenu: close any previously-open window first.
	}

	win := p.nextContainerCounter()
	p.openContainer = &openContainer{windowID: win, kind: containerKindGrindstone, grindPos: pos}

	menuID := menuTypeID(registryid.Menu, "minecraft:grindstone")
	p.client.Send(openScreen(int32(win), menuID, grindstoneTitle))

	// initMenu → broadcastChanges: push the full 39-slot list (empty inputs/result + the player inventory).
	t.sendGrindstoneContent(p)
	return true
}

// grindstoneMenuItems builds the 39-slot ContainerSetContent list: input0 (0), input1 (1), result (2),
// then the player inventory — main 3..29 (window slots 9..35), hotbar 30..38 (window slots 36..44).
// Mirrors GrindstoneMenu's addSlot(0)+addSlot(1)+addSlot(result 2)+addStandardInventorySlots order.
func grindstoneMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, grindstoneMenuSize)
	out[0] = oc.grind0
	out[1] = oc.grind1
	out[2] = oc.grindResult
	for i := 0; i < 27; i++ {
		out[3+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[3+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendGrindstoneContent pushes the authoritative ContainerSetContent for the open grindstone window (the
// initMenu → broadcastChanges full-slot sync + the resend after any click). Bumps the player inventory's
// state id, exactly like sendMerchantContent.
func (t *TickLoop) sendGrindstoneContent(p *tickPlayer) {
	if p.client == nil || p.openContainer == nil || p.openContainer.kind != containerKindGrindstone {
		return
	}
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(int32(p.openContainer.windowID), inv.stateID,
		grindstoneMenuItems(p.openContainer, inv), inv.getCarried()))
}

// grindstoneMayPlace ports the two input slots' mayPlace: itemStack.isDamageableItem() ||
// EnchantmentHelper.hasAnyEnchantments(itemStack).
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu (repairSlots slot ctor mayPlace)
func grindstoneMayPlace(s component.SlotData) bool {
	if stackEmpty(s) {
		return true // an empty stack is always placeable (Slot.mayPlace on removal path)
	}
	return stackIsDamageableItem(s) || stackHasAnyEnchantments(s)
}

// grindstoneCreateResult ports GrindstoneMenu.createResult → computeResult over the two inputs, writing
// the virtual result slot. Called after every input change.
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.createResult
func (t *TickLoop) grindstoneCreateResult(oc *openContainer) {
	oc.grindResult = grindstoneComputeResult(oc.grind0, oc.grind1)
}

// grindstoneComputeResult ports GrindstoneMenu.computeResult(input, additional).
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.computeResult
func grindstoneComputeResult(input, additional component.SlotData) component.SlotData {
	hasAnItem := !stackEmpty(input) || !stackEmpty(additional)
	if !hasAnItem {
		return component.SlotData{Count: 0} // EMPTY
	}
	if int(input.Count) > 1 || int(additional.Count) > 1 {
		return component.SlotData{Count: 0} // EMPTY (grindstone only accepts single items)
	}
	hasBothItems := !stackEmpty(input) && !stackEmpty(additional)
	if !hasBothItems {
		item := input
		if stackEmpty(input) {
			item = additional
		}
		if !stackHasAnyEnchantments(item) {
			return component.SlotData{Count: 0} // EMPTY (a plain single item has no result)
		}
		return removeNonCursesFrom(item) // DISENCHANT (item.copy() -> strip non-curses)
	}
	return grindstoneMergeItems(input, additional) // REPAIR
}

// grindstoneMergeItems ports GrindstoneMenu.mergeItems(input, additional).
//
//	if (!input.is(additional.getItem())) return EMPTY;
//	int durability = max(input.maxDamage, additional.maxDamage);
//	int remaining1 = input.maxDamage - input.damageValue;
//	int remaining2 = additional.maxDamage - additional.damageValue;
//	int remaining = remaining1 + remaining2 + durability * 5 / 100;
//	int count = 1;
//	if (!input.isDamageableItem()) { if (input.maxStackSize < 2 || !ItemStack.matches(input, additional)) return EMPTY; count = 2; }
//	newItem = input.copyWithCount(count);
//	if (newItem.isDamageableItem()) { newItem.set(MAX_DAMAGE, durability); newItem.setDamageValue(max(durability - remaining, 0)); }
//	mergeEnchantsFrom(newItem, additional); return removeNonCursesFrom(newItem);
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.mergeItems
func grindstoneMergeItems(input, additional component.SlotData) component.SlotData {
	if input.ItemID != additional.ItemID {
		return component.SlotData{Count: 0} // !input.is(additional.getItem())
	}
	durability := max(stackMaxDamage(input), stackMaxDamage(additional))
	remaining1 := stackMaxDamage(input) - stackDamageValue(input)
	remaining2 := stackMaxDamage(additional) - stackDamageValue(additional)
	remaining := remaining1 + remaining2 + durability*5/100
	count := 1
	if !stackIsDamageableItem(input) {
		// Non-damageable merge (e.g. two enchanted books): only when stackable-to-2 and ItemStack.matches
		// (equal count + same item + same components). slotDataEqual is exactly ItemStack.matches.
		if stackMaxSize(input) < 2 || !slotDataEqual(input, additional) {
			return component.SlotData{Count: 0}
		}
		count = 2
	}
	newItem := stackCopyWithCount(input, count)
	if stackIsDamageableItem(newItem) {
		newItem = setStackMaxDamage(newItem, durability)
		dmg := durability - remaining
		if dmg < 0 {
			dmg = 0
		}
		newItem = setStackDamageValue(newItem, dmg)
	}
	newItem = grindstoneMergeEnchantsFrom(newItem, additional)
	return removeNonCursesFrom(newItem)
}

// grindstoneMergeEnchantsFrom ports GrindstoneMenu.mergeEnchantsFrom(target, source):
//
//	updateEnchantments(target, newEnch -> { for (ench, lvl) in getEnchantmentsForCrafting(source):
//	    if (ench.is(CURSE) && newEnch.getLevel(ench) != 0) continue; newEnch.upgrade(ench, lvl); }})
//
// upgrade(ench, lvl) sets the level to max(existing, lvl). The curse guard skips a source curse only when
// the target ALREADY has that curse (avoids stacking a duplicate curse). Writes back to the target's
// crafting enchantment component (ENCHANTMENTS for gear).
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.mergeEnchantsFrom
func grindstoneMergeEnchantsFrom(target, source component.SlotData) component.SlotData {
	src := stackEnchantmentsForCrafting(source)
	if len(src) == 0 {
		return target
	}
	compType := enchantComponentType(target)
	p := component.DecodePatch(target)

	// Load target's current enchants into a level map (id -> level).
	levels := map[int]int{}
	var order []int
	switch e := p.Get(compType).(type) {
	case *component.Enchantments:
		for _, en := range e.Enchantments {
			levels[int(en.ID)] = int(en.Level)
			order = append(order, int(en.ID))
		}
	case *component.StoredEnchantments:
		for _, en := range e.Enchantments {
			levels[int(en.ID)] = int(en.Level)
			order = append(order, int(en.ID))
		}
	}
	for _, en := range src {
		id := int(en.ID)
		lvl := int(en.Level)
		if enchantIsCurse(id) && levels[id] != 0 {
			continue // curse already present on the target: skip (no curse stacking)
		}
		if _, ok := levels[id]; !ok {
			order = append(order, id) // upgrade of a new enchant
		}
		if lvl > levels[id] { // Mutable.upgrade: newLevel = max(existing, lvl)
			levels[id] = lvl
		}
	}
	merged := make([]component.EnchantmentEntry, 0, len(order))
	for _, id := range order {
		merged = append(merged, component.EnchantmentEntry{ID: pk.VarInt(id), Level: pk.VarInt(levels[id])})
	}
	if len(merged) > 0 {
		if compType == compStoredEnchantments {
			p.Set(compType, &component.StoredEnchantments{Enchantments: merged})
		} else {
			p.Set(compType, &component.Enchantments{Enchantments: merged})
		}
	}
	return p.ApplyTo(target)
}

// grindstoneExperienceFromItem ports GrindstoneMenu.getExperienceFromItem(item): sum getMinCost(level)
// over the item's non-curse crafting enchantments.
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.getExperienceFromItem
func grindstoneExperienceFromItem(s component.SlotData) int {
	amount := 0
	for _, en := range stackEnchantmentsForCrafting(s) {
		if enchantIsCurse(int(en.ID)) {
			continue
		}
		amount += enchantMinCost(int(en.ID), int(en.Level))
	}
	return amount
}

// grindstoneExperienceAmount ports GrindstoneMenu.getExperienceAmount(level): amount = sum
// getExperienceFromItem over the two inputs; if amount>0, half=ceil(amount/2), return half+random.nextInt(half).
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.getExperienceAmount
func (t *TickLoop) grindstoneExperienceAmount(oc *openContainer) int {
	amount := grindstoneExperienceFromItem(oc.grind0)
	amount += grindstoneExperienceFromItem(oc.grind1)
	if amount > 0 {
		half := int(math.Ceil(float64(amount) / 2.0))
		return half + t.grindstoneRandomInt(half)
	}
	return 0
}

// grindstoneRandomInt draws level.getRandom().nextInt(bound) from the current region's seeded
// levelRandom (the same source the furnace XP roll uses). A missing region random yields 0 (no bonus),
// keeping XP deterministic + panic-free in tests without a region. bound<=0 yields 0 (nextInt guard).
func (t *TickLoop) grindstoneRandomInt(bound int) int {
	if bound <= 0 {
		return 0
	}
	if r := t.cur(); r != nil && r.levelRandom != nil {
		return int(r.levelRandom.NextIntN(int32(bound)))
	}
	return 0
}

// closeGrindstoneWindow ports GrindstoneMenu.removed → clearContainer(player, repairSlots): the two
// inputs are returned to the player inventory (invAdd; overflow → playerDrop as an ItemEntity), NOT
// persisted. The result is virtual (never returned). Called from handleContainerClose.
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.removed
func (t *TickLoop) closeGrindstoneWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	for _, s := range []component.SlotData{oc.grind0, oc.grind1} {
		if stackEmpty(s) {
			continue
		}
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.grind0 = component.SlotData{Count: 0}
	oc.grind1 = component.SlotData{Count: 0}
	oc.grindResult = component.SlotData{Count: 0}
}
