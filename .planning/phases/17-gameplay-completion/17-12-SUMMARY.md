---
phase: 17
plan: 12
subsystem: worldgen / feature / tree
tags: [worldgen, tree-feature, parity, bugfix, free-space-abort]
requires: [tree-feature port (13-01/13-02), placement chain (11-xx)]
provides: [vanilla-faithful TreeFeature.doPlace abort, isFree/validTreePos/isVine predicates]
affects: [world/levelgen/feature, world/feature_tree.go, world/levelgen/data]
key-files:
  modified:
    - world/levelgen/feature/tree.go
    - world/feature_tree.go
    - world/levelgen/data/embed.go
    - world/levelgen/feature/tree_test.go
  created:
    - world/levelgen/data/tags/block/replaceable_by_trees.json (+ leaves, small_flowers, logs, logs_that_burn, *_logs, crimson/warped_stems — embedded vanilla tags)
decisions:
  - "Port the EXACT TreeFeature.doPlace abort: freeHeight >= treeHeight, else the minClippedHeight escape — replacing the buggy hardcoded minFree=2 floor."
  - "Resolve REPLACEABLE_BY_TREES / LOGS from the embedded vanilla block-tag JSONs (recursive nested-tag expansion) rather than hand-transcribing membership."
metrics:
  completed: 2026-06-25
---

# Phase 17 Plan 12: Tree Free-Space Abort Fidelity Fix Summary

Fixed the confirmed WORLDGEN 1:1 fidelity bug where trees generated on top of / through
other trees, by porting `TreeFeature.doPlace` + `getMaxFreeTreeHeight` method-for-method
against the 26.2 jar bytecode: the full-height abort condition, the always-`i-2`
blocked-layer return, the `isFree` vs `validTreePos` predicate split, and the `isVine`
scan guard.

## Root Cause (verified against `temp/cache/26.2-inner.jar` via `javap -c -p`)

Three deviations from vanilla `net.minecraft.world.level.levelgen.feature.TreeFeature`:

1. **Hardcoded `minFree = 2` abort floor** (`world/feature_tree.go`): a tree placed whenever
   ≥2 layers were free, so a 6-tall tree could plant through only 2 free layers — stacking.
   Vanilla `doPlace` (bytecode offsets 154-180) aborts unless
   `freeHeight >= treeHeight`, with a `minClippedHeight` escape:
   ```
   if (freeHeight < treeHeight
       && (minimumSize.minClippedHeight().isEmpty()
           || freeHeight < minimumSize.minClippedHeight().getAsInt()))
       return false;
   ```
   Overworld trees (`two_layers_feature_size`, no `min_clipped_height`) therefore require
   the FULL tree height — a too-short column places NOTHING.

2. **Wrong `getMaxFreeTreeHeight` blocked-layer return** (`tree.go:maxFreeTreeHeight`): Sulfur
   did `if depth >= treeHeight { return treeHeight } else return depth - 1`. Vanilla
   (bytecode offsets 101-105) ALWAYS does `return i - 2` on the first blocked layer — no
   `i >= treeHeight` special-case, and `-2` not `-1`. The special case was exactly what let a
   tree blocked above still report full height and place.

3. **Missing `isVine` scan guard + wrong free predicate**. Vanilla's scan test is
   `!TrunkPlacer.isFree(pos) || (!ignoreVines && TreeFeature.isVine(pos))`. Sulfur omitted the
   vine half AND used one leaf-only `treePosFree` for everything. The bytecode shows TWO
   distinct predicates:
   - `TreeFeature.validTreePos` (used by `placeLog`, `tryPlaceLeaf`) =
     `state.isAir() || state.is(BlockTags.REPLACEABLE_BY_TREES)`
   - `TrunkPlacer.isFree` (used ONLY by the scan) = `validTreePos(pos) || state.is(BlockTags.LOGS)`
   - `TreeFeature.isVine` = `state.is(Blocks.VINE)`

## Bytecode Evidence (quoted)

`doPlace` abort (`javap` offsets 154-180):
```
154: iload 16 (freeHeight)  156: iload 8 (treeHeight)  158: if_icmpge 181  (>= treeHeight -> place)
161: aload 15 (minClipped)  163: OptionalInt.isEmpty   166: ifne 179
169: iload 16  173: OptionalInt.getAsInt  176: if_icmpge 181
179: iconst_0  180: ireturn  (abort)
```

`getMaxFreeTreeHeight` (offsets 12-124): loop `i = 0 .. treeHeight+1`; per layer
`size = minimumSize.getSizeAtHeight(treeHeight, i)`; scan `[-size,size]^2`; on
`!TrunkPlacer.isFree(pos) || (!ignoreVines && isVine(pos))` -> `iload 6; iconst_2; isub; ireturn`
(= `return i - 2`); fallthrough `return treeHeight`.

`TrunkPlacer.isFree` = `validTreePos(pos) || isStateAtPosition(pos, s -> s.is(BlockTags.LOGS))`.
`TreeFeature.lambda$validTreePos$0` = `state.isAir() || state.is(BlockTags.REPLACEABLE_BY_TREES)`.
`TreeFeature.lambda$isVine$0` = `state.is(Blocks.VINE)`.
`replaceable_by_trees.json` includes `#minecraft:leaves`, `#minecraft:small_flowers`, grass/fern,
`vine`, `water`, ... (so leaves DO read as free; the abort is driven by SOLID material and the
vine guard, plus the full-height requirement).

## Fix

- `world/feature_tree.go`: dropped the `minFree`/`minTreeHeight=2` argument; `PlaceTree` now
  takes only `treeHeight` and applies the exact `doPlace` abort internally.
- `tree.go:maxFreeTreeHeight`: `return depth - 2` on first block; removed the
  `depth >= treeHeight -> return treeHeight` special case; added the
  `!treeIsFree(p) || (!ignoreVines && isVine(p))` guard.
- `tree.go:PlaceTree`: abort is now `if freeHeight < treeHeight { if !minClippedSet ||
  freeHeight < minClipped { return false } }`.
- `tree.go` predicates: split into `treePosFree` (validTreePos = air || REPLACEABLE_BY_TREES),
  `treeIsFree` (isFree = validTreePos || LOGS, scan-only), and `isVine`. Membership is resolved
  from the embedded vanilla block tags via a new recursive `data.BlockTag` resolver — never
  hand-transcribed.
- `featureSize` interface gained `minClippedHeight() (int, bool)`; `two_layers_feature_size`
  and `three_layers_feature_size` now parse `min_clipped_height`.
- `world/levelgen/data/embed.go`: added `BlockTag(id)` which recursively expands nested
  `#minecraft:...` tag references from the embedded `tags/block/*.json`.

## Before / After

- **Before:** a column blocked above (solid terrain or another tree's solid footprint, or an
  unwanted vine) still satisfied `freeHeight >= 2`, so a full tree was placed THROUGH it —
  trees stacked on / through one another.
- **After:** the same column yields `freeHeight = i-2 < treeHeight`; with no `min_clipped_height`
  the tree ABORTS (zero blocks written). A clear column still places the full tree. The rng
  draws (foliageHeight/Radius/trunk_offset_y) still happen before the abort, so the selector's
  per-feature seed is unchanged (determinism contract preserved).

## Density Audit (secondary)

Audited the `trees_*` placed_feature count modifiers. `trees_plains` is a weighted_list count
(0 @ w19, 1 @ w1) -> ~1 attempt per 20 chunks; `trees_birch`/forest use noise-based counts.
Sulfur's `Count` modifier (`placement/modifiers.go`) ports `CountPlacement.count = IntProvider.Sample`
faithfully, the chain `count -> in_square -> surface_water_depth_filter -> heightmap ->
would_survive filter -> biome` matches `VegetationPlacements.treePlacementBase`, and the
weighted_list int provider is implemented. Density is NOT inflated — the stacking was purely the
free-space abort bug, now fixed.

## Tests

- Updated `TestPlaceTreeRootHookNoOp`: replaced the old `minFree=100` (abort) / `minFree=2`
  (place) cases with the vanilla semantics — a stone-capped column aborts (0 blocks), a clear
  column places (trunk base log present).
- Added `TestPlaceTreeNoStacking`: a first tree places in a clear column; a second tree whose
  column is capped by solid stone two layers up ABORTS — proving no stacking.
- Added `TestMaxFreeTreeHeightIMinus2`: pins the exact numerics — clear -> treeHeight; stone at
  `i=4` -> `2`; stone at `i=1` -> `-1` (no clamp); vine at `i=3` with `!ignoreVines` -> `1`;
  same vine with `ignoreVines` (oak's real value) -> treeHeight.
- Full `go test ./world/...` passes (372s), including the determinism gates
  `TestDecorationReorderIdentical`, `TestEmitOnce`, `TestEmitOnceUnderHold`. The change alters
  which trees place, but those gates assert order-independence and emit-once (not golden tree
  positions), so they remain valid without regeneration.
- `CGO_ENABLED=0 go build ./...` exit 0; `go vet` clean.

## Self-Check: PASSED
