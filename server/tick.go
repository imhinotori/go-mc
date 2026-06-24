package server

import (
	"context"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/world"

	"github.com/google/uuid"
	"github.com/panjf2000/ants/v2"
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
	if t.world == nil {
		return // no world wired (defensive; SetWorld always sets it before feeding asyncIn)
	}
	if r.res.Err != nil {
		t.world.MarkEmpty(r.res.Pos) // un-strand the Loading holder so the tick retries
		return
	}
	t.world.Insert(r.res.Pos, r.res.Chunk)
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

	// MSPT ring buffer (TICK-06). Written only by the tick goroutine inside recordMSPT.
	ring   [msptRingSize]time.Duration
	ringN  int // total ticks recorded (so we know how much of the ring is valid)
	ringIx int // next write index

	// stats is the published read-only telemetry snapshot. Sole writer = tick
	// goroutine; observers read via Stats(). atomic.Pointer keeps it off the
	// critical path and -race clean.
	stats atomic.Pointer[TickStats]

	// asyncIn is the async-result rejoin channel. It is nil until SetWorld wires it
	// (Phase 3: nil => applyAsyncResults is a genuine no-op — the seam just EXISTS in
	// the right slot). Plan 04-03 sets it to asyncBridge, fed by the world worker.
	asyncIn <-chan asyncResult

	// world is the tick-owned chunk manager and worker is the off-tick load/generate
	// worker (Plan 04-03, WORLD-01/05). Both are nil until SetWorld; the streaming
	// phases (tickChunks/flushOutbound) treat a nil world as a no-op so Phase-3-style
	// tests still run. The manager is a PLAIN map mutated ONLY by the tick goroutine —
	// the worker emits immutable ChunkResults and never touches it (TICK-05 / T-4-05).
	world  *world.ChunkManager
	worker *world.Worker

	// asyncBridge is the internal channel SetWorld assigns to asyncIn. A small adapter
	// goroutine ranges the worker's Results() and forwards each as a chunkReady onto
	// this channel; applyAsyncResults drains it on the owner. The adapter touches NO
	// tick state — it only re-wraps the immutable ChunkResult — so it adds no race.
	asyncBridge chan asyncResult

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

	// spawnScanPending is the OPT-03 (08-05) single-in-flight gate for the async natural-spawn
	// scan: naturalSpawn sets it true when it SUBMITS a candidate scan to spawnPool, and
	// spawnCandidatesReady.applyTo clears it on rejoin (always — even when the scan placed nothing).
	// While true, naturalSpawn submits no new scan, so at most ONE spawn scan is ever in flight (the
	// 08-RESEARCH Pitfall 4 / OPT-01 !pending discipline — never a pile-up of redundant scans). It
	// is a plain bool touched ONLY on the tick goroutine (set in naturalSpawn, cleared in applyTo,
	// both owner-side — TICK-05), so it needs no atomic.
	spawnScanPending bool

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

	// leaveSnapshots carries the immutable per-player save snapshot OUT of the tick on leave
	// (ENT-06 / TICK-05 / T-6-15). removePlayer runs on the OWNER goroutine; before it drops a
	// leaving player it takes an immutable snapshotPlayer (a value copy — no live tick-owned
	// pointer crosses) and sends it here. An off-tick consumer (main's save loop, wired via
	// LeaveSnapshots) drains the channel and does the disk IO, so the snapshot is taken on the
	// owner and the IO runs OFF the tick — the Phase-4 chunk-result discipline. nil until
	// SetSaveSink wires it (tests that never leave a player leave it nil); a nil channel makes
	// the leave-snapshot send a cheap skipped no-op. Buffered so a leave never parks the tick.
	leaveSnapshots chan playerLeaveSnapshot

	// entities is the tick-owned entity store (ENT-01). The by-id map + per-section grid
	// buckets are mutated ONLY by the tick goroutine (TICK-05) — entity spawning (Plan
	// 06-02), movement/re-bucketing (Plan 06-03), and the tracker's near() broad-phase all
	// run on-thread over this store. A plain map (not xsync; that is Phase 8).
	entities *entityStore

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

	// spawnSurfaceY is the world spawn column's top-solid block world-Y (the superflat
	// generator's SurfaceY — the same value gameTick threads into the join bootstrap). It
	// is set once before Run via SetSpawn and read only on the tick goroutine by
	// performRespawn (ENT-05) to place a respawning player two blocks above the surface,
	// matching the join placement. Tick-owned; written once at setup, never during a tick.
	spawnSurfaceY int

	// respawnTeleportSeq is the tick-owned producer of fresh teleport ids for in-game
	// re-teleports (ENT-05 respawn). It is the on-tick analogue of gameTick.teleportSeq
	// (which serves the off-tick join): performRespawn allocates a fresh id from it via
	// nextTeleportID and re-arms the player's confirm gate, mirroring the bootstrap. It is
	// seeded high (respawnTeleportBase) so a respawn id can never collide a join id issued
	// by gameTick.teleportSeq for the same player. Touched ONLY on the tick goroutine
	// (TICK-05), so no atomic is needed.
	respawnTeleportSeq int
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

	// name is the player's login-profile name (the username from AcceptPlayer). It is the
	// SERVER-authoritative chat attribution: handleChat renders "<name> message" from it
	// (CMD-02), never trusting any client-supplied sender field (T-7-07). The name is
	// accepted at AcceptPlayer (gameplay_tick.go) and threaded onto the tickPlayer at
	// registration — exactly as entityID/uuid are claimed off-tick and recorded here. It is
	// a value (crosses no tick-owned state across the register boundary). Tick-owned
	// thereafter.
	name string

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
		entities:   newEntityStore(),     // ENT-01: tick-owned entity store, non-nil from construction
		idAlloc:    &EntityIDAllocator{}, // ENT-01: monotonic id allocator (first AllocID()==1)
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
	}
	// OPT-02 (08-04) SWAP-POINT — the single line that swaps the tracker EXECUTOR off-tick behind
	// the UNCHANGED tracker.Tick() seam. ENT-01 filled this with the synchronous &entityTracker{};
	// Phase 8 replaces it with &asyncTracker{}, whose Tick() builds a per-player snapshot ON the
	// owner, submits the visibility-diff MATH to trackerPool (off-tick), and rejoins via
	// trackerDiffReady on asyncIn2 — applyAsyncResults (the UNCHANGED seam) emits the packets +
	// updates p.tracked owner-side. The interface (tracker{ Tick() }), the t.tracker.Tick() call
	// site at tick_phases.go, and the pipeline order are ALL unchanged (TestTickPhaseOrder passes).
	// The synchronous entityTracker stays in tracker.go as the golden reference the async tracker
	// is diffed against (TestAsyncTrackerMatchesSync). The async executor holds a back-reference to
	// the loop so Tick() can read the tick-owned store (loop.entities.near) + players on the owner.
	t.tracker = &asyncTracker{loop: t}
	return t
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
	t.world = mgr
	t.worker = worker
	bridge := make(chan asyncResult, asyncBridgeBuffer)
	t.asyncBridge = bridge
	t.asyncIn = bridge
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

// traceTo installs a test-only phase-order recorder. Each phase appends its name to
// *dst as it runs, letting TestTickPhaseOrder assert the fixed pipeline order. Pass
// nil to disable. Not used in production.
func (t *TickLoop) traceTo(dst *[]string) { t.phaseTrace = dst }

// trace records a phase name when the test hook is installed; a no-op otherwise.
func (t *TickLoop) trace(name string) {
	if t.phaseTrace != nil {
		*t.phaseTrace = append(*t.phaseTrace, name)
	}
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
			}
		case c := <-t.unregister:
			t.removePlayer(c)
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
		packetid.ServerboundSetCarriedItem:
		// A subtick-relevant input: stamp it with the SERVER clock (never a client-
		// supplied timestamp — T-3-07) and append to the bounded per-player buffer.
		// resolveSubtickInputs drains it in chronological order this tick.
		if player != nil {
			player.subtick.append(SubtickInput{At: t.clock.Now(), Packet: p})
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
