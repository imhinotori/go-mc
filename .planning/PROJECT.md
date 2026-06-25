# Ender — Minecraft Java Server in Go

## What This Is

A from-scratch Minecraft Java Edition server (version 26.2, protocol 776) written in Go, inspired architecturally by Leaf (Winds-Studio's high-performance Paper fork). It uses `Tnze/go-mc` as the protocol/data foundation and builds the full server stack — networking, world, chunks, entities, AI, physics, game loop — on top. Target audience: server operators and Go developers who want a high-performance, vanilla-faithful Minecraft server without the JVM.

## Core Value

A Go server that a vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — with an architecture designed from day one for the concurrency-based optimizations Leaf pioneered (async pathfinding, async entity tracking, async mob spawning).

## Current State

**Shipped: v1.0 (2026-06-24).** A real unmodified vanilla 26.2 client connects, logs in,
and PLAYS a persistent, ticking world: it walks a biome-varied **noise overworld**
(hills/valleys/plains + caves/ravines/aquifers/ore-veins, water at sea level), sees mobs
that spawn/wander/navigate with ported vanilla AI, places/breaks blocks, manages a
component-slot inventory, takes damage/dies/respawns, persists, runs commands, and chats —
all `-race` clean with Leaf-style async optimizations (async pathfinding/tracker/spawn,
xsync/ants, linear region format). 9 phases, 46 plans, 45/45 v1 requirements + PARITY-01.
The "no JVM" Go static binary value prop holds (Java is build-time-only jar extraction).

## Next Milestone Goals (v2 — Worldgen deferrals + auth + regionization)

User-chosen direction for the next milestone:
- **Trees / vegetation** (the feature/decoration subsystem) and **structures** (mineshafts, villages) — the documented Phase-9 deferrals.
- **ONLINE-01 / ONLINE-02** — Mojang/Microsoft account authentication + protocol encryption for online-mode.
- **REGION-01** — Folia-style per-region tick threading on top of the ownership-isolated core.

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

### Active (v2 — pending /gsd-new-milestone)

- [ ] Trees / vegetation features + structures (mineshafts, villages)
- [ ] Online-mode: Mojang auth + protocol encryption (ONLINE-01/02)
- [ ] Folia-style per-region tick threading (REGION-01)

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
*Last updated: 2026-06-24 — v1.0 milestone shipped (all 45 v1 requirements + PARITY-01 validated; next milestone = v2 worldgen-deferrals/auth/regionization).*
