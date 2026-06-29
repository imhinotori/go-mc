---
phase: 24-vanilla-mobs-as-plugins
verified: 2026-06-28T08:05:00Z
status: passed
score: 6/6 must-haves verified
overrides_applied: 0
re_verification: false
---

# Phase 24: Vanilla mobs AS plugins (1:1 dogfood) Verification Report

**Phase Goal:** The existing Go mob/entity logic is rewritten as Starlark plugins that remain a literal 1:1 port of the 26.2 jar — validating the API expresses real vanilla AI (the FIRST dogfood). (PLUGIN-04)
**Verified:** 2026-06-28T08:05:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

Phase 24 is autonomous/standalone-testable (no real-client gate — that is Phase 28). The phase goal is fully achieved: the vanilla Pig's passive-ambient AI is re-expressed AS a Starlark plugin that is a literal, bytecode-faithful 1:1 port of the 26.2 jar, swapped in as the only pig, and proven behavior-identical to the Go-native oracle it replaces. All gates run by the verifier (build, vet, full quick suite, the behavior-identity gate, and Docker -race) are green.

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Per-entity seeded RNG reproduces the jar draw order; fixed seed → deterministic; the TestTickAIDrivesMobs flake retired | ✓ VERIFIED | `server/ai_random.go`: `entityRandom` over `math/rand/v2.PCG`, `newEntityRandom`/`reseed`/`nextInt`/`nextFloat`/`nextDouble`, each citing its `RandomSource` analogue + the javap-confirmed draw order. `TestEntityRandFaithfulDrawOrder` PASS 3/3; `TestTickAIDrivesMobs` PASS 3/3 (deterministic). `! grep math/rand/v2 ai_goals_passive.go` clean (per 24-01 SUMMARY). |
| 2 | 3 handle extensions (set_look/set_look_at, world.nearest_player, rand_int/rand_float/rand_double) 1:1 + capability-gated; requires_update_every_tick threaded; per-(mob,goal) scratch | ✓ VERIFIED | `server/plugin_entity.go`: `setLook` (entities.write), `setLookAt` (entities.write, derives the same `yawTowardDeg`), `nearestPlayer` (world.read, reuses `nearestPlayerAt`), `randInt`/`randFloat`/`randDouble` (ungated, e.ai.rng), `getState`/`setState` over per-goal scratch via `newEntityHandleWithScratch`. `requiresUpdateEveryTick` threaded goalDecl→starlarkGoal override (`plugin_mob_decl.go:240/272`, `plugin_mob_ai.go:129/191`). `TestSetLookSeam`/`TestNearestPlayerSeam` PASS 3/3. |
| 3 | The 3 existing pig goals re-expressed 1:1 in vanilla_pig/main.star, each CITING its jar class + matching bytecode | ✓ VERIFIED | `plugins/vanilla_pig/main.star` declares Stroll@6 MOVE, LookAt@7 LOOK, LookAround@8 MOVE\|LOOK, each `# ports net.minecraft.world.entity.ai.goal.<Class>`. **javap spot-checks vs `temp/cache/26.2-inner.jar`:** RandomLookAroundGoal — flags EnumSet.of(MOVE,LOOK)✓, canUse nextFloat()<0.02f✓, canContinue lookTime>=0✓, start `6.283185307179586*nextDouble()`→cos/sin→`20+nextInt(20)`✓, requiresUpdateEveryTick=iconst_1(true)✓, tick decrement-THEN-setLookAt✓. LookAtPlayerGoal — canUse `nextFloat()<probability` gate first✓. `TestVanillaPigGoalsPorted`/`TestVanillaPigDeclaresGoalSet` PASS 3/3. |
| 4 | Each NEW goal (Float/Panic/Breed/Tempt/FollowParent) built-1:1 OR deferred WITH a cited reason (no silent skip, no fake) | ✓ VERIFIED | `deferred-goals.md`: javap-verified `Pig.registerGoals` full set; all 5 DEFERRED, each with jar FQCN + exact missing subsystem (mob JumpControl+fluid; mob last-damage-source; entity aging/breeding; held-item+item-tag) + codebase grep evidence of absence. The deferral is correct per the no-built-but-unwired rule (building would be unwired or faked). |
| 5 | SWAP: newPigAI replaced at both sites by the plugin build; vanilla_pig boot-loads (//go:embed); plugin pig is the only pig (entity.Pig.ID); Go goals kept as oracle | ✓ VERIFIED | `async.go:316` + `debug.go:169` call `t.spawnVanillaPig(...)`; `! grep newPigAI\(\) async.go debug.go` clean. `vanilla_pig_embed.go` `//go:embed assets/vanilla_pig/...`, `loadVanillaPigRegistry` loud-fail if absent; `cmd/sulfur/main.go:311` `LoadVanillaPigRegistry`→`SetMobRegistry` before tick.Run, `log.Fatalf` on failure. Embedded copy byte-identical to plugins/ copy (diff clean). `spawnVanillaPig` → `spawnDeclaredMob` renders entity.Pig.ID. `newPigAI`+Go goals retained as oracle. `TestPluginPigBootLoads` PASS 3/3. |
| 6 | THE GATE — behavior-identical: existing pig-AI suite passes against the plugin pig; TestPluginPigEqualsGoNativePig proves identity | ✓ VERIFIED | `TestPluginPigEqualsGoNativePig` (Go pig vs plugin pig, same id-seed/world over 500 ticks, identical wantTarget/yaw/position) PASS 3/3. `TestServerAiStep*`, `TestPigGoalSetRegistered`, `TestVanillaPigStrollSetsTarget`/`LooksAtPlayer`, `TestPluginPigRace` PASS 3/3. The historically-flaky `TestServerAiStepWalksToGoalTarget` PASS 3/3. |

**Score:** 6/6 truths verified

### Roadmap Success Criteria Coverage

| # | Success Criterion (PLUGIN-04) | Status | Evidence |
|---|-------------------------------|--------|----------|
| 1 | Vanilla mob behavior re-expressed in Starlark as a literal jar port (cited, bytecode-verified) | ✓ SATISFIED | Truths 3 + 4; javap spot-checks of RandomLookAround + LookAtPlayer match the .star exactly. |
| 2 | Plugin-driven vanilla mob behavior-identical to the Go-native path it replaces | ✓ SATISFIED | Truth 6; `TestPluginPigEqualsGoNativePig` green. |
| 3 | Existing mob-AI tests green against the plugin path; Docker -race clean | ✓ SATISFIED | Truth 6 + the verifier's Docker -race run (server 16.9s, host 1.3s, starlark 1.0s — clean). |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `server/ai_random.go` | per-entity seeded RandomSource | ✓ VERIFIED | newEntityRandom + nextInt/nextFloat/nextDouble, jar-cited, tick-owned. |
| `server/plugin_entity.go` | set_look/set_look_at + rand_*/state + nearest_player | ✓ VERIFIED | All seams present, capability-gated as specified; scratch via newEntityHandleWithScratch. |
| `plugins/vanilla_pig/main.star` | the 1:1 Pig AI plugin (3 goals, jar-cited) | ✓ VERIFIED | 3 goals @6/@7/@8, each citing its ai.goal.* class; bytecode-matched. |
| `plugins/vanilla_pig/plugin.toml` | least-privilege caps | ✓ VERIFIED | entities.read/write, world.read, nav — NO world.write. |
| `server/vanilla_pig.go` | spawnVanillaPig + registry | ✓ VERIFIED | spawnVanillaPig/WithID via spawnDeclaredMob; loud-fail on missing registry/decl. |
| `server/vanilla_pig_embed.go` | //go:embed boot-load | ✓ VERIFIED | embeds assets/vanilla_pig/*, materialize+LoadDirWith, loud-fail if absent. |
| `server/plugin_pig_test.go` | the gate + boot tests | ✓ VERIFIED | TestPluginPigEqualsGoNativePig/BootLoads/VanillaPig* all green. |
| `deferred-goals.md` | the build/defer audit | ✓ VERIFIED | All 5 new goals cited + evidenced. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| async.go spawn | spawnVanillaPig | the SWAP | ✓ WIRED | async.go:316 calls t.spawnVanillaPig; no newPigAI(). |
| debug.go spawn | spawnVanillaPig | the SWAP | ✓ WIRED | debug.go:169 calls t.spawnVanillaPig; no newPigAI(). |
| cmd/sulfur/main.go | tick mob registry | boot-load embed | ✓ WIRED | LoadVanillaPigRegistry→SetMobRegistry before tick.Run, Fatalf on fail. |
| main.star look goals | entity.set_look_at | the LOOK seam | ✓ WIRED | look_tick/around_tick call set_look_at; seam present in plugin_entity.go. |
| goalDecl.requiresUpdateEveryTick | starlarkGoal.requiresUpdateEveryTick() | the fidelity fix | ✓ WIRED | threaded plugin_mob_decl.go→plugin_mob_ai.go:191; @8 carries it. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| CGO=0 ship build | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| go vet | `go vet ./server/ ./plugin/...` | exit 0 | ✓ PASS |
| Full quick suite (clean cache) | `go test -count=1 ./server/ ./plugin/... ./level/attribute/` | all ok | ✓ PASS |
| The behavior-identity gate (3×) | `go test -run TestPluginPigEqualsGoNativePig -count=3` | PASS 3/3 | ✓ PASS |
| Determinism / retired flake (3×) | `go test -run 'TestTickAIDrivesMobs\|TestServerAiStepWalksToGoalTarget' -count=3` | PASS 3/3 | ✓ PASS |
| Docker -race (CGO=1) | `docker run golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...` | clean (16.9s/1.3s/1.0s) | ✓ PASS |
| jar fidelity spot-check | `javap -c -p RandomLookAroundGoal / LookAtPlayerGoal` | matches main.star | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PLUGIN-04 | 24-01, 24-02 | Vanilla mobs + entity logic as Starlark plugins, literal 1:1 port, behavior-identical to Go-native, jar-verified, existing mob-AI tests green | ✓ SATISFIED | All 6 truths + 3 roadmap SCs verified above. |

### Anti-Patterns Found

None. Grep for TODO/FIXME/placeholder/not-implemented across the key phase files returned no matches (DEFER/oracle mentions are intentional, documented design — not stubs).

### Git Attribution Check

The 6 phase-24 commits (2d8d8ac9, 2be8584c, 0f624c45, 56b2f88a, a66659b7, 4a61e648 + ab45f2d8 docs) contain NO Claude/Co-Authored-By/generated-with attribution. Clean.

### Human Verification Required

None. Phase 24 is autonomous/standalone-testable — every success criterion is provable programmatically (the behavior-identity gate + Docker -race + the bytecode spot-checks). The real-client play gate is Phase 28, out of scope here.

### Gaps Summary

No gaps. The expected dogfood outcome — surfacing further API needs (rand_double, sandbox math, set_look_at, nav.stop) — was realized and is tied to the faithfully-ported goals (closed) and the cited-deferred goals (justified, not gaps). The 5 deferred new goals are correctly deferred per the no-built-but-unwired rule, each with a jar citation + missing subsystem + codebase evidence; this is the right call, not a missed deliverable. Their prerequisite subsystems (mob jump control + fluid, mob damage pipeline, entity aging/breeding, held-item + item tags) are future-phase work, not Phase-24 scope.

---

_Verified: 2026-06-28T08:05:00Z_
_Verifier: Claude (gsd-verifier)_
