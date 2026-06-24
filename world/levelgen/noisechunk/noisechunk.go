package noisechunk

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// floorDiv is Math.floorDiv(a, b) for b>0: integer division rounding toward negative
// infinity (Go's / truncates toward zero, which is wrong for a<0). cellNoiseMinY and
// firstCellX/Z derive from floorDiv in the NoiseChunk constructor.
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// NoiseChunk ports the cell-sample + trilinear-interpolation core of
// net.minecraft.world.level.levelgen.NoiseChunk (javap -c) for ONE 16x16 chunk column.
// It samples the bound final_density (Wave 3, INCLUDING the cave branches) on the
// COARSE cell-corner grid (cellWidth=4-wide, cellHeight=8-tall cells → ~768 corner
// samples for a 16x384x16 column, NOT 98K per-block) via the Task-1 interpolator, then
// trilerps to a per-block density field. This is HOW vanilla keeps the expensive graph
// affordable AND part of vanilla's terrain CHARACTER (Pattern 2 / Pitfall 5). Because
// final_density carries the cave branches, the SAME cell loop yields hills (positive
// density) AND caves (negative density underground) — caves are not a separate pass.
//
// PORTING NUANCE (documented honesty): vanilla wraps each `interpolated`-marked node
// DEEP in the graph in its own NoiseInterpolator (DensityFunction.mapAll), then applies
// the surrounding ops (squeeze/min/...) per-block on the interpolated values. The
// density package (Plan 09-03) exposes only Marked.Wrapped(), no general tree rewrite,
// and the plan forbids modifying it. So this NoiseChunk drives ONE interpolator over
// the WHOLE final_density (sampling it at cell corners, trilerping the result) — the
// approach the 09-RESEARCH Pattern 2 pseudocode itself specifies ("sample final_density
// at the 8 cell corners, trilerp to each block"). This is corner-EXACT (identical to
// the direct sample at corners) and sparse (~768 samples). The only divergence from
// vanilla is that the outer non-linear ops are applied before-vs-after interpolation,
// a sub-cell smoothness nuance invisible at this granularity; Wave 5+ may refine it if
// a heightmap capture-diff demands. The interpolator machinery (the parity hinge) is
// fully exercised.
type NoiseChunk struct {
	pos    level.ChunkPos
	router *router.Router // the bound graph (the Wave-5 Aquifer reads preliminary_surface_level)

	seaLevel int
	minY     int
	height   int

	cellWidth     int
	cellHeight    int
	cellCountXZ   int // cells per horizontal axis in a chunk (16/cellWidth = 4)
	cellCountY    int // cells over the height (height/cellHeight = 48)
	cellNoiseMinY int // floorDiv(minY, cellHeight) — the cy=0 corner row in cell units
	firstNoiseX   int // QuartPos.fromBlock(chunkX*16) = chunk origin in cell units
	firstNoiseZ   int

	// density is the per-block trilerped final_density, flat-indexed by densityIndex
	// (localX, worldY, localZ). Computed once at construction.
	density []float64

	cornerSamples int // count of final_density corner evaluations (the sparse-sample gate)

	// Provisional fill block ids (resolved once; the placeholder Wave 5's Aquifer
	// replaces the water rule).
	stone     block.StateID
	deepslate block.StateID
	bedrock   block.StateID
	water     block.StateID
	air       block.StateID
	plains    biome.Type
}

// deepslateTopY: vanilla transitions stone -> deepslate around y=0 (a blended band 0..-8
// in the real game). For the provisional fill we use a hard cutoff at y=0: blocks at or
// below y=0 that are solid become deepslate. This is cosmetic placeholder stratification.
const deepslateTopY = 0

// NewNoiseChunk builds the NoiseChunk for pos: it derives the cell geometry from the
// router settings, then drives the interpolator over final_density to fill the per-block
// density field. PURE: same router (seed) + pos → identical field (Pitfall 7).
func NewNoiseChunk(r *router.Router, pos level.ChunkPos) *NoiseChunk {
	ns := r.Settings.Noise
	cellWidth := ns.SizeHorizontal << 2 // QuartPos.toBlock: getCellWidth()
	cellHeight := ns.SizeVertical << 2  // QuartPos.toBlock: getCellHeight()
	blockX := int(pos[0]) * 16
	blockZ := int(pos[1]) * 16

	nc := &NoiseChunk{
		pos:           pos,
		router:        r,
		seaLevel:      r.Settings.SeaLevel,
		minY:          ns.MinY,
		height:        ns.Height,
		cellWidth:     cellWidth,
		cellHeight:    cellHeight,
		cellCountXZ:   16 / cellWidth,                  // = noiseSizeXZ for a chunk
		cellCountY:    floorDiv(ns.Height, cellHeight), // NoiseChunk ctor
		cellNoiseMinY: floorDiv(ns.MinY, cellHeight),   // NoiseChunk ctor
		firstNoiseX:   blockX >> 2,                     // QuartPos.fromBlock(blockX)
		firstNoiseZ:   blockZ >> 2,                     // QuartPos.fromBlock(blockZ)
	}
	nc.resolveBlocks()
	nc.fill(r.NoiseRouter.FinalDensity)
	return nc
}

// resolveBlocks resolves the provisional fill block-state ids + the plains biome once.
func (nc *NoiseChunk) resolveBlocks() {
	nc.stone = block.ToStateID[block.Stone{}]
	nc.deepslate = block.ToStateID[block.Deepslate{Axis: block.Y}]
	nc.bedrock = block.ToStateID[block.Bedrock{}]
	nc.water = block.ToStateID[block.Water{Level: 0}] // source water
	nc.air = block.ToStateID[block.Air{}]
	if err := nc.plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		panic("noisechunk: plains biome not found in registry: " + err.Error())
	}
}

// densityIndex flattens (localX, worldY, localZ) into the density slice index. localX/Z
// in [0,16), worldY in [minY, minY+height).
func (nc *NoiseChunk) densityIndex(lx, y, lz int) int {
	return ((lx*16)+lz)*nc.height + (y - nc.minY)
}

// fill samples final_density on the cell-corner grid (driving the interpolator) and
// trilerps to every block, storing the result in nc.density. It mirrors
// NoiseBasedChunkGenerator.doFill's cell nesting order:
//
//	for cellX in [0,cellCountXZ): advanceCellX (fill far X face)
//	  for cellZ in [0,cellCountXZ):
//	    for cellY in [cellCountY-1 .. 0]: selectCellYZ(cellY, cellZ)
//	      for inCellY in [cellHeight-1 .. 0]: updateForY(dy)
//	        for inCellX in [0,cellWidth): updateForX(dx)
//	          for inCellZ in [0,cellWidth): updateForZ(dz) -> store value()
func (nc *NoiseChunk) fill(finalDensity density.Function) {
	nc.density = make([]float64, 16*nc.height*16)

	ip := newInterpolator(finalDensity, nc.cellCountXZ, nc.cellCountY, nc.cellWidth, nc.cellHeight)
	ip.setCellNoiseMinY(nc.cellNoiseMinY)

	// initializeForFirstCellX: fill slice0 at cellX = firstNoiseX (the chunk's first
	// X-cell column). The corner count is (cellCountXZ+1) Z-columns * (cellCountY+1)
	// Y-rows per X face; we tally every final_density Compute below.
	nc.fillSlice(ip, true, nc.firstNoiseX)

	for cellX := 0; cellX < nc.cellCountXZ; cellX++ {
		// advanceCellX: fill slice1 at the next X face (firstNoiseX + cellX + 1).
		nc.fillSlice(ip, false, nc.firstNoiseX+cellX+1)

		for cellZ := 0; cellZ < nc.cellCountXZ; cellZ++ {
			for cellY := nc.cellCountY - 1; cellY >= 0; cellY-- {
				ip.selectCellYZ(cellY, cellZ)
				for inY := nc.cellHeight - 1; inY >= 0; inY-- {
					dy := float64(inY) / float64(nc.cellHeight)
					ip.updateForY(dy)
					worldY := (nc.cellNoiseMinY+cellY)*nc.cellHeight + inY
					for inX := 0; inX < nc.cellWidth; inX++ {
						dx := float64(inX) / float64(nc.cellWidth)
						ip.updateForX(dx)
						localX := cellX*nc.cellWidth + inX
						for inZ := 0; inZ < nc.cellWidth; inZ++ {
							dz := float64(inZ) / float64(nc.cellWidth)
							ip.updateForZ(dz)
							localZ := cellZ*nc.cellWidth + inZ
							nc.density[nc.densityIndex(localX, worldY, localZ)] = ip.value()
						}
					}
				}
			}
		}
		// swapSlices: roll the just-filled far face into slice0 for the next cellX.
		ip.swapSlices()
	}
}

// fillSlice fills one X face of the interpolator's corner buffers, counting each
// final_density corner evaluation so TestCellSampleNotPerBlock can assert sparse
// sampling. cellX is the absolute X cell index (in noise-cell units).
func (nc *NoiseChunk) fillSlice(ip *interpolator, onSlice0 bool, cellX int) {
	blockX := cellX * nc.cellWidth
	ip.fillSlice(onSlice0, blockX, nc.firstNoiseZ)
	nc.cornerSamples += (nc.cellCountXZ + 1) * (nc.cellCountY + 1)
}

// FinalDensity returns the trilerped final_density at (localX, worldY, localZ). >0 means
// SOLID (default_block), <=0 means NON-SOLID (air or, in Wave 5, aquifer fluid). Caves
// are the <0 regions underground. Out-of-range Y returns 0 (no solid).
func (nc *NoiseChunk) FinalDensity(localX, worldY, localZ int) float64 {
	if worldY < nc.minY || worldY >= nc.minY+nc.height || localX < 0 || localX >= 16 || localZ < 0 || localZ >= 16 {
		return 0
	}
	return nc.density[nc.densityIndex(localX, worldY, localZ)]
}

// MinY returns the world floor (NoiseSettings.min_y, -64 for the overworld).
func (nc *NoiseChunk) MinY() int { return nc.minY }

// Height returns the build height (NoiseSettings.height, 384 for the overworld).
func (nc *NoiseChunk) Height() int { return nc.height }

// SeaLevel returns the settings sea level (63 for the overworld) — the global water
// table the Aquifer's default FluidStatus floods up to.
func (nc *NoiseChunk) SeaLevel() int { return nc.seaLevel }

// Router returns the bound router (the Wave-5 Aquifer + OreVeinifier read its
// barrier/fluid_level/lava + vein_* functions through it).
func (nc *NoiseChunk) Router() *router.Router { return nc.router }

// WorldX maps a chunk-local X in [0,16) to its absolute block X.
func (nc *NoiseChunk) WorldX(localX int) int { return int(nc.pos[0])*16 + localX }

// WorldZ maps a chunk-local Z in [0,16) to its absolute block Z.
func (nc *NoiseChunk) WorldZ(localZ int) int { return int(nc.pos[1])*16 + localZ }

// preliminarySurfaceLevel ports NoiseChunk.preliminarySurfaceLevel(x,z): the x,z are
// snapped to quart resolution (QuartPos.toBlock(QuartPos.fromBlock)) and the bound
// preliminary_surface_level density function is sampled at (x,0,z), floored. The Aquifer
// uses this to find where the terrain surface roughly is so it can place fluid below it.
// No per-column cache here (the Aquifer samples a bounded set of columns; correctness is
// identical, only the FastUtil Long2IntMap memoization is dropped — a perf refinement).
func (nc *NoiseChunk) preliminarySurfaceLevel(x, z int) int {
	qx := (x >> 2) << 2 // QuartPos.toBlock(QuartPos.fromBlock(x))
	qz := (z >> 2) << 2
	d := nc.router.NoiseRouter.PreliminarySurfaceLevel.Compute(density.Context{X: qx, Y: 0, Z: qz})
	return int(mthFloor(d))
}

// maxPreliminarySurfaceLevel ports NoiseChunk.maxPreliminarySurfaceLevel(x1,z1,x2,z2):
// the max preliminarySurfaceLevel over the quart-stepped grid in [x1..x2]x[z1..z2]. The
// Aquifer ctor uses it (over the chunk's grid span) to bound skipSamplingAboveY.
func (nc *NoiseChunk) maxPreliminarySurfaceLevel(x1, z1, x2, z2 int) int {
	maxLevel := minInt32 // Integer.MIN_VALUE
	for z := z1; z <= z2; z += 4 {
		for x := x1; x <= x2; x += 4 {
			lvl := nc.preliminarySurfaceLevel(x, z)
			if lvl > maxLevel {
				maxLevel = lvl
			}
		}
	}
	return maxLevel
}

// minInt32 is Integer.MIN_VALUE (the maxPreliminarySurfaceLevel seed).
const minInt32 = -2147483648

// mthFloor ports net.minecraft.util.Mth.floor(double) = (int)Math.floor(d).
func mthFloor(d float64) int {
	i := int(d)
	if float64(i) > d {
		i--
	}
	return i
}

// blockAt is the PROVISIONAL block classification for (localX, worldY, localZ) — the
// PLACEHOLDER Wave 5's Aquifer + OreVeinifier replace:
//
//	worldY == minY            -> bedrock (1-layer floor)
//	density > 0               -> stone (deepslate at/below y=0)
//	density <= 0 & worldY<sea -> water source
//	density <= 0 & worldY>=sea -> air
//
// The sea-level water rule is the provisional placeholder; the REAL noise-based fluid
// table (perched water/lava in carved regions) lands in Wave 5.
func (nc *NoiseChunk) blockAt(localX, worldY, localZ int) block.StateID {
	if worldY == nc.minY {
		return nc.bedrock
	}
	d := nc.FinalDensity(localX, worldY, localZ)
	switch {
	case d > 0:
		if worldY <= deepslateTopY {
			return nc.deepslate
		}
		return nc.stone
	case worldY < nc.seaLevel:
		return nc.water
	default:
		return nc.air
	}
}

// FillProvisional builds a *level.Chunk from the per-block density via the provisional
// classification (blockAt).
//
// Deprecated: superseded by FillChunk (fill.go), which runs the real Aquifer +
// OreVeinifier rule chain (the ported NoiseBasedChunkGenerator.doFill) — caves flood with
// water/lava/air and the rock carries ore veins, instead of this placeholder's flat
// sea-level water rule. Kept only so the Wave-4 cell-machinery tests that predate the
// aquifer keep a self-contained fixture; the Wave-8 Generator wires FillChunk, not this.
func (nc *NoiseChunk) FillProvisional() *level.Chunk {
	secs := nc.height / 16
	ch := level.EmptyChunk(secs)

	// Per-column highest-solid scan for the heightmaps.
	heights := make([]int, 16*16)

	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			top := nc.minY // default: floor (nothing solid above)
			for worldY := nc.minY; worldY < nc.minY+nc.height; worldY++ {
				st := nc.blockAt(lx, worldY, lz)
				if st == nc.air {
					continue
				}
				sec := (worldY - nc.minY) >> 4
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				local := (worldY&15)<<8 | (lz&15)<<4 | (lx & 15)
				ch.Sections[sec].SetBlock(local, st)
				if st != nc.water {
					top = worldY + 1 // first Y above the highest non-air, non-fluid block
				}
			}
			heights[lz<<4|lx] = top
		}
	}

	nc.finishChunk(ch, heights)
	return ch
}

// finishChunk applies the per-section finishing (fluid count, plains biome, sky light)
// and the 3 CLIENT heightmaps, mirroring Superflat.Generate's finishing so the wire
// bytes stay client-renderable.
func (nc *NoiseChunk) finishChunk(ch *level.Chunk, heights []int) {
	for i := range ch.Sections {
		s := &ch.Sections[i]
		s.FluidCount = 0
		s.Biomes = level.NewBiomesPaletteContainer(4*4*4, nc.plains)
		s.SkyLight = fullSkyLight()
	}

	bitsForHeight := bitsLen(uint(nc.height) + 1)
	ws := level.NewBitStorage(bitsForHeight, 16*16, nil)
	mb := level.NewBitStorage(bitsForHeight, 16*16, nil)
	mbnl := level.NewBitStorage(bitsForHeight, 16*16, nil)
	for col := 0; col < 16*16; col++ {
		// Heightmaps are stored relative to the world floor (minY).
		v := heights[col] - nc.minY
		if v < 0 {
			v = 0
		}
		ws.Set(col, v)
		mb.Set(col, v)
		mbnl.Set(col, v)
	}
	ch.HeightMaps.WorldSurface = ws
	ch.HeightMaps.MotionBlocking = mb
	ch.HeightMaps.MotionBlockingNoLeaves = mbnl

	ch.Status = level.StatusFull
}

// bitsLen is bits.Len without importing math/bits into the hot path twice.
func bitsLen(v uint) int {
	n := 0
	for v > 0 {
		n++
		v >>= 1
	}
	return n
}

// fullSkyLight is a full-brightness (level 15) sky-light array for one section: 4096
// nibbles packed into 2048 bytes, every nibble = 0xF (mirrors world.fullSkyLight).
func fullSkyLight() []byte {
	b := make([]byte, 2048)
	for i := range b {
		b[i] = 0xFF
	}
	return b
}
