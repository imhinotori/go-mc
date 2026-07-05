package lighting

import "github.com/imhinotori/sulfur/level/block"

// lightEngine ports the abstract net.minecraft.world.level.lighting.LightEngine base: the shared BFS
// driver (increase/decrease FIFO queues, checkBlock set, the 2-entry chunk cache, getState/getOpacity/
// shapeOccludes, runLightUpdates). The three abstract methods (checkNode, propagateIncrease,
// propagateDecrease) are supplied by the concrete sky/block engine via the `impl` interface (Go has
// no inheritance). CITE: LightEngine.
type lightEngine struct {
	chunkSource LightChunkGetter
	storage     *layerStorage
	impl        engineImpl

	blockNodesToCheck map[int64]struct{}
	// FIFO queues store (fromNode, data) pairs, dequeued two at a time. CITE: LongArrayFIFOQueue.
	decreaseQueue []int64
	increaseQueue []int64

	// 2-entry chunk cache. CITE: LightEngine.lastChunkPos/lastChunk.
	lastChunkPos [2]int64
	lastChunk    [2]LightChunk
}

// engineImpl is the concrete-engine surface for the three abstract LightEngine methods. CITE:
// LightEngine.checkNode/propagateIncrease/propagateDecrease (abstract).
type engineImpl interface {
	checkNode(blockNode int64)
	propagateIncrease(fromNode, increaseData int64, fromLevel int)
	propagateDecrease(fromNode, decreaseData int64)
}

// pullLightInEntry ports LightEngine.PULL_LIGHT_IN_ENTRY = QueueEntry.decreaseAllDirections(1). CITE.
var pullLightInEntry = qeDecreaseAllDirections(1)

const maxLevel = 15 // LightEngine.MAX_LEVEL

func newLightEngine(chunkSource LightChunkGetter, storage *layerStorage) *lightEngine {
	e := &lightEngine{
		chunkSource:       chunkSource,
		storage:           storage,
		blockNodesToCheck: make(map[int64]struct{}),
	}
	e.clearChunkCache()
	return e
}

// getState ports LightEngine.getState(pos): read the block at blockNode, BEDROCK if the chunk is not
// loaded. CITE: LightEngine.getState.
func (e *lightEngine) getState(blockNode int64) block.StateID {
	chunkX := blockToSectionCoord(blockGetX(blockNode))
	chunkZ := blockToSectionCoord(blockGetZ(blockNode))
	chunk := e.getChunk(chunkX, chunkZ)
	if chunk == nil {
		return bedrockState
	}
	return chunk.GetBlockState(blockGetX(blockNode), blockGetY(blockNode), blockGetZ(blockNode))
}

// getOpacity ports LightEngine.getOpacity(state) = max(1, getLightDampening()). CITE.
func (e *lightEngine) getOpacity(state block.StateID) int { return block.LightOpacity(state) }

// shapeOccludes ports LightEngine.shapeOccludes(from,to,dir). CITE.
func (e *lightEngine) shapeOccludes(fromState, toState block.StateID, dir block.Direction) bool {
	return block.ShapeOccludes(fromState, toState, dir)
}

// isEmptyShape ports LightEngine.isEmptyShape(state) = !canOcclude || !useShapeForLightOcclusion.
// For the light BFS the only thing derived from a state's shape emptiness is the FLAG_FROM_EMPTY_SHAPE
// bit, which the occlusion-face classification already encodes: a state is "empty shape" iff ALL its
// occlusion faces are class EMPTY (block.LightShapeIsEmpty). CITE: LightEngine.isEmptyShape.
func (e *lightEngine) isEmptyShape(state block.StateID) bool { return block.LightShapeIsEmpty(state) }

// getChunk ports LightEngine.getChunk with the 2-entry cache. CITE.
func (e *lightEngine) getChunk(chunkX, chunkZ int) LightChunk {
	pos := chunkPosPack(chunkX, chunkZ)
	for i := 0; i < 2; i++ {
		if pos == e.lastChunkPos[i] {
			return e.lastChunk[i]
		}
	}
	chunk := e.chunkSource.GetChunkForLighting(chunkX, chunkZ)
	for i := 1; i > 0; i-- {
		e.lastChunkPos[i] = e.lastChunkPos[i-1]
		e.lastChunk[i] = e.lastChunk[i-1]
	}
	e.lastChunkPos[0] = pos
	e.lastChunk[0] = chunk
	return chunk
}

func (e *lightEngine) clearChunkCache() {
	e.lastChunkPos[0], e.lastChunkPos[1] = invalidChunkPos, invalidChunkPos
	e.lastChunk[0], e.lastChunk[1] = nil, nil
}

// checkBlock ports LightEngine.checkBlock. CITE.
func (e *lightEngine) checkBlock(blockNode int64) { e.blockNodesToCheck[blockNode] = struct{}{} }

func (e *lightEngine) enqueueDecrease(fromNode, data int64) {
	e.decreaseQueue = append(e.decreaseQueue, fromNode, data)
}
func (e *lightEngine) enqueueIncrease(fromNode, data int64) {
	e.increaseQueue = append(e.increaseQueue, fromNode, data)
}

// hasLightWork ports LightEngine.hasLightWork. CITE.
func (e *lightEngine) hasLightWork() bool {
	return e.storage.hasInconsistenciesFlag() || len(e.blockNodesToCheck) > 0 || len(e.decreaseQueue) > 0 || len(e.increaseQueue) > 0
}

// runLightUpdates ports LightEngine.runLightUpdates: run all checkNodes, drain decreases, reconcile
// inconsistencies, swap, then drain increases. CITE.
func (e *lightEngine) runLightUpdates() int {
	// Iterate the check set. Note: vanilla uses a LongOpenHashSet whose iteration order does not
	// affect the RESULT — checkNode only enqueues; the ordered FIFO drains produce the final levels.
	for node := range e.blockNodesToCheck {
		e.impl.checkNode(node)
	}
	e.blockNodesToCheck = make(map[int64]struct{})
	count := 0
	count += e.propagateDecreases()
	e.clearChunkCache()
	e.storage.markNewInconsistencies()
	e.storage.swapSectionMap()
	count += e.propagateIncreases()
	return count
}

// propagateIncreases ports LightEngine.propagateIncreases. CITE.
func (e *lightEngine) propagateIncreases() int {
	count := 0
	for len(e.increaseQueue) > 0 {
		fromNode := e.increaseQueue[0]
		increaseData := e.increaseQueue[1]
		e.increaseQueue = e.increaseQueue[2:]
		fromLevel := e.storage.getStoredLevel(fromNode)
		fromTargetLevel := qeGetFromLevel(increaseData)
		if qeIsIncreaseFromEmission(increaseData) && fromLevel < fromTargetLevel {
			e.storage.setStoredLevel(fromNode, fromTargetLevel)
			fromLevel = fromTargetLevel
		}
		if fromLevel == fromTargetLevel {
			e.impl.propagateIncrease(fromNode, increaseData, fromLevel)
		}
		count++
	}
	return count
}

// propagateDecreases ports LightEngine.propagateDecreases. CITE.
func (e *lightEngine) propagateDecreases() int {
	count := 0
	for len(e.decreaseQueue) > 0 {
		fromNode := e.decreaseQueue[0]
		decreaseData := e.decreaseQueue[1]
		e.decreaseQueue = e.decreaseQueue[2:]
		e.impl.propagateDecrease(fromNode, decreaseData)
		count++
	}
	return count
}

// getLightValue ports LightEngine.getLightValue = storage.getLightValue. Dispatched by engine kind.
func (e *lightEngine) getLightValue(blockNode int64) int {
	if e.storage.sky != nil {
		return skyGetLightValue(e.storage, blockNode)
	}
	return blockGetLightValue(e.storage, blockNode)
}

// updateSectionStatus ports LightEngine.updateSectionStatus. CITE.
func (e *lightEngine) updateSectionStatus(sectionNode int64, sectionEmpty bool) {
	e.storage.updateSectionStatus(sectionNode, sectionEmpty)
}

// queueSectionData ports LightEngine.queueSectionData. CITE.
func (e *lightEngine) queueSectionData(sectionNode int64, data *DataLayer) {
	e.storage.queueSectionData(sectionNode, data)
}

// getDataLayerData ports LightEngine.getDataLayerData. CITE.
func (e *lightEngine) getDataLayerData(sectionNode int64) *DataLayer {
	return e.storage.getDataLayerData(sectionNode)
}
