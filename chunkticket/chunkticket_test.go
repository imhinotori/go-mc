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
	if RadiusAroundFullChunk != 11 {
		t.Fatalf("RadiusAroundFullChunk = %d, want 11", RadiusAroundFullChunk)
	}
	if MaxLevel != 44 {
		t.Fatalf("MaxLevel = %d, want 44", MaxLevel)
	}
	cases := []struct {
		lvl  int
		want FullChunkStatus
	}{
		{0, EntityTicking}, {31, EntityTicking},
		{32, BlockTicking},
		{33, Full},
		{34, Inaccessible}, {44, Inaccessible},
	}
	for _, c := range cases {
		if got := FullStatus(c.lvl); got != c.want {
			t.Errorf("FullStatus(%d) = %d, want %d", c.lvl, got, c.want)
		}
	}
	// byStatus round trip. CITE: ChunkLevel.byStatus(FullChunkStatus).
	if ByStatus(EntityTicking) != 31 || ByStatus(BlockTicking) != 32 || ByStatus(Full) != 33 || ByStatus(Inaccessible) != 44 {
		t.Fatal("byStatus mapping wrong")
	}
	if !IsEntityTicking(31) || IsEntityTicking(32) {
		t.Error("isEntityTicking threshold wrong")
	}
	if !IsBlockTicking(32) || IsBlockTicking(33) {
		t.Error("isBlockTicking threshold wrong")
	}
	if !IsLoaded(44) || IsLoaded(45) {
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

// TestGenerationStatusMapping pins the level -> ChunkStatus mapping for every distance
// from 0..RADIUS_AROUND_FULL_CHUNK(11) and the not-generated boundary beyond it.
// CITE: ChunkLevel.generationStatus / getStatusAroundFullChunk +
// ChunkPyramid.GENERATION_PYRAMID accumulated-dependency table.
func TestGenerationStatusMapping(t *testing.T) {
	// distance 0 (level <= 33) => FULL.
	if s, ok := GenerationStatus(33); !ok || s != level.StatusFull {
		t.Errorf("gen status @33 = %v ok=%v, want full", s, ok)
	}
	if s, ok := GenerationStatus(30); !ok || s != level.StatusFull {
		t.Errorf("gen status @30 (<=full) = %v ok=%v, want full", s, ok)
	}
	// distance 1..11 => the accumulated-dependency table.
	distCases := []struct {
		dist int
		want level.ChunkStatus
	}{
		{1, level.StatusInitializeLight},
		{2, level.StatusCarvers},
		{3, level.StatusBiomes},
		{4, level.StatusStructureStarts},
		{5, level.StatusStructureStarts},
		{6, level.StatusStructureStarts},
		{7, level.StatusStructureStarts},
		{8, level.StatusStructureStarts},
		{9, level.StatusStructureStarts},
		{10, level.StatusStructureStarts},
		{11, level.StatusStructureStarts},
	}
	for _, c := range distCases {
		lvl := FullChunkLevel + c.dist
		s, ok := GenerationStatus(lvl)
		if !ok || s != c.want {
			t.Errorf("gen status @%d (dist=%d) = %v ok=%v, want %v", lvl, c.dist, s, ok, c.want)
		}
	}
	// distance > RADIUS_AROUND_FULL_CHUNK (11) => not generated. The boundary is
	// level 45 (dist 12); level 44 (dist 11) is the last generated ring.
	if s, ok := GenerationStatus(MaxLevel); !ok || s != level.StatusStructureStarts {
		t.Errorf("gen status @%d (dist=%d, boundary) = %v ok=%v, want structure_starts", MaxLevel, RadiusAroundFullChunk, s, ok)
	}
	if _, ok := GenerationStatus(MaxLevel + 1); ok { // dist 12 > RADIUS 11
		t.Errorf("gen status @%d (dist=%d) should be not-generated", MaxLevel+1, RadiusAroundFullChunk+1)
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

// TestPlayerTicketTrackerMaxDistance pins the 26.2 PlayerTicketTracker boundary:
// the FixedPlayerDistanceChunkTracker is constructed with the cap (32), maxDistance is
// 32, ChunkTracker levelCount = cap+2 = 34 (the default-return value), and setLevel drops
// entries > 32. CITE: DistanceManager ctor `new PlayerTicketTracker(this, 32)` +
// FixedPlayerDistanceChunkTracker ctor super(maxDistance+2, ...) and the chunks
// defaultReturnValue(maxDistance+2).
func TestPlayerTicketTrackerMaxDistance(t *testing.T) {
	dm := NewDistanceManager(10)
	p := dm.playerTicket

	// Stored maxDistance == playerViewCap == 32.
	if p.maxDistance != playerViewCap {
		t.Fatalf("maxDistance = %d, want %d (playerViewCap)", p.maxDistance, playerViewCap)
	}
	if p.maxDistance != 32 {
		t.Fatalf("maxDistance = %d, want 32 (26.2 cap)", p.maxDistance)
	}
	// ChunkTracker levelCount = cap+2 = 34.
	if p.tracker.graph.levelCount != playerViewCap+2 {
		t.Fatalf("tracker levelCount = %d, want %d (cap+2)", p.tracker.graph.levelCount, playerViewCap+2)
	}
	if p.tracker.graph.levelCount != 34 {
		t.Fatalf("tracker levelCount = %d, want 34", p.tracker.graph.levelCount)
	}
	// Default getLevel value for an untracked chunk == maxDistance+2 = 34.
	const sentinel int64 = 0x0a0b0c0d0e0f1011
	if got := p.getLevel(sentinel); got != p.maxDistance+2 {
		t.Fatalf("getLevel default = %d, want %d (maxDistance+2)", got, p.maxDistance+2)
	}
	if got := p.getLevel(sentinel); got != 34 {
		t.Fatalf("getLevel default = %d, want 34", got)
	}

	// setLevel at the cap (32) is retained.
	ring32 := packChunk(32, 0)
	p.setLevel(ring32, 32)
	if v, ok := p.chunks[ring32]; !ok || v != 32 {
		t.Fatalf("chunks[ring32] = (%d, %v), want (32, true)", v, ok)
	}
	// getLevel still returns 32 for the retained entry (not the default).
	if got := p.getLevel(ring32); got != 32 {
		t.Errorf("getLevel(ring32) = %d, want 32", got)
	}

	// setLevel above the cap (33) is dropped from the map; getLevel falls back to 34.
	ring33 := packChunk(33, 0)
	p.setLevel(ring33, 33)
	if _, ok := p.chunks[ring33]; ok {
		t.Fatalf("chunks[ring33] should be removed when level > maxDistance")
	}
	if got := p.getLevel(ring33); got != 34 {
		t.Errorf("getLevel(ring33) after removal = %d, want 34 (default)", got)
	}
}

// TestPlayerTicketTrackerCapTickets pins that a chunk at the cap (Chebyshev distance 32
// from the player) carries PLAYER_LOADING when viewDistance >= 32, and a chunk just
// beyond the cap (distance 33) does not. CITE: PlayerTicketTracker.haveTicketFor +
// PlayerTicketTracker.updateViewDistance.
func TestPlayerTicketTrackerCapTickets(t *testing.T) {
	dm := NewDistanceManager(10)
	dm.UpdateViewDistance(32) // the new max
	dm.AddPlayer(level.ChunkPos{0, 0})
	dm.RunAllUpdates()

	// Ring-32 chunk: retained in p.chunks and ticketed.
	ring32 := packChunk(32, 0)
	if v, ok := dm.playerTicket.chunks[ring32]; !ok || v != 32 {
		t.Fatalf("chunks[ring32] = (%d, %v), want (32, true)", v, ok)
	}
	if !hasPlayerLoadingTicket(dm, ring32) {
		t.Error("ring-32 chunk missing PLAYER_LOADING ticket at viewDistance=32")
	}

	// Ring-33 chunk: not retained (33 > maxDistance 32) and therefore not ticketed.
	ring33 := packChunk(33, 0)
	if _, ok := dm.playerTicket.chunks[ring33]; ok {
		t.Fatal("chunks[ring33] should be absent (33 > maxDistance 32)")
	}
	if hasPlayerLoadingTicket(dm, ring33) {
		t.Error("ring-33 chunk should not have PLAYER_LOADING ticket (beyond cap)")
	}
	// The default-return getLevel for an untracked chunk is cap+2 = 34.
	if got := dm.playerTicket.getLevel(ring33); got != 34 {
		t.Errorf("getLevel(ring33) default = %d, want 34", got)
	}
}

// hasPlayerLoadingTicket reports whether the chunk at key currently holds a PLAYER_LOADING
// ticket (same-type+level match; the tracker applies at PLAYER_TICKET_LEVEL).
func hasPlayerLoadingTicket(dm *DistanceManager, key int64) bool {
	for _, tk := range dm.storage.getTickets(key) {
		if tk.Type == PlayerLoading && tk.Level == playerTicketLevel {
			return true
		}
	}
	return false
}
