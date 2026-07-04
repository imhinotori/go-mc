package server

// enchant_helper.go — the ItemStack enchantment + durability reads/edits the GRINDSTONE and SMITHING
// menus port 1:1 from the jar (EnchantmentHelper + ItemStack), expressed over this server's SlotData
// component model (level/component.Patch decode/rebuild over RawComponents).
//
// 1:1 jar surfaces (temp/cache/26.2-inner.jar, CFR/javap this session), cited per function:
//   net.minecraft.world.item.enchantment.EnchantmentHelper.hasAnyEnchantments
//   net.minecraft.world.item.enchantment.EnchantmentHelper.getEnchantmentsForCrafting / getComponentType
//   net.minecraft.world.item.enchantment.Enchantment.getMinCost (Enchantment$Cost.calculate)
//   net.minecraft.world.item.ItemStack.isDamageableItem / getMaxDamage / getDamageValue
//   net.minecraft.world.inventory.GrindstoneMenu.removeNonCursesFrom (the enchant strip + book transmute + repair-cost)
//   net.minecraft.world.inventory.AnvilMenu.calculateIncreasedRepairCost
//
// Tick-owned: every function is a pure read/transform over a value SlotData (no shared mutable state);
// the enchant-cost table + curse set are lazily loaded once from registrydata (the same embedded
// datapack content the server sends), then read-only.

import (
	"sync"

	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/server/registrydata"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Component wire type ids (level/component.NewComponent registry) the enchant/durability reads address.
// These are the stable protocol_id values from the generated component registry.
const (
	compEnchantments       = 13 // minecraft:enchantments
	compStoredEnchantments = 42 // minecraft:stored_enchantments
	compMaxDamage          = 2  // minecraft:max_damage
	compDamage             = 3  // minecraft:damage
	compUnbreakable        = 4  // minecraft:unbreakable
	compRepairCost         = 19 // minecraft:repair_cost
)

// enchantCostOnce lazily loads the per-enchantment min-cost table + the curse set from registrydata
// (the same embedded enchantment registry content the server sends to the client). Indexed by the
// enchantment WIRE id (the registry index == EnchantmentOrder index == the EnchantmentEntry.ID on the
// wire). On a load error the table stays empty (grindstone XP degrades to 0, never a panic).
var (
	enchantCostOnce  sync.Once
	enchantCostTable []registrydata.EnchantmentCost
	enchantCurseSet  map[int]bool
)

func enchantCosts() ([]registrydata.EnchantmentCost, map[int]bool) {
	enchantCostOnce.Do(func() {
		costs, curses, err := registrydata.EnchantmentCosts()
		if err != nil {
			enchantCurseSet = map[int]bool{}
			return // leave the table empty (XP -> 0, never a panic)
		}
		enchantCostTable = costs
		enchantCurseSet = curses
	})
	return enchantCostTable, enchantCurseSet
}

// enchantIsCurse reports whether an enchantment WIRE id is in #minecraft:enchantment/curse.
//
// 1:1 Holder<Enchantment>.is(EnchantmentTags.CURSE)
func enchantIsCurse(wireID int) bool {
	_, curses := enchantCosts()
	return curses[wireID]
}

// enchantMinCost ports Enchantment.getMinCost(level) = definition.minCost().calculate(level) =
// base + perLevelAboveFirst*(level-1). An unknown wire id (table gap) yields 0.
//
// 1:1 net.minecraft.world.item.enchantment.Enchantment.getMinCost + Enchantment$Cost.calculate
func enchantMinCost(wireID, level int) int {
	costs, _ := enchantCosts()
	if wireID < 0 || wireID >= len(costs) {
		return 0
	}
	c := costs[wireID]
	return c.Base + c.PerLevelAboveFirst*(level-1)
}

// enchantComponentType ports EnchantmentHelper.getComponentType(stack): an enchanted book carries its
// enchantments under STORED_ENCHANTMENTS; every other item under ENCHANTMENTS.
//
// 1:1 net.minecraft.world.item.enchantment.EnchantmentHelper.getComponentType
func enchantComponentType(s component.SlotData) int32 {
	if int(s.ItemID) == itemID("minecraft:enchanted_book") {
		return compStoredEnchantments
	}
	return compEnchantments
}

// stackEnchantmentsForCrafting ports EnchantmentHelper.getEnchantmentsForCrafting(stack): the entry list
// of the item's crafting enchantment component (STORED for books, ENCHANTMENTS otherwise), or nil when
// absent/empty.
//
// 1:1 net.minecraft.world.item.enchantment.EnchantmentHelper.getEnchantmentsForCrafting
func stackEnchantmentsForCrafting(s component.SlotData) []component.EnchantmentEntry {
	if stackEmpty(s) {
		return nil
	}
	p := component.DecodePatch(s)
	if e, ok := p.Get(enchantComponentType(s)).(*component.Enchantments); ok {
		return e.Enchantments
	}
	return nil
}

// stackHasAnyEnchantments ports EnchantmentHelper.hasAnyEnchantments(stack): non-empty ENCHANTMENTS OR
// non-empty STORED_ENCHANTMENTS.
//
// 1:1 net.minecraft.world.item.enchantment.EnchantmentHelper.hasAnyEnchantments
func stackHasAnyEnchantments(s component.SlotData) bool {
	if stackEmpty(s) {
		return false
	}
	p := component.DecodePatch(s)
	if e, ok := p.Get(compEnchantments).(*component.Enchantments); ok && len(e.Enchantments) > 0 {
		return true
	}
	if e, ok := p.Get(compStoredEnchantments).(*component.StoredEnchantments); ok && len(e.Enchantments) > 0 {
		return true
	}
	return false
}

// stackComponentInt reads a single-VarInt component value (DAMAGE/MAX_DAMAGE/REPAIR_COST) off a stack,
// returning (value, present). These components share the `struct{ pk.VarInt }` shape in level/component.
func stackComponentInt(s component.SlotData, typeID int32) (int, bool) {
	p := component.DecodePatch(s)
	switch typeID {
	case compMaxDamage:
		if c, ok := p.Get(typeID).(*component.MaxDamage); ok {
			return int(c.VarInt), true
		}
	case compDamage:
		if c, ok := p.Get(typeID).(*component.Damage); ok {
			return int(c.VarInt), true
		}
	case compRepairCost:
		if c, ok := p.Get(typeID).(*component.RepairCost); ok {
			return int(c.VarInt), true
		}
	}
	return 0, false
}

// stackIsDamageableItem ports ItemStack.isDamageableItem(): has(MAX_DAMAGE) && !has(UNBREAKABLE) &&
// has(DAMAGE). In this server's SlotData model, a damageable item carries the explicit MAX_DAMAGE +
// DAMAGE components (as vanilla-created gear does); a bare stack lacking them is treated as
// non-damageable — the faithful reading given the stack literally lacks those components.
//
// 1:1 net.minecraft.world.item.ItemStack.isDamageableItem
func stackIsDamageableItem(s component.SlotData) bool {
	if stackEmpty(s) {
		return false
	}
	p := component.DecodePatch(s)
	return p.Has(compMaxDamage) && !p.Has(compUnbreakable) && p.Has(compDamage)
}

// stackMaxDamage / stackDamageValue are defined in enchantment_logic.go (anvil path) — shared here.

// setStackMaxDamage / setStackDamageValue / setStackRepairCost write a single-VarInt component onto a
// stack (ItemStack.set(DataComponents.X, v)), returning the updated stack (value copy).
func setStackMaxDamage(s component.SlotData, v int) component.SlotData {
	p := component.DecodePatch(s)
	p.Set(compMaxDamage, &component.MaxDamage{VarInt: pk.VarInt(v)})
	return p.ApplyTo(s)
}

func setStackDamageValue(s component.SlotData, v int) component.SlotData {
	p := component.DecodePatch(s)
	p.Set(compDamage, &component.Damage{VarInt: pk.VarInt(v)})
	return p.ApplyTo(s)
}

func setStackRepairCost(s component.SlotData, v int) component.SlotData {
	p := component.DecodePatch(s)
	p.Set(compRepairCost, &component.RepairCost{VarInt: pk.VarInt(v)})
	return p.ApplyTo(s)
}

// removeNonCursesFrom ports GrindstoneMenu.removeNonCursesFrom(item):
//
//	newEnchantments = updateEnchantments(item, e -> e.removeIf(!ench.is(CURSE)))  // keep only curses
//	if (item.is(ENCHANTED_BOOK) && newEnchantments.isEmpty()) item = item.transmuteCopy(BOOK)
//	repairCost = 0; for i in 0..newEnchantments.size(): repairCost = calculateIncreasedRepairCost(repairCost)
//	item.set(REPAIR_COST, repairCost)
//	return item
//
// updateEnchantments writes back to the SAME component slot getComponentType selects (STORED for a book,
// ENCHANTMENTS otherwise). The removeIf keeps only curse enchants. If a now-empty enchanted book results,
// it transmutes to a plain book (item id swap; the component patch otherwise carries over — vanilla's
// transmuteCopy preserves the patch minus the enchantment component, which is already emptied here).
//
// 1:1 net.minecraft.world.inventory.GrindstoneMenu.removeNonCursesFrom
func removeNonCursesFrom(s component.SlotData) component.SlotData {
	compType := enchantComponentType(s)
	p := component.DecodePatch(s)

	// updateEnchantments -> removeIf(!ench.is(CURSE)): keep only curse enchants.
	var kept []component.EnchantmentEntry
	switch e := p.Get(compType).(type) {
	case *component.Enchantments:
		for _, en := range e.Enchantments {
			if enchantIsCurse(int(en.ID)) {
				kept = append(kept, en)
			}
		}
	case *component.StoredEnchantments:
		for _, en := range e.Enchantments {
			if enchantIsCurse(int(en.ID)) {
				kept = append(kept, en)
			}
		}
	}
	// Write the kept-enchants component back (or drop it when empty, matching an EMPTY ItemEnchantments).
	if len(kept) > 0 {
		if compType == compStoredEnchantments {
			p.Set(compType, &component.StoredEnchantments{Enchantments: kept})
		} else {
			p.Set(compType, &component.Enchantments{Enchantments: kept})
		}
	} else {
		p.Remove(compType)
	}
	out := p.ApplyTo(s)

	// Enchanted book with no remaining enchantments -> transmute to a plain book.
	if int(out.ItemID) == itemID("minecraft:enchanted_book") && len(kept) == 0 {
		out.ItemID = pk.VarInt(itemID("minecraft:book"))
	}

	// repairCost accumulation: one calculateIncreasedRepairCost step per remaining enchantment.
	repairCost := 0
	for i := 0; i < len(kept); i++ {
		repairCost = calculateIncreasedRepairCost(repairCost)
	}
	out = setStackRepairCost(out, repairCost)
	return out
}

// calculateIncreasedRepairCost is defined in anvil_result.go (anvil path) — shared here.

// itemID resolves a namespaced item name to its numeric id via registryid.Item (the slice index == id),
// or -1 if absent. Cached lazily. Used for the enchanted_book/book identity checks.
var (
	itemIDOnce  sync.Once
	itemIDTable map[string]int
)

func itemID(name string) int {
	itemIDOnce.Do(func() {
		itemIDTable = make(map[string]int, len(registryid.Item))
		for id, n := range registryid.Item {
			itemIDTable[n] = id
		}
	})
	if id, ok := itemIDTable[name]; ok {
		return id
	}
	return -1
}
