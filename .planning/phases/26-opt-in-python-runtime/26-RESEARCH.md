# Phase 26: Opt-in Python runtime — Research

**Researched:** 2026-06-28
**Domain:** A SECOND plugin runtime — idiomatic CPython embedded via `qur/gopy` (cgo + libpython), gated behind a `python` build tag so the DEFAULT binary stays pure-Go static (CGO_ENABLED=0), dispatched OFF-TICK only and rejoining the tick via the existing `asyncIn2`/ants async seam, reusing the runtime-neutral Phase-22 host + register + event API unchanged.
**Confidence:** HIGH on the gopy module facts (verified by cloning the `python3.14` branch and reading its source), HIGH on the async-seam + host wiring (verified against the actual `server/*.go` + `plugin/host/*.go` read this session), MEDIUM on the GIL/sub-interpreter execution strategy (a real design decision with two valid shapes — surfaced as an Open Question, not decided here).

## Summary

Phase 26 is an **integration-and-isolation** phase, not a from-scratch runtime phase. Three things already exist and are reused unchanged: (1) the Phase-22 host (`plugin/host`) is runtime-NEUTRAL — its `Manager`, `register(event, fn)` builtin, typed `map[EventType][]Hook` bus, manifest `Runtime` selector, and the 8 discrete event seams are all in place, and `LoadDir` already has the explicit `if man.Runtime != "starlark" { continue }` skip that Phase 26 fills in for `"python"`; (2) the async seam (`server/async.go` + `asyncIn2` + `applyAsyncResults` + the per-subsystem `ants` pools `pathPool`/`trackerPool`/`spawnPool`) is the EXACT, proven off-tick → owner rejoin mechanism Python plugins must use; (3) the `manifest.Runtime` field already reserves `"python"`. Phase 26 adds a Python execution lane that hangs off these three.

**The single load-bearing constraint** (PLUGIN-06, CLAUDE.md, the v4-PLAN "Python as default = NO"): `qur/gopy` is CPython-via-cgo (`import "C"` + `#cgo pkg-config: python-3.14-embed libffi`), so the instant any package in the default build graph imports it, `CGO_ENABLED=0 go build ./...` breaks. The mechanism that prevents this is a **build-tag split**: ALL gopy-importing code lives in files tagged `//go:build python`, paired with a no-tag stub file (`//go:build !python`) that satisfies the same internal interface and returns "python runtime not built in this binary". The default build compiles ONLY the stub, never sees `import "C"`, stays CGO=0 static. A `-tags python` build compiles the real impl, pulls libpython, links cgo. This must be proven by TWO build gates: `CGO_ENABLED=0 go build ./...` (default, must stay green and cgo-free) and `CGO_ENABLED=1 go build -tags python ./...` (the opt-in build).

**The second load-bearing constraint** (the off-tick rule): cgo call overhead + the GIL + Python's non-determinism make Python categorically unfit for `tickOnce`. A Python hook must NEVER run on the tick goroutine. Instead, when a discrete event fires for a `runtime="python"` plugin, the dispatch is **submitted to an off-tick worker** (an `ants` pool, exactly like `pathPool`), the Python runs there holding the GIL on a `runtime.LockOSThread()`-pinned goroutine, and any result rejoins the tick by sending an `asyncResult` on `asyncIn2` — drained by `applyAsyncResults` on the owner. This is the `pathReady`/`trackerDiffReady` pattern applied to plugin dispatch. Starlark dispatch stays inline-on-tick (Phase 22, unchanged); Python dispatch is async-off-tick — **same `register` API, different execution lane keyed by `manifest.Runtime`**.

**Module-path correction (must surface):** the v4-PLAN and PLUGIN-06 say `gopython.xyz/py/v3`. VERIFIED: the `python3.14` branch declares `module gopython.xyz/py/v14` and links `python-3.14-embed`. `gopython.xyz/py/v3` is the OLDER major line (currently Python 3.11, `v3.13.0-alpha.1`). To target Python 3.14 the import is `gopython.xyz/py/v14`, latest tag `v14.0.0-alpha.0` (alpha). The plan's `/py/v3` reference is stale — Phase 26 should pin `gopython.xyz/py/v14` (see Open Question 5).

**Primary recommendation:** Create a `plugin/python` package with a build-tag split: `runtime_python.go` (`//go:build python`, imports `gopython.xyz/py/v14`, owns Initialize/Lock/Load/Call/marshal) + `runtime_stub.go` (`//go:build !python`, same exported surface, returns a "not built in" error). Wire it into `host.Manager.LoadDir`: a `runtime="python"` manifest loads through the python runtime IF the tag is set, else logs "python runtime not built in this binary; skipping plugin X" and continues. Add an off-tick Python dispatch lane: a new `pluginPool *ants.Pool` (or reuse a dedicated one) on `TickLoop`, and a `pythonHookReady` `asyncResult` that carries the hook outcome back via `asyncIn2`. Phase 26 keeps Python read-only w.r.t. tick state — a Python hook receives the frozen scalar event payload, does heavy off-tick work, and its result rejoins via the async drain; it does NOT touch live world/entity state (that crosses the tick boundary unsafely — deferred). Gate: a `runtime="python"` plugin's `on_block_break` hook runs off-tick (proven by a goroutine/thread assertion), rejoins via `applyAsyncResults`, AND `CGO_ENABLED=0 go build ./...` (no `-tags python`) stays pure-Go static with zero cgo.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-06 | An opt-in Python runtime (`qur/gopy` @ branch `python3.14`, idiomatic CPython via cgo + libpython) behind a `python` build tag, so the DEFAULT (no-tag) build stays pure-Go static (CGO=0). For HEAVY off-tick plugins ONLY (never the per-tick hot path — cgo overhead + GIL + non-determinism), running on their own goroutines/pools and rejoining via the existing async seam (`asyncIn2`/ants). Same event/registration API as Starlark (author picks runtime per workload, not per API). A Python plugin runs off-tick, rejoins via the async seam; the default build is still pure-Go static. | gopy module/branch/build-req VERIFIED (Standard Stack). Build-tag stub+impl split (Architecture §Build-tag isolation; Code Examples). Off-tick dispatch lane keyed by `manifest.Runtime`, rejoin via the REAL `asyncIn2`/`applyAsyncResults`/`ants` pool names (Architecture §Off-tick lane; Don't Hand-Roll). Same `register`/event API reuse (Architecture §Same-API routing; the host is already runtime-neutral). Two-build matrix gate (Validation Architecture). GIL/sub-interp, marshal, world-access surfaced (Open Questions). |
</phase_requirements>

<user_constraints>
## User Constraints

> No `CONTEXT.md` exists for Phase 26 yet (this research feeds `/gsd-discuss-phase` / the planner). Constraints below are extracted VERBATIM from `v4-PLAN.md` (the plan of record), `REQUIREMENTS.md` (PLUGIN-06 + Out of Scope), and `CLAUDE.md`. Open Questions flag the kickoff decisions — do NOT decide them in the plan; surface them.

### Locked decisions (from v4-PLAN.md + REQUIREMENTS.md PLUGIN-06)
- **Python is OPT-IN, build-tag-gated.** `qur/gopy @ branch python3.14`, idiomatic CPython via cgo + libpython, behind a `python` build tag. The DEFAULT (no-tag) build MUST stay pure-Go static (CGO_ENABLED=0). "Python as the DEFAULT/core runtime" is explicitly OUT OF SCOPE — "cgo/libpython would break the CGO=0 static-binary value prop."
- **CGO_ENABLED=0 default binary** (CLAUDE.md): Python is "the ONE build-tag-gated exception." Gate includes `CGO_ENABLED=0 go build ./...` clean + the default build graph carries zero `import "C"`.
- **OFF-TICK ONLY.** "HEAVY off-tick plugins ONLY (never the per-tick hot path — cgo overhead + GIL + non-determinism), running on their own goroutines/pools and rejoining via the existing async seam (`asyncIn2`/ants)." Python must NEVER run inside `tickOnce`.
- **Same event/registration API as Starlark.** "Author picks the runtime per workload, not per API." The host (Phase 22) is already runtime-neutral; the `runtime` manifest selector exists.
- **Reuse the existing async seam.** Rejoin via `asyncIn2`/ants — the SAME pattern pathfinding uses (`pathReady`/`pathPool`). Do not build a parallel async mechanism.
- **1:1 jar mandate does NOT apply to Phase 26.** No gameplay logic — this is runtime/infra plumbing (Python plugins authored against the API are user code, free of the mandate by definition).
- **No Co-Authored-By / no Claude attribution** in commits (CLAUDE.md).
- **Push to `development`** (v4-PLAN constraint #5).

### Claude's Discretion
- The exact package layout (recommended: `plugin/python` with the build-tag split — see Architecture).
- The off-tick dispatch mechanism details (recommended: a dedicated `ants` pool + a `pythonHookReady` `asyncResult` on `asyncIn2`, mirroring `pathPool`/`pathReady` — see Architecture).
- The stub's exact "not built in" error message + how `LoadDir` reports a skipped python plugin.
- Whether the build-tagged Python lane lives in `plugin/python` only, or also needs a tagged shim in `server/` for the off-tick submit site.

### Open decisions to surface, NOT decide (kickoff)
- **GIL / sub-interpreter strategy** (Open Q 1): one interpreter + serialize via a single `Lock` on a pinned worker, vs. PEP-734 sub-interpreters (3.14) for parallel python, vs. the 3.14 free-threaded build. Default recommendation: ONE interpreter, serialized — simplest correct first cut.
- **Marshal surface** (Open Q 2): the event payload is currently frozen Starlark scalars; for Python it must marshal to Python ints/strings/floats. What types cross, and which direction (result back to Go).
- **Can a Python plugin touch the world?** (Open Q 3): recommendation NO in Phase 26 — off-tick cannot touch tick-owned state; it rejoins via the async drain. Mutations through the owner are a later phase.
- **libpython version pin** (Open Q 5): `python-3.14-embed` pkg-config name pins 3.14; the `gopython.xyz/py/v14` module + `v14.0.0-alpha.0` tag is alpha — confirm acceptable, and whether to vendor/replace-pin the branch.

### Deferred Ideas (OUT OF SCOPE for Phase 26)
- **Python as the default/core runtime** → permanently out of scope (REQUIREMENTS Out of Scope).
- **A Python plugin mutating live world/entity state** → not Phase 26 (off-tick can't touch tick-owned state; a future owner-side seam if ever wanted).
- **Per-entity-per-tick Python scripting** → permanently out of scope.
- **Folia region-awareness of the python lane** → Phase 27 (regionize a working single-lane seam later).
- **A Python equivalent of the Phase-23 frozen entity/world/nav handle API** → not in Phase 26 (Phase 26 passes only the frozen scalar payload, like Phase 22).
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **CGO_ENABLED=0 default binary** — the single most important constraint. Python is "the ONE build-tag-gated exception." The default build graph (no `-tags python`) must carry zero `import "C"` and zero gopy import. Gate: `CGO_ENABLED=0 go build ./...` clean + a grep proving no `gopython.xyz` / `import "C"` reachable from the default build.
- **`go test -race` non-negotiable** for concurrency seams. The off-tick Python dispatch + the `asyncIn2` rejoin is a new concurrency path; it is covered by the Docker `-race` gate (CGO=1). Note: the `-tags python` race test ALSO needs libpython present in the CI image.
- **TICK-05 single-owner** carries into the rejoin: the Python worker computes off-tick; the ONLY tick-state mutation happens in `pythonHookReady.applyTo` on the owner (the `asyncIn2` discipline), exactly like `pathReady.applyTo`.
- **No built-but-unwired code rule:** build the minimal python lane PLUGIN-06 requires (load + off-tick dispatch + rejoin + the stub). Do NOT pre-build sub-interpreter pools, a world-mutation seam, or a Python frozen-handle API speculatively.
- **Push to `development`.**

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Decide a plugin is `runtime="python"` | `plugin/host` (LoadDir, EXISTING skip branch) | — | The manifest `Runtime` selector already routes; Phase 26 fills the python branch. |
| Initialize CPython + hold the GIL | `plugin/python` (`//go:build python`) | cgo/libpython | All `import "C"` is confined to the tagged impl file; nothing else may import gopy. |
| Load a `.py` plugin + capture its hooks | `plugin/python` (tagged) | `plugin/host` (the register-equivalent) | Python reuses the register-once concept; the captured callable is a `*py.Object`. |
| Stub the python lane when the tag is OFF | `plugin/python` (`//go:build !python`) | — | The no-tag stub keeps the default build cgo-free; returns "not built in". |
| Submit a Python hook OFF-tick | `server/` (the discrete seam) → an `ants` pool | `plugin/python` (runs the hook) | The seam decides WHEN; the pool runs Python off the tick goroutine (GIL-held, OS-thread-pinned). |
| Rejoin a Python result to the tick | `server/` (`pythonHookReady.applyTo` on `asyncIn2`) | `applyAsyncResults` (owner drain) | The proven off-tick→owner rejoin; the ONLY mutation is owner-side. |
| Run a Python hook ON the tick | **NOBODY — forbidden** | — | cgo + GIL + non-determinism. Python NEVER runs in `tickOnce`. |
| Starlark hook dispatch | `plugin/host` (inline `Emit` on tick) | — | Unchanged from Phase 22 — Starlark stays inline-on-tick. |

**Key boundary:** the gopy dependency is reachable ONLY through `//go:build python` files. `plugin/host` stays runtime-neutral and cgo-free (it already is). The off-tick submit site in `server/` that hands a python hook to the pool must itself be behind the tag OR call through a build-tag-split shim so the default `server` build never imports `plugin/python`'s tagged side. Event payloads remain PLAIN FROZEN SCALARS (the Phase-22 `Event.toStarlark` shape) — Python marshals them to Python scalars; no live `*Entity`/`*tickPlayer` crosses.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `gopython.xyz/py/v14` | branch `python3.14`, tag `v14.0.0-alpha.0` `[VERIFIED: git ls-remote + cloned go.mod, 2026-06-28]` | Idiomatic CPython embedding via cgo: `Initialize`/`InitAndLock`, `RunString`/`RunFile`/`Import`, `Lock`/`Unlock` (GIL), `Call`/`CallGo`/`CallGoArgs`, value marshal. | The runtime PLUGIN-06 names (`qur/gopy @ python3.14`). The `python3.14` branch declares this module path and links `python-3.14-embed`. CPython is the real-Python ecosystem the opt-in lane exists for. |
| libpython 3.14 (system, dev/embed) | 3.14.x | The C library gopy links against via cgo. Resolved by `pkg-config --cflags --libs python-3.14-embed`. | Required at BUILD and RUN time for the `-tags python` binary ONLY. The default CGO=0 binary never links it. |
| libffi (system) | latest | gopy's `#cgo pkg-config: python-3.14-embed libffi` also links libffi. | Verified in the cgo preamble of `python.go` / `gen_types`. Needed alongside libpython for the tagged build. |

**Module-path note (CORRECTION — surface to operator):** PLUGIN-06 + v4-PLAN say `gopython.xyz/py/v3`. That path is the OLD line (Python 3.11, `v3.13.0-alpha.1`, Dec 2024). The `python3.14` branch is `module gopython.xyz/py/v14`. To get Python 3.14 the import path is `gopython.xyz/py/v14`. `[VERIFIED: cloned go.mod on python3.14 declares `module gopython.xyz/py/v14`; pkg-config is `python-3.14-embed`]`

**Pin mechanism:** the latest published tag is `v14.0.0-alpha.0` (ALPHA). Options: (a) `go get gopython.xyz/py/v14@v14.0.0-alpha.0`; (b) pin the branch commit `b0bdc04a384b443df2279101ac335b5808727793` (HEAD of `python3.14` as of 2026-06-28) via a pseudo-version; (c) `replace`-pin a vendored fork (the project already FORKS go-mc, so a vendored gopy fork is in-house precedent). Decide at kickoff (Open Q 5). Because it is behind a build tag, the alpha status only affects operators who opt into Python — the default binary is unaffected.

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/panjf2000/ants/v2` | v2.12.1 (already in go.mod) | The off-tick worker pool that runs Python hooks. A dedicated `pluginPool` (or reuse the pattern of `pathPool`). | The Python execution lane — exactly the substrate `pathPool` already uses. No new dep. |
| (stdlib) `runtime` | — | `runtime.LockOSThread()` — gopy's `Lock` already calls it; the worker goroutine must stay pinned to its OS thread while holding the GIL (CPython per-thread state). | Every off-tick Python call. gopy handles this inside `Lock()`, but the worker design must not migrate goroutines mid-call. |
| (the existing `asyncIn2` + `asyncResult` seam) | — | The rejoin channel + interface a Python result implements (`pythonHookReady applyTo(*TickLoop)`). | The off-tick→owner rejoin. Already in `server/tick.go` + `server/async.go`. No new dep. |

**No new Go dependency beyond gopy** — the async substrate (`ants`, the `asyncIn2` channel, `applyAsyncResults`) is already present and is the correct reuse. Do NOT introduce a second async mechanism, a channel-based python broker, or `cgo.Handle` plumbing the gopy lib already provides.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `gopython.xyz/py/v14` (qur/gopy) | `go-python/gopy` (codegen, generates a CPython EXTENSION from a Go pkg) | **Reject — wrong direction.** `go-python/gopy` generates a Python-importable extension FROM Go; we need to EMBED CPython INTO Go and call Python from Go. `qur/gopy` is the embed-and-call library PLUGIN-06 names. |
| `gopython.xyz/py/v14` | `github.com/tliron/py4go`, raw `cgo` against `Python.h` | py4go is less battle-tested; raw cgo means hand-rolling the entire GIL/refcount/marshal layer gopy already provides. Reject — PLUGIN-06 names gopy and it ships the idiomatic surface. |
| Build-tag split (`//go:build python` + stub) | A runtime plugin (`-buildmode=plugin`) loaded via `plugin.Open` | **Reject.** Go's `plugin` package is Linux/macOS-only, fragile across versions, and STILL requires cgo. The build-tag split is the standard, portable, compile-time isolation. |
| Off-tick via `ants` + `asyncIn2` | A bare `go func(){...}` per python hook | **Reject.** Unbounded goroutines + the GIL = thread thrash; the result must rejoin on the owner, not mutate tick state from the goroutine. The `ants` + `asyncIn2` pattern is the proven, bounded, single-owner-rejoin discipline (`pathPool`/`pathReady`). |
| One interpreter + serialize | PEP-734 sub-interpreters / free-threaded 3.14 | Sub-interpreters allow parallel python but add isolation complexity + per-interp module state; free-threaded 3.14 is now supported (PEP 779) but a different build of libpython. ONE interpreter serialized is the simplest correct first cut. Surface as Open Q 1. |

**Installation (the `-tags python` build only):**
```bash
# system: libpython 3.14 with the embed pkg-config + libffi
#   debian/ubuntu: apt-get install python3.14-dev libffi-dev  (or build CPython --enable-shared)
#   verify: pkg-config --exists python-3.14-embed && pkg-config --libs python-3.14-embed
go get gopython.xyz/py/v14@v14.0.0-alpha.0   # or pin the python3.14 branch commit
go mod tidy
CGO_ENABLED=1 go build -tags python ./...
```

**Version verification (run at execution):**
```bash
git ls-remote --heads https://github.com/qur/gopy | grep python3.14   # branch exists
go list -m gopython.xyz/py/v14@latest                                  # confirm the pin
pkg-config --modversion python-3.14-embed                              # libpython present
```

## Architecture Patterns

### System Architecture Diagram

```
  BUILD TIME — the tag decides which files compile
  ─────────────────────────────────────────────────────────────────────
   go build ./...                       go build -tags python ./...
   (DEFAULT, CGO_ENABLED=0)             (OPT-IN, CGO_ENABLED=1)
        │                                    │
        ▼                                    ▼
   plugin/python/runtime_stub.go        plugin/python/runtime_python.go
   //go:build !python                   //go:build python
   - NO import "C"                      - import "gopython.xyz/py/v14"
   - NO gopy import                     - import "C" (transitively, via gopy)
   - Load() returns                     - Load() really inits CPython,
     errPythonNotBuilt                    runs the .py, captures hooks
   ⇒ pure-Go static, links nothing      ⇒ cgo binary, links libpython+libffi

  ═══════════════════════ runtime: a runtime="python" plugin ═══════════════════════
  LOAD TIME (host goroutine, before the tick loop)
   plugins/heavylogger/
   ├── plugin.toml  (runtime = "python", entrypoint = "main.py")
   └── main.py
        │ host.Manager.LoadDir: man.Runtime == "python"
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ default build:  log "python runtime not built in"; SKIP       │
   │ -tags python:   plugin/python.Load(entry) → InitAndLock once, │
   │                 RunFile(main.py), capture register()'d hooks  │
   │                 into the host bus keyed runtime="python"      │
   └──────────────────────────────────────────────────────────────┘
  ═══════════════════════════ TICK TIME (tick goroutine) ════════════════════════════
   A DISCRETE OCCURRENCE: server/block_break.go destroyAndAck() → block removed
        │  t.plugins.Emit(EventBlockBreak, payload)   (EXISTING Phase-22 call)
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ host.Manager.Emit / dispatch keyed by the hook's runtime:     │
   │                                                                │
   │  runtime="starlark" hook → INLINE on the tick (Phase 22, ✓)   │
   │                                                                │
   │  runtime="python"   hook → SUBMIT to pluginPool (ants) ───────┼──┐ OFF-TICK
   │     (NEVER starlark.Call/py.Call on the tick goroutine)       │  │
   └──────────────────────────────────────────────────────────────┘  │
                                                                       ▼
                          ┌────────────────────────────────────────────────────┐
                          │ pluginPool worker (off-tick goroutine):             │
                          │  lock := py.NewLock()  // GIL + LockOSThread         │
                          │  defer lock.Unlock()                                │
                          │  hookFn.CallGoArgs(x, y, z, state, playerID)         │
                          │  // heavy work: aggregate, log, external IO, ML      │
                          │  build an IMMUTABLE result value                     │
                          │  t.asyncIn2 <- pythonHookReady{...}  ── rejoin       │
                          └────────────────────────────────────────────────────┘
                                                                       │
   ┌──────────────────────────────────────────────────────────────┐  │
   │ applyAsyncResults() on the OWNER (UNCHANGED seam):            │◄─┘
   │   drains asyncIn2; pythonHookReady.applyTo(t) runs HERE —     │
   │   the ONLY tick-state touch, owner-side (TICK-05)             │
   └──────────────────────────────────────────────────────────────┘
```

### Pattern 1: The build-tag stub/impl split (THE isolation primitive)
**What:** Two files in `plugin/python`, mutually exclusive by tag, exporting the SAME surface. One real (imports gopy), one stub (cgo-free). The package compiles in BOTH builds; only the file body differs.
**Why:** This is what keeps `CGO_ENABLED=0 go build ./...` green. The default build sees only the stub — no `gopython.xyz/py/v14`, no `import "C"`. Verified: gopy's cgo preamble is `#cgo pkg-config: python-3.14-embed libffi` + `import "C"`, so any transitive import of it forces cgo.
```go
// plugin/python/runtime_stub.go
//go:build !python

package python

import "errors"

// errNotBuilt is returned by every entry point when the binary was built
// WITHOUT -tags python. The default (CGO=0) build compiles ONLY this file, so
// it never imports gopy and never pulls libpython/cgo.
var errNotBuilt = errors.New("python: runtime not built in this binary (rebuild with -tags python)")

// Available reports whether the Python runtime is compiled in.
func Available() bool { return false }

// Load is the no-tag stub: it refuses, so a runtime="python" plugin is skipped
// gracefully on a default build.
func Load(entrypoint string) (*Runtime, error) { return nil, errNotBuilt }

// Runtime is an opaque handle; on the stub side it carries nothing.
type Runtime struct{}
```
```go
// plugin/python/runtime_python.go
//go:build python

package python

import (
    "fmt"

    "gopython.xyz/py/v14" // CPython via cgo — pulls #cgo pkg-config: python-3.14-embed libffi
)

func Available() bool { return true }

// Runtime owns the single CPython interpreter + the captured hooks.
type Runtime struct {
    lock  *py.Lock            // GIL handle from InitAndLock (held by the worker per call)
    hooks map[string]*py.Object // event name -> the registered python callable
}

func Load(entrypoint string) (*Runtime, error) {
    // ... InitAndLock once (process-global), RunFile(entrypoint), capture hooks ...
    // (full body in Code Examples)
    return nil, fmt.Errorf("python: load %s: not yet implemented", entrypoint)
}
```

### Pattern 2: The off-tick dispatch lane keyed by `manifest.Runtime`
**What:** When `Emit` finds a hook whose owning plugin is `runtime="python"`, it does NOT call inline; it submits the call to an `ants` pool. Starlark hooks keep the inline path.
**Why:** PLUGIN-06's off-tick rule. The submit is non-blocking (`submitOrDrop`, the existing drop-on-overload discipline in `server/async.go`), so a saturated pool drops the dispatch rather than stalling the tick.
**Where:** the routing key is the hook's runtime. The host already tags each `Hook` with its `plugin` name; extend `Hook` (or the loadedPlugin lookup) to carry `runtime` so `Emit`/dispatch can branch. The python branch must live behind a build-tag-split shim so `server`/`host` default builds never import the tagged code.
```go
// Conceptual — the routing branch (the exact home for this is a planner decision:
// host.Manager with a runtime-aware dispatch, or a server-side shim). Python
// submit + py.Call live behind //go:build python.
func (m *Manager) dispatch(h Hook, payload Event) {
    switch h.runtime {
    case "starlark":
        m.callHook(h, payload.toStarlark())          // INLINE on tick (Phase 22)
    case "python":
        submitPythonHook(h, payload)                  // OFF-TICK (build-tag-split)
    }
}
```

### Pattern 3: The `pythonHookReady` rejoin (the `pathReady` discipline applied to plugins)
**What:** A new `asyncResult` type carrying the IMMUTABLE outcome of an off-tick python hook, sent on `asyncIn2`, drained by `applyAsyncResults`, applied on the owner in `applyTo`.
**Why:** TICK-05 single-owner. The worker computed off-tick; the owner performs any tick-state effect. For Phase 26 the "effect" is minimal (the gate is "runs off-tick + rejoins") — likely just bookkeeping/telemetry, NOT world mutation (Open Q 3). It exactly mirrors `pathReady`/`trackerDiffReady`/`spawnCandidatesReady`.
```go
// server/async_python.go (no cgo here — carries plain values, like pathReady).
type pythonHookReady struct {
    plugin string // for attribution / drop if the plugin was unloaded between submit and apply
    event  string // which event this was
    // a plain-value outcome the off-tick python produced (e.g. a count, a flag).
    // NEVER a *py.Object or a live tick pointer — only marshalled-out Go scalars.
    note   string
}

func (r pythonHookReady) applyTo(t *TickLoop) {
    // owner-side, UNCHANGED applyAsyncResults seam. Re-validate (the plugin may
    // have been unloaded), then do the minimal owner-side effect (telemetry/log).
    // Phase 26 does NOT mutate world/entity state here (Open Q 3).
}
```

### Recommended Project Structure
```
plugin/
├── starlark/                 # Phase 21 (existing)
├── host/                     # Phase 22 (existing, runtime-neutral) — extend Hook with runtime
└── python/                   # Phase 26 (NEW)
    ├── runtime_python.go     # //go:build python  — gopy: Init/Lock/Load/Call/marshal
    ├── runtime_stub.go       # //go:build !python — same surface, errNotBuilt, cgo-free
    ├── marshal_python.go     # //go:build python  — Event scalars -> py args; py result -> Go
    ├── runtime_python_test.go# //go:build python  — load a .py, call a hook, GIL, off-tick
    ├── runtime_stub_test.go  # //go:build !python — Available()==false, Load errors cleanly
    └── testdata/
        └── heavylogger/
            ├── plugin.toml   # runtime = "python"
            └── main.py       # register("on_block_break", fn) — heavy off-tick work

server/
├── async.go                  # existing — pathReady/asyncIn2/submitOrDrop (REUSE)
└── async_python.go           # NEW (build-tag-split): pythonHookReady + the off-tick submit shim
```

### Anti-Patterns to Avoid
- **Importing gopy outside a `//go:build python` file.** The instant a non-tagged file (transitively) imports `gopython.xyz/py/v14`, the default `CGO_ENABLED=0 go build ./...` breaks. ALL gopy imports are tag-gated.
- **Calling `py.Call`/`RunString` on the tick goroutine.** The forbidden pattern — cgo overhead + GIL serialization + Python non-determinism in `tickOnce`. Always off-tick.
- **Holding the GIL across the tick boundary.** The GIL is acquired by the off-tick worker (`py.NewLock`) and released (`Unlock`) before the result rejoins. The owner never touches the GIL.
- **Letting the worker goroutine migrate OS threads while holding the GIL.** CPython uses per-thread state; gopy's `Lock` calls `runtime.LockOSThread()` for exactly this. Do not unlock the OS thread mid-call.
- **A `go func()` per python hook (unbounded goroutines).** Use the bounded `ants` pool + `submitOrDrop`; an unbounded spawn under a flood of events thrashes threads against the GIL.
- **Mutating tick-owned state from the off-tick worker.** The worker computes; `pythonHookReady.applyTo` on the owner is the only mutation point (TICK-05).
- **A stub that doesn't match the impl surface.** If the stub and the tagged impl export different signatures, one of the two builds fails to compile. Keep the exported surface byte-identical.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Embedding CPython / GIL / refcounts | Raw cgo against `Python.h` | `gopython.xyz/py/v14` (`InitAndLock`, `Lock`, `Call`, `Decref`) | gopy provides the idiomatic embed surface + GIL/OS-thread management (`Lock` calls `runtime.LockOSThread`). Hand-rolling the C API is the exact mistake PLUGIN-06 names gopy to avoid. |
| GIL acquire/release per call | Manual `PyGILState_Ensure`/`Release` choreography | `py.NewLock()` / `lock.Unlock()` (or `GILStateEnsure`) | `Lock` wraps the GIL + OS-thread pin + reentrancy count correctly. Verified in `lock.go`. |
| The off-tick worker pool | A custom goroutine pool / channel fan-out | the existing `ants` pool pattern + `submitOrDrop` (`server/async.go`) | Bounded, non-blocking, drop-on-overload — the proven `pathPool` substrate. |
| Rejoining off-tick results to the tick | A new result channel + drain loop | the existing `asyncIn2` + `applyAsyncResults` + `asyncResult.applyTo` | The proven Phase-8 single-owner rejoin (`pathReady`). Adding a parallel mechanism is duplication + a second TICK-05 surface. |
| The event/registration API for Python | A second event bus / register API | the Phase-22 host (already runtime-neutral; `register` + the bus + the manifest `Runtime` selector) | "Same event/registration API as Starlark" is a LOCKED decision. The host is built to be reused. |
| Compile-time runtime isolation | `-buildmode=plugin` / runtime dlopen | a `//go:build python` vs `!python` file split | Standard, portable, no Go `plugin`-package fragility, and it's the only thing that keeps the default build cgo-free. |
| Go↔Python value marshal | A custom serializer | gopy's `CallGoArgs(args ...any)` / `CallGo(args, kwds)` (converts Go `any` → Python objects) + `Object` accessors back | gopy auto-converts Go scalars to Python objects and back; `NewValue`/`CallGo` handle ints/strings/floats. Verified in `base.go`. |

**Key insight:** Phase 26 writes almost no new infrastructure. The host (register + bus + manifest selector), the async substrate (`ants` + `asyncIn2` + `applyAsyncResults`), and the CPython embed (gopy) all already exist. Phase 26 writes: the build-tag stub/impl pair, a small marshal shim, a `pythonHookReady` `asyncResult`, the off-tick submit shim, and the `LoadDir` python branch. The RISK is not algorithmic — it's (1) keeping gopy strictly behind the tag so the default build stays CGO=0, and (2) keeping every Python call off the tick goroutine.

## Common Pitfalls

### Pitfall 1: cgo leaks into the default build (breaks the CGO=0 static binary)
**What goes wrong:** A file that's NOT tagged `//go:build python` imports `gopython.xyz/py/v14` (directly or transitively — e.g. a `server` file that imports `plugin/python`'s tagged side without a stub). `CGO_ENABLED=0 go build ./...` then fails with a cgo error, OR silently produces a non-static binary linked against libpython.
**Why it happens:** gopy's package has `import "C"` + `#cgo pkg-config: python-3.14-embed libffi` in `python.go`/`lock.go`/`run.go` (VERIFIED). Any import path reaching it forces cgo. Go build tags are file-level, so one untagged importer defeats the isolation.
**How to avoid:** ALL gopy imports live in `//go:build python` files. The `server`-side off-tick submit shim must ALSO be build-tag-split (a `!python` stub that does nothing / drops, a `python` impl that submits to the pool). Add a CI grep gate: the default build graph must contain zero `gopython.xyz` import and zero `import "C"`.
**Warning signs:** `CGO_ENABLED=0 go build ./...` errors mentioning `pkg-config`/`python-3.14-embed`; `go list -deps ./cmd/sulfur | grep gopython` returns anything on a default build; the shipped binary is dynamically linked (`file sulfur` / `ldd`).

### Pitfall 2: a Python hook runs on the tick goroutine
**What goes wrong:** The dispatch routing calls `py.Call` inline (like the Starlark path) instead of submitting off-tick. Every block break / damage event now blocks `tickOnce` on a cgo crossing + the GIL — the tick stutters, and a slow Python hook hangs the server.
**Why it happens:** The Starlark path IS inline (Phase 22, correct for pure-Go sandboxed Starlark). Copying that path for Python is the natural-but-wrong move.
**How to avoid:** Route by `manifest.Runtime` — `python` ALWAYS submits to the off-tick pool; only `starlark` runs inline. A test asserts a python hook executes on a DIFFERENT goroutine than the tick (capture goroutine IDs / a thread-local marker).
**Warning signs:** MSPT spikes when a python plugin is loaded; the tick goroutine's stack shows `py.Call`/`PyObject_Call`; a sleeping python hook freezes the whole server.

### Pitfall 3: the GIL serializes ALL Python — a slow hook stalls every python hook
**What goes wrong:** With ONE interpreter + one GIL, two python hooks can't run truly in parallel even across pool workers — whoever holds the GIL blocks the rest. A heavy hook starves others.
**Why it happens:** CPython's GIL. One interpreter = one GIL = serialized python execution regardless of pool size.
**How to avoid:** Phase 26 default: accept serialization (the lane is for HEAVY but not necessarily concurrent plugins; the off-tick pool + drop-on-overload bounds the damage). Size the python pool small. For real parallel python, Open Q 1 (sub-interpreters / free-threaded 3.14) — but do NOT build that speculatively. Use `lock.UnblockThreads()` around a hook's blocking IO so other python can run during a wait.
**Warning signs:** Python pool workers serialize despite a pool size > 1; one hook's latency tracks the slowest concurrent hook.

### Pitfall 4: libpython not present → the `-tags python` build/run fails
**What goes wrong:** `CGO_ENABLED=1 go build -tags python ./...` fails with `pkg-config: python-3.14-embed not found`, or the binary builds but fails at runtime to `dlopen` libpython.
**Why it happens:** The embed pkg-config + the shared libpython must be installed on the build AND run host. Python 3.14 with `--enable-shared` (or the distro `python3.14-dev`) provides `python-3.14-embed.pc`.
**How to avoid:** The `-tags python` Docker image installs `python3.14-dev libffi-dev` (or builds CPython `--enable-shared`). Document `pkg-config --exists python-3.14-embed` as a precondition. The DEFAULT image stays distroless-static (no python).
**Warning signs:** `pkg-config --exists python-3.14-embed` returns non-zero; `ldd sulfur-python | grep -i python` empty after a -tags build that "succeeded" against the wrong python.

### Pitfall 5: the worker goroutine migrates OS threads while holding the GIL
**What goes wrong:** A python call that yields (blocking IO, a `time.Sleep` in Go around the call, a channel op) lets the Go scheduler move the goroutine to a different OS thread; CPython's per-thread state is now invalid → crash or corruption.
**Why it happens:** Go goroutines aren't pinned to OS threads by default; CPython requires the thread holding the GIL to be the thread with the matching thread-state.
**How to avoid:** gopy's `Lock()` calls `runtime.LockOSThread()` and `Unlock()` undoes it. Keep the entire python call between `Lock`/`Unlock` on one goroutine; do NOT do Go channel/blocking ops between them without `UnblockThreads`. The `ants` worker runs the lock→call→unlock as one unit.
**Warning signs:** Intermittent crashes in `-tags python` under load; `fatal: PyGILState` errors; corruption only under concurrency.

### Pitfall 6: `-race` for the python lane needs CGO=1 AND libpython
**What goes wrong:** `CGO_ENABLED=0 go test -race -tags python ./...` is doubly impossible (`-race` needs cgo; `-tags python` needs libpython). And the default `-race` job (CGO=1, no `-tags python`) does NOT exercise the python lane.
**How to avoid:** THREE gates, not in conflict: (a) default static build `CGO_ENABLED=0 go build ./...` (proves CGO=0 + the stub compiles); (b) default race `CGO_ENABLED=1 go test -race ./...` (Phase-21/22 gate, covers the host + the stub); (c) python-lane race `CGO_ENABLED=1 go test -race -tags python ./...` in an image WITH libpython 3.14 (covers the off-tick lane + the rejoin). Gate (c) is NEW CI surface — the image needs python3.14-dev.
**Warning signs:** the python lane has no `-race` coverage; CI `-tags python` job fails to find libpython.

### Pitfall 7: the stub and impl drift out of sync
**What goes wrong:** A new exported function is added to `runtime_python.go` but not `runtime_stub.go` (or with a different signature). One of the two builds fails to compile.
**How to avoid:** Treat the exported surface as a contract; every exported symbol exists in BOTH files with identical signatures. Both builds run in CI (the default build AND a `-tags python` build), so drift is caught.
**Warning signs:** `go build ./...` (default) fails with "undefined: python.X" after a `-tags python`-only addition.

## Code Examples

### The build-tag stub + impl pair (the isolation primitive)
```go
// plugin/python/runtime_stub.go
//go:build !python
package python

import "errors"

var ErrNotBuilt = errors.New("python: runtime not built (rebuild with -tags python)")

func Available() bool                                 { return false }
func Load(entrypoint string) (*Runtime, error)        { return nil, ErrNotBuilt }

type Runtime struct{}
func (r *Runtime) Close() {}
```
```go
// plugin/python/runtime_python.go
//go:build python
package python

import (
    "fmt"
    "gopython.xyz/py/v14" // VERIFIED module path on the python3.14 branch; pulls cgo+libpython
)

var initLock *py.Lock // InitAndLock returns a LOCKED Lock; we Unlock after setup

func Available() bool { return true }

type Runtime struct {
    hooks map[string]*py.Object // event name -> the python callable captured at load
}

// Load initializes CPython ONCE (process-global) and runs the plugin's .py,
// which registers its hooks. Source: VERIFIED against gopy lock.go (InitAndLock)
// + run.go (RunFile) + base.go (Call) on the python3.14 branch.
func Load(entrypoint string) (*Runtime, error) {
    if initLock == nil {
        initLock = py.InitAndLock()  // Py_Initialize + GIL + LockOSThread, returns LOCKED
        defer initLock.Unlock()      // release the GIL after setup so workers can acquire it
    } else {
        initLock.Lock(); defer initLock.Unlock()
    }
    rt := &Runtime{hooks: map[string]*py.Object{}}
    // expose a register(event, fn) builtin to the .py, then:
    if _, err := py.RunFile(entrypoint, py.FileInput, nil, nil); err != nil {
        return nil, fmt.Errorf("python: run %s: %w", entrypoint, err)
    }
    // rt.hooks now holds the registered callables (captured by the register builtin)
    return rt, nil
}
```

### An off-tick python hook call (GIL-held, on a pool worker)
```go
// plugin/python/dispatch_python.go  //go:build python
// Runs on an ants pool worker — NEVER the tick goroutine. Source: VERIFIED gopy
// lock.go (NewLock/Unlock) + base.go (CallGoArgs converts Go scalars to py args).
func (rt *Runtime) CallHook(event string, args ...any) error {
    fn, ok := rt.hooks[event]
    if !ok {
        return nil // no python hook for this event
    }
    lock := py.NewLock()        // acquire GIL + LockOSThread for THIS goroutine
    defer lock.Unlock()         // release GIL + UnlockOSThread
    res, err := fn.Base().CallGoArgs(args...) // args: ints/strings/floats -> python objects
    if err != nil {
        return fmt.Errorf("python hook %s: %w", event, err)
    }
    if res != nil { res.Decref() } // gopy returns New References; drop it
    return nil
}
```

### The off-tick submit + rejoin (REUSING the existing async seam)
```go
// server/async_python.go  //go:build python  (the !python twin is a no-op drop)
// Submits a python hook to a bounded ants pool with the EXISTING drop-on-overload
// discipline; the worker rejoins via asyncIn2 (the pathReady pattern).
func (t *TickLoop) submitPythonHook(rt *python.Runtime, event string, args []any) {
    submitOrDrop(t.pluginPool, func() {           // existing helper in server/async.go
        err := rt.CallHook(event, args...)        // OFF-TICK, GIL-held
        note := "ok"; if err != nil { note = err.Error() }
        t.asyncIn2 <- pythonHookReady{event: event, note: note} // rejoin on the OWNER channel
    })
}
```
```go
// server/async_python_result.go  (NO cgo — plain values, compiled in BOTH builds)
type pythonHookReady struct {
    event string
    note  string // a marshalled-out Go scalar outcome; NEVER a *py.Object or live pointer
}

// applyTo runs on the OWNER inside applyAsyncResults (UNCHANGED seam). Phase 26
// does the minimal owner-side effect (telemetry); it does NOT mutate world state.
func (r pythonHookReady) applyTo(t *TickLoop) {
    // owner-side: e.g. bump a telemetry counter / log. No world/entity mutation (Open Q 3).
}
```

### A Python plugin + its manifest (reuses the SAME register API as Starlark)
```toml
# plugins/heavylogger/plugin.toml
name = "heavylogger"
version = "0.1.0"
entrypoint = "main.py"
runtime = "python"        # the Phase-22 selector — routes to the off-tick python lane
```
```python
# plugins/heavylogger/main.py — same register(event, fn) concept as a .star plugin
counts = {}
def on_break(x, y, z, state, player_id):
    # HEAVY off-tick work: aggregate, write a log, hit an external service, run ML.
    # This runs on a pool worker, never on the tick — latency here does NOT stall the server.
    counts[(x, y, z)] = counts.get((x, y, z), 0) + 1

register("on_block_break", on_break)
```

### The CGO=0-default verification (the gate)
```bash
# DEFAULT build: pure-Go static, no python, no cgo.
CGO_ENABLED=0 go build ./...                       # must succeed
go list -deps ./cmd/sulfur | grep gopython         # must print NOTHING (no gopy in default graph)
# OPT-IN build: cgo + libpython linked.
CGO_ENABLED=1 go build -tags python ./...          # needs python-3.14-embed + libffi present
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `gopython.xyz/py/v3` (Python 3.11 line, the path PLUGIN-06 quotes) | `gopython.xyz/py/v14` (the `python3.14` branch, `python-3.14-embed`) | qur/gopy moved its module major to track the python minor; `python3.14` branch added | Phase 26 must import `/py/v14` to get 3.14, not `/py/v3`. `[VERIFIED]` |
| CPython GIL mandatory, sub-interpreters share state | PEP 703 free-threaded build officially SUPPORTED (PEP 779) in 3.14; PEP 734 isolated sub-interpreters in `concurrent.interpreters` | Python 3.14 (2025–2026) | A future option for PARALLEL python off-tick (Open Q 1) — but a different libpython build / more isolation work. Not Phase 26's default. `[CITED: docs.python.org/3/whatsnew/3.14.html]` |
| Go `-buildmode=plugin` for optional native runtimes | Build-tag (`//go:build`) stub/impl split | long-standing Go idiom | The portable, CGO=0-preserving isolation for an opt-in cgo dependency. |

**Deprecated/outdated for our use:**
- `gopython.xyz/py/v3` for a 3.14 target — it's the 3.11 line. Use `/py/v14`.
- Go `+build python` legacy comment syntax — use `//go:build python` (modern, since Go 1.17).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The `register(event, fn)` concept can be exposed to a `.py` plugin the same way the Starlark host injects `register` — i.e. gopy lets Go register a callable into the interpreter that captures the python hook into `rt.hooks`. gopy supports building Go-backed callables (`CFunction`/class methods, verified in `cfunction_gen.go`/`class.go`), so this is mechanically available, but the EXACT capture wiring (a Go builtin the `.py` calls, vs. reading a module-level dict after `RunFile`) is a planner design choice. | Architecture / Code Examples | Low–Medium — both shapes work; the planner picks one. The "read a registered dict after RunFile" fallback needs no Go-callable-into-python at all. |
| A2 | The off-tick submit shim in `server/` can be build-tag-split cleanly so the default `server` package never imports `plugin/python`'s tagged side. The existing `t.plugins.Emit` call sites are unconditional; routing python off-tick needs the dispatch to know a hook's runtime AND to reach the tagged submit path only under `-tags python`. | Architecture §Pattern 2 | Medium — this is the main integration design question. A clean approach: `host.Manager` exposes a `runtime`-tagged hook list; a `server` `//go:build python` file owns the submit; the `!python` twin drops python hooks (logs "python not built"). Verify the package boundaries at planning. |
| A3 | Phase 26 keeps Python read-only w.r.t. tick state (the hook gets the frozen scalar payload, its result is telemetry/bookkeeping, not a world mutation). PLUGIN-06's gate is "runs off-tick + rejoins via the async seam" — it does NOT require a world-mutation-from-python capability. | Architecture §Pattern 3 / Open Q 3 | Low — matches the locked off-tick rule; a world-mutation seam is explicitly deferrable. If the operator wants python to affect the world, that's a `pythonHookReady.applyTo` owner-side effect — a bigger design (Open Q 3). |
| A4 | The server seam function names cited (`destroyAndAck`, `applyAsyncResults`, `asyncIn2`, `submitOrDrop`, `pathPool`/`pathReady`, `t.plugins.Emit`) are current as of this session's read of `server/*.go` + `plugin/host/*.go`. | throughout | Low — VERIFIED this session by direct read; names are the stable anchors. A new `pluginPool` field + `pythonHookReady` type are additive. |
| A5 | `v14.0.0-alpha.0` (alpha) is an acceptable pin for an opt-in, build-tag-gated runtime, OR the project vendors/replace-pins the `python3.14` branch (as it already forks go-mc). | Standard Stack / Open Q 5 | Low–Medium — alpha API may shift; but it only affects operators who opt into python. The fork precedent (go-mc) makes a vendored pin in-house-standard. |

## Open Questions

> Kickoff decisions to SURFACE for the operator / discuss-phase — do NOT decide in the plan.

1. **GIL / sub-interpreter / free-threaded strategy.**
   - What we know: one interpreter + one GIL serializes all python; gopy's `Lock`/`UnblockThreads` manage it correctly. Python 3.14 supports PEP-734 isolated sub-interpreters (`concurrent.interpreters`) and the PEP-779 free-threaded build (officially supported, ~5–10% single-thread penalty).
   - What's unclear: does Phase 26 need PARALLEL python (multiple heavy hooks at once), or is serialized off-tick enough?
   - Recommendation: **ONE interpreter, serialized via a single `Lock` on the off-tick pool** — simplest correct first cut, matches "heavy off-tick" (latency-tolerant, not throughput-critical). Defer sub-interpreters / free-threaded to a later phase if profiling demands. Do NOT build sub-interpreter pools speculatively.

2. **Marshal surface (which types cross, which direction).**
   - What we know: the Phase-22 payload is frozen Starlark scalars (`int`/`string`/`float`). gopy's `CallGoArgs(args ...any)` converts Go scalars → python objects, and python results come back as `*py.Object` (accessors back to Go). The `pythonHookReady` rejoin must carry only marshalled-out Go scalars.
   - What's unclear: does a python hook RETURN anything Phase 26 acts on, or is it fire-and-forget (effect = the heavy work it does, result = telemetry)?
   - Recommendation: Phase 26 = **fire-and-forget + a status/error rejoin** (no semantic return value acted on). A richer return-value contract is a later phase.

3. **Can a Python plugin touch the world?**
   - What we know: off-tick cannot touch tick-owned state (TICK-05). A python hook gets the frozen scalar payload; any world effect must rejoin via `pythonHookReady.applyTo` on the owner.
   - What's unclear: does Phase 26 expose ANY world-affecting capability, or is it strictly observe-and-process?
   - Recommendation: **NO world mutation from python in Phase 26.** The gate is "runs off-tick + rejoins" — keep it observe/aggregate/integrate. Owner-side effects (telemetry) only. World mutation = a future owner-side seam design.

4. **Where does the runtime-routing branch live + how is `server` kept cgo-free?**
   - What we know: `t.plugins.Emit` is unconditional; the python lane must be reached only under `-tags python`. The host `Hook` needs a `runtime` field (or a parallel python-hook list).
   - What's unclear: routing in `host.Manager` (runtime-aware dispatch) vs. a `server`-side build-tag-split shim vs. both.
   - Recommendation: extend `Hook` with `runtime`; put the python SUBMIT behind a `//go:build python` file in `server` with a `!python` no-op twin. Confirm the exact package boundaries at planning (A2).

5. **libpython version pin + gopy pin (alpha / branch / vendor).**
   - What we know: `python3.14` branch = `gopython.xyz/py/v14`, latest tag `v14.0.0-alpha.0`, links `python-3.14-embed`. The project already forks go-mc.
   - What's unclear: pin the alpha tag, pin the branch commit, or vendor/replace-pin a fork.
   - Recommendation: pin the branch commit (or `@v14.0.0-alpha.0`) initially; consider a vendored fork if the API shifts (go-mc precedent). Pin libpython to **3.14** via the embed pkg-config. Operator confirms python 3.14 availability on the build/run host.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `gopython.xyz/py/v14` module | PLUGIN-06 (`-tags python` only) | ✓ (branch `python3.14` exists; module verified) | branch HEAD `b0bdc04` / tag `v14.0.0-alpha.0` | — (named by the requirement) |
| `qur/gopy` `python3.14` branch | PLUGIN-06 | ✓ VERIFIED via `git ls-remote` | branch present (python2.6 → 3.14) | — |
| libpython 3.14 (embed, shared) | the `-tags python` build/run | ✗ on this host (default CGO=0 dev) | needs `python-3.14-embed.pc` | the DEFAULT build (CGO=0, no python) is the fallback — python is opt-in |
| libffi | the `-tags python` build | ✗ on this host | — | same — opt-in only |
| `panjf2000/ants/v2` | the off-tick pool | ✓ (in go.mod) | v2.12.1 | — |
| `asyncIn2`/`applyAsyncResults`/`submitOrDrop`/`pathPool` seam | the rejoin | ✓ (in `server/`) | — | — |
| Phase 22 host (`plugin/host`, runtime-neutral, `Runtime` selector) | the same-API reuse | ✓ (executed) | — | — |
| C compiler | the `-tags python` build + any `-race` | ✓ in CI (ubuntu) | — | n/a |

**Missing dependencies with no fallback:** none that block — libpython 3.14 + libffi are required ONLY for the opt-in `-tags python` build; the DEFAULT (CGO=0, no python) build needs neither and is the shipped artifact. The python lane's CI gate is the only place that needs python3.14-dev provisioned.
**Missing dependencies with fallback:** the entire python lane has a built-in fallback — without `-tags python`, the stub returns "not built in" and python plugins are skipped gracefully.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `sync`/goroutine-id assertion for the off-tick proof) |
| Config file | none (Go convention) |
| Default quick run | `go test ./plugin/python/ ./plugin/host/` (compiles the STUB side) |
| Python-lane run | `go test -tags python ./plugin/python/ ./server/` (needs libpython 3.14) |
| Default static gate | `CGO_ENABLED=0 go build ./...` (must stay green, cgo-free) |
| Default race gate | `CGO_ENABLED=1 go test -race ./...` (host + stub) |
| Python-lane race gate | `CGO_ENABLED=1 go test -race -tags python ./...` (off-tick lane + rejoin; needs libpython) |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| PLUGIN-06 | DEFAULT build is pure-Go static, NO cgo, NO gopy in the graph | build | `CGO_ENABLED=0 go build ./... && ! (go list -deps ./cmd/sulfur \| grep -q gopython)` | ✓ gate exists / extend |
| PLUGIN-06 | The `-tags python` build compiles + links libpython | build | `CGO_ENABLED=1 go build -tags python ./...` | ❌ Wave 0 (needs libpython CI) |
| PLUGIN-06 | Stub: `Available()==false`, `Load` returns "not built in" on default build | unit | `go test ./plugin/python/ -run TestStubNotBuilt` | ❌ Wave 0 |
| PLUGIN-06 | A `runtime="python"` plugin is SKIPPED gracefully (logged, not fatal) on a default build | unit | `go test ./plugin/host/ -run TestPythonRuntimeSkippedWhenNotBuilt` | ❌ Wave 0 |
| PLUGIN-06 | `-tags python`: a `.py` loads, `register("on_block_break", fn)` captures the hook | unit (tagged) | `go test -tags python ./plugin/python/ -run TestLoadAndRegister` | ❌ Wave 0 |
| PLUGIN-06 | A python hook runs OFF the tick goroutine (different goroutine/OS thread than `tickOnce`) | unit (tagged) | `go test -tags python ./server/ -run TestPythonHookOffTick` | ❌ Wave 0 |
| PLUGIN-06 | **The gate:** a python `on_block_break` hook runs off-tick AND rejoins via `applyAsyncResults` (a `pythonHookReady` lands on `asyncIn2`, drained on the owner) | integration (tagged) | `go test -tags python ./server/ -run TestPythonHookRejoinsViaAsyncSeam` | ❌ Wave 0 |
| PLUGIN-06 | The off-tick lane is race-clean (submit + GIL + `asyncIn2` rejoin) | race (tagged) | `CGO_ENABLED=1 go test -race -tags python ./server/ -run TestPythonLaneRace` | ❌ Wave 0 |
| PLUGIN-06 | GIL serialization is correct (concurrent submits don't corrupt the interpreter) | race (tagged) | `CGO_ENABLED=1 go test -race -tags python ./plugin/python/ -run TestGILConcurrent` | ❌ Wave 0 |

**The signature test** (proves the architecture, not the plumbing): `TestPythonHookRejoinsViaAsyncSeam` — load a `runtime="python"` plugin with an `on_block_break` hook that does work and sets a marshalled-out result; drive a real break through `destroyAndAck`; assert (a) the hook ran on a NON-tick goroutine, and (b) a `pythonHookReady` was drained by `applyAsyncResults` on the owner. PLUS the always-on `TestDefaultBuildIsCGO0` (the default-build-stays-static half of the two-build matrix).

### Sampling Rate
- **Per task commit:** `go test ./plugin/python/ ./plugin/host/` (stub side) + `CGO_ENABLED=0 go build ./...`.
- **Per wave merge / phase gate:** the FULL two-build matrix — (a) `CGO_ENABLED=0 go build ./...` + no-gopy-in-graph grep; (b) `CGO_ENABLED=1 go test -race ./...` (default); (c) `CGO_ENABLED=1 go build -tags python ./...` + `CGO_ENABLED=1 go test -race -tags python ./...` in the libpython image.
- **Phase gate (full):** both Docker images green — the default distroless-static (CGO=0, no python) AND the `-tags python` image (libpython 3.14 linked).

### Wave 0 Gaps
- [ ] `plugin/python/runtime_python.go` (`//go:build python`) + `runtime_stub.go` (`//go:build !python`) — the split with an identical exported surface.
- [ ] `plugin/python/testdata/heavylogger/{plugin.toml, main.py}` — a `runtime="python"` plugin that registers `on_block_break`.
- [ ] `server/async_python*.go` — the `pythonHookReady` `asyncResult` (cgo-free) + the build-tag-split off-tick submit shim + a `pluginPool` field on `TickLoop` (or reuse pattern).
- [ ] `host.Manager`: extend `Hook` with a `runtime` field + the `LoadDir` python branch (load via `plugin/python` when `Available()`, else log+skip).
- [ ] CI: a NEW `-tags python` job (build + `-race`) on an image with `python3.14-dev libffi-dev`; a NEW grep gate (no `gopython`/`import "C"` in the default `./cmd/sulfur` dep graph).
- [ ] `go get gopython.xyz/py/v14@<pin> && go mod tidy` — added ONLY to the module; it stays out of the default build graph via the build tag.
- [ ] A `-tags python` Dockerfile stage (or a separate Dockerfile) that installs libpython 3.14 + builds with `CGO_ENABLED=1 -tags python`.

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V5 Input Validation / untrusted-code execution | yes | A Python plugin is FULL CPython — NO sandbox (unlike Starlark). This is the key security delta: python plugins are TRUSTED operator-installed code, not a sandbox. The control is operational (only install trusted python plugins), not technical. Document this loudly. The off-tick pool + `submitOrDrop` bounds DoS-by-slowness; the build tag means python is absent unless the operator opted in. |
| V1.4 Trust boundaries / least privilege | partial | Python has NO step budget, NO recursion guard, NO I/O restriction (it's real Python with `os`/`socket`/`subprocess`). The trust boundary is "operator chose to build `-tags python` and install this plugin." Phase 26 exposes only the frozen scalar payload (no live world handle), narrowing the blast radius to what a hook can DO with full python + the event data. |
| V6 Cryptography | no | None in this phase. |
| V2/V3/V4 Auth/Session/Access | no | No network/auth surface; plugins are local operator files. |

### Known Threat Patterns for the python lane
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| A python hook runs arbitrary code (full CPython, no sandbox) | Elevation of Privilege / Tampering | OPERATIONAL: python is opt-in (`-tags python`), plugins are trusted operator-installed files. There is no technical sandbox (this is the documented difference from Starlark — heavy/dynamic = trust required). |
| A slow/blocking python hook stalls the server | Denial of Service | Off-tick ONLY (never `tickOnce`) + bounded `ants` pool + `submitOrDrop` drop-on-overload + GIL `UnblockThreads` around blocking IO. A slow hook delays its OWN lane, not the tick. |
| cgo leaks into the default binary (breaks the security-relevant static-binary property) | Tampering (supply chain / attack surface) | Build-tag isolation + the CI grep gate (no `gopython`/`import "C"` in the default graph) + the distroless-static default image. The default attack surface is unchanged from today. |
| Interpreter corruption from GIL/OS-thread mishandling | Denial of Service | gopy `Lock` (GIL + `LockOSThread`); the worker runs lock→call→unlock as one unit; `-race -tags python` gate. |
| A late python result applies to an unloaded plugin | Tampering | `pythonHookReady.applyTo` re-validates on the owner (drop if the plugin was unloaded between submit and apply) — the `pathReady` re-check discipline. |

## Sources

### Primary (HIGH confidence)
- **`qur/gopy` `python3.14` branch (CLONED + read this session)** — `go.mod` declares `module gopython.xyz/py/v14`, `go 1.22.0`; cgo preamble `#cgo pkg-config: python-3.14-embed libffi` (in `python.go`, `lock.go`, `run.go`, `cmd/gen_extension`, `examples/extension`); `lock.go` (`InitAndLock`/`NewLock`/`Lock`/`Unlock`/`UnblockThreads`/`GILStateEnsure`, `runtime.LockOSThread`); `python.go` (`Initialize`/`InitializeEx`/`Finalize`/`AddToPath`); `run.go` (`RunString`/`RunFile`/`StartToken`); `base.go` (`Call`/`CallGo`/`CallGoArgs`/`CallObject`). Branch list verified via `git ls-remote` (python2.6 → 3.14). Latest tag `v14.0.0-alpha.0`. `[VERIFIED 2026-06-28]`
- **`D:/ender/server/async.go`** (read in full) — `newAsyncPool`/`submitOrDrop` (the bounded non-blocking ants pool + drop-on-overload), `pathReady`/`trackerDiffReady`/`spawnCandidatesReady` (the `asyncResult` rejoin contracts + `applyTo` re-validation discipline), the `asyncIn2` rejoin invariant. `[VERIFIED]`
- **`D:/ender/server/tick.go` + `tick_phases.go`** — `asyncIn2 chan asyncResult` (buffered, `asyncIn2Buffer=256`), `applyAsyncResults` (the non-blocking owner drain), `pathPool`/`trackerPool`/`spawnPool` (ants pools), `asyncResult interface{ applyTo(*TickLoop) }`, `t.plugins *host.Manager` + `SetPlugins`/`PluginSwapChan`, the `if t.plugins != nil { t.plugins.Emit(...) }` discrete-seam calls (join/leave/tick). `[VERIFIED]`
- **`D:/ender/plugin/host/{manager.go, manifest.go, emit.go, register.go, event.go}`** — the runtime-neutral host: `Manifest.Runtime` ("starlark"/"python" reserved), the `if man.Runtime != "starlark" { continue }` skip in `LoadDir`, the `register` builtin capturing a callable, the `map[EventType][]Hook` bus, `Emit` (zero-sub guard + fresh-thread + isolation), the 8 `EventType` consts + frozen-scalar `Event.toStarlark` payloads. `[VERIFIED]`
- **`D:/ender/plugin/starlark/{runtime.go, loader.go}`** — `LoadWith(path, extra)`, `SafeGlobals`, `NewThread` (the surface Python's lane mirrors conceptually). `[VERIFIED]`
- **`D:/ender/{go.mod, Dockerfile, .github/workflows/go.yml, cmd/sulfur/main.go}`** — module `github.com/imhinotori/sulfur` (go 1.25.0), `ants v2.12.1`, the CGO=0 distroless-static build, the CI build(CGO=0)+race(CGO=1) split, the host wiring (`host.New`/`LoadDir`/`SetPlugins` + the fsnotify watcher). `[VERIFIED]`
- **`.planning/v4-PLAN.md` + `REQUIREMENTS.md` (PLUGIN-06 + Out of Scope)** — the opt-in/build-tag/off-tick/same-API locked decisions, "Python as default = NO". `[CITED]`

### Secondary (MEDIUM confidence)
- **pkg.go.dev `gopython.xyz/py/v3` + `qur.me/py/v3`** — the GIL/threading model narrative (Lock calls LockOSThread; InitAndLock convenience; GILState), the marshal philosophy, the v3=3.11 vs v14=3.14 mapping. Cross-checked against the cloned source. `[CITED]`
- **docs.python.org `whatsnew/3.14`** — PEP 779 free-threaded build supported, PEP 734 `concurrent.interpreters` sub-interpreters (the Open-Q-1 parallel-python options). `[CITED]`

### Tertiary (LOW confidence)
- None — every load-bearing claim is either VERIFIED against cloned gopy source / the actual Sulfur code, or CITED from official docs.

## Metadata

**Confidence breakdown:**
- gopy module facts (path `/py/v14`, branch, build req `python-3.14-embed libffi`, API surface): **HIGH** — cloned the `python3.14` branch and read `go.mod`/`lock.go`/`python.go`/`run.go`/`base.go` directly.
- Async-seam reuse (`asyncIn2`/`applyAsyncResults`/`ants`/`submitOrDrop`/`pathReady`): **HIGH** — read `server/async.go` + `tick.go` + `tick_phases.go` this session; the names are exact.
- Host runtime-neutrality + the `Runtime="python"` skip: **HIGH** — read `plugin/host/*.go`; the skip branch is already in `LoadDir`.
- Build-tag isolation pattern: **HIGH** — standard Go idiom; the gopy cgo preamble confirms why it's necessary.
- GIL / sub-interpreter EXECUTION strategy: **MEDIUM** — the mechanism is verified (gopy `Lock`), but one-interp-serialized vs sub-interpreters vs free-threaded is a real design choice (Open Q 1), recommended not decided.
- Module-path correction (`/py/v3` → `/py/v14`): **HIGH** — the plan's `/py/v3` is stale; verified the 3.14 branch is `/py/v14`.

**Research date:** 2026-06-28
**Valid until:** ~14 days — gopy's `python3.14` branch is ACTIVE/alpha (`v14.0.0-alpha.0`), so the API may shift; re-clone + re-verify the `lock.go`/`run.go`/`base.go` surface and re-run `git ls-remote --heads https://github.com/qur/gopy | grep python3.14` at execution. The Sulfur server seam NAMES (`asyncIn2`, `applyAsyncResults`, `submitOrDrop`, `pathReady`, `t.plugins.Emit`, `LoadDir`'s runtime skip) are stable anchors — re-grep by name if line numbers drift.
