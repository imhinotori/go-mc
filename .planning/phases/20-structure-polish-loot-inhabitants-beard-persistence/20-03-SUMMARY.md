---
phase: 20-structure-polish-loot-inhabitants-beard-persistence
plan: 03
subsystem: worldgen-structures
tags: [nbt, persistence, structure-start, region, piece-serialization, proto-776]

# Dependency graph
requires:
  - phase: 14-16 (structures)
    provides: the StructureStart/StructurePiece model + the cache (ComputeStarts/StartsForChunk) + every concrete piece type (desert/jungle/swamp temples, igloo templates, mineshaft, stronghold)
  - phase: 20-01
    provides: the level/loot package (disjoint files; landed alongside this plan)
provides:
  - "StructurePiece Save()/Load() NBT (the {id, BB, O, GD} base + per-piece addAdditionalSaveData) for every Sulfur piece type, type-keyed LoadPiece registry"
  - "StructureStart.CreateTag / LoadStaticStart (the flat {id, ChunkX, ChunkZ, references, Children} compound) with INVALID handling + a Children DoS bound"
  - "save.StructuresData: the chunk structures compound {starts, References} as an opaque save-layer seam (no world/structure import cycle)"
  - "Cache.StoreStarts seeder + WriteChunkStructures / ReadChunkStructures (the two-way cache<->region structures.Starts seam, recompute-fallback on absent/garbled)"
  - "forward-compatible one-shot spawn-guard slots (SpawnedWitch/SpawnedCat/HasPlacedSpawner) in piece NBT for 20-04"
affects: [20-04 (structure inhabitants — reads the spawn-guard slots), 20-05 (generator ordering)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Opaque-RawMessage save seam: save/structure.go keeps per-start compounds opaque so the lower save layer never imports the higher world/structure layer (no cycle)"
    - "Coherence-by-construction Load: igloo Load re-runs newIglooPiece (the same ctor the generator calls) so the loaded piece is byte-identical to a freshly generated one"
    - "PostProcess-output equality as the piece/start coherence gate (two pieces that draw identical blocks ARE identical for gameplay)"

key-files:
  created:
    - world/structure/piece_nbt.go
    - world/structure/piece_nbt_test.go
    - world/structure/start_nbt.go
    - world/structure/start_nbt_test.go
    - world/structure/persistence.go
    - world/structure/persistence_test.go
    - save/structure.go
  modified: []

key-decisions:
  - "structures NBT keys are jar-exact: `starts` (lowercase) keyed by structure id -> StructureStart.createTag; `References` (capital R) keyed by structure id -> LongArray (SerializableChunkData.packStructureData)"
  - "Persistence is a PURE optimization: ReadChunkStructures returns seeded=false (never errors fatally) on an absent/garbled/unknown-piece tag so the worker recomputes — recompute is the source of truth (A6/T-20-07)"
  - "save/structure.go keeps per-start compounds OPAQUE (nbt.RawMessage) to avoid a save -> world/structure import cycle; world/structure encodes/decodes the startTag"
  - "Children decode is bounded at 4096 pieces (T-20-08 DoS) — no faithful start reaches it; the village jigsaw is capped at 1000 by the placer"
  - "Igloo/jigsaw template pieces persist a template identity (selector + offset + origin + rotation) and re-resolve via the ctor rather than field-copying — coherence by construction"

patterns-established:
  - "Per-piece Save/Load dispatch: SavePiece type-switches on the concrete Go type; LoadPiece dispatches on the createTag `id` string (unknown id errors loudly, Phase-11 discipline)"
  - "from2DDataValue inverse of get2DDataValue for the O orientation field (StructurePiece ctor port)"

requirements-completed: [STRUCT-POLISH-04]

# Metrics
duration: 35min
completed: 2026-06-27
---

# Phase 20 Plan 03: StructureStart NBT persistence + per-piece Save/Load Summary

**StructureStarts now round-trip region NBT (createTag <-> loadStaticStart) with full per-piece Children, so a reloaded structure chunk reads its starts from disk instead of recomputing — with recompute as the always-valid coherence authority (a missing/garbled tag falls back, never panics).**

## Performance

- **Duration:** ~35 min
- **Tasks:** 3 (all TDD)
- **Files created:** 7 (4 source + 3 test)

## Accomplishments
- Ported `StructurePiece.createTag` base layout `{id, BB int[6], O, GD}` + the `(StructurePieceType, CompoundTag)` ctor (`from2DDataValue` -> `setOrientation`), jar-verified vs `26.2-inner.jar`, with per-piece `addAdditionalSaveData` for every Sulfur piece type (scattered temples, igloo templates, mineshaft corridor/crossing/room/stairs, all 11 stronghold piece kinds).
- Ported `StructureStart.createTag` / `loadStaticStart` (the flat `{id, ChunkX, ChunkZ, references, Children}` compound) with the `INVALID` early-return + an over-bound Children guard.
- Wired the two-way cache<->region `structures` seam: `WriteChunkStructures` serializes the cache's own starts into the `{starts, References}` compound; `ReadChunkStructures` decodes + seeds the cache (`Cache.StoreStarts`) or falls back to recompute on absence/garble.
- Proved coherence: `LoadStaticStart(CreateTag(s))` EQUALS `ComputeStarts(seed,pos)` (the Pitfall-4/T-20-09 gate), and the full write->NBT-bytes->read round-trip seeds a recompute-equal cache.
- Docker `-race` over `./world/structure/ ./save/` green.

## Task Commits

1. **Task 1: Per-piece Save()/Load() NBT** - `f8c17b9b` (feat)
2. **Task 2: StructureStart.CreateTag / LoadStaticStart** - `85f35998` (feat)
3. **Task 3: cache <-> region structures.Starts seam** - `531a0c6f` (feat)

_TDD note: tests and implementation were committed together per task (the test API and the implementation co-define the surface); each commit is independently green._

## Files Created/Modified
- `world/structure/piece_nbt.go` - `SavePiece`/`LoadPiece` + `pieceTag`/`pieceExtraData` (base `{id,BB,O,GD}` + per-piece additional save data + spawn-guard slots).
- `world/structure/piece_nbt_test.go` - `TestPieceNBTRoundTrip` (every piece type, bbox + PostProcess-output equality), base-field + unknown-id tests.
- `world/structure/start_nbt.go` - `StructureStart.CreateTag` / `LoadStaticStart` + the Children DoS bound.
- `world/structure/start_nbt_test.go` - round-trip (in-memory + through NBT bytes), INVALID, recompute-coherence, garbled-bound/garbled-piece.
- `world/structure/persistence.go` - `Cache.StoreStarts`, `StartsForOwner`, `WriteChunkStructures`, `ReadChunkStructures`, the startTag<->RawMessage encode/decode.
- `world/structure/persistence_test.go` - write->bytes->read round-trip, missing/garbled/garbled-piece/empty-chunk fallbacks.
- `save/structure.go` - `save.StructuresData` (`{starts, References}`) + `EncodeStructures`/`DecodeStructures` (opaque per-start RawMessage, no import cycle).

## Decisions Made
- See `key-decisions` frontmatter. The load-bearing ones: jar-exact NBT keys (`starts` lowercase / `References` capital), persistence-as-pure-optimization (recompute fallback), opaque save-layer seam, and coherence-by-construction for template pieces.

## Handoff for 20-04 (structure inhabitants)

- **structures.Starts NBT key names:** the chunk `structures` compound is `{ starts: { <structure-id>: <StructureStart.createTag> }, References: { <structure-id>: LongArray } }`. `starts` is lowercase, `References` is capital-R (jar `SerializableChunkData.packStructureData`). Each start's compound is `{ id, ChunkX, ChunkZ, references, Children: [<pieceTag>...] }`. Each pieceTag is `{ id, BB int[6], O, GD, Extra: <pieceExtraData> }`.
- **Cache.StoreStarts seeder signature:** `func (c *Cache) StoreStarts(pos level.ChunkPos, starts []*StructureStart)` — seeds the cache from loaded NBT without recompute (the counterpart to `ComputeStarts`). `func (c *Cache) StartsForOwner(pos level.ChunkPos) []*StructureStart` reads the chunk's OWN starts (for the write side).
- **Spawn-guard slots left for 20-04 (Pitfall 6):** `pieceExtraData` (in `world/structure/piece_nbt.go`) carries three forward-compatible bool slots — `SpawnedWitch` (`nbt:"SpawnedWitch"`), `SpawnedCat` (`nbt:"SpawnedCat"`), `HasPlacedSpawner` (`nbt:"HasPlacedSpawner"`). They round-trip as `false` today. 20-04 adds the matching fields to `SwampHutPiece` (witch/cat) and the stronghold spawner piece, then wires them in `SavePiece`/`LoadPiece` (the `case *SwampHutPiece:` and stronghold cases already have a TODO-shaped slot in `SavePiece`) so a reloaded structure reads the guard as true and skips re-spawning. No re-exploration of the piece NBT format is needed — just set/read the three slots.
- **Worker wiring (additive, not done here per file scope):** `ReadChunkStructures(cache, pos, sc.Structures)` belongs in the worker's region-hit decode path (after `save.Chunk.Load`); `sc.Structures, _ = WriteChunkStructures(cache, pos)` belongs in the chunk-save path. Both are infallible-from-the-worker (recompute fallback) and do not change the off-tick discipline. The plan scoped this plan to the seam functions; the call sites are a one-line additive change owned by the integration step.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing test coverage] Added piece_nbt_test.go and persistence_test.go**
- **Found during:** Tasks 1 and 3 (both `tdd="true"`)
- **Issue:** The plan frontmatter `files_modified` listed only `start_nbt_test.go`, but Tasks 1 and 3 are TDD tasks requiring their own round-trip + fallback test coverage.
- **Fix:** Created `world/structure/piece_nbt_test.go` (per-piece round-trip + unknown-id) and `world/structure/persistence_test.go` (write/read round-trip + missing/garbled fallbacks).
- **Verification:** Both green under CGO=0 and Docker -race.
- **Committed in:** `f8c17b9b` (Task 1), `531a0c6f` (Task 3)

---

**Total deviations:** 1 auto-fixed (missing test coverage for the TDD tasks)
**Impact on plan:** Test-only additions required by the tasks' `tdd="true"` flag. No scope creep; the out-of-scope files (piece.go/place.go/cache.go-core/noisegen.go/block_drop.go/worker.go/level/chunk.go) were NOT touched.

## Issues Encountered
None. The piece set is broad (30+ concrete piece types across mineshaft/stronghold/temples/igloo), but each maps cleanly onto the base `{BB,O,GD}` + a small scalar `addAdditionalSaveData`; the PostProcess-output equality check caught any field omissions during TDD.

## Next Phase Readiness
- 20-04 (inhabitants) can fill the spawn-guard slots without re-exploring the NBT format.
- The worker call-site wiring (`ReadChunkStructures`/`WriteChunkStructures`) is a documented one-line additive change for the integration step.
- No new deps; CGO_ENABLED=0 clean; no `import "C"`.

## Self-Check: PASSED

---
*Phase: 20-structure-polish-loot-inhabitants-beard-persistence*
*Completed: 2026-06-27*
