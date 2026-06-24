---
phase: 04-world-chunk-system
plan: 01
subsystem: world-chunk
tags: [protocol-776, chunk, level-chunk-section, paletted-container, heightmaps, wire-format]

# Dependency graph
requires:
  - phase: 02-protocol-foundation
    provides: go-mc fork level/chunk.go codec (Section, PaletteContainer, heightMapEntry, lightData) retargeted toward proto 776
provides:
  - "Section per-section fluid-count short (two-short LevelChunkSection.write layout): pk.Short(BlockCount) then pk.Short(FluidCount) before the states container, read symmetrically"
  - "Chunk.WriteTo restricted to the 3 Usage.CLIENT heightmaps (WORLD_SURFACE id 1, MOTION_BLOCKING id 4, MOTION_BLOCKING_NO_LEAVES id 5)"
  - "Native cheap-regression tests locking the two-short layout, the exact section byte length, and the 3-heightmap set"
affects: [04-02-world-package, 04-03-chunk-streaming, 04-04-vanilla-capture-diff]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Section wire = nonEmptyBlockCount short + fluidCount short + states PaletteContainer (no length prefix, LSB longs) + biomes PaletteContainer"
    - "Heightmap WRITE set = sendToClient (Usage.CLIENT) ids only; ReadFrom stays permissive to ids 0..5"
    - "Self-round-trip + golden byte-length are necessary-not-sufficient regression; vanilla-correctness proof deferred to capture-diff (04-04)"

key-files:
  created:
    - level/chunk_test.go
  modified:
    - level/chunk.go

key-decisions:
  - "FluidCount defaults to 0 for fluid-free (all-stone superflat) chunks; computing a correct non-zero count is a later-phase concern. The presence of the short is what fixes the byte-misalignment."
  - "Chunk.ReadFrom heightmap switch left permissive (accepts ids 0..5); only the WRITE set was trimmed, keeping round-trip and any future inbound decode working."
  - "Heightmap test decodes only the leading pk.Array(hmEntries) prefix of the chunk body to assert the exact set {1,4,5} directly."

patterns-established:
  - "Two-short section header locked by TestSectionByteLength (encoded length == 4 shorts-bytes + states + biomes, computed from the real container encoders, not hard-coded)"
  - "Symmetric round-trip tests explicitly documented as cheap regression, NOT the WORLD-02/03 correctness gate"

requirements-completed: [WORLD-02, WORLD-03]

# Metrics
duration: ~12min
completed: 2026-06-24
---

# Phase 4 Plan 01: Chunk Wire Fixes (fluid-count short + CLIENT heightmaps) Summary

**Fixed the two jar-confirmed proto-776 chunk byte-layout bugs in level/chunk.go: added the missing per-section fluid-count short (two-short LevelChunkSection.write header) and trimmed Chunk.WriteTo to the 3 Usage.CLIENT heightmaps — locked by native round-trip, golden byte-length, and heightmap-set regression tests.**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-06-24T01:03Z (approx)
- **Completed:** 2026-06-24T01:15Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 2 (1 modified, 1 created)

## Accomplishments
- **WORLD-02 (partial):** `Section` gains a `FluidCount int16` field; `Section.WriteTo` writes `pk.Short(BlockCount)` THEN `pk.Short(FluidCount)` before the states container, and `Section.ReadFrom` reads both symmetrically — fixing the confirmed stripes/void byte-misalignment caused by the missing second short.
- **WORLD-03:** `Chunk.WriteTo` now emits exactly the 3 `sendToClient` (Usage.CLIENT) heightmaps — WORLD_SURFACE (1), MOTION_BLOCKING (4), MOTION_BLOCKING_NO_LEAVES (5) — dropping the WORLDGEN/LIVE_WORLD ids 0/2/3.
- Added `level/chunk_test.go` with `TestSectionRoundTrip`, `TestSectionByteLength`, and `TestChunkHeightmapsClientSet` as cheap native regression.
- Verified under `-race` in the `golang:1.26` Docker image (host cgo unavailable); plain build/vet/test green natively.

## New Section wire layout (after this plan)

Per section, in order:

1. `pk.Short(BlockCount)`  — nonEmptyBlockCount (2 bytes)
2. `pk.Short(FluidCount)`  — fluidCount (2 bytes) **← the fix; 0 for fluid-free chunks but always present**
3. `s.States`  — states `PaletteContainer` (UnsignedByte bits + palette + raw LSB longs, **no VarInt length prefix**)
4. `s.Biomes`  — biomes `PaletteContainer` (same no-length-prefix LSB form)

Per-section byte length grows by exactly 2 (the fluid-count short). `PaletteContainer`, `BitStorage`, `heightMapEntry`, and `lightData` were reused unchanged.

## Heightmap ids now sent (Chunk.WriteTo)

Exactly 3, in order: **1 (WORLD_SURFACE), 4 (MOTION_BLOCKING), 5 (MOTION_BLOCKING_NO_LEAVES)**. Dropped from the wire: 0 (WORLD_SURFACE_WG), 2 (OCEAN_FLOOR_WG), 3 (OCEAN_FLOOR). `Chunk.ReadFrom` remains permissive (still parses ids 0..5).

## Task Commits

Each task was committed atomically:

1. **Task 1: Add fluid-count short to Section + round-trip/byte-length regression** - `afc2c88c` (feat)
2. **Task 2: Trim Chunk.WriteTo to the 3 sendToClient heightmaps + golden set test** - `42ef2037` (feat)

_TDD note: the regression tests for both tasks were authored up front in `chunk_test.go` (RED — compile failure on `FluidCount`, then 6-vs-3 heightmap failure), then made GREEN by the respective code edits, then committed within each task's atomic commit._

## Files Created/Modified
- `level/chunk.go` - Added `FluidCount int16` to `Section`; second short in `Section.WriteTo`/`ReadFrom`; trimmed `Chunk.WriteTo` hmEntries to ids 1/4/5.
- `level/chunk_test.go` - `TestSectionRoundTrip` (symmetric BlockCount/FluidCount/states), `TestSectionByteLength` (length == 4 + states + biomes, computed from real encoders), `TestChunkHeightmapsClientSet` (exactly {1,4,5}).

## Decisions Made
- `FluidCount` defaults to 0 for fluid-free chunks; the byte-alignment fix is the presence of the short, not a correct non-zero count (deferred).
- Kept `Chunk.ReadFrom` permissive (ids 0..5) so only the WRITE set changed.
- Heightmap test decodes just the leading `pk.Array(hmEntries)` prefix to assert the set directly.

## Deviations from Plan

None - plan executed exactly as written. Both tasks followed the plan's exact edits and verify gates.

## Issues Encountered
- Native `go test -race` requires cgo (`CGO_ENABLED=1` + a C toolchain), which is not configured on the Windows host. Resolved by running the race-enabled regression in the `golang:1.26` Docker image (`MSYS_NO_PATHCONV=1 docker run -v //d/ender://src`), per the plan's runtime guidance — clean. Plain build/vet/test run natively.

## CRITICAL — correctness proof is deferred
A green self-round-trip here is **necessary but NOT sufficient**: the fork's `Section.ReadFrom`/`WriteTo` are symmetric, so a round-trip passes even on a byte-misaligned wire (this is exactly how the missing fluid-count short hid). `TestSectionByteLength` adds a golden-length guard against a dropped/extra short, and `TestChunkHeightmapsClientSet` locks the 3-heightmap set. The **authoritative WORLD-02/WORLD-03 correctness gate is the vanilla capture-diff in Plan 04-04.** Do not treat this plan as full vanilla-correctness proof.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The section wire and heightmap set are now vanilla-aligned at the byte level, ready for the `world/` package (04-02) to assemble and the chunk-streaming/capture-diff (04-03/04-04) to validate against a real vanilla server.
- No blockers. `go build ./...` and `go vet ./...` clean project-wide; full `level/...` test suite green natively and under `-race` in Docker.

## Self-Check: PASSED

- FOUND: level/chunk.go
- FOUND: level/chunk_test.go
- FOUND: .planning/phases/04-world-chunk-system/04-01-SUMMARY.md
- FOUND commit: afc2c88c (Task 1)
- FOUND commit: 42ef2037 (Task 2)

---
*Phase: 04-world-chunk-system*
*Completed: 2026-06-24*
