---
phase: 25-crafting-recipes-as-plugins
plan: 01
subsystem: recipe-engine + plugin-host-match-seam
tags: [recipes, crafting, plugin-host, 1-to-1-port, dogfood, embed]
requires:
  - "plugin/host (Phase-22 Manager + register pattern + frozen-payload discipline)"
  - "plugin/starlark (Phase-21 NewThread + step budget)"
  - "data/registryid.Item (name->id index)"
  - "level/loot/embed.go (the //go:embed pattern twinned)"
provides:
  - "level/recipe: ParseAll() over the embedded jar recipe tree (all 7 matchable types + special markers)"
  - "level/recipe: the 1:1 Match (ofPositioned shrink, MatchShaped mirror, MatchShapeless multiset, MatchCooking/MatchStonecutting single-ingredient) + the per-cell used mask"
  - "plugin/host: the value-returning Match/Remaining seam (set_recipe_matcher + set_recipe_remaining + recipes builtins + SetRecipeTable)"
affects:
  - "plugin/host/manager.go (Manager gains recipeMatcher/recipeRemaining/recipeTable/recipeOwner; LoadDirWith predeclared set; Unload)"
tech-stack:
  added: []
  patterns:
    - "//go:embed the whole jar data tree (recipe/ + item_tags/) like level/loot"
    - "value-returning starlark.Call seam reading {id,count} back into Go (extends void Emit)"
key-files:
  created:
    - level/recipe/model.go
    - level/recipe/embed.go
    - level/recipe/parse.go
    - level/recipe/match.go
    - level/recipe/recipe_test.go
    - level/recipe/match_test.go
    - level/recipe/data/recipe/ (1585 JSON files)
    - level/recipe/data/item_tags/ (224 JSON files)
    - plugin/host/recipe.go
    - plugin/host/recipe_test.go
    - plugin/host/testdata/plugins/{recipematcher,runawaymatcher,badmatcher}/
  modified:
    - plugin/host/manager.go
decisions:
  - "Item tags embedded INTO level/recipe (copied from server/registrydata/tags/item) to keep level/recipe pure (no server import); #tag refs expanded recursively"
  - "Special types (crafting_special_*, crafting_dye/transmute/imbue/decorated_pot, smithing_*) parsed as recorded markers (not matchable) via a type-first lightweight decode, because smithing_trim's pattern is a STRING not a list"
  - "Shapeless multiset cover expressed as bipartite matching (Kuhn) — order-independent accept/reject identical to StackedItemContents.canCraft for the crafting-grid case"
metrics:
  duration: 11min
  tasks: 3
  files: 13
  completed: 2026-06-28
---

# Phase 25 Plan 01: Recipe Engine + Value-Returning Match Seam Summary

The two pure, server-independent halves of the crafting dogfood: `level/recipe`
embeds + parses ALL vanilla recipe types from the jar JSON and ports the 1:1
match (shaped shrink+mirror, shapeless multiset, cooking/stonecutting
single-ingredient), and `plugin/host` gains the NEW value-returning `Match` seam
(`set_recipe_matcher` + `Manager.Match`/`Remaining`) that extends Phase-22's void
`Emit` into query-resolution.

## The recipe model shape

- `Recipe` is a discriminated union by `Type` (shaped / shapeless / cooking /
  stonecutting / special).
- `Ingredient` is a SET of item ids (`map[int]struct{}`) — the resolution of a
  bare id (1-element), a `#tag` (the tag's member set), or a list (the union).
  An EMPTY ingredient = the empty-optional pattern cell.
- `Shaped{Width,Height,Ingredients(row-major),Symmetrical,IngredientCount,Result}`
  — Width/Height are the trimmed pattern dims (the jar's datagen pre-shrinks the
  on-disk pattern, so the embedded dims ARE the recipe dims). `Symmetrical` =
  `isSymmetrical` (a 1:1 port of `Util.isSymmetrical` — pattern equals its
  horizontal mirror).
- `Shapeless{Ingredients,Result}`, `Cooking{Ingredient,Result,Subtype}`,
  `Stonecutting{Ingredient,Result}`, `Stack{ID,Count}` (count defaults to 1).

## The parsed type census (>= the datagen census; tolerance assert)

shaped 733, shapeless 323, smelting 73, blasting 25, smoking 9, campfire 9,
stonecutting 319 — plus the special markers (crafting_special_* ×24,
crafting_dye 6, crafting_transmute 33, crafting_decorated_pot 1, crafting_imbue
1, smithing_transform 12, smithing_trim 18) recorded but not matchable. ParseAll
walks all 1585 files without erroring; an unknown type or unknown item name is a
LOUD `fmt.Errorf` (T-25-02), never a silent drop.

## The exact match algorithm (javap'd vs 26.2-inner.jar — the 3 pitfalls)

All four match algorithms were `javap -c -p`'d this session and ported
method-for-method (each cites its jar method in match.go):

1. **Bounding-box SHRINK** — `ofPositioned` ports
   `CraftingInput.ofPositioned`: scan for min/max non-empty row+col
   (minCol=w-1/maxCol=0/minRow=h-1/maxRow=0 init), trimmedW=maxCol-minCol+1,
   trimmedH=maxRow-minRow+1; all-empty -> `Empty`; no-trim -> the whole grid +
   (minCol,minRow); else build the trimmed sub-grid from
   `items[(c+minCol)+(r+minRow)*w]`. **Pitfall 1 closed** (a 2x1 stick matches
   anywhere in a 3x3).
2. **MIRROR-then-unmirror** — `MatchShaped` ports `ShapedRecipePattern.matches`:
   `ingredientCount` gate, `width/height` gate, then
   `if (!symmetrical && scan(true)) return true; return scan(false)`; `scan`
   double-loops row<height/col<width with index
   `(mirror ? width-col-1 : col) + row*width`, testing
   `testOptionalIngredient(opt, cell)` (empty optional matches empty cell).
   **Pitfall 2 closed**.
3. **Shapeless MULTISET** — `MatchShapeless` ports `ShapelessRecipe.matches`:
   `ingredientCount == len(ingredients)` gate, the 1-ingredient fast path
   (`ingredients[0].test(getItem(0))`), else the greedy multiset cover
   (`StackedItemContents.canCraft`) expressed as bipartite matching (Kuhn),
   order-independent.
4. **Single-ingredient** — `MatchCooking` / `MatchStonecutting` port
   `SingleItemRecipe.matches` (`input.test(item)`); cooking is
   `AbstractCookingRecipe extends SingleItemRecipe`.

## The Match seam signature + the per-cell used mask (Wave 2 needs this)

`level/recipe.Match(recipes []Recipe, cells []Stack, w, h int) (result Stack, used []bool, ok bool)`
— runs `ofPositioned` once, iterates recipes by type (shaped/shapeless for the
grid; cooking/stonecutting only for a 1x1 grid), returns the FIRST match's
result + a **per-cell `used` mask mapped back through (Left,Top) to the REAL
w-wide grid positions**. The used mask is what the Wave-2 `ResultSlot.onTake`
consume reads to `removeItem(cell, 1)` per used cell. `Remaining(cells)` ports
`CraftingRecipe.defaultCraftingReminder` (all-empty; the per-item bucket-back is
the structured-but-default path — cited).

`plugin/host.Manager.Match(grid starlark.Value) (id, count int, ok bool)` — the
value-returning seam: nil-matcher fast path; fresh budget-bounded
`starlarkpkg.NewThread` + recover (Emit-parity isolation); `starlark.Call` reads
back a `{id,count}` dict. `Remaining` is the same shape for the leftover read.
`SetRecipeTable(v)` + the `recipes()` builtin are the Go->plugin recipe channel
(Starlark has no json/file builtin).

## The value-read symbols used (verified at the pin)

`go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb` — confirmed present:
`starlark.Call` (eval.go:1194), `starlark.AsInt32` (int.go:358),
`(*starlark.Dict).Get` (value.go:896), `EvalError` (eval.go:243). `readResult`
validates `id>0 && count>0` (T-25-03) so a bogus/negative/non-dict result yields
`ok=false`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Type-first decode for special markers.**
- **Found during:** Task 1 (TestRecipeParse failed on a smithing_trim file).
- **Issue:** `smithing_trim`'s `"pattern"` field is a STRING (`"minecraft:bolt"`),
  not a `[]string`, so a single rigid `rawRecipe` decode over the whole tree
  failed.
- **Fix:** Decode only the `"type"` first (a lightweight pass), return the
  SPECIAL types as markers BEFORE the rigid type-specific decode. The matchable
  types decode their full shape as before.
- **Files modified:** level/recipe/parse.go.
- **Commit:** ff62c439.

**2. [Rule 2 - Critical] Item tags embedded into level/recipe.**
- **Found during:** Task 1 (planning purity boundary).
- **Issue:** `#tag` ingredients (e.g. `#minecraft:planks`) must resolve to a
  member-id set, but the only item-tag source (`server/registrydata/tags/item`)
  lives in `package registrydata` with an unexported FS — importing it would
  violate the level/recipe purity boundary (no server import).
- **Fix:** Copied the item-tag tree into `level/recipe/data/item_tags` and
  `//go:embed`'d it alongside the recipe tree; recursive `#tag`-of-`#tag`
  expansion in `expandTag`.
- **Commit:** ff62c439.

## Deferred / Notes

- **Remaining (getRemainingItems) bucket-back** is the default all-empty path
  (`CraftingRecipe.defaultCraftingReminder`). The per-item bucket-back
  (`Item.getCraftingRemainder` — e.g. the empty bucket after a cake) is
  STRUCTURED but returns empty for the gate recipes; Wave 2 wires the real
  per-item remainder when a bucket recipe is in the gate.
- **No server changes in this plan** (per the plan objective). The crafting_table
  3x3 menu, the un-stub of `ResultSlot.onTake`, and the vanilla recipe-provider
  `.star` plugin are Wave 2 — they consume this Match seam + the parsed recipes.

## Verification

- `CGO_ENABLED=0 go build ./...` exit 0 (full tree, static).
- `CGO_ENABLED=0 go vet ./level/recipe/ ./plugin/host/` clean.
- `CGO_ENABLED=0 go test ./level/recipe/ ./plugin/host/` all green (5 parse + 5
  match + 6 seam tests).
- Citations present: `grep ShapedRecipePattern level/recipe/match.go` (5),
  `grep 'func (m \*Manager) Match' plugin/host/recipe.go`.

## Self-Check: PASSED
