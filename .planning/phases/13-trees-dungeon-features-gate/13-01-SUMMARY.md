---
phase: 13-trees-dungeon-features-gate
plan: 01
subsystem: worldgen
tags: [trees, tree-feature, trunk-placer, foliage-placer, tree-configuration, decoration, feat-04, protocol-776]

# Dependency graph
requires:
  - phase: 12-core-feature-types
    provides: "the featureBody dispatch seam (registerFeatureBody/bodyContext/placeState), the BlockStateProvider hierarchy (ParseProvider + RuleBasedStateProvider.withExisting), and the random_selector recursion (placeSubFeature)"
  - phase: 11-feature-decoration
    provides: "the Neighborhood 3x3 cross-chunk write proxy (heightmap-live SetBlock/GetBlock) + the placement context (GetHeight/MinY/Height)"
provides:
  - "TreeConfiguration parse (the REAL oak.json/birch.json field set) + the OPTIONAL root_placer field/hook"
  - "StraightTrunkPlacer.placeTrunk + the TrunkPlacer base (getTreeHeight/placeLog/setDirtAt)"
  - "BlobFoliagePlacer.createFoliage + the FoliagePlacer base (placeLeavesRow) + TwoLayersFeatureSize"
  - "feature.PlaceTree — the pure TreeFeature.place assembly (height -> roots-hook -> trunk -> foliage)"
  - "the live \"tree\" featureBody — the validity scan + the cross-chunk placement; the forest now renders"
  - "ParseProvider rule_based tolerance for an ABSENT fallback (identity fallback)"
affects: [13-02, 13-03, 13-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Pure placer/world seam: the trunk/foliage placers run against SetBlockFn/ReadFn callbacks in package feature (acyclic — no placement/world import); the live body supplies the closures over the Neighborhood"
    - "The TreeConfiguration carries an OPTIONAL nil root_placer field + a PlaceTree hook so 13-03 plugs mangrove_root_placer in without re-touching the struct"
    - "Conservative validTreePos (air-or-leaves replaceable) — never a false placement over solid ground (12-02 precedent)"

key-files:
  created:
    - "world/levelgen/feature/tree.go — TreeConfiguration parse + StraightTrunkPlacer + BlobFoliagePlacer + the placer bases + TwoLayersFeatureSize + RootPlacer interface/hook + PlaceTree (pure)"
    - "world/levelgen/feature/tree_test.go — placer draw-order + geometry + config-decode + unported-error tests"
    - "world/feature_tree.go — the live \"tree\" featureBody (validity scan + cross-chunk placement), registered via registerFeatureBody"
    - "world/feature_tree_test.go — deterministic placement, cross-chunk edge spill, validity abort, birch, and the selector->real-tree integration"
  modified:
    - "world/levelgen/feature/provider.go — rule_based_state_provider tolerates an absent fallback via an identity fallback (the 26.2 tree below_trunk_providers carry only rules)"

key-decisions:
  - "rule_based_state_provider absent-fallback -> identity fallback (keeps existing block): the REAL 26.2 oak.json/birch.json below_trunk_providers omit `fallback`, carrying only `rules`; the Phase-12 ParseProvider required it. Faithful semantics: the dirt rule matches every non-trunk block, so the fallback is reached only over an existing trunk (a no-change)."
  - "Conservative validTreePos = air-or-leaves replaceable (logs are NOT replaceable): two trees' foliage may interpenetrate (vanilla), but a trunk never grows through another trunk; never a false placement over solid ground."
  - "minTreeHeight=2 floor: the body declines a 1-log stub; on flat overworld ground trees always have full room, so this only triggers against a ceiling/overhang where a no-tree is correct."

patterns-established:
  - "Pure-placer + live-body seam for trees (mirrors the provider.go discipline): the algorithm in feature/, the Neighborhood wiring in world/"
  - "The height draw happens BEFORE the validity scan (jar order) so the per-feature rng sequence is correct even when the scan aborts"

requirements-completed: [FEAT-04]

# Metrics
duration: 38min
completed: 2026-06-25
---

# Phase 13 Plan 01: Trees (FEAT-04 headline) Summary

**TreeFeature.place + StraightTrunkPlacer + BlobFoliagePlacer + TreeConfiguration (the REAL oak.json shape) + the optional root_placer hook, wired to the cross-chunk Neighborhood — a biome's tree random_selector now grows a real oak/birch instead of no-op'ing, so the forest renders.**

## Performance

- **Duration:** ~38 min
- **Started:** 2026-06-25
- **Completed:** 2026-06-25
- **Tasks:** 2 (both TDD)
- **Files modified:** 4 created + 1 modified

## Accomplishments
- **The pure placers (Task 1):** `StraightTrunkPlacer.placeTrunk` (dirt-below + log column + one `FoliageAttachment`) and `BlobFoliagePlacer.createFoliage` (leaf rows with the corner trim), plus the `TrunkPlacer`/`FoliagePlacer` bases (`getTreeHeight` = base + nextInt(a+1) + nextInt(b+1); `placeLeavesRow`) and `TwoLayersFeatureSize.getSizeAtLayer` — all pure over `(rng, setBlock, read)`, no `placement`/`world` import (acyclic).
- **TreeConfiguration parse:** decodes the REAL embedded oak.json/birch.json — trunk/foliage providers, the `below_trunk_provider` (a `rule_based_state_provider`, NOT a `dirt_provider`/`force_dirt`), bare `two_layers_feature_size`, `ignore_vines`, empty `decorators`, and the **OPTIONAL** `root_placer` field (nil for oak/birch). `RootPlacer` interface + `parseRootPlacer` (errors loudly on `mangrove_root_placer` -> 13-03) + the `PlaceTree` root hook (no-op when nil) are wired now so 13-03 plugs mangrove in without re-touching the struct.
- **The live "tree" body (Task 2):** decodes the config, builds set/read closures over the 3x3 `Neighborhood`, binds the rule_based below-trunk provider to the live read, runs the `getMaxFreeTreeHeight` validity scan, and on success runs `feature.PlaceTree` — writing logs/leaves through the cross-chunk view (heightmap-live). Registered under `"tree"` via `registerFeatureBody` (no parse.go edit).
- **The forest renders:** a `random_selector` over an oak sub-feature resolves through the Phase-12 `placeSubFeature` recursion to a REAL oak (oak_log/oak_leaves placed) — the "tree" no-op is gone. Draw-order pinned to a 2-nextInt oracle; cross-chunk edge spill confirmed (18 leaves into the +x neighbor).

## Task Commits

1. **Task 1: TreeConfiguration + StraightTrunkPlacer + BlobFoliagePlacer (pure)** - `4070c732` (feat, TDD)
2. **Task 2: the live "tree" featureBody — the forest renders** - `b103cffb` (feat, TDD)

## Files Created/Modified
- `world/levelgen/feature/tree.go` - TreeConfiguration parse + the two canonical placers + the placer bases + TwoLayersFeatureSize + the RootPlacer interface/hook + `PlaceTree` (pure assembly) + exported accessors (`TrunkHeight`/`SizeAtLayer`/`PosFree`/`BelowTrunkWithExisting`)
- `world/levelgen/feature/tree_test.go` - `getTreeHeight`/`placeTrunk`/`createFoliage` draw-order + geometry, the oak/birch config decode, the unported-placer + optional-root-placer error tests
- `world/feature_tree.go` - the live `"tree"` featureBody: the `getMaxFreeTreeHeight` validity scan + the cross-chunk placement, registered via `registerFeatureBody`
- `world/feature_tree_test.go` - deterministic oak placement (oracle-pinned), cross-chunk edge spill, validity abort (no partial tree), birch, and the selector->real-tree integration proof
- `world/levelgen/feature/provider.go` - `rule_based_state_provider` now tolerates an absent `fallback` (the 26.2 tree below_trunk providers carry only `rules`) via an identity fallback

## Decisions Made
- See `key-decisions` frontmatter: absent-fallback identity provider, conservative validTreePos (air-or-leaves), the minTreeHeight stub floor, and the height-before-scan rng order.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] ParseProvider rejected the REAL oak.json below_trunk_provider (no fallback)**
- **Found during:** Task 1 (TreeConfiguration decode)
- **Issue:** The REAL embedded `oak.json`/`birch.json` `below_trunk_provider` is a `rule_based_state_provider` carrying ONLY `rules` (NO `fallback`). The Phase-12 `ParseProvider` rule_based arm required a `fallback`, so `ParseTreeConfiguration` errored ("empty block state provider") — blocking the whole plan's decode.
- **Fix:** Made the rule_based arm tolerate an absent `fallback` via an `identityStateProvider` (yields the EXISTING block — a no-change), with `RuleBasedStateProvider.GetState` special-casing it against the bound `existingAt`. This is the faithful semantics: the dirt rule's "not in cannot_replace_below_tree_trunk" predicate matches every non-trunk ground block, so the fallback is reached only over an existing trunk.
- **Files modified:** `world/levelgen/feature/provider.go`
- **Verification:** All pre-existing provider tests still green; the oak/birch configs decode; the below-trunk provider over stone yields dirt (the rule's then-provider).
- **Committed in:** `4070c732` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** The fix was required to decode the REAL config the plan mandates; it is a faithful port of the absent-fallback semantics and does not change any existing provider behavior. No scope creep.

## Issues Encountered
- A test-helper name collision (`fillFloor` already defined in `feature_patch_test.go`) — renamed mine to `fillTreeFloor`. Pure test plumbing, no production impact.
- The `placeLeavesRow` initial draft double-counted the row Y (the row center already carries it); corrected so leaves place at the center's Y and `localY` is passed only to the corner-trim predicate — caught by the `BlobFoliagePlacer` oracle test before commit.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- **13-02** (common-overworld placers + decorators): adds the `ParseTrunkPlacer`/`ParseFoliagePlacer` dispatch arms (fancy/forking/dark_oak/spruce/pine/...) + the `TreeDecorator` loop over `cfg.decoratorsRaw`. The dispatch + the decorator field are already wired; 13-02 fills the arms in its own files.
- **13-03** (special-biome trees + RootPlacer): plugs the concrete `mangrove_root_placer`/`azalea` bodies into the OPTIONAL `root_placer` field + the `PlaceTree` root hook (both already present, nil for oak/birch) + the cherry placers — WITHOUT re-touching the `TreeConfiguration` struct.
- **13-04** (dungeon + gate): registers the `monster_room` body and owns the Docker `-race` gate once trees + dungeon are all live.
- No blockers. The `feature` package stays acyclic; no new deps; `go build ./...` + `CGO_ENABLED=0` clean.

## Self-Check: PASSED

- All 4 created files + the SUMMARY exist on disk.
- Both task commits (`4070c732`, `b103cffb`) exist in git history.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` clean; `go.mod`/`go.sum` unchanged (no new deps).
- `go test ./world/levelgen/feature/ ./world/` green (incl. the heavier 5x5 determinism / emit-once suites with trees now placing).

---
*Phase: 13-trees-dungeon-features-gate*
*Completed: 2026-06-25*
