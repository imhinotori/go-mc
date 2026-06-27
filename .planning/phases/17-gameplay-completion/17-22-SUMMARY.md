---
phase: 17
plan: 17-22
subsystem: gameplay / block-survival
tags: [block-survival, vegetation, updateOrDestroy, 1to1-port, deferred-gate-close]
requires: [GAMEPLAY-06 spawnBlockDrop, reconcileEdit, IsVegetationGround]
provides: [block.IsVegetation, block.IsDoublePlant, updateVegetationOnEdit]
affects: [server/block_interact.go reconcileEdit, level/block predicates]
tech-stack:
  added: []
  patterns: [neighbor-update recursion (bounded 512), reuse-existing-drop-path]
key-files:
  created:
    - server/block_survival.go
    - server/block_survival_test.go
    - level/block/vegetation_test.go
  modified:
    - level/block/utilfuncs.go
    - server/block_interact.go
    - .planning/phases/17-gameplay-completion/deferred-items.md
decisions:
  - "Scope to VEGETATION on #supports_vegetation only; torches/rails/redstone/doors + the dry/flowerbed/mangrove/seagrass plants (different ground predicate) deferred to a follow-up."
  - "Cascade via the cell ABOVE only (vegetation canSurvive depends solely on below), bounded by the vanilla 512 recursion seed; covers single plants + 2-tall double plants + stacked columns."
  - "Survival destroy passes a NIL player to spawnBlockDrop so it always drops (matches destroyBlock's null-entity arg: a creative player breaking the ground still drops the flower above)."
metrics:
  duration: "~1 session"
  completed: 2026-06-27
---

# Phase 17 Plan 22: Block-Survival (Vegetation) Summary

One-liner: breaking a block that supports a plant now destroys + drops the unsupported vegetation
the instant its ground support is removed — a 1:1 port of `Level.updateNeighborsAt` ->
`VegetationBlock.updateShape` -> `Block.updateOrDestroy`, scoped to the `#supports_vegetation` plant
family (flowers, saplings, short_grass/fern, bushes, 2-tall double plants).

## What shipped

- **`level/block/utilfuncs.go`** — `IsVegetation` (single-cell base-`canSurvive` family),
  `IsDoublePlant` / `DoublePlantLowerHalf` / `SameDoublePlant` (the half-dependent 2-tall family).
  Reuses the existing `IsVegetationGround` (`#supports_vegetation`) ground predicate.
- **`server/block_survival.go`** — `updateVegetationOnEdit` / `destroyUnsupportedVegetationAbove`:
  ports `VegetationBlock.canSurvive` (== `IsVegetationGround(below)`), `DoublePlantBlock.canSurvive`
  (UPPER survives over its matching LOWER half), and `Block.updateOrDestroy`
  (`newState.isAir()` -> `destroyBlock(pos, dropBlock=(flags&32)==0, null, recursionLeft=512)`).
  Recursion bounded at 512 so 2-tall plants + stacked columns cascade. Reuses GAMEPLAY-06
  `spawnBlockDrop` + `broadcastBlockUpdate(air)`.
- **`server/block_interact.go`** — wired `updateVegetationOnEdit` into `reconcileEdit` alongside
  `scheduleFluidNeighborsOnEdit`, so the survival check runs on both the break and place paths.

## Jar citations (temp/cache/26.2-inner.jar, `javap -c -p`)

- `VegetationBlock.updateShape`: `if (!state.canSurvive(level, pos)) return AIR.defaultBlockState();`
- `VegetationBlock.canSurvive` -> `mayPlaceOn(belowState)` -> `belowState.is(SUPPORTS_VEGETATION)`.
- `DoublePlantBlock.canSurvive`: `HALF==UPPER ? (belowState.is(this) && belowState.HALF==LOWER) : super.canSurvive(...)`.
- `Block.updateOrDestroy`: `newState.isAir() && !isClientSide -> destroyBlock(pos, (flags&32)==0, null, recursionLeft)`; recursionLeft seed = `sipush 512`.
- Per-block `javap -p` confirmed each in-scope Go type extends VegetationBlock with NO
  canSurvive/mayPlaceOn override (FlowerBlock incl. EyeblossomBlock, TallGrassBlock, base
  SaplingBlock, BushBlock, FireflyBushBlock); excluded blocks (DryVegetationBlock,
  FlowerBedBlock/LeafLitterBlock, MangrovePropaguleBlock, CactusFlowerBlock, Seagrass) DO override.

## Tests

- `server/block_survival_test.go`: predicate unit-check + break-under-flower drops + 2-tall grass
  cascade + non-vegetation no-op + still-supported survives + full survival-dig-path integration.
- `level/block/vegetation_test.go`: `IsVegetation` / `IsDoublePlant` / `SameDoublePlant` coverage,
  incl. the deliberate exclusions.

## Gates

- `CGO_ENABLED=0 go build ./...` exit 0; `go vet ./...` clean.
- `CGO_ENABLED=0 go test ./server/ ./level/...` green.
- Docker `-race` (`golang:1.26 go test -race ./server/ ./level/...`) green.
- No new deps; no `import "C"`.

## Still deferred (follow-up)

The NON-vegetation survival classes: torches/walls, rails (`BaseRailBlock`), redstone
(`RedStoneWireBlock`/`DiodeBlock`), doors/beds (two-cell), ladders/vines/signs/banners/pressure
plates, and the plants on a DIFFERENT ground predicate (DeadBush/ShortDryGrass/TallDryGrass,
LeafLitter/PinkPetals/Wildflowers, MangrovePropagule, CactusFlower, Seagrass/TallSeagrass/LilyPad,
mushrooms, crops, nether plants). The `destroyUnsupportedVegetationAbove` recursion seam is the
template to extend — those classes also need side/below neighbor checks for wall/floor mounts.

## Self-Check: PASSED

- server/block_survival.go, server/block_survival_test.go, level/block/vegetation_test.go: FOUND
- commit 8b799d42 (feat(server): destroy unsupported vegetation when its ground support breaks): FOUND
