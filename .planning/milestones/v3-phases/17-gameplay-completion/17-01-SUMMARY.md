---
phase: 17-gameplay-completion
plan: 01
subsystem: gameplay
tags: [entity-tracker, tab-list, player-info, persistence, inventory, tick-pipeline, wave-seams]

# Dependency graph
requires:
  - phase: 06-entities
    provides: "entityStore (add/remove/move/get/near) + generic entityTracker + EntityIDAllocator + encodeAddEntity/Teleport/Remove"
  - phase: 05-play
    provides: "play bootstrap (sendPlayBootstrap, bootstrapParams, writePlayerInfoUpdateAdd, confirm-teleport gate)"
provides:
  - "GAMEPLAY-01: players inserted into the entity store (id==entityID) + per-tick pos-sync + bidirectional PlayerInfoUpdate(ADD)/PlayerInfoRemove tab-list broadcast at join/leave"
  - "GAMEPLAY-02: persisted Pos/Rotation applied to the single bootstrap teleport + live player x/y/z/center"
  - "GAMEPLAY-03: first-tick ContainerSetContent join-sync guarded by a per-player bootstrapped flag"
  - "Wave-2 seam surface: fluidSchedule TickLoop field, fall-damage tickPlayer fields, lookupPlayerByEntityID reverse lookup, and the fluid.go/fall_damage.go stub seam files 17-02/17-03 overwrite"
affects: [17-02-fluid, 17-03-damage, 17-04-pvp, 17-06-item-drops]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Wave-2 conflict elimination: dispatcher hooks wired in Wave-1-owned tick_phases.go; bodies live in dedicated stub files (fluid.go/fall_damage.go) Wave-2 overwrites disjointly"
    - "Tab-list-precedes-AddEntity: broadcast PlayerInfoUpdate(ADD) at join before the tracker's next AddEntity"

key-files:
  created:
    - server/player_visibility.go
    - server/inventory_join.go
    - server/fluid.go
    - server/fall_damage.go
    - server/player_visibility_test.go
    - server/position_load_test.go
    - server/inventory_join_test.go
  modified:
    - server/tick.go
    - server/tick_phases.go
    - server/play_join.go
    - server/gameplay_tick.go

key-decisions:
  - "fluidSchedule lazy-inits inside tickFluids (nil-check), NOT at SetWorld — SetWorld lives in shared tick.go, so 17-02 touches ONLY fluid.go"
  - "ClientboundPlayerInfoRemove wire = VarInt count + raw 16-byte UUIDs (jar-verified ClientboundPlayerInfoRemovePacket.write = writeCollection UUIDUtil.STREAM_CODEC)"
  - "Persisted Pos feeds the single bootstrap teleport (no second teleport) — reuses the existing confirm gate (Pitfall 3)"
  - "syncJoinInventories + syncPlayerEntities guard nil-client players (tracker p.client==nil discipline) so debug/test fixtures never deref"

patterns-established:
  - "Pattern: Wave-1 owns shared tick-pipeline files (tick.go, tick_phases.go); Wave-2 plans overwrite their own dedicated stub seam file with zero shared-file edits"
  - "Pattern: player Entity constructed DIRECTLY (not NewEntity) so id==entityID and uuid==p.uuid"

requirements-completed: [GAMEPLAY-01, GAMEPLAY-02, GAMEPLAY-03]

# Metrics
duration: 9min
completed: 2026-06-25
---

# Phase 17 Plan 01: Gameplay Keystone (player visibility + position load + inventory join-sync) Summary

**Wired the three already-built-but-disconnected player-join seams — players now enter the entity store and broadcast their tab-list entry bidirectionally (so a second client renders them), reconnecting players spawn at their persisted position via the single bootstrap teleport, and the inventory window populates on the first tick — plus laid the exact Wave-2 seam surface (struct fields, dispatcher hooks, and two disjoint stub files) so 17-02/17-03 overwrite without any shared-file conflict.**

## Performance

- **Duration:** ~9 min
- **Started:** 2026-06-25T22:22:03Z
- **Completed:** 2026-06-25T22:31:19Z
- **Tasks:** 4 (Task 0 scaffold + Tasks 1-3 seams)
- **Files modified:** 4 modified, 7 created (11 total)

## Accomplishments
- GAMEPLAY-01 keystone: a joining player becomes an `entity.Player` (ID 156) in the tick-owned store with `id==entityID` + `uuid==p.uuid`, is position-synced from the authoritative `tickPlayer` every tick before `tracker.Tick`, and its `PlayerInfoUpdate(ADD_PLAYER)` is broadcast to every other player while existing players' entries are sent to the joiner; on leave the Entity is removed and a `PlayerInfoRemove` is broadcast.
- GAMEPLAY-02: a reconnecting player's persisted `Pos`/`Rotation` drives the SINGLE bootstrap teleport and seeds `tickPlayer.x/y/z` + view-ring `center` (no second teleport, no rubber-band).
- GAMEPLAY-03: authoritative `ContainerSetContent` fires exactly once on the first tick after register, guarded by `tickPlayer.bootstrapped`.
- Wave-2 foundation: all shared tick-pipeline edits (tick.go, tick_phases.go) are Wave-1-owned; 17-02/17-03 overwrite only their own dedicated stub file.

## Task Commits

1. **Task 0: struct fields + tick_phases hooks + Wave-2 stub seams + test scaffolds** - `8104d008` (feat)
2. **Task 1: GAMEPLAY-01 player Entity + pos-sync + bidirectional tab broadcast** - `96e99b9d` (feat)
3. **Task 2: GAMEPLAY-02 persisted position → single bootstrap teleport** - `19485d6b` (feat)
4. **Task 3: GAMEPLAY-03 inventory join-sync** - `c3032938` (feat)

_Note: the drainRegistrations/removePlayer join-leave seam (Task 1) and the bootstrapParams spawn fields (Task 2) landed in the tick.go/play_join.go edits committed alongside Tasks 0/1 because all four tasks' edits to the shared files were staged together; each task's logic is described in its commit message._

## Files Created/Modified
- `server/player_visibility.go` (created) - GAMEPLAY-01: `newPlayerEntity`, `syncPlayerEntities`, `lookupPlayerByEntityID`, `broadcastPlayerInfoAdd`, `sendExistingPlayersTo`, `broadcastPlayerInfoRemove`.
- `server/inventory_join.go` (created) - GAMEPLAY-03: `syncJoinInventories`.
- `server/fluid.go` (created) - STUB seam: `type fluidScheduleQueue struct{}` + `func (t *TickLoop) tickFluids()`. 17-02 OVERWRITES.
- `server/fall_damage.go` (created) - STUB seam: `func (t *TickLoop) tickFallDamage()`. 17-03 OVERWRITES.
- `server/tick.go` (modified) - tickPlayer + TickLoop fields; drainRegistrations/removePlayer join-leave seam.
- `server/tick_phases.go` (modified) - tickWorld→tickFluids(); tickEntities→syncPlayerEntities/syncJoinInventories/tickFallDamage.
- `server/play_join.go` (modified) - `writePlayerInfoUpdateRemove` + `playerInfoRemoveEncoder`; bootstrapParams spawn fields; sendPlayBootstrap hasSpawn override.
- `server/gameplay_tick.go` (modified) - persisted-position load applied to bootstrap + live player.
- `server/{player_visibility,position_load,inventory_join}_test.go` (created) - GAMEPLAY-01/02/03 unit tests.

## WAVE-2 HANDOFF (CRITICAL — 17-02 and 17-03 read this)

**Shared-file ownership:** `server/tick.go` and `server/tick_phases.go` are Wave-1-owned and are NOT edited by any Wave-2 plan. The dispatcher hooks are already wired; Wave-2 plans overwrite only their own dedicated stub file.

### 1. `fluidSchedule` field + init point (for 17-02 GAMEPLAY-05)
- **Field:** `fluidSchedule *fluidScheduleQueue` on the `TickLoop` struct in `server/tick.go` (declared just after the `entities *entityStore` field).
- **Type declaration:** `type fluidScheduleQueue struct{}` in `server/fluid.go` (the stub — 17-02 overwrites with the real per-gametime bucket/min-heap).
- **Init point (DECIDED):** **lazy-init inside `tickFluids` (nil-check), NOT at `SetWorld`.** Rationale: `SetWorld` lives at `server/tick.go:521` (a shared, Wave-1-owned file). To keep 17-02 touching ONLY `fluid.go`, 17-02 must construct the queue lazily inside `tickFluids` (e.g. `if t.fluidSchedule == nil { t.fluidSchedule = newFluidScheduleQueue() }`). A nil `fluidSchedule` is a valid "nothing scheduled" no-op state.
- **Call site:** `server/tick_phases.go` `tickWorld()` calls `t.tickFluids()` (already wired; do not edit tick_phases.go).

### 2. Fall-damage tickPlayer fields (for 17-03 GAMEPLAY-04)
Declared on the `tickPlayer` struct in `server/tick.go` (just after `debugGaveItems`):
- `fallDistance float64` — accumulated airborne descent.
- `wasOnGround bool` — previous-tick onGround (the false→true landing-edge detector).
- `lastY float64` — previous-tick y for the per-tick descent delta.
- **Reverse lookup:** `func (t *TickLoop) lookupPlayerByEntityID(id int32) *tickPlayer` is in `server/player_visibility.go` (provided by 17-01 so 17-03 never edits tick.go). Returns the tickPlayer whose `entityID==id`, or nil.
- **Call site:** `server/tick_phases.go` `tickEntities()` calls `t.tickFallDamage()` (already wired; do not edit tick_phases.go).

### 3. Stub seam files shipped (exact signatures 17-02/17-03 must match when overwriting)
- `server/fluid.go`: `type fluidScheduleQueue struct{}` and `func (t *TickLoop) tickFluids() {}`.
- `server/fall_damage.go`: `func (t *TickLoop) tickFallDamage() {}`.

### 4. Call-site hook locations (tick_phases.go — Wave-1-owned, do not edit)
- `tickWorld()` (server/tick_phases.go:~113): `t.tickFluids()` after `t.trace("tickWorld")`.
- `tickEntities()` (server/tick_phases.go:~179-181): after `t.tickDebug()`, in order `t.syncPlayerEntities()`, `t.syncJoinInventories()`, `t.tickFallDamage()` — all BEFORE `tracker.Tick`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] syncJoinInventories crashed on nil-client players**
- **Found during:** Task 3 (full-suite verification — `TestDebugPigUsesRealAI` and other fixtures build a `tickPlayer` with a nil `client`).
- **Issue:** `sendContent` dereferences `p.client.Send`; the new `syncJoinInventories` ran for every player including the debug/test fixtures that have no connection, panicking.
- **Fix:** `syncJoinInventories` now skips `p.client == nil`, matching the established `entityTracker.Tick` `p.client == nil` discipline.
- **Files modified:** `server/inventory_join.go`
- **Commit:** `c3032938`

### Implementation grouping note
All four tasks' edits to the SHARED files (`tick.go`, `tick_phases.go`, `play_join.go`) were implemented before the first commit, so the join/leave seam (Task 1) landed in the Task-0 commit and the bootstrapParams spawn fields (Task 2) landed in the Task-1 commit. Each piece of logic is described in its commit message; per-task dedicated files (player_visibility.go, gameplay_tick.go, inventory_join.go) are in their matching task commit.

## Verification
- `go build ./...` exits 0 (server module; the separate `tools/` module is excluded per CLAUDE.md).
- `go test ./server/...` passes (all new tests + existing, including `TestTickPhaseOrder`).
- Docker `-race` over `./server/...`: zero data races. The three new tests + `TestTickPhaseOrder` + the previously-flaky `TestTickAIDrivesMobs` pass 3× under `-race`.
- `grep "NOT applied for v1" server/gameplay_tick.go` returns nothing (the Pos-skip was removed).
- All four hooks present in `tick_phases.go` (`syncPlayerEntities`, `syncJoinInventories`, `tickFallDamage`, `tickFluids`).

## Pre-existing Flaky Test (NOT caused by this plan)
`TestTickAIDrivesMobs` (server/spawner_test.go:228) intermittently fails under full-suite load: it only exercises `tickAI`/`tickPhysics`/`applyAsyncResults` (none touched by this plan) and asserts an off-tick (OPT-01) async-pool path lands within 400 ticks. Under concurrent test load the pool starves and the path lands late, so the mob hasn't walked far enough yet. It passes consistently in isolation and 3× under `-race`. This plan touches no AI/physics/async-pool code; the flake is a timing sensitivity in the OPT-01 async-path budget and should be addressed separately (e.g. increase the tick budget or make the path landing deterministic in the test).

## Self-Check: PASSED
- All 7 created files verified present on disk.
- All 4 task commit hashes (8104d008, 96e99b9d, 19485d6b, c3032938) verified in git log.
