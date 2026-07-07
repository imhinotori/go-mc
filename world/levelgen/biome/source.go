package biome

import (
	"encoding/json"
	"fmt"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// biomeParamJSON is one entry of the embedded biome_parameters.json (Wave 1): a biome id
// plus its 6-D climate ParameterPoint. The parameter values are stored UNQUANTIZED
// (plain floats, e.g. [-1.0, 1.0]) — GenBiomeParams.java unquantized them ÷10000 when it
// extracted the baked OverworldBiomes, so the source RE-quantizes them on parse (below)
// to land back in the long box space Climate.fitness operates in (Pitfall 2).
type biomeParamJSON struct {
	Biome      string `json:"biome"`
	Parameters struct {
		Temperature     [2]float32 `json:"temperature"`
		Humidity        [2]float32 `json:"humidity"`
		Continentalness [2]float32 `json:"continentalness"`
		Erosion         [2]float32 `json:"erosion"`
		Depth           [2]float32 `json:"depth"`
		Weirdness       [2]float32 `json:"weirdness"`
		Offset          float32    `json:"offset"`
	} `json:"parameters"`
}

// span re-quantizes an unquantized [min,max] climate float pair into a Parameter (the
// long box space). This inverts GenBiomeParams.java's ÷10000 un-quantization so the
// boxes match what Climate.target produces for a sampled position.
func span(p [2]float32) Parameter {
	return Parameter{Min: quantizeCoord(p[0]), Max: quantizeCoord(p[1])}
}

// MultiNoiseBiomeSource ports net.minecraft.world.level.biome.MultiNoiseBiomeSource for
// the overworld preset: it holds the parsed Climate$ParameterList (the Wave-1 boxes) and
// the Climate$Sampler bound to the Wave-3 router climate functions. getBiome(x,y,z)
// (block coords) samples the climate at the quart cell and returns the nearest box's
// biome — REAL biome diversity, not a plains stub.
type MultiNoiseBiomeSource struct {
	params  *ParameterList
	sampler Sampler

	// endErosion, when non-nil, marks this as a TheEndBiomeSource (NOT a multi-noise
	// source): getNoiseBiome then ports net.minecraft.world.level.biome.TheEndBiomeSource
	// .getNoiseBiome (the section-distance central-island test + the erosion-thresholded
	// outer-island biomes) instead of the parameter-list climate lookup. It holds the
	// router's erosion density function (the End source samples ONLY erosion). params is
	// nil for an End source.
	endErosion density.Function
}


// NewMultiNoiseBiomeSource parses the embedded overworld biome parameter list and binds
// the climate sampler to the router's six climate functions. PURE over the router seed:
// same seed → same router → same biomes (Pitfall 7). The router field mapping mirrors
// Climate$Sampler's construction in NoiseBasedChunkGenerator (humidity←vegetation,
// continentalness←continents, weirdness←ridges).
func NewMultiNoiseBiomeSource(r *router.Router) (*MultiNoiseBiomeSource, error) {
	raw, err := data.BiomeParameters()
	if err != nil {
		return nil, fmt.Errorf("biome source: load biome parameters: %w", err)
	}
	var entries []biomeParamJSON
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("biome source: parse biome parameters: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("biome source: biome parameters empty")
	}

	boxes := make([]ParameterPoint, len(entries))
	for i, e := range entries {
		var bt levelbiome.Type
		if err := bt.UnmarshalText([]byte(e.Biome)); err != nil {
			// Surface a missing biome loudly (do NOT silently map to plains) — the
			// registry must cover every overworld biome the parameter list references.
			return nil, fmt.Errorf("biome source: unknown biome %q in parameter list: %w", e.Biome, err)
		}
		boxes[i] = ParameterPoint{
			Temperature:     span(e.Parameters.Temperature),
			Humidity:        span(e.Parameters.Humidity),
			Continentalness: span(e.Parameters.Continentalness),
			Erosion:         span(e.Parameters.Erosion),
			Depth:           span(e.Parameters.Depth),
			Weirdness:       span(e.Parameters.Weirdness),
			Offset:          quantizeCoord(e.Parameters.Offset),
			Biome:           bt,
		}
	}

	nr := r.NoiseRouter
	return &MultiNoiseBiomeSource{
		params: NewParameterList(boxes),
		sampler: Sampler{
			Temperature:     nr.Temperature,
			Humidity:        nr.Vegetation,
			Continentalness: nr.Continents,
			Erosion:         nr.Erosion,
			Depth:           nr.Depth,
			Weirdness:       nr.Ridges,
		},
	}, nil
}

// netherPresetEntry is one biome of the hardcoded NETHER multi-noise preset — its id and its
// 7-tuple Climate.parameters(temp, humidity, continentalness, erosion, depth, weirdness, offset).
// The first six are POINT values (Parameter.min == max == quantize(v)); offset is a single
// quantized coord. CITE: MultiNoiseBiomeSourceParameterList$Preset.NETHER (Climate.parameters
// tuples, verified against the 26.2 jar).
type netherPresetEntry struct {
	biome                    string
	t, h, c, e, d, w, offset float32
}

// netherPreset is the 5-biome NETHER preset, EXACT from the jar's Preset.NETHER static init:
//
//	NETHER_WASTES     (0.0,  0.0, 0,0,0,0, 0.0)
//	SOUL_SAND_VALLEY  (0.0, -0.5, 0,0,0,0, 0.0)
//	CRIMSON_FOREST    (0.4,  0.0, 0,0,0,0, 0.0)
//	WARPED_FOREST     (0.0,  0.5, 0,0,0,0, 0.375)
//	BASALT_DELTAS    (-0.5,  0.0, 0,0,0,0, 0.175)
var netherPreset = []netherPresetEntry{
	{"minecraft:nether_wastes", 0.0, 0.0, 0, 0, 0, 0, 0.0},
	{"minecraft:soul_sand_valley", 0.0, -0.5, 0, 0, 0, 0, 0.0},
	{"minecraft:crimson_forest", 0.4, 0.0, 0, 0, 0, 0, 0.0},
	{"minecraft:warped_forest", 0.0, 0.5, 0, 0, 0, 0, 0.375},
	{"minecraft:basalt_deltas", -0.5, 0.0, 0, 0, 0, 0, 0.175},
}

// point re-quantizes a single climate float into a POINT Parameter (min == max) — the form
// Climate.parameters(...) produces for each of its six non-offset axes (Climate.Parameter.point).
func point(f float32) Parameter { return Parameter{Min: quantizeCoord(f), Max: quantizeCoord(f)} }

// NewNetherBiomeSource builds the MultiNoiseBiomeSource for the NETHER preset (5 fixed biomes) and
// binds the same six climate density functions the router exposes — the nether router (nether.json)
// supplies the temperature/humidity/etc. functions. It is the dimension-parameterized sibling of
// NewMultiNoiseBiomeSource: instead of the embedded overworld parameter list it uses the jar's
// hardcoded NETHER preset. PURE over the router seed. CITE: MultiNoiseBiomeSource(Preset.NETHER)
// wired by the_nether LevelStem.
func NewNetherBiomeSource(r *router.Router) (*MultiNoiseBiomeSource, error) {
	boxes := make([]ParameterPoint, len(netherPreset))
	for i, e := range netherPreset {
		var bt levelbiome.Type
		if err := bt.UnmarshalText([]byte(e.biome)); err != nil {
			return nil, fmt.Errorf("nether biome source: unknown biome %q: %w", e.biome, err)
		}
		boxes[i] = ParameterPoint{
			Temperature:     point(e.t),
			Humidity:        point(e.h),
			Continentalness: point(e.c),
			Erosion:         point(e.e),
			Depth:           point(e.d),
			Weirdness:       point(e.w),
			Offset:          quantizeCoord(e.offset),
			Biome:           bt,
		}
	}
	nr := r.NoiseRouter
	return &MultiNoiseBiomeSource{
		params: NewParameterList(boxes),
		sampler: Sampler{
			Temperature:     nr.Temperature,
			Humidity:        nr.Vegetation,
			Continentalness: nr.Continents,
			Erosion:         nr.Erosion,
			Depth:           nr.Depth,
			Weirdness:       nr.Ridges,
		},
	}, nil
}

// endBiomeConst maps the five End biome ids to their levelbiome.Type once at init, so
// the End getNoiseBiome path is allocation-free. A bad id here is a build/asset bug.
var (
	endBiomeTheEnd          = mustEndBiome("minecraft:the_end")
	endBiomeHighlands       = mustEndBiome("minecraft:end_highlands")
	endBiomeMidlands        = mustEndBiome("minecraft:end_midlands")
	endBiomeSmallIslands    = mustEndBiome("minecraft:small_end_islands")
	endBiomeBarrens         = mustEndBiome("minecraft:end_barrens")
)

func mustEndBiome(id string) levelbiome.Type {
	var bt levelbiome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		panic("biome: end biome id " + id + ": " + err.Error())
	}
	return bt
}

// NewEndBiomeSource builds the TheEndBiomeSource for the END dimension. Unlike the
// overworld/nether multi-noise sources it is NOT parameter-list driven: it is a fixed
// geometric selector over the router's erosion density function. It binds ONLY erosion
// (the sole function TheEndBiomeSource samples). PURE over the router seed. CITE:
// net.minecraft.world.level.biome.TheEndBiomeSource (wired by the the_end LevelStem).
func NewEndBiomeSource(r *router.Router) (*MultiNoiseBiomeSource, error) {
	if r == nil || r.NoiseRouter == nil {
		return nil, fmt.Errorf("end biome source: nil router")
	}
	return &MultiNoiseBiomeSource{
		endErosion: r.NoiseRouter.Erosion,
		sampler: Sampler{
			// Only erosion is consulted; the rest are set so NewClimateCachedView copies a
			// complete sampler even though the End path never reads them.
			Temperature:     r.NoiseRouter.Temperature,
			Humidity:        r.NoiseRouter.Vegetation,
			Continentalness: r.NoiseRouter.Continents,
			Erosion:         r.NoiseRouter.Erosion,
			Depth:           r.NoiseRouter.Depth,
			Weirdness:       r.NoiseRouter.Ridges,
		},
	}, nil
}

// getEndBiome ports TheEndBiomeSource.getNoiseBiome(quartX, quartY, quartZ, sampler):
//
//	blockX = QuartPos.toBlock(quartX) = quartX<<2 (same for Y, Z)
//	secX   = SectionPos.blockToSectionCoord(blockX) = blockX>>4 (same for Z)
//	if secX*secX + secZ*secZ <= 4096L -> the_end (the central-island region)
//	else erosion = router.erosion.compute(SinglePointContext((secX*2+1)*8, blockY,
//	     (secZ*2+1)*8))
//	     erosion  > 0.25    -> end_highlands
//	     erosion >= -0.0625 -> end_midlands
//	     erosion  < -0.21875-> small_end_islands
//	     else               -> end_barrens
func (s *MultiNoiseBiomeSource) getEndBiome(quartX, quartY, quartZ int) levelbiome.Type {
	blockX := quartX << 2
	blockY := quartY << 2
	blockZ := quartZ << 2
	secX := blockX >> 4
	secZ := blockZ >> 4
	if int64(secX)*int64(secX)+int64(secZ)*int64(secZ) <= 4096 {
		return endBiomeTheEnd
	}
	sampleX := (secX*2 + 1) * 8
	sampleZ := (secZ*2 + 1) * 8
	erosion := s.endErosion.Compute(density.Context{X: sampleX, Y: blockY, Z: sampleZ})
	if erosion > 0.25 {
		return endBiomeHighlands
	}
	if erosion >= -0.0625 {
		return endBiomeMidlands
	}
	if erosion < -0.21875 {
		return endBiomeSmallIslands
	}
	return endBiomeBarrens
}

// Params exposes the parsed parameter list (tests inspect the box count / biomes).

func (s *MultiNoiseBiomeSource) Params() *ParameterList { return s.params }

// getNoiseBiome ports MultiNoiseBiomeSource.getNoiseBiome(x,y,z,Sampler) (x/y/z in QUART
// coords): sample the climate target at the quart cell, then findValue the nearest box.
// Returns plains as a never-reached fallback only for an empty list (NewMultiNoiseBiomeSource
// rejects that), so callers always get a real biome.
func (s *MultiNoiseBiomeSource) getNoiseBiome(quartX, quartY, quartZ int) levelbiome.Type {
	if s.endErosion != nil {
		return s.getEndBiome(quartX, quartY, quartZ)
	}
	t := s.sampler.sample(quartX, quartY, quartZ)
	bt, ok := s.params.findValue(t)
	if !ok {
		return 0
	}
	return bt
}

// GetBiome returns the biome at a BLOCK position (x,y,z). It converts to quart coords
// (QuartPos.fromBlock(b) = b>>2, the noise-biome resolution: one biome per 4×4×4 cell)
// and delegates to getNoiseBiome. This is the public entry the Wave-7 surface rules and
// the Wave-8 biome containers call.
func (s *MultiNoiseBiomeSource) GetBiome(x, y, z int) levelbiome.Type {
	return s.getNoiseBiome(x>>2, y>>2, z>>2)
}

// NewClimateCachedView returns a per-call biome lookup that shares this source's parameter
// list (and its lazily-built RTree) but samples the climate through a FRESH Sampler whose six
// climate density functions have their `flat_cache` markers replaced by lazy 2D memoizing
// caches (density.WrapClimateFlatCaches). Since flat_cache-marked DFs are Y-independent by
// vanilla's own declaration, the 2D memoization is BYTE-IDENTICAL to the uncached source — it
// only elides the redundant Perlin re-evaluations of the Y-flat climate DFs that the FillBiomes
// per-Y-layer sweep otherwise triggers (~99x for the 5 Y-flat DFs; `depth`'s Y-dependent
// y_clamped_gradient is not flat_cache-marked and stays uncached).
//
// The returned MultiNoiseBiomeSource is a lightweight view: it reuses s.params (immutable +
// concurrency-safe) and owns only the fresh, per-call climate caches — so it is NOT shared
// across goroutines and keeps Generate pure over (seed, pos), exactly like world.biomeCache.
// The BLOCK-position GetBiome contract is unchanged, so callers use it as a drop-in source.
func (s *MultiNoiseBiomeSource) NewClimateCachedView() *MultiNoiseBiomeSource {
	return &MultiNoiseBiomeSource{
		params:     s.params,
		endErosion: s.endErosion,
		sampler: Sampler{
			Temperature:     density.WrapClimateFlatCaches(s.sampler.Temperature),
			Humidity:        density.WrapClimateFlatCaches(s.sampler.Humidity),
			Continentalness: density.WrapClimateFlatCaches(s.sampler.Continentalness),
			Erosion:         density.WrapClimateFlatCaches(s.sampler.Erosion),
			Depth:           density.WrapClimateFlatCaches(s.sampler.Depth),
			Weirdness:       density.WrapClimateFlatCaches(s.sampler.Weirdness),
		},
	}
}
