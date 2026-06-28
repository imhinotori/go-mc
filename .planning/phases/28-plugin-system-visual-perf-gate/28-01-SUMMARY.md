---
phase: 28-plugin-system-visual-perf-gate
plan: 01
subsystem: testing
tags: [plugin, starlark, perf-gate, benchmark, mob-ai, test-kit, go-embed]

# Dependency graph
requires:
  - phase: 24-vanilla-mobs-as-plugins
    provides: "the retained Go-native pig oracle (newPigAI) + spawnVanillaPigWithID + the //go:embed boot-load pattern (vanilla_pig_embed.go) + the TestPluginPigEqualsGoNativePig A/B harness (drainPendingPath)"
  - phase: 23-plugin-mob-declarations
    provides: "declare_mob/goal builtins + mobRegistry + spawnDeclaredMob + the isolated testdata wandermob decl this plan promotes to a live embed"
provides:
  - "BenchmarkPluginPigVsGoNative — the A/B perf bench isolating the plugin pig's per-(mob·tick) overhead vs the Go-native oracle"
  - "BenchmarkTickEmitOverhead — the zero-subscriber Emit fast-path proof (0 allocs/op) + the one-subscriber documented cost"
  - "TestPerfGate — the hard, baseline-calibrated CI gate (absolute ns cap AND relative % cap)"
  - "a CUSTOM wander mob boot-loaded into the LIVE registry (server/wandermob_embed.go) alongside vanilla_pig"
  - "a SULFUR_TEST_KIT-gated spawn trigger (gate egg → handleGateSpawnEgg → spawnDeclaredMob) — the seam Plan 02's bot uses"
affects: [28-02, plugin-system, perf-regression-gating]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "A/B perf bench: two sub-benches over the IDENTICAL world + RNG seeds differing only in the AI builder; read the DELTA, never the absolute (Pitfall 1)"
    - "baseline-calibrated dual-cap gate: an absolute ns backstop + a relative % primary cap, both documented from a real multi-run baseline (threat T-28-01)"
    - "shared-store wiring for direct-step harnesses: alias every region's entity store so regionForEntity and only() agree when the full tick's region-transfer barrier is bypassed"
    - "gate-only env-toggled in-game trigger: a kit item + use-seam branch behind testKitEnabled(), inert in prod (threat T-28-03)"

key-files:
  created:
    - server/wandermob_embed.go
    - server/assets/wandermob/plugin.toml
    - server/assets/wandermob/main.star
    - server/plugin_perf_bench_test.go
    - server/perf_gate_test.go
  modified:
    - server/test_kit.go
    - server/item_use.go
    - cmd/sulfur/main.go

key-decisions:
  - "The perf gate's PRIMARY signal is the RELATIVE % cap (15%), not the absolute ns — the absolute per-(mob·tick) number (~213 µs) is dominated by drainPendingPath's async-A* latency jitter, so the absolute cap (15000 ns) is a coarse backstop only"
  - "Shared the entity store across all regions in buildPerfLoop: the direct serverAiStep+drainPendingPath harness bypasses the tick's region-transfer barrier, so a strolling pig that crosses a column boundary would otherwise resolve its goal handle against a region whose store does not hold it (corrupting the plugin number) — aliasing the stores fixes this with NO AI/RNG/physics change"
  - "The gate spawn egg is a pig-spawn-egg re-skin (the wander mob renders as the pig wire id by design — custom = behavior) in main-inventory slot 35, matched in handleUseItem BEFORE the food path, gated by testKitEnabled()"

patterns-established:
  - "Promote-to-live-embed: a Phase-23 testdata-isolated decl becomes a //go:embed asset + a registerXxx merge into the SAME tick-owned registry at boot (RegisterWanderMob)"
  - "Zero-subscriber hot-path proof: a dedicated bench sub-case asserts the unsubscribed Emit costs 0 allocs/op"

requirements-completed: [PLUGIN-07]

# Metrics
duration: 17min
completed: 2026-06-28
---

# Phase 28 Plan 01: Plugin Perf Gate + Custom-Mob Spawn Trigger Summary

**A baseline-calibrated CI perf gate proves the plugin pig's per-tick overhead is ~1% (well within a 15% cap), the zero-subscriber Emit path is allocation-free, and a custom wander mob now boot-loads into the live registry and is spawnable in-game via a SULFUR_TEST_KIT gate egg — the server-side half of PLUGIN-07.**

## Performance

- **Duration:** 17 min
- **Started:** 2026-06-28T23:17:04Z
- **Completed:** 2026-06-28T23:34:53Z
- **Tasks:** 2 completed
- **Files modified:** 8 (5 created, 3 modified)

## Accomplishments
- Promoted the Phase-23 isolated wander-mob decl to a live `//go:embed` asset boot-loaded into the SAME tick-owned registry as vanilla_pig (both spawnable).
- Added the `SULFUR_TEST_KIT` gate-egg spawn trigger (slot 35 → `handleGateSpawnEgg` → `spawnDeclaredMob`), wired into `handleUseItem` before the food path and inert in prod.
- Promoted the Phase-24 A/B equality harness into a perf bench + a hard `TestPerfGate` whose dual caps are set from a documented real baseline.
- Proved the zero-subscriber `Emit` hot path is 0 allocs/op.

## Measured Perf Baseline (the numbers Plan 02 reads)

Measured 2026-06-28 on AMD Ryzen 7 5800X (8-Core), windows/amd64, Go default CGO=0 host.

**`TestPerfGate` (avgTickNs wall-clock; 100 mobs, warmup=50, ticks=2000) — 4 runs:**

| Run | go-native ns/(mob·tick) | plugin ns/(mob·tick) | delta ns | delta % |
|-----|-------------------------|----------------------|----------|---------|
| 1   | 212679.5                | 215036.8             | 2357.3   | 1.11%   |
| 2   | 213062.8                | 217850.2             | 4787.4   | 2.25%   |
| 3   | 212943.0                | 216515.8             | 3572.8   | 1.68%   |
| 4 (gate pass) | 213793.0      | 215382.8             | 1589.8   | 0.74%   |

The absolute ~213 µs/(mob·tick) is dominated by `drainPendingPath`'s blocking async-A* round-trip latency, NOT AI cost — so its delta is noisy. The **relative %** (1.1–2.25%, median ~1.7%) is the stable plugin-tax signal.

**`BenchmarkPluginPigVsGoNative` (100 mobs/iter, -count=3) corroboration:**

| Arm | ns/op | B/op | allocs/op |
|-----|-------|------|-----------|
| go-native | ~14.95M | 705512 | 3436 |
| plugin    | ~15.10M | 937457 | 10245 |

Plugin delta ≈ 1.0% ns; the plugin tax is the starlark goal call's per-tick allocation (10245 vs 3436 allocs/100-mob-iter), not a CPU blowup.

**`BenchmarkTickEmitOverhead`:**

| Sub-bench | ns/op | allocs/op |
|-----------|-------|-----------|
| zero-subscribers | ~20–25 | **0** |
| one-subscriber   | ~360–370 | 7 |

## Chosen Threshold Caps

- **`maxDeltaNsPerMobTick = 15000.0`** — ABSOLUTE backstop, ~3× the max observed 4787 ns delta (sized above the async-latency jitter).
- **`maxDeltaPct = 15.0`** — RELATIVE primary cap, ~6–7× the median ~1.7% / above the 2.25% peak, far below a "plugin doubles cost" regression.

Both documented in-file (`server/perf_gate_test.go` baseline comment) so the values are auditable (threat T-28-01).

## Wander-Mob Embed / Boot-Load Wiring

- **Asset:** `server/assets/wandermob/{plugin.toml,main.star}` — copied from the Phase-23 testdata fixture (kept identical). The decl: `declare_mob(name="wanderer", base_type="pig", ...)` with ONE MOVE goal (`on_wander_tick`) that nav-targets +8 east so the mob WALKS via the Go nav. Renders as the pig wire id (custom = behavior).
- **Loader:** `server/wandermob_embed.go` — `//go:embed assets/wandermob/...`, `const wanderMobName = "wanderer"`, `LoadWanderMobRegistry()` (materialize + LoadDirWith with declare_mob/goal injected, fails loudly if the decl is absent), and `RegisterWanderMob()` which MERGES the captured decl into the existing tick-owned (vanilla_pig) registry.
- **Boot:** `cmd/sulfur/main.go` — after `tick.SetMobRegistry(reg)` for vanilla_pig, calls `tick.RegisterWanderMob()` (FATAL on failure). Single-owner at boot, before tick.Run (TICK-05). Both `vanilla_pig` AND `wanderer` are now in the live registry.

## Test-Kit Trigger Shape (the seam Plan 02's bot drives)

- **Item:** `gateSpawnEggID = item.PigSpawnEgg.ID` (id **1161**), a gate-only re-skin.
- **Slot:** main-inventory menu **slot 35** (last main row), added to `testKit` only — so it lands only when `SULFUR_TEST_KIT=1`.
- **Use seam:** `server/item_use.go::handleUseItem` — after the hand/validity decode, BEFORE the food path: if `testKitEnabled()` and the held item's `ItemID == gateSpawnEggID`, call `t.handleGateSpawnEgg(p)` and return.
- **Trigger:** `server/test_kit.go::handleGateSpawnEgg(p)` — guards on `testKitEnabled() && t.mobRegistry != nil`, looks up the `"wanderer"` decl, and spawns it ~2 blocks in front (`p.x+2.0, p.y, p.z`) via `t.spawnDeclaredMob(decl, ...)`. Owner-goroutine, no locks. The bot distinguishes the custom mob by its Go-nav wander movement, not its wire type (threat T-28-05).

## Task Commits

1. **Task 1: boot-load custom wander mob into live registry + test-kit spawn trigger** — `63c65daf` (feat)
2. **Task 2: A/B perf bench + Emit-overhead bench + TestPerfGate** — `6fd45e9e` (test)

## Files Created/Modified
- `server/wandermob_embed.go` (created) — embed + boot-load + RegisterWanderMob merge of the custom wander mob into the live registry.
- `server/assets/wandermob/plugin.toml`, `server/assets/wandermob/main.star` (created) — the embedded custom wander-mob declaration.
- `server/plugin_perf_bench_test.go` (created) — the A/B pig bench, the per-mob serverAiStep bench, and the Emit-overhead bench.
- `server/perf_gate_test.go` (created) — `TestPerfGate` (baseline-calibrated dual-cap assertion) + `avgTickNs` helper.
- `server/test_kit.go` (modified) — `gateSpawnEggID`, the kit slot-35 entry, and `handleGateSpawnEgg`.
- `server/item_use.go` (modified) — the gate-egg branch in `handleUseItem` before the food path.
- `cmd/sulfur/main.go` (modified) — `tick.RegisterWanderMob()` boot-load after the vanilla_pig install.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Shared the entity store across regions in the perf harness to fix dropped plugin goal callbacks**
- **Found during:** Task 2 (first A/B bench run)
- **Issue:** With `regionCount = 2` (a const), a plugin pig that strolls across a chunk-column boundary has its goal-callback handle built via `regionForEntity` (by position) while the pig is stored in `only()`'s `globalRegion`. The direct `serverAiStep`+`drainPendingPath` bench bypasses the full tick's region-transfer barrier, so the handle resolved against a region whose store did not hold the pig → "entity N no longer exists" → the plugin goal silently no-opped, CORRUPTING the measured plugin cost (a failing callback is cheaper than a succeeding one).
- **Fix:** Alias every region's `.entities` to `regions[globalRegion].entities` in `buildPerfLoop` (the same pattern `newPhysicsLoop` uses for the shared world), so `regionForEntity` and `only()` always resolve the SAME store. Changes NO AI/RNG/physics — it only removes the direct-step harness's region-transfer gap. The production tick handles transfer at the barrier; the perf number is identical once the callback actually runs.
- **Files modified:** server/plugin_perf_bench_test.go
- **Commit:** 6fd45e9e

**2. [Rule 3 - Blocking] `gateSpawnEggID` changed from const to var**
- **Found during:** Task 1 (build)
- **Issue:** `item.PigSpawnEgg.ID` is a struct field, not a compile-time constant, so `const gateSpawnEggID = item.PigSpawnEgg.ID` failed to build.
- **Fix:** Declared it `var` with a comment explaining why.
- **Files modified:** server/test_kit.go
- **Commit:** 63c65daf

## Threshold Re-Calibration Note

The PLAN suggested deriving the absolute cap as `measured delta × 1.5–2×`. The real measured absolute delta turned out noisy (driven by async-A* latency, not AI cost), so per the gate-quality mandate the RELATIVE % was made the primary cap and the absolute cap was sized as a coarse 3× backstop. This is the documented, auditable calibration (threat T-28-01) — the gate still fails on a real plugin regression (which moves the relative %).

## Verification

- `CGO_ENABLED=0 go build ./cmd/sulfur ./server/` — exit 0 (default static binary preserved).
- `go vet ./server/ ./cmd/sulfur` — clean.
- `go test -run TestPerfGate ./server/ -v` — exit 0 (delta 0.74%, within both caps).
- `go test -bench 'BenchmarkPluginPigVsGoNative|BenchmarkTickEmitOverhead|BenchmarkServerAiStepPluginVsGo' -benchmem ./server/ -benchtime=20x` — all three run; zero-subscriber Emit = 0 allocs/op.
- `go test ./server/ -count=1` — full server suite green (111s).
- Docker -race NOT run here (needs CGO=1; the bench/trigger add no cross-tick state — the spawn path is tick-owned and the equality path is already -race-verified in Phase 24). Flagged for the orchestrator if a CGO=1 race pass is desired.

## Notes for Plan 02 (the bot run)
- Spawn the custom mob: join with `SULFUR_TEST_KIT=1`, the gate egg is in main-inventory slot 35 (item id 1161); right-click it (`ServerboundUseItem`) to spawn the wander mob ~2 blocks in front (+X).
- The mob renders as the pig wire id; distinguish it by its Go-nav wander movement (it walks ~8 blocks east toward its nav target), not its wire type.

## Self-Check: PASSED

All 6 created files exist on disk; both task commits (63c65daf, 6fd45e9e) are present in git history.
