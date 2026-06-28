---
phase: 27-folia-regionization
verified: 2026-06-28T18:40:00Z
status: passed
score: 7/7 must-haves verified
overrides_applied: 0
re_verification: null
---

# Phase 27: Folia regionization Verification Report

**Phase Goal:** Folia-style independent-region tick threads so the world ticks in parallel regions; the plugin call seam + entity API become region-aware (REGION-01).
**Verified:** 2026-06-28T18:40:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (the 7 success criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | STEP 1 — region struct extracted @ N=1 (per-region state moved; behavior-neutral) | ✓ VERIFIED | `server/region.go:71` `type region struct` with the 9 per-region fields (entities/world/worker/asyncIn/asyncBridge/blockTicks/blockTickSubCounter/fluidSchedule/levelRandom/spawnScanPending) + id + coord; `newRegion` (line 148) wires a fresh per-region store + per-region levelRandom (never-shared invariant). Per-region/global boundary documented in the file doc comment (lines 21-55). `TestRegionExtractionBehaviorNeutral` PASS; `TestRegionStructHasPerRegionFields` PASS. |
| 2 | STEP 2 — conc coordinator/barrier (fan-out → barrier → cross-region phase → gametime-advance-once); region panic isolated | ✓ VERIFIED | `server/region_coordinator.go:156` `var wg conc.WaitGroup` + `wg.Go`/`wg.Wait()` (lines 157-161); gametime advanced EXACTLY once at line 185 with `advanced` flag making normal/panic paths mutually exclusive (`recoverTick` lines 211-226). `TestRegionPanicIsolated` PASS, `TestSharedGameTimeAdvancedOnce` PASS, `TestCoordinatorBarrier` PASS. |
| 3 | STEP 3 — N=2 static chunk→region hash; 2 regions tick CONCURRENTLY (parallelism proven) | ✓ VERIFIED | `server/region_transfer.go:43` `regionOf` = `(col[0]^col[1])&1` (pure, restart-stable checkerboard); `regionCount=2`; `NewTickLoop` builds 2 regions. `TestTwoRegionsTickInParallel` (region_transfer_test.go:92) uses a real rendezvous (`bothArrived`/`release` channels) that DEADLOCKS if serial — proves genuine simultaneity, not just len==2. PASS. |
| 4 | Cross-region transfer at the barrier (entity A→B, no double-tick/drop, state travels) | ✓ VERIFIED | `region_transfer.go:152` `detectTransfers` (queue mid-tick on region goroutine) + `applyCrossRegionTransfers:174` (remove-from-A then add-to-B at quiescent barrier, same *Entity pointer adopted). Wired as FIRST post-phase step (region_coordinator.go:169). `TestCrossRegionTransfer` asserts present-in-B / absent-from-A / `got.ai == aiPtr` (scratch travels) / ticked-once across 2 ticks. PASS. |
| 5 | The 5 pitfalls handled (async-rejoin→owning region; tracker cross-region; gametime one anchor; handles re-resolve per region; global broadcasts via global thread) | ✓ VERIFIED | (1) async.go:164 `owningRegion(r.mobID)` re-resolve + drop-if-nil; (2) `entitiesNearAcrossRegions` wired into tracker.go:63/155, item_entity.go:176, spawner.go:116; (3) coordinator advances gametime once, regions read `gt` read-only; (4) plugin_mob_ai.go:78 `regionForEntity(e)` → region-bound handles (`newEntityHandleInRegion`); (5) region_coordinator.go:143-155 documented read-only-during-fan-out audit, `TestGlobalBroadcastSafeFromRegion` PASS. `TestAsyncRejoinRoutesToOwningRegion` + `TestTrackerSeesAcrossRegions` PASS. |
| 6 | THE GATE — region-aware Emit: an entity in region R → its declared-mob goal callback runs on R's goroutine | ✓ VERIFIED | `TestPluginHookRunsOnOwningRegion` (region_plugin_test.go:76) asserts goroutine identity: `cb != r1` FAILS the test, `cb == r0` FAILS the test, AND `sawMobInRegion1` (handle re-resolves R's store). The callback genuinely runs on region 1's tick goroutine. Mechanism: `only()` (tick.go:903) resolves per-goroutine via `currentRegion` xsync.Map registered in `region.tick` (region_coordinator.go:57-61). PASS. |
| 7 | BEHAVIOR-NEUTRAL + -race: existing suite passes UNCHANGED; regionized tick is -race clean | ✓ VERIFIED | `CGO_ENABLED=0 go build ./...` exit 0; `go vet ./server/ ./plugin/...` exit 0; `CGO_ENABLED=0 go test ./server/ ./plugin/...` PASS; full `./world/...` suite PASS (241s — documented pre-existing slowness, NOT a race). **Docker -race over `./server/ ./plugin/...` GREEN (server 48.6s, no DATA RACE reports)** — the REGION-01 load-bearing gate. |

**Score:** 7/7 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `server/region.go` | region struct + per-region/global boundary | ✓ VERIFIED | 160 lines; `type region struct` with all 9 per-region fields + newRegion |
| `server/region_coordinator.go` | conc fan-out/barrier + region.tick + advance-once | ✓ VERIFIED | 226 lines; `conc.WaitGroup` present; recoverTick exactly-once gametime |
| `server/region_transfer.go` | regionOf hash + detectTransfer + applyCrossRegionTransfers | ✓ VERIFIED | 194 lines; `func regionOf` checkerboard split + owningRegion + entitiesNearAcrossRegions |
| `server/region_plugin_test.go` | TestPluginHookRunsOnOwningRegion | ✓ VERIFIED | Asserts goroutine identity (cb==r1, cb!=r0) + store re-resolution |
| `server/region_race_test.go` | TestRegionizedTickRace | ✓ VERIFIED | Combined 2-region + transfer + tracker + Emit scenario; PASS 5.06s, -race clean |
| `server/region_transfer_test.go` | regionOf + parallel + transfer + async-rejoin | ✓ VERIFIED | Parallelism rendezvous probe; transfer ai-pointer identity check |
| `server/region_coordinator_test.go` | barrier + panic + gametime-once | ✓ VERIFIED | All 3 pass |

### Key Link Verification

| From | To | Via | Status |
|------|-----|-----|--------|
| tickOnce | region fan-out then post-phase | conc.WaitGroup.Go + Wait() | ✓ WIRED (region_coordinator.go:157-161) |
| region.tick | shared gametime gt (read-only) | coordinator advances once after barrier | ✓ WIRED (gt read-only; line 185 advance) |
| applyCrossRegionTransfers | regionA.entities.remove → regionB.entities.add | pendingTransfers drain at barrier | ✓ WIRED (region_transfer.go:189-190) |
| starlarkGoal.handles | owning region's store | regionForEntity(e) → region-bound handles | ✓ WIRED (plugin_mob_ai.go:78-81) |
| pathReady.applyTo | owning region's store | owningRegion(id) + drop if nil | ✓ WIRED (async.go:164-167) |
| tracker.Tick / item pickup / spawner | cross-region near() | entitiesNearAcrossRegions | ✓ WIRED (tracker.go:63,155; item_entity.go:176; spawner.go:116) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| REGION-01 | 27-01/02/03 | Folia per-region tick threading; region-aware plugin seam + entity API; -race clean | ✓ SATISFIED | All 3 roadmap success criteria verified (truths 1-7); Docker -race green |

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| `server/region_transfer.go:86` `emitEntityEvent` | Defined but no production caller (only referenced in design comments/tests) | ℹ️ Info | NOT a gap. The region-aware Emit GATE (truth 6) is satisfied by the region-bound *handles* (plugin_mob_ai.go), which the gate test verifies. The direct entity-scoped Emit calls (combat.go death/damage, structure_spawn.go) fire on the frozen+shared Phase-21 registry — thread-safe by the freeze, confirmed -race clean. `emitEntityEvent` is an unused convenience helper, not a missing link. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build pure-Go static | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Vet | `go vet ./server/ ./plugin/...` | exit 0 | ✓ PASS |
| Phase 27 gate tests (forced, uncached) | `go test -count=1 ./server/ -run "TestRegion\|TestTwoRegions\|TestCrossRegion\|TestPluginHook\|..."` | 14/14 PASS | ✓ PASS |
| Docker -race (LOAD-BEARING) | `docker run golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...` | server ok 48.6s, no DATA RACE | ✓ PASS |
| World suite behavior-neutral | `CGO_ENABLED=0 go test ./world/...` | all ok (241s) | ✓ PASS |

### Commit Attribution Check

No Co-Authored-By, no "Generated with Claude", no robot-emoji footers. Author: Matias Canovas <hello@hinotori.moe>. Only the mandated `Claude-Session` trailer present (harness-required, not an attribution). ✓ CLEAN.

### Deferred Items (informational)

The `world` -race suite exceeds `-timeout 900s` under the race detector — a documented PRE-EXISTING slowness (world imports nothing from server; it is a TIMEOUT dump rooted in worldgen, NOT a DATA RACE; passes clean at `-timeout 2400s`). Logged in `deferred-items.md`. Does NOT affect the verdict — the -race gate is correctly scoped to `./server/ ./plugin/`, which is green.

The real-client gate + the perf-scaling benchmark are explicitly Phase 28 (the roadmap separates them). Phase 27 is autonomous/standalone-testable.

### Gaps Summary

None. All 7 success criteria are verified against the codebase (not SUMMARY claims): the region struct exists with the documented boundary, the conc barrier advances gametime exactly once with panic isolation, N=2 ticks genuinely in parallel (rendezvous-proven), cross-region transfer moves the live *Entity at the barrier, all 5 pitfalls are wired into production paths, the region-aware-Emit gate asserts goroutine identity, and the whole regionized tick is Docker -race clean while the existing world suite passes unchanged.

---

_Verified: 2026-06-28T18:40:00Z_
_Verifier: Claude (gsd-verifier)_
