---
phase: 24-vanilla-mobs-as-plugins
plan: 02
subsystem: plugins
tags: [plugins, starlark, mob-ai, 1to1-port, dogfood, capability, embed, boot-load, behavior-identical]

requires:
  - phase: 24-01
    provides: "per-entity seeded RandomSource (e.ai.rng) + set_look/rand_int/rand_float/nearest_player seams + the requiresUpdateEveryTick flag"
  - phase: 23
    provides: "declare_mob/goal capture + starlarkGoal + buildAIFromDecl + spawnDeclaredMob + the entity/world/nav handles + capability enforcement"
provides:
  - "plugins/vanilla_pig — the FIRST 1:1 vanilla-mob plugin (3 passive goals re-expressed 1:1, each citing its net.minecraft.world.entity.ai.goal.* class)"
  - "The boot-load: //go:embed the bundled plugin + LoadVanillaPigRegistry into a tick-owned registry (loud failure if absent)"
  - "The SWAP: spawnVanillaPig replaces newPigAI at both spawn sites (async.go natural, debug.go SULFUR_DEBUG); the plugin pig is the only pig"
  - "Two API-fidelity fixes: requires_update_every_tick threaded into starlarkGoal (FIDELITY GAP 1); per-(mob,goal) get_state/set_state scratch (FIDELITY GAP 2)"
  - "New handle seams: entity.set_look_at (LookControl.setLookAt analogue), entity.rand_double (nextDouble), nav.stop (navigation.stop); goal() tick-optional + can_continue kwarg"
  - "go.starlark.net standard math module added to the sandbox allowlist (pure, no IO)"
  - "TestPluginPigEqualsGoNativePig — the behavior-identical proof (plugin pig == Go oracle over 500 ticks)"
affects:
  - "Future mob ports (the declare_mob/goal/handle API is now proven to express real vanilla AI; the deferred goals' prerequisites — mob jump control + fluid detection, a mob damage source, entity aging/breeding, held-item tags — are the next subsystems)"

tech-stack:
  added:
    - "go.starlark.net/lib/math (the standard numeric module) exposed in the sandbox allowlist"
  patterns:
    - "1:1 vanilla-goal port AS a Starlark declaration (each goal cites its ai.goal.* class; bytecode-verified draw order/flags/constants)"
    - "Per-(mob,goal) mutable scratch (get_state/set_state over a goal-owned float64 map) — the frozen-Starlark-boundary fix for goal struct fields"
    - "//go:embed a bundled plugin + materialize-to-temp + LoadDirWith for a boot-loaded, always-present plugin (loud-fail if absent)"
    - "The SWAP-with-oracle: the Go-native builder is KEPT as the behavior-identical comparison oracle, not deleted, while the plugin is the only live spawn"

key-files:
  created:
    - plugins/vanilla_pig/main.star
    - plugins/vanilla_pig/plugin.toml
    - server/assets/vanilla_pig/main.star
    - server/assets/vanilla_pig/plugin.toml
    - server/vanilla_pig.go
    - server/vanilla_pig_embed.go
    - server/plugin_pig_test.go
    - server/plugin_pig_race_test.go
    - .planning/phases/24-vanilla-mobs-as-plugins/deferred-goals.md
  modified:
    - server/plugin_mob_decl.go
    - server/plugin_mob_ai.go
    - server/plugin_entity.go
    - plugin/starlark/runtime.go
    - server/async.go
    - server/debug.go
    - server/tick.go
    - cmd/sulfur/main.go
    - server/physics_test.go
    - server/async_stress_test.go

key-decisions:
  - "Build the 3 passive goals 1:1 (Stroll@6/LookAt@7/LookAround@8 — all seams exist); DEFER all 5 new goals (Float@0/Panic@1/Breed@3/Tempt@4x2/FollowParent@5) with cited reasons — their prerequisites (mob jump control+fluid, mob damage source, aging+breeding, held-item tags) are unbuilt; building them would be built-but-unwired or faked"
  - "entity.set_look_at(x,y,z) is MORE faithful than set_look(yaw): the jar goals call LookControl.setLookAt(world point); the host derives the SAME yawTowardDeg the Go oracle uses"
  - "entity.rand_double is a DISTINCT seam from rand_float: RandomLookAround.start draws nextDouble for the heading — a nextFloat substitute would diverge the RNG stream and break behavior-identity"
  - "Embed the bundled plugin (//go:embed) so it is ALWAYS in the binary; a swapped-on-disk plugins/ copy cannot widen the swap's caps (the embedded manifest governs)"
  - "KEEP newPigAI + the 3 Go goals as the behavior-identical oracle for this phase (TestPluginPigEqualsGoNativePig asserts plugin == Go), not deleted"

patterns-established:
  - "Per-(mob,goal) get_state/set_state scratch — the host-owned mutable bag a Starlark goal callback uses across the frozen-module boundary"
  - "set_look_at / rand_double — faithful host seams that keep a ported goal a literal port (LookControl.setLookAt, RandomSource.nextDouble)"
  - "boot-load an embedded plugin into a tick-owned registry before tick.Run, fail loudly if the declaration is absent"

requirements-completed: [PLUGIN-04]

duration: 25min
completed: 2026-06-28
---

# Phase 24 Plan 02: The vanilla Pig AI re-expressed AS a 1:1 Starlark plugin (the FIRST dogfood), boot-loaded + swapped in as the only pig Summary

**The vanilla Pig's 3 passive goals (WaterAvoidingRandomStroll@6, LookAtPlayer@7, RandomLookAround@8) re-expressed 1:1 in `plugins/vanilla_pig/main.star` against the 26.2 jar bytecode, boot-loaded via `//go:embed`, and SWAPPED in as the only pig — proven behavior-identical to the Go-native pig it replaces by `TestPluginPigEqualsGoNativePig` (identical wantTarget/yaw/position over 500 ticks from the same seed), Docker -race clean.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-06-28T06:37:35Z
- **Completed:** 2026-06-28T~07:02Z
- **Tasks:** 3
- **Files modified/created:** 19

## Accomplishments

- **The 3 passive goals ported 1:1** — each goal block in `main.star` cites its `net.minecraft.world.entity.ai.goal.*` class; the priorities (@6/@7/@8), flags (MOVE / LOOK / MOVE|LOOK), draw order, and numeric constants were re-disassembled from `temp/cache/26.2-inner.jar` this session BEFORE writing and matched to the bytecode.
- **Per-new-goal build-OR-defer audit** (`deferred-goals.md`) — all 5 new goals (Float@0, Panic@1, Breed@3, Tempt@4×2, FollowParent@5) DEFERRED with cited jar classes + the exact missing subsystem + codebase grep evidence. No silent skip, no fake.
- **Two API-fidelity fixes** the dogfood surfaced and closed: (1) `requires_update_every_tick` threaded from the goal declaration into `starlarkGoal` (RandomLookAround's `requiresUpdateEveryTick()==true`, javap-confirmed — the 24-01 flag); (2) a per-(mob,goal) `get_state`/`set_state` scratch seam (the frozen-Starlark-boundary fix for `lookTime`/`relX`/`relZ`/`lookAt`).
- **The boot-load + SWAP** — the bundled plugin is `//go:embed`'d, loaded at boot into a tick-owned registry (loud-fail if absent), and `spawnVanillaPig` replaces `newPigAI()` at BOTH spawn sites. The plugin pig is the only pig; `newPigAI` + the 3 Go goals are KEPT as the comparison oracle.
- **The behavior-identical GATE** — `TestPluginPigEqualsGoNativePig` drives a Go pig and a plugin pig (same id-derived seed, same world/player) through `serverAiStep` for 500 ticks and asserts the IDENTICAL observable sequence (wantTarget, yaw, position). Green. The existing pig-AI suite (`TestServerAiStep*`, `TestPigGoalSetRegistered`, spawner/async-stress/nav) passes against the swapped path.
- **Docker -race clean** over `./server/ ./plugin/...` — the swapped plugin-pig tick + the per-entity RNG + the new seams are race-clean.

## Task Commits

1. **Task 1: port the 3 passive goals 1:1 + the 2 API-fidelity fixes** — `56b2f88a` (feat)
2. **Task 2: boot-load + SWAP the plugin pig + the behavior-identical gate** — `a66659b7` (feat)
3. **Task 3: the swapped-pig Docker -race gate** — `4a61e648` (test)

## The 1:1 anchor (javap-verified this session, BEFORE writing)

Disassembled from `temp/cache/26.2-inner.jar` via `javap -c -p`:

| Goal (jar class) | @ | flags | canUse / start draw order (bytecode) | port |
|------------------|---|-------|--------------------------------------|------|
| `Pig.registerGoals` | — | — | @0 Float, @1 Panic(1.25), @3 Breed(1.0), @4 Tempt×2, @5 FollowParent(1.1), @6 WaterAvoidingRandomStroll(1.0), @7 LookAtPlayer(Player,6.0f), @8 RandomLookAround | the @6/@7/@8 subset ported; @0/@1/@3/@4/@5 deferred |
| `WaterAvoidingRandomStrollGoal` / `RandomStrollGoal` | 6 | MOVE | canUse: `nextInt(reducedTickDelay(interval))` gate **[DRAW 1]** → `getPosition()` = 3 `nextInt` offsets **[DRAWS 2,3,4]**; start = `navigation.moveTo`; continue = `!isDone`; tick EMPTY | stroll@6 |
| `LookAtPlayerGoal` | 7 | LOOK | canUse: `nextFloat() < probability` → `getNearestPlayer`; start: `lookTime = adjustedTickDelay(40 + nextInt(40))`; continue: alive && `distanceToSqr <= dist²` && `lookTime>0`; tick: `setLookAt(point)` THEN `lookTime--` | lookAt@7 |
| `RandomLookAroundGoal` | 8 | MOVE\|LOOK | canUse: `nextFloat() < 0.02f`; start: `d = 2π·nextDouble()` **[DRAW 1]** → `relX=cos(d)`,`relZ=sin(d)` → `lookTime = 20 + nextInt(20)` **[DRAW 2]**; `requiresUpdateEveryTick()==true`; continue: `lookTime >= 0`; tick: `lookTime--` THEN `setLookAt(x+relX, eyeY, z+relZ)` | lookAround@8 |

The plugin draws in the EXACT bytecode order using the per-entity seeded source (24-01), so a fixed seed reproduces the identical stream — which is what makes `TestPluginPigEqualsGoNativePig` provable.

## The boot-load / bundling approach chosen

**`//go:embed` (not a plugins/-dir-only load).** The SWAP makes the plugin pig the only pig, so the declaration MUST always be present — an operator-deletable `plugins/` dir alone is insufficient. The bundled plugin (`server/assets/vanilla_pig/`) is embedded into the server binary; `LoadVanillaPigRegistry` materializes it to a temp dir, parses the manifest's least-privilege caps (`entities.read/write`, `world.read`, `nav` — NO `world.write`), stamps them via `setLoadCaps`, and `LoadDirWith` captures the declaration. A missing declaration after load is a FATAL boot error (Pitfall 4 / T-24-09). The canonical operator-facing copy also ships at `plugins/vanilla_pig/` (kept byte-identical to the embedded copy).

## The two API-fidelity fixes

1. **`requires_update_every_tick` (FIDELITY GAP 1):** added as an optional `goal()` kwarg captured into `goalDecl.requiresUpdateEveryTick`, overriding `starlarkGoal.requiresUpdateEveryTick()` (was hard-false from `baseGoal`). `TestVanillaPigGoalsPorted` asserts @8 carries it and @6/@7 do not.
2. **Per-(mob,goal) scratch (FIDELITY GAP 2):** `entity.get_state(key, default)` / `entity.set_state(key, value)` over a `map[string]float64` allocated per spawned mob per goal in `buildAIFromDecl` and threaded into the entity handle via `newEntityHandleWithScratch`. It replaces the Go goal's struct fields (`lookTime`/`lookX/Y/Z`, `relX/relZ/lookTime`). Tick-owned, never shared across mobs — Docker -race clean.

## Which existing tests were updated to the seeded-source expectation

**None needed updating** for the seeded-source value — the existing pig-AI suite passed UNCHANGED against the swapped path (the 24-01 seeded source already made them deterministic, and they assert behavior the swap preserves). The only test-file edits were **setup wiring**, not expectation changes: `newPhysicsLoop` and `newStressLoop` now install the boot-loaded vanilla_pig registry so the swap's `spawnVanillaPig` works in tests (`installVanillaPigRegistry`).

## TestPluginPigEqualsGoNativePig design

Two independent loops with identical worlds + an identical player. A Go-native pig (`newPigAI`, id 4242) and a plugin pig (`spawnVanillaPigWithID(4242, …)` — same id ⇒ same `reseedMobAI` seed ⇒ same RNG stream) are driven through `serverAiStep` for 500 ticks. Each tick, both pigs' pending async paths are block-drained to completion (`drainPendingPath`) so the async A* timing jitter — a scheduler artifact, NOT a behavior difference — is removed from the comparison; the path is identical for both (same want, same world). The observable tuple `{hasTarget, wantX/Y/Z, yaw, headYaw, x, y, z}` must be byte-identical every tick, or the test fails with the diverging tick. It passes only because the goals are ported 1:1, the draw order matches, and `set_look_at` writes exactly what the Go look goals wrote.

## The Docker -race result

`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...` → **clean** (server 15.6s, host 1.3s, starlark 1.0s). No flake-isolation re-run was needed — the full -race run was green on the first pass (the per-entity seeded source from 24-01 reduces the historical `TestServerAiStepWalksToGoalTarget` flake; it also passed 3/3 in isolation non-race).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `set_state` rejected Starlark ints**
- **Found during:** Task 2 (the gate tests)
- **Issue:** `look_start` stashes `40 + rand_int(40)` (a Starlark int), but `set_state` unpacked a strict `float64` → "got int, want float" errors that the goal-callback isolation absorbed, silently desyncing the RNG draw count between the two pigs and failing the equality test.
- **Fix:** `set_state` now coerces the value via `starlark.AsFloat` (accepts int or float; a non-number is a clean error).
- **Files modified:** server/plugin_entity.go
- **Verification:** TestPluginPigEqualsGoNativePig + TestVanillaPigLooksAtPlayer green; no callback errors logged.
- **Committed in:** a66659b7 (and the seam in 56b2f88a)

**2. [Rule 2 - Missing critical] `entity.rand_double` + the sandbox `math` module + `entity.set_look_at` + `nav.stop`**
- **Found during:** Task 1 (writing the plugin against the bytecode)
- **Issue:** Faithfully porting the goals surfaced FOUR missing seams the 24-01 set lacked: RandomLookAround.start draws `nextDouble()` (distinct from the existing `rand_float()`=nextFloat — a substitute diverges the stream); it needs `cos`/`sin` (no math in the sandbox); both look goals call `LookControl.setLookAt(point)` (the existing `set_look(yaw)` is less faithful); RandomStroll.stop calls `navigation.stop()` (no nav-clear seam). Without these the port could not be 1:1 or behavior-identical.
- **Fix:** added `entity.rand_double` (the `nextDouble` seam), exposed `go.starlark.net/lib/math` in the sandbox allowlist (pure numeric module, no IO/network — within the sandbox boundary), added `entity.set_look_at(x,y,z)` (the `LookControl.setLookAt` analogue; host derives the SAME `yawTowardDeg`), and `nav.stop()` (the `navigation.stop`/`clearWantTarget` analogue).
- **Files modified:** server/plugin_entity.go, plugin/starlark/runtime.go
- **Verification:** the gate tests + Docker -race green.
- **Committed in:** 56b2f88a

**3. [Rule 1 - Bug] the async-stress test went trivial after the swap**
- **Found during:** Task 2 (full suite)
- **Issue:** `TestAllAsyncSubsystemsRaceClean` builds its loop via `newStressLoop` (not `newPhysicsLoop`), which had no registry; the swapped natural spawner's `spawnVanillaPig` then panicked in the spawn applyTo, the tickOnce recover dropped the mob, and the test asserted "added no mobs".
- **Fix:** `newStressLoop` now installs the vanilla_pig registry (same as `newPhysicsLoop`).
- **Files modified:** server/async_stress_test.go
- **Verification:** TestAllAsyncSubsystemsRaceClean green (non-race + Docker -race).
- **Committed in:** a66659b7

---

**Total deviations:** 3 auto-fixed (1 blocking, 1 missing-critical seam set, 1 bug).
**Impact on plan:** All necessary for a faithful 1:1 port + behavior-identity. The new seams (rand_double, set_look_at, nav.stop, sandbox math, the get_state/set_state scratch) are exactly the "if vanilla AI doesn't fit the API, the API is wrong — caught here" deliverable the phase exists to produce. No scope creep.

## Issues Encountered

- **The equality test's async timing.** The first cut compared the two pigs with a plain non-blocking `applyAsyncResults`; the async A* rejoined at non-deterministic ticks for the two loops, so the position diverged purely from scheduler jitter (not behavior). Resolved by `drainPendingPath` — block-draining each pig's pending path to completion the same tick it is requested, removing the timing artifact so the comparison tests the goal/RNG/look LOGIC.

## Newing oracle retention

`newPigAI` + the 3 Go goal structs (`randomStrollGoal`/`lookAtPlayerGoal`/`randomLookAroundGoal`) are RETAINED as the behavior-identical ORACLE for this phase — `TestPluginPigEqualsGoNativePig` asserts the plugin pig matches them. They are no longer a live spawn path (both spawn sites call `spawnVanillaPig`). A future plan may retire them once the plugin pig is the established baseline; keeping them now is the cheapest 1:1 regression guard.

## Next Phase Readiness

- The declare_mob/goal/handle API is now PROVEN to express real vanilla AI (the dogfood succeeded). The next mob ports need the DEFERRED subsystems (`deferred-goals.md`): a mob JumpControl + fluid detection (Float), a mob damage pipeline + last-damage-source (Panic), entity aging/breeding (Breed/FollowParent), held-item + item-tag reads (Tempt).
- The embedded-plugin boot-load pattern is reusable for any future bundled 1:1 mob.

---
*Phase: 24-vanilla-mobs-as-plugins*
*Completed: 2026-06-28*

## Self-Check: PASSED

- plugins/vanilla_pig/main.star — FOUND
- server/vanilla_pig.go — FOUND
- server/vanilla_pig_embed.go — FOUND
- server/plugin_pig_test.go — FOUND
- server/plugin_pig_race_test.go — FOUND
- .planning/phases/24-vanilla-mobs-as-plugins/deferred-goals.md — FOUND
- Commit 56b2f88a (Task 1) — FOUND
- Commit a66659b7 (Task 2) — FOUND
- Commit 4a61e648 (Task 3) — FOUND
