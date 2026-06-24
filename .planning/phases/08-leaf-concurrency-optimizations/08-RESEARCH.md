# Phase 8: Leaf Concurrency Optimizations - Research

**Researched:** 2026-06-24
**Domain:** Go concurrency (goroutine pools, lock-free collections) applied behind pre-built tick seams + linear region file format
**Confidence:** HIGH

## Summary

This is the **payoff phase**, not an exploration phase. Phases 3–7 deliberately built every async-bound subsystem as a *synchronous* reference implementation behind a clean one-shape seam, and established the single-owner-tick invariant (TICK-05) plus the `applyAsyncResults` rejoin channel that has been **proven in production since Phase 4** (the `chunkReady` rejoin). Phase 8's job is to swap the synchronous executor behind each seam for a bounded goroutine pool — additively, with **zero logic change** to the ported algorithms — and prove the result `-race` clean.

The concurrency stack is **already chosen and verified in CLAUDE.md** (xsync/v4 concurrent maps + MPMCQueue, ants/v2 pools, sourcegraph/conc structured concurrency, golang.org/x/sync singleflight/semaphore). None of it is currently a dependency — `go.mod` today has only `google/uuid`, `golang.org/x/exp`, and `golang.org/x/sync` `[VERIFIED: go.mod]`. Phase 8 is the first phase to introduce `xsync/v4` and `ants/v2` per the CLAUDE.md "Stack Patterns by Variant" guidance ("Introduce xsync/v4 for the entity table… Introduce ants/v2 pools — one per async subsystem").

The single load-bearing discipline is the **rejoin pattern**: async work computes off-tick over an **immutable snapshot copied on the owner goroutine**, wraps its result in an `asyncResult`, sends it on a channel, and the owner applies it inside `applyAsyncResults`. **Nothing async ever mutates tick-owned state directly.** Every OPT-01/02/03 follows the exact discipline the `chunkReady` rejoin already proves. OPT-04 (xsync/ants) and OPT-05 (linear region) are supporting work; OPT-06 (`-race` clean) is the acceptance gate that the single-owner discipline makes pass *by construction*.

**Primary recommendation:** For each of OPT-01/02/03, define a concrete `asyncResult` type (`pathReady`, `trackerDiffReady`, `spawnCandidatesReady`) with an `applyTo(*TickLoop)` method, submit the existing pure/snapshot computation to a per-subsystem `ants.Pool`, and route results through the **unchanged** `applyAsyncResults` seam — copying the existing `SetWorld`/`chunkReady` wiring verbatim. Swap a collection to xsync/v4 **only** where an async worker reads it while the tick writes it; keep every tick-only collection a plain map (single-owner is faster). Extend `save/region` in-place with a `.linear` reader/writer behind the existing `loadOrGenerate`/`saveEntities` call sites.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| A* path computation (OPT-01) | Off-tick worker pool (ants) | Tick owner (snapshot build + apply) | The compute is PURE over an immutable snapshot (`computePath`); only the snapshot copy and the result apply touch tick state |
| Visibility diff (OPT-02) | Off-tick worker pool (ants) | Tick owner (snapshot build + packet send) | Diff math is pure over a snapshot of entity positions + tracked-set; packet emission stays on the owner |
| Spawn candidate scan (OPT-03) | Off-tick worker pool (ants) | Tick owner (cap count + actual `entityStore.add`) | The column/Y scan is read-only over a world snapshot; the mutation (spawn) is owner-only |
| Hot collection access (OPT-04) | Tick owner (writes) | Off-tick workers (reads) | Only collections genuinely crossed by the async boundary become xsync; tick-only collections stay plain maps |
| Region disk I/O (OPT-05) | Off-tick worker (`world.Worker` / save loop) | — | Already off-tick (Phase 4); OPT-05 only changes the on-disk codec, not the threading |
| Race correctness (OPT-06) | Cross-cutting | — | Proven by the single-owner discipline + the Docker `-race` gate |

## User Constraints

No `CONTEXT.md` exists for this phase (greenfield planning from REQUIREMENTS/ROADMAP). The binding constraints come from the roadmap, STATE.md accumulated context, and CLAUDE.md:

### Locked Decisions (from ROADMAP + STATE + CLAUDE.md)
- **ADDITIVE, NOT A REWRITE.** Every swap goes behind an existing seam. If a swap requires changing the pipeline order or the tick's ownership model, it is wrong — re-derive. `[CITED: ROADMAP Phase 8 goal]`
- **TICK-05 PRESERVED.** Async work computes over an IMMUTABLE SNAPSHOT (copied on the owner) and rejoins via the channel applied ON the owner. No async mutation of tick-owned state. `[CITED: REQUIREMENTS TICK-05, STATE Accumulated Context]`
- **`-race` IS NON-NEGOTIABLE (OPT-06).** Every async subsystem proven `-race` clean in Docker `golang:1.26` (host is `CGO_ENABLED=0`, no C compiler). `[VERIFIED: STATE.md line 148, 07-05-SUMMARY.md]`
- **Paths tolerated 1+ ticks late (OPT-01).** The async result may arrive a tick or more after the request; navigation must tolerate a pending/late path. `[CITED: REQUIREMENTS OPT-01]`
- **The verified stack is xsync/v4 + ants/v2 + conc + x/sync** — do not re-research alternatives. `[CITED: CLAUDE.md Technology Stack]`

### Claude's Discretion
- Pool sizing per subsystem, channel buffer depths, and whether OPT-04 uses `xsync.Map` vs an int64-keyed plain-map-behind-mutex for any specific collection (decide by the contention rationale in `## Common Pitfalls`).
- Whether OPT-05 ships `.linear` as opt-in (config flag) or as the default save codec — recommend opt-in for v1 (keeps `.mca` compatibility, see Open Questions).

### Deferred Ideas (OUT OF SCOPE)
- Folia-style per-region tick threading (REGION-01) — that is Phase 9.
- Async chunk *generation parallelism* beyond what Phase 4 already does (the worker already runs off-tick with singleflight) — OPT scope is the AI/tracker/spawner executors + collections + region codec.
- Bandwidth-optimizing delta entity moves (`MoveEntity*` short deltas) — the tracker already has the encoders; not an OPT requirement.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| OPT-01 | Async pathfinding swaps the synchronous A* executor for a goroutine pool behind the Phase-7 seam, paths tolerated 1+ ticks late | `## Seam Map` row 1; `computePath` is already PURE (`pathfinder.go`); `requestPath` submits-and-continues to an ants pool; result rejoins as `pathReady` via `applyAsyncResults` |
| OPT-02 | Async entity tracker runs visibility updates off the main tick and rejoins via the apply-async seam | `## Seam Map` row 2; the `tracker interface{ Tick() }` seam swaps the executor; the diff is computed off-tick over a position snapshot, packets sent on rejoin |
| OPT-03 | Async mob spawning scans candidate locations off-thread | `## Seam Map` row 3; `spawnableColumns`/`findStandableY` scan moves off-tick over a world snapshot; the `entityStore.add` mutation rejoins on the owner |
| OPT-04 | Hot-path collections use lock-free/specialized variants (xsync/v4); worker pools use ants/v2 | `## Standard Stack` + `## Common Pitfalls` Pitfall 1 (contention rationale: which collections cross the async boundary) |
| OPT-05 | Linear region file format reduces disk I/O and footprint | `## Seam Map` row 5; the exact `.linear` binary spec below; extend `save/region` in-place behind `loadOrGenerate`/`saveEntities` |
| OPT-06 | The server passes `-race` clean on every async subsystem | `## -race Strategy`; the Docker gate + the per-subsystem concurrent test that exercises each rejoin boundary |

## Standard Stack

### Core (NEW this phase — first introduction)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/panjf2000/ants/v2` | **v2.12.1** | Bounded, recycling goroutine pool — ONE per async subsystem (pathfinding, tracker, spawner) | Caps goroutine count (prevents "goroutine per mob" blowup), recycles workers; the execution substrate for every OPT-01/02/03 swap. `[VERIFIED: go list -m -versions → latest v2.12.1; CLAUDE.md pins v2.12.0+]` |
| `github.com/puzpuzpuz/xsync/v4` | **v4.5.0** | CLHT `Map[K,V]` (obstruction-free reads, lock-sharded writes), Vyukov `MPMCQueue`, `Counter` | For the COLLECTIONS genuinely crossed by the async boundary (read by a worker while the tick writes). Generics-only API. `[VERIFIED: go list -m -versions → latest v4.5.0; CLAUDE.md pins v4.x]` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/sourcegraph/conc` | v0.3.0 | Structured concurrency: scoped `WaitGroup`, `pool.NewWithResults[T]`, panic propagation | Tick-bounded fan-out/join where work must complete WITHIN a tick (e.g. parallelizing the per-player tracker diff across players within one tick). Use ONLY if a swap is tick-bounded rather than tolerate-late. `[VERIFIED: go list -m -versions → latest v0.3.0]` |
| `golang.org/x/sync` | **v0.21.0 (already a dep)** | `singleflight` (dedup concurrent path requests for the same mob/target), `semaphore` (weighted IO throttle on region flush) | `singleflight` is already used by `world.Worker` for chunk-gen dedup — reuse the same pattern for OPT-01 if two goals request the same path key. `[VERIFIED: go.mod line 10]` |

### Alternatives Considered (all rejected — stack is locked)
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `ants/v2` long-lived pool | `conc/pool` (tick-scoped) | `conc/pool` joins before the next tick — WRONG for OPT-01 where paths are tolerated 1+ ticks late. Use `ants` for tolerate-late streams, `conc` only for tick-bounded fan-out. `[CITED: CLAUDE.md "Stack Patterns by Variant"]` |
| `xsync.Map` | `sync.Map` / plain map + mutex | `sync.Map` only for read-mostly low-churn; the entity table is write-heavy under async. Plain map stays correct for tick-only collections (single-owner = faster — DO NOT xsync these). `[CITED: CLAUDE.md What NOT to Use]` |

**Installation:**
```bash
go get github.com/panjf2000/ants/v2@v2.12.1
go get github.com/puzpuzpuz/xsync/v4@v4.5.0
# conc only if a tick-bounded fan-out is actually needed:
# go get github.com/sourcegraph/conc@v0.3.0
```

**Version verification (this session):**
```
github.com/puzpuzpuz/xsync/v4   → latest v4.5.0   [VERIFIED: go list -m -versions]
github.com/panjf2000/ants/v2    → latest v2.12.1  [VERIFIED: go list -m -versions]
github.com/sourcegraph/conc     → latest v0.3.0   [VERIFIED: go list -m -versions]
golang.org/x/sync               → v0.21.0 (in go.mod) [VERIFIED: go.mod]
```
> Note: `go.mod` declares `go 1.25.0` but the host toolchain is `go 1.26.1` and the `-race` Docker image is `golang:1.26` `[VERIFIED: go version; 07-02-SUMMARY.md]`. All three new deps require far older Go floors (ants ≥1.19, xsync generics ≥1.18) — no toolchain bump needed.

## Architecture Patterns

### System Architecture Diagram — The Rejoin Discipline (the ONE pattern every OPT follows)

```
                          ┌─────────────────────── TICK GOROUTINE (sole owner) ───────────────────────┐
                          │                                                                            │
   inbound chan Intent ──>│ drainInbound                                                               │
                          │ resolveSubtickInputs                                                       │
                          │ tickWorld / tickChunks                                                     │
                          │ tickEntities                                                               │
                          │ tickAI ─────┐ (1) build IMMUTABLE snapshot ON the owner (cheap value copy) │
                          │             │     - OPT-01: snapshotRegion(...) -> pathRequest             │
                          │             │     - OPT-02: position+tracked snapshot                     │
                          │             │     - OPT-03: loaded-column + world-solidity snapshot        │
                          │ tickPhysics │                                                              │
                          │             │ (2) submit(snapshot) to ants.Pool ───────┐                   │
                          │             │     navigation does NOT block; mob keeps  │                   │
                          │             │     its last action (path tolerated late) │                   │
                          │ applyAsyncResults <───── asyncIn <-chan asyncResult <───┼──┐                │
                          │   for r := range drained: r.applyTo(t)  (4) APPLY on    │  │                │
                          │     - pathReady.applyTo: nav.path = result              │  │                │
                          │     - trackerDiffReady.applyTo: p.client.Send(diff pkts)│  │                │
                          │     - spawnCandidatesReady.applyTo: entityStore.add     │  │                │
                          │ tracker.Tick()  (may itself be the async executor)      │  │                │
                          │ flushOutbound                                           │  │                │
                          └─────────────────────────────────────────────────────────┼──┼────────────────┘
                                                                                     │  │
       ┌───────────────── OFF-TICK ants.Pool workers (bounded, recycling) ──────────┘  │
       │  (3) compute PURE over the immutable snapshot (NO *TickLoop, NO live world)    │
       │      - OPT-01: computePath(req)  -> *Path                                      │
       │      - OPT-02: diff(snapshot)    -> []packet                                   │
       │      - OPT-03: scan(snapshot)    -> []candidate                                │
       │      wrap result as asyncResult, send on asyncIn ──────────────────────────────┘
       └───────────────────────────────────────────────────────────────────────────────
```

This is **exactly** the Phase-4 `chunkReady` flow, generalized. The `world.Worker` (off-tick) → `worker.Results()` → `SetWorld`'s adapter goroutine → `asyncBridge` → `applyAsyncResults` → `chunkReady.applyTo(t)` path is the **proven reference** `[VERIFIED: tick.go lines 51–73, 434–448; tick_phases.go lines 35–48]`.

### Recommended Project Structure (additive — no new packages required)
```
server/
├── async.go            # NEW: per-subsystem ants pools + the asyncResult types
│                       #   (pathReady, trackerDiffReady, spawnCandidatesReady)
├── pathfinder.go       # UNCHANGED: computePath stays pure
├── navigation.go       # EDIT: requestPath submits to the pool instead of inline computePath
├── tracker.go          # EDIT: add an asyncTracker behind the SAME tracker interface
├── spawner.go          # EDIT: naturalSpawn scans off-tick, adds on rejoin
├── tick.go             # EDIT: register pathReady/trackerDiffReady/... applyTo; wire pools in NewTickLoop
└── tick_phases.go      # UNCHANGED pipeline ORDER (applyAsyncResults already in the right slot)
save/region/
├── mca.go              # UNCHANGED: Anvil reader/writer stays
└── linear.go           # NEW: .linear reader/writer (zstd whole-region)
world/
└── worker.go           # EDIT: loadOrGenerate tries .linear then .mca (OPT-05)
```

### Pattern 1: The `asyncResult` + per-subsystem `ants.Pool` (the OPT-01/02/03 template)
**What:** Each async subsystem gets ONE long-lived `*ants.Pool` (created in `NewTickLoop`, sized per subsystem) and a concrete `asyncResult` type whose `applyTo(*TickLoop)` runs on the owner.
**When to use:** Every tolerate-late off-tick computation (path, tracker diff, spawn scan).
**Example (OPT-01 — the canonical swap):**
```go
// async.go — NEW. Mirrors world.Worker's bounded discipline but for in-process compute.
// Source: ants/v2 README NewPool + Submit; the rejoin shape ports tick.go chunkReady.
type pathReady struct {
	mobID  int32      // validate the entity still exists on apply (Pitfall: late path to despawned mob)
	target [3]int     // validate the goal target is still current
	path   *Path      // the immutable result computed off-tick
}

func (r pathReady) applyTo(t *TickLoop) {
	e, ok := t.entities.byID[r.mobID]          // owner-only map read
	if !ok || e.ai == nil || e.ai.nav == nil { // mob despawned while the path computed: drop it
		return
	}
	if e.ai.nav.pendingTarget != r.target {    // goal changed mid-flight: stale result, drop it
		return
	}
	e.ai.nav.path = r.path                      // adopt the late path; mob starts following next tick
	e.ai.nav.pending = false
}

// navigation.go — requestPath becomes submit-and-continue (was: inline computePath).
func (n *groundNavigation) requestPath(t *TickLoop, e *Entity, tx, ty, tz int) {
	region := snapshotRegion(t.world, e, tx, ty, tz, navFollowRange, navReachRange) // COPY on the owner
	req := pathRequest{ /* ... same fields as today ... */ region: region }
	n.pending = true
	n.pendingTarget = [3]int{tx, ty, tz}
	mobID := e.id
	_ = t.pathPool.Submit(func() {                 // OFF-TICK: pure compute over the immutable copy
		p := computePath(req)                       // UNCHANGED pure function
		t.asyncIn2 <- pathReady{mobID, [3]int{tx, ty, tz}, p} // rejoin via the channel
	})
	// NOTE: no n.path assignment here — the mob keeps its current action until pathReady lands.
}
```
**Key:** `computePath` does not change at all — its purity (`TestComputePathPure`) is the contract that makes this a swap, not a rewrite `[VERIFIED: pathfinder.go lines 39–45, 241–244]`.

### Pattern 2: Swap the tracker EXECUTOR behind the unchanged `tracker interface{ Tick() }`
**What:** `tracker.go`'s `entityTracker` becomes `asyncTracker`, assigned at the **single swap-point line** in `NewTickLoop` (`t.tracker = &entityTracker{loop: t}`). The interface and the `t.tracker.Tick()` call site stay identical.
**When to use:** OPT-02.
**How:** `asyncTracker.Tick()` runs on the owner but only does cheap work: it builds an immutable per-player snapshot (positions + tracked-set copy) and submits the **diff computation** to the tracker pool. The diff result (`trackerDiffReady` — a list of packets per player) rejoins via `applyAsyncResults`, where the owner calls `p.client.Send(...)`. Packet *emission* must stay on the owner because `p.client.Send` enqueues onto the bounded per-player outbound queue (the writeLoop stays the sole socket writer) — only the *diff math* goes off-tick.
```go
// tracker.go — the swap-point in NewTickLoop (tick.go line 417):
//   t.tracker = &entityTracker{loop: t}   // BEFORE (synchronous)
//   t.tracker = &asyncTracker{loop: t}    // AFTER  (off-tick diff, owner-side send)
```

### Anti-Patterns to Avoid
- **Mutating tick-owned state from a pool worker.** A worker may ONLY read its immutable snapshot and produce an immutable result. The `entityStore.add`, `nav.path =`, and `p.client.Send` mutations happen exclusively in `applyTo` on the owner. `[CITED: tick.go chunkReady.applyTo precedent]`
- **Blocking the tick on a pool result.** `requestPath` must return immediately (submit-and-continue). Waiting on the path defeats OPT-01's "tolerated 1+ ticks late." 
- **Reordering the pipeline.** `applyAsyncResults` is already in the correct fixed slot (after tickPhysics, before tracker.Tick) `[VERIFIED: tick_phases.go lines 23–25]`. Do not move it; `TestTickPhaseOrder` will fail.
- **xsync-ing every collection.** A tick-only map is faster as a plain map; only collections an off-tick worker reads while the tick writes need xsync (see Pitfall 1).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Off-tick rejoin plumbing | A new result channel + drain loop | The existing `applyAsyncResults` + `asyncResult` interface | Already exists, already proven `-race` clean by `chunkReady` `[VERIFIED: tick.go, tick_phases.go]` |
| Bounded recycling goroutine pool | A `chan func()` + worker goroutines + size cap | `ants/v2` Pool | Recycling, panic recovery, dynamic resize, battle-tested `[CITED: CLAUDE.md]` |
| Concurrent map under async read + tick write | A `sync.RWMutex` around a plain map | `xsync.Map[K,V]` | CLHT: obstruction-free reads, lock-sharded writes — far better under write contention `[CITED: CLAUDE.md]` |
| Dedup concurrent same-key path requests | A pending-requests map + mutex | `singleflight.Group` (already used by `world.Worker`) | The exact pattern Phase 4 uses for chunk-gen dedup `[VERIFIED: world/worker.go line 89]` |
| Snapshot-on-owner for pathfinding | A new copy routine | `snapshotRegion` (already builds the immutable `pathRegion`) | Phase 7 built it precisely as the Phase-8 hinge `[VERIFIED: path_region.go lines 104–140]` |
| Anvil region read/write | A new `.mca` codec | The fork's `save/region` `Region` + `world.Worker.tryRegion` | Already reused for chunks AND entities; OPT-05 ADDS `.linear` alongside, never replaces `.mca` `[VERIFIED: world/worker.go, persistence.go]` |
| zstd compression for `.linear` | A hand-rolled compressor | `github.com/klauspost/compress/zstd` (pure-Go, no cgo) | Vanilla `.linear` uses zstd; a pure-Go zstd keeps the `CGO_ENABLED=0` build (see Open Questions) |

**Key insight:** Phase 8 is overwhelmingly *deletion of synchronous call sites + wiring of pools*. The hardest-won asset — the immutable-snapshot purity of `computePath` and the `applyAsyncResults` rejoin — was paid for in Phases 3–7. Building anything custom here is re-litigating settled architecture.

## Seam Map (load-bearing — each OPT → its exact existing seam, snapshot, rejoin, and what stays single-owner)

| OPT | Existing seam (file:symbol) | Snapshot copied ON the owner | Off-tick compute (pure) | Rejoin path | Stays single-owner |
|-----|------------------------------|-------------------------------|--------------------------|-------------|---------------------|
| **OPT-01** Async pathfinding | `navigation.go:requestPath` → calls pure `pathfinder.go:computePath`; `groundNavigation.path` field | `snapshotRegion(...) -> *pathRegion` (immutable solidity bitset) wrapped in `pathRequest` (already a value) `[VERIFIED: path_region.go:104]` | `computePath(req) -> *Path` (PURE, `TestComputePathPure`) `[VERIFIED: pathfinder.go:241]` | new `pathReady{mobID,target,path}.applyTo` → `nav.path = path` (after validating mob still exists + target unchanged) via `applyAsyncResults` | the `pathRegion` build (reads tick-owned `ChunkManager`); `moveEntity` path-following; `entityStore` |
| **OPT-02** Async entity tracker | `tracker.go:entityTracker.Tick()` behind `tick.go:tracker interface{ Tick() }`; swap-point at `NewTickLoop` line 417 `[VERIFIED: tick.go:81, 417]` | per-player: copy of in-range entity positions (`near()` result is already a fresh slice `[VERIFIED: tracker.go:59]`) + a copy of `p.tracked` | `diff(snapshot) -> []packet` (the spawn/teleport/remove decision math, pure) | new `trackerDiffReady{playerID,packets}.applyTo` → `p.client.Send(pkt)` for each + update `p.tracked` on the owner | `p.client.Send` (bounded outbound queue; writeLoop is sole socket writer); `p.tracked` mutation; `entityStore.near` read |
| **OPT-03** Async mob spawning | `spawner.go:naturalSpawn` → `spawnableColumns`/`findStandableY`/`mobNear` scans; `tickAI` calls it `[VERIFIED: spawner.go:163, tick_phases.go:163]` | snapshot of loaded columns near players (`spawnableColumns` result) + the world-solidity reads `findStandableY` needs (copy the relevant column slices, like `pathRegion`) | `scan(snapshot) -> []candidate{x,y,z}` (the standable-Y + packing scan, pure) | new `spawnCandidatesReady{candidates}.applyTo` → re-check cap (`countByCategory`) on the owner, then `entityStore.add(pig)` for one candidate | the cap count (`countByCategory` over the authoritative store); the `entityStore.add` MUTATION; `idAlloc.AllocID` |
| **OPT-04** Hot collections / pools | `entityStore.byID`+buckets, `ChunkManager.columns`, `clientIndex`, `players` (all plain today `[VERIFIED: tick.go:119,134,140,166; manager.go:41]`) | n/a (this is the cross-boundary access policy) | n/a | n/a | **Decide per-collection by contention** (Pitfall 1). `entityStore.byID` is read by the OPT-01/02/03 workers' *snapshots* (which copy ON the owner), so it may stay plain IF every worker reads only its copy. xsync only where a worker reads the LIVE collection. Pools = ants/v2, one per subsystem. |
| **OPT-05** Linear region | `world/worker.go:loadOrGenerate`/`tryRegion`; `persistence.go:saveEntities`/`loadEntities` `[VERIFIED: world/worker.go:109, persistence.go:214]` | n/a (already off-tick since Phase 4) | `.linear` encode/decode (whole-region zstd) | n/a (writes are already off-tick; the codec changes, not the threading) | the `ChunkManager` mutation is already owner-only via `chunkReady`; OPT-05 changes only the on-disk bytes |
| **OPT-06** `-race` clean | cross-cutting | n/a | n/a | n/a | the WHOLE design — proven by the Docker `-race` gate over concurrent tests exercising each rejoin |

**Critical observation for OPT-04:** Because OPT-01/02/03 each copy their snapshot **on the owner before submitting** (exactly like `snapshotRegion` and `near()` already do), the workers never touch the live `entityStore`/`ChunkManager` — they read only their immutable copy. This means **most collections can stay plain maps**. xsync is needed only if a design choice has a worker read a live tick-owned collection directly (avoid that; prefer the snapshot). The honest recommendation: introduce xsync **only** for a collection where profiling or a specific design forces a live cross-boundary read; otherwise the single-owner plain map is correct and faster (see Open Questions Q1).

## Common Pitfalls

### Pitfall 1: xsync-ing a tick-only collection (slower, pointless)
**What goes wrong:** Swapping `entityStore.byID` or `ChunkManager.columns` to `xsync.Map` "to be safe" adds lock-sharding overhead to a collection only the tick touches.
**Why it happens:** Conflating "Phase 8 = use xsync" with "use xsync everywhere."
**How to avoid:** A collection becomes xsync ONLY if an off-tick worker reads the LIVE collection concurrently with a tick write. Since every OPT here snapshots on the owner, the live collections stay tick-only → keep them plain. CLAUDE.md is explicit: "a tick-only map stays a plain map (single-owner = faster)."
**Warning signs:** A worker closure capturing `t.entities` or `t.world` directly instead of a copied snapshot.

### Pitfall 2: A late path applied to a despawned/retargeted mob
**What goes wrong:** OPT-01's path arrives 1+ ticks late; by then the mob despawned, or its goal changed, or it already reached the target. Applying the stale path crashes (nil deref on a removed entity) or makes the mob walk to a wrong spot.
**Why it happens:** "Tolerated 1+ ticks late" means the world moved on between submit and apply.
**How to avoid:** `pathReady.applyTo` MUST re-validate on the owner: (a) the mob id still exists in `entityStore.byID`, (b) the navigation's pending target still matches the result's target, (c) the navigation still wants a path. Drop the result otherwise. Carry a `mobID` + `target` in the result, not a live `*Entity` pointer.
**Warning signs:** A pool closure capturing `*Entity` instead of `e.id`; `applyTo` without an existence check.

### Pitfall 3: A worker capturing a live pointer instead of a value snapshot
**What goes wrong:** A worker closure captures `e *Entity` or `t.world` and reads it off-tick while the tick mutates it → data race (OPT-06 fails).
**Why it happens:** Closures capture by reference; it's easy to capture the entity instead of copying its fields.
**How to avoid:** Submit only immutable values: `pathRequest` (already a value with an immutable `*pathRegion`), copied position floats, copied tracked-set. Mirror `chunkReady`, which carries an immutable `world.ChunkResult` and "retains no reference … never mutates it" `[VERIFIED: world/worker.go:17–26]`.
**Warning signs:** `-race` flags a read in a pool worker against a write in `tickPhysics`/`moveEntity`.

### Pitfall 4: Unbounded pool growth / blocking submit
**What goes wrong:** Either a goroutine-per-task blowup (no pool cap) or `ants.Submit` blocking the tick when the pool is saturated.
**Why it happens:** Default `ants` blocks when full; a blocking submit on the owner stalls the tick.
**How to avoid:** Create pools with `ants.NewPool(size, ants.WithNonblocking(true))` so `Submit` returns `ErrPoolOverload` instead of blocking — drop the request (the mob keeps its last action; navigation re-requests next tick, exactly like `world.Worker.Request` drops on a full bounded queue `[VERIFIED: world/worker.go:65–70]`). Size each pool to its subsystem's steady-state (e.g. pathfinding ~ number of CPU cores; tracker/spawner small).
**Warning signs:** Rising goroutine count under load; tick MSPT spiking when many mobs path simultaneously.

### Pitfall 5: Tracker packets sent off the owner (corrupts the outbound queue ordering)
**What goes wrong:** OPT-02 worker calls `p.client.Send` directly off-tick → races the bounded per-player outbound queue and the writeLoop.
**Why it happens:** It's tempting to emit packets where the diff is computed.
**How to avoid:** The worker produces a `[]packet` result; `trackerDiffReady.applyTo` calls `p.client.Send` on the owner. Note the `ChannelQueue` close-vs-send race already fixed in Phase 5 `[VERIFIED: STATE.md line 174]` — keep all sends owner-side.
**Warning signs:** `-race` on `net/queue.ChannelQueue`; out-of-order entity spawn/move packets.

### Pitfall 6: `.linear` breaking `.mca` world compatibility
**What goes wrong:** Switching the save codec to `.linear` makes existing `.mca` worlds unreadable, or a half-converted world has both formats and the loader picks wrong.
**Why it happens:** `.linear` is a different on-disk format (whole-region zstd vs per-chunk zlib sectors).
**How to avoid:** `loadOrGenerate` tries BOTH: prefer `.linear` if `r.<x>.<z>.linear` exists, else fall back to `.mca`, else generate. Write `.linear` only when configured. Keep `.mca` read support permanently (vanilla worlds). The v1 server `regionDir` is `""` (always-generate `[VERIFIED: main.go:124]`), so there is no existing world to break — but the loader must still be format-aware for when persistence is enabled.
**Warning signs:** A world that loaded yesterday returns all-air; a corrupt-region error on a valid `.mca`.

### Pitfall 7: cgo creeping in via a zstd dependency
**What goes wrong:** Picking a cgo-binding zstd library breaks the `CGO_ENABLED=0` Go-only build (a core project value: "no JVM, pure Go static binary").
**Why it happens:** Several zstd Go libs wrap the C library.
**How to avoid:** Use `github.com/klauspost/compress/zstd` (pure Go, no cgo) so the static-binary guarantee and the `golang:1.26` `-race` build both hold. Confirm `CGO_ENABLED=0 go build ./...` still works after adding it.
**Warning signs:** Build failures without a C compiler; the binary suddenly dynamically linking.

## Code Examples

### OPT-01 — submit-and-continue with a non-blocking pool
```go
// async.go — Source: ants/v2 README (NewPool + WithNonblocking + Submit).
import "github.com/panjf2000/ants/v2"

func newPathPool() *ants.Pool {
	// Non-blocking so a saturated pool drops the submit (mob keeps last action,
	// re-requests next tick) instead of stalling the owner — mirrors world.Worker.Request.
	p, _ := ants.NewPool(runtime.NumCPU(), ants.WithNonblocking(true))
	return p
}

// In navigation.requestPath, replace the inline computePath with:
if err := t.pathPool.Submit(func() {
	t.asyncIn2 <- pathReady{mobID: mobID, target: tgt, path: computePath(req)}
}); err != nil {
	n.pending = false // pool overloaded: leave the mob on its current path; retry next tick
}
```

### OPT-04 — xsync only at a genuine cross-boundary collection (if any)
```go
// Source: xsync/v4 README. Use ONLY if a worker must read the live store (prefer a snapshot instead).
import "github.com/puzpuzpuz/xsync/v4"

// Generics-only in v4: MapOf is an alias for Map.
entityIndex := xsync.NewMap[int32, *Entity]() // obstruction-free reads, lock-sharded writes
e, ok := entityIndex.Load(id)
entityIndex.Store(id, e)
```

### OPT-05 — the `.linear` codec behind the existing region call site
```go
// save/region/linear.go — NEW. Source: xymb-endcrystalme/LinearRegionFileFormatTools/linear.py.
// Header struct ">QBQbhIQ": magic(8) version(1) newestTimestamp(8) compressionLevel(1)
// chunkCount(2) completeRegionLength(4) hash/reserved(8). Body = zstd(headers[1024]*">II" + chunk bytes).
// Trailer = the 8-byte magic again.
const linearSignature uint64 = 0xc3ff13183cca9d9a
const linearVersion byte = 1
// Read: verify leading + trailing signature, zstd-decode the body, slice 1024 (size,timestamp)
// headers, then the concatenated chunk NBT blobs. Write: concat chunks, zstd-encode, frame.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Anvil `.mca` (per-chunk zlib, 4KB-sector padding) | `.linear` (whole-region zstd) | Leaf/LinearPaper, ~2021–present | ~50% disk savings (Overworld/Nether), ~95% (End); fewer IOPS on HDD; whole-file read/write `[CITED: xymb LinearRegionFileFormatTools README]` |
| Goroutine-per-task | Bounded recycling pool (`ants/v2`) | Established Go practice | Caps goroutine count, recycles workers — prevents the per-mob blowup |
| `sync.Map` / `RWMutex`+map under write contention | CLHT `xsync.Map` | xsync v3→v4 (generics-only) | Obstruction-free reads, lock-sharded writes; v4 dropped non-generic types (`MapOf` aliases `Map`) `[VERIFIED: CLAUDE.md version note]` |

**Deprecated/outdated:**
- xsync v3 non-generic API: v4 is generics-only. Copying v3 example code needs name adjustment (`MapOf` → `Map`). `[CITED: CLAUDE.md]`

## Runtime State Inventory

This is an additive code phase (new pools, new codec, swapped executors) — no rename/migration. Categories:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — `regionDir` is `""` (always-generate, no persisted world yet) `[VERIFIED: main.go:124]`. OPT-05 only matters once persistence is enabled. | None for v1 default; format-aware loader for when enabled |
| Live service config | None — server is a single self-contained Go binary, no external services | None |
| OS-registered state | None | None |
| Secrets/env vars | `SULFUR_DEBUG*` env triggers exist `[VERIFIED: main.go:138]` — pool wiring must not disturb them | None (debug stays off in production/tests) |
| Build artifacts | New deps (ants, xsync, klauspost/compress) added to `go.mod`/`go.sum` — run `go mod tidy` | `go mod tidy` after `go get` |

**Nothing to migrate:** verified — the v1 world is always-generated, so there is no existing `.mca` data to convert.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | build/test | ✓ | 1.26.1 (host) | — `[VERIFIED: go version]` |
| Docker | `-race` gate (host `CGO_ENABLED=0`) | ✓ | 29.4.3 | none needed — Docker is the sanctioned race path `[VERIFIED: docker --version; STATE.md:148]` |
| `golang:1.26` image | `-race` run | ✓ (used Phases 2–7) | 1.26 | — `[VERIFIED: 07-02-SUMMARY.md]` |
| `ants/v2` v2.12.1 | OPT-01/02/03/04 pools | ✓ (registry) | v2.12.1 | — `[VERIFIED: go list -m -versions]` |
| `xsync/v4` v4.5.0 | OPT-04 collections | ✓ (registry) | v4.5.0 | — `[VERIFIED: go list -m -versions]` |
| `klauspost/compress/zstd` | OPT-05 `.linear` | ✓ (registry, pure-Go) | latest | gzip fallback (worse ratio, keeps cgo-free) — see Q3 |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** zstd lib choice (pure-Go klauspost vs cgo) — pick pure-Go to preserve the static binary.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no external runner) |
| Config file | none — `go test` convention |
| Quick run command | `go test ./server/... ./world/... ./save/...` |
| Full suite command (race) | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/... ./world/... ./save/...` `[VERIFIED: 07-05-SUMMARY.md pattern]` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OPT-01 | Async path rejoins; late path to despawned mob is dropped, not crashed | unit + race | `go test ./server/ -run TestAsyncPath` | ❌ Wave 0 |
| OPT-02 | Async tracker diff emits same packets as the synchronous tracker | unit (golden vs `entityTracker`) | `go test ./server/ -run TestAsyncTracker` | ❌ Wave 0 |
| OPT-03 | Async spawn scan rejoins; cap re-checked on owner before add | unit + race | `go test ./server/ -run TestAsyncSpawn` | ❌ Wave 0 |
| OPT-04 | Chosen xsync collection round-trips; tick-only maps unchanged | unit | `go test ./server/ -run TestHotCollections` | ❌ Wave 0 |
| OPT-05 | `.linear` encode→decode round-trips a region; `.mca` still loads | unit (round-trip + cross-format) | `go test ./save/region/ -run TestLinear` | ❌ Wave 0 |
| OPT-06 | Every async subsystem `-race` clean under concurrent load | race (Docker) | full suite command above, `-count=10` | partial (existing `-race` gate) |

### Sampling Rate
- **Per task commit:** `go build ./... && go test ./server/... ./world/... ./save/...`
- **Per wave merge:** the Docker `-race` full suite (`-count=1`)
- **Phase gate:** Docker `-race -count=10` over `./server/... ./world/... ./save/...` green before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `server/async_test.go` — covers OPT-01/02/03 rejoin + the despawn/retarget drop (Pitfall 2)
- [ ] `server/async.go` — the pools + asyncResult types (production code, but the test seam lives here)
- [ ] `save/region/linear_test.go` — `.linear` round-trip + `.mca` cross-format (OPT-05, Pitfall 6)
- [ ] A concurrent stress test that submits many paths/spawns while the tick runs, to give `-race` something to catch (OPT-06)
- [ ] Framework install: none (stdlib testing); but `go get` the 3 deps + `go mod tidy`

## Security Domain

`security_enforcement` is not configured for this internal server core; the relevant controls are resource-exhaustion (DoS) bounds, which this phase must preserve.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | offline mode (v1); online auth is Phase 9 |
| V3 Session Management | no | — |
| V4 Access Control | no | command permission gate exists (Phase 7); unchanged here |
| V5 Input Validation | partial | the A* `maxVisited` budget + recompute cooldown bound untrusted-input-driven work `[VERIFIED: navigation.go:62, pathfinder.go:296]` — pools must NOT remove these |
| V6 Cryptography | no | — |

### Known Threat Patterns for this phase
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Pool saturation / goroutine blowup from many mobs pathing | Denial of Service | `ants` non-blocking bounded pool; drop submit on overload (Pitfall 4) |
| Unbounded result-channel growth | Denial of Service | Bounded result channel + non-blocking send (mirror `asyncBridge` buffer + `world.Worker` drop-on-full) |
| Path-flood from an unreachable target | Denial of Service | The existing recompute cooldown + `maxVisited` budget MUST survive the swap (do not bypass them in the async path) `[VERIFIED: navigation.go shouldRecomputePath]` |
| `.linear` 4GB region / decompression bomb | Denial of Service | Enforce the 4GB region cap + a sane decompressed-size limit on read `[CITED: linear format 4GB limit]` |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The async tracker (OPT-02) should send packets on the owner (not off-tick) because `p.client.Send` enqueues onto the bounded per-player queue that the writeLoop owns | Seam Map / Pitfall 5 | If `client.Send` were already concurrency-safe for multiple producers, the diff packets *could* be sent off-tick — but the Phase-5 ChannelQueue close-vs-send race fix `[VERIFIED: STATE.md:174]` argues for owner-side sends. Low risk; owner-side is strictly safe. |
| A2 | Most tick-owned collections can stay plain maps because every OPT snapshots on the owner | Seam Map OPT-04 / Pitfall 1 | If a chosen design has a worker read the live store, that specific collection needs xsync. The recommendation (snapshot, don't share) avoids this — but the planner should confirm no worker captures a live collection. |
| A3 | `klauspost/compress/zstd` (pure-Go) is the right zstd for `.linear` to keep `CGO_ENABLED=0` | Don't Hand-Roll / Pitfall 7 / Q3 | If its zstd output is incompatible with vanilla `.linear` readers, cross-tool interop breaks (but Sulfur reading its own `.linear` is fine). Verify byte-interop only if `.linear` files must be shared with Java tools. |
| A4 | OPT-05 can ship opt-in (default stays generate/`.mca`) since v1 `regionDir=""` | User Constraints / Pitfall 6 / Q2 | If the planner wants `.linear` as the default persistent codec, the loader must be format-aware from day one and a conversion path is needed. Recommended: opt-in for v1. |

## Open Questions

1. **Which collections (if any) genuinely need xsync?**
   - What we know: every OPT snapshots on the owner, so workers read copies, not live collections. CLAUDE.md says xsync only for collections "genuinely crossed by the async boundary."
   - What's unclear: whether any chosen design (e.g. an async tracker that reads `entityStore.near` live for performance) forces a live cross-boundary read.
   - Recommendation: default to **snapshot + plain map**; introduce `xsync.Map[int32,*Entity]` for `entityStore.byID` ONLY if a worker is designed to read it live or profiling shows the snapshot copy is a hotspot. Decide per-collection, not wholesale.

2. **Is `.linear` (OPT-05) the default save codec or opt-in?**
   - What we know: v1 `regionDir=""` → always-generate, no persisted world `[VERIFIED: main.go:124]`. `.mca` read support must stay for vanilla worlds.
   - What's unclear: whether the phase wants `.linear` writing on by default once persistence lands.
   - Recommendation: ship `.linear` as a **config flag** (read both, write `.linear` when enabled); keep `.mca` the compatibility default. This satisfies OPT-05 ("reduces disk I/O and footprint" — demonstrable via the round-trip + a size comparison test) without a forced migration.

3. **zstd library choice for `.linear`.**
   - What we know: vanilla `.linear` uses zstd; the project mandates a cgo-free static binary.
   - What's unclear: exact byte-interop requirements with Java `.linear` tools.
   - Recommendation: `github.com/klauspost/compress/zstd` (pure Go). If Sulfur only reads/writes its own `.linear`, interop is moot; if files must be exchanged with Java servers, add a cross-tool round-trip test.

4. **OPT-01 staleness window — how late is acceptable?**
   - What we know: REQUIREMENTS says "tolerated 1+ ticks late"; Leaf/Folia compute paths async and the mob continues its prior action until the path lands.
   - What's unclear: an explicit upper bound on lateness before a request is abandoned.
   - Recommendation: bound it via the existing recompute cooldown — if a `pathReady` arrives after the target changed, `applyTo` drops it (Pitfall 2) and navigation re-requests; no separate timeout needed. The mob simply keeps wandering/idling until a valid path lands, which is the vanilla-faithful behavior.

## Sources

### Primary (HIGH confidence)
- Codebase (this session): `server/tick.go`, `tick_phases.go`, `tracker.go`, `pathfinder.go`, `navigation.go`, `path_region.go`, `spawner.go`, `world/worker.go`, `world/manager.go`, `save/region/mca.go`, `persistence.go`, `cmd/sulfur/main.go`, `go.mod` — the exact seams, snapshot builders, and rejoin plumbing `[VERIFIED]`
- `.planning/STATE.md`, `.planning/ROADMAP.md`, `.planning/REQUIREMENTS.md` — TICK-05 discipline, the "swap behind pre-built seams" mandate, the `-race` Docker precedent `[VERIFIED]`
- `CLAUDE.md` Technology Stack — the verified xsync/ants/conc/x-sync table + "Stack Patterns by Variant" + "What NOT to Use" `[CITED]`
- `go list -m -versions` (this session) — ants v2.12.1, xsync v4.5.0, conc v0.3.0 latest `[VERIFIED]`
- LinearRegionFileFormatTools `linear.py` — the exact `.linear` binary layout (signature `0xc3ff13183cca9d9a`, header `">QBQbhIQ"`, per-chunk `">II"`, zstd body, trailer) `[CITED: https://github.com/xymb-endcrystalme/LinearRegionFileFormatTools]`

### Secondary (MEDIUM confidence)
- Leaf README + LinearPaper/LinearPurpur — `.linear` adoption, ~50%/~95% disk savings, whole-region read/write tradeoffs `[CITED]`
- `07-05-SUMMARY.md`, `07-02-SUMMARY.md`, `03-VALIDATION.md` — the exact Docker `-race` command + the single-owner race-clean-by-construction reasoning `[VERIFIED]`

### Tertiary (LOW confidence)
- None — every load-bearing claim is grounded in the codebase, CLAUDE.md, the version registry, or the linear-format source.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions registry-verified; stack pre-chosen in CLAUDE.md
- Seam map / architecture: HIGH — every seam, snapshot, and rejoin read directly from the current source this session
- Linear region format: HIGH — exact binary layout extracted from the canonical `linear.py`
- `-race` strategy: HIGH — the Docker gate is the established Phase 2–7 path
- xsync vs plain (OPT-04 specifics): MEDIUM — the *policy* is clear (snapshot, don't share); the *per-collection* decision depends on the planner's exact worker design (Q1)

**Research date:** 2026-06-24
**Valid until:** ~30 days (stable Go ecosystem; the seams are committed code and will not drift)
