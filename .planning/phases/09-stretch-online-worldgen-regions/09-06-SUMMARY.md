---
phase: 09-stretch-online-worldgen-regions
plan: 06
subsystem: worldgen
tags: [carver, ravine, cave, worldcarver, aquifer, legacy-random, lcg, parity, minecraft-26.2]

# Dependency graph
requires:
  - phase: 09-01
    provides: configured_carver/{cave,canyon,cave_extra_underground}.json + overworld_carver_replaceables tag (data.ConfiguredCarver / data.CarverReplaceables)
  - phase: 09-02
    provides: the worldgen seeding chain (the carver re-uses the world seed; it ports its OWN legacy LCG for the per-source-chunk carver seed)
  - phase: 09-05
    provides: the Aquifer (computeSubstance) — consumed via the FluidSource interface so a carve below the fluid level floods
provides:
  - "world/levelgen/carver/ — the legacy WorldCarver pass: CaveWorldCarver (extra tunnel caves) + CanyonWorldCarver (RAVINES), aquifer-aware, replaceables-gated, cross-chunk-continuous, deterministic over the world seed"
  - "ApplyCarvers(worldSeed, chunk, fluid, carvers, replaceables) — the applyCarvers driver the Wave-8 Generator runs after doFill, before the surface"
  - "ParseCarverConfig / ParseReplaceables / LoadOverworldCarvers — the carver DATA parsed from the Wave-1 embed"
  - "a ported java.util.Random LCG (legacyRandom) + WorldgenRandom.setLargeFeatureSeed — the per-chunk carver seed"
affects: [09-08-generator, worldgen-surface, mineshaft-structures-deferred]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "FluidSource interface decouples the carver (a separate package) from the noisechunk Aquifer (whose computeSubstance is unexported) — the Generator wires the Aquifer; tests inject a flat water table"
    - "CarveChunk.Set drops out-of-footprint writes — a carve started in a neighbor source chunk only edits blocks inside the target chunk (cross-chunk reach without touching other chunks)"
    - "the legacy LCG (java.util.Random) lives in the carver package — distinct from the xoroshiro source the noise stack uses; the carver pass seeds from it exactly as vanilla's WorldgenRandom does"

key-files:
  created:
    - "world/levelgen/carver/config.go — CarverConfig + Replaceables (DATA parse) + the float/height value providers"
    - "world/levelgen/carver/random.go — the LegacyRandomSource/BitRandomSource LCG + setLargeFeatureSeed"
    - "world/levelgen/carver/carver.go — WorldCarver base (carveEllipsoid/carveBlock/canReplaceBlock/getCarveState/canReach + CarvingMask) + ApplyCarvers"
    - "world/levelgen/carver/cave.go — CaveWorldCarver (createRoom + createTunnel tunnel walk)"
    - "world/levelgen/carver/canyon.go — CanyonWorldCarver (doCarve + initWidthFactors + updateVerticalRadius ravine walk)"
    - "world/levelgen/carver/carver_test.go — the 8 verification tests"
  modified: []

key-decisions:
  - "The carver consumes the Aquifer via a FluidSource interface (CarveFluid) rather than calling noisechunk.computeSubstance directly: that method is unexported and the carver is a separate package. The Wave-8 Generator wires the real Aquifer; this keeps the carver pure + testable and avoids an import cycle."
  - "The nested #minecraft:* block tags inside overworld_carver_replaceables (base_stone_overworld, substrate_overworld, sand, terracotta, iron_ores, copper_ores, snow) are NOT embedded as DATA (only the top-level tag is), so their membership is resolved constant-for-constant from the vanilla 26.2 block-tag definitions, then expanded over ALL state variants of each block id."
  - "The per-source-chunk carver seed is setLargeFeatureSeed(worldSeed + carverIndex, srcX, srcZ) using the ported legacy LCG — the carver INDEX in the biome's ordered list salts the seed (load-bearing: a different order = a different world)."

patterns-established:
  - "Carve-pass packaging: a self-contained world/levelgen/carver/ package that READS the chunk via CarveChunk and the aquifer via FluidSource, both supplied by the Generator — no direct coupling to noisechunk/router/density."

requirements-completed: [PARITY-01]

# Metrics
duration: 30 min
completed: 2026-06-24
---

# Phase 9 Plan 06: WorldCarver Pass (Ravines + Extra Tunnel Caves) Summary

**Ported the legacy 26.2 WorldCarver pass — CaveWorldCarver (winding tunnel caves) + CanyonWorldCarver (RAVINES) — that runs ON TOP of the noise terrain: a seeded, aquifer-aware, replaceables-gated, cross-chunk-continuous carve that produces the player's recognizable caves + ravines (the "minas" answer, carver class).**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-06-24T21:18Z
- **Completed:** 2026-06-24T21:48Z
- **Tasks:** 2 (both TDD)
- **Files created:** 6 (config, random, carver, cave, canyon, test)

## Accomplishments

- **CanyonWorldCarver = RAVINES**: a single long ravine path whose per-Y `widthFactors` profile + the `dy²/6` membership term make the carved region TALL and NARROW (test measured height=12 vs width=5 inside one chunk — the ravine signature).
- **CaveWorldCarver = the extra tunnel caves**: a random branch count, optional cave room (`createRoom`), and the `createTunnel` ellipsoid-segment random walk (yaw/pitch deltas, branch forking at the midpoint, the 3-in-4 gap), carving connected tunnels of air.
- **Aquifer-aware carving**: `getCarveState` places lava below the config's lava level, else queries the `FluidSource` (the Wave-5 Aquifer) — a carve below the local fluid level floods with water, above is cave_air. Proven by `TestCarverAquiferAware`.
- **Cross-chunk continuity**: `ApplyCarvers` iterates the `[-8,8]×[-8,8]` source-chunk box (getRange()=4 → range 8) and seeds each source chunk independently, so a carve started in a neighbor reaches the target (proven: a cave seeded in (1,0) carved 119 blocks into (0,0)).
- **Replaceables gate**: only blocks in `overworld_carver_replaceables` are carved (stone/dirt/deepslate/etc.); bedrock and non-replaceable blocks are left intact (`TestCanReplaceGate`, and the cave test asserts the bedrock floor is never carved).
- **Determinism**: same seed + chunk → byte-identical carve, for both cave and canyon (`TestCarveDeterministic`, `TestApplyCarversSeeded`).

## Bytecode Provenance (javap -c, temp/cache/26.2-inner.jar)

Ported constant-for-constant, cited inline in each file:

- **WorldCarver** (`carver.go`): `getRange()=4`; `carveEllipsoid` (the per-block normalized ellipsoid `ndx²+ndz²<1` then `skip(ndx,ndy,ndz)`, bounded to the 16×16 footprint + the `minGenY+1 .. minGenY+height-1-7` y-band); `carveBlock`→`canReplaceBlock`(state.is(replaceable)) → `getCarveState` (y≤lavaLevel→lava, else `aquifer.computeSubstance(pos,0.0)`); `canReach` (`dx²+dz²-remaining² ≤ (radius+2+16)²`); the `CarvingMask` visited bitset.
- **applyCarvers** (`ApplyCarvers` in `carver.go`): the `[-8,8]` source-chunk double loop, `WorldgenRandom.setLargeFeatureSeed(worldSeed + carverIdx, srcX, srcZ)`, `isStartChunk` probability roll, `carve()` into the target.
- **CaveWorldCarver** (`cave.go`): `carve` (`j=(7)<<4=112`, `tunnelCount=nextInt(nextInt(nextInt(15)+1)+1)`, the `nextInt(4)==0` room branch + `branchCount+=nextInt(4)`); `createRoom` (`hr=1.5+sin(π/2)*caveRadius`); `createTunnel` (the yaw/pitch random walk, `branchPoint=nextInt(count/2)+count/4`, the `0.92/0.7` pitch decay, the fork at the branch point, the `nextInt(4)==0` gap); `getThickness`; `getCaveBound=15`; `getYScale=1.0`; `shouldSkip`.
- **CanyonWorldCarver** (`canyon.go`): `carve` (`j=112`, `segmentCount=(int)(j*distanceFactor)`); `doCarve` (the single path, `radius=1.5+sin(π*seg/count)*thickness`, `hr=radius*horizontalRadiusFactor`, `vr=updateVerticalRadius(radius*yScale)`, the `0.7/0.05/0.8/0.5` walk decay); `initWidthFactors` (the per-Y squared-width array jumping every `widthSmoothness` rows); `updateVerticalRadius` (`1-|0.5-seg/count|*2` center taper × `randomBetween(0.75,1.0)`); `shouldSkip` (`dx²+dz²·widthFactors[i-1]+dy²/6 < 1` — the tall-narrow term).
- **LegacyRandomSource / BitRandomSource** (`random.go`): `setSeed=(s^0x5DEECE66D)&mask`; `next(bits)`; `nextInt(bound)` (power-of-two fast path + rejection loop); `nextLong`; `nextFloat`; `setLargeFeatureSeed`.
- **CaveCarverConfiguration / CanyonCarverConfiguration$CanyonShapeConfiguration** + the `UniformFloat`/`TrapezoidFloat`/`UniformHeight` providers (`config.go`).

## Task Commits

1. **Task 1: carver DATA + WorldCarver base + ApplyCarvers driver** — `1254b21a` (feat)
2. **Task 2: CaveWorldCarver tunnels + CanyonWorldCarver ravines** — `72a28919` (feat)

_TDD: each task landed its tests + implementation together (the plan's per-task verify runs all tests green)._

## Verification Results

- `go test ./world/levelgen/carver/ -run '<all 8>' -count=1` → **PASS** (TestParseCarverConfigs, TestCanReplaceGate, TestApplyCarversSeeded, TestCarverAquiferAware, TestCaveCarves, TestCanyonCarvesRavine, TestCarveDeterministic, TestCarveContinuousAcrossChunks)
- `go test ./world/...` → **PASS** (no regression in biome/density/noisechunk/router/synth/data)
- `go build ./...` → exit **0**; `go vet ./...` → **clean**
- **Zero new dependencies** (go.mod / go.sum unchanged); CGO_ENABLED=0 clean
- **Docker `-race` over `./world/...`** (golang:1.26) → **clean** (the carver is pure/deterministic — no goroutines)
- Forbidden/adjacent files (world/generator.go, world/worker.go, cmd/sulfur, levelgen/biome, levelgen/surface, levelgen/density, levelgen/router, levelgen/noisechunk) → **untouched** (git diff confirmed)

## Mineshafts-as-Structures: DEFERRED (scoped-out, not dropped)

The user's "minas" spans two systems. This plan delivers the **carver class**: RAVINES (CanyonWorldCarver) + the extra winding tunnel caves (CaveWorldCarver). **Mineshafts-as-STRUCTURES remain DEFERRED** — the StructureFeature system (StructureStart / StructurePiece / jigsaw placement) is a large separate subsystem not in scope here. Documented explicitly so it is scoped-out rather than silently missed. Trees/vegetation features (the feature/decoration pass) are likewise a separate deferred subsystem.

## Decisions Made

See `key-decisions` in the frontmatter. Most consequential: the **FluidSource seam** (carver consumes the Aquifer through an interface, the Generator wires it) and the **nested-tag resolution** (the base block tags are not embedded, so their membership is ported from the vanilla tag defs).

## Deviations from Plan

The plan's `<interfaces>` block referenced `world/levelgen/aquifer.go` with an exported `func (aq *Aquifer) computeSubstance(...)`. The actual Wave-5 Aquifer lives in **`world/levelgen/noisechunk/aquifer.go`** and its `computeSubstance` is **unexported**. Rather than modify the noisechunk package (out of this plan's scope) or export an internal method, I introduced the **`FluidSource` interface** in the carver package — the Generator (Wave 8) wires the noisechunk Aquifer to it, tests inject a flat water table. This is the idiomatic seam (the same shape vanilla's CarvingContext uses to reach the aquifer) and keeps the carver a clean separate package. Classified as a [Rule 3 - Blocking] resolution (the stated interface did not exist as described); no behavior change, no scope creep.

**Two honest in-code deferrals** (documented in comments, do NOT affect cave/ravine shape):
- `carveBlock` omits vanilla's grass/mycelium **top-material restoration** (when carving through a grass surface, vanilla replaces the block below with the biome top material). That is a surface-cosmetic refinement belonging to the Wave-7 surface pass; the air/water placement the cave/ravine shape needs is faithful.
- `carveEllipsoid` uses the constant upgrade margin `7` (vanilla's non-upgrading-chunk path) for the top y-bound — Sulfur never generates "upgrading" chunks, so the branch is fixed at the live value.

**Total deviations:** 1 (Rule 3 — the interface seam) + 2 documented surface-cosmetic deferrals. **Impact:** none on the carve geometry or determinism; the FluidSource seam is strictly cleaner than the plan's assumed direct call.

## Issues Encountered

None beyond the interface mismatch above (resolved via the FluidSource seam). The stale-LSP caveat held — `go build ./...` (exit 0) + `go test` were the authority; no phantom-error chasing needed.

## Next Phase Readiness

- **Wave 8 (Generator) is unblocked**: it calls `ApplyCarvers(worldSeed, chunk, aquiferFluidSource, LoadOverworldCarvers(), ParseReplaceables())` after `noisechunk.Fill`, before the surface pass. It must supply a `CarveChunk` adapter over the chunk sections (Get/Set with the 16×16 footprint bound) and a `FluidSource` adapter over the noisechunk Aquifer (CarveFluid → computeSubstance at density 0).
- **Carvers run on top of the noise caves**: caves (negative final_density) + carved tunnels + ravines together = explorable caves + ravines, the full-parity gate.
- No blockers. Mineshafts-as-structures + the feature/decoration pass remain the documented deferrals.

## Self-Check: PASSED

- All 6 created files exist on disk (config.go, random.go, carver.go, cave.go, canyon.go, carver_test.go).
- Both task commits exist in git history (1254b21a, 72a28919).
- All 8 plan tests pass; go build/vet clean; -race clean; zero new deps.

---
*Phase: 09-stretch-online-worldgen-regions*
*Completed: 2026-06-24*
