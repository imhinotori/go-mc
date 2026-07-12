package chunkticket

import "github.com/imhinotori/sulfur/level"

// playerTicketLevel mirrors DistanceManager.PLAYER_TICKET_LEVEL =
// ChunkLevel.byStatus(FullChunkStatus.ENTITY_TICKING) = 31. CITE: DistanceManager.<clinit>.
const playerTicketLevel = EntityTickingLevel // 31

// loadingBackend / simulationBackend record the propagated per-chunk ticket level for
// the loading and simulation trackers respectively. In vanilla the loading tracker
// reads the level from the ChunkHolder and the simulation tracker from a Long2ByteMap;
// since this subsystem observes alongside the existing loader (no ChunkHolder graph yet)
// both keep their own Long2ByteMap-equivalent, exactly like SimulationChunkTracker.
// CITE: SimulationChunkTracker.getLevel/setLevel (Long2ByteMap, default 33 / cap 34).

type loadingBackend struct {
	storage *ticketStorage
	levels  map[int64]int // ChunkHolder ticket levels; missing == MaxLevel+1
}

func (b *loadingBackend) getLevelFromSource(key int64) int {
	return b.storage.getTicketLevelAt(key, false)
}
func (b *loadingBackend) getLevel(key int64) int {
	if v, ok := b.levels[key]; ok {
		return v
	}
	return MaxLevel + 1 // LoadingChunkTracker.MAX_LEVEL == ChunkLevel.MAX_LEVEL + 1
}
func (b *loadingBackend) setLevel(key int64, lvl int) {
	if lvl > MaxLevel { // cap identical to LoadingChunkTracker/ChunkHolder unloaded level
		delete(b.levels, key)
		return
	}
	b.levels[key] = lvl
}

type simulationBackend struct {
	storage *ticketStorage
	levels  map[int64]int // default 33, values >= 33 removed (SimulationChunkTracker)
}

func (b *simulationBackend) getLevelFromSource(key int64) int {
	return b.storage.getTicketLevelAt(key, true)
}
func (b *simulationBackend) getLevel(key int64) int {
	if v, ok := b.levels[key]; ok {
		return v
	}
	return FullChunkLevel // SimulationChunkTracker default return value == 33
}
func (b *simulationBackend) setLevel(key int64, lvl int) {
	// CITE: SimulationChunkTracker.setLevel: level >= 33 removes, else stores.
	if lvl >= FullChunkLevel {
		delete(b.levels, key)
		return
	}
	b.levels[key] = lvl
}

// DistanceManager is a 1:1-behaviour port of net.minecraft.server.level.DistanceManager
// restricted to the ticket -> level model (the async ThrottlingChunkTaskDispatcher and
// ChunkHolder scheduling are the perf layer and are replaced by synchronous
// runAllUpdates, which yields the identical converged level set). CITE: DistanceManager.
type DistanceManager struct {
	storage           *ticketStorage
	loading           *chunkTracker
	loadingBackend    *loadingBackend
	simulation        *chunkTracker
	simulationBackend *simulationBackend

	playersPerChunk    map[int64]int // count of players standing in a chunk column
	playerTicket       *playerTicketTracker
	simulationDistance int
}

// NewDistanceManager builds the ticket subsystem. simulationDistance is the server
// simulation distance (chunks). CITE: DistanceManager constructor.
func NewDistanceManager(simulationDistance int) *DistanceManager {
	st := newTicketStorage()
	lb := &loadingBackend{storage: st, levels: make(map[int64]int)}
	sb := &simulationBackend{storage: st, levels: make(map[int64]int)}
	dm := &DistanceManager{
		storage:            st,
		loadingBackend:     lb,
		simulationBackend:  sb,
		playersPerChunk:    make(map[int64]int),
		simulationDistance: simulationDistance,
	}
	// LoadingChunkTracker level count = ChunkLevel.MAX_LEVEL + 2, i.e. levelCount passed
	// as MAX_LEVEL+1 to ChunkTracker which stores it; here MaxLevel+2 so index MaxLevel+1
	// (the unloaded level) is representable. CITE: LoadingChunkTracker ctor (MAX_LEVEL+1),
	// ChunkTracker(levelCount=MAX_LEVEL+1... ) - vanilla uses MAX_LEVEL+1 as levelCount.
	dm.loading = newChunkTracker(MaxLevel+2, lb)
	// SimulationChunkTracker level count = 34 (FULL_CHUNK_LEVEL+1). CITE:
	// SimulationChunkTracker ctor super(34, 16, 256).
	dm.simulation = newChunkTracker(FullChunkLevel+1, sb)
	dm.playerTicket = newPlayerTicketTracker(dm)
	dm.storage.setLoadingChunkUpdatedListener(func(key int64, lvl int, dec bool) {
		dm.loading.update(key, lvl, dec)
	})
	dm.storage.setSimulationChunkUpdatedListener(func(key int64, lvl int, dec bool) {
		dm.simulation.update(key, lvl, dec)
	})
	return dm
}

// getPlayerTicketLevel mirrors DistanceManager.getPlayerTicketLevel():
// max(0, byStatus(ENTITY_TICKING) - simulationDistance) = max(0, 31 - simDist).
func (dm *DistanceManager) getPlayerTicketLevel() int {
	v := EntityTickingLevel - dm.simulationDistance
	if v < 0 {
		return 0
	}
	return v
}

// AddPlayer mirrors DistanceManager.addPlayer(SectionPos, ServerPlayer) reduced to the
// chunk key: bumps the per-chunk player count, notifies the player-loading tracker, and
// adds a PLAYER_SIMULATION ticket at the player ticket level. CITE: DistanceManager.addPlayer.
func (dm *DistanceManager) AddPlayer(pos level.ChunkPos) {
	key := packChunk(pos[0], pos[1])
	dm.playersPerChunk[key]++
	dm.playerTicket.update(key, 0, true)
	dm.storage.addTicket(key, NewTicket(PlayerSimulation, dm.getPlayerTicketLevel()))
}

// RemovePlayer mirrors DistanceManager.removePlayer. CITE: DistanceManager.removePlayer.
func (dm *DistanceManager) RemovePlayer(pos level.ChunkPos) {
	key := packChunk(pos[0], pos[1])
	if dm.playersPerChunk[key] > 0 {
		dm.playersPerChunk[key]--
	}
	if dm.playersPerChunk[key] == 0 {
		delete(dm.playersPerChunk, key)
		dm.playerTicket.update(key, maxInt32Val, false)
		dm.storage.removeTicket(key, NewTicket(PlayerSimulation, dm.getPlayerTicketLevel()))
	}
}

const maxInt32Val = 2147483647 // Integer.MAX_VALUE, the "no player" level used by the trackers

// havePlayer mirrors FixedPlayerDistanceChunkTracker.havePlayer(long).
func (dm *DistanceManager) havePlayer(key int64) bool { return dm.playersPerChunk[key] > 0 }

// UpdateViewDistance mirrors DistanceManager.updatePlayerTickets(int viewDistance).
// CITE: DistanceManager.updatePlayerTickets -> PlayerTicketTracker.updateViewDistance.
func (dm *DistanceManager) UpdateViewDistance(viewDistance int) {
	dm.playerTicket.updateViewDistance(viewDistance)
}

// UpdateSimulationDistance mirrors DistanceManager.updateSimulationDistance(int).
// CITE: DistanceManager.updateSimulationDistance.
func (dm *DistanceManager) UpdateSimulationDistance(simDist int) {
	if simDist == dm.simulationDistance {
		return
	}
	dm.simulationDistance = simDist
	dm.storage.replaceTicketLevelOfType(dm.getPlayerTicketLevel(), PlayerSimulation)
}

// AddTicket / RemoveTicket expose the generic ticket API. CITE: DistanceManager.addTicket.
func (dm *DistanceManager) AddTicket(t TicketType, pos level.ChunkPos, level int) {
	dm.storage.addTicket(packChunk(pos[0], pos[1]), NewTicket(t, level))
}
func (dm *DistanceManager) RemoveTicket(t TicketType, pos level.ChunkPos, level int) {
	dm.storage.removeTicket(packChunk(pos[0], pos[1]), NewTicket(t, level))
}

// AddTicketWithRadius / RemoveTicketWithRadius: level = byStatus(FULL) - radius.
func (dm *DistanceManager) AddTicketWithRadius(t TicketType, pos level.ChunkPos, radius int) {
	dm.storage.addTicketWithRadius(t, packChunk(pos[0], pos[1]), radius)
}

// RunAllUpdates mirrors DistanceManager.runAllUpdates(ChunkMap) collapsed to the
// synchronous case: run the player-ticket tracker, then drain both level trackers to a
// fixed point. Returns whether any level changed (best-effort; callers observe levels).
// CITE: DistanceManager.runAllUpdates + LoadingChunkTracker.runDistanceUpdates +
// SimulationChunkTracker.runAllUpdates.
func (dm *DistanceManager) RunAllUpdates() {
	dm.playerTicket.runAllUpdates()
	dm.simulation.runUpdates(maxInt32Val)
	dm.loading.runUpdates(maxInt32Val)
}

// TickTimedTickets fades timed tickets by one tick and re-levels. Call once per server
// tick. CITE: Ticket.decreaseTicksLeft/isTimedOut driven from the chunk scheduler.
func (dm *DistanceManager) TickTimedTickets() {
	dm.storage.purgeStaleTickets()
}

// ChunkLevelAt returns the propagated LOADING ticket level of a chunk (post-RunAllUpdates).
// This is the authoritative per-chunk level the loader observes. CITE:
// DistanceManager.getChunkLevel(long, false).
func (dm *DistanceManager) ChunkLevelAt(pos level.ChunkPos) int {
	return dm.loadingBackend.getLevel(packChunk(pos[0], pos[1]))
}

// SimulationLevelAt returns the propagated SIMULATION level of a chunk.
func (dm *DistanceManager) SimulationLevelAt(pos level.ChunkPos) int {
	return dm.simulationBackend.getLevel(packChunk(pos[0], pos[1]))
}

// FullStatusAt maps the loading level to a FullChunkStatus. CITE: ChunkLevel.fullStatus.
func (dm *DistanceManager) FullStatusAt(pos level.ChunkPos) FullChunkStatus {
	return FullStatus(dm.ChunkLevelAt(pos))
}

// InEntityTickingRange / InBlockTickingRange mirror DistanceManager.inEntityTickingRange
// / inBlockTickingRange over the simulation tracker. CITE: DistanceManager.
func (dm *DistanceManager) InEntityTickingRange(pos level.ChunkPos) bool {
	return IsEntityTicking(dm.SimulationLevelAt(pos))
}
func (dm *DistanceManager) InBlockTickingRange(pos level.ChunkPos) bool {
	return IsBlockTicking(dm.SimulationLevelAt(pos))
}

// LoadedChunks returns every chunk key whose loading level is loaded (level <= MAX_LEVEL),
// for the observe-only comparison against the existing view-distance loader.
func (dm *DistanceManager) LoadedChunks() []level.ChunkPos {
	out := make([]level.ChunkPos, 0, len(dm.loadingBackend.levels))
	for key, lvl := range dm.loadingBackend.levels {
		if IsLoaded(lvl) {
			out = append(out, level.ChunkPos{unpackX(key), unpackZ(key)})
		}
	}
	return out
}
