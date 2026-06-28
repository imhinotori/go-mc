---
phase: 21
slug: starlark-runtime-foundation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-27
---

# Phase 21 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Config file** | none — go modules; the new `plugin/starlark` package self-contains its tests + `testdata/*.star` |
| **Quick run command** | `CGO_ENABLED=0 go test ./plugin/...` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./plugin/... && CGO_ENABLED=0 go test ./plugin/...` |
| **-race command (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./plugin/...` |
| **Estimated runtime** | ~5–15 seconds (interpreter is fast; no world/tick startup) |

---

## Sampling Rate

- **After every task commit:** Run `CGO_ENABLED=0 go test ./plugin/...`
- **After every plan wave:** Run the full suite command + the Docker `-race` command (the freeze/cross-goroutine invariant is only meaningful under `-race`)
- **Before `/gsd-verify-work`:** Full suite green AND Docker `-race` green
- **Max feedback latency:** ~15 seconds (quick), ~3 min (race in Docker, cold)

---

## Per-Task Verification Map

> Task IDs are provisional — the planner sets the final plan/wave split. The invariants below are the load-bearing Nyquist samples PLUGIN-01 must hit.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 21-01-01 | 01 | 1 | PLUGIN-01 | — | go.starlark.net added as plain `require`; `CGO_ENABLED=0 go build ./...` exit 0; zero `import "C"` in plugin pkg + transitively | unit/build | `CGO_ENABLED=0 go build ./... && ! grep -rn 'import \"C\"' plugin/` | ❌ W0 | ⬜ pending |
| 21-01-02 | 01 | 1 | PLUGIN-01 | — | a `.star` from `testdata/` loads + execs once via the dir loader and its globals are returned (auto-frozen) | unit | `CGO_ENABLED=0 go test ./plugin/... -run TestLoadExec` | ❌ W0 | ⬜ pending |
| 21-01-03 | 01 | 1 | PLUGIN-01 | — | a registered server-NEUTRAL Go builtin is callable from Starlark; Go calls a Starlark fn back via `starlark.Call` and gets a value | unit | `CGO_ENABLED=0 go test ./plugin/... -run TestBuiltinRoundTrip` | ❌ W0 | ⬜ pending |
| 21-01-04 | 01 | 2 | PLUGIN-01 | — | SANDBOX: an infinite loop **inside a `def`** hits `SetMaxExecutionSteps` budget → `*starlark.EvalError` "too many steps" (NOT a hang, NOT a parse-reject) | unit | `CGO_ENABLED=0 go test ./plugin/... -run TestStepBudgetHalts` | ❌ W0 | ⬜ pending |
| 21-01-05 | 01 | 2 | PLUGIN-01 | — | SANDBOX: a self-recursive `.star` errors (recursion OFF — `FileOptions.Recursion` zero-value false) | unit | `CGO_ENABLED=0 go test ./plugin/... -run TestRecursionRejected` | ❌ W0 | ⬜ pending |
| 21-01-06 | 01 | 2 | PLUGIN-01 | — | SANDBOX: a `.star` attempting I/O / unknown name fails (no filesystem/network builtin in the default predeclared set) | unit | `CGO_ENABLED=0 go test ./plugin/... -run TestNoIOBuiltins` | ❌ W0 | ⬜ pending |
| 21-01-07 | 01 | 2 | PLUGIN-01 | — | RACE: a global frozen at load is read from N reader goroutines + called from M caller goroutines (one fresh Thread per goroutine) — `-race` clean | race | Docker `-race ./plugin/...` `-run TestFrozenCrossGoroutine` | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `plugin/starlark/runtime_test.go` — the unit tests for load/exec, builtin round-trip
- [ ] `plugin/starlark/sandbox_test.go` — the three sandbox negative tests (step budget, recursion, no-I/O)
- [ ] `plugin/starlark/race_test.go` — the frozen cross-goroutine read/call test
- [ ] `plugin/starlark/testdata/*.star` — the `.star` fixtures (hello/echo, infinite-loop-in-def, self-recursive, io-attempt)
- [ ] `go get go.starlark.net@<pinned>` — add the dependency (Wave 0 / first task)

*The infinite-loop fixture MUST put the loop inside a `def` — a top-level `for`/`while` is a PARSE rejection (steps=0), which would make the budget test pass for the wrong reason (research pitfall P1).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | — |

*All Phase-21 behaviors have automated verification — the runtime foundation is fully testable standalone (no real client, no world). The real-client gate is Phase 28.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s (quick) / race in Docker
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
