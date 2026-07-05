package lighting

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
)

// skyLightEngine ports net.minecraft.world.level.lighting.SkyLightEngine. CITE.
type skyLightEngine struct {
	*lightEngine
	emptyChunkSources *ChunkSkyLightSources
}

// Sky BFS entry constants. CITE: SkyLightEngine static fields.
var (
	removeTopSkySourceEntry = qeDecreaseAllDirections(15)
	removeSkySourceEntry    = qeDecreaseSkipOneDirection(15, block.Up)
	addSkySourceEntry       = qeIncreaseSkipOneDirection(15, false, block.Up)
)

func newSkyLightEngine(chunkSource LightChunkGetter) *skyLightEngine {
	sm := newSkyStorageMap()
	storage := newLayerStorage(sm)
	storage.sky = &skyStorageExt{}
	se := &skyLightEngine{}
	se.lightEngine = newLightEngine(chunkSource, storage)
	se.lightEngine.impl = se
	se.emptyChunkSources = NewChunkSkyLightSources(chunkSource.MinSectionY())
	return se
}

func skyIsSourceLevel(value int) bool { return value == 15 } // SkyLightEngine.isSourceLevel

// getLowestSourceY ports SkyLightEngine.getLowestSourceY. CITE.
func (s *skyLightEngine) getLowestSourceY(x, z, defaultValue int) int {
	sources := s.getChunkSources(blockToSectionCoord(x), blockToSectionCoord(z))
	if sources == nil {
		return defaultValue
	}
	return sources.GetLowestSourceY(sectionRelative(x), sectionRelative(z))
}

// getChunkSources ports SkyLightEngine.getChunkSources. CITE.
func (s *skyLightEngine) getChunkSources(chunkX, chunkZ int) *ChunkSkyLightSources {
	chunk := s.chunkSource.GetChunkForLighting(chunkX, chunkZ)
	if chunk != nil {
		return chunk.SkyLightSources()
	}
	return nil
}

// checkNode ports SkyLightEngine.checkNode. CITE.
func (s *skyLightEngine) checkNode(blockNode int64) {
	x := blockGetX(blockNode)
	y := blockGetY(blockNode)
	z := blockGetZ(blockNode)
	sectionNode := blockNodeToSection(blockNode)
	lowestSourceY := math.MaxInt32
	if s.storage.lightOnInSection(sectionNode) {
		lowestSourceY = s.getLowestSourceY(x, z, math.MaxInt32)
	}
	if lowestSourceY != math.MaxInt32 {
		s.updateSourcesInColumn(x, z, lowestSourceY)
	}
	if !s.storage.storingLightForSection(sectionNode) {
		return
	}
	isSource := y >= lowestSourceY
	if isSource {
		s.enqueueDecrease(blockNode, removeSkySourceEntry)
		s.enqueueIncrease(blockNode, addSkySourceEntry)
	} else {
		oldLevel := s.storage.getStoredLevel(blockNode)
		if oldLevel > 0 {
			s.storage.setStoredLevel(blockNode, 0)
			s.enqueueDecrease(blockNode, qeDecreaseAllDirections(oldLevel))
		} else {
			s.enqueueDecrease(blockNode, pullLightInEntry)
		}
	}
}

// updateSourcesInColumn ports SkyLightEngine.updateSourcesInColumn. CITE.
func (s *skyLightEngine) updateSourcesInColumn(x, z, lowestSourceY int) {
	worldBottomY := sectionToBlockCoord(getBottomSectionY(s.storage))
	s.removeSourcesBelow(x, z, lowestSourceY, worldBottomY)
	s.addSourcesAbove(x, z, lowestSourceY, worldBottomY)
}

// removeSourcesBelow ports SkyLightEngine.removeSourcesBelow. CITE.
func (s *skyLightEngine) removeSourcesBelow(x, z, lowestSourceY, worldBottomY int) {
	if lowestSourceY <= worldBottomY {
		return
	}
	sectionX := blockToSectionCoord(x)
	sectionZ := blockToSectionCoord(z)
	startY := lowestSourceY - 1
	sectionYc := blockToSectionCoord(startY)
	for hasLightDataAtOrBelow(s.storage, sectionYc) {
		if s.storage.storingLightForSection(sectionAsLong(sectionX, sectionYc, sectionZ)) {
			sectionBottomY := sectionToBlockCoord(sectionYc)
			sectionTopY := sectionBottomY + 15
			for y := min(sectionTopY, startY); y >= sectionBottomY; y-- {
				blockNode := blockAsLong(x, y, z)
				if !skyIsSourceLevel(s.storage.getStoredLevel(blockNode)) {
					return
				}
				s.storage.setStoredLevel(blockNode, 0)
				if y == lowestSourceY-1 {
					s.enqueueDecrease(blockNode, removeTopSkySourceEntry)
				} else {
					s.enqueueDecrease(blockNode, removeSkySourceEntry)
				}
			}
		}
		sectionYc--
	}
}

// addSourcesAbove ports SkyLightEngine.addSourcesAbove. CITE.
func (s *skyLightEngine) addSourcesAbove(x, z, lowestSourceY, worldBottomY int) {
	sectionX := blockToSectionCoord(x)
	sectionZ := blockToSectionCoord(z)
	neighborLowestSourceY := max(
		max(s.getLowestSourceY(x-1, z, math.MinInt32), s.getLowestSourceY(x+1, z, math.MinInt32)),
		max(s.getLowestSourceY(x, z-1, math.MinInt32), s.getLowestSourceY(x, z+1, math.MinInt32)))
	startY := max(lowestSourceY, worldBottomY)
	sectionNode := sectionAsLong(sectionX, blockToSectionCoord(startY), sectionZ)
	for !isAboveData(s.storage, sectionNode) {
		if s.storage.storingLightForSection(sectionNode) {
			sectionBottomY := sectionToBlockCoord(sectionY(sectionNode))
			sectionTopY := sectionBottomY + 15
			for y := max(sectionBottomY, startY); y <= sectionTopY; y++ {
				blockNode := blockAsLong(x, y, z)
				if skyIsSourceLevel(s.storage.getStoredLevel(blockNode)) {
					return
				}
				s.storage.setStoredLevel(blockNode, 15)
				if y >= neighborLowestSourceY && y != lowestSourceY {
					continue
				}
				s.enqueueIncrease(blockNode, addSkySourceEntry)
			}
		}
		sectionNode = sectionOffsetDir(sectionNode, block.Up)
	}
}

// propagateIncrease ports SkyLightEngine.propagateIncrease. CITE.
func (s *skyLightEngine) propagateIncrease(fromNode, increaseData int64, fromLevel int) {
	var fromState block.StateID
	fromStateSet := false
	emptySectionsBelow := s.countEmptySectionsBelowIfAtBorder(fromNode)
	for _, propagationDirection := range allDirections {
		if !qeShouldPropagateInDirection(increaseData, propagationDirection) {
			continue
		}
		toNode := blockOffset(fromNode, propagationDirection)
		if !s.storage.storingLightForSection(blockNodeToSection(toNode)) {
			continue
		}
		maxPossibleNewToLevel := fromLevel - 1
		toLevel := s.storage.getStoredLevel(toNode)
		if maxPossibleNewToLevel <= toLevel {
			continue
		}
		toState := s.getState(toNode)
		newToLevel := fromLevel - s.getOpacity(toState)
		if newToLevel <= toLevel {
			continue
		}
		if !fromStateSet {
			if qeIsFromEmptyShape(increaseData) {
				fromState = airState
			} else {
				fromState = s.getState(fromNode)
			}
			fromStateSet = true
		}
		if s.shapeOccludes(fromState, toState, propagationDirection) {
			continue
		}
		s.storage.setStoredLevel(toNode, newToLevel)
		if newToLevel > 1 {
			s.enqueueIncrease(toNode, qeIncreaseSkipOneDirection(newToLevel, s.isEmptyShape(toState), oppositeDir(propagationDirection)))
		}
		s.propagateFromEmptySections(toNode, propagationDirection, newToLevel, true, emptySectionsBelow)
	}
}

// propagateDecrease ports SkyLightEngine.propagateDecrease. CITE.
func (s *skyLightEngine) propagateDecrease(fromNode, decreaseData int64) {
	emptySectionsBelow := s.countEmptySectionsBelowIfAtBorder(fromNode)
	oldFromLevel := qeGetFromLevel(decreaseData)
	for _, propagationDirection := range allDirections {
		if !qeShouldPropagateInDirection(decreaseData, propagationDirection) {
			continue
		}
		toNode := blockOffset(fromNode, propagationDirection)
		if !s.storage.storingLightForSection(blockNodeToSection(toNode)) {
			continue
		}
		toLevel := s.storage.getStoredLevel(toNode)
		if toLevel == 0 {
			continue
		}
		if toLevel <= oldFromLevel-1 {
			s.storage.setStoredLevel(toNode, 0)
			s.enqueueDecrease(toNode, qeDecreaseSkipOneDirection(toLevel, oppositeDir(propagationDirection)))
			s.propagateFromEmptySections(toNode, propagationDirection, toLevel, false, emptySectionsBelow)
			continue
		}
		s.enqueueIncrease(toNode, qeIncreaseOnlyOneDirection(toLevel, false, oppositeDir(propagationDirection)))
	}
}

// countEmptySectionsBelowIfAtBorder ports SkyLightEngine.countEmptySectionsBelowIfAtBorder. CITE.
func (s *skyLightEngine) countEmptySectionsBelowIfAtBorder(blockNode int64) int {
	y := blockGetY(blockNode)
	localY := sectionRelative(y)
	if localY != 0 {
		return 0
	}
	x := blockGetX(blockNode)
	z := blockGetZ(blockNode)
	localX := sectionRelative(x)
	localZ := sectionRelative(z)
	if localX == 0 || localX == 15 || localZ == 0 || localZ == 15 {
		sectionX := blockToSectionCoord(x)
		sectionYc := blockToSectionCoord(y)
		sectionZ := blockToSectionCoord(z)
		emptySectionsBelow := 0
		for !s.storage.storingLightForSection(sectionAsLong(sectionX, sectionYc-emptySectionsBelow-1, sectionZ)) &&
			hasLightDataAtOrBelow(s.storage, sectionYc-emptySectionsBelow-1) {
			emptySectionsBelow++
		}
		return emptySectionsBelow
	}
	return 0
}

// propagateFromEmptySections ports SkyLightEngine.propagateFromEmptySections. CITE.
func (s *skyLightEngine) propagateFromEmptySections(toNode int64, propagationDirection block.Direction, toLevel int, increase bool, emptySectionsBelow int) {
	if emptySectionsBelow == 0 {
		return
	}
	x := blockGetX(toNode)
	z := blockGetZ(toNode)
	if !skyCrossedSectionEdge(propagationDirection, sectionRelative(x), sectionRelative(z)) {
		return
	}
	y := blockGetY(toNode)
	sectionX := blockToSectionCoord(x)
	sectionZ := blockToSectionCoord(z)
	sectionYc := blockToSectionCoord(y) - 1
	bottomSectionY := sectionYc - emptySectionsBelow + 1
	for sectionYc >= bottomSectionY {
		if !s.storage.storingLightForSection(sectionAsLong(sectionX, sectionYc, sectionZ)) {
			sectionYc--
			continue
		}
		sectionMinY := sectionToBlockCoord(sectionYc)
		for localY := 15; localY >= 0; localY-- {
			blockNode := blockAsLong(x, sectionMinY+localY, z)
			if increase {
				s.storage.setStoredLevel(blockNode, toLevel)
				if toLevel <= 1 {
					continue
				}
				s.enqueueIncrease(blockNode, qeIncreaseSkipOneDirection(toLevel, true, oppositeDir(propagationDirection)))
				continue
			}
			s.storage.setStoredLevel(blockNode, 0)
			s.enqueueDecrease(blockNode, qeDecreaseSkipOneDirection(toLevel, oppositeDir(propagationDirection)))
		}
		sectionYc--
	}
}

// skyCrossedSectionEdge ports SkyLightEngine.crossedSectionEdge. CITE.
func skyCrossedSectionEdge(propagationDirection block.Direction, x, z int) bool {
	switch propagationDirection {
	case block.North:
		return z == 15
	case block.South:
		return z == 0
	case block.West:
		return x == 15
	case block.East:
		return x == 0
	default:
		return false
	}
}

// setLightEnabled ports SkyLightEngine.setLightEnabled. CITE.
func (s *skyLightEngine) setLightEnabled(chunkX, chunkZ int, enable bool) {
	s.storage.setLightEnabled(getZeroNodeXZ(chunkX, chunkZ), enable)
	if enable {
		sources := s.getChunkSources(chunkX, chunkZ)
		if sources == nil {
			sources = s.emptyChunkSources
		}
		highestNonSourceY := sources.GetHighestLowestSourceY() - 1
		lowestFullySourceSectionY := blockToSectionCoord(highestNonSourceY) + 1
		zeroNode := getZeroNodeXZ(chunkX, chunkZ)
		topSectionYv := getTopSectionY(s.storage, zeroNode)
		bottomSectionY := max(getBottomSectionY(s.storage), lowestFullySourceSectionY)
		for sec := topSectionYv - 1; sec >= bottomSectionY; sec-- {
			dataLayer := s.getDataLayerToWrite(sectionAsLong(chunkX, sec, chunkZ))
			if dataLayer == nil || !dataLayer.IsEmpty() {
				continue
			}
			dataLayer.Fill(15)
		}
	}
}

// getDataLayerToWrite ports LayerLightSectionStorage.getDataLayerToWrite (single-buffered: no COW —
// return the stored layer). CITE: LayerLightSectionStorage.getDataLayerToWrite.
func (s *skyLightEngine) getDataLayerToWrite(sectionNode int64) *DataLayer {
	return s.storage.data.getLayer(sectionNode)
}

// propagateLightSources ports SkyLightEngine.propagateLightSources. CITE.
func (s *skyLightEngine) propagateLightSources(chunkX, chunkZ int) {
	zeroNode := getZeroNodeXZ(chunkX, chunkZ)
	s.storage.setLightEnabled(zeroNode, true)
	sources := s.getChunkSourcesOrEmpty(chunkX, chunkZ)
	northSources := s.getChunkSourcesOrEmpty(chunkX, chunkZ-1)
	southSources := s.getChunkSourcesOrEmpty(chunkX, chunkZ+1)
	westSources := s.getChunkSourcesOrEmpty(chunkX-1, chunkZ)
	eastSources := s.getChunkSourcesOrEmpty(chunkX+1, chunkZ)
	topSectionYv := getTopSectionY(s.storage, zeroNode)
	bottomSectionY := getBottomSectionY(s.storage)
	sectionMinX := sectionToBlockCoord(chunkX)
	sectionMinZ := sectionToBlockCoord(chunkZ)
	for sec := topSectionYv - 1; sec >= bottomSectionY; sec-- {
		sectionNode := sectionAsLong(chunkX, sec, chunkZ)
		dataLayer := s.getDataLayerToWrite(sectionNode)
		if dataLayer == nil {
			continue
		}
		sectionMinY := sectionToBlockCoord(sec)
		sectionMaxY := sectionMinY + 15
		sourcesBelow := false
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				lowestSourceY := sources.GetLowestSourceY(x, z)
				if lowestSourceY > sectionMaxY {
					continue
				}
				var northLowestSourceY, southLowestSourceY, westLowestSourceY, eastLowestSourceY int
				if z == 0 {
					northLowestSourceY = northSources.GetLowestSourceY(x, 15)
				} else {
					northLowestSourceY = sources.GetLowestSourceY(x, z-1)
				}
				if z == 15 {
					southLowestSourceY = southSources.GetLowestSourceY(x, 0)
				} else {
					southLowestSourceY = sources.GetLowestSourceY(x, z+1)
				}
				if x == 0 {
					westLowestSourceY = westSources.GetLowestSourceY(15, z)
				} else {
					westLowestSourceY = sources.GetLowestSourceY(x-1, z)
				}
				if x == 15 {
					eastLowestSourceY = eastSources.GetLowestSourceY(0, z)
				} else {
					eastLowestSourceY = sources.GetLowestSourceY(x+1, z)
				}
				neighborLowestSourceY := max(max(northLowestSourceY, southLowestSourceY), max(westLowestSourceY, eastLowestSourceY))
				for y := sectionMaxY; y >= max(sectionMinY, lowestSourceY); y-- {
					dataLayer.Set(x, sectionRelative(y), z, 15)
					if y != lowestSourceY && y >= neighborLowestSourceY {
						continue
					}
					blockNode := blockAsLong(sectionMinX+x, y, sectionMinZ+z)
					s.enqueueIncrease(blockNode, qeIncreaseSkySourceInDirections(
						y == lowestSourceY,
						y < northLowestSourceY,
						y < southLowestSourceY,
						y < westLowestSourceY,
						y < eastLowestSourceY))
				}
				if lowestSourceY >= sectionMinY {
					continue
				}
				sourcesBelow = true
			}
		}
		if !sourcesBelow {
			break
		}
	}
}

// getChunkSourcesOrEmpty is Objects.requireNonNullElse(getChunkSources(...), emptyChunkSources). CITE.
func (s *skyLightEngine) getChunkSourcesOrEmpty(chunkX, chunkZ int) *ChunkSkyLightSources {
	src := s.getChunkSources(chunkX, chunkZ)
	if src == nil {
		return s.emptyChunkSources
	}
	return src
}
