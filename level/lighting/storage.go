package lighting

// dataLayerStorageMap ports net.minecraft.world.level.lighting.DataLayerStorageMap: the section ->
// DataLayer table. Vanilla adds a 2-entry read cache (pure perf) and a copy()/disableCache used for
// the visible/updating double buffer; per the package-level optimization note we run single-buffered,
// so the cache and copy() are unnecessary and omitted (value-identical). CITE: DataLayerStorageMap.
type dataLayerStorageMap struct {
	m map[int64]*DataLayer
	// sky-only fields (see SkyLightSectionStorage.SkyDataLayerStorageMap); block storage leaves them
	// unused. currentLowestY == the lowest section-Y that has ever held a layer; topSections[zeroNode]
	// == 1 + the highest section-Y with a layer for that column. CITE: SkyDataLayerStorageMap.
	sky          bool
	currentLowestY int
	topSections    map[int64]int
}

func newBlockStorageMap() *dataLayerStorageMap {
	return &dataLayerStorageMap{m: make(map[int64]*DataLayer)}
}

func newSkyStorageMap() *dataLayerStorageMap {
	return &dataLayerStorageMap{
		m:              make(map[int64]*DataLayer),
		sky:            true,
		currentLowestY: maxInt, // Integer.MAX_VALUE
		topSections:    make(map[int64]int),
	}
}

const maxInt = int(^uint(0) >> 1)

func (d *dataLayerStorageMap) hasLayer(sectionNode int64) bool { _, ok := d.m[sectionNode]; return ok }

func (d *dataLayerStorageMap) getLayer(sectionNode int64) *DataLayer { return d.m[sectionNode] }

func (d *dataLayerStorageMap) setLayer(sectionNode int64, layer *DataLayer) { d.m[sectionNode] = layer }

func (d *dataLayerStorageMap) removeLayer(sectionNode int64) *DataLayer {
	l := d.m[sectionNode]
	delete(d.m, sectionNode)
	return l
}

// topSectionsGet mirrors Long2IntOpenHashMap.get with defaultReturnValue == currentLowestY. CITE:
// SkyDataLayerStorageMap topSections.defaultReturnValue(currentLowestY).
func (d *dataLayerStorageMap) topSectionsGet(zeroNode int64) int {
	if v, ok := d.topSections[zeroNode]; ok {
		return v
	}
	return d.currentLowestY
}

// sectionState ports LayerLightSectionStorage.SectionState (packed byte: bit5 = hasData, bits0..4 =
// neighbor count 0..26). CITE: LayerLightSectionStorage$SectionState.
const (
	sectionHasDataBit = 0x20
)

func sectionStateHasData(state byte, hasData bool) byte {
	if hasData {
		return state | 0x20
	}
	return state &^ 0x20
}
func sectionStateGetHasData(state byte) bool { return state&0x20 != 0 }
func sectionStateNeighborCountSet(state byte, count int) byte {
	// throws if out of [0,26]; the caller guarantees the range as in the jar.
	return byte(int(state)&^0x1F | (count & 0x1F))
}
func sectionStateNeighborCountGet(state byte) int { return int(state & 0x1F) }

// layerStorage ports net.minecraft.world.level.lighting.LayerLightSectionStorage (single-buffered).
// It owns the section->DataLayer map, the per-section state bytes (neighbor counting for
// storingLightForSection), the columnsWithSources set (light-enabled columns), and queued section
// data. The double-buffer machinery (visible/updating, changedSections, swapSectionMap,
// markNewInconsistencies with a toRemove reconciliation) is collapsed: since reads never race an
// update, `updatingSectionData == visibleSectionData` at every observable point. We keep
// `sectionsAffectedByLightUpdates` conceptually as the affected-section notification hook but the
// synchronous engine has no cross-thread listener, so it is a no-op set. CITE:
// LayerLightSectionStorage.
type layerStorage struct {
	data              *dataLayerStorageMap
	sectionStates     map[int64]byte
	columnsWithSources map[int64]struct{}
	queuedSections    map[int64]*DataLayer
	toRemove          map[int64]struct{}
	hasInconsistencies bool

	// subclass hooks (sky overrides onNodeAdded/onNodeRemoved/createDataLayer). set for sky storage.
	sky *skyStorageExt
}

func newLayerStorage(m *dataLayerStorageMap) *layerStorage {
	return &layerStorage{
		data:               m,
		sectionStates:      make(map[int64]byte),
		columnsWithSources: make(map[int64]struct{}),
		queuedSections:     make(map[int64]*DataLayer),
		toRemove:           make(map[int64]struct{}),
	}
}

// storingLightForSection ports LayerLightSectionStorage.storingLightForSection = getDataLayer(node,
// updating) != null. CITE.
func (s *layerStorage) storingLightForSection(sectionNode int64) bool {
	return s.data.getLayer(sectionNode) != nil
}

// getDataLayer(updating) — single-buffered, so always the one map. CITE.
func (s *layerStorage) getDataLayer(sectionNode int64) *DataLayer { return s.data.getLayer(sectionNode) }

// getDataLayerData ports LayerLightSectionStorage.getDataLayerData: queued layer preferred, else the
// stored layer. CITE.
func (s *layerStorage) getDataLayerData(sectionNode int64) *DataLayer {
	if l, ok := s.queuedSections[sectionNode]; ok {
		return l
	}
	return s.data.getLayer(sectionNode)
}

// getStoredLevel ports LayerLightSectionStorage.getStoredLevel. CITE.
func (s *layerStorage) getStoredLevel(blockNode int64) int {
	sectionNode := blockNodeToSection(blockNode)
	layer := s.data.getLayer(sectionNode)
	return layer.Get(sectionRelative(blockGetX(blockNode)), sectionRelative(blockGetY(blockNode)), sectionRelative(blockGetZ(blockNode)))
}

// setStoredLevel ports LayerLightSectionStorage.setStoredLevel. In vanilla the changedSections check
// triggers a copy-on-write into the updating buffer; single-buffered we write the one layer directly.
// The aroundAndAtBlockPos affected-sections mark is a listener notification (no-op here). CITE.
func (s *layerStorage) setStoredLevel(blockNode int64, level int) {
	sectionNode := blockNodeToSection(blockNode)
	layer := s.data.getLayer(sectionNode)
	layer.Set(sectionRelative(blockGetX(blockNode)), sectionRelative(blockGetY(blockNode)), sectionRelative(blockGetZ(blockNode)), level)
}

// createDataLayer ports LayerLightSectionStorage.createDataLayer (block) / SkyLightSectionStorage
// override (sky). CITE.
func (s *layerStorage) createDataLayer(sectionNode int64) *DataLayer {
	if s.sky != nil {
		return s.sky.createDataLayer(s, sectionNode)
	}
	if q, ok := s.queuedSections[sectionNode]; ok {
		return q
	}
	return NewDataLayer()
}

func (s *layerStorage) hasInconsistenciesFlag() bool { return s.hasInconsistencies }

// setLightEnabled ports LayerLightSectionStorage.setLightEnabled. CITE.
func (s *layerStorage) setLightEnabled(zeroNode int64, enable bool) {
	if enable {
		s.columnsWithSources[zeroNode] = struct{}{}
	} else {
		delete(s.columnsWithSources, zeroNode)
	}
}

// lightOnInSection ports LayerLightSectionStorage.lightOnInSection. CITE.
func (s *layerStorage) lightOnInSection(sectionNode int64) bool {
	_, ok := s.columnsWithSources[getZeroNode(sectionNode)]
	return ok
}

// lightOnInColumn ports LayerLightSectionStorage.lightOnInColumn. CITE.
func (s *layerStorage) lightOnInColumn(sectionZeroNode int64) bool {
	_, ok := s.columnsWithSources[sectionZeroNode]
	return ok
}

// queueSectionData ports LayerLightSectionStorage.queueSectionData. CITE.
func (s *layerStorage) queueSectionData(sectionNode int64, data *DataLayer) {
	if data != nil {
		s.queuedSections[sectionNode] = data
		s.hasInconsistencies = true
	} else {
		delete(s.queuedSections, sectionNode)
	}
}

// updateSectionStatus ports LayerLightSectionStorage.updateSectionStatus: maintain hasData + the
// 26-neighbor counts, initializing/removing sections as their state crosses zero. CITE.
func (s *layerStorage) updateSectionStatus(sectionNode int64, sectionEmpty bool) {
	state := s.sectionStates[sectionNode]
	newState := sectionStateHasData(state, !sectionEmpty)
	if state == newState {
		return
	}
	s.putSectionState(sectionNode, newState)
	neighborIncrement := 1
	if sectionEmpty {
		neighborIncrement = -1
	}
	for offsetX := -1; offsetX <= 1; offsetX++ {
		for offsetY := -1; offsetY <= 1; offsetY++ {
			for offsetZ := -1; offsetZ <= 1; offsetZ++ {
				if offsetX == 0 && offsetY == 0 && offsetZ == 0 {
					continue
				}
				neighborNode := sectionOffset(sectionNode, offsetX, offsetY, offsetZ)
				neighborState := s.sectionStates[neighborNode]
				s.putSectionState(neighborNode, sectionStateNeighborCountSet(neighborState, sectionStateNeighborCountGet(neighborState)+neighborIncrement))
			}
		}
	}
}

// putSectionState ports LayerLightSectionStorage.putSectionState. CITE.
func (s *layerStorage) putSectionState(sectionNode int64, state byte) {
	if state != 0 {
		old := s.sectionStates[sectionNode]
		s.sectionStates[sectionNode] = state
		if old == 0 {
			s.initializeSection(sectionNode)
		}
	} else {
		if old, ok := s.sectionStates[sectionNode]; ok && old != 0 {
			delete(s.sectionStates, sectionNode)
			s.removeSection(sectionNode)
		}
	}
}

// initializeSection ports LayerLightSectionStorage.initializeSection. CITE.
func (s *layerStorage) initializeSection(sectionNode int64) {
	if _, ok := s.toRemove[sectionNode]; ok {
		delete(s.toRemove, sectionNode)
		return
	}
	s.data.setLayer(sectionNode, s.createDataLayer(sectionNode))
	// changedSections.add — collapsed (single buffer).
	s.onNodeAdded(sectionNode)
	// markSectionAndNeighborsAsAffected — listener notification (no-op).
	s.hasInconsistencies = true
}

// removeSection ports LayerLightSectionStorage.removeSection. CITE.
func (s *layerStorage) removeSection(sectionNode int64) {
	s.toRemove[sectionNode] = struct{}{}
	s.hasInconsistencies = true
}

func (s *layerStorage) onNodeAdded(sectionNode int64) {
	if s.sky != nil {
		s.sky.onNodeAdded(s, sectionNode)
	}
}
func (s *layerStorage) onNodeRemoved(sectionNode int64) {
	if s.sky != nil {
		s.sky.onNodeRemoved(s, sectionNode)
	}
}

// markNewInconsistencies ports LayerLightSectionStorage.markNewInconsistencies. Single-buffered, the
// visible/updating reconciliation collapses; what remains observable is: (1) drain toRemove, removing
// layers + running onNodeRemoved, honoring columnsToRetainQueuedDataFor (not modeled — retainData is
// only used by the chunk-unload path we don't drive), and (2) promote queued section data into the
// map for sections that are storing light. CITE: LayerLightSectionStorage.markNewInconsistencies.
func (s *layerStorage) markNewInconsistencies() {
	if !s.hasInconsistencies {
		return
	}
	s.hasInconsistencies = false
	for node := range s.toRemove {
		delete(s.queuedSections, node)
		s.data.removeLayer(node)
	}
	for node := range s.toRemove {
		s.onNodeRemoved(node)
	}
	s.toRemove = make(map[int64]struct{})
	for sectionNode, data := range s.queuedSections {
		if !s.storingLightForSection(sectionNode) {
			continue
		}
		if s.data.getLayer(sectionNode) != data {
			s.data.setLayer(sectionNode, data)
		}
		delete(s.queuedSections, sectionNode)
	}
}

// swapSectionMap ports LayerLightSectionStorage.swapSectionMap — single-buffered => no-op (the
// visible snapshot IS the updating map; the affected-section onLightUpdate notifications have no
// synchronous listener). CITE.
func (s *layerStorage) swapSectionMap() {}
