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
	// Test-only seam (nil in production): inject a panic (TestRegionPanicIsolated) or observe the
	// barrier (TestCoordinatorBarrier) on the region's goroutine, before the per-region phases.
	if r.tickHook != nil {
		r.tickHook()
	}
	t := r.coord
	// The PER-REGION phases, in the EXACT order today's tickOnce ran them (the gameplay order is
	// the load-bearing contract — TestTickPhaseOrder). At N=1 these range t.only() == r.
	t.tickWorld()         // PER-REGION: scheduled blocks/fluids + chunk-save over the region's world
	t.tickChunks()        // PER-REGION: per-player ring → region.world requests
	t.tickEntities()      // PER-REGION: the per-entity seams over region.entities
	t.tickAI()            // PER-REGION: serverAiStep over region.entities + naturalSpawn
	t.tickPhysics()       // PER-REGION: moveEntity over region.entities
	t.applyAsyncResults() // PER-REGION chunkReady drain + the global asyncIn2 drain (identical N=1)
	_ = gt                // gt is read by the region's scheduled-tick phases via t.gametime; the
	// parameter documents the read-only shared anchor the coordinator passes in (Plan 03 threads
	// it into the per-region phases explicitly when they range THIS region's store).
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

	// Hoisted backstop: a panic in ANY phase — or one surfaced by wg.Wait() from a region —
	// must not kill the tick goroutine (that would freeze the world + disconnect every player).
	// Deferred first => runs last => outermost. recoverTick handles the panic path's gametime++
	// and recordMSPT; the normal path below does its own (the `advanced` guard makes it exactly
	// once). Pass start so the panic path can still record this tick's MSPT.
	advanced := false
	defer t.recoverTick(start, &advanced)

	t.resolveSubtickInputs() // GLOBAL pre-phase (per-player input; touches no region store at N=1).
	// At N>1 this routes a region-affine player's input to the OWNING region; here it stays on the
	// coordinator before the fan-out because it touches no region store.

	gt := t.gametime // the ONE shared tick number every region reads this tick (Pitfall 3 /
	// T-27-02-GT): a single value the coordinator passes into each region.tick, read-only.

	// --- FAN OUT: each region ticks its own store in parallel (TICK-05 per region). At N=1 there
	// is one region, so this runs the per-region pipeline once, serially. conc.WaitGroup.Go spawns
	// the region tick under a per-region recover; wg.Wait re-raises the FIRST region panic's value
	// + stack on this goroutine (structured propagation), where the recoverTick backstop catches
	// it — one region's panic does NOT hang the tick (T-27-02). ---
	var wg conc.WaitGroup
	for _, r := range t.regions {
		r := r
		wg.Go(func() { r.tick(gt) })
	}
	wg.Wait() // BARRIER: no region mutates its store past this point this tick.

	// --- CROSS-REGION / GLOBAL POST-PHASE (coordinator, all regions quiescent → safe to read
	// across regions). These stay where they were sequenced before, just AFTER the barrier: the
	// tracker spans region seams (Plan 03), and the movement/equipment/flush read the global
	// player list. At N=1 the single region is already quiescent, so the order/effect is identical
	// to the pre-regionization inline pipeline (the gameplay order is the load-bearing contract —
	// TestTickPhaseOrder). ---
	t.tickEntityMovement() // GAMEPLAY-07: ServerEntity.sendChanges → delta move packets to trackers
	t.tickEquipment()      // GAMEPLAY-07: detectEquipmentUpdates → SetEquipment to trackers
	t.trace("tracker.Tick")
	t.tracker.Tick()  // cross-region at the barrier (near() spans region seams — Plan 03)
	t.flushOutbound() // GLOBAL: per-player chunk stream over the global player list

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

	t.recordMSPT(t.clock.Now().Sub(start)) // publish the read-only telemetry snapshot (TICK-06)
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
