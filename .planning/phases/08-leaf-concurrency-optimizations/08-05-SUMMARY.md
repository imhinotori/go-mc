---
phase: 08-leaf-concurrency-optimizations
plan: 05
subsystem: concurrency
tags: [ants, spawner, natural-spawn, snapshot, async, mob-spawning, opt-03, opt-06, race-clean]

# Dependency graph
requires:
  - phase: 08-leaf-concurrency-optimizations (08-01)
    provides: "the async substrate — spawnPool (ants), asyncIn2 (owner-drained), submitOrDrop drop-on-overload, and the spawnCandidatesReady/spawnCandidate asyncResult contract (stub applyTo)"
  - phase: 07-ai-pathfinding-commands-chat (07-03)
    provides: "the synchronous naturalSpawn (spawnableColumns + findStandableY + countByCategory + mobNear + newPigAI) this plan splits scan-from-mutation"
  - phase: 06-entities-physics-interaction (06-01)
    provides: "entityStore.add + EntityIDAllocator + the per-column bucket index the owner-side add re-buckets into"
provides:
  - "OPT-03: natural-spawn candidate SCAN runs off-tick over an immutable solidity snapshot (spawnSnapshot), with the spawn MUTATION (entityStore.add + idAlloc) rejoined on the owner via spawnCandidatesReady.applyTo"
  - "spawnSnapshot — the per-column frozen solidity copy (the spawn analogue of pathRegion), built on the owner via snapshotSpawnColumns and read off-tick by findStandableYIn"
  - "owner-side cap re-check + mobNear re-check on apply (the stale-scan anti-flood/anti-piling safety)"
  - "the spawnScanPending single-in-flight gate (one scan at a time)"
affects: [08-06-OPT-04, 08-07-OPT-06]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Owner-snapshot -> off-tick scan -> owner-mutate: the snapshotRegion/computePath A* discipline reused for the spawner — copy solidity on the owner, scan the copy off-tick, mutate (add) only on the owner"
    - "Apply-time re-validation against the authoritative store: the off-tick scan is advisory; the owner re-checks the cap + mobNear before the single mutation (a stale scan can never over-spawn or pile)"
    - "Single-in-flight gate (spawnScanPending) cleared unconditionally on apply so an empty/dropped result never wedges future scans"

key-files:
  created: []
  modified:
    - "server/spawner.go — naturalSpawn split (owner snapshot+submit); spawnSnapshot + snapshotSpawnColumns + findStandableYIn (the off-tick scan); spawnCandidatePick"
    - "server/async.go — spawnCandidatesReady.applyTo filled (cap re-check + mobNear re-check + one owner-side Pig add); spawnableChunkCount carried on the result"
    - "server/tick.go — spawnScanPending tick-owned gate field"
    - "server/spawner_test.go — runSpawnCycle harness + 5 OPT-03 tests; Phase-7 spawner tests adapted to drain the off-tick rejoin (assertions unchanged)"

key-decisions:
  - "Random candidate (x,z) picks roll ON the owner (trivial rand); only the EXPENSIVE standable-Y solidity scan goes off-tick — so the snapshot copies just the exact picked columns' Y-windows (a tight per-column copy), not the whole 17x17 spawnable area"
  - "spawnSnapshot is a dedicated sparse per-column map[(x,z)]map[y]bool rather than reusing pathRegion's dense bitset — the candidate columns are scattered, so a dense box over their bounding extent would be ~1.3M cells; the sparse copy is tight and frozen"
  - "out-of-snapshot reads report NON-solid (air) — the scan never probes beyond its own bounded window, so air is the faithful ON_GROUND policy there (unlike pathRegion's out-of-box=solid frontier-containment policy)"
  - "the worker ALWAYS sends spawnCandidatesReady (even an empty candidate set) so applyTo clears the in-flight gate; otherwise a cycle that found nothing would wedge spawnScanPending forever"

patterns-established:
  - "Off-tick read / owner-mutate split for a third subsystem (after OPT-01 path, OPT-02 tracker): the snapshot is the proof of -race cleanliness"

requirements-completed: [OPT-03, OPT-06]

# Metrics
duration: 35 min
completed: 2026-06-24
---

# Phase 8 Plan 05: Async Mob Spawning (OPT-03) Summary

**Natural-spawn candidate scan moved off-tick over an immutable per-column solidity snapshot (spawnSnapshot), with the cap-rechecked spawn mutation rejoined on the owner via spawnCandidatesReady.applyTo — -race clean, every anti-flood/anti-piling guard preserved.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-24T17:37:00Z
- **Completed:** 2026-06-24T18:12:05Z
- **Tasks:** 1 (TDD)
- **Files modified:** 4

## Accomplishments

- **Split `naturalSpawn`** so the read-only candidate SCAN runs off-tick: on the tick it gates single-in-flight, cap-gates on the owner, rolls the random candidate `(x,z)` picks, COPIES those columns' solidity into an immutable `spawnSnapshot` (mirroring `snapshotRegion`), and submits the standable-Y scan (`findStandableYIn` over the snapshot) to `spawnPool`. On pool overload the cycle is skipped (retries next `spawnInterval`).
- **Filled `spawnCandidatesReady.applyTo`** on the owner: clears the in-flight gate, RE-CHECKS the CREATURE cap against the authoritative store, then adds ONE Pig (with `newPigAI`) at the first still-unoccupied candidate (`mobNear` re-checked against the LIVE store). The mutation (`entityStore.add` + `idAlloc.AllocID`) stays single-owner (TICK-05).
- **The scan reads ONLY the frozen snapshot** — no live `ChunkManager`/`entityStore` — so the off-tick scan ↔ owner add boundary is `-race` clean by construction. Proven by the Docker `-race` gate.
- **Every existing guard survives the swap:** the `spawnInterval` throttle (still gated in `tickAI`), the CREATURE cap (gated on submit AND re-checked on apply), the `mobNear` packing guard (re-checked on apply), and the bounded `findStandableY` window (the snapshot is sized to it).
- **5 new OPT-03 tests + adapted Phase-7 spawner tests** all green; the `runSpawnCycle` harness drives the off-tick scan → owner rejoin so the Phase-7 assertions are unchanged.

## Task Commits

1. **Task 1 (impl): off-tick scan + owner-side cap-checked add** - `2dfa064b` (feat)
2. **Task 1 (tests): async rejoin, cap re-check, occupied/overload drops** - `47382500` (test)

_Single TDD task; the impl swaps existing tested behavior (scan off-tick), so the test commit adapts the Phase-7 harness + adds the 5 async assertions alongside the feat._

## Files Created/Modified

- `server/spawner.go` — `naturalSpawn` split into owner snapshot+submit; new `spawnSnapshot` (frozen per-column solidity), `snapshotSpawnColumns` (owner-side copy), `findStandableYIn` (off-tick scan over the snapshot), `spawnCandidatePick` (owner-rolled random positions). `spawnScanPending` gate + `submitOrDrop` overload skip. `findStandableY` retained as the synchronous golden reference `findStandableYIn` mirrors.
- `server/async.go` — `spawnCandidatesReady.applyTo` filled (gate clear + cap re-check + `mobNear` re-check + one owner-side Pig add); `spawnableChunkCount` carried on the result so the cap is recomputed on apply; `data/entity` import added for the owner-side `NewEntity(..., entity.Pig, ...)`.
- `server/tick.go` — `spawnScanPending bool` tick-owned field.
- `server/spawner_test.go` — `runSpawnCycle` harness; `TestAsyncSpawnRejoinsAndAdds`, `TestAsyncSpawnCapRecheck`, `TestAsyncSpawnOccupiedDropped`, `TestAsyncSpawnPoolOverloadSkips`, `TestAsyncSpawnScanReadsSnapshot`; Phase-7 tests (`TestSpawnCapAccounting`, `TestSpawnPlacementOnGround`, `TestSpawnAddsToStore`, `TestTickAISpawns`) adapted to drain the rejoin (assertions unchanged).

## Decisions Made

- **Owner rolls the random `(x,z)` picks; only the standable-Y solidity scan goes off-tick.** The random pick is trivial; the expensive part is reading column solidity. This lets the snapshot copy only the exact picked columns' Y-windows (a tight sparse copy) instead of the full ~17×17 spawnable area.
- **`spawnSnapshot` is a sparse per-column `map[(x,z)]map[y]bool`, not a dense `pathRegion` bitset.** Scattered candidate columns over a dense bounding box would be ~1.3M cells; the sparse copy is small and still immutable/frozen.
- **Out-of-snapshot reads = air (non-solid).** The scan never probes beyond its bounded window, so air is the faithful ON_GROUND policy there (deliberately different from `pathRegion`'s out-of-box=solid frontier-containment policy, which exists for the A* frontier, not a spawn scan).
- **The worker always sends a result (even empty)** so `applyTo` always clears `spawnScanPending` — an empty/over-cap/occupied drop can never wedge the in-flight gate.

## Deviations from Plan

None - plan executed exactly as written. The plan's `<action>` allowed reusing `pathRegion` OR a small dedicated solidity snapshot; the dedicated sparse `spawnSnapshot` was chosen for the reasons above (an explicitly sanctioned plan option, not a deviation).

## Issues Encountered

- The Phase-7 spawner tests called `loop.naturalSpawn()` expecting a synchronous add; after the split they saw zero adds (the scan is now off-tick). Resolved with the `runSpawnCycle` harness (submit → receive the single worker result off `asyncIn2` → `applyTo` on the test goroutine), which is exactly the production `applyAsyncResults` flow made deterministic — assertions left unchanged, as the plan directed.

## Verification

- `go test ./server/ -run 'TestAsyncSpawn|TestSpawn|TestNaturalSpawn|TestTickPhaseOrder' -count=1` — PASS (async scan rejoins + adds; cap re-checked; occupied/overload dropped; throttle + phase order intact).
- Full `go test ./...` — PASS (all packages).
- `go vet ./server/...` clean; `go build ./...` exits 0; no `go.mod`/`go.sum` change (no new external deps).
- Docker `-race` over `./server/... ./world/...` — CLEAN: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/... ./world/... -count=1` (the off-tick scan captures only the frozen snapshot + ints; the add is owner-only — OPT-06 slice for this subsystem).

## Next Phase Readiness

- OPT-03 complete: the third async subsystem (after OPT-01 pathfinding and OPT-02 tracker) follows the same owner-snapshot → off-tick read → owner-mutate discipline.
- **OPT-04 (08-06) contention finding confirmed:** the `ChunkManager` stays a PLAIN map — the spawn scan reads a copied solidity snapshot, not the live world, so no `xsync` swap is warranted for the spawner. OPT-04 extends the `xsync` surface only where profiling/contention warrants.
- **OPT-06 (08-07):** this subsystem's `-race` slice is green; the final wave-wide `-race` gate inherits a clean spawner boundary.

## Self-Check: PASSED

- All modified files present on disk: `server/spawner.go`, `server/async.go`, `server/tick.go`, `server/spawner_test.go`, `08-05-SUMMARY.md`.
- Both task commits present in history: `2dfa064b` (feat), `47382500` (test).
- Plan `<verification>` re-run: spawner/async tests PASS, full suite PASS, `go vet`/`go build` clean, Docker `-race` clean, no new deps.

---
*Phase: 08-leaf-concurrency-optimizations*
*Completed: 2026-06-24*
