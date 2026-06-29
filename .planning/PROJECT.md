# Ender — Minecraft Java Server in Go

## What This Is

A from-scratch Minecraft Java Edition server (version 26.2, protocol 776) written in Go, inspired architecturally by Leaf (Winds-Studio's high-performance Paper fork). It uses `Tnze/go-mc` as the protocol/data foundation and builds the full server stack — networking, world, chunks, entities, AI, physics, game loop — on top. Target audience: server operators and Go developers who want a high-performance, vanilla-faithful Minecraft server without the JVM.

## Core Value

A Go server that a vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — with an architecture designed from day one for the concurrency-based optimizations Leaf pioneered (async pathfinding, async entity tracking, async mob spawning).

## Current State

**Shipped: v4 (2026-06-29).** Sulfur's gameplay is now **scriptable through a dual-runtime
plugin API** — without giving up the two things that define the project (the pure-Go static
binary, CGO_ENABLED=0; the 1:1-with-the-jar mandate). **Starlark** (`go.starlark.net`, pure-Go,
sandboxed with a step-budget + recursion-off + no-I/O builtins, FrozenValue tick-boundary
sharing) is the hot-path core; a typed event bus fires the core gameplay events to register-once
hooks at 9 discrete server seams, OFF the per-entity hot path. Plugins DECLARE behavior loaded
once and Go runs the hot path calling the declared hooks (a plugin can fully override a mob). The
API is **dogfood-validated across two domains**: the vanilla pig was rewritten AS a Starlark
plugin (3 goals, literal jar port, behavior-identical to the retained `newPigAI` Go oracle, the
ONLY pig now), and crafting was built THROUGH the plugin API (a recipe-provider plugin drives the
result + the formerly-stubbed `ResultSlot.onTake` consume, plus the `crafting_table` 3×3 menu).
An **opt-in Python runtime** (`qur/gopy` @ `python3.14`, module `gopython.xyz/py/v14`, CPython via
cgo) sits behind a `python` build tag — the default no-tag build stays pure-Go static with ZERO
gopy in the import graph (verified `go list -deps`); Python runs HEAVY plugins off-tick, rejoining
via the async seam. **Folia regionization** (REGION-01, folded from the v3 deferral) layers
parallel-region tick threading (conc fan-out/barrier, N=2 static chunk→region hash, cross-region
transfer at the barrier, region-aware Emit) over the faithful logic — 2 regions tick in parallel,
`-race` clean, behavior-neutral. Closed by **PLUGIN-07**: a bot-driven visual gate (cmd/testbot, a
real proto-776 client, OBSERVES the wire — custom mob spawns+walks, vanilla pig wire-identical,
vanilla+custom crafting result/consume, an on_block_break SystemChat — exit 0 = PASS) PLUS an
automated perf gate (`TestPerfGate` + the A/B `BenchmarkPluginPigVsGoNative`: the plugin layer
adds ~1.7–4.2% per-mob-tick over Go-native, under a baseline-derived cap; zero-subscriber Emit =
0 allocs/op). 8 phases (21–28), 19 plans, 8/8 requirements, 7/7 cross-phase integration chains
wired, 4/4 gate flows green. The "no plugin API" scope was intentionally inverted for v4.

**Shipped: v3 (2026-06-27).** Sulfur is now a real **online-mode** server an operator can
run + watch. The six v1 "unwired seams" are closed — two clients see each other move, position
+ inventory survive reconnect, attacks deal damage with death/respawn, water flows (ported
FlowingFluid) and affects swim/buoyancy/breath, and broken blocks drop pickable Item entities.
Logins authenticate against Yggdrasil with RSA + hand-rolled AES-128/CFB8 encryption (no new
dep, CGO=0 preserved) behind an `online-mode` flag, with real UUIDs/skins propagated to the
tab list. A bubbletea TUI console (commands + live logs, degrades off-TTY) + full
disconnect-reason taxonomy. The v2 structures are finished — a shared loot evaluator (chests +
block drops), inhabitants (witch/cat/villager/silverfish), Beardifier terrain-fit, and
structure-start NBT persistence. Closed with a live 12-bug fidelity sweep (chest/water/cane/
hats/persist/swim/oxygen), each reproduced, jar-verified, 1:1 ported, regression-tested, and
operator-confirmed in-game. 4 phases (17–20), 33 plans, 15/15 v3 requirements, 4/4 integration
seams wired. Also landed (parallel worktrees, v3.1 vanilla-completeness): the SUB-PERSIST /
SUB-ITEMNBT / SUB-BLOCKTICK / SUB-FACESTURDY / SUB-ATTRIB subsystems — chunk-save loop, ItemStack
disk codec, scheduled block ticks, per-face block support, and the attribute system — all 1:1
from the jar, `-race` clean, the prerequisite base for v4 entity plugins.

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

## Next Milestone Goals

v4 shipped the plugin/scripting system. The next milestone is undefined — candidate directions
(operator-driven plugin distribution; richer plugin mutation vocabulary + sub-interpreter Python
parallelism; dynamic region merge/split + per-region persistence; the cited vanilla-completeness
follow-ups: more mob goals/breeding, furnace/cooking blocks, SUB-ITEMNBT Phase B). Run
`/gsd-new-milestone` to scope it. The cited carryover lives in the v4 phase `deferred-*.md` files
and the milestone audit's `tech_debt`.

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
- [x] Gameplay completion: the six unwired seams — player-visibility broadcast, position-load apply, inventory join-sync, damage dispatch + fall damage, fluid simulation + player fluid physics, block-break item drops (GAMEPLAY-01..07) — Phase 17 — v3
- [x] Online-mode: Mojang/Yggdrasil auth + AES-128/CFB8 protocol encryption (ONLINE-01/02) — Phase 18 — v3
- [x] TUI (bubbletea + bubbles) console + disconnect-reason logs (TUI-01/02) — Phase 19 — v3
- [x] Structure polish: loot tables, structure entities, afterPlace beard, NBT persistence (STRUCT-POLISH-01..04) — Phase 20 — v3
- [x] Vanilla-completeness subsystems: chunk-save loop, ItemStack disk codec, scheduled block ticks, per-face block support, attribute system (SUB-PERSIST/ITEMNBT/BLOCKTICK/FACESTURDY/ATTRIB) — v3.1 (parallel worktrees)
- [x] Starlark runtime foundation (`go.starlark.net`, CGO=0, step-budget sandbox, FrozenValue tick-boundary sharing) + plugin host + typed event bus (register-once, off-hot-path) (PLUGIN-01/02) — Phases 21–22 — v4
- [x] Declarative entity/mob behavior API (frozen tick-owned handles, full-override path) + vanilla-mobs-as-plugins dogfood (behavior-identical, jar-verified) (PLUGIN-03/04) — Phases 23–24 — v4
- [x] Crafting/recipes THROUGH the plugin API — 2nd-domain dogfood (recipe-provider plugin, result+consume, crafting_table 3×3 menu) (PLUGIN-05) — Phase 25 — v4
- [x] Opt-in Python runtime (`qur/gopy` @ `python3.14`) behind a `python` build tag — default build stays pure-Go static (PLUGIN-06) — Phase 26 — v4
- [x] Folia-style per-region tick threading, region-aware plugin seam (REGION-01) — Phase 27 — v4
- [x] Plugin system bot-driven visual + automated perf gate (PLUGIN-07) — Phase 28 — v4

### Active (next milestone — undefined; run `/gsd-new-milestone`)

_No active requirements — v4 shipped. See "Next Milestone Goals" above for candidate directions._

### Out of Scope

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
- **Scope**: Server core PLUS (as of v4) a dual-runtime plugin/extension API. The original "server core only — no plugin API" boundary is intentionally inverted for v4 (user-directed): a Starlark (pure-Go, CGO=0) hot-path runtime + an opt-in build-tag-gated Python runtime. The plugin layer carries the 1:1 mandate (vanilla mobs become jar-faithful plugins) and the CGO=0/-race constraints.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Use Tnze/go-mc as protocol/data base | Provides ~30% (codec, NBT, chunk/region, save) for free; avoids reimplementing the wire format | ✅ Shipped v1.0 |
| Code-generate data layer from official 26.2 jar (PR #294-296 approach) | Mojang ships unobfuscated jar; codegen yields authoritative packets/registries/blocks for 776 with no hand-transcription | ✅ Shipped v1.0 |
| Build vanilla logic first, Leaf optimizations last | Async-optimizing nonexistent logic is impossible; correctness before performance | ✅ Shipped v1.0 |
| No plugin system (v1–v3) → dual-runtime plugin API (v4) | v1–v3 kept focus on server core. For v4 the user reversed it: a Starlark (CGO=0, sandboxed, deterministic) hot-path runtime + opt-in Python (build-tag) off-tick, with vanilla mobs + crafting dogfooded THROUGH the API. The 1:1 mandate + CGO=0/-race constraints carry into the plugin layer. | ✅ Shipped v4 |
| Starlark as the CORE plugin runtime (not Python) | `go.starlark.net` is pure-Go → preserves the CGO=0 static-binary value prop; natively sandboxed (step budget, recursion-off, no I/O builtins) + deterministic → safe inside the tick loop. Python (cgo/libpython) can't be the default without breaking the static binary, so it's opt-in behind a build tag for heavy off-tick work only. | ✅ Shipped v4 (`go list -deps` confirms ZERO gopy in the default graph) |
| Plugins DECLARE behavior once; Go runs the hot path | 200 mobs × 20 TPS ≠ 4000 interpreter calls/sec — the interpreter runs at load + on events + on cached decisions, not per-entity-per-tick. Keeps "ultra-efficient" true while still allowing a plugin to FULLY override a mob. | ✅ Shipped v4 (perf gate: plugin pig ~1.7–4.2% over the Go-native oracle, under the cap) |
| Thin-id entity handle (id + *TickLoop, re-resolves on the owner thread; never a live *Entity) | The Phase-23 plugin↔entity bridge MUST be race-safe across the tick boundary AND across region threads. A handle that carries only an id + re-resolves on the thread owning the entity is the single primitive that satisfies both — it is also the Folia-safety primitive that made region-aware hooks (Phase 27) fall out cleanly. | ✅ Shipped v4 (the entity API + REGION-01, `-race` clean) |
| gopy = `qur/gopy` @ `python3.14` (`gopython.xyz/py/v14`), NOT go-Python/gopy | The maintained fork supports CPython 3.14; the old go-Python/gopy line (the `/py/v3` 3.11 module) is years stale. The `/py/v14` vanity path resolves to `github.com/qur/gopy`. | ✅ Shipped v4 (Phase 26, the `-tags python` build linked libpython3.14 + tests + -race ran live in a Docker python:3.14 image) |
| Regionize a WORKING single-thread seam (extract → barrier → N=2), don't design around regions first | The operator's locked incremental path: extract the `region` struct at N=1 (behavior-neutral) → add the conc fan-out/barrier at N=1 → flip to N=2. Each step is independently `-race`-verifiable + keeps the existing suite green, so regionization stays a pure concurrency change over the faithful logic (the only permitted deviation). | ✅ Shipped v4 (REGION-01, 2 regions parallel + behavior-neutral) |
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
*Last updated: 2026-06-29 — v4 Plugin/Scripting System shipped (8/8 requirements validated across Phases 21–28, 7/7 cross-phase integration chains wired, 4/4 bot-driven gate flows green + the automated perf gate). Dual-runtime API: Starlark hot-path core (CGO=0 preserved) + opt-in build-tag Python; vanilla mobs + crafting dogfooded 1:1 through the API; Folia regionization (REGION-01) folded in. Next milestone undefined — run `/gsd-new-milestone`.*
