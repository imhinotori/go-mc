package main

// extract_worldgen.go — PARITY-01 (DATA half): the offline step that copies the
// FULL vanilla worldgen graph DATA out of the pinned 26.2 jar into the runtime
// tree at world/levelgen/data/, so the later waves PARSE the graph instead of
// hand-transcribing it.
//
// TWO DATA SOURCES, TWO METHODS:
//
//  1. PURE UNZIP (no Java): the wired noise router, the WHOLE density-function
//     tree (INCLUDING the cave functions under overworld/caves/), the noise octave
//     params, the configured-carver configs, and the carver-replaceables block tag
//     are PLAIN JSON resources inside temp/cache/<version>-inner.jar. We open the
//     jar as a stdlib archive/zip and ITERATE EVERY entry (a glob over
//     density_function/* does NOT recurse into overworld/caves/ — iterate, never
//     glob), copying each resource under the worldgen prefixes verbatim into
//     world/levelgen/data/, preserving sub-paths. This mirrors how Phase 2 embedded
//     the registry NBT: the JSON is build-time-trusted DATA, embedded + parsed in
//     pure Go at runtime (no JVM, no jar at runtime).
//
//  2. JAVA EXTRACTOR (GenBiomeParams.java): the overworld biome climate parameters
//     are BAKED into OverworldBiomes/MultiNoiseBiomeSourceParameterList (the loose
//     multi_noise_biome_source_parameter_list/overworld.json is just
//     {"preset":"minecraft:overworld"}). The container-run GenBiomeParams extractor
//     reflects over the runtime and writes biome_parameters.json into the jsons
//     output dir; this step copies that into world/levelgen/data/.
//
// EXTRACT-TIME VALIDATION (T-9-01): after the unzip we assert overworld.json
// parses with the expected keys (aquifers_enabled==true, ore_veins_enabled==true,
// sea_level, default_block, default_fluid, noise_router, surface_rule) AND that a
// cave density function (overworld/caves/entrances.json) was copied — so a partial
// extraction that misses the cave subtree fails LOUDLY rather than silently
// shipping an incomplete graph.

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// worldgenDataDir returns the runtime embed directory (world/levelgen/data)
// relative to the repo root. This is the //go:embed source the runtime reads.
func worldgenDataDir(goMCRoot string) string {
	return filepath.Join(goMCRoot, "world", "levelgen", "data")
}

// worldgenZipPrefixes maps a jar resource prefix to the destination sub-directory
// under world/levelgen/data/. Every zip entry whose name starts with the prefix is
// copied, preserving the path tail after the jar's "data/minecraft/worldgen/"
// (or "data/minecraft/") root. We intentionally take the WHOLE density_function
// tree (not just overworld/) so every node the noise_router references resolves —
// the Wave-3 router parser walks all 15 functions, and a missing referenced file
// would fail that parse.
var worldgenZipPrefixes = []struct {
	jarPrefix string // prefix inside the inner jar
	destSub   string // destination sub-dir under world/levelgen/data/
}{
	{"data/minecraft/worldgen/noise_settings/", "noise_settings"},
	{"data/minecraft/worldgen/density_function/", "density_function"},
	{"data/minecraft/worldgen/noise/", "noise"},
	{"data/minecraft/worldgen/configured_carver/", "configured_carver"},
	// FEAT-02 (Phase 11): the feature/decoration data half. configured_feature
	// (the Feature type + full config), placed_feature (a configured ref + ordered
	// placement modifier list), and biome (the 11-element features array per
	// GenerationStep) are PURE-UNZIPPED here — same mechanism, embedded + parsed by
	// the world/levelgen/feature package, never hand-transcribed. All three trees
	// are FLAT (no nesting) in the jar.
	{"data/minecraft/worldgen/configured_feature/", "configured_feature"}, // 226
	{"data/minecraft/worldgen/placed_feature/", "placed_feature"},         // 262
	{"data/minecraft/worldgen/biome/", "biome"},                           // 66
	// STRUCT-01 (Phase 14): the structure PIPELINE data half. structure (the
	// per-structure type+config — 34 entries) and structure_set (the placement
	// machinery: spacing/separation/salt/spread_type per set — 20 entries) are
	// PURE-UNZIPPED JSON (the temples ship 0 .nbt — they are code-assembled, so
	// NO binary extraction this phase). Embedded + parsed by world/structure,
	// never hand-transcribed. Both trees are FLAT in the jar.
	{"data/minecraft/worldgen/structure/", "structure"},         // 34
	{"data/minecraft/worldgen/structure_set/", "structure_set"}, // 20
	// The has_structure biome-tag allow-lists (the load-bearing half of "vanilla
	// positions"): desert_pyramid->[desert], igloo->[snowy_taiga,snowy_plains,
	// snowy_slopes], jungle_temple->[bamboo_jungle,jungle], swamp_hut->[swamp].
	// 14-02/14-03 gate each temple's start on GetBiome-at-origin in its allow-set.
	{"data/minecraft/tags/worldgen/biome/has_structure/", "tags/worldgen/biome/has_structure"},
	// STRUCT-04 (Phase 15): the BARE worldgen/biome tags (is_* category tags AND
	// stronghold_biased_to). The stronghold's concentric_rings placement biome-
	// validates each ring position against #minecraft:stronghold_biased_to (the
	// ~38-biome preferred set); the is_* category tags back the mineshaft/mesa
	// has_structure nested #-refs. This parent prefix re-lands the has_structure/
	// sub-tree at the same dest (idempotent) and additionally pulls every flat
	// biome tag at this level — stronghold_biased_to.json included.
	{"data/minecraft/tags/worldgen/biome/", "tags/worldgen/biome"},
	// STRUCT-05 (Phase 16): the .nbt StructureTemplate system DATA half — the
	// data-driven geometry layer villages need. THREE new trees, all pure-unzip:
	//
	//  1. The BINARY village .nbt StructureTemplates (483 files, gzip-wrapped). The
	//     existing copyZipEntry streams bytes VERBATIM (no JSON transform), so the
	//     binary path needs only this prefix entry, NOT new logic (D-RESEARCH "Don't
	//     Hand-Roll": binary .nbt = copyZipEntry verbatim). Scoped to village/ to keep
	//     the embed small per STRUCT-05 — bastion/mansion/etc .nbt are v3 deferrals.
	//     The .nbt files nest under structure/village/...; they do NOT collide with the
	//     flat structure/*.json (different extension + nested path).
	{"data/minecraft/structure/village/", "structure/village"}, // 483 binary .nbt
	// 2. The 62 village template_pool JSONs (common 6 + desert 12 + plains 11 +
	//    savanna 12 + snowy 11 + taiga 10). The 16-02 jigsaw Placer parses these
	//    (the weighted element list + the fallback chain). Scoped to village/ — other
	//    structures' pools are v3.
	{"data/minecraft/worldgen/template_pool/village/", "template_pool/village"}, // 62
	// 3. The 40 processor_list JSONs (small, take all). The block-replace processors
	//    villages apply at place time (mossify/zombie/street/farm rule lists).
	{"data/minecraft/worldgen/processor_list/", "processor_list"}, // 40
	// BASTION (nether jigsaw structure, gap-reaudit): the bastion_remnant JigsawStructure
	// (nether_complexes structure_set, shared with the fortress at weight fortress:2 /
	// bastion:3) reads its 60 template_pool/bastion/** JSONs (units / hoglin_stable /
	// treasure / bridge + the shared blocks/mobs/walls sub-pools) and 167 structure/bastion/**
	// .nbt piece templates. Same pure-unzip path the village trees use; the .nbt are binary
	// gzip-wrapped StructureTemplates the world/structure loader gunzips at runtime. The 12
	// bastion rule processor_lists ride the whole-tree processor_list prefix above.
	{"data/minecraft/worldgen/template_pool/bastion/", "template_pool/bastion"}, // 60
	{"data/minecraft/structure/bastion/", "structure/bastion"},                  // 167 binary .nbt
	// TRIAL CHAMBERS + ANCIENT CITY (jigsaw structures, gap-reaudit): both are JigsawStructures
	// on their own single-entry structure_sets. trial_chambers reads its 47 template_pool/
	// trial_chambers/** JSONs + 191 structure/trial_chambers/** .nbt piece templates; ancient_city
	// reads its 7 template_pool/ancient_city/** JSONs + 58 structure/ancient_city/** .nbt. Same
	// pure-unzip path the village + bastion trees use; the .nbt are binary gzip-wrapped
	// StructureTemplates the world/structure loader gunzips at runtime. Their degradation
	// processor_lists ride the whole-tree processor_list prefix above.
	{"data/minecraft/worldgen/template_pool/trial_chambers/", "template_pool/trial_chambers"}, // 47
	{"data/minecraft/structure/trial_chambers/", "structure/trial_chambers"},                  // 191 binary .nbt
	{"data/minecraft/worldgen/template_pool/ancient_city/", "template_pool/ancient_city"},     // 7
	{"data/minecraft/structure/ancient_city/", "structure/ancient_city"},                      // 58 binary .nbt
	// WOODLAND MANSION (surface structure, gap-reaudit): the mansion is CODE-generated (grid RNG),
	// but its room/wall/roof geometry is 73 structure/woodland_mansion/*.nbt templates the
	// WoodlandMansionPiece placer emits. No template_pool (not a jigsaw structure). Same pure-unzip.
	{"data/minecraft/structure/woodland_mansion/", "structure/woodland_mansion"}, // 73 binary .nbt
}

// worldgenSingleFiles names individual jar resources (not whole trees) to copy,
// each mapped to its destination path under world/levelgen/data/.
var worldgenSingleFiles = []struct {
	jarPath string // exact entry name in the inner jar
	dest    string // destination path under world/levelgen/data/ (slash-separated)
}{
	{"data/minecraft/tags/block/overworld_carver_replaceables.json", "tags/block/overworld_carver_replaceables.json"},
	// FEAT-17-16: the #minecraft:supports_vegetation tag chain — the sustaining-block
	// set VegetationBlock.mayPlaceOn(state, level, pos) tests (state.is(SUPPORTS_VEGETATION))
	// inside VegetationBlock.canSurvive, which SimpleBlockFeature.place gates on before
	// placing a plant. Resolving it via data.BlockTag("supports_vegetation") needs the
	// whole nested chain embedded: supports_vegetation -> #substrate_overworld + farmland,
	// substrate_overworld -> #dirt + #mud + #moss_blocks + #grass_blocks. Each is copied
	// verbatim from the jar so the membership is authoritative, never hand-transcribed.
	{"data/minecraft/tags/block/supports_vegetation.json", "tags/block/supports_vegetation.json"},
	{"data/minecraft/tags/block/substrate_overworld.json", "tags/block/substrate_overworld.json"},
	{"data/minecraft/tags/block/dirt.json", "tags/block/dirt.json"},
	{"data/minecraft/tags/block/mud.json", "tags/block/mud.json"},
	{"data/minecraft/tags/block/moss_blocks.json", "tags/block/moss_blocks.json"},
	{"data/minecraft/tags/block/grass_blocks.json", "tags/block/grass_blocks.json"},
	// PROTECTED BLOCKS (trial_chambers + ancient_city degradation processor_lists): the
	// ProtectedBlockProcessor cannotReplace HolderSet is #minecraft:features_cannot_replace.
	// Flat tag (no nested refs). Needed so LoadProcessorList resolves the protected_blocks value.
	{"data/minecraft/tags/block/features_cannot_replace.json", "tags/block/features_cannot_replace.json"},
	// STRUCT-05 (Phase 16): the minecraft:empty terminator pool. It lives at the
	// TOP level (data/minecraft/worldgen/template_pool/empty.json — OUTSIDE village/),
	// so the village-only prefix above never copies it. 42 of the 62 village pools
	// reference it as the fallback-chain terminator (Pitfall #6 depth-0 -> empty);
	// without it the 16-02 Placer's data.TemplatePoolJSON("empty") returns not-found
	// and the jigsaw never terminates correctly.
	{"data/minecraft/worldgen/template_pool/empty.json", "template_pool/empty.json"},
}

// genWorldgen is the wired generator entry: it pure-unzips the worldgen JSON
// trees from the inner jar into world/levelgen/data/ and copies the
// Java-extracted biome_parameters.json alongside them, then validates the result.
func genWorldgen(jsonDir, goMCRoot string) error {
	version := filepath.Base(jsonDir)
	innerJar := filepath.Join(goMCRoot, "temp", "cache", version+"-inner.jar")
	dataDir := worldgenDataDir(goMCRoot)

	if _, err := os.Stat(innerJar); err != nil {
		return fmt.Errorf("worldgen: inner jar not found at %s (run the extractor first): %w", innerJar, err)
	}

	zr, err := zip.OpenReader(innerJar)
	if err != nil {
		return fmt.Errorf("worldgen: opening inner jar %s: %w", innerJar, err)
	}
	defer zr.Close()

	// Counts per destination sub-tree for the log line.
	counts := map[string]int{}

	// PURE UNZIP: iterate EVERY zip entry exactly once and match the prefixes.
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name

		// Whole-tree prefixes.
		matched := false
		for _, p := range worldgenZipPrefixes {
			if strings.HasPrefix(name, p.jarPrefix) {
				rel := name[len(p.jarPrefix):] // path tail, preserves overworld/caves/...
				dest := filepath.Join(dataDir, p.destSub, filepath.FromSlash(rel))
				if err := copyZipEntry(f, dest); err != nil {
					return fmt.Errorf("worldgen: copying %s: %w", name, err)
				}
				counts[p.destSub]++
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		// Individual single-file resources.
		for _, sf := range worldgenSingleFiles {
			if name == sf.jarPath {
				dest := filepath.Join(dataDir, filepath.FromSlash(sf.dest))
				if err := copyZipEntry(f, dest); err != nil {
					return fmt.Errorf("worldgen: copying %s: %w", name, err)
				}
				counts["tags"]++
			}
		}
	}

	for _, p := range worldgenZipPrefixes {
		logf("  worldgen: %-18s %d files", p.destSub, counts[p.destSub])
	}
	logf("  worldgen: %-18s %d files", "tags", counts["tags"])

	// Copy the Java-extracted biome_parameters.json (baked OverworldBiomes params)
	// from the jsons output dir into the runtime data dir.
	if err := copyBiomeParameters(jsonDir, dataDir); err != nil {
		return err
	}

	// EXTRACT-TIME VALIDATION (T-9-01): fail loudly on a partial/incomplete extraction.
	if err := validateWorldgenExtraction(dataDir); err != nil {
		return err
	}

	logf("  worldgen: extraction validated (aquifers + ore_veins + caves present)")
	return nil
}

// copyZipEntry writes the contents of a zip entry to dest, creating parent dirs.
func copyZipEntry(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating dir for %s: %w", dest, err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("opening zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return nil
}

// copyBiomeParameters copies the Java-extracted biome_parameters.json from the
// jsons output dir into world/levelgen/data/. The Java extractor
// (GenBiomeParams.java, run inside the container) writes it there.
func copyBiomeParameters(jsonDir, dataDir string) error {
	src := filepath.Join(jsonDir, "biome_parameters.json")
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("worldgen: biome_parameters.json not found at %s "+
			"(GenBiomeParams.java must run in the extractor container first): %w", src, err)
	}
	// Sanity: it must be a non-empty JSON array.
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("worldgen: biome_parameters.json is not valid JSON: %w", err)
	}
	if len(arr) == 0 {
		return fmt.Errorf("worldgen: biome_parameters.json is an empty list (extractor produced no biome boxes)")
	}
	dest := filepath.Join(dataDir, "biome_parameters.json")
	if err := writeFile(dest, data); err != nil {
		return fmt.Errorf("worldgen: writing biome_parameters.json: %w", err)
	}
	logf("  worldgen: %-18s %d biome boxes", "biome_parameters", len(arr))
	return nil
}

// noiseSettingsShape is the subset of noise_settings/overworld.json the extract
// validates. It proves the wired router (incl. aquifer + ore-vein inputs) and the
// surface-rule sequence survived the copy.
type noiseSettingsShape struct {
	SeaLevel        *int            `json:"sea_level"`
	DefaultBlock    json.RawMessage `json:"default_block"`
	DefaultFluid    json.RawMessage `json:"default_fluid"`
	NoiseRouter     json.RawMessage `json:"noise_router"`
	SurfaceRule     json.RawMessage `json:"surface_rule"`
	AquifersEnabled *bool           `json:"aquifers_enabled"`
	OreVeinsEnabled *bool           `json:"ore_veins_enabled"`
}

// validateWorldgenExtraction asserts the extraction is COMPLETE for full parity:
// overworld.json parses with the expected keys (aquifers/ore-veins enabled,
// sea_level, default_block/fluid, noise_router, surface_rule), AND a cave density
// function was copied. A partial extraction (e.g. one that misses the caves
// subtree) fails here loudly rather than silently shipping an incomplete graph.
func validateWorldgenExtraction(dataDir string) error {
	// 1. overworld.json must parse with the router/aquifer/ore-vein/surface keys.
	overworldPath := filepath.Join(dataDir, "noise_settings", "overworld.json")
	raw, err := os.ReadFile(overworldPath)
	if err != nil {
		return fmt.Errorf("worldgen: noise_settings/overworld.json missing after extraction: %w", err)
	}
	var ns noiseSettingsShape
	if err := json.Unmarshal(raw, &ns); err != nil {
		return fmt.Errorf("worldgen: noise_settings/overworld.json is not valid JSON: %w", err)
	}
	switch {
	case ns.SeaLevel == nil:
		return fmt.Errorf("worldgen: overworld.json missing sea_level")
	case len(ns.DefaultBlock) == 0:
		return fmt.Errorf("worldgen: overworld.json missing default_block")
	case len(ns.DefaultFluid) == 0:
		return fmt.Errorf("worldgen: overworld.json missing default_fluid")
	case len(ns.NoiseRouter) == 0:
		return fmt.Errorf("worldgen: overworld.json missing noise_router (the wired graph)")
	case len(ns.SurfaceRule) == 0:
		return fmt.Errorf("worldgen: overworld.json missing surface_rule")
	case ns.AquifersEnabled == nil || !*ns.AquifersEnabled:
		return fmt.Errorf("worldgen: overworld.json aquifers_enabled is not true (got %v)", ns.AquifersEnabled)
	case ns.OreVeinsEnabled == nil || !*ns.OreVeinsEnabled:
		return fmt.Errorf("worldgen: overworld.json ore_veins_enabled is not true (got %v)", ns.OreVeinsEnabled)
	}

	// 2. A cave density function must be present — caves are IN the graph.
	cavePath := filepath.Join(dataDir, "density_function", "overworld", "caves", "entrances.json")
	if fi, err := os.Stat(cavePath); err != nil || fi.Size() == 0 {
		return fmt.Errorf("worldgen: density_function/overworld/caves/entrances.json missing or empty "+
			"(the cave functions must be extracted — caves are in the graph): %v", err)
	}

	// 3. A configured carver (the legacy ravine/cave pass) must be present.
	canyonPath := filepath.Join(dataDir, "configured_carver", "canyon.json")
	if fi, err := os.Stat(canyonPath); err != nil || fi.Size() == 0 {
		return fmt.Errorf("worldgen: configured_carver/canyon.json missing or empty "+
			"(the carver configs must be extracted): %v", err)
	}

	return nil
}
