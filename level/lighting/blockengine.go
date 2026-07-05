package lighting

import "github.com/imhinotori/sulfur/level/block"

// blockLightEngine ports net.minecraft.world.level.lighting.BlockLightEngine. CITE.
type blockLightEngine struct {
	*lightEngine
}

func newBlockLightEngine(chunkSource LightChunkGetter) *blockLightEngine {
	storage := newLayerStorage(newBlockStorageMap())
	be := &blockLightEngine{}
	be.lightEngine = newLightEngine(chunkSource, storage)
	be.lightEngine.impl = be
	return be
}

// getEmission ports BlockLightEngine.getEmission: the state's emission, gated by lightOnInSection.
// CITE: BlockLightEngine.getEmission.
func (b *blockLightEngine) getEmission(blockNode int64, state block.StateID) int {
	emission := block.LightEmission(state)
	if emission > 0 && b.storage.lightOnInSection(blockNodeToSection(blockNode)) {
		return emission
	}
	return 0
}

// checkNode ports BlockLightEngine.checkNode. CITE.
func (b *blockLightEngine) checkNode(blockNode int64) {
	sectionNode := blockNodeToSection(blockNode)
	if !b.storage.storingLightForSection(sectionNode) {
		return
	}
	state := b.getState(blockNode)
	lightEmission := b.getEmission(blockNode, state)
	oldLevel := b.storage.getStoredLevel(blockNode)
	if lightEmission < oldLevel {
		b.storage.setStoredLevel(blockNode, 0)
		b.enqueueDecrease(blockNode, qeDecreaseAllDirections(oldLevel))
	} else {
		b.enqueueDecrease(blockNode, pullLightInEntry)
	}
	if lightEmission > 0 {
		b.enqueueIncrease(blockNode, qeIncreaseLightFromEmission(lightEmission, b.isEmptyShape(state)))
	}
}

// propagateIncrease ports BlockLightEngine.propagateIncrease. CITE.
func (b *blockLightEngine) propagateIncrease(fromNode, increaseData int64, fromLevel int) {
	var fromState block.StateID
	fromStateSet := false
	for _, propagationDirection := range allDirections {
		if !qeShouldPropagateInDirection(increaseData, propagationDirection) {
			continue
		}
		toNode := blockOffset(fromNode, propagationDirection)
		if !b.storage.storingLightForSection(blockNodeToSection(toNode)) {
			continue
		}
		maxPossibleNewToLevel := fromLevel - 1
		toLevel := b.storage.getStoredLevel(toNode)
		if maxPossibleNewToLevel <= toLevel {
			continue
		}
		toState := b.getState(toNode)
		newToLevel := fromLevel - b.getOpacity(toState)
		if newToLevel <= toLevel {
			continue
		}
		if !fromStateSet {
			if qeIsFromEmptyShape(increaseData) {
				fromState = airState
			} else {
				fromState = b.getState(fromNode)
			}
			fromStateSet = true
		}
		if b.shapeOccludes(fromState, toState, propagationDirection) {
			continue
		}
		b.storage.setStoredLevel(toNode, newToLevel)
		if newToLevel <= 1 {
			continue
		}
		b.enqueueIncrease(toNode, qeIncreaseSkipOneDirection(newToLevel, b.isEmptyShape(toState), oppositeDir(propagationDirection)))
	}
}

// propagateDecrease ports BlockLightEngine.propagateDecrease. CITE.
func (b *blockLightEngine) propagateDecrease(fromNode, decreaseData int64) {
	oldFromLevel := qeGetFromLevel(decreaseData)
	for _, propagationDirection := range allDirections {
		if !qeShouldPropagateInDirection(decreaseData, propagationDirection) {
			continue
		}
		toNode := blockOffset(fromNode, propagationDirection)
		if !b.storage.storingLightForSection(blockNodeToSection(toNode)) {
			continue
		}
		toLevel := b.storage.getStoredLevel(toNode)
		if toLevel == 0 {
			continue
		}
		if toLevel <= oldFromLevel-1 {
			toState := b.getState(toNode)
			toEmission := b.getEmission(toNode, toState)
			b.storage.setStoredLevel(toNode, 0)
			if toEmission < toLevel {
				b.enqueueDecrease(toNode, qeDecreaseSkipOneDirection(toLevel, oppositeDir(propagationDirection)))
			}
			if toEmission <= 0 {
				continue
			}
			b.enqueueIncrease(toNode, qeIncreaseLightFromEmission(toEmission, b.isEmptyShape(toState)))
			continue
		}
		b.enqueueIncrease(toNode, qeIncreaseOnlyOneDirection(toLevel, false, oppositeDir(propagationDirection)))
	}
}

// propagateLightSources ports BlockLightEngine.propagateLightSources. CITE.
func (b *blockLightEngine) propagateLightSources(chunkX, chunkZ int) {
	b.setLightEnabledColumn(chunkX, chunkZ, true)
	chunk := b.chunkSource.GetChunkForLighting(chunkX, chunkZ)
	if chunk != nil {
		chunk.FindBlockLightSources(func(x, y, z int, state block.StateID) {
			lightEmission := block.LightEmission(state)
			b.enqueueIncrease(blockAsLong(x, y, z), qeIncreaseLightFromEmission(lightEmission, b.isEmptyShape(state)))
		})
	}
}

// setLightEnabledColumn ports LightEngine.setLightEnabled(ChunkPos) for the block engine (sky
// overrides it). CITE: LightEngine.setLightEnabled.
func (b *blockLightEngine) setLightEnabledColumn(chunkX, chunkZ int, enable bool) {
	b.storage.setLightEnabled(getZeroNodeXZ(chunkX, chunkZ), enable)
}
