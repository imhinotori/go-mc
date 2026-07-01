package server

// async.go is the Phase-8 ASYNC SUBSTRATE (Wave 0, OPT-04/OPT-06). It introduces the
// verified concurrency stack (ants/v2 + xsync/v4) and the contract-first scaffolding every
// later Phase-8 wave (OPT-01/02/03) fills:
//
//   - newAsyncPool: the per-subsystem bounded, NON-BLOCKING ants goroutine-pool factory. A
//     saturated Submit returns ants.ErrPoolOverload and the caller DROPS the work (keeping its
//     last action and re-requesting next tick), never a blocking Submit that would stall the
//     tick — mirroring world.Worker.Request's drop-on-full backpressure (08-RESEARCH Pitfall 4).
//
//   - pathReady / trackerDiffReady / spawnCandidatesReady: the three concrete asyncResult
//     CONTRACTS the OPT-01/02/03 plans implement. Each satisfies the EXISTING, UNCHANGED
//     asyncResult interface{ applyTo(*TickLoop) } (tick.go), carries an id/value for the
//     on-apply validity re-check (NOT a live *Entity/*tickPlayer pointer — 08-RESEARCH
//     Pitfall 2/3), and ships a documented applyTo STUB the owning plan fills.
//
// THE REJOIN DISCIPLINE (the load-bearing invariant): a pool worker computes PURE over an
// IMMUTABLE snapshot copied ON the owner before Submit, then rejoins by sending an asyncResult
// on TickLoop.asyncIn2; applyAsyncResults drains it on the OWNER goroutine and the ONLY mutation
// happens there, inside applyTo (TICK-05). This is the Phase-4 chunkReady flow generalized
// (tick.go chunkReady / SetWorld), the PROVEN reference these contracts copy verbatim.
//
// xsync/v4 is added to go.mod HERE so the Phase-8 stack lands in one place, but it is
// JUSTIFIED-PER-USE only in OPT-04: the default is snapshot-on-owner + plain map (single-owner =
// faster), and a collection becomes xsync ONLY where an off-tick worker reads the LIVE
// collection concurrently with a tick write (08-RESEARCH Pitfall 1 / Open Question 1). This
// file imports it for the substrate work-queue/counter primitives the pools draw on; a blanket
// map swap is NOT done here.

import (
	"github.com/panjf2000/ants/v2"
	"github.com/puzpuzpuz/xsync/v4"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// asyncSubmitDrops is the counter of pool-Submit overflows (ErrPoolOverload), shared across the
// per-subsystem pools so the operator/telemetry can see how often the async substrate is degrading
// to "compute a tick later" under saturation. It is an xsync.Counter — a striped, contention-
// friendly counter the submit paths increment from any goroutine without a mutex (08-RESEARCH
// Pitfall 4 backpressure observability).
//
// This is the ONE JUSTIFIED xsync/v4 usage the OPT-04 audit (08-06, collections_audit.go) keeps: a
// value GENUINELY crossing the async boundary in BOTH directions — the submit paths (any goroutine)
// Inc() it, and the TICK goroutine reads its Value() each tick (recordMSPT -> TickStats.AsyncDrops,
// surfaced by TickLoop.AsyncDrops). A plain int64 would race that concurrent inc-vs-read; the
// xsync.Counter is the correct lock-free primitive. It is NOT a tick-only collection (those stay
// plain maps per Pitfall 1 — see the audit table). The OPT-04 audit concluded NO tick-owned map
// needs converting (snapshot-and-stay-plain), so this remains the only xsync map/counter use; the
// other half of OPT-04 ("worker pools use ants/v2") is the pathPool/trackerPool/spawnPool trio.
var asyncSubmitDrops = xsync.NewCounter()

// newAsyncPool constructs one per-subsystem ants goroutine pool sized to `size`. It is
// NON-BLOCKING (ants.WithNonblocking(true)): when every worker is busy a Submit returns
// ants.ErrPoolOverload rather than parking the caller, so a saturated pool DROPS the request and
// the subsystem keeps its last action / re-requests next tick — NEVER a blocking Submit that
// would stall the owner goroutine (08-RESEARCH Pitfall 4; the in-process analogue of
// world.Worker.Request's `select { case w.requests <- pos: default: }` drop-on-full).
//
// The pool caps goroutine count and recycles workers, so a burst of submits (e.g. many mobs
// pathing at once) can never blow the goroutine count up. ants.NewPool only errors for an
// invalid size (size <= 0 with the default options), which is a programming error at
// construction, not a runtime condition — so we panic on it (it can never fire for the positive
// sizes NewTickLoop passes). Callers Release() the pool on shutdown (TickLoop.Close).
func newAsyncPool(size int) *ants.Pool {
	p, err := ants.NewPool(size, ants.WithNonblocking(true))
	if err != nil {
		// size <= 0 is the only failure mode and is a construction-time programming error;
		// every NewTickLoop call site passes a positive size, so this is unreachable in practice.
		panic("server: newAsyncPool: " + err.Error())
	}
	return p
}

// submitOrDrop submits work to a non-blocking ants pool with the drop-on-overload discipline,
// centralizing the Pitfall-4 backpressure so every OPT-01/02/03 submit site behaves identically.
// It returns true if the work was accepted by the pool, false if the pool was saturated (the
// caller then keeps its last action and re-requests next tick). On a drop it bumps
// asyncSubmitDrops for observability. A nil pool (a Phase-3-style TickLoop that never wired the
// Phase-8 setup) is treated as saturated — a safe no-op drop — so the substrate is robust even
// before a real subsystem is swapped in.
func submitOrDrop(pool *ants.Pool, work func()) bool {
	if pool == nil {
		asyncSubmitDrops.Inc()
		return false
	}
	if err := pool.Submit(work); err != nil {
		// ants.ErrPoolOverload (the non-blocking pool is full): DROP the work rather than block
		// the tick. The subsystem keeps its last state and re-requests on a later tick.
		asyncSubmitDrops.Inc()
		return false
	}
	return true
}

// playerByEntityID re-resolves the tick-owned player carrying entityID by scanning the players
// slice on the OWNER goroutine, returning nil if no such player is currently registered (it left
// between an async Submit and its applyTo — Pitfall 3). It is the on-apply existence re-check
// trackerDiffReady.applyTo uses: the result carries the player's entity id (a value), never a
// live *tickPlayer pointer, so a late diff to a departed player is dropped, not crashed. A linear
// scan is correct here — player counts are small and applyTo runs at most once per result on the
// owner; a despawned id simply finds no match. Tick-owned (called only on the tick goroutine).
func (t *TickLoop) playerByEntityID(id int32) *tickPlayer {
	for _, p := range t.players {
		if p.entityID == id {
			return p
		}
	}
	return nil
}

// --- The three asyncResult CONTRACTS (OPT-01/02/03 implement applyTo) ---------------------
//
// Each type below satisfies the EXISTING asyncResult interface{ applyTo(*TickLoop) } (tick.go,
// UNCHANGED). They are the immutable messages a pool worker hands back to the owner via
// asyncIn2. The cardinal rule (08-RESEARCH Pitfall 2/3): a result carries an ID/VALUE for an
// on-apply existence/validity RE-CHECK — never a live *Entity/*tickPlayer pointer — because the
// world moves on between Submit (off-tick) and applyTo (1+ ticks later, on the owner). applyTo
// re-resolves the target on the owner and DROPS the result if the target is gone or has changed.
//
// For THIS Wave-0 plan every applyTo is a validate-then-no-op STUB: it performs the owner-side
// validity re-check (proving the contract + the despawn/retarget drop path) and then leaves a
// documented placeholder where the owning plan inserts the real mutation. NO executor is swapped
// here — these are the blueprints the OPT plans build against.

// pathReady is the OPT-01 (async pathfinding, 08-02) rejoin message. A pool worker ran the PURE
// computePath over an immutable pathRequest snapshot and hands back the resulting *Path. It
// carries the mob's id and the goal target (NOT a live *Entity — Pitfall 2/3) so applyTo can,
// on the owner, confirm the mob still exists and still wants exactly this path before adopting
// it. A path tolerated 1+ ticks late may arrive after the mob despawned or retargeted; applyTo
// drops it in that case and navigation re-requests next tick (OPT-01 "tolerated 1+ ticks late").
type pathReady struct {
	mobID  int32  // the entity to re-resolve on apply (existence re-check); never a live pointer
	target [3]int // the goal this path was computed for; dropped on apply if the goal changed
	path   *Path  // the immutable result computed off-tick by computePath (may be nil = no path)
}

// applyTo runs on the OWNER goroutine inside applyAsyncResults — the OPT-01 (08-02) implementation
// of the Wave-0 contract. It re-validates the late result before adopting it (08-RESEARCH Pitfall
// 2, the load-bearing safety: a path 1+ ticks late may arrive after the mob despawned or
// retargeted):
//
//  1. The mob must still exist in the authoritative store (r.mobID re-resolves to a live Entity)
//     and carry an AI handle. A despawned mob → DROP (no nil-deref). e.ai.navigation is a struct
//     VALUE on mobAI, never nil — only e.ai (the pointer) is guarded; testing e.ai.navigation==nil
//     would be a compile error on a struct value.
//  2. The navigation must still want EXACTLY this goal: its tracked target (lastTX/Y/Z) must equal
//     r.target and it must still have a target. The mob retargeting mid-flight (a new requestPath
//     reset lastT* to a different goal) → DROP the stale result via the EXISTING target tracking —
//     no separate timeout (08-RESEARCH Open Question 4). This is also the safety net for the
//     single-in-flight gate: even if a retarget submitted a second compute, the first (stale)
//     result is dropped here.
//
// Only a still-valid result is adopted: nav.path = r.path (the late path the mob follows next tick)
// and nav.pending = false (the in-flight gate clears, so shouldRecomputePath may submit again).
// Carrying r.mobID + r.target (plain values, NOT a live *Entity) is what makes this late apply safe.
func (r pathReady) applyTo(t *TickLoop) {
	// Phase-27 STEP-3 (Pitfall 1): re-resolve the OWNING region by id (the mob may live in either
	// region, or have transferred/despawned). owningRegion scans the regions on the coordinator
	// (quiescent at the barrier) and returns nil if no region owns it → DROP (the existing
	// "drop if gone" discipline extended cross-region — a path for a despawned/un-owned mob is
	// discarded, never applied to the wrong region).
	reg := t.owningRegion(r.mobID)
	if reg == nil {
		return // no region owns the mob (despawned): DROP the late path
	}
	e, ok := reg.entities.get(r.mobID)
	if !ok || e.ai == nil {
		return // mob despawned (or has no AI) while the path computed (Pitfall 2): DROP the result
	}
	nav := &e.ai.navigation
	if !nav.hasTarget || nav.lastTX != r.target[0] || nav.lastTY != r.target[1] || nav.lastTZ != r.target[2] {
		return // the mob retargeted mid-flight: this path is for a stale goal — DROP it
	}
	// Still-valid: adopt the late path on the owner; the mob starts following it next tick.
	nav.path = r.path
	nav.pending = false
}

// trackerDiffReady is the OPT-02 (async entity tracker, 08-04) rejoin message. The off-tick
// worker computed the per-player visibility DIFF (spawn/teleport/remove decision math) over an
// immutable position+tracked-set snapshot and hands back the resulting clientbound packets. It
// carries the player's entity id (NOT a live *tickPlayer — Pitfall 3) so applyTo can re-resolve
// the player on the owner and SEND the packets there. Packet emission MUST stay owner-side: it
// enqueues onto the bounded per-player outbound queue the writeLoop owns (Pitfall 5 / the
// Phase-5 ChannelQueue close-vs-send race) — only the diff MATH goes off-tick.
type trackerDiffReady struct {
	playerID int32       // the player to re-resolve on apply; never a live *tickPlayer pointer
	packets  []pk.Packet // the immutable visibility-diff packets to Send on the owner
	// added / removed are the per-player tracked-set DELTA the off-tick diff computed: `added`
	// are the ids newly-in-range this diff spawned (an AddEntity is in `packets`), `removed` are
	// the ids that left range (a single RemoveEntities is in `packets`). They are plain value
	// slices so the owner can update p.tracked deterministically in applyTo WITHOUT the worker
	// ever touching the live tracked map (08-RESEARCH Pitfall 1: p.tracked stays a plain map
	// mutated only on the owner). Carrying the delta — not the whole new set — keeps the message
	// small and makes the apply an O(delta) owner-side bookkeeping step that exactly mirrors what
	// the packets did. A still-in-range, already-tracked entity appears in NEITHER list (its
	// TeleportEntity/RotateHead is in `packets` but the tracked membership is unchanged).
	added   []int32
	removed []int32
}

// applyTo runs on the OWNER goroutine inside applyAsyncResults — the OPT-02 (08-04)
// implementation of the Wave-0 contract. The off-tick worker computed the per-player visibility
// diff over an immutable snapshot; this re-resolves the player on the owner and performs the ONLY
// two owner-side mutations:
//
//  1. Existence re-check (08-RESEARCH Pitfall 2/3): r.playerID re-resolves over the live players
//     slice. A player who LEFT between Submit and apply finds no match → DROP the whole result
//     (no send to a gone player, no nil-deref). The id is a plain value, never a live pointer.
//  2. Owner-side emission + bookkeeping (08-RESEARCH Pitfall 5 — the load-bearing invariant):
//     each diff packet is p.client.Send(pkt) HERE, on the owner, because Send enqueues onto the
//     bounded per-player outbound queue the writeLoop solely owns (sending off-tick would race
//     the queue / the Phase-5 ChannelQueue close-vs-send fix). Then p.tracked is updated from the
//     carried delta — newly-spawned ids added, departed ids deleted — so the NEXT tick's diff
//     sees the correct tracked set. The worker NEVER did either of these; only the diff MATH ran
//     off-tick.
func (r trackerDiffReady) applyTo(t *TickLoop) {
	p := t.playerByEntityID(r.playerID)
	if p == nil {
		return // player left while the diff computed (Pitfall 3): DROP the stale packets
	}
	if p.client == nil {
		return // a player mid-registration / without a connection: nothing to emit onto
	}
	if p.tracked == nil {
		p.tracked = make(map[int32]bool) // lazy-init mirrors entityTracker (owner-side only)
	}
	// Owner-side emission: the bounded outbound queue / writeLoop stay the sole socket writer.
	for _, pkt := range r.packets {
		p.client.Send(pkt)
	}
	// Owner-side bookkeeping: apply the tracked delta so the next diff is computed against the
	// set that actually reflects what was just Sent (added spawns, removed despawns).
	for _, id := range r.added {
		p.tracked[id] = true
	}
	for _, id := range r.removed {
		delete(p.tracked, id)
	}
}

// spawnCandidate is one standable spawn location the OPT-03 (08-05) off-tick scan produced. It
// is a plain value (the immutable result of the column/Y scan over a world snapshot), carrying
// only the block coordinates — no live world or entity reference (Pitfall 3). applyTo re-checks
// the mob cap on the owner and adds at most one candidate, so a scan computed against a stale
// world snapshot can never over-spawn past the authoritative cap.
type spawnCandidate struct {
	x, y, z int // the standable block position the off-tick scan found (immutable value)
}

// spawnCandidatesReady is the OPT-03 (async mob spawning, 08-05) rejoin message. The off-tick
// worker scanned candidate spawn columns + standable Y over an immutable world snapshot and
// hands back the candidates. applyTo re-checks the live mob cap on the OWNER (the count over the
// authoritative store) before any entityStore.add, so the spawn MUTATION + the id allocation
// stay single-owner even though the read-only scan ran off-tick.
type spawnCandidatesReady struct {
	candidates []spawnCandidate // the immutable scan result; applyTo adds at most one under the cap

	// region is the Phase-27 STEP-3 source region that submitted this scan (the region whose
	// naturalSpawn ran in the fan-out + set spawnScanPending). applyTo clears THAT region's in-flight
	// gate (the gate is per-region) regardless of which region the candidate lands in. nil for a
	// legacy/test result (then globalRegion's gate is cleared, the N=1 behavior).
	region *region

	// spawnableChunkCount is the eligible-column count the OWNER captured at submit time (len of
	// spawnableColumns()), carried as a plain int so applyTo can recompute the cap
	// (maxInstancesPerChunk * spawnableChunkCount) on the owner without re-walking the columns. It
	// is the same scaling vanilla's getFilteredSpawningCategories uses; carrying it (a value, not a
	// live reference — Pitfall 3) keeps the apply-time cap derivation consistent with the gate the
	// scan was submitted under, while the live COUNT is re-read from the authoritative store.
	spawnableChunkCount int

	// category is the MobCategory this scan was submitted FOR (Phase 35-02, SC#3). The natural
	// spawner now runs TWO passes per cycle (CREATURE + the night-gated MONSTER pass); each submits
	// its own spawnCandidatesReady carrying the category it gated under, so applyTo re-checks the
	// RIGHT per-category cap (creatureCap for CREATURE, monsterCap for MONSTER) against
	// countByCategoryAcrossRegions()[category] and picks from the RIGHT species list
	// (pickNaturalCreatureMob vs pickNaturalMonsterMob). Carried as a plain value (Pitfall 3 — never
	// a live reference). The zero value (categoryMonster, iota 0) is NOT relied on as a default — every
	// submit site sets it explicitly; a legacy/test result that omits it would be treated as MONSTER,
	// so the test/CREATURE submit sites set categoryCreature explicitly.
	category mobCategory
}

// applyTo runs on the OWNER goroutine inside applyAsyncResults — the OPT-03 (08-05) implementation
// of the Wave-0 contract. The off-tick worker scanned standable candidates over an immutable
// solidity snapshot; this re-validates on the authoritative store and performs the ONLY owner-side
// mutation (the spawn add). It is the load-bearing anti-flood/anti-piling re-check (08-RESEARCH
// Pitfall 3): the scan's mob count was a snapshot, so the owner re-reads the live state before
// adding.
//
//  1. Clear the single-in-flight gate (t.only().spawnScanPending) FIRST and unconditionally, so the next
//     spawnInterval cycle can submit again even when this result places nothing (an empty candidate
//     set, an over-cap drop, or an occupied drop must never wedge the gate — Pitfall 4).
//  2. CAP RE-CHECK (the load-bearing safety): re-read the live CREATURE count over the authoritative
//     store (countByCategory) and compare to maxInstancesPerChunk * the carried spawnableChunkCount.
//     If AT/OVER cap now (mobs may have spawned since the scan), DROP — the stale scan never
//     over-spawns past the anti-flood cap (T-8-17).
//  3. Place ONE candidate: the FIRST whose position is not now occupied (the mobNear packing guard
//     re-checked against the LIVE store — a candidate a mob moved onto since the snapshot is dropped,
//     T-8-20). entityStore.add the Pig (fresh id from idAlloc, newPigAI attached). At most one per
//     apply (the throttle). The mutation (add + idAlloc) is owner-only (TICK-05 / T-8-18).
func (r spawnCandidatesReady) applyTo(t *TickLoop) {
	// Phase-27 STEP-3 (N=2): the in-flight gate is per-region. naturalSpawn set it on the region that
	// submitted the scan (r.region); clear it THERE so the next cycle on that region can submit again,
	// regardless of whether anything is placed below.
	src := r.region
	if src == nil {
		src = t.regions[globalRegion] // defensive: a result without a recorded region (legacy/test)
	}
	src.spawnScanPending = false

	if len(r.candidates) == 0 {
		return // the scan found no standable spot: nothing to apply (the gate is already cleared)
	}

	// CAP RE-CHECK on the AUTHORITATIVE store ACROSS REGIONS (Pitfall 3 anti-flood + Pitfall 1
	// cross-region cap): the off-tick scan counted a stale snapshot, so re-validate the live count
	// before mutating. This runs on the coordinator at the barrier (quiescent), so counting every
	// region's store is race-clean. A mob spawned/added since the scan can push us to cap — drop
	// rather than over-spawn. Phase 35-02 (SC#3): use the RIGHT per-category cap + live count for the
	// category this scan was submitted under (CREATURE vs MONSTER) so the MONSTER pass re-checks
	// monsterCap/countByCategoryAcrossRegions()[categoryMonster], not the CREATURE budget.
	cap := categorySpawnCap(r.category, r.spawnableChunkCount) // maxInstancesPerChunk * count / MAGIC_NUMBER (vanilla)
	live := t.countByCategoryAcrossRegions()[r.category]
	if live >= cap {
		return // now AT/OVER cap: DROP the stale candidates (no over-cap add)
	}

	// Place ONE candidate: the first still-unoccupied position (the mobNear packing guard re-checked
	// against the LIVE store across regions — the anti-piling guard survives the swap + the seam). A
	// candidate now occupied is skipped, not piled on.
	for _, c := range r.candidates {
		if t.mobNearAcrossRegions(float64(c.x)+0.5, float64(c.z)+0.5, 6.0) {
			continue // a mob moved/spawned onto this candidate since the snapshot: DROP it
		}
		// SWAP (PLUGIN-04 / Plan 24-02, generalized to all 4 CREATURE mobs in Plan 34-04): the natural
		// spawn now picks among the 4 PLUGIN-DRIVEN vanilla mobs (pig/cow/sheep/chicken), not the
		// pig-hardcoded path. Each is built from its boot-loaded Starlark declaration via
		// spawnDeclaredMob (real per-mob attrs + the declared 1:1 goals + per-entity RNG) and rendered
		// as its base wire id. All 4 are categoryOf -> CREATURE (34-CONTEXT) sharing the same
		// creatureCap, so the cap re-check above (categoryCreature) governs the pick uniformly — no
		// cap-math change. Phase-27 STEP-3 (N=2): add the mob to the region that OWNS the candidate
		// column (regionForColumn), not blindly globalRegion — withRegion registers that region so
		// spawnDeclaredMob's t.only().entities.add lands in the right store AND pickNaturalCreatureMob's
		// draw reads THAT region's seeded levelRandom (race-clean, deterministic per region — NOT the
		// unseeded global rand.IntN that caused the STATE.md async-spawner flake, T-34-11).
		dest := t.regionForColumn(columnOf(float64(c.x)+0.5, float64(c.z)+0.5))
		t.withRegion(dest, func() {
			// Phase 35-02 (SC#3): pick from the species list for the category this scan was submitted
			// under — pickNaturalCreatureMob (pig/cow/sheep/chicken) for CREATURE, pickNaturalMonsterMob
			// (zombie/skeleton/spider) for MONSTER. Both draw from THIS region's seeded levelRandom inside
			// the withRegion scope (race-clean, deterministic per region — T-34-11).
			name := t.pickNaturalSpawnMob(r.category)
			t.spawnVanillaMob(name, float64(c.x)+0.5, float64(c.y), float64(c.z)+0.5)
		})
		return // one placement per apply (the throttle)
	}
}

// naturalCreatureMobNames is the set the natural spawner picks among — the 4 vanilla CREATURE mobs that
// boot-load into the registry (vanillaMobNames). They ALL map to categoryOf -> CREATURE (34-CONTEXT),
// so they share the one creatureCap the apply-time re-check enforces; the pick only chooses WHICH
// creature to place in the one throttled slot. Kept as its own slice (not reusing vanillaMobNames
// directly) so a future non-CREATURE bundled mob added to the boot-load is NOT silently dragged into
// the creature spawn pool — the natural-spawn pool is an explicit, intentional list.
//
// NOTE (Task #9): the Mooshroom (vanilla_mooshroom) is boot-loaded + spawnable (declaration + /dbg) but
// is DELIBERATELY NOT in this uniform natural pool — vanilla spawns it ONLY in mushroom-field biomes. With
// no per-biome MobSpawnSettings weights yet, adding it here would spawn it everywhere (a behavior
// DEVIATION, not fidelity). It joins the pool once biome-gated spawn weights land (the cited deferral).
var naturalCreatureMobNames = []string{
	vanillaPigMobName,
	vanillaCowMobName,
	vanillaSheepMobName,
	vanillaChickenMobName,
	// MOB-PASS-05 (Task #9): the Rabbit spawns naturally on grass/snow overworld surfaces — an overworld
	// passive, so it JOINS the uniform CREATURE pool (unlike the biome-gated mooshroom).
	vanillaRabbitMobName,
}

// pickNaturalCreatureMob returns the name of one of the 4 vanilla CREATURE mobs to place at a natural
// spawn point. The choice is uniform-random among the 4 for v1 (a cited deferral: vanilla's per-biome
// MobSpawnSettings spawn WEIGHTS — different mobs/weights per biome — are a future subsystem, not yet
// ported; v1 spawns any of the 4 overworld passives with equal probability). The draw reads the OWNING
// region's seeded levelRandom (Level.random analogue, NextIntN), which is:
//   - SEEDED + per-region (newRegion seeds it from a unique nondeterministic seed) — NOT the unseeded
//     global rand.IntN that caused the STATE.md async-spawner flake (T-34-11);
//   - race-clean: levelRandom is advanced ONLY on its region's goroutine, and this runs inside
//     withRegion(dest) on that owner (the apply-time barrier is quiescent — TICK-05);
//   - deterministic for a given region seed + draw order, so a test that seeds the region's levelRandom
//     gets a reproducible pick sequence.
// It must be called ONLY inside a withRegion scope (cur() resolves the owning region); off a region it
// would fall back to region 0 / panic under strictRegion — exactly the discipline the rest of the
// per-region spawn path follows.
func (t *TickLoop) pickNaturalCreatureMob() string {
	n := int32(len(naturalCreatureMobNames))
	idx := t.cur().levelRandom.NextIntN(n)
	return naturalCreatureMobNames[idx]
}

// naturalMonsterMobNames is the set the natural spawner's MONSTER pass picks among — the 3 vanilla
// MONSTER mobs (Phase 35-02). They ALL map to categoryOf -> MONSTER (35-CONTEXT), so they share the
// one monsterCap the apply-time re-check enforces; the pick only chooses WHICH hostile to place in
// the one throttled slot. Kept as its OWN explicit slice (the exact discipline naturalCreatureMobNames
// uses) so a future non-MONSTER bundled mob added to the boot-load is NEVER silently dragged into the
// hostile spawn pool — the monster-spawn pool is an intentional, explicit list.
//
// NOTE (Phase boundary): the vanilla_zombie/skeleton/spider PLUGIN declarations + their embeds are
// boot-loaded by the sibling hostile-plugin plans (35-03..05); this plan (35-02) owns only the spawn
// GATING (category/cap/dark-gate/picker). The picker returns these names by value — a pickNaturalMonsterMob
// test asserts the name membership WITHOUT needing the embed to load; the live spawnVanillaMob lookup
// resolves them once the hostile declarations boot-load.
//
// NOTE (Task #9): the Husk (vanilla_husk) is boot-loaded + spawnable (declaration + /dbg) but is
// DELIBERATELY NOT in this uniform natural pool — vanilla spawns it ONLY in desert biomes (a special
// desert-zombie spawn). With no per-biome MobSpawnSettings weights yet, adding it here would spawn it
// everywhere (a behavior DEVIATION). It joins the pool once biome-gated spawn weights land (cited deferral).
var naturalMonsterMobNames = []string{
	vanillaZombieMobName,
	vanillaSkeletonMobName,
	vanillaSpiderMobName,
	// MOB-HOST-06 (Task #9): the Creeper spawns naturally in the overworld dark (unlike the biome-gated
	// husk/silverfish), so it JOINS the uniform MONSTER pool — a faithful overworld hostile.
	vanillaCreeperMobName,
	// MOB-HOST-08 (Task #9): the Enderman spawns naturally in the overworld dark too — JOINS the pool.
	vanillaEndermanMobName,
}

// pickNaturalMonsterMob returns the name of one of the 3 vanilla MONSTER mobs to place at a natural
// spawn point. The choice is uniform-random among the 3 for v1 (a cited deferral, the exact mirror of
// pickNaturalCreatureMob: vanilla's per-biome MobSpawnSettings spawn WEIGHTS are a future subsystem,
// not yet ported; v1 spawns any of the 3 overworld hostiles with equal probability). The draw reads
// the OWNING region's seeded levelRandom (Level.random analogue, NextIntN) — SEEDED + per-region +
// race-clean + deterministic, the same draw discipline pickNaturalCreatureMob obeys. Call ONLY inside
// a withRegion scope (cur() resolves the owning region).
func (t *TickLoop) pickNaturalMonsterMob() string {
	n := int32(len(naturalMonsterMobNames))
	idx := t.cur().levelRandom.NextIntN(n)
	return naturalMonsterMobNames[idx]
}

// pickNaturalSpawnMob dispatches the species pick to the right category list (Phase 35-02). The
// natural spawner runs a CREATURE pass and a night-gated MONSTER pass; each carries its category on
// the spawnCandidatesReady message so applyTo picks from the matching explicit list. Any non-MONSTER
// category falls through to the CREATURE picker (v1 only spawns CREATURE + MONSTER naturally).
func (t *TickLoop) pickNaturalSpawnMob(cat mobCategory) string {
	if cat == categoryMonster {
		return t.pickNaturalMonsterMob()
	}
	return t.pickNaturalCreatureMob()
}
