---
phase: 09-stretch-online-worldgen-regions
plan: 07
subsystem: worldgen
tags: [biome, multi-noise, climate, surface, surfacerules, surfacesystem, parity, minecraft-26.2]

# Dependency graph
requires:
  - phase: 09-01
    provides: biome_parameters.json (the overworld Climate$ParameterList boxes) + the overworld.json surface_rule subtree (data.BiomeParameters / data.NoiseSettings → Settings.SurfaceRule)
  - phase: 09-03
    provides: the bound router climate functions (Temperature/Vegetation/Continents/Erosion/Depth/Ridges) for the Climate sampler + preliminary_surface_level for the surface above_preliminary_surface condition + RandomState (the surface-noise seed bridge)
  - phase: 09-05
    provides: the filled+aquifered chunk (FillChunk) + NoiseChunk.preliminarySurfaceLevel — the raw terrain the surface pass runs ON TOP of
provides:
  - "world/levelgen/biome/ — MultiNoiseBiomeSource.GetBiome(x,y,z): the 6-D Climate sampler + the nearest-box search over the Wave-1 parameter list → a VARIED biome per quart cell (committed earlier this plan as f210d705)"
  - "world/levelgen/surface/ — SurfaceSystem + the FULL SurfaceRules vocabulary: ParseRuleSource(surface_rule) → the ported rule tree; BuildSurface(s, rule, chunk, nc, biomeOf) walks each column top-down, applies the rule sequence (biome-correct surface blocks), and writes the 3 CLIENT heightmaps; FillBiomes fills the per-section biome containers with the real biomes"
  - "noisechunk.Pos() + noisechunk.PreliminarySurfaceLevel() — exposed for the surface Context"
  - "router.RandomState.BaseFactory() — the SurfaceSystem noiseRandom (the base positional factory)"
affects: [09-08-generator, worldgen-client-render, mob-spawning]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the surface_rule is parsed with a type-dispatch identical to the 09-03 density parser — an unsupported rule/condition type errors LOUDLY (never a silent wrong block)"
    - "the surface ConditionSource and Condition collapse into one interface (the parsed source reads the Context per-test) — the vanilla LazyCondition apply(Context) memoization is a dropped perf refinement, result-identical"
    - "a generic {Name,Properties} block-state resolver (FromID + nbt.Marshal(map[string]string) → State.Block → ToStateID) resolves any surface_rule result_state, incl. the Sulfur-fork custom blocks (sulfur, cinnabar)"
    - "buildSurface only REWRITES cells whose state is the default_block (stone) — it caps raw stone with the surface, never clobbering ore veins / water / aquifer fluid"

key-files:
  created:
    - "world/levelgen/biome/climate.go — the Climate 6-D sampler + Climate$ParameterList nearest-match (committed f210d705)"
    - "world/levelgen/biome/source.go — MultiNoiseBiomeSource.GetBiome (committed f210d705)"
    - "world/levelgen/biome/climate_test.go — the 4 biome tests + a fitness/tiebreak test (committed f210d705)"
    - "world/levelgen/surface/rules.go — the surface_rule parser + the ported ConditionSource/RuleSource node set (the full vocabulary) + the {Name,Properties} block resolver"
    - "world/levelgen/surface/system.go — SurfaceSystem (seeded surface/secondary/clay-band noises + getSurfaceDepth/getSurfaceSecondary/getBand + generateBands/makeBands), the Context column state, BuildSurface, FillBiomes, the client heightmap writers"
    - "world/levelgen/surface/surface_test.go — the 4 surface tests + a biome-variety sanity test"
  modified:
    - "world/levelgen/noisechunk/noisechunk.go — added Pos() + PreliminarySurfaceLevel() accessors for the surface Context"
    - "world/levelgen/router/router.go — added RandomState.BaseFactory() (the surfaceSystem noiseRandom)"

key-decisions:
  - decision: "Port the LINEAR Climate nearest-match (findValueBruteForce), not the RTree"
    rationale: "The brute-force scan returns the SAME nearest box + the same first-match-wins tiebreak as the RTree (findValueIndex); the biome is sampled per 4×4×4 quart cell (not per block) so the O(boxes) cost is bounded (T-9-19). The RTree is a documented perf refinement, deferred."
  - decision: "temperatureCondition (the surface temperature/coldEnoughToSnow rule) returns false (conservative)"
    rationale: "Biome.coldEnoughToSnow needs the per-biome temperature model, which is a Phase-2+ biome-data concern not yet ported. Returning false leaves the underlying grass/dirt/stone surface — the safe non-snow default — and only gates a small set of snowy-biome surface branches. Documented as a known limitation (no silent wrong block); recorded under Known Stubs."
  - decision: "Model the surface column via a BlockColumn-style adapter over *level.Chunk + write heightmaps from the post-surface top"
    rationale: "Mirrors vanilla's BlockColumn abstraction and keeps BuildSurface operating directly on the renderable chunk; the WORLD_SURFACE_WG heightmap is recomputed before the walk (the walk's top input) and the 3 CLIENT heightmaps after (Pitfall 6 — a wrong heightmap mis-renders/mis-spawns)."

requirements-completed: [PARITY-01]
duration: 1h 5m
completed: 2026-06-24
---

# Phase 9 Plan 07: SurfaceSystem/SurfaceRules + Multi-Noise Biomes Summary

Ported the multi-noise Climate biome source (biome diversity by climate) and the FULL SurfaceSystem/SurfaceRules from the 26.2 jar, so the filled+carved terrain becomes a biome-VARIED world with biome-correct surfaces (grass+dirt plains, sand deserts, gravel beaches, terracotta-band badlands) plus correct CLIENT heightmaps and biome containers — pure and deterministic over the world seed, zero new dependencies.

## What shipped

### Biome half (Task 1 — committed earlier this plan, f210d705)
- `world/levelgen/biome/climate.go` — `Climate$TargetPoint` (the 6-D quantized point), `Climate$Parameter.distance`, `Climate$ParameterPoint.fitness` (the squared 7-D distance), and `Climate$ParameterList.findValueBruteForce` (the linear nearest-match, first-match-wins tiebreak). The `Sampler` binds the six Wave-3 router climate functions (temperature/vegetation→humidity/continents→continentalness/erosion/depth/ridges→weirdness) and samples them at the quart→block position, `d2f`-quantized via `quantizeCoord(f) = (long)(f*10000)`.
- `world/levelgen/biome/source.go` — `MultiNoiseBiomeSource.GetBiome(x,y,z)`: convert block→quart (`b>>2`), sample the climate, return the nearest box's biome. Parses the Wave-1 `biome_parameters.json`, surfacing a missing biome loudly.
- Tests green: `TestClimateTargetPoint`, `TestBiomeParametersParse`, `TestNearestBiomeVaries`, `TestBiomeDeterministic` (+ `TestFitnessNearestAndTiebreak`).

### Surface half (Task 2 — this session, c1ccb08c + 04313a74)
- `world/levelgen/surface/rules.go` — `ParseRuleSource` parses the overworld `surface_rule` into the ported tree with a type-dispatch (loud error on an unsupported type):
  - RuleSources: `sequence` (first-match-wins), `condition` (if/then), `block` (a resolved state), `bandlands` (the badlands terracotta band).
  - ConditionSources, each ported constant-for-constant from the nested `$` condition bytecode: `biome` (is-in-set), `stone_depth` (`depth <= 1 + offset + (addSurfaceDepth?surfaceDepth:0) + (secondaryDepthRange==0?0:(int)Mth.map(secondary,-1,1,0,range))`), `water`, `y_above`, `vertical_gradient` (the piecewise random gradient), `noise_threshold` (2-D surface noise in `[min,max]`), `above_preliminary_surface` (`blockY >= getMinSurfaceLevel`), `hole` (`surfaceDepth<=0`), `steep` (WORLD_SURFACE_WG slope ≥ 4), `temperature`, `not`.
  - A generic `{Name,Properties}` → `StateID` resolver (handles the Sulfur-fork custom blocks: `sulfur`, `cinnabar`).
- `world/levelgen/surface/system.go` — `SurfaceSystem` (the seeded `surface`/`surface_secondary`/`clay_bands_offset` noises + `getSurfaceDepth = (int)(noise*2.75 + 3.0 + at(x,0,z).nextDouble()*0.25)`, `getSurfaceSecondary`, `getBand`, `generateBands`/`makeBands`); the `Context` column state (`updateXZ`/`updateY`, `getMinSurfaceLevel` bilinear-lerp over the preliminary-surface cell, `getBiome`, `getSurfaceSecondary`); `BuildSurface` (the top-down column walk replicating the buildSurface bytecode's stoneDepthAbove/Below + waterHeight bookkeeping); `FillBiomes` (the per-section 4×4×4 biome containers); and the WORLD_SURFACE_WG recompute + the 3 CLIENT heightmap writers.
- Tests green: `TestParseSurfaceRuleSequence`, `TestSurfaceBiomeCorrect` (plains→grass+dirt, desert→sand and never grass), `TestHeightmapsWritten`, `TestBiomeSurfaceDeterministic` (+ `TestSurfaceVariesByBiome`).

## SurfaceRules vocabulary ported (cite bytecode)
Verified via `javap -c temp/cache/26.2-inner.jar`:
- RuleSources: `SurfaceRules$SequenceRule.tryApply` (first non-null), `SurfaceRules$TestRule.tryApply` (cond→followup), `SurfaceRules$StateRule.tryApply` (fixed state), `SurfaceRules$Bandlands` (`getBand`).
- ConditionSources: `SurfaceRules$StoneDepthCheck$1StoneDepthCondition.compute`, `SurfaceRules$WaterConditionSource$1WaterCondition.compute`, `SurfaceRules$YConditionSource$1YCondition.compute`, `SurfaceRules$VerticalGradientConditionSource$1VerticalGradientCondition.compute`, `SurfaceRules$NoiseThresholdConditionSource$1NoiseThresholdCondition.test`, `SurfaceRules$BiomeConditionSource$1BiomeCondition.compute`, `SurfaceRules$Context$AbovePreliminarySurfaceCondition.test`, `SurfaceRules$Context$HoleCondition.compute`, `SurfaceRules$Context$SteepMaterialCondition.compute`, `SurfaceRules$Context$TemperatureHelperCondition.compute`, `SurfaceRules$NotConditionSource`.
- Drivers: `SurfaceSystem.buildSurface`, `SurfaceSystem.getSurfaceDepth/getSurfaceSecondary/getBand/generateBands/makeBands`, `SurfaceRules$Context.updateXZ/updateY/getSurfaceSecondary/getBiome/getMinSurfaceLevel` (the bilinear `Mth.lerp2` + `Context$1`/`Context$2` 2-D/3-D noise samplers), `VerticalAnchor` (absolute / above_bottom `resolveY`).

The WHOLE overworld `surface_rule` parses (TestParseSurfaceRuleSequence) — the full 15-type vocabulary (sequence, condition, block, bandlands, biome, stone_depth, vertical_gradient, water, y_above, above_preliminary_surface, hole, steep, not, temperature, noise_threshold); an unsupported rule OR condition type errors loudly.

## Verification

| Check | Result |
|-------|--------|
| `go test ./world/levelgen/biome/ -run 'TestClimateTargetPoint\|TestBiomeParametersParse\|TestNearestBiomeVaries\|TestBiomeDeterministic'` | PASS |
| `go test ./world/levelgen/surface/ -run 'TestParseSurfaceRuleSequence\|TestSurfaceBiomeCorrect\|TestHeightmapsWritten\|TestBiomeSurfaceDeterministic'` | PASS |
| full `go test ./world/...` | PASS (all 11 packages) |
| `go vet ./world/levelgen/...` | clean |
| `go build ./...` | clean |
| `CGO_ENABLED=0 go build ./...` | clean |
| Docker `go test -race ./world/...` (golang:1.26) | clean (surface 21.97s, biome 3.29s, all green) |
| new dependencies | zero (go.mod/go.sum unchanged) |
| untouched | world/generator.go, world/worker.go, the tick, cmd/sulfur, carver/ |

## Deviations from Plan

### Rule 3 — Auto-fixed blocking issue: missing consuming seams
- **Found during:** Task 2 (wiring the surface Context).
- **Issue:** `NoiseChunk.preliminarySurfaceLevel` + the chunk pos were unexported, and `RandomState`'s base positional factory (the surfaceSystem `noiseRandom`) had no accessor — the surface package could not build its Context.
- **Fix:** Added `noisechunk.Pos()` + `noisechunk.PreliminarySurfaceLevel()` (thin wrappers over the existing unexported logic) and `router.RandomState.BaseFactory()`. Pure exposures, no behavior change.
- **Verification:** `go build ./...` + the full world suite + -race all clean.
- **Commit:** c1ccb08c.

**Total deviations:** 1 auto-fixed (Rule 3 — seam exposure). **Impact:** none on behavior; the additions are read-only accessors mirroring the existing AquiferRandom/OreRandom pattern.

## Known Stubs
- `temperatureCondition.test` (`world/levelgen/surface/rules.go`) returns `false` (conservative). The `minecraft:temperature` surface condition (`Biome.coldEnoughToSnow`) needs the per-biome temperature/snow model, which is a later biome-data concern not yet ported. A `false` result leaves the underlying grass/dirt/stone surface — the safe non-snow default — and only gates a small set of snowy-biome surface branches. Documented in-code; the loud-error discipline holds (no silent wrong block, no crash). To be resolved when the biome climate data lands.

## Threat surface
No new network/auth/file surface. The biome + surface systems read only build-trusted DATA (biome boxes + surface_rule) + the bound router + the clamped chunk position; integrity = determinism (asserted by TestBiomeSurfaceDeterministic, T-9-21), render-correctness = the heightmaps + biome-correct blocks (asserted by TestHeightmapsWritten + TestSurfaceBiomeCorrect, T-9-20), DoS = the per-quart-cell bounded nearest-match (T-9-19). All three threat-register mitigations are asserted by tests.

## Next
Wave 8's Generator wires `BuildSurface` + `FillBiomes` into the chunk pipeline after the fill+carve. Ready for the next plan (09-08, the Generator integration).

## Self-Check: PASSED
- `world/levelgen/surface/rules.go` — FOUND
- `world/levelgen/surface/system.go` — FOUND
- `world/levelgen/surface/surface_test.go` — FOUND
- `world/levelgen/biome/climate.go` / `source.go` / `climate_test.go` — FOUND (committed f210d705)
- commit `f210d705` (biome) — FOUND
- commit `c1ccb08c` (surface feat) — FOUND
- commit `04313a74` (surface test) — FOUND
