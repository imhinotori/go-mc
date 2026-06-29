# Phase 22: Plugin host + event bus — Research

**Researched:** 2026-06-27
**Domain:** A plugin host (discover/load/unload `.star` plugins from a dir, with a manifest) + a typed Go event bus that dispatches discrete gameplay events to plugin-registered Starlark hooks, kept OFF the per-entity-per-tick hot path.
**Confidence:** HIGH (the dispatch architecture is grounded in the actual Sulfur tick code read this session — every named seam function was verified by `grep`/`sed` against `D:/ender/server/*.go`; the Starlark capture-a-callback pattern is verified against the Phase-21 research + Context7 `/google/starlark-go`).

## Summary

Phase 22 is a **plumbing-and-policy** phase, not a runtime phase: Phase 21 already proved the hard part (sandbox, CGO=0, freeze, `starlark.Call` race-safety). Phase 22 stacks two things on top of `plugin/starlark`: (1) a **plugin manager** that scans a `plugins/` directory, reads a per-plugin **manifest** (name, version, entrypoint, runtime selector), calls Phase-21's `Load()` once per plugin, and holds the N resulting `LoadedPlugin` handles; and (2) a **typed event bus** — a Go-side `map[EventType][]hook` registry that plugins populate ONCE at load via a registration builtin (`register("on_block_break", fn)`), and that the tick goroutine fires at **discrete occurrence points** (a block breaks, a player joins, a mob dies) by calling `starlark.Call` on a fresh per-goroutine thread with frozen args.

**The single load-bearing constraint** (RESEARCH target #3, the v4-PLAN "off the per-entity hot path" rule, REQUIREMENTS "per-entity-per-tick scripting → OUT OF SCOPE"): the dispatch seam must hang off **event emission points**, NOT off the per-tick per-entity loops. The Sulfur tick has three per-entity loops that run every tick for every entity — `tickEntities`, `tickAI`, `tickPhysics` (verified in `server/tick_phases.go`). Putting a `starlark.Call` inside any of those = 200 mobs × 20 TPS = 4000 interpreter calls/sec, the exact anti-pattern the architecture forbids. Instead, events fire from the **discrete, already-existing seams**: `destroyAndAck`/`reconcileEdit`/`broadcastBlockUpdate` (block break/place), the player-add seam in `tick.go` + `removePlayer` (join/leave), `entities.add` in `structure_spawn.go` (spawn), `die`/`applyDamage` in `combat.go` (death/damage). These fire O(occurrences), not O(entities×ticks). The `tick` event itself is the ONE per-tick event — and it must be guarded (fire ONLY if a plugin actually subscribed, and consider a tick-interval throttle) so an unsubscribed server pays zero interpreter cost.

The event bus itself is **stdlib-only** — a `map[EventType][]Hook` guarded by the existing single-owner tick discipline (TICK-05). Do NOT reach for a pub/sub library, channels, reflection, or generics-heavy machinery: registration happens once at load (single-threaded), dispatch happens on the tick goroutine (single-owner), so there is no concurrency to engineer away. This is a host + a map, not a message broker.

**Primary recommendation:** Create a new `plugin/host` package (imports `plugin/starlark`, NOT `server/` internals). It owns: the manifest type + loader, the `Manager` (holds `[]*LoadedPlugin` + the `map[EventType][]Hook`), the `register` builtin (captures a `starlark.Callable` keyed by event name during module exec), and the `Emit(evt)` API the server calls at each discrete seam. The server wires `Manager.Emit(...)` calls into the ~6 discrete seam functions named above — each guarded by a "no subscribers → return immediately" fast path. Gate: a plugin's `on_block_break` hook fires when a real block breaks (driven through `destroyAndAck`), NOT on a per-tick scan; load+unload works; `-race` clean.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-02 | A plugin host + typed event bus. Manager discovers/loads/unloads plugins from a plugins dir (with a manifest); a typed event system fires the core gameplay events (tick, join/leave, break/place, spawn/death, damage); a plugin registers hooks ONCE at load via a registration API; the Go→plugin dispatch seam stays OFF the per-entity hot path (event-driven + cached, not per-tick-per-entity scan). A plugin subscribes to an event and its hook fires on the real tick. | Manager + manifest design (Architecture Patterns §Manager, §Manifest). Typed event bus = `map[EventType][]Hook` (Architecture Patterns §Event bus; Don't Hand-Roll). Register-once builtin that captures a `starlark.Callable` (Code Examples §register builtin). The discrete-seam emission map naming the EXACT Sulfur tick functions, with the hot-path trap called out (Architecture Patterns §Dispatch seam map; Common Pitfalls §1). Hook dispatch via `starlark.Call` on a fresh thread with frozen args, TICK-05/-race clean (Architecture Patterns §Dispatch; Validation Architecture). |
</phase_requirements>

<user_constraints>
## User Constraints

> No `CONTEXT.md` exists for Phase 22 yet (this research feeds `/gsd-discuss-phase` / the planner). The constraints below are extracted VERBATIM from `v4-PLAN.md` (the plan of record) and `REQUIREMENTS.md` (PLUGIN-02 + Out of Scope). The Open Questions section flags exactly what the operator must decide at Phase-22 kickoff — do NOT decide these in the plan; surface them.

### Locked decisions (from v4-PLAN.md "Architecture (user-decided)")
- **Plugins DECLARE behavior loaded once; Go executes the hot path.** A plugin registers its hooks at load time (parse/compile once). The per-tick hot path stays Go-native and calls into the plugin only at **declared seams** (event hooks), NOT by running a script per-entity-per-tick. "200 mobs × 20 TPS does NOT mean 4000 interpreter calls/sec — the interpreter runs at load + on events + on cached decisions."
- **Manifest-dir over single-`.star`.** v4-PLAN "Plugin distribution format … Lean manifest-dir (Phase 22) for versioning + the Python/Starlark runtime selector." (This is a *lean*, not a *locked* decision — see Open Questions; the manifest schema's exact fields are explicitly an open kickoff decision.)
- **Race-cleanliness (TICK-05) carries in.** "The plugin call seam runs on the tick goroutine over tick-owned state." The Docker `-race` gate covers the plugin path.
- **CGO_ENABLED=0 default binary** — Starlark core respects it (Phase 21 verified). Phase 22 adds no new deps, so this is preserved by construction.
- **No Co-Authored-By / no Claude attribution** in commits (CLAUDE.md).
- **Push to `development`** (v4-PLAN constraint #5).

### Claude's Discretion
- The exact package layout (recommended: `plugin/host`, isolated from `server/` internals — see Architecture).
- The event-bus data structure (recommended: `map[EventType][]Hook` — see Don't Hand-Roll).
- The registration-builtin name/signature (recommended `register(event_name, fn)` — see Code Examples).

### Open decisions to surface, NOT decide (from v4-PLAN + REQUIREMENTS — flag for the operator)
- **Hot-reload:** "unload/reload a plugin without a server restart? Nice-to-have; decide scope at 22." REQUIREMENTS lists it as "Hot-reload without restart (maybe) … not a committed requirement."
- **Capability/permission model:** "does a plugin declare what it can touch (entities, world, network)? Security-relevant if plugins are third-party. Decide at 22."
- **Manifest schema exact fields:** "single `.star` vs manifest-dir; lean manifest-dir … exact fields" is a kickoff decision.

### Deferred Ideas (OUT OF SCOPE for Phase 22)
- The frozen entity/world/nav handle API → **Phase 23**. Phase 22 hooks pass only SIMPLE frozen values (ints/strings/positions), NOT live entity/world handles.
- 1:1 vanilla-mob logic → **Phase 24**. No gameplay re-port in Phase 22.
- Crafting/recipes → **Phase 25**.
- The opt-in Python runtime (cgo, build tag) → **Phase 26**. But the registration/event API surface designed here must be runtime-NEUTRAL (the manifest carries a `runtime` selector) so Python reuses it unchanged.
- Folia region-awareness of hooks → **Phase 27** (single-thread seam now; regionize later).
- Per-entity-per-tick scripting → **permanently out of scope** (REQUIREMENTS "Out of Scope").
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **CGO_ENABLED=0 default binary** — Phase 22 adds NO new runtime dependency (the event bus is stdlib; Starlark is already in via Phase 21). Gate: `CGO_ENABLED=0 go build ./...` clean.
- **`go test -race` non-negotiable** for the concurrency seam. The dispatch path (tick goroutine calling a plugin hook over frozen args) is covered by the Docker `-race` gate (which runs `CGO_ENABLED=1`). Two gates, not in conflict: ship build = CGO=0, race test = CGO=1 (Phase-21 Pitfall 3).
- **TICK-05 single-owner** carries into the dispatch seam: `Emit` is called ONLY from the tick goroutine over tick-owned state; the bus map is mutated only at load (before the tick loop owns it).
- **No built-but-unwired code rule:** if hot-reload / capabilities are deferred, do NOT pre-build their machinery. Build the minimal manifest + bus that PLUGIN-02 requires.
- 1:1 jar mandate does NOT apply to Phase 22 (no gameplay logic — the host + bus are server-infra, gameplay-neutral). It carries in at Phase 24.
- **Push to `development`.**

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Scan `plugins/` dir, read manifests | `plugin/host` (Manager.Discover) | filesystem | Discovery is a load-time host concern; isolate from the tick. |
| Read/validate a manifest | `plugin/host` (manifest.go) | — | Manifest schema is host policy; the runtime selector lives here. |
| Load each plugin's `.star` once | `plugin/host` → `plugin/starlark.Load` | — | Reuse Phase-21 load-once; the host orchestrates N loads. |
| Hold N loaded plugins + the hook registry | `plugin/host` (Manager) | — | The Manager is the single owner of the registry map. |
| Capture a hook (`register(...)` builtin) | `plugin/host` (register builtin) | `plugin/starlark` (predeclared injection) | The builtin stores a `starlark.Callable` into the Manager keyed by event. |
| Decide WHEN an event occurs | `server/` (the discrete seam functions) | — | Only the server knows a block broke / a mob died — it owns the occurrence. |
| Fire the hook (`Emit`) | `server/` calls `Manager.Emit` on the tick goroutine | `plugin/host` (Emit → `starlark.Call`) | Emission is server-triggered; the Manager does the Starlark call on a fresh thread. |
| Per-entity hot path (physics/AI) | `server/` (tickAI/tickPhysics/tickEntities) | — | Stays 100% Go. **Never** calls a plugin per entity per tick. |

**Key boundary:** `plugin/host` imports `plugin/starlark` but does NOT import `server/`, `world/`, `level/`, or any tick-owned type. The server depends on `plugin/host` (one direction). Event payloads are PLAIN VALUES (ints, strings, a position struct) — NOT `*Entity`/`*tickPlayer` (those are Phase-23 frozen handles). This keeps `plugin/host` a leaf-ish package testable standalone, and keeps the live entity-exposure design out of Phase 22.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `go.starlark.net` | `v0.0.0-20260613233743-8ba36ccb83fb` `[VERIFIED: go list -m go.starlark.net@latest, 2026-06-27]` | Already in via Phase 21. `starlark.Callable`/`starlark.Function` values are stored in the bus and invoked via `starlark.Call`. | Same dep as Phase 21; no new dep added by Phase 22. |
| (stdlib) `os`, `io/fs`, `path/filepath` | — | Scan `plugins/`, read manifest + `.star` files (the dir/path loader seam Phase 21 established). | Dir walking is stdlib; no third-party FS lib. |
| (stdlib) `encoding/json` **OR** an embedded TOML-in-Go decoder | — | Parse the manifest. JSON is zero-dep + Go-native; TOML is friendlier but adds a dep. **See Open Questions / Don't Hand-Roll.** | Manifest format is an open decision; JSON keeps CGO=0 + zero new deps. |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| (stdlib) `testing`, `sync` | — | Race-gated dispatch tests; a fixture plugin that registers a hook + a test that fires the event. | The whole Phase-22 gate. No third-party test dep. |

**Do NOT introduce `xsync`/`ants`/`conc` here.** Registration is single-threaded (load time); dispatch is single-owner (tick goroutine). There is no write-contended map and no async fan-out to optimize. CLAUDE.md: "Do NOT introduce ants/xsync yet — no async subsystems exist to optimize." The async/Python off-tick rejoin is Phase 26, not Phase 22. `[CITED: CLAUDE.md Stack Patterns by Variant]`

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `map[EventType][]Hook` (typed bus) | A 3rd-party pub/sub (`asaskevich/EventBus`, `cskr/pubsub`) | **Reject.** Those are channel/goroutine-based async brokers — the OPPOSITE of the tick-owned single-goroutine dispatch we need. They add a dep, hidden goroutines (a `-race` and TICK-05 hazard), and a delivery-ordering question we don't want. A plain map is simpler, faster, and tick-owned. |
| JSON manifest | TOML (`BurntSushi/toml` / `pelletier/go-toml`) | TOML is nicer for hand-authored config (comments, no quote noise) but adds a dependency. JSON is zero-dep + already in stdlib. Decide at kickoff (Open Questions); JSON is the safe default. |
| Manifest-dir | Single `.star` with a header docstring / magic comment | v4-PLAN leans manifest-dir for versioning + the runtime selector. A single-`.star` is simpler but can't carry the `runtime=starlark|python` selector cleanly, which Phase 26 needs. Manifest-dir is the forward-compatible choice. |
| Capturing `starlark.Callable` directly | Re-exec the module per event to "find" the handler | **Reject — this IS the hot-path trap.** Re-running the module per event re-parses/re-binds; the whole point is load-once. Capture the callable value once at load. |

**Installation:** None — `go.starlark.net` is added by Phase 21. Phase 22 adds only stdlib (and possibly one manifest-format dep if TOML is chosen at kickoff). Re-verify the pin at execution:
```bash
cd D:/ender && go list -m go.starlark.net@latest   # confirm/refresh the pseudo-version
```

## Architecture Patterns

### System Architecture Diagram

```
  LOAD TIME (host goroutine, single-threaded, before the tick loop runs)
  ─────────────────────────────────────────────────────────────────────
   plugins/                              ┌──────────────────────────────┐
   ├── greeter/                          │ plugin/host : Manager.Load() │
   │   ├── plugin.json  ── manifest ────►│  for each plugin dir:        │
   │   └── main.star    ── entrypoint ──►│   1. read+validate manifest  │
   ├── spawnlog/                         │      (name,version,entry,    │
   │   ├── plugin.json                   │       runtime=starlark)      │
   │   └── main.star                     │   2. predeclared = safe +    │
   └── ...                               │      register(name, fn)      │
                                         │      builtin (host-injected) │
                                         │   3. plugin/starlark.Load(    │
                                         │       entrypoint, predeclared)│
                                         │      ── module body runs ONCE;│
                                         │         each register(...)    │
                                         │         call captures a       │
                                         │         starlark.Callable into│
                                         │         the Manager's map     │
                                         │   4. hold LoadedPlugin handle │
                                         └──────────────┬───────────────┘
                                                        │ builds:
                            hooks  map[EventType][]Hook ▼  (frozen callables)
                            on_block_break -> [greeter.cb]
                            on_entity_spawn -> [spawnlog.cb]
                            on_tick         -> []           (empty = pay nothing)
  ═══════════════════════════════ map frozen / tick loop starts ═══════════════════
  TICK TIME (tick goroutine — single owner, TICK-05)
  ─────────────────────────────────────────────────────────────────────
   A DISCRETE GAMEPLAY OCCURRENCE happens in the server, e.g.:
     server/block_break.go  destroyAndAck() ──► a block is removed
              │
              ▼  (additive call at the existing seam, guarded)
     mgr.Emit(EventBlockBreak, BlockBreakEvent{X,Y,Z, State, PlayerID})
              │
              ▼
   ┌──────────────────────────────────────────────────────────┐
   │ plugin/host : Manager.Emit(evt EventType, payload)         │
   │  hooks := m.hooks[evt]                                      │
   │  if len(hooks)==0 { return }   ◄── NO subscribers = 0 cost  │
   │  args := freeze(payload)        ── plain values, frozen     │
   │  for _, h := range hooks {                                  │
   │     th := newThread("emit")     ── FRESH thread per call    │
   │     _, err := starlark.Call(th, h.fn, args, nil)            │
   │     if err != nil { log + isolate; one bad hook ≠ tick kill }│
   │  }                                                          │
   └──────────────────────────────────────────────────────────┘

   ✗ NEVER from inside tickEntities / tickAI / tickPhysics per-entity loops.
     Those run O(entities × ticks). Emit runs O(occurrences).
```

### Pattern 1: The Manager (single owner of plugins + the bus)
**What:** One struct holds the loaded plugins and the `map[EventType][]Hook`. Built once at load; read-only during the tick.
**Why:** Single ownership = no locks (TICK-05). The map is WRITTEN only during `Load` (before the tick loop owns it) and READ during `Emit` (on the tick goroutine).
```go
// plugin/host/manager.go  — imports plugin/starlark, NOT server/
type EventType string

const (
    EventTick        EventType = "on_tick"
    EventPlayerJoin  EventType = "on_player_join"
    EventPlayerLeave EventType = "on_player_leave"
    EventBlockBreak  EventType = "on_block_break"
    EventBlockPlace  EventType = "on_block_place"
    EventEntitySpawn EventType = "on_entity_spawn"
    EventEntityDeath EventType = "on_entity_death"
    EventDamage      EventType = "on_damage"
)

type Hook struct {
    fn     starlark.Callable // frozen at module completion; safe to read+call
    plugin string            // owning plugin name (for unload + error attribution)
}

type Manager struct {
    plugins []*loadedPlugin               // each wraps plugin/starlark LoadedPlugin + manifest
    hooks   map[EventType][]Hook          // the typed bus
}
```

### Pattern 2: The `register` builtin captures a callable ONCE at load
**What:** The host injects a `register(event_name, fn)` builtin into the predeclared globals. When the plugin's module body runs (once, in `Load`), each `register(...)` call appends the passed `starlark.Callable` to `Manager.hooks[event]`.
**When:** Exactly once per hook, during module exec. NOT re-run per event.
**Why:** This is the "register-hooks-once" API. The captured `starlark.Callable` is a frozen value after `Load` returns — safe to call from the tick goroutine on a fresh thread (Phase-21 frozen-crossing guarantee).
```go
// plugin/host/register.go
func (m *Manager) makeRegisterBuiltin(pluginName string) *starlark.Builtin {
    return starlark.NewBuiltin("register", func(th *starlark.Thread, b *starlark.Builtin,
        args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
        var name string
        var fn starlark.Callable // any Starlark def / lambda / builtin is Callable
        if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 2, &name, &fn); err != nil {
            return nil, err
        }
        evt := EventType(name)
        if !isKnownEvent(evt) { // reject typo'd event names at LOAD, not silently at runtime
            return nil, fmt.Errorf("register: unknown event %q", name)
        }
        m.hooks[evt] = append(m.hooks[evt], Hook{fn: fn, plugin: pluginName})
        return starlark.None, nil
    })
}
```
*`starlark.Callable` is the interface every callable Starlark value implements (`*starlark.Function` from a `def`, a `lambda`, a `*starlark.Builtin`); `UnpackPositionalArgs` unpacks into a `*starlark.Callable` directly. `[VERIFIED: Phase-21 research API surface + Context7 /google/starlark-go bound-method/closure examples — function values are first-class and storable].*`

### Pattern 3: `Emit` — fire on a discrete occurrence, guard for zero subscribers
**What:** The server calls `m.Emit(evt, payload)` at each discrete seam. `Emit` no-ops instantly if nothing subscribed, else calls each hook on a fresh thread with frozen args.
**Why:** The zero-subscriber guard is what makes the per-tick `on_tick` event free on an unsubscribed server, and keeps every discrete seam's cost ≈ a map lookup when no plugin cares.
```go
// plugin/host/emit.go
func (m *Manager) Emit(evt EventType, payload Event) {
    hooks := m.hooks[evt]
    if len(hooks) == 0 {
        return // NO subscribers → a single map read, zero interpreter cost
    }
    args := payload.toStarlark() // plain frozen values: starlark.Int/String/a small struct
    for _, h := range hooks {
        th := newThread("emit:" + string(evt)) // FRESH per-goroutine thread (never share)
        th.SetMaxExecutionSteps(hookStepBudget) // bound a runaway hook (DoS guard)
        if _, err := starlark.Call(th, h.fn, args, nil); err != nil {
            // ISOLATION: one bad hook must not kill the tick. Log + continue.
            log.Printf("plugin %q hook %s error: %v", h.plugin, evt, err)
        }
    }
}
```

### Pattern 4: The discrete-seam emission map (THE core deliverable — named Sulfur functions)
The server adds ONE additive `m.Emit(...)` call at each of these EXISTING, already-discrete functions. None of these is a per-entity-per-tick loop. `[VERIFIED: grep/sed against D:/ender/server/*.go this session]`

| Event | Emit from (file:function) | Why it's the right seam (occurrence, not a scan) |
|-------|---------------------------|--------------------------------------------------|
| `on_block_break` | `server/block_break.go : destroyAndAck()` (line ~340) — the single funnel both insta-mine and delayed-destroy route through (`destroyBlock`). | Fires once per block actually removed. NOT in `tickBlockBreak`'s per-player progress loop (that's the dig-time scan — emit only on COMPLETION inside destroyAndAck). |
| `on_block_place` | `server/block_interact.go : reconcileEdit()` / `broadcastBlockUpdate()` (lines ~334/~365) — every authoritative block change funnels here. | Fires once per placed block. (break also passes broadcastBlockUpdate; distinguish via the seam — emit place at the place call site in `handleUseItemOn`, not the shared broadcaster, to avoid double-firing.) |
| `on_player_join` | `server/tick.go` — the join seam at ~line 1100 (`t.entities.add(p.playerEntity)` + `broadcastPlayerInfoAdd`), inside the `drainRegistrations` add branch. | Fires exactly ONCE per join, on the owner goroutine, the same place the GAMEPLAY-01 join seam already lives. |
| `on_player_leave` | `server/tick.go : removePlayer()` (line ~1127). | Fires once per leave, on the owner, where the save-on-leave snapshot is already taken. |
| `on_entity_spawn` | `server/structure_spawn.go` — the `t.entities.add(e)` seam (~line 69), "the ONLY off-tick-boundary store mutation". | Fires once per spawned mob. NOT in `tickAI`'s `naturalSpawn` per-tick attempt loop — at the actual `add`. |
| `on_entity_death` | `server/combat.go : die()` (line ~385). | Fires once per death. |
| `on_damage` | `server/combat.go : applyDamage()` (line ~187) or `actuallyHurt()` (~261). | Fires once per damage application. (Pick applyDamage for the pre-mitigation amount, or actuallyHurt for post — an Open Question; default applyDamage = the dispatcher entry.) |
| `on_tick` | `server/tick_phases.go : tickOnce()` — a single `m.Emit(EventTick, ...)` at the END of `tickOnce` (after `gametime++`), guarded by the zero-subscriber check. | The ONE per-tick event. Fires once per tick TOTAL (not once per entity). Guard + optional interval throttle keeps an unsubscribed server free. |

**The anti-seam list (NEVER emit per-entity from here):** `tickEntities`, `tickAI` (the `serverAiStep` per-mob loop), `tickPhysics` (the per-entity gravity/collision loop) — all in `server/tick_phases.go`. These iterate every entity every tick. A `starlark.Call` in any of them is the forbidden 4000-calls/sec pattern.

### Recommended Project Structure
```
plugin/
├── starlark/                 # Phase 21 (existing) — runtime, Load(), sandbox
└── host/                     # Phase 22 (new) — imports plugin/starlark, NOT server/
    ├── manifest.go           # Manifest struct + Read/Validate (name,version,entry,runtime)
    ├── manager.go            # Manager{plugins, hooks}; Discover(dir); Load(); Unload(name)
    ├── register.go           # makeRegisterBuiltin — captures starlark.Callable into hooks
    ├── event.go              # EventType consts + Event payload types + toStarlark()
    ├── emit.go               # Emit(evt, payload) — zero-sub guard + fresh-thread Call + isolation
    ├── manager_test.go       # discover/load/unload; register-once; unknown-event reject
    ├── emit_test.go          # a hook fires on Emit; zero-sub Emit is a no-op; isolation
    ├── race_test.go          # Emit from a goroutine over frozen hooks, run under -race
    └── testdata/
        └── plugins/
            ├── greeter/{plugin.json, main.star}   # registers on_block_break
            └── badhook/{plugin.json, main.star}   # hook that errors → isolation test
```

### Anti-Patterns to Avoid
- **`starlark.Call` inside a per-entity loop** (`tickAI`/`tickPhysics`/`tickEntities`). THE forbidden pattern. Emit from discrete occurrences only.
- **Re-exec'ing the module to find a handler.** Capture the `starlark.Callable` once at load; never re-run the module per event.
- **Sharing one `starlark.Thread` across emits/goroutines.** Fresh thread per `Emit` call (Phase-21 Pitfall 4).
- **A channel/goroutine-based pub/sub bus.** Hidden goroutines violate TICK-05 single-owner and add `-race` surface. The map is dispatched inline on the tick goroutine.
- **Mutating `hooks` after the tick loop starts.** Registration is load-time only. (Hot-reload, IF adopted, must re-synchronize on the tick goroutine — see Open Questions; don't build it speculatively.)
- **Silently dropping a typo'd event name** in `register("on_blockbreak", ...)`. Validate the event name at LOAD and error — a silently-never-firing hook is the worst debugging experience.
- **Letting one hook error kill the tick.** `Emit` must isolate per-hook errors (log + continue), like `tickOnce`'s panic-recover guard.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Storing/invoking a plugin callback | A custom function-pointer ABI / a string-keyed eval | Capture `starlark.Callable` at load; invoke via `starlark.Call(th, fn, args, nil)` | Starlark function values are first-class, storable, and frozen-safe. The library handles arg packing + frames. `[VERIFIED: Phase-21 + Context7]` |
| The event bus | A 3rd-party pub/sub broker (channels/goroutines) | `map[EventType][]Hook`, dispatched inline on the tick goroutine | Registration is single-threaded; dispatch is single-owner. No concurrency to engineer. A broker adds deps + hidden goroutines (TICK-05/-race hazard). |
| Sandboxing a runaway hook | A watchdog timer goroutine | `th.SetMaxExecutionSteps(budget)` per `Emit` thread | Phase 21 proved this: deterministic step counter, clean `*EvalError`, no timer race. |
| Cross-goroutine safety of stored hooks | Locks around the hooks map | Load-time-only writes + frozen callables + single-owner Emit | The map is read-only during the tick; the callables are frozen. Lock-free by construction (TICK-05). |
| Parsing the manifest | A hand-rolled key=value parser | `encoding/json` (stdlib) — or one TOML dep IF chosen at kickoff | stdlib JSON is zero-dep + CGO=0; don't write a config parser. |
| Walking the plugins dir | `ioutil`/manual recursion | `os.ReadDir` / `io/fs.WalkDir` | Stdlib dir walking; the Phase-21 dir/path loader seam already exists to extend. |

**Key insight:** Phase 22 writes almost no algorithmic code. It writes: a manifest struct + validator, a Manager that loops Phase-21's `Load`, a builtin that does `append`, an `Emit` that does a map lookup + `starlark.Call`, and ~8 one-line `m.Emit(...)` insertions at named server seams. Everything load-bearing (parse/compile, sandbox, freeze, call) is the Starlark library; everything concurrency-related is the existing TICK-05 single-owner discipline. The risk is NOT writing a bus — it's putting the `Emit` calls in the RIGHT (discrete) places and NOT in the wrong (per-entity-loop) places.

## Common Pitfalls

### Pitfall 1: The hot-path trap — emitting per-entity instead of per-occurrence
**What goes wrong:** A natural-looking but catastrophic mistake: putting `m.Emit(EventEntitySpawn, ...)` inside `tickAI` (which loops every mob every tick) or `m.Emit(EventTick, ...)` per entity, turning 200 mobs × 20 TPS into 4000 `starlark.Call`/sec. This is the EXACT pattern v4-PLAN + REQUIREMENTS forbid.
**Why it happens:** The per-tick loops (`tickEntities`/`tickAI`/`tickPhysics` in `server/tick_phases.go`) are the most "obvious" place to "check if anything happened" — but they run O(entities×ticks). The correct seams are the discrete mutation points (a block breaks, a mob is `add`ed, a player `die`s) which run O(occurrences).
**How to avoid:** Emit ONLY from the discrete-seam map in Pattern 4. The `on_tick` event is the single allowed per-tick emit, fired ONCE per tick total (not per entity) from `tickOnce`, guarded by the zero-subscriber check. Add a test that asserts a hook fires N times for N occurrences, NOT N×entities times.
**Warning signs:** An `Emit` call inside a `for _, e := range snapshot` / `for _, e := range t.entities.byID` loop. A profiler showing interpreter time scaling with mob count rather than event count.

### Pitfall 2: Double-firing on a shared broadcaster
**What goes wrong:** Both break and place funnel through `broadcastBlockUpdate`. Emitting `on_block_place` from the shared broadcaster fires it for breaks too (and vice-versa).
**Why it happens:** `reconcileEdit`/`broadcastBlockUpdate` (`server/block_interact.go`) are the shared authoritative-edit funnel; break ALSO routes block updates through the broadcaster.
**How to avoid:** Emit `on_block_break` from `destroyAndAck`/`destroyBlock` (the break funnel) and `on_block_place` from the place call site in `handleUseItemOn` — NOT both from the shared `broadcastBlockUpdate`. One emit per logical occurrence.
**Warning signs:** A place hook firing when a block is broken.

### Pitfall 3: A typo'd event name silently never fires
**What goes wrong:** `register("on_blockbreak", fn)` (missing underscore) appends to a bus key nothing ever emits. The hook silently never runs; the author has no error.
**How to avoid:** Validate the event name against the known set INSIDE the `register` builtin and return an error at LOAD. A bad plugin fails to load loudly instead of failing silently at runtime.
**Warning signs:** "My hook never fires" with no error in the log.

### Pitfall 4: Sharing a Thread across emits (silent race)
**What goes wrong:** Caching one `starlark.Thread` on the Manager and reusing it for every `Emit` races on the thread's step counter / call stack across (future) concurrent emitters, and is wrong even single-threaded if reentrant.
**How to avoid:** Fresh `newThread()` per `Emit` call (Phase-21 Pitfall 4). Frozen *values* cross goroutines; *Threads* never do. The `-race` gate catches a shared thread.
**Warning signs:** `-race` flags a data race on `Thread.steps` / the eval stack.

### Pitfall 5: One bad hook kills the tick
**What goes wrong:** A plugin hook panics or returns an error and, unhandled, propagates up and crashes `tickOnce` (the panic-recover guard catches a panic but an unhandled error path could still misbehave / a panic mid-emit aborts the remaining hooks for that event).
**How to avoid:** `Emit` logs + continues on a hook error (per-hook isolation), and the `tickOnce` panic-recover guard (already in `server/tick_phases.go`) is the backstop. Consider a per-hook `recover()` inside the `Emit` loop so one panicking hook doesn't skip the others.
**Warning signs:** One misbehaving plugin disconnecting every player (the tick goroutine dying stops keepalive — exactly what `tickOnce`'s recover comment warns about).

### Pitfall 6: `Emit` allocates the args even when nobody's subscribed
**What goes wrong:** Building the frozen Starlark arg tuple BEFORE the zero-subscriber check allocates on every discrete event even when no plugin cares — a per-occurrence allocation in gameplay-hot funnels (block break, damage).
**How to avoid:** Check `len(hooks)==0` FIRST, return before constructing `args`. The cost of an unsubscribed event = one map read.
**Warning signs:** Allocation profile shows `toStarlark`/arg-tuple allocs scaling with block-breaks/damage on a server with no plugins loaded.

### Pitfall 7: `-race` needs CGO=1, ship binary needs CGO=0 (carried from Phase 21)
**What goes wrong:** `CGO_ENABLED=0 go test -race ./...` fails (`-race requires cgo`).
**How to avoid:** Two gates — ship build `CGO_ENABLED=0 go build ./...`; race test `CGO_ENABLED=1 go test -race ./plugin/host/`. Already wired in `.github/workflows/go.yml`. `[VERIFIED: Phase-21 Pitfall 3]`

## Code Examples

### A fixture plugin (`testdata/plugins/greeter/main.star`)
```python
# registers ONCE at load; the hook fires on the real event, not per tick.
def on_break(x, y, z, state, player_id):
    log("block broke at %d,%d,%d" % (x, y, z))   # `log` = a server-neutral builtin from Phase 21

register("on_block_break", on_break)             # capture the callable into the bus
```

### The manifest (`testdata/plugins/greeter/plugin.json`)
```json
{
  "name": "greeter",
  "version": "0.1.0",
  "entrypoint": "main.star",
  "runtime": "starlark"
}
```

### Manager: discover + load (load-once per plugin)
```go
// plugin/host/manager.go
func (m *Manager) LoadDir(root string) error {
    entries, err := os.ReadDir(root)
    if err != nil { return err }
    for _, e := range entries {
        if !e.IsDir() { continue }
        dir := filepath.Join(root, e.Name())
        man, err := readManifest(filepath.Join(dir, "plugin.json"))
        if err != nil { return fmt.Errorf("plugin %s: %w", e.Name(), err) }
        if man.Runtime != "starlark" {
            continue // Phase 26 handles runtime=="python"; skip for the default build
        }
        predeclared := starlarkpkg.SafeGlobals() // Phase-21 safe subset...
        predeclared["register"] = m.makeRegisterBuiltin(man.Name) // ...plus the host builtin
        lp, err := starlarkpkg.Load(filepath.Join(dir, man.Entrypoint), predeclared)
        if err != nil { return fmt.Errorf("plugin %s load: %w", man.Name, err) }
        // module body ran ONCE; its register(...) calls already populated m.hooks.
        m.plugins = append(m.plugins, &loadedPlugin{manifest: man, loaded: lp})
    }
    return nil
}
```
*(`SafeGlobals()`/`Load(path, predeclared)` are the Phase-21 surface; the planner confirms the exact exported names at execution — Phase 21 used a `safeGlobals()` + a `Load(path string)`; Phase 22 needs `Load` to accept the predeclared set so the host can inject `register`. If Phase 21 shipped `Load` without a predeclared param, the small extension is a Phase-22 task. `[ASSUMED: A2]`)*

### Unload (the minimal version PLUGIN-02 requires)
```go
// Drop a plugin's hooks from every event slice + forget the plugin handle.
// MUST run on the tick goroutine if the tick loop is live (TICK-05) — or before it starts.
func (m *Manager) Unload(name string) {
    for evt, hooks := range m.hooks {
        kept := hooks[:0]
        for _, h := range hooks {
            if h.plugin != name { kept = append(kept, h) }
        }
        m.hooks[evt] = kept
    }
    // remove from m.plugins...
    // Starlark has no explicit teardown; dropping the references lets GC reclaim the module.
}
```
*Hot-reload (unload+reload without restart) is unload + LoadDir of the one plugin, re-synchronized on the tick goroutine — but whether to BUILD it is an Open Question; the unload primitive above is the minimum PLUGIN-02 asks for ("discovers/loads/unloads").*

### Server-side: the additive emit at a discrete seam (block break)
```go
// server/block_break.go — inside destroyAndAck, AFTER the block is actually removed.
// `t.plugins` is the *host.Manager, wired into TickLoop at construction.
func (t *TickLoop) destroyAndAck(p *tickPlayer, pos pk.Position, sequence int32) {
    // ... existing 1:1 destroyBlock + ack logic (unchanged) ...
    if t.plugins != nil {
        t.plugins.Emit(host.EventBlockBreak, host.BlockBreakEvent{
            X: int(pos.X), Y: int(pos.Y), Z: int(pos.Z),
            State: int(brokenState), PlayerID: int(p.entityID),
        })
    }
}
```

### Event payload → frozen Starlark args (plain values only — NO live handles)
```go
// plugin/host/event.go
type BlockBreakEvent struct{ X, Y, Z, State, PlayerID int }

func (e BlockBreakEvent) toStarlark() starlark.Tuple {
    return starlark.Tuple{
        starlark.MakeInt(e.X), starlark.MakeInt(e.Y), starlark.MakeInt(e.Z),
        starlark.MakeInt(e.State), starlark.MakeInt(e.PlayerID),
    } // all immutable scalars → inherently frozen, race-safe to read on any thread
}
```
*Phase 22 passes ONLY scalars/positions. Live entity/world/nav frozen HANDLES are Phase 23 — do not design them here.*

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `ExecFile` + globals lookup (Phase-21 README example) | `ExecFileOptions` + a curated predeclared `StringDict` that includes `register` | FileOptions era (Phase 21) | The host injects `register` into the predeclared set; the captured callables are the bus entries. |
| Channel/goroutine pub-sub for game events | Inline `map[EventType][]Hook` dispatch on the single tick-owner | This project's TICK-05 model | No hidden goroutines; `-race` clean by single ownership; deterministic ordering. |

**Deprecated/outdated for our use:**
- 3rd-party Go event-bus libraries (`asaskevich/EventBus` et al.) — async/channel model is the wrong fit for a single-owner tick.
- A single-`.star`-no-manifest plugin format — can't carry the `runtime` selector Phase 26 needs.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Manifest format = JSON (zero-dep, CGO=0). TOML is an alternative if the operator wants comments. | Standard Stack / Open Questions | Low — format is a kickoff decision; JSON is the safe default. Changing it later is a small refactor. |
| A2 | Phase-21's `Load` can accept (or be cheaply extended to accept) a host-supplied predeclared `StringDict` so the host can inject `register`. Phase-21 research shows `Load(path string)` + an internal `safeGlobals()`; Phase 22 needs the predeclared set to be injectable. | Code Examples | Low–Medium — if `Load` is hard-coded to `safeGlobals()`, a tiny Phase-22 extension (add a predeclared param / a `LoadWith` variant) is needed. Verify against the executed Phase-21 code at planning. |
| A3 | The exact line numbers cited for the server seams (`destroyAndAck` ~340, `die` ~385, join ~1100, etc.) are current as of this session's read; they may shift as Phase 21/other work lands. The FUNCTION NAMES are the stable anchors, not the line numbers. | Architecture §Dispatch map | Low — names are stable; the planner greps by name at execution. |
| A4 | `on_damage` fires from `applyDamage` (pre-mitigation dispatcher entry). `actuallyHurt` (post-armor/absorb) is the alternative. | Architecture §Dispatch map | Low — a semantic choice (raw vs final damage); flag for the operator. Default = applyDamage. |

## Open Questions

> These are the v4-PLAN/REQUIREMENTS "decide at Phase 22 kickoff" items. SURFACE for the operator/discuss-phase — do NOT decide in the plan.

1. **Hot-reload scope (yes / no / deferred).**
   - What we know: the `Unload` + `LoadDir(one plugin)` primitives are cheap to build; re-sync must happen on the tick goroutine (TICK-05). REQUIREMENTS lists hot-reload as "maybe … not a committed requirement."
   - What's unclear: is reload-without-restart wanted in v4, or is load+unload-at-startup enough? Building reload speculatively violates the no-built-but-unwired rule.
   - Recommendation: ship `Unload` (PLUGIN-02 says "unloads"); make full hot-RELOAD an explicit operator yes/no at kickoff. If deferred, do NOT build the file-watch/re-sync machinery.

2. **Capability/permission model (declare-what-you-touch).**
   - What we know: v4-PLAN: "does a plugin declare what it can touch … security-relevant if plugins are third-party." Phase 22 events pass only scalars (no live handles yet), so the attack surface is small NOW; it grows in Phase 23 (entity/world handles).
   - What's unclear: do plugins declare capabilities (`capabilities: ["events", "world.read"]`) in the manifest now, or is that deferred until Phase 23 exposes real state?
   - Recommendation: add an OPTIONAL `capabilities` array to the manifest schema now (so the field EXISTS for forward-compat) but do NOT enforce it in Phase 22 (nothing sensitive is exposed yet). Enforcement lands with the Phase-23 handle API. Operator confirms.

3. **Manifest schema exact fields.**
   - What we know: minimum = `name`, `version`, `entrypoint`, `runtime` (starlark|python — the Phase-26 selector). Optional = `capabilities`, `description`, `author`, `sulfur_api_version`.
   - What's unclear: format (JSON vs TOML — A1) and the optional-field set.
   - Recommendation: lock the 4 required fields; treat optional fields + format as a quick kickoff decision. JSON default.

4. **`on_damage` payload semantics: raw or post-mitigation?** (A4) — `applyDamage` (raw amount) vs `actuallyHurt` (after armor/absorb). Operator picks; default raw.

5. **`on_tick` throttling.** Should `on_tick` fire every tick (20/s) or be throttleable (e.g., every N ticks per subscriber)? Every-tick is simplest; a throttle is a nice-to-have. Default: every tick, with the zero-subscriber guard so it's free when unused.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `go.starlark.net` | PLUGIN-02 (via Phase 21) | ✓ (added by Phase 21) | `v0.0.0-20260613233743-8ba36ccb83fb` | — |
| `plugin/starlark` package | PLUGIN-02 (the host wraps it) | ✓ once Phase 21 executes | — | **BLOCKING if Phase 21 not done** — Phase 22 builds on it. |
| Go toolchain ≥ 1.25 | starlark floor | ✓ | project go 1.25.0 / 1.26.1 | — |
| stdlib `os`/`io/fs`/`encoding/json` | manifest + dir scan | ✓ | — | — |
| C compiler (for `-race` only) | race-test gate | ✓ in CI | — | n/a — `-race` runs CGO=1 in CI; ship binary stays CGO=0 |

**Missing dependencies with no fallback:** Phase 21 (`plugin/starlark`) must be executed first — it is the direct dependency. Phase 22 cannot start until `plugin/starlark.Load` + `SafeGlobals` exist.
**Missing dependencies with fallback:** None — Phase 22 adds only stdlib (+ optionally one TOML dep if chosen).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `sync` for the race proof) |
| Config file | none (Go convention) |
| Quick run command | `go test ./plugin/host/` |
| Full suite (race) command | `CGO_ENABLED=1 go test -race ./plugin/host/` |
| Ship-build gate | `CGO_ENABLED=0 go build ./...` |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| PLUGIN-02 | Manager discovers + loads plugins from a dir (reads manifest, calls Load once each) | unit | `go test ./plugin/host/ -run TestLoadDir` | ❌ Wave 0 |
| PLUGIN-02 | `register("on_block_break", fn)` captures the callable into the bus at LOAD | unit | `go test ./plugin/host/ -run TestRegisterCaptures` | ❌ Wave 0 |
| PLUGIN-02 | Unknown event name in `register` errors at load (not silent) | unit | `go test ./plugin/host/ -run TestRegisterUnknownEvent` | ❌ Wave 0 |
| PLUGIN-02 | `Emit(evt, payload)` fires every subscribed hook with the frozen args | unit | `go test ./plugin/host/ -run TestEmitFires` | ❌ Wave 0 |
| PLUGIN-02 | **Hook fires on a real OCCURRENCE, NOT per-tick-scan** — a fixture driven through `destroyAndAck` fires the hook exactly ONCE per break, and an N-entity world does NOT multiply the count | integration | `go test ./server/ -run TestBlockBreakEmitsOncePerBreak` | ❌ Wave 0 |
| PLUGIN-02 | `Emit` with zero subscribers is a no-op (no alloc, no call) | unit | `go test ./plugin/host/ -run TestEmitNoSubscribers` | ❌ Wave 0 |
| PLUGIN-02 | One erroring/panicking hook is isolated — other hooks still fire, tick survives | unit | `go test ./plugin/host/ -run TestHookIsolation` | ❌ Wave 0 |
| PLUGIN-02 | Unload removes a plugin's hooks from every event | unit | `go test ./plugin/host/ -run TestUnload` | ❌ Wave 0 |
| PLUGIN-02 | Emit over frozen hooks from a goroutine is race-clean | race | `CGO_ENABLED=1 go test -race ./plugin/host/ -run TestEmitRace` | ❌ Wave 0 |
| PLUGIN-02 | Default binary is CGO=0 (no new cgo dep) | build | `CGO_ENABLED=0 go build ./...` | ✓ (gate exists) |

**The signature test** (proves the architecture, not just the plumbing): `TestBlockBreakEmitsOncePerBreak` — load a plugin that increments a counter on `on_block_break`, break ONE block in a world with N mobs present, assert the counter == 1 (NOT N, NOT N×ticks). This is the concrete form of "event-driven not per-tick-scan."

### Sampling Rate
- **Per task commit:** `go test ./plugin/host/`
- **Per wave merge / phase gate:** `CGO_ENABLED=1 go test -race ./plugin/host/ ./server/` AND `CGO_ENABLED=0 go build ./...`
- **Phase gate (full):** the Docker `-race` image (CGO=1) green + the CGO=0 static build green — both already in `.github/workflows/go.yml`; `./...` picks up the new `plugin/host` package and the server seam edits.

### Wave 0 Gaps
- [ ] `plugin/host/testdata/plugins/greeter/{plugin.json, main.star}` — a plugin that registers `on_block_break`.
- [ ] `plugin/host/testdata/plugins/badhook/{plugin.json, main.star}` — a hook that errors/panics (isolation test).
- [ ] `plugin/host/manager_test.go`, `emit_test.go`, `race_test.go` — the unit + race suite.
- [ ] `server/<seam>_test.go` extension — `TestBlockBreakEmitsOncePerBreak` (the per-occurrence-not-per-scan proof), wired against `destroyAndAck` with a mock Manager.
- [ ] Possibly extend Phase-21 `Load`/`SafeGlobals` to accept an injectable predeclared set (A2) — verify against executed Phase-21 code.

*(No framework install needed — Go stdlib `testing`. The race gate already exists in CI.)*

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V5 Input Validation / untrusted-code execution | yes | The Phase-21 sandbox carries: each `Emit` thread gets `SetMaxExecutionSteps` (a runaway hook is bounded, not a hang). The `register` builtin validates event names. Manifest fields are validated at load. |
| V1.4 Trust boundaries / least privilege | partial (deferred) | The capability/permission model (Open Q #2) is the future control. Phase 22 exposes ONLY scalars (no live state), so the boundary is narrow now; it widens at Phase 23 (handles) where capabilities should be enforced. |
| V6 Cryptography | no | None in this phase. |
| V2/V3/V4 Auth/Session/Access | no | No auth surface; plugins are operator-installed local files, not network-facing. |

### Known Threat Patterns for the plugin host
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| A hook with an infinite loop hangs the tick | Denial of Service | `th.SetMaxExecutionSteps(budget)` per `Emit` thread → `*EvalError`, tick continues. (Phase-21 verified mechanism.) |
| A hook panics/errors and kills the tick goroutine (disconnects everyone — keepalive stops) | Denial of Service | Per-hook error+panic isolation in `Emit` (log + continue) + the existing `tickOnce` recover backstop. |
| A plugin registers for a sensitive event it shouldn't (third-party trust) | Elevation of Privilege | Deferred capability model (Open Q #2). NOW: only scalars exposed, narrow surface. Manifest could declare `capabilities` (forward-compat field). |
| A malformed/hostile manifest (path traversal in `entrypoint`) | Tampering | Validate `entrypoint` is a simple filename within the plugin dir (reject `..`/absolute paths) when building the load path. |
| Per-occurrence allocation amplification (a flood of events from a hostile client action) | Denial of Service | The zero-subscriber guard means no cost when unsubscribed; the step budget bounds each hook; the discrete seams are already rate-limited by the gameplay actions that drive them. |

## Sources

### Primary (HIGH confidence)
- **`D:/ender/server/tick_phases.go`** (read in full this session) — the tick pipeline: the per-entity loops `tickEntities`/`tickAI`/`tickPhysics` (the anti-seams), `tickOnce` (the `on_tick` seam), the panic-recover guard, TICK-05 single-owner discipline. `[VERIFIED]`
- **`D:/ender/server/{block_break.go, block_interact.go, combat.go, structure_spawn.go, tick.go}`** (grep/sed this session) — the discrete event seam functions + their signatures: `destroyAndAck`/`destroyBlock` (~340/353), `reconcileEdit`/`broadcastBlockUpdate` (~334/365), `applyDamage`/`actuallyHurt`/`die` (187/261/385), `entities.add` spawn seam (structure_spawn ~69), the join seam (tick.go ~1100) + `removePlayer` (~1127). `[VERIFIED]`
- **`.planning/phases/21-starlark-runtime-foundation/21-RESEARCH.md`** — the Phase-21 surface this builds on: `Load(path)`, `safeGlobals()`, `ExecFileOptions`, `starlark.Call`, `NewBuiltin`, frozen cross-goroutine reads, one-Thread-per-goroutine, `SetMaxExecutionSteps`, the CGO=0 / `-race`-needs-CGO=1 split. `[VERIFIED]`
- **`.planning/v4-PLAN.md`** + **`.planning/REQUIREMENTS.md`** — PLUGIN-02 scope, the DECLARE-once architecture, the off-the-per-entity-hot-path rule, the open decisions (manifest/hot-reload/capabilities), Out-of-Scope (per-entity-per-tick scripting). `[CITED]`
- **Context7 `/google/starlark-go`** — function values are first-class, storable (bound-method + closure examples), invoked via `starlark.Call`; the embed pattern. `[CITED]`
- **`go list -m go.starlark.net@latest`** → `v0.0.0-20260613233743-8ba36ccb83fb`. `[VERIFIED 2026-06-27]`

### Secondary (MEDIUM confidence)
- None needed — the architecture is grounded in the actual server code + the verified Phase-21 surface.

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Standard stack (no new dep; map-based bus; stdlib manifest): **HIGH** — Phase-22 adds nothing beyond Phase-21's dep + stdlib; verified.
- Architecture (Manager + map bus + discrete-seam emit): **HIGH** — the seam functions were read by name in the actual `server/*.go`; the hot-path anti-seams are the actual per-entity loops in `tick_phases.go`.
- Dispatch (register-captures-callable, fresh-thread Call, frozen scalars): **HIGH** — Phase-21-verified mechanism + Context7-confirmed first-class storable functions.
- Pitfalls (hot-path trap, double-fire, silent typo, thread-share, isolation): **HIGH** — derived from the actual code structure + Phase-21 verified gotchas.
- Open decisions (manifest fields / hot-reload / capabilities): **N/A (surfaced, not decided)** — explicitly operator-owned per v4-PLAN/REQUIREMENTS.

**Research date:** 2026-06-27
**Valid until:** ~30 days for the Starlark API (stable). The server seam LINE NUMBERS may drift as other work lands — re-grep by FUNCTION NAME at execution (the names are the stable anchors). Re-verify Phase-21's exported `Load`/`SafeGlobals` signature (A2) against the executed Phase-21 code before planning the host.
