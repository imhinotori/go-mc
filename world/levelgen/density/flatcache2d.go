package density

// flatcache2d.go — a LAZY, map-backed 2D memoizing cache for the biome CLIMATE density
// functions (temperature/humidity/continentalness/erosion/depth/weirdness). It is the
// biome-source analog of the NoiseChunk FlatCache the final_density path already uses.
//
// WHY (perf, NOT behavior): the six climate DFs are evaluated by the biome Sampler at every
// quart cell — and FillBiomes samples the WHOLE chunk column-by-column at 96 distinct Y quart
// levels per (x,z) column. The five climate DFs that vanilla wraps in a `flat_cache` marker are
// Y-INDEPENDENT (2D over x,z) by that very declaration, so re-evaluating their Perlin stacks
// once per Y level is pure waste: a single warm Generate makes ~14.6k Sampler.sample calls
// spanning only ~148 distinct (x,z) quart columns (measured). The existing per-quart-cell
// biomeCache keys on the 3D cell (x,y,z)>>2, but the Y-dependent `depth` DF makes almost every
// 3D cell distinct, so that cache does NOT collapse the Y-flat DFs — each still re-runs ~99x.
//
// This wraps ONLY nodes vanilla marks `flat_cache` (its own guarantee they are Y-flat), so
// memoizing their value by the 2D (blockX, blockZ) key and returning it for ANY Y is
// BYTE-IDENTICAL: the cache elides recomputes of a PURE, Y-independent function at a repeated
// (x,z). The Y-dependent parts (y_clamped_gradient inside `depth`) are NOT flat_cache-marked
// and pass through untouched. It is a LAZY map (unlike NoiseChunk$FlatCache's fixed-lattice
// precompute) so it covers the full multi-chunk region the biome source samples (center chunk +
// the surface/decoration neighbor reads) with no out-of-lattice fallback misses.
//
// SCOPE: one instance per Generate call (never shared across goroutines), so the map needs no
// synchronization and Generate stays pure over (seed, pos) — exactly like world.biomeCache.

// flatCache2D wraps a flat_cache-marked (Y-flat) function and memoizes its value per 2D quart
// cell. The key is the quart lattice (blockX>>2, blockZ>>2) — the resolution the biome sampler
// works at — so every block in a 4x4 (x,z) footprint shares one evaluation, identical to what
// the wrapped Y-flat function would return for any of them.
type flatCache2D struct {
	fn    Function
	cache map[[2]int]float64
}

func newFlatCache2D(fn Function) *flatCache2D {
	return &flatCache2D{fn: fn, cache: make(map[[2]int]float64)}
}

func (c *flatCache2D) Compute(ctx Context) float64 {
	key := [2]int{ctx.X >> 2, ctx.Z >> 2}
	if v, ok := c.cache[key]; ok {
		return v
	}
	v := c.fn.Compute(ctx)
	c.cache[key] = v
	return v
}

func (c *flatCache2D) MinValue() float64 { return c.fn.MinValue() }
func (c *flatCache2D) MaxValue() float64 { return c.fn.MaxValue() }

// WrapClimateFlatCaches rewrites fn (a climate density function) so every `flat_cache` marker
// in it is replaced by a lazy 2D memoizing cache over the marker's wrapped subtree. It reuses
// MapAll (the same graph-rewrite hinge NoiseChunk.wrapFinalDensity uses) so shared subtrees are
// wrapped exactly once (identity-deduped) and every surrounding op is preserved. The result is
// a NEW, INDEPENDENT graph (fresh caches) suitable for a single Generate call; the input fn is
// left untouched (the shared, immutable per-world router functions).
//
// Only MarkerFlatCache nodes are replaced. Because vanilla declares those Y-independent, the 2D
// memoization is byte-identical to the un-wrapped evaluation. Every other node — including the
// Y-dependent y_clamped_gradient inside `depth` and the other marker kinds — is copied verbatim.
func WrapClimateFlatCaches(fn Function) Function {
	if fn == nil {
		return nil
	}
	return MapAll(fn, func(node Function) Function {
		if m, ok := node.(Marked); ok && m.Kind() == MarkerFlatCache {
			return newFlatCache2D(m.Wrapped())
		}
		return node
	})
}
