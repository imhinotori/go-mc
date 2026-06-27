---
phase: 20-structure-polish-loot-inhabitants-beard-persistence
plan: 01
subsystem: loot
tags: [loot-table, rng, legacy-random-source, go-embed, vanilla-parity, determinism]

# Dependency graph
requires:
  - phase: 09-worldgen-parity
    provides: world/levelgen.LegacyRandomSource (the java.util.Random LCG = the loot RNG)
  - phase: 17-block-drops
    provides: level/component.SlotData (the Roll result element type) + server/block_drop.go (the future 20-02 consumer)
provides:
  - "level/loot: a pure, self-contained loot-table evaluator (parse + roll engine)"
  - "Roll(table *LootTable, seed int64, ctx *LootContext) []component.SlotData — the single shared evaluator entry point for 20-02 block drops + chest fill"
  - "RollStacks(table, ctx) []ItemStack — raw stacks carrying Enchantments for the chest BE write"
  - "LoadTable(id) / ParseTable(bytes) / TableJSON(id) — the embedded-loot-table loaders (1355 tables)"
  - "the bytecode-hand-traced golden seed-reproduction proof (simple_dungeon @ seed 123456789)"
affects: [20-02-block-drops-chest-fill, 20-03, 20-04, 20-05]

# Tech tracking
tech-stack:
  added: []  # NO new deps — pure stdlib (encoding/json, embed) + in-repo LegacyRandomSource
  patterns:
    - "local //go:embed data tree (level/loot/data) mirroring world/levelgen/data"
    - "type-string validation against data/registryid lists with loud error on unknown (Phase-11 feature-parser discipline)"
    - "cited-stub for an incomplete subsystem (EnchantmentHelper.enchantItem) — never bakes the value away"

key-files:
  created:
    - level/loot/embed.go (//go:embed loot_table + enchantment data; LoadTable/TableJSON)
    - level/loot/model.go (LootTable/LootPool/Entry/NumberProvider/LootFunction/LootCondition/ItemStack)
    - level/loot/parse.go (JSON->model dispatch; type validation; rolls/count cap)
    - level/loot/context.go (LootContext; withOptionalRandomSeed -> LegacyRandomSource)
    - level/loot/provider.go (ConstantValue, Uniform; Mth.nextInt/nextFloat/floor; Math.round)
    - level/loot/function.go (set_count + the conditionalFunction gate + parseFunction dispatch)
    - level/loot/enchant.go (enchant_randomly full port; enchant_with_levels faithful draw + cited stub)
    - level/loot/condition.go (location_check + parseCondition dispatch)
    - level/loot/roll.go (getRandomItemsRaw/addRandomItems/addRandomItem/createItemStack — Roll/RollStacks)
    - level/loot/testdata/golden_loot.json (bytecode-hand-traced expected list + per-draw LCG trace + citations)
    - level/loot/{loot,roll,function,golden}_test.go
    - level/loot/data/ (1355 loot tables + 43 enchantment defs + enchantment tags, embedded)
  modified: []  # server/block_drop.go intentionally NOT touched (that is 20-02)

key-decisions:
  - "Package placement = level/loot (the lowest layer both server and world/structure import; no import cycle — confirmed by go build ./...)"
  - "Embed the WHOLE loot_table tree (1355 tables, 2.9MB) so 20-02 gets the block tables for free (per 20-RESEARCH)"
  - "ConstantValue.getInt = Math.round(value) (the NumberProvider DEFAULT per bytecode), NOT Mth.floor as 20-RESEARCH stated — corrected against javap"
  - "Golden table = simple_dungeon (item+empty + set_count/uniform, NO enchant) so every LCG draw is hand-traceable"
  - "enchant_with_levels' EnchantmentHelper selection stubbed behind a cited constant (CLAUDE.md) — the registry cost/exclusivity subsystem is a later plan; the level-budget draw IS faithful"

patterns-established:
  - "Determinism core: addRandomItem builds eligible list + weight sum, single-eligible fast-path skips nextInt(total), else nextInt(total) subtract-to-select; functions run in JSON order"
  - "Golden proves PARITY WITH THE BYTECODE (independent Python transcription of the LCG+algorithm), not Go self-consistency"

requirements-completed: [STRUCT-POLISH-01]

# Metrics
duration: 16min
completed: 2026-06-27
---

# Phase 20 Plan 01: Shared Loot Evaluator (level/loot) Summary

**A pure, self-contained `level/loot` package that reproduces vanilla Minecraft 26.2 chest/block-drop contents per seed — the LootTable/LootPool roll engine + LegacyRandomSource draw order ported byte-for-byte from the jar, proven by a bytecode-hand-traced golden (simple_dungeon @ seed 123456789).**

## Performance

- **Duration:** 16 min
- **Started:** 2026-06-27T03:43:52Z
- **Completed:** 2026-06-27T04:00:37Z
- **Tasks:** 3/3
- **Files modified:** 14 source/test + the embedded data tree (1421 data files)

## Accomplishments
- The keystone STRUCT-POLISH-01 evaluator exists: `Roll(table, seed, ctx) []component.SlotData`, a pure package importable by both `server` and `world/structure` with NO import cycle.
- The determinism core (rolls.getInt -> nextInt(total) weighted subtract-select -> per-entry functions in JSON order) is ported LITERALLY from the decompiled bytecode, with the single-eligible fast-path that skips the nextInt(total) draw.
- The MANDATORY golden passes: `Roll(simple_dungeon, 123456789)` reproduces the EXACT item list a by-hand trace of the jar bytecode predicts (the LCG draws written out independently in Python, then committed as testdata with cited jar method+offsets) — proving parity with the bytecode, not a Go self-snapshot.
- All 3 in-scope functions (set_count, enchant_randomly fully; enchant_with_levels faithfully with a cited stub for the deferred EnchantmentHelper selection) + 2 number providers (uniform, constant) + 2 entry types (item, empty) ported and tested.

## Task Commits

1. **Task 1: Loot JSON model + parser + embed** - `2e017c58` (feat)
2. **Task 2: Roll engine — context, providers, weighted selection** - `efb66ec0` (feat)
3. **Task 3: 3 functions + golden seed-reproduction** - `8b6486b8` (feat)

_TDD: each task wrote tests + implementation; the parse/draw-order/golden tests are the RED-then-GREEN gates._

## Exported API Surface (for 20-02)

```go
// level/loot
func Roll(table *LootTable, seed int64, ctx *LootContext) []component.SlotData // primary entry point
func RollStacks(table *LootTable, ctx *LootContext) []ItemStack                 // raw stacks (carry Enchantments)
func LoadTable(id string) (*LootTable, error)                                   // e.g. "minecraft:chests/simple_dungeon", "minecraft:blocks/stone"
func ParseTable(data []byte) (*LootTable, error)
func TableJSON(id string) ([]byte, error)
func NewLootContext(seed int64, luck float32) *LootContext
func NewLootContextWithSource(rng levelgen.RandomSource, luck float32) *LootContext

type ItemStack struct { ItemID int32; Count int; Enchantments map[string]int }
// Result element type: level/component.SlotData{ Count, ItemID pk.VarInt }
```

20-02 wires `server/block_drop.go` onto `Roll(loot.LoadTable("minecraft:blocks/<name>"), gameRng-derived-seed, ctx)` (replacing the v1 `blockDropTable` map), and the chest-open path onto `Roll(loot.LoadTable(chest.LootTable), chest.LootTableSeed, ctx)`. The model already carries `Conditions` (pool + entry) + `Children` so the block-table delta (alternatives, match_tool, survives_explosion, apply_bonus, explosion_decay) is added DATA-ONLY.

## Golden Vector (for the live-client visual gate at phase close)

- **Table:** `minecraft:chests/simple_dungeon`
- **Seed:** `123456789`, luck `0`
- **Expected:** leather x4, music_disc_13 x1, wheat x4, bucket x1, gunpowder x4, bone x4, rotten_flesh x1
- **Cited bytecode:** `LootTable.getRandomItemsRaw` -> `LootPool.addRandomItems` (rolls.getInt: UniformGenerator.getInt L#130 -> Mth.nextInt) -> `addRandomItem` (L120 nextInt(total); L164-194 subtract-select) -> `SetItemCountFunction.run`; RNG = `LegacyRandomSource` (LegacyRandomSource.setSeed/next + BitRandomSource.nextInt). The per-draw trace is in `testdata/golden_loot.json`. A vanilla 26.2 server's `/loot` at the same (table, seed) confirms the trace.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] ConstantValue.getInt corrected to Math.round (not Mth.floor)**
- **Found during:** Task 2 (decompiling ConstantValue / NumberProvider)
- **Issue:** 20-RESEARCH stated `ConstantValue.getInt = Mth.floor(value)`. The bytecode shows getInt is the NumberProvider DEFAULT = `Math.round(getFloat(ctx))` (invokestatic java/lang/Math.round); UniformGenerator overrides getInt with Mth.nextInt. Using Mth.floor would mis-round any future non-integer constant count.
- **Fix:** `ConstantValue.GetInt = mathRound(value)` (Math.round = floor(x+0.5f)); cited in provider.go.
- **Files modified:** level/loot/provider.go
- **Commit:** efb66ec0

**2. [Rule 2 - Missing critical] Entry-level condition path made real (abandoned_mineshaft location_check)**
- **Found during:** Task 1 (grepping the 5 chest groups for conditions)
- **Issue:** 20-RESEARCH Assumption A1 said the 5 chest groups use NO per-entry conditions. abandoned_mineshaft has exactly one: a `location_check` (biome predicate) on the music_disc_bounce entry. A no-op condition path would silently mis-roll that entry.
- **Fix:** `expand` evaluates the entry's `canRun` (compositeCondition); `location_check` is parsed faithfully with a cited permissive stub (returns true when the context Biome is unset — the pure path), to be wired to the real biome predicate in 20-02. The golden table (simple_dungeon) has no conditions, so this never affects the proof.
- **Files modified:** level/loot/condition.go, level/loot/roll.go
- **Commit:** 2e017c58 (model) / efb66ec0 (expand)

**3. [Rule 3 - Blocking] go-mc import path = fork path**
- **Found during:** Task 2 (first roll.go build)
- **Issue:** `github.com/Tnze/go-mc/net/packet` is not the fork's path; the vendored fork is `github.com/imhinotori/sulfur/net/packet`.
- **Fix:** corrected the pk import in roll.go.
- **Commit:** efb66ec0

### Test methodology note (not a plan deviation)
The weighted-select + empty-entry tests initially sampled `LegacyRandomSource(seed)`'s FIRST draw across sequential seeds, which is heavily correlated (java.util.Random's first output barely changes for small sequential seeds — first nextInt(4) only ever returned 2 or 3 over seeds 0..3999). Rewritten to roll many times from ONE warmed source (properly uniform). The engine was correct throughout (the draw-order test, which asserts the exact RNG sequence, passed from the start).

## Known Stubs

| Stub | File | Reason |
|------|------|--------|
| `EnchantWithLevelsFunction.Run` records `__enchant_with_levels_budget__` instead of the selected enchantments | level/loot/enchant.go | The full `EnchantmentHelper.enchantItem` cost-weighted selection (per-enchantment min/max cost + exclusivity sets over the registry) is a large subsystem not yet ported. The level-BUDGET draw IS faithful (consumes RNG correctly). Cited per CLAUDE.md; the value is never baked away. Used by exactly ONE chest entry (jungle_temple book, levels:30); the golden uses simple_dungeon (no enchant), so it is unaffected. A later plan replaces the body with the real selectEnchantment draw chain. |
| `locationCheck.Test` returns true when ctx.Biome is unset | level/loot/condition.go | The pure evaluator carries no world geometry; 20-02 populates ctx.Biome from the real chest-open LootParams. Permissive default keeps the entry eligible (vanilla default for the pure path). Only abandoned_mineshaft uses it; the golden is unaffected. |

## Verification Results
- `CGO_ENABLED=0 go build ./...` — exit 0 (no import cycle)
- `go vet ./level/loot/` — clean
- `go test ./level/loot/` — green (TestLootSeedReproduces, TestPoolDrawOrder, TestProvider*, TestWeightedSubtractSelect, TestSingleEligibleFastPath, TestEmptyEntry*, TestSetCount, TestEnchant*, TestLootParse*, TestRollsCap*)
- Docker `-race` (`golang:1.26`) over `./level/loot/` — green (2.06s)
- `git diff go.mod go.sum` — empty (no new deps)
- `grep import "C"` — none
- `server/block_drop.go` — NOT touched (last commit 17-14)

## Self-Check: PASSED
All 14 created files exist on disk; all 3 task commits (2e017c58, efb66ec0, 8b6486b8) are in git history.
