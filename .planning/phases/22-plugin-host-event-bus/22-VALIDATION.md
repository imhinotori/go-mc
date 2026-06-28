---
phase: 22
slug: plugin-host-event-bus
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-27
---

# Phase 22 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing) |
| **Config file** | none — Go modules |
| **Quick run command** | `CGO_ENABLED=0 go test ./plugin/...` |
| **Full suite command** | `CGO_ENABLED=0 go build ./... && go vet ./plugin/... ./server/... && CGO_ENABLED=0 go test ./plugin/... ./server/...` |
| **-race command (Docker, CGO=1)** | `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./plugin/...` |
| **Estimated runtime** | ~10–30s (host/bus unit tests; server seam tests are targeted) |

---

## Sampling Rate

- **After every task commit:** `CGO_ENABLED=0 go test ./plugin/...`
- **After every plan wave:** full suite + Docker `-race` (the hot-reload-during-dispatch test is only meaningful under -race)
- **Before `/gsd-verify-work`:** full suite green + Docker `-race` green
- **Max feedback latency:** ~30s quick / ~3 min race

---

## Per-Task Verification Map

> Task IDs provisional — the planner sets the final split. These are the PLUGIN-02 load-bearing samples.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 22-01-01 | 01 | 1 | PLUGIN-02 | — | TOML manifest decoded (name/version/entrypoint/runtime/capabilities); pure-Go TOML dep, CGO=0 clean | unit | `CGO_ENABLED=0 go test ./plugin/host/ -run TestManifest` | ❌ W0 | ⬜ pending |
| 22-01-02 | 01 | 1 | PLUGIN-02 | — | manager discovers plugins/*/plugin.toml, loads each via Phase-21 Load() once, holds N LoadedPlugins | unit | `CGO_ENABLED=0 go test ./plugin/host/ -run TestDiscoverLoad` | ❌ W0 | ⬜ pending |
| 22-01-03 | 01 | 1 | PLUGIN-02 | — | typed event bus map[EventType][]Hook; register(event,fn) builtin captures a Starlark callable ONCE at load | unit | `CGO_ENABLED=0 go test ./plugin/host/ -run TestRegisterHook` | ❌ W0 | ⬜ pending |
| 22-02-01 | 02 | 2 | PLUGIN-02 | — | THE GATE: a hook fires EXACTLY ONCE per real block break (via destroyAndAck), fire count NOT × entity count (event-driven, not per-tick-scan) | unit/integration | `CGO_ENABLED=0 go test ./server/ -run TestBlockBreakEventFiresOnce` | ❌ W0 | ⬜ pending |
| 22-02-02 | 02 | 2 | PLUGIN-02 | — | events emit at the named discrete seams (break/place/join/leave/spawn/death/damage/tick); on_damage = POST-mitigation value; NOT emitted from tickEntities/tickAI/tickPhysics anti-seams | unit + grep | `CGO_ENABLED=0 go test ./server/ -run TestEventSeams` + `! grep -n 'Emit' server/tick_phases.go` (no emit in per-entity loops) | ❌ W0 | ⬜ pending |
| 22-02-03 | 02 | 2 | PLUGIN-02 | T-22-01 | hot-reload: Unload()+Load() swap a plugin without restart; the hook-map swap happens on the tick goroutine (drainRegistrations pattern), dispatch never reads a half-updated map | unit | `CGO_ENABLED=0 go test ./plugin/host/ -run TestHotReloadSwap` | ❌ W0 | ⬜ pending |
| 22-02-04 | 02 | 2 | PLUGIN-02 | T-22-01 | RACE: a plugin reload concurrent with event dispatch — no torn map read, -race clean | race | Docker `-race ./plugin/... ./server/...` `-run TestReloadDuringDispatch` | ❌ W0 | ⬜ pending |

---

## Wave 0 Requirements

- [ ] `plugin/host/manifest_test.go` — TOML manifest decode + the capabilities field
- [ ] `plugin/host/manager_test.go` — discover/load/unload + register-hook
- [ ] `plugin/host/reload_test.go` — hot-reload swap + the -race reload-during-dispatch test
- [ ] `server/plugin_events_test.go` — the discrete-seam emit tests + the fire-once-per-break GATE
- [ ] `plugin/host/testdata/` — a test plugin dir (plugin.toml + a .star that registers on_block_break / on_tick / on_damage)
- [ ] `go get <pure-Go TOML lib>` + `go get github.com/fsnotify/fsnotify` (both pure-Go, CGO=0 — confirm at planning) + `go mod tidy`
- [ ] Phase-21 `Load`/`safeGlobals` extension (A2) — accept a host-injected predeclared StringDict so `register` can be injected (small, in plugin/starlark)

*The fire-once-per-break test is THE architecture proof — it must assert the hook count is independent of entity count (event-driven, not a per-tick-per-entity scan).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | — |

*All Phase-22 behaviors are automatable (host + bus + seam emits are standalone-testable). The real-client gate is Phase 28.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick) / race in Docker
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
