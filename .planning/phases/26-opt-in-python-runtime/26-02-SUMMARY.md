---
phase: 26-opt-in-python-runtime
plan: 02
subsystem: infra
tags: [python, cgo, gopy, off-tick, async, asyncIn2, ants, gil, plugin-runtime, build-tags]

# Dependency graph
requires:
  - phase: 26-opt-in-python-runtime
    provides: "the build-tag stub/impl pair (plugin/python), the PythonRuntime/PythonPlugin host interfaces + SetPythonRuntime, the gopy@python3.14 pin (behind the tag), the register-capture Wave-2 stub fleshed here"
  - phase: 08-async-substrate
    provides: "the off-tick → owner rejoin lane: asyncIn2 / applyAsyncResults / submitOrDrop / newAsyncPool / the pathReady id-carry-owner-re-resolve discipline"
  - phase: 22-plugin-host-event-bus
    provides: "the runtime-neutral host (Manager, Emit at the 8 discrete seams, the register-once concept, manifest.Runtime routing)"
provides:
  - "the OFF-TICK python execution lane: a runtime=python plugin's hook is submitted to a bounded ants pluginPool (GIL-held on a LockOSThread-pinned worker) and rejoins the tick via pythonHookReady on asyncIn2 — NEVER on the tick goroutine"
  - "pythonHookReady asyncResult (server/async_python.go) — cgo-free plain-value rejoin (the pathReady twin), applyTo on the owner does telemetry-only (no world mutation)"
  - "the SAME register(event,fn) API for python (plugin/python/register_python.go) routed by manifest.Runtime — host.Emit offers each python plugin every discrete event off-tick via SetPythonDispatch/emitPython"
  - "the GIL-held off-tick CallHook (plugin/python/dispatch_python.go) + the serialized-interpreter DECISION (subinterp_python.go, cited from gopy source)"
  - "the build-tag-split server wiring (WirePython: tagged registers the gopy loader + dispatch, !python no-op) keeping the default server graph cgo-free"
affects: [26-03, wave-2-off-tick-python-lane, world-mutation-bridge]

# Tech tracking
tech-stack:
  added: []  # no new dep — reuses gopy@python3.14 (26-01) + the existing ants/asyncIn2 substrate
  patterns:
    - "the pathReady discipline applied to plugin dispatch: submitOrDrop(pluginPool) off-tick → pythonHookReady on asyncIn2 → applyTo on the owner (the ONLY tick-state touch, TICK-05)"
    - "cgo-free off-tick plumbing default-built (pythonHookReady, submitPythonHook, pluginPool use the host.PythonPlugin interface — gopy is one interface hop away, inside CallHook behind the tag), only the gopy WIRING (WirePython) is build-tag split"
    - "same register API routed by manifest.Runtime: Event.toArgs() marshals the SAME scalars as toStarlark; host.emitPython offers every discrete event to every python plugin off-tick (per-event subscription lives in the plugin's own captured hooks, not the host bus)"

key-files:
  created:
    - server/async_python.go
    - server/async_python_python.go
    - server/async_python_stub.go
    - server/async_python_test.go
    - server/async_python_python_test.go
    - plugin/python/register_python.go
    - plugin/python/dispatch_python.go
    - plugin/python/subinterp_python.go
    - plugin/python/offtick_python_test.go
    - server/testdata/plugins_python/heavylogger/plugin.toml
    - server/testdata/plugins_python/heavylogger/main.py
  modified:
    - plugin/python/runtime_python.go
    - plugin/host/manager.go
    - plugin/host/emit.go
    - plugin/host/event.go
    - server/tick.go
    - cmd/sulfur/main.go

key-decisions:
  - "GIL/sub-interpreters: SERIALIZED FALLBACK (one process-global interpreter), NOT sub-interpreters — CITED from the pinned gopy@python3.14 source: gopy's GIL model is the single-interpreter PyGILState API (PyGILState_Ensure/Release), and a whole-module grep finds ZERO sub-interpreter surface (no Py_NewInterpreterFromConfig, no PyInterpreterConfig, no PEP-684 per-interp-GIL) in every non-test .go AND the cgo headers; the alpha condition mandates the documented fallback. The PLUGIN-06 gate passes either way (it needs python off-tick + CGO=0-default, NOT N-way parallelism). NOT faked."
  - "submitPythonHook is UNTAGGED/cgo-free (the plan's explicit allowance): it talks to the python runtime via host.PythonPlugin, so the gopy call is one interface hop away inside CallHook behind //go:build python. Only WirePython (the gopy loader + dispatch registration) is build-tag split."
  - "the python rejoin (pythonHookReady) does telemetry-only on the owner (pythonHooksApplied/pythonHookErrors counters) — NO world mutation (that is Plan 26-03's request→apply bridge), matching the Phase-26 off-tick-observe gate."
  - "host.Emit calls emitPython FIRST (before the starlark zero-subscriber check) because a python plugin's hooks live in its own handle, not m.hooks — so the starlark short-circuit must not skip python dispatch; emitPython is a cheap nil-check no-op on a server with no python plugin."

patterns-established:
  - "Pattern: the off-tick plugin lane = pathReady for plugins. submitPythonHook → submitOrDrop(t.pluginPool) → worker CallHook (GIL-held) → t.asyncIn2 <- pythonHookReady → applyAsyncResults drains it on the owner → applyTo. Reuses the proven substrate; no parallel mechanism."
  - "Pattern: keep the default graph cgo-free by routing through a plain-Go interface (host.PythonPlugin) and a build-tag-split WIRING function (WirePython python/stub), so the cgo-free lane plumbing is default-built and only the gopy registration is gated."

requirements-completed: [PLUGIN-06]

# Metrics
duration: 11min
completed: 2026-06-28
---

# Phase 26 Plan 02: The off-tick Python execution lane Summary

**A runtime="python" plugin's hook now runs OFF the tick goroutine — submitted to a bounded ants `pluginPool` (GIL-held on a LockOSThread-pinned worker via gopy), rejoining the tick through a cgo-free `pythonHookReady` on the existing `asyncIn2` seam (the `pathReady` discipline applied to plugin dispatch), using the SAME `register(event, fn)` API as Starlark routed by `manifest.Runtime`; sub-interpreters were verified ABSENT in gopy@python3.14 and the serialized-interpreter fallback was taken with the source-cited reason — all while `CGO_ENABLED=0 go build ./...` stays pure-Go static with ZERO gopython in the import graph.**

## Performance

- **Duration:** ~11 min
- **Started:** 2026-06-28T18:37:33Z
- **Completed:** 2026-06-28T18:48:34Z
- **Tasks:** 3
- **Files modified:** 17 (11 created, 6 modified)

## Accomplishments
- **THE #1 GATE holds on every task:** `CGO_ENABLED=0 go build ./...` exits 0 and `go list -deps ./... | grep -i gopython` is EMPTY. The whole off-tick lane plumbing (`pythonHookReady`, `submitPythonHook`, `pluginPool`, the telemetry counters, `emitPython`, `WirePython` stub) is DEFAULT-BUILT and cgo-free; only the gopy CALL (inside `plugin/python.CallHook`) and the gopy WIRING (`server/async_python_python.go`) live behind `//go:build python`.
- **The off-tick lane (PLUGIN-06):** a python hook is submitted via `submitOrDrop(t.pluginPool, ...)` (the existing drop-on-overload helper), runs GIL-held on a `LockOSThread`-pinned worker (gopy `NewLock`), and rejoins via `pythonHookReady` on `asyncIn2` — drained by `applyAsyncResults` on the owner, where `applyTo` does the ONLY tick-state touch (telemetry counters; NO world mutation). Python NEVER runs on the tick goroutine.
- **Same register API, routed by manifest.Runtime:** `plugin/python/register_python.go` injects a `register(event, fn)` builtin (gopy `NewCFunction` into the plugin globals dict) that captures hooks into `Runtime.hooks` at Load — the IDENTICAL concept Starlark uses. `host.Emit` → `emitPython` offers every discrete event to every loaded python plugin off-tick via the `SetPythonDispatch` callback the server wires to `submitPythonHook`. Starlark stays inline-on-tick, unchanged.
- **Sub-interpreter decision (verified, not faked):** read the pinned gopy@python3.14 source — gopy's GIL model is the single-interpreter `PyGILState` API and there is ZERO sub-interpreter surface anywhere in the module (Go or C). Took the SERIALIZED one-interpreter fallback with the cited evidence in `subinterp_python.go`; sized the `pluginPool` small (the single GIL serializes python). The gate passes regardless of N-way parallelism.
- **Additive + safe:** the default-build tests prove a python manifest is skipped gracefully (no gopy) and the inline starlark path is unaffected; the cgo-free rejoin + off-tick round-trip are covered with a pure-Go fake plugin locally.

## Task Commits

Each task was committed atomically:

1. **Task 1: the off-tick python lane (pythonHookReady rejoin + GIL-held dispatch + the wiring)** - `771ef48d` (feat)
2. **Task 2: heavylogger plugin + off-tick register-and-fire tests** - `ee114ac8` (test)
3. **Task 3: sub-interpreter decision — serialized fallback, cited** - `9f44f918` (docs)

**Plan metadata:** (final docs commit — this SUMMARY + STATE + ROADMAP)

## Files Created/Modified
- `server/async_python.go` - `pythonHookReady` (cgo-free plain-value rejoin, both builds) + `applyTo` (owner-side telemetry) + `submitPythonHook` (off-tick submit via `submitOrDrop` → `asyncIn2`)
- `server/async_python_python.go` - `//go:build python` — `WirePython` registers the gopy loader (`pythonRuntimeAdapter`) + routes hooks to `submitPythonHook`
- `server/async_python_stub.go` - `//go:build !python` — `WirePython` no-op (keeps the default server graph cgo-free)
- `server/tick.go` - `pluginPool` (small, drop-on-overload) + `pythonHooksApplied`/`pythonHookErrors` + `PythonHookStats`; constructed in `NewTickLoop`, released in `Close`
- `plugin/python/runtime_python.go` - init/lifecycle only (`ensureInit` once-per-process, `Runtime` holds `hooks` + retained `globals`, `Close` drops refs)
- `plugin/python/register_python.go` - `//go:build python` — `Load` injects the `register(event,fn)` builtin (`NewCFunction` + globals `Dict`) + the capture closure (`registerBuiltin`)
- `plugin/python/dispatch_python.go` - `//go:build python` — `CallHook`: GIL-held off-tick call (`NewLock` → `CallGoArgs` → `Decref` → `Unlock`)
- `plugin/python/subinterp_python.go` - `//go:build python` — the serialized-interpreter DECISION + the cited gopy-source evidence
- `plugin/host/manager.go` - `pythonDispatch` field + `SetPythonDispatch` + `emitPython` (off-tick offer to each python plugin)
- `plugin/host/emit.go` - `Emit` calls `emitPython` first (before the starlark zero-subscriber check)
- `plugin/host/event.go` - `Event.toArgs() []any` (the SAME scalars as `toStarlark`, for the python lane) on all 8 event types
- `cmd/sulfur/main.go` - `WirePython(tick, pluginMgr)` before `LoadDir`
- `server/testdata/plugins_python/heavylogger/{plugin.toml,main.py}` - a `runtime="python"` plugin: `register("on_block_break", ...)` doing heavy off-tick aggregation
- `server/async_python_test.go` / `server/async_python_python_test.go` / `plugin/python/offtick_python_test.go` - the default + tagged lane tests

## Decisions Made
- **Serialized one-interpreter fallback (NOT sub-interpreters), cited.** See key-decisions above. Evidence is reproducible: a whole-module grep of the pinned gopy commit (`b0bdc04a384b`) finds no sub-interpreter API in any non-test `.go` or the cgo headers, and gopy's GIL model is the single-interpreter `PyGILState_Ensure/Release`. Recorded in `subinterp_python.go` + the `TestPythonSubInterpOrSerialized` consistency test.
- **`submitPythonHook` is cgo-free / untagged** (the plan's explicit allowance) — it uses the `host.PythonPlugin` interface; only `WirePython` (the gopy registration) is build-tag split. This keeps the lane plumbing default-built and tested locally with a pure-Go fake.
- **Telemetry-only owner-side effect** for `pythonHookReady.applyTo` (no world mutation — that is Plan 26-03).

## Deviations from Plan

None — plan executed as written. (Two in-task structural choices the plan explicitly delegated, not behavioral deviations: (a) `submitPythonHook` was kept UNTAGGED/cgo-free per the plan's "the interface is cgo-free, so submitPythonHook itself MAY be untagged — if so, drop the tag and keep ONE file" allowance, with the gopy wiring moved to a separate build-tag-split `WirePython`; (b) the `Runtime` retains the plugin globals dict so the tagged test can read the aggregation total back — a test-affordance, dropped in `Close`.)

## Known Stubs

None. The Wave-2 register-capture stub that 26-01 left in `Load` is now FULLY FLESHED (the `register(event,fn)` builtin captures real callables via `NewCFunction` + the globals dict; `CallHook` calls them GIL-held). No remaining stubs in the python lane.

## Sub-interpreter Decision (the operator-directed gate, recorded per CONTEXT decision 5)

**DECISION: one process-global interpreter + serialized calls (the safe fallback). Sub-interpreters are NOT used and NOT faked.**

**Verified from the pinned gopy@python3.14 source** (`gopython.xyz/py/v14@...-b0bdc04a384b` in the module cache):
1. gopy's entire GIL model is the single-interpreter `PyGILState` API (`lock.go`: `PyGILState_Ensure`/`PyGILState_Release`, `InitAndLock` → process-global `Py_Initialize`, `Finalize` → `Py_Finalize`). `PyGILState_Ensure` is documented by CPython as assuming the single main interpreter — incompatible with per-sub-interpreter thread states.
2. A whole-module grep (`NewInterpreter|Py_NewInterpreter|PyInterpreterConfig|InterpreterConfig|EndInterpreter|sub.?interpreter|PyInterpreterState_New`) returns ZERO hits in every non-test `.go` AND in the cgo headers (`*.h`/`*.c`). No `Py_NewInterpreterFromConfig`, no `PyInterpreterConfig`, no PEP-684 per-interpreter-GIL knob.
3. gopy is an alpha (`v14.0.0-alpha.0...`) — "not cleanly usable in this alpha" is exactly the cited fallback condition (CONTEXT decision 5).

**Consequence:** the `pluginPool` is sized small (the single GIL serializes python regardless of pool size); `submitOrDrop` bounds the damage. The PLUGIN-06 gate requires python OFF-TICK + CGO=0-default, NOT N-way parallelism, so the fallback satisfies it. **Revisit when** gopy ships a stable `Py_NewInterpreterFromConfig`/per-interpreter-GIL binding, or on a free-threaded (PEP 703) 3.14 build — then one isolated sub-interpreter per worker becomes the throughput upgrade (implemented in `subinterp_python.go`, pool re-sized to NumCPU).

## Issues Encountered
- No C compiler / libpython3.14 on the local Windows box, so `CGO_ENABLED=1 go build -tags python` and `-race` cannot run locally — this is the documented split (mirrors the existing `-race` Docker gate). The local gate is `CGO_ENABLED=0 go build ./...` + the no-gopython grep + the CGO=0 test suite, all green. The tagged tests (`TestPythonHookOffTick`, `TestPythonRegisterAndFire`, `TestPythonSubInterpOrSerialized`) are written and gated to the python3.14 Docker image (`python3.14-dev` + `libffi-dev`) per `.github/workflows/python.md`. The tagged code was verified by reading the actual gopy source for every API used (`NewCFunction`/`NewDict`/`SetItemString`/`Tuple.GetIndex`/`Unicode.AsString`/`Base().CallGoArgs`/`Long.Int64`/`None`).

## Build Verification (local, the gates that MUST pass on Windows)
- `CGO_ENABLED=0 go build ./...` → exit 0 (pure-Go static) — every task.
- `go list -deps ./... | grep -i gopython` → EMPTY (the #1 isolation gate) — every task.
- `CGO_ENABLED=0 go vet ./server/ ./plugin/...` → clean.
- `CGO_ENABLED=0 go test ./plugin/... ./server/` → PASS (incl. `TestPythonLaneSkippedDefault`, `TestPythonLaneAdditiveToStarlark`, `TestPythonHookReadyRejoin`, `TestSubmitPythonHookOffTickRoundTrip`).
- `grep '//go:build python' plugin/python/subinterp_python.go` → present.

**The `-tags python` build + `-race` run in the python3.14 Docker image / CI, NOT locally** (no libpython3.14/gcc on Windows) — the documented split.

## Next Phase Readiness
- The off-tick lane is live and proven (default-built plumbing + tagged round-trip tests). Ready for 26-03 (the world-mutation bridge: an off-tick python hook produces a mutation REQUEST that the owner drains and applies through the Phase-23 tick-owned seams — `pythonHookReady.applyTo` is the owner-side hook where that apply lands, currently telemetry-only).
- No blockers. The serialized-interpreter decision is recorded + revisitable; the `-tags python` link/-race is Docker-gated as planned.

## Self-Check: PASSED

All 11 created files exist on disk; all 3 task commits (`771ef48d`, `ee114ac8`, `9f44f918`) exist in the git log.

---
*Phase: 26-opt-in-python-runtime*
*Completed: 2026-06-28*
