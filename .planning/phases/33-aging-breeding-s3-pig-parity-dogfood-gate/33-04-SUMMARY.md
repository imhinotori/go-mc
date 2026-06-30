---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
plan: 04
subsystem: mob-ai
tags: [breeding, lockstep, starlark, host-handles, goals, pig-parity, rng-lockstep]

# Dependency graph
requires:
  - phase: 33-03
    provides: "Go-native breedGoal@3 + followParentGoal@5 + TickLoop.breed (the ONE host breed: variant nextBoolean() FIRST then XP 1+nextInt(7), both on the initiator's per-mob RNG) + getFreePartner/nearestAdultSameClass scans + isPanicking + canMate"
  - phase: 33-02
    provides: "inLove int + isInLove/setInLove + the FEED path arming inLove + broadcastHearts"
  - phase: 33-01
    provides: "breedAge int + isBaby + refreshDimensions (baby half-scale) + broadcastBabyFlag"
  - phase: 32-01
    provides: "the TemptGoal .star block + nearestPlayerHoldingPigFood host-scan pattern (the copy template)"
provides:
  - "5 host handles on entityHandle: is_in_love/is_baby/breed_age (read accessors) + nearest_breeding_partner/nearest_adult_parent (cap-gated tuple-or-None scans) + try_breed (the host breed op)"
  - "findFreePartner / findNearestAdultParent — standalone same-class scans shared by the Go-native goals AND the host handles (one selection logic → lockstep)"
  - "BreedGoal@3 {MOVE,LOOK} + FollowParentGoal@5 EMPTY-flags declared + callbacks in BOTH byte-identical vanilla_pig/main.star copies"
  - "the .star breed routes through ONE host try_breed (= TickLoop.breed) — the .star draws NO breed RNG itself"
  - "the plugin pig now has all 9 goals {0,1,3,4,4,5,6,7,8} matching the Go-native pig"
  - "the 9v9 oracle TestPluginPigEqualsGoNativePig byte-identical GREEN (the gate this plan closes)"
affects: [33-05-dogfood-gate, 34-new-passive-mobs]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "host-owns-the-scan-AND-the-RNG: the .star breed/follow callbacks read host scalars + position tuples, and route the breed through ONE host breed() — both the Go pig and the plugin pig call IDENTICAL findFreePartner + TickLoop.breed, so the variant+XP draws fire host-side in the same order on the same per-mob RNG (the lockstep)"
    - "READ accessor vs bound method in the .star: is_in_love/is_baby are Attr reads (NO parens); nearest_breeding_partner/try_breed are bound methods (parens). Calling a read as a function is a load-bearing bug the oracle masks but the live drive catches."
    - "EMPTY-flags goal via the .star decl: flags = [] parses to goalFlag 0 (FollowParentGoal claims no control flag); the selector never blocks it"

key-files:
  created: []
  modified:
    - server/plugin_entity.go
    - server/ai_goals_breed.go
    - server/ai_goals_follow.go
    - plugins/vanilla_pig/main.star
    - server/assets/vanilla_pig/main.star
    - server/plugin_pig_test.go
    - server/breed_follow_test.go

key-decisions:
  - "try_breed is THE lockstep seam: the .star breed_tick signals readiness; the host re-finds the partner (findFreePartner) and calls t.breed(e, partner) — the EXACT TickLoop.breed the Go-native breedGoal.tick calls. The .star NEVER draws breed RNG, so the variant nextBoolean()+XP nextInt(7) order is guaranteed identical to the Go pig."
  - "the same-class scans were factored OUT of the goal methods (findFreePartner, findNearestAdultParent) so the Go pig and the plugin pig call ONE selection implementation — not two parallel copies that could drift"
  - "is_in_love/is_baby/breed_age are READ accessors (Attr-returned scalars), not bound methods — the .star reads them without parens; nearest_breeding_partner/nearest_adult_parent/try_breed are bound methods (with parens)"
  - "breed_continue / follow_continue re-scan via the host (nearest_breeding_partner != None / nearest_adult_parent != None) as the observable equivalent of partner.isInLove()+!isPanicking() / parent.isAlive()+in-band — the host owns the filter, the .star only sees tuple-or-None (mirroring Tempt's re-scan continue)"
  - "nearest_adult_parent takes a range arg for symmetry only — the FollowParentGoal scan box is the host's fixed inflate(8,4,8)"

patterns-established:
  - "host-owns-the-scan-AND-the-RNG: route any RNG-drawing plugin op (breed) through ONE host method that BOTH the Go-native goal and the plugin goal call, so the draws + their order are byte-identical by construction"
  - ".star READ accessor vs bound method discipline: an Attr-returned scalar is read WITHOUT parens; a bound *Builtin is called WITH parens — verify against the live drive, not just the deterministic oracle (which can mask an erroring-but-dormant callback)"

requirements-completed: [MOB-SUB-09, MOB-GATE-01]

# Metrics
duration: 13min
completed: 2026-06-30
---

# Phase 33 Plan 04: The .star Lockstep — BreedGoal@3 + FollowParentGoal@5 Mirrored Onto Both Plugin Pigs (9v9 Oracle Closed) Summary

**The plugin pig's last two goals ported in lockstep: 5 host handles (is_in_love/is_baby/breed_age reads + nearest_breeding_partner/nearest_adult_parent scans + the try_breed host breed op) let BOTH byte-identical vanilla_pig/main.star copies declare BreedGoal@3 {MOVE,LOOK} + FollowParentGoal@5 EMPTY-flags; the .star breed routes through ONE host try_breed (= TickLoop.breed) so the variant nextBoolean()+XP nextInt(7) draws are host-side in the jar's exact order. The plugin pig now has all 9 goals {0,1,3,4,4,5,6,7,8} and TestPluginPigEqualsGoNativePig is byte-identical GREEN — the 9v9 oracle gate this plan closes.**

## Performance

- **Duration:** ~13 min
- **Started:** 2026-06-30T08:38:28Z
- **Completed:** 2026-06-30T08:51:16Z
- **Tasks:** 3
- **Files modified:** 7 (0 created, 7 modified), ~633 insertions

## Accomplishments
- **5 host handles** on `entityHandle` (`plugin_entity.go`): 3 READ accessors (`is_in_love` = Animal.isInLove, `is_baby` = AgeableMob.isBaby, `breed_age` = AgeableMob.getAge) re-resolved via `h.store()`, and 2 cap-gated host scans (`nearest_breeding_partner`, `nearest_adult_parent`) returning a position tuple or `None` — the same-class + canMate + isPanicking + adult filters stay HOST-side (mirroring `nearestPlayerHoldingPigFood`).
- **`try_breed`** — THE lockstep host breed op: the `.star` `breed_tick` calls it at the threshold; the host re-finds the partner (`findFreePartner`) and routes through `t.breed(e, partner)` — the EXACT `TickLoop.breed` the Go-native `breedGoal.tick` calls (variant `nextBoolean()` FIRST then XP `1+nextInt(7)` SECOND, on the initiator's per-mob RNG). The `.star` draws NO breed RNG itself.
- **`findFreePartner` / `findNearestAdultParent`** — factored the same-class scans OUT of the Go-native goal methods so the Go pig and the plugin pig call ONE selection implementation (no parallel-copy drift).
- **BreedGoal@3 {MOVE,LOOK} + FollowParentGoal@5 EMPTY-flags + callbacks** declared in BOTH `vanilla_pig/main.star` copies IDENTICALLY (`diff` EMPTY). `breed_can_use` gates on `is_in_love` (dormant on the un-fed oracle); `follow_can_use` gates on `is_baby` (dormant on the lone-adult oracle). `breed_tick` courts (look + move + `++loveTime`) and at `loveTime>=60` within 3 blocks calls `try_breed`; `follow_tick` re-paths to the nearest adult every 10 ticks (NO RNG).
- **The 3 plugin-side goal-count tests flipped 7→9** (`TestPluginPigBootLoads`, `TestVanillaPigDeclaresGoalSet` with @3 {MOVE,LOOK} + @5 EMPTY byPriority checks, `TestVanillaPigGoalsPorted` with @3/@5 not-requires-update-every-tick).
- **2 scenario tests** (`breed_follow_test.go`): `TestBreedSpawnsBaby` (two in-love pigs → breedGoal canUse→tick → a baby + cooldown + inLove reset + XP orb) and `TestFollowParentPathsToAdult` (a baby's want target points at the nearest adult, re-pathing every 10 ticks).
- **THE 9v9 ORACLE `TestPluginPigEqualsGoNativePig` is byte-identical GREEN** — both pigs at 9 goals, breed/follow dormant on the un-fed lone-adult oracle. The gate this plan closes.

## Task Commits

Each task was committed atomically:

1. **Task 1: The 5 host handles (3 read accessors + 2 host scans + try_breed) + the factored-out scans** — `b7ff07c7` (feat)
2. **Task 2: Declare BreedGoal@3 + FollowParentGoal@5 + callbacks in BOTH byte-identical .star copies** — `505abeda` (feat)
3. **Task 3: Flip the 3 goal-count tests 7→9 + breed/follow scenario tests + close the 9v9 oracle (incl. the Rule-1 call-vs-read fix)** — `02f57564` (feat)

## Files Created/Modified
- `server/plugin_entity.go` — the 5 host handles (3 read accessor cases in `Attr` + the read names in `AttrNames`; the 3 method cases + `nearestBreedingPartner`/`nearestAdultParent`/`tryBreed` impls + their `AttrNames` entries).
- `server/ai_goals_breed.go` — `findFreePartner(t, e, rng)` factored out of `breedGoal.getFreePartner` (now a thin wrapper) so the host handle calls the same scan.
- `server/ai_goals_follow.go` — `findNearestAdultParent(t, e)` factored out of `followParentGoal.canUse` so the host handle calls the same scan.
- `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` — byte-identical: the header comment flipped @3/@5 DEFERRED→PORTED, 5 new constants, the breed_/follow_ callback blocks, and the @3/@5 `goal(...)` decl entries.
- `server/plugin_pig_test.go` — `TestPluginPigBootLoads` + `TestVanillaPigDeclaresGoalSet` 7→9 with @3/@5 checks; `TestVanillaPigGoalsPorted` @3/@5 not-requires-update-every-tick.
- `server/breed_follow_test.go` — `TestBreedSpawnsBaby` + `TestFollowParentPathsToAdult` scenario tests.

## Decisions Made
- **`try_breed` is the single lockstep seam.** The `.star` `breed_tick` never re-implements the breed draws — it signals readiness and the host re-finds the partner and calls `t.breed(e, partner)`. Because both halves call the IDENTICAL `TickLoop.breed`, the variant `nextBoolean()` + XP `1+nextInt(7)` order/source is guaranteed identical (the make-or-break lockstep).
- **One scan implementation, not two.** Factoring `findFreePartner`/`findNearestAdultParent` out of the goal methods means the Go pig and the plugin pig select the SAME partner/parent — eliminating the risk of two parallel copies drifting.
- **READ accessor vs bound method.** `is_in_love`/`is_baby`/`breed_age` are Attr-returned scalars (read WITHOUT parens); `nearest_breeding_partner`/`nearest_adult_parent`/`try_breed` are bound `*Builtin` methods (called WITH parens). The Task-3 Rule-1 fix corrected an initial parens slip.
- **`*_continue` re-scans via the host** as the observable equivalent of the partner/parent liveness+in-love+in-band checks (the filter stays Go-side; the `.star` only ever sees tuple-or-None), mirroring how Tempt's `continue` re-scans.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `entity.is_in_love()`/`is_baby()` called as functions in the .star (they are READ accessors, not bound methods)**
- **Found during:** Task 3 (running the full `./server/` suite after the goal-count + scenario edits).
- **Issue:** The Task-2 `.star` callbacks called the new state reads as functions — `entity.is_in_love()` / `entity.is_baby()`. But those are `entityHandle.Attr` READ accessors (they return a `starlark.Bool` directly, like `entity.in_water`), NOT bound `*Builtin` methods (like `entity.rand_float()`). Calling a `bool` value as a function raised `invalid call of non-function (bool)` on EVERY `breed_can_use`/`follow_can_use`/`follow_continue` tick in the LIVE drive (`TestPerfGate` logged it once per tick per pig). The deterministic 9v9 oracle MASKED it: the erroring callback still ran dormant (no want set) on the un-fed lone-adult oracle, so the observable stream stayed byte-identical by coincidence — the bug only surfaced when a real spawned pig drove the gate.
- **Fix:** Changed the 3 occurrences to accessor reads (no parens): `if not entity.is_in_love:` / `if not entity.is_baby:` — in BOTH byte-identical `.star` copies.
- **Files modified:** plugins/vanilla_pig/main.star, server/assets/vanilla_pig/main.star
- **Verification:** `TestPerfGate` now logs ZERO callback errors; the 9v9 oracle stays byte-identical GREEN; the files stay byte-identical (`diff` EMPTY); full `./server/` suite + Docker `-race` clean.
- **Committed in:** `02f57564` (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — a real call-vs-read bug the live drive caught and the oracle masked).
**Impact on plan:** The fix was essential for correctness (the gate would error every tick on any real pig). No scope creep — all plan deliverables shipped. The deviation reinforced a pattern: verify `.star` accessor-vs-method usage against the LIVE drive, not only the deterministic oracle.

## Issues Encountered
- The CRLF warnings on commit are the repo's standard line-ending normalization (not errors).

## Oracle State (THE GATE — EXPLICIT)

**`TestPluginPigEqualsGoNativePig` (the 9v9 oracle) is byte-identical GREEN.** Both the Go-native pig (33-03, 9 goals) AND the plugin pig (this plan, 9 goals) now have `{0,1,3,4,4,5,6,7,8}`. On the un-fed lone-adult oracle: `breed_can_use` → `not is_in_love` → false (inLove=0); `follow_can_use` → `not is_baby` → false (adult). Both goals dormant → zero draws → the observable stream (wantTarget, yaw, headYaw, position over 500 ticks) is byte-identical. The breed RNG (variant `nextBoolean()` then XP `1+nextInt(7)`) fires ONLY inside `try_breed`/`t.breed`, reached only when the goal courts to threshold — never on the oracle. This is the full 9v9 lockstep closure; 33-05 formally runs the dogfood gate (live breeding + `-race` + the bot test).

## Known Stubs
- **Pig variant assignment (cite-deferred, inherited from 33-03, NOT baked away):** `t.breed()` consumes the variant `nextBoolean()` draw for RNG lockstep but does not assign `child.variant` (Entity has no variant field yet). The draw + its order are the observable contract; the assignment slots in at the cited seam when a variant field lands. The plugin pig routes through the SAME `t.breed`, so it inherits the same cite-deferred behavior — no NEW stub introduced by this plan.

## Final Gate Results
- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet ./server/` — clean.
- `diff plugins/vanilla_pig/main.star server/assets/vanilla_pig/main.star` — EMPTY (byte-identical).
- The 3 plugin-side goal-count tests (9 goals) + `TestPigGoalSetRegistered` (33-03) — all GREEN.
- `TestBreedSpawnsBaby` + `TestFollowParentPathsToAdult` scenario tests — GREEN.
- **`TestPluginPigEqualsGoNativePig` (the 9v9 oracle) — byte-identical GREEN.**
- The full plan verification set (`TestBreed|TestFollowParent|TestTempt|TestPanic|TestFloat|TestPigStrolls|TestAging|TestInLove|TestFeed|...`) — GREEN.
- Full `./server/` suite — GREEN (after the Rule-1 fix).
- Docker `-race ./server/` — clean, NO DATA RACE (13.6s).

## Next Phase Readiness
- The plugin pig is at 9 goals byte-identical with the Go-native pig. The full 9v9 byte-identical lockstep is CLOSED here; 33-05 formally runs the dogfood gate (the live breeding scenario on a real client + the bot test + the documented same-region cut roll-up into 33-deviations.md).
- No blockers. The oracle is green throughout (breed/follow dormant on the un-fed adult).

## Self-Check: PASSED

All 7 modified source files exist on disk; all 3 task commits (`b7ff07c7`, `505abeda`, `02f57564`) are present in the git log. CGO=0 build/vet clean, both .star copies byte-identical, the 9v9 oracle byte-identical GREEN, the 4 goal-count tests + 2 scenario tests green, Docker -race clean.

---
*Phase: 33-aging-breeding-s3-pig-parity-dogfood-gate*
*Completed: 2026-06-30*
