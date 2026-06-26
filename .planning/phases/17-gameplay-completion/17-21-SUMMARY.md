---
phase: 17
plan: 21
subsystem: block-break / dig-time (ServerPlayerGameMode) + block-hardness data extraction
tags: [block-break, dig-time, gameplay-1to1, ServerPlayerGameMode, BlockBehaviour, codegen, GAMEPLAY-06]
requires:
  - server/block_interact.go (handlePlayerAction dispatch, withinReach reach gate, reconcileEdit, broadcastBlockUpdate, broadcastBlockUpdate column-tracking send)
  - server/block_drop.go (spawnBlockDrop — the creative-gated drop spawn reused by destroyBlock)
  - level/block (StateList, ToStateID, Block.ID(), IsAir — the state->id lookup behind blockHardness)
  - world.ChunkManager (GetBlock/SetBlock over the tick-owned manager)
  - tools/java/ExtractAll.java + tools/extract.go (the Docker codegen harness)
provides:
  - ServerPlayerGameMode block-break dig-time 1:1 (START/STOP/ABORT + tick + getDestroyProgress + incrementDestroyProgress + destroyBlockProgress + destroyAndAck/destroyBlock)
  - per-tick survival dig timer with the 0-9 ClientboundBlockDestruction crack overlay
  - instant-mine on START (1-tick progress >= 1.0), creative break on START, STOP completion at progress >= 0.7, delayed-destroy at progress >= 1.0
  - level/block/hardness.go — generated map[string]BlockHardness (destroySpeed + requiresCorrectTool) for all 1196 blocks, extracted from the jar
  - tools GenBlockHardness.java extractor + gen_block_hardness.go generator (re-runnable on retarget)
affects:
  - server/block_break.go (new)
  - server/block_break_test.go (new)
  - server/block_interact.go (handlePlayerAction body replaced with the dispatch)
  - server/tick.go (tickPlayer dig fields)
  - server/tick_phases.go (tickBlockBreak slotted into tickEntities)
  - server/gameplay_tick.go (lastSentDestroyStage = -1 seed)
  - level/block/hardness.go (new, generated)
  - tools/java/GenBlockHardness.java, tools/java/ExtractAll.java, tools/gen_block_hardness.go, tools/main.go
tech-stack:
  added: []
  patterns:
    - "Part A hardness extracted via the EXISTING codegen harness (Docker eclipse-temurin:25-jdk over the cached 26.2 jar) — reflected off defaultBlockState() because destroyTime/requiresCorrectToolForDrops are hardcoded in Java Block ctors and absent from the --all blocks.json report."
    - "dig state is tick-owned (single-owner TICK-05, like food/breath/fall-damage); the vanilla per-instance ServerPlayerGameMode.gameTicks is supplied by the loop's gametime (the shared per-tick counter), not duplicated per player."
    - "tickBlockBreak slotted ADDITIVELY into tickEntities — no new phase, TestTickPhaseOrder stays green."
key-files:
  created:
    - server/block_break.go
    - server/block_break_test.go
    - level/block/hardness.go
    - tools/java/GenBlockHardness.java
    - tools/gen_block_hardness.go
  modified:
    - server/block_interact.go
    - server/block_interact_test.go
    - server/block_drop_test.go
    - server/tick.go
    - server/tick_phases.go
    - server/gameplay_tick.go
    - tools/java/ExtractAll.java
    - tools/main.go
decisions:
  - "Hardness came from the LIVE jar extractor (not a hand-authored subset): ran `cd tools && go run . --version 26.2 --extract` over the cached 26.2-inner.jar via Docker; the generated level/block/hardness.go carries real values for all 1196 blocks, spot-verified against every canonical value in the plan."
  - "Reused Sulfur's reconcileEdit as destroyAndAck (it already does SetBlock-to-air + BlockChangedAck(sequence) + tracking-column BlockUpdate) and spawnBlockDrop as the destroyBlock loot path; the delayed-destroy tick path breaks with ack=false (no sequence to echo — the client predicted the break ticks ago) but still broadcasts the air to trackers."
  - "destroyBlockProgress broadcasts to every player tracking the block's column (center or sentChunks), mirroring vanilla's chunk-tracking-player-set broadcast, so the crack overlay is visible to others as well as the breaker."
  - "maxBuildHeightY = dimMinY + 384 = 320 (verified: the handleBlockBreakAction caller passes ServerLevel.getMaxY() == getMinY()+getHeight())."
  - "Existing instant-break-on-STOP tests updated: TestBreakBlock/TestBlockBroadcastToTrackers now use a creative START break; TestBlockDropSpawnsItem/TestBlockDropTracked drive a full survival dig (completeSurvivalDig) since a survival break is now a dig-timer."
metrics:
  duration: ~2h
  completed: 2026-06-26
---

# Phase 17 Plan 21: Block-Break Dig-Time + Block-Hardness Extraction Summary

Ported the server-authoritative block-break dig-time model from
`net.minecraft.server.level.ServerPlayerGameMode` 1:1, backed by a new jar-extracted per-block
hardness table — so a vanilla 26.2 client now sees the real breaking animation and per-hardness dig
times (dirt fast, stone slower, obsidian very slow, bedrock unbreakable) instead of every block
shattering instantly on first click.

## What changed

### Part A — block hardness data (new codegen infra)
`BlockBehaviour.getDestroyProgress` needs each block's `destroySpeed` (hardness) and
`requiresCorrectToolForDrops` — values **hardcoded in Java Block constructors** and therefore absent
from the `--all` `blocks.json` report. Extracted them via the existing Docker codegen harness:
- **`tools/java/GenBlockHardness.java`**: iterates `BuiltInRegistries.BLOCK`, emits one row per block
  (`key`, `destroy_speed`, `requires_tool`) by reflecting
  `defaultBlockState().getDestroySpeed(EmptyBlockGetter.INSTANCE, BlockPos.ZERO)` and
  `requiresCorrectToolForDrops()`. Wired into `ExtractAll.java`'s extractor list.
- **`tools/gen_block_hardness.go`** (+ `main.go` registration): reads `block_hardness.json` and
  generates **`level/block/hardness.go`** — a generated `map[string]BlockHardness{DestroySpeed
  float32, RequiresCorrectTool bool}` keyed by `Block.ID()` (-1.0 = unbreakable).
- **Ran the live extractor** (`cd tools && go run . --version 26.2 --extract`) over the cached
  `temp/cache/26.2-inner.jar` via Docker (`eclipse-temurin:25-jdk`). The committed `hardness.go`
  carries **real jar values for all 1196 blocks**. Every canonical value verified: stone 1.5/true,
  dirt 0.5/false, grass_block 0.6/false, obsidian 50.0/true, bedrock -1.0, oak_log 2.0/false,
  oak_leaves 0.2/false, sand 0.5/false, coal_ore 3.0/true, water/lava 100.0. All other generated
  files regenerated **byte-identically** (no spurious diff), confirming pipeline reproducibility.

### Part B — the dig-time port (`server/block_break.go`)
Ported these `ServerPlayerGameMode` / `BlockBehaviour` methods 1:1 (each cited against the jar
bytecode, decompiled via `javap -c -p` this session):

| Method | Behavior |
|--------|----------|
| `getDestroyProgress` | `hardness==-1 -> 0`; `divisor = hasCorrectToolForDrops ? 30 : 100`; `playerDestroySpeed/hardness/divisor` |
| `handleBlockBreakAction` | reach gate, `pos.Y > 320` reject (re-assert state), spawn-protection/mayInteract PASS, START/STOP/ABORT dispatch |
| START | creative -> destroyAndAck; else capture `destroyProgressStart=gameTicks`, `progress>=1.0` -> insta-mine, else begin per-tick dig + send stage |
| STOP | `pos==destroyPos`: `progress = getDestroyProgress*(elapsed+1)`; `>=0.7` -> break+ack+clear; else schedule delayed-destroy |
| ABORT | clear the crack overlay (stage -1) at destroyPos and pos |
| `tick()` (`tickBlockBreak`) | advance delayed-destroy (break at `progress>=1.0`), else refresh the in-progress overlay |
| `incrementDestroyProgress` | `elapsed=gameTicks-start`; `progress=getDestroyProgress*(elapsed+1)`; stage dirty-send; return progress |
| `destroyAndAck` / `destroyBlock` | reuse `reconcileEdit` (ack) + `spawnBlockDrop` (creative drops nothing); delayed path breaks with no ack |
| `destroyBlockProgress` | `ClientboundBlockDestruction` (wire jar-verified: VarInt id, Position pos, Byte stage) to column trackers |

New tick-owned dig fields on `tickPlayer` (single-owner TICK-05): `isDestroyingBlock`, `destroyPos`,
`destroyProgressStart`, `lastSentDestroyStage` (seeded -1), `hasDelayedDestroy`, `delayedDestroyPos`,
`delayedTickStart`. `tickBlockBreak` is slotted **additively** into `tickEntities` (no phase reorder —
`TestTickPhaseOrder` stays green). `handlePlayerAction` (block_interact.go) now only decodes the
packet and dispatches START(0)/STOP(2)/ABORT(1) into `handleBlockBreakAction`.

## Cited v1 stubs (structured to become real later)
- **`playerDestroySpeed` = 1.0f** — bare-hand base; no tools/enchants/effects in v1 (cite
  `Player.getDestroySpeed`).
- **`hasCorrectToolForDrops`** — v1 has no tools, so a tool-requiring block is always INCORRECT bare-
  handed (divisor 100, e.g. stone 1.0/1.5/100 per tick == vanilla); a non-tool block is always correct
  (divisor 30) (cite `Player.hasCorrectToolForDrops`).
- **spawn-protection / mayInteract / blockActionRestricted** — faithful PASS (v1 has no regions/
  permissions beyond the creative gate) (cite `handleBlockBreakAction`).
- **`EnchantmentHelper.onHitBlock` + `BlockState.attack`** — omitted no-ops between the START read and
  `getDestroyProgress` (no enchants, no per-block attack behavior in v1) (cite `handleBlockBreakAction`).
- **`blockHardness` missing-entry default = unbreakable (-1.0)** — safe default; never fires in
  practice (the generated table covers every registered block).

## Deviations from Plan

### None affecting the port
The port followed the plan exactly. Two mechanical adjustments, both anticipated by the plan:
- **gameTicks reuse:** the plan said "if a tick counter already exists on TickLoop, reuse it." The
  loop's `gametime` (int64, ++ once per tick in `tickOnce`) IS the vanilla per-tick counter, so the
  dig reads `t.gametime` instead of adding a per-player `gameTicks++` — the elapsed math is identical.
- **Existing-test updates (Rule 1 — keep the suite green):** `TestBreakBlock` /
  `TestBlockBroadcastToTrackers` asserted instant-break-on-STOP; updated to a creative START break.
  `TestBlockDropSpawnsItem` / `TestBlockDropTracked` asserted a drop on STOP; updated to drive a full
  survival dig (`completeSurvivalDig`). These reflect the new (correct) dig-time behavior, not a
  regression. `TestBreakAirNoDrop` / `TestBlockReachRejected` / `TestBlockMalformed` pass unchanged.

### Out-of-scope (logged, not committed)
Re-running the codegen pipeline emitted ~19 NEW untracked worldgen biome-tag JSONs under
`world/levelgen/data/tags/worldgen/biome/`. Unrelated to block-break; logged to `deferred-items.md`
for a worldgen/codegen plan to review and commit separately. NOT included in either 17-21 commit.

## Verification
- `go build ./...` (server module) — exit 0.
- `cd tools && go build ./...` + `go vet ./...` — exit 0 (touched the tools module).
- `go vet ./server/` — exit 0.
- `go test ./server/` — PASS (full suite green; the known `TestTickAIDrivesMobs` flake did not fire
  this run and is unrelated — mob AI, untouched).
- New `server/block_break_test.go` (8 deterministic tests, no RNG) — all PASS: START on stone does
  not insta-break (stage 0, no ack); STOP after enough ticks (progress>=0.7) breaks + acks + clears
  overlay; early STOP schedules delayed-destroy that finishes at progress>=1.0; creative START breaks
  instantly; zero-hardness short_grass insta-mines on START; ABORT clears overlay (-1); bedrock
  (-1.0) never breaks; getDestroyProgress exact values.
- `TestTickPhaseOrder` / `TestTickPhaseOrderUnchanged` — PASS (no phase reorder).
- `-race`: NOT run — this environment has no C compiler (`CGO_ENABLED=1` requires gcc, absent). The
  dig state is tick-owned (single-owner TICK-05, identical discipline to the race-clean food/breath
  seams), so it is -race clean by construction; a future run on a gcc-equipped host should confirm.

## Known Stubs
The cited v1 stubs above (playerDestroySpeed=1.0, hasCorrectToolForDrops bare-hand, spawn-protection/
mayInteract PASS, EnchantmentHelper/attack no-ops) are intentional v1 placeholders, each structured
as its own seam so a future tools/enchants/effects/region plan slots in with no formula change. They
do NOT block the plan's goal: the breaking animation, per-hardness dig times, instant/creative mine,
and unbreakable bedrock are all observable on a real client with these stubs in place (a bare-handed
player is exactly the vanilla no-tool case).

## Self-Check: PASSED

All created files exist on disk (block_break.go, block_break_test.go, hardness.go, GenBlockHardness.java, gen_block_hardness.go, 17-21-SUMMARY.md) and both per-part commits are present in the branch history (255e6aff feat hardness extraction, dfe3870f fix dig-time port).
