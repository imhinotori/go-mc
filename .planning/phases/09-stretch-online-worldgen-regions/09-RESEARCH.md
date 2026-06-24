# Phase 9: Stretch — Vanilla-Parity Worldgen (PARITY-01) - Research

**Researched:** 2026-06-24
**Domain:** Minecraft 26.2 density-function terrain generation — port `net.minecraft.world.level.levelgen` from the unobfuscated jar into pure Go, behind the existing off-tick `world.Generator` seam
**Confidence:** HIGH on the port targets, the data-driven graph, and the seeding chain; MEDIUM on the exact v2 effort estimate (depends on the chosen scope tier)

> **Scope note:** This research covers **PARITY-01 ONLY**. The user chose PARITY-01 for Phase 9; **ONLINE-01, ONLINE-02, and REGION-01 are DEFERRED** (not researched here, not in scope for the resulting plans). The roadmap groups all four under "Phase 9 stretch," but only PARITY-01 is being planned now.

## Summary

The question is **not "which noise library."** It is settled (CLAUDE.md, verified again this session) that no off-the-shelf noise primitive reproduces vanilla terrain — Minecraft 26.2 uses a specific improved-Perlin / octave stack (`PerlinNoise` → `NormalNoise`), a legacy `BlendedNoise`, a `Xoroshiro`-seeded `PositionalRandomFactory`, and a **density-function graph** whose final node decides solid-vs-air per block. The real question is: *what is the exact port order of the levelgen classes, how much of the graph is DATA vs hand-ported, and what v2 scope yields recognizable Minecraft terrain without boiling the ocean.*

**The single highest-leverage finding of this research:** the entire wired overworld terrain graph is **DATA in the jar**, not Java logic. `[VERIFIED: unzip -l temp/cache/26.2-inner.jar]` ships `data/minecraft/worldgen/noise_settings/overworld.json` (120 KB — the complete wired `NoiseRouter` including `final_density`, `sea_level: 63`, `default_block: stone`, `default_fluid: water`, `legacy_random_source: false`), a tree of 35 `density_function/*.json` nodes (`overworld/offset.json` is 60 KB of spline data, `factor.json` 34 KB, `jaggedness.json` 12 KB), and the `noise/*.json` octave-amplitude parameters (e.g. `temperature.json` = `firstOctave -10, amplitudes [1.5,0,1,0,0,0]`). This means **the graph must NOT be hand-transcribed** — Sulfur extracts these JSON files offline (extend the `tools/` codegen) and a pure-Go density-function *evaluator* walks the parsed tree at runtime. Only the ~15 leaf **primitives** (the noise math + the `Xoroshiro` seeding + the ~25 density-function node *types*) are hand-ported from the `.class` files; the *wiring* of those primitives into overworld terrain is parsed data.

**Primary recommendation:** Target **Tier T0 — a faithful overworld surface**: port the `Xoroshiro` seeding chain + `ImprovedNoise`/`PerlinNoise`/`NormalNoise`/`BlendedNoise` primitives exactly (seed-matched), build a data-driven `DensityFunction` evaluator that parses the vanilla `density_function`/`noise_settings` JSON, evaluate `final_density` per cell with vanilla's **cell-interpolation** (`NoiseChunk`: sample on a coarse 4×8×4-ish grid, trilerp between), place `default_block`/`default_fluid` with `sea_level: 63` water, and apply a **minimal surface layer** (grass/dirt/stone via a small subset of `SurfaceRules`, or a hardcoded single-biome surface for the first cut). **Defer:** caves/carvers, full aquifers, ore veins, structures, features (trees/ores), and full multi-noise biome diversity. The outcome a real 26.2 client sees: recognizable Minecraft terrain — hills, valleys, plains, oceans at sea level — generated behind the **unchanged** `world.Generator` interface and the **unchanged** off-tick worker. No concurrency work, no new runtime dependency.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Seed → RandomSource → per-noise seeding | Pure Go port (hand) | Offline (none) | `Xoroshiro` + `RandomSupport` mixing must match the jar bit-for-bit or terrain diverges; this is logic, ported from `.class` |
| Noise primitives (Improved/Perlin/Normal/Blended) | Pure Go port (hand) | — | The exact math (gradients, octave amplitudes, lerp) is ported from `synth/*.class`; unforgiving |
| Density-function graph (the wiring) | **DATA — offline extract + runtime parse** | `tools/` codegen | The whole overworld router is JSON in the jar; extract offline, evaluate at runtime — do NOT hand-type 100 KB of splines |
| Density-function node *types* (Add, Mul, Spline, Noise, …) | Pure Go port (hand) | — | ~25 concrete node evaluators ported from `DensityFunctions$*.class`; small, exact |
| Per-cell sample + interpolation | Pure Go port (hand) | — | `NoiseChunk` coarse-grid sample + trilerp is how vanilla keeps the graph cheap; port the cell loop |
| Surface layer (grass/dirt/stone/water) | Pure Go port (hand, subset) | DATA (surface_rule JSON) | Minimal subset of `SurfaceSystem`/`SurfaceRules` for the top few blocks |
| Biome source (climate → biome) | Pure Go port (hand) OR single-biome stub | DATA (biome climate params baked in `OverworldBiomes.class`) | Surface rules + block choice need a biome; T0 may stub single-biome, T0+ ports multi-noise `Climate` |
| Chunk fill (`SetBlock` into paletted sections) | **REUSE** `level.Chunk`/`Section` | — | Already built + capture-diff-sealed (Phase 4); the generator only fills it |
| Off-tick execution | **REUSE** `world.Worker` + `Generator` seam | — | Worldgen is already async/off-tick; PARITY-01 is purely "a better `Generate`" |

## User Constraints

No `CONTEXT.md` exists for this phase yet (this research precedes discuss/plan). The binding constraints come from CLAUDE.md, STATE.md accumulated context, REQUIREMENTS, and the user's standing mandate stated in the objective.

### Locked Decisions (from CLAUDE.md + STATE + the standing mandate)
- **PORT FROM THE JAR — the standing mandate.** Gameplay/world LOGIC is ported DIRECTLY FROM JAVA. The noise primitives + density-function node evaluators + seeding are translated faithfully from the unobfuscated `net.minecraft.world.level.levelgen` classes (the jar at `temp/cache/26.2-inner.jar`, javap-able). Translate the algorithm into idiomatic Go — **no GPL/Mojang code paste**, cite the bytecode/source. `[CITED: STATE.md Phase 6/7 mandate, extended to worldgen by the objective; CLAUDE.md "Custom Mojang-noise port — you write it"]`
- **NO off-the-shelf noise library for parity.** `ojrac/opensimplex-go` is archived, original-OpenSimplex-only, and **cannot match vanilla** (verified again). It is MVP-stub-only and not relevant to PARITY-01. `[CITED: CLAUDE.md "What NOT to Use", verified this session]`
- **PURE GO, no cgo, no JVM at runtime.** Java 25 is build-time codegen only. Any extracted worldgen DATA is pulled offline (`tools/`) and embedded; the runtime evaluator is pure Go. `[CITED: CLAUDE.md, REQUIREMENTS Out of Scope]`
- **DETERMINISM (REQUIREMENT).** Same seed → same world. The `seed → RandomSource → noise` chain must be deterministic and reproducible. `[CITED: WORLD-04 "deterministic", REQUIREMENTS]`
- **THE GENERATOR SEAM IS FIXED.** A new generator implements the existing `world.Generator` interface (`Generate(pos level.ChunkPos) *level.Chunk`) and is swapped in `cmd/sulfur/main.go` (`NewSuperflat` → the new constructor). The off-tick worker (`world/worker.go`) already runs `Generate` off the tick — **no concurrency/seam work**. `[VERIFIED: world/generator.go:20, cmd/sulfur/main.go:123-126]`

### Claude's Discretion
- The **scope tier** (T0 faithful-surface vs T1 +caves/aquifers vs T2 byte-identical) — this research RECOMMENDS T0; the discuss phase confirms with the user.
- Whether the biome source is a **single-biome stub** (plains everywhere, simplest first cut) or a **ported multi-noise `Climate` source** (real biome diversity). Recommend: single-biome or a 2-3 biome climate stub for T0; full multi-noise is T0+.
- The interpolation cell size (vanilla overworld uses `size_horizontal: 1` → 4-block cells, `size_vertical: 2` → 8-block cells `[VERIFIED: noise_settings/overworld.json noise block]`); match vanilla for parity.
- The surface-rule subset — hardcode the top grass/dirt/stone for T0, or parse the `surface_rule` JSON sequence for fidelity.

### Deferred Ideas (OUT OF SCOPE for PARITY-01 / this phase's plans)
- **ONLINE-01, ONLINE-02** (Mojang auth + protocol encryption) — separate v2 requirement, not researched here, not planned now.
- **REGION-01** (Folia regionization) — separate v2 requirement, not researched here.
- **Caves & carvers** (noise caves: `entrances`/`noodle`/`pillars`/`spaghetti`; the `WorldCarver` ravines/caverns) — the JSON exists (`density_function/overworld/caves/*.json`) but DEFER to a later tier.
- **Full aquifers** (`Aquifer$NoiseBasedAquifer` — perched water tables, fluid-level noise) — sea-level water only for T0; defer the aquifer system.
- **Ore veins** (`vein_toggle`/`vein_ridged`/`vein_gap` router nodes) — DEFER.
- **Structures & `Beardifier`** (structure terrain adaptation) — DEFER hard.
- **Features** (trees, ores, vegetation `PlacedFeature`/`ConfiguredFeature`) — DEFER; T0 is bare terrain.
- **Nether/End generation** — overworld only.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PARITY-01 | Mojang density-function world generation (improved-Perlin/OctaveSimplex) producing terrain with vanilla parity | `## Java Sources To Port` (the exact port order); `## Scope Tiers` (T0 recommended); `## Codegen Question` (the graph is DATA — extract + parse, don't hand-type); `## Architecture Patterns` (data-driven evaluator + per-cell interpolation + seed chain); `## Validation Strategy` (real-client visual + optional seed-heightmap capture-diff) |

## Standard Stack

### Reused (NO new runtime dependency for parity)
| Component | Where | Purpose | Why Reuse |
|-----------|-------|---------|-----------|
| `world.Generator` interface | `world/generator.go:20` | The swap seam — a new `NoiseGenerator` implements `Generate(pos) *level.Chunk` | Already the contract; `Superflat` is the reference impl; the worker already calls it off-tick `[VERIFIED]` |
| `world.Worker` (off-tick) | `world/worker.go` | Runs `Generate` off the tick with singleflight dedup, rejoins via `chunkReady` | Worldgen is ALREADY async — PARITY-01 adds zero concurrency `[VERIFIED]` |
| `level.Chunk`/`Section`/`SetBlock` | `level/chunk.go`, `level/palette.go` | The paletted container the generator fills; capture-diff-sealed vs vanilla in Phase 4 | Wire-correct already; the generator only writes blocks/biomes/heightmaps `[VERIFIED: STATE.md 04-04]` |
| `block.ToStateID[block.X{}]` | `level/block/blocks.go` | Resolve `Stone`/`Dirt`/`GrassBlock`/`Water`/`Deepslate`/`Bedrock` state ids | All needed surface blocks exist as Go structs `[VERIFIED: blocks.go:7,14,17,68,3919]` |
| `level/biome` registry | `level/biome/list.go` | Biome `Type` ids (plains etc.) for the biome containers | `Superflat` already uses `plains`; the noise generator needs a biome source feeding these ids `[VERIFIED]` |
| `tools/` codegen pipeline | `tools/` (separate `go.mod`) | Extend with a worldgen-data extractor (the NEW codegen step) | The sanctioned offline-extract path; already extracts blocks/biomes/registries `[VERIFIED: tools/ listing]` |

### Written from scratch (the ONE bespoke deliverable — the Mojang noise port)
| Component | New file(s) (suggested) | Ported from (jar `.class`) |
|-----------|------------------------|----------------------------|
| Random source + seeding | `world/levelgen/random.go` | `XoroshiroRandomSource`, `RandomSupport`, `PositionalRandomFactory` |
| Noise primitives | `world/levelgen/synth/*.go` | `ImprovedNoise`, `PerlinNoise`, `NormalNoise`, `BlendedNoise`, `NoiseUtils` |
| Density-function evaluator + node types | `world/levelgen/density/*.go` | `DensityFunction`, `DensityFunctions$*` (~25 node types) |
| Graph loader (parse extracted JSON) | `world/levelgen/router.go` | `NoiseRouter`, `NoiseGeneratorSettings`, `NoiseSettings`, `Noises` |
| Per-cell sampler + interpolation | `world/levelgen/noisechunk.go` | `NoiseChunk` (coarse-grid sample + trilerp) |
| Surface layer | `world/levelgen/surface.go` | `SurfaceSystem` + a `SurfaceRules` subset |
| Biome source (T0: stub; T0+: ported) | `world/levelgen/biome.go` | `Climate`, `MultiNoiseBiomeSource` (or a single-biome stub) |
| The `Generator` impl | `world/noisegen.go` | (assembly — implements `world.Generator`) |

### Alternatives Considered (rejected)
| Instead of | Could Use | Why Rejected |
|------------|-----------|--------------|
| Hand-ported Mojang noise | `ojrac/opensimplex-go`, `KdotJPG/OpenSimplex2`, any Perlin lib | **None reproduce vanilla terrain** — different algorithm + different seeding → different world. Verified in CLAUDE.md; non-negotiable for PARITY-01. `[CITED]` |
| Hand-transcribing the density graph into Go | A big Go literal of the overworld router | 100+ KB of spline/router JSON; transcription-error-prone, un-maintainable across version bumps. The jar ships it as DATA — parse it. `[VERIFIED: jar listing]` |
| A second NBT/JSON lib | stdlib `encoding/json` + the fork's `nbt` | stdlib `encoding/json` parses the worldgen JSON fine; the fork already has NBT. No new dep. `[CITED: CLAUDE.md "a second NBT library"]` |

**Installation:** None. No new runtime dependency. (The offline extractor uses the existing `tools/` Java + Go pipeline.)

**Version verification:** N/A — nothing added to the runtime `go.mod`. The authoritative data source is the pinned 26.2 jar (`temp/cache/26.2-inner.jar`, sha1-gated by the existing `tools/download.go`).

## Java Sources To Port

> **THE load-bearing section.** Port order is **dependency order** — each tier depends on the one above. A wrong primitive at the top corrupts everything below it. All classes verified present in `temp/cache/26.2-inner.jar` this session via `unzip -l` and `javap`.

### Tier A — Random source & seeding (the parity hinge — port FIRST, test FIRST)

| Class (jar) | Port | What to extract / match | Notes |
|-------------|------|--------------------------|-------|
| `util.RandomSource` (interface) | the Go interface | `nextInt/nextLong/nextDouble/nextGaussian/consumeCount/fork/forkPositional` surface | `[VERIFIED: javap RandomSource.class — 2525 bytes]` |
| `levelgen.XoroshiroRandomSource` | **exact** | The xoroshiro128++ state + `setSeed(long)`, `nextLong`, `nextBits`, ctor `(long)` and `(long,long)`, `forkPositional` | 26.2 uses Xoroshiro (NOT legacy): `noise_settings/overworld.json` `legacy_random_source: false` `[VERIFIED]`. Methods confirmed via `javap` |
| `levelgen.RandomSupport` | **exact** | The seed mixing (`mixStafford13`, the 128-bit seed upgrade, `seedSlimUuid`) — this is how a world seed + a noise name → the noise's seed | The seed→per-noise mapping lives here; a wrong mix = wrong terrain `[VERIFIED: RandomSupport.class 3218 bytes]` |
| `PositionalRandomFactory` (interface) + Xoroshiro impl | **exact** | `at(x,y,z)`, `fromHashOf(name)`, `fromSeed` — used to seed each `NormalNoise`/`PerlinNoise` from the router | The router seeds every density-function `Noise` node through this factory |
| `levelgen.LegacyRandomSource` | **only if needed** | The pre-1.18 LCG. Overworld 26.2 does not use it (`legacy_random_source:false`); some legacy noises (`BlendedNoise` internal) may. Port only the subset `BlendedNoise` needs. | DEFER unless `BlendedNoise` requires it |

**Why FIRST:** Tier A is the determinism + parity foundation. Write a unit test that reproduces a few known `Xoroshiro` outputs for a fixed seed *before* touching noise. If Tier A is wrong, every higher tier produces a different (but plausible-looking) world, and you will not notice until a seed-heightmap diff fails.

### Tier B — Noise primitives (port SECOND)

| Class (jar) | Port | What to extract / match |
|-------------|------|--------------------------|
| `synth.ImprovedNoise` | **exact** | The single-octave improved-Perlin: gradient table, `noise(x,y,z)`, the `gradDot`/fade-curve math. `[VERIFIED: ImprovedNoise.class 5892 bytes]` |
| `synth.PerlinNoise` | **exact** | The octave stack over `ImprovedNoise`: `firstOctave`, `amplitudes[]`, `lowestFreqValueFactor`/`lowestFreqInputFactor`, `getValue`, the `wrap`/`maxValue`. Seeded via the `RandomSource`/`PositionalRandomFactory`. `[VERIFIED: 11281 bytes]` |
| `synth.NormalNoise` | **exact** | The 2-`PerlinNoise` (first + second) normalized noise that density-function `Noise` nodes actually call: `create(RandomSource, NoiseParameters)`, `getValue(x,y,z)`, the `INPUT_FACTOR`/`TARGET_DEVIATION`/`expectedDeviation` constants, `valueFactor`. `[VERIFIED: javap NormalNoise.class — create(RandomSource,int,double...), getValue, expectedDeviation]` |
| `synth.NormalNoise$NoiseParameters` | **exact** (or DATA) | `firstOctave` + `amplitudes[]` — this is the `noise/*.json` content (e.g. `aquifer_barrier.json` = `firstOctave -3, amplitudes [1.0]`). Extract as DATA. `[VERIFIED: noise/*.json]` |
| `synth.BlendedNoise` | **exact** | The legacy "main"/old terrain noise the overworld `base_3d_noise` uses (`old_blended_noise` node: `xz_scale 0.25, y_scale 0.125, xz_factor 80, y_factor 160, smear_scale_multiplier 8`). `[VERIFIED: BlendedNoise.class 9584 bytes; overworld/base_3d_noise.json]` |
| `synth.NoiseUtils` | **exact** | Small helpers (`biasTowardsExtreme`, sampling) used by some nodes. `[VERIFIED: 1602 bytes]` |
| `synth.SimplexNoise` / `PerlinSimplexNoise` | **DEFER** | Used by legacy/feature noise, not the overworld density router. Skip for T0. |

### Tier C — Density-function node types (port THIRD)

The graph is data, but the **node evaluators** are logic. Port the concrete `DensityFunctions$*` classes — each is a small pure `compute(context) → double`. `[VERIFIED: DensityFunctions.class 25056 bytes + ~40 nested `$` classes]`

**Required for the overworld `final_density` path (the T0 minimum set):**

| Node type (JSON `type`) | Java class | Role |
|--------------------------|-----------|------|
| `constant` (bare number) | `DensityFunctions$Constant` | Leaf constant (`zero.json` = `0.0`) |
| `y_clamped_gradient` | (in `DensityFunctions`) | Y-based ramp (`y.json`, `depth.json` arg) `[VERIFIED: y.json]` |
| `noise` | `DensityFunctions$Noise` | Sample a `NormalNoise` at scaled coords `[VERIFIED]` |
| `shifted_noise` | `DensityFunctions$ShiftedNoise` | Noise with x/y/z shift inputs (continents/erosion) |
| `shift_a`/`shift_b`/`shift` | `DensityFunctions$ShiftA/ShiftB/Shift` | Domain-warp shift offsets (`shift_x.json`/`shift_z.json`) |
| `add`/`mul`/`min`/`max` | `DensityFunctions$Ap2` / `MulOrAdd` / `TwoArgumentSimpleFunction` | Binary ops (`depth.json` is `add`) `[VERIFIED]` |
| `interpolated` | `DensityFunctions$Marker` (Interpolated) | Marks a node for cell interpolation in `NoiseChunk` |
| `flat_cache` / `cache_2d` / `cache_once` / `cache_all_in_cell` | `DensityFunctions$Marker` | Caching markers — for T0 you may treat as pass-through (slower but correct); port real caching for perf |
| `clamp` | `DensityFunctions$Clamp` | Clamp to [min,max] |
| `squeeze` | `DensityFunctions$Mapped` (Squeeze) | The `final_density` squeeze curve `[VERIFIED: final_density uses squeeze]` |
| `spline` | `DensityFunctions$Spline` + `Spline`/`Coordinate`/`Point` | Cubic spline (continents/erosion/ridges → offset/factor/jaggedness) — the big `offset.json`/`factor.json` are spline-heavy |
| `range_choice` | `DensityFunctions$RangeChoice` | Pick branch by input range |
| `old_blended_noise` | `DensityFunctions$BlendedNoise` wrapper | Wraps `BlendedNoise` (`base_3d_noise`) `[VERIFIED]` |
| `blend_density` / `blend_alpha` / `blend_offset` | `DensityFunctions$BlendDensity/BlendAlpha/BlendOffset` | Blending (used in `final_density`); for T0 with blending disabled they pass through `[VERIFIED: final_density uses blend_density]` |

**Defer (cave/aquifer/end-only nodes):** `weird_scaled_sampler`, `noodle`/`spaghetti` helpers, `end_islands`, `beardifier`/`marker` for structures, `interpolated` cave nodes. They appear only in the deferred `caves/*` and aquifer branches.

### Tier D — The router / settings loader (port FOURTH — mostly a DATA parser)

| Class (jar) | Port | Role |
|-------------|------|------|
| `levelgen.NoiseGeneratorSettings` | as a parsed struct | The top-level `noise_settings/overworld.json`: `noise{height,min_y,size_h,size_v}`, `default_block`, `default_fluid`, `sea_level`, `noise_router`, `surface_rule`, `aquifers_enabled`, `legacy_random_source` `[VERIFIED: overworld.json keys]` |
| `levelgen.NoiseRouter` | as a parsed struct | The named density functions: `final_density` (the one T0 needs), plus `continents/erosion/depth/ridges/temperature/vegetation/preliminary_surface_level/barrier/fluid_level_*/lava/vein_*` `[VERIFIED: noise_router keys]` |
| `levelgen.NoiseSettings` | as a parsed struct | Cell dims: `height 384, min_y -64, size_horizontal 1 (→4-block cells), size_vertical 2 (→8-block cells)` `[VERIFIED]` |
| `levelgen.Noises` | DATA registry | The named noise-parameter registry (`noise/*.json`) — maps `minecraft:temperature` → `firstOctave/amplitudes` `[VERIFIED]` |
| `levelgen.RandomState` | **exact** | Holds the per-world `PositionalRandomFactory` and instantiates every router noise from the world seed — **the bridge from Tier A seeding to the graph**. `[VERIFIED: RandomState.class 9806 bytes]` |
| `levelgen.DensityFunction$Visitor`/`mapAll` | **exact** | The graph-walk that binds noise nodes to seeded noises + applies interpolation markers. Needed to instantiate the parsed graph against `RandomState`. |

### Tier E — Terrain assembly (port FIFTH)

| Class (jar) | Port | Role / T0 decision |
|-------------|------|--------------------|
| `levelgen.NoiseChunk` | **exact (core loop)** | Samples `final_density` on the coarse cell grid (4×8×4) and **trilinearly interpolates** between cells — this is HOW vanilla makes the graph cheap. Port the cell loop + interpolation. `[VERIFIED: NoiseChunk.class 22874 bytes]` |
| `NoiseBasedChunkGenerator.doFill`/`iterateNoiseColumn` | **port the fill loop** | For each cell, for each block: `final_density > 0 ? default_block : (y < sea_level ? water : air)`. This is the solid/air/water placement. |
| `levelgen.SurfaceSystem` | **subset** | Applies the surface (grass on top, dirt below, stone deep). T0: a minimal hardcoded top-layer OR a small `SurfaceRules` subset. |
| `SurfaceRules` (in `levelgen`) | **subset / DATA** | The `surface_rule` JSON is a `sequence` of conditions `[VERIFIED: surface_rule top type = minecraft:sequence]`. T0: port the handful of rules that put grass/dirt/sand/stone (skip the long badlands/biome-specific tail). |
| `levelgen.Aquifer` + `$NoiseBasedAquifer` | **DEFER** | T0 uses flat sea-level water (`y < 63 → water`). The full perched-aquifer system is T1. `[VERIFIED: Aquifer.class present]` |
| `levelgen.Beardifier` | **DEFER hard** | Structure terrain adaptation — no structures in T0. `[VERIFIED: present]` |

### Tier F — Biome source (port SIXTH — or stub for T0)

| Class (jar) | Port | T0 decision |
|-------------|------|-------------|
| `biome.Climate` + `Climate$Sampler`/`TargetPoint`/`ParameterList` | **exact (if multi-noise)** | The 6-D climate (temperature/humidity/continentalness/erosion/depth/weirdness) → nearest biome. `[VERIFIED: Climate.class 4958 bytes]` |
| `biome.MultiNoiseBiomeSource` | **exact (if multi-noise)** | Maps a climate sample to a biome via the parameter list. `[VERIFIED: 10836 bytes]` |
| `data.worldgen.biome.OverworldBiomes` | DATA (baked) | The overworld biome climate parameters are **hardcoded in this class** (32 KB), not loose JSON — `multi_noise_biome_source_parameter_list/overworld.json` is just `{"preset":"minecraft:overworld"}` `[VERIFIED]`. To port multi-noise, extract these parameters via a Java extractor (codegen) since they are code, not data. |

**T0 recommendation for biomes:** start with a **single-biome stub** (plains everywhere, like `Superflat` does) so the surface rules have a biome to key on and the chunk biome containers are valid. The terrain *shape* (the part PARITY-01 is really about) comes entirely from `final_density` and is biome-independent at T0. Add the multi-noise `Climate` source as the first T0+ increment once the surface renders.

## Scope Tiers

> **The crux. PARITY-01 has tiers of "parity." Recommend T0 for v2; be explicit about what each tier shows a real client and what it defers.**

### T0 — Faithful overworld surface (✅ RECOMMENDED for this phase)
**What a real 26.2 client sees:** Recognizable Minecraft terrain. Rolling hills, valleys, plains, mountains, and oceans/lakes filled with water at sea level (y=63). The *shape* matches vanilla's terrain character because it comes from the real `final_density` graph fed by the real seed-matched noises. Solid ground, walkable, lit, renders without void.
**Includes:**
- Tier A (Xoroshiro seeding) + Tier B (all 4 noise primitives) + Tier C (the ~20 node types on the `final_density` path) + Tier D (router/settings parsed from extracted JSON + `RandomState`) + Tier E core (`NoiseChunk` sample+interp, the solid/air/water fill, a minimal surface layer) + Tier F stub (single biome, or a 2-3 biome climate stub).
- `default_block: stone`, `default_fluid: water`, `sea_level: 63`, deepslate below the stone transition (cheap y-threshold), bedrock floor.
**Defers:** caves, aquifers (beyond flat sea water), ore veins, structures, features (trees/ores), full biome diversity.
**Parity meaning:** *recognizable-faithful* — the terrain looks like Minecraft and uses vanilla's exact graph + noise math, but is NOT guaranteed byte-identical to a vanilla server for a given seed (because cave-carving, aquifer water placement, and surface-rule edge cases are simplified/deferred). **This is achievable and is the right v2 target.**

### T1 — + Caves, aquifers, ore veins (a later increment, NOT this phase)
**Adds:** the `caves/*` density-function branches (noise caves: entrances/noodle/pillars/spaghetti) into `final_density`, the `Aquifer$NoiseBasedAquifer` (perched water tables, fluid-level noise), the `vein_*` ore-vein nodes, the `WorldCarver` ravines/caverns.
**What a client sees:** terrain with caves you can explore, underground water/lava pockets, ore-vein regions.
**Parity meaning:** much closer to byte-identical, but still not guaranteed (features/structures absent).

### T2 — Byte-identical-to-vanilla-for-a-seed (GOLD — likely beyond v2)
**Requires:** EVERY primitive bit-exact (especially Tier A seeding), the FULL density graph (all router nodes incl. caves/veins), the FULL surface-rule sequence, full aquifers, and full multi-noise biomes — such that, for a fixed seed, Sulfur's generated chunk column heightmaps + block columns match a real vanilla 26.2 server **byte-for-byte** (before features/structures, which are a separate decoration pass).
**Parity meaning:** the gold standard. Achievable in principle because everything is ported from the same jar + the same JSON, but it is a **tier of effort beyond T0** — it requires the seed-heightmap capture-diff (below) to pass exactly, which surfaces every subtle off-by-one in the noise math. **Recommend NOT committing this phase to T2; treat byte-identical as a validation *goal* to measure against, achieved incrementally.**

**Honest recommendation:** Plan **T0**. Frame the success criterion as *"a real vanilla 26.2 client renders recognizable, vanilla-character overworld terrain (hills/plains/oceans, water at sea level) generated by the real ported density-function graph behind the unchanged Generator seam."* Treat byte-identical (T2) as the aspirational validation target the seed-heightmap diff measures, not the phase gate.

## Architecture Patterns

### System Architecture Diagram — the worldgen pipeline (all behind the unchanged seam)

```
  OFFLINE (codegen, build-time, Java + Go in tools/) — runs ONCE per version
  ┌──────────────────────────────────────────────────────────────────────────┐
  │ 26.2-inner.jar                                                             │
  │   data/.../worldgen/noise_settings/overworld.json  (wired NoiseRouter)     │
  │   data/.../worldgen/density_function/**.json       (35 graph nodes)        │
  │   data/.../worldgen/noise/**.json                  (octave params)         │
  │   OverworldBiomes.class                            (biome climate params)  │──┐
  │        │ extract (unzip JSON) + a Java extractor for the baked biome params│  │
  │        ▼                                                                    │  │
  │   embed into the runtime (//go:embed the JSON, or generate Go from it)     │  │
  └──────────────────────────────────────────────────────────────────────────┘  │
                                   │ embedded DATA                                │
  ───────────────────────────────────────────────────────────────────────────── │
  RUNTIME (pure Go, off-tick worker)                                             │
  ┌──────────────────────────────────────────────────────────────────────────┐  │
  │ NewNoiseGenerator(seed, dimSettings):                                      │  │
  │   1. parse noise_settings + density_function JSON (stdlib encoding/json)   │◄─┘
  │   2. RandomState{ seed → PositionalRandomFactory (Xoroshiro) }   [Tier A]  │
  │   3. instantiate each `noise` node's NormalNoise from RandomState [Tier B/D]│
  │   4. bind the parsed graph → an evaluable DensityFunction tree    [Tier C] │
  │                                                                            │
  │ Generate(pos ChunkPos) *level.Chunk:   ── called OFF-TICK by world.Worker ─│
  │   NoiseChunk: for each 4×8×4 cell in the 16×384×16 column:        [Tier E] │
  │       sample final_density at the 8 cell corners                          │
  │       trilerp to each block in the cell:                                  │
  │           density > 0      → default_block (stone)                         │
  │           else y < 63      → default_fluid  (water)                        │
  │           else             → air                                          │
  │   SurfaceSystem: top stone→grass, below→dirt, beaches→sand     [Tier E,F] │
  │   biome source: climate sample → biome id (or single-biome stub) [Tier F] │
  │   fill level.Chunk sections via SetBlock + biome containers + heightmaps   │
  │        │ returns *level.Chunk (REUSE — Phase-4 paletted container)         │
  └──────────────────────────────────────────────────────────────────────────┘
        │ the worker rejoins the tick via the UNCHANGED chunkReady seam (Phase 4)
        ▼
   client renders recognizable terrain
```

### Recommended Project Structure (additive — new sub-package, existing seam)
```
world/
├── generator.go        # UNCHANGED interface; Superflat stays (keep as a fallback/test gen)
├── noisegen.go         # NEW: NoiseGenerator implements world.Generator (assembly)
├── worker.go           # UNCHANGED (already runs Generate off-tick)
└── levelgen/           # NEW sub-package: the ported Mojang noise stack (pure Go)
    ├── random.go       # Tier A: Xoroshiro + RandomSupport + PositionalRandomFactory
    ├── synth/          # Tier B: ImprovedNoise, PerlinNoise, NormalNoise, BlendedNoise
    ├── density/        # Tier C: the DensityFunction node evaluators
    ├── router.go       # Tier D: parse noise_settings/density_function JSON → graph + RandomState
    ├── noisechunk.go   # Tier E: cell sampler + trilerp + solid/air/water fill
    ├── surface.go      # Tier E/F: minimal surface rules
    ├── biome.go        # Tier F: single-biome stub or ported Climate/MultiNoise
    └── data/           # //go:embed the extracted worldgen JSON (the graph as DATA)

tools/
├── ExtractWorldgen (NEW): unzip the worldgen JSON + a Java extractor for OverworldBiomes params
```

### Pattern 1: The data-driven density-function evaluator (NOT a hand-typed graph)
**What:** Parse the vanilla `density_function`/`noise_settings` JSON into a tree of typed nodes; each node implements `Compute(ctx) float64`. The *types* are ported logic; the *tree shape* is parsed data.
**When:** This is the core of T0. The 60 KB `offset.json` and 34 KB `factor.json` are splines — you parse them, you do not type them.
**Example (the node interface + two ported nodes):**
```go
// world/levelgen/density/df.go — Source: net.minecraft.world.level.levelgen.DensityFunction (javap)
type Context struct{ X, Y, Z int }      // block coords (NoiseChunk supplies these)
type Function interface{ Compute(c Context) float64 ; MinValue() float64 ; MaxValue() float64 }

// "minecraft:add" — Source: DensityFunctions$Ap2 (ADD)
type add struct{ a, b Function }
func (f add) Compute(c Context) float64 { return f.a.Compute(c) + f.b.Compute(c) }

// "minecraft:y_clamped_gradient" — Source: DensityFunctions.yClampedGradient
type yClampedGradient struct{ fromY, toY int; fromV, toV float64 }
func (f yClampedGradient) Compute(c Context) float64 {
    return clampedLerp(f.fromV, f.toV, invLerp(float64(c.Y), float64(f.fromY), float64(f.toY)))
}
```
A `parse(json) Function` dispatches on the `"type"` field; string args (`"minecraft:overworld/offset"`) resolve to other parsed density_function files (a registry), exactly like Mojang's `DensityFunctions.HolderHolder`.

### Pattern 2: `NoiseChunk` cell interpolation (the performance pattern — port it, don't skip it)
**What:** Do NOT evaluate `final_density` for all 16×384×16 = 98 304 blocks per chunk. Vanilla samples on a coarse grid — `size_horizontal:1` → cells of 4 blocks wide, `size_vertical:2` → 8 blocks tall `[VERIFIED: noise_settings noise block]` — so ~4×48×4 ≈ 768 samples per chunk, then **trilinearly interpolates** between the 8 corners of each cell.
**Why:** Sampling the full graph (splines + multiple `NormalNoise`) per-block is ~100× more expensive. The interpolation is not just an optimization — it is part of vanilla's terrain *character* (the smooth cell-lerp is visible), so matching it is also a parity requirement.
**Anti-pattern:** Per-block density evaluation. Correct shape but catastrophically slow off-tick AND subtly non-vanilla-looking.

### Pattern 3: Seed → noise determinism chain (test it in isolation first)
**What:** `seed → XoroshiroRandomSource → forkPositional() → fromHashOf("minecraft:temperature") → NormalNoise(params)`. Each named noise is seeded by hashing its registry name through the positional factory.
**When:** Tier A + the start of Tier D. **Unit-test the `Xoroshiro` outputs against known vectors BEFORE building anything on top.**
**Anti-pattern:** Seeding noises from `math/rand` or a hand-rolled LCG — the seed mapping won't match and terrain diverges silently.

### Anti-Patterns to Avoid
- **Hand-transcribing the density graph** — it is DATA in the jar; parse it (Codegen Question below).
- **Using any off-the-shelf noise lib for parity** — verified impossible (CLAUDE.md).
- **Per-block density evaluation** — port `NoiseChunk`'s cell interpolation.
- **Building T1/T2 (caves/aquifers/structures/features) in this phase** — scope creep; T0 is the deliverable.
- **Touching the worker / tick / Generator interface** — worldgen is already off-tick; only `Generate`'s body changes.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| The overworld terrain graph wiring | A giant Go literal of the router/splines | **Extract + `//go:embed` the vanilla `noise_settings`/`density_function` JSON**; a parser builds the tree | 120 KB router + 35 node files incl. 60 KB splines — transcription is error-prone and un-versionable. The jar ships it as DATA `[VERIFIED]` |
| Noise octave parameters | Hand-typed amplitude arrays | Parse the `noise/*.json` (`firstOctave`+`amplitudes`) | They're DATA too (`temperature.json` etc.) `[VERIFIED]` |
| Off-tick execution / rejoin | A new worker or goroutine | The existing `world.Worker` + `chunkReady` seam | Worldgen is already async since Phase 4; zero new concurrency `[VERIFIED]` |
| Paletted chunk encoding | A new chunk container | `level.Chunk`/`Section`/`SetBlock` | Capture-diff-sealed vs vanilla in Phase 4 `[VERIFIED: STATE.md 04-04]` |
| Block state ids | Hardcoded ints | `block.ToStateID[block.Stone{}]` etc. | Generated, authoritative `[VERIFIED]` |
| JSON parsing | A new parser | stdlib `encoding/json` | The worldgen JSON is plain JSON; no new dep `[CITED: CLAUDE.md "a second NBT library"]` |
| The jar extraction | A new download/unzip flow | The existing `tools/` pipeline (`download.go` sha1-gated, the Java+Go extractors) | Already the sanctioned offline path `[VERIFIED: tools/ listing]` |

**Key insight:** PARITY-01 is *port the ~15 noise/random primitives + ~25 density-node evaluators (logic, from `.class`), then DATA-DRIVE the wiring (parse the JSON the jar already ships).* The bespoke effort is the primitive math + the cell sampler. Everything that connects them is data, and everything downstream of `*level.Chunk` (encoding, lighting, streaming, off-tick execution) was paid for in Phases 4-8. Do not re-build any of it.

## Codegen Question

**Does `tools/` already extract `worldgen/noise_settings` + `worldgen/density_function` + `worldgen/biome`? — NO.** `[VERIFIED: grep across tools/ — only gen_registryid.go mentions worldgen, and only to map registry *ids*, not to extract the data files.]`

**What the jar contains (all verified this session by `unzip -l temp/cache/26.2-inner.jar`):**
- `data/minecraft/worldgen/noise_settings/overworld.json` — 120 KB, the **complete wired router** (`final_density`, `sea_level:63`, `default_block:stone`, `default_fluid:water`, `legacy_random_source:false`, `noise{height:384,min_y:-64,size_h:1,size_v:2}`, `surface_rule` sequence). `[VERIFIED]`
- `data/minecraft/worldgen/density_function/**.json` — 35 files (the named graph nodes; `overworld/offset.json` 60 KB, `factor.json` 34 KB, `jaggedness.json` 12 KB, plus small ones like `y.json`, `depth.json`, `zero.json`). `[VERIFIED]`
- `data/minecraft/worldgen/noise/**.json` — the `NormalNoise$NoiseParameters` (`firstOctave`+`amplitudes`) for each named noise. `[VERIFIED]`
- The overworld **biome climate parameters** are **baked into `OverworldBiomes.class`** (32 KB), NOT loose JSON — the loose `multi_noise_biome_source_parameter_list/overworld.json` is just `{"preset":"minecraft:overworld"}`. `[VERIFIED]`

**The extraction step (NEW codegen work, a sub-task of this phase):**
1. **For the graph (the bulk):** a pure-unzip step — copy the `worldgen/noise_settings/`, `worldgen/density_function/`, `worldgen/noise/` JSON trees out of `26.2-inner.jar` into `world/levelgen/data/` and `//go:embed` them. No Java needed; it's already JSON. (Mirror how Phase 2 embedded the registry NBT via `//go:embed`.)
2. **For the biome climate params (only if T0+ multi-noise):** a small Java extractor (like the existing `GenBiomes.java`) that reflects over `OverworldBiomes`/`MultiNoiseBiomeSource` to dump the parameter list (temperature/humidity/continentalness/erosion/depth/weirdness ranges → biome) as JSON. DEFER if T0 uses a single-biome stub.

**Decision:** **DATA-DRIVEN graph, hand-ported primitives.** Extract (unzip + embed) the worldgen JSON; the runtime parses it into the ported node tree. This is the only sane approach — the alternative (hand-typing 100+ KB of splines) is explicitly an anti-pattern.

## Common Pitfalls

### Pitfall 1: Wrong RandomSource seeding (Xoroshiro vs Legacy) → a different (but plausible) world
**What goes wrong:** Seed the noises with the legacy LCG, or get the `RandomSupport` seed-mix wrong, and the terrain *looks* like Minecraft but is a completely different world than vanilla for that seed — and you won't notice until a byte-diff fails.
**Why it happens:** 26.2 overworld uses `Xoroshiro` (`legacy_random_source:false` `[VERIFIED]`), but legacy code/tutorials reference the old LCG; the positional `fromHashOf(name)` mixing is subtle.
**How to avoid:** Port `XoroshiroRandomSource` + `RandomSupport` + `PositionalRandomFactory` EXACTLY (Tier A first), and **unit-test against known `Xoroshiro` output vectors before building anything on top.** Carry the noise's registry name into its seed via the positional factory exactly as `RandomState` does.
**Warning signs:** Terrain renders but doesn't match a vanilla server for a shared seed; the seed-heightmap diff is off everywhere (not just at edges).

### Pitfall 2: Octave amplitude / firstOctave / lacunarity mismatch → wrong terrain scale
**What goes wrong:** Off-by-one on `firstOctave`, a missing amplitude, or wrong `lowestFreqInputFactor`/`lowestFreqValueFactor` in `PerlinNoise` → hills are the wrong size/frequency.
**Why it happens:** The octave math has several constants; `firstOctave` is often negative (e.g. `temperature` = `-10` `[VERIFIED]`).
**How to avoid:** Parse `firstOctave`+`amplitudes` from the `noise/*.json` (don't hardcode), and port `PerlinNoise.getValue` / `NormalNoise.expectedDeviation` constant-for-constant from `synth/*.class`.
**Warning signs:** Terrain has the right *character* but wrong *scale* (too spiky / too smooth / too tall).

### Pitfall 3: Density-function evaluation order / interpolation marker handling
**What goes wrong:** Evaluating `interpolated`/`flat_cache`/`cache_*` markers as no-ops *changes results* if you also skip the cell-grid sampling they imply; or evaluating spline coordinates in the wrong order.
**Why it happens:** The `Marker` nodes (`interpolated`, caches) are not pure pass-throughs in vanilla — `interpolated` specifically means "sample me on the cell grid and lerp," which `NoiseChunk` orchestrates.
**How to avoid:** Port `NoiseChunk`'s cell loop so `interpolated` nodes are sampled at cell corners and trilerped; for T0 the other `cache_*` markers can be transparent (correctness preserved, just slower). Port `Spline.compute` (the cubic) exactly.
**Warning signs:** Blocky/stair-stepped terrain (interpolation missing) or smooth-but-wrong (spline math off).

### Pitfall 4: Sea-level water placement
**What goes wrong:** No water in oceans/lakes, or water above terrain.
**Why it happens:** T0 uses a flat sea-level rule instead of the full aquifer system.
**How to avoid:** For T0: `density > 0 → stone; else if y < sea_level(63) → water; else air` `[VERIFIED: sea_level 63, default_fluid water]`. This gives correct oceans/lakes for the surface tier. (Real aquifers — perched water tables underground — are T1.)
**Warning signs:** Dry ocean basins; floating water.

### Pitfall 5: Per-block density evaluation (performance cliff off-tick)
**What goes wrong:** Evaluating the full graph per block (98 K/chunk) makes `Generate` so slow the worker backs up and chunks stream visibly late.
**Why it happens:** Skipping the `NoiseChunk` interpolation as "an optimization for later."
**How to avoid:** Port the cell-grid sample + trilerp from the start (Pattern 2). ~768 samples/chunk, not 98 K.
**Warning signs:** `Generate` taking tens of ms; the worker's bounded queue dropping; chunks popping in slowly.

### Pitfall 6: Heightmap / surface-rule interaction
**What goes wrong:** Wrong heightmap values (client lighting/spawn glitches) or surface rules keyed on a heightmap that isn't computed yet.
**Why it happens:** Surface rules and heightmaps both depend on "the top solid block per column," which you must compute from the density fill before applying surface or writing heightmaps.
**How to avoid:** Two-pass per chunk: (1) fill solid/air/water from density; (2) walk each column top-down to find the surface, apply the surface layer, and write the 3 CLIENT heightmaps (`WorldSurface`/`MotionBlocking`/`MotionBlockingNoLeaves`) — exactly the heightmap discipline `Superflat` already demonstrates `[VERIFIED: generator.go:120-136]`.
**Warning signs:** Mobs spawn in the air / lighting wrong / client desyncs surface height.

### Pitfall 7: Determinism break (REQUIREMENT violation)
**What goes wrong:** Same seed yields different terrain across runs → violates WORLD-04 determinism + breaks the off-tick worker's purity contract (`Generator` must be PURE: same pos → identical bytes `[VERIFIED: generator.go:17-19]`).
**Why it happens:** Using `math/rand` global state, map iteration order, or a non-seeded RNG anywhere in `Generate`.
**How to avoid:** ALL randomness flows from the world seed through the ported `Xoroshiro` positional factory. No `math/rand`, no time-based seeds, no global mutable state in the noise stack. `Generate` reads only the seed + the parsed graph.
**Warning signs:** A chunk regenerates differently; the existing `Generator` purity test (`world/generator_test.go`) fails for the noise gen.

## Code Examples

### The Generator implementation (the swap point)
```go
// world/noisegen.go — NEW. Implements the UNCHANGED world.Generator interface.
// Source: assembly of the ported levelgen stack; mirrors Superflat's seam usage.
type NoiseGenerator struct {
    settings *levelgen.NoiseGeneratorSettings // parsed from embedded overworld.json
    router   *levelgen.Router                  // the bound density-function graph
    state    *levelgen.RandomState             // seed → positional factory → seeded noises
    surface  *levelgen.SurfaceSystem
    biomes   levelgen.BiomeSource              // single-biome stub or multi-noise
    secs, minY int
    stone, water, deepslate, grass, dirt, bedrock block.StateID
}

func NewNoiseGenerator(seed int64, secs, minY int) *NoiseGenerator { /* parse + seed */ }

// Generate is PURE: same pos + same seed → identical *level.Chunk bytes.
func (g *NoiseGenerator) Generate(pos level.ChunkPos) *level.Chunk {
    ch := level.EmptyChunk(g.secs)
    nc := levelgen.NewNoiseChunk(g.router, g.state, pos, g.settings.Noise) // cell sampler
    // pass 1: density → solid/air/water (cell sample + trilerp)
    nc.FillBlocks(ch, g.stone, g.deepslate, g.water, g.bedrock, g.settings.SeaLevel)
    // pass 2: surface layer + heightmaps + biome containers (reuse Superflat's heightmap discipline)
    g.surface.Apply(ch, pos, g.biomes)
    ch.Status = level.StatusFull
    return ch
}
```

### Wiring in main.go (the one-line swap)
```go
// cmd/sulfur/main.go ~line 123 — Source: existing Superflat wiring, swapped.
//   gen := world.NewSuperflat(overworldSecs, overworldMinY, overworldSurfaceY)  // BEFORE
gen := world.NewNoiseGenerator(worldSeed, overworldSecs, overworldMinY)          // AFTER
worker := world.NewWorker(gen, "", workerBuf)  // UNCHANGED — worker is generator-agnostic
```

## State of the Art

| Old Approach | Current (26.2) Approach | When Changed | Impact |
|--------------|-------------------------|--------------|--------|
| Pre-1.18 LCG random + `BlendedNoise`-only terrain | `Xoroshiro` positional random + **density-function graph** + `NormalNoise` | MC 1.18 (Caves & Cliffs pt2) | Terrain is a data-driven graph, not hardcoded noise; `legacy_random_source:false` for overworld `[VERIFIED]` |
| Hardcoded terrain in Java | **Datapack-serialized** `noise_settings`/`density_function` JSON in the jar | MC 1.18.2+ | The whole router is DATA → Sulfur parses it instead of porting wiring `[VERIFIED]` |
| `OpenSimplex`/`Perlin` libraries | Mojang's specific improved-Perlin octave stack | always | Off-the-shelf libs cannot match vanilla `[CITED: CLAUDE.md]` |

**Deprecated/outdated for this phase:** any tutorial predating 1.18 (no density functions); `ojrac/opensimplex-go` (archived, wrong algorithm). Misode's density-function generator (`misode.github.io/worldgen/density-function`) supports 26.2 and is a useful *reference visualizer* for understanding a node graph — not a code source.

## Runtime State Inventory

PARITY-01 is **additive, greenfield code** (a new generator + a new `levelgen` sub-package + an offline extract step). No rename/migration.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — `regionDir` is `""` (always-generate, no persisted world). A noise world is generated fresh each run; nothing to migrate. `[VERIFIED: main.go:124]` | None |
| Live service config | None — single self-contained Go binary | None |
| OS-registered state | None | None |
| Secrets/env vars | `SULFUR_DEBUG` triggers exist; the noise gen must not disturb them | None (debug independent of the generator) |
| Build artifacts | NEW embedded worldgen JSON in `world/levelgen/data/` (extracted offline); a NEW `tools/` extract step. No new `go.mod` runtime dep. | Run the extract step once per version; `//go:embed` the JSON; `go mod tidy` (no new deps expected) |

**Nothing to migrate** — verified the v1 world is always-generated.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | build/test the pure-Go noise stack | ✓ | 1.26.1 (host) | — `[VERIFIED: prior phases]` |
| `temp/cache/26.2-inner.jar` (unobfuscated) | port source (`javap`) + DATA extract (`unzip`) | ✓ | 26.2 | — `[VERIFIED: ls -la temp/cache, 24 MB]` |
| Zulu JDK 25 (`javap`) | reading the levelgen `.class` signatures + (optional) biome-param extractor | ✓ | 25.0.3 | — `[VERIFIED: javap -version]` |
| stdlib `encoding/json` | parse the worldgen JSON at runtime | ✓ | stdlib | — |
| `//go:embed` | embed the extracted graph JSON | ✓ | stdlib (Go ≥1.16) | — |
| Docker `golang:1.26` | `-race`/CI (host `CGO_ENABLED=0`) | ✓ | used Phases 2-8 | — `[VERIFIED: STATE.md]` |
| A reference vanilla 26.2 server | (T2 only) seed-heightmap capture-diff | ✓ (the jar can run a vanilla server) | 26.2 | manual visual check is the fallback |

**Missing dependencies with no fallback:** none. **Missing with fallback:** a running vanilla server for byte-diff (only needed if targeting T2; T0 validates by real-client visual).

## Validation Architecture

> `workflow.nyquist_validation` is enabled `[VERIFIED: config.json]` — this section is included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no external runner) |
| Config file | none — `go test` convention |
| Quick run command | `go test ./world/... ./world/levelgen/...` |
| Full suite command (race) | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./world/...` `[VERIFIED: prior-phase Docker -race pattern]` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PARITY-01 (Tier A) | `Xoroshiro` reproduces known output vectors for a fixed seed | unit (golden vectors) | `go test ./world/levelgen/ -run TestXoroshiro` | ❌ Wave 0 |
| PARITY-01 (Tier B) | `NormalNoise.getValue` matches known noise samples at fixed coords | unit (golden) | `go test ./world/levelgen/synth/ -run TestNormalNoise` | ❌ Wave 0 |
| PARITY-01 (Tier C/D) | The parsed `final_density` graph evaluates a constant/known node correctly | unit | `go test ./world/levelgen/density/ -run TestGraphEval` | ❌ Wave 0 |
| PARITY-01 (determinism) | `Generate(pos)` is PURE — same seed+pos → identical chunk bytes | unit (reuse the Superflat purity test shape) | `go test ./world/ -run TestNoiseGenDeterministic` | ❌ Wave 0 |
| PARITY-01 (surface) | Generated chunk has solid ground, water at y=63, valid heightmaps, valid biome containers | unit + round-trip vs `level.Chunk` encode | `go test ./world/ -run TestNoiseGenChunk` | ❌ Wave 0 |
| PARITY-01 (T2 optional) | A fixed-seed column heightmap matches a real vanilla 26.2 server | capture-diff (manual/golden) | `go test ./world/ -run TestSeedHeightmapVsVanilla` | ❌ optional |
| PARITY-01 (milestone) | A real vanilla 26.2 client renders recognizable terrain | **human-verify (BLOCKING, autonomous:false)** | manual real-client visual check | n/a |

### Sampling Rate
- **Per task commit:** `go build ./... && go test ./world/... ./world/levelgen/...`
- **Per wave merge:** the Docker `-race` `./world/...` suite (`-count=1`) — the noise stack is pure/deterministic so `-race` is cheap insurance.
- **Phase gate:** the BLOCKING real-client visual check (like 04-04 / 05-03) — *"does it look like Minecraft terrain?"* — plus the determinism + chunk-encode tests green.

### Wave 0 Gaps
- [ ] `world/levelgen/random_test.go` — `Xoroshiro` golden vectors (Tier A — test FIRST)
- [ ] `world/levelgen/synth/*_test.go` — noise primitive golden samples (Tier B)
- [ ] `world/levelgen/density/*_test.go` — node evaluator + parser tests (Tier C/D)
- [ ] `world/noisegen_test.go` — determinism (purity) + chunk-encode round-trip + sea-level water + heightmaps
- [ ] The offline extract step in `tools/` + the `//go:embed` of the worldgen JSON (production code, but the test data seam lives here)
- [ ] (Optional, T2) a captured vanilla-26.2 seed-heightmap fixture for the byte-diff
- [ ] Framework install: none (stdlib testing); no new runtime deps expected

## Validation Strategy

**The achievable validation (recommend):** a **real-client visual check** — connect an unmodified vanilla 26.2 client, swap the generator, and confirm the world renders as recognizable Minecraft terrain (hills, plains, valleys, oceans/lakes with water at sea level, walkable solid ground, no void, no stripes). This is the same human-verify gate that sealed Phases 4 and 5 (`autonomous:false`, BLOCKING). It directly proves the T0 success criterion.

**The gold-standard validation (T2, optional/aspirational):** a **seed-heightmap capture-diff** — run a real vanilla 26.2 server with a fixed seed, capture its generated chunk-column heightmaps (or the pre-decoration block columns) for a few chunks, and byte-diff against Sulfur's output for the same seed. This is the Phase-4/5 capture-diff discipline applied to terrain. It surfaces every subtle noise/seeding off-by-one. **Recommend treating this as a measurement tool to drive incremental fidelity, not the phase gate** (the phase gate is the visual check at T0).

**Per-tier unit determinism:** the `Xoroshiro` golden-vector test (Tier A) is the cheapest, highest-value test — it catches the #1 pitfall (wrong seeding) before any terrain renders. Write it first.

## Security Domain

`security_enforcement` is not configured for this internal server core `[VERIFIED: config.json — null]`. The relevant control is resource-exhaustion (DoS) bounds in `Generate`.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | — (online auth is the deferred ONLINE-01) |
| V5 Input Validation | partial | The chunk position is the only "input" to `Generate`; it is server-clamped by the view-distance ring already. The embedded worldgen JSON is build-time-trusted (from the pinned jar), not runtime input. |
| V6 Cryptography | no | — |

### Known Threat Patterns for this phase
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Slow `Generate` (per-block density) backs up the off-tick worker | Denial of Service | Port `NoiseChunk` cell interpolation (Pitfall 5); the worker's bounded queue already drops on overload `[VERIFIED: world/worker.go]` |
| Unbounded recursion parsing a malformed density graph | DoS (build-time only) | The graph JSON is from the trusted pinned jar, not runtime input; parse-time depth is bounded by the known vanilla graph. Validate at extract time. |
| Non-deterministic generation | Integrity (world corruption) | All randomness flows from the seed via ported `Xoroshiro`; no global RNG (Pitfall 7) |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The standing Phase-6/7 "port from Java" mandate EXTENDS to worldgen (the objective states it explicitly) | User Constraints / Java Sources To Port | Low — the objective is explicit; if the user wants a non-parity custom generator instead, the whole approach changes (but that contradicts PARITY-01's text). Confirm in discuss. |
| A2 | T0 (recognizable-faithful surface) is the right v2 scope; byte-identical (T2) is aspirational | Scope Tiers | Medium — if the user demands byte-identical for a seed as the *gate*, effort is materially higher (full graph + caves + aquifers + full surface rules + the seed-diff must pass). The recommendation is T0; the user picks the tier in discuss. |
| A3 | The worldgen graph JSON can be `//go:embed`-ed directly (it's plain JSON, build-time trusted) rather than codegen'd into Go literals | Codegen Question / Don't Hand-Roll | Low — embedding JSON + a runtime parser is simpler than generating Go; if a build prefers generated Go (no runtime parse), that's a cosmetic variation. Either way it's DATA-driven, not hand-typed. |
| A4 | A single-biome stub is acceptable for the FIRST T0 cut (terrain shape is biome-independent at the density level) | Java Sources To Port Tier F / Scope Tiers | Low — surface block choice (grass vs sand) is biome-keyed, so a single-biome stub yields uniform surface material until the multi-noise `Climate` source is ported. Visually "all plains hills" until then; acceptable as a first cut, confirm the user wants biome diversity in-scope. |
| A5 | T0 sea-level water (`y<63→water`) is acceptable in place of the full aquifer system | Pitfall 4 / Scope Tiers | Low-Medium — gives correct oceans/lakes but no underground water tables; if the user considers aquifers part of "parity," that's T1. Recommend deferring; confirm. |

## Open Questions

1. **Which scope tier? (T0 vs T1 vs T2)**
   - What we know: T0 (faithful surface) is achievable and yields recognizable terrain; T2 (byte-identical) is a tier of effort beyond.
   - What's unclear: whether the user wants caves/aquifers (T1) or byte-identical-for-a-seed (T2) as the *gate*.
   - Recommendation: **plan T0**; frame byte-identical as the validation target, not the gate. Confirm in discuss-phase.

2. **Biome source: single-biome stub vs ported multi-noise `Climate`?**
   - What we know: terrain *shape* is biome-independent; surface *material* and biome containers need a biome.
   - What's unclear: whether v2 wants real biome diversity (different surface blocks, biome map) or accepts uniform-biome terrain first.
   - Recommendation: single-biome (plains) stub for the first T0 cut; add the ported `Climate`/`MultiNoiseBiomeSource` (requires the `OverworldBiomes` param extractor) as the next increment if diversity is in-scope.

3. **Embed the worldgen JSON, or codegen it into Go?**
   - What we know: it's plain JSON in the jar; both `//go:embed`+parse and generate-Go-literals work.
   - What's unclear: build preference (runtime parse cost is negligible — parsed once at startup).
   - Recommendation: `//go:embed` the JSON + a runtime parser (simpler, mirrors Phase-2 registry NBT embedding). Decide in plan.

4. **Caves/aquifers/features deferred — does the user consider those part of "PARITY-01"?**
   - What we know: PARITY-01's text says "density-function world generation for terrain parity"; caves/features are separate vanilla passes (carvers/decoration), not the noise terrain itself.
   - What's unclear: the user's mental model of "parity" — surface-shape parity vs full-world parity.
   - Recommendation: scope PARITY-01 to **terrain-shape parity** (T0); track caves/aquifers/features as explicit follow-on tiers. Confirm.

## Sources

### Primary (HIGH confidence — verified this session)
- `temp/cache/26.2-inner.jar` via `unzip -l` / `javap` (Zulu 25) — the levelgen `.class` inventory (`synth/{ImprovedNoise,PerlinNoise,NormalNoise,BlendedNoise,NoiseUtils}`, `{XoroshiroRandomSource,RandomSupport,LegacyRandomSource}`, `{DensityFunction,DensityFunctions$*,NoiseRouter,NoiseChunk,NoiseGeneratorSettings,NoiseSettings,Noises,RandomState,Aquifer,Beardifier,SurfaceSystem}`, `biome/{Climate,MultiNoiseBiomeSource,BiomeSource}`), the method signatures of `XoroshiroRandomSource`/`NormalNoise` `[VERIFIED]`
- The extracted worldgen DATA: `data/minecraft/worldgen/noise_settings/overworld.json` (top-level keys, `noise_router` keys, `sea_level:63`, `default_block:stone`, `default_fluid:water`, `legacy_random_source:false`, `noise{height:384,min_y:-64,size_h:1,size_v:2}`, `final_density` uses min/squeeze/interpolated/blend_density, `surface_rule` = sequence), `density_function/{y,zero,depth,overworld/base_3d_noise}.json`, `noise/{aquifer_barrier,temperature}.json`, the 35-file density_function count, `multi_noise_biome_source_parameter_list/overworld.json` = `{preset:overworld}` `[VERIFIED]`
- Codebase: `world/generator.go` (the `Generator` interface + `Superflat` reference + heightmap discipline), `cmd/sulfur/main.go:123-126` (the swap point + worker wiring), `level/block/blocks.go` (Stone/Dirt/GrassBlock/Water/Deepslate structs), `level/biome/list.go`, `tools/` listing + grep (worldgen NOT currently extracted), `go.mod` `[VERIFIED]`
- `.planning/STATE.md` (the port-from-Java mandate, the off-tick worker + `chunkReady` rejoin, the capture-diff discipline, `-race` Docker), `.planning/REQUIREMENTS.md` (PARITY-01 text, WORLD-04 determinism, v2 boundary), `.planning/ROADMAP.md` (Phase 9, "which density-function subset is bespoke"), `.planning/phases/08-.../08-RESEARCH.md` (the off-tick worker pattern) `[VERIFIED]`
- `CLAUDE.md` Technology Stack — "Minecraft's terrain is NOT OpenSimplex… port from the unobfuscated `net.minecraft.world.level.levelgen` classes"; `ojrac/opensimplex-go` MVP-stub-only/archived; "Custom Mojang-noise port — you write it" `[CITED]`
- `.planning/config.json` — `nyquist_validation:true`, `security_enforcement:null` `[VERIFIED]`

### Secondary (MEDIUM confidence)
- minecraft.wiki "World generation" / "Noise settings" — density-function/noise-router concepts, `legacy_random_source` semantics, density>0 ⇒ solid `[CITED: https://minecraft.wiki/w/World_generation, https://minecraft.wiki/w/Noise_settings]`
- misode.github.io density-function generator (26.2-aware) — useful *reference visualizer* for a node graph (not a code source) `[CITED: https://misode.github.io/worldgen/density-function/]`

### Tertiary (LOW confidence)
- None — every load-bearing claim is grounded in the jar (`unzip`/`javap`), the extracted JSON, the codebase, or CLAUDE.md.

## Metadata

**Confidence breakdown:**
- Java port targets + dependency order: HIGH — every class verified present in the jar this session; method signatures confirmed via `javap`
- Data-driven graph (extract + parse, don't hand-type): HIGH — the wired router + node tree + noise params verified as JSON in the jar; codegen confirmed NOT to extract them yet
- Seeding chain (Xoroshiro, `legacy_random_source:false`): HIGH — verified in `overworld.json` + `javap` of `XoroshiroRandomSource`/`RandomSupport`
- Scope tiers / v2 recommendation: MEDIUM — T0 is clearly the right *recommendation*, but the exact effort and the "what counts as parity" boundary are user decisions for discuss-phase (Open Questions 1, 4)
- Validation strategy: HIGH — the real-client visual + seed-heightmap capture-diff mirror the proven Phase-4/5 gates
- Reuse surface (Generator seam, worker, level.Chunk, blocks): HIGH — read directly from the current source

**Research date:** 2026-06-24
**Valid until:** stable while pinned to the 26.2 jar — the levelgen classes + worldgen JSON are frozen for this version; re-verify only on a version bump (26.3+).
