package server

import (
	"context"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/yggdrasil/user"

	"github.com/google/uuid"
	"github.com/panjf2000/ants/v2"
	"github.com/puzpuzpuz/xsync/v4"
)

// msptRingSize is the number of recent tick durations kept for the rolling
// MSPT average / p99. 100 ticks = 5s of history at 20 TPS.
const msptRingSize = 100

// Loop timing constants shared by the production driver (Run) and the test seam
// (advance) so the accumulator/clamp/step logic is defined in exactly one place.
const (
	// tickStep is one logical Minecraft tick: 50ms => 20 logical ticks/second.
	tickStep = 50 * time.Millisecond
	// wakeInterval is how often Run wakes to sample the clock; faster than a tick
	// for fine-grained input. The LOGICAL tick is wake-rate-independent (TICK-02).
	wakeInterval = 5 * time.Millisecond
	// maxFrame is the spiral-of-death clamp: a single frame delta is capped here
	// before entering the accumulator, so a long stall degrades to slowdown
	// (TPS < 20) instead of an unbounded catch-up freeze. 250ms => <=5 catch-up
	// steps per frame.
	maxFrame = 250 * time.Millisecond
)

// TickStats is an immutable, read-only snapshot of tick telemetry. The tick
// goroutine is the SOLE writer; observers read it lock-free via TickLoop.Stats().
// Never a backdoor to mutate game state off-thread — it is pure telemetry.
type TickStats struct {
	MSPTavg  float64 // rolling average tick duration, milliseconds
	MSPTp99  float64 // rolling p99 tick duration, milliseconds
	TPS      float64 // effective ticks per second (capped at 20)
	GameTime int64   // current game-time counter (age of the world in ticks)

	// AsyncDrops is the cumulative async-pool overload count (asyncSubmitDrops.Value()) republished
	// each tick. It is the OPT-04 read half of the ONE justified xsync.Counter (collections_audit.go):
	// the off-tick submit paths Inc() the counter via submitOrDrop, and the TICK goroutine reads it
	// here for telemetry — a genuine multi-writer/cross-boundary value where xsync.Counter is the
	// correct lock-free primitive (a plain int64 would race the read against the increments). It lets
	// an operator SEE how often the async substrate is degrading to "compute a tick later" under
	// saturation (08-RESEARCH Pitfall 4 backpressure observability).
	AsyncDrops int64
}

// asyncResult is the immutable message an async worker returns to the tick goroutine,
// applied on-thread inside applyAsyncResults. The seam exists from day one (Phase 3)
// so concrete result types attach without reordering the pipeline; Plan 04-03 is the
// first to feed it (chunkReady).
type asyncResult interface{ applyTo(*TickLoop) }

// chunkReady is the off-tick chunk worker's rejoin message (WORLD-01). It wraps an
// IMMUTABLE world.ChunkResult: the worker computed the chunk off-thread and now hands
// sole ownership to the tick. applyTo runs on the OWNER goroutine inside
// applyAsyncResults — the single insert that crosses the off-tick boundary — keeping
// the rejoin -race clean by construction (threat T-4-05).
type chunkReady struct{ res world.ChunkResult }

// applyTo inserts the worker-produced chunk into the tick-owned ChunkManager. On a
// generation/region error it reverts the holder to Empty so tickChunks can re-request
// the column on a later tick (threat W1 retry) — it NEVER inserts a nil chunk. This is
// the ONLY mutation that crosses the off-tick boundary, and it runs on the owner.
func (r chunkReady) applyTo(t *TickLoop) {
	if t.world() == nil {
		return // no world wired (defensive; SetWorld always sets it before feeding asyncIn)
	}
	if r.res.Err != nil {
		t.world().MarkEmpty(r.res.Pos) // un-strand the Loading holder so the tick retries
		return
	}
	t.world().Insert(r.res.Pos, r.res.Chunk)
	// Phase-27 N=2 ROUTING: a chunk's per-region containers + fluid kicks + structure spawns must land
	// in the region that OWNS this column, NOT blindly globalRegion. chunkReady.applyTo runs on the
	// COORDINATOR (quiescent post-barrier), where cur() would otherwise fall back to region 0 and strand
	// every container/fluid/spawn for a region-1 chunk in region 0's stores. Wrap the per-region writes
	// in the owning region so ensureChunkBlockTicks (cur().blockTicks), postProcessChunkFluids
	// (cur().fluidSchedule), and drainStructureSpawns (cur().entities) resolve to r.res.Pos's region.
	t.withRegion(t.regionForColumn(r.res.Pos), func() {
		// Register an (empty) block-tick container for the chunk so live scheduleTick calls inside
		// it are not dropped (LevelTicks.Schedule routes by chunk; a chunk with no container drops
		// the tick). Vanilla addContainer's every loaded chunk; without this the sugar-cane cascade
		// (and every other scheduled block tick) never fired on generated/streamed chunks.
		t.ensureChunkBlockTicks(r.res.Pos)
		t.postProcessChunkFluids(r.res.Pos, r.res.Chunk)
		// STRUCT-POLISH-02: drain the structure-inhabitant SpawnRequests the worker recorded
		// off-tick onto the entity store — on the owner (TICK-05 / Pitfall 5). The witch/cat/villager
		// then ride GAMEPLAY-01's tracker (AddEntity broadcast) next tick. No-op when Spawns is empty.
		t.drainStructureSpawns(r.res)
	})
}

// tracker is the entity/chunk tracking executor seam (TICK-05). Phase 3 shipped it with a
// synchronous no-op stub; Plan 06-02 FILLS it with the real synchronous entityTracker
// (tracker.go) assigned in NewTickLoop. The interface and the t.tracker.Tick() call site in
// tick_phases.go are deliberately UNCHANGED so Phase 8 (OPT-02) can swap the EXECUTOR
// off-tick behind this exact one-method seam without touching the pipeline. Any executor —
// the synchronous entityTracker now, an async one later, or a test double — satisfies it.
type tracker interface{ Tick() }

// TickLoop is the single-owner authoritative game loop. Exactly one goroutine —
// the one running Run — owns and mutates every field below. The only legal crossing
// of the boundary is an immutable channel message (the Phase-2 chan Intent inbound;
// the asyncIn results channel in Phase 8). This single-owner discipline (TICK-05)
// makes the loop -race clean by construction.
type TickLoop struct {
	clock Clock // injectable time source; the loop driver never calls time.Now() directly

	// Accumulator state (TICK-02). last is the clock reading at the previous wake;
	// acc is unconsumed wall-clock time waiting to be turned into logical steps.
	// Owned by the tick goroutine (set up by start(), advanced by advance()/Run()).
	last time.Time
	acc  time.Duration

	gametime int64 // pure tick counter: ++ exactly once per consumed 50ms step (TICK-02)

	// weather is the WORLD-GLOBAL rain/thunder cycle state (weather.go — ServerLevel.advanceWeatherCycle
	// + WeatherData). Weather is a single world-global phase, so it lives ONCE here on the coordinator
	// (not per-region): tickWeather advances it on the coordinator before the region fan-out, and the
	// RNG draws use globalRegion.levelRandom (the ServerLevel this.random analogue). Zero-value = a
	// fresh clear world (all timers 0, flags false, levels 0.0) — exactly WeatherData()'s default ctor.
	weather weatherState

	// worldSeed is the overworld seed (WORLD-04), for /seed. Set at boot via SetWorldSeed; 0 if unset.
	worldSeed int64

	// perms is the LuckPerms-style permission store (permissions.go) the command gate consults
	// (t.playerHasPermission). nil = the legacy all-players-operator fallback (tests + a server
	// booted without SetPermStore), so existing behavior is preserved until a store is wired.
	// Read on the tick goroutine (HasPermission); mutated on-tick (op/deop) + saved off-tick.
	perms *permStore

	// MSPT ring buffer (TICK-06). Written only by the tick goroutine inside recordMSPT.
	ring   [msptRingSize]time.Duration
	ringN  int // total ticks recorded (so we know how much of the ring is valid)
	ringIx int // next write index

	// stats is the published read-only telemetry snapshot. Sole writer = tick
	// goroutine; observers read via Stats(). atomic.Pointer keeps it off the
	// critical path and -race clean.
	stats atomic.Pointer[TickStats]

	// regions holds the per-region tick owners (Phase-27 STEP-1 extraction). At N=1 there is
	// exactly ONE region (globalRegion) that holds everything the pre-extraction TickLoop held —
	// the WORLD-half of the tick state (entities, world+worker, the chunkReady bridge, the
	// scheduled block/fluid ticks, levelRandom, the spawn-scan gate). It is constructed in
	// NewTickLoop. The coordinator reaches the single region via region()/only(); Plans 02/03
	// add the coordinator fan-out/barrier and N=2. See region.go for the per-region/global
	// boundary contract. These fields moved OFF TickLoop onto region: asyncIn, world, worker,
	// asyncBridge, blockTicks, blockTickSubCounter, fluidSchedule, levelRandom, spawnScanPending.
	regions []*region

	// currentRegion is the Phase-27 STEP-3 (N=2) per-goroutine current-region registry: when a
	// region's fan-out goroutine is running its tick, it registers itself here keyed by its goroutine
	// id (region.tick does this on entry, clears on exit). only() consults it so the ~200 existing
	// t.only() call sites in the per-region phases (physics/AI/spawner/handles) resolve to the OWNING
	// region's store WITHOUT being rewritten — the lowest-churn way to scope the single-owner store
	// per region across the conc fan-out. A goroutine NOT inside a region tick (the coordinator, a
	// test calling tickPhysics directly) finds no entry and only() falls back to regions[globalRegion]
	// (so N=1-style direct calls + the existing tests keep working unchanged). It is an xsync.Map
	// because the fan-out goroutines write distinct keys concurrently and only() reads it on those
	// same goroutines — a genuine concurrent map access (a plain map would race the parallel fan-out).
	currentRegion *xsync.Map[int64, *region]

	// spawnLiveCreatureSnapshot is the GLOBAL live-CREATURE count snapshotted on the coordinator at the
	// QUIESCENT point right before the region fan-out, for naturalSpawn's pre-submit cap gate (Phase-27
	// N=2). naturalSpawn runs INSIDE the parallel fan-out, where ranging another region's store
	// (countByCategoryAcrossRegions) would RACE that region's concurrent tickAI/physics mutations. The
	// cap, however, spans ALL players/regions — a per-region countByCategory() under-counts and lets
	// each region submit a scan even when the GLOBAL cap is met. So the coordinator computes the
	// cross-region count once while every region is quiescent (no race) and stashes it here; naturalSpawn
	// reads this immutable snapshot during the fan-out. It is exactly a "snapshot" gate (the comment at
	// the gate already says the count is a snapshot the apply-time re-check re-validates), now a GLOBAL
	// snapshot instead of a per-region one. The authoritative anti-flood remains the quiescent
	// apply-time countByCategoryAcrossRegions re-check (spawnCandidatesReady.applyTo). Written only on
	// the coordinator before the fan-out; read-only during the fan-out (TICK-05, race-clean).
	spawnLiveCreatureSnapshot int

	// spawnLiveMonsterSnapshot is the GLOBAL live-MONSTER count snapshotted on the coordinator at the
	// SAME quiescent pre-fan-out point as spawnLiveCreatureSnapshot (Phase 35-02). The naturalSpawn
	// MONSTER pass runs inside the parallel fan-out too, so it reads THIS race-free cross-region count
	// for its pre-submit cap gate instead of ranging another region's live store. Same discipline as the
	// CREATURE snapshot: written only on the coordinator before the fan-out, read-only during it
	// (TICK-05), and the authoritative anti-flood remains the quiescent apply-time
	// countByCategoryAcrossRegions()[categoryMonster] re-check.
	spawnLiveMonsterSnapshot int

	// strictRegion arms the per-region access guard in cur(): when true, a cur() call from a
	// goroutine with NO region registered PANICS instead of silently falling back to globalRegion.
	// It is the catch for the systemic regionization bug class (Phase-27 N=2): coordinator-phase and
	// DISPATCH-handler code that touches PER-REGION state (entities/fluidSchedule/blockTicks/
	// levelRandom) without first wrapping in withRegion would, under the old fallback, silently SKIP
	// regions 1..N-1 by resolving to region 0. With strictRegion on, that mistake fails loudly in
	// tests instead of corrupting gameplay in only region 0. It defaults to false so production +
	// the existing N=1 suite keep today's tolerant fallback; tests opt in to prove the contract.
	// SHARED accessors world()/worker() NEVER consult it (region-0 read is correct for shared state).
	strictRegion bool

	// asyncIn2 is the Phase-8 compute-pool rejoin channel (OPT-04/OPT-06), the SECOND result
	// channel alongside asyncIn. It is a BOUNDED buffered channel (asyncIn2Buffer): the per-
	// subsystem ants pools below send their immutable asyncResult on it from off-tick workers,
	// and applyAsyncResults drains it NON-BLOCKINGLY on the OWNER each tick (take what's queued,
	// never park the tick — threat T-8-03 / mirrors asyncBridge's bounded buffer). Kept SEPARATE
	// from asyncIn so the Phase-4 chunkReady wiring (SetWorld/asyncBridge, which tests depend on)
	// is untouched — this is purely additive. Always constructed in NewTickLoop (non-nil in
	// production and tests); OPT-01/02/03 fill the pool-submit sites that feed it.
	asyncIn2 chan asyncResult

	// pathPool, trackerPool, spawnPool are the per-subsystem bounded, non-blocking ants pools
	// (08-RESEARCH Pattern 1 — ONE pool per async subsystem). They are constructed in NewTickLoop
	// (cheap and idle until a subsystem submits) and released by Close() on shutdown. A worker
	// runs a PURE computation over an immutable snapshot copied on the owner and rejoins by
	// sending an asyncResult on asyncIn2 — it NEVER touches tick-owned state (TICK-05). pathPool
	// is CPU-sized (pathfinding is the heavy, frequent compute); trackerPool/spawnPool are small
	// (their submits are sparse). They are idle no-ops in this Wave-0 plan; OPT-01 (08-02) submits
	// to pathPool, OPT-02 (08-04) to trackerPool, OPT-03 (08-05) to spawnPool.
	pathPool    *ants.Pool
	trackerPool *ants.Pool
	spawnPool   *ants.Pool

	// pluginPool is the OFF-TICK PYTHON LANE pool (PLUGIN-06, Plan 26-02): a python
	// plugin's hook is submitted here (submitPythonHook → submitOrDrop), runs
	// GIL-held on a LockOSThread-pinned worker (plugin/python.CallHook, behind
	// //go:build python), and rejoins via pythonHookReady on asyncIn2. It is plain
	// ants — NO cgo (the gopy call is one host.PythonPlugin interface hop away,
	// inside CallHook), so this field + its construction stay in the default build.
	// Sized SMALL (asyncSmallPoolSize): the single process-global GIL serializes
	// every python call (subinterp_python.go — gopy@3.14 has no sub-interpreters),
	// so a large pool would only pile work; small + drop-on-overload bounds it
	// (T-26-07 — a saturated pool DROPS, the tick never blocks).
	pluginPool *ants.Pool

	// pythonHooksApplied / pythonHookErrors are the off-tick python lane telemetry,
	// bumped ONLY on the owner inside pythonHookReady.applyTo (TICK-05 — the worker
	// never touches them). They count successful vs errored python hook round-trips
	// (the Phase-26 owner-side effect is bookkeeping only, not world mutation).
	// Tick-owned plain int64s; read via PythonHookStats for tests/telemetry.
	pythonHooksApplied int64
	pythonHookErrors   int64

	// tracker is the (synchronous stub) tracking executor; Phase 8 swaps it.
	tracker tracker

	// players is the per-player collection owned by the tick goroutine. A plain
	// slice is correct and faster than a concurrent map under single ownership —
	// do NOT use xsync here (that is Phase 8). Empty until players join (Wave 2/3).
	players []*tickPlayer

	// clientIndex maps a connection handle to its tick-owned player so dispatch can
	// route an inbound packet to the right per-player subtick buffer in O(1). It is a
	// plain map owned by the tick goroutine (single-owner; not xsync). Nil until
	// players join; dispatch treats a missing client as a cheap no-op (T-3-02).
	clientIndex map[*Client]*tickPlayer

	// register / unregister are the player join/leave seams (TICK-05). AcceptPlayer
	// runs on the network-accept goroutine and MUST NOT mutate players/clientIndex
	// directly — it sends the new tickPlayer on register (join) and the *Client on
	// unregister (leave). The tick goroutine drains both non-blockingly in
	// drainRegistrations and performs the actual map/slice mutation on-thread, so the
	// per-player collection is only ever touched by its owner. Buffered so a join/leave
	// never parks the accept goroutine on a busy tick (T-3-03).
	register   chan *tickPlayer
	unregister chan *Client

	// consoleCmd is the OPERATOR-CONSOLE command seam (TUI-01 / Plan 19-02). The TUI runs on
	// its own goroutine; it MUST NOT execute command handlers off-thread (handlers mutate
	// authoritative game state — TICK-05 / Pitfall 7). EnqueueConsoleCommand sends a typed line
	// here (non-blocking, drop-on-full); the tick goroutine drains it in drainRegistrations and
	// runs runConsoleCommand ON-THREAD, so a console command crosses to the tick exactly like a
	// register/unregister message. Buffered (registerBuffer) so a busy tick never parks the TUI
	// goroutine on Enqueue. nil-safe: a nil channel makes Enqueue's select hit default (drop).
	consoleCmd chan string

	// leaveSnapshots carries the immutable per-player save snapshot OUT of the tick on leave
	// (ENT-06 / TICK-05 / T-6-15). removePlayer runs on the OWNER goroutine; before it drops a
	// leaving player it takes an immutable snapshotPlayer (a value copy — no live tick-owned
	// pointer crosses) and sends it here. An off-tick consumer (main's save loop, wired via
	// LeaveSnapshots) drains the channel and does the disk IO, so the snapshot is taken on the
	// owner and the IO runs OFF the tick — the Phase-4 chunk-result discipline. nil until
	// SetSaveSink wires it (tests that never leave a player leave it nil); a nil channel makes
	// the leave-snapshot send a cheap skipped no-op. Buffered so a leave never parks the tick.
	leaveSnapshots chan playerLeaveSnapshot

	// debug holds the OPTIONAL, off-by-default debug triggers for the Plan 06-07 interactive
	// human-verify gate (a visible moving pig + periodic damage so the operator can SEE entity
	// movement and the health/death/respawn loop). nil in production AND in every test, so the
	// fixed tick-phase pipeline and the -race gate are unaffected; armed only by SetDebug when
	// cmd/sulfur is started with SULFUR_DEBUG=1. Read/written only on the tick goroutine.
	debug *debugConfig

	// idAlloc is the monotonic entity-ID allocator (ENT-01). It REPLACES the hard-coded
	// joinEntityID=1: every player AND every entity draws a unique, never-reused id from
	// this single space (06-RESEARCH Pitfall 7 / threat T-6-07). Although it is a tick-owned
	// field, its only state is an atomic counter, so AcceptPlayer claims a player's id
	// OFF-tick from the accept goroutine without racing the tick — exactly like
	// gameTick.teleportSeq (T-6-08). The tick goroutine claims entity ids from the same
	// allocator when it spawns entities (Plans 06-02+).
	idAlloc *EntityIDAllocator

	// applyInputHook is a test-only observability seam: when non-nil, applyInput
	// invokes it with each resolved input so a test can assert chronological apply
	// order without depending on Phase-6 physics. In production it stays nil and costs
	// one nil-check per input.
	applyInputHook func(*tickPlayer, SubtickInput)

	// phaseTrace, when non-nil, records the name of each phase as it runs. It is a
	// test-only observability hook (set by tests via traceTo) used to assert the
	// fixed phase order; in production it stays nil and costs nothing.
	phaseTrace *[]string

	// onGoalCall is a TEST-ONLY observability seam (Phase-27 STEP-3): when non-nil, starlarkGoal.call
	// invokes it with the mob whose declared-goal callback is about to fire, ON the region's goroutine
	// (so a test can capture the goroutine id + re-resolve the mob's owning region — THE GATE proof
	// that a hook for an entity in region R runs on R's goroutine and resolves R's store). In
	// production it stays nil and costs one nil-check per goal callback — the same zero-cost discipline
	// as applyInputHook/phaseTrace.
	onGoalCall func(e *Entity)

	// spawnSurfaceY is the world spawn column's top-solid block world-Y (the superflat
	// generator's SurfaceY — the same value gameTick threads into the join bootstrap). It
	// is set once before Run via SetSpawn and read only on the tick goroutine by
	// performRespawn (ENT-05) to place a respawning player two blocks above the surface,
	// matching the join placement. Tick-owned; written once at setup, never during a tick.
	spawnSurfaceY int

	// spawnPoint is the SAFE fresh-spawn world position (the air cell the player's feet occupy,
	// ON a standable floor) the ported vanilla PlayerSpawnFinder found over the FULLY-DECORATED
	// spawn chunk (17-06). performRespawn re-teleports a respawning player HERE (the same column
	// the join bootstrap uses for a fresh spawn) instead of the blind (8.5, spawnSurfaceY+2, 8.5)
	// center column — so a respawn never lands the player embedded in a tree/structure. Set once
	// before Run via SetSpawnPoint; read only on the tick goroutine. hasSpawnPoint guards the
	// fallback for setups that never wired it (e.g. NewTickLoop-only unit tests).
	spawnPoint    SpawnPoint
	hasSpawnPoint bool

	// respawnTeleportSeq is the tick-owned producer of fresh teleport ids for in-game
	// re-teleports (ENT-05 respawn). It is the on-tick analogue of gameTick.teleportSeq
	// (which serves the off-tick join): performRespawn allocates a fresh id from it via
	// nextTeleportID and re-arms the player's confirm gate, mirroring the bootstrap. It is
	// seeded high (respawnTeleportBase) so a respawn id can never collide a join id issued
	// by gameTick.teleportSeq for the same player. Touched ONLY on the tick goroutine
	// (TICK-05), so no atomic is needed.
	respawnTeleportSeq int

	// openChests is the runtime store of loot-bearing chest containers keyed by world position
	// (STRUCT-POLISH-01 chest-open UI, the 20-02 W2 follow-up). A chest's {LootTable,
	// LootTableSeed} is recorded into the chunk's BlockEntity list at gen; on the FIRST open the
	// chest-open path (chest_open.go) decodes that BE into a chestLoot here, rolls the loot lazily
	// (unpackLootTable, one-shot), and keeps the rolled 27-slot container so a re-open serves the
	// SAME contents (no re-roll) and item moves persist across opens. Lazily constructed; tick-owned
	// (resolved/mutated only on the tick goroutine — TICK-05 — since the open/click/close path runs
	// on-tick). A future plan flushes this back to the chunk BE NBT on unload/save.
	openChests map[pk.Position]*chestLoot

	// furnaces is the runtime store of furnace/blast_furnace/smoker BLOCK-ENTITIES keyed by world position
	// (GAMEPLAY-05 furnace, the openChests twin). A furnace's per-tick drive (furnace_be.go
	// furnaceServerTick) reads/writes its furnaceBE here every tick (tickWorld); the menu (furnace_menu.go)
	// resolves the SAME furnaceBE on open so clicks + the cook drive share one state. Lazily constructed;
	// tick-owned (TICK-05 — resolved/mutated only on the tick goroutine). Like openChests, the BE NBT
	// save/load seam (AbstractFurnaceBlockEntity.loadAdditional/saveAdditional) is a persistence follow-up;
	// the live in-memory drive is fully faithful.
	furnaces map[pk.Position]*furnaceBE

	// brewingStands is the runtime store of brewing-stand BLOCK-ENTITIES keyed by world position (the
	// furnaces twin). A brewing stand's per-tick brew drive (brewing_stand_be.go brewingStandServerTick)
	// reads/writes its brewingStandBE here every tick (tickWorld); the menu (brewing_stand_menu.go) resolves
	// the SAME brewingStandBE on open so clicks + the brew drive share one state. Lazily constructed;
	// tick-owned (TICK-05). Persistence (Items + BrewTime + Fuel) round-trips via brewing_stand_persist.go.
	brewingStands map[pk.Position]*brewingStandBE

	// dispensers is the runtime store of dispenser/dropper BLOCK-ENTITIES keyed by world position (REDSTONE
	// TIER-4, the furnaces twin). A dispenser fires from its 9-slot dispenserBE here when its scheduled
	// TRIGGERED tick runs (dispenser.go dispenseFrom); the menu (dispenser_menu.go) resolves the SAME
	// dispenserBE on open so clicks + the dispense drive share one state. Lazily constructed; tick-owned
	// (TICK-05 — resolved/mutated only on the tick goroutine). Persistence (Items) round-trips via
	// dispenser_persist.go (the furnace-BE twin).
	dispensers map[pk.Position]*dispenserBE

	// hoppers is the runtime store of HOPPER block-entities keyed by world position (the furnaces twin).
	// A hopper's per-tick transfer drive (hopper_be.go hopperPushItemsTick) reads/writes its hopperBE here
	// every tick (tickWorld) — pulling one item from the container/loose-item above and pushing one item
	// into the container in FACING, gated by the 8-tick cooldown + the redstone ENABLED property. The menu
	// (hopper_menu.go) resolves the SAME hopperBE on open so clicks + the transfer drive share one state.
	// Lazily constructed; tick-owned (TICK-05 — resolved/mutated only on the tick goroutine). Persistence
	// (Items + TransferCooldown) round-trips via hopper_persist.go (the dispenser-BE twin).
	hoppers map[pk.Position]*hopperBE

	// beacons is the runtime store of BEACON block-entities keyed by world position (the furnaces twin,
	// BEACON-01). A beacon's per-tick drive (beacon_be.go beaconServerTick) reads/writes its beaconBE here
	// every tick (tickWorld) — the incremental beam-column scan + the every-80-tick pyramid-level recompute +
	// the in-range player effect application. The menu (beacon_menu.go) resolves the SAME beaconBE on open so
	// the SetBeacon effect selection + payment share one state. Lazily constructed; tick-owned (TICK-05).
	beacons map[pk.Position]*beaconBE

	// conduits is the runtime store of CONDUIT block-entities keyed by world position (the beacons twin,
	// CONDUIT-01). A conduit's per-tick drive (conduit_be.go conduitServerTick) reads/writes its conduitBE
	// here every tick (tickWorld) — the every-40-tick activation-frame re-scan (updateShape) + the in-range
	// player CONDUIT_POWER application + the full-frame hostile attack. Unlike the beacon, a conduit has no
	// menu; it registers on placement (createBlockEntityOnPlace) and ticks passively. Lazily constructed;
	// tick-owned (TICK-05).
	conduits map[pk.Position]*conduitBE

	// chunkSaver is the off-tick chunk-persistence consumer (SUB-PERSIST). It is nil until
	// SetChunkSaver wires it (tests/ephemeral runs leave it nil → no chunk saves). The tick's save
	// phase (tickChunkSave) drains the manager's dirty set, SERIALIZES each dirty/unloaded chunk ON
	// the owner goroutine (folding any rolled openChests for that column into the chunk's BE Items
	// NBT), and Enqueue's the IMMUTABLE bytes here; chunkSaver.RunChunkSaveLoop (its own goroutine)
	// does the region IO. Only finished bytes cross the seam — no live tick-owned pointer — so the
	// save IO is race-free by construction (the leaveSnapshots discipline applied to chunks).
	chunkSaver *world.ChunkSaver

	// chunkSaveTickCounter counts ticks toward the next periodic chunk-save pass (SUB-PERSIST). The
	// save phase flushes dirty chunks every chunkSaveIntervalTicks rather than every tick, so a
	// rapid edit stream coalesces into one save per interval (a chunk dirtied 20× in a second is
	// serialized once). Tick-owned (incremented only on the owner goroutine).
	chunkSaveTickCounter int

	// persistDir is the world directory the raid + POI SavedData persist under (raids -> data/raids.dat,
	// POI -> poi/r.x.z.mca). "" disables raid/POI persistence (tests/ephemeral runs). Set once by
	// SetPersistDir before Run; read only on the tick goroutine (the periodic save pass). See
	// raid_persist.go / poi_persist.go for the on-disk shapes.
	persistDir string

	// savedDataTickCounter counts ticks toward the next periodic raid/POI save pass, mirroring
	// chunkSaveTickCounter (independent so a raid/POI flush never blocks on the chunk pass).
	savedDataTickCounter int

	// plugins is the loaded plugin host + typed event bus (PLUGIN-02 / Plan 22). It is nil until
	// SetPlugins wires it (a server with no plugins dir leaves it nil — every seam emit is a cheap
	// skipped no-op behind an `if t.plugins != nil` guard). The discrete gameplay seams
	// (destroyBlock / handleUseItemOn / the join+leave seams / structure spawn / combat die +
	// actuallyHurt / tickOnce) call t.plugins.Emit at the OCCURRENCE point — NEVER from the
	// per-entity tickEntities/tickAI/tickPhysics loops (the forbidden O(entities×ticks) anti-seam).
	// The pointer is READ only on the tick goroutine (Emit) and WRITTEN only on the tick goroutine
	// (SetPlugins before Run, and the pluginSwap drain in drainRegistrations), so dispatch never
	// reads a half-updated map (TICK-05).
	plugins *host.Manager

	// mobRegistry is the boot-loaded declared-mob registry (PLUGIN-04 / Plan 24-02): the captured
	// "vanilla_pig" declaration the SWAP spawns. WRITTEN once at boot (SetMobRegistry, before Run) and
	// READ at spawn on the tick goroutine (spawnVanillaPig) — tick-owned, lock-free (TICK-05). nil in
	// a tick built without the boot-load (most unit tests); the swap sites that need it install one.
	mobRegistry *mobRegistry

	// pluginSwap is the hot-reload swap channel (FULL hot-reload, Plan 22-02). The off-tick fsnotify
	// watcher (plugin/host/reload.go) REBUILDS a fresh *host.Manager off-tick and sends the pointer
	// here; drainRegistrations drains it on the OWNER goroutine and swaps t.plugins on-thread — the
	// SAME select-with-default discipline as register/unregister/consoleCmd. The watcher NEVER
	// mutates t.plugins or any live hook map, so a reload concurrent with dispatch is a pointer swap
	// between two ticks, never a mid-Emit map mutation (TICK-05 / T-22-05). Buffered 1 with
	// replace-latest semantics (a swap is idempotent-latest); nil-safe — a nil channel makes the
	// select case never fire, so a server with no watcher is unaffected.
	pluginSwap chan *host.Manager
}

// respawnTeleportBase seeds the tick-owned respawn teleport-id counter well above any join id
// gameTick.teleportSeq is likely to issue, so a respawn's fresh teleport id never collides the
// outstanding join id space. nextTeleportID pre-increments, so the first respawn id is
// respawnTeleportBase+1.
const respawnTeleportBase = 1 << 30

// tickPlayer is the per-player game state owned by the tick goroutine. Wave 2 adds
// the subtick input buffer (TICK-03); Wave 3 threads the keep-alive adapter and
// later phases add position/inventory. Every field is mutated ONLY by the tick
// goroutine — the ownership boundary is the structural -race guarantee (TICK-05).
type tickPlayer struct {
	client *Client // the Phase-2 connection handle; flushOutbound enqueues via client.Send

	// entityID is this player's server-issued entity id, claimed off-tick from the loop's
	// EntityIDAllocator at AcceptPlayer (like awaitingTeleport) and threaded into the
	// bootstrap ClientboundLogin playerId — the replacement for the old hard-coded
	// joinEntityID=1 (06-RESEARCH Pitfall 7). Players and entities share the allocator's id
	// space so no id ever collides. Recorded here so a later plan (entity tracker /
	// the player's own Entity instance) can reference it. Tick-owned once registered.
	entityID int32

	// uuid is the player's network/profile UUID (the login id). It is the key for the
	// player's persisted world/playerdata/<uuid>.dat (ENT-06): removePlayer pairs it with the
	// owner-taken snapshot so the off-tick save path knows which file to write. Set at
	// registration; tick-owned thereafter.
	uuid uuid.UUID

	// lastHurtMob / lastHurtMobTimestamp are the OWNER-SIDE attack bookkeeping
	// net.minecraft.world.entity.LivingEntity.lastHurtMob (the entity this player last ATTACKED) +
	// its gameTime stamp — the attack-side mirror of a mob's lastHurtByMob. A tamed wolf's
	// OwnerHurtTargetGoal reads its OWNER's getLastHurtMob()/getLastHurtMobTimestamp() to retaliate
	// against whatever its owner is fighting (MOB-NEUT-01, Phase 36-01). v1 owners are players, so the
	// owner-side bookkeeping lives here; setLastHurtMob is recorded when a player attacks a mob
	// (handleMobAttack). 0 == no attack yet (the zero value). THIN id (the Folia rule). Tick-owned.
	//	[VERIFIED javap LivingEntity.setLastHurtMob(Entity): lastHurtMob = entity; lastHurtMobTimestamp
	//	 = tickCount. OwnerHurtTargetGoal.canUse: owner.getLastHurtMob(); owner.getLastHurtMobTimestamp().]
	lastHurtMob          int32
	lastHurtMobTimestamp int32

	// gameMode is the player's GameType byte (play_join.go: gameModeSurvival==0). It gates the
	// block-drop path (Plan 17-14 / ServerPlayerGameMode.destroyBlock): a CREATIVE player's
	// break drops NOTHING. v1 hardcodes survival at registration, so the gate always passes
	// today — but the check is present and correct so a future creative toggle drops nothing
	// without any further edit. Tick-owned (set at registration, read on the tick goroutine).
	gameMode int32

	// name is the player's login-profile name (the username from AcceptPlayer). It is the
	// SERVER-authoritative chat attribution: handleChat renders "<name> message" from it
	// (CMD-02), never trusting any client-supplied sender field (T-7-07). The name is
	// accepted at AcceptPlayer (gameplay_tick.go) and threaded onto the tickPlayer at
	// registration — exactly as entityID/uuid are claimed off-tick and recorded here. It is
	// a value (crosses no tick-owned state across the register boundary). Tick-owned
	// thereafter.
	name string

	// properties is the authenticated GameProfile properties (the `textures` skin from the
	// online-mode hasJoined response). Set at registration from the value AcceptPlayer threads
	// in (exactly like name/uuid/entityID), so it crosses no tick-owned state across the register
	// boundary. It is empty in offline-mode (no skin -> Steve/Alex, byte-identical to before).
	// Read by broadcastPlayerInfoAdd / sendExistingPlayersTo when building each ADD_PLAYER
	// tab-list entry, and by the self-add bootstrap so the joiner sees its OWN skin (ONLINE-01).
	// SERVER-authoritative (the hasJoined response, never a client-supplied field — T-18-05).
	// Tick-owned once registered.
	properties []user.Property

	// subtick is this player's bounded µs-timestamped input buffer (TICK-03). dispatch
	// appends server-stamped inputs on arrival; resolveSubtickInputs drains it in
	// chronological order each tick. Owned by the tick goroutine — never touched off it.
	subtick subtickBuffer

	// lastInputAt is the arrival stamp of the most recent input resolved through the
	// applyInput stub — the Phase-3 observable that an input was "resolved". Phase 6
	// replaces the stub with real movement/collision state.
	lastInputAt time.Time

	// sawTickEnd records that a ServerboundClientTickEnd boundary marker has been seen
	// for this player (new in 1.21.2 / present in 776). Phase 3 only records the
	// boundary; reading its payload is deferred to Phase 6.
	sawTickEnd bool

	// confirmedTeleport records that the client echoed back the bootstrap
	// PlayerPosition's teleport id via ServerboundAcceptTeleportation (Play bootstrap,
	// forward slice of PLAY-03). The minimal Phase-4 milestone only RECORDS the confirm
	// (the client renders regardless once Login lands); Phase 5 gates movement
	// acceptance on the matching confirm: applyInput drops movement while this is false,
	// and dispatch sets it true ONLY when the echoed id matches awaitingTeleport (T-5-01).
	confirmedTeleport bool

	// awaitingTeleport is the outstanding bootstrap teleport id the confirm gate must
	// match (PLAY-02 / T-5-01). dispatch's AcceptTeleportation case sets confirmedTeleport
	// true ONLY when the client's echoed VarInt == awaitingTeleport, so a forged/wrong id
	// leaves the gate closed. Plan 05-02 sets it from the incrementing bootstrap id; this
	// plan adds the field and the gate that reads it. Tick-owned (set/read only on the
	// tick goroutine).
	awaitingTeleport int

	// loaded records that the client sent ServerboundPlayerLoaded (its "world loaded"
	// signal, 1.21.4+). Routed as a no-op for v1 — streaming is NOT gated on it (Open
	// Question 2). Tick-owned.
	loaded bool

	// --- Player sleep state (SLEEP-01). The 1:1 port of the LivingEntity/Player sleep fields the bed
	// interaction + per-tick sleep advance drive, and the read seam the cat comfort goals gate on
	// (owner.isSleeping() / owner.getSleepTimer()). ALL tick-owned (set by useBed + tickPlayerSleep,
	// read by the cat goals -- all on the tick goroutine, TICK-05). ---

	// sleepingPos is net.minecraft.world.entity.LivingEntity's SLEEPING_POS_ID (Optional<BlockPos>):
	// nil == awake, non-nil == the bed the player is sleeping in. isSleeping() == (sleepingPos != nil)
	// (LivingEntity.isSleeping == getSleepingPos().isPresent()). startSleeping sets it; stopSleeping
	// clears it. THIN copy of the block position (the Folia rule).
	//	[VERIFIED javap LivingEntity: SLEEPING_POS_ID = OPTIONAL_BLOCK_POS accessor; isSleeping() ==
	//	 getSleepingPos().isPresent(); startSleeping/setSleepingPos set it, clearSleepingPos clears it.]
	sleepingPos *pk.Position

	// sleepCounter is net.minecraft.world.entity.player.Player.sleepCounter -- the tick counter that
	// climbs to SLEEP_DURATION (100) while sleeping and unwinds to 0 while waking. getSleepTimer()
	// returns it; the cat morning-gift gate reads it (owner.getSleepTimer() >= 100). Advanced by
	// tickPlayerSleep (Player.tick). 0 for an awake, never-slept player (the zero value).
	//	[VERIFIED javap Player: private int sleepCounter; getSleepTimer() returns it; Player.tick climbs
	//	 it to 100 (bipush 100) while sleeping, unwinds to 0 at 110 (bipush 110) while waking;
	//	 startSleepInBed sets it to 0.]
	sleepCounter int

	// --- Player position (PLAY-04). ALL tick-owned: decoded and updated only by
	// applyInput on the tick goroutine, so the position is -race clean by the same
	// single-owner discipline as the rest of tickPlayer (TICK-05 / T-5-06). The four
	// ServerboundMovePlayer* layouts feed these; the trailing wire field is a packed
	// flags Byte (bit0=onGround, bit1=horizontalCollision), NEVER a Boolean. ---

	// x, y, z are the player's block-space position (Double on the wire). Updated by the
	// Pos and PosRot movement variants; Rot/StatusOnly leave them unchanged.
	x, y, z float64

	// yaw, pitch are the player's look angles in degrees (Float on the wire). Updated by
	// the PosRot and Rot variants; Pos/StatusOnly leave them unchanged.
	yaw, pitch float32

	// onGround is the masked bit0 of the trailing movement flags byte; horizontalCollision
	// (bit1) is decoded but not yet retained (movement physics is Phase 6). Every movement
	// variant updates onGround.
	onGround bool

	// keep is the independent keep-alive component (TICK-04); keepalive is this
	// player's KeepAliveClient adapter. dispatch forwards a returning
	// ServerboundKeepAlive to keep.ClientTick(keepalive) so the keep-alive bookkeeping
	// is driven off the tick yet the timers stay on KeepAlive's OWN goroutine. Both are
	// set by the tick goroutine at registration; nil for a player joined without
	// keep-alive (dispatch guards the nil).
	keep      *KeepAlive
	keepalive KeepAliveClient

	// --- Chunk streaming state (Plan 04-03, WORLD-05). ALL tick-owned: mutated only by
	// the tick goroutine in tickChunks/flushOutbound, so the per-player sent-set is
	// -race clean by the same single-owner discipline as the players slice. ---

	// center is the player's current chunk-column center. Phase 4 defaults it to {0,0}
	// (a chunk square exists around origin); Phase 5 (PLAY-01/03) sets the real spawn
	// center and updates it on movement, re-issuing SetChunkCacheCenter.
	center level.ChunkPos

	// viewDist is the SERVER-CLAMPED view distance in chunks (the DoS control, T-4-01).
	// The needed-ring is (2*viewDist+1)^2 — bounded by the server, never by an untrusted
	// client. Defaulted to serverViewDistance at registration.
	viewDist int

	// sentChunks is the per-player set of columns already streamed, so each chunk is
	// sent to a player at most once. Lazily initialized; tick-owned.
	sentChunks map[level.ChunkPos]bool

	// centerSent records whether SetChunkCacheCenter (+ radius) has been sent for the
	// current center this session, so the flush sends the cache framing once per center.
	centerSent bool

	// --- PlayerChunkSender state (1:1 port of net.minecraft.server.network.PlayerChunkSender) ---
	// Client-acknowledged flow control so the server never floods the connection (sending the whole
	// ring every tick overran the bounded outbound queue → backpressure kick). desiredChunksPerTick
	// starts at 9.0; the client's ServerboundChunkBatchReceived ack adjusts it (0.01..64) and raises
	// maxUnacknowledgedBatches from 1 to 10. batchQuota accumulates fractional budget;
	// unacknowledgedBatches gates sending.
	chunkSenderInit          bool
	desiredChunksPerTick     float32
	batchQuota               float32
	unacknowledgedBatches    int
	maxUnacknowledgedBatches int

	// displayedSkinParts is the client's reported skin-customisation bitmask (the displayed skin
	// LAYERS: bit1 jacket, bit2/3 sleeves, bit4/5 pants, bit6 hat), decoded from
	// ServerboundClientInformation.modelCustomisation. It is propagated to OTHER players via the
	// player entity's DATA_PLAYER_MODE_CUSTOMISATION metadata so they render the second/overlay skin
	// layer (hat etc.). 0 until the client reports it (then the avatar renders the base model).
	displayedSkinParts uint8

	// secs is the dimension's section count (overworld 24), derived at registration for
	// chunk generation/empty sizing — never hard-coded deeper in the pipeline.
	secs int

	// tracked is the per-player set of entity ids currently visible to (and spawned on) this
	// client — the entityTracker's tick-owned bookkeeping (ENT-01). Each tick the tracker
	// diffs the store's near() broad-phase against this set: an id newly in range is spawned
	// (AddEntity) and added here; an id that left range is batched into one RemoveEntities and
	// deleted. A player never tracks itself, so its own entityID is never inserted. Lazily
	// initialized by the tracker; mutated ONLY on the tick goroutine (TICK-05), so it is
	// -race clean by the same single-owner discipline as the rest of tickPlayer.
	tracked map[int32]bool

	// inventory is this player's server-owned, tick-owned component-slot inventory (ENT-04).
	// It is the AUTHORITATIVE source of truth: ContainerClick hashes are decoded-and-discarded,
	// SetCreativeModeSlot writes a slot directly, and the server re-sends ContainerSetContent/
	// SetSlot from here. Lazily initialized by the inventory handlers; mutated ONLY on the tick
	// goroutine (TICK-05 / T-6-08), so it is -race clean by the single-owner discipline.
	inventory *Inventory

	// openContainer is the player's currently-open non-inventory container window, or nil when
	// only the player inventory (window 0) is open (STRUCT-POLISH-01 chest-open UI). It carries the
	// allocated windowId (the containerCounter draw) and the chest world position so a
	// ContainerClick/ContainerClose on that windowId resolves to the right chest (chest_open.go).
	// Tick-owned: set by the chest-open path, cleared by handleContainerClose, both on the tick
	// goroutine (TICK-05). Mirrors ServerPlayer.containerMenu (one open menu at a time).
	openContainer *openContainer

	// containerCounter is the per-player window-id allocator — ServerPlayer.containerCounter. Each
	// open draws nextContainerCounter() = (counter % 100) + 1, so window ids cycle 1..100 and never
	// collide the player inventory (window 0). Tick-owned (advanced only on the tick goroutine).
	containerCounter int

	// lastMainHand is the snapshot of the player's MAINHAND item the last time tickEquipment
	// broadcast it — the Sulfur analogue of LivingEntity.lastEquipmentItems (the per-slot last-
	// sent equipment map). detectEquipmentUpdates compares the current mainhand against this and,
	// on a change, broadcasts ClientboundSetEquipment to trackers + updates this snapshot. Set on
	// the first tickEquipment (gated by equipInit) so the initial empty hand doesn't spuriously
	// broadcast. Tick-owned.
	lastMainHand component.SlotData
	equipInit    bool

	// --- Health / food / death (ENT-05). ALL tick-owned and SERVER-owned: the client has NO
	// health-setting packet (T-6-05) — it only REQUESTS a respawn via ServerboundClientCommand.
	// The server drives damage -> SetHealth -> death (PlayerCombatKill) -> respawn entirely from
	// these fields, mutated ONLY on the tick goroutine (TICK-05). Defaulted to a full survival
	// player (maxHealth / maxFood / defaultSaturation) at registration. ---

	// health is the player's current hit points (Float on the wire, 0..maxHealth). applyDamage
	// lowers it (clamped at 0) and sends SetHealth; performRespawn restores it to maxHealth.
	health float32

	// food is the player's current food level (VarInt on the wire, 0..maxFood). Carried in the
	// SetHealth packet alongside health/saturation; v1 does not yet drain it over time.
	food int32

	// saturation is the player's current food saturation (Float on the wire). Carried in the
	// SetHealth packet; reset with food/health on respawn.
	saturation float32

	// dead records that the player's health reached 0 (the death screen is up). Set by die();
	// cleared by performRespawn. While dead, a respawn request is honored (and a living-player
	// request is ignored). Tick-owned.
	dead bool

	// debugGaveItems marks that the off-by-default debug trigger (SULFUR_DEBUG=1) has already
	// handed this player the placeable test stack, so the give runs once per session. Tick-owned;
	// untouched in production (debug off) and in tests.
	debugGaveItems bool

	// --- Phase 17 gameplay seams (Plan 17-01). ALL tick-owned (TICK-05). ---

	// playerEntity is this player's instance in the tick-owned entityStore (GAMEPLAY-01).
	// Constructed in drainRegistrations with id==entityID and uuid==uuid so the tracker's
	// self-skip works and the joiner's tab-list entry matches; syncPlayerEntities re-syncs
	// its pos/angles from these authoritative fields each tick BEFORE tracker.Tick. nil for
	// a player mid-registration. Set by Plan 17-01 (player_visibility.go).
	playerEntity *Entity

	// headYaw is the player's head rotation (degrees), synced onto playerEntity each tick so
	// the tracker's RotateHead reflects where the player is looking. v1 mirrors yaw (no
	// independent head turn decoded yet). Set by Plan 17-01.
	headYaw float32

	// bootstrapped is the GAMEPLAY-03 first-tick guard: syncJoinInventories sends the
	// authoritative ContainerSetContent exactly once (on the first tick after register) then
	// sets this true so the window is never re-sent on subsequent ticks. Set by Plan 17-01.
	bootstrapped bool

	// fallDistance is the accumulated airborne descent used for fall damage (GAMEPLAY-04 /
	// Plan 17-03): it grows by the per-tick downward delta while airborne and resets on
	// landing, where floor(fallDistance-3) half-hearts of damage are applied. DECLARED here
	// by 17-01 so 17-03 never edits tick.go; USED by 17-03 (fall_damage.go).
	fallDistance float64

	// wasOnGround is the previous tick's onGround state — the landing-edge detector for fall
	// damage (false->true transition triggers the damage check). DECLARED by 17-01, USED by
	// 17-03 (fall_damage.go).
	wasOnGround bool

	// lastY is the player's y at the end of the previous tick, used to compute the per-tick
	// descent delta that accumulates into fallDistance. DECLARED by 17-01, USED by 17-03
	// (fall_damage.go).
	lastY float64

	// --- Melee combat 1:1 port (Plan 17-11). ALL tick-owned (TICK-05): mutated only on the tick
	// goroutine by the attack/damage/tick-decrement paths, so they are -race clean by the same
	// single-owner discipline as the rest of tickPlayer. ---

	// attributes is the per-player attribute holder (attributes.go) — the Go stand-in for
	// vanilla's AttributeMap. Lazily seeded with the player default base values on first access
	// via playerAttributes(); getAttributeValue(attr) reads through it. The melee-combat formulas
	// READ attributes (ATTACK_DAMAGE, ATTACK_SPEED, ATTACK_KNOCKBACK, ARMOR, ARMOR_TOUGHNESS, …)
	// instead of hardcoding numbers, so item/effect modifiers compose here later with no formula
	// change. nil until first read; self-initializing on the tick goroutine.
	attributes *attributeHolder

	// activeEffects is the per-player mob-effect map (mob_effect.go) — the Go stand-in for
	// LivingEntity.activeEffects (effectId -> the active MobEffectInstance). Populated by addEffect (a
	// thrown-potion splash) and ticked down by tickPlayerEffects. nil until the first effect applies.
	activeEffects map[string]*activeEffect

	// raidOmenPosition is net.minecraft.server.level.ServerPlayer.raidOmenPosition: the block pos the
	// player was standing at when their BAD_OMEN converted to RAID_OMEN (BadOmenMobEffect.applyEffectTick
	// -> setRaidOmenPosition). It is where RaidOmenMobEffect.applyEffectTick fires createOrExtendRaid when
	// the raid-omen effect expires. nil when the player has no pending raid omen. Tick-owned.
	raidOmenPosition *pk.Position

	// invulnerableTime is net.minecraft.world.entity.Entity.invulnerableTime: the post-hit damage
	// grace window in ticks. Set to 20 on a fresh hit (hurtServer), and decremented by 1 each tick
	// while > 0 (ServerPlayer.tick — the player path, not the LivingEntity.tick path which skips
	// ServerPlayer). While > 10.0F a subsequent hit only applies the EXCESS over lastHurt (the
	// anti-spam rate limit). Tick-owned.
	invulnerableTime int32

	// lastHurt is net.minecraft.world.entity.LivingEntity.lastHurt: the damage amount of the most
	// recent hit, used by the invulnerableTime>10 window so a second hit within the grace period
	// applies only (damage - lastHurt) and a non-greater hit applies nothing (returns false).
	// Tick-owned.
	lastHurt float32

	// hurtTime / hurtDuration are net.minecraft.world.entity.LivingEntity.hurtTime/hurtDuration:
	// the red-flash animation timer (set to hurtDuration=10 on a fresh hit, counted down each
	// tick). Server-side they gate nothing in v1 (the flash is client visual), but they are ported
	// and ticked for fidelity so the field semantics match vanilla. Tick-owned.
	hurtTime     int32
	hurtDuration int32

	// attackStrengthTicker is net.minecraft.world.entity.player.Player.attackStrengthTicker: ticks
	// since the last attack, incremented by 1 each tick (Player.tick) and reset to 0 on an attack
	// (resetAttackStrengthTicker). getAttackStrengthScale reads it to ramp damage from 0.2x (just
	// attacked) to 1.0x (fully recharged) over getCurrentItemAttackStrengthDelay ticks. Tick-owned.
	attackStrengthTicker int32

	// sprinting is net.minecraft.world.entity.Entity.isSprinting(): whether the player is sprinting,
	// which adds knockback and forbids critical hits in the attack sequence. v1 has no sprint-state
	// decode yet, so it is always false (a faithful stub — the field exists so the attack formula
	// reads it and a future sprint-flag decode wires it with no formula change). Tick-owned.
	sprinting bool

	// --- PASSENGER / VEHICLE ride state (net.minecraft.world.entity.Entity ride subsystem) -------
	// The player-as-passenger side of the ride subsystem (passenger.go). vehicleID is the THIN id of
	// the entity this player is riding (Entity.vehicle; 0 == not riding — the Folia rule, never a live
	// *Entity). boardingCooldown mirrors Entity.boardingCooldown (set to 60 on dismount, gates
	// re-mount via canRide, decremented each tick). lastInput{Forward,Backward,Left,Right,Jump} mirror
	// the ServerboundPlayerInput flags (net.minecraft.world.entity.player.Input) the client sends; the
	// controlling-passenger steer (HappyGhast.getRiddenInput) reads them AS the controller's xxa/zza/
	// isJumping. All tick-owned (TICK-05); 0/false for a player that is not riding — the pig oracle uses
	// no player ride state so its stream is unperturbed.
	vehicleID         int32
	boardingCooldown  int32
	lastInputForward  bool
	lastInputBackward bool
	lastInputLeft     bool
	lastInputRight    bool
	lastInputJump     bool

	// swimming is the player's swim pose state (Entity.updateSwimming). While swimming the
	// hitbox is horizontal (0.6 tall) and the eyes sit at 0.4 above the feet — NOT the standing
	// 1.62 — so the breath/drowning submersion check must use the swim eye height or the player
	// wrongly regains air while submerged in the top water layer. Tick-owned.
	swimming bool

	// absorptionAmount is net.minecraft.world.entity.LivingEntity.getAbsorptionAmount(): the
	// current absorption (golden-apple "yellow heart") shield, folded into actuallyHurt before the
	// health subtraction. v1 has no absorption source (MAX_ABSORPTION base 0), so it stays 0; the
	// field exists so the absorption formula in actuallyHurt reads/writes it faithfully and a future
	// effect slots in with no formula change. Tick-owned.
	absorptionAmount float32

	// --- Experience (WR-06, the XP-orb pickup path). ALL tick-owned (TICK-05): mutated only on the
	// tick goroutine by the orb pickup path (playerTouchOrb -> giveExperiencePoints), so they are
	// -race clean by the same single-owner discipline as the rest of tickPlayer. They mirror the
	// identically-named net.minecraft.world.entity.player.Player fields. ---

	// takeXpDelay is net.minecraft.world.entity.player.Player.takeXpDelay: the per-player cooldown
	// (in ticks) between absorbing XP orbs. ExperienceOrb.playerTouch refuses to collect while it is
	// > 0 and sets it to 2 on a successful pickup; the player tick decrements it toward 0 (mirroring
	// the item pickupDelay pattern). Tick-owned.
	takeXpDelay int32

	// experienceLevel / experienceProgress / totalExperience are Player.experienceLevel /
	// experienceProgress / totalExperience — the XP bar state giveExperiencePoints updates and the
	// ClientboundSetExperience packet carries. experienceProgress is the 0..1 fraction toward the next
	// level; experienceLevel is the green number; totalExperience is the lifetime total. All default to
	// 0 (a fresh player). Tick-owned.
	experienceLevel    int32
	experienceProgress float32
	totalExperience    int32

	// lastSentExperience is the experienceProgress/level/total triple last pushed to this player's own
	// client via ClientboundSetExperience. giveExperiencePoints sends the packet on a change; a separate
	// per-tick resend is not modeled (v1 sends on the XP-gain event, the only mutation site). Seeded at
	// registration so the first send fires only on a real change. Tick-owned.
	lastSentXpLevel    int32
	lastSentXpProgress float32
	lastSentXpTotal    int32
	xpInit             bool

	// --- Breath / drowning 1:1 port (Plan 17-13). Tick-owned (TICK-05): mutated only on the tick
	// goroutine by tickBreath, so it is -race clean by the same single-owner discipline as the rest
	// of tickPlayer. ---

	// airSupply is net.minecraft.world.entity.Entity's DATA_AIR_SUPPLY_ID value (the bubble bar).
	// Vanilla seeds it to getMaxAirSupply()==300 (Entity ctor `define(DATA_AIR_SUPPLY_ID,
	// getMaxAirSupply())`) and LivingEntity.baseTick decrements it while the eyes are submerged in
	// water (decreaseAirSupply: air-1 for a bare player), refilling toward 300 (increaseAirSupply:
	// min(air+4,300)) when not. When it reaches shouldTakeDrowningDamage()'s threshold (<= -20) it
	// resets to 0 and the player takes 2.0 DROWN damage. Seeded to maxAirSupply at registration so a
	// fresh player spawns with a full bubble bar. Tick-owned.
	airSupply int32

	// lastAirSent is the airSupply value last pushed to this player's own client via
	// ClientboundSetEntityData (GAMEPLAY-17 / Plan 17-18). DATA_AIR_SUPPLY_ID is a SYNCHED entity
	// field — the bubble bar reads it off the wire, the client does NOT locally simulate air in
	// multiplayer — so tickBreath must push the value when it changes. This mirrors vanilla's
	// SynchedEntityData dirty-tracking: only a CHANGED field is broadcast, never a per-tick resend.
	// Seeded to maxAirSupply at registration to match the client's registered DATA_AIR_SUPPLY_ID
	// default (Entity ctor define(DATA_AIR_SUPPLY_ID, getMaxAirSupply()==300)) so the first send
	// fires only on a real change (the first underwater decrement), not redundantly on spawn.
	// Tick-owned.
	lastAirSent int32

	// --- Food / hunger / exhaustion 1:1 port (Plan 17-19, net.minecraft.world.food.FoodData).
	// Tick-owned (TICK-05): mutated only on the tick goroutine by tickFood, so it is -race clean by
	// the same single-owner discipline as the rest of tickPlayer. The existing food/saturation
	// fields above hold FoodData.foodLevel/saturationLevel; these add the rest of FoodData's state
	// plus the movement-delta + dirty-send bookkeeping. ---

	// exhaustion is net.minecraft.world.food.FoodData.exhaustionLevel (default 0.0f). Actions
	// (movement, attacks, taking damage) add to it via addExhaustion (capped at 40.0f); FoodData.tick
	// drains it 4.0 at a time, converting each drain into 1.0 saturation loss (or 1 food loss when
	// saturation is empty). Seeded to 0 at registration (the vanilla FoodData ctor default).
	exhaustion float32

	// foodTickTimer is net.minecraft.world.food.FoodData.tickTimer (default 0): the shared counter
	// that gates the fast-regen (>=10), slow-regen (>=80), and starvation (>=80) branches of
	// FoodData.tick. Reset to 0 whenever a gated branch fires or none of the regen/starve conditions
	// hold. Seeded to 0 at registration.
	foodTickTimer int32

	// prevX/prevY/prevZ are the player's position at the END of the previous tick — the v1 stand-in
	// for vanilla's xo/yo/zo (the per-tick movement delta source ServerPlayer.checkMovementStatistics
	// reads as getX()-xo etc.). tickFood computes (x-prevX, y-prevY, z-prevZ) at the TOP of the tick
	// for the movement-exhaustion port, then writes the current x/y/z back here at the END, exactly
	// like vanilla's xo=getX() at the tail of the tick. Tick-owned.
	prevX, prevY, prevZ float64

	// lastFoodSent / lastFoodSaturationZero are the dirty-send tracker for ClientboundSetHealth,
	// matching vanilla ServerPlayer.doTick EXACTLY: the re-send condition is
	// `health != lastSentHealth || foodLevel != lastSentFood || (saturation==0) != lastFoodSaturationZero`.
	// CRITICAL: saturation is compared ONLY via its is-ZERO boolean, NOT the exact float — saturation
	// drains fractionally every tick while moving, so comparing the raw float marked the player dirty
	// EVERY tick and spammed SetHealth ~20×/s (a multi-hundred-MB bandwidth flood). Vanilla never sends
	// on a sub-zero-crossing saturation change. Seeded at registration so the first send fires only on a
	// real change. Tick-owned.
	lastFoodSent           int32
	lastFoodSaturationZero bool

	// lastHealthSent is the health value last carried to this player's own client via
	// ClientboundSetHealth. tickFood folds health into the dirty-send because the regen path
	// (heal()) changes health WITHOUT routing through applyDamage's own SetHealth — the food
	// packet is the carrier that keeps the HUD hearts in sync with natural regeneration. Seeded to
	// the spawn health at registration so the first send fires only on a real change. Tick-owned.
	lastHealthSent float32

	// --- Server-authoritative block-break dig-time 1:1 port (Plan 17-21,
	// net.minecraft.server.level.ServerPlayerGameMode). ALL tick-owned (TICK-05): the dig state is
	// mutated only on the tick goroutine by handleBlockBreakAction (the START/STOP/ABORT dispatch)
	// and tickBlockBreak (the per-tick crack-overlay + delayed-destroy step), so they are -race clean
	// by the same single-owner discipline as the rest of tickPlayer. Each field is the Go mirror of
	// the identically-named ServerPlayerGameMode field; the vanilla per-instance gameTicks counter is
	// supplied by the loop's gametime (a per-tick counter), not duplicated per player. ---

	// isDestroyingBlock is ServerPlayerGameMode.isDestroyingBlock: set true on START_DESTROY_BLOCK
	// once a multi-tick dig begins (not instant-mine, not creative), and read by tickBlockBreak to
	// keep refreshing the crack overlay. Cleared on completion, abort, or when the target turns to air.
	isDestroyingBlock bool

	// destroyPos is ServerPlayerGameMode.destroyPos: the block currently being dug (set on START to
	// the immutable target). STOP only completes when its pos equals this; ABORT clears the overlay
	// here. Defaults to {0,0,0} (vanilla seeds destroyPos to BlockPos.ZERO).
	destroyPos pk.Position

	// destroyProgressStart is ServerPlayerGameMode.destroyProgressStart: the gameTicks value captured
	// when START arrived. incrementDestroyProgress / STOP compute elapsed = gameTicks - this to scale
	// the per-tick getDestroyProgress into accumulated progress.
	destroyProgressStart int32

	// lastSentDestroyStage is ServerPlayerGameMode.lastSentState: the crack-overlay stage (0-9) last
	// sent for the current dig, so destroyBlockProgress is sent only when the stage CHANGES (the
	// vanilla dirty-send). Seeded to -1 at registration (the vanilla ctor `lastSentState = -1`).
	lastSentDestroyStage int32

	// hasDelayedDestroy is ServerPlayerGameMode.hasDelayedDestroy: set when a STOP arrives with
	// progress < 0.7 (the block is not yet done) so tickBlockBreak finishes the break once the
	// accumulated progress reaches 1.0. Cleared when the delayed block breaks or turns to air.
	hasDelayedDestroy bool

	// delayedDestroyPos is ServerPlayerGameMode.delayedDestroyPos: the block the delayed-destroy is
	// finishing. Defaults to {0,0,0} (vanilla seeds delayedDestroyPos to BlockPos.ZERO).
	delayedDestroyPos pk.Position

	// delayedTickStart is ServerPlayerGameMode.delayedTickStart: the destroyProgressStart carried into
	// the delayed-destroy so incrementDestroyProgress keeps scaling from the original dig start.
	delayedTickStart int32

	// --- Item-use / EATING state (Plan 17-22, the LivingEntity.useItem / useItemRemaining /
	// usedItemHand 1:1 port). ALL tick-owned (TICK-05): set on the tick goroutine by the
	// ServerboundUseItem handler (startUsingItem) and advanced by tickUseItem (the per-tick
	// updatingUsingItem → updateUsingItem → completeUsingItem step), so they are -race clean by the
	// same single-owner discipline as the dig/food/breath state above. ---

	// useItem is net.minecraft.world.entity.LivingEntity.useItem: the ItemStack currently being used
	// (eaten). Empty (Count <= 0) == not using anything (the vanilla ItemStack.EMPTY sentinel). Seeded
	// to the empty stack (zero value) at registration — a fresh player is not using an item.
	useItem component.SlotData

	// useItemRemaining is net.minecraft.world.entity.LivingEntity.useItemRemaining: the ticks left in
	// the current use. Seeded from stack.getUseDuration (Consumable.consumeTicks == consumeSeconds*20,
	// f2i) on startUsingItem; decremented each tick by updateUsingItem; completeUsingItem fires when it
	// hits 0. Meaningful only while useItem is non-empty.
	useItemRemaining int32

	// useItemHand is net.minecraft.world.entity.LivingEntity.getUsedItemHand (the InteractionHand the
	// use began on): 0 == MAIN_HAND, 1 == OFF_HAND. completeUsingItem writes the shrunk stack back to
	// this hand. Meaningful only while useItem is non-empty.
	useItemHand int32
}

// Health constants for a fresh survival player (the ENT-05 defaults). maxHealth is the vanilla
// 20 HP (10 hearts); maxFood is the full 20-point hunger bar; defaultSaturation is the spawn
// saturation. They seed tickPlayer.health/food/saturation at registration and the values
// performRespawn restores on respawn.
const (
	maxHealth         float32 = 20
	maxFood           int32   = 20
	defaultSaturation float32 = 5
)

// NewTickLoop constructs a TickLoop over the given injectable clock with a
// synchronous no-op tracker and a nil async channel (so applyAsyncResults is a
// no-op). asyncIn and a real tracker are wired in Phase 8 with no pipeline change.
// registerBuffer bounds the join/leave channels. Player joins/leaves are rare
// relative to the 5ms wake, so a small buffer is ample; it exists only so a burst of
// connects/disconnects never parks an accept goroutine waiting on a busy tick (T-3-03).
const registerBuffer = 64

func NewTickLoop(clock Clock) *TickLoop {
	t := &TickLoop{
		clock:      clock,
		register:   make(chan *tickPlayer, registerBuffer),
		unregister: make(chan *Client, registerBuffer),
		consoleCmd: make(chan string, registerBuffer), // TUI-01: operator-console line seam (Plan 19-02)
		idAlloc:    &EntityIDAllocator{},              // ENT-01: monotonic id allocator (first AllocID()==1)
		// The per-region entityStore + levelRandom now live on the single region (Phase-27 STEP-1),
		// constructed below after t exists so newRegion can back-ref the coordinator.
		// asyncIn stays nil (no-op Phase-4 seam until SetWorld); ring is zero-valued; gametime 0.

		// Phase-8 async substrate (OPT-04): the SECOND rejoin channel + the per-subsystem
		// non-blocking ants pools, constructed here (cheap and idle) so applyAsyncResults drains
		// asyncIn2 from day one and OPT-01/02/03 inherit live pools. asyncIn2 is bounded so a
		// burst of worker results never blocks a worker's send (T-8-03); the pools are bounded +
		// non-blocking so a saturated Submit drops rather than stalls the tick (T-8-02 / Pitfall
		// 4). Released by Close() on shutdown. pathPool is CPU-sized (the heavy, frequent path
		// compute); trackerPool/spawnPool are small (sparse submits).
		asyncIn2:    make(chan asyncResult, asyncIn2Buffer),
		pathPool:    newAsyncPool(runtime.NumCPU()),
		trackerPool: newAsyncPool(asyncSmallPoolSize),
		spawnPool:   newAsyncPool(asyncSmallPoolSize),
		// pluginPool: the off-tick python lane (Plan 26-02). Small + non-blocking —
		// the single GIL serializes python, so a saturated pool DROPS (T-26-07).
		// Idle no-op until a runtime="python" plugin is wired (WirePython, tagged).
		pluginPool: newAsyncPool(asyncSmallPoolSize),
		// pluginSwap is the hot-reload swap channel (Plan 22-02). Buffered 1 with replace-latest
		// semantics: the off-tick watcher sends a freshly-rebuilt *host.Manager here and the owner
		// drains+swaps it in drainRegistrations. Constructed always (cheap) so the watcher can feed it
		// from day one; a server with no watcher simply never sends, and t.plugins stays whatever
		// SetPlugins set (possibly nil).
		pluginSwap: make(chan *host.Manager, 1),
	}
	// Phase-27 STEP-3 (the N=2 flip): construct regionCount (==2) regions that statically split the
	// world (regionOf — region_transfer.go). Each region holds its OWN entity store + (never-shared)
	// levelRandom; the world/worker/blockTicks/fluidSchedule stay nil until SetWorld (the same lazy
	// wiring as before). globalRegion (id 0) remains the world-streaming/scheduled-tick home (the
	// world-global phases run on the coordinator over regions[globalRegion]'s world — see
	// region_coordinator.go); the entity phases fan out per region. The region back-refs t so it
	// reaches the global idAlloc/players/plugins.
	t.regions = make([]*region, regionCount)
	for id := 0; id < regionCount; id++ {
		t.regions[id] = newRegion(regionID(id), t)
	}
	// The per-goroutine current-region registry (N=2): the fan-out goroutines register themselves so
	// only() routes the per-region phases' t.only() calls to the OWNING region's store. Empty until a
	// region.tick runs; a non-fan-out goroutine (coordinator / direct test calls) finds no entry and
	// only() falls back to regions[globalRegion].
	t.currentRegion = xsync.NewMap[int64, *region]()
	// OPT-02 (08-04) SWAP-POINT — the single line that swaps the tracker EXECUTOR off-tick behind
	// the UNCHANGED tracker.Tick() seam. ENT-01 filled this with the synchronous &entityTracker{};
	// Phase 8 replaces it with &asyncTracker{}, whose Tick() builds a per-player snapshot ON the
	// owner, submits the visibility-diff MATH to trackerPool (off-tick), and rejoins via
	// trackerDiffReady on asyncIn2 — applyAsyncResults (the UNCHANGED seam) emits the packets +
	// updates p.tracked owner-side. The interface (tracker{ Tick() }), the t.tracker.Tick() call
	// site at tick_phases.go, and the pipeline order are ALL unchanged (TestTickPhaseOrder passes).
	// The synchronous entityTracker stays in tracker.go as the golden reference the async tracker
	// is diffed against (TestAsyncTrackerMatchesSync). The async executor holds a back-reference to
	// the loop so Tick() can read the tick-owned store (loop.only().entities.near) + players on the owner.
	t.tracker = &asyncTracker{loop: t}
	return t
}

// region returns the per-region tick owner for id (Phase-27 STEP-1). At N=1 the only valid id is
// globalRegion. Plans 02/03 add the coordinator/barrier and more regions; for now this is the
// accessor every coordinator call site uses to reach the world-half store.
func (t *TickLoop) region(id regionID) *region { return t.regions[id] }

// resolveRegion is the shared goroutine→region lookup behind cur()/only(): it returns the region the
// CURRENTLY-EXECUTING goroutine registered (region.tick / withRegion), and whether one was found. A
// goroutine NOT inside a region tick (the coordinator running the global/post phases, a dispatch
// handler, a test calling a phase directly) has no entry — (regions[globalRegion], false).
func (t *TickLoop) resolveRegion() (*region, bool) {
	if t.currentRegion != nil {
		if r, ok := t.currentRegion.Load(curGoroutineID()); ok {
			return r, true
		}
	}
	return t.regions[globalRegion], false
}

// world returns the SHARED tick-owned ChunkManager. The world (ChunkManager + worker) is wired
// IDENTICALLY into EVERY region by SetWorld, so reading it off globalRegion is correct regardless of
// which goroutine calls — it NEVER depends on the per-goroutine current region. world() therefore
// reads globalRegion directly and NEVER consults strictRegion: a coordinator-phase or dispatch read
// of the shared world is legitimate and must never panic. This is the SHARED half of the old only()
// (~78 call sites: only().world). Concurrent READS during the parallel entity phase are safe (the
// world is mutated only on the coordinator, never inside the fan-out).
func (t *TickLoop) world() *world.ChunkManager { return t.regions[globalRegion].world }

// worker returns the SHARED off-tick chunk load/generate worker — the other half of the shared world
// pair SetWorld wires into every region. Like world() it reads globalRegion directly and never
// consults strictRegion (shared state; region-0 read is correct on any goroutine).
func (t *TickLoop) worker() *world.Worker { return t.regions[globalRegion].worker }

// cur returns the region whose PER-REGION store the CURRENTLY-EXECUTING goroutine should read/write
// (Phase-27 STEP-3, the N=2 routing). When a region's fan-out goroutine is inside its tick it has
// registered itself in currentRegion (region.tick), so cur() returns THAT region — the per-region
// phase call sites (physics/AI/spawner/handles' cur().entities / cur().fluidSchedule /
// cur().blockTicks / cur().levelRandom) resolve against the OWNING region's store WITHOUT being
// rewritten.
//
// THE GUARD (the systemic-bug catch): a goroutine with NO region registered — the coordinator
// running a per-region phase it forgot to wrap, a dispatch handler mutating per-region state, a test
// calling a per-region phase directly — is the bug class this whole fix targets. When strictRegion is
// armed, cur() PANICS there (the access would otherwise silently land in region 0 and SKIP regions
// 1..N-1). When strictRegion is off (production + the existing N=1 suite), it falls back to
// regions[globalRegion] — today's tolerant behavior — so behavior is unchanged at N=1.
func (t *TickLoop) cur() *region {
	r, ok := t.resolveRegion()
	if !ok && t.strictRegion {
		panic("per-region access with no region registered — wrap in withRegion or run inside the fan-out")
	}
	return r
}

// only is the NON-STRICT region resolver kept for the bare *region passers (e.g.
// `submitRegion := t.only()`) and the existing direct-store test call sites (loop.only().entities…).
// It NEVER panics: it always falls back to regions[globalRegion] off the fan-out, preserving the
// pre-fix contract (region_test.go: only() == region(globalRegion) on a non-fan-out goroutine) and
// every legacy test that touches a store directly. Per-region PRODUCTION code uses cur() (strict);
// only() is the deliberate tolerant alias for the cases where region-0 fallback is the intended
// semantics (a captured submit region, a test driving a single region by hand).
func (t *TickLoop) only() *region {
	r, _ := t.resolveRegion()
	return r
}

// asyncIn2Buffer bounds the Phase-8 compute-pool rejoin channel (asyncIn2). It mirrors
// asyncBridgeBuffer: generously above the per-tick async-result burst (the pools are themselves
// bounded), so an off-tick worker's send never parks; if it ever filled, the bound caps memory
// growth (threat T-8-03) rather than letting the result channel grow without limit.
const asyncIn2Buffer = 256

// asyncSmallPoolSize is the worker cap for the sparse-submit subsystems (tracker, spawner): a
// small pool is ample because their submits are infrequent relative to pathfinding's per-mob
// cadence (08-RESEARCH Pitfall 4 — size each pool to its subsystem's steady state). pathPool is
// CPU-sized separately (runtime.NumCPU()).
const asyncSmallPoolSize = 2

// Close releases the Phase-8 async pools (OPT-04), returning their workers to the runtime. It is
// idempotent and nil-safe — a TickLoop constructed by NewTickLoop always has the pools, but a
// double Close or a partially-constructed loop never panics — so main() and tests can tear down
// cleanly on shutdown. It does NOT close asyncIn2: the channel is left to be garbage-collected
// with the loop, avoiding a send-on-closed race if a worker is still in flight when Close runs
// (the bounded buffer absorbs any final sends; the drained-or-not results are simply discarded).
func (t *TickLoop) Close() {
	if t.pathPool != nil {
		t.pathPool.Release()
	}
	if t.trackerPool != nil {
		t.trackerPool.Release()
	}
	if t.spawnPool != nil {
		t.spawnPool.Release()
	}
	if t.pluginPool != nil {
		t.pluginPool.Release()
	}
}

// PythonHookStats reports the off-tick python lane telemetry (Plan 26-02):
// successful applies + errored hooks, both bumped owner-side in
// pythonHookReady.applyTo. Tick-owned — call on the tick goroutine (or after the
// loop stops). Exposed for tests (the off-tick round-trip assertion) + operator
// telemetry.
func (t *TickLoop) PythonHookStats() (applied, errors int64) {
	return t.pythonHooksApplied, t.pythonHookErrors
}

// asyncBridgeBuffer bounds the internal worker-results -> asyncResult bridge channel.
// It is generously above the per-tick chunk-result burst for the small clamped view
// ring (the worker itself is bounded), so the adapter never parks; if it ever filled,
// the worker's send would backpressure rather than grow memory.
const asyncBridgeBuffer = 256

// SetWorld wires the off-tick chunk subsystem into the tick (WORLD-01). It stores the
// tick-owned manager and the worker, then starts a small adapter goroutine that ranges
// the worker's immutable Results() and forwards each as a chunkReady onto the internal
// asyncBridge — which it assigns to asyncIn so applyAsyncResults (UNCHANGED Phase-3
// seam) now drains chunk results on the owner goroutine. MUST be called before Run so
// asyncIn is non-nil. The adapter touches NO tick state (only re-wraps the immutable
// result), so it introduces no data race; the manager is mutated solely by the tick.
func (t *TickLoop) SetWorld(mgr *world.ChunkManager, worker *world.Worker) {
	// Phase-27 STEP-3 (N=2): the world (ChunkManager + worker) is SHARED across every region — wire
	// the same pointers into ALL regions so a region's t.only().world (read on its fan-out goroutine
	// during the parallel entity phase) reads the SAME blocks the others do. The world is a single
	// global store here (not yet per-region-sharded chunks — a locked deferral), so concurrent READS
	// during the parallel phase are safe (the world is MUTATED only on the coordinator: tickWorld's
	// scheduled blocks/fluids, tickChunks' streaming, chunkReady.applyTo, and the dispatch-driven
	// block edits — none run inside the fan-out). The chunkReady bridge stays on globalRegion (the
	// coordinator drains it in the post-phase), so only globalRegion gets the asyncBridge/asyncIn.
	bridge := make(chan asyncResult, asyncBridgeBuffer)
	for _, r := range t.regions {
		r.world = mgr
		r.worker = worker
	}
	t.regions[globalRegion].asyncBridge = bridge
	t.regions[globalRegion].asyncIn = bridge
	go func() {
		// Adapter: immutable world.ChunkResult -> chunkReady (asyncResult). Ranges until
		// the worker's results channel closes (it stays open for the worker's lifetime);
		// it never reads or writes tick-owned state.
		for res := range worker.Results() {
			bridge <- chunkReady{res: res}
		}
	}()
}

// Stats returns the latest published telemetry snapshot, readable off the tick
// goroutine without locking (TICK-06). Returns nil before the first tick records
// stats.
func (t *TickLoop) Stats() *TickStats { return t.stats.Load() }

// GameTime returns the current game-time counter. Intended for the tick goroutine
// and tests; off-thread observers should read Stats().GameTime.
func (t *TickLoop) GameTime() int64 { return t.gametime }

// SetSpawn records the world spawn column's surface world-Y for in-game re-teleports (ENT-05
// respawn). main() calls it before Run with the same SurfaceY it hands the Superflat generator
// and the join bootstrap, so a respawning player lands two blocks above the surface exactly
// like a joining one. Set-once at setup; read only on the tick goroutine (TICK-05).
func (t *TickLoop) SetSpawn(surfaceY int) { t.spawnSurfaceY = surfaceY }

// SetSpawnPoint records the SAFE fresh-spawn world position (the ported PlayerSpawnFinder
// column over the FULLY-DECORATED spawn chunk — 17-06) so an in-game respawn re-teleports the
// player to the SAME safe column the join bootstrap uses, instead of the blind center column.
// NewGameTick forwards the value cmd/sulfur computed. Set-once at setup; read only on the tick
// goroutine (TICK-05). performRespawn prefers it when set; otherwise it falls back to the
// (8.5, spawnSurfaceY+2, 8.5) center column (the pre-17-06 behavior, kept for tests that wire
// only SetSpawn).
func (t *TickLoop) SetSpawnPoint(p SpawnPoint) { t.spawnPoint, t.hasSpawnPoint = p, true }

// nextTeleportID claims a fresh, never-zero, monotonically increasing teleport id for an
// in-game re-teleport (ENT-05 respawn). It is the on-tick producer mirroring
// gameTick.nextTeleportID (the off-tick join producer): performRespawn allocates an id here,
// stores it in tickPlayer.awaitingTeleport, and re-arms the confirm gate so movement is gated
// until the client echoes the new id (PLAY-02 / T-5-01). Seeded at respawnTeleportBase so a
// respawn id never collides a join id. Tick-owned (called only on the owner goroutine), so no
// atomic is needed.
func (t *TickLoop) nextTeleportID() int {
	if t.respawnTeleportSeq < respawnTeleportBase {
		t.respawnTeleportSeq = respawnTeleportBase
	}
	t.respawnTeleportSeq++
	return t.respawnTeleportSeq
}

// playerLeaveSnapshot pairs a leaving player's UUID with the IMMUTABLE save snapshot taken on
// the owner goroutine (ENT-06 / T-6-15). It is the only player value that crosses the tick
// boundary on leave; the off-tick save consumer writes save/<uuid>.dat from it without ever
// touching live tick-owned state.
type playerLeaveSnapshot struct {
	uuid uuid.UUID
	data save.PlayerData
}

// leaveSnapshotBuffer bounds the leave-snapshot channel. Leaves are rare relative to the tick
// rate; a small buffer ensures removePlayer's owner-side send never parks the tick even if the
// off-tick save consumer is briefly busy with disk IO.
const leaveSnapshotBuffer = 64

// SetSaveSink wires the off-tick persistence consumer (ENT-06). main() calls it before Run; it
// allocates the leaveSnapshots channel so removePlayer emits a snapshot per leave, and returns
// the receive end for the caller's save loop to drain off the tick. Tests that never persist a
// leaving player simply never call it (the nil channel makes the leave-snapshot send a no-op).
func (t *TickLoop) SetSaveSink() <-chan playerLeaveSnapshot {
	t.leaveSnapshots = make(chan playerLeaveSnapshot, leaveSnapshotBuffer)
	return t.leaveSnapshots
}

// SetPlugins wires the loaded plugin host + event bus (PLUGIN-02 / Plan 22). Call it ONCE before
// Run, on the setup goroutine, with the Manager main() built from LoadDir(plugins/). A nil Manager
// (no plugins dir, or plugins disabled) leaves every discrete-seam emit a cheap skipped no-op behind
// the `if t.plugins != nil` guard. After Run starts, the ONLY way to replace the Manager is the
// pluginSwap channel drained on the tick goroutine (hot-reload) — never call SetPlugins concurrently
// with a live tick (TICK-05).
func (t *TickLoop) SetPlugins(m *host.Manager) { t.plugins = m }

// PluginSwapChan returns the send side of the hot-reload swap channel (Plan 22-02). The off-tick
// fsnotify watcher built by main() sends each freshly-rebuilt *host.Manager here; the tick owner
// drains it in drainRegistrations and swaps t.plugins on-thread, so the watcher never touches live
// tick-owned state. The channel is buffered 1 (replace-latest) so a watcher send never parks.
func (t *TickLoop) PluginSwapChan() chan<- *host.Manager { return t.pluginSwap }

// traceTo installs a test-only phase-order recorder. Each phase appends its name to
// *dst as it runs, letting TestTickPhaseOrder assert the fixed pipeline order. Pass
// nil to disable. Not used in production.
func (t *TickLoop) traceTo(dst *[]string) { t.phaseTrace = dst }

// trace records a phase name when the test hook is installed; a no-op otherwise. Phase-27 STEP-3
// (N=2): the per-region phases run on N fan-out goroutines, so to keep the trace a SINGLE coherent
// sequence (the load-bearing TestTickPhaseOrder contract) only the globalRegion goroutine (and the
// coordinator, which sees globalRegion via only()'s fallback) appends — the other regions tick the
// same phases silently. This also keeps the append single-goroutine (no race on phaseTrace) while the
// fan-out runs. The observable phase ORDER is unchanged: region 0 emits exactly the N=1 sequence.
func (t *TickLoop) trace(name string) {
	if t.phaseTrace == nil {
		return
	}
	if t.only().id != globalRegion {
		return // a non-global region's fan-out goroutine: stay silent to keep the trace one sequence
	}
	*t.phaseTrace = append(*t.phaseTrace, name)
}

// start initializes the accumulator baseline. Run calls it with the first clock
// reading; tests call it before driving advance() so frame deltas are well-defined.
func (t *TickLoop) start(now time.Time) {
	t.last = now
	t.acc = 0
}

// advance feeds one wall-clock sample into the accumulator and consumes as many
// whole 50ms steps as are due, running drainInbound + tickOnce per step. It returns
// the number of logical steps consumed this call (0 on a sub-tick wake). This is the
// SINGLE place the accumulator/clamp/step logic lives — Run wraps it under a ticker;
// tests call it directly with fake-clock frames. drainInbound is invoked per step
// here only as a no-op fallback safety; Run drains once per wake before stepping (it
// passes a nil channel here so the per-step drain is skipped during tests).
func (t *TickLoop) advance(now time.Time) int {
	return t.advanceDraining(now, nil)
}

// advanceDraining is advance() with an explicit inbound channel drained once per
// consumed step. Run uses it so inbound is drained on the owner goroutine right
// before each logical tick; advance() (tests) passes nil to skip the drain.
func (t *TickLoop) advanceDraining(now time.Time, inbound <-chan Intent) int {
	frame := now.Sub(t.last)
	t.last = now
	if frame < 0 {
		frame = 0 // a non-monotonic source must never rewind the accumulator
	}
	if frame > maxFrame {
		frame = maxFrame // spiral-of-death clamp: drop backlog, slow not freeze
	}
	t.acc += frame

	steps := 0
	for t.acc >= tickStep {
		// Apply queued player joins/leaves on the owner goroutine BEFORE draining
		// inbound, so a just-joined player's clientIndex entry exists when its first
		// inbound packets are dispatched (TICK-05). Always drained (channels are nil-
		// safe to select on; an empty channel falls straight through the default).
		t.drainRegistrations()
		if inbound != nil {
			t.drainInbound(inbound) // non-blocking drain on the OWNER goroutine
		}
		t.tickOnce() // one logical tick: ordered phases + gametime++ + recordMSPT
		t.acc -= tickStep
		steps++
	}
	return steps
}

// Run is the production tick driver (TICK-01/TICK-02/TICK-05). It owns all game
// state and consumes the Phase-2 chan Intent. A time.Ticker wakes faster than the
// tick (self-correcting, no Sleep drift); each wake samples the injectable clock,
// feeds the accumulator, and runs whole logical steps. Returns when ctx is cancelled.
func (t *TickLoop) Run(ctx context.Context, inbound <-chan Intent) {
	ticker := time.NewTicker(wakeInterval)
	defer ticker.Stop()

	t.start(t.clock.Now())

	for {
		select {
		case <-ctx.Done():
			// SUB-PERSIST (raids/POI): a final dirty-flush on the OWNER goroutine before the tick
			// exits, so a clean shutdown right after a raid/POI mutation is never lost (the managers
			// are tick-owned; flushing here keeps the IO race-free). A "" persistDir is a no-op.
			t.flushSavedData()
			return
		case <-ticker.C:
			// Drain joins/leaves every wake (not only on a full step) so a player that
			// joins between steps is registered promptly and its inbound packets route
			// to a live clientIndex entry. drainRegistrations is non-blocking.
			t.drainRegistrations()
			t.advanceDraining(t.clock.Now(), inbound)
		}
	}
}

// drainInbound consumes every currently-queued Intent and returns immediately when
// the channel is empty (select-default) — it NEVER parks the tick (T-3-02). A closed
// channel (server shutdown) also returns. Dispatch runs on the owner goroutine, so
// no game-state pointer crosses the boundary; only Intent does.
func (t *TickLoop) drainInbound(inbound <-chan Intent) {
	for {
		select {
		case it, ok := <-inbound:
			if !ok {
				return // channel closed: shutting down
			}
			t.dispatch(it.Client, it.Packet) // decode + route on the owner goroutine
		default:
			return // nothing queued: return immediately, never block
		}
	}
}

// drainRegistrations applies every queued player join/leave on the OWNER goroutine,
// then returns immediately when both channels are empty (select-default) — it NEVER
// parks the tick. A join inserts the tickPlayer into the tick-owned players slice and
// clientIndex; a leave removes it. Because the actual mutation happens here (not in
// AcceptPlayer's accept goroutine), the per-player collection is single-owner and the
// boundary stays -race clean (T-3-03 / TICK-05).
func (t *TickLoop) drainRegistrations() {
	for {
		select {
		case p := <-t.register:
			if t.clientIndex == nil {
				t.clientIndex = make(map[*Client]*tickPlayer)
			}
			if _, exists := t.clientIndex[p.client]; !exists {
				t.players = append(t.players, p)
				t.clientIndex[p.client] = p

				// GAMEPLAY-01 (Plan 17-01) join seam — added EXACTLY ONCE here on the owner
				// (never in AcceptPlayer, which crosses the tick boundary). Insert the player's
				// store Entity (id==entityID so the tracker self-skips), then sync the tab list
				// bidirectionally BEFORE the tracker's next AddEntity: broadcast the joiner's
				// ADD_PLAYER entry to every OTHER player and send all existing players' entries to
				// the joiner (a Notchian client drops AddEntity without the tab entry — Pitfall 1).
				p.playerEntity = newPlayerEntity(p)
				// Phase-27 STEP-3 (N=2): the player entity is added to its OWNING region (regionOf its
				// spawn column), not blindly globalRegion, so the cross-region tracker + transfer treat
				// the player like any other region-owned entity. At N=1 this was t.only(); here it routes
				// by position. syncPlayerEntities keeps it in the right region as the player walks (a
				// player crossing a seam transfers like a mob).
				t.regionForEntity(p.playerEntity).entities.add(p.playerEntity)
				// PLUGIN-02 (Plan 22) on_player_join seam: fire ONCE here on the owner, at the
				// discrete join occurrence (the same place GAMEPLAY-01 adds the player entity) —
				// NEVER from a per-tick scan. Nil-guarded so a no-plugin server is unaffected; the
				// payload is plain frozen scalars (name + entity id), no live handles (Phase 23).
				if t.plugins != nil {
					t.plugins.Emit(host.EventPlayerJoin, host.PlayerJoinEvent{
						Name:     p.name,
						EntityID: int(p.entityID),
					})
				}
				t.broadcastPlayerInfoAdd(p)
				t.sendExistingPlayersTo(p)
				// Send the joiner its OWN skin metadata so its client renders its second/overlay
				// layer (the tracker self-skips this player). displayedSkinParts was captured in
				// the CONFIG state (BUG-4) before the join, so it is already set here.
				t.sendSelfSkin(p)
				// WEATHER (weather.go): tell the joiner the CURRENT weather so it doesn't join to a clear
				// sky during a storm. Vanilla's PlayerList.sendLevelInfo sends, when isRaining():
				// START_RAINING(0) -> RAIN_LEVEL_CHANGE(getRainLevel(1)) -> THUNDER_LEVEL_CHANGE(getThunderLevel(1)).
				// Owner-goroutine send over the joiner's own connection (mutates no tick state). CITE:
				// PlayerList.sendLevelInfo.
				if p.client != nil && t.isRaining() {
					p.client.Send(writeGameEventPacket(gameEventStartRaining, 0))
					p.client.Send(writeGameEventPacket(gameEventRainLevelChange, t.getRainLevel(1.0)))
					p.client.Send(writeGameEventPacket(gameEventThunderLevelChange, t.getThunderLevel(1.0)))
				}
			}
		case c := <-t.unregister:
			t.removePlayer(c)
		case line := <-t.consoleCmd:
			// TUI-01 (Plan 19-02): an operator-console line crosses to the tick here and runs
			// on the OWNER goroutine, exactly like register/unregister (TICK-05 / Pitfall 7).
			// The select still falls through to default when consoleCmd is empty, so this case
			// never parks the tick.
			t.runConsoleCommand(line)
		case mgr := <-t.pluginSwap:
			// PLUGIN-02 (Plan 22-02) FULL hot-reload swap: the off-tick fsnotify watcher REBUILT a
			// fresh *host.Manager (New+LoadDir) off-tick and sent the pointer here; the OWNER swaps
			// t.plugins on-thread — exactly like the register/unregister/consoleCmd cases above. This
			// is the ONLY post-Run writer of t.plugins; Emit reads it only on this same goroutine, so
			// dispatch never sees a half-updated map and a reload concurrent with dispatch is a pointer
			// swap between two ticks (TICK-05 / T-22-05). The select still falls through to default
			// when pluginSwap is empty, so this case never parks the tick.
			t.plugins = mgr
		default:
			return // nothing queued: return immediately, never block
		}
	}
}

// removePlayer deletes the tick-owned player for c from clientIndex and the players
// slice. Owner-goroutine only (called from drainRegistrations). A missing client is a
// cheap no-op so a double-leave can never panic.
func (t *TickLoop) removePlayer(c *Client) {
	p, ok := t.clientIndex[c]
	if !ok {
		return
	}

	// RIDE (passenger.go): a leaving player that is riding must dismount so its vehicle's passenger
	// list shrinks (the SetPassengers broadcast to the remaining trackers drops the gone rider) and no
	// stale id lingers on the ghast. Entity.setRemoved runs `this.stopRiding()` for a removed passenger;
	// this is that call for a departing player. A non-riding player (vehicleID == 0) is a no-op.
	if p.vehicleID != 0 {
		t.playerStopRiding(p)
	}

	// ENT-06 save-on-leave (TICK-05 / T-6-15): take the IMMUTABLE snapshot HERE, on the owner
	// goroutine, while the player is still a live tick-owned value, then hand only that value
	// copy to the off-tick save consumer. No live tick-owned pointer crosses the boundary, so
	// the off-tick disk IO is -race clean by construction (the Phase-4 chunk-result discipline).
	// A nil channel (no save sink wired) or a full buffer is a cheap skipped no-op — a leave
	// never parks the tick on persistence.
	if t.leaveSnapshots != nil {
		snap := playerLeaveSnapshot{uuid: p.uuid, data: snapshotPlayer(p)}
		select {
		case t.leaveSnapshots <- snap:
		default: // buffer full: drop this save rather than stall the tick (rare; leaves are sparse)
		}
	}

	delete(t.clientIndex, c)
	for i, pl := range t.players {
		if pl == p {
			// swap-remove: order in the players slice is not significant.
			last := len(t.players) - 1
			t.players[i] = t.players[last]
			t.players[last] = nil
			t.players = t.players[:last]
			break
		}
	}

	// GAMEPLAY-01 (Plan 17-01) leave seam — drop the player's store Entity (so the tracker
	// batches a RemoveEntities for it on the next tick) and broadcast a PlayerInfoRemove for
	// its UUID to every REMAINING player so their clients drop the tab entry + avatar. Done
	// AFTER the swap-remove so the leaving player is not re-sent its own removal. remove() is a
	// no-op for a missing id, so a leave before the join seam ran never panics. Phase-27 STEP-3
	// (N=2): the player entity may live in EITHER region (it transferred as the player walked), so
	// re-resolve its owning region by id and remove there (owningRegion returns nil only if it was
	// never added / already gone — then the removes below are skipped, a safe no-op).
	if owner := t.owningRegion(p.entityID); owner != nil {
		owner.entities.remove(p.entityID)
	}
	t.broadcastPlayerInfoRemove(p.uuid)

	// PLUGIN-02 (Plan 22) on_player_leave seam: fire ONCE per leave here on the owner (the existing
	// save-on-leave seam), at the discrete leave occurrence — NEVER from a per-tick scan. Nil-guarded
	// so a no-plugin server is unaffected; the payload is plain frozen scalars (name + entity id).
	if t.plugins != nil {
		t.plugins.Emit(host.EventPlayerLeave, host.PlayerLeaveEvent{
			Name:     p.name,
			EntityID: int(p.entityID),
		})
	}
}

// dispatch routes one inbound packet on the tick goroutine. It is total and cheap:
// every path is a no-op-or-cheap action, never blocks on IO, never panics on an
// unknown/malformed ID or unknown client (T-3-02). Movement/use/attack packets are
// stamped with the SERVER arrival time and appended to the player's bounded subtick
// buffer (TICK-03 / T-3-07); ServerboundClientTickEnd is recorded as a boundary
// marker only. Keep-alive forwarding to the independent timer lands in 03-03.
func (t *TickLoop) dispatch(c *Client, p pk.Packet) {
	// Resolve the tick-owned player for this connection. A missing/nil client is a
	// cheap no-op — dispatch must never deref an unknown handle (T-3-02).
	player := t.clientIndex[c]

	switch packetid.ServerboundPacketID(p.ID) {
	case packetid.ServerboundMovePlayerPos,
		packetid.ServerboundMovePlayerPosRot,
		packetid.ServerboundMovePlayerRot,
		packetid.ServerboundMovePlayerStatusOnly,
		packetid.ServerboundPlayerInput,
		packetid.ServerboundAttack,
		packetid.ServerboundInteract,
		packetid.ServerboundSwing,
		packetid.ServerboundUseItem,
		packetid.ServerboundUseItemOn,
		// ServerboundPlayerAction (dig: START/STOP/ABORT_DESTROY_BLOCK + ...) drives BREAK.
		// It was NOT in this set before 06-04 — it fell through to the default no-op, so the
		// break action never reached the subtick buffer and was a silent dead feature. Routing
		// it here (server-stamped, appended to the bounded buffer like UseItemOn) is the
		// load-bearing 06-04 fix; applyInput resolves it on-tick into handlePlayerAction.
		packetid.ServerboundPlayerAction,
		// The container/inventory packets (ENT-04, 06-05). They were NOT in this set before —
		// they fell through to the default no-op, so a ContainerClick/creative-set never
		// reached the subtick buffer and the inventory was a silent dead feature (06-07's
		// interactive check does NOT exercise inventory). Routing them here (server-stamped,
		// appended to the bounded buffer like UseItemOn) is the load-bearing 06-05 fix;
		// applyInput resolves them on-tick into the inventory handlers. The server is
		// AUTHORITATIVE: the click's HashedStack hashes are decoded-and-discarded.
		packetid.ServerboundContainerClick,
		packetid.ServerboundSetCreativeModeSlot,
		packetid.ServerboundContainerClose,
		// ServerboundContainerButtonClick (stonecutter recipe pick), ServerboundSelectTrade (merchant trade
		// pick) and ServerboundSetBeacon (beacon effect selection) are menu-mutation packets resolved on-tick
		// by applyInput (subtick.go) — buffered here (server-stamped) like the container clicks so they reach
		// the tick-goroutine handlers in chronological order. A forged/stale window is a no-op in the handler.
		packetid.ServerboundContainerButtonClick,
		packetid.ServerboundSelectTrade,
		packetid.ServerboundSetBeacon,
		// ServerboundMoveVehicle (the controlling-passenger steer): the client sends the vehicle's new
		// absolute position each tick while a player controls it (happy-ghast ride). Routed through the
		// subtick buffer like the player-movement packets so handleMoveVehicle resolves it on-tick in
		// chronological order; a non-riding player's stray MoveVehicle is a no-op inside the handler.
		packetid.ServerboundMoveVehicle,
		packetid.ServerboundSetCarriedItem:
		// A subtick-relevant input: stamp it with the SERVER clock (never a client-
		// supplied timestamp — T-3-07) and append to the bounded per-player buffer.
		// resolveSubtickInputs drains it in chronological order this tick.
		if player != nil {
			player.subtick.append(SubtickInput{At: t.clock.Now(), Packet: p})
		}
	case packetid.ServerboundChunkBatchReceived:
		// The client's per-batch chunk ACK (ServerboundChunkBatchReceivedPacket = one Float
		// desiredChunksPerTick). Handled DIRECTLY here (not via the subtick buffer / teleport gate)
		// because chunk streaming runs BEFORE the teleport confirm — gating the ack would deadlock
		// the PlayerChunkSender flow control (the server holds at maxUnacknowledgedBatches and never
		// streams). 1:1 port of PlayerChunkSender.onChunkBatchReceivedByClient. T-3-07: the rate is
		// clamped (0.01..64), so a malicious value cannot blow up the send budget.
		if player != nil {
			var rate pk.Float
			if err := p.Scan(&rate); err == nil {
				player.onChunkBatchReceivedByClient(float32(rate))
			}
		}
	case packetid.ServerboundClientInformation:
		// The client's settings, re-sent in PLAY on join and whenever the player changes options.
		// 1:1 port of ServerGamePacketListenerImpl.handleClientInformation -> ServerPlayer.
		// updateOptions: the only field we propagate is modelCustomisation (the displayed skin
		// parts), set into DATA_PLAYER_MODE_CUSTOMISATION so OTHER players render the avatar's
		// overlay layers (hat/jacket/sleeves/pants). Handled DIRECTLY here (not the subtick buffer)
		// because it carries no positional/temporal ordering. Decoded on the owner.
		if player != nil {
			t.handleClientInformation(player, p)
		}
	case packetid.ServerboundPlayerCommand:
		// ServerboundPlayerCommandPacket: VarInt entityId, VarInt actionId (enum ordinal), VarInt
		// data. We decode the SPRINT toggle (START_SPRINTING=1, STOP_SPRINTING=2) into p.sprinting —
		// vanilla ServerGamePacketListenerImpl.handlePlayerCommand -> setSprinting. This drives the
		// swim-pose eye height (a swimming player's eyes sit at 0.4, not the standing 1.62), so the
		// breath/drowning check uses the right submersion threshold. Other actions are no-ops in v1.
		if player != nil {
			var entityID, actionID, data pk.VarInt
			if err := p.Scan(&entityID, &actionID, &data); err == nil {
				switch actionID {
				case 1: // START_SPRINTING
					player.sprinting = true
				case 2: // STOP_SPRINTING
					player.sprinting = false
				}
			}
		}
	case packetid.ServerboundClientTickEnd:
		// Client input-batch boundary marker (new in 1.21.2 / present in 776). Phase 3
		// records the boundary only; its payload is NOT decoded (deferred to Phase 6).
		if player != nil {
			player.sawTickEnd = true
		}
	case packetid.ServerboundAcceptTeleportation:
		// Confirm Teleportation: the client echoes the bootstrap PlayerPosition's
		// teleport id (PLAY-02 gate). Phase 5 VALIDATES the id: decode the single VarInt
		// and set confirmedTeleport true ONLY when it matches the outstanding
		// awaitingTeleport (T-5-01). A forged/wrong id leaves the gate closed, so
		// applyInput keeps dropping movement (no spawn rubber-band, T-5-03). A malformed
		// payload (Scan error) is ignored — the gate stays closed; never panics (T-3-02).
		if player != nil {
			var id pk.VarInt
			if err := p.Scan(&id); err == nil && int(id) == player.awaitingTeleport {
				player.confirmedTeleport = true
			}
		}
	case packetid.ServerboundPlayerLoaded:
		// The client's "world loaded" signal (UNIT/empty payload, 1.21.4+). Routed as a
		// no-op for v1: streaming is NOT gated on it (Open Question 2). We only record
		// that it arrived; its (empty) payload is never read. Never panics (T-3-02).
		if player != nil {
			player.loaded = true
		}
	case packetid.ServerboundKeepAlive:
		// Forward a returning keep-alive to the independent KeepAlive component so it
		// can move the player back to the ping list and reset the wait timer (TICK-04).
		// The bookkeeping is DRIVEN off the tick (we route the response here) but the
		// timers live on KeepAlive's OWN goroutine: ClientTick sends on KeepAlive.tick,
		// which KeepAlive.Run selects on — never gated on tick cadence (T-3-05). A
		// player without a wired adapter is a cheap no-op.
		if player != nil && player.keep != nil && player.keepalive != nil {
			player.keep.ClientTick(player.keepalive)
		}
	case packetid.ServerboundClientCommand:
		// The client's Client Command (ENT-05 / T-6-05). It carries a single VarInt action
		// enum: 0 = PERFORM_RESPAWN, 1 = REQUEST_STATS. Health is SERVER-owned — this is the
		// ONLY way a client influences its health, and it can only REQUEST a respawn, never
		// set health or claim "I'm not dead". Resolved on-tick: route PERFORM_RESPAWN to the
		// respawn flow ONLY when the player is actually dead (a forged request for a living
		// player is ignored); REQUEST_STATS is a v1 no-op. Decode defensively — a Scan error
		// or an out-of-range enum is a silent no-op, never a panic (T-6-04 / T-3-02).
		if player != nil {
			var action pk.VarInt
			if err := p.Scan(&action); err == nil {
				if int32(action) == clientCommandPerformRespawn && player.dead {
					t.performRespawn(player)
				}
			}
		}
	case packetid.ServerboundChatCommand:
		// The client's slash command (CMD-01). ServerboundChatCommandPacket is jar-verified
		// (javap'd from the 26.2 inner jar) as a record with a SINGLE `command:String` field
		// (FriendlyByteBuf.readUtf, NO signing/salt/timestamp). Resolve it INLINE on the tick
		// goroutine — like ServerboundClientCommand above — NOT via the subtick buffer: a
		// command is not a timestamped movement input, it mutates authoritative game state and
		// must run on the owner (TICK-05). runChatCommand decodes defensively (a Scan error is a
		// silent no-op, T-3-02), length-bounds the string before Execute (T-7-03), and routes it
		// to the fork Graph.Execute through the permission gate (ASVS V4). A nil player (unknown
		// connection) is a cheap no-op.
		if player != nil {
			t.runChatCommand(player, p)
		}
	case packetid.ServerboundChatCommandSigned:
		// The SIGNED command variant drags a salt + per-argument signatures + a last-seen
		// acknowledgement. For v1 the server runs in offline mode and the unmodified client
		// sends the UNSIGNED ServerboundChatCommand for /commands, so the signed variant is a
		// documented no-op (we do NOT verify or trust client signatures, and decoding the
		// leading string then ignoring the signing buys nothing until the chat-signing path
		// lands). Routed here EXPLICITLY (rather than the default no-op) so the choice is
		// visible and the capture-diff in 07-06 can confirm the client never takes this path.
	case packetid.ServerboundChat:
		// The client's chat message (CMD-02). ServerboundChatPacket is jar-verified (javap'd
		// from the 26.2 inner jar this session) as readUtf(256) message -> readInstant
		// timeStamp -> readLong salt -> readNullable(MessageSignature) -> LastSeenMessages$Update.
		// Resolve it INLINE on the tick goroutine — like ServerboundChatCommand/ClientCommand
		// above — NOT via the subtick buffer: chat is not a timestamped movement input, and the
		// broadcast fan-out touches the tick-owned players slice + the bounded outbound queues,
		// which must run on the owner (TICK-05). handleChat decodes DEFENSIVELY (a Scan error is
		// a silent no-op, T-3-02), keeps ONLY the message string (the signature/lastSeen are
		// ignored — offline/server-authoritative), bounds it at 256, and broadcasts a
		// server-attributed ClientboundSystemChat ("<name> msg") to every player. A nil player
		// (unknown connection) is a cheap no-op.
		if player != nil {
			t.handleChat(player, p)
		}
	case packetid.ServerboundChatAck, packetid.ServerboundChatSessionUpdate:
		// The chat acknowledgement (A6) and the chat-session update (a public-key session the
		// client establishes for SIGNED chat). v1 runs offline + broadcasts via SystemChat (it
		// never sends signed PlayerChat), so neither is needed: they are DECODE-AND-IGNORE
		// no-ops. Routed here EXPLICITLY (rather than the silent default) so the choice is
		// visible and the 07-06 capture-diff can confirm the SystemChat round-trip does not
		// depend on acking. The payload is never read; never panics (T-3-02).
	default:
		// Unknown / not-yet-handled IDs are cheap no-ops: never block, never panic.
	}
}

// recordMSPT writes one tick's duration into the ring and republishes the read-only
// telemetry snapshot (TICK-06). The tick goroutine is the SOLE writer; observers read
// via Stats(). p99/avg/TPS are recomputed cheaply over the valid ring window.
func (t *TickLoop) recordMSPT(d time.Duration) {
	t.ring[t.ringIx] = d
	t.ringIx = (t.ringIx + 1) % msptRingSize
	if t.ringN < msptRingSize {
		t.ringN++
	}

	n := t.ringN
	// Copy the valid window for a cheap p99 sort (n <= 100; allocation is trivial).
	window := make([]time.Duration, n)
	copy(window, t.ring[:n])

	var sum time.Duration
	for _, v := range window {
		sum += v
	}
	avg := sum / time.Duration(n)

	sort.Slice(window, func(i, j int) bool { return window[i] < window[j] })
	p99idx := (n * 99) / 100
	if p99idx >= n {
		p99idx = n - 1
	}
	p99 := window[p99idx]

	// Effective TPS from the rolling average tick cost, capped at the target 20.
	tps := 20.0
	if avg > tickStep {
		tps = float64(time.Second) / float64(avg)
	}

	t.stats.Store(&TickStats{
		MSPTavg:  float64(avg) / float64(time.Millisecond),
		MSPTp99:  float64(p99) / float64(time.Millisecond),
		TPS:      tps,
		GameTime: t.gametime,
		// OPT-04: the tick-goroutine READ of the one justified xsync.Counter. The submit paths
		// (submitOrDrop, off-tick / any goroutine) Inc() asyncSubmitDrops; here the owner reads its
		// Value() to publish it. This concurrent inc-vs-read is exactly why the counter is an
		// xsync.Counter and not a plain int64 (collections_audit.go).
		AsyncDrops: asyncSubmitDrops.Value(),
	})
}
