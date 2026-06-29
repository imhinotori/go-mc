---
phase: 25
slug: crafting-recipes-as-plugins
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-28
---

# Phase 25 — Validation Strategy

> 1:1 mandate → bytecode-fidelity on the match + consume is the load-bearing check.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Quick run command** | `CGO_ENABLED=0 go test ./level/recipe/ ./plugin/... ./server/` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./level/recipe/ ./plugin/... ./server/ && CGO_ENABLED=0 go test ./level/recipe/ ./plugin/... ./server/` |
| **-race command (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./plugin/... ./server/` |
| **1:1 anchor** | `javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>` before the match/consume port |
| **Estimated runtime** | ~20–50s |

---

## Sampling Rate

- **After every task commit:** the quick suite
- **After every plan wave:** full suite + Docker `-race` (the menu click + the tick-owned consume are -race-relevant)
- **Before `/gsd-verify-work`:** full suite green + Docker `-race` green
- **Max feedback latency:** ~50s quick / ~3 min race

---

## Per-Task Verification Map

> Provisional. The PLUGIN-05 load-bearing samples — each port carries a jar citation.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 25-01-01 | 01 | 1 | PLUGIN-05 | — | level/recipe data extraction + parse (//go:embed the jar recipe/ JSON, the level/loot twin), all recipe types parsed | unit | `CGO_ENABLED=0 go test ./level/recipe/ -run TestRecipeParse` | ❌ W0 | ⬜ pending |
| 25-01-02 | 01 | 1 | PLUGIN-05 | — | the 1:1 match: shaped (bbox SHRINK + MIRROR-then-unmirror, ShapedRecipePattern.matches), shapeless (multiset, StackedItemContents), cooking/stonecutting single-ingredient — jar-cited, bytecode-matched | unit | `CGO_ENABLED=0 go test ./level/recipe/ -run TestMatch` | ❌ W0 | ⬜ pending |
| 25-01-03 | 01 | 1 | PLUGIN-05 | — | the value-returning Match seam: set_recipe_matcher(fn) builtin + Go Match(grid)→result on the tick goroutine (extends Emit from dispatch to query-resolution); the recipe-provider plugin registers a matcher | unit | `CGO_ENABLED=0 go test ./plugin/host/ -run TestMatchSeam` | ❌ W0 | ⬜ pending |
| 25-02-01 | 02 | 2 | PLUGIN-05 | T-25-01 | un-stub ResultSlot.onTake (inventory_click.go:258-260) wired to the matcher + the 1:1 consume (removeItem 1-per-used-cell + getRemainingItems bucket-back); the 2×2 inventory grid crafts | unit | `CGO_ENABLED=0 go test ./server/ -run TestResultSlotConsume` | ❌ W0 | ⬜ pending |
| 25-02-02 | 02 | 2 | PLUGIN-05 | — | crafting_table block + 3×3 menu (the chest-open clone: counter→openScreen→ContainerSetContent→click→close); right-click opens, the grid crafts | unit | `CGO_ENABLED=0 go test ./server/ -run TestCraftingTableMenu` | ❌ W0 | ⬜ pending |
| 25-02-03 | 02 | 2 | PLUGIN-05 | — | cooking/stonecutting BLOCK: build the minimal furnace/stonecutter block+menu+cooking-tick 1:1 where feasible, OR ship the matcher + DEFER the block with a CITED reason (deferred-blocks.md: jar class + missing subsystem + evidence) — no fake | unit | `CGO_ENABLED=0 go test ./server/ -run TestCookingOrDeferred` | ❌ W0 | ⬜ pending |
| 25-02-04 | 02 | 2 | PLUGIN-05 | — | THE GATE: a vanilla recipe crafts through the plugin path (result + per-cell consume, jar-verified — e.g. planks→sticks shapeless or a shaped recipe) AND a custom recipe works; the vanilla recipe plugin is //go:embed'd + boot-loaded | integration | `CGO_ENABLED=0 go test ./server/ -run 'TestVanillaRecipeCrafts\|TestCustomRecipeWorks'` | ❌ W0 | ⬜ pending |
| 25-02-05 | 02 | 2 | PLUGIN-05 | T-25-01 | RACE: menu click + the tick-owned consume + the Match call are -race clean | race | Docker `-race ./plugin/... ./server/` `-run TestCraftingRace` | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `level/recipe/embed.go` + `parse.go` (the level/loot twin — //go:embed the jar recipe/ JSON) + `recipe_test.go`
- [ ] `level/recipe/match.go` (shaped/shapeless/cooking/stonecutting match, 1:1 jar) + the match tests with jar citations
- [ ] `plugin/host/match.go` (the set_recipe_matcher builtin + the value-returning Match seam) + its test
- [ ] `server/crafting_menu.go` + `crafting_click.go` (the 3×3 menu, the chest-open clone) + the un-stub of inventory_click.go ResultSlot.onTake
- [ ] the cooking/stonecutting block work OR `deferred-blocks.md` (cited)
- [ ] the bundled vanilla recipe plugin (//go:embed the .star + the recipe data) + a custom-recipe test plugin
- [ ] javap evidence for the match + consume (the 1:1 anchor)

*The 1:1 mandate: the match + consume are written ONLY after javap-confirming against the jar. The 3 pitfalls (shaped shrink, shaped mirror, 1-per-cell consume) are the bytecode-faithful core.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | — |

*All Phase-25 behaviors are automatable headless (crafting through the plugin path is testable; the menu click engine is the Phase-20 chest pattern, already tested headless). The real-client visual gate is Phase 28.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] The match + consume have jar-citations in their acceptance
- [ ] Feedback latency < 50s (quick) / race in Docker
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
