package surface

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// This file ports the Minecraft 26.2 (protocol 776) biome temperature model — the
// half of net.minecraft.world.level.biome.Biome that answers "is it cold enough to
// snow here?" — so the surface_rule `temperature` condition
// (SurfaceRules$Context$TemperatureHelperCondition) generates the SNOW / ICE /
// POWDER_SNOW / packed-ice / FROZEN-OCEAN surface layers in cold biomes.
//
// Ported from the bytecode (CFR, temp/cache/26.2-inner.jar), method-for-method:
//
//	Biome.coldEnoughToSnow(pos, seaLevel)          = !warmEnoughToRain(pos, seaLevel)
//	Biome.warmEnoughToRain(pos, seaLevel)          = getTemperature(pos, seaLevel) >= 0.15f
//	Biome.getTemperature(pos, seaLevel)            = getHeightAdjustedTemperature(pos, seaLevel)
//	     (the ThreadLocal Long2FloatLinkedOpenHashMap CACHE in getTemperature is a pure
//	      perf memo; porting the uncached getHeightAdjustedTemperature directly is
//	      result-identical, so the cache is intentionally omitted.)
//	Biome.getHeightAdjustedTemperature(pos, seaLevel):
//	     float adjusted = climateSettings.temperatureModifier.modifyTemperature(pos, getBaseTemperature());
//	     int   snowLevel = seaLevel + 17;
//	     if (pos.getY() > snowLevel) {
//	         float v = (float)(TEMPERATURE_NOISE.getValue((float)pos.getX()/8.0f, (float)pos.getZ()/8.0f, false) * 8.0);
//	         return adjusted - (v + (float)pos.getY() - (float)snowLevel) * 0.05f / 40.0f;
//	     }
//	     return adjusted;
//	Biome.getBaseTemperature()                     = climateSettings.temperature  (per-biome JSON float)
//	Biome$TemperatureModifier.NONE.modifyTemperature(pos, base)   = base
//	Biome$TemperatureModifier.FROZEN.modifyTemperature(pos, base):
//	     double large = FROZEN_TEMPERATURE_NOISE.getValue(x*0.05, z*0.05, false) * 7.0;
//	     double edge  = BIOME_INFO_NOISE.getValue(x*0.2,  z*0.2,  false);
//	     double icePatches = large + edge;
//	     if (icePatches < 0.3) {
//	         double small = BIOME_INFO_NOISE.getValue(x*0.09, z*0.09, false);
//	         if (small < 0.8) return 0.2f;
//	     }
//	     return base;
//
// The three static PerlinSimplexNoise instances (Biome static fields, CFR-verified),
// seeded from the legacy LCG through a WorldgenRandom:
//
//	TEMPERATURE_NOISE        = new PerlinSimplexNoise(new WorldgenRandom(new LegacyRandomSource(1234L)), of(0));
//	FROZEN_TEMPERATURE_NOISE = new PerlinSimplexNoise(new WorldgenRandom(new LegacyRandomSource(3456L)), of(-2,-1,0));
//	BIOME_INFO_NOISE         = new PerlinSimplexNoise(new WorldgenRandom(new LegacyRandomSource(2345L)), of(0));
//
// These are WORLD-SEED-INDEPENDENT (fixed legacy seeds 1234/3456/2345 — the same in
// every world), so they are built ONCE (lazily, sync.Once) into package globals, and
// the per-biome temperature/modifier table is loaded ONCE from the embedded biome JSON.
//
// A faithful algorithmic port, NOT a copy of Mojang source. The EXACT float32/float64
// cast chain is preserved (see coldEnoughToSnow below).

// biomeTemperatureModel holds the three static Biome noises + the per-biome climate
// table. Built once via ensureTemperatureModel.
type biomeTemperatureModel struct {
	temperatureNoise *synth.PerlinSimplexNoise // TEMPERATURE_NOISE (seed 1234, octaves {0})
	frozenNoise      *synth.PerlinSimplexNoise // FROZEN_TEMPERATURE_NOISE (seed 3456, octaves {-2,-1,0})
	biomeInfoNoise   *synth.PerlinSimplexNoise // BIOME_INFO_NOISE (seed 2345, octaves {0})

	// per-biome climate, keyed by biome.Type. temperature is the JSON float; frozen is
	// (temperature_modifier == "frozen").
	temperature map[biome.Type]float32
	frozen      map[biome.Type]bool
}

var (
	temperatureModel     *biomeTemperatureModel
	temperatureModelOnce sync.Once
	temperatureModelErr  error
)

// ensureTemperatureModel builds the shared biome temperature model exactly once. The
// three noises are seeded from the fixed legacy seeds (world-seed-independent — the jar
// constructs them as static final fields); the per-biome table is loaded from the
// embedded biome JSON. On error it records temperatureModelErr; callers surface it.
func ensureTemperatureModel() (*biomeTemperatureModel, error) {
	temperatureModelOnce.Do(func() {
		m := &biomeTemperatureModel{
			temperature: map[biome.Type]float32{},
			frozen:      map[biome.Type]bool{},
		}
		// TEMPERATURE_NOISE = new PerlinSimplexNoise(new WorldgenRandom(new LegacyRandomSource(1234L)), of(0))
		m.temperatureNoise = synth.NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(1234), []int{0})
		// FROZEN_TEMPERATURE_NOISE = ... LegacyRandomSource(3456L), of(-2,-1,0)
		m.frozenNoise = synth.NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(3456), []int{-2, -1, 0})
		// BIOME_INFO_NOISE = ... LegacyRandomSource(2345L), of(0)
		m.biomeInfoNoise = synth.NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(2345), []int{0})

		if err := loadBiomeClimate(m); err != nil {
			temperatureModelErr = err
			return
		}
		temperatureModel = m
	})
	return temperatureModel, temperatureModelErr
}

// biomeClimateJSON is the subset of a data/biome/<name>.json we read: the top-level
// float `temperature` and the optional string `temperature_modifier` ("none"/"frozen",
// absent = "none").
type biomeClimateJSON struct {
	Temperature         float32 `json:"temperature"`
	TemperatureModifier string  `json:"temperature_modifier"`
}

// loadBiomeClimate fills the per-biome temperature/frozen tables from the embedded
// biome JSON. Every id the biome registry knows is resolved to a biome.Type and its
// climate recorded. A biome whose JSON cannot be parsed errors loudly.
func loadBiomeClimate(m *biomeTemperatureModel) error {
	ids, err := data.BiomeIDs()
	if err != nil {
		return fmt.Errorf("surface: biome temperature: list biomes: %w", err)
	}
	for _, id := range ids {
		raw, err := data.BiomeJSON(id)
		if err != nil {
			return fmt.Errorf("surface: biome temperature: read biome %q: %w", id, err)
		}
		var c biomeClimateJSON
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("surface: biome temperature: decode biome %q: %w", id, err)
		}
		var bt biome.Type
		// The biome registry ids carry the minecraft: namespace; the embedded file
		// ids are bare, so re-add it for the UnmarshalText lookup.
		if err := bt.UnmarshalText([]byte("minecraft:" + id)); err != nil {
			// A biome present in the embed but not in the (generated) registry list is
			// skipped rather than fatal — it can never be produced by the biome source,
			// so it can never key a temperature test.
			continue
		}
		m.temperature[bt] = c.Temperature
		m.frozen[bt] = c.TemperatureModifier == "frozen"
	}
	return nil
}

// modifyTemperature ports Biome$TemperatureModifier.modifyTemperature for the biome:
// NONE returns base unchanged; FROZEN applies the ice-patch carveout. All noise math is
// double (float64); the return is float32 (0.2f or base).
func (m *biomeTemperatureModel) modifyTemperature(bt biome.Type, x, z int, base float32) float32 {
	if !m.frozen[bt] {
		return base // NONE.modifyTemperature(pos, base) = base
	}
	// FROZEN.modifyTemperature:
	large := m.frozenNoise.GetValue(float64(x)*0.05, float64(z)*0.05, false) * 7.0
	edge := m.biomeInfoNoise.GetValue(float64(x)*0.2, float64(z)*0.2, false)
	icePatches := large + edge
	if icePatches < 0.3 {
		small := m.biomeInfoNoise.GetValue(float64(x)*0.09, float64(z)*0.09, false)
		if small < 0.8 {
			return 0.2
		}
	}
	return base
}

// getBaseTemperature ports Biome.getBaseTemperature() = climateSettings.temperature.
// An unknown biome (never produced by the source) defaults to 0 — the same as the
// vanilla ClimateSettings default and safe because it can never be tested.
func (m *biomeTemperatureModel) getBaseTemperature(bt biome.Type) float32 {
	return m.temperature[bt]
}

// getHeightAdjustedTemperature ports Biome.getHeightAdjustedTemperature(pos, seaLevel).
// The float32/float64 cast chain is preserved EXACTLY:
//
//	float adjusted = modifier.modifyTemperature(pos, base);       // float32
//	int snowLevel = seaLevel + 17;
//	if (y > snowLevel) {
//	   float v = (float)(TEMPERATURE_NOISE.getValue((float)x/8.0f, (float)z/8.0f, false) * 8.0);
//	   return adjusted - (v + (float)y - (float)snowLevel) * 0.05f / 40.0f;
//	}
//	return adjusted;
func (m *biomeTemperatureModel) getHeightAdjustedTemperature(bt biome.Type, x, y, z, seaLevel int) float32 {
	adjusted := m.modifyTemperature(bt, x, z, m.getBaseTemperature(bt))
	snowLevel := seaLevel + 17
	if y > snowLevel {
		// (float)x/8.0f and (float)z/8.0f are FLOAT32 divisions, widened to float64 for
		// getValue; the product with 8.0 is float64; the (float) cast truncates to float32.
		nx := float64(float32(x) / 8.0)
		nz := float64(float32(z) / 8.0)
		v := float32(m.temperatureNoise.GetValue(nx, nz, false) * 8.0)
		// The whole tail is float32 arithmetic: (v + (float)y - (float)snowLevel) * 0.05f / 40.0f.
		return adjusted - (v+float32(y)-float32(snowLevel))*0.05/40.0
	}
	return adjusted
}

// getTemperature ports Biome.getTemperature(pos, seaLevel). The vanilla method wraps
// getHeightAdjustedTemperature in a per-thread Long2FloatLinkedOpenHashMap memo (a pure
// perf cache); the uncached call is result-identical, so the cache is omitted.
func (m *biomeTemperatureModel) getTemperature(bt biome.Type, x, y, z, seaLevel int) float32 {
	return m.getHeightAdjustedTemperature(bt, x, y, z, seaLevel)
}

// warmEnoughToRain ports Biome.warmEnoughToRain(pos, seaLevel) = getTemperature >= 0.15f.
func (m *biomeTemperatureModel) warmEnoughToRain(bt biome.Type, x, y, z, seaLevel int) bool {
	return m.getTemperature(bt, x, y, z, seaLevel) >= 0.15
}

// coldEnoughToSnow ports Biome.coldEnoughToSnow(pos, seaLevel) = !warmEnoughToRain.
func coldEnoughToSnow(bt biome.Type, x, y, z, seaLevel int) bool {
	m, err := ensureTemperatureModel()
	if err != nil {
		// The biome climate table is build-trusted DATA; a failure here is a build-data
		// error, surfaced loudly (consistent with surfaceNoiseValue's panic discipline)
		// rather than silently defaulting to a wrong surface.
		panic(fmt.Sprintf("surface: biome temperature model: %v", err))
	}
	return !m.warmEnoughToRain(bt, x, y, z, seaLevel)
}
