---
phase: 09-stretch-online-worldgen-regions
plan: 05
subsystem: worldgen
tags: [aquifer, ore-veins, doFill, density-functions, minecraft-26.2, parity, go]

# Dependency graph
requires:
  - phase: 09-03
    provides: "the bound NoiseRouter (barrier/fluid_level_floodedness/fluid_level_spread/lava + vein_toggle/vein_ridged/vein_gap + erosion/depth + preliminary_surface_level) and RandomState (the positional random factory)"
  - phase: 09-04
    provides: "the NoiseChunk cell-sample + trilerp -> per-block final_density field (caves as negative density) + the provisional fill this replaces"
provides:
  - "world/levelgen/noisechunk/orevein.go — the ported OreVeinifier (vein_toggle sign -> COPPER/IRON, ridged/gap shaping, copper/iron ore-vein placement)"
  - "world/levelgen/noisechunk/aquifer.go — the ported NoiseBasedAquifer (computeSubstance + the aquifer-grid FluidStatus cache + barrier pressure + fluid-level/lava sampling): water/lava/air in carved regions"
  - "world/levelgen/noisechunk/fill.go — the real doFill (Fill + FillChunk): per-block density -> aquifer (non-solid) / ore-vein-or-stone (solid) + deepslate band + bedrock floor, replacing 09-04's provisional fill"
  - "NoiseChunk accessors (MinY/Height/SeaLevel/WorldX/WorldZ) + preliminarySurfaceLevel/maxPreliminarySurfaceLevel the aquifer consumes"
  - "RandomState.AquiferRandom()/OreRandom() — the vanilla-forked positional factories for the aquifer/vein randomness"
affects: [09-06-carvers, 09-07-surface, 09-08-generator]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Aquifer/OreVeinifier/Fill live in package noisechunk (NOT package levelgen) — the same import-cycle relocation 09-03/09-04 used (levelgen -> density -> synth -> levelgen would cycle)"
    - "Per-grid-cell FluidStatus cache (aquiferCache + aquiferLocationCache) — the aquifer samples on a 16x12x16 grid, not per block (T-9-14)"
    - "doFill = the MaterialRuleList[aquiferBaseRule, oreVeinifier] first-non-null rule chain, ported as the blockState() helper"

key-files:
  created:
    - world/levelgen/noisechunk/orevein.go
    - world/levelgen/noisechunk/orevein_test.go
    - world/levelgen/noisechunk/aquifer.go
    - world/levelgen/noisechunk/aquifer_test.go
    - world/levelgen/noisechunk/fill.go
    - world/levelgen/noisechunk/fill_test.go
  modified:
    - world/levelgen/noisechunk/noisechunk.go
    - world/levelgen/router/router.go

key-decisions:
  - "Files placed in package noisechunk, not the plan's package levelgen — forced by the levelgen->density->synth->levelgen import cycle (same precedent as 09-03 router + 09-04 noisechunk)"
  - "Ported the FULL 26.2 aquifer (erosion/depth surface sampling + 13 SURFACE_SAMPLING probes + computeSurfaceLevel/RandomizedFluidSurfaceLevel/FluidType), not a simplified pre-1.18 aquifer — 26.2's NoiseBasedAquifer.class added the surface-aware floodedness path"
  - "shouldScheduleFluidUpdate (vanilla's flowing-water flag) omitted — it does not affect static block PLACEMENT, only fluid-tick scheduling; the substance return value is ported faithfully"
  - "Bedrock floor + deepslate band kept as the provisional stratification (matching 09-04) — the real RandomBedrockFloor + deepslate surface rules land in Wave 7; this keeps the column renderable"

patterns-established:
  - "Port-exact from javap -v ConstantValue attributes (the aquifer's X/Y/Z_SPACING, SAMPLE_OFFSET, VeinType y-ranges read literally, not guessed)"
  - "fluidResult() maps the aquifer substance to (state, isFluid): a real fluid wins, air -> (0,false) so the fill keeps default air"

requirements-completed: [PARITY-01]

# Metrics
duration: 20min
completed: 2026-06-24
---

# Phase 9 Plan 05: Aquifer + OreVeinifier + the real doFill Summary

**Ported the 26.2 NoiseBasedAquifer (cave water/lava/air via the barrier/fluid-level/lava noises over a 16x12x16 aquifer grid) and the OreVeinifier (copper/iron veins via vein_toggle/ridged/gap), then wired the real doFill rule chain that turns 09-04's per-block density field into cave-complete terrain — caves flood with water below the local table, get lava deep, stay dry-air above, and the rock carries ore veins.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-06-24T21:11:00Z (approx, after 09-04 docs)
- **Completed:** 2026-06-24T21:31:00Z
- **Tasks:** 3 (all TDD: RED -> GREEN)
- **Files modified:** 8 (6 created, 2 modified)

## Accomplishments

- **OreVeinifier** (`orevein.go`): ported `OreVeinifier.calculate` constant-for-constant — `vein_toggle` sign selects COPPER (y 0..50) / IRON (y -60..-8); the 0.4 veininess threshold + edge roundoff, 0.7 solidness, `vein_ridged >= 0` reject, richness `clampedMap(0.4..0.6 -> 0.1..0.3)`, and `vein_gap > -0.3 && nextFloat < 0.02 -> raw block` else ore else filler. VeinType block ids read from `OreVeinifier$VeinType.class` (copper_ore/raw_copper_block/granite; deepslate_iron_ore/raw_iron_block/tuff).
- **NoiseBasedAquifer** (`aquifer.go`): ported the 16KB `computeSubstance` — finds the 4 closest aquifer-grid centers by squared distance, caches each cell's `FluidStatus` (the floodedness/spread-noise-driven local water table + deep lava), and interpolates the `barrier_noise` pressure (`calculatePressure`) between the closest aquifers to pick water/lava/air. Ported `computeFluid` + the 13 `SURFACE_SAMPLING_OFFSETS_IN_CHUNKS` probes, `computeSurfaceLevel` (deep-dark check + floodedness blend), `computeRandomizedFluidSurfaceLevel`, and `computeFluidType` (lava when surfaceLevel<=-10 && |lava_noise|>0.3). The global FluidPicker (lava@-54, water@63, air@MIN_Y*2) ported from `createFluidPicker`.
- **The real doFill** (`fill.go`): `Fill` + `FillChunk` port `NoiseBasedChunkGenerator.doFill` + the `MaterialRuleList[aquiferBaseRule, oreVeinifier]` first-non-null chain — per block: density>0 -> ore-vein-or-stone (deepslate band at/below y=0), density<=0 -> aquifer water/lava/air, bedrock floor at minY. Replaces `FillProvisional` (now `Deprecated`).
- **Verified cave-complete**: an 81-chunk scan shows caves flood with water (67741 blocks), have dry air pockets (118867), deep lava lakes (52, intentionally rare like vanilla), and ore veins present.

## Task Commits

1. **Task 1: Port the OreVeinifier** — `df23c001` (test) -> `52661dfd` (feat)
2. **Task 2: Port the NoiseBasedAquifer** — `b28f3709` (test) -> `1f73b18b` (feat)
3. **Task 3: Wire the real doFill** — `ca0570aa` (test) -> `bfd0f380` (feat)

_All TDD: failing test committed first (RED), then the implementation (GREEN)._

## Files Created/Modified

- `world/levelgen/noisechunk/orevein.go` — the ported OreVeinifier (`NewOreVeinifier` + `vein(x,y,z)`); VeinType COPPER/IRON.
- `world/levelgen/noisechunk/aquifer.go` — the ported NoiseBasedAquifer (`NewAquifer` + `computeSubstance` + the grid/FluidStatus/pressure/surface-level chain).
- `world/levelgen/noisechunk/fill.go` — `Fill` (the doFill loop) + `FillChunk` (renderable chunk), replacing FillProvisional.
- `world/levelgen/noisechunk/{orevein,aquifer,fill}_test.go` — the 11 TDD tests.
- `world/levelgen/noisechunk/noisechunk.go` — added MinY/Height/SeaLevel/WorldX/WorldZ accessors + preliminarySurfaceLevel/maxPreliminarySurfaceLevel; stored the router; FillProvisional marked Deprecated.
- `world/levelgen/router/router.go` — added `RandomState.AquiferRandom()`/`OreRandom()` (the vanilla-forked positional factories).

## Decisions Made

- **Package placement (noisechunk, not levelgen):** the plan named the files under package `levelgen`, but the aquifer/orevein consume `router.NoiseRouter` (density.Function) + `noisechunk.NoiseChunk`, which would create the `levelgen -> density -> synth -> levelgen` import cycle. Placed them in package `noisechunk` — the identical relocation 09-03 (router) and 09-04 (noisechunk) already made.
- **Full 26.2 aquifer:** 26.2's `NoiseBasedAquifer` adds an erosion/depth surface-aware floodedness path (`computeFluid` scans 13 chunk-section probes + `isDeepDarkRegion`) absent in pre-1.18 versions. Ported it in full rather than a simplified placeholder, so the perched water tables + deep lava match vanilla.
- **`shouldScheduleFluidUpdate` omitted:** it is a flowing-water bookkeeping flag, irrelevant to static block placement; the substance return value (the only thing the fill consumes) is ported faithfully.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Files placed in package noisechunk, not package levelgen**
- **Found during:** Task 1 (before writing orevein.go)
- **Issue:** The plan's paths (`world/levelgen/aquifer.go` etc., package levelgen) would import `router`+`density`, recreating the `levelgen -> density -> synth -> levelgen` import cycle the project already broke in 09-03/09-04 by relocating router and noisechunk to subpackages.
- **Fix:** Placed `aquifer.go`/`orevein.go`/`fill.go` in `world/levelgen/noisechunk/` (package noisechunk), the consumer package — `go build ./...` is clean. Behaviour and public API are identical; only the import path differs.
- **Files modified:** the three new files live under noisechunk/.
- **Verification:** `go build ./...` exits 0; the full world suite + Docker -race pass.
- **Committed in:** `52661dfd`, `1f73b18b`, `bfd0f380` (the task commits).

**2. [Rule 3 - Blocking] Added RandomState.AquiferRandom()/OreRandom() + NoiseChunk surface-level accessors**
- **Found during:** Tasks 1 & 2
- **Issue:** The aquifer needs the per-position random factory + the preliminary surface level; RandomState's factory and NoiseChunk's geometry were private with no accessor.
- **Fix:** Added `RandomState.AquiferRandom()`/`OreRandom()` (forking `base.fromHashOf("minecraft:aquifer"/"ore").forkPositional()` exactly as vanilla RandomState.<init> does) and `NoiseChunk.MinY/Height/SeaLevel/WorldX/WorldZ` + `preliminarySurfaceLevel`/`maxPreliminarySurfaceLevel` (ported from NoiseChunk.class).
- **Files modified:** `world/levelgen/router/router.go`, `world/levelgen/noisechunk/noisechunk.go`.
- **Verification:** determinism tests pass (same seed -> same world); -race clean.
- **Committed in:** `52661dfd`, `1f73b18b`.

**3. [Rule 1 - Test correctness] TestAquiferPerchedAndLava scan widened**
- **Found during:** Task 2 (GREEN)
- **Issue:** The initial RED test scanned only 5 chunks for deep lava; vanilla lava lakes are sparse (~50 blocks / 80 chunks), so the assertion flaked. The port was correct — the test was too strict.
- **Fix:** Widened the scan to a 9x9 chunk area (with an early break) and assert water + air + lava all appear; confirmed via an 81-chunk debug scan that lava genuinely occurs.
- **Files modified:** `world/levelgen/noisechunk/aquifer_test.go`.
- **Verification:** the test passes deterministically.
- **Committed in:** `1f73b18b` (Task 2 commit).

---

**Total deviations:** 3 auto-fixed (2 blocking, 1 test correctness)
**Impact on plan:** All necessary — the package relocation + accessors are mechanical (the import cycle was a known constraint from 09-03/09-04), and the test-scan fix corrected an over-strict assertion, not the port. No scope creep; go.mod/go.sum unchanged (zero new deps).

## Issues Encountered

- The 26.2 aquifer is materially more complex than older versions (surface-aware floodedness via erosion/depth + the 13-probe surface scan + `maxPreliminarySurfaceLevel`). Resolved by reading the full `Aquifer$NoiseBasedAquifer.class` bytecode (dumped to scratch) method-by-method and porting `computeFluid`/`computeSurfaceLevel`/`computeRandomizedFluidSurfaceLevel`/`computeFluidType` faithfully, plus adding the `preliminarySurfaceLevel`/`maxPreliminarySurfaceLevel` accessors NoiseChunk lacked.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- The terrain is now CAVE-COMPLETE: caves flood with water/lava/air via the real aquifer, the rock carries copper/iron veins, with stone/deepslate/bedrock stratification.
- **Wave 6 (carvers)** runs ConfiguredWorldCarver on top of this filled terrain (the aquifer is passed to the carvers in vanilla — `FillChunk` exposes the aquifer-aware fill they carve into).
- **Wave 7 (surface)** applies the surface rules (grass/dirt/sand + the real deepslate/bedrock floor) over this fill.
- **Wave 8 (generator)** drives `FillChunk` from the chunk Generator.
- No blockers. Zero new deps; CGO_ENABLED=0 clean; Docker -race over ./world/... clean.

## Self-Check: PASSED

- All 6 created files exist on disk (orevein/aquifer/fill .go + their _test.go).
- All 6 task commits exist (3 TDD test->feat pairs).
- `go build ./...` (CGO_ENABLED=0) exits 0; `go vet ./...` clean.
- All 11 plan-named tests pass; full `./world/...` suite green; Docker `-race ./world/...` clean.
- go.mod/go.sum unchanged (zero new deps); no out-of-scope files touched (generator/worker/cmd/server untouched).

---
*Phase: 09-stretch-online-worldgen-regions*
*Completed: 2026-06-24*
