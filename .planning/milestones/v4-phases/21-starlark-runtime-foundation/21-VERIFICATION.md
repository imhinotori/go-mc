---
phase: 21-starlark-runtime-foundation
verified: 2026-06-27T00:00:00Z
status: passed
score: 4/4 must-haves verified
overrides_applied: 0
re_verification: # No — initial verification
  previous_status: none
---

# Phase 21: Starlark runtime foundation Verification Report

**Phase Goal:** A Starlark runtime is embedded in Sulfur (pure-Go, CGO_ENABLED=0 preserved) that loads, sandboxes, and runs a `.star` plugin file — the riskiest single thing proven first (sandbox + CGO=0 + race-safety) before any host/event/behavior layer is built on top.
**Verified:** 2026-06-27
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `go.starlark.net` embedded; a `.star` loads/parses/compiles once via the load lifecycle; `CGO_ENABLED=0 go build ./...` stays clean (no `import "C"`, go.mod static) | ✓ VERIFIED | `go.mod:17` plain `require go.starlark.net v0.0.0-20260613233743-...` (no `replace`/fork); `go.sum:55-56` checksums present. `loader.go:Load()` runs `ExecFileOptions` (parse+compile+run ONCE). `CGO_ENABLED=0 go build ./...` exit 0. Zero `import "C"` under `plugin/` (grep: no matches). `plugin/starlark` transitive deps = only go.starlark.net subpackages (no `runtime/cgo`). `TestLoadExec` PASS (frozen `result=="hi"` + missing-file error). go directive 1.25.0 unchanged. |
| 2 | Sandbox holds: per-Thread step budget enforced, recursion OFF, no fs/network builtins | ✓ VERIFIED | `runtime.go:newThread()` calls `SetMaxExecutionSteps(stepBudget)` un-bypassably (both loader and Call route through it). `defaultFileOptions()` returns zero-value `FileOptions{}` → recursion guard ON (NOT set true). `safeGlobals()` exposes ONLY `echo`. `TestStepBudgetHalts` PASS — asserts `*starlark.EvalError`, "too many steps", `th.ExecutionSteps()==50000` (exact cap), AND guards the P1 false-pass by asserting error is NOT "not within a function". Fixture `infinite_loop.star` puts the loop INSIDE `def spin()` (P1) using `for...range` not `while` (P2). `TestRecursionRejected` PASS ("function f called recursively"). `TestNoIOBuiltins` PASS ("undefined: open"). |
| 3 | One Thread per goroutine; a FrozenValue produced at load read race-clean from another goroutine | ✓ VERIFIED | Globals AUTO-FROZEN by `ExecFileOptions` at load. `plugin.go:Call()` builds a FRESH Thread per call (Threads never shared). `TestFrozenCrossGoroutine` (8 readers + 4 callers, one Thread each) PASS. **Docker `-race` gate run by verifier** (`golang:1.26`, CGO=1): `ok .../plugin/starlark 1.045s`, exit 0, NO DATA RACE. |
| 4 | A plugin calls a registered Go builtin and returns a value | ✓ VERIFIED | `builtins.go:echoBuiltin` registered in `safeGlobals` via `NewBuiltin`; `greet.star` `def greet(who): return echo(who)`. `TestBuiltinRoundTrip` PASS (`greet("bob")=="bob"` round-trips Go→Starlark→Go-builtin→Go). Path is Docker `-race` clean. |

**Score:** 4/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `plugin/starlark/runtime.go` | Sandbox policy single source of truth | ✓ VERIFIED | `stepBudget=10_000_000`, `defaultFileOptions()` (zero-value), `newThread()` (budget set), `safeGlobals()` (echo only). Substantive (45 lines), imported by all package files. |
| `plugin/starlark/loader.go` | Load-once dir/path loader → frozen globals | ✓ VERIFIED | `Load()` = `os.ReadFile` + `ExecFileOptions`. Wired (used by tests + Call path). |
| `plugin/starlark/plugin.go` | `LoadedPlugin` handle + `Call`/`Global` | ✓ VERIFIED | `Call()` fresh per-call Thread; `Global()` frozen read. Wired. |
| `plugin/starlark/builtins.go` | `echo` Go↔Starlark bridge | ✓ VERIFIED | `echoBuiltin` via `UnpackPositionalArgs`. Registered in `safeGlobals`, exercised by round-trip + race tests. |
| `plugin/starlark/runtime_test.go` | Load/exec + round-trip tests | ✓ VERIFIED | `TestLoadExec`, `TestBuiltinRoundTrip` — both PASS. |
| `plugin/starlark/sandbox_test.go` | 3 sandbox negatives | ✓ VERIFIED | `TestStepBudgetHalts` (+ P1 guard + exact step count), `TestRecursionRejected`, `TestNoIOBuiltins` — all PASS. |
| `plugin/starlark/race_test.go` | Frozen cross-goroutine -race test | ✓ VERIFIED | `TestFrozenCrossGoroutine` PASS under Docker -race. |
| `plugin/starlark/testdata/*.star` | 4 fixtures (greet, infinite_loop, recursive, tries_io) | ✓ VERIFIED | All present; `infinite_loop.star` correctly loop-in-`def`/`for-range` (P1/P2 encoded). |
| `go.mod` / `go.sum` | plain require + checksums | ✓ VERIFIED | Line 17 require; lines 55-56 sum; no `replace`. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `loader.go:Load` | go.starlark.net | `ExecFileOptions(defaultFileOptions(), newThread, ...)` | ✓ WIRED | Parse+compile+run once; budget + safe dialect threaded through. |
| `plugin.go:Call` | go.starlark.net | `starlark.Call(newThread, fn, ...)` | ✓ WIRED | Fresh Thread per call; returns value to Go. |
| `safeGlobals` | `echoBuiltin` | `NewBuiltin("echo", echoBuiltin)` | ✓ WIRED | Builtin reachable from `.star`; round-trip test proves it. |
| `.star` globals | tick/reader goroutine | auto-freeze + frozen read | ✓ WIRED | Race test (8R+4C) clean under Docker -race. |

### Data-Flow Trace (Level 4)

Not applicable — Phase 21 is a runtime foundation (leaf package, no dynamic-data-rendering UI/API surface). The "data" is the round-tripped value, traced directly through `TestBuiltinRoundTrip` (`"bob"` in → `"bob"` out) and `TestLoadExec` (`echo("hi")` → frozen `result=="hi"`). Real values flow; nothing hardcoded-empty.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All packages build CGO=0 | `CGO_ENABLED=0 go build ./...` | exit 0, no output | ✓ PASS |
| Package + tests pass CGO=0 | `CGO_ENABLED=0 go test ./plugin/starlark/ -v` | 6/6 PASS | ✓ PASS |
| Vet clean | `go vet ./plugin/...` | exit 0 | ✓ PASS |
| No cgo in plugin | `grep 'import "C"' plugin/` | no matches | ✓ PASS |
| Race-safety (CGO=1) | Docker `golang:1.26 go test -race ./plugin/starlark/` | `ok ... 1.045s`, no DATA RACE | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| PLUGIN-01 | 21-01, 21-02 | Embedded sandboxed CGO-free Starlark runtime: load/parse/compile once, step budget, recursion off, no I/O, one Thread/goroutine, frozen cross-boundary, builtin round-trip, -race clean | ✓ SATISFIED | All 4 success criteria verified above; 6/6 tests pass CGO=0; Docker -race clean (verifier-run). |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | none | — | No TODO/FIXME/placeholder/panic/stub patterns in any `plugin/starlark/*.go`. The `echo` builtin is server-neutral BY DESIGN (locked phase decision; real world handles are Phase 23), not a stub. |

### Human Verification Required

None. Phase 21 is fully autonomous and standalone-testable — there is no visual/real-client behavior in this phase (the real-client gate is Phase 28). All four observable truths are verified programmatically, including the Docker `-race` gate which the verifier executed directly.

### Git Authorship Check

All Phase 21 commits (`531df91e`, `69e6a880`, `07f7305a`, `44fd1708`, `84b6057f`, plus docs commits) are authored by `Matias Canovas <hello@hinotori.moe>`. No `Co-Authored-By: Claude`, no "Generated with", no Anthropic attribution found in any commit body. CLEAN.

### Gaps Summary

No gaps. Every success criterion is backed by substantive, wired code and a passing test that asserts the exact required behavior (not just existence). The two highest-risk false-pass traps were specifically guarded and verified:
- **P1 (hollow step-budget test):** `infinite_loop.star` loops inside `def spin()` and `TestStepBudgetHalts` asserts the error is NOT "not within a function" AND `ExecutionSteps()==50000` — proving the budget (not the parser) fired.
- **Race gate:** Run by the verifier in Docker `golang:1.26` (CGO=1, the correct gate since `-race` needs cgo) — clean, not merely reported by the executor.

The `go.starlark.net` dependency is a plain `require` (the v4 fork-or-vendor open decision is correctly resolved as "no fork needed"), CGO_ENABLED=0 is preserved end to end, and the build/test/vet/race gates are all independently green.

---

_Verified: 2026-06-27_
_Verifier: Claude (gsd-verifier)_
