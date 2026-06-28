---
phase: 26
slug: opt-in-python-runtime
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-28
---

# Phase 26 — Validation Strategy

> The load-bearing check: the DEFAULT (no-tag) build stays CGO=0 pure-Go static with ZERO gopy in the graph.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Default-build gate** | `CGO_ENABLED=0 go build ./...` (no `-tags python` — must be static, no gopy/cgo) |
| **Default test** | `CGO_ENABLED=0 go test ./plugin/... ./server/` |
| **Python-build** | `go build -tags python ./...` (CGO=1, links libpython3.14) |
| **Python test (Docker, CGO=1)** | an image with `python3.14-dev libffi-dev`: `docker run ... go test -tags python -race ./plugin/...` |
| **no-gopy-in-default-graph grep** | `go list -deps ./... \| grep -i gopython` → EMPTY on the default build |
| **Estimated runtime** | ~15s default; the python-tagged job needs the python3.14 image |

---

## Sampling Rate

- **After every task commit:** `CGO_ENABLED=0 go build ./...` (the static gate) + `CGO_ENABLED=0 go test ./plugin/...`
- **After every plan wave:** the two-build matrix — default CGO=0 static + `-tags python` build + the `-tags python` -race
- **Before `/gsd-verify-work`:** default build CGO=0 clean + no-gopy-in-graph + the python-tagged build/-race green
- **Max feedback latency:** ~15s default / the python image for the tagged job

---

## Per-Task Verification Map

> Provisional. The PLUGIN-06 load-bearing samples. The #1 invariant: the default build is untouched.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 26-01-01 | 01 | 1 | PLUGIN-06 | T-26-01 | build-tag isolation: //go:build python (real gopy @ /py/v14) + //go:build !python stub ("not built in"); DEFAULT build is CGO=0 static, ZERO gopy in the import graph | build | `CGO_ENABLED=0 go build ./...` exit 0 + `go list -deps ./... \| grep -i gopython` EMPTY | ❌ W0 | ⬜ pending |
| 26-01-02 | 01 | 1 | PLUGIN-06 | — | gopy @ python3.14 PINNED to a branch commit (module gopython.xyz/py/v14, NOT /py/v3); the -tags python build links python-3.14-embed + builds | build | `go build -tags python ./...` (on the python3.14 image) exit 0 | ❌ W0 | ⬜ pending |
| 26-01-03 | 01 | 1 | PLUGIN-06 | — | the runtime-routing interface keeps server/plugin-host cgo-free on the default build; manifest.Runtime=="python" → skipped/graceful-error without the tag, dispatched to the python lane with the tag | unit | `CGO_ENABLED=0 go test ./plugin/host/ -run TestRuntimeRoutingDefaultBuild` | ❌ W0 | ⬜ pending |
| 26-02-01 | 02 | 2 | PLUGIN-06 | T-26-02 | OFF-TICK lane: a python plugin hook is queued to the ants pool (LockOSThread worker, GIL-held), runs Python OFF the tick goroutine, rejoins via pythonHookReady on asyncIn2 drained on the owner | unit (tags python) | `go test -tags python ./plugin/... -run TestPythonHookOffTick` | ❌ W0 | ⬜ pending |
| 26-02-02 | 02 | 2 | PLUGIN-06 | — | same event/registration API as Starlark (the python plugin uses register, routed by manifest.Runtime); a python on_block_break hook fires (off-tick) | unit (tags python) | `go test -tags python ./plugin/... -run TestPythonRegisterAndFire` | ❌ W0 | ⬜ pending |
| 26-02-03 | 02 | 2 | PLUGIN-06 | T-26-03 | the WORLD-BRIDGE: a python mutation REQUEST (e.g. set_block) is queued off-tick → drained on the owner → applied through the Phase-23 tick-owned seam → -race clean. NO live handle escapes off-tick (request/apply indirection). Capability-gated. | race (tags python) | Docker `-tags python -race ./plugin/... ./server/` `-run TestPythonWorldBridge` | ❌ W0 | ⬜ pending |
| 26-02-04 | 02 | 2 | PLUGIN-06 | — | sub-interpreters: one per off-tick worker (3.14 per-interp GIL) for real parallelism IF gopy@3.14 cleanly exposes it; ELSE one interp + serialized calls (the safe fallback), with the WHY cited (not faked) | unit (tags python) | `go test -tags python ./plugin/... -run TestPythonSubInterpOrSerialized` | ❌ W0 | ⬜ pending |
| 26-02-05 | 02 | 2 | PLUGIN-06 | T-26-01 | THE GATE: a python plugin runs off-tick + rejoins via the async seam + a mutation request applies on-tick; AND the default (no-tag) build is STILL pure-Go static (CGO=0, no libpython) | build+unit | the no-gopy-default grep + `go test -tags python ./...` green | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `plugin/host/python_runtime_python.go` (//go:build python — the real gopy impl) + `python_runtime_stub.go` (//go:build !python — the stub) + the runtime interface
- [ ] `plugin/host/python_lane_python.go` (the off-tick dispatch + the pythonHookReady async rejoin) — reuse server/async.go asyncIn2/submitOrDrop
- [ ] the world-bridge (the mutation-request queue + the tick-drain + apply-through-Phase-23-seam) + the capability gate
- [ ] `plugin/host/python_test.go` (//go:build python) — off-tick fire, register, world-bridge, sub-interp-or-serialized
- [ ] testdata: a python plugin (manifest runtime="python" + a .py hook) + a custom-recipe/world-mutate test plugin
- [ ] `go get gopython.xyz/py/v14@<python3.14 branch commit>` (behind the tag); confirm the default build stays clean
- [ ] the CI matrix note: a `-tags python` job on python3.14-dev + libffi-dev; the default jobs stay pure-Go static

*THE #1 GATE: `go list -deps ./...` on the DEFAULT build has ZERO gopython — the build-tag isolation is the whole safety of "opt-in". If gopy leaks into the default graph, CGO=0 is broken.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| The `-tags python` build on a real python3.14 host | PLUGIN-06 | Needs libpython3.14-dev + libffi installed (not on every CI runner) | On a python3.14-dev image: `go build -tags python ./...` + `go test -tags python ./plugin/...` — the off-tick + world-bridge tests pass |

*The default-build CGO=0 gate is fully automated. The python-tagged build needs the python3.14 toolchain (Docker image) — automatable in CI with the right image, but flagged manual if the runner lacks it.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] The DEFAULT build CGO=0 static + no-gopy-in-graph is a hard gate
- [ ] The python-tagged build/-race is green on the python3.14 image
- [ ] No watch-mode flags
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
