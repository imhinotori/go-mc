package server

// campfire_be.go -- the CAMPFIRE BLOCK-ENTITY (CAMPFIRE-01): a 1:1 port of
// net.minecraft.world.level.block.entity.CampfireBlockEntity.cookTick / cooldownTick / placeFood over the
// 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). The campfire is a per-tick DRIVE (the
// hopper/furnace twin, t.campfires keyed by world position) that holds 4 cooking slots; each occupied slot
// advances its cookingProgress by 1 per tick until it reaches that slot cookingTime (set from the matching
// CampfireCookingRecipe when the food was placed), at which point the recipe result is dropped into the
// world and the slot is cleared. Placing food (right-click a LIT campfire with a campfire-cooking input)
// fills the first empty slot and records its cookingTime.
//
// 1:1 jar (net.minecraft.world.level.block.entity.CampfireBlockEntity), VERIFIED CFR:
//   BURN_COOL_SPEED = 2 (cooldownTick clamp step, unlit; DEFERRED cited); NUM_SLOTS = 4.
//   cookTick: for each occupied slot, cookingProgress[i]++; when >= cookingTime[i], assemble the
//     CampfireCookingRecipe result, Containers.dropItemStack it, clear the slot (BE-data sync deferred).
//   placeFood: fill the first empty slot; cookingTime[i]=recipe.cookingTime(), cookingProgress[i]=0,
//     items.set(i, stack.consumeAndReturn(1, placer)); return true. No free slot / no recipe -> false.
//
// The per-slot cookingProgress++ -> >=cookingTime -> drop-result-and-clear machine is preserved so the
// cook timing matches vanilla tick-for-tick. particleTick (client cosmetic) + cooldownTick (unlit-campfire
// progress wind-down) are cite-deferred.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Campfire constants (CampfireBlockEntity static fields, VERIFIED CFR).
const (
	campfireNumSlots      = 4 // NUM_SLOTS -- items/cookingProgress/cookingTime length
	campfireBurnCoolSpeed = 2 // BURN_COOL_SPEED -- cooldownTick clamp step (unlit; DEFERRED cited)
)

// campfireBE is the tick-owned state of one campfire block-entity -- the Go analogue of
// CampfireBlockEntity narrowed to the 4-slot cooking arrays. items[i] is the raw food stack in slot i
// (empty Count 0 == ItemStack.EMPTY); cookingProgress[i] is how many ticks it has cooked; cookingTime[i]
// is the target set from the CampfireCookingRecipe when the food was placed.
type campfireBE struct {
	items           [campfireNumSlots]component.SlotData
	cookingProgress [campfireNumSlots]int
	cookingTime     [campfireNumSlots]int
}

// campfireCookTick ports CampfireBlockEntity.cookTick EXACTLY: advance each occupied slot progress and,
// when it reaches its cookingTime, assemble the CampfireCookingRecipe result, drop it, and clear the slot.
// Runs on the tick goroutine, only for a LIT campfire (the getTicker gate -- see tickCampfires). The
// isItemEnabled feature-flag check always passes in v1 (all default features on), folded to always-true
// (cited). The sendBlockUpdated + BLOCK_CHANGE gameEvent are cite-deferred (BE-data sync only; the drop is
// the observable gameplay).
//
// 1:1 net.minecraft.world.level.block.entity.CampfireBlockEntity.cookTick
func (t *TickLoop) campfireCookTick(pos pk.Position, c *campfireBE) {
	if t.world() == nil {
		return
	}
	// boolean changed = false;
	changed := false
	for i := 0; i < campfireNumSlots; i++ {
		food := c.items[i]
		// if (food.isEmpty()) continue;
		if stackEmpty(food) {
			continue
		}
		// changed = true; cookingProgress[i]++;
		changed = true
		c.cookingProgress[i]++
		// if (cookingProgress[i] < cookingTime[i]) continue;
		if c.cookingProgress[i] < c.cookingTime[i] {
			continue
		}
		// ItemStack result = quickCheck.getRecipeFor(input, level).map(assemble).orElse(food);
		result, ok := findCampfireResult(int32(food.ItemID))
		if !ok {
			// getRecipeFor -> Optional.empty -> .orElse(food): the result IS the food (a slot that no longer
			// matches any recipe cooks into itself). Preserve the vanilla orElse fall-back.
			result = food
		}
		// Containers.dropItemStack(level, x, y, z, result): drop the assembled result as one Item entity.
		t.campfireDropCookedItem(pos, result)
		// items.set(i, ItemStack.EMPTY);
		c.items[i] = component.SlotData{Count: 0}
		// sendBlockUpdated + gameEvent(BLOCK_CHANGE): cite-deferred (BE-data sync only).
	}
	// if (changed) setChanged(level, pos, state): persistence DEFERRED (cited -- beacon/conduit pattern:
	// register-on-place + tick, no NBT round-trip yet). The changed flag is preserved for fidelity.
	_ = changed
}

// campfireDropCookedItem ports Containers.dropItemStack(level, x, y, z, stack) for the campfire cooked
// result: spawn one Item entity carrying the assembled result at the campfire cell. Vanilla dropItemStack
// floors the passed x/y/z and adds a per-axis jitter; the cook call site passes the raw block coords. v1
// uses the shared NewItemEntity spawn (block-center + toss velocity), which lands the item at the same cell
// -- the observable drop is identical (a precise vanilla offset is a cited cosmetic follow-up). CITE
// Containers.dropItemStack.
func (t *TickLoop) campfireDropCookedItem(pos pk.Position, result component.SlotData) {
	if t.cur() == nil || stackEmpty(result) {
		return
	}
	x := float64(pos.X) + 0.5
	y := float64(pos.Y) + 0.5
	z := float64(pos.Z) + 0.5
	ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, result)
	t.cur().entities.add(ie)
}

// campfirePlaceFood ports CampfireBlockEntity.placeFood(serverLevel, placer, stack): fill the FIRST empty
// slot with one item off the placed stack, recording that slot cookingTime from the matching
// CampfireCookingRecipe. Returns true when the food was placed (a slot was free AND the stack matches a
// campfire-cooking recipe), false otherwise (no free slot, or the stack has no recipe). The placed stack is
// shrunk by 1 by the caller (consumeAndReturn) -- see useCampfire.
//
// 1:1 net.minecraft.world.level.block.entity.CampfireBlockEntity.placeFood
func (t *TickLoop) campfirePlaceFood(p *tickPlayer, c *campfireBE, stack component.SlotData) bool {
	for i := 0; i < campfireNumSlots; i++ {
		// if (!items.get(i).isEmpty()) continue;
		if !stackEmpty(c.items[i]) {
			continue
		}
		// Optional<RecipeHolder<CampfireCookingRecipe>> r = getRecipeFor(CAMPFIRE_COOKING, input, level);
		// if (r.isEmpty()) return false;
		r, ok := findCookingRecipe(int32(stack.ItemID), campfireCookingSubtype)
		if !ok {
			return false // no campfire-cooking recipe for this input -> placeFood returns false
		}
		// cookingTime[i] = r.get().value().cookingTime(); cookingProgress[i] = 0;
		c.cookingTime[i] = r.CookingTime
		c.cookingProgress[i] = 0
		// items.set(i, stack.consumeAndReturn(1, placer)): store ONE item in the slot (single-count copy).
		placed := stack
		placed.Count = 1
		c.items[i] = placed
		// gameEvent(BLOCK_CHANGE) + setChanged(): cite-deferred (BE-data sync; persistence DEFERRED cited).
		return true
	}
	return false // no empty slot -> placeFood returns false
}

// campfireCookingSubtype is the RecipeType.CAMPFIRE_COOKING subtype string used to filter the shared
// cooking-recipe cache (furnace_recipe.go). A campfire uses the same SingleItemRecipe.matches path as a
// furnace, under a different subtype. CITE CampfireBlockEntity.placeFood / cookTick.
const campfireCookingSubtype cookSubtype = "campfire_cooking"

// findCampfireResult resolves the assembled result stack of the FIRST campfire-cooking recipe whose single
// ingredient accepts inputID -- the cookTick assemble path. Returns (result, true) on a match, (zero,
// false) when no campfire recipe matches (cookTick then falls back to the food via orElse). CITE
// CampfireCookingRecipe.assemble (== recipe.Result).
func findCampfireResult(inputID int32) (component.SlotData, bool) {
	r, ok := findCookingRecipe(inputID, campfireCookingSubtype)
	if !ok {
		return component.SlotData{Count: 0}, false
	}
	cnt := r.Result.Count
	if cnt <= 0 {
		cnt = 1
	}
	return component.SlotData{ItemID: toItemID(r.Result.ID), Count: toVar(cnt)}, true
}

// useCampfire ports CampfireBlock.useItemOn -> placeFood: a right-click on a campfire with a
// campfire-cooking input places the food (if a slot is free), consuming the action so no block is placed.
// A non-recipe hand returns TRY_WITH_EMPTY_HAND in vanilla -> the interaction is NOT consumed and placement
// continues; we return false there so the block-place path runs. CITE CampfireBlock.useItemOn
// (RecipePropertySet.CAMPFIRE_INPUT.test(stack) gate -> placeFood).
//
// 1:1 net.minecraft.world.level.block.CampfireBlock.useItemOn
func (t *TickLoop) useCampfire(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	_ = state
	if t.world() == nil {
		return false
	}
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	// RecipePropertySet.CAMPFIRE_INPUT.test(held): the held item must match a campfire-cooking recipe. When
	// it does not, vanilla returns TRY_WITH_EMPTY_HAND (the campfire block does NOT consume the click) --
	// return false so the use path continues (a non-food item may place a block).
	if stackEmpty(held) {
		return false
	}
	if _, ok := findCookingRecipe(int32(held.ItemID), campfireCookingSubtype); !ok {
		return false
	}
	c := t.resolveCampfire(pos)
	if c == nil {
		return false
	}
	// placeFood(serverLevel, player, held): fill the first empty slot; on success shrink the held stack by 1
	// (creative keeps it). On a full campfire placeFood returns false -> CONSUME (the campfire still ate the
	// click) -- either way no block is placed for a food item.
	if t.campfirePlaceFood(p, c, held) {
		if p.gameMode != gameModeCreative {
			// stack.consumeAndReturn(1, placer): shrink the held stack by 1 + sync the slot to the client.
			t.shrinkHeldItem(p, inv)
		}
	}
	return true // the campfire consumed the interaction (placed or full) -- never a block-place fall-through.
}

// resolveCampfire returns the tick-owned campfireBE for pos, creating an EMPTY one (all 4 slots clear) on
// first access -- the analogue of a freshly-placed campfire default CampfireBlockEntity. Returns nil when
// pos is not a campfire block (or the world is unloaded). Tick-owned (t.campfires, the t.beacons twin).
func (t *TickLoop) resolveCampfire(pos pk.Position) *campfireBE {
	if t.campfires == nil {
		t.campfires = make(map[pk.Position]*campfireBE)
	}
	if c, ok := t.campfires[pos]; ok {
		return c
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isCampfireBlock(state) {
		return nil
	}
	c := &campfireBE{}
	t.campfires[pos] = c
	return c
}

// isCampfireBlock reports whether a state is a campfire or soul_campfire (any LIT/FACING combo). CITE
// CampfireBlock (both campfire and soul_campfire are CampfireBlock instances).
func isCampfireBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	switch block.StateList[s].(type) {
	case block.Campfire, block.SoulCampfire:
		return true
	}
	return false
}

// isLitCampfireBlock reports whether a state is a LIT campfire/soul_campfire -- the getTicker gate that
// wires cookTick (an unlit campfire wires cooldownTick, DEFERRED cited). Reuses the block-package
// IsLitCampfire predicate (already ported for path-type fire avoidance). CITE CampfireBlock.getTicker.
func isLitCampfireBlock(s block.StateID) bool {
	return block.IsLitCampfire(s)
}

// tickCampfires ticks every live campfire block-entity once per tick (the blockEntityTicker fan-out for
// CampfireBlockEntity.cookTick). Called from tickWorld (the tickBeacons twin). Each campfire reads its
// CURRENT block state; a non-campfire cell (broken/replaced) drops the BE from the store. Only a LIT
// campfire runs cookTick (the getTicker gate); an UNLIT campfire cooldownTick is DEFERRED (cited -- it only
// winds cookingProgress DOWN when the fire is extinguished, a cosmetic reset with no drop). A nil world
// leaves campfires un-ticked (tests may drive campfireCookTick directly). Tick-owned.
func (t *TickLoop) tickCampfires() {
	if len(t.campfires) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, c := range t.campfires {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isCampfireBlock(state) {
			delete(t.campfires, pos)
			continue
		}
		// getTicker wires cookTick only when LIT; an unlit campfire cooldownTick is DEFERRED (cited).
		if isLitCampfireBlock(state) {
			t.campfireCookTick(pos, c)
		}
	}
}
