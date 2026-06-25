package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
)

// inventory_join_test.go covers GAMEPLAY-03: the authoritative ContainerSetContent is sent
// exactly once on the first tick after a player registers, guarded by the per-player
// bootstrapped flag, so the client's window 0 is populated on join.

// joinInvPlayer registers a fresh player (bootstrapped=false) with a capturing client.
func joinInvPlayer(loop *TickLoop, entityID int32) *tickPlayer {
	p := &tickPlayer{
		client:     captureClient(64),
		entityID:   entityID,
		center:     level.ChunkPos{0, 0},
		viewDist:   serverViewDistance,
		sentChunks: map[level.ChunkPos]bool{},
		secs:       overworldSections,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestInventoryJoinSync asserts: bootstrapped is initially false; the first
// syncJoinInventories sends exactly one ContainerSetContent and flips bootstrapped true; a
// second pass sends nothing more.
func TestInventoryJoinSync(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := joinInvPlayer(loop, 1)

	if p.bootstrapped {
		t.Fatal("a freshly registered player must start unbootstrapped")
	}

	loop.syncJoinInventories()
	if !p.bootstrapped {
		t.Fatal("syncJoinInventories did not set bootstrapped")
	}

	// Second pass must be a no-op (the flag guards the re-send).
	loop.syncJoinInventories()

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundContainerSetContent); n != 1 {
		t.Fatalf("ContainerSetContent sent %d times, want exactly 1", n)
	}
}
