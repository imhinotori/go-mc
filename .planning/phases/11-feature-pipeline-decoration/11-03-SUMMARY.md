---
phase: 11-feature-pipeline-decoration
plan: 03
subsystem: world / worldgen-decoration
tags: [worldgen, decoration, features, determinism, scheduler, feat-02]

# Dependency graph
requires:
  - phase: 11-01
    provides: "feature.Registry (LoadAllEmbedded + PlacedByID/ConfiguredByID), the parsed ConfiguredFeature/PlacedFeature DAG, PlacementModifierRaw envelopes"
  - phase: 11-02
    provides: "placement package — the 8 modifier bodies, BoundPlacedFeature.place fold, Bind/BindModifier, PlacementContext interface, ModifierDeps"
  - phase: 10-worldgen-foundation
    provides: "the 3x3 Neighborhood proxy (live heightmap-updating SetBlock), the worker staging scheduler, WorldgenRandom.SetDecorationSeed/SetFeatureSeed, the GEN2-02 Decorate seam"
provides:
  - "feature.FeatureSorter.BuildFeaturesPerStep — cross-biome IntSet dedup + Kahn toposort giving each placed_feature ONE global per-step index (memoized at construction)"
  - "world.applyBiomeDecoration — the JAR-exact 11-step GenerationStep.Decoration outer loop (SetDecorationSeed at block origin, per-step IntSet+sort of global indices, SetFeatureSeed(decoSeed,idx,step) per feature, Bind+Place with biome re-check)"
  - "world.placementContext — the Neighborhood -> placement.PlacementContext adapter (live worldgen heightmaps, biome cache)"
  - "placement.PlacerFunc — exported cross-package bridge to the unexported-method ConfiguredFeaturePlacer (the real dispatch + Phase-12 bodies live in world/)"
  - "the live NoiseGenerator.Decorate (runs applyBiomeDecoration) + the feature/sorter build in NewNoiseGenerator"
  - "the worker D2 Option-Y emit rule (decorate-on-own-3x3, emit-once-all-wanted-neighbors-decorated, no resend)"
affects: [worldgen-decoration, phase-12-feature-bodies, world-worker]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Kahn toposort with first-seen tie-break for biome-order-independent cross-biome feature ordering; a contradictory order errors as a cycle (vanilla FeatureSorter)"
    - "JAR-exact applyBiomeDecoration: block-origin decoration seed, GLOBAL cross-biome index (not per-biome counter), IntSet+sort dedup, single-threaded ordered fold (no goroutines/map iteration — the rng draw order is the determinism contract)"
    - "exported PlacerFunc bridge for an interface whose method is unexported, so a downstream package supplies the body"
    - "D2 Option Y: split decorated from emitted; decorate-as-carved + hold-until-all-wanted-neighbors-decorated -> emit-once, no resend, the immutable single-owner ChunkResult preserved"
    - "recordable no-op feature placer dispatch: orchestration testable now (seed/index trace), real bodies drop into the same dispatch in Phase 12+ unchanged"

key-files:
  created:
    - world/levelgen/feature/sorter.go
    - world/levelgen/feature/sorter_test.go
    - world/decoration.go
    - world/placement_context.go
    - world/decoration_test.go
  modified:
    - world/levelgen/placement/place.go
    - world/noisegen.go
    - world/worker.go
    - world/generator.go
    - world/worker_seam_test.go

decisions:
  - "FeatureSorter is built over ALL biomes once at construction (vanilla builds it over the biome source's possibleBiomes), NOT per-chunk — the global per-step ordering must be consistent across every biome, and applyBiomeDecoration's per-chunk 3x3 set only selects which indices get referenced."
  - "loadBiomeFeatures lives in the world package (not feature) — it needs level/biome.Type + world/levelgen/data, and keeping it in world avoids adding a level/biome import to the import-cycle-disciplined feature package."
  - "placement.PlacerFunc added (Rule 3 blocking fix): ConfiguredFeaturePlacer.place is unexported, so the world package CANNOT implement the interface directly; PlacerFunc is the sanctioned exported bridge. Minimal, non-breaking addition to placement."
  - "GetHeight maps the client heightmap variants (WORLD_SURFACE/OCEAN_FLOOR) to their _WG counterparts — mid-worldgen there is no separate client heightmap, and surface_water_depth_filter's difference is identical on either pair."
  - "D2 = Option Y (hold-until-neighborhood-complete). The emit pass scans each NEWLY-decorated center's OWN 3x3 ring (reaches pos±2, beyond the decorate ring) because decorating C unblocks both C and C's wanted neighbors — the first naive 'emit over the event ring' deadlocked the 5x5 test."

metrics:
  duration: "~1 session"
  tasks: 5
  files: 10
  tests_added: 8
  completed: 2026-06-25
---

# Phase 11 Plan 03: Feature Decoration Orchestration (FEAT-02 orchestration half) Summary

**Wired 11-01's parsed feature graph + 11-02's modifier fold into a LIVE decoration pass: ported `FeatureSorter.buildFeaturesPerStep` (cross-biome dedup -> one global per-step index, Kahn toposort, cycle-erroring), the JAR-exact 11-step `applyBiomeDecoration` outer loop (block-origin decoration seed, `SetFeatureSeed(decoSeed, globalIndex, step)` per feature, biome-rechecked place), upgraded `NoiseGenerator.Decorate` from the Phase-10 no-op to drive it over the 3x3, and RESOLVED Open Decision D2 with Option Y (hold-until-neighborhood-complete, emit-once, no resend) — proven by a hand-derived per-feature seed/index trace, a cross-biome dedup test on the real embed, and the Phase-10 5x5 reorder + emit-once gates staying green with the now-live Decorate, all Docker `-race` clean.**

## What was built

**Task 1 — FeatureSorter (`sorter.go`), commit `5a3bfe48`:**
`BuildFeaturesPerStep([][][]*PlacedFeature)` — per GenerationStep.Decoration step, a deduped flat ordered `[]*PlacedFeature` + a `pf -> global index` mapping. A feature shared by N biomes collapses to ONE index (so it gets ONE decoration seed). Kahn's algorithm over the consecutive cross-biome ordering constraints with a first-seen tie-break makes the output independent of biome iteration order; a contradictory order (A: p1<p2, B: p2<p1) errors as a cycle (vanilla). `TestSorterDedup/CycleErrors/Deterministic` green.

**Task 2 — applyBiomeDecoration + the adapter (`decoration.go`, `placement_context.go`, `place.go`), commit `079d7e73`:**
- `applyBiomeDecoration` ports `ChunkGenerator.applyBiomeDecoration` EXACTLY: `decoSeed = SetDecorationSeed(seed, chunkX*16, chunkZ*16)` (BLOCK origin); per step gather the IntSet of GLOBAL indices the retained 3x3 biomes reference, sort ascending, per idx `SetFeatureSeed(decoSeed, idx, step)` then `Bind`+`Place` with the biome allowance re-check. Single-threaded ordered fold, no goroutines, no map iteration over the index set.
- `placementContext` adapts the live `*Neighborhood` + biome cache to `placement.PlacementContext` (GetHeight reads the live worldgen heightmaps `bs.Get(col)+minY`; GetBlock/BiomeAt via the proxy + cache; Height/MinY from Dims).
- The configured-feature placer dispatch: every real type is a recordable no-op this phase; one test-only `test_set_block` type writes through the view. `placement.PlacerFunc` is the exported bridge to the unexported-method `ConfiguredFeaturePlacer`.
- `buildDecorationData` parses the full roster + builds the FeatureSorter over all biomes once; `loadBiomeFeatures` resolves each biome JSON `features` array through the registry.

**Task 3 — live Decorate (`noisegen.go`), commit `bc1709c9`:**
`NewNoiseGenerator` builds the feature graph once (panics on a build-data error like the router/carver builds). `Decorate` runs `applyBiomeDecoration` over the retained 3x3 biome set after the worldgen-heightmap build, writing through the Neighborhood (heightmaps stay live), then keeps the sky-light + promote-to-StatusFull tail. Superflat.Decorate stays a no-op.

**Task 4 — D2 Option Y (`worker.go`, `generator.go`), commit `e78bf72e`:**
Split `decorated` from `emitted` in `stagedChunk`. `tryDecorate` decorate-only (returns whether it decorated); `tryEmit` is the gate — emit a decorated center exactly once iff every WANTED neighbor that holds it is also decorated. `processRing` decorates the wanted centers a carved/wanted pos could complete, then re-checks the emit gate over each newly-decorated center's 3x3 (the superset of centers a decoration could unblock, reaching pos±2). A non-wanted ring chunk never decorates so never gates emit (no hold-deadlock); `decorated && !emitted` is the only mutation window (no write-after-emit, ChunkResult contract intact).

**Task 5 — acceptance suite (`decoration_test.go`, `worker_seam_test.go`), commit `4296b0e3`:**
- `TestFeatureSeedTrace`: synthetic controlled-index graph; the recorded `(step, globalIndex, decoSeed, featureSeed)` sequence matches the hand-derived oracle (`featureSeed = decoSeed + idx + 10000*step`, decoSeed via the public `SetDecorationSeed` at the block origin, sorted ascending).
- `TestTraceOrderIndependent`: identical trace under `{A,B}` vs `{B,A}` biome order.
- `TestTestSetBlockFlows`: the test feature writes through the view AND the live worldgen heightmap rises.
- `TestSorterIntegration`: `ore_dirt` shared by plains+forest in the REAL embed is DAG-deduped to one pointer + one global index.
- `TestEmitOnceUnderHold`: a 3x3 block of maximally-adjacent wanted centers each emits exactly once, StatusFull.
- `TestDecorationReorderIdentical` + `TestEmitOnce` + `TestNeighborhoodCompletion` (Phase 10) stay GREEN with the live Decorate.

## Seed-trace + determinism + -race results

- **Seed/index trace:** `TestFeatureSeedTrace` PASS — the per-feature `(step, idx, featureSeed)` sequence equals the hand-derived `decoSeed + idx + 10000*step` for the sorted global indices across the steps (block origin confirmed). `TestTraceOrderIndependent` PASS (order-free).
- **Determinism-at-seams:** `TestDecorationReorderIdentical` (5x5, two request orders) PASS with the now-LIVE Decorate — byte-identical chunks regardless of order. `TestEmitOnce` + `TestEmitOnceUnderHold` PASS — exactly-once emit under the hold gate.
- **FeatureSorter:** dedup/cycle/determinism + the real-embed `ore_dirt` dedup all PASS.
- **Docker `-race` gate** (`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./world/...`) — GREEN, tail:

```
ok  github.com/imhinotori/sulfur/world                       386.126s
ok  github.com/imhinotori/sulfur/world/levelgen              1.019s
ok  github.com/imhinotori/sulfur/world/levelgen/biome        26.445s
ok  github.com/imhinotori/sulfur/world/levelgen/carver       2.324s
ok  github.com/imhinotori/sulfur/world/levelgen/data         1.443s
ok  github.com/imhinotori/sulfur/world/levelgen/density      1.144s
ok  github.com/imhinotori/sulfur/world/levelgen/feature      2.620s
ok  github.com/imhinotori/sulfur/world/levelgen/noisechunk   12.598s
ok  github.com/imhinotori/sulfur/world/levelgen/placement    2.329s
ok  github.com/imhinotori/sulfur/world/levelgen/router       2.181s
ok  github.com/imhinotori/sulfur/world/levelgen/surface      10.308s
ok  github.com/imhinotori/sulfur/world/levelgen/synth        1.046s
```

The 386s `world` run exercises the live decoration + the Option-Y scheduler under the race detector — no data races; the single-scheduler-goroutine single-owner discipline holds.

- `go build ./...` exit 0; `CGO_ENABLED=0 go build ./...` exit 0; `go vet` clean.
- `go.mod`/`go.sum` unchanged — NO new deps.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] placement.PlacerFunc added — ConfiguredFeaturePlacer's method is unexported**
- **Found during:** Task 2.
- **Issue:** `placement.ConfiguredFeaturePlacer` has an UNEXPORTED method `place(...)`, so a type in the `world` package cannot implement the interface directly — the plan's "the configured-feature placer dispatch (a placement.ConfiguredFeaturePlacer)" in `world/placement_context.go` was not constructible as written.
- **Fix:** Added `placement.PlacerFunc` — an exported `func(...) bool` adapter that satisfies `ConfiguredFeaturePlacer` — to `world/levelgen/placement/place.go`. The world package supplies the real dispatch as a `PlacerFunc` closure. Minimal, non-breaking (purely additive) to the placement package.
- **Files:** world/levelgen/placement/place.go. **Commit:** `079d7e73`.

**2. [Rule 1 - Bug] Emit pass must scan each newly-decorated center's 3x3, not the event's ring**
- **Found during:** Task 4 (the 5x5 reorder + emit-once tests deadlocked on the first implementation).
- **Issue:** Running the emit gate only over the 3x3 ring of the carved/wanted EVENT missed centers that a decoration two chunks away unblocked: decorating center D unblocks D AND D's wanted neighbors (D is a wanted neighbor they waited on), which can be at event±2.
- **Fix:** `tryDecorate` now returns whether it decorated this turn; `processRing` collects the newly-decorated centers and runs `tryEmit` over each one's OWN 3x3 (the superset of centers a decoration could unblock). The decorate/emit passes are idempotent (flag-guarded). The 5x5 reorder + emit-once + neighborhood-completion gates then pass.
- **Files:** world/worker.go. **Commit:** `e78bf72e`.

## Known Stubs

By design (Phase 11 ships ORCHESTRATION, not feature bodies):
- The configured-feature placer dispatches every one of the 226 real vanilla feature types to a **recordable no-op** — the orchestration + the per-feature seed/index trace are fully exercised now, but NO blocks are written by a real type this phase. The real `Feature.place` bodies (tree/ore/patch/...) drop into the SAME dispatch in **Phase 12+** with no orchestration change. This is the explicit plan boundary ("Phase 11 ships orchestration WITHOUT feature bodies"), not an unintended stub — Phase 12 owns the bodies.
- Because no real type writes, production decorated chunk bytes are identical to Phase 10 (the noise/generate tests stay byte-stable). The single test-only `test_set_block` type proves the write path (SetBlock -> live heightmap) works for when the bodies arrive.

## Self-Check: PASSED

- Created files FOUND: world/levelgen/feature/sorter.go + sorter_test.go, world/decoration.go, world/placement_context.go, world/decoration_test.go.
- Modified files FOUND: world/levelgen/placement/place.go, world/noisegen.go, world/worker.go, world/generator.go, world/worker_seam_test.go.
- Commits FOUND in git log: 5a3bfe48, 079d7e73, bc1709c9, e78bf72e, 4296b0e3.
