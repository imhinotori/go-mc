---
phase: 31-panicgoal-s2-consumer
verified: 2026-06-30T00:00:00Z
status: passed
score: 6/6 must-haves verified
overrides_applied: 0
---

# Phase 31: PanicGoal (S2 consumer) Verification Report

**Phase Goal:** A hurt pig flees — PanicGoal@1 (speed 1.25) reads the real `lastDamageSource` + `is(panic_causes)`, then flees via `DefaultRandomPos.getPos(5,4)`. Jar-faithful; the pig oracle stays byte-identical.
**Verified:** 2026-06-30
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | A pig taking panic-causing damage (player_attack) acquires a flee target / navigates | ✓ VERIFIED | `TestPanicGoalFleesOnPanicDamage` PASS — drives full tick loop, pig acquires `hasTarget` after a `damageSourcePlayerAttack(77)` hit |
| 2 | A pig taking NON-panic damage (minecraft:fall, id 10) does NOT panic — canUse false | ✓ VERIFIED | `TestPanicGoalIgnoresNonPanicDamage` PASS — `panicGoal.canUse` false for `damageSourceOf(damageTypeFall)`; fall (id 10) confirmed absent from `panic_causes` table (tags.go:152) |
| 3 | shouldPanic = `hasLastDamage && lastDamageSource.is("panic_causes")` — genuine tag read; typeTag==0 is a real member (not the sentinel) | ✓ VERIFIED | `ai_goals_panic.go:91-93`; `is()` reads `tag.DamageTypeTags[name][typeTag]` (damage_source.go:78-80); panic_causes table has `0:true` (tags.go:152), confirming bool sentinel needed; `hasLastDamage bool` field exists (entity.go:219) |
| 4 | canUse draws ZERO RNG when shouldPanic is false (first-line short-circuit) → oracle byte-identical | ✓ VERIFIED | `canUse` returns on line 1 before any `mobRandom`/draw (ai_goals_panic.go:124-128); `TestPluginPigEqualsGoNativePig` PASS (dry pig never panics → byte-identical with PanicGoal@1 on both halves) |
| 5 | Go-native pig + BOTH byte-identical .star copies declare PanicGoal@1 (speed 1.25, MOVE) in lockstep, same 30-draw 5/4 path | ✓ VERIFIED | `ai_mob.go:254` `addGoal(1, newPanicGoal(panicSpeedModifier))`; `diff plugins/vanilla_pig/main.star server/assets/vanilla_pig/main.star` EMPTY; plugin `panic_can_use` draws x/y/z @ PANIC_H=5/PANIC_V=4, landMode 0.0 (main.star:89-104) matching Go `findRandomPosition` (ai_goals_panic.go:153-166) |
| 6 | The pig now has 5 goals at {0,1,6,7,8} (was 4 at {0,6,7,8}) | ✓ VERIFIED | `TestPluginPigBootLoads` (len==5, slice {0,1,6,7,8}), `TestVanillaPigDeclaresGoalSet` (len==5, @1 flagMove), `TestPigGoalSetRegistered` (len==5, @1 `*panicGoal`/flagMove) all PASS |

**Score:** 6/6 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `server/ai_goals_panic.go` | Go-native panicGoal (canUse/shouldPanic/findRandomPosition/lookForWater/start/stop/canContinue) | ✓ VERIFIED | 207 lines (min 80), `type panicGoal` present, all methods present, every method cites javap PanicGoal bytecode; wired via `addGoal(1, ...)` |
| `server/ai_goals_panic_test.go` | TestPanicGoalFleesOnPanicDamage + TestPanicGoalIgnoresNonPanicDamage | ✓ VERIFIED | Both functions present and PASS; substantive (full-tick flee drive + direct canUse false assertion) |
| `plugins/vanilla_pig/main.star` | panic_can_use/panic_stop/panic_continue + goal(@1, MOVE) | ✓ VERIFIED | `panic_can_use` (line 89), PANIC_H/PANIC_V/PANIC_SPEED consts, `goal(priority=1, ...)` decl present |
| `server/assets/vanilla_pig/main.star` | byte-identical mirror | ✓ VERIFIED | `diff` against repo-root copy EMPTY |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| combat_mob.go flag2 block | `e.hasLastDamage = true` | set alongside `e.lastDamageSource = src` | ✓ WIRED | combat_mob.go:111+116 — both stores in the same flag2 block |
| ai_goals_panic.go shouldPanic | `lastDamageSource.is("panic_causes")` | damageSource.is tag read | ✓ WIRED | ai_goals_panic.go:92 → damage_source.go:78-80 genuine table read |
| ai_mob.go newPigAI | `addGoal(1, newPanicGoal(...))` | registration after FloatGoal@0 | ✓ WIRED | ai_mob.go:254 |
| plugin panic_can_use | `nav.path_to(*flat)` 31 floats, landMode 0.0 | setWantCandidates (case 31) | ✓ WIRED | main.star:103, flat ends with `0.0` (landMode) at line 102 |
| plugin handle | `damage_in_tag(name)` host-side `is` | damageInTag bound method | ✓ WIRED | plugin_entity.go:142-143 dispatch + 278-291 genuine `e.lastDamageSource.is(tagName)` |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
| --- | --- | --- | --- | --- |
| panicGoal.shouldPanic | `e.hasLastDamage`, `e.lastDamageSource` | set by `applyDamageEntity`→flag2 block (combat_mob.go:111,116) | Yes — real damage event sets both | ✓ FLOWING |
| panicGoal.findRandomPosition | `wantCandidates` | `generateRandomDirection(r, 5, 4)` real RNG draws → setWantCandidates → snapStrollWant | Yes — 10 real candidates, snapped to ground | ✓ FLOWING |
| damageInTag handle | tag membership | `tag.DamageTypeTags["panic_causes"]` (29-id table, tags.go:152) | Yes — genuine table read, not const | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| --- | --- | --- | --- |
| Panic flee fires on panic damage | `go test -run TestPanicGoalFleesOnPanicDamage` | PASS (0.02s) | ✓ PASS |
| No panic on non-panic damage | `go test -run TestPanicGoalIgnoresNonPanicDamage` | PASS (0.00s) | ✓ PASS |
| Oracle byte-identical | `go test -run TestPluginPigEqualsGoNativePig` | PASS (0.03s) | ✓ PASS |
| 5 goals at {0,1,6,7,8} | `go test -run 'TestPluginPigBootLoads\|TestVanillaPigDeclaresGoalSet\|TestPigGoalSetRegistered'` | 3/3 PASS | ✓ PASS |
| Regression suite | `go test ./server/ ./data/tag/ ./level/loot/` | all ok | ✓ PASS |
| Build + vet (CGO=0) | `go build ./...` + `go vet ./...` | exit 0 both | ✓ PASS |
| .star byte-identical | `diff plugins/.../main.star server/assets/.../main.star` | empty | ✓ PASS |
| -race (Docker) | `docker run golang:1.26 go test -race ./server/` | ok 11.348s, 0 races, 0 FAIL | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| MOB-GATE-01 | 31-01-PLAN.md | Pig's 5 deferred goals wired 1:1 in lockstep (incremental across Phases 30–33; formally closed Phase 33) | ✓ SATISFIED (advanced) | PanicGoal@1 — this phase's increment — wired Go + both .star in lockstep; goal set now {0,1,6,7,8}. Requirement formally closes at Phase 33 per REQUIREMENTS.md:102 (not a gap for Phase 31) |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| ai_goals_panic.go | 102, 113 | `isOnFire`/`lookForWater` return false-stubs | ℹ️ Info | CITED jar-faithful stubs — a non-burning pig never enters the on-fire branch (zero RNG), upgrade paths documented (fire-tick / fluid spiral). Faithful per CLAUDE.md stub rule (cited const = vanilla default for a non-burning pig). Not a stub-anti-pattern: the dynamic flee path (findRandomPosition) is fully real. |
| ai_goals_panic.go | 173-186 | panic speedModifier 1.25 stored but not routed to nav speed | ℹ️ Info | Cited-deferred — the want carries POSITION only; v1 nav uses single `pigWalkSpeed` const (same posture as stroll). Speed 1.25 is stored + cited; success criteria require the value declared/cited, not live-routed. No goal-blocking impact. |

### Human Verification Required

None. All success criteria are programmatically verified via headless tests + Docker -race. (Note: a separate LIVE bot test reportedly confirmed a hit pig flees ~4.2 blocks end-to-end against a running server — corroborating evidence, but the headless gates above are sufficient and were all re-run independently.)

### Gaps Summary

No gaps. All 6 observable truths VERIFIED, all 4 artifacts pass three+four levels (exist, substantive, wired, data flowing), all 5 key links WIRED. Every headless gate green:

- Gate 1 (flee/non-panic pair): PASS
- Gate 2 (oracle byte-identical): PASS
- Gate 3 (5-goal boot-load ×3): PASS
- Gate 4 (regression server/tag/loot): PASS
- Gate 5 (build + vet CGO=0): PASS
- Gate 6 (.star diff empty): PASS
- Gate 7 (Docker -race ./server/): PASS, 0 races, 0 FAIL

Faithful scope confirmed: PanicGoal@1 speed 1.25 / priority 1 cited from javap Pig.registerGoals; shouldPanic uses the genuine `hasLastDamage` not-null signal + `is("panic_causes")` table read (not was_hurt, not typeTag==0); findRandomPosition = DefaultRandomPos.getPos(5,4) via reused generateRandomDirection; isOnFire/lookForWater/speedModifier are cited stubs with documented upgrade paths. MOB-GATE-01 advanced (formally closes Phase 33).

---

_Verified: 2026-06-30_
_Verifier: Claude (gsd-verifier)_
