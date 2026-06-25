---
phase: 12-core-feature-types
plan: 02
subsystem: worldgen
tags: [orefeature, simpleblockfeature, randompatchfeature, ruletest, ore-replaceable-tags, blockstateprovider, decoration, blob, cross-chunk]

# Dependency graph
requires:
  - phase: 12-core-feature-types
    plan: 01
    provides: "featureBody registry (registerFeatureBody/lookupFeatureBody) + bodyContext{view,reg} + placeState/getState; feature.ParseProvider + the BlockStateProvider hierarchy; LegacyRandomSource draws (NextFloat/NextIntN/NextDouble); the resolveBlockState path"
  - phase: 12-core-feature-types
    plan: 03
    provides: "placeSubFeature + resolveSubFeature (the shared sub-feature placement recursion random_patch routes its inner PlacedFeature through) — landed alongside in the same parallel wave"
provides:
  - "OreFeature body (UNDERGROUND_ORES decoration blob) — OreConfiguration decode + the ellipsoid place/doPlace transcribed jar-exact + the RuleTest set (tag_match/block_match/always_true/blockstate_match/random_block_match/random_blockstate_match) + the ore-replaceable tag sets, registered under \"ore\""
  - "SimpleBlockFeature body (provider-backed single-block place + conservative canSurvive), registered under \"simple_block\""
  - "RandomPatchFeature body (tries x symmetric xz/y jitter + inner PlacedFeature recursion), registered under \"random_patch\""
  - "feature.ResolveBlockStateJSON — exported {Name,Properties} -> StateID resolver the bodies consume"
affects: [12-03, 13-trees-structures]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "feature bodies registered from disjoint files via registerFeatureBody(init); ore/simple_block/random_patch own their types, no shared dispatch edit"
    - "jar-exact draw-order transcription pinned by tests that assert BOTH the placed set AND the post-place rng fingerprint vs an independent in-test oracle"
    - "ore-replaceable tag->StateID sets resolved constant-for-constant from the jar tag defs (carver Replaceables precedent), property-agnostic over all member states"
    - "random_patch routes its inner PlacedFeature through 12-03's shared placeSubFeature so the rng-threading + sub-feature-modifier recursion is unified across 12-02/12-03"

key-files:
  created:
    - world/feature_ore.go
    - world/feature_ore_test.go
    - world/feature_ore_alldecode_test.go
    - world/feature_patch.go
    - world/feature_patch_test.go
  modified:
    - world/levelgen/feature/blockstate.go

key-decisions:
  - "OreFeature.place->doPlace transcribed from javap -c of the REAL 26.2-inner.jar OreFeature.class: 1 angle nextFloat, 2 y-jitter nextInt(3), per-step nextDouble radius, the ellipsoid fill with per-cell dedup, then canPlaceOre (RuleTest draws + discard-on-air nextFloat). The place() OCEAN_FLOOR_WG bbox-column scan was transcribed faithfully (scan columns, run doPlace ONCE at the first column below the floor) — NOT the single-corner check I initially wrote."
  - "RandomPatchFeature.class is REMOVED from the 26.2 jar (26.2 routes overworld vegetation through simple_block + the random_offset placement modifier 12-01 bound). The parser (12-01) still recognizes \"random_patch\", so the body is ported from the stable, version-invariant RandomPatchFeature.place algorithm (the same constant-for-constant approach 12-01 used for the absent TrapezoidalInt). Jar-faithful jitter: per try x,y,z each a symmetric nextInt(spread+1)-nextInt(spread+1) (6 nextInt), NOT the plan's tentative nextInt(spread*2+1)-spread single-draw form."
  - "SimpleBlockFeature canSurvive is a CONSERVATIVE port (12-01 would_survive precedent): place iff the target cell is air AND a solid block sits directly below — never a floating block over air. The full BlockBehaviour.canSurvive state-shape is unavailable mid-worldgen; documented inline as conservative."
  - "The ore RuleTest set covers ALL overworld+nether ores: tag_match (43 uses) + block_match (6, netherrack) dominate; always_true/blockstate_match/random_block(state)_match ported for completeness with jar-exact draw semantics (random_* draw 1 nextFloat iff the block matches, short-circuit). A guard test confirms all 30 embedded ore configs decode, so the body's decode-or-panic never fires in production."
  - "feature.ResolveBlockStateJSON exported on blockstate.go so the ore body resolves target states without re-deriving the {Name,Properties} shape — the single shared resolution path."

patterns-established:
  - "configRaw(cf) nil-safe accessor for a configured feature's raw config JSON, shared by all 12-02 bodies."
  - "determinism tests fill a stone (ore) / floor (patch) neighborhood, run the body at a fixed seed+anchor, and assert the written cells equal an independent oracle that replays the same draw sequence — pinning draw order AND placement."

requirements-completed: [FEAT-03]

# Metrics
duration: 58min
completed: 2026-06-25
---

# Phase 12 Plan 02: FEAT-03 Bodies (Ore + SimpleBlock + RandomPatch) Summary

**The three high-volume FEAT-03 feature bodies — OreFeature (the UNDERGROUND_ORES blob, jar-exact ellipsoid + the full RuleTest target set + ore-replaceable tags), SimpleBlockFeature (provider-backed leaf), and RandomPatchFeature (tries x jitter + inner-feature recursion) — all registered into 12-01's featureBody registry from disjoint files, all writing through the cross-chunk Neighborhood, with deterministic-placement tests pinning exact blocks at exact positions for known seeds. Zero new deps.**

## Performance
- **Duration:** ~58 min
- **Completed:** 2026-06-25
- **Tasks:** 2 (both TDD)
- **Files:** 6 (5 created, 1 modified)

## Accomplishments
- **OreFeature** (`world/feature_ore.go`): `place -> doPlace` transcribed from `javap -c` of the real 26.2-inner.jar `OreFeature.class`. The blob is `size` points along a lerp line (endpoints from a 1-draw angle nextFloat + 2 y-jitter nextInt(3)), each with a per-step nextDouble radius, dominated-point pruning, then an ellipsoid fill with per-cell dedup. Each candidate runs `canPlaceOre`: the target's RuleTest (`tag_match`/`block_match`/`always_true`/`blockstate_match`/`random_block_match`/`random_blockstate_match`, jar-exact draws) over the existing block, then the discard-on-air `nextFloat` (only when 0<discard<1) gated by the 6-neighbor air scan. The place() OCEAN_FLOOR_WG bbox-column scan runs doPlace once at the first underground column. Ore-replaceable tags (`stone_ore_replaceables`, `deepslate_ore_replaceables`, `base_stone_overworld`, `base_stone_nether`) resolved constant-for-constant from the jar tag defs. Registered under `"ore"`.
- **SimpleBlockFeature** (`world/feature_patch.go`): decode `to_place` via `feature.ParseProvider`, draw the provider state at the origin (weighted = 1 draw, simple = 0), conservative `canSurvive` gate (air-at-pos + solid-below), then `SetBlock`. Registered under `"simple_block"`.
- **RandomPatchFeature** (`world/feature_patch.go`): ported from the stable algorithm (the class is removed from 26.2). Per try: x,y,z jitter each a symmetric `nextInt(spread+1)-nextInt(spread+1)` (6 nextInt, jar draw order), then the inner PlacedFeature placed at the jittered anchor via 12-03's shared `placeSubFeature` (its modifiers re-apply on the threaded rng). Registered under `"random_patch"`.
- All bodies write ONLY through `Neighborhood.SetBlock` (3x3 proxy, heightmap-live, cross-chunk) — confirmed by cross-chunk-edge tests that an edge-spanning blob/patch reaches the +x neighbor.

## Task Commits
1. **Task 1: OreFeature body + rule_test targets** — `2283dbcd` (feat)
2. **Task 2: SimpleBlockFeature + RandomPatchFeature bodies** — `3a153a89` (feat)

## Deviations from Plan

### [Rule 1 - Bug] OreFeature place() column scan (not a single-corner check)
- **Found during:** Task 1, transcribing `place()` from the bytecode.
- **Issue:** The plan's interface sketch implied a single OCEAN_FLOOR_WG check at (minX,minZ); the real bytecode scans EVERY bbox column and runs doPlace ONCE at the first column whose OCEAN_FLOOR_WG height is >= minY.
- **Fix:** Ported the faithful column scan (no rng draws in the scan; doPlace called at most once with minX/minZ).
- **Files:** world/feature_ore.go. **Commit:** 2283dbcd.

### [Rule 1 - Bug] RandomPatch jitter is the symmetric two-draw form, x/y/z order
- **Found during:** Task 2. The plan flagged "CONFIRM jar order" for the random_patch jitter and tentatively wrote `nextInt(xz_spread*2+1)-xz_spread`.
- **Issue:** The canonical jar algorithm is `nextInt(spread+1) - nextInt(spread+1)` (a symmetric two-draw triangular jitter) per axis, in x,y,z order (6 nextInt draws/try) — NOT a single-draw centered form, and y is jittered between x and z.
- **Fix:** Ported the jar-faithful two-draw x,y,z sequence; the draw count test (tries*6) pins it.
- **Files:** world/feature_patch.go. **Commit:** 3a153a89.

### [Rule 3 - Blocking] RandomPatchFeature/RandomPatchConfiguration absent from the 26.2 jar
- **Found during:** Task 2 setup (the class is not in 26.2-inner.jar).
- **Issue:** 26.2 removed the top-level random_patch feature (vegetation now = simple_block + random_offset). No bytecode to transcribe.
- **Fix:** Ported the body from the stable, version-invariant RandomPatchFeature.place algorithm (the exact precedent 12-01 used for the absent TrapezoidalInt). The tests use a synthetic constructed config (the plan anticipated this: "26.2 has ZERO top-level random_patch... the test uses a synthetic/constructed config").
- **Files:** world/feature_patch.go, world/feature_patch_test.go. **Commit:** 3a153a89.

### [Rule 3 - Blocking] feature.ResolveBlockStateJSON added (export gap)
- **Found during:** Task 1. The ore body needs to resolve {Name,Properties} target states, but `resolveBlockState` was unexported and took an internal struct.
- **Fix:** Added the exported `ResolveBlockStateJSON(raw json.RawMessage)` to blockstate.go wrapping the existing path. Additive, no behavior change.
- **Files:** world/levelgen/feature/blockstate.go. **Commit:** 2283dbcd.

### [Parallel coordination] fillFloor is the canonical 12-02 test helper consumed by 12-03
- During the parallel run a `fillFloor` redeclaration surfaced; 12-03's `feature_misc_test.go` explicitly reuses `12-02's fillFloor(view, center, floorY)`. Resolved by keeping `fillFloor(view, center, floorY)` as 12-02's single canonical definition (12-03 dropped its local copy). No production-code impact (test-helper only).

## Threat Model Coverage
- **T-12-05** (doPlace draw-order divergence) — mitigated: TestOreFeatureBlob pins the exact written positions AND the post-place rng fingerprint vs an independent oracle.
- **T-12-06** (feature/noise ore conflation) — mitigated: oreBody is a separate registered body placing discrete OreConfiguration blobs; distinct from v1's noise OreVeinifier.
- **T-12-07** (wrong tag membership) — mitigated: TestOreTagMembership asserts stone_ore_replaceables matches stone (not deepslate) and vice-versa; unported tag/predicate errors loudly.
- **T-12-08** (out-of-range SetBlock) — accepted (Neighborhood drops out-of-3x3 by design); the cross-chunk-edge tests confirm in-range neighbor writes land.

## Threat Flags
None — no new trust boundary or network/auth/file surface introduced; the bodies are pure over (rng, Neighborhood) and run off-tick on server-derived anchors.

## Verification
- `go test ./world/ -run 'TestOre|TestSimpleBlock|TestRandomPatch|TestPatch'` — green (all 3 bodies place jar-exact blocks at jar-exact positions for known seeds, draw-order pinned, cross-chunk spill confirmed).
- Full `go test ./world/` (88s) green — the real-decoration determinism + emit-once Phase-11 gates stay green with the bodies now writing real blocks.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` clean; go.mod/go.sum unchanged (no new deps).
- A guard test confirms all 30 embedded ore configs decode (the body's decode-or-panic never fires).
- The full Docker `-race` gate is owned by 12-03 (the last Phase-12 plan) once all bodies are live; this plan's bodies are single-threaded by construction (the registry is built before the scheduler starts; the bodies are pure over rng+Neighborhood). Local `-race` unavailable (no CGO/gcc in this environment) — deferred to 12-03 per the plan.

## Known Stubs
None — all three bodies place real blocks wired to real config (the embedded ore configs + provider-backed simple_block + the synthetic-tested random_patch). The conservative `canSurvive` / discard-on-air are documented approximations of richer vanilla checks, not stubs (they never produce a false placement).

## Self-Check: PASSED
All 5 created files + the 1 modified file exist on disk; both task commits (`2283dbcd`, `3a153a89`) are in git history. `go build ./...` + `CGO_ENABLED=0 go build ./...` exit 0; the targeted tests + the full world suite are green; go.mod/go.sum unchanged.

---
*Phase: 12-core-feature-types*
*Completed: 2026-06-25*
