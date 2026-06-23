# Architecture Research

**Domain:** Minecraft Java Edition server (26.2, proto 776) reimplemented in Go on Tnze/go-mc, concurrency-ready for Leaf-style async optimizations
**Researched:** 2026-06-23
**Confidence:** HIGH on vanilla/Leaf/Folia architecture and go-mc package boundaries (verified against DeepWiki Paper/Folia/Leaf wikis, pkg.go.dev, GitHub); MEDIUM on exact Go-idiomatic mapping (synthesized, not a port-of-record exists)

## Executive Take

The whole architecture hinges on one invariant: **the tick is the unit of authority, and game state must only be mutated by the goroutine that owns it.** Vanilla single-threads everything. Folia keeps the single-threaded *correctness* guarantee but shards it per-region. Leaf keeps the single main thread but moves *pure computation* (pathfinding, tracked-set diffing, brain ticks) off-thread and rejoins results into the tick.

For Ender, the right move is: **build a single-threaded authoritative tick loop with explicit "compute off, apply on" seams from day one**, never letting off-tick code touch live game state directly. If those seams exist, Leaf optimizations bolt on without rewrites. If you bake locks into game state instead, you will rewrite. The design choice that prevents rewrites is **ownership-based synchronization (the Folia/Leaf model), not lock-based shared state.**

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                          NETWORK EDGE (per-connection goroutines)      │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────────────────┐    │
│  │ TCP Listener │──▶│ Conn goroutine│──▶│ Packet Codec (net pkg)   │    │
│  │  (accept)    │   │ read / write  │   │ VarInt/compress/encrypt  │    │
│  └──────────────┘   └──────┬───────┘    └──────────┬───────────────┘    │
│                            │ State Machine: Handshake│ Status Login     │
│                            │            Config Play   │                  │
├────────────────────────────┼──────────────────────────┼─────────────────┤
│            inbound packets ▼ (channel)      outbound ▲ (channel)         │
├──────────────────────────────────────────────────────────────────────┤
│                    AUTHORITATIVE TICK LOOP (20 TPS, owns game state)    │
│   ┌───────────────────────────────────────────────────────────────┐    │
│   │ 1.drain inbound 2.world tick 3.entity tick 4.AI/brain 5.physics │    │
│   │ 6.apply async results 7.entity tracking 8.flush outbound        │    │
│   └─────────┬──────────────┬──────────────┬───────────────┬─────────┘    │
│             │ owns         │ owns         │ owns          │ owns         │
│   ┌─────────▼───┐ ┌────────▼─────┐ ┌──────▼──────┐ ┌──────▼───────┐      │
│   │ World Mgr   │ │ Chunk System │ │ Entity Sys  │ │ Command      │      │
│   │ (dimensions)│ │ load/gen/view│ │ store + tick│ │ Dispatch     │      │
│   └─────┬───────┘ └──────┬───────┘ └──────┬──────┘ └──────────────┘      │
├─────────┼────────────────┼────────────────┼─────────────────────────────┤
│         │ RESULT QUEUES (off-thread compute → apply on tick) — SEAMS     │
│  ┌──────▼──────┐  ┌───────▼────────┐  ┌────▼─────────┐                    │
│  │ async path  │  │ async tracker  │  │ async spawn  │  (Leaf layers)    │
│  │ goroutine   │  │ goroutine pool │  │ goroutine    │   added LATER     │
│  │ pool        │  │                │  │              │                    │
│  └─────────────┘  └────────────────┘  └──────────────┘                    │
├──────────────────────────────────────────────────────────────────────┤
│                    PERSISTENCE (save/region — off-tick I/O)             │
│   ┌──────────────────┐  ┌───────────────────┐  ┌───────────────────┐   │
│   │ Region/MCA files │  │ player data (NBT) │  │ level.dat         │   │
│   │ (save pkg)       │  │                   │  │                   │   │
│   └──────────────────┘  └───────────────────┘  └───────────────────┘   │
└──────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility (what it owns) | go-mc coverage | Built by Ender |
|-----------|-------------------------------|----------------|----------------|
| **Connection/Session Manager** | TCP accept, per-conn goroutine, read/write loops, compression & encryption thresholds, keepalive, connection lifecycle | `net` (codec), `server` (login/keepalive/playerlist framework) | Session-to-player binding, play-state loop |
| **Packet Codec Layer** | VarInt/VarLong, packet length framing, zlib compression, AES-CFB8 encryption, serialize/deserialize typed packets | `net`, `net/packet`, `CFB8` | Generated 776 packet structs (PR #294-296 codegen) |
| **Protocol State Machine** | Handshake → Status / Login → Config → Play transitions; routes packets to the right handler set per state | `net` has state constants; `server` has login flow | Config-state handler, Play-state handler, transition logic |
| **World Manager** | Owns set of dimensions/levels, world rules, time, weather; routes by dimension | `save.LevelData` (level.dat struct) | Runtime world container, dimension registry |
| **Chunk System** | Load/generate/store chunk columns, BitStorage block states, heightmaps, view-distance chunk streaming to clients, ticket/loading | `level`/`level/block`/`chunk` data structures; `save` MCA region I/O | Chunk manager (ticket system), generator, view tracking |
| **Entity System + Tick** | Entity registry (id→entity), per-entity tick, position/velocity, spawn/despawn, metadata sync | none | Full system |
| **AI / Brain** | Goal selectors or brain (memories + behaviors + sensors), pathfinding requests, target selection | none | Full system |
| **Physics** | Gravity, AABB collision (entity↔block, entity↔entity), block place/break, fluid, knockback | none | Full system |
| **Command Dispatch** | Parse Brigadier-style command tree, execute, permissions, chat routing | `chat` (message format) | Command tree + dispatch |
| **Persistence** | Region/MCA read/write, player NBT, level.dat, async flush | `save`, `save/region`, `nbt` | Async scheduling, dirty-tracking |

**Critical boundary:** Everything from World Manager down to Command Dispatch is **game state owned by the tick loop**. The Network Edge and the Persistence layer are the *only* components that legitimately run on other goroutines, and they communicate with the tick loop **exclusively through channels/queues**, never by touching game state. This boundary is the concurrency seam that makes Leaf optimizations possible later.

## Recommended Project Structure

```
ender/
├── cmd/ender/              # main: wire up server, load config, start tick loop
├── internal/
│   ├── net/                # thin wrappers / extensions over go-mc net
│   │   ├── conn.go         # per-connection goroutine, read/write channels
│   │   └── codec.go        # compression/encryption setup
│   ├── protocol/
│   │   ├── packet/         # GENERATED 776 packet structs (codegen output)
│   │   ├── state.go        # Handshake/Status/Login/Config/Play state machine
│   │   ├── handshake.go
│   │   ├── login.go        # offline-mode first; encryption later
│   │   ├── config.go       # registries, known packs
│   │   └── play.go         # play-state inbound packet routing
│   ├── server/             # Server struct, session registry, player list
│   │   ├── server.go
│   │   ├── session.go      # binds conn <-> player; inbound/outbound channels
│   │   └── tick.go         # THE authoritative tick loop (20 TPS scheduler)
│   ├── world/
│   │   ├── world.go        # World/dimension manager, owned by tick
│   │   ├── chunk/          # chunk manager, ticket system, view tracking
│   │   ├── gen/            # world generation (deterministic first)
│   │   └── block/          # block state behavior (uses generated block data)
│   ├── entity/
│   │   ├── entity.go       # entity store + interface/component split
│   │   ├── tick.go         # entity tick orchestration
│   │   ├── tracker.go      # entity tracking (sync first; async seam)
│   │   └── metadata.go     # data components / entity metadata sync
│   ├── ai/
│   │   ├── brain.go        # memories/behaviors/sensors OR goal selectors
│   │   ├── pathfind/       # A* navigation (sync first; async seam)
│   │   └── spawn/          # mob spawning (sync first; async seam)
│   ├── physics/            # gravity, AABB collision, fluids
│   ├── command/            # Brigadier-style tree + dispatch
│   ├── persist/            # region/save scheduling, player data, dirty flush
│   └── data/               # GENERATED registries/blocks/items/entities/sounds
├── tools/                  # codegen pipeline (fork of go-mc PR #294-296)
└── .planning/
```

### Structure Rationale

- **`server/tick.go` is the spine.** Every system exposes a `Tick(ctx)` method called in a fixed order from here. This is where the single-threaded authority lives.
- **`data/` and `protocol/packet/` are generated, never hand-edited.** They come from the 776 jar extractor. Keeping them isolated means a `--version` bump regenerates cleanly.
- **`pathfind/`, `tracker.go`, `spawn/` each get their own sub-package from the start** even when implemented synchronously, so the async swap is a single-package change behind a stable interface — not a cross-cutting rewrite.
- **`net` and `persist` are the only packages allowed to spawn long-lived goroutines** touching server data, and they only do so through channels into the tick loop.

## Architectural Patterns

### Pattern 1: Authoritative Single-Threaded Tick Loop

**What:** One goroutine drives a 20 TPS loop (50 ms budget per tick). All game-state mutation happens here, in a deterministic phase order. Vanilla does exactly this — "every tick of every entity, every block update, every player action runs on one CPU thread."

**When to use:** As the foundation. Correctness and determinism come free; you never reason about data races inside game logic.

**Tick phase order (mirrors vanilla, drives build order):**
```
1. Drain inbound packet queue  → apply player intent (movement, actions)
2. World tick                  → time, weather, scheduled block ticks, random ticks
3. Chunk tick                  → loaded-chunk upkeep, block entities
4. Entity tick                 → movement, velocity integration
5. AI / brain tick             → goal selection, navigation step
6. Physics resolution          → collisions, gravity settle
7. Apply async results         → drain result queues from off-thread workers
8. Entity tracking             → compute who-sees-what, emit spawn/move/despawn
9. Flush outbound              → push packets to per-conn write channels
```

**Trade-offs:** Simple and correct, but the whole world is bounded by one core. That is *acceptable and intended* — Leaf/Folia optimizations relieve it later. Do not pre-optimize.

**Go example (the spine):**
```go
func (s *Server) runTickLoop(ctx context.Context) {
    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            start := time.Now()
            s.drainInbound()        // 1
            s.world.Tick()          // 2-3 (owns chunks)
            s.entities.Tick()       // 4
            s.ai.Tick()             // 5
            s.physics.Resolve()     // 6
            s.applyAsyncResults()   // 7  <-- the rejoin seam
            s.tracker.Tick()        // 8
            s.flushOutbound()       // 9
            s.recordMSPT(time.Since(start)) // watch the 50ms budget
        }
    }
}
```

### Pattern 2: Compute-Off / Apply-On (the async rejoin seam)

**What:** Heavy *pure* computation (pathfinding A*, tracked-set diffing, brain evaluation, spawn candidate selection) is dispatched to a worker goroutine pool with an immutable snapshot of inputs. The worker never touches live game state. It returns a result onto a channel. The tick loop drains that channel in phase 7 and applies the result to live state single-threaded.

This is exactly how Leaf works: "the result is safely returned to the mob's navigation system for the next tick cycle"; tracked sets are "computed off-thread, then applied back to the main thread during the tick." It is also how Folia reintegrates: async results are "queued to execute during the target region's next tick, ensuring single-threaded access."

**When to use:** This is THE pattern that must exist in the seams from day one. Even the synchronous v1 should be structured as request→result so going async is swapping the executor, not restructuring callers.

**Critical rule:** The worker gets a **snapshot** (copied coordinates, copied nav-mesh slice, copied entity positions), not a live pointer into game state. If a worker reads live state, you have a data race the moment you parallelize.

**Go example (pathfinding seam, sync today / async tomorrow):**
```go
type PathRequest struct {
    EntityID int32
    Start, Goal Vec3
    Snapshot NavSnapshot // immutable copy of relevant collision data
}
type PathResult struct {
    EntityID int32
    Path     []Vec3
}

// v1 synchronous: compute inline, push to same queue the async version uses
func (p *Pathfinder) Request(req PathRequest) {
    res := computePath(req)        // later: go computePath into worker pool
    p.results <- res               // tick loop drains this in phase 7
}

// tick loop, phase 7 — identical whether sync or async
func (s *Server) applyAsyncResults() {
    for {
        select {
        case r := <-s.pathfinder.results:
            if e := s.entities.Get(r.EntityID); e != nil {
                e.Nav.SetPath(r.Path) // single-threaded mutation, safe
            }
        default:
            return
        }
    }
}
```

### Pattern 3: Ownership-Based Synchronization (not locks)

**What:** Game state has exactly one owner goroutine (the tick loop in v1; a per-region tick thread in a future Folia-style model). Other goroutines never lock-and-mutate; they enqueue requests/results. "Data is isolated, not shared. Regions do not share data structures." Folia enforces "only the thread currently ticking a region may access data owned by that region."

**When to use:** Always, for game state. Use locks/atomics only for genuinely shared infra (metrics counters, connection registry, the channels themselves).

**Trade-offs:** Forces you to think in messages, but eliminates the entire class of game-logic data races and makes the future regionization a *partitioning* exercise rather than a *locking* exercise. The alternative (shared-state + mutexes on chunks/entities) appears easier early and becomes a rewrite when you parallelize — avoid it.

### Pattern 4: Entity Model — OOP-with-component-split, not pure ECS

**What:** Vanilla and Leaf are OOP (Entity → Mob → PathfinderMob; Brain holds memories/behaviors/sensors). A pure data-oriented ECS would be faster for cache locality but means *not* mirroring vanilla, which fights the "vanilla-faithful" requirement and makes 1:1 behavior porting harder.

**Recommendation:** Use **OOP entities (Go interfaces + embedding) with a deliberate data/behavior split for the hot, async-bound parts** — position, velocity, nav state, and tracked-set inputs stored as plain copyable structs so they can be snapshotted for off-thread work. This is the pragmatic middle: faithful to vanilla logic, but the async-critical data is already in snapshot-friendly form.

**When pure ECS would win:** if raw entity throughput became the goal over vanilla fidelity. Given the "vanilla-faithful" requirement, ECS is an anti-goal for v1.

## Data Flow

### Inbound (client → world)
```
client TCP → conn goroutine reads → codec decode → typed packet
    → inbound channel → [TICK phase 1] dispatch by play-state handler
    → mutate player/world state (single-threaded)
```

### Outbound (world → clients)
```
[TICK phase 8] tracker computes per-player visible entity deltas
    → [TICK phase 9] build packets → per-conn outbound channel
    → conn goroutine writes → codec encode → client TCP
```

### Async compute rejoin (the seam)
```
[TICK phase 5] AI needs path → enqueue PathRequest(snapshot)
    → worker goroutine computes A* on snapshot (no live state access)
    → PathResult onto results channel
    → [NEXT TICK phase 7] drain results → apply to entity nav (single-threaded)
```

### Persistence
```
[TICK] mark chunk/entity dirty → enqueue save job
    → persist goroutine serializes (NBT) → region/MCA file write (off-tick I/O)
chunk load: ticket added → enqueue load → persist goroutine reads MCA / generator
    → decoded chunk onto load-result channel → [TICK] insert into world
```

## Build Order (dependency-driven)

This is the load-bearing output. Each stage depends on the prior; concurrency seams are designed in at the stage marked, even though the async *implementation* comes last.

| Order | Stage | Depends on | Why this order | Concurrency seam to design in |
|-------|-------|-----------|----------------|-------------------------------|
| 0 | **Data codegen** (packets, registries, blocks, items, entities for 776) | go-mc PR #294-296 fork, 26.2 jar | Nothing speaks the protocol or knows block states without this. Pure prerequisite. | — |
| 1 | **Net + protocol state machine** (handshake→status→login→config→play) | Stage 0 packets, go-mc `net`/`server` | A client cannot connect at all until states transition. Login (offline-mode) gates everything visible. | Per-conn goroutines + inbound/outbound channels established here — the network/tick boundary is born now |
| 2 | **Authoritative tick loop skeleton** (20 TPS, keepalive, empty phases) | Stage 1 | Everything below is "a thing that ticks." Establish the spine and phase order before filling phases. | The tick is the single owner; `applyAsyncResults` phase exists as a no-op from day one |
| 3 | **World + chunk system** (load/gen/store, BitStorage, heightmaps, view streaming) | Stage 2 (something to tick), go-mc `level`/`save` | Player needs ground to stand on and chunks to receive. Must exist before entities have a world. | Chunk load/save runs on persist goroutine → load-result channel into tick |
| 4 | **Player session in-world** (spawn, movement, view chunks, see other players, physics for player) | Stage 3 | First *playable* milestone. Validates the whole inbound→tick→outbound→client round trip with real state. | Entity tracking implemented synchronously but behind `tracker.Tick()` interface (async seam) |
| 5 | **Entity system + tick** (non-player entities: store, spawn, metadata, movement) | Stage 4 (tracking + physics primitives exist) | AI and spawning have nothing to drive without entities. | Entity data hot-fields stored as snapshot-friendly structs; `tracker` already async-shaped |
| 6 | **Physics** (gravity, AABB collision, block place/break, knockback) | Stage 5 entities, Stage 3 blocks | Entities need collision to behave; block interaction needs world. Can co-develop with 5. | Collision data snapshot-able for off-thread pathfinding later |
| 7 | **AI / brain + pathfinding** (goal selectors or brain, sync A* navigation, mob spawning) | Stage 6 (physics/collision), Stage 5 (entities) | Pathfinding needs collision; brain needs entities + world. Highest-value async target, so its request→result seam must be clean. | Pathfinding, brain ticks, and spawn candidate selection all built as request→snapshot→result, executed **synchronously** for now |
| 8 | **Command dispatch + chat** | Stage 2 (tick), Stage 4 (players) | Orthogonal; can slot in any time after players exist. | Commands enqueue into tick like any inbound intent |
| 9 | **Leaf concurrency optimizations** (async pathfinding, async entity tracker, DAB, async spawn, linear region format, lock-free queues) | Stages 4–8 complete | "You cannot async-optimize logic that does not yet exist." Each optimization swaps a synchronous executor for a goroutine pool behind the seam built in stages 4–7. No caller changes. | This stage *consumes* the seams; if stages 4–7 built them, this is additive, not a rewrite |
| 10 (stretch) | **Folia-style regionization** | Stage 9 ownership model proven | If single-tick-thread becomes the bottleneck after async offload, partition the world into per-region tick goroutines. Only viable because state was ownership-isolated, not lock-shared, from stage 2. | Tick loop generalizes from 1 owner to N region-owners |

**One-line dependency chain:** `data → protocol/net → tick spine → world/chunks → player-in-world → entities → physics → AI/pathfinding → (commands) → async optimizations → (regionization)`.

## Concurrency Seams (where Leaf attaches later — design these in early)

| Seam | Built synchronously in stage | Async swap in stage 9 | What must be true at build time so the swap is non-breaking |
|------|------------------------------|------------------------|--------------------------------------------------------------|
| **Pathfinding** | 7 | async pathfinding goroutine pool | Path request carries an immutable nav snapshot; result returns via channel drained in tick phase 7; navigation tolerates a path arriving 1+ ticks late (Leaf's `path.isProcessed()` / callback model) |
| **Entity tracking** | 4 | async entity tracker pool | Tracked-set computation reads only snapshotted entity positions; emits deltas applied in-tick; "intervals adapt via backpressure" — so make tracking interval a tunable, not hardcoded every-tick |
| **Mob spawning** | 7 | async spawn executor (note: Leaf removed this in 1.21.7 — treat as optional) | Spawn candidate scan runs on snapshot of chunk/entity density; actual spawn (state mutation) happens in-tick |
| **Brain / AI (DAB)** | 7 | distance-aware brain frequency throttling | Brain tick frequency is a per-entity tunable keyed on nearest-player distance; brain reads memories/sensors that can be snapshotted |
| **Persistence I/O** | 3 | already off-tick | Save serialization works on dirty-copied data; never blocks the tick on disk |
| **Region partitioning** | 2 (ownership rule) | per-region tick goroutines (Folia model, stretch) | No game state guarded by mutexes; all mutation funnels through an owner. If true, partitioning = giving each region its own owner + cross-region ops via the global/coordination queue |

**The single most important early decision:** adopt **ownership-based synchronization at stage 2** (tick loop owns all game state; off-thread code only snapshots-in and channels-results-out). Every seam above is cheap if this holds and a rewrite if it does not. Folia and Leaf both prove the model; the cost of imposing it on a single-threaded v1 is near zero (one goroutine owns everything anyway), and it is the entire insurance policy against the "concurrency-ready from day one" requirement.

## How go-mc Slots In (and where the seams are)

| go-mc package | Role in Ender | Seam / boundary |
|---------------|---------------|-----------------|
| `net`, `net/packet`, `CFB8` | Wire codec: framing, VarInt, compression, encryption. Used by stage-1 conn goroutines. | Provides codec only — Ender owns the per-conn goroutine lifecycle and the channel boundary to the tick |
| `server` | Connection framework: login flow, keepalive, player list scaffolding. **No game loop, no world, no entities** (confirmed: "focused on protocol and connection management"). | The seam is exactly here: go-mc stops at "client logged in." Ender's tick loop, world, entities, AI begin past this line. This is the ~70% Ender builds. |
| `level`, `level/block`, chunk structs | Chunk/block **data structures** (block states, sections). Used by stage-3 chunk system. | Data only — Ender owns chunk *management* (tickets, loading, view tracking, ticking) |
| `save`, `save/region` | MCA region file read/write, `LevelData`/level.dat. Used by stage-3 + persist goroutine. | I/O primitives only — Ender owns *when* (off-tick scheduling, dirty tracking) |
| `nbt` | NBT encode/decode for persistence, player data, (legacy) metadata. | Library; no seam concern |
| `chat` | Chat component format (JSON + legacy §). Used by stage-8 commands/chat. | Library; note 1.20.5+ moved slots to data components (not NBT) — verify chat/component handling against generated 776 data |
| `data` | Static registries. **Replaced/augmented** by the stage-0 codegen output for 776. | Ender's generated `data/` supersedes this for version accuracy |
| `yggdrasil`, `realms`, `bot` | Auth (online-mode, later), Realms (out of scope), client bot (test harness only). | Online-mode auth is a later stage; offline-mode first keeps stage 1 simple |

**Summary of the seam:** go-mc gives Ender the *wire* (net/codec), the *data shapes* (level/chunk/nbt), and the *persistence I/O* (save/region) — roughly the 30% noted in PROJECT.md. The boundary where go-mc ends and Ender begins is precisely the line "a client is connected and authenticated." Everything that makes it a *game* — the tick loop, world simulation, entities, AI, physics, commands — is Ender, and all of it lives behind the network/persistence channel boundaries that double as the concurrency seams.

## Anti-Patterns

### Anti-Pattern 1: Mutex-guarded shared game state
**What people do:** Put a `sync.RWMutex` on the chunk map / entity registry and let any goroutine lock-and-mutate.
**Why it's wrong:** It "works" single-threaded but locks become the bottleneck and the source of deadlocks the moment you add async workers; converting to a region model later is a full rewrite. Folia explicitly rejects this: "ownership-based synchronization eliminates lock contention."
**Do this instead:** One owner goroutine per state partition; mutate only in-tick; off-thread code snapshots in / channels out.

### Anti-Pattern 2: Off-thread workers reading live game state
**What people do:** Pass a `*Entity` or live chunk pointer into the pathfinding goroutine to "save a copy."
**Why it's wrong:** Instant data race when the tick mutates that entity concurrently. This is the #1 way async optimizations introduce heisenbugs.
**Do this instead:** Pass an immutable snapshot struct; return a plain result; apply in-tick.

### Anti-Pattern 3: Async-first, before vanilla logic exists
**What people do:** Build the pathfinder async from the start to "save a rewrite."
**Why it's wrong:** You cannot validate correctness of logic you haven't built, and you debug concurrency + game logic simultaneously. PROJECT.md is explicit: "you cannot async-optimize logic that does not yet exist."
**Do this instead:** Build synchronous behind the request→result seam; flip the executor to a goroutine pool in stage 9.

### Anti-Pattern 4: Pure ECS to chase performance
**What people do:** Adopt a data-oriented ECS for entities to maximize cache locality.
**Why it's wrong:** It diverges from vanilla's OOP brain/goal model, making "vanilla-faithful" behavior porting much harder, for a performance win the async-offload model already largely delivers.
**Do this instead:** OOP entities with snapshot-friendly hot data; reconsider ECS only if entity throughput becomes the proven bottleneck.

### Anti-Pattern 5: Blocking the tick on I/O or async completion
**What people do:** Synchronously read a chunk from disk, or `<-result` (block) inside the tick.
**Why it's wrong:** A 50 ms tick budget cannot absorb disk latency; one blocking call stalls the whole world (TPS drop).
**Do this instead:** Chunk load/save and async compute are fire-and-forget into queues; the tick *drains what's ready* (non-blocking `select … default`) and moves on.

## Scaling Considerations

| Scale | Architecture posture |
|-------|----------------------|
| 1 player, dev | Single tick thread, all sync. Validates correctness. Stages 1–8. |
| ~tens of players, vanilla parity | Single tick thread + async pathfinding/tracker (stage 9). This is where Leaf's wins land: heavy compute off the main thread, tick stays under 50 ms. DAB throttles distant brains. |
| hundreds, spread out | Folia-style regionization (stage 10). Only reachable because state was ownership-isolated from stage 2. Players spread across regions tick in parallel. |

**First bottleneck:** the single tick thread saturating at 50 ms — relieved by stage-9 async offload (pathfinding and entity tracking are the classic top consumers per Leaf/Mojang MC-198840 "entities do pathfinding on the main thread").
**Second bottleneck:** the tick thread *itself* even after offload (dense single region) — relieved by stage-10 regionization.

## Sources

- PaperMC/Folia Region Threading System — DeepWiki (region definition, thread ownership, async reintegration, ownership-based sync): https://deepwiki.com/PaperMC/Folia/2-region-threading-system [HIGH]
- Folia overview — paper-chan.moe & papermc.io (regionized ticking, 20 TPS per region): https://paper-chan.moe/folia/ , https://papermc.io/software/folia/ [HIGH]
- PaperMC/Paper Tick Loop and Task Scheduling — DeepWiki (single main thread, scheduler): https://deepwiki.com/PaperMC/Paper/3.2-tick-loop-and-task-scheduling [HIGH]
- Winds-Studio/Leaf Features and Benefits — DeepWiki (async pathfinding, async tracker, DAB, async spawn, rejoin pattern): https://deepwiki.com/Winds-Studio/Leaf/1.1-features-and-benefits [HIGH]
- Leaf docs / global config (DAB, async pathfinding behavior): https://docs.leafmc.one/reference/config/leaf-global/ [MEDIUM]
- Mojang bug MC-198840 "Entities do pathfinding on the main Thread" (confirms the offload target): https://bugs.mojang.com/browse/MC-198840 [HIGH]
- Tnze/go-mc package layout — GitHub & pkg.go.dev (net/server/level/save/nbt/chat/data scope; server = connection framework, no game loop): https://github.com/Tnze/go-mc , https://pkg.go.dev/github.com/Tnze/go-mc [HIGH]
- go-mc save package (LevelData / region format): https://pkg.go.dev/github.com/Tnze/go-mc/save [HIGH]
- Minecraft single-threaded tick loop / 20 TPS / 50 ms budget (vanilla behavior): https://deepwiki.com/PaperMC/Paper/3.2-tick-loop-and-task-scheduling , https://wabbanode.com/blog/minecraft/minecraft-server-tps-explained [MEDIUM]
- ECS vs OOP tradeoffs & Go game-server concurrency (goroutines/channels per-connection, ticker-driven sim): https://github.com/SanderMertens/ecs-faq , https://reintech.io/blog/implementing-multiplayer-game-server-with-go [MEDIUM]

---
*Architecture research for: Minecraft Java 26.2 server in Go (Ender)*
*Researched: 2026-06-23*
