---
phase: 23-entity-mob-behavior-api
verified: 2026-06-28T06:10:00Z
status: passed
score: 9/9 must-haves verified
overrides_applied: 0
re_verification: null
---

# Phase 23: Entity/mob behavior API — Verification Report

**Phase Goal:** A declarative mob-behavior interface — a plugin declares a mob's attributes/goals/AI once, Go runs the hot path calling the declared hooks, with a full-override path and a frozen tick-safe entity/world/nav bridge (PLUGIN-03).
**Verified:** 2026-06-28T06:10:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

Verified the 4 ROADMAP success criteria (the contract) merged with the 5 task-supplied truths. All resolve to VERIFIED against the codebase — not against SUMMARY claims. Every gate was re-run live (not from cache), the Docker `-race` gate was executed, and the SUB-ATTRIB supplier values were spot-checked against the 26.2 jar bytecode directly.

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| R1 | A plugin DECLARES a mob's attributes/goals/AI at load; Go runs the hot path calling the declared hooks — NOT a script per-entity-per-tick | ✓ VERIFIED | `declare_mob`/`goal` builtins capture ONCE at load into `mobRegistry.byName` (plugin_mob_decl.go:259-302, 219-252); `tickAI`→`serverAiStep`→`goals.tick` (ai_mob.go:69-89, tick_phases.go:323-339) is the Go hot path; `starlark.Call` appears in NO tick body (grep: absent from tick_phases.go; present only in plugin_mob_ai.go goal-callback methods + tests). `TestNoInterpreterWhenIdle` PASS (idle mob = 0 calls). |
| R2 | The FULL-OVERRIDE path: a plugin replaces a mob's whole decision logic through declared seams | ✓ VERIFIED | `starlarkGoal` implements the full `Goal` interface (canUse/canContinueToUse/start/stop/tick/flags) via `baseGoal` embed + overrides (plugin_mob_ai.go:40-135); a declared goal owns all of MOVE arbitration through `goalSelector.addGoal`. The wander mob's MOVE goal fully drives the mob's movement decisions. |
| R3 | The Go-side bridge exposes entity/world/nav to Starlark as FROZEN, tick-owned-safe handles; read-only vs mutate-through-a-tick-owned-seam distinguished | ✓ VERIFIED | `entityHandle`/`worldHandle`/`navHandle` carry `id int32`+`*TickLoop`+`capSet`, NEVER `*Entity` (plugin_entity.go:33-37, 245-248, 360-364); `Freeze()` no-op; reads = frozen scalars via `Attr`, mutates = bound `*Builtin` through seams (setWantTarget/requestPath, direct vx/vy/vz, attribute.Map, ChunkManager.SetBlock+broadcast). `TestHandlesHoldNoLivePointer` reflect-asserts no `*Entity` field. PASS. |
| R4 | A trivial custom mob declared in Starlark spawns, ticks, and moves via the Go nav, Docker -race clean | ✓ VERIFIED | `TestWanderMobSpawnsAndMoves` PASS: base_type pig, 400 ticks, asserts `math.Hypot(Δ) > 2` via real `moveEntity`, `e.typ == entity.Pig.ID`. Docker `-race ./plugin/... ./server/` GREEN (15.5s server, exit 0). |
| 1 | SUB-ATTRIB coverage fix: per-type suppliers (1:1 jar) + LivingEntity fallback so NewMapForEntity returns a REAL map for any living type (pig != bare-default) | ✓ VERIFIED | defaults.go: 7 new suppliers (pig/cow/sheep/chicken/skeleton/creeper/spider) + `livingFallbackSupplier()`; `NewMapForEntity` returns supplier→living-fallback→nil (defaults.go:333-343). Jar-verified bit-for-bit: Pig=Animal+MH 10.0+MS 0.25; Cow=Animal+MH 10.0+MS 0.20000000298023224; Spider=Monster+MH 16.0+MS 0.30000001192092896 (javap on 26.2-inner.jar this session). `TestSupplierCoverage`/`TestLivingFallback`/`TestNewMapForEntity_NonLivingNil` PASS. |
| 2 | Thin handles wrap id+*TickLoop (NOT a live *Entity); HasAttrs reads; dead-entity read safe; Freeze no-op; mutates through tick-owned seams | ✓ VERIFIED | See R3. `TestEntityHandleReads`/`TestEntityHandleStale`/`TestHandleFreezeNoop`/`TestHandleMutators`/`TestNoRawPositionWrite` all PASS — stale read returns clean error not panic; move_to sets nav target without writing e.x. |
| 3 | Capability ENFORCEMENT: plugin without world.write calling set_block → Starlark error; with it → block changes | ✓ VERIFIED | plugin_capability.go: `parseCapabilities` rejects unknown loudly; `capError` surfaced per-op. `TestCapabilityEnforced_WorldWrite` PASS: denied→`mgr.GetBlock` confirms block UNCHANGED; allowed→block becomes stone (real ChunkManager data-flow, Level 4). Same enforced for entities.read/write, world.read, nav. |
| 4 | declare_mob captures once; starlarkGoal implements EXISTING Goal + goes through goalSelector arbitration (not a bypass); interpreter fires ONLY in a running goal (idle=0) | ✓ VERIFIED | `var _ Goal = (*starlarkGoal)(nil)` (plugin_mob_ai.go:52); registered via `m.goals.addGoal` (buildAIFromDecl:150-166). `TestStarlarkGoalArbitration` PASS (hi-precedence holds MOVE, lo locked out). `! grep starlark.Call tick_phases.go` confirmed. `TestNoInterpreterWhenIdle` PASS. |
| 5 | THE GATE: wander mob (base_type pig) spawns, ticks, position CHANGES via Go nav; renders as entity.Pig.ID | ✓ VERIFIED | See R4. Read TestWanderMobSpawnsAndMoves (plugin_mob_test.go:409-450): full pipeline tickOnce×400, |Δ|>2 via moveEntity, e.typ==entity.Pig.ID asserted before AND after walking. main.star uses `nav.path_to(entity.x+8.0, ...)`. PASS. |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `level/attribute/defaults.go` | per-type suppliers + living fallback | ✓ VERIFIED | `pigSupplier`…`spiderSupplier`, `livingFallbackSupplier`, `isLivingType`, rewritten `NewMapForEntity`. Jar-cited doc comments per supplier. |
| `server/plugin_entity.go` | entityHandle+worldHandle (+navHandle) | ✓ VERIFIED | id-not-pointer handles, HasAttrs reads, bound mutate methods through seams; navHandle added in Wave 2. |
| `server/plugin_capability.go` | capSet vocab + enforcement | ✓ VERIFIED | `capEntitiesRead..capNav`, `parseCapabilities`, `has`, `capError`. |
| `server/plugin_mob_decl.go` | mobRegistry + declare_mob/goal + spawnDeclaredMob | ✓ VERIFIED | registry, base-type resolver, seedAttributes, goalValue, flag parsing, spawnDeclaredMob (newPigAI analogue). |
| `server/plugin_mob_ai.go` | starlarkGoal (implements Goal) + buildAIFromDecl | ✓ VERIFIED | `type starlarkGoal struct` + `var _ Goal`; buildAIFromDecl mirrors newPigAI (fresh struct, shared frozen callables). |
| `server/testdata/mobplugins/wandermob/{plugin.toml,main.star}` | trivial custom mob | ✓ VERIFIED | declare_mob base_type pig, one MOVE goal using nav.path_to. (Path relocated from testdata/plugins to testdata/mobplugins — documented deviation to avoid the events-fixture load collision; the `contains: declare_mob` check holds on content.) |
| test files | handle/capability/declare/gate/race suites | ✓ VERIFIED | plugin_entity_test.go, plugin_mob_test.go, plugin_mob_race_test.go, level/attribute/defaults_test.go — all present, all pass live. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| NewMapForEntity | livingFallbackSupplier | supplier-miss + isLivingType | ✓ WIRED | defaults.go:333-343 |
| entityHandle.Attr | t.entities.get(id) | re-resolve per read | ✓ WIRED | plugin_entity.go:87, 127 |
| worldHandle.set_block | ChunkManager.SetBlock + broadcastBlockUpdate | world.write-gated | ✓ WIRED | plugin_entity.go:318-321 |
| starlarkGoal | goalSelector.addGoal | buildAIFromDecl | ✓ WIRED | plugin_mob_ai.go:154 |
| starlarkGoal.tick | starlark.Call(thread, tickFn, handles) | only while running | ✓ WIRED | plugin_mob_ai.go:72-80, 130-135 |
| declare_mob builtin | host.LoadDirWith extra injection | server-owned builtins | ✓ WIRED | plugin_mob_test.go:42-46 (load harness); server owns registry |
| tickAI | serverAiStep → navigation.tick → moveEntity | the unchanged Go nav | ✓ WIRED | tick_phases.go:337-339, ai_mob.go:79-88 |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
| --- | --- | --- | --- | --- |
| wander mob position | e.x/e.z | nav.path_to → setWantTarget → requestPath → navigation.tick → moveEntity | Yes — |Δ|>2 blocks asserted | ✓ FLOWING |
| set_block | world block state | ChunkManager.SetBlock | Yes — GetBlock confirms change/no-change | ✓ FLOWING |
| pig attributes | e.attributes.GetValue | NewMapForEntity(pig) → pigSupplier (jar values) | Yes — MH 12.0 override over real pig base, MS 0.25 not 0.7 | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| --- | --- | --- | --- |
| Ship build | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Vet | `go vet ./server/ ./level/attribute/ ./plugin/...` | clean | ✓ PASS |
| Phase-23 + full server suite | `go test ./server/` | ok | ✓ PASS |
| Attribute + plugin suites | `go test ./level/attribute/ ./plugin/...` | ok | ✓ PASS |
| THE GATE (live, -v) | `go test -run TestWanderMobSpawnsAndMoves -v` | --- PASS (0.01s) | ✓ PASS |
| Docker -race | `docker run golang:1.26 go test -race ./plugin/... ./server/` | ok (15.5s), exit 0 | ✓ PASS |
| Jar 1:1 check | `javap -c -p` Pig/AbstractCow/Spider.createAttributes | values match suppliers bit-for-bit | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| PLUGIN-03 | 23-01, 23-02 | Declarative entity/mob behavior API; declare-once; hot path; full-override; frozen tick-safe entity/world/nav handles; custom mob spawns/ticks/moves -race clean | ✓ SATISFIED | All 4 roadmap success criteria + 5 task truths verified above; the gate + Docker -race green. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| — | — | none | — | No TODO/FIXME/placeholder/stub-return found in the phase files. `return nil, nil` in handle `Attr` is the documented HasAttrs "no such field" contract, not a stub. The one accepted deferral (no plugin-facing spawn builtin; `spawnDeclaredMob` is the test seam) is documented in 23-02-SUMMARY "Known Stubs" and threat T-23-11 (disposition `accept`) — an intentional scope boundary, not a gap. |

### Human Verification Required

None. Phase 23 is autonomous/standalone-testable — every behavior (declared mob spawning, ticking, moving via the Go nav) is verifiable headless. The real-client visual gate is Phase 28 (per 23-VALIDATION Manual-Only section: "All Phase-23 behaviors are automatable").

### Gaps Summary

No gaps. All 9 must-haves (4 roadmap success criteria + 5 task truths) are VERIFIED against the codebase with live-run gates:

- The SUB-ATTRIB ports obey the 1:1 mandate — spot-checked Pig/Cow/Spider against the 26.2 jar bytecode this session; the float-widened doubles (cow 0.20000000298023224, spider 0.30000001192092896) are preserved bit-for-bit and each supplier cites its class. The executor's claim of correcting wrong plan values against bytecode is confirmed (the code matches the jar, not the plan's "confirm" guesses).
- The handles are id-not-pointer (reflect-asserted), tick-owned-seam-routed, capability-enforced (block-unchanged proven through the real ChunkManager).
- The starlarkGoal is a genuine `server.Goal` arbitrated by the EXISTING goalSelector (compile-time assertion + arbitration test), and `starlark.Call` is absent from every tick-phase body (idle mob = 0 calls).
- THE GATE moves a base_type-pig custom mob > 2 blocks through the real `moveEntity`, rendering as `entity.Pig.ID`, and the whole path is Docker `-race` clean.

No Claude attribution in the phase commits (8e3303f7..9d6f185a) — sole author Matias Canovas, 0 co-authored-by/generated-with lines.

The documented scope boundary (no plugin-facing spawn builtin; `spawnDeclaredMob` is the test/debug seam, deferred to a future plan under the naturalSpawner CREATURE cap) is an accepted deferral consistent with threat T-23-11 disposition `accept`, not a gap.

---

_Verified: 2026-06-28T06:10:00Z_
_Verifier: Claude (gsd-verifier)_
