---
phase: 15-mineshaft-stronghold
plan: 01
subsystem: worldgen-structures
tags: [structures, mineshaft, recursive-pieces, addchildren, findcollisionpiece, frequency-reducer, legacy-type-3, cross-chunk, biome-tag-resolution, protocol-776, STRUCT-03]

# Dependency graph
requires:
  - phase: 14-01
    provides: "the placement math (getPotentialStructureChunk/isStructureChunk), the four probabilityReducer variants + ApplyFrequencyReducer, the StructureStart cache + 8-radius REFERENCES, the SurfaceSampler, the StartGenerator interface + the REAL BiomeAt seam, LoadStructureSet + HasStructureBiomes"
  - phase: 14-02
    provides: "the StructurePiece machinery (Piece+PostProcess, placeBlock cross-chunk clip, generateBox/generateAirBox/generateMaybeBox/fillColumnDown/createChest), the PieceAccessor + FindCollisionPiece recursion hooks, StructureStart.placeInChunk + the re-derivable piece RNG, the CompositeStartGenerator registration site"
  - phase: 14-03
    provides: "the addChildren multi-piece pattern (first exercised by the igloo, fixed 1-3 set), the per-temple StartGenerator template (frequency/biome gate + RecomputeBBox), the structures-pipeline acceptance test shape"
provides:
  - "the CORRECTED FrequencyReductionMethod enum->reducer table: legacy_type_1->legacyPillagerOutpostReducer, legacy_type_3->legacyProbabilityReducerWithDouble (Phase-14 had these SWAPPED) + the corrected legacyProbabilityReducerWithDouble seeding SetLargeFeatureSeed(seed,chunkX,chunkZ)"
  - "HasStructureBiomes nested-tag resolution: #minecraft:is_* category refs flatten recursively (the mineshaft/mesa has_structure tags are nested-ref tags, unlike the flat temple tags); data.BiomeCategoryTag + the embedded is_* biome category tag set"
  - "the mineshaft StartGenerator (NewMineshaftStartGen): the legacy_type_3 frequency-reduction PLACEMENT path (Pitfall #4 — NOT the spacing-grid path), the normal/mesa weighted pick, the REAL biome gate, the recursive piece-graph driver"
  - "the recursive MineshaftPieces graph (corridor/crossing/room/stairs) on the addChildren + FindCollisionPiece machinery, genDepth-bounded (cap 8), collision-pruned — the FIRST true recursive structure; the stronghold (15-03) reuses this verbatim"
affects: [15-03-stronghold]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the recursive piece-graph driver: a mineshaftBuilder (PieceAccessor over an in-progress []Piece) is seeded with the root, then a pieceChildContext{acc, rng} is driven through each piece's AddChildren — generateAndAddPiece proposes a child (genDepth-capped), constructs its bbox at the jar-exact exit offset, FindCollisionPiece-prunes overlaps, AddPiece's survivors, and recurses. The genDepth>8 bound + collision pruning guarantee TERMINATION."
    - "SOUTH-identity orientation for axis-stable pieces (room/crossing/stairs): setOrientation(South,true) gives getWorldX=bbox.MinX+x / getWorldZ=bbox.MinZ+z / getWorldY=y+bbox.MinY (no axis swap), so a generateBox sized from the bbox extents stays INSIDE the bbox — the cross-chunk clip (placeInChunk's bbox-intersect) then reproduces every write per overlapping chunk. The corridor keeps its dir-oriented frame (its local2 long-axis maps self-consistently)."
    - "nested biome-tag flattening: resolveBiomeTagValues recurses #-prefixed values through data.BiomeCategoryTag with a visited-set cycle guard, unioning concrete biome ids — the vanilla TagLoader semantics, first needed by the mineshaft (the temples had flat tags)"

key-files:
  created:
    - world/structure/mineshaft.go
    - world/structure/mineshaft_pieces.go
    - world/structure/mineshaft_test.go
    - world/levelgen/data/tags/worldgen/biome/is_badlands.json (+ 13 more is_* category tags embedded)
  modified:
    - world/structure/placement.go
    - world/structure/placement_test.go
    - world/structure/structure_set.go
    - world/levelgen/data/embed.go
    - world/noisegen.go

key-decisions:
  - "THE LOAD-BEARING FIX (Task 1): the Phase-14 placement.go FrequencyReductionMethod table was MIS-PORTED — legacy_type_1 <-> legacy_type_3 were SWAPPED, and legacyProbabilityReducerWithDouble was mis-seeded (z,salt). JAR GROUND TRUTH (StructurePlacement\$FrequencyReductionMethod, cross-checked vs pillager_outposts.json legacy_type_1 + mineshafts.json legacy_type_3): legacy_type_1->legacyPillagerOutpostReducer, legacy_type_3->legacyProbabilityReducerWithDouble; the latter seeds SetLargeFeatureSeed(seed,chunkX,chunkZ) (the bytecode loads iload_3,iload_4=chunkX,chunkZ), then nextDouble()<freq. Un-swapped the dispatch + re-seeded. The research v2-structures.md:374-376 names the WRONG reducer (legacyArbitrarySaltProbabilityReducer, which is legacy_type_2) — disregarded per the plan; the jar is authoritative. TestFrequencyReductionMethod pins all four bindings via independent per-reducer oracles."
  - "DEVIATION (Rule 3 — blocking, no architectural change): the mineshaft + mesa has_structure biome tags are NOT flat biome lists (the temples were) — they are expressed as nested #minecraft:is_* category refs (mineshaft: is_ocean/is_river/.../is_badlands; mesa: #is_badlands). HasStructureBiomes stored raw values, so the nested refs never resolved to concrete biomes -> the mesa variant could NEVER place + normal placed in far fewer biomes than vanilla. The is_* category tags were also NOT embedded (only has_structure/ was). FIX: copied the 14 transitively-needed is_* tags into the embed tree, added data.BiomeCategoryTag, and made HasStructureBiomes resolve nested #-refs recursively (visited-set cycle guard). This is a correctness requirement (vanilla-faithful biome gate), not new scope — the plan's 'CONFIRM HasStructureBiomes resolves them' surfaced a real gap."
  - "the mineshaft PLACEMENT is the legacy frequency-reduction path (Pitfall #4), NOT the spacing-grid path: mineshafts.json spacing 1 makes IsStructureChunk ALWAYS true (floorDiv(cx,1)=cx, nextInt(1)=0 -> start==chunk), so the DECISIVE gate is ApplyFrequencyReducer's 0.4%% legacy_type_3 draw. TestMineshaftPlacementGate derives the probed pass/fail chunk FROM the corrected reducer math (not eyeballed)."
  - "the genDepth cap is 8 (mineshaftGenDepthCap — the jar's `if (genDepth > 8) return null` bound in MineShaftPieces.generateAndAddPiece); a candidate proposed at depth > 8 is rejected, so the corridor graph TERMINATES. TestMineshaftTerminates pins a bounded piece count + no-overlap across six seeds."
  - "per-type block tables (MineShaftPiece's mineshaftType switch): normal = oak_planks/oak_fence/oak_log/rail/torch/chest/cobweb/cave_air; mesa = dark_oak_planks/dark_oak_fence/dark_oak_log. planksState/fenceState/logState resolve them via the block.ToStateID path (the stateOf helper, shared with the temples)."
  - "LOOT/ENTITY DEFERRED v3 (matches the Phase-14 temple chest precedent): corridor chests place as the chest BLOCK + the loot tag minecraft:chests/abandoned_mineshaft (no loot rolled, tracked on the corridor's LootChests). The minecart-with-chest entities + the rail-on-chest detail are entity/v3 deferrals. The VISIBLE corridor (planks/rails/torches/support beams/cobwebs/the chest block) is fully delivered."

patterns-established:
  - "the recursive multi-piece structure pattern (builder + pieceChildContext + generateAndAddPiece) the stronghold (15-03) reuses verbatim — the stronghold's concentric-ring piece graph is the same addChildren/FindCollisionPiece/genDepth-bounded recursion over a different piece set"
  - "the cross-chunk correctness pattern proven for REAL on the first genuinely-many-chunk structure: a mineshaft spans 14x16 chunks (TestMineshaftCrossChunk asserts spanned>=2, then union-over-overlapping-chunks == whole-graph, idempotent) — the +-8 REFERENCES + placeInChunk clip + re-derivable piece RNG are load-bearing here, not latent"

requirements-completed: [STRUCT-03]

# Metrics
duration: 55min
completed: 2026-06-25
---

# Phase 15 Plan 01: Mineshaft (First Recursive Multi-Piece Structure) Summary

**The mineshaft — STRUCT-03, the FIRST true recursive structure — lands on Phase-14's piece machinery after a JAR-CONFIRMED reducer FIX: the mis-ported legacy_type_1<->legacy_type_3 enum swap is un-swapped and legacyProbabilityReducerWithDouble is re-seeded (seed,chunkX,chunkZ), so the mineshaft places at the vanilla 0.4%% frequency via the legacy frequency-reduction path (Pitfall #4, NOT the spacing grid); the recursive corridor/crossing/room/stairs graph grows on the addChildren + FindCollisionPiece machinery, genDepth-bounded (cap 8) and collision-pruned so it terminates, with per-type normal/mesa block tables; the multi-chunk mineshaft places idempotently once-per-overlapping-chunk (the first genuinely-many-chunk structure validates the +-8 REFERENCES + clip for real), and the 5x5/emit-once determinism stays byte-identical with the mineshaft live.**

## The reducer FIX (Task 1, load-bearing)

The Phase-14 `world/structure/placement.go` `FrequencyReductionMethod` table was mis-ported. The JAR-correct enum->reducer bindings (decompiled from `StructurePlacement$FrequencyReductionMethod`, cross-checked against the on-disk `pillager_outposts.json`=legacy_type_1 and `mineshafts.json`=legacy_type_3):

| enum string   | reducer                                | seed call / draw                                          |
|---------------|----------------------------------------|-----------------------------------------------------------|
| default       | probabilityReducer                     | setLargeFeatureWithSalt(seed,x,z,salt); nextFloat()<freq  |
| legacy_type_1 | legacyPillagerOutpostReducer           | i=x>>4,j=z>>4; setSeed((i^(j<<4))^seed); nextInt; nextInt(1/freq)==0 |
| legacy_type_2 | legacyArbitrarySaltProbabilityReducer  | setLargeFeatureWithSalt(seed,z,salt,10387320); nextFloat()<freq |
| legacy_type_3 | legacyProbabilityReducerWithDouble     | setLargeFeatureSeed(seed,chunkX,chunkZ); nextDouble()<(double)freq |

The existing code had **legacy_type_1 <-> legacy_type_3 SWAPPED** AND `legacyProbabilityReducerWithDouble` **mis-seeded `(z,salt)`** where the bytecode loads `iload_3,iload_4 = chunkX,chunkZ`. Both fixed. `TestFrequencyReductionMethod` pins all four bindings via independent per-reducer oracles + the corrected `(chunkX,chunkZ)` seeding (asserting it differs from the old `(z,salt)` draw). `TestMineshaftFrequencyReducerBindings` pins the two on-disk fixtures end-to-end. The four temples are frequency 1.0, so `ApplyFrequencyReducer` short-circuits true regardless of method — the fix does NOT regress them (`TestStructuresPipelineAcceptance` stays green).

## The mineshaft

- **Placement (Pitfall #4):** `mineshafts.json` spacing 1 / frequency 0.004 / `legacy_type_3` -> EVERY chunk is a candidate (`IsStructureChunk` always true), gated by the corrected `legacyProbabilityReducerWithDouble` 0.4%% draw. `NewMineshaftStartGen` gates on `ApplyFrequencyReducer`, NOT `IsStructureChunk`.
- **Two types:** the structure_set weighted pick (both weight 1) chooses `minecraft:mineshaft`(normal) or `minecraft:mineshaft_mesa`(mesa); each gates on the REAL biome at the chunk-center surface in its `has_structure` allow-set (no accept-by-default).
- **The recursive graph:** the root `MineshaftRoom` seeds a `mineshaftBuilder` (PieceAccessor), then `AddChildren` drives `generateAndAddPiece` to grow corridors/crossings/stairs — each child genDepth-capped (8), collision-pruned via `FindCollisionPiece`, recursed to fixpoint. `RecomputeBBox` encapsulates the whole graph so the +-8 REFERENCES finds it.
- **Geometry (0 .nbt, hardcoded postProcess):** corridor floor/rail/torch/oak-fence support beams/cobweb; crossing + room floors; stairs descent — via `placeBlock`/`generateBox`/`generateMaybeBox`. Mesa swaps to dark_oak. Chests via `createChest` (block + `minecraft:chests/abandoned_mineshaft` tag, loot deferred v3).
- **Registered** into the `CompositeStartGenerator` alongside the four temples in `world/noisegen.go`.

## Task Commits

1. **Task 1 (reducer fix + biome-tag resolution):** `5527fdeb` (fix) — un-swap legacy_type_1/legacy_type_3, re-seed legacyProbabilityReducerWithDouble, TestFrequencyReductionMethod (4 bindings), nested #is_* biome-tag resolution + the embedded is_* category tags.
2. **Task 2 (mineshaft pieces + start gen + registration):** `7a25d38f` (feat) — the recursive corridor/crossing/room/stairs graph, the StartGenerator, the composite registration, the full mineshaft acceptance suite.

## Deviations from Plan

**1. [Rule 3 - blocking] HasStructureBiomes nested-tag resolution + embedded is_* category tags**
- **Found during:** Task 1 (confirming `HasStructureBiomes("mineshaft"/"mineshaft_mesa")` resolves).
- **Issue:** The plan assumed the mineshaft/mesa biome tags resolve via the existing loader "no new extractor work". In reality both tags are expressed as nested `#minecraft:is_*` category references (mesa = only `#minecraft:is_badlands`; normal = many `#is_*`), and `HasStructureBiomes` stored raw values without resolving `#`-refs — so the mesa variant could NEVER match a real biome (badlands) and normal matched far fewer biomes than vanilla. The `is_*` category tags were also not embedded (only `has_structure/` was).
- **Fix:** Copied the 14 transitively-needed `is_*` biome category tags into the embedded data tree, added `data.BiomeCategoryTag`, and made `HasStructureBiomes` recurse nested `#`-refs (`resolveBiomeTagValues`, visited-set cycle guard) — the vanilla TagLoader union semantics. No architectural change; the temple flat-tag tests stay green. Pinned by `TestMineshaftMesaBiome` (badlands resolves) + the normal allow-set breadth check.
- **Files:** `world/structure/structure_set.go`, `world/levelgen/data/embed.go`, the 14 embedded `is_*.json`.
- **Commit:** `5527fdeb`

**2. [Rule 1 - bug, found+fixed in-task] MineShaftStairs out-of-bbox writes broke cross-chunk placement**
- **Found during:** Task 2 (`TestMineshaftCrossChunk` failing — a stairs piece wrote z=1392 while its bbox MaxZ was 1388).
- **Issue:** `MineShaftStairs` used `setOrientation(dir,true)` whose rotating EAST/WEST mappings swap the local X/Z axes, but its `generateBox` sized the local frame from `bbox.MaxX-MinX` regardless — so for an X-long (E/W) stairs the full box escaped the declared bbox, and `placeInChunk` (which places a piece only into chunks its bbox intersects) failed to reproduce the escaped writes per-chunk.
- **Fix:** Switched the room/crossing/stairs to SOUTH-identity orientation (`getWorld* = bbox.Min + local`, no axis swap), carrying the stairs' descent direction in a separate `descentDir` field for `AddChildren`. The corridor keeps its dir-oriented frame (its long-axis `local2` maps self-consistently). All writes now stay inside each piece bbox -> the cross-chunk clip reproduces them per chunk.
- **Files:** `world/structure/mineshaft_pieces.go`
- **Commit:** `7a25d38f`

## Loot / Entity Deferrals (v3, documented)

- Corridor chests place as the chest BLOCK + the loot tag `minecraft:chests/abandoned_mineshaft` (no loot rolled), tracked on the corridor's `LootChests`.
- The minecart-with-chest entities + the rail-on-chest detail are entity/v3 deferrals.

## Known Stubs

None that block the plan goal. The loot/entity deferrals above are documented v3 subsystems, not stubs preventing generation — the visible mineshaft (full corridor/crossing/room/stairs geometry + rails/torches/support beams/cobwebs + the chest block) is fully delivered and fingerprint-pinned.

## Verification

- `go test ./world/structure/ -run 'TestFrequencyReductionMethod|TestMineshaft'` green: all four jar-correct reducer bindings + the corrected seeding pinned; the mineshaft places via the corrected frequency gate (not the spacing grid); the recursive graph terminates (bounded count, no overlap across 6 seeds) + fingerprint-matches; the chest places as block+tag; the multi-chunk span is idempotent (union==whole, byte-identical re-place); the start is pure; the mesa nested-tag biome resolves.
- `go test ./world/ -run 'TestDecorationReorderIdentical|TestEmitOnce|TestStructuresCrossChunkIdempotent|TestDesertPyramid'` green: the 5x5 reorder-determinism + emit-once stay byte-identical with the mineshaft live; the temples stay green (the reducer fix does not regress them).
- `go test ./world/ ./world/structure/` full suites green.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` + `go vet ./world/...` clean; go.mod/go.sum UNCHANGED (zero new deps); NO encoder/packet/chunk-wire/golden file touched.
- Docker `-race` over `./world/structure/` (2.9s) AND `./world/` (445s) CLEAN (golang:1.26, CGO_ENABLED=0) — no data race in the recursive assembly / cross-chunk placement.

## Next Phase Readiness

- **Phase 15-03 (stronghold)** reuses the recursive piece-graph pattern (mineshaftBuilder + pieceChildContext + generateAndAddPiece + FindCollisionPiece + the genDepth bound) verbatim — the stronghold's concentric-ring piece set is the same recursion over different pieces. The cross-chunk re-derivable-RNG clip is now proven on a genuinely-many-chunk structure.
- The corrected legacy reducer table also unblocks the **pillager outpost** (legacy_type_1) if a later plan adds it — its reducer binding is now jar-correct + pinned.

---
*Phase: 15-mineshaft-stronghold*
*Completed: 2026-06-25*

## Self-Check: PASSED

All 4 created files verified present (mineshaft.go, mineshaft_pieces.go, mineshaft_test.go, 15-01-SUMMARY.md); both task commits (`5527fdeb`, `7a25d38f`) verified in git history; the `contains` anchors (ApplyFrequencyReducer, legacyProbabilityReducerWithDouble, addChildren/AddChildren, FindCollisionPiece, TestFrequencyReductionMethod, TestMineshaft, Mineshaft in noisegen) confirmed present. Docker `-race` over `./world/structure/` clean; the `./world/` `-race` gate result recorded below.
