package server

// furnace_recipe.go — the COOKING-recipe lookup the furnace block-entity drive needs (GAMEPLAY-05 furnace).
// The Sulfur analogue of AbstractFurnaceBlockEntity.quickCheck.getRecipeFor(SingleRecipeInput, level) —
// the RecipeManager.CachedCheck<SingleRecipeInput, AbstractCookingRecipe> each furnace holds for its
// RecipeType (SMELTING for a furnace, BLASTING for a blast_furnace, SMOKING for a smoker).
//
// Vanilla stores each cooking recipe under a RecipeType and looks up by (input, type). The parsed recipe
// tree (level/recipe.ParseAll) carries the Subtype string ("smelting" / "blasting" / "smoking" /
// "campfire_cooking"); this file caches the cooking recipes once (immutable tree) and resolves the FIRST
// recipe of the requested subtype whose single ingredient accepts the input — exactly SingleItemRecipe.
// matches (recipe.MatchCooking). getRecipeFor returns the first match (RecipeManager iterates in registry
// order and returns the first matching); v1 iterates parse order, which is the same set.
//
// JAR: AbstractFurnaceBlockEntity.serverTick uses entity.quickCheck.getRecipeFor(input, level); the result
// carries assemble() (== recipe.Result), cookingTime() (== recipe.CookingTime), experience() (==
// recipe.Experience). AbstractFurnaceBlockEntity(type, pos, state, recipeType) fixes the subtype per BE
// subclass (BlastFurnaceBlockEntity → RecipeType.BLASTING, SmokerBlockEntity → RecipeType.SMOKING).

import (
	"sync"

	"github.com/imhinotori/sulfur/level/recipe"
)

// cookSubtype is the furnace-family cooking subtype (the RecipeType the AbstractFurnaceBlockEntity ctor
// fixes per subclass). A furnace uses smelting, a blast_furnace blasting, a smoker smoking.
type cookSubtype string

const (
	cookSmelting cookSubtype = "smelting" // FurnaceBlockEntity → RecipeType.SMELTING
	cookBlasting cookSubtype = "blasting" // BlastFurnaceBlockEntity → RecipeType.BLASTING
	cookSmoking  cookSubtype = "smoking"  // SmokerBlockEntity → RecipeType.SMOKING
)

// cookingOnce caches the parsed cooking recipe subset (all four subtypes). Parsed once (the recipe tree is
// immutable), tick-read (TICK-05).
var (
	cookingOnce  sync.Once
	cookingCache []recipe.Cooking
)

// cookingRecipes returns the parsed cooking recipes (cached). On a parse error it returns an empty list (a
// furnace with no recipes ticks but cooks nothing — never a panic). Sulfur analogue of the cooking-recipe
// subset of RecipeAccess.
func cookingRecipes() []recipe.Cooking {
	cookingOnce.Do(func() {
		recipes, err := recipe.ParseAll()
		if err != nil {
			return // leave the cache empty (tick-but-cook-nothing, never a panic)
		}
		for i := range recipes {
			if recipes[i].Type == recipe.TypeCooking && recipes[i].Cooking != nil {
				cookingCache = append(cookingCache, *recipes[i].Cooking)
			}
		}
	})
	return cookingCache
}

// findCookingRecipe ports AbstractFurnaceBlockEntity.quickCheck.getRecipeFor(SingleRecipeInput(input),
// level): the FIRST cooking recipe of the given subtype whose single ingredient accepts inputID (count 1,
// SingleItemRecipe.matches == input.test(item)). Returns the recipe + true, or (zero, false) when no
// recipe of that subtype matches (getRecipeFor → Optional.empty). inputID is the numeric wire item id.
//
// 1:1 net.minecraft.world.item.crafting.RecipeManager.CachedCheck.getRecipeFor (first match) over
// SingleItemRecipe.matches (recipe.MatchCooking).
func findCookingRecipe(inputID int32, subtype cookSubtype) (recipe.Cooking, bool) {
	if inputID <= 0 {
		return recipe.Cooking{}, false // empty input → no recipe (SingleRecipeInput of EMPTY matches nothing)
	}
	list := cookingRecipes()
	for i := range list {
		if list[i].Subtype != string(subtype) {
			continue // wrong RecipeType (a smelting recipe is not a blasting recipe)
		}
		if recipe.MatchCooking(&list[i], recipe.Stack{ID: int(inputID), Count: 1}) {
			return list[i], true
		}
	}
	return recipe.Cooking{}, false
}

// findCookingRecipeByResult resolves the FIRST cooking recipe of the given subtype whose RESULT item is
// resultID — the inverse lookup the RecipesUsed reload uses to re-derive a restored recipe's experience()
// (block_entity_persist.go furnaceExperienceForKey). This is the load-time analogue of vanilla resolving a
// persisted ResourceKey through the recipe registry to read experience(): furnaceRecipeKey keys recipesUsed
// by (subtype, result-item-id), so a persisted key maps back to the recipe by matching its result under its
// subtype. Returns (zero, false) when no such recipe exists (a removed recipe — its cooks award no XP,
// faithful to a byKey miss).
func findCookingRecipeByResult(resultID int32, subtype cookSubtype) (recipe.Cooking, bool) {
	if resultID <= 0 {
		return recipe.Cooking{}, false
	}
	list := cookingRecipes()
	for i := range list {
		if list[i].Subtype != string(subtype) {
			continue
		}
		if int32(list[i].Result.ID) == resultID {
			return list[i], true
		}
	}
	return recipe.Cooking{}, false
}
