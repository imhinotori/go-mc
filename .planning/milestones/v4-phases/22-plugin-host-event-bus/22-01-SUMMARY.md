---
phase: 22-plugin-host-event-bus
plan: 01
subsystem: infra
tags: [starlark, plugin-host, event-bus, toml, fsnotify, cgo-free]

# Dependency graph
requires:
  - phase: 21-starlark-runtime-foundation
    provides: "plugin/starlark sandbox runtime (Load, safeGlobals, newThread, step budget, freeze, starlark.Call race-safety)"
provides:
  - "plugin/starlark.LoadWith(path, extra) + exported SafeGlobals/NewThread — the host can inject builtins into the predeclared set on a budget-bounded thread (A2)"
  - "plugin/host package: TOML manifest decode + path-traversal guard, the map[EventType][]Hook typed event bus, the register-once builtin, Manager (LoadDir/LoadDirWith/Unload), Emit (zero-sub guard + fresh-thread per-hook isolation)"
  - "8 EventType consts + frozen-scalar payload structs (Tick, PlayerJoin/Leave, BlockBreak/Place, EntitySpawn/Death, Damage)"
  - "github.com/BurntSushi/toml + github.com/fsnotify/fsnotify added as pure-Go requires (CGO=0 clean)"
affects: [22-02-server-wiring, 23-entity-mob-behavior-api, 26-python-runtime]

# Tech tracking
tech-stack:
  added:
    - "github.com/BurntSushi/toml v1.6.0 (pure-Go TOML decode for plugin.toml)"
    - "github.com/fsnotify/fsnotify v1.10.1 (pure-Go file watcher; anchored here, used by the Plan-02 hot-reload watcher)"
  patterns:
    - "Host-injected predeclared set via starlark.LoadWith (safeGlobals merged with host extras, extra wins, copied into a fresh map)"
    - "Typed map[EventType][]Hook event bus dispatched inline on the single tick owner — no channels/goroutines/pub-sub broker"
    - "register-once-at-load: the register builtin captures a frozen starlark.Callable into the bus during module exec; unknown event names error at LOAD"
    - "Emit zero-subscriber guard BEFORE arg alloc; fresh thread per hook; per-hook recover+error-log isolation"

key-files:
  created:
    - "plugin/host/manifest.go — Manifest + readManifest (TOML + path-traversal guard)"
    - "plugin/host/event.go — 8 EventType consts + frozen-scalar payloads + toStarlark"
    - "plugin/host/register.go — makeRegisterBuiltin (capture + unknown-event reject)"
    - "plugin/host/builtins.go — server-neutral log builtin"
    - "plugin/host/manager.go — Manager{plugins,hooks}; LoadDir/LoadDirWith; Unload"
    - "plugin/host/emit.go — Emit (zero-sub guard + fresh-thread + isolation)"
    - "plugin/host/deps.go — fsnotify require anchor (until the Plan-02 watcher imports it)"
    - "plugin/host/{manifest,manager,emit,race}_test.go + testdata/plugins/{greeter,badhook}"
  modified:
    - "plugin/starlark/runtime.go — exported SafeGlobals + NewThread"
    - "plugin/starlark/loader.go — LoadWith(path, extra); Load now delegates to LoadWith(path, nil)"
    - "go.mod / go.sum — toml + fsnotify requires"

key-decisions:
  - "LoadWith merges a COPY of safeGlobals() with host extras (extra wins) so the shared allowlist is never mutated; Load is the zero-extra path through it, keeping Phase-21 Load + its tests unchanged"
  - "Test/Plan-02 observability seam = LoadDirWith(root, extra StringDict) — tests inject a count builtin; the watcher will reuse it to reload one plugin"
  - "DamageEvent.Amount carries the FINAL post-mitigation value (locked CONTEXT decision); payloads are frozen scalars only — no live handles (Phase 23)"
  - "fsnotify require anchored via a blank import in deps.go because it is first imported for real by the Plan-02 watcher; go mod tidy would otherwise prune it"

patterns-established:
  - "Pattern: host injects builtins into Phase-21's predeclared set via the new LoadWith seam"
  - "Pattern: inline map-bus dispatch on the tick owner with a zero-subscriber fast path and per-hook panic/error isolation"

requirements-completed: [PLUGIN-02]

# Metrics
duration: ~25min
completed: 2026-06-28
---

# Phase 22 Plan 01: Plugin host + typed event bus Summary

**Standalone `plugin/host` package — TOML-manifest plugin Manager (discover/load/unload), a `map[EventType][]Hook` typed event bus, a register-once builtin, and a zero-sub-guarded, per-hook-isolated `Emit` — plus the Phase-21 `LoadWith` extension that makes the host's `register` builtin injectable.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-06-28 (Phase 22 execution)
- **Completed:** 2026-06-28
- **Tasks:** 3
- **Files modified/created:** 15 (3 modified, 12 created)

## Accomplishments
- A2 unblocked: `plugin/starlark.LoadWith(path, extra)` + exported `SafeGlobals`/`NewThread`; existing `Load` delegates to `LoadWith(path, nil)` so Phase-21 behavior and tests are untouched.
- Full `plugin/host` package: TOML manifest (4 required fields validated, entrypoint path-traversal guarded — T-22-03), the 8-event typed bus over frozen scalars, the register-once builtin (unknown event rejected at LOAD), `Manager.LoadDir`/`LoadDirWith`/`Unload`, and `Emit` (zero-subscriber guard before arg alloc, fresh thread per hook, per-hook recover + error isolation).
- Two pure-Go deps (BurntSushi/toml, fsnotify) wired as plain requires; CGO=0 ship build clean; no `import "C"` under `plugin/`.
- 11 host tests green under CGO=0; `TestEmitRace` (8 goroutines × 50 concurrent Emits) green under the Docker `-race` (CGO=1) gate; the boundary holds (`plugin/host` imports `plugin/starlark` + stdlib + the 2 deps only — no server/world/level).

## Task Commits

1. **Task 1: Pure-Go deps + A2 starlark extension** - `60bd0890` (feat)
2. **Task 2: plugin/host package (manifest, events, register, Manager, Emit)** - `5e59e754` (feat)
3. **Task 3: Host unit + race suite + fixtures** - `b5029609` (test)

_Task 2/3 were tdd-flagged; implementation (Task 2) and its exercising suite (Task 3) committed separately — the suite surfaced no bugs in the implementation._

## Files Created/Modified
- `plugin/starlark/runtime.go` - exported `SafeGlobals`/`NewThread` (delegate to the unchanged unexported helpers so the step budget stays un-bypassable)
- `plugin/starlark/loader.go` - `LoadWith(path, extra)`; `Load` delegates with nil extra
- `plugin/host/manifest.go` - `Manifest` + `readManifest` (TOML decode, required-field + path-traversal validation)
- `plugin/host/event.go` - 8 `EventType` consts, `isKnownEvent`, frozen-scalar payloads + `toStarlark`
- `plugin/host/register.go` - `makeRegisterBuiltin` (capture callable, reject unknown event)
- `plugin/host/builtins.go` - server-neutral `log` builtin
- `plugin/host/manager.go` - `Manager`, `New`, `LoadDir`/`LoadDirWith`, `Unload`, `PluginCount`/`HookCount`
- `plugin/host/emit.go` - `Emit` + `callHook` (zero-sub guard, fresh thread, recover isolation)
- `plugin/host/deps.go` - fsnotify require anchor
- `plugin/host/{manifest,manager,emit,race}_test.go` + `testdata/plugins/{greeter,badhook}` - the suite + fixtures

## Decisions Made
- `LoadWith` copies `safeGlobals()` into a fresh map before merging host extras (extra wins) — the shared allowlist is never mutated, and `Load` is the zero-extra path so there is one code path.
- Added `LoadDirWith(root, extra)` as a reusable observability/reload seam (tests inject a `count` builtin; Plan 02's watcher reloads one plugin through it).
- Exposed `PluginCount`/`HookCount` accessors so tests assert register-once and unload without reaching into unexported fields.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added a host `log` builtin (builtins.go) beyond the planned files**
- **Found during:** Task 2 (package implementation)
- **Issue:** The fixtures and the research example call `log(...)`, but Phase-21 only ships `echo`. The plan said "define a minimal host `log` builtin ... OR write fixtures to use `echo` — pick one and be consistent." A `log` builtin is the cleaner, forward-compatible choice (echo returns a value; plugins want a side-effecting logger).
- **Fix:** Added `plugin/host/builtins.go` with a `logBuiltin` + `hostBuiltins()`, injected into every plugin's predeclared set in `LoadDir`. Fixtures use `log(...)`.
- **Files modified:** plugin/host/builtins.go, plugin/host/manager.go
- **Verification:** Fixtures load + the greeter hook's `log` call runs under Emit; all tests green.
- **Committed in:** `5e59e754` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 missing-critical/consistency).
**Impact on plan:** The `log` builtin was an explicit either/or the plan left to executor discretion; choosing it added one small file. No scope creep — payloads stay frozen scalars, no server/world/level imports, no speculative machinery.

## Issues Encountered
- `go mod tidy` prunes a require with no importing package (the documented Phase-21 gotcha): the `toml` require disappeared after the first tidy because `manifest.go` did not yet exist. Handled per the plan — anchored both deps via a temporary `deps.go` blank import for the Task-1 commit, then dropped the `toml` anchor once `manifest.go` imported it for real, leaving only the fsnotify anchor for the Plan-02 watcher.

## Known Stubs
None. The `capabilities` manifest field is parsed + stored but intentionally NOT enforced — this is the locked CONTEXT decision (enforcement lands in Phase 23 when frozen handles exist), not a stub: the field flows through `Manifest.Capabilities` and is available to Phase 23.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Plan 02 (Wave 2) wiring is ready: the server adds one-line `Manager.Emit(...)` calls at the named discrete seams (`destroyAndAck`, `reconcileEdit`/`handleUseItemOn`, the join seam + `removePlayer`, `structure_spawn` add, `combat.die`/`actuallyHurt`, `tickOnce`), each behind the bus's zero-subscriber guard. The fsnotify hot-reload watcher composes `Unload` + `LoadDirWith` and must swap on the tick goroutine (TICK-05) with a Docker `-race` reload-during-dispatch test.
- `LoadDirWith` is the reusable seam Plan 02's watcher reloads a single plugin through.
- No blockers.

## Self-Check: PASSED

All 14 created/modified files verified present on disk; all 3 task commits (`60bd0890`, `5e59e754`, `b5029609`) verified in git history.

---
*Phase: 22-plugin-host-event-bus*
*Completed: 2026-06-28*
