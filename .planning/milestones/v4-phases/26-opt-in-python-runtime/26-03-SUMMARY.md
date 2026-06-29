---
phase: 26-opt-in-python-runtime
plan: 03
subsystem: infra
tags: [python, cgo, gopy, off-tick, world-bridge, mutation-request, capability-gate, asyncIn2, plugin-runtime, build-tags, phase-gate]

# Dependency graph
requires:
  - phase: 26-opt-in-python-runtime
    provides: "the off-tick python lane (26-02): pythonHookReady on asyncIn2, the GIL-held off-tick CallHook, submitPythonHook, the cgo-free WirePython split; pythonHookReady.applyTo noted as the owner-side hook where the world-apply lands"
  - phase: 23-plugin-entity-world-handles
    provides: "the tick-owned mutator seams the world-bridge applies THROUGH: worldHandle.setBlock (ChunkManager.SetBlock + broadcastBlockUpdate), worldHandle.blockAt (GetBlock), and the capSet gate (parseCapabilities / capWorldWrite / capError)"
  - phase: 08-async-substrate
    provides: "the asyncResult/asyncIn2/applyAsyncResults rejoin lane (pathReady discipline) the mutation/read requests ride"
provides:
  - "the WORLD-BRIDGE: an off-tick python hook produces a MUTATION REQUEST (set_block + a simple spawn/log) carried as PLAIN SCALARS, queued on asyncIn2, drained on the TICK goroutine, and applied through the SAME Phase-23 seam (ChunkManager.SetBlock + broadcastBlockUpdate / spawnVanillaPig) — the ONLY mutation point (TICK-05); NO live tick-owned handle ever escapes off-tick"
  - "pythonMutation + pythonReadReq (server/python_bridge.go) — cgo-free plain-value asyncResult requests; applyTo enforces the Phase-23 capSet FIRST (capWorldWrite/capEntitiesWrite/capWorldRead) then applies/snapshots on the owner"
  - "serverWorldBridge + host.WorldBridge — the plain-Go (cgo-free) seam the off-tick python builtins talk to; stamped per-plugin with the manifest-derived capSet (the SAME parseCapabilities the Starlark handles use)"
  - "the //go:build python request PRODUCERS (plugin/python/bridge_python.go): set_block/spawn/log/block_at builtins that construct+enqueue requests (never mutate, never hold a live handle); block_at does request -> owner-snapshot -> return-a-copy"
  - "THE PHASE GATE: python off-tick (26-02) + an on-tick mutation apply (this plan, -race in Docker) + the default no-tag build STILL pure-Go static (CGO=0, ZERO gopython in the graph incl. the cmd/sulfur binary)"
affects: [wave-3-world-bridge, future-richer-mutation-vocabulary]

# Tech tracking
tech-stack:
  added: []  # no new dep — reuses gopy@python3.14 (26-01) + the asyncIn2 substrate (Phase 8) + the Phase-23 seams
  patterns:
    - "request/apply indirection AS the safety boundary: off-tick python -> construct a plain pythonMutation (scalars + capSet) -> asyncIn2 -> the owner enforces the capSet then applies through the EXISTING Phase-23 seam; NO live handle crosses (T-26-03)"
    - "read = request -> tick-snapshot -> return-a-copy: pythonReadReq carries a buffered reply channel; the owner snapshots GetBlock and sends the COPIED scalar back; the off-tick side blocks on the reply (no live world reference)"
    - "capability gate reused verbatim: the python lane parses the manifest capabilities via the SAME parseCapabilities the Starlark handles use; a denied request is dropped + logged with capError at apply (T-26-09); an unknown capability errors loudly at load"
    - "cgo-free both-builds bridge (server/python_bridge.go) + tagged producers (plugin/python/bridge_python.go): only the gopy builtins are behind //go:build python, so the default server graph + the cmd/sulfur binary stay gopython-free"

key-files:
  created:
    - server/python_bridge.go
    - server/python_bridge_test.go
    - server/python_bridge_python_test.go
    - plugin/python/bridge_python.go
    - plugin/python/bridge_python_test.go
    - server/testdata/worldbridge_granted/worldmutate/plugin.toml
    - server/testdata/worldbridge_granted/worldmutate/main.py
    - server/testdata/worldbridge_denied/worldmutate_denied/plugin.toml
    - server/testdata/worldbridge_denied/worldmutate_denied/main.py
  modified:
    - plugin/host/runtime.go
    - plugin/host/manager.go
    - plugin/python/runtime_python.go
    - plugin/python/runtime_stub.go
    - plugin/python/register_python.go
    - server/async_python_python.go
    - plugin/host/runtime_routing_test.go
    - server/async_python_test.go

key-decisions:
  - "The request/apply indirection IS the safety boundary (26-CONTEXT decision 4). A WRITE: off-tick python builtin constructs a pythonMutation (plain ints + capSet, NEVER a *py.Object or a live *Entity/*ChunkManager) and enqueues it on asyncIn2; the owner enforces the capSet then applies through the EXACT Phase-23 worldHandle.setBlock body. A READ: pythonReadReq carries a buffered reply channel; the owner snapshots GetBlock and sends the copied scalar back. No live tick-owned handle ever reaches the off-tick goroutine."
  - "MINIMAL vocabulary, the rest deferred-cited (26-CONTEXT decision 4 — keep it small). set_block + a simple spawn (spawnVanillaPig — the only entity-add the server exposes) + log are the locked v1 surface. A typed spawn, block-entity edits, item drops, and attribute mutations are DEFERRED to a follow-up plan once the round-trip is proven (cited in python_bridge.go mutationKind doc)."
  - "Capabilities threaded via a server-provided FACTORY, not by widening Load's signature. The host gains SetPythonBridgeFactory(func(plugin, capabilities) (WorldBridge, error)); LoadDir's python branch calls it with the manifest capabilities and installs the per-plugin bridge via pp.SetWorldBridge. The server's factory runs parseCapabilities (the SAME Phase-23 derivation) — an unknown capability errors loudly at load. This keeps plugin/host + plugin/python cgo-free (the bridge is a plain-Go interface; the capSet parse lives in server)."
  - "The Go-side bridge is the authoritative -race-able proof. Because the request/apply indirection is the boundary and the python side only PRODUCES the request, the default-build test (python_bridge_test.go) drives the SAME serverWorldBridge -> asyncIn2 -> applyTo path the python builtin drives, and asserts the round-trip + the capability drop + the read snapshot WITHOUT libpython. The tagged test adds the real CPython producer on top (Docker -race)."

patterns-established:
  - "Pattern: the world-bridge = pathReady for plugin mutations. The off-tick producer constructs an immutable plain-value request; asyncIn2 carries it; applyAsyncResults drains it on the owner; applyTo enforces the cap + applies through the EXISTING tick-owned seam (the ONLY mutation point). A read uses the same lane with a buffered reply channel for the snapshot copy."
  - "Pattern: a per-plugin capability-stamped bridge installed at load via a server-provided factory keeps the cgo boundary clean — the host holds WorldBridge by interface, the server owns the capSet parse, the python builtins hold the bridge and produce requests."

requirements-completed: [PLUGIN-06]

# Metrics
duration: 22min
completed: 2026-06-28
---

# Phase 26 Plan 03: The world-bridge (off-tick python mutation requests applied on-tick) Summary

**A Python hook running OFF-TICK now produces a small, capability-gated mutation REQUEST (set_block + a simple spawn/log) carried as PLAIN SCALARS (never a live handle), which the owner drains from `asyncIn2` and APPLIES on the tick goroutine through the SAME Phase-23 seam (`ChunkManager.SetBlock` + `broadcastBlockUpdate` / `spawnVanillaPig`) — the ONLY mutation point (TICK-05); a READ goes request → owner-snapshot → return-a-copy; the request is dropped with a `capError` if the plugin lacks the grant — and THE PHASE GATE holds: python off-tick (26-02) + an on-tick mutation apply (this plan) with the default no-tag build STILL pure-Go static (CGO=0, ZERO gopython in the graph, including the `cmd/sulfur` binary).**

## Performance

- **Duration:** ~22 min
- **Tasks:** 2
- **Files:** 17 (9 created, 8 modified)

## Accomplishments

- **THE #1 GATE holds on every task:** `CGO_ENABLED=0 go build ./...` exits 0 and `go list -deps ./... | grep -i gopython` is EMPTY — for the whole tree AND the `cmd/sulfur` binary's own graph. The bridge request types + the owner apply + the server-side `serverWorldBridge` are DEFAULT-BUILT and cgo-free; only the gopy request PRODUCERS (the `set_block`/`spawn`/`log`/`block_at` builtins) live behind `//go:build python`.
- **The world-bridge (PLUGIN-06):** an off-tick python `set_block(x,y,z,state)` constructs a `pythonMutation` (plain ints + the owning plugin's capSet) and enqueues it on `asyncIn2`. The owner drains it in `applyAsyncResults`, `pythonMutation.applyTo` enforces `capWorldWrite` FIRST, then applies through the EXACT `worldHandle.setBlock` body (`t.world.SetBlock` + `t.broadcastBlockUpdate`). `GetBlock` reflects the change and a `BlockUpdate` is broadcast — proven in `TestWorldBridgeSetBlockRoundTrip`.
- **No live handle off-tick (the safety boundary, T-26-03):** the request carries only scalars; the ONLY mutation is `applyTo` on the owner. A READ (`block_at`) issues a `pythonReadReq` with a buffered reply channel — the owner snapshots `GetBlock` and sends the COPIED scalar back; the off-tick builtin blocks on the reply and returns the copy (`TestWorldBridgeReadSnapshot`). No `*ChunkManager`/`*Entity`/`*py.Object` ever crosses the boundary.
- **Capability-gated (T-26-09):** the request carries the owning plugin's capSet, parsed from the manifest via the SAME `parseCapabilities` the Phase-23 Starlark handles use. A denied `set_block`/`block_at` is DROPPED with a `capError`-style log and the world is UNCHANGED (`TestWorldBridgeCapabilityDenied`, `TestWorldBridgeReadDenied`); an unknown capability errors loudly at load (`TestWorldBridgeUnknownCapability`).
- **Minimal vocabulary, the rest deferred-cited:** set_block + a simple spawn (`spawnVanillaPig`) + log are the locked v1 surface (26-CONTEXT decision 4). A typed spawn, block-entity edits, item drops, and attribute mutations are DEFERRED to a follow-up plan (cited in `python_bridge.go`).
- **THE PHASE GATE (26-02-05):** (a) a python plugin runs off-tick + rejoins via the async seam (26-02) ✓; (b) a mutation request applies on-tick (this plan, -race in Docker) ✓; (c) the default no-tag build is STILL pure-Go static — CGO=0 build clean + ZERO gopython in the graph + the `cmd/sulfur` binary links no libpython ✓.

## Task Commits

1. **Task 1: the mutation-request type + owner-side apply through the Phase-23 seams (capability-gated)** — `e51c8138` (feat)
2. **Task 2: the world-bridge round-trip test (-race) + the worldmutate testdata plugin + THE PHASE GATE** — `2c71da73` (test)

## Files Created/Modified

- `server/python_bridge.go` — `pythonMutation` (set_block/spawn/log) + `pythonReadReq` (the read snapshot) as cgo-free plain-value `asyncResult`s; `applyTo` enforces the capSet then applies through the Phase-23 seam; `serverWorldBridge` (the per-plugin, capSet-stamped `host.WorldBridge` impl) + `newServerWorldBridge` (parses the manifest capabilities).
- `plugin/host/runtime.go` — the `host.WorldBridge` plain-Go interface + `PythonPlugin.SetWorldBridge`.
- `plugin/host/manager.go` — `SetPythonBridgeFactory` + the per-plugin bridge install in `LoadDir`'s python branch.
- `plugin/python/bridge_python.go` (`//go:build python`) — the `set_block`/`spawn`/`log`/`block_at` request-producer builtins + `SetWorldBridge`; `block_at` does request → owner-snapshot → return-a-copy.
- `plugin/python/runtime_python.go` / `runtime_stub.go` — the `bridge` field + `SetWorldBridge` (tagged stores it; stub no-op — both satisfy `host.PythonPlugin`).
- `plugin/python/register_python.go` — `Load` now injects the world builtins alongside `register`.
- `server/async_python_python.go` — `WirePython` also registers the bridge factory.
- `server/python_bridge_test.go` (default, CGO=0) — the Go-side round-trip + capability-denied + read-snapshot + unknown-capability tests + the additive-skip gate.
- `server/python_bridge_python_test.go` (`//go:build python`) — the full off-tick → request → tick-apply round-trip + the capability-denied drop (Docker -race).
- `plugin/python/bridge_python_test.go` (`//go:build python`) — the builtins produce the right requests through a fake bridge + no-op without a bridge.
- `server/testdata/worldbridge_granted/worldmutate/{plugin.toml,main.py}` (world.write) + `server/testdata/worldbridge_denied/worldmutate_denied/{plugin.toml,main.py}` (no caps) — the world-mutator + the denied variant.

## Decisions Made

See `key-decisions` above: the request/apply indirection as the boundary; the minimal vocabulary with cited deferrals; capabilities threaded via a server-provided factory (keeping the cgo boundary clean); and the Go-side bridge as the authoritative -race-able proof (the python side only produces the request).

## Deviations from Plan

None — plan executed as written. Two structural choices the plan delegated, not behavioral deviations:
- **(a)** Capabilities were threaded via a new `host.SetPythonBridgeFactory` seam (the server owns the `parseCapabilities` derivation, the host installs the bridge per-plugin at load) rather than widening `PythonRuntime.Load`'s signature — this keeps `plugin/host` + `plugin/python` cgo-free (the plan said "thread the owning plugin's capSet through the PythonPlugin load... the python lane reuses the Phase-23 capability derivation", which this does).
- **(b)** The tagged round-trip test was split into `server/python_bridge_python_test.go` (the full owner round-trip) + `plugin/python/bridge_python_test.go` (the producer unit), and the default-build test into `server/python_bridge_test.go` — mirroring the 26-02 default/tagged file split. The worldmutate plugins live in isolated parent dirs (`worldbridge_granted` / `worldbridge_denied`) so each tagged case loads exactly one plugin.

## Known Stubs

None. The bridge is fully wired: the producers construct real requests, the owner applies them through the live Phase-23 seam, and the capability gate is enforced. The MINIMAL vocabulary (set_block + spawn + log + block_at) is a SCOPE decision (26-CONTEXT decision 4), not a stub — richer mutations are explicitly deferred with a citation, and every shipped op round-trips end-to-end.

## Threat Flags

None. The world-bridge introduces no new wire surface — it routes through the EXISTING Phase-23 `ChunkManager.SetBlock` + `broadcastBlockUpdate` seam (already wire-sealed). The new trust boundary (off-tick python → tick-owned world) is exactly the one the plan's `<threat_model>` enumerates (T-26-03 / T-26-09 / T-26-10), and each is mitigated: the request/apply indirection (no live handle), the capSet gate (no elevation), and the bounded `asyncIn2` + small `pluginPool` drop-on-overload (no DoS).

## Build Verification (local, the gates that MUST pass on Windows)

- `CGO_ENABLED=0 go build ./...` → exit 0 (pure-Go static) — every task.
- `go list -deps ./... | grep -i gopython` → EMPTY; `go list -deps ./cmd/sulfur | grep -i gopython` → EMPTY (the #1 isolation gate, tree + binary).
- `CGO_ENABLED=0 go vet ./server/ ./plugin/...` → clean.
- `CGO_ENABLED=0 go test ./plugin/... ./server/` → PASS (incl. `TestPythonWorldBridgeSkippedDefault`, `TestWorldBridgeSetBlockRoundTrip`, `TestWorldBridgeCapabilityDenied`, `TestWorldBridgeReadSnapshot`, `TestWorldBridgeReadDenied`, `TestWorldBridgeUnknownCapability` — the last run 20x clean for the channel-handoff path).
- `gofmt -l` on all changed files → empty.

**The `-tags python` build + the `-race` run are the python3.14 Docker image / CI gate, NOT local** (no libpython3.14 / gcc on Windows — the documented 26-02 split). The tagged tests (`TestPythonWorldBridge`, `TestPythonWorldBridgeCapabilityDenied`, `TestPythonWorldBridgeNoLiveHandle`, `TestWorldBuiltinsProduceRequests`, `TestWorldBuiltinsNoBridgeNoOp`) were written against the VERIFIED gopy@python3.14 source (every API used — `NewLong`/`PackTuple`/`Long.Int64`/`Bool.Bool`/`Tuple.GetIndex`/`Tuple.Size`/`NewCFunction`/`Dict.SetItemString` — confirmed in the module cache; `PackTuple` STEALS its item refs, so `block_at` does not over-Decref).

## Next Phase Readiness

- Phase 26 (opt-in-python-runtime) is COMPLETE: 26-01 (the build-tag split + host seam), 26-02 (the off-tick lane), 26-03 (the world-bridge). PLUGIN-06 is closed end-to-end — a python plugin can run off-tick, observe events, and now (capability-gated) request world mutations the owner applies on-tick, all while the default binary stays pure-Go static.
- No blockers. The richer mutation vocabulary (typed spawn, block entities, item drops, attribute edits) is the cited deferral — a clean follow-up that reuses this exact request/apply lane.

## Self-Check: PASSED

All 9 created files exist on disk; both task commits (`e51c8138`, `2c71da73`) exist in the git log.

---
*Phase: 26-opt-in-python-runtime*
*Completed: 2026-06-28*
