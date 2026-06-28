# Phase 26: Opt-in Python runtime — Context

**Gathered:** 2026-06-28
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 26-RESEARCH.md (HIGH-confidence — cloned the python3.14 branch)
**Requirement:** PLUGIN-06

<domain>
## Phase Boundary

**Delivers:** an OPT-IN Python runtime behind a `python` build tag. The DEFAULT (no-tag) binary stays pure-Go static (CGO_ENABLED=0) and NEVER pulls libpython/cgo. Python runs HEAVY plugins OFF-TICK (own goroutine/pool, the GIL-bounded interpreter), rejoining the tick via the EXISTING async seam. It reuses the SAME event/registration API as Starlark (the host is already runtime-neutral — manifest.Runtime routes starlark vs python). Plus (operator-directed) a bridge so a Python plugin's mutations re-enter the tick goroutine via the async drain, and sub-interpreter parallelism.

**OUT:** Folia → Phase 27; the final visual/perf gate → Phase 28.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Dependency — qur/gopy @ python3.14, PINNED TO A BRANCH COMMIT
- **CORRECTED MODULE PATH (research finding — was wrong in v4-PLAN/REQUIREMENTS):** the module is **`gopython.xyz/py/v14`** (the python3.14 branch's go.mod declares `module gopython.xyz/py/v14`), NOT `gopython.xyz/py/v3` (v3 = the OLD 3.11 line). Phase 26 imports `/py/v14`. cgo preamble: `#cgo pkg-config: python-3.14-embed libffi`. The python3.14 branch is confirmed present (git ls-remote).
- **PIN to a specific python3.14 BRANCH COMMIT** (not the moving `v14.0.0-alpha.0` tag, not a fork). Reproducible; and because it's behind the `python` build tag, only opt-in operators are exposed to the alpha. The go-mc-style vendor/fork is NOT needed for an opt-in runtime. Update v4-PLAN/REQUIREMENTS/PROJECT to the corrected `/py/v14` path.

### Build-tag isolation — THE non-negotiable constraint
- ALL gopy code goes behind `//go:build python` (the real impl) + a cgo-free `//go:build !python` STUB (returns "Python runtime not built in"). So `CGO_ENABLED=0 go build ./...` (no tag) stays pure-Go static and NEVER links libpython. A manifest `runtime="python"` plugin is skipped/errors gracefully on the default build (the host already has `if man.Runtime != "starlark" { continue }`).
- VERIFY: the default build has ZERO cgo (no gopy in the import graph) — grep the build. Two builds: `go build` (default, CGO=0, no Python) and `go build -tags python` (CGO=1, libpython3.14 linked).

### Off-tick execution — the async lane (reuse the proven seam)
- Python NEVER runs on the tick goroutine (cgo overhead + GIL + non-determinism). It runs on its own pool, rejoining via the EXISTING async seam: `server/async.go` `asyncIn2` / `applyAsyncResults` / `submitOrDrop` (the bounded ants pools) + the `pathReady`/`applyTo` id-carry/owner-re-resolve discipline. A Python hook is `pathReady` applied to plugins: an event for a python plugin is QUEUED to an off-tick worker, Python runs there (holding the GIL on a LockOSThread-pinned worker), and any RESULT rejoins the tick via a new `pythonHookReady` on `asyncIn2`, drained on the owner.
- Starlark stays inline-on-tick (Phase 22); Python is async-off-tick — SAME register API, routed by manifest.Runtime. This is the dual-lane the host supports.

### Python CAN touch the world — via a bridge BACK to the tick (operator-directed)
- A Python plugin may REQUEST world/entity mutations; they are applied on the TICK GOROUTINE via the async drain (NOT directly off-tick — that would race tick-owned state). The Python hook produces a mutation REQUEST (e.g. set_block(x,y,z,state), spawn, set_velocity) off-tick; it's queued to the owner; the tick applies it through the SAME tick-owned seams Phase 23 uses (the thin-handle mutators / ChunkManager.SetBlock).
- **The planner keeps this MINIMAL + correct:** the bridge is a request-queue → tick-drain → apply-through-existing-seam path (reuse the Phase-23 mutators + the async-rejoin). A Python READ of world state likewise goes through a request→tick-resolve→return (snapshot copied back, since off-tick can't hold a live handle). Do NOT expose a live tick-owned handle to the off-tick Python goroutine (that's the race). The request/apply indirection is the safety boundary. Capability enforcement (Phase 23) applies to the requests.
- RISK FLAG: this is more surface than "observe-only". The planner scopes the mutation-request set minimally (start with set_block + a simple spawn/log), proves the round-trip (off-tick Python → request → tick applies → -race clean), and leaves the full mutation vocabulary as incremental.

### GIL — sub-interpreters (operator-directed, 3.14)
- Use Python 3.14's improved SUB-INTERPRETERS for real parallelism (one per off-tick worker), instead of a single serialized interpreter. Each sub-interpreter is isolated; the off-tick pool runs them in parallel.
- **The planner keeps this MINIMAL + correct:** verify gopy @ python3.14 actually exposes sub-interpreter creation + the per-sub-interp GIL (3.14's per-interpreter GIL). If sub-interpreters are NOT cleanly usable via gopy in this alpha, FALL BACK to one interpreter + serialized calls (the safe default) and record WHY (cited) — do NOT fake parallelism. State isolation + per-sub-interp marshalling is the complexity; scope it to what's provably correct. The gate does NOT require N-way parallelism to pass (it requires Python off-tick + CGO=0-default); sub-interpreters are the throughput upgrade, attempted but not faked.

### The gate
- A Python plugin runs OFF-TICK (e.g. an on_block_break hook doing heavy work — aggregation/logging), rejoins via the async seam; a Python mutation request applies on the tick (-race clean); AND the default (no-tag) build is STILL pure-Go static (CGO=0, no libpython). Two-build matrix: `CGO_ENABLED=0 go build ./...` (static, no gopy in graph) + `go build -tags python` (CGO=1, libpython3.14) + the `-tags python` -race job on an image with python3.14-dev + libffi-dev.

### Where the routing branch lives (research A2 — keep server cgo-free)
- The runtime-routing (starlark inline vs python off-tick, keyed by manifest.Runtime) must NOT drag cgo into the `server` package on the default build. The python impl lives behind the build tag in a python-tagged file (e.g. plugin/host/python_runtime_python.go + a _stub.go); the host's dispatch consults a runtime interface that has a no-op python impl on the default build. Confirm the import graph: `server`/`plugin/host` stay cgo-free without `-tags python`.
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 26 row + gate (opt-in Python, build-tag, off-tick, same API). **UPDATE the module path to /py/v14.**
- `.planning/REQUIREMENTS.md` — PLUGIN-06 full. **UPDATE /py/v3 → /py/v14.**
- `.planning/phases/26-opt-in-python-runtime/26-RESEARCH.md` — HIGH-confidence: the /py/v14 path correction, the build-tag stub/impl split, the off-tick async lane (real asyncIn2/applyAsyncResults/submitOrDrop/pathReady names), the runtime-neutral host, the 5 pitfalls (cgo leak, on-tick, GIL, libpython, OS-thread), the GIL/sub-interp open decision.
- `.planning/phases/22-...` + `plugin/host/manager.go` — the runtime-neutral host (the `if man.Runtime != "starlark"` skip), the register/event API Python reuses.
- `.planning/phases/23-...` — the tick-owned mutator seams the world-bridge applies through.
- `server/async.go` — asyncIn2 / applyAsyncResults / submitOrDrop / pathReady — the off-tick rejoin lane.
- `CLAUDE.md` — **CGO=0 DEFAULT mandate (Python is the ONE build-tag-gated exception)**, push development, -race Docker.
</canonical_refs>

<specifics>
## Specific Ideas

- The build-tag stub/impl pair is the linchpin: `//go:build python` (real gopy) + `//go:build !python` (stub). The default-build CGO=0 + no-gopy-in-graph grep is a gate.
- The off-tick lane = `pathReady` for plugins: queue the python hook to a worker pool, run Python (GIL-held on a LockOSThread worker), rejoin via `pythonHookReady` on asyncIn2, drained on the owner. Reuse the exact pattern.
- The world-bridge: off-tick Python → a mutation REQUEST → the owner drains it → applies through the Phase-23 tick-owned seam → -race clean. Reads: request → tick snapshots → return a copy (no live handle off-tick).
- Sub-interpreters: verify gopy/3.14 support; if clean, one sub-interp per worker (parallel); if not, one interp + serialized (fallback, cited). The gate passes either way.
- CI: a new `-tags python` build/-race job on a python3.14-dev + libffi-dev image; the default jobs stay the pure-Go static ones.

## Open items the planner resolves
- The exact pinned python3.14 commit (verify it builds with python-3.14-embed).
- The minimal mutation-request vocabulary for the world-bridge (start small, prove the round-trip).
- Sub-interp viability via gopy@3.14 → parallel vs the serialized fallback (cited).
- The runtime-interface shape that keeps server/plugin-host cgo-free on the default build.
- The off-tick worker pool sizing (reuse the ants pattern).
</specifics>

<deferred>
## Deferred Ideas

- A large Python mutation vocabulary (full entity/world API from Python) — start minimal, grow incrementally.
- Free-threaded (no-GIL) 3.14 build — beyond sub-interpreters; evaluate later if needed.
- Folia region-awareness of Python hooks → Phase 27.
</deferred>

---

*Phase: 26-opt-in-python-runtime*
*Context gathered: 2026-06-28 via operator decisions + v4-PLAN.md + 26-RESEARCH.md*
