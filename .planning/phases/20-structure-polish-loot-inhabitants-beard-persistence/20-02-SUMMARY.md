---
phase: 20-structure-polish-loot-inhabitants-beard-persistence
plan: 02
subsystem: loot
tags: [loot-table, block-drops, chest-loot, block-entity, lazy-roll, vanilla-parity, cross-chunk-determinism]

# Dependency graph
requires:
  - phase: 20-structure-polish-loot-inhabitants-beard-persistence
    plan: 01
    provides: "level/loot.Roll(table, seed, ctx) — the shared evaluator + the LootTable/LootPool model (Conditions+Children shaped)"
  - phase: 17-block-drops
    provides: "server/block_drop.go spawnBlockDrop (the tick-owned drop spawn) + level/component.SlotData"
  - phase: 14-16-structures
    provides: "world/structure createChest + the temple/igloo/mineshaft/stronghold pieces"
provides:
  - "server/block_drop.go: blockDropsFor routes block-break drops through level/loot.Roll over minecraft:blocks/<name> (the v1 blockDropTable map is DELETED)"
  - "level/loot: the block-table delta — alternatives entry (first-passing child), match_tool/survives_explosion conditions, apply_bonus/explosion_decay functions"
  - "world/structure: createChest emits a chest BlockEntity carrying {LootTable, LootTableSeed} (seed = piece-RNG nextLong); WorldGenView.SetBlockEntity + Neighborhood impl"
  - "server/chest_loot.go: chestLoot.unpackLootTable — the lazy roll-on-first-open seam (ported RandomizableContainer.unpackLootTable)"
affects: [20-03, 20-04, 20-05, future-chest-open-UI-plan]

# Tech tracking
tech-stack:
  added: []  # NO new deps — pure stdlib + in-repo level/loot, nbt
  patterns:
    - "block-break drops + structure chests both flow through the SINGLE level/loot.Roll evaluator (the shared-evaluator mandate)"
    - "lazy chest loot: store {LootTable, LootTableSeed} at gen, roll on first open (vanilla unpackLootTable), never at gen (Pitfall 2)"
    - "UNCONDITIONAL RNG draw in createChest (before the box clip) to keep per-chunk re-run streams identical (Pitfall #2 cross-chunk determinism)"
    - "cited-stub LootContext tool/explosion fields (HasTool/ToolSilkTouch/ToolFortuneLevel/HasExplosion/ExplosionRadius) — faithful no-tool/no-explosion v1 defaults, structured to become real reads"

key-files:
  created:
    - level/loot/blockdelta.go (matchTool/explosionCondition/applyExplosionDecay/applyBonusCount — the block-only conditions+functions)
    - level/loot/blockdelta_test.go (TestBlockDropViaLootAlternatives + the per-element tests)
    - world/structure/chest_be_test.go (TestCreateChestEmitsBE + seed-determinism + no-items-at-gen)
    - server/chest_loot.go (chestLoot + unpackLootTable — the lazy roll seam)
    - server/chest_loot_test.go (TestChestLazyRoll + golden-match + no-table-opens-empty)
  modified:
    - server/block_drop.go (blockDropsFor -> level/loot.Roll; DELETE blockDropTable; spawnBlockDrop spawns one Item per stack)
    - server/block_drop_test.go (TestBlockDropLookup -> TestBlockDropViaLoot)
    - level/loot/condition.go (parseCondition + match_tool/survives_explosion)
    - level/loot/function.go (parseFunction + apply_bonus/explosion_decay; parseApplyBonus)
    - level/loot/roll.go (expand handles the alternatives composite — first-passing child wins)
    - level/loot/context.go (LootContext tool/explosion cited-stub fields)
    - world/structure/piece.go (WorldGenView.SetBlockEntity; createChest draws nextLong + emits BE; LootChest +LootTableSeed)
    - world/structure/igloo.go + jungle_temple.go + mineshaft_pieces.go + stronghold_pieces.go + desert_pyramid.go (thread rng into createChest)
    - world/neighborhood.go (SetBlockEntity writes the chest BE into the chunk BlockEntity list + chestLootNBT)
    - world/structure/{piece,jungle_temple,acceptance,phase16_acceptance}_test.go (mapView.SetBlockEntity; jungle_temple fingerprint RE-SEAL)

key-decisions:
  - "Block-table delta lives in level/loot (data-only model was already shaped by 20-01); the no-tool/no-explosion v1 defaults are jar-faithful (match_tool TOOL==null->false; survives_explosion EXPLOSION_RADIUS==null->true; apply_bonus/explosion_decay no-op without their param)"
  - "createChest's nextLong() draw is UNCONDITIONAL (before the box.IsInside clip) because Sulfur re-runs PostProcess per overlapping chunk with a re-seeded rng — a conditional draw desyncs the streams (caught by TestMineshaftCrossChunk). Vanilla writes the whole structure in one WorldGenLevel pass so it can early-return; Sulfur's per-chunk model requires draw-before-clip."
  - "Chest loot stored as {LootTable, LootTableSeed} NBT (keys LootTable/LootTableSeed from RandomizableContainer.LOOT_TABLE_TAG/LOOT_TABLE_SEED_TAG), rolled LAZILY on first open — vanilla unpackLootTable, never at gen (Pitfall 2)"
  - "W2 SPLIT: the full chest-OPEN UI is net-new and large (no runtime BE resolution, no windowId allocator, no OpenScreen usage, no non-player container menu today) — SPLIT to a follow-up per the plan's explicit Case-B ceiling; this plan ships the gen-time store + the unpackLootTable seam"

requirements-completed: []  # STRUCT-POLISH-01 spans 20-01+20-02; the chest-OPEN UI is split to a follow-up, so the requirement is not fully closed here

# Metrics
duration: 25min
completed: 2026-06-27
---

# Phase 20 Plan 02: Shared Evaluator -> Block Drops + Lazy Chest Loot Summary

**Block-break drops and structure chests both now flow through the single `level/loot.Roll` evaluator: the v1 hardcoded `blockDropTable` map is DELETED and replaced by a roll over the embedded `minecraft:blocks/<name>` tables (with the block-table delta ported), and `createChest` emits a chest BlockEntity carrying `{LootTable, LootTableSeed}` that rolls LAZILY on first open via a ported `unpackLootTable`.**

## Performance
- **Duration:** 25 min (concurrent with 20-05 on disjoint files)
- **Started:** 2026-06-27T04:23:16Z
- **Completed:** 2026-06-27T04:49:04Z
- **Tasks:** 3/3
- **Files:** 5 created + 14 modified

## Task Commits
1. **Task 1: rewire block drops onto the shared evaluator + block-table delta** — `4d935460` (feat)
2. **Task 2: createChest emits a {LootTable, LootTableSeed} BlockEntity** — `6dbbb9a9` (feat)
3. **Task 3: lazy roll on chest open (unpackLootTable)** — `8432a68b` (feat)

_TDD: each task wrote a failing test (RED) then the implementation (GREEN). 20-05 committed `f9cb7ba3` interleaved (concurrent wave, disjoint files)._

## Accomplishments
- **Block drops route through the shared evaluator.** `blockDropsFor(state, seed)` calls `loot.Roll(loot.LoadTable("minecraft:blocks/"+name), seed, ctx)`; `spawnBlockDrop` spawns one Item entity per rolled stack. The v1 `blockDropTable` map is GONE (grep count 0). stone->cobblestone, grass_block->dirt, oak_log->oak_log, diamond_ore->diamond, all via the tables.
- **The block-table delta is ported jar-faithfully.** The `alternatives` entry (CompositeEntryBase.expand OR — first-passing child wins), `match_tool` (TOOL==null->false), `survives_explosion` (EXPLOSION_RADIUS==null->true), `apply_bonus` (ore_drops fortune, TOOL==null->no-op), `explosion_decay` (EXPLOSION_RADIUS==null->no-op). The v1 hand-break defaults (no tool, no explosion) drop the non-silk-touch alternative at base count — exactly vanilla.
- **Chests store table+seed and roll lazily.** `createChest` draws `rng.NextLong()` (the piece-RNG, the determinism keystone) and emits a chest `level.BlockEntity` with NBT `{LootTable, LootTableSeed}`. `chestLoot.unpackLootTable` rolls ONCE on first open through `loot.Roll` at the server-stored seed, then clears the table (no re-roll). The rolled contents match 20-01's `simple_dungeon@123456789` golden.
- **Cross-chunk determinism preserved.** The chest `nextLong()` draw is UNCONDITIONAL (before the box clip) so the per-chunk re-run RNG streams stay byte-identical — `TestMineshaftCrossChunk` + `TestPhase15Acceptance` green.

## Open Question 4 (chest-open path) — RESOLVED via W2 SPLIT

The plan's Task 3 had Case A (hang `unpackLootTable` on an existing open seam) vs Case B (wire the minimal open seam), with a W2 split note if Case B balloons. **Finding:** Sulfur has NO block-entity chest-open path today — no runtime block-entity resolution from a world position, no `windowId` allocator, no `ClientboundOpenScreen` usage, and no non-player container menu (the ENT-04 `InventoryMenu` is the PLAYER inventory only; `useBlockInteraction` is a structural no-op stub). Wiring the full open seam (UseItemOn a chest -> resolve the chest BE -> windowId -> OpenScreen + a chest container menu backed by the BE -> ContainerSetContent slot sync -> close) is a NET-NEW interaction subsystem far larger than "a menu hookup".

**Per the plan's explicit W2 instruction, the full chest-OPEN UI is SPLIT to a follow-up plan.** This plan ships the two halves it owns: the gen-time store (Task 2) + the pure, tick-side, fully-tested `unpackLootTable` roll seam (Task 3) that the future open path will call. Scope did not balloon.

## Capture-diff Re-seal

- **Chunk-wire goldens: UNCHANGED, stay green.** Adding chest BlockEntities touches the chunk's `BlockEntity` list, but the wire ENCODER (`level/chunk.go` WriteTo) was NOT touched — the chest BE rides the existing `level.BlockEntity` struct + `PackXZ` + the existing list encoder. `level/chunk_capture_test.go` (the chunk-wire capture-diff) passes; `level/chunk.go` git diff is empty. The full `world` integration suite (242s, generates chunks with structures incl. chest BEs) is green. **No chunk-wire re-seal needed.**
- **jungle_temple structure fingerprint: RE-SEALED** `0xf50b9fb8be352499 -> 0x471f867df2e73b40` (block COUNT unchanged at 1746). Reason: `createChest` now draws `rng.NextLong()`, and `JungleTemplePiece.postProcess` calls `createChest` BEFORE 3 more `generateBox(STONE_SELECTOR)` boxes (jar-verified bytecode sequence: generateBox, createChest, generateBox x3, createChest), so the chest's nextLong shifts those MossStoneSelector nextFloat draws — some cobble<->mossy_cobble cells flip. The NEW hash is the FAITHFUL value (the old no-draw hash was unfaithful to vanilla, which does draw nextLong here). Re-pinned in `jungle_temple_test.go`, `acceptance_test.go`, `phase16_acceptance_test.go`.

## Chest-BE NBT key names used
- `LootTable` (TagString) — the loot-table id, e.g. `"minecraft:chests/simple_dungeon"`
- `LootTableSeed` (TagLong) — the piece-RNG `nextLong()` draw

These mirror `RandomizableContainer.LOOT_TABLE_TAG` / `LOOT_TABLE_SEED_TAG` from the jar (verified via `javap` — `tryLoadLootTable`/`trySaveLootTable` read/write exactly these keys).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] createChest nextLong draw made UNCONDITIONAL (cross-chunk determinism)**
- **Found during:** Task 2 (TestMineshaftCrossChunk + TestPhase15Acceptance failed: "union diverges 15293 vs 2247")
- **Issue:** The first createChest implementation drew `rng.NextLong()` AFTER the `box.IsInside` guard (mirroring vanilla's early-return-before-draw). But Sulfur re-runs each piece's PostProcess once per overlapping chunk with a re-seeded rng (place.go placeInChunk); a chest in chunk A but not chunk B then drew nextLong in A's pass but not B's, desyncing every subsequent draw between the two passes -> cross-chunk block divergence.
- **Fix:** Draw nextLong UNCONDITIONALLY, before the box clip (like maybeGenerateBlock draws nextFloat before placeBlock clips). The block + BE writes are still clipped. This is the Sulfur-faithful equivalent of vanilla's single-pass WorldGenLevel write.
- **Files modified:** world/structure/piece.go (createChest), world/structure/igloo.go (the direct chest-emit path)
- **Commit:** 6dbbb9a9

**2. [Rule 1 - Bug] jungle_temple fingerprint re-seal (jar-faithful nextLong draw)**
- **Found during:** Task 2 (TestJungleTemplePlaces + 2 acceptance tests failed on the pinned hash)
- **Issue:** The pinned jungle_temple fingerprint was captured WITHOUT a chest nextLong draw (loot was deferred). Adding the faithful draw shifts the downstream MossStoneSelector draws.
- **Fix:** Verified against the jar bytecode that createChest precedes 3 generateBox(STONE_SELECTOR) boxes, confirming the draw SHOULD shift them; re-pinned the hash to the faithful value (count unchanged at 1746). Documented the re-seal inline.
- **Files modified:** world/structure/{jungle_temple,acceptance,phase16_acceptance}_test.go
- **Commit:** 6dbbb9a9

## Verification Results
- `CGO_ENABLED=0 go build ./...` — exit 0
- `go vet ./server/ ./world/structure/ ./world/ ./level/loot/` — clean
- `go test ./server/ ./world/structure/ ./level/loot/` — green (TestBlockDropViaLoot, TestBlockDropViaLootAlternatives, TestCreateChestEmitsBE, TestChestLazyRoll, TestChestLazyRollMatchesGolden, TestChestNoTableOpensEmpty, + the full existing suites incl. the re-sealed jungle_temple)
- `go test ./world/` — green (242s, the worldgen integration suite incl. structures with chest BEs)
- `go test ./level/` — green (chunk-wire capture-diff goldens stay green)
- Docker `-race` (golang:1.26) over `./server/ ./world/structure/ ./level/loot/` — green (server 11s, structure 36s, loot 2s)
- `grep -v '^//' server/block_drop.go | grep -c blockDropTable` — 0 (the v1 map is DELETED)
- `git diff go.mod go.sum` — empty (no new deps)
- `grep import "C"` — none
- Only my files touched: block_drop.go, chest_loot.go, piece.go + the level/loot delta + the createChest callers + neighborhood.go — NOT 20-05's beard/fill/noisegen files (verified at each commit's staging)

## Known Stubs
| Stub | File | Reason |
|------|------|--------|
| `chestLoot.unpackLootTable` has no chest-OPEN caller yet | server/chest_loot.go | The full block-entity chest-open UI (runtime BE resolution + windowId + OpenScreen + container menu + slot sync) is net-new and large; SPLIT to a follow-up plan per the plan's W2 instruction. The seam itself is ported + fully tested, ready to hang on the open path. NOT a masquerading placeholder — it is the real lazy-roll function. |
| LootContext `HasTool`/`ToolSilkTouch`/`ToolFortuneLevel`/`HasExplosion`/`ExplosionRadius` default to false/0 | level/loot/context.go | The v1 block-break path supplies no held tool / no explosion, so the block tables take their jar-faithful no-tool/no-explosion branches (cobblestone over the silk-touch alternative, base count). Cited per CLAUDE.md; structured to become real reads when the dig path threads the held tool / the explosion path supplies a radius. Never baked away. |

## Self-Check: PASSED
All 5 created files exist on disk; all 3 task commits (4d935460, 6dbbb9a9, 8432a68b) are in git history.
