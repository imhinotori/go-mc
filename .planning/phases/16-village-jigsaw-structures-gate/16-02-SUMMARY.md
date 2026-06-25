---
phase: 16-village-jigsaw-structures-gate
plan: 02
subsystem: worldgen
tags: [jigsaw, structure, village, template_pool, voxelshape, bounded-bfs, random_spread, protocol-776]

# Dependency graph
requires:
  - phase: 16-01
    provides: ParseTemplate / StructureTemplate.PlaceInWorld+BoundingBoxAt+Jigsaws, TemplateProcessor / LoadProcessorList, data.TemplatePoolJSON+ProcessorListJSON+StructureTemplateNBT
  - phase: 14-01
    provides: RandomSpreadStructurePlacement (random_spread math), StartGenerator / Cache / StructureStart, LoadStructureSet / HasStructureBiomes, BoundingBox
  - phase: 14-02
    provides: StructurePiece / Piece interface / placeInChunk cross-chunk clip, transformState
provides:
  - "template_pool / pool-element model: StructureTemplatePool parse + the 5 PoolElement types (Single/Legacy/List/Feature/Empty) + weight-expanded Util.shuffle candidate selection"
  - "the bounded-BFS JigsawPlacement.Placer: SequencedPriorityIterator work queue, tryPlacingChildren (jigsaw alignment + canAttach), the THREE bounds (maxDepth + max_distance_from_center=80 + VoxelShape collision) + a defensive 1000-piece cap"
  - "PoolElementStructurePiece (a placed pool element clipped to the writable box)"
  - "villageStartGen: random_spread (salt 10387312 / spacing 34 / separation 8) + weighted-without-replacement variant pick over the 5 biome variants + the REAL biome gate + the Placer-built start, registered into the CompositeStartGenerator"
affects: [16-03, structure-acceptance, structure-visual-gate]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bounded-BFS jigsaw assembly via a priority-queue work iterator (NOT recursion) so RNG draws stay in vanilla lockstep"
    - "Weight-expanded pool + Util.shuffle Fisher-Yates selection (the weight IS the candidate count)"
    - "VoxelShape collision modeled as a strict-interior box-overlap over accumulated placed boxes (rigid projection)"
    - "Weighted-without-replacement structure-set variant pick (nextInt over the running total, remove+redraw on a biome miss)"

key-files:
  created:
    - world/structure/jigsaw_pool.go
    - world/structure/jigsaw_pool_test.go
    - world/structure/jigsaw_placement.go
    - world/structure/jigsaw_placement_test.go
    - world/structure/village.go
    - world/structure/village_test.go
  modified:
    - world/structure/template.go
    - world/noisegen.go

key-decisions:
  - "Single + Legacy pool elements share one Go type (singlePoolElement) — the LegacySingle 'jigsaw block -> air' semantics already live in the 16-01 StructureTemplate.PlaceInWorld, so the distinction is purely place-behavior the template layer handles uniformly"
  - "The VoxelShape is a (bound, []placed) box model with a strict-interior overlap test (the jar deflates the candidate AABB by 0.25 before colliding, so face-abutting pieces do not collide) — exact for the rigid village projection"
  - "Extended the 16-01 Jigsaws() with LocalPos + TopFacing + placement/selection priority (Rule 3 blocking dependency: canAttach needs the top face; the SequencedPriorityIterator needs the placement priority)"
  - "The defensive total-piece cap is 1000 (the stronghold precedent) — backstops a malformed self-referencing pool beyond the three real bounds"

patterns-established:
  - "PoolElement interface (BoundingBox/Jigsaws/Place/Projection/IsEmpty) abstracts the 5 jar pool-element types behind the Placer"
  - "Probe-then-pin deterministic test seed (seed 0, anchor chunk (15,2) = its own region's PotentialStructureChunk) with a flat surface + constant biome stub — the 14-02/15-03 oracle pattern"

requirements-completed: [STRUCT-05]

# Metrics
duration: ~95min
completed: 2026-06-25
---

# Phase 16 Plan 02: Village Jigsaw Placement Summary

**The bounded-BFS JigsawPlacement.Placer (maxDepth + max_distance_from_center=80 + VoxelShape collision + a 1000-piece cap) over a SequencedPriorityIterator work queue, the template_pool/5-pool-element model with weight-expanded Util.shuffle selection, and a random_spread village StartGenerator gated on the 5 biome variants — assembling real 80-150-piece vanilla villages deterministically per (seed,pos).**

## Performance

- **Duration:** ~95 min
- **Tasks:** 2 (both TDD)
- **Files created:** 6
- **Files modified:** 2

## Accomplishments
- **The pool-element model** (jigsaw_pool.go, 409 lines): parses a template_pool JSON into a weight-EXPANDED element list + the 5 CFR pool-element types (Single/Legacy via the 16-01 StructureTemplate, List composes, Feature stubs, Empty terminates) behind a `PoolElement` interface; weighted-candidate selection is the jar's expand-by-weight then `Util.shuffle` Fisher-Yates (the EXACT draw count, `size-1`). The `minecraft:empty` terminator resolves through `data.TemplatePoolJSON("empty")` to a size-0 pool without panicking (Pitfall #6).
- **The Placer** (jigsaw_placement.go, 522 lines): `addPieces` picks a root rotation + start element, builds the 80-radius free VoxelShape, and drains a `SequencedPriorityIterator` work queue (BFS, NOT recursion). `tryPlacingChildren` resolves each jigsaw block's target+fallback pool, shuffles candidates by weight, tries each rotation, aligns the child jigsaw to the parent (`canAttach` + `calculateConnectedPosition`), and rejects on height-band exit OR placed-shape collision. The THREE bounds are independently load-bearing.
- **The village StartGenerator** (village.go, 223 lines): `random_spread` placement (salt 10387312 / spacing 34 / separation 8, VERIFIED by decoding villages.json), a weighted-WITHOUT-REPLACEMENT variant pick over the 5 biomes seeded `SetLargeFeatureSeed(seed,cx,cz)`, the REAL biome gate at the chunk-center surface (NO accept-by-default), surface projection, then the Placer. Registered into the `CompositeStartGenerator` alongside the temples + mineshaft + stronghold.

## Task Commits

1. **Task 1: template_pool model + bounded-BFS Placer** - `2cc787d0` (feat)
2. **Task 2: village StartGenerator + registration** - `03959bc2` (feat)

_(Both tasks were TDD; each landed as one feat commit carrying impl + tests + the GREEN verification.)_

## Files Created/Modified
- `world/structure/jigsaw_pool.go` - StructureTemplatePool parse + the 5 PoolElement types + weighted selection
- `world/structure/jigsaw_placement.go` - the Placer (SequencedPriorityIterator BFS, tryPlacingChildren, the 3 bounds) + PoolElementStructurePiece + canAttach
- `world/structure/village.go` - villageStartGen (random_spread + biome gate + Placer-built start)
- `world/structure/template.go` - extended Jigsaws()/JigsawBlockInfo/jigsawNBT with LocalPos + TopFacing + placement/selection priority
- `world/noisegen.go` - registered villageGen into the CompositeStartGenerator
- `world/structure/{jigsaw_pool,jigsaw_placement,village}_test.go` - the pool parse + termination-under-each-bound + village placement/determinism/idempotence tests

## Verified placement constants (decoded from villages.json)
- **salt 10387312**, **spacing 34**, **separation 8**, **LINEAR spread** — `NewVillageStartGen` FAILS LOUD if villages.json disagrees, and `TestVillagePlacementConstants` re-pins them.
- **5 biome variants** (plains/desert/savanna/snowy/taiga), each weight 1, each gated on its own `has_structure/village_<biome>` tag (plains -> {plains, meadow}).

## Test seed + anchor
- **Deterministic test seed: 0**, anchor chunk **(15, 2)** — confirmed `IsStructureChunk` / `PotentialStructureChunk(seed=0, 15, 2) == (15,2)` (its own region's potential start, algorithm-derived NOT eyeballed).
- The seed-0 (15,2) village is **plains, 146 pieces, spanning 9x7 chunks** (bbox X[156..283] Z[-6..82]) on a flat y=72 surface + constant plains biome stub.
- The three bounds proven independently (synthetic self-referencing pool): **maxDepth=2 -> 13 pieces**, **max_distance=24 -> 121 pieces**, **collision-only (wide depth+distance) -> 841 pieces** — each below the 1000 cap, each a distinct cap level.

## Verification gates
1. `go build ./...` — **PASS** (exit 0)
2. `CGO_ENABLED=0 go build ./...` — **PASS** (exit 0)
3. `go vet ./world/...` — **PASS** (clean)
4. `go test ./world/structure/ -run 'TestJigsawPool|TestPlacer|TestJigsaw|TestVillage'` — **PASS**
5. `go test ./world/... -count=1` — **PASS** (incl. TestDecorationReorderIdentical + TestEmitOnce byte-identical WITH villages live)
6. `cd tools && go build ./...` — **PASS** (exit 0)
7. Docker `-race` (`golang:1.26`, `-timeout 1800s ./world/...`) — **PASS** (all 13 ./world/... packages ok; the `world` package took 792s under -race, within the 1800s budget — a timing artifact of real recursive structure work, NOT a race)

`go.mod` / `go.sum` UNCHANGED; no new deps; no encoder/packet/chunk-wire/golden touched; no import cycle.

## Decisions Made
See `key-decisions` frontmatter. The load-bearing port choices: the VoxelShape strict-interior overlap (the 0.25 deflate equivalent), Single+Legacy sharing one Go type, and the weighted-without-replacement variant pick (the jar's remove+redraw loop).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Extended the 16-01 Jigsaws() with TopFacing + LocalPos + priorities**
- **Found during:** Task 1 (the Placer's canAttach + SequencedPriorityIterator need data the 16-01 JigsawBlockInfo did not carry)
- **Issue:** `JigsawBlock.canAttach` tests the jigsaw's TOP face (unless rollable); the SequencedPriorityIterator keys on `placement_priority`; the alignment needs the template-LOCAL jigsaw pos. The 16-01 `Jigsaws()` returned only the front face + world pos.
- **Fix:** Added `LocalPos`, `TopFacing`, `PlacementPriority`, `SelectionPriority` to `JigsawBlockInfo` + the `jigsawNBT` schema; added a `jigsawOrientation()` helper returning both front+top via `block.FrontAndTop.Directions()`; populated all in `Jigsaws()`.
- **Files modified:** world/structure/template.go (42 added lines)
- **Verification:** `go build` + the real-village probe confirmed front/top/joint/priorities decode correctly; all 16-01 template tests stay green.
- **Committed in:** `2cc787d0` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking dependency extension).
**Impact on plan:** The extension was a required dependency completion (the Placer cannot align without the top face). No scope creep — the new fields are consumed only by 16-02.

## Issues Encountered
- The plan's `<interfaces>` block listed 16-01 signatures WITHOUT the `pivotX, pivotZ` params the actual `template.go` carries — read the real file (the mandatory research-first), so the Placer passes pivot ZERO everywhere (the jar's default StructurePlaceSettings pivot), matching `getBoundingBox(ZERO, rot)`.

## Known Stubs
- `featurePoolElement.Place` is a no-op + empty box (FeaturePoolElement places a placed_feature — entity spawners in village/common/*). Villages reference it only in the common/animals + iron_golem spawner pools, which carry NO structural blocks; entity spawning is a v3 subsystem. Documented as intentional; the visible village geometry is fully delivered.
- `listPoolElement` composes its sub-elements (not stubbed) but is never exercised by villages (no village pool uses list_pool_element); parsed for completeness.

## Next Phase Readiness
- STRUCT-05 is functionally complete: the data-driven jigsaw Placer + villages generate in vanilla positions per biome variant, deterministic per (seed,pos), cross-chunk idempotent. 16-03 closes STRUCT-05 with the acceptance + the visual gate.

## Self-Check: PASSED
- All 6 created files + the SUMMARY exist on disk.
- Both task commits (`2cc787d0`, `03959bc2`) exist in the branch history.

---
*Phase: 16-village-jigsaw-structures-gate*
*Completed: 2026-06-25*
