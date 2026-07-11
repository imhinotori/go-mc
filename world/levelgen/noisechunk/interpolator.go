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
// PER-MARKER INTERPOLATION (the parity fix): vanilla's NoiseChunk constructor calls
// noiseRouter.mapAll(this::wrap), replacing each `interpolated`-marked subtree DEEP in
// the graph with its OWN NoiseInterpolator and leaving every surrounding op (squeeze/
// min/the noodle cave graph) to evaluate PER-BLOCK. We do the same: density.MapAll
// rewrites final_density so each MarkerInterpolated node becomes an *interpolatedFn
// (this file), the chunk drives ALL of them on the cell grid, then evaluates the
// rewritten final_density per block — interpolated nodes return their trilerped value,
// everything else computes exactly. This makes cliffs/overhangs sharp and the noodle
// (spaghetti) caves survive, because the non-linear outer ops are no longer smeared by
// a single whole-graph interpolation.
//
// It is a faithful algorithmic port, not a copy of Mojang source.
package noisechunk

import "github.com/imhinotori/sulfur/world/levelgen/density"

// mthLerp is net.minecraft.util.Mth.lerp(t, a, b) = a + t*(b - a) (javap -c
// net.minecraft.util.Mth.lerp). It is the single lerp primitive every stage of the
// trilinear interpolation reduces to.
func mthLerp(t, a, b float64) float64 { return a + t*(b-a) }

// interpolatedFn ports net.minecraft.world.level.levelgen.NoiseChunk$NoiseInterpolator:
// it wraps ONE `interpolated`-marked density.Function (the noiseFiller) and is what
// density.MapAll substitutes for that marker. It implements density.Function so the
// rewritten final_density tree contains it directly.
//
// Compute mirrors NoiseInterpolator.compute(ctx): inside the chunk's interpolation loop
// (state.filling) it returns the trilerped value() built from the 8 loaded cell corners;
// OUTSIDE the loop (slice-fill of OTHER interpolators, or any off-loop sampler like the
// aquifer/preliminary_surface_level) it samples noiseFiller directly. Vanilla discriminates
// on `ctx == this$0` (the chunk is its own context); we discriminate on the shared
// fillState the chunk toggles — behaviour-identical.
//
// Slice layout (NoiseChunk$NoiseInterpolator.allocateSlice): slice0/slice1 are
// [cellCountXZ+1][cellCountY+1] grids. slice0 is the X=0 cell face, slice1 the X=1
// face; the first index steps Z (the "xz" param of selectCellYZ), the second steps Y.
// The two slices are the rolling pair of X faces the chunk advances across cells
// (advanceCellX fills slice1; swapSlices rolls slice1 into slice0).
type interpolatedFn struct {
	noiseFiller density.Function
	state       *fillState // shared with the chunk + every sibling interpolator

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

// fillState is the chunk-owned flag the interpolators read in Compute to decide whether
// to return their trilerped value (filling==true, inside the cell loop) or sample their
// inner filler directly (filling==false). Mirrors NoiseChunk.interpolating/fillingCell —
// the chunk sets it true only while iterating cells and pushing values through the
// rewritten final_density.
type fillState struct {
	filling bool
	// interpCounter is bumped once per per-block visit in fill() so a cacheOnce wrapper can memoize the
	// wrapped value for the duration of a single block's final_density evaluation (NoiseChunk's
	// interpolationCounter). Monotonic; wraparound is irrelevant (equality within one visit only).
	interpCounter uint64
}

// Compute ports NoiseInterpolator.compute: trilerped value() inside the loop, direct
// sample of the inner filler outside it. MinValue/MaxValue forward to the inner filler
// (the interpolated value is always within the filler's range), keeping the Ap2 min/max
// short-circuits in the surrounding ops conservative + correct.
func (f *interpolatedFn) Compute(c density.Context) float64 {
	if f.state.filling {
		return f.val
	}
	return f.noiseFiller.Compute(c)
}
func (f *interpolatedFn) MinValue() float64 { return f.noiseFiller.MinValue() }
func (f *interpolatedFn) MaxValue() float64 { return f.noiseFiller.MaxValue() }

// newInterpolatedFn allocates an interpolatedFn wrapping noiseFiller, sharing state with
// the chunk. allocateSlice(cellY, cellXZ) returns a [cellXZ+1][cellY+1] double grid
// (NoiseChunk$NoiseInterpolator.allocateSlice: outer = cellNoiseSizeXZ = cellCountXZ+1,
// inner = cellCountY+1).
func newInterpolatedFn(noiseFiller density.Function, state *fillState, cellCountXZ, cellCountY, cellWidth, cellHeight, cellNoiseMinY int) *interpolatedFn {
	return &interpolatedFn{
		noiseFiller:   noiseFiller,
		state:         state,
		cellCountXZ:   cellCountXZ,
		cellCountY:    cellCountY,
		cellWidth:     cellWidth,
		cellHeight:    cellHeight,
		cellNoiseMinY: cellNoiseMinY,
		slice0:        allocateSlice(cellCountY, cellCountXZ),
		slice1:        allocateSlice(cellCountY, cellCountXZ),
	}
}

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
// coordinates. cellX/firstCellZ are the cell-grid indices (Math.floorDiv(blockX/Z,
// cellWidth) origin, then +offset); blockX = cellX * cellWidth and blockZ =
// (firstCellZ + cz) * cellWidth -- matching NoiseChunk.fillSlice (cellStartBlockX =
// cellX*cellWidth, cellStartBlockZ = (firstCellZ+cz)*cellWidth). The filler is sampled
// DIRECTLY here (state.filling is false during the chunk's fillSlice pass), so nested
// interpolators inside this filler also sample direct -- matching vanilla's
// NoiseInterpolator.fillArray, which runs with fillingCell=false.
//
// onSlice0 selects the X=0 face (slice0) vs the X=1 face (slice1).
func (f *interpolatedFn) fillSlice(onSlice0 bool, cellX, firstCellZ int) {
	slice := f.slice1
	if onSlice0 {
		slice = f.slice0
	}
	blockX := cellX * f.cellWidth
	for cz := 0; cz <= f.cellCountXZ; cz++ {
		blockZ := (firstCellZ + cz) * f.cellWidth
		col := slice[cz]
		for cy := 0; cy <= f.cellCountY; cy++ {
			// Corner world Y for cell row cy (NoiseBasedChunkGenerator.iterateNoiseColumn:
			// blockY = (cellNoiseMinY + cellY) * cellHeight, inCellY=0 at the corner).
			blockY := (f.cellNoiseMinY + cy) * f.cellHeight
			col[cy] = f.noiseFiller.Compute(density.Context{X: blockX, Y: blockY, Z: blockZ})
		}
	}
}

// selectCellYZ loads the 8 corners of the cell at (cellY=y, cellZ=xz) from the two
// X-face slices (NoiseChunk$NoiseInterpolator.selectCellYZ). slice0 -> noise0YZ (X=0),
// slice1 -> noise1YZ (X=1); the inner index y/y+1 = Y, the outer index xz/xz+1 = Z.
func (f *interpolatedFn) selectCellYZ(y, xz int) {
	f.noise000 = f.slice0[xz][y]
	f.noise001 = f.slice0[xz+1][y]
	f.noise100 = f.slice1[xz][y]
	f.noise101 = f.slice1[xz+1][y]
	f.noise010 = f.slice0[xz][y+1]
	f.noise011 = f.slice0[xz+1][y+1]
	f.noise110 = f.slice1[xz][y+1]
	f.noise111 = f.slice1[xz+1][y+1]
}

// updateForY lerps the 8 corners down the Y axis (NoiseInterpolator.updateForY),
// collapsing them to the 4 XZ-face values at fractional height dy in [0,1).
func (f *interpolatedFn) updateForY(dy float64) {
	f.valueXZ00 = mthLerp(dy, f.noise000, f.noise010)
	f.valueXZ10 = mthLerp(dy, f.noise100, f.noise110)
	f.valueXZ01 = mthLerp(dy, f.noise001, f.noise011)
	f.valueXZ11 = mthLerp(dy, f.noise101, f.noise111)
}

// updateForX lerps the 4 XZ-face values across X (NoiseInterpolator.updateForX),
// collapsing them to the 2 Z-edge values at fractional width dx in [0,1).
func (f *interpolatedFn) updateForX(dx float64) {
	f.valueZ0 = mthLerp(dx, f.valueXZ00, f.valueXZ10)
	f.valueZ1 = mthLerp(dx, f.valueXZ01, f.valueXZ11)
}

// updateForZ lerps the 2 Z-edge values across Z (NoiseInterpolator.updateForZ) to the
// final interpolated block density at fractional depth dz in [0,1), stored in val (what
// Compute returns while state.filling is true).
func (f *interpolatedFn) updateForZ(dz float64) {
	f.val = mthLerp(dz, f.valueZ0, f.valueZ1)
}

// swapSlices rolls slice1 (the just-filled X+1 face) into slice0 for the next cellX
// (NoiseInterpolator.swapSlices) so advanceCellX only fills the new far face.
func (f *interpolatedFn) swapSlices() {
	f.slice0, f.slice1 = f.slice1, f.slice0
}
