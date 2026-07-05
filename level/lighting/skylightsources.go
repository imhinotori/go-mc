package lighting

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
)

// ChunkSkyLightSources ports net.minecraft.world.level.lighting.ChunkSkyLightSources: the per-column
// heightmap of the LOWEST world-Y that still receives full (level-15) sky light in this chunk — the
// "sky sources". The sky engine seeds level-15 down each column to this Y and lets it attenuate
// below. CITE: ChunkSkyLightSources.
//
// Vanilla stores values as (worldY - minY) in a BitStorage; we store the world-Y directly in a
// [256]int (value-identical — the BitStorage is only a packing detail). minY here == level.getMinY()
// - 1 (one below the world floor), exactly as the jar. CITE: ChunkSkyLightSources.<init>.
type ChunkSkyLightSources struct {
	minY   int
	values [256]int // world-Y per column (index = x + z*16)
}

// skySourcesNegativeInfinity ports ChunkSkyLightSources.NEGATIVE_INFINITY. CITE.
const skySourcesNegativeInfinity = math.MinInt32

func skySourcesIndex(x, z int) int { return x + z*16 } // ChunkSkyLightSources.index

// NewChunkSkyLightSources ports ChunkSkyLightSources(LevelHeightAccessor): minY = level.getMinY()-1.
// getMinY() = minSectionY<<4. CITE.
func NewChunkSkyLightSources(minSectionY int) *ChunkSkyLightSources {
	minY := sectionToBlockCoord(minSectionY) - 1
	s := &ChunkSkyLightSources{minY: minY}
	// A freshly-constructed vanilla heightmap is zeroed => get(i) == 0 + minY == minY for every
	// column. Mirror that sentinel until FillFrom runs.
	for i := range s.values {
		s.values[i] = minY
	}
	return s
}

// columnReader reads a block state at a world (x,y,z). It is the block accessor used to (re)build
// the heightmap — mirrors ChunkAccess/BlockGetter.getBlockState in the jar's fillFrom/update.
type columnReader func(x, y, z int) block.StateID

// FillFrom ports ChunkSkyLightSources.fillFrom(ChunkAccess). topSectionY is the section-Y of the
// highest non-empty section; allAir true means the whole chunk is air (highestFilledSectionIndex==
// -1) => fill to minY. CITE: ChunkSkyLightSources.fillFrom + findLowestSourceY.
func (s *ChunkSkyLightSources) FillFrom(read columnReader, topSectionY int, allAir bool) {
	if allAir {
		s.fill(s.minY)
		return
	}
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			edge := s.findLowestSourceY(read, topSectionY, x, z)
			if edge < s.minY {
				edge = s.minY
			}
			s.set(skySourcesIndex(x, z), edge)
		}
	}
}

// findLowestSourceY ports ChunkSkyLightSources.findLowestSourceY: descend the column from the top of
// the highest filled section; the first occluded edge fixes the lowest source Y. CITE.
func (s *ChunkSkyLightSources) findLowestSourceY(read columnReader, topSectionY, x, z int) int {
	topY := sectionToBlockCoord(topSectionY + 1)
	topState := airState
	for y := topY - 1; y > s.minY; y-- {
		bottomState := read(x, y, z)
		if isEdgeOccluded(topState, bottomState) {
			return y + 1 // the Y of the block ABOVE the occluding bottom (topPos.getY())
		}
		topState = bottomState
	}
	return s.minY
}

// Update ports ChunkSkyLightSources.update(BlockGetter, x, y, z): recompute the source Y after a
// block change at (x,y,z). Returns true iff the source Y changed. CITE.
func (s *ChunkSkyLightSources) Update(read columnReader, x, y, z int) bool {
	upperEdgeY := y + 1
	index := skySourcesIndex(x, z)
	currentLowestSourceY := s.get(index)
	if upperEdgeY < currentLowestSourceY {
		return false
	}
	topState := read(x, y+1, z)
	middleState := read(x, y, z)
	if s.updateEdge(read, index, currentLowestSourceY, x, z, y+1, topState, y, middleState) {
		return true
	}
	bottomState := read(x, y-1, z)
	return s.updateEdge(read, index, currentLowestSourceY, x, z, y, middleState, y-1, bottomState)
}

// updateEdge ports ChunkSkyLightSources.updateEdge. topY/bottomY are world-Ys. CITE.
func (s *ChunkSkyLightSources) updateEdge(read columnReader, index, oldTopEdgeY, x, z, topY int, topState block.StateID, bottomY int, bottomState block.StateID) bool {
	checkedEdgeY := topY
	if isEdgeOccluded(topState, bottomState) {
		if checkedEdgeY > oldTopEdgeY {
			s.set(index, checkedEdgeY)
			return true
		}
	} else if checkedEdgeY == oldTopEdgeY {
		s.set(index, s.findLowestSourceBelow(read, x, z, bottomY, bottomState))
		return true
	}
	return false
}

// findLowestSourceBelow ports ChunkSkyLightSources.findLowestSourceBelow. CITE.
func (s *ChunkSkyLightSources) findLowestSourceBelow(read columnReader, x, z, startY int, startState block.StateID) int {
	topY := startY
	topState := startState
	for bottomY := startY - 1; bottomY >= s.minY; bottomY-- {
		bottomState := read(x, bottomY, z)
		if isEdgeOccluded(topState, bottomState) {
			return topY
		}
		topState = bottomState
		topY = bottomY
	}
	return s.minY
}

// isEdgeOccluded ports ChunkSkyLightSources.isEdgeOccluded(top, bottom): bottom dampens>0, or the
// down/up occlusion faces occlude. CITE.
func isEdgeOccluded(topState, bottomState block.StateID) bool {
	if block.LightBlock(bottomState) != 0 {
		return true
	}
	// LightEngine.getOcclusionShape(top, DOWN) vs getOcclusionShape(bottom, UP) via faceShapeOccludes
	// == block.ShapeOccludes(top, bottom, DOWN) (ShapeOccludes already takes from-dir + to-opposite).
	return block.ShapeOccludes(topState, bottomState, block.Down)
}

// GetLowestSourceY ports ChunkSkyLightSources.getLowestSourceY(x,z). CITE.
func (s *ChunkSkyLightSources) GetLowestSourceY(x, z int) int {
	return s.extendSourcesBelowWorld(s.get(skySourcesIndex(x, z)))
}

// GetHighestLowestSourceY ports ChunkSkyLightSources.getHighestLowestSourceY(). CITE.
func (s *ChunkSkyLightSources) GetHighestLowestSourceY() int {
	maxValue := math.MinInt32
	for i := 0; i < len(s.values); i++ {
		// heightmap.get(i) is the STORED value (worldY - minY); the max is over stored values.
		v := s.values[i] - s.minY
		if v > maxValue {
			maxValue = v
		}
	}
	return s.extendSourcesBelowWorld(maxValue + s.minY)
}

func (s *ChunkSkyLightSources) fill(lowestSourceY int) {
	for i := range s.values {
		s.values[i] = lowestSourceY
	}
}

// set/get mirror ChunkSkyLightSources.set/get (which offset by minY into the BitStorage); here we
// store world-Y directly so set/get are identity — kept as methods for a 1:1 call-site mapping.
func (s *ChunkSkyLightSources) set(index, value int) { s.values[index] = value }
func (s *ChunkSkyLightSources) get(index int) int    { return s.values[index] }

// extendSourcesBelowWorld ports ChunkSkyLightSources.extendSourcesBelowWorld. CITE.
func (s *ChunkSkyLightSources) extendSourcesBelowWorld(value int) int {
	if value == s.minY {
		return skySourcesNegativeInfinity
	}
	return value
}
