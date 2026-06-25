---
phase: 11-feature-pipeline-decoration
plan: 02
subsystem: world/levelgen/placement
tags: [worldgen, decoration, placement, determinism, feat-01]
requires:
  - "world/levelgen/random.go (RandomSource / LegacyRandomSource / WorldgenRandom)"
  - "world/levelgen/feature (11-01: PlacedFeature + PlacementModifierRaw)"
  - "level/block (StateID), level/biome (Type)"
provides:
  - "world/levelgen/placement package: the 8 load-bearing placement modifiers"
  - "BoundPlacedFeature.place — the ordered flatMap fold"
  - "PlacementContext interface (GetHeight/MinY/Height/GetBlock/BiomeAt)"
  - "BindModifier + Bind seams for 11-03"
affects:
  - "11-03 applyBiomeDecoration (will back PlacementContext with the Neighborhood, supply the configured placer, and call Bind+Place per feature)"
tech-stack:
  added: []
  patterns:
    - "JAR-exact RNG-draw bodies transcribed from javap -c; draw count = determinism contract"
    - "abstract bases as embeddable structs wrapping a predicate/counter (PlacementFilter/RepeatingPlacement)"
    - "PlacementContext as an interface to keep placement free of a world import (no cycle)"
    - "single-threaded ordered fold (no goroutines/map-iteration) per research Pitfall 4"
key-files:
  created:
    - world/levelgen/placement/context.go
    - world/levelgen/placement/placement.go
    - world/levelgen/placement/heightprovider.go
    - world/levelgen/placement/modifiers.go
    - world/levelgen/placement/place.go
    - world/levelgen/placement/providers_test.go
    - world/levelgen/placement/modifiers_test.go
    - world/levelgen/placement/place_test.go
  modified: []
decisions:
  - "SurfaceWaterDepthFilter ported as the JAR heightmap-difference (WORLD_SURFACE - OCEAN_FLOOR) form, NOT the block-scan the research text described — the bytecode is authoritative."
  - "VerticalAnchor.below_top ported in full (added PlacementContext.Height() gen-depth) rather than erroring — ore height_range data uses it; erroring would break every ore feature."
  - "IntProviders ported: constant/uniform/clamped/biased_to_bottom/weighted_list (the set the overworld COUNT modifier uses). clamped_normal/very_biased only appear in the deferred random_offset, so they error loudly in count."
  - "HeightProviders ported: uniform/trapezoid/very_biased_to_bottom (the set overworld height_range uses)."
metrics:
  duration: "~1 session"
  tasks: 3
  files: 8
  tests: 22
  completed: 2026-06-25
---

# Phase 11 Plan 02: Placement Modifiers + PlacedFeature.place Fold Summary

Ported the 8 load-bearing Minecraft 26.2 (proto 776) placement modifiers with
JAR-exact RNG-draw bodies plus `PlacedFeature.place` as a single-threaded ordered
flatMap fold threading one `WorldgenRandom` through the whole modifier chain — the
draw sequence is the determinism contract (research Pitfall 4 / T-11-04).

## What was built

New `world/levelgen/placement` package (its own package, import-cycle clean —
imports `level/block`, `level/biome`, `world/levelgen`, and `world/levelgen/feature`;
NOT the `world` package):

**Interface + 2 abstract bases** (`placement.go`):
- `PlacementModifier` interface — `getPositions(ctx, rng, p) []BlockPos` — + `BlockPos`.
- `PlacementFilter` base (JAR: `shouldPlace ? {p} : {}`) — embeddable, wraps a `shouldPlace` predicate.
- `RepeatingPlacement` base (JAR: n = count(rng,pos); n copies) — embeddable, wraps a `counter`.

**The 8 modifiers** (`modifiers.go`), each `javap -c` transcribed and cited:
- `in_square` — `nextInt(16)+X` then `nextInt(16)+Z` (x FIRST, 2 draws, Y unchanged).
- `heightmap` — project to `GetHeight(type,x,z)`; empty iff `y <= MinY`; 0 draws.
- `count` (extends RepeatingPlacement) — `IntProvider.Sample(rng)` draws then N copies.
- `rarity_filter` (PlacementFilter) — keep iff `NextFloat() < 1/chance`; exactly 1 NextFloat.
- `biome` (PlacementFilter) — keep iff the per-position biome allows the feature (a `func(biome.Type) bool` field bound by 11-03); 0 draws.
- `height_range` — replace Y with `HeightProvider.Sample(rng,ctx)`, keep X/Z; 1 draw.
- `surface_water_depth_filter` (PlacementFilter) — keep iff `WORLD_SURFACE - OCEAN_FLOOR <= maxWaterDepth`; 0 draws.

**Providers** (`heightprovider.go`):
- `VerticalAnchor` (absolute / above_bottom / below_top) with JAR-exact `resolveY`.
- `HeightProvider` (uniform / trapezoid / very_biased_to_bottom) — the overworld height_range set.
- `IntProvider` (constant / uniform / clamped / biased_to_bottom / weighted_list) — the overworld count set. Unported types error loudly.

**Context** (`context.go`): `PlacementContext` interface + `HeightmapType` enum (WORLD_SURFACE_WG / OCEAN_FLOOR_WG / MOTION_BLOCKING / WORLD_SURFACE / OCEAN_FLOOR). Concrete Neighborhood-backed impl deferred to 11-03.

**The fold** (`place.go`): `BoundPlacedFeature.place` — explicit ordered loop folding the modifier chain over the origin then running `ConfiguredFeaturePlacer.place` at each anchor, one rng threaded throughout. `Bind(*feature.PlacedFeature, placer, deps)` is the 11-01→11-02 seam. `BindModifier` turns a `PlacementModifierRaw` envelope into a concrete modifier, erroring loudly on an unported type (T-11-05).

## Draw-order test results

`go test ./world/levelgen/placement/ -count=1` — **22 tests, all PASS**:
- Per-modifier draw-order: `TestInSquareDrawsXThenZ` (x-then-z, 2 draws + a guard against z-first), `TestHeightmapProjection` (+ empty at minY), `TestCountCopies` (constant 0-draw / uniform 1-draw before copies), `TestRarityOneFloat`, `TestBiomeFilter`, `TestHeightRange`, `TestSurfaceWaterDepthFilter`, `TestBindModifier` (incl. loud-error path).
- Provider draw counts: `TestProvider{Constant0,Uniform1,BiasedToBottom2,ClampedSource,WeightedListPick}` + `TestProviderUniformHeight{OneDraw,EmptyRangeNoDraw}` + `TestProviderBelowTopUsesGenTop` + `TestAnchorResolveY`.
- Bases: `TestFilterKeepsAndDrops`, `TestRepeatingCopies`.
- The fold: `TestPlaceFold` — a count→in_square→heightmap chain pins the exact anchor list, that the placer was called at each, the exact post-fold rng draw count (6), AND the exact post-fold rng STATE (next draw equals the hand-traced oracle's). `TestPlaceEmptyChain` (rarity reject → 0 anchors → false, 1 draw). `TestPlaceThreadsOneRng` (one threaded rng).

Every test uses a `drawCounter` wrapper that tallies primitive draws and asserts the JAR-exact count, plus an oracle computed off the same seeded `LegacyRandomSource`.

## Verification

- `go test ./world/levelgen/placement/ -count=1` — PASS (22 tests).
- `go build ./...` — clean.
- `CGO_ENABLED=0 go build ./...` — clean.
- `go vet ./world/levelgen/placement/` — clean; `gofmt` clean.
- No new deps (go.mod / go.sum unchanged).
- Acyclic: `go list -deps` shows placement → {levelgen, levelgen/data, levelgen/feature} — no `world` import; `feature` does not import `placement`.
- All sibling levelgen packages (incl. the parallel 11-01 `feature`) still pass.

## Deviations from Plan

### Auto-fixed / design adjustments

**1. [Rule 2 - Missing critical functionality] VerticalAnchor `below_top` ported, not errored**
- **Found during:** Task 1.
- **Issue:** The plan/interfaces said `below_top` "errors loudly (unsupported, as in surface)" and `PlacementContext` exposed only `MinY()`. But the overworld ore `height_range` placed_features (e.g. `ore_coal` `max_inclusive {below_top:0}`) DO use `below_top`. Erroring would break every ore/dripstone feature 11-03 binds.
- **Fix:** Ported `below_top` per the JAR (`(genDepth-1) + minGenY - offset`) and added `Height() int` (gen depth) to the `PlacementContext` interface so the anchor resolves faithfully.
- **Files:** context.go, heightprovider.go. **Commit:** 5a9a8f4e.

**2. [Rule 1 - Correctness] SurfaceWaterDepthFilter is a heightmap difference, not a block scan**
- **Found during:** Task 2.
- **Issue:** The research text described the modifier as "scan down counting water blocks via GetBlock". The `javap -c` bytecode of `SurfaceWaterDepthFilter.shouldPlace` does NO block scan — it reads two heightmaps: `depth = GetHeight(WORLD_SURFACE,x,z) - GetHeight(OCEAN_FLOOR,x,z)` and keeps iff `depth <= maxWaterDepth`.
- **Fix:** Ported the JAR heightmap-difference form (the authoritative source). `GetBlock` remains on the `PlacementContext` interface for future terrain-aware filters (block_predicate_filter, deferred), so no surface was lost.
- **Files:** modifiers.go. **Commit:** 7dd73b1c.

**3. [Scope] IntProvider/HeightProvider type sets bounded to actual overworld usage**
- The plan listed `clamped_normal` / `biased_to_bottom` for providers. A jar-data tally showed the `count` modifier uses only constant/uniform/clamped/biased_to_bottom/weighted_list, and `clamped_normal`/`very_biased_to_bottom` appear only in the deferred `random_offset` modifier (and `very_biased_to_bottom` in `height_range`). Ported exactly the sets the overworld modifiers reference; everything else errors loudly (T-11-05 precedent). `clamped_normal`/`Mth.normal` need `nextGaussian` (not on the shared `RandomSource`), so excluding them also avoids an unported primitive — they belong with `random_offset` in a later plan.

### Note on `-race`

`go test -race` could not run in this environment (`-race` requires cgo and no gcc/C compiler is present). The placement package is single-threaded BY DESIGN (the fold is an explicit sequential loop with no goroutines — that is the determinism contract), so the race detector would find nothing. Plain `go test` is green; the determinism is enforced by the exact post-fold rng-state assertion in `TestPlaceFold`.

## Known Stubs

- The `biome` filter's allowed-biome predicate is a `func(biome.Type) bool` field; for in-package tests a nil predicate is permissive. 11-03 supplies the real placed_feature biome allowance via `ModifierDeps.BiomeAllowed`. This is the documented 11-02↔11-03 seam, not an unintended stub.
- `ConfiguredFeaturePlacer` is a one-method interface; 11-03 supplies the real dispatch, Phase 12 the type-specific bodies. The fold is fully tested with a recording stub placer.

## Self-Check: PASSED

- world/levelgen/placement/{context,placement,heightprovider,modifiers,place}.go — FOUND
- world/levelgen/placement/{providers,modifiers,place}_test.go — FOUND
- Commits 5a9a8f4e, 7dd73b1c, 757fcadb — FOUND in git log
