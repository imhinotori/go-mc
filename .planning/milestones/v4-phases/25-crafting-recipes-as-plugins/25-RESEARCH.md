# Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood) — Research

**Researched:** 2026-06-28
**Domain:** Building crafting (recipe match + result + ResultSlot.onTake consume) and the crafting_table 3×3 menu THROUGH the Phase-22 plugin API — a recipe-PROVIDER plugin drives the result, the Go menu calls back into it. A 1:1 jar port of `RecipeManager`/`ShapedRecipe`/`ShapelessRecipe`/`CraftingMenu`/`ResultSlot`, re-expressed across the Go↔Starlark seam.
**Confidence:** HIGH on the jar logic (every match/consume/menu method `javap`'d this session against `temp/cache/26.2-inner.jar` and the recipe JSON shape confirmed from `temp/cache/26.2-datagen`), HIGH on the Sulfur inventory/menu types (every named type read from `server/inventory*.go` + `server/chest_*.go` this session), MEDIUM on the exact plugin-bridge API shape (the Go→plugin-returns-a-value seam is NEW — Phase 22 was fire-and-forget Emit; the recommended design is grounded but the precise builtin signature is a planner/discuss decision).

## Summary

Phase 25 is the SECOND dogfood: it proves the plugin API generalizes from entity AI (Phase 24) to a completely different domain — recipes/menus. It is built on three things that ALREADY EXIST in the tree: (1) the Phase-22 host (`plugin/host`: `Manager`, `LoadDirWith`, the `register` builtin, `starlark.Call` dispatch — verified wired, `block_interact.go` already imports `plugin/host`); (2) the full survival container-click engine in `server/inventory_click.go` + `inventory_doclick.go` (the `menuSlot` Slot-primitive port, `doClick`, `safeInsert`/`safeTake`/`tryRemove`), where the craft-result path is explicitly stubbed — `ResultSlot.onTake` is a no-op (`inventory_click.go:258-260` "ResultSlot.onTake (crafting consumption) is a CITED no-op for v1 — slot 0 holds nothing with no recipes wired"); and (3) the Phase-20 chest-OPEN subsystem (`server/chest_open.go` + `chest_click.go`) which is the EXACT structural template for the crafting_table: right-click block → resolve → allocate windowId → `openScreen` + `ContainerSetContent` → a window-specific click engine → close. The crafting_table menu is "the chest path with a different menu type, a 3×3 grid + result slot, and a recipe matcher wired to the result."

The jar logic decomposes cleanly (all `javap`'d this session). **Match:** `CraftingMenu.slotChangedCraftingGrid` → `RecipeManager.getRecipeFor(CRAFTING, input, level)` → on hit, `recipe.assemble(input)` → `ResultContainer.setItem(0, result)` + send a SetSlot for the result slot. The input is a `CraftingInput` built via `CraftingInput.of(w, h, items)`, which **shrinks the grid to the minimal bounding box of non-empty cells** (`ofPositioned` computes min/max row/col) — this is why a 2-tall recipe matches anywhere in a 3×3 grid. **Shaped match** (`ShapedRecipePattern.matches`) checks `ingredientCount` + `width`/`height` equality against the shrunk input, then tries the pattern MIRRORED (horizontal flip: `ingredients[width - col - 1 + row*width]`) and, if not symmetrical, UN-mirrored — each scanning every cell with `Ingredient.testOptionalIngredient`. **Shapeless match** (`ShapelessRecipe.matches`) is a multiset: `ingredientCount == ingredients.size()`, then `StackedItemContents.canCraft` (a greedy multiset cover). **Consume** (`ResultSlot.onTake`): recompute the positioned input, get `getRemainingItems` (buckets→empty bucket, default empty), then for each grid cell in the positioned region, `removeItem(slot, 1)` (shrink each used ingredient by exactly 1) and place any remaining item (e.g. empty bucket) back.

**The dogfood seam (the load-bearing NEW mechanism):** Phase 22's `Emit` is fire-and-forget (Go→plugin, no return read). Phase 25 needs Go→plugin-**returns-a-value**: the menu hands the plugin a 9-slot grid (item ids + counts), the plugin runs the 1:1 match, and returns a result (`{id, count}` or None) AND the per-slot consume deltas. This is `starlark.Call(thread, matchFn, gridTuple, nil)` reading back a `starlark.Value` (a dict/tuple), converted to Go. The recipe-provider plugin REGISTERS its recipes + the match function once at load (via a new `register_recipes(...)` / `set_recipe_matcher(fn)` host builtin, analogous to Phase-22's `register(event, fn)`), then the Go `slotChangedCraftingGrid`/`onTake` ports call into it. The 1:1 match/consume STAYS a literal jar port — it just executes in Starlark instead of Go. A custom recipe falls out free: a plugin adds a non-vanilla entry the same way.

**Primary recommendation:** Three deliverables. (1) **Recipe data**: extract the jar's `data/minecraft/recipe/*.json` (1585 files, of which 1129 are crafting_shaped/shapeless/smelting) into `level/recipe/data/` and `//go:embed` it — EXACTLY the Phase-20 `level/loot/embed.go` pattern (`//go:embed data/loot_table`). Scope the FIRST port to `crafting_shaped` + `crafting_shapeless` (the 3×3/2×2 grid recipes the gate needs); flag smelting/stonecutting/smithing as deferred (they need furnace/other menus). (2) **The recipe plugin + bridge**: a Starlark plugin that loads the embedded recipes (or Go pre-parses them and hands the plugin a registration call) and provides a `match(grid)->result` + `consume`/`remaining(grid)` function the menu calls via `starlark.Call`. The 1:1 shaped/shapeless match (bounding-box shrink, mirroring, multiset) is ported into the plugin. (3) **The crafting_table block + 3×3 CraftingMenu**: clone `chest_open.go`/`chest_click.go` for `minecraft:crafting_table` → `openScreen(win, menuTypeID("minecraft:crafting"), "Crafting")` → a 9-grid+result window whose result slot is driven by the plugin matcher, with `onTake` running the consume through the plugin. ALSO wire the EXISTING player-inventory 2×2 grid (slots 1-4, result slot 0) to the same matcher so `slotChangedCraftingGrid` fills slot 0 and `ResultSlot.onTake` (today the no-op stub) consumes.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Recipe DATA (jar JSON → embedded) | `level/recipe` (new, pure, `//go:embed`) | — | Mirrors `level/loot` — pure, no tick state, no network. Parsing is load-time. |
| Recipe MATCH (shaped/shapeless 1:1) | **the recipe plugin (Starlark)** | `level/recipe` (Go parse helper) | The dogfood point: match logic runs AS A PLUGIN, not hardcoded Go. 1:1 jar port re-expressed in Starlark. |
| Result computation (`assemble`) | the recipe plugin | — | `assemble` = the recipe's result template; the plugin returns it. |
| Consume / `getRemainingItems` | the recipe plugin (logic) + Go menu (applies deltas to tick-owned slots) | — | The plugin computes which cells shrink; Go mutates the tick-owned `Inventory`/grid (TICK-05). |
| The Go↔plugin bridge (grid→result call) | `plugin/host` (new `set_recipe_matcher` builtin + a `Match`/`Resolve` Go API) | `server/` (calls it on the tick) | New seam: Go→plugin-returns-a-value, beyond Phase-22 Emit. Host owns it; server invokes. |
| crafting_table block interaction (right-click→open) | `server/` (`useBlockInteraction` extension) | — | Same tier as the chest open; only the server knows a block was clicked. |
| 3×3 CraftingMenu window (open/click/close/sync) | `server/` (new `crafting_menu.go`, chest-style) | — | Tick-owned menu state + the click engine over the grid+result layout. |
| Player 2×2 grid result wiring (slots 1-4 → slot 0) | `server/` (wire `slotChangedCraftingGrid` + real `ResultSlot.onTake`) | the recipe plugin | The existing stub becomes a real call into the matcher. |
| Result/consume on the tick, race-clean | `server/` tick goroutine (TICK-05) | `plugin/host` (fresh Thread per Call) | The Call seam runs on the tick over tick-owned slots; frozen recipe data crosses safely. |

**Key boundary (carried from Phase 22):** `plugin/host` and `level/recipe` import NO `server/` types. The grid handed to the plugin is PLAIN frozen scalars (item-id ints + counts) — the Phase-23 frozen-handle pattern, but for a crafting grid the payload is so simple (9× (id,count)) it needs no live handle at all (see Open Questions §item/grid handle). The server depends on `plugin/host` + `level/recipe` (one direction).

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-05 | Crafting built THROUGH the plugin API (not hardcoded Go): a recipe-provider plugin loads jar-extracted recipes (shaped/shapeless/smelting/…) and drives the crafting-grid result + `ResultSlot.onTake` consumption (today a no-op stub — `inventory_click.go`: "no recipes wired in v1"). Adds the `crafting_table` block + 3×3 menu (core only had the 2×2 inventory grid). Vanilla recipes craft correctly through the plugin path (result + consume, jar-verified vs `RecipeManager`/`CraftingMenu`) AND a custom recipe works. | Recipe-data extraction+embed (Standard Stack §Recipe data; Architecture §Recipe data). The recipe-provider plugin + the Go→plugin-returns-a-value bridge (Architecture §Pattern 1-2; Code Examples §match builtin, §recipe plugin). The 1:1 shaped/shapeless match (bounding-box shrink + mirroring + multiset) and `ResultSlot.onTake` consume, all `javap`'d (Architecture §Pattern 3-4; Code Examples §onTake port; Common Pitfalls §1-3). The existing stub to un-stub (`inventory_click.go:258-260`, the player 2×2 grid slots 0-4). The crafting_table block + 3×3 menu cloned from the chest open path (Architecture §Pattern 5; named Sulfur types). The gate = vanilla recipe (planks→sticks shaped, or 4×planks shapeless) crafts + consumes + a custom recipe (Validation Architecture). |
</phase_requirements>

<user_constraints>
## User Constraints

> No `CONTEXT.md` exists for Phase 25 yet (this research feeds `/gsd-discuss-phase` / the planner). The constraints below are extracted VERBATIM from `v4-PLAN.md` (Phase 25 row + build-order rationale) and `REQUIREMENTS.md` (PLUGIN-05). Open Questions flag what the operator decides at kickoff — do NOT decide those in the plan.

### Locked decisions (from v4-PLAN.md + REQUIREMENTS.md PLUGIN-05)
- **Crafting is built THROUGH the plugin API, NOT a hardcoded Go subsystem.** The recipe-MATCHING + result/consume logic runs AS A PLUGIN (a recipe-provider via the Phase-22 host). This is the dogfood — the whole point is API generality, not "a crafting feature."
- **1:1 mandate carries.** The recipe match/consume logic (RecipeManager/CraftingMenu/ShapedRecipe/ShapelessRecipe/ResultSlot jar port) stays a literal, method-for-method copy of `temp/cache/26.2-inner.jar` (`javap -c -p` / CFR), cited, re-expressed idiomatically in Starlark (no GPL paste). Custom (non-vanilla) recipes are free of the mandate by definition.
- **Custom recipes fall out free.** A plugin adds a non-vanilla recipe the same way it adds a vanilla one. The gate requires BOTH a vanilla recipe AND a custom recipe working.
- **Adds the crafting_table block + 3×3 menu.** The core only had the 2×2 inventory grid (the InventoryMenu slots 1-4 + result slot 0, today a no-op result). Net-new: the crafting_table right-click → open a generic 3×3 crafting menu.
- **CGO_ENABLED=0 default binary** — the Starlark recipe plugin respects it (Phase 21 verified). Recipe data is `//go:embed`'d JSON (pure-Go, like `level/loot`). No new cgo dep.
- **TICK-05 single-owner / Docker -race clean carries into the plugin call seam.** The match/consume Call runs on the tick goroutine over tick-owned slots; frozen recipe data crosses safely.
- **No Co-Authored-By / no Claude attribution** in commits (CLAUDE.md).
- **Push to `development`** (v4-PLAN constraint #5).

### Claude's Discretion
- The recipe-data package layout (recommended `level/recipe` mirroring `level/loot`).
- The exact host builtin name/signature for recipe registration + the grid→result match bridge (recommended `set_recipe_matcher(fn)` / `register_recipe(...)` — see Code Examples).
- Whether Go pre-parses the embedded recipe JSON and hands the plugin a structured registration call, vs. the plugin parsing JSON itself (recommended: Go parses → passes structured data to the plugin, since Starlark has no JSON/file builtins by sandbox design — see Open Questions §recipe parsing locus).
- The crafting_menu.go file/type layout (recommended: clone chest_open.go/chest_click.go).

### Deferred Ideas (OUT OF SCOPE for Phase 25)
- **Smelting/blasting/smoking/campfire** recipes (`minecraft:smelting` etc.) — they need a FURNACE block + menu + a burn-time/cook-time tick subsystem, which is a separate feature. PLUGIN-05 says "shaped/shapeless/smelting/…" but the GATE is grid crafting; port shaped+shapeless now, SHAPE the recipe model for smelting, flag furnace as a follow-up. (Open Q §recipe-type scope.)
- **Stonecutting / smithing / brewing** menus — net-new blocks/menus, out of the crafting dogfood.
- **The recipe book** (ClientboundRecipe / unlock advancements / `minecraft:recipes` component) — the `Recipes` component in `level/component/recipes_gen.go` is the recipe-BOOK unlock list, NOT recipe definitions; the recipe book UI is out of scope (a plugin-driven matcher does not need it).
- **Mob AI** (Phase 24) — Phase 25 does NOT port mob AI and does NOT depend on Phase 24's mob plugins.
- **Python runtime** (Phase 26), **Folia regions** (Phase 27).
- **Bundle / feature-flag item overrides** in the result slot — `tryItemClickBehaviourOverride` stays the cited-false stub (`inventory_doclick.go:187`).
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **GAMEPLAY IS A 1:1 PORT OF VANILLA JAVA — ABSOLUTE.** The recipe match (shaped mirroring, shapeless multiset, bounding-box shrink), `assemble`, and `ResultSlot.onTake` consume MUST mirror the `javap`'d bytecode EXACTLY — including the shrink-to-bounding-box, the mirrored-then-unmirrored try order, `removeItem(slot, 1)` per used cell, and `getRemainingItems` (bucket-back). Re-express in Starlark (no GPL paste), CITE every class/method. Verify against bytecode before writing. Only deviation permitted = optimization preserving identical observable gameplay.
- **CGO_ENABLED=0 default binary** — recipe data is `//go:embed`'d (pure Go, like `level/loot`); the Starlark plugin is CGO=0. Gate: `CGO_ENABLED=0 go build ./...` clean.
- **`go test -race` non-negotiable** — the match/consume Call seam is tick-owned; Docker `-race` gate (CGO=1) covers it. Two gates: ship build CGO=0, race test CGO=1 (Phase-21 Pitfall 3).
- **TICK-05 single-owner** — every grid/result/inventory mutation runs only on the tick goroutine; the plugin Call uses a fresh `starlark.Thread` per call; frozen recipe data is read-only-crossed.
- **Jar source of truth**: `temp/cache/26.2-inner.jar` (`javap -c -p`); recipe JSON in `temp/cache/26.2-datagen/generated/data/minecraft/recipe/`. Both confirmed present this session.
- **No built-but-unwired code rule:** port shaped+shapeless (gate-needed) now; SHAPE the model for smelting but do NOT build a furnace menu speculatively. Wire BOTH the new 3×3 menu AND the existing 2×2 player grid (the stub becomes live).
- **Push to `development`.**

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `go.starlark.net` | `v0.0.0-20260613233743-8ba36ccb83fb` `[VERIFIED: go.mod this session]` | The recipe plugin runtime + the `starlark.Call(grid)->result` bridge. Already in via Phase 21/22; no new dep. | Same dep; Phase 25 adds zero runtime deps. |
| `plugin/host` (Phase 22, in-tree) | — | The `Manager`, `LoadDirWith`, the `register`-style builtin pattern, `starlark.Call` dispatch. Phase 25 ADDS a recipe-registration builtin + a grid→result Call seam to it. | Reuse the proven host; extend it with the value-returning seam. |
| `plugin/starlark` (Phase 21, in-tree) | — | `LoadWith(path, predeclared)`, `NewThread`, `safeGlobals`, frozen-cross-goroutine. | The runtime foundation; unchanged. |
| (stdlib) `embed`, `encoding/json`, `io/fs`, `path` | — | `//go:embed` the jar recipe JSON tree; parse shaped/shapeless/smelting into a Go model (then hand to the plugin, or expose to it). | EXACTLY the `level/loot/embed.go` approach — zero-dep, CGO=0. |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `data/item` (in-tree, `item.ByID`) | — | Item id↔stack-size lookups (`maxStackSize` already uses `item.ByID`). | Result/ingredient stack sizing. |
| `data/registryid` (in-tree, `Item`, `Menu`, `RecipeType`) | — | `registryid.Item` ([]string indexed by protocol id) gives **name→id** (index lookup); `registryid.Menu` has `"minecraft:crafting"` (the 3×3 menu type for `openScreen`); `registryid.RecipeType` lists the 7 recipe types. | Name→id resolution for recipe ingredients/results; menu-type id for OpenScreen. |
| `level/component` (`SlotData`) | — | The component-slot ItemStack (`ItemID`, `Count`, `RawComponents`) the grid/result/consume operate on. | The grid cells + result stack. |
| (stdlib) `testing` | — | Match/consume/menu tests + the gate (vanilla + custom recipe). | The whole Phase-25 gate. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Go parses recipe JSON → hands structured data to the plugin | The plugin parses JSON itself | **Reject the plugin parsing JSON.** Starlark has NO `json`/`open` builtin (sandbox, Phase-21 verified). Either Go parses and passes structured recipes to a `register_recipe(...)` builtin, OR you expose a `load_recipes()` Go builtin the plugin calls. Go-parses-then-registers is cleaner + reuses the Phase-22 pattern. |
| `//go:embed` the recipe JSON tree (like `level/loot`) | Hand-transcribe recipes into Go | **Reject hand-transcription** — 1585 recipe files (1129 crafting). CLAUDE.md: "Hand-transcription is a non-starter." Embed the whole `recipe/` tree once (small JSON), parse the crafting subset. |
| A new value-returning Call seam (`Match`) | Reuse Phase-22 `Emit` (fire-and-forget) | **Emit can't return a value** — it's void dispatch. Crafting needs Go→plugin→**result back**. This is the new seam Phase 25 contributes; it's a thin `starlark.Call` + a Go-side value reader (Code Examples §match builtin). |
| Per-`(id,count)` scalar grid payload | A Phase-23 live-entity frozen handle | The crafting grid is 9 cells of (item-id, count) — plain immutable scalars, no live handle needed. Phase 25 does NOT need Phase-23's frozen *entity* handle; it needs a frozen *grid value* (a list of (id,count) tuples), which is the simpler Phase-22-style payload. (See Open Q §item/grid handle.) |
| Clone `chest_open.go`/`chest_click.go` for the 3×3 menu | Write the crafting menu from scratch | **Reuse the chest template** — it already solves windowId allocation (`nextContainerCounter`), `openScreen`, `ContainerSetContent`, `openContainer` tracking, the resend-after-click model, and a window-specific click engine. The crafting menu is the chest path with a 3×3 grid + a recipe-driven result slot. |

**Installation:** None — no new deps. Recipe JSON is copied from the datagen cache into the new `level/recipe/data/` and embedded.

**Version verification (run at execution):**
```bash
cd /d/ender && grep go.starlark.net go.mod   # confirm the pin is still v0.0.0-20260613233743-8ba36ccb83fb
```

## Architecture Patterns

### System Architecture Diagram

```
  LOAD TIME (host goroutine, before the tick loop)
  ──────────────────────────────────────────────────────────────────────────
   level/recipe/data/  ──┐  //go:embed (jar recipe JSON, like level/loot)
   (shaped/shapeless     │
    JSON, 1129 files)    ▼
   ┌──────────────────────────────────────────────┐
   │ level/recipe : ParseAll() → []Recipe (Go)     │  shaped{pattern,key,result}
   │   name→id via registryid.Item index            │  shapeless{ingredients,result}
   └───────────────────┬──────────────────────────┘
                       │  (Go hands structured recipes to the plugin OR
                       │   exposes a load_recipes() builtin)
                       ▼
   plugins/crafting/                ┌───────────────────────────────────────┐
   ├── plugin.toml                  │ plugin/host : LoadDirWith(predeclared) │
   └── main.star  ──entrypoint────► │  predeclared = safeGlobals + log +     │
        register_recipes(recipes)   │    register_recipes(...) +              │
        set_recipe_matcher(match)   │    set_recipe_matcher(fn)  (NEW)        │
                                    │  module body runs ONCE → captures the   │
                                    │  recipe table + the match callable      │
                                    └──────────────┬────────────────────────┘
                                                   │ holds: matchFn (frozen),
                                                   ▼        recipe table (frozen)
  ═══════════════════════════════ frozen / tick loop starts ═════════════════
  TICK TIME (tick goroutine — single owner, TICK-05)
  ──────────────────────────────────────────────────────────────────────────
   (A) GRID CHANGES (player put an item in the 2×2 or 3×3 grid)
        server: slotChangedCraftingGrid(grid)               ← jar port
              │  build CraftingInput.of(w,h,items)  (BOUNDING-BOX SHRINK)
              ▼
        host.Match(gridScalars)  →  starlark.Call(freshTh, matchFn, grid, nil)
              │                          plugin runs 1:1 shaped/shapeless match
              ▼  reads back result value (dict {id,count} or None)
        server: ResultContainer.setItem(0, result)
              + send ClientboundContainerSetSlot(win, stateId, 0, result)

   (B) PLAYER TAKES THE RESULT (clicks result slot 0)
        server: ResultSlot.onTake(player, result)            ← jar port
              │  host.Remaining(gridScalars) → Call(remainFn) (buckets→empty)
              ▼  for each grid cell in the positioned region:
        removeItem(cell, 1)   (shrink each used ingredient by EXACTLY 1)
              + place any remaining item (empty bucket) back
              + re-run (A) so the result recomputes (grid may still match)

   ✗ The match/consume is the SAME jar logic — it just executes IN THE PLUGIN.
   ✗ NEVER call the matcher per-tick-per-grid-scan: call it on a grid CHANGE
     and on a result TAKE (discrete occurrences), like Phase-22's discrete seams.
```

### Recommended Project Structure
```
level/
└── recipe/                       # NEW — pure, mirrors level/loot
    ├── embed.go                  # //go:embed data/recipe ; TableJSON/LoadAll
    ├── model.go                  # Recipe, Shaped{pattern,key,result}, Shapeless{...}, Ingredient (item OR #tag)
    ├── parse.go                  # JSON → model; name→id via registryid.Item; tag resolution
    └── data/recipe/              # the embedded jar recipe JSON (shaped+shapeless subset, or whole tree)

plugin/host/
    ├── recipe.go                 # NEW — set_recipe_matcher builtin + Match/Remaining Go API (the value-returning Call seam)
    └── recipe_test.go

plugins/crafting/                 # NEW — the recipe-provider plugin (the dogfood)
    ├── plugin.toml               # name=crafting, runtime=starlark, entrypoint=main.star
    └── main.star                 # 1:1 shaped/shapeless match + consume, in Starlark

server/
    ├── crafting_menu.go          # NEW — crafting_table open + 3×3 CraftingMenu (clone chest_open.go)
    ├── crafting_click.go         # NEW — the grid+result click engine (clone chest_click.go) + slotChangedCraftingGrid + onTake
    ├── inventory_click.go        # EDIT — un-stub ResultSlot.onTake (line 258-260); wire mayPlace result false stays
    └── crafting_menu_test.go
```

### Pattern 1: Recipe data — embed the jar JSON (the `level/loot` twin)
**What:** Copy `temp/cache/26.2-datagen/generated/data/minecraft/recipe/*.json` into `level/recipe/data/recipe/` and `//go:embed` it, EXACTLY like `level/loot/embed.go` does `//go:embed data/loot_table`.
**Why:** 1585 recipe files; hand-transcription is forbidden (CLAUDE.md). The JSON is small and the parse is pure.
**The recipe JSON shape (confirmed this session):**
```jsonc
// stick.json — SHAPED (note: ingredient is a TAG #minecraft:planks)
{ "type": "minecraft:crafting_shaped", "group": "sticks",
  "key": { "#": "#minecraft:planks" },
  "pattern": ["#", "#"],
  "result": { "count": 4, "id": "minecraft:stick" } }

// oak_planks.json — SHAPELESS (also a tag ingredient)
{ "type": "minecraft:crafting_shapeless",
  "ingredients": ["#minecraft:oak_logs"],
  "result": { "count": 4, "id": "minecraft:oak_planks" } }
```
**Tag pitfall:** ingredients are often `#minecraft:planks` (an item TAG), not a bare item id. The matcher must resolve a tag to its member-id SET. Tags are in `server/registrydata/tags.go` (`//go:embed tags`). A recipe with no tags (a direct-id custom recipe) is the simplest gate case; a vanilla shaped recipe (stick) needs tag resolution. (See Common Pitfalls §4.)

### Pattern 2: The recipe-provider plugin + the value-returning bridge (THE dogfood)
**What:** A new host builtin `set_recipe_matcher(fn)` captures a Starlark callable (like Phase-22's `register`), and a Go `Manager.Match(grid) (result, ok)` invokes it via `starlark.Call`, reading back the returned value. `register_recipes(recipes)` (or a Go-exposed `recipes()` builtin) gives the plugin the recipe table.
**Why:** This is the API-generality proof. Phase 22 dispatched events (void); Phase 25 RESOLVES a query (grid→result). The host gains ONE new capability — a Call whose return value is read — and the whole crafting domain rides on it.
```go
// plugin/host/recipe.go  (NEW) — the value-returning seam beyond Emit.
func (m *Manager) makeSetMatcherBuiltin(plugin string) *starlark.Builtin {
    return starlark.NewBuiltin("set_recipe_matcher", func(th *starlark.Thread, b *starlark.Builtin,
        args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
        var fn starlark.Callable
        if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &fn); err != nil {
            return nil, err
        }
        m.recipeMatcher = fn // frozen after Load; safe to Call from the tick
        return starlark.None, nil
    })
}

// Match runs the plugin matcher over a 3×3 (or 2×2) grid of (item-id, count) scalars and
// returns the result stack (ok=false = no recipe). Called on the tick at a grid change.
// grid is a flat 9-element list of (id, count) tuples; empty cell = (0, 0).
func (m *Manager) Match(grid starlark.Value) (resultID, resultCount int, ok bool) {
    if m.recipeMatcher == nil { return 0, 0, false }
    th := starlarkpkg.NewThread("recipe:match")
    out, err := starlark.Call(th, m.recipeMatcher, starlark.Tuple{grid}, nil)
    if err != nil { log.Printf("recipe matcher error: %v", err); return 0, 0, false }
    if out == starlark.None { return 0, 0, false }
    // out is a dict/tuple {id, count} — convert back to Go (see Code Examples §value read).
    return readResult(out)
}
```

### Pattern 3: Shaped match — bounding-box shrink + mirror-then-unmirror (1:1, `javap`'d)
**What:** The jar shaped match (`ShapedRecipePattern.matches(CraftingInput)`):
1. The input grid is FIRST shrunk by `CraftingInput.ofPositioned(w, h, items)` to the minimal bounding box of non-empty cells (min/max row+col), yielding `(input, left, top)`. **A 2×1 recipe matches a 2×1 footprint anywhere in the 3×3.**
2. `ingredientCount` must equal the pattern's; `input.width`/`height` must equal the pattern's `width`/`height` (else fail — no further trying).
3. Try the pattern MIRRORED (horizontal flip): `ingredients[width - col - 1 + row*width]` vs `input.getItem(col,row)`. If `symmetrical`, skip the mirror try.
4. If the mirror try fails, try UN-mirrored: `ingredients[col + row*width]`.
5. Each cell: `Ingredient.testOptionalIngredient(optIngredient, stack)` — an empty optional matches an empty cell.
**Verified bytecode (`ShapedRecipePattern.matches` + private `matches(input, mirrored)` this session):** the public method checks count+w+h, then `if (!symmetrical && matches(input, true)) return true;` then `return matches(input, false);`. The private body double-loops `row<height, col<width`, computes the ingredient index `(mirrored ? width-col-1 : col) + row*width`, and returns false on the first non-matching cell.

### Pattern 4: Shapeless match (multiset) + the consume (`ResultSlot.onTake`)
**Shapeless match** (`ShapelessRecipe.matches`, `javap`'d): `if (input.ingredientCount != ingredients.size()) return false;` then a fast 1-ingredient path (`input.size()==1 && ingredients.size()==1` → `ingredients[0].test(input.getItem(0))`), else `input.stackedContents().canCraft(this, null)` — a greedy multiset cover (each ingredient consumes one distinct grid item).
**Consume** (`ResultSlot.onTake(player, result)`, `javap`'d — the method that REPLACES the no-op stub):
1. `checkTakeAchievements` (a no-op for v1 — no stats).
2. `input = craftSlots.asPositionedCraftInput()` → `(input, left, top)`.
3. `remaining = recipe.getRemainingItems(input, level)` — default `CraftingRecipe.defaultCraftingReminder` returns the bucket/bottle leftover (empty bucket from a water bucket); for plain recipes it's all-empty.
4. For `row in [0,height), col in [0,width)`: `slot = (col+left) + (row+top) * gridWidth`; `current = craftSlots.getItem(slot)`; `rem = remaining[col + row*width]`; if `current` non-empty: `craftSlots.removeItem(slot, 1)` (**shrink each used ingredient by EXACTLY 1**), re-read `current`; then if `rem` non-empty: if `current` now empty → `setItem(slot, rem)` else try to merge `rem` / drop it.
**The consume runs through the plugin too:** the plugin returns BOTH the result AND the per-cell remaining list (or a "consume(grid)→new grid" function); Go applies the shrink to the tick-owned grid slots. The grid-shrink + remaining-placement loop is the 1:1 jar port; whether the loop lives in Go (driving plugin-returned data) or in the plugin is an Open Question (§consume locus) — recommended: plugin returns the result + remaining list, Go applies (Go owns tick-state mutation, TICK-05).

### Pattern 5: The crafting_table block + 3×3 menu (clone the chest path — named Sulfur types)
**What:** The crafting_table is the chest-OPEN subsystem with a different menu type + slot layout. The chest path (verified this session) is the EXACT template:

| Chest (existing, `server/chest_open.go`/`chest_click.go`) | crafting_table (new, clone) |
|-----------------------------------------------------------|------------------------------|
| `isChestBlock(state)` (`StateList[s].ID()=="minecraft:chest"`) | `isCraftingTableBlock(state)` (`=="minecraft:crafting_table"`) |
| `useBlockInteraction` → `openChest` (it's the `block_interact.go` hook; `chest_open.go` overrides it) | `useBlockInteraction` must ALSO handle crafting_table (one hook, two block kinds) |
| `openScreen(win, menuTypeID(registryid.Menu,"minecraft:generic_9x3"), "Chest")` | `openScreen(win, menuTypeID(registryid.Menu,"minecraft:crafting"), "Crafting")` |
| `openContainer{windowID, chestPos}` (tick-owned, `inventory.go:225-231` routes clicks to it) | `openContainer` gains a `kind`/grid: a crafting menu has a 3×3 grid + result, NO backing block-entity (the grid is transient, like vanilla — closing drops the grid items back to the player) |
| `chestMenuSize = 27+27+9 = 63`; layout chest 0..26, main 27..53, hotbar 54..62 | `craftingMenuSize = 1(result)+9(grid)+27(main)+9(hotbar) = 46`; layout: slot 0=result, 1..9=grid, 10..36=main, 37..45=hotbar (jar `CraftingMenu`: RESULT_SLOT=0, CRAFT_SLOT 1..9, then inv) |
| `chestResolveSlot(menuIdx)` → chest cell or player cell | `craftingResolveSlot(menuIdx)` → result(0) / grid(1-9) / player cell |
| `clickedChest` (PICKUP/QUICK_MOVE/THROW over the two-container view) | `clickedCrafting` (same inputs; result slot is take-only `mayPlace=false`, take triggers onTake/consume) |
| close = free windowId (chest items persist in BE) | close = free windowId + **drop the grid items back to the player inventory** (vanilla `CraftingMenu.removed` → `clearContainer`) |

**Slot indices (jar `CraftingMenu`, `javap`'d):** `RESULT_SLOT=0`, `CRAFT_SLOT_START=1`, `CRAFT_SLOT_COUNT=9` (so grid is 1..9), then `INV_SLOT`/`USE_ROW` for the 27 main + 9 hotbar. The result slot's `mayPlace=false` (take-only) is ALREADY the rule in `inventory_click.go:143` (`m.index==0 → return false`) — the 3×3 menu mirrors it.

**The player 2×2 grid (the EXISTING stub to un-stub):** `inventory_click.go:108` documents the InventoryMenu layout — `0=result, 1-4 craft grid, 5-8 armor, 9-35 main, 36-44 hotbar, 45 offhand`. The 2×2 grid (slots 1-4) + result (slot 0) ALREADY EXIST; `mayPlace(0)` is already false; `ResultSlot.onTake` (`inventory_click.go:258-260`) is the no-op to replace. Wiring the 2×2 to the matcher = call `slotChangedCraftingGrid` when slots 1-4 change in `doClick`, and make `onTake` consume. This is "crafting in your own inventory" and is a smaller-scope second proof alongside the 3×3 table.

### Anti-Patterns to Avoid
- **Hardcoding the match in Go.** The whole phase is "through the plugin." The Go side builds the input, calls the plugin, and applies the result/consume to tick state. The MATCH (shaped/shapeless logic) lives in the plugin.
- **Skipping the bounding-box shrink.** Matching the raw 3×3 against a 2×1 pattern fails. You MUST shrink to the non-empty bounding box first (`CraftingInput.ofPositioned`) — vanilla does, so a faithful port must.
- **Forgetting the mirror try.** A shaped recipe matches its horizontal mirror unless `symmetrical`. Dropping the mirror breaks ~half of asymmetric recipes.
- **Consuming the whole stack instead of 1 per cell.** `removeItem(slot, 1)` — exactly one item per used ingredient cell, per craft. Taking the whole stack is the classic crafting bug.
- **Re-running the matcher per tick per grid.** Call it on a grid CHANGE and a result TAKE (discrete), not in a per-tick scan (Phase-22 hot-path rule).
- **Letting the plugin parse JSON / read files.** No sandbox builtin for it. Go parses; the plugin gets structured data.
- **Calling the matcher off the tick goroutine.** TICK-05: the Call runs on the tick over tick-owned slots; fresh Thread per call.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Recipe data | Hand-transcribe 1585 recipes into Go | `//go:embed` the jar `recipe/` JSON tree (the `level/loot/embed.go` pattern) + a parser | 1129 crafting recipes; CLAUDE.md forbids hand-transcription. The JSON is the authoritative jar export. |
| The window/open machinery | A new OpenScreen/windowId/ContainerSetContent stack | Clone `server/chest_open.go` (`nextContainerCounter`, `openScreen`, `sendChestContent`, `openContainer`) | The chest path already solved windowId allocation + screen open + slot sync + click routing. The crafting menu is a re-layout of it. |
| The click engine over the grid+result | A from-scratch click handler | Clone `server/chest_click.go` (PICKUP/QUICK_MOVE/THROW over a two-region `resolveSlot`) + the `menuSlot` Slot primitives (`safeInsert`/`safeTake`/`tryRemove`) | The Slot-primitive engine (`inventory_click.go`) is the 1:1 jar `Slot`/`doClick` port; reuse it, don't re-port it. |
| ItemStack ops (split/grow/shrink/same-item) | New stack helpers | `stackSplit`/`stackCopyWithCount`/`stackSameItemSameComponents`/`stackMaxSize` (`inventory_click.go:47-104`) | Already the 1:1 `ItemStack` port. |
| Item name→id | A new registry | `registryid.Item` ([]string indexed by id → index = id) | Already generated; index lookup is name→id. (Note: `item.ByID` is id→Item; there's no `ByName` map — build one from `registryid.Item` or linear-scan, see Open Q.) |
| Menu-type id for OpenScreen | A constant | `menuTypeID(registryid.Menu, "minecraft:crafting")` (`chest_open.go:75`) | The exact helper the chest open uses; `"minecraft:crafting"` is in `registryid.Menu`. |
| The Go→plugin call | A custom RPC/ABI | `starlark.Call(th, fn, args, nil)` reading the return value | First-class Starlark functions return values; convert the result with the value-read helper (Context7-confirmed return semantics). |

**Key insight:** Phase 25 writes almost no NEW machinery. The recipe data is the `level/loot` twin; the menu is the chest twin; the click engine + ItemStack ops are the existing `inventory_click.go` 1:1 port; the plugin call is a thin value-returning extension of Phase-22's `Emit`. The genuinely new code is: (1) the recipe model+parse, (2) the shaped/shapeless MATCH ported into Starlark, (3) the `set_recipe_matcher` builtin + `Match`/`Remaining` Go seam, (4) the 3×3 menu re-layout, and (5) un-stubbing `ResultSlot.onTake`. The RISK is fidelity (mirroring, bounding-box shrink, 1-per-cell consume), not volume.

## Common Pitfalls

### Pitfall 1: Skipping the bounding-box shrink → recipes never match
**What goes wrong:** You match the raw 3×3 grid against a 2-tall pattern and it never matches because the pattern is 1×2, not 3×3.
**Why it happens:** `CraftingInput.of(w, h, items)` calls `ofPositioned` which SHRINKS the grid to the minimal bounding box of non-empty cells before matching (`javap`'d this session — it computes min/max row/col over non-empty stacks). The recipe's `width`/`height` are the PATTERN dims, compared against the SHRUNK input dims.
**How to avoid:** Port `ofPositioned`: scan the grid, find min/max non-empty row+col, produce the trimmed sub-grid + its `(left, top)` offset. Match against the trimmed grid. The `(left, top)` is also needed by the consume to map trimmed cells back to real grid slots.
**Warning signs:** Only recipes that fill all 9 cells craft; planks-in-corner→stick fails.

### Pitfall 2: Dropping the mirror → asymmetric recipes break
**What goes wrong:** A shaped recipe with an asymmetric pattern crafts only in one orientation; its mirror image silently fails.
**Why it happens:** `ShapedRecipePattern.matches` tries the pattern MIRRORED first (unless `symmetrical`), then un-mirrored (`javap`'d: `if (!symmetrical && matches(input, true)) return true; return matches(input, false);`).
**How to avoid:** Compute `symmetrical` (pattern equals its horizontal mirror) at parse time; in match, try mirrored then unmirrored. The mirror index is `ingredients[width - col - 1 + row*width]`.
**Warning signs:** A recipe works left-handed but not right-handed (or vice-versa).

### Pitfall 3: Consuming the whole stack / wrong cells → the crafting dupe-or-eat bug
**What goes wrong:** Taking the result eats the entire ingredient stack, or eats the wrong cells, or doesn't put the empty bucket back.
**Why it happens:** `ResultSlot.onTake` does `removeItem(slot, 1)` per USED cell (mapped through the positioned `(left, top)`), then places `getRemainingItems[cell]` (e.g. empty bucket) back. The consume operates on the POSITIONED region, not the raw grid.
**How to avoid:** Port the exact double-loop: `slot = (col+left) + (row+top)*gridWidth`, `removeItem(slot, 1)`, then handle `remaining[col+row*width]` (set into the now-empty cell, or merge/drop). `getRemainingItems` default = all-empty (no buckets in the gate recipes), so the gate consume is "shrink each used cell by 1."
**Warning signs:** Crafting eats more than one of each ingredient; or a water-bucket recipe deletes the bucket instead of returning an empty one.

### Pitfall 4: Tag ingredients (`#minecraft:planks`) unresolved
**What goes wrong:** The stick recipe (`key.#: "#minecraft:planks"`) never matches because you compared the ingredient string `"#minecraft:planks"` to an item id.
**Why it happens:** Recipe ingredients are frequently item TAGS (`#namespace:tag`), each expanding to a SET of member item ids. `Ingredient.test` matches if the stack's item is IN the tag's set.
**How to avoid:** At parse time, resolve a `#tag` ingredient to its member-id set from the embedded tags (`server/registrydata/tags.go`, `//go:embed tags`). A bare item id ingredient is a one-element set. The matcher tests set-membership. (For the CUSTOM-recipe gate, use a direct-id recipe to sidestep tags; for the VANILLA gate, a tag-free recipe like `4 oak_planks → crafting_table`-style direct items, OR resolve `#minecraft:planks` — pick a vanilla recipe whose tag is easy, or implement tag resolution.) See Open Questions §tag scope.
**Warning signs:** Direct-id custom recipes craft but vanilla `stick` doesn't.

### Pitfall 5: The result slot is take-only but you let it accept placement
**What goes wrong:** A player drops an item INTO the result slot.
**Why it happens:** `ResultSlot.mayPlace` returns false (you can never place into a result). Sulfur already encodes this (`inventory_click.go:143`, `m.index==0 → return false`) for the player window; the 3×3 menu must too.
**How to avoid:** Result slot `mayPlace=false`, `mayPickup=true`; taking it triggers `onTake` (the consume). Reuse the existing rule.

### Pitfall 6: Recomputing the result after a take (chained crafting)
**What goes wrong:** After taking one result, the grid still has ingredients for another craft, but the result slot stays empty until the player jiggles the grid.
**Why it happens:** Vanilla re-runs `slotsChanged` after `onTake` (the grid shrank → re-evaluate). Sulfur must re-call the matcher after the consume so the result repopulates.
**How to avoid:** After applying the consume, call `slotChangedCraftingGrid` again (re-match the now-shrunk grid) and re-send the result slot.

### Pitfall 7: Grid items lost on close
**What goes wrong:** Closing the crafting_table with items in the grid silently deletes them.
**Why it happens:** A 3×3 crafting grid is TRANSIENT (no backing block-entity, unlike a chest). Vanilla `CraftingMenu.removed` → `clearContainer` drops the grid contents back to the player (or to the world). The chest path persists items in the BE; the crafting menu must instead RETURN them on close.
**How to avoid:** On `handleContainerClose` for a crafting window, move each grid cell back into the player inventory (or drop as an ItemEntity if full). Do NOT reuse the chest "items persist in the BE" model — there is no BE.

### Pitfall 8: `-race` needs CGO=1, ship binary needs CGO=0 (carried)
**What goes wrong:** `CGO_ENABLED=0 go test -race ./...` fails.
**How to avoid:** Two gates — ship `CGO_ENABLED=0 go build ./...`; race `CGO_ENABLED=1 go test -race ./...`. Already in CI. `[VERIFIED: Phase-21 Pitfall 3]`

## Code Examples

### The recipe-provider plugin (the dogfood — 1:1 match in Starlark)
```python
# plugins/crafting/main.star — the recipe-MATCHING logic AS A PLUGIN.
# recipes() is a host builtin returning the Go-parsed recipe table (list of dicts);
# the 1:1 shaped/shapeless match below is a literal port of ShapedRecipePattern.matches
# + ShapelessRecipe.matches (CITE: javap'd this session). No GPL paste.

RECIPES = recipes()   # host-provided, Go-parsed from the embedded jar JSON

def _shrink(grid):
    # CraftingInput.ofPositioned: minimal bounding box of non-empty cells. grid is a flat
    # list of 9 (id,count) on a 3x3 (or 4 on a 2x2). Returns (cells, w, h, left, top).
    rows, cols = 3, 3   # menu passes its real dims
    ...                 # min/max non-empty row+col -> trimmed cells + offset

def _matches_shaped(r, cells, w, h):
    if w != r["w"] or h != r["h"]: return False
    # try mirrored then un-mirrored (ShapedRecipePattern.matches), unless symmetrical
    for mirror in ([True, False] if not r["sym"] else [False]):
        if _scan(r, cells, w, h, mirror): return True
    return False

def match(grid):
    cells, w, h, left, top = _shrink(grid)
    for r in RECIPES:
        if r["type"] == "shaped"   and _matches_shaped(r, cells, w, h):    return r["result"]
        if r["type"] == "shapeless" and _matches_shapeless(r, cells):       return r["result"]
    return None   # no recipe -> empty result slot

def remaining(grid):
    # getRemainingItems: per-cell leftover (empty bucket etc.); default all-empty.
    ...

set_recipe_matcher(match)         # NEW host builtin — captures the matcher once at load
set_recipe_remaining(remaining)   # (or fold consume into one matcher returning result+remaining)
```

### The plugin.toml (Phase-22 manifest format — TOML, confirmed `plugin.toml` in `manager.go:73`)
```toml
name = "crafting"
version = "0.1.0"
entrypoint = "main.star"
runtime = "starlark"
```

### Reading the plugin's returned result back into Go
```go
// plugin/host/recipe.go — convert the matcher's return value to a Go (id, count).
// out is a starlark dict {"id": int, "count": int} or starlark.None.
func readResult(out starlark.Value) (id, count int, ok bool) {
    d, isDict := out.(*starlark.Dict)
    if !isDict { return 0, 0, false }
    idv, _, _ := d.Get(starlark.String("id"))
    cnv, _, _ := d.Get(starlark.String("count"))
    iid, _ := starlark.AsInt32(idv)
    icn, _ := starlark.AsInt32(cnv)
    if iid <= 0 || icn <= 0 { return 0, 0, false }
    return int(iid), int(icn), true
}
```
*(`starlark.AsInt32` / dict `.Get` are the standard value-read path; the planner confirms exact symbols at execution against the pinned version. `[ASSUMED: A3]`)*

### Server-side: the grid-change seam (un-stubbing the result) — jar port over the menu
```go
// server/crafting_click.go — slotChangedCraftingGrid (1:1 CraftingMenu.slotChangedCraftingGrid).
// Called whenever a grid cell (player window 1-4, or 3x3 menu 1-9) changes.
func (t *TickLoop) slotChangedCraftingGrid(p *tickPlayer, grid []component.SlotData, w, h int) {
    if t.plugins == nil { t.setResult(p, component.SlotData{Count: 0}); return }
    gridVal := gridToStarlark(grid, w, h)               // flat (id,count) list, frozen scalars
    id, count, ok := t.plugins.Match(gridVal)           // NEW value-returning Call seam
    if !ok { t.setResult(p, component.SlotData{Count: 0}); return }
    t.setResult(p, component.SlotData{ItemID: pk.VarInt(id), Count: pk.VarInt(count)})
    // + send ClientboundContainerSetSlot(win, stateId, RESULT_SLOT=0, result) like the jar.
}
```

### Server-side: ResultSlot.onTake — the consume (replaces `inventory_click.go:258-260` no-op)
```go
// 1:1 ResultSlot.onTake: positioned input -> getRemainingItems -> removeItem(cell,1) per used cell.
func (m menuSlot) onTakeCraft(t *TickLoop, p *tickPlayer, grid []component.SlotData, w, h, gw int) {
    cells, left, top := positioned(grid, gw)            // CraftingInput.asPositionedCraftInput
    remaining := t.plugins.Remaining(gridToStarlark(cells, w, h)) // buckets-back; default empty
    for row := 0; row < h; row++ {
        for col := 0; col < w; col++ {
            slot := (col + left) + (row+top)*gw
            cur := m.inv.get(int16(craftGridBase + slot))
            if !stackEmpty(cur) {
                m.inv.set(int16(craftGridBase+slot), shrinkBy1(cur)) // removeItem(slot, 1)
            }
            // place remaining[col+row*w] back (empty bucket) — default no-op for gate recipes
        }
    }
    // re-run slotChangedCraftingGrid so the result repopulates if the grid still matches (Pitfall 6).
}
```

### The crafting_table open (clone of `chest_open.go:openChest`)
```go
// server/crafting_menu.go — useBlockInteraction handles crafting_table (alongside chest).
func (t *TickLoop) openCraftingTable(p *tickPlayer, pos pk.Position) bool {
    if p.client == nil { return false }
    win := p.nextContainerCounter()
    p.openContainer = &openContainer{windowID: win, craftingTable: true} // no chestPos/BE
    menuID := menuTypeID(registryid.Menu, "minecraft:crafting")          // the 3x3 menu type
    p.client.Send(openScreen(int32(win), menuID, "Crafting"))
    t.sendCraftingContent(p)                                             // result(0)+grid(1-9)+inv
    return true
}
```

## State of the Art

| Old (Sulfur today) | Phase 25 | Impact |
|--------------------|----------|--------|
| `ResultSlot.onTake` = no-op (`inventory_click.go:258-260`), no recipes wired | Real onTake consume driven by the plugin matcher | The 2×2 player grid actually crafts. |
| Only the 2×2 player-inventory grid slots exist (result no-op) | + a 3×3 crafting_table block + menu | Net-new crafting station. |
| Recipes are not represented at all (the `Recipes` component is the recipe-BOOK unlock list, not definitions) | `level/recipe` embeds + parses the jar recipe JSON | Authoritative recipe data, `//go:embed` like `level/loot`. |
| Plugin host = fire-and-forget `Emit` (Go→plugin, void) | + a value-returning `Match` Call seam (Go→plugin→result) | The API generalizes to QUERIES, not just events — the dogfood. |

**Deprecated/irrelevant for this phase:**
- The recipe BOOK (`ClientboundRecipe`, unlock advancements, the `minecraft:recipes` component) — not needed for a plugin-driven matcher.
- Smelting/furnace — needs a separate block+menu+cook-tick subsystem (deferred).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The FIRST port targets `crafting_shaped` + `crafting_shapeless` only (the grid-crafting gate); smelting/stonecutting/smithing are SHAPED-for but deferred (they need furnace/other menus). PLUGIN-05 lists "shaped/shapeless/smelting/…" but the gate is grid crafting. | User Constraints / Open Q | Low — flagged as an operator decision; shaped+shapeless satisfy the gate. Adding smelting later reuses the model. |
| A2 | The consume LOOP (removeItem-per-cell + remaining placement) lives in GO, driving plugin-returned result+remaining data, because Go owns tick-state mutation (TICK-05). The plugin computes WHAT to consume; Go applies it. | Architecture §Pattern 4 / Open Q | Low–Medium — alternative is the plugin returning a whole new grid; either is faithful. Go-applies keeps tick-state mutation Go-side. Operator/planner picks. |
| A3 | `starlark.AsInt32` + `*starlark.Dict.Get` are the value-read symbols for converting the matcher's return; exact names confirmed against the pinned version at execution. | Code Examples §value read | Low — standard library surface; verify symbol names at planning. |
| A4 | A Go-parsed recipe table is handed to the plugin (via a `recipes()` builtin or a `register_recipes` call), NOT the plugin parsing JSON (no sandbox json/file builtin). | Standard Stack / Architecture §Pattern 2 | Low — forced by the Phase-21 sandbox (no `json`/`open`). The only question is builtin shape. |
| A5 | Tag ingredients (`#minecraft:planks`) resolve at PARSE time from the embedded `server/registrydata/tags.go` tree to a member-id set. The custom-recipe gate can use direct ids to sidestep tags. | Common Pitfalls §4 / Open Q | Medium — if tag resolution is harder than expected, the VANILLA gate recipe must be tag-free (rare for crafting) OR tag resolution is a sized sub-task. Flag for the planner. |
| A6 | `item.ByID` is id→Item; there is NO `ByName` map. Name→id uses `registryid.Item` ([]string indexed by id → index = id). A small name→id helper (or a built map) is a Phase-25 task. | Standard Stack §Supporting | Low — `registryid.Item` index lookup works; building a map from it is trivial. |

## Open Questions

1. **Recipe-type scope: shaped+shapeless only, or also smelting?**
   - Known: 1129 crafting (shaped/shapeless) + ~456 others (smelting/stonecutting/smithing). The gate is grid crafting. Smelting needs a furnace block+menu+cook-tick.
   - Recommendation: port shaped+shapeless now; SHAPE the recipe model so smelting parses (it's in the embedded data), but do NOT build a furnace menu (no-built-but-unwired). Operator confirms.

2. **Consume locus: Go applies plugin-returned deltas, or the plugin returns a whole new grid?** (A2)
   - Recommendation: plugin returns `result` + `remaining` (the jar `getRemainingItems`); Go runs the `removeItem(cell,1)` loop over tick-owned slots (TICK-05 owns mutation). Either is faithful; pick at kickoff.

3. **Tag-ingredient resolution scope.** (A5)
   - Known: vanilla crafting recipes lean heavily on `#tag` ingredients (planks, logs, etc.). Tags are embedded (`registrydata/tags.go`).
   - Recommendation: resolve tags→member-set at parse time. If sized too large for this phase, the VANILLA gate recipe is chosen to be tag-light and the custom-recipe gate uses direct ids. Surface the size estimate to the planner.

4. **The item/grid handle shape (vs Phase-23 entity handles).**
   - Known: Phase 23 builds FROZEN ENTITY handles. The crafting grid is 9× (id,count) scalars — it needs NO live handle, just a frozen list (Phase-22-style payload). But PLUGIN-05's "item/menu bridge" might be intended to reuse the Phase-23 handle pattern.
   - Recommendation: pass the grid as plain frozen scalars (a `starlark.List` of (id,count) tuples) — simplest + race-safe. Note that this means Phase 25 is INDEPENDENT of Phase 23's entity-handle work (it does not need a live entity handle), though it follows the same frozen-payload discipline. Confirm with the operator whether a richer item handle is wanted (it is not needed for the gate).

5. **Where does the recipe-provider plugin live — `plugins/` dir (operator-installed) or an embedded built-in plugin?**
   - Known: vanilla recipes are a CORE feature, not an optional plugin; but the dogfood requires them to load THROUGH the plugin path.
   - Recommendation: ship the vanilla recipe plugin as a built-in (loaded from an embedded/standard `plugins/crafting/` the server always loads), so a default server crafts, while still proving the plugin path. A custom recipe is an additional operator plugin. Operator confirms whether the vanilla matcher is embedded-default or operator-installed.

## Dependency on Phase 23 vs independence (RESEARCH target #7)

- **DEPENDS on Phase 22 (host):** the `Manager`, `LoadDirWith`, the register-builtin pattern, `starlark.Call` dispatch. VERIFIED present (`plugin/host/*`). Phase 25 EXTENDS it with the value-returning `Match` seam.
- **DEPENDS on Phase 22's frozen-payload discipline** (frozen scalars cross the tick boundary) — but NOT on Phase 23's live ENTITY handles. The crafting grid is plain (id,count) scalars; it needs no entity/world/nav handle. **Phase 25 is effectively independent of Phase 23's handle API** — it reuses the SAME frozen-payload concept Phase 22 established, applied to a grid value. (If the planner sequences 25 after 23, that's fine, but 25 does not block on 23's entity-handle deliverable.)
- **Does NOT depend on Phase 24** (mob plugins) — confirmed by v4-PLAN build-order rationale ("It needs 22's host + an item/menu bridge; it does NOT depend on 24"). Ordering after 24 is narrative ("domain 1 then domain 2"), not technical.
- **Independent sub-deliverables** (can proceed without the plugin seam): the recipe DATA extraction+parse (`level/recipe`, pure) and the 3×3 menu+block (the chest-clone) are buildable independently and only JOIN the plugin at the matcher Call.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `temp/cache/26.2-inner.jar` + `javap` (Zulu 25) | 1:1 verification of match/consume/menu | ✓ | Zulu 25 | — (mandatory) |
| `temp/cache/26.2-datagen/.../recipe/*.json` | recipe data extraction | ✓ (1585 files, 1129 crafting) | 26.2 | — |
| `temp/cache/.../tags` (or `server/registrydata/tags`) | tag-ingredient resolution | ✓ (embedded) | 26.2 | direct-id recipes sidestep tags |
| `go.starlark.net` | the recipe plugin + Call seam | ✓ (Phase 21/22) | v0.0.0-20260613233743-8ba36ccb83fb | — |
| `plugin/host` + `plugin/starlark` | the host + runtime | ✓ (Phase 21/22 landed) | — | **BLOCKING if those phases not done** |
| `registryid.Menu` has `"minecraft:crafting"` | OpenScreen menu type | ✓ (verified) | — | — |
| `registryid.Item` (name↔id), `item.ByID` | ingredient/result id mapping | ✓ | — | — |
| Go ≥ 1.25, stdlib `embed`/`json` | parse+embed | ✓ | 1.25/1.26.1 | — |
| C compiler (for `-race` only) | race gate | ✓ in CI | — | n/a (CGO=1 in CI; ship CGO=0) |

**Missing with no fallback:** none — all jar/data/runtime deps present.
**Missing with fallback:** tag resolution (fallback: tag-free / direct-id gate recipes).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `sync` for the race proof) |
| Config file | none (Go convention) |
| Quick run | `go test ./level/recipe/ ./plugin/host/ ./server/ -run Craft` |
| Full suite (race) | `CGO_ENABLED=1 go test -race ./level/recipe/ ./plugin/host/ ./server/` |
| Ship-build gate | `CGO_ENABLED=0 go build ./...` |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| PLUGIN-05 | Recipe JSON parses (shaped+shapeless) from the embedded tree; name→id + tag resolution | unit | `go test ./level/recipe/ -run TestParse` | ❌ Wave 0 |
| PLUGIN-05 | Shaped match: bounding-box shrink + mirror + cell test (jar-verified vs `ShapedRecipePattern.matches`) | unit | `go test ./plugin/host/ -run TestShapedMatch` | ❌ Wave 0 |
| PLUGIN-05 | Shapeless match: multiset cover (jar-verified vs `ShapelessRecipe.matches`) | unit | `go test ./plugin/host/ -run TestShapelessMatch` | ❌ Wave 0 |
| PLUGIN-05 | **The plugin path drives the result** — a grid change calls the plugin matcher and slot 0 gets the result | integration | `go test ./server/ -run TestCraftResultThroughPlugin` | ❌ Wave 0 |
| PLUGIN-05 | **Consume is 1-per-cell, positioned** — taking the result shrinks each used ingredient by exactly 1 (jar-verified vs `ResultSlot.onTake`) | integration | `go test ./server/ -run TestCraftConsume` | ❌ Wave 0 |
| PLUGIN-05 | **A vanilla recipe crafts end-to-end** (e.g. 2×planks→4 sticks shaped, or 1 log→4 planks shapeless): grid→result→take→consume | integration | `go test ./server/ -run TestVanillaRecipeGate` | ❌ Wave 0 |
| PLUGIN-05 | **A custom recipe works** — an operator plugin adds a non-vanilla recipe; it crafts the same way | integration | `go test ./server/ -run TestCustomRecipeGate` | ❌ Wave 0 |
| PLUGIN-05 | crafting_table right-click opens the 3×3 menu (OpenScreen `minecraft:crafting`) | integration | `go test ./server/ -run TestCraftingTableOpen` | ❌ Wave 0 |
| PLUGIN-05 | Closing the crafting menu returns grid items to the player (no item loss) | integration | `go test ./server/ -run TestCraftingClose` | ❌ Wave 0 |
| PLUGIN-05 | Result recomputes after a take (chained crafting) | integration | `go test ./server/ -run TestCraftChained` | ❌ Wave 0 |
| PLUGIN-05 | The match Call seam is race-clean | race | `CGO_ENABLED=1 go test -race ./plugin/host/ ./server/ -run Craft` | ❌ Wave 0 |
| PLUGIN-05 | Default binary is CGO=0 | build | `CGO_ENABLED=0 go build ./...` | ✓ (gate exists) |

**The signature gate test** (proves "through the plugin", not just "crafting works"): `TestVanillaRecipeGate` + `TestCustomRecipeGate` — a vanilla recipe (jar-verified result+consume) AND a custom recipe both craft via the SAME plugin matcher path, with the result coming from `Manager.Match` (a plugin Call), not a hardcoded Go table. Assert the consume is exactly 1-per-used-cell.

### Sampling Rate
- **Per task commit:** `go test ./level/recipe/ ./plugin/host/ ./server/ -run Craft`
- **Per wave merge / phase gate:** `CGO_ENABLED=1 go test -race ./level/recipe/ ./plugin/host/ ./server/` AND `CGO_ENABLED=0 go build ./...`
- **Phase gate (full):** the Docker `-race` image (CGO=1) green + the CGO=0 static build green (both in `.github/workflows/go.yml`).

### Wave 0 Gaps
- [ ] `level/recipe/{embed.go, model.go, parse.go}` + `data/recipe/` (copy from `temp/cache/26.2-datagen/.../recipe/`).
- [ ] `plugin/host/recipe.go` — `set_recipe_matcher` builtin + `Match`/`Remaining` Go API + value-read.
- [ ] `plugins/crafting/{plugin.toml, main.star}` — the recipe-provider plugin (1:1 shaped/shapeless match).
- [ ] `server/{crafting_menu.go, crafting_click.go}` — crafting_table open + 3×3 menu + slotChangedCraftingGrid + onTake (clones of chest_open/chest_click).
- [ ] EDIT `server/inventory_click.go` — un-stub `onTake` (258-260); wire the 2×2 player grid (slots 1-4 → matcher).
- [ ] Test fixtures: a known vanilla recipe + a custom-recipe plugin (`plugins/customrecipe/`).
- [ ] `jq`/`javap` re-verification of the chosen gate recipes against the jar (shaped mirroring, consume).

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V5 Input Validation / untrusted-code execution | yes | The match Call runs on a fresh `starlark.Thread` with the Phase-21 step budget (a runaway matcher returns `*EvalError`, never hangs the tick). The GRID handed to the plugin is server-built (authoritative slot contents) — the client never supplies recipe data (T-6-02 carries: client item claims are discarded). |
| V4 Access Control (server-authoritative crafting) | yes | The result + consume are SERVER-computed (the plugin runs server-side over server slots). A creative-count guard / item-dupe guard: the consume is 1-per-cell, the result is the recipe's template — a client cannot inject a result or skip the consume. |
| V1.4 Trust boundary | partial | The recipe plugin is operator-installed (like other plugins). A hostile matcher is bounded by the step budget + isolation (one bad matcher returns no result, doesn't crash the tick). |
| V6 Cryptography | no | None. |

### Known Threat Patterns for the crafting path
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| A client forges a result / claims an item it doesn't have | Tampering / Elevation | The grid is server-built from authoritative slots (the HashedStack click hashes are already discarded, `inventory_click.go:195`); the result comes from the server-side matcher; the consume is server-side 1-per-cell. |
| A matcher infinite-loops and hangs the tick | Denial of Service | Fresh thread + `SetMaxExecutionSteps` per Call (Phase-21 mechanism). On `*EvalError`, no result, tick continues. |
| Item dupe via take without consume (or consume>1) | Tampering | The 1:1 `ResultSlot.onTake` port: `removeItem(cell, 1)` per used cell, positioned region only. Tested by `TestCraftConsume`. |
| Creative-mode count/clone interaction with the result | Tampering | The result slot is take-only (`mayPlace=false`); creative clone (`safeClone`/`doClickClone`) on a result clones the result item (vanilla behavior) — verify against `doClickClone` (`inventory_doclick.go:311`) so a creative player can't dupe ingredients. Flag as a consume-path test. |
| Grid items lost on close | (Availability) | `CraftingMenu.removed` → return grid items to the player (Pitfall 7), tested by `TestCraftingClose`. |

## Sources

### Primary (HIGH confidence)
- **`temp/cache/26.2-inner.jar`** (`javap -c -p` this session) — `ShapedRecipe.matches`/`assemble`, `ShapedRecipePattern.matches(input)` + private `matches(input, mirrored)` (the bounding-box + mirror logic), `ShapelessRecipe.matches` (multiset via `StackedItemContents.canCraft`), `CraftingInput.of`/`ofPositioned` (bounding-box shrink), `CraftingMenu` (slot indices RESULT_SLOT=0/CRAFT_SLOT 1-9, `slotChangedCraftingGrid` → `RecipeManager.getRecipeFor` → `assemble` → `setItem(0,result)` + SetSlot, `removed`, `quickMoveStack`), `ResultSlot.onTake` (positioned input → `getRemainingItems` → `removeItem(cell,1)` per used cell). `[VERIFIED]`
- **`temp/cache/26.2-datagen/generated/data/minecraft/recipe/`** — 1585 recipe JSON (1129 crafting_shaped/shapeless/smelting); confirmed shape of `stick.json` (shaped, tag key `#minecraft:planks`, pattern, result {count,id}), `oak_planks.json` (shapeless, tag ingredient), a smelting example. `[VERIFIED]`
- **`server/inventory_click.go` + `inventory_doclick.go`** (read in full) — `menuSlot` (the 1:1 `Slot` port: `safeInsert`/`safeTake`/`tryRemove`/`mayPlace`/`onTake`), the slot-index map (0=result, 1-4 craft grid, 5-8 armor, 9-44 main/hotbar, 45 offhand), `ResultSlot.onTake` no-op stub (258-260), `mayPlace(0)=false`, the 7 `doClick` inputs, `doClickClone` (creative). `[VERIFIED]`
- **`server/chest_open.go` + `chest_click.go` + `chest_loot.go`** (read in full) — the OPEN-menu template: `useBlockInteraction`, `nextContainerCounter`, `openScreen` + `menuTypeID(registryid.Menu, ...)`, `openContainer{windowID, chestPos}`, `sendChestContent`/`ContainerSetContent`, the window-specific click engine (`chestResolveSlot`/`clickedChest`/`doChestClick`), `handleContainerClose`. The structural twin of the crafting menu. `[VERIFIED]`
- **`server/inventory.go`** — `Inventory` (`slots []SlotData`, `carried`, `stateID`), `clicked` routing to `openContainer`/`clickedChest` (225-231), `broadcastInventoryChanges`, `handleContainerClose`. `[VERIFIED]`
- **`level/loot/embed.go`** — the `//go:embed data/loot_table` + `LoadTable`/`ParseTable` pattern the recipe data must mirror. `[VERIFIED]`
- **`plugin/host/{manager.go, register.go, builtins.go, event.go, emit.go}`** + **`plugin/starlark/loader.go`** — the host surface Phase 25 extends: `Manager`, `LoadDirWith(root, extra)`, `makeRegisterBuiltin`, `Emit` (fire-and-forget — the seam Phase 25 makes value-returning), `LoadWith(path, predeclared)`, `NewThread`, `plugin.toml` manifest. `[VERIFIED]`
- **`data/registryid/{menu.go (has "minecraft:crafting"), item.go (name→id by index), recipetype.go}`** + **`data/item/item.go` (`ByID`, no `ByName`)**. `[VERIFIED]`
- **`.planning/v4-PLAN.md` (Phase 25 row + build order) + `REQUIREMENTS.md` (PLUGIN-05)** — through-the-plugin mandate, 1:1 carry, custom-recipe gate, crafting_table+3×3, independence from Phase 24. `[CITED]`
- **`.planning/phases/{21,22}-*/RESEARCH.md`** — the runtime + host foundation, the frozen-payload + discrete-seam discipline, the CGO=0 / `-race`-CGO=1 split. `[VERIFIED]`
- **Context7 `/google/starlark-go`** — `starlark.Call(th, fn, args, nil)` returns a `starlark.Value`; functions return values (None/single/tuple); the embed pattern; struct/dict return types. `[CITED]`

### Secondary (MEDIUM confidence)
- The exact `starlark.AsInt32`/`*starlark.Dict.Get` value-read symbols (A3) — standard library surface, verify names at planning.

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Jar match/consume/menu logic (shaped mirror+shrink, shapeless multiset, ResultSlot.onTake 1-per-cell, CraftingMenu slot layout): **HIGH** — every method `javap`'d this session against the actual jar; the bounding-box shrink and mirror-then-unmirror are read directly from bytecode.
- Recipe data approach (embed jar JSON, parse, name→id, tags): **HIGH** — the JSON shape is confirmed; the `level/loot` embed twin is the proven pattern; tag resolution is the one MEDIUM sub-item (A5).
- Sulfur inventory/menu types + the chest-clone template: **HIGH** — every named type read from the actual `server/` files this session.
- The plugin bridge (value-returning `Match` Call beyond `Emit`): **MEDIUM-HIGH** — the mechanism (`starlark.Call` reading a return value) is Context7-confirmed and a thin extension of the verified Phase-22 host; the exact builtin signature + consume locus are planner/operator decisions (Open Q 2, A2/A4).
- Phase-23 independence: **HIGH** — derived from the grid being plain scalars + the v4-PLAN build-order text.

**Research date:** 2026-06-28
**Valid until:** ~30 days for the Starlark API (stable). The jar logic is fixed for 26.2. Re-grep the Sulfur seam names (`ResultSlot.onTake` stub location, `chest_open.go`/`chest_click.go` helpers, `inventory_click.go` slot map) by NAME at execution — line numbers drift, names are the stable anchors. Re-verify the `go.starlark.net` pin and the `starlark.AsInt32`/dict-read symbols at planning.
