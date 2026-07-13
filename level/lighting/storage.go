package lighting

// dataLayerStorageMap ports net.minecraft.world.level.lighting.DataLayerStorageMap: the section ->
// DataLayer table PLUS vanilla's 2-entry LRU read cache. The cache is NOT double-buffer machinery
// (that is copy()/disableCache, which single-buffering legitimately omits) — it is a PURE PERF
// optimization on the read hot path: getLayer is called on every getStoredLevel/setStoredLevel/
// storingLightForSection during light propagation, which walks a handful of adjacent sections
// repeatedly. Without the cache every one of those was a Go map hash lookup; a live CPU profile
// showed getLayer -> mapaccess1_fast64 at ~15% of total server CPU (13s of 87s) while a player
// loaded chunks, the dominant cost of skyLightEngine.propagateIncrease. The 2-entry cache turns the
// overwhelmingly-repeated same-section reads into an array compare, cutting the hash traffic. Ported
// 1:1 from DataLayerStorageMap.getLayer (CACHE_SIZE == 2, MRU-at-index-0 shift on miss). CITE:
// net.minecraft.world.level.lighting.DataLayerStorageMap (lastSectionKeys/lastSections/cacheEnabled).
type dataLayerStorageMap struct {
	m map[int64]*DataLayer
	// 2-entry LRU read cache (DataLayerStorageMap.CACHE_SIZE == 2). lastSectionKeys[i]==cacheMiss is
	// the empty sentinel (vanilla uses Long.MAX_VALUE); a real section node never equals it. cacheEnabled
	// mirrors the field (vanilla disables the cache on the visible copy; single-buffered we keep it on).
	lastSectionKeys [2]int64
	lastSections    [2]*DataLayer
	cacheEnabled    bool
	// sky-only fields (see SkyLightSectionStorage.SkyDataLayerStorageMap); block storage leaves them
	// unused. currentLowestY == the lowest section-Y that has ever held a layer; topSections[zeroNode]
	// == 1 + the highest section-Y with a layer for that column. CITE: SkyDataLayerStorageMap.
	sky          bool
	currentLowestY int
	topSections    map[int64]int
}

// cacheMiss is the empty-slot sentinel for the 2-entry read cache (vanilla's Long.MAX_VALUE, which no
// real packed section node equals). CITE: DataLayerStorageMap.<init> Arrays.fill(lastSectionKeys, MAX).
const cacheMiss = int64(^uint64(0) >> 1)

func newBlockStorageMap() *dataLayerStorageMap {
	d := &dataLayerStorageMap{m: make(map[int64]*DataLayer)}
	d.clearCache()
	d.cacheEnabled = true
	return d
}

func newSkyStorageMap() *dataLayerStorageMap {
	d := &dataLayerStorageMap{
		m:              make(map[int64]*DataLayer),
		sky:            true,
		currentLowestY: maxInt, // Integer.MAX_VALUE
		topSections:    make(map[int64]int),
	}
	d.clearCache()
	d.cacheEnabled = true
	return d
}

const maxInt = int(^uint(0) >> 1)

// clearCache resets both cache slots to the empty sentinel (DataLayerStorageMap.clearCache). Called
// at construction and whenever a layer is inserted/removed so a stale *DataLayer is never returned.
func (d *dataLayerStorageMap) clearCache() {
	d.lastSectionKeys[0] = cacheMiss
	d.lastSectionKeys[1] = cacheMiss
	d.lastSections[0] = nil
	d.lastSections[1] = nil
}

func (d *dataLayerStorageMap) hasLayer(sectionNode int64) bool { _, ok := d.m[sectionNode]; return ok }

// getLayer ports DataLayerStorageMap.getLayer with the 2-entry MRU read cache. On a cache hit the
// map hash is skipped entirely; on a miss the map is consulted and (when non-nil) the result is
// pushed to slot 0, shifting the previous slot 0 to slot 1 (LRU-2). CITE: DataLayerStorageMap.getLayer.
func (d *dataLayerStorageMap) getLayer(sectionNode int64) *DataLayer {
	if d.cacheEnabled {
		for i := 0; i < 2; i++ {
			if d.lastSectionKeys[i] == sectionNode {
				return d.lastSections[i]
			}
		}
	}
	layer := d.m[sectionNode]
	if layer != nil && d.cacheEnabled {
		// Shift slot 0 -> slot 1, install the freshly-read layer at slot 0 (MRU). Mirrors the bytecode's
		// System.arraycopy-of-one then store at index 0.
		d.lastSectionKeys[1] = d.lastSectionKeys[0]
		d.lastSections[1] = d.lastSections[0]
		d.lastSectionKeys[0] = sectionNode
		d.lastSections[0] = layer
	}
	return layer
}

func (d *dataLayerStorageMap) setLayer(sectionNode int64, layer *DataLayer) {
	d.m[sectionNode] = layer
	// A mutation must invalidate the read cache so a subsequent getLayer never returns a stale slot
	// (vanilla clears the cache on any structural change via clearCache in the setter path).
	d.clearCache()
}

func (d *dataLayerStorageMap) removeLayer(sectionNode int64) *DataLayer {
	l := d.m[sectionNode]
	delete(d.m, sectionNode)
	d.clearCache()
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
