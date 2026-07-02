package server

// potion_brewing.go — net.minecraft.world.item.alchemy.PotionBrewing, ported 1:1 from the unobfuscated 26.2
// jar (temp/cache/26.2-inner.jar, CFR/javap this session). This is the mix engine the brewing-stand
// block-entity drives: the container list + potion-mix list + container-mix list, and the isIngredient /
// hasMix / mix predicates over them.
//
// 1:1 jar chain (VERIFIED CFR PotionBrewing + PotionBrewing.Builder):
//
//	PotionBrewing holds three lists:
//	    containers      : List<Ingredient>            — the base bottle items (potion/splash/lingering)
//	    potionMixes     : List<Mix<Potion>>           — {from:Holder<Potion>, ingredient:Ingredient, to:Holder<Potion>}
//	    containerMixes  : List<Mix<Item>>             — {from:Holder<Item>,  ingredient:Ingredient, to:Holder<Item>}
//	isIngredient(stack)          = isContainerIngredient(stack) || isPotionIngredient(stack)
//	isContainerIngredient(stack) = any containerMixes.ingredient.test(stack)
//	isPotionIngredient(stack)    = any potionMixes.ingredient.test(stack)
//	isContainer(stack)           = any containers.test(stack)
//	hasMix(bottle, ingredient)   = isContainer(bottle) && (hasContainerMix(bottle,ing) || hasPotionMix(bottle,ing))
//	hasContainerMix(bottle,ing)  = any containerMixes: bottle.is(from) && ingredient.test(ing)
//	hasPotionMix(bottle,ing)     = bottle.potion() present && any potionMixes: from.is(bottlePotion) && ingredient.test(ing)
//	mix(ingredient, bottle):
//	    if bottle empty -> bottle; potion = bottle.potion(); if empty -> bottle;
//	    for containerMixes: if bottle.is(from) && ingredient.test(mix.ingredient)
//	        -> PotionContents.createItemStack(mix.to.value(), potion)   // new item TYPE, same potion
//	    for potionMixes: if from.is(potion) && ingredient.test(mix.ingredient)
//	        -> PotionContents.createItemStack(bottle.getItem(), mix.to)  // same item TYPE, new potion
//	    else -> bottle
//	the mix table is PotionBrewing.addVanillaMixes(Builder) (VERIFIED CFR, ported in buildVanillaPotionBrewing).
//
// Sulfur model: Ingredient.of(item) is a single-item ingredient (Ingredient.test == stack.is(item)); every
// vanilla brewing ingredient/container is a single item, so an Ingredient is represented as an item id here
// (brewMix.ingredientID) and .test(stack) is stack.ItemID == ingredientID. Potion "Holder.is" is a potion
// registry-id equality (registryid.Potion index), and Item "Holder" a numeric item id. The bottle's potion
// id is read from its potion_contents component (potion_component.go readBottlePotion) — the
// PotionContents.potion() Optional. mix()'s createItemStack is makePotionBottle / setBottlePotion.
//
// The full addVanillaMixes table (VERIFIED CFR this session) is ported below; per the task's cited scope the
// STRUCTURAL engine is fully 1:1 and the exhaustive table is present (CITE PotionBrewing.addVanillaMixes as
// the authoritative source — a Mojang table change is a one-line edit here).

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
)

// brewMix is one PotionBrewing.Mix entry (from Holder, ingredient Ingredient, to Holder), specialized to the
// single-item-ingredient / registry-id-Holder shape every vanilla brewing mix uses. For a POTION mix
// (potionMixes) fromID/toID are potion registry ids; for a CONTAINER mix (containerMixes) they are item ids.
type brewMix struct {
	fromID       int32 // Mix.from.is(...) target (potion registry id, or item id for a container mix)
	ingredientID int32 // Mix.ingredient (a single-item Ingredient.of(item))
	toID         int32 // Mix.to (potion registry id, or item id for a container mix)
}

// potionBrewing is the ported PotionBrewing: the three lists (containers as item ids). Built once from the
// vanilla mix table (buildVanillaPotionBrewing) — the analogue of Level.potionBrewing() (PotionBrewing.
// bootstrap -> addVanillaMixes). A single package-level instance is fine: the table is immutable +
// feature-flag-free in v1 (all vanilla mixes enabled).
type potionBrewing struct {
	containers     []int32   // Ingredient list of base bottle items (potion/splash/lingering)
	potionMixes    []brewMix // Mix<Potion> list
	containerMixes []brewMix // Mix<Item> list
}

// vanillaPotionBrewingInstance is the process-wide PotionBrewing instance (Level.potionBrewing()). Lazy.
var vanillaPotionBrewingInstance *potionBrewing

func vanillaPotionBrewing() *potionBrewing {
	if vanillaPotionBrewingInstance == nil {
		vanillaPotionBrewingInstance = buildVanillaPotionBrewing()
	}
	return vanillaPotionBrewingInstance
}

// potionRegID resolves a potion registry name ("minecraft:<name>") to its index (registryid.Potion, i.e.
// Registries.POTION id). Returns -1 for an unknown name (never matches).
func potionRegID(name string) int32 {
	for i, n := range registryid.Potion {
		if n == name {
			return int32(i)
		}
	}
	return -1
}

// isContainer ports PotionBrewing.isContainer(stack): any containers Ingredient tests the stack (the stack
// is one of the base bottle items). Single-item ingredients -> id equality.
func (pb *potionBrewing) isContainer(itemID int32) bool {
	for _, c := range pb.containers {
		if c == itemID {
			return true
		}
	}
	return false
}

// isContainerIngredient ports PotionBrewing.isContainerIngredient(stack): any containerMixes.ingredient
// tests the stack.
func (pb *potionBrewing) isContainerIngredient(itemID int32) bool {
	for _, m := range pb.containerMixes {
		if m.ingredientID == itemID {
			return true
		}
	}
	return false
}

// isPotionIngredient ports PotionBrewing.isPotionIngredient(stack): any potionMixes.ingredient tests it.
func (pb *potionBrewing) isPotionIngredient(itemID int32) bool {
	for _, m := range pb.potionMixes {
		if m.ingredientID == itemID {
			return true
		}
	}
	return false
}

// isIngredient ports PotionBrewing.isIngredient(stack) = isContainerIngredient || isPotionIngredient.
func (pb *potionBrewing) isIngredient(itemID int32) bool {
	return pb.isContainerIngredient(itemID) || pb.isPotionIngredient(itemID)
}

// hasContainerMix ports PotionBrewing.hasContainerMix(bottle, ingredient): any containerMixes where the
// bottle is the from-item AND the ingredient matches the mix's Ingredient.
func (pb *potionBrewing) hasContainerMix(bottle, ingredient component.SlotData) bool {
	for _, m := range pb.containerMixes {
		if int32(bottle.ItemID) == m.fromID && int32(ingredient.ItemID) == m.ingredientID {
			return true
		}
	}
	return false
}

// hasPotionMix ports PotionBrewing.hasPotionMix(bottle, ingredient): the bottle's potion() must be present,
// then any potionMixes where from.is(bottlePotion) AND the ingredient matches. A bottle with no potion
// component (PotionContents.potion() empty) can never mix (the early `if potion.isEmpty() return false`).
func (pb *potionBrewing) hasPotionMix(bottle, ingredient component.SlotData) bool {
	pid, ok := readBottlePotion(bottle)
	if !ok {
		return false
	}
	for _, m := range pb.potionMixes {
		if m.fromID == pid && int32(ingredient.ItemID) == m.ingredientID {
			return true
		}
	}
	return false
}

// hasMix ports PotionBrewing.hasMix(bottle, ingredient): the bottle must be a container, then either a
// container mix or a potion mix applies.
func (pb *potionBrewing) hasMix(bottle, ingredient component.SlotData) bool {
	if !pb.isContainer(int32(bottle.ItemID)) {
		return false
	}
	return pb.hasContainerMix(bottle, ingredient) || pb.hasPotionMix(bottle, ingredient)
}

// mix ports PotionBrewing.mix(ingredient, bottle): an empty bottle or a bottle with no potion returns
// unchanged; a matching CONTAINER mix rebuilds the bottle as the new item type carrying the same potion; a
// matching POTION mix rebuilds it as the same item type carrying the new potion; otherwise the bottle is
// returned unchanged.
func (pb *potionBrewing) mix(ingredient, bottle component.SlotData) component.SlotData {
	if stackEmpty(bottle) {
		return bottle
	}
	pid, ok := readBottlePotion(bottle)
	if !ok {
		return bottle // PotionContents.potion() empty -> return the input
	}
	// containerMixes: bottle.is(from) && ingredient matches -> createItemStack(to.value(), bottlePotion).
	for _, m := range pb.containerMixes {
		if int32(bottle.ItemID) == m.fromID && int32(ingredient.ItemID) == m.ingredientID {
			return makePotionBottle(m.toID, pid)
		}
	}
	// potionMixes: from.is(bottlePotion) && ingredient matches -> createItemStack(bottle.getItem(), to).
	for _, m := range pb.potionMixes {
		if m.fromID == pid && int32(ingredient.ItemID) == m.ingredientID {
			return makePotionBottle(int32(bottle.ItemID), m.toID)
		}
	}
	return bottle
}

// buildVanillaPotionBrewing ports PotionBrewing.addVanillaMixes(Builder) (VERIFIED CFR this session) +
// Builder.build(): the container list, the container-mix list, and the potion-mix list. Builder.addStartMix
// (ingredient, potion) expands to TWO potion mixes: WATER+ingredient->MUNDANE and AWKWARD+ingredient->potion
// (VERIFIED CFR Builder.addStartMix), reproduced by addStart below. All vanilla items/potions are enabled in
// v1 (no feature-flag gating), so every entry is added.
func buildVanillaPotionBrewing() *potionBrewing {
	pb := &potionBrewing{}

	item := func(name string) int32 { return itemNameToID(name) }
	pot := func(name string) int32 { return potionRegID("minecraft:" + name) }

	// addContainer(item): expectPotion + containers.add(Ingredient.of(item)).
	addContainer := func(name string) { pb.containers = append(pb.containers, item(name)) }
	// addContainerRecipe(from, ingredient, to): containerMixes.add(Mix(from, Ingredient.of(ingredient), to)).
	addContainerRecipe := func(from, ingredient, to string) {
		pb.containerMixes = append(pb.containerMixes, brewMix{fromID: item(from), ingredientID: item(ingredient), toID: item(to)})
	}
	// addMix(fromPotion, ingredient, toPotion): potionMixes.add(Mix(from, Ingredient.of(ingredient), to)).
	addMix := func(from, ingredient, to string) {
		pb.potionMixes = append(pb.potionMixes, brewMix{fromID: pot(from), ingredientID: item(ingredient), toID: pot(to)})
	}
	// addStartMix(ingredient, potion): addMix(WATER, ingredient, MUNDANE); addMix(AWKWARD, ingredient, potion).
	addStart := func(ingredient, potion string) {
		addMix("water", ingredient, "mundane")
		addMix("awkward", ingredient, potion)
	}

	// --- VERBATIM order of PotionBrewing.addVanillaMixes (VERIFIED CFR this session) ---
	addContainer("potion")
	addContainer("splash_potion")
	addContainer("lingering_potion")
	addContainerRecipe("potion", "gunpowder", "splash_potion")
	addContainerRecipe("splash_potion", "dragon_breath", "lingering_potion")
	addMix("water", "glowstone_dust", "thick")
	addMix("water", "redstone", "mundane")
	addMix("water", "nether_wart", "awkward")
	addStart("breeze_rod", "wind_charged")
	addStart("slime_block", "oozing")
	addStart("stone", "infested")
	addStart("cobweb", "weaving")
	addMix("awkward", "golden_carrot", "night_vision")
	addMix("night_vision", "redstone", "long_night_vision")
	addMix("night_vision", "fermented_spider_eye", "invisibility")
	addMix("long_night_vision", "fermented_spider_eye", "long_invisibility")
	addMix("invisibility", "redstone", "long_invisibility")
	addStart("magma_cream", "fire_resistance")
	addMix("fire_resistance", "redstone", "long_fire_resistance")
	addStart("rabbit_foot", "leaping")
	addMix("leaping", "redstone", "long_leaping")
	addMix("leaping", "glowstone_dust", "strong_leaping")
	addMix("leaping", "fermented_spider_eye", "slowness")
	addMix("long_leaping", "fermented_spider_eye", "long_slowness")
	addMix("slowness", "redstone", "long_slowness")
	addMix("slowness", "glowstone_dust", "strong_slowness")
	addMix("awkward", "turtle_helmet", "turtle_master")
	addMix("turtle_master", "redstone", "long_turtle_master")
	addMix("turtle_master", "glowstone_dust", "strong_turtle_master")
	addMix("swiftness", "fermented_spider_eye", "slowness")
	addMix("long_swiftness", "fermented_spider_eye", "long_slowness")
	addStart("sugar", "swiftness")
	addMix("swiftness", "redstone", "long_swiftness")
	addMix("swiftness", "glowstone_dust", "strong_swiftness")
	addMix("awkward", "pufferfish", "water_breathing")
	addMix("water_breathing", "redstone", "long_water_breathing")
	addStart("glistering_melon_slice", "healing")
	addMix("healing", "glowstone_dust", "strong_healing")
	addMix("healing", "fermented_spider_eye", "harming")
	addMix("strong_healing", "fermented_spider_eye", "strong_harming")
	addMix("harming", "glowstone_dust", "strong_harming")
	addMix("poison", "fermented_spider_eye", "harming")
	addMix("long_poison", "fermented_spider_eye", "harming")
	addMix("strong_poison", "fermented_spider_eye", "strong_harming")
	addStart("spider_eye", "poison")
	addMix("poison", "redstone", "long_poison")
	addMix("poison", "glowstone_dust", "strong_poison")
	addStart("ghast_tear", "regeneration")
	addMix("regeneration", "redstone", "long_regeneration")
	addMix("regeneration", "glowstone_dust", "strong_regeneration")
	addStart("blaze_powder", "strength")
	addMix("strength", "redstone", "long_strength")
	addMix("strength", "glowstone_dust", "strong_strength")
	addMix("water", "fermented_spider_eye", "weakness")
	addMix("weakness", "redstone", "long_weakness")
	addMix("awkward", "phantom_membrane", "slow_falling")
	addMix("slow_falling", "redstone", "long_slow_falling")

	return pb
}
