---
phase: 21-starlark-runtime-foundation
plan: 01
subsystem: plugin
tags: [starlark, go.starlark.net, sandbox, scripting, cgo-free, plugin]

# Dependency graph
requires:
  - phase: (none — first plan of the v4 plugin milestone)
    provides: the project module github.com/imhinotori/sulfur + CGO=0 ship-build discipline
provides:
  - "plugin/starlark leaf package: the embedded, sandboxed, CGO-free Starlark runtime"
  - "Centralized sandbox-policy constructor (step budget + FileOptions zero value + curated globals) as a single source of truth"
  - "Load(path) dir/path loader: parse+compile+run a .star ONCE, return auto-frozen globals"
  - "LoadedPlugin handle + Call helper (fresh per-call thread; one Thread per goroutine)"
  - "echo server-neutral demo builtin bridging Go<->Starlark"
  - "go.starlark.net pinned as a plain require (fork NOT needed — closes the v4 fork-or-vendor open decision)"
affects: [22-plugin-manager-event-bus, 23-frozen-handle-world-api, plugin, scripting]

# Tech tracking
tech-stack:
  added: ["go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb (plain require, pure-Go, CGO=0)"]
  patterns:
    - "Single-source-of-truth sandbox policy: every Thread goes through newThread() so SetMaxExecutionSteps can never be forgotten"
    - "Curated predeclared StringDict as the entire reachable surface (allowlist-by-construction; no fs/net/eval builtin in the universe)"
    - "Load-once, call-many: ExecFileOptions compiles+runs the module body once; later Call reuses frozen globals on a fresh per-goroutine Thread"
    - "Leaf package boundary: plugin/starlark imports only go.starlark.net + stdlib, never server/world/level"

key-files:
  created:
    - plugin/starlark/runtime.go
    - plugin/starlark/loader.go
    - plugin/starlark/plugin.go
    - plugin/starlark/builtins.go
    - plugin/starlark/runtime_test.go
    - plugin/starlark/testdata/greet.star
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "go.starlark.net embedded as a PLAIN require, NOT a fork — research verified all three sandbox knobs (step budget, recursion-off, no-I/O) are exposed upstream unpatched. This closes the v4 fork-or-vendor open decision as 'plain dep, no fork needed'."
  - "Sandbox dialect = syntax.FileOptions{} zero value (recursion CHECK ON, while OFF, set() OFF, no top-level control flow). Recursion is NOT set true — that would DISABLE the guard."
  - "Step budget centralized in newThread(); stepBudget const = 10_000_000 for load-time bodies (a later phase can make it per-plugin configurable)."
  - "Pinned version retained as v0.0.0-20260613233743-8ba36ccb83fb (whatever go get @latest resolved to at execution); API stable across pseudo-versions."

patterns-established:
  - "Sandbox single-source-of-truth: loader and caller both construct Threads via newThread() (un-bypassable budget)"
  - "Allowlist-by-construction globals via safeGlobals() — the predeclared StringDict is the full app surface"
  - "Load-once / call-many lifecycle with frozen globals crossing the goroutine boundary read-only"

requirements-completed: [PLUGIN-01]

# Metrics
duration: 3min
completed: 2026-06-28
---

# Phase 21 Plan 01: Starlark runtime foundation Summary

**Embedded go.starlark.net as a plain CGO=0 dependency and stood up the isolated `plugin/starlark/` leaf package — centralized sandbox policy, a load-once dir/path loader returning auto-frozen globals, a server-neutral `echo` Go<->Starlark bridge builtin, and green load/exec + round-trip tests.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-06-28T03:28:43Z
- **Completed:** 2026-06-28T03:31:11Z
- **Tasks:** 3
- **Files created:** 6 (+ go.mod/go.sum modified)

## Accomplishments
- `go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb` added as a **plain `require`** (no fork) — `CGO_ENABLED=0 go build ./...` stays exit 0, zero `import "C"` under `plugin/`, go directive unchanged at 1.25.0.
- New leaf package `plugin/starlark/` that imports **only** go.starlark.net + stdlib (no server/world/level), with sandbox policy centralized in `runtime.go` (`defaultFileOptions()` zero-value FileOptions, `newThread()` with `SetMaxExecutionSteps(stepBudget)`, curated `safeGlobals()`).
- `Load(path)` reads a `.star` from disk and execs it **once** via `ExecFileOptions`, returning auto-frozen globals as a `LoadedPlugin`; `LoadedPlugin.Call` invokes a Starlark fn on a fresh per-call thread and round-trips a value back to Go through the registered `echo` builtin.
- Happy-path tests green under `CGO_ENABLED=0 go test`: `TestLoadExec` (frozen `result == "hi"` proves load-time exec + missing-file error) and `TestBuiltinRoundTrip` (`greet("bob") -> "bob"`).
- **The v4 fork-or-vendor open decision is recorded as resolved: plain dep, no fork needed.**

## Task Commits

Each task was committed atomically:

1. **Task 1: Add go.starlark.net as a plain dependency** - `531df91e` (chore)
2. **Task 2: Build the plugin/starlark leaf package** - `69e6a880` (feat)
3. **Task 3: Happy-path tests** - `07f7305a` (test)

**Plan metadata:** (final docs commit — SUMMARY/STATE/ROADMAP/REQUIREMENTS)

## TDD Gate Compliance

Task 3 carried `tdd="true"`. By the plan's deliberate structure, the implementation under test (`Load`, `LoadedPlugin.Call`) was built in Task 2 (`feat` commit `69e6a880`), and Task 3 adds the tests (`test` commit `07f7305a`). The `feat` GREEN commit therefore precedes the `test` commit in history rather than following it. This is intentional per the plan task ordering (runtime in Task 2, happy-path tests in Task 3) — not a skipped RED gate. The negative-path sandbox tests and the frozen cross-goroutine `-race` test are Plan 02 (Wave 2), which depends on this runtime existing. Tests pass deterministically (<1s) under `CGO_ENABLED=0 go test`.

## Files Created/Modified
- `plugin/starlark/runtime.go` - Sandbox-policy single source of truth: `stepBudget` const, `defaultFileOptions()` (FileOptions zero value), `newThread()` (step budget set), `safeGlobals()` (curated predeclared StringDict).
- `plugin/starlark/loader.go` - `Load(path)` dir/path seam: `os.ReadFile` + `ExecFileOptions` (parse+compile+run once) -> frozen globals.
- `plugin/starlark/plugin.go` - `LoadedPlugin{path, globals}` handle, `Global()` read, `Call()` helper (fresh per-call thread).
- `plugin/starlark/builtins.go` - `echoBuiltin` server-neutral demo builtin (pure value-return via `UnpackPositionalArgs`).
- `plugin/starlark/testdata/greet.star` - happy-path fixture: load-time `result = echo("hi")` + `def greet(who): return echo(who)`.
- `plugin/starlark/runtime_test.go` - `TestLoadExec` + `TestBuiltinRoundTrip`.
- `go.mod` / `go.sum` - plain `require go.starlark.net` + checksums.

## Decisions Made
- **Plain dep, no fork** — confirmed against the verified research that all sandbox knobs are upstream-exposed; resolves the v4 fork-or-vendor open decision.
- **FileOptions zero value** for the safe dialect; never set `Recursion:true` (that disables the guard).
- **Step budget in `newThread()`** so it is un-bypassable; const `stepBudget = 10_000_000`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Reordered the dep-add vs. package-create steps so `go mod tidy` retains the require**
- **Found during:** Task 1 (Add go.starlark.net as a plain dependency)
- **Issue:** Running `go get go.starlark.net@latest` followed immediately by `go mod tidy` (as the task literally specified) PRUNED the dependency from go.mod/go.sum, because no Go file imported it yet — `tidy` removes requires with no importing package. Task 1's acceptance criteria (`go.mod` carries the require, `go.sum` carries the entries) could not hold in isolation before any `plugin/` code existed.
- **Fix:** Created the `plugin/starlark/` source files (Task 2 deliverables) first so the import `go.starlark.net/starlark` and `.../syntax` exists, THEN re-ran `go get go.starlark.net@latest && go mod tidy`. The require now persists. The two logical task commits are still atomic and in plan order (`chore` go.mod/go.sum, then `feat` package), only the on-disk authoring order was adjusted. No version, no behavior, no scope change.
- **Files modified:** go.mod, go.sum (Task 1 commit `531df91e`); plugin/starlark/*.go authored before tidy but committed under Task 2 `69e6a880`.
- **Verification:** `grep go.starlark.net go.mod` (present), `grep -c go.starlark.net go.sum` (2 entries), `CGO_ENABLED=0 go build ./...` exit 0, `go directive` still 1.25.0.
- **Committed in:** `531df91e` (Task 1) and `69e6a880` (Task 2)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** The authoring-order adjustment was necessary because `go mod tidy` requires an importer; the committed task sequence and all acceptance criteria are unchanged. No scope creep.

## Issues Encountered
- None beyond the deviation above. The workspace has many untracked `*.log` files and other untracked dirs at the repo root; only task-related files were staged individually (never `git add .`), so none of that noise entered any commit.

## Known Stubs
None. The `echo` builtin is intentionally server-neutral (pure value-return) per the locked phase decision — this is the designed scope, not a stub; real entity/world handles are a later phase.

## Next Phase Readiness
- The runtime exists and is proven CGO=0-clean with load-once + round-trip green. **Plan 02 (Wave 2)** builds on this: the three sandbox negative tests (step-budget hit inside a `def`, self-recursion dynamic error, `open` undefined) and the frozen cross-goroutine `-race` test (CGO=1 in Docker — a separate gate from this CGO=0 ship build).
- No blockers. The dir/path loader seam is in place for Phase 22's discovery/manifest extension; the centralized policy constructor is ready for per-plugin budget configurability.

## Self-Check: PASSED

All 6 source/test/fixture files + the SUMMARY exist on disk; all 3 task commits (`531df91e`, `69e6a880`, `07f7305a`) are present in git history.

---
*Phase: 21-starlark-runtime-foundation*
*Completed: 2026-06-28*
