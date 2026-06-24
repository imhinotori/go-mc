package density

import "math"

// Spline ports net.minecraft.util.CubicSpline (the Multipoint + Constant variants)
// as used by net.minecraft.world.level.levelgen.DensityFunctions$Spline. The
// overworld offset/factor/jaggedness density functions are spline-heavy; the cubic
// here is ported constant-for-constant from CubicSpline$Multipoint.sample /
// linearExtend / findIntervalStart so the terrain SHAPE matches vanilla.
//
// A spline is either:
//   - a Constant (a bare value), or
//   - a Multipoint: a coordinate (a density Function whose Compute drives the spline
//     input) + sorted control points {location, value (itself a spline), derivative}.
//
// Source: net.minecraft.util.CubicSpline$Multipoint (javap -c).
type Spline interface {
	// apply evaluates the spline at the given context (CubicSpline.apply via the
	// BoundedFloatFunction over the coordinate's Compute). Vanilla works in float32;
	// we mirror that (the values are float32 in the JSON and the cubic) to stay
	// bit-faithful to the bytecode.
	apply(c Context) float32
	// minVal / maxVal are the spline's static bounds (CubicSpline.minValue/maxValue).
	minVal() float32
	maxVal() float32
}

// constantSpline is CubicSpline$Constant: a fixed value independent of the coordinate.
type constantSpline struct{ v float32 }

func (s constantSpline) apply(Context) float32 { return s.v }
func (s constantSpline) minVal() float32       { return s.v }
func (s constantSpline) maxVal() float32       { return s.v }

// multipointSpline is CubicSpline$Multipoint: the cubic interpolation over control
// points, indexed by a coordinate Function.
type multipointSpline struct {
	coordinate  Function  // the density function driving the spline input
	locations   []float32 // sorted control-point x locations
	values      []Spline  // the value at each location (a nested spline)
	derivatives []float32 // the slope at each location
	min, max    float32
}

func (s *multipointSpline) minVal() float32 { return s.min }
func (s *multipointSpline) maxVal() float32 { return s.max }

// apply is CubicSpline$Multipoint.sample (the cubic), ported from the bytecode:
//
//	coord = coordinate.compute(c)
//	i = findIntervalStart(locations, coord)        // binarySearch(coord<loc[k]) - 1
//	last = len(locations)-1
//	if i < 0:    return linearExtend(coord, locations, values[0].sample,  derivatives, 0)
//	if i == last:return linearExtend(coord, locations, values[last].sample,derivatives, last)
//	loc0, loc1 = locations[i], locations[i+1]
//	t = (coord - loc0) / (loc1 - loc0)
//	v0, v1 = values[i].sample, values[i+1].sample
//	d0, d1 = derivatives[i], derivatives[i+1]
//	m = d0*(loc1-loc0) - (v1-v0)
//	n = -d1*(loc1-loc0) + (v1-v0)
//	return lerpF(t, v0, v1) + t*(1-t)*lerpF(t, m, n)
func (s *multipointSpline) apply(c Context) float32 {
	coord := float32(s.coordinate.Compute(c))
	i := findIntervalStart(s.locations, coord)
	last := len(s.locations) - 1
	if i < 0 {
		return linearExtend(coord, s.locations, s.values[0].apply(c), s.derivatives, 0)
	}
	if i == last {
		return linearExtend(coord, s.locations, s.values[last].apply(c), s.derivatives, last)
	}
	loc0 := s.locations[i]
	loc1 := s.locations[i+1]
	t := (coord - loc0) / (loc1 - loc0)
	v0 := s.values[i].apply(c)
	v1 := s.values[i+1].apply(c)
	d0 := s.derivatives[i]
	d1 := s.derivatives[i+1]
	m := d0*(loc1-loc0) - (v1 - v0)
	n := -d1*(loc1-loc0) + (v1 - v0)
	return lerpF(t, v0, v1) + t*(1.0-t)*lerpF(t, m, n)
}

// linearExtend ports CubicSpline$Multipoint.linearExtend:
//
//	d = derivatives[i]; return d == 0 ? value : value + d*(coord - locations[i])
func linearExtend(coord float32, locations []float32, value float32, derivatives []float32, i int) float32 {
	d := derivatives[i]
	if d == 0 {
		return value
	}
	return value + d*(coord-locations[i])
}

// findIntervalStart ports CubicSpline$Multipoint.findIntervalStart:
// binarySearch(0, len, k -> coord < locations[k]) - 1. binarySearch (Mth.binarySearch)
// returns the first index where the predicate is true (a lower-bound); minus one is
// the interval index (or -1 below the first point, last above the last point).
func findIntervalStart(locations []float32, coord float32) int {
	lo, hi := 0, len(locations)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if coord < locations[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo - 1
}

// lerpF mirrors Mth.lerp(float delta, float start, float end) = start + delta*(end-start).
func lerpF(delta, start, end float32) float32 { return start + delta*(end-start) }

// splineMinMax computes the static bounds of a multipoint spline. Vanilla derives
// these in the builder from the value-splines' bounds and the linear-extension slope
// at the ends; we conservatively take the min/max over each value-spline's bounds,
// which is a valid (never-tighter-than-vanilla) bound for the Ap2 short-circuits.
func splineMinMax(values []Spline) (float32, float32) {
	mn := float32(math.Inf(1))
	mx := float32(math.Inf(-1))
	for _, v := range values {
		if v.minVal() < mn {
			mn = v.minVal()
		}
		if v.maxVal() > mx {
			mx = v.maxVal()
		}
	}
	return mn, mx
}
