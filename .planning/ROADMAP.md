# Roadmap: Ender — Minecraft Java 26.2 Server in Go

## Overview

Ender is built dependency-first: the protocol-state sequence (Handshake → Status → Login → Configuration → Play) is the external contract that forces the early phase order, and "the tick is the unit of authority" is the internal contract that shapes everything below it. Phase 1 code-generates the authoritative 776 data layer from the unobfuscated jar (the bleeding-edge prerequisite that gates everything). Phases 2–5 walk a vanilla 26.2 client from a TCP connection to standing in a solid, visible, ticking world — the first-playable milestone. Phases 6–7 fill that world with entities, physics, block interaction, AI, and chat/commands, building every async-bound subsystem as a *synchronous* reference implementation behind a clean seam. Phase 8 then swaps synchronous executors for goroutine pools behind those pre-built seams — the Leaf optimizations are additive, never a rewrite, because ownership-based synchronization was established in Phase 3. Phase 9 is the optional stretch (online-mode, vanilla-parity worldgen, Folia regionization), reachable only because state was ownership-isolated from the start.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Foundation — Fork & Codegen** - Fork go-mc, retarget #294-296 codegen to proto 776, commit generated data as Go source
 (completed 2026-06-23)
- [x] **Phase 2: Net & Protocol State Machine** - Handshake → Status → Login → Configuration → Play transitions with the network/tick channel boundary (completed 2026-06-23)
- [x] **Phase 3: Authoritative Tick Loop** - 20-TPS ordered tick spine, ownership-based sync, game-time anchor, subtick layer, keepalive
- [x] **Phase 4: World & Chunk System** - Paletted-container chunk encode/decode, heightmaps/light, deterministic worldgen, view-distance streaming (completed 2026-06-24)
- [x] **Phase 5: Player Session In-World (FIRST PLAYABLE)** - ✅ Vanilla 26.2 client logs in and WALKS AROUND a solid, visible, ticking world whose ring follows it; listed in tab, no kick — FIRST PLAYABLE achieved
- [ ] **Phase 6: Entities, Physics & Interaction** - Entity store/tracker, gravity/AABB collision, block place/break, component inventory, health/respawn, persistence
- [ ] **Phase 7: AI, Pathfinding, Commands & Chat** - Goal-selector brain, synchronous A* navigation, mob spawning, command dispatch, chat broadcast
- [ ] **Phase 8: Leaf Concurrency Optimizations** - Swap synchronous executors for goroutine pools behind pre-built seams; lock-free collections; linear region format; `-race` clean
- [ ] **Phase 9: Stretch — Online Mode, Vanilla-Parity Worldgen, Regionization** - v2 optional add-ons enabled by the ownership-isolated core

## Phase Details

### Phase 1: Foundation — Fork & Codegen
**Goal**: A forked, Go-only build of go-mc with authoritative proto-776 data (packets, registries, blocks/states, items, entities, components, sounds) generated from the official jar and committed as Go source.
**Depends on**: Nothing (first phase)
**Requirements**: GEN-01, GEN-02, GEN-03, GEN-04
**Success Criteria** (what must be TRUE):
  1. The forked go-mc (pinned master commit + #294-296 cherry-picks) compiles and runs with `go build` alone — no JVM in `go.mod` or the runtime image
  2. `cd tools && go run . --version 26.2` against the official unobfuscated 26.2 server jar (JDK-25 + Docker extractor) produces 776 data without manual transcription
  3. Generated 776 packets, registries, blocks/states, items, entities, components, and sounds exist as committed Go source files
  4. The 774→776 diff is enumerated and reviewed, so every changed byte layout traces to the jar rather than memory
**Plans**: 3 plans
- [x] 01-01-PLAN.md — Fork go-mc at pinned commit, apply #294-296, capture 774 baseline, retarget extractor to JDK 25
- [x] 01-02-PLAN.md — Run codegen at 26.2 (sha1-verified jar), fix any extractor rename, commit generated 776 Go source
- [x] 01-03-PLAN.md — Enumerate the 774->776 diff, correct protocol/version constants, assert Go-only build + human sign-off
**Research**: Needs deeper per-phase research — retargeting draft #294-296 from 1.21.11→26.2 is real engineering with expected schema/rebase drift; the 774→776 diff must be derived against the jar.

### Phase 2: Net & Protocol State Machine
**Goal**: A vanilla 26.2 client transitions Handshake → Status → Login → Configuration → Play without a silent disconnect, over a network layer that talks to the tick only through channels.
**Depends on**: Phase 1
**Requirements**: NET-01, NET-02, NET-03, NET-04, NET-05, NET-06, NET-07
**Success Criteria** (what must be TRUE):
  1. The server completes Handshake and asserts protocol 776, and answers a Status ping with version, MOTD, and player count in the client's server list
  2. The server completes offline-mode Login with compression negotiated, then completes the full Configuration sequence (Known Packs, Registry Data, Update Tags, Feature Flags, Finish Configuration) and reaches Play with no silent kick / "Loading terrain…" hang
  3. Each connection has exactly one writer goroutine; inbound packets enqueue intents to the tick via channels with no game-state access from network goroutines
  4. VarInt/VarLong framing, the compression threshold, and the 2²¹−1 max-packet cap are enforced; an explicit Disconnect packet carries a readable reason
**Plans**: 4 plans
- [x] 02-01-PLAN.md — NET-05 concurrency seam (one writer goroutine, intent inbound channel + stub consumer, bounded disconnect-on-full backpressure), NET-06 cap/threshold assertions, NET-07 state-aware Disconnect helper, shared net.Pipe harness
- [x] 02-02-PLAN.md — NET-01 handshake assert-776 (readable Login Disconnect on mismatch), NET-02 Status ping (version 26.2/proto 776/MOTD/players), NET-03 offline Login + compression, cmd/ender assembly
- [x] 02-03-PLAN.md — NET-04 payload: extract + embed (//go:embed) the real 26.2 registry NBT (dimension_type, biome incl. plains, damage_type, chat_type), WriteRegistryData/WriteTags send helpers (defuses the stale-schema trap)
- [x] 02-04-PLAN.md — NET-04 sequence: rewrite AcceptConfig as the full ordered Configuration flow (Known Packs → Feature Flags → Registry Data → Update Tags → Finish → read Acknowledge), reach Play; vanilla-26.2 capture-diff + human sign-off
**Research**: Needs deeper per-phase research — the exact 776 registry set, order, and network-NBT encoding for Configuration is the highest-risk single area and must be jar-derived/capture-diffed (the wiki documents only ≤773).

### Phase 3: Authoritative Tick Loop
**Goal**: A deterministic, ordered tick spine that owns all game state, with the ownership-based-sync invariant, game-time anchor, subtick layer, and async rejoin seam established before any subsystem fills it.
**Depends on**: Phase 2
**Requirements**: TICK-01, TICK-02, TICK-03, TICK-04, TICK-05, TICK-06
**Success Criteria** (what must be TRUE):
  1. An authoritative loop drives game state through deterministic ordered phases (drain inbound → world → chunk → entity → AI → physics → apply async results → tracking → flush outbound), with `applyAsyncResults` present as a no-op seam from day one
  2. Game-time is anchored to 50ms / MC-tick — a measured in-game day is 20 minutes and redstone/crops/weather/time never accelerate or slow regardless of internal scheduling
  3. A CS2-style subtick layer resolves player movement, collisions, hit detection, and projectiles by microsecond-timestamped input in chronological order, broadcasting at the vanilla protocol rate
  4. All game state is mutated only by the tick-owning goroutine; Keep Alive runs on an independent timer (no ~20s mystery disconnects); MSPT budget is tracked and observable
**Plans**: 3 plans
- [x] 03-01-PLAN.md — Tick spine core: injectable clock, single-owner Run loop consuming chan Intent, fixed-order phase pipeline, 50ms game-time accumulator + spiral clamp, no-op applyAsyncResults + tracker.Tick seams, MSPT atomic snapshot (TICK-01/02/05/06)
- [x] 03-02-PLAN.md — Minimal subtick seam: bounded per-player us-timestamped input buffer + chronological in-tick drain + stub applyInput (TICK-03); prove KeepAlive independence (stalled tick fires no timeout, TICK-04)
- [x] 03-03-PLAN.md — Wire the real tick-driven gameTick GamePlay + cmd/sulfur/main.go (replace stubGamePlay), start tick + keepalive goroutines; end-to-end connection-stands-in-a-ticking-world assertion (TICK-01/04/05/06)
**Research**: Standard pattern (lighter research) — the single-threaded loop + compute-off/apply-on design is well-documented across Paper/Folia/Leaf and is settled.

### Phase 4: World & Chunk System
**Goal**: Chunks load, generate, encode, light, and stream correctly so a vanilla client renders solid ground instead of void or stripes — off-tick IO channeled into the tick.
**Depends on**: Phase 3
**Requirements**: WORLD-01, WORLD-02, WORLD-03, WORLD-04, WORLD-05
**Success Criteria** (what must be TRUE):
  1. Chunks load, generate, and store via the region/Anvil save format off the tick goroutine, with results channeled into the tick
  2. Paletted-container chunk sections round-trip encode/decode correctly (bits-per-entry → palette format, LSB long-packing, no length prefix per 1.21.5+, fluid-count short per 26.1+); heightmaps encode as typed long arrays with block light and sky light sent
  3. A deterministic minimal worldgen (superflat/noise stub) produces a stable, reproducible world
  4. A vanilla 26.2 client stands on an all-stone flat chunk: chunks stream by view distance with the neighbor-ring requirement satisfied — the client renders and does not fall through the void
**Plans**: 4 plans
- [x] 04-01-PLAN.md — Fix the two confirmed 776 wire bugs in level/chunk.go (fluid-count short on Section, trim heightmaps to the 3 CLIENT ids) + self-round-trip/byte-length regression (WORLD-02 partial, WORLD-03 heightmaps)
- [x] 04-02-PLAN.md — New world/ package: deterministic superflat generator, tick-owned chunk manager, off-tick singleflight worker (region-load-or-generate), ClientboundLevelChunkWithLight assembly (WORLD-01 worker side, WORLD-02 full, WORLD-04)
- [x] 04-03-PLAN.md — Wire the off-tick chunkReady rejoin into the Phase-3 applyAsyncResults seam + fill tickChunks/flushOutbound: clamped center-out view-distance ring streaming with ChunkBatchStart/Finished (WORLD-01 tick side, WORLD-05)
- [x] 04-04-PLAN.md — Capture-diff Sulfur's chunk+light bytes vs a real vanilla 26.2 superflat chunk + BLOCKING human-verify real-client visual check (autonomous:false; WORLD-02/03/05 authoritative gate)
**Research**: Needs deeper per-phase research — the exact 776 section layout (fluid-count short, no length prefix, heightmap bits-per-entry) must be confirmed against the jar; go-mc may lag. [DONE — see 04-RESEARCH.md: both wire bugs jar-confirmed against temp/cache/26.2-server.jar.]

### Phase 5: Player Session In-World (FIRST PLAYABLE)
**Goal**: The atomic first-playable bundle — an unmodified vanilla 26.2 client connects, logs in, and stands in a solid, visible, ticking world it can walk around.
**Depends on**: Phase 4
**Requirements**: PLAY-01, PLAY-02, PLAY-03, PLAY-04, PLAY-05, PLAY-06
**Success Criteria** (what must be TRUE):
  1. The server sends Join Game referencing valid registries and the player enters the Play state
  2. Synchronize Player Position uses the proto 769+ layout (Teleport ID first, DX/DY/DZ velocity in 1/8000-block units, Int32 flags) and Confirm Teleportation is handled
  3. A filled chunk square (radius ≥ 2) plus the "start waiting for chunks" Game Event makes the world visible; Player Info Update places the player in the tab list
  4. Player movement is handled and the view-distance chunk ring follows the player
  5. An unmodified vanilla 26.2 client connects, logs in, and stands in a solid, visible, ticking world it can walk around (the milestone verifier)
**Plans**: 3 plans
- [x] 05-01-PLAN.md — Movement decode (4 ServerboundMovePlayer* layouts, packed flags-byte) into tick-owned position + recenterRing so the view-distance ring follows the player + teleport-id gate + jar-derived ForgetLevelChunk (PLAY-04/02)
- [x] 05-02-PLAN.md — Early-Play tail (PlayerAbilities -> SetHeldSlot -> PlayerInfoUpdate self tab-list -> SetDefaultSpawnPosition) appended before register + incrementing teleport id threaded into awaitingTeleport (PLAY-01/05/02)
- [x] 05-03-PLAN.md — Capture-diff sealed the uncertain Play encoders byte-identical to vanilla 26.2 (no encoder bug; fixed a net/queue close-vs-send race) + BLOCKING human-verify real-client walk-around APPROVED (ring follows / tab list / no kick — "si, funciona :)") (autonomous:false; PLAY-01/02/03/05/06 gate)
**Research**: Needs deeper per-phase research — confirm the 769-era position-sync layout (Teleport ID, DX/DY/DZ in 1/8000-block units, Int32 flags) survived to 776 against the jar/capture.

### Phase 6: Entities, Physics & Interaction
**Goal**: The world becomes interactive — entities spawn/move/track, physics simulates gravity and collision, players place/break blocks and manage inventory, and entity/player state persists.
**Depends on**: Phase 5
**Requirements**: ENT-01, ENT-02, ENT-03, ENT-04, ENT-05, ENT-06
**Success Criteria** (what must be TRUE):
  1. Entities spawn, carry metadata, and move; a synchronous entity tracker sends spawn/move/despawn to nearby players behind an async-shaped `tracker.Tick()` interface
  2. Gravity and AABB collision are simulated for entities and players; players can place and break blocks with Block Update reconciliation
  3. Inventory uses the component-based slot format (post-1.20.5, no NBT in slots)
  4. Health, damage, and respawn work — the player death and respawn flow completes
  5. Entity and player state persists to the Anvil format and reloads correctly
**Plans**: 7 plans
- [ ] 06-01-PLAN.md — Entity store + monotonic entity-ID allocator (replaces hard-coded joinEntityID=1) + per-section grid bucketing (ENT-01 foundation)
- [ ] 06-02-PLAN.md — Synchronous entityTracker behind tracker.Tick() + jar-derived AddEntity/SetEntityData/Move/Remove encoders + tickEntities (ENT-01)
- [ ] 06-03-PLAN.md — Per-axis swept-AABB physics: gravity + collision (entity lands/blocked, no tunnel) + authoritative player anti-clip-through (ENT-02)
- [ ] 06-04-PLAN.md — Block place/break: new world.ChunkManager.SetBlock + PlayerAction/UseItemOn handlers + BlockChangedAck/BlockUpdate reconciliation (ENT-03)
- [ ] 06-05-PLAN.md — Component-slot inventory: extended SlotData encoder + ContainerSetContent/SetSlot + HashedStack ContainerClick decode (ENT-04)
- [ ] 06-06-PLAN.md — Health/damage/respawn (reusing the sealed spawn-info encoder) + Anvil persistence of player .dat + entities region (ENT-05, ENT-06)
- [ ] 06-07-PLAN.md — Capture-diff (AddEntity LP / SetEntityData 0xFF / ContainerSetContent / ContainerClick) + BLOCKING real-client interactive sign-off (autonomous:false; ENT-01..05 gate)
**Research**: Standard pattern (lighter research) — keep entity hot-fields snapshot-friendly and start with per-section grid bucketing for the broad phase.

### Phase 7: AI, Pathfinding, Commands & Chat
**Goal**: Mobs behave and navigate, the world populates by vanilla-style spawning, and the server dispatches commands and broadcasts chat — every async-bound piece built synchronously behind a clean request→snapshot→result seam.
**Depends on**: Phase 6
**Requirements**: AI-01, AI-02, AI-03, CMD-01, CMD-02
**Success Criteria** (what must be TRUE):
  1. Mobs have a goal-selector/brain behavior model driven by the tick
  2. Synchronous A* navigation moves mobs around the world, built as request→snapshot→result executed inline (the seam is ready for async)
  3. Mob spawning populates the world by vanilla-style rules
  4. The server dispatches server-side commands
  5. Chat messages are received and broadcast with the PlayerChat globalIndex prefix handled
**Plans**: TBD
**Research**: Standard pattern (lighter research) — the brain/goal-selector and A* designs follow established vanilla behavior; the load-bearing concern is a clean async-ready seam.

### Phase 8: Leaf Concurrency Optimizations
**Goal**: Swap synchronous executors for goroutine pools behind the seams built in Phases 5–7 — additive optimization, not a rewrite — with hot-path collections specialized and the server `-race` clean.
**Depends on**: Phase 7
**Requirements**: OPT-01, OPT-02, OPT-03, OPT-04, OPT-05, OPT-06
**Success Criteria** (what must be TRUE):
  1. Async pathfinding swaps the synchronous A* executor for a goroutine pool behind the Phase-7 seam, with paths tolerated 1+ ticks late
  2. The async entity tracker runs visibility updates off the main tick and rejoins via the apply-async seam; async mob spawning scans candidate locations off-thread
  3. Hot-path collections use lock-free/specialized variants (xsync/v4) and worker pools use ants/v2; the linear region file format reduces disk I/O and footprint
  4. The server passes `-race` clean on every async subsystem
**Plans**: TBD
**Research**: Standard pattern (lighter research) — the seam-swap pattern and the Go concurrency stack (xsync/ants/conc) are verified; this is applying a known pattern.

### Phase 9: Stretch — Online Mode, Vanilla-Parity Worldgen, Regionization
**Goal**: Optional v2 add-ons enabled by the ownership-isolated core — online-mode auth/encryption, Mojang density-function worldgen, and Folia-style per-region tick threading. Each is independent and optional.
**Depends on**: Phase 8
**Requirements**: ONLINE-01, ONLINE-02, PARITY-01, REGION-01 (all v2 stretch — not counted in v1 coverage)
**Success Criteria** (what must be TRUE):
  1. Mojang/Microsoft account authentication and protocol encryption work for online-mode connections (ONLINE-01, ONLINE-02)
  2. Mojang density-function world generation (improved-Perlin/OctaveSimplex) produces terrain with vanilla parity (PARITY-01)
  3. Folia-style per-region tick threading runs on top of the ownership-isolated core without a rewrite (REGION-01)
**Plans**: TBD
**Research**: Needs deeper per-phase research — which `levelgen` density-function subset to target for vanilla-parity worldgen is bespoke and phase-specific.

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation — Fork & Codegen | 3/3 | Complete   | 2026-06-23 |
| 2. Net & Protocol State Machine | 4/4 | Complete   | 2026-06-23 |
| 3. Authoritative Tick Loop | 3/3 | Complete   | 2026-06-23 |
| 4. World & Chunk System | 4/4 | Complete   | 2026-06-24 |
| 5. Player Session In-World (FIRST PLAYABLE) | 3/3 | ✅ Complete | FIRST PLAYABLE — real client walks around a following world |
| 6. Entities, Physics & Interaction | 0/7 | Not started | - |
| 7. AI, Pathfinding, Commands & Chat | 0/TBD | Not started | - |
| 8. Leaf Concurrency Optimizations | 0/TBD | Not started | - |
| 9. Stretch — Online / Parity / Regionization | 0/TBD | Not started | - |
