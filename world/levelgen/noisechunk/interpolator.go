// Package noisechunk ports Tier-E of the Minecraft 26.2 (protocol 776) world
// generator from the unobfuscated server jar (temp/cache/26.2-inner.jar, javap -c):
// the NoiseChunk cell-sample + trilinear-interpolation core that turns the bound
// final_density graph (Wave 3, INCLUDING the cave branches) into a per-block density
// field, plus a provisional solid/air/water fill for end-to-end testing.
//
// PACKAGE LOCATION NOTE: this is package `noisechunk` (under world/levelgen/
// noisechunk/), NOT package `levelgen`. It is a composition root: it consumes
// density.Function and the bound router.Router, both of which transitively import
// package levelgen (the Tier-A primitives: RandomSource/PositionalRandomFactory/
// Xoroshiro). A package levelgen file importing density/router therefore cycles
// (levelgen -> density -> synth -> levelgen). This mirrors the 09-03 router decision
// (router also lives one level up for the same reason). The plan listed the files at
// world/levelgen/noisechunk.go (package levelgen); that location is impossible under
// Go's import-cycle rule, so they live in this sibling package instead.
//
// It is a faithful algorithmic port, not a copy of Mojang source.
package noisechunk

import "github.com/imhinotori/sulfur/world/levelgen/density"

// mthLerp is net.minecraft.util.Mth.lerp(t, a, b) = a + t*(b - a) (javap -c
// net.minecraft.util.Mth.lerp). It is the single lerp primitive every stage of the
// trilinear interpolation reduces to.
func mthLerp(t, a, b float64) float64 { return a + t*(b-a) }

// interpolator ports net.minecraft.world.level.levelgen.NoiseChunk$NoiseInterpolator:
// the per-cell 8-corner buffers + the staged trilinear interpolation in vanilla's
// exact cell-fill order. It wraps ONE interpolated-marked density.Function (the
// noiseFiller) and is sampled at cell corners + lerped to each block — the parity+perf
// hinge (Pattern 2 / Pitfall 3: interpolated is NOT a no-op pass-through).
//
// Slice layout (NoiseChunk$NoiseInterpolator.allocateSlice): slice0/slice1 are
// [cellCountXZ+1][cellCountY+1] grids. slice0 is the X=0 cell face, slice1 the X=1
// face; the first index steps Z (the "xz" param of selectCellYZ), the second steps Y.
// The two slices are the rolling pair of X faces the NoiseChunk advances across cells
// (advanceCellX fills slice1; swapSlices rolls slice1 into slice0).
type interpolator struct {
	noiseFiller density.Function

	cellCountXZ   int
	cellCountY    int
	cellWidth     int
	cellHeight    int
	cellNoiseMinY int // floorDiv(minY, cellHeight): the cy=0 corner row in CELL units

	slice0 [][]float64 // X=0 face: [cz][cy]
	slice1 [][]float64 // X=1 face: [cz][cy]

	// The 8 active cell corners loaded by selectCellYZ. noiseXYZ: X in {0,1} =
	// slice0/slice1, Y in {0,1} = cy/cy+1, Z in {0,1} = cz/cz+1.
	noise000, noise001, noise100, noise101 float64
	noise010, noise011, noise110, noise111 float64

	// Progressive lerp state (NoiseInterpolator.updateForY/X/Z fields).
	valueXZ00, valueXZ10, valueXZ01, valueXZ11 float64
	valueZ0, valueZ1                           float64
	val                                        float64
}

// newInterpolator allocates an interpolator wrapping noiseFiller. allocateSlice(cellY,
// cellXZ) returns a [cellXZ+1][cellY+1] double grid (NoiseChunk$NoiseInterpolator.
// allocateSlice: the outer dimension is cellNoiseSizeXZ = cellCountXZ+1, inner is
// cellCountY+1).
func newInterpolator(noiseFiller density.Function, cellCountXZ, cellCountY, cellWidth, cellHeight int) *interpolator {
	return &interpolator{
		noiseFiller: noiseFiller,
		cellCountXZ: cellCountXZ,
		cellCountY:  cellCountY,
		cellWidth:   cellWidth,
		cellHeight:  cellHeight,
		slice0:      allocateSlice(cellCountY, cellCountXZ),
		slice1:      allocateSlice(cellCountY, cellCountXZ),
	}
}

// setCellNoiseMinY sets the cy=0 corner row (in cell units, = floorDiv(minY,
// cellHeight)) used by fillSlice to offset corner rows into world Y. Defaults to 0
// (the Task-1 unit tests drive corners directly at a zero base).
func (ip *interpolator) setCellNoiseMinY(c int) { ip.cellNoiseMinY = c }

// allocateSlice mirrors NoiseChunk$NoiseInterpolator.allocateSlice(cellY, cellXZ):
// a [cellXZ+1][cellY+1] grid (outer = XZ corners, inner = Y corners).
func allocateSlice(cellY, cellXZ int) [][]float64 {
	grid := make([][]float64, cellXZ+1)
	for i := range grid {
		grid[i] = make([]float64, cellY+1)
	}
	return grid
}

// fillSlice fills one X face of the corner buffers from noiseFiller, sampling the whole
// (cellCountXZ+1) x (cellCountY+1) Y/Z corner grid at the cell-aligned world block
// coordinates. blockX is the world X of this face (= cellX * cellWidth);
// firstNoiseZ is the world Z origin in noise-cell units. This is the per-interpolator
// half of NoiseChunk.fillSlice (the bytecode fills, per interpolator, slice[cz] columns
// via fillArray + the sliceFillingContextProvider; we inline the column fill).
//
// onSlice0 selects the X=0 face (slice0) vs the X=1 face (slice1).
func (ip *interpolator) fillSlice(onSlice0 bool, blockX, firstNoiseZ int) {
	slice := ip.slice1
	if onSlice0 {
		slice = ip.slice0
	}
	for cz := 0; cz <= ip.cellCountXZ; cz++ {
		blockZ := (firstNoiseZ + cz) * ip.cellWidth
		col := slice[cz]
		for cy := 0; cy <= ip.cellCountY; cy++ {
			// Corner world Y for cell row cy (NoiseBasedChunkGenerator.iterateNoiseColumn:
			// blockY = (cellNoiseMinY + cellY) * cellHeight, inCellY=0 at the corner).
			blockY := (ip.cellNoiseMinY + cy) * ip.cellHeight
			col[cy] = ip.noiseFiller.Compute(density.Context{X: blockX, Y: blockY, Z: blockZ})
		}
	}
}

// selectCellYZ loads the 8 corners of the cell at (cellY=y, cellZ=xz) from the two
// X-face slices (NoiseChunk$NoiseInterpolator.selectCellYZ). slice0 -> noise0YZ (X=0),
// slice1 -> noise1YZ (X=1); the inner index y/y+1 = Y, the outer index xz/xz+1 = Z.
func (ip *interpolator) selectCellYZ(y, xz int) {
	ip.noise000 = ip.slice0[xz][y]
	ip.noise001 = ip.slice0[xz+1][y]
	ip.noise100 = ip.slice1[xz][y]
	ip.noise101 = ip.slice1[xz+1][y]
	ip.noise010 = ip.slice0[xz][y+1]
	ip.noise011 = ip.slice0[xz+1][y+1]
	ip.noise110 = ip.slice1[xz][y+1]
	ip.noise111 = ip.slice1[xz+1][y+1]
}

// updateForY lerps the 8 corners down the Y axis (NoiseInterpolator.updateForY),
// collapsing them to the 4 XZ-face values at fractional height dy in [0,1).
func (ip *interpolator) updateForY(dy float64) {
	ip.valueXZ00 = mthLerp(dy, ip.noise000, ip.noise010)
	ip.valueXZ10 = mthLerp(dy, ip.noise100, ip.noise110)
	ip.valueXZ01 = mthLerp(dy, ip.noise001, ip.noise011)
	ip.valueXZ11 = mthLerp(dy, ip.noise101, ip.noise111)
}

// updateForX lerps the 4 XZ-face values across X (NoiseInterpolator.updateForX),
// collapsing them to the 2 Z-edge values at fractional width dx in [0,1).
func (ip *interpolator) updateForX(dx float64) {
	ip.valueZ0 = mthLerp(dx, ip.valueXZ00, ip.valueXZ10)
	ip.valueZ1 = mthLerp(dx, ip.valueXZ01, ip.valueXZ11)
}

// updateForZ lerps the 2 Z-edge values across Z (NoiseInterpolator.updateForZ) to the
// final interpolated block density at fractional depth dz in [0,1).
func (ip *interpolator) updateForZ(dz float64) {
	ip.val = mthLerp(dz, ip.valueZ0, ip.valueZ1)
}

// value returns the last interpolated block density (NoiseInterpolator.value, returned
// by compute() when fillingCell is false).
func (ip *interpolator) value() float64 { return ip.val }

// swapSlices rolls slice1 (the just-filled X+1 face) into slice0 for the next cellX
// (NoiseInterpolator.swapSlices) so advanceCellX only fills the new far face.
func (ip *interpolator) swapSlices() {
	ip.slice0, ip.slice1 = ip.slice1, ip.slice0
}
