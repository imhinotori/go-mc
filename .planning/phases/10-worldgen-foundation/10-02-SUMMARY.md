---
phase: 10-worldgen-foundation
plan: 02
subsystem: level + world/levelgen/surface
tags: [worldgen, heightmap, gen2, determinism, post-carve]
requires: []
provides:
  - "level.HeightmapUpdate — the incremental jar-exact Heightmap.update primitive (O(1)-amortized per-write column-top fix)"
  - "surface.BuildWorldgenHeightmaps — builds all 3 worldgen heightmaps (WORLD_SURFACE_WG/OCEAN_FLOOR_WG/MOTION_BLOCKING) from post-carve terrain"
  - "recomputeOceanFloorWG + recomputeMotionBlockingWG (per-type opaque predicates mirroring recomputeWorldSurfaceWG)"
affects:
  - "world/noisegen.go (FINISH step now builds all 3 worldgen heightmaps post-carve)"
  - "world/generator.go (Superflat populates its worldgen heightmaps via the same helper)"
  - "10-03 Neighborhood proxy + Phase 11+ feature decoration (will CALL HeightmapUpdate on every worldgen block write)"
tech-stack:
  added: []
  patterns:
    - "Jar-exact Heightmap.update port: firstAvailable-2 early-out, opaque set-(y+1), non-opaque downward rescan via opaqueAt closure"
    - "Pure top-down per-column heightmap build with per-type opaque predicate (mirrors existing recomputeWorldSurfaceWG)"
    - "Allocation-free, chunk-free primitive (BitStorage + predicate closure) so it serves any of the 3 worldgen heightmaps"
key-files:
  created:
    - "level/heightmap.go (HeightmapUpdate primitive)"
    - "level/heightmap_test.go (TestHeightmapUpdate — place/remove/below-surface/floor)"
    - "world/levelgen/surface/heightmap_test.go (TestWorldgenHeightmapsBuild + Deterministic)"
  modified:
    - "world/levelgen/surface/system.go (+86: BuildWorldgenHeightmaps + 2 recompute* siblings)"
    - "world/noisegen.go (+21/-4: FINISH-step worldgen-heightmap build)"
    - "world/generator.go (+10: Superflat worldgen-heightmap build via shared helper)"
decisions:
  - "Used built-in Read/Edit (not Serena MCP) — Serena MCP tools are not present in this agent's tool set; go build/test are the source of truth per the plan's stale-LSP caveat"
  - "Wired BuildWorldgenHeightmaps into the generator FINISH step (after ApplyCarvers, before Status=Full) so all 3 worldgen heightmaps reflect FINAL carved terrain — documented in noisegen.go"
  - "Populated Superflat's worldgen heightmaps via the same helper (no carve/fluid → OCEAN_FLOOR_WG==WORLD_SURFACE_WG) rather than leaving them nil"
metrics:
  duration: "~10m"
  completed: 2026-06-25
  tasks: 2
  files: 6
---

# Phase 10 Plan 02: Live Worldgen Heightmap (HeightmapUpdate + OCEAN_FLOOR_WG/MOTION_BLOCKING) Summary

GEN2-03: a jar-exact incremental `HeightmapUpdate` primitive in the `level` package plus
the two missing worldgen heightmaps (`OCEAN_FLOOR_WG`, `MOTION_BLOCKING`) built from
POST-CARVE terrain alongside the existing `WORLD_SURFACE_WG`, so all three worldgen
heightmaps are live and correct before the first decoration feature runs — while the
3 CLIENT heightmaps stay finalized by the unchanged `writeClientHeightmaps`/`WriteTo`
wire path.

## What Was Built

### Task 1 — `level.HeightmapUpdate` primitive (commit `8d9228b0`)
`level/heightmap.go` (new) + `level/heightmap_test.go` (new):

- **`HeightmapUpdate(bs *BitStorage, lx, y, lz, minY int, opaque bool, opaqueAt func(y int) bool)`**
  ported verbatim from `net.minecraft.world.level.levelgen.Heightmap.update`:
  - `firstAvail := bs.Get(col)+minY`; early-out when `y <= firstAvail-2` (below surface-1).
  - opaque write: raise to `(y+1)-minY` when `y >= firstAvail`.
  - non-opaque write AT the exact surface (`firstAvail-1 == y`): rescan downward via
    `opaqueAt`, set to the next opaque top or `0` (floor) if none.
- Pure + allocation-free (no chunk reference — caller supplies the BitStorage + the
  `opaqueAt` closure), so it works against any of the 3 worldgen heightmaps. It is the
  O(1)-amortized per-write counterpart to the bulk `recompute*`. Phase 10's `Decorate` is
  a no-op so it is built + tested now but not yet CALLED in production; 10-03's
  Neighborhood proxy + the feature phase wire it on every worldgen block write.
- **`TestHeightmapUpdate`** (7 subtests): place-opaque-on-empty → y+1; place at/above
  surface raises; place well below = no-op; remove exact surface → rescan to next opaque;
  remove the only opaque → floor 0; non-opaque below surface = no-op; non-opaque at a
  non-surface y = no-op.

### Task 2 — `OCEAN_FLOOR_WG` + `MOTION_BLOCKING` from post-carve terrain (commit `0b60ff3c`)
`world/levelgen/surface/system.go` (+86), `world/noisegen.go`, `world/generator.go`, and
`world/levelgen/surface/heightmap_test.go` (new):

- **`BuildWorldgenHeightmaps(ch, minY, maxY)`** (exported) resolves air/caveAir/water
  itself and builds all 3 worldgen heightmaps with their jar-confirmed `Heightmap$Types`
  predicates:
  - `WORLD_SURFACE_WG` = NOT_AIR (existing `recomputeWorldSurfaceWG`).
  - `OCEAN_FLOOR_WG` = motion-blocking AND NOT fluid (`recomputeOceanFloorWG`, new).
  - `MOTION_BLOCKING` = blocks-motion OR fluid (`recomputeMotionBlockingWG`, new).
  Both new siblings mirror `recomputeWorldSurfaceWG` exactly (top-down per-column scan,
  minY-relative store, clamp-negative-to-0).
- **Wired into `NoiseGenerator.Generate`'s FINISH step** (after `ApplyCarvers`, before
  `Status=Full`) so the 3 worldgen heightmaps reflect the FINAL carved terrain — the
  pre-decoration build GEN2-03 requires. The CLIENT heightmaps stay the job of
  `BuildSurface`/`writeClientHeightmaps`.
- **Superflat** also populates its worldgen heightmaps via the same helper (no carve/fluid
  → `OCEAN_FLOOR_WG == WORLD_SURFACE_WG`), so a superflat world carries a complete
  worldgen heightmap too.
- **`TestWorldgenHeightmapsBuild`**: a stone column capped by water then air proves the
  per-predicate DIVERGENCE — `WORLD_SURFACE_WG`/`MOTION_BLOCKING` land at the water top,
  `OCEAN_FLOOR_WG` lands strictly below at the stone top. **`TestWorldgenHeightmapsDeterministic`**:
  all-air floors all three to 0, and the build is idempotent.

## Test Results

```
go build ./...                                                    → exit 0
CGO_ENABLED=0 go build ./...                                      → exit 0
go vet ./level/... ./world/levelgen/...                           → clean
go test ./level/ -run TestHeightmapUpdate -count=1               → ok
go test ./world/levelgen/surface/ -run TestWorldgenHeightmaps    → ok
go test ./world/ -run 'TestNoiseGenDeterministic|TestNoiseGenChunkComplete' → ok (16.4s)
go test ./world/ -run 'TestSuperflat|Generator|Packet|Chunk'     → ok
go test ./level/ (full package, incl. chunk_capture)             → ok
go test ./world/levelgen/surface/ (full package)                 → ok
```

The noise generator's determinism + chunk-completeness are intact; the wire/capture tests
(heightmap count=3, ids={1,4,5}) and the packet round-trip still pass.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — Tooling] Used built-in Read/Edit instead of Serena MCP**
- **Found during:** Task 1 (agent startup).
- **Issue:** The prompt mandated Serena tools for code reading/editing, but the Serena MCP
  tools (`mcp__serena__*`) are not present in this agent's tool set.
- **Fix:** Used the built-in Read/Edit/Write tools; the edits are surgical and identical in
  effect. Per the plan's explicit stale-LSP caveat, `go build`/`go test` are the source of
  truth — all gates are green.
- **Files modified:** n/a (process note).
- **Commit:** n/a.

### Design notes (within plan scope)

- **MOTION_BLOCKING field is shared between the worldgen and CLIENT heightmaps.** The
  `level.Chunk.HeightMaps` struct has a single `MotionBlocking` field. The plan's
  interfaces section ties the worldgen MOTION_BLOCKING to this field, and notes the values
  coincide for noise terrain (water blocks motion). For the noise generator,
  `BuildWorldgenHeightmaps` runs post-carve and overwrites `MotionBlocking`; this only
  diverges from the post-surface value when a carve breaches the surface, in which case the
  post-carve value is the MORE correct one (vanilla's MOTION_BLOCKING reflects final
  terrain). The wire INVARIANT is preserved structurally: `WriteTo` still emits exactly the
  3 CLIENT heightmaps with ids {1,4,5} (verified by the unchanged capture test), and no
  test pins the exact MotionBlocking value on the wire. No deviation — this is the plan's
  documented MOTION_BLOCKING build.
- **`go test -race`** is not runnable in this environment (race requires cgo; `CGO_ENABLED=0`
  is the project default and cgo is unavailable). The new code is pure deterministic scans
  with no shared mutable state, so it is race-clean by construction; the CLAUDE.md `-race`
  gate applies to the (future) concurrent worker seam (10-03/GEN2-02), not these pure
  primitives.

## Threat Surface

T-10-03 (a wrong opaque predicate / off-by-one mis-placing future features) is mitigated as
planned: `HeightmapUpdate` is ported from the jar bytecode and pinned by the
place/remove/below-surface/floor unit tests; each of the 3 worldgen heightmaps uses its
jar-confirmed predicate, proven by the known-column round-trip + per-predicate-divergence
test. T-10-04 (leaking worldgen heightmaps onto the wire) stays accepted/mitigated: `WriteTo`
is untouched and still emits only the 3 CLIENT heightmaps. No new threat surface introduced.

## GEN2-03 Status: COMPLETE

A live, mutable worldgen heightmap is built from post-carve terrain before the first feature
(all 3 worldgen heightmaps with divergent jar-confirmed predicates), and an incremental
`HeightmapUpdate` primitive exists to keep it live on every worldgen block write. The wire
format (3 CLIENT heightmaps) + persistence are unchanged; zero new dependencies; pure Go.

## Self-Check: PASSED

All created files exist on disk (level/heightmap.go, level/heightmap_test.go, world/levelgen/surface/heightmap_test.go, 10-02-SUMMARY.md) and both task commits (8d9228b0, 0b60ff3c) are present in git history.
