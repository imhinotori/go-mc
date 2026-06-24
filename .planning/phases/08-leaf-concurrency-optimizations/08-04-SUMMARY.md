---
phase: 08-leaf-concurrency-optimizations
plan: 04
subsystem: entity-tracker
tags: [concurrency, async, tracker, opt-02, ants]
requires: [08-01]
provides:
  - "asyncTracker (off-tick visibility-diff executor behind the unchanged tracker.Tick() seam)"
  - "trackerDiffReady.applyTo filled (owner-side packet emission + tracked-set delta apply)"
  - "computeTrackerDiff (pure diff math over an immutable snapshot)"
affects:
  - "server/tick.go NewTickLoop swap-point (now assigns asyncTracker)"
  - "server/async.go trackerDiffReady contract (extended with added/removed delta)"
tech-stack:
  added: []
  patterns:
    - "snapshot-on-owner + diff-off-tick + emit-on-owner (the chunkReady rejoin discipline, generalized to per-player visibility)"
    - "tracked-delta message (added/removed ids) so the owner updates a plain map deterministically; the worker never touches it"
key-files:
  created: []
  modified:
    - server/tracker.go
    - server/async.go
    - server/tick.go
    - server/tracker_test.go
    - server/tick_test.go
key-decisions:
  - "The off-tick diff carries a tracked DELTA (added/removed id slices), not the whole new tracked set — so applyTo is an O(delta) owner-side bookkeeping step that exactly mirrors what the packets did, and p.tracked stays a plain map mutated only on the owner (Pitfall 1)."
  - "The snapshot copies near()'s live *Entity into worker-owned value Entity structs (snapshotEntity); the pure encoders take the address of those worker-owned copies, so no live-store pointer (and no *mobAI) crosses the off-tick boundary (Pitfall 3)."
  - "The Phase-6 tracker tests were re-pointed (harness only) at the golden entityTracker via a syncTrackerTick helper — they assert the synchronous diff LOGIC, which entityTracker still implements unchanged; the async equivalence is a separate new test (TestAsyncTrackerMatchesSync)."
requirements-completed: [OPT-02, OPT-06]
duration: 18 min
completed: 2026-06-24
---

# Phase 8 Plan 04: Async Entity Tracker (OPT-02) Summary

Swapped the entity-tracker EXECUTOR to compute the per-player visibility diff OFF the main tick (in the `trackerPool` ants pool) over an immutable snapshot, rejoining via `trackerDiffReady` through the unchanged `applyAsyncResults` seam — packet emission and the `p.tracked` update stay owner-side. The swap is a single line in `NewTickLoop`; the synchronous `entityTracker` is kept verbatim as the golden reference.

- Duration: 18 min (start 2026-06-24T17:42Z, end 2026-06-24T18:00Z)
- Tasks: 1 (TDD: RED → GREEN, no REFACTOR needed)
- Files modified: 5

## What was built

**The swap behind `tracker.Tick()` (one line).** `NewTickLoop` now assigns `&asyncTracker{loop: t}` instead of `&entityTracker{loop: t}`. The `tracker interface{ Tick() }` interface, the `t.tracker.Tick()` call site in `tick_phases.go`, and the pipeline order are all unchanged (`TestTickPhaseOrder` and `TestTrackerTickStub` pass — the latter's assertion updated to expect `*asyncTracker` as the swapped-in executor, while the load-bearing once-per-tick synchronous-call contract is still proven via the `countingTracker`).

**The snapshot / rejoin / validation.**
- `asyncTracker.Tick()` runs on the owner and does only cheap work per player: it calls `near()` (a fresh slice), copies each in-range entity (skipping the player's own id) into a detached value `Entity` via `snapshotEntity`, copies `p.tracked` into a fresh map, captures `p.entityID`, and submits the diff closure to `trackerPool` via `submitOrDrop`. On `ErrPoolOverload` the player is skipped this tick (recomputes next tick — a one-tick-late visibility update, the OPT-01 tolerate-late rationale).
- The worker runs `computeTrackerDiff(snap, trackedCopy)` — the golden `entityTracker` diff logic lifted verbatim to operate over the snapshot values: newly-in-range → `AddEntity` + `SetEntityData` (+ `SetEntityMotion` if moving); still-in-range + tracked → `TeleportEntity` + `RotateHead`; gone → one batched `RemoveEntities`. It returns the `[]pk.Packet` plus the tracked delta (`added`/`removed` id slices) and sends `trackerDiffReady{playerID, packets, added, removed}` on `asyncIn2`.
- `trackerDiffReady.applyTo` runs on the owner: re-resolves the player by id (`playerByEntityID`); a player who left between submit and apply finds no match → DROP (no send, no nil-deref). Otherwise it `p.client.Send`s each packet and applies the delta to `p.tracked` (add spawns, delete despawns), so the next tick's diff sees the correct set.

**Off-tick math confirmed, send stays owner-side.** The only work in the pool worker is `computeTrackerDiff` + the channel send of an immutable result. `p.client.Send` and every `p.tracked` mutation happen exclusively in `applyTo` on the owner. `snapshotEntity` deliberately omits the `ai *mobAI` pointer and deep-copies `metadata`, so the closure captures no alias into the live store. `TestAsyncTrackerSendsOnOwner` proves no packet and no tracked-mutation occurs until the owner drains the result.

## Test results

- `go test ./server/ -run 'TestAsyncTracker|TestTracker|TestVisibilityDiff|TestTickPhaseOrder' -count=1` — PASS
  - New: `TestAsyncTrackerMatchesSync` (async emits the same per-id packet counts as the sync golden for a 3-entity scene + tracked set of 3), `TestAsyncTrackerSendsOnOwner`, `TestAsyncTrackerLeftPlayerDropped`, `TestAsyncTrackerSwapPointCompiles`.
  - Phase-6 tracker tests green: `TestVisibilityDiff`, `TestTrackerSelfNotTracked`, `TestTrackerMove`, `TestTrackerRemove`, `TestTrackerRemoveBatchesMany`, `TestTrackerSynchronousNoChangeToSeam` (driven at the golden `entityTracker` via the `syncTrackerTick` harness helper — assertions unchanged).
- `go test ./server/... -count=1` — all packages PASS.
- `go vet ./server/...` and `go vet ./...` — clean.
- `go build ./...` — exit 0.
- Docker `-race`: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/... -count=1` — all packages PASS, race-clean. The snapshot copy (workers read value copies, owner Sends) is the structural proof of the off-tick-diff ↔ owner-send boundary.
- No new external deps (`go.mod`/`go.sum` unchanged).

## Deviations from Plan

**[Rule 3 - Blocking] Re-pointed the Phase-6 tracker tests at the golden `entityTracker`**
- Found during: Task 1 (RED→GREEN, running the existing suite after the swap).
- Issue: `TestVisibilityDiff`/`TestTrackerMove`/`TestTrackerRemove`/`TestTrackerRemoveBatchesMany`/`TestTrackerSelfNotTracked` and `TestTrackerTickStub` called `loop.tracker.Tick()` expecting SYNCHRONOUS emission. After the swap `loop.tracker` is the async executor (submits off-tick, never drains within the test), so those assertions failed.
- Fix: added a `syncTrackerTick(loop)` helper that drives `(&entityTracker{loop}).Tick()` directly and re-pointed the Phase-6 diff-logic tests at it (the assertions themselves are unchanged — they verify the golden diff logic the kept `entityTracker` still implements). Updated `TestTrackerTickStub`'s default-type assertion from `*entityTracker` to `*asyncTracker` (the swapped-in executor); its load-bearing once-per-tick-synchronous contract is still proven by the `countingTracker` swap. The async equivalence is covered by the new `TestAsyncTrackerMatchesSync`.
- Files modified: server/tracker_test.go, server/tick_test.go
- Verification: full `./server/...` suite + Docker `-race` green.
- Commit: 1f6268be

This matches the plan's explicit guidance (gotcha #5/#6): adjust the harness, not the assertions, so the Phase-6 behavior still works and its tests stay green.

**Total deviations:** 1 auto-fixed (1 blocking). **Impact:** none on behavior — the async tracker emits identical spawn/move/despawn packets; the change is a test-harness re-pointing plus one assertion-type update reflecting the intended swap.

## Self-Check: PASSED

- `server/tracker.go`, `server/async.go`, `server/tick.go`, `server/tracker_test.go`, `server/tick_test.go` — all present and modified on disk.
- Commits present: `f4a74b11` (test RED), `1f6268be` (feat GREEN) — both in `git log`.
- TDD gates: `test(08-04)` RED commit precedes `feat(08-04)` GREEN commit. No REFACTOR commit (none needed).
