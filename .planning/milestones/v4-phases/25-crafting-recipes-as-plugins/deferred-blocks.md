# Phase 25 — Cooking/Stonecutting BLOCK build-or-defer audit (PLUGIN-05, Plan 25-03)

**Audited:** 2026-06-28
**Rule:** the no-built-but-unwired rule + the build-or-defer-per-block mandate (25-CONTEXT.md
"Recipe scope"). The recipe DATA + 1:1 MATCH for ALL these types already SHIPPED + is tested in
Plan 25-01 (`level/recipe` + the plugin `Manager.Match` seam). This audit decides, per type and ON
EVIDENCE, whether its BLOCK + MENU (+ cooking TICK) is faithfully buildable in this phase's budget
or must be DEFERRED — the matcher ships either way, only the block UI is the deferral.

**Authority for any defer:** one of the three legitimate reasons (CLAUDE.md / the planner-authority
rule) — `context-cost` (>~50% of one agent's budget), `missing-information`, or
`dependency-conflict`. NOT "too hard". Every defer below cites `context-cost`: a large unbuilt
subsystem (block-entity per-tick drive + fuel table) that does not exist in the codebase.

All jar sizing via `javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>` against the
unobfuscated 26.2 (protocol 776) server jar.

---

## Per-Type Decisions

| Type | Decision | Jar class(es) | Missing subsystem (for DEFER) | Codebase evidence | Cost reason |
|------|----------|---------------|-------------------------------|-------------------|-------------|
| **stonecutting** | **BUILD** | `net.minecraft.world.inventory.StonecutterMenu` | — (no fuel, no tick, no block-entity) | The menu is a single-input + result + recipe-picker; clones the Plan-25-02 `crafting_menu.go`/`chest_open.go` scaffolding. No new per-tick subsystem. Buildable now. | n/a (built) |
| **smelting** | **DEFER** | `net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity` (`FurnaceBlockEntity`), `.serverTick`, `net.minecraft.world.inventory.FurnaceMenu` | a furnace BLOCK-ENTITY with `litTimeRemaining`/`litTotalTime`/`cookingTimer`/`cookingTotalTime` fields + NBT load/save + the per-tick `serverTick` COOKING drive + a `FuelValues`/`getBurnDuration` FUEL TABLE + the `LIT` blockstate transition + `ClientboundContainerSetData` for the 4 furnace DataSlots — NONE exist | (1) NO block-entity is ticked: there is no `serverTick`/`BlockEntityTick` seam anywhere (`grep -rln "serverTick\|BlockEntityTick" server/ level/` → 0 matches; the only chest persistence runs on OPEN/CLOSE, not a tick). (2) NO fuel table: `grep -rl "FuelValues\|getBurnDuration\|BURN_TIME" server/ level/ data/` → 0 matches. (3) Block-entity types are EMPTY struct markers only (`level/block/blockentities.go`: `FurnaceEntity struct{}` — no fields). (4) NO `ClientboundContainerSetData` encoder (`grep -rln "ContainerSetData" server/ net/` → only the packet-id enum in `data/packetid`, no encoder). | `context-cost` — building the furnace requires standing up an ENTIRE new per-tick block-entity drive subsystem (the `serverTick` is 203 bytecode ops + `canBurn`/`burn`/`consumeFuel`/`getBurnDuration`/`getTotalCookTime` + the fuel table + the `LIT` state + experience tracking + BE NBT persistence). That far exceeds ~50% of one agent's budget. A separate feature per ROADMAP ("smelting needs a furnace block+menu+cook-tick subsystem"). |
| **blasting** | **DEFER** | `AbstractFurnaceBlockEntity` (`BlastFurnaceBlockEntity`), `.serverTick`, `net.minecraft.world.inventory.BlastFurnaceMenu` | identical to smelting — `BlastFurnaceBlockEntity extends AbstractFurnaceBlockEntity` (same `serverTick`, same fuel+progress machinery, 2× cook speed via `getTotalCookTime`); the SAME missing block-entity-tick + fuel-table subsystem | same as smelting (one shared `AbstractFurnaceBlockEntity.serverTick`; `BlastFurnaceEntity struct{}` is an empty marker in `level/block/blockentities.go`) | `context-cost` — rides the SAME unbuilt furnace subsystem as smelting; cannot build without it. |
| **smoking** | **DEFER** | `AbstractFurnaceBlockEntity` (`SmokerBlockEntity`), `.serverTick`, `net.minecraft.world.inventory.SmokerMenu` | identical to smelting — `SmokerBlockEntity extends AbstractFurnaceBlockEntity` (same `serverTick`, 2× cook speed); the SAME missing block-entity-tick + fuel-table subsystem | same as smelting (`SmokerEntity struct{}` empty marker) | `context-cost` — rides the SAME unbuilt furnace subsystem. |
| **campfire_cooking** | **DEFER** | `net.minecraft.world.level.block.entity.CampfireBlockEntity`, `.cookTick`, `net.minecraft.world.level.block.CampfireBlock` | a CampfireBlockEntity with a per-position `cookingProgress[4]` + `cookingTime[4]` array, the `cookTick`/`particleTick` per-tick drive, the place-onto-campfire `placeFood` interaction (NOT a menu — a direct hit-item placement), and the same absent per-tick block-entity seam; campfire has NO fuel slot but STILL needs the per-tick cooking drive + the multi-item visual state + dropping the result as an ItemEntity | (1) the same missing per-tick block-entity drive (`grep -rln "serverTick\|BlockEntityTick"` → 0). (2) `CampfireEntity struct{}` is an empty marker (`level/block/blockentities.go`). (3) the campfire interaction is a use-item-on-block placement onto a 4-slot BE, a path that does not exist (the only block-entity interaction wired is the chest OPEN). | `context-cost` — needs the same absent per-tick block-entity drive PLUS a net-new place-food-onto-block interaction + a 4-slot per-tick cooking array + result-drop-as-ItemEntity. Exceeds budget; rides no existing seam. |

---

## The matcher-ships / block-defers split (explicit)

For EVERY deferred type the **recipe matcher is READY and TESTED** — Plan 25-01 shipped:

- `level/recipe.ParseAll()` parses all cooking subtypes: **smelting 73, blasting 25, smoking 9,
  campfire 9** recipes (the 25-01 census), plus **stonecutting 319**.
- `level/recipe.MatchCooking` / `MatchStonecutting` (1:1 `SingleItemRecipe.matches`) are ported +
  unit-tested (`level/recipe/match_test.go`).
- The plugin path `Manager.Match` already tries cooking/stonecutting for a 1×1 grid
  (`server/assets/crafting/main.star` `_matches_single`; `level/recipe/match.go:305` "Single-cell
  grid: try the cooking + stonecutting single-ingredient recipes").

So the deferral is **ONLY the BLOCK / menu / cooking-tick UI** — never the recipe logic. A
`TestSmeltingMatcherShips` test (Task 2) feeds the smelting matcher an `iron_ore` and asserts
`iron_ingot`, proving the deferred types still resolve headless through the SAME plugin seam the
built stonecutter and the Plan-02 crafting grid use.

## What gets BUILT this plan (stonecutting)

`StonecutterMenu` is the one buildable block: a single INPUT slot (slot 0) + a RESULT slot (slot 1)
+ the 36 player slots, with a recipe-picker (`selectByInput` → the list of stonecutter results for
the input; `clickMenuButton` selects an index; `setupResultSlot` shows the chosen result; the
result-take consumes exactly 1 input). It has **no fuel, no cooking tick, no block-entity** — it
clones the Plan-25-02 `openContainer.kind` menu scaffolding (`containerKindStonecutter`), adds the
two small wire pieces it needs (`ServerboundContainerButtonClick` decode/dispatch — 2 VarInts; and
`ClientboundContainerSetData` for the `selectedRecipeIndex` DataSlot — 3 VarInts), and drives the
result list off the Go-parsed `recipe` tree (the same parse `LoadCraftingPlugin` already runs),
validating the taken result through the plugin `Manager.Match` 1×1 stonecutting path. Built in
Task 2 with `TestStonecutter*`. Cited: `StonecutterMenu.slotsChanged` / `setupRecipeList` /
`clickMenuButton` / `setupResultSlot` / `quickMoveStack` / `removed`.

## Threat-register note (T-25-13, the cooking-tick DoS disposition)

T-25-13 ("cooking tick on the hot path") is resolved as **N/A** for this plan: no cooking/furnace
block was built, so there is no cook tick on any path. If a furnace is built in a future phase, the
cook tick MUST ride a bounded per-position block-entity drive (the absent subsystem this audit
defers), never a per-tick world scan.
