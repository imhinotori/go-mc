package density

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// NoiseBinder resolves a noise registry id to its seeded primitives. RandomState
// (Wave 2, router.go) implements this — it seeds each NormalNoise via the positional
// factory's FromHashOf(id) (Pitfall 1, the determinism hinge). The parser stays free
// of the seeding layer: it asks the binder for the noise as it builds noise/
// shifted_noise/shift_a/shift_b nodes, and for the legacy BlendedNoise for
// old_blended_noise. A nil binder is rejected when a noise node is encountered.
type NoiseBinder interface {
	// NormalNoise returns the seeded NormalNoise for a noise registry id
	// (e.g. "minecraft:temperature").
	NormalNoise(id string) (*synth.NormalNoise, error)
	// BlendedNoise returns the seeded legacy BlendedNoise for old_blended_noise with
	// the given base_3d_noise scales.
	BlendedNoise(xzScale, yScale, xzFactor, yFactor, smearScaleMultiplier float64) (*synth.BlendedNoise, error)
}

// DataSource loads the raw JSON bytes for a density-function registry ref. The Wave-1
// data package implements this (data.DensityFunction); tests may supply an in-memory
// map. It is how a string ref ("minecraft:overworld/offset") resolves to another
// density_function file the parser then parses recursively.
type DataSource interface {
	DensityFunction(id string) ([]byte, error)
}

// DataSourceFunc adapts a plain func (e.g. data.DensityFunction from Wave 1) to the
// DataSource interface, so the router (Wave 2) and tests can wire the embedded graph
// without a wrapper type.
type DataSourceFunc func(id string) ([]byte, error)

// DensityFunction implements DataSource.
func (f DataSourceFunc) DensityFunction(id string) ([]byte, error) { return f(id) }

// Registry caches parsed density functions by id — the HolderHolder dedup: a ref
// shared by multiple parents (the spline-heavy offset/factor/jaggedness, the cache
// wrappers) is parsed ONCE and the same *Function instance is returned, so the graph
// is a DAG, not a re-parsed tree. It also detects cycles (a malformed graph would
// otherwise recurse forever — T-9-05, accepted but guarded here cheaply).
type Registry struct {
	data    DataSource
	binder  NoiseBinder
	cache   map[string]Function
	parsing map[string]bool // ids currently being parsed (cycle guard)
}

// NewRegistry creates a parser registry over a data source + a noise binder.
func NewRegistry(data DataSource, binder NoiseBinder) *Registry {
	return &Registry{
		data:    data,
		binder:  binder,
		cache:   make(map[string]Function),
		parsing: make(map[string]bool),
	}
}

// Resolve parses (or returns the cached) density function for a registry ref id. It
// lazily loads the file via the DataSource, parses it, and caches the result so a
// shared sub-graph is built once (dedup).
func (r *Registry) Resolve(id string) (Function, error) {
	if fn, ok := r.cache[id]; ok {
		return fn, nil
	}
	if r.parsing[id] {
		return nil, fmt.Errorf("density: cyclic density-function reference %q", id)
	}
	if r.data == nil {
		return nil, fmt.Errorf("density: cannot resolve ref %q: no data source", id)
	}
	raw, err := r.data.DensityFunction(id)
	if err != nil {
		return nil, fmt.Errorf("density: loading ref %q: %w", id, err)
	}
	r.parsing[id] = true
	fn, err := r.Parse(raw)
	delete(r.parsing, id)
	if err != nil {
		return nil, fmt.Errorf("density: parsing ref %q: %w", id, err)
	}
	r.cache[id] = fn
	return fn, nil
}

// Parse builds a Function from a raw JSON message. The three argument shapes the
// vanilla graph uses:
//   - a bare JSON number -> constant
//   - a JSON string -> a ref to another density_function (resolved + cached via the
//     DataSource)
//   - a JSON object with a "type" field -> a node (dispatched below, recursively
//     parsing its arguments)
//
// An unsupported "type" (only end_islands for the overworld) ERRORS LOUDLY naming it
// — never a silent mis-evaluation (T-9-07).
func (r *Registry) Parse(raw json.RawMessage) (Function, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("density: empty density-function value")
	}
	switch trimmed[0] {
	case '"':
		var ref string
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, fmt.Errorf("density: bad string ref: %w", err)
		}
		return r.Resolve(ref)
	case '{':
		return r.parseObject(raw)
	default:
		// A bare number -> constant.
		var num float64
		if err := json.Unmarshal(raw, &num); err != nil {
			return nil, fmt.Errorf("density: expected number/string/object, got %q: %w", truncate(trimmed), err)
		}
		return constantFn{num}, nil
	}
}

// objNode is the common envelope: every node object carries a "type"; the rest of the
// fields are type-specific and decoded lazily as json.RawMessage.
type objNode struct {
	Type string `json:"type"`
}

// stripNS strips the "minecraft:" namespace from a type so dispatch is namespace-agnostic.
func stripNS(t string) string {
	if i := strings.IndexByte(t, ':'); i >= 0 {
		return t[i+1:]
	}
	return t
}

func (r *Registry) parseObject(raw json.RawMessage) (Function, error) {
	var head objNode
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("density: bad node object: %w", err)
	}
	if head.Type == "" {
		return nil, fmt.Errorf("density: node object missing \"type\": %s", truncate(string(raw)))
	}
	// Decode the full object as a field map for type-specific extraction.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("density: bad node fields: %w", err)
	}

	// arg parses a named child argument (object/string/number).
	arg := func(name string) (Function, error) {
		v, ok := m[name]
		if !ok {
			return nil, fmt.Errorf("density: %s missing argument %q", head.Type, name)
		}
		return r.Parse(v)
	}
	// num parses a named numeric field.
	num := func(name string) (float64, error) {
		v, ok := m[name]
		if !ok {
			return 0, fmt.Errorf("density: %s missing field %q", head.Type, name)
		}
		var f float64
		if err := json.Unmarshal(v, &f); err != nil {
			return 0, fmt.Errorf("density: %s field %q: %w", head.Type, name, err)
		}
		return f, nil
	}
	intf := func(name string) (int, error) {
		f, err := num(name)
		return int(f), err
	}

	switch stripNS(head.Type) {

	// --- unary Mapped transforms ---
	case "abs":
		return mappedNode(mapAbs, arg)
	case "square":
		return mappedNode(mapSquare, arg)
	case "cube":
		return mappedNode(mapCube, arg)
	case "half_negative":
		return mappedNode(mapHalfNegative, arg)
	case "quarter_negative":
		return mappedNode(mapQuarterNegative, arg)
	case "invert":
		return mappedNode(mapInvert, arg)
	case "squeeze":
		return mappedNode(mapSqueeze, arg)

	// --- arithmetic ---
	case "add":
		return ap2Node(ap2Add, m, r)
	case "mul":
		return ap2Node(ap2Mul, m, r)
	case "min":
		return ap2Node(ap2Min, m, r)
	case "max":
		return ap2Node(ap2Max, m, r)

	// --- clamp ---
	case "clamp":
		in, err := arg("input")
		if err != nil {
			return nil, err
		}
		mn, err := num("min")
		if err != nil {
			return nil, err
		}
		mx, err := num("max")
		if err != nil {
			return nil, err
		}
		return &clampFn{input: in, min: mn, max: mx}, nil

	// --- y_clamped_gradient ---
	case "y_clamped_gradient":
		fromY, err := num("from_y")
		if err != nil {
			return nil, err
		}
		toY, err := num("to_y")
		if err != nil {
			return nil, err
		}
		fromV, err := num("from_value")
		if err != nil {
			return nil, err
		}
		toV, err := num("to_value")
		if err != nil {
			return nil, err
		}
		return yClampedGradient{fromY: fromY, toY: toY, fromValue: fromV, toValue: toV}, nil

	// --- range_choice ---
	case "range_choice":
		in, err := arg("input")
		if err != nil {
			return nil, err
		}
		mnInc, err := num("min_inclusive")
		if err != nil {
			return nil, err
		}
		mxExc, err := num("max_exclusive")
		if err != nil {
			return nil, err
		}
		inR, err := arg("when_in_range")
		if err != nil {
			return nil, err
		}
		outR, err := arg("when_out_of_range")
		if err != nil {
			return nil, err
		}
		return &rangeChoice{input: in, minInclusive: mnInc, maxExclusive: mxExc, whenInRange: inR, whenOutOfRange: outR}, nil

	// --- interval_select ---
	case "interval_select":
		in, err := arg("input")
		if err != nil {
			return nil, err
		}
		var ths []float64
		if err := json.Unmarshal(m["thresholds"], &ths); err != nil {
			return nil, fmt.Errorf("density: interval_select thresholds: %w", err)
		}
		var fnsRaw []json.RawMessage
		if err := json.Unmarshal(m["functions"], &fnsRaw); err != nil {
			return nil, fmt.Errorf("density: interval_select functions: %w", err)
		}
		if len(fnsRaw) != len(ths)+1 {
			return nil, fmt.Errorf("density: interval_select expects len(functions)==len(thresholds)+1, got %d functions / %d thresholds", len(fnsRaw), len(ths))
		}
		fns := make([]Function, len(fnsRaw))
		for i, fr := range fnsRaw {
			fn, err := r.Parse(fr)
			if err != nil {
				return nil, fmt.Errorf("density: interval_select function[%d]: %w", i, err)
			}
			fns[i] = fn
		}
		return newIntervalSelect(in, ths, fns), nil

	// --- noise / shifted_noise ---
	case "noise":
		if r.binder == nil {
			return nil, fmt.Errorf("density: noise node needs a NoiseBinder")
		}
		id, err := strField(m, "noise")
		if err != nil {
			return nil, err
		}
		xz, _ := num("xz_scale")
		y, _ := num("y_scale")
		nn, err := r.binder.NormalNoise(id)
		if err != nil {
			return nil, fmt.Errorf("density: noise %q: %w", id, err)
		}
		return &noiseFn{noise: nn, xzScale: xz, yScale: y, noiseID: id}, nil

	case "shifted_noise":
		if r.binder == nil {
			return nil, fmt.Errorf("density: shifted_noise node needs a NoiseBinder")
		}
		id, err := strField(m, "noise")
		if err != nil {
			return nil, err
		}
		sx, err := arg("shift_x")
		if err != nil {
			return nil, err
		}
		sy, err := arg("shift_y")
		if err != nil {
			return nil, err
		}
		sz, err := arg("shift_z")
		if err != nil {
			return nil, err
		}
		xz, _ := num("xz_scale")
		y, _ := num("y_scale")
		nn, err := r.binder.NormalNoise(id)
		if err != nil {
			return nil, fmt.Errorf("density: shifted_noise %q: %w", id, err)
		}
		return &shiftedNoise{noise: nn, shiftX: sx, shiftY: sy, shiftZ: sz, xzScale: xz, yScale: y, noiseID: id}, nil

	// --- shift_a / shift_b ---
	case "shift_a":
		return r.shiftNode(shiftA, m)
	case "shift_b":
		return r.shiftNode(shiftB, m)

	// --- old_blended_noise ---
	case "old_blended_noise":
		if r.binder == nil {
			return nil, fmt.Errorf("density: old_blended_noise node needs a NoiseBinder")
		}
		xz, _ := num("xz_scale")
		y, _ := num("y_scale")
		xzf, _ := num("xz_factor")
		yf, _ := num("y_factor")
		smear, _ := num("smear_scale_multiplier")
		bn, err := r.binder.BlendedNoise(xz, y, xzf, yf, smear)
		if err != nil {
			return nil, fmt.Errorf("density: old_blended_noise: %w", err)
		}
		return &oldBlendedNoise{noise: bn}, nil

	// --- spline ---
	case "spline":
		sp, err := r.parseSpline(m["spline"])
		if err != nil {
			return nil, fmt.Errorf("density: spline: %w", err)
		}
		return &splineFn{spline: sp}, nil

	// --- find_top_surface ---
	case "find_top_surface":
		dens, err := arg("density")
		if err != nil {
			return nil, err
		}
		ub, err := arg("upper_bound")
		if err != nil {
			return nil, err
		}
		ch, err := intf("cell_height")
		if err != nil {
			return nil, err
		}
		lb, err := intf("lower_bound")
		if err != nil {
			return nil, err
		}
		return &findTopSurface{density: dens, upperBound: ub, cellHeight: ch, lowerBound: lb}, nil

	// --- markers (interpolated preserved; cache markers transparent) ---
	case "interpolated":
		return markerNode(MarkerInterpolated, arg)
	case "flat_cache":
		return markerNode(MarkerFlatCache, arg)
	case "cache_2d":
		return markerNode(MarkerCache2D, arg)
	case "cache_once":
		return markerNode(MarkerCacheOnce, arg)

	// --- blend nodes (blending off) ---
	case "blend_alpha":
		return blendAlpha{}, nil
	case "blend_offset":
		return blendOffset{}, nil
	case "blend_density":
		a, err := arg("argument")
		if err != nil {
			return nil, err
		}
		return &blendDensity{argument: a}, nil

	default:
		// Unsupported type — error loudly naming it (T-9-07). end_islands lands here.
		return nil, fmt.Errorf("density: unsupported density node type %q (only the overworld node set is ported; end_islands and others are deferred)", head.Type)
	}
}

// mappedNode builds a unary Mapped node from its "argument".
func mappedNode(t mappedType, arg func(string) (Function, error)) (Function, error) {
	in, err := arg("argument")
	if err != nil {
		return nil, err
	}
	return newMapped(t, in), nil
}

// markerNode builds a Marker node from its "argument".
func markerNode(kind MarkerKind, arg func(string) (Function, error)) (Function, error) {
	in, err := arg("argument")
	if err != nil {
		return nil, err
	}
	return &marker{kind: kind, argument: in}, nil
}

// ap2Node builds an Ap2 node from "argument1"/"argument2".
func ap2Node(t ap2Type, m map[string]json.RawMessage, r *Registry) (Function, error) {
	a1raw, ok := m["argument1"]
	if !ok {
		return nil, fmt.Errorf("density: ap2 missing argument1")
	}
	a2raw, ok := m["argument2"]
	if !ok {
		return nil, fmt.Errorf("density: ap2 missing argument2")
	}
	a1, err := r.Parse(a1raw)
	if err != nil {
		return nil, fmt.Errorf("density: ap2 argument1: %w", err)
	}
	a2, err := r.Parse(a2raw)
	if err != nil {
		return nil, fmt.Errorf("density: ap2 argument2: %w", err)
	}
	return newAp2(t, a1, a2), nil
}

// shiftNode builds a shift_a/shift_b node: its "argument" is the offset noise id.
func (r *Registry) shiftNode(t shiftType, m map[string]json.RawMessage) (Function, error) {
	if r.binder == nil {
		return nil, fmt.Errorf("density: shift node needs a NoiseBinder")
	}
	id, err := strField(m, "argument")
	if err != nil {
		return nil, err
	}
	nn, err := r.binder.NormalNoise(id)
	if err != nil {
		return nil, fmt.Errorf("density: shift noise %q: %w", id, err)
	}
	return &shift{typ: t, noise: nn, noiseID: id}, nil
}

// parseSpline parses a CubicSpline JSON value: either a bare number (Constant) or an
// object {coordinate: <density-fn ref>, points: [{location, value, derivative}]}.
// value is itself a spline (recursive). The coordinate ref is parsed as a density
// Function (it drives the spline input).
func (r *Registry) parseSpline(raw json.RawMessage) (Spline, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing spline value")
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed[0] != '{' {
		// Constant spline (a bare number).
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("spline constant: %w", err)
		}
		return constantSpline{float32(v)}, nil
	}
	var obj struct {
		Coordinate json.RawMessage `json:"coordinate"`
		Points     []struct {
			Location   float64         `json:"location"`
			Value      json.RawMessage `json:"value"`
			Derivative float64         `json:"derivative"`
		} `json:"points"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("spline object: %w", err)
	}
	coord, err := r.Parse(obj.Coordinate)
	if err != nil {
		return nil, fmt.Errorf("spline coordinate: %w", err)
	}
	sp := &multipointSpline{coordinate: coord}
	for i, p := range obj.Points {
		val, err := r.parseSpline(p.Value)
		if err != nil {
			return nil, fmt.Errorf("spline point[%d] value: %w", i, err)
		}
		sp.locations = append(sp.locations, float32(p.Location))
		sp.values = append(sp.values, val)
		sp.derivatives = append(sp.derivatives, float32(p.Derivative))
	}
	sp.min, sp.max = splineMinMax(sp.values)
	return sp, nil
}

// strField decodes a named JSON string field.
func strField(m map[string]json.RawMessage, name string) (string, error) {
	v, ok := m[name]
	if !ok {
		return "", fmt.Errorf("density: missing string field %q", name)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", fmt.Errorf("density: field %q not a string: %w", name, err)
	}
	return s, nil
}

// truncate shortens a JSON snippet for error messages.
func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
