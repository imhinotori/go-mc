---
phase: 35-hostiles
plan: 05
subsystem: mob-ai
tags: [spider, leap-at-target, melee, daylight-gate, hostiles, mob-as-plugin, rng-lockstep, starlark]

# Dependency graph
requires:
  - phase: 35-01
    provides: "the targetSelector machinery + meleeAttackGoal base + nearestAttackable/hurtByTarget target goals + attackTargetID/setTarget + the TARGET-flag buildAIFromDecl routing + the mob-attack damage path"
  - phase: 35-02
    provides: "the isDarkEnoughToSpawn day/night gametime proxy (the FORCED light-read stand-in) + the MONSTER category cap"
  - phase: 34-mob-as-plugin
    provides: "buildAIFromDecl + the starlarkGoal adapter + spawnDeclaredMob + the vanilla_cow template + the byte-identical embed pair invariant"
provides:
  - "leapAtTargetGoal — the ONE goal that IMPULSES (setDeltaMovement), with the nextInt(reducedTickDelay(5)=>raw 5) gate + the normalize().scale(0.4)+0.2*delta horizontal / yd vertical impulse vector"
  - "the faithful Spider$SpiderAttackGoal daylight-flee: canContinueToUse draws nextInt(100) ONLY when bright (the day proxy) and on 0 drops the target (setTarget(null)) — replacing the wave-1 always-block-in-daylight stub"
  - "isBright (== !isDarkEnoughToSpawn) wiring the daylight gate to the live 35-02 proxy"
  - "the vanilla_spider plugin pair (byte-identical repo-root + embed) — Spider.registerGoals v1 subset"
  - "world.is_dark() host seam — the day/night darkness proxy exposed read-only under world.read"
affects: [35-03, 35-04, 36-wolf, wall-climb-physics, lighting-engine, hostile-plugin-embed-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the IMPULSE goal exception: LeapAtTargetGoal sets a velocity delta (setDeltaMovement / entity.set_velocity), NOT a nav want — the only goal that does not path"
    - "the full-rate-tick RNG identity extended to the leap gate: nextInt(reducedTickDelay(5)) -> the raw nextInt(5) (NOT the jar's halved 3), the same compensation NearestAttackableTargetGoal (10 not 5) follows"
    - "the daylight-flee is a stochastic canContinueToUse target-drop (1/100/tick when bright), NOT a canUse block — bright == !isDarkEnoughToSpawn (the 35-02 day/night proxy)"
    - "a read-only world proxy seam (world.is_dark) lets a hostile .star express a light-gated behavior faithfully while the brightness read stays Go-side (cite-deferred to the lighting engine)"

key-files:
  created:
    - plugins/vanilla_spider/main.star
    - plugins/vanilla_spider/plugin.toml
    - server/assets/vanilla_spider/main.star
    - server/assets/vanilla_spider/plugin.toml
    - server/spider_test.go
  modified:
    - server/ai_goals_attack.go
    - server/plugin_entity.go

key-decisions:
  - "THE LEAP GATE IS nextInt(reducedTickDelay(5)), NOT nextFloat — the exec-time decompile this session CORRECTS the 35-JARNOTES pre-decompile guess; the 1:1-with-the-jar mandate is absolute, so the real bytecode wins. Faithful Go bound = the raw 5 (the full-rate identity)."
  - "The SpiderAttackGoal daylight behavior is the stochastic 1/100 daylight-flee in canContinueToUse (decompiled: br>=0.5 && nextInt(100)==0 -> setTarget(null)), NOT a flat no-attack-in-daylight canUse block — the wave-1 stub was rewritten to match the bytecode."
  - "world.is_dark() added as a read-only world.read seam (Rule 2) so the spider .star expresses the daylight gate; the embed boot-load wiring (vanillaMobNames///go:embed) is LEFT to the shared hostile-integration step to avoid a wave-2 cross-plan test collision."

patterns-established:
  - "Impulse goal: set_velocity/setDeltaMovement, never setWantTarget — the leap pounce"
  - "Light-gated mob behavior via the world.is_dark day/night proxy seam"

requirements-completed: [MOB-HOST-03]

# Metrics
duration: ~35min
completed: 2026-06-30
---

# Phase 35 Plan 05: Spider (Leap + Daylight Melee) Summary

**The third hostile as a jar-faithful dogfood plugin: vanilla_spider with the LeapAtTargetGoal impulse (the nextInt(5)-gated setDeltaMovement pounce) + the Spider$SpiderAttackGoal 1/100 daylight-flee, both ported method-for-method from the 26.2 bytecode and lockstep-tested.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-30T15:19Z
- **Completed:** 2026-06-30T15:54Z
- **Tasks:** 3
- **Files modified:** 7 (5 created, 2 modified)

## Accomplishments
- `leapAtTargetGoal` (ai_goals_attack.go): the distance-band [4,16] + on-ground guards, the `nextInt(reducedTickDelay(5)=>raw 5)` gate drawn LAST (out-of-band/airborne reject draws zero RNG), and the `start()` impulse (`normalize().scale(0.4) + 0.2*delta` horizontal, `yd` vertical) via `setDeltaMovement` — the ONE goal that impulses, never paths.
- The faithful `Spider$SpiderAttackGoal` daylight-flee: `canContinueToUse` draws `nextInt(100)` ONLY when bright (the day proxy) and on a 0 roll drops the target (`setTarget(null)`) — rewriting the wave-1 always-block stub to match the decompile.
- The `vanilla_spider` plugin pair (byte-identical repo-root + embed) declaring the Spider.registerGoals v1 subset: FloatGoal@1 + LeapAtTarget@3 + SpiderAttack@4 + stroll@5(0.8) + look@6(8.0) + around@6; targetSelector HurtByTarget@1 + SpiderTarget<Player>@2. Attrs max_health 16.0 + movement_speed 0.3.
- `world.is_dark()` read-only seam wiring the daylight gate to the live 35-02 proxy.
- Six tests (3 leap/daylight Go-native lockstep + boot-load + behavior); the pig oracle stays byte-identical.

## Task Commits

1. **Task 1: leapAtTargetGoal + faithful spiderAttackGoal daylight-flee** - `6a73700f` (feat)
2. **Task 2: vanilla_spider plugin pair + world.is_dark seam** - `93c91fab` (feat)
3. **Task 3: spider behavior + boot-load + leap/daylight RNG tests** - `e4055ce0` (test)

## Files Created/Modified
- `server/ai_goals_attack.go` (modified) - added `leapAtTargetGoal` (the impulse goal + the nextInt(5) gate constants) and rewrote the `spiderAttackGoal` daylight handling to the faithful 1/100 `canContinueToUse` flee (replacing `isNight`-always-true with `isBright == !isDarkEnoughToSpawn`).
- `server/plugin_entity.go` (modified) - added the `world.is_dark()` seam (reads `t.isDarkEnoughToSpawn`, gated on `world.read`).
- `plugins/vanilla_spider/main.star` + `plugin.toml` (created) - the jar-faithful spider declaration + least-privilege manifest.
- `server/assets/vanilla_spider/main.star` + `plugin.toml` (created) - the byte-identical embed copies.
- `server/spider_test.go` (created) - the leap/daylight lockstep + boot-load + behavior tests.

## Decisions Made
See `key-decisions` frontmatter. The load-bearing one: the leap gate is `nextInt(reducedTickDelay(5))`, NOT `nextFloat` — the exec-time decompile corrected the 35-JARNOTES guess, and the 1:1-with-the-jar mandate made the bytecode authoritative.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] The leap gate is nextInt(5), not nextFloat (the plan/JARNOTES pre-decompile guess was wrong)**
- **Found during:** Task 1 (the exec-time `javap` decompile of `LeapAtTargetGoal.canUse`)
- **Issue:** The plan's `must_haves`, `key_links`, and Task-2 acceptance criteria (`rand_float` for the leap, `rand_int(5)` count == 0) encode the 35-JARNOTES line-127/140 pre-decompile assumption that the leap is `nextFloat`-gated. The actual 26.2 bytecode is `getRandom().nextInt(reducedTickDelay(5))` (the modern 1.21+ LeapAtTargetGoal — the old `nextFloat()<0.05` gate is gone). The CLAUDE.md 1:1 mandate is absolute: the jar wins.
- **Fix:** Ported the REAL `nextInt(reducedTickDelay(5))` gate; the faithful Go bound is the raw 5 (the full-rate-tick identity, mirroring `nearestTargetRandomInterval`=10). The `.star` uses `rand_int(LEAP_REDUCED_INTERVAL=5)`. The `rand_float`/`rand_int(10)` acceptance greps still pass (the Float/look/around/stroll gates supply `rand_float`; the SpiderTarget gate supplies `rand_int(10)` via a named const). The `grep rand_int(5)==0` criterion is technically violated by ONE explanatory comment (the code uses the named constant), kept as load-bearing deviation documentation.
- **Files modified:** server/ai_goals_attack.go, plugins/vanilla_spider/main.star (+ embed)
- **Verification:** `TestLeapAtTargetGateRNG` lockstep-proves exactly one `nextInt(5)` drawn (a `nextFloat` or wrong bound would desync).
- **Committed in:** 6a73700f / 93c91fab

**2. [Rule 1 - Bug] The SpiderAttackGoal daylight behavior is the 1/100 canContinueToUse flee, not a canUse block**
- **Found during:** Task 1 (the `Spider$SpiderAttackGoal` decompile)
- **Issue:** The wave-1 (35-01) `spiderAttackGoal` stub blocked `canUse`/`canContinueToUse` entirely in daylight (`!isNight`). The real bytecode: `canUse = super.canUse() && !isVehicle()` (NO daylight check); `canContinueToUse` = `if (br>=0.5f && nextInt(100)==0) setTarget(null), return false; else super.canContinueToUse()` — a stochastic 1-in-100/tick target-drop when bright.
- **Fix:** Rewrote the daylight handling: removed the unfaithful `canUse` daylight block (kept `!isVehicle` as a cited no-op) and implemented the `canContinueToUse` 1/100 flee gated on `isBright` (== `!isDarkEnoughToSpawn`). Wired `isBright` to the live 35-02 proxy (the wave-1 `isNight` always-true stub was a recorded TODO to "wire in 35-02/35-05").
- **Files modified:** server/ai_goals_attack.go
- **Verification:** `TestSpiderAttackDaylightGate` (no draw + retained target at night; 1/100 drop in daylight); the wave-1 melee/target tests + the pig oracle still pass.
- **Committed in:** 6a73700f

**3. [Rule 2 - Missing Critical] Added the world.is_dark() seam for the .star daylight gate**
- **Found during:** Task 2 (no world brightness/daylight seam existed for the .star to express the gate)
- **Issue:** The spider `.star`'s SpiderAttackGoal daylight branch needs to read the day/night proxy, but no such seam was exposed (the first hostile that needs it, 35-03/zombie, had not landed). `plugin_entity.go` was not in the plan's `files_modified`.
- **Fix:** Added a minimal read-only `world.is_dark()` seam (returns `t.isDarkEnoughToSpawn()`, gated on `world.read`). Least-privilege, no new capability; the brightness read stays Go-side (cite-deferred to `getLightLevelDependentMagicValue`).
- **Files modified:** server/plugin_entity.go
- **Verification:** `TestSpiderBootLoads`/`TestSpiderBehavior` load + drive the spider (whose `.star` calls `world.is_dark`) without error; the full suite passes.
- **Committed in:** 93c91fab

---

**Total deviations:** 3 auto-fixed (2 jar-fidelity bug fixes, 1 missing-critical seam)
**Impact on plan:** The two fidelity fixes were MANDATORY under the absolute 1:1-with-the-jar mandate (the plan encoded pre-decompile guesses the exec-time bytecode contradicted). The seam was the minimal addition needed for the `.star` to express the daylight gate. No scope creep.

## Known Stubs / Cite-Deferred (recorded for the verifier)

| Deferral | Where | Reason | Resolves with |
|----------|-------|--------|---------------|
| `AvoidEntityGoal<Armadillo>@2` | vanilla_spider main.star (DEFERRED comment) | no Armadillo entity / no AvoidEntityGoal port in v1 | the Armadillo + AvoidEntityGoal plan |
| `SpiderTargetGoal<IronGolem>@3` | vanilla_spider main.star (DEFERRED) | no IronGolem entity in v1 | the IronGolem plan |
| WALL-CLIMB (the spider's defining movement) | vanilla_spider main.star (DEFERRED) | no climb subsystem (nav is ground-only); Spider.tick sets the CLIMBING flag from horizontalCollision — a physics/nav behavior, not a goal | the wall-climb physics plan |
| `getLightLevelDependentMagicValue` (the real light read) | ai_goals_attack.go isBright / world.is_dark | no light engine in v1 — the gametime day/night proxy (the SAME FORCED 35-02 decision); the daylight-flee RNG draw IS still made when bright (fidelity) | the lighting engine |
| embed boot-load wiring (`//go:embed` + `vanillaMobNames`) | server/vanilla_pig_embed.go (NOT modified) | shared file jointly extended by 35-03..05; modifying it here would break the shared `TestAllFourMobsBootLoad` count and risk a wave-2 cross-plan collision | the hostile-plugin embed integration (all three hostiles together) |

These are intentional, cited stubs equal to the vanilla default, structured to become real reads later — none alter observable behavior beyond the recorded gap. The spider hunts/leaps/melees the player at night on the ground meanwhile.

## Issues Encountered
- Adding the spider to the shared `vanillaMobNames`/`//go:embed` broke `TestAllFourMobsBootLoad` (count 4 -> 5). Reverted the embed wiring (the test loads the spider from the repo-root copy, so it is not needed for this plan's verification) and left the production embed extension to the joint hostile integration — avoiding a wave-2 cross-plan collision on the shared file.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The leap impulse goal + the daylight-flee are reusable Go-native primitives (the wolf/cave-spider reuse the leap; the cave spider reuses the daylight gate).
- The hostile-plugin embed integration (wiring vanilla_zombie/skeleton/spider into `vanillaMobNames` + `//go:embed` + updating the bundled-mob count tests) is the one remaining shared step once all three hostile plugin pairs (35-03/35-04/35-05) have landed.
- SC#2 (spider half): the spider acquires + leaps + melees the player at night (not in daylight), maps to MONSTER — delivered. MOB-HOST-03: the jar-faithful Spider.registerGoals plugin with the light-gated aggression differentiator shipped; the wall-climb differentiator is cite-deferred (recorded).

## Self-Check: PASSED

All created/modified files exist on disk (vanilla_spider main.star/plugin.toml + the byte-identical embed pair, spider_test.go, ai_goals_attack.go, plugin_entity.go, this SUMMARY) and all three task commits (6a73700f, 93c91fab, e4055ce0) are in the git log. Verification: `CGO_ENABLED=0 go build ./...` exit 0; `go vet ./server/` clean; `go test ./server/ -run 'TestPluginPigEqualsGoNativePig|TestSpider|TestLeap|TestDaylight'` green (pig oracle byte-identical); `diff` of both vanilla_spider copies EMPTY; the full `go test ./server/` suite passes.

---
*Phase: 35-hostiles*
*Completed: 2026-06-30*
