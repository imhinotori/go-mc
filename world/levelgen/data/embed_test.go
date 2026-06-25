package data

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorldgenEmbed proves the DATA half of PARITY-01 is wired: the FULL vanilla
// worldgen graph (the wired router with aquifers/ore-veins enabled + a cave
// density function + a noise param + the canyon carver + the carver-replaceables
// tag + the baked biome params) is extracted, embedded, and resolves + parses as
// valid JSON. If any referenced file were missing from the embed, the later-wave
// parsers would fail; this test catches that at build time.
func TestWorldgenEmbed(t *testing.T) {
	// 1. The embed is non-empty (at least the key trees resolve).
	if ids, err := NoiseSettingsIDs(); err != nil || len(ids) == 0 {
		t.Fatalf("NoiseSettingsIDs() = %v, %v; want non-empty", ids, err)
	}

	// 2. noise_settings/overworld.json parses with the wired-router keys, the
	//    aquifer/ore-vein toggles, sea_level, and the default block/fluid.
	t.Run("overworld_noise_settings", func(t *testing.T) {
		raw, err := NoiseSettings("minecraft:overworld")
		if err != nil {
			t.Fatalf("NoiseSettings(overworld): %v", err)
		}
		if len(raw) == 0 {
			t.Fatal("overworld.json is empty")
		}

		var ns struct {
			SeaLevel     *int `json:"sea_level"`
			DefaultBlock struct {
				Name string `json:"Name"`
			} `json:"default_block"`
			DefaultFluid struct {
				Name string `json:"Name"`
			} `json:"default_fluid"`
			NoiseRouter     map[string]json.RawMessage `json:"noise_router"`
			SurfaceRule     json.RawMessage            `json:"surface_rule"`
			AquifersEnabled *bool                      `json:"aquifers_enabled"`
			OreVeinsEnabled *bool                      `json:"ore_veins_enabled"`
		}
		if err := json.Unmarshal(raw, &ns); err != nil {
			t.Fatalf("overworld.json does not parse as JSON: %v", err)
		}

		if ns.SeaLevel == nil || *ns.SeaLevel != 63 {
			t.Errorf("sea_level = %v; want 63", ns.SeaLevel)
		}
		if ns.DefaultBlock.Name != "minecraft:stone" {
			t.Errorf("default_block = %q; want minecraft:stone", ns.DefaultBlock.Name)
		}
		if ns.DefaultFluid.Name != "minecraft:water" {
			t.Errorf("default_fluid = %q; want minecraft:water", ns.DefaultFluid.Name)
		}
		if ns.AquifersEnabled == nil || !*ns.AquifersEnabled {
			t.Errorf("aquifers_enabled = %v; want true", ns.AquifersEnabled)
		}
		if ns.OreVeinsEnabled == nil || !*ns.OreVeinsEnabled {
			t.Errorf("ore_veins_enabled = %v; want true", ns.OreVeinsEnabled)
		}
		if len(ns.SurfaceRule) == 0 {
			t.Error("surface_rule missing")
		}

		// The noise_router must carry the wired graph incl. the aquifer + ore-vein
		// inputs (proving those systems' data is present for Waves 4-6).
		for _, key := range []string{
			"final_density", "barrier",
			"fluid_level_floodedness", "fluid_level_spread", "lava",
			"vein_toggle", "vein_ridged", "vein_gap",
		} {
			if _, ok := ns.NoiseRouter[key]; !ok {
				t.Errorf("noise_router missing key %q", key)
			}
		}

		// final_density must REFERENCE a cave function — caves are in the graph.
		if fd, ok := ns.NoiseRouter["final_density"]; ok {
			if !containsCaveRef(fd) {
				t.Errorf("final_density does not reference a cave function; caves must be in the graph")
			}
		}
	})

	// 3. A cave density function resolves and is non-empty valid JSON. CAVES ARE
	//    IN THE GRAPH (negative-density carved regions).
	t.Run("cave_density_function", func(t *testing.T) {
		raw, err := DensityFunction("minecraft:overworld/caves/entrances")
		if err != nil {
			t.Fatalf("DensityFunction(overworld/caves/entrances): %v", err)
		}
		if len(raw) == 0 {
			t.Fatal("caves/entrances.json is empty")
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("caves/entrances.json does not parse as JSON: %v", err)
		}
	})

	// 4. A noise param resolves and parses with firstOctave + amplitudes.
	t.Run("noise_param", func(t *testing.T) {
		raw, err := Noise("minecraft:temperature")
		if err != nil {
			t.Fatalf("Noise(temperature): %v", err)
		}
		var n struct {
			FirstOctave *int      `json:"firstOctave"`
			Amplitudes  []float64 `json:"amplitudes"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatalf("temperature.json does not parse as JSON: %v", err)
		}
		if n.FirstOctave == nil {
			t.Error("noise param missing firstOctave")
		}
		if len(n.Amplitudes) == 0 {
			t.Error("noise param missing amplitudes")
		}
	})

	// 5. The canyon (ravine) carver config resolves and parses.
	t.Run("configured_carver", func(t *testing.T) {
		raw, err := ConfiguredCarver("minecraft:canyon")
		if err != nil {
			t.Fatalf("ConfiguredCarver(canyon): %v", err)
		}
		var c struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("canyon.json does not parse as JSON: %v", err)
		}
		if c.Type != "minecraft:canyon" {
			t.Errorf("canyon carver type = %q; want minecraft:canyon", c.Type)
		}
		if len(c.Config) == 0 {
			t.Error("canyon carver missing config")
		}
	})

	// 6. The carver-replaceables block tag resolves and parses.
	t.Run("carver_replaceables", func(t *testing.T) {
		raw, err := CarverReplaceables()
		if err != nil {
			t.Fatalf("CarverReplaceables(): %v", err)
		}
		var tag struct {
			Values json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(raw, &tag); err != nil {
			t.Fatalf("overworld_carver_replaceables does not parse as JSON: %v", err)
		}
		if len(tag.Values) == 0 {
			t.Error("carver-replaceables tag missing values")
		}
	})

	// 7. The baked biome parameters resolve and parse as a non-empty list of
	//    6-D climate boxes (the Wave-7 multi-noise source input).
	t.Run("biome_parameters", func(t *testing.T) {
		raw, err := BiomeParameters()
		if err != nil {
			t.Fatalf("BiomeParameters(): %v", err)
		}
		var boxes []struct {
			Biome      string `json:"biome"`
			Parameters struct {
				Temperature     []float64 `json:"temperature"`
				Humidity        []float64 `json:"humidity"`
				Continentalness []float64 `json:"continentalness"`
				Erosion         []float64 `json:"erosion"`
				Depth           []float64 `json:"depth"`
				Weirdness       []float64 `json:"weirdness"`
				Offset          *float64  `json:"offset"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &boxes); err != nil {
			t.Fatalf("biome_parameters.json does not parse as a list: %v", err)
		}
		if len(boxes) == 0 {
			t.Fatal("biome_parameters.json is an empty list")
		}
		// Spot-check the first box has a biome id and a 2-element climate span.
		b := boxes[0]
		if b.Biome == "" {
			t.Error("first biome box has empty biome id")
		}
		if len(b.Parameters.Temperature) != 2 {
			t.Errorf("first biome box temperature = %v; want a [min,max] pair", b.Parameters.Temperature)
		}
		if b.Parameters.Offset == nil {
			t.Error("first biome box missing offset")
		}
	})

	// 8. A missing id errors clearly (the accessor contract).
	t.Run("missing_id_errors", func(t *testing.T) {
		if _, err := NoiseSettings("minecraft:does_not_exist"); err == nil {
			t.Error("NoiseSettings(missing) returned nil error; want a clear error")
		}
	})
}

// TestWorldgenFeatureEmbed proves the DATA half of FEAT-02 is wired: the full
// vanilla feature/decoration data tree (226 configured_feature + 262 placed_feature
// + 66 biome JSONs) is extracted, embedded, and resolves by id. These counts are
// the FEAT-02 "embedded data loads" acceptance — a partial extraction (a missing
// configured_feature a placed_feature references) would fail the Wave-11 parser, so
// the count assertions catch a build-data mismatch here.
func TestWorldgenFeatureEmbed(t *testing.T) {
	cases := []struct {
		name string
		ids  func() ([]string, error)
		want int
	}{
		{"configured_feature", ConfiguredFeatureIDs, 226},
		{"placed_feature", PlacedFeatureIDs, 262},
		{"biome", BiomeIDs, 66},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			ids, err := c.ids()
			if err != nil {
				t.Fatalf("%s ids: %v", c.name, err)
			}
			if len(ids) != c.want {
				t.Errorf("%s count = %d; want %d", c.name, len(ids), c.want)
			}
		})
	}

	// Spot reads: a known configured_feature + biome resolve to non-empty bytes.
	t.Run("spot_reads", func(t *testing.T) {
		raw, err := ConfiguredFeatureJSON("minecraft:oak")
		if err != nil || len(raw) == 0 {
			t.Fatalf("ConfiguredFeatureJSON(oak) = %d bytes, %v; want non-empty", len(raw), err)
		}
		var cf struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &cf); err != nil {
			t.Fatalf("oak configured_feature does not parse: %v", err)
		}
		if cf.Type != "minecraft:tree" {
			t.Errorf("oak configured_feature type = %q; want minecraft:tree", cf.Type)
		}

		praw, err := PlacedFeatureJSON("minecraft:oak")
		if err != nil || len(praw) == 0 {
			t.Fatalf("PlacedFeatureJSON(oak) = %d bytes, %v; want non-empty", len(praw), err)
		}

		braw, err := BiomeJSON("minecraft:plains")
		if err != nil || len(braw) == 0 {
			t.Fatalf("BiomeJSON(plains) = %d bytes, %v; want non-empty", len(braw), err)
		}
	})

	// A missing id errors clearly (the accessor contract).
	t.Run("missing_id_errors", func(t *testing.T) {
		if _, err := ConfiguredFeatureJSON("minecraft:does_not_exist"); err == nil {
			t.Error("ConfiguredFeatureJSON(missing) returned nil error; want a clear error")
		}
	})
}

// containsCaveRef reports whether the density-function JSON references one of the
// overworld cave functions (used to prove caves are wired into final_density).
func containsCaveRef(raw json.RawMessage) bool {
	s := string(raw)
	for _, ref := range []string{
		"overworld/caves/entrances",
		"overworld/caves/noodle",
		"overworld/caves/pillars",
		"overworld/caves/spaghetti_2d",
	} {
		if strings.Contains(s, ref) {
			return true
		}
	}
	return false
}
