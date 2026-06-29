# Phase 23: Entity/mob behavior API — Context

**Gathered:** 2026-06-28
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 23-RESEARCH.md + a focused SUB-ATTRIB/velocity investigation (this session)
**Requirement:** PLUGIN-03

<domain>
## Phase Boundary

**Delivers:** the DECLARATIVE entity/mob behavior API — a plugin DECLARES a mob (attributes + goals/AI) once at load; Go runs the hot path (physics/pathfinding/collision/tick) calling the declared decision hooks; plus a FULL-OVERRIDE path. The Go-side bridge exposes entity/world/nav to Starlark as FROZEN, tick-owned-safe THIN HANDLES (id + *TickLoop, re-resolved each read on the tick goroutine — NEVER a live *Entity pointer). Capability enforcement (deferred from Phase 22) lands here. Gate: a custom mob declared in Starlark spawns, ticks, and MOVES via the Go nav, -race clean.

**OUT (later phases):**
- 1:1 port of any SPECIFIC vanilla mob → Phase 24 (Phase 23 ships a TRIVIAL custom mob proving the API).
- Crafting/recipes → Phase 25; Python → Phase 26; Folia → Phase 27.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Handle surface — WIDE from the start (operator-directed)
- Expose a BROAD entity/world/nav surface to Starlark (not the minimal set). Via the THIN-HANDLE pattern the research validated: a handle is a custom `starlark.Value` wrapping ONLY `id int32` + `*TickLoop` — every read RE-RESOLVES `t.entities.get(id)` on the tick goroutine; `Freeze()` is a no-op (no mutable Starlark state; the live entity is governed by TICK-05, not Starlark's frozen flag). This is the exact id-carry/owner-re-resolve discipline `pathReady.applyTo` already uses (server/async.go).
- **READS** (direct, tick-owned): `entity.health` / `.pos` / `.type` / `.on_ground` / `.velocity`; `entity.attribute(name)` (via attribute.Map.GetValue); `world.block_at(x,y,z)` (ChunkManager.GetBlock); `world.entities_near(x,y,z,r)` (the per-section bucket query); a raycast/`world.is_solid` if cheap.
- **MUTATES** (through tick-owned seams, NEVER raw field writes): `entity.move_to(x,y,z)` (→ mobAI.setWantTarget → groundNavigation.requestPath, the async A* already wired via pathPool); `entity.set_velocity(vx,vy,vz)` (LOCKED — see below); `entity.set_attribute(name,val)` (attribute.Map.GetInstance.SetBaseValue); `world.set_block(x,y,z,state)` (ChunkManager.SetBlock + broadcastBlockUpdate); a `spawn` builtin (scope: spawn a declared mob at a pos).
- Wide ≠ unbounded: every exposed op maps to an EXISTING tick-owned Go seam (the research named them all). No op exposes a raw mutable pointer.

### set_velocity — YES, direct setter (investigation-confirmed faithful)
- `entity.set_velocity(vx,vy,vz)` writes the entity's `vx/vy/vz` fields directly (server/entity.go:60-62). This is VANILLA-FAITHFUL: `Entity.setDeltaMovement` is a direct field write (jar bytecode confirmed), and `LivingEntity.knockback` (the canonical external-push) calls it directly. Safe under TICK-05 (hooks run on the tick goroutine; the fields are tick-owned). The EXISTING `tickPhysics` (tick_phases.go:365-392) integrates it next tick (gravity → drag → friction → moveEntity swept collision → onGround), exactly as vanilla integrates deltaMovement. No new physics plumbing — just the tick-owned setter. Keep `move_to` (nav A*) too — different tool (navigation vs raw push).

### SUB-ATTRIB COVERAGE GAP — CLOSE IT (operator: "todos deberían estar disponibles")
- **The investigation confirmed a REAL gap:** Sulfur's `level/attribute/defaults.go` registers suppliers for ONLY 6 types (player/witch/cat/villager/zombie/silverfish). Unregistered types (pig, cow, …) get a `nil` attribute map and SILENTLY degrade to bare registration defaults (e.g. pig misses its `Animal.createAnimalAttributes` overrides). VANILLA registers EVERY LivingEntity type (jar `DefaultAttributes` confirmed — pig IS supplier-backed in vanilla, via `Pig.createAttributes`).
- **Phase 23 MUST close this so a declared/custom mob of ANY base_type gets faithful attributes** (the operator's expectation is the correct vanilla design). Two-part fix, both 1:1 from the jar:
  1. **Port the missing per-type attribute suppliers** for the common mob/animal types (at minimum the animals + monsters a plugin would plausibly spawn — pig/cow/sheep/chicken/skeleton/creeper/spider/… as scoped at planning), each a literal port of that type's `createAttributes()` from the 26.2 jar (cite the class). Use the existing supplier-builder pattern in defaults.go.
  2. **Add a faithful FALLBACK** for any still-unregistered LivingEntity type: port vanilla's `LivingEntity.createLivingAttributes()` (the base LivingEntity attribute set) as the default supplier, so `NewMapForEntity` NEVER returns nil for a living entity — it returns the base living set. This mirrors what vanilla guarantees (every living type has a supplier) without requiring every single type ported up front.
- The plan SCOPES exactly which per-type suppliers to port now vs lean on the fallback — but the OUTCOME is: a custom mob declared with base_type "pig" (or any living type) gets real attributes, not a silent nil. This is also a vanilla-completeness win beyond plugins.

### Gate mob
- The Phase-23 gate = a TRIVIAL custom mob declared in Starlark (a wander goal: pick a random nearby pos, `move_to` it via the Go nav) that spawns, ticks, and MOVES. With the SUB-ATTRIB fix it can use base_type "pig" (or zombie) and get faithful attributes either way. The wander goal is a `starlarkGoal` implementing the EXISTING Go `Goal` interface (server/ai_goal.go), arbitrated by the existing `goalSelector` (MOVE/LOOK/JUMP/TARGET flag-locking) UNCHANGED — `buildAIFromDecl` mirrors `newPigAI()`.

### Capability ENFORCEMENT — ON in Phase 23
- Now that real entity/world handles are exposed, ENFORCE the `capabilities` manifest field (parsed-but-unenforced since Phase 22). A plugin without the `world` capability cannot call `world.set_block`/`world.block_at`; without `entities` cannot mutate entities; etc. Enforcement check at the handle-op boundary (the builtin/handle method consults the owning plugin's declared capabilities). Define the capability set (e.g. `entities.read`, `entities.write`, `world.read`, `world.write`, `nav`) at planning. A capability-denied call returns a Starlark error (not a silent no-op) so the author sees it.

### The declare-once model (no per-mob-per-tick interpreter)
- A plugin declares a mob's attributes + a set of goals (each goal = a priority + a Starlark `tick`/`should_run` callback) ONCE at load (`buildAIFromDecl`). Go runs the hot path; the interpreter fires ONLY inside a RUNNING goal's callback, gated by `goalSelector` arbitration — an idle mob = ZERO starlark.Calls. Full-override = the degenerate case (one top-priority goal claiming all flags), no separate code path. This preserves the "200 mobs × 20 TPS ≠ 4000 interpreter calls/sec" rule.

### Where the code lives
- The handle bridge + the declared-mob builder live where they can see the tick-owned entity/world/nav types — research Open-Q #7 (import direction): recommended **A** (handles in `server`, since they need `*TickLoop`/`*Entity`/`ChunkManager`), with the Starlark-value wrappers in a sub-file. The plugin/host event payloads stay scalar; Phase 23 adds the HANDLE values as a new bridge the host can pass to declared-mob callbacks. Confirm the exact package boundary at planning (avoid an import cycle: server imports plugin/host + plugin/starlark; the handle types may need to live in server or a server-visible package).
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 23 row + gate; "the frozen-handle API surface is the hardest design question".
- `.planning/REQUIREMENTS.md` — PLUGIN-03 full.
- `.planning/phases/23-entity-mob-behavior-api/23-RESEARCH.md` — HIGH-confidence: the thin-handle pattern; the read-vs-mutate-seam table with the REAL named functions (entityStore.get, Entity fields, attribute.Map, ChunkManager.GetBlock/SetBlock, moveEntity, entities.move, mobAI.setWantTarget→groundNavigation.requestPath/pathPool); `tickAI`/`serverAiStep`/`Goal`/`goalSelector`/`buildAIFromDecl` (the AI tick the declared goal slots into); the freezing model; the 7 open questions (now decided here).
- The SUB-ATTRIB/velocity investigation (this session): `level/attribute/defaults.go` (6 suppliers, nil fallback gap, the supplier-builder pattern), `server/entity.go:60-62` (vx/vy/vz), `server/tick_phases.go:365-392` (tickPhysics integration), `server/physics.go` (moveEntity swept collision), `server/async.go:311` (newPigAI spawn), jar `DefaultAttributes`/`Pig.createAttributes`/`LivingEntity.createLivingAttributes`/`Entity.setDeltaMovement`/`LivingEntity.knockback`.
- `CLAUDE.md` — CGO=0, -race Docker, push development, **1:1-jar mandate (applies to the SUB-ATTRIB supplier ports — literal jar copies, cited)**, TICK-05.
- The jar: `temp/cache/26.2-inner.jar` via `javap -c -p` (`/c/Program Files/Zulu/zulu-25/bin/javap`) — for the per-type `createAttributes()` ports + the velocity/knockback confirmation.
</canonical_refs>

<specifics>
## Specific Ideas

- The thin-handle: `type entityHandle struct { id int32; t *TickLoop }` implementing `starlark.Value` (+ `HasAttrs` for reads, `HasSetField`/Callable methods for mutates). `Freeze()`/`Hash()` trivial. Reads re-resolve `t.entities.get(id)`; a dead entity → the read returns None / an error (the author handles a despawned target).
- The SUB-ATTRIB supplier ports are 1:1 jar copies (`javap` each `createAttributes`); the fallback is `LivingEntity.createLivingAttributes` ported once. After the fix, `NewMapForEntity` returns a real map for every living type. Add a test that a pig (and the gate mob) gets non-default, type-correct attributes.
- The gate test: declare a wander mob in a `.star`, spawn it, advance N ticks, assert its position CHANGED via the Go nav, -race clean (Docker). The declared goal must go through `goalSelector` (not bypass it).
- Capability enforcement test: a plugin without `world.write` calling `world.set_block` gets a Starlark error; with it, the block changes.
- Race: prove no handle escapes the tick goroutine holding a live pointer (freeze + tick re-resolve). The handle stores only an id.

## Open items the planner resolves
- Exact set of per-type suppliers to port now vs rely on the fallback (scope the SUB-ATTRIB fix).
- Exact capability vocabulary + the enforcement check site.
- Import-direction final call (handles in server vs a server-visible bridge package) — avoid a cycle.
- The `spawn` builtin's exact signature/scope.
</specifics>

<deferred>
## Deferred Ideas

- 1:1 port of specific vanilla mobs (zombie/skeleton/etc. full AI) → Phase 24 (Phase 23 = the API + a trivial custom mob + the attribute coverage fix).
- TARGET-flag attack AI (mob attacking the player) — research Open-Q #5: defer to Phase 24 unless trivially in scope.
- Crafting/Python/Folia → Phases 25–27.
</deferred>

---

*Phase: 23-entity-mob-behavior-api*
*Context gathered: 2026-06-28 via operator decisions + v4-PLAN.md + 23-RESEARCH.md + SUB-ATTRIB/velocity investigation*
