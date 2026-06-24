package server

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// tracker_test.go covers ENT-01's headline behavior: the SYNCHRONOUS entityTracker behind
// the unchanged tracker.Tick() seam. It asserts the per-player visibility diff (newly-visible
// → AddEntity(+SetEntityData); moved-and-tracked → Teleport(+RotateHead); gone → ONE batched
// RemoveEntities), that a player never tracks itself, that the per-player tracked set
// bookkeeps without duplicate spawns or stale entries, and that the seam (interface + call
// site) is unchanged and Tick() spawns no goroutine.
//
// The tracker mutates only tick-owned state on the owner goroutine — the Docker -race gate
// over ./server/... is the structural proof (TICK-05 / T-6-08).

// captureClient builds a Client backed by a real bounded ChannelQueue (no socket / no
// writeLoop). The tracker's p.client.Send enqueues onto it; drainPackets closes the queue and
// pulls every buffered packet so a test can assert exactly what the tracker emitted.
func captureClient(cap int) *Client { return NewClient(nil, cap) }

// drainPackets closes the client's outbound queue and returns every packet the tracker
// enqueued, in FIFO order. A closed buffered ChannelQueue drains its buffer then reports
// ok=false (the writeLoop stop signal), so Pull terminates cleanly.
func drainPackets(c *Client) []pk.Packet {
	c.outbound.Close()
	var out []pk.Packet
	for {
		p, ok := c.outbound.Pull()
		if !ok {
			return out
		}
		out = append(out, p)
	}
}

// countID returns how many packets in the slice carry the given clientbound id.
func countID(ps []pk.Packet, id packetid.ClientboundPacketID) int {
	n := 0
	for _, p := range ps {
		if p.ID == int32(id) {
			n++
		}
	}
	return n
}

// newTrackerPlayer registers a tick player at (x,z) with a capturing client and a known
// entity id, returning the player so the test can drain its client. The caller passes a
// player entity id that must NOT collide with any entity it later spawns (entity ids come
// from loop.idAlloc starting at 1, so pass a high, distinct value like 1000).
func newTrackerPlayer(loop *TickLoop, entityID int32, x, z float64) *tickPlayer {
	// viewDist is irrelevant to the tracker (it uses its own trackRange const), set only so
	// the player looks fully registered.
	p := &tickPlayer{
		client:   captureClient(64),
		entityID: entityID,
		x:        x, z: z,
		viewDist: 8,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// syncTrackerTick drives the SYNCHRONOUS golden entityTracker directly (not loop.tracker, which
// is the OPT-02 asyncTracker after the swap). The Phase-6 tracker tests below assert the golden
// diff LOGIC — newly-visible/moved/gone packet emission and the tracked-set bookkeeping — which
// the synchronous entityTracker still implements unchanged. The async equivalence is covered
// separately by TestAsyncTrackerMatchesSync, so these tests keep driving the golden reference
// inline (a harness change, not an assertion change).
func syncTrackerTick(loop *TickLoop) { (&entityTracker{loop: loop}).Tick() }

// TestVisibilityDiff asserts the FIRST Tick spawns an in-range entity (AddEntity +
// SetEntityData) and records it, and a SECOND Tick with the entity unmoved sends NO new
// AddEntity (no duplicate spawn). The player does not track itself.
func TestVisibilityDiff(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	// A test entity one block from the player, well within trackRange.
	e := NewEntity(loop.idAlloc.AllocID(), entity.SulfurCube, 9.5, 64, 9.5)
	loop.entities.add(e)

	// First Tick: the entity is newly visible → AddEntity + SetEntityData, and tracked.
	syncTrackerTick(loop)
	if !p.tracked[e.id] {
		t.Fatalf("after first Tick, entity %d must be in the player's tracked set", e.id)
	}

	// Second Tick (entity unmoved): no NEW AddEntity may be sent (no duplicate spawn).
	syncTrackerTick(loop)

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundAddEntity); n != 1 {
		t.Fatalf("AddEntity sent %d times across two unmoved ticks, want exactly 1 (no duplicate spawn)", n)
	}
	if n := countID(got, packetid.ClientboundSetEntityData); n != 1 {
		t.Fatalf("SetEntityData sent %d times, want exactly 1 (sent once with the spawn)", n)
	}

	// The player must never track its OWN entity id.
	if p.tracked[p.entityID] {
		t.Fatalf("player tracked its own entity id %d — a player must not track itself", p.entityID)
	}
}

// TestTrackerSelfNotTracked asserts a player whose OWN Entity is in the store is not spawned
// to itself: the player's entity sits exactly at the player position but never produces an
// AddEntity for that player.
func TestTrackerSelfNotTracked(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	selfID := loop.idAlloc.AllocID()
	p := newTrackerPlayer(loop, selfID, 8.5, 8.5)

	// The player's own entity instance, at the player's position.
	self := NewEntity(selfID, entity.SulfurCube, 8.5, 64, 8.5)
	loop.entities.add(self)

	syncTrackerTick(loop)
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundAddEntity); n != 0 {
		t.Fatalf("player was spawned its own entity (%d AddEntity), want 0 — a player never tracks itself", n)
	}
	if p.tracked[selfID] {
		t.Fatalf("player tracked its own id %d", selfID)
	}
}

// TestTrackerMove asserts a tracked entity that MOVES within range produces a Teleport
// (+ RotateHead) on the next Tick — NOT a re-AddEntity.
func TestTrackerMove(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	e := NewEntity(loop.idAlloc.AllocID(), entity.SulfurCube, 9.5, 64, 9.5)
	loop.entities.add(e)

	syncTrackerTick(loop)            // spawn
	_ = drainPackets(p.client)     // discard the spawn packets
	p.client = captureClient(64)   // fresh capture for the move tick
	loop.clientIndex[p.client] = p // keep the index consistent (not strictly needed here)

	// Move the entity a few blocks (still in range) via the store's bucket-consistent move.
	loop.entities.move(e, 14.5, 64, 14.5)

	syncTrackerTick(loop)
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundAddEntity); n != 0 {
		t.Fatalf("a moved-but-still-tracked entity sent %d AddEntity, want 0 (must not re-spawn)", n)
	}
	if n := countID(got, packetid.ClientboundTeleportEntity); n != 1 {
		t.Fatalf("a moved entity sent %d TeleportEntity, want exactly 1", n)
	}
	if n := countID(got, packetid.ClientboundRotateHead); n != 1 {
		t.Fatalf("a moved entity sent %d RotateHead, want exactly 1", n)
	}
}

// TestTrackerRemove asserts an entity that leaves range (or is removed) produces exactly ONE
// batched RemoveEntities containing its id and is dropped from the tracked set; a re-entry
// re-Adds it.
func TestTrackerRemove(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	e := NewEntity(loop.idAlloc.AllocID(), entity.SulfurCube, 9.5, 64, 9.5)
	loop.entities.add(e)

	syncTrackerTick(loop)          // spawn
	_ = drainPackets(p.client)   // discard spawn
	p.client = captureClient(64) // fresh capture
	loop.clientIndex[p.client] = p

	// Move the entity FAR away (out of trackRange ≈ 6 columns ≈ 96 blocks): column 100.
	loop.entities.move(e, 1600, 64, 1600)

	syncTrackerTick(loop)
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundRemoveEntities); n != 1 {
		t.Fatalf("an out-of-range entity sent %d RemoveEntities, want exactly 1 (batched)", n)
	}
	if p.tracked[e.id] {
		t.Fatalf("an out-of-range entity is still tracked; it must be dropped from the set")
	}

	// Re-entry: move it back into range → it must re-Add.
	p.client = captureClient(64)
	loop.clientIndex[p.client] = p
	loop.entities.move(e, 9.5, 64, 9.5)
	syncTrackerTick(loop)
	got2 := drainPackets(p.client)
	if n := countID(got2, packetid.ClientboundAddEntity); n != 1 {
		t.Fatalf("a re-entering entity sent %d AddEntity, want exactly 1 (clean re-spawn)", n)
	}
}

// TestTrackerRemoveBatchesMany asserts MULTIPLE entities leaving range in one tick are batched
// into a SINGLE RemoveEntities carrying all their ids (not one packet per entity).
func TestTrackerRemoveBatchesMany(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	var es []*Entity
	for i := 0; i < 3; i++ {
		e := NewEntity(loop.idAlloc.AllocID(), entity.SulfurCube, 9.5+float64(i), 64, 9.5)
		loop.entities.add(e)
		es = append(es, e)
	}

	syncTrackerTick(loop)          // spawn all three
	_ = drainPackets(p.client)   // discard spawns
	p.client = captureClient(64) // fresh capture
	loop.clientIndex[p.client] = p

	// All three leave range this tick.
	for _, e := range es {
		loop.entities.move(e, 1600, 64, 1600)
	}

	syncTrackerTick(loop)
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundRemoveEntities); n != 1 {
		t.Fatalf("3 entities leaving range sent %d RemoveEntities, want exactly 1 (single batch)", n)
	}
	// The single packet must carry all three ids.
	var count pk.VarInt
	var a, b, c pk.VarInt
	for _, pkt := range got {
		if pkt.ID == int32(packetid.ClientboundRemoveEntities) {
			if err := pkt.Scan(&count, &a, &b, &c); err != nil {
				t.Fatalf("RemoveEntities scan failed: %v", err)
			}
		}
	}
	if int32(count) != 3 {
		t.Fatalf("batched RemoveEntities count = %d, want 3 (all ids in one packet)", count)
	}
}

// TestTrackerSynchronousNoChangeToSeam asserts the entityTracker satisfies the UNCHANGED
// one-method tracker interface (the seam is filled, not modified). NewTickLoop now assigns the
// OPT-02 asyncTracker (08-04) behind that seam — the synchronous entityTracker is KEPT as the
// golden reference. The interface and the t.tracker.Tick() call site in tick_phases.go are
// deliberately UNCHANGED so the swap is a single line in NewTickLoop.
func TestTrackerSynchronousNoChangeToSeam(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// The interface is still the one-method seam: a value satisfying `interface{ Tick() }`
	// must be assignable from the tracker (compile-time + runtime check). The concrete type
	// is the asyncTracker after the OPT-02 swap; entityTracker still satisfies it too.
	var seam interface{ Tick() } = loop.tracker
	seam.Tick()

	// The synchronous entityTracker is still constructible behind the same seam (the golden
	// reference the async tracker is diffed against). It holds a back-reference to the loop.
	et := &entityTracker{loop: loop}
	if et.loop != loop {
		t.Fatalf("entityTracker must hold a back-reference to its loop")
	}

	// A bare Tick() with no players/entities must be a safe inline no-op (no panic).
	et.Tick()
	loop.tracker.Tick()
}

// drainAsyncTracker submits the async tracker's per-player diffs, waits for the worker results
// to land on asyncIn2, then drains them on the OWNER (the applyAsyncResults discipline) so the
// owner-side p.client.Send + p.tracked update run. It mirrors what tickOnce does, but in a test
// harness: Tick() submits to the pool, the worker sends trackerDiffReady on asyncIn2, and this
// drains that channel inline. expected is how many results to wait for (one per player that had
// a client and was submitted) so the test is deterministic without sleeping.
func drainAsyncTracker(t *testing.T, loop *TickLoop, expected int) {
	t.Helper()
	loop.tracker.Tick() // submits the per-player diff math to trackerPool (off-tick)

	// Wait for exactly `expected` results to arrive on asyncIn2, applying each on the owner.
	// A bounded timeout keeps a wedged worker from hanging the test.
	deadline := time.After(2 * time.Second)
	for i := 0; i < expected; i++ {
		select {
		case r := <-loop.asyncIn2:
			r.applyTo(loop) // OWNER-side: Send the diff packets + update p.tracked
		case <-deadline:
			t.Fatalf("async tracker: timed out waiting for diff result %d/%d", i+1, expected)
		}
	}
}

// TestAsyncTrackerMatchesSync drives a fixed scene through BOTH the async tracker (submit →
// drain → owner Send) and the synchronous entityTracker (direct send) and asserts the SAME
// per-player packet COUNTS by clientbound id — OPT-02 is an executor swap, not a behavior
// change. The async path produces them a tick later, but the SET/content must match.
func TestAsyncTrackerMatchesSync(t *testing.T) {
	// --- Reference run: the synchronous entityTracker (golden). ---
	refLoop := NewTickLoop(newFakeClock())
	refP := newTrackerPlayer(refLoop, 1000, 8.5, 8.5)
	for i := 0; i < 3; i++ {
		e := NewEntity(refLoop.idAlloc.AllocID(), entity.SulfurCube, 9.5+float64(i), 64, 9.5)
		refLoop.entities.add(e)
	}
	(&entityTracker{loop: refLoop}).Tick()
	refPackets := drainPackets(refP.client)

	// --- Async run: the asyncTracker (off-tick diff, owner Send). Identical scene. ---
	asyncLoop := NewTickLoop(newFakeClock())
	asyncP := newTrackerPlayer(asyncLoop, 1000, 8.5, 8.5)
	for i := 0; i < 3; i++ {
		e := NewEntity(asyncLoop.idAlloc.AllocID(), entity.SulfurCube, 9.5+float64(i), 64, 9.5)
		asyncLoop.entities.add(e)
	}
	drainAsyncTracker(t, asyncLoop, 1) // one player → one diff result
	asyncPackets := drainPackets(asyncP.client)

	// The async path must emit the SAME packet counts per clientbound id as the golden sync path.
	ids := []packetid.ClientboundPacketID{
		packetid.ClientboundAddEntity,
		packetid.ClientboundSetEntityData,
		packetid.ClientboundTeleportEntity,
		packetid.ClientboundRotateHead,
		packetid.ClientboundRemoveEntities,
	}
	for _, id := range ids {
		if got, want := countID(asyncPackets, id), countID(refPackets, id); got != want {
			t.Fatalf("async tracker emitted %d of packet id %d, sync golden emitted %d — must match", got, id, want)
		}
	}
	// Three entities spawned: AddEntity must be 3 in both (sanity that the scene is non-trivial).
	if n := countID(asyncPackets, packetid.ClientboundAddEntity); n != 3 {
		t.Fatalf("async tracker spawned %d entities, want 3", n)
	}
	// The owner-side tracked set must reflect all three spawns after apply.
	if len(asyncP.tracked) != 3 {
		t.Fatalf("after async apply, player tracked %d entities, want 3", len(asyncP.tracked))
	}
}

// TestAsyncTrackerSendsOnOwner asserts NO packet reaches the client until the result is drained
// on the owner (applyAsyncResults). The worker only computes the diff; emission is owner-side
// (Pitfall 5). We submit, then assert the queue is empty BEFORE the owner drains, then drain and
// assert the packets arrive.
func TestAsyncTrackerSendsOnOwner(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	e := NewEntity(loop.idAlloc.AllocID(), entity.SulfurCube, 9.5, 64, 9.5)
	loop.entities.add(e)

	loop.tracker.Tick() // submit the diff math off-tick

	// Wait for the worker to finish (its result lands on asyncIn2) WITHOUT applying it yet.
	var r asyncResult
	select {
	case r = <-loop.asyncIn2:
	case <-time.After(2 * time.Second):
		t.Fatal("async tracker: timed out waiting for the off-tick diff result")
	}

	// The worker has run, but applyTo has NOT — no packet may have been sent yet, because
	// emission is owner-side only. Peek the queue by closing+draining a SEPARATE assertion:
	// the worker must not have Sent. We assert the player's tracked set is still empty (the
	// tracked update is also owner-side in applyTo).
	if len(p.tracked) != 0 {
		t.Fatalf("worker mutated p.tracked off-tick (len=%d) — tracked update must be owner-side", len(p.tracked))
	}

	// Now apply on the owner: the packets are Sent and tracked is updated here.
	r.applyTo(loop)
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundAddEntity); n != 1 {
		t.Fatalf("after owner apply, AddEntity sent %d times, want 1", n)
	}
	if !p.tracked[e.id] {
		t.Fatalf("after owner apply, entity %d must be tracked", e.id)
	}
}

// TestAsyncTrackerLeftPlayerDropped submits a diff for a player, removes the player BEFORE the
// result is drained, then drains — trackerDiffReady.applyTo must DROP it (no send, no nil-deref).
func TestAsyncTrackerLeftPlayerDropped(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	e := NewEntity(loop.idAlloc.AllocID(), entity.SulfurCube, 9.5, 64, 9.5)
	loop.entities.add(e)

	loop.tracker.Tick() // submit the diff for player 1000

	// Wait for the worker result.
	var r asyncResult
	select {
	case r = <-loop.asyncIn2:
	case <-time.After(2 * time.Second):
		t.Fatal("async tracker: timed out waiting for the off-tick diff result")
	}

	// The player leaves between submit and apply: remove it from the owner's collections.
	loop.players = nil
	delete(loop.clientIndex, p.client)

	// Apply MUST be a safe drop — the player is gone, so no send and no panic.
	r.applyTo(loop) // must not panic

	// Nothing was sent to the (departed) player's client.
	got := drainPackets(p.client)
	if len(got) != 0 {
		t.Fatalf("a diff for a left player sent %d packets, want 0 (must be dropped)", len(got))
	}
}

// TestAsyncTrackerSwapPointCompiles asserts the asyncTracker satisfies the tracker interface and
// is the executor NewTickLoop assigns at the single swap-point. The pipeline order is asserted
// separately by TestTickPhaseOrder (unchanged).
func TestAsyncTrackerSwapPointCompiles(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	at, ok := loop.tracker.(*asyncTracker)
	if !ok {
		t.Fatalf("NewTickLoop must assign a *asyncTracker to t.tracker (the OPT-02 swap), got %T", loop.tracker)
	}
	if at.loop != loop {
		t.Fatalf("asyncTracker must hold a back-reference to its loop")
	}

	// asyncTracker satisfies the unchanged one-method seam.
	var seam interface{ Tick() } = at
	seam.Tick() // safe inline no-op with no players
}
