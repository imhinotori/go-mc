# Feature Research

**Domain:** Minecraft Java Edition server core (vanilla-faithful, 26.2 / protocol 776, Go, no plugins)
**Researched:** 2026-06-23
**Confidence:** HIGH (protocol sequence verified against minecraft.wiki; gameplay surface from established vanilla behavior)

> Framing: this is not a "user-facing feature" product. The "user" is an **unmodified vanilla 26.2 client**, and "table stakes" means *the client disconnects, hangs, or renders nothing without it*. The dominant axis is **protocol-state ordering** — what bytes the client needs first, and what causes a kick if missing or out of order. Features are ordered by how early in the connection lifecycle the client demands them.

---

## The connection lifecycle (the spine everything hangs off)

Four protocol states, entered in strict order. State transitions are themselves table stakes — a wrong transition is an instant kick.

```
HANDSHAKING ──(intent=1)──> STATUS ──> [disconnect]      (server list ping)
            └─(intent=2)──> LOGIN ──> CONFIGURATION ──> PLAY
```

- **Handshaking**: single serverbound packet carrying protocol version (776) + next-state intent (1=status, 2=login/transfer). Trivial but gatekeeps everything.
- **Status**: server list ping. Two packets: Status Response (JSON: version name+protocol, players, MOTD, favicon) and Ping/Pong. Required for the server to *appear online* in the multiplayer list, but NOT required to play (a client connecting directly skips it). Easy win, do early.
- **Login**: Login Start → (offline) Login Success → client Login Acknowledged. Online-mode inserts Encryption Request/Response + Mojang session auth + AES encryption. Compression (Set Compression threshold) is negotiated here. **Offline-mode first** per PROJECT scope removes encryption/auth from the critical path.
- **Configuration** (mandatory since 1.20.2 — jumping Login→Play is an instant kick): registry/data sync happens here. See sequence below.
- **Play**: the actual game. Join sequence below.

---

## Feature Landscape

### Table Stakes (client breaks / disconnects without these)

Ordered roughly by when the client first needs them.

| Feature | Why the client needs it | Complexity | Notes |
|---------|------------------------|------------|-------|
| **VarInt/VarLong + packet framing** | Every packet is length-prefixed VarInt; mis-frame = desync = kick | LOW | go-mc `net` provides this |
| **Handshake parse + state machine** | Routes connection to status/login; wrong state = kick | LOW | 1 packet; gates everything |
| **Status / server list ping** | Server shows "online" with MOTD in multiplayer list | LOW | Status Response JSON + Ping/Pong; not needed to *play* but trivial and validates codec end-to-end |
| **Login (offline)** | Without Login Success the client never leaves login | LOW–MED | Login Start → Login Success(UUID,name) → client Login Ack. Offline UUID = `OfflinePlayer:<name>` MD5 |
| **Compression negotiation** | Once threshold set, both sides MUST use zlib-framed packets or desync | MED | Set Compression in login; -1 disables. Get this exactly right or everything after breaks |
| **Configuration state sequence** | Login→Play directly is rejected by all 1.20.2+ clients | MED | Must traverse config and finish it cleanly |
| **Clientbound Known Packs + read Serverbound Known Packs** | Notchian server *waits* for client's Known Packs response before continuing config; client uses known packs to source registry NBT it didn't receive inline | MED | Send `[minecraft:core / <version>]`; **read and discard** the client's reply before proceeding |
| **Registry Data packets** | Client validates registries at Finish Configuration; missing/garbage registries = kick. Covers dimension_type, worldgen/biome, wolf/cat/etc. variants, damage_type, painting_variant, banner_pattern, trim_*, chat_type, etc. | HIGH | One packet per registry. Codegen from jar (PR #294-296) supplies the data. Most error-prone single area for first connect |
| **Update Tags (config)** | Tags must be sent in full (never sourced from known packs); block/item/fluid tags | MED | Client expects tag data; gameplay logic (mineable, etc.) depends on it later |
| **Feature Flags** | Enables `minecraft:vanilla` (and others) feature set; without it some content is gated | LOW | Send `minecraft:vanilla` |
| **Finish Configuration handshake** | Server sends Finish Configuration → client replies Acknowledge → state flips to Play | LOW | Do NOT send any Play packet before the acknowledge arrives |
| **Login (play) / "Join Game"** | First Play packet; carries entity ID, gamemode, dimension list, dimension type, world name, view distance, hashed seed, etc. Wrong dimension reference = kick | MED | References registry entries sent in config — they must match |
| **Synchronize Player Position** | Client stays in a loading/limbo state until it receives a position to teleport to and ACKs it (Confirm Teleportation) | MED | **Proto 769+ restructured**: TeleportID first, then X/Y/Z, then DX/DY/DZ velocity, int32 relative-flags. Client sends Confirm Teleportation back — server must honor the ID |
| **Set Default Spawn Position** | Establishes compass target / respawn point | LOW | Cheap; client expects it |
| **Set Center Chunk** | Tells client which chunk is the loading origin; chunks outside the window around center are ignored | LOW | Must be set before/with chunk sends or chunks are dropped silently |
| **Chunk Data and Update Light** | **The world is invisible without this.** Client will not render a chunk lacking loaded neighbors, so you must send a full square (radius ≥ 2–3) around the player before they see anything but void | HIGH | Paletted containers (single/indirect/direct), BitStorage packed into longs with no cross-long spanning, biomes (4×4×4), heightmaps as typed long arrays, light masks + arrays. The single biggest correctness sink |
| **Block states / global palette** | Chunk encoding indexes the global block-state palette (29,671 states for 26.2); wrong IDs = wrong/garbled blocks | HIGH | Codegen from jar gives the authoritative state IDs |
| **"Start waiting for level chunks" Game Event** | Signals the client to begin rendering the received chunks / dismiss the loading screen | LOW | Game Event id for level-chunk-load; client may hang on loading terrain without it |
| **Keep Alive (both directions)** | Client is kicked if no Keep Alive within timeout (~15–30s); server must also expect the client's response and kick on timeout | LOW | Send periodically, match IDs. Simple but non-optional — forgetting it = mysterious disconnects ~20s in |
| **Player Info Update (Add Player)** | Without an entry the player has no tab-list/skin identity; required for self and other players to render properly | MED | Add Player action + later Update Listed/latency |
| **Spawn self + chunk-gated render** | Combined effect of position + chunks + game event = player drops into a solid, visible world | — | This is the "first playable" finish line |
| **Player movement handling** | Client streams Set Player Position/Rotation; server must accept, validate loosely, and update center chunk + stream new chunks as the player walks | MED | Anti-cheat-grade validation is NOT table stakes; *accepting* movement is |
| **View-distance chunk streaming** | As player moves, load/send entering chunks and unload exiting ones, updating Set Center Chunk | MED–HIGH | Naive version is fine for v1; this is where perf work later lives |
| **Disconnect packet** | Graceful kicks (login + play variants) with a chat-component reason | LOW | Needed for clean error UX instead of raw socket close |
| **System Chat / message plumbing** | Client expects chat to function; even a stub keeps the experience non-broken | LOW–MED | Full signed-chat (PlayerChat with `globalIndex` VarInt prefix) is more involved |

### Table Stakes — second tier (needed for "play," not just "connect and stand")

| Feature | Why expected | Complexity | Notes |
|---------|--------------|------------|-------|
| **Authoritative tick loop (20 TPS)** | Drives time, entities, physics, scheduled chunk sends; without it the world is frozen | MED | The heartbeat of the server. Everything gameplay hangs off it |
| **Block break / place** | Player Action (digging) + Use Item On (placement); server applies, then Block Update / Block Changed Ack to confirm | MED | Client predicts then reconciles with server's Block Update; mismatch = blocks "flicker back" |
| **Inventory / window handling** | Click Container, Set Container Content/Slot, held-item slot; component-based slots (post-1.20.5, NO NBT in slots) | HIGH | Even creative-mode item placement uses Creative Inventory Action. Component item format is a known 26.2 shift |
| **Entity spawn + tracking + movement** | Other players/mobs appear and move via Spawn Entity + Update Entity Position(/Rotation) + Teleport Entity, gated by view range | HIGH | Async entity tracker is the *optimized* version; the *correct* version comes first |
| **Player list / multiplayer visibility** | Players seeing each other is core to "multiplayer" | MED | Player Info Update + entity spawn for each remote player |
| **World persistence (region/Anvil)** | Without save/load the world resets every restart; go-mc `save`/`region`/`level` provides the format | MED | Read existing worlds + write modified chunks. Linear region format is the *optimized* variant (differentiator) |
| **World generation (deterministic minimum)** | Need *some* terrain to send; a flat/noise generator unblocks first playable. Vanilla-parity is a stretch goal | MED–HIGH | go-mc does NOT provide worldgen. A superflat-style generator is the pragmatic v1 |
| **Command dispatch + chat commands** | Players expect `/` commands; Brigadier graph (Commands packet) tells client about completions | MED–HIGH | Sending an empty/minimal command graph is fine for v1; full Brigadier sync later |
| **Time / day-night (Update Time)** | Expected ambiance; cheap to send each tick interval | LOW | |
| **Health / food / respawn** | Without these the player can't die/respawn correctly; Set Health, Respawn, Combat Death | MED | Needed for a "playable" loop beyond walking around |

### Differentiators (the Leaf-style edge — the *reason this project exists*)

These are explicitly **layered after** vanilla correctness (per PROJECT key decision: "Build vanilla logic first, Leaf optimizations last"). You cannot async-optimize logic that does not yet exist.

| Feature | Value proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| **No-JVM Go runtime** | Lower memory baseline, fast startup, single static binary, no GC-pause tuning hell | — | The structural differentiator vs Paper/Leaf themselves |
| **Async pathfinding** | Mob pathfinding off the main tick thread; biggest TPS sink in vanilla under mob load | HIGH | Requires entity/AI + nav mesh to exist first |
| **Async entity tracker** | Compute who-sees-what / movement packets off-thread; scales with entity count | HIGH | Needs the synchronous tracker working first as the reference |
| **Async mob spawning** | Spawn candidate evaluation off the tick thread | HIGH | Needs mob spawning rules first |
| **Dynamic Activation of Brain (DAB)** | Throttle AI ticking for distant/idle mobs — fewer wasted brain ticks | MED–HIGH | Leaf/Pufferfish pattern; needs goal-selector/brain model first |
| **Lock-free / specialized collections** | FastUtil-equivalent primitive collections + lock-free queues for hot paths | MED | Go idiom differs from Java; design for it from day one (concurrency-ready architecture) |
| **Linear region file format** | Faster/smaller chunk I/O than Anvil `.mca` | MED | Optional storage backend; Anvil compatibility still wanted for interop |
| **Optimized keep-alive / network batching** | Reduced per-tick network overhead | LOW–MED | Cheap win once base networking exists |
| **Concurrency-ready architecture from day one** | The enabler for ALL of the above without rewrites | — | A PROJECT constraint, not a feature — bake clean ownership/boundaries into core types early |

### Anti-Features (deliberately NOT built)

| Feature | Why tempting | Why problematic here | Instead |
|---------|--------------|----------------------|---------|
| **Plugin / Bukkit-Spigot-Paper API** | "Every real server has plugins" | Explicit PROJECT scope exclusion; an extension API is a *second product* with its own ABI/compat burden; freezes internal architecture prematurely | Server core only. Keep internals free to change for perf work |
| **Bedrock / cross-platform protocol** | Wider audience | go-mc is Java-protocol; a second wire format doubles the protocol surface | Java Edition only |
| **Online-mode auth/encryption as v1** | "Real servers are online-mode" | Mojang session auth + AES + Yggdrasil on the critical path delays first playable; not needed to validate the core | Offline-mode first; add Encryption Request/Response + session auth as a later, isolated layer |
| **Full vanilla worldgen parity for v1** | "Players want real Minecraft terrain" | Reproducing Mojang's noise/biome/feature/structure pipeline bit-exact is enormous; blocks everything behind it | Deterministic minimal generator (superflat/noise) for v1; parity as a stretch goal |
| **Signed/secure chat reporting, full Brigadier richness, advancements, statistics, recipe book, world border, scoreboard polish (early)** | Vanilla has them | Each is a deep rabbit hole that does NOT gate "connect and play"; many can be stubbed | Stub or send minimal valid packets; flesh out after first playable |
| **Premature async optimization** | "It's a perf project, optimize now" | Async-optimizing nonexistent or wrong logic produces fast bugs; Leaf itself patches *over* working vanilla logic | Correct synchronous version first, then move it off-thread with the reference to test against |
| **Anti-cheat / strict movement validation (early)** | "Players will fly-hack" | Strict server-authoritative movement is subtle and not needed to play; over-strict = legitimate players rubber-band | Loosely accept movement in v1; tighten later |
| **Custom non-vanilla content** | "Add cool stuff" | Breaks the vanilla-faithful goal and the unmodified-client compatibility contract | Stay 1:1 with vanilla 26.2 semantics |

---

## Feature Dependencies

```
Packet framing (VarInt) + codec [go-mc net]
   └──requires──> Handshake state machine
         ├──> Status ping (leaf node — server appears online)
         └──> Login (offline)
               └──requires──> Compression negotiation
                     └──> CONFIGURATION state
                           ├──> Known Packs (send + read client reply)
                           ├──> Registry Data  [needs codegen data from jar]
                           ├──> Update Tags
                           ├──> Feature Flags
                           └──> Finish Configuration handshake
                                 └──> PLAY: Login(play)/Join Game  [refs registry entries]
                                       └──> Synchronize Player Position (+ Confirm Teleport)
                                             ├──> Set Default Spawn
                                             ├──> Set Center Chunk
                                             │     └──requires──> Chunk Data + Light  [needs worldgen OR persistence]
                                             │           └──requires──> Block-state global palette [codegen]
                                             ├──> "start waiting for chunks" Game Event  ──> world becomes visible
                                             ├──> Player Info Update (self)
                                             └──> Keep Alive loop  [needs tick loop]

Tick loop (20 TPS) ──drives──> {chunk streaming, entity tracking, physics, time, block updates}
Movement handling ──updates──> Set Center Chunk ──triggers──> chunk streaming
Block place/break ──requires──> Block-state palette + chunk mutation + Block Update ack
Inventory ──requires──> component-based slot format (post-1.20.5)
Entity tracking ──requires──> tick loop + view-distance windowing
Persistence (region/Anvil) ──alternative-to / feeds──> worldgen (load existing vs generate new)

Async pathfinding / tracker / spawning / DAB ──require──> the SYNCHRONOUS versions to exist first
Linear region format ──enhances──> persistence
```

### Dependency notes (load-bearing)

- **Configuration must complete before any Play packet.** Sending Join Game before the client's Acknowledge Finish Configuration is a hard kick. This is the #1 sequencing trap.
- **Known Packs is a request/response gate.** The Notchian server blocks on the client's Serverbound Known Packs reply. You must read it (even just to discard) or configuration stalls.
- **Registry Data references propagate.** Join Game names a dimension type that MUST exist in the registries sent during config. Mismatch = kick. The codegen pipeline (PR #294-296) is the dependency that makes this tractable.
- **Chunks need neighbors to render.** The client won't render a chunk lacking loaded neighbors — send a filled square (radius ≥ 2) around spawn, not a single chunk, or the player sees void despite "correct" data.
- **Position + chunks + game event together = visible spawn.** Any one missing leaves the client in a loading/limbo screen. Treat them as an atomic "first playable" bundle.
- **Tick loop gates all dynamic features.** Keepalive scheduling, chunk streaming, entity movement, and time all assume a running 20 TPS loop.
- **Optimizations strictly depend on their vanilla counterpart.** Per the PROJECT decision, async layers go on last and need the sync version as a correctness oracle.

---

## MVP Definition

### Launch With (v1 — "connect, log in, stand in a visible, ticking world")

- [ ] Handshake + state machine — gates everything
- [ ] Status ping — validates codec + makes server visible
- [ ] Offline login + compression — get to configuration
- [ ] Configuration: Known Packs (send+read), Registry Data, Update Tags, Feature Flags, Finish Configuration — the only path to Play
- [ ] Join Game (play) referencing valid registries — first Play packet
- [ ] Synchronize Player Position + Confirm Teleportation handling — exits limbo
- [ ] Set Center Chunk + Chunk Data/Light for a square around spawn + "start waiting for chunks" Game Event — world becomes visible
- [ ] Block-state global palette + paletted-container chunk encoding (BitStorage, heightmaps, biomes, light) — correct-looking world
- [ ] Keep Alive loop — no mystery 20s disconnects
- [ ] 20 TPS tick loop — the world ticks
- [ ] Player Info Update (self) — player identity/skin
- [ ] Movement handling + view-distance chunk streaming — walk around, terrain keeps loading
- [ ] Minimal deterministic worldgen (superflat/noise) — something to send
- [ ] Disconnect packet — clean kicks
- [ ] **Verifier:** an unmodified vanilla 26.2 client connects, logs in, and stands in a solid, visible, ticking world it can walk around.

### Add After Validation (v1.x)

- [ ] Block place/break with Block Update reconciliation — interact with the world
- [ ] World persistence (Anvil region read/write) — world survives restart
- [ ] Inventory / component-based slots + creative item placement — manage items
- [ ] Entity spawn + synchronous tracking — see mobs and other players move
- [ ] Multiplayer: multiple players seeing each other
- [ ] Health/food/respawn — full survival-ish loop
- [ ] Time/day-night, basic system chat, minimal command graph

### Future Consideration (v2+ — the differentiators)

- [ ] Async entity tracker (sync tracker as oracle)
- [ ] Async pathfinding + goal-selector/brain AI
- [ ] Async mob spawning + DAB
- [ ] Lock-free/specialized collections on hot paths
- [ ] Linear region file format
- [ ] Online-mode (encryption + Mojang session auth)
- [ ] Vanilla-parity world generation
- [ ] Full Brigadier command sync, signed chat, advancements/statistics/scoreboard polish

---

## Feature Prioritization Matrix

| Feature | Client value | Implementation cost | Priority |
|---------|-------------|---------------------|----------|
| Handshake + state machine | HIGH (gate) | LOW | P1 |
| Status ping | MEDIUM | LOW | P1 |
| Offline login + compression | HIGH (gate) | MEDIUM | P1 |
| Configuration sequence (Known Packs/Registry/Tags/Feature/Finish) | HIGH (gate) | HIGH | P1 |
| Join Game (play) | HIGH (gate) | MEDIUM | P1 |
| Synchronize Player Position + Confirm | HIGH (exits limbo) | MEDIUM | P1 |
| Chunk Data + Light + paletted encoding | HIGH (world visible) | HIGH | P1 |
| Block-state global palette | HIGH | HIGH | P1 |
| Keep Alive | HIGH (no kicks) | LOW | P1 |
| 20 TPS tick loop | HIGH | MEDIUM | P1 |
| Movement + chunk streaming | HIGH | MEDIUM–HIGH | P1 |
| Minimal worldgen | HIGH (terrain) | MEDIUM | P1 |
| Block break/place | HIGH | MEDIUM | P2 |
| Persistence (Anvil) | HIGH | MEDIUM | P2 |
| Inventory (component slots) | HIGH | HIGH | P2 |
| Entity spawn/tracking (sync) | HIGH | HIGH | P2 |
| Health/respawn, time, chat, commands (minimal) | MEDIUM | MEDIUM | P2 |
| Async tracker / pathfinding / spawning / DAB | MEDIUM (perf) | HIGH | P3 |
| Linear region format | LOW–MEDIUM (perf) | MEDIUM | P3 |
| Online-mode auth/encryption | MEDIUM | MEDIUM–HIGH | P3 |
| Vanilla-parity worldgen | MEDIUM | HIGH | P3 |
| Plugin API / Bedrock / custom content | — | — | NEVER (anti-feature) |

---

## Competitor / reference feature analysis

| Aspect | Vanilla (Notchian) | Paper/Leaf | go-mc (base) | Ender's approach |
|--------|--------------------|-----------|--------------|------------------|
| Protocol codec | reference impl | inherits vanilla | provides `net` codec | reuse go-mc, retarget 776 |
| Registry/data | from jar datapacks | inherits | — | **codegen from jar (PR #294-296)** |
| World format | Anvil | Anvil + Linear (Leaf) | provides `save`/`region` | Anvil first, Linear as perf differentiator |
| Worldgen | full noise/biome/structure | inherits vanilla | none | minimal first, parity stretch |
| Entity AI | goal selectors / brains | + async path, DAB | none | sync brain first, async layers later |
| Concurrency | mostly single main thread | async trackers/path/spawn | — | concurrency-ready core from day one |
| Plugins | none | Bukkit/Spigot/Paper API | `server` framework only | **none (anti-feature)** |
| Runtime | JVM | JVM | Go | Go, no JVM (core differentiator) |

---

## Sources

- [Java Edition protocol/Packets – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/Packets) — play-state login sequence, packet ordering (HIGH)
- [Java Edition protocol/Chunk format – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/Chunk_format) — paletted containers, BitStorage, heightmaps, light, neighbor-render rule (HIGH)
- [Java Edition protocol/Registries – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/Registries) — registry sync, known packs, tag rules (HIGH)
- [Java Edition protocol/FAQ – Minecraft Wiki](https://minecraft.wiki/w/Java_Edition_protocol/FAQ) — configuration state mandatory since 1.20.2, Login→Play invalid (HIGH)
- [Configuration Stage – PaperMC/Velocity (DeepWiki)](https://deepwiki.com/PaperMC/Velocity/8.1-configuration-stage) — real-world config sequence: read/discard Known Packs, send Registry Data + Update Tags, Finish Configuration, await acknowledge (MEDIUM)
- [Reverse-Engineering the Minecraft Protocol – Brendan Swanson](https://www.bswanson.dev/blog/reverse-engineering-minecraft-codec-system/) — practical codec/state walkthrough (MEDIUM)
- PROJECT.md context block — protocol 776 facts, 29671 block states, component slots, Teleport restructure (proto 769+), Leaf architecture, go-mc coverage (HIGH, internal)

---
*Feature research for: Minecraft Java Edition 26.2 server core in Go*
*Researched: 2026-06-23*
