package chunkticket

// playerTicketTracker is a 1:1 port of DistanceManager$PlayerTicketTracker (which
// extends DistanceManager$FixedPlayerDistanceChunkTracker). The base tracker computes,
// for every chunk, the Chebyshev distance to the nearest player (source level 0 at a
// player chunk, +1 per ring) capped at maxDistance = playerViewCap. The subclass adds
// a PLAYER_LOADING ticket to every chunk whose distance <= viewDistance and removes it
// otherwise, whenever the distance or the view distance changes.
//
// CITE: DistanceManager$FixedPlayerDistanceChunkTracker + DistanceManager$PlayerTicketTracker.
//
// The FixedPlayerDistanceChunkTracker is constructed in DistanceManager with the max
// possible view distance (32) so a single tracker serves any runtime view distance.
// CITE: DistanceManager ctor `new PlayerTicketTracker(this, 32)`.
const playerViewCap = 32

type playerTicketTracker struct {
	dm *DistanceManager

	tracker     *chunkTracker
	chunks      map[int64]int // per-chunk Chebyshev distance (FixedPlayerDistanceChunkTracker.chunks)
	maxDistance int

	viewDistance int
	queueLevels  map[int64]int
	toUpdate     map[int64]struct{}
}

func newPlayerTicketTracker(dm *DistanceManager) *playerTicketTracker {
	p := &playerTicketTracker{
		dm:          dm,
		chunks:      make(map[int64]int),
		maxDistance: playerViewCap,
		queueLevels: make(map[int64]int),
		toUpdate:    make(map[int64]struct{}),
	}
	// FixedPlayerDistanceChunkTracker(this, maxDistance) -> ChunkTracker(maxDistance+2,...).
	// The ChunkTracker levelCount is maxDistance+2 (cap+2 = 34): the default getLevel value
	// for chunks outside the stored ring, used by getLevel below. CITE: ChunkTracker ctor
	// super(maxDistance+2, 16, 256) + chunks.defaultReturnValue(maxDistance+2).
	p.tracker = newChunkTracker(playerViewCap+2, p)
	return p
}

// --- trackerBackend (FixedPlayerDistanceChunkTracker overrides) ---

// getLevelFromSource mirrors FixedPlayerDistanceChunkTracker.getLevelFromSource: 0 when a
// player stands in the chunk, else Integer.MAX_VALUE.
func (p *playerTicketTracker) getLevelFromSource(key int64) int {
	if p.dm.havePlayer(key) {
		return 0
	}
	return maxInt32Val
}

// getLevel mirrors FixedPlayerDistanceChunkTracker.getLevel: chunks.get(key), default
// maxDistance+2 (the map default value). CITE: ctor chunks.defaultReturnValue(maxDistance+2).
func (p *playerTicketTracker) getLevel(key int64) int {
	if v, ok := p.chunks[key]; ok {
		return v
	}
	return p.maxDistance + 2
}

// setLevel mirrors FixedPlayerDistanceChunkTracker.setLevel: values > maxDistance are
// removed, else stored; then onLevelChange(key, oldLevel, newLevel).
func (p *playerTicketTracker) setLevel(key int64, lvl int) {
	old := p.getLevel(key)
	if lvl > p.maxDistance {
		delete(p.chunks, key)
	} else {
		p.chunks[key] = lvl
	}
	p.onLevelChangeBase(key, old, lvl)
}

// onLevelChangeBase mirrors PlayerTicketTracker.onLevelChange(long,int,int): record the
// chunk into toUpdate. CITE: PlayerTicketTracker.onLevelChange(long, int, int).
func (p *playerTicketTracker) onLevelChangeBase(key int64, _ int, _ int) {
	p.toUpdate[key] = struct{}{}
}

// update mirrors FixedPlayerDistanceChunkTracker.update via ChunkTracker.update.
func (p *playerTicketTracker) update(key int64, level int, isDecreasing bool) {
	p.tracker.update(key, level, isDecreasing)
}

// haveTicketFor mirrors PlayerTicketTracker.haveTicketFor(int level): level <= viewDistance.
func (p *playerTicketTracker) haveTicketFor(level int) bool { return level <= p.viewDistance }

// updateViewDistance mirrors PlayerTicketTracker.updateViewDistance(int): for every
// tracked chunk, re-evaluate the ticket against the old and new view distance, then set
// viewDistance. CITE: PlayerTicketTracker.updateViewDistance.
func (p *playerTicketTracker) updateViewDistance(viewDistance int) {
	for key, dist := range p.chunks {
		hadTicket := p.haveTicketFor(dist)
		hasTicket := dist <= viewDistance
		p.applyTicket(key, dist, hadTicket, hasTicket)
	}
	p.viewDistance = viewDistance
}

// applyTicket mirrors PlayerTicketTracker.onLevelChange(long, int level, boolean
// hadTicket, boolean hasTicket): when membership flips, add or remove the PLAYER_LOADING
// ticket at PLAYER_TICKET_LEVEL. CITE: PlayerTicketTracker.onLevelChange(JIZZ).
func (p *playerTicketTracker) applyTicket(key int64, _ int, hadTicket, hasTicket bool) {
	if hadTicket == hasTicket {
		return
	}
	ticket := NewTicket(PlayerLoading, playerTicketLevel)
	if hasTicket {
		p.dm.storage.addTicket(key, ticket)
	} else {
		p.dm.storage.removeTicket(key, ticket)
	}
}

// runAllUpdates mirrors PlayerTicketTracker.runAllUpdates(): drain the base tracker,
// then for every queued chunk compare the queued vs current level and apply the ticket
// delta. CITE: PlayerTicketTracker.runAllUpdates.
func (p *playerTicketTracker) runAllUpdates() {
	p.tracker.runUpdates(maxInt32Val)
	if len(p.toUpdate) == 0 {
		return
	}
	for key := range p.toUpdate {
		queued := p.queuedLevel(key)
		current := p.getLevel(key)
		if queued == current {
			continue
		}
		// vanilla dispatches an async task keyed on queued level; synchronously we just
		// apply the ticket delta immediately (identical converged result).
		p.applyTicket(key, current, p.haveTicketFor(queued), p.haveTicketFor(current))
		p.setQueuedLevel(key, current)
	}
	p.toUpdate = make(map[int64]struct{})
}

// queuedLevel / setQueuedLevel mirror the queueLevels Long2IntMap (default viewDistance+2).
func (p *playerTicketTracker) queuedLevel(key int64) int {
	if v, ok := p.queueLevels[key]; ok {
		return v
	}
	return p.viewDistance + 2
}
func (p *playerTicketTracker) setQueuedLevel(key int64, lvl int) {
	if lvl >= p.viewDistance+2 {
		delete(p.queueLevels, key)
	} else {
		p.queueLevels[key] = lvl
	}
}
