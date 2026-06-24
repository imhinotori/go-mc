---
phase: 06-entities-physics-interaction
plan: 01
subsystem: entities
tags: [entity-store, entity-id-allocator, grid-bucketing, broad-phase, tick-owned, aabb, snapshot-friendly]

# Dependency graph
requires:
  - phase: 03-tick-loop
    provides: single-owner TickLoop, the tracker interface{ Tick() } stub, teleportSeq atomic pattern
  - phase: 05-player-session
    provides: the bootstrap (play_join.go), incrementing teleport-id producer (the off-tick atomic claim pattern mirrored here)
  - phase: 01-codegen
    provides: data/entity (generated 776 table — 158 entities with Width/Height), server/internal/bvh primitive
provides:
  - "EntityIDAllocator: monotonic atomic.Int32 id allocator (first id 1, non-zero, never reused) — replaces the hard-coded joinEntityID=1"
  - "Entity instance struct: snapshot-friendly plain-value hot fields (id/type/uuid/pos/vel/angles/onGround/dims) + AABB() helper from data/entity Width/Height"
  - "entityStore: tick-owned by-id map + per-chunk-column grid buckets; add/get/remove/len, move() (re-buckets on column crossing), near(x,z,range) broad-phase"
  - "TickLoop.entities + TickLoop.idAlloc tick-owned fields wired in NewTickLoop; tickPlayer.entityID recorded per join"
affects: [06-02-tracker, 06-03-physics, entity-persistence, mob-spawning, combat, inventory]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-chunk-column grid bucketing (level.ChunkPos keys), NOT a quadtree — vanilla buckets entities per section/column (CLAUDE.md)"
    - "Off-tick atomic id claim (no game state crosses) — the teleportSeq discipline reused for entity ids, race-clean by construction"
    - "Snapshot-friendly plain-value entity hot fields so a Phase-8 async tracker can copy a cheap immutable snapshot"

key-files:
  created:
    - server/entity.go
    - server/entity_store.go
    - server/entity_store_test.go
  modified:
    - server/tick.go
    - server/gameplay_tick.go
    - server/play_join.go
    - server/play_join_test.go

key-decisions:
  - "EntityIDAllocator is an atomic.Int32 pre-increment (first id 1, non-zero, monotonic) claimed off-tick exactly like gameTick.teleportSeq — players AND entities draw from one id space, so a spawned entity can never collide a player's id (T-6-07); race-clean by construction (T-6-08)"
  - "The Entity instance type lives in package server (NewEntity copies data/entity.Entity's ID + Width/Height at spawn); data/entity.Entity remains the type-table record — no naming ambiguity, no second AABB type (reused server/internal/bvh)"
  - "Bucketing keys on chunk COLUMN via the existing chunkCenterOf/floorDiv16 (negative-correct), so an entity and a player at the same block land in the same bucket; near() walks only the (2r+1)^2 candidate columns (no full entity scan)"
  - "tickPlayer.entityID added (recorded per join) so a later plan can build the player's own Entity instance / reference it in the tracker"

patterns-established:
  - "near(x,z,rangeChunks) is the broad-phase the tracker (06-02) consumes — bounded by the player's clamped track range, never the world entity count"
  - "move(e,x,y,z) is the tick-owned mutation path physics (06-03) uses — it keeps the derived bucket index consistent with the entity's column"

requirements-completed: [ENT-01]

# Metrics
duration: 7min
completed: 2026-06-24
---

# Phase 6 Plan 01: Entity Foundation Summary

**Tick-owned entity store + Entity instance (snapshot-friendly hot fields, AABB from data/entity) + per-chunk-column grid bucketing with a near() broad-phase + a monotonic atomic EntityIDAllocator that replaces the hard-coded joinEntityID=1 so players and entities draw from one collision-free id space.**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-06-24T04:48:41Z
- **Completed:** 2026-06-24T04:55:06Z
- **Tasks:** 2
- **Files modified:** 7 (3 created, 4 modified)

## Accomplishments
- **Monotonic entity-ID allocator** (`EntityIDAllocator`, atomic.Int32 pre-increment): unique, non-zero, never-reused int32 ids; first id is 1; claimable off-tick from the accept goroutine like `teleportSeq` so the claim crosses no tick-owned game state — race-clean by construction (T-6-08). It **replaces the hard-coded `const joinEntityID = 1`** so a second player or a spawned entity can never collide the playerId (06-RESEARCH Pitfall 7 / T-6-07).
- **Entity instance struct** (`server/entity.go`): plain-value snapshot-friendly hot fields (`id`, `typ entity.ID`, `uuid`, `x/y/z`, `vx/vy/vz`, `yaw/pitch/headYaw`, `onGround`, `width/height` copied from the data/entity table, `metadata []byte`). `NewEntity` copies the table's wire ID + AABB dims at spawn; `AABB()` returns a feet-anchored `bvh.AABB[float64, bvh.Vec3[float64]]` (half-width on X/Z, base at y, top at y+height) sourced from `data/entity` (not hand-typed).
- **entityStore** (`server/entity_store.go`): tick-owned by-id map + per-chunk-column grid buckets. `add`/`get`/`remove`/`len`, `move(e,x,y,z)` re-buckets only on a column crossing, and `near(x,z,rangeChunks)` walks only the `(2r+1)^2` candidate columns (never a full entity scan) — the broad-phase the 06-02 tracker consumes.
- **Wired onto TickLoop**: `entities *entityStore` + `idAlloc *EntityIDAllocator` initialized non-nil in `NewTickLoop`; `AcceptPlayer` claims each player's entity id off-tick and threads it into the bootstrap `ClientboundLogin` playerId and the `tickPlayer.entityID` field.

## API delivered (for 06-02 / 06-03)

```go
// Allocator (tick-owned field; atomic, claimable off-tick like teleportSeq)
type EntityIDAllocator struct { /* next atomic.Int32 */ }
func (a *EntityIDAllocator) AllocID() int32 // unique, monotonic, non-zero (first == 1)

// Entity instance (players are entities too)
func NewEntity(id int32, t entity.Entity, x, y, z float64) *Entity
func (e *Entity) AABB() bvh.AABB[float64, bvh.Vec3[float64]]

// Store (tick-owned; mutated only on the tick goroutine)
func newEntityStore() *entityStore
func (s *entityStore) add(e *Entity)
func (s *entityStore) get(id int32) (*Entity, bool)
func (s *entityStore) remove(id int32)
func (s *entityStore) move(e *Entity, x, y, z float64) // re-buckets on column crossing
func (s *entityStore) len() int
func (s *entityStore) near(x, z float64, rangeChunks int) []*Entity // broad-phase
```

**How the 06-02 tracker queries it:** per player, call `loop.entities.near(p.x, p.z, p.viewDist)` (or a clamped track range) on the tick goroutine to get the candidate in-range entities to diff visibility against — the buckets are keyed by chunk column via the same `chunkCenterOf`/`floorDiv16` the streamer uses, so the broad-phase cost is bounded by the track range, not the world entity count.

## Task Commits

1. **Task 1 (TDD): Entity + entityStore + EntityIDAllocator + bucketing**
   - RED: `ef58337f` (test)
   - GREEN: `151d0904` (feat)
2. **Task 2: Wire store + allocator onto TickLoop, replace joinEntityID** - `87a1eb53` (feat)

_(No REFACTOR commit needed — the GREEN implementation was clean.)_

## Files Created/Modified
- `server/entity.go` - **modified** (appended to the existing Pos/Rot file): the `Entity` instance struct + `NewEntity` + `AABB()` helper.
- `server/entity_store.go` - **created**: `EntityIDAllocator`, `entityStore`, `columnOf`, `add/get/remove/move/len/near`, `unbucket`.
- `server/entity_store_test.go` - **created**: TestEntityIDAllocator (monotonic + concurrent-distinct), TestEntityStore, TestEntityBucketing (near + re-bucket on move), TestEntityAABB.
- `server/tick.go` - **modified**: `TickLoop.entities` + `TickLoop.idAlloc` fields, init in `NewTickLoop`, `tickPlayer.entityID` field.
- `server/gameplay_tick.go` - **modified**: `AcceptPlayer` claims the entity id off-tick from `g.loop.idAlloc.AllocID()`, threads it into `bootstrapParams` + `tickPlayer`.
- `server/play_join.go` - **modified**: removed `const joinEntityID = 1`; `writeLoginPacket(entityID int32, viewDist int)`; `bootstrapParams.entityID`.
- `server/play_join_test.go` - **modified**: updated `TestLoginPacketWireLayout` for the new signature; added `TestBootstrapEntityID`.

## Gate Results
- `go test ./server/ -run 'TestEntityIDAllocator|TestEntityStore|TestEntityBucketing|TestEntityAABB|TestBootstrapEntityID'` — **PASS**
- `TestTickPhaseOrder` — **PASS** (explicitly verified by name; the tick seam was not reshaped — fields added only)
- `TestLoginPacketWireLayout`, `TestJoinSequenceOrdering`, and the full `./server/... ./world/...` suites — **PASS** (no Phase 1-5 regression)
- `go vet ./...` — **clean**; `go build ./...` — **clean**
- **Docker `-race` over `./server/...` (golang:1.26) — clean.** The concurrent `AllocID` claim (off-tick accept-goroutine pattern) and the tick-owned store cross no boundary mutably (TICK-05 / T-6-08).
- Zero new dependencies; `data/entity` dims + `server/internal/bvh` reused (not rebuilt).

## Decisions Made
None beyond the key-decisions listed in frontmatter — followed the plan as specified. The plan offered allocator option (a) "single atomic counter" vs (b) "accept-side mirror"; chose (a) as the plan recommended, mirroring `teleportSeq` exactly.

## Deviations from Plan
None - plan executed exactly as written. (One non-deviation worth noting: `server/entity.go` already existed carrying the unrelated `Pos`/`Rot` types used by movement/command code, so the Entity definition was APPENDED rather than the file overwritten — the plan's intent of "create server/entity.go (the Entity struct)" is satisfied without disturbing existing types.)

## Issues Encountered
None. The stale-LSP caveat noted in the plan did not block — `go build ./...` was the authority throughout and stayed at exit 0.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- **06-02 (tracker)** is unblocked: it has `loop.entities.near(...)` as the per-player broad-phase and the `Entity` instance (snapshot-friendly hot fields + `AABB()`) to diff visibility and emit AddEntity/Move/RemoveEntities.
- **06-03 (physics)** is unblocked: `entityStore.move(e,x,y,z)` is the tick-owned mutation path that keeps the bucket consistent as an entity steps.
- The `joinEntityID=1` hazard (Pitfall 7) is closed; players and entities share one monotonic id space. The FIRST PLAYABLE join path is intact (the bootstrap now sends an allocated playerId; `TestJoinSequenceOrdering` + the full suite stay green).
- No blockers.

## Known Stubs
- `Entity.metadata []byte` is an empty slot today (no wire metadata) — intentional: Plan 06-02 fills the SynchedEntityData entries for AddEntity/SetEntityData. Documented in the plan (`<action>` step 1) and in the field comment. Not a goal-blocking stub for ENT-01 (this plan provides the store/allocator, not the tracker or metadata).

## Self-Check: PASSED

All created files exist on disk (server/entity.go, server/entity_store.go, server/entity_store_test.go, 06-01-SUMMARY.md) and all three task commits (ef58337f, 151d0904, 87a1eb53) are present in git history.

---
*Phase: 06-entities-physics-interaction*
*Completed: 2026-06-24*
