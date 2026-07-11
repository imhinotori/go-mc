package server

// advancement_triggers_test.go — behaviour tests for the ported advancement triggers
// (advancements.go). Each test loads the embedded tree, gives a player a fresh
// PlayerAdvancements, fires a trigger, and asserts the matching criterion is granted
// (the vanilla-faithful predicate match). The grant path is the same
// grantAdvancementCriterion the login/inventory tests exercise.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// advTestPlayer builds a tick player with a fresh PlayerAdvancements registered on the loop.
func advTestPlayer(loop *TickLoop, id int32) *tickPlayer {
	p := combatPlayer(loop, id)
	p.advancements = newPlayerAdvancements()
	return p
}

// TestPlayerKilledEntityTriggerGrants asserts minecraft:player_killed_entity grants the
// adventure/kill_a_mob zombie criterion when a zombie is killed (entity_type predicate),
// and that a mob no criterion lists grants nothing.
func TestPlayerKilledEntityTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 40)

	loop.triggerPlayerKilledEntity(p, "minecraft:zombie")
	if _, ok := p.advancements.progress["minecraft:adventure/kill_a_mob"]["minecraft:zombie"]; !ok {
		t.Fatal("player_killed_entity(zombie) should grant adventure/kill_a_mob zombie criterion")
	}
	// A different mob does not grant the zombie criterion (predicate is per-type).
	if _, ok := p.advancements.progress["minecraft:adventure/kill_a_mob"]["minecraft:creeper"]; ok {
		// creeper is a separate criterion; killing a zombie must not grant it.
		t.Fatal("killing a zombie must not grant the creeper criterion")
	}
	// No panic on an entity id no advancement lists.
	loop.triggerPlayerKilledEntity(p, "minecraft:bat")
}

// TestPlacedBlockTriggerGrants asserts minecraft:placed_block grants husbandry/plant_seed
// when a wheat crop is placed (location.block predicate), and no-ops on a non-listed block.
func TestPlacedBlockTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 41)

	loop.triggerPlacedBlock(p, "minecraft:wheat")
	if _, ok := p.advancements.progress["minecraft:husbandry/plant_seed"]["wheat"]; !ok {
		t.Fatal("placed_block(wheat) should grant husbandry/plant_seed wheat criterion")
	}
	// A block no placed_block criterion lists grants nothing (no panic).
	loop.triggerPlacedBlock(p, "minecraft:stone")
}

// TestChangedDimensionTriggerGrants asserts minecraft:changed_dimension grants
// nether/root (to == the_nether) on a nether entry, and does NOT grant it on an
// end entry (to predicate mismatch).
func TestChangedDimensionTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()

	pNether := advTestPlayer(loop, 42)
	loop.triggerChangedDimension(pNether, "minecraft:overworld", "minecraft:the_nether")
	if _, ok := pNether.advancements.progress["minecraft:nether/root"]["entered_nether"]; !ok {
		t.Fatal("changed_dimension(to the_nether) should grant nether/root entered_nether")
	}

	pEnd := advTestPlayer(loop, 43)
	loop.triggerChangedDimension(pEnd, "minecraft:overworld", "minecraft:the_end")
	if _, ok := pEnd.advancements.progress["minecraft:nether/root"]["entered_nether"]; ok {
		t.Fatal("changed_dimension(to the_end) must NOT grant nether/root (to predicate is the_nether)")
	}
	// But it SHOULD grant story/enter_the_end (to == the_end).
	if _, ok := pEnd.advancements.progress["minecraft:story/enter_the_end"]["entered_end"]; !ok {
		t.Fatal("changed_dimension(to the_end) should grant story/enter_the_end entered_end")
	}
}

// TestSleptInBedTriggerGrants asserts minecraft:slept_in_bed unconditionally grants
// adventure/sleep_in_bed (empty predicate wildcard).
func TestSleptInBedTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 44)

	loop.triggerSleptInBed(p)
	if _, ok := p.advancements.progress["minecraft:adventure/sleep_in_bed"]["slept_in_bed"]; !ok {
		t.Fatal("slept_in_bed should grant adventure/sleep_in_bed slept_in_bed criterion")
	}
}

// TestTameAnimalTriggerGrants asserts minecraft:tame_animal unconditionally grants
// husbandry/tame_an_animal (empty predicate wildcard).
func TestTameAnimalTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 45)

	loop.triggerTameAnimal(p)
	if _, ok := p.advancements.progress["minecraft:husbandry/tame_an_animal"]["tamed_animal"]; !ok {
		t.Fatal("tame_animal should grant husbandry/tame_an_animal tamed_animal criterion")
	}
}

// TestConsumeItemTriggerGrants asserts minecraft:consume_item grants husbandry/root
// (empty item predicate = any consumable wildcard) and husbandry/balanced_diet's apple
// criterion when an apple is eaten (item.items predicate).
func TestConsumeItemTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 46)

	loop.triggerConsumeItem(p, "minecraft:apple")
	if _, ok := p.advancements.progress["minecraft:husbandry/root"]["consumed_item"]; !ok {
		t.Fatal("consume_item(apple) should grant husbandry/root consumed_item (wildcard)")
	}
	if _, ok := p.advancements.progress["minecraft:husbandry/balanced_diet"]["apple"]; !ok {
		t.Fatal("consume_item(apple) should grant husbandry/balanced_diet apple criterion")
	}
}

// TestFishingRodHookedTriggerGrants asserts minecraft:fishing_rod_hooked grants
// husbandry/fishy_business cod when a cod is caught (item.items predicate).
func TestFishingRodHookedTriggerGrants(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 47)

	loop.triggerFishingRodHooked(p, "minecraft:cod")
	if _, ok := p.advancements.progress["minecraft:husbandry/fishy_business"]["cod"]; !ok {
		t.Fatal("fishing_rod_hooked(cod) should grant husbandry/fishy_business cod criterion")
	}
	// A non-fish item grants nothing (no panic).
	loop.triggerFishingRodHooked(p, "minecraft:stick")
}

// TestInventoryChangedMenuFeedGrantsRoot asserts the broadcastInventoryChanges seam feeds
// minecraft:inventory_changed: a crafting_table appearing in a player slot grants story/root
// (the craft-into-inventory observable), mirroring ServerPlayer.inventoryChanged.
func TestInventoryChangedMenuFeedGrantsRoot(t *testing.T) {
	loop := &TickLoop{}
	loop.SetAdvancements()
	p := advTestPlayer(loop, 48)
	inv := ensureInventory(p)

	// Simulate a crafting_table landing in a main-inventory slot: snapshot BEFORE, mutate, broadcast.
	ctID := indexOf(registryid.Item, "minecraft:crafting_table")
	if ctID < 0 {
		t.Fatal("crafting_table item id not found in registry")
	}
	before := inv.snapshot()
	inv.set(9, component.SlotData{Count: 1, ItemID: pk.VarInt(ctID)}) // slot 9 = first main-inventory slot
	loop.broadcastInventoryChanges(p, inv, before)

	if _, ok := p.advancements.progress["minecraft:story/root"]["crafting_table"]; !ok {
		t.Fatal("a crafting_table entering inventory should grant story/root crafting_table via broadcastInventoryChanges")
	}
}
