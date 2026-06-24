---
phase: 09-stretch-online-worldgen-regions
plan: 03
subsystem: worldgen
tags: [density-functions, noise-router, randomstate, spline, caves, parity, go-port, protocol-776]

# Dependency graph
requires:
  - phase: 09-01
    provides: the embedded worldgen graph as DATA (data.NoiseSettings/DensityFunction/Noise — overworld.json router + the 35-file density_function tree incl. caves + noise params)
  - phase: 09-02
    provides: the Tier-A Xoroshiro seeding chain (RandomSource/PositionalRandomFactory/NewXoroshiro/FromHashOf) + the Tier-B noise primitives (synth.NewNormalNoise/NewBlendedNoise)
provides:
  - "world/levelgen/density — the Function interface + Context + the FULL 29-type ported density-function node set + the cubic Spline + the data-driven Parse(json)->Function with a dedup Registry"
  - "world/levelgen/router — NoiseGeneratorSettings (parsed overworld.json) + NoiseRouter (all 15 bound named functions) + RandomState (the seed->noise binding bridge, implements density.NoiseBinder) + NewRouter(seed)"
  - "an evaluable, deterministic final_density (caves included as negative density) + barrier/fluid_level_*/lava (Aquifer inputs) + vein_* (OreVein inputs) + preliminary_surface_level (surface-rule input)"
affects: [09-04 noise-chunk cell sampling, 09-05 aquifer, 09-05 ore-veinifier, 09-06 carvers, 09-07 surface-rules, 09-07 biome-source]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DATA/LOGIC split: the wired graph SHAPE is parsed DATA (Wave 1 JSON); the node TYPES are ported LOGIC (each a pure Compute(ctx) from DensityFunctions$* bytecode)"
    - "Dedup parse Registry (HolderHolder): shared sub-graphs (the spline-heavy offset/factor/jaggedness, the cache wrappers) parsed once, returned as the same instance — a DAG not a re-parsed tree, with a cycle guard"
    - "NoiseBinder seam: the parser stays free of the seeding layer; RandomState binds noise nodes to their seeded NormalNoise via factory.FromHashOf(id) as the graph is walked (the determinism hinge)"
    - "Composition root above leaf packages: package router imports density/synth/levelgen; it cannot be package levelgen (cycle), so it lives one level up"

key-files:
  created:
    - world/levelgen/density/df.go
    - world/levelgen/density/nodes.go
    - world/levelgen/density/spline.go
    - world/levelgen/density/parse.go
    - world/levelgen/density/density_test.go
    - world/levelgen/router/router.go
    - world/levelgen/router/router_test.go
  modified: []

key-decisions:
  - "invert = 1.0/x, NOT -x (bytecode-verified from DensityFunctions$Mapped.transform ordinal INVERT) — corrects the plan's stated -x"
  - "interval_select uses the 26.2 functions[]+thresholds[] shape (N functions, N-1 thresholds), not the older value-threshold pair"
  - "find_top_surface is the upper_bound/density/cell_height/lower_bound column-scan node (scans top->down at cell_height for the first density>0); it IS preliminary_surface_level"
  - "router lives in package router (world/levelgen/router/), not package levelgen — a composition root importing density would cycle levelgen->density->synth->levelgen"
  - "blend_alpha=1.0 / blend_offset=0.0 / blend_density=identity pass-throughs (blending OFF, no neighbour-chunk blend) — correct only with blending disabled, documented"

patterns-established:
  - "Pattern: every node carries bytecode-derived MinValue/MaxValue so the Ap2 min/max short-circuits (which read the arg bounds) stay faithful"
  - "Pattern: the interpolated marker is preserved (Marked interface, kind=MarkerInterpolated) so Wave-4 NoiseChunk can detect cell-grid sampling; cache markers are transparent"

requirements-completed: [PARITY-01]

# Metrics
duration: 10min
completed: 2026-06-24
---

# Phase 9 Plan 03: Full Density-Function Node Set + Router/RandomState Summary

**The whole wired overworld noise_router (all 15 named functions, caves included as negative final_density) parses + evaluates deterministically — 29 density-function node types ported constant-for-constant from DensityFunctions$* (javap), a dedup data-driven parser, the cubic spline, and a RandomState that seeds every noise via FromHashOf(id).**

## Performance

- **Duration:** 10 min
- **Started:** 2026-06-24T20:26:31Z
- **Completed:** 2026-06-24T20:36:40Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 7 created

## Accomplishments

- **The FULL 29-type node set is ported** from the bytecode (javap -c, 26.2-inner.jar): the Mapped unary transforms (abs/square/cube/half_negative/quarter_negative/invert/squeeze), add/mul/min/max (Ap2 with the exact short-circuits + bounds), clamp, range_choice, interval_select, y_clamped_gradient, noise/shifted_noise/shift_a/shift_b, old_blended_noise, find_top_surface, the markers (interpolated preserved; flat_cache/cache_2d/cache_once transparent), and blend_alpha/blend_offset/blend_density. **All confirmed present incl. find_top_surface + invert.**
- **The cubic Spline** (CubicSpline$Multipoint.sample + linearExtend + findIntervalStart) ported exactly, recursive over nested value-splines, driven by a density-function coordinate.
- **The data-driven Parser + dedup Registry**: dispatches on `type` (bare number → constant, string → cached ref, object → node), resolves string refs recursively via `data.DensityFunction`, dedups shared sub-graphs, and **errors LOUDLY on an unsupported type** naming it (T-9-07; only `end_islands` deferred).
- **Tier-D router**: NoiseGeneratorSettings (parsed overworld.json), NoiseRouter (all 15 named functions bound), and RandomState (the seed→noise bridge implementing `density.NoiseBinder`). `NewRouter(seed)` parses + binds the WHOLE graph once.
- **Caves come free**: `TestFinalDensityEvaluable` finds negative-density carved cells underground; the cave functions (entrances/noodle/pillars/spaghetti_2d) parse + evaluate as ordinary noise/shifted_noise/range_choice/spline nodes — NOT weird_scaled_sampler.
- **Determinism proven** (same seed → identical samples) and **Docker `-race` clean** over both new packages.

## Task Commits

1. **Task 1: Port the full node set + parser** — `c40c2223` (feat) + `810356cc` (refactor: drop unused zeroFn)
2. **Task 2: Port Tier-D router/settings + RandomState** — `1138a636` (feat)

_Both tasks are TDD; node-port tasks land test+impl in one commit per node-port convention._

## Files Created/Modified

- `world/levelgen/density/df.go` — `Function` interface (Compute/MinValue/MaxValue), `Context`, the `Marked` marker-kind accessor (interpolated NOT a no-op).
- `world/levelgen/density/nodes.go` — the FULL 29-type ported node set; each Compute + MinValue/MaxValue bytecode-cited.
- `world/levelgen/density/spline.go` — the ported `CubicSpline$Multipoint` cubic (sample/linearExtend/findIntervalStart), float32-faithful.
- `world/levelgen/density/parse.go` — `Parse` + the dedup `Registry` + the `NoiseBinder`/`DataSource(Func)` seams.
- `world/levelgen/density/density_test.go` — the 7 node/parser tests.
- `world/levelgen/router/router.go` — `NoiseGeneratorSettings` + `NoiseRouter` (15 funcs) + `RandomState` + `NewRouter`.
- `world/levelgen/router/router_test.go` — the 5 router/seeding tests.

## Decisions Made

- **`invert` = `1.0/x`, NOT `-x`.** The plan (and the earlier assumed list) stated `invert` is a trivial unary `-x`. The bytecode (`DensityFunctions$Mapped.transform`, ordinal `INVERT` → `dconst_1; dload_1; ddiv`) is unambiguously `1.0 / x`. Ported as `1.0/x` and the test asserts `invert(4)=0.25`. This is the "PORT EXACT" mandate overriding the plan's prose.
- **`interval_select` uses the 26.2 `functions[]` + `thresholds[]` shape** (N functions, N−1 thresholds; first `i` where `v < thresholds[i]` → `functions[i]`, else last), confirmed from the embedded cave JSON + `IntervalSelect.compute` bytecode.
- **`find_top_surface` fields are `cell_height`/`density`/`lower_bound`/`upper_bound`** (upper_bound is itself a node). It scans `floor(upperBound/cellHeight)*cellHeight` DOWN to lower_bound for the first `density(x,y,z)>0` — ported from `FindTopSurface.compute`. It IS `preliminary_surface_level`.
- **`RandomState` seeds via the base factory directly**: `Xoroshiro(seed).forkPositional()` is the base `random`; each overworld noise is `random.FromHashOf(id)` → `NormalNoise.create(rs, params)` (RandomState.getOrCreateNoise → Noises.instantiate, bytecode-confirmed). No per-noise sub-fork for the overworld path.
- **blend nodes pass through with blending off**: `blend_alpha=1.0`, `blend_offset=0.0`, `blend_density=identity` — correct only because there is no neighbour-chunk blend in this server; documented in the node comments.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Router relocated from `package levelgen` to `package router`**
- **Found during:** Task 2 (router.go)
- **Issue:** The plan specifies `world/levelgen/router.go` in `package levelgen` and `router_test.go` in `package levelgen`. This is physically impossible: the Tier-A primitives live in `package levelgen`, which `synth` imports, which `density` imports. A composition root in `package levelgen` that imports `density` creates the cycle `levelgen → density → synth → levelgen`, which `go build` rejects.
- **Fix:** Moved the file to `world/levelgen/router/router.go` (package `router`) and the test to `world/levelgen/router/router_test.go` (package `router`) — the standard Go layout for a composition root that sits above its leaf packages. The public surface (`NewRouter`, `NewRandomState`, `Router`, `NoiseRouter`, `NoiseGeneratorSettings`, `RandomState`) is unchanged; only the import path moves from `levelgen.NewRouter` to `router.NewRouter`. Documented with a package-level note.
- **Files modified:** world/levelgen/router/router.go, world/levelgen/router/router_test.go
- **Verification:** `go build ./...` clean; all 5 router tests pass; no downstream code references the old path (Wave 4+ not yet built).
- **Committed in:** 1138a636 (Task 2 commit)

**2. [Rule 1 - Bug] `invert` ported as `1.0/x` not `-x`**
- **Found during:** Task 1 (nodes.go)
- **Issue:** The plan repeatedly states `invert` is a unary `-x`. The bytecode says `1.0/x`. Implementing `-x` would silently produce wrong terrain wherever `invert` appears (it appears in `preliminary_surface_level.upper_bound`).
- **Fix:** Ported `mapInvert = 1.0/x` per the bytecode; the test pins `invert(4)=0.25`.
- **Files modified:** world/levelgen/density/nodes.go, world/levelgen/density/density_test.go
- **Verification:** `TestUnaryAndArithmeticNodes` asserts `0.25`; the full graph (which uses invert) parses + evaluates finite + deterministic.
- **Committed in:** c40c2223 (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (1 blocking-structural, 1 bug). **Impact:** Both necessary for correctness. The package move is purely structural (no behavior/surface change). The invert fix is the "PORT EXACT" mandate winning over the plan's prose — without it terrain would be silently wrong. No scope creep.

## Issues Encountered

- **Import cycle** when the router was placed in `package levelgen` (see Deviation 1) — resolved by relocating to `package router`.

## Known Stubs

None. No TODO/FIXME/placeholder patterns; no hardcoded empty data flowing to output. The `weird_scaled_sampler`/`beardifier`/`cache_all_in_cell` types are NOT ported (correctly — they are not in the overworld graph; the parse tests do not depend on them, and an attempt to use them would hit the loud "unsupported type" error). `end_islands` is the only deferred density node (End-only).

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- **Wave 4 (NoiseChunk / 09-04):** `router.NoiseRouter.FinalDensity` is evaluable per (x,y,z); the `interpolated` markers are preserved (`Marked` interface, `MarkerInterpolated`) so the cell-grid sampler can detect them. Ready.
- **Wave 5 (Aquifer / OreVeinifier):** `Barrier`/`FluidLevelFloodedness`/`FluidLevelSpread`/`Lava` and `VeinToggle`/`VeinRidged`/`VeinGap` are bound + exposed on `NoiseRouter`. Ready.
- **Wave 7 (Surface rules):** `PreliminarySurfaceLevel` (the find_top_surface node) parses + evaluates; `NoiseGeneratorSettings.SurfaceRule` carries the raw surface_rule JSON for parsing. Ready.
- **No blockers.** The graph parses end-to-end with only supported node types (`TestParseFullGraphEndToEnd` is the gate); any future node gap fails at construction, not runtime.

---
*Phase: 09-stretch-online-worldgen-regions*
*Completed: 2026-06-24*

## Self-Check: PASSED

- All 7 created source files exist on disk.
- All 3 task commits (c40c2223, 810356cc, 1138a636) exist in git history.
- Plan verification re-run green: density tests (7), router tests (5), Wave 1/2 regression, `go vet ./...` + `go build ./...` clean across the whole project, zero new deps, Docker `-race` clean over density+router.
