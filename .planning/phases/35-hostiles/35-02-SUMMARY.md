---
phase: 35-hostiles
plan: 02
subsystem: spawn-gating
tags: [natural-spawner, mob-category, monster-cap, day-night-gate, light-proxy, hostiles]

# Dependency graph
requires:
  - phase: 34-new-passive-mobs
    provides: "the categoryOf additive-case pattern + countByCategory/creatureCap + the 4-CREATURE pickNaturalCreatureMob/naturalCreatureMobNames machinery + the OPT-03 off-tick spawn scan + apply-time cap re-check"
  - phase: 27-folia-regionization
    provides: "countByCategoryAcrossRegions + the per-region spawnScanPending gate + the quiescent pre-fan-out snapshot discipline (spawnLiveCreatureSnapshot)"
provides:
  - "categoryOf -> categoryMonster for zombie/skeleton/spider (the MONSTER spawn budget)"
  - "monsterCap (70 * spawnableChunkCount / 289) + categorySpawnCap dispatch"
  - "isDarkEnoughToSpawn — the FORCED gametime-darkness day/night proxy (SC#4)"
  - "the night-gated + cap-gated natural-spawn MONSTER pass (submitSpawnScanFor categoryMonster)"
  - "pickNaturalMonsterMob + the explicit naturalMonsterMobNames {zombie,skeleton,spider}"
  - "the category-carrying spawnCandidatesReady + the cross-region monster apply-time re-check"
affects: [35-03, 35-04, 35-05, hostile-plugin-embeds, lighting-engine, mob-despawn]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "per-category natural-spawn passes via a category-parameterized submitSpawnScanFor (CREATURE then night-gated MONSTER), single-in-flight gate admits one scan/cycle"
    - "the FORCED light-gate proxy: a clearly-cited stub over t.gametime that EQUALS the vanilla night default and is structured to become a real sky-light read"
    - "category carried on the async spawn message so applyTo re-checks the RIGHT per-category cap + picks from the RIGHT species list"

key-files:
  created:
    - server/spawner_monster_test.go
  modified:
    - server/mob_category.go
    - server/spawner.go
    - server/async.go
    - server/tick.go
    - server/region_coordinator.go
    - server/vanilla_pig_embed.go
    - server/spawner_test.go

key-decisions:
  - "Shipped the FORCED gametime-darkness proxy (35-CONTEXT SC#4) — gate hostile spawns on t.gametime % 24000 night window (13000..23000), NOT a real light read (no light engine exists). Cited Monster.isDarkEnoughToSpawn; structured to become a real sky-light read."
  - "Mob.checkDespawn is DEFERRED (no noActionTime / per-mob nearest-player despawn / persistenceRequired subsystems exist). The cap re-check already bounds SPAWNS — the anti-flood — so the world cannot exceed the MONSTER cap. Recorded in code + here, not faked."
  - "Hostile mob-name constants (vanillaZombieMobName etc.) defined in vanilla_pig_embed.go as the canonical single home so the picker (this plan) and the later hostile-plugin embed plans share ONE definition (no duplicate const)."
  - "Carried a `category mobCategory` field on spawnCandidatesReady so the MONSTER pass re-checks monsterCap/countByCategoryAcrossRegions()[categoryMonster] cross-region, not the CREATURE budget."

patterns-established:
  - "submitSpawnScanFor(cat, ...): the category-parameterized core of naturalSpawn — cap gate + roll + snapshot + submit, category-agnostic except cap/live-count/carried-category."
  - "spawnLiveMonsterSnapshot: the race-free pre-fan-out global MONSTER count, mirroring spawnLiveCreatureSnapshot, read-only during the parallel fan-out."

requirements-completed: [MOB-SUB-11]

# Metrics
duration: 35min
completed: 2026-06-30
---

# Phase 35 Plan 02: Hostile Spawn Gating Summary

**Hostiles (zombie/skeleton/spider) now spawn only at night and only under a 70/289 MONSTER cap — via the FORCED gametime-darkness light-gate proxy and a category-parameterized natural-spawn MONSTER pass — pure-additive over the CREATURE machinery, with the pig oracle byte-identical.**

## Performance

- **Duration:** ~35 min
- **Tasks:** 2/2 (both `tdd="true"`)
- **Files modified:** 7 (+1 created)

## Accomplishments

### Task 1 — categoryOf MONSTER cases + monsterCap + the isDarkEnoughToSpawn proxy
- `mob_category.go`: added the 3 MONSTER `categoryOf` cases (zombie/skeleton/spider), each jar-cited (`data/entity` `Type == "monster"`, `EntityType.<Mob> = MobCategory.MONSTER`). `maxInstancesPerChunk` already returned 70 for MONSTER — no cap-table change. `countByCategory` / `countByCategoryAcrossRegions` tally MONSTER with **zero counting-code change** once `categoryOf` returns `categoryMonster`.
- `spawner.go`: `monsterCap(n) = 70 * n / 289` (the `/289` MAGIC_NUMBER divisor KEPT, only the per-category 70-vs-10 differs) + `categorySpawnCap` dispatch.
- `spawner.go`: **`isDarkEnoughToSpawn`** — THE FORCED PROXY (SC#4). Gates on `t.gametime % 24000` in the night window `[13000, 23000)`, behind a loudly-documented stub citing `Monster.isDarkEnoughToSpawn`. NEVER a silent daylight flood, NEVER a silent missing gate. Structured so the gametime read swaps for `level.getBrightness(SKY, pos)` + the `nextInt(32)` sample when the lighting engine lands.

### Task 2 — the natural-spawn MONSTER pass + picker + apply-time cross-region re-check
- `spawner.go`: `naturalSpawn` refactored into two faithful passes via `submitSpawnScanFor(cat, ...)`: the CREATURE pass, then the **night-gated** MONSTER pass (`if t.isDarkEnoughToSpawn()`). The single-in-flight gate admits one scan/cycle; the passes alternate naturally. `findStandableY` ON_GROUND check + the snapshot/submit machinery reused verbatim (category-agnostic).
- `async.go`: `pickNaturalMonsterMob` + the explicit `naturalMonsterMobNames` {`vanilla_zombie`, `vanilla_skeleton`, `vanilla_spider`} (its own list, never dragging a non-MONSTER bundled mob in). `pickNaturalSpawnMob(cat)` dispatch.
- `async.go`: `spawnCandidatesReady` now carries `category`; `applyTo` re-checks `categorySpawnCap(r.category, ...)` against `countByCategoryAcrossRegions()[r.category]` (the cross-region MONSTER cap re-check, T-35-04 mitigation) and picks from the matching species list. The `mobNear` packing guard + `spawnVanillaMob` placement reused verbatim.
- `tick.go` / `region_coordinator.go`: `spawnLiveMonsterSnapshot` — the race-free pre-fan-out global MONSTER count for the in-fan-out pre-submit gate (mirrors `spawnLiveCreatureSnapshot`; one quiescent tally feeds both).
- `spawner_monster_test.go` (NEW): `TestCategoryOfMonster`, `TestMonsterCap`, `TestIsDarkEnoughToSpawn`, `TestMonsterPassDaytimeNoSpawn`, `TestMonsterCapBlocks`, `TestPickNaturalMonsterMob`.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0 (with the parallel 35-01 in-progress files moved aside; see Deviations).
- `CGO_ENABLED=0 go vet ./server/` — clean.
- `CGO_ENABLED=0 go test ./server/` (full suite) — PASS (12.6s).
- Targeted: `TestPluginPigEqualsGoNativePig` **byte-identical PASS** (the pig oracle untouched), plus all 6 new MONSTER tests PASS and the full existing spawner/async-spawn suite (`TestSpawn*`/`TestAsyncSpawn*`/`TestTickAI*`) PASS — no regression from the `naturalSpawn` two-pass refactor or the `category` message field.
- `-race`: not runnable in this environment (no gcc/cgo for the race detector). The concurrency design is unchanged from the proven CREATURE path — only an immutable `category` value rides the async message and a second snapshot field is written at the same quiescent coordinator point and read-only during fan-out (identical discipline to `spawnLiveCreatureSnapshot`); no new shared-mutable state crosses the async boundary.

## Deviations from Plan

### Auto-fixed / structural (Rules 2-3)

**1. [Rule 3 - Blocking] Hostile mob-name constants added to `vanilla_pig_embed.go`**
- **Found during:** Task 2 (the picker needs `vanillaZombieMobName` etc., which did not exist — the hostile plugin embeds are a sibling/later plan, 35-03..05).
- **Fix:** Defined `vanillaZombieMobName` / `vanillaSkeletonMobName` / `vanillaSpiderMobName` in the canonical mob-name const block in `vanilla_pig_embed.go` (not in this plan's `files_modified`, but the natural single home), documented so the later embed plans only EXTEND the embed directive + `vanillaMobNames` load order — never redefine the constants (avoids a duplicate-const collision across parallel/later plans).
- **Files:** server/vanilla_pig_embed.go
- **Commit:** 7f00d4c2

**2. [Rule 3 - Blocking] `spawnLiveMonsterSnapshot` added (tick.go + region_coordinator.go)**
- **Found during:** Task 2. The Phase-27 in-fan-out pre-submit gate only snapshotted the CREATURE count; the MONSTER pass needs its own race-free global count.
- **Fix:** Added `spawnLiveMonsterSnapshot`, set from the same single quiescent `countByCategoryAcrossRegions()` tally as the CREATURE snapshot (one pass, both keys). Same single-owner/read-only-during-fan-out discipline.
- **Files:** server/tick.go, server/region_coordinator.go
- **Commit:** 7f00d4c2

**3. [Scope boundary] Parallel 35-01 in-progress files left untouched**
- A concurrent 35-01 executor (AI/combat, disjoint scope) wrote partial `server/ai_goals_attack.go` / `server/ai_goals_target.go` into the shared working tree mid-run (they reference not-yet-defined `mathSqrt` / `broadcastMobSwing` / `damageSourceMobAttack`). Per the SCOPE BOUNDARY these are NOT my changes — I did NOT fix them, did NOT stage them (committed only my 8 files by name), and verified my code builds/tests green with those incomplete files moved aside, then restored them. `go build ./...` will be clean once 35-01 finishes its files.

## Known Stubs

**isDarkEnoughToSpawn — the FORCED gametime-darkness proxy (intentional, SC#4).**
- **File/site:** `server/spawner.go` `isDarkEnoughToSpawn` (gametime window over `t.gametime % 24000`).
- **Why:** No light-propagation engine exists (v1 is flat-stone superflat — `node_evaluator.go:27` / `path_region.go:29`), so the real `Monster.isDarkEnoughToSpawn` SKY/BLOCK-brightness read is unavailable.
- **Resolution:** Becomes a real `level.getBrightness(SKY, pos)` + `nextInt(32)` sample read when the lighting engine lands. It EQUALS the vanilla night default today (hostiles spawn dusk→dawn) and hard-blocks daytime hostile spawns — never a silent flood.

## Deferred (recorded, to land with future subsystems)

- **Full light-propagation engine + block-light/cave hostile spawning** + the light-test RNG: the spawn-attempt `nextInt(32)` SKY sample + the `monsterSpawnLightTest` `UniformInt(0,7).sample(random)` draw (35-JARNOTES.md:179-183). These are off the per-mob RNG stream (spawn-attempt RNG), so the deferral does not perturb any mob's lockstep draw order.
- **`Mob.checkDespawn`** (SC#3 complement): needs a `noActionTime` counter, the `getNearestPlayer` distance-bucket despawn loop, the `nextInt(800)` soft-despawn roll, and `persistenceRequired`/`requiresCustomPersistence` guards — none exist yet. Recorded in code (`spawner.go` `naturalSpawn` doc) + here. The cap re-check already bounds spawns (the anti-flood); `checkDespawn` is the future culling complement.

## Threat Surface

No new trust boundaries beyond the plan's register. T-35-04 (DoS via the MONSTER pass) is mitigated as designed — `monsterCap` (70/289) bounds the global count + the apply-time cross-region `countByCategoryAcrossRegions()[categoryMonster]` re-check closes the snapshot-staleness window. T-35-05 (the proxy reads only host-owned `t.gametime`, not client/plugin-controllable) accepted as planned.

## TDD Gate Compliance

Both tasks are `tdd="true"`. Development followed RED (the new test symbols written first, confirmed failing-to-compile — `undefined: monsterCap` — the per-symbol-port RED), then GREEN (the production code, tests passing). The interdependent `naturalSpawn` two-pass refactor + the `category`-carrying message couple the two tasks through one cohesive change, so the work landed in a single atomic commit (7f00d4c2) rather than separate `test`/`feat` commits — each intermediate split would have broken the build mid-history. The RED→GREEN discipline was followed in development; the commit captures the GREEN feature.

## Self-Check: PASSED

- Created files verified present + tracked: `server/spawner_monster_test.go` (in commit 7f00d4c2), `.planning/phases/35-hostiles/35-02-SUMMARY.md`.
- Commit verified: `7f00d4c2` exists, contains all 8 of my files (mob_category.go, spawner.go, async.go, tick.go, region_coordinator.go, vanilla_pig_embed.go, spawner_test.go, spawner_monster_test.go), zero deletions, no foreign 35-01 files swept in.
