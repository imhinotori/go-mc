---
phase: 24-vanilla-mobs-as-plugins
plan: 01
subsystem: plugin-handle-api + mob-ai-rng
tags: [plugins, starlark, mob-ai, rng, capability, 1to1-port, handle-extension]
requires:
  - "Phase 23 entity/world/nav handles (server/plugin_entity.go) + capability enforcement (plugin_capability.go)"
  - "Phase 23 starlarkGoal + buildAIFromDecl (plugin_mob_ai.go)"
  - "The ported passive Pig goals (server/ai_goals_passive.go) + newPigAI (ai_mob.go)"
provides:
  - "Per-entity seeded RandomSource (server/ai_random.go: entityRandom, newEntityRandom, reseed, nextInt/nextFloat/nextDouble)"
  - "mobAI.rng plumbed into newPigAI + buildAIFromDecl; reseedMobAI per entity id at both spawn sites"
  - "entity.set_look(yaw, pitch=0) handle seam (entities.write)"
  - "entity.rand_int(n) / entity.rand_float() handle seams (ungated — the mob's own RNG)"
  - "world.nearest_player(x,y,z,range) handle seam (world.read), reusing nearestPlayerAt"
affects:
  - "Wave 2 (24-02): the vanilla_pig.star goals consume set_look / nearest_player / rand_int/rand_float"
tech-stack:
  added: []
  patterns:
    - "Per-entity seeded math/rand/v2 PCG source (Mob.getRandom() analogue) — faithful draw ORDER, not bit-exact stream"
    - "Additive handle methods on the existing entityHandle/worldHandle (no new package, no import-direction change)"
    - "Position-based scan core (nearestPlayerAt) factored out so a seam reuses the identical tick-owned t.players scan"
key-files:
  created:
    - server/ai_random.go
    - server/ai_random_test.go
  modified:
    - server/ai_mob.go
    - server/ai_goals_passive.go
    - server/plugin_mob_ai.go
    - server/async.go
    - server/debug.go
    - server/plugin_entity.go
    - server/plugin_entity_test.go
decisions:
  - "Seeded math/rand/v2 PCG per entity (not a bit-exact LegacyRandomSource port) — 24-RESEARCH Open-Q §4 A4: no test asserts bit-exact vanilla sequences; the draw ORDER is the observable behavior"
  - "set_look writes headYaw AND yaw (+pitch) instantly — behavior-identical to the Go look goals it replaces (Pitfall 2)"
  - "rand_int/rand_float are UNGATED (the mob's own RNG, like has_path); set_look gates entities.write; nearest_player gates world.read"
metrics:
  duration: 9min
  tasks: 2
  files: 9
  completed: 2026-06-28
---

# Phase 24 Plan 01: Per-entity seeded RandomSource + 3 faithful handle extensions Summary

Built the THREE handle-API extensions the Phase-23 surface was missing to express real vanilla Pig AI — `entity.set_look`, `entity.rand_int`/`rand_float`, `world.nearest_player` — plus the per-entity seeded RandomSource (the `Mob.getRandom()` analogue) that makes the AI 1:1-faithful (the bytecode draw ORDER) AND deterministic, retiring the documented `TestTickAIDrivesMobs` global-rand flake. Wave-1 ships the seams the Wave-2 `vanilla_pig.star` goals consume; no spawn swap yet.

## What shipped

### Task 1 — Per-entity seeded RandomSource (commit 2d8d8ac9)

- `server/ai_random.go`: `entityRandom{r *rand.Rand}`, `newEntityRandom(seed)`, `reseed(seed)`, and the three draws `nextInt(n)` / `nextFloat()` / `nextDouble()` — backed by a per-entity seeded `math/rand/v2.PCG` (zero new deps). Same seed → identical stream.
- `mobAI.rng` field; `newPigAI()` and `buildAIFromDecl()` create it; `reseedMobAI(m, id)` derives a per-entity deterministic seed from the entity id at the two spawn sites (`async.go`, `debug.go`).
- `server/ai_goals_passive.go`: the three passive goals now draw from `e.ai.rng` (via the nil-safe `mobRandom(e)` helper) in the EXACT bytecode order; the package-global RNG is gone (the import removed; the verification grep is clean).

### Task 2 — 3 handle extensions (commit 2be8584c)

- `entity.set_look(yaw, pitch=0)` — writes `headYaw == yaw == float32(yaw)` (+`pitch`) instantly, gated `entities.write`; non-finite yaw/pitch clamped to 0 (T-24-03).
- `entity.rand_int(n)` / `entity.rand_float()` — draw from `e.ai.rng`, UNGATED; `n<=0` / no-AI / removed-entity error cleanly.
- `world.nearest_player(x,y,z,range)` — nearest player tuple or `None`, gated `world.read`, reusing the factored `nearestPlayerAt` scan over `t.players` (players are NOT in the entityStore).

## The bytecode draw order (the 1:1 anchor — javap-verified this session)

Disassembled from `temp/cache/26.2-inner.jar` via `javap -c -p` BEFORE writing (the 1:1 mandate):

| Goal (jar class) | canUse draw order | start draw order |
|------------------|-------------------|------------------|
| `RandomStrollGoal` (WaterAvoiding base) | `hasControllingPassenger?` → `forceTrigger?` → (`checkNoActionTime`) → `getRandom().nextInt(reducedTickDelay(interval))` **[gate]** → `getPosition()` = `DefaultRandomPos.getPos(mob,10,7)` (3× `nextInt`) | — (start = `navigation.moveTo(wanted…)`) |
| `LookAtPlayerGoal` | `getRandom().nextFloat()` → `< probability` (0.02), then find player | `lookTime = 40 + getRandom().nextInt(40)` |
| `RandomLookAroundGoal` | `getRandom().nextFloat() < 0.02f` | `d = 6.283185307179586d * getRandom().nextDouble()`; `relX=cos(d)`; `relZ=sin(d)`; `lookTime = 20 + getRandom().nextInt(20)` |

The Go ports already matched this structure; this plan only swapped the SOURCE (package-global → per-entity seeded) while preserving the order, confirmed by `TestEntityRandFaithfulDrawOrder` which pins each goal's draw sequence against a replayed reference source.

## Which existing tests now run against the per-entity source

`TestRandomStrollSetsTarget`, `TestLookAtPlayerFacesNearest`, `TestServerAiStepOrder`, `TestPigGoalSetRegistered`, `TestServerAiStepWalksToGoalTarget`, `TestTickAIDrivesMobs`, `TestDebugPigUsesRealAI` — all pass unchanged (they use the `forceTrigger`/`alwaysLook` seams that bypass the ROLL, not the source; the seeded source did not shift any asserted value). `TestTickAIDrivesMobs` is now deterministic (5/5).

## Wave-2 flag: RandomLookAround `requiresUpdateEveryTick` is NOT honored by starlarkGoal

`RandomLookAroundGoal.requiresUpdateEveryTick()` returns `true` in the jar (and the Go `randomLookAroundGoal` overrides it to `true`). The Phase-23 `starlarkGoal` (server/plugin_mob_ai.go) embeds `baseGoal`, whose `requiresUpdateEveryTick()` returns `false`, and it does NOT override that from the `goalDecl`. So when Wave 2 re-expresses RandomLookAround as a declared goal, that goal will only tick under the selector's normal running-goal path, NOT the every-tick path vanilla guarantees. **Plan 02 must thread `requires_update_every_tick` (or equivalent) from the declaration into the starlarkGoal so the ported RandomLookAround is faithful** — this is a real Wave-2 deliverable, flagged here per the plan output spec.

## Verification

- `CGO_ENABLED=0 go build ./...` clean; `go vet ./server/` clean.
- `CGO_ENABLED=0 go test ./server/ -run 'TestSeededAIRandom|TestEntityRandFaithfulDrawOrder|TestSetLookSeam|TestNearestPlayerSeam|TestTickAIDrivesMobs' -count=5` passes.
- `CGO_ENABLED=0 go test ./server/ ./plugin/...` full suite green.
- `! grep -nE 'math/rand/v2' server/ai_goals_passive.go` — clean (package RNG gone from the AI goals).
- Docker `-race` (CGO=1, golang:1.26) over the new AI + handle seams + the existing AI suite: clean (the per-entity rng is tick-owned; the seams re-resolve by id, never store a live `*Entity`).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Comment text tripped the `! grep math/rand/v2` verification gate**
- **Found during:** Task 1 verification
- **Issue:** A descriptive comment in `ai_goals_passive.go` mentioning the old `math/rand/v2` package matched the plan's `! grep -nE 'math/rand/v2'` gate, which would have failed CI even though the import was removed.
- **Fix:** Reworded the comment to "shared package-global stdlib RNG" (no literal `math/rand/v2`).
- **Files modified:** server/ai_goals_passive.go
- **Commit:** 2d8d8ac9

No other deviations — the plan executed as written.

## Self-Check: PASSED

- server/ai_random.go — FOUND
- server/ai_random_test.go — FOUND
- server/plugin_entity.go (set_look/rand_int/rand_float/nearest_player) — FOUND
- Commit 2d8d8ac9 — FOUND
- Commit 2be8584c — FOUND
