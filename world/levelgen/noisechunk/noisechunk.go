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
	// firstCellX/firstCellZ = Math.floorDiv(blockX/Z, cellWidth): the cell-grid origin the
	// cell-corner FILL loop uses (NoiseChunk.initializeForFirstCellX/advanceCellX/fillSlice
	// all read firstCellX/Z, then blockX = cellX * cellWidth). For the overworld cellWidth=4
	// so this equals blockX>>2, but for the End cellWidth=8 it does NOT -- the fill loop must
	// use floorDiv, not the quart shift. CITE: NoiseChunk ctor (Math.floorDiv) + fillSlice.
	firstCellX int
	firstCellZ int
	// firstNoiseX/firstNoiseZ = QuartPos.fromBlock(blockX/Z) = blockX>>2: the QUART origin,
	// used ONLY by the flat_cache (2D quart-resolution cache), NOT the cell fill loop.
	firstNoiseX int // QuartPos.fromBlock(chunkX*16)
	firstNoiseZ int

	// density is the per-block final_density, flat-indexed by densityIndex (localX,
	// worldY, localZ). Computed once at construction by evaluating the rewritten
	// final_density per block while the interpolators feed their trilerped corner values.
	density []float64

	// wrappedFinalDensity is final_density after density.MapAll(wrap): each interpolated
	// marker is now an *interpolatedFn (in interps), everything else per-block.
	wrappedFinalDensity density.Function
	interps             []*interpolatedFn // every interpolator the rewrite produced
	fillState           *fillState        // toggles interps between trilerp + direct sampling

	// beard is the STRUCT-POLISH-03 terrain-adaptation contribution (the structure
	// Beardifier). It is the ADDITIVE, NON-interpolated per-block term vanilla's NoiseChunk
	// substitutes for DensityFunctions$BeardifierMarker — added to final_density AFTER the
	// trilerp at the exact block coords (A5). nil means no adapting structure influences
	// this chunk -> the fill is byte-identical (the NONE regression guard). Cite:
	// NoiseChunk ctor's beardifier wrap of the BeardifierMarker.
	beard func(wx, wy, wz int) float64

	cornerSamples int // count of interpolated-filler corner evaluations (the sparse-sample gate)

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
	return NewNoiseChunkWithBeard(r, pos, nil)
}

// NewNoiseChunkWithBeard builds the NoiseChunk threading an optional structure-Beardifier
// contribution (STRUCT-POLISH-03): beard(wx,wy,wz) is added to final_density per block AFTER
// the trilerp, mirroring the DensityFunctions$BeardifierMarker substitution vanilla performs
// in the NoiseChunk constructor. A nil beard is byte-identical to NewNoiseChunk (the NONE
// regression guard: structures that do not adapt contribute 0 -> identical density field).
//
// PURE over (seed, pos, beard): the beard is itself a pure function of the chunk's structure
// starts (computed pre-fill in the generator from the singleflight-memoized cache), so the
// chunk stays deterministic.
func NewNoiseChunkWithBeard(r *router.Router, pos level.ChunkPos, beard func(wx, wy, wz int) float64) *NoiseChunk {
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
		firstCellX:    floorDiv(blockX, cellWidth),     // Math.floorDiv(blockX, cellWidth)
		firstCellZ:    floorDiv(blockZ, cellWidth),     // Math.floorDiv(blockZ, cellWidth)
		firstNoiseX:   blockX >> 2,                     // QuartPos.fromBlock(blockX)
		firstNoiseZ:   blockZ >> 2,                     // QuartPos.fromBlock(blockZ)
		beard:         beard,
	}
	nc.resolveBlocks()
	nc.wrapFinalDensity(r.NoiseRouter.FinalDensity)
	nc.fill()
	return nc
}

// wrapFinalDensity ports the interpolated-marker half of NoiseChunk's
// noiseRouter.mapAll(this::wrap): it rewrites final_density so each MarkerInterpolated
// node becomes an *interpolatedFn (collected in nc.interps), leaving every surrounding
// op per-block. The cache markers (flat_cache/cache_2d/cache_once) stay transparent
// pass-throughs (MapAll preserves them; correctness-identical). The other NoiseRouter
// functions (preliminary_surface_level, aquifer inputs) are NOT wrapped: they are
// sampled off the cell loop, where interpolators fall back to direct sampling anyway, so
// wrapping them would be a no-op for correctness.
func (nc *NoiseChunk) wrapFinalDensity(finalDensity density.Function) {
	nc.fillState = &fillState{}
	nc.wrappedFinalDensity = density.MapAll(finalDensity, func(fn density.Function) density.Function {
		m, ok := fn.(density.Marked)
		if !ok {
			return fn
		}
		// NoiseChunk.wrapNew: replace each cache/interp marker with its memoizing wrapper. Was: only
		// Interpolated was replaced; flat_cache/cache_2d/cache_once fell through as pass-throughs, so their
		// Perlin sub-trees re-evaluated per-block (the 1.2s/chunk hotspot). Now all four are wrapped 1:1.
		switch m.Kind() {
		case density.MarkerInterpolated:
			ip := newInterpolatedFn(m.Wrapped(), nc.fillState,
				nc.cellCountXZ, nc.cellCountY, nc.cellWidth, nc.cellHeight, nc.cellNoiseMinY)
			nc.interps = append(nc.interps, ip)
			return ip
		case density.MarkerFlatCache:
			return newFlatCache(m.Wrapped(), nc.cellCountXZ, nc.firstNoiseX, nc.firstNoiseZ)
		case density.MarkerCache2D:
			return &cache2D{fn: m.Wrapped(), state: nc.fillState}
		case density.MarkerCacheOnce:
			return &cacheOnce{fn: m.Wrapped(), state: nc.fillState}
		default:
			return fn
		}
	})
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

// fill drives EVERY interpolator (one per interpolated marker) on the cell-corner grid
// and, for each block, evaluates the rewritten final_density — the interpolators return
// their trilerped value while every surrounding op (squeeze/min/the noodle cave graph)
// computes per-block. It mirrors NoiseBasedChunkGenerator.doFill's cell nesting order
// (NoiseChunk.initializeForFirstCellX / advanceCellX / selectCellYZ / updateForY/X/Z):
//
//	for cellX in [0,cellCountXZ): advanceCellX (fill far X face of every interpolator)
//	  for cellZ in [0,cellCountXZ):
//	    for cellY in [cellCountY-1 .. 0]: selectCellYZ(cellY, cellZ) on each interpolator
//	      for inCellY in [cellHeight-1 .. 0]: updateForY(dy)
//	        for inCellX in [0,cellWidth): updateForX(dx)
//	          for inCellZ in [0,cellWidth): updateForZ(dz); filling=true;
//	                                        density = wrappedFinalDensity.Compute(ctx)
//
// If the graph has NO interpolated marker (degenerate), the per-block compute still runs
// correctly (no interpolator to drive).
func (nc *NoiseChunk) fill() {
	nc.density = make([]float64, 16*nc.height*16)

	// initializeForFirstCellX: fill slice0 of every interpolator at cellX = firstCellX
	// (NoiseChunk.initializeForFirstCellX calls fillSlice(true, firstCellX)).
	// state.filling stays false here so the inner fillers sample directly at corners.
	nc.fillState.filling = false
	nc.fillSlices(true, nc.firstCellX)

	for cellX := 0; cellX < nc.cellCountXZ; cellX++ {
		// advanceCellX: fill slice1 of every interpolator at firstCellX+cellX+1.
		nc.fillState.filling = false
		nc.fillSlices(false, nc.firstCellX+cellX+1)

		for cellZ := 0; cellZ < nc.cellCountXZ; cellZ++ {
			for cellY := nc.cellCountY - 1; cellY >= 0; cellY-- {
				for _, ip := range nc.interps {
					ip.selectCellYZ(cellY, cellZ)
				}
				for inY := nc.cellHeight - 1; inY >= 0; inY-- {
					dy := float64(inY) / float64(nc.cellHeight)
					for _, ip := range nc.interps {
						ip.updateForY(dy)
					}
					worldY := (nc.cellNoiseMinY+cellY)*nc.cellHeight + inY
					for inX := 0; inX < nc.cellWidth; inX++ {
						dx := float64(inX) / float64(nc.cellWidth)
						for _, ip := range nc.interps {
							ip.updateForX(dx)
						}
						localX := cellX*nc.cellWidth + inX
						for inZ := 0; inZ < nc.cellWidth; inZ++ {
							dz := float64(inZ) / float64(nc.cellWidth)
							for _, ip := range nc.interps {
								ip.updateForZ(dz)
							}
							localZ := cellZ*nc.cellWidth + inZ
							worldX := nc.WorldX(localX)
							worldZ := nc.WorldZ(localZ)
							// filling=true: interpolated nodes return their trilerped value,
							// every other op computes per-block at the exact block coords.
							// Bump interpCounter so a cacheOnce wrapper memoizes for THIS block visit only.
							nc.fillState.filling = true
							nc.fillState.interpCounter++
							v := nc.wrappedFinalDensity.Compute(density.Context{X: worldX, Y: worldY, Z: worldZ})
							nc.fillState.filling = false
							// STRUCT-POLISH-03: add the structure Beardifier contribution to
							// final_density AFTER the trilerp (the BeardifierMarker substitution
							// vanilla's NoiseChunk ctor performs — an ADDITIVE, NON-interpolated
							// per-block term, A5). nil beard -> no add -> byte-identical (the NONE
							// regression guard). Gated internally on the Beardifier's affectedBox.
							v += nc.beardAt(worldX, worldY, worldZ)
							nc.density[nc.densityIndex(localX, worldY, localZ)] = v
						}
					}
				}
			}
		}
		// swapSlices: roll the just-filled far face into slice0 for the next cellX.
		for _, ip := range nc.interps {
			ip.swapSlices()
		}
	}
}

// fillSlices fills one X face of EVERY interpolator's corner buffers (NoiseChunk.fillSlice
// loops over interpolators), counting each interpolated-filler corner evaluation so
// TestCellSampleNotPerBlock can assert sparse sampling. cellX is the absolute X cell index
// (in noise-cell units).
func (nc *NoiseChunk) fillSlices(onSlice0 bool, cellX int) {
	for _, ip := range nc.interps {
		ip.fillSlice(onSlice0, cellX, nc.firstCellZ)
		nc.cornerSamples += (nc.cellCountXZ + 1) * (nc.cellCountY + 1)
	}
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

// Pos returns the chunk position (the Wave-7 surface system needs the chunk origin
// to map local↔world coords for the SurfaceRules$Context).
func (nc *NoiseChunk) Pos() level.ChunkPos { return nc.pos }

// PreliminarySurfaceLevel exposes NoiseChunk.preliminarySurfaceLevel(x,z) for the
// Wave-7 surface system: the SurfaceRules$Context.getMinSurfaceLevel bilinear-lerps
// the preliminary surface level over the 4-block surface cell to drive the
// above_preliminary_surface condition. (Internally it snaps x,z to quart resolution
// and samples the bound preliminary_surface_level density function.)
func (nc *NoiseChunk) PreliminarySurfaceLevel(x, z int) int {
	return nc.preliminarySurfaceLevel(x, z)
}

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
		// Count the section's fluid blocks for the chunk packet's second short
		// (LevelChunkSection.nonEmptyFluidCount). The client uses it to decide whether the section
		// holds fluid it must SIMULATE — a hardcoded 0 made generated/aquifer water inert
		// client-side, so a player would not float in it until a block update woke the cell.
		s.FluidCount = level.CountFluidBlocks(s)
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
