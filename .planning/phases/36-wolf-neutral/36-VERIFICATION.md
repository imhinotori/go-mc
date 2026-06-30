---
phase: 36-wolf-neutral
verified: 2026-06-30T00:00:00Z
status: passed
score: 3/3 must-haves verified (all 8 task-level must-haves confirmed)
overrides_applied: 0
re_verification:
  # No previous verification — initial mode
gaps: []
---

# Phase 36: Wolf (Neutral/Tameable) Verification Report

**Phase Goal:** The flagship neutral mob — a wolf that lives wild, defends itself, and (second pass) can be tamed, sit, and anger on hit. The pig oracle stays byte-identical.
**Verified:** 2026-06-30
**Status:** passed
**Re-verification:** No — initial verification
**Milestone note:** This is the LAST v5 phase. With it passing, v5 ("Mob Behaviors & Living-Entity Subsystems") is ready for milestone-completion verification.

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria SC#1–3)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| SC1 | `"wolf"` base type + NEW 1:1 `Wolf.createAttributes()` supplier in `level/attribute/defaults.go`; wild goal set runs jar-faithfully | ✓ VERIFIED | `wolfSupplier()` at defaults.go:182 (MOVEMENT_SPEED 0.30000001192092896, MAX_HEALTH 8.0, ATTACK_DAMAGE 4.0); `"wolf"` base_type resolver + 10 goalSelector goals; `TestWolfBootLoads` asserts MH 8.0/ATK 4.0/CREATURE/10+5 goals — PASS |
| SC2 | Tame second pass lands: `TamableAnimal` owner/sit state + OwnerHurt target chain + anger-on-hit reading `lastDamageSource` | ✓ VERIFIED | tame/orderedToSit/inSittingPose/ownerUUID/angerEndTime/angerTarget state in entity.go; 4 new goal classes (sit/follow_owner/owner_hurt_by/owner_hurt); anger trigger at combat_mob.go:149 reads the post-hit store-point; `TestWolfTame/Sit/FollowOwner/AngerOnHit` — all PASS |
| SC3 | Standing: jar-verified; CGO=0 + no new deps; lockstep discipline; pig oracle GREEN; -race clean | ✓ VERIFIED | `CGO_ENABLED=0 go build ./...` exit 0; `go vet ./server/` clean; `TestPluginPigEqualsGoNativePig` PASS; `TestWolfTameRNG`/`TestWolfAngerRNG` lockstep proofs PASS; go.mod unchanged; Docker -race confirmed ok by gate executor |

**Score: 3/3 ROADMAP Success Criteria verified.**

### Task-Level Must-Haves (from verification context)

| # | Must-have | Status | Evidence |
|---|-----------|--------|----------|
| 1 | TAME: BONE feed (nextInt(3) path) → isTame + HP 8→40 + owner + sit | ✓ VERIFIED | `TestWolfTame`/`TestWolfTameSuccess` assert `wolf.tame`, `health==40.0`, `MAX_HEALTH==40.0`, `ownerUUID==p.entityID`, `orderedToSit`, bone consumed — PASS |
| 2 | SIT: tamed wolf sit-toggles | ✓ VERIFIED | `TestWolfSit` (canUse, MOVE+JUMP claimed, inSittingPose set/cleared) + `TestWolfSitToggleByOwner` (false→true→false) — PASS |
| 3 | WILD-NO-AGGRO: wild un-hit wolf does NOT target a nearby player | ✓ VERIFIED | `TestWolfWildNoAggro`: forceTrigger isolates the anger gate; non-vacuous (un-gated scan WOULD acquire the player) — PASS |
| 4 | ANGER-ON-HIT: hit wolf targets attacker (angerEndTime = gameTime+400+nextInt(381)) | ✓ VERIFIED | `TestWolfAngerOnHit` drives real `applyDamageEntity`, asserts exact angerEndTime, angerTarget, isAngryAt, goal acquire + commit, + gametime-endpoint expiry — PASS |
| 5 | SKELETON-TARGET: wolf can target a skeleton (B2) | ✓ VERIFIED | `TestWolfSkeletonTarget`: in-range acquire + out-of-range rejection, nil angerGate — PASS |
| 6 | FOLLOW-OWNER | ✓ VERIFIED | `TestWolfFollowOwner`: owner >10 blocks → canUse + nav want; <2 blocks → canUse false — PASS |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `level/attribute/defaults.go` | wolfSupplier | ✓ VERIFIED | wolfSupplier():182 — spd 0.30000001192092896 / MH 8.0 / ATK 4.0 |
| `server/ai_goals_target.go` | parameterized target goal | ✓ VERIFIED | nearestTargetClass enum, targetClass + angerGate fields, isAngryAt, newAngryPlayerTargetGoal/newSkeletonTargetGoal, nearestEntityOfTypeAt skeleton scan; bare goal byte-identical (zero-value PLAYER + nil gate) |
| `server/ai_goals_sit.go` | SitWhenOrderedToGoal | ✓ VERIFIED | newSitWhenOrderedToGoal():37 |
| `server/ai_goals_owner.go` | FollowOwner + OwnerHurt×2 | ✓ VERIFIED | newFollowOwnerGoal:57, newOwnerHurtByTargetGoal:203, newOwnerHurtTargetGoal:294 |
| `server/plugin_mob_ai.go` | 6 new kinds | ✓ VERIFIED | sit:287, follow_owner:291, owner_hurt_by:296, owner_hurt:299, angry_player_target:302, skeleton_target:307 |
| `server/combat_mob.go` | anger trigger | ✓ VERIFIED | combat_mob.go:149 gametime-endpoint, wolf+player-gated, one nextInt(381) draw |
| `server/attack_dispatch.go` | tryWolfInteract | ✓ VERIFIED | gate:811, tryWolfInteract:1108, tryToTameWolf:1196, applyWolfTamingSideEffects:1048 (SetBaseValue 40/8); exactly 1 production nextInt(3):1199 |
| `plugins/vanilla_wolf/main.star` | wolf decl, no bare target | ✓ VERIFIED | 0 `nearest_attackable_target`; uses angry_player_target + skeleton_target + the 6 kinds |
| `server/vanilla_pig_embed.go` | loads all 8 | ✓ VERIFIED | //go:embed includes all 8; vanillaMobNames lists all 8 incl. vanillaWolfMobName |
| `server/commands_dbg.go` | /dbg wolf | ✓ VERIFIED | case "wolf":50 + usage line incl. wolf |
| `server/wolf_test.go` | gate suite | ✓ VERIFIED | 579 lines, 9 holistic+RNG tests, all substantive (assert real observables) |
| `server/wolf_taming_test.go` | taming suite | ✓ VERIFIED | 290 lines, 8 taming tests |
| `internal/botclient/live_wolf_test.go` | live bot test | ✓ VERIFIED | present (4590 bytes) — confirmed run + PASS per context |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| wolf .star | buildNativeGoal | kind="angry_player_target"/"skeleton_target"/etc | ✓ WIRED | 6 wolf kinds resolve; `TestWolfBootLoads` asserts the real boot-loaded goals carry angerGate (player non-nil, skeleton nil) |
| combat store-point | angerEndTime | applyDamageEntity wolf-gate | ✓ WIRED | `TestWolfAngerOnHit` drives real applyDamageEntity → exact angerEndTime |
| tryWolfInteract | handleInteract | mob.typ==Wolf.ID gate before tryFeedAnimal | ✓ WIRED | attack_dispatch.go:811 |
| angry_player_target goal | isAngryAt | angerGate predicate | ✓ WIRED | no-aggro (gate holds) + anger-on-hit (gate opens) both test-proven |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| build | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| vet | `CGO_ENABLED=0 go vet ./server/` | clean | ✓ PASS |
| wolf suite | `go test ./server/ -run TestWolf -v` | 17 tests PASS | ✓ PASS |
| hostile regression | `go test -run 'TestZombie/Skeleton/Spider/NearestAttackableTargetGateUsesTen'` | 4 PASS | ✓ PASS |
| pig oracle | `go test -run TestPluginPigEqualsGoNativePig` | PASS | ✓ PASS |
| full server suite | `go test ./server/ -count=1` | ok 20.3s, no FAIL | ✓ PASS |
| level suite | `go test ./level/... -count=1` | all ok | ✓ PASS |
| 8 embed pairs | `diff plugins/* server/assets/*` | all identical | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| MOB-NEUT-01 | 36-01..04 | Wolf wild subset: "wolf" base type + supplier + wild goal set | ✓ SATISFIED | wolfSupplier + 10 goalSelector + 5 targetSelector; TestWolfBootLoads/WildNoAggro/SkeletonTarget PASS |
| MOB-NEUT-02 | 36-01..04 | Wolf tame second pass: TamableAnimal owner/sit + OwnerHurt chain + anger-on-hit | ✓ SATISFIED | tame/sit/owner state + 4 goal classes + anger trigger; TestWolfTame/Sit/FollowOwner/AngerOnHit PASS |

No orphaned requirements.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | — | — | None blocking. Cited constant-false stubs (owner inbound lastHurtByMob, FollowOwner teleport, water malus, advancement trigger) are FAITHFUL v1 reductions of not-yet-built subsystems — each a named predicate, not a baked-away value. The owner ATTACK side (getLastHurtMob, OwnerHurtTargetGoal) is live. |

### Deferred Items (cited, not gaps)

All deferrals carry their not-yet-built target entity/subsystem and are correctly out-of-scope per CONTEXT:
- WolfAvoidEntityGoal<Llama> (no Llama mob), BegGoal (client visual), NonTameRandomTargetGoal prey-hunting (no prey/turtle mobs), ResetUniversalAngerTargetGoal (gamerule defaults FALSE + dissolved by gametime-endpoint model), feed-heal/collar-dye/body-armor (no equipment/dye/health-feed system), FollowOwner teleport + water malus, owner-UUID wire broadcast (index 19).

### Human Verification Required

None required for automated acceptance. The live-bot in-world VERIFY checkpoint (Task 3) was already run and PASSED per the verification context (`internal/botclient/live_wolf_test.go` confirms a wild wolf is neutral idle + engages on strike). The pre-existing join-health server bug noted in STATE carryover is NOT a wolf defect — the wolf melee/anger is unit-proven through the keystone.

### Gaps Summary

No gaps. Every must-have resolves to VERIFIED with passing, substantive test evidence:
- The anger model is the gametime-endpoint (angerEndTime int64; isAngry = endTime>0 && endTime-gameTime>0) — confirmed NO per-tick decrement, NO ResetUniversalAngerTargetGoal (both mentions are doc comments confirming dissolution).
- The B1/B2 parameterization is additive — `TestNearestAttackableTargetGateUsesTen` + all 3 hostile behavior tests stay GREEN; the wolf .star uses 0 bare `nearest_attackable_target`.
- The pig oracle is byte-identical (`TestPluginPigEqualsGoNativePig` PASS).
- All 8 mob embed pairs byte-identical; all 8 boot-load.
- Full server + level suites clean (the documented `TestRegionPanicIsolated` false-alarm recovered-panic print does not surface a FAIL — the suite exits `ok`).

---

_Verified: 2026-06-30_
_Verifier: Claude (gsd-verifier)_
