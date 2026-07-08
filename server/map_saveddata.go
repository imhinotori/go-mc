package server

// map_saveddata.go -- MapItemSavedData (the per-map pixel data), a 1:1 port of
// net.minecraft.world.level.saveddata.maps.MapItemSavedData over the 26.2 jar (javap this session):
// a 128x128 byte color grid, the scale (0..4), the world-space centerX/centerZ, the dimension, the
// locked flag, and the per-holding-player dirty-rectangle tracking that drives the incremental
// ClientboundMapItemDataPacket. The terrain color sampling (MapItem.update) lives in map_item.go; the
// wire encoder in map_packet.go.
//
// ANCHORS (VERIFIED javap this session):
//   MAP_SIZE = 128; HALF_MAP_SIZE = 64; MAX_SCALE = 4. colors = new byte[16384] (128*128).
//   createFresh(x, z, scale, tracking, unlimited, dim): i = 128 << scale;
//       cx = floor((x+64)/i); cz = floor((z+64)/i);
//       centerX = cx*i + i/2 - 64; centerZ = cz*i + i/2 - 64;
//       new MapItemSavedData(centerX, centerZ, scale, tracking, unlimited, false, dim).
//   updateColor(x, y, b): if (colors[x + y*128] != b) { setColor(x, y, b); return true; } return false.
//   getRedstoneSignal etc. N/A (map is not a container).

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
)

// mapImageSize is MapItemSavedData.MAP_SIZE (128) -- the map is 128x128 pixels. CITE: MapItemSavedData.MAP_SIZE.
const mapImageSize = 128

// mapHalfSize is MapItemSavedData.HALF_MAP_SIZE (64). CITE: MapItemSavedData.HALF_MAP_SIZE.
const mapHalfSize = 64

// mapMaxScale is MapItemSavedData.MAX_SCALE (4). CITE: MapItemSavedData.MAX_SCALE.
const mapMaxScale = 4

// mapColorArraySize is the colors buffer length (128*128 = 16384). CITE: MapItemSavedData.<init> new byte[16384].
const mapColorArraySize = mapImageSize * mapImageSize

// mapHoldingPlayer mirrors MapItemSavedData.HoldingPlayer: the per-carrier incremental-update cursor.
// step advances every update tick (the sparse-column scan phase); the dirty rectangle (min/maxDirtyX/Y)
// accumulates the pixels changed since the last packet, and dirtyData flags a pending color patch. CITE:
// MapItemSavedData.HoldingPlayer.
type mapHoldingPlayer struct {
	step      int
	dirtyData bool
	minDirtyX int
	minDirtyY int
	maxDirtyX int
	maxDirtyY int
}

// mapItemSavedData is the Sulfur analogue of MapItemSavedData: the 128x128 color grid + the map's
// world anchor (scale/centerX/centerZ/dimension) + the locked flag + the per-carrier dirty tracking.
// Tick-owned (t.maps, keyed by map id). CITE: MapItemSavedData.
type mapItemSavedData struct {
	centerX           int
	centerZ           int
	dimension         int
	scale             byte
	trackingPosition  bool
	unlimitedTracking bool
	locked            bool
	colors            [mapColorArraySize]byte
	carriedBy         map[int32]*mapHoldingPlayer // keyed by player entityID
}

// newMapSavedDataFresh ports MapItemSavedData.createFresh(x, z, scale, tracking, unlimited, dim): compute
// the grid-snapped centerX/centerZ from the world (x,z) so the map tiles align on a scale*128 lattice.
// CITE: MapItemSavedData.createFresh.
func newMapSavedDataFresh(x, z float64, scale byte, tracking, unlimited bool, dimension int) *mapItemSavedData {
	i := mapImageSize << scale // 128 << scale
	cx := mthFloorD((x + 64.0) / float64(i))
	cz := mthFloorD((z + 64.0) / float64(i))
	centerX := cx*i + i/2 - 64
	centerZ := cz*i + i/2 - 64
	return &mapItemSavedData{
		centerX:           centerX,
		centerZ:           centerZ,
		dimension:         dimension,
		scale:             scale,
		trackingPosition:  tracking,
		unlimitedTracking: unlimited,
		locked:            false,
		carriedBy:         make(map[int32]*mapHoldingPlayer),
	}
}

// mapUpdateColor ports MapItemSavedData.updateColor(x, y, b): if the pixel at (x,y) differs, write it and
// flag every holding player dirty over that pixel; returns whether the pixel changed. CITE:
// MapItemSavedData.updateColor + setColor + setColorsDirty.
func (m *mapItemSavedData) mapUpdateColor(x, y int, b byte) bool {
	if x < 0 || x >= mapImageSize || y < 0 || y >= mapImageSize {
		return false
	}
	idx := x + y*mapImageSize
	if m.colors[idx] == b {
		return false
	}
	m.colors[idx] = b
	// setColorsDirty(x, y): expand each holding player dirty rectangle over (x,y). CITE:
	// MapItemSavedData.setColorsDirty.
	for _, hp := range m.carriedBy {
		mapMarkColorsDirty(hp, x, y)
	}
	return true
}

// mapMarkColorsDirty ports MapItemSavedData.HoldingPlayer.markColorsDirty(x, y): grow the dirty rectangle
// to include (x,y), setting dirtyData. CITE: MapItemSavedData.HoldingPlayer.markColorsDirty.
func mapMarkColorsDirty(hp *mapHoldingPlayer, x, y int) {
	if hp.dirtyData {
		hp.minDirtyX = min(hp.minDirtyX, x)
		hp.minDirtyY = min(hp.minDirtyY, y)
		hp.maxDirtyX = max(hp.maxDirtyX, x)
		hp.maxDirtyY = max(hp.maxDirtyY, y)
	} else {
		hp.dirtyData = true
		hp.minDirtyX = x
		hp.minDirtyY = y
		hp.maxDirtyX = x
		hp.maxDirtyY = y
	}
}

// mapGetHoldingPlayer ports MapItemSavedData.getHoldingPlayer(player): return the carrier cursor for the
// player, creating it (and registering the player as a carrier) on first access. CITE:
// MapItemSavedData.getHoldingPlayer.
func (m *mapItemSavedData) mapGetHoldingPlayer(entityID int32) *mapHoldingPlayer {
	if m.carriedBy == nil {
		m.carriedBy = make(map[int32]*mapHoldingPlayer)
	}
	hp, ok := m.carriedBy[entityID]
	if !ok {
		hp = &mapHoldingPlayer{}
		m.carriedBy[entityID] = hp
	}
	return hp
}

// mapColorIDForState ports the block-side of BlockState.getMapColor(): map a block state to its default
// MapColor id (0..63) via the generated per-block table (block.MapColorID), keyed by Block.ID(). A block
// with no table entry (should not happen for a registered block) reports MapColor.NONE (0). CITE:
// BlockBehaviour.Properties.mapColor / BlockState.getMapColor.
func mapColorIDForState(state block.StateID) byte {
	if int(state) < 0 || int(state) >= len(block.StateList) {
		return 0
	}
	id := block.StateList[state].ID()
	if c, ok := block.MapColorID[id]; ok {
		return c
	}
	return 0
}

// mthFloorD ports net.minecraft.util.Mth.floor(double): the mathematical floor cast to int (the same
// floor(d) cast used across the codebase). CITE: Mth.floor.
func mthFloorD(d float64) int {
	return int(math.Floor(d))
}
