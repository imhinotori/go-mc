---
phase: 05-player-session-in-world-first-playable
plan: 01
subsystem: api
tags: [protocol-776, movement, teleport-gate, chunk-streaming, view-distance, jar-codegen]

# Dependency graph
requires:
  - phase: 03-deterministic-tick-loop
    provides: "applyInput subtick seam (TICK-03), dispatch, applyInputHook test seam, tickPlayer + bounded subtick buffer"
  - phase: 04-chunk-streaming
    provides: "centerOutRing/chunkCenterOf/floorDiv16, tickChunks/flushOutbound streamer keyed on p.center, sentChunks/centerSent state, world.SetChunkCacheCenter"
provides:
  - "applyInput decodes all 4 ServerboundMovePlayer* layouts (packed flags byte, not Boolean) into tick-owned position"
  - "recenterRing makes the view-distance ring FOLLOW the walking player (move center, re-emit SetChunkCacheCenter, forget far columns)"
  - "Teleport-id gate (PLAY-02): movement dropped until the echoed ServerboundAcceptTeleportation VarInt matches awaitingTeleport"
  - "world.ForgetLevelChunk encoder with the jar-derived 26.2 packed-Long ChunkPos layout"
  - "ServerboundPlayerLoaded routed as a no-op (records p.loaded, never gates streaming)"
affects: [05-02-teleport-id-producer, 05-03-capture-diff, 06-entity-physics]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Hook-before-gate ordering in applyInput (test seam fires before the teleport gate so subtick-ordering tests survive)"
    - "Bounded re-center: recenterRing called only on a real chunk-column crossing (newC != p.center) — within-column jitter is a no-op"
    - "Packed-flags-byte decode: trailing movement field read as UnsignedByte and masked (&1 onGround, &2 horizontalCollision)"

key-files:
  created:
    - server/movement_test.go
  modified:
    - server/subtick.go
    - server/tick.go
    - server/world_stream.go
    - world/packet.go
    - world/packet_test.go
    - server/world_stream_test.go

key-decisions:
  - "ClientboundForgetLevelChunk wire = a single packed Long (ChunkPos.pack: x low 32 bits, z high 32 bits), NOT the ≤773 wiki's VarInt z,x — jar-derived via javap"
  - "Trailing movement field decoded as a packed flags UnsignedByte (never Boolean), matching the 1.21.3+ jar shift"
  - "applyInputHook fires BEFORE the teleport gate to preserve the existing TestSubtickOrdering/TestSubtickBufferCap"
  - "Re-center gated on a real chunk-boundary crossing (T-5-05 thrash control)"

patterns-established:
  - "Pattern 1: jar-derive ambiguous wire layouts (ForgetLevelChunk) with javap before writing the encoder, recording the bytecode in a code comment"
  - "Pattern 2: single-owner movement — decode/apply/recenter all run on the tick goroutine over tick-owned state (-race clean)"

requirements-completed: [PLAY-04, PLAY-02]

# Metrics
duration: 18min
completed: 2026-06-23
---

# Phase 5 Plan 01: Handle Player Movement Summary

**The player now WALKS AROUND a world that follows it: applyInput decodes the 4 jar-confirmed ServerboundMovePlayer* layouts (packed flags byte) into tick-owned position, recenterRing re-streams the view ring on a chunk crossing (forgetting far columns via the jar-derived packed-Long ForgetLevelChunk), and movement is gated on the confirmed teleport id.**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-06-23
- **Completed:** 2026-06-23
- **Tasks:** 3
- **Files modified:** 7 (1 created, 6 modified)

## Accomplishments
- **PLAY-04 decode:** `applyInput` fills the Phase-3 stub — decodes Pos / PosRot / Rot / StatusOnly with the jar-confirmed field order, reading the trailing field as a **packed flags UnsignedByte** (`&0x01` onGround, `&0x02` horizontalCollision), updating tick-owned `x/y/z/yaw/pitch/onGround`.
- **PLAY-04 follow:** `recenterRing` moves `p.center`, resets `centerSent` (so `flushOutbound` re-emits `SetChunkCacheCenter`), prunes `sentChunks` of out-of-window columns, and sends one `ForgetLevelChunk` per dropped column — the void-on-walk failure mode is closed. Re-center is gated on a real chunk-boundary crossing (T-5-05).
- **PLAY-02 gate:** `applyInput` returns (after the hook) while `!confirmedTeleport`; dispatch sets `confirmedTeleport` **only** when the echoed `ServerboundAcceptTeleportation` VarInt equals `awaitingTeleport` (a forged id leaves the gate closed).
- **Jar-derived encoder:** `world.ForgetLevelChunk` encodes the exact 26.2 layout (a single packed Long), `ServerboundPlayerLoaded` routes as a no-op.

## Jar-verified ForgetLevelChunk layout
`javap -p -c ClientboundForgetLevelChunkPacket` → `write` calls `FriendlyByteBuf.writeChunkPos` → `ChunkPos.pack()` → a single big-endian `writeLong`. `ChunkPos.pack(x,z) = (x & 0xFFFFFFFF) | ((z & 0xFFFFFFFF) << 32)` (x in the LOW 32 bits, z in the HIGH 32 bits); `unpack(L)` does `x=(int)L, z=(int)(L>>32)`. The client reads it via `readChunkPos → readLong → unpack`. This is **not** the ≤773 wiki's "VarInt z, VarInt x" — encoding that would mis-frame the packet. Recorded in the `world.ForgetLevelChunk` code comment for 05-03's byte-diff.

## Movement decode (flags byte)
Per-variant field order (jar-confirmed): Pos = `Double x,y,z` + flags; PosRot = `Double x,y,z` + `Float yaw,pitch` + flags; Rot = `Float yaw,pitch` + flags; StatusOnly = flags only. The trailing flags field is **always** `pk.UnsignedByte` (masked), never a Boolean — a Boolean read would discard bit1 and mis-frame any byte > 0x01. A flags byte of `0x03` (TestMovementDecode/FlagsByteMasked) confirms onGround + clean framing.

## recenterRing shape
`recenterRing(p, newCenter)`: `p.center = newCenter`; `p.centerSent = false`; build `needed = centerOutRing(newCenter, viewDist)`; for each `pos` in `sentChunks` not in `needed`: `delete` + `client.Send(ForgetLevelChunk(pos))`. Nil-guards `client` and `sentChunks`. Called by `maybeRecenter` only when `chunkCenterOf(floor(x), floor(z)) != p.center`. The needed-ring stays server-clamped at `serverViewDistance=2`, so a wild coordinate can't enlarge the set (T-5-04).

## Teleport gate
`applyInput` runs the hook, records `lastInputAt`, then `if !p.confirmedTeleport { return }` — movement decode/apply is reached only after confirmation. `dispatch`'s AcceptTeleportation case scans the VarInt and sets `confirmedTeleport` only on `int(id) == p.awaitingTeleport`; a malformed payload or wrong id leaves the gate closed.

## Task Commits
1. **Task 0: jar-derive ClientboundForgetLevelChunk + encoder** - `88fd1a6a` (feat)
2. **Task 1: decode 4 movement layouts + teleport-id gate** - `ca9f6b78` (feat)
3. **Task 2: recenterRing — ring follows the player + forget far columns** - `1e422958` (feat)

_Task 1 also added the `awaitingTeleport`/`loaded`/position fields to `tickPlayer` and the dispatch validation; `recenterRing`'s body was authored alongside Task 1's call site and committed with its tests in Task 2._

## Files Created/Modified
- `world/packet.go` - Added `ForgetLevelChunk(cx,cz)` (jar-derived packed-Long).
- `world/packet_test.go` - `TestForgetLevelChunkWire` (round-trips (7,-3), asserts 8-byte body).
- `server/tick.go` - tickPlayer position fields + `awaitingTeleport`/`loaded`; dispatch validates the teleport id + PlayerLoaded no-op.
- `server/subtick.go` - `applyInput` movement decode (4 layouts, flags byte) + hook-before-gate + `maybeRecenter`.
- `server/world_stream.go` - `recenterRing` helper.
- `server/movement_test.go` (created) - `TestMovementDecode`, `TestTeleportGate`, `TestSubtickHookFiresBeforeGate`.
- `server/world_stream_test.go` - `TestRecenterRing`, `TestRingFollowsOnMove`.

## Gate Results
- `go build ./...`, `go vet ./...` — clean, zero new deps.
- `go test ./server/... ./world/...` (native) — all pass.
- **Docker `-race` over `./server/... ./world/...` (golang:1.26) — clean.**
- Flags-byte test (`TestMovementDecode/FlagsByteMasked`, 0x03 → onGround) — green.
- Existing `TestSubtickOrdering` / `TestSubtickBufferCap` / `TestTickPhaseOrder` / `TestDispatchAppendsSubtickInput` — all still green (hook-before-gate preserved).
- `TestRecenterRing` / `TestRingFollowsOnMove` — green (center moves, far columns forgotten, bounded, SetChunkCacheCenter re-emitted).

## Decisions Made
None beyond the plan — the jar-derived layout (packed Long) and the flags-byte decode were both pre-flagged in the plan and confirmed by javap.

## Deviations from Plan
None - plan executed exactly as written.

## Issues Encountered
None. The stale-LSP caveat held (the compiler resolved every symbol; `go build`/`-race` are the source of truth). The Docker `-race` gate ran via the documented `MSYS_NO_PATHCONV=1 docker run -v //d/ender://src` mount.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- **05-02** can now thread an incrementing bootstrap teleport id into `p.awaitingTeleport`; the gate that reads it is in place and tested.
- **05-03** can byte-diff `world.ForgetLevelChunk` against a real vanilla 26.2 capture — the jar-derived layout is recorded in the code comment.
- Movement physics/anti-cheat bounds remain Phase 6 (ENT-02); v1's structural control is the server-clamped ring.

## Self-Check: PASSED

All created/modified files exist on disk (movement_test.go, subtick.go, world_stream.go, tick.go, packet.go, packet_test.go, world_stream_test.go, 05-01-SUMMARY.md) and all three task commits (`88fd1a6a`, `ca9f6b78`, `1e422958`) are in git history.

---
*Phase: 05-player-session-in-world-first-playable*
*Completed: 2026-06-23*
