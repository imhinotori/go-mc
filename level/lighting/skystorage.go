package lighting

import "github.com/imhinotori/sulfur/level/block"

// skyStorageExt ports the sky-specific overrides in
// net.minecraft.world.level.lighting.SkyLightSectionStorage: onNodeAdded/onNodeRemoved maintain the
// per-column topSections + currentLowestY; createDataLayer synthesizes a layer from the one above
// (repeatFirstLayer) or a fully-lit/empty layer; getLightValue implements the above-top => 15 rule.
// CITE: SkyLightSectionStorage.
type skyStorageExt struct{}

// onNodeAdded ports SkyLightSectionStorage.onNodeAdded. CITE.
func (skyStorageExt) onNodeAdded(s *layerStorage, sectionNode int64) {
	y := sectionY(sectionNode)
	if s.data.currentLowestY > y {
		s.data.currentLowestY = y
		// topSections.defaultReturnValue(currentLowestY) — modeled by topSectionsGet returning
		// currentLowestY for absent keys.
	}
	zeroNode := getZeroNode(sectionNode)
	oldTop := s.data.topSectionsGet(zeroNode)
	if oldTop < y+1 {
		s.data.topSections[zeroNode] = y + 1
	}
}

// onNodeRemoved ports SkyLightSectionStorage.onNodeRemoved. CITE.
func (skyStorageExt) onNodeRemoved(s *layerStorage, sectionNode int64) {
	zeroNode := getZeroNode(sectionNode)
	y := sectionY(sectionNode)
	if s.data.topSectionsGet(zeroNode) == y+1 {
		newTopSection := sectionNode
		for !s.storingLightForSection(newTopSection) && hasLightDataAtOrBelow(s, y) {
			y--
			newTopSection = sectionOffsetDir(newTopSection, block.Down)
		}
		if s.storingLightForSection(newTopSection) {
			s.data.topSections[zeroNode] = y + 1
		} else {
			delete(s.data.topSections, zeroNode)
		}
	}
}

// createDataLayer ports SkyLightSectionStorage.createDataLayer. CITE.
func (skyStorageExt) createDataLayer(s *layerStorage, sectionNode int64) *DataLayer {
	if q, ok := s.queuedSections[sectionNode]; ok {
		return q
	}
	topSection := s.data.topSectionsGet(getZeroNode(sectionNode))
	if topSection == s.data.currentLowestY || sectionY(sectionNode) >= topSection {
		if s.lightOnInSection(sectionNode) {
			return NewDataLayerDefault(15)
		}
		return NewDataLayer()
	}
	aboveSection := sectionOffsetDir(sectionNode, block.Up)
	var aboveData *DataLayer
	for {
		aboveData = s.data.getLayer(aboveSection)
		if aboveData != nil {
			break
		}
		aboveSection = sectionOffsetDir(aboveSection, block.Up)
	}
	return repeatFirstLayer(aboveData)
}

// repeatFirstLayer ports SkyLightSectionStorage.repeatFirstLayer: if homogenous, a copy; else tile
// the first 128-byte layer (one Y-slice) up all 16 Y-slices. CITE.
func repeatFirstLayer(data *DataLayer) *DataLayer {
	if data.IsDefinitelyHomogenous() {
		return data.Copy()
	}
	input := data.Data()
	output := make([]byte, dataLayerSize)
	for i := 0; i < 16; i++ {
		copy(output[i*128:(i+1)*128], input[0:128])
	}
	return NewDataLayerBytes(output)
}

// hasLightDataAtOrBelow ports SkyLightSectionStorage.hasLightDataAtOrBelow. CITE.
func hasLightDataAtOrBelow(s *layerStorage, sectionY int) bool {
	return sectionY >= s.data.currentLowestY
}

// isAboveData ports SkyLightSectionStorage.isAboveData. CITE.
func isAboveData(s *layerStorage, sectionNode int64) bool {
	zeroNode := getZeroNode(sectionNode)
	topSection := s.data.topSectionsGet(zeroNode)
	return topSection == s.data.currentLowestY || sectionY(sectionNode) >= topSection
}

// getTopSectionY / getBottomSectionY port the same-named SkyLightSectionStorage methods. CITE.
func getTopSectionY(s *layerStorage, zeroNode int64) int { return s.data.topSectionsGet(zeroNode) }
func getBottomSectionY(s *layerStorage) int              { return s.data.currentLowestY }

// skyGetLightValue ports SkyLightSectionStorage.getLightValue(blockNode) (== getLightValue(node,
// false) — the visible read). CITE.
func skyGetLightValue(s *layerStorage, blockNode int64) int {
	sectionNode := blockNodeToSection(blockNode)
	secY := sectionY(sectionNode)
	topSection := s.data.topSectionsGet(getZeroNode(sectionNode))
	if topSection == s.data.currentLowestY || secY >= topSection {
		// updating=false path: return 15 (no lightOnInSection gate on the visible read).
		return 15
	}
	layer := s.data.getLayer(sectionNode)
	if layer == nil {
		flatNode := blockGetFlatIndex(blockNode)
		for layer == nil {
			secY++
			if secY >= topSection {
				return 15
			}
			sectionNode = sectionOffsetDir(sectionNode, block.Up)
			layer = s.data.getLayer(sectionNode)
		}
		blockNode = flatNode
	}
	return layer.Get(sectionRelative(blockGetX(blockNode)), sectionRelative(blockGetY(blockNode)), sectionRelative(blockGetZ(blockNode)))
}

// blockGetLightValue ports BlockLightSectionStorage.getLightValue(blockNode). CITE.
func blockGetLightValue(s *layerStorage, blockNode int64) int {
	sectionNode := blockNodeToSection(blockNode)
	layer := s.data.getLayer(sectionNode)
	if layer == nil {
		return 0
	}
	return layer.Get(sectionRelative(blockGetX(blockNode)), sectionRelative(blockGetY(blockNode)), sectionRelative(blockGetZ(blockNode)))
}
