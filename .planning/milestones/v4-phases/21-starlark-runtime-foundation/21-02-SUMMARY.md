---
phase: 21-starlark-runtime-foundation
plan: 02
subsystem: plugin
tags: [starlark, sandbox, security, race, cgo-free, plugin, negative-tests]

# Dependency graph
requires:
  - phase: 21-01
    provides: "plugin/starlark runtime (Load, LoadedPlugin.Call/Global, safeGlobals, newThread, defaultFileOptions, stepBudget, echo builtin)"
provides:
  - "Proven sandbox trust boundary: step-budget halt (no hang), recursion rejected, no fs/net builtin — all three observable invariants tested"
  - "Proven frozen cross-goroutine race-safety: N readers + M callers (one Thread each) Docker -race (CGO=1) clean"
  - "Phase 21 PLUGIN-01 gate fully met — phase complete"
affects: [22-plugin-host-event-bus, 23-frozen-handle-world-api, plugin, scripting]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Negative-path sandbox proof: each invariant (DoS via loop, DoS via recursion, info-disclosure via I/O) has a dedicated .star fixture + assertion on the EXACT observed error string"
    - "Budget test builds its OWN Thread with a SMALL cap (50_000) via the package's safe options so the budget fires fast and the exact ExecutionSteps can be asserted, instead of burning the 10M production budget"
    - "P1 guard encoded as a test assertion: the budget error must NOT contain 'not within a function' (a top-level-loop parse reject would make the test hollow)"
    - "Cross-goroutine race proof: frozen globals read lock-free from N readers; M callers each on a fresh per-call Thread (LoadedPlugin.Call) — Threads never shared (Pitfall 4)"

key-files:
  created:
    - plugin/starlark/sandbox_test.go
    - plugin/starlark/race_test.go
    - plugin/starlark/testdata/infinite_loop.star
    - plugin/starlark/testdata/recursive.star
    - plugin/starlark/testdata/tries_io.star
  modified: []

key-decisions:
  - "Budget test uses a test-local Thread with a 50_000 cap (via defaultFileOptions()+safeGlobals()+starlark.ExecFileOptions) rather than the 10M production stepBudget — fires fast and lets the test assert ExecutionSteps()==cap exactly. Internal test package (package starlark) makes this possible."
  - "The P1 trap is encoded twice: the loop fixture puts the for-range loop INSIDE def spin() (a top-level loop would be a parse reject, steps=0), AND the test asserts the error is NOT 'not within a function' before asserting the budget hit."
  - "P2 encoded: the loop uses for i in range(...) (allowed inside a def), not while (OFF by default in the safe dialect). The fixture text contains zero 'while' tokens so the acceptance grep is clean."

patterns-established:
  - "Per-invariant negative .star fixture + exact-error-string assertion as the sandbox security proof"
  - "Small-cap test Thread for fast, exact step-budget assertions"
  - "Docker -race (CGO=1) as the separate race gate from the CGO=0 ship build"

requirements-completed: [PLUGIN-01]

# Metrics
duration: 3min
completed: 2026-06-28
---

# Phase 21 Plan 02: Sandbox negatives + frozen cross-goroutine race test Summary

**Proved the three sandbox safety invariants (a runaway loop hits the step budget at the exact cap and returns a `*starlark.EvalError` "too many steps" — not a hang, not a parse reject; a self-recursive fn errors "function f called recursively"; `open(...)` is "undefined: open") plus the frozen cross-goroutine race-safety (8 readers + 4 callers, one fresh Thread each) Docker `-race` clean — closing the PLUGIN-01 gate and Phase 21.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-06-28T03:34:42Z
- **Completed:** 2026-06-28T03:37:14Z
- **Tasks:** 2
- **Files created:** 5 (2 test files + 3 negative .star fixtures)

## Accomplishments

- **TestStepBudgetHalts** (T-21-04 DoS mitigation): `infinite_loop.star` runs a `for i in range(...)` loop INSIDE `def spin()`; under a small 50_000 test cap the error `errors.As`-matches `*starlark.EvalError`, its message contains `"too many steps"`, and `th.ExecutionSteps()` equals the cap exactly. The test also guards against the P1 hollow-test risk by asserting the error is NOT a `"not within a function"` parse reject.
- **TestRecursionRejected** (T-21-05 DoS mitigation): `recursive.star` (`def f(n): return f(n-1)` called at load) errors with `"function f called recursively"` — the FileOptions zero-value recursion guard is ON.
- **TestNoIOBuiltins** (T-21-06 info-disclosure mitigation): `tries_io.star` (`open("etc/passwd")` at load) errors with `"undefined: open"` — no fs/network builtin in `safeGlobals` or the Starlark universe.
- **TestFrozenCrossGoroutine** (T-21-07 race mitigation): after `Load("testdata/greet.star")`, 8 reader goroutines read the frozen `result` global (`== "hi"`) concurrently and 4 caller goroutines each call `greet("bob")` (`-> "bob"`) on its OWN fresh Thread via `LoadedPlugin.Call`; joined with `sync.WaitGroup` using `t.Errorf` inside goroutines. Passes CGO=0 (logic) and Docker `-race` (CGO=1) with no DATA RACE.
- **Full PLUGIN-01 gate met:** `CGO_ENABLED=0 go build ./...` exit 0, `go vet` clean, all 6 `plugin/starlark` tests green under CGO=0, and `./plugin/...` Docker `-race` (CGO=1) clean.

## Task Commits

Each task was committed atomically (no Claude attribution):

1. **Task 1: Sandbox negative invariants + 3 fixtures** — `44fd1708` (test)
2. **Task 2: Frozen cross-goroutine -race test** — `84b6057f` (test)

**Plan metadata:** (final docs commit — SUMMARY/STATE/ROADMAP/REQUIREMENTS)

## Observed Error Strings (the security assertions)

| Invariant | Fixture | Observed error (asserted substring) | Extra assertion |
|-----------|---------|-------------------------------------|-----------------|
| Step budget halt | `infinite_loop.star` | `too many steps` (`*starlark.EvalError`) | `ExecutionSteps()==50000`; NOT `not within a function` (P1 guard) |
| Recursion off | `recursive.star` | `function f called recursively` | — |
| No I/O | `tries_io.star` | `undefined: open` | — |
| Frozen cross-goroutine | `greet.star` | (no error; no DATA RACE) | `result=="hi"` from 8 readers; `greet("bob")=="bob"` from 4 callers |

## P1/P2 Encoding in the Loop Fixture

- **P1 (loop-in-def):** `infinite_loop.star` puts the runaway loop INSIDE `def spin()` and calls `spin()`. A TOP-LEVEL `for`/`while` is a PARSE reject (`for loop not within a function`, steps=0) — which would make the budget test pass for the WRONG reason. The test additionally asserts the error does NOT contain `"not within a function"`, so the budget — not the parser — is proven to have fired.
- **P2 (no `while`):** the loop uses `for i in range(100000000)` (allowed inside a def), not `while` (OFF by default in the safe `FileOptions{}` dialect). The fixture contains zero `while` tokens (comment prose reworded), so the acceptance grep is clean.

## TDD Gate Compliance

Both tasks carried `tdd="true"`. The runtime under test (`Load`, `LoadedPlugin.Call`/`Global`, `safeGlobals`, `newThread`, `defaultFileOptions`, `stepBudget`) was built and committed in Plan 01 (Wave 1, `feat` `69e6a880`); Plan 02 is the Wave-2 negative-path + race proof layered on top of that existing GREEN implementation. These are pure invariant/safety tests against already-shipped behavior — they passed on first run with no implementation change required (the safe-by-default contract was already correct). No RED-then-GREEN cycle applies because no new production code was written in this plan; this is the deliberate Wave-1/Wave-2 split (runtime first, security proofs second). All tests pass deterministically (<1s) under CGO=0 and under Docker `-race` (CGO=1).

## Files Created

- `plugin/starlark/sandbox_test.go` — `TestStepBudgetHalts` (small-cap Thread, `*EvalError` + exact `ExecutionSteps` + P1 guard), `TestRecursionRejected`, `TestNoIOBuiltins`.
- `plugin/starlark/race_test.go` — `TestFrozenCrossGoroutine` (8 readers + 4 callers, fresh Thread per call, `sync.WaitGroup`, `t.Errorf`).
- `plugin/starlark/testdata/infinite_loop.star` — `for`/`range` loop INSIDE `def spin()` (P1/P2 encoded).
- `plugin/starlark/testdata/recursive.star` — `def f(n): return f(n-1)` self-call at load.
- `plugin/starlark/testdata/tries_io.star` — `open("etc/passwd")` at load.

## Decisions Made

- Budget test constructs its own 50_000-cap Thread (not the 10M production budget) so it fires fast and can assert the exact step count; possible because the tests live in the internal `package starlark`.
- The P1 trap is encoded both structurally (loop inside `def`) and as an explicit test assertion (reject `"not within a function"`), so the budget — not the parser — is provably what fired.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed `while` tokens from the loop fixture's comment prose**
- **Found during:** Task 1 (loop fixture authoring)
- **Issue:** The fixture's explanatory comments referenced `while` twice ("for/while is a PARSE reject", "`while` is OFF by default"). The plan's P2 acceptance criterion is `! grep -q 'while' plugin/starlark/testdata/infinite_loop.star` — a substring grep that does not distinguish code from comments, so the comment text would have failed the gate even though the executable loop correctly uses `for`/`range`.
- **Fix:** Reworded both comment lines to "A TOP-LEVEL loop is a PARSE reject" and "Conditional loops are OFF by default", eliminating all `while` tokens. The loop construct was always `for i in range(...)` — only the prose changed; no behavior change.
- **Files modified:** `plugin/starlark/testdata/infinite_loop.star` (Task 1 commit `44fd1708`)
- **Verification:** `grep -ci while plugin/starlark/testdata/infinite_loop.star` -> 0; all three sandbox tests still pass.
- **Committed in:** `44fd1708` (Task 1)

---

**Total deviations:** 1 auto-fixed (1 bug). **Impact:** none on scope or behavior — a documentation-prose fix to satisfy the P2 substring-grep acceptance check.

## Issues Encountered

- None. Local `-race` on Windows would require gcc/CGO, so the race gate was run via the project's Docker `golang:1.26` image exactly as the plan specifies (`MSYS_NO_PATHCONV=1 docker run ... go test -race ./plugin/...`) — it passed with no DATA RACE. The CGO=0 ship build and CGO=0 test suite are separately green; these are the two intended, non-conflicting gates (P3).

## Known Stubs

None. The negative fixtures and the frozen-read test exercise the real, already-shipped runtime; nothing is stubbed.

## Threat Flags

None — this plan adds only tests + fixtures; it introduces no new network endpoint, auth path, file-access pattern, or schema. (It PROVES the existing sandbox trust boundary holds; it does not widen the surface.)

## Next Phase Readiness

- **Phase 21 is COMPLETE.** PLUGIN-01 is fully met: a `.star` loads -> runs sandboxed (step budget, recursion off, no I/O) -> calls a registered Go builtin -> returns a value, all `-race` clean, default build CGO=0.
- **Next: Phase 22 (plugin host + event bus)** — plugin discovery/manifest/auto-load, the register-hooks-once event API, hot-reload, and the capability/permission model. The `plugin/starlark` dir/path loader seam and the centralized sandbox policy are ready to extend; the per-plugin step-budget knob (currently the 10M `stepBudget` const) is the configurability hook for Phase 22.
- No blockers.

## Self-Check: PASSED

All 5 created files exist on disk; both task commits (`44fd1708`, `84b6057f`) are present in git history.

---
*Phase: 21-starlark-runtime-foundation*
*Completed: 2026-06-28*
