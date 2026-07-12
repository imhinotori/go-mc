package chunkticket

// This file is a 1:1 port of the shared level-propagation core:
//   net.minecraft.world.level.lighting.LeveledPriorityQueue
//   net.minecraft.world.level.lighting.DynamicGraphMinFixedPoint
//   net.minecraft.server.level.ChunkTracker
//
// The min-fixed-point graph propagates a source level outward, raising a neighbour's
// level by +1 per edge (ChunkTracker.computeLevelFromNeighbor). It is the identical
// algorithm the light engine uses; here the graph is the 3x3 Chebyshev neighbourhood
// of every chunk column, keyed by a packed long.

// invalidChunkPos mirrors ChunkPos.INVALID_CHUNK_POS = pack(1875066, 1875066).
// CITE: ChunkPos.<clinit> INVALID_CHUNK_POS = pack(1875066, 1875066).
var invalidChunkPos = packChunk(1875066, 1875066)

// packChunk mirrors ChunkPos.asLong(int,int): (x & 0xFFFFFFFF) | ((z & 0xFFFFFFFF) << 32).
// CITE: ChunkPos.asLong(int, int).
func packChunk(x, z int32) int64 {
	return int64(uint32(x)) | int64(uint32(z))<<32
}

// unpackX / unpackZ mirror ChunkPos.getX/getZ(long).
func unpackX(k int64) int32 { return int32(uint32(k)) }
func unpackZ(k int64) int32 { return int32(uint32(k >> 32)) }

// leveledPriorityQueue is a 1:1 port of LeveledPriorityQueue: one insertion-ordered
// long set per priority level, tracking the lowest non-empty level.
// CITE: LeveledPriorityQueue.
type leveledPriorityQueue struct {
	levelCount       int
	queues           []*linkedLongSet
	firstQueuedLevel int
}

func newLeveledPriorityQueue(levelCount int) *leveledPriorityQueue {
	q := &leveledPriorityQueue{levelCount: levelCount, queues: make([]*linkedLongSet, levelCount)}
	for i := 0; i < levelCount; i++ {
		q.queues[i] = newLinkedLongSet()
	}
	q.firstQueuedLevel = levelCount
	return q
}

// removeFirstLong mirrors LeveledPriorityQueue.removeFirstLong().
func (q *leveledPriorityQueue) removeFirstLong() int64 {
	set := q.queues[q.firstQueuedLevel]
	v := set.removeFirstLong()
	if set.isEmpty() {
		q.checkFirstQueuedLevel(q.levelCount)
	}
	return v
}

// isEmpty mirrors LeveledPriorityQueue.isEmpty(): firstQueuedLevel >= levelCount.
func (q *leveledPriorityQueue) isEmpty() bool { return q.firstQueuedLevel >= q.levelCount }

// dequeue mirrors LeveledPriorityQueue.dequeue(long, int fromLevel, int toLevel).
func (q *leveledPriorityQueue) dequeue(key int64, fromLevel, toLevel int) {
	set := q.queues[fromLevel]
	set.remove(key)
	if set.isEmpty() && q.firstQueuedLevel == fromLevel {
		q.checkFirstQueuedLevel(toLevel)
	}
}

// enqueue mirrors LeveledPriorityQueue.enqueue(long, int level).
func (q *leveledPriorityQueue) enqueue(key int64, level int) {
	q.queues[level].add(key)
	if q.firstQueuedLevel > level {
		q.firstQueuedLevel = level
	}
}

// checkFirstQueuedLevel mirrors LeveledPriorityQueue.checkFirstQueuedLevel(int maxLevel).
func (q *leveledPriorityQueue) checkFirstQueuedLevel(maxLevel int) {
	prev := q.firstQueuedLevel
	q.firstQueuedLevel = maxLevel
	for i := prev + 1; i < maxLevel; i++ {
		if !q.queues[i].isEmpty() {
			q.firstQueuedLevel = i
			break
		}
	}
}

// noComputedLevel mirrors DynamicGraphMinFixedPoint.NO_COMPUTED_LEVEL: the map default,
// read back as (byte)-1 & 0xFF == 255.
const noComputedLevel = 255

// levelOps supplies the DGMFP abstract methods; the concrete ChunkTracker implements it.
type levelOps interface {
	isSource(key int64) bool
	getLevel(key int64) int
	setLevel(key int64, level int)
	getComputedLevel(id, excludedID int64, maxLevel int) int
	computeLevelFromNeighbor(from, to int64, level int) int
	checkNeighborsAfterUpdate(key int64, level int, isDecreasing bool)
}

// levelGraph is the DynamicGraphMinFixedPoint state. CITE: DynamicGraphMinFixedPoint.
type levelGraph struct {
	levelCount     int
	priorityQueue  *leveledPriorityQueue
	computedLevels map[int64]int // Long2ByteMap; missing == NO_COMPUTED_LEVEL(255)
	hasWork        bool
	ops            levelOps
}

func newLevelGraph(levelCount int, ops levelOps) *levelGraph {
	if levelCount >= 254 { // CITE: DGMFP constructor guard.
		panic("Level count must be < 254.")
	}
	return &levelGraph{
		levelCount:     levelCount,
		priorityQueue:  newLeveledPriorityQueue(levelCount),
		computedLevels: make(map[int64]int),
		ops:            ops,
	}
}

func (g *levelGraph) getComputed(key int64) int {
	if v, ok := g.computedLevels[key]; ok {
		return v & 0xFF
	}
	return noComputedLevel
}

func (g *levelGraph) putComputed(key int64, v int) { g.computedLevels[key] = v & 0xFF }
func (g *levelGraph) removeComputed(key int64)     { delete(g.computedLevels, key) }

// calculatePriority mirrors DGMFP.calculatePriority: min(min(a,b), levelCount-1).
func (g *levelGraph) calculatePriority(a, b int) int {
	return minInt(minInt(a, b), g.levelCount-1)
}

// checkNode mirrors DGMFP.checkNode(long): checkEdge(id, id, levelCount-1, false).
func (g *levelGraph) checkNode(key int64) {
	g.checkEdgePublic(key, key, g.levelCount-1, false)
}

// checkEdgePublic mirrors the protected DGMFP.checkEdge(long,long,int,boolean).
func (g *levelGraph) checkEdgePublic(from, to int64, level int, isDecreasing bool) {
	g.checkEdge(from, to, level, g.ops.getLevel(to), g.getComputed(to), isDecreasing)
	g.hasWork = !g.priorityQueue.isEmpty()
}

// checkEdge mirrors the private DGMFP.checkEdge(long,long,int,int,int,boolean).
func (g *levelGraph) checkEdge(from, to int64, level, oldLevel, computedLevel int, isDecreasing bool) {
	if g.ops.isSource(to) {
		return
	}
	level = clamp(level, 0, g.levelCount-1)
	oldLevel = clamp(oldLevel, 0, g.levelCount-1)
	firstQueue := computedLevel == noComputedLevel
	if firstQueue {
		computedLevel = oldLevel
	}
	var newComputed int
	if isDecreasing {
		newComputed = minInt(computedLevel, level)
	} else {
		newComputed = clamp(g.ops.getComputedLevel(to, from, level), 0, g.levelCount-1)
	}
	oldPriority := g.calculatePriority(oldLevel, computedLevel)
	// CITE: DGMFP.checkEdge offset 108 branches on oldLevel == newComputed (NOT
	// computedLevel == newComputed): when the newly computed level differs from the
	// node stored level, (re)enqueue at the new priority; otherwise the node has
	// converged, so drop it from the queue and its computed slot.
	if oldLevel != newComputed {
		newPriority := g.calculatePriority(oldLevel, newComputed)
		if oldPriority != newPriority && !firstQueue {
			g.priorityQueue.dequeue(to, oldPriority, newPriority)
		}
		g.priorityQueue.enqueue(to, newPriority)
		g.putComputed(to, newComputed)
	} else if !firstQueue {
		g.priorityQueue.dequeue(to, oldPriority, g.levelCount)
		g.removeComputed(to)
	}
}

// checkNeighbor mirrors DGMFP.checkNeighbor(long,long,int,boolean).
func (g *levelGraph) checkNeighbor(from, to int64, level int, isDecreasing bool) {
	computedLevel := g.getComputed(to)
	propagated := clamp(g.ops.computeLevelFromNeighbor(from, to, level), 0, g.levelCount-1)
	if isDecreasing {
		g.checkEdge(from, to, propagated, g.ops.getLevel(to), computedLevel, isDecreasing)
		return
	}
	firstQueue := computedLevel == noComputedLevel
	var base int
	if firstQueue {
		base = clamp(g.ops.getLevel(to), 0, g.levelCount-1)
	} else {
		base = computedLevel
	}
	if propagated == base {
		var oldLevel int
		if firstQueue {
			oldLevel = base
		} else {
			oldLevel = g.ops.getLevel(to)
		}
		g.checkEdge(from, to, g.levelCount-1, oldLevel, computedLevel, isDecreasing)
	}
}

// runUpdates mirrors DGMFP.runUpdates(int maxUpdates).
func (g *levelGraph) runUpdates(maxUpdates int) int {
	if g.priorityQueue.isEmpty() {
		return maxUpdates
	}
	for !g.priorityQueue.isEmpty() && maxUpdates > 0 {
		maxUpdates--
		key := g.priorityQueue.removeFirstLong()
		oldLevel := clamp(g.ops.getLevel(key), 0, g.levelCount-1)
		var computed int
		if v, ok := g.computedLevels[key]; ok {
			computed = v & 0xFF
			delete(g.computedLevels, key)
		} else {
			computed = noComputedLevel
		}
		if computed < oldLevel {
			g.ops.setLevel(key, computed)
			g.ops.checkNeighborsAfterUpdate(key, computed, true)
		} else if computed > oldLevel {
			g.ops.setLevel(key, g.levelCount-1)
			if computed != g.levelCount-1 {
				g.priorityQueue.enqueue(key, g.calculatePriority(g.levelCount-1, computed))
				g.putComputed(key, computed)
			}
			g.ops.checkNeighborsAfterUpdate(key, oldLevel, false)
		}
	}
	g.hasWork = !g.priorityQueue.isEmpty()
	return maxUpdates
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
