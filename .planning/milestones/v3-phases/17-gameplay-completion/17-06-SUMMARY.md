---
phase: 17
plan: "17-06"
subsystem: world-spawn
tags: [spawn, worldgen, player-placement, gap-closure, port]
type: gap-closure
requires:
  - "world.NoiseGenerator (full-parity decorated Generate)"
  - "level.Chunk heightmaps (WorldSurface/OceanFloor/MotionBlocking) + section block API"
  - "server bootstrap/respawn placement (sendPlayBootstrap / performRespawn)"
provides:
  - "world.NoiseGenerator.SpawnPos -> SAFE fresh-spawn (x,y,z) over the DECORATED chunk"
  - "server.SpawnPoint threaded through NewGameTick -> join bootstrap + in-game respawn"
affects:
  - "cmd/sulfur/main.go (spawn computation + wiring)"
  - "server bootstrap + respawn placement"
tech-stack:
  added: []
  patterns:
    - "port vanilla net.minecraft.server.level.PlayerSpawnFinder (getLevelRespawnPos / getSpawnPosInChunk)"
    - "conservative full-up-face floor approximation (no-collision plant deny-set)"
    - "bounded outward chunk spiral as the all-ocean radius fallback"
key-files:
  created:
    - "world/spawn.go"
    - "world/spawn_test.go"
    - "world/spawn_unit_test.go"
  modified:
    - "world/noisegen.go"
    - "cmd/sulfur/main.go"
    - "server/gameplay_tick.go"
    - "server/tick.go"
    - "server/combat.go"
    - "server/gameplay_tick_test.go"
    - "server/play_join_test.go"
    - "server/position_load_test.go"
decisions:
  - "Read the FULLY-DECORATED chunk (Generate) for spawn, not the terrain-only GenerateTerrain"
  - "Thread the full safe (x,y,z) (not just Y) so a fresh spawn lands at the safe column, not blind (8.5,_,8.5)"
  - "Conservative standable-floor: descend past no-collision plants to real ground"
  - "All-ocean origin -> bounded chunk spiral, else float-on-sea fallback (vanilla last resort)"
metrics:
  duration: "~1 session"
  tasks: 1
  files-created: 3
  files-modified: 8
completed: 2026-06-25
---

# Phase 17 Plan 06: Spawn-Inside-A-Block Fix (PlayerSpawnFinder Port) Summary

Fixed the Phase-17 visual-gate bug where a fresh player spawned EMBEDDED in a block /
decoration, by porting vanilla `net.minecraft.server.level.PlayerSpawnFinder`
(`getLevelRespawnPos` + `getSpawnPosInChunk`) and running it over the FULLY-DECORATED spawn
chunk, then threading the resulting SAFE (x,y,z) into both the join bootstrap and the in-game
respawn.

## Root Cause (confirmed)

`world/noisegen.go` `SpawnSurfaceY(pos)` sampled the **terrain-only** `WorldSurface` heightmap
(it called `GenerateTerrain`, NOT the full decorated `Generate`) at the FIXED `(8,8)` column,
then the bootstrap did a blind `surfaceY + 2`. So the spawn Y was computed BEFORE worldgen
decoration/structures placed blocks in the spawn column — and the player ended up inside a vine,
a tree trunk, a village house, or underwater. This is structural, not seed-specific.

## The Port (cited)

Decompiled the authoritative algorithm from `temp/cache/26.2-inner.jar` via
`javap -c -p net.minecraft.server.level.PlayerSpawnFinder` and ported it faithfully (idiomatic
Go, no GPL paste). The class was renamed from `PlayerRespawnLogic` in 26.2; both
`getLevelRespawnPos(level,x,z)` and `getSpawnPosInChunk(level,chunkPos)` still exist and were
transcribed from bytecode:

- **`getLevelRespawnPos`** (overworld, no ceiling): `mY = MOTION_BLOCKING.getHeight`; reject if
  `mY < minY` (void). `wsY = WORLD_SURFACE`, `ofY = OCEAN_FLOOR`; reject if `wsY > mY && ofY > mY`
  (water/ocean covers the column). Then walk DOWN from `mY+1`: the first block whose fluid state
  is non-empty aborts the column (return null); the first block with a full UP collision face is
  the floor — return the pos ABOVE it (the player's feet cell).
- **`getSpawnPosInChunk`**: scan EVERY column of the chunk (x-major, then z) and return the first
  non-null `getLevelRespawnPos`. The whole-chunk scan is why a tree/ocean at `(8,8)` does not
  break spawn — a clear neighbor column is found.

Sulfur-specific adaptations (documented in `world/spawn.go`):
- **Fluid detection** ("fluid state empty"): the mid-worldgen chunk has no live FluidState graph,
  so the canonical check is realized as a type test against the only two fluid blocks the
  generator emits (`block.Water` / `block.Lava`).
- **Full-up-face floor** (`Block.isFaceFull(collisionShape, UP)`): collision shapes are
  unavailable mid-worldgen, so it is approximated CONSERVATIVELY — a block is standable iff it is
  not air, not fluid, and not one of the no-collision surface plants/decorations the generator
  places on top (grass/ferns/flowers/saplings/vines/mushrooms/bushes/...). Misclassifying a plant
  only makes the walk descend one block deeper to real ground (always safe); it can never report
  a passable block AS a floor.
- **All-ocean origin fallback** (vanilla's `findSpawn` candidate-radius search): a bounded
  outward chunk spiral (radius 3) finds the nearest standable column; a genuinely deep-ocean
  origin with no land in range legitimately floats the player on the sea surface (the terrain
  `SpawnSurfaceY` fallback), exactly as vanilla does as a last resort.

## Before / After (verified by tests)

Default seed `0x5EED_C0DE` — the `(8,8)` spawn column is filled with **vine decoration**:

```
(8,88,8)=vine   (8,85,8)=vine   (8,84,8)=grass_block   (8,83,8)=dirt   (8,82,8)=stone
```

- **OLD**: terrain WorldSurface at `(8,8)` = y84, blind `+2` -> feet at `(8.5, 86, 8.5)` — placed
  in/over the vine-filled center column (player settles into the vine at y85).
- **NEW**: PlayerSpawnFinder rejects/avoids the vine column and lands the player at
  `(0.5, 85, 0.5)` on a clean `grass_block` (no vine) — the `(0,0)` column has clear air at y85.

Seed `0x2` — the `(8,8)` origin column is OCEAN:

- **OLD**: feet at `(8.5, 57, 8.5)` **inside `water`** (drowning/embedded).
- **NEW**: the whole-chunk scan rejects the ocean column and finds land at `(0.5, 64, 0.5)` on
  `grass_block`.

## What Changed

- `world/spawn.go` (new): `SpawnPoint`, `SpawnPos` (decorated-chunk getSpawnPosInChunk + spiral
  fallback), `levelRespawnY` (getLevelRespawnPos port), fluid + standable-floor predicates, the
  passable-decoration deny-set.
- `world/noisegen.go`: `SpawnSurfaceY` now delegates to `SpawnPos` (decoration-aware), with the
  old terrain-only `(8,8)` read kept ONLY as the void/ocean scalar fallback.
- `cmd/sulfur/main.go`: computes the safe `server.SpawnPoint` from `ng.SpawnPos` and threads it
  into `NewGameTick`.
- `server/gameplay_tick.go`: new `SpawnPoint` type; `gameTick.spawnPoint`; `NewGameTick` takes it
  and forwards it to the tick (`SetSpawnPoint`); `AcceptPlayer` seeds a FRESH spawn from
  `spawnPoint` (a reconnecting player's persisted .dat still overrides — GAMEPLAY-02 intact); the
  bootstrap teleport now always uses the authoritative coords (`hasSpawn: true`).
- `server/tick.go`: `TickLoop.spawnPoint` + `SetSpawnPoint`.
- `server/combat.go`: `performRespawn` re-teleports to the SAFE spawn column (not blind
  `(8.5, spawnSurfaceY+2, 8.5)`), seeding `p.center` from it; falls back to the center column only
  when `SetSpawnPoint` was never wired (unit tests).
- Test call-site updates for the new `NewGameTick` signature.

## Tests

- `world/spawn_test.go`: `TestSafeSpawnStandable` (default + 25 + ocean seed 2 — each yields a
  non-fluid solid floor with >=2 air above, with logged before/after coords),
  `TestSafeSpawnDeterministic` (pure over seed, stable across calls/generators),
  `TestSafeSpawnSurfaceYConsistent`.
- `world/spawn_unit_test.go`: fast synthetic-chunk `TestLevelRespawnYSemantics` (clean floor /
  plant-skip / fluid-reject / void-reject) + `TestPassableDecorationPredicate`.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0. `go vet ./world/ ./server/ ./cmd/...` — clean.
- `go test ./world/ ./server/` — PASS (incl. the determinism gates
  `TestDecorationReorderIdentical`, `TestEmitOnce`, `TestEmitOnceUnderHold`,
  `TestNoiseGenDeterministic`, and `TestTerrainSanity` — the spawn change READS the decorated
  chunk but does NOT mutate generation).
- `-race` could not run in this environment (no gcc/cgo); the change is set-once-before-`Run` and
  read-only on the tick goroutine (same discipline as the existing `spawnSurfaceY`/`SetSpawn`), so
  it adds no new shared mutable state.

## Deviations from Plan

**[Rule 2 - Missing critical functionality] In-game respawn placement + all-ocean fallback.**
- The plan focused on the FRESH join spawn, but `performRespawn` (`server/combat.go`) had the SAME
  blind `(8.5, spawnSurfaceY+2, 8.5)` placement bug — a respawn could re-embed the player. Threaded
  the safe spawn point through the tick (`SetSpawnPoint`) so respawn uses it too. Also added the
  bounded-spiral fallback so an ocean-origin world still finds real ground instead of floating the
  player on the sea surface. Both are correctness requirements for the spawn fix, not new scope.

## Self-Check: PASSED
