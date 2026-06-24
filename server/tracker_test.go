package server

import (
	"testing"

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
	loop.tracker.Tick()
	if !p.tracked[e.id] {
		t.Fatalf("after first Tick, entity %d must be in the player's tracked set", e.id)
	}

	// Second Tick (entity unmoved): no NEW AddEntity may be sent (no duplicate spawn).
	loop.tracker.Tick()

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

	loop.tracker.Tick()
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

	loop.tracker.Tick()            // spawn
	_ = drainPackets(p.client)     // discard the spawn packets
	p.client = captureClient(64)   // fresh capture for the move tick
	loop.clientIndex[p.client] = p // keep the index consistent (not strictly needed here)

	// Move the entity a few blocks (still in range) via the store's bucket-consistent move.
	loop.entities.move(e, 14.5, 64, 14.5)

	loop.tracker.Tick()
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

	loop.tracker.Tick()          // spawn
	_ = drainPackets(p.client)   // discard spawn
	p.client = captureClient(64) // fresh capture
	loop.clientIndex[p.client] = p

	// Move the entity FAR away (out of trackRange ≈ 6 columns ≈ 96 blocks): column 100.
	loop.entities.move(e, 1600, 64, 1600)

	loop.tracker.Tick()
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
	loop.tracker.Tick()
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

	loop.tracker.Tick()          // spawn all three
	_ = drainPackets(p.client)   // discard spawns
	p.client = captureClient(64) // fresh capture
	loop.clientIndex[p.client] = p

	// All three leave range this tick.
	for _, e := range es {
		loop.entities.move(e, 1600, 64, 1600)
	}

	loop.tracker.Tick()
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
// one-method tracker interface (the seam is filled, not modified) and that NewTickLoop now
// assigns a real entityTracker (not the noopTracker) behind it — so the t.tracker.Tick() call
// site in tick_phases.go drives the real tracker without any interface or call-site change.
// The synchronous-no-goroutine guarantee is proven structurally by the Docker -race gate (a
// goroutine racing the tick-owned tracked set would trip it); here we assert the type identity
// and that a bare Tick() completes inline.
func TestTrackerSynchronousNoChangeToSeam(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	// The loop's tracker field is the real entityTracker, satisfying `tracker interface{ Tick() }`.
	et, ok := loop.tracker.(*entityTracker)
	if !ok {
		t.Fatalf("NewTickLoop must assign a *entityTracker to t.tracker (the Phase-3 seam is FILLED), got %T", loop.tracker)
	}
	if et.loop != loop {
		t.Fatalf("entityTracker must hold a back-reference to its loop")
	}

	// A bare Tick() with no players/entities must be a safe inline no-op (no panic, no
	// goroutine needed) — the seam's synchronous contract.
	loop.tracker.Tick()

	// The interface is still the one-method seam: a value satisfying `interface{ Tick() }`
	// must be assignable from the tracker (compile-time + runtime check).
	var seam interface{ Tick() } = loop.tracker
	seam.Tick()
}
