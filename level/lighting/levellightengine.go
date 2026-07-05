package lighting

import "github.com/imhinotori/sulfur/level/block"

// LightLayer ports net.minecraft.world.level.LightLayer. CITE.
type LightLayer int

const (
	LightLayerSky   LightLayer = iota // SKY
	LightLayerBlock                   // BLOCK
)

// LightSectionPadding ports LevelLightEngine.LIGHT_SECTION_PADDING = 1 (one extra light section
// above and below the block sections). CITE: LevelLightEngine.LIGHT_SECTION_PADDING.
const LightSectionPadding = 1

// LevelLightEngine ports net.minecraft.world.level.lighting.LevelLightEngine — the facade the server
// calls. It owns the optional block + sky engines and fans work out to both. CITE: LevelLightEngine.
type LevelLightEngine struct {
	chunkSource LightChunkGetter
	blockEngine *blockLightEngine
	skyEngine   *skyLightEngine
}

// NewLevelLightEngine ports LevelLightEngine(chunkSource, hasBlockLight, hasSkyLight). CITE.
func NewLevelLightEngine(chunkSource LightChunkGetter, hasBlockLight, hasSkyLight bool) *LevelLightEngine {
	l := &LevelLightEngine{chunkSource: chunkSource}
	if hasBlockLight {
		l.blockEngine = newBlockLightEngine(chunkSource)
	}
	if hasSkyLight {
		l.skyEngine = newSkyLightEngine(chunkSource)
	}
	return l
}

// CheckBlock ports LevelLightEngine.checkBlock. CITE.
func (l *LevelLightEngine) CheckBlock(x, y, z int) {
	node := blockAsLong(x, y, z)
	if l.blockEngine != nil {
		l.blockEngine.checkBlock(node)
	}
	if l.skyEngine != nil {
		l.skyEngine.checkBlock(node)
	}
}

// HasLightWork ports LevelLightEngine.hasLightWork. CITE.
func (l *LevelLightEngine) HasLightWork() bool {
	if l.skyEngine != nil && l.skyEngine.hasLightWork() {
		return true
	}
	return l.blockEngine != nil && l.blockEngine.hasLightWork()
}

// RunLightUpdates ports LevelLightEngine.runLightUpdates. CITE.
func (l *LevelLightEngine) RunLightUpdates() int {
	count := 0
	if l.blockEngine != nil {
		count += l.blockEngine.runLightUpdates()
	}
	if l.skyEngine != nil {
		count += l.skyEngine.runLightUpdates()
	}
	return count
}

// UpdateSectionStatus ports LevelLightEngine.updateSectionStatus. CITE.
func (l *LevelLightEngine) UpdateSectionStatus(sectionX, sectionY, sectionZ int, sectionEmpty bool) {
	node := sectionAsLong(sectionX, sectionY, sectionZ)
	if l.blockEngine != nil {
		l.blockEngine.updateSectionStatus(node, sectionEmpty)
	}
	if l.skyEngine != nil {
		l.skyEngine.updateSectionStatus(node, sectionEmpty)
	}
}

// SetLightEnabled ports LevelLightEngine.setLightEnabled. The block engine uses the base impl; the
// sky engine uses its override (which also fills fully-lit sections). CITE.
func (l *LevelLightEngine) SetLightEnabled(chunkX, chunkZ int, enable bool) {
	if l.blockEngine != nil {
		l.blockEngine.setLightEnabledColumn(chunkX, chunkZ, enable)
	}
	if l.skyEngine != nil {
		l.skyEngine.setLightEnabled(chunkX, chunkZ, enable)
	}
}

// PropagateLightSources ports LevelLightEngine.propagateLightSources. CITE.
func (l *LevelLightEngine) PropagateLightSources(chunkX, chunkZ int) {
	if l.blockEngine != nil {
		l.blockEngine.propagateLightSources(chunkX, chunkZ)
	}
	if l.skyEngine != nil {
		l.skyEngine.propagateLightSources(chunkX, chunkZ)
	}
}

// QueueSectionData ports LevelLightEngine.queueSectionData. CITE.
func (l *LevelLightEngine) QueueSectionData(layer LightLayer, sectionX, sectionY, sectionZ int, data *DataLayer) {
	node := sectionAsLong(sectionX, sectionY, sectionZ)
	if layer == LightLayerBlock {
		if l.blockEngine != nil {
			l.blockEngine.queueSectionData(node, data)
		}
	} else if l.skyEngine != nil {
		l.skyEngine.queueSectionData(node, data)
	}
}

// GetDataLayerData ports LightEngine.getDataLayerData via getLayerListener — returns the current
// DataLayer for a section (queued preferred), used to hand light to the wire. CITE:
// LayerLightEventListener.getDataLayerData.
func (l *LevelLightEngine) GetDataLayerData(layer LightLayer, sectionX, sectionY, sectionZ int) *DataLayer {
	node := sectionAsLong(sectionX, sectionY, sectionZ)
	if layer == LightLayerBlock {
		if l.blockEngine != nil {
			return l.blockEngine.getDataLayerData(node)
		}
		return nil
	}
	if l.skyEngine != nil {
		return l.skyEngine.getDataLayerData(node)
	}
	return nil
}

// SkySectionIsAboveData reports whether a sky-light section sits at or above the column's stored top
// (SkyLightSectionStorage.isAboveData). Such a section has NO stored DataLayer but reads as fully lit
// (skyGetLightValue returns 15 there). The wire assembler uses this to emit a full-15 sky array for
// above-terrain sections (the client would otherwise render them dark under this repo's simplified
// light mask). CITE: SkyLightSectionStorage.isAboveData / getLightValue (above-top => 15).
func (l *LevelLightEngine) SkySectionIsAboveData(sectionX, sectionY, sectionZ int) bool {
	if l.skyEngine == nil {
		return false
	}
	return isAboveData(l.skyEngine.storage, sectionAsLong(sectionX, sectionY, sectionZ))
}

// GetRawBrightness ports LevelLightEngine.getRawBrightness(pos, skyDampen): max(blockLight,
// skyLight - skyDampen). CITE: LevelLightEngine.getRawBrightness.
func (l *LevelLightEngine) GetRawBrightness(x, y, z, skyDampen int) int {
	node := blockAsLong(x, y, z)
	skyLight := 0
	if l.skyEngine != nil {
		skyLight = l.skyEngine.getLightValue(node) - skyDampen
	}
	blockLight := 0
	if l.blockEngine != nil {
		blockLight = l.blockEngine.getLightValue(node)
	}
	if blockLight > skyLight {
		return blockLight
	}
	return skyLight
}

// GetBrightness returns the light value for a single layer (LayerLightEventListener.getLightValue).
// CITE: LightEngine.getLightValue.
func (l *LevelLightEngine) GetBrightness(layer LightLayer, x, y, z int) int {
	node := blockAsLong(x, y, z)
	if layer == LightLayerBlock {
		if l.blockEngine != nil {
			return l.blockEngine.getLightValue(node)
		}
		return 0
	}
	if l.skyEngine != nil {
		return l.skyEngine.getLightValue(node)
	}
	return 0
}

// GetLightSectionCount ports LevelLightEngine.getLightSectionCount = sectionsCount + 2. CITE.
func (l *LevelLightEngine) GetLightSectionCount() int { return l.chunkSource.SectionsCount() + 2 }

// GetMinLightSection ports LevelLightEngine.getMinLightSection = minSectionY - 1. CITE.
func (l *LevelLightEngine) GetMinLightSection() int { return l.chunkSource.MinSectionY() - 1 }

// GetMaxLightSection ports LevelLightEngine.getMaxLightSection. CITE.
func (l *LevelLightEngine) GetMaxLightSection() int {
	return l.GetMinLightSection() + l.GetLightSectionCount()
}

// compile-time: engines satisfy engineImpl.
var (
	_ engineImpl    = (*blockLightEngine)(nil)
	_ engineImpl    = (*skyLightEngine)(nil)
	_ block.StateID = 0
)
