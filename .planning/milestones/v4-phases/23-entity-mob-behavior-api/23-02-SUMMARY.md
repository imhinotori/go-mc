---
phase: 23-entity-mob-behavior-api
plan: 02
subsystem: api
tags: [starlark, plugin, mob-ai, declare-mob, goal-selector, navigation, custom-mob, capability]

# Dependency graph
requires:
  - phase: 23-01
    provides: "thin entityHandle/worldHandle (id-not-pointer), capSet + parseCapabilities, the SUB-ATTRIB per-type supplier coverage fix"
  - phase: 07-ai-pathfinding-commands-chat
    provides: "server.Goal + goalSelector flag-locking arbitration, mobAI/serverAiStep, groundNavigation.requestPath (the async A* seam), newPigAI"
  - phase: 08-async-optimizations
    provides: "the pathPool + pathReady async rejoin (OPT-01) the declared-mob nav uses unchanged"
  - phase: 22-plugin-host-event-bus
    provides: "host.Manager.LoadDirWith(root, extra) — the import-direction-A injection seam"
provides:
  - "declare_mob/goal load-time capture into a tick-readable mobRegistry (the declare-once invariant)"
  - "starlarkGoal: a server.Goal adapter arbitrated by the EXISTING goalSelector (not a bypass)"
  - "buildAIFromDecl (the newPigAI analogue: fresh *mobAI per spawn, shared frozen callables) + spawnDeclaredMob"
  - "navHandle (path_to -> setWantTarget, has_path) — the wander gate's mutate seam, capNav-gated"
  - "THE GATE: a Starlark wander mob (base_type pig) spawns, ticks, and MOVES via the Go nav, rendering as entity.Pig.ID"
affects: [24 vanilla-mob-as-plugin, 26 python-runtime]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "declare-once: a declaration captured ONCE at load into a tick-read registry; per-spawn mobAI references the SHARED frozen callables (per-mob struct state, shared frozen values)"
    - "starlark-goal-as-Goal: a Starlark goal and a Go goal are the SAME server.Goal, arbitrated by the SAME goalSelector flag-locking — the interpreter fires ONLY inside a running goal's tick/canUse/start/stop"
    - "interpreter-only-at-the-active-seam: no starlark.Call in tickAI/tickPhysics/tickEntities; an idle declared mob makes 0 calls/tick (T-23-08)"
    - "callback isolation + DoS guard: every callback runs on a fresh budget-bounded thread (stepBudget) and its error is logged + absorbed (T-23-06/07)"
    - "import-direction-A: the server owns the registry + declare_mob/goal builtins and injects them via host.LoadDirWith(root, extra); the host just runs the body that captures into them"

key-files:
  created:
    - server/plugin_mob_decl.go
    - server/plugin_mob_ai.go
    - server/plugin_mob_test.go
    - server/plugin_mob_race_test.go
    - server/testdata/mobplugins/wandermob/plugin.toml
    - server/testdata/mobplugins/wandermob/main.star
  modified:
    - server/plugin_entity.go

key-decisions:
  - "starlarkGoal stores NO entity id/pointer — it reads e.id from the *Entity the selector passes each Goal method, so one goal instance correctly drives whatever entity it ticks for (and holds no live *Entity — T-23-09)"
  - "navHandle.has_path is a bound CALLABLE (nav.has_path()), matching the plugin call form; path_to is capNav-gated, has_path is an ungated read"
  - "the wander gate plugin lives in an ISOLATED testdata root (testdata/mobplugins), NOT testdata/plugins — the events fixture loads the whole testdata/plugins dir WITHOUT declare_mob/goal injected, so wandermob there would error at load"
  - "declaredWalkSpeed maps a declared movement_speed to a blocks/tick pace scaled off the pig baseline (a tunable, wire-irrelevant value — the gate mob is custom, free of the 1:1 mandate)"
  - "the -race test drives ONLY the legitimate owner+pathPool concurrency (the real cross-goroutine boundary); the entityStore is tick-owned (single-owner TICK-05), never read off-tick"

requirements-completed: [PLUGIN-03]

# Metrics
duration: 12min
completed: 2026-06-28
---

# Phase 23 Plan 02: Entity/Mob Behavior API — declare_mob + the Wander Gate Summary

**The PLUGIN-03 behavior layer: declare_mob/goal load-time capture, a starlarkGoal that slots into the EXISTING goalSelector arbitration (not a bypass), and THE GATE — a Starlark-declared wander mob (base_type pig) that spawns, ticks, and MOVES through the real Go nav, rendering as the pig wire id, with the interpreter firing only inside the active goal seam and the declared-mob tick path Docker -race clean.**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-06-28T05:26Z
- **Completed:** 2026-06-28
- **Tasks:** 3
- **Files modified:** 7 (6 created, 1 modified)

## Accomplishments

- **declare_mob/goal capture ONCE at load.** `declare_mob(name, base_type, attributes, goals)` and `goal(priority, flags, tick=…)` are server-owned Starlark builtins injected as `extra` into `host.Manager.LoadDirWith` (import-direction-A: the server owns the `mobRegistry` + builtins, the host runs the module body that captures into them). A declaration is captured exactly once at load into a tick-readable registry; the goal callables freeze with the module. An unknown `base_type`, an unknown flag, and a duplicate name all error LOUDLY at load (never a silent default), mirroring host's unknown-event rule.
- **Custom = behavior, not a new wire type.** The base-type resolver maps `base_type` to an EXISTING `data/entity.Entity` (pig/cow/.../silverfish — the living types with a real Wave-1 attribute supplier). `spawnDeclaredMob` is the `newPigAI` analogue: `NewEntity` copies the base type's wire id + AABB dims + attaches the per-type AttributeMap, `seedAttributes` overrides the declared attrs onto that real base, `buildAIFromDecl` attaches a fresh `*mobAI`, and `entities.add` puts it in the tick-owned store. The gate asserts `e.typ == entity.Pig.ID`.
- **A Starlark goal IS a server.Goal.** `starlarkGoal` embeds `baseGoal` and implements the full Goal contract (canUse/canContinueToUse/start/stop/tick/flags), registered via the EXISTING `goalSelector.addGoal` — arbitrated by the SAME flag-locking precedence logic the Go goals use. `buildAIFromDecl` mirrors `newPigAI` exactly: a fresh `starlarkGoal` per spawned mob (per-mob struct state) referencing the SHARED frozen goalDecl callables (Pattern 4); it never re-parses the plugin. The arbitration test proves two starlarkGoals competing for MOVE: the higher-precedence one runs and holds MOVE, the other is locked out.
- **The interpreter fires ONLY at the active seam.** `starlark.Call` lives ONLY inside `starlarkGoal`'s methods (which the selector invokes only for a running goal) — there is NO `starlark.Call` in `tickAI`/`tickPhysics`/`tickEntities`. An idle declared mob (its goal's `can_use` false, so it never runs) makes 0 starlark.Calls per tick; an active one makes ≥1 (`TestNoInterpreterWhenIdle`). Every callback runs on a fresh budget-bounded thread (the stepBudget runaway guard) and its error is logged + absorbed — one bad/hung/erroring callback never kills the tick (`TestGoalCallbackIsolation`).
- **THE GATE passes.** `TestWanderMobSpawnsAndMoves`: the wandermob plugin's MOVE goal nav-targets a fixed nearby point via `navHandle.path_to` (→ `setWantTarget` → the async A* `requestPath`, already wired); `serverAiStep`'s `navigation.tick` + `moveEntity` walk the mob. Driven through the full `tickOnce` pipeline, the mob's (x,z) changes by > 2 blocks via the real Go nav, still rendering as `entity.Pig.ID`.
- **Declared-mob tick is -race clean.** `TestDeclaredMobRace` drives the owner + pathPool concurrency (the only genuine cross-goroutine boundary — the off-tick A* compute over an immutable snapshot, rejoining via the id-carrying `pathReady`); Docker `-race` (CGO=1) over `./plugin/... ./server/` is green. Structural no-escape gates (`TestHandlesHoldNoLivePointer`, `TestStarlarkGoalHoldsNoLivePointer`) prove the handles + the starlarkGoal store an id, never a live `*Entity` (T-23-09).

## Task Commits

1. **Task 1: declare_mob/goal capture + spawnDeclaredMob** — `58dc2da1` (feat)
2. **Task 2: starlarkGoal implements Goal + arbitration + isolation + navHandle tests** — `dd2d58dd` (test)
3. **Task 3: THE GATE + idle-no-interpreter + the -race file** — `9d6f185a` (feat)

_The starlarkGoal/navHandle implementation landed in Task 1's commit (the package compiles as a unit — spawnDeclaredMob calls buildAIFromDecl); Task 2 adds its dedicated tests. This telescoping matches the 23-01 Task 2/3 pattern (interdependent handle code committed together)._

## Files Created/Modified

- `server/plugin_mob_decl.go` — `mobRegistry` + `mobDecl`/`goalDecl`, the `declare_mob`/`goal` builtins (load-time capture), the base-type resolver (`baseTypeByName`), the friendly-name attribute alias + `seedAttributes`, `goalValue` (the opaque goal wrapper), flag parsing, and `spawnDeclaredMob` (the newPigAI analogue)
- `server/plugin_mob_ai.go` — `starlarkGoal` (implements `server.Goal`, `var _ Goal = (*starlarkGoal)(nil)`), the fresh-thread + error-isolated `call`, and `buildAIFromDecl` + `declaredWalkSpeed`
- `server/plugin_entity.go` — `navHandle` (path_to/has_path bound methods, capNav-gated path_to) added (Wave 1 deferred it here)
- `server/plugin_mob_test.go` — the declare/spawn/reject suites, the starlarkGoal arbitration/isolation/only-when-running suites, the navHandle suite, THE GATE, and idle-no-interpreter
- `server/plugin_mob_race_test.go` — `TestDeclaredMobRace` (owner+pathPool -race) + the structural no-escape gates
- `server/testdata/mobplugins/wandermob/{plugin.toml,main.star}` — the trivial custom wander mob (declare_mob base_type pig, one MOVE goal using nav.path_to)

## Decisions Made

- **starlarkGoal reads e.id from the per-call `*Entity`, not a stored id.** Every Goal method receives the live `*Entity` from `serverAiStep`; the starlarkGoal builds its handles from `e.id` each call. So one goal instance correctly drives whatever entity the selector ticks it for, and it retains no live `*Entity` (the no-escape invariant T-23-09 holds by construction).
- **navHandle.has_path is a callable, not a read attribute.** The plugin writes `nav.has_path()`; an attribute returning a bare Bool would be called as a function and error. Both `path_to` and `has_path` are bound methods.
- **The wander plugin is isolated in `testdata/mobplugins`, NOT `testdata/plugins`.** The 22-02 events test loads the whole `testdata/plugins` dir WITHOUT the declare_mob/goal builtins injected, so wandermob there would error at load (undefined `declare_mob`). A separate root keeps both load paths clean.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] My -race test illegally read the tick-owned store off-tick**
- **Found during:** Task 3 (the Docker -race gate)
- **Issue:** The first `TestDeclaredMobRace` spun a second goroutine calling `loop.entities.near(...)` concurrently with `tickOnce`. The `entityStore` is single-owner (TICK-05, NOT concurrent), so the race detector correctly flagged a data race between the tick's re-bucket and the external read. The test itself violated the design's single-owner contract.
- **Fix:** Rewrote the test to drive ONLY the legitimate owner + pathPool concurrency — `tickOnce` submits the A* compute to the real pool goroutine over an immutable snapshot and the owner applies the id-carrying `pathReady`; that is the genuine cross-goroutine boundary in a declared-mob tick. No off-tick store read. The structural no-escape gates (reflect over the handle/goal structs) pin the *Entity-never-stored invariant statically.
- **Files modified:** server/plugin_mob_race_test.go
- **Verification:** Docker `-race -timeout 900s ./plugin/... ./server/` green
- **Committed in:** 9d6f185a

**2. [Rule 3 - Blocking] The wander plugin's path collided with the events fixture root**
- **Found during:** Task 1 (designing the load harness)
- **Issue:** The plan named `server/testdata/plugins/wandermob/`, but the existing 22-02 events test loads the entire `testdata/plugins` dir without injecting declare_mob/goal, so adding wandermob there would break that test at load (undefined `declare_mob`).
- **Fix:** Placed the wander plugin in an isolated `server/testdata/mobplugins/wandermob/` root. The `contains: declare_mob` artifact check is on main.star content (satisfied); the exact path is overridden by the isolation requirement.
- **Files modified:** (new) server/testdata/mobplugins/wandermob/{plugin.toml,main.star}
- **Verification:** both the events suite and the mob suite load cleanly; full server+plugin tests green
- **Committed in:** 58dc2da1

---

**Total deviations:** 2 auto-fixed (1 bug — my own test broke the single-owner contract; 1 blocking — testdata root collision). No scope creep; the plan's behavior is delivered exactly.

## Threat Model Coverage

All `mitigate` dispositions from the plan's `<threat_model>` are implemented and tested:
- **T-23-06 (runaway callback DoS):** every callback runs on a fresh `starlarkpkg.NewThread` (stepBudget) — a runaway loop returns an EvalError, the tick continues.
- **T-23-07 (callback panic/error kills the tick):** `starlarkGoal.call` logs + absorbs the error (`TestGoalCallbackIsolation`); the tickOnce recover backstop is the second line.
- **T-23-08 (per-mob-per-tick interpreter storm):** the interpreter fires ONLY inside a running goal's tick; `! grep starlark.Call tick_phases.go`; `TestNoInterpreterWhenIdle` proves idle = 0 calls.
- **T-23-09 (live *Entity escape):** the handles + starlarkGoal store an id, re-resolved on the owner; `TestHandlesHoldNoLivePointer`/`TestStarlarkGoalHoldsNoLivePointer` + the Docker -race run prove it.
- **T-23-10 (declared mob as a new wire id):** the base-type resolver maps to an EXISTING data/entity record; the gate asserts `e.typ == entity.Pig.ID`; an unknown base_type errors at load.
- **T-23-11 (mob-flood) — `accept`:** the Phase-23 gate spawns via the test/debug seam `spawnDeclaredMob`; NO plugin-facing spawn builtin is in scope (documented, deferred to a future plan that must respect the naturalSpawner CREATURE cap).

## Known Stubs

None that block the plan's goal. The single intentional deferral is the **plugin-facing spawn builtin** (threat T-23-11, disposition `accept` in the plan): a plugin cannot itself spawn a declared mob in Phase 23 — `spawnDeclaredMob` is the test/debug seam. A future plan adds a rate-limited plugin-facing spawn path under the existing CREATURE cap. This is the plan's intended scope boundary, not an incomplete feature.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0
- `CGO_ENABLED=0 go vet ./server/ ./plugin/...` — clean
- `CGO_ENABLED=0 go test ./server/ ./plugin/...` — green (declare/spawn/reject + arbitration/isolation/only-when-running + navHandle + THE GATE + idle-no-interpreter + the no-escape gates)
- Docker `-race -timeout 900s ./plugin/... ./server/` (CGO=1) — green
- `! grep starlark.Call server/tick_phases.go` — CLEAN (interpreter only at the goal-callback seam)
- `! grep 'import "C"' server/plugin_mob_decl.go server/plugin_mob_ai.go` — CLEAN (no cgo)
- `grep 'type starlarkGoal struct'` + `grep 'var _ Goal = (\*starlarkGoal)(nil)'` — both match
- `grep declare_mob server/testdata/mobplugins/wandermob/main.star` — matches

## Next Phase Readiness

- **Phase 24 (vanilla-mob-as-plugin) can build directly on this.** The starlarkGoal/goalSelector/nav chain is the architecture proof that the API can express real AI. Phase 24's vanilla mobs are subject to the 1:1 mandate (ported goal logic), but they ride the SAME `server.Goal` + arbitration + nav this plan validated with a custom mob.
- **Phase 26 (python-runtime)** inherits the declare_mob/goal capture contract — a Python runtime injects the same builtins into its loader seam.
- One follow-up noted for a later plan: wiring `spawnDeclaredMob` to `main()` (a plugins/ load that calls `loadMobPlugins` instead of/in addition to plain `LoadDir`) and a rate-limited plugin-facing spawn builtin under the CREATURE cap (T-23-11).

## Self-Check: PASSED

All created files exist on disk (server/plugin_mob_decl.go, server/plugin_mob_ai.go, server/plugin_mob_test.go, server/plugin_mob_race_test.go, server/testdata/mobplugins/wandermob/{plugin.toml,main.star}) and all three task commits (58dc2da1, dd2d58dd, 9d6f185a) are present in git history.

---
*Phase: 23-entity-mob-behavior-api*
*Completed: 2026-06-28*
