---
phase: 09-stretch-online-worldgen-regions
plan: 08
subsystem: worldgen
tags: [generator, noise, fill, carve, surface, heightmaps, biomes, aquifer, ore-veins, parity, minecraft-26.2]

# Dependency graph
requires:
  - phase: 09-03
    provides: router.NewRouter(seed) — the bound NoiseRouter (final_density + the 15 functions) + RandomState; the generator constructs the router ONCE from the world seed
  - phase: 09-04
    provides: noisechunk.NewNoiseChunk — the cell-sample + trilerp final_density per column
  - phase: 09-05
    provides: noisechunk.NewAquifer + NewOreVeinifier + FillChunk — the real doFill (stone/deepslate/water/lava/air/ore veins, aquifer-aware)
  - phase: 09-06
    provides: carver.ApplyCarvers + LoadOverworldCarvers + ParseReplaceables + the CarveChunk/FluidSource interfaces — the ravine + tunnel-cave carve pass
  - phase: 09-07
    provides: biome.NewMultiNoiseBiomeSource.GetBiome + surface.NewSurfaceSystem + ParseRuleSource + BuildSurface + FillBiomes — the biome-correct surface + the 3 CLIENT heightmaps + varied biome containers
provides:
  - "world/noisegen.go — NoiseGenerator implementing the UNCHANGED world.Generator: NewNoiseGenerator(seed, secs, minY) builds the router+biome+surface+carvers ONCE; Generate(pos) drives fill->carve->surface(+heightmaps)->biomes into the reused capture-diff-sealed level.Chunk, pure"
  - "world/levelgen/noisechunk Aquifer.CarveFluid — exported carver.FluidSource seam (computeSubstance at density 0) wiring the Wave-5 aquifer into the Wave-6 carve pass"
affects: [09-09-wire-main, worldgen-client-render, mob-spawning, view-streaming]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the full-parity Generator is a pure assembly: NewNoiseGenerator builds every stage ONCE; Generate(pos) reads only seed+pos+the bound graph (no math/rand, no global state, no map-iteration-order) — same seed+pos -> identical encoded bytes (WORLD-04 / Pitfall 7)"
    - "pipeline order fill->carve->surface (heightmaps last): vanilla's ChunkStatusTasks runs generateSurface BEFORE generateCarvers as STATUS steps, but a single-shot generator must carve-then-surface so a carved ravine gets a correct surface on its NEW top and the FINAL client heightmaps (BuildSurface's last step) are truthful"
    - "the carve adapter (carveChunk) bridges *level.Chunk to carver.CarveChunk with world-coord Get/Set and a footprint-bound Set, so a carve started in a neighbor source chunk only edits THIS chunk (cross-chunk caves without leaking into other chunks)"
    - "build-data errors panic (NewNoiseGenerator mirrors NewSuperflat): the worker treats Generate as infallible; missing/unparseable embedded DATA is an asset/programming bug, not runtime input"

key-files:
  created:
    - "world/noisegen.go — NoiseGenerator + NewNoiseGenerator + Generate (the 4-stage pipeline) + the carveChunk adapter + the compile-time Generator/CarveChunk/FluidSource assertions"
    - "world/noisegen_test.go — the 4 plan tests: ImplementsGenerator, Deterministic (encode round-trip), ChunkComplete (solid/water/biomes/heightmaps/round-trip), FullParityFeatures (varied height, caves, cave_air, ore veins, perched fluid)"
  modified:
    - "world/levelgen/noisechunk/aquifer.go — added Aquifer.CarveFluid (the exported carver.FluidSource seam)"

key-decisions:
  - decision: "Pipeline order is fill -> carve -> surface, with the 3 CLIENT heightmaps written LAST (by BuildSurface)"
    rationale: "javap -p ChunkStatusTasks (26.2-inner.jar) shows generateBiomes->generateNoise->generateSurface->generateCarvers->generateFeatures, i.e. SURFACE precedes CARVERS as STATUS steps. But the BLOCK-level dependency for a single-shot generator is the opposite: the carve must cut stone BEFORE the surface is applied so a carved ravine gets a correct grass/dirt/gravel surface on its new top (not the pre-carve top), and the heightmaps must reflect the FINAL blocks (a cave opening at the surface). fill->carve->surface achieves both; this is the same ordering 09-RESEARCH specifies. The only divergence from vanilla's STATUS order is a surface-on-carved-top refinement — the correct behavior here."
  - decision: "Expose Aquifer.CarveFluid rather than build a separate FluidSource in the generator"
    rationale: "The carver package doc explicitly states the Generator (Wave 8) wires the FluidSource to the Wave-5 aquifer's computeSubstance at density 0. A thin exported method on Aquifer (CarveFluid = computeSubstance(x,y,z,0)) reuses the real aquifer's per-cell caches + the global FluidPicker for free, so the carve floods exactly as the fill does (water/lava below the local table, air above). A re-implemented FluidSource would duplicate the aquifer logic and risk drift."
  - decision: "Generate constructs a fresh NoiseChunk/Aquifer/OreVeinifier per call (no per-generator reuse)"
    rationale: "Those carry per-chunk position state + per-chunk caches; they MUST be per-pos. Only the router + biome source + surface system + carver configs (all position-independent) are built once in the constructor. This keeps Generate pure (no shared mutable state across calls) and -race clean while the off-tick worker runs many Generates concurrently."

requirements-completed: [PARITY-01]
duration: 35 min
completed: 2026-06-24
---

# Phase 9 Plan 08: NoiseGenerator Full-Parity Assembly Summary

Assembled the `NoiseGenerator` — a pure, drop-in `world.Generator` that drives the entire ported worldgen pipeline (router -> cell-sample -> aquifer/ore fill -> carve -> biome surface) into a complete, recognizable vanilla overworld chunk: hills and valleys, noise caves, carved tunnels and ravines, perched aquifers and deep lava, ore veins, biome-varied surfaces, and correct client heightmaps — all deterministic over the world seed, behind the UNCHANGED off-tick worker, with zero new dependencies.

## What shipped

- **`world/noisegen.go` — the full-parity Generator.** `NewNoiseGenerator(seed, secs, minY)` builds the router (`router.NewRouter`), the multi-noise biome source (`biome.NewMultiNoiseBiomeSource`), the surface system + parsed `surface_rule` (`surface.NewSurfaceSystem` / `surface.ParseRuleSource`), and the overworld carver list + replaceables (`carver.LoadOverworldCarvers` / `carver.ParseReplaceables`) — all ONCE. `Generate(pos)` drives the 4-stage pipeline into a fresh `level.Chunk`:
  1. **fill** — `NewNoiseChunk` (cell-sample `final_density`) + `NewAquifer` + `NewOreVeinifier` + `FillChunk` place stone/deepslate, aquifer water/lava/air, and ore veins.
  2. **carve** — `ApplyCarvers` runs the ravines + extra tunnel caves over the filled stone, aquifer-aware via the new `Aquifer.CarveFluid` seam (a carve below the local water table floods).
  3. **surface** — `BuildSurface` applies the biome-correct surface rules to each column's stone top (grass/dirt/sand/gravel/terracotta) and rewrites the 3 CLIENT heightmaps from the FINAL blocks; `FillBiomes` fills the per-section 4x4x4 biome containers from the multi-noise source (varied, not single-plains).
  4. **finish** — sky light per present section; `Status = StatusFull`.
- **`carveChunk` adapter** — bridges `*level.Chunk` to `carver.CarveChunk` (world-coord `Get`/`Set`, footprint-bounded `Set` so cross-chunk carves don't leak into neighbor chunks).
- **`Aquifer.CarveFluid` (aquifer.go)** — the exported `carver.FluidSource` seam: `computeSubstance(x,y,z, 0)`, reusing the real aquifer's per-cell caches + global FluidPicker so the carve floods exactly as the fill does.
- **Compile-time assertions** — `var _ Generator = (*NoiseGenerator)(nil)`, `var _ carver.CarveChunk = (*carveChunk)(nil)`, `var _ carver.FluidSource = (*noisechunk.Aquifer)(nil)`.

## Verification

The plan's `<verify>` automated command passes:

```
go test ./world/ -run 'TestNoiseGenDeterministic|TestNoiseGenImplementsGenerator|TestNoiseGenChunkComplete|TestNoiseGenFullParityFeatures' -count=1   ->  ok
go vet ./world/...   ->  clean
go build ./...       ->  clean
```

- **TestNoiseGenImplementsGenerator** — `*NoiseGenerator` satisfies `world.Generator`; a chunk built through the interface has the right section count and `StatusFull`.
- **TestNoiseGenDeterministic** — two `Generate(pos)` calls on one generator encode to identical bytes; a second generator from the same seed agrees byte-for-byte (WORLD-04 / Pitfall 7).
- **TestNoiseGenChunkComplete** — across a 5x5 chunk span: solid ground present (not all-air), water near sea level (terrain dips to ocean), >= 2 distinct biomes in the containers (varied, not single-plains), all 3 client heightmaps present and in `[minY, maxY]`, and every chunk round-trips the Phase-4 encoder to non-empty bytes.
- **TestNoiseGenFullParityFeatures** — across a 4x4 chunk span: the WorldSurface heightmap varies (hills/valleys), underground air pockets exist (noise caves + carved tunnels, air under rock), `cave_air` present (the carve pass ran), ore-vein blocks (copper/iron ore, raw blocks, granite/tuff fillers) appear in the rock, and a perched/deep aquifer fluid (lava, or water well below sea level) appears.

Full `./world/...` suite: no regressions (the existing Superflat + Wave-1..7 tests still pass). `-race` over `./world/` (TestNoiseGen) clean via `docker run golang:1.26 go test -race` (504s — expected for the heavy noise pipeline). `CGO_ENABLED=0 go build ./...` clean (static-binary value prop preserved).

## ChunkStatusTasks citation

`javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.chunk.status.ChunkStatusTasks` confirms the STATUS order `generateBiomes -> generateNoise -> generateSurface -> generateCarvers -> generateFeatures`. The generator runs **fill -> carve -> surface** in the block sense (carve before surface, heightmaps last) so carved tops get a correct surface and the client heightmaps are truthful — see the "Pipeline order" key-decision above.

## Untouched (the reused seam)

`world/worker.go` (the off-tick reader), `world/generator.go` (the `Generator` interface + Superflat — kept as a fallback/test generator), the tick, the `level.Chunk` wire (capture-diff-sealed in Phase 4), and `cmd/sulfur/main.go` are ALL UNTOUCHED. PARITY-01 is purely "a better `Generate`"; the wiring into `main.go` + the BLOCKING real-client visual gate is Plan 09-09.

## Deviations from Plan

**[Rule 2 — Missing critical seam] Added `Aquifer.CarveFluid`.**
- Found during: Task 1 (wiring the carve stage).
- Issue: the carve stage needs a `carver.FluidSource`, and the carver package doc states it is wired to "the Wave-5 Aquifer (computeSubstance at density 0)" — but `Aquifer.computeSubstance` is unexported, so no exported seam existed for the Generator to wire.
- Fix: added a one-line exported `Aquifer.CarveFluid(x,y,z) (block.StateID, bool)` delegating to `computeSubstance(x,y,z, 0)`, satisfying `carver.FluidSource`. This is the documented seam, not new logic.
- Files modified: `world/levelgen/noisechunk/aquifer.go`.
- Verification: `go build ./...` clean; `var _ carver.FluidSource = (*noisechunk.Aquifer)(nil)` compiles; the carve floods correctly (TestNoiseGenFullParityFeatures finds perched water/lava).
- Commit: b2f1c106 (grouped with the GREEN implementation, since the seam is what makes the carve wiring compile and run).

**Total deviations:** 1 auto-fixed (1 Rule 2 — missing critical seam). **Impact:** minimal — a single exported method on an existing type that exposes already-ported logic; no behavior change to the aquifer's fill path.

## Known Stubs

None new in this plan. The deferred items it inherits (documented, not introduced here): features/decoration (trees, ores-as-features, structures) are out of PARITY-01 scope by design; the surface temperature/snow condition (09-07) and the deepslate-blend band (09-04) remain documented Wave-7/provisional limitations in their own packages.

## Notes for the next plan (09-09)

- Wire `NewNoiseGenerator(seed, overworldSecs=24, overworldMinY=-64)` into `cmd/sulfur/main.go` in place of (or alongside) `NewSuperflat` — same constructor shape minus the `surfaceY` arg.
- The off-tick worker takes the generator as-is (`world.NewWorker(gen, regionDir, buf)`); no worker change.
- Run the BLOCKING real-client visual gate (connect a vanilla 26.2 client, fly around, confirm hills/caves/ravines/biomes render and the player doesn't fall through).
- Per-chunk `Generate` is heavier than Superflat (~1-2s in the test harness under no -race; the worker runs it off-tick so this is acceptable per the plan's performance note). If streaming latency is an issue, that is a worker-tuning concern (buffer size / pool), not a generator change.

## Self-Check: PASSED

- `world/noisegen.go` — FOUND
- `world/noisegen_test.go` — FOUND
- `world/levelgen/noisechunk/aquifer.go` (modified, CarveFluid) — FOUND
- Commit afb7a485 (RED test) — FOUND
- Commit b2f1c106 (GREEN feat) — FOUND
- Plan `<verify>` command (tests + vet + build) — PASSED
