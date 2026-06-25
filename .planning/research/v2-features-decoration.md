# v2 Research: World Feature / Decoration Subsystem (MC 26.2, protocol 776)

**Goal:** Port the vanilla feature/decoration pipeline 1:1 from the unobfuscated `26.2-inner.jar` into Sulfur's existing `world/levelgen/` + `world/noisegen.go`, as a new post-carve step in `NoiseGenerator.Generate`.

**Method:** All shapes below are `javap -c` confirmed against `D:\ender\temp\cache\26.2-inner.jar` (unobfuscated, Zulu 25). The feature/placement model has been stable since 1.18, so wiki lag on 26.2 is irrelevant to the architecture — but every *count* and *seed-math* claim here is jar-confirmed, not wiki-sourced.

**The single biggest finding:** the decoration RNG is the **legacy `java.util.Random` LCG** (`LegacyRandomSource`, multiplier `0x5DEECE66D`), NOT Xoroshiro. `world/levelgen/random.go` currently has Xoroshiro only. **You must add a `LegacyRandomSource` (LCG) before anything else** — it is the determinism hinge for the entire decoration pass, exactly like Xoroshiro was for v1's noise.

---

## Standard Stack

Port in this exact order. Each item is a hard dependency of the next.

### Wave A — RNG + pipeline seam (the foundation; nothing works without these)

1. **`LegacyRandomSource` (LCG)** — port into `world/levelgen/random.go`. Java `java.util.Random` semantics: `seed = (seed ^ 0x5DEECE66D) & ((1<<48)-1)` on `setSeed`; `next(bits)` advances `seed = (seed*0x5DEECE66D + 0xB) & mask` and returns `seed >> (48-bits)`. JAR-CONFIRMED multiplier `25214903917` and mask `281474976710655` in `LegacyRandomSource.setSeed`. Implement `nextLong`, `nextInt(bound)`, `nextFloat`, `nextDouble` with **Java's exact algorithms** (the modulo-bias loop in `nextInt`, the two-call `nextLong = ((long)next(32)<<32)+next(32)`). This is a SEPARATE type from your Xoroshiro — both implement your existing `RandomSource` interface.

2. **`WorldgenRandom.setDecorationSeed` + `setFeatureSeed`** — two methods on the LCG (see Code Examples for exact bytecode-derived math). These produce the per-feature placement seed deterministic over `(worldSeed, chunkOriginX, chunkOriginZ, featureIndex, stepIndex)`.

3. **`GenerationStep.Decoration` enum** — the 11-step order is JAR-CONFIRMED:
   `RAW_GENERATION, LAKES, LOCAL_MODIFICATIONS, UNDERGROUND_STRUCTURES, SURFACE_STRUCTURES, STRONGHOLDS, UNDERGROUND_ORES, UNDERGROUND_DECORATION, FLUID_SPRINGS, VEGETAL_DECORATION, TOP_LAYER_MODIFICATION`. Features iterate step-by-step in this order; **all of one step's features across all biomes run before the next step begins.**

4. **`WORLD_SURFACE_WG` heightmap** — `HeightmapPlacement` reads this (JAR: `PlacementContext.getHeight` → `WorldGenLevel.getHeight(Types,x,z)`). v1's `BuildSurface` already writes the CLIENT heightmaps; for features you need the **`_WG` (worldgen) variant** queryable mid-pipeline. The 6 types are JAR-CONFIRMED: `WORLD_SURFACE_WG, WORLD_SURFACE, OCEAN_FLOOR_WG, OCEAN_FLOOR, MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES`. For features you need `WORLD_SURFACE_WG`, `OCEAN_FLOOR_WG`, and `MOTION_BLOCKING` reads.

5. **The neighbor-aware placement seam** — `applyBiomeDecoration` runs per chunk but features WRITE into a 3×3 (it uses `ChunkPos.rangeClosed(center, 1)`) region. You must give the feature placer a `WorldGenLevel`-like view that spans the 8 neighbors. See Architecture Patterns — this is the brownfield seam decision.

### Wave B — placement modifiers (load-bearing subset)

Port these 8 first; they cover ~95% of overworld placed_features. Each is a `PlacementModifier.getPositions(ctx, rng, pos) -> Stream<BlockPos>`:

6. **`InSquarePlacement`** (`in_square`) — `x += rng.nextInt(16); z += rng.nextInt(16)`. JAR-CONFIRMED. The horizontal scatter every surface feature uses.
7. **`HeightmapPlacement`** (`heightmap`) — set `y = ctx.getHeight(type, x, z)`; emit nothing if `y <= minY`. JAR-CONFIRMED.
8. **`CountPlacement`** (`count`) extends `RepeatingPlacement` — repeats the input pos `count(rng)` times (an `IntStream.range(0,count).mapToObj(_ -> pos)`). JAR-CONFIRMED via `RepeatingPlacement.getPositions`.
9. **`RarityFilter`** (`rarity_filter`) extends `PlacementFilter` — keep pos iff `rng.nextFloat() < 1.0/chance`. JAR-CONFIRMED.
10. **`BiomeFilter`** (`biome`) extends `PlacementFilter` — keep iff the configured-feature is allowed in the biome AT the candidate pos (re-checks biome per-position; prevents a feature seeded in biome A from spilling into biome B). Load-bearing for biome-correct edges.
11. **`HeightRangePlacement`** (`height_range`) — `y = heightProvider.sample(rng, ctx)` (uniform/trapezoid between two `VerticalAnchor`s). Ores/underground use this. JAR-CONFIRMED factory `uniform/triangle`.
12. **`SurfaceWaterDepthFilter`** (`surface_water_depth_filter`) extends `PlacementFilter` — keep iff water column depth ≤ maxDepth. Keeps land vegetation off deep water. JAR-CONFIRMED.
13. **`PlacementFilter` / `RepeatingPlacement` base classes** — port the two abstract bases first; `PlacementFilter.getPositions` is `shouldPlace ? Stream.of(pos) : Stream.empty()` (JAR-CONFIRMED), `RepeatingPlacement.getPositions` is the count-loop.

Defer (Nether/cave/rare): `NoiseBasedCountPlacement`, `NoiseThresholdCountPlacement`, `CountOnEveryLayerPlacement`, `EnvironmentScanPlacement`, `BlockPredicateFilter`, `SurfaceRelativeThresholdFilter`, `RandomOffsetPlacement`, `FixedPlacement`, `CaveSurface`.

### Wave C — the core Feature types (overworld minimum)

Port `Feature<FC>.place(FeaturePlaceContext)` per type. The overworld-critical set:

14. **`OreFeature`** (`ore`) — *as a feature* (distinct from v1's noise-router ore veins). `UNDERGROUND_ORES` step places coal/iron/copper/etc. as discrete blobs via `OreConfiguration` (size + target-block-state list + discard-on-air chance). This is the bulk of underground decoration. **Port early — it is high-volume and self-contained.**
15. **`RandomPatchFeature`** (`random_patch`) — the vegetation workhorse: `tries`/`xz_spread`/`y_spread` around the origin, places an inner `PlacedFeature` (usually `simple_block`). Grass, flowers, dead bushes, most ground cover. (Class is `RandomPatchFeature`; config `RandomPatchConfiguration`.)
16. **`SimpleBlockFeature`** (`simple_block`) — places one block from a `BlockStateProvider` if a predicate passes. The leaf of most patches.
17. **`TreeFeature`** (`tree`) — the headline. See dedicated section below.
18. **`FlowerFeature`** / handled via `random_patch` + `NoiseProvider` state provider — flower forests use `dual_noise_provider`/`noise_provider` to pick flower type by position.
19. **`BlockPileFeature`** (`block_pile`), **`FallenTreeFeature`** (`fallen_tree`) — JAR-CONFIRMED present (`BlockPileFeature.class`, `FallenTreeFeature.class`, `FallenTreeConfiguration`). Lower priority but cheap once trees work.
20. **`VegetationPatchFeature`** / **`WaterloggedVegetationPatchFeature`** — moss/clay patches; medium priority.

Selector/composite features needed because configured_features nest them:
21. **`RandomSelectorFeature`** (`random_selector`), **`SimpleRandomSelectorFeature`** (`simple_random_selector`), **`RandomBooleanSelectorFeature`** (`random_boolean_selector`) — JAR-CONFIRMED. Trees in a biome are usually a `random_selector` over `WeightedPlacedFeature`s (e.g. forest = mostly oak, some birch, rare large-oak). **You can't render a forest without the selector.**

Defer: everything Nether/End/cave-cosmetic (`GeodeFeature`, `DripstoneCluster`, `BasaltColumns`, `Coral*`, `HugeFungus`, `EndSpike`, `ChorusPlant`, `Iceberg`, `SculkPatch`, etc.) — ~40 classes, none overworld-surface-critical.

### Wave D — tree placers + state providers

22. **`BlockStateProvider` hierarchy** — JAR-CONFIRMED 11 types. Port `SimpleStateProvider` (single state) + `WeightedStateProvider` (weighted pick) + `RuleBasedStateProvider` (the `below_trunk_provider` in oak.json) first. `NoiseProvider`/`DualNoiseProvider`/`NoiseThresholdProvider` for flower-forest variety; `RotatedBlockProvider`/`RandomizedIntStateProvider` later.
23. **`TrunkPlacer` hierarchy** — port `StraightTrunkPlacer` first (oak/birch/jungle/spruce trunks; see Code Examples), then `ForkingTrunkPlacer`, `FancyTrunkPlacer` (large oak), `DarkOakTrunkPlacer`, `MegaJungleTrunkPlacer`, `GiantTrunkPlacer`, `CherryTrunkPlacer`, `BendingTrunkPlacer`, `UpwardsBranchingTrunkPlacer` (mangrove). JAR-CONFIRMED 10 subclasses.
24. **`FoliagePlacer` hierarchy** — port `BlobFoliagePlacer` first (oak/birch — see oak.json), then `SpruceFoliagePlacer`, `PineFoliagePlacer`, `AcaciaFoliagePlacer`, `BushFoliagePlacer`, `FancyFoliagePlacer`, `DarkOakFoliagePlacer`, `MegaPineFoliagePlacer`, `MegaJungleFoliagePlacer`, `CherryFoliagePlacer`, `RandomSpreadFoliagePlacer`. JAR-CONFIRMED 11 subclasses.
25. **`TreeDecorator` hierarchy** (`decorators` array, often empty for oak) — JAR-CONFIRMED 12 types. Port lazily: `AlterGroundDecorator` (spruce podzol), `BeehiveDecorator`, `CocoaDecorator` (jungle), `LeaveVineDecorator`/`TrunkVineDecorator` (jungle), `AttachedToLeaves/Logs`, `PlaceOnGround`, `PaleMoss`/`CreakingHeart` (pale garden), `Mangrove*`. Most overworld trees have `"decorators": []` (oak.json confirms), so a no-op-decorator MVP renders forests fine.
26. **`RootPlacer`** (`rootplacers/`) — only mangrove + azalea. Defer.

### Wave E — biome→feature wiring + data

27. **`BiomeGenerationSettings.features()`** — returns `List<HolderSet<PlacedFeature>>` indexed by GenerationStep ordinal (the 11-element `features` array in each biome JSON). JAR-CONFIRMED via `applyBiomeDecoration` reading `generationSettingsGetter.apply(holder).features().get(stepIndex)`.
28. **`FeatureSorter.buildFeaturesPerStep`** — the cross-biome toposort. JAR signature confirmed: `buildFeaturesPerStep(List<T> biomes, Function<T,List<HolderSet<PlacedFeature>>> getter, boolean)`. Produces, per step, a flat ordered `List<PlacedFeature>` + an `indexMapping: ToIntFunction<PlacedFeature>`. Build this ONCE at generator construction (vanilla memoizes it in a `Supplier`). This deduplicates the same placed_feature appearing in 50 biomes into one global per-step index, and enforces a consistent cross-biome ordering (it errors on cycles — features must agree on relative order across all biomes).
29. **Embedded data** — see Don't Hand-Roll.

---

## Architecture Patterns

### Where features slot into `NoiseGenerator.Generate`

v1's `Generate` is currently `fill -> surface -> carve -> finish`. Features are a **new fifth step, post-carve**, mirroring vanilla's ChunkStatus `...CARVERS -> FEATURES`:

```
fill -> surface -> carve -> [FEATURES] -> finish
```

But there is a **fundamental shape change**: every prior step writes ONLY into the target chunk (your `carveChunk` even drops out-of-footprint writes). **Features cannot.** `applyBiomeDecoration` decorates the CENTER chunk but a feature rooted near the edge writes logs/leaves into the 8 neighbors. Vanilla resolves this by only running a chunk's FEATURES status once all 8 neighbors have reached at least CARVERS status, and giving the feature placer a `WorldGenLevel` that proxies reads/writes across the 3×3 of loaded chunks.

**Brownfield recommendation — two viable seams, pick by your worker model:**

- **Option 1 (vanilla-faithful, deferred edits):** Generate produces a chunk at "carved" status; a SECOND pass (`DecorateChunk(center, neighbors[8])`) runs features once the 3×3 carved neighborhood exists, writing into all 9. This requires the `world/worker.go` to hold chunks at an intermediate status and schedule decoration when the neighborhood completes — a real change to the worker's chunk lifecycle. **This is the correct long-term port.**
- **Option 2 (single-shot, write-buffer):** Keep `Generate(pos)` single-shot but, in the feature step, collect cross-chunk writes into a per-neighbor-keyed buffer (`map[ChunkPos][]blockEdit`) and have the worker flush each neighbor's buffer into that neighbor when it generates (or replay deterministically). Simpler worker change, but you must guarantee deterministic merge order (a chunk receives edits from up to 8 decorating neighbors; vanilla's order is fixed by chunk-load order, which you must reproduce or you get non-determinism at seams).

**Prescription:** scope v2 around **Option 1**. It is the only one that is provably 1:1 with vanilla determinism (the 3×3 neighborhood + the per-chunk decoration seed fully determine the result regardless of generation order). Option 2's correctness depends on replaying vanilla's load order, which is fragile. The worker-lifecycle change (hold-at-status + neighborhood-ready scheduling) is the real v2 cost and should be a named requirement.

### The ConfiguredFeature / PlacedFeature / PlacementModifier layering

Three nested records (all JAR-CONFIRMED as `java.lang.Record`):

- **`ConfiguredFeature<FC,F>`** = `(Feature<FC> feature, FC config)`. "WHAT to build and with what params." E.g. `(TreeFeature, TreeConfiguration{oak trunk/foliage/...})`. `.place(level, gen, rng, pos)` runs the feature's logic at one anchor.
- **`PlacedFeature`** = `(Holder<ConfiguredFeature> feature, List<PlacementModifier> placement)`. "WHERE to build." `.place()` runs the modifier chain to produce anchor positions, then calls the ConfiguredFeature at each.
- **`PlacementModifier.getPositions(ctx, rng, pos) -> Stream<BlockPos>`** — each transforms the position stream: count multiplies, in_square jitters, heightmap projects to terrain top, filters drop positions.

**`PlacedFeature.placeWithContext` is a fold/flatMap** (JAR-CONFIRMED `placeWithContext`):

```
positions = Stream.of(originPos)
for modifier in placement:           // order matters
    positions = positions.flatMap(p -> modifier.getPositions(ctx, rng, p))
for p in positions:
    configuredFeature.place(level, gen, rng, p)   // sets MutableBoolean if any placed
```

Note the rng threading: the SAME `WorldgenRandom` (seeded once per feature by `setFeatureSeed`) is consumed by every modifier AND by the feature body, in stream order. **Stream evaluation order is the determinism contract** — Java streams here are lazy/sequential, so `count -> in_square -> heightmap` consumes rng as: countN draws, then per-position the in_square 2 draws, etc. Port as an explicit ordered loop (NOT Go `range` over a map, NOT goroutines) to reproduce the exact draw sequence.

### The decoration-seed RNG chain (the determinism hinge)

Per CHUNK, once: `decoSeed = wgRandom.setDecorationSeed(worldSeed, originX, originZ)` where origin = the chunk's min-corner block coords (`sectionPos.origin()`, i.e. `chunkX*16, chunkZ*16`). Then per FEATURE: `wgRandom.setFeatureSeed(decoSeed, featureIndexInStep, stepIndex)`. This makes each feature's RNG a pure function of `(worldSeed, chunkX, chunkZ, featureIndexWithinStep, stepIndex)` — independent of how many features ran before it, so adding/removing a feature doesn't shift others. Mirrors exactly how v1's Xoroshiro positional factory made noise pure over `(seed, pos)`. **Both seed methods use the legacy LCG, not Xoroshiro** (see Code Examples for the exact math).

### `applyBiomeDecoration` outer loop (JAR-CONFIRMED structure)

```
decoSeed = wgRandom.setDecorationSeed(seed, origin.x, origin.z)
stepCount = max(GenerationStep.Decoration.values().length, featuresPerStep.size())  // = 11
for stepIndex in 0..stepCount:
    featureIndex = 0
    // (structures first — skip for v2, no structures)
    indicesThisStep = IntSet()
    for biomeHolder in biomesInThis3x3:                  // the retained possible biomes
        stepFeatures = biome.generationSettings.features().get(stepIndex)  // HolderSet<PlacedFeature>
        if stepIndex >= biome.features().size(): continue
        for pf in stepFeatures: indicesThisStep.add( stepFeatureData.indexMapping(pf) )
    sortedIndices = sort(indicesThisStep.toIntArray())   // ascending global index — deterministic
    for idx in sortedIndices:
        placedFeature = stepFeatureData.features().get(idx)
        wgRandom.setFeatureSeed(decoSeed, idx, stepIndex)
        placedFeature.placeWithBiomeCheck(level, generator, wgRandom, origin)
```

Key subtleties (all JAR-CONFIRMED): (a) the per-feature seed uses the **global cross-biome index** `idx`, not a per-biome counter — so the same placed_feature gets the same seed regardless of which biome referenced it; (b) the IntSet+sort dedups a feature shared by overlapping biomes to ONE placement at a stable index; (c) `placeWithBiomeCheck` (not bare `place`) — it re-validates the biome at the placement position via `BiomeFilter`.

### Heightmap timing

`HeightmapPlacement`/`SurfaceWaterDepthFilter`/tree validity all read `WORLD_SURFACE_WG`/`OCEAN_FLOOR_WG` MID-decoration, and features mutate the heightmap as they place blocks (a tree raises `MOTION_BLOCKING`). So the heightmap must be (1) built from the post-carve terrain BEFORE the first feature, and (2) **kept live and updated** as features set blocks — a later feature in the same step sees the earlier tree. v1's `BuildSurface` writes the CLIENT heightmaps once and never updates them; for v2 you need a mutable heightmap on the chunk that feature `setBlock` calls keep current.

---

## Don't Hand-Roll (embed-from-jar vs port-logic)

**EMBED (data — `//go:embed` under `world/levelgen/data/`, parse to structs):**

| Registry | Count (JAR-CONFIRMED) | Path in jar | Notes |
|---|---|---|---|
| `configured_feature` | **226** | `data/minecraft/worldgen/configured_feature/*.json` | The Feature type + its full config (tree params, ore target lists, patch params). |
| `placed_feature` | **262** | `data/minecraft/worldgen/placed_feature/*.json` | Each = a configured_feature ref + ordered placement modifier list. |
| `biome` | **66** | `data/minecraft/worldgen/biome/*.json` | The 11-element `features` array (HolderSet<placed_feature> per step) + `carvers`. v1 may already embed these for the biome source — REUSE; don't re-embed. |
| `tree_decorator` | (inline) | inline in configured_feature JSON | Not a separate registry; decorators are inline in the tree config's `decorators` array. |

The existing `tools/` codegen pipeline already extracts the `data/minecraft/worldgen/**` JSON tree wholesale (v1 embeds density_function, noise_settings, biome_parameters, tags from the same tree). **configured_feature + placed_feature are NEW directories to add to the embed set — same extraction mechanism, no new tooling.** Parsing is new (these are polymorphic `"type"`-tagged JSON — a registry-dispatch decoder, like v1's density-function/surface-rule parsers).

**Block state references in the data** (e.g. `"minecraft:oak_log"` with `{"axis":"y"}`) resolve through your existing `level/block` `ToStateID` — the JSON gives `Name`+`Properties`, you map to `block.StateID`. JAR-CONFIRMED shape in oak.json.

**PORT (logic — idiomatic Go from bytecode, cite the class):**

- `LegacyRandomSource` LCG + `WorldgenRandom.setDecorationSeed/setFeatureSeed` (`world/level/levelgen/`).
- Every `PlacementModifier.getPositions` (`world/level/levelgen/placement/`).
- Every `Feature.place` (`world/level/levelgen/feature/`).
- Every `TrunkPlacer.placeTrunk` / `FoliagePlacer.createFoliage` / `TreeDecorator.place` / `BlockStateProvider.getState`.
- `FeatureSorter.buildFeaturesPerStep` toposort.
- `applyBiomeDecoration` orchestration loop.
- The `Heightmap` update logic (`getFirstAvailable`/`update`).

**NEVER hand-transcribe** the 226+262+66 JSON entries, and never GPL-paste — port the *algorithm*, embed the *data*.

---

## Common Pitfalls

1. **Wrong RNG family for placement.** The decoration chain is the **legacy LCG** (`0x5DEECE66D`), NOT Xoroshiro. If you reuse v1's Xoroshiro for `setDecorationSeed`, every feature lands in the wrong spot vs vanilla. JAR-CONFIRMED: `WorldgenRandom extends LegacyRandomSource`. You must add a second RNG type.

2. **Cross-chunk cascade / wrong neighborhood.** A chunk's features are placed by ITS 3×3 neighbors decorating into it, AND it decorates into its own neighbors. If you only let a chunk write into itself (like the current `carveChunk` footprint guard), trees get clipped at every chunk border. The fix is the 8-neighbor `WorldGenLevel` proxy + holding chunks at an intermediate status until the neighborhood is carved (vanilla's "8 neighbors at ≥ features-prerequisite status" rule). This is the central architectural cost of v2.

3. **Placement-seed bugs (origin coords + index basis).** `setDecorationSeed` takes the chunk's **block-origin** (`chunkX*16, chunkZ*16`), not chunk coords. `setFeatureSeed` takes the **global cross-biome feature index** (from FeatureSorter), not a per-biome 0-based counter. Get either wrong and determinism diverges from vanilla. JAR-CONFIRMED in `applyBiomeDecoration` (uses `SectionPos.origin()` and `stepFeatureData.indexMapping`).

4. **Stream-order / RNG-draw-order divergence.** `placeWithContext` is a lazy sequential `flatMap` fold; the modifier order and Java's sequential stream consume the rng in a precise sequence (count draws first, then per-position in_square draws, then the feature body's draws). Porting with goroutines, a Go `map` iteration, or reordered loops corrupts the draw sequence → wrong placements. Port as a single-threaded explicit ordered loop.

5. **Heightmap timing / staleness.** `HeightmapPlacement` reads `WORLD_SURFACE_WG` live, and features mutate it as they place. Build the WG heightmaps from post-carve terrain BEFORE features, and UPDATE them on every feature `setBlock`. A stale (surface-only) heightmap puts trees underground or floating, and makes a later same-step feature ignore an earlier tree. v1's `BuildSurface` writes heightmaps once and never updates — insufficient for features.

6. **`BiomeFilter` skipped → features bleed across biome edges.** `placeWithBiomeCheck` (not bare `place`) re-checks the biome at each candidate position. Drop it and e.g. cactus seeded in a desert chunk spills into the adjacent forest. JAR-CONFIRMED the loop calls `placeWithBiomeCheck`.

7. **Feature-vs-noise ore double-placement.** v1's `OreVeinifier` (noise router) is DISTINCT from `OreFeature` (the `UNDERGROUND_ORES` decoration step). Vanilla has BOTH — large rare veins (noise) + the common scattered ore blobs (feature). Don't conflate them or skip the feature ores thinking the noise veins cover it; they're different ore distributions.

8. **`LegacyRandomSource.nextInt(bound)` modulo-bias loop.** Java's `nextInt(bound)` has a specific rejection-sampling loop for non-power-of-2 bounds. A naive `next(31) % bound` diverges. Port Java's exact algorithm.

9. **Composite/selector recursion + sub-feature RNG.** Trees are usually wrapped in `random_selector`/`weighted` configured_features that pick a sub-PlacedFeature, which has its OWN placement modifiers re-applied. `PlacedFeature.getFeatures()` (JAR-CONFIRMED) flattens sub-features via `ConfiguredFeature.getSubFeatures` — the rng flows through. Implement the recursion or forests collapse to a single tree type.

---

## Code Examples

### Decoration-seed derivation (JAR-CONFIRMED bytecode, `WorldgenRandom`)

```go
// LCG-backed (LegacyRandomSource). NOT Xoroshiro.
// setDecorationSeed(worldSeed, originX, originZ): per-chunk base seed.
func (r *LegacyRandomSource) SetDecorationSeed(worldSeed int64, x, z int) int64 {
    r.SetSeed(worldSeed)
    a := r.NextLong() | 1          // bytecode: nextLong(); lconst_1; lor
    b := r.NextLong() | 1
    seed := (int64(x)*a + int64(z)*b) ^ worldSeed   // iload x; i2l; lmul; ... ladd; lxor
    r.SetSeed(seed)
    return seed
}

// setFeatureSeed(decoSeed, index, step): per-feature seed. NO nextLong calls — pure arithmetic.
// bytecode: lload decoSeed; iload index; i2l; ladd; sipush 10000; iload step; imul; i2l; ladd
func (r *LegacyRandomSource) SetFeatureSeed(decoSeed int64, index, step int) {
    seed := decoSeed + int64(index) + int64(10000*step)
    r.SetSeed(seed)
}
```

### `LegacyRandomSource.setSeed` (JAR-CONFIRMED constants)

```go
const lcgMult = 0x5DEECE66D     // 25214903917
const lcgAdd  = 0xB
const lcgMask = (1 << 48) - 1   // 281474976710655
func (r *LegacyRandomSource) SetSeed(seed int64) { r.seed = (seed ^ lcgMult) & lcgMask }
func (r *LegacyRandomSource) next(bits int) int32 {
    r.seed = (r.seed*lcgMult + lcgAdd) & lcgMask
    return int32(r.seed >> (48 - bits))   // arithmetic shift, Java semantics
}
```

### `PlacedFeature.placeWithContext` (JAR-CONFIRMED flatMap fold)

```go
func (pf *PlacedFeature) place(ctx *PlacementContext, rng RandomSource, origin BlockPos) bool {
    positions := []BlockPos{origin}
    for _, mod := range pf.placement {              // ordered; rng threaded through
        var next []BlockPos
        for _, p := range positions {
            next = append(next, mod.getPositions(ctx, rng, p)...)  // flatMap
        }
        positions = next
    }
    placed := false
    cf := pf.feature.Value() // ConfiguredFeature
    for _, p := range positions {
        if cf.place(ctx.Level(), ctx.Generator(), rng, p) { placed = true }
    }
    return placed
}
```

### Placement modifiers (JAR-CONFIRMED bodies)

```go
// InSquarePlacement: rng.nextInt(16) on x and z (consumes 2 ints, x first).
func (InSquarePlacement) getPositions(ctx *PlacementContext, rng RandomSource, p BlockPos) []BlockPos {
    return []BlockPos{{X: rng.NextIntN(16) + p.X, Y: p.Y, Z: rng.NextIntN(16) + p.Z}}
}
// HeightmapPlacement: project to terrain top; empty if at/below minY.
func (h HeightmapPlacement) getPositions(ctx *PlacementContext, _ RandomSource, p BlockPos) []BlockPos {
    y := ctx.GetHeight(h.heightmap, p.X, p.Z)
    if y <= ctx.MinY() { return nil }
    return []BlockPos{{p.X, y, p.Z}}
}
// RarityFilter: keep iff nextFloat < 1/chance.
func (r RarityFilter) shouldPlace(_ *PlacementContext, rng RandomSource, _ BlockPos) bool {
    return rng.NextFloat() < 1.0/float32(r.chance)
}
// CountPlacement (RepeatingPlacement): repeat input pos count(rng) times.
func (c CountPlacement) getPositions(ctx *PlacementContext, rng RandomSource, p BlockPos) []BlockPos {
    n := c.count.Sample(rng)
    out := make([]BlockPos, n)
    for i := range out { out[i] = p }
    return out
}
```

### `StraightTrunkPlacer.placeTrunk` (JAR-CONFIRMED — the canonical placer)

```go
// height = baseHeight + nextInt(heightRandA+1) + nextInt(heightRandB+1)  (TrunkPlacer.getTreeHeight)
func (StraightTrunkPlacer) placeTrunk(level WorldGenLevel, setBlock BiConsumer,
    rng RandomSource, freeHeight int, pos BlockPos, cfg *TreeConfiguration) []FoliageAttachment {
    placeBelowTrunkBlock(level, setBlock, rng, pos.Below(), cfg) // dirt under trunk
    for i := 0; i < freeHeight; i++ {
        placeLog(level, setBlock, rng, pos.Above(i), cfg)        // log column
    }
    // foliage attaches one block above the top log, radiusOffset 0, doubleTrunk false
    return []FoliageAttachment{{Pos: pos.Above(freeHeight), RadiusOffset: 0, DoubleTrunk: false}}
}
```

### `applyBiomeDecoration` outer loop — see Architecture Patterns (full JAR-CONFIRMED structure given there).

---

## Confidence

| Claim | Confidence | Basis |
|---|---|---|
| Decoration RNG is legacy LCG (`0x5DEECE66D`), not Xoroshiro | **HIGH** | `WorldgenRandom extends LegacyRandomSource`; `setSeed` bytecode shows mult 25214903917, mask 281474976710655. Jar-confirmed. |
| `setDecorationSeed` math (`x*a + z*b ^ seed`, a/b = `nextLong\|1`) | **HIGH** | Full `javap -c` of `WorldgenRandom.setDecorationSeed` transcribed. |
| `setFeatureSeed` math (`deco + index + 10000*step`) | **HIGH** | Full `javap -c` of `setFeatureSeed` transcribed (sipush 10000). |
| GenerationStep.Decoration = 11 steps in the listed order | **HIGH** | `javap -p GenerationStep$Decoration` enum constants, in declaration order. |
| `applyBiomeDecoration` loop (FeatureSorter index, IntSet dedup, sort, setFeatureSeed-per-feature, placeWithBiomeCheck) | **HIGH** | Full `javap -c` of `ChunkGenerator.applyBiomeDecoration` (250+ lines) read and structurally mapped. |
| PlacedFeature = flatMap fold over modifiers then place | **HIGH** | `javap -c placeWithContext` transcribed. |
| Placement modifier bodies (InSquare, Heightmap, Rarity, Count/Repeating, Filter base) | **HIGH** | Each `getPositions`/`shouldPlace` bytecode read directly. |
| StraightTrunkPlacer.placeTrunk shape | **HIGH** | `javap -c` of `placeTrunk` transcribed. |
| Heightmap types (`WORLD_SURFACE_WG` etc.) + `PlacementContext.getHeight` path | **HIGH** | `Heightmap$Types` fields + `PlacementContext.getHeight` bytecode. |
| Data counts: 226 configured_feature, 262 placed_feature, 66 biome JSONs | **HIGH** | `jar tf \| grep -c` against the jar. |
| configured_feature/biome JSON shapes (tree config; 11-elem `features` array) | **HIGH** | `unzip -p` of oak.json + plains.json inspected. |
| Trunk/foliage/decorator/state-provider subclass rosters (10/11/12/11) | **HIGH** | `jar tf` class listings enumerated. |
| Existing `random.go` has NO legacy LCG (Xoroshiro only) | **HIGH** | Grep of `world/levelgen/random.go` — only `xoroshiro128pp`/`Xoroshiro`. |
| `tools/` already extracts the worldgen data tree; configured/placed_feature are additive | **MEDIUM** | Inferred from v1 embedding density_function/noise_settings/biome from the same `data/minecraft/worldgen/**` path; not re-verified inside `tools/`. |
| The 8-neighbor / hold-at-status worker change is the central v2 architectural cost | **MEDIUM** | The 3×3 (`ChunkPos.rangeClosed(...,1)`) and cross-chunk writes are jar-confirmed; the *specific* Sulfur worker integration (Option 1 vs 2) is a design recommendation, not jar-dictated. |
| `OreFeature` (decoration ore) is distinct from v1's noise `OreVeinifier` | **HIGH** | `OreFeature.class` + `UNDERGROUND_ORES` step both present; v1 OreVeinifier is the noise-router path. Both exist in vanilla. |
| The minimal overworld feature set (Wave C) | **MEDIUM** | Feature classes jar-confirmed present; the "minimum for a recognizable overworld" selection is judgment, validated against which configured_features the overworld biome JSONs actually reference. |
