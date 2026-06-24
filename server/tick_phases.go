package server

import (
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

// applyAsyncResults is the async rejoin seam (TICK-05). It is a genuine no-op today
// because asyncIn is nil; Phase 8 wires worker result channels here and applies each
// immutable result on the owner goroutine — without reordering the pipeline.
func (t *TickLoop) applyAsyncResults() {
	t.trace("applyAsyncResults")
	if t.asyncIn == nil {
		return // Phase 3: the seam just EXISTS; nothing to apply
	}
	for {
		select {
		case r := <-t.asyncIn:
			r.applyTo(t) // applied by the owner; the worker only computed an immutable result
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

// tickWorld advances world/block-tick state. Phase 4 fills it.
func (t *TickLoop) tickWorld() { t.trace("tickWorld") }

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
}

// tickAI advances mob AI / pathfinding decisions. Phase 7 fills it.
func (t *TickLoop) tickAI() { t.trace("tickAI") }

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
