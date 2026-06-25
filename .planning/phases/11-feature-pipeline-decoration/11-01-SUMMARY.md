---
phase: 11-feature-pipeline-decoration
plan: 01
subsystem: worldgen

# Dependency graph
requires:
  - phase: 10-worldgen-foundation
    provides: world/levelgen/data embed FS + readEmbedded/list helpers; surface.resolveBlockState pattern (block.FromID + nbt Properties + block.ToStateID); density.Registry parser template (Parse/parseObject/stripNS + cache/parsing cycle guard)
provides:
  - "Embedded feature/decoration DATA: 226 configured_feature + 262 placed_feature + 66 biome JSON trees under world/levelgen/data, with data.ConfiguredFeatureJSON/PlacedFeatureJSON/BiomeJSON accessors + ConfiguredFeatureIDs/PlacedFeatureIDs/BiomeIDs listers"
  - "New world/levelgen/feature package (own package, import-cycle clean, does NOT import placement): the typed-but-body-deferred AST — ConfiguredFeature{Type,Config}, PlacedFeature{FeatureRef,Feature,Placement}, PlacementModifierRaw envelope, ParsedConfig{Raw,States resolved to block.StateID}"
  - "Polymorphic type-dispatch parser (Registry.ParseConfiguredFeature/ParsePlacedFeature) modeled on density.Registry: recognized-type SET of 54 jar-confirmed configured_feature types, loud error on a genuinely-unknown type, walkBlockStates resolving nested {Name,Properties} refs to block.StateID, parsing-set cycle guard"
  - "feature.Registry: DAG-deduped cache + cycle guard + NewEmbeddedRegistry/LoadAllEmbedded that parses ALL 226+262 entries up front (fails loudly at construction) + PlacedByID/ConfiguredByID accessors for plans 11-02/11-03"
affects: [worldgen-features, worldgen-decoration, phase-11, plan-11-02, plan-11-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Polymorphic \"type\"-tagged registry-dispatch parser (mirrors density.Registry): stripNS dispatch on a recognized-type SET; KNOWN-but-deferred types parse to a generic typed node, GENUINELY-unknown types error loudly naming them (T-11-01)"
    - "Typed-but-body-deferred AST: feature TYPE recorded + config captured (with block-state refs resolved to StateID now), Feature.place body deferred to Phase 12+; placement modifier list captured as a raw envelope plan 11-02 binds (keeps the two plans package-disjoint)"
    - "Recursive walkBlockStates: descends config JSON resolving every {Name,Properties} leaf (bare refs + provider envelopes) to block.StateID, leaving provider getState() semantics deferred to Phase 13"
    - "DAG-dedup + parsing-set cycle guard ported from density.Registry: a configured_feature shared by N placed_features parses once; a placed->configured->placed cycle is rejected, not infinitely recursed (T-11-02)"

key-files:
  created:
    - world/levelgen/feature/feature.go
    - world/levelgen/feature/parse.go
    - world/levelgen/feature/blockstate.go
    - world/levelgen/feature/registry.go
    - world/levelgen/feature/embed.go
    - world/levelgen/feature/parse_test.go
    - world/levelgen/data/configured_feature/ (226 JSON files)
    - world/levelgen/data/placed_feature/ (262 JSON files)
    - world/levelgen/data/biome/ (66 JSON files)
  modified:
    - tools/extract_worldgen.go
    - world/levelgen/data/embed.go
    - world/levelgen/data/embed_test.go

key-decisions:
  - "feature is its OWN package (mirrors density/surface/carver) so a package levelgen file can import it without a cycle; it MUST NOT import placement (11-02) — the placement modifier list is captured as PlacementModifierRaw{Type,Raw} that 11-02 binds, keeping 11-01/11-02 file- AND package-disjoint and acyclic"
  - "The recognized-feature-type SET (54 entries) is derived from the distinct top-level \"type\" values across the 226 configured_feature JSONs (jar-enumerated), embedded as a map literal; LoadAll cross-checks it against the live embed so any in-set type parses and a genuinely-unknown type errors loudly (no hardcode-drift, T-11-01 honored)"
  - "Block-state refs resolve through the EXISTING level/block path: resolveBlockState ported verbatim from surface/rules.go (block.FromID + nbt Properties 3-byte strip + block.ToStateID); a parsed config carries resolved block.StateID values in ParsedConfig.States, not raw strings"
  - "The inner jar (temp/cache/26.2-inner.jar) was present, so the three trees were pure-unzipped directly from it (the same mechanism tools/extract_worldgen.go would run) and COMMITTED as build-time-trusted DATA — identical to how v1's worldgen trees are committed"
  - "Config is captured as raw JSON + resolved StateIDs (typed-but-deferred); the full type-specific config decode + Feature.place body is Phase 12+'s job, keeping Phase 11 strictly orchestration"

patterns-established:
  - "NewEmbeddedRegistry + LoadAllEmbedded is the canonical construction plans 11-02/11-03 consume: parse + validate the whole feature roster up front so a build-data mismatch fails at generator-construction time, never mid-decoration"

requirements-completed: [FEAT-02]

# Metrics
duration: ~40min
completed: 2026-06-25
---

# Phase 11 Plan 01: Feature/Biome Data + Polymorphic Parser (FEAT-02 data half) Summary

**Embedded the 226 configured_feature + 262 placed_feature + 66 biome worldgen JSON trees and built a new import-cycle-clean world/levelgen/feature package whose polymorphic "type"-tagged parser (modeled exactly on density.Registry) loads ALL 488 entries into a typed-but-body-deferred AST — block-state refs resolved to block.StateID, known types parsed, a genuinely-unknown type errored loudly, and a placed->configured->placed cycle guarded — proven by TestLoadAll over the full real embed.**

## What was built

**Task 1 — DATA half (extract tooling + embed FS), commit `8f96b283`:**
- Added `configured_feature` / `placed_feature` / `biome` whole-tree prefixes to `worldgenZipPrefixes` in `tools/extract_worldgen.go` (same pure-unzip loop + count log, no new tooling).
- Extended the `//go:embed` directive in `world/levelgen/data/embed.go` and added six accessors/listers: `ConfiguredFeatureJSON` / `PlacedFeatureJSON` / `BiomeJSON` + `ConfiguredFeatureIDs` / `PlacedFeatureIDs` / `BiomeIDs` (mirroring the existing NoiseSettings/NoiseSettingsIDs pattern; the trees are flat so `list()` suffices).
- Pure-unzipped the three trees from the present `temp/cache/26.2-inner.jar` and committed them: **226 / 262 / 66** `.json` files.
- `TestWorldgenFeatureEmbed` asserts the 226/262/66 counts + spot reads (oak configured_feature is type `minecraft:tree`, oak placed_feature + plains biome resolve non-empty) + missing-id errors.

**Task 2 — the AST + parser + block-state resolver (TDD), commit `7b4a5b8a`:**
- `feature.go`: the typed-but-deferred AST — `ConfiguredFeature{ID,Type,Config}`, `PlacedFeature{ID,FeatureRef,Feature,Placement}`, `PlacementModifierRaw{Type,Raw}` (the unbound envelope plan 11-02 binds), `ParsedConfig{Raw,States []block.StateID}`.
- `parse.go`: `Registry.ParseConfiguredFeature` / `ParsePlacedFeature` — `stripNS` dispatch on `recognizedFeatureTypes` (54 jar-confirmed types) with a loud error on an unknown type; `recognizedProviderTypes` (8 jar-confirmed BlockStateProvider types) documented for 11-02/11-03; `walkBlockStates` recursively resolves nested `{Name,Properties}` block-state refs to `block.StateID`; `NewFuncSource` adapts the data accessors.
- `blockstate.go`: `resolveBlockState` ported verbatim from `surface/rules.go` (`block.FromID` + `nbt.Marshal` Properties with the 3-byte name-header strip + `block.ToStateID`); loud on unknown block/property.
- `registry.go`: `feature.Registry` cache + `parsing` cycle-guard set (keyed `cf:`/`pf:` so a placed->configured->placed loop is caught), `ResolveConfigured` / `ResolvePlaced` (DAG-dedup), `PlacedByID` / `ConfiguredByID` accessors, `LoadAll(configuredIDs, placedIDs)`.
- `parse_test.go`: known types parse, unknown type errors loudly naming it, oak_log{axis:y} resolves to the right StateID (and surfaces in `ParsedConfig.States`), a placed ref resolves + DAG-dedups, a synthetic cycle is rejected.

**Task 3 — LoadAll over the full embed, commit `d5a815e0`:**
- `embed.go`: `NewEmbeddedRegistry` wires the Registry to `data.ConfiguredFeatureJSON/PlacedFeatureJSON`; `LoadAllEmbedded` lists + parses the whole roster.
- `TestLoadAll`: `LoadAllEmbedded()` returns nil over the **full real embed** — all 226 configured_feature + 262 placed_feature parse clean; every placed_feature's configured ref resolves to a non-nil `ConfiguredFeature`; the cached counts match 226/262.

## TestLoadAll results

`TestLoadAll` PASSED — **226 configured_feature + 262 placed_feature (488 entries) parsed clean** over the embedded vanilla 26.2 data, with zero genuinely-unknown types and zero unresolvable block-state refs, and every placed ref bound to a non-nil configured_feature.

## Test output

```
ok  github.com/imhinotori/sulfur/world/levelgen/feature   0.523s
ok  github.com/imhinotori/sulfur/world/levelgen/data      0.358s
```
- `go build ./...` -> exit 0
- `CGO_ENABLED=0 go build ./...` -> exit 0
- `go vet ./world/levelgen/feature/` -> clean
- full `go test ./...` -> no failures; `go.mod`/`go.sum` unchanged (no new deps)

## Deviations from Plan

**1. [Rule 3 - Blocking adaptation] Embed test name in the plan's verify command does not match the test.** The plan's Task 1 verify was `go test ./world/levelgen/data/ -run TestEmbed`, but the existing data test is `TestWorldgenEmbed` and the new one is `TestWorldgenFeatureEmbed` — `-run TestEmbed` matches neither (it would silently run nothing and pass). Ran the tests by their real names (`-run TestWorldgen`) instead; both pass. The deliverable (the count-assertion test) is present and green; only the verify regex in the plan was stale. No code impact.

**2. [Rule 2 - Critical robustness] `-race` not run (environment lacks cgo).** CLAUDE.md mandates `go test -race` on concurrency subsystems, but `-race` requires `CGO_ENABLED=1` which is unavailable here, and the target build is `CGO_ENABLED=0`. The feature parser is single-threaded by construction (no goroutines, no shared mutable state across goroutines — the Registry caches are touched only by the calling goroutine), so `-race` is not applicable to this plan's code. `go vet` is clean. Flagging for a CI run with cgo when 11-02/11-03 wire the parser into the concurrent generator.

## Known Stubs

None that block the plan goal. By design (Phase 11 = orchestration, not feature bodies):
- `ParsedConfig.Raw` retains the verbatim config JSON for Phase 12+ to decode the full type-specific config; `ParsedConfig.States` already carries the resolved block.StateID leaves.
- `PlacementModifierRaw.Raw` retains the verbatim modifier JSON for plan 11-02 to bind to real `PlacementModifier.getPositions` bodies.
- `recognizedProviderTypes` documents the BlockStateProvider roster; the provider `getState()` body is Phase 13's job (the envelope's inner states are already resolved to StateID now).
These are the explicit typed-but-deferred boundaries the plan prescribes, each owned by a named later plan — not unintended stubs.

## Self-Check: PASSED

- Created files all FOUND: feature.go, parse.go, blockstate.go, registry.go, embed.go, parse_test.go; data trees 226/262/66.
- Commits all FOUND in git log: 8f96b283, 7b4a5b8a, d5a815e0.
