package server

// furnace_fuel.go — the vanilla FUEL TABLE (the furnace block-entity's burn-duration source). A 1:1 port
// of net.minecraft.world.level.block.entity.FuelValues.vanillaBurnTimes (the default datapack fuel
// registry) over the 26.2 jar (temp/cache/26.2-inner.jar, CFR this session).
//
// JAR SOURCE (FuelValues.vanillaBurnTimes(registries, features, baseUnit=200) — CFR this session):
//
//	new Builder(...).add(LAVA_BUCKET, base*100).add(COAL_BLOCK, base*8*10).add(BLAZE_ROD, base*12)
//	  .add(COAL, base*8).add(CHARCOAL, base*8).add(#logs, base*3/2).add(#bamboo_blocks, base*3/2)
//	  .add(#planks, base*3/2).add(BAMBOO_MOSAIC, base*3/2).add(#wooden_stairs, base*3/2)
//	  .add(BAMBOO_MOSAIC_STAIRS, base*3/2).add(#wooden_slabs, base*3/4).add(BAMBOO_MOSAIC_SLAB, base*3/4)
//	  ... .add(STICK, base/2).add(#saplings, base/2).add(DRIED_KELP_BLOCK, 1+base*20) ...
//	  .add(LEAF_LITTER, base/2).remove(#non_flammable_wood).build();
//
// baseUnit = 200 (the FuelValues.vanillaBurnTimes(registries, features) → 200 overload). So COAL/CHARCOAL
// = 1600, COAL_BLOCK = 16000, BLAZE_ROD = 2400, LAVA_BUCKET = 20000, planks/logs = 300, slabs = 150,
// stick/sapling = 100, DRIED_KELP_BLOCK = 4001, hanging_signs = 800. VERIFIED against the CFR builder.
//
// Builder semantics (CFR FuelValues.Builder.add/remove): add(item,time) puts item to time; add(tag,time)
// puts EVERY tag member to time; a LATER add overwrites an earlier one for the same item (map put); remove
// (#non_flammable_wood) deletes those members. The port replays the adds IN ORDER into a map, then removes
// the non_flammable_wood members — so overlap resolves to the last writer, exactly like the Builder.
//
// The map is built ONCE lazily (the fuel registry is immutable) and read on the tick goroutine (TICK-05):
// furnaceBurnDuration(itemID) is FuelValues.burnDuration (map lookup, 0 = not a fuel); furnaceIsFuel is
// FuelValues.isFuel (map contains).

import (
	"sync"

	"github.com/imhinotori/sulfur/data/tag"
)

const furnaceFuelBaseUnit = 200 // FuelValues.vanillaBurnTimes(registries, features) → baseUnit 200 overload.

var (
	furnaceFuelOnce  sync.Once
	furnaceFuelTable map[int32]int // itemID → burn duration in ticks (FuelValues.values)
)

// fuelBuilder replays the FuelValues.Builder add/remove ops into a plain map, resolving item literals via
// itemNameToID and tags via data/tag.ItemTags. A later add overwrites an earlier value for the same item
// (map put), matching the Builder; remove deletes members. Unknown literals/tags are skipped (an item
// absent from the 26.2 registry contributes nothing — never a panic).
type fuelBuilder struct{ m map[int32]int }

// add(item, time): FuelValues.Builder.add(ItemLike, int) — put one item to time (last writer wins).
func (b *fuelBuilder) add(name string, time int) {
	id := itemNameToID(name)
	if id == 0 { // itemNameToID returns 0 (air) for an unknown name — skip (never map air as fuel).
		return
	}
	b.m[id] = time
}

// addTag(tag, time): FuelValues.Builder.add(TagKey, int) — put every tag member to time.
func (b *fuelBuilder) addTag(tagName string, time int) {
	for id, ok := range tag.ItemTags[tagName] {
		if ok {
			b.m[id] = time
		}
	}
}

// removeTag(tag): FuelValues.Builder.remove(TagKey) — delete every tag member from the map.
func (b *fuelBuilder) removeTag(tagName string) {
	for id, ok := range tag.ItemTags[tagName] {
		if ok {
			delete(b.m, id)
		}
	}
}

// buildFuelTable ports FuelValues.vanillaBurnTimes(registries, features, 200): replay the add(...) chain
// in the EXACT jar order, then remove(#non_flammable_wood). The order is load-bearing only where a later
// add overwrites an earlier one for the same item — replaying in order reproduces the Builder's result.
func buildFuelTable() map[int32]int {
	base := furnaceFuelBaseUnit
	b := &fuelBuilder{m: make(map[int32]int, 512)}

	b.add("lava_bucket", base*100) // 20000
	b.add("coal_block", base*8*10) // 16000
	b.add("blaze_rod", base*12)    // 2400
	b.add("coal", base*8)          // 1600
	b.add("charcoal", base*8)      // 1600
	b.addTag("logs", base*3/2)     // 300
	b.addTag("bamboo_blocks", base*3/2)
	b.addTag("planks", base*3/2) // 300
	b.add("bamboo_mosaic", base*3/2)
	b.addTag("wooden_stairs", base*3/2)
	b.add("bamboo_mosaic_stairs", base*3/2)
	b.addTag("wooden_slabs", base*3/4) // 150
	b.add("bamboo_mosaic_slab", base*3/4)
	b.addTag("wooden_trapdoors", base*3/2)
	b.addTag("wooden_pressure_plates", base*3/2)
	b.addTag("wooden_shelves", base*3/2)
	b.addTag("wooden_fences", base*3/2)
	b.addTag("fence_gates", base*3/2)
	b.add("note_block", base*3/2)
	b.add("bookshelf", base*3/2)
	b.add("chiseled_bookshelf", base*3/2)
	b.add("lectern", base*3/2)
	b.add("jukebox", base*3/2)
	b.add("chest", base*3/2)
	b.add("trapped_chest", base*3/2)
	b.add("crafting_table", base*3/2)
	b.add("daylight_detector", base*3/2)
	b.addTag("banners", base*3/2)
	b.add("bow", base*3/2)
	b.add("fishing_rod", base*3/2)
	b.add("ladder", base*3/2)
	b.addTag("signs", base) // 200
	b.addTag("hanging_signs", base*4)
	b.add("wooden_shovel", base)
	b.add("wooden_sword", base)
	b.add("wooden_spear", base)
	b.add("wooden_hoe", base)
	b.add("wooden_axe", base)
	b.add("wooden_pickaxe", base)
	b.addTag("wooden_doors", base)
	b.addTag("boats", base*6)
	b.addTag("wool", base/2)
	b.addTag("wooden_buttons", base/2)
	b.add("stick", base/2) // 100
	b.addTag("saplings", base/2)
	b.add("bowl", base/2)
	b.addTag("wool_carpets", 1+base/3)
	b.add("dried_kelp_block", 1+base*20) // 4001
	b.add("crossbow", base*3/2)
	b.add("bamboo", base/4)
	b.add("dead_bush", base/2)
	b.add("short_dry_grass", base/2)
	b.add("tall_dry_grass", base/2)
	b.add("scaffolding", base/4)
	b.add("loom", base*3/2)
	b.add("barrel", base*3/2)
	b.add("cartography_table", base*3/2)
	b.add("fletching_table", base*3/2)
	b.add("smithing_table", base*3/2)
	b.add("composter", base*3/2)
	b.add("azalea", base/2)
	b.add("flowering_azalea", base/2)
	b.add("mangrove_roots", base*3/2)
	b.add("leaf_litter", base/2)

	b.removeTag("non_flammable_wood") // remove(ItemTags.NON_FLAMMABLE_WOOD)
	return b.m
}

// fuelTable returns the lazily-built vanilla fuel map (itemID → burn ticks). Built once (immutable).
func fuelTable() map[int32]int {
	furnaceFuelOnce.Do(func() { furnaceFuelTable = buildFuelTable() })
	return furnaceFuelTable
}

// furnaceBurnDuration ports FuelValues.burnDuration(itemStack): the map value for the item, or 0 if the
// item is not a fuel (an empty/absent item burns for 0 ticks). itemID is the numeric wire id.
//
// 1:1 net.minecraft.world.level.block.entity.FuelValues.burnDuration
func furnaceBurnDuration(itemID int32) int {
	if itemID <= 0 {
		return 0 // ItemStack.isEmpty() → 0
	}
	return fuelTable()[itemID]
}

// furnaceIsFuel ports FuelValues.isFuel(itemStack): the map contains the item.
//
// 1:1 net.minecraft.world.level.block.entity.FuelValues.isFuel
func furnaceIsFuel(itemID int32) bool {
	if itemID <= 0 {
		return false
	}
	_, ok := fuelTable()[itemID]
	return ok
}
