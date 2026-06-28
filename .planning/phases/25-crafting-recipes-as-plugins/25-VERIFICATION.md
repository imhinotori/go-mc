---
phase: 25-crafting-recipes-as-plugins
verified: 2026-06-28T00:00:00Z
status: passed
score: 7/7 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: none
---

# Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood) Verification Report

**Phase Goal:** Crafting is built THROUGH the plugin API (a recipe-provider plugin + the crafting_table 3×3 menu), proving the API generalizes to a second, very different domain (menus/recipes, not entity AI). Crafting was never built in the core — only the empty grid slots existed.
**Verified:** 2026-06-28
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

PLUGIN-05 is delivered. The crafting result + consume flow through the plugin `Manager.Match` seam (not a hardcoded Go table); the embedded vanilla recipe plugin re-expresses the 1:1 jar match in Starlark over a host-injected recipe table; a custom non-vanilla recipe (dirt→diamond) crafts through the same seam; the crafting_table 3×3 menu and the stonecutter block are built; the four cooking blocks are deferred with cited, evidence-based reasons while their matchers ship and stay tested.

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | All 7 recipe types embedded (//go:embed jar JSON, the level/loot twin) + parsed | ✓ VERIFIED | `level/recipe/embed.go:23-24` `//go:embed data/recipe` + `//go:embed data/item_tags`; 1585 recipe JSON files + 193 item_tag files present; `ParseAll` parses shaped/shapeless/smelting/blasting/smoking/campfire/stonecutting + special markers (census in 25-01-SUMMARY); `TestRecipeParse` green |
| 2 | The 1:1 match (shaped shrink+mirror, shapeless multiset, cooking/stonecutting single-ingredient) — jar-cited, bytecode-matched | ✓ VERIFIED | `match.go` carries 12+ FQCN citations; **javap spot-check confirmed**: `ShapedRecipePattern.matches` index `(mirror?width-col-1:col)+row*width` and the `if(!symmetrical&&matches(true))return true;return matches(false)` order are byte-for-byte the Go `MatchShaped`/`shapedScan`; `CraftingInput.ofPositioned` minCol=w-1/maxCol=0 shrink matches the Go `ofPositioned` loop exactly; `TestMatch*` green |
| 3 | The value-returning Match seam: set_recipe_matcher(fn) builtin + Go Match(grid)→result (extends Phase-22 void Emit to query-resolution) | ✓ VERIFIED | `plugin/host/recipe.go`: `makeSetMatcherBuiltin` (`set_recipe_matcher`), `Manager.Match` runs `starlark.Call` on a fresh `NewThread` and reads `{id,count}` back via `AsInt32` (value-returning, the dogfood extension); fresh-thread + recover + step-budget bounded; `TestMatchSeam`/`TestMatchNoMatcher`/`TestMatchRunawayBounded` green |
| 4 | ResultSlot.onTake un-stubbed + the 1:1 per-cell consume (removeItem per used cell, no dupe) | ✓ VERIFIED | "no recipes wired in v1" stub comment GONE; `slotOnTake`→`onTakeCraft` wired at all 3 result-take sites (inventory_doclick.go:207,230; inventory_click.go:635); **javap spot-check confirmed**: `ResultSlot.onTake` slot index `(col+left)+(top+row)*getWidth()` + `removeItem(slot,1)` per non-empty cell is 1:1 with `onTakeCraft`; `TestResultSlotConsume`/`TestConsumePositioned` assert exactly 1-per-cell |
| 5 | crafting_table block + 3×3 menu (chest-open clone); vanilla recipe plugin //go:embed'd + boot-loaded | ✓ VERIFIED | `crafting_menu.go`: `openCraftingTable` (OpenScreen `minecraft:crafting` + 46-slot ContainerSetContent, `CraftingMenu`-cited); `recipe_embed.go`: `//go:embed assets/crafting/{plugin.toml,main.star}` + `LoadCraftingPlugin` (ParseAll → SetRecipeTable → LoadDirWith, FATAL if no matcher); `cmd/sulfur/main.go:297` boot-loads it; `TestCraftingTableOpen/Crafts` green |
| 6 | THE GATE: a vanilla recipe crafts through the plugin path (result+consume, jar-verified) AND a custom recipe works | ✓ VERIFIED | `slotChangedCraftingGrid` calls `t.plugins.Match(...)` (the plugin Call, not a Go table); `TestVanillaRecipeCrafts` (2 planks → 4 sticks via Match, 1-per-cell consume) + `TestCustomRecipeWorks` (1 dirt → 1 diamond non-vanilla + vanilla fallthrough) + `TestEmbeddedBootLoadDefault` all PASS fresh (count=1) |
| 7 | cooking/stonecutting blocks: each BUILT 1:1 OR deferred WITH a cited reason | ✓ VERIFIED | `deferred-blocks.md`: stonecutter BUILT (`StonecutterMenu`-cited, `server/stonecutter_menu.go`+`cooking_block.go`, `TestStonecutter*` green); smelting/blasting/smoking/campfire DEFERRED on `context-cost` with jar class + missing subsystem (no per-tick block-entity drive, no fuel table — grep evidence: 0 matches) + the matchers still ship (`TestSmeltingMatcherShips`: iron_ore→iron_ingot through Manager.Match) |

**Score:** 7/7 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `level/recipe/embed.go` | //go:embed jar recipe + item_tags | ✓ VERIFIED | embeds 1585 recipe + 193 item_tag files |
| `level/recipe/parse.go` | ParseAll, name→id, #tag resolution | ✓ VERIFIED | ParseAll over the whole tree, loud errors on unknown type/name |
| `level/recipe/match.go` | 1:1 shaped/shapeless/cooking/stonecutting | ✓ VERIFIED | bytecode-faithful, javap spot-checked, 12+ FQCN citations |
| `plugin/host/recipe.go` | set_recipe_matcher + value-returning Match | ✓ VERIFIED | `Manager.Match` reads `{id,count}` back via `starlark.Call`+`AsInt32` |
| `server/crafting_click.go` | onTakeCraft 1:1 consume + slotChangedCraftingGrid | ✓ VERIFIED | routes through `t.plugins.Match`; 1-per-cell javap-faithful |
| `server/crafting_menu.go` | crafting_table 3×3 menu | ✓ VERIFIED | openCraftingTable, minecraft:crafting, 46 slots |
| `server/recipe_embed.go` | embedded plugin + boot-load | ✓ VERIFIED | //go:embed + LoadCraftingPlugin, FATAL on no-matcher |
| `server/assets/crafting/main.star` | plugin re-expresses the 1:1 match | ✓ VERIFIED | ofPositioned/shaped-mirror/shapeless-multiset/single over recipes(); set_recipe_matcher |
| `plugins/customrecipe/main.star` | custom recipe + vanilla fallthrough | ✓ VERIFIED | dirt→diamond custom-first, vanilla delegate |
| `server/stonecutter_menu.go` | stonecutter block 1:1 | ✓ VERIFIED | StonecutterMenu-cited, selectByInput picker |
| `deferred-blocks.md` | cited cooking-block deferral | ✓ VERIFIED | per-type jar class + missing subsystem + grep evidence |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| parse.go | data/registryid.Item | name→id | ✓ WIRED | nameToID map built from registryid.Item |
| recipe.go | starlark.Call | value-returning matcher on fresh thread | ✓ WIRED | `Manager.Match`→`starlark.Call(NewThread,...)` |
| slotChangedCraftingGrid | Manager.Match | plugin result query | ✓ WIRED | `t.plugins.Match(craftGridPayload(v))` (line 86) — result is plugin-sourced |
| inventory_doclick/click | onTakeCraft | slotOnTake dispatch | ✓ WIRED | all 3 result-take sites route through slotOnTake |
| cmd/sulfur/main.go | LoadCraftingPlugin | boot-load embedded plugin | ✓ WIRED | main.go:297, FATAL on failure |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build (CGO=0, static) | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Vet | `go vet ./level/recipe/ ./plugin/... ./server/ ./cmd/...` | exit 0 | ✓ PASS |
| recipe + host tests | `go test ./level/recipe/ ./plugin/host/ -count=1` | ok | ✓ PASS |
| server suite (fresh) | `go test ./server/ -count=1` | ok 4.5s | ✓ PASS |
| Gate tests (fresh, verbose) | `go test ./server/ -run 'TestVanilla\|TestCustom\|TestResultSlot\|TestCraftingTable\|TestStonecutter\|TestSmelting...' -count=1` | 12/12 PASS | ✓ PASS |
| Docker -race (CGO=1) | `docker run golang:1.26 go test -race ./plugin/host/ ./server/` | ok, race-clean | ✓ PASS |
| Shaped match vs jar | javap ShapedRecipePattern.matches | index + try-order 1:1 | ✓ PASS |
| ofPositioned shrink vs jar | javap CraftingInput.ofPositioned | minCol/maxCol loop 1:1 | ✓ PASS |
| Consume vs jar | javap ResultSlot.onTake | slot index + removeItem(slot,1) 1:1 | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PLUGIN-05 | 25-01/02/03 | Crafting built THROUGH the plugin API; jar-extracted recipes; result + ResultSlot.onTake consume; crafting_table 3×3 menu; vanilla + custom craft via plugin path | ✓ SATISFIED | All 4 ROADMAP success criteria + all 7 phase truths verified; gate tests green; jar-faithful (javap-confirmed) |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | "no recipes wired in v1" stub | — | REMOVED — the stub the phase set out to un-stub is gone (verified absent) |

No blocking anti-patterns. The `Remaining`/`getRemainingItems` all-empty default is the faithful `CraftingRecipe.defaultCraftingReminder` path (cited, structured-but-default — correct for the gate recipes), not a stub.

### Deferred / Justified Non-Gaps

The four cooking blocks (smelting/blasting/smoking/campfire) defer their BLOCK UI per success criterion 7 ("BUILT 1:1 OR deferred WITH a cited reason"). The deferral is evidence-based (`deferred-blocks.md`): the furnace needs a per-tick block-entity drive (`grep serverTick` → 0) + a fuel table (`grep FuelValues` → 0), neither of which exists; the BE types are empty struct markers. The matcher for every deferred type ships and stays tested (`TestSmeltingMatcherShips`). This is a justified deferral, NOT a gap. Note (minor, non-load-bearing): 25-01-SUMMARY says 224 item_tag files; the actual embedded count is 193 — a documentation discrepancy only; parse + tag resolution tests pass.

### Human Verification Required

None. Phase 25 is autonomous and fully testable headless (per 25-VALIDATION.md). The real-client visual gate is Phase 28 (PLUGIN-07), out of scope here.

### Gaps Summary

No gaps. All 7 observable truths VERIFIED, all artifacts substantive + wired + data-flowing (the result flows from the plugin Match seam, not a hardcoded table), all key links wired, the build/vet/test/-race gates green, the three load-bearing 1:1 ports (shaped match, ofPositioned shrink, onTake consume) spot-checked against the 26.2 jar bytecode and confirmed byte-for-byte faithful, and no Claude attribution in any Phase-25 commit. PLUGIN-05 is delivered.

---

_Verified: 2026-06-28_
_Verifier: Claude (gsd-verifier)_
