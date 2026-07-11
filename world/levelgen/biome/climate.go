// Package biome ports the Minecraft 26.2 (protocol 776) MULTI-NOISE biome source —
// the system that gives the overworld REAL biome diversity (plains/forest/desert/
// ocean/badlands/mountains vary by climate) rather than a single-biome plains stub.
//
// It is the LOGIC half of PARITY-01's biome split. The DATA half is the Wave-1
// embedded biome_parameters.json (the overworld Climate$ParameterList — 7594 6-D
// climate boxes, each a ParameterPoint + the biome it selects, extracted from the
// baked net.minecraft.data.worldgen.biome.OverworldBiomes). This package PARSES
// those boxes and ports the two ported algorithms that consume them:
//
//   - Climate (this file): the 6-D climate sampler. Climate$Sampler.sample(x,y,z)
//     reads the six Wave-3 router climate density functions (temperature, vegetation
//     [=humidity], continents [=continentalness], erosion, depth, ridges
//     [=weirdness]) at the quart-to-block position, quantizes each to a long via
//     Climate.quantizeCoord(f) = (long)(f * 10000), and packs them into a
//     Climate$TargetPoint. Climate$ParameterList.findValueBruteForce then does the
//     linear nearest-fit over the boxes (the squared 7-D fitness, first-match-wins
//     on a tie) to pick the biome.
//   - MultiNoiseBiomeSource (source.go): getBiome(x,y,z) = sample → nearest box.
//
// PORTED FROM (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.biome.Climate
//     quantizeCoord:        (long)(f * 10000.0)
//     target(6 floats):     quantize each → TargetPoint(6 longs)
//   - net.minecraft.world.level.biome.Climate$Sampler.sample(int,int,int):
//     QuartPos.toBlock(q) = q<<2; compute each DF at SinglePointContext(bx,by,bz);
//     d2f (cast double→float32); Climate.target(...)
//   - net.minecraft.world.level.biome.Climate$Parameter.distance(long):
//     l = x-max; m = min-x; if l>0 return l; if m>0 return m; return 0
//   - net.minecraft.world.level.biome.Climate$ParameterPoint.fitness(TargetPoint):
//     sum over the 6 climate dims of Mth.square(param.distance(target.coord))
//   - Mth.square(offset)   (the target's 7th "offset" coord is always 0)
//   - net.minecraft.world.level.biome.Climate$ParameterList.findValueBruteForce:
//     linear scan; keep the pair with the minimum fitness; on fitness >= best keep
//     the earlier pair (first-match-wins tiebreak → deterministic, no map order)
//
// It is a faithful algorithmic port, not a copy of Mojang source.
//
// PACKAGE LOCATION NOTE: this is package `biome` under world/levelgen/biome/. It is
// DISTINCT from level/biome (the protocol biome registry); it imports level/biome for
// the biome.Type ids the parameter list selects. The climate functions are supplied by
// world/levelgen/router (Plan 09-03) — the import direction is biome → router → density,
// no cycle.
package biome

import (
	"math"
	"sync"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen/density"
)

// quantizationFactor is Climate.QUANTIZATION_FACTOR (10000.0f). quantizeCoord scales a
// climate float into the long space the boxes are stored in so the distance math is
// exact integer arithmetic (Pitfall 2 — match vanilla's quantization bit-for-bit).
const quantizationFactor = 10000.0

// quantizeCoord ports Climate.quantizeCoord(float): (long)(f * 10000.0f). The cast is a
// Java float→long truncation toward zero; Go's int64(float32(...)) matches it. The
// intermediate multiply is done in float32 to mirror the JVM's single-precision math
// (the climate functions feed in as float32 after the sample's d2f cast).
func quantizeCoord(f float32) int64 {
	return int64(f * float32(quantizationFactor))
}

// Parameter ports Climate$Parameter: a quantized [min,max] long span on ONE climate
// dimension. A box (ParameterPoint) holds six of these plus an offset.
type Parameter struct {
	Min int64
	Max int64
}

// distance ports Climate$Parameter.distance(long): how far a quantized target coord is
// OUTSIDE the [min,max] span (0 if inside). l = x-max (above), m = min-x (below); the
// first positive of the two is the distance, else 0.
func (p Parameter) distance(x int64) int64 {
	l := x - p.Max
	m := p.Min - x
	switch {
	case l > 0:
		return l
	case m > 0:
		return m
	default:
		return 0
	}
}

// TargetPoint ports Climate$TargetPoint: the six quantized climate coords sampled at a
// position. (Vanilla's 7th "offset" coord in toParameterArray is always 0 and is folded
// into ParameterPoint.fitness as square(box.offset); it is not stored here.)
type TargetPoint struct {
	Temperature     int64
	Humidity        int64
	Continentalness int64
	Erosion         int64
	Depth           int64
	Weirdness       int64
}

// target ports Climate.target(6 floats): quantize each climate value into the
// TargetPoint long space.
func target(temperature, humidity, continentalness, erosion, depth, weirdness float32) TargetPoint {
	return TargetPoint{
		Temperature:     quantizeCoord(temperature),
		Humidity:        quantizeCoord(humidity),
		Continentalness: quantizeCoord(continentalness),
		Erosion:         quantizeCoord(erosion),
		Depth:           quantizeCoord(depth),
		Weirdness:       quantizeCoord(weirdness),
	}
}

// ParameterPoint ports Climate$ParameterPoint: a 6-D climate box (a Parameter span per
// dimension) plus a quantized offset, paired with the biome it selects. Climate stores
// the offset as a long (parameters(...) quantizes the offset float too).
type ParameterPoint struct {
	Temperature     Parameter
	Humidity        Parameter
	Continentalness Parameter
	Erosion         Parameter
	Depth           Parameter
	Weirdness       Parameter
	Offset          int64

	// Biome is the level/biome registry id this box selects (the multi-noise source's
	// findValue result). Stored alongside the box (vanilla pairs it via Pair<Point,T>).
	Biome levelbiome.Type
}

// square ports Mth.square(long): x*x. Long overflow matches the JVM (the fitness sums
// stay well within int64 for the quantized ranges).
func square(x int64) int64 { return x * x }

// fitness ports Climate$ParameterPoint.fitness(TargetPoint): the squared 7-D distance
// from this box to the target — the sum of square(distance) over the six climate
// dimensions plus square(offset). Lower is a closer match. The earlier box wins ties in
// findValue, so this is a stable, deterministic nearest-match (no map iteration order).
func (b *ParameterPoint) fitness(t TargetPoint) int64 {
	return square(b.Temperature.distance(t.Temperature)) +
		square(b.Humidity.distance(t.Humidity)) +
		square(b.Continentalness.distance(t.Continentalness)) +
		square(b.Erosion.distance(t.Erosion)) +
		square(b.Depth.distance(t.Depth)) +
		square(b.Weirdness.distance(t.Weirdness)) +
		square(b.Offset)
}

// ParameterList ports Climate$ParameterList<Holder<Biome>>: the list of climate boxes +
// the nearest-match search. Vanilla's findValue dispatches to findValueIndex, an RTree
// (Climate$RTree) over the boxes that prunes the search to O(log n)-ish instead of O(n);
// the linear findValueBruteForce is CORRECTNESS-EQUIVALENT (same nearest box, same
// first-match-wins tiebreak). findValue here uses the ported RTree (rtree.go) and is
// guarded by TestRTreeMatchesLinearScan to prove it returns the identical box as the
// brute-force scan for any target.
//
// The RTree is built lazily on the first lookup (and only once) so NewParameterList stays
// cheap and the build cost is paid by the first chunk, not every construction. tree+once
// give a data-race-free single build under the off-tick worker's concurrent Generate.
type ParameterList struct {
	boxes []ParameterPoint
	once  sync.Once
	tree  *rtreeNode
}

// NewParameterList wraps the parsed boxes. The slice order is the embedded JSON order
// (deterministic) so the first-match-wins tiebreak is stable across runs (Pitfall 7).
func NewParameterList(boxes []ParameterPoint) *ParameterList {
	return &ParameterList{boxes: boxes}
}

// Boxes returns the underlying box list (read-only; tests inspect it).
func (l *ParameterList) Boxes() []ParameterPoint { return l.boxes }

// index lazily builds (once) and returns the RTree over the boxes. Built from the box
// list order so the nearest-leaf tiebreak matches findValueBruteForce's first-match-wins.
func (l *ParameterList) index() *rtreeNode {
	l.once.Do(func() {
		l.tree = buildRTree(l.boxes)
	})
	return l.tree
}

// findValue ports Climate$ParameterList.findValue → findValueIndex(TargetPoint): an RTree
// search that returns the box with the minimum fitness, pruning subtrees whose bounding-box
// lower bound cannot beat the best-so-far. It is behavior-identical to the brute-force scan
// (findValueBruteForce / findValueLinear below): same nearest box, same earliest-wins tie.
func (l *ParameterList) findValue(t TargetPoint) (levelbiome.Type, bool) {
	if len(l.boxes) == 0 {
		return 0, false
	}
	point := toParameterArray(t)
	leaf := l.index().search(&point, nil)
	if leaf == nil {
		return 0, false
	}
	return leaf.biome, true
}

// findValueLinear ports Climate$ParameterList.findValueBruteForce(TargetPoint): a single
// linear pass keeping the box with the minimum fitness, where a later box must be STRICTLY
// closer (fitness < best) to displace an earlier one — so equal-fitness ties resolve to the
// earliest box (deterministic, matching the bytecode's `ifge` keep). Retained as the
// correctness oracle for TestRTreeMatchesLinearScan (the RTree must agree with it exactly).
func (l *ParameterList) findValueLinear(t TargetPoint) (levelbiome.Type, bool) {
	if len(l.boxes) == 0 {
		return 0, false
	}
	best := &l.boxes[0]
	bestFit := best.fitness(t)
	for i := 1; i < len(l.boxes); i++ {
		b := &l.boxes[i]
		fit := b.fitness(t)
		if fit < bestFit {
			best = b
			bestFit = fit
		}
	}
	return best.Biome, true
}

// toParameterArray ports Climate$TargetPoint.toParameterArray: the 6 quantized climate
// coords followed by a trailing 0 for the offset axis — the 7-element point the RTree's
// per-node distance is computed against.
func toParameterArray(t TargetPoint) [rtreeAxes]int64 {
	return [rtreeAxes]int64{
		t.Temperature,
		t.Humidity,
		t.Continentalness,
		t.Erosion,
		t.Depth,
		t.Weirdness,
		0,
	}
}

// Sampler ports Climate$Sampler: the six bound climate density functions. sample(x,y,z)
// (x/y/z in QUART coords) maps them to a TargetPoint exactly as the bytecode does.
type Sampler struct {
	Temperature     density.Function // router temperature
	Humidity        density.Function // router vegetation
	Continentalness density.Function // router continents
	Erosion         density.Function // router erosion
	Depth           density.Function // router depth
	Weirdness       density.Function // router ridges
}

// sample ports Climate$Sampler.sample(int,int,int): the quart coords are converted to
// block coords (QuartPos.toBlock(q) = q<<2), the six density functions are computed at
// that single point, each result is cast double→float32 (the JVM `d2f`), and
// Climate.target quantizes them into a TargetPoint.
func (s Sampler) sample(quartX, quartY, quartZ int) TargetPoint {
	bx := quartX << 2
	by := quartY << 2
	bz := quartZ << 2
	c := density.Context{X: bx, Y: by, Z: bz}
	return target(
		float32(s.Temperature.Compute(c)),
		float32(s.Humidity.Compute(c)),
		float32(s.Continentalness.Compute(c)),
		float32(s.Erosion.Compute(c)),
		float32(s.Depth.Compute(c)),
		float32(s.Weirdness.Compute(c)),
	)
}

// parameterSpan ports Climate$Parameter.span(float,float): quantizes [min,max] into the long
// span. CITE: Climate$Parameter.span(FF) (new Parameter(quantizeCoord(min), quantizeCoord(max))).
func parameterSpan(min, max float32) Parameter {
	return Parameter{Min: quantizeCoord(min), Max: quantizeCoord(max)}
}

// parameterPoint ports Climate$Parameter.point(float) == span(f,f). CITE: Climate$Parameter.point.
func parameterPoint(f float32) Parameter { return parameterSpan(f, f) }

// OverworldSpawnTarget ports OverworldBiomeBuilder.spawnTarget(): the two ParameterPoints the
// initial-spawn climate search steers toward (a temperate inland column near weirdness +/-0.16..1
// -> a mid-latitude, mid-erosion, inland-continentalness, depth-0 spawn). The exact jar values:
//
//	FULL_RANGE            = span(-1.0, 1.0)
//	inlandContinentalness = span(-0.11, 0.55)
//	both points: T=FULL, H=FULL, C=span(inland, FULL)=span(-0.11, 1.0), E=FULL, D=point(0.0), offset=0
//	point 1 weirdness = span(-1.0, -0.16);  point 2 weirdness = span(0.16, 1.0)
//
// CITE: OverworldBiomeBuilder.spawnTarget (two Climate$ParameterPoint, List.of(...)).
func OverworldSpawnTarget() []ParameterPoint {
	full := parameterSpan(-1.0, 1.0)
	inland := parameterSpan(-0.11, 0.55)
	// span(inland, FULL) = Parameter(inland.min, FULL.max).
	contin := Parameter{Min: inland.Min, Max: full.Max}
	depth0 := parameterPoint(0.0)
	return []ParameterPoint{
		{Temperature: full, Humidity: full, Continentalness: contin, Erosion: full,
			Depth: depth0, Weirdness: parameterSpan(-1.0, -0.16), Offset: 0},
		{Temperature: full, Humidity: full, Continentalness: contin, Erosion: full,
			Depth: depth0, Weirdness: parameterSpan(0.16, 1.0), Offset: 0},
	}
}

// SpawnPos is the (x,z) block result of the climate spawn search (Y is resolved later from the
// world's WORLD_SURFACE height at that chunk). Fitness is the best squared climate distance.
type SpawnPos struct {
	X, Z    int
	Fitness int64
}

// spawnFitnessTarget ports Climate.SpawnFinder.getSpawnPositionAndFitness's TargetPoint build:
// sample the climate at (fromBlock(x), 0, fromBlock(z)) then force DEPTH to 0 (a surface column)
// before the fitness compare. CITE: getSpawnPositionAndFitness (new TargetPoint(temp, humidity,
// continentalness, erosion, 0L, weirdness)).
func (s Sampler) spawnFitnessAt(spawnTarget []ParameterPoint, blockX, blockZ int) int64 {
	// QuartPos.fromBlock(b) == b >> 2 (arithmetic shift). sample takes QUART coords.
	tp := s.sample(blockX>>2, 0, blockZ>>2)
	tp.Depth = 0 // getSpawnPositionAndFitness zeroes depth
	best := int64(9223372036854775807)
	for i := range spawnTarget {
		f := spawnTarget[i].fitness(tp)
		if f < best {
			best = f
		}
	}
	return best
}

// radialSearch ports Climate$SpawnFinder.radialSearch: an Archimedean spiral outward from the
// current best (angle step increment / radius, radius stepping by increment up to maxRadius),
// keeping the lowest-fitness candidate. CITE: Climate$SpawnFinder.radialSearch.
func (s Sampler) radialSearch(spawnTarget []ParameterPoint, best *SpawnPos, increment, maxRadius float32) {
	angle := float32(0.0)
	radius := increment
	for radius <= maxRadius {
		x := best.X + int(math.Sin(float64(angle))*float64(radius))
		z := best.Z + int(math.Cos(float64(angle))*float64(radius))
		fit := s.spawnFitnessAt(spawnTarget, x, z)
		if fit < best.Fitness {
			best.X = x
			best.Z = z
			best.Fitness = fit
		}
		angle += increment / radius
		if float64(angle) > 6.283185307179586 { // 2*pi
			angle = 0
			radius += increment
		}
	}
}

// FindSpawnPosition ports Climate$Sampler.findSpawnPosition + Climate.findSpawnPosition +
// SpawnFinder: seed the result at (0,0), then two radial sweeps (2048 step 512, then 512 step
// 32) keeping the min-fitness column. Returns (0,0) when spawnTarget is empty (vanilla returns
// BlockPos.ZERO). CITE: Climate$Sampler.findSpawnPosition; Climate$SpawnFinder.<init> (radial
// 2048/512 then 512/32).
func (s Sampler) FindSpawnPosition(spawnTarget []ParameterPoint) SpawnPos {
	if len(spawnTarget) == 0 {
		return SpawnPos{X: 0, Z: 0, Fitness: 0}
	}
	best := SpawnPos{X: 0, Z: 0, Fitness: s.spawnFitnessAt(spawnTarget, 0, 0)}
	// Vanilla: radialSearch(f3=2048 maxRadius, f4=512 increment), then (512, 32).
	s.radialSearch(spawnTarget, &best, 512.0, 2048.0)
	s.radialSearch(spawnTarget, &best, 32.0, 512.0)
	return best
}
