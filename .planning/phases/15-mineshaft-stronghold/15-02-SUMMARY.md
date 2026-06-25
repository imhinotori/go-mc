---
phase: 15-mineshaft-stronghold
plan: 02
subsystem: worldgen-structures
tags: [structures, stronghold, concentric-rings, global-placement, ring-spiral, biome-validation, stronghold-biased-to, sync-once, worldgen-state, placement-half, byte-inert-stub, protocol-776, STRUCT-04]

# Dependency graph
requires:
  - phase: 14-01
    provides: "the StructureStart cache + 8-radius REFERENCES, the SurfaceSampler, the StartGenerator interface + the REAL BiomeAt seam, the CompositeStartGenerator dispatch, LoadStructureSet/HasStructureBiomes + resolveBiomeTagValues nested-tag flattening"
  - phase: 15-01
    provides: "the mineshaft StartGenerator registration site in NewNoiseGenerator, the resolveBiomeTagValues recursive nested-#-ref resolver (reused defensively for the biome tag loader), the byte-inert-stub discipline"
provides:
  - "LoadConcentricRingsPlacement: the concentric_rings structure_set parse (count/distance/spread/preferred_biomes/salt) — the placement type the temples/mineshaft path rejects"
  - "LoadStrongholdBiasedTo: the #stronghold_biased_to preferred-biome name set (38 biomes), a NEW bare-biome-tag embed (only has_structure/ was embedded before)"
  - "generateRingPositions: the GLOBAL seeded biome-validated ring spiral (Pitfall #5) — concentricRingsSeed == the raw world seed, the legacy LCG, the radius/angle/round math, the per-position fork + findBiomeHorizontal reservoir adjustment"
  - "StrongholdRingState: the per-world ring-position cache (sync.Once single-compute, immutable lock-free read) + isPlacementChunk = the precomputed-list-contains test — the concentric-rings worldgen-state home, owned by the NoiseGenerator"
  - "strongholdStartGen: the placement-half StartGenerator (ring-gated anchor start, pieces STUBBED byte-inert) registered into the CompositeStartGenerator"
affects: [15-03-stronghold]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "GLOBAL (not per-chunk) structure placement: the ~128 ring positions are precomputed ONCE for the world by a seeded spiral; isPlacementChunk(pos) = the cached-list contains pos — the FIRST structure whose placement cannot be decided locally (every prior structure was a per-chunk spacing/frequency test). This breaks the implicit assumption that GenerateStarts(seed,pos) is computable from (seed,pos) alone; the stronghold gen closes over the per-world ring state."
    - "sync.Once worldgen-state caching: the expensive biome-validated spiral (~128 positions x a 28-quart-radius GetBiome search) runs a SINGLE time per world via sync.Once, then the cached []ChunkPos + a packed-pos membership map are immutable and read lock-free under concurrent worker access (-race clean). The NoiseGenerator owns the state, keeping world->world/structure one-directional (no import cycle)."
    - "the concentricRingsSeed == raw world seed derivation (createForNormal passes lload_1 for BOTH levelSeed and concentricRingsSeed): the ring RNG is the legacy java.util.Random LCG seeded directly with the world seed — NOT the SetLargeFeatureWithSalt structure-salt path the temples/mineshaft use, and NOT a seedFromHashOf. The salt field (0 for strongholds) is parsed but unused by the spiral."

key-files:
  created:
    - world/structure/concentric_rings.go
    - world/structure/concentric_rings_test.go
    - world/levelgen/data/tags/worldgen/biome/stronghold_biased_to.json
  modified:
    - tools/extract_worldgen.go
    - world/levelgen/data/embed.go
    - world/noisegen.go

key-decisions:
  - "THE RING SEED IS THE RAW WORLD SEED (javap -c ChunkGeneratorStructureState.createForNormal): the static factory passes the world seed (lload_1) for BOTH the levelSeed AND the concentricRingsSeed ctor params — there is NO salt, NO hash, NO SetLargeFeatureWithSalt. generateRingPositions does RandomSource.create().setSeed(worldSeed) on the LEGACY java.util.Random LCG (RandomSource.create() -> LegacyRandomSource), then angle0 = nextDouble()*PI*2. TestRingSeed pins this against an independent algorithm oracle; a salt-path or hash-path derivation would move every stronghold."
  - "generateRingPositions is bit-exact to the bytecode (javap -c ChunkGeneratorStructureState.generateRingPositions): per position, dist = double(4*distance + distance*ring*6) + (nextDouble()-0.5)*double(distance)*2.5; x=round(cos(angle)*dist), z=round(sin(angle)*dist); angle += 2*PI/spread; per spread-group: ring++, spread = min(spread + 2*spread/(ring+1), count-index), angle = nextDouble()*PI*2. The per-position rng.fork() is mirrored even when the biome reservoir draws nothing, so the parent RNG stream stays in lockstep. TestGenerateRingPositions pins all 128 positions vs the in-test oracle (count 128/distance 32/spread 3)."
  - "the biome adjustment ports findBiomeHorizontal (javap -c BiomeSource.findBiomeHorizontal): the rounded spiral chunk is snapped to its block-center (sectionToBlockCoord = chunk*16+8), then a quart-grid ring-border spiral out to radius 112 block / 28 quart (step 1) reservoir-samples the first/random #stronghold_biased_to match (found==nil || fork.nextInt(matchCount+1)==0); on a hit the chunk moves to blockToSectionCoord(found) = found>>4, else the original chunk stands. biomeAt is threaded as the seam (block coords -> the test stubs it; production = NoiseGenerator's GetBiome). TestGenerateRingPositionsBiomeAdjust pins a center-rejecting stub moving the position off its raw spiral chunk."
  - "THE WORLDGEN-STATE HOME (locked): StrongholdRingState lives in world/structure, constructed ONCE by the NoiseGenerator (NewStrongholdRingState(seed, biomeAt, LoadStrongholdBiasedTo())) alongside the structCache. It computes lazily-but-once via sync.Once (the spiral is expensive — many GetBiome calls — so it must NOT run per chunk or per worker). The cached []ChunkPos + a packed-pos membership map are immutable after the Once; isPlacementChunk is O(1) lock-free. TestStrongholdRingState pins compute-count==1 under 64 goroutines x 100 calls; Docker -race clean."
  - "THE NEW BARE-BIOME-TAG EMBED: only tags/worldgen/biome/has_structure/ was extracted before; the stronghold biome-validates against #minecraft:stronghold_biased_to which lives at the bare tags/worldgen/biome/stronghold_biased_to.json. Extended extract_worldgen.go's prefix list with {data/minecraft/tags/worldgen/biome/ -> tags/worldgen/biome} (re-lands has_structure/ idempotently + pulls every flat biome tag incl. stronghold_biased_to), added data.StrongholdBiasedTo + LoadStrongholdBiasedTo (38-biome set, reusing resolveBiomeTagValues defensively). TestStrongholdBiasedTo pins the 38-entry set (plains/desert/taiga/sulfur_caves IN; ocean/nether/end OUT)."
  - "THE PLACEMENT IS PROVEN IN ISOLATION (pieces stubbed, byte-inert): strongholdStartGen gates on ringState.isPlacementChunk INSTEAD of any spacing/frequency math; on a ring chunk it seeds the start RNG via SetLargeFeatureSeed(seed,cx,cz) + samples the surface Y (the seams 15-03 consumes) and returns a single anchor StructureStart{Structure: minecraft:stronghold, ChunkPos: pos, Pieces: nil}. An empty piece set is IsValid()==false -> byte-inert (writes NO blocks), mirroring 14-01's empty PLACE hook. The 5x5 reorder/emit-once/temple/mineshaft determinism gates stay byte-identical WITH the stronghold registered. 15-03 fills the recursive pieces."

patterns-established:
  - "the GLOBAL-placement de-risk pattern (mirrors 14-01 landing the pipeline before geometry): the architecturally-unique concentric_rings model is proven in isolation — the ring spiral + the dedicated seed + the biome validation + the sync.Once worldgen-state home + the placement-half anchor — all tested + deterministic, BEFORE 15-03's recursive pieces (the 'more of the same' half, reusing the 15-01 mineshaft recursion verbatim) hang on it."
  - "the per-world structure-state seam: a StartGenerator that closes over expensive per-world precomputed state (vs pure-over-(seed,pos)) — the NoiseGenerator constructs the state once and the generator reads it; any future structure needing global/precomputed placement state follows this shape"

requirements-completed: [STRUCT-04]

# Metrics
metrics:
  duration: ~75min
  tasks: 2
  files-created: 3
  files-modified: 3
  completed: 2026-06-25
---

# Phase 15 Plan 02: Stronghold Concentric-Rings Placement Summary

Ported the **stronghold PLACEMENT** (STRUCT-04, the architectural-risk half) in isolation — the unique `concentric_rings` model (Pitfall #5) — before the recursive pieces (15-03) hang on it. The stronghold's ~128 chunk positions are **GLOBAL** (precomputed once for the world by a seeded biome-validated spiral), not a per-chunk decision; `isPlacementChunk(pos)` is the precomputed-list-contains test.

## What landed

**The ring math (`generateRingPositions`)** — bit-exact `javap -c` port of `ChunkGeneratorStructureState.generateRingPositions`: the legacy LCG seeded with the **raw world seed** (`concentricRingsSeed == levelSeed`, confirmed from `createForNormal`), the per-position `dist = double(4*distance + distance*ring*6) + (nextDouble()-0.5)*double(distance)*2.5`, `round(cos/sin * dist)`, the `angle += 2*PI/spread` advance, and the per-group `spread = min(spread + 2*spread/(ring+1), count-index)` redistribution. Each position is biome-validated via a ported `findBiomeHorizontal` quart-grid reservoir search (radius 112 block / 28 quart, step 1) against `#stronghold_biased_to`, threading `biomeAt` as the seam.

**The worldgen-state home (`StrongholdRingState`)** — the ring list lives in `world/structure`, owned + constructed ONCE by the `NoiseGenerator`, fed `(seed, biomeAt, preferredSet)`. The expensive spiral runs a single time via `sync.Once`; the cached `[]ChunkPos` + a packed-pos membership map are immutable and read lock-free. `isPlacementChunk` is the O(1) contains-test.

**The new embed** — extended `extract_worldgen.go` to pull the bare `tags/worldgen/biome/` dir (so `stronghold_biased_to.json` is extractable) + `data.StrongholdBiasedTo` + `LoadStrongholdBiasedTo` (the 38-biome preferred set).

**The placement-half generator** — `strongholdStartGen` gates on `isPlacementChunk` and emits a single anchor start (`minecraft:stronghold`, `ChunkPos==pos`, **empty pieces — byte-inert**), registered into the `CompositeStartGenerator`. 15-03 fills the recursive pieces.

## Verification

- `go test ./world/structure/` green: the exact 128 ring positions vs an algorithm oracle, the dedicated ring seed, the biome adjustment, the `sync.Once` single-compute (64 goroutines), `isPlacementChunk` contains + purity, the byte-inert anchor.
- `go test ./world/` green (141s full suite): the 5x5 reorder / emit-once / temple+mineshaft acceptance stay byte-identical WITH the stronghold placement registered (stubbed pieces write nothing).
- `go build ./...` + `CGO_ENABLED=0 go build ./...` + `go vet ./...` (+ the tools module) all clean. No new deps, no import cycle (`world->world/structure` one-directional).
- **Docker `-race` clean** on `golang:1.26` (`CGO_ENABLED=1`) across `./world/structure/` and `./world/` (determinism + stronghold) — the `sync.Once` ring compute is race-free under concurrent worker access.

## Deviations from Plan

None — plan executed exactly as written. The biome-search loop was ported as `findBiomeHorizontal`'s reservoir-sampled quart-grid spiral (the bytecode showed the exact form), and the test oracle uses a center-accepting biome stub (accepting only each candidate's exact spiral-center column) to keep the biome-adjusted result equal to the raw rounded spiral — making the 128-position assertion tractable without the production code self-referencing the oracle.

## The 15-03 seam

`strongholdStartGen.GenerateStarts` already seeds the start RNG (`SetLargeFeatureSeed`) and samples the surface Y on a ring chunk — the exact inputs 15-03's recursive `StrongholdPieces` assembly consumes. 15-03 replaces the `Pieces: nil` stub with the recursive piece graph (reusing the 15-01 mineshaft `addChildren`/`FindCollisionPiece`/genDepth recursion verbatim) and `RecomputeBBox`, hanging on this proven global-ring foundation.

## Self-Check: PASSED
