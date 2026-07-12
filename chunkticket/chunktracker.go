package chunkticket

// chunkTracker is a 1:1 port of net.minecraft.server.level.ChunkTracker: a
// DynamicGraphMinFixedPoint over the chunk grid where every column has its 8 Chebyshev
// neighbours as graph edges, and a source contributes getLevelFromSource(key) while a
// neighbour contributes neighbourLevel+1. CITE: ChunkTracker.
//
// Go has no abstract classes, so the two ChunkTracker-abstract hooks (getLevel/setLevel
// and getLevelFromSource) are supplied by trackerBackend, and the level-graph plumbing
// (getComputedLevel/computeLevelFromNeighbor/checkNeighborsAfterUpdate/isSource) lives
// here and is shared by all three concrete trackers.
type trackerBackend interface {
	// getLevel mirrors the ChunkTracker-abstract getLevel(long).
	getLevel(key int64) int
	// setLevel mirrors the ChunkTracker-abstract setLevel(long,int).
	setLevel(key int64, level int)
	// getLevelFromSource mirrors ChunkTracker.getLevelFromSource(long).
	getLevelFromSource(key int64) int
}

type chunkTracker struct {
	graph   *levelGraph
	backend trackerBackend
}

func newChunkTracker(levelCount int, backend trackerBackend) *chunkTracker {
	t := &chunkTracker{backend: backend}
	t.graph = newLevelGraph(levelCount, t)
	return t
}

// isSource mirrors ChunkTracker.isSource(long): key == INVALID_CHUNK_POS.
func (t *chunkTracker) isSource(key int64) bool { return key == invalidChunkPos }

// getLevel / setLevel delegate to the backend.
func (t *chunkTracker) getLevel(key int64) int      { return t.backend.getLevel(key) }
func (t *chunkTracker) setLevel(key int64, lvl int) { t.backend.setLevel(key, lvl) }

// checkNeighborsAfterUpdate mirrors ChunkTracker.checkNeighborsAfterUpdate(long, int,
// boolean): unless (isDecreasing && level >= levelCount-2), visit all 8 Chebyshev
// neighbours (skipping self) with checkNeighbor.
func (t *chunkTracker) checkNeighborsAfterUpdate(key int64, level int, isDecreasing bool) {
	if isDecreasing && level >= t.graph.levelCount-2 {
		return
	}
	x := unpackX(key)
	z := unpackZ(key)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			nb := packChunk(x+dx, z+dz)
			if nb == key {
				continue
			}
			t.graph.checkNeighbor(key, nb, level, isDecreasing)
		}
	}
}

// getComputedLevel mirrors ChunkTracker.getComputedLevel(long id, long excludedId,
// int maxLevel): the minimum, over the 8 neighbours (excluding excludedId, mapped to
// INVALID when it equals a neighbour), of computeLevelFromNeighbor(neighbour, id,
// getLevel(neighbour)); short-circuits at 0.
func (t *chunkTracker) getComputedLevel(id, excludedID int64, maxLevel int) int {
	minLevel := maxLevel
	x := unpackX(id)
	z := unpackZ(id)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			nb := packChunk(x+dx, z+dz)
			if nb == id {
				nb = invalidChunkPos
			}
			if nb == excludedID {
				continue
			}
			propagated := t.computeLevelFromNeighbor(nb, id, t.getLevel(nb))
			if minLevel > propagated {
				minLevel = propagated
			}
			if minLevel == 0 {
				return minLevel
			}
		}
	}
	return minLevel
}

// computeLevelFromNeighbor mirrors ChunkTracker.computeLevelFromNeighbor(long from,
// long to, int level): if from == INVALID_CHUNK_POS it is the source, so
// getLevelFromSource(to); otherwise level + 1.
func (t *chunkTracker) computeLevelFromNeighbor(from, to int64, level int) int {
	if from == invalidChunkPos {
		return t.backend.getLevelFromSource(to)
	}
	return level + 1
}

// update mirrors ChunkTracker.update(long, int, boolean): checkEdge(INVALID, key, level,
// isDecreasing).
func (t *chunkTracker) update(key int64, level int, isDecreasing bool) {
	t.graph.checkEdgePublic(invalidChunkPos, key, level, isDecreasing)
}

// runUpdates mirrors DynamicGraphMinFixedPoint.runUpdates(int) via the tracker.
func (t *chunkTracker) runUpdates(maxUpdates int) int { return t.graph.runUpdates(maxUpdates) }
