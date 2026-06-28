# Phase 27: Folia Regionization — Research

**Researched:** 2026-06-28
**Domain:** Concurrent game-loop architecture (Folia-style per-region tick threading) layered over a working single-owner tick seam
**Confidence:** HIGH on the Sulfur code map + the concurrency stack; MEDIUM on the Folia internals (DeepWiki/docs are partial on the transfer + barrier mechanics — flagged below)

## Summary

Phase 27 takes Sulfur's single-owner `TickLoop` (TICK-05: ONE goroutine owns every entity, chunk, player, and plugin hook) and splits the WORLD-OWNED half of that state into N independent **region owners**, each a tick goroutine that ticks its own chunks + entities in parallel, exactly like Folia's model (each region has its own "main thread" running the full tick loop at 20 TPS; there is no single main thread anymore). The plugin call seam and the entity/world handles — built in Phases 22–23 to run on THE tick goroutine — must become region-aware: a hook for an entity in region R must run on R's goroutine, and the thin-id handles (`entityHandle`/`worldHandle`/`navHandle`, which already re-resolve `t.entities.get(id)` on the owner per access) must re-resolve against the OWNING region's store, not a single global store.

The single most important architectural fact for planning: **Sulfur already has the exact discipline Folia depends on.** The thin-id-handle pattern (Phase 23, `plugin_entity.go`) and the async rejoin pattern (`async.go` — carry an id/value, never a live pointer; re-resolve on the owner; drop if gone) are *precisely* the cross-thread-safety primitives regionization needs. Folia's core rule — "only the thread currently ticking a region may access data owned by that region" — is the same rule as TICK-05, just multiplied by N. The handles never hold a `*Entity`; they hold an id and re-resolve on whatever goroutine owns the entity. That is already region-correct by construction. The hard work is (a) splitting the world-owned state (`entityStore`, `ChunkManager`, scheduled ticks, `levelRandom`, the per-region plugin dispatch) into per-region instances, (b) keeping the things that MUST stay global (the player network/session, the `EntityIDAllocator`, the gametime anchor, the frozen plugin registry, the connection-accept path) global, and (c) making the inherently CROSS-region paths (the entity tracker, `broadcastBlockUpdate`, `nearestPlayerAt`) read across region boundaries safely.

**Primary recommendation:** Start with a **fixed chunk→region hash into 2+ static regions** (NOT Folia's dynamic merge/split regionizer) to prove the seam end-to-end with a per-tick barrier; keep the player-session/network/tracker GLOBAL and read-only-snapshot across regions; regionize the WORLD-owned state (`entityStore`, `ChunkManager`, scheduled-tick queues) per region; make each region own a `*host.Manager`-dispatch context that re-resolves handles against ITS store. Use `conc` for the per-tick fan-out/barrier (NEW dependency — see Standard Stack) and `xsync` for the chunk→region map only where an off-region reader needs it. Do NOT build the dynamic regionizer, region merge/split, or cross-region async work-stealing in this phase — that is Folia's hardest machinery and is out of scope for a 2-region proof.

## User Constraints (from CONTEXT.md)

No CONTEXT.md exists for Phase 27 yet (`has_context: false`). The constraints below are extracted from REQUIREMENTS.md (REGION-01) and v4-PLAN.md and should be treated as locked until a discuss-phase produces CONTEXT.md.

### Locked Decisions (from REQUIREMENTS.md REGION-01 + v4-PLAN.md)
- **Regionize a WORKING single-thread seam — do NOT design the seam around regions first.** [CITED: v4-PLAN.md line 124-125, REQUIREMENTS.md line 38] The plugin/entity seam is built and proven single-owner (Phases 22–24); Phase 27 re-homes it onto region threads. This is the explicit build-order rationale.
- **The world ticks in parallel regions.** [CITED: REQUIREMENTS.md line 38] 2+ regions tick concurrently.
- **The plugin call seam + the entity API become region-aware — a plugin hook runs on its region's thread.** [CITED: REQUIREMENTS.md line 38]
- **`-race` clean.** [CITED: REQUIREMENTS.md line 38, CLAUDE.md] The Docker `-race` gate is non-negotiable and carries into the region threads.
- **`CGO_ENABLED=0` stays clean for the default binary.** [CITED: CLAUDE.md] No new cgo dependency.
- **The concurrency stack (xsync/ants/conc) is AVAILABLE for this phase.** [CITED: CLAUDE.md "Stack Patterns by Variant", v4-PLAN.md] This is the phase the stack was reserved for. (Caveat: `conc` is NOT yet in go.mod — see Standard Stack.)
- **Game-time stays anchored to 50ms (TICK-02) and shared across all regions.** [CITED: CLAUDE.md "the game-time-anchored-to-50ms decision"] All regions share ONE gametime anchor.
- **1:1 vanilla gameplay mandate.** [CITED: CLAUDE.md] Regionization is the ONLY permitted deviation class (a concurrency/perf change that provably preserves identical observable gameplay — Leaf/Folia-style async over faithful logic). Observable behavior must stay vanilla-identical.

### Claude's Discretion
- Region granularity (chunks-per-region), the exact chunk→region mapping function, the barrier implementation, which cross-region reads use a snapshot vs a concurrent map, and the incremental rollout order (2 regions → N).

### Deferred Ideas (OUT OF SCOPE for Phase 27)
- Folia's **dynamic `ThreadedRegionizer`** (regions that grow/shrink/merge/split at runtime based on chunk load). Start static.
- **Region merge/split** between ticks (Folia's `HolderManagerRegionData.split()/merge()`).
- Cross-region **async work-stealing** / load balancing across the region pool.
- The **perf benchmark / real-client gate** — that is Phase 28 (PLUGIN-07), explicitly separate.
- **Python runtime** — that is Phase 26 (PLUGIN-06), explicitly separate. DO NOT build Python here.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| REGION-01 | Folia-style per-region tick threading; the plugin call seam + entity API become region-aware (a hook runs on its region's thread); the world ticks in parallel regions, `-race` clean, plugin hooks run on the correct region thread. | The whole document. Architecture Patterns gives the region-owner + chunk→region map + per-tick barrier + cross-region transfer + region-aware Emit, named against the real `TickLoop`/`entityStore`/`ChunkManager`/`host.Manager` types. Code Examples give the region tick loop + barrier, a region-aware Emit, and a cross-region transfer. Validation Architecture gives the parallel-regions gate + the hook-on-correct-region gate + `-race`. |

## Architectural Responsibility Map

The central planning question for this phase is **what becomes per-region vs what stays global.** This table IS the phase design.

| Capability | Today (single-owner) | Phase 27 tier | Rationale |
|------------|---------------------|---------------|-----------|
| Entity storage + bucketing (`entityStore`) | ONE `t.entities` | **Per-region** | Folia: each region owns the entity/chunk data in its chunks. An entity is owned by exactly one region. `near()` is region-local for the broad-phase. |
| Chunk storage (`ChunkManager`) | ONE `t.world` | **Per-region** | Folia: chunks belong to exactly one region. Each region loads/generates/flushes ITS columns. |
| Scheduled block/fluid ticks (`blockTicks`, `fluidSchedule`) | ONE per-loop | **Per-region** | Block ticks are per-chunk in vanilla; a region owns its chunks' tick queues (Folia keeps per-region tick lists). |
| Per-mob AI walk (`tickAI`, `serverAiStep`, goal selector) | ONE loop over `t.entities.byID` | **Per-region** | A mob is owned by its region; its AI ticks on that region's thread over that region's store. |
| Physics (`tickPhysics`, `moveEntity`) | ONE loop | **Per-region** | Same — a moving entity re-buckets in its region's store; a cross-column move that crosses a region boundary triggers transfer (see below). |
| Plugin hook dispatch for entity events (`Emit` for spawn/death/AI goals) | ONE `t.plugins.Emit` on the tick | **Per-region dispatch, shared frozen registry** | The CALLABLES are frozen + shared (Phase 21 guarantee, safe across threads); the HANDLES re-resolve against the calling region's store. A hook for an entity in R runs on R's thread. |
| Plugin REGISTRY (the loaded `*host.Manager`, frozen modules) | ONE `t.plugins` | **GLOBAL, shared, frozen, read-only after load** | Load-once-shared-frozen (Phase 21/22). The `hooks` map is written only at load, read on every region thread — lock-free by the frozen guarantee. Hot-reload swap must become a global barrier-gated swap. |
| `levelRandom` (Level.random analogue) | ONE shared `LegacyRandomSource` | **Per-region** | A shared RNG advanced by N goroutines would race AND diverge the stream non-deterministically. Each region needs its own (vanilla's per-level random is non-seed-pinned anyway — see Pitfalls). |
| Gametime anchor (`gametime`, TICK-02 accumulator) | ONE counter, ++ per 50ms | **GLOBAL anchor, per-region read** | All regions MUST share ONE wall-clock-anchored gametime (the 50ms decision). The accumulator/driver is global; each region reads the same tick number. Scheduled block ticks compare against the shared gametime. |
| Player session / network (`tickPlayer`, `Client`, writeLoop, `register`/`unregister`) | tick-owned slice | **GLOBAL (player list) + region-affinity** | A player connection spans regions as the player walks. Folia keeps connection handshaking on the GlobalTickThread. The player's session/socket is global; which region "ticks" the player follows the player's chunk. |
| Entity tracker (`tracker.Tick`, the visibility diff) | ONE loop over `t.players` × `near()` | **CROSS-region read (global driver, per-region snapshot read)** | A player in region A must see entities in adjacent region B. The tracker is INHERENTLY cross-region — it cannot be purely region-local. Use the snapshot-and-merge discipline (Pitfalls). |
| `broadcastBlockUpdate` / chat / playerlist broadcast | ONE loop over `t.players` | **CROSS-region read** | Broadcasts fan out to ALL players regardless of region. Reads the global player list. |
| `EntityIDAllocator` | ONE atomic counter | **GLOBAL** | Already off-tick-safe (atomic, crosses no game state). Players + entities share one id space across all regions — must stay one global allocator so ids never collide. |
| Console command / TUI (`consoleCmd`) | tick-owned drain | **GLOBAL region** | Folia: console execution lives on the GlobalTickThread. A command that mutates a specific region's state must be marshalled onto that region's thread. |
| Chunk save (`chunkSaver`, off-tick) | ONE off-tick consumer | **Per-region produce, shared off-tick consumer** | Each region drains ITS dirty set on its own thread and hands immutable bytes to the (shared) save loop — the existing leaveSnapshots discipline, already cross-goroutine-safe. |

## Standard Stack

The concurrency primitives. **No new gameplay logic** — this phase is pure re-homing of ownership.

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/sourcegraph/conc` | latest v0.3.x | Per-tick fan-out/join (the region barrier): spawn N region-tick closures, wait for all to finish before advancing the shared gametime. Structured concurrency with panic propagation. | [CITED: CLAUDE.md] "conc for tick-bounded fan-out/join where you fan out per-region work within a tick boundary and must join before the next tick." This is the textbook use. **⚠ NOT YET IN go.mod** — only `ants` + `xsync` are present [VERIFIED: go.mod]. Phase 27 ADDS this dep. It is pure-Go (CGO=0 safe). |
| `github.com/puzpuzpuz/xsync/v4` | v4.5.0 (present) | The chunk→region map IF an off-region goroutine reads it concurrently with a region write; the global cross-region entity lookup if needed. | [VERIFIED: go.mod v4.5.0] Already a dep (added in Phase 8 / OPT-04). Apply the SAME justified-per-use discipline `async.go`/`collections_audit.go` established: default is snapshot-on-owner + plain map; reach for xsync ONLY where a genuine cross-thread concurrent read+write exists. |
| `github.com/panjf2000/ants/v2` | v2.12.1 (present) | The existing per-subsystem async pools (path/tracker/spawn). Regions can keep submitting to these; the pools are already global + bounded + non-blocking. | [VERIFIED: go.mod v2.12.1] Already a dep. The region threads become NEW submitters; the rejoin discipline (`applyTo` on the owner) must rejoin onto the OWNING region's thread, not a single tick (see Pitfalls). |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| stdlib `sync` | — | A plain `sync.WaitGroup` is a viable barrier if `conc` is rejected; `sync/atomic` for the shared gametime read; a `sync.Mutex`-guarded global player list if snapshot-read proves insufficient. | If the planner wants ZERO new deps, a `sync.WaitGroup`-based barrier replaces `conc`. `conc` is nicer (panic propagation, `pool.NewWithResults`) but is strictly optional. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `conc` barrier | raw `sync.WaitGroup` | No new dep, but manual panic handling (each region tick already has the `tickOnce` recover backstop, so panic propagation is less critical here). RECOMMEND `conc` for the cleaner join + the existing CLAUDE.md blessing, but note the planner may choose `WaitGroup` to avoid the dep. |
| Static chunk→region hash (RECOMMENDED for this phase) | Folia's dynamic `ThreadedRegionizer` (merge/split) | The dynamic regionizer is Folia's single most complex subsystem (region independence invariants, buffer chunks, adjacency-forbids-tick, eventual-merge). OUT OF SCOPE — start static, prove the seam. |
| Per-region `*host.Manager` dispatch context | One global Emit serialized behind a lock | A global lock on Emit would serialize the regions on every plugin event — defeating the parallelism. The frozen-registry + per-region-handle design avoids the lock entirely. |

**Installation (the only new dep):**
```bash
go get github.com/sourcegraph/conc@latest
```
**Version verification:** Run `go list -m -versions github.com/sourcegraph/conc` before pinning; confirm the latest v0.3.x and that it requires no Go version above 1.25.0 [ASSUMED: conc is pure-Go and Go-1.25-compatible — verify at plan time]. `ants` v2.12.1 and `xsync` v4.5.0 are already pinned [VERIFIED: go.mod].

## Architecture Patterns

### System Architecture Diagram

```
                         ┌──────────────────────────────────────────────┐
   inbound packets       │   GLOBAL DRIVER (the wall-clock anchor)       │
   (per-conn goroutine)  │   - accumulator/clamp (TICK-02, 50ms step)    │
        │                │   - gametime++ ONCE per logical step (shared) │
        ▼                │   - drainRegistrations (join/leave, console)  │
   chan Intent ──────────│   - EntityIDAllocator (global atomic)         │
                         │   - frozen *host.Manager registry (shared)    │
                         └───────────────────┬──────────────────────────┘
                                             │  per logical tick:
                                             │  fan out (conc / WaitGroup)
              ┌──────────────────────────────┼──────────────────────────────┐
              ▼                               ▼                               ▼
     ┌─────────────────┐            ┌─────────────────┐            ┌─────────────────┐
     │  REGION 0       │            │  REGION 1       │   ...      │  REGION N        │
     │  (goroutine)    │            │  (goroutine)    │            │  (goroutine)     │
     │  owns:          │            │  owns:          │            │  owns:           │
     │  - entityStore  │            │  - entityStore  │            │  - entityStore   │
     │  - ChunkManager │            │  - ChunkManager │            │  - ChunkManager  │
     │  - blockTicks   │            │  - blockTicks   │            │  - blockTicks    │
     │  - levelRandom  │            │  - levelRandom  │            │  - levelRandom   │
     │  tickWorld→AI→  │            │  tickWorld→AI→  │            │  tickWorld→AI→   │
     │  physics→async  │            │  physics→async  │            │  physics→async   │
     │  Emit(R0 hooks) │            │  Emit(R1 hooks) │            │  Emit(RN hooks)  │
     └────────┬────────┘            └────────┬────────┘            └────────┬─────────┘
              │  re-resolve handle.id        │                              │
              │  against THIS region's store │                              │
              └──────────────┬───────────────┴──────────────────────────────┘
                             │  BARRIER: all regions joined
                             ▼
              ┌──────────────────────────────────────────────┐
              │  CROSS-REGION / GLOBAL POST-PHASE             │
              │  (runs after the barrier, reads all regions)  │
              │  - entity tracker (player sees entities in    │
              │    adjacent regions — reads region snapshots) │
              │  - broadcastBlockUpdate / chat / playerlist   │
              │  - cross-region entity TRANSFERS applied      │
              │    (entity left R_a's column → add to R_b)    │
              │  - flushOutbound (per-player chunk stream)    │
              └──────────────────────────────────────────────┘
                             │
                             ▼  bounded Client.Send → writeLoop (sole socket writer)
                          clients
```

The load-bearing structure: a GLOBAL driver advances ONE gametime, fans out N region ticks in parallel, **barriers**, then runs the cross-region post-phase (tracker, broadcasts, transfers) on a single coordinator while all region goroutines are quiescent. This barrier is what makes the cross-region reads `-race` clean — no region is mutating its store during the cross-region read window.

### Component Responsibilities

| Concern | Type / file today | Phase 27 change |
|---------|-------------------|-----------------|
| The N region owners | NEW `region` struct | Each holds `entities *entityStore`, `world *ChunkManager`, `blockTicks`, `fluidSchedule`, `levelRandom`, `spawnScanPending`, and a region id. It is the per-region slice of today's `TickLoop` fields. |
| The global driver | `TickLoop` (`tick.go` `Run`/`advanceDraining`/`tickOnce`) | `TickLoop` becomes the GLOBAL coordinator: it keeps the accumulator, gametime, allocator, registrations, the frozen plugin registry, the global player list, and the async-pool ownership. `tickOnce` becomes "fan out region ticks → barrier → cross-region post-phase". |
| chunk→region map | NEW | A pure function `regionOf(col level.ChunkPos) regionID` (static hash for this phase) + a `map[regionID]*region`. If an off-region goroutine reads it, make the map `xsync`; if only the barriered coordinator reads it, a plain map suffices. |
| Region-aware Emit | `host.Manager.Emit` (`plugin/host/emit.go`) | UNCHANGED in `host` (it just walks the frozen hooks). The CHANGE is the call site: a region's entity-event Emit passes handles bound to `region` (re-resolving against that region's store), not the global loop. The handle struct gains a region reference instead of (or in addition to) `*TickLoop`. |
| Thin handles | `entityHandle`/`worldHandle`/`navHandle` (`plugin_entity.go`) | Today they carry `*TickLoop` + `id` and re-resolve `h.t.entities.get(id)`. They must re-resolve against the OWNING region's store. Minimal change: carry the `*region` (or a resolver that the region supplies) instead of `*TickLoop` for entity/nav reads; world reads (block_at/set_block) resolve against the region's `ChunkManager`. |

### Pattern 1: Region as a slice of the single-owner loop
**What:** A `region` struct is literally the per-world-state fields of today's `TickLoop`, extracted. The `TickLoop` keeps the global/coordination fields.
**When to use:** This phase, as the first move. It minimizes risk: each region keeps the EXACT single-owner discipline TICK-05 gives today, just scoped to its own chunks.
**Example:**
```go
// region owns the WORLD-half of today's TickLoop, scoped to its chunks. Exactly one
// goroutine (the region's tick goroutine) mutates these — TICK-05 multiplied by N.
type region struct {
    id          regionID
    entities    *entityStore                 // was TickLoop.entities
    world       *world.ChunkManager          // was TickLoop.world
    worker      *world.Worker                // per-region OR shared+region-filtered (Open Q)
    blockTicks  *ticks.LevelTicks[blockTickType]
    fluidSched  *fluidScheduleQueue
    levelRandom *levelgen.LegacyRandomSource // PER region — never shared (race + RNG divergence)
    spawnPending bool

    coord *TickLoop // back-ref to the GLOBAL coordinator (allocator, players, plugins, gametime)
}
```

### Pattern 2: The per-tick barrier (the conc fan-out/join)
**What:** The global `tickOnce` fans out a region-tick per region, waits for ALL to finish (barrier), then runs the cross-region post-phase.
**When to use:** Every logical tick.
**Anti-pattern it replaces:** Running the cross-region tracker/broadcast WHILE regions still tick (a race on the region stores).

### Pattern 3: Region-aware plugin Emit (frozen registry, region-resolved handles)
**What:** The plugin registry (`*host.Manager`, the frozen hooks) is GLOBAL and shared — the Phase-21 freeze makes the callables safe to call from any region thread. The HANDLES passed to a hook re-resolve against the CALLING region's store.
**When to use:** Every entity/world plugin event fired from inside a region tick (spawn, death, AI goal callback). The on_tick/global events fire once from the coordinator.

### Pattern 4: Cross-region entity transfer at the barrier
**What:** When a region's physics/AI moves an entity into a column owned by a DIFFERENT region, the move is detected (the column's `regionOf` changed) and the entity is queued for transfer. The transfer is APPLIED at the barrier (all regions quiescent): remove from region A's store, add to region B's store. The entity's AI/nav/plugin-goal scratch travels with the `*Entity` (it is a plain pointer the new region now owns).
**When to use:** Any entity crossing a region boundary.

### Anti-Patterns to Avoid
- **Designing the region seam first.** [CITED: v4-PLAN.md] The plan must regionize the WORKING Phase-22/23/24 seam, not redesign it. Phases 22–24 are done and green.
- **A shared `levelRandom` across regions.** Two goroutines drawing from one `LegacyRandomSource` is a data race AND makes the RNG stream non-deterministic per region.
- **Holding a `*Entity` across a region boundary.** The handles already never do this (id-carry + re-resolve). Preserve that — a transferred entity must be re-resolved by id, never aliased.
- **A global lock on Emit.** Serializes the regions. The frozen-registry design avoids it.
- **Letting a region tick read another region's live store.** Cross-region reads (tracker, broadcast) happen ONLY at the barrier when all regions are quiescent, OR via an immutable per-region snapshot.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-tick fan-out + join of N region ticks | A hand-rolled goroutine-spawn-and-channel-collect per tick | `conc` `WaitGroup`/`pool` (or stdlib `sync.WaitGroup`) | Structured join with panic propagation; spawning N raw goroutines per tick churns the scheduler. CLAUDE.md blesses `conc` for exactly this. |
| Cross-thread chunk→region map (if needed) | A `map` + a hand-rolled `sync.RWMutex` | `xsync.Map` | Already a dep; CLHT map is obstruction-free on reads, far better under the region read pattern than RWMutex. ONLY if a genuine concurrent read+write exists (else plain map at the barrier). |
| Async work rejoin onto the owning region | A new bespoke rejoin channel design | The EXISTING `asyncResult`/`applyTo` discipline (`async.go`) | The id-carry + owner-re-resolve + drop-if-gone pattern is already proven `-race` clean. Regionize it: the rejoin targets the OWNING region's `applyAsyncResults`, not one global tick (see Pitfalls). |
| Bounded recycling worker pool for region async | A goroutine-per-task | The existing `ants` pools | Already wired, bounded, non-blocking, drop-on-overload. |
| The dynamic regionizer (merge/split) | A reimplementation of Folia's `ThreadedRegionizer` | NOTHING — out of scope. Static hash. | Folia's regionizer is its single most complex subsystem with subtle independence invariants. A static 2+ region hash proves REGION-01 without it. |

**Key insight:** Sulfur already paid the architectural cost of regionization in Phases 8 and 23. The async rejoin pattern (`async.go`) and the thin-id handles (`plugin_entity.go`) are the cross-thread-safety primitives Folia needs. Phase 27 is mostly **multiplying the single-owner store by N and routing handles/Emit/async-rejoin to the right N** — not inventing new safety machinery.

## Runtime State Inventory

Phase 27 is a re-homing/refactor of ownership, not a rename — but the Folia transfer model means there IS runtime state to inventory. The question "after the code splits the store into N regions, what state still assumes ONE owner?":

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | Persisted chunks (`save/` region files) and `playerdata/<uuid>.dat` are addressed by world position / uuid — NO embedded region id. A static chunk→region hash means the SAME chunk always maps to the same region across restarts (good). | Code edit only (the region mapping is computed, not stored). If the region count changes between runs, in-flight entities/chunks re-hash deterministically — no data migration. **Confirm the hash is stable across restarts.** |
| Live service config | None — Sulfur has no external service config. | None — verified: no n8n/Datadog/external registrations in this codebase. |
| OS-registered state | None — no OS-level task/process registrations tied to region identity. | None. |
| Secrets/env vars | `SULFUR_DEBUG`, `SULFUR_ULTRA_DEBUG` env toggles read by `tickDebug`/`tickUltraDebug` (`tick_phases.go`). These run per-player; after regionization the debug pig must be owned by SOME region. | Code edit: the debug seam must pick a region (or the coordinator) to own the debug entity. Not a data migration. |
| Build artifacts | The embedded `vanilla_pig` plugin (`server/assets/vanilla_pig/main.star`, `vanilla_pig_embed.go`) is loaded ONCE into the frozen registry. The frozen registry stays global — no per-region copy. | None — the registry is shared-frozen by design. Verify the boot-load + `SetMobRegistry` happen ONCE globally before any region ticks. |

**The canonical question for this phase:** *after the store is split into N regions, what code still calls `t.entities`/`t.world` as if there is ONE?* That is the real "inventory" — grep for every `t.entities.` and `t.world.` reference and classify each as (a) per-region-now, (b) cross-region-read-at-barrier, or (c) global. The high-traffic call sites: `tick_phases.go` (the whole pipeline), `tracker.go` (cross-region), `spawner.go` (`countByCategory` iterates `t.entities.byID` — becomes per-region cap counting), `block_interact.go` (`broadcastBlockUpdate` iterates `t.players` — cross-region), `plugin_entity.go` (the handles — re-resolve per region), `async.go` (the rejoin `applyTo` — must target the owning region).

## Common Pitfalls

### Pitfall 1: The async rejoin lands on the WRONG region (or one global tick)
**What goes wrong:** Today `applyAsyncResults` drains `asyncIn`/`asyncIn2` on THE tick goroutine and applies each `asyncResult.applyTo(t)`. A path computed off-tick for a mob in region B must rejoin on region B's thread, not on a single global tick — else the apply mutates B's store from the wrong goroutine (a data race) OR the result targets a store that no longer owns the entity.
**Why it happens:** The `asyncResult` interface is `applyTo(*TickLoop)` — a single owner. Regionization breaks that signature's assumption.
**How to avoid:** Make the rejoin region-routed. Either (a) per-region `asyncIn2` channels drained by each region's `applyAsyncResults`, with the result carrying its origin region id; or (b) the result re-resolves the entity by id at the barrier and is applied to whatever region currently owns it (matching the existing "drop if gone / re-resolve on owner" discipline). The existing `pathReady.applyTo` already re-resolves `t.entities.get(r.mobID)` and drops if gone — extend that to "find the owning region, apply there, drop if no region owns it."
**Warning signs:** `-race` flags on the `entityStore` maps; a mob's path applied to a region that doesn't contain it (silent no-op / stuck mob).

### Pitfall 2: The entity tracker is inherently cross-region and cannot be region-local
**What goes wrong:** `tracker.Tick()` (and `asyncTracker`) loops over ALL players and calls `t.entities.near(p.x, p.z, trackRange)` (trackRange=6 columns ≈ 96 blocks). A player near a region boundary must see entities in the ADJACENT region. If `near()` only queries the player's own region store, entities across the boundary vanish from view.
**Why it happens:** Visibility radius (96 blocks) is larger than nothing and crosses region boundaries; the tracker is a global concern, not a per-region one.
**How to avoid:** Run the tracker at the BARRIER (all regions quiescent), and make `near()` query span the regions the radius touches — either by iterating the regions whose columns intersect the query box and reading each region's (now-stable) store, or by building a per-region immutable entity snapshot at the barrier and querying across snapshots. The existing `asyncTracker` already snapshots entities into VALUE structs before going off-tick — extend that snapshot to be a cross-region snapshot built at the barrier.
**Warning signs:** Entities flicker/disappear when a player stands near a region seam.

### Pitfall 3: Game-time MUST stay one shared anchor across all regions
**What goes wrong:** If each region runs its own accumulator/gametime, the regions drift — block ticks, mob spawn cadence, day/night, and any gametime-keyed behavior diverge between regions, breaking vanilla observability AND the 1:1 mandate.
**Why it happens:** The naive "each region has its own main thread" reading of Folia suggests per-region timing. Folia regions DO each tick at 20 TPS independently, but Sulfur's CLAUDE.md decision is explicit: gametime is wall-clock-anchored to 50ms and shared.
**How to avoid:** Keep ONE global accumulator + gametime in the coordinator (`TickLoop`). The coordinator decides "advance one logical tick", then fans out the regions for THAT tick number. Each region reads the shared gametime (atomic or passed as a tick param). Scheduled block ticks in every region compare against the same gametime. [CITED: CLAUDE.md "game-time-anchored-to-50ms"]
**Warning signs:** Two regions report different gametime; spawn/growth cadence differs across the seam.

### Pitfall 4: The plugin handle re-resolves against the wrong region's store
**What goes wrong:** A hook for an entity in region R receives an `entityHandle{t: globalLoop, id: 42}`. If the handle re-resolves `globalLoop.entities.get(42)` but entity 42 lives in R's per-region store (not a global store), the read returns "entity no longer exists" — the hook silently sees a dead entity.
**Why it happens:** The handle carries `*TickLoop` and resolves against `t.entities`, which no longer exists as one global store.
**How to avoid:** The handle must resolve against the OWNING region's store. Bind the handle to the `*region` (or a resolver closure the region supplies) when the region fires the Emit. Because the AI goal callback already receives the live `*Entity` from `serverAiStep` (`plugin_mob_ai.go` `handles(e)`), the region context is available at handle-build time — thread the region through. World handles (`block_at`/`set_block`) resolve against the region's `ChunkManager`; a `set_block` that targets a column in ANOTHER region must marshal (or be rejected/deferred) — flag as an Open Question.
**Warning signs:** Plugin reads of a live mob's position return errors; declared-mob goals no-op.

### Pitfall 5: State that genuinely cannot be regionized, regionized anyway
**What goes wrong:** The `EntityIDAllocator` (must be ONE global id space so players + entities never collide), the player connection/session (spans regions), the frozen plugin registry (load-once-shared), and the network writeLoop (per-connection, sole socket writer) are GLOBAL. Splitting them per-region breaks id uniqueness, player continuity, or the frozen guarantee.
**Why it happens:** Over-eager "everything per region."
**How to avoid:** Use the Responsibility Map above as the contract. The allocator stays one global atomic (it already crosses no game state — `entity_store.go`). The player list stays global; a region "ticks the player" by affinity to the player's chunk, but the session/socket is global. The plugin registry stays global+frozen.
**Warning signs:** Entity id collisions; a player walking across a seam loses its session; a frozen-module race.

### Pitfall 6: `broadcastBlockUpdate` / chat / playerlist read the global player list from a region thread
**What goes wrong:** `broadcastBlockUpdate` (`block_interact.go`) and chat/playerlist iterate `t.players` and call `pl.client.Send`. If a region thread does this mid-tick while the coordinator mutates `t.players` (a join/leave), it races.
**Why it happens:** These are global broadcasts triggered from within region-owned events (a block placed by a plugin in region R).
**How to avoid:** Either (a) defer the broadcast to the barrier/coordinator (collect "broadcast intents" per region, flush at the barrier), or (b) make the player list read-only during region ticks (registrations applied only at the barrier, as `drainRegistrations` already runs before stepping). The existing design already drains registrations BEFORE `tickOnce` — preserve that: the player list is stable during the region fan-out, so a region thread reading it (not mutating) is safe.
**Warning signs:** `-race` on `t.players`; a Send to a just-removed player.

## Code Examples

### The region tick loop + barrier (the coordinator's new tickOnce)
```go
// tickOnce becomes: advance ONE shared gametime, fan out the regions in parallel,
// barrier, then run the cross-region post-phase while every region is quiescent.
// The accumulator/gametime/registrations stay in the global coordinator (TICK-02 anchor).
func (t *TickLoop) tickOnce() {
    start := t.clock.Now()
    defer t.recoverTick() // the existing per-tick recover backstop, hoisted

    gt := t.gametime // the ONE shared tick number every region sees this tick (Pitfall 3)

    // --- FAN OUT: each region ticks its own store in parallel (TICK-05 per region) ---
    var wg conc.WaitGroup // or sync.WaitGroup if conc is rejected
    for _, r := range t.regions {
        r := r
        wg.Go(func() { r.tick(gt) }) // r.tick = the old per-region pipeline:
                                     // tickWorld→tickEntities→tickAI→tickPhysics→
                                     // applyAsyncResults(region)→region Emit
    }
    wg.Wait() // BARRIER: no region mutates its store past this point this tick

    // --- CROSS-REGION POST-PHASE (all regions quiescent → safe to read across) ---
    t.applyCrossRegionTransfers() // entities that crossed a boundary: remove A, add B
    t.tracker.Tick()             // visibility diff: reads across region snapshots (Pitfall 2)
    t.flushBroadcasts()          // deferred broadcastBlockUpdate/chat (Pitfall 6)
    t.flushOutbound()            // per-player chunk stream (global player list)

    t.gametime++ // EXACTLY once per logical tick — the shared anchor (TICK-02)
    if t.plugins != nil {
        t.plugins.Emit(host.EventTick, host.TickEvent{Tick: int(t.gametime)}) // ONE global on_tick
    }
    t.recordMSPT(t.clock.Now().Sub(start))
}
```

### A region-aware Emit (frozen registry, region-resolved handles)
```go
// A region fires an entity-scoped plugin event from INSIDE its tick. The registry
// (t.plugins) is GLOBAL + frozen (Phase 21 safe-across-threads); the handles re-resolve
// against THIS region's store (Pitfall 4). callback receives the region context.
func (r *region) emitEntityEvent(evt host.EventType, e *Entity, payload host.Event) {
    if r.coord.plugins == nil {
        return // no plugins loaded: cheap no-op (the existing guard)
    }
    // The frozen callables are shared; calling them from r's goroutine is safe BECAUSE
    // they are frozen (Phase 21 guarantee). The handles bind to r, so every read inside
    // the hook re-resolves r.entities.get(id) — the OWNING region's store.
    r.coord.plugins.Emit(evt, payload) // payload carries frozen scalars + region-bound handles
}

// the handle change: resolve against the region, not the global loop.
func (h *entityHandle) resolve() (*Entity, error) {
    e, ok := h.region.entities.get(h.id) // was h.t.entities.get(h.id)
    if !ok {
        return nil, fmt.Errorf("entity %d no longer exists", h.id)
    }
    return e, nil
}
```

### A cross-region entity transfer (applied at the barrier)
```go
// During a region's physics/AI, a move that crosses a region boundary is DETECTED and
// QUEUED (not applied mid-tick — that would mutate two region stores from one goroutine).
// The region records the intent; the coordinator applies it at the barrier.
func (r *region) detectTransfer(e *Entity) {
    newRegion := regionOf(columnOf(e.x, e.z)) // the static chunk→region hash
    if newRegion != r.id {
        r.pendingTransfers = append(r.pendingTransfers, transferIntent{ent: e, to: newRegion})
    }
}

// At the barrier (all regions quiescent), the coordinator moves each entity between stores.
// The *Entity (with its ai/nav/plugin-goal scratch) is a plain pointer the new region adopts.
func (t *TickLoop) applyCrossRegionTransfers() {
    for _, r := range t.regions {
        for _, ti := range r.pendingTransfers {
            r.entities.remove(ti.ent.id)        // out of the old region's store + bucket
            t.regions[ti.to].entities.add(ti.ent) // into the new region's store + bucket
        }
        r.pendingTransfers = r.pendingTransfers[:0]
    }
}
```

## State of the Art

| Old Approach (Sulfur today) | Phase 27 Approach | When Changed | Impact |
|-----------------------------|-------------------|--------------|--------|
| ONE `TickLoop` goroutine owns every entity/chunk/player/hook (TICK-05) | N region goroutines, each owns its chunks' entities/world; a global coordinator owns timing/allocator/registry/player-list | Phase 27 | The world ticks in parallel; per-tick cost on a multi-region world drops toward `total_work / num_regions` (the perf payoff, gated in Phase 28). |
| Plugin Emit runs on THE tick goroutine; handles resolve `t.entities` | Emit runs on the OWNING region's goroutine; handles resolve the region's store | Phase 27 | A hook for an entity in R runs on R's thread (the REGION-01 gate). |
| `asyncResult.applyTo(*TickLoop)` rejoins one tick | Async rejoin routes to the owning region | Phase 27 | Path/tracker/spawn results land on the right region thread. |

**Folia reference (the model Sulfur mirrors, simplified):**
- Folia groups nearby loaded chunks into independent regions; each has its own tick loop at 20 TPS on a thread pool; there is no main thread. [CITED: paper-chan.moe/folia, docs.papermc.io/folia/reference/overview]
- Region size = `gridExponent` (default 4 → 16×16 chunks per region). Sulfur should start with a smaller fixed grid (or 2 regions) to prove the seam. [CITED: deepwiki.com/PaperMC/Folia]
- The thread-ownership rule: only the thread currently ticking a region may access that region's data (validated by `TickThread.isTickThreadFor()`). This IS TICK-05 multiplied by N. [CITED: deepwiki.com/PaperMC/Folia]
- The `GlobalTickThread` owns console execution, player connection handshaking, global plugin tasks, and shutdown — it owns NO chunks/entities. Sulfur's coordinator plays this role. [CITED: deepwiki.com/PaperMC/Folia]
- Region independence invariants (a ticking region can't grow, must own a buffer of perimeter chunks, can't tick adjacent to another region, adjacent regions must eventually merge) are part of the DYNAMIC regionizer — OUT OF SCOPE for the static-hash Phase 27. [CITED: paper-chan.moe/folia]

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `conc` is pure-Go and compatible with Go 1.25.0 / CGO_ENABLED=0 | Standard Stack | If `conc` pulled cgo (it does not, to my knowledge), it would violate the CGO=0 mandate. Verify `go get` + a `CGO_ENABLED=0 go build` at plan time. LOW risk — conc is well-known pure-Go. The `sync.WaitGroup` fallback removes the risk entirely. |
| A2 | A static chunk→region hash is stable across restarts (same chunk → same region) | Runtime State Inventory | If the hash depends on region COUNT and the count changes between runs, entities/chunks re-home deterministically but DIFFERENTLY — acceptable for a static world but confirm no persisted state embeds a region id. Verified no region id is persisted (chunks keyed by position, players by uuid). |
| A3 | Folia applies cross-region entity/chunk transfer between ticks (at a quiescent point), not mid-tick | Architecture Patterns / Code Examples | DeepWiki did not detail the exact transfer mechanism (it said "specifics aren't provided"). The barrier-apply design is the SAFE choice regardless of Folia's exact internals — it does not depend on matching Folia bit-for-bit (REGION-01 is "Folia-style", not "Folia-identical"). MEDIUM confidence on Folia's exact mechanism; HIGH that the barrier-apply is correct for Sulfur. |
| A4 | Folia regions each run an independent 20 TPS loop, but Sulfur deliberately shares ONE gametime anchor | Pitfall 3 | This is a deliberate DIVERGENCE from Folia, mandated by CLAUDE.md's 50ms-anchor decision. If a future requirement wanted per-region independent timing, this would need revisiting. Locked by CLAUDE.md for now. |
| A5 | The per-region `worker` (chunk gen) can be shared (region-filtered) or per-region | Pattern 1 / Open Questions | The chunk worker + `singleflight` dedup currently assume one manager. Whether to give each region its own worker or share one filtered by region is unresolved — flagged as an Open Question, not assumed. |

## Open Questions

1. **Region granularity / count for the proof.**
   - What we know: Folia uses 16×16-chunk regions via `gridExponent`. The gate needs "2+ regions tick concurrently."
   - What's unclear: Whether to start with exactly 2 fixed regions (simplest proof) or a small fixed grid (e.g. 2×2 = 4 regions).
   - Recommendation: Start with a **2-region split** (e.g. `regionOf = col.X >= 0 ? 1 : 0`, or a hash mod 2) to prove the seam + barrier + transfer + region-aware Emit with the least machinery, then generalize to a grid. The gate only requires 2 concurrent regions.

2. **The global region's scope — what exactly runs on the coordinator vs a region.**
   - What we know: Folia's GlobalTickThread owns console, connection handshaking, global plugin tasks, shutdown. Sulfur's coordinator owns timing/allocator/registry/player-list/registrations.
   - What's unclear: Where does a console command that mutates a SPECIFIC region's world run? (Folia marshals it onto that region's thread.) Where does `on_tick` fire — once globally (current design) or once per region?
   - Recommendation: `on_tick` fires ONCE globally from the coordinator (matches today's single emit + the "not per-entity" rule). A console command targeting a region's chunk is marshalled onto that region's thread (or deferred to its next tick via a per-region command queue, mirroring the existing `consoleCmd` drain).

3. **The chunk worker + singleflight under regions.**
   - What we know: One `world.Worker` + `singleflight` dedups concurrent chunk-gen today; each region owns its `ChunkManager`.
   - What's unclear: Per-region worker (clean ownership, more goroutines) vs one shared worker whose results route to the owning region (fewer goroutines, needs region-routed rejoin — Pitfall 1).
   - Recommendation: Share ONE worker; route each `chunkReady` result to the owning region's `applyAsyncResults` (the result already carries `res.Pos`, so `regionOf(pos)` picks the region). This reuses the existing bounded worker + singleflight unchanged.

4. **A `set_block` / interaction that targets a column in ANOTHER region.**
   - What we know: `worldHandle.setBlock` resolves against the region's `ChunkManager`. A plugin in region A could request a write in region B's chunk.
   - What's unclear: Reject, defer to the barrier, or marshal onto B's thread.
   - Recommendation: For the proof, the region's `ChunkManager` returns "unloaded/not-owned" for a foreign column (the existing `SetBlock` already no-ops an unloaded column — a foreign column is simply not in this region's manager, so it cleanly no-ops). Document this as a known limitation; a cross-region write seam is a follow-up.

5. **The safest incremental path.**
   - What we know: This is the most invasive change in v4 (re-homes the discipline the whole server rests on).
   - Recommendation: (1) Extract the `region` struct holding the world-owned fields, with N=1 (one region == today's behavior, no parallelism, `-race` still green — proves the extraction is behavior-neutral). (2) Add the coordinator fan-out/barrier with N=1 (still serial, still green). (3) Flip to N=2 with the static hash; add cross-region transfer + the cross-region tracker read + region-aware Emit. (4) Run the parallel-regions gate + the hook-on-correct-region gate + `-race`. Each step is independently `-race`-verifiable.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `github.com/sourcegraph/conc` | The per-tick barrier (RECOMMENDED) | ✗ (not in go.mod) | — | stdlib `sync.WaitGroup` (no new dep) |
| `github.com/puzpuzpuz/xsync/v4` | Cross-thread chunk→region map (if needed) | ✓ | v4.5.0 | plain map at the barrier |
| `github.com/panjf2000/ants/v2` | Existing async pools (region submitters) | ✓ | v2.12.1 | — |
| Go toolchain | Build | ✓ | 1.25.0 | — |
| Docker `-race` runner | The non-negotiable race gate | ✓ (used by existing gates) | — | `go test -race` locally |

**Missing dependencies with fallback:**
- `conc` — fallback is stdlib `sync.WaitGroup`. The phase is not blocked; the planner chooses `conc` (cleaner, CLAUDE.md-blessed) or `WaitGroup` (zero new dep).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `-race`) — the existing suite (`*_test.go` throughout `server/`, `world/`, `plugin/`) |
| Config file | none (standard `go test`) |
| Quick run command | `go test -race ./server/... -run TestRegion` |
| Full suite command | `CGO_ENABLED=1 go test -race ./...` (race needs cgo for the race detector ONLY; the default BUILD stays CGO=0) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REGION-01 | 2+ regions tick concurrently (parallelism proven, not just N=1 serial) | unit/integration | `go test -race ./server/... -run TestRegionsTickInParallel` | ❌ Wave 0 |
| REGION-01 | The whole world advances one shared gametime per logical tick across all regions | unit | `go test ./server/... -run TestSharedGameTimeAcrossRegions` | ❌ Wave 0 |
| REGION-01 | An entity walking A→B transfers ownership; its AI/nav/plugin scratch survives | unit | `go test -race ./server/... -run TestCrossRegionTransfer` | ❌ Wave 0 |
| REGION-01 | A plugin hook for an entity in region R runs on R's goroutine (gate) | unit/integration | `go test -race ./server/... -run TestHookRunsOnOwningRegion` | ❌ Wave 0 |
| REGION-01 | A player near a region seam still sees entities across the boundary (tracker cross-region) | unit | `go test ./server/... -run TestTrackerSeesAcrossRegions` | ❌ Wave 0 |
| REGION-01 | Behavior-neutral extraction: N=1 region == pre-regionization behavior (the existing mob-AI/physics/tracker tests still green) | regression | `CGO_ENABLED=1 go test -race ./...` | ✅ (existing suite) |
| REGION-01 | `-race` clean across the region threads | race | `CGO_ENABLED=1 go test -race ./server/...` | ✅ (gate exists; new region tests join it) |

### Sampling Rate
- **Per task commit:** `go test -race ./server/... -run TestRegion` (the new region tests)
- **Per wave merge:** `CGO_ENABLED=1 go test -race ./server/...` (the whole server package under race)
- **Phase gate:** `CGO_ENABLED=1 go test -race ./...` green + the existing mob-AI/physics/tracker/plugin tests green (proving behavior-neutral regionization) before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `server/region_test.go` — `TestRegionsTickInParallel` (the parallelism proof — e.g. each region's tick records its goroutine id / a concurrency-detecting probe), `TestSharedGameTimeAcrossRegions`, `TestCrossRegionTransfer`, `TestTrackerSeesAcrossRegions`.
- [ ] `server/region_plugin_test.go` — `TestHookRunsOnOwningRegion` (the REGION-01 gate: an entity in R, its declared-mob goal callback observes R's goroutine / R's store). Mirror the existing `plugin_pig_race_test.go` / `plugin_mob_race_test.go` structure.
- [ ] Framework install (if `conc` is chosen): `go get github.com/sourcegraph/conc@latest` — else no install (use `sync.WaitGroup`).
- Existing test infrastructure (the `server` package suite, the `-race` gate, the plugin-pig tests) already covers the behavior-neutral regression — the new files are ADDITIVE.

## Security Domain

`security_enforcement` is not set in config.json [VERIFIED: config.json has only `nyquist_validation: true`]; absent = enabled, so this section is included. Phase 27 is an internal concurrency refactor with NO new external attack surface (no new network input, no new auth/session, no new untrusted parsing), so most ASVS categories are N/A.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | No auth change (login/session unchanged; the session stays global). |
| V3 Session Management | partial | The player session spans regions — it stays GLOBAL (not per-region). Mis-regionizing the session could let one player's region thread touch another's session (a logic bug, not a classic auth flaw). Control: keep `tickPlayer`/`Client` global (Responsibility Map). |
| V4 Access Control | partial | The plugin capability gate (`plugin_capability.go`, `capSet`) MUST survive regionization: a region-bound handle still enforces the owning plugin's capabilities at the handle-op boundary. Control: thread `caps` through the region-bound handle unchanged. |
| V5 Input Validation | no | No new input parsing. The existing defensive dispatch is unchanged. |
| V6 Cryptography | no | None. |

### Known Threat Patterns for {region-threaded Go server}
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Data race on a region store from the wrong goroutine | Tampering | TICK-05-per-region: only the region's goroutine mutates its store; cross-region reads only at the barrier. The `-race` gate is the enforcement. |
| Async result applied to the wrong region | Tampering | Region-routed rejoin + re-resolve-by-id + drop-if-gone (the existing `applyTo` discipline). |
| A plugin handle escaping its capability set across regions | Elevation of Privilege | The `capSet` is carried by the handle, checked at every handle op; regionization carries it unchanged. |
| A region thread reading the global player list while it mutates (join/leave) | Tampering | Registrations applied only at the barrier (before fan-out, as `drainRegistrations` already runs pre-step); the player list is read-only during the region fan-out. |
| Goroutine blowup (a goroutine per region per tick) | Denial of Service | Bounded fan-out via `conc`/`WaitGroup` (reuse) + the existing bounded `ants` pools; never spawn-per-entity. |

## Sources

### Primary (HIGH confidence)
- Sulfur codebase (read this session): `server/tick.go` (TickLoop, Run/advanceDraining/tickOnce, drainRegistrations, dispatch), `server/tick_phases.go` (the fixed phase pipeline, applyAsyncResults), `server/async.go` (the asyncResult/applyTo rejoin discipline, the ants pools), `server/entity_store.go` (entityStore add/get/move/near, EntityIDAllocator), `world/manager.go` (ChunkManager), `server/tracker.go` (entityTracker + asyncTracker — the cross-region concern), `server/plugin_entity.go` (the thin-id entity/world/nav handles), `server/plugin_mob_ai.go` (starlarkGoal + buildAIFromDecl), `plugin/host/emit.go` + `plugin/host/manager.go` (Emit + the frozen registry), `server/ai_goal.go` (the goal selector), `server/spawner.go` + `server/block_interact.go` + `server/ai_goals_passive.go` (countByCategory / broadcastBlockUpdate / nearestPlayerAt — cross-region call sites).
- `go.mod` [VERIFIED]: Go 1.25.0; `ants/v2 v2.12.1`, `xsync/v4 v4.5.0` present; `conc` ABSENT.
- `.planning/REQUIREMENTS.md` (REGION-01), `.planning/v4-PLAN.md` (Phase 27 row + gate + build-order rationale), `CLAUDE.md` (the concurrency stack decisions, TICK-05, the 50ms anchor, the 1:1 mandate).

### Secondary (MEDIUM confidence)
- PaperMC Folia overview — region grouping, per-region 20 TPS tick loops, no main thread, thread-ownership rule, GlobalTickThread scope. [CITED: paper-chan.moe/folia, docs.papermc.io/folia/reference/overview, deepwiki.com/PaperMC/Folia/2-region-threading-system]

### Tertiary (LOW confidence)
- Folia's EXACT cross-region transfer mechanism + the precise barrier/merge/split internals — DeepWiki explicitly stated specifics "aren't provided" for the transfer and synchronization mechanics. The barrier-apply transfer design here is the safe Sulfur-appropriate choice, NOT a verified copy of Folia's internals (and REGION-01 is "Folia-style", not "Folia-identical").

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — go.mod verified; conc/xsync/ants roles are CLAUDE.md-blessed; the one new dep (conc) is flagged with a stdlib fallback.
- Architecture (responsibility map, region/barrier/transfer/region-aware-Emit): HIGH — derived directly from the read Sulfur code; the per-region-vs-global split is grounded in the actual field-by-field TickLoop structure.
- Folia model: MEDIUM — the high-level model (regions, per-region tick, ownership rule, global thread) is well-sourced; the exact transfer/barrier internals are LOW (DeepWiki gaps), but the phase is "Folia-style" and the barrier design does not depend on matching Folia bit-for-bit.
- Pitfalls: HIGH — each is grounded in a specific Sulfur call site (the async rejoin signature, the cross-region tracker, the handle re-resolve, the global broadcasts, the shared gametime decision).

**Research date:** 2026-06-28
**Valid until:** 2026-07-28 (30 days — the Sulfur code map is stable; the Folia reference is stable; the only volatile item is the `conc` version, verify at plan time)
