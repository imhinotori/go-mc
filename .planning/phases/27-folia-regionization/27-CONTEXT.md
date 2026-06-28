# Phase 27: Folia regionization — Context

**Gathered:** 2026-06-28
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 27-RESEARCH.md (HIGH on the Sulfur map; MEDIUM on Folia internals — REGION-01 is "Folia-style", not "Folia-identical")
**Requirement:** REGION-01

<domain>
## Phase Boundary

**Delivers:** the world ticks in PARALLEL independent regions (Folia model) instead of ONE tick goroutine, AND the plugin call seam + entity API become region-aware (a hook for an entity in region R runs on R's thread). This is the perf payoff ("ultra-efficient" at scale) and the last v3 deferral folded into v4.

**THE INVARIANT to preserve:** every gameplay behavior stays identical (the 50ms game-time anchor, the 1:1 ported logic, the deterministic-per-seed contract) — regionization is a CONCURRENCY change layered over the faithful logic, the ONLY permitted deviation per CLAUDE.md. -race clean is non-negotiable.

**OUT:** the final visual/perf gate → Phase 28 (Phase 27 proves regions tick in parallel + -race; Phase 28's perf bench measures the scaling).
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Incremental path — N=1 extract → barrier → N=2 (operator-directed; the research's safest path)
Build in 3 independently -race-verifiable steps (do NOT jump to full dynamic multi-region):
1. **Extract the `region` struct at N=1** — pull the per-region-owned state (entityStore, ChunkManager, scheduled ticks, levelRandom) out of TickLoop into a `region` struct, with ONE region holding everything. Behavior-NEUTRAL (the single region == today's TickLoop). -race green, all existing tests pass unchanged. This is the refactor that makes regionization possible without changing behavior.
2. **Add the coordinator fan-out/barrier at N=1** — a coordinator drives the region tick(s) via a fan-out + per-tick barrier (conc), still N=1 (one region in the fan-out). Proves the barrier discipline + the global/region split with zero behavior change.
3. **Flip to N=2** — a static chunk→region hash (2 regions), cross-region entity TRANSFER at the barrier, the cross-region tracker, and the region-aware plugin Emit. THE GATE: 2 regions tick concurrently (proven), -race clean, a plugin hook runs on the correct region's thread.
Dynamic region merge/split (real Folia growing/merging regions) is OUT — N=2 static hash proves the seam; dynamic regionization is a future scaling step.

### The per-region / global / cross-region-at-the-barrier split (from research — the central design)
- **PER-REGION (each region-owner thread owns):** the `entityStore`, the `ChunkManager` (its chunks), the scheduled block/fluid ticks, the `levelRandom`. Each region ticks these in parallel.
- **GLOBAL (a SEPARATE global-region thread — operator-directed):** the `EntityIDAllocator`, the player session + network (the player list, keepalive, the socket writers), the FROZEN `*host.Manager` plugin registry (load-once-shared — the Phase-21 frozen guarantee makes the callables safe across region threads), the `on_tick` GLOBAL event, console commands, the GAMETIME anchor (ONE shared 50ms anchor — all regions read it, never advance it independently). The global thread ticks the server-wide state; it syncs with the regions at the barrier.
- **CROSS-REGION-AT-THE-BARRIER:** the entity TRACKER (trackRange ≈ 6 columns ≈ 96 blocks crosses region seams), `broadcastBlockUpdate`, chat/playerlist broadcasts. These read across regions and run at the synchronization point between region ticks (after all regions finish their parallel phase, before the next tick).

### The barrier — conc (operator-directed)
- Use `github.com/sourcegraph/conc` for the per-tick fan-out/join barrier (scoped WaitGroup + panic propagation — the pattern CLAUDE.md blesses for tick-bounded fan-out). NOT yet in go.mod — ADD it (pure-Go, CGO=0 OK; confirm). A region panic propagates structured (a recover per region so one region's panic doesn't hang the whole tick — conc gives this; a raw WaitGroup would need manual per-region recover).
- The coordinator: fan out the N region ticks (conc pool), join at the barrier, then run the cross-region phase (tracker/broadcasts) on the owner/global, then advance the shared gametime ONCE.

### The region-aware plugin seam (the Phase-specific deliverable)
- The plugin hooks (Emit, the Phase-23 declared-mob goals, the Phase-24 plugin pig) run on THE tick goroutine today. After regionization, a hook for an entity in region R runs on R's THREAD.
- **The thin-id handle pattern (Phase 23) is what makes this safe:** the handle carries an entity id + re-resolves on whatever thread owns the entity. Emit becomes region-aware — an entity-scoped event (spawn/death/damage for an entity in R) fires on R's thread; the GLOBAL on_tick fires on the global thread. The frozen `*host.Manager` registry is shared (load-once frozen callables are safe across threads — Phase 21); only the HANDLES resolve against the CALLING region's owned state.
- Map: per-entity Emit → the owning region's thread; on_tick → the global thread; block break/place → the region owning that chunk.

### Cross-region entity transfer
- An entity walking from region A to region B hands off ownership AT THE BARRIER (the safe sync point): removed from A's store + added to B's store, on the synchronization point between region ticks. The nav/physics/plugin-goal state travels with it (the goal struct is per-entity; the frozen callables are shared). Faithful to Folia's transfer (simplified for the static N=2 hash).

### The risks (research flagged — the planner must handle)
- The async rejoin (`pathReady`/`applyTo(*TickLoop)`) must route to the OWNING region, not "the" TickLoop — the applyTo signature breaks; thread the owning region.
- The tracker is INHERENTLY cross-region (96-block range crosses seams) — it runs at the barrier reading all regions.
- Gametime MUST stay ONE shared anchor (the 50ms decision) — regions read it, the coordinator advances it once per tick.
- Handles re-resolve against the region's store (the thin-id pattern handles this).
- Global broadcasts read `t.players` from region threads — route through the global thread.

### Where the code lives
- The `region` struct + the coordinator live in `server` (they own the tick-owned types). The conc barrier + the chunk→region hash + the transfer + the region-aware Emit are server-internal. Keep the global/region split explicit so Phase 28's bench can measure parallel scaling.
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 27 row + gate (parallel regions, region-aware plugin seam, "regionize a WORKING single-thread seam — don't design around regions first").
- `.planning/REQUIREMENTS.md` — REGION-01 full.
- `.planning/phases/27-folia-regionization/27-RESEARCH.md` — HIGH-confidence Sulfur map: the per-region/global/cross-region responsibility table (named field-by-field from the real TickLoop/entityStore/ChunkManager/host.Manager); conc NOT in go.mod (stdlib fallback noted); the thin-id handle = the Folia-safety primitive; the 5 pitfalls (async-rejoin-routing, tracker-cross-region, gametime-shared, handle-resolve, global-broadcast); the safest incremental path (N=1 extract → barrier → N=2).
- The tick + ownership code: `server/tick.go` (TickLoop, tickOnce, drainRegistrations, the single-owner discipline), `server/tick_phases.go` (the per-entity loops), `server/async.go` (asyncIn2, the ants pools, pathReady/applyTo), the entity store (entities.add/get/move/near), `world/manager.go` (ChunkManager), plugin/host (Manager.Emit, the frozen registry), the Phase-23 thin handles (server/plugin_entity.go), the Phase-24 plugin pig.
- `CLAUDE.md` — TICK-05 single-owner (what regionization PARALLELIZES into N owners), the 50ms gametime anchor (MUST stay shared), the concurrency stack (xsync for the region map, conc for the barrier, ants for the pools), -race non-negotiable, push development, no Claude attribution.
- `PROJECT.md` — the Leaf/Folia architectural inspiration; the concurrency-ready-from-day-one decision this phase finally cashes in.
</canonical_refs>

<specifics>
## Specific Ideas

- Step 1 (extract region @ N=1) is a pure refactor — the test that it's behavior-neutral: the FULL existing server suite passes unchanged + -race green. This is the safety net for the whole phase.
- The chunk→region hash (N=2): a simple static `region = hash(chunkX,chunkZ) % 2` (or a spatial split) — deterministic, no dynamic merge. xsync.Map for the chunk→region lookup if concurrent.
- The barrier test: 2 regions tick concurrently (assert parallelism — e.g. both region goroutines are in their tick phase simultaneously), -race clean.
- The region-aware-Emit gate: spawn an entity in region R, assert its declared-mob goal callback runs on R's goroutine (not the global, not region 0) — the plugin-hook-on-correct-region proof.
- The transfer test: an entity crosses the region boundary → ownership moves A→B at the barrier → it keeps ticking on B's thread, -race clean, no double-tick / no drop.
- conc: confirm pure-Go/CGO=0; add as a plain require. The barrier = conc's scoped WaitGroup/pool with panic propagation.

## Open items the planner resolves
- The exact `region` struct boundary (which TickLoop fields move to region vs stay global) — step 1.
- The chunk→region hash function (modulo vs spatial) — step 3.
- The coordinator's tick sequence (fan-out regions → barrier → cross-region phase → advance gametime).
- The async-rejoin re-routing (thread the owning region into applyTo).
- The conc-vs-stdlib final call (operator picked conc; confirm pure-Go).
</specifics>

<deferred>
## Deferred Ideas

- Dynamic region merge/split (real Folia growing/merging regions) — N=2 static hash proves the seam; dynamic is a future scaling step.
- N>2 regions / per-core region scaling — the N=2 proof generalizes; tuning is post-gate.
- Region-aware persistence (each region flushes its own chunks) — the SUB-PERSIST loop exists; wiring per-region flush is a follow-up if not trivially in scope.
- The final perf benchmark proving regions SCALE → Phase 28.
</deferred>

---

*Phase: 27-folia-regionization*
*Context gathered: 2026-06-28 via operator decisions + v4-PLAN.md + 27-RESEARCH.md*
