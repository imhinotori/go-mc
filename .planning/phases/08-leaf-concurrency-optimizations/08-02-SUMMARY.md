---
phase: 08-leaf-concurrency-optimizations
plan: 02
subsystem: ai-pathfinding
tags: [async, pathfinding, a-star, ants, goroutine-pool, tick-loop, race, opt-01]

# Dependency graph
requires:
  - phase: 07-ai-pathfinding-commands-chat
    provides: "the PURE computePath over an immutable pathRequest snapshot + the requestPath/shouldRecomputePath/tick navigation seam this plan swaps the executor behind"
  - phase: 08-leaf-concurrency-optimizations
    plan: 01
    provides: "the Wave-0 substrate: pathPool (ants, NumCPU), submitOrDrop (drop-on-overload), asyncIn2 rejoin channel, the pathReady contract + its validate-then-no-op applyTo stub, applyAsyncResults dual-drain"
provides:
  - "OPT-01: the synchronous A* executor is swapped for the off-tick pathPool behind the UNCHANGED requestPath->computePath seam — paths tolerated 1+ ticks late, the mob keeps its action while the A* runs off-tick"
  - "groundNavigation.pending: the single-in-flight gate (one outstanding path compute per mob) + the late-path tolerance flag"
  - "pathReady.applyTo FILLED: owner-side despawn-drop + retarget-drop re-validation before adopting a late path (the load-bearing safety for a result that is the norm to arrive late)"
affects: [08-04-async-tracker, 08-05-async-spawning, 08-06-hot-collections]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "executor swap behind a pure-compute seam: snapshot owner-built ON the tick, the PURE compute submitted off-tick via submitOrDrop, the result rejoined on asyncIn2 and adopted on the owner — computePath unchanged"
    - "late-result validation: the rejoin message carries an id + target value (never a live *Entity); applyTo re-resolves on the owner and DROPS on despawn/retarget"
    - "single-in-flight gate (nav.pending): shouldRecomputePath blocks a second same-target submit while one is in flight; a target change still supersedes"

key-files:
  created: []
  modified:
    - "server/navigation.go - groundNavigation.pending; requestPath rewritten as submit-to-pathPool-and-continue (drop-on-overload leaves pending=false + path untouched); shouldRecomputePath !pending gate; tick late-path tolerance doc"
    - "server/async.go - pathReady.applyTo filled: despawn-drop (mob gone / e.ai nil) + retarget-drop (target mismatch) re-validation, then adopt nav.path + clear nav.pending on the owner"
    - "server/navigation_test.go - the 5 OPT-01 tests + drainAsyncPath helper; the Phase-7 nav tests adjusted to drain the off-tick rejoin (assertions unchanged)"
    - "server/spawner_test.go - TestTickAIDrivesMobs drains applyAsyncResults each tick (the rejoin phase) so the late path lands"

key-decisions:
  - "computePath is UNCHANGED — not one line edited (pathfinder.go untouched). Its purity (TestComputePathPure) is the contract that makes this a swap of the EXECUTOR, not a rewrite: the snapshot stays owner-built and the closure captures only the immutable req + id + target ints, never a live *Entity/*TickLoop."
  - "The submit closure captures mobID (int32) + tgt ([3]int) + req (immutable snapshot) ONLY. It computes computePath off-tick and sends pathReady on asyncIn2; the OWNER (applyTo) performs the only state mutation. No tick-owned world/entity state crosses the boundary (Pitfall 3 / T-8-05)."
  - "Drop-on-retarget reuses the EXISTING target tracking (nav.lastTX/Y/Z), NOT a separate timeout (08-RESEARCH Open Question 4): applyTo drops a late path whose target != the nav's current lastT*, so a mob that retargeted mid-flight never walks the stale destination — and this is also the safety net behind the !pending gate."
  - "pending reflects an ACCEPTED submit only: on pool overload (submitOrDrop returns false) pending is left false and n.path is UNTOUCHED — the mob keeps its last path and shouldRecomputePath re-requests next tick (Pitfall 4). The cooldown + lastT* tracking are reset regardless so the throttle anchors on the requested target even when dropped."
  - "The !pending gate in shouldRecomputePath (one in-flight request per mob) was chosen over singleflight for v1 (08-RESEARCH 'Don't Hand-Roll' notes singleflight is an option, but the simpler gate avoids a new pattern and is sufficient — a target change still supersedes a pending compute, never blocked)."

patterns-established:
  - "requestPath: snapshotRegion (owner) -> build pathRequest (owner) -> submitOrDrop(t.pathPool, func(){ asyncIn2 <- pathReady{id, target, computePath(req)} }) -> set pending=accepted; never assign n.path inline"
  - "pathReady.applyTo (owner): entities.get(mobID) + e.ai!=nil guard (despawn drop) -> lastT*==target + hasTarget guard (retarget drop) -> nav.path = r.path; nav.pending = false"
  - "test harness for an off-tick seam: drainAsyncPath spins applyAsyncResults (owner) + runtime.Gosched until the pool worker's result lands and pending clears"

requirements-completed: [OPT-01, OPT-06]

# Metrics
duration: 18min
completed: 2026-06-24
---

# Phase 8 Plan 02: Async Pathfinding (OPT-01) Summary

**The synchronous A* executor is swapped for the Wave-0 off-tick ants pathPool behind the UNCHANGED Phase-7 `requestPath`->`computePath` seam: requestPath now builds the immutable snapshot ON the tick then SUBMITS the pure `computePath(req)` to the pool and returns immediately — the mob keeps its action while the A* runs off-tick (paths tolerated 1+ ticks late) — and the result rejoins as `pathReady` via the unchanged `applyAsyncResults`, where `pathReady.applyTo` re-validates the mob still exists and the target is unchanged before adopting the late path.**

## Performance

- **Duration:** ~18 min
- **Tasks:** 1 (TDD)
- **Files modified:** 4 (0 created, 4 modified)
- **New deps:** none (ants/v2 + xsync/v4 landed in 08-01)

## Accomplishments

- **The executor swap (OPT-01), additive not a rewrite:** `requestPath` keeps building the immutable `snapshotRegion` + `pathRequest` ON the tick (the snapshot is the seam value the off-tick worker reads), then SUBMITS the PURE `computePath(req)` to `t.pathPool` via `submitOrDrop` and returns immediately. `computePath` itself is **unchanged — pathfinder.go was not touched** (its purity, `TestComputePathPure`, is the whole point). The mob keeps its current action while the A* runs off-tick; the path arrives 1+ ticks late.
- **The off-tick worker captures ONLY immutable values:** `mobID` (int32), `tgt` ([3]int), and `req` (the immutable snapshot copy) — never the live `*Entity` or `*TickLoop`. It computes `computePath(req)` off-tick and rejoins by sending `pathReady{mobID, tgt, path}` on `asyncIn2`; the OWNER performs the only mutation in `applyTo` (TICK-05 preserved).
- **`pathReady.applyTo` FILLED with the load-bearing late-result validation** (a late path is the NORM, not the exception): on the owner — (1) `e, ok := t.entities.get(r.mobID); if !ok || e.ai == nil { return }` (despawn drop, no nil-deref — `e.ai.navigation` is a struct value, never tested for nil); (2) `if !nav.hasTarget || nav.lastTX/Y/Z != r.target { return }` (retarget drop, via the existing target tracking, no separate timeout); (3) else adopt `nav.path = r.path; nav.pending = false`.
- **`groundNavigation.pending` — the single-in-flight gate + late tolerance:** set on an accepted submit, cleared by `applyTo`, left false on a dropped/overloaded submit. `shouldRecomputePath` gates a same-target second submit on `!pending` (one outstanding compute per mob); a target CHANGE still supersedes. `navigation.tick` needs no structural change — its existing `path==nil/done` no-op IS the tolerance (it keeps the mob on its last action while pending, never blocks).
- **The DoS guards SURVIVE the swap:** `navRecomputeCooldown` (the recompute throttle) and `maxVisitedBudget()` (the A* visited cap, fed into `req.maxVisited`) are reset/applied exactly as before — an unreachable-target flood stays bounded (asserted by `TestAsyncPathCooldownPreserved`).
- **OPT-06 slice for this wave:** the A* pool<->tick boundary (the worker submitting `pathReady` while the owner drains `asyncIn2`) is **Docker `-race` clean** over `./server/... ./world/...`.

## Confirmation: the swap is behind the seam

- **`computePath` unchanged + pure:** `pathfinder.go` is not in the modified-files set; `git diff` touches only `navigation.go` + `async.go` (+ tests). `TestComputePathPure` (the purity contract) still passes, proving the off-tick closure captured no live state.
- **The snapshot is owner-built:** `snapshotRegion(...)` and the `pathRequest{...}` build still run ON the tick inside `requestPath` before the submit; only `computePath(req)` moved off-tick.
- **Pipeline order unchanged:** `applyAsyncResults` stays in its slot; `TestTickPhaseOrder` passes (no reorder).

## The validation logic (pathReady.applyTo)

```
e, ok := t.entities.get(r.mobID)
if !ok || e.ai == nil { return }            // despawn drop — no nil-deref
nav := &e.ai.navigation                      // struct VALUE on mobAI, never nil
if !nav.hasTarget ||
   nav.lastTX != r.target[0] ||
   nav.lastTY != r.target[1] ||
   nav.lastTZ != r.target[2] { return }      // retarget drop — stale goal
nav.path = r.path; nav.pending = false       // adopt on the owner; follow next tick
```

This carries `r.mobID` + `r.target` (plain values, never a live `*Entity`), which is exactly what makes the late apply safe across the 1+ tick gap.

## Task Commits

1. **Task 1 (RED):** `2d9bbab7` (test) — the 5 failing OPT-01 tests + drainAsyncPath helper
2. **Task 1 (GREEN):** `431350b3` (feat) — requestPath submit-and-continue + pending + applyTo validation + Phase-7 test harness adjustments

No REFACTOR commit was needed — the GREEN implementation is minimal and clean.

## Files Modified

- `server/navigation.go` — `groundNavigation.pending` field; `requestPath` rewritten as submit-to-`pathPool`-and-continue (captures `mobID`+`tgt`+`req` only; drop-on-overload leaves `pending=false` + `n.path` untouched); `shouldRecomputePath` `!pending` single-in-flight gate; `tick` late-path tolerance documented (no logic change — the existing `path==nil` no-op IS the tolerance).
- `server/async.go` — `pathReady.applyTo` filled: despawn-drop (`!ok || e.ai == nil`) + retarget-drop (`lastT* != target` / `!hasTarget`) re-validation on the owner, then `nav.path = r.path; nav.pending = false`.
- `server/navigation_test.go` — `drainAsyncPath` helper + the 5 OPT-01 tests (`TestAsyncPathRejoins`, `...DespawnedDropped`, `...RetargetedDropped`, `...PoolOverloadDrops`, `...CooldownPreserved`); the 3 local-nav Phase-7 tests adjusted to attach the nav to `e.ai` + drain the off-tick rejoin; the serverAiStep integration test drains `applyAsyncResults` each tick. **Assertions unchanged — only the harness drives the off-tick seam.**
- `server/spawner_test.go` — `TestTickAIDrivesMobs` drains `applyAsyncResults` each tick (the rejoin pipeline phase) so the late path lands and the mob walks.

## Decisions Made

See `key-decisions` frontmatter. The load-bearing ones: `computePath` is untouched (the swap rides on its purity); the closure captures id+target+req values only (no live state crosses the boundary); drop-on-retarget reuses the existing `lastT*` tracking (no separate timeout); `pending` reflects an accepted submit only (overload drops cleanly, mob keeps its last path); the `!pending` gate (chosen over singleflight for v1) gives one in-flight request per mob while a target change still supersedes.

## Deviations from Plan

None — plan executed exactly as written.

The plan anticipated the Phase-7 navigation tests "must still pass (possibly needing a few extra ticks for the async path to land — adjust the loop count, NOT the assertion)." Adjusting those tests to drive the off-tick rejoin (drain `applyAsyncResults`) is the planned harness change for swapping a synchronous seam to asynchronous, not a deviation: every assertion is byte-for-byte unchanged, and the local-nav tests were moved onto `e.ai.navigation` only so the owner-side `applyTo` (which reaches the nav via `entities.get(mobID)`) can deliver the late path.

## Issues Encountered

None. The Wave-0 substrate (08-01) provided every seam exactly as documented (`pathPool`, `submitOrDrop`, `asyncIn2`, the `pathReady` contract + stub), so OPT-01 was filling the stub + converting the submit site, no scavenger hunt.

## Verification Results

- `go test ./server/ -run 'TestAsyncPath|TestComputePathPure|TestTickPhaseOrder' -count=1` — **PASS** (async rejoin works; despawn/retarget drop; the purity contract + pipeline order survive).
- Full `go test ./server/ -count=1` — **PASS** (the Phase-7 nav tests `TestNavigationFollow`, `TestNavigationMovesViaMoveEntity`, `TestRequestPathBuildsSnapshot`, `TestServerAiStepWalksToGoalTarget`, `TestTickAIDrivesMobs` all still green — the mob still navigates correctly, the path just arrives a tick or two later).
- Full repo `go test ./... -count=1` — **PASS** (nothing regressed; additive change to the navigation executor only).
- `go vet ./server/...` — **clean**; `go build ./...` — **exits 0**; `go.mod`/`go.sum` unchanged (no new deps).
- Docker `-race` over `./server/... ./world/...` (`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/... ./world/... -count=1`) — **clean** (the A* pool<->tick boundary — submit while the owner drains `asyncIn2` — is race-free; the OPT-06 slice for this wave passes).

## Threat Model Coverage

| Threat ID | Disposition | Status |
|-----------|-------------|--------|
| T-8-04 (late path to a despawned/retargeted mob) | mitigate | `pathReady.applyTo` despawn-drop + retarget-drop; asserted by `TestAsyncPathDespawnedDropped` + `TestAsyncPathRetargetedDropped` |
| T-8-05 (worker reading live tick state off-tick) | mitigate | closure captures only `req`+`id`+`target`; `TestComputePathPure` + Docker `-race` clean |
| T-8-06 (unreachable target flooding the A*) | mitigate | `navRecomputeCooldown` + `!pending` gate + `maxVisitedBudget` survive; asserted by `TestAsyncPathCooldownPreserved` |
| T-8-07 (pool saturation) | mitigate | `submitOrDrop` overload drop leaves the mob on its last action; asserted by `TestAsyncPathPoolOverloadDrops` |

## User Setup Required

None — no external service configuration required.

## Known Stubs

None — `pathReady.applyTo` is now fully implemented (it was the documented Wave-0 stub this plan filled). The remaining two `applyTo` stubs (`trackerDiffReady` for 08-04, `spawnCandidatesReady` for 08-05) are out of this plan's scope and unchanged.

## Next Phase Readiness

- **OPT-01 complete.** Async pathfinding is live: the heaviest per-tick AI cost (the A* compute) now runs off-tick in `pathPool`, behind the unchanged `requestPath`->`computePath` seam, rejoining with despawn/retarget validation — and `-race` clean.
- **OPT-02 (08-04, async tracker)** and **OPT-03 (08-05, async spawning)** are independent: they fill `trackerDiffReady.applyTo` / `spawnCandidatesReady.applyTo` against the same substrate (their pools `trackerPool`/`spawnPool` + the `asyncIn2` rejoin are already wired). This plan touched only the navigation executor + the `pathReady` contract, so it does not collide with those waves.
- The parallel 08-03 (`.linear` region) wave is disjoint (save/region/world); no shared files.
- No blockers.

## Self-Check: PASSED

- Modified files exist: `server/navigation.go`, `server/async.go`, `server/navigation_test.go`, `server/spawner_test.go` — all FOUND.
- Task commits exist: `2d9bbab7` (test RED), `431350b3` (feat GREEN) — both FOUND in `git log`.
- TDD gate compliance: a `test(08-02)` commit (RED) precedes a `feat(08-02)` commit (GREEN) — gate sequence satisfied.
- Plan `<verification>` re-run: target tests PASS, full server + repo suite PASS, `go vet`/`go build` clean (no new deps), Docker `-race` over `./server/... ./world/...` clean.
- `pathfinder.go` (`computePath`) confirmed UNCHANGED — the swap is executor-only.

---
*Phase: 08-leaf-concurrency-optimizations*
*Completed: 2026-06-24*
