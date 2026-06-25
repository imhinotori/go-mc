package server

import (
	"log"
	"runtime/debug"

	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// tickOnce runs ONE logical tick: the explicit, fixed-order phase pipeline (TICK-01).
// drainInbound is intentionally NOT here — Run drains inbound once per wake before
// calling tickOnce, kept separate so input is drained per wake, not per catch-up
// step. Each phase is an empty stub today (later phases fill them); the call ORDER
// is the load-bearing contract asserted by TestTickPhaseOrder.
func (t *TickLoop) tickOnce() {
	start := t.clock.Now() // capture via the injectable clock for MSPT (TICK-06)

	// Resilience guard: a panic in any phase (a malformed mob, a bad packet build, a nil deref in
	// new AI/spawn code) must NOT kill the tick goroutine — that would freeze the whole world and
	// disconnect EVERY player (keepalive stops). Recover, log the stack, and let the loop continue
	// to the next tick. This is a safety net, not a license to ignore panics: a logged panic is a
	// real bug to fix, but one bad mob should never take down the server.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tick panic recovered (gametime=%d): %v\n%s", t.gametime, r, debug.Stack())
			t.gametime++ // still advance time so the anchor (TICK-02) does not stall on a bad tick
		}
	}()

	t.resolveSubtickInputs() // no-op slot in this plan; 03-02 fills the subtick buffer
	t.tickWorld()            // Phase 4 fills
	t.tickChunks()           // Phase 4 fills
	t.tickEntities()         // Phase 6 fills
	t.tickAI()               // Phase 7 fills
	t.tickPhysics()          // Phase 6 fills
	t.applyAsyncResults()    // NO-OP today (asyncIn nil); Phase 8 drains result channels here
	t.trace("tracker.Tick")  // record the tracking phase at its call site
	t.tracker.Tick()         // synchronous stub today; Phase 8 swaps the executor
	t.flushOutbound()        // enqueue clientbound via Client.Send (no-op until players join)

	t.gametime++                           // EXACTLY once per logical tick — anchors TICK-02
	t.recordMSPT(t.clock.Now().Sub(start)) // publish the read-only telemetry snapshot (TICK-06)
}

// applyAsyncResults is the async rejoin seam (TICK-05). It drains BOTH result channels on the
// OWNER goroutine, NON-BLOCKINGLY, applying each immutable result via applyTo — WITHOUT reordering
// the pipeline (its slot and the surrounding phase order are UNCHANGED, so TestTickPhaseOrder
// still passes):
//
//   - asyncIn  is the Phase-4 chunkReady bridge (nil until SetWorld; a nil channel makes its
//     drain a genuine no-op — the seam just EXISTS in the right slot before a world is wired).
//   - asyncIn2 is the Phase-8 compute-pool rejoin channel (OPT-04/OPT-06), always non-nil from
//     NewTickLoop; the per-subsystem ants-pool workers send their immutable asyncResult here and
//     the owner applies it. This is the SECOND channel — kept separate so the Phase-4 wiring is
//     untouched (purely additive).
//
// Both drains are select-with-default loops: take everything currently queued, then return —
// NEVER park the tick on either channel (T-8-03). The worker only COMPUTED an immutable result;
// the OWNER performs the only mutation, here, inside applyTo (the chunkReady discipline,
// generalized).
func (t *TickLoop) applyAsyncResults() {
	t.trace("applyAsyncResults")

	// Drain the Phase-4 chunkReady bridge (skipped cheaply when no world is wired).
	if t.asyncIn != nil {
		for {
			select {
			case r := <-t.asyncIn:
				r.applyTo(t) // applied by the owner; the worker only computed an immutable result
			default:
				goto phase8 // nothing queued on asyncIn: move to the Phase-8 channel
			}
		}
	}

phase8:
	// Drain the Phase-8 compute-pool results (OPT-01/02/03 feed this; idle until a subsystem is
	// swapped, but always present so the drain is live from day one). A nil asyncIn2 is defensive
	// only — NewTickLoop always constructs it, so it is non-nil in production and tests.
	if t.asyncIn2 == nil {
		return
	}
	for {
		select {
		case r := <-t.asyncIn2:
			r.applyTo(t) // owner applies the immutable Phase-8 result; no async state mutation
		default:
			return // non-blocking drain: take what's queued, never park the tick
		}
	}
}

// resolveSubtickInputs drains each player's bounded subtick buffer in CHRONOLOGICAL
// order (by server-arrival stamp) and resolves each input through the stub applyInput
// (TICK-03). This is the CS2-style separation of input RESOLUTION (subtick-precise,
// here) from broadcast RATE (the vanilla 20/s flush, in flushOutbound): rapid input
// sequences resolve in the order they arrived rather than collapsing to one tick. The
// real movement/collision/hit-detection math behind applyInput is deferred to Phase 6
// — Phase 3 proves only the timestamped, chronological, vanilla-rate contract. Runs on
// the owner goroutine over tick-owned state, so it is -race clean by construction.
func (t *TickLoop) resolveSubtickInputs() {
	t.trace("resolveSubtickInputs")
	for _, p := range t.players {
		for _, in := range p.subtick.drain() { // chronological, then emptied
			t.applyInput(p, in)
		}
	}
}

// tickWorld advances world/block-tick state. Plan 17-01 wires the GAMEPLAY-05 fluid pass
// (tickFluids) here, INSIDE this existing phase, so no new phase is added to the fixed tick
// order (TestTickPhaseOrder stays green). tickFluids lives in fluid.go — a 17-01 stub that
// 17-02 (GAMEPLAY-05) overwrites with the real FlowingFluid port; this call site stays
// Wave-1-owned and is NOT edited by 17-02.
func (t *TickLoop) tickWorld() {
	t.trace("tickWorld")
	t.tickFluids()
}

// tickChunks issues the per-player chunk requests for this tick (WORLD-05). For each
// player it walks the center-out needed ring out to the player's CLAMPED view distance
// and, for every column still Empty, marks it Loading and issues exactly one
// worker.Request. It NEVER re-requests a Loading/Ready column, so re-walking the same
// bounded ring every tick (position spam, threat T-4-06) issues no duplicate work; the
// worker's singleflight collapses any cross-player overlap (threat T-4-02). The whole
// ring is bounded by the server clamp, so an untrusted client cannot make the request
// set unbounded (threat T-4-01). Runs on the owner goroutine over tick-owned state.
func (t *TickLoop) tickChunks() {
	t.trace("tickChunks")
	if t.world == nil || t.worker == nil {
		return // no world wired (Phase-3-style tests / pre-SetWorld): cheap no-op
	}
	for _, p := range t.players {
		ring := centerOutRing(p.center, p.viewDist)
		for _, pos := range ring {
			if t.world.IsEmpty(pos) {
				t.world.MarkLoading(pos) // Empty -> Loading: this tick owns the single request
				t.worker.Request(pos)    // non-blocking; drops if the bounded queue is full
			}
		}
	}
}

// tickEntities is the per-tick entity step the tracker reads (ENT-01 / TICK-05). It runs on
// the tick goroutine over the tick-owned store, AFTER tickChunks and BEFORE the tracker, so
// the tracker's near() broad-phase sees this tick's entity positions.
//
// THE BUCKET-CONSISTENCY CONTRACT (must_have): any code that changes an entity's position
// MUST route the change through entityStore.move, which re-buckets on a column cross so
// near() never returns a stale bucket. In THIS plan no subsystem moves an entity inside the
// tick — entities do not yet self-propel (the physics that integrates velocity into position
// is Plan 06-03) — so there is no position to re-bucket here and the step is intentionally a
// minimal trace marker. The contract is enforced at the store boundary (move()), so when
// Plan 06-03 adds velocity integration AT THIS SEAM it will call store.move and the bucket
// invariant the tracker depends on is preserved by construction. Ageing/other per-tick
// entity bookkeeping lands alongside that physics in 06-03.
func (t *TickLoop) tickEntities() {
	t.trace("tickEntities")
	// No entity self-propulsion in this plan: positions are unchanged within the tick, so the
	// store's per-section buckets are already consistent for the tracker's near() read that
	// follows. Plan 06-03 fills this with velocity integration via t.entities.move (the
	// bucket-consistent mutation path), keeping near() stale-free.

	// Plan 06-07 interactive gate: the OFF-by-default debug triggers (a visible moving pig +
	// periodic damage) run here, INSIDE this existing phase, so no new phase is added to the
	// fixed tick order (TestTickPhaseOrder is preserved). tickDebug is a nil-check no-op when
	// debug is off (production / every test).
	t.tickDebug()

	// Plan 17-01 gameplay seams, all INSIDE this existing phase (no phase reorder — the fixed
	// tick order is preserved, TestTickPhaseOrder stays green) and BEFORE tracker.Tick so the
	// tracker's near() broad-phase reads this tick's synced player positions:
	//   - syncPlayerEntities: pos-sync each player's store Entity from the tickPlayer (GAMEPLAY-01)
	//   - syncJoinInventories: first-tick ContainerSetContent join-sync (GAMEPLAY-03)
	//   - tickFallDamage: the fall-damage dispatcher; its body lives in fall_damage.go (a 17-01
	//     stub 17-03 overwrites — this call site stays Wave-1-owned, NOT edited by 17-03).
	t.syncPlayerEntities()
	t.syncJoinInventories()
	t.tickFallDamage()
}

// tickAI drives mob AI for every AI mob in the tick-owned store, then runs the throttled
// natural spawner — the AI-03 fill of the Phase-7 stub, in its FIXED pipeline slot
// (tickEntities -> tickAI -> tickPhysics -> applyAsyncResults -> tracker.Tick -> flushOutbound).
// The pipeline ORDER is unchanged (TestTickPhaseOrder); this only fills the body.
//
// Per AI mob (a stable snapshot, mirroring tickPhysics's copy-then-range): call
// e.ai.serverAiStep (07-01 goal arbitration + 07-02 navigation) — which advances the mob's
// goals, (re)computes its A* path, and steps it via moveEntity (the per-axis swept collision +
// the entities.move re-bucket). A moved mob re-buckets on the owner so the tracker's near()
// stays correct; the tracker (after tickPhysics) auto-broadcasts the moved/turned mob via the
// unchanged AddEntity/TeleportEntity/RotateHead encoders — NO new entity packet.
//
// THEN the throttled naturalSpawn (every spawnInterval ticks): it counts live mobs by
// MobCategory from the tick-owned store and, under the CREATURE cap, attempts ONE ON_GROUND
// placement of a Pig near a player (the Pitfall-3 anti-flood gate). Spawning AFTER the per-mob
// AI keeps a freshly spawned mob from being driven on the very tick it appears — it begins
// wandering next tick. tickPhysics runs AFTER this so gravity settles each post-AI-move Y.
//
// SINGLE-OWNER (TICK-05): the serverAiStep walk, the spawn count, and the entityStore.add all
// run on the tick goroutine over tick-owned state — no goroutine, no xsync/ants/conc (the
// off-tick candidate scan is Phase 8 / OPT-03). The snapshot makes spawning a mob mid-range
// safe (it cannot corrupt the iteration we are driving).
func (t *TickLoop) tickAI() {
	t.trace("tickAI")
	if t.entities == nil {
		return // defensive: store is non-nil from NewTickLoop, but never panic if absent
	}

	// Snapshot the AI mobs so the loop is stable even if a spawn (below) or a move re-buckets
	// mid-range — exactly the discipline tickPhysics uses (copy the byID values, then range).
	snapshot := make([]*Entity, 0, len(t.entities.byID))
	for _, e := range t.entities.byID {
		if e.ai != nil {
			snapshot = append(snapshot, e)
		}
	}
	for _, e := range snapshot {
		e.ai.serverAiStep(t, e) // 07-01 goals + 07-02 navigation: the real ported AI walk
	}

	// Throttled natural spawner: vanilla attempts every tick (most no-op under cap); v1 runs the
	// bounded one-placement attempt every spawnInterval ticks to keep the per-tick cost trivial.
	if t.gametime%spawnInterval == 0 {
		t.naturalSpawn()
	}
}

// tickPhysics simulates gravity + per-axis swept-AABB collision for every entity in the
// tick-owned store (ENT-02). It runs on the tick goroutine in its FIXED pipeline slot
// (after tickEntities/tickAI, before applyAsyncResults/tracker.Tick) — do NOT reorder it,
// so the tracker that follows emits this tick's post-physics positions.
//
// Per entity, per tick: apply downward gravity then air drag to the vertical velocity,
// apply horizontal friction, then integrate the velocity into the position via moveEntity —
// which resolves each axis INDEPENDENTLY (clip Δy, Δx, Δz) against solid world blocks so the
// entity LANDS on the floor (onGround) and is BLOCKED by walls without tunneling, and routes
// the position change through entities.move so the per-section bucket stays consistent for
// the tracker's near() (TICK-05). With no world wired (Phase-3-style tests) blockSolidAt
// treats everything as air, so entities simply free-fall and never collide — harmless.
//
// Iterating a snapshot of the store's by-id values is safe: moveEntity re-buckets via
// entities.move, which only mutates the per-column bucket slices, never the byID map we are
// ranging — but we copy to a local slice first so the iteration order is stable and immune
// to any future in-loop add/remove.
func (t *TickLoop) tickPhysics() {
	t.trace("tickPhysics")
	if t.entities == nil {
		return // defensive: store is non-nil from NewTickLoop, but never panic if absent
	}

	// Snapshot the live entities so the loop is stable even if a move re-buckets mid-range.
	snapshot := make([]*Entity, 0, len(t.entities.byID))
	for _, e := range t.entities.byID {
		snapshot = append(snapshot, e)
	}

	for _, e := range snapshot {
		// Gravity: accelerate downward, then air drag so vertical speed converges to a
		// terminal velocity (06-RESEARCH A1 — tunable, wire-irrelevant constants).
		e.vy -= gravityPerTick
		e.vy *= airDrag

		// Horizontal friction: a moving entity slows instead of sliding forever.
		e.vx *= horizontalFriction
		e.vz *= horizontalFriction

		// Integrate via the per-axis swept resolver (the anti-tunneling discipline). This
		// also re-buckets through entities.move and updates onGround / zeroes blocked
		// velocity components.
		t.moveEntity(e, e.vx, e.vy, e.vz)
	}
}

// flushOutbound enqueues this tick's clientbound chunk stream per player (WORLD-05).
// For each player it first sends the chunk-cache framing once per center
// (SetChunkCacheCenter + SetChunkCacheRadius), then collects the player's center-out
// ring columns that are Ready AND not yet sent and, if any, brackets them in
// ChunkBatchStart -> N x ClientboundLevelChunkWithLight (center-out order) ->
// ChunkBatchFinished(N). Each column is added to the player's sent-set so it is sent at
// most once; re-flushing the same center sends nothing new (idempotent — threat T-4-06).
// All sends go through the bounded Client.Send queue (the writeLoop stays the SOLE
// socket writer); the tick never writes the socket directly. Runs on the owner.
func (t *TickLoop) flushOutbound() {
	t.trace("flushOutbound")
	if t.world == nil {
		return // no world wired: cheap no-op (Phase-3-style tests / pre-SetWorld)
	}
	for _, p := range t.players {
		if p.client == nil {
			continue
		}
		if p.sentChunks == nil {
			p.sentChunks = make(map[level.ChunkPos]bool) // lazy-init: keep registration minimal
		}

		// Send the chunk-cache framing once per center so the client knows where its
		// streaming window is centered and how wide it is before the chunks arrive.
		if !p.centerSent {
			p.client.Send(world.SetChunkCacheCenter(p.center[0], p.center[1]))
			p.client.Send(world.SetChunkCacheRadius(int32(p.viewDist)))
			p.centerSent = true
		}

		ring := centerOutRing(p.center, p.viewDist)
		var batch []pk.Packet
		for _, pos := range ring {
			if p.sentChunks[pos] {
				continue // already streamed to this player — send at most once
			}
			ch, ok := t.world.Get(pos)
			if !ok {
				continue // not Ready yet — a later tick will send it once the worker rejoins
			}
			pkt, err := world.WriteLevelChunkWithLight(pos[0], pos[1], ch)
			if err != nil {
				continue // encoding failure for this column: skip, do not poison the batch
			}
			batch = append(batch, pkt)
			p.sentChunks[pos] = true
		}

		if len(batch) > 0 {
			p.client.Send(world.ChunkBatchStart())
			for _, pkt := range batch {
				p.client.Send(pkt)
			}
			p.client.Send(world.ChunkBatchFinished(int32(len(batch))))
		}
	}
}
