# Project Research Summary

**Project:** Ender — Minecraft Java Server in Go
**Domain:** High-performance Minecraft Java Edition server core (v26.2 / protocol 776) in Go on Tnze/go-mc, concurrency-ready for Leaf-style async optimizations
**Researched:** 2026-06-23
**Confidence:** HIGH on foundation, protocol sequence, architecture, and pitfalls; MEDIUM on exact 776 byte layouts (must be jar-derived) and vanilla-parity worldgen.

## Executive Summary

Ender is a from-scratch Minecraft Java 26.2 server core in Go. It is not a "user-facing feature" product — the "user" is an unmodified vanilla 26.2 client, and the entire build is governed by one external contract (**protocol-state ordering**: Handshake → Status → Login → Configuration → Play) and one internal contract (**the tick is the unit of authority**). `Tnze/go-mc` supplies roughly 30% of the foundation (wire codec, NBT, chunk/region data structures, save format, connection framework); Ender builds the other 70% — tick loop, world simulation, entities, AI, physics, commands. Experts in this space (Paper/Folia/Leaf) all converge on the same shape: a single-threaded authoritative tick loop, with heavy pure computation moved off-thread and rejoined into the tick — never lock-guarded shared state.

The recommended approach is opinionated and the four research streams agree tightly. **Fork `Tnze/go-mc` (do not vendor, do not depend on `@master`)** and retarget the unmerged #294–296 codegen pipeline from proto 774 → 776 — this is bleeding-edge (776 post-dates upstream go-mc, the community wiki, and LLM training data), so the 26.2 jar is the single authoritative source for every byte layout. The codegen output gates everything downstream. Build order is strictly dependency-driven: `data codegen → net/protocol state machine → tick spine → world/chunks → player-in-world → entities → physics → AI/pathfinding → commands → async optimizations → (regionization stretch)`. The hard ordering constraint baked into PROJECT.md and confirmed by every research file is **"vanilla logic first, Leaf optimizations last"** — you cannot async-optimize logic that does not yet exist. The single most important early architectural decision is **ownership-based synchronization from stage 2** (tick owns all game state; off-thread code snapshots-in and channels-results-out), which costs near-zero on a single-threaded v1 but is the entire insurance policy against the "concurrency-ready from day one" requirement.

The risk profile is dominated by silent failures, not crashes. The top kill-zone is the **Configuration state**: since 1.20.2, a client validates all synchronized registries at `Finish Configuration` and disconnects silently (empty reason, "Loading terrain..." hang) if a registry is missing, mis-ordered, or wrongly NBT-encoded — this is the #1 "connects but can't play" trap. The second kill-zone is **chunk encoding** (paletted containers, BitStorage long-packing with no length prefix since 1.21.5, heightmaps as typed long arrays, the 26.1+ fluid-count short, light, and the neighbor-render rule) — the "falls through the void / sees stripes" class. Both are mitigated the same way: code-generate from the jar, never hand-transcribe; build encode+decode as one round-trip-tested module; and validate against a *real vanilla 26.2 client* and a captured-byte diff, not your own decoder. The "first playable" finish line is an atomic bundle — Join Game + Synchronize Player Position (proto 769+ layout) + a filled chunk square (radius ≥ 2) + the "start waiting for chunks" Game Event + keepalive loop — any one missing leaves the client in limbo.

## Key Findings

### Recommended Stack

The stack splits cleanly into a build-time codegen toolchain and a Go runtime. **Fork go-mc and retarget the #294–296 draft codegen pipeline to `--version 26.2`** — upstream master is pinned at MC 1.21 (Dec 2024) and the codegen drafts target 1.21.11/774, so a fork is mandatory, not optional. Java 25 is strictly a build-time extractor dependency (in a JDK-25 container) and must never enter the server's `go.mod` or runtime image — leaking it defeats the "no JVM" value prop. For the runtime, the concurrency layer mirrors Leaf's FastUtil + lock-free queues with idiomatic Go libraries, introduced **only** at the async-optimization milestone — the MVP needs nothing beyond stdlib `sync` + `golang.org/x/sync`.

**Core technologies:**
- **Fork of `Tnze/go-mc`** (pinned master commit + #294–296 cherry-picks, retargeted to 776): protocol codec, NBT, region/chunk/save, connection framework — the ~30% foundation. Do NOT `go get @master`.
- **go-mc `tools/` codegen submodule** (separate `go.mod`, `cd tools && go run . --version 26.2`): the only sanctioned way to produce authoritative 776 packets/registries/blocks/items/entities/components — hand-transcription is a non-starter (264 packets, 29,671 block states, 104 components).
- **Java 25 (Zulu/Temurin) + Docker 29**: build-time-only extractor that reflects over the unobfuscated 26.2 jar. Hermetic container, pinned digest. Never a runtime dependency.
- **`puzpuzpuz/xsync/v4`** (async milestone): CLHT concurrent maps + Vyukov MPMCQueue — the Go answer to Leaf's FastUtil/lock-free queues; for the entity table, chunk-holder map, async work queues.
- **`panjf2000/ants/v2`** (async milestone): bounded recycling goroutine pool — execution substrate for async pathfinding/tracker/spawner, one tuned pool per subsystem.
- **`sourcegraph/conc` + `golang.org/x/sync`**: tick-bounded fan-out/join; `singleflight` for chunk-gen dedup; `semaphore` for region-flush IO throttling.
- **Custom Mojang-noise port** (parity stretch only): vanilla terrain is improved-Perlin/OctaveSimplex, NOT OpenSimplex — no off-the-shelf lib reproduces it. `ojrac/opensimplex-go` (archived) is acceptable only for a throwaway non-parity MVP stub.

### Expected Features

The feature axis is protocol-state ordering: "table stakes" means the client disconnects, hangs, or renders void without it. Features are ordered by how early in the connection lifecycle the client demands them. There is no plugin/Bedrock/online-mode-v1 surface — those are explicit anti-features.

**Must have (table stakes — the client breaks without these):**
- Handshake state machine + VarInt framing (go-mc `net`) — gates everything.
- Offline login + compression negotiation — get to configuration.
- **Configuration sequence**: Known Packs (send + read/discard reply), Registry Data, Update Tags, Feature Flags, Finish Configuration handshake — the only path to Play; #1 sequencing/desync trap.
- Join Game (play) referencing valid registries; Synchronize Player Position (proto 769+ layout) + Confirm Teleportation — exits limbo.
- Set Center Chunk + Chunk Data/Light for a square (radius ≥ 2) + "start waiting for chunks" Game Event — world becomes visible (atomic bundle).
- Block-state global palette + paletted-container chunk encoding — correct-looking world.
- Keep Alive loop (independent timer) — no mystery ~20s disconnects.
- 20 TPS authoritative tick loop — the world ticks.
- Movement handling + view-distance chunk streaming; Player Info Update (self); Disconnect packet.

**Should have (competitive differentiators — the reason the project exists, layered AFTER vanilla):**
- No-JVM Go runtime (the structural differentiator vs Paper/Leaf).
- Async pathfinding, async entity tracker, async mob spawning, Dynamic Activation of Brain (DAB).
- Lock-free/specialized collections on hot paths; linear region file format; concurrency-ready core from day one.

**Defer (v2+):**
- Block place/break, inventory (component slots), entity spawn/tracking, health/respawn, time, minimal chat/commands (v1.x — after first playable).
- Online-mode auth/encryption, vanilla-parity worldgen, full Brigadier/signed-chat/advancements polish (v2+).
- NEVER: plugin API, Bedrock, custom non-vanilla content (anti-features).

### Architecture Approach

The architecture hinges on one invariant: the tick is the unit of authority, and game state is mutated only by the goroutine that owns it. Build a single-threaded authoritative tick loop (20 TPS, 50ms budget) with explicit "compute-off / apply-on" seams from day one. The Network Edge (per-connection goroutines) and Persistence (off-tick IO) are the *only* components allowed to run on other goroutines, and they communicate with the tick **exclusively through channels** — never by touching game state. Adopt **ownership-based synchronization (Folia/Leaf model), not lock-based shared state** — this is the entire insurance policy; locks-on-chunks "works" single-threaded but becomes a full rewrite the moment you parallelize.

**Major components (everything from World Manager down is tick-owned game state):**
1. **Connection/Session Manager + Packet Codec + State Machine** — go-mc `net`/`server` provide the wire; Ender owns the per-conn goroutine lifecycle, the channel boundary to the tick, and the Config/Play handlers.
2. **Authoritative Tick Loop** (`server/tick.go`, the spine) — 9 deterministic phases (drain inbound → world → chunk → entity → AI → physics → **apply async results** → tracking → flush outbound). The `applyAsyncResults` rejoin seam exists as a no-op from day one.
3. **World + Chunk System** — go-mc `level`/`save` provide data shapes + MCA IO; Ender owns chunk management (tickets, generation, view tracking, ticking).
4. **Entity System + AI/Brain + Physics** — fully built by Ender; OOP-with-component-split (NOT pure ECS, which fights vanilla fidelity); hot async-bound fields stored as snapshot-friendly structs so going async is swapping an executor, not restructuring callers.
5. **Command Dispatch + Persistence** — orthogonal; persistence runs off-tick on the persist goroutine, communicating via load/save channels.

### Critical Pitfalls

1. **Registry/codec desync during Configuration → silent disconnect** — the #1 "connects but can't play" trap. Avoid: code-generate the full 95-registry set from the jar; send in vanilla order (order = numeric index); use 1.20.2+ network NBT (no root compound name); start simplest path = skip Known Packs, inline all NBT. Verify with a REAL 26.2 client + last-packet-before-disconnect logging.
2. **Treating 776 as a known/training-data version** — Mojang restructures packets nearly every minor version; 776 post-dates everything. Avoid: the 26.2 jar is the single source of truth; diff generated 776 against 774 to enumerate what moved; assert proto 776 at handshake. Every "the packet looks like X" is a hypothesis until jar-confirmed.
3. **Chunk array encoding** (bits-per-entry → palette format, LSB-first long packing with no cross-long spanning, **no length prefix since 1.21.5**, 26.1+ fluid-count short) — the "falls through void / striped terrain" class. Avoid: one round-trip-tested paletted-container module; test all-stone single section first, then mixed; diff against captured vanilla bytes.
4. **Data races / connection write ordering** — Go makes goroutines trivial; world mutation from a network goroutine, or multi-goroutine writes to one connection, corrupt state/frames. Avoid: one writer goroutine per connection (channel-fed); world mutated ONLY in tick; network goroutines enqueue intents. Run `-race` from day one, treat any report as a release blocker.
5. **Premature async** — building async pathfinding/tracking before correct single-threaded logic exists yields fast bugs and a HIGH-cost rip-out. Avoid: enforce "vanilla correctness first" as a phase-ordering constraint; "concurrency-ready" ≠ "concurrent now"; defer async to a dedicated late phase.

(Also load-bearing: heightmaps/light/neighbor-ring, teleport 769+ velocity in 1/8000-block units + Int32 flags, keepalive on an independent timer, component-based slot format 1.20.5+, go-mc tag-vs-master pinning, VarInt/compression framing caps.)

## Implications for Roadmap

Build order is dependency-forced — the protocol-state sequence IS the phase spine, and "vanilla first, Leaf last" is a hard constraint. Suggested phases:

### Phase 1: Foundation — Fork & Codegen (proto 776 data)
**Rationale:** Nothing speaks the protocol or knows block states without this; it is a pure prerequisite and the bleeding-edge risk (776 post-dates all sources). Gates everything.
**Delivers:** Forked go-mc (pinned master commit + #294–296), JDK-25 extractor container, generated 776 packets/registries/blocks/items/entities/components committed as Go source. 774→776 diff reviewed.
**Uses:** go-mc `tools/` submodule, Java 25/Docker, `--version 26.2`.
**Avoids:** Pitfalls 2 (wrong-version drift) and 10 (go-mc tag/PR/Java leakage). Server must build with Go alone.

### Phase 2: Net + Protocol State Machine (handshake → status → login → config → play)
**Rationale:** A client cannot connect until states transition cleanly. The network/tick channel boundary is born here.
**Delivers:** Handshake (assert proto 776), Status ping, offline login + compression, full Configuration sequence (Known Packs, Registry Data, Update Tags, Feature Flags, Finish Configuration), reaching Play state.
**Implements:** Connection/Session Manager, Packet Codec, State Machine. Per-conn goroutines + inbound/outbound channels + single-writer-per-conn established now.
**Avoids:** Pitfalls 1 (registry/config desync — the kill-zone), 11 (VarInt/framing/compression caps).

### Phase 3: Authoritative Tick Loop Skeleton (20 TPS spine)
**Rationale:** Everything below "is a thing that ticks." Establish the spine, phase order, and ownership invariant before filling phases.
**Delivers:** 20 TPS loop with empty/no-op phases, keepalive (independent timer), `applyAsyncResults` no-op seam, MSPT budget tracking.
**Implements:** the tick spine + ownership-based synchronization (the load-bearing early decision).
**Avoids:** Pitfalls 6 (premature async — enforce ordering), 7 (data races — establish writer + intent-queue + ownership invariant), 8 (keepalive on independent timer).

### Phase 4: World + Chunk System
**Rationale:** Player needs ground and chunks before entities have a world. The single biggest correctness sink lives here.
**Delivers:** Chunk load/gen/store, paletted-container BitStorage encode+decode (round-trip tested), heightmaps (typed long arrays), light, fluid-count, view-distance streaming, minimal deterministic worldgen (superflat/noise stub).
**Uses:** go-mc `level`/`save`/region; `singleflight` for chunk-gen dedup. Persist goroutine → load-result channel into tick.
**Avoids:** Pitfalls 3 (chunk array encoding), 4 (heightmaps/light/neighbors). Sub-gate: vanilla client stands on an all-stone flat chunk.

### Phase 5: Player Session In-World (FIRST PLAYABLE)
**Rationale:** Validates the whole inbound→tick→outbound→client round trip with real state. The milestone.
**Delivers:** Join Game + Synchronize Player Position (769+ layout, Confirm Teleportation) + filled chunk square + "start waiting for chunks" Game Event + Player Info Update + movement + view-distance ring — the atomic first-playable bundle.
**Avoids:** Pitfall 5 (teleport 769+ velocity units + Int32 flags), Pitfall 4 (neighbor ring on movement).
**Verifier:** unmodified vanilla 26.2 client connects, logs in, stands in a solid, visible, ticking world it can walk around.

### Phase 6: Entities + Physics + Block Interaction (v1.x)
**Rationale:** AI/spawning have nothing to drive without entities; entities need collision; block interaction needs world. Co-develops.
**Delivers:** Entity store/spawn/metadata/movement, synchronous entity tracker (behind async-shaped `tracker.Tick()` interface), gravity/AABB collision, block place/break with Block Update reconciliation, component-based inventory slots, persistence (Anvil), health/respawn, time, minimal chat/commands.
**Avoids:** Pitfall 9 (component slot format 1.20.5+). Entity hot-fields stored snapshot-friendly.

### Phase 7: AI / Brain + Pathfinding (sync)
**Rationale:** Pathfinding needs collision; brain needs entities + world. Highest-value async target, so its request→snapshot→result seam must be clean — but built **synchronously** now.
**Delivers:** Goal-selector/brain model, synchronous A* navigation, mob spawning — all as request→snapshot→result executed inline.

### Phase 8: Leaf Concurrency Optimizations (async)
**Rationale:** "You cannot async-optimize logic that does not yet exist." Each optimization swaps a synchronous executor for a goroutine pool behind the seams built in Phases 5–7. Additive, not a rewrite.
**Delivers:** Async pathfinding, async entity tracker, async mob spawning, DAB, lock-free collections, linear region format. Introduce `xsync/v4` + `ants/v2` + `conc` here. `-race` gates on every async subsystem.
**Uses:** the full concurrency stack, consuming the seams designed in earlier phases.

### Phase 9 (stretch): Online-mode, Vanilla-parity Worldgen, Folia Regionization
**Rationale:** Only reachable because state was ownership-isolated from Phase 3. Each is independent and optional.

### Phase Ordering Rationale
- **The protocol-state sequence forces the early order** (handshake → config → play); the Configuration gate must complete before any Play packet (instant kick otherwise).
- **Codegen gates everything** — block states, registries, packet layouts all flow from Phase 1; it is the bleeding-edge risk and must be correct first.
- **"Vanilla logic first, Leaf optimizations last"** is a hard PROJECT constraint confirmed by all four research files — Phases 5–7 build synchronous reference implementations; Phase 8 swaps executors behind pre-built seams.
- **Ownership-based sync at Phase 3** is near-zero cost on a single-threaded v1 and is the insurance policy that makes Phases 8–9 additive rather than rewrites.

### Research Flags

Phases likely needing deeper research during planning (`/gsd-research-phase`):
- **Phase 1 (Foundation/Codegen):** retargeting draft #294–296 from 1.21.11→26.2 is explicit work with expected schema drift, not a flag flip — needs the 774→776 diff enumerated against the jar.
- **Phase 2 (Configuration):** exact 776 registry set, order, and network-NBT encoding must be jar-derived/capture-diffed — the highest-risk single area; the wiki documents only ≤773.
- **Phase 4 (Chunk encoding):** exact 776 section layout (fluid-count short, no length prefix, heightmap bpe) must be confirmed against the jar; go-mc may lag.
- **Phase 5 (Teleport/spawn):** confirm the 769-era position-sync layout survived to 776 (Teleport ID, DX/DY/DZ in 1/8000-block units, Int32 flags).
- **Phase 9 (Vanilla-parity worldgen):** which `levelgen` density-function subset to target is phase-specific and bespoke.

Phases with standard patterns (lighter research):
- **Phase 3 (Tick loop):** the single-threaded-loop + compute-off/apply-on pattern is well-documented (Paper/Folia/Leaf); the design is settled.
- **Phase 8 (Async optimizations):** the seam-swap pattern and the Go concurrency stack (xsync/ants/conc) are verified; this is applying a known pattern.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Fork decision and codegen mechanics verified against go-mc repo/PRs #294/#296; concurrency stack verified. MEDIUM only on worldgen-noise library (parity is bespoke). |
| Features | HIGH | Protocol sequence verified against minecraft.wiki; gameplay surface from established vanilla behavior. |
| Architecture | HIGH | Vanilla/Leaf/Folia model verified against DeepWiki Paper/Folia/Leaf + go-mc package boundaries. MEDIUM on exact Go-idiomatic mapping (synthesized — no port-of-record exists). |
| Pitfalls | HIGH | Protocol/registry/chunk/concurrency mechanics verified. MEDIUM only on exact 776-only byte layouts — explicitly must be re-derived from the jar, not asserted. |

**Overall confidence:** HIGH on strategy, build order, and risk identification; MEDIUM on the bleeding-edge specifics (776 byte layouts, parity worldgen) that are intentionally deferred to jar-derivation during their phases.

### Gaps to Address
- **Exact 776 byte layouts** (packet field order, registry NBT, chunk section): the meta-pitfall. Handle by treating the jar as ground truth — diff generated 776 vs 774, capture real vanilla 26.2 client/server traffic, never trust memory or pre-773 wiki. Address per-phase (Phases 1, 2, 4, 5), not up front.
- **#294–296 retarget effort** is unbounded until attempted (drafts, expect rebase conflicts and schema drift). Budget Phase 1 as real engineering, not configuration.
- **Vanilla-parity worldgen scope** (which density-function subset): defer to Phase 9 research; MVP uses a deterministic stub.
- **Entity broad-phase structure** (grid bucketing vs tree): start with per-section grid bucketing (matches vanilla); revisit only if profiling demands.

## Sources

### Primary (HIGH confidence)
- `Tnze/go-mc` repo + PRs #294/#296 (mj41, draft, 1.21.11/774): codegen 3-phase pipeline, extraction counts, master-vs-tag versioning offset, package boundaries (net/server/level/save/nbt).
- minecraft.wiki Java Edition protocol — Packets, Chunk format, Registries, FAQ: state sequence, configuration mandatory since 1.20.2, paletted containers, BitStorage long-packing, length-prefix removed at 1.21.5, network NBT no-root-name, keepalive timing, component slots.
- DeepWiki PaperMC/Folia + Paper + Winds-Studio/Leaf: region threading / ownership-based sync, single-threaded tick loop, async pathfinding/tracker/DAB/rejoin pattern.
- `puzpuzpuz/xsync/v4`, `panjf2000/ants/v2`, `sourcegraph/conc` repos/docs: concurrency primitives.
- Mojang MC-198840 (entities pathfind on main thread — confirms the offload target).
- `.planning/PROJECT.md` Context (codegen facts, protocol shifts, Leaf architecture, toolchain).

### Secondary (MEDIUM confidence)
- PaperMC/Velocity DeepWiki configuration stage; LuminolMC/Luminol Folia threading fixes (same-region check insufficient).
- Velocity #1556 / BungeeCord #2258: compression framing, VarInt byte caps, 2^21−1 max packet.
- minecraft.wiki 26.2 / Protocol version page (776, YY.D.H scheme, released 2026-06-16; wiki documents only ≤773).

### Tertiary (LOW confidence — needs validation)
- All exact 776 byte layouts: hypotheses from ≤774 sources, must be jar-confirmed during their phases.
- `ojrac/opensimplex-go` (archived) — acceptable only for a throwaway non-parity stub.

---
*Research completed: 2026-06-23*
*Ready for roadmap: yes*
