---
phase: 05-player-session-in-world-first-playable
plan: 02
subsystem: api
tags: [protocol-776, play-bootstrap, tab-list, abilities, spawn-position, teleport-id, jar-codegen]

# Dependency graph
requires:
  - phase: 04-chunk-streaming
    provides: "sendPlayBootstrap (Login -> GameEvent -> PlayerPosition) + commonPlayerSpawnInfoEncoder/levelsEncoder custom-FieldEncoder pattern; FIFO-before-register ordering invariant"
  - phase: 05-player-session-in-world-first-playable
    plan: 01
    provides: "tickPlayer.awaitingTeleport/confirmedTeleport fields + the dispatch teleport gate that reads awaitingTeleport"
provides:
  - "Early-Play tail builders (writePlayerAbilities, writeSetHeldSlot, writePlayerInfoUpdateAdd, writeSetDefaultSpawnPosition) appended to the bootstrap before register (PLAY-01)"
  - "Self tab-list entry (PLAY-05): 1-byte 8-action mask (0x0D) + count-prefixed UUID/name/gameMode/listed entry"
  - "gameTick.nextTeleportID(): atomic incrementing per-player teleport-id producer threaded into PlayerPosition AND tickPlayer.awaitingTeleport (PLAY-02 producer side)"
  - "AcceptPlayer threads real login name/uuid into the self tab-list entry, replacing the const placeholders"
affects: [05-03-capture-diff, 06-entity-physics]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "bootstrapParams struct carries per-player identity + issued teleport id into sendPlayBootstrap (avoids a long parameter list)"
    - "Atomic teleport-id producer on gameTick (issued off-tick on the accept goroutine; recorded in the off-tick-built tickPlayer handed to the owner via register — no tick-owned mutation off-tick, TICK-05)"
    - "writeEnumSet over an 8-value enum == a single pk.Byte mask (byte-identical FixedBitSet); entry actions written in enum order"

key-files:
  created: []
  modified:
    - server/play_join.go
    - server/gameplay_tick.go
    - server/play_join_test.go

key-decisions:
  - "SetDefaultSpawnPosition pinned to the jar RespawnData record: STREAM_CODEC composite(GlobalPos, FLOAT yaw, FLOAT pitch); GlobalPos = composite(ResourceKey<Level> dimension, BlockPos). Wire = Identifier + packed-Long BlockPos + Float + Float (W1 resolved)"
  - "PlayerInfoUpdate self-entry = writeEnumSet(8-action enum) -> 1-byte mask 0x0D (ADD_PLAYER|UPDATE_GAME_MODE|UPDATE_LISTED), then writeCollection: VarInt(1) + UUID + (enum order) String name + VarInt(0) properties + VarInt gameMode + Boolean listed"
  - "Teleport id is a per-gameTick atomic.Uint64 pre-incremented (first id == 1, never 0); same id feeds PlayerPosition and awaitingTeleport so the 05-01 gate matches the client echo"
  - "Removed const initialTeleportID=1 (replaced by the incrementing producer); joinEntityID left as-is for Login (the load-bearing tab-list key is the real UUID, not the Login entity id)"

patterns-established:
  - "Pattern: jar-derive every tail wire layout with javap (bytecode recorded in code comments) before writing the encoder; MEDIUM-confidence encoders flagged as Plan-05-03 capture-diff candidates"
  - "Pattern: extend the bootstrap by APPENDING after the proven three sends + before register — never rebuild the jar-verified Login/PlayerPosition scaffolding"

requirements-completed: [PLAY-01, PLAY-05, PLAY-02]

# Metrics
duration: 20min
completed: 2026-06-24
---

# Phase 5 Plan 02: Early-Play Tail + Incrementing Teleport-ID Producer Summary

**The joining player is now a real, listed player: the bootstrap appends the jar-derived early-Play tail (PlayerAbilities -> SetHeldSlot -> PlayerInfoUpdate self tab-list entry -> SetDefaultSpawnPosition) after the proven Login/GameEvent/PlayerPosition and before register, the real login name/UUID feed the self tab-list entry (PLAY-05), and a per-gameTick atomic incrementing teleport id is threaded into both the bootstrap PlayerPosition and tickPlayer.awaitingTeleport so the Plan-05-01 gate validates the client's Confirm Teleportation echo (PLAY-02).**

## What Changed

### Task 1 — early-Play tail builders (PLAY-01/05)

Four new builders in `server/play_join.go`, each with a field-by-field wire test in `server/play_join_test.go` (all jar-derived from the unobfuscated 26.2 inner jar via `javap`):

- **`writePlayerAbilities(invuln, flying, canFly, instabuild bool, flySpeed, walkSpeed float32)`** — `ClientboundPlayerAbilitiesPacket.write`: a single `Byte` flags (bit0 invuln `0x01`, bit1 flying `0x02`, bit2 canFly `0x04`, bit3 instabuild `0x08`) + `Float` flyingSpeed + `Float` walkingSpeed. Survival join sends `0x00`, 0.05, 0.1.
- **`writeSetHeldSlot(slot int32)`** — `ClientboundSetHeldSlotPacket.STREAM_CODEC` over `VAR_INT`: a single `VarInt` slot.
- **`writePlayerInfoUpdateAdd(id uuid.UUID, name string, gameMode int32)`** — `ClientboundPlayerInfoUpdatePacket.write` = `writeEnumSet(actions, Action.class)` then `writeCollection(entries)`.
- **`writeSetDefaultSpawnPosition(dimension string, pos pk.Position, yaw, pitch float32)`** — wraps `LevelData.RespawnData`.

### Task 2 — append the tail + incrementing teleport-id producer (PLAY-01/02/05)

- `sendPlayBootstrap` now takes a `bootstrapParams{name, id, teleportID, gameMode}` and, after the original Login -> GameEvent -> PlayerPosition, appends PlayerAbilities -> SetHeldSlot -> PlayerInfoUpdate(self) -> SetDefaultSpawnPosition. The PlayerPosition is driven by `params.teleportID` (no const).
- `gameTick.teleportSeq atomic.Uint64` + `nextTeleportID()` (pre-incremented, first id 1, never 0).
- `AcceptPlayer` issues the id, passes the real login `name`/`id` into the tail, and sets `tickPlayer.awaitingTeleport = teleportID` — all before `g.loop.register <- player` (FIFO invariant preserved). The id is computed and the `tickPlayer` built off-tick, then handed to the owner via `register` — no tick-owned state mutated off-tick (TICK-05).
- Removed the now-unused `const initialTeleportID = 1`.

## Jar-Verified Wire Layouts (the load-bearing facts)

### ClientboundPlayerAbilities
`Byte` flags (`0x01` invuln | `0x02` flying | `0x04` canFly | `0x08` instabuild) + `Float` flyingSpeed + `Float` walkingSpeed. (From `ClientboundPlayerAbilitiesPacket.write` bytecode: `ior` of `iconst_1/2/4/8` per flag, then two `writeFloat`.)

### ClientboundSetHeldSlot
Single `VarInt` slot (STREAM_CODEC composite over `ByteBufCodecs.VAR_INT`).

### ClientboundPlayerInfoUpdate (self entry — PLAY-05)
1. `writeEnumSet(actions, Action.class)` → the `Action` enum has **exactly 8** constants in this order: `ADD_PLAYER, INITIALIZE_CHAT, UPDATE_GAME_MODE, UPDATE_LISTED, UPDATE_LATENCY, UPDATE_DISPLAY_NAME, UPDATE_LIST_ORDER, UPDATE_HAT`. 8 bits → a single-byte FixedBitSet, byte-identical to `pk.Byte(mask)`. Self mask = `ADD_PLAYER|UPDATE_GAME_MODE|UPDATE_LISTED` = `0x01|0x04|0x08` = **`0x0D`**.
2. `writeCollection(entries)` → `VarInt(count)` then, per entry: `writeUUID(profileId)` (16 raw bytes) followed by the present actions' writers **in enum order**:
   - **ADD_PLAYER** (`lambda$static$1`): `String name` + `GAME_PROFILE_PROPERTIES` (a `VarInt(propertyCount)` list; offline sends `VarInt(0)`).
   - **UPDATE_GAME_MODE** (`lambda$static$5`): `VarInt(GameType.getId)`.
   - **UPDATE_LISTED** (`lambda$static$7`): `Boolean listed`.

   So the self entry is `UUID | String name | VarInt(0) | VarInt(gameMode) | Boolean(true)`.

### ClientboundSetDefaultSpawnPosition (W1 resolved)
Wraps `LevelData$RespawnData` (record: `GlobalPos globalPos`, `float yaw`, `float pitch`).
- `RespawnData.STREAM_CODEC` = `composite(GlobalPos.STREAM_CODEC, ByteBufCodecs.FLOAT, ByteBufCodecs.FLOAT)`.
- `GlobalPos.STREAM_CODEC` = `composite(ResourceKey.streamCodec(Registries.DIMENSION), BlockPos.STREAM_CODEC)`.
- On the wire: **`Identifier` dimension** + **packed-`Long` BlockPos** (`pk.Position`, X<<38|Z<<12|Y — matches `BlockPos.STREAM_CODEC`) + **`Float` yaw** + **`Float` pitch**. Four fields; this is the exact field count/types the Plan-05-03 capture-diff will seal to bytes.

## How the Teleport-ID Producer Wires to the Gate (PLAY-02)

`gameTick.teleportSeq` (an `atomic.Uint64`) is pre-incremented by `nextTeleportID()` each join (first id = 1, never 0; atomic because joins run on per-connection accept goroutines). `AcceptPlayer` issues one id and uses it twice: it is passed into `bootstrapParams.teleportID` → drives `writePlayerPositionPacket(params.teleportID, …)`, AND it is assigned to the off-tick-built `tickPlayer.awaitingTeleport`. The Plan-05-01 dispatch gate (`ServerboundAcceptTeleportation` → `confirmedTeleport = true` iff echoed VarInt == `awaitingTeleport`) therefore matches the exact id the client received. `TestBootstrapTeleportID` proves distinct ids across joins, decodes the id back out of the produced PlayerPosition, and drives the real dispatch path: a wrong echo leaves the gate closed, the matching echo confirms it.

## Gate Results

- `go test ./server/ -run 'TestPlayerInfoUpdateWire|TestPlayerAbilitiesWire|TestSetHeldSlotWire|TestSetDefaultSpawnPositionWire|TestBootstrapTailOrdering|TestBootstrapTeleportID' -count=1` — PASS.
- `go test ./server/ -run 'TestJoinSequenceOrdering|TestLoginPacketWireLayout|TestPlayerPositionPacketWireLayout|TestTeleportGate' -count=1` — PASS (existing bootstrap extended, not regressed). **TestJoinSequenceOrdering still green**: Login still first, LEVEL_CHUNKS_LOAD_START still precedes the chunk stream, and SetChunkCacheCenter still follows the bootstrap. `TestBootstrapTailOrdering` additionally asserts the full 7-packet order (3 core + 4 tail) lands before any chunk packet (W3 satisfied).
- `go test ./...` (full module) — PASS, no regressions.
- `go vet ./... && go build ./...` — clean; **zero new dependencies**.
- **Docker `-race` over `./server/...`** (`golang:1.26`) — clean.

## TDD Gate Compliance

Both tasks followed RED→GREEN. RED was confirmed for each (Task 1: undefined builders; Task 2: undefined `g.nextTeleportID`) before implementing. The two `feat(...)` commits combine the test+impl per task (the RED tests and GREEN implementation landed in the same atomic per-task commit rather than separate `test(...)`/`feat(...)` commits). No REFACTOR commit was needed.

## Capture-Diff Candidates Carried to Plan 05-03 (MEDIUM confidence)

- **PlayerInfoUpdate entry sub-encoding** — the `GAME_PROFILE_PROPERTIES` property-list shape (a `VarInt` count; each property would be `String name`, `String value`, `Optional<String> signature`) and the `listed` Boolean. Offline sends 0 properties, so only the count-prefix shape is exercised; the capture-diff seals the exact bytes.
- **SetDefaultSpawnPosition RespawnData bytes** — the field count/types are jar-confirmed; the exact byte sequence (esp. the `Identifier` + packed-`Long` framing) is sealed by the capture-diff against a real vanilla 26.2 server.

`SetTime` is deliberately NOT sent (A2 — optional for walking; add only if the 05-03 real-client test shows a kick without it).

## Deviations from Plan

None — plan executed exactly as written. Both tasks completed; the W1 (SetDefaultSpawnPosition signature) and W3 (ordering invariant) plan-checks were resolved without architectural change.

## Self-Check: PASSED

- `server/play_join.go` — FOUND (modified; contains `writePlayerInfoUpdateAdd`)
- `server/gameplay_tick.go` — FOUND (modified; contains `awaitingTeleport`)
- `server/play_join_test.go` — FOUND (modified; contains `TestPlayerInfoUpdateWire`)
- Commit `ccb116e8` (Task 1) — FOUND
- Commit `6c75fa8e` (Task 2) — FOUND
