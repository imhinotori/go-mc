package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// chunkLoadGraceTicks is how long a column may sit in stateLoading (request issued, no result yet)
// before tickChunks reverts it to Empty and re-requests it. At 20 TPS this is ~2s — far longer than
// a healthy generate/region-load round-trip, so it only fires for a genuinely dropped/stranded
// request, not for a chunk that is merely still generating.
const chunkLoadGraceTicks = 200

// tickOnce — the ONE logical tick — is the Folia coordinator (Phase-27 STEP-2): it fans out the
// region tick(s) via conc, BARRIERS, runs the cross-region/global post-phase, and advances the
// shared gametime EXACTLY ONCE. It now lives in region_coordinator.go alongside region.tick (the
// per-region pipeline it fans out). The phase methods below (resolveSubtickInputs, tickWorld,
// tickChunks, tickEntities, tickAI, tickPhysics, applyAsyncResults, the post-phase) are unchanged;
// the coordinator sequences them in the byte-identical observable order (TestTickPhaseOrder).
// drainInbound is intentionally NOT in the tick — Run drains inbound once per wake before stepping.

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
	if t.cur().asyncIn != nil {
		for {
			select {
			case r := <-t.cur().asyncIn:
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
	// Phase-27 N=2: the scheduled-block + fluid drains are PER-REGION (each region owns its own
	// blockTicks manager + fluidSchedule queue), but they run on the COORDINATOR where cur() would
	// fall back to region 0 and silently drain ONLY region 0's queues — leaving fluids/scheduled
	// blocks dead in regions 1..N-1. forEachRegion runs each drain once per region with that region
	// registered, so cur().blockTicks / cur().fluidSchedule resolve to the OWNING region's queue;
	// every drain still operates against the SHARED world (world()). At N=1 this is exactly one
	// iteration over region 0 — identical to the pre-fix single drain (TestTickPhaseOrder unchanged:
	// the trace marker is "tickWorld" above and stays put; only the bodies are wrapped).
	t.forEachRegion(func(r *region) {
		// SUB-BLOCKTICK: drain THIS region's scheduled-BLOCK-tick queue (ServerLevel.blockTicks.tick)
		// BEFORE the fluid pass, matching ServerLevel.tick which drains blockTicks then fluidTicks at
		// the same game-time. A nil manager (no chunk container ever registered) is a cheap no-op.
		t.tickScheduledBlocks()
		t.tickFluids()
		// REDSTONE TIER-3 (PISTON): tick THIS region's live moving_piston block-entities
		// (PistonMovingBlockEntity.tick — progress 0->1 over 2 ticks, then finalTick completes the move),
		// then drain the piston block-event queue (ServerLevel.runBlockEvents -> triggerEvent). The BE
		// tick runs first (a BE created LAST tick advances before this tick's fresh events fire, matching
		// vanilla's tickBlockEntities-then-runBlockEvents ordering). Both are cheap no-ops when empty.
		t.tickMovingPistons()
		t.drainPistonBlockEvents()
	})
	// SUB-RANDOMTICK: the UNSCHEDULED random-tick driver (ServerLevel.tickChunk block-sampling pass —
	// sugar-cane growth + future crops/saplings/grass/leaves). It is a WORLD-GLOBAL pass over the
	// SHARED ChunkManager (the world is not yet per-region-sharded), so it runs ONCE here on the
	// coordinator (the tickChunkSave twin), NOT inside the per-region forEachRegion wrap. It reads the
	// RANDOM_TICK_SPEED gamerule (constant 3 in v1) and samples every loaded, randomly-ticking section.
	// The block-position draw uses the region's SEPARATE randValue int LCG (Level.getBlockRandomPos),
	// never levelRandom, so the pig oracle's pinned stream is unperturbed. A nil world is a cheap no-op.
	// Its body lives in random_tick.go. Placed AFTER the scheduled block/fluid drains, mirroring
	// vanilla's ServerLevel.tick ordering (tickChunk runs in ServerChunkCache.tickChunks, after the
	// pending block/fluid ticks). CITE: ServerChunkCache.tickChunks -> ServerLevel.tickChunk.
	t.tickRandomBlocks()
	// SUB-THUNDER: the WORLD-GLOBAL lightning-strike gate (ServerLevel.tickThunder — the per-chunk
	// isRaining && isThundering && nextInt(100000)==0 probability that spawns a LightningBolt). In vanilla
	// tickThunder runs in ServerChunkCache.tickSpawningChunk, a SIBLING of tickChunk (the random-tick pass)
	// in the same chunk-cache tick — so it belongs right after tickRandomBlocks here, as another
	// world-global per-loaded-chunk pass over the SHARED ChunkManager on the coordinator. It draws the
	// GLOBAL levelRandom (like the weather cycle), never a per-entity stream, so the pig oracle (which never
	// runs this) is unperturbed. A clear/non-thundering world is a cheap early-out (no per-column draw). Its
	// body lives in lightning.go. CITE: ServerChunkCache.tickSpawningChunk -> ServerLevel.tickThunder.
	t.tickThunder()
	// SUB-BLOCKENTITY: tick every furnace/blast_furnace/smoker block-entity (GAMEPLAY-05
	// AbstractFurnaceBlockEntity.serverTick). Furnaces are keyed by world position (t.furnaces, global —
	// not per-region), so they tick ONCE globally here (the tickChunkSave twin), after the per-region
	// block/fluid drains. A furnace with no items ticks to a cheap no-op. Nil map = no-op (no furnace open).
	t.tickFurnaces()
	// SUB-BLOCKENTITY: tick every brewing-stand block-entity (BrewingStandBlockEntity.serverTick). Keyed by
	// world position (t.brewingStands, global — not per-region), so they tick ONCE globally here (the
	// tickFurnaces twin). A brewing stand with no items/fuel ticks to a cheap no-op. Nil map = no-op.
	t.tickBrewingStands()
	// SUB-BLOCKENTITY: tick every HOPPER block-entity (HopperBlockEntity.pushItemsTick — the 8-tick
	// single-item transfer drive: pull from the container/loose-item above, push into the container in
	// FACING, gated by the redstone ENABLED property). Keyed by world position (t.hoppers, global — not
	// per-region), so they tick ONCE globally here (the tickFurnaces twin). A hopper with no items + no
	// source above ticks to a cheap no-op. Nil map = no-op (no hopper placed). Runs AFTER the item pass so
	// a hopper sucks an item that already settled this tick. CITE: HopperBlock.getTicker -> pushItemsTick.
	t.tickHoppers()
	// SUB-BLOCKENTITY: tick every BEACON block-entity (BeaconBlockEntity.tick — the incremental beam-column
	// scan + the every-80-tick pyramid-level recompute + the in-range player effect application). Keyed by
	// world position (t.beacons, global — not per-region), so they tick ONCE globally here (the tickFurnaces
	// twin). A beacon with no primary effect / obstructed beam ticks to a cheap no-op (no effect applied). Nil
	// map = no-op (no beacon placed). CITE: BeaconBlock.getTicker -> BeaconBlockEntity.tick.
	t.tickBeacons()
	// SUB-BLOCKENTITY: tick every CONDUIT block-entity (ConduitBlockEntity.serverTick — the every-40-tick
	// activation-frame re-scan + the in-range player CONDUIT_POWER application + the full-frame hostile
	// attack). Keyed by world position (t.conduits, global — not per-region), so they tick ONCE globally here
	// (the tickBeacons twin). A conduit whose frame is broken / not submerged ticks to a cheap no-op (no
	// effect applied). Nil map = no-op (no conduit placed). CITE: ConduitBlock.getTicker ->
	// ConduitBlockEntity.serverTick.
	t.tickConduits()
	// SUB-PERSIST: the periodic chunk-save pass (every chunkSaveIntervalTicks) stays GLOBAL — it
	// serializes the SHARED world's dirty chunks once, not per region. It lives INSIDE this existing
	// phase so no new phase is added to the fixed tick order (TestTickPhaseOrder stays green). A
	// nil/disabled chunkSaver makes it a cheap no-op (tests/ephemeral runs).
	t.tickChunkSave()
	// SUB-PERSIST (raids/POI): the periodic raid + POI SavedData flush, on the SAME cadence as the
	// chunk-save pass. It flushes only DIRTY per-region managers to world/data/raids.dat + world/poi/.
	// A "" persistDir makes it a cheap no-op (tests/ephemeral runs). See saveddata.go.
	t.tickSavedData()
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
	if t.world() == nil || t.worker() == nil {
		return // no world wired (Phase-3-style tests / pre-SetWorld): cheap no-op
	}
	// Advance the manager clock, then revert any column stuck in Loading past the grace window
	// back to Empty so the request below re-issues it. This recovers a worker request that was
	// DROPPED under burst backpressure (worker.Request is non-blocking and silently drops when its
	// bounded queue is full) or a center stranded by the scheduler's emit gate — without it a
	// dropped request leaves the column transparent forever (the "invisible chunk" bug).
	t.world().Tick()
	t.world().RetryStale(chunkLoadGraceTicks)
	if t.netherWorld != nil {
		t.netherWorld.Tick()
		t.netherWorld.RetryStale(chunkLoadGraceTicks)
	}
	for _, p := range t.players {
		// NETHER: a player in the nether streams from the nether world/worker; an overworld player from
		// the overworld's. dimWorld(p) picks the manager; the request goes to the matching worker so the
		// nether column is generated by the nether generator, not the overworld one.
		mgr := t.dimWorld(p)
		wk := t.worker()
		if p.dimension == dimNether && t.netherWorker != nil {
			wk = t.netherWorker
		}
		if mgr == nil || wk == nil {
			continue
		}
		ring := centerOutRing(p.center, p.viewDist)
		for _, pos := range ring {
			if mgr.IsEmpty(pos) {
				mgr.MarkLoading(pos) // Empty -> Loading: this tick owns the single request
				wk.Request(pos)      // non-blocking; drops if the bounded queue is full
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
	// follows. Plan 06-03 fills this with velocity integration via t.cur().entities.move (the
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

	// SLEEP-01: the per-player sleep advance (Player.tick sleep branch — sleepCounter climb/unwind + the
	// wake-at-dawn stopSleepInBed). ADDITIVE, sibling of tickBreath (its body lives in player_sleep.go),
	// inside this existing phase so no new phase is added to the fixed tick order. Placed AFTER tickBreath
	// so a drowning hit this tick is reflected before the sleep step reads the player. Untraced, like
	// tickBreath/tickFood.
	t.tickPlayerSleep()

	// MOB-EFFECT-01 (Task #9): the per-player mob-effect tick (LivingEntity.tickEffects — poison damage,
	// duration countdown, modifier expiry). ADDITIVE, sibling of tickBreath. Placed AFTER tickBreath so a
	// poison tick this frame lands on the post-drown health, mirroring the food step's ordering rationale.
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		// Run each player's effect tick in the region that OWNS the player's column, so the BAD_OMEN /
		// RAID_OMEN village check + createOrExtendRaid read that region's poiManager/raidsManager (t.cur()).
		t.withRegion(t.regionForColumn(columnOf(p.x, p.z)), func() {
			t.tickPlayerEffects(p)
		})
	}

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

	// WR-06 XP-ORB PICKUP: the experience-orb lifecycle — ExperienceOrb.tick (0.03 gravity, the
	// followNearbyPlayer homing, 6000-tick despawn) for every orb, then the Player.touch scan that
	// collects nearby orbs (ExperienceOrb.playerTouch + giveExperiencePoints + the orb-suck animation).
	// A single ADDITIVE call inside this existing phase keeps the tick order unchanged (TestTickPhaseOrder
	// stays green), mirroring the tickItems seam directly above. Its body lives in xp_orb.go. Placed right
	// AFTER tickItems (the sibling pickup pass) and BEFORE tracker.Tick so a collected/despawned orb's
	// removal is reflected in this tick's near() and the tracker emits RemoveEntities promptly.
	t.tickOrbs()

	// PROJECTILE-01 (Task #8): the AbstractArrow flight lifecycle — AbstractArrow.tick (block latch,
	// drag 0.99 + gravity 0.05, swept entity hit → onHitEntity, 1200-tick despawn) for every in-flight
	// arrow. A single ADDITIVE call inside this existing phase keeps the tick order unchanged
	// (TestTickPhaseOrder stays green), mirroring the tickOrbs seam directly above. Its body lives in
	// projectile.go. Placed AFTER tickOrbs and BEFORE tracker.Tick so a hit/despawn removal is reflected
	// in this tick's near() and the tracker emits RemoveEntities promptly.
	t.tickArrows()

	// PRIMED TNT: the PrimedTnt.tick lifecycle — apply gravity (0.04) + drag (0.98) + the on-ground
	// bounce, then count the fuse (80) down and, at 0, discard the entity and run the ServerExplosion
	// (radius 4.0, TNT interaction). A single ADDITIVE call inside this existing phase keeps the tick
	// order unchanged (TestTickPhaseOrder stays green), mirroring the tickArrows seam directly above. Its
	// body lives in primed_tnt.go. Placed AFTER tickArrows and BEFORE tracker.Tick so a detonation removal
	// is reflected in this tick's near() and the tracker emits RemoveEntities promptly. tnt-gated (zero
	// cost when no primed TNT exists, so the pig oracle stream is unperturbed). CITE PrimedTnt.tick.
	t.tickPrimedTnt()

	// TNT MINECART: the MinecartTNT.tick fuse countdown → velocity-scaled explode, driven inside
	// tickMinecarts (minecart.go) for a primed TNT minecart. No separate phase call — the minecart tick
	// already visits it. (Comment kept here for the tick-order narrative.)

	// MOB-EFFECT-01 (Task #9): the thrown-splash-potion arc + splash (AbstractThrownPotion.tick →
	// onHitAsPotion). Sibling of tickArrows; a potion that hits a block/player applies its effects to
	// nearby players. ADDITIVE + potion-gated (zero cost when no potion is in flight).
	t.tickPotions()

	// FISHING HOOK (bobber): the FishingHook.tick state machine (FLYING -> BOBBING float, the
	// catchingFish wait/lure/hook countdowns ending in a bite). Sibling of tickArrows/tickPotions;
	// ADDITIVE + fishing-hook-gated (zero cost when no bobber is out, so the pig oracle stream is
	// unperturbed — a bobber only exists after a rod cast, and its RNG is a dedicated per-bobber
	// stream). Placed AFTER tickPotions and BEFORE tracker.Tick so a discard/land is reflected in this
	// tick's near(). Body in fishing.go. CITE FishingHook.tick.
	t.tickFishingHooks()

	// MINECART + RAILS: the AbstractMinecart rail-follow physics (OldMinecartBehavior.tick — the DEFAULT
	// vanilla movement; the experimental NewMinecartBehavior is off by default and cited-deferred). A
	// minecart on a rail follows the track (moveAlongTrack: the ascending slide, the EXITS velocity
	// projection, the position snap, the powered-rail boost/brake, applyNaturalSlowdown 0.997/0.96); off a
	// rail it falls (comeOffTrack). Sibling of tickArrows/tickPotions; ADDITIVE + minecart-gated (zero cost
	// when no minecart exists, so the pig oracle stream is unperturbed). Placed AFTER tickPotions and BEFORE
	// tracker.Tick so a moved/removed minecart is reflected in this tick's near(). Body in minecart.go.
	t.tickMinecarts()

	// BOAT: the AbstractBoat surface-float physics (floatBoat — the water buoyancy + the per-status friction +
	// the air->water surface snap). An EMPTY boat is server-authoritative: the server runs floatBoat + move(SELF)
	// so it settles onto the water surface and drifts; a RIDDEN boat is client-authoritative (the rider's client
	// drives it via ServerboundMoveVehicle). Sibling of tickMinecarts; ADDITIVE + boat-gated (zero cost when no
	// boat exists, so the pig oracle stream is unperturbed). Placed AFTER tickMinecarts and BEFORE tracker.Tick so
	// a moved boat is reflected in this tick's near(). Body in boat.go.
	t.tickBoats()

	// VEX + FANGS (Task): the EvokerFangs warmup -> attack -> despawn lifecycle (EvokerFangs.tick). Sibling
	// of tickArrows/tickPotions; a code-spawned projectile the evoker's FANGS spell places. ADDITIVE +
	// fangs-gated (zero cost when no fangs are active). Placed AFTER tickPotions and BEFORE tracker.Tick so a
	// despawned fangs' removal is reflected in this tick's near() and the tracker emits RemoveEntities.
	t.tickFangs()

	// LIGHTNING BOLT: the LightningBolt.tick life/flashes lifecycle (net.minecraft.world.entity
	// .LightningBolt) for every active bolt the thunder strike (or a future channeling trident) spawned —
	// at life==2 it starts ground fire; while life>=0 and not visual-only it deals 5.0 lightning damage +
	// fire to every LivingEntity in a ±3 box; then it re-flashes flashes-1 times and discards. Sibling of
	// tickFangs (a code-spawned tick-owned entity); ADDITIVE + bolt-gated (zero cost when no bolt is
	// active). Placed AFTER tickFangs and BEFORE tracker.Tick so a discarded bolt's removal is reflected in
	// this tick's near() and the tracker emits RemoveEntities promptly. Its body lives in lightning.go.
	// CITE: LightningBolt.tick.
	t.tickLightning()

	// Plan 17-21 block-break dig-time: the ServerPlayerGameMode.tick() port — advance any pending
	// delayed-destroy (finish the break at progress>=1.0) and refresh the in-progress crack overlay
	// for each digging player. A single ADDITIVE call inside this existing phase keeps the tick order
	// unchanged (TestTickPhaseOrder stays green), mirroring the tickFallDamage / tickBreath / tickFood
	// seams above. Its body lives in block_break.go. Placed AFTER tickItems so a block broken by the
	// delayed-destroy this tick spawns its drop and the tracker reflects it in this tick's near().
	t.tickBlockBreak()

	// NETHER PORTAL travel: the Entity.handlePortal port — for each player, process the portal cooldown
	// and (if standing in a nether_portal) accrue the dwell timer, teleporting to the other dimension at
	// the transition threshold. A single ADDITIVE call inside this existing phase keeps the tick order
	// unchanged (mirrors the tickBlockBreak / tickBreath seams). No-op until the nether world is armed.
	t.tickNetherPortal()

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
	if t.cur().entities == nil {
		return // defensive: store is non-nil from NewTickLoop, but never panic if absent
	}

	// MOB-SUB-01 (Plan 29-02 / WR-03): the LivingEntity.baseTick i-frame decrement, run as its OWN
	// per-entity step BEFORE serverAiStep — never inside a goal callback / navigation.tick. In vanilla
	// baseTick the hurtTime--/invulnerableTime-- block runs in LivingEntity.tick() ahead of aiStep ->
	// serverAiStep for EVERY LivingEntity, INDEPENDENT of whether it has a goalSelector. Iterate the
	// FULL store (NOT the AI-only snapshot): an AI-less living entity that takes a hit arms its
	// invulnerableTime to 20 and would otherwise NEVER decrement, leaving it stuck in the upper i-frame
	// half (effectively immune). tickMobIFrames is self-gated (only touches hurtTime/invulnerableTime
	// when > 0), so it is a harmless no-op for non-living entities (items, XP orbs) that never arm them
	// — there is no isLiving flag yet, and gating on >0 is exactly equivalent to vanilla's per-field
	// `if (... > 0)`.
	//
	//	[VERIFIED javap net.minecraft.world.entity.LivingEntity.baseTick: at the isAlive-branch target
	//	 the block is `if (hurtTime > 0) hurtTime--;` (unconditional for every LivingEntity) then
	//	 `if (invulnerableTime > 0 && !(this instanceof ServerPlayer)) invulnerableTime--;` — neither
	//	 gated on AI. The ServerPlayer guard is honored elsewhere (combat.go ServerPlayer.tick); mobs
	//	 here are never ServerPlayer, so both fields decrement.]
	//
	// It is PURE INTEGER MATH (no RNG draw), so it cannot perturb the per-mob RNG stream the pig oracle
	// pins (PITFALLS Pitfall 5), and it stays structurally outside the AI RNG flow (its own loop, before
	// serverAiStep).
	for _, e := range t.cur().entities.byID {
		t.tickMobIFrames(e)
		// Entity.baseTick fire block (fire.go): while burning, deal on_fire damage every 20 ticks +
		// keep the on-fire shared-flag synced + extinguish in water. Pure-int + a gated damage/broadcast;
		// NO RNG draw, and a non-burning entity (remainingFireTicks==0, the oracle pig) is an early-return
		// no-op → the pig oracle's pinned stream is unperturbed (PITFALLS Pitfall 5).
		t.tickEntityFire(e)
		// MOB-SUB-08 (Plan 33-01): AgeableMob aging, in the SAME OUTSIDE-serverAiStep per-mob loop as the
		// i-frame decrement (vanilla runs aging in aiStep; we run it here — pure-int, no draw — to keep it
		// off the per-mob RNG stream the pig oracle pins; the cited oracle-preserving optimization). A baby
		// (breedAge<0) ages up toward 0, an adult on cooldown (breedAge>0) decays toward 0, an un-fed adult
		// (breedAge==0, the oracle pig) is a no-op. Self-gated on breedAge sign, so it is a harmless no-op
		// for non-animals (items/orbs, breedAge 0).
		t.tickMobAging(e)
	}

	// WR death-animation drive: LivingEntity.baseTick runs `if (isDeadOrDying() && shouldTickDeath(this))
	// tickDeath();` RIGHT AFTER the i-frame block above (javap baseTick: the hurtTime--/invulnerableTime--
	// block at offsets 450-491, then the isDeadOrDying -> tickDeath gate at 491-513). A dead mob runs
	// baseTick (so its deathTime counts up and it is removed at 20) but does NOT run aiStep goals — the
	// fall-over is a CLIENT animation driven by the die() status-3 broadcast, not server movement. We
	// snapshot the byID values first because tickDeath removes the entity from the byID map at deathTime
	// >= 20 (ranging the map directly while deleting from it is unsafe).
	//
	//	[VERIFIED javap LivingEntity.baseTick: after the i-frame decrements, `isDeadOrDying ifeq skip;
	//	 level.shouldTickDeath(this) ifeq skip; tickDeath()`. The dead corpse stays in the store (aiStep
	//	 is gated only on !isRemoved, but its goals self-gate and v1 deliberately skips them for a corpse —
	//	 a dead mob does not path).]
	deadSnapshot := make([]*Entity, 0, len(t.cur().entities.byID))
	for _, e := range t.cur().entities.byID {
		if e.dead {
			deadSnapshot = append(deadSnapshot, e)
		}
	}
	for _, e := range deadSnapshot {
		t.tickDeath(e) // ++deathTime; at >=20 broadcast the status-60 poof + remove via the owner region
	}

	// Snapshot the AI mobs so the serverAiStep loop is stable even if a spawn (below) or a move
	// re-buckets mid-range — exactly the discipline tickPhysics uses (copy the byID values, then range).
	// A DEAD mob is EXCLUDED: a corpse does not run AI/goals/navigation (it is counting down its death
	// animation via tickDeath above), matching vanilla where a dying mob's goals self-gate to no-ops.
	snapshot := make([]*Entity, 0, len(t.cur().entities.byID))
	for _, e := range t.cur().entities.byID {
		if e.ai != nil && !e.dead {
			snapshot = append(snapshot, e)
		}
	}

	for _, e := range snapshot {
		e.ai.serverAiStep(t, e) // 07-01 goals + 07-02 navigation: the real ported AI walk
		// MOB item-pickup (net.minecraft.world.entity.Mob.aiStep looting block): a mob that canPickUpLoot()
		// scans getBoundingBox().inflate(1,0,1) for dropped items and picks them up (item_entity_mob.go). Runs
		// as part of aiStep for EVERY live mob, gated INSIDE mobPickupItems on e.canPickUpLoot -- FALSE for every
		// Animal (the oracle pig), so a non-pickup mob returns at the first gate with ZERO new RNG draws and the
		// pig oracle stream is byte-identically unperturbed. Only the Fox (setCanPickUpLoot(true)) enters it in
		// v1. Placed AFTER serverAiStep (mirroring vanilla aiStep, where the looting scan runs after the goal/
		// nav tick). Cite Mob.aiStep looting block.
		t.mobPickupItems(e)
		// MOB-PASS-03 (Phase 34): the Chicken.aiStep server extras (slow-fall + egg-lay). Vanilla runs
		// aiStep INDEPENDENTLY of the running goals (Mob.aiStep -> customServerAiStep), so it fires every
		// tick for a live chicken regardless of which goal is active. It is gated on typ == entity.Chicken.ID
		// — a PER-TYPE branch (the minimal faithful wiring; no new generic customServerAiStep seam in
		// ai_mob.go that would risk the pig oracle stream). The slow-fall lands HERE (in tickAI, before
		// tickPhysics integrates this tick's gravity), mirroring where the pig jump impulse lands, so the
		// y *= 0.6 dampens the velocity tickPhysics then integrates. ADDITIVE + chicken-gated: the pig (and
		// every non-chicken mob) is a zero-cost skip, so the pig oracle's RNG stream gains ZERO draws.
		if e.typ == entity.Chicken.ID {
			t.chickenAiStep(e)
		}
		// MOB-HOST-06 (Task #9): the Creeper.tick fuse advance (swell += swellDir, explode at maxSwell).
		// Runs INDEPENDENTLY of the goals (Creeper.tick), per-type-gated like the chicken, AFTER
		// serverAiStep so the SwellGoal has set swellDir this tick. ADDITIVE + creeper-gated (zero cost /
		// zero RNG for every non-creeper — the pig oracle stream is untouched).
		if e.typ == entity.Creeper.ID {
			t.creeperAiStep(e)
		}
		// MOB-HOST-08 (Task #9): the EnderMan.customServerAiStep daylight-flee (random teleport away when
		// brightly lit + sky-exposed). Per-type-gated like the creeper/chicken, AFTER serverAiStep.
		// ADDITIVE + enderman-gated (zero cost / zero RNG for every non-enderman — pig oracle untouched).
		if e.typ == entity.Enderman.ID {
			t.endermanAiStep(e)
		}
		// MOB-PASS-05 (rabbit hop): the Rabbit's RabbitJumpControl/RabbitMoveControl hop-vs-walk movement
		// (Rabbit.customServerAiStep + aiStep counter). Per-type-gated like the creeper/enderman, AFTER
		// serverAiStep (so the stroll/panic MOVE goal has committed its want this tick). ADDITIVE +
		// rabbit-gated (zero cost / zero RNG for every non-rabbit — the pig oracle stream is untouched).
		if e.typ == entity.Rabbit.ID {
			t.rabbitAiStep(e)
		}
		// MOB-CUBE (SulfurCube): the AbstractCubeMob CubeMobMoveControl.tick (yaw rotlerp + jump-on-delay +
		// the direct travel drive) + the squish edge. Per-type-gated like the creeper/enderman, AFTER
		// serverAiStep so the three cube goals have set the move-control state this tick. ADDITIVE +
		// cube-gated (zero cost / zero RNG for every non-cube - the pig oracle stream is untouched).
		if e.typ == entity.SulfurCube.ID {
			t.sulfurCubeAiStep(e)
		}
		// happy_ghast (Task): the HappyGhast FLIGHT (RandomFloatAroundGoal fly-to selection +
		// GhastMoveControl.tick deltaMovement kick). Runs the moveControl-driven hover (NOT the ground A*
		// nav), per-type-gated like the creeper/enderman, AFTER serverAiStep. The 0.91 flying drag +
		// no-gravity integration land in tickPhysics (also happy-ghast-gated). ADDITIVE + ghast-gated
		// (zero cost / zero RNG for every non-ghast — the pig oracle stream is untouched).
		if e.typ == entity.HappyGhast.ID {
			t.happyGhastAiStep(e)
		}
		// The Fox character-layer per-tick extras (Fox.tick + Fox.aiStep server branch): the crouch/
		// interested animation lerp, ++ticksSinceEaten, the wake/sit-in-water/target-lost state clears,
		// and the sleep immobility (jump+horizontal-velocity zero). Per-type-gated like the creeper/chicken,
		// AFTER serverAiStep so this tick's fox goals have set the flags. ADDITIVE + fox-gated (zero cost /
		// zero RNG for every non-fox — the pig oracle stream is untouched).
		if e.typ == entity.Fox.ID {
			t.foxAiStep(e)
		}
		// MOB-PREY (Task #9): the Endermite.aiStep despawn timer (life++ while non-persistent, discard at
		// life>=2400). Per-type-gated like the creeper/enderman, AFTER serverAiStep. ADDITIVE + endermite-gated
		// (zero cost / zero RNG for every non-endermite - the pig oracle stream is untouched).
		if e.typ == entity.Endermite.ID {
			t.endermiteAiStep(e)
		}
		// MOB-HOST-07 (Task #9, heal branch): the Witch.aiStep self-drink potion buff (roll the potion
		// ladder + advance the drink countdown + apply the self-effect on finish) plus tickMobEffects (the
		// entity-side effect countdown — regeneration heal, buff expiry). Per-type-gated like the creeper/
		// enderman, AFTER serverAiStep. ADDITIVE + witch-gated (zero cost / zero RNG for every non-witch —
		// the pig oracle stream is untouched). Cite Witch.aiStep + LivingEntity.tickEffects.
		if e.typ == entity.Witch.ID {
			t.witchAiStep(e)
			t.tickMobEffects(e)
		}
		// RAIDER (Task): the Ravager.aiStep roar/stun/attack countdowns + the roar() AoE. Per-type-gated
		// like the creeper/enderman, AFTER serverAiStep so this tick's target/melee state is set. ADDITIVE +
		// ravager-gated (zero cost / zero RNG for every non-ravager — the pig oracle stream is untouched).
		if e.typ == entity.Ravager.ID {
			t.ravagerAiStep(e)
		}
		// RAIDER (Task): the SpellcasterIllager.customServerAiStep spell-cast countdown for an Evoker.
		// Per-type-gated like the ravager, AFTER serverAiStep. ADDITIVE + evoker-gated (zero cost / zero RNG
		// for every non-evoker — the pig oracle stream is untouched).
		if e.typ == entity.Evoker.ID {
			t.evokerAiStep(e)
		}
		// VEX (Task): the Vex.tick + the vex goals (charge/random-move/copy-owner-target) + VexMoveControl
		// flight, driven per-type like the evoker/ravager, AFTER serverAiStep (the vex's empty goalSelector
		// no-op). ADDITIVE + vex-gated: every non-vex entity is a zero-cost skip, and all the vex's RNG draws
		// are on its OWN per-entity stream, so no other mob's lockstep (the pig oracle) is perturbed.
		if e.typ == entity.Vex.ID {
			t.vexAiStep(e)
		}
		// IRON GOLEM (Task): the IronGolem.aiStep countdowns (attackAnimationTick + offerFlowerTick decrements)
		// + updatePersistentAnger (the gametime-endpoint anger expiry, a no-op read). Per-type-gated like the
		// ravager/vex, AFTER serverAiStep. ADDITIVE + golem-gated (zero cost / zero RNG for every non-golem —
		// the pig oracle stream is untouched). Cite IronGolem.aiStep.
		if e.typ == entity.IronGolem.ID {
			t.ironGolemAiStep(e)
		}
		// VILLAGER (Task): the Villager.customServerAiStep brain tick — the villager is a BRAIN mob (no
		// classic goals), so the brain (Swim/LookAtTargetSink/MoveToTargetSink + AcquirePoi(JOB_SITE) +
		// AssignProfessionFromJobSite) is the sole AI driver. Per-type-gated like the golem, AFTER
		// serverAiStep (the villager's empty goalSelector is a no-op). ADDITIVE + villager-gated (zero cost /
		// zero RNG for every non-villager — the pig oracle stream is untouched; the AcquirePoi rate jitter
		// draws off the region levelRandom, never the mob stream). Cite Villager.customServerAiStep.
		if e.typ == entity.Villager.ID {
			t.villagerBrainTick(e)
		}
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
	if t.cur().entities == nil {
		return // defensive: store is non-nil from NewTickLoop, but never panic if absent
	}

	// Snapshot the live entities so the loop is stable even if a move re-buckets mid-range.
	snapshot := make([]*Entity, 0, len(t.cur().entities.byID))
	for _, e := range t.cur().entities.byID {
		snapshot = append(snapshot, e)
	}

	for _, e := range snapshot {
		// A DEAD mob is frozen for its death-animation window: the ~1s fall-over is a CLIENT animation
		// driven by the die() status-3 broadcast, NOT server movement, so freezing the server position
		// for the 20 ticks until tickDeath removes it matches exactly what the client renders. This is a
		// cited v1 simplification (vanilla corpses still integrate gravity, but with goals/navigation
		// stopped a settled corpse barely moves; freezing it avoids running physics on a thing about to
		// despawn and keeps the observable result identical). The corpse is still in the store so the
		// tracker keeps sending it through the animation.
		if e.dead {
			continue
		}

		// VEX (Task): a Vex is a FLYING mob (Vex.tick sets noPhysics=true + setNoGravity(true)) whose
		// movement is integrated in vexAiStep (the VexMoveControl delta + the direct entities.move flight,
		// no collision, no gravity). Skip it here so the generic gravity/drag/collision path never touches
		// it -- the observable "the vex flies freely toward its wanted position, unaffected by gravity".
		// Cite Vex.tick (noPhysics=true; setNoGravity(true)).
		if e.isVex {
			continue
		}

		// LIVE-DEBUG A (the "mobs sink in water" fix): a mob whose AABB is in water runs the
		// VANILLA water physics (LivingEntity.travelInWater) INSTEAD of the dry travelInAir path —
		// vertical drag 0.8 (NOT the 0.98 air drag) + reduced gravity baseGravity/16 == 0.005 (NOT
		// the full 0.08). Without this the dry 0.08 gravity sank the mob faster than FloatGoal's
		// +0.04 swim impulse could lift it, so a pig dropped in water swam briefly then sank to the
		// floor. travelInWaterVertical mirrors travelInWater's vertical ops + getFluidFallingAdjusted
		// Movement (fluid_travel.go, javap-cited); mobInWater is the Phase-30 predicate (fluid_physics
		// .go). A DRY mob takes the unchanged air branch below. The dead-mob corpse is already frozen
		// (the `if e.dead { continue }` guard above), so a corpse never swims — the live-mob gate holds.
		if t.mobInWater(e) {
			// Water branch: travelInWater vertical (0.8 drag + 0.005 gravity) replaces gravity +
			// air drag + the dry horizontal friction (the 0.8 horizontal water drag is applied
			// inside travelInWaterVertical). FloatGoal's +0.04 impulse (applied in tickAI, before
			// this) survives the gentle 0.005 pull, so the mob bobs at the surface instead of sinking.
			travelInWaterVertical(e)
		} else if happyGhastIsFlyer(e) {
			// happy_ghast (Task): the travelFlying AIR branch (HappyGhast.travel → LivingEntity.travelFlying).
			// There is NO gravity for a hovering ghast; deltaMovement is scaled by 0.91 on ALL three axes
			// (deltaMovement *= 0.91f) so the moveControl kick (happyGhastAiStep) drifts and settles. The
			// ghast keeps its Y (does not sink) — this is the whole point of the mob. Cite HappyGhast.travel /
			// LivingEntity.travelFlying (air branch: move(deltaMovement); deltaMovement *= 0.91f; no gravity).
			e.vx *= ghastFlyingDrag
			e.vy *= ghastFlyingDrag
			e.vz *= ghastFlyingDrag
		} else {
			// Dry (travelInAir) branch: accelerate downward, then air drag so vertical speed
			// converges to a terminal velocity (06-RESEARCH A1 — tunable, wire-irrelevant constants).
			e.vy -= gravityPerTick
			e.vy *= airDrag

			// Horizontal friction: a moving entity slows instead of sliding forever.
			e.vx *= horizontalFriction
			e.vz *= horizontalFriction
		}

		// LIVE-DEBUG B (mob fall damage). VANILLA ORDER: Entity.move() integrates the motion AND, at its
		// tail, calls checkFallDamage(this.getY() - yBefore, onGround, ...) with the ACTUAL resolved
		// vertical displacement — NOT the pre-move velocity intent. This distinction is load-bearing: a
		// mob STANDING on the ground still has gravity pull vy to -0.08 each tick, but moveEntity clips
		// that against the floor so the ACTUAL moved deltaY ≈ 0. Accumulating the pre-move intent (-0.08)
		// instead added phantom fall distance every tick on flat ground (the "mobs on land taking
		// constant fall damage" bug — fallDistance climbed 0.0784/tick from gravity*airDrag even at rest).
		// So: capture y, move, then accumulate from the REAL displacement.
		yBefore := e.y

		// Integrate via the per-axis swept resolver (the anti-tunneling discipline). This
		// also re-buckets through entities.move and updates onGround / zeroes blocked
		// velocity components.
		t.moveEntity(e, e.vx, e.vy, e.vz)

		// Entity.checkFallDamage(actualDeltaY, onGround, ...) — run with the resolved displacement, the
		// freshly-set onGround, and isInWater re-read at the SETTLED position (vanilla reads all three
		// fresh here). actualDeltaY = e.y - yBefore: ~0 for a grounded mob (no phantom accumulation),
		// negative for a real fall. `if (!isInWater && actualDeltaY<0) fallDistance -= (float)
		// actualDeltaY; if (onGround) { if (fallDistance>0) causeFallDamage(...); resetFallDistance(); }`.
		actualDeltaY := e.y - yBefore
		mobWet := t.mobInWater(e)
		t.accumulateMobFallDistance(e, actualDeltaY, mobWet)
		t.landMobFallDamage(e, mobWet)
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
	if t.world() == nil {
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
	// NETHER: read Ready columns from the player's-dimension world so a nether player receives nether
	// chunks. dimWorld(p) is the nether manager for a dimNether player, the overworld otherwise.
	mgr := t.dimWorld(p)
	if mgr == nil {
		return
	}
	for _, pos := range ring {
		if len(batch) >= limit {
			break
		}
		if p.sentChunks[pos] {
			continue
		}
		ch, ok := mgr.Get(pos)
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
