package server

import (
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// region.go is the Phase-27 (Folia regionization) STEP-1 extraction: the `region` struct that
// owns the WORLD half of the tick state. It is the per-region slice of today's TickLoop — the
// "Pattern 1: Region as a slice of the single-owner loop" from 27-RESEARCH (Architecture
// Patterns), extracted so Plans 02 (coordinator/barrier) and 03 (N=2 + cross-region transfer)
// can multiply it by N WITHOUT changing observable behavior.
//
// THIS STEP IS BEHAVIOR-NEUTRAL BY CONSTRUCTION. At N=1 there is exactly ONE region
// (globalRegion), and it holds everything the old single-owner TickLoop held, so the world ticks
// IDENTICALLY to before the extraction (the #1 gate: the FULL existing server + world suite
// passes UNCHANGED, and the Docker -race gate stays green). No parallelism, no conc, no new
// dependency is added here — that is Plan 02. The single region == today's single-owner TickLoop.
//
// THE PER-REGION vs GLOBAL BOUNDARY (the documented contract — 27-RESEARCH "Architectural
// Responsibility Map"). This split is the design of the whole phase; it is stated explicitly here
// so the boundary is the documented contract, not implicit:
//
//	PER-REGION (owned by the region's tick goroutine — TICK-05 multiplied by N):
//	  entities    *entityStore                    — the by-id map + per-column buckets
//	  world       *world.ChunkManager             — the tick-owned chunk manager
//	  worker      *world.Worker                   — the off-tick load/generate worker
//	  asyncIn     <-chan asyncResult              — the chunkReady bridge (per-world)
//	  asyncBridge chan asyncResult                — SetWorld's internal bridge channel
//	  blockTicks  *ticks.LevelTicks[blockTickType]— the scheduled-block-tick manager
//	  blockTickSubCounter int64                   — Level.subTickCount tiebreak
//	  fluidSchedule *fluidScheduleQueue           — the scheduled-fluid-tick queue
//	  levelRandom *levelgen.LegacyRandomSource    — Level.random analogue (NEVER shared across
//	                                                regions — a shared RNG would race AND diverge
//	                                                the stream non-deterministically)
//	  spawnScanPending bool                       — the OPT-03 single-in-flight spawn-scan gate
//
//	GLOBAL (STAY on TickLoop — the coordinator owns these; one shared instance for all regions):
//	  clock/last/acc/gametime  — the TICK-02 accumulator + the ONE shared 50ms gametime anchor
//	  ring/ringN/ringIx/stats  — MSPT telemetry (server-wide)
//	  asyncIn2 + the ants pools (path/tracker/spawn/plugin) — shared bounded async substrate
//	  tracker                  — cross-region at the barrier (Plan 03)
//	  players + clientIndex     — the GLOBAL player list / session / network
//	  register/unregister/consoleCmd/pluginSwap/leaveSnapshots — global registration seams
//	  idAlloc *EntityIDAllocator— ONE global id space (players + entities never collide)
//	  plugins *host.Manager    — the FROZEN shared registry (Phase 21/22)
//	  mobRegistry              — boot-loaded shared declaration
//	  debug                    — global debug seam
//	  spawn*/respawn*/openChests/chunkSaver/... — global setup/runtime state
//
// THE STORE STAYS SINGLE-SOURCED (no duplicate / aliased store — threat T-27-EXT-1). The region
// is the SOLE holder of entities/world/etc; TickLoop reaches them ONLY through the only()/region()
// accessors (tick.go). Every phase loop ranges the SAME store instance the handles/tracker read,
// so there is no second store to drift.

// regionID identifies a region within the coordinator's regions slice. At N=1 there is only
// globalRegion; Plan 03 adds more ids via the static chunk->region hash.
type regionID int

// globalRegion is the id of the single region that exists at N=1 (this step). It holds everything
// the pre-extraction TickLoop held, so the world ticks identically. Plan 03 introduces additional
// region ids alongside it.
const globalRegion regionID = 0

// region owns the WORLD-half of today's TickLoop, scoped (at N>1) to its chunks. Exactly one
// goroutine — the region's tick goroutine (the coordinator's single goroutine at N=1) — mutates
// these fields, so the region is -race clean by the same single-owner discipline as the
// pre-extraction TickLoop (TICK-05, multiplied by N). See the file doc comment for the explicit
// per-region vs global boundary contract (27-RESEARCH Responsibility Map / Pattern 1).
type region struct {
	// id identifies this region within the coordinator's regions slice (globalRegion at N=1).
	id regionID

	// coord is the back-reference to the GLOBAL coordinator (Pattern 1's `coord *TickLoop`), so a
	// region reaches the shared idAlloc, players, plugins, and the ONE gametime anchor that stay
	// global. The region never mutates the coordinator's global state off-pattern; it reads the
	// shared allocator/registry and (at N=1) is driven by the coordinator's single goroutine.
	coord *TickLoop

	// --- PER-REGION world state (moved off TickLoop by this step; see the file doc boundary) ---

	// entities is the per-region tick-owned entity store (ENT-01). The by-id map + per-section grid
	// buckets are mutated ONLY by the region's goroutine. A region owns its own *entityStore so
	// move()/near()/add()/remove() stay single-owner per region (the bucket-consistency contract,
	// entity_store.go, is unchanged). Non-nil from newRegion.
	entities *entityStore

	// world is the per-region tick-owned chunk manager and worker is the off-tick load/generate
	// worker (WORLD-01/05). Both are nil until SetWorld wires them (lazily, exactly as the
	// pre-extraction TickLoop did); the streaming phases treat a nil world as a no-op.
	world  *world.ChunkManager
	worker *world.Worker

	// asyncIn is the async-result rejoin channel for THIS region's chunk worker; asyncBridge is the
	// internal channel SetWorld assigns to asyncIn (a small adapter goroutine forwards the worker's
	// immutable Results() as chunkReady onto it). nil until SetWorld; a nil asyncIn makes the
	// chunkReady drain a genuine no-op.
	asyncIn     <-chan asyncResult
	asyncBridge chan asyncResult

	// blockTicks is the per-region SUB-BLOCKTICK level-wide scheduled-block-tick manager
	// (ServerLevel.blockTicks port); blockTickSubCounter is the Level.subTickCount tiebreak.
	// blockTicks is lazily constructed inside tickScheduledBlocks (a nil manager drains to nothing);
	// blockTickSubCounter is advanced only on the region's goroutine via nextSubTick.
	blockTicks          *ticks.LevelTicks[blockTickType]
	blockTickSubCounter int64

	// redstoneToggles is the per-level RedstoneTorchBlock.RECENT_TOGGLES list (a (pos, gameTime)
	// FIFO). Vanilla stores it in a static WeakHashMap<BlockGetter, List<Toggle>> keyed by level; here
	// it lives on the region (the level) so it is tick-owned (TICK-05) and pruned/appended only on the
	// region goroutine by the torch tick. Bounds the redstone-torch burnout (>=8 toggles per pos within
	// a 60-tick window -> 160-tick cooldown). CITE: RedstoneTorchBlock.RECENT_TOGGLES.
	redstoneToggles []redstoneToggle

	// notesPlayed is the per-tick list of note-block positions that played this tick (NoteBlock
	// .playNote blockEvent, an audible/vibration client effect that is a cited no-op). Test/observability
	// seam: the note-play has no server gameplay, so this records the rising-edge play for assertions.
	// Tick-owned. CITE: NoteBlock.playNote (blockEvent 0,0).
	notesPlayed []pk.Position

	// comparatorOutput is the per-level ComparatorBlockEntity.output store (REDSTONE TIER-2): a map
	// from a comparator's position to the last output-signal value refreshOutputState computed and the
	// tick(...) reads back via getOutputSignal. Vanilla holds this int in the comparator's BlockEntity
	// (ComparatorBlockEntity.output, default 0); Sulfur has no live comparator block-entity, so the
	// value lives here on the region (the level) — tick-owned (TICK-05), read/written only on the region
	// goroutine by the comparator diode logic. A pos absent from the map reads 0 (the BE default). CITE:
	// ComparatorBlockEntity.getOutputSignal/setOutputSignal (private int output = 0).
	comparatorOutput map[pk.Position]int

	// pistonBlockEvents is the per-level ServerLevel.blockEvents queue slice for the PISTON port
	// (redstone tier-3): the ObjectLinkedOpenHashSet<BlockEventData> vanilla enqueues from
	// Level.blockEvent(pos, block, b0, b1) and drains once per tick in ServerLevel.runBlockEvents ->
	// BlockState.triggerEvent. A piston's checkIfExtend posts a b0=0 (extend) / b0=1|2 (retract) event
	// here; drainPistonBlockEvents fires each at the end of tickWorld (the vanilla same-tick drain).
	// Tick-owned (TICK-05): appended/drained only on the region goroutine. CITE: ServerLevel.blockEvents
	// / blockEvent / runBlockEvents.
	pistonBlockEvents []pistonBlockEvent

	// movingPistons is the per-level store of live PistonMovingBlockEntity animation state for the
	// PISTON port (redstone tier-3): keyed by the moving_piston block's world position, it holds the
	// moved block-state + direction + extending/source flags + the 0.0..1.0 progress the BE ticks over
	// TICKS_TO_EXTEND(2) ticks (PistonMovingBlockEntity.tick, progress += 0.5) before finalTick places
	// the moved block and removes the BE. Vanilla holds this in the block-entity; Sulfur keeps it here on
	// the region (tick-owned, TICK-05) — the animation interpolation is cited-simplified to a faithful
	// 2-tick block-state completion (the observable END STATE — block moved by 1, head placed/removed —
	// is 1:1). CITE: PistonMovingBlockEntity (movedState/direction/extending/isSourcePiston/progress).
	movingPistons map[pk.Position]*movingPistonBE

	// fluidSchedule is the per-region GAMEPLAY-05 scheduled-fluid-tick queue. Lazily constructed
	// inside tickFluids (a nil queue drains to nothing).
	fluidSchedule *fluidScheduleQueue

	// raidsManager is the per-region Raids manager (raids.go — the ServerLevel.getRaids() analogue): it
	// holds every active Raid for this region's level and is ticked once per tick on the coordinator at
	// the quiescent barrier (raidsTickAllRegions). nil until the first raid is created (ensureRaidsManager
	// lazily builds it); most regions never have one. Coordinator-owned (TICK-05).
	raidsManager *raidsManager

	// poiManager is the per-region Point-of-Interest manager (poi.go -- the ServerLevel.getPoiManager()
	// analogue): the per-section store of POI records (beds -> HOME, bells -> MEETING) the village-center
	// query + bad-omen raid trigger read. nil until the first POI is registered (ensurePoiManager lazily
	// builds it); a region with no beds/bells never has one. Coordinator/region-goroutine-owned (TICK-05),
	// mutated only by the block place/break POI hooks + the coordinator-barrier village scan.
	poiManager *poiManager

	// levelRandom is the per-region level RandomSource (Level.random analogue). It is NEVER shared
	// across regions: two goroutines drawing from one LegacyRandomSource is a data race AND makes
	// the RNG stream non-deterministic per region (27-RESEARCH anti-pattern). newRegion seeds it
	// from a unique nondeterministic seed, exactly like vanilla's RandomSource.create() (the level
	// random is NOT seed-pinned — only worldgen RNG is). Advanced only on the region's goroutine.
	levelRandom *levelgen.LegacyRandomSource

	// randValue is the per-region port of net.minecraft.world.level.Level.randValue — the SEPARATE
	// integer LCG state that ONLY Level.getBlockRandomPos advances (`randValue = randValue*3 +
	// 1013904223`). It is DELIBERATELY NOT levelRandom: in vanilla getBlockRandomPos never touches
	// this.random, so the random-tick block-POSITION sampling must not perturb the region's
	// levelRandom stream (the one the pig oracle pins). Vanilla seeds it ONCE in the Level ctor from
	// RandomSource.createThreadLocalInstance().nextInt() (a nondeterministic int); newRegion seeds it
	// from the low 32 bits of a unique nondeterministic seed — same "arbitrary starting int"
	// semantics, per-region, never shared. Advanced only on the region's goroutine.
	// CITE: Level.randValue (protected int; ctor RandomSource.createThreadLocalInstance().nextInt());
	// Level.getBlockRandomPos.
	randValue int32

	// spawnScanPending is the OPT-03 single-in-flight gate for the async natural-spawn scan, now
	// per-region (naturalSpawn sets it when it submits; spawnCandidatesReady.applyTo clears it).
	// A plain bool touched only on the region's goroutine, so it needs no atomic.
	spawnScanPending bool

	// pendingTransfers is the Phase-27 STEP-3 cross-region hand-off queue: detectTransfers (run at
	// the END of this region's tick, on the region's goroutine) appends a transferIntent for every
	// entity whose column now maps to ANOTHER region; applyCrossRegionTransfers drains it at the
	// barrier (the quiescent coordinator) and moves each entity A→B. It is touched ONLY on this
	// region's goroutine (append, during the tick) and on the coordinator at the barrier (drain, when
	// no region ticks) — never concurrently — so it needs no lock (27-RESEARCH Pattern 4). nil until
	// the first transfer is queued; reset to [:0] after each drain (the backing array is reused).
	pendingTransfers []transferIntent

	// pendingDamage is the Phase-29 cross-region DAMAGE queue (the project's FIRST true cross-region
	// write — PITFALLS Pitfall 2), mirroring pendingTransfers above. handleAttack (on the dispatch
	// goroutine, this region being the ATTACKER's region) appends a damageIntent for every cross-region
	// hit (a player here hitting a mob owned by ANOTHER region, the intent tagged `to` that owner);
	// applyCrossRegionDamage drains it at the barrier (the quiescent coordinator) and applies each hit
	// via withRegion(owner) -> applyDamageEntity, re-resolving the owner by id (drop if gone). Like
	// pendingTransfers it is touched ONLY on this region's goroutine (the append, off the fan-out) and
	// on the coordinator at the barrier (the drain, when no region ticks) — never concurrently — so it
	// needs no lock (A3: kept on the SOURCE region to avoid a cross-goroutine append race). nil until
	// the first cross-region hit is queued; reset to [:0] after each drain (the backing array is reused).
	pendingDamage []damageIntent

	// tickHook is a TEST-ONLY seam (Phase-27 STEP-2): when non-nil, region.tick invokes it FIRST,
	// on the region's goroutine, before the per-region phases. It exists so a test can inject a
	// region whose tick panics (TestRegionPanicIsolated — the T-27-02 DoS-isolation proof: conc
	// re-raises the panic on the coordinator goroutine where recoverTick catches it) or observe
	// the barrier ordering (TestCoordinatorBarrier). In production it stays nil and costs one
	// nil-check per region per tick — the same zero-cost discipline as applyInputHook/phaseTrace.
	tickHook func()
}

// newRegion constructs an empty region bound to the global coordinator. It wires a fresh
// per-region entity store and a fresh per-region levelRandom (seeded from a unique
// nondeterministic seed, NEVER shared — the 27-RESEARCH never-shared-RNG invariant). The
// world/worker/blockTicks/fluidSchedule stay nil until wired (matching how the pre-extraction
// TickLoop wired them lazily). coord may be nil for a standalone region in a unit test.
func newRegion(id regionID, coord *TickLoop) *region {
	return &region{
		id:    id,
		coord: coord,
		// entities: non-nil from construction so the per-region store the tracker/handles read
		// exists immediately (the pre-extraction TickLoop's `entities: newEntityStore()`).
		entities: newEntityStore(),
		// levelRandom: PER-REGION, seeded from a unique nondeterministic seed exactly like the
		// pre-extraction TickLoop's `levelRandom: levelgen.NewLegacyRandomSource(uniqueLevelRandomSeed())`.
		// Distinct per region (never shared) — the anti-pattern guard.
		levelRandom: levelgen.NewLegacyRandomSource(uniqueLevelRandomSeed()),
		// randValue: the Level.randValue seed — a nondeterministic starting int (low 32 bits of a
		// fresh unique seed), matching vanilla's ctor init from createThreadLocalInstance().nextInt().
		// Its own stream, never levelRandom, so the block-random-pos LCG cannot perturb the pig oracle.
		randValue: int32(uniqueLevelRandomSeed()),
	}
}
