---
phase: 14-structure-pipeline-temples
plan: 02
subsystem: worldgen
tags: [structures, structure-piece, desert-pyramid, postprocess, place-in-chunk, cross-chunk, rotation-mirror, worldgen, protocol-776]

# Dependency graph
requires:
  - phase: 14-01
    provides: "the structure PLACEMENT pipeline (getPotentialStructureChunk salt-14357617 math + isStructureChunk), the StructureStart cache (pure singleflight memoization + 8-radius REFERENCES), the SurfaceSampler (heightmap-at-STARTS), the StartGenerator interface carrying the REAL BiomeAt seam, the empty placeStructures PLACE hook in Decorate, BoundingBox + WritableArea + the bbox-only Piece placeholder"
  - phase: 10-worldgen-foundation
    provides: "WorldgenRandom.SetLargeFeatureSeed (the piece RNG / GenerationContext.makeRandom), the two-pass worker seam, the *world.Neighborhood 3x3 SetBlock/GetBlock proxy"
provides:
  - "world/structure piece machinery: the Piece interface extended with PostProcess + the base StructurePiece (bbox/orientation->rotation/mirror/genDepth) + getWorldX/Y/Z local->world transform"
  - "the block helpers: placeBlock (the cross-chunk clip via box.IsInside + state mirror/rotate), generateBox/generateAirBox/generateMaybeBox, fillColumnDown, maybeGenerateBlock, createChest (block + loot tag, loot deferred v3), findCollisionPiece, the addChildren recursion hook + PieceAccessor"
  - "Rotation/Mirror enums + the FACING state transform (stairs/chest); NONE=identity, the CW90/mirror path exercised for Phase-16 jigsaw reuse"
  - "StructureStart.placeInChunk (intersect-then-PostProcess, clip-to-writableBox, re-derivable piece RNG -> idempotent once-per-overlapping-chunk) + the Cache.PlaceStructures driver"
  - "the desert_pyramid StartGenerator (salt placement + getLowestY sea gate + REAL desert biome check, no accept-by-default) + DesertPyramidPiece with the full hardcoded postProcess geometry (0 .nbt) + LootChest tracking"
  - "the FILLED placeStructures PLACE hook wired into NoiseGenerator.Decorate (the first BLOCKS the structure pipeline places)"
affects: [14-03-jungle-igloo-swamp-temples, 15-mineshaft-outpost-stronghold, 16-jigsaw-villages]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "javap -c / CFR jar-port discipline extended to StructurePiece (placeBlock clip + generateBox/fillColumnDown + getWorldX/Y/Z orientation transform) + DesertPyramidPiece.postProcess (block-by-block + draw order)"
    - "placeBlock IS the cross-chunk clip (Pitfall #2): getWorldPos -> box.IsInside guard -> state transform -> SetBlock; a chunk-spanning piece writes ONLY the passed writable box's slice"
    - "the piece RNG is RE-DERIVABLE (SetLargeFeatureSeed over (seed,ownerChunkX,ownerChunkZ)) so placeInChunk from any overlapping chunk draws the same stream + clips to that chunk's slice -> idempotent placement"
    - "the WorldGenView interface (defined in world/structure, *world.Neighborhood satisfies it structurally) keeps world->world/structure one-directional (NO import cycle)"
    - "the ScatteredFeaturePiece terrain-height move (updateHeightPositionToLowestGroundHeight + -nextInt(3)) is BAKED at START time from the heightmap-at-STARTS sampler + the same RNG draw, keeping placement RNG-faithful + order-independent"

key-files:
  created:
    - world/structure/piece.go
    - world/structure/piece_test.go
    - world/structure/place.go
    - world/structure/place_test.go
    - world/structure/desert_pyramid.go
    - world/structure/desert_pyramid_test.go
  modified:
    - world/structure/start.go
    - world/structure/cache_test.go
    - world/noisegen.go
    - world/worker_structure_test.go

key-decisions:
  - "the Piece interface gains PostProcess + the canonical declaration moves from start.go (14-01's bbox-only placeholder) to piece.go; the cache/worker test fixtures (fakePiece/fixedStartPiece) get a no-op PostProcess"
  - "PostProcess takes a WorldGenView interface (SetBlock/GetBlock) NOT *world.Neighborhood, so the dependency points world->world/structure only (no cycle, T-14-10) — the Neighborhood satisfies it structurally"
  - "the orientation/rotation/mirror is ported faithfully (getRandomHorizontalDirection draws nextInt(4) over [N,E,S,W]; the FACING state transform handles stairs/chest; STRAIGHT-shape stairs are rotation/mirror-invariant so FACING-only is jar-exact for this structure)"
  - "the terrain-height move is baked at START (the bbox is moved to lowestGround + -nextInt(3) from the surface sampler) rather than re-read in postProcess — vanilla reads the live heightmap there, but the heightmap-at-STARTS sampler is the same preliminary surface, and baking keeps the RNG draw order + order-independence"
  - "LOOT/REDSTONE/SUSPICIOUS-SAND DEFERRED v3: chests place as the chest BLOCK + a loot-table tag on the start (LootChest); TNT trap places the tnt + pressure-plate BLOCKS (no redstone/entity logic); the cellar's suspicious-sand positions + the collapsed-roof randomization are skipped (afterPlace archaeology is v3). The VISIBLE pyramid is fully delivered"
  - "the fingerprint oracle uses a FIXED surface/biome stub (y=64, desert) reproducing the seed-38 (-58,32) terrain (the real router agrees) so the block-fingerprint is router-independent + stable; the owning chunk is derived FROM getPotentialStructureChunk in-test, not eyeballed"

patterns-established:
  - "structure pieces write through a WorldGenView interface; placeBlock clips to the piece bbox AND the chunk writable box (the cross-chunk mechanism every future structure reuses)"
  - "the block-fingerprint (FNV-1a over the sorted (pos,state) set) + signature-block assertions are the geometry completeness gate (a truncation flips the hash), not the min_lines floor"

requirements-completed: [STRUCT-02]

# Metrics
duration: 28min
completed: 2026-06-25
---

# Phase 14 Plan 02: Desert Pyramid Shakedown Summary

**The first true structure end-to-end: the StructurePiece bounding-box-tree machinery (placeBlock cross-chunk clip + generateBox/fillColumnDown/createChest + rotation/mirror) + StructureStart.placeInChunk + the desert pyramid (single-piece, 0 .nbt, full hardcoded postProcess) hung on 14-01's pipeline — a known seed places a vanilla-positioned pyramid whose every block is fingerprint-pinned, placed idempotently across the chunks it spans.**

## Performance

- **Duration:** ~28 min active (plus the ~10 min Docker -race gate)
- **Started:** 2026-06-25T13:15:20Z
- **Completed:** 2026-06-25T13:44:19Z
- **Tasks:** 2 (both TDD-style)
- **Files modified:** 10 (.go)

## Accomplishments

- **The StructurePiece machinery (CFR `StructurePiece`):** the `Piece` interface extended with `PostProcess(view, box, chunkPos, rng)` + the base `StructurePiece` (bbox / orientation->rotation/mirror / genDepth), `getWorldX/Y/Z` (local->world orientation transform), and the full block-helper set — `placeBlock` (the cross-chunk clip: `getWorldPos` -> `box.IsInside` guard -> mirror/rotate the state -> `SetBlock`), `generateBox`/`generateAirBox`/`generateMaybeBox`, `fillColumnDown`, `maybeGenerateBlock`, `createChest` (chest block + loot tag, loot deferred), `findCollisionPiece`, the `addChildren` recursion hook + `PieceAccessor`.
- **Rotation/Mirror (CFR `Rotation`/`Mirror`/`StairBlock`):** the enums + the FACING state transform (a type-switch over the stairs/chest the pyramid places); NONE/NORTH is the identity path the temples exercise; CW90 + LEFT_RIGHT/FRONT_BACK are unit-tested for Phase-16 jigsaw reuse.
- **`StructureStart.placeInChunk` (CFR):** intersect-then-`PostProcess` over the start's pieces, clipped to the chunk's `WritableArea`; the piece RNG is **re-derived per placement** from `(seed, ownerChunk)` via `SetLargeFeatureSeed`, so a multi-chunk structure is placed **once per overlapping chunk, idempotently** (Pitfall #2). `AfterPlace` is the documented v3 stub. `Cache.PlaceStructures` is the gather-and-place driver.
- **The desert pyramid (CFR `DesertPyramidStructure` + `DesertPyramidPiece` + `ScatteredFeaturePiece`):** `desertPyramidStartGen.GenerateStarts` = salt-14357617 `isStructureChunk` + `getLowestY` sea-level gate + a **REAL desert biome check** at chunk-center (the embedded `has_structure/desert_pyramid` {desert} set, **no accept-by-default**) + `SampleSurfaceY` project height; the orientation-draw RNG + the baked terrain-height move. `DesertPyramidPiece.PostProcess` writes the **full hardcoded geometry** block-by-block + draw order: the stepped sandstone body, the 2 corner towers, the entrance arch + corridors, the orange/blue terracotta floor **mosaic**, the hidden **TNT-trap chamber** (tnt + pressure plate) with **4 chests**, and the 26.2 **cellar** shell.
- **The filled PLACE hook:** `NoiseGenerator` registers the desert generator (replacing `NoopStartGenerator`) and `placeStructures` now dispatches `Cache.PlaceStructures` at the end of `Decorate` — **the first blocks the structure pipeline places.** The real production pipeline (router + GetBiome + STARTS + REFERENCES + PLACE) lands the seed-38 pyramid in chunk (-58,32) and gives the overlapping +x neighbor its own slice.

## Task Commits

Each task committed atomically:

1. **Task 1: the StructurePiece machinery + placeInChunk** - `560a2697` (feat)
2. **Task 2: the desert pyramid (StartGenerator + DesertPyramidPiece) + the filled PLACE hook** - `581b123c` (feat)

_TDD note: each task wrote its machinery + its pinning tests in a tight loop; the placeBlock-clip / cross-chunk / fingerprint oracles are the RED-equivalent gates (each fails on a clip/draw-order/truncation regression)._

## Files Created/Modified

- `world/structure/piece.go` - the Piece interface (+ PostProcess), base StructurePiece, WorldGenView, Rotation/Mirror + the FACING transform, placeBlock/generateBox/fillColumnDown/createChest/findCollisionPiece/addChildren, LootChest
- `world/structure/place.go` - placeInChunk (intersect+clip+re-derivable RNG, AfterPlace stub) + Cache.PlaceStructures driver
- `world/structure/desert_pyramid.go` - desertPyramidStartGen (placement + sea gate + biome gate + surface project + the baked height move) + DesertPyramidPiece + the full PostProcess geometry + the cellar room
- `world/structure/piece_test.go` - placeBlock clip, generateBox edge/fill, fillColumnDown, rotation/mirror identity+transform, findCollisionPiece, the orientation-draw order
- `world/structure/place_test.go` - cross-chunk slice/idempotence/no-double-write/skip-non-intersecting
- `world/structure/desert_pyramid_test.go` - the expected-chunk (derived from the algorithm), start-purity, the full block-fingerprint completeness oracle + signature blocks, the cross-chunk-span idempotence
- `world/structure/start.go` - removed the bbox-only Piece placeholder (moved to piece.go)
- `world/structure/cache_test.go` - fakePiece gains a no-op PostProcess
- `world/noisegen.go` - register the desert StartGenerator + fill the placeStructures PLACE hook with Cache.PlaceStructures
- `world/worker_structure_test.go` - fixedStartPiece no-op PostProcess + TestDesertPyramidPlacesInPipeline + TestDesertPyramidCrossChunkIdempotent (real pipeline)

## Decisions Made

See `key-decisions` frontmatter. The load-bearing ones: (1) PostProcess takes a `WorldGenView` interface (no world->world/structure cycle); (2) the terrain-height move is baked at START from the heightmap-at-STARTS sampler + the same `-nextInt(3)` draw (RNG-faithful, order-independent); (3) loot/redstone/suspicious-sand deferred v3 (chest BLOCK + loot tag, tnt + plate BLOCKS); (4) the fingerprint oracle uses a fixed surface/biome stub reproducing the seed-38 chunk so the geometry test is router-independent + stable.

## Deviations from Plan

None - plan executed exactly as written. Both tasks landed their specified artifacts; all ported constants/geometry were cross-checked against the decompiled 26.2-inner.jar (salt 14357617, spacing 32, separation 8, biome {desert}; the DesertPyramidPiece.postProcess block sequence + draw order; the getRandomHorizontalDirection [N,E,S,W]/nextInt(4) orientation draw). The min_lines floor (250) is comfortably exceeded; the real completeness gate is the block-fingerprint, which passes.

## Issues Encountered

- **Finding a deterministic desert-pyramid test seed:** the desert biome is abundant (~11% of chunks) but the rare jittered structure-chunks rarely land in desert above sea level for low seeds near origin. A probe scan over seeds 1-40 located seed 38 -> chunk (-58,32) (desert, surface y=64 above the sea-level gate, footprint spanning 4 chunks — an ideal cross-chunk subject). The probe test was removed after extracting the anchor; the fingerprint test uses a fixed surface/biome stub matching that chunk so it does not depend on the slow real router.
- **fillColumnDown fills to the world floor in the isolated test view:** the pyramid foundation columns fill downward to bedrock (vanilla-faithful — in the real world they hit terrain immediately). In the empty `mapView` this yields ~57k placed blocks; deterministic over the fixed y=64 sampler, so the fingerprint is stable and the count is pinned. In the real pipeline (TestDesertPyramidPlacesInPipeline) the columns stop at terrain, so the chunk holds a normal sandstone mass.

## User Setup Required

None - no external service configuration required.

## Verification

- `go test ./world/structure/ -run 'TestPlaceBlock|TestGenerateBox|TestFillColumnDown|TestRotation|TestMirror|TestFindCollisionPiece|TestPlaceInChunk|TestGetRandomHorizontal'` green: the StructurePiece machinery + the cross-chunk clip + placeInChunk idempotence.
- `go test ./world/structure/ -run 'TestDesertPyramid'` green: the pyramid lands at the expected chunk (derived from getPotentialStructureChunk), the full block-fingerprint oracle (count 57050 + FNV `0x02570922c9bdc1bf`) + the signature blocks (mosaic center, TNT, pressure plate, lintel chiseled, 4 chests) match, the start is pure over (seed,pos), the cross-chunk-span union = the whole pyramid (no double-write/truncation).
- `go test ./world/ -run 'TestDesertPyramidPlacesInPipeline|TestDesertPyramidCrossChunkIdempotent|TestDecorationReorderIdentical|TestEmitOnce'` green: the real pipeline places the pyramid + gives the overlapping neighbor its slice; re-decoration is byte-identical; the 5x5 reorder + emit-once stay byte-identical WITH the pyramid placing.
- **Docker `-race` clean** across every `./world/...` package (incl. `world` 555s + `world/structure`) on `golang:1.26`.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` + `go vet ./world/...` clean; NO new deps; NO import cycle (world->world/structure only); NO encoder/packet/chunk-wire file touched.

## Next Phase Readiness

- 14-03 (jungle temple / igloo / swamp hut) lands on a proven piece-machinery + PLACE pipeline: each cheap temple implements `StartGenerator.GenerateStarts` (its salt + biome gate) + a `PostProcess` geometry (jungle temple is multi-mechanism but still single-piece; igloo/swamp hut are tiny), reusing placeBlock/generateBox/createChest verbatim. A composite StartGenerator will dispatch all four temples.
- Phase-15 (mineshaft/outpost/stronghold) reuses `findCollisionPiece` + `addChildren` + the `PieceAccessor` for true multi-piece recursion; the BoundingBox 3D Intersects + the re-derivable-RNG cross-chunk clip already handle structures owned >=2 chunks out.
- Phase-16 (jigsaw) reuses the Rotation/Mirror state transform (the CW90/mirror path is ported + tested ahead of need).

---
*Phase: 14-structure-pipeline-temples*
*Completed: 2026-06-25*

## Self-Check: PASSED

All created files verified present on disk (piece.go, piece_test.go, place.go, place_test.go, desert_pyramid.go, desert_pyramid_test.go, 14-02-SUMMARY.md); both task commits (`560a2697`, `581b123c`) verified in git history.
