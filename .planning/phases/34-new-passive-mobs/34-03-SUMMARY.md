---
phase: 34-new-passive-mobs
plan: 03
subsystem: mob-passive
tags: [mob, passive, chicken, jar-port, starlark-plugin, dogfood, slow-fall, egg-lay, registerGoals, mob-pass]

# Dependency graph
requires:
  - phase: 34-00
    provides: "nearest_player_holding_food(tag,range) parameterized tempt handle; TickLoop.chickenAiStep (slow-fall + egg-lay) + dropChickenEgg + chickenIsChickenJockey const; Entity.eggTime field + spawnDeclaredMob eggTime spawn-init (nextInt(6000)+6000, Chicken-gated); tick_phases.go chickenAiStep wiring (entity.Chicken.ID-gated, additive)"
  - phase: 24-02
    provides: "the vanilla_pig dogfood template (declare_mob/goal builtins, the starlarkGoal runtime, the byte-identical embed-pair discipline, loadMobRegistry + spawnDeclaredMob)"
provides:
  - "vanilla_chicken plugin pair (plugins/ + server/assets/, byte-identical) — the 8-goal Chicken.registerGoals set as a 1:1 Starlark declaration"
  - "server/chicken_test.go — boot-load (entity.Chicken.ID + 8 goals @0..@7 + 0.4x0.7 hitbox), behavior (walk/tempt/CREATURE), slow-fall-applies-to-a-plugin-chicken, egg-lay-applies-to-a-plugin-chicken"
affects: [34-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "third 1:1 vanilla-mob dogfood = a declare_mob Starlark plugin reusing the proven Go goal runtime; only goal PARAMS + food tag + attributes differ from the pig (the JARNOTES dogfood-pays-off thesis)"
    - "per-mob host aiStep extras (slow-fall + egg-lay) are NOT plugin-expressed goals — the plugin only makes the entity EXIST as entity.Chicken so the 34-00 host hook applies"
    - "test loads the on-disk plugins/vanilla_chicken/ copy into an isolated temp dir via loadMobRegistry, merges the decl into the loop registry, spawns via the shared spawnDeclaredMob (no new Go embed/loader file needed for the gate)"

key-files:
  created:
    - "plugins/vanilla_chicken/main.star"
    - "plugins/vanilla_chicken/plugin.toml"
    - "server/assets/vanilla_chicken/main.star"
    - "server/assets/vanilla_chicken/plugin.toml"
    - "server/chicken_test.go"
  modified: []

key-decisions:
  - "TestChickenBehavior asserts CREATURE via the authoritative data/entity Chicken.Type == \"creature\" (NOT a categoryOf() Go edit — categoryOf is a pig-only stub in the shared mob_category.go and editing it would race with cow/sheep). The data table is the vanilla category source."
  - "The test loads the on-disk plugins/vanilla_chicken/ operator copy (byte-identical to the embed) rather than adding a bespoke vanilla_chicken_embed.go — the plan's files_modified lists no new Go infra file, and the gate only needs the decl spawnable in a test loop."
  - "TestChickenSlowFallsLive + TestChickenEggLaysLive drive loop.chickenAiStep(chicken) directly — the EXACT call tick_phases.go makes for a live entity.Chicken — proving the 34-00 hook applies to a real plugin-spawned chicken, not just the synthetic 34-00 unit fixture."

patterns-established:
  - "Pattern: a wave-2 new-mob plan is disjoint — only its own .star pair + its own _test.go; zero shared Go file edits (the pig oracle + the cow/sheep plans stay untouched + race-free)."

requirements-completed: [MOB-PASS-03]

# Metrics
duration: ~22min
completed: 2026-06-30
---

# Phase 34 Plan 03: vanilla_chicken Plugin (MOB-PASS-03) Summary

**The Chicken's 8-goal `Chicken.registerGoals` set re-expressed as a byte-identical 1:1 Starlark dogfood plugin (single chicken_food Tempt, MH 4.0 / spd 0.25, 0.4x0.7 hitbox), with tests proving a plugin-spawned chicken walks/tempts and that the 34-00 slow-fall + egg-lay aiStep hook applies to it.**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-06-30T14:04:19Z
- **Completed:** 2026-06-30T~14:26Z
- **Tasks:** 1
- **Files modified:** 5 created

## Accomplishments
- `vanilla_chicken` declares the 8-goal `Chicken.registerGoals` set 1:1 from the unobfuscated 26.2 jar: FloatGoal@0 (JUMP, requiresUpdateEveryTick), PanicGoal(1.4)@1, BreedGoal(1.0)@2, TemptGoal(1.0, CHICKEN_FOOD)@3, FollowParentGoal(1.1)@4, WaterAvoidingRandomStrollGoal(1.0)@5, LookAtPlayerGoal(6.0)@6, RandomLookAroundGoal@7 (MOVE|LOOK, requiresUpdateEveryTick). Consecutive @0..@7 — a SINGLE chicken_food Tempt (no carrot-on-a-stick goal, which is pig-only).
- The @3 TemptGoal uses the 34-00 parameterized `entity.nearest_player_holding_food("chicken_food", TEMPT_RANGE)` handle; the breed @2 routes through the one host `try_breed`; all RNG draw orders mirror the proven Go goal runtime.
- Attributes `max_health 4.0` / `movement_speed 0.25` (Chicken.createAttributes); the smaller 0.4x0.7 chicken hitbox auto-applies via `NewEntity(decl.baseType)`.
- Both `.star` copies and both `.toml` copies are byte-identical (verified by `diff` AND `git hash-object`).
- `chicken_test.go` proves the 34-00 host extras apply to a REAL plugin-spawned chicken: the slow-fall (`vy *= 0.6` per falling tick, only y, only when `!onGround && vy<0`) and the egg-lay (drops exactly one `minecraft:egg`, resets `eggTime` to 6000..12000, baby skips).
- The pig oracle (`TestPluginPigEqualsGoNativePig`) is untouched and byte-identical.

## Task Commits

1. **Task 1: vanilla_chicken plugin pair (8 goals, byte-identical) + chicken behavior test** — `7706be26` (feat)

## Files Created/Modified
- `plugins/vanilla_chicken/main.star` - the 8-goal Chicken.registerGoals declaration (1:1 jar port, single chicken_food Tempt, the 34-00 food handle)
- `plugins/vanilla_chicken/plugin.toml` - least-privilege caps `["entities.read","entities.write","world.read","nav"]` (the pig set; slow-fall/egg-lay are host-side, no new cap)
- `server/assets/vanilla_chicken/main.star` - byte-identical embed copy
- `server/assets/vanilla_chicken/plugin.toml` - byte-identical embed copy
- `server/chicken_test.go` - TestChickenBootLoads, TestChickenBehavior (walks/tempts/CREATURE), TestChickenSlowFallsLive, TestChickenEggLaysLive + the countEggItems helper

## Decisions Made
- **CREATURE asserted via `entity.Chicken.Type == "creature"`, not a `categoryOf()` Go edit.** `categoryOf` (mob_category.go) is a pig-only stub in a SHARED, pig-touching file; mapping chicken there is out of this plan's scope (it belongs to 34-00 / would race with the parallel cow + sheep plans). The data/entity table is the authoritative vanilla MobCategory source and needs no Go edit — and the chicken spawns/behaves fine without a categoryOf entry (the gate spawns directly, not via the natural spawner).
- **No bespoke `vanilla_chicken_embed.go`.** The plan's `files_modified` lists no new Go infra file. The test loads the on-disk operator copy (`../plugins/vanilla_chicken`) into an isolated temp dir via the existing `loadMobRegistry` harness, merges the decl into the loop registry, and spawns through the shared `spawnDeclaredMob` — sufficient for the gate, and it keeps the plan disjoint from the pig embed.
- **Slow-fall/egg-lay tests call `loop.chickenAiStep` directly** — the exact call `tick_phases.go` makes for a live `entity.Chicken` — to prove the 34-00 hook fires for a plugin-spawned chicken (vs the synthetic 34-00 unit fixture).

## Deviations from Plan

None — plan executed exactly as written. (No Rule 1-4 deviations; the chicken plugin + test landed with no shared-Go edit, exactly as the plan scoped. The CREATURE-assertion and no-embed-file choices above are in-scope design choices within the plan's stated approach, not deviations.)

## Issues Encountered

**Out-of-scope discovery (logged, NOT fixed): a broken parallel-executor WIP `server/cow_test.go`.**
- During final `go test ./...`, the server test package failed to COMPILE because of `server/cow_test.go` — an UNTRACKED, in-progress file from the parallel 34-01 (cow) executor that references symbols not yet in the tree (`loop.tryMilkCow`, `loop.tick`, `component`, `bytesReader`, `cow.attributes.value`). Its mtime is AFTER this plan's files — it appeared mid-execution (the wave-2 parallel-execution scenario the plan's critical rules anticipate).
- **NOT touched.** It belongs to 34-01 and will compile once that plan lands `tryMilkCow` + helpers; fixing it here would be a cross-plan race. Logged to `.planning/phases/34-new-passive-mobs/deferred-items.md`.
- The chicken tests + the full pig oracle suite were verified GREEN by temporarily PARKING cow_test.go (moving it aside UNMODIFIED, running the suite, restoring it byte-for-byte — 13331 bytes, untouched). With cow parked, the entire `go test ./server/` passes (8.6s).

## Verification
- `CGO_ENABLED=0 go build ./...` → exit 0.
- `CGO_ENABLED=0 go vet ./server/` → clean.
- `diff plugins/vanilla_chicken/main.star server/assets/vanilla_chicken/main.star` → empty (byte-identical); same for plugin.toml; `git hash-object` blobs match.
- `grep -v '^#' main.star | grep -c 'priority = '` → 8; `grep -c 'def tempt_carrot'` → 0; `nearest_player_holding_food("chicken_food"` matches; `base_type = "chicken"`, `"max_health": 4.0`, `"movement_speed": 0.25` all match.
- `CGO_ENABLED=0 go test ./server/ -run 'TestChicken|TestPluginPig|TestVanillaPig|TestSlowFall|TestEggLay' -v` → ALL PASS (TestChickenBootLoads, TestChickenBehavior/{walks,tempts_on_chicken_food}, TestChickenSlowFallsLive, TestChickenEggLaysLive, + the full pig oracle TestPluginPigEqualsGoNativePig). Verified with cow_test.go parked (it is the parallel cow executor's broken WIP).
- Full `go test ./server/` (cow parked) → ok, 8.6s.

## Next Phase Readiness
- MOB-PASS-03 (chicken) is satisfied: the 8-goal declaration, entity.Chicken.ID render, attrs 4.0/0.25, the 0.4x0.7 hitbox, and the 34-00 slow-fall + egg-lay on a plugin-spawned chicken are all proven.
- **Blocker for 34-04 (gate):** the parallel cow plan's `server/cow_test.go` must compile before the phase-wide `go test ./...` / the `-race` Docker run is green. That is 34-01's responsibility (it lands `tryMilkCow`); 34-04 should run the phase gate only after cow + sheep have landed their Go symbols.
- The pig oracle stays the bit-exact gate; the chicken (like the sheep) reuses the proven goals so its lighter per-mob spawn+behavior+extras test suffices.

## Self-Check: PASSED

---
*Phase: 34-new-passive-mobs*
*Completed: 2026-06-30*
