package biome

import (
	"encoding/json"
	"fmt"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen/data"
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

// Params exposes the parsed parameter list (tests inspect the box count / biomes).
func (s *MultiNoiseBiomeSource) Params() *ParameterList { return s.params }

// getNoiseBiome ports MultiNoiseBiomeSource.getNoiseBiome(x,y,z,Sampler) (x/y/z in QUART
// coords): sample the climate target at the quart cell, then findValue the nearest box.
// Returns plains as a never-reached fallback only for an empty list (NewMultiNoiseBiomeSource
// rejects that), so callers always get a real biome.
func (s *MultiNoiseBiomeSource) getNoiseBiome(quartX, quartY, quartZ int) levelbiome.Type {
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
