# Phase 24: Vanilla mobs AS plugins (1:1 dogfood) — Research

**Researched:** 2026-06-28
**Domain:** Re-express the EXISTING Go vanilla-mob AI (the Pig — the only mob with real Go AI) AS Starlark plugins using the Phase-23 `declare_mob`/`goal`/handle API, while staying a LITERAL method-for-method port of the unobfuscated 26.2 jar. This is the FIRST 1:1 dogfood: it validates that the Phase-23 API can *express real vanilla AI*. The load-bearing finding is that it CANNOT yet — three Pig goals (`LookAtPlayerGoal`, `RandomLookAroundGoal`, and a faithful `RandomStrollGoal`) read/write state the Phase-23 handle does NOT expose (head/body yaw mutation, nearest-PLAYER lookup, a per-entity RNG with a faithful draw order). Phase 24's real work is the FAITHFUL EXTENSION of the handle API to cover those, then porting the goals against bytecode.
**Confidence:** HIGH on the Sulfur side (every AI file — `ai_mob.go`, `ai_goal.go`, `ai_goals_passive.go`, the Phase-23 `plugin_mob_decl.go`/`plugin_mob_ai.go`/`plugin_entity.go`/`plugin_capability.go`, the AI tests, and the two `newPigAI` spawn sites — was read in full this session). HIGH on the jar anchor (`Pig.registerGoals`, `RandomStrollGoal.canUse` disassembled directly from `temp/cache/26.2-inner.jar` via `javap -c -p` this session). MEDIUM only on the final handle-extension SURFACE and the swap-vs-parallel mechanics, which are operator/planner decisions surfaced in Open Questions.

## Summary

Phase 23 proved a Starlark goal IS a `server.Goal` arbitrated by the SAME `goalSelector`, with a thin id-handle bridge that is `-race` clean. But Phase 23's gate mob was a CUSTOM wander mob (one `MOVE` goal, free of the 1:1 mandate). Phase 24 is the opposite: take the REAL Go Pig AI — three jar-ported goals (`WaterAvoidingRandomStrollGoal`@6 `[MOVE]`, `LookAtPlayerGoal`@7 `[LOOK]`, `RandomLookAroundGoal`@8 `[MOVE|LOOK]`, all in `server/ai_goals_passive.go`) plus the `newPigAI()` builder (`server/ai_mob.go`) — and re-express EACH as a declared Starlark goal that stays a literal port of the same `net.minecraft.world.entity.ai.goal.*` classes. The existing mob-AI tests (`ai_mob_test.go`, `navigation_test.go`) must stay green against the plugin-driven Pig.

The investigation's central result is the **validation gap** the phase exists to find: mapping each Pig goal's needs onto the Phase-23 handle surface shows the handle is INSUFFICIENT to express two of the three goals faithfully. `LookAtPlayerGoal` reads the nearest PLAYER (`nearestPlayerWithin` scans `loop.players` — players are NOT in the entityStore, and the handle's `world.entities_near` returns mob handles, never players) and SETS `e.headYaw`/`e.yaw` (the handle has NO yaw/head mutate — `set_velocity`/`move_to`/`set_attribute` are the only entity mutates). `RandomLookAroundGoal` SETS the same yaw fields. A faithful `RandomStrollGoal` must draw from the mob's RandomSource in the EXACT vanilla order (`nextInt(reducedTickDelay(interval))` THEN `getPosition()` which draws again) — the handle exposes NO random source, and the Go port currently uses the `math/rand/v2` PACKAGE RNG (not a per-entity seeded source), which is itself a known 1:1 debt (`STATE.md`: `TestTickAIDrivesMobs` flaky — "mob AI non-deterministic (RNG/async-pool), needs a seeded source per the 1:1 mandate"). **This gap IS the phase's deliverable signal: "if vanilla AI doesn't fit the API, the API is wrong — caught here." Phase 24 must EXTEND the handle API (faithfully, capability-gated, tick-owned) to add: head/body-look mutation, nearest-player read, and a per-entity deterministic random source — then port the three goals against bytecode.**

**Primary recommendation:** (1) SCOPE Phase 24 to the Pig (the only mob with Go AI) and its three already-ported passive goals — do NOT add `Float`/`Panic`/`Breed`/`Tempt`/`FollowParent` (their preconditions — water hazard, damage source, breeding, items — are unbuilt; adding them violates no-built-but-unwired). (2) EXTEND the handle API with the three faithful additions the goals need: `entity.set_look(yaw, pitch)` + `entity.head_yaw`/`yaw`/`pitch` reads (the LOOK mutate seam), `world.nearest_player(x,y,z,max_dist)` (the player-lookup seam, scanning `loop.players` server-side), and a per-entity `entity.random()`/`rand_int(n)`/`rand_float()` backed by the mob's seeded RandomSource so the draw order is faithful. (3) PORT each Go goal to a declared Starlark goal 1:1 (same priority, same flags, same `canUse`/`canContinue`/`start`/`stop`/`tick`, same RNG draw order), cite each `ai.goal.*` class. (4) SWAP, don't parallel: make `buildAIFromDecl` (driven by a "vanilla pig" plugin declaration) the spawn path that REPLACES `newPigAI()` at its two call sites (`async.go` natural spawn, `debug.go` debug spawn), and assert the SAME existing tests pass against the plugin-driven Pig. (5) FIX the RNG-determinism debt as part of the faithful port (a seeded per-entity source replacing the package `math/rand/v2`), which also retires the `TestTickAIDrivesMobs` / `TestServerAiStepWalksToGoalTarget` flakes.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-04 | The vanilla mobs + entity logic are rewritten AS Starlark plugins that remain a literal 1:1 port of the 26.2 jar. Validates the API expresses real vanilla AI. The plugin-driven vanilla mob is behavior-identical to the Go-native path it replaces, jar-verified against bytecode, with the existing mob-AI tests green. (FIRST dogfood — if vanilla AI doesn't fit the API, the API is wrong, caught here before Python.) | The port-source inventory (only the Pig has Go AI; its 3 passive goals — `randomStrollGoal`/`lookAtPlayerGoal`/`randomLookAroundGoal` — and the `newPigAI` builder) (Standard Stack §Port source; Architecture §Goal-by-goal map). The jar anchor for each goal, bit-for-bit disassembled this session (Architecture §Jar source). THE VALIDATION GAP — the handle does NOT express LookAt/LookAround/faithful Stroll; the required faithful extensions (`set_look`, `nearest_player`, per-entity RNG) (Architecture §The API-is-wrong findings; Don't Hand-Roll; Open Questions). The SWAP path (replace `newPigAI` at its 2 sites with the plugin-driven build) + behavior-identical proof via the EXISTING tests (Architecture §Swap; Validation Architecture). The RNG-draw-order trap + the seeded-source fix (Common Pitfalls §1). |
</phase_requirements>

<user_constraints>
## User Constraints

> No `CONTEXT.md` exists for Phase 24 yet — this research feeds `/gsd-discuss-phase` / the planner. The constraints below are extracted VERBATIM from `v4-PLAN.md` (the plan of record, Phase 24 row + build-order rationale) and `REQUIREMENTS.md` (PLUGIN-04). Open Questions flags what the operator must lock at kickoff (chiefly the handle-extension surface and swap-vs-parallel).

### Locked decisions (from v4-PLAN.md Phase-24 row + REQUIREMENTS PLUGIN-04)
- **The vanilla-mob plugins stay a LITERAL 1:1 jar port.** v4-PLAN: "Re-expressing a mob's AI in Starlark is still 'method-for-method copy of the 26.2 jar, verified against bytecode' — the runtime changed, the mandate did not." Each re-expressed goal is verified against the unobfuscated 26.2 jar (`temp/cache/26.2-inner.jar`, `javap -c -p` / CFR), cited, re-expressed in idiomatic Starlark (no GPL paste).
- **Behavior-identical to the Go-native path it replaces.** PLUGIN-04: "The plugin-driven vanilla mob is behavior-identical to the Go-native path it replaces … with the existing mob-AI tests green."
- **This is the API-validation phase.** v4-PLAN: "if vanilla AI doesn't fit the API, the API is wrong, caught here before Python." The phase is EXPECTED to surface + close API gaps.
- **Custom (non-vanilla) plugins remain free of the mandate** — Phase 23's custom wander mob is NOT re-touched here; Phase 24 is the VANILLA dogfood.
- **CGO_ENABLED=0 default binary** — no new runtime dep (Starlark is in via Phase 21; the goals are `.star` + existing Sulfur types).
- **TICK-05 single-owner / Docker `-race` clean** carries into the plugin call seam exactly as Phase 23.
- **No Co-Authored-By / no Claude attribution** in commits (CLAUDE.md).
- **Push to `development`.**

### Claude's Discretion
- WHICH handle extensions to add and their exact names (`set_look` vs `look_at`, `nearest_player` signature, the RNG builtin shape) — recommend the minimal faithful set the three Pig goals actually read/write (Open Q §1).
- SWAP vs PARALLEL mechanics — recommend SWAP (the plugin pig REPLACES `newPigAI` at its spawn sites; the SAME tests assert the SAME behavior) (Open Q §2).
- The "vanilla pig" plugin's file location + load wiring (`testdata/mobplugins/` vs a real `plugins/` dir loaded at boot) (Open Q §3).
- Whether to fix the per-entity-RNG determinism debt now (recommend YES — a faithful port REQUIRES the right draw order, and it retires two documented flakes) (Open Q §4 / Pitfall 1).

### Deferred Ideas (OUT OF SCOPE for Phase 24)
- **The deferred Pig goals: `FloatGoal`@0, `PanicGoal`@1, `BreedGoal`@3, `TemptGoal`@4 (×2), `FollowParentGoal`@5.** Their preconditions (water hazard / mob-water-nav, a damage source, breeding + age state, the `PIG_FOOD` item tag + a held-item read) are UNBUILT in v1. Porting them needs those subsystems first — adding them now violates no-built-but-unwired. Phase 24 ports the SAME three passive goals the Go code ports (`@6 @7 @8`), now AS plugins.
- **Other mobs.** Only the Pig has Go AI (`grep newXxxAI` → `newPigAI` only). No Zombie/Cow/etc. AI exists to dogfood. The base-type resolver lists 12 types, but only the Pig has a goal set.
- **The `targetSelector` / `TARGET`-flag attack-AI seam → still deferred (Phase-23 Open Q §5).** The passive Pig has no attack targets; `serverAiStep` skips `targetSelector`. No combat mob is ported here.
- **Crafting/recipes → Phase 25. Python → Phase 26. Folia region-awareness → Phase 27. Real-client visual gate → Phase 28.**
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **GAMEPLAY IS A 1:1 PORT OF VANILLA — ABSOLUTE, AND IT APPLIES HERE.** Unlike Phase 23 (a custom mob, exempt), Phase 24's re-expressed goals ARE vanilla logic: each ported goal must be a literal method-for-method copy of the `net.minecraft.world.entity.ai.goal.*` class it ports — same priorities, same flags, same `canUse`/`canContinueToUse`/`start`/`stop`/`tick` conditions, same numeric ops, **same RNG draw order**. Verify every port against the jar bytecode (disassembled this session) before writing it. The ONLY permitted deviation is OPTIMIZATION that provably preserves identical observable gameplay.
- **The jar is the source of truth:** `temp/cache/26.2-inner.jar`, read via `javap -c -p -classpath 26.2-inner.jar <fqcn>` (confirmed working this session — the classes are in the jar, NOT in the partially-extracted `inner/x` tree which only has network/server packages).
- **CGO_ENABLED=0 default binary** — no new runtime dep. Gate: `CGO_ENABLED=0 go build ./...` clean.
- **`go test -race` non-negotiable** for the plugin call seam — Docker (CGO=1). Ship build = CGO=0, race test = CGO=1 (two gates, not in conflict — Phase 21/23 pattern).
- **TICK-05 single-owner** carries into every new handle extension: the look-mutate / nearest-player / RNG seams run ON the tick goroutine over tick-owned state; no handle holds a live `*Entity`.
- **No built-but-unwired code:** port ONLY the three passive goals that already exist in Go (their preconditions are met); add ONLY the handle extensions those three goals actually need. Do NOT pre-build `Float`/`Panic`/`Breed`/`Tempt`/`FollowParent` or their handle seams (water/damage/breed/item reads) with no goal consuming them.
- **Push to `development`.**

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Declare the vanilla Pig AI (3 goals) in Starlark, captured once at load | `.star` plugin (`declare_mob`/`goal`) | `server` (`mobRegistry` capture, Phase 23) | The Pig AI becomes a declaration; the existing capture path is reused unchanged. |
| Port each goal's `canUse`/`tick`/`start`/`stop` 1:1 | `.star` plugin (the goal callbacks) | jar (`ai.goal.*` bytecode) | The decision logic moves into Starlark, but the LOGIC is the jar's, verified against bytecode. |
| Run goal arbitration (MOVE/LOOK locking) | `server` (`goalSelector`, Phase 7) | — | UNCHANGED — a declared goal claims its flags exactly like the Go goal it replaces. |
| Drive `serverAiStep` order, A* nav, swept collision | `server` (`serverAiStep`/`groundNavigation`/`moveEntity`) | — | UNCHANGED — Go owns HOW; the plugin owns WHAT (target/look). |
| **Mutate the mob's look (head/body yaw + pitch)** | `server` (NEW `entity.set_look` mutate seam) | `.star` (LookAt/LookAround `tick` calls it) | **NEW EXTENSION** — the handle has no yaw mutate today; LookAt + LookAround both SET `e.headYaw`/`e.yaw`. |
| **Read the nearest PLAYER in range** | `server` (NEW `world.nearest_player`, scans `loop.players`) | `.star` (LookAt `canUse` calls it) | **NEW EXTENSION** — players are NOT in the entityStore; `entities_near` returns mobs only. |
| **Draw from the mob's deterministic RandomSource** | `server` (NEW per-entity seeded RNG + `entity.rand_int`/`rand_float`) | `.star` (every goal's `canUse`/`start` draws) | **NEW EXTENSION + a 1:1 FIX** — faithful behavior needs the vanilla draw order; the Go port currently uses package `math/rand/v2` (a documented determinism debt). |
| Seed/read attributes (`movement_speed` → walk pace) | `level/attribute` (`Map`, SUB-ATTRIB) + `server` (`seedAttributes`) | `.star` (declared `attributes={}`) | UNCHANGED — the Pig base type already has a real attribute supplier (Wave-1 fix). |
| Replace `newPigAI` at its spawn sites with the plugin build | `server` (`async.go`/`debug.go`, NEW load + swap) | `server` (`buildAIFromDecl`, Phase 23) | The swap is the behavior-identical proof: same tests, plugin-driven pig. |

**Key boundary:** The three NEW extension seams (`set_look`, `nearest_player`, per-entity RNG) all live in `server` (Option A, LOCKED in Phase 23 — handles live in `server` because they need `*TickLoop`/`*Entity`/`loop.players`). They are additive to the existing `entityHandle`/`worldHandle` (`server/plugin_entity.go`) — no new package, no import-direction change.

## Standard Stack

### Core (no new dependency — same as Phases 21–23)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `go.starlark.net` | `v0.0.0-20260613233743-8ba36ccb83fb` `[VERIFIED: go.mod pin, read this session]` | The declared-goal callbacks + the NEW handle-extension builtins (`set_look`, `nearest_player`, `rand_int`/`rand_float`) use the SAME `starlark.Value`/`HasAttrs`/`*Builtin.BindReceiver` surface Phase 23 established. | Same dep as 21/22/23; the extensions are new methods on existing handle types, not a new mechanism. |
| (existing) `server` AI subsystem | in-repo | `Goal`/`goalSelector`/`mobAI`/`serverAiStep`/`groundNavigation`/`moveEntity`/`entityStore` + the Phase-23 `starlarkGoal`/`buildAIFromDecl`/`spawnDeclaredMob`/handles. | The entire mechanical path + the declare-once adapter ALREADY EXIST and stay unchanged. Phase 24 adds 3 handle methods + 1 plugin + the swap. |
| (existing) `level/attribute` | in-repo | The Pig's real attribute supplier (`pigSupplier`: `max_health 10.0`, `movement_speed 0.25`, Wave-1 SUB-ATTRIB) seeds the declared pig. | Reuse; the pig already has jar-exact attributes (verified Phase 23). |
| (NEW, stdlib) per-entity `math/rand/v2.Rand` or a ported `LegacyRandomSource` | stdlib `math/rand/v2` or in-repo port | A DETERMINISTIC per-entity random source so the goal draw order is faithful (replacing the package-global `rand.IntN`/`rand.Float32` the Go goals use today). | The 1:1 mandate needs the right draw order + determinism; the current package RNG is a documented flake source (`STATE.md`). See Pitfall 1 / Open Q §4 for the seeded-source-vs-LegacyRandomSource decision. |

### Port source — the EXACT menu to re-express (read in full this session)
| Go type / func (file) | Ports from (jar class) | Priority | Flags | Re-express as |
|-----------------------|------------------------|----------|-------|---------------|
| `newPigAI()` (`ai_mob.go`) | `Pig.registerGoals` (the @6/@7/@8 subset) | — | — | A `declare_mob("vanilla_pig", base_type="pig", attributes={max_health:10.0, movement_speed:0.25}, goals=[…])` declaration. |
| `randomStrollGoal` (`ai_goals_passive.go`) | `RandomStrollGoal` / `WaterAvoidingRandomStrollGoal` | 6 | `MOVE` | A declared goal: `canUse` rolls `1-in-interval` then `getPosition` (a ±10/±7 offset), `start` → `nav.path_to(want)`, `canContinue` → `nav.has_path()`, `stop` → clear. |
| `lookAtPlayerGoal` (`ai_goals_passive.go`) | `LookAtPlayerGoal` | 7 | `LOOK` | A declared goal: `canUse` rolls `<0.02` then `world.nearest_player(...)` (NEW), `start` sets `lookTime=40+rnd(40)`, `tick` `entity.set_look(...)` (NEW) + decrement. |
| `randomLookAroundGoal` (`ai_goals_passive.go`) | `RandomLookAroundGoal` | 8 | `MOVE\|LOOK` | A declared goal: `canUse` rolls `<0.02`, `start` picks a heading on the unit circle + `lookTime=20+rnd(20)`, `requiresUpdateEveryTick`, `tick` `entity.set_look(...)` (NEW) + decrement. |

**`grep newXxxAI server/` → `newPigAI` is the ONLY mob-AI builder.** There are exactly three concrete goal structs (`randomStrollGoal`, `lookAtPlayerGoal`, `randomLookAroundGoal`); the others (`baseGoal`, `wrappedGoal`, `emptyGoal`) are infrastructure, and `testGoal`/`fixedTargetGoal`/`orderProbe`/`starlarkGoal` are test/adapter types, not vanilla goals.

### Jar anchor — disassembled this session (1:1 source of truth)
`Pig.registerGoals()` (verified via `javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.entity.animal.pig.Pig`):
```
0  FloatGoal(mob)                                    [DEFERRED — no water hazard]
1  PanicGoal(mob, 1.25)                              [DEFERRED — no damage source]
3  BreedGoal(mob, 1.0)                               [DEFERRED — no breeding]
4  TemptGoal(mob, 1.2, PIG_FOOD-predicate, false)    [DEFERRED — no items/tag read]
4  TemptGoal(mob, 1.2, CARROT_ON_A_STICK-pred, false)[DEFERRED — no items]
5  FollowParentGoal(mob, 1.1)                         [DEFERRED — no age/parent]
6  WaterAvoidingRandomStrollGoal(mob, 1.0)            ← PORT (randomStrollGoal)
7  LookAtPlayerGoal(mob, Player.class, 6.0f)          ← PORT (lookAtPlayerGoal)
8  RandomLookAroundGoal(mob)                          ← PORT (randomLookAroundGoal)
```
`RandomStrollGoal.canUse()` (disassembled) draw order: `hasControllingPassenger?` → `forceTrigger?` → (`checkNoActionTime?`) → `mob.getRandom().nextInt(reducedTickDelay(interval))` → `getPosition()` (draws again). **The `getRandom()` per-entity source + the nextInt-then-getPosition order is the 1:1 anchor for Pitfall 1.** The Go port (`ai_goals_passive.go`) omits the passenger/noActionTime gates (documented faithful-scope: a v1 passive Pig has no rider/combat) and uses package `rand.IntN` — both are re-confirmable against this bytecode during the port.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| EXTEND the handle (add `set_look`/`nearest_player`/RNG) then port | Port the goals "as close as the current handle allows" | **REJECT.** The whole POINT of the phase (v4-PLAN) is "if vanilla AI doesn't fit the API, the API is wrong — caught here." Porting LookAt without head-yaw mutation or a player read would be NON-faithful (it could not face a player), defeating the dogfood. The gap MUST be closed by extending the API, not by degrading the port. |
| SWAP (`buildAIFromDecl`-from-the-vanilla-pig-plugin replaces `newPigAI` at its 2 sites) | PARALLEL (keep `newPigAI`, add a plugin pig alongside, compare) | **REJECT parallel.** PLUGIN-04 demands "behavior-identical to the Go-native path IT REPLACES" — a replacement, not a coexistence. SWAP also runs the SAME `ai_mob_test.go`/`navigation_test.go` against the plugin pig (the behavior-identical proof). Parallel leaves dead Go AI (no-built-but-unwired) and never proves replacement. |
| Port the SAME 3 passive goals | Also port `Float`/`Panic`/`Breed`/`Tempt`/`FollowParent` | **REJECT (this phase).** Their preconditions are unbuilt; the Go code itself defers them. Porting them needs water-nav/damage/breeding/item subsystems first — out of scope, no-built-but-unwired. |
| Per-entity seeded RandomSource (fix the draw order) | Keep package `math/rand/v2` | **REJECT for the faithful port.** Vanilla determinism depends on the per-entity `mob.getRandom()` and the exact draw order; the package RNG is shared + non-deterministic and is the documented `TestTickAIDrivesMobs` flake. A faithful port REQUIRES the seeded source. (Whether to also port `LegacyRandomSource` bit-for-bit vs a seeded `rand/v2` is Open Q §4.) |
| Faithful `getPosition` (DefaultRandomPos bias toward a random angle + walkable node) | The Go port's uniform ±10/±7 offset | The Go `getPosition` is already a documented simplification (the walkability check is the navigation's job). The port should DECIDE: keep the documented simplification (consistent with the existing Go behavior the tests assert) or port `DefaultRandomPos.getPos` more faithfully. Recommend KEEP the existing simplification so the existing tests stay green (the phase proves API-expressiveness + 1:1 of the goal STRUCTURE, not a new terrain-pos algorithm). Flag as Open Q §5. |

**Installation:** None — no new dep. Re-verify the pin at execution: `cd D:/ender && go list -m go.starlark.net@latest`.

## Architecture Patterns

### System Architecture Diagram

```
  LOAD TIME (host goroutine, before the tick owns state)
  ─────────────────────────────────────────────────────────────────────────────
   plugins/vanilla_pig/main.star   (the 1:1 port — each goal cites its ai.goal.* class)
     PIG_FOOD = ...                                      # (only if a deferred goal needs it — NOT in v1)
     STROLL_INTERVAL = 120 ; STROLL_H = 10 ; STROLL_V = 7   # RandomStrollGoal constants (jar)

     def stroll_can_use(entity, world, nav):             # ports RandomStrollGoal.canUse 1:1
         if entity.rand_int(STROLL_INTERVAL) != 0: return False   # nextInt(interval) — DRAW 1
         dx = entity.rand_int(2*STROLL_H+1) - STROLL_H            # getPosition — DRAW 2,3,4
         dz = entity.rand_int(2*STROLL_H+1) - STROLL_H
         dy = entity.rand_int(2*STROLL_V+1) - STROLL_V
         _stash(entity, entity.x+dx, entity.y+dy, entity.z+dz)    # vanilla wantedX/Y/Z
         return True
     def stroll_start(entity, world, nav): nav.path_to(*_want(entity))   # navigation.moveTo
     def stroll_continue(entity, world, nav): return nav.has_path()      # !navigation.isDone()
     def stroll_stop(entity, world, nav): nav.stop()                     # navigation.stop()

     def look_can_use(entity, world, nav):               # ports LookAtPlayerGoal.canUse 1:1
         if entity.rand_float() >= 0.02: return False                    # DEFAULT_PROBABILITY
         p = world.nearest_player(entity.x, entity.eye_y, entity.z, 6.0) # NEW SEAM
         if p == None: return False
         _stash_look(entity, p); return True
     def look_start(entity, world, nav): _set_look_time(entity, 40 + entity.rand_int(40))
     def look_tick(entity, world, nav):
         lx,ly,lz = _look(entity); entity.set_look(yaw_toward(lx-entity.x, lz-entity.z))  # NEW SEAM
         _dec_look_time(entity)
     ...                                                  # randomLookAroundGoal similarly

     declare_mob("vanilla_pig", base_type="pig",
                 attributes={"max_health":10.0, "movement_speed":0.25},
                 goals=[ goal(priority=6, flags=["MOVE"],       tick=stroll_tick, can_use=stroll_can_use,
                              start=stroll_start, stop=stroll_stop),          # @6 [MOVE]
                         goal(priority=7, flags=["LOOK"],        tick=look_tick, can_use=look_can_use,
                              start=look_start, stop=look_stop),               # @7 [LOOK]
                         goal(priority=8, flags=["MOVE","LOOK"], tick=around_tick, can_use=around_can_use,
                              start=around_start) ])                           # @8 [MOVE|LOOK]
              │  (declare_mob/goal capture — UNCHANGED Phase-23 path)
  ═══════════════════════════════════════════════════════════════════════════════
  SPAWN TIME (tick goroutine) — THE SWAP (replaces newPigAI at its 2 sites)
  ─────────────────────────────────────────────────────────────────────────────
   async.go spawnCandidatesReady.applyTo:   pig := spawnVanillaPig(t, x,y,z)   # was: NewEntity + newPigAI
   debug.go SULFUR_DEBUG spawn:             pig := spawnVanillaPig(t, x,y,z)   # was: NewEntity + newPigAI
       └─ spawnVanillaPig = t.spawnDeclaredMob(registry["vanilla_pig"], x,y,z) # the Phase-23 path
          (NewEntity entity.Pig → real pig attrs → buildAIFromDecl → starlarkGoals → entities.add)
  ─────────────────────────────────────────────────────────────────────────────
  TICK TIME (tick goroutine) — UNCHANGED driver; same tests assert same behavior
  ─────────────────────────────────────────────────────────────────────────────
   tickAI → serverAiStep → goalSelector.tick (MOVE/LOOK arbitration, UNCHANGED)
            → starlarkGoal.tick → starlark.Call(goal callback, handles)
                                     ├─ stroll: nav.path_to(...)        → setWantTarget → A* (UNCHANGED)
                                     ├─ look:   entity.set_look(...)    → e.headYaw/e.yaw (NEW seam)
                                     └─ around: entity.set_look(...)    → e.headYaw/e.yaw (NEW seam)
            → navigation.tick → moveEntity (swept collision, UNCHANGED)
   ✓ ai_mob_test.go / navigation_test.go assert the SAME wantTarget / facing / walk — now plugin-driven.
```

### Pattern 1: The validation gap IS the deliverable — extend the API faithfully

**What:** Before porting, MAP each goal's reads/writes to the existing handle surface. The three additions the Pig goals require but the Phase-23 handle lacks:

| Goal need (Go source) | Phase-23 handle has? | NEW extension (faithful, tick-owned, capability-gated) |
|-----------------------|----------------------|---------------------------------------------------------|
| SET `e.headYaw` + `e.yaw` toward a point (`lookAtPlayerGoal.tick`, `randomLookAroundGoal.tick`) | ✗ — only `move_to`/`set_velocity`/`set_attribute` | `entity.set_look(yaw, pitch=...)` (or `entity.look_at(x,y,z)`) → writes `e.headYaw`/`e.yaw`/`e.pitch` directly (these are tick-owned plain fields the tracker broadcasts, like the Go goal does). Gate: `capEntitiesWrite`. |
| READ the nearest PLAYER pos in range (`lookAtPlayerGoal.canUse` → `nearestPlayerWithin` scans `loop.players`) | ✗ — `world.entities_near` returns mob handles; players are NOT in the entityStore | `world.nearest_player(x, y, z, max_dist)` → scans `t.players` (the existing `nearestPlayerWithin` seam), returns `(x,y,z)` or `None`. Gate: `capWorldRead`. |
| DRAW from the mob's RandomSource in a faithful order (`canUse`/`getPosition`/`start` in all 3 goals) | ✗ — no RNG on the handle; Go uses package `rand.IntN`/`rand.Float32` | `entity.rand_int(n)` / `entity.rand_float()` backed by a PER-ENTITY seeded source (the `mob.getRandom()` analogue), so the draw order matches vanilla and is deterministic. Gate: none (a read of the mob's own RNG). |

**Why:** This mapping is the phase's reason to exist. The Go code reads/writes these directly because it IS the mob's owner; the plugin must reach them through the bridge, and the bridge does not expose them yet. **Surfacing these three gaps and closing them faithfully is the proof that "the API expresses real vanilla AI."** Each extension must obey the Phase-23 disciplines: a method on the existing handle, re-resolving the entity by id on the tick goroutine, routing a write through a tick-owned field/seam, capability-gated, `Freeze()` still a no-op.

### Pattern 2: Each goal → a declared goal, ported 1:1 against bytecode

**What:** For each of the three goals, write a Starlark callback set (`can_use`/`start`/`tick`/`stop`/`continue`) that is a literal re-expression of the Go goal — which is itself a cited 1:1 port of the jar class. The priority, the `flags`, the `canUse` condition, the `start`/`stop` side effects, the `tick` body, and **the RNG draw order** must all match. The declaration's `goal(priority=…, flags=[…], …)` carries the exact priority/flags from `Pig.registerGoals` (@6 `MOVE`, @7 `LOOK`, @8 `MOVE|LOOK`).

**Why:** A declared goal IS a `server.Goal` (Phase-23 `starlarkGoal`), arbitrated by the SAME `goalSelector`. So the ported goal composes with the unchanged Go arbitration identically — the LOOK goal and the MOVE goal lock their flags exactly as the Go `lookAtPlayerGoal`/`randomStrollGoal` did. Cite each class in the `.star` (`# ports net.minecraft.world.entity.ai.goal.LookAtPlayerGoal`), no GPL paste.

**Example (`lookAtPlayerGoal` re-expressed — the goal that NEEDS two new seams):**
```python
# ports net.minecraft.world.entity.ai.goal.LookAtPlayerGoal (26.2 jar; cf. server/ai_goals_passive.go)
LOOK_DIST = 6.0
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY

def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:   # canUse: roll < probability (DRAW)
        return False
    p = world.nearest_player(entity.x, entity.eye_y, entity.z, LOOK_DIST)  # NEW SEAM
    if p == None:
        return False
    _state[entity.id] = {"look": p, "time": 0}    # capture the look target (vanilla lookAt)
    return True

def look_start(entity, world, nav):
    _state[entity.id]["time"] = 40 + entity.rand_int(40)   # start: lookTime = 40 + rnd(40) (DRAW)

def look_tick(entity, world, nav):
    s = _state[entity.id]; lx, ly, lz = s["look"]
    entity.set_look(yaw_toward(lx - entity.x, lz - entity.z))   # NEW SEAM (was e.headYaw=e.yaw=yaw)
    s["time"] = s["time"] - 1

def look_continue(entity, world, nav):
    s = _state.get(entity.id)
    if s == None or s["time"] <= 0: return False
    lx, ly, lz = s["look"]
    d2 = (lx-entity.x)**2 + (ly-entity.y)**2 + (lz-entity.z)**2
    return d2 <= LOOK_DIST*LOOK_DIST                 # canContinueToUse: within distance²
```
> Note the plugin-side per-mob state (`_state[entity.id]`) replaces the Go goal's struct fields (`lookTime`, `lookX/Y/Z`). Phase-23's `starlarkGoal` carries NO per-goal Starlark state (it stores callables + caps), so the port keeps mutable per-mob state in a module-level dict keyed by `entity.id`. **This is an Open Question (§6): is a module-global dict frozen-safe across the tick boundary, or does the goal need a per-mob scratch handle?** The Starlark module globals freeze on load; a frozen dict cannot be mutated. The port likely needs either (a) a small per-mob mutable scratch the host provides, or (b) state carried in handle fields the host owns. SURFACE this — it may be a SECOND API gap the dogfood reveals.

### Pattern 3: SWAP — the plugin pig replaces `newPigAI` at its two spawn sites

**What:** `newPigAI()` is called in exactly two places (`grep` this session): `server/async.go:312` (natural spawn — `spawnCandidatesReady.applyTo`) and `server/debug.go:167` (the SULFUR_DEBUG spawn). The swap replaces both `pig.ai = newPigAI()` lines with a spawn through the vanilla-pig DECLARATION: `pig := t.spawnDeclaredMob(registry["vanilla_pig"], x, y, z)` (or a thin `spawnVanillaPig` wrapper). This requires the vanilla-pig plugin to be LOADED at server boot (a real `plugins/` load, not just a test fixture) so the registry has the declaration when a pig spawns.

**Why:** PLUGIN-04 says "behavior-identical to the Go-native path it REPLACES." Replacing the spawn path makes the plugin pig the ONLY pig — and then the EXISTING tests (`TestPigGoalSetRegistered`, `TestRandomStrollSetsTarget`, `TestLookAtPlayerFacesNearest`, `TestServerAiStepWalksToGoalTarget`, the `navigation_test.go` follow tests, `async_stress_test.go`, `spawner_test.go`) become the behavior-identical proof: they must pass against the plugin-driven pig. Some tests construct `newPigAI()` directly (`ai_mob_test.go`); the planner decides whether those become plugin-driven too or stay as a Go-reference oracle the plugin pig is asserted equal to (Open Q §2).

**Consideration (no-built-but-unwired):** after the swap, is `newPigAI()` + the three Go goal structs DEAD CODE? If the plugin pig fully replaces it, the Go goals become unused. Options: (a) DELETE the Go goals (the plugin IS the pig now — the cleanest dogfood, but loses the Go reference oracle); (b) KEEP them as the test oracle the plugin pig is proven equal to (behavior-identical = the plugin pig matches the Go pig's observable output). Recommend (b) for the phase's duration (the oracle proves identity), with a documented decision on whether the Go goals are retired after. SURFACE as Open Q §2.

### Pattern 4: Faithful RNG — the per-entity seeded source

**What:** Add a deterministic per-entity random source (the `mob.getRandom()` analogue) and expose `entity.rand_int(n)`/`rand_float()` over it. Replace the Go goals' package `rand.IntN`/`rand.Float32` with the same per-entity source. The draw ORDER in each ported `canUse`/`start`/`getPosition` must match the bytecode (e.g. stroll: `nextInt(interval)` THEN three `nextInt` for the offset; lookAround: one `nextDouble` for the heading then `nextInt(20)` for the time).

**Why:** Vanilla AI determinism + reproducibility depend on the per-entity source + draw order. The package RNG is shared and non-deterministic — the documented cause of `TestTickAIDrivesMobs` flaky (`STATE.md`) and a contributor to the `TestServerAiStepWalksToGoalTarget` flake. A faithful port REQUIRES this fix; doing it here both satisfies the 1:1 mandate and retires the flakes. (LegacyRandomSource bit-for-bit vs a seeded `rand/v2` — Open Q §4. For a passive ambient pig, a seeded `rand/v2` per entity is likely sufficient and far simpler; a bit-exact LegacyRandomSource matters only if exact vanilla sequences are asserted, which the existing tests do not.)

### Recommended Project Structure
```
server/
├── plugin_entity.go        # EXTEND: entityHandle.set_look (+ eye_y/head_yaw reads), rand_int/rand_float;
│                           #         worldHandle.nearest_player. (Additive methods; existing surface unchanged.)
├── plugin_mob_ai.go        # POSSIBLY EXTEND: per-entity RNG plumbed into the handle the starlarkGoal builds.
├── ai_goals_passive.go     # REFERENCE (the 1:1 oracle): the 3 Go goals the plugin re-expresses. Keep as
│                           #         the behavior-identical oracle (Open Q §2) OR retire after the swap.
├── ai_mob.go               # newPigAI — the build the swap replaces; keep as the oracle builder or retire.
├── async.go / debug.go     # SWAP: pig.ai = newPigAI()  →  spawnVanillaPig (the plugin-driven build).
├── plugin_entity_test.go   # NEW tests: set_look / nearest_player / rand_int faithful-draw-order.
├── plugin_mob_test.go      # NEW: the vanilla-pig declaration loads; its 3 goals arbitrate; behavior == Go pig.
└── (plugins or testdata)/vanilla_pig/{plugin.toml, main.star}   # the 1:1 Pig AI plugin (3 ported goals).
```

### Anti-Patterns to Avoid
- **Degrading the port to fit the current handle** (e.g. a LookAt goal that can't face a player because there's no `set_look`). Extend the API; that's the phase's job.
- **Changing the goal STRUCTURE / priorities / flags from the jar.** @6 MOVE, @7 LOOK, @8 MOVE|LOOK are bytecode-confirmed — do not renumber or merge.
- **Wrong RNG draw order** (drawing `getPosition` before the `nextInt(interval)` gate, or sharing a package RNG). Match the bytecode order; use the per-entity source.
- **A PARALLEL plugin pig alongside `newPigAI`.** SWAP — the plugin pig replaces it (no dead Go AI driving live mobs).
- **Porting the deferred goals** (`Float`/`Panic`/`Breed`/`Tempt`/`FollowParent`) whose preconditions are unbuilt.
- **`starlark.Call` per mob per tick unconditionally** (carried Phase-23 pitfall) — the interpreter fires only inside a RUNNING goal's callback.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| A declared goal that IS a `server.Goal` | A new "vanilla goal adapter" | The Phase-23 `starlarkGoal` + `buildAIFromDecl` (UNCHANGED) | The adapter + the declare-once registry already exist and are verified. A vanilla goal is just a declaration. |
| Goal arbitration (MOVE/LOOK locking) | A custom selector | The EXISTING `goalSelector` (a verified 1:1 `GoalSelector` port) | The ported goals claim flags exactly like the Go goals — same selector, same result. |
| Spawning the declared pig | A new spawn routine | The Phase-23 `spawnDeclaredMob` (the `newPigAI` analogue) at the swap sites | Already builds `NewEntity` + real attrs + `buildAIFromDecl` + `entities.add`. |
| Nearest-player lookup | A new player scan | The EXISTING `nearestPlayerWithin` (`ai_goals_passive.go`) exposed via `world.nearest_player` | The seam already exists (the Go LookAt goal uses it); wrap it, don't rewrite it. |
| Head/body facing math | A new yaw formula | The EXISTING `yawTowardDeg` (`ai_goals_passive.go`) behind `entity.set_look`/`look_at` | The MC-convention `atan2(-dx, dz)` is already ported + tested (`TestLookAtPlayerFacesNearest`). |
| The 1:1 goal logic | Inventing behavior | The jar bytecode (`javap -c -p`) + the cited Go port as the oracle | The mandate: every goal is a literal jar port, verified against bytecode (disassembled this session). |
| Per-entity determinism | A custom PRNG scheme | A seeded `math/rand/v2.Rand` per entity (or a ported `LegacyRandomSource`) | Faithful draw order + reproducibility; retires the documented RNG flakes. |

**Key insight:** Phase 24 writes almost NO new mechanical code. It writes: (1) THREE faithful handle extensions (`set_look`, `nearest_player`, per-entity RNG), (2) ONE `.star` plugin re-expressing three already-cited goals 1:1, and (3) the SWAP wiring at two spawn sites. Everything else (the selector, the nav, collision, attributes, the `starlarkGoal` adapter, the spawn path) ALREADY EXISTS and is unchanged. The risk is not volume — it's FIDELITY: each goal must match the bytecode (priority/flags/conditions/draw order), the extensions must be tick-owned + capability-gated + `-race` clean, and the swap must keep the existing tests green.

## Common Pitfalls

### Pitfall 1: Wrong RNG draw order / shared package RNG (the 1:1-critical trap)
**What goes wrong:** The ported goal draws from the random source in a different ORDER than vanilla (e.g. computing the stroll offset before the `nextInt(interval)` gate, or drawing the lookAround time before the heading), or it uses the shared package `math/rand/v2` instead of the per-entity source — producing non-vanilla sequences and non-deterministic, un-reproducible behavior.
**Why it happens:** The Go port already uses package `rand.IntN`/`rand.Float32` (a documented 1:1 debt — `STATE.md`: "mob AI non-deterministic (RNG/async-pool), needs a seeded source per the 1:1 mandate"). Re-expressing it in Starlark naively carries the same debt; and the draw order is easy to permute when the Go fields (`wantX` stashed in `canUse`) are restructured into Starlark.
**How to avoid:** Disassemble each goal's `canUse`/`start`/`getPosition` (done this session for `RandomStrollGoal.canUse`: `getRandom().nextInt(reducedTickDelay(interval))` THEN `getPosition()`). Expose a PER-ENTITY seeded source via `entity.rand_int`/`rand_float`; draw in the bytecode order. Add a test asserting the exact sequence for a known seed (a faithful-draw-order test).
**Warning signs:** `TestTickAIDrivesMobs` / `TestServerAiStepWalksToGoalTarget` still flaky; a goal's behavior differs run-to-run with the same seed; the offset is computed even when the interval gate would have returned false (an extra draw).

### Pitfall 2: The behavior-identical tests assert the OLD direct-field behavior
**What goes wrong:** `TestLookAtPlayerFacesNearest` asserts `e.headYaw == yawTowardDeg(...)` after the Go goal's `tick`. The plugin pig sets the same field via `entity.set_look`, but if the swap routes the test through the plugin path and the seam writes a *slightly* different value (e.g. only `yaw` not `headYaw`, or a gradual turn), the test fails — correctly flagging a non-faithful seam.
**Why it happens:** The Go goal sets `e.headYaw = yaw; e.yaw = yaw` (both, instantly). A `set_look(yaw)` seam must write BOTH the same way to be behavior-identical.
**How to avoid:** Make `entity.set_look` write exactly what the Go goal wrote (`headYaw` and `yaw`, instantly — the v1 non-gradual analogue). Keep the existing tests as the contract. If a test constructs `newPigAI()` directly, decide (Open Q §2) whether it stays a Go-reference oracle or becomes plugin-driven.
**Warning signs:** `TestLookAtPlayerFacesNearest` fails after the swap with a near-but-not-equal yaw; the pig faces but does not turn its head on the client.

### Pitfall 3: Per-mob goal state in a frozen Starlark global
**What goes wrong:** The ported goals need per-mob mutable state (stroll's stashed `wantX/Y/Z`, lookAt's `lookTime`+target, lookAround's `relX/relZ`+`lookTime`). The Go goals keep these as struct fields. A Starlark port keeps them in a module-level dict — but Starlark FREEZES module globals on load, so a frozen dict cannot be mutated at tick time, raising a dynamic error (which the `starlarkGoal.call` isolation logs + absorbs → the goal silently no-ops).
**Why it happens:** Phase-23's `starlarkGoal` stores callables + caps, NOT per-goal Starlark state (each spawned mob shares the frozen callables). There is no host-provided per-mob scratch yet.
**How to avoid:** This may be a SECOND API gap the dogfood reveals (Open Q §6). Options: (a) a per-mob mutable scratch the host threads into the callbacks (a 4th handle arg, or a field on the entity handle: `entity.state`); (b) keep tiny per-mob state on the Go side (the `starlarkGoal` gains typed scratch fields the seams read/write); (c) for stroll specifically, the want-target is already stored on the mob (`e.ai.wantTarget` via `nav.path_to`), so stroll needs NO extra state — only lookAt/lookAround need a countdown + captured target. Recommend a minimal per-mob scratch seam if the dogfood confirms it's needed.
**Warning signs:** A goal callback errors with "cannot mutate frozen dict"; lookAt's countdown never decrements (state write silently fails); the isolation log shows repeated frozen-write errors.

### Pitfall 4: Loading the vanilla-pig plugin at boot (not just in tests)
**What goes wrong:** The swap makes `spawnDeclaredMob(registry["vanilla_pig"], …)` the live spawn path, but the registry is only populated if the vanilla-pig plugin was LOADED. Phase 23 only loaded mob plugins in TEST fixtures (`testdata/mobplugins/`, isolated from the events fixture); there is no boot-time mob-plugin load wiring (Phase-23 23-02 "Next Phase Readiness": "wiring `spawnDeclaredMob` to `main()` … is a follow-up"). If the plugin isn't loaded at boot, the live pig spawn finds no declaration → nil-deref or no pig.
**Why it happens:** Phase 23 deliberately deferred the boot-time load (no-built-but-unwired — it had no live consumer). Phase 24 IS that consumer.
**How to avoid:** Wire a boot-time load of the vanilla-pig plugin (the host's `LoadDirWith` with the `declare_mob`/`goal` builtins injected, mirroring the test harness) BEFORE the first pig can spawn. Guard the swap sites: if the declaration is missing, fail loudly at boot (not silently at spawn). This boot-load wiring is itself a small deliverable of the phase.
**Warning signs:** A pig spawns in a test (fixture-loaded) but not in the live server; `registry["vanilla_pig"]` is nil at the swap site.

### Pitfall 5: `-race` needs CGO=1, ship binary needs CGO=0 (carried from Phase 21/23)
**What goes wrong:** `CGO_ENABLED=0 go test -race ./...` fails (`-race requires cgo`). The host has no gcc (the 23-01 summary hit this).
**How to avoid:** Two gates — ship build `CGO_ENABLED=0 go build ./...`; race test in the `golang:1.26` Docker image (CGO=1) `go test -race ./server/ ./plugin/...`. The new look/nearest-player/RNG seams + the swapped spawn path must be `-race` clean (they're tick-owned by construction — id-handles re-resolved on the owner, per-entity RNG owned by the tick).
**Warning signs:** `-race requires cgo` locally; a data race flagged on `e.headYaw`/`e.yaw` write or the per-entity RNG if a seam is touched off-tick.

### Pitfall 6: The known AI-nav flake gets blamed on Phase 24
**What goes wrong:** `TestServerAiStepWalksToGoalTarget` (`server/navigation_test.go`) intermittently fails under full-suite load — a PRE-EXISTING low-frequency AI-nav timing flake (`STATE.md` FLAKE note: passes 5/5 on re-run + in isolation + at the pre-Phase-22 parent commit; NOT a Phase-22 regression). After the swap, a full-suite run that trips this flake could be misattributed to Phase 24's plugin pig.
**Why it happens:** The flake is timing/async-pool-related (the late path rejoin), independent of who drives the goal. The RNG fix (Pitfall 1) may REDUCE it (less non-determinism) but the async-pool timing component is separate.
**How to avoid:** NOTE the flake explicitly in the plan; on a failure, re-run in isolation before attributing it. Phase 24's RNG-determinism fix is a candidate to HARDEN this test (the `STATE.md` note: "investigate + harden the AI-nav test … once the v4 phase chain completes") — but a residual async-timing flake is not a Phase-24 regression. Use `-count=1 -run TestServerAiStepWalksToGoalTarget` in isolation as the disambiguator.
**Warning signs:** A full-suite failure of `TestServerAiStepWalksToGoalTarget` that passes on isolated re-run.

## Code Examples

### The faithful look-mutate extension (the NEW seam LookAt/LookAround need)
```go
// Source: VERIFIED against entityHandle (server/plugin_entity.go) + the Go goals' direct field write
//         (e.headYaw = e.yaw = yaw, ai_goals_passive.go) + yawTowardDeg (the ported MC-convention yaw).
// entity.set_look(yaw, pitch=0.0): MUTATE the body+head facing — exactly what lookAtPlayerGoal.tick /
// randomLookAroundGoal.tick do (set headYaw AND yaw instantly; the v1 non-gradual LookControl analogue).
// Tick-owned plain fields the tracker auto-broadcasts; gated on capEntitiesWrite.
func (h *entityHandle) setLook(_ *starlark.Thread, b *starlark.Builtin,
    args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
    if !h.caps.has(capEntitiesWrite) {
        return nil, capError("entities.write")
    }
    var yaw float64
    pitch := 0.0
    if err := starlark.UnpackArgs(b.Name(), args, kwargs, "yaw", &yaw, "pitch?", &pitch); err != nil {
        return nil, err
    }
    e, err := h.resolve() // re-resolve on the tick goroutine (Pitfall: never a stale pointer)
    if err != nil {
        return nil, err
    }
    e.headYaw = float32(yaw) // EXACTLY what the Go goal wrote
    e.yaw = float32(yaw)
    e.pitch = float32(pitch)
    return starlark.None, nil
}
// + Attr cases "set_look" (mutate method), "head_yaw"/"eye_y" (reads), wired into AttrNames.
```

### The nearest-player read extension (LookAt's canUse needs it)
```go
// Source: VERIFIED against worldHandle (server/plugin_entity.go) + the EXISTING nearestPlayerWithin
//         seam (server/ai_goals_passive.go) the Go LookAt goal already uses.
// world.nearest_player(x, y, z, max_dist): READ the nearest PLAYER pos in range (players are NOT in
// the entityStore — they live in t.players; world.entities_near returns mobs only). Returns a (x,y,z)
// tuple or None. Gated on capWorldRead.
func (h *worldHandle) nearestPlayer(_ *starlark.Thread, b *starlark.Builtin,
    args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
    if !h.caps.has(capWorldRead) {
        return nil, capError("world.read")
    }
    var x, y, z, maxDist float64
    if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 4, &x, &y, &z, &maxDist); err != nil {
        return nil, err
    }
    // Reuse the EXISTING seam: nearestPlayerWithin scans the tick-owned t.players (the same data the
    // Go lookAtPlayerGoal.canUse reads). A pseudo-entity at (x,y,z) lets us reuse it, or factor a
    // pos-based variant — the planner picks; the SCAN is unchanged.
    px, py, pz, ok := nearestPlayerAt(h.t, x, y, z, maxDist)
    if !ok {
        return starlark.None, nil
    }
    return starlark.Tuple{starlark.Float(px), starlark.Float(py), starlark.Float(pz)}, nil
}
```

### The vanilla-pig declaration (the 1:1 plugin — stroll goal shown)
```python
# plugins/vanilla_pig/main.star — the 26.2 Pig AI re-expressed AS a plugin (1:1, no GPL paste).
# Ports the @6/@7/@8 passive subset of net.minecraft.world.entity.animal.pig.Pig.registerGoals
# (FloatGoal@0/PanicGoal@1/BreedGoal@3/TemptGoal@4/FollowParentGoal@5 deferred — preconditions unbuilt).
STROLL_INTERVAL = 120   # RandomStrollGoal.DEFAULT_INTERVAL
STROLL_H = 10           # DefaultRandomPos.getPos horizontal radius
STROLL_V = 7            # vertical radius

def stroll_can_use(entity, world, nav):     # ports RandomStrollGoal.canUse (draw order matches bytecode)
    if entity.rand_int(STROLL_INTERVAL) != 0:        # nextInt(reducedTickDelay(interval)) — DRAW 1
        return False
    dx = entity.rand_int(2*STROLL_H+1) - STROLL_H    # getPosition — DRAWS 2..4 (after the gate)
    dz = entity.rand_int(2*STROLL_H+1) - STROLL_H
    dy = entity.rand_int(2*STROLL_V+1) - STROLL_V
    nav.set_want(entity.x+dx, entity.y+dy, entity.z+dz)   # stash (vanilla wantedX/Y/Z) — see Open Q §6
    return True

def stroll_start(entity, world, nav):  nav.path_to(*nav.want())   # start = navigation.moveTo(wanted,speed)
def stroll_continue(entity, world, nav): return nav.has_path()    # canContinueToUse = !navigation.isDone()
def stroll_stop(entity, world, nav):   nav.stop()                 # stop = navigation.stop()

declare_mob("vanilla_pig", base_type="pig",
    attributes={"max_health": 10.0, "movement_speed": 0.25},   # the jar-exact pig attributes (Wave-1)
    goals=[
        goal(priority=6, flags=["MOVE"], tick=stroll_continue_noop, can_use=stroll_can_use,
             start=stroll_start, stop=stroll_stop),                       # @6 WaterAvoidingRandomStroll
        goal(priority=7, flags=["LOOK"], tick=look_tick, can_use=look_can_use,
             start=look_start, stop=look_stop),                           # @7 LookAtPlayer
        goal(priority=8, flags=["MOVE","LOOK"], tick=around_tick, can_use=around_can_use,
             start=around_start),                                         # @8 RandomLookAround
    ])
```
> Two API-shape notes the planner must resolve: (1) `goal()` REQUIRES a `tick` callable (Phase-23 `plugin_mob_decl.go`), but stroll's vanilla `tick` is empty (the nav does the work) — the port needs either a no-op tick or a Phase-24 relaxation making `tick` optional when `start`/`continue` suffice (Open Q §7). (2) `nav.set_want`/`nav.want` are NEW conveniences OR the stroll stash lives in per-mob scratch (Pitfall 3 / Open Q §6).

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| The Pig AI is hard-coded Go (`newPigAI` + 3 goal structs) | The Pig AI is a DECLARED `.star` plugin (3 ported goals) driving the SAME selector/nav | This phase | Proves the API expresses real vanilla AI; the plugin pig replaces the Go pig at its spawn sites. |
| The Phase-23 handle exposes pos/velocity/attr mutate + block/entities-near reads | + look mutation (`set_look`), nearest-PLAYER read, per-entity RNG | This phase | The three faithful extensions the dogfood revealed; the handle now covers the passive-AI surface. |
| Mob AI uses package `math/rand/v2` (non-deterministic, a documented flake) | Per-entity seeded RandomSource with a faithful draw order | This phase | 1:1 fidelity + reproducibility; retires `TestTickAIDrivesMobs` (and likely reduces the AI-nav flake). |
| Goal per-mob state = Go struct fields | Goal per-mob state = a host-provided scratch (TBD — Open Q §6) | This phase | May surface a SECOND API gap (frozen-global mutation) the dogfood closes. |

**Deprecated/outdated for our use:**
- Package-global RNG for mob AI (the documented 1:1 debt) — replaced by the per-entity seeded source.
- A PARALLEL plugin pig — replaced by the SWAP (the plugin pig is the only pig).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The three handle extensions (`set_look`, `nearest_player`, per-entity RNG) are SUFFICIENT to express the three passive Pig goals faithfully. Derived by mapping each Go goal's reads/writes (read in full) to the handle surface (read in full). | Architecture §Pattern 1 | LOW-MEDIUM — the per-mob-state gap (Pitfall 3 / Open Q §6) may be a 4th needed seam. Surfaced as an Open Q; the planner should validate the full read/write set per goal at planning. |
| A2 | `entity.set_look` writing `headYaw`+`yaw` instantly is behavior-identical to the Go goals (which do exactly that). Verified against `lookAtPlayerGoal.tick`/`randomLookAroundGoal.tick` this session. | Code Examples; Pitfall 2 | LOW — verified; the v1 Go goals are non-gradual, so an instant set IS the current behavior the tests assert. |
| A3 | Keeping the Go goals as the behavior-identical ORACLE (vs deleting them) is the safer phase posture. The swap makes the plugin pig live; the Go pig stays as the equality reference. | Architecture §Pattern 3; Open Q §2 | LOW — a posture recommendation, not a fact; the operator decides retire-vs-keep. |
| A4 | A seeded per-entity `math/rand/v2.Rand` is sufficient for the passive pig (no test asserts exact vanilla LegacyRandomSource sequences). The existing tests assert RANGES/behaviors, not exact sequences (read this session). | Pattern 4; Open Q §4 | LOW-MEDIUM — if a future test (or Phase 28 visual gate) needs bit-exact vanilla sequences, a `LegacyRandomSource` port is needed. Flagged in Open Q §4. |
| A5 | The vanilla-pig plugin must be loaded at BOOT (not just in tests) for the live swap to work, and Phase 23 left this load wiring as a follow-up. Verified against 23-02-SUMMARY "Next Phase Readiness" + the test-only `testdata/mobplugins` load. | Pitfall 4 | LOW — verified; the boot-load wiring is a known Phase-24 deliverable. |
| A6 | `goal()` requires a `tick` callable today (Phase-23 `plugin_mob_decl.go` errors if `tickFn == nil`), so the empty-tick stroll goal needs either a no-op tick or a relaxation. Verified by reading the builtin this session. | Code Examples note; Open Q §7 | LOW — verified; a small API-shape decision for the planner. |

## Open Questions

> These are the kickoff decisions for `/gsd-discuss-phase`. SURFACE, do not decide in the plan.

1. **The exact handle-extension surface.** Recommend the minimal faithful set: `entity.set_look(yaw, pitch=0)` + `entity.head_yaw`/`eye_y` reads (LOOK mutate), `world.nearest_player(x,y,z,max_dist)` (player read), `entity.rand_int(n)`/`entity.rand_float()` (per-entity RNG). LOCK these; defer any not-yet-needed (`entity.is_in_water`, `entity.age`, breeding/item reads — those belong to the deferred goals).
2. **SWAP vs PARALLEL, and retire-vs-keep the Go pig.** Recommend SWAP (the plugin pig replaces `newPigAI` at `async.go`+`debug.go`) with the Go goals KEPT as the behavior-identical oracle for the phase (the existing tests assert the plugin pig == the Go pig). Decide whether the Go goals are deleted AFTER the dogfood proves identity, or kept as a permanent reference.
3. **Where the vanilla-pig plugin lives + boot-load wiring.** A real `plugins/vanilla_pig/` loaded at server boot (the live swap needs it) vs a `testdata/` fixture (tests only). The live swap REQUIRES a boot load (Pitfall 4). Recommend a real boot-loaded plugin + a test fixture mirroring it.
4. **Per-entity RNG: a seeded `math/rand/v2.Rand` or a bit-exact `LegacyRandomSource` port?** Recommend seeded `rand/v2` per entity (sufficient for a passive ambient pig; far simpler; retires the flake). Port `LegacyRandomSource` only if bit-exact vanilla sequences must be reproduced (no current test needs that).
5. **`getPosition` fidelity:** keep the Go port's documented uniform ±10/±7 offset (the existing tests assert it), or port `DefaultRandomPos.getPos`'s angle-bias + walkable-node bias more faithfully? Recommend KEEP the existing simplification (the phase proves API-expressiveness + goal-structure 1:1, not a new terrain-pos algorithm; the nav already resolves walkability).
6. **Per-mob goal state across the frozen Starlark boundary** (Pitfall 3). Does a ported goal need host-provided per-mob mutable scratch (lookAt's countdown + captured target, lookAround's heading)? This may be a SECOND API gap the dogfood reveals. Recommend a minimal per-mob scratch seam IF the port confirms it's needed; stroll alone needs none (its want-target lives on the mob's nav).
7. **`goal()` requires a `tick` callable, but stroll's vanilla `tick` is empty.** Relax `goal()` to make `tick` optional when `start`/`continue` carry the behavior, or pass a no-op tick? Recommend making `tick` optional (the faithful stroll has no per-tick work — the nav does it).
8. **Which existing tests become the behavior-identical contract** (and which stay Go-reference). `ai_mob_test.go` constructs `newPigAI()` directly; `navigation_test.go`/`async_stress_test.go`/`spawner_test.go` drive spawned pigs. Recommend: the spawn-driven tests run against the plugin pig (the swap); the direct-`newPigAI` tests stay as the Go oracle the plugin pig is asserted equal to.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `go.starlark.net` | the goal callbacks + handle-extension builtins | ✓ (in go.mod) | `v0.0.0-20260613233743-8ba36ccb83fb` | — |
| Phase 23 (`declare_mob`/`goal`, `starlarkGoal`, `buildAIFromDecl`, `spawnDeclaredMob`, handles, caps) | the entire re-expression mechanism | ✓ (Phase 23 complete + verified 9/9) | in-repo | — |
| Phase 22 (`host.LoadDirWith` builtin injection) | the boot-time vanilla-pig load | ✓ | in-repo | — |
| `server` AI subsystem (`Goal`/`goalSelector`/`mobAI`/`serverAiStep`/`groundNavigation`/`moveEntity`/`nearestPlayerWithin`/`yawTowardDeg`) | the unchanged mechanical path + the seams the extensions wrap | ✓ (Phases 6/7/8) | in-repo | — |
| `level/attribute` `pigSupplier` (jar-exact pig attrs) | the declared pig's attributes | ✓ (Wave-1 SUB-ATTRIB fix) | in-repo | — |
| `temp/cache/26.2-inner.jar` + `javap` (Zulu 25) | the 1:1 bytecode verification of each goal | ✓ (confirmed working this session: `javap -c -p -classpath 26.2-inner.jar <fqcn>`) | jar present; Zulu 25.0.3 | — |
| C compiler (for `-race` only) | the Docker race gate | ✓ in CI (`golang:1.26`) | — | n/a — ship binary stays CGO=0 |

**Missing dependencies with no fallback:** None — every prerequisite (Phase 23 API, the AI subsystem, the jar, the pig attribute supplier) is present and verified.
**Missing dependencies with fallback:** None.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `.star` fixtures) |
| Config file | none (Go convention) |
| Quick run command | `go test ./server/ -run 'TestPigGoalSet|TestRandomStroll|TestLookAtPlayer|TestVanillaPig'` |
| Full suite (race) command | `docker run --rm -v "$PWD":/app -w /app golang:1.26 env CGO_ENABLED=1 go test -race ./server/ ./plugin/...` |
| Ship-build gate | `CGO_ENABLED=0 go build ./...` |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| PLUGIN-04 | The vanilla-pig `.star` declares 3 goals at the jar priorities (@6 MOVE, @7 LOOK, @8 MOVE\|LOOK); load captures them | unit | `go test ./server/ -run TestVanillaPigDeclaresGoalSet` | ❌ Wave 0 |
| PLUGIN-04 | `entity.set_look(yaw)` writes `headYaw`+`yaw` exactly like the Go goal (behavior-identical seam) | unit | `go test ./server/ -run TestSetLookSeam` | ❌ Wave 0 |
| PLUGIN-04 | `world.nearest_player` returns the nearest player pos in range (and None when none) — the LookAt seam | unit | `go test ./server/ -run TestNearestPlayerSeam` | ❌ Wave 0 |
| PLUGIN-04 | `entity.rand_int`/`rand_float` draw from the PER-ENTITY seeded source in the bytecode draw order (deterministic for a seed) | unit | `go test ./server/ -run TestEntityRandFaithfulDrawOrder` | ❌ Wave 0 |
| PLUGIN-04 | The ported stroll goal sets a wantTarget within ±10/±7 and holds MOVE — SAME as `TestRandomStrollSetsTarget`, now plugin-driven | unit | `go test ./server/ -run TestVanillaPigStrollSetsTarget` | ❌ Wave 0 |
| PLUGIN-04 | The ported lookAt goal faces the nearest player (sets headYaw via the seam) — SAME as `TestLookAtPlayerFacesNearest`, plugin-driven | unit | `go test ./server/ -run TestVanillaPigLooksAtPlayer` | ❌ Wave 0 |
| PLUGIN-04 | **BEHAVIOR-IDENTICAL:** the plugin pig and the Go pig produce the SAME observable output (wantTarget / facing / walk) under the same seed + inputs | integration | `go test ./server/ -run TestPluginPigEqualsGoNativePig` | ❌ Wave 0 |
| PLUGIN-04 | The SWAP: a naturally/debug-spawned pig is now plugin-driven and still WALKS via the real nav (the existing nav tests pass against it) | integration | `go test ./server/ -run 'TestNavigationFollow|TestServerAiStepWalksToGoalTarget'` | ✓ (existing — must stay green after swap) |
| PLUGIN-04 | The existing pig-AI suite passes against the plugin-driven path (no regression) | regression | `go test ./server/ -run 'TestPig|TestRandomStroll|TestLookAt|TestTickAIDrivesMobs'` | ✓ (existing) |
| PLUGIN-04 | The plugin-driven pig tick (look/nearest-player/RNG seams + the swap) is `-race` clean | race | `docker … CGO_ENABLED=1 go test -race ./server/ ./plugin/...` | ❌ Wave 0 |
| PLUGIN-04 | RNG-determinism fix retires the flake: `TestTickAIDrivesMobs` is deterministic for a seed | unit | `go test ./server/ -run TestTickAIDrivesMobs -count=5` | ✓ (exists; currently flaky — must become deterministic) |
| PLUGIN-04 | Default binary is CGO=0 (no new cgo dep) | build | `CGO_ENABLED=0 go build ./...` | ✓ (gate exists) |
| PLUGIN-04 | Each ported goal matches the jar (priority/flags/conditions/draw order) | manual+oracle | `javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.entity.ai.goal.{RandomStrollGoal,LookAtPlayerGoal,RandomLookAroundGoal}` + diff vs the `.star`/Go oracle | ✓ (jar present) |

**The signature test** (proves the dogfood, not just the plumbing): `TestPluginPigEqualsGoNativePig` — spawn a Go-native pig (`newPigAI`) and a plugin-driven pig (`spawnDeclaredMob(vanilla_pig)`) with the SAME seeded RNG and the SAME world/player inputs, drive both through `serverAiStep`/`tickOnce` for N ticks, and assert their observable outputs (wantTarget sequence, facing, position) are IDENTICAL. This is the literal "behavior-identical to the Go-native path it replaces" requirement (PLUGIN-04) — and it only passes if (a) the goals are ported 1:1 (same logic), (b) the RNG draw order matches (the per-entity seeded source), and (c) the new seams write exactly what the Go goals wrote.

### Sampling Rate
- **Per task commit:** `go test ./server/ -run 'TestVanillaPig|TestSetLook|TestNearestPlayer|TestEntityRand'`
- **Per wave merge / phase gate:** the Docker `-race` image (CGO=1) green over `./server/ ./plugin/...` AND `CGO_ENABLED=0 go build ./...` AND the FULL existing pig-AI suite green against the swapped path.
- **Phase gate (full):** `TestPluginPigEqualsGoNativePig` green + the existing `ai_mob_test.go`/`navigation_test.go` green against the plugin pig + the jar-diff of each goal confirmed.

### Wave 0 Gaps
- [ ] `plugins/vanilla_pig/{plugin.toml, main.star}` (+ a `testdata/` mirror) — the 1:1 Pig AI plugin (3 ported goals, each cited).
- [ ] `server/plugin_entity.go` extensions: `set_look` + `head_yaw`/`eye_y` reads, `nearest_player`, `rand_int`/`rand_float` (+ the per-entity RNG plumbing).
- [ ] `server/plugin_entity_test.go` additions: `TestSetLookSeam`, `TestNearestPlayerSeam`, `TestEntityRandFaithfulDrawOrder`.
- [ ] `server/plugin_mob_test.go` additions: `TestVanillaPigDeclaresGoalSet`, `TestVanillaPigStrollSetsTarget`, `TestVanillaPigLooksAtPlayer`, `TestPluginPigEqualsGoNativePig`.
- [ ] The SWAP at `server/async.go` + `server/debug.go` (+ the boot-time vanilla-pig load wiring).
- [ ] The per-entity RNG determinism fix (replacing package `rand` in the AI path) — retires `TestTickAIDrivesMobs` flaky.
- [ ] A `-race` test for the plugin-driven pig tick (the new seams + the swap).

*(No framework install — Go stdlib `testing` + the existing `.star` load harness from Phase 23.)*

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V5 Input Validation / untrusted-code execution | yes | Phase-21/23 sandbox carries: each goal callback runs on a fresh budget-bounded thread (`SetMaxExecutionSteps`); the new `set_look`/`nearest_player`/`rand_*` seams validate args (`UnpackArgs`) + re-resolve the entity (a removed-entity call errors cleanly). The vanilla-pig declaration is validated at load (base_type/flags/attrs). |
| V1.4 Trust boundaries / least privilege | yes (carried) | The new seams are capability-gated: `set_look` → `capEntitiesWrite`; `nearest_player` → `capWorldRead`; `rand_*` → none (the mob's own RNG). The vanilla-pig plugin's manifest declares exactly `entities.read`, `entities.write`, `world.read`, `nav` (NOT `world.write` — a pig AI never sets blocks). |
| V6 Cryptography | no | None — the RNG is a gameplay PRNG (seeded `math/rand/v2`), explicitly NOT cryptographic; never use it for anything security-relevant. |
| V2/V3/V4 Auth/Session/Access | no | No network/auth surface; plugins are operator-installed local files. |

### Known Threat Patterns for the vanilla-pig dogfood
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| A goal callback infinite-loops / hangs the tick | Denial of Service | Per-callback `SetMaxExecutionSteps` (Phase-21) → `*EvalError`, the tick continues. |
| A goal callback errors and kills the tick | Denial of Service | `starlarkGoal.call` logs + absorbs (Phase-23) + the `tickOnce` recover backstop. |
| The new look/nearest-player/RNG seam leaks a live `*Entity` off-tick | Tampering / data race | The seams are methods on the id-handle, re-resolving on the tick goroutine (Phase-23 discipline); the per-entity RNG is tick-owned. Docker `-race` covers the plugin-driven pig tick. |
| `set_look` writes a NaN/garbage yaw (bad facing / encoder panic) | Tampering | Validate/clamp the yaw (the wire byte-angle pack already wraps via `Mth.packDegrees`); the Go goal's `yawTowardDeg` never produces NaN for a finite vector. |
| A pig-AI plugin with `world.write` griefs blocks | Tampering | Least-privilege manifest: the vanilla-pig plugin declares NO `world.write`; `set_block` denial is enforced (Phase-23 capability gate, verified). |
| Non-deterministic RNG makes behavior un-reproducible (a correctness/audit gap, not an attack) | (Tampering-adjacent / correctness) | The per-entity seeded source makes the AI deterministic + reproducible — the 1:1 fix that also retires the flake. |

## Sources

### Primary (HIGH confidence)
- **`D:/ender/server/ai_mob.go`** (read in full) — `mobAI`, `serverAiStep` (the jar-confirmed order, targetSelector skipped for the passive pig), `newPigAI` (the @6/@7/@8 subset + the deferral note), `setWantTarget`/`clearWantTarget`, `pigWalkSpeed`. The build the swap replaces. `[VERIFIED]`
- **`D:/ender/server/ai_goal.go`** (read in full) — the `Goal` interface, `baseGoal`, `goalSelector` flag-locking arbitration (MOVE/LOOK/JUMP/TARGET, `canBeReplacedBy`). The unchanged selector the ported goals arbitrate through. `[VERIFIED]`
- **`D:/ender/server/ai_goals_passive.go`** (read in full) — the THREE concrete Pig goals (`randomStrollGoal`/`lookAtPlayerGoal`/`randomLookAroundGoal`): each `canUse`/`canContinueToUse`/`start`/`stop`/`tick`, the package-`rand` usage, `nearestPlayerWithin` (the player seam), `yawTowardDeg` (the facing math), the per-goal struct state. The exact port menu. `[VERIFIED]`
- **`D:/ender/server/plugin_mob_decl.go`** + **`plugin_mob_ai.go`** (read in full) — the Phase-23 `declare_mob`/`goal` capture, `mobRegistry`, `seedAttributes`, the base-type resolver (pig is allowed + has a supplier), `starlarkGoal` (the `server.Goal` adapter), `buildAIFromDecl` (the `newPigAI` analogue), `spawnDeclaredMob`, the goal-requires-tick rule, `declaredWalkSpeed`. The unchanged re-expression mechanism. `[VERIFIED]`
- **`D:/ender/server/plugin_entity.go`** + **`plugin_capability.go`** (read in full) — the EXISTING handle surface (entity reads x/y/z/yaw/pitch/on_ground/type/velocity/health/attribute; mutates move_to/set_velocity/set_attribute; world block_at/set_block/entities_near; nav path_to/has_path) and the capability vocab. **Confirms the THREE gaps: no look mutate, no player read, no RNG.** `[VERIFIED]`
- **`D:/ender/server/ai_mob_test.go`** + **`navigation_test.go`** (read in full) — the existing pig-AI tests that become the behavior-identical contract: `TestRandomStrollSetsTarget`, `TestLookAtPlayerFacesNearest`, `TestServerAiStepOrder`, `TestPigGoalSetRegistered`, the nav-follow + async-rejoin tests, `TestServerAiStepWalksToGoalTarget` (the documented flake). `[VERIFIED]`
- **`D:/ender/server/async.go:283-316`** + **`debug.go:160-172`** (read this session) — the TWO `newPigAI()` spawn sites the swap replaces (`spawnCandidatesReady.applyTo` natural spawn; the SULFUR_DEBUG spawn). `[VERIFIED]`
- **`temp/cache/26.2-inner.jar`** via `javap -c -p` (disassembled this session) — `net.minecraft.world.entity.animal.pig.Pig.registerGoals` (the EXACT @0..@8 goal set + priorities + ctor args, bit-for-bit) and `net.minecraft.world.entity.ai.goal.RandomStrollGoal.canUse` (the `getRandom().nextInt(reducedTickDelay(interval))` → `getPosition()` DRAW ORDER). The 1:1 anchor. `[VERIFIED]`
- **`.planning/v4-PLAN.md`** (Phase-24 row + build-order rationale) + **`.planning/REQUIREMENTS.md`** (PLUGIN-04) + **the Phase-23 `23-RESEARCH.md`/`23-01-SUMMARY.md`/`23-02-SUMMARY.md`/`23-VERIFICATION.md`** (read in full) — the locked constraints, the declare-once/handle architecture, the SUB-ATTRIB coverage fix, the verified 9/9 Phase-23 outcome, the deferred plugin-facing-spawn + boot-load follow-ups. `[CITED]`
- **`.planning/STATE.md`** — the `TestServerAiStepWalksToGoalTarget` FLAKE note (pre-existing AI-nav timing flake, NOT a regression) + the `TestTickAIDrivesMobs` 1:1 RNG-determinism debt ("needs a seeded source per the 1:1 mandate"). `[CITED]`

### Secondary (MEDIUM confidence)
- The per-mob-state-across-the-frozen-boundary gap (Pitfall 3 / Open Q §6) — inferred from the Phase-23 `starlarkGoal` storing no per-goal Starlark state + Starlark's freeze-on-load model; to be confirmed during the port.
- The exact `nearest_player` signature (pos-based vs pseudo-entity) — the `nearestPlayerWithin` seam is mob-based; a pos-based variant is a small factor, validated at planning.

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Port source (only the pig has Go AI; its 3 goals + `newPigAI`; the 2 swap sites): **HIGH** — every file read in full + `grep newXxxAI`/`grep newPigAI` this session.
- Jar anchor (the goal set/priorities + the stroll draw order): **HIGH** — disassembled directly from `26.2-inner.jar` this session.
- The validation gap + the three required extensions: **HIGH** — derived by mapping the Go goals' (read in full) reads/writes against the handle surface (read in full); the gaps are concrete (no `set_look`, no player read, no RNG on the handle).
- The per-mob-state gap (a possible 4th extension): **MEDIUM** — surfaced as Open Q §6; to be confirmed when the port is written.
- Swap mechanics + behavior-identical proof: **MEDIUM** — the SWAP sites are verified; whether the direct-`newPigAI` tests become plugin-driven vs stay oracles is an operator decision (Open Q §2/§8).
- RNG approach (seeded `rand/v2` vs `LegacyRandomSource`): **MEDIUM (deliberately deferred to Open Q §4)** — seeded `rand/v2` is recommended + sufficient for the passive pig; bit-exact only if a test demands it.

**Research date:** 2026-06-28
**Valid until:** ~30 days. The stable anchors to re-grep at execution (line numbers may drift): `newPigAI`, `randomStrollGoal`/`lookAtPlayerGoal`/`randomLookAroundGoal`, `serverAiStep`, `goalSelector`, `nearestPlayerWithin`, `yawTowardDeg`, `spawnDeclaredMob`/`buildAIFromDecl`/`starlarkGoal`, the `entityHandle`/`worldHandle`/`navHandle` surfaces, the two `pig.ai = newPigAI()` swap sites. Re-disassemble `Pig.registerGoals` + the three `ai.goal.*` classes against `temp/cache/26.2-inner.jar` before writing each port (the 1:1 mandate).
