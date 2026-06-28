package server

import (
	"log"
	"runtime/debug"

	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/world"
)

// chunkLoadGraceTicks is how long a column may sit in stateLoading (request issued, no result yet)
// before tickChunks reverts it to Empty and re-requests it. At 20 TPS this is ~2s — far longer than
// a healthy generate/region-load round-trip, so it only fires for a genuinely dropped/stranded
// request, not for a chunk that is merely still generating.
const chunkLoadGraceTicks = 200

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
	t.tickEntityMovement()   // GAMEPLAY-07: ServerEntity.sendChanges → delta move packets to trackers
	t.tickEquipment()        // GAMEPLAY-07: detectEquipmentUpdates → SetEquipment to trackers
	t.trace("tracker.Tick")  // record the tracking phase at its call site
	t.tracker.Tick()         // synchronous stub today; Phase 8 swaps the executor
	t.flushOutbound()        // enqueue clientbound via Client.Send (no-op until players join)

	t.gametime++ // EXACTLY once per logical tick — anchors TICK-02

	// PLUGIN-02 (Plan 22) on_tick seam: the ONE per-tick emit, fired ONCE per tick TOTAL (not once
	// per entity) at the END of tickOnce after gametime++. Emit's zero-subscriber guard makes this
	// free on a server with no on_tick hook (a single map read, zero alloc) — so an unsubscribed
	// server pays nothing. Nil-guarded; payload = the current gametime as a frozen scalar. This is
	// the SOLE per-tick emit — every other seam fires on a discrete occurrence, never per tick.
	if t.plugins != nil {
		t.plugins.Emit(host.EventTick, host.TickEvent{Tick: int(t.gametime)})
	}

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
	if t.only().asyncIn != nil {
		for {
			select {
			case r := <-t.only().asyncIn:
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
	// SUB-BLOCKTICK: drain the general scheduled-BLOCK-tick queue (ServerLevel.blockTicks.tick)
	// BEFORE the fluid pass, matching ServerLevel.tick which drains blockTicks then fluidTicks at
	// the same game-time. It lives INSIDE this existing phase so no new phase is added to the
	// fixed tick order (TestTickPhaseOrder stays green). A nil manager (no chunk container ever
	// registered) is a cheap no-op.
	t.tickScheduledBlocks()
	t.tickFluids()
	// SUB-PERSIST: the periodic chunk-save pass (every chunkSaveIntervalTicks). It lives INSIDE
	// this existing phase so no new phase is added to the fixed tick order (TestTickPhaseOrder
	// stays green). A nil/disabled chunkSaver makes it a cheap no-op (tests/ephemeral runs).
	t.tickChunkSave()
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
	if t.only().world == nil || t.only().worker == nil {
		return // no world wired (Phase-3-style tests / pre-SetWorld): cheap no-op
	}
	// Advance the manager clock, then revert any column stuck in Loading past the grace window
	// back to Empty so the request below re-issues it. This recovers a worker request that was
	// DROPPED under burst backpressure (worker.Request is non-blocking and silently drops when its
	// bounded queue is full) or a center stranded by the scheduler's emit gate — without it a
	// dropped request leaves the column transparent forever (the "invisible chunk" bug).
	t.only().world.Tick()
	t.only().world.RetryStale(chunkLoadGraceTicks)
	for _, p := range t.players {
		ring := centerOutRing(p.center, p.viewDist)
		for _, pos := range ring {
			if t.only().world.IsEmpty(pos) {
				t.only().world.MarkLoading(pos) // Empty -> Loading: this tick owns the single request
				t.only().worker.Request(pos)    // non-blocking; drops if the bounded queue is full
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
	// follows. Plan 06-03 fills this with velocity integration via t.only().entities.move (the
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

	// Suffocation: the IN_WALL branch of LivingEntity.baseTick (`if isInWall() hurtServer(inWall(),
	// 1.0F)`). In vanilla baseTick this check runs BEFORE the air/drowning branch, so it is placed
	// here ahead of tickBreath. Its body lives in suffocation.go; a single ADDITIVE call inside this
	// existing phase keeps the tick order unchanged (TestTickPhaseOrder updated to include it),
	// mirroring the tickFallDamage seam above.
	t.tickSuffocation()

	// Plan 17-13 breath/drowning: the LivingEntity.baseTick air branch (air drains while the eyes
	// are submerged, refills otherwise, 2.0 DROWN damage at the air<=-20 threshold). Its body lives
	// in breath.go; a single ADDITIVE call inside this existing phase keeps the tick order unchanged
	// (TestTickPhaseOrder stays green), mirroring the tickFallDamage seam above.
	t.tickBreath()

	// Plan 17-19 food/hunger: the FoodData.tick port (exhaustion drains saturation then food, health
	// regenerates from saturation while fed, starvation damage at food 0) plus the movement-exhaustion
	// ladder (ServerPlayer.checkMovementStatistics). Its body lives in food.go; a single ADDITIVE call
	// inside this existing phase keeps the tick order unchanged (TestTickPhaseOrder stays green),
	// mirroring the tickBreath seam directly above. Placed AFTER tickBreath so a drowning hit this
	// tick is reflected before the hunger step reads health for its regen gate.
	t.tickFood()

	// Plan 17-11 melee-combat per-tick bookkeeping: the attack-strength ticker increment
	// (Player.tick) and the invulnerableTime / hurtTime decrements (ServerPlayer.tick), inside this
	// existing phase so no new phase is added to the fixed tick order (TestTickPhaseOrder stays
	// green). Its body lives in combat.go; a single ADDITIVE call keeps tick.go/tick_phases.go edits
	// minimal so the sibling Wave edits (subtick.go fluid, block_drop.go drops) do not conflict.
	t.tickPlayerCombat()

	// Plan 17-14 ITEM-PICKUP: the dropped-item lifecycle — ItemEntity.tick (0.04 gravity, age,
	// 6000-tick despawn) for every ground item, then the Player.aiStep item-collection scan that
	// picks up nearby pickable items (ItemEntity.playerTouch + Inventory.add + the take-item
	// animation). A single ADDITIVE call inside this existing phase keeps the tick order unchanged
	// (TestTickPhaseOrder stays green), mirroring the tickFallDamage / tickBreath seams above. Its
	// body lives in item_entity.go. Runs BEFORE tracker.Tick so a pickup/despawn removal is
	// reflected in this tick's near() and the tracker emits RemoveEntities promptly.
	t.tickItems()

	// Plan 17-21 block-break dig-time: the ServerPlayerGameMode.tick() port — advance any pending
	// delayed-destroy (finish the break at progress>=1.0) and refresh the in-progress crack overlay
	// for each digging player. A single ADDITIVE call inside this existing phase keeps the tick order
	// unchanged (TestTickPhaseOrder stays green), mirroring the tickFallDamage / tickBreath / tickFood
	// seams above. Its body lives in block_break.go. Placed AFTER tickItems so a block broken by the
	// delayed-destroy this tick spawns its drop and the tracker reflects it in this tick's near().
	t.tickBlockBreak()

	// Plan 17-22 item-use / EATING: the LivingEntity.updatingUsingItem port — for each player using
	// an item (eating), decrement the use-duration and, on completion, refill the food bar
	// (FoodData.eat(FoodProperties)) + shrink the held stack (ItemStack.consume). A single ADDITIVE
	// call inside this existing phase keeps the tick order unchanged (TestTickPhaseOrder stays green),
	// mirroring the tickFood / tickBlockBreak seams above. Its body lives in item_use.go. Placed AFTER
	// tickFood so the eat's FoodData.eat lands on the post-hunger-tick food value, and AFTER
	// tickBlockBreak so it sits with the other ServerPlayerGameMode/LivingEntity per-tick seams.
	t.tickUseItem()

	// ULTRA_DEBUG firehose: a throttled per-player state snapshot (pos/vel/in-water/air/food/health/
	// dig/use), emitted LAST in the per-player phase so it captures the post-tick state. No-op unless
	// SULFUR_ULTRA_DEBUG=1. Placed here (not a new phase) so it never perturbs the fixed tick order.
	t.tickUltraDebug()
}

// tickUltraDebug emits the SULFUR_ULTRA_DEBUG per-player state snapshot, throttled to one line per
// udebugTickEvery ticks per player so a long session log stays readable. A no-op unless the env
// toggle is on (the udebug* calls short-circuit on the cached bool). Tick-owned: reads tick-owned
// player state on the tick goroutine, same as the sibling per-player seams.
func (t *TickLoop) tickUltraDebug() {
	if !udebugEnabled {
		return
	}
	for _, p := range t.players {
		if p == nil {
			continue
		}
		// In water, snapshot EVERY tick (the throttle hides the per-tick sink dynamics that a
		// "won't float" report hinges on); otherwise throttle to keep the log readable.
		inWater := t.playerInWater(p) || t.eyeInWater(p)
		if !inWater && t.gametime%udebugTickEvery != 0 {
			continue
		}
		t.udebugTickSnapshot(p)
	}
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
	if t.only().entities == nil {
		return // defensive: store is non-nil from NewTickLoop, but never panic if absent
	}

	// Snapshot the AI mobs so the loop is stable even if a spawn (below) or a move re-buckets
	// mid-range — exactly the discipline tickPhysics uses (copy the byID values, then range).
	snapshot := make([]*Entity, 0, len(t.only().entities.byID))
	for _, e := range t.only().entities.byID {
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
	if t.only().entities == nil {
		return // defensive: store is non-nil from NewTickLoop, but never panic if absent
	}

	// Snapshot the live entities so the loop is stable even if a move re-buckets mid-range.
	snapshot := make([]*Entity, 0, len(t.only().entities.byID))
	for _, e := range t.only().entities.byID {
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
	if t.only().world == nil {
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

		t.sendNextChunks(p)
	}
}

// sendNextChunks is the 1:1 port of net.minecraft.server.network.PlayerChunkSender.sendNextChunks:
// the client-acknowledged flow control that paces chunk batches so the server never floods the
// connection. Without it, sending the whole view ring every tick overran the bounded outbound queue
// and the client was kicked for backpressure (the "invisible chunk" + disconnect bug).
//
// Vanilla:
//
//	if (unacknowledgedBatches >= maxUnacknowledgedBatches) return;        // throttle: wait for an ack
//	float f = Math.max(1.0f, desiredChunksPerTick);
//	batchQuota = Math.min(batchQuota + desiredChunksPerTick, f);
//	if (batchQuota < 1.0f) return;                                        // not enough budget yet
//	if (pendingChunks.isEmpty()) return;
//	List<LevelChunk> list = collectChunksToSend(...);                     // up to floor(batchQuota), nearest-first
//	if (list.isEmpty()) return;
//	send(ChunkBatchStart); unacknowledgedBatches++;
//	for (ch : list) sendChunk(ch);
//	send(ChunkBatchFinished(list.size()));
//	batchQuota -= list.size();
//
// CITE: PlayerChunkSender.sendNextChunks / collectChunksToSend / constructor (START_CHUNKS_PER_TICK
// 9.0, maxUnacknowledgedBatches 1). "pendingChunks" here is the set of ring columns that are Ready
// but not yet sent (computed from the live ring + sentChunks). Tick-owned.
func (t *TickLoop) sendNextChunks(p *tickPlayer) {
	if !p.chunkSenderInit {
		// Constructor defaults: desiredChunksPerTick = START_CHUNKS_PER_TICK (9.0),
		// maxUnacknowledgedBatches = 1 (raised to 10 on the first client ack).
		p.desiredChunksPerTick = 9.0
		p.maxUnacknowledgedBatches = 1
		p.chunkSenderInit = true
	}

	// Throttle: hold until the client acknowledges outstanding batches.
	if p.unacknowledgedBatches >= p.maxUnacknowledgedBatches {
		return
	}

	f := p.desiredChunksPerTick
	if f < 1.0 {
		f = 1.0
	}
	p.batchQuota = minF32(p.batchQuota+p.desiredChunksPerTick, f)
	if p.batchQuota < 1.0 {
		return // not enough budget accumulated for even one chunk this tick
	}

	// collectChunksToSend: up to floor(batchQuota) Ready+unsent ring columns, NEAREST-FIRST
	// (vanilla sorts pending by ChunkPos.distanceSquared to the player chunk). centerOutRing is
	// already center-out (non-decreasing Chebyshev), which yields the same nearest-first order.
	limit := int(p.batchQuota) // Mth.floor on a positive float
	ring := centerOutRing(p.center, p.viewDist)
	batch := make([]pk.Packet, 0, limit)
	var sent []level.ChunkPos
	for _, pos := range ring {
		if len(batch) >= limit {
			break
		}
		if p.sentChunks[pos] {
			continue
		}
		ch, ok := t.only().world.Get(pos)
		if !ok {
			continue // not Ready yet (still generating) — a later tick sends it
		}
		pkt, err := world.WriteLevelChunkWithLight(pos[0], pos[1], ch)
		if err != nil {
			continue
		}
		batch = append(batch, pkt)
		sent = append(sent, pos)
	}
	if len(batch) == 0 {
		return // nothing Ready to send this tick
	}

	// Send exactly ONE batch and account it as unacknowledged until the client's
	// ServerboundChunkBatchReceived ack (onChunkBatchReceivedByClient) clears it.
	p.client.Send(world.ChunkBatchStart())
	p.unacknowledgedBatches++
	for i, pkt := range batch {
		p.client.Send(pkt)
		p.sentChunks[sent[i]] = true
	}
	p.client.Send(world.ChunkBatchFinished(int32(len(batch))))
	p.batchQuota -= float32(len(batch))
}

// minF32 is Math.min for float32 (no generic builtin min on float in older style; explicit for the
// faithful PlayerChunkSender port).
func minF32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// onChunkBatchReceivedByClient is the 1:1 port of
// net.minecraft.server.network.PlayerChunkSender.onChunkBatchReceivedByClient(float). The client
// sends ServerboundChunkBatchReceived after it has processed a batch, carrying the rate it can
// sustain. Vanilla:
//
//	this.unacknowledgedBatches--;
//	if (this.unacknowledgedBatches < 0) { this.unacknowledgedBatches = 0; LOGGER.warn(...); }
//	this.desiredChunksPerTick = Double.isNaN(rate) ? 0.01f : Mth.clamp(rate, 0.01f, 64.0f);
//	if (this.unacknowledgedBatches == 0) this.batchQuota = 1.0f;
//	this.maxUnacknowledgedBatches = 10;
//
// CITE: PlayerChunkSender.onChunkBatchReceivedByClient (MIN_CHUNKS_PER_TICK 0.01,
// MAX_CHUNKS_PER_TICK 64.0, MAX_UNACKNOWLEDGED_BATCHES 10). Tick-owned.
func (p *tickPlayer) onChunkBatchReceivedByClient(rate float32) {
	p.unacknowledgedBatches--
	if p.unacknowledgedBatches < 0 {
		p.unacknowledgedBatches = 0
	}
	if rate != rate { // NaN
		p.desiredChunksPerTick = 0.01
	} else {
		p.desiredChunksPerTick = clampF32(rate, 0.01, 64.0)
	}
	if p.unacknowledgedBatches == 0 {
		p.batchQuota = 1.0
	}
	p.maxUnacknowledgedBatches = 10
}

// clampF32 is Mth.clamp for float32.
func clampF32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
