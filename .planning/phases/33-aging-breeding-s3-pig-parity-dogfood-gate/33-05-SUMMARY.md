---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
plan: 05
subsystem: testing
tags: [dogfood-gate, oracle, pig-parity, race, breed, aging, follow, deviations, live-verify]

# Dependency graph
requires:
  - phase: 33-01
    provides: "breedAge int + isBaby + refreshDimensions (baby half-scale AABB) + tickMobAging + onGrewUp (cross-0 restore + DATA_BABY_ID broadcast)"
  - phase: 33-02
    provides: "inLove int + setInLove/canFallInLove + the FEED path (tryFeedAnimal) + broadcastHearts (EntityEvent-18)"
  - phase: 33-03
    provides: "Go-native breedGoal@3 + followParentGoal@5 + TickLoop.breed (variant nextBoolean() then XP 1+nextInt(7)) + findFreePartner/findNearestAdultParent + isPanicking + canMate"
  - phase: 33-04
    provides: "both .star pigs at 9 goals (lockstep) + 5 host handles + the 9v9 oracle TestPluginPigEqualsGoNativePig byte-identical green + the breed/follow scenario tests"
provides:
  - "TestDogfoodGateOracleNineGoals — the FORMAL MOB-GATE-02 gate: boot-load-9 (both pigs len==9 {0,1,3,4,4,5,6,7,8}) + the byte-identical 500-tick oracle, wrapped with the len==9 up-front assertion (a regression below 9 fails HERE)"
  - "TestDogfoodGateEndToEnd — the observable dogfood composed: two in-love adults -> live breedGoal spawns a HALF-SCALE baby (AABB 0.45) + 6000 cooldown + inLove reset + XP orb -> the baby's live followParentGoal paths to a nearby adult (NO RNG) -> the baby ages to 0 and grows to full size (AABB 0.9 restored + DATA_BABY_ID cross-0 broadcast)"
  - "33-deviations.md — the finalized v5 deviation register (same-region cut + baby eye-height defer + variant-assign defer + isPanicking/eat-sound/heart-wire faithful + Age/InLove NBT persist defer)"
  - "Docker -race over ./server/ ./data/tag/ ./level/loot/ verified clean with all 9 goals + aging + breeding live"
affects: [34-new-passive-mobs, 35-hostiles-spawn-rules]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the dogfood gate is a thin ORCHESTRATION over the Plan 33-01..04 unit harnesses (reuses spawnVanillaPig/newDamageMob/the breed/follow/aging helpers) — it asserts the COMPOSED feed->breed->age->follow->grow behavior, never re-implements the subsystem logic"
    - "boot-load-9 + byte-identical in ONE test: assert len(goals)==9 up front (a drop below 9 fails the gate, not silently) THEN run the 500-tick byte-identical proof"
    - "the deviation register is the CLAUDE.md never-silent contract realized: every cut/defer/correction has a vanilla cite + a v5 delta + a path to full fidelity"

key-files:
  created:
    - server/dogfood_gate_test.go
    - .planning/phases/33-aging-breeding-s3-pig-parity-dogfood-gate/33-deviations.md
  modified: []

key-decisions:
  - "the E2E follow step (c) moves BOTH parents into the 3..16 follow band (initiator 5 blocks, partner 8 blocks) so the NEAREST adult qualifies — a partner left 1 block from the baby would be vetoed by DONT_FOLLOW_IF_CLOSER_THAN (distSqr<9), which is correct vanilla behavior, not a code bug"
  - "the E2E feed step (a) composes from setInLove() (the feed path's effect) — the feed->inLove wiring itself is unit-tested in inlove_feed_test.go; this gate proves the BREEDING/AGING/FOLLOW composition, not the feed mechanics"
  - "TestPerfGate (the Phase-28/30.1 wall-clock perf gate) is a PRE-EXISTING flaky timing gate, NOT a regression from this plan — it passes 5/5 in isolation (232-269%) and only spikes over its 350% cap (~352-356%) when the full suite's parallel goroutines starve the plugin-arm measurement. This plan adds a TEST-ONLY file and cannot touch the plugin tick hot path."

patterns-established:
  - "gate-as-orchestration: a phase's hard gate composes the prior plans' unit-tested seams into one end-to-end observable scenario + the formal byte-identical oracle, asserting they COMPOSE without re-testing internals"

requirements-completed: [MOB-GATE-01, MOB-GATE-02]

# Metrics
duration: 35min
completed: 2026-06-30
---

# Phase 33 Plan 05: THE DOGFOOD GATE — 9-goal oracle byte-identical + end-to-end breed/age/follow + the deviation register Summary

**The consolidated dogfood gate (server/dogfood_gate_test.go): TestDogfoodGateOracleNineGoals asserts BOTH pigs boot with exactly 9 goals {0,1,3,4,4,5,6,7,8} then drives the Go-native pig vs the plugin pig byte-identical over 500 ticks (the formal MOB-GATE-02 oracle); TestDogfoodGateEndToEnd composes the observable dogfood — two in-love adults breed a HALF-SCALE baby (AABB 0.45) + 6000 cooldown + inLove reset + XP orb, the baby's live FollowParentGoal paths to a nearby adult (NO RNG), then the baby ages to breedAge==0 and grows to full size (AABB 0.9 restored + DATA_BABY_ID cross-0 broadcast). The finalized 33-deviations.md documents every accepted v5 cut/defer (same-region breeding + baby eye-height + variant-assign + Age/InLove NBT) never silent. CGO=0 build/vet clean; the full named-test regex + the byte-identical oracle GREEN; the .star copies byte-identical; Docker -race over ./server/ ./data/tag/ ./level/loot/ CLEAN. The HEADLESS gate is GREEN — ready for the live bot dogfood (the blocking human-verify checkpoint).**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-30
- **Completed:** 2026-06-30
- **Tasks:** 2 of 3 (Task 3 is the blocking live-verify checkpoint — RETURNED, not auto-passed)
- **Files changed:** 2 (2 created, 0 modified), 490 insertions

## Accomplishments
- **TestDogfoodGateOracleNineGoals** (the FORMAL MOB-GATE-02 gate): asserts BOTH the Go-native pig AND the plugin pig boot with EXACTLY 9 goals {0,1,3,4,4,5,6,7,8} (boot-load-9, with per-priority counts: one each at 0/1/3/5/6/7/8 + two at 4) up front, then runs the SAME byte-identical comparison TestPluginPigEqualsGoNativePig runs (same id => same RNG seed, same world + player, 500 ticks). A regression below 9 goals fails HERE, not silently.
- **TestDogfoodGateEndToEnd** (the observable dogfood in ONE scenario): (a) two adult pigs in love (inLove==600) + canMate; (b) the LIVE breedGoal (canUse->start->tick) courts to threshold and spawns a HALF-SCALE baby (breedAge==-24000, AABB span 0.45 == Pig.BABY_DIMENSIONS, typ==Pig), both parents to breedAge==6000 + inLove==0, an XP orb (value 1..7); (c) the baby's LIVE followParentGoal (EMPTY flags) acquires the nearest adult and paths to it (want target AT the parent, timeToRecalcPath==10, NO RNG); (d) the baby aged across -1->0 grows up (isBaby() false, AABB restored to 0.9, DATA_BABY_ID via onGrewUp).
- **33-deviations.md** finalized — 7 entries, each vanilla-cited with the v5 delta + the path to fidelity: the same-region breeding CUT (the headline, pre-accepted MOB-SUB-09/ROADMAP), the baby eye-height DEFER (0.45 AABB ships, 0.40625 eye-height deferred), the pig variant DEFER (the nextBoolean() draw ships, the assign defers), plus the faithful isPanicking / eat-sound no-op / EntityEvent-18 heart corrections and the Age/InLove NBT persist defer.
- **Docker -race** over ./server/ ./data/tag/ ./level/loot/ — CLEAN (ok, no DATA RACE) with all 9 goals + aging + breeding live; the breed/follow/aging/feed/hitbox concurrent paths carry no new race.
- The headless gate is GREEN: CGO=0 build exit 0 + vet clean; the full named-test regex + the byte-identical oracle pass; the two .star copies byte-identical (diff empty).

## Task Commits

Each task was committed atomically:

1. **Task 1: the consolidated dogfood-gate test suite (9-goal oracle + end-to-end scenario + boot-load-9)** — `d6095046` (test)
2. **Task 2: Docker -race + the deviation doc (same-region cut + baby eye-height + isPanicking)** — `14bf4a6c` (docs)
3. **Task 3: live vanilla-client/bot dogfood verification** — the BLOCKING human-verify checkpoint (RETURNED to the orchestrator, not auto-passed — auto-advance is OFF).

## Files Created/Modified
- `server/dogfood_gate_test.go` (created) — TestDogfoodGateOracleNineGoals (boot-load-9 + the 500-tick byte-identical oracle) + TestDogfoodGateEndToEnd (the feed->breed->age->follow->grow composition). A thin orchestration over the Plan 33-01..04 harnesses (spawnVanillaPig/spawnVanillaPigWithID, newDamageMob, newBreedGoal/newFollowParentGoal, tickMobAging, the AABB span helpers).
- `.planning/phases/33-aging-breeding-s3-pig-parity-dogfood-gate/33-deviations.md` (created) — the finalized v5 deviation register (7 entries, each cited + a fidelity path).

## Decisions Made
- **The E2E follow step moves both parents into the follow band.** After breeding, both parents are adults on cooldown (breedAge>0, isBaby() false → valid follow targets). FollowParentGoal picks the NEAREST adult and vetoes it if it is within 3 blocks (distSqr<9, DONT_FOLLOW_IF_CLOSER_THAN). A partner left 1 block from the bred baby would veto the follow — so step (c) puts the initiator at 5 blocks (distSqr 25) and the partner at 8 blocks (distSqr 64), making the nearest qualifying adult the initiator. This is the correct vanilla scan behavior, encoded into the scenario setup.
- **The feed step composes from setInLove().** setInLove() is exactly what the feed path arms (tryFeedAnimal: adult + pig_food → setInLove); the feed→inLove wiring is unit-tested in inlove_feed_test.go. This gate proves the breeding/aging/follow COMPOSITION downstream of being in love, not the feed mechanics (which are separately covered).
- **TestPerfGate is a pre-existing flaky wall-clock timing gate, not a regression.** See Issues Encountered.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] The fork module import path is `github.com/imhinotori/sulfur`, not `github.com/Tnze/go-mc`**
- **Found during:** Task 1 (the first vet of the new test file).
- **Issue:** The new test file initially imported `github.com/Tnze/go-mc/data/entity` + `.../level` (the upstream paths). The fork's module path is `github.com/imhinotori/sulfur` — vet failed `no required module provides package`.
- **Fix:** Changed the two imports to `github.com/imhinotori/sulfur/data/entity` + `.../level` (the paths the sibling test files use).
- **Files modified:** server/dogfood_gate_test.go
- **Verification:** `CGO_ENABLED=0 go vet ./server/` exit 0.
- **Committed in:** `d6095046` (Task 1 commit)

**2. [Rule 1 - Bug] The E2E follow-step scenario setup vetoed its own follow (a too-close partner)**
- **Found during:** Task 1 (running TestDogfoodGateEndToEnd the first time).
- **Issue:** Step (c) left the partner pig 1 block from the bred baby. FollowParentGoal scans for the NEAREST adult and rejects it if within 3 blocks (distSqr<9); the nearest adult was the 1-block partner, so canUse correctly returned false — the test's setup, not the goal, was wrong.
- **Fix:** Moved BOTH parents into the 3..16 follow band (initiator 5 blocks / partner 8 blocks) so the nearest qualifying adult is in-band. This exercises the real canUse->tick path the step intends.
- **Files modified:** server/dogfood_gate_test.go
- **Verification:** TestDogfoodGateEndToEnd PASS (all of a/b/c/d).
- **Committed in:** `d6095046` (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 — a fork-import correction + a scenario-setup correction). Both in the NEW test file only; no production code touched.
**Impact on plan:** No scope creep — all plan deliverables (the consolidated gate suite, the deviation doc, the -race run) shipped. The two fixes were in the gate test's own setup.

## Issues Encountered

**TestPerfGate (the Phase-28/30.1 wall-clock perf gate) is a pre-existing flaky timing gate — NOT a regression from this plan.**
- **What:** Under the full `go test -count=1 ./server/` suite, TestPerfGate intermittently trips its 350% RELATIVE cap (measured ~352-356% in ~1/3 of full-suite runs). It is a wall-clock microbenchmark asserting the plugin pig's per-(mob·tick) overhead vs the Go-native oracle.
- **Root:** Run IN ISOLATION it passes 5/5 at 232-269% (well under 350%). The spike ONLY occurs when the full suite's parallel goroutines (notably the concurrent panic-isolation test) momentarily starve the plugin-arm wall-clock measurement. The perf_gate_test.go header itself documents this: the absolute ns is "DOMINATED by drainPendingPath's blocking async-A* round-trip latency... run-to-run JITTER is high"; the relative cap is "deliberately LOOSE... set generously above that faithful baseline".
- **Why it is not mine:** This plan adds a TEST-ONLY file (server/dogfood_gate_test.go) and the deviation doc — it does NOT touch the plugin tick hot path (ai_mob.go, the .star, plugin_entity.go, the goal files), so it cannot regress plugin per-(mob·tick) cost. The flake is a scheduling artifact of the wall-clock gate under contention, pre-dating this plan (it is skipped entirely under -race, which is why the Docker -race run is clean + stable).
- **Resolution / ownership:** Out of scope per the SCOPE BOUNDARY (not caused by this plan's changes), logged here so the verifier recognizes it. The FUTURE-WORK note already in perf_gate_test.go (assert the per-stroll Starlark ALLOCATION COUNT — the stable, jitter-immune signal — instead of wall-clock ns) is the real fix; it belongs to a perf-gate hardening task, not the dogfood gate. The CORRECTNESS suite (the 9-goal oracle, breed/age/follow, all prior phases) is GREEN; only the jittery wall-clock perf assertion flakes under parallel contention.

## Live-Verify Checkpoint (Task 3 — BLOCKING, RETURNED)

The headless gate is GREEN; Task 3 is the OBSERVABLE dogfood, a blocking human-verify checkpoint run by the ORCHESTRATOR on a live vanilla 26.2 client / testbot (auto-advance is OFF, so it is NOT auto-passed). The live test must confirm: feed two adult pigs (hearts on feed) → a SMALL baby spawns between them (+ XP orbs, parents stop hearts) → the small baby PATHS toward an adult → the baby GROWS to full size. The exact checklist is in the checkpoint return.

## Next Phase Readiness
- The HEADLESS dogfood gate (MOB-GATE-02) is GREEN: the 9-goal oracle byte-identical over 500 ticks, the end-to-end breed/age/follow/grow scenario, the .star byte-identical, Docker -race clean. The deviation register is finalized.
- BLOCKED on the live-verify checkpoint: Phase 34 (new passive mobs) may begin ONLY after the orchestrator's live bot pass confirms the observable dogfood + the user approves.
- No code blockers. The one intermittent (TestPerfGate wall-clock flake) is pre-existing, isolated to the parallel-contention timing assertion, and untouched by this plan.

## Self-Check: PASSED

Both created files exist on disk (server/dogfood_gate_test.go, 33-deviations.md) + the SUMMARY itself; both task commits (`d6095046`, `14bf4a6c`) are present in the git log. CGO=0 build/vet clean, the full named-test regex + the byte-identical 9-goal oracle GREEN, the two .star copies byte-identical, Docker -race over ./server/ ./data/tag/ ./level/loot/ CLEAN. The one intermittent (TestPerfGate wall-clock flake) is pre-existing, pre-dates this plan, and is isolated to the parallel-contention timing assertion (passes 5/5 in isolation; skipped under -race).

---
*Phase: 33-aging-breeding-s3-pig-parity-dogfood-gate*
*Completed: 2026-06-30*
