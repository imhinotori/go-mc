---
phase: 34-new-passive-mobs
plan: 04
subsystem: mobs
tags: [vanilla-mobs, plugin-boot-load, natural-spawn, starlark, mob-registry, pig-oracle, race-clean]

# Dependency graph
requires:
  - phase: 34-00
    provides: shared mob infra (parameterized food handle, eat seam, DATA_WOOL/shear, chicken aiStep host hook, spawnDeclaredMob)
  - phase: 34-01
    provides: vanilla_cow plugin + Cow.mobInteract milking
  - phase: 34-02
    provides: vanilla_sheep plugin + EatBlockGoal + shear/wool regrow
  - phase: 34-03
    provides: vanilla_chicken plugin + aiStep egg-lay + slow-fall
  - phase: 24-02
    provides: the pig boot-load + spawnVanillaPig + mobRegistry + the pig oracle gate (TestPluginPigEqualsGoNativePig)
provides:
  - "loadVanillaMobRegistry: ONE boot-load that materializes + loads all 4 vanilla mob embeds (pig/cow/sheep/chicken) into one tick-owned registry under per-mob least-privilege caps, failing loudly on any missing declaration"
  - "spawnVanillaMob(name, x, y, z): the name-parameterized generic spawn lever; spawnVanillaPig is now a thin wrapper over it (pig oracle call sites byte-identical)"
  - "/dbg cow|sheep|chicken: operator spawn levers at the player"
  - "pickNaturalCreatureMob: uniform pick among the 4 CREATURE mobs for natural spawning, drawn from the OWNING region's SEEDED levelRandom (race-clean, not the unseeded global rand)"
affects: [future-mobs, mob-spawning, plugin-dogfood]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-mob boot-load loop: ONE registry, setLoadCaps per-mob before each LoadDirWith (caps are per-load), loud per-mob post-load assertion (T-34-10)"
    - "Seeded per-region pick: natural-spawn mob choice reads cur().levelRandom.NextIntN inside withRegion — deterministic + race-clean, avoiding the unseeded-global-rand flake class (T-34-11)"
    - "Thin-wrapper preservation: generalize a hardcoded helper (spawnVanillaPig) into a parameterized one (spawnVanillaMob) while keeping the original as a wrapper so a bit-exact oracle stays untouched (T-34-13)"

key-files:
  created:
    - server/vanilla_mob_test.go
  modified:
    - server/vanilla_pig_embed.go
    - server/vanilla_pig.go
    - cmd/sulfur/main.go
    - server/commands_dbg.go
    - server/async.go
    - server/spawner_test.go
    - server/async_stress_test.go
    - server/sheep_test.go
    - server/chicken_test.go

key-decisions:
  - "Natural-spawn mob pick is UNIFORM among the 4 CREATURE mobs for v1; per-biome MobSpawnSettings spawn WEIGHTS are a cited deferral (no biome-weight subsystem yet)"
  - "The pick draws from the owning region's seeded levelRandom (NOT a new TickLoop field, NOT the unseeded global rand.IntN) — matches the existing per-region RNG discipline and is race-clean inside withRegion"
  - "loadVanillaPigRegistry kept as a thin alias over loadVanillaMobRegistry so all existing test harnesses stay byte-identical and the pig oracle is untouched"

patterns-established:
  - "Boot-load generalization: pig-hardcoded loader -> per-mob loop into one shared registry with per-mob caps"
  - "Spawn-lever generalization: spawnVanillaPig -> spawnVanillaMob(name) + thin wrapper"

requirements-completed: [MOB-PASS-01, MOB-PASS-02, MOB-PASS-03]

# Metrics
duration: ~35min
completed: 2026-06-30
---

# Phase 34 Plan 04: The Gate — All Four Vanilla Mobs Live Summary

**Generalized the pig-hardcoded boot-load + spawn into a 4-mob registry (loadVanillaMobRegistry / spawnVanillaMob(name)), wired /dbg cow|sheep|chicken + a seeded natural-spawn pick among the 4 CREATURE mobs, and kept the pig oracle byte-identical green through the generalization.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-30 (Wave 3, THE GATE)
- **Completed:** 2026-06-30
- **Tasks:** 2 auto tasks executed (Task 3 is a human-verify checkpoint — deferred for bot-verification; see Deferred Verification)
- **Files modified:** 9 (1 created)

## Accomplishments
- `loadVanillaMobRegistry` materializes + loads ALL FOUR vanilla mob embeds (pig/cow/sheep/chicken) into ONE tick-owned registry, stamping each mob's own least-privilege caps before its load and asserting EACH name is present after load (loud failure on a missing declaration, T-34-10).
- `spawnVanillaMob(name, x, y, z)` is the generic spawn lever; `spawnVanillaPig` is now a thin wrapper over it, so the pig oracle call sites (`spawnVanillaPigWithID`) are byte-identical and `TestPluginPigEqualsGoNativePig` stays GREEN (T-34-13).
- `/dbg cow|sheep|chicken` spawn the right mob at the player; main.go boot-loads all 4 (fatal on any missing).
- Natural creature spawning now picks among the 4 CREATURE mobs via `pickNaturalCreatureMob`, drawn from the owning region's SEEDED `levelRandom` — race-clean and deterministic, avoiding the unseeded-global-rand flake class (T-34-11). The cap math (`creatureCap`, `categoryCreature`) is unchanged (all 4 are CREATURE).
- New gate coverage: `TestAllFourMobsBootLoad`, `TestSpawnVanillaMobByName`, `TestPigOracleStillRoutesThroughDecl`, `TestNaturalSpawnPicksAmongFour`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Generalize boot-load (all 4 embeds) + spawnVanillaMob(name) + main.go + /dbg** - `35102074` (feat)
2. **Task 2: Natural-spawn mob pick + the gate verification (pig oracle + all mobs + Docker -race)** - `e39e7a3c` (feat)

_Task 3 (checkpoint:human-verify) is a live in-game bot pass — deferred for orchestrator bot-verification (USER AFK; see Deferred Verification)._

## Files Created/Modified
- `server/vanilla_pig_embed.go` - generalized to `loadVanillaMobRegistry` (per-mob loop, embed glob over all 4 dirs, per-mob caps, loud per-mob assertion); name constants + `vanillaMobNames`
- `server/vanilla_pig.go` - `LoadVanillaMobRegistry` export + `loadVanillaPigRegistry` alias; `spawnVanillaMob(name,...)` generic spawn; `spawnVanillaPig` thin wrapper; `spawnVanillaPigWithID` UNTOUCHED
- `cmd/sulfur/main.go` - boot calls `LoadVanillaMobRegistry`; updated log line
- `server/commands_dbg.go` - `/dbg cow|sheep|chicken` cases + usage string
- `server/async.go` - natural-spawn `applyTo` picks via `pickNaturalCreatureMob` (seeded per-region source) + `spawnVanillaMob`; `naturalCreatureMobNames` slice
- `server/vanilla_mob_test.go` (NEW) - the 4-mob boot-load/spawn/pick gate tests
- `server/spawner_test.go` - `findAnyNaturalCreature` helper; spawn assertions accept any of the 4 CREATURE mobs
- `server/async_stress_test.go` - `TestBehaviorRegressionMobSpawns` accepts any CREATURE mob
- `server/sheep_test.go`, `server/chicken_test.go` - dropped now-redundant test-local mob-name consts (the package consts moved to vanilla_pig_embed.go)

## Decisions Made
- **Uniform pick for v1, biome weights deferred:** `pickNaturalCreatureMob` chooses uniformly among the 4 CREATURE mobs. Vanilla's per-biome `MobSpawnSettings` spawn weights are a cited deferral (no biome-weight subsystem exists yet). Observably, any of the 4 overworld passives spawn under the existing CREATURE cap.
- **Seeded per-region source for the pick:** the draw reads `cur().levelRandom.NextIntN` inside `withRegion` rather than adding a new `*rand.Rand` field or using the unseeded global `rand.IntN`. This matches the established per-region RNG discipline (race-clean, deterministic per region seed) and directly addresses T-34-11.
- **`loadVanillaPigRegistry` kept as a thin alias:** so the existing test harnesses (`installVanillaPigRegistry`, the pig oracle, cow_test) stay byte-identical; the returned 4-mob registry holds an identical pig declaration, so the pig path is unperturbed.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Removed duplicate test-local mob-name consts**
- **Found during:** Task 1 (boot-load generalization)
- **Issue:** Adding the package-level `vanillaCowMobName`/`vanillaSheepMobName`/`vanillaChickenMobName` consts (needed by the spawn levers) collided with test-local `const` declarations in `sheep_test.go` and `chicken_test.go` (added in waves 1-2) — `redeclared in this block`, blocking `go vet`/compile.
- **Fix:** Removed the two redundant test-local consts; the package consts provide the identical values.
- **Files modified:** server/sheep_test.go, server/chicken_test.go
- **Verification:** `go vet ./server/` clean; full suite green.
- **Committed in:** `35102074` (Task 1 commit)

**2. [Rule 1 - Bug] Generalized spawner test assertions from pig-specific to any-CREATURE**
- **Found during:** Task 2 (natural-spawn generalization)
- **Issue:** Four tests (`TestSpawnPlacementOnGround`, `TestSpawnAddsToStore`, `TestTickAISpawns`, `TestAsyncSpawnRejoinsAndAdds`, `TestBehaviorRegressionMobSpawns`) asserted the natural spawner produces a **Pig** specifically. After generalizing the pick to the 4 CREATURE mobs, those assertions are wrong: a spawn may now be a cow/sheep/chicken, making the tests fail (or pass only by RNG luck — a latent flake).
- **Fix:** Added a `findAnyNaturalCreature(loop)` helper and routed the placement/store/AI assertions through it. The properties under test (ON_GROUND placement, fresh id, real mobAI, bucketed for `near()`) are CREATURE-mob-agnostic, so the tests stay meaningful while accepting any of the 4.
- **Files modified:** server/spawner_test.go, server/async_stress_test.go
- **Verification:** Full suite green across 3 reruns + Docker -race final line `ok`.
- **Committed in:** `e39e7a3c` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 blocking, 1 bug)
**Impact on plan:** Both auto-fixes were necessary consequences of the planned generalization (a new package const colliding with stale test consts; pig-specific assertions on a now-multi-mob spawner). No scope creep — no gameplay logic changed; the generalization is the planned infra work.

## Issues Encountered
- **Boot smoke test — "skipping ... undefined: declare_mob" log lines:** the general plugin host scan of `plugins/` cannot resolve the server-injected `declare_mob` builtin (it is injected only by the dedicated `LoadVanillaMobRegistry` boot-load via `LoadDirWith` with the `extra` builtins). These skip lines are EXPECTED and pre-existing (the pig had them before this plan); the authoritative line is `vanilla mobs: bundled 1:1 pig/cow/sheep/chicken plugins boot-loaded`, which confirmed all 4 captured (the boot would `log.Fatalf` on a missing mob). Not a regression.

## Verification (the gate)
- `CGO_ENABLED=0 go build ./...` exit 0; `go vet ./server/ ./cmd/...` clean.
- `CGO_ENABLED=0 go test ./server/ -count=1` — full suite `ok` (3 consecutive reruns, no flake).
- THE PIG ORACLE: `TestPluginPigEqualsGoNativePig` — `ok` (byte-identical; the generalization did not perturb the pig path).
- All per-mob + new tests green (cow/sheep/chicken/eatblock/milk/egglay/slowfall/shear + the 4 new gate tests).
- Docker -race (verbatim 33-05 command): final line `ok github.com/imhinotori/sulfur/server` — no real data race. (`TestRegionPanicIsolated`'s recovered-panic stack is the known false alarm; the final ok line is the judge.)
- All 4 embed pairs (cow/sheep/chicken/pig) byte-identical (`diff` empty for both main.star + plugin.toml).
- Headless boot smoke: the server binary builds and boots; the 4-mob boot-load log line confirms `LoadVanillaMobRegistry` loaded all 4 (no fatal).

## Deferred Verification (Task 3 — human-verify checkpoint)
Task 3 is a `checkpoint:human-verify` requiring interactive in-game observation (connect a bot, `/dbg cow|sheep|chicken`, milk a cow with a bucket, shear a sheep + watch wool regrow on grass-eat, drop a chicken to watch slow-fall + wait for an egg, confirm the pig is unchanged). The USER is AFK, so this LIVE-bot pass is deferred to the orchestrator for bot-verification. All headless prerequisites are satisfied: the server builds + boots, all 4 mobs boot-load, the /dbg levers compile + dispatch, and every automated behavior test (milk/shear/wool/egg/slow-fall) is green. No headless step remains blocked.

## Next Phase Readiness
- MOB-PASS-01/02/03 closed: all four vanilla passives boot-load, spawn (operator + natural), and carry their jar-faithful goals + per-mob extras; the pig oracle remains the standing bit-exact gate.
- Open carryover (pre-existing, NOT introduced here): `TestBehaviorRegressionMobSpawns`'s candidate-COLUMN pick still uses the unseeded global `rand.IntN` at `spawner.go:360` (STATE.md CARRYOVER). This plan's NEW mob-name pick is correctly seeded (per-region levelRandom), so it does not add to that flake — but the column-pick flake itself is a separate, still-open task (seed the spawner's candidate-column RNG).

## Self-Check: PASSED
- `.planning/phases/34-new-passive-mobs/34-04-SUMMARY.md` — FOUND
- `server/vanilla_mob_test.go` — FOUND
- commit `35102074` (Task 1) — FOUND
- commit `e39e7a3c` (Task 2) — FOUND

---
*Phase: 34-new-passive-mobs*
*Completed: 2026-06-30*
