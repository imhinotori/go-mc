package chunkticket

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// centerOutRingSquare reproduces the (2r+1)^2 column square the existing loader streams
// for a player (server/world_stream.go centerOutRing). Kept local to avoid importing the
// server package (which would create a cycle); the shape is what matters for the
// observe-equivalence check.
func centerOutRingSquare(center level.ChunkPos, r int) map[level.ChunkPos]bool {
	m := make(map[level.ChunkPos]bool)
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			m[level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}] = true
		}
	}
	return m
}

// TestObserveMatchesRing pins that the columns the ticket model marks with a
// PLAYER_LOADING ticket (level == PLAYER_TICKET_LEVEL) are EXACTLY the (2r+1)^2 square
// the existing view-distance loader streams. This is the behaviour-neutral guarantee for
// making the ticket model authoritative. CITE: PlayerTicketTracker.updateViewDistance
// (ticket on every column with Chebyshev distance <= viewDistance).
func TestObserveMatchesRing(t *testing.T) {
	for _, r := range []int{2, 3, 5, 10} {
		center := level.ChunkPos{3, -7}
		dm := NewDistanceManager(10)
		dm.UpdateViewDistance(r)
		dm.AddPlayer(center)
		dm.RunAllUpdates()

		want := centerOutRingSquare(center, r)
		got := make(map[level.ChunkPos]bool)
		for _, p := range dm.LoadedChunks() {
			if dm.ChunkLevelAt(p) == playerTicketLevel {
				got[p] = true
			}
		}
		if len(got) != len(want) {
			t.Fatalf("r=%d: ticketed columns = %d, want %d", r, len(got), len(want))
		}
		for p := range want {
			if !got[p] {
				t.Fatalf("r=%d: ring column %v missing from ticket model", r, p)
			}
		}
		// And every loaded column beyond the ticketed square is the generation border
		// (level 32..MAX_LEVEL), i.e. loaded but not player-ticketed -- vanilla keeps it.
		for _, p := range dm.LoadedChunks() {
			if !want[p] && dm.ChunkLevelAt(p) <= playerTicketLevel {
				t.Fatalf("r=%d: column %v loaded at <=%d but outside the ring", r, p, playerTicketLevel)
			}
		}
	}
}

// TestObserveLoadedSquareTwoPlayers pins the union behaviour for two players.
func TestObserveLoadedSquareTwoPlayers(t *testing.T) {
	centers := []level.ChunkPos{{0, 0}, {100, 100}}
	got := ObserveLoadedSquare(centers, 2, 10)
	// Both player columns must be loaded and player-ticketed.
	if !got[level.ChunkPos{0, 0}] || !got[level.ChunkPos{100, 100}] {
		t.Fatal("both player columns should be loaded")
	}
	// A column far from both is not loaded.
	if got[level.ChunkPos{50, 50}] {
		t.Fatal("midpoint column should not be loaded")
	}
}
