---
phase: 13-trees-dungeon-features-gate
plan: 03
subsystem: worldgen
tags: [trees, root-placer, mangrove, cherry, azalea, pale-oak, trunk-placer, foliage-placer, tree-decorator, randomized-int-state-provider, feat-04, protocol-776]

# Dependency graph
requires:
  - phase: 13-trees-dungeon-features-gate
    plan: 01
    provides: "the TrunkPlacer/FoliagePlacer/RootPlacer bases + the optional root_placer field/hook, TreeConfiguration parse, PlaceTree (the pure assembly), the live 'tree' featureBody"
  - phase: 13-trees-dungeon-features-gate
    plan: 02
    provides: "the common overworld trunk/foliage roster (incl. dark_oak reused by pale_oak), the TreeDecorator subsystem + DecoratorContext + ParseTreeDecorator, the 26.2 doPlace draw order + the placed-log/leaf collectors, ThreeLayersFeatureSize + the IntProvider seam"
  - phase: 12-core-feature-types
    provides: "ParseProvider (simple/weighted/rule_based) + ResolveBlockStateJSON, the featureBody dispatch seam, the random_selector recursion"
provides:
  - "the RootPlacer subsystem: MangroveRootPlacer (getTrunkOrigin samples trunk_offset_y; placeRoots grows the jar-exact root columns through #mangrove_roots_can_grow_through with the mud->muddy_mangrove_roots swap + the above-root moss carpet)"
  - "the special-biome trunk placers: cherry_trunk_placer, upwards_branching_trunk_placer (mangrove), bending_trunk_placer (azalea)"
  - "the special-biome foliage placers: cherry_foliage_placer (the lacy canopy + hanging-leaves), random_spread_foliage_placer (mangrove + azalea)"
  - "the special decorators: AttachedToLeaves (mangrove propagules), PaleMoss (pale_hanging_moss drape), CreakingHeart (the surrounded-log heart)"
  - "randomized_int_state_provider (the 12-01-deferred provider) + the weighted_list IntProvider (cherry branch_count)"
  - "PlaceTree reworked to the EXACT 26.2 doPlace order with the validity scan internalised (so trunk_offset_y draws between foliageRadius and the scan)"
  - "TestAllOverworldTreeConfigsDecode — the FEAT-04 completeness guard (every generatable-overworld tree config decodes end-to-end)"
affects: [13-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RootPlacer two-phase seam: getTrunkOrigin (the trunk_offset_y draw, BEFORE the scan) + placeRoots (the root simulation, AFTER the scan) match the jar doPlace order; PlaceTree internalised the scan so the draw sits in the right place"
    - "randomized_int_state_provider re-resolves the source {Name,Properties} with the property overridden (the source is always simple_state_provider in the data) rather than reflecting over the typed Block"
    - "The hanging-leaves / moss-hanger isSet membership reads the per-tree accum leafSet (the FoliageSetter equivalent) so cherry's placeLeavesRowWithHangingLeavesBelow draws jar-exact"
  modified: []

key-files:
  created:
    - "world/levelgen/feature/tree_special.go — the special trunk/foliage placers (cherry/upwards_branching/bending + cherry_foliage/random_spread) + the RootPlacer subsystem (MangroveRootPlacer) + the special decorators (AttachedToLeaves/PaleMoss/CreakingHeart) + the special-biome Parse* constructors, each on the 13-01 bases"
    - "world/levelgen/feature/tree_special_test.go — per-special-placer/foliage/root/decorator draw-order + determinism tests + TestAllOverworldTreeConfigsDecode (the completeness guard)"
  modified:
    - "world/levelgen/feature/tree.go — RootPlacer interface split (getTrunkOrigin + placeRoots); PlaceTree reworked to the doPlace order with the scan internalised; the cherry/upwards/bending + cherry/random_spread Parse* arms + the mangrove_root_placer arm now resolve; weighted_list IntProvider added"
    - "world/levelgen/feature/provider.go — randomized_int_state_provider ported (the 12-01 deferred arm is now a real case)"
    - "world/levelgen/feature/tree_decorator.go — ParseTreeDecorator resolves attached_to_leaves/pale_moss/creaking_heart"
    - "world/feature_tree.go — the body hands the anchor + treeHeight to PlaceTree (which now runs the scan + the root hook + the decorator loop internally)"
    - "world/feature_tree_test.go — TestCherryTree/TestMangroveTreeWithRoots/TestPaleOakWithPaleMoss/TestAzaleaTree + TestPerSpecialBiomeSmoke + the realistic mud floor"
    - "world/levelgen/feature/provider_test.go — TestRandomizedIntStateProvider"
    - "world/levelgen/feature/{tree_test,tree_placers_test,tree_decorator_test}.go — the 13-01/13-02 'special STILL errors -> 13-03' assertions flipped to 'now decodes' (the deferred path is closed)"

key-decisions:
  - "PlaceTree reworked to the exact 26.2 doPlace order (foliageHeight -> foliageRadius -> trunk_offset_y -> scan -> roots -> trunk -> foliage -> decorators); the getMaxFreeTreeHeight scan moved from world/feature_tree.go INTO PlaceTree so the trunk_offset_y draw (RootPlacer.getTrunkOrigin) sits between foliageRadius and the scan. Oak/birch (no root, constant providers) are unchanged (0 extra draws)."
  - "RootPlacer interface split into getTrunkOrigin (samples trunk_offset_y before the scan) + placeRoots (grows roots from the ORIGINAL anchor, not the shifted origin, after the scan) — the jar's doPlace order, where the trunk floats on root stilts above the anchor."
  - "randomized_int_state_provider sets the int property by re-resolving the source {Name,Properties} with `property` overridden (the data's source is always simple_state_provider) rather than reflecting over the typed Block — faithful and allocation-light; the source draw is still consumed to keep the rng in lockstep."
  - "PaleMoss's ground pale_moss_patch is a NESTED vegetation configured-feature needing the chunk generator (which the pure DecoratorContext does not carry); the ground gate nextFloat is consumed (rng lockstep) but the patch is not placed (Known Stub). The trunk/leaves pale_hanging_moss drape — the rendered surface — is faithful."

patterns-established:
  - "Special-placer roster derived from the four embedded special configs (the provider/common-placer precedent): read each trunk/foliage/root/decorator type, port EXACTLY that set jar-exact, close the deferred path"
  - "The completeness guard (TestAllOverworldTreeConfigsDecode) is the objective acceptance: iterate every generatable-overworld tree config + assert decode — the analogue of 12-02's all-ore-configs-decode guard"

requirements-completed: [FEAT-04]

# Metrics
duration: ~2h
completed: 2026-06-25
---

# Phase 13 Plan 03: Special-Biome Trees + the RootPlacer Subsystem Summary

**The four special-biome trees (cherry_grove cherry, mangrove_swamp mangrove + the RootPlacer subsystem, lush_caves azalea, pale_garden pale_oak) ported jar-exact + the randomized_int_state_provider, closing FEAT-04 to the FULL vanilla overworld tree set — every generatable biome now grows its correct vanilla tree, proven by the all-overworld-tree-configs-decode completeness guard.**

## Performance

- **Duration:** ~2h
- **Started:** 2026-06-25
- **Completed:** 2026-06-25
- **Tasks:** 2 (both TDD)
- **Files modified:** 2 created + 6 modified

## Accomplishments

### The RootPlacer subsystem (the headline new work)
`MangroveRootPlacer` (javap -c against `rootplacers.{RootPlacer, MangroveRootPlacer, MangroveRootPlacement, AboveRootPlacement}`): `getTrunkOrigin` samples `trunk_offset_y` (uniform{1,3}/{3,7}) so the trunk floats on root stilts above the anchor; `placeRoots` walks from the anchor up to the trunk origin, then grows root columns DOWN through `#mangrove_roots_can_grow_through` (`simulateRoots` + `potentialRootPositions` — the per-step `nextFloat` skew / `nextBoolean` draws, bounded by `max_root_length 15`/`max_root_width 8`), swapping mud → `muddy_mangrove_roots` and draping a `moss_carpet` above each root per `above_root_placement_chance`. The 13-01 optional `root_placer` field + hook are now filled.

### Special trunk/foliage placers (jar-exact)
- **cherry_trunk_placer**: the trunk + the weighted `branch_count` (1/2/3) arching branches (the `branch_start`/`branch_horizontal_length`/`branch_end_offset` draws + the per-step `nextFloat` up/down arch walk).
- **upwards_branching_trunk_placer** (mangrove): the column + the per-log `place_branch_per_log_probability` branch (`extra_branch_steps`/`extra_branch_length`).
- **bending_trunk_placer** (azalea): the straight portion (the per-row `nextInt(2)` step) then the `bend_length` bend in a random horizontal direction.
- **cherry_foliage_placer**: the lacy wide canopy (the `corner_hole`/`wide_bottom_layer_hole`/`hanging_leaves` `nextFloat` draws + `placeLeavesRowWithHangingLeavesBelow` reading the accum leafSet for the `isSet`/extension draws).
- **random_spread_foliage_placer** (mangrove + azalea): `leaf_placement_attempts` scatter, 6 `nextInt` draws per attempt.

### The deferred provider + the special decorators
- **randomized_int_state_provider** (the 12-01 deferred type): draws the source state, then randomizes one int property (mangrove_propagule `age` via uniform{0,4}) by re-resolving `{Name,Properties}` with the property overridden.
- **AttachedToLeaves** (mangrove propagules under leaves per probability/`directions`/`exclusion_radius`/`required_empty_blocks`), **PaleMoss** (the `pale_hanging_moss` trunk/leaves drape), **CreakingHeart** (the `creaking_heart{dormant,natural}` in the first fully-log-surrounded trunk cell).
- **weighted_list IntProvider** (cherry `branch_count`).

### The completeness guard (the objective acceptance)
`TestAllOverworldTreeConfigsDecode` iterates the full generatable-overworld `tree` roster — oak/birch/spruce/pine/jungle/acacia/dark_oak/mega variants (13-01/13-02) **plus** cherry/cherry_bees/mangrove/tall_mangrove/azalea/pale_oak/pale_oak_creaking/pale_oak_bonemeal — and asserts every one decodes end-to-end. The four special biomes are explicitly asserted present + decoding. No loud-error/decode-or-skip path remains for any production overworld tree.

## Derived Special Roster (from the four embedded configs — the data is authoritative)

| config | trunk | foliage | root | decorators |
|--------|-------|---------|------|------------|
| cherry | **cherry** | **cherry** | — | (cherry_bees adds beehive) |
| mangrove / tall_mangrove | **upwards_branching** | **random_spread** | **mangrove_root_placer** | leave_vine + **attached_to_leaves** + beehive |
| azalea_tree | **bending** | **random_spread** | — (rooted_dirt below) | — |
| pale_oak | dark_oak (13-02) | dark_oak (13-02) | — | **pale_moss** |
| pale_oak_creaking | dark_oak | dark_oak | — | pale_moss + **creaking_heart** |

## Test Results (all green)

- `TestCherryTrunkPlacer / TestUpwardsBranchingTrunkPlacer / TestBendingTrunkPlacer` (geometry + draw-order determinism)
- `TestCherryFoliagePlacer / TestRandomSpreadFoliagePlacer` (shape + determinism)
- `TestMangroveRootPlacer` (the trunk_offset_y shift + the root columns + determinism)
- `TestRandomizedIntStateProvider` (the two-stage draw + the exact propagule age)
- `TestAttachedToLeaves / TestPaleMoss / TestCreakingHeart` (draw-pinned placement)
- `TestAllOverworldTreeConfigsDecode` (the completeness guard — every generatable overworld config decodes)
- `TestCherryTree / TestMangroveTreeWithRoots (logs+roots+propagules) / TestPaleOakWithPaleMoss / TestAzaleaTree / TestPerSpecialBiomeSmoke` (the four biomes resolve through the live body to real trees)
- The 13-01/13-02 oak/birch/common determinism + emit-once suites STILL green with the special trees live.

## Task Commits

1. **Task 1: the RootPlacer subsystem + the special-biome trunk/foliage placers + completeness guard** - `0cd18bbe` (feat, TDD)
2. **Task 2: the randomized_int_state_provider draw test + the per-special-biome body smokes** - `c2c6f9c1` (test, TDD)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] PlaceTree had to be reworked to internalise the validity scan**
- **Found during:** Task 1 (the RootPlacer ordering).
- **Issue:** The jar `TreeFeature.doPlace` samples `trunk_offset_y` (`RootPlacer.getTrunkOrigin`) BETWEEN `foliageRadius` and the `getMaxFreeTreeHeight` scan, then runs `placeRoots` AFTER the scan. 13-01's split ran the scan in `world/feature_tree.go` BEFORE `PlaceTree`, leaving no place to insert the `trunk_offset_y` draw in the jar position.
- **Fix:** Reworked `PlaceTree` to the exact doPlace order — `foliageHeight -> foliageRadius -> getTrunkOrigin (trunk_offset_y) -> getMaxFreeTreeHeight (internalised) -> placeRoots -> placeTrunk -> foliage -> decorators` — and moved `maxFreeTreeHeight` from `world/feature_tree.go` into `feature/tree.go` (it is a pure read scan). `PlaceTree`'s signature changed from `(…, freeHeight, origin)` to `(…, minFree, pos)`. Oak/birch (no root, constant providers) are 0-draw for the new steps, so their sequence is unchanged (the 13-01/13-02 determinism tests stay green).
- **Files modified:** `world/levelgen/feature/tree.go`, `world/feature_tree.go`, `world/levelgen/feature/tree_test.go` (the `TestPlaceTreeRootHookNoOp` minFree semantics).
- **Committed in:** `0cd18bbe`.

**2. [Rule 1 - Bug] The mangrove smoke needed a realistic floor (root termination)**
- **Found during:** Task 2 (the mangrove body smoke aborted every seed).
- **Issue:** `simulateRoots` returns false when the root branch exceeds `max_root_length` (15). A test floor of solid mud (all `can_grow_through`) let the roots grow down forever → exceed 15 → the whole tree aborted. Vanilla mangrove roots terminate at the stone below the mud.
- **Fix:** The smoke floor is now a 2-deep mud cap over stone — the roots grow through the mud and stop at the stone (NOT in `#mangrove_roots_can_grow_through`), so `simulateRoots` terminates. This is test-fixture realism, not a production change.
- **Files modified:** `world/feature_tree_test.go` (`fillMudFloor`).
- **Committed in:** `c2c6f9c1`.

**3. [Closure] The 13-01/13-02 deferred-path assertions flipped to "now decodes"**
- The 13-01/13-02 tests that asserted the special placers/decorators/root STILL error "-> 13-03" (`TestParseTrunkPlacerUnported`, `TestRootPlacerFieldOptional`, `TestParseTreeDecoratorUnported`, `TestCommonOverworldTreeConfigsDecode`) were flipped to assert the now-ported types DECODE — the deferred path this plan closes. Not a behavioural deviation; the expected closure of the milestone deferral.
- **Committed in:** `0cd18bbe`.

**Total deviations:** 2 auto-fixed (1 blocking, 1 bug) + 1 expected closure. All required for a jar-exact port + the milestone completeness; no scope creep.

## Known Stubs

**PaleMoss ground patch:** the `pale_moss` decorator's ground branch places the `PALE_MOSS_PATCH` configured-feature (a nested vegetation patch above the lowest log) — a recursive feature placement needing the chunk generator + the configured-feature graph, which the pure `DecoratorContext` (setBlock/read only) does not carry. The ground-gate `nextFloat` is consumed (rng lockstep preserved) but the patch is NOT placed. The rendered pale-oak surface — the trunk/leaves `pale_hanging_moss` drape — IS faithful. The ground moss carpet would be wired when the decorator seam gains generator access (a later phase); it does not affect the pale_oak tree shape or the FEAT-04 tree-set completeness.

**BeehiveDecorator bee occupants** (carried from 13-02): cherry_bees / mangrove beehive nests place the block + facing deterministically but skip the bee-occupant draws (a BlockEntity concern). Unchanged here.

## Issues Encountered

- The cherry `placeLeavesRowWithHangingLeavesBelow` needs a FoliageSetter `isSet` membership test; reused the per-tree `accum.leafSet` (which already records every placed leaf) as the equivalent, so the hanging-leaves extension draws stay jar-exact and deterministic.
- The mangrove `directions:[down]` decorator direction is a vertical step; modeled as a special `downDir` hdir carrying a `dy=-1` so `AttachedToLeaves` attaches the propagule downward without widening the 2D hdir for every placer.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- **13-04** (dungeon + the visual gate + the Docker `-race` gate): the full overworld tree set now renders 1:1; 13-04 registers the `monster_room` body and owns the `-race` gate once trees + dungeon are all live. No blockers; the `feature` package stays acyclic; no new deps; `go build ./...` + `CGO_ENABLED=0` clean; `go vet` clean.

## Self-Check: PASSED

- Both created files (`tree_special.go`, `tree_special_test.go`) + the SUMMARY exist on disk.
- Both task commits (`0cd18bbe`, `c2c6f9c1`) are in git history.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` clean; `go.mod`/`go.sum` unchanged (no new deps).
- `go vet ./world/levelgen/feature/ ./world/` clean.
- `go test ./world/levelgen/feature/ ./world/` green (the per-special placer/root/foliage/decorator/provider tests, TestAllOverworldTreeConfigsDecode, the four special-biome body smokes, and the 13-01/13-02 determinism gates).

---
*Phase: 13-trees-dungeon-features-gate*
*Completed: 2026-06-25*
