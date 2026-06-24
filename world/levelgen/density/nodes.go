package density

import (
	"math"

	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// This file ports the FULL overworld density-function node set from
// net.minecraft.world.level.levelgen.DensityFunctions$* (javap -c, 26.2-inner.jar).
// Each Compute is ported constant-for-constant from the bytecode; MinValue/MaxValue
// mirror DensityFunction.minValue/maxValue (load-bearing for the Ap2 short-circuits).

// ---- Mth helpers (net.minecraft.util.Mth, javap -c) ----

// mthClamp = Mth.clamp(v,lo,hi): v<lo ? lo : min(v,hi).
func mthClamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	return math.Min(v, hi)
}

// mthLerp = Mth.lerp(delta,start,end) = start + delta*(end-start).
func mthLerp(delta, start, end float64) float64 { return start + delta*(end-start) }

// mthInverseLerp = Mth.inverseLerp(x,a,b) = (x-a)/(b-a).
func mthInverseLerp(x, a, b float64) float64 { return (x - a) / (b - a) }

// mthClampedLerp = Mth.clampedLerp(delta,start,end): delta<0?start : delta>1?end : lerp.
func mthClampedLerp(delta, start, end float64) float64 {
	if delta < 0 {
		return start
	}
	if delta > 1 {
		return end
	}
	return mthLerp(delta, start, end)
}

// mthClampedMap = Mth.clampedMap(x,a,b,c,d) = clampedLerp(inverseLerp(x,a,b), c, d).
func mthClampedMap(x, a, b, c, d float64) float64 {
	return mthClampedLerp(mthInverseLerp(x, a, b), c, d)
}

// mthFloor = Mth.floor(double) = (int)Math.floor.
func mthFloor(d float64) int { return int(math.Floor(d)) }

// ---- constant (a bare JSON number) ----

// constantFn is DensityFunctions$Constant.
type constantFn struct{ v float64 }

func (f constantFn) Compute(Context) float64 { return f.v }
func (f constantFn) MinValue() float64       { return f.v }
func (f constantFn) MaxValue() float64       { return f.v }

// ---- y_clamped_gradient ----

// yClampedGradient is DensityFunctions$YClampedGradient:
// Compute = Mth.clampedMap(blockY, fromY, toY, fromValue, toValue).
type yClampedGradient struct {
	fromY, toY         float64
	fromValue, toValue float64
}

func (f yClampedGradient) Compute(c Context) float64 {
	return mthClampedMap(float64(c.Y), f.fromY, f.toY, f.fromValue, f.toValue)
}
func (f yClampedGradient) MinValue() float64 { return math.Min(f.fromValue, f.toValue) }
func (f yClampedGradient) MaxValue() float64 { return math.Max(f.fromValue, f.toValue) }

// ---- Ap2: add / mul / min / max (DensityFunctions$Ap2) ----

type ap2Type int

const (
	ap2Add ap2Type = iota
	ap2Mul
	ap2Min
	ap2Max
)

// ap2 is DensityFunctions$Ap2 (TwoArgumentSimpleFunction): the short-circuiting
// two-argument arithmetic. The MUL/MIN/MAX short-circuits use the arguments' bounds,
// so MinValue/MaxValue must be correct (DensityFunctions.TwoArgumentSimpleFunction.create
// derives the bounds; ported below).
type ap2 struct {
	typ      ap2Type
	a1, a2   Function
	min, max float64
}

// newAp2 builds an Ap2 and precomputes its bounds exactly as
// TwoArgumentSimpleFunction.create does from the bytecode.
func newAp2(typ ap2Type, a1, a2 Function) *ap2 {
	d1mn, d1mx := a1.MinValue(), a1.MaxValue()
	d2mn, d2mx := a2.MinValue(), a2.MaxValue()
	var mn, mx float64
	switch typ {
	case ap2Add:
		mn = d1mn + d2mn
		mx = d1mx + d2mx
	case ap2Mul:
		// MUL bounds: from the four corner products, but Mojang uses a sign-aware
		// reduction. Mirror TwoArgumentSimpleFunction.create's MUL branch:
		//   min = d1mn>0 && d2mn>0 ? d1mn*d2mn : (d1mx<0 && d2mx<0 ? d1mx*d2mx : min(d1mn*d2mx, d1mx*d2mn))
		//   max = d1mn>0 && d2mn>0 ? d1mx*d2mx : (d1mx<0 && d2mx<0 ? d1mn*d2mn : max(d1mn*d2mn, d1mx*d2mx))
		if d1mn > 0 && d2mn > 0 {
			mn = d1mn * d2mn
			mx = d1mx * d2mx
		} else if d1mx < 0 && d2mx < 0 {
			mn = d1mx * d2mx
			mx = d1mn * d2mn
		} else {
			mn = math.Min(d1mn*d2mx, d1mx*d2mn)
			mx = math.Max(d1mn*d2mn, d1mx*d2mx)
		}
	case ap2Min:
		mn = math.Min(d1mn, d2mn)
		mx = math.Min(d1mx, d2mx)
	case ap2Max:
		mn = math.Max(d1mn, d2mn)
		mx = math.Max(d1mx, d2mx)
	}
	return &ap2{typ: typ, a1: a1, a2: a2, min: mn, max: mx}
}

// Compute ports Ap2.compute (the short-circuiting evaluation from the bytecode).
func (f *ap2) Compute(c Context) float64 {
	d := f.a1.Compute(c)
	switch f.typ {
	case ap2Add:
		return d + f.a2.Compute(c)
	case ap2Mul:
		if d == 0 {
			return 0
		}
		return d * f.a2.Compute(c)
	case ap2Min:
		if d < f.a2.MinValue() {
			return d
		}
		return math.Min(d, f.a2.Compute(c))
	case ap2Max:
		if d > f.a2.MaxValue() {
			return d
		}
		return math.Max(d, f.a2.Compute(c))
	}
	return 0
}
func (f *ap2) MinValue() float64 { return f.min }
func (f *ap2) MaxValue() float64 { return f.max }

// ---- Mapped: abs/square/cube/half_negative/quarter_negative/invert/squeeze ----

type mappedType int

const (
	mapAbs mappedType = iota
	mapSquare
	mapCube
	mapHalfNegative
	mapQuarterNegative
	mapInvert
	mapSqueeze
)

// mapped is DensityFunctions$Mapped: a unary transform of its input.
// transform() ported constant-for-constant from the bytecode:
//
//	ABS              = Math.abs(x)
//	SQUARE           = x*x
//	CUBE             = x*x*x
//	HALF_NEGATIVE    = x>0 ? x : x*0.5
//	QUARTER_NEGATIVE = x>0 ? x : x*0.25
//	INVERT           = 1.0 / x           (NOT -x — verified from bytecode)
//	SQUEEZE          = d=clamp(x,-1,1); d/2 - d*d*d/24
type mapped struct {
	typ      mappedType
	input    Function
	min, max float64
}

func transform(t mappedType, x float64) float64 {
	switch t {
	case mapAbs:
		return math.Abs(x)
	case mapSquare:
		return x * x
	case mapCube:
		return x * x * x
	case mapHalfNegative:
		if x > 0 {
			return x
		}
		return x * 0.5
	case mapQuarterNegative:
		if x > 0 {
			return x
		}
		return x * 0.25
	case mapInvert:
		return 1.0 / x
	case mapSqueeze:
		d := mthClamp(x, -1, 1)
		return d/2.0 - d*d*d/24.0
	}
	return x
}

// newMapped builds a Mapped and precomputes its bounds as DensityFunctions$Mapped's
// minValue/maxValue do: transform both endpoints and take min/max — except ABS/SQUARE
// which clamp the lower bound to 0 when the input straddles zero (Mojang's special
// cases). We port the conservative-but-correct version Mojang uses.
func newMapped(typ mappedType, input Function) *mapped {
	imn, imx := input.MinValue(), input.MaxValue()
	m := &mapped{typ: typ, input: input}
	// DensityFunctions$Mapped.minValue/maxValue: for ABS/SQUARE the floor is 0 when
	// the input spans zero; otherwise it is min/max of the transformed endpoints.
	switch typ {
	case mapAbs, mapSquare:
		lo := math.Max(0, imn)
		hi := math.Max(math.Abs(imn), math.Abs(imx))
		m.min = transform(typ, lo)
		m.max = transform(typ, hi)
	default:
		t0 := transform(typ, imn)
		t1 := transform(typ, imx)
		m.min = math.Min(t0, t1)
		m.max = math.Max(t0, t1)
	}
	return m
}

func (f *mapped) Compute(c Context) float64 { return transform(f.typ, f.input.Compute(c)) }
func (f *mapped) MinValue() float64         { return f.min }
func (f *mapped) MaxValue() float64         { return f.max }

// ---- clamp (DensityFunctions$Clamp) ----

type clampFn struct {
	input    Function
	min, max float64
}

func (f *clampFn) Compute(c Context) float64 { return mthClamp(f.input.Compute(c), f.min, f.max) }
func (f *clampFn) MinValue() float64         { return f.min }
func (f *clampFn) MaxValue() float64         { return f.max }

// ---- range_choice (DensityFunctions$RangeChoice) ----

// rangeChoice: v=input.Compute; (minInclusive<=v<maxExclusive) ? whenInRange : whenOutOfRange.
type rangeChoice struct {
	input                       Function
	minInclusive, maxExclusive  float64
	whenInRange, whenOutOfRange Function
}

func (f *rangeChoice) Compute(c Context) float64 {
	v := f.input.Compute(c)
	if v >= f.minInclusive && v < f.maxExclusive {
		return f.whenInRange.Compute(c)
	}
	return f.whenOutOfRange.Compute(c)
}
func (f *rangeChoice) MinValue() float64 {
	return math.Min(f.whenInRange.MinValue(), f.whenOutOfRange.MinValue())
}
func (f *rangeChoice) MaxValue() float64 {
	return math.Max(f.whenInRange.MaxValue(), f.whenOutOfRange.MaxValue())
}

// ---- interval_select (DensityFunctions$IntervalSelect) ----

// intervalSelect: v=input.Compute; first i where v<thresholds[i] -> functions[i];
// else functions.last(). Invariant: len(functions) == len(thresholds)+1.
type intervalSelect struct {
	input      Function
	thresholds []float64
	functions  []Function
	min, max   float64
}

func newIntervalSelect(input Function, thresholds []float64, functions []Function) *intervalSelect {
	mn := math.Inf(1)
	mx := math.Inf(-1)
	for _, fn := range functions {
		mn = math.Min(mn, fn.MinValue())
		mx = math.Max(mx, fn.MaxValue())
	}
	return &intervalSelect{input: input, thresholds: thresholds, functions: functions, min: mn, max: mx}
}

func (f *intervalSelect) Compute(c Context) float64 {
	v := f.input.Compute(c)
	for i := 0; i < len(f.thresholds); i++ {
		if v < f.thresholds[i] {
			return f.functions[i].Compute(c)
		}
	}
	return f.functions[len(f.functions)-1].Compute(c)
}
func (f *intervalSelect) MinValue() float64 { return f.min }
func (f *intervalSelect) MaxValue() float64 { return f.max }

// ---- noise (DensityFunctions$Noise) ----

// noiseFn binds a *synth.NormalNoise (seeded by RandomState in Wave 2) and samples it:
// Compute = noise.GetValue(blockX*xzScale, blockY*yScale, blockZ*xzScale).
// minValue = -maxValue; maxValue = noise.MaxValue() (Noise.minValue/maxValue).
type noiseFn struct {
	noise           *synth.NormalNoise
	xzScale, yScale float64
	noiseID         string // the registry id, carried so RandomState can seed it (Pitfall 1)
}

func (f *noiseFn) Compute(c Context) float64 {
	return f.noise.GetValue(float64(c.X)*f.xzScale, float64(c.Y)*f.yScale, float64(c.Z)*f.xzScale)
}
func (f *noiseFn) MinValue() float64 { return -f.noise.MaxValue() }
func (f *noiseFn) MaxValue() float64 { return f.noise.MaxValue() }

// ---- shifted_noise (DensityFunctions$ShiftedNoise) ----

// shiftedNoise: Compute = noise.GetValue(
//
//	blockX*xzScale + shiftX.Compute,
//	blockY*yScale  + shiftY.Compute,
//	blockZ*xzScale + shiftZ.Compute).
type shiftedNoise struct {
	noise                  *synth.NormalNoise
	shiftX, shiftY, shiftZ Function
	xzScale, yScale        float64
	noiseID                string
}

func (f *shiftedNoise) Compute(c Context) float64 {
	x := float64(c.X)*f.xzScale + f.shiftX.Compute(c)
	y := float64(c.Y)*f.yScale + f.shiftY.Compute(c)
	z := float64(c.Z)*f.xzScale + f.shiftZ.Compute(c)
	return f.noise.GetValue(x, y, z)
}
func (f *shiftedNoise) MinValue() float64 { return -f.noise.MaxValue() }
func (f *shiftedNoise) MaxValue() float64 { return f.noise.MaxValue() }

// ---- shift_a / shift_b (DensityFunctions$ShiftA / $ShiftB) ----

// The Shift base computes offsetNoise.GetValue(x*0.25, y*0.25, z*0.25) * 4.0.
//
//	ShiftA.compute(ctx) = shift(blockX, 0,      blockZ)
//	ShiftB.compute(ctx) = shift(blockZ, blockX, 0)
//
// The Shift's bound is conservative: maxValue = noise.MaxValue()*4 (the *0.25 only
// scales the input). minValue = -maxValue.
type shiftType int

const (
	shiftA shiftType = iota
	shiftB
)

type shift struct {
	typ     shiftType
	noise   *synth.NormalNoise
	noiseID string
}

func (f *shift) shiftValue(x, y, z float64) float64 {
	return f.noise.GetValue(x*0.25, y*0.25, z*0.25) * 4.0
}
func (f *shift) Compute(c Context) float64 {
	switch f.typ {
	case shiftA:
		return f.shiftValue(float64(c.X), 0, float64(c.Z))
	case shiftB:
		return f.shiftValue(float64(c.Z), float64(c.X), 0)
	}
	return 0
}
func (f *shift) MinValue() float64 { return -f.MaxValue() }
func (f *shift) MaxValue() float64 { return f.noise.MaxValue() * 4.0 }

// ---- old_blended_noise (DensityFunctions$BlendedNoise wrapper) ----

// oldBlendedNoise binds a *synth.BlendedNoise (the legacy base_3d_noise).
type oldBlendedNoise struct {
	noise *synth.BlendedNoise
}

func (f *oldBlendedNoise) Compute(c Context) float64 { return f.noise.Compute(c.X, c.Y, c.Z) }
func (f *oldBlendedNoise) MinValue() float64         { return -f.noise.MaxValue() }
func (f *oldBlendedNoise) MaxValue() float64         { return f.noise.MaxValue() }

// ---- spline (DensityFunctions$Spline) ----

// splineFn wraps a ported Spline; Compute = spline.apply (widened to float64).
type splineFn struct {
	spline Spline
}

func (f *splineFn) Compute(c Context) float64 { return float64(f.spline.apply(c)) }
func (f *splineFn) MinValue() float64         { return float64(f.spline.minVal()) }
func (f *splineFn) MaxValue() float64         { return float64(f.spline.maxVal()) }

// ---- find_top_surface (DensityFunctions$FindTopSurface) ----

// findTopSurface is noise_router.preliminary_surface_level. It scans a column from a
// top cell (derived from upperBound) DOWN at cellHeight steps for the first cell where
// density>0, returning that Y (clamped to lowerBound). Ported from the bytecode:
//
//	top = floor(upperBound.Compute(ctx) / cellHeight) * cellHeight
//	if top <= lowerBound: return lowerBound
//	for y := top; y >= lowerBound; y -= cellHeight:
//	    if density.Compute(ctx.at(y)) > 0: return y
//	return lowerBound
//
// Consumed by the NoiseRouter AND by 09-07's surface rules (above_preliminary_surface).
type findTopSurface struct {
	density    Function
	upperBound Function
	cellHeight int
	lowerBound int
}

func (f *findTopSurface) Compute(c Context) float64 {
	top := mthFloor(f.upperBound.Compute(c)/float64(f.cellHeight)) * f.cellHeight
	if top <= f.lowerBound {
		return float64(f.lowerBound)
	}
	for y := top; y >= f.lowerBound; y -= f.cellHeight {
		if f.density.Compute(c.at(y)) > 0 {
			return float64(y)
		}
	}
	return float64(f.lowerBound)
}

// MinValue/MaxValue are the column bounds (DensityFunctions$FindTopSurface): the result
// is always within [lowerBound, upperBound.maxValue].
func (f *findTopSurface) MinValue() float64 { return float64(f.lowerBound) }
func (f *findTopSurface) MaxValue() float64 { return f.upperBound.MaxValue() }

// ---- markers: interpolated / flat_cache / cache_2d / cache_once ----

// marker is DensityFunctions$Marker. interpolated is PRESERVED (Pitfall 3 — Wave-4
// NoiseChunk samples it on the cell grid and lerps); the cache markers are
// correctness-transparent pass-throughs (real caching is a Wave-4 perf refinement).
// All forward Compute/bounds to the wrapped argument.
type marker struct {
	kind     MarkerKind
	argument Function
}

func (f *marker) Compute(c Context) float64 { return f.argument.Compute(c) }
func (f *marker) MinValue() float64         { return f.argument.MinValue() }
func (f *marker) MaxValue() float64         { return f.argument.MaxValue() }
func (f *marker) Kind() MarkerKind          { return f.kind }
func (f *marker) Wrapped() Function         { return f.argument }

var _ Marked = (*marker)(nil)

// ---- blend nodes: blend_alpha / blend_offset / blend_density ----

// Blending (cross-chunk terrain blending against old/neighbouring chunks) is OFF in
// this server (no neighbour-chunk blend), so the blend nodes pass through at runtime:
//
//	blend_alpha   = 1.0   (BlendAlpha: with blending off, alpha is fully "new chunk")
//	blend_offset  = 0.0   (BlendOffset: no offset)
//	blend_density(arg) = arg  (BlendDensity: identity with blending off)
//
// This pass-through is CORRECT ONLY with blending disabled; documented per the plan.

// blendAlpha is DensityFunctions$BlendAlpha — constant 1.0 with blending off.
type blendAlpha struct{}

func (blendAlpha) Compute(Context) float64 { return 1.0 }
func (blendAlpha) MinValue() float64       { return 1.0 }
func (blendAlpha) MaxValue() float64       { return 1.0 }

// blendOffset is DensityFunctions$BlendOffset — constant 0.0 with blending off.
type blendOffset struct{}

func (blendOffset) Compute(Context) float64 { return 0.0 }
func (blendOffset) MinValue() float64       { return 0.0 }
func (blendOffset) MaxValue() float64       { return 0.0 }

// blendDensity is DensityFunctions$BlendDensity — identity over its argument with
// blending off (BlendDensity.transform returns the input unchanged when no blend).
type blendDensity struct{ argument Function }

func (f *blendDensity) Compute(c Context) float64 { return f.argument.Compute(c) }

// BlendDensity's static bounds are the worst-case the blend could produce; Mojang
// uses the full noise range. With blending off the value equals the argument, but we
// keep the wide bounds Mojang declares so downstream short-circuits stay conservative.
func (f *blendDensity) MinValue() float64 { return math.Inf(-1) }
func (f *blendDensity) MaxValue() float64 { return math.Inf(1) }
