---
phase: 22-plugin-host-event-bus
verified: 2026-06-28T01:05:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 22: Plugin host + event bus Verification Report

**Phase Goal:** A plugin host loads/unloads plugins from a plugins dir and a typed event bus fires the core gameplay events to register-once hooks — with the Go→plugin dispatch seam kept OFF the per-entity hot path.
**Requirement:** PLUGIN-02
**Verified:** 2026-06-28
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Manager discovers/loads/unloads plugins from `plugins/*/plugin.toml`; TOML manifest decoded (name/version/entrypoint/runtime + optional capabilities) | ✓ VERIFIED | `manifest.go` `readManifest` (BurntSushi/toml `DecodeFile`, 4 required fields validated, path-traversal guard); `manager.go` `LoadDir`/`LoadDirWith`/`Unload`; `TestManifestValid`, `TestManifestMissingRequired` (4 sub), `TestManifestPathTraversal` (3 sub), `TestDiscoverLoad`, `TestUnload` all PASS |
| 2 | Typed event bus: `register(event,fn)` captures a callable ONCE at load into `map[EventType][]Hook`; unknown event name rejected at load | ✓ VERIFIED | `register.go` `makeRegisterBuiltin` → `m.hooks[evt] = append(...)`; `isKnownEvent` rejects typo with `fmt.Errorf("register: unknown event %q")`; `event.go` 8 EventType consts + frozen-scalar payloads; `TestRegisterUnknownEvent` PASS; fixture proves 8/8 hooks captured once at load (`HookCount==1`) |
| 3 | The 8 events fire at DISCRETE seams; on_damage POST-mitigation (actuallyHurt not applyDamage); on_tick once at tickOnce end | ✓ VERIFIED | All 8 `t.plugins.Emit(...)` confirmed nil-guarded at the named funcs (grep): break=`destroyBlock` (after SetBlock removal), place=`handleUseItemOn` after reconcileEdit (NOT broadcastBlockUpdate — 0 Emit there), join=`drainRegistrations`, leave=`removePlayer`, spawn=`structure_spawn` after `entities.add`, death=`die`, damage=`actuallyHurt` after `p.health -= amount`, tick=`tickOnce` after `gametime++`. applyDamage Emit count=0; EventDamage present in actuallyHurt; `TestDamageIsPostMitigation` PASS |
| 4 | THE ARCHITECTURE GATE: a hook fires EXACTLY ONCE per block break, count NOT multiplied by entity count | ✓ VERIFIED | `TestBlockBreakEventFiresOnce` adds N=50 entities, breaks one block through the funnel, asserts `on_block_break==1` (N-independent) AND `on_tick==0`; anti-seam grep: tickEntities/tickAI/tickPhysics Emit count = 0/0/0. Test PASS (CGO=0 and Docker -race) |
| 5 | FULL hot-reload: fsnotify watcher rebuilds Manager off-tick + swaps on tick goroutine (pluginSwap/drainRegistrations); reload-during-dispatch -race clean | ✓ VERIFIED | `reload.go` `NewWatcher` (fsnotify, debounce, off-tick `New`+`LoadDirWith` rebuild, publish on swap chan); `tick.go` `pluginSwap chan *host.Manager` (buffered 1), `drainRegistrations` `case mgr := <-t.pluginSwap: t.plugins = mgr` (owner-only writer); `TestHotReloadSwap` + `TestReloadDuringDispatch` PASS (CGO=0 and Docker CGO=1 -race) |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `plugin/starlark/loader.go` | LoadWith(path, extra) injectable predeclared | ✓ VERIFIED | `LoadWith` merges a fresh copy of safeGlobals with extra (extra wins); `Load` delegates `LoadWith(path, nil)` — Phase-21 behavior intact |
| `plugin/host/manifest.go` | Manifest + readManifest + path-traversal guard | ✓ VERIFIED | TOML decode, 4 required fields, `..`/absolute rejected (T-22-03) |
| `plugin/host/event.go` | 8 EventType consts + frozen-scalar payloads + toStarlark | ✓ VERIFIED | 8 consts, `isKnownEvent`, scalar-only payloads (DamageEvent.Amount = post-mitigation) |
| `plugin/host/register.go` | makeRegisterBuiltin captures callable; rejects unknown | ✓ VERIFIED | append to hooks map; unknown-event error at load |
| `plugin/host/emit.go` | Emit zero-sub guard + fresh-thread + per-hook isolation | ✓ VERIFIED | len-check before toStarlark; fresh `NewThread` per hook; recover+log isolation |
| `plugin/host/manager.go` | Manager; LoadDir/LoadDirWith; Unload | ✓ VERIFIED | imports plugin/starlark+stdlib only (no server/world/level boundary) |
| `plugin/host/reload.go` | fsnotify watcher + off-tick rebuild + swap chan | ✓ VERIFIED | debounced, blocking-with-done publish, last-good-on-error |
| `server/*` seams (7 files) | additive nil-guarded Emit at named funcs | ✓ VERIFIED | 8 Emits, gameplay logic unchanged (guarded one-liners) |
| `server/plugin_events_test.go` | the GATE + seam + post-mitigation tests | ✓ VERIFIED | TestBlockBreakEventFiresOnce, TestEventSeams (5 sub), TestDamageIsPostMitigation |
| `plugin/host/reload_test.go` | hot-reload swap + reload-during-dispatch race | ✓ VERIFIED | TestHotReloadSwap, TestReloadDuringDispatch |
| `cmd/sulfur/main.go` | host wiring end-to-end | ✓ VERIFIED | LoadDir → SetPlugins → NewWatcher(PluginSwapChan), closed on ctx; missing dir = no-op (not built-but-unwired) |

### Key Link Verification

| From | To | Via | Status |
|------|----|----|--------|
| manager.LoadDir | starlark.LoadWith | injects register into predeclared, once per plugin | ✓ WIRED |
| register builtin | Manager.hooks map | `m.hooks[evt] = append(...)` at load | ✓ WIRED |
| emit.Emit | starlark.Call on fresh thread | zero-sub guard + per-hook isolation | ✓ WIRED |
| block_break.destroyBlock | Emit(EventBlockBreak) | after SetBlock removal | ✓ WIRED |
| combat.actuallyHurt | Emit(EventDamage) | after p.health -= amount (post-mitigation) | ✓ WIRED |
| reload.watcher | tick.pluginSwap chan | off-tick rebuild → owner swap | ✓ WIRED |
| main() | tick.PluginSwapChan + SetPlugins | full runtime wiring | ✓ WIRED |

### Gates Run

| Gate | Command | Result |
|------|---------|--------|
| Build (CGO=0) | `CGO_ENABLED=0 go build ./...` | ✓ exit 0 |
| Vet | `go vet ./plugin/... ./server/` | ✓ exit 0 |
| No cgo in plugin/ | `grep -rn 'import "C"' plugin/` | ✓ 0 hits |
| Host unit (CGO=0) | `go test ./plugin/host/` | ✓ ok |
| Server seam (CGO=0) | `go test ./server/ -run GATE+seams` | ✓ ok |
| Host -race (Docker CGO=1) | `TestEmitRace\|TestReloadDuringDispatch\|TestHotReloadSwap` | ✓ ok |
| Server -race (Docker CGO=1) | `TestBlockBreakEventFiresOnce\|TestEventSeams\|TestDamageIsPostMitigation` | ✓ ok |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| PLUGIN-02 | 22-01, 22-02 | Plugin host + typed event bus, register-once, dispatch off the per-entity hot path | ✓ SATISFIED | All 4 ROADMAP success criteria met; the GATE proves the off-hot-path architecture; hot-reload + capability-parse resolve the open decisions |

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| `server/combat.go` EntityDeathEvent.TypeID=0 | Hardcoded scalar | ℹ️ Info | Documented CONTEXT boundary: v1 routes all death through the player path; real per-type id arrives with the Phase-23 entity-handle API. NOT a stub — payloads are frozen scalars from the seam's immediate context. Does not block PLUGIN-02. |
| `manifest.Capabilities` | Parsed + stored, not enforced | ℹ️ Info | Locked decision: enforcement lands in Phase 23 with frozen handles. The field flows through and is available downstream. |

### Behavioral Spot-Checks

| Behavior | Result | Status |
|----------|--------|--------|
| register-once: 8 fixture hooks each captured exactly once at load | HookCount==1 for all 8 events | ✓ PASS |
| GATE: one break with 50 entities → on_block_break==1 | counter==1, on_tick==0 | ✓ PASS |
| on_damage = post-mitigation value | emitted from actuallyHurt, equals landed health delta | ✓ PASS |
| no double-fire: break does not fire place, place does not re-fire break | both asserted | ✓ PASS |
| hot-reload swap: v2 hook map replaces v1 (removed hook gone, new hook live) | asserted via swap chan | ✓ PASS |
| -race: reload concurrent with dispatch, no torn read | Docker CGO=1 clean | ✓ PASS |

### Human Verification Required

None. Phase 22 is autonomous/standalone-testable (the real-client gate is Phase 28). All behaviors are proven by automated tests + the Docker -race gates.

### Git Authorship

Phase-22 commits (`60bd0890`, `5e59e754`, `b5029609`, `e5739e36`, `42d513f0`) authored by `Matias Canovas <hello@hinotori.moe>`. No `Co-Authored-By: Claude`, no "Generated with Claude" footer, no 🤖 attribution. The `Claude-Session:` trailer is the harness-mandated session link, not a prohibited self-attribution. ✓ Clean.

### Notes / Observations

- **Flaky pre-existing test (NOT a Phase 22 regression):** `TestServerAiStepWalksToGoalTarget` failed once during the first full `go test ./server/` run, then passed 5/5 on re-runs and passes in isolation. The test lives in `server/navigation_test.go` / `server/ai_mob.go` — neither file is touched by any Phase-22 commit. It also passed at the pre-Phase-22 parent commit (169f2220) on the same machine. It is a low-frequency, ordering/RNG-dependent flake in the AI-navigation subsystem that pre-dates Phase 22 and is unrelated to the PLUGIN-02 seams. Recommend tracking it as a separate AI-navigation flake item; it does not gate PLUGIN-02.
- **Seam edits are additive:** Every server seam is a guarded `if t.plugins != nil { t.plugins.Emit(...) }` one-liner inserted between unchanged vanilla steps (verified at destroyBlock, actuallyHurt, tickOnce). The 1:1 gameplay logic at each seam is unmodified; existing server tests pass (5/5 full-suite re-runs green).
- **Boundary held:** `plugin/host` imports only `plugin/starlark` + stdlib + the two pure-Go deps (BurntSushi/toml, fsnotify) — never server/world/level. Server depends on plugin/host (one direction).

### Gaps Summary

No gaps. All 5 success criteria verified against the codebase with passing tests, including both Docker -race gates. The Go→plugin dispatch demonstrably hangs off O(occurrences) discrete seams (the GATE proves count is entity-count-independent), never the O(entities×ticks) per-entity loops (anti-seam grep clean). FULL hot-reload swaps on the tick goroutine and is reload-during-dispatch -race clean. PLUGIN-02 is delivered.

---

_Verified: 2026-06-28T01:05:00Z_
_Verifier: Claude (gsd-verifier)_
