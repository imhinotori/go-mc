// Package density ports the Minecraft 26.2 (protocol 776) density-function node
// types DIRECTLY from the unobfuscated server jar (temp/cache/26.2-inner.jar, read
// via `javap -c`) and a data-driven parser that builds the WHOLE wired overworld
// graph (Wave-1 embedded JSON) into a tree of these ported nodes.
//
// THE DATA/LOGIC SPLIT (PARITY-01): the graph SHAPE is DATA (the 120KB noise_router
// + the 35-file density_function tree the jar ships as JSON, embedded by Wave 1 and
// PARSED here), while the node TYPES are LOGIC (each a small pure Compute(ctx) ported
// constant-for-constant from net.minecraft.world.level.levelgen.DensityFunctions$*).
// Once the FULL node-type set parses + evaluates the whole graph, CAVES come "free":
// the cave functions (overworld/caves/{entrances,noodle,pillars,spaghetti_2d,...}) are
// just MORE nodes built from noise/shifted_noise/range_choice/spline, producing
// negative final_density regions the surface graph itself carves — NOT a separate
// system, and NOT from weird_scaled_sampler.
//
// THE 29 PORTED NODE TYPES (the authoritative used-set, derived by walking the real
// overworld.json noise_router + ALL overworld density_function/**.json this session):
//
//	abs add blend_alpha blend_density blend_offset cache_2d cache_once clamp cube
//	find_top_surface flat_cache half_negative interpolated interval_select invert
//	max min mul noise old_blended_noise quarter_negative range_choice shift_a
//	shift_b shifted_noise spline square squeeze y_clamped_gradient
//
// plus a bare JSON number = constant. end_islands is the ONLY DEFERRED density node
// (End-only, never referenced by the overworld graph); Parse errors loudly on it (or
// any unsupported type) — never a silent mis-evaluation (T-9-07).
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.DensityFunction          (compute/minValue/maxValue)
//   - net.minecraft.world.level.levelgen.DensityFunctions$*        (all node impls)
//   - net.minecraft.util.CubicSpline$Multipoint                    (the spline cubic)
//   - net.minecraft.util.Mth                                       (clampedMap/clamp/lerp/clampedLerp/floor)
//
// It is a faithful algorithmic port, not a copy of Mojang source.
package density

// Context is the per-sample input the density graph evaluates against —
// net.minecraft.world.level.levelgen.DensityFunction$FunctionContext, the subset the
// runtime supplies (block coordinates). The NoiseChunk (Wave 4) supplies this at each
// sampled cell corner; find_top_surface synthesizes a SinglePointContext with an
// overridden Y as it scans a column.
type Context struct {
	X, Y, Z int
}

// at returns a Context with the same X/Z but a different Y — mirrors
// DensityFunction$SinglePointContext(blockX, y, blockZ), used by find_top_surface's
// column scan.
func (c Context) at(y int) Context { return Context{X: c.X, Y: y, Z: c.Z} }

// Function is the ported net.minecraft.world.level.levelgen.DensityFunction interface:
// a pure Compute(ctx)->float64 plus the static value bounds the graph uses for the
// Ap2 (min/max) short-circuits and the range_choice/clamp bounds.
type Function interface {
	// Compute evaluates the node at the given block context.
	Compute(c Context) float64
	// MinValue is the conservative lower bound of Compute over all inputs
	// (DensityFunction.minValue) — load-bearing: Ap2 MIN short-circuits on it.
	MinValue() float64
	// MaxValue is the conservative upper bound of Compute over all inputs
	// (DensityFunction.maxValue) — load-bearing: Ap2 MAX short-circuits on it.
	MaxValue() float64
}

// MarkerKind classifies the cache/interpolation markers. interpolated is NOT a no-op
// (Pitfall 3): it tells the Wave-4 NoiseChunk to sample on the cell grid and lerp.
// The cache markers (flat_cache/cache_2d/cache_once) are correctness-transparent
// pass-throughs here (real per-cell/per-column caching is a Wave-4 perf refinement).
type MarkerKind int

const (
	// MarkerNone is "not a marker".
	MarkerNone MarkerKind = iota
	// MarkerInterpolated is the interpolated marker — preserved so NoiseChunk can
	// detect it at cell corners (DensityFunctions$Marker$Type.Interpolated).
	MarkerInterpolated
	// MarkerFlatCache is flat_cache (per-column 2D cache; transparent here).
	MarkerFlatCache
	// MarkerCache2D is cache_2d (per-(x,z) cache; transparent here).
	MarkerCache2D
	// MarkerCacheOnce is cache_once (single-cell cache; transparent here).
	MarkerCacheOnce
)

// Marked is implemented by marker nodes so a consumer (the NoiseChunk) can detect the
// marker kind and unwrap the wrapped argument without flattening the graph.
type Marked interface {
	Function
	// Kind reports which marker this node is.
	Kind() MarkerKind
	// Wrapped returns the inner argument the marker wraps.
	Wrapped() Function
}
