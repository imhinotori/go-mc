package world

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/lighting"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// This file wires the 1:1 net.minecraft.world.level.lighting engine into chunk sealing. It replaces
// the old fullSkyLight() seal (which hard-set every section's SkyLight to level 15 and left
// BlockLight nil) with the REAL computed sky+block DataLayers. CITE: the initial-lighting sequence
// is ThreadedLevelLightEngine.initializeLight + lightChunk (updateSectionStatus per section ->
// setLightEnabled -> propagateLightSources -> runLightUpdates), driven here synchronously over a
// LightChunkGetter view of the freshly-generated neighborhood.

// lightChunkView adapts one *level.Chunk (a column) to lighting.LightChunk. blocks are read from the
// section palette; the sky-source heightmap is computed once via ChunkSkyLightSources.FillFrom; the
// emissive-block enumeration scans the palette-backed sections.
type lightChunkView struct {
	chunk         *level.Chunk
	cx, cz        int
	minSectionY   int
	sectionsCount int
	sources       *lighting.ChunkSkyLightSources
	air           block.StateID
}

func newLightChunkView(chunk *level.Chunk, cx, cz, minSectionY, sectionsCount int, air block.StateID) *lightChunkView {
	v := &lightChunkView{
		chunk:         chunk,
		cx:            cx,
		cz:            cz,
		minSectionY:   minSectionY,
		sectionsCount: sectionsCount,
		air:           air,
	}
	v.buildSources()
	return v
}

// getBlock returns the block state at WORLD (x,y,z) for this column. Out-of-column or out-of-range Y
// reads return air (the neighborhood clips to the chunk).
func (v *lightChunkView) getBlock(x, y, z int) block.StateID {
	secIdx := (y - v.minSectionY*16) >> 4
	if secIdx < 0 || secIdx >= len(v.chunk.Sections) {
		return v.air
	}
	local := (y&15)<<8 | (z&15)<<4 | (x & 15)
	return v.chunk.Sections[secIdx].GetBlock(local)
}

func (v *lightChunkView) GetBlockState(x, y, z int) block.StateID { return v.getBlock(x, y, z) }

func (v *lightChunkView) SkyLightSources() *lighting.ChunkSkyLightSources { return v.sources }

func (v *lightChunkView) FindBlockLightSources(fn func(x, y, z int, state block.StateID)) {
	minX, minZ := v.cx*16, v.cz*16
	minY := v.minSectionY * 16
	for si := range v.chunk.Sections {
		sec := &v.chunk.Sections[si]
		// Skip sections with no emissive block by scanning the palette first (cheap).
		anyEmissive := false
		for _, st := range sec.States.Palette() {
			if block.LightEmission(st) > 0 {
				anyEmissive = true
				break
			}
		}
		if !anyEmissive {
			continue
		}
		baseY := minY + si*16
		for ly := 0; ly < 16; ly++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					st := sec.GetBlock(ly<<8 | lz<<4 | lx)
					if block.LightEmission(st) > 0 {
						fn(minX+lx, baseY+ly, minZ+lz, st)
					}
				}
			}
		}
	}
}

// buildSources computes the sky-source heightmap for this column via ChunkSkyLightSources.FillFrom.
// CITE: ChunkSkyLightSources.fillFrom.
func (v *lightChunkView) buildSources() {
	v.sources = lighting.NewChunkSkyLightSources(v.minSectionY)
	// Find the highest non-air section (ChunkAccess.getHighestFilledSectionIndex).
	topSectionY := v.minSectionY
	allAir := true
	for si := len(v.chunk.Sections) - 1; si >= 0; si-- {
		if !sectionAllAir(&v.chunk.Sections[si]) {
			topSectionY = v.minSectionY + si
			allAir = false
			break
		}
	}
	read := func(x, y, z int) block.StateID { return v.getBlock(x, y, z) }
	v.sources.FillFrom(read, topSectionY, allAir)
}

// sectionAllAir reports whether a section's palette contains only air (LevelChunkSection.hasOnlyAir).
func sectionAllAir(s *level.Section) bool {
	for _, st := range s.States.Palette() {
		if !block.IsAir(st) {
			return false
		}
	}
	return true
}

// lightNeighborhood is the LightChunkGetter over a freshly-generated 3x3, keyed by chunk coord.
type lightNeighborhood struct {
	views         map[[2]int]*lightChunkView
	minSectionY   int
	sectionsCount int
}

func (n *lightNeighborhood) GetChunkForLighting(cx, cz int) lighting.LightChunk {
	if v, ok := n.views[[2]int{cx, cz}]; ok {
		return v
	}
	return nil
}
func (n *lightNeighborhood) MinSectionY() int   { return n.minSectionY }
func (n *lightNeighborhood) SectionsCount() int { return n.sectionsCount }

// ComputeChunkLight computes the sky+block DataLayers for the center chunk (at centerPos) and writes
// them into center.Sections[*].SkyLight/.BlockLight, replacing the fullSkyLight() seal. `neighbors`
// supplies the fully-generated 3x3 (center + its 8 neighbors) keyed by [cx,cz]; missing neighbors are
// tolerated (edge columns read as air, exactly as an unloaded LightChunk). It runs the engine over
// the whole 3x3 so cross-chunk edge light is correct, then extracts only the center's per-section
// light. minSectionY/secs describe the world geometry; air is the resolved air StateID.
//
// The wire format is unchanged: the per-section []byte light arrays are handed to the section exactly
// as before; only their contents (now computed) differ. A fully-lit above-terrain sky section reads
// back as 0xFF; below-surface / occluded cells attenuate; block light radiates from emitters.
func ComputeChunkLight(centerPos level.ChunkPos, neighbors map[[2]int]*level.Chunk, minSectionY, secs int, air block.StateID, hasSkyLight bool) {
	center := neighbors[[2]int{int(centerPos[0]), int(centerPos[1])}]
	if center == nil {
		return
	}
	nb := &lightNeighborhood{
		views:         make(map[[2]int]*lightChunkView, 9),
		minSectionY:   minSectionY,
		sectionsCount: secs,
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			cx := int(centerPos[0]) + dx
			cz := int(centerPos[1]) + dz
			ch := neighbors[[2]int{cx, cz}]
			if ch == nil {
				continue
			}
			nb.views[[2]int{cx, cz}] = newLightChunkView(ch, cx, cz, minSectionY, secs, air)
		}
	}

	eng := lighting.NewLevelLightEngine(nb, true, hasSkyLight) // block engine always; sky engine only where the dimension has sky light (overworld yes; nether/end no). CITE: DimensionType.hasSkyLight().

	// Drive the initial-light sequence over every loaded chunk in the 3x3.
	var coords [][2]int
	for c := range nb.views {
		coords = append(coords, c)
	}
	for _, c := range coords {
		for si := 0; si < secs; si++ {
			sec := &nb.views[c].chunk.Sections[si]
			eng.UpdateSectionStatus(c[0], minSectionY+si, c[1], sectionAllAir(sec))
		}
	}
	for _, c := range coords {
		eng.SetLightEnabled(c[0], c[1], true)
	}
	for _, c := range coords {
		eng.PropagateLightSources(c[0], c[1])
	}
	eng.RunLightUpdates()

	// Extract the CENTER column's per-section light into the section arrays. The light sections span
	// [minLightSection, maxLightSection) = [minSectionY-1, minSectionY+secs+1); the wire only carries
	// the block sections [minSectionY, minSectionY+secs), which is what center.Sections holds. CITE:
	// LevelLightEngine.getMinLightSection/getLightSectionCount (LIGHT_SECTION_PADDING=1).
	cx, cz := int(centerPos[0]), int(centerPos[1])
	for si := 0; si < secs; si++ {
		secY := minSectionY + si
		sky := eng.GetDataLayerData(lighting.LightLayerSky, cx, secY, cz)
		blk := eng.GetDataLayerData(lighting.LightLayerBlock, cx, secY, cz)
		// An absent sky DataLayer (nil) is left nil: the wire encoder sets NEITHER the data
		// bit NOR the empty bit for that section, so a vanilla 26.2 client keeps/derives its
		// own light there — an above-terrain sky section renders fully lit (15) natively
		// (SkyLightSectionStorage.getLightValue: above-top => 15). No 0xFF compensation fill:
		// the corrected per-layer empty-mask semantics (ClientboundLightUpdatePacketData
		// .prepareSectionData) make the old seal unnecessary and non-vanilla.
		center.Sections[si].SkyLight = materializeLayer(sky)
		center.Sections[si].BlockLight = materializeLayer(blk)
	}
}

// LightPropertiesDiffer ports net.minecraft.world.level.lighting.LightEngine.hasDifferentLightProperties
// (old, new) — the gate LevelChunk.setBlockState consults before calling getLightEngine().checkBlock(pos).
// Vanilla: old==new ? false : (getLightDampening differs || getLightEmission differs ||
// new.useShapeForLightOcclusion() || old.useShapeForLightOcclusion()). We reproduce the first two exactly
// (LightBlock == getLightDampening, LightEmission == getLightEmission) and reproduce the occlusion-shape
// term with the FROM_EMPTY_SHAPE flag (LightShapeIsEmpty == !canOcclude || !useShapeForLightOcclusion):
// !empty(s) implies s.useShapeForLightOcclusion() is true for an occluding block, so "either state is a
// non-empty occlusion shape" is a faithful SUPERSET of vanilla's `new.useShape || old.useShape` term. A
// superset only ever triggers an EXTRA recompute, and a recompute that changes nothing emits no packet
// (the array diff is empty) — so over-triggering is observably identical, never a missed relight. CITE:
// LightEngine.hasDifferentLightProperties.
func LightPropertiesDiffer(oldState, newState block.StateID) bool {
	if oldState == newState {
		return false
	}
	if block.LightBlock(oldState) != block.LightBlock(newState) {
		return true
	}
	if block.LightEmission(oldState) != block.LightEmission(newState) {
		return true
	}
	// new.useShapeForLightOcclusion() || old.useShapeForLightOcclusion(), via the empty-shape flag.
	return !block.LightShapeIsEmpty(oldState) || !block.LightShapeIsEmpty(newState)
}

// ColumnLight carries one column's freshly-recomputed per-section light so the server can assemble a
// ClientboundLightUpdate for it. Sky/Block are indexed by block-section (0 == the bottom section at
// minSectionY); a nil entry means "no light data for that section" (the wire leaves its mask bit unset).
type ColumnLight struct {
	Pos   level.ChunkPos
	Sky   [][]byte
	Block [][]byte
}

// RelightEdit is the incremental-relight seam LevelChunk.setBlockState drives via
// getLightEngine().checkBlock(pos) + the ThreadedLevelLightEngine's runLightUpdates. It is called AFTER
// SetBlock has written newState at pos, ONLY when LightPropertiesDiffer(old,new) is true. Because light is
// a pure function of the block states in the neighborhood, recomputing the affected columns from the live
// loaded chunks (via the same ComputeChunkLight that seals a freshly-generated chunk) converges to exactly
// the values a persistent incremental engine would — the recompute mechanism is the permitted OPTIMIZATION
// substitution (simpler than a live-seeded persistent engine), producing byte-identical light output.
//
// Light propagates up to 15 blocks, so an edit near a chunk border can change a NEIGHBOR column's stored
// light too. We therefore recompute the edited column AND its 8 neighbors (each ComputeChunkLight uses its
// own 3x3, so cross-border light flows both ways), snapshot each column's per-section arrays BEFORE, and
// return a ColumnLight only for the columns whose arrays actually CHANGED. An unloaded column in the 3x3 is
// skipped (its light is not tracked). minSectionY/secs are the world geometry (minY>>4, height>>4); air is
// the resolved air StateID. Tick-owned (runs on the tick goroutine over the tick-owned manager).
func (m *ChunkManager) RelightEdit(pos pk.Position, minSectionY, secs int, air block.StateID, hasSkyLight bool) []ColumnLight {
	col := colOf(pos)
	// The columns whose light this edit can affect: the edited column and its 8 neighbors.
	affected := make([]level.ChunkPos, 0, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			affected = append(affected, level.ChunkPos{col[0] + int32(dx), col[1] + int32(dz)})
		}
	}

	// Snapshot BEFORE arrays for each affected LOADED column so we can detect real changes.
	type snap struct {
		ch     *level.Chunk
		sky    [][]byte
		block  [][]byte
		loaded bool
	}
	before := make(map[level.ChunkPos]*snap, len(affected))
	for _, c := range affected {
		ch, ok := m.Get(c)
		if !ok {
			before[c] = &snap{loaded: false}
			continue
		}
		s := &snap{ch: ch, loaded: true, sky: make([][]byte, len(ch.Sections)), block: make([][]byte, len(ch.Sections))}
		for i := range ch.Sections {
			s.sky[i] = cloneNibble(ch.Sections[i].SkyLight)
			s.block[i] = cloneNibble(ch.Sections[i].BlockLight)
		}
		before[c] = s
	}

	// Recompute each affected column's light from the live loaded neighborhood. Each ComputeChunkLight
	// reads the CURRENT block states (post-edit) and rewrites that center column's stored arrays.
	for _, c := range affected {
		if !before[c].loaded {
			continue
		}
		neighbors := make(map[[2]int]*level.Chunk, 9)
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				nc := level.ChunkPos{c[0] + int32(dx), c[1] + int32(dz)}
				if ch, ok := m.Get(nc); ok {
					neighbors[[2]int{int(nc[0]), int(nc[1])}] = ch
				}
			}
		}
		ComputeChunkLight(c, neighbors, minSectionY, secs, air, hasSkyLight)
	}

	// Emit a ColumnLight only for columns whose light actually changed (mark them dirty so the recomputed
	// light persists with the save). An unchanged column produces no packet.
	var out []ColumnLight
	for _, c := range affected {
		s := before[c]
		if !s.loaded {
			continue
		}
		changed := false
		sky := make([][]byte, len(s.ch.Sections))
		blk := make([][]byte, len(s.ch.Sections))
		for i := range s.ch.Sections {
			sky[i] = s.ch.Sections[i].SkyLight
			blk[i] = s.ch.Sections[i].BlockLight
			if !nibbleEqual(sky[i], s.sky[i]) || !nibbleEqual(blk[i], s.block[i]) {
				changed = true
			}
		}
		if changed {
			m.MarkDirty(c)
			out = append(out, ColumnLight{Pos: c, Sky: sky, Block: blk})
		}
	}
	return out
}

// cloneNibble copies a 2048-byte light array (nil stays nil) for the before-snapshot.
func cloneNibble(src []byte) []byte {
	if src == nil {
		return nil
	}
	out := make([]byte, len(src))
	copy(out, src)
	return out
}

// nibbleEqual reports whether two light arrays are byte-identical (nil == nil; nil != non-nil).
func nibbleEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// computeChunkLightFromNeighborhood is the single-chunk-path adapter: it lifts a *Neighborhood's 3x3
// into the [cx,cz]->*Chunk map ComputeChunkLight wants.
func computeChunkLightFromNeighborhood(view *Neighborhood, minSectionY, secs int, air block.StateID, hasSkyLight bool) {
	neighbors := make(map[[2]int]*level.Chunk, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			cx := int(view.center[0]) + dx
			cz := int(view.center[1]) + dz
			if ch := view.chunks[packPos(level.ChunkPos{int32(cx), int32(cz)})]; ch != nil {
				neighbors[[2]int{cx, cz}] = ch
			}
		}
	}
	ComputeChunkLight(view.center, neighbors, minSectionY, secs, air, hasSkyLight)
}

// materializeLayer returns the 2048-byte light array for a DataLayer, or nil if the layer is absent
// or empty (matching the wire's "no light data for this section" case: a nil array is skipped in the
// light mask). A non-empty layer's backing bytes are copied so later engine mutation can't alias the
// emitted chunk. CITE: level/chunk.go WriteTo lightData (nil SkyLight/BlockLight => bit unset).
func materializeLayer(d *lighting.DataLayer) []byte {
	if d == nil || d.IsEmpty() {
		return nil
	}
	src := d.Data()
	out := make([]byte, 2048)
	copy(out, src)
	return out
}
