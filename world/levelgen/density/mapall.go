package density

// MapAll ports net.minecraft.world.level.levelgen.DensityFunction.mapAll(Visitor): it
// rebuilds the graph bottom-up, mapping every child via MapAll first, then applying the
// visitor to the rebuilt node. This is the hinge the NoiseChunk uses to replace each
// `interpolated`-marked subtree with its OWN trilerping interpolator while leaving every
// surrounding op (squeeze/min/the noodle cave graph) to evaluate PER-BLOCK — vanilla's
// NoiseChunk constructor calls noiseRouter.mapAll(this::wrap) for exactly this.
//
// Identity memoization (the `seen` map) mirrors vanilla's NoiseChunk.wrapped Map: the
// parser (parse.go Registry) dedups shared refs to a single *Function instance, so a
// subtree reachable from two parents must map ONCE — otherwise a shared `interpolated`
// node would get two independent interpolators (vanilla keeps one). The visitor is
// applied at most once per distinct node.
//
// It is a faithful algorithmic port, not a copy of Mojang source.
func MapAll(root Function, visit func(Function) Function) Function {
	return mapAll(root, visit, make(map[Function]Function))
}

// mapAll recurses with an identity cache. For each node it maps the children (recursively),
// rebuilds the node with the mapped children, then applies visit to the rebuilt node.
func mapAll(fn Function, visit func(Function) Function, seen map[Function]Function) Function {
	if fn == nil {
		return nil
	}
	if m, ok := seen[fn]; ok {
		return m
	}
	rebuilt := mapChildren(fn, visit, seen)
	out := visit(rebuilt)
	seen[fn] = out
	return out
}

// mapChildren returns a copy of fn whose children have each been MapAll'd. Leaf nodes
// (constant/noise/shift/spline-leaves/blend/yClampedGradient) have no density-function
// children to map and return themselves unchanged. Mirrors each DensityFunctions$*.
// mapAll's `withChildren` lambda from the bytecode.
func mapChildren(fn Function, visit func(Function) Function, seen map[Function]Function) Function {
	rec := func(child Function) Function { return mapAll(child, visit, seen) }

	switch n := fn.(type) {
	case *ap2:
		// Rebuild via newAp2 so the bounds are recomputed from the mapped children
		// (TwoArgumentSimpleFunction.create), matching vanilla's Ap2.withChildren.
		return newAp2(n.typ, rec(n.a1), rec(n.a2))
	case *mapped:
		return newMapped(n.typ, rec(n.input))
	case *clampFn:
		return &clampFn{input: rec(n.input), min: n.min, max: n.max}
	case *rangeChoice:
		return &rangeChoice{
			input:          rec(n.input),
			minInclusive:   n.minInclusive,
			maxExclusive:   n.maxExclusive,
			whenInRange:    rec(n.whenInRange),
			whenOutOfRange: rec(n.whenOutOfRange),
		}
	case *intervalSelect:
		fns := make([]Function, len(n.functions))
		for i, f := range n.functions {
			fns[i] = rec(f)
		}
		return newIntervalSelect(rec(n.input), n.thresholds, fns)
	case *shiftedNoise:
		return &shiftedNoise{
			noise:   n.noise,
			shiftX:  rec(n.shiftX),
			shiftY:  rec(n.shiftY),
			shiftZ:  rec(n.shiftZ),
			xzScale: n.xzScale,
			yScale:  n.yScale,
			noiseID: n.noiseID,
		}
	case *findTopSurface:
		return &findTopSurface{
			density:    rec(n.density),
			upperBound: rec(n.upperBound),
			cellHeight: n.cellHeight,
			lowerBound: n.lowerBound,
		}
	case *marker:
		return &marker{kind: n.kind, argument: rec(n.argument)}
	case *blendDensity:
		return &blendDensity{argument: rec(n.argument)}
	case *splineFn:
		return &splineFn{spline: mapSpline(n.spline, rec)}
	default:
		// Leaf nodes with no density-function children: constantFn, yClampedGradient,
		// noiseFn, shift, oldBlendedNoise, blendAlpha, blendOffset. Unchanged.
		return fn
	}
}

// mapSpline maps a spline's coordinate density function (and recurses into nested value
// splines), mirroring CubicSpline.mapAll: a Multipoint spline maps its coordinate and
// each point's nested spline; a Constant spline has nothing to map. In the overworld
// graph spline coordinates are flat_cache(shifted_noise) (not interpolated), so this is
// correctness-preserving — but it keeps the rewrite total so no marker can hide here.
func mapSpline(sp Spline, rec func(Function) Function) Spline {
	mp, ok := sp.(*multipointSpline)
	if !ok {
		return sp // constantSpline: no coordinate, no children.
	}
	values := make([]Spline, len(mp.values))
	for i, v := range mp.values {
		values[i] = mapSpline(v, rec)
	}
	return &multipointSpline{
		coordinate:  rec(mp.coordinate),
		locations:   mp.locations,
		values:      values,
		derivatives: mp.derivatives,
		min:         mp.min,
		max:         mp.max,
	}
}
