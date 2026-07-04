package server

// anvil_result.go — AnvilMenu.createResult, the anvil COST MATH (the heart of the anvil), ported
// method-for-method from net.minecraft.world.inventory.AnvilMenu.createResult (temp/cache/
// 26.2-inner.jar, CFR this session). Every numeric op — the repair-unit cost, the durability
// transfer (maxDamage*12/100), the enchant-merge fee (anvilCost * level, halved for a book), the
// prior-work penalty (2^k-1 via calculateIncreasedRepairCost), the rename cost 1, the 40-level cap,
// the incompatible-enchant +1 penalties — matches the bytecode exactly.
//
// 1:1 net.minecraft.world.inventory.AnvilMenu.createResult / calculateIncreasedRepairCost.

import (
	"sort"

	"github.com/imhinotori/sulfur/level/component"
)

// anvilCreateResult ports AnvilMenu.createResult(): recompute the result slot + the cost DataSlot
// from the two input slots + the pending rename. Sets oc.anvilResult, oc.anvilCost,
// oc.anvilRepairUnits (repairItemCountCost), oc.anvilOnlyRenaming. Mirrors the CFR exactly.
func (t *TickLoop) anvilCreateResult(p *tickPlayer, oc *openContainer) {
	input := oc.anvilInput
	oc.anvilOnlyRenaming = false
	oc.anvilCost = 1 // this.cost.set(1)
	price := 0       // int price
	var tax int64    // long l (the accumulated prior-work tax)
	namingCost := 0  // int namingCost
	oc.anvilRepairUnits = 0

	// if (input.isEmpty() || !canStoreEnchantments(input)) { result=EMPTY; cost=0; return }
	if stackEmpty(input) || !canStoreEnchantments(input) {
		oc.anvilResult = component.SlotData{Count: 0}
		oc.anvilCost = 0
		return
	}

	// ItemStack result = input.copy();
	result := input
	addition := oc.anvilAdd
	// ItemEnchantments.Mutable enchantments = new Mutable(getEnchantmentsForCrafting(result));
	enchantments := copyEnchantMap(enchantmentsForCrafting(result))

	// l += input.repairCost + addition.repairCost;
	tax += int64(stackRepairCost(input)) + int64(stackRepairCost(addition))

	// result damage tracking (mutate a local so we can re-serialize the repaired damage onto result).
	resultDamage := stackDamageValue(result)
	resultMaxDamage := stackMaxDamage(result)
	resultDamageChanged := false

	if !stackEmpty(addition) {
		usingBook := stackHasComponent(addition, enchTypeStoredEnchantments)

		// if (result.isDamageableItem() && input.isValidRepairItem(addition)) { repair-by-material }
		if isDamageableItem(result) && anvilIsValidRepairItem(input, addition) {
			// int repairAmount = Math.min(result.getDamageValue(), result.getMaxDamage()/4);
			repairAmount := min(resultDamage, resultMaxDamage/4)
			if repairAmount <= 0 {
				oc.anvilResult = component.SlotData{Count: 0}
				oc.anvilCost = 0
				return
			}
			// for (count=0; repairAmount>0 && count<addition.count; ++count) { damage -= repairAmount;
			//     ++price; repairAmount = min(damage, maxDamage/4); }
			count := 0
			for repairAmount > 0 && count < int(addition.Count) {
				resultDamage = resultDamage - repairAmount
				resultDamageChanged = true
				price++
				repairAmount = min(resultDamage, resultMaxDamage/4)
				count++
			}
			oc.anvilRepairUnits = count // repairItemCountCost
		} else {
			// if (!(usingBook || result.is(addition.getItem()) && result.isDamageableItem())) { invalid }
			if !(usingBook || (int(result.ItemID) == int(addition.ItemID) && isDamageableItem(result))) {
				oc.anvilResult = component.SlotData{Count: 0}
				oc.anvilCost = 0
				return
			}
			// if (result.isDamageableItem() && !usingBook) { combine-two-items durability transfer }
			if isDamageableItem(result) && !usingBook {
				// remaining1 = input.maxDamage - input.damageValue; remaining2 = addition.maxDamage - addition.damageValue;
				remaining1 := stackMaxDamage(input) - stackDamageValue(input)
				remaining2 := stackMaxDamage(addition) - stackDamageValue(addition)
				// additional = remaining2 + result.maxDamage*12/100; remaining = remaining1 + additional;
				additional := remaining2 + resultMaxDamage*12/100
				remaining := remaining1 + additional
				// int resultDamageV = result.maxDamage - remaining; if (<0) 0;
				resultDamageV := resultMaxDamage - remaining
				if resultDamageV < 0 {
					resultDamageV = 0
				}
				// if (resultDamageV < result.getDamageValue()) { setDamageValue(resultDamageV); price += 2; }
				if resultDamageV < resultDamage {
					resultDamage = resultDamageV
					resultDamageChanged = true
					price += 2
				}
			}

			// enchant merge loop over additionalEnchantments.
			additionalEnchantments := enchantmentsForCrafting(addition)
			isAnyCompatible := false
			isAnyNotCompatible := false
			for _, entry := range sortedEnchantEntries(additionalEnchantments) {
				enchID := entry.id
				additionalLevel := entry.level
				current := enchantments[enchID] // getLevel(holder) (0 if absent)
				// level = (current == entryLevel) ? entryLevel+1 : max(entryLevel, current);
				var level int
				if current == additionalLevel {
					level = additionalLevel + 1
				} else {
					level = max(additionalLevel, current)
				}
				// boolean compatible = enchantment.canEnchant(input);
				compatible := enchantCanEnchant(enchID, input)
				// if (creative || input.is(ENCHANTED_BOOK)) compatible = true;
				if playerHasInfiniteMaterials(p) || int32(input.ItemID) == itemNameToID("enchanted_book") {
					compatible = true
				}
				// for (other : enchantments.keySet()) { if (other==this || areCompatible) continue;
				//     compatible=false; ++price; }
				for _, other := range sortedEnchantKeys(enchantments) {
					if other == enchID || enchantAreCompatible(enchID, other) {
						continue
					}
					compatible = false
					price++
				}
				if !compatible {
					isAnyNotCompatible = true
					continue
				}
				isAnyCompatible = true
				// if (level > enchantment.getMaxLevel()) level = getMaxLevel();
				if level > enchantMaxLevelOf(enchID) {
					level = enchantMaxLevelOf(enchID)
				}
				enchantments[enchID] = level
				// int fee = enchantment.getAnvilCost(); if (usingBook) fee = max(1, fee/2);
				fee := enchantAnvilCost(enchID)
				if usingBook {
					fee = max(1, fee/2)
				}
				// price += fee * level; if (input.count > 1) price = 40;
				price += fee * level
				if int(input.Count) > 1 {
					price = anvilMaximumCost
				}
			}
			// if (isAnyNotCompatible && !isAnyCompatible) { invalid }
			if isAnyNotCompatible && !isAnyCompatible {
				oc.anvilResult = component.SlotData{Count: 0}
				oc.anvilCost = 0
				return
			}
		}
	}

	// rename cost + custom_name application.
	// if (itemName == null || isBlank(itemName)) { if (input.has(CUSTOM_NAME)) { namingCost=1; price+=1; result.remove(CUSTOM_NAME) } }
	// else if (!itemName.equals(input.getHoverName().getString())) { namingCost=1; price+=1; result.set(CUSTOM_NAME, literal(itemName)) }
	renameRemoveCustomName := false
	renameSetCustomName := false
	renameName := ""
	if !oc.anvilNameSet || isBlank(oc.anvilName) {
		if stackHasComponent(input, enchTypeCustomName) {
			namingCost = 1
			price += namingCost
			renameRemoveCustomName = true
		}
	} else if oc.anvilName != inputHoverName(input) {
		namingCost = 1
		price += namingCost
		renameSetCustomName = true
		renameName = oc.anvilName
	}

	// int finalPrice = price <= 0 ? 0 : (int)Mth.clamp(l + price, 0, Integer.MAX_VALUE);
	finalPrice := 0
	if price > 0 {
		finalPrice = int(mthClampLong(tax+int64(price), 0, int64(int32(^uint32(0)>>1))))
	}
	oc.anvilCost = finalPrice

	// if (price <= 0) result = ItemStack.EMPTY;
	resultEmpty := price <= 0

	// if (namingCost == price && namingCost > 0) { if (cost >= 40) cost = 39; onlyRenaming = true; }
	if namingCost == price && namingCost > 0 {
		if oc.anvilCost >= anvilMaximumCost {
			oc.anvilCost = anvilMaximumCost - 1 // 39
		}
		oc.anvilOnlyRenaming = true
	}

	// if (cost >= 40 && !creative) result = ItemStack.EMPTY;
	if oc.anvilCost >= anvilMaximumCost && !playerHasInfiniteMaterials(p) {
		resultEmpty = true
	}

	if resultEmpty {
		oc.anvilResult = component.SlotData{Count: 0}
		return
	}

	// Build the result stack: apply the repaired damage, the rename, the new repair_cost, and the
	// merged enchantments.
	edit := editStack(result)

	if resultDamageChanged {
		edit.setInt(enchTypeDamage, resultDamage)
	}
	if renameRemoveCustomName {
		edit.remove(enchTypeCustomName)
	}
	if renameSetCustomName {
		edit.setCustomName(renameName)
	}

	// int baseCost = result.repairCost; if (baseCost < addition.repairCost) baseCost = addition.repairCost;
	baseCost := stackRepairCost(input) // result inherits input's repair_cost (result = input.copy())
	if baseCost < stackRepairCost(addition) {
		baseCost = stackRepairCost(addition)
	}
	// if (namingCost != price || namingCost == 0) baseCost = calculateIncreasedRepairCost(baseCost);
	if namingCost != price || namingCost == 0 {
		baseCost = calculateIncreasedRepairCost(baseCost)
	}
	edit.setInt(enchTypeRepairCost, baseCost)

	// EnchantmentHelper.setEnchantments(result, enchantments.toImmutable()): write onto the active
	// component type (ENCHANTMENTS or STORED_ENCHANTMENTS for a book).
	if enchantComponentTypeIsStored(result) {
		edit.setStoredEnchantments(enchantments)
	} else {
		edit.setEnchantments(enchantments)
	}

	oc.anvilResult = edit.materialize()
}

// calculateIncreasedRepairCost ports AnvilMenu.calculateIncreasedRepairCost(baseCost): (int)Math.min
// (baseCost*2 + 1, Integer.MAX_VALUE). The 2^k-1 prior-work penalty. CITE AnvilMenu.
// calculateIncreasedRepairCost.
func calculateIncreasedRepairCost(baseCost int) int {
	v := int64(baseCost)*2 + 1
	if v > int64(int32(^uint32(0)>>1)) {
		return int(int32(^uint32(0) >> 1))
	}
	return int(v)
}

// anvilIsValidRepairItem ports ItemStack.isValidRepairItem(repairItem): the input's REPAIRABLE
// component lists the addition's item as a valid repair material. CITE ItemStack.isValidRepairItem
// -> Repairable.isValidRepairItem (repairItems.contains(repairItem.typeHolder)).
func anvilIsValidRepairItem(input, addition component.SlotData) bool {
	comp := stackComponent(input, enchTypeRepairable)
	if comp == nil {
		return false
	}
	rep, ok := comp.(*component.Repairable)
	if !ok {
		return false
	}
	return idSetContainsItem(rep.Items, int32(addition.ItemID))
}

// idSetContainsItem tests membership of an item id in an IDSet (a "#tag" name when Type==0, else an
// explicit id list). A tag is resolved through the item-tag membership (data/tag.ItemTags).
func idSetContainsItem(set component.IDSet, itemID int32) bool {
	if set.Type == 0 {
		// A tag reference: "#minecraft:foo" -> tag name "foo".
		return itemInTag(itemID, stripTagPrefix(string(set.Tag)))
	}
	for _, id := range set.IDs {
		if int32(id) == itemID {
			return true
		}
	}
	return false
}

// stripTagPrefix normalizes a "#minecraft:foo" / "minecraft:foo" tag reference to the bare "foo".
func stripTagPrefix(s string) string {
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	if len(s) >= len("minecraft:") && s[:len("minecraft:")] == "minecraft:" {
		s = s[len("minecraft:"):]
	}
	return s
}

// inputHoverName ports ItemStack.getHoverName().getString() for the anvil rename compare: the custom
// name if present, else "" (the plain item's translatable name is not compared byte-for-byte here —
// vanilla compares the rendered string, and a rename that only differs from the default name still
// costs 1; v1 treats a plain item with no custom_name as an empty hover name so any non-blank rename
// registers a change, matching the observable "renaming a fresh item costs 1"). CITE
// AnvilMenu.createResult itemName.equals(input.getHoverName().getString()).
func inputHoverName(input component.SlotData) string {
	if name, ok := stackCustomName(input); ok {
		return name
	}
	return ""
}

// isBlank ports net.minecraft.util.StringUtil.isBlank: null or all-whitespace.
func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// mthClampLong ports Mth.clamp(long, long, long).
func mthClampLong(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// copyEnchantMap clones a resource-id->level map (the Mutable enchantments working copy).
func copyEnchantMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// enchEntry is a (resource id, level) pair for deterministic iteration over an enchant map.
type enchEntry struct {
	id    string
	level int
}

// sortedEnchantEntries returns a map's entries sorted by resource id — a deterministic iteration
// order that stands in for the additionalEnchantments.entrySet() order (an Object2IntOpenHashMap
// whose iteration order is not observable on the wire; sorting gives a stable, well-defined pass).
func sortedEnchantEntries(m map[string]int) []enchEntry {
	out := make([]enchEntry, 0, len(m))
	for id, level := range m {
		out = append(out, enchEntry{id: id, level: level})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// sortedEnchantKeys returns a map's keys sorted (the enchantments.keySet() iteration for the
// cross-compat penalty loop).
func sortedEnchantKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
