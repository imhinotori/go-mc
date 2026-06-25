---
phase: 12-core-feature-types
plan: 03
subsystem: worldgen
tags: [selector, composite-feature, sub-feature-recursion, placeSubFeature, block-pile, fallen-tree, vegetation-patch, decoration, determinism, race-gate]

# Dependency graph
requires:
  - phase: 12-core-feature-types
    plan: 01
    provides: "featureBody registry + bodyContext{view,reg} + registerFeatureBody, NextBoolean on LegacyRandomSource, BlockStateProvider hierarchy (ParseProvider), the *feature.Registry plumbed into the dispatch"
  - phase: 11-feature-pipeline-decoration
    provides: "Registry (ResolvePlaced/ParsePlacedFeature + cycle guard), placement.Bind fold, applyBiomeDecoration, Neighborhood 3x3 write proxy"
provides:
  - "random_selector / simple_random_selector / random_boolean_selector composite bodies (javap-exact draw order)"
  - "placeSubFeature recursion seam: re-decodes OPAQUE nested sub-features from cf.Config.Raw, resolves ref (ResolvePlaced) + inline (ParsePlacedFeature) through the plumbed registry, binds + folds the sub-feature's OWN modifiers with the SAME threaded rng (Pitfall #9)"
  - "block_pile / fallen_tree / vegetation_patch (+ waterlogged) bodies (javap-exact draw order; tree decorators deferred to Phase 13)"
  - "roster fix: speleothem/coral_claw/coral_mushroom/coral_tree/template added to recognizedFeatureTypes (real 26.2 inline selector sub-features 11-01 never parsed)"
  - "the final Phase-12 acceptance gate: TestFeatureBodiesProduceBlocks (live bodies write 5219 blocks) + the 5x5 reorder determinism + emit-once + Docker -race over ./world/... GREEN with all real bodies"
affects: [13-trees-structures]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "placeSubFeature: a sub-PlacedFeature resolved from Raw is bound via placement.Bind with a registry-dispatching placer (subFeaturePlacer) so its OWN modifiers re-apply with the threaded rng — selector->weighted->leaf resolves end-to-end with one deterministic rng"
    - "bodyContext.subDepth threads selector->selector recursion depth through the fixed featureBody signature (set around the bound Place, restored after) — a backstop over the registry's acyclic-by-construction cycle guard"
    - "defensive skip on a sub-feature bind/resolve failure (an unported Nether/cave modifier like environment_scan), matching applyBiomeDecoration's 'skip rather than crash mid-decoration' convention"
    - "minimal in-package IntProvider (constant/uniform — the only kinds the misc configs use) avoids exporting placement internals; replaceable tags flattened constant-for-constant from the jar tag defs"

key-files:
  created:
    - world/feature_selector.go
    - world/feature_selector_test.go
    - world/feature_misc.go
    - world/feature_misc_test.go
  modified:
    - world/feature_body.go
    - world/levelgen/feature/parse.go
    - world/decoration_test.go

key-decisions:
  - "Selector nested sub-features are NOT pre-parsed (11-01's walkBlockStates resolved only {Name,Properties} block-state leaves): each selector body RE-DECODES its config from cf.Config.Raw and resolves each sub-entry through bctx.reg (the 12-01 plumbing) — ref string via ResolvePlaced, inline {feature,placement} via ParsePlacedFeature(\"\", raw). This is the exact dependency that made 12-01's registry plumbing mandatory."
  - "Going live exposed a latent 11-01 roster gap: five inline selector sub-feature types (speleothem, coral_claw, coral_mushroom, coral_tree, template) were absent from recognizedFeatureTypes because 11-01 never parsed selector sub-features. All five are JAR-CONFIRMED real 26.2 feature classes; added to the roster (Rule 1/3 build-data fix)."
  - "placeSubFeature SKIPS a sub-feature whose modifier chain cannot bind (e.g. environment_scan, a deferred Nether/cave modifier a real selector sub-feature lists) rather than crashing the generator — the SAME convention applyBiomeDecoration uses (decoration.go). The selector's pick draw stands; the unported leaf-placement is Phase-13 work."
  - "random_boolean_selector calls the exported NextBoolean (next(1), one bit — 12-01's LCG primitive) via a booleanSource type-assertion at the call site (mirrors placement.gaussianSource); random.go is NOT edited, keeping wave-2 file-disjoint."
  - "Tree DECORATORS (attached_to_logs / trunk_vine / stump+log decorators) are the TreeDecorator subsystem deferred to Phase 13 — fallen_tree places the trunk/stump LOG blocks + their trunkProvider draws (the determinism contract) but not the decorator draws (documented)."
  - "vegetation_patch waterlogged shares the vegetation_patch body (same GROUND-placement draw order — the waterlogged variant only post-filters the placed set for the inner vegetation); replaceable tags (moss/lush_ground) resolved constant-for-constant from the jar, transitively flattening nested tags."
  - "The test leaf is registered under the recognized 'no_op' type (referenced by ZERO embedded configured_features; a real no_op carries an empty config -> the body's States[0] read returns no-op), so the global registration is behaviorally a true no-op for production while giving the selector tests a deterministic asserting leaf."

requirements-completed: [FEAT-03]

# Metrics
duration: ~95min
completed: 2026-06-25
---

# Phase 12 Plan 03: FEAT-03 Composite Selectors + Misc Bodies + Final Gate Summary

**The composite selectors with the sub-feature RECURSION (Pitfall #9: get it wrong and every forest collapses to one tree type) + the BlockPile/FallenTree/VegetationPatch bodies, all javap-exact on draw order, closing Phase 12 with the live-decoration + 5x5 determinism + Docker -race acceptance gate GREEN with every real FEAT-03 body placing blocks.**

## Accomplishments

- **The three composite selectors** ported javap-exact (`RandomSelectorFeature` / `SimpleRandomSelectorFeature` / `RandomBooleanSelectorFeature.place`): random_selector draws one `NextFloat()` per weighted entry in order until one passes (< chance) else the default; simple_random_selector draws ONE `NextIntN(N)`; random_boolean_selector draws `NextBoolean()` (next(1), one bit). Each re-decodes its OPAQUE config from `cf.Config.Raw` and resolves nested sub-features through the plumbed `bctx.reg` (ref via `ResolvePlaced`, inline via `ParsePlacedFeature`).
- **placeSubFeature** is the recursion seam (T-12-09): a resolved sub-`*PlacedFeature` is bound via `placement.Bind` — the SAME fold applyBiomeDecoration uses — with a registry-dispatching placer, then `bound.Place(ctx, rng, pos)` runs it. The rng IS the threaded selector rng, so the sub-feature's modifier draws AND body draws continue the deterministic sequence. A nested selector recurses and terminates (the registry's 11-01 cycle guard + a depth backstop).
- **block_pile / fallen_tree / vegetation_patch (+ waterlogged)** ported javap-exact on draw order: block_pile's bell-pattern (2 radius `nextInt` + per-cell `nextFloat*10 - nextFloat*6` bell / `0.031` scatter + dirt-path `nextBoolean`) in Cursor iteration order (x inner, z mid, y outer); fallen_tree's stump + `HORIZONTAL.getRandomDirection(nextInt 4)` + `logLength.sample-2` + `gap 2+nextInt(2)` + per-log trunkProvider line; vegetation_patch's two xz_radius samples + per-column edge/depth/extra-bottom draws + ground layers + `distributeVegetation` (`nextFloat < chance` -> vegetation_feature via placeSubFeature).
- **Roster fix** (latent 11-01 gap exposed by going live): added `speleothem`/`coral_claw`/`coral_mushroom`/`coral_tree`/`template` (JAR-CONFIRMED inline selector sub-feature types 11-01 never parsed) to `recognizedFeatureTypes`.
- **The final acceptance gate**: `TestFeatureBodiesProduceBlocks` runs the REAL `applyBiomeDecoration` over plains' full feature roster on a stone-filled 3x3 and confirms **5219 non-stone feature blocks** are written through the live dispatch (ore blobs + cover) — proving all FEAT-03 bodies (12-02 ore/simple_block/random_patch + 12-03 selectors/pile/fallen/veg_patch) place end-to-end. The Phase-11 `TestDecorationReorderIdentical` (5x5) + emit-once gates stay green; the **Docker `-race` gate over `./world/...` is GREEN** (no DATA RACE).

## Task Commits

1. **Task 1: composite selectors + placeSubFeature recursion** (+ roster fix) — `810c195e` (feat)
2. **Task 2: block_pile + fallen_tree + vegetation_patch bodies** — `daee2fc3` (feat)
3. **Task 3: final live-decoration + determinism acceptance gate** — `071b9812` (test)

## Files Created/Modified

- `world/feature_selector.go` — the 3 selectors + placeSubFeature + subFeaturePlacer + resolveSubFeature, registered.
- `world/feature_selector_test.go` — weighted pick / falls-to-default / uniform / boolean-branch / sub-modifier re-apply + draw-thread / nested-terminates / inline-vs-ref, all draw-pinned via parallel oracles.
- `world/feature_misc.go` — block_pile/fallen_tree/vegetation_patch(+waterlogged) bodies + the minimal IntProvider + the replaceable tag sets + conservative reads, registered.
- `world/feature_misc_test.go` — pile pattern / fallen line / veg disk / veg determinism / cross-chunk-edge, draw-pinned.
- `world/feature_body.go` — added `bodyContext.subDepth` (the selector recursion-depth counter).
- `world/levelgen/feature/parse.go` — 5 inline selector sub-feature types added to `recognizedFeatureTypes`.
- `world/decoration_test.go` — `TestFeatureBodiesProduceBlocks` (the live-write proof).

## Recursion + Determinism + -race Results

- **Selector recursion (Pitfall #9):** `TestSubFeatureModifiersReapply` + `TestSubFeatureModifiersDrawThread` pin that the picked sub-feature's OWN count modifier advances the SHARED rng (a broken thread fails the post-run fingerprint). `TestNestedSelectorTerminates` proves selector->selector->leaf threads one rng and terminates. Weighted pick / default-fallthrough / uniform / boolean branch each draw-pinned to a parallel oracle.
- **Live decoration:** `TestFeatureBodiesProduceBlocks` — 5219 feature blocks written by the real pipeline.
- **5x5 determinism + emit-once:** `TestDecorationReorderIdentical`, `TestEmitOnce`, `TestEmitOnceUnderHold` all PASS with every real body registered.
- **Docker -race over ./world/... — GREEN (no DATA RACE).** Tail:
  ```
  ok  github.com/imhinotori/sulfur/world             478.162s
  ok  github.com/imhinotori/sulfur/world/levelgen     1.024s
  ok  github.com/imhinotori/sulfur/world/levelgen/feature   3.073s
  ok  github.com/imhinotori/sulfur/world/levelgen/placement 2.868s
  ... (all ./world/... packages ok)
  ```
- **Builds:** `go build ./...` + `CGO_ENABLED=0 go build ./...` exit 0; **go.mod/go.sum unchanged** (no new deps).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1/3 - Bug] Roster gap: 5 inline selector sub-feature types missing from recognizedFeatureTypes**
- **Found during:** Task 1 (TestNoiseGenImplementsGenerator panicked once the selectors went live)
- **Issue:** 11-01's `walkBlockStates` only resolved `{Name,Properties}` block-state leaves and NEVER parsed selector sub-features, so `speleothem`/`coral_claw`/`coral_mushroom`/`coral_tree`/`template` (real 26.2 features referenced inline inside selector sub-features) were absent from the recognized roster. The selector recursion forced these inline sub-features to parse, which errored loudly ("unknown configured_feature type").
- **Fix:** Added all five (JAR-CONFIRMED feature classes) to `recognizedFeatureTypes` in parse.go, with cited comments.
- **Files modified:** world/levelgen/feature/parse.go
- **Commit:** 810c195e

**2. [Rule 1 - Bug] placeSubFeature must SKIP an unbindable sub-feature, not panic**
- **Found during:** Task 1 (a sub-feature's placement listed `environment_scan`, a deferred Nether/cave modifier 11-02 doesn't bind)
- **Issue:** My initial placeSubFeature panicked on a `placement.Bind` error, crashing the whole generator mid-decoration.
- **Fix:** Skip the sub-feature defensively (return false), the SAME convention `applyBiomeDecoration` already uses for a top-level unbindable feature (decoration.go: "skip rather than crash mid-decoration"). The selector's pick draw stands; the unported leaf-placement is Phase-13 work. Same treatment applied to a sub-feature resolve error (a genuine build-data gap is still caught loudly at construction by LoadAll).
- **Files modified:** world/feature_selector.go
- **Commit:** 810c195e

### Scope notes (not deviations)

- **Tree decorators deferred:** fallen_tree's `log_decorators`/`stump_decorators` (attached_to_logs/trunk_vine/etc.) are the TreeDecorator subsystem Phase 13 owns. fallen_tree places the trunk/stump LOG blocks + their trunkProvider draws (the determinism contract); the decorator draws are deferred with that port. block_pile's `rotated_block_provider` (the hay pile) is likewise a deferred Phase-13 provider — that body skips (no-op) when ParseProvider returns unported, while simple/weighted-provider piles (the ice/blocks piles) place fully.
- **Conservative worldgen reads:** vegetation_patch / block_pile / fallen_tree ground gates use 12-01's conservative `isFaceSturdy` (not air, not fluid) since the full BlockBehaviour face-occlusion shape is unavailable mid-worldgen. The DRAW ORDER is jar-exact regardless (the determinism contract); block placement over real terrain is exercised by the stone-filled gate test.
- **12-02 ran in parallel and landed (commits 2283dbcd ore, fa9b139b/3a153a89 simple_block+random_patch).** File-disjoint as designed; the only coordination was the shared `world` test package, where 12-02 renamed its floor helper to `fillPatchFloor` to avoid a collision — this plan defines its own `fillMiscFloor`. All bodies register from disjoint files via `registerFeatureBody`.

## Issues Encountered

- `bodyPlacerFor` (referenced in the plan's interface block as a 12-01 helper) was NOT actually built by 12-01 (the 12-01-SUMMARY confirms no such helper). placeSubFeature builds its own registry-dispatching placer (`subFeaturePlacer`) inline — functionally equivalent, and the only adaptation needed.
- The plan said the 5x5 reorder + emit-once tests live in `decoration_test.go`; they actually live in `world/worker_seam_test.go`. They run as part of `go test ./world/...` with all real bodies registered, so the gate holds regardless of file; `decoration_test.go` owns the new live-write proof (`TestFeatureBodiesProduceBlocks`).

## User Setup Required

None.

## Next Phase Readiness

- **FEAT-03 is fully delivered:** the composite selectors (criterion 3, Pitfall #9 satisfied) + BlockPile/FallenTree/VegetationPatch (criterion 4) are live, and the final acceptance gate (live writes + 5x5 determinism + Docker -race) is green.
- **Phase 13 (trees + structures)** picks up the deferred TreeDecorator subsystem (fallen_tree's log/stump decorators), the `rotated_block_provider` (hay block_pile), and the remaining feature bodies (tree placers, geode, etc.). The selector recursion + placeSubFeature seam is the reusable substrate trees plug into (a biome's tree set is a random_selector over weighted tree sub-features).

## Self-Check: PASSED

All 4 created files exist; the 3 task commits (810c195e, daee2fc3, 071b9812) are in git history; `go build ./...` + `CGO_ENABLED=0 go build ./...` exit 0; the selector + misc + gate tests pass; the Docker -race gate over ./world/... is green; go.mod/go.sum unchanged.

---
*Phase: 12-core-feature-types*
*Completed: 2026-06-25*
