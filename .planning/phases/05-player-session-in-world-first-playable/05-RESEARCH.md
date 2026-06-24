# Phase 5: Player Session In-World (FIRST PLAYABLE) - Research

**Researched:** 2026-06-23
**Domain:** Minecraft Java proto-776 Play-state networking — serverbound player movement, teleport-ID handshake, tab-list (PlayerInfoUpdate), and the early-Play packet set a real 26.2 client needs to walk around
**Confidence:** HIGH (all load-bearing wire layouts jar-derived from `temp/cache/26.2-inner.jar` this session; the real-client walk-around remains the final gate)

## Summary

Phase 5 is **NOT greenfield**. A minimal Play bootstrap (`server/play_join.go`, commit `0fd96850`) already drives a real vanilla 26.2 client from login to *standing on solid ground* — the user confirmed "veo y puedo pisar." Phase 4 also already built the per-player view-distance chunk ring (`server/world_stream.go` + `tick_phases.go`), but that ring is **pinned to the spawn chunk `{0,0}`** and never re-centers. The entire delta of Phase 5 is closing the gap between "client stands still" and "client WALKS AROUND a ring that follows it, appears in the tab list, and the PLAY-01..06 bundle is hardened."

The single largest new behavior is **PLAY-04**: serverbound movement packets currently fall into the Phase-3 subtick buffer and are resolved by `applyInput`, a documented **stub** (`p.lastInputAt = in.At`) that never decodes position. Phase 5 must decode the four `ServerboundMovePlayer*` packets, update the tick-owned player position, and — when the player crosses a chunk boundary — re-center the ring: emit a new `SetChunkCacheCenter`, request the newly-needed columns, and forget the columns that fell out of range. The good news: the streaming machinery (`centerOutRing`, `tickChunks`, `flushOutbound`, `chunkCenterOf`) already exists and is parameterized on `p.center` — re-centering is a matter of *mutating `p.center` and resetting `p.centerSent`/pruning `p.sentChunks`*, not rebuilding the streamer.

The biggest **wire surprise** this research caught: the 776 `ServerboundMovePlayer*` packets no longer end in a single on-ground `Boolean`. They end in a **packed flags Byte** (`bit0 = onGround`, `bit1 = horizontalCollision`) — a 1.21.3+ shift the ≤773 wiki does not document. Decoding it as a boolean would mis-frame every movement packet. Similarly, `ClientboundPlayerInfoUpdate` now has **8 actions** (added `UPDATE_LIST_ORDER` + `UPDATE_HAT`), and several "obvious" early-Play packets (`SetDefaultSpawnPosition`, `SetTime`) were restructured in 26.x to delegate to composite codecs (`RespawnData`/`GlobalPos`, `WorldClock`/`ClockNetworkState`) — those are capture-diff candidates, not static-derivation candidates.

**Primary recommendation:** EXTEND `server/play_join.go` (add the early-Play tail: PlayerAbilities → SetHeldSlot → PlayerInfoUpdate(self) → SetDefaultSpawnPosition), make `applyInput` in `server/subtick.go` decode the four jar-confirmed movement layouts and update `p.center`, and add a `recenterRing` helper that mutates `p.center` + prunes `p.sentChunks` + sends `ForgetLevelChunk` for dropped columns. Gate movement acceptance on the confirmed teleport id (already half-wired via `confirmedTeleport`). Seal every uncertain layout (SetDefaultSpawnPosition, SetTime, PlayerInfoUpdate) with the Phase-4 capture-diff harness, and gate PLAY-06 on the real-client walk-around visual.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Decode serverbound movement (PLAY-04) | Tick goroutine (`applyInput` / `dispatch`) | — | Player position is tick-owned state (TICK-05); decode + apply must run on the owner |
| Ring re-center on movement (PLAY-04) | Tick goroutine (`tickChunks`/`flushOutbound`) | — | `p.center`/`p.sentChunks` are tick-owned; the existing streamer already keys on them |
| Teleport-ID issue + validation (PLAY-02) | Tick goroutine (movement gate) | Accept goroutine (initial issue in bootstrap) | The confirm arrives via `dispatch` on the owner; the id counter is per-player tick state |
| Early-Play bootstrap tail (PLAY-01/05) | Accept goroutine (`AcceptPlayer`/`sendPlayBootstrap`) | — | Per-connection one-shot sends through the bounded queue, BEFORE `loop.register` (the proven FIFO-ordering invariant) |
| Tab-list assembly (PLAY-05) | Accept goroutine (self-entry at join) | Tick goroutine (multi-player later) | v1 is single-self-entry at join; broadcast to others is Phase-6-adjacent but the self-entry is mandatory now |
| Chunk encode/stream (PLAY-03) | Already built (Phase 4) | — | `world_stream.go` + `tick_phases.go` — reuse verbatim, only re-target the center |

## Standard Stack

This phase adds **zero dependencies**. It is pure protocol work on the existing fork primitives. The "stack" is the set of fork APIs and generated packet IDs the plans call.

### Core (already built — EXTEND, do not rebuild)
| Symbol | File | Purpose | Phase-5 action |
|--------|------|---------|----------------|
| `sendPlayBootstrap` | `server/play_join.go` | Sends Login → GameEvent → PlayerPosition before `loop.register` | **EXTEND**: append the early-Play tail (abilities/held-slot/playerinfo/spawn-pos) |
| `writeLoginPacket` | `server/play_join.go` | Jar-verified ClientboundLogin (id 49, isFlat) | Reuse; only swap `joinEntityID`/spawn to real per-player values |
| `writePlayerPositionPacket` | `server/play_join.go` | proto-769+ PlayerPosition (teleport-id first, Int32 flags last) | Reuse; Phase 5 drives it with an **incrementing** teleport id, not the const `1` |
| `centerOutRing` | `server/world_stream.go` | Center-out Chebyshev ring, `(2r+1)²` bounded | Reuse verbatim — already parameterized on `(center, viewDist)` |
| `chunkCenterOf` / `floorDiv16` | `server/world_stream.go` | block→chunk column (negative-correct) | **THE SEAM Phase 5 calls** when movement lands (the file comment says so) |
| `tickChunks` / `flushOutbound` | `server/tick_phases.go` | Request + stream the ring per player | Reuse; they re-read `p.center` every tick, so re-centering is automatic once `p.center` moves and `centerSent`/`sentChunks` are reset |
| `applyInput` | `server/subtick.go` | **STUB** today (`p.lastInputAt = in.At`) | **THIS IS THE PLAY-04 INSERTION POINT** — decode movement, update `p.center` |
| `dispatch` | `server/tick.go` | Routes movement packets into the subtick buffer; records `confirmedTeleport` | Reuse routing; Phase 5 reads the AcceptTeleportation VarInt payload + validates |
| `world.SetChunkCacheCenter` | `world/packet.go` | `VarInt cx, VarInt cz` | Reuse — re-send on re-center |
| `world.ChunkBatchStart/Finished` | `world/packet.go` | Batch framing | Reuse |

### Supporting (new packet builders to ADD — IDs from `data/packetid/packetid.go`)
| Packet ID const | Direction | Purpose | Wire layout source |
|-----------------|-----------|---------|--------------------|
| `ServerboundMovePlayerPos` | in | x,y,z + flags-byte | **jar-confirmed (this session)** |
| `ServerboundMovePlayerPosRot` | in | x,y,z,yaw,pitch + flags-byte | **jar-confirmed** |
| `ServerboundMovePlayerRot` | in | yaw,pitch + flags-byte | **jar-confirmed** |
| `ServerboundMovePlayerStatusOnly` | in | flags-byte only | **jar-confirmed** |
| `ServerboundAcceptTeleportation` | in | VarInt teleportId | already routed; decode the VarInt |
| `ServerboundPlayerLoaded` | in | **UNIT (empty payload)** — client signals "world loaded" | jar-confirmed (StreamCodec.unit) |
| `ClientboundPlayerAbilities` | out | Byte flags + Float flySpeed + Float walkSpeed | **jar-confirmed** |
| `ClientboundSetHeldSlot` | out | VarInt slot | jar-confirmed (ByteBufCodecs.VAR_INT) |
| `ClientboundPlayerInfoUpdate` | out | EnumSet(8) actions + collection of entries | **jar-confirmed (action set)**; entry bodies capture-diff |
| `ClientboundSetDefaultSpawnPosition` | out | RespawnData = GlobalPos + Float + Float (**26.x restructure**) | jar-confirmed shape; **capture-diff the bytes** |
| `ClientboundForgetLevelChunk` | out | drop a column as the ring shrinks | jar-derive (VarInt z + VarInt x packed long, verify) |
| `ClientboundSetTime` | out (optional) | WorldClock + ClockNetworkState (**26.x restructure**) | **capture-diff if used** — not mandatory for walking |

### Fork primitives needed
| Primitive | Location | Note |
|-----------|----------|------|
| `pk.FixedBitSet` / `pk.NewFixedBitSet(n)` | `net/packet/types.go:80,649` | For the PlayerInfoUpdate action EnumSet — vanilla writes `writeEnumSet(actions, Action.class)` = a fixed bitset over 8 actions = **1 byte**. go-mc has `FixedBitSet` but **no `writeEnumSet` helper** — write the 1-byte action mask directly (`pk.Byte(mask)`), confirmed identical to a 1-byte FixedBitSet. |
| `pk.UnsignedByte` / `pk.Byte` | `net/packet/types.go` | movement flags-byte read; abilities flags-byte write |
| `pk.Double`, `pk.Float`, `pk.VarInt`, `pk.UUID`, `pk.String`, `pk.Identifier` | `net/packet/types.go` | field encode/decode (all already used by `play_join.go`) |

**Installation:** none. `go.mod` is unchanged — this is the payoff of the Phase-1 codegen + Phase-4 streamer investment.

## Architecture Patterns

### System Architecture Diagram

```
  Vanilla 26.2 client                          Sulfur (one tick goroutine owns all game state)
  ───────────────────                          ─────────────────────────────────────────────

  (login complete, Play state)
        │
        │  ◄── AcceptPlayer (accept goroutine, OFF-tick, per-conn queue) ──────────────┐
        │      sendPlayBootstrap: Login → GameEvent(LOAD_START) → PlayerPosition(tpID) │
        │      → PlayerAbilities → SetHeldSlot → PlayerInfoUpdate(self) → SpawnPos      │
        │      [all enqueued BEFORE loop.register → FIFO-guaranteed before chunks]      │
        │                                                                               │
        ├── ServerboundAcceptTeleportation(tpID) ──► dispatch ──► validate id ──► confirmedTeleport=true
        │                                            (tick goroutine)
        │                                                  │
        │  ◄── (tick streams ring centered on p.center) ◄──┤ tickChunks/flushOutbound
        │      SetChunkCacheCenter + ChunkBatch[ LevelChunkWithLight×N ]
        │
        │  (player walks)
        ├── ServerboundMovePlayerPosRot(x,y,z,yaw,pitch,flagsByte) ──► dispatch
        │        │                                                       │ (stamp At, append subtick buffer)
        │        │                                       resolveSubtickInputs ──► applyInput  ◄── PLAY-04 INSERT
        │        │                                                                    │  decode → if !confirmedTeleport: ignore
        │        │                                                                    │  update p.x/y/z/yaw/pitch
        │        │                                                                    │  newC = chunkCenterOf(x,z)
        │        │                                                                    │  if newC != p.center: recenterRing(p,newC)
        │        │                                                                    ▼
        │  ◄── SetChunkCacheCenter(newC) + new ring columns ◄── flushOutbound (next flush)
        │  ◄── ForgetLevelChunk(dropped columns) ◄────────────  recenterRing prune
        ▼
  walks around a solid, ticking, following world  ── PLAY-06 GATE: real-client visual ──
```

### Recommended Project Structure
No new packages. All work lands in existing `server/` files:
```
server/
├── play_join.go      # EXTEND: early-Play tail builders (abilities/heldslot/playerinfo/spawnpos) + incrementing tpID
├── world_stream.go   # ADD: recenterRing(p, newCenter) helper (prune sentChunks, ForgetLevelChunk dropped, reset centerSent)
├── subtick.go        # REPLACE applyInput stub: decode 4 movement layouts → update p position → re-center
├── tick.go           # tickPlayer: add x,y,z,yaw,pitch,onGround + nextTeleportID + awaitingTeleport; dispatch: decode AcceptTeleportation VarInt + validate
└── gameplay_tick.go  # AcceptPlayer: real per-player entityID + UUID/name threaded into PlayerInfoUpdate
```

### Pattern 1: Movement → tick-owned position → ring re-center (PLAY-04 — the core delta)
**What:** Movement packets are already routed by `dispatch` into the per-player subtick buffer. `resolveSubtickInputs` drains them chronologically through `applyInput`. Replace the `applyInput` stub body with movement decode + position update + conditional ring re-center.
**When to use:** Every `ServerboundMovePlayer{Pos,PosRot,Rot,StatusOnly}` input.
**Example:**
```go
// server/subtick.go — applyInput is the tick-owned resolution point (runs on the owner).
// Source: jar-confirmed layouts (ServerboundMovePlayerPacket$Pos/PosRot/Rot/StatusOnly.read)
func (t *TickLoop) applyInput(p *tickPlayer, in SubtickInput) {
    // PLAY-02 gate: ignore movement until the bootstrap teleport is confirmed.
    if !p.confirmedTeleport {
        return
    }
    switch packetid.ServerboundPacketID(in.Packet.ID) {
    case packetid.ServerboundMovePlayerPos:
        var x, y, z pk.Double
        var flags pk.UnsignedByte // bit0=onGround, bit1=horizontalCollision (jar-confirmed)
        if err := in.Packet.Scan(&x, &y, &z, &flags); err != nil { return }
        p.x, p.y, p.z = float64(x), float64(y), float64(z)
        p.onGround = flags&0x01 != 0
    case packetid.ServerboundMovePlayerPosRot:
        var x, y, z pk.Double
        var yaw, pitch pk.Float
        var flags pk.UnsignedByte
        if err := in.Packet.Scan(&x, &y, &z, &yaw, &pitch, &flags); err != nil { return }
        p.x, p.y, p.z = float64(x), float64(y), float64(z)
        p.yaw, p.pitch = float32(yaw), float32(pitch)
        p.onGround = flags&0x01 != 0
    case packetid.ServerboundMovePlayerRot:
        var yaw, pitch pk.Float
        var flags pk.UnsignedByte
        if err := in.Packet.Scan(&yaw, &pitch, &flags); err != nil { return }
        p.yaw, p.pitch = float32(yaw), float32(pitch)
        p.onGround = flags&0x01 != 0
    case packetid.ServerboundMovePlayerStatusOnly:
        var flags pk.UnsignedByte
        if err := in.Packet.Scan(&flags); err != nil { return }
        p.onGround = flags&0x01 != 0
        return // no position change → no re-center
    default:
        return
    }
    // Re-center the streaming ring when the player crosses a chunk boundary.
    newC := chunkCenterOf(int32(math.Floor(p.x)), int32(math.Floor(p.z)))
    if newC != p.center {
        t.recenterRing(p, newC)
    }
}
```

### Pattern 2: Ring re-center (drop-far + request-new) (PLAY-04)
**What:** When `p.center` moves, the existing `tickChunks`/`flushOutbound` will automatically request and stream the *new* ring on the next tick (they call `centerOutRing(p.center, p.viewDist)`). The only extra work is (a) re-send `SetChunkCacheCenter` and (b) forget the columns that left the window so the client unloads them and the server stops considering them sent.
**Why this is small:** the streamer is already idempotent and keyed on `p.center`. You do NOT rebuild it.
**Example:**
```go
// server/world_stream.go — runs on the owner goroutine over tick-owned state.
func (t *TickLoop) recenterRing(p *tickPlayer, newCenter level.ChunkPos) {
    old := p.center
    p.center = newCenter
    p.centerSent = false // flushOutbound re-emits SetChunkCacheCenter once for the new center

    // Forget columns that fell out of the new clamped window so the client unloads them
    // and a later move back re-streams them. Build the new needed-set once.
    needed := make(map[level.ChunkPos]bool, (2*p.viewDist+1)*(2*p.viewDist+1))
    for _, pos := range centerOutRing(newCenter, p.viewDist) {
        needed[pos] = true
    }
    for pos := range p.sentChunks {
        if !needed[pos] {
            delete(p.sentChunks, pos)
            p.client.Send(world.ForgetLevelChunk(pos[0], pos[1])) // drop from the client
        }
    }
    _ = old
}
```
> NOTE: `world.ForgetLevelChunk` must be ADDED to `world/packet.go` — jar-derive its exact field order (`ClientboundForgetLevelChunkPacket` packs the chunk pos as a single Long in some versions; verify against the 26.2 jar before committing). The packet id is `packetid.ClientboundForgetLevelChunk`.

### Pattern 3: Teleport-ID handshake (PLAY-02 hardening)
**What:** The bootstrap sends `PlayerPosition(tpID)`; the client echoes `ServerboundAcceptTeleportation(tpID)`. Today `dispatch` just sets `confirmedTeleport = true` without reading the id. Phase 5: issue an **incrementing** per-player teleport id, store the outstanding id, and only set `confirmedTeleport` when the echoed VarInt matches.
**Example:**
```go
// server/tick.go dispatch — decode and validate the echoed id.
case packetid.ServerboundAcceptTeleportation:
    if player != nil {
        var id pk.VarInt
        if err := p.Scan(&id); err == nil && int(id) == player.awaitingTeleport {
            player.confirmedTeleport = true
        }
    }
```
> The bootstrap PlayerPosition is sent off-tick in `AcceptPlayer` BEFORE `register`; pass the issued `tpID` into the `tickPlayer{awaitingTeleport: tpID}` constructed there so the tick knows what to match.

### Pattern 4: PlayerInfoUpdate self-entry (PLAY-05 — tab list)
**What:** Send one `ClientboundPlayerInfoUpdate` with the action set `{ADD_PLAYER, UPDATE_LISTED}` (+ `UPDATE_GAME_MODE`) for the joining player so it appears in its own tab list.
**Wire shape (jar-confirmed this session):** `EnumSet(8-action) as 1 byte` then `writeCollection` of entries; each entry = `UUID` then per-present-action body in enum order: `ADD_PLAYER`→`String name, VarInt propertyCount, [property: String name, String value, Boolean signed, (String sig)]`; `UPDATE_GAME_MODE`→`VarInt gameMode`; `UPDATE_LISTED`→`Boolean listed`.
**Example:**
```go
// server/play_join.go — minimal self-entry. Action mask bits (jar enum order):
//   ADD_PLAYER=0x01, INITIALIZE_CHAT=0x02, UPDATE_GAME_MODE=0x04, UPDATE_LISTED=0x08,
//   UPDATE_LATENCY=0x10, UPDATE_DISPLAY_NAME=0x20, UPDATE_LIST_ORDER=0x40, UPDATE_HAT=0x80
const (
    piuAddPlayer    = 0x01
    piuUpdateGameMode = 0x04
    piuUpdateListed = 0x08
)
func writePlayerInfoUpdateAdd(id uuid.UUID, name string, gameMode int32) pk.Packet {
    return pk.Marshal(
        int32(packetid.ClientboundPlayerInfoUpdate),
        pk.Byte(piuAddPlayer|piuUpdateGameMode|piuUpdateListed), // 1-byte action EnumSet
        pk.VarInt(1),          // writeCollection count
        pk.UUID(id),           // entry uuid
        pk.String(name),       // ADD_PLAYER: name
        pk.VarInt(0),          // ADD_PLAYER: property count (no skin for offline)
        pk.VarInt(gameMode),   // UPDATE_GAME_MODE
        pk.Boolean(true),      // UPDATE_LISTED
    )
}
```
> Capture-diff the property-list sub-encoding (`Boolean signed` + optional `String signature`) against vanilla even though offline mode sends zero properties — the count-prefix shape must match exactly.

### Pattern 5: The early-Play bootstrap tail (PLAY-01 hardening — what a real client needs to walk)
Append AFTER the existing three bootstrap packets, still before `loop.register` (preserving the FIFO invariant):
```
... Login → GameEvent(LOAD_START) → PlayerPosition(tpID)         (already sent)
  → PlayerAbilities(flags, flySpeed=0.05, walkSpeed=0.1)          (jar-confirmed simple)
  → SetHeldSlot(0)                                                (jar-confirmed: VarInt)
  → PlayerInfoUpdate{ADD_PLAYER|UPDATE_LISTED|UPDATE_GAME_MODE}   (self-entry, tab list)
  → SetDefaultSpawnPosition(...)                                  (capture-diff: RespawnData/GlobalPos)
```

### Anti-Patterns to Avoid
- **Decoding movement in `dispatch` instead of `applyInput`:** `dispatch` only routes/stamps; position must be resolved chronologically through the subtick buffer (TICK-03). Decode in `applyInput`.
- **Mutating `p.center` off-tick:** all position/center updates run on the owner goroutine. `AcceptPlayer` only sends per-conn packets and registers via message (TICK-05).
- **Treating the movement trailing byte as a Boolean:** it is a packed flags Byte in 776 (jar-confirmed). A boolean read mis-frames the stream.
- **Rebuilding the streamer for re-centering:** `tickChunks`/`flushOutbound` already key on `p.center`. Move the center, reset `centerSent`, prune `sentChunks` — done.
- **Sending `SetChunkCacheRadius` on every re-center:** radius is fixed (`serverViewDistance`); only `SetChunkCacheCenter` changes. (The current `flushOutbound` sends both under `!centerSent` — fine, but radius never changes, so it is harmless repetition.)

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Join Game / Login packet | A new ClientboundLogin builder | `writeLoginPacket` in `play_join.go` | Already jar-verified (id 49, isFlat, Holder<DimensionType> VarInt(1), full CommonPlayerSpawnInfo) + covered by `TestLoginPacketWireLayout` |
| PlayerPosition / teleport layout | A new proto-769+ position encoder | `writePlayerPositionPacket` | Already jar-verified (tpID first, Int32 flags last) + `TestPlayerPositionPacketWireLayout` |
| View-distance ring math | A new neighbor-ring computation | `centerOutRing` + `chunkCenterOf` | Already bounded `(2r+1)²`, negative-correct, center-out ordered |
| Per-player chunk streaming | A new request/flush pipeline | `tickChunks` + `flushOutbound` | Already idempotent, batched, sent-set-deduped, race-clean |
| Ordering Login-before-chunks | New sequencing logic | The existing "enqueue before `loop.register`" FIFO invariant | Proven by `TestJoinSequenceOrdering`; the NPE this fixed is documented |
| Packet IDs | Hand-numbered constants | `data/packetid/packetid.go` (generated) | All Phase-5 IDs already present: `ServerboundMovePlayer*`, `ClientboundPlayerInfoUpdate`, `ClientboundPlayerAbilities`, `ClientboundSetHeldSlot`, `ClientboundSetDefaultSpawnPosition`, `ClientboundForgetLevelChunk` |
| Bitset/varint/uuid encoding | New wire primitives | `net/packet/types.go` (`pk.*`) | `Byte`, `Double`, `Float`, `VarInt`, `UUID`, `String`, `FixedBitSet` all exist |
| Wire-correctness proof | Self-round-trip tests | The Phase-4 capture-diff harness (`temp/chunkcapture/` pattern + golden fixture) | A symmetric round-trip passed even on the broken chunk wire; only a vanilla capture-diff caught it |

**Key insight:** Phase 4 already de-risked the hardest half of "first playable" (Login, position, chunk wire, streaming). Phase 5's job is *decode-inbound + re-center + tab-list*, almost entirely composing existing pieces. The only genuinely new encoders are PlayerAbilities/SetHeldSlot/PlayerInfoUpdate/SetDefaultSpawnPosition/ForgetLevelChunk — three of which are trivial and two of which (SpawnPos, optional SetTime) are capture-diff candidates.

## Runtime State Inventory

Not a rename/refactor/migration phase — this is additive feature work on the existing player-session slice. No stored data, live-service config, OS-registered state, secrets, or build artifacts carry a string that this phase renames.

- **Stored data:** None — the superflat world is generated deterministically; no player data is persisted until Phase 6 (ENT-06).
- **Live service config:** None.
- **OS-registered state:** None.
- **Secrets/env vars:** None.
- **Build artifacts:** None — verified: `go.mod` unchanged (zero new deps), all packet IDs already generated.

## Common Pitfalls

### Pitfall 1: Movement trailing byte is a packed flags Byte, not a Boolean
**What goes wrong:** Decoding `ServerboundMovePlayer*` with a trailing `Boolean onGround` (per the ≤773 wiki) leaves 0 bytes consumed correctly for the boolean but mis-models the field; worse, future fields shift. The 776 reality is `Byte` with `bit0=onGround`, `bit1=horizontalCollision`.
**Why it happens:** A 1.21.3+ shift the community wiki (documented ≤773) does not cover; training data predates it.
**How to avoid:** Decode the trailing field as `pk.UnsignedByte` and mask (`&0x01`, `&0x02`). **[VERIFIED: jar — `ServerboundMovePlayerPacket.unpackOnGround` masks `& 1`, `unpackHorizontalCollision` masks `& 2`; all four subclass `read()` end in `readUnsignedByte`.]**
**Warning signs:** Movement appears to work but `Scan` returns `unexpected EOF` or trailing-bytes mismatch in a wire test; the client rubber-bands.

### Pitfall 2: Ring does not re-center → world unloads into void as you walk (the PLAY-04 failure mode)
**What goes wrong:** Player walks past the spawn ring; chunks stop appearing; the client renders void at the edges and eventually the player "walks off the world."
**Why it happens:** `p.center` is pinned to `{0,0}` (the Phase-4 default); `tickChunks`/`flushOutbound` keep streaming the same 25 columns.
**How to avoid:** Update `p.center` in `applyInput` and call `recenterRing`. Verify by walking >2 chunks in any direction and confirming new `SetChunkCacheCenter` + new `LevelChunkWithLight` arrive (capture in a pipe test; confirm in the real-client visual).
**Warning signs:** No `SetChunkCacheCenter` after the initial one in a movement pipe test.

### Pitfall 3: Accepting movement before teleport confirmed → spawn-position fight
**What goes wrong:** The server processes early movement packets the client sent before it acknowledged the spawn teleport, fighting the authoritative spawn; the client snaps back ("rubber-band on join").
**Why it happens:** Vanilla clients send movement immediately; the server must drop movement until the matching `ServerboundAcceptTeleportation` arrives.
**How to avoid:** Gate `applyInput` on `p.confirmedTeleport` (already a field) and validate the echoed VarInt id matches `p.awaitingTeleport`. **[CITED: vanilla `ServerGamePacketListenerImpl.handleMovePlayer` ignores moves while an outstanding teleport is awaited.]**
**Warning signs:** Player snaps to spawn repeatedly right after joining.

### Pitfall 4: PlayerInfoUpdate action set sized for 6 actions instead of 8
**What goes wrong:** Encoding the action EnumSet with the ≤773 6-action layout mis-sizes the bitset / mis-aligns entry bodies; the client rejects the packet or shows no tab entry.
**Why it happens:** 26.2 added `UPDATE_LIST_ORDER` and `UPDATE_HAT` (now 8 actions). **[VERIFIED: jar — `ClientboundPlayerInfoUpdatePacket$Action` enum lists ADD_PLAYER, INITIALIZE_CHAT, UPDATE_GAME_MODE, UPDATE_LISTED, UPDATE_LATENCY, UPDATE_DISPLAY_NAME, UPDATE_LIST_ORDER, UPDATE_HAT.]**
**How to avoid:** Write the action set as a 1-byte mask with the jar enum-order bit positions (ADD_PLAYER=0x01 … UPDATE_HAT=0x80). 8 actions still fit in 1 byte, so a single `pk.Byte` matches `writeEnumSet`. Capture-diff to confirm.
**Warning signs:** Player missing from its own tab list, or a decode error on the client.

### Pitfall 5: SetDefaultSpawnPosition / SetTime use 26.x composite codecs
**What goes wrong:** Encoding `SetDefaultSpawnPosition` as the ≤773 `Position(BlockPos) + Float angle` mis-frames it; 26.2 delegates to `LevelData$RespawnData = GlobalPos(dimension ResourceKey + BlockPos) + Float + Float`. `SetTime` now composes `WorldClock + ClockNetworkState` (not the old two Longs).
**Why it happens:** 26.x restructured both packets into record codecs. **[VERIFIED: jar — `ClientboundSetDefaultSpawnPositionPacket.STREAM_CODEC` = `RespawnData.STREAM_CODEC`; `RespawnData` holds a `GlobalPos` + two floats. `ClientboundSetTimePacket.STREAM_CODEC` composes `WorldClock.STREAM_CODEC` + `ClockNetworkState.STREAM_CODEC`.]**
**How to avoid:** Capture-diff both against the vanilla 26.2 server before committing. **Treat `SetTime` as optional** for "walk around" — the client renders and moves without it (it only affects sky/day cycle). `SetDefaultSpawnPosition` is recommended (compass/respawn anchor) but verify with a capture, and confirm via the real-client test whether its absence causes any kick.
**Warning signs:** A kick at join citing a malformed packet; or a compass that points nowhere (cosmetic).

### Pitfall 6: ForgetLevelChunk field order guessed, not jar-verified
**What goes wrong:** Dropping far chunks with a wrong `ForgetLevelChunk` layout silently fails to unload (memory leak on the client) or errors.
**Why it happens:** Across versions this packet has been both "VarInt z, VarInt x" and a single packed Long.
**How to avoid:** Jar-derive `ClientboundForgetLevelChunkPacket` for 26.2 before writing `world.ForgetLevelChunk`. (Decompile it the same way this research decompiled the movement packets.)
**Warning signs:** Walking back and forth across a boundary grows client memory / leaves ghost chunks.

## Code Examples

### Decoding a movement packet (jar-confirmed field order)
```go
// ServerboundMovePlayerPosRot — Source: jar ServerboundMovePlayerPacket$PosRot.read:
//   readDouble x, readDouble y, readDouble z, readFloat yaw, readFloat pitch, readUnsignedByte flags
var x, y, z pk.Double
var yaw, pitch pk.Float
var flags pk.UnsignedByte
if err := in.Packet.Scan(&x, &y, &z, &yaw, &pitch, &flags); err != nil { return }
onGround := flags&0x01 != 0
horizontalCollision := flags&0x02 != 0
```

### PlayerAbilities (jar-confirmed)
```go
// Source: jar ClientboundPlayerAbilitiesPacket.write:
//   writeByte(flags: 0x01 invulnerable | 0x02 flying | 0x04 canFly | 0x08 instabuild),
//   writeFloat(flyingSpeed), writeFloat(walkingSpeed)
func writePlayerAbilities(invuln, flying, canFly, instabuild bool, flySpeed, walkSpeed float32) pk.Packet {
    var f byte
    if invuln { f |= 0x01 }
    if flying { f |= 0x02 }
    if canFly { f |= 0x04 }
    if instabuild { f |= 0x08 }
    return pk.Marshal(
        int32(packetid.ClientboundPlayerAbilities),
        pk.Byte(f), pk.Float(flySpeed), pk.Float(walkSpeed),
    )
}
// Survival default: writePlayerAbilities(false,false,false,false, 0.05, 0.1)
```

### SetHeldSlot (jar-confirmed)
```go
// Source: jar ClientboundSetHeldSlotPacket.STREAM_CODEC = ByteBufCodecs.VAR_INT composite (single slot index)
func writeSetHeldSlot(slot int32) pk.Packet {
    return pk.Marshal(int32(packetid.ClientboundSetHeldSlot), pk.VarInt(slot))
}
```

### Capture-diff harness (reuse the Phase-4 pattern for the uncertain packets)
```
# Boot the real vanilla 26.2 server (temp/cache/26.2-server.jar, Java 25, offline, flat),
# connect a bot through the fork's net/bot, and dump the bytes of:
#   ClientboundSetDefaultSpawnPosition, ClientboundPlayerInfoUpdate, ClientboundForgetLevelChunk
# Commit each as a golden fixture; assert Sulfur's encoder is byte-identical.
# Pattern proven in .planning/phases/04-world-chunk-system/WORLD-CAPTURE-DIFF.md
```

## State of the Art

| Old Approach (≤773 wiki / training data) | Current 776 Approach (jar-confirmed) | When Changed | Impact |
|------------------------------------------|--------------------------------------|--------------|--------|
| Movement packets end in `Boolean onGround` | End in packed `Byte` flags (`bit0=onGround`, `bit1=horizontalCollision`) | 1.21.3 | Movement decode MUST mask a byte, not read a bool |
| PlayerPosition: flags byte early, no teleport id field reorder | proto-769+: teleport id FIRST, Int32 flags LAST, DX/DY/DZ velocity doubles | 1.21.2 (769) | Already handled in `play_join.go` (de-risked) |
| PlayerInfoUpdate: 6 actions | 8 actions (+UPDATE_LIST_ORDER, +UPDATE_HAT) | 1.21.2→26.x | Action EnumSet covers 8 bits; still 1 byte |
| SetDefaultSpawnPosition: `Position + Float angle` | `RespawnData = GlobalPos + Float + Float` | 26.x | Capture-diff before encoding |
| SetTime: `Long worldAge + Long timeOfDay` | `WorldClock + ClockNetworkState` composite | 26.x | Optional for walking; capture-diff if used |
| New since some versions | `ServerboundPlayerLoaded` (UNIT/empty) — client signals world loaded | 1.21.4 | Route as a no-op acknowledgment; do not require its payload |

**Deprecated/outdated:**
- The minecraft.wiki protocol page (documented to 1.21.10/773) is **authoritative for nothing in 776's movement/player-info layouts** — it predates the flags-byte and the 8-action set. The jar is the only source.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Survival ability defaults `flySpeed=0.05, walkSpeed=0.1` and flags=0 are what the client expects | Code Examples | Low — cosmetic movement speed; client clamps; confirm via real-client walk speed |
| A2 | `SetTime` is NOT mandatory for "walk around" | Pitfall 5 / Stack | Low — only affects day/night & sky; if the client kicks without it, add it (capture-diff). Confirmed disposable by reasoning, not by a no-SetTime client run |
| A3 | A single `pk.Byte` mask is byte-identical to vanilla `writeEnumSet(actions, Action.class)` for ≤8 actions | Pattern 4 | Medium — verify by capture-diff; `writeEnumSet` uses a FixedBitSet of `ceil(8/8)=1` byte, so this should hold |
| A4 | `ForgetLevelChunk` 26.2 layout needs jar verification before encoding | Pattern 2 / Pitfall 6 | Medium — flagged explicitly as a jar-derive task, not assumed |
| A5 | `SetDefaultSpawnPosition` absence does not kick the client | Pitfall 5 | Low-Medium — recommended to send; the real-client test confirms |
| A6 | Sending the early-Play tail (abilities/heldslot/playerinfo/spawnpos) off-tick in `AcceptPlayer` before `register` is safe and preserves ordering | Pattern 5 | Low — same proven FIFO-before-register invariant the existing three bootstrap packets rely on |

## Open Questions

1. **Exact `ClientboundForgetLevelChunk` 26.2 wire layout**
   - What we know: it drops a column from the client; the packet id const exists.
   - What's unclear: VarInt(z),VarInt(x) vs a single packed Long for 26.2.
   - Recommendation: jar-decompile `ClientboundForgetLevelChunkPacket` (same method as this research used for movement) during planning/Wave-0; do not guess.

2. **Is `ServerboundPlayerLoaded` required before the server considers the player "in"?**
   - What we know: it is a UNIT (empty) packet the client sends after the world loads (1.21.4+).
   - What's unclear: whether withholding any clientbound packet until it arrives matters for v1.
   - Recommendation: route it as a no-op (or set a `p.loaded` flag) in `dispatch`; do not block streaming on it for v1.

3. **Does the real 26.2 client need `SetDefaultSpawnPosition` and/or `SetTime` to avoid a kick?**
   - What we know: both are cosmetic (compass, day cycle) in principle.
   - What's unclear: client strictness in 26.2.
   - Recommendation: send `SetDefaultSpawnPosition` (capture-diff'd), treat `SetTime` as optional, and let the PLAY-06 real-client walk-around be the final arbiter.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `temp/cache/26.2-inner.jar` | Jar-deriving any remaining layout (ForgetLevelChunk) | ✓ | 26.2 (sha1-verified upstream) | — |
| `temp/cache/26.2-server.jar` | Capture-diff golden (SpawnPos, PlayerInfoUpdate) | ✓ | 26.2, sha1 `823e2250…` | — |
| Java (Zulu/Temurin) 25 | Running the vanilla server for capture-diff | ✓ (per CLAUDE.md / Phase-4 use) | 25 | — |
| `javap` (JDK 25) | Decompiling class files for wire layout | ✓ (used this session) | 25 | — |
| Docker `golang:1.26` | `-race` gate (host is CGO_ENABLED=0) | ✓ (Phase 2-4 used it) | 1.26.1 / Docker 29.4.3 | — |
| Phase-4 capture harness (`temp/chunkcapture/` pattern) | Byte-diffing Phase-5 packets | ✓ (pattern documented in WORLD-CAPTURE-DIFF.md) | — | rebuild from the documented method |

**Missing dependencies with no fallback:** None.
**Missing dependencies with fallback:** None.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (the project standard — see `server/*_test.go`) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./server/ -run 'Movement|PlayerInfo|Recenter|Teleport|Bootstrap' -count=1` |
| Full suite command | `go test ./...` (and Docker `-race` for the tick/movement seam) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PLAY-01 | Login + early-Play tail reach Play | unit (wire layout) | `go test ./server/ -run TestLoginPacketWireLayout` | ✅ (extend for tail) |
| PLAY-02 | PlayerPosition layout + teleport-id validation gates movement | unit | `go test ./server/ -run 'TestPlayerPosition|TestTeleportGate' -count=1` | ⚠️ position ✅ / gate ❌ Wave 0 |
| PLAY-03 | Filled ring radius≥2 + LOAD_START event | integration (pipe) | `go test ./server/ -run TestJoinSequenceOrdering` | ✅ |
| PLAY-04 | 4 movement layouts decode; ring re-centers; far chunks forgotten | unit + pipe | `go test ./server/ -run 'TestMovementDecode|TestRecenterRing' -count=1` | ❌ Wave 0 |
| PLAY-05 | PlayerInfoUpdate self-entry encodes (8-action mask) | unit + capture-diff | `go test ./server/ -run TestPlayerInfoUpdateWire` | ❌ Wave 0 |
| PLAY-06 | Real vanilla 26.2 client walks around | **manual (human-verify)** | real-client visual (PrismLauncher) — the gate, not automatable | n/a |

### Sampling Rate
- **Per task commit:** `go test ./server/ -run '<the task's tests>' -count=1`
- **Per wave merge:** `go test ./...` + Docker `go test -race ./server/...` over the movement/tick seam
- **Phase gate:** Full suite green, capture-diffs byte-identical, AND the PLAY-06 real-client walk-around signed off (same human gate as Phase 4 / NET-04).

### Wave 0 Gaps
- [ ] `server/movement_test.go` — decode each of the 4 `ServerboundMovePlayer*` layouts (flags-byte masking), covers PLAY-04 decode
- [ ] `server/world_stream_test.go` (extend) — `recenterRing` prunes `sentChunks` + emits `ForgetLevelChunk` + re-emits `SetChunkCacheCenter`, covers PLAY-04 re-center
- [ ] `server/play_join_test.go` (extend) — wire-layout tests for PlayerAbilities / SetHeldSlot / PlayerInfoUpdate / SetDefaultSpawnPosition, covers PLAY-01/05
- [ ] teleport-gate test — movement ignored until matching `AcceptTeleportation`, covers PLAY-02
- [ ] capture-diff fixtures for `SetDefaultSpawnPosition` + `PlayerInfoUpdate` (+ `ForgetLevelChunk` after jar-derive), reuse Phase-4 harness

*(Movement routing into the subtick buffer and `TestJoinSequenceOrdering` already exist — Wave 0 is the decode/re-center/new-encoder layer.)*

## Security Domain

`security_enforcement` is not set to `false` in config, so the domain applies. This phase is offline-mode (NET-03 complete) with no auth/session/crypto surface; the relevant ASVS axis is input validation of untrusted client packets, which the existing tick architecture already structures.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Offline mode (v1); online auth is v2/Phase-9 (ONLINE-01) |
| V3 Session Management | no | No web/HTTP session; connection liveness is KeepAlive (TICK-04) |
| V4 Access Control | partial | Teleport-id gate (drop movement until confirmed) is the only access-style control here |
| V5 Input Validation | yes | Decode-with-bounds: every `Scan` error → drop the packet (never panic); subtick buffer is bounded (drop-oldest, cap 256); view ring is server-clamped (`serverViewDistance=2`) regardless of client request |
| V6 Cryptography | no | None in offline Play |

### Known Threat Patterns for proto-776 Play
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Movement-packet flood (position spam) | Denial of Service | Bounded subtick buffer (drop-oldest, `subtickCap`); ring already deduped + server-clamped (`tickChunks` re-walks a bounded ring, issues no duplicate work — T-4-06) |
| Malformed movement (short/garbage payload) | Tampering | `Scan` returns error → `applyInput` returns without mutating state; `dispatch` is total and never panics (T-3-02) |
| Forged teleport confirm (wrong id) | Spoofing | Validate the echoed VarInt against `p.awaitingTeleport`; only then accept movement (PLAY-02) |
| Re-center thrash (jitter across a boundary) | Denial of Service | Re-center only on actual chunk-column change (`newC != p.center`); the ring + worker singleflight bound the work |
| Off-tick state mutation via network goroutine | Tampering/race | All position/center mutation runs on the owner goroutine (TICK-05); `AcceptPlayer` only sends per-conn packets + registers via message |

## Sources

### Primary (HIGH confidence)
- **`temp/cache/26.2-inner.jar`** (decompiled via `javap -p -c` this session) — authoritative 776 wire layouts:
  - `ServerboundMovePlayerPacket` (+ `$Pos/$PosRot/$Rot/$StatusOnly`): trailing packed-flags Byte (`unpackOnGround & 1`, `unpackHorizontalCollision & 2`); per-variant field order
  - `ClientboundPlayerInfoUpdatePacket$Action`: 8-action enum (ADD_PLAYER…UPDATE_HAT); `writeEnumSet` + `writeCollection`; entry starts with `writeUUID`
  - `ClientboundPlayerAbilitiesPacket`: Byte flags (0x01/0x02/0x04/0x08) + Float + Float
  - `ClientboundSetHeldSlotPacket`: `ByteBufCodecs.VAR_INT` (single slot)
  - `ClientboundSetDefaultSpawnPositionPacket` → `LevelData$RespawnData` = `GlobalPos + Float + Float`
  - `ClientboundSetTimePacket` → `WorldClock + ClockNetworkState` composite
  - `ServerboundPlayerLoadedPacket`: `StreamCodec.unit` (empty payload)
- **`server/play_join.go`, `server/world_stream.go`, `server/tick.go`, `server/tick_phases.go`, `server/subtick.go`, `server/gameplay_tick.go`, `world/packet.go`, `data/packetid/packetid.go`** (read this session) — the exact code to extend
- **`server/play_join_test.go`** — existing jar-verified wire-layout tests + the ordering invariant test (the pattern to extend)
- **`.planning/phases/04-world-chunk-system/WORLD-CAPTURE-DIFF.md`** — the reusable capture-diff harness/method

### Secondary (MEDIUM confidence)
- **`.planning/STATE.md` / `04-04-SUMMARY.md`** — the pulled-forward bootstrap provenance, the "EXTEND not duplicate" mandate, the real-client visual gate
- **`.planning/PROJECT.md`** — the proto-769+ Teleport restructure note (DX/DY/DZ velocity, Int32 flags) and the first-playable framing

### Tertiary (LOW confidence — flagged for capture-diff/jar-derive, not relied upon)
- Vanilla join-sequence behavior (which early-Play packets are *strictly* mandatory) — resolved by the PLAY-06 real-client test, not asserted from memory
- `ClientboundForgetLevelChunk` 26.2 field order — explicitly deferred to a jar-derive task

## Metadata

**Confidence breakdown:**
- Standard stack (what to extend): **HIGH** — every file read; the streamer/bootstrap are concrete and tested
- Movement wire layouts (PLAY-04): **HIGH** — all four jar-confirmed this session (the flags-byte shift caught)
- Tab-list / abilities / held-slot (PLAY-05/01): **HIGH** for abilities/held-slot, **MEDIUM** for PlayerInfoUpdate entry sub-encoding (capture-diff to seal)
- Re-center pattern (PLAY-04): **HIGH** — the streamer already keys on `p.center`; the delta is mechanical
- SpawnPos / SetTime / ForgetLevelChunk: **MEDIUM** — shapes jar-confirmed, exact bytes deferred to capture-diff/jar-derive (flagged)
- PLAY-06 gate: **HIGH** that the real-client walk-around is the verifier (same proven gate as Phase 4)

**Research date:** 2026-06-23
**Valid until:** stable for this milestone (proto 776 / MC 26.2). Re-derive on any 26.3 retarget — movement flags and the action set are exactly the fields that drift between versions.
```