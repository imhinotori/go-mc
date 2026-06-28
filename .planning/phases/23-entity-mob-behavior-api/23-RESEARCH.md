# Phase 23: Entity/mob behavior API — Research

**Researched:** 2026-06-27
**Domain:** A declarative mob-behavior API for Starlark plugins — a plugin DECLARES a mob's attributes/goals/AI hooks ONCE at load; Go runs the hot path (goal arbitration, A* nav, swept collision, physics, tick) and calls the declared hooks only at decision seams; plus a FULL-OVERRIDE path. The load-bearing design artifact is the Go→Starlark bridge that exposes the live entity/world/nav as FROZEN, tick-owned-safe handles, with READ-ONLY access distinguished from MUTATE-THROUGH-A-TICK-OWNED-SEAM.
**Confidence:** HIGH for the Sulfur side (every named type/function — `Entity`, `mobAI`, `goalSelector`, `Goal`, `groundNavigation`, `attribute.Map`, `moveEntity`, `entityStore.move`, `ChunkManager.GetBlock/SetBlock`, the `tickAI` driver — was read in full this session from `D:/ender/server/*.go`, `world/manager.go`, `level/attribute/*.go`). HIGH for the Starlark freezing model (Context7 `/google/starlark-go` + the Phase-21 verified surface). MEDIUM only on the exact final handle surface (which is deliberately an operator decision — see Open Questions).

## Summary

Phase 23 is the FIRST phase that lets a plugin touch live game state. Phases 21–22 deliberately exposed only frozen SCALARS (block-pos ints, a damage float). Phase 23 must expose the live `*server.Entity`, the `*world.ChunkManager`, and the per-mob `groundNavigation` to Starlark — but it must do so without (a) letting a plugin reach a `*Entity` pointer that escapes to another goroutine, (b) deep-freezing the live Go object (which would make the tick unable to mutate it), or (c) running a Starlark interpreter pass per-mob-per-tick (the forbidden 4000-calls/sec pattern). The answer to all three is one pattern: a **thin handle** — a custom `starlark.Value` that wraps ONLY an entity id (an `int32`) plus a back-pointer to the tick-owned `*TickLoop`, never the live `*Entity`. Every read re-resolves the entity through `t.entities.get(id)` ON the tick goroutine; every write routes through an EXISTING tick-owned mutator (`t.moveEntity`, `t.entities.move`, the attribute `Map`, `ChunkManager.SetBlock`). The handle's `Freeze()` is a no-op because the handle holds no mutable Starlark state — the Go object it points at is frozen-irrelevant (it is governed by TICK-05, not by Starlark's frozen flag).

The Sulfur AI tick is already structured EXACTLY for the declare-once model. `tickAI` (server/tick_phases.go) snapshots every entity with `e.ai != nil` and calls `e.ai.serverAiStep(t, e)`. `serverAiStep` (server/ai_mob.go) runs `goalSelector.tick` then `tickRunningGoals` then `navigation.tick`. A `Goal` (server/ai_goal.go) is a Go interface — `canUse`/`canContinueToUse`/`start`/`stop`/`tick`/`flags` — and goals SET targets, they never move the mob (the navigation/physics does the moving). So a Starlark-declared goal slots in as a NEW `Goal` implementation: a `starlarkGoal` struct whose `canUse`/`tick` methods call the plugin's frozen `starlark.Callable`s via `starlark.Call`, passing the entity/world/nav HANDLES as args. The goal STRUCTURE (which callbacks, what priority, what flags, what attributes) is parsed ONCE at load via a `declare_mob(...)` builtin; only the active goal's decision callbacks invoke the interpreter, and only when the `goalSelector` runs them — never a full pass per mob per tick.

**Primary recommendation:** Create a new `plugin/entity` package (imports `plugin/starlark` + the entity/world/nav HANDLE types, NOT a circular import of `server` — see Architecture for the import-direction resolution). It owns: the three handle `starlark.Value` types (`entityHandle`, `worldHandle`, `navHandle`), the `declare_mob(name, attributes={}, goals=[...])` builtin that captures the declaration into a registry, and the `starlarkGoal` adapter that implements `server.Goal` by calling the plugin's frozen callbacks. The server adds: a `*Entity.ai` built from a Starlark declaration at spawn (the `newPigAI()` analogue), and a way for the handle's mutators to call `t.moveEntity`/`t.entities.move`/`SetBlock` on the tick goroutine. Gate: a trivial `.star` mob (a wander mob — declare one MOVE goal that picks a random nav target) spawns, ticks, and MOVES via the existing `groundNavigation` + `moveEntity`, `-race` clean.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-03 | A declarative entity/mob behavior API. A plugin DECLARES a mob's attributes/goals/AI once (loaded at parse time); Go runs the hot path (physics/pathfinding/tick/collision) calling the declared hooks; a FULL-OVERRIDE path lets a plugin replace a mob's whole decision logic. The Go-side bridge exposes the entity/world/nav API to Starlark as FROZEN, tick-owned-safe handles (read-only vs mutate-through-a-tick-owned-seam distinguished). A trivial custom mob declared in Starlark spawns, ticks, and moves via the Go nav, `-race` clean. | The thin-handle pattern wrapping an entity id, not a live `*Entity` (Architecture §Handle pattern; Code Examples §entityHandle). The read-vs-mutate seam table naming every real Sulfur mutator (`moveEntity`, `entities.move`, `attribute.Map`, `SetBlock`) (Architecture §Read-vs-mutate table). The `starlarkGoal` adapter slotting into the existing `Goal` interface + `serverAiStep`/`tickAI` driver (Architecture §Declare-once model; Code Examples §starlarkGoal, §declare_mob). The full-override path (Architecture §Full-override). The hot-path cache (declare-once, callbacks-only-when-active) (Architecture §Hot-path; Common Pitfalls §3). The wander-mob gate + `-race` coverage (Validation Architecture). The frozen-handle/escape pitfalls (Common Pitfalls §1, §2, §4). SUB-ATTRIB integration via `attribute.Map` (Architecture §Attributes). |
</phase_requirements>

<user_constraints>
## User Constraints

> No `CONTEXT.md` exists for Phase 23 yet (this research feeds `/gsd-discuss-phase` / the planner). The constraints below are extracted VERBATIM from `v4-PLAN.md` (the plan of record) and `REQUIREMENTS.md` (PLUGIN-03). The Open Questions section flags exactly what the operator must lock at Phase-23 kickoff — chiefly the exact frozen-handle API surface (v4-PLAN calls it "the hardest design question").

### Locked decisions (from v4-PLAN.md "Architecture (user-decided)" + REQUIREMENTS PLUGIN-03)
- **Plugins DECLARE behavior loaded once; Go executes the hot path.** "A plugin registers its hooks at load time (parse/compile once). The per-tick hot path — physics, pathfinding, collision, the tick loop itself — stays Go-native and calls into the plugin only at declared seams." The interpreter runs at load + on cached decisions, NOT per-entity-per-tick.
- **A plugin CAN fully override a mob.** "When an author wants total control, the behavior API lets a plugin replace a mob's whole decision logic (still through the declared-seam model, just owning all the seams). The Go hot path still runs the mechanical work (move the AABB, sweep collision, step the nav) — the plugin owns WHAT to do, Go owns HOW to execute it efficiently."
- **FROZEN, tick-owned-safe handles.** "The Go→Starlark bridge exposes entity/world/nav as FROZEN, tick-owned-safe handles." The plugin call seam runs ON the tick goroutine over tick-owned state.
- **Read-only vs mutate-through-a-tick-owned-seam DISTINGUISHED.** PLUGIN-03 explicitly: handles distinguish "read-only vs mutate-through-a-tick-owned-seam."
- **Race-cleanliness (TICK-05) carries in.** "The Docker `-race` gate covers the plugin path exactly as it covers the async subsystems today." A declared mob ticking must be `-race` clean.
- **CGO_ENABLED=0 default binary** — Phase 23 adds no new runtime dep (Starlark is in via Phase 21; the handles are stdlib + existing Sulfur types). Preserved by construction.
- **No Co-Authored-By / no Claude attribution** in commits (CLAUDE.md).
- **Push to `development`** (v4-PLAN constraint #5).

### Claude's Discretion
- The exact package layout (recommended: a new `plugin/entity` or `server`-adjacent package — see Architecture §Import direction for the dependency-cycle resolution, which is the one real layout constraint).
- The handle method/field NAMES on the Starlark side (`entity.health` vs `entity.hp`, `entity.move_to(...)` vs `entity.path_to(...)`) — recommend vanilla-ish names; the SET is the operator decision (Open Questions §1).
- The `declare_mob` builtin signature shape (positional vs kwargs) — recommend kwargs for readability.

### Deferred Ideas (OUT OF SCOPE for Phase 23)
- **Porting any SPECIFIC vanilla mob 1:1 → Phase 24.** Phase 23 is the API + a TRIVIAL custom (non-vanilla) mob proving it. Do NOT re-express the Pig/Witch/Zombie AI in Starlark here.
- **Crafting/recipes/menus → Phase 25.** No item/menu bridge in Phase 23.
- **Python runtime → Phase 26.** The handle API must be runtime-NEUTRAL (a Python plugin in Phase 26 reuses the same entity/world/nav handle CONCEPTS) but Phase 23 wires only Starlark.
- **Folia region-awareness of the handle seam → Phase 27.** Phase 23 builds the single-thread tick-owned handle; Phase 27 makes a hook run on its region's thread. Do NOT design the handle around regions now.
- **targetSelector / combat-target acquisition as a plugin seam (attack AI) → can ride Phase 23 OR defer.** The existing `serverAiStep` skips `targetSelector` for the passive Pig; whether the declarative API exposes a TARGET-flag goal (attack target acquisition) in Phase 23 or waits for Phase 24's vanilla-mob dogfood is an Open Question (§5). The TRIVIAL gate mob only needs a MOVE goal.
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **GAMEPLAY IS A 1:1 PORT OF VANILLA — but does NOT apply to a CUSTOM mob.** Phase 23's gate mob is a NON-vanilla custom mob (a "wander mob"); it is free of the 1:1 mandate by definition (v4-PLAN: "Custom (non-vanilla) plugins are free of the mandate by definition"). The 1:1 mandate carries in at Phase 24 (vanilla mobs AS plugins). Phase 23 must NOT re-port the Pig — it builds the API and a trivial proof.
- **CGO_ENABLED=0 default binary** — no new runtime dep. Gate: `CGO_ENABLED=0 go build ./...` clean.
- **`go test -race` non-negotiable** for the concurrency seam. A declared mob ticking (handle read/mutate on the tick goroutine) is covered by the Docker `-race` gate (CGO=1). Two gates, not in conflict: ship build = CGO=0, race test = CGO=1 (Phase-21 Pitfall 3).
- **TICK-05 single-owner** carries into the handle seam: every handle read re-resolves through the tick-owned store ON the tick goroutine; every handle mutate routes through an existing tick-owned mutator ON the tick goroutine. No handle ever escapes to another goroutine holding a live pointer.
- **No built-but-unwired code rule:** build the minimal declare-once + handle surface PLUGIN-03 requires (entity read + nav move + the wander gate). Do NOT pre-build the full-override execution path, the targetSelector seam, or a capability enforcer unless the operator locks them in (Open Questions).
- **Push to `development`.**

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Parse a mob declaration ONCE at load (`declare_mob`) | `plugin/entity` (declare builtin) | `plugin/starlark` (predeclared injection) | Declaration is load-time; capture the callables + attrs into a registry. |
| Hold the declared-mob registry (name → declaration) | `plugin/entity` (registry) | — | One owner; read at spawn to build `e.ai`. |
| Expose a live entity to Starlark (read) | `plugin/entity` (`entityHandle`) | `server` (tick-owned `entityStore`, re-resolved by id) | A handle is a thin id wrapper; reads re-resolve on the tick goroutine. |
| Mutate an entity (velocity / move-to) | `server` (`moveEntity` / `entities.move` / the nav) | `plugin/entity` (`entityHandle` callable forwards) | Mutation MUST route through the existing tick-owned mutator — never a raw field write. |
| Expose the world (read a block) | `plugin/entity` (`worldHandle`) | `server`/`world` (`ChunkManager.GetBlock`) | Read-only block lookup through the existing read API. |
| Mutate the world (set a block) | `server`/`world` (`ChunkManager.SetBlock` + `broadcastBlockUpdate`) | `plugin/entity` (`worldHandle` callable forwards) | A set MUST route through the existing edit seam so neighbors/dirty/broadcast all fire. |
| Run a declared goal's decision callback | `server` (`goalSelector.tick` → `Goal.tick`) | `plugin/entity` (`starlarkGoal` → `starlark.Call`) | The Go selector drives arbitration; the adapter invokes the plugin only when the goal is active. |
| The mechanical hot path (A* nav, swept collision, gravity) | `server` (`groundNavigation`, `moveEntity`, `tickPhysics`) | — | Stays 100% Go. The plugin owns WHAT (the target); Go owns HOW (the move). |
| Read/set a mob's attributes | `level/attribute` (`Map.GetValue` / `GetInstance.SetBaseValue`) | `plugin/entity` (`entityHandle` attr forwards) | SUB-ATTRIB landed; declared attrs seed the `attribute.Map`. |

**Key boundary / the one real layout constraint (import direction):** the handle types need to call `*server.TickLoop` methods (`moveEntity`, `entities.get`, `world.GetBlock/SetBlock`), but `server` must NOT import a plugin package that imports `server` (a cycle). Two clean resolutions, both viable — the planner picks one at execution:
- **(A) Handles live IN `server`** (e.g. `server/plugin_entity.go`), implementing `starlark.Value`; the `plugin/starlark` runtime is imported by `server` (one direction). This is the SIMPLEST and matches how the AI code already lives in `server` (the Pig goals are `server` types). Recommended for Phase 23.
- **(B) A narrow interface in `plugin/entity`** (e.g. `type Mob interface { Pos() (x,y,z float64); MoveTo(x,y,z float64); ... }`) that `server.*Entity`/`*TickLoop` satisfies; `server` constructs the handles passing itself as the interface. Cleaner separation but more plumbing.
Both keep the import graph acyclic. Recommendation: **(A)** for Phase 23 (the AI types are already `server` types; co-locating the handles avoids inventing an interface layer the only caller is `server` anyway), revisit (B) if Phase 26's Python runtime wants to share the handle abstraction.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `go.starlark.net` | `v0.0.0-20260613233743-8ba36ccb83fb` `[VERIFIED: go list -m go.starlark.net@latest + grep go.mod, 2026-06-27]` | Already pinned (Phase 21). Phase 23 uses the EXTENSION interfaces: `starlark.Value` (the handle base), `starlark.HasAttrs` (`Attr`/`AttrNames` for `entity.health`), `starlark.HasSetField` (`SetField` for a mutate-through-seam like `entity.velocity = (...)`), `*starlark.Builtin` bound as a method (for `entity.move_to(x,y,z)`), `starlark.Call`. | Same dep as 21/22; no new dep. The custom-Value interfaces are the documented extension path. |
| (existing) `github.com/imhinotori/sulfur/level/attribute` | in-repo | SUB-ATTRIB. `attribute.Map` (per-entity), `NewMapForEntity(name)`, `GetValue(name)`, `GetInstance(name).SetBaseValue(v)`. A declared mob's attrs seed/override this. | The attribute system landed in v3.1; reuse it, do not re-model attributes. |
| (existing) `github.com/imhinotori/sulfur/data/entity` | in-repo | The generated 776 entity TYPE table (`entity.Entity{ID,Name,Width,Height,Type}`). A declared custom mob needs a base type record for its AABB dims + wire id. | Generated; a custom mob either reuses an existing type's dims (e.g. `entity.Pig`) or the declaration supplies width/height (Open Q §3). |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| (stdlib) `testing`, `sync` | — | The wander-mob `-race` gate: declare a mob, add it to the store, tick it, assert it moved; run under `-race`. | The whole Phase-23 gate. No third-party test dep. |

**Do NOT introduce `xsync`/`ants`/`conc` here.** The handle seam runs INLINE on the tick goroutine (the existing `serverAiStep`/`tickAI` discipline). The async pathfinding pool (`pathPool`) is ALREADY wired (Phase 8 / OPT-01) and the declared goal feeds it through the UNCHANGED `groundNavigation.requestPath` — Phase 23 adds no new async surface. `[CITED: CLAUDE.md "Do NOT introduce ants/xsync yet — no async subsystems exist to optimize"; the existing nav already uses pathPool.]`

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Thin handle (wraps entity `id` + `*TickLoop`) | A handle that wraps the live `*Entity` pointer | **REJECT.** A `*Entity` captured in a frozen Starlark value could be read AFTER the entity is removed from the store (a dangling stale pointer) and, worse, a plugin could stash the handle in a module global that another goroutine reads — a live-pointer escape the `-race` gate would catch. Re-resolving by id on the tick goroutine is the safe form (the same discipline `pathReady.applyTo` uses: carry the id, re-resolve on the owner). |
| `starlarkGoal` adapter implementing the existing `Goal` interface | A parallel "Starlark AI" tick that bypasses `goalSelector` | **REJECT.** The `goalSelector` flag-locking arbitration (MOVE/LOOK/JUMP/TARGET) is the load-bearing vanilla mechanic that stops two goals jittering the mob. A declared goal MUST go through it (declare its `flags()`), so it composes with any future ported goals. Bypassing it re-implements arbitration wrong. |
| `declare_mob` captures callables once at load | Re-exec the plugin module per spawn to "find" the AI | **REJECT — the hot-path trap.** Capture the `starlark.Callable`s ONCE; build a fresh `e.ai` per spawned mob that REFERENCES the shared frozen callables (the goal STRUCT is per-mob state, the callables are shared frozen values). |
| Custom mob reuses an existing `entity.Entity` type record for dims | A new generated type | The 776 type table is generated; you cannot add a new wire entity id without breaking the client. A custom mob MUST render as an EXISTING vanilla type id (e.g. spawn as a Pig visually) — the client only knows the 776 ids. The "custom" is the BEHAVIOR, not a new wire type. Document this clearly (Open Q §3). |

**Installation:** None — no new dep. Re-verify the pin at execution:
```bash
cd D:/ender && go list -m go.starlark.net@latest   # confirm/refresh the pseudo-version
```

## Architecture Patterns

### System Architecture Diagram

```
  LOAD TIME (host goroutine, single-threaded, before the tick loop owns state)
  ─────────────────────────────────────────────────────────────────────────────
   plugins/wandermob/main.star
     def on_wander_tick(entity, world, nav):     # the decision callback
         if not nav.has_path():
             tx, ty, tz = entity.x + rand_offset(), entity.y, entity.z + rand_offset()
             nav.path_to(tx, ty, tz)              # MUTATE through the tick-owned nav seam
     declare_mob("wanderer",
                 base_type="zombie",              # render as a Zombie (existing 776 wire id; HAS a supplier)
                 attributes={"max_health": 10.0, "movement_speed": 0.2},
                 goals=[goal(priority=6, flags=["MOVE"], tick=on_wander_tick)])
              │
              ▼  declare_mob builtin (plugin/entity OR server)
   ┌─────────────────────────────────────────────────────────────────┐
   │ captures into the registry (ONCE):                              │
   │   mobDecl{ name:"wanderer", baseType:Zombie,                    │
   │            attrs:{max_health:10, movement_speed:0.2},           │
   │            goals:[ goalDecl{prio:6, flags:MOVE,                 │
   │                             tickFn: <frozen starlark.Callable> }]│
   └─────────────────────────────────────────────────────────────────┘
  ═══════════════════ callables frozen / tick loop owns state ════════════════════
  SPAWN TIME (tick goroutine) — the newPigAI() analogue
  ─────────────────────────────────────────────────────────────────────────────
   e := NewEntity(idAlloc.AllocID(), entity.Zombie, x, y, z) # base-type dims+wire id (zombie has a supplier)
   seedAttributes(e.attributes, decl.attrs)                 # SUB-ATTRIB: SetBaseValue
   e.ai = buildAIFromDecl(decl)  # mobAI{ goals: [starlarkGoal{decl.goals[i]}], navigation }
   t.entities.add(e)             # tracker broadcasts AddEntity (as a Zombie) next tick
  ─────────────────────────────────────────────────────────────────────────────
  TICK TIME (tick goroutine — single owner, TICK-05) — the UNCHANGED AI driver
  ─────────────────────────────────────────────────────────────────────────────
   tickAI (server/tick_phases.go):
     for e in entities where e.ai != nil:  e.ai.serverAiStep(t, e)
                       │
                       ▼  serverAiStep (server/ai_mob.go) — UNCHANGED order
     goalSelector.tick(t,e)  ──► for each NOT-running goal in priority order:
                                   if canUse(t,e) && can-claim-flags: start + run
                       │            (a starlarkGoal.canUse calls its frozen canUse callable
                       │             ONLY if declared; default true)
                       ▼
     goalSelector.tickRunningGoals(t,e,true) ──► running goal.tick(t,e)
                       │                            │
                       │      ┌─────────────────────▼──────────────────────────────┐
                       │      │ starlarkGoal.tick(t,e):                             │
                       │      │   eh := newEntityHandle(t, e.id)   # thin id wrapper │
                       │      │   wh := newWorldHandle(t)                           │
                       │      │   nh := newNavHandle(t, e.id)                       │
                       │      │   th := newThread("ai:"+name); th.SetMaxSteps(B)    │
                       │      │   starlark.Call(th, decl.tickFn, (eh, wh, nh), nil) │
                       │      │     └─ plugin reads eh.x / mutates nh.path_to(...)  │
                       │      │        → routes to t.moveEntity / nav.requestPath   │
                       │      └─────────────────────────────────────────────────────┘
                       ▼
     navigation.tick(t,e) ──► follows the active A* path → t.moveEntity (swept collision)
                              (UNCHANGED — Go owns HOW; the plugin only set the target)
   tickPhysics (AFTER tickAI): gravity settles the post-move Y (UNCHANGED)
   tracker.Tick: auto-broadcasts the moved/turned mob (UNCHANGED — no new encoder)

   ✗ The interpreter runs ONLY inside a running goal's tick callback — NOT once per mob
     per tick unconditionally. A mob whose goals are all idle invokes ZERO starlark.Calls.
```

### Pattern 1: The thin handle (id wrapper, NOT a live pointer) — the core safety mechanism
**What:** A handle is a `starlark.Value` carrying ONLY an entity `id int32` (or, for the world handle, nothing but the `*TickLoop`) + the `*TickLoop`. Every read re-resolves `t.entities.get(id)`; a removed entity resolves to "gone" and the read returns a default / error instead of dereferencing a stale pointer.
**Why (three problems, one fix):**
1. **No live-pointer escape:** if the plugin stashes the handle in a module global, the global is frozen, but the handle holds no `*Entity` — only an `int32` and a `*TickLoop` whose state is only ever touched on the tick goroutine. A read from another goroutine would re-resolve the store, which IS the race the `-race` gate guards — so the discipline is: handles are only ever USED inside a `starlark.Call` that the tick goroutine makes (the tick is the sole caller).
2. **No deep-freeze of the live object:** `Freeze()` on the handle is a no-op (it wraps no mutable Starlark state). Starlark's freeze "recursively sets the frozen flag for contained values" `[CITED: Context7 /google/starlark-go impl.md]` — but the handle CONTAINS no Starlark values, so there is nothing to freeze, and the live `*Entity` is governed by TICK-05, not by Starlark's flag.
3. **No stale read:** a per-call re-resolve means a handle used after the entity died returns "entity gone," not garbage.
```go
// Source: VERIFIED against go.starlark.net Value/HasAttrs interfaces (Phase-21 surface) +
// server.entityStore.get re-resolve discipline (server/entity_store.go).
type entityHandle struct {
    t  *TickLoop // tick-owned; methods run only on the tick goroutine
    id int32     // re-resolved each access — NEVER a live *Entity
}

func (h *entityHandle) String() string        { return fmt.Sprintf("<entity %d>", h.id) }
func (h *entityHandle) Type() string          { return "entity" }
func (h *entityHandle) Freeze()                {}            // no mutable Starlark state → no-op
func (h *entityHandle) Truth() starlark.Bool   { return starlark.True }
func (h *entityHandle) Hash() (uint32, error)  { return uint32(h.id), nil }
```

### Pattern 2: READ via `HasAttrs.Attr`; MUTATE via a bound `*Builtin` method (the seam distinction)
**What:** `entity.x` / `entity.health` are READS — `Attr(name)` re-resolves the entity and returns a frozen scalar (a `starlark.Float`). `entity.move_to(x,y,z)` / `nav.path_to(x,y,z)` are MUTATES — they are bound `*starlark.Builtin` methods that route through an EXISTING tick-owned mutator. `HasSetField` (`entity.velocity = (vx,vy,vz)`) is an alternative mutate surface, but a METHOD is clearer (it can validate args + return a result); prefer methods for mutation, fields for reads.
**Why:** This is the literal "read-only vs mutate-through-a-tick-owned-seam" PLUGIN-03 requires. A read NEVER writes; a mutate NEVER touches a field directly — it calls `t.moveEntity`/`nav.requestPath`/`SetBlock`, which already maintain the bucket index, onGround, collision, dirty-tracking, and broadcast.
```go
// Source: VERIFIED — Attr/AttrNames are starlark.HasAttrs; the float reads re-resolve the store.
func (h *entityHandle) Attr(name string) (starlark.Value, error) {
    e, ok := h.t.entities.get(h.id) // re-resolve ON the tick goroutine — no stale pointer
    if !ok {
        return nil, fmt.Errorf("entity %d no longer exists", h.id)
    }
    switch name {
    case "x":      return starlark.Float(e.x), nil        // READ
    case "y":      return starlark.Float(e.y), nil        // READ
    case "z":      return starlark.Float(e.z), nil        // READ
    case "health": return starlark.Float(e.getAttributeValue(attrMaxHealthPlaceholder)), nil // READ via SUB-ATTRIB
    case "on_ground": return starlark.Bool(e.onGround), nil
    case "move_to": return h.boundMethod("move_to", h.moveTo), nil  // MUTATE method
    }
    return nil, nil // Attr returning (nil,nil) ⇒ "no such field" per the HasAttrs contract
}
func (h *entityHandle) AttrNames() []string {
    return []string{"x", "y", "z", "health", "on_ground", "move_to"}
}
```

### Pattern 3: The `starlarkGoal` adapter — a declared goal IS a `server.Goal`
**What:** A `starlarkGoal` implements the existing `Goal` interface (server/ai_goal.go: `canUse`/`canContinueToUse`/`start`/`stop`/`tick`/`requiresUpdateEveryTick`/`flags`). Each method, if the declaration supplied a callback, invokes it via `starlark.Call` passing the handles; if not, it falls back to the `baseGoal` default. The `flags()` come from the declaration's `flags=["MOVE","LOOK",...]` so the `goalSelector` arbitration works UNCHANGED.
**Why:** The declared goal slots into the EXISTING `goalSelector.tick` flag-locking arbitration with zero changes to the selector. A custom MOVE goal and a (future Phase-24) ported vanilla MOVE goal compete for the MOVE flag identically. This is what makes the API "express real vanilla AI" (Phase 24's claim) — because a Starlark goal and a Go goal are the SAME interface.
```go
// Source: VERIFIED against the Goal interface + baseGoal (server/ai_goal.go) + starlark.Call (Phase 21).
type starlarkGoal struct {
    baseGoal                       // supplies flags()/defaults; flags set from the declaration
    t        *TickLoop
    canUseFn starlark.Callable     // frozen; nil ⇒ default (always usable when its turn comes)
    tickFn   starlark.Callable     // frozen; the decision callback
    // ... startFn/stopFn/continueFn optional
}
func (g *starlarkGoal) tick(t *TickLoop, e *Entity) {
    if g.tickFn == nil {
        return
    }
    eh := &entityHandle{t: t, id: e.id}
    wh := &worldHandle{t: t}
    nh := &navHandle{t: t, id: e.id}
    th := newThread("ai-tick")
    th.SetMaxExecutionSteps(aiStepBudget) // bound a runaway callback (DoS guard, Phase-21 mechanism)
    if _, err := starlark.Call(th, g.tickFn, starlark.Tuple{eh, wh, nh}, nil); err != nil {
        log.Printf("custom mob goal tick error: %v", err) // ISOLATE: one bad callback ≠ tick kill
    }
}
```

### Pattern 4: The DECLARE-once registry → `buildAIFromDecl` at spawn (the `newPigAI` analogue)
**What:** `declare_mob` captures a `mobDecl` (name, base type, attrs, `[]goalDecl`) into a registry ONCE at load. At SPAWN, `buildAIFromDecl(decl)` constructs a fresh `*mobAI` whose `goalSelector` holds `starlarkGoal`s referencing the SHARED frozen callables (the goal STRUCT is per-mob mutable state; the callables are shared immutable frozen values). This mirrors `newPigAI()` exactly (server/ai_mob.go) — which builds a `*mobAI`, sets `navigation.speed`, and `addGoal(priority, goal)` for each goal.
**Why:** The parse/compile cost is paid once at load. Spawning the 50th wanderer allocates a `*mobAI` + N `starlarkGoal` structs (cheap Go allocs) — it does NOT re-parse the plugin or re-bind the callbacks.
```go
func buildAIFromDecl(t *TickLoop, decl *mobDecl) *mobAI {
    m := &mobAI{}
    m.navigation.speed = decl.walkSpeed // from attributes.movement_speed, scaled
    for _, gd := range decl.goals {
        m.goals.addGoal(gd.priority, &starlarkGoal{
            baseGoal: newBaseGoal(gd.flags),
            t:        t,
            tickFn:   gd.tickFn,   // SHARED frozen callable
            canUseFn: gd.canUseFn,
        })
    }
    return m
}
```

### Pattern 5: The FULL-OVERRIDE path
**What:** "Full override" is the DEGENERATE case of the declare-once model: a single declared goal at the TOP priority that claims ALL flags (MOVE|LOOK|JUMP|TARGET) and whose `tick` callback makes EVERY decision (it owns all the seams). Go still runs the mechanical work — the override callback sets a nav target via `nav.path_to`, and `groundNavigation.tick` + `moveEntity` still do the swept-collision move. The plugin owns WHAT (where to go, when to jump); Go owns HOW (the AABB sweep, the A*).
**Why:** No new machinery — it is the same `starlarkGoal` with `flags = MOVE|LOOK|JUMP|TARGET` and `priority = 0`. The arbitration naturally gives it every flag. Document it as a usage pattern, not a separate code path. (Per the no-built-but-unwired rule, do NOT build a distinct override executor — the declare-once goal IS the override when it claims all flags.)

### Pattern 6: SUB-ATTRIB integration (attributes via the existing `attribute.Map`)
**What:** A declaration's `attributes={"max_health": 10.0, "movement_speed": 0.2}` seeds the spawned entity's `attribute.Map`. The map already exists on every `*Entity` (`e.attributes`, seeded by `NewMapForEntity(typeName)` in `NewEntity`). For a base type with a supplier (e.g. `pig`), the declared values OVERRIDE the base via `e.attributes.GetInstance(name).SetBaseValue(v)` (the materialize-local-instance path, supplier.go). For a base type with NO supplier, the map is nil — the declaration must build one (or the planner restricts custom mobs to base types that have a supplier; Open Q §3).
**Why:** SUB-ATTRIB landed in v3.1; the Phase-23 job is to WIRE declarations into it, not re-model attributes. The read side (`entity.health`, `entity.attribute("movement_speed")`) is `e.attributes.GetValue(name)` / `e.getAttributeValue(...)`.
```go
// Source: VERIFIED against level/attribute/supplier.go (GetInstance/SetBaseValue) + server/entity.go (e.attributes).
func seedAttributes(m *attribute.Map, attrs map[string]float64) {
    if m == nil { return } // base type has no supplier — see Open Q §3
    for name, v := range attrs {
        if inst := m.GetInstance(name); inst != nil { // materialize the local instance
            inst.SetBaseValue(v)                       // override the supplier base
        }
    }
}
```

### Recommended Project Structure (Option A — handles in `server`)
```
server/
├── plugin_entity.go        # NEW: entityHandle / worldHandle / navHandle (starlark.Value + HasAttrs)
│                           #      bound *Builtin methods routing to moveEntity/SetBlock/nav.requestPath
├── plugin_mob_decl.go      # NEW: mobDecl/goalDecl registry + declare_mob/goal builtins (load-time capture)
├── plugin_mob_ai.go        # NEW: starlarkGoal (implements Goal) + buildAIFromDecl (the newPigAI analogue)
├── ai_goal.go              # EXISTING (unchanged): Goal interface, goalSelector arbitration
├── ai_mob.go               # EXISTING: mobAI, serverAiStep, newPigAI — REFERENCE for buildAIFromDecl
├── navigation.go           # EXISTING (unchanged): groundNavigation.requestPath/tick — the nav seam
├── physics.go              # EXISTING (unchanged): moveEntity — the move seam
├── entity_store.go         # EXISTING (unchanged): entities.get (re-resolve) / move (re-bucket)
├── tick_phases.go          # EXISTING (unchanged): tickAI drives e.ai.serverAiStep for EVERY ai mob
├── plugin_entity_test.go   # NEW: handle read/mutate tests + the wander-mob gate
└── testdata/plugins/
    └── wandermob/{plugin.toml, main.star}   # the trivial custom mob (declare one MOVE goal)
```
*(If the planner chooses Option B, the handle types move to `plugin/entity` behind a `Mob`/`World`/`Nav` interface `server` satisfies — see §Import direction.)*

### Anti-Patterns to Avoid
- **A handle wrapping a live `*Entity` pointer.** Wrap the id; re-resolve per access. (Pitfall 2.)
- **A `Freeze()` that deep-freezes the Go entity / a custom-Value Freeze that touches game state.** It must be a no-op. (Pitfall 1.)
- **`starlark.Call` per mob per tick UNCONDITIONALLY.** The interpreter runs only inside a RUNNING goal's `tick`, gated by `goalSelector` arbitration. An idle mob = zero calls. (Pitfall 3.)
- **A raw field write through a handle** (`e.x = ...`, `e.vx = ...`). Mutation routes through `t.moveEntity`/`entities.move`/`nav.requestPath`/`SetBlock` — never a bare assignment (it would skip re-bucketing, collision, onGround, dirty-tracking, broadcast). (Pitfall 5.)
- **Sharing a `starlark.Thread` across goal ticks / goroutines.** Fresh `newThread()` per `starlark.Call` (Phase-21 Pitfall 4).
- **Bypassing `goalSelector`** with a parallel Starlark AI loop. A declared goal IS a `Goal`; it goes through the existing arbitration. (Alternatives table.)
- **Inventing a new wire entity type id for a custom mob.** The 776 client only knows generated ids; a custom mob renders as an EXISTING type. (Open Q §3.)

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Exposing a Go object to Starlark with fields + methods | A string-keyed `dict` snapshot rebuilt per call | A custom `starlark.Value` + `HasAttrs` (`Attr`/`AttrNames`) + bound `*Builtin` methods | The library's documented extension path; fields lazily re-resolve, methods validate args. Snapshotting to a dict loses live-ness and allocates per access. |
| Mob goal arbitration (MOVE/LOOK/JUMP/TARGET locking, preemption) | A custom priority/conflict resolver | The EXISTING `goalSelector` (server/ai_goal.go) — declare `flags()` and add via `addGoal` | The flag-locking + `canBeReplacedBy` preemption is a verified 1:1 port of vanilla `GoalSelector`; re-implementing it diverges from vanilla and re-introduces the jitter bug it fixes. |
| Pathfinding for a declared mob | A new A* | The EXISTING `groundNavigation.requestPath` → `pathPool` → `computePath` | The A* + async pool + recompute throttle + DoS budget are all built (Phase 7/8). A declared MOVE goal just calls `nav.path_to(x,y,z)` which forwards to `requestPath`. |
| Swept collision / the AABB move | A new collision step | The EXISTING `moveEntity` (server/physics.go) | Per-axis swept collision + onGround + `entities.move` re-bucket are all in `moveEntity`. A handle mutator forwards to it. |
| Per-entity attributes (health/speed) | A new attribute holder | The EXISTING `attribute.Map` (SUB-ATTRIB, level/attribute) | The fold (ADD_VALUE→ADD_MULTIPLIED_BASE→ADD_MULTIPLIED_TOTAL→sanitize) is a verified 1:1 port; declared attrs seed it via `GetInstance.SetBaseValue`. |
| Re-resolving a live entity safely from a stored id | A weak-pointer / finalizer scheme | `t.entities.get(id)` on the tick goroutine (the `pathReady.applyTo` pattern) | The store IS the registry; an id re-resolves to the live entity or "gone" — exactly the discipline the async rejoin already uses. |
| Sandboxing a runaway callback | A watchdog goroutine | `th.SetMaxExecutionSteps(budget)` per call thread | Phase-21-verified deterministic step counter; clean `*EvalError`; no timer race. |

**Key insight:** Phase 23 writes almost NO algorithmic gameplay code. It writes glue: three `starlark.Value` handle types, a `declare_mob` builtin that captures a declaration, a `starlarkGoal` adapter that implements the existing `Goal` interface, and a `buildAIFromDecl` that mirrors `newPigAI`. Everything mechanical (arbitration, A*, collision, attributes, the tick driver) ALREADY EXISTS and stays unchanged. The risk is NOT writing AI — it's (a) the handle wrapping an id not a pointer, (b) every mutate routing through an existing tick-owned mutator, and (c) the interpreter firing only inside a running goal, never unconditionally per mob per tick.

## Common Pitfalls

### Pitfall 1: Deep-freezing the live Go entity
**What goes wrong:** A naive `entityHandle.Freeze()` tries to mark the entity immutable, or the handle stores Starlark values built from the entity that get recursively frozen at module completion. Either way, the tick goroutine can no longer mutate the entity (or the frozen flag is meaningless but the author thinks it protects something).
**Why it happens:** Starlark's freeze "recursively sets the frozen flag for contained values" `[CITED: Context7 /google/starlark-go impl.md]`. An author assumes the handle must "freeze the entity" to be safe.
**How to avoid:** `Freeze()` is a NO-OP. The handle holds an `int32` id and a `*TickLoop` — no mutable Starlark state to freeze, and the live `*Entity` is governed by TICK-05 (the tick is its sole mutator), not by Starlark's flag. Safety comes from the id re-resolve + tick-ownership, NOT from freezing.
**Warning signs:** A `Freeze()` body that does anything; a handle field of type `starlark.Value` built from entity data.

### Pitfall 2: A live `*Entity` pointer escapes the tick goroutine
**What goes wrong:** The handle wraps `e *Entity`. A plugin stashes the handle in a module global (`g_last_mob = entity`). Later, ANOTHER goroutine (a future Folia region thread, or even a test reader) dereferences the stashed handle's `*Entity` while the tick goroutine mutates it — a data race the `-race` gate flags, or a read of a stale entity that was removed from the store.
**Why it happens:** It's the obvious design ("just wrap the entity"). The escape is invisible until `-race` runs or the entity dies.
**How to avoid:** Wrap the `id int32`, never the pointer. Every access re-resolves `t.entities.get(id)` on the tick goroutine; a stashed handle re-resolved on another goroutine would touch the tick-owned store — which is the race the gate catches, so the DISCIPLINE is: handles are only ever used inside the tick's `starlark.Call`. The id re-resolve also makes a stashed-then-entity-died handle return "gone" instead of garbage. This is the same id-carry / owner-re-resolve discipline `pathReady.applyTo` and `spawnCandidatesReady.applyTo` already use (server/async.go).
**Warning signs:** A handle struct field of type `*Entity`; `-race` flagging `Entity.x`/`Entity.vx` access; a test that retains a handle across ticks and reads it off-thread.

### Pitfall 3: The per-mob-per-tick interpreter trap
**What goes wrong:** Calling `starlark.Call` for every AI mob every tick — e.g. a "tick the plugin AI" call placed directly in `tickAI`'s per-mob loop, or a goal whose `tick` runs unconditionally — turns 200 custom mobs × 20 TPS into 4000 `starlark.Call`/sec, the exact pattern v4-PLAN + REQUIREMENTS forbid.
**Why it happens:** It's the most "obvious" place ("run the plugin AI for each mob"). But `tickAI` already runs O(mobs); adding an unconditional interpreter call there multiplies it by 20 TPS.
**How to avoid:** The interpreter runs ONLY inside a RUNNING goal's `tick` callback, and a goal runs only when `goalSelector` arbitration starts it (it must `canUse` AND claim its flags). An idle mob (no goal running, or a goal whose `tick` callback is nil and runs the Go default) invokes ZERO `starlark.Call`s. A wander goal's `tick` fires only while it holds MOVE — and even then, a well-written callback early-returns when a path is already active (`if not nav.has_path()`), so the heavy decision runs only on (re)target, not every tick. Add a test asserting an idle declared mob makes 0 interpreter calls per tick, and an active one makes ≤1.
**Warning signs:** A profiler showing `starlark.Call` time scaling with mob count × tick rate; an `Emit`/`Call` inside `tickAI`/`tickPhysics`/`tickEntities` per-entity loops.

### Pitfall 4: A handle read after the entity is removed (stale read)
**What goes wrong:** A mob dies / despawns mid-tick (removed from the store). A goal callback still holding a handle reads `entity.x` and dereferences a removed entity.
**Why it happens:** The entity can be removed by another seam (a future death path) within the same tick the goal runs.
**How to avoid:** `Attr` re-resolves `t.entities.get(id)` and returns a Starlark error ("entity N no longer exists") on a miss, never a nil deref. The callback sees a clean dynamic error (isolated by the per-call error handling), not a crash.
**Warning signs:** A nil-pointer panic in a goal callback; an `Attr` that caches the resolved `*Entity` instead of re-resolving.

### Pitfall 5: A raw field write skips the tick-owned invariants
**What goes wrong:** A mutate handle does `e.x = newX` or `e.vx = v` directly. This skips `entities.move`'s re-bucket (the tracker's `near()` goes stale), skips swept collision (the mob clips into walls), skips onGround, and skips the dirty-tracking/broadcast.
**Why it happens:** A direct field write is the simplest-looking mutation.
**How to avoid:** EVERY entity-position mutate routes through `t.moveEntity(e, dx, dy, dz)` (swept collision + re-bucket + onGround) or, for a nav target, `nav.requestPath` (which the move follows). A velocity set is `e.vx = v` ONLY if the planner decides velocity is a first-class mutate (it's tick-owned and the next `tickPhysics` integrates it via `moveEntity`, so a velocity set is acceptable — but a POSITION set must go through `moveEntity`). A world block set routes through `ChunkManager.SetBlock` + `broadcastBlockUpdate` (so neighbors/dirty/clients all fire). Map this explicitly in the read-vs-mutate table.
**Warning signs:** A handle method assigning `e.x`/`e.y`/`e.z`; a custom mob clipping through walls; the tracker not seeing a moved mob.

### Pitfall 6: `-race` needs CGO=1, ship binary needs CGO=0 (carried from Phase 21)
**What goes wrong:** `CGO_ENABLED=0 go test -race ./...` fails (`-race requires cgo`).
**How to avoid:** Two gates — ship build `CGO_ENABLED=0 go build ./...`; race test `CGO_ENABLED=1 go test -race ./server/`. Already wired in `.github/workflows/go.yml`. `[VERIFIED: Phase-21 Pitfall 3]`

## Code Examples

### A bound-method helper for mutate seams (the `entity.move_to` form)
```go
// Source: VERIFIED against starlark.NewBuiltin + BindReceiver (Phase-21 builtin surface) and
//         server.moveEntity (server/physics.go).
// A handle method is a *starlark.Builtin whose receiver is the handle; it routes to a tick mutator.
func (h *entityHandle) boundMethod(name string, fn func(*starlark.Thread, *starlark.Builtin,
    starlark.Tuple, []starlark.Tuple) (starlark.Value, error)) starlark.Value {
    return starlark.NewBuiltin(name, fn).BindReceiver(h)
}

// move_to(x, y, z): MUTATE through the tick-owned moveEntity (NOT a raw field write).
func (h *entityHandle) moveTo(th *starlark.Thread, b *starlark.Builtin,
    args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
    var x, y, z float64
    if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &y, &z); err != nil {
        return nil, err
    }
    e, ok := h.t.entities.get(h.id) // re-resolve ON the tick goroutine
    if !ok {
        return nil, fmt.Errorf("entity %d no longer exists", h.id)
    }
    // Route through the EXISTING swept-collision mutator: per-axis clip + entities.move re-bucket
    // + onGround. A raw e.x=… would skip all of that (Pitfall 5).
    h.t.moveEntity(e, x-e.x, y-e.y, z-e.z)
    return starlark.None, nil
}
```

### The nav handle's `path_to` (the wander gate's one mutate seam)
```go
// Source: VERIFIED against groundNavigation.requestPath/shouldRecomputePath (server/navigation.go).
type navHandle struct {
    t  *TickLoop
    id int32
}
func (h *navHandle) Attr(name string) (starlark.Value, error) {
    e, ok := h.t.entities.get(h.id)
    if !ok || e.ai == nil {
        return nil, fmt.Errorf("entity %d has no nav", h.id)
    }
    switch name {
    case "has_path":
        // READ: a path is active iff there is a current target (server/ai_mob.go hasTarget).
        return h.boundMethod("has_path", func(_ *starlark.Thread, _ *starlark.Builtin,
            _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
            return starlark.Bool(e.ai.hasTarget), nil
        }), nil
    case "path_to":
        return h.boundMethod("path_to", h.pathTo), nil // MUTATE
    }
    return nil, nil
}
// path_to(x,y,z): MUTATE through the existing nav seam — set the want-target; serverAiStep's
// requestPath (already wired, async via pathPool) computes the A* path; navigation.tick follows it
// via moveEntity. The plugin sets WHAT (target); Go owns HOW (A* + swept collision).
func (h *navHandle) pathTo(th *starlark.Thread, b *starlark.Builtin,
    args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
    var x, y, z float64
    if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 3, &x, &y, &z); err != nil {
        return nil, err
    }
    e, ok := h.t.entities.get(h.id)
    if !ok || e.ai == nil {
        return nil, fmt.Errorf("entity %d has no nav", h.id)
    }
    e.ai.setWantTarget(x, y, z) // server/ai_mob.go: the navigation.moveTo analogue the nav consumes
    return starlark.None, nil
}
```

### The `declare_mob` builtin (capture ONCE at load)
```go
// Source: VERIFIED against starlark.NewBuiltin/UnpackArgs (Phase-21) + the registry-capture pattern (Phase-22).
func (r *mobRegistry) declareMobBuiltin() *starlark.Builtin {
    return starlark.NewBuiltin("declare_mob", func(th *starlark.Thread, b *starlark.Builtin,
        args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
        var name, baseType string
        var attrs *starlark.Dict
        var goals *starlark.List
        if err := starlark.UnpackArgs(b.Name(), args, kwargs,
            "name", &name, "base_type", &baseType, "attributes?", &attrs, "goals?", &goals); err != nil {
            return nil, err
        }
        decl, err := buildMobDecl(name, baseType, attrs, goals) // parse attrs map + each goal()
        if err != nil {
            return nil, err // reject a bad declaration LOUDLY at load (e.g. unknown base_type / flag)
        }
        r.byName[name] = decl // captured ONCE; spawn reads it via buildAIFromDecl
        return starlark.None, nil
    })
}
```

### The trivial wander mob (`testdata/plugins/wandermob/main.star`) — the GATE
```python
# A NON-vanilla custom mob: declare ONE MOVE goal whose tick picks a new random nav target
# when the mob has no active path. Proves declare-once + the nav mutate seam + a moving mob.
def on_wander_tick(entity, world, nav):
    if not nav.has_path():                         # READ — only act on (re)target, not every tick
        ox = entity.x + (rand() * 16.0 - 8.0)      # READ entity.x
        oz = entity.z + (rand() * 16.0 - 8.0)
        nav.path_to(ox, entity.y, oz)              # MUTATE through the tick-owned nav seam

declare_mob(
    name = "wanderer",
    base_type = "zombie",                          # renders as a Zombie (existing 776 wire id; HAS an attribute supplier)
    attributes = {"max_health": 10.0, "movement_speed": 0.2},
    goals = [goal(priority = 6, flags = ["MOVE"], tick = on_wander_tick)],
)
```

### Spawning a declared mob on the tick (the `newPigAI` analogue, server-side)
```go
// Source: VERIFIED against spawnCandidatesReady.applyTo (server/async.go) + NewEntity (server/entity.go).
func (t *TickLoop) spawnDeclaredMob(decl *mobDecl, x, y, z float64) *Entity {
    rec := decl.baseTypeRecord                       // e.g. entity.Pig — existing dims + wire id
    e := NewEntity(t.idAlloc.AllocID(), rec, x, y, z)
    seedAttributes(e.attributes, decl.attrs)         // SUB-ATTRIB: override the supplier base
    e.ai = buildAIFromDecl(t, decl)                  // the mobAI with starlarkGoal(s) (the newPigAI analogue)
    t.entities.add(e)                                // tracker broadcasts AddEntity (as the base type) next tick
    return e
}
```

## Runtime State Inventory

> Phase 23 is greenfield API code (new handle/declaration types + new tests) plus additive wiring at existing seams. It is NOT a rename/refactor/migration. No stored data, live-service config, OS-registered state, secrets, or build artifacts carry an old string that needs migration.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — declared mobs are NOT persisted in Phase 23 (no entity-AI NBT codec; a custom mob is re-declared at plugin load each run). | None — verified by the absence of an AI-persistence path (entity attribute persistence is itself a CITED-TODO stub, instance.go `permanent`). |
| Live service config | None — no external service. | None. |
| OS-registered state | None. | None. |
| Secrets/env vars | None new. | None. |
| Build artifacts | None — pure Go source + a new `testdata/plugins/` dir. | None. |

**Note (forward-compat, not Phase 23 work):** if a declared mob is ever PERSISTED (survives restart), the plugin that declared it must be loaded before the world loads the mob — a load-order concern for a FUTURE persistence phase, explicitly NOT in Phase 23 (custom mobs are re-declared at plugin load, ephemeral).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `go.starlark.net` | PLUGIN-03 (via Phase 21) | ✓ (in go.mod) | `v0.0.0-20260613233743-8ba36ccb83fb` | — |
| `plugin/starlark` package (Phase 21) | the handle runtime + `starlark.Call` | ✓ once Phase 21 executed | — | **BLOCKING if Phase 21 not done.** |
| `plugin/host` event bus (Phase 22) | the plugin LOAD lifecycle that runs `declare_mob` at load | ✓ once Phase 22 executed | — | **BLOCKING if Phase 22 not done** — `declare_mob` is a builtin injected at load by the host. |
| `level/attribute` (SUB-ATTRIB) | declared-mob attributes | ✓ (landed v3.1) | in-repo | — |
| `server` AI subsystem (`Goal`, `goalSelector`, `mobAI`, `groundNavigation`, `moveEntity`, `entityStore`) | the declare-once adapter + the mutate seams | ✓ (Phases 6/7/8 landed) | in-repo | — |
| Go toolchain ≥ 1.25 | starlark floor | ✓ | project go 1.25.0 / 1.26.1 | — |
| C compiler (for `-race` only) | race-test gate | ✓ in CI | — | n/a — `-race` runs CGO=1 in CI; ship binary stays CGO=0 |

**Missing dependencies with no fallback:** Phases 21 + 22 must be executed first — Phase 23 hangs `declare_mob` on Phase 22's load lifecycle and calls Phase 21's `starlark.Call` / handle-value machinery.
**Missing dependencies with fallback:** None — Phase 23 adds only stdlib + existing Sulfur types.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `sync` for the race proof) |
| Config file | none (Go convention) |
| Quick run command | `go test ./server/ -run TestDeclared` |
| Full suite (race) command | `CGO_ENABLED=1 go test -race ./server/` |
| Ship-build gate | `CGO_ENABLED=0 go build ./...` |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| PLUGIN-03 | `declare_mob(...)` captures a declaration ONCE at load into the registry | unit | `go test ./server/ -run TestDeclareMobCaptures` | ❌ Wave 0 |
| PLUGIN-03 | `entityHandle.Attr("x")` re-resolves the live entity and returns its position (READ) | unit | `go test ./server/ -run TestEntityHandleRead` | ❌ Wave 0 |
| PLUGIN-03 | `entityHandle.Attr("x")` on a REMOVED entity returns a clean error, not a panic (Pitfall 4) | unit | `go test ./server/ -run TestEntityHandleStale` | ❌ Wave 0 |
| PLUGIN-03 | `entity.move_to` / `nav.path_to` route through `moveEntity` / `setWantTarget` (MUTATE), never a raw field write | unit | `go test ./server/ -run TestHandleMutateSeam` | ❌ Wave 0 |
| PLUGIN-03 | `entityHandle.Freeze()` is a no-op and the entity stays tick-mutable after a frozen handle exists (Pitfall 1) | unit | `go test ./server/ -run TestHandleFreezeNoop` | ❌ Wave 0 |
| PLUGIN-03 | A `starlarkGoal` slots into `goalSelector`: it claims its declared flag, arbitrates with another goal, ticks only while running | unit | `go test ./server/ -run TestStarlarkGoalArbitration` | ❌ Wave 0 |
| PLUGIN-03 | SUB-ATTRIB: a declared `max_health`/`movement_speed` seeds the spawned mob's `attribute.Map` (GetValue returns the override) | unit | `go test ./server/ -run TestDeclaredAttributes` | ❌ Wave 0 |
| PLUGIN-03 | **THE GATE:** a declared "wanderer" spawns, ticks via `tickAI`→`serverAiStep`, and MOVES (its x/z change over N ticks via the Go nav) | integration | `go test ./server/ -run TestWanderMobMoves` | ❌ Wave 0 |
| PLUGIN-03 | The interpreter fires ≤1×/tick for an ACTIVE custom mob and 0×/tick for an idle one (NOT per-mob-per-tick) (Pitfall 3) | unit | `go test ./server/ -run TestNoPerTickInterpreterStorm` | ❌ Wave 0 |
| PLUGIN-03 | A declared mob ticking (handle read+mutate on the tick goroutine) is race-clean | race | `CGO_ENABLED=1 go test -race ./server/ -run TestWanderMobRace` | ❌ Wave 0 |
| PLUGIN-03 | One erroring goal callback is isolated — the tick survives, other mobs still tick | unit | `go test ./server/ -run TestGoalCallbackIsolation` | ❌ Wave 0 |
| PLUGIN-03 | Default binary is CGO=0 (no new cgo dep) | build | `CGO_ENABLED=0 go build ./...` | ✓ (gate exists) |

**The signature test** (proves the architecture, not just the plumbing): `TestWanderMobMoves` — declare a wander mob, spawn it at a known position over a flat floor, run `tickOnce` N times (which drives `tickAI`→`serverAiStep`→the `starlarkGoal.tick`→`nav.path_to`→`requestPath`→`navigation.tick`→`moveEntity`), and assert the mob's (x,z) changed by a plausible walk distance — proving a Starlark-declared mob moves through the REAL Go nav, end-to-end, with the interpreter touched only at the decision seam.

### Sampling Rate
- **Per task commit:** `go test ./server/ -run TestDeclared -run TestEntityHandle -run TestStarlarkGoal`
- **Per wave merge / phase gate:** `CGO_ENABLED=1 go test -race ./server/` AND `CGO_ENABLED=0 go build ./...`
- **Phase gate (full):** the Docker `-race` image (CGO=1) green + the CGO=0 static build green — both already in `.github/workflows/go.yml`; the new handle/declaration code lives in `server/` so `./...` picks it up.

### Wave 0 Gaps
- [ ] `server/testdata/plugins/wandermob/{plugin.toml, main.star}` — the trivial declared custom mob (one MOVE goal).
- [ ] `server/testdata/plugins/badgoal/{plugin.toml, main.star}` — a goal whose `tick` callback errors (isolation test).
- [ ] `server/plugin_entity_test.go` — handle read/mutate/stale/freeze tests.
- [ ] `server/plugin_mob_ai_test.go` — `starlarkGoal` arbitration + `buildAIFromDecl`.
- [ ] `server/plugin_mob_decl_test.go` — `declare_mob`/`goal` capture + declared attributes.
- [ ] `server/<gate>_test.go` — `TestWanderMobMoves` (the end-to-end gate, driven through `tickOnce`/`tickAI`) + the `-race` variant + the no-interpreter-storm assertion.

*(No framework install needed — Go stdlib `testing`. The race gate already exists in CI.)*

## Security Domain

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V5 Input Validation / untrusted-code execution | yes | Phase-21 sandbox carries: each goal-tick thread gets `SetMaxExecutionSteps` (a runaway callback is bounded, not a hang). `declare_mob`/`goal` validate the base_type, flag names, and attribute names at LOAD (reject a typo loudly). A handle mutate validates its args (`UnpackPositionalArgs`) and re-resolves the entity (a removed-entity mutate errors cleanly). |
| V1.4 Trust boundaries / least privilege | partial (the capability model lands HERE) | Phase 22 recorded an OPTIONAL manifest `capabilities` field (parsed, not enforced). Phase 23 is the FIRST phase exposing live state (entity/world handles), so this is where capability ENFORCEMENT should begin — gate `world.set_block`/`entity.move_to` on a declared capability. Whether to enforce in Phase 23 or defer is Open Q §4. |
| V6 Cryptography | no | None in this phase. |
| V2/V3/V4 Auth/Session/Access | no | No network/auth surface; plugins are operator-installed local files. |

### Known Threat Patterns for the entity/world handle bridge
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| A goal callback with an infinite loop hangs the tick | Denial of Service | `th.SetMaxExecutionSteps(budget)` per goal-tick thread → `*EvalError`, tick continues. (Phase-21 verified.) |
| A goal callback panics/errors and kills the tick goroutine (disconnects everyone) | Denial of Service | Per-callback error+panic isolation in `starlarkGoal.tick` (log + continue) + the existing `tickOnce` recover backstop (server/tick_phases.go). |
| A live `*Entity` pointer escapes to another goroutine via a stashed handle | Tampering / data race | Wrap the `id`, not the pointer; re-resolve on the tick goroutine. The `-race` gate covers a declared mob ticking. (Pitfall 2.) |
| A handle mutate writes a raw field, corrupting the bucket index / clipping a wall | Tampering | Every mutate routes through `moveEntity`/`entities.move`/`SetBlock`/`setWantTarget` — never a bare assignment. (Pitfall 5.) |
| `world.set_block` at an arbitrary coordinate (grief / out-of-bounds) | Tampering | `ChunkManager.SetBlock` already rejects an unloaded column / out-of-range y (server-authoritative, no panic). Capability gating (Open Q §4) restricts WHICH plugins may set blocks at all. |
| A declared attribute outside the valid envelope (e.g. negative max_health) | Tampering | `attribute.Map`/`RangedAttribute.sanitizeValue` clamps to [min,max] (the verified 1:1 fold) — a bad declared value is clamped, not honored. |
| Spawning unbounded custom mobs (mob-flood DoS) | Denial of Service | The existing `naturalSpawner` CREATURE cap (server/spawner.go) bounds natural spawns; a plugin-triggered spawn path (if added) must respect a cap. (The Phase-23 gate spawns via a test/debug seam; a plugin-facing `spawn` builtin's rate-limit is Open Q §6.) |

## Open Questions

> These are the v4-PLAN/REQUIREMENTS "decide at Phase 23 kickoff" items. SURFACE for the operator/discuss-phase — do NOT decide in the plan. §1 is v4-PLAN's named "hardest design question."

1. **The exact frozen-handle API surface (THE hardest question — v4-PLAN).** Which entity/world/nav operations does a plugin get, and which are read vs mutate?
   - What we know: the MINIMUM the gate needs is `entity.{x,y,z}` (read), `nav.has_path()` (read), `nav.path_to(x,y,z)` (mutate). The mechanism (Attr-read / Builtin-mutate) is settled.
   - What's unclear: the FULL set. Candidate READS: `entity.{x,y,z,yaw,pitch,health,on_ground,type,age}`, `world.block_at(x,y,z)`, `world.is_solid(x,y,z)`, nearest-player. Candidate MUTATES: `entity.move_to`, `entity.set_velocity`, `entity.set_yaw`, `nav.path_to`, `nav.stop`, `world.set_block`, `entity.hurt`/`entity.heal`, `entity.set_attribute`.
   - Recommendation: LOCK the gate-minimal set for Phase 23; ENUMERATE the full candidate set and let the operator pick the Phase-23 scope vs Phase-24 deferral (the vanilla-mob dogfood will reveal what's actually needed). Map EACH chosen op as read-or-mutate-seam in CONTEXT.md.

2. **`entity.set_velocity` — is a velocity set a first-class mutate, or must everything go through `move_to`?**
   - What we know: `e.vx/vy/vz` are tick-owned plain fields; the next `tickPhysics` integrates them via `moveEntity` (so a velocity set is safe — it's consumed by the existing collision step). A POSITION set MUST go through `moveEntity` (Pitfall 5).
   - Recommendation: allow `set_velocity` (it's tick-owned + collision-integrated next tick) but forbid a raw position write; the planner documents the distinction.

3. **Custom-mob base type: reuse an existing 776 type, or supply width/height?**
   - What we know: the client only knows generated wire ids; a custom mob must RENDER as an existing type (e.g. `base_type="zombie"`). The base type also supplies AABB dims + the attribute supplier. VERIFIED: the `level/attribute/defaults.go` `suppliers` map registers ONLY `player`/`witch`/`cat`/`villager`/`zombie`/`silverfish` — NOT `pig`. So a `pig` base type yields a NIL `attribute.Map` and `seedAttributes` would silently no-op. The gate must use a supplier-backed base type (zombie/cat/witch/villager/silverfish).
   - What's unclear: must a custom mob name an existing type (simplest, and gives it an attribute supplier), or may a declaration supply raw width/height + render as a chosen type? And what of a base type with NO attribute supplier (a nil `attribute.Map`)?
   - Recommendation: Phase 23 requires `base_type` to name an existing type WITH a registered supplier (zombie/cat/witch/villager/silverfish — verified set) so `seedAttributes` has a map to override; the gate uses `zombie`. A `base_type` with no supplier must either be rejected at `declare_mob` (loud load error) or `seedAttributes` must build a fresh map. Defer arbitrary-dims/no-supplier custom mobs.

4. **Capability ENFORCEMENT — start in Phase 23, or defer?**
   - What we know: Phase 22 recorded an optional `capabilities` manifest field (parsed, not enforced). Phase 23 is the first to expose live state, so it's the natural enforcement point (gate `world.set_block`/`entity.hurt` on a declared capability).
   - What's unclear: enforce now, or treat all loaded plugins as trusted (operator-installed) for v4 and defer enforcement?
   - Recommendation: surface to the operator. Default for v4 (operator-installed plugins) = parse-but-don't-enforce, consistent with Phase 22; revisit if third-party plugins become a goal.

5. **Does Phase 23 expose a TARGET-flag (attack-target) goal seam, or only MOVE/LOOK?**
   - What we know: `serverAiStep` (server/ai_mob.go) SKIPS `targetSelector` for the passive Pig. The gate mob needs only MOVE. A combat mob needs TARGET (target acquisition) + a second `goalSelector` (the `targetSelector`).
   - What's unclear: build the TARGET seam now (so Phase 24's vanilla zombie can be a plugin) or defer the `targetSelector` wiring to Phase 24.
   - Recommendation: Phase 23 exposes MOVE/LOOK/JUMP flags (the single-selector path the gate needs); add the `targetSelector`/TARGET seam in Phase 24 when the first attack mob is dogfooded (don't build the second selector with no consumer — no-built-but-unwired).

6. **A plugin-facing `spawn(mob_name, x,y,z)` builtin — in Phase 23 or later?**
   - What we know: the gate can spawn the wanderer via a test/debug seam (the `spawnDeclaredMob` server helper). A plugin-facing spawn builtin is a separate, rate-limit-sensitive surface (mob-flood DoS).
   - Recommendation: Phase 23 builds `spawnDeclaredMob` (server-side, for the gate) and an OPTIONAL minimal `spawn` builtin gated behind a cap; the full event-driven spawn (a plugin spawning on a custom condition) can ride the Phase-22 event bus. Operator decides the Phase-23 scope.

7. **Import direction: handles in `server` (Option A) or a `plugin/entity` interface (Option B)?** (Architecture §Import direction) — Recommendation A for Phase 23 (the AI types are already `server` types). Operator/planner confirms.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Phase-22 hooks pass FROZEN SCALARS only (ints/strings/floats) | Phase-23 passes FROZEN HANDLES (custom `starlark.Value` wrapping an entity id + `*TickLoop`) | This phase | The handle is the read/mutate-seam bridge; it extends the frozen-value model from scalars to live-state accessors. |
| A mob's AI is a hard-coded Go `*mobAI` (`newPigAI`) | A mob's AI can be DECLARED in Starlark and built at spawn (`buildAIFromDecl`), composing with Go goals via the shared `Goal` interface | This phase | A Starlark goal and a Go goal are the SAME interface → the API can express vanilla AI (Phase 24's claim). |
| Entity state exposed (if at all) by copying a value | Entity state exposed by a thin id-handle re-resolved per access | This phase | No live-pointer escape; no stale read; no deep-freeze of the Go object. |

**Deprecated/outdated for our use:**
- Wrapping a live `*Entity` in a Starlark value (the obvious-but-wrong design) — replaced by the id-handle.
- A separate "plugin AI loop" — replaced by the `starlarkGoal`-as-`Goal` adapter that reuses `goalSelector`.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `starlark.NewBuiltin(...).BindReceiver(h)` is the current way to bind a handle as a method's receiver (so `entity.move_to` carries `h`). This is the standard go.starlark.net method pattern; Phase 21 used `NewBuiltin` for free functions but did not bind a receiver. | Code Examples §boundMethod | Low — if `BindReceiver` is named differently in the pinned version, the alternative is to capture `h` in the builtin closure (no `BindReceiver` needed). Verify against the pinned API at planning. |
| A2 | RESOLVED (not an assumption): `level/attribute/defaults.go` `suppliers` map registers EXACTLY `player`/`witch`/`cat`/`villager`/`zombie`/`silverfish` (read in full this session). **`pig` is NOT registered** — so the gate's `base_type` is `zombie` (a confirmed-supplier type), NOT `pig`; a `pig` base type would give a nil `attribute.Map` and `seedAttributes` would no-op. `seedAttributes` must nil-guard regardless (it already does in the example). | Architecture §Attributes / Open Q §3 | None — verified. The gate uses `zombie`; the planner must keep `base_type` within the verified supplier set or build a map for an unsupplied type. |
| A3 | The exact line numbers and the assumption that `tickAI` drives `serverAiStep` for EVERY `e.ai != nil` entity unchanged (so a declared mob's `*mobAI` is driven with no `tickAI` edit). Verified by reading `tickAI` this session — it snapshots `e.ai != nil` and calls `serverAiStep`. The names are the stable anchors; re-grep at execution. | Architecture / Diagram | Low — verified this session; names stable. |
| A4 | Whether velocity (`set_velocity`) is exposed is an operator decision (Open Q §2); the research recommends allowing it but the planner must not build it unless locked. | Open Q §2 | Low — surfaced, not decided. |

## Sources

### Primary (HIGH confidence)
- **`D:/ender/server/entity.go`** (read in full) — the live `Entity` struct: id/typ/x,y,z/vx,vy,vz/yaw/onGround/width/height/`ai *mobAI`/`attributes *attribute.Map`; `NewEntity` (copies base-type dims + `NewMapForEntity`); `getAttributeValue`; `AABB`. The handle wraps `e.id`; reads re-resolve here. `[VERIFIED]`
- **`D:/ender/server/entity_store.go`** (read in full) — `entityStore.get(id)` (the re-resolve), `move` (the re-bucket mutator), `add`, `near`. The id-re-resolve discipline. `[VERIFIED]`
- **`D:/ender/server/ai_goal.go`** (read in full) — the `Goal` interface (canUse/canContinueToUse/start/stop/tick/flags), `baseGoal`, `goalSelector` flag-locking arbitration (MOVE/LOOK/JUMP/TARGET, `canBeReplacedBy` preemption). A `starlarkGoal` implements this interface. `[VERIFIED]`
- **`D:/ender/server/ai_mob.go`** (read in full) — `mobAI` (goalSelector + groundNavigation + wantTarget), `serverAiStep` (the tick order), `newPigAI` (the build-an-AI analogue), `setWantTarget`/`clearWantTarget` (the nav mutate seam). `[VERIFIED]`
- **`D:/ender/server/ai_goals_passive.go`** (read in full) — the concrete Pig goals (randomStroll/lookAtPlayer/randomLookAround) showing a goal SETS a target via `setWantTarget`, never moves the mob — the pattern a `starlarkGoal` mirrors. `[VERIFIED]`
- **`D:/ender/server/navigation.go`** (read in full) — `groundNavigation.requestPath` (the A* seam, async via pathPool), `shouldRecomputePath`, `tick` (follows the path via `moveEntity`). The `nav.path_to` mutate forwards to `setWantTarget`→`requestPath`. `[VERIFIED]`
- **`D:/ender/server/physics.go`** (read `moveEntity`, `blockSolidAt`, `entityBoxAt`) — `moveEntity` (per-axis swept collision + `entities.move` re-bucket + onGround) is THE position mutate seam; `blockSolidAt`→`ChunkManager.GetBlock` is the world read. `[VERIFIED]`
- **`D:/ender/server/tick_phases.go`** (read in full) — `tickAI` (snapshots `e.ai != nil` + calls `serverAiStep` for each — the UNCHANGED driver a declared mob rides), the per-entity anti-seams (`tickEntities`/`tickAI`/`tickPhysics`), `tickOnce` order + the panic-recover backstop. `[VERIFIED]`
- **`D:/ender/server/spawner.go`** + **`server/async.go`** (`naturalSpawn`, `spawnCandidatesReady.applyTo`) — the spawn-then-attach-AI path (`NewEntity` + `pig.ai = newPigAI()` + `entities.add`), the model for `spawnDeclaredMob`; the CREATURE cap (mob-flood guard); the id-carry/owner-re-resolve async discipline. `[VERIFIED]`
- **`D:/ender/server/attributes.go`** + **`level/attribute/{attribute,instance,supplier,defaults}.go`** (read in full) — SUB-ATTRIB: `attribute.Map` (per-entity), `GetValue`, `GetInstance.SetBaseValue` (the declared-attr seed), `NewMapForEntity`, the verified fold + `RangedAttribute.sanitizeValue` clamp. `[VERIFIED]`
- **`D:/ender/world/manager.go`** (`GetBlock`/`SetBlock`/`SetBlockEntityAt` + the dirty/reject discipline) + **`server/block_interact.go`** (`reconcileEdit`/`broadcastBlockUpdate`) — the world read (`block_at`) + the world mutate seam (`set_block` must route through `SetBlock`+`broadcastBlockUpdate`). `[VERIFIED]`
- **`.planning/phases/21-starlark-runtime-foundation/21-RESEARCH.md`** + **`22-RESEARCH.md`/`22-CONTEXT.md`** — the Phase-21 frozen/one-thread-per-goroutine/`starlark.Call`/`NewBuiltin`/`SetMaxExecutionSteps`/CGO=0 surface; the Phase-22 register-capture pattern, the discrete-seam discipline, the frozen-SCALARS-only-in-22 boundary that 23 extends to handles. `[VERIFIED]`
- **`.planning/v4-PLAN.md`** + **`.planning/REQUIREMENTS.md`** — PLUGIN-03 scope, the DECLARE-once + full-override architecture, "the frozen-handle API surface … the hardest design question," the read-vs-mutate-seam requirement, TICK-05/-race/CGO=0 carries. `[CITED]`
- **Context7 `/google/starlark-go`** — the freezing model ("Freeze recursively sets the frozen flag for contained values"; "mutable values frozen on module completion … safely referenced by multiple threads"); `HasAttrs`/dot-expression/bound-method/`getattr`/`hasattr` semantics for custom application Value types. `[CITED]`
- **`go list -m go.starlark.net@latest` + grep `go.mod`/`go.sum`** → `v0.0.0-20260613233743-8ba36ccb83fb`. `[VERIFIED 2026-06-27]`

### Secondary (MEDIUM confidence)
- The exact `BindReceiver` spelling for binding a handle as a method receiver (A1) — standard go.starlark.net pattern; verify against the pinned version at planning.

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Standard stack (no new dep; handles via starlark custom-Value interfaces; SUB-ATTRIB reuse): **HIGH** — same dep as 21/22; the attribute/AI/nav types were all read this session.
- Architecture (thin-id handle, read-vs-mutate seam table, `starlarkGoal`-as-`Goal`, declare-once registry, full-override-as-all-flags): **HIGH** — every Sulfur seam (`Goal`, `goalSelector`, `serverAiStep`, `tickAI`, `moveEntity`, `entities.get/move`, `groundNavigation`, `attribute.Map`, `SetBlock`) was read by name in the actual source; the freezing model is Context7-verified.
- Pitfalls (deep-freeze, pointer escape, per-tick-interpreter storm, stale read, raw-field-write): **HIGH** — derived from the actual code structure (id-re-resolve store, tick-owned mutators) + the Phase-21 verified freeze/thread gotchas.
- The exact handle surface: **MEDIUM (deliberately deferred)** — v4-PLAN names it the hardest design question and an operator decision; the research enumerates the candidate set and the mechanism, and surfaces the choice (Open Q §1).

**Research date:** 2026-06-27
**Valid until:** ~30 days for the Starlark API (stable). The server seam FUNCTION NAMES (`moveEntity`, `entities.get`, `serverAiStep`, `goalSelector`, `setWantTarget`, `GetBlock`/`SetBlock`, `attribute.Map.GetInstance`) are the stable anchors — re-grep by name at execution; line numbers may drift. Confirm the `level/attribute/defaults.go` `suppliers` map includes the chosen `base_type` (A2) and the `BindReceiver` spelling (A1) against the pinned code before planning the handle surface.
