package server

// async_stress_test.go — OPT-06: the FINAL Phase-8 acceptance gate. Where the per-subsystem tests
// (08-02 pathfinding, 08-04 tracker, 08-05 spawner) each proved their OWN pool↔tick boundary in
// isolation, this file runs ALL of them SIMULTANEOUSLY under ONE running TickLoop and gives the
// Docker -race detector the full cross-subsystem boundary to inspect — interleavings no isolated
// test can surface. It also proves the async swaps changed only WHEN work happens, not WHAT the
// server does (the behavior-regression assertions: paths still arrive and are followed 1+ tick
// late, the tracker still emits the spawn/move/despawn lifecycle, mobs still spawn under the cap).
//
// THE PHASE GATE (run it, confirm GREEN before closing Phase 8):
//
//	MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 \
//	    go test -race ./server/... ./world/... ./save/... -count=10
//
// -race needs cgo (the host is CGO_ENABLED=0), so the gate runs in the golang:1.26 image — the
// established Phase 2-7 path. -count=10 re-runs each test ten times to shake the scheduler so an
// INTERMITTENT race (a worker that captured a live pointer, an off-tick send) cannot hide behind a
// single lucky pass. The single-owner rejoin discipline (snapshot-on-owner → compute-off-tick →
// apply-on-owner) is WHY this is clean by construction; this gate is the proof.

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
)

// newStressLoop builds a TickLoop over a small LOADED world: a square of all-air chunks with a
// solid stone floor at floorY, wired straight onto the tick-owned ChunkManager (like
// newPhysicsLoop, no off-tick chunk worker — the chunks are already Ready so tickChunks/flushOutbound
// are no-ops here and the stress stays focused on the path/tracker/spawn pools). Returns the loop and
// the floor Y. The chunks span columns [-radius,radius]^2 so mobs can wander and path across
// boundaries while the tracker diffs and the spawner scans.
func newStressLoop(t *testing.T, radius, floorY int) *TickLoop {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.world = mgr
	// PLUGIN-04 (Plan 24-02): the SWAP routes the natural pig spawn through spawnVanillaPig, which
	// needs the boot-loaded vanilla_pig registry. Install it so the stress loop's natural spawner can
	// actually add plugin pigs under load (without it the spawn applyTo panics and the tickOnce recover
	// drops the mob, leaving the stress trivial).
	installVanillaPigRegistry(loop)
	for cx := -radius; cx <= radius; cx++ {
		for cz := -radius; cz <= radius; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, floorY)
		}
	}
	return loop
}

// addStressPlayer registers a tick player at (x,z) with a capturing client (so the async tracker has
// a real Send sink to emit the visibility lifecycle into) standing on the floor. It mirrors what
// newTrackerPlayer does but routes through the same players/clientIndex collections the running loop
// drives. The player carries a distinct high entity id so it never collides an allocator-issued mob id.
func addStressPlayer(loop *TickLoop, entityID int32, x, z float64, floorY int) *tickPlayer {
	p := &tickPlayer{
		client:   captureClient(4096), // generous so a few hundred ticks of diffs never block a worker send
		entityID: entityID,
		x:        x, y: float64(floorY + 1), z: z,
		viewDist: serverViewDistance,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestAllAsyncSubsystemsRaceClean is the COMBINED all-subsystems stress (OPT-06). It builds a small
// loaded world, registers two players (each with a capturing client so the async tracker emits real
// packets), seeds several Pigs with the real newPigAI() (so tickAI submits A* paths to pathPool),
// and then advances the running TickLoop many ticks. Across those ticks the pipeline drives, every
// tick and SIMULTANEOUSLY:
//
//   - tickAI → serverAiStep → navigation.requestPath SUBMITS A* computes to pathPool (OPT-01),
//   - tickAI → naturalSpawn (every spawnInterval) SUBMITS a candidate scan to spawnPool (OPT-03),
//   - tracker.Tick (the asyncTracker) SUBMITS per-player visibility diffs to trackerPool (OPT-02),
//   - applyAsyncResults DRAINS asyncIn2 on the OWNER, rejoining all three subsystems' results.
//
// The pools' workers compute off-tick over immutable owner-built snapshots and rejoin via asyncIn2;
// the owner is the sole mutator. Under the Docker -race gate (-count=10) the detector inspects the
// whole pool→channel→owner boundary across subsystems and must find NOTHING. The test asserts the
// world actually churned (mobs exist, the tracker emitted spawns) so the stress is non-trivial, and
// drains any in-flight async work before Close() so no worker is mid-send at teardown.
func TestAllAsyncSubsystemsRaceClean(t *testing.T) {
	const floorY = 64
	loop := newStressLoop(t, 2, floorY) // chunks [-2,2]^2 — room to wander and path
	defer loop.Close()

	// Two players with capturing clients, a few columns apart so each has its own visibility set and
	// the trackerPool runs two independent diffs every tick.
	addStressPlayer(loop, 100000, 8.5, 8.5, floorY)
	addStressPlayer(loop, 100001, 24.5, 8.5, floorY)

	// Several Pigs with the REAL passive AI (stroll/look goals): each tickAI step asks navigation to
	// (re)compute a path, which submits to pathPool. Spread across the world so the tracker sees them
	// enter/leave range as they wander.
	for i := 0; i < 8; i++ {
		x := 4.5 + float64(i*3)
		z := 6.5 + float64((i%3)*4)
		e := NewEntity(loop.idAlloc.AllocID(), entity.Pig, x, float64(floorY+1), z)
		e.ai = newPigAI()
		loop.entities.add(e)
	}

	startMobs := loop.entities.len()

	// Drive the loop through a few hundred logical ticks. advance() consumes whole 50ms steps from the
	// fake clock and runs the full ordered pipeline per step (tickAI submits to path/spawn pools,
	// tracker submits to trackerPool, applyAsyncResults drains asyncIn2 — all on the owner). The pool
	// workers run concurrently on their own goroutines; the -race detector watches every crossing.
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	const ticks = 400
	for i := 0; i < ticks; i++ {
		clock.add(tickStep) // exactly one logical tick's worth of synthetic time
		loop.advance(clock.Now())
	}

	// Quiesce: give any in-flight pool worker a bounded chance to land its final result, draining on
	// the owner, so no worker is mid-send into asyncIn2 when Close() releases the pools.
	deadline := time.After(2 * time.Second)
	for {
		loop.applyAsyncResults()
		if !loop.spawnScanPending {
			break
		}
		select {
		case <-deadline:
			t.Fatal("a spawn scan never rejoined while quiescing the stress loop")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// A couple of extra drains to mop up any path/tracker results that landed during the last submits.
	loop.applyAsyncResults()
	loop.applyAsyncResults()

	// The stress must have been NON-TRIVIAL: the spawner should have added at least one mob over 400
	// ticks (20 spawnInterval cycles, under cap, with a loaded world + players), and the tracker must
	// have spawned the seeded mobs to at least one player (its tracked set is non-empty). If neither
	// happened the test would pass the race gate vacuously — assert real work occurred.
	if loop.entities.len() <= startMobs {
		t.Fatalf("the spawner added no mobs over %d ticks (%d -> %d) — the stress was trivial",
			ticks, startMobs, loop.entities.len())
	}
	tracked := 0
	for _, p := range loop.players {
		tracked += len(p.tracked)
	}
	if tracked == 0 {
		t.Fatal("the async tracker tracked no entities for any player — the tracker subsystem did not run under load")
	}
}

// TestBehaviorRegressionPathArrives proves OPT-01 is additive: a mob still NAVIGATES around the
// world via the async path. With an obstacle (a wall) between a Pig and a target east of it, after
// enough running-loop ticks the mob has progressed PAST its start toward the far side — the path was
// computed off-tick, arrived 1+ ticks late via applyAsyncResults, and was followed. WHAT (the mob
// navigates) is unchanged; only WHEN the path is produced shifted off-tick.
func TestBehaviorRegressionPathArrives(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	defer loop.Close()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	// A Pig at the west end with a deterministic MOVE goal toward a reachable point east along the
	// floor. fixedTargetGoal (navigation_test.go) sets a concrete wantTarget so the assertion is
	// deterministic (unlike the random stroll goal).
	e := testEntity(1, entity.Pig, 2.5, float64(floorY+1), 8.5)
	ai := &mobAI{}
	ai.navigation.speed = 0.2
	ai.goals.addGoal(0, &fixedTargetGoal{
		baseGoal: newBaseGoal(flagMove),
		tx:       12.5, ty: float64(floorY + 1), tz: 8.5,
	})
	e.ai = ai
	loop.entities.add(e)

	startX := e.x
	// Drive the AI + physics + async rejoin per tick exactly as the live pipeline does: serverAiStep
	// SUBMITS the path off-tick, applyAsyncResults rejoins it (1+ ticks late), navigation.tick follows.
	// The path pool is NON-BLOCKING (ants.WithNonblocking): under full-suite CPU contention a Submit
	// can hit ErrPoolOverload and DROP that tick's request — which is the correct live behavior (the
	// mob simply re-submits next tick). So the assertion is "eventually navigates", not "arrives in
	// exactly 400 ticks": tick until the mob has progressed past the threshold, up to a generous cap
	// that absorbs dropped-and-resubmitted paths (the live server has no fixed deadline either). This
	// makes the test deterministic instead of flaky under contention.
	const maxTicks = 4000
	arrived := false
	for i := 0; i < maxTicks; i++ {
		loop.tickAI()
		loop.tickPhysics()
		loop.applyAsyncResults()
		if e.x > startX+2.0 {
			arrived = true
			break
		}
	}

	if !arrived {
		t.Fatalf("the async path never arrived/was followed after %d ticks: mob x=%v (start %v) — OPT-01 changed behavior", maxTicks, e.x, startX)
	}
}

// TestBehaviorRegressionTrackerSends proves OPT-02 is additive: the async tracker still emits the
// FULL visibility lifecycle to a near player — AddEntity when a mob enters range, a movement packet
// (TeleportEntity) while it is tracked and moving, and RemoveEntities when it leaves range — matching
// the synchronous golden's behavior, just produced off-tick. We drive the asyncTracker (the executor
// NewTickLoop assigns) through three scenes, draining each diff on the owner, and assert each
// lifecycle packet was sent.
func TestBehaviorRegressionTrackerSends(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	defer loop.Close()
	p := newTrackerPlayer(loop, 100000, 8.5, 8.5)

	// (1) Enter range: a mob one block away → AddEntity.
	e := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 9.5, 64, 9.5)
	loop.entities.add(e)
	drainAsyncTracker(t, loop, 1) // one player → one diff result, applied on the owner
	got := drainPackets(p.client)
	if countID(got, packetid.ClientboundAddEntity) != 1 {
		t.Fatalf("mob entering range: AddEntity sent %d times, want 1 (async tracker dropped the spawn)",
			countID(got, packetid.ClientboundAddEntity))
	}

	// (2) Move within range: a tracked mob that moves → a DELTA move packet from
	// tickEntityMovement (the per-entity ServerEntity.sendChanges port), NOT from the tracker
	// (which now only spawns/despawns). Seed the move base first (moveInit), then move + tick.
	loop.tickEntityMovement() // seed the mob's move base; no packet
	p.client = captureClient(64)
	loop.clientIndex[p.client] = p
	loop.entities.move(e, 13.5, 64, 13.5)
	loop.tickEntityMovement()      // the 4-block delta → MoveEntityPos
	drainAsyncTracker(t, loop, 1)  // tracker: no re-spawn
	got = drainPackets(p.client)
	if countID(got, packetid.ClientboundMoveEntityPos) != 1 {
		t.Fatalf("tracked mob moving: MoveEntityPos sent %d times, want 1 (delta-move lifecycle lost)",
			countID(got, packetid.ClientboundMoveEntityPos))
	}
	if countID(got, packetid.ClientboundAddEntity) != 0 {
		t.Fatalf("a moved-but-tracked mob must NOT re-AddEntity; got %d", countID(got, packetid.ClientboundAddEntity))
	}

	// (3) Leave range: the mob moves far away → exactly one batched RemoveEntities, dropped from tracked.
	p.client = captureClient(64)
	loop.clientIndex[p.client] = p
	loop.entities.move(e, 1600, 64, 1600)
	drainAsyncTracker(t, loop, 1)
	got = drainPackets(p.client)
	if countID(got, packetid.ClientboundRemoveEntities) != 1 {
		t.Fatalf("mob leaving range: RemoveEntities sent %d times, want 1 (despawn lifecycle lost)",
			countID(got, packetid.ClientboundRemoveEntities))
	}
	if p.tracked[e.id] {
		t.Fatal("an out-of-range mob must be dropped from the tracked set after the async despawn")
	}
}

// TestBehaviorRegressionMobSpawns proves OPT-03 is additive: under the CREATURE cap, with a loaded
// world and a nearby player, the async spawner still POPULATES the store — within a bounded number of
// spawnInterval cycles a Pig is added (with a real mobAI so it wanders next tick). The scan moved
// off-tick (submit → owner re-check → add), but the observable outcome (a mob appears under the cap)
// is unchanged.
func TestBehaviorRegressionMobSpawns(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)
	defer loop.Close()

	before := loop.entities.len()
	// Run several spawn cycles. runSpawnCycle (spawner_test.go) submits the off-tick scan and applies
	// the single rejoin on the owner — exactly what applyAsyncResults does in the live loop. A bounded
	// number of cycles must yield at least one spawn under cap.
	const cycles = 8
	for i := 0; i < cycles && loop.entities.len() == before; i++ {
		runSpawnCycle(t, loop)
	}

	if loop.entities.len() <= before {
		t.Fatalf("the async spawner added no mob over %d cycles under cap (%d -> %d) — OPT-03 changed behavior",
			cycles, before, loop.entities.len())
	}
	var pig *Entity
	for _, e := range loop.entities.byID {
		if e.typ == entity.Pig.ID {
			pig = e
		}
	}
	if pig == nil || pig.ai == nil {
		t.Fatal("an async-spawned Pig must carry a real mobAI (so it wanders) — the spawn is not a static placeholder")
	}
}
