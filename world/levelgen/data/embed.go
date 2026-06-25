// Package data embeds the FULL vanilla Minecraft 26.2 worldgen graph as DATA and
// exposes typed accessors that resolve a registry id (e.g. "minecraft:overworld"
// or "minecraft:overworld/caves/entrances") to the embedded JSON bytes.
//
// WHY THIS PACKAGE EXISTS (PARITY-01, the DATA half of full worldgen parity):
//
// The single highest-leverage finding of 09-RESEARCH: the WHOLE wired overworld
// terrain graph is DATA the 26.2 jar already ships as JSON, not logic to be
// hand-written. That includes:
//
//   - noise_settings/overworld.json (120KB): the wired noise_router (final_density,
//     barrier, fluid_level_floodedness/spread, lava, vein_toggle/ridged/gap, ...),
//     aquifers_enabled:true, ore_veins_enabled:true, and the full surface_rule.
//   - the ENTIRE density_function/ tree (35 files) — INCLUDING the cave functions
//     under overworld/caves/ (entrances, noodle, pillars, spaghetti_2d, ...). CAVES
//     ARE IN THE GRAPH: final_density references those functions, so caves are
//     produced as negative-density regions by the SAME graph the surface uses, not
//     a bolted-on system. This is why caves come "free" once the Wave-3 evaluator
//     parses + evaluates the whole graph.
//   - the noise/ octave params, the configured_carver/ configs (the legacy
//     ravine/cave WorldCarver pass, Wave 5), the overworld_carver_replaceables tag.
//   - biome_parameters.json: the overworld multi-noise biome climate boxes (the
//     6-D ParameterPoint per biome), extracted from the BAKED OverworldBiomes by
//     tools/java/GenBiomeParams.java (the one Java extractor — those params are not
//     loose JSON). Consumed by the Wave-7 multi-noise biome source.
//
// HOW IT IS PRODUCED: tools/extract_worldgen.go pure-unzips the JSON trees out of
// the sha1-gated pinned 26.2 jar (temp/cache/26.2-inner.jar) and copies the
// Java-extracted biome params alongside them — all build-time-trusted DATA. The
// runtime reads ONLY this embedded FS: pure Go, stdlib embed + encoding/json, no
// JVM and no jar at runtime (CGO_ENABLED=0 stays clean, zero new runtime deps).
//
// HOW IT IS CONSUMED: the graph is NEVER hand-transcribed into Go literals (the
// 120KB router + the 60KB offset/factor splines + the cave functions are extracted
// + embedded, not typed). The Wave-3 density-function router parser, the Wave-5
// carver, and the Wave-7 biome source PARSE these bytes. Only the ~15 noise
// primitives + the ~30 density-function node TYPES + Aquifer + OreVeinifier +
// SurfaceRules + Climate are hand-ported (Waves 2-7) — the logic half of the split.
package data

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// FS is the embedded worldgen graph. The directories below are the extracted
// vanilla JSON trees (tools/extract_worldgen.go); biome_parameters.json is the
// Java-extracted baked biome climate boxes.
//
//go:embed noise_settings density_function noise configured_carver tags biome_parameters.json
//go:embed configured_feature placed_feature biome
//go:embed structure structure_set
var FS embed.FS

// resolveID splits a "namespace:path" registry id into its path component,
// stripping the minecraft: namespace. A bare id with no namespace is taken as-is.
// The path may itself be nested (e.g. "overworld/caves/entrances") — that nesting
// is preserved so it maps onto the embedded sub-directory structure.
func resolveID(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// readEmbedded reads subdir/<path(id)>.json from the embedded FS, returning a
// clear error for a missing id.
func readEmbedded(subdir, id string) ([]byte, error) {
	rel := resolveID(id)
	if rel == "" {
		return nil, fmt.Errorf("worldgen data: empty id for %s", subdir)
	}
	p := path.Join(subdir, rel+".json")
	b, err := FS.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("worldgen data: %s not found (id %q -> %s): %w", subdir, id, p, err)
	}
	return b, nil
}

// NoiseSettings returns the embedded noise_settings JSON for a registry id.
// e.g. "minecraft:overworld" -> noise_settings/overworld.json. This is the wired
// router the Wave-3 parser consumes (final_density + the aquifer/ore-vein inputs +
// the surface_rule sequence).
func NoiseSettings(id string) ([]byte, error) {
	return readEmbedded("noise_settings", id)
}

// DensityFunction returns the embedded density-function JSON for a registry id.
// The id may be nested: "minecraft:overworld/offset" -> density_function/overworld/
// offset.json; "minecraft:overworld/caves/entrances" ->
// density_function/overworld/caves/entrances.json (the cave functions — caves are
// in the graph). Fed to the Wave-3 router parser as it walks the graph.
func DensityFunction(id string) ([]byte, error) {
	return readEmbedded("density_function", id)
}

// Noise returns the embedded noise octave-param JSON for a registry id.
// e.g. "minecraft:temperature" -> noise/temperature.json. Fed to the Wave-2 noise
// primitives.
func Noise(id string) ([]byte, error) {
	return readEmbedded("noise", id)
}

// ConfiguredCarver returns the embedded configured-carver JSON for a registry id.
// e.g. "minecraft:canyon" -> configured_carver/canyon.json. The legacy ravine/cave
// WorldCarver pass (Wave 5) reads these configs as DATA.
func ConfiguredCarver(id string) ([]byte, error) {
	return readEmbedded("configured_carver", id)
}

// CarverReplaceables returns the embedded overworld_carver_replaceables block tag
// bytes — the set of blocks the legacy carver pass may replace (Wave 5).
func CarverReplaceables() ([]byte, error) {
	b, err := FS.ReadFile("tags/block/overworld_carver_replaceables.json")
	if err != nil {
		return nil, fmt.Errorf("worldgen data: overworld_carver_replaceables tag not found: %w", err)
	}
	return b, nil
}

// BiomeParameters returns the embedded overworld multi-noise biome parameter list
// (the 6-D climate boxes -> biome ids, extracted from the baked OverworldBiomes).
// The Wave-7 multi-noise biome source parses this for real biome diversity.
func BiomeParameters() ([]byte, error) {
	b, err := FS.ReadFile("biome_parameters.json")
	if err != nil {
		return nil, fmt.Errorf("worldgen data: biome_parameters.json not found: %w", err)
	}
	return b, nil
}

// ConfiguredFeatureJSON returns the embedded configured_feature JSON for a
// registry id. e.g. "minecraft:oak" -> configured_feature/oak.json. The
// world/levelgen/feature parser (FEAT-02) parses these polymorphic "type"-tagged
// objects (the Feature type + its full config) into a typed-but-body-deferred AST.
func ConfiguredFeatureJSON(id string) ([]byte, error) { return readEmbedded("configured_feature", id) }

// PlacedFeatureJSON returns the embedded placed_feature JSON for a registry id.
// e.g. "minecraft:oak" -> placed_feature/oak.json. Each is a configured_feature
// ref + an ordered placement-modifier list (FEAT-02).
func PlacedFeatureJSON(id string) ([]byte, error) { return readEmbedded("placed_feature", id) }

// BiomeJSON returns the embedded biome JSON for a registry id.
// e.g. "minecraft:plains" -> biome/plains.json. Carries the 11-element features
// array (a placed_feature HolderSet per GenerationStep) the 11-03 orchestration
// (applyBiomeDecoration) consumes.
func BiomeJSON(id string) ([]byte, error) { return readEmbedded("biome", id) }

// ConfiguredFeatureIDs lists the configured_feature ids available in the embed
// (without the minecraft: namespace). FEAT-02 expects 226 entries.
func ConfiguredFeatureIDs() ([]string, error) { return list("configured_feature") }

// PlacedFeatureIDs lists the placed_feature ids available in the embed. FEAT-02
// expects 262 entries.
func PlacedFeatureIDs() ([]string, error) { return list("placed_feature") }

// BiomeIDs lists the biome ids available in the embed. FEAT-02 expects 66 entries.
func BiomeIDs() ([]string, error) { return list("biome") }

// list returns the .json entry names directly under an embedded sub-directory,
// for callers (later waves) that enumerate a tree rather than resolving by id.
func list(subdir string) ([]string, error) {
	ents, err := fs.ReadDir(FS, subdir)
	if err != nil {
		return nil, fmt.Errorf("worldgen data: listing %s: %w", subdir, err)
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names, nil
}

// NoiseSettingsIDs lists the noise-settings ids available in the embed (without
// the minecraft: namespace), e.g. ["overworld", "nether", ...].
func NoiseSettingsIDs() ([]string, error) { return list("noise_settings") }

// NoiseIDs lists the noise octave-param ids available in the embed.
func NoiseIDs() ([]string, error) { return list("noise") }

// ConfiguredCarverIDs lists the configured-carver ids available in the embed.
func ConfiguredCarverIDs() ([]string, error) { return list("configured_carver") }

// StructureSetJSON returns the embedded structure_set JSON for a registry id.
// e.g. "minecraft:desert_pyramids" -> structure_set/desert_pyramids.json. The
// world/structure placement loader parses the random_spread placement body
// (spacing/separation/salt/spread_type/frequency_reduction_method) — STRUCT-01.
func StructureSetJSON(id string) ([]byte, error) { return readEmbedded("structure_set", id) }

// StructureSetIDs lists the structure_set ids available in the embed (20 entries).
func StructureSetIDs() ([]string, error) { return list("structure_set") }

// StructureJSON returns the embedded structure JSON for a registry id.
// e.g. "minecraft:desert_pyramid" -> structure/desert_pyramid.json. 14-02/14-03
// read the structure body (type + biomes ref + step) when they assemble pieces.
func StructureJSON(id string) ([]byte, error) { return readEmbedded("structure", id) }

// StructureIDs lists the structure ids available in the embed (34 entries).
func StructureIDs() ([]string, error) { return list("structure") }

// HasStructureBiomeTag returns the embedded worldgen/biome/has_structure tag JSON
// for a structure id, e.g. "desert_pyramid" -> tags/worldgen/biome/has_structure/
// desert_pyramid.json (the {"values":[...]} biome allow-list). The 14-02/14-03
// temple placement gates each start on GetBiome-at-origin in this set (STRUCT-01
// biome-check seam). The id is taken bare (namespace stripped).
func HasStructureBiomeTag(id string) ([]byte, error) {
	rel := resolveID(id)
	if rel == "" {
		return nil, fmt.Errorf("worldgen data: empty has_structure tag id")
	}
	p := path.Join("tags", "worldgen", "biome", "has_structure", rel+".json")
	b, err := FS.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("worldgen data: has_structure tag not found (id %q -> %s): %w", id, p, err)
	}
	return b, nil
}

// BiomeCategoryTag returns the embedded worldgen/biome/<id>.json category tag (the is_*
// biome tags: is_ocean, is_badlands, is_taiga, ...). The has_structure tags nest these via
// "#minecraft:is_*" references; HasStructureBiomes resolves them recursively through here so
// the mineshaft's biome allow-set (which is expressed almost entirely as nested category
// refs) flattens to concrete biome ids. The id is taken bare (namespace stripped).
func BiomeCategoryTag(id string) ([]byte, error) {
	rel := resolveID(id)
	if rel == "" {
		return nil, fmt.Errorf("worldgen data: empty biome category tag id")
	}
	p := path.Join("tags", "worldgen", "biome", rel+".json")
	b, err := FS.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("worldgen data: biome category tag not found (id %q -> %s): %w", id, p, err)
	}
	return b, nil
}
