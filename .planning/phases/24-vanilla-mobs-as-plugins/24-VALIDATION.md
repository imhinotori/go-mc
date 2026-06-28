---
phase: 24
slug: vanilla-mobs-as-plugins
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-28
---

# Phase 24 — Validation Strategy

> Per-phase validation contract. The 1:1 mandate makes bytecode-fidelity the load-bearing check.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Quick run command** | `CGO_ENABLED=0 go test ./server/ ./plugin/... ./level/attribute/` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./server/ ./plugin/... && CGO_ENABLED=0 go test ./server/ ./plugin/...` |
| **-race command (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...` |
| **1:1 anchor** | `javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>` before each ported goal/value |
| **Estimated runtime** | ~20–60s |

---

## Sampling Rate

- **After every task commit:** the quick suite
- **After every plan wave:** full suite + Docker `-race` (the swapped plugin pig ticking + seeded-RNG determinism are only meaningful under -race + with the fixed seed)
- **Before `/gsd-verify-work`:** full suite green + Docker `-race` green + the behavior-identical suite green
- **Max feedback latency:** ~60s quick / ~3 min race

---

## Per-Task Verification Map

> Task IDs provisional. The PLUGIN-04 load-bearing samples. EACH ported goal/value carries a jar-citation acceptance.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 24-01-01 | 01 | 1 | PLUGIN-04 | — | per-entity SEEDED RandomSource reproduces the bytecode draw order (nextInt(reducedTickDelay) THEN getPosition); fixed seed → deterministic jar-matching sequence; retires the global-rand AI flake | unit | `CGO_ENABLED=0 go test ./server/ -run TestSeededAIRandomMatchesJarOrder` | ❌ W0 | ⬜ pending |
| 24-01-02 | 01 | 1 | PLUGIN-04 | T-24-01 | handle API extended 1:1: entity.set_look(yaw,pitch)→headYaw/yaw seam; world.nearest_player(x,y,z,r)→player query (faithful to getNearestPlayer + targeting conditions); entity.rand_int/rand_float→per-entity seeded source. Capability-gated. | unit | `CGO_ENABLED=0 go test ./server/ -run TestHandleExtensions` | ❌ W0 | ⬜ pending |
| 24-02-01 | 02 | 2 | PLUGIN-04 | — | the 3 EXISTING pig goals re-expressed 1:1 in vanilla_pig.star (RandomStroll@6 MOVE, LookAtPlayer@7 LOOK, RandomLookAround@8 MOVE\|LOOK) — each matches the jar goal class (priority/flags/canUse/RNG order/constants), CITED | unit + grep | `CGO_ENABLED=0 go test ./server/ -run TestVanillaPigGoalsPorted` (+ each goal's .star cites its jar FQCN) | ❌ W0 | ⬜ pending |
| 24-02-02 | 02 | 2 | PLUGIN-04 | — | NEW vanilla pig goals built+ported 1:1 where the prerequisite is faithfully buildable (FloatGoal@0 via in-water detect; PanicGoal@1 via on_damage hurt-flag); any goal whose subsystem is too large (Breed/Tempt/FollowParent aging) is DEFERRED with a CITED reason (NOT silently skipped) | unit | `CGO_ENABLED=0 go test ./server/ -run TestVanillaPigNewGoals` | ❌ W0 | ⬜ pending |
| 24-02-03 | 02 | 2 | PLUGIN-04 | T-24-02 | SWAP: newPigAI replaced at both call sites (async.go, debug.go) by the plugin-driven build; the vanilla plugin boot-loads; the plugin pig is the only pig; renders as entity.Pig.ID | unit + grep | `! grep -n 'newPigAI' server/async.go server/debug.go` (replaced) + `CGO_ENABLED=0 go test ./server/ -run TestPluginPigBootLoads` | ❌ W0 | ⬜ pending |
| 24-02-04 | 02 | 2 | PLUGIN-04 | — | THE GATE — BEHAVIOR-IDENTICAL: the existing pig-AI suite (TestServerAiStep*) passes against the plugin pig UNCHANGED (or updated only to the seeded-source expectation); TestPluginPigEqualsGoNativePig proves identity | integration | `CGO_ENABLED=0 go test ./server/ -run 'TestServerAiStep\|TestPluginPigEqualsGoNativePig'` | ❌ W0 | ⬜ pending |
| 24-02-05 | 02 | 2 | PLUGIN-04 | T-24-01 | RACE: the swapped plugin pig ticking (handle reads/look/nav mutate + seeded RNG, all tick-owned) is -race clean; the seeded source is per-entity (no shared global-rand race) | race | Docker `-race ./server/` `-run TestPluginPigRace` | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `server/ai_random.go` (or similar) — the per-entity seeded RandomSource + the bytecode draw-order test
- [ ] handle-extension code in server/plugin_entity.go (set_look, nearest_player, rand) + tests
- [ ] `server/testdata/.../vanilla_pig/` — the vanilla_pig .star (manifest + the ported goals, each citing its jar FQCN) + the boot-load wiring
- [ ] `server/plugin_pig_test.go` — the ported-goals tests + the behavior-identical gate + the boot-load test
- [ ] `server/plugin_pig_race_test.go` — the swapped-pig -race test
- [ ] update/confirm the existing pig-AI tests pass against the plugin pig (seeded-source expectation where needed)
- [ ] javap evidence for EACH ported goal + RNG draw order (the 1:1 anchor — in the commit/SUMMARY)

*The 1:1 mandate: NO ported value is written without javap-confirming it against `temp/cache/26.2-inner.jar` first. Each goal cites its jar class.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | — |

*All Phase-24 behaviors are automatable headless (a plugin pig ticking is testable; behavior-identity is a test-suite assertion). The real-client visual gate is Phase 28.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] Each ported goal/value has a jar-citation in its acceptance
- [ ] Feedback latency < 60s (quick) / race in Docker
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
