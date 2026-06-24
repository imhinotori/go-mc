---
phase: 06-entities-physics-interaction
plan: 02
subsystem: entities
tags: [entity-tracker, visibility-diff, add-entity, set-entity-data, remove-entities, lp-vec3-quantizer, jar-derived, synchronous, tick-owned, phase-8-seam]

# Dependency graph
requires:
  - phase: 03-tick-loop
    provides: single-owner TickLoop, the tracker interface{ Tick() } seam (+ noopTracker stub), the fixed tickOnce phase order
  - phase: 06-01-entity-foundation
    provides: entityStore (near/get/move), EntityIDAllocator, the Entity instance (incl. the metadata []byte slot), per-section grid bucketing
  - phase: 01-codegen
    provides: data/entity table (SulfurCube id 130, AABB dims), data/packetid Clientbound entity ids, net/packet primitives, temp/cache/26.2-inner.jar (jar-derive source)
provides:
  - "entityTracker (server/tracker.go): the SYNCHRONOUS tracking executor assigned to TickLoop.tracker, FILLING the Phase-3 seam without changing the interface or the call site"
  - "Per-player visibility diff: near() broad-phase → AddEntity(+SetEntityData[+SetEntityMotion]) newly-visible / TeleportEntity(+RotateHead) moved / ONE batched RemoveEntities gone; per-player tracked map[int32]bool (tick-owned); a player never tracks itself"
  - "Jar-derived 776 entity encoders (server/entity_encode.go): AddEntity, SetEntityData (0xFF terminator), MoveEntityPos/PosRot/Rot, TeleportEntity, RotateHead, SetEntityMotion, RemoveEntities"
  - "lpVec3: the 26.x Vec3.LP_STREAM_CODEC quantizer (LpVec3.write — variable-width, NOT the legacy x8000 short); degToByteAngle; VecDeltaCodec 4096 move-delta scale"
  - "tickEntities filled (per-tick entity step + the bucket-consistency contract for 06-03 physics)"
  - "06-CAPTURE-DIFF.md: the jar-derived LP quantizer + SetEntityData 0xFF framing record (byte-seal deferred to 06-07)"
affects: [06-03-physics, 06-07-capture-diff, mob-spawning, combat, entity-persistence, 08-async-tracker-OPT-02]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Fill-the-seam, don't-change-it: the real entityTracker is assigned in NewTickLoop behind the UNCHANGED tracker interface + t.tracker.Tick() call site, so Phase 8 swaps the executor off-tick with no pipeline change"
    - "Synchronous, single-owner tracker (no goroutines, no xsync/ants/conc): all store reads + tracked-set writes happen on the tick goroutine; -race clean by construction"
    - "Per-player tracked set diff against the bounded near() broad-phase (never a full entity scan) — the DoS bound on the visible/tracked set (T-6-10)"
    - "Jar-derive MEDIUM wire surfaces (LpVec3 quantizer + SynchedEntityData 0xFF framing) via javap, record in CAPTURE-DIFF, byte-seal in a later capture-diff plan — the established proto-776 correctness discipline"

key-files:
  created:
    - server/tracker.go
    - server/tracker_test.go
    - server/entity_encode.go
    - server/entity_encode_test.go
    - .planning/phases/06-entities-physics-interaction/06-CAPTURE-DIFF.md
  modified:
    - server/tick.go
    - server/tick_phases.go
    - server/tick_test.go

key-decisions:
  - "The 26.2 AddEntity/SetEntityMotion movement is the NEW Vec3.LP_STREAM_CODEC -> net.minecraft.network.LpVec3 quantizer (DATA_BITS=15, SCALE_BITS=2, continuation bit; pack=round((d*0.5+0.5)*32766)), NOT the historical clamp(v,3.9)*8000 short. A stationary entity (velocity 0) encodes as a single 0x00 byte — the only path the v1 tracker exercises. The full quantizer is implemented for non-zero velocity; byte-correctness sealed by 06-07."
  - "AddEntity field order is jar-confirmed: VarInt id, UUID, VarInt typeId (registry codec, BEFORE x/y/z), Double x/y/z, LP movement, Byte xRot/yRot/yHeadRot, VarInt data. SetEntityData = VarInt id + DataValue entries + a MANDATORY single 0xFF (EOF_MARKER=255) terminator, ALWAYS present (even for an empty list)."
  - "v1 tracker uses TeleportEntity (absolute Doubles via PositionMoveRotation = 3 Double pos + 3 Double delta + Float yaw + Float pitch) for MOVED entities, NOT the MoveEntityPos short deltas — absolute avoids the short-delta overflow edge (delta>±8 blocks/tick). The 4096-scaled delta encoders exist + are tested for the later bandwidth optimization."
  - "The entityTracker is assigned in NewTickLoop (t.tracker = &entityTracker{loop:t}); the noopTracker was removed as dead code (the seam is FILLED — Phase 8 swaps to an async executor behind the same interface, never back to a noop). The interface + the tick_phases.go:26 call site are UNCHANGED."
  - "trackRange = 6 chunk columns (~96 blocks Chebyshev) — a single conservative v1 range (06-RESEARCH A5), the DoS bound via near()'s (2r+1)^2 column walk (T-6-10)."
  - "encodeSetEntityData splices any pre-built Entity.metadata bytes (the 06-01 snapshot-friendly slot) before the 0xFF terminator — the path a future plan/spawn-helper uses to ship an entity's default SynchedEntityData entries."

patterns-established:
  - "entityTracker.Tick() is the synchronous reference implementation behind the tracker seam; Phase 8 (OPT-02) swaps the executor off-tick via applyAsyncResults behind this exact interface"
  - "tickEntities is the per-tick entity step + the bucket-consistency contract: any position change MUST route through entityStore.move (re-buckets on a column cross) so near() is never stale — 06-03 physics fills the velocity integration here"

requirements-completed: [ENT-01]

# Metrics
duration: 32min
completed: 2026-06-24
tasks: 3
files-changed: 8
---

# Phase 6 Plan 02: Synchronous Entity Tracker Summary

A synchronous `entityTracker` fills the Phase-3 `tracker.Tick()` seam (ENT-01): each tick it
diffs every player's visibility against the entity store's `near()` grid broad-phase and emits
the jar-derived 776 wire packets — `AddEntity`(+`SetEntityData`) for newly-visible entities,
`TeleportEntity`(+`RotateHead`) for moved ones, and one batched `RemoveEntities` for those that
left range — so nearby players see entities spawn, move, and despawn. The seam shape (interface
+ call site) is unchanged, keeping it Phase-8-swappable.

## What was built

### Jar-derived entity encoders (`server/entity_encode.go`)

All wire layouts decompiled (`javap -p -c`) from `temp/cache/26.2-inner.jar`:

- **`encodeAddEntity`** — `VarInt id, UUID, VarInt typeId` (registry codec, BEFORE x/y/z),
  `Double x/y/z`, `LP movement`, `Byte xRot/yRot/yHeadRot`, `VarInt data`. The entity type id
  sits right after the UUID (not after the position, as the wiki implies).
- **`encodeSetEntityData`** — `VarInt id`, then the entity's pre-built `metadata` bytes + any
  explicit entries (each `Byte index, VarInt serializerId, value`), then a **mandatory single
  `0xFF` (EOF_MARKER=255) terminator** — always present, even for an empty list.
- **`lpVec3`** — the new 26.x `Vec3.LP_STREAM_CODEC` → `net.minecraft.network.LpVec3.write`
  quantizer (sanitize → `absMax` → `ceilLong` scale → 15-bit `pack` per axis → 3-bit header +
  6 bytes + optional `VarInt` scale extension). A zero/stationary velocity emits a single
  `0x00` byte; non-zero velocity uses the full quantized form. This replaces the historical
  `clamp(v,±3.9)*8000` short triple.
- **`encodeTeleportEntity`** — `VarInt id` + `PositionMoveRotation` (3 `Double` pos + 3
  `Double` delta + `Float yaw` + `Float pitch`) + `Int relativeFlags` (0 = absolute) +
  `Boolean onGround`.
- **`encodeMoveEntityPos/PosRot/Rot`** — the `4096`-scaled (`VecDeltaCodec.TRUNCATION_STEPS`)
  short-delta moves, with the jar-exact field order (PosRot writes yaw before pitch). Built +
  tested for the later bandwidth optimization; the v1 tracker favors teleport.
- **`encodeRotateHead`** (`VarInt id, Byte yHeadRot`), **`encodeSetEntityMotion`**
  (`VarInt id` + LP movement), **`encodeRemoveEntities`** (`writeIntIdList` = `VarInt count` +
  N `VarInt` ids — the tracker batches all gone ids into one).
- **`degToByteAngle`** — `round(deg * 256 / 360)` byte angle.

### Synchronous tracker (`server/tracker.go`) + seam assignment (`server/tick.go`)

`entityTracker{loop}` is assigned to `TickLoop.tracker` in `NewTickLoop` (replacing the
removed `noopTracker`) WITHOUT changing the `tracker interface{ Tick() }` shape or the
`t.tracker.Tick()` call site in `tick_phases.go`. `Tick()`, on the tick goroutine, for each
player: queries `loop.entities.near(p.x, p.z, trackRange)` (a bounded `(2r+1)²` column walk,
never a full scan), diffs against the player's tick-owned `tracked map[int32]bool` —
newly-in-range → `AddEntity`(+`SetEntityData`[+`SetEntityMotion` if moving]); still-tracked →
`TeleportEntity`(+`RotateHead`); no-longer-in-range → one batched `RemoveEntities` — and
**never tracks the player's own entity id**. No goroutine; no xsync/ants/conc.

### `tickEntities` (`server/tick_phases.go`)

Filled as the per-tick entity step with the **bucket-consistency contract**: any position
change must route through `entityStore.move` (re-buckets on a column cross) so the tracker's
`near()` read is never stale. This plan moves no entity (entity physics is 06-03), so the step
is a documented minimal marker; 06-03 fills the velocity integration here against the same
contract.

## Gate results

- `go test ./server/...` — **PASS** (all encoder + tracker + existing tick tests).
- `go vet ./...`, `go build ./...` — **clean**.
- `golangci-lint run ./server/` — **clean for all 06-02 files** (5 remaining issues are
  pre-existing in `server.go`/`configuration_test.go`, logged to `deferred-items.md`).
- **Docker `-race` over `./server/...`** (golang:1.26) — **clean** (the synchronous tracker
  mutates only tick-owned state on the owner goroutine — TICK-05 / T-6-08).
- `TestTickPhaseOrder` — **PASS** (the fixed phase order holds; the seam is filled, not
  reordered).
- Tracker visibility tests — **PASS**: `TestVisibilityDiff` (spawn once, no duplicate on an
  unmoved re-tick), `TestTrackerMove` (teleport+rotate, no re-add), `TestTrackerRemove`
  (one batched remove + clean re-entry), `TestTrackerRemoveBatchesMany` (3 gone → 1 packet,
  3 ids), `TestTrackerSelfNotTracked` (a player is never spawned its own entity),
  `TestTrackerSynchronousNoChangeToSeam` (the `*entityTracker` satisfies the unchanged
  one-method interface; bare `Tick()` is an inline no-op).

## Capture-diff surfaces deferred to 06-07

Recorded in `06-CAPTURE-DIFF.md` (jar-shape-correct now, byte-sealed there):

1. **LP quantizer byte-correctness for non-zero velocity** — the zero-vector `0x00` path is
   trivially correct; the quantized path needs a real-vanilla diff.
2. **`SetEntityData` v1 metadata** — confirm the empty-list `0xFF`-only body renders the
   `SulfurCube` in a real 26.2 client, or add the minimal required entry.
3. **`AddEntity` type-id registry index** — confirm `SulfurCube`'s `data/entity.ID` (130)
   matches the vanilla `ENTITY_TYPE` registry index on the wire (codegen-derived, HIGH
   confidence; the real-client visual seals it).

The self-round-trip encoder tests are symmetric regression guards — they do NOT prove
byte-identity with vanilla; that is 06-07's job.

## Deviations from Plan

**1. [Rule 1 - Bug] Updated `TestTrackerTickStub` for the now-filled seam**
- **Found during:** Task 2 (full test run).
- **Issue:** the pre-existing `TestTrackerTickStub` asserted `loop.tracker` is `noopTracker` —
  exactly the Phase-3 invariant this plan intentionally supersedes by filling the seam.
- **Fix:** updated the assertion to require the real `*entityTracker` (the seam is FILLED),
  keeping the test's still-valid synchronous once-per-tick proof via the swappable interface.
- **Files modified:** `server/tick_test.go`. **Commit:** 614ea852.

**2. [Rule 1 - Bug] Removed the now-dead `noopTracker` type**
- **Found during:** Task 2 (golangci-lint `unused`).
- **Issue:** with the real tracker assigned, `noopTracker`/`noopTracker.Tick` were unreferenced
  dead code (Phase 8 swaps to an async executor, never back to a noop).
- **Fix:** removed the type + method; the `tracker` interface doc now carries the seam history.
- **Files modified:** `server/tick.go`. **Commit:** 614ea852.

**3. [Rule 2 - Missing critical functionality] Wired `Entity.metadata` into `encodeSetEntityData`**
- **Found during:** Task 2 (golangci-lint flagged the 06-01 `metadata` field as unused).
- **Issue:** the `Entity.metadata []byte` slot 06-01 reserved "for the tracker to fill with the
  SynchedEntityData entries" had no consumer — the encoder ignored it.
- **Fix:** `encodeSetEntityData` now splices `e.metadata` verbatim before the `0xFF`
  terminator (the forward-looking path for an entity's default metadata), covered by
  `TestSetEntityDataMetadataSplice`. Resolves the unused-field warning AND completes the slot's
  intended wiring.
- **Files modified:** `server/entity_encode.go`, `server/entity_encode_test.go`. **Commit:** 614ea852.

**4. [Rule 3 - Out of scope] Pre-existing lint issues logged, not fixed**
- 5 `errcheck`/`staticcheck` issues in `server/server.go` + `configuration_test.go` predate
  06-02 — logged to `deferred-items.md`, not touched (deviation scope boundary).

## Self-Check: PASSED
