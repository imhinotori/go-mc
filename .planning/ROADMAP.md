# Roadmap: Sulfur — Worldgen Features + Structures (v2)

## Shipped Milestones

- **v1.0** (2026-06-24) — Full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied **noise world** (caves/ravines/aquifers/ore-veins) with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. 9 phases, 46 plans, 45/45 v1 requirements + PARITY-01. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md) · [audit](v1.0-MILESTONE-AUDIT.md)

## Overview

v2 finishes the worldgen story v1 deliberately deferred: the overworld must now **look and read like vanilla 26.2** — biome-correct trees/grass/flowers/ores cover the surface, and the emblematic structures (desert pyramids, mineshafts, strongholds, villages) generate in their exact vanilla positions, all ported 1:1 from the unobfuscated jar and deterministic per seed.

The sequence is **dependency-forced, not arbitrary**. A single FOUNDATION phase (10) lands the three primitives BOTH features and structures share — the legacy `java.util.Random` LCG (the decoration/structure determinism hinge, distinct from v1's Xoroshiro), the cross-chunk neighborhood-ready worker seam (the single biggest architectural cost of v2: a chunk holds at carved status until its 8 neighbors are carved, then decorates/places into the 3×3), and the live mutable worldgen heightmap. Nothing else can start until this lands.

Features come first for the fast visual win (phases 11–13), building the `ConfiguredFeature`/`PlacedFeature`/placement-modifier pipeline and `applyBiomeDecoration` orchestration, then the core feature types (ores/patches/selectors), then the headline `TreeFeature` subsystem plus the dungeon — ending in a real-client **VISUAL GATE**. Structures follow in the research's strict easy→hard tier order (phases 14–16): the STARTS/REFERENCES/PLACE pipeline + `StructurePiece` machinery shaken down on the single-piece desert pyramid, then the recursive mineshaft + the stronghold's concentric-rings placement, then the data-driven village jigsaw (the hardest — the full `.nbt`/template_pool/`Placer` system) — ending in the second **VISUAL GATE**. Each tier reuses the prior tier's machinery, so difficulty order is also dependency order.

Worldgen adds **no new wire surface** (the chunk format was sealed in v1 Phase 4), so the two gates are VISUAL determinism checks against a real client, not capture-diffs.

## Phases

**Phase Numbering:**
- Integer phases (10, 11, 12): Planned milestone work (v2 continues v1's 1–9)
- Decimal phases (11.1, 11.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 10: Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap** - Port the legacy `java.util.Random` LCG + `WorldgenRandom` seed methods, the neighborhood-ready 3×3 worker seam (hold-at-carved → decorate), and the live mutable worldgen heightmap — the shared substrate both features and structures depend on (completed 2026-06-25)
- [x] **Phase 11: Feature Pipeline & Decoration Orchestration** - Port the placement-modifier layer + the `ConfiguredFeature`/`PlacedFeature` model + the polymorphic JSON parser + `FeatureSorter` + `applyBiomeDecoration` driving the 11 decoration steps over the 3×3 biome set
 (completed 2026-06-25)
- [x] **Phase 12: Core Feature Types** - Port `OreFeature`, `RandomPatchFeature`/`SimpleBlockFeature`, the random selectors, and `BlockPile`/`FallenTree`/`VegetationPatch` so a biome's full ground-cover + decoration-ore set generates (completed 2026-06-25)
- [x] **Phase 13: Trees, Dungeon & Features Visual Gate** - Port `TreeFeature` (trunk/foliage placers + tree decorators + `BlockStateProvider` hierarchy) + the `MonsterRoomFeature` dungeon; close the block with the real-client VISUAL GATE (full per-biome vegetation, deterministic per seed) (completed 2026-06-25 — visual gate approved: trees + vines confirmed on a real client)
- [x] **Phase 14: Structure Pipeline & Temples** - Port the STARTS/REFERENCES/PLACE two-phase pipeline + the `StructurePiece` bounding-box-tree machinery; the desert pyramid (+ jungle temple/igloo/swamp hut) generates in vanilla positions as the pipeline shakedown (completed 2026-06-25)
- [ ] **Phase 15: Mineshaft & Stronghold** - Port the multi-piece recursive mineshaft (`legacy_type_3` frequency-reduction placement) and the stronghold (concentric-rings placement + recursive piece set reusing the piece machinery)
- [ ] **Phase 16: Village Jigsaw & Structures Visual Gate** - Port the `.nbt` `StructureTemplate` system + template_pool/processor data + the bounded-BFS `JigsawPlacement.Placer` so villages generate per biome variant; close the milestone with the real-client structures VISUAL GATE

## Phase Details

### Phase 10: Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap
**Goal**: The three shared worldgen primitives both features and structures require exist and are jar-exact — a legacy LCG random source, a worker pipeline that can write across chunk boundaries deterministically, and a live worldgen heightmap that tracks placements.
**Depends on**: Phase 9 (v1 noise worldgen — the `world/levelgen/` + `world/noisegen.go` Generate seam this extends)
**Requirements**: GEN2-01, GEN2-02, GEN2-03
**Success Criteria** (what must be TRUE):
  1. A `LegacyRandomSource` (java.util.Random LCG: multiplier `0x5DEECE66D`, addend `0xB`, 48-bit mask) coexists with the existing Xoroshiro under the same `RandomSource` interface, with Java-exact `nextInt(bound)` (the modulo-bias rejection loop), `nextLong`, `nextFloat`, `nextDouble`; a known seed reproduces Java's exact draw sequence in a golden test
  2. `WorldgenRandom` exposes `setDecorationSeed`/`setFeatureSeed` and `setLargeFeatureSeed`/`setLargeFeatureWithSalt`, each bit-exact to the jar bytecode (the per-chunk + per-feature + per-structure seed derivations are pure functions of their inputs)
  3. The worker holds a chunk at carved status until its 8 neighbors have reached carved, then runs a decoration/placement pass over a `WorldGenLevel`-like view spanning the 3×3 — replacing the single-chunk footprint guard; the same seed produces identical seam results regardless of chunk generation order
  4. A live, mutable worldgen heightmap (`WORLD_SURFACE_WG`/`OCEAN_FLOOR_WG`/`MOTION_BLOCKING`) is built from post-carve terrain before the first feature and updated on every worldgen block write, so heightmap-relative placement sees prior placements
**Plans**: 3 plans
  - [x] 10-01-PLAN.md — Port the legacy java.util.Random LCG (LegacyRandomSource) + the WorldgenRandom seed-derivation wrapper, jar-exact, with golden-vector tests (GEN2-01)
  - [x] 10-02-PLAN.md — Add the incremental HeightmapUpdate primitive + build OCEAN_FLOOR_WG/MOTION_BLOCKING from post-carve terrain (GEN2-03)
  - [x] 10-03-PLAN.md — Split Generator into GenerateTerrain/Decorate + the staging-scheduler worker seam + the 3x3 Neighborhood proxy (no-op Decorate), proven by the determinism/-race/emit-once suite (GEN2-02)
**Research**: `.planning/research/v2-features-decoration.md` (Wave A — RNG + pipeline seam; "Architecture Patterns → the neighbor-aware placement seam, Option 1"; "Heightmap timing"); `.planning/research/v2-structures.md` ("the determinism hinge" + "Brownfield: how STARTS + PLACE slot into the worker"). The cross-chunk worker-lifecycle change is the central architectural cost of v2 — give it real weight.

### Phase 11: Feature Pipeline & Decoration Orchestration
**Goal**: The full `ConfiguredFeature`/`PlacedFeature`/`PlacementModifier` model is ported and wired — embedded feature/biome data loads, the placement-modifier chain runs with the exact RNG-draw order, and `applyBiomeDecoration` drives the 11 decoration steps across the 3×3 biome set. (No feature *bodies* yet — the orchestration that calls them.)
**Depends on**: Phase 10
**Requirements**: FEAT-01, FEAT-02
**Success Criteria** (what must be TRUE):
  1. The 8 load-bearing placement modifiers (`in_square`, `heightmap`, `count`/`RepeatingPlacement`, `rarity_filter`, `biome` filter, `height_range`, `surface_water_depth_filter`, the `PlacementFilter` base) are ported, and `PlacedFeature.place` runs the modifier chain as a single-threaded ordered flatMap fold consuming the same `WorldgenRandom` in jar-exact draw order
  2. The polymorphic `"type"`-tagged JSON parser loads the embedded 226 configured_feature + 262 placed_feature + 66 biome feature-lists and binds them (block-state refs resolved through the existing `level/block` `ToStateID`)
  3. `FeatureSorter.buildFeaturesPerStep` produces the deduped per-step global ordering (the IntSet+sort cross-biome dedup), built once at generator construction
  4. `applyBiomeDecoration` iterates the 11 `GenerationStep.Decoration` steps over the retained 3×3 biome set, calling `setFeatureSeed(decoSeed, globalIndex, step)` per feature and `placeWithBiomeCheck` — verifiable by tracing the per-feature seed/index for a known chunk
**Plans**: 3 plans
  - [x] 11-01-PLAN.md — Extend the codegen extract set + embed FS (226 configured_feature + 262 placed_feature + 66 biome JSONs) and build the polymorphic "type"-tagged ConfiguredFeature/PlacedFeature parser (typed-but-body-deferred AST, block-state refs via level/block ToStateID) (FEAT-02 data half)
  - [x] 11-02-PLAN.md — Port the 8 load-bearing placement modifiers + the 2 abstract bases + VerticalAnchor/HeightProvider/IntProvider + PlacedFeature.place as the single-threaded ordered flatMap fold, with JAR-exact draw-order tests (FEAT-01)
  - [x] 11-03-PLAN.md — FeatureSorter.buildFeaturesPerStep + applyBiomeDecoration wired into NoiseGenerator.Decorate + the D2 emit-rule resolution (Option Y: hold-until-neighborhood-complete) + the per-feature seed/index trace + live-Decorate determinism/emit-once suite (FEAT-02 orchestration half)
**Research**: `.planning/research/v2-features-decoration.md` (Wave B — placement modifiers; Wave E — biome→feature wiring; "Architecture Patterns → ConfiguredFeature/PlacedFeature/PlacementModifier layering", "the decoration-seed RNG chain", "applyBiomeDecoration outer loop"; "Don't Hand-Roll" for the embed set).

### Phase 12: Core Feature Types
**Goal**: The core overworld feature bodies generate — decoration ores, ground-cover patches, and the composite selectors that nest them — so a biome's non-tree vegetation + scattered ore set appears.
**Depends on**: Phase 11
**Requirements**: FEAT-03
**Success Criteria** (what must be TRUE):
  1. `OreFeature` (the `UNDERGROUND_ORES` decoration blobs, distinct from v1's noise `OreVeinifier`) places coal/iron/copper/etc. via `OreConfiguration` (size + target-state list + discard-on-air)
  2. `RandomPatchFeature` + `SimpleBlockFeature` place grass/flowers/dead-bushes/ground-cover via an inner `PlacedFeature` with jar-exact tries/spread draw order
  3. The `RandomSelector`/`SimpleRandomSelector`/`RandomBooleanSelector` composites resolve nested sub-features with the rng threaded through (so a biome's weighted feature choice works)
  4. `BlockPile`/`FallenTree`/`VegetationPatch` features place, so a biome's full non-tree ground cover + ore set generates deterministically per seed
**Plans**: 3 plans
  - [x] 12-01-PLAN.md — The BlockStateProvider hierarchy (Simple/Weighted/RuleBased + Noise/DualNoise) + the deferred-modifier completion (random_offset + block_predicate_filter & its BlockPredicate set — the vegetation tries/spread + ground gate 11-02 left) + the featureBody-registry dispatch refactor so the body plans run parallel (FEAT-03 prerequisites)
  - [x] 12-02-PLAN.md — OreFeature (UNDERGROUND_ORES blob via OreConfiguration + tag_match/block_match rule_test targets, distinct from v1's noise OreVeinifier) + SimpleBlockFeature + RandomPatchFeature bodies, deterministic-placement + cross-chunk-spill tested (FEAT-03 ore + ground-cover bulk)
  - [ ] 12-03-PLAN.md — The composite selectors (random_selector/simple_random_selector/random_boolean_selector) + the placeSubFeature recursion (Pitfall #9, the rng threaded through nested sub-features) + BlockPile/FallenTree/VegetationPatch bodies + the final 5x5 determinism/emit-once/Docker -race acceptance gate with real bodies live (FEAT-03 selectors + remaining types)
**Research**: `.planning/research/v2-features-decoration.md` (Wave C — the core Feature types; Wave D — BlockStateProvider hierarchy; "Common Pitfalls #7 feature-vs-noise ore double-placement, #9 composite/selector recursion").
**Planning note**: 26.2 has ZERO top-level `random_patch` configured_features — grass/flowers/dead-bush are `simple_block` configured_features whose placed_features scatter via `count` + `random_offset` + `block_predicate_filter` (modifiers 11-02 deferred). So the vegetation "tries/spread" (criterion 2) lives in the `random_offset` PLACEMENT modifier, closed in 12-01; `RandomPatchFeature` is still ported for jar completeness.

### Phase 13: Trees, Dungeon & Features Visual Gate
**Goal**: Every overworld biome grows its correct vanilla tree set and the dungeon places — and a real vanilla 26.2 client exploring the world confirms the full per-biome vegetation + decoration ores generate, deterministic per seed.
**Depends on**: Phase 12
**Requirements**: FEAT-04, FEAT-05, FEAT-06
**Success Criteria** (what must be TRUE):
  1. `TreeFeature` is ported with its trunk placers (Straight/Forking/Fancy/DarkOak/MegaJungle/...), foliage placers (Blob/Spruce/Pine/Acacia/Bush/Fancy/DarkOak/...), tree decorators, and the `BlockStateProvider` hierarchy (Simple/Weighted/RuleBased/Noise) — so each overworld biome grows its correct vanilla tree set (oak/birch/spruce/jungle/acacia/dark_oak/etc.)
  2. The dungeon (`MonsterRoomFeature`, a feature not a structure) is ported and places via its placed_feature (cobble room + spawner + chests as blocks)
  3. **VISUAL GATE (autonomous:false)**: a real vanilla 26.2 client exploring the world sees the FULL vanilla per-biome vegetation set (biome-correct trees, grass, flowers, cactus/cane/pumpkins, mushrooms) + decoration ores, generated by the ported pipeline — and the same seed reproduces the same world (human-verified visual determinism, no capture-diff — worldgen adds no new wire surface)
**Plans**: 4 plans
  - [x] 13-01-PLAN.md — Port TreeFeature.place + StraightTrunkPlacer + BlobFoliagePlacer + TreeConfiguration parse (the REAL oak.json shape: below_trunk_provider rule_based, bare two_layers_feature_size, ignore_vines) + the OPTIONAL root_placer field/hook so oak/birch forests render, wired to the cross-chunk Neighborhood + the Phase-12 selector recursion (FEAT-04)
  - [x] 13-02-PLAN.md — Port the COMMON overworld trunk/foliage placers (Forking/Fancy/DarkOak/MegaJungle-trunk + Spruce/Pine/Acacia/Bush/jungle_foliage/...) + the common tree decorators (AlterGround/Beehive/Cocoa/vines) so the common biomes grow their correct tree set (FEAT-04)
  - [x] 13-03-PLAN.md — Port the SPECIAL-biome trees the 4 extra generatable biomes need — cherry, mangrove (+ the RootPlacer subsystem), azalea (bending trunk), pale_oak (pale_moss/creaking_heart) + randomized_int_state_provider — + THE all-overworld-tree-configs-decode completeness guard, so every generatable biome grows its vanilla tree 1:1 (FEAT-04)
  - [x] 13-04-PLAN.md — Port the MonsterRoomFeature dungeon (cobble room + spawner + chests as blocks, loot deferred) + the automated full-features acceptance (full tree set+dungeon+selector+5x5 determinism+Docker -race) + THE autonomous:false real-client VISUAL GATE (FEAT-05, FEAT-06) — visual gate APPROVED 2026-06-25 (trees + vines confirmed on a real client)
**Research**: `.planning/research/v2-features-decoration.md` (Wave D — tree placers + state providers; "StraightTrunkPlacer" code example); `.planning/research/v2-structures.md` ("Tier 0 — Dungeon … NOT a Structure" — the dungeon is `MonsterRoomFeature`, belongs here, not the structure pipeline).
**UI hint**: yes

### Phase 14: Structure Pipeline & Temples
**Goal**: The two-phase structure pipeline and the `StructurePiece` machinery are ported and shaken down end-to-end on the simplest true structure — the desert pyramid generates in vanilla positions, with the other single-piece scattered temples following as cheap add-ons.
**Depends on**: Phase 13 (reuses the Phase 10 foundation + the Phase 11–13 feature-step orchestration that PLACE hooks into via `applyBiomeDecoration`)
**Requirements**: STRUCT-01, STRUCT-02
**Success Criteria** (what must be TRUE):
  1. The two-phase pipeline is ported: STRUCTURE_STARTS (decide WHERE per chunk, cache a `StructureStart` keyed by packed chunk pos in a `world/structure` cache), STRUCTURE_REFERENCES (8-chunk-radius bbox-intersect scan), and PLACE (`placeInChunk` clipping each piece write to the target chunk's writable box)
  2. The placement math is bit-exact to the jar: `getPotentialStructureChunk` (floorDiv + salt-in-seed), the spread types, the four `probabilityReducer` variants, `isStructureChunk`
  3. The `StructurePiece` bounding-box-tree machinery is ported (`addChildren` recursion, `findCollisionPiece`, `postProcess`, `placeBlock`/`generateBox`/`fillColumnDown` with rotation/mirror)
  4. The desert pyramid (single-piece, hardcoded geometry, 0 .nbt) generates in vanilla positions as the pipeline shakedown; jungle temple / igloo / swamp hut follow as cheap single-piece add-ons
**Plans**: 3 plans
  - [x] 14-01-PLAN.md — The structure PIPELINE: the placement math (getPotentialStructureChunk floorDiv+salt-in-seed + spread types + the 4 probabilityReducers + isStructureChunk) + the `world/structure` StructureStart cache (pure-memoized, singleflight-deduped) + the 8-radius REFERENCES scan + the heightmap-at-STARTS column sampler (router preliminary-surface) + the two-pass worker seam (STARTS/REFERENCES/PLACE-hook), ZERO blocks placed (STRUCT-01)
  - [x] 14-02-PLAN.md — The StructurePiece machinery (Piece tree + placeBlock-with-clip/generateBox/fillColumnDown/createChest/addChildren + rotation/mirror) + StructureStart.placeInChunk + the DESERT PYRAMID shakedown (single-piece hardcoded geometry, the first structure end-to-end: STARTS→cache→REFERENCES→PLACE→postProcess, cross-chunk idempotent, 5x5 determinism green with blocks placing) (STRUCT-02)
  - [x] 14-03-PLAN.md — The cheap temple follow-ons: jungle temple + igloo (the first addChildren multi-piece — dome + probabilistic basement) + swamp hut + the automated structures-pipeline acceptance (all 4 temples at expected chunks per-salt + cross-chunk idempotence + STARTS purity + 5x5/emit-once byte-identity + Docker -race) — NO visual gate (Phase 16) (STRUCT-02)
**Research**: `.planning/research/v2-structures.md` (Tier 1 — Desert Pyramid…; "Architecture Patterns → the two-phase pipeline", "the StructurePiece bounding-box tree", "the structure-start cache", "Brownfield: how STARTS + PLACE slot into the worker"; "Common Pitfalls #1–3").

### Phase 15: Mineshaft & Stronghold
**Goal**: The two recursive hardcoded-geometry structures generate in vanilla positions — the mineshaft via its frequency-reduction placement path, the stronghold via its unique concentric-rings placement — both reusing the Phase 14 piece machinery.
**Depends on**: Phase 14
**Requirements**: STRUCT-03, STRUCT-04
**Success Criteria** (what must be TRUE):
  1. The mineshaft is ported (multi-piece recursive corridor/crossing/room assembly, hardcoded geometry, the `legacy_type_3` frequency-reduction placement path); chests place as block + tag (loot deferred to v3)
  2. The stronghold's `concentric_rings` placement is ported — the ~128 biome-validated ring positions precomputed once at worldgen-state init, `isPlacementChunk` = ring-list contains
  3. The stronghold's recursive piece set (corridors/stairs/portal room/library) is ported reusing the Phase 14 `StructurePiece`/`addChildren` machinery
  4. Both structures generate in vanilla positions and shapes, deterministic per seed (verified against a known-seed reference)
**Plans**: TBD
**Research**: `.planning/research/v2-structures.md` (Tier 2 — Mineshaft; Tier 3 — Stronghold; "Common Pitfalls #4 mineshaft special placement, #5 stronghold rings are global"; the four `probabilityReducer` variants).

### Phase 16: Village Jigsaw & Structures Visual Gate
**Goal**: The hardest structure — the data-driven village jigsaw — generates per biome variant via the full template/`.nbt`/pool/`Placer` system, and a real vanilla 26.2 client confirms all v2 structures generate in vanilla positions, deterministic per seed.
**Depends on**: Phase 15
**Requirements**: STRUCT-05, STRUCT-06
**Success Criteria** (what must be TRUE):
  1. `.nbt` `StructureTemplate` parsing is ported (gzip+NBT via the existing nbt package; rotation/mirror applied at place time; processor application), and the embedded 483 village .nbt + 74 template_pool JSON + processor lists load
  2. The bounded-BFS `JigsawPlacement.Placer` is ported (max-depth + max-distance-from-center + VoxelShape collision, the `SequencedPriorityIterator` queue ordering) with `SinglePoolElement`/`LegacySinglePoolElement` instantiation and jigsaw-block alignment math
  3. Villages generate in vanilla positions per biome variant, deterministic per seed
  4. **VISUAL GATE (autonomous:false)**: a real vanilla 26.2 client exploring the world finds vanilla-positioned, structurally-correct mineshafts, desert pyramids/jungle temples/igloos/swamp huts, strongholds, and villages — and the same seed reproduces the same structures (human-verified visual determinism, no capture-diff — worldgen adds no new wire surface)
**Plans**: TBD
**Research**: `.planning/research/v2-structures.md` (Tier 4 — Village; "Architecture Patterns → JigsawPlacement"; "Common Pitfalls #6 jigsaw recursion bounds, #7 .nbt parsing details"; "Don't Hand-Roll" for the embed set; "Deferrals" — loot/afterPlace-beard/NBT-persistence pushed to v3).

## Progress

**Execution Order:**
Phases execute in numeric order: 10 → 11 → 12 → 13 → 14 → 15 → 16

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 10. Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap | 3/3 | Complete   | 2026-06-25 |
| 11. Feature Pipeline & Decoration Orchestration | 3/3 | Complete   | 2026-06-25 |
| 12. Core Feature Types | 3/3 | Complete | 2026-06-25 |
| 13. Trees, Dungeon & Features Visual Gate | 4/4 | Complete   | 2026-06-25 |
| 14. Structure Pipeline & Temples | 3/3 | Complete   | 2026-06-25 |
| 15. Mineshaft & Stronghold | 0/0 | Not started | - |
| 16. Village Jigsaw & Structures Visual Gate | 0/0 | Not started | - |
