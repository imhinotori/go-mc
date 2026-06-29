---
phase: 25-crafting-recipes-as-plugins
plan: 02
subsystem: crafting-menu + result-slot-consume + embedded-recipe-plugin
tags: [crafting, recipes, plugin-host, 1-to-1-port, dogfood, embed, menu, chest-clone]
requires:
  - "level/recipe (Plan 01: ParseAll + the 1:1 match + OfPositioned shrink)"
  - "plugin/host.Manager.Match/Remaining/SetRecipeTable/HasRecipeMatcher (Plan 01 seam)"
  - "server/inventory_click.go (the menuSlot/doClick engine + the ResultSlot.onTake stub)"
  - "server/chest_open.go + chest_click.go (the Phase-20 chest-open subsystem = the menu clone template)"
  - "server/vanilla_pig_embed.go (the //go:embed + boot-load twin)"
provides:
  - "server: ResultSlot.onTake UN-STUBBED — the 2x2 player grid + the 3x3 crafting_table both craft through the plugin Match path with a 1:1 per-cell consume"
  - "server: crafting_table block + the 3x3 CraftingMenu (the chest-open clone; openContainer.kind discriminator; close-returns-grid)"
  - "server: //go:embed'd vanilla crafting plugin + LoadCraftingPlugin boot-load (a default server crafts out-of-the-box)"
  - "the matcher-fallthrough custom-recipe model (an operator plugin adds a non-vanilla recipe)"
affects:
  - "cmd/sulfur/main.go (a single runtime Manager always boot-loads the embedded crafting plugin; plugins/ loads on top)"
  - "server/inventory.go (clicked() routes a crafting window; handleContainerClose returns the grid)"
  - "server/inventory_click.go + inventory_doclick.go (result-slot takes route through slotOnTake)"
  - "level/recipe/match.go (export OfPositioned)"
tech-stack:
  added: []
  patterns:
    - "craftView abstraction (one Match/onTake engine over the 2x2 player grid AND the 3x3 menu grid)"
    - "the chest-open subsystem cloned for the crafting menu (openContainer.kind discriminator)"
    - "the embedded-plus-operator Manager: embedded vanilla matcher first, operator plugins/ on top (last-loaded matcher wins)"
key-files:
  created:
    - server/recipe_embed.go
    - server/crafting_menu.go
    - server/crafting_click.go
    - server/assets/crafting/plugin.toml
    - server/assets/crafting/main.star
    - plugins/crafting/plugin.toml
    - plugins/crafting/main.star
    - plugins/customrecipe/plugin.toml
    - plugins/customrecipe/main.star
    - server/crafting_click_test.go
    - server/crafting_menu_test.go
    - server/crafting_gate_test.go
  modified:
    - server/inventory_click.go
    - server/inventory_doclick.go
    - server/inventory.go
    - server/chest_open.go
    - cmd/sulfur/main.go
    - level/recipe/match.go
decisions:
  - "GO applies the consume over the tick-owned grid (the locked CONTEXT decision): onTakeCraft re-derives the asPositionedCraftInput footprint via the exported level/recipe.OfPositioned (the SAME 1:1 shrink the matcher used) and removes exactly 1 per non-empty cell — NOT a whole-new-grid return, NOT a plugin-supplied used mask"
  - "The custom-recipe registration model = matcher-fallthrough: set_recipe_matcher is last-wins (single matcher), so the operator customrecipe plugin (loaded after crafting) registers the FINAL matcher that checks its custom recipes first, then falls through to the SAME 1:1 vanilla algorithm over recipes()"
  - "The 3x3 crafting grid is a TRANSIENT openContainer.craftGrid (no block-entity); close returns it to the player (CraftingMenu.removed -> clearContainer), NOT the chest persist model"
  - "A single Manager always carries the embedded vanilla crafting plugin (LoadCraftingPlugin, FATAL on failure); the operator plugins/ dir loads on top so a default server crafts even with no plugins/ dir"
metrics:
  duration: 17min
  tasks: 3
  files: 18
  completed: 2026-06-28
---

# Phase 25 Plan 02: Crafting Menu + Result-Slot Consume + Embedded Recipe Plugin Summary

The crafting ENGINE wired to the GAME: `ResultSlot.onTake` is un-stubbed, the
crafting_table block opens a 3x3 menu (a clone of the Phase-20 chest-open
subsystem), the vanilla recipe-provider plugin is `//go:embed`'d + boot-loaded,
and THE GATE is green — a vanilla recipe AND a custom operator recipe both craft
through the plugin `Match` path with a 1:1 per-cell consume.

## The un-stub site + the consume port

`server/inventory_click.go:258-260` was the cited `ResultSlot.onTake` no-op ("no
recipes wired in v1"). It is now wired through a NEW TickLoop-bound dispatch
`slotOnTake(p, inv, slotIndex, taken)`: for the RESULT slot (index 0) it fires
`t.onTakeCraft` (the 1:1 consume); any other slot is the base setChanged no-op.
The player engine's three result-take sites (the two PICKUP arms in
`inventory_doclick.go` + the shift-click result arm in `quickMoveStack`) now call
`slotOnTake` instead of the bare `menuSlot.onTake`.

`onTakeCraft` (server/crafting_click.go) is the literal `ResultSlot.onTake`
bytecode port (javap'd this session): `asPositionedCraftInput()` shrink (the
exported `level/recipe.OfPositioned`), `getRemainingItems` (the all-empty
`defaultCraftingReminder` default), then the `for row<input.height / col<input.width`
double-loop with `slot=(col+left)+(top+row)*gridWidth`, `removeItem(slot,1)` per
non-empty cell, and the remaining bucket-back (`setItem` / `grow` /
`inventory.add` / `drop`). The 1-per-cell consume is the dupe/eat-bug pitfall —
`TestResultSlotConsume`/`TestConsumePositioned` assert exactly 1 removed from each
POSITIONED used cell (mapped via Left/Top), never the whole stack, never the
wrong cell.

`slotChangedCraftingGrid` (CraftingMenu.slotChangedCraftingGrid) recomputes the
result on every grid change + after every take (chained crafting) via
`t.plugins.Match(craftGridPayload(v))` — a nil matcher / no match clears slot 0.

## The crafting_table menu (the chest-clone deltas)

`openContainer` gained a `kind` discriminator (`containerKindChest` /
`containerKindCrafting`) and a transient `craftGrid [9]SlotData` +
`craftResult`. `useBlockInteraction` (chest_open.go) now handles crafting_table
alongside chest behind the same reach gate. `openCraftingTable`
(server/crafting_menu.go) clones `openChest`: `nextContainerCounter` →
`OpenScreen(minecraft:crafting)` → `ContainerSetContent` (the 46-slot layout:
result 0, grid 1-9, main 10-36, hotbar 37-45). The 3x3 click engine
(server/crafting_click.go: `craftingResolveSlot`/`clickedCrafting`/
`doCraftingClick` PICKUP/QUICK_MOVE/THROW) clones the chest engine; the result
slot (0) is take-only (`craftSlotRef.set` ignores a place into it) and a take
fires `onTakeCraft` (w=h=3 over the craftGrid). `clicked()` routes a crafting
window to `clickedCrafting`. **Close returns the grid** (Pitfall 7):
`handleContainerClose` calls `closeCraftingWindow` (the
`AbstractContainerMenu.clearContainer` port: `removeItemNoUpdate` each cell →
`invAdd` else `playerDrop`) BEFORE freeing the window — NOT the chest persist
model. The 2x2 player grid + the 3x3 menu reuse the SAME engine via the
`craftView` abstraction (different getCell/setCell closures, w/h).

## The embedded boot-load wiring

`server/recipe_embed.go` (the vanilla_pig_embed twin): `//go:embed
assets/crafting/{plugin.toml,main.star}`; `LoadCraftingPlugin(mgr)` (1) parses
the whole vanilla recipe tree (`level/recipe.ParseAll`), (2) converts it to the
Starlark table the plugin matcher reads (`recipeTable` — per-recipe dicts with
ingredient-membership sets) and injects it via `SetRecipeTable`, (3) materializes
+ `LoadDirWith` the embedded plugin, (4) FAILS LOUDLY if `HasRecipeMatcher()` is
false (the Pitfall-4 twin). The embedded `assets/crafting/main.star` re-expresses
the 1:1 jar match (ofPositioned shrink, ShapedRecipePattern mirror-then-unmirror,
shapeless multiset bipartite cover, single-ingredient cooking/stonecutting) in
Starlark over the host-injected table — so the result + consume come from the
PLUGIN, not a hardcoded Go table. `cmd/sulfur/main.go` always builds one Manager,
boot-loads the embedded plugin (FATAL on failure), then loads the operator
`plugins/` dir ON TOP — so a default server crafts even with no `plugins/` dir.

## The custom-recipe registration model chosen — matcher-fallthrough

`set_recipe_matcher` captures a SINGLE matcher (last-loaded wins, manager.go).
Plugins load in directory order (`crafting` < `customrecipe`), so the operator
`plugins/customrecipe` plugin registers the FINAL matcher. To keep every vanilla
recipe working it checks its CUSTOM recipes (1 dirt → 1 diamond, direct ids)
FIRST, then FALLS THROUGH to the SAME 1:1 vanilla algorithm over `recipes()` (the
match helpers re-included — Starlark plugins cannot import one another). This is
the operator-extension dogfood: drop the plugin into `plugins/`, a brand-new
recipe crafts through the exact Match seam the vanilla recipes use, zero server
changes.

## THE GATE — results

- `TestVanillaRecipeCrafts`: 2 planks (vertical) in the 2x2 grid → result slot 0
  = 4 sticks via `Manager.Match` (NOT a hardcoded Go table); take → exactly 1 per
  used cell.
- `TestCustomRecipeWorks`: 1 dirt → 1 diamond through the custom matcher, AND
  vanilla (2 planks → 4 sticks) still crafts through the same fallthrough matcher.
- `TestEmbeddedBootLoadDefault`: with NO operator `plugins/` dir, a default server
  still crafts (the embedded boot-load).
- `TestCraftingTableOpen/Crafts/ResultMayPlaceFalse/Close`: the 3x3 menu opens
  (`minecraft:crafting`, 46 slots), crafts through the same Match path, the result
  is take-only, close returns the grid (no loss).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] TestNoMatchEmptyResult used a matching grid.**
- **Found during:** Task 1 (the test asserted a single oak_planks yields no result).
- **Issue:** A single oak_planks DOES match a vanilla 1-ingredient shapeless
  recipe (verified against the `level/recipe` Go oracle: result id 779) — so the
  plugin correctly returned a result and the test's no-match assumption was wrong,
  not the code.
- **Fix:** Switched the no-match grid to a single dirt + two dirt (verified
  genuine non-matches against the Go oracle). The plugin's match is faithful 1:1.
- **Files modified:** server/crafting_click_test.go.
- **Commit:** 9c3f3281.

### Plan-shape notes (not deviations)

- The plan's "from Match's used mask" consume: `Manager.Match` returns only
  `(id,count,ok)` (no used mask). The faithful jar `onTake` does NOT consume a
  match-supplied mask — it independently re-shrinks via `asPositionedCraftInput`
  and removes 1 per non-empty cell in the footprint. So the consume re-derives the
  footprint via the exported `level/recipe.OfPositioned` (the same 1:1 shrink),
  which is MORE faithful than threading a mask through the value-seam. The result
  still comes from the plugin Match; only the footprint geometry is recomputed
  identically.

## Note for Plan 03 (cooking/stonecutting blocks)

The menu/block scaffolding here is directly reusable for the furnace/stonecutter
blocks: `openContainer.kind` (add `containerKindFurnace` etc.), the
`craftingResolveSlot`/`craftSafeInsert`/`sendCraftingContent` pattern, and the
`craftView` engine (a cooking block feeds a 1-cell grid to the SAME
`Manager.Match`, which already tries cooking/stonecutting for a 1x1 grid). The
embedded-plugin boot-load (`LoadCraftingPlugin`) is the template for a bundled
cooking-recipe provider. The `useBlockInteraction` one-hook/many-block-kinds shape
extends to the new blocks. `Manager.Remaining` is wired but kept all-empty (the
gate recipes carry no bucket-back); a furnace/bucket recipe is where the real
per-item remainder gets read.

## Verification

- `CGO_ENABLED=0 go build ./...` exit 0 (static, no import "C").
- `CGO_ENABLED=0 go vet ./server/ ./cmd/... ./level/recipe/ ./plugin/...` clean.
- `CGO_ENABLED=0 go test ./server/ ./plugin/... ./level/recipe/` all green
  (Task 1: 4 consume/boot tests; Task 2: 4 menu tests; Task 3: 3 gate tests).
- Docker `-race` (CGO=1) over `./plugin/... ./server/` clean (the menu click + the
  tick-owned consume + the Match Call are race-clean).
- Citations present: `ResultSlot.onTake` / `CraftingMenu.slotChangedCraftingGrid`
  in crafting_click.go; `CraftingTableBlock` / `CraftingMenu` in crafting_menu.go;
  `asPositionedCraftInput` on the exported `OfPositioned`.
- Acceptance greps: `onTakeCraft` in inventory_click.go; `//go:embed` in
  recipe_embed.go; `openCraftingTable` + `minecraft:crafting` in crafting_menu.go;
  `LoadCraftingPlugin` in cmd/sulfur/main.go; the "no recipes wired in v1" stub
  comment is GONE.

## Self-Check: PASSED
