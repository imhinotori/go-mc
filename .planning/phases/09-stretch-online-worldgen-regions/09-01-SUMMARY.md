---
phase: 09-stretch-online-worldgen-regions
plan: 01
subsystem: worldgen
tags: [worldgen, density-function, noise, caves, carvers, biome-parameters, embed, codegen, parity]

# Dependency graph
requires:
  - phase: 01-codegen-protocol-foundation
    provides: "the tools/ codegen pipeline (sha1-gated jar download, the temp/cache/<version>-inner.jar, the Docker temurin:25-jdk extractor container, ExtractAll.java + the GenBiomes.java Java-extractor pattern)"
  - phase: 02-configuration-registries
    provides: "the //go:embed directory-tree pattern (server/registrydata/embed.go) mirrored here for the worldgen tree"
provides:
  - "world/levelgen/data/: the FULL vanilla 26.2 overworld worldgen graph extracted offline + //go:embed-ed as DATA — the 120KB wired noise_settings/overworld.json (noise_router incl. final_density + the aquifer + ore-vein inputs + the full surface_rule), the ENTIRE 35-file density_function tree INCLUDING the 6 cave functions under overworld/caves/, the 63 noise octave params, the 4 configured_carver configs, the overworld_carver_replaceables tag, and the 7594-box biome_parameters.json"
  - "world/levelgen/data/embed.go: typed accessors (NoiseSettings/DensityFunction/Noise/ConfiguredCarver/CarverReplaceables/BiomeParameters + the *IDs listers) resolving a registry id -> embedded file bytes, with nested cave sub-path handling"
  - "tools/extract_worldgen.go: the wired offline extraction step (pure-unzip the worldgen JSON trees from the inner jar + copy the Java-extracted biome params) with extract-time validation"
  - "tools/java/GenBiomeParams.java: the one Java extractor for the baked OverworldBiomes climate parameters"
affects: [09-02, 09-03, 09-04, 09-05, 09-06, 09-07, worldgen-router-parser, density-function-evaluator, aquifer, ore-veinifier, surface-rules, world-carver, multi-noise-biome-source]

# Tech tracking
tech-stack:
  added: []  # ZERO new runtime deps — embed + encoding/json are stdlib
  patterns:
    - "DATA-driven worldgen: the wired graph (incl. caves) is extracted+embedded+parsed, NEVER hand-transcribed into Go literals"
    - "//go:embed a directory tree with typed registry-id accessors (mirrors server/registrydata/embed.go)"
    - "build-time-trusted DATA: extracted from the sha1-gated pinned jar, parsed once at startup, never runtime input"

key-files:
  created:
    - "tools/extract_worldgen.go"
    - "tools/java/GenBiomeParams.java"
    - "world/levelgen/data/embed.go"
    - "world/levelgen/data/embed_test.go"
    - "world/levelgen/data/ (111 extracted JSON files: noise_settings + density_function incl caves + noise + configured_carver + tags + biome_parameters.json)"
  modified:
    - "tools/main.go (wire genWorldgen into the generators list)"
    - "tools/java/ExtractAll.java (add GenBiomeParams to the custom-extractor list)"
    - "tools/go.mod (go mod tidy: pre-existing 1.22->1.25 staleness)"
    - ".gitignore (exclude /world/playerdata/ runtime saves)"

key-decisions:
  - "The FULL density_function tree (not just overworld/) is extracted so every node the noise_router references resolves for the Wave-3 parser; caves are in the graph (final_density references overworld/caves/*), so caves come 'free' once the node set evaluates the whole graph."
  - "Biome params un-quantized to floats: Climate stores coords as longs (float*10000, Climate.quantizeCoord); GenBiomeParams divides by 10000 to emit the standard vanilla multi-noise JSON [min,max] float shape the Wave-7 source expects."
  - "26.2 renamed ResourceKey.location() -> ResourceKey.identifier() (ResourceLocation -> Identifier) — the GenBiomeParams accessor had to use identifier()."

patterns-established:
  - "Pattern 1: worldgen graph as embedded DATA — extract offline from the pinned jar, //go:embed, parse in pure Go at runtime (no JVM/jar at runtime, CGO_ENABLED=0 clean)"
  - "Pattern 2: pure-unzip iterates EVERY zip entry (never a glob) so nested subtrees like overworld/caves/ are not missed"
  - "Pattern 3: extract-time validation (aquifers/ore_veins/caves/canyon present) fails a partial extraction loudly (T-9-01)"

requirements-completed: [PARITY-01]

# Metrics
duration: 35min
completed: 2026-06-24
---

# Phase 9 Plan 01: FULL Vanilla Worldgen DATA Extraction + Embed Summary

**The complete wired vanilla 26.2 overworld terrain graph — the 120KB noise_router (aquifers + ore-veins enabled), the entire 35-file density_function tree including the 6 cave functions, 63 noise octave params, the ravine/cave carver configs, and the 7594-box baked biome climate parameters — extracted offline from the pinned jar and //go:embed-ed as DATA the later waves parse, never hand-transcribed.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-24T19:38Z
- **Completed:** 2026-06-24T20:13:45Z
- **Tasks:** 2
- **Files modified:** 114 (4 source files + 111 extracted JSON data files; counting the data tree as the single delivered artifact it is ~7 source/config edits)

## Accomplishments

- **PARITY-01 DATA half delivered (full scope):** the WHOLE wired overworld terrain graph is now extracted + embedded as DATA. Verified contents on disk: `noise_settings/overworld.json` is exactly 119,936 bytes (120KB) with `aquifers_enabled:true`, `ore_veins_enabled:true`, `sea_level:63`, the full `noise_router` (final_density + barrier + fluid_level_floodedness/spread + lava + vein_toggle/ridged/gap + continents/depth/erosion/ridges/temperature/vegetation/preliminary_surface_level), and the surface_rule sequence.
- **Caves are in the graph (proven):** all 6 cave density functions (`overworld/caves/{entrances,noodle,pillars,spaghetti_2d,spaghetti_2d_thickness_modulator,spaghetti_roughness_function}.json`) are extracted, and `final_density` in overworld.json textually references `overworld/caves/entrances|noodle|pillars|spaghetti_*` — a test asserts this. Caves come "free" once Wave 3 evaluates the whole graph.
- **Aquifer + ore-vein + carver data present:** the aquifer/ore-vein router inputs are in overworld.json; the 4 `configured_carver` configs (cave, canyon, cave_extra_underground, nether_cave) + the `overworld_carver_replaceables` block tag are extracted for the Wave-5 legacy carver pass.
- **Baked biome params extracted via Java:** `GenBiomeParams.java` reflects `MultiNoiseBiomeSourceParameterList.knownPresets().get(OVERWORLD)` and dumps 7594 6-D climate boxes (temperature/humidity/continentalness/erosion/depth/weirdness [min,max] + offset -> biome id) as `biome_parameters.json` (1.7MB) — the one place a Java extractor was needed (the loose multi_noise JSON is just `{preset:minecraft:overworld}`).
- **Pure Go runtime, zero new deps:** `embed.go` uses only stdlib (embed + encoding/json + fmt + io/fs + path + strings). Runtime `go.mod`/`go.sum` untouched. `CGO_ENABLED=0 go build ./...` clean. No JVM/jar at runtime — Java is build-time extraction only.

## Task Commits

1. **Task 1: Extract the FULL worldgen JSON + baked biome params** — `46077bb4` (feat)
2. **Task 2 (fix): GenBiomeParams ResourceKey accessor for 26.2** — `8621349a` (feat)
3. **Task 2: Run extraction + //go:embed with typed accessors + test** — `ea939a97` (feat)

## Files Created/Modified

- `tools/extract_worldgen.go` — the wired offline step: pure-unzip (archive/zip, iterating every entry) the worldgen JSON trees from `temp/cache/26.2-inner.jar` into `world/levelgen/data/`, copy the Java-extracted `biome_parameters.json`, and extract-time-validate (overworld.json aquifers/ore_veins/router/surface_rule + a cave function + the canyon carver present).
- `tools/java/GenBiomeParams.java` — Java extractor for the baked OverworldBiomes climate boxes -> `biome_parameters.json`.
- `tools/main.go` — wired `{"worldgen", genWorldgen}` into the generators list.
- `tools/java/ExtractAll.java` — added `GenBiomeParams` to the custom-extractor list.
- `world/levelgen/data/embed.go` — `//go:embed` the tree + typed accessors (NoiseSettings/DensityFunction/Noise/ConfiguredCarver/CarverReplaceables/BiomeParameters + *IDs listers).
- `world/levelgen/data/embed_test.go` — `TestWorldgenEmbed` (8 subtests) proving the embed round-trips with the expected keys + the cave reference.
- `world/levelgen/data/**` — 111 extracted JSON files (7 noise_settings, 35 density_function incl 6 caves, 63 noise, 4 configured_carver, 1 carver tag, 1 biome_parameters.json), 2.8MB total — the embedded DATA, committed by name.
- `tools/go.mod` — `go mod tidy` (pre-existing 1.22->1.25 staleness; no new deps).
- `.gitignore` — exclude `/world/playerdata/` (runtime player saves, not source).

## Decisions Made

- **Extract the whole density_function tree, not just overworld/** — the Wave-3 router parser walks all 15 noise_router functions; a missing referenced file would fail that parse, so the full tree (incl. end/nether/amplified/large_biomes variants) is embedded.
- **Un-quantize biome climate coords to floats** — Climate stores coords as `long = float*10000` (`Climate.quantizeCoord`, javap-confirmed factor 10000); GenBiomeParams divides by 10000 to emit the standard vanilla multi-noise `[min,max]` float JSON shape the Wave-7 parser expects.
- **Commit the extracted JSON by name** — it is the embedded DATA (the wired graph), not a build artifact; large size (2.8MB) is expected and intended.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `go mod tidy` on the tools module**
- **Found during:** Task 1 (the `cd tools && go build ./...` verify)
- **Issue:** `go build` in the tools module errored `updates to go.mod needed; to update it: go mod tidy` — confirmed PRE-EXISTING (the same error reproduced on a clean `git stash` of my changes). The tools module's `go` directive was stale at 1.22 vs the installed 1.25 toolchain. This blocked Task 1's verification.
- **Fix:** Ran `go mod tidy` in `tools/`. It bumped only the `go` directive 1.22 -> 1.25.0; `go.sum` was unchanged (my new file is pure stdlib, no new deps). The tools module is the codegen-isolated module — this has zero runtime impact.
- **Files modified:** `tools/go.mod`
- **Verification:** `cd tools && go build ./... && go vet ./...` clean afterward.
- **Committed in:** `46077bb4` (Task 1 commit)

**2. [Rule 1 - Bug] `GenBiomeParams.java` used the pre-26.2 `ResourceKey.location()` accessor**
- **Found during:** Task 2 (running the extraction to produce biome_parameters.json)
- **Issue:** The batch `javac` (which compiles all extractors together) failed at the `pair.getSecond().location()` call — 26.2 renamed `ResourceKey.location()` -> `ResourceKey.identifier()` (and `ResourceLocation` -> `Identifier`). A failed compile in my extractor failed the whole extractor batch, so biome_parameters.json was never produced.
- **Fix:** javap-confirmed `ResourceKey.identifier()` exists in the 26.2 jar; changed the accessor to `.identifier()`.
- **Files modified:** `tools/java/GenBiomeParams.java`
- **Verification:** Re-ran the container extraction; GenBiomeParams compiled, ran, and wrote `biome_parameters.json` (7594 boxes).
- **Committed in:** `8621349a`

**3. [Rule 3 - Blocking] Untracked runtime player-save output not gitignored**
- **Found during:** Task 2 (staging the data tree)
- **Issue:** `world/playerdata/*.dat` (ENT-06 save-on-leave output from a prior server run) was untracked and NOT gitignored — it would be accidentally committable. It is runtime output, not source, and unrelated to this task.
- **Fix:** Added `/world/playerdata/` to `.gitignore` (the existing .gitignore explicitly warns NOT to ignore all of `/world/` since it is the source package, so the exclude is scoped to the runtime save dir only).
- **Files modified:** `.gitignore`
- **Verification:** `git status --short` no longer lists `world/playerdata/`; the data tree commit excluded it.
- **Committed in:** `ea939a97` (Task 2 commit)

---

**Total deviations:** 3 auto-fixed (2 blocking, 1 bug)
**Impact on plan:** All three were necessary to complete the plan's verification (the tidy + the accessor fix were build/extraction blockers; the gitignore prevented committing runtime output). No scope creep — the source/data deliverables are exactly as planned, and the runtime go.mod stayed untouched (zero new runtime deps).

## Issues Encountered

None beyond the deviations above. The pure-unzip and the Java extractor both produced the expected counts (7 / 35 / 63 / 4 / 1 / 7594) on the first full run after the accessor fix.

## Verification Results

- `cd tools && go build ./... && go vet ./...` — clean.
- `go test ./world/levelgen/data/ -run TestWorldgenEmbed -count=1` — PASS (8 subtests: overworld_noise_settings, cave_density_function, noise_param, configured_carver, carver_replaceables, biome_parameters, missing_id_errors).
- `go vet ./world/levelgen/...`, `go build ./...`, `go vet ./...` — all clean.
- `CGO_ENABLED=0 go build ./...` — clean (pure Go, no JVM/jar at runtime).
- Runtime `go.mod`/`go.sum` diff — empty (ZERO new runtime deps).
- `go test ./world/levelgen/...` — both the data package and the parallel 09-02 `world/levelgen` package pass (no interference).

## User Setup Required

None - no external service configuration required. (The Docker temurin:25-jdk extractor container is a build-time-only requirement, already part of the Phase-1 codegen pipeline.)

## Next Phase Readiness

- **The DATA half of full worldgen parity is complete.** The embedded graph is the foundation the rest of Phase 9 consumes:
  - 09-03 (router parser) parses `NoiseSettings("minecraft:overworld")` + the whole `density_function` tree (incl. caves).
  - 09-05 (carver pass) reads `ConfiguredCarver` + `CarverReplaceables`.
  - 09-07 (multi-noise biome source) reads `BiomeParameters()`.
  - 09-02 (Tier-A primitives, already landed on this branch) is disjoint — `world/levelgen/random.go` + synth/, untouched here.
- **No blockers.** The graph is complete and validated; a missing referenced file would have failed extract-time validation or the embed_test.
- The logic half (the ~15 noise primitives + ~30 density-function node types + Aquifer + OreVeinifier + SurfaceRules + Climate) is hand-ported in Waves 2-7 and PARSES this graph.

## Self-Check: PASSED

All created files verified present on disk (tools/extract_worldgen.go, tools/java/GenBiomeParams.java, world/levelgen/data/embed.go + embed_test.go, and the key embedded data files: noise_settings/overworld.json, density_function/overworld/caves/entrances.json, biome_parameters.json, configured_carver/canyon.json, tags/block/overworld_carver_replaceables.json). All three task commits verified in git log (46077bb4, 8621349a, ea939a97).

---
*Phase: 09-stretch-online-worldgen-regions*
*Completed: 2026-06-24*
