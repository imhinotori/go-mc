package server

// furnace_be.go — the FURNACE BLOCK-ENTITY (the smelting DRIVE): a 1:1 port of
// net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.serverTick over the 26.2 jar
// (temp/cache/26.2-inner.jar, CFR this session). This is the per-tick fuel+cook engine the Plan-25
// cooking build-or-defer audit DEFERRED (cooking_block.go): the recipe MATCHER shipped, this is the block.
//
// The furnace/blast_furnace/smoker share AbstractFurnaceBlockEntity, parameterized by the cook RecipeType
// (subtype: smelting/blasting/smoking) and, for blast_furnace/smoker, a HALVED getBurnDuration (fuel burns
// twice as fast). VERIFIED per BlastFurnaceBlockEntity/SmokerBlockEntity: they override getBurnDuration ->
// super/2 (NOT getTotalCookTime — cook time comes from the recipe's cookingTime(), 100 for blast/smoke via
// the per-recipe-class MAP_CODEC default). AbstractFurnaceBlockEntity(type,pos,state,recipeType) fixes the
// subtype; getTotalCookTime = quickCheck.getRecipeFor(input).map(cookingTime).orElse(200).
//
// FIELD NAMES (26.2, VERIFIED CFR — NOTE the 26.2 rename from the older litTime/litDuration):
//   litTimeRemaining  (DATA_LIT_TIME)          — ticks of fuel burn left; >0 == isLit
//   litTotalTime      (DATA_LIT_DURATION)      — the burn duration the current fuel started at
//   cookingTimer      (DATA_COOKING_PROGRESS)  — ticks the current item has cooked
//   cookingTotalTime  (DATA_COOKING_TOTAL_TIME)— ticks needed to finish (getTotalCookTime)
//   BURN_TIME_STANDARD = 200, BURN_COOL_SPEED = 2, DEFAULT cook time 200.
//
// SCOPE (cited deferrals): the furnace BE is IN-MEMORY (t.furnaces, keyed by world pos — the openChests
// twin). There is no BE NBT save/load seam yet (chest_open.go says the same for chest items), so
// AbstractFurnaceBlockEntity.loadAdditional/saveAdditional (cooking_time_spent / cooking_total_time /
// lit_time_remaining / lit_total_time / Items / RecipesUsed) are the persistence follow-up — the live drive
// is fully faithful. recipesUsed accumulates faithfully; the XP orb spawn on result-take is wired
// (furnace_menu.go awardUsedRecipesAndPopExperience -> createExperience -> awardExperienceOrbs).

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/recipe"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Furnace slot indices (AbstractFurnaceBlockEntity.SLOT_INPUT/SLOT_FUEL/SLOT_RESULT).
const (
	furnaceSlotInput  = 0 // SLOT_INPUT
	furnaceSlotFuel   = 1 // SLOT_FUEL
	furnaceSlotResult = 2 // SLOT_RESULT
)

// furnaceBurnCoolSpeed is AbstractFurnaceBlockEntity.BURN_COOL_SPEED (the idle cook-progress decay rate).
const furnaceBurnCoolSpeed = 2

// furnaceDefaultCookTime is the getTotalCookTime orElse default (200) — the fallback when no recipe matches
// the current input (AbstractFurnaceBlockEntity.getTotalCookTime ...orElse(200)).
const furnaceDefaultCookTime = 200

// furnaceBE is the tick-owned state of one furnace/blast_furnace/smoker block-entity — the Sulfur analogue
// of AbstractFurnaceBlockEntity narrowed to the fields serverTick + the menu read/write. items[0..2] are
// input/fuel/result (NonNullList<ItemStack> items). subtype fixes the RecipeType (the ctor's recipeType).
// blastLike halves getBurnDuration (BlastFurnaceBlockEntity/SmokerBlockEntity.getBurnDuration -> super/2).
//
// recipesUsed + recipesXP together model AbstractFurnaceBlockEntity.recipesUsed (a
// Reference2IntOpenHashMap<ResourceKey<Recipe>>): vanilla keys the completed-cook COUNT by the recipe's
// registry key and later resolves each key's experience() via recipeAccess().byKey. v1's recipe.Cooking
// carries no registry id, so we key by a synthetic per-recipe string (furnaceRecipeKey) and carry that
// recipe's experience() alongside (recipesXP) so the result-take XP award computes floor(amount*exp) +
// fractional-orb IDENTICALLY to createExperience — the id indirection is the only shape change, the XP
// math is 1:1.
type furnaceBE struct {
	items [3]component.SlotData

	litTimeRemaining int // DATA_LIT_TIME
	litTotalTime     int // DATA_LIT_DURATION
	cookingTimer     int // DATA_COOKING_PROGRESS
	cookingTotalTime int // DATA_COOKING_TOTAL_TIME

	subtype   cookSubtype // the RecipeType this BE cooks (smelting/blasting/smoking)
	blastLike bool        // true for blast_furnace + smoker (getBurnDuration -> /2)

	recipesUsed map[string]int     // synthetic recipe key -> times used (RecipesUsed.addTo(id,1))
	recipesXP   map[string]float64 // synthetic recipe key -> that recipe's experience() (byKey resolve)
}

// furnaceRecipeKey builds the synthetic per-recipe key for the recipesUsed/recipesXP maps: the subtype + the
// result item id uniquely identifies a cooking recipe within a furnace's RecipeType (each furnace resolves
// exactly one recipe per input, and two inputs producing the same result under the same subtype share the
// same experience — so keying by result is faithful for the XP sum). Stands in for the vanilla ResourceKey.
func furnaceRecipeKey(r recipe.Cooking) string {
	return r.Subtype + ":" + itemName(int32(r.Result.ID))
}

// getBurnDuration ports AbstractFurnaceBlockEntity.getBurnDuration(fuelValues, itemStack) with the
// BlastFurnaceBlockEntity/SmokerBlockEntity override: the base is FuelValues.burnDuration(item); the
// blast/smoke subclasses return super/2 (Java integer division). VERIFIED CFR: both overrides are
// `return super.getBurnDuration(fuelValues, itemStack) / 2;`.
func (f *furnaceBE) getBurnDuration(fuelID int32) int {
	d := furnaceBurnDuration(fuelID)
	if f.blastLike {
		return d / 2
	}
	return d
}

// getTotalCookTime ports AbstractFurnaceBlockEntity.getTotalCookTime(level, entity): the current input's
// cooking recipe's cookingTime(), or 200 when no recipe of this subtype matches the input. (Blast/smoke do
// NOT override this — their faster cook comes from the recipe's own cookingTime, 100 by MAP_CODEC default.)
//
// 1:1 net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.getTotalCookTime
func (f *furnaceBE) getTotalCookTime() int {
	if r, ok := findCookingRecipe(int32(f.items[furnaceSlotInput].ItemID), f.subtype); ok {
		return r.CookingTime
	}
	return furnaceDefaultCookTime
}

// furnaceCanBurn ports AbstractFurnaceBlockEntity.canBurn(items, maxStackSize, burnResult): the result slot
// is empty, OR it holds the same item+components AND (resultCount + burnResult.count) <= min(maxStackSize,
// burnResult.getMaxStackSize()).
//
// 1:1 net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.canBurn
func furnaceCanBurn(result component.SlotData, maxStackSize int, burnResult component.SlotData) bool {
	if stackEmpty(burnResult) {
		return false // !burnResult.isEmpty() gate in serverTick (a null/empty result never burns)
	}
	if stackEmpty(result) {
		return true // result slot empty -> always burnable
	}
	if !stackSameItemSameComponents(result, burnResult) {
		return false
	}
	resultCount := int(result.Count) + int(burnResult.Count)
	maxResultCount := min(maxStackSize, stackMaxSize(burnResult))
	return resultCount <= maxResultCount
}

// furnaceMaxStackSize ports AbstractFurnaceBlockEntity.getMaxStackSize() = Container.getMaxStackSize() = 64
// (the default container stack limit — SimpleContainer(3) has no override). CITE Container.getMaxStackSize.
func furnaceMaxStackSize() int { return 64 }

// furnaceServerTick ports AbstractFurnaceBlockEntity.serverTick(level, pos, state, entity) EXACTLY: the
// fuel-burn decrement, the ignite-from-fuel step, the canBurn gate, the cook increment, the burn (place
// result + consume input + setRecipeUsed), the idle cook-progress decay, and the LIT blockstate toggle +
// setChanged. Runs on the tick goroutine (called from tickWorld). state is the current furnace stateID.
//
// 1:1 net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.serverTick
func (t *TickLoop) furnaceServerTick(pos pk.Position, state block.StateID, f *furnaceBE) {
	changed := false

	// boolean wasLit; boolean isLit; if (litTimeRemaining > 0) { wasLit=true; --litTimeRemaining;
	//   isLit = litTimeRemaining > 0; } else { wasLit=false; isLit=false; }
	var wasLit, isLit bool
	if f.litTimeRemaining > 0 {
		wasLit = true
		f.litTimeRemaining--
		isLit = f.litTimeRemaining > 0
	} else {
		wasLit = false
		isLit = false
	}

	fuel := f.items[furnaceSlotFuel]
	ingredient := f.items[furnaceSlotInput]
	hasIngredient := !stackEmpty(ingredient)
	hasFuel := !stackEmpty(fuel)

	// if (isLit || (hasFuel && hasIngredient)) { ... }
	if isLit || (hasFuel && hasIngredient) {
		if hasIngredient {
			// SingleRecipeInput input = new SingleRecipeInput(ingredient);
			// RecipeHolder recipe = quickCheck.getRecipeFor(input, level).orElse(null);
			r, recipeOK := findCookingRecipe(int32(ingredient.ItemID), f.subtype)
			if recipeOK {
				maxStackSize := furnaceMaxStackSize()
				// ItemStack burnResult = recipe.value().assemble(input);
				burnResult := furnaceAssemble(r)
				// if (!burnResult.isEmpty() && canBurn(items, maxStackSize, burnResult)) { ... }
				if !stackEmpty(burnResult) && furnaceCanBurn(f.items[furnaceSlotResult], maxStackSize, burnResult) {
					// if (!isLit) { litTimeRemaining = litTotalTime = getBurnDuration(fuelValues, fuel);
					//   if (newLitTime > 0) { consumeFuel(items, fuel); isLit=true; changed=true; } }
					if !isLit {
						newLitTime := f.getBurnDuration(int32(fuel.ItemID))
						f.litTimeRemaining = newLitTime
						f.litTotalTime = newLitTime
						if newLitTime > 0 {
							furnaceConsumeFuel(f, int32(fuel.ItemID))
							isLit = true
							changed = true
						}
					}
					// if (isLit) { ++cookingTimer; if (cookingTimer == cookingTotalTime) { cookingTimer=0;
					//   cookingTotalTime = recipe.cookingTime(); burn(items, ingredient, burnResult);
					//   setRecipeUsed(recipe); changed=true; } } else { cookingTimer=0; }
					if isLit {
						f.cookingTimer++
						if f.cookingTimer == f.cookingTotalTime {
							f.cookingTimer = 0
							f.cookingTotalTime = r.CookingTime
							furnaceBurn(f, burnResult)
							furnaceSetRecipeUsed(f, r)
							changed = true
						}
					} else {
						f.cookingTimer = 0
					}
				} else {
					f.cookingTimer = 0 // else branch of canBurn
				}
			}
		} else {
			f.cookingTimer = 0 // else branch of hasIngredient
		}
	} else if f.cookingTimer > 0 {
		// cookingTimer = Mth.clamp(cookingTimer - 2, 0, cookingTotalTime);
		f.cookingTimer = mthClampInt(f.cookingTimer-furnaceBurnCoolSpeed, 0, f.cookingTotalTime)
	}

	// if (wasLit != isLit) { changed=true; state = state.setValue(LIT, isLit); level.setBlock(pos, state, 3); }
	if wasLit != isLit {
		changed = true
		if ns, ok := furnaceWithLit(state, isLit); ok {
			t.setFurnaceBlockLit(pos, ns)
			state = ns
		}
	}

	// if (changed) setChanged(level, pos, state); — v1 setChanged is the in-memory drive itself (no BE NBT
	// save seam yet, cited above); the authoritative container re-send to any open viewer is the observable
	// equivalent. broadcastFurnaceChange re-sends the open menu's content + progress data slots.
	if changed {
		t.broadcastFurnaceChange(pos, f)
	}
}

// furnaceAssemble ports AbstractCookingRecipe.assemble(input): the recipe result stack (a fresh copy). v1
// cooking recipes carry a flat result id+count; assemble has no component transform (SingleItemRecipe
// assemble is result.copy()). Returns the burn result stack.
func furnaceAssemble(r recipe.Cooking) component.SlotData {
	cnt := r.Result.Count
	if cnt <= 0 {
		cnt = 1 // ItemStackTemplate default
	}
	return component.SlotData{ItemID: toItemID(r.Result.ID), Count: toVar(cnt)}
}

// furnaceConsumeFuel ports AbstractFurnaceBlockEntity.consumeFuel(items, fuel): shrink the fuel by 1; if it
// empties, replace it with the fuel item's crafting remainder (a lava_bucket -> bucket). v1 has no general
// getCraftingRemainder data seam; the only DEFAULT-datapack fueled remainder is lava_bucket -> bucket
// (Items with a crafting_remainder). Port that case explicitly (cited) and leave empty for others —
// faithful to the observed vanilla fuel set (no other default fuel has a crafting remainder).
//
// 1:1 net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.consumeFuel
func furnaceConsumeFuel(f *furnaceBE, fuelID int32) {
	fuel := f.items[furnaceSlotFuel]
	fuel.Count = toVar(int(fuel.Count) - 1) // fuel.shrink(1)
	if fuel.Count <= 0 {
		// fuel.isEmpty() -> items.set(1, remainder != null ? remainder.create() : EMPTY).
		if rem, ok := furnaceCraftingRemainder(fuelID); ok {
			f.items[furnaceSlotFuel] = rem
		} else {
			f.items[furnaceSlotFuel] = component.SlotData{Count: 0}
		}
	} else {
		f.items[furnaceSlotFuel] = fuel
	}
}

// furnaceCraftingRemainder ports the DEFAULT-datapack fueled crafting-remainder set: lava_bucket -> bucket
// (Items.LAVA_BUCKET.craftingRemainder == BUCKET). No other default-datapack fuel has a crafting remainder,
// so every other fuel returns (empty, false). CITE Item.getCraftingRemainder / Items.LAVA_BUCKET.
func furnaceCraftingRemainder(fuelID int32) (component.SlotData, bool) {
	if fuelID == itemNameToID("lava_bucket") {
		return component.SlotData{ItemID: toItemID(int(itemNameToID("bucket"))), Count: 1}, true
	}
	return component.SlotData{Count: 0}, false
}

// furnaceBurn ports AbstractFurnaceBlockEntity.burn(items, inputItemStack, result): place/grow the result,
// the wet_sponge->water_bucket special (a wet_sponge smelted with a bucket in the fuel slot fills it), then
// shrink the input by 1.
//
// 1:1 net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.burn
func furnaceBurn(f *furnaceBE, result component.SlotData) {
	res := f.items[furnaceSlotResult]
	if stackEmpty(res) {
		f.items[furnaceSlotResult] = result // items.set(2, result.copy())
	} else {
		res.Count = toVar(int(res.Count) + int(result.Count)) // resultItemStack.grow(result.getCount())
		f.items[furnaceSlotResult] = res
	}
	// if (input.is(WET_SPONGE) && !items.get(1).isEmpty() && items.get(1).is(BUCKET)) items.set(1, WATER_BUCKET).
	if int32(f.items[furnaceSlotInput].ItemID) == itemNameToID("wet_sponge") {
		fuel := f.items[furnaceSlotFuel]
		if !stackEmpty(fuel) && int32(fuel.ItemID) == itemNameToID("bucket") {
			f.items[furnaceSlotFuel] = component.SlotData{ItemID: toItemID(int(itemNameToID("water_bucket"))), Count: 1}
		}
	}
	// input.shrink(1).
	in := f.items[furnaceSlotInput]
	in.Count = toVar(int(in.Count) - 1)
	if in.Count <= 0 {
		in = component.SlotData{Count: 0}
	}
	f.items[furnaceSlotInput] = in
}

// furnaceSetRecipeUsed ports AbstractFurnaceBlockEntity.setRecipeUsed(recipe): recipesUsed.addTo(id, 1) —
// accumulate the completed-cook count for this recipe (for the result-take XP award). v1 keys by the
// synthetic furnaceRecipeKey and stashes the recipe's experience() in recipesXP so the take-time award sums
// floor(count*exp) + fractional IDENTICALLY to createExperience.
//
// 1:1 net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity.setRecipeUsed
func furnaceSetRecipeUsed(f *furnaceBE, r recipe.Cooking) {
	if f.recipesUsed == nil {
		f.recipesUsed = make(map[string]int)
		f.recipesXP = make(map[string]float64)
	}
	key := furnaceRecipeKey(r)
	f.recipesUsed[key]++ // Reference2IntOpenHashMap.addTo(id, 1)
	f.recipesXP[key] = r.Experience
}

// mthClampInt ports net.minecraft.util.Mth.clamp(int, int, int).
func mthClampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// tickFurnaces ticks every live furnace/blast_furnace/smoker block-entity once per tick (the
// ServerLevel-side blockEntityTicker fan-out for AbstractFurnaceBlockEntity.serverTick). Called from
// tickWorld. Each furnace reads its CURRENT block state from the world (the serverTick `state` arg) and
// runs the drive; if the block at that position is no longer a furnace-family block (broken/replaced), the
// block-entity is dropped from the store (vanilla removes the ticker when the block entity is removed). A
// nil world leaves furnaces un-ticked (tests may drive furnaceServerTick directly). Tick-owned (TICK-05).
func (t *TickLoop) tickFurnaces() {
	if len(t.furnaces) == 0 {
		return
	}
	w := t.world()
	for pos, f := range t.furnaces {
		if w == nil {
			continue
		}
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isAnyFurnaceBlock(state) {
			// The furnace block is gone (broken/unloaded): drop the BE (its ticker is removed in vanilla).
			delete(t.furnaces, pos)
			continue
		}
		t.furnaceServerTick(pos, state, f)
	}
}
