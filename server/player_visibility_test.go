package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"

	"github.com/google/uuid"
)

// player_visibility_test.go covers GAMEPLAY-01: a joining player becomes an entity.Player
// (id==entityID) in the tick-owned store, is position-synced from the authoritative
// tickPlayer each tick BEFORE the tracker reads near(), is tab-list-broadcast
// (PlayerInfoUpdate ADD) to every OTHER player at join + (PlayerInfoRemove) at leave, and
// is removed from the store on leave. All assertions exercise the owner goroutine via the
// tick-loop register/unregister + drainRegistrations seam (TICK-05).

// visPlayer constructs a registerable tickPlayer with a capturing client at (x,y,z). It is
// NOT inserted into the loop directly — the test sends it on loop.register so
// drainRegistrations performs the real owner-side insert (the GAMEPLAY-01 join seam).
func visPlayer(entityID int32, name string, x, y, z float64) *tickPlayer {
	return &tickPlayer{
		client:            captureClient(64),
		entityID:          entityID,
		uuid:              uuid.New(),
		name:              name,
		confirmedTeleport: true,
		x:                 x,
		y:                 y,
		z:                 z,
		center:            columnOf(x, z),
		viewDist:          serverViewDistance,
		sentChunks:        map[level.ChunkPos]bool{},
		secs:              overworldSections,
		health:            maxHealth,
		food:              maxFood,
		saturation:        defaultSaturation,
	}
}

// TestPlayerVisibility proves the three GAMEPLAY-01 store invariants: (1) a registered
// player gets a store Entity with id==entityID at its position; (2) after a tick the store
// Entity's position equals the player's; (3) on leave the Entity is removed from the store.
func TestPlayerVisibility(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	p1 := visPlayer(1000, "Alice", 8.5, 64, 8.5)
	p2 := visPlayer(1001, "Bob", 24.5, 64, 24.5)

	loop.register <- p1
	loop.register <- p2
	loop.drainRegistrations()

	// (1) both players are in the store with id==entityID and a non-nil playerEntity.
	for _, p := range []*tickPlayer{p1, p2} {
		if p.playerEntity == nil {
			t.Fatalf("player %q has nil playerEntity after register", p.name)
		}
		e, ok := loop.entities.get(p.entityID)
		if !ok {
			t.Fatalf("player %q (id %d) not in store after register", p.name, p.entityID)
		}
		if e.id != p.entityID {
			t.Fatalf("store entity id = %d, want player entityID %d", e.id, p.entityID)
		}
		if e.uuid != p.uuid {
			t.Fatalf("store entity uuid mismatch for %q", p.name)
		}
	}

	// (2) move p1 and run the per-tick sync; the store Entity must follow.
	p1.x, p1.y, p1.z = 100.5, 70, -40.5
	loop.syncPlayerEntities()
	e1, _ := loop.entities.get(p1.entityID)
	if e1.x != 100.5 || e1.y != 70 || e1.z != -40.5 {
		t.Fatalf("synced entity pos = (%v,%v,%v), want (100.5,70,-40.5)", e1.x, e1.y, e1.z)
	}

	// (3) leave removes the Entity from the store.
	loop.unregister <- p2.client
	loop.drainRegistrations()
	if _, ok := loop.entities.get(p2.entityID); ok {
		t.Fatalf("player %q still in store after leave", p2.name)
	}
}

// TestPlayerVisibilityTabBroadcast proves the bidirectional tab-list sync: when a second
// player joins, the first receives a PlayerInfoUpdate(ADD) for the joiner, and on leave a
// PlayerInfoRemove is broadcast.
func TestPlayerVisibilityTabBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	p1 := visPlayer(1000, "Alice", 8.5, 64, 8.5)
	loop.register <- p1
	loop.drainRegistrations()

	p2 := visPlayer(1001, "Bob", 24.5, 64, 24.5)
	loop.register <- p2
	loop.drainRegistrations()

	// p1 must have received a PlayerInfoUpdate ADD for the joiner p2.
	ps := drainPackets(p1.client)
	if countID(ps, packetid.ClientboundPlayerInfoUpdate) == 0 {
		t.Fatalf("p1 did not receive a PlayerInfoUpdate(ADD) for the joining p2")
	}

	// On p2 leave, a PlayerInfoRemove must be broadcast to p1.
	p1.client = captureClient(64)
	loop.clientIndex[p1.client] = p1
	loop.unregister <- p2.client
	loop.drainRegistrations()
	ps = drainPackets(p1.client)
	if countID(ps, packetid.ClientboundPlayerInfoRemove) == 0 {
		t.Fatalf("p1 did not receive a PlayerInfoRemove for the leaving p2")
	}
}
