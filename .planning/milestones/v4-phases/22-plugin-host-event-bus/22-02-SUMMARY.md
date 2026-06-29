---
phase: 22-plugin-host-event-bus
plan: 02
subsystem: server-wiring
tags: [plugin-host, event-bus, server-seams, hot-reload, fsnotify, tick-05, cgo-free]

# Dependency graph
requires:
  - phase: 22-plugin-host-event-bus
    plan: 01
    provides: "plugin/host Manager (New/LoadDir/LoadDirWith/Unload/Emit), 8 EventType consts + frozen-scalar payloads, the register-once builtin, the LoadWith starlark seam, fsnotify+toml deps"
provides:
  - "TickLoop.plugins *host.Manager + SetPlugins setter + pluginSwap chan + PluginSwapChan accessor — the server-side plugin host wiring"
  - "8 nil-guarded host.Emit calls at the discrete gameplay seams (break/place/join/leave/spawn/death/damage/tick), on_damage from the POST-mitigation site, on_tick once per tick zero-sub-guarded"
  - "THE GATE proof: a hook fires EXACTLY ONCE per real block break, count independent of entity count (event-driven, not per-tick-per-entity scan)"
  - "plugin/host/reload.go: FULL fsnotify hot-reload watcher — off-tick Manager rebuild + tick-goroutine swap via the pluginSwap channel (TICK-05 / T-22-05 clean)"
  - "cmd/sulfur main() wiring: load plugins/ + SetPlugins + arm the hot-reload watcher (no-op when plugins/ absent)"
affects: [23-entity-mob-behavior-api, 26-python-runtime, 27-folia-region-awareness]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Discrete-seam dispatch: one nil-guarded t.plugins.Emit at each O(occurrences) gameplay funnel — NEVER in the O(entities×ticks) tickEntities/tickAI/tickPhysics anti-seam loops (grep-gated)"
    - "on_block_break emits from destroyBlock (the single funnel both destroyAndAck and the delayed-destroy route through) AFTER the SetBlock removal — once per real break; on_block_place from the place site in handleUseItemOn (NOT the shared broadcastBlockUpdate — Pitfall 2 double-fire)"
    - "on_damage emits the FINAL post-mitigation amount from actuallyHurt (after p.health -= amount) — NOT the raw applyDamage input (the LOCKED operator decision)"
    - "FULL hot-reload: watcher REBUILDS a fresh *Manager off-tick and sends the POINTER on pluginSwap; the tick owner is the SOLE writer of t.plugins and swaps on-thread in drainRegistrations (same select-with-default discipline as register/unregister/consoleCmd) — a reload concurrent with dispatch is a pointer swap between ticks, never a mid-Emit map mutation"

key-files:
  created:
    - "plugin/host/reload.go — fsnotify Watcher (root + sub-dirs, debounced .star/.toml change -> off-tick New+LoadDirWith rebuild -> publish on swap chan), Close, RebuildCount"
    - "plugin/host/reload_test.go — TestHotReloadSwap (v2 hook map replaces v1) + TestReloadDuringDispatch (-race: owner-side swap, no torn read)"
    - "server/plugin_events_test.go — THE GATE (TestBlockBreakEventFiresOnce, count N-independent of 50 entities), TestEventSeams (per-seam fire-once), TestDamageIsPostMitigation"
    - "server/testdata/plugins/events/{plugin.toml,main.star} — one counting hook per event fixture"
  modified:
    - "server/tick.go — TickLoop.plugins + pluginSwap fields, SetPlugins + PluginSwapChan, NewTickLoop pluginSwap init, drainRegistrations swap case + on_player_join seam, removePlayer on_player_leave seam, host import"
    - "server/block_break.go — on_block_break emit in destroyBlock after removal"
    - "server/block_interact.go — on_block_place emit in handleUseItemOn after reconcileEdit"
    - "server/combat.go — on_damage emit in actuallyHurt (post-mitigation) + on_entity_death in die"
    - "server/structure_spawn.go — on_entity_spawn emit after entities.add"
    - "server/tick_phases.go — on_tick emit at tickOnce end (after gametime++, zero-sub guarded)"
    - "cmd/sulfur/main.go — load plugins/ + SetPlugins + hot-reload watcher wiring"
  deleted:
    - "plugin/host/deps.go — the fsnotify require anchor (reload.go now imports fsnotify for real, so the anchor is obsolete)"

key-decisions:
  - "The pluginSwap channel + the drainRegistrations swap case + the seam Emits all landed in the Task-1 commit (the seam wiring needs the swap field to compile cleanly as one TickLoop change); reload.go + the watcher + main() wiring landed in Task-2"
  - "The watcher takes an `extra starlark.StringDict` so a reloaded Manager carries the SAME host/observability builtins the initial LoadDirWith used (tests inject a count builtin through it; production passes nil)"
  - "rebuild() publishes the fresh Manager with a BLOCKING send (select on done) onto the buffered-1 swap channel — the owner drains pluginSwap every tick, so a send never parks more than one tick, and a rebuild is guaranteed delivered (no silent drop)"
  - "A rebuild LOAD ERROR (a plugin saved mid-edit with a syntax error) is logged and the swap SKIPPED — the live Manager keeps running the last-good plugins rather than swapping in a broken set"
  - "on_entity_death payload TypeID=0: v1 routes all death through the player path (die(p *tickPlayer)); the real per-type id arrives with the Phase-23 entity-handle API"

patterns-established:
  - "Pattern: server depends on plugin/host (one direction); every seam Emit is `if t.plugins != nil { t.plugins.Emit(...) }` so a no-plugin server is a cheap nil-check no-op"
  - "Pattern: the GATE test (fire-once-per-occurrence, count entity-independent) is the concrete PLUGIN-02 architecture proof; the anti-seam grep gate is the static backstop"

requirements-completed: [PLUGIN-02]

# Metrics
duration: ~40min
completed: 2026-06-28
---

# Phase 22 Plan 02: Server seam wiring + FULL hot-reload Summary

**Wires `host.Manager.Emit` into the 8 named discrete server seams (break/place/join/leave/spawn/death/damage/tick), proves the off-the-hot-path architecture with the fire-once-per-break GATE + the anti-seam grep gate, emits on_damage at the POST-mitigation site, and ships FULL fsnotify hot-reload (off-tick rebuild -> tick-goroutine swap) proven -race clean under reload-during-dispatch.**

## Performance

- **Duration:** ~40 min
- **Started:** 2026-06-28
- **Completed:** 2026-06-28
- **Tasks:** 2
- **Files modified/created:** 13 (7 modified, 5 created, 1 deleted)

## Accomplishments
- All 8 discrete gameplay seams emit their event (nil-guarded) at the EXACT named functions: `on_block_break` from `destroyBlock` (after the SetBlock removal — covers both `destroyAndAck` and the `tickBlockBreak` delayed-destroy through the single funnel), `on_block_place` from `handleUseItemOn` (after `reconcileEdit`, NOT the shared `broadcastBlockUpdate` — Pitfall 2 avoided), `on_player_join`/`on_player_leave` from the `drainRegistrations` join seam + `removePlayer`, `on_entity_spawn` from `drainStructureSpawns` (after `entities.add`), `on_entity_death` from `die`, `on_damage` from `actuallyHurt` POST-mitigation (after `p.health -= amount` — the LOCKED final-landed-damage value, NOT the raw `applyDamage` input), and `on_tick` once at the END of `tickOnce` (after `gametime++`, zero-subscriber guarded).
- THE GATE green: `TestBlockBreakEventFiresOnce` drives ONE block break through the funnel with 50 entities in the store and asserts the hook fired EXACTLY ONCE — the count is independent of entity count, the concrete "event-driven, not per-tick-per-entity scan" proof.
- The anti-seam grep gate passes: zero `Emit(` in `tickEntities`/`tickAI`/`tickPhysics`. The post-mitigation grep gate passes: `EventDamage` is in `actuallyHurt`, absent from `applyDamage`.
- FULL hot-reload ships: `plugin/host/reload.go` watches `plugins/` (+ sub-dirs; fsnotify is non-recursive), debounces an editor-save burst into one rebuild, builds a fresh `*Manager` OFF-tick (`New`+`LoadDirWith`), and publishes the pointer on the swap channel the tick owner drains in `drainRegistrations` — the swap lands on the tick goroutine, never a mid-Emit map mutation. `TestReloadDuringDispatch` proves it -race clean.
- `cmd/sulfur` main() wires it end-to-end: loads `plugins/` via `host.LoadDir`, `SetPlugins`, and arms the watcher feeding `tick.PluginSwapChan()` (closed on ctx cancel). A missing `plugins/` dir is a clean no-op.
- CGO_ENABLED=0 ship build green; existing server tests still pass (the seam edits are additive single Emit calls — the 1:1 gameplay logic at each seam is UNCHANGED); CGO=0 `./server/ ./plugin/host/` green; Docker `-race` over `./plugin/host/ ./server/` (CGO=1) green — the reload-during-dispatch + fire-once-per-break tests prove no torn read / no per-entity multiplication.

## Task Commits

1. **Task 1: 8 discrete seam emits + TickLoop plugin wiring + THE GATE + anti-seam grep gate** - `e5739e36` (feat)
2. **Task 2: FULL hot-reload watcher (fsnotify off-tick rebuild + tick-goroutine swap) + main() wiring** - `42d513f0` (feat)

_Both tasks were tdd-flagged. The seam wiring + its GATE/seam test suite landed together in Task 1 (the GATE asserts the architecture as the implementation is written); the watcher + its swap/reload-race suite landed in Task 2._

## Files Created/Modified
See the frontmatter key-files block. Net: 7 server/main files gained one additive guarded Emit (or the TickLoop field/wiring) each; `reload.go` + two test files + the fixture are new; `deps.go` was deleted (its fsnotify anchor is obsolete now that `reload.go` imports fsnotify for real).

## Decisions Made
- The pluginSwap channel, the `drainRegistrations` swap case, and all 8 seam Emits landed in the **Task-1** commit: the seam wiring references `t.plugins` and the swap field, so the TickLoop struct change is one coherent unit. `reload.go`, the watcher, and the main() wiring are **Task-2**. This keeps each commit a buildable, test-green unit.
- The watcher accepts an `extra starlark.StringDict` so a reloaded Manager carries the SAME host builtins the initial load used — production passes `nil`; tests inject a `count` builtin through it to observe a reloaded hook firing.
- `rebuild()` publishes with a blocking send (with a `done` escape) onto the buffered-1 swap channel: the owner drains every tick, so delivery is guaranteed and a rebuild is never silently dropped.
- A rebuild that fails to load (a plugin mid-edit with a syntax error) is logged and the swap skipped — the live Manager keeps the last-good plugins.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Removed plugin/host/deps.go (the obsolete fsnotify anchor)**
- **Found during:** Task 2 (writing reload.go)
- **Issue:** `deps.go` existed solely to anchor the `fsnotify` go.mod require via a blank import "until the Plan-02 watcher imports it" (documented in 22-01-SUMMARY). Now that `reload.go` imports `fsnotify` for real, the anchor is dead code; leaving it would be a redundant blank import.
- **Fix:** `git rm plugin/host/deps.go`. `go mod tidy` confirmed go.mod unchanged (fsnotify retained via the real `reload.go` import).
- **Files modified:** plugin/host/deps.go (deleted)
- **Verification:** `go mod tidy` leaves go.mod byte-identical; CGO=0 build green; fsnotify still required.
- **Committed in:** `42d513f0` (Task 2 commit)

**2. [Rule 2 - Missing Critical] Wired the plugin host + watcher into cmd/sulfur main()**
- **Found during:** Task 2 (the watcher would otherwise be built-but-unwired — a CLAUDE.md no-built-but-unwired violation)
- **Issue:** The plan's tasks build the Manager wiring + the watcher but did not explicitly call for the main() integration; without it the feature is unreachable in the running server (and PLUGIN-02 asks for a working plugin host, not just a library).
- **Fix:** Added a `plugins/`-discovery block to `cmd/sulfur/main.go` before `tick.Run`: `host.New`+`LoadDir`, `SetPlugins`, `host.NewWatcher` feeding `tick.PluginSwapChan()`, closed on ctx cancel; a missing `plugins/` dir is a no-op.
- **Files modified:** cmd/sulfur/main.go
- **Verification:** CGO=0 build + `go vet ./cmd/sulfur/` green.
- **Committed in:** `42d513f0` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 blocking dead-code removal, 1 missing-critical wiring).
**Impact on plan:** Both are additive correctness fixes within the plan's scope (the watcher must be reachable and the obsolete anchor must go); no gameplay logic changed, no new deps, payloads stay frozen scalars.

## Issues Encountered
- None blocking. The fsnotify non-recursive watch required Adding each plugin sub-dir (handled in `addSubdirs` + a Create-event re-Add for runtime-created dirs).

## Known Stubs
- `on_entity_death` emits `TypeID: 0` because v1 routes all death through the player path (`die(p *tickPlayer)`) — the player's own Entity carries the real type id, which the Phase-23 entity-handle API surfaces. This is the documented CONTEXT boundary (Phase 22 payloads are frozen scalars from the seam's immediate context), not a gap that blocks PLUGIN-02.

## Threat Flags
None. The seams added no new network endpoints, auth paths, or trust boundaries beyond the plan's `<threat_model>` (T-22-05 reload-race + T-22-06 double-fire are both mitigated and tested). Payloads remain frozen scalars — no live state crosses to a hook.

## User Setup Required
None for an empty server. To use plugins: create a `plugins/` directory beside the `sulfur` binary, with one sub-dir per plugin containing `plugin.toml` (name/version/entrypoint/runtime) + the `.star` entrypoint. Editing a `.star`/`.toml` hot-reloads it live.

## Self-Check: PASSED

Verified on disk (all present): plugin/host/reload.go, plugin/host/reload_test.go,
server/plugin_events_test.go, server/testdata/plugins/events/plugin.toml,
server/testdata/plugins/events/main.star. Verified deleted: plugin/host/deps.go.
Verified in git history: e5739e36 (Task 1), 42d513f0 (Task 2).

---
*Phase: 22-plugin-host-event-bus*
*Completed: 2026-06-28*
