# Ender — Minecraft Java Server in Go

## What This Is

A from-scratch Minecraft Java Edition server (version 26.2, protocol 776) written in Go, inspired architecturally by Leaf (Winds-Studio's high-performance Paper fork). It uses `Tnze/go-mc` as the protocol/data foundation and builds the full server stack — networking, world, chunks, entities, AI, physics, game loop — on top. Target audience: server operators and Go developers who want a high-performance, vanilla-faithful Minecraft server without the JVM.

## Core Value

A Go server that a vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — with an architecture designed from day one for the concurrency-based optimizations Leaf pioneered (async pathfinding, async entity tracking, async mob spawning).

## Current State

**Shipped: v2.0 (2026-06-25).** On top of the v1.0 playable server, the overworld now
**looks + reads like vanilla 26.2**: every biome grows its correct vanilla tree set + full
ground cover (grass/flowers/cactus/cane/mushrooms) + decoration ores, and the emblematic
structures generate in their exact vanilla positions — desert pyramids/jungle temples/
igloos/swamp huts, mineshafts, strongholds (concentric-rings + end-portal room), and
data-driven **villages** (5 biome variants via the `.nbt`/template_pool/jigsaw `Placer`
system) — all ported 1:1 from the unobfuscated jar, deterministic per seed, `-race` clean.
7 phases (10–16), 22 plans, 15/15 v2 requirements, two real-client VISUAL GATES approved.
Worldgen added NO new wire surface (chunk format sealed in v1 Phase 4). CI/CD live: a public
GHCR image (`ghcr.io/imhinotori/sulfur`, development→`:latest`, main→`:stable`) auto-deploys
to a production demo server (demo.trysulfur.net:25565) via Watchtower. Per-player view
distance is the vanilla default (radius 10). The "no JVM" Go static binary value prop holds.

**Shipped: v1.0 (2026-06-24).** A real unmodified vanilla 26.2 client connects, logs in,
and PLAYS a persistent, ticking world: biome-varied noise overworld + mobs (ported vanilla
AI) + block place/break + component-slot inventory + damage/death/respawn + persistence +
commands + chat — all `-race` clean with Leaf-style async optimizations. 9 phases, 46 plans,
45/45 v1 requirements + PARITY-01.

## Current Milestone: v3 — Online-mode + Operator UX + Structure polish

**Goal:** Make Sulfur a real online-mode server an operator can run + watch — Mojang/Microsoft authenticated logins with encrypted protocol, a proper TUI console (commands + live logs + disconnect reasons), and the structures finished (loot, inhabitants, terrain-fit, persistence).

**Target features:**
- **ONLINE-01 / ONLINE-02** — Mojang/Microsoft account authentication (Yggdrasil session join) + protocol encryption (AES/CFB8 + the encryption-request/response handshake), so the server runs in online-mode (real UUIDs, skins, ownership verification).
- **TUI (bubbletea + bubbles)** — a terminal console with a command-input zone + live scrolling logs; disconnect-reason logging (why each player dropped: kick/timeout/protocol error/quit).
- **Structure polish** — loot tables (chest contents), structure entities (villagers/witch/cat/silverfish spawns), `afterPlace` terrain-beard (structures adapt to terrain), structure-start NBT persistence (the documented v2 deferrals).

**Out of this milestone:** REGION-01 (Folia-style per-region tick threading) — deferred to v4.

## Requirements

### Validated (shipped v1.0)

- [x] Vanilla 26.2 client handshake → login → configuration → play (protocol 776) — Phase 2 (NET-01..07)
- [x] Protocol/data layer code-generated from the official 26.2 jar (#294-296 pipeline) — Phase 1 (GEN-01..04)
- [x] Component-based slot/item format, correct BitStorage + heightmap encoding — Phase 4/6 (WORLD-02/03, ENT-04)
- [x] Authoritative 50ms-anchored tick loop + CS2-style subtick layer — Phase 3 (TICK-01..06)
- [x] World + chunk management: load/generate/store/stream/persist — Phase 4 (WORLD-01..05)
- [x] World generation — deterministic stub (v1) AND full vanilla-parity noise worldgen — Phase 4/9 (WORLD-04, PARITY-01)
- [x] Player session lifecycle: spawn, move, view chunks, tab list, keepalive — Phase 5 (PLAY-01..06)
- [x] Entity system + tick-driven goal-selector AI for mobs — Phase 6/7 (ENT-01, AI-01..03)
- [x] Basic physics: gravity, collision, block place/break — Phase 6 (ENT-02/03)
- [x] Command system (dispatch + chat) — Phase 7 (CMD-01/02)
- [x] Leaf-style async optimizations layered after vanilla logic — Phase 8 (OPT-01..06)
- [x] Full vanilla per-biome vegetation (trees/grass/flowers/ores) via the ported ConfiguredFeature/PlacedFeature pipeline — Phase 10–13 (GEN2-01..03, FEAT-01..06) — v2.0
- [x] Vanilla structures (temples, mineshafts, strongholds, villages) in vanilla positions, deterministic per seed — Phase 14–16 (STRUCT-01..06) — v2.0

### Active (v3 — Phases 17–20, see REQUIREMENTS.md)

- [ ] Gameplay completion: the six unwired seams — player-visibility broadcast, position-load apply, inventory join-sync, damage dispatch + fall damage, fluid simulation + player fluid physics, block-break item drops (GAMEPLAY-01..07) — Phase 17
- [ ] Online-mode: Mojang/Yggdrasil auth + AES-128/CFB8 protocol encryption (ONLINE-01/02) — Phase 18
- [ ] TUI (bubbletea + bubbles) console + disconnect-reason logs (TUI-01/02) — Phase 19
- [ ] Structure polish: loot tables, structure entities, afterPlace beard, NBT persistence (STRUCT-POLISH-01..04) — Phase 20
- [ ] Folia-style per-region tick threading (REGION-01) — **deferred to v4**

### Out of Scope

- **Plugin system (Bukkit/Spigot/Paper API)** — explicitly excluded by user; this is a server core, not a plugin platform
- **Bedrock Edition / cross-platform protocol** — Java Edition only; go-mc is Java-protocol
- **Mojang account auth / online-mode encryption as v1 priority** — offline-mode first; online-mode is a later requirement, not core
- **Faithful 1:1 port of Leaf's Java code** — Leaf is architectural inspiration; Go reimplements concurrency patterns idiomatically, it does not transpile Java patches

## Context

- **Toolchain confirmed:** Go 1.26.1 (windows/amd64), Java 25 Zulu (local, for running jar extractors), Docker 29, javac 25.
- **26.2 facts:** Java Edition, protocol 776, new Mojang version scheme YY.D.H (year.drop.hotfix). Official server jar ships **unobfuscated** (public mappings) — extractors read real type/field names.
- **go-mc coverage (~30% of foundation):** provides `net` (protocol codec), `nbt`, `level`/`region`/`chunks` (data structures), `save` (world format), `server` (connection framework), `chat`, RCON. Does NOT provide: world generation, entity system, game tick loop, AI, physics.
- **go-mc PRs #294–296 (mj41, draft, MC 1.21.11 / proto 774):** a unified codegen pipeline that extracts everything from the server jar — 264 packet IDs, 1505 items, 1166 block types / 29671 states, 157 entities, 104 data components, 95 registries, 1838 sounds, 147 lang files. Run via `cd tools && go run . --version <v>`. Generates **static data only**; server logic is hand-written. Same pipeline retargets to 776 with `--version 26.2`.
- **Known protocol shifts to handle:** component-based slot format (no NBT in slots, post-1.20.5); BitStorage without VarInt length prefix (reader computes long count from bits-per-entry); chunk heightmaps as typed long arrays not NBT compounds; Teleport packet restructured for protocol 769+ (TeleportID first, DX/DY/DZ velocity, int32 flags); PlayerChat `globalIndex` VarInt prefix.
- **Leaf reference architecture:** Paper fork (Paperweight patches). Optimizations are concurrency layers over vanilla server logic: async pathfinding, Dynamic Activation of Brain (DAB), async entity tracker, async mob spawning, FastUtil collections, lock-free queues, linear region file format, optimized keepalive. In Go we must BUILD the vanilla logic first, then add these layers — you cannot async-optimize logic that does not yet exist.

## Constraints

- **Tech stack**: Go 1.26.1 server; Java 25 only for offline jar data extraction (not a runtime dependency of the server).
- **Compatibility**: Must speak protocol 776 to an unmodified vanilla 26.2 client.
- **Dependencies**: Built on `Tnze/go-mc` (likely a fork to apply the #294-296 codegen approach and retarget 776).
- **Performance**: Architecture must be concurrency-ready from the start so Leaf-style async optimizations can be layered without rewrites.
- **Scope**: Server core only — no plugin/extension API.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Use Tnze/go-mc as protocol/data base | Provides ~30% (codec, NBT, chunk/region, save) for free; avoids reimplementing the wire format | ✅ Shipped v1.0 |
| Code-generate data layer from official 26.2 jar (PR #294-296 approach) | Mojang ships unobfuscated jar; codegen yields authoritative packets/registries/blocks for 776 with no hand-transcription | ✅ Shipped v1.0 |
| Build vanilla logic first, Leaf optimizations last | Async-optimizing nonexistent logic is impossible; correctness before performance | ✅ Shipped v1.0 |
| No plugin system | User-specified scope boundary; keeps focus on server core | ✅ Shipped v1.0 |
| Offline-mode first | Removes auth/encryption from the critical path to first playable connection | ✅ Shipped v1.0 |
| Game-time anchored to 50ms; subtick layer (CS2-style) for player movement/combat only | MC defines game-time by tick count, not real seconds — raising TPS naively accelerates the world (shorter day, faster crops/redstone), violating vanilla-parity. Anchoring game-time to 50ms keeps the world correct; a subtick layer adds µs-precise resolution for movement/hit-reg/projectiles without touching world simulation speed (same pattern as CS2 subtick over a fixed broadcast rate). | ✅ Shipped v1.0 |
| Cross-chunk worldgen seam: hold-at-carved until 8 neighbors carve, then decorate/place into a 3×3 Neighborhood via a single lock-free scheduler goroutine (emit-once) | Features + structures write across chunk boundaries; a per-chunk footprint guard can't express that. The hold-until-neighborhood-complete seam makes decoration/placement pure over (seed, pos) regardless of generation order — the determinism contract the whole milestone rests on. | ✅ Shipped v2.0 (`-race` clean, 5×5 reorder byte-identical) |
| Port worldgen + structure LOGIC directly from the decompiled jar (javap/CFR), embed the DATA the jar ships (//go:embed) | Hand-transcribing 488 feature JSONs / 483 village .nbt / the jigsaw algorithm is infeasible + error-prone; the plan-checker decompiles the jar to verify port fidelity (caught a real frequency-reducer enum swap in Phase 14). Idiomatic Go, no GPL paste, cite the class. | ✅ Shipped v2.0 |
| Structure starts recompute on demand (pure over seed+pos via the ±8 REFERENCES scan), NOT persisted to NBT | A structure owned ≤8 chunks away is found by the bbox-intersect scan; placeInChunk clips each piece to the target chunk's writable box (idempotent). No start-cache persistence needed — the cache is a pure memoization, not state. | ✅ Shipped v2.0 (NBT persistence deferred to v3) |
| Server view distance = vanilla default (radius 10), not the v1 radius-2 placeholder | v1 Phase 4 capped the view at radius 2 (25 columns) as a load-bearing DoS floor before worldgen was profiled; a real client saw a 5×5 window. With the pipeline at ~88ms/chunk, raise to the vanilla 10 (441 columns); the clamp still bounds the ring by the server, never the client. | ✅ Shipped v2.0 |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-25 — v2.0 milestone shipped (all 15 v2 worldgen-features+structures requirements validated, two real-client visual gates approved, CI/CD + prod deploy live; next milestone = v3 online-mode/regionization/TUI).*
