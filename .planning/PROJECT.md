# Ender — Minecraft Java Server in Go

## What This Is

A from-scratch Minecraft Java Edition server (version 26.2, protocol 776) written in Go, inspired architecturally by Leaf (Winds-Studio's high-performance Paper fork). It uses `Tnze/go-mc` as the protocol/data foundation and builds the full server stack — networking, world, chunks, entities, AI, physics, game loop — on top. Target audience: server operators and Go developers who want a high-performance, vanilla-faithful Minecraft server without the JVM.

## Core Value

A Go server that a vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — with an architecture designed from day one for the concurrency-based optimizations Leaf pioneered (async pathfinding, async entity tracking, async mob spawning).

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Server accepts a vanilla 26.2 client through handshake → login → configuration → play state transitions (protocol 776)
- [ ] Protocol/data layer is code-generated from the official 26.2 server jar (packets, registries, blocks, items, entities, components) via the go-mc PR #294-296 pipeline approach
- [ ] Component-based slot/item format (post-1.20.5), correct BitStorage and heightmap encoding
- [ ] Authoritative server tick loop (20 TPS) driving world and entity updates
- [ ] World + chunk management: load/generate/store chunks, send to clients, persist via region/save format
- [ ] World generation (at minimum a deterministic generator; vanilla-parity generation a stretch goal)
- [ ] Player session lifecycle: spawn, move, view chunks, see other players, keepalive
- [ ] Entity system with a tick-driven behavior/AI model (goal selectors / brain) for mobs
- [ ] Basic physics: gravity, collision, block placement/breaking
- [ ] Command system (server-side command dispatch + chat)
- [ ] Leaf-style concurrency optimizations layered on AFTER vanilla logic exists: async pathfinding, async entity tracker, async mob spawning, lock-free/specialized collections, linear region format

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
| Use Tnze/go-mc as protocol/data base | Provides ~30% (codec, NBT, chunk/region, save) for free; avoids reimplementing the wire format | — Pending |
| Code-generate data layer from official 26.2 jar (PR #294-296 approach) | Mojang ships unobfuscated jar; codegen yields authoritative packets/registries/blocks for 776 with no hand-transcription | — Pending |
| Build vanilla logic first, Leaf optimizations last | Async-optimizing nonexistent logic is impossible; correctness before performance | — Pending |
| No plugin system | User-specified scope boundary; keeps focus on server core | — Pending |
| Offline-mode first | Removes auth/encryption from the critical path to first playable connection | — Pending |

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
*Last updated: 2026-06-23 after initialization*
