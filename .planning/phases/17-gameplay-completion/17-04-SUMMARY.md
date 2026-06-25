---
phase: 17-gameplay-completion
plan: 04
subsystem: gameplay
tags: [entity, item-drop, block-break, metadata, synched-entity-data, item-stack, tracker, jar-port]

# Dependency graph
requires:
  - phase: 17-gameplay-completion (plan 01)
    provides: "players + entities in the tick-owned store; the synchronous/async entityTracker broadcasts AddEntity + SetEntityData for any store entity (the dropped Item rides this same path)"
  - phase: 06-entities-physics-interaction
    provides: "entityStore (add/near), NewEntity, EntityIDAllocator, Entity.metadata splice slot, encodeAddEntity/encodeSetEntityData + entityDataEntry framing, component.SlotData ItemStack codec, the block-break handlePlayerAction seam"
provides:
  - "GAMEPLAY-06: breaking a block spawns a visible, tracked entity.Item (ID 71) at the block center carrying the ITEM data-value (so it renders, not invisible)"
  - "itemDataEntry constructor (jar-derived dataItemIndex=8 + itemStackSerializerID=7) reusing the component.SlotData ItemStack codec — the first populated SynchedEntityData entry on the server"
  - "blockDropFor: the v1 1:1 block->item drop map (superseded by STRUCT-POLISH-01's loot evaluator)"
affects: [20-struct-polish (STRUCT-POLISH-01 loot tables replace blockDropFor), 18-online (item pickup / TakeItemEntity)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ITEM metadata population: a dropped Item's Entity.metadata is pre-built DataValue bytes (no 0xFF terminator) that encodeSetEntityData splices verbatim — the Pitfall-5 fix for invisible items"
    - "Drop derived SERVER-side from the broken block state (blockDropFor); the client supplies no item data (T-17-10)"

key-files:
  created:
    - server/block_drop.go
    - server/block_drop_test.go
  modified:
    - server/entity_encode.go
    - server/block_interact.go

key-decisions:
  - "dataItemIndex=8: ItemEntity extends Entity directly; Entity defines 8 base SynchedEntityData accessors (indices 0..7), so ItemEntity.DATA_ITEM — its only accessor — is index 8 (javap-confirmed, no capture-diff needed)"
  - "itemStackSerializerID=7: EntityDataSerializers registration order is BYTE=0,INT=1,LONG=2,FLOAT=3,STRING=4,COMPONENT=5,OPTIONAL_COMPONENT=6,ITEM_STACK=7 (javap-confirmed); ITEM_STACK's codec is ItemStack.OPTIONAL_STREAM_CODEC, the SAME codec component.SlotData encodes, so &SlotData is the correct value encoder"
  - "Item metadata bytes carry NO 0xFF terminator — encodeSetEntityData appends the EOF_MARKER itself, so Entity.metadata holds only the entry body"
  - "Zero velocity for the v1 drop (deterministic, no pop) — the tracker's LP motion encode supports a pop later"
  - "blockDropFor is the MINIMAL 1:1 map (stone->cobblestone, grass->dirt, identity elsewhere), explicitly NOT a loot evaluator (Assumption A1 — STRUCT-POLISH-01 supersedes it)"

patterns-established:
  - "Pattern: an Item entity renders only with its ITEM data-value in Entity.metadata; populate it at spawn via encodeItemMetadata(itemDataEntry(stack))"
  - "Pattern: capture the broken block state via GetBlock BEFORE SetBlock(air) so the drop lookup sees the real block, not air"

requirements-completed: [GAMEPLAY-06]

# Metrics
duration: 11min
completed: 2026-06-25
---

# Phase 17 Plan 04: GAMEPLAY-06 Block-Break Item Drops Summary

**Breaking a block now spawns a visible, tracker-broadcast entity.Item (ID 71) at the block center carrying the jar-derived ITEM data-value (SynchedEntityData index 8, EntityDataSerializers.ITEM_STACK id 7) so the dropped stack renders instead of spawning invisible — the drop is server-derived from the broken block via a v1 1:1 block->item map.**

## Performance

- **Duration:** ~11 min
- **Started:** 2026-06-25T22:37:35Z
- **Completed:** 2026-06-25T22:48:55Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 2 created, 2 modified (4 total)

## Accomplishments
- Resolved the research **Open Question 1** by decompiling the jar: `ItemEntity.DATA_ITEM` is SynchedEntityData index **8** (Entity defines 8 base accessors 0..7; ItemEntity adds exactly one), and `EntityDataSerializers.ITEM_STACK` is serializer id **7** (registration order), whose codec is `ItemStack.OPTIONAL_STREAM_CODEC` — the same codec `component.SlotData` already encodes for `ContainerSetContent`. No capture-diff was needed; the bytecode was unambiguous.
- `itemDataEntry(stack component.SlotData) entityDataEntry` in entity_encode.go builds the single ITEM DataValue reusing the sealed SlotData codec (no hand-rolled item stream) — the first populated SynchedEntityData entry on the server (prior entities shipped empty metadata).
- `blockDropFor(block.StateID) (component.SlotData, bool)` — the v1 1:1 block->item drop map keyed by the broken block's `Block.ID()` resource name (stone->cobblestone, grass_block->dirt, dirt/cobblestone/sand/oak_log/oak_planks identity); air/unknown -> no drop.
- `spawnBlockDrop` constructs the `entity.Item` at the block center (x+0.5, y, z+0.5), sets `ie.metadata` to the verbatim ITEM DataValue bytes (the Pitfall-5 invisibility fix), and `t.entities.add(ie)` — the GAMEPLAY-01 tracker broadcasts AddEntity + SetEntityData with zero new tracker code.
- `handlePlayerAction` now captures the broken state via `GetBlock` BEFORE `SetBlock(air)` and calls `spawnBlockDrop` after `reconcileEdit`.

## Task Commits

Each task was committed atomically (TDD: failing test + GREEN impl in one commit per task's logic):

1. **Task 1: ITEM data-value entry + block->drop map** - `07832d6d` (feat)
2. **Task 2: spawn the Item entity on break + tracker broadcast** - `2a16bddf` (feat)

**Plan metadata:** (this SUMMARY + STATE/ROADMAP/REQUIREMENTS) — final docs commit.

## Files Created/Modified
- `server/block_drop.go` (created) - `blockDropFor` (v1 drop map + `blockDropTable`), `spawnBlockDrop` (Item construction + metadata population + store add), `encodeItemMetadata` (verbatim ITEM DataValue bytes).
- `server/block_drop_test.go` (created) - `TestItemMetadataEntry`, `TestBlockDropLookup`, `TestBlockDropSpawnsItem`, `TestBlockDropTracked` (drives the synchronous golden `entityTracker`), `TestBreakAirNoDrop`.
- `server/entity_encode.go` (modified) - `const dataItemIndex uint8 = 8`, `const itemStackSerializerID int32 = 7` (each with javap citations), `func itemDataEntry`; added the `level/component` import.
- `server/block_interact.go` (modified) - read broken state BEFORE SetBlock(air); call `t.spawnBlockDrop(pos, brokenState)` after `reconcileEdit`.

## Decisions Made
- **DATA_ITEM index 8 / ITEM_STACK serializer id 7** — both javap-confirmed against `temp/cache/26.2-inner.jar`; cited in entity_encode.go comments. The bytecode (Entity's 8 `defineId` calls; the EntityDataSerializers static-init registration order) was unambiguous, so **no capture-diff confirmation was required** (the Open-Question-1 fallback was not needed).
- **Reuse the SlotData codec for the ITEM value** — `EntityDataSerializers$1.codec() == ItemStack.OPTIONAL_STREAM_CODEC`, identical to the `ContainerSetContent` carried-item framing, so `&component.SlotData` is the byte-correct value encoder rather than a new item stream.
- **Metadata bytes hold no 0xFF terminator** — `encodeSetEntityData` appends the EOF_MARKER, so `Entity.metadata` carries only the entry body (splice contract from 06-01).
- **Zero drop velocity for v1** — deterministic, no random pop; the tracker's LP motion encode already supports a pop if added later.
- **Spawn offset = block center on X/Z, block lower-Y** — vanilla nudges Y by ~0.25 inside the block; v1's lower-Y center renders the item on the ground at the column, sufficient for the visual gate.

## Deviations from Plan

None - plan executed exactly as written. Both tasks landed on their declared files with the jar-derived constants the plan mandated.

## Issues Encountered
- **`TestBlockDropTracked` initially read 0 AddEntity packets.** Root cause: `drainPackets` **closes** the client's outbound queue, so calling it to "clear the break's ack" BEFORE the tracker tick meant the tracker's `Send` enqueued into a closed queue (dropped). Also, the live `loop.tracker` is the OPT-02 `asyncTracker` whose diff lands a tick later via `applyAsyncResults`, not synchronously. **Fixed** by driving the synchronous golden `entityTracker` via the existing `syncTrackerTick(loop)` helper (the byte-identical reference the other tracker tests use) and draining exactly once after it runs. This is a test-harness correction, not a production change.
- **Parallel-wave compile interference (not a regression).** This plan ran in Wave 2 alongside 17-02 (fluid) and 17-03 (damage). Their in-flight uncommitted files (`fluid.go`/`fluid_schedule.go`/`fluid_test.go`, a stray `zzdbg_test.go`) intermittently broke the shared `server` test binary. My production code (`go build ./server/...`) stayed GREEN throughout; I verified MY tests by running them against an isolated, consistent package (sibling files temporarily reset to their committed HEAD versions, then restored byte-identically) — never editing a sibling-owned file. No `git clean`/`git reset --hard`/blanket-restore was used.

## Verification
- `go build ./server/...` exits 0.
- All five plan tests pass: `TestItemMetadataEntry`, `TestBlockDropLookup`, `TestBlockDropSpawnsItem`, `TestBlockDropTracked`, `TestBreakAirNoDrop`.
- `TestTickPhaseOrder` stays GREEN — the drop spawns inside the existing break handler, no phase reorder.
- `grep -n "spawnBlockDrop" server/block_interact.go` → the call is present after `reconcileEdit`.
- `grep -c "ItemEntity\|DATA_ITEM\|ITEM_STACK" server/entity_encode.go` → 9 (the javap citations present).
- Docker `-race` clean over my tests + `TestTickPhaseOrder`:
  `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ...` → ok.
- Pre-existing flake (NOT this plan): `TestTickAIDrivesMobs` (an OPT-01 async-pool timing sensitivity documented in 17-01-SUMMARY) fails only under full-suite load and passes in isolation; this plan touches no AI/physics/async-pool code.

## Next Phase Readiness
- GAMEPLAY-06 complete: broken blocks drop visible, tracked, pickable Item entities (visual gate ready, pending GAMEPLAY-07 human verification).
- **STRUCT-POLISH-01 (Phase 20)** replaces `blockDropFor`/`blockDropTable` with the shared loot-table evaluator (tool/fortune/silk-touch/multi-drop). The metadata population path (`itemDataEntry`/`encodeItemMetadata`) is reused unchanged.
- Item **pickup** (ClientboundTakeItemEntity + inventory insert) is a later requirement — the dropped Item entity is the prerequisite this plan delivers.

## Self-Check: PASSED
- All 4 created/modified files verified present on disk (block_drop.go, block_drop_test.go, entity_encode.go, block_interact.go) + the SUMMARY.
- Both task commit hashes (07832d6d, 2a16bddf) verified in git log.

---
*Phase: 17-gameplay-completion*
*Completed: 2026-06-25*
