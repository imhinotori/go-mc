---
phase: 10-worldgen-foundation
plan: 03
subsystem: worldgen

# Dependency graph
requires:
  - phase: 10-01-worldgen-foundation
    provides: LegacyRandomSource LCG + WorldgenRandom (the decoration RNG the feature phase seeds per chunk origin)
  - phase: 10-02-worldgen-foundation
    provides: level.HeightmapUpdate (incremental worldgen-heightmap primitive) + surface.BuildWorldgenHeightmaps (pre-decoration bulk build)
provides:
  - "Split Generator interface: GenerateTerrain (pure single-chunk, status carvers) + Decorate (post-carve 3x3 pass) + Dims"
  - "Concrete Generate(pos) retained on *NoiseGenerator and *Superflat (= GenerateTerrain then Decorate over a 3x3) for single-chunk/test/SpawnSurfaceY callers"
  - "world/neighborhood.go: WorldGenLevel-like 3x3 read/write proxy with the 3 worldgen heightmaps wired into SetBlock"
  - "Worker staging map + single scheduler goroutine: holds carved chunks, auto-requests neighbors (bounded to one ring), decorates each wanted center over a Neighborhood once its 3x3 is carved, emits exactly once"
  - "GEN2-02 cross-chunk decoration seam, proven determinism-at-seams + emit-once + -race clean"
affects: [worldgen-features, worldgen-structures, decoration, phase-11]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Two-phase generation lifecycle (parallel terrain behind singleflight -> serial decoration on a single scheduler goroutine, handed off over a channel)"
    - "Single-owner-by-goroutine concurrency (staging/requested/wanted maps touched only by the scheduler -> no locks, -race clean by construction)"
    - "Bounded auto-request frontier (only externally-requested centers pull in a one-ring neighborhood; ring chunks never expand)"

key-files:
  created:
    - world/neighborhood.go
    - world/worker_seam_test.go
  modified:
    - world/generator.go
    - world/noisegen.go
    - world/worker.go
    - world/worker_test.go
    - world/noisegen_test.go

key-decisions:
  - "Split-Generate (not write-buffer): a staging map + single scheduler goroutine is the seam design (user-locked D1)"
  - "Decorate is a NO-OP body for Phase 10 (writes nothing, only promotes status); the seam + Neighborhood + heightmap wiring is the deliverable; emit-on-own-decoration with the late-write-after-emit rule DEFERRED to Phase 11+ (user-locked D2)"
  - "Generate stays a CONCRETE method (not on the interface, not a free function) so all ~13 existing concrete g.Generate call sites compile unchanged"
  - "handleTerrain routes on load PROVENANCE (fromRegion), not chunk Status — the scheduler mutates a staged generated chunk's Status to StatusFull, which a singleflight-shared concurrent waiter would otherwise misread as a region hit and double-emit"
  - "Auto-request frontier bounded to one ring (wanted set) — an unbounded ring expansion (every carved chunk requesting its own neighbors) floods the worker"

patterns-established:
  - "Parallel->serial channel handoff (w.carved) mirrors the existing single-owner ChunkResult discipline"
  - "decorated flag + all-9-carved scan guard guarantees exactly-once emit independent of request order"

requirements-completed: [GEN2-02]

# Metrics
duration: ~75min
completed: 2026-06-25
---

# Phase 10 Plan 03: Cross-Chunk Worker Seam (GEN2-02) Summary

**Split the Generator into a pure single-chunk GenerateTerrain + a 3x3 Decorate pass, grew the Worker a staging map + single scheduler goroutine that holds carved chunks until their 8 neighbors carve then decorates + emits each exactly once — proven determinism-at-seams + emit-once + Docker -race clean.**

## Performance

- **Duration:** ~75 min
- **Tasks:** 4 (executed as 4 atomic commits)
- **Files modified:** 7 (2 created, 5 modified)

## Accomplishments
- Split the `Generator` interface into `GenerateTerrain(pos)` (pure, fill->surface->carve, status `carvers`) + `Decorate(view *Neighborhood)` (post-carve 3x3 pass) + `Dims()`, with `Generate(pos)` retained as a concrete method on both impls.
- Built `world/neighborhood.go` — the WorldGenLevel-like 3x3 read/write proxy clipped to the 3x3, with `SetBlock` wiring `level.HeightmapUpdate` on the 3 worldgen heightmaps via self-built predicates (`block.IsAir` + resolved water StateID).
- Grew the `Worker` a staging map + a single scheduler goroutine that holds carved chunks, auto-requests the (bounded one-ring) neighborhood, decorates each externally-requested center over a `Neighborhood` once its 3x3 is carved, and emits each exactly once — all single-owner, no locks.
- Migrated the existing world test suite to the split interface (countingGen + the re-scoped concurrent-same-key dedup test + the one retargeted interface-typed Generate site) and added the GEN2-02 acceptance suite (reorder-identical, emit-once, neighborhood-completion).
- Verified the seam is determinism-at-seams correct, emits each center exactly once, and is Docker `-race -count=10` clean.

## Task Commits

Each task was committed atomically:

1. **Task 1: Split the Generator interface + Neighborhood proxy** - `c96df1f9` (feat)
2. **Task 2: Worker staging map + scheduler goroutine + handleTerrain split** - `2930e714` (feat)
3. **Task 4: Migrate existing world tests to the split interface** - `6f306f5a` (test)
4. **Task 3: GEN2-02 acceptance suite + double-emit / unbounded-frontier fixes** - `2e5575f5` (test)

_(Task 4 was executed before Task 3 so the package compiled and the seam tests could run; both committed atomically.)_

**Plan metadata:** _(this docs commit)_

## Files Created/Modified
- `world/generator.go` - Split Generator interface (GenerateTerrain/Decorate/Dims) + the shared `decorateSingle` helper + Superflat's split impl + concrete Generate + packPos reuse via chunkKey.
- `world/noisegen.go` - NoiseGenerator split at the carve boundary: FILL/SURFACE/CARVE in GenerateTerrain (status carvers), the FINISH tail (worldgen heightmaps, sky light, promote-to-full) in Decorate; SpawnSurfaceY retargeted to GenerateTerrain.
- `world/neighborhood.go` (NEW) - The 3x3 WorldGenLevel-like proxy + the 3 self-built worldgen-heightmap predicates wired into SetBlock.
- `world/worker.go` - carved channel + staging/requested/wanted maps + runScheduler/tryDecorate/requestNeighbors/emit + the handleTerrain split + the loadResult provenance discriminator.
- `world/worker_test.go` - countingGen migrated to the split interface; TestWorkerSingleflightDedup re-scoped to concurrent same-key dedup; TestWorkerEmitsResult timeout bumped.
- `world/noisegen_test.go` - The one interface-typed gen.Generate site retargeted to concrete g.Generate.
- `world/worker_seam_test.go` (NEW) - The GEN2-02 acceptance suite (reorder-identical, emit-once, neighborhood-completion).

## Decisions Made
- **Split-Generate over write-buffer** (user-locked D1): a staging map + single scheduler goroutine is the seam; terrain stays parallel behind singleflight, the parallel->serial handoff is a channel.
- **No-op Decorate for Phase 10** (user-locked D2): Decorate writes nothing and only promotes the center to StatusFull; the Neighborhood proxy + heightmap-update wiring is built for the feature phase but never called. Emit-on-own-decoration is safe by construction (nothing written after emit); the strict late-write-after-emit rule is DEFERRED to Phase 11+.
- **Generate stays a concrete method** (not on the interface, not a free function): preserves all ~13 existing concrete `g.Generate(pos)` call sites; only the single interface-typed `var gen Generator = g; gen.Generate(...)` site was retargeted.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Double-emit from singleflight-shared chunk pointer mutation**
- **Found during:** Task 3 (writing the emit-once acceptance test, which failed reproducibly)
- **Issue:** `handleTerrain` routed region-hit vs generated on the chunk's `Status == StatusFull`. The scheduler mutates a STAGED (generated) chunk's `Status` to `StatusFull` during decoration, and that exact pointer is what `singleflight` caches under the pos key. A concurrent same-key `handleTerrain` waiter (a directly-requested pos that is ALSO auto-requested) read `Status == Full` on a generated chunk and emitted it directly — a second, un-staged emit (threat T-10-08).
- **Fix:** Route on load PROVENANCE (`fromRegion`) not status. Added `loadOrGenerateEx` returning `(ch, fromRegion, err)` + a `loadResult` singleflight payload carrying `fromRegion` across waiters. `loadOrGenerate` keeps its `(ch, err)` signature for the existing linear tests via a thin wrapper. Also guarded re-carve of an already-decorated staged chunk (never resurrect the emitted single-owner copy).
- **Files modified:** world/worker.go
- **Verification:** TestEmitOnce passes -count=3 + the Docker -race -count=10 run is clean.
- **Committed in:** 2e5575f5

**2. [Rule 1 - Bug] Unbounded auto-request frontier expansion**
- **Found during:** Task 3 (the emit-once test converged in seconds with 11k+ chunks generated, frontier still growing)
- **Issue:** Every carved chunk auto-requested its 8 neighbors, and each ring chunk in turn auto-requested ITS neighbors — an unbounded outward expansion (11k+ chunks in 3s), flooding the bounded requests channel.
- **Fix:** Bound the frontier to one ring: only externally-`Request`ed centers (tracked via a scheduler-owned `wanted` set fed by a `wantedCh` channel) auto-request their 8 neighbors; ring chunks are generated solely to complete a wanted center's 3x3 and never expand or get decorated/emitted. This matches vanilla's "generate the 8 neighbors to decorate a wanted chunk" gating.
- **Files modified:** world/worker.go
- **Verification:** A 5x5 region now converges in ~127ms (vs unbounded); the reorder/emit-once/completion tests pass.
- **Committed in:** 2e5575f5

**3. [Rule 1 - Bug] TestWorkerEmitsResult 2s timeout flake**
- **Found during:** Final full-suite run (timed out at exactly 2.00s under the heavy noise-gen suite's CPU load)
- **Issue:** A single `Request(C)` now requires C's 8 neighbors to be terrain-generated before C decorates + emits (9 generations, not 1). The pre-existing 2s hard timeout flaked under CPU starvation in the full `-count=1` suite (the work itself completes in well under a second).
- **Fix:** Bumped the timeout to 10s with an explanatory comment.
- **Files modified:** world/worker_test.go
- **Verification:** Full `go test ./world/ -count=1` green (53s).
- **Committed in:** 2e5575f5

---

**Total deviations:** 3 auto-fixed (3 bugs, all in the new seam code, all surfaced by the acceptance suite)
**Impact on plan:** All three are correctness bugs in the seam being built (not scope creep). The provenance-discriminator + bounded-frontier fixes are required for the emit-once + convergence acceptance gates to hold. No interface, tick-side, or signature changes beyond the plan.

## Issues Encountered
- The double-emit was non-obvious because it occurred via the `handleTerrain` emit path (not `tryDecorate`), and a debug hook on `tryDecorate`'s emit masked it by perturbing timing. Root cause was identified by recognizing the singleflight-shared chunk pointer is mutated to `StatusFull` by the scheduler. Resolved by keying on load provenance.

## Threat Flags
None — no new security-relevant surface introduced. The seam is internal to the off-tick worker; no client input reaches it (positions are server-clamped column coords; neighbor auto-requests are internal + idempotent).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The cross-chunk decoration seam is built + proven (determinism-at-seams, emit-once, neighborhood-completion, -race clean). The feature phase (Phase 11+) inherits the `Neighborhood` 3x3 proxy with live worldgen-heightmap updates already wired into `SetBlock`.
- **Open Decision 2 (DEFERRED to Phase 11+):** the late-neighbor-write-after-emit rule. Phase 10's no-op Decorate makes emit-on-own-decoration safe by construction (nothing is written after emit). When features land and Decorate writes into neighbors, the feature phase must choose: vanilla-style re-send of an already-emitted chunk, or a stricter "hold C until every center touching C is decorated" rule. The seam documents this inline (tryDecorate + the Generator/Decorate doc comments).

## Self-Check: PASSED

- Created files exist: world/neighborhood.go, world/worker_seam_test.go, 10-03-SUMMARY.md
- Task commits exist: c96df1f9, 2930e714, 6f306f5a, 2e5575f5
- go build ./... clean; CGO_ENABLED=0 go build ./... clean; go vet ./world/... clean
- go test ./world/ -count=1 green (full suite, 53s)
- Docker -race -count=10 over TestDecorationReorderIdentical|TestEmitOnce: clean (ok, 11.5s)
- ChunkResult + NewWorker signature + server/tick.go + world/manager.go + cmd/sulfur/main.go unchanged

---
*Phase: 10-worldgen-foundation*
*Completed: 2026-06-25*
