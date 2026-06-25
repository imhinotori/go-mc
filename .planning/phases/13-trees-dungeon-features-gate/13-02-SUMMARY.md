---
phase: 13-trees-dungeon-features-gate
plan: 02
subsystem: worldgen
tags: [trees, trunk-placer, foliage-placer, tree-decorator, feat-04, protocol-776]

# Dependency graph
requires:
  - phase: 13-trees-dungeon-features-gate
    plan: 01
    provides: "the TrunkPlacer/FoliagePlacer bases (getTreeHeight/placeLog/placeLeavesRow), TreeConfiguration parse + the optional root_placer hook, the live 'tree' featureBody, and PlaceTree (the pure assembly)"
  - phase: 12-core-feature-types
    provides: "ParseProvider (incl. rule_based + matching_block_tag predicates) + ResolveBlockStateJSON, the featureBody dispatch seam"
provides:
  - "the COMMON overworld trunk placers: ForkingTrunkPlacer (acacia), FancyTrunkPlacer (large oak), DarkOakTrunkPlacer (2x2, reused by pale_oak in 13-03), GiantTrunkPlacer (mega pine/spruce), MegaJungleTrunkPlacer"
  - "the COMMON overworld foliage placers: Acacia/Spruce/Pine/Bush/Fancy/DarkOak/Jungle(jungle_foliage_placer=mega_jungle)/MegaPine"
  - "the COMMON TreeDecorator subsystem: AlterGround (podzol), Beehive (bee-nest), Cocoa (jungle pods), LeaveVine + TrunkVine, plus ParseTreeDecorator + the DecoratorContext"
  - "the 26.2 TreeFeature.doPlace draw order in PlaceTree (getTreeHeight -> foliageHeight -> foliageRadius -> placeTrunk -> offset+createFoliage -> decorators) + the placed-log/placed-leaf collectors"
  - "ThreeLayersFeatureSize + the IntProvider (constant/uniform) seam + the beneath_tree_podzol_replaceable block tag + RandomSource.NextBoolean"
affects: [13-03, 13-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "FoliagePlacer createFoliage(radius,foliageHeight,offset) matches the 26.2 protected shape; the public-wrapper offset(rng) draw + the foliageHeight/foliageRadius pre-trunk draws live in PlaceTree (the jar doPlace order)"
    - "Generalized placeLeavesRow with a signed-skip seam (placeLeavesRowSigned) + the doubleTrunk fold (min(|c|,|c-1|)) so DarkOak's shouldSkipLocationSigned override and the +x/+z-wider large rows are both jar-exact"
    - "Decorators run AFTER trunk+foliage on the SAME threaded rng via the DecoratorContext (logs/leaves/roots collected in insertion order — the first log IS the trunk base, which Beehive/Cocoa/AlterGround min-Y logic relies on)"
    - "BlockStateProvider.OptionalState seam (rule_based getOptionalState) so AlterGround skips non-podzol-replaceable ground instead of overwriting it"

key-files:
  created:
    - "world/levelgen/feature/tree_placers.go — the common trunk + foliage placer roster on the 13-01 bases (Forking/Fancy/DarkOak/Giant/MegaJungle + Acacia/Spruce/Pine/Bush/Fancy/DarkOak/Jungle/MegaPine), each javap -c ported"
    - "world/levelgen/feature/tree_placers_test.go — per-placer geometry+determinism tests + TestCommonOverworldTreeConfigsDecode + TestThreeLayersFeatureSize"
    - "world/levelgen/feature/tree_decorator.go — TreeDecorator interface + DecoratorContext + AlterGround/Beehive/Cocoa/LeaveVine/TrunkVine + ParseTreeDecorator + the bee_nest/cocoa/vine/podzol state resolvers"
    - "world/levelgen/feature/tree_decorator_test.go — AlterGround/Beehive/Cocoa/vine draw+placement tests + the special-biome unported-error guard"
  modified:
    - "world/levelgen/feature/tree.go — reworked FoliagePlacer interface + placeLeavesRow + BlobFoliagePlacer to the 26.2 shape; reworked PlaceTree to the jar doPlace order + the decorator loop + the placed-position collectors; extended ParseTrunkPlacer/ParseFoliagePlacer to the common roster; added ThreeLayersFeatureSize + the IntProvider seam + the decorator decode"
    - "world/levelgen/feature/provider.go — RuleBasedStateProvider.getOptionalState + OptionalState seam (for AlterGround) + the beneath_tree_podzol_replaceable block tag (jar tag chain flattened)"
    - "world/levelgen/random.go — NextBoolean on the RandomSource interface + Xoroshiro impl (DarkOak foliage extra-row draw)"
    - "world/feature_tree.go — (unchanged body; PlaceTree internalized the draw-order rework)"
    - "world/feature_tree_test.go — the oak determinism re-run (the blob corner nextInt(2) draws corrected) + TestSpruceWithPodzol/OakBeesNest/JungleCocoaVines/PerBiomeTreeSmoke"
    - "world/levelgen/placement/providers_test.go — drawCounter.NextBoolean (interface widened)"

key-decisions:
  - "BlobFoliagePlacer.shouldSkipLocation corrected to the jar-exact corner draw `localX==range && localZ==range && (nextInt(2)!=0 || localY==0)` — 13-01 used a zero-draw approximation `|dx|==range && |dz|==range && range>0`. The real oak/birch foliage DOES draw nextInt(2) per widest-row corner; the corrected version is the determinism truth (Rule 1 fix). The 13-01 oak/birch tests were updated to replay these draws."
  - "jungle_foliage_placer (the JSON id) registers the MegaJungleFoliagePlacer CLASS (verified in FoliagePlacerType) — used by mega_jungle_tree; jungle_tree itself uses blob_foliage_placer (13-01). Ported under the JSON id 'jungle_foliage_placer' as JungleFoliagePlacer. There is no separate 'mega_jungle_foliage_placer' id."
  - "Decorator log/leaf positions collected in INSERTION order (not vanilla's HashSet iteration order): the first placed log is the trunk base, which Beehive/Cocoa/AlterGround's getFirst()/min-Y logic assumes — more faithful and deterministic than the JVM-impl-specific HashSet order."
  - "PlaceTree reworked to the EXACT 26.2 TreeFeature.doPlace draw order (foliageHeight + foliageRadius draw BEFORE the trunk). For oak/birch (constant providers) these are 0-draw so the 13-01 oak height sequence is preserved; for spruce/pine/megapine they DRAW, which is the jar truth."
  - "BeehiveDecorator places the bee_nest block + facing deterministically but SKIPS the bee-occupant draws (nextInt(3) bees, each nextInt(599)) — those populate a BeehiveBlockEntity which worldgen block placement here does not model. The block + facing are the rendered surface; the occupant draws are a BlockEntity concern for a later phase."

patterns-established:
  - "Pure-placer roster derived from the embedded data (Phase-12 provider precedent): enumerate the COMMON tree configs, read their trunk/foliage/decorator types, port EXACTLY that set; the special-biome arms stay loud-errors -> 13-03"
  - "Signed-skip seam for foliage corner rules: most placers fold dx/dz then apply shouldSkipLocation; DarkOak overrides the signed variant directly"

requirements-completed: [FEAT-04]

# Metrics
duration: ~2h
completed: 2026-06-25
---

# Phase 13 Plan 02: Common Overworld Trunk/Foliage Placers + Common Tree Decorators Summary

**The common overworld trunk placers (Forking/Fancy/DarkOak/Giant/MegaJungle) + foliage placers (Acacia/Spruce/Pine/Bush/Fancy/DarkOak/Jungle/MegaPine) + the common TreeDecorator subsystem (AlterGround podzol, Beehive nests, Cocoa pods, jungle vines), all derived jar-exact from the embedded common-overworld tree configs and wired into the 13-01 dispatch + tree body — so taiga grows spruce-with-podzol, savanna acacia, jungle jungle-trees-with-cocoa-and-vines, dark_forest dark-oak, and the mega variants.**

## Derived Common Roster (from the embedded configs — the data is authoritative)

Enumerated the COMMON `tree` configured_features and read each `trunk_placer`/`foliage_placer`/`minimum_size`/`decorators[]`:

| config | trunk | foliage | min_size | decorators |
|--------|-------|---------|----------|------------|
| oak/birch/jungle_tree_no_vine/super_birch_bees/swamp_oak | straight (13-01) | blob (13-01) | two_layers | (bees/leave_vine) |
| acacia | **forking** | **acacia** | two_layers | — |
| dark_oak | **dark_oak** | **dark_oak** | **three_layers** | — |
| fancy_oak/fancy_oak_bees | **fancy** | **fancy** | two_layers | (beehive) |
| spruce | straight | **spruce** | two_layers | — |
| pine | straight | **pine** | two_layers | — |
| jungle_bush | straight | **bush** | two_layers | — |
| jungle_tree | straight | blob | two_layers | **cocoa + trunk_vine + leave_vine** |
| mega_jungle_tree | **mega_jungle** | **jungle (jungle_foliage_placer)** | two_layers | trunk_vine + leave_vine |
| mega_pine / mega_spruce | **giant** | **mega_pine** | two_layers | **alter_ground** |
| birch_bees_002 | straight | blob | two_layers | **beehive** |

Ported EXACTLY that set. The data confirmed the plan's notes: mega_jungle uses `jungle_foliage_placer` (the MegaJungleFoliagePlacer class, NOT a non-existent mega-jungle id), jungle_tree uses blob (13-01), mega_spruce reuses the mega_pine foliage + giant trunk.

## Placers Ported (javap -c, temp/cache/26.2-inner.jar)

**Trunk:** ForkingTrunkPlacer (the single + optional-second diagonal fork), FancyTrunkPlacer (the BigTree branch-arc math: treeShape envelope, makeLimb line-walk, makeBranches, trimBranches), DarkOakTrunkPlacer (the leaning 2x2 + corner stubs), GiantTrunkPlacer (the solid 2x2 column), MegaJungleTrunkPlacer (Giant + the cos/sin branch arcs).

**Foliage:** AcaciaFoliagePlacer (3-row flat canopy), SpruceFoliagePlacer (the tapered cone, nextInt(2) start), PineFoliagePlacer (the symmetric tuft + the foliageRadius extra draw), BushFoliagePlacer (the small blob), FancyFoliagePlacer (per-branch rounded-disc blob), DarkOakFoliagePlacer (the wide canopy + the nextBoolean extra row + the signed-skip override), JungleFoliagePlacer (the shrinking dome, !large nextInt(2)), MegaPineFoliagePlacer (the expanding jittered cap).

## Decorators Ported

AlterGroundDecorator (the 5x5 podzol disc via getOptionalState over `beneath_tree_podzol_replaceable`), BeehiveDecorator (nextFloat<probability -> shuffle candidates -> bee_nest facing south), CocoaDecorator (lower-trunk per-face nextFloat<0.25 + nextInt(3) age cocoa), LeaveVineDecorator + TrunkVineDecorator (the jungle/swamp hanging + trunk vines).

## Per-Placer Test Results (all green)

`TestForkingTrunk / TestDarkOakTrunk / TestGiantTrunk / TestMegaJungleTrunk / TestFancyTrunk` (geometry + draw-order determinism), `TestSpruce/Pine/Acacia/DarkOak/Bush/Fancy/Jungle/MegaPine FoliagePlacer` (shape + determinism), `TestThreeLayersFeatureSize`, `TestCommonOverworldTreeConfigsDecode` (every common config decodes; special-biome trees STILL error -> 13-03). Decorator tests: `TestAlterGround/Beehive/Cocoa/LeaveVine/TrunkVine` + `TestParseTreeDecoratorUnported`. Body smokes: `TestSpruceWithPodzol / TestOakBeesNest / TestJungleCocoaVines / TestPerBiomeTreeSmoke`.

## Task Commits

1. **Task 1: common trunk + foliage placers + the dispatch/PlaceTree rework** - `4f9e9b91` (feat, TDD)
2. **Task 2: the common TreeDecorator tests + the body decorator smokes** - `e95e971d` (feat, TDD)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] BlobFoliagePlacer.shouldSkipLocation was a zero-draw approximation in 13-01**
- **Found during:** Task 1 (porting the 26.2 placeLeavesRow + the foliage shouldSkip rules).
- **Issue:** 13-01's `BlobFoliagePlacer.shouldSkip` used `|dx|==range && |dz|==range && range>0` with NO rng draw. The jar (javap -c shouldSkipLocation) is `localX==range && localZ==range && (nextInt(2)!=0 || localY==0)` — the oak/birch foliage DRAWS nextInt(2) on every widest-row corner cell. The 13-01 version is not the determinism truth.
- **Fix:** Ported the jar-exact rule (with the corner nextInt(2) draw) + the generalized 26.2 placeLeavesRow + the createFoliage(radius,foliageHeight,offset) shape. Updated the 13-01 oak/birch tests to replay these draws (the oak placement is now bit-identical to vanilla, and the determinism re-run proves it).
- **Files modified:** `world/levelgen/feature/tree.go`, `world/levelgen/feature/tree_test.go`, `world/feature_tree_test.go`.
- **Committed in:** `4f9e9b91`.

**2. [Rule 3 - Blocking] PlaceTree draw order had to be reworked to the 26.2 TreeFeature.doPlace order**
- **Found during:** Task 1 (the spruce/pine/megapine foliage placers sample foliageHeight/foliageRadius/offset from rng).
- **Issue:** 13-01's PlaceTree drew height -> trunk -> foliage with zero foliage draws (correct for oak's constant providers). The new placers REQUIRE foliageHeight + foliageRadius drawn BEFORE the trunk and a per-attachment offset() draw — the exact jar doPlace order — or the per-feature rng sequence diverges.
- **Fix:** Reworked PlaceTree to getTreeHeight -> foliageHeight -> foliageRadius -> (root) -> placeTrunk -> per-attachment offset+createFoliage -> decorators. Oak/birch (constant providers) stay 0-draw for the foliage sample so their sequence is unchanged.
- **Files modified:** `world/levelgen/feature/tree.go`.
- **Committed in:** `4f9e9b91`.

**3. [Rule 3 - Blocking] the beneath_tree_podzol_replaceable block tag + RandomSource.NextBoolean were missing**
- **Found during:** Task 1/2 (the mega_pine alter_ground decode + the DarkOak foliage nextBoolean draw).
- **Issue:** (a) The `alter_ground` provider's rule gates on `#minecraft:beneath_tree_podzol_replaceable`, which the Phase-12 rule-tag set did not carry -> mega_pine decode failed. (b) DarkOakFoliagePlacer draws `nextBoolean()`, which the `RandomSource` interface did not expose.
- **Fix:** Added the tag (flattened from the jar `#substrate_overworld -> #dirt/#mud/#moss_blocks/#grass_blocks` chain, constant-for-constant) and a `getOptionalState` seam for the rule-gated AlterGround placement; added `NextBoolean()` to the `RandomSource` interface (+ Xoroshiro impl + the test mock).
- **Files modified:** `world/levelgen/feature/provider.go`, `world/levelgen/random.go`, `world/levelgen/placement/providers_test.go`.
- **Committed in:** `4f9e9b91`.

**Total deviations:** 3 auto-fixed (1 bug, 2 blocking). All required for a jar-exact port of the mandated roster; no scope creep.

## Known Stubs

**BeehiveDecorator bee occupants:** the bee_nest BLOCK + facing land deterministically, but the bee-occupant population (the `nextInt(3)` bees, each `nextInt(599)`) is NOT drawn — it populates a `BeehiveBlockEntity` which worldgen block placement here does not model. This is an intentional scope boundary (block-entity occupant data is a later concern); the rendered surface (the nest block + facing) is faithful. No common config's tree shape depends on the occupant draws.

## Self-Check: PASSED

- All 4 created files + the modified files exist on disk; both task commits (`4f9e9b91`, `e95e971d`) are in git history.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` clean; `go.mod`/`go.sum` unchanged (no new deps).
- `go vet ./world/... ./world/levelgen/feature/` clean.
- `go test ./world/levelgen/feature/ ./world/` green (the per-placer geometry/determinism tests, the decorator draw/placement tests, the COMMON-tree-configs-decode guard, and the per-biome body smokes).

---
*Phase: 13-trees-dungeon-features-gate*
*Completed: 2026-06-25*
