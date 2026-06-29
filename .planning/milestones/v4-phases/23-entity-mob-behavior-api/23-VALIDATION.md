---
phase: 23
slug: entity-mob-behavior-api
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-28
---

# Phase 23 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Config file** | none — Go modules |
| **Quick run command** | `CGO_ENABLED=0 go test ./plugin/... ./server/ ./level/attribute/` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./plugin/... ./server/ ./level/attribute/ && CGO_ENABLED=0 go test ./plugin/... ./server/ ./level/attribute/` |
| **-race command (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./plugin/... ./server/` |
| **Estimated runtime** | ~15–40s (server suite is the slow part) |

---

## Sampling Rate

- **After every task commit:** `CGO_ENABLED=0 go test ./plugin/... ./server/ ./level/attribute/`
- **After every plan wave:** full suite + Docker `-race` (the declared-mob-ticks + handle-no-escape invariants are only meaningful under -race)
- **Before `/gsd-verify-work`:** full suite green + Docker `-race` green
- **Max feedback latency:** ~40s quick / ~3 min race

---

## Per-Task Verification Map

> Task IDs provisional. The PLUGIN-03 load-bearing samples.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 23-01-01 | 01 | 1 | PLUGIN-03 | — | SUB-ATTRIB coverage fix: per-type suppliers ported (1:1 jar) + a LivingEntity fallback so NewMapForEntity returns a REAL map for any living type (pig gets type-correct attrs, not nil/default) | unit | `CGO_ENABLED=0 go test ./level/attribute/ -run TestSupplierCoverage` | ❌ W0 | ⬜ pending |
| 23-01-02 | 01 | 1 | PLUGIN-03 | — | thin entityHandle (id+*TickLoop) is a starlark.Value w/ HasAttrs reads (health/pos/type/velocity/attribute); Freeze() no-op; a dead/removed entity read returns None/error (no stale pointer) | unit | `CGO_ENABLED=0 go test ./server/ -run TestEntityHandleReads` | ❌ W0 | ⬜ pending |
| 23-01-03 | 01 | 1 | PLUGIN-03 | T-23-01 | mutates route through tick-owned seams: move_to→nav requestPath, set_velocity→direct vx/vy/vz (faithful, integrated next tick by tickPhysics), set_block→ChunkManager.SetBlock+broadcast; NO raw field write outside a seam | unit | `CGO_ENABLED=0 go test ./server/ -run TestHandleMutators` | ❌ W0 | ⬜ pending |
| 23-01-04 | 01 | 1 | PLUGIN-03 | T-23-02 | capability ENFORCEMENT: a plugin without `world.write` calling world.set_block gets a Starlark error; with it, the block changes (same for entities.write, nav) | unit | `CGO_ENABLED=0 go test ./server/ -run TestCapabilityEnforced` | ❌ W0 | ⬜ pending |
| 23-02-01 | 02 | 2 | PLUGIN-03 | — | declare_mob(name, base_type, attributes, goals) captures the declaration ONCE at load; a starlarkGoal implements the existing server.Goal interface (canUse/tick/flags) and goes through goalSelector arbitration (not a bypass) | unit | `CGO_ENABLED=0 go test ./server/ -run TestDeclareMobGoal` | ❌ W0 | ⬜ pending |
| 23-02-02 | 02 | 2 | PLUGIN-03 | — | declare-once hot path: the interpreter fires ONLY inside a RUNNING goal callback (idle mob = 0 starlark.Calls); the goal struct is per-mob, the callables shared frozen | unit | `CGO_ENABLED=0 go test ./server/ -run TestNoInterpreterWhenIdle` | ❌ W0 | ⬜ pending |
| 23-02-03 | 02 | 2 | PLUGIN-03 | — | THE GATE: a wander mob declared in .star (base_type pig, one MOVE goal that nav-targets a random nearby pos) spawns, ticks, and its position CHANGES via the Go nav + moveEntity; renders as the existing pig wire-id | integration | `CGO_ENABLED=0 go test ./server/ -run TestWanderMobSpawnsAndMoves` | ❌ W0 | ⬜ pending |
| 23-02-04 | 02 | 2 | PLUGIN-03 | T-23-01 | RACE: a declared mob ticking (handle read + nav mutate on the tick goroutine) is -race clean; no handle escapes to another goroutine holding a live *Entity | race | Docker `-race ./server/` `-run TestDeclaredMobRace` | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `level/attribute/defaults_test.go` ext — TestSupplierCoverage (pig + the fallback return real maps)
- [ ] the new per-type suppliers + the LivingEntity fallback in `level/attribute/defaults.go` (1:1 jar ports, cited)
- [ ] `server/plugin_entity_test.go` — handle reads/mutators + capability enforcement
- [ ] `server/plugin_mob_test.go` — declare_mob + starlarkGoal + the wander-mob GATE + idle-no-interpreter
- [ ] `server/plugin_mob_race_test.go` — the declared-mob -race test
- [ ] testdata `.star` — the wander mob declaration (declare_mob + a MOVE goal callback using nav)

*The wander-mob GATE is THE architecture proof: a custom mob, declared in Starlark, ticking + moving through the UNCHANGED Go goalSelector/nav/moveEntity — the interpreter only in the active goal callback.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | — |

*All Phase-23 behaviors are automatable (a declared mob ticking is testable headless). The real-client visual gate is Phase 28.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 40s (quick) / race in Docker
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
