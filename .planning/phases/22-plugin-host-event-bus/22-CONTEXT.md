# Phase 22: Plugin host + event bus — Context

**Gathered:** 2026-06-27
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 22-RESEARCH.md (HIGH-confidence, named real Sulfur seams)
**Requirement:** PLUGIN-02

<domain>
## Phase Boundary

**Delivers:** the plugin HOST (manager: discover/load/unload `.star` plugins from a `plugins/` dir with a manifest) + the typed EVENT BUS (tick / player join+leave / block break+place / entity spawn+death / damage) + the register-hooks-ONCE-at-load API + the Go→plugin dispatch seam that stays OFF the per-entity hot path. Built on Phase 21's `plugin/starlark` runtime (Load/LoadedPlugin.Call/safeGlobals/newThread).

**OUT (later phases):**
- The entity/world/nav FROZEN-HANDLE API (what a hook can READ/MUTATE of game state) → Phase 23. Phase 22 hooks receive simple FROZEN SCALARS only (e.g. block pos ints, player name string, damage float) — NOT live entity/world handles.
- Mob behavior declaration / full-override → Phase 23.
- Vanilla-mobs-as-plugins, crafting, Python, Folia → Phases 24–27.
- Capability ENFORCEMENT → Phase 23 (when handles exist to restrict).
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Manifest + distribution
- **TOML manifest-dir.** Each plugin is a directory under `plugins/` containing a `plugin.toml` manifest. Discovery scans `plugins/*/plugin.toml`. Manifest fields (minimum): `name`, `version`, `entrypoint` (the `.star` file, relative to the plugin dir), `runtime` ("starlark" now; "python" reserved for Phase 26). Use a Go TOML decoder — check what's already in go.mod; if none, add a pure-Go TOML lib (BurntSushi/toml or pelletier/go-toml) as a plain dep (CGO=0 preserved).
- TOML chosen over JSON (author-readable) and over single-.star (needs the dir+manifest for versioning + the Phase-26 runtime selector).

### Hot-reload — FULL (file-watcher)
- **Build full hot-reload:** a file-watcher over `plugins/` that reloads a plugin when its `.star`/manifest changes — unload the old, load the new, WITHOUT a server restart. Use a pure-Go watcher (`fsnotify` — pure-Go, CGO=0 OK; confirm at planning).
- **Race-safety is the hard part (TICK-05):** hook invalidation + the swap of the hook map MUST be coordinated with the tick goroutine. The reload cannot mutate the live hook map mid-dispatch. The plan must define a safe swap: the watcher goroutine prepares the new LoadedPlugin + a new hook map off-tick, then the SWAP happens on the tick goroutine (e.g. via the existing register/drain channel pattern the tick already uses — see `drainRegistrations` in server/tick.go) so dispatch never reads a half-updated map. Docker `-race` MUST cover a reload-during-dispatch test.
- `Unload()` + `Load()` are the primitives (PLUGIN-02 needs them regardless); hot-reload composes them behind the watcher.

### Capability / permission model
- **Optional manifest field `capabilities` (declarative), NOT enforced yet.** The manifest accepts a `capabilities` list (e.g. `["entities","world","network"]`) and the manager parses + stores it, but Phase 22 does NOT enforce it — there are no entity/world handles to restrict until Phase 23. Enforcement lands in Phase 23 when the frozen-handle API exists. Recording it now means the field is stable + plugins can declare intent.

### Event semantics
- **`on_tick` fires RAW every tick** (20/s). Documented as costly; the plugin author throttles in-script if needed. No built-in throttle interval in Phase 22 (keeps the API surface minimal; a throttle can be added later if profiling demands).
- **`on_damage` fires with the FINAL (post-mitigation) damage** — after armor/effects/absorption, the value most hooks want — matching where the jar computes final damage (`actuallyHurt`/`die` path in combat.go). NOT the raw pre-mitigation amount.
- Other events fire on their discrete occurrence (one event per block break, one per join, etc.) — NEVER per-entity-per-tick.

### The dispatch seam (from research — the load-bearing constraint)
- Events are emitted from DISCRETE-occurrence functions, NOT per-entity loops. The research named the exact Sulfur seam functions:
  - block break → `destroyAndAck`
  - block place → `reconcileEdit` / `handleUseItemOn`
  - join → the `tick.go` join seam; leave → `removePlayer`
  - entity spawn → `structure_spawn.go` `entities.add` (and the player/item add paths)
  - death/damage → `combat.go` `die` / `applyDamage` (damage hook = post-mitigation value)
  - tick → `tickOnce` (the ONE per-tick event)
- **ANTI-SEAMS (forbidden):** the per-entity loops `tickEntities`/`tickAI`/`tickPhysics` in `tick_phases.go` — a `starlark.Call` there is the 4000-calls/sec pattern PLUGIN-02 exists to avoid.
- The bus is a plain `map[EventType][]Hook` dispatched INLINE on the tick goroutine (single-owner, TICK-05). NOT a channel/goroutine pub-sub broker (wrong fit, -race hazard).
- `register(event_name, fn)` is a Go builtin (added to the predeclared set the host injects into Phase 21's `Load`) that captures the `starlark.Callable` into the map ONCE at load.

### Where the code lives
- A new `plugin/host/` (or `plugin/manager/`) package — imports `plugin/starlark` (Phase 21) but NOT server hot-path internals. The server wires the emit calls at the named seams (small, surgical edits to server/*.go at the discrete-occurrence points). The manager owns the hook map + dispatch; the server owns calling `host.Emit(event, args)` at the seams.
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 22 row + gate, the DECLARE-once architecture, off-hot-path rule.
- `.planning/REQUIREMENTS.md` — PLUGIN-02 full text.
- `.planning/phases/22-plugin-host-event-bus/22-RESEARCH.md` — HIGH-confidence: the named seam/anti-seam functions, the map-bus design, the register-capture pattern, the pitfalls (shared broadcastBlockUpdate double-fire), the Phase-21 Load extension note (A2).
- `.planning/phases/21-starlark-runtime-foundation/21-01-SUMMARY.md` + `plugin/starlark/*.go` — the Phase-21 runtime API the host builds on (Load signature, safeGlobals injection point — A2: may need a tiny extension to accept a host-injected predeclared set so `register` can be injected).
- Sulfur seam files (read at planning): `server/tick.go` (drainRegistrations, tickOnce, join seam), `server/tick_phases.go` (the anti-seam loops), `server/combat.go` (die/applyDamage), `server/structure_spawn.go` (entities.add), `server/block_interact.go` (destroyAndAck/reconcileEdit/handleUseItemOn), `server/keepalive.go` (removePlayer).
- `CLAUDE.md` — CGO=0, -race in Docker, push development, no Claude attribution, TICK-05 single-owner.
</canonical_refs>

<specifics>
## Specific Ideas

- **A2 (verify at planning):** Phase 21's `Load`/`safeGlobals` likely needs a small extension so the host can inject host-specific builtins (`register`) into the predeclared StringDict per-load. Confirm against the executed Phase-21 code (`plugin/starlark/runtime.go` safeGlobals + loader.go Load signature). If Load takes a fixed safeGlobals(), add an overload/param that accepts an extra StringDict the host merges in.
- The signature architecture test: a hook fires EXACTLY ONCE per real block break (driven through `destroyAndAck`), and the fire count is NOT multiplied by entity count — concrete proof of "event-driven, not per-tick-scan." This test is the PLUGIN-02 gate.
- The hot-reload -race test: reload a plugin WHILE dispatch is happening (concurrent watcher swap + tick dispatch) → Docker -race clean, no torn map read.
- Pitfall (research): `broadcastBlockUpdate` is shared by break AND place — don't double-fire; emit the event at the specific break/place seam, not the shared broadcast.
- fsnotify is pure-Go (CGO=0) — confirm + pin at planning. TOML lib likewise pure-Go.
</specifics>

<deferred>
## Deferred Ideas

- Entity/world/nav frozen-handle API (what a hook reads/mutates of live state) → Phase 23.
- Capability ENFORCEMENT → Phase 23.
- Mob behavior declaration + full-override → Phase 23.
- on_tick throttle interval → later, only if profiling demands.
- Python runtime selector wired through the manifest `runtime` field → Phase 26 (the field exists now, the Python loader lands then).
</deferred>

---

*Phase: 22-plugin-host-event-bus*
*Context gathered: 2026-06-27 via operator decisions + v4-PLAN.md + 22-RESEARCH.md*
