---
phase: 06-entities-physics-interaction
plan: 04
subsystem: api
tags: [block-edit, reconciliation, blockupdate, blockchangedack, subtick, protocol-776, chunk-manager]

# Dependency graph
requires:
  - phase: 06-entities-physics-interaction (06-01)
    provides: the subtick dispatch/applyInput pipeline + tickPlayer center/sentChunks view state
  - phase: 06-entities-physics-interaction (06-03)
    provides: dimMinY/floorDiv + the inline block-at-pos read (blockSolidAt) now promoted into world.GetBlock
  - phase: 04-world (04-03)
    provides: ChunkManager holder storage + per-player chunk streaming (center/sentChunks)
provides:
  - world.ChunkManager.SetBlock/GetBlock — the block-edit API (the explicit research gap, now closed)
  - ServerboundPlayerAction routed into the subtick buffer (the previously-missing dispatch route)
  - place/break handlers (handlePlayerAction/handleUseItemOn) with server-authoritative reach + defensive decode
  - the BlockChangedAck(sequence) + BlockUpdate-to-trackers reconciliation contract (anti-ghost-block)
  - jar-derived blockChangedAck/blockUpdate clientbound encoders
affects: [06-05 ENT-04 held-item->block place resolution, 06-07 interactive capture-diff, world persistence]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Single block mapping: world.ChunkManager owns the ONE pos->(column,section,local) mapping; physics reads it via GetBlock, edits write it via SetBlock — no competing duplicate"
    - "Reconciliation contract: a valid edit always sends BlockChangedAck(seq) to the editor + BlockUpdate to all chunk trackers (editor included) — omitting the ack leaves ghost blocks"
    - "Server-authoritative + defensive: reach gate (T-6-01) + loaded-column check + Scan-error-to-no-op (T-6-04) before any mutation; reject is a silent no-op (no SetBlock, no ack)"

key-files:
  created:
    - server/block_interact.go
    - server/block_encode.go
    - server/block_interact_test.go
  modified:
    - world/manager.go
    - world/manager_test.go
    - server/physics.go
    - server/tick.go
    - server/subtick.go

key-decisions:
  - "Used pk.Position as the BlockPos type for SetBlock/GetBlock/decode/encode (it is already the packed-BlockPos-long codec) rather than inventing a new BlockPos type"
  - "world package keeps a LOCAL floorDiv16 (signed >>4) since it cannot import server (import cycle); the mapping lives once in world.sectionLocal mirroring the generator"
  - "v1 place uses a fixed stand-in block (stone) at the adjacent face — real held-item->block resolution is ENT-04 (06-05); the load-bearing requirement is the wire handshake, not the placed block"
  - "Break triggers on STOP_DESTROY_BLOCK (survival dig finish, action=2) and START_DESTROY_BLOCK (creative instant, action=0); ABORT (1) and other actions are no-ops"
  - "Reach is a feet->block-center distance gate (6.0 blocks) — generous v1 bound, server-authoritative"

patterns-established:
  - "TDD per task: a test(...) RED commit then a feat(...) GREEN commit; a coordination refactor(...) for the 06-03 mapping promotion"
  - "Jar-derive serverbound/clientbound wire layouts via javap on temp/cache/26.2-inner.jar before encoding/decoding"

requirements-completed: [ENT-03]

# Metrics
duration: 7 min
completed: 2026-06-24
---

# Phase 6 Plan 04: Block Place/Break with BlockUpdate Reconciliation Summary

**Players place and break blocks via a new world.ChunkManager.SetBlock/GetBlock edit API, with ServerboundPlayerAction now routed into the subtick buffer and the BlockChangedAck(sequence) + BlockUpdate-to-trackers reconciliation handshake that prevents ghost blocks (ENT-03).**

## Performance

- **Duration:** 7 min
- **Started:** 2026-06-24T05:54:20Z
- **Completed:** 2026-06-24T06:01:39Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 8 (3 created, 5 modified)

## Accomplishments
- Closed the explicit research gap: `world.ChunkManager` gained `SetBlock(pos, state, minY) (changed bool)` and `GetBlock(pos, minY) (StateID, bool)`, mapping a world block pos -> (column via floor-div 16, section via `(y-minY)>>4`, local via `(y&15)<<8|(z&15)<<4|(x&15)`) over the tick-owned chunk, delegating to `level.Section.SetBlock` (which maintains the non-air BlockCount). Unloaded/out-of-range -> `changed=false`/`ok=false`, never mutates, never panics.
- Added the load-bearing dispatch route: `ServerboundPlayerAction` is now stamped + appended to the per-player subtick buffer (it previously fell to the `default:` no-op at tick.go and BREAK was a silent dead feature). `UseItemOn` remains routed; both now resolve in `applyInput` on-tick, sequence-ordered.
- Implemented place/break handlers that validate reach (server-authoritative, T-6-01) + loaded column, decode defensively (Scan-error -> no-op, never panic, T-6-04), then honor the reconciliation contract: `BlockChangedAck(sequence)` to the editor + `BlockUpdate(pos, newState)` broadcast to every player tracking the column (editor included, so a rejected/adjusted prediction snaps back — anti-ghost-block, Pitfall 5).
- Promoted 06-03's inline block-at-pos read into `world.GetBlock` so physics (read) and edits (write) share ONE mapping (the plan's coordination key_fact).

## Task Commits

Each task was committed atomically (TDD: test -> feat, plus one coordination refactor):

1. **Task 1 RED: ChunkManager block-edit API tests** - `8c57ccd2` (test)
2. **Task 1 GREEN: ChunkManager.SetBlock/GetBlock** - `ff64b3ae` (feat)
3. **Task 1 coordination: route physics blockSolidAt through world.GetBlock** - `5d5fb540` (refactor)
4. **Task 2 RED: place/break + reconciliation tests** - `04840b44` (test)
5. **Task 2 GREEN: route PlayerAction + handlers + encoders + reconciliation** - `fd29cafc` (feat)

**Plan metadata:** _this commit_ (docs: complete plan)

## Files Created/Modified
- `world/manager.go` - Added GetBlock/SetBlock + the shared columnAndSection lookup, local floorDiv16 + sectionLocal (mirrors the generator; one mapping). Modified.
- `world/manager_test.go` - TestSetBlock / TestSetBlockUnloaded / TestBlockPosMapping (negative-y/xz). Modified.
- `server/physics.go` - blockSolidAt now reads via world.GetBlock (one mapping); dropped the inline section/local read. Modified.
- `server/tick.go` - dispatch switch extended to route ServerboundPlayerAction into the subtick buffer. Modified.
- `server/subtick.go` - applyInput resolves PlayerAction (break) + UseItemOn (place) on-tick after the teleport gate. Modified.
- `server/block_encode.go` - jar-derived blockChangedAck (VarInt seq) + blockUpdate (Position + VarInt stateId) encoders. Created.
- `server/block_interact.go` - handlePlayerAction / handleUseItemOn / broadcastBlockUpdate / withinReach / directionNormal + the reconciliation contract. Created.
- `server/block_interact_test.go` - the 6 ENT-03 tests + jar-derived serverbound packet builders. Created.

## Decisions Made
- **pk.Position as the BlockPos type** for SetBlock/GetBlock/decode/encode — it is already the packed-BlockPos-long codec used by the spawn-position encoder, so reusing it keeps one BlockPos representation across read/write/wire.
- **v1 place = fixed stone stand-in at the adjacent face** — real held-item->block resolution lands with ENT-04 (06-05). The load-bearing ENT-03 requirement is the wire handshake (ack + update at the adjacent position), documented here as a v1 stand-in.
- **Break stages**: STOP_DESTROY_BLOCK (survival finish, action=2) and START_DESTROY_BLOCK (creative instant, action=0) trigger break; ABORT (1) and non-destroy actions are no-ops (intermediate dig stages are no-ops for v1).
- **Reach** is a feet->block-center distance gate (6.0 blocks) — a simple, generous, server-authoritative v1 bound.

## UseItemOn / PlayerAction field-order finding (jar-derived this session)

The plan flagged the UseItemOn field order as needing defensive jar-derivation. I javap'd `temp/cache/26.2-inner.jar` and confirmed the exact proto-776 wire layouts (no Scan mis-frame occurred — the place test passed first try with the jar-derived order):

- **ServerboundUseItemOn** (`ServerboundUseItemOnPacket(FriendlyByteBuf)`): reads **hand FIRST** (`readEnum` -> VarInt), **THEN** the BlockHitResult, **THEN** sequence (VarInt). The BlockHitResult (`FriendlyByteBuf.readBlockHitResult`) reads: BlockPos (packed long), Direction (`readEnum` -> VarInt), three Floats (cursor X/Y/Z), then **TWO** Booleans — `insideBlock` AND `worldBorderHit`. The second boolean (the "in some versions" the research flagged) IS present in 26.2. Full wire: `VarInt hand + Position pos + VarInt direction + Float cx + Float cy + Float cz + Boolean insideBlock + Boolean worldBorderHit + VarInt sequence`.
- **ServerboundPlayerAction** (`ServerboundPlayerActionPacket(FriendlyByteBuf)`): `VarInt action` (enum index) `+ Position pos` (BlockPos) `+ UnsignedByte direction` (`readUnsignedByte` -> `Direction.from3DDataValue`, NOT a VarInt) `+ VarInt sequence`. Action enum order: 0=START_DESTROY_BLOCK, 1=ABORT_DESTROY_BLOCK, 2=STOP_DESTROY_BLOCK, ...
- **Direction 3D-data values**: 0=DOWN(0,-1,0), 1=UP(0,1,0), 2=NORTH(0,0,-1), 3=SOUTH(0,0,1), 4=WEST(-1,0,0), 5=EAST(1,0,0) — the place-adjacent normal mapping.
- **ClientboundBlockUpdate** STREAM_CODEC = composite(BlockPos.STREAM_CODEC, idMapper(BLOCK_STATE_REGISTRY)) => packed Position long + VarInt stateId.
- **ClientboundBlockChangedAck** = a single VarInt sequence.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] UseItemOn carries TWO trailing booleans, not one**
- **Found during:** Task 2 (UseItemOn decode)
- **Issue:** The plan/research described the BlockHitResult as `{BlockPos, Direction, cursor Vec3, insideBlock}` (one trailing boolean) and flagged the field order as "confirm against the jar if the Scan mis-frames". Decoding only one boolean would leave the trailing `worldBorderHit` boolean + the sequence VarInt mis-framed, Scan-erroring every legitimate place into a silent no-op.
- **Fix:** javap of `ServerboundUseItemOnPacket` + `FriendlyByteBuf.readBlockHitResult` confirmed 26.2 reads TWO booleans (`insideBlock`, `worldBorderHit`). The decoder reads both, plus the jar-confirmed `hand FIRST` ordering.
- **Files modified:** server/block_interact.go (handleUseItemOn), server/block_interact_test.go (the useItemOnPacket builder)
- **Verification:** TestPlaceBlock / TestBlockBroadcastToTrackers pass (the place handshake frames correctly to zero trailing bytes).
- **Committed in:** fd29cafc (Task 2 GREEN)

**2. [Coordination - plan key_fact] Promoted physics' inline block read into world.GetBlock**
- **Found during:** Task 1 (after adding the API)
- **Issue:** 06-03 left an inline pos->section/local read in `blockSolidAt`. The plan's COORDINATION key_fact requires ONE mapping, in world.
- **Fix:** `blockSolidAt` now calls `t.world.GetBlock(pk.Position{...}, dimMinY)`; the inline mapping (and the now-unused `level` import) were removed from physics.go. Behavior preserved (unloaded/out-of-range -> non-solid air; world==nil guard kept).
- **Files modified:** server/physics.go
- **Verification:** TestEntityLands / TestEntityBlockedByWall / TestNoTunnel / TestGravityTunable / TestTickPhysicsRunsGravity / TestPlayerClipRejected all pass unchanged.
- **Committed in:** 5d5fb540 (refactor)

---

**Total deviations:** 2 (1 missing-critical decode fix, 1 planned coordination refactor).
**Impact on plan:** Both were anticipated by the plan (the UseItemOn order was explicitly flagged for jar-derivation; the mapping promotion is a stated key_fact). No scope creep, zero new dependencies.

## Issues Encountered
None. The jar-derived wire layouts framed correctly on the first GREEN run; both RED phases failed as expected (no premature pass).

## TDD Gate Compliance
Both tasks followed RED -> GREEN. Task 1: `8c57ccd2` (test) -> `ff64b3ae` (feat). Task 2: `04840b44` (test) -> `fd29cafc` (feat). The RED commits compiled-failed / behavior-failed before the GREEN implementations, confirming the tests exercised the new behavior rather than passing vacuously.

## Verification Results
- `go test ./world/ -run 'TestSetBlock|TestSetBlockUnloaded|TestBlockPosMapping' -count=1` — PASS
- `go test ./server/ -run 'TestPlayerActionRouted|TestBreakBlock|TestPlaceBlock|TestBlockBroadcastToTrackers|TestBlockReachRejected|TestBlockMalformed' -count=1` — PASS
- Existing tests (TestMovementDecode / TestTeleportGate / TestTickPhaseOrder + the 06-03 physics tests) — PASS (no regression)
- `go vet ./... && go build ./...` — clean; `go.mod`/`go.sum` unchanged (zero new deps)
- Docker `-race` over `./server/... ./world/...` — clean (`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/... ./world/...`)
- Full repo `go test ./...` — no failures

## Next Phase Readiness
- ENT-03 is implemented: players can place/break blocks with the reconciliation handshake, broadcast to all chunk trackers, validated server-side, all on the tick goroutine (TICK-05, -race clean).
- 06-05 (ENT-04) replaces the v1 stone place stand-in with real held-item -> block resolution; the wire handshake and the SetBlock/broadcast plumbing are already in place to consume the resolved state.
- 06-07's interactive capture-diff is the consumer-side proof of the place/break wire (the encoders here are the producer side).

## Self-Check: PASSED

All created files exist on disk (server/block_interact.go, server/block_encode.go, server/block_interact_test.go, world/manager.go modified, 06-04-SUMMARY.md). All task commits exist in git history (8c57ccd2, ff64b3ae, 5d5fb540, 04840b44, fd29cafc). All plan `<verification>` commands pass; Docker -race clean; zero new deps.

---
*Phase: 06-entities-physics-interaction*
*Completed: 2026-06-24*
