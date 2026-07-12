package chunkticket

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// TestChunkLevelConstants pins the bytecode-verified ChunkLevel constants and the
// level -> FullChunkStatus thresholds. CITE: ChunkLevel.fullStatus / byStatus.
func TestChunkLevelConstants(t *testing.T) {
	if FullChunkLevel != 33 || BlockTickingLevel != 32 || EntityTickingLevel != 31 {
		t.Fatalf("level constants wrong: %d %d %d", FullChunkLevel, BlockTickingLevel, EntityTickingLevel)
	}
	if MaxLevel != 41 {
		t.Fatalf("MaxLevel = %d, want 41", MaxLevel)
	}
	cases := []struct {
		lvl  int
		want FullChunkStatus
	}{
		{0, EntityTicking}, {31, EntityTicking},
		{32, BlockTicking},
		{33, Full},
		{34, Inaccessible}, {41, Inaccessible},
	}
	for _, c := range cases {
		if got := FullStatus(c.lvl); got != c.want {
			t.Errorf("FullStatus(%d) = %d, want %d", c.lvl, got, c.want)
		}
	}
	// byStatus round trip. CITE: ChunkLevel.byStatus(FullChunkStatus).
	if ByStatus(EntityTicking) != 31 || ByStatus(BlockTicking) != 32 || ByStatus(Full) != 33 || ByStatus(Inaccessible) != 41 {
		t.Fatal("byStatus mapping wrong")
	}
	if !IsEntityTicking(31) || IsEntityTicking(32) {
		t.Error("isEntityTicking threshold wrong")
	}
	if !IsBlockTicking(32) || IsBlockTicking(33) {
		t.Error("isBlockTicking threshold wrong")
	}
	if !IsLoaded(41) || IsLoaded(42) {
		t.Error("isLoaded threshold wrong")
	}
}

// TestTicketTypeTable pins the nine registered ticket types' timeout+flags.
// CITE: TicketType.<clinit>.
func TestTicketTypeTable(t *testing.T) {
	check := func(tt TicketType, timeout int64, load, sim, persist bool) {
		if tt.Timeout != timeout {
			t.Errorf("%s timeout=%d want %d", tt.Name, tt.Timeout, timeout)
		}
		if tt.DoesLoad() != load || tt.DoesSimulate() != sim || tt.Persist() != persist {
			t.Errorf("%s flags load=%v sim=%v persist=%v", tt.Name, tt.DoesLoad(), tt.DoesSimulate(), tt.Persist())
		}
	}
	check(PlayerSpawn, 20, true, false, false)
	check(SpawnSearch, 1, true, false, false)
	check(Dragon, 0, true, true, false)
	check(PlayerLoading, 0, true, false, false)
	check(PlayerSimulation, 0, false, true, false)
	check(Forced, 0, true, true, true)
	check(Portal, 300, true, true, true)
	check(EnderPearl, 40, true, true, false)
	check(Unknown, 1, true, false, false)

	// hasTimeout == timeout != 0. CITE: TicketType.hasTimeout.
	if PlayerLoading.HasTimeout() || !PlayerSpawn.HasTimeout() {
		t.Error("hasTimeout wrong")
	}
}

// levelOf runs the manager to a fixed point and returns the loading level of a column.
func levelOf(dm *DistanceManager, x, z int32) int {
	return dm.ChunkLevelAt(level.ChunkPos{x, z})
}

// TestPlayerTicketPropagation pins that a player produces a PLAYER_LOADING ticket at
// level 31 in its column, propagating outward +1 per ring, and that the loaded set
// matches a view-distance-N square. CITE: PlayerTicketTracker + ChunkTracker propagation.
func TestPlayerTicketPropagation(t *testing.T) {
	dm := NewDistanceManager(10)
	const viewDistance = 3
	dm.UpdateViewDistance(viewDistance)
	dm.AddPlayer(level.ChunkPos{0, 0})
	dm.RunAllUpdates()

	// Center column: the PLAYER_LOADING ticket sits at PLAYER_TICKET_LEVEL(31), so the
	// loading level is 31 (its own ticket). CITE: DistanceManager.PLAYER_TICKET_LEVEL.
	if got := levelOf(dm, 0, 0); got != playerTicketLevel {
		t.Fatalf("center loading level = %d, want %d", got, playerTicketLevel)
	}

	// The PLAYER_LOADING ticket is placed on every chunk within viewDistance of the
	// player (Chebyshev). Each such ticket source is level 31, so a chunk at ring r
	// (0..viewDistance) still has a ticket at 31, and beyond that the propagated level
	// grows +1 per ring from the nearest ticketed chunk (edge at distance viewDistance).
	// Verify a ring-1 chunk that itself carries a PLAYER_LOADING ticket is at 31.
	if got := levelOf(dm, 1, 0); got != playerTicketLevel {
		t.Fatalf("ring-1 loading level = %d, want %d (should carry its own ticket)", got, playerTicketLevel)
	}
	if got := levelOf(dm, viewDistance, viewDistance); got != playerTicketLevel {
		t.Fatalf("corner-of-view loading level = %d, want %d", got, playerTicketLevel)
	}

	// One chunk beyond the ticketed square: propagated 31+1 = 32 (Chebyshev distance 1
	// from the nearest ticketed chunk at the view edge).
	if got := levelOf(dm, viewDistance+1, 0); got != playerTicketLevel+1 {
		t.Fatalf("just-outside loading level = %d, want %d", got, playerTicketLevel+1)
	}
	if got := levelOf(dm, viewDistance+2, 0); got != playerTicketLevel+2 {
		t.Fatalf("two-outside loading level = %d, want %d", got, playerTicketLevel+2)
	}

	// The ticketed square is (2*viewDistance+1)^2 columns, all at level 31.
	ticketed := 0
	for _, p := range dm.LoadedChunks() {
		if levelOf(dm, p[0], p[1]) == playerTicketLevel {
			ticketed++
		}
	}
	want := (2*viewDistance + 1) * (2*viewDistance + 1)
	if ticketed != want {
		t.Fatalf("ticketed columns = %d, want %d", ticketed, want)
	}
}

// TestTicketRemovalDropsLevel pins that removing the player's tickets restores the
// column to unloaded. CITE: DistanceManager.removePlayer + TicketStorage.removeTicket.
func TestTicketRemovalDropsLevel(t *testing.T) {
	dm := NewDistanceManager(10)
	dm.UpdateViewDistance(2)
	dm.AddPlayer(level.ChunkPos{5, 5})
	dm.RunAllUpdates()
	if levelOf(dm, 5, 5) != playerTicketLevel {
		t.Fatalf("pre-remove level = %d", levelOf(dm, 5, 5))
	}
	dm.RemovePlayer(level.ChunkPos{5, 5})
	dm.RunAllUpdates()
	if got := levelOf(dm, 5, 5); IsLoaded(got) {
		t.Fatalf("post-remove level = %d, want unloaded (> %d)", got, MaxLevel)
	}
}

// TestGenericTicketPropagation pins a raw ticket at an arbitrary level propagating +1
// per Chebyshev ring. CITE: ChunkTracker.computeLevelFromNeighbor (level+1).
func TestGenericTicketPropagation(t *testing.T) {
	dm := NewDistanceManager(10)
	dm.AddTicket(Forced, level.ChunkPos{0, 0}, 20)
	dm.RunAllUpdates()
	if got := levelOf(dm, 0, 0); got != 20 {
		t.Fatalf("source level = %d, want 20", got)
	}
	if got := levelOf(dm, 1, 0); got != 21 {
		t.Fatalf("ring1 level = %d, want 21", got)
	}
	if got := levelOf(dm, 1, 1); got != 21 { // Chebyshev distance 1 diagonally
		t.Fatalf("diag ring1 level = %d, want 21", got)
	}
	if got := levelOf(dm, 5, 0); got != 25 {
		t.Fatalf("ring5 level = %d, want 25", got)
	}
	// Beyond MAX_LEVEL the propagation stops (levels cap at levelCount-1 == MaxLevel+1).
	if got := levelOf(dm, 30, 0); IsLoaded(got) {
		t.Fatalf("far chunk level = %d, expected unloaded", got)
	}
}

// TestTimedTicketFade pins that a timed ticket (PLAYER_SPAWN, timeout 20) fades after
// its timeout, dropping the column. CITE: Ticket.decreaseTicksLeft/isTimedOut +
// TicketStorage.purgeStaleTickets.
func TestTimedTicketFade(t *testing.T) {
	dm := NewDistanceManager(10)
	// PLAYER_SPAWN loads (FLAG_LOADING) with timeout 20.
	dm.AddTicket(PlayerSpawn, level.ChunkPos{0, 0}, 22)
	dm.RunAllUpdates()
	if levelOf(dm, 0, 0) != 22 {
		t.Fatalf("initial level = %d, want 22", levelOf(dm, 0, 0))
	}
	// ticksLeft starts at timeout (20) and decrements each tick; isTimedOut is
	// ticksLeft < 0. So it survives 20 decrements (20..0) and times out on the 21st
	// (ticksLeft = -1). CITE: Ticket.decreaseTicksLeft / isTimedOut.
	for i := 0; i < 20; i++ {
		dm.TickTimedTickets()
		dm.RunAllUpdates()
		if levelOf(dm, 0, 0) != 22 {
			t.Fatalf("after %d ticks level = %d, want still 22", i+1, levelOf(dm, 0, 0))
		}
	}
	dm.TickTimedTickets() // 21st decrement -> ticksLeft = -1 -> timed out
	dm.RunAllUpdates()
	if got := levelOf(dm, 0, 0); IsLoaded(got) {
		t.Fatalf("after timeout level = %d, want unloaded", got)
	}
}

// TestSimulationVsLoading pins that a load-only ticket (PLAYER_SPAWN) raises the loading
// level but NOT the simulation level, and a simulating ticket raises both. CITE:
// TicketStorage.getLowestTicket(simulation) / getTicketLevelAt.
func TestSimulationVsLoading(t *testing.T) {
	dm := NewDistanceManager(10)
	dm.AddTicket(PlayerSpawn, level.ChunkPos{0, 0}, 20) // FLAG_LOADING only
	dm.RunAllUpdates()
	if dm.ChunkLevelAt(level.ChunkPos{0, 0}) != 20 {
		t.Fatalf("loading level = %d, want 20", dm.ChunkLevelAt(level.ChunkPos{0, 0}))
	}
	// Simulation tracker default level is FULL(33); a load-only ticket must not lower it.
	if got := dm.SimulationLevelAt(level.ChunkPos{0, 0}); got != FullChunkLevel {
		t.Fatalf("simulation level = %d, want default %d (load-only ticket)", got, FullChunkLevel)
	}
	// A simulating ticket at 20 lowers the simulation level too.
	dm.AddTicket(Forced, level.ChunkPos{0, 0}, 20) // FLAG_SIMULATION
	dm.RunAllUpdates()
	if got := dm.SimulationLevelAt(level.ChunkPos{0, 0}); got != 20 {
		t.Fatalf("simulation level after sim ticket = %d, want 20", got)
	}
	if !dm.InEntityTickingRange(level.ChunkPos{0, 0}) {
		t.Error("expected entity-ticking at sim level 20")
	}
}

// TestGenerationStatusMapping pins the level -> ChunkStatus mapping.
// CITE: ChunkLevel.generationStatus / getStatusAroundFullChunk.
func TestGenerationStatusMapping(t *testing.T) {
	if s, ok := GenerationStatus(33); !ok || s != level.StatusFull {
		t.Errorf("gen status @33 = %v ok=%v, want full", s, ok)
	}
	if s, ok := GenerationStatus(30); !ok || s != level.StatusFull {
		t.Errorf("gen status @30 (<=full) = %v ok=%v, want full", s, ok)
	}
	if s, ok := GenerationStatus(34); !ok || s != level.StatusFeatures {
		t.Errorf("gen status @34 (dist1) = %v ok=%v, want features", s, ok)
	}
	if _, ok := GenerationStatus(42); ok { // dist 9 > RADIUS 8
		t.Error("gen status @42 should be not-generated")
	}
}

// TestSimulationDistanceAffectsPlayerTicket pins that the PLAYER_SIMULATION ticket level
// follows getPlayerTicketLevel = max(0, 31 - simDistance). CITE: DistanceManager.
func TestSimulationDistanceAffectsPlayerTicket(t *testing.T) {
	dm := NewDistanceManager(10)
	if dm.getPlayerTicketLevel() != 31-10 {
		t.Fatalf("player ticket level = %d, want %d", dm.getPlayerTicketLevel(), 31-10)
	}
	dm.UpdateViewDistance(2)
	dm.AddPlayer(level.ChunkPos{0, 0})
	dm.RunAllUpdates()
	// Simulation level at the player column is the PLAYER_SIMULATION ticket level (21).
	if got := dm.SimulationLevelAt(level.ChunkPos{0, 0}); got != 21 {
		t.Fatalf("sim level at player = %d, want 21", got)
	}
	dm.UpdateSimulationDistance(31) // ticket level -> max(0, 31-31) = 0
	dm.RunAllUpdates()
	if got := dm.SimulationLevelAt(level.ChunkPos{0, 0}); got != 0 {
		t.Fatalf("sim level after simDist=31 = %d, want 0", got)
	}
}
