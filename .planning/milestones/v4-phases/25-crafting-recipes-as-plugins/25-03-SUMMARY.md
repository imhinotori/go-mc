---
phase: 25-crafting-recipes-as-plugins
plan: 03
subsystem: crafting-blocks (stonecutter menu + cooking-block build-or-defer audit)
tags: [crafting, stonecutter, recipes, plugin-host, 1-to-1-port, menu, build-or-defer, dogfood]

requires:
  - phase: 25-01
    provides: "level/recipe.ParseAll (smelting/blasting/smoking/campfire/stonecutting parsed) + Manager.Match 1x1 cooking/stonecutting seam + MatchStonecutting/MatchCooking"
  - phase: 25-02
    provides: "openContainer.kind discriminator, the chest/crafting menu scaffolding (nextContainerCounter/openScreen/containerSetContent/the click engine/close-returns), LoadCraftingPlugin boot-load"
provides:
  - "server: the STONECUTTER block + its single-input recipe-picker MENU (open -> selectByInput list -> button-pick -> take consumes 1 input), 1:1 with StonecutterMenu, plugin-gated through Manager.Match"
  - "server: ServerboundContainerButtonClick decode+dispatch (the menu-button selection, previously unhandled)"
  - "server: ClientboundContainerSetData encoder (the selectedRecipeIndex DataSlot sync)"
  - "the build-or-defer DECISION LOG (deferred-blocks.md): stonecutting BUILT; smelting/blasting/smoking/campfire_cooking DEFERRED with cited reasons (no per-tick block-entity drive + no fuel table); every deferred matcher still tested headless"
affects: [future furnace/cooking-block phase, block-entity-tick subsystem, any new openable-block menu]

tech-stack:
  added: []
  patterns:
    - "the single-input recipe-PICKER menu (input -> server-side selectByInput list -> DataSlot-synced selection -> take), a 3rd openContainer.kind cloning the Plan-25-02 menu engine"
    - "plugin-gated + server-side-picked result: Manager.Match (1x1) GATES that an input is recipe-matchable (the dogfood); the SPECIFIC pick is the server-side parsed selectByInput list (vanilla selects server-side too)"
    - "evidence-based build-or-defer audit: javap-size the subsystem vs codebase-grep what exists; defer only on a legitimate cost reason with a citation, never silent/fake"

key-files:
  created:
    - server/stonecutter_menu.go
    - server/cooking_block.go
    - server/cooking_test.go
    - .planning/phases/25-crafting-recipes-as-plugins/deferred-blocks.md
  modified:
    - server/chest_open.go
    - server/inventory.go
    - server/subtick.go
    - server/slot_encode.go
    - server/recipe_embed.go

key-decisions:
  - "stonecutting BUILT, the four cooking types DEFERRED — on EVIDENCE: the furnace needs a per-tick block-entity drive (none exists) + a FuelValues fuel table (none exists); the stonecutter needs none of those"
  - "the stonecutter result is plugin-GATED (Manager.Match 1x1) but server-side-PICKED (selectByInput) — because Manager.Match returns only the FIRST 1x1 match, and a stone input has 11 stonecutter outputs the player picks from (vanilla picks server-side too)"
  - "the cooking deferral is the BLOCK UI only — every deferred matcher (Plan 01) is tested headless (TestSmeltingMatcherShips: iron_ore -> iron_ingot)"

patterns-established:
  - "single-input recipe-picker menu (input/result/picker) as a reusable openContainer.kind"
  - "ServerboundContainerButtonClick + ClientboundContainerSetData as the menu-selection wire pair"

requirements-completed: [PLUGIN-05]

duration: 30min
completed: 2026-06-28
---

# Phase 25 Plan 03: Cooking/Stonecutting BLOCK build-or-defer Summary

**Built the STONECUTTER block + its 1:1 single-input recipe-picker menu (open → `selectByInput` list → button-pick → take consumes 1 input), plugin-gated through `Manager.Match`; audited and DEFERRED the four cooking blocks (smelting/blasting/smoking/campfire) with cited evidence — no per-tick block-entity drive seam and no fuel table exist — keeping every deferred matcher tested headless.**

## Performance

- **Duration:** ~30 min
- **Tasks:** 2 (Task 1 audit; Task 2 TDD build = RED + GREEN)
- **Files modified:** 9 (4 created, 5 modified)

## Accomplishments

- **The evidence-based build-or-defer audit** (`deferred-blocks.md`): every one of the 5
  cooking/stonecutting types gets a recorded decision with a jar-class citation, the missing
  subsystem (for defers), file-level codebase evidence, and a legitimate cost reason. No silent
  drop, no fake furnace.
- **The stonecutter BUILT 1:1**: right-click a stonecutter → `minecraft:stonecutter` menu (38
  slots: input 0, result 1, player 2..37); placing an input builds the recipe list
  (`selectByInput`); a `ContainerButtonClick` selects a result (`clickMenuButton` →
  `setupResultSlot`, the `selectedRecipeIndex` DataSlot synced via `ContainerSetData`); taking the
  result consumes exactly 1 input (`ResultSlot.onTake`); close returns the input
  (`removed`/`clearContainer`).
- **Two previously-unhandled wire pieces added**: `ServerboundContainerButtonClick` decode+dispatch
  (no menu-button handler existed) and the `ClientboundContainerSetData` encoder.
- **The cooking-block deferral is the UI only**: `TestSmeltingMatcherShips` proves the deferred
  smelting matcher still resolves `iron_ore → iron_ingot` headless through the SAME plugin seam.

## Task Commits

1. **Task 1: audit cooking/stonecutting blocks** — `14966823` (docs)
2. **Task 2 RED: failing stonecutter + matcher-ships tests** — `9a5fee10` (test)
3. **Task 2 GREEN: build the stonecutter block + menu** — `6d2ca968` (feat)

## Files Created/Modified

- `.planning/.../deferred-blocks.md` — the per-type build-or-defer decision log (`## Per-Type Decisions`).
- `server/stonecutter_menu.go` — `openStonecutter`, the 38-slot menu builder, `stonecutterInputChanged`
  (`setupRecipeList`/`selectByInput`), `closeStonecutterWindow` (`removed`/`clearContainer`).
- `server/cooking_block.go` — the click engine (PICKUP/QUICK_MOVE/THROW), `handleContainerButtonClick`
  + `clickStonecutterButton` (`clickMenuButton`), `setupStonecutterResult`, `onTakeStonecut`
  (`ResultSlot.onTake` single-input consume), `stonecutterMatchPayload`, and the cooking-deferral marker.
- `server/cooking_test.go` — `TestStonecutterOpen/SelectAndCraft/ResultIsPluginMatch/Close` +
  `TestSmeltingMatcherShips`.
- `server/chest_open.go` — `containerKindStonecutter` + `cutInput/cutResult/cutResults/cutSelected`;
  `useBlockInteraction` opens a stonecutter.
- `server/inventory.go` — stonecutter click routing (`clicked`) + close routing (`handleContainerClose`).
- `server/subtick.go` — `ServerboundContainerButtonClick` dispatch case.
- `server/slot_encode.go` — `containerSetData` (`ClientboundContainerSetData`, the DataSlot sync).
- `server/recipe_embed.go` — `stonecutterRecipes()` cached parse (the `RecipeAccess.stonecutterRecipes`
  `selectByInput` source).

## Per-Type Decisions (mirrors deferred-blocks.md)

| Type | Decision | Why |
|------|----------|-----|
| stonecutting | **BUILD** | no fuel, no cooking tick, no block-entity — a single-input picker menu, buildable now. |
| smelting | **DEFER** (context-cost) | `AbstractFurnaceBlockEntity.serverTick` needs a per-tick block-entity drive (none exists) + a `FuelValues` fuel table (none exists) + BE fields/NBT (the BE types are empty struct markers) + `ContainerSetData` for 4 DataSlots. A large unbuilt subsystem. |
| blasting | **DEFER** (context-cost) | `BlastFurnaceBlockEntity extends AbstractFurnaceBlockEntity` — rides the SAME unbuilt furnace subsystem. |
| smoking | **DEFER** (context-cost) | `SmokerBlockEntity extends AbstractFurnaceBlockEntity` — rides the SAME unbuilt furnace subsystem. |
| campfire_cooking | **DEFER** (context-cost) | `CampfireBlockEntity.cookTick` needs the same absent per-tick BE drive + a 4-slot cooking array + a net-new place-food-onto-block interaction. |

**The matcher-ships split (explicit):** every deferred type keeps its Plan-01 matcher (parsed +
1:1-ported + unit-tested). The deferral is the BLOCK/menu/tick UI only. The codebase evidence behind
each defer: `grep -rln "serverTick\|BlockEntityTick" server/ level/` → 0 (no per-tick block-entity
drive); `grep -rl "FuelValues\|getBurnDuration\|BURN_TIME"` → 0 (no fuel table);
`level/block/blockentities.go` `FurnaceEntity struct{}` etc. are empty markers; no
`ClientboundContainerSetData` encoder existed before this plan.

## Decisions Made

- **Stonecutter result = plugin-gated + server-side-picked.** `Manager.Match` (the Plan-01 seam)
  returns only the FIRST 1×1 match — for a stone input that is `smooth_stone` (the smelting result),
  not the player's chosen cut. So the stonecutter uses `Match` to GATE matchability (the dogfood:
  a stonecuttable input must resolve through the plugin) and the server-side `selectByInput` list
  (`stonecutterRecipes()`, the same parsed tree `LoadCraftingPlugin` uses) for the SPECIFIC pick —
  which is exactly how vanilla `StonecutterMenu` works (it reads `RecipeAccess.stonecutterRecipes()`
  server-side too). `TestStonecutterResultIsPluginMatch` asserts both halves.
- **No furnace was faked.** The cooking defers cite the precise missing subsystem; the operator's
  "as much built 1:1 as feasible" mandate is honored by building the one block that needs no new
  subsystem (the stonecutter) and deferring the four that need an entire per-tick block-entity +
  fuel-table subsystem.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test used wrong item ids + a non-registered block state.**
- **Found during:** Task 2 (GREEN).
- **Issue:** The test's item-id constants were read from JSON-file line numbers, not the
  `registryid.Item` array indices (stone/stone_bricks/iron_ore/iron_ingot were off), and
  `block.ToStateID[block.Stonecutter{}]` keyed the zero-value `Facing=0` which is NOT a registered
  state (it resolved to air), so the open never fired.
- **Fix:** Corrected the constants to the real indices (stone=1, stone_bricks=403, iron_ore=93,
  iron_ingot=932) and placed `block.Stonecutter{Facing: 2}` (north, the canonical placed state).
- **Files modified:** server/cooking_test.go.
- **Verification:** all 5 tests green.
- **Committed in:** `6d2ca968` (GREEN commit).

---

**Total deviations:** 1 auto-fixed (1 bug — a test-only fixture correction). No scope creep; the
production stonecutter code was unaffected.

## Issues Encountered

- `Manager.Match` returns only the first 1×1 match (not a list) — this drove the plugin-gated +
  server-side-picked design above rather than threading a new list-returning seam through plugin/host
  (which would have been a larger, less-faithful change). Resolved by reading the `selectByInput` list
  from the already-parsed Go recipe tree, exactly as vanilla does.

## Verification

- `CGO_ENABLED=0 go build ./...` exit 0 (static).
- `CGO_ENABLED=0 go vet ./server/ ./cmd/... ./level/recipe/ ./plugin/...` clean.
- `CGO_ENABLED=0 go test ./server/ ./plugin/... ./level/recipe/` all green (incl. the 5 new
  `TestStonecutter*`/`TestSmeltingMatcherShips`).
- Docker `-race` (CGO=1, `golang:1.26`) over `./server/` (stonecutter + crafting menu tests) clean —
  though not required (no tickable/cross-tick state was built; the menu state is tick-owned transient,
  like the already-race-clean crafting menu). The one shared addition is a load-time `sync.Once`
  recipe cache (safe by construction).
- `deferred-blocks.md` exists with `## Per-Type Decisions` covering all 5 types; jar citations
  (`StonecutterMenu`, `AbstractFurnaceBlockEntity.serverTick`) present in both the doc and the built code.

## Next Phase Readiness

- **PLUGIN-05 complete**: the recipe matcher for ALL types ships + is tested (Plan 01); the
  crafting-grid menus ship (Plan 02); the stonecutter block ships (this plan); the four cooking
  blocks defer with cited reasons + tested matchers. Phase 25 is done.
- **For a future cooking phase**: the deferral cites exactly what to build — a per-tick block-entity
  drive seam (rideable on `level/ticks`), a `FuelValues`/`getBurnDuration` fuel table, the furnace BE
  fields + NBT load/save, and the `AbstractFurnaceBlockEntity.serverTick` 1:1 port. The
  `ContainerSetData` encoder built here is reusable for the furnace's 4 DataSlots, and the
  `openContainer.kind` + menu scaffolding extends to the furnace/campfire blocks.

## Self-Check: PASSED

All created files exist (stonecutter_menu.go, cooking_block.go, cooking_test.go,
deferred-blocks.md, 25-03-SUMMARY.md); all task commits present (14966823, 9a5fee10, 6d2ca968).

---
*Phase: 25-crafting-recipes-as-plugins*
*Completed: 2026-06-28*
