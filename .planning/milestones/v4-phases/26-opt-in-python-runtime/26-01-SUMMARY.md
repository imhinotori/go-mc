---
phase: 26-opt-in-python-runtime
plan: 01
subsystem: infra
tags: [python, cgo, build-tags, gopy, libpython, plugin-runtime, isolation]

# Dependency graph
requires:
  - phase: 22-plugin-host
    provides: the runtime-neutral host (Manager, LoadDir, the manifest.Runtime selector, the bare runtime!=starlark skip)
provides:
  - "plugin/python: the //go:build python real gopy impl + the //go:build !python cgo-free stub (identical exported surface)"
  - "gopython.xyz/py/v14 pinned to the python3.14 branch commit, reachable ONLY behind //go:build python"
  - "plugin/host runtime-routing interface (PythonRuntime/PythonPlugin) + SetPythonRuntime seam — host stays cgo-free"
  - "LoadDir routes runtime=python via the interface (loaded with the tag, skipped+logged without it); unknown runtime errors loudly"
  - "the three-gate CI matrix notes (.github/workflows/python.md)"
affects: [26-02, 26-03, wave-2-off-tick-python-lane]

# Tech tracking
tech-stack:
  added:
    - "gopython.xyz/py/v14 @ v14.0.0-alpha.0.0.20260510154237-b0bdc04a384b (python3.14 branch commit b0bdc04a384b — behind //go:build python ONLY)"
  patterns:
    - "build-tag stub/impl split: //go:build python (real cgo) + //go:build !python (cgo-free stub), byte-identical exported surface"
    - "runtime-routing via a plain-Go interface in the host + a //go:build python registration seam (SetPythonRuntime) — concrete cgo impl plugs in behind the tag, host never imports gopy"

key-files:
  created:
    - plugin/python/doc.go
    - plugin/python/runtime_stub.go
    - plugin/python/runtime_python.go
    - plugin/python/runtime_stub_test.go
    - plugin/python/runtime_python_test.go
    - plugin/host/runtime.go
    - plugin/host/runtime_routing_test.go
    - plugin/host/testdata/python_plugins/heavylogger/plugin.toml
    - plugin/host/testdata/python_plugins/heavylogger/main.py
    - .github/workflows/python.md
  modified:
    - go.mod
    - go.sum
    - plugin/host/manager.go

key-decisions:
  - "gopy pinned to the python3.14 BRANCH commit b0bdc04a384b (pseudo-version v14.0.0-alpha.0.0.20260510154237-b0bdc04a384b), NOT the moving v14.0.0-alpha.0 tag — reproducible, and behind the tag only opt-in operators see the alpha"
  - "the host holds the python runtime BY INTERFACE (PythonRuntime/PythonPlugin); the concrete gopy impl registers via SetPythonRuntime from a //go:build python adapter at boot — keeps plugin/host + server cgo-free on the default build"
  - "the bare `if man.Runtime != \"starlark\" { continue }` skip became a real switch: starlark inline (unchanged), python routed via the interface, unknown runtime errors loudly (T-26-05 — no silent load)"

patterns-established:
  - "Pattern 1: build-tag stub/impl split is THE isolation primitive — the default CGO=0 build compiles only the cgo-free stub, the -tags python build compiles the gopy impl; both export the same surface so the package builds in both modes"
  - "Pattern 2: a cgo dependency stays out of the default import graph by being imported ONLY from a //go:build python file, even though it is a direct require in go.mod"

requirements-completed: [PLUGIN-06]

# Metrics
duration: 7min
completed: 2026-06-28
---

# Phase 26 Plan 01: The build-tag isolation primitive + runtime-routing seam Summary

**A `plugin/python` build-tag stub/impl pair (cgo-free `!python` stub + gopy `python` impl with an identical surface), gopy `gopython.xyz/py/v14` pinned to the python3.14 branch commit behind the tag only, and a plain-Go runtime-routing interface in `plugin/host` so `LoadDir` dispatches `runtime="python"` to the CPython lane — all while `CGO_ENABLED=0 go build ./...` stays pure-Go static with ZERO gopython in the import graph.**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-06-28T18:20:05Z
- **Completed:** 2026-06-28T18:26:40Z
- **Tasks:** 3
- **Files modified:** 13 (10 created, 3 modified)

## Accomplishments
- **THE #1 GATE holds:** `CGO_ENABLED=0 go build ./...` exits 0 and `go list -deps ./... | grep -i gopython` is EMPTY after every task — gopy never enters the default import graph. `plugin/host`, `server`, and `plugin/python` default graphs carry zero gopython/cgo packages.
- **The build-tag stub/impl pair** (`plugin/python`): `runtime_python.go` (`//go:build python`, imports `gopython.xyz/py/v14`, real `InitAndLock`/`RunFile`/GIL-held `CallHook`) + `runtime_stub.go` (`//go:build !python`, cgo-free, `ErrNotBuilt`) export a byte-identical surface (`Available`, `Runtime`, `Load`, `Runtime.Close`, `Runtime.CallHook`).
- **gopy pinned** to the python3.14 branch commit `b0bdc04a384b443df2279101ac335b5808727793` (resolved pseudo-version `v14.0.0-alpha.0.0.20260510154237-b0bdc04a384b`) as a direct require in `go.mod`, reachable only behind `//go:build python`.
- **Runtime routing:** the host's bare `runtime != "starlark"` skip became a real switch — `starlark` inline (unchanged), `python` routed via the `PythonRuntime`/`PythonPlugin` interface (loaded with the tag, logged+skipped without it), unknown runtime errors loudly. The concrete gopy impl plugs in via `SetPythonRuntime` from a tagged adapter, so the host stays cgo-free.
- **Default test suite untouched:** `CGO_ENABLED=0 go test ./plugin/... ./server/` passes (host, python-stub, starlark, server all green).

## Task Commits

Each task was committed atomically:

1. **Task 1: the build-tag stub/impl pair (the isolation primitive)** - `c7ca602b` (feat)
2. **Task 2: pin gopy @ python3.14 branch commit + CI matrix notes** - `e3fa8968` (chore)
3. **Task 3: the runtime-routing interface (host stays cgo-free)** - `cccb5be3` (feat)

**Plan metadata:** (final docs commit — this SUMMARY + STATE + ROADMAP)

## Files Created/Modified
- `plugin/python/doc.go` - package doc + the build matrix (no tag)
- `plugin/python/runtime_stub.go` - `//go:build !python` cgo-free stub (`ErrNotBuilt`, `Available()==false`)
- `plugin/python/runtime_python.go` - `//go:build python` real gopy impl (init-once, RunFile, GIL-held CallHook)
- `plugin/python/runtime_stub_test.go` - `//go:build !python` default-build test (stub refuses gracefully)
- `plugin/python/runtime_python_test.go` - `//go:build python` tagged load smoke test (python3.14 image only)
- `plugin/host/runtime.go` - the `PythonRuntime`/`PythonPlugin` interfaces + `SetPythonRuntime` seam (no tag, plain Go)
- `plugin/host/manager.go` - `pythonRuntime` field, `loadedPlugin.python`, the routing switch, Unload closes the python handle
- `plugin/host/runtime_routing_test.go` - default-build routing test (skip with no runtime, route via a pure-Go fake, unavailable-runtime skip)
- `plugin/host/testdata/python_plugins/heavylogger/{plugin.toml,main.py}` - a `runtime="python"` test plugin
- `go.mod` / `go.sum` - the gopy pin (with an explanatory comment so future `go mod tidy` keeps it)
- `.github/workflows/python.md` - the three-gate CI matrix notes

## Decisions Made
- Pinned gopy to the python3.14 **branch commit** (pseudo-version), not the moving `v14.0.0-alpha.0` tag — reproducible; the alpha only affects opt-in operators because it's behind the tag.
- The host holds the python runtime **by interface** and the concrete gopy impl registers via `SetPythonRuntime` from a tagged adapter — this is what keeps `plugin/host` and `server` cgo-free on the default build (26-CONTEXT decision 6).
- Added a comment block to the gopy require in `go.mod` documenting the tag-only reachability so a future `go mod tidy` and reviewers don't mistake it for a stray dep.

## Deviations from Plan

None — plan executed exactly as written. (Two trivial in-task adjustments, not behavioral deviations: removed an unused `initErr` package var from `runtime_python.go` to keep the tagged build clean; ran `gofmt -w` on the new files. Both inside the Task 1 commit.)

## Known Stubs

- **`plugin/python/runtime_python.go` `Load` — register-capture hook harvesting** is a documented Wave-2 stub: `RunFile` runs the plugin body for real (so the tagged build links libpython), but the `register(event, fn)` builtin that captures callables into `rt.hooks` is wired in Wave 2 (26-RESEARCH "the register-capture + CallHook GIL path"). The plan explicitly permits this — the load/init/run + the build are real; only the capture detail is deferred. This stub lives ONLY behind `//go:build python` and does not affect the default build or the #1 gate.

## Issues Encountered
- `go mod tidy` keeps the gopy require because the tagged import file is part of the module, but does NOT pull it into the default import graph (tidy honors build tags for graph reachability). Verified the default graph stays gopy-free after tidy. No issue — this is the intended behavior of the build-tag isolation.

## Build Verification (local, the gates that MUST pass on Windows)
- `CGO_ENABLED=0 go build ./...` → exit 0 (pure-Go static).
- `go list -deps ./... | grep -i gopython` → EMPTY (the #1 isolation gate).
- `CGO_ENABLED=0 go vet ./plugin/...` → clean.
- `CGO_ENABLED=0 go test ./plugin/... ./server/` → PASS (host, python-stub, starlark, server).
- `go.mod` pins `gopython.xyz/py/v14` at the python3.14 branch commit.

**The `-tags python` build (`CGO_ENABLED=1 go build -tags python ./...`) is verified in the python3.14 Docker image / CI, NOT locally on Windows** — there is no libpython3.14 (`python-3.14-embed`) on the Windows box. This is the expected, documented split (mirrors the existing `-race` Docker gate): the tagged code + tests are written and the link is gated to the python3.14-dev + libffi-dev image per `.github/workflows/python.md` gate (c).

## Next Phase Readiness
- The isolation primitive is proven and is the foundation Wave 2/3 hang off. Ready for 26-02 (the off-tick dispatch lane + the `pythonHookReady` rejoin via `asyncIn2`, and the register-capture + `CallHook` GIL path that fleshes out the Wave-2 stub above).
- No blockers. The `-tags python` link is unverified locally (Windows has no libpython3.14) but is gated to the python3.14 Docker image, exactly as planned.

## Self-Check: PASSED

All 11 created files exist on disk; all 3 task commits (`c7ca602b`, `e3fa8968`, `cccb5be3`) exist in the git log.

---
*Phase: 26-opt-in-python-runtime*
*Completed: 2026-06-28*
