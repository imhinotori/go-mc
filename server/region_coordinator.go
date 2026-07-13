package server

import (
	"log"
	"runtime/debug"
	"time"

	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/sourcegraph/conc"
)

// region_coordinator.go is the Phase-27 (Folia regionization) STEP-2: the conc fan-out/barrier
// coordinator at N=1. Plan 01 extracted the `region` struct (the WORLD-half of the tick state);
// this plan restructures tickOnce into the Folia coordinator shape — drain (global, already
// pre-step) → fan out the region tick(s) via conc → BARRIER (join) → cross-region/global
// post-phase on the coordinator → advance the shared gametime EXACTLY ONCE — WHILE STAYING N=1.
//
// THIS STEP IS BEHAVIOR-NEUTRAL BY CONSTRUCTION. At N=1 there is exactly ONE region in the
// fan-out, so r.tick(gt) runs the EXACT same per-region pipeline serially that today's tickOnce
// ran inline, the coordinator advances gametime once (the shared 50ms anchor — Pitfall 3), and
// the post-phase runs after the single region finishes. The observable phase order and every
// gameplay effect are identical — the #1 gate: the FULL existing server + world suite passes
// UNCHANGED + Docker -race clean.
//
// THE SEAM Plan 03 FILLS: the split between r.tick (the PER-REGION phases, steps 3-4 of the
// objective) and the coordinator post-phase (the CROSS-REGION tracker/movement/equipment/flush,
// step 5) is the explicit boundary N=2 fills with cross-region transfer + the cross-region
// tracker. At N=1 the boundary is a no-op seam: one region, one post-phase, one gametime advance.
//
// conc gives STRUCTURED panic propagation: a region whose tick panics is recovered per region by
// conc.WaitGroup and the panic value is re-raised on Wait() on the coordinator goroutine, where
// the hoisted recoverTick backstop catches it, logs the stack, and STILL advances gametime — so
// one region's panic does NOT hang the whole tick (the world never freezes / every player never
// disconnects). T-27-02 mitigation; TestRegionPanicIsolated is the proof.

// tick runs the PER-REGION pipeline for one logical tick (the FAN-OUT body, steps 3-4 of the
// coordinator objective). It is lifted from today's tickOnce middle — the PER-REGION phases in
// the SAME order: tickWorld → tickChunks → tickEntities → tickAI → tickPhysics →
// applyAsyncResults (the region's chunkReady drain + the global asyncIn2 drain, identical at
// N=1). At N=1 only()==r, so r.tick calls the existing *TickLoop phase methods via r.coord
// UNCHANGED — they still range t.only()'s store, which IS this region. Plan 03 threads `r`
// explicitly so each phase ranges THIS region's store.
//
// gt is the ONE shared tick number for this logical tick, passed in READ-ONLY by the coordinator
// (scheduled block ticks compare against it). The region NEVER advances gametime — only the
// coordinator does, exactly once, AFTER the barrier (Pitfall 3 / T-27-02-GT). Exactly one
// goroutine (the region's, == the coordinator's at N=1) mutates this region's store, so the
// per-region tick is -race clean by the same single-owner discipline as the pre-extraction
// TickLoop (TICK-05, multiplied by N).
func (r *region) tick(gt int64) {
	t := r.coord
	// Phase-27 STEP-3 (N=2): register THIS goroutine→region so only() routes every per-region phase
	// call site (physics/AI/spawner/handles' t.cur().entities / t.world() / t.cur().levelRandom)
	// to THIS region's store WITHOUT rewriting the ~200 sites. Cleared on exit so a transferred/joined
	// goroutine never leaks a stale region. The coordinator's own goroutine never registers here, so
	// the world-global + post phases see globalRegion via only()'s fallback.
	if t.currentRegion != nil {
		gid := curGoroutineID()
		t.currentRegion.Store(gid, r)
		defer t.currentRegion.Delete(gid)
	}

	// Test-only seam (nil in production): inject a panic (TestRegionPanicIsolated) or observe the
	// barrier (TestCoordinatorBarrier / the parallelism probe) on the region's goroutine, before the
	// per-region phases.
	if r.tickHook != nil {
		r.tickHook()
	}

	// The PER-REGION entity phases, in the EXACT order today's tickOnce ran them (the gameplay order
	// is the load-bearing contract — TestTickPhaseOrder). These are the phases that are safe to run
	// CONCURRENTLY across regions: each ranges ONLY THIS region's entity store (via only(), now
	// resolving to r) and only READS the shared world (block reads for AI/physics) — the world is
	// mutated solely on the coordinator (tickWorld/tickChunks/chunkReady), never inside the fan-out,
	// so concurrent reads here are race-clean. The world-global phases (tickWorld/tickChunks), the
	// player/item seams (tickEntities), and the async rejoin (applyAsyncResults) run on the
	// coordinator (region_coordinator.go tickOnce) — quiescent, where a cross-region read is legal.
	t.tickAI()      // PER-REGION: serverAiStep over r.entities + naturalSpawn (the plugin goal callbacks)
	t.tickPhysics() // PER-REGION: moveEntity over r.entities

	// Cross-region transfer DETECTION (queue only — applied at the barrier by the coordinator). The
	// AI/physics step above may have moved an entity across the region seam; record the hand-off
	// intent here on the region's goroutine (a pure scan + append to THIS region's pendingTransfers,
	// no cross-region mutation), and applyCrossRegionTransfers moves it A→B at the quiescent barrier.
	r.detectTransfers()

	_ = gt // the shared anchor the coordinator passes read-only (scheduled ticks read t.gametime).
}

// tickOnce runs ONE logical tick as the Folia coordinator (Phase-27 STEP-2). It is the rewrite of
// the pre-regionization inline pipeline into: resolve global inputs → read the ONE shared gametime
// → FAN OUT the region tick(s) via conc → BARRIER (wg.Wait) → cross-region/global POST-PHASE on
// the coordinator (all regions quiescent) → advance the shared gametime EXACTLY ONCE → on_tick →
// recordMSPT.
//
// At N=1 this is behavior-neutral: the single region runs the same per-region pipeline serially,
// the post-phase runs after it finishes, and gametime advances once (the shared 50ms anchor).
//
// recoverTick (deferred FIRST so it is the OUTERMOST defer) is the hoisted panic backstop: it
// catches a panic surfaced anywhere in the tick — INCLUDING one re-raised by wg.Wait() from a
// region goroutine (conc re-panics on the calling goroutine, so the recover here catches it) —
// logs the stack, advances gametime so the anchor never stalls, and records MSPT. A region panic
// is therefore ISOLATED: the tick does not hang, the loop survives, and gametime still advances
// (T-27-02). recoverTick advances gametime on the panic path; the normal path advances it once at
// the end — structured so EITHER path advances exactly once, never both (T-27-02-GT).
func (t *TickLoop) tickOnce() {
	start := t.clock.Now() // capture via the injectable clock for MSPT (TICK-06)
	t.profReset()          // DIAGNOSTIC: reset the opt-in per-phase timer (no-op when off)

	// Hoisted backstop: a panic in ANY phase — or one surfaced by wg.Wait() from a region —
	// must not kill the tick goroutine (that would freeze the world + disconnect every player).
	// Deferred first => runs last => outermost. recoverTick handles the panic path's gametime++
	// and recordMSPT; the normal path below does its own (the `advanced` guard makes it exactly
	// once). Pass start so the panic path can still record this tick's MSPT.
	advanced := false
	defer t.recoverTick(start, &advanced)

	t.resolveSubtickInputs() // GLOBAL pre-phase (per-player input; touches no region store).

	// --- WORLD-GLOBAL phases on the COORDINATOR (single-threaded, BEFORE the fan-out). These MUTATE
	// the shared world (scheduled blocks/fluids/chunk-save; the per-player chunk-streaming requests),
	// so they MUST run exactly once outside the parallel section — running them per-region would race
	// the shared ChunkManager. only() falls back to globalRegion on the coordinator goroutine, so they
	// operate over the (shared) world exactly as before. ---
	// WORLD-GLOBAL weather cycle (weather.go — ServerLevel.advanceWeatherCycle): the rain/thunder
	// timers + level ramp + the GameEvent broadcasts. It is a single world-global phase (one WeatherData
	// per world), so it runs ONCE here on the coordinator, BEFORE the region fan-out — never per-region
	// (a per-region run would advance the cycle N times + double-broadcast). Its RNG draws use
	// globalRegion.levelRandom, and the pig oracle never calls tickOnce, so the pinned per-entity streams
	// are unperturbed. Placed first among the world-global phases, mirroring ServerLevel.tick where
	// advanceWeatherCycle runs early. CITE: ServerLevel.advanceWeatherCycle.
	t.profPhase("tickWeather", t.tickWeather)

	// WORLD-GLOBAL sleep handling (sleep.go -- the ServerLevel.tick all-players-asleep block): the
	// players_sleeping_percentage gate -> skip the night to dawn + wake everyone + clear the storm. Like
	// tickWeather it is a single world-global phase (one SleepStatus per world), so it runs ONCE here on
	// the coordinator BEFORE the region fan-out -- never per-region. Placed right after the weather cycle,
	// mirroring ServerLevel.tick where the sleep block runs early (right after advanceWeatherCycle). It
	// draws NO RNG and the pig oracle never calls tickOnce, so the pinned per-entity streams are
	// unperturbed. CITE: net.minecraft.server.level.ServerLevel.tick (sleep block).
	t.profPhase("tickSleep", t.tickSleep)

	t.profPhase("tickWorld", t.tickWorld)   // scheduled blocks/fluids + raid + random/thunder over the shared world
	t.profPhase("tickChunks", t.tickChunks) // per-player ring → world requests (≈ ServerChunkCache.tick / chunkSource)

	// RUN BLOCK EVENTS (ServerLevel.runBlockEvents): vanilla drains the block-event queue AFTER
	// getChunkSource().tick() and BEFORE the entity pass (bytecode: "chunkSource" getChunkSource().tick
	// at pc 337, then "blockEvents" runBlockEvents() at pc 360, then "entities" at pc 418). The piston
	// block-event drain (triggerEvent → PistonBaseBlock.triggerEvent starts a moving-piston BE) is our
	// runBlockEvents; it must sit HERE, after chunkSource (tickChunks) and before tickEntities — NOT
	// inside tickWorld where it ran before #42 (which put it before chunkSource, wrong). Per-region
	// queue, wrapped so cur() resolves the owning region's piston event queue. Cheap no-op when empty.
	t.forEachRegion(func(r *region) { t.drainPistonBlockEvents() })

	// tickEntities (the player/item seams) runs on the COORDINATOR too: it iterates the GLOBAL player
	// list + the per-region entity stores (syncPlayerEntities moves each player within its OWNING
	// region; tickItems spans regions), so it must be single-threaded (a per-region fan-out would
	// double-process the global player list). It keeps its fixed slot BEFORE tickAI (the trace order
	// contract — TestTickPhaseOrder).
	t.profPhase("tickEntities", t.tickEntities)

	gt := t.gametime // the ONE shared tick number every region reads this tick (Pitfall 3 /
	// T-27-02-GT): a single value the coordinator passes into each region.tick, read-only.

	// Phase-27 N=2 SPAWN-CAP SNAPSHOT: compute the GLOBAL per-MobCategory live count map NOW, while
	// every region is quiescent (the fan-out has not started), so naturalSpawn's pre-submit cap gate
	// reads a race-free cross-region count per category during the parallel fan-out instead of
	// ranging another region's live store. The cap spans all players/regions; the apply-time re-check
	// (also cross-region, at the barrier) remains the authoritative anti-flood.
	// countByCategoryAcrossRegions is safe here (no region is ticking) and is called ONCE: it buckets
	// every MobCategory in a single pass, so the resulting map covers every entry in
	// spawningCategories (MONSTER, CREATURE, AMBIENT, AXOLOTLS, UNDERGROUND_WATER_CREATURE,
	// WATER_CREATURE, WATER_AMBIENT). This replaces the prior two-int (CREATURE+MONSTER) layout
	// that silently fell back to the CREATURE count for AMBIENT/AXOLOTLS/WATER_*, corrupting the
	// cap gate for those categories (Phase 35-02/P1 audit).
	t.spawnLiveCategorySnapshot = t.countByCategoryAcrossRegions()

	// --- FAN OUT: each region ticks its OWN entity store in PARALLEL (TICK-05 per region) — the
	// per-region entity phases (tickAI + tickPhysics + detectTransfers). conc.WaitGroup.Go spawns the
	// region tick under a per-region recover; wg.Wait re-raises the FIRST region panic's value + stack
	// on this goroutine (structured propagation), where the recoverTick backstop catches it — one
	// region's panic does NOT hang the tick (T-27-02). The regions touch ONLY their own store + READ
	// the shared world, so the fan-out is race-clean by construction (the Docker -race gate proves it).
	// GLOBAL-BROADCAST SAFETY AUDIT (Phase-27 STEP-3, Pitfall 6) — the read-only-during-fan-out
	// invariant: broadcastBlockUpdate / chat / playerlist iterate t.players from within region-owned
	// events (a plugin set_block fired from a goal callback in the fan-out). The AUDIT (grep for
	// `t.players =` / `append(t.players` / `drainRegistrations`): EVERY mutation of t.players /
	// clientIndex lives in drainRegistrations + removePlayer, which run ONLY on the coordinator,
	// BEFORE the fan-out (Run/advance call drainRegistrations, then tickOnce). NOTHING inside the
	// fan-out (tickAI/tickPhysics and the goal callbacks they invoke) mutates t.players — it only
	// READS it (naturalSpawn's spawnableColumns/spawnRefY, broadcastBlockUpdate's send loop). So
	// t.players is STABLE during the region ticks and a region thread READING it (never mutating) is
	// race-clean — no broadcast needs deferring. Each player's sentChunks is likewise only WRITTEN by
	// flushOutbound (coordinator, post-barrier) and only READ during the fan-out, so there is no
	// concurrent read+write. TestGlobalBroadcastSafeFromRegion proves the read path; the Docker -race
	// gate (Task 4) proves the read-during-fan-out crossing.
	stopFanout := t.profStart("regionFanout(AI+physics)")
	var wg conc.WaitGroup
	for _, r := range t.regions {
		r := r
		wg.Go(func() { r.tick(gt) })
	}
	wg.Wait() // BARRIER: no region mutates its store past this point this tick.
	stopFanout()

	// --- CROSS-REGION / GLOBAL POST-PHASE (coordinator, all regions quiescent → safe to read AND
	// write across regions). ---

	// Cross-region entity TRANSFER FIRST (before the tracker/movement read across regions): drain
	// each region's pendingTransfers and move every boundary-crossing entity A→B (the *Entity adopted
	// by the new region, ai/nav/scratch travelling). 27-RESEARCH Pattern 4.
	t.applyCrossRegionTransfers()

	// Cross-region DAMAGE drain (Phase-29, the FIRST true cross-region write — Pitfall 2 / T-29-02):
	// drain each region's pendingDamage and apply every boundary hit to its OWNER region (re-resolved by
	// id, drop if gone), inside withRegion(owner) so the mob-store write + on_damage emit are region-
	// correct. Quiescent here (every region joined), so it is -race clean. Mirrors the transfer drain
	// above; ordered right after it (the transfer establishes post-transfer ownership before the damage
	// owner re-resolve, so a victim that just transferred is hit in its NEW region).
	t.applyCrossRegionDamage()

	// BLOCK ENTITIES (ServerLevel.tickBlockEntities): vanilla ticks block entities AFTER the entity
	// pass (bytecode: "entities" at pc 418, then "blockEntities" tickBlockEntities() at pc 484). The
	// whole global BE cluster (furnace/crafter/hopper/beacon/spawner/piston/... — see tickBlockEntities)
	// belongs at this post-fan-out quiescent slot (all regions joined → the BE world mutations are
	// race-clean, same window as the transfer/damage drains). Before #42 the cluster ran inside tickWorld
	// BEFORE entities (wrong order — a hopper/spawner/piston saw last tick's entity positions, and a
	// piston BE advanced a tick early relative to the entities riding it). CITE: ServerLevel.tick
	// blockEntities section.
	t.profPhase("tickBlockEntities", t.tickBlockEntities)

	// The async rejoin runs on the coordinator now (quiescent): the chunkReady drain (world mutation,
	// globalRegion's asyncIn) + the asyncIn2 entity results (pathReady/spawnCandidatesReady), which
	// re-resolve the OWNING region by id and apply there (drop if no region owns it — Pitfall 1).
	t.profPhase("applyAsyncResults", t.applyAsyncResults)

	// BATCHED RELIGHT (fluid-spread stall fix): drain the per-tick relight dirty set built by
	// relightChanged (recorded on every light-affecting SetBlock during tickWorld's fluid/block edits,
	// tickBlockEntities, and applyAsyncResults' chunk-drain), recompute each affected column ONCE over
	// this tick's FINAL block states, and broadcast one ClientboundLightUpdate per changed column.
	// Placed AFTER tickBlockEntities + applyAsyncResults (so every block/fluid/BE/async edit this tick is
	// reflected in the light) and BEFORE tickEntityMovement/tracker.Tick (so the light packet goes out in
	// the SAME tick as the movement/tracker broadcast). Coordinator-only (single-threaded, post-barrier)
	// -- the same quiescent window as the other global post-phases. This replaces the former per-SetBlock
	// inline RelightEdit (9 full-column light recomputes per edit -> 100-140ms stalls during fluid
	// spread) with vanilla's once-per-tick coalesced runLightUpdates + one ClientboundLightUpdate per
	// column. CITE: LevelChunk.setBlockState checkBlock -> ThreadedLevelLightEngine.runLightUpdates ->
	// ChunkMap ClientboundLightUpdate (once per tick).
	t.profPhase("flushRelight", t.flushRelight)

	// RIDE (passenger.go): re-position every vehicle's passengers AFTER physics moved the vehicles and
	// AFTER the cross-region transfer/async rejoin (so the vehicle is in its post-transfer region and at
	// its settled position), and BEFORE tickEntityMovement/tracker.Tick so this tick's ridden positions
	// are the ones broadcast. This is the coordinator-side analogue of Entity.rideTick's positionRider
	// (it touches the GLOBAL player list + per-region entity stores, so it must run single-threaded at
	// the quiescent barrier, like tickEntities). A store with no vehicles is a cheap no-op (the pig
	// oracle's world has none, so its RNG stream is unperturbed).
	t.rideTickVehicles()

	t.profPhase("tickEntityMovement", t.tickEntityMovement) // GAMEPLAY-07: ServerEntity.sendChanges → delta move packets to trackers
	t.profPhase("tickEquipment", t.tickEquipment)           // GAMEPLAY-07: detectEquipmentUpdates → SetEquipment to trackers
	t.trace("tracker.Tick")
	t.profPhase("tracker.Tick", t.tracker.Tick) // cross-region at the barrier (near() spans region seams — Plan 03)
	t.profPhase("flushOutbound", t.flushOutbound)

	// Advance the shared gametime EXACTLY ONCE per logical tick — the coordinator is the SINGLE
	// advancer of the shared 50ms anchor (TICK-02 / Pitfall 3). The `advanced` flag makes the
	// normal path mutually exclusive with the recoverTick panic path (T-27-02-GT: never twice).
	t.gametime++
	advanced = true

	// PLUGIN-02 on_tick seam: the ONE per-tick emit, fired ONCE per tick TOTAL on the coordinator
	// (the GLOBAL thread — not per region) at the END of tickOnce after gametime++. Emit's
	// zero-subscriber guard makes it free on a server with no on_tick hook. Nil-guarded.
	if t.plugins != nil {
		t.plugins.Emit(host.EventTick, host.TickEvent{Tick: int(t.gametime)})
	}

	// TIME SYNC (time.go): every 20 ticks broadcast the gameTime-only SetTime packet to all players
	// so the vanilla client clock stays aligned (it otherwise free-runs from login). Mirrors
	// MinecraftServer.tickChildren: if (tickCount % 20 == 0) forceGameTimeSynchronization(). Runs on
	// the coordinator after gametime++ (all regions quiescent), so ranging t.players is race-clean --
	// the SAME single-threaded post-barrier window flushOutbound uses. CITE:
	// MinecraftServer.tickChildren (timeSync) + forceGameTimeSynchronization.
	if t.gametime%20 == 0 {
		t.broadcastTimeSync()
	}

	total := t.clock.Now().Sub(start)
	t.profDump(total)      // DIAGNOSTIC: on a slow tick, log the worst phases (no-op when off / fast)
	t.recordMSPT(total)    // publish the read-only telemetry snapshot (TICK-06)
}

// recoverTick is the hoisted per-tick panic backstop (Phase-27 STEP-2, lifted from the inline
// recover that lived in the pre-regionization tickOnce). It runs as the OUTERMOST deferred call,
// so it catches a panic from ANY phase — including one re-raised by conc's wg.Wait() from a region
// goroutine (conc re-panics on the calling/coordinator goroutine, so this recover catches it). On
// a panic it logs the stack, advances gametime so the anchor (TICK-02) does not stall on a bad
// tick, and records this tick's MSPT — so a region panic is ISOLATED: the tick does not hang and
// the loop survives (T-27-02). It is NOT a license to ignore panics: a logged panic is a real bug
// to fix; one bad region must never take down the server.
//
// EXACTLY-ONCE gametime advance (T-27-02-GT): `advanced` is the normal path's flag. If the normal
// path already did gametime++ (advanced==true) before the panic, recoverTick does NOT advance
// again; if the panic happened BEFORE the normal advance (advanced==false), recoverTick advances
// once. So gametime advances exactly once per tick whether or not a region panicked — never twice.
func (t *TickLoop) recoverTick(start time.Time, advanced *bool) {
	r := recover()
	if r == nil {
		return // no panic: the normal path already advanced gametime + recorded MSPT.
	}
	log.Printf("tick panic recovered (gametime=%d): %v\n%s", t.gametime, r, debug.Stack())
	if !*advanced {
		t.gametime++ // advance once so the anchor does not stall — but only if the normal path
		// did not already advance (exactly-once: never double-advance on a late panic).
		*advanced = true
	}
	// Record this tick's MSPT even on the panic path so the telemetry ring does not skip a
	// bad tick (the pre-regionization recover did not, but the loop's recordMSPT is cheap and
	// keeps the rolling window contiguous). Uses the same injectable clock as the normal path.
	t.recordMSPT(t.clock.Now().Sub(start))
}
