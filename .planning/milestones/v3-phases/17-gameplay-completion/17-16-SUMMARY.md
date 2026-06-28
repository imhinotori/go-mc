---
phase: 17
plan: 16
subsystem: worldgen / feature / vegetation
tags: [worldgen, simple-block-feature, vegetation, can-survive, parity, bugfix]
requires: [simple_block/random_patch port (12-02), data.BlockTag tag resolver (17-12), placed_feature embed]
provides: [vanilla-faithful VegetationBlock.canSurvive / mayPlaceOn sustaining-block gate, #supports_vegetation tag chain embedded]
affects: [world/feature_patch.go, world/levelgen/data, tools/extract_worldgen.go]
key-files:
  modified:
    - world/feature_patch.go
    - world/feature_patch_test.go
    - tools/extract_worldgen.go
  created:
    - world/levelgen/data/tags/block/supports_vegetation.json (+ substrate_overworld, dirt, mud, moss_blocks, grass_blocks — embedded vanilla tags, byte-identical to 26.2-inner.jar)
decisions:
  - "Port the EXACT VegetationBlock.canSurvive -> mayPlaceOn -> state.is(BlockTags.SUPPORTS_VEGETATION) sustaining-block check, replacing the buggy below != air rule that accepted water and overwrote nothing about column occupancy."
  - "Resolve #minecraft:supports_vegetation from the embedded vanilla block-tag JSONs via data.BlockTag (recursive nested-tag expansion) rather than hand-transcribing membership — the same mechanism the 17-12 tree fix used for #replaceable_by_trees."
  - "Keep the placed_feature WORLD_SURFACE_WG heightmap UNCHANGED: it is byte-identical to the jar; vanilla relies on the canSurvive gate (not the heightmap) to reject the water-surface placement WORLD_SURFACE_WG lands on."
metrics:
  completed: 2026-06-26
---

# Phase 17 Plan 16: Plant canSurvive Sustaining-Block Fidelity Fix Summary

Fixed two confirmed WORLDGEN 1:1 fidelity bugs — flowers spawning on top of other
flowers (BUG A) and grass spawning on top of water (BUG B) — by porting the vanilla
`BlockState.canSurvive` path for the overworld vegetation set method-for-method against
the 26.2 jar bytecode: `SimpleBlockFeature.place` → `VegetationBlock.canSurvive` →
`VegetationBlock.mayPlaceOn` → `belowState.is(BlockTags.SUPPORTS_VEGETATION)`. Both bugs
shared one root cause: Sulfur's survival gate accepted any non-air block below as
"ground", so water (non-air) passed and the column-occupancy half was never enforced.

## Root Cause (verified against `temp/cache/26.2-inner.jar` via `javap -c -p`)

`world/feature_patch.go` `simpleBlockCanSurvive` reduced the vanilla canSurvive to:

```go
here := bctx.getState(pos)
if !block.IsAir(here) { return false }
below := bctx.getState(pos.Y-1)
return !block.IsAir(below)   // BUG: water is !air -> accepted as ground
```

The `!block.IsAir(below)` test is NOT what vanilla does. The real path:

1. **`SimpleBlockFeature.place`** (`net.minecraft.world.level.levelgen.feature.SimpleBlockFeature`,
   bytecode offset 47) calls `state.canSurvive(level, origin)` and returns `false` at offset
   160 if it fails — BEFORE writing.

2. **`BlockStateBase.canSurvive(level, pos)`** dispatches to `Block.canSurvive(state, level, pos)`.

3. **`VegetationBlock.canSurvive(state, level, pos)`** (the superclass of `BushBlock`,
   `TallGrassBlock`, `FlowerBlock`, `DoublePlantBlock` — all overworld plants):
   ```
   BlockPos below = pos.below();
   return mayPlaceOn(level.getBlockState(below), level, below);
   ```

4. **`VegetationBlock.mayPlaceOn(state, getter, pos)`** is exactly:
   ```
   return state.is(BlockTags.SUPPORTS_VEGETATION);
   ```

So the keep condition is: **the block directly below the origin is in
`#minecraft:supports_vegetation`** — which contains dirt/grass-like ground and farmland,
but NOT water and NOT any plant.

## The 1:1 Fix

`simpleBlockCanSurvive` now gates on `supportsVegetation(below)` (the `mayPlaceOn`
predicate), in addition to keeping the air-at-origin guard:

```go
here := bctx.getState(pos)
if !block.IsAir(here) { return false }            // air half — BUG A (no flower-on-flower)
below := bctx.getState(pos.Y-1)
return supportsVegetation(below)                   // sustaining tag — BUG B (no grass-on-water)
```

- **BUG B (grass-on-water):** water is not in `#supports_vegetation`, so a column whose
  surface is water (the cell `WORLD_SURFACE_WG` lands on) is rejected. No floating grass.
- **BUG A (flower-on-flower):** the air-at-origin guard rejects a second plant where one
  already sits — a plant is not air. This mirrors vanilla's vegetation PIPELINE: the
  `random_patch`/`simple_block` placed_features run a `block_predicate_filter` with a
  `matching_block_tag #minecraft:air` predicate at the origin before the inner feature
  places (`world/levelgen/placement/predicate.go`), so a non-air origin never reaches a
  write. Keeping the guard in the leaf body makes it self-consistent for the direct/
  synthetic call paths (tests, `random_patch` inner) that do not route through a filter.

### `#minecraft:supports_vegetation` resolved authoritatively (not hand-listed)

`supportsVegetation` builds its StateID set once (lazy `sync.Once`) from
`data.BlockTag("supports_vegetation")`, which recursively expands the nested jar tag
chain extracted into the embedded FS:

```
supports_vegetation -> #substrate_overworld + minecraft:farmland
substrate_overworld -> #dirt + #mud + #moss_blocks + #grass_blocks
dirt        -> dirt, coarse_dirt, rooted_dirt
mud         -> mud, muddy_mangrove_roots
moss_blocks -> moss_block, pale_moss_block
grass_blocks-> grass_block, podzol, mycelium
```

Fully resolved = 11 blocks: `coarse_dirt, dirt, farmland, grass_block, moss_block, mud,
muddy_mangrove_roots, mycelium, pale_moss_block, podzol, rooted_dirt` — exactly the
26.2 `SUPPORTS_VEGETATION` membership. The six tag JSONs were extracted from
`temp/cache/26.2-inner.jar` byte-for-byte (verified identical) into
`world/levelgen/data/tags/block/`, and registered in `tools/extract_worldgen.go`'s
`worldgenSingleFiles` so a future re-extract keeps them. This is the same authoritative
tag-resolution approach the 17-12 tree fix used for `#replaceable_by_trees` — never a
hand-transcribed constant list.

## Heightmap note (no change required)

The grass placed_features sample `WORLD_SURFACE_WG` (which counts water as the surface)
and flowers sample `MOTION_BLOCKING`. Both embedded placed_feature JSONs were verified
**byte-identical to the jar** (`patch_grass_normal.json`, `flower_default.json`), so the
embedded vanilla data is correct and was NOT changed. Vanilla itself relies on the
`canSurvive` sustaining-block gate (this fix), not a different heightmap, to reject the
water-surface placement `WORLD_SURFACE_WG` produces over oceans/rivers/lakes.

## Before / After

| Scenario                         | Before (buggy)        | After (vanilla 1:1)          |
| -------------------------------- | --------------------- | ---------------------------- |
| grass over a water surface       | placed (floating)     | rejected (water not in tag)  |
| flower on an existing flower     | could place / overwrite | rejected (origin not air)  |
| grass/flower on dirt + air above | placed                | placed (dirt in tag)         |
| grass on stone                   | placed (stone != air) | rejected (stone not in tag)  |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Existing tests pinned the OLD buggy "stone floor" placement**
- **Found during:** running the world test suite after the fix.
- **Issue:** `feature_patch_test.go`'s `fillFloor` laid a `minecraft:stone` floor and asserted
  grass/short_grass placed on it. Stone is not in `#supports_vegetation`, so the corrected
  gate rejects it — the old assertions encoded the bug.
- **Fix:** `fillFloor` now lays a `minecraft:grass_block` floor (a valid vanilla vegetation
  substrate), regenerating the golden placements from the CORRECTED output. The reorder/
  determinism fingerprints (`TestDecorationReorderIdentical`, `TestEmitOnce`,
  `TestEmitOnceUnderHold`) were unaffected and still pass.
- **Files modified:** `world/feature_patch_test.go`.

## Tests Added

`TestSimpleBlockCanSurviveSustainingBlock` (3 subcases) locks in the fix:
- `water_below_no_grass` — water directly below → grass does NOT place, origin left air (BUG B).
- `flower_below_no_second_flower` — a dandelion already at the origin → a second flower does
  NOT place, original preserved (BUG A).
- `dirt_below_air_places` — dirt below + air origin → short_grass DOES place (positive case).

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet ./world/` — exit 0.
- `CGO_ENABLED=0 go test ./world/` — PASS (202s, full package).
- `CGO_ENABLED=0 go test ./world/levelgen/{placement,feature,data}/` — PASS.
- Determinism gates `TestDecorationReorderIdentical`, `TestEmitOnce`,
  `TestEmitOnceUnderHold` — PASS.
- `data.BlockTag("supports_vegetation")` resolves to the exact 11-member jar set.

## Self-Check: PASSED
