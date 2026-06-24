# Requirements: Ender — Minecraft Java 26.2 Server in Go

**Defined:** 2026-06-23
**Core Value:** A Go server that an unmodified vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — architected from day one for Leaf-style async optimizations.

## v1 Requirements

Requirements for the initial release. The "user" is an unmodified vanilla 26.2 (protocol 776) client. Each maps to exactly one roadmap phase.

### Foundation & Codegen

- [x] **GEN-01**: A forked `Tnze/go-mc` (pinned master commit + #294-296 cherry-picks) builds with Go alone, no JVM at runtime
- [x] **GEN-02**: A JDK-25 + Docker extractor produces 26.2 data from the official unobfuscated server jar via `cd tools && go run . --version 26.2`
- [x] **GEN-03**: Generated 776 data (packets, registries, blocks/states, items, entities, components, sounds) is committed as Go source
- [x] **GEN-04**: The 774→776 diff is enumerated and reviewed so every byte layout traces to the jar, not memory

### Networking & Protocol State Machine

- [x] **NET-01**: Server completes the Handshake state and asserts protocol 776
- [x] **NET-02**: Server answers Status (server-list ping) with version, MOTD, and player count
- [x] **NET-03**: Server completes offline-mode Login and negotiates packet compression
- [x] **NET-04**: Server completes the full Configuration sequence (Known Packs, Registry Data, Update Tags, Feature Flags, Finish Configuration) and transitions to Play without silent disconnect
- [x] **NET-05**: Each connection has exactly one writer goroutine; inbound packets enqueue intents to the tick via channels (no game-state access from network goroutines)
- [x] **NET-06**: VarInt/VarLong framing, compression threshold, and the 2²¹−1 max-packet cap are enforced correctly
- [x] **NET-07**: Server sends a proper Disconnect packet with a readable reason

### Tick Loop & Timing

- [x] **TICK-01**: An authoritative tick loop drives all game state through deterministic ordered phases (drain inbound → world → chunk → entity → AI → physics → apply async results → tracking → flush outbound)
- [x] **TICK-02**: Game-time is anchored to 50ms / MC-tick — day = 20 minutes, redstone/crops/weather/time never accelerate or slow regardless of internal scheduling
- [x] **TICK-03**: A CS2-style subtick layer resolves player movement, collisions, hit detection, and projectiles by microsecond-timestamped input in chronological order, broadcasting to the client at the vanilla protocol rate
- [x] **TICK-04**: Keep Alive runs on an independent timer; no mystery ~20s disconnects
- [x] **TICK-05**: All game state is mutated only by the tick-owning goroutine (ownership-based synchronization); an `applyAsyncResults` rejoin seam exists as a no-op from day one
- [x] **TICK-06**: MSPT budget is tracked and observable

### World & Chunks

- [x] **WORLD-01**: Chunks load, generate, and store via the region/Anvil save format off the tick goroutine, with results channeled into the tick
- [x] **WORLD-02**: Paletted-container chunk sections encode and decode correctly (bits-per-entry → palette format, LSB long-packing, no length prefix per 1.21.5+, fluid-count short per 26.1+) — round-trip tested
- [x] **WORLD-03**: Heightmaps encode as typed long arrays; block light and sky light are sent
- [x] **WORLD-04**: A deterministic minimal world generator produces a stable world (superflat/noise stub for v1)
- [x] **WORLD-05**: Chunks stream to the client by view distance with the neighbor-ring requirement satisfied (client renders, does not fall through void)

### Player Session (First Playable)

- [x] **PLAY-01**: Server sends Join Game referencing valid registries and the player enters the Play state
- [x] **PLAY-02**: Synchronize Player Position uses the proto 769+ layout (Teleport ID first, DX/DY/DZ velocity in 1/8000-block units, Int32 flags) and handles Confirm Teleportation
- [x] **PLAY-03**: Server sends a filled chunk square (radius ≥ 2) plus the "start waiting for chunks" Game Event so the world becomes visible
- [x] **PLAY-04**: Player movement is handled and the view-distance chunk ring follows the player
- [x] **PLAY-05**: Player Info Update is sent so the player exists in the tab list
- [x] **PLAY-06**: An unmodified vanilla 26.2 client connects, logs in, and stands in a solid, visible, ticking world it can walk around

### Entities, Physics & Interaction

- [x] **ENT-01**: Entities spawn, carry metadata, and move; a synchronous entity tracker sends spawn/move/despawn to nearby players behind an async-shaped `tracker.Tick()` interface
- [x] **ENT-02**: Gravity and AABB collision are simulated for entities and players
- [x] **ENT-03**: Players can place and break blocks with Block Update reconciliation
- [x] **ENT-04**: Inventory uses the component-based slot format (post-1.20.5, no NBT in slots)
- [x] **ENT-05**: Health, damage, and respawn work; player death and respawn flow completes
- [x] **ENT-06**: Entity and player state persists to the Anvil format and reloads correctly

### AI & Pathfinding

- [x] **AI-01**: Mobs have a goal-selector/brain behavior model driven by the tick
- [x] **AI-02**: Synchronous A* navigation moves mobs around the world, built as request→snapshot→result executed inline (seam ready for async)
- [x] **AI-03**: Mob spawning populates the world by vanilla-style rules

### Commands & Chat

- [x] **CMD-01**: Server dispatches server-side commands
- [x] **CMD-02**: Chat messages are received and broadcast (with the PlayerChat globalIndex prefix handled)

### Leaf Concurrency Optimizations

- [x] **OPT-01**: Async pathfinding swaps the synchronous A* executor for a goroutine pool behind the Phase-7 seam, with paths tolerated 1+ ticks late
- [x] **OPT-02**: Async entity tracker runs visibility updates off the main tick and rejoins via the apply-async seam
- [x] **OPT-03**: Async mob spawning scans candidate locations off-thread
- [x] **OPT-04**: Hot-path collections use lock-free/specialized variants (xsync/v4); worker pools use ants/v2
- [x] **OPT-05**: Linear region file format reduces disk I/O and footprint
- [x] **OPT-06**: The server passes `-race` clean on every async subsystem

## v2 Requirements

Deferred to a future release. Tracked but not in the current roadmap. Mapped to Phase 9 (stretch).

### Online Mode

- **ONLINE-01**: Mojang/Microsoft account authentication
- **ONLINE-02**: Protocol encryption for online-mode connections

### Vanilla-Parity Worldgen

- **PARITY-01**: Mojang density-function world generation (improved-Perlin/OctaveSimplex) for terrain parity

### Regionization

- **REGION-01**: Folia-style per-region tick threading on top of the ownership-isolated core

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Plugin system (Bukkit/Spigot/Paper API) | User-specified scope boundary — this is a server core, not a plugin platform |
| Bedrock Edition / cross-platform protocol | Java Edition only; go-mc is Java-protocol |
| Faithful 1:1 port of Leaf's Java code | Leaf is architectural inspiration; Go reimplements concurrency idiomatically, does not transpile Java patches |
| Custom non-vanilla content | Goal is vanilla parity, not a modded server |
| JVM at runtime | The "no JVM" Go runtime is the structural differentiator; Java is build-time-only (jar extraction) |
| Raising effective TPS to speed up the world | Game-time is anchored to 50ms for vanilla parity; precision comes from the subtick layer, never from accelerating simulation |

## Traceability

Which phases cover which requirements. Populated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| GEN-01 | Phase 1 | Complete |
| GEN-02 | Phase 1 | Complete |
| GEN-03 | Phase 1 | Complete |
| GEN-04 | Phase 1 | Complete |
| NET-01 | Phase 2 | Complete |
| NET-02 | Phase 2 | Complete |
| NET-03 | Phase 2 | Complete |
| NET-04 | Phase 2 | Complete |
| NET-05 | Phase 2 | Complete |
| NET-06 | Phase 2 | Complete |
| NET-07 | Phase 2 | Complete |
| TICK-01 | Phase 3 | Complete |
| TICK-02 | Phase 3 | Complete |
| TICK-03 | Phase 3 | Complete |
| TICK-04 | Phase 3 | Complete |
| TICK-05 | Phase 3 | Complete |
| TICK-06 | Phase 3 | Complete |
| WORLD-01 | Phase 4 | Complete |
| WORLD-02 | Phase 4 | Complete |
| WORLD-03 | Phase 4 | Complete |
| WORLD-04 | Phase 4 | Complete |
| WORLD-05 | Phase 4 | Complete |
| PLAY-01 | Phase 5 | Complete |
| PLAY-02 | Phase 5 | Complete |
| PLAY-03 | Phase 5 | Complete |
| PLAY-04 | Phase 5 | Complete |
| PLAY-05 | Phase 5 | Complete |
| PLAY-06 | Phase 5 | Complete |
| ENT-01 | Phase 6 | Complete |
| ENT-02 | Phase 6 | Complete |
| ENT-03 | Phase 6 | Complete |
| ENT-04 | Phase 6 | Complete |
| ENT-05 | Phase 6 | Complete |
| ENT-06 | Phase 6 | Complete |
| AI-01 | Phase 7 | Complete |
| AI-02 | Phase 7 | Complete |
| AI-03 | Phase 7 | Complete |
| CMD-01 | Phase 7 | Complete |
| CMD-02 | Phase 7 | Complete |
| OPT-01 | Phase 8 | Complete |
| OPT-02 | Phase 8 | Complete |
| OPT-03 | Phase 8 | Complete |
| OPT-04 | Phase 8 | Complete |
| OPT-05 | Phase 8 | Complete |
| OPT-06 | Phase 8 | Complete |
| ONLINE-01 (v2) | Phase 9 | Pending |
| ONLINE-02 (v2) | Phase 9 | Pending |
| PARITY-01 (v2) | Phase 9 | Pending |
| REGION-01 (v2) | Phase 9 | Pending |

**Coverage:**
- v1 requirements: 45 total (GEN 4 + NET 7 + TICK 6 + WORLD 5 + PLAY 6 + ENT 6 + AI 3 + CMD 2 + OPT 6)
- Mapped to phases: 45 (100%) ✓
- Unmapped: 0 ✓
- v2 stretch requirements: 4 (ONLINE-01, ONLINE-02, PARITY-01, REGION-01) → all mapped to Phase 9

> Note: the prior "41 total" count was a stale tally; counting the actual REQ-IDs yields 45 v1 requirements. All 45 are mapped to exactly one phase with no orphans or duplicates.

---
*Requirements defined: 2026-06-23*
*Last updated: 2026-06-23 after roadmap creation (traceability populated, coverage 45/45)*
