---
phase: 20-structure-polish-loot-inhabitants-beard-persistence
plan: 05
subsystem: worldgen-structures
tags: [beardifier, terrain-adaptation, density, noisechunk, fill-ordering, proto-776, struct-polish-03]

# Dependency graph
requires:
  - phase: 14-16 (structures)
    provides: StructureStart/StructurePiece + the cache (ComputeStarts) + village jigsaw + stronghold pieces
  - phase: 20-03
    provides: the StructureStart cache (computed pre-fill must read the same singleflight-memoized cache)
  - phase: 09 (PARITY-01)
    provides: the NoiseChunk cell-sample fill + final_density summation site the beard joins
provides:
  - "structure.Beardifier: ForStructuresInChunk + Compute + getBuryContribution + getBeardContribution (ported 1:1 from Beardifier)"
  - "BEARD_KERNEL (24^3 gaussian) built via the ported computeBeardContribution"
  - "terrainAdaptationFor(id): reads terrain_adaptation from embedded structure JSON (default NONE) — only village (beard_thin) + stronghold (bury) adapt"
  - "noisechunk.NewNoiseChunkWithBeard: threads an additive NON-interpolated per-block beard term into the fill summation (the BeardifierMarker substitution)"
  - "NoiseGenerator.beardifierFor: computes STARTS (C + the +-1 ring) PRE-fill and builds the chunk Beardifier"
affects: [phase-close (the v3 real-client visual gate — structures fit terrain)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Pre-fill start computation: ComputeStarts (pure (seed,pos), singleflight-deduped, memoized) is called in GenerateTerrain BEFORE NewNoiseChunk — resolving the FILL-vs-Decorate ordering hazard without triggering neighbor chunk generation"
    - "Additive non-interpolated density term: the beard is added to final_density AFTER the trilerp at the exact block coords (nc.fill), the BeardifierMarker substitution — NOT a router-graph node, NOT inside the per-marker interpolator (A5)"
    - "nil-closure byte-identity guard: NewNoiseChunkWithBeard(nil) == NewNoiseChunk — a NONE-adaptation structure contributes 0 -> identical density field"

key-files:
  created:
    - world/structure/beard.go
    - world/structure/beard_kernel.go
    - world/structure/beard_test.go
    - world/noisegen_beard_test.go
  modified:
    - world/levelgen/noisechunk/noisechunk.go
    - world/levelgen/noisechunk/fill.go
    - world/noisegen.go

key-decisions:
  - "terrain_adaptation read from the embedded worldgen/structure JSON (codec default NONE, jar-verified optionalFieldOf) — data-driven, exactly matching vanilla; among Sulfur's structures only village (beard_thin) + stronghold (bury) are non-NONE"
  - "The beard is added in nc.fill() (noisechunk.go) where the per-block final_density is assembled (the BeardifierMarker substitution site) — so EVERYTHING downstream (aquifer/computeSubstance, block placement) reads the beard-adjusted density, exactly as vanilla. fill.go's beardAt helper holds the nil-guard"
  - "Pre-fill starts gathered from C + its +-1 chunk ring only: a piece within 12 blocks of C can only come from C or an immediately-adjacent chunk (12 < 16), so the +-1 ring is the complete candidate set; ForStructuresInChunk's isCloseToChunk(12) gate then keeps only the truly-close pieces"
  - "groundLevelDelta = 0 for all current pieces (cited stub): vanilla reads PoolElementStructurePiece.getGroundLevelDelta for RIGID pool pieces and 0 for non-pool pieces; Sulfur's jigsaw placer projects every village piece to the surface (effective delta 0) and stronghold pieces are non-pool (jar-exact 0). A piece may opt in via an optional GroundLevelDelta() method when the jigsaw delta lands"
  - "JigsawJunction contributions OMITTED: Sulfur's village PoolElementStructurePiece does not yet carry a junction list; each piece is gathered as a single Rigid over its bbox (the same rigid path vanilla takes for the RIGID projection). Documented; junctions are a sub-cell refinement on the village's own footprint"
  - "afterPlace BURY block-fill NOT needed: the jar shows Structure.afterPlace is a no-op for village + stronghold (only DesertPyramidStructure + WoodlandMansionStructure override it — for suspicious-sand archaeology / cartography map, NOT terrain). The BURY terrain effect is ENTIRELY the Beardifier density term"

patterns-established:
  - "fastInvSqrt port: Mth.fastInvSqrt (0x5FE6EC85E7DE30DA magic + one Newton step) ported bit-exact for the beard falloff"
  - "BEARD_KERNEL stored float32 (the jar's d2f cast) so the kernel lookup in getBeardContribution is bit-identical to the jar"

requirements-completed: [STRUCT-POLISH-03]

# Metrics
duration: 20min
completed: 2026-06-27
---

# Phase 20 Plan 05: Beardifier terrain adaptation (village beard_thin + stronghold bury) Summary

**Structures now adapt to terrain (STRUCT-POLISH-03): the ported Beardifier density contribution raises terrain to meet a floating village (beard_thin) and digs terrain to bury a stronghold (bury), threaded as an additive non-interpolated term into the noisechunk fill — with the structure STARTS computed PRE-fill to resolve the FILL-vs-Decorate ordering hazard, and temples/igloo/mineshaft/swamp-hut (NONE) staying byte-identical.**

## Performance
- **Duration:** ~20 min
- **Tasks:** 2 (both TDD)
- **Files:** 4 created + 3 modified

## Accomplishments
- Ported `Beardifier.compute` + `getBuryContribution` (Mth.clampedMap length falloff) + `getBeardContribution` (kernel-gated `-d * fastInvSqrt(...) / 2 * BEARD_KERNEL[...]`) 1:1 from `26.2-inner.jar`, with the `BEARD_KERNEL` (24^3 gaussian) built via the ported `computeBeardContribution` at init. Cited every jar method.
- `terrainAdaptationFor(id)` reads `terrain_adaptation` from the embedded structure JSON (codec default NONE, jar-verified `optionalFieldOf`): only village (`beard_thin`) + stronghold (`bury`) gather; the 4 NONE structures yield an EMPTY Beardifier.
- Resolved THE ORDERING HAZARD (Pitfall 3): `GenerateTerrain` now computes the structure STARTS for C + its ±1 ring (`beardifierFor`) BEFORE `NewNoiseChunkWithBeard`/fill — pure, singleflight-deduped, no neighbor chunk generation.
- Threaded the additive NON-interpolated beard term into the fill summation (`nc.fill`, the `BeardifierMarker` substitution, A5) — added AFTER the trilerp at the exact block coords; nil beard -> 0 -> byte-identical.
- The NONE byte-identity guard (`TestNonAdaptingUnchanged`) + village-raise + stronghold-bury + empty-chunk tests all green; Docker `-race` over `./world/structure/ ./world/levelgen/noisechunk/ ./world/` green.

## Task Commits
1. **Task 1: Port Beardifier (forStructuresInChunk + compute + bury/beard contributions)** — `5427ad9d` (feat)
2. **Task 2: Compute starts pre-fill + thread the additive beard term into the fill** — `f9cb7ba3` (feat)

## Required SUMMARY records (per the plan output spec)

### Exact fill.go / noisechunk summation site
The beard is added at the per-block `final_density` assembly in `world/levelgen/noisechunk/noisechunk.go` `(*NoiseChunk).fill()`, immediately after the trilerp:
```go
v := nc.wrappedFinalDensity.Compute(density.Context{X: worldX, Y: worldY, Z: worldZ})
nc.fillState.filling = false
v += nc.beardAt(worldX, worldY, worldZ)   // <-- the BeardifierMarker substitution (additive, non-interpolated)
nc.density[nc.densityIndex(localX, worldY, localZ)] = v
```
`(*NoiseChunk).beardAt` lives in `world/levelgen/noisechunk/fill.go` and holds the nil-guard (returns 0 when no adapting structure influences the chunk). Because the term is baked into the stored `nc.density` field, every downstream reader (aquifer `computeSubstance`, ore veins, block placement, heightmaps) sees the beard-adjusted density — exactly as vanilla's `NoiseChunk` ctor substitutes the marker once for the whole chunk.

### Pre-fill start-computation placement in GenerateTerrain
`world/noisegen.go` `GenerateTerrain` step (0), before `NewNoiseChunkWithBeard`:
```go
beardifier := g.beardifierFor(pos)
nc := noisechunk.NewNoiseChunkWithBeard(g.router, pos, beardifier.Compute)
```
`beardifierFor(pos)` gathers `ComputeStarts(seed, C+±1-ring)` (pure geometry, singleflight-deduped, memoized — reads the SAME cache the Decorate-time STARTS/REFERENCES pass and 20-03's region persistence use; triggers NO neighbor chunk generation) and hands them to `structure.ForStructuresInChunk`.

### NONE-adaptation byte-identity held (the regression guard)
`TestNonAdaptingUnchanged` (PASS): a chunk influenced only by a `desert_pyramid` (NONE) start produces a `final_density` field byte-identical to the beard-free chunk (the Beardifier is EMPTY -> Compute 0 everywhere), AND `GenerateTerrain` re-encodes byte-identically. The 4 NONE structures (temples/igloo/mineshaft/swamp-hut) cannot change a terrain byte.

### W4 RE-SEAL outcome — NO village/stronghold golden coverage, no re-seal needed
I checked every capture/golden fixture in the repo:
- `level/chunk_capture_test.go` (`TestSectionWireVsVanillaCapture`) captures a **hand-built Superflat** chunk (seed 144), NOT the NoiseGenerator and NOT a village/stronghold.
- `net/packet/joingame_test.bin` + `server/*_capture_test.go` are **packet-wire** goldens (Phase 4/5), untouched by worldgen.
- The `world/feature_*_test.go` "fingerprints" are feature-placement determinism, not village/stronghold terrain bytes.
- `world/village_pipeline_test.go` (`TestVillageCrossChunkIdempotent`) asserts byte-identity of re-generating the SAME village chunk twice (determinism), NOT against a stored golden — and it stays green because both calls include the beard equally (the beard is pure over (seed,C)).

**Conclusion: no Phase-13/16 determinism/capture-diff golden covers a village or stronghold chunk, so NO re-seal was performed.** `TestNonAdaptingUnchanged` remains the hard byte-identity guard for the NONE structures. The village/stronghold terrain bytes DO change by design (the beard raises/buries), but no stored fixture pins them, so the change is exercised by the new village-raise / stronghold-bury tests rather than a golden delta.

## Files Created/Modified
- `world/structure/beard.go` — Beardifier (ForStructuresInChunk/Compute) + getBuryContribution/getBeardContribution + terrainAdaptationFor + the Mth ports (clampedMap/inverseLerp/clampedLerp/lerp/length/lengthSquared/fastInvSqrt) + inflatedBy + isCloseToChunk.
- `world/structure/beard_kernel.go` — BEARD_KERNEL (24^3) + computeBeardContribution (the gaussian).
- `world/structure/beard_test.go` — TestBeardContribution (exact jar values) + TestBeardKernelDeterministic + TestBeardScope + TestBeardOutsideAffectedBox + TestBeardThinRaisesBelow.
- `world/levelgen/noisechunk/noisechunk.go` — the `beard` field + NewNoiseChunkWithBeard + the `v += nc.beardAt(...)` summation.
- `world/levelgen/noisechunk/fill.go` — `(*NoiseChunk).beardAt` (the nil-guarded contribution).
- `world/noisegen.go` — `beardifierFor` (pre-fill start gather) + the GenerateTerrain step (0) wiring.
- `world/noisegen_beard_test.go` — TestNonAdaptingUnchanged + TestVillageBeardRaises + TestStrongholdBuries + TestBeardifierForEmptyChunk.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking, cross-plan] world package transiently broken by concurrent 20-02 chest work**
- **Found during:** Task 2 build (the `world` package would not compile).
- **Issue:** 20-02 (running concurrently on `world/structure/piece.go`) added `SetBlockEntity` to the `WorldGenView` interface + new `createChest` signature, but the `Neighborhood` production implementer had not yet been updated — `world` failed to build, blocking my Task 2 integration tests.
- **Resolution:** I did NOT modify `world/neighborhood.go` (it is unowned by my plan and building the chest BE NBT is 20-02's exact deliverable). I verified `world/structure` + `noisechunk` build/test in isolation, then 20-02 added `Neighborhood.SetBlockEntity` itself (the package built clean immediately after). No conflict; I stayed strictly within my file set (beard.go, beard_kernel.go, fill.go, noisechunk.go, noisegen.go + tests).
- **Files modified by me:** none beyond my own set.

**2. [Documentation] afterPlace BURY block-fill is a no-op for the beard structures**
- **Found during:** Task 2 (the plan asked to port the afterPlace BURY block-fill in the PLACE pass).
- **Finding:** `javap Structure.afterPlace` is EMPTY in the base; the ONLY overrides are `DesertPyramidStructure` (suspicious-sand archaeology) and `WoodlandMansionStructure` (cartography) — NEITHER village nor stronghold, and NEITHER a terrain fill. The BURY terrain effect is achieved entirely by the Beardifier density `bury` contribution. So no afterPlace block-fill was ported (there is nothing to port for village/stronghold).
- **Impact:** none — the success criterion (stronghold buries) is met by the density term, jar-verified.

**Total deviations:** 1 cross-plan blocking issue (resolved without touching out-of-scope files) + 1 documented jar finding (afterPlace is a no-op here).
**Impact on plan:** No scope creep. Only my declared files were committed. The afterPlace finding means the plan's optional afterPlace sub-task is vacuous for these two structures.

## Known Stubs
- **groundLevelDelta = 0** for every gathered piece — a CITED stub (Beardifier records 0 for non-pool pieces and reads getGroundLevelDelta for RIGID pool pieces; Sulfur's village pieces are surface-projected so the effective delta is 0, and stronghold pieces are non-pool = jar-exact 0). Structured so a piece can opt in via an optional `GroundLevelDelta() int` method when the jigsaw delta lands — never baked away.
- **JigsawJunction contributions omitted** — Sulfur's PoolElementStructurePiece has no junction list yet; each piece is a single Rigid over its bbox (the RIGID-projection path). A sub-cell refinement on the village's own footprint; documented in beard.go.

## NOTE FOR PHASE CLOSE (flagged to the orchestrator)
This is the FINAL v3 plan. The phase (and v3) should close with an **autonomous:false real-client visual gate**: connect a vanilla 26.2 client and confirm structures fit terrain (villages sit ON the ground / no floating; strongholds buried), plus the 20-01/02/04 deliverables (open a structure chest with vanilla loot; see villagers/witch/cat). Recommend a closing **human-verify checkpoint**.

## Self-Check: PASSED

---
*Phase: 20-structure-polish-loot-inhabitants-beard-persistence*
*Completed: 2026-06-27*
