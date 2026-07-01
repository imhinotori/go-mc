package noisechunk

// cache.go — the NoiseChunk cache-marker wrappers (NoiseChunk.wrapNew's FlatCache / Cache2D /
// CacheOnce cases). PORTED 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.level.levelgen
// .NoiseChunk$Cache2D / $CacheOnce / $FlatCache, read via CFR this session).
//
// WHY THIS EXISTS (perf, NOT behavior): a density.marker of kind flat_cache/cache_2d/cache_once was a
// pass-through (marker.Compute == argument.Compute), so the sub-tree it wraps — typically a stack of
// Perlin octaves — was RE-EVALUATED for EVERY block in fill()'s per-block loop (16×height×16 ≈ 98k
// points/chunk). That is why BenchmarkGenerateOneChunk was ~1.2s (63% in PerlinNoise.GetValue). Vanilla
// replaces each cache marker in NoiseChunk.wrapNew with a memoizing wrapper so the wrapped tree is
// computed at most once per (x,z) column (Cache2D), once per quart-cell 2D lattice point (FlatCache), or
// once per interpolation step (CacheOnce). These wrappers reproduce that memoization EXACTLY — same
// value, far fewer Perlin evaluations. The results are byte-identical: the cache only elides recomputes
// of a PURE function at a repeated coordinate.

import "github.com/imhinotori/sulfur/world/levelgen/density"

// cache2D ports NoiseChunk$Cache2D: memoize the wrapped function's value for the last (blockX, blockZ).
// fill() iterates Y innermost within a fixed (x,z) column, so a cache_2d sub-tree (which by construction
// does not depend on Y) is computed ONCE per column instead of once per block in the column.
//
//	[VERIFIED CFR NoiseChunk$Cache2D.compute: pos2D = ChunkPos.pack(blockX, blockZ); if (lastPos2D ==
//	 pos2D) return lastValue; lastPos2D = pos2D; lastValue = function.compute(context); return lastValue.]
type cache2D struct {
	fn      density.Function
	state   *fillState
	hasLast bool
	lastX   int
	lastZ   int
	lastVal float64
}

func (c *cache2D) Compute(ctx density.Context) float64 {
	// Only memoize inside the per-block loop (filling==true). During fillSlice (filling==false) the
	// interpolators sample corner columns in sequence; caching there would hand one column's value to the
	// next. Vanilla's Cache2D lives per-NoiseChunk and its keying (ChunkPos.pack of blockX/Z) makes the
	// corner grid re-key naturally, but our shared-tree wrapper is also hit by the foreign corner context,
	// so gate on filling like CacheOnce. (In the per-block loop Y is innermost within a fixed (x,z), so
	// this still collapses a whole column to one evaluation — the intended win.)
	if !c.state.filling {
		return c.fn.Compute(ctx)
	}
	if c.hasLast && ctx.X == c.lastX && ctx.Z == c.lastZ {
		return c.lastVal
	}
	c.lastX, c.lastZ = ctx.X, ctx.Z
	c.lastVal = c.fn.Compute(ctx)
	c.hasLast = true
	return c.lastVal
}

func (c *cache2D) MinValue() float64 { return c.fn.MinValue() }
func (c *cache2D) MaxValue() float64 { return c.fn.MaxValue() }

// cacheOnce ports NoiseChunk$CacheOnce: memoize the wrapped value for the current interpolation step.
// fill() bumps interpCounter once per block-visit; a cache_once sub-tree evaluated multiple times WITHIN
// the same block-visit (e.g. referenced by two parents) is computed once. Also serves as a within-step
// dedup. (The array/fillArray path of vanilla's CacheOnce is not modeled — v1 has no ContextProvider
// array fill; the scalar compute path is the one fill() drives.)
//
//	[VERIFIED CFR NoiseChunk$CacheOnce.compute: if (context != noiseChunk) return function.compute(context);
//	 if (lastCounter == interpolationCounter) return lastValue; lastCounter = interpolationCounter;
//	 lastValue = function.compute(context); return lastValue.]
type cacheOnce struct {
	fn          density.Function
	state       *fillState
	hasLast     bool
	lastCounter uint64
	lastVal     float64
}

func (c *cacheOnce) Compute(ctx density.Context) float64 {
	// Vanilla guards `if (context != this.noiseChunk) return function.compute(context)` — CacheOnce only
	// memoizes inside the chunk's own per-block interpolation loop, NOT when sampled with a foreign
	// context (e.g. the corner SinglePointContexts the interpolators fill from, or the exact-corner probe
	// in tests). Our equivalent: only cache while filling==true (the per-block loop bumps interpCounter);
	// during fillSlice (filling==false) interpCounter is frozen, so caching there would return one corner's
	// value for every corner. Compute directly when not filling.
	if !c.state.filling {
		return c.fn.Compute(ctx)
	}
	if c.hasLast && c.lastCounter == c.state.interpCounter {
		return c.lastVal
	}
	c.lastCounter = c.state.interpCounter
	c.lastVal = c.fn.Compute(ctx)
	c.hasLast = true
	return c.lastVal
}

func (c *cacheOnce) MinValue() float64 { return c.fn.MinValue() }
func (c *cacheOnce) MaxValue() float64 { return c.fn.MaxValue() }

// flatCache ports NoiseChunk$FlatCache: precompute the wrapped function on the (noiseSizeXZ+1)² quart
// lattice at construction (SinglePointContext(blockX, 0, blockZ), Y=0), then a lookup by quart cell for
// any block in the chunk. This is the heavy hitter for the CLIMATE density functions (temperature/
// humidity/continentalness/…): flat over Y, they collapse from ~98k evals to (sizeXZ+1)² ≈ 25 evals.
// A coordinate OUTSIDE the precomputed lattice falls back to a direct compute (vanilla's guard).
//
//	[VERIFIED CFR NoiseChunk$FlatCache: values[(sizeXZ+1)²] filled at ctor over quartX/quartZ =
//	 firstNoiseX/Z + x/z, blockX/Z = QuartPos.toBlock(quart), SinglePointContext(blockX,0,blockZ);
//	 compute: x = fromBlock(blockX) − firstNoiseX; z likewise; in-range → values[x + z*sizeXZ], else direct.]
type flatCache struct {
	fn         density.Function
	values     []float64
	sizeXZ     int // noiseSizeXZ + 1
	firstNoiseX int // quart origin (QuartPos.fromBlock(chunkMinBlockX))
	firstNoiseZ int
}

func newFlatCache(fn density.Function, noiseSizeXZ, firstNoiseX, firstNoiseZ int) *flatCache {
	sizeXZ := noiseSizeXZ + 1
	fc := &flatCache{
		fn:          fn,
		values:      make([]float64, sizeXZ*sizeXZ),
		sizeXZ:      sizeXZ,
		firstNoiseX: firstNoiseX,
		firstNoiseZ: firstNoiseZ,
	}
	for x := 0; x < sizeXZ; x++ {
		quartX := firstNoiseX + x
		blockX := quartToBlock(quartX)
		for z := 0; z < sizeXZ; z++ {
			quartZ := firstNoiseZ + z
			blockZ := quartToBlock(quartZ)
			fc.values[x+z*sizeXZ] = fn.Compute(density.Context{X: blockX, Y: 0, Z: blockZ})
		}
	}
	return fc
}

func (c *flatCache) Compute(ctx density.Context) float64 {
	quartX := blockToQuart(ctx.X)
	quartZ := blockToQuart(ctx.Z)
	x := quartX - c.firstNoiseX
	z := quartZ - c.firstNoiseZ
	if x >= 0 && z >= 0 && x < c.sizeXZ && z < c.sizeXZ {
		return c.values[x+z*c.sizeXZ]
	}
	return c.fn.Compute(ctx)
}

func (c *flatCache) MinValue() float64 { return c.fn.MinValue() }
func (c *flatCache) MaxValue() float64 { return c.fn.MaxValue() }

// quartToBlock / blockToQuart mirror QuartPos.toBlock (q<<2) / fromBlock (b>>2). Quart = 4-block cell.
func quartToBlock(q int) int { return q << 2 }
func blockToQuart(b int) int { return b >> 2 }
