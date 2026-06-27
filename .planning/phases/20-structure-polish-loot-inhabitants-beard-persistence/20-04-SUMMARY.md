---
phase: 20-structure-polish-loot-inhabitants-beard-persistence
plan: 04
subsystem: worldgen-structures
tags: [structure-inhabitants, spawn-seam, off-tick-tick, entity-spawn, mob-spawner, village-entities, proto-776, tick-05]

# Dependency graph
requires:
  - phase: 20-03
    provides: "the persisted spawn-guard NBT slots (SpawnedWitch/SpawnedCat/HasPlacedSpawner) in pieceExtraData"
  - phase: 20-02
    provides: "the WorldGenView interface (SetBlockEntity for chests) that this plan extends with RecordSpawn/SetSpawner"
  - phase: 20-05
    provides: "world/noisegen.go (shared file; this plan's spawn buffer rides the existing placeStructures path, no noisegen edit needed)"
  - phase: 16-02
    provides: "the village jigsaw placer + StructureTemplate.entities (the ALREADY-stored template entity list)"
  - phase: 06 (GAMEPLAY-01)
    provides: "the entity store + tracker (AddEntity broadcast) the structure spawn rides for free"
provides:
  - "structure.SpawnRequest{EntityType, X,Y,Z, PersistenceRequired} — the off-tick spawn carrier"
  - "world.ChunkResult.Spawns []SpawnRequest — the immutable off-tick->tick handoff for structure inhabitants"
  - "WorldGenView.RecordSpawn (live mob request) + WorldGenView.SetSpawner (silverfish mob_spawner BE), alongside 20-02's SetBlockEntity"
  - "server.drainStructureSpawns — the tick-side drain (chunkReady.applyTo) that adds the witch/cat/villager to the entity store"
  - "swamp-hut witch+cat live spawns; stronghold silverfish SPAWNER block; village villager+cat spawns from the template entityInfoList"
affects: [future-mob-finalizeSpawn (the cited stub becomes a real attribute/variant read), future-spawner-tick (the mob_spawner BE)]

# Tech tracking
tech-stack:
  added: []  # NO new deps — pure stdlib + in-repo nbt/data/entity
  patterns:
    - "off-tick->tick spawn seam: PostProcess RECORDS a SpawnRequest (a value) -> Neighborhood buffers it -> tryDecorate captures view.Spawns() onto the staged chunk -> tryEmit forwards onto ChunkResult.Spawns -> the tick drains onto the entity store (TICK-05 / Pitfall 5: the worker never touches the store)"
    - "the structure spawn rides GAMEPLAY-01's tracker for free — the SAME store-add path spawnBlockDrop uses (NewEntity + entities.add -> AddEntity broadcast next tick), no new tracker code"
    - "silverfish is a BLOCK not a live entity: a mob_spawner block-entity ({SpawnData:{entity:{id}}}), placed via SetSpawner — never a SpawnRequest, never the entity-store seam"
    - "reload-no-double-spawn: the per-piece one-shot guard adapted to Sulfur's per-chunk re-run — the swamp-hut witch/cat guard is set in-gen (spawns are buffer-recorded, no block-fingerprint test); the stronghold spawner guard is a RELOAD-ONLY skip (NOT set in-gen) so per-chunk block re-runs stay byte-deterministic (the box.isInside clip alone makes placement idempotent, like createChest)"
    - "cited-stub finalizeSpawn: the mob spawns at its vanilla-DEFAULT state (no variant/profession/attribute read yet), structured to become a real Mob.finalizeSpawn call when the attribute subsystem lands — never baked away (CLAUDE.md)"

key-files:
  created:
    - world/structure/spawn.go (SpawnRequest — the off-tick carrier)
    - world/structure/stronghold_spawner.go (PortalRoom silverfish SPAWNER block + BE, idempotent + reload guard)
    - world/structure/village_entities.go (StructureTemplate.PlaceEntities + transformVec3 — the float pivot transform)
    - server/structure_spawn.go (drainStructureSpawns + the entity-id resolver)
    - world/structure/swamp_spawn_test.go
    - world/structure/stronghold_spawner_test.go
    - world/structure/village_entities_test.go
    - server/structure_spawn_test.go
  modified:
    - world/structure/piece.go (WorldGenView gains RecordSpawn + SetSpawner)
    - world/neighborhood.go (RecordSpawn buffer + SetSpawner BE + Spawns() accessor + spawnerNBT + the spawnRequestCap)
    - world/worker.go (ChunkResult.Spawns; stagedChunk.spawns; tryDecorate captures, tryEmit forwards)
    - server/tick.go (chunkReady.applyTo drains Spawns on the owner)
    - world/structure/swamp_hut.go (spawnWitch/spawnCat record requests, one-shot guarded)
    - world/structure/stronghold_pieces.go (PortalRoom hasPlacedSpawner field + spawnerStateID + the placeSilverfishSpawner call)
    - world/structure/piece_nbt.go (SwampHutPiece + StrongholdPortalRoom save/load the spawn guards)
    - world/structure/jigsaw_pool.go (singlePoolElement.Place places blocks THEN entities)
    - world/structure/piece_test.go (mapView gains SetSpawner + RecordSpawn capture)

key-decisions:
  - "SpawnRequest carries the entity-type id STRING (not a numeric id) so the structure package need not import data/entity (which sits above it); the tick-side drain resolves the string to a data/entity record — mirrors how the chest BE carries the loot-table id string"
  - "the spawn buffer is captured from view.Spawns() in tryDecorate (the ONLY place a center's PLACE pass runs over its own writable box) and forwarded in tryEmit, so it rides the EXACT same immutable handoff as the chunk — no noisegen.go edit needed (the Neighborhood IS the view PlaceStructures writes through)"
  - "stronghold spawner guard is a RELOAD-ONLY skip (NOT set during PostProcess): Sulfur re-runs PostProcess per-chunk on the same piece instance, so a mutable in-gen guard would wrongly block the owning chunk's pass on a re-run (and break the fingerprint/cross-chunk determinism tests). box.isInside ALONE makes placement idempotent + chunk-correct within a gen; the persisted guard at its actual value (false on fresh gen) keeps the 20-03 round-trip coherence; reload protection is the region-hit bypass (a saved chunk never re-runs PostProcess) + the guard read at Load"
  - "swamp-hut witch/cat guard IS set in-gen (the jar's spawnedWitch/spawnedCat) because spawns are buffer-recorded (not block-written), so no block-fingerprint/cross-chunk determinism test re-runs them; the guard correctly prevents a double-record on re-decorate; round-trips via the 20-03 SpawnedWitch/SpawnedCat slots"
  - "the village entity list came from template.go's ALREADY-STORED StructureTemplate.entities (A4 resolved -> NOT a net-new offline extraction); PlaceEntities only transforms the stored list + emits SpawnRequests"
  - "spawnRequestCap=256 bounds the per-chunk spawn-request count (T-20-12 DoS); a faithful structure emits a tiny fixed set, excess is dropped (never panic)"

requirements-completed: [STRUCT-POLISH-02]

# Metrics
duration: 75min
completed: 2026-06-27
---

# Phase 20 Plan 04: Structure Inhabitant Spawns via ChunkResult.Spawns Summary

**Structures now have their inhabitants: a swamp hut spawns a live witch + cat, a stronghold places a silverfish SPAWNER block, and a village spawns villagers + a cat from its template entity list — all crossing the off-tick worldgen -> tick entity store via a new immutable `ChunkResult.Spawns` slice (the worker only RECORDS a SpawnRequest; the tick performs the only store add, where GAMEPLAY-01's tracker broadcasts AddEntity for free), with reload-no-double-spawn guards that respect Sulfur's per-chunk re-run model.**

## Performance
- **Duration:** ~75 min
- **Started:** 2026-06-27T05:00:08Z
- **Completed:** 2026-06-27T06:15:46Z
- **Tasks:** 3/3 (all TDD)
- **Files:** 8 created + 9 modified

## Task Commits
1. **Task 1: ChunkResult.Spawns seam + WorldGenView.RecordSpawn/SetSpawner + tick drain** — `19129b28` (feat)
2. **Task 2: swamp-hut witch+cat + stronghold silverfish SPAWNER + reload guards** — `df210f3d` (feat)
3. **Task 3: village template entity spawns (villagers + cat)** — `69fa2ac0` (feat)

_TDD: each task wrote failing tests (RED) then the implementation (GREEN); the test API + implementation co-define the seam, so each commit bundles them and is independently green._

## Accomplishments

### The off-tick -> tick spawn seam (Task 1, the keystone)
- `structure.SpawnRequest{EntityType, X,Y,Z, PersistenceRequired}` is the immutable off-tick carrier (a VALUE, no live entity pointer — Pitfall 5).
- `WorldGenView` gained `RecordSpawn` (a live-mob request) + `SetSpawner` (a mob_spawner block-entity) alongside 20-02's `SetBlockEntity`. PostProcess records spawns through the view, never touching any store.
- `*world.Neighborhood` buffers `RecordSpawn` calls (capped at 256, T-20-12) and writes the silverfish spawner BE on `SetSpawner`.
- `world.ChunkResult` gained `Spawns []SpawnRequest`. `tryDecorate` captures `view.Spawns()` onto the staged chunk; `tryEmit` forwards them onto the emitted ChunkResult — riding the EXACT same immutable handoff as `Chunk`.
- `server.drainStructureSpawns` (wired into `chunkReady.applyTo`, server/tick.go) drains them on the tick owner: resolve the entity id -> `NewEntity(idAlloc.AllocID(), ...)` -> `entities.add(e)`. The tracker broadcasts AddEntity next tick (no new tracker code — the same path `spawnBlockDrop` rides).
- `TestStructureSpawnRaceClean` exercises the two-goroutine boundary (off-tick producers building ChunkResults, the tick draining) — Docker `-race` green.

### Swamp hut witch + cat, stronghold silverfish SPAWNER (Task 2)
- `SwampHutPiece.spawnWitch`/`spawnCat` (jar `SwampHutPiece.postProcess` tail) record a witch + a cat SpawnRequest at `getWorldPos(2,2,5)` block-center (+0.5 X/Z, the `snapTo(x+0.5,y,z+0.5)`), one-shot guarded by `spawnedWitch`/`spawnedCat` (persisted in the 20-03 NBT slots).
- `StrongholdPortalRoom.placeSilverfishSpawner` (jar `StrongholdPieces$PortalRoom.postProcess`) places a SPAWNER block at `getWorldPos(5,3,6)` + a mob_spawner BE set to silverfish (a BLOCK, NOT a live entity). `hasPlacedSpawner` is a RELOAD-ONLY skip (see Decisions): NOT set in-gen, so per-chunk re-runs stay byte-deterministic (the `TestStrongholdFingerprint` + `TestStrongholdCrossChunk` gates stay green).
- Both guards round-trip in the piece NBT (20-03 coherence gate held: `LoadPiece(SavePiece(p))` places identically).

### Village villager + cat from the stored entity list (Task 3)
- `StructureTemplate.PlaceEntities` ports `StructureTemplate.placeEntities`: for each stored `rawEntity`, transform its position (`transformPos` for the integer clip pos, the new `transformVec3` for the float spawn pos) + origin, clip to the box, read the entity NBT `id`, and `RecordSpawn`.
- `transformVec3` is the float pivot transform, verified against the jar bytecode + the `$SwitchMap$Rotation` remapping (CCW90/CW90/CW180 each carry the +1 half-cell term; the float mirror uses `1.0 - coord`).
- `singlePoolElement.Place` now places blocks THEN entities, so the `cat_black`/`nitwit`/`villager` village templates spawn their inhabitants. The entities come from template.go's ALREADY-STORED `StructureTemplate.entities` (A4 resolved — no net-new extraction); an empty list is a no-op.

## SpawnRequest struct + WorldGenView signatures (the handoff)
```go
// world/structure/spawn.go
type SpawnRequest struct {
    EntityType          string  // "minecraft:witch" | "minecraft:cat" | "minecraft:villager"
    X, Y, Z             float64 // world spawn pos (block-center +0.5, the snapTo args)
    PersistenceRequired bool    // setPersistenceRequired() — a structure mob never despawns
}

// world/structure/piece.go (added alongside 20-02's SetBlockEntity)
RecordSpawn(req SpawnRequest)            // a live mob request (witch/cat/villager)
SetSpawner(wx, wy, wz int, entityID string) // a mob_spawner BE (silverfish) — a BLOCK
```

## finalizeSpawn cited stub
The mob spawns at its vanilla-DEFAULT state (`server/structure_spawn.go`): `finalizeSpawn(ServerLevelAccessor, DifficultyInstance, STRUCTURE, null)` initializes mob attributes/variants (cat variant, witch held item, villager profession) + sets NoAI/persistence. Those subsystems are not built yet, so the spawn is the value `finalizeSpawn` would leave for a default difficulty with no SpawnGroupData — equal to the vanilla default, NOT baked away. `PersistenceRequired` is carried for the same reason. When the attribute subsystem lands, `finalizeSpawn` becomes a real per-type call at the drain site. Cited per CLAUDE.md.

## Capture-diff Re-seal
- **Chunk-wire goldens: UNCHANGED, no re-seal needed.** The silverfish spawner adds a mob_spawner BlockEntity to the chunk's `BlockEntity` list, but the wire ENCODER (`level/chunk.go`) was NOT touched — the spawner BE rides the existing `level.BlockEntity` struct + `PackXZ` + the existing list encoder, exactly like 20-02's chest BE. `level/` (the chunk-wire capture-diff goldens) passes fresh; the full `world/` worldgen integration suite (278s, generates chunks with structures incl. the spawner BE) is green. The live mob SPAWNS are server-side entities (the entity store + tracker), NOT chunk-wire data, so they touch no chunk golden.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Stronghold spawner guard made a RELOAD-ONLY skip (not an in-gen flag)**
- **Found during:** Task 2 (`TestStrongholdFingerprint` + `TestStrongholdCrossChunk` failed: "fingerprint non-deterministic" / "cross-chunk union diverges")
- **Issue:** The first port set the jar's mutable `hasPlacedSpawner` true at placement. But Sulfur re-runs `PostProcess` per overlapping chunk on the SAME piece instance (place.go placeInChunk), and the fingerprint/cross-chunk tests re-run PostProcess on shared instances — so the in-gen flag set on one pass wrongly blocked the spawner BLOCK write on a later pass, breaking byte determinism.
- **Fix:** The `box.isInside(5,3,6)` clip ALONE makes the spawner placement idempotent + chunk-correct within a gen (only the owning chunk writes it, identically each pass — the createChest discipline). The guard is now a RELOAD-ONLY skip: NOT set during PostProcess, read from NBT at Load (a reloaded piece whose guard is true skips). Reload protection is primarily the region-hit bypass (a saved chunk never re-runs PostProcess) + the guard read.
- **Files modified:** world/structure/stronghold_spawner.go, world/structure/piece_nbt.go (SavePiece persists the actual field value, keeping the 20-03 round-trip coherence)
- **Commit:** df210f3d

**2. [Rule 3 - Blocking] Renamed the test helper newSpawnLoop -> newStructureSpawnLoop**
- **Found during:** Task 1 (build failed: `newSpawnLoop redeclared` — server/spawner_test.go already has one)
- **Issue:** The new server test's loop helper collided with the existing mob-spawner test helper.
- **Fix:** Renamed to `newStructureSpawnLoop`.
- **Files modified:** server/structure_spawn_test.go
- **Commit:** 19129b28

## Verification Results
- `CGO_ENABLED=0 go build ./...` — exit 0
- `go vet ./world/ ./world/structure/ ./server/` — clean
- `go test ./world/structure/ -run 'TestSwampHutSpawns|TestStrongholdSpawner|TestVillageSpawns'` — green
- `go test ./server/ -run TestStructureSpawnSeam` — green (incl. TestStructureSpawnUnknownTypeSkipped + TestStructureSpawnRaceClean)
- `go test ./world/structure/ ./server/ ./level/` — green (the full structure suite incl. fingerprints/cross-chunk/acceptance, the server suite, the chunk-wire capture-diff goldens)
- `go test ./world/` — green (278s, the worldgen integration suite generating chunks with structures + the spawner BE)
- Docker `-race` (golang:1.26): `./server/` green (15.8s, incl. the two-goroutine TestStructureSpawnRaceClean), `./world/structure/` green (40.9s), `./world/` worker/emit/neighborhood seam tests green (the off-tick recording side)
- `git diff go.mod go.sum` — empty (no new deps)
- `grep import "C"` — none

### A note on the full ./world/ -race
The full `./world/` suite under `-race` TIMES OUT (the worldgen integration tests — `TestDesertPyramidCrossChunkIdempotent` et al. — generate full chunks and run minutes-each under race instrumentation; the non-race suite is already 278s, so race + the 1800s cap is exceeded). This is a PRE-EXISTING property of that suite, NOT a data race (zero `DATA RACE` reports in the output — only a SIGQUIT timeout dump). The off-tick->tick spawn boundary itself is proven race-clean by: the `./server/` Docker -race green (incl. the explicit two-goroutine `TestStructureSpawnRaceClean`), the `./world/structure/` Docker -race green, and the focused `./world/` worker/emit/neighborhood -race run green (the off-tick recording side — `TestEmitOnce`/`TestEmitOnceUnderHold`/`TestNeighborhoodCompletion`/`TestWorkerEmitsResult` all PASS under -race, no race).

## Known Stubs
| Stub | File | Reason |
|------|------|--------|
| `finalizeSpawn` (mob attribute/variant/profession init) | server/structure_spawn.go | The attribute/effect/variant subsystems are not built; the mob spawns at its vanilla-DEFAULT state (== the value finalizeSpawn leaves for default difficulty + no SpawnGroupData). Cited per CLAUDE.md; structured to become a real per-type call when the attribute subsystem lands — never baked away. PersistenceRequired is carried (no despawn flag yet) for the same reason. NOT a masquerading placeholder — the witch/cat/villager genuinely spawn, render, and are tracked; only their per-instance variant/profession is the default. |
| the mob_spawner BE has no spawner TICK yet | world/neighborhood.go (spawnerNBT) | The silverfish spawner block-entity carries the faithful `{SpawnData:{entity:{id:"minecraft:silverfish"}}}` (jar `BaseSpawner.setEntityId`), but the spawner's runtime tick (the Delay/MinSpawnDelay/SpawnCount countdown that actually spawns silverfish) is a separate subsystem. The BE is placed correctly so a future spawner-tick reads it; the block is visible + the entity id is set. |

## Threat Flags
None — no new network endpoint, auth path, or trust-boundary schema. The off-tick->tick spawn crossing is covered by T-20-10 (mitigated: the ChunkResult.Spawns handoff + the -race gate) and the spawn-count cap by T-20-12 (mitigated: spawnRequestCap=256). The reload double-spawn is T-20-11 (mitigated: the guards + the region-hit bypass).

## Self-Check: PASSED
All 8 created files exist on disk; all 3 task commits (19129b28, df210f3d, 69fa2ac0) are in git history.

---
*Phase: 20-structure-polish-loot-inhabitants-beard-persistence*
*Completed: 2026-06-27*
