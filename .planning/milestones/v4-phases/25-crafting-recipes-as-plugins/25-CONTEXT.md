# Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood) — Context

**Gathered:** 2026-06-28
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 25-RESEARCH.md (HIGH-confidence, named the exact stub)
**Requirement:** PLUGIN-05

<domain>
## Phase Boundary

**Delivers:** crafting built THROUGH the plugin API — proving the API generalizes to a SECOND, very different domain (recipes/menus, not entity AI). A recipe-provider plugin (via the Phase-22 host + a NEW value-returning Match seam) loads the jar-extracted recipes and drives the crafting-grid result + the ResultSlot.onTake consume. Includes the crafting_table block + the 3×3 menu (the core only had the 2×2 inventory grid). The 1:1 MANDATE APPLIES to the recipe-match + consume logic (literal jar port, cited). Custom recipes fall out free.

**Phase-23-INDEPENDENT** (the crafting grid is plain (id,count) scalars — reuses the Phase-22 frozen-payload discipline, NOT the Phase-23 entity-handle API). Independent of Phase 24.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Recipe scope — ALL types (operator-directed: "todos los tipos incl smelting")
- Port the recipe DATA + 1:1 MATCH for ALL vanilla recipe types: shaped + shapeless (crafting grid), AND smelting/blasting/smoking/campfire_cooking + stonecutting. Recipe data = `//go:embed` the jar `recipe/` JSON (the exact `level/loot/embed.go` twin — ~1129 crafting files + the cooking/stonecutting files).
- **The match logic is the 1:1 dogfood for EACH type:** shaped = bounding-box shrink (CraftingInput.ofPositioned) + mirror-then-unmirror (ShapedRecipePattern.matches); shapeless = multiset (StackedItemContents.canCraft); cooking/stonecutting = single-ingredient match. javap each + cite.
- **BUT a recipe needs its MENU/BLOCK to be craftable, and the no-built-but-unwired rule governs each menu/block:**
  - **Crafting-grid (shaped+shapeless):** the 2×2 inventory grid EXISTS (the stub) + the crafting_table 3×3 menu is built this phase → these recipes are fully craftable now. CORE of the dogfood.
  - **Smelting/blasting/smoking/campfire:** need a furnace/blast-furnace/smoker/campfire BLOCK-ENTITY with a fuel+progress COOKING TICK (a NEW subsystem). The planner decides: if the furnace block-entity + cooking tick is faithfully buildable in this phase's budget, BUILD the minimal 1:1 slice (the furnace block + its menu + the cooking tick driving the smelting matcher) so smelting is craftable; if it's too large, SHIP the smelting recipe matcher (the data + match, usable + tested) but DEFER the furnace block/menu with a CITED reason (the recipe matcher is ready, the block subsystem is the deferral) — do NOT fake a furnace. Record the decision.
  - **Stonecutting:** needs the stonecutter block + its single-input menu — same build-or-defer judgment.
- The OUTCOME the operator wants: as much of the full crafting system as can be built 1:1 this phase (crafting-grid definitely; smelting/stonecutting blocks built if feasible, else the matcher ships + the block defers with a reason). Every deferral cited, not silent.

### Consume locus — Go applies the plugin's deltas (1:1 ResultSlot.onTake)
- The plugin does the MATCH (grid → {result stack, which cells used}); the plugin returns the result + the per-cell consume info. GO applies the consume over the tick-owned inventory: the 1:1 ResultSlot.onTake port — `removeItem(cell, 1)` per positioned used cell + `getRemainingItems` bucket-back (e.g. the bucket left after crafting a cake). Inventory mutation stays in the tick-owned seam. NOT "plugin returns a whole new grid" (less faithful — the jar consume is per-cell).

### The plugin↔menu bridge — a NEW value-returning Match Call (the dogfood point)
- Phase 22's host gives fire-and-forget Emit. Phase 25 adds a value-returning `set_recipe_matcher(fn)` builtin + a Go `Match(grid) → result` seam (a thin extension of Emit — proving the API generalizes from event-DISPATCH to query-RESOLUTION). The recipe plugin registers a matcher; the menu's result slot calls it (on the tick goroutine) with the current grid (plain (id,count) scalars), gets back the result + consume deltas.
- The grid handle Phase 25 needs = the 9 (or N) input stacks as frozen scalars + a result — NOT a live entity handle. Reuse the Phase-22 frozen-payload pattern.

### Vanilla recipe plugin — embedded by default (//go:embed, boot-load)
- The vanilla recipe-provider plugin is `//go:embed`'d + boot-loaded (like the Phase-24 vanilla_pig). Vanilla crafting works out-of-the-box. A custom recipe = a plugin adds one the same way (the gate proves a custom recipe works).

### Un-stub ResultSlot.onTake
- The exact stub to un-stub: `server/inventory_click.go:258-260` — ResultSlot.onTake is the cited no-op ("no recipes wired in v1"). The 2×2 player grid (slots 1-4, result slot 0, mayPlace(0)=false) already exists. Wire it to the matcher + the 1:1 consume.

### Where the code lives
- `level/recipe/` (the data extraction + parse + the embed, the level/loot twin) + the match port. The plugin-bridge (set_recipe_matcher + Match) extends plugin/host. The menu/block work + the un-stub live in server (the chest-open subsystem from Phase 20 is the clone template: nextContainerCounter → openScreen → ContainerSetContent → click engine → close). The recipe-provider .star + manifest bundled.
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 25 row + gate (crafting THROUGH the plugin API, the ResultSlot no-op stub, crafting_table 3×3, 1:1 carries, custom recipes free).
- `.planning/REQUIREMENTS.md` — PLUGIN-05 full.
- `.planning/phases/25-crafting-recipes-as-plugins/25-RESEARCH.md` — HIGH-confidence: the exact stub (inventory_click.go:258-260), the jar match/consume javap'd (shaped shrink+mirror, shapeless multiset, ResultSlot.onTake removeItem+getRemainingItems), the recipe-data embed (level/loot twin, 1129 files), the menu = the Phase-20 chest-open clone, the value-returning Match seam, Phase-23-independence.
- `.planning/phases/22-plugin-host-event-bus/22 SUMMARYs` + `plugin/host/*.go` — the host + the register pattern + the frozen-payload discipline the Match seam extends.
- `.planning/phases/20-...` chest-open subsystem: `server/chest_open.go`/`chest_click.go` (nextContainerCounter, openScreen, ContainerSetContent, the click engine) — the menu template to clone for the 3×3.
- The stub + click engine: `server/inventory_click.go` (the menuSlot/doClick 1:1 port, ResultSlot.onTake stub, safeInsert/safeTake/tryRemove), the 2×2 grid slots.
- `level/loot/embed.go` — the //go:embed pattern to twin for recipes.
- `CLAUDE.md` — **1:1 mandate (APPLIES to the match+consume — javap, cite)**, CGO=0, -race Docker, push development, TICK-05.
- The jar: `temp/cache/26.2-inner.jar` via javap: `net.minecraft.world.item.crafting.{RecipeManager,ShapedRecipe,ShapedRecipePattern,ShapelessRecipe,CraftingInput,StackedItemContents,SmeltingRecipe,AbstractCookingRecipe}`, `net.minecraft.world.inventory.{CraftingMenu,ResultSlot,CraftingContainer}`.
</canonical_refs>

<specifics>
## Specific Ideas

- The gate: a vanilla recipe crafts through the plugin path (e.g. planks→sticks shapeless, or a shaped recipe — result + per-cell consume, jar-verified) AND a custom recipe (a plugin adds a non-vanilla recipe) works.
- The 3 load-bearing 1:1 pitfalls (from research): shaped bounding-box SHRINK, shaped MIRROR-then-unmirror, the 1-per-cell CONSUME (removeItem per used cell, getRemainingItems bucket-back). javap-verify each.
- The recipe data extraction mirrors level/loot exactly — same embed + parse pattern, just the recipe/ JSONs.
- The Match seam is value-returning (Emit was void) — the new API shape proving query-resolution. It runs on the tick goroutine (the result slot is tick-owned); the matcher plugin call is bounded by the step budget like any hook.
- If smelting's furnace block is deferred, the smelting matcher still ships + is unit-tested (a test feeds it an iron_ore + asserts iron_ingot) — the deferral is the BLOCK/menu, not the recipe logic.

## Open items the planner resolves
- Furnace/blast/smoker/campfire/stonecutter block+menu: build-minimal-1:1 vs ship-matcher-defer-block (per type, with cited reason).
- The exact Match seam signature + the grid payload shape.
- The recipe-type parse scope (all types' data parsed; the match for each).
- Bundling the vanilla recipe plugin (//go:embed the .star + the recipe data).
</specifics>

<deferred>
## Deferred Ideas

- Any cooking/stonecutting BLOCK whose block-entity+tick subsystem is too large to build faithfully this phase → deferred WITH a cited reason (the recipe matcher ships; the block defers).
- Python runtime → Phase 26; Folia → Phase 27.
- Brewing/anvil/enchanting/loom/smithing menus (not "crafting" recipes) → out of scope.
</deferred>

---

*Phase: 25-crafting-recipes-as-plugins*
*Context gathered: 2026-06-28 via operator decisions + v4-PLAN.md + 25-RESEARCH.md*
