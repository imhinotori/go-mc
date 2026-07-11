package noisechunk

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// TestFirstCellUsesFloorDivNotQuartShift verifies the NoiseChunk cell-fill origin is
// Math.floorDiv(blockX, cellWidth) (firstCellX), NOT QuartPos.fromBlock (blockX>>2).
// For the overworld cellWidth=4 the two coincide, but for a cellWidth=8 settings (the
// End) they DIVERGE at negative/large X -- the cell loop MUST use floorDiv. CITE:
// NoiseChunk ctor (Math.floorDiv(blockX, cellWidth)) + fillSlice (cellStartBlockX =
// cellX * cellWidth).
func TestFirstCellUsesFloorDivNotQuartShift(t *testing.T) {
	r, err := router.NewRouter(0x5EED_1234)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	// Force an End-style horizontal cell size (getCellWidth() = size_horizontal << 2 = 8).
	r.Settings.Noise.SizeHorizontal = 2

	// A negative chunk so floorDiv(blockX,8) != blockX>>2 diverges observably.
	// blockX = -80 -> floorDiv(-80,8) = -10; (-80)>>2 = -20. Different.
	const cx = int32(-5) // blockX = -80
	nc := NewNoiseChunk(r, level.ChunkPos{cx, 0})

	blockX := int(cx) * 16
	if nc.cellWidth != 8 {
		t.Fatalf("cellWidth = %d, want 8 (size_horizontal<<2)", nc.cellWidth)
	}
	wantCell := floorDiv(blockX, nc.cellWidth) // -10
	if nc.firstCellX != wantCell {
		t.Fatalf("firstCellX = %d, want floorDiv(%d,%d) = %d", nc.firstCellX, blockX, nc.cellWidth, wantCell)
	}
	// Prove the bug it guards against: the OLD code used firstNoiseX = blockX>>2, which
	// differs here. firstNoiseX must still be the quart value (used only by flat_cache).
	quart := blockX >> 2 // -20
	if nc.firstNoiseX != quart {
		t.Fatalf("firstNoiseX = %d, want blockX>>2 = %d", nc.firstNoiseX, quart)
	}
	if nc.firstCellX == quart {
		t.Fatalf("firstCellX (%d) equals blockX>>2 (%d) at cellWidth=8 -- the >>2 bug is back", nc.firstCellX, quart)
	}

	// Z axis too.
	const cz = int32(-3) // blockZ = -48; floorDiv(-48,8) = -6; (-48)>>2 = -12
	nc2 := NewNoiseChunk(r, level.ChunkPos{0, cz})
	blockZ := int(cz) * 16
	if nc2.firstCellZ != floorDiv(blockZ, nc2.cellWidth) {
		t.Fatalf("firstCellZ = %d, want floorDiv(%d,8) = %d", nc2.firstCellZ, blockZ, floorDiv(blockZ, nc2.cellWidth))
	}
}
