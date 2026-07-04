package server

// anvil_test.go — AnvilMenu validation gates. Each asserts the ported cost math against the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, net.minecraft.world.inventory.AnvilMenu.
// createResult / onTake / setItemName). The enchant-merge cost, the prior-work penalty, the rename
// cost 1, and the 40-level too-expensive cap are all jar-verified.

import (
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// mkEnchantedBook builds an enchanted_book SlotData carrying a minecraft:stored_enchantments component
// with the given (resource id -> level) entries (the anvil merge reads getEnchantmentsForCrafting,
// which for a book is the stored_enchantments component). It uses editStack (the same RawComponents
// encoder the anvil result uses) so the component wire form is exactly what the server produces.
func mkEnchantedBook(enchants map[string]int) component.SlotData {
	base := component.SlotData{ItemID: pk.VarInt(itemNameToID("enchanted_book")), Count: 1}
	e := editStack(base)
	e.setStoredEnchantments(enchants)
	return e.materialize()
}

// mkDamageableSword builds a diamond_sword carrying max_damage + damage + enchantable + repairable +
// enchantments components so isDamageableItem / isEnchantable / canStoreEnchantments read real values
// (the v1 component seam: a stack behaves fully when it actually carries the components). The
// enchantments map may be empty (a fresh, enchantable sword).
func mkEnchantableSword(damage int, enchants map[string]int) component.SlotData {
	base := component.SlotData{ItemID: pk.VarInt(itemNameToID("diamond_sword")), Count: 1}
	e := editStack(base)
	e.setInt(enchTypeMaxDamage, 1561) // diamond tool durability
	e.setInt(enchTypeDamage, damage)
	e.set(enchTypeEnchantable, &component.Enchantable{VarInt: pk.VarInt(10)}) // diamond enchantability
	if len(enchants) > 0 {
		e.setEnchantments(enchants)
	} else {
		// isEnchantable requires the ENCHANTMENTS component to be EMPTY (present-but-empty), which is
		// vanilla's default component for an enchantable item. Set an empty enchantments component.
		e.setEnchantments(map[string]int{})
	}
	return e.materialize()
}

// anvilTestLoop builds a TickLoop + a creative-off player at a fixed level, with an OPEN anvil window.
func anvilTestLoop(t *testing.T, xpLevel int32) (*TickLoop, *tickPlayer, *openContainer) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{entityID: 1, gameMode: gameModeSurvival, experienceLevel: xpLevel, client: nil}
	oc := &openContainer{windowID: 1, kind: containerKindAnvil, anvilPos: pk.Position{X: 0, Y: 64, Z: 0}}
	p.openContainer = oc
	loop.players = append(loop.players, p)
	return loop, p, oc
}

// TestAnvilMergeTwoEnchantedBooks: combining a Sharpness-III book with a Sharpness-III book yields a
// Sharpness-IV book. The merge cost is jar-verified: for equal levels the merged level is level+1
// (III+III -> IV), the anvil fee is anvilCost(sharpness=1) * mergedLevel(4) = 4. No prior-work tax
// (both books repairCost 0), no rename. CITE AnvilMenu.createResult enchant-merge branch.
func TestAnvilMergeTwoEnchantedBooks(t *testing.T) {
	loop, p, oc := anvilTestLoop(t, 30)
	oc.anvilInput = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})
	oc.anvilAdd = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})

	loop.anvilCreateResult(p, oc)

	if stackEmpty(oc.anvilResult) {
		t.Fatal("result empty after merging two sharpness-III books")
	}
	merged := stackStoredEnchantments(oc.anvilResult)
	if merged["minecraft:sharpness"] != 4 {
		t.Fatalf("merged sharpness level = %d, want 4 (III+III)", merged["minecraft:sharpness"])
	}
	// fee = anvilCost(sharpness)=1 * mergedLevel=4, both books usingBook so fee = max(1, 1/2)=1 -> 1*4=4.
	// tax 0 (both repairCost 0), no rename -> cost 4.
	if oc.anvilCost != 4 {
		t.Fatalf("anvil cost = %d, want 4 (sharpness book merge III+III -> IV, book-halved fee)", oc.anvilCost)
	}
}

// TestAnvilRenameOnlyCostsOne: renaming a fresh (unnamed) enchantable sword with no additional item
// costs exactly 1 (COST_RENAME), sets onlyRenaming, and applies the custom_name to the result. CITE
// AnvilMenu.createResult naming branch + the namingCost==price onlyRenaming path.
func TestAnvilRenameOnlyCostsOne(t *testing.T) {
	loop, p, oc := anvilTestLoop(t, 30)
	oc.anvilInput = mkEnchantableSword(0, nil)
	oc.anvilNameSet = true
	oc.anvilName = "Excalibur"

	loop.anvilCreateResult(p, oc)

	if stackEmpty(oc.anvilResult) {
		t.Fatal("result empty after a rename")
	}
	if oc.anvilCost != 1 {
		t.Fatalf("rename cost = %d, want 1 (COST_RENAME)", oc.anvilCost)
	}
	if !oc.anvilOnlyRenaming {
		t.Fatal("onlyRenaming not set for a pure rename (namingCost == price)")
	}
	name, ok := stackCustomName(oc.anvilResult)
	if !ok || name != "Excalibur" {
		t.Fatalf("result custom_name = %q (ok=%v), want \"Excalibur\"", name, ok)
	}
}

// TestAnvilFortyCapBlocksNonCreative: a merge whose cost reaches >= 40 produces an EMPTY result for a
// non-creative player (the "Too Expensive!" gate). We force the cost to 40 via input.count > 1 (the
// vanilla `price = 40` when combining a stack). CITE AnvilMenu.createResult (cost >= 40 &&
// !creative -> result EMPTY).
func TestAnvilFortyCapBlocksNonCreative(t *testing.T) {
	loop, p, oc := anvilTestLoop(t, 40)
	// input.count > 1 forces price = 40 in the enchant-merge branch (an enchant must merge to reach it).
	input := mkEnchantableSword(0, map[string]int{})
	input.Count = 2 // > 1 -> price = 40
	oc.anvilInput = input
	oc.anvilAdd = mkEnchantedBook(map[string]int{"minecraft:sharpness": 1})

	loop.anvilCreateResult(p, oc)

	if oc.anvilCost < 40 {
		t.Fatalf("expected cost >= 40 (input count > 1 forces price=40), got %d", oc.anvilCost)
	}
	if !stackEmpty(oc.anvilResult) {
		t.Fatal("result should be EMPTY at cost >= 40 for a non-creative player (Too Expensive gate)")
	}
}

// TestAnvilOnTakeConsumesLevels: taking the anvil result consumes exactly `cost` experience levels
// (giveExperienceLevels(-cost)) and clears the input slot. CITE AnvilMenu.onTake.
func TestAnvilOnTakeConsumesLevels(t *testing.T) {
	loop, p, oc := anvilTestLoop(t, 30)
	oc.anvilInput = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})
	oc.anvilAdd = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})
	loop.anvilCreateResult(p, oc)
	cost := oc.anvilCost
	if cost <= 0 {
		t.Fatalf("precondition: cost should be > 0, got %d", cost)
	}
	levelBefore := p.experienceLevel

	loop.onTakeAnvil(p, oc)

	if p.experienceLevel != levelBefore-int32(cost) {
		t.Fatalf("level after take = %d, want %d (consumed %d)", p.experienceLevel, levelBefore-int32(cost), cost)
	}
	if !stackEmpty(oc.anvilInput) {
		t.Fatal("input slot not cleared after take (AnvilMenu.onTake sets input 0 EMPTY)")
	}
	if oc.anvilCost != 0 {
		t.Fatalf("cost after take = %d, want 0 (onTake resets cost)", oc.anvilCost)
	}
}

// TestAnvilPriorWorkPenalty: after a merge, the result carries a repair_cost of
// calculateIncreasedRepairCost(max(input.repairCost, addition.repairCost)) = 2*base+1. Two books at
// repairCost 0 -> result repairCost 2*0+1 = 1. And the prior-work TAX adds the summed input repair
// costs to the next merge. CITE AnvilMenu.calculateIncreasedRepairCost + the tax accumulation.
func TestAnvilPriorWorkPenalty(t *testing.T) {
	loop, p, oc := anvilTestLoop(t, 30)
	oc.anvilInput = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})
	oc.anvilAdd = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})

	loop.anvilCreateResult(p, oc)
	rc := stackRepairCost(oc.anvilResult)
	if rc != 1 {
		t.Fatalf("result repair_cost = %d, want 1 (calculateIncreasedRepairCost(0) = 2*0+1)", rc)
	}

	// Now merge the result (repairCost 1) with another sharpness-III book (repairCost 0): the tax adds
	// 1 (the result's prior work), and the new repairCost = 2*1+1 = 3.
	oc.anvilInput = oc.anvilResult
	oc.anvilAdd = mkEnchantedBook(map[string]int{"minecraft:sharpness": 3})
	loop.anvilCreateResult(p, oc)
	if stackEmpty(oc.anvilResult) {
		t.Fatal("second merge produced an empty result")
	}
	rc2 := stackRepairCost(oc.anvilResult)
	if rc2 != 3 {
		t.Fatalf("second-merge repair_cost = %d, want 3 (calculateIncreasedRepairCost(1) = 2*1+1)", rc2)
	}
	// cost: input is Sharpness-IV, addition is Sharpness-III book. current(4) != entry(3) -> level =
	// max(3,4) = 4 (no upgrade). fee 1 (book-halved) * level 4 = 4, plus prior-work tax 1 = 5.
	if oc.anvilCost != 5 {
		t.Fatalf("second-merge cost = %d, want 5 (fee 1*4 + prior-work tax 1)", oc.anvilCost)
	}
}
