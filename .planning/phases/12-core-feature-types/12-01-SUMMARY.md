---
phase: 12-core-feature-types
plan: 01
subsystem: worldgen
tags: [blockstateprovider, intprovider, blockpredicate, placement-modifier, lcg, nextgaussian, feature-body-registry, decoration]

# Dependency graph
requires:
  - phase: 11-feature-pipeline-decoration
    provides: "ParsedConfig + Registry (ResolvePlaced/ParsePlacedFeature), placement modifier seam (BindModifier), PlacementContext, the no-op featureBody dispatch + WorldgenRandom/LegacyRandomSource"
provides:
  - "BlockStateProvider hierarchy (Simple/Weighted/RuleBased + Noise/DualNoise) parsed from the already-resolved config via resolveBlockState"
  - "NextBoolean (next(1)) + NextGaussian (Marsaglia two-draw polar, cached) on LegacyRandomSource, jar-exact"
  - "IntProvider trapezoid (TrapezoidalInt, the primary random_offset spread) + clamped_normal + very_biased_to_bottom"
  - "random_offset + block_predicate_filter placement modifiers bound (11-02's deferred gap closed)"
  - "The BlockPredicate set (matching_block_tag/matching_blocks/would_survive/all_of/any_of/not/solid/replaceable/matching_fluids)"
  - "featureBody REGISTRY with the *feature.Registry plumbed through (g.deco.registry -> makePlacer -> newConfiguredPlacer -> bodyContext)"
affects: [12-02, 12-03, 13-trees-structures]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "type->body registry populated at init() from disjoint files (no shared switch) so parallel wave-2 plans stay file-disjoint"
    - "LegacyRandomSource-only LCG primitives (NextBoolean/NextGaussian) reached via type-assertion at the call site (lower churn than widening the RandomSource interface)"
    - "tag->StateID sets resolved constant-for-constant from the jar tag defs (carver Replaceables precedent), cited inline"

key-files:
  created:
    - world/levelgen/feature/provider.go
    - world/levelgen/feature/provider_test.go
    - world/levelgen/placement/predicate.go
    - world/levelgen/placement/predicate_test.go
    - world/levelgen/placement/heightprovider_test.go
    - world/feature_body.go
    - world/feature_body_test.go
  modified:
    - world/levelgen/random.go
    - world/levelgen/placement/heightprovider.go
    - world/levelgen/placement/modifiers.go
    - world/levelgen/placement/modifiers_test.go
    - world/placement_context.go
    - world/noisegen.go
    - world/decoration_test.go

key-decisions:
  - "NextBoolean/NextGaussian kept LegacyRandomSource-only (NOT added to the RandomSource interface): the decoration chain is always a *WorldgenRandom (embeds *LegacyRandomSource), so the methods are exposed there; clamped_normal type-asserts a gaussianSource at the call site and panics loudly off the legacy chain. Avoids forcing Xoroshiro to grow a Gaussian cache it never uses."
  - "ClampedNormalInt ported jar-exact (javap -c): v = mean + (float)nextGaussian()*deviation, clamped, then f2i TRUNCATION toward zero (NOT round) — confirmed against the bytecode (Mth.normal + Mth.clamp(F,F,F) + f2i)."
  - "RuleBasedStateProvider rule predicates are a SELF-CONTAINED func(existing StateID) bool in the feature package (feature must not import placement), distinct from placement.BlockPredicate."
  - "would_survive / solid / replaceable are CONSERVATIVE ports over the StateID the worldgen context can read (the full BlockBehaviour canSurvive/isSolidRender state-shape is unavailable mid-worldgen), documented inline; never a false keep over air/floating."
  - "registerFeatureBody panics on a duplicate type registration (a programming error — 12-02/12-03 must own disjoint types)."

patterns-established:
  - "featureBody dispatch registry: var featureBodies map[string]featureBody + registerFeatureBody(init) + lookupFeatureBody; newConfiguredPlacer consults it and falls back to a recordable no-op."
  - "bodyContext{view, reg} carries the shared deps (3x3 proxy + live registry) a body resolves nested sub-features through."
  - "Determinism tests pin BOTH the picked value AND the post-draw rng fingerprint (oracle-traced) so a wrong draw count/order fails loudly."

requirements-completed: [FEAT-03]

# Metrics
duration: 73min
completed: 2026-06-25
---

# Phase 12 Plan 01: FEAT-03 Prerequisites Summary

**BlockStateProvider hierarchy + the deferred random_offset/block_predicate_filter modifiers (with the TrapezoidalInt IntProvider) + NextBoolean/NextGaussian on the LCG + a featureBody registry that plumbs the *feature.Registry through — every shared prerequisite wave-2's FEAT-03 bodies need, with zero new deps.**

## Performance

- **Duration:** ~73 min
- **Started:** 2026-06-25T01:41Z (approx, first task commit chain)
- **Completed:** 2026-06-25T02:54Z
- **Tasks:** 3 (all TDD)
- **Files modified:** 14 (7 created, 7 modified)

## Accomplishments
- The `BlockStateProvider` hierarchy (Simple 0-draw / Weighted 1-draw cumulative walk / RuleBased fallback / Noise + DualNoise positional) parses from the already-resolved `ParsedConfig` via `resolveBlockState` (re-resolve, not position-indexing), and `ParseProvider` errors loudly on an unported type. Verified against the REAL embedded `flower_default` weighted provider.
- `NextBoolean` (`next(1)`) and `NextGaussian` (the Marsaglia two-draw polar with the cached second value) ported jar-exact on `LegacyRandomSource`, oracle-pinned (seeds 42/12345), with `SetSeed` clearing the Gaussian cache.
- The IntProvider `trapezoid` (TrapezoidalInt — the primary random_offset spread, 62/75) + `clamped_normal` (via NextGaussian, javap-confirmed truncation) + `very_biased_to_bottom` ported; `parseIntProvider` no longer errors on them.
- `random_offset` (javap-confirmed x,y,z draw order; xz_spread sampled twice) + `block_predicate_filter` bound in `BindModifier` — the 11-02 deferred gap is closed; `flower_default` now binds all 7 of its modifiers end-to-end.
- The `featureBody` registry with the `*feature.Registry` plumbed (`g.deco.registry -> makePlacer -> newConfiguredPlacer -> bodyContext`), so 12-03's selectors can resolve nested sub-features and 12-02/12-03 register bodies from disjoint files. The Phase-11 determinism + emit-once gates stay green.

## Task Commits

Each task was committed atomically (TDD: tests + impl landed together per task):

1. **Task 1: NextBoolean/NextGaussian + BlockStateProvider hierarchy** - `91cdf87b` (feat)
2. **Task 2: IntProvider trapezoid/clamped_normal/very_biased + random_offset + block_predicate_filter + BlockPredicate set** - `df381353` (feat)
3. **Task 3: featureBody registry + *feature.Registry plumbing** - `5abe527c` (feat)

## Files Created/Modified
- `world/levelgen/random.go` - NextBoolean/NextGaussian on LegacyRandomSource + Gaussian cache fields; SetSeed clears the cache.
- `world/levelgen/feature/provider.go` - BlockStateProvider interface + Simple/Weighted/RuleBased/Noise/DualNoise + ParseProvider + the self-contained rule-predicate set + cited tag sets.
- `world/levelgen/feature/provider_test.go` - LCG-primitive oracles + provider draw-count tests + the real flower_default weighted provider.
- `world/levelgen/placement/heightprovider.go` - trapezoid/clamped_normal/very_biased IntProvider kinds + the gaussianSource type-assert helper.
- `world/levelgen/placement/heightprovider_test.go` - trapezoid (two-draw + uniform-fallback) / clamped_normal / very_biased oracle tests.
- `world/levelgen/placement/predicate.go` - the BlockPredicate set + ParsePredicate + offset handling + cited tag sets.
- `world/levelgen/placement/predicate_test.go` - matching_block_tag/offset/composites/would_survive/unknown-error tests.
- `world/levelgen/placement/modifiers.go` - random_offset + block_predicate_filter bound in BindModifier.
- `world/levelgen/placement/modifiers_test.go` - random_offset xyz-order + trapezoid fingerprint + block_predicate_filter keep/drop; bind-all coverage extended.
- `world/feature_body.go` - featureBody type + bodyContext + featureBodies registry + registerFeatureBody + placeState/getState helpers.
- `world/feature_body_test.go` - register/dispatch with the plumbed live registry + unregistered no-op + test_set_block + duplicate-panic.
- `world/placement_context.go` - newConfiguredPlacer gains the *feature.Registry param, captures the real ctx+rng, dispatches to a registered body.
- `world/noisegen.go` - makePlacer threads g.deco.registry into newConfiguredPlacer.
- `world/decoration_test.go` - all 3 makePlacer sites updated for the new signature.

## Decisions Made
See `key-decisions` frontmatter. The load-bearing ones: LCG primitives kept LegacyRandomSource-only + type-asserted; ClampedNormalInt confirmed as f2i truncation (not round) against the bytecode; rule predicates self-contained in feature (acyclic); conservative would_survive/solid/replaceable ports documented; duplicate body registration panics.

## Deviations from Plan

None - plan executed exactly as written. The plan's interface block, draw bodies, file list, and the registry-plumbing seam were followed verbatim. The two LCG primitives, the trapezoid distinction, the conservative-predicate documentation, and the makePlacer call-site updates all landed as specified.

## Issues Encountered
- Two TDD tests had flawed no-draw assertions (comparing independent rng sources at different positions) and one flawed "NextBoolean != NextIntN(2)" divergence claim (for seed 42 they coincide for the first 12 draws because both extract the high bit of the same LCG step). Both were corrected to the robust form: a parallel same-seed source advanced by the equivalent ConsumeCount, fingerprinted after. The oracle pins remained the real correctness guard.
- TrapezoidalInt and VeryBiasedToBottomInt classes are not present in the extracted inner jar (ClampedNormalInt and RandomOffsetPlacement ARE — both decompiled and confirmed); the two absent providers were ported from their stable, version-invariant algorithm bodies (the plan's transcription), with draws pinned against hand-traced oracles.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- 12-02 (ore + simple_block/random_patch bodies) and 12-03 (selectors + pile/fallen/vegetation_patch) can both register bodies from their own files via `registerFeatureBody` and consume `ParseProvider` + the bound modifiers + the plumbed registry. They are file-disjoint and parallel-safe.
- 12-03 owns the final Docker `-race` acceptance gate over the live decoration (this plan's code is single-threaded by construction: the registry is built before the scheduler starts; providers/predicates/primitives are pure over `(rng, pos)`). Local `-race` was unavailable (no gcc/CGO in this environment) — deferred to 12-03 per the plan.
- The conservative would_survive/solid/replaceable ports may need a richer ground check once the bodies place real vegetation; documented as conservative, never a false keep.

## Self-Check: PASSED

All 7 created files exist on disk; all 3 task commits (`91cdf87b`, `df381353`, `5abe527c`) are in the git history. `go build ./...` + `CGO_ENABLED=0 go build ./...` exit 0; `go test ./world/... ./world/levelgen/...` all green; go.mod/go.sum unchanged (no new deps); feature does not import placement (acyclic).

---
*Phase: 12-core-feature-types*
*Completed: 2026-06-25*
