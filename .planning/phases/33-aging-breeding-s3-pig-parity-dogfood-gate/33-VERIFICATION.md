---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
verified: 2026-06-30T00:00:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: none
  previous_score: n/a
gaps: []
deferred:
  - truth: "Cross-region (barrier-resolved) full-fidelity breeding"
    addressed_in: "Backlog (post-gate)"
    evidence: "MOB-SUB-09 pre-accepts the same-region cut; ROADMAP Deferred/Backlog v5-deferred: 'Barrier-resolved full-fidelity cross-region breeding'. Documented in 33-deviations.md #1 (CUT, pre-accepted)."
  - truth: "Baby eye-height 0.40625 (the half-scale AABB ships)"
    addressed_in: "Future (when Entity.eyeHeight field lands)"
    evidence: "33-deviations.md #2 (DEFER, pre-accepted). Not goal-load-bearing — every breed/follow distSqr gate reads the AABB, which IS scaled to 0.45. ROADMAP SC names the half-scale hitbox, which ships."
  - truth: "Age/InLove NBT persist across save/load"
    addressed_in: "Future (a persistence plan)"
    evidence: "33-deviations.md #7 (DEFER). Not gate-load-bearing — the oracle is in-memory; the live bot dogfood spawns fresh pigs without restart."
  - truth: "PigVariant assignment on the bred child (the nextBoolean() DRAW ships, consumed in jar order)"
    addressed_in: "Future (when a PigVariant field lands)"
    evidence: "33-deviations.md #4 (DEFER, draw preserved). The RNG draw + order ARE shipped so the oracle stays byte-identical; only the cosmetic assignment is deferred."
---

# Phase 33: Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE Verification Report

**Phase Goal:** Animals age and breed, and the pig's last two deferred goals land — closing full pig parity and PROVING S1–S4 are 1:1 before any new mob reuses them. This is the HARD GATE.
**Verified:** 2026-06-30
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

Every gate was RUN, not trusted from SUMMARY. All green.

### Observable Truths (ROADMAP Success Criteria — the contract)

| # | Truth (SC) | Status | Evidence |
|---|-----------|--------|----------|
| 1 | Signed-int `breedAge`/age machine (-24000 baby up; +6000 cooldown), RNG-free `tickMobAging`, `isBaby`, baby half-scale hitbox — never an `isBaby bool` | ✓ VERIFIED | `server/entity.go:269` `breedAge int` (distinct from the item `age int` at :119). `tickMobAging` (combat_mob.go:677) is pure-int: `breedAge<0 → ++`, `>0 → --`; the only RNG (3× nextGaussian heart velocity) is `inLove>0`-gated → dormant on the oracle. `babyDimensionScale = 0.5` (entity.go:417), AABB 0.9→0.45, derive-from-adult. TestAging*/TestBabyHitboxHalfScale/TestGrowUpRestoresHitbox all PASS. |
| 2 | Two fed pigs same-region breed: inLove/loveCause by feeding, same-region partner search, canMate, baby spawn (region routing + per-id reseed) + finalizeSpawnChildFromBreeding (XP 1+nextInt(7) + cooldown); cross-region ships the same-region cut | ✓ VERIFIED | FEED path `tryFeedAnimal` (attack_dispatch.go:801) sets inLove via `setInLove`. `findFreePartner`/`findNearestAdultParent` use owning-region `t.cur().entities.near` (the documented cut). `breed()` (ai_goals_breed.go:227): variant `nextBoolean()` DRAW 1 (:239), child `breedAge=-24000` (:245), both parents `breedAge=6000` (:251-252), XP `1+nextInt(7)` DRAW 2 (:259). TestBreed/TestBreedSpawnsBaby/TestFeed* PASS. |
| 3 | BreedGoal@3 + FollowParentGoal@5 wired (oracle + plugin lockstep), completing all 5 deferred goals; baby follows parent | ✓ VERIFIED | `newPigAI` registers @3 breed {MOVE,LOOK} + @5 follow EMPTY flags. TestPigGoalSetRegistered asserts exactly 9 goals with the full priority multiset {0,1,3,4,4,5,6,7,8} and per-goal types/flags. `diff plugins/vanilla_pig/main.star server/assets/vanilla_pig/main.star` is EMPTY (byte-identical lockstep). TestFollowParent/TestFollowParentPathsToAdult PASS. |
| 4 | **THE GATE**: full 9-goal oracle `TestPluginPigEqualsGoNativePig` byte-identical over 500 ticks with all goals live, AND two fed pigs breed + baby follows | ✓ VERIFIED | `TestPluginPigEqualsGoNativePig` PASS. `TestDogfoodGateOracleNineGoals` (500-tick byte-identical Go-vs-plugin, both asserted 9 goals) PASS. `TestDogfoodGateEndToEnd` (live breedGoal→0.45 baby→XP orb 1..7→cooldown 6000→inLove reset→followParent→grow-up restore) PASS. |
| 5 | Standing: jar-verified, CGO=0 + no new deps, Docker -race + strictRegion clean | ✓ VERIFIED | `CGO_ENABLED=0 go build ./...` exit 0; `go vet ./...` exit 0. Full regression `go test ./server/ ./data/tag/ ./level/loot/ -count=1` PASS (8.069s, incl. TestPerfGate — no flake). Docker `go test -race ./server/` PASS (12.257s, no race report). Code cites javap throughout (`[VERIFIED javap ...]`). |

**Score:** 5/5 truths verified

### Deferred Items

| # | Item | Addressed In | Evidence |
|---|------|-------------|----------|
| 1 | Cross-region barrier-resolved breeding | Backlog (post-gate) | MOB-SUB-09 pre-accepts the same-region cut; 33-deviations.md #1 |
| 2 | Baby eye-height 0.40625 (AABB ships) | When Entity.eyeHeight lands | 33-deviations.md #2; not goal-load-bearing (gates read the AABB) |
| 3 | Age/InLove NBT persist | A persistence plan | 33-deviations.md #7; not gate-load-bearing |
| 4 | PigVariant assignment (DRAW ships in jar order) | When PigVariant field lands | 33-deviations.md #4; RNG draw + order preserved → oracle unaffected |

All four are CITED in `33-deviations.md`, not silent. None reduce the observable breeding/aging/follow dogfood the gate proves.

### The Gates (RUN, per verification context)

| # | Gate | Command | Result |
|---|------|---------|--------|
| 1 | Dogfood gate | `CGO=0 go test ./server/ -run 'TestDogfoodGate|TestPluginPigEqualsGoNativePig' -v` | ✓ 3/3 PASS (OracleNineGoals, EndToEnd, PluginPigEqualsGoNativePig) |
| 2 | Aging/feed/breed/follow/hitbox + 4 goal-count tests | `CGO=0 go test ./server/ -run 'TestBreed|...|TestVanillaPigGoalsPorted' -v` | ✓ ALL PASS; 4 goal-count tests (TestPigGoalSetRegistered, TestPluginPigBootLoads, TestVanillaPigDeclaresGoalSet, TestVanillaPigGoalsPorted) assert 9 goals |
| 3 | No regressions | `CGO=0 go test ./server/ ./data/tag/ ./level/loot/ -count=1` | ✓ PASS (8.069s) — TestPerfGate did NOT flake this run |
| 4 | Build + vet clean (CGO=0) | `go build ./...` / `go vet ./...` | ✓ exit 0 / exit 0 |
| 5 | .star byte-identical | `diff plugins/vanilla_pig/main.star server/assets/vanilla_pig/main.star` | ✓ EMPTY (byte-identical) |
| 6 | -race | Docker `golang:1.26 go test -race -timeout 900s ./server/` | ✓ PASS (12.257s, no race report) |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `server/entity.go` | breedAge + inLove + isBaby + canMate + dimension toggle | ✓ VERIFIED | breedAge:269, babyDimensionScale:417, refreshDimensions:425, canMate present |
| `server/combat_mob.go` | tickMobAging (pure-int + cross-0 + inLove tail) | ✓ VERIFIED | :677, RNG-free aging branch, inLove gated, onGrewUp + emitInLoveHearts |
| `server/entity_encode.go` | babyDataEntry + encodeLevelParticles | ✓ VERIFIED | babyDataEntry:411, encodeLevelParticles:834 (the closed gap) |
| `server/attack_dispatch.go` | FEED path (handleInteract + tryFeedAnimal) | ✓ VERIFIED | handleInteract:725, tryFeedAnimal:801, pig_food → adult setInLove / baby ageUp |
| `server/ai_goals_breed.go` | breedGoal + adjustedTickDelay + isPanicking + breed() | ✓ VERIFIED | adjustedTickDelay(n)=n :41 (NOT ceil/2); isPanicking:52 faithful running-PanicGoal read + nil guard |
| `server/ai_goals_follow.go` | followParentGoal (owning-region scan, NO RNG) | ✓ VERIFIED | findNearestAdultParent uses t.cur().entities.near |
| `server/dogfood_gate_test.go` | the gate suite | ✓ VERIFIED | TestDogfoodGateOracleNineGoals + TestDogfoodGateEndToEnd, substantive assertions |
| `plugins/...` + `server/assets/...` main.star | 9-goal byte-identical twins | ✓ VERIFIED | diff empty |
| `33-deviations.md` | cited cuts | ✓ VERIFIED | 7 entries; cuts #1/#2/#4/#7, faithful #3/#5/#6 |

### Key Link Verification

| From | To | Via | Status |
|------|----|----|--------|
| tick_phases.go per-mob loop | tickMobAging | called at :332 OUTSIDE serverAiStep (twin of tickMobIFrames) | ✓ WIRED |
| ai_mob.go newPigAI | newBreedGoal/newFollowParentGoal | addGoal(3,...)/addGoal(5,...) | ✓ WIRED |
| breed() | spawnVanillaPig + XP orb + parent cooldown | child breedAge=-24000 + refreshDimensions + 1+nextInt(7) | ✓ WIRED |
| plugins/vanilla_pig | server/assets/vanilla_pig | byte-identical (diff empty) | ✓ WIRED |
| attack_dispatch handleInteract | inLove/breedAge | pig_food → setInLove / ageUp | ✓ WIRED |

### Faithful-Scope Spot-Checks (per verification context line 7)

| Claim | Status | Evidence |
|-------|--------|----------|
| breedAge separate from existing item `age` | ✓ | entity.go:269 vs :119 (item ticks-since-spawn) |
| aging/inLove tick PURE INT outside serverAiStep | ✓ | tickMobAging in tick_phases per-mob loop :332; pure-int branch; heart draws inLove-gated |
| baby hitbox 0.45 (half) + restore on grow-up + DATA_BABY_ID cross-0 broadcast | ✓ | babyDimensionScale 0.5; onGrewUp restores + broadcasts babyDataEntry(false) |
| FEED path (adult→love, baby→ageUp) | ✓ | tryFeedAnimal:801 |
| BreedGoal@3(1.0) {MOVE,LOOK} + FollowParentGoal@5(1.1) EMPTY flags | ✓ | TestPigGoalSetRegistered asserts flags exactly |
| breed() variant nextBoolean() THEN XP nextInt(7) | ✓ | DRAW 1 :239, DRAW 2 :259 (jar order) |
| isPanicking = faithful running-PanicGoal read (NOT hurtTime) | ✓ | ai_goals_breed.go:52 scans for *panicGoal.running |
| adjustedTickDelay(n)=n (NOT reducedTickDelay) | ✓ | ai_goals_breed.go:41 `return n` |

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
|-------------|-------------|--------|----------|
| MOB-SUB-08 (aging, half-scale hitbox) | 33-01 | ✓ SATISFIED | breedAge machine + tickMobAging RNG-free + 0.45 hitbox; aging tests pass |
| MOB-SUB-09 (breeding, same-region cut) | 33-02/03/04 | ✓ SATISFIED | feed→inLove, same-region scan, canMate, breed() + XP/cooldown; cut documented |
| MOB-GATE-01 (5 deferred goals lockstep) | 33-03/04 | ✓ SATISFIED | BreedGoal@3 + FollowParentGoal@5 on both pigs; 9-goal count + byte-identical .star |
| MOB-GATE-02 (full 9-goal oracle + breed dogfood) | 33-05 | ✓ SATISFIED | TestPluginPigEqualsGoNativePig + TestDogfoodGate* green |

No orphaned requirements. All four phase requirements satisfied.

### Anti-Patterns Found

None gate-blocking. The deferred items (PigVariant assignment, eye-height, NBT persist) are NOT stubs that flow to user-visible output that should be populated — they are explicitly cite-deferred, documented in 33-deviations.md, and the load-bearing values (the RNG draws, the AABB) ARE shipped. The `_ = inheritFromInitiator` at ai_goals_breed.go:240 consumes the draw (preserving stream order) by design, not as a dead stub.

### Human Verification Required

None outstanding. The verification context notes a LIVE bot test already confirmed end-to-end on a running server that feeding two pigs a carrot spawns a baby pig (near-pig count +1, confirmed 3×) — the blocking dogfood checkpoint PASSED. All headless gates were also independently RUN here and are green.

### Gaps Summary

No gaps. All 5 ROADMAP success criteria are VERIFIED against the codebase, all 6 gates (dogfood, behavioral+goal-count, regression, build/vet, .star diff, -race) were RUN and are green, all 4 requirements (MOB-SUB-08/09, MOB-GATE-01/02) are satisfied, and the faithful-scope claims (breedAge distinct, pure-int aging outside serverAiStep, 0.45 hitbox + grow-up restore, FEED path, goal priorities/flags, breed RNG order, faithful isPanicking, adjustedTickDelay identity) all check out against the actual code. The four fidelity deferrals (same-region cut, eye-height, variant assignment, NBT persist) are pre-accepted and/or CITED in 33-deviations.md, are not load-bearing for the gate, and the load-bearing values they touch (RNG draws, AABB) are shipped. The HARD GATE is closed — S1–S4 are proven 1:1.

---

_Verified: 2026-06-30_
_Verifier: Claude (gsd-verifier)_
