package server

// tickOnce runs ONE logical tick: the explicit, fixed-order phase pipeline (TICK-01).
// drainInbound is intentionally NOT here — Run drains inbound once per wake before
// calling tickOnce, kept separate so input is drained per wake, not per catch-up
// step. Each phase is an empty stub today (later phases fill them); the call ORDER
// is the load-bearing contract asserted by TestTickPhaseOrder.
//
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

	t.gametime++                          // EXACTLY once per logical tick — anchors TICK-02
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

// tickChunks advances chunk loading/section state. Phase 4 fills it.
func (t *TickLoop) tickChunks() { t.trace("tickChunks") }

// tickEntities advances entity state. Phase 6 fills it.
func (t *TickLoop) tickEntities() { t.trace("tickEntities") }

// tickAI advances mob AI / pathfinding decisions. Phase 7 fills it.
func (t *TickLoop) tickAI() { t.trace("tickAI") }

// tickPhysics resolves movement/collision. Phase 6 fills it.
func (t *TickLoop) tickPhysics() { t.trace("tickPhysics") }

// flushOutbound enqueues this tick's clientbound packets via Client.Send. A no-op
// until players join (the writeLoop remains the sole socket writer — the tick never
// writes the socket directly).
func (t *TickLoop) flushOutbound() {
	t.trace("flushOutbound")
	// No players yet; nothing to flush. Wave 2/3 enqueues per-player packets here.
}
