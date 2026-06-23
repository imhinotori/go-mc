# Phase 2: Net & Protocol State Machine - Research

**Researched:** 2026-06-23
**Domain:** Driving a vanilla 26.2 (protocol 776) client through Handshake → Status → Login → Configuration → Play over the forked `go-mc` connection framework, with a network/tick channel seam.
**Confidence:** HIGH on framing/login/status/channel-seam (read directly from committed fork code); HIGH on the *shape* of the Configuration sequence; **MEDIUM-HIGH on the exact 776 Registry Data payload** — the protocol *flow* is confirmed, but the registry **content/schema** is the load-bearing risk and is addressed below with a concrete jar-derived source + capture-diff verification.

## Summary

Phase 1 produced more than just generated data: the fork already ships a working **gate** (`server/` package — `handshake.go`, `ping.go`, `login.go`, `configuration.go`, `server.go`) plus a complete, low-level **packet codec** (`net/packet/packet.go`) that already enforces VarInt/VarLong framing, zlib compression with threshold, and the `0x200000` (2²¹) max-data cap. NET-01, NET-02, NET-03, NET-06, and NET-07 are therefore *mostly wiring + assertions* over existing primitives — the heavy lifting (framing, compression negotiation, login-success, disconnect packets) is already in the fork and must NOT be re-implemented. [VERIFIED: read `net/packet/packet.go`, `server/login.go`, `server/handshake.go`, `server/ping.go`]

The real risk is **NET-04 (Configuration → Play with no silent kick)** and it splits into two independent problems. (1) *The sequence*: the current `server/configuration.go::AcceptConfig` is a stub — it sends only Registry Data + Finish Configuration and **omits Select Known Packs, Update Tags, and (correctly) never reads the client's Acknowledge Finish Configuration / Client Information packets**. The full, established vanilla order (stable since 1.20.5, unchanged through 26.2) must be hand-written. (2) *The payload*: the `registry.Registries` struct in `registry/codec.go` is a **client-side decode codec hardcoded to a pre-26.2 registry set** (it has `trim_material`, `wolf_variant`, `painting_variant` as fields and a flat `Dimension` struct). The 26.2 jar's actual `dimension_type`/`worldgen/biome` schemas have **changed shape** (nested `attributes`, `default_clock`, `has_ender_dragon_fight`, `timelines`; biomes moved visual fields into `attributes`). Sending the old struct = a malformed registry the client silently rejects → "Loading terrain…" hang. **The authoritative 26.2 registry content already exists on disk** at `temp/cache/26.2-datagen/generated/data/minecraft/` (datapack JSON: `dimension_type/`, `worldgen/biome/`, `damage_type/`, `chat_type/`) — but `temp/` is **gitignored**, so Phase 2 must extract/embed it. [VERIFIED: read `registry/codec.go`, `server/configuration.go`; inspected the 26.2 datagen JSON on disk; confirmed `temp/` in `.gitignore`]

NET-05 (one writer goroutine + channel-only tick handoff) has a clean home: the fork provides `net/queue` (`ChannelQueue`/`LinkedListQueue` implementing `queue.Queue[pk.Packet]`) and a `PacketQueue = queue.Queue[pk.Packet]` alias in `server/client.go`. The established go-mc/server pattern is one **read goroutine** → decode → push *intents* onto an inbound channel drained by the tick; the tick (or a per-connection write pump) pulls from a per-connection **outbound `PacketQueue`** drained by exactly **one writer goroutine**. Phase 2 builds this seam with a **stub tick consumer** (the real tick is Phase 3) so Phase 3 drops in without rework. [VERIFIED: read `net/queue/queue.go`, `server/client.go`, `server/keepalive.go`]

**Primary recommendation:** Keep `Server.AcceptConn`'s handshake→login dispatch as-is (it already calls `handshake` → `AcceptLogin` → `AcceptConfig` → `AcceptPlayer` and now propagates the config error — the dropped-error bug is fixed). **Rewrite `AcceptConfig`** into the full ordered sequence below, and **replace the registry source**: stop using the stale client-decode `registry.Registries` struct for the server send path; instead embed the 26.2 datapack JSON/NBT for the three mandatory registries (`dimension_type`, `worldgen/biome`, `damage_type`) plus `chat_type`, and send them as network-format NBT. Verify with a **capture-diff against a real vanilla 26.2 server's Configuration packets** — this is the only way to be certain the 776 payload is byte-correct, since no published spec covers 776.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| TCP accept + per-conn goroutine | `net.Listener` / `server.Server.Listen` | — | Already exists; one goroutine per accepted conn (`go s.AcceptConn`). |
| Frame/compress/decompress, packet cap | `net/packet` (`Packet.Pack/UnPack`) | — | Already enforces VarInt length, zlib threshold, `MaxDataLength`. Reuse verbatim. |
| Handshake / Status / Login | `server/` gate (`handshake.go`,`ping.go`,`login.go`) | — | Already implemented; Phase 2 adds the proto-776 assertion + wires a real `ListPingHandler`. |
| Configuration sequence | `server/configuration.go` (REWRITE) | `registry` + embedded datapack NBT | The stub must become the full ordered flow; payload comes from 26.2-jar-derived registry data. |
| Registry Data payload (the risk) | embedded 26.2 datapack NBT (NEW) | `net/packet/NBTField` (network format) | Mandatory registries must carry real 26.2 NBT; the old `registry/codec.go` struct is stale. |
| Inbound intent enqueue | per-conn read goroutine → inbound channel | `net/queue` | Network goroutine decodes only; pushes intents; never touches game state (NET-05). |
| Outbound write | one writer goroutine per conn ← `PacketQueue` | `net/queue` (`ChannelQueue`) | Single-writer invariant; tick/handlers enqueue, writer drains. |
| Tick seam (consumer) | **stub in Phase 2**, real in Phase 3 | channel | Define the channel types now so Phase 3 attaches the loop without API change. |
| Disconnect | `server/` (Login/Config/Play disconnect packets) | `chat.Message` | Distinct packet IDs per state already generated; send readable `chat.Message`. |

## Standard Stack

> No new third-party libraries. Everything is in the fork (`github.com/imhinotori/go-mc`) or stdlib. xsync/ants/conc are explicitly **out of scope** for Phase 2 (Phase 8).

### Core — fork packages and the exact APIs Phase 2 calls
| Package / Symbol | Path | Purpose | Reuse note |
|------------------|------|---------|------------|
| `net.Listener`, `net.ListenMC`, `(Listener).Accept` | `net/conn.go` | TCP listen, accept an MC `net.Conn` (`threshold:-1`) | Already wired by `Server.Listen`. |
| `net.Conn` + `ReadPacket`/`WritePacket`/`SetThreshold`/`SetCipher`/`Close` | `net/conn.go` | Per-connection IO; threshold drives compression | `SetThreshold(t)` flips compression on; `-1`=off. |
| `pk.Packet` + `Pack`/`UnPack`/`Marshal`/`Scan` | `net/packet/packet.go` | Frame, (de)compress, encode/decode fields | **Do not re-implement.** Enforces cap + threshold already. |
| `pk.VarInt`,`pk.VarLong`,`pk.String`,`pk.UUID`,`pk.Identifier`,`pk.Array`,`pk.Option`,`pk.Boolean` | `net/packet/types.go`,`util.go` | Field codecs incl. `Identifier = String`, `Array` = VarInt-prefixed | `pk.Array(&slice)` for length-prefixed lists. |
| `pk.NBTField{V:..., AllowUnknownFields}` | `net/packet/types.go` | **Network-format** NBT (nameless root, post-1.20.2) | `enc.NetworkFormat(true)` already set — correct for Registry Data. |
| `packetid.Clientbound*`/`Serverbound*` (Config/Login/Status) | `data/packetid/packetid.go` | Generated 776 packet IDs | Iota-based per-state; **use the symbols, never literals** (775 reshuffled IDs). |
| `server.Server{ListPingHandler,LoginHandler,ConfigHandler,GamePlay}` | `server/server.go` | The gate composition + `AcceptConn` dispatch | Keep; supply real handler impls. |
| `server.MojangLoginHandler{OnlineMode:false,Threshold:N}` | `server/login.go` | Offline login + compression negotiation + LoginSuccess + reads LoginAck | NET-03 done; set `OnlineMode=false`, `Threshold` ≥0 to compress. |
| `server.PingInfo` / `NewPingInfo` / `ListPingHandler` | `server/ping.go` | Status JSON (version/MOTD/players/favicon) | NET-02 done; implement `Protocol(clientProto)` to return 776. |
| `queue.Queue[pk.Packet]`, `NewChannelQueue`, `NewLinkedQueue`; `PacketQueue` alias | `net/queue/queue.go`, `server/client.go` | Inbound/outbound packet channels | Backbone of the NET-05 single-writer seam. |
| `chat.Message` + `ClearString()` | `chat/` | Disconnect/MOTD text | NET-07 reason payload. |
| `server.KeepAlive` (`NewKeepAlive`, `SendKeepAlive`/`SendDisconnect`) | `server/keepalive.go` | 15s ping / 30s timeout independent timer | **Note for Phase 3 (TICK-04), not required in Phase 2** but available. |

### Generated 776 data Phase 2 depends on
| Artifact | Path | Use |
|----------|------|-----|
| Config-state packet IDs (incl. `SelectKnownPacks`, `UpdateTags`, `UpdateEnabledFeatures`, `FinishConfiguration`, `CodeOfConduct`) | `data/packetid/packetid.go` (lines 43–79) | All Configuration packets; **26.2 added `ClientboundConfigCodeOfConduct` + `ServerboundConfigAcceptCodeOfConduct`** — see Pitfall 6. |
| 26.2 datapack JSON (registry content) | `temp/cache/26.2-datagen/generated/data/minecraft/{dimension_type,worldgen/biome,damage_type,chat_type}/` | **Source of the Registry Data payload** — but `temp/` is gitignored; must be embedded into the repo as part of Phase 2. |
| `registries.json` (canonical registry id-order + entry sets) | `temp/jsons/26.2/registries.json` | Authoritative registry ordering / entry names for the Known-Packs-omitted path. |
| `biomes.json` | `temp/jsons/26.2/biomes.json` | Biome entry set / ids. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Embedding 26.2 datapack NBT for the registry send | Reusing `registry.Registries` struct (`registry/codec.go`) | **Rejected** — that struct is a *client decode* codec with a pre-26.2 field set and a stale `Dimension` schema; it would serialize malformed 26.2 registries. It is fine to keep for the `bot/` client path; it is wrong for the server send path. |
| `NewChannelQueue` (bounded, non-blocking `Push`) for outbound | `NewLinkedQueue` (unbounded, blocking) | Channel queue drops on full (`Push` returns false) — good backpressure signal; linked queue never drops but can balloon memory. Use channel for outbound, decide per backpressure policy. |
| Sending Known Packs + omitting NBT | Sending all registries with full NBT and **no** Known Packs | Both valid. Simplest correct path for Phase 2: send **Clientbound Known Packs `[minecraft:core/<version>]`**, accept the serverbound echo, then send full NBT anyway (omission is an optional bandwidth optimization, not required). Keeps the flow vanilla-shaped without an NBT-omission engine. |

**Installation:** none — `go build ./...` only.

**Version verification:** Framework constants already corrected in Phase 1: `server/server.go` → `ProtocolName="26.2"`, `ProtocolVersion=776` [VERIFIED: read `server/server.go:40-43`]. `MaxDataLength = 0x200000` (2²¹) in `net/packet/packet.go:11` [VERIFIED].

## Architecture Patterns

### System Architecture Diagram

```
            vanilla 26.2 client (proto 776)
                      │  TCP
                      ▼
   net.Listener.Accept ──► go Server.AcceptConn(conn)   [1 goroutine / conn]
                      │
        ┌─────────────┴───────────── state machine (blocking, in-goroutine) ──────────────┐
        │  handshake(conn) ─► (protocol, intention)                                        │
        │     intention==1 ─► acceptListPing ─► [Status] ─► close                          │
        │     intention==2 ─► AcceptLogin ─► [compression negotiated, threshold set]       │
        │                       │  (LoginSuccess; read LoginAcknowledged)                  │
        │                       ▼                                                          │
        │                  AcceptConfig (REWRITE)  ── full ordered Configuration ──┐       │
        │                       │  (see sequence below; ends on AckFinishConfig)   │       │
        │                       ▼                                                  │       │
        │                  AcceptPlayer(name,id,…,conn)  ─► [Play] ────────────────┘       │
        └──────────────────────────────────────────────────────────────────────────────────┘
                                            │  on entering Play (Phase 3+):
                          ┌─────────────────┴──────────────────┐
                          ▼                                     ▼
              READ goroutine (1/conn)                WRITE goroutine (1/conn)  ◄── NET-05
              conn.ReadPacket → decode →              for p := range outQueue.Pull():
              build *intent* → inboundCh ──►          conn.WritePacket(p)
                          │                                     ▲
                          ▼                                     │ enqueue (handlers/tick)
              ┌──────────────────────────────┐                 │
              │  TICK SEAM (channel)          │  drain inbound, mutate game state,
              │  Phase 2: STUB consumer       │──► produce outbound ► push to per-conn outQueue
              │  Phase 3: real tick loop      │
              └──────────────────────────────┘
```

The state-machine portion (handshake→config) runs **synchronously inside the accept goroutine** — there is no tick involvement until Play. The read/write goroutine split + tick seam only matters once a player enters Play; Phase 2 stands up the seam with a stub consumer so Phase 3 attaches the loop.

### Recommended Project Structure (Phase 2 touch points)
```
server/
├── server.go          # keep AcceptConn dispatch; add proto-776 assert in handshake path
├── handshake.go       # add: if protocol != 776 → readable disconnect (NET-01)
├── ping.go            # implement a concrete ListPingHandler returning protocol 776 (NET-02)
├── login.go           # KEEP (offline + compression done) — set Threshold, OnlineMode=false (NET-03)
├── configuration.go   # REWRITE AcceptConfig into the full ordered sequence (NET-04)
├── client.go          # extend: per-conn read+write goroutines, inbound/outbound PacketQueue (NET-05)
└── registrydata/      # NEW: embedded 26.2 registry NBT + send helpers (the payload)
    ├── embed.go        # //go:embed the extracted 26.2 datapack NBT/JSON
    └── send.go         # WriteRegistryData(conn): mandatory registries in network NBT
```

### Pattern 1: The full 26.2 Configuration sequence (NET-04)
**What:** Replace the stub. The established vanilla order (stable 1.20.5 → 26.2):
```
[Client already in Config after LoginAcknowledged]
S→C  (optional) Custom Payload  minecraft:brand
C→S  Client Information         (ServerboundConfigClientInformation)  ── client sends first; READ it
S→C  Select Known Packs         (ClientboundConfigSelectKnownPacks)   [{namespace:"minecraft", id:"core", version:<v>}]
C→S  Select Known Packs         (ServerboundConfigSelectKnownPacks)   ── READ the echoed subset
S→C  Update Enabled Features    (ClientboundConfigUpdateEnabledFeatures)  [minecraft:vanilla]   (Feature Flags)
S→C  Registry Data × N          (ClientboundConfigRegistryData)       ── one packet per registry
S→C  Update Tags                (ClientboundConfigUpdateTags)         ── always full; never pack-sourced
S→C  Finish Configuration       (ClientboundConfigFinishConfiguration)
C→S  Acknowledge Finish Config  (ServerboundConfigFinishConfiguration) ── READ this; THEN go to Play
```
**When to use:** every login. **Key rules:**
- **Known Packs MUST precede Registry Data** (it decides NBT omission). [VERIFIED: minecraft.wiki Registry data]
- **Tags are never sourced from known packs** — always send Update Tags in full (or an empty-but-present set). [VERIFIED: search result]
- The client sends **Client Information** on entering config; the server should read/ignore it but must not block waiting for it in the wrong order — handle inbound packets in a loop, not a rigid read-N.
- The loop ends **only** when `ServerboundConfigFinishConfiguration` (Acknowledge) arrives — the current stub never reads it.

**Example (server side — mirrors the client decode in `bot/configuration.go`):**
```go
// Source: derived from fork bot/configuration.go (the authoritative wire reference)
func (c *Configurations) AcceptConfig(conn *net.Conn) error {
    // 1. Select Known Packs (clientbound) — must precede Registry Data
    corePack := []bot.DataPack{{Namespace: "minecraft", ID: "core", Version: server.ProtocolName}}
    if err := conn.WritePacket(pk.Marshal(
        packetid.ClientboundConfigSelectKnownPacks, pk.Array(corePack))); err != nil {
        return err
    }
    // 2. Drain serverbound config packets until we have Known-Packs echo + Client Information,
    //    answering KeepAlive/Ping inline. (Loop — order from the client is not guaranteed.)
    if err := c.awaitClientConfigReplies(conn); err != nil { return err }

    // 3. Feature Flags
    if err := conn.WritePacket(pk.Marshal(
        packetid.ClientboundConfigUpdateEnabledFeatures,
        pk.Array([]pk.Identifier{"minecraft:vanilla"}))); err != nil { return err }

    // 4. Registry Data — mandatory registries in network NBT (see Pattern 2)
    if err := registrydata.WriteAll(conn); err != nil { return err }

    // 5. Update Tags — always full (empty-but-present is acceptable for v1)
    if err := registrydata.WriteTags(conn); err != nil { return err }

    // 6. Finish + wait for Acknowledge
    if err := conn.WritePacket(pk.Marshal(packetid.ClientboundConfigFinishConfiguration)); err != nil {
        return err
    }
    var p pk.Packet
    if err := conn.ReadPacket(&p); err != nil { return err }
    if packetid.ServerboundPacketID(p.ID) != packetid.ServerboundConfigFinishConfiguration {
        return ConfigFailErr{reason: chat.Text("expected finish-config ack")}
    }
    return nil
}
```

### Pattern 2: Registry Data payload from embedded 26.2 NBT (the risk area)
**What:** Each `ClientboundConfigRegistryData` packet = `Identifier registryId` + `VarInt entryCount` + per entry `Identifier entryKey` + `Boolean hasData` + (network-format NBT). [VERIFIED: `registry/network.go::WriteTo` already implements exactly this shape; reuse the shape, change the *content source*.]
**Source of truth:** the 26.2 datapack JSON under `temp/cache/26.2-datagen/generated/data/minecraft/` — convert to NBT (network format) and embed. Mandatory: `dimension_type` (≥1 entry, used `overworld`), `worldgen/biome` (must include `minecraft:plains`), `damage_type` (full set — 51 entries in 26.2). Recommended: `chat_type`.
**Why not the struct:** `registry/codec.go`'s `Dimension` struct is missing 26.2 fields (`attributes`, `default_clock`, `has_ender_dragon_fight`, `timelines`) and contains removed ones — serializing it yields a registry the client rejects.
**Example (reusing the existing per-registry frame):**
```go
// The frame is correct; only the data must be real 26.2 content.
// Source: registry/network.go Registry[E].WriteTo (already implements id+count+entries+NBT)
for _, reg := range mandatoryRegistries { // dimension_type, worldgen/biome, damage_type, chat_type
    conn.WritePacket(pk.Marshal(
        packetid.ClientboundConfigRegistryData,
        pk.Identifier(reg.ID),
        reg, // a FieldEncoder writing VarInt(count) + [key, hasData, NBTField{network}] per entry
    ))
}
```

### Pattern 3: One-writer-goroutine + channel-to-tick seam (NET-05)
**What:** After Play begins, split IO into a **read goroutine** (decode → push intent to `inbound chan`) and a **single write goroutine** (pull from a per-conn `outbound PacketQueue` → `conn.WritePacket`). Game state is mutated only by the (stub, then real) tick consumer.
**Shape the seam for Phase 3:**
```go
// Source: fork net/queue + server/client.go (PacketQueue alias)
type Client struct {
    conn     *net.Conn
    outbound queue.Queue[pk.Packet]   // NewChannelQueue(N): exactly one writer drains this
}
type Intent struct {                  // what the read goroutine produces
    Client *Client
    Packet pk.Packet                  // raw; decoded/dispatched by the tick, not here
}
// inboundCh is OWNED by the tick. Phase 2: a stub goroutine drains and discards.
// Phase 3: the tick loop's "drain inbound" phase consumes it. Same channel type → no rework.
func (c *Client) writeLoop() {        // the ONE writer
    for { p, ok := c.outbound.Pull(); if !ok { return }; _ = c.conn.WritePacket(p) }
}
func (c *Client) readLoop(inbound chan<- Intent) {
    for { var p pk.Packet; if err := c.conn.ReadPacket(&p); err != nil { return }
          inbound <- Intent{Client: c, Packet: p} } // NEVER touches game state here
}
```
**Invariant to enforce in review:** no game-state pointer is reachable from `readLoop`/`writeLoop`; they only move bytes and `Intent`s. This is the cheap-single-threaded version of TICK-05 ownership and must hold from day one.

### Anti-Patterns to Avoid
- **Rigid "read exactly N packets" config loop.** The client interleaves Client Information / KeepAlive / Pong unpredictably — loop and dispatch by packet ID (mirror `bot/configuration.go`), don't hard-sequence reads.
- **Serializing `registry.Registries` for the server send path.** Stale schema → silent kick. Embed real 26.2 NBT.
- **Packet ID literals.** 775 reshuffled IDs; always reference `packetid.*` symbols.
- **Game-state access from network goroutines.** Violates NET-05/TICK-05; enqueue intents instead.
- **Swallowing config-state errors.** The exact bug already fixed in `server.go` — keep the error propagated and send a Config Disconnect so failures are visible, never silent.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Packet framing (VarInt length prefix) | A custom length reader/writer | `pk.Packet.Pack/UnPack` (`net/packet/packet.go`) | Already correct; handles `MaxDataLength` cap. |
| Compression threshold + zlib | A zlib wrapper | `Pack(w, threshold)` / `UnPack(r, threshold)` + `conn.SetThreshold` | Threshold semantics (0=all, -1=off, uncompressed-mark `DataLength=0`) already implemented and pooled. |
| 2²¹ max packet cap | A manual bounds check | `MaxDataLength = 0x200000` checks in `unpack*` | Both compressed/uncompressed paths already guard it. |
| VarInt/VarLong codec | A varint loop | `pk.VarInt`,`pk.VarLong` (`MaxVarIntLen=5`,`MaxVarLongLen=10`) | Done + length helpers used by the packer. |
| Network-format NBT (nameless root) | A second NBT encoder | `pk.NBTField` (`enc.NetworkFormat(true)`) | The post-1.20.2 root-name-less format is already the default here. |
| Length-prefixed arrays / optionals | Manual loops | `pk.Array`, `pk.Option`, `pk.Opt` | VarInt-prefixed by default (`Ary[VarInt]`). |
| Offline UUID | A namespace hash | `offline.NameToUUID(name)` (called in `login.go`) | Already wired in `AcceptLogin`. |
| LoginSuccess + compression handshake | Re-do login | `MojangLoginHandler.AcceptLogin` | Sends compression, LoginSuccess, reads LoginAck. Just set `Threshold`. |
| Status JSON | Hand-build JSON | `server.PingInfo` + `listResp` | Version/MOTD/sample/favicon already marshaled. |
| Per-conn packet queue | A custom channel wrapper | `queue.Queue[pk.Packet]` / `PacketQueue` | Channel + linked impls already provided. |
| KeepAlive timer (Phase 3) | A ticker | `server.KeepAlive` | 15s/30s independent timer ready for TICK-04. |

**Key insight:** Phase 2 is ~80% *assembly* of existing fork primitives. The only genuinely new code is (a) the rewritten Configuration sequence, (b) the embedded 26.2 registry NBT + its send helper, and (c) the read/write goroutine + channel seam. Everything else is calling APIs that already exist and are already correct for 776 framing.

## Runtime State Inventory

> Phase 2 is greenfield server logic over generated data — no rename/migration. The one "stored state" concern is the **registry data source**, captured here because it is not yet committed.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — no datastore in Phase 2. | None. |
| Live service config | None. | None. |
| OS-registered state | None. | None. |
| Secrets/env vars | None (offline mode; no auth keys; `MojangLoginHandler` generates an RSA key only if `OnlineMode`). | None. |
| Build artifacts / uncommitted source | **26.2 registry datapack JSON lives only in gitignored `temp/cache/26.2-datagen/generated/data/minecraft/`** (`.gitignore` line `temp/`). It is the source of the Registry Data payload and is NOT in the repo. | **Phase 2 must extract + embed** the needed registries (`dimension_type`, `worldgen/biome`, `damage_type`, `chat_type`) into a committed package (e.g. `server/registrydata/`) via `//go:embed`, or regenerate them deterministically. Do not depend on `temp/` at runtime. |

## Common Pitfalls

### Pitfall 1: Stale registry schema → silent "Loading terrain…" hang (THE big one)
**What goes wrong:** Server sends `dimension_type`/`biome` built from `registry/codec.go`'s pre-26.2 structs; client validates registries at Finish Configuration, finds a malformed/incomplete `dimension_type`, and silently refuses to enter Play (no disconnect text — just hangs at "Loading terrain").
**Why it happens:** 26.2 changed the dimension_type and biome schemas (nested `attributes`, `default_clock`, `has_ender_dragon_fight`, `timelines`; biome visual fields moved into `attributes`). The committed struct predates this. [VERIFIED: compared `registry/codec.go::Dimension` against 26.2 `dimension_type/overworld.json` on disk]
**How to avoid:** Embed the real 26.2 datapack NBT (from the datagen JSON) instead of serializing the struct. Include `minecraft:plains` in `worldgen/biome` and ≥1 `dimension_type`.
**Warning signs:** Client reaches Play state per server logs but shows "Loading terrain…" indefinitely; no Disconnect sent.
**Verification:** capture-diff (see Pitfall 7).

### Pitfall 2: Known Packs sent after Registry Data (or omitted) → wrong NBT-omission
**What goes wrong:** If Registry Data precedes Known Packs, the omission negotiation is undefined; if you *omit* NBT without the client having acked `minecraft:core`, entries arrive empty and the client kicks on the first reference.
**Why it happens:** The spec requires Known Packs **before** Registry Data; omission is only legal for packs both peers know. [VERIFIED: minecraft.wiki Registry data]
**How to avoid:** Send Clientbound Known Packs `[{minecraft, core, <version>}]` first, read the serverbound echo, and for v1 **send full NBT regardless** (omission is optional). Never omit unless the echo confirmed the pack.
**Warning signs:** Kick referencing a missing biome/dimension entry.

### Pitfall 3: Config loop never reads Acknowledge Finish Configuration
**What goes wrong:** Server sends Finish Configuration and immediately calls `AcceptPlayer`; it then tries to send Play packets while the client is still finishing config → desync/kick.
**Why it happens:** The current stub returns right after Finish without reading `ServerboundConfigFinishConfiguration`.
**How to avoid:** Block on reading the Acknowledge packet before transitioning to Play (Pattern 1, step 6).
**Warning signs:** First Play packet rejected; immediate disconnect after config.

### Pitfall 4: Update Tags omitted → client kick on tag reference
**What goes wrong:** Tags are never pack-sourced; if Update Tags is skipped, the client may kick when a tag is referenced.
**How to avoid:** Always send Update Tags. For v1, an empty-but-present tag set (count 0) is the minimal safe send; expand if the client complains.
**Warning signs:** Disconnect mentioning a tag (`#minecraft:...`).

### Pitfall 5: Compression threshold mismatch
**What goes wrong:** Set compression (`SetThreshold`) but a packet ≥ threshold is sent uncompressed, or threshold negotiated on one side only.
**Why it happens:** Compression must be enabled on the conn (`SetThreshold(t)`) **immediately after** sending Set Compression and **before** LoginSuccess — `MojangLoginHandler.AcceptLogin` already does this in the right order. Re-implementing it elsewhere risks ordering bugs.
**How to avoid:** Use `MojangLoginHandler` as-is; set `Threshold` (e.g. 256). Confirm `Pack`/`UnPack` both receive the same threshold (the `Conn.threshold` field guarantees this).
**Warning signs:** Garbled packets right after Set Compression; zlib errors.

### Pitfall 6: New 26.2 Code of Conduct packet ignored
**What goes wrong:** 26.2 added `ClientboundConfigCodeOfConduct` / `ServerboundConfigAcceptCodeOfConduct` (present in generated `packetid.go`). A vanilla client does not *require* the server to send it, but if a future flow expects the accept, an unhandled packet ID in the config loop must not crash. [VERIFIED: `data/packetid/packetid.go:64,78`]
**How to avoid:** In the config read loop, treat unknown/unhandled config packet IDs as no-ops (log + continue), exactly like `bot/configuration.go`'s `default` behavior. Do **not** send Code of Conduct for v1 (offline, no CoC).
**Warning signs:** Panic/return on an unexpected config packet ID.

### Pitfall 7: Trusting any published spec for 776 (it doesn't exist)
**What goes wrong:** The wiki documents ≤773; 776 byte layouts are unverified by any public source.
**Why it happens:** 776/26.2 post-dates the wiki, wiki.vg, and training data (per STATE.md blocker).
**How to avoid — the prescribed verification:** Run a **real vanilla 26.2 server** (the official jar, same one Phase 1 extracted, sha1 `823e2250…`) and a packet capture (e.g. a logging proxy / `tcpdump` + a decoder, or point the fork's `bot/` client at vanilla and dump every config packet). **Diff Ender's Configuration byte stream against vanilla's** for the same client. Any divergence in registry order, entry count, NBT shape, or packet IDs is the bug. This capture-diff is the *only* authoritative correctness check for NET-04 and should be a required verification step in the plan.

### Pitfall 8: Game-state access leaking into network goroutines
**What goes wrong:** A handler in the read goroutine touches world/entity state → data race the moment Phase 3's tick owns that state.
**How to avoid:** Read goroutine produces `Intent`s only; all mutation happens in the (stub→real) tick consumer. Add a `go test -race` smoke test on the read/write/seam even in Phase 2.
**Warning signs:** `-race` failures appear in Phase 3, not Phase 2 (too late). Enforce now.

## Code Examples

### Proto-776 assertion in handshake (NET-01)
```go
// server/handshake.go — extend the existing handshake to assert protocol
protocol, intention, err := s.handshake(conn)
if err != nil { return }
if intention == 2 && protocol != server.ProtocolVersion { // 776
    // we are past handshake but before login-state disconnect is available;
    // send a Login Disconnect with a readable reason (NET-01 + NET-07)
    _ = conn.WritePacket(pk.Marshal(
        packetid.ClientboundLoginLoginDisconnect,
        chat.Text("Unsupported protocol: server is 26.2 (776)"),
    ))
    return
}
```
> Note: Status (intention 1) must answer regardless of client protocol so the server list shows a version mismatch nicely; only gate login.

### Status handler returning 776 (NET-02)
```go
// implement ListPingHandler.Protocol to advertise 776 so the client sees a clean version label
func (h *enderPing) Protocol(clientProtocol int32) int { return server.ProtocolVersion } // 776
```

### Disconnect with readable reason, per state (NET-07)
```go
// Each state has its own disconnect packet id (all generated):
//   Login:  packetid.ClientboundLoginLoginDisconnect
//   Config: packetid.ClientboundConfigDisconnect
//   Play:   packetid.ClientboundDisconnect
// server.go already maps LoginFailErr/ConfigFailErr → the right disconnect packet. Reuse that.
return ConfigFailErr{reason: chat.Text("registry validation failed")}
```

### Wiring the Server (assembly)
```go
srv := &server.Server{
    Logger:          log.Default(),
    ListPingHandler: &enderPing{},                                  // NET-02 (Protocol→776)
    LoginHandler:    &server.MojangLoginHandler{OnlineMode:false, Threshold:256}, // NET-03
    ConfigHandler:   &Configurations{ /* embedded 26.2 registry data */ },        // NET-04 (rewritten)
    GamePlay:        &enderGamePlay{ /* read/write goroutines + tick seam */ },   // NET-05 (stub tick)
}
log.Fatal(srv.Listen(":25565"))
```

## State of the Art

| Old Approach | Current (26.2) Approach | When Changed | Impact |
|--------------|-------------------------|--------------|--------|
| Single big "Login (play)" with embedded dimension codec NBT | Separate **Configuration** state with per-registry Registry Data | 1.20.2 | Phase 2 must implement the config state machine, not a codec blob. |
| Registry NBT named-root | **Network-format NBT (nameless root)** | 1.20.2 | `pk.NBTField` already sets `NetworkFormat(true)`. |
| Send all registry NBT always | **Known Packs negotiation** to omit NBT for shared packs | 1.20.5 | Send Known Packs first; omission optional (v1: send full). |
| Item slots carry NBT | Component-based slots (no NBT) | 1.20.5 | Not Phase 2, but noted (ENT-04/Phase 6). |
| Flat `dimension_type` fields | Nested `attributes` + `default_clock`/`has_ender_dragon_fight`/`timelines` | 26.x | **`registry/codec.go::Dimension` is stale** — embed real NBT. |
| (new) | `ConfigCodeOfConduct` / `AcceptCodeOfConduct` config packets | 26.x | Handle gracefully as no-op; don't send for v1. |

**Deprecated/outdated for the server send path:**
- `registry.Registries` struct + `NewNetworkCodec()` in `registry/codec.go` — keep for the `bot/` client decode; **do not** use to serialize 26.2 server registries.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The vanilla config order (Known Packs → Feature Flags → Registry Data → Update Tags → Finish) is unchanged from 1.20.5 through 26.2/776. | Pattern 1 | Wrong order → silent kick. **Mitigated by the capture-diff (Pitfall 7), which is a required verification.** |
| A2 | Sending full registry NBT *without* exercising NBT omission is accepted by the 26.2 client as long as Known Packs was sent first. | Alternatives / Pitfall 2 | If 26.2 *requires* omission for `minecraft:core`, full-send could be rejected — capture-diff catches it. |
| A3 | The three mandatory registries (`dimension_type`, `worldgen/biome` incl. `plains`, `damage_type`) suffice for the client to enter Play; other registries optional. | Pattern 2 | A 26.2-new mandatory registry would also be needed — capture-diff against vanilla reveals which registries vanilla actually sends. |
| A4 | An empty-but-present Update Tags is accepted for v1. | Pitfall 4 | If a tag reference occurs before Play, may need real tag data — capture-diff reveals the minimal tag set vanilla sends. |
| A5 | `Threshold:256` is a safe compression threshold (vanilla default). | Pitfall 5 | Non-fatal; any ≥0 value works; cosmetic/bandwidth only. |
| A6 | The 26.2 datagen JSON in `temp/cache/.../data/minecraft/` is the exact content vanilla sends as Registry Data (datapack form ≈ wire form, modulo JSON→NBT). | Pattern 2 / Runtime State | If the wire form differs from the datapack JSON (field renames in NBT), capture-diff against vanilla is the corrective source. |

## Open Questions

1. **Exact set of registries vanilla 26.2 sends in Registry Data (and their order).**
   - What we know: 3 are mandatory; order = registry-id order from `registries.json`; Known Packs precedes them.
   - What's unclear: whether 26.2 added a newly-mandatory registry, and whether vanilla omits NBT for `minecraft:core`.
   - Recommendation: **Capture-diff against a real vanilla 26.2 server** (Pitfall 7) — make it a required plan verification, and source the registry list/order from `temp/jsons/26.2/registries.json`.

2. **Whether Update Tags must be non-empty for the v1 mandatory registries.**
   - What we know: tags are always server-sent, never pack-sourced.
   - What's unclear: whether the client kicks on an empty tag set before any tag reference in config.
   - Recommendation: start empty-but-present; if the capture-diff shows vanilla sends specific tags in config, mirror that minimal set.

3. **Backpressure policy for the outbound `PacketQueue`.**
   - What we know: `ChannelQueue.Push` drops when full; `LinkedListQueue` is unbounded.
   - What's unclear: desired behavior under a slow client (drop vs. block vs. disconnect).
   - Recommendation: bounded channel + disconnect-on-full for Phase 2 (safe default); revisit in Phase 3 when the tick owns flush timing.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | build/run | ✓ | 1.26.1 (module floor `go 1.22`) | — |
| Fork `imhinotori/go-mc` (this repo) | all of Phase 2 | ✓ | branch `ender-776` | — |
| Generated 776 packet IDs | NET-01..07 | ✓ | committed `data/packetid/` | — |
| 26.2 registry datapack JSON | NET-04 payload | ⚠ present but **gitignored** (`temp/`) | 26.2 datagen | Re-run `cd tools && go run . --version 26.2` to regenerate; then embed |
| Official vanilla 26.2 server jar | capture-diff verification (Pitfall 7) | ✓ (cached `temp/cache/26.2-server.jar`, sha1 `823e2250…`) | 26.2 | Re-download from Mojang manifest |
| JDK 25 (to run vanilla server for capture) | capture-diff | ✓ (Zulu 25, per PROJECT.md) | 25 | — |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** the registry NBT content (regenerate via codegen if `temp/` is cleared) — must be embedded into the repo regardless, since runtime must not read `temp/`.

## Validation Architecture

> `.planning/config.json` was not present/inspected for an explicit `nyquist_validation:false`; treating validation as enabled.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` (+ `go test -race`) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./server/... ./net/... -run TestConfig -count=1` |
| Full suite command | `go test ./... -race -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| NET-01 | Handshake asserts proto 776; non-776 login gets readable disconnect | unit | `go test ./server/ -run TestHandshakeProtocol -x` | ❌ Wave 0 |
| NET-02 | Status returns version/MOTD/players, protocol 776 | unit | `go test ./server/ -run TestStatusPing -x` | ❌ Wave 0 |
| NET-03 | Offline login completes; compression negotiated; LoginAck read | unit/integration | `go test ./server/ -run TestOfflineLogin -x` | ❌ Wave 0 |
| NET-04 | Full Configuration sequence reaches Play (no silent kick) | integration + **capture-diff** | `go test ./server/ -run TestConfigSequence -x` + manual vanilla capture-diff | ❌ Wave 0 |
| NET-05 | One writer goroutine; intents via channel; no game-state access in net goroutines | unit + `-race` | `go test ./server/ -run TestWriterSingle -race -x` | ❌ Wave 0 |
| NET-06 | VarInt/VarLong framing, threshold, 2²¹ cap | unit (mostly exists in `net/packet`) | `go test ./net/packet/ -count=1` | ✅ (`packet_test.go`, `types_test.go`) — extend for cap/threshold edges |
| NET-07 | Disconnect packet per state carries readable reason | unit | `go test ./server/ -run TestDisconnectReason -x` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go build ./... && go test ./server/... ./net/... -count=1`
- **Per wave merge:** `go test ./... -race -count=1`
- **Phase gate:** full suite green **and** the manual vanilla-26.2 capture-diff for NET-04 passes before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `server/handshake_test.go` — NET-01 protocol assertion + disconnect
- [ ] `server/ping_test.go` — NET-02 status JSON / protocol 776
- [ ] `server/login_test.go` — NET-03 offline login + compression (use `bot/` client or an in-memory pipe conn)
- [ ] `server/configuration_test.go` — NET-04 full sequence (drive with the fork's `bot/configuration.go` client over an in-memory `net.Pipe`)
- [ ] `server/client_test.go` — NET-05 single-writer + `-race` seam test
- [ ] `server/registrydata/` embed + `registrydata_test.go` — NBT round-trips and contains `plains`/`overworld`
- [ ] Test harness: an **in-memory `net.Conn` pipe** pairing the fork's `bot` client with the server gate (no real socket) — drives NET-01..04 end-to-end in unit tests; the fork's `bot/configuration.go` is the ready-made client side.

*Existing `net/packet/packet_test.go` + `types_test.go` already cover much of NET-06; extend with explicit `MaxDataLength` overflow and threshold-boundary cases.*

## Sources

### Primary (HIGH confidence) — committed fork code read this session
- `net/packet/packet.go` — framing, zlib threshold, `MaxDataLength=0x200000` cap (NET-06)
- `net/packet/types.go` — VarInt/VarLong, `NBTField` network format, field codecs
- `net/conn.go`, `net/interface.go` — `Conn`, `SetThreshold`, `SetCipher`, Listen/Accept
- `net/queue/queue.go`, `server/client.go` — `PacketQueue`, channel/linked queues (NET-05)
- `server/server.go` — `AcceptConn` dispatch, proto constants (776/"26.2"), disconnect mapping
- `server/handshake.go`, `server/ping.go`, `server/login.go` — gate (NET-01/02/03)
- `server/configuration.go` — the **stub** `AcceptConfig` to rewrite (NET-04)
- `registry/codec.go`, `registry/network.go`, `registry/registry.go` — registry frame (reusable) + **stale 26.2 schema** (risk)
- `bot/configuration.go` — **authoritative wire reference** for the full Configuration sequence (client side)
- `data/packetid/packetid.go` — generated 776 config/login/status packet IDs (incl. 26.2 CodeOfConduct)
- 26.2 datagen JSON on disk: `temp/cache/26.2-datagen/generated/data/minecraft/{dimension_type,worldgen/biome,damage_type,chat_type}/`, `temp/jsons/26.2/registries.json` — registry content/order source
- `.planning/phases/01-foundation-fork-codegen/774-to-776-DIFF.md` + `01-RESEARCH.md` — Phase 1 outcomes, +5 packet delta, jar sha1

### Secondary (MEDIUM confidence) — official docs, ≤773
- [minecraft.wiki — Registry data](https://minecraft.wiki/w/Java_Edition_protocol/Registry_data) — mandatory registries (`dimension_type`/`worldgen/biome`(plains)/`damage_type`), missing-entry kick behavior, network-NBT
- [minecraft.wiki — Packets](https://minecraft.wiki/w/Java_Edition_protocol/Packets) — Configuration packet set/order (documents 773)
- WebSearch (minecraft.wiki, Fabric yarn `ConfigPackets`) — Known Packs precede Registry Data; tags never pack-sourced; `minecraft:core` only known pack

### Tertiary (LOW confidence) — must be capture-diff verified for 776
- Exact 776 registry set/order, NBT-omission requirement, minimal tag set — **no published source covers 776**; verify by capturing a real vanilla 26.2 server (Pitfall 7 / Open Question 1).

## Metadata

**Confidence breakdown:**
- Framing/compression/cap (NET-06): **HIGH** — read the implementation; tests exist.
- Handshake/Status/Login (NET-01/02/03): **HIGH** — implemented in fork; Phase 2 = wiring + assertions.
- Configuration *flow* (NET-04 sequence): **HIGH** — confirmed by `bot/configuration.go` + wiki + cross-search; order stable since 1.20.5.
- Configuration *payload* (NET-04 registry content for 776): **MEDIUM-HIGH** — authoritative source on disk (26.2 datagen JSON) + stale-struct risk identified; final byte-correctness requires capture-diff (prescribed).
- Channel/single-writer seam (NET-05): **HIGH** — `net/queue` + `PacketQueue` + established go-mc/server pattern; seam shaped for Phase 3.
- Disconnect (NET-07): **HIGH** — per-state disconnect packet IDs generated; `server.go` already maps them.

**Research date:** 2026-06-23
**Valid until:** ~2026-07-23 for the fork-code facts (stable, in-repo); the 776 registry-payload specifics are validated at execution time by capture-diff, not by a dated external source.
