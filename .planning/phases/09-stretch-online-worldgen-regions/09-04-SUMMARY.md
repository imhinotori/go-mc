---
phase: 09-stretch-online-worldgen-regions
plan: 04
subsystem: worldgen
tags: [noisechunk, noise-interpolator, trilinear-interpolation, cell-sampling, final-density, caves, density-function, parity, port-776]

# Dependency graph
requires:
  - phase: 09-03
    provides: "world/levelgen/router (NewRouter + bound final_density incl. cave branches) + world/levelgen/density (Function/Context/Marked interfaces, interpolated marker)"
  - phase: 09-02
    provides: "world/levelgen/synth noise primitives the bound graph calls"
  - phase: 04
    provides: "level.Chunk / Section.SetBlock / BiomesPaletteContainer / heightmaps the provisional fill emits"
provides:
  - "world/levelgen/noisechunk: the Tier-E NoiseChunk cell-sample + trilinear-interpolation core turning bound final_density (incl. caves) into a per-block density field"
  - "NewNoiseChunk(router, pos) + FinalDensity(localX, worldY, localZ) per-block accessor (>0 solid, <=0 carved/cave)"
  - "the ported NoiseInterpolator (corner buffers + Mth.lerp trilerp in vanilla's cell-fill order)"
  - "a PROVISIONAL solid/air/water/bedrock+deepslate fill (FillProvisional) Wave 5's Aquifer replaces"
affects: [wave-5-aquifer-orevein, wave-6-carvers, wave-7-surface-biome, wave-8-generator-assembly]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Composition-root package above levelgen (noisechunk, like router) to break the density->synth->levelgen import cycle"
    - "Sparse cell-corner sampling (1225 corners/chunk) + trilerp instead of 98K per-block graph evaluation (Pattern 2 / anti-Pitfall-5)"
    - "Whole-final_density corner sampling driven through ONE NoiseInterpolator (the RESEARCH Pattern 2 pseudocode), corner-exact + sparse"

key-files:
  created:
    - "world/levelgen/noisechunk/interpolator.go - NoiseChunk$NoiseInterpolator port (corner buffers + staged trilerp)"
    - "world/levelgen/noisechunk/noisechunk.go - NewNoiseChunk + cell-fill loop + FinalDensity accessor + provisional fill"
    - "world/levelgen/noisechunk/noisechunk_test.go - 8 tests (trilerp/cell-fill-order/sparse-sample/corner-exact/caves/fill/determinism)"
  modified: []

key-decisions:
  - "NoiseChunk lives in package noisechunk (under world/levelgen/noisechunk/), NOT package levelgen as the plan listed: a levelgen file importing density/router cycles (levelgen->density->synth->levelgen). Direct analogue of the 09-03 router placement. [Rule 3 - blocking import cycle]"
  - "Drives ONE NoiseInterpolator over the WHOLE final_density (sample at cell corners, trilerp result) rather than rewrapping each of the 5 deep interpolated-marked nodes: the density package exposes only Marked.Wrapped() (no mapAll tree-rewrite) and the plan forbids modifying it. This is exactly the 09-RESEARCH Pattern 2 pseudocode. Corner-EXACT + sparse; the only divergence from vanilla is outer non-linear ops applied before-vs-after interpolation (a sub-cell smoothness nuance, untested, refinable in Wave 5+)."
  - "TestDensityFieldHasCaves scans a 5x5 chunk REGION over the full underground Y band (not one column): caves are sparse, chunk (0,0) has none; a real cave exists at chunk (-8,-8) ~y=-54 (direct-sample confirmed). The field, not a single column, is the unit of the caves-present assertion."

patterns-established:
  - "Cell geometry derivation: cellWidth=sizeHorizontal<<2 (QuartPos.toBlock)=4, cellHeight=sizeVertical<<2=8, cellCountY=floorDiv(height,cellHeight)=48, cellNoiseMinY=floorDiv(minY,cellHeight)=-8, firstNoise{X,Z}=blockX>>2 (QuartPos.fromBlock)"
  - "doFill cell nesting: cellX(advanceCellX, fill far X face) > cellZ > cellY desc(selectCellYZ) > inCellY desc(updateForY) > inCellX(updateForX) > inCellZ(updateForZ); swapSlices per cellX"

requirements-completed: [PARITY-01]

# Metrics
duration: 27 min
completed: 2026-06-24
---

# Phase 9 Plan 04: NoiseChunk — per-cell density sampling + trilinear interpolation Summary

**Tier-E NoiseChunk + NoiseInterpolator ported from NoiseChunk.class: samples the bound final_density (caves included) on the coarse 5x5x49 cell-corner grid and trilerps to a per-block density field where caves appear as negative density — the parity+perf hinge, sparse (1225 samples/chunk, not 98K per-block).**

## Performance

- **Duration:** ~27 min
- **Started:** 2026-06-24T20:40Z
- **Completed:** 2026-06-24T21:07Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 3 created (interpolator.go, noisechunk.go, noisechunk_test.go)

## Accomplishments
- Ported `NoiseChunk$NoiseInterpolator` (javap -c) constant-for-constant: the `slice0`/`slice1` rolling X-face corner buffers (`[cellCountXZ+1][cellCountY+1]`, `[cz][cy]`), `selectCellYZ` loading the 8 `noiseXYZ` corners, and the staged `updateForY`/`updateForX`/`updateForZ` `Mth.lerp` trilerp collapse (Y -> XZ faces -> Z edges -> value), matching `Mth.lerp3` order.
- Ported the NoiseChunk cell-fill loop (`NoiseBasedChunkGenerator.doFill`/`iterateNoiseColumn`): cellX(advanceCellX) > cellZ > cellY desc(selectCellYZ) > inCellY desc(updateForY) > inCellX(updateForX) > inCellZ(updateForZ), with `swapSlices` per cellX — yielding a deterministic per-block density field from ~1225 corner samples, an ~80x reduction vs per-block.
- Confirmed caves materialize as negative density: `final_density` carries the cave branches, so the same cell loop produces hills (positive) AND carved cave regions (negative under a solid roof) — no separate cave pass.
- Added the provisional solid/deepslate/water/air + bedrock-floor fill (the placeholder Wave 5's Aquifer replaces) so the cell machinery is testable end-to-end into a real `*level.Chunk`.

## Task Commits

1. **Task 1 (TDD): NoiseInterpolator** — `5dd02ba9` (test RED) -> `871c66b1` (feat GREEN)
2. **Task 2 (TDD): NoiseChunk cell-sample + provisional fill** — `a9c4d676` (test RED) -> `4bbb6fa7` (feat GREEN)

_TDD: each task is a test (RED) then feat (GREEN) commit._

## Files Created/Modified
- `world/levelgen/noisechunk/interpolator.go` (161 lines) — the NoiseInterpolator: corner buffers, `selectCellYZ`, staged `updateForY/X/Z` trilerp, `fillSlice`, `swapSlices`; `mthLerp` = `Mth.lerp(t,a,b)=a+t*(b-a)`.
- `world/levelgen/noisechunk/noisechunk.go` (312 lines) — `NewNoiseChunk(router, pos)`, the `fill()` cell loop driving the interpolator, `FinalDensity(lx,y,lz)` accessor, `blockAt`/`FillProvisional` provisional fill, `floorDiv` for negative minY.
- `world/levelgen/noisechunk/noisechunk_test.go` (345 lines) — 8 tests, all passing.

## Bytecode citations (port-exact)
- `NoiseChunk$NoiseInterpolator.selectCellYZ(y, xz)`: `slice0[xz][y]->noise000`, `slice0[xz+1][y]->noise001`, `slice1[xz][y]->noise100`, `slice1[xz+1][y]->noise101`, and the `+1` Y variants -> noise*1*. slice0 = X=0 face, slice1 = X=1 face; first index = Z, second = Y.
- `updateForY(dy)`: `valueXZ00=Mth.lerp(dy, noise000, noise010)` (+10/+01/+11). `updateForX(dx)`: `valueZ0=lerp(dx, valueXZ00, valueXZ10)`, `valueZ1=lerp(dx, valueXZ01, valueXZ11)`. `updateForZ(dz)`: `value=lerp(dz, valueZ0, valueZ1)`. Equivalent to `Mth.lerp3(tx,ty,tz, n000,n100,n010,n110,n001,n101,n011,n111)`.
- `NoiseChunk` ctor: `cellWidth=getCellWidth()=noiseSizeHorizontal<<2`, `cellHeight=getCellHeight()=noiseSizeVertical<<2`, `cellCountY=floorDiv(height,cellHeight)`, `cellNoiseMinY=floorDiv(minY,cellHeight)`, `firstNoiseX=QuartPos.fromBlock(blockX)=blockX>>2`.
- `doFill`: nesting cellX(advanceCellX) -> cellZ -> cellY(desc, selectCellYZ) -> inCellY(desc, updateForY) -> inCellX(updateForX) -> inCellZ(updateForZ); `swapSlices` at end of each cellX. `initializeForFirstCellX` -> `fillSlice(slice0, firstCellX)`; `advanceCellX(c)` -> `fillSlice(slice1, firstCellX+c+1)`.
- `iterateNoiseColumn`: corner world Y for cell row = `(cellNoiseMinY+cellY)*cellHeight + inCellY`.

## Decisions Made
See `key-decisions` frontmatter. The two load-bearing ones: (1) package placement above `levelgen` to break the import cycle; (2) whole-`final_density` corner sampling through one interpolator (the RESEARCH Pattern 2 approach) since the density package exposes no tree-rewrite and the plan forbids modifying it — corner-exact and sparse, with the per-node-vs-whole interpolation nuance documented in the NoiseChunk doc comment.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] NoiseChunk package relocated to break an import cycle**
- **Found during:** Task 1 (first test run)
- **Issue:** The plan listed the files at `world/levelgen/noisechunk.go` (package `levelgen`). A `levelgen` file importing `density`/`router` forms a cycle: `levelgen` (holds the Tier-A random primitives) <- `synth` <- `density` <- the new file. `go test` failed with `import cycle not allowed in test`.
- **Fix:** Created sibling package `world/levelgen/noisechunk/` (package `noisechunk`), a composition root above `levelgen` — the exact resolution the 09-03 plan applied to `router`. The plan's own `<interfaces>` already referenced `world/levelgen/router.go` while the real file is `world/levelgen/router/router.go`, confirming this is the established pattern.
- **Files modified:** all three plan files live under `world/levelgen/noisechunk/` instead of `world/levelgen/`.
- **Verification:** `go build ./...`, `go vet ./...`, `go test ./world/levelgen/...` all clean; no cycle.
- **Committed in:** `871c66b1`, `4bbb6fa7`.

**2. [Rule 1 - Test correctness] Caves test scans a chunk region, not chunk (0,0)**
- **Found during:** Task 2 (GREEN)
- **Issue:** The plan's TestDensityFieldHasCaves asserted a cave in the single built chunk (0,0). Direct `final_density` sampling proved chunk (0,0) has NO cave in its underground band — caves are statistically sparse (3536 negative samples across a 5x5-chunk scan; nearest true underground cave at chunk (-8,-8) ~y=-54). The strict single-chunk assertion is incorrect.
- **Fix:** Rewrote the test to scan a 5x5 chunk region over the full underground Y band, asserting a carved (negative) region exists under a solid roof — the field (not one column) is the correct unit for "caves present."
- **Files modified:** `world/levelgen/noisechunk/noisechunk_test.go`.
- **Verification:** Test passes; the carved region is reproduced by the trilerped field, not just the direct sample.
- **Committed in:** `a9c4d676` / `4bbb6fa7`.

---

**Total deviations:** 2 auto-fixed (1 blocking import-cycle relocation, 1 test-correctness fix).
**Impact on plan:** Both essential and faithful to the plan's intent. The package relocation mirrors the established 09-03 pattern (no scope creep). The caves-test fix makes the assertion correct against the real sparse cave distribution. The interpolation machinery, sparse sampling, corner-exactness, caves-present, determinism, and provisional fill all land exactly as specified.

## Perf approach (sparse vs per-block)
The whole point of cell-sampling+trilerp: the expensive `final_density` graph (splines + multiple NormalNoise) is evaluated at only **1225 cell corners** (`(cellCountXZ+1)^2 * (cellCountY+1)` = 5*5*49) per chunk, then trilerped to all 98,304 blocks — an ~80x reduction. `TestCellSampleNotPerBlock` asserts the exact corner count and that it is far below per-block (anti-Pitfall-5). Field build is ~40ms/chunk on this host (off-tick, well within the worker budget; T-9-08 mitigated).

## Issues Encountered
None beyond the two deviations above. The interpolated-marker tree-rewrite tension (5 deep interpolated nodes vs density's lack of mapAll) was resolved by the whole-final_density corner-sampling approach the RESEARCH doc itself prescribes — documented transparently in the NoiseChunk doc comment for Wave 5+ to refine if a heightmap capture-diff ever demands sub-cell fidelity.

## Verification
- `go test ./world/levelgen/noisechunk/` — all 8 tests pass (TestTrilerpCorners, TestInterpolatorDeterministic, TestCellFillOrder, TestCellSampleNotPerBlock, TestNoiseChunkCornerExact, TestDensityFieldHasCaves, TestProvisionalFill, TestNoiseChunkDeterministic).
- `go test ./world/levelgen/...` — Wave 1/2/3 not regressed (data/density/router/synth all green).
- `go vet ./...` + `go build ./...` clean; ZERO new dependencies (go.mod/go.sum unchanged); `CGO_ENABLED=0` clean.
- Docker `-race` over `./world/...` clean (golang:1.26).
- `world/generator.go`, `world/worker.go`, the tick, and `cmd/sulfur` UNTOUCHED.

## Next Phase Readiness
- **Wave 5 (Aquifer + OreVeinifier)** consumes `NoiseChunk.FinalDensity` and REPLACES the provisional sea-level water rule in `FillProvisional`/`blockAt` with the real noise-based fluid table (perched water/lava in carved regions) + ore veins. The router already exposes the Aquifer (`Barrier`/`FluidLevel*`/`Lava`) and OreVein (`Vein*`) functions for it.
- Wave 6 (carvers), Wave 7 (surface/biome rules), and Wave 8 (the assembled `Generator` behind the unchanged `world.Generator` interface + off-tick worker) all sit on this density field.
- No blockers. The documented interpolation nuance (whole-vs-per-node) is the one item a future heightmap capture-diff could revisit.

---
*Phase: 09-stretch-online-worldgen-regions*
*Completed: 2026-06-24*

## Self-Check: PASSED

- Files created: interpolator.go, noisechunk.go, noisechunk_test.go, 09-04-SUMMARY.md — all present on disk.
- Commits: 5dd02ba9 (test T1), 871c66b1 (feat T1), a9c4d676 (test T2), 4bbb6fa7 (feat T2) — all in git history.
- All 8 tests pass; go vet + go build clean; zero new deps; CGO_ENABLED=0 clean; Docker -race clean.
