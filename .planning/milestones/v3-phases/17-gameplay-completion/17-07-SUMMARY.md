---
phase: 17
plan: "17-07"
subsystem: worldgen-feature-tree
tags: [worldgen, trees, foliage, bug-fix, port, visual-gate, protocol-776]
type: bug-fix
requires:
  - "world/levelgen/feature foliage placer family (Blob/Spruce/Pine/Bush/Fancy/Jungle/DarkOak)"
  - "FoliagePlacer.placeLeavesRow seam (placeLeavesRow / placeLeavesRowSigned, center.offset(dx,0,dz))"
  - "world.NoiseGenerator.SpawnPos (17-06 decoration-aware PlayerSpawnFinder)"
provides:
  - "vanilla-faithful foliage orientation: wide-at-bottom / narrow-at-top blobs (rows grow DOWN from the attachment)"
  - "DarkOak per-row Y mapping (rows at center.Y + localY, no longer collapsed onto one Y)"
  - "TestBlobFoliageWideAtBottom orientation regression guard"
affects:
  - "every tree feature that uses Blob/Spruce/Pine/Bush/Fancy/Jungle/DarkOak foliage (oak, birch, spruce, pine, jungle, fancy_oak, dark_oak, mega_jungle, bushes)"
  - "tree decorators that read leaf/log geometry (BeehiveDecorator nest attachment)"
  - "spawn safety near decorated columns (now exercised via the real SpawnPos finder)"
tech-stack:
  added: []
  patterns:
    - "port-faithful sign of FoliagePlacer.setWithOffset(pos, dx, localY, dz) => row.Y = pos.Y + localY"
    - "bake localY into the row center (above(localY)) since Sulfur's placeLeavesRow flattens dy to 0"
key-files:
  created: []
  modified:
    - "world/levelgen/feature/tree.go"
    - "world/levelgen/feature/tree_placers.go"
    - "world/levelgen/feature/tree_test.go"
    - "world/feature_tree_test.go"
    - "world/noisegen_test.go"
decisions:
  - "Foliage row world-Y = attachment.Y + loopvar (vanilla setWithOffset), so the buggy below(i) (= Y - i) became above(i) (= Y + i) across all 6 blob-family placers."
  - "DarkOak was a SECOND Y-mapping bug (all rows collapsed onto center.Y because placeLeavesRowSigned ignores localY); fixed by baking localY into center via center.above(localY) and using above(offset) not above(foliageHeight) for the canopy center."
  - "TestOakBeesNest retargeted from super_birch_bees (straight-trunk blob that vanilla-correctly encloses the trunk top -> no exposed face -> no nest) to fancy_oak_bees (branched canopy with genuinely exposed trunk faces). Confirmed nests place across seeds."
  - "TestTerrainSanity spawn-safety assertion moved off the legacy terrain-only SpawnSurfaceY at fixed (8,8) onto the decoration-aware SpawnPos finder (17-06), which the player actually uses; it correctly avoids the leaf-draped (8,8) column."
metrics:
  duration: ~40m
  completed: 2026-06-25
---

# Phase 17 Plan 07: Fix Upside-Down Tree Foliage Summary

Tree foliage grew UPSIDE-DOWN (wide leaf blob above the trunk top) because the foliage
placers negated the vanilla row-Y sign; fixed the sign across all six blob-family placers
(+ a second collapsed-Y bug in DarkOak) so blobs grow DOWNWARD wide-at-bottom like vanilla.

## Root Cause (confirmed against temp/cache/26.2-inner.jar via `javap -c -p`)

Vanilla `FoliagePlacer.placeLeavesRow` places each cell via
`mutablePos.setWithOffset(pos, dx, localY, dz)` -> **row world-Y = `pos.Y + localY`** (verified
in the base `FoliagePlacer` bytecode: the `setWithOffset` at the leaf write takes `iload 7`
= `localY` as the Y arg). Every blob-family `createFoliage` passes `att.pos()` (NOT a shifted
pos) plus `localY = loopvar`, and the loop walks `loopvar` DOWN from `offset` toward
`offset - foliageHeight`, with the per-row radius WIDEST at the most-negative `loopvar`.
So the widest rows land at the LOWEST Y (below the trunk top): wide-at-bottom, narrow-at-top.

Sulfur's `placeLeavesRow` / `placeLeavesRowSigned` instead place at `center.offset(dx, 0, dz)`
(dy hard-coded to 0) and forward `localY` ONLY to the skip rule — so the caller must bake the
row's Y into `center`. Every blob-family caller did `att.Pos.below(loopvar)` (= `Y - loopvar`),
which is the **negated** vanilla mapping `Y + loopvar`. Result: the blob is mirrored vertically
(the wide part grows ABOVE the trunk). A real client saw upside-down trees in the Phase 17
visual gate.

## The Fix — 6 sign sites (below(loopvar) -> above(loopvar))

Each verified against its vanilla class bytecode (all use the base `placeLeavesRow`
`setWithOffset(pos, dx, localY, dz)` => `row.Y = pos.Y + localY`):

| Placer (vanilla class)            | File:line (orig)          | Change                          | Loop bound (vanilla)            |
|-----------------------------------|---------------------------|---------------------------------|---------------------------------|
| BlobFoliagePlacer                 | tree.go:477               | `att.Pos.below(i)` -> `.above(i)`  | `i >= offset - foliageHeight` (was wrongly `-foliageHeight`; FIXED) |
| SpruceFoliagePlacer               | tree_placers.go:526       | `pos.below(k)` -> `.above(k)`      | `k >= -foliageHeight` (already correct) |
| PineFoliagePlacer                 | tree_placers.go:572       | `att.Pos.below(l)` -> `.above(l)`  | `l >= offset - foliageHeight` (already correct) |
| BushFoliagePlacer                 | tree_placers.go:596       | `att.Pos.below(k)` -> `.above(k)`  | `k >= offset - foliageHeight` (already correct) |
| FancyFoliagePlacer                | tree_placers.go:619       | `att.Pos.below(k)` -> `.above(k)`  | `k >= offset - foliageHeight` (already correct) |
| MegaJungleFoliagePlacer (jungle)  | tree_placers.go:713       | `att.Pos.below(k)` -> `.above(k)`  | `k >= offset - l` (already correct) |

Also fixed the BlobFoliagePlacer **loop bound** `i >= -foliageHeight` -> `i >= offset - foliageHeight`
(identical for oak/birch offset=0, but correct for non-zero offsets) and updated the stale
cited comment at tree.go:463.

### Before / After row-Y (canonical oak blob: radius 2, offset 0, height 3, attachment Y=100)

`j = radius + radiusOffset - 1 - i/2` (Java trunc div):

| i (loopvar) | j (row radius) | OLD row-Y `below(i)` = 100 - i | NEW row-Y `above(i)` = 100 + i |
|-------------|----------------|--------------------------------|--------------------------------|
| 0           | 1 (narrow)     | 100                            | 100                            |
| -1          | 1 (narrow)     | 101                            | 99                             |
| -2          | 2 (WIDE)       | 102                            | 98                             |
| -3          | 2 (WIDE)       | 103                            | 97                             |

OLD: widest rows (j=2) at the HIGHEST Y (102,103) -> wide-at-top = upside-down.
NEW: widest rows (j=2) at the LOWEST Y (97,98) -> wide-at-bottom = vanilla. Top row stays at
the attachment (Y=100), blob drapes DOWNWARD.

DarkOak and MegaPine were already correct on the sign and were left unchanged on Y direction:
DarkOak uses `center = att.Pos.above(offset)` + signed localY; MegaPine passes an absolute `yy`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] DarkOakFoliagePlacer collapsed all canopy rows onto one Y**
- **Found during:** auditing the two non-`below` placers (DarkOak / MegaPine) the task asked
  to verify against bytecode.
- **Issue:** DarkOak passes a FIXED `center` plus a signed `localY` ∈ {-1,0,1,2} to
  `placeLeavesRowSigned`, which ignores `localY` for Y (dy=0). So all 2-4 canopy rows landed on
  the SAME Y (a flat 1-row canopy) instead of vanilla's `center.Y + localY`. It also used
  `att.Pos.above(foliageHeight)` for the center, but vanilla bytecode (`iload 9` -> `BlockPos.above`)
  uses `above(offset)`.
- **Fix:** `center = att.Pos.above(offset)`; bake each row's localY into the position via
  `center.above(localY)` (above accepts negatives, so `above(-1) == below(1)`).
- **Files modified:** world/levelgen/feature/tree_placers.go (DarkOakFoliagePlacer.createFoliage)
- **Verified:** vanilla `DarkOakFoliagePlacer.createFoliage` bytecode (rows at radius+2/-1,
  radius+3/0, radius+2/1, [nextBoolean] radius/2; else radius+2/-1, radius+1/0).

**2. [Rule 1 - Bug] TestOakBeesNest validated bug-dependent geometry**
- **Found during:** full `./world/...` run after the fix.
- **Issue:** the test grew `super_birch_bees` (tall STRAIGHT trunk + tight blob foliage) and
  expected a bee_nest. The BeehiveDecorator attaches the nest to a trunk-side cell (N/E/W of a
  log at `targetY = leaves[0].Y - 1`) whose own SOUTH face is air. With the CORRECTED foliage,
  the j=1 blob row fully encloses the trunk top -> no exposed face -> no nest. Confirmed via
  bytecode that this matches vanilla (the nest genuinely cannot attach to that enclosed trunk);
  the OLD upside-down blob grew AWAY from the trunk top, leaving it exposed (a bug side effect).
- **Fix:** retargeted the test to `fancy_oak_bees` (FancyFoliagePlacer = branched per-blob canopy
  that leaves trunk faces open). Verified nests place across many seeds with the corrected
  geometry.
- **Files modified:** world/feature_tree_test.go

**3. [Rule 1 - Bug] TestTerrainSanity asserted feet-clear via the legacy terrain-only spawn path**
- **Found during:** full `./world/...` run after the fix.
- **Issue:** the test computed spawn Y from `SpawnSurfaceY` (terrain-only WorldSurface heightmap)
  at the FIXED (8,8) column, then asserted the feet cell there is non-solid. A jungle tree grows
  at (8,8); its now-correct DOWNWARD foliage reaches y=86 (feet=spawnY+2), so (8,8) feet = leaf.
  This is exactly the spawn-inside-decoration class that 17-06 replaced `SpawnSurfaceY` with the
  decoration-aware `SpawnPos` (PlayerSpawnFinder) to fix.
- **Fix:** the spawn-safety assertion now uses the real `SpawnPos` finder (reads the
  fully-decorated chunk, scans the WHOLE chunk for the first standable column) and asserts floor
  standable + feet/head clear. Confirmed `SpawnPos` returns column-local (0,0) at feet Y=85 with
  air at feet+head -> correctly avoids the leaf-draped (8,8) column. The terrain-range sanity on
  `SpawnSurfaceY` is retained.
- **Files modified:** world/noisegen_test.go

## Regression Guard Added

`TestBlobFoliageWideAtBottom` (world/levelgen/feature/tree_test.go): grows the canonical oak
blob at Y=100 and asserts (a) no leaf row sits above the attachment Y, and (b) the widest row
is the BOTTOM-most row (lowest Y) and is strictly below the narrowest row. This fails if the
foliage ever mirrors back to wide-at-top. The existing `TestBlobFoliagePlacer` oracle was
regenerated to the corrected mapping (`y := i`, bound `i >= offset - foliageHeight`).

## Verification

- `CGO_ENABLED=0 go build ./...` -> exit 0.
- `go test ./world/...` -> all packages PASS (including the previously-failing TestTerrainSanity
  and TestOakBeesNest, now corrected).
- Determinism gates PASS: `TestDecorationReorderIdentical`, `TestEmitOnce`,
  `TestEmitOnceUnderHold` (the change is a deterministic arithmetic sign flip; no rng/draw-order
  or shared-state change, so reproducibility is preserved).
- `go vet ./world/levelgen/feature/` clean.
- `-race` could not be run in this environment (no cgo/gcc toolchain present); the change adds no
  shared state, and the concurrent decoration determinism gates pass.

## Self-Check: PASSED

- world/levelgen/feature/tree.go: FOUND (BlobFoliagePlacer above(i) + loop bound + comment)
- world/levelgen/feature/tree_placers.go: FOUND (Spruce/Pine/Bush/Fancy/Jungle above() + DarkOak Y fix)
- world/levelgen/feature/tree_test.go: FOUND (oracle regenerated + TestBlobFoliageWideAtBottom)
- world/feature_tree_test.go: FOUND (TestOakBeesNest -> fancy_oak_bees)
- world/noisegen_test.go: FOUND (TestTerrainSanity -> SpawnPos)
- All `./world/...` tests + the 3 named determinism gates: PASS
