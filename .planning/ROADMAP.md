# Roadmap: Sulfur — Minecraft Java 26.2 (protocol 776) server

## Milestones

- ✅ **v1.0 MVP** — Phases 1–9 (shipped 2026-06-24) — full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied noise world with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md)
- ✅ **v2.0 Worldgen Features + Structures** — Phases 10–16 (shipped 2026-06-25) — the overworld now looks + reads like vanilla: full per-biome vegetation (trees/grass/flowers/ores) + the emblematic structures (temples, mineshafts, strongholds, villages) in vanilla positions, deterministic per seed, ported 1:1 from the jar. → [archive](milestones/v2-ROADMAP.md) · [requirements](milestones/v2-REQUIREMENTS.md)
- ✅ **v3 Online-mode + Operator UX + Structure polish** — Phases 17–20 (shipped 2026-06-27) — the world became *actually* playable on a real client (the six unwired gameplay seams), then authenticated/encrypted online-mode logins, a bubbletea TUI console + disconnect logging, the structures finished (loot, inhabitants, terrain-beard, NBT persistence), and a live 12-bug fidelity sweep (chest/water/cane/hats/persist/swim/oxygen) all operator-confirmed in-game. REGION-01 Folia regionization deferred to v4. → [archive](milestones/v3-ROADMAP.md) · [requirements](milestones/v3-REQUIREMENTS.md) · [audit](milestones/v3-MILESTONE-AUDIT.md)
- 📋 **v4 Plugin / Scripting System** — Phases 21–28 (PLANNED, not started) — a dual-runtime extension API: **Starlark** (`go.starlark.net`, pure-Go, CGO=0 preserved, deterministic, native sandbox) as the hot-path core, plus an **opt-in Python** runtime (`qur/gopy` @ `python3.14`, CPython-via-cgo behind a build tag so the default binary stays pure-Go static) for heavy off-tick plugins. Architecture: plugins DECLARE behavior loaded once, Go executes the hot path (physics/pathfinding/tick) calling plugin hooks — NOT per-entity-per-tick scripting — but a plugin can FULLY OVERRIDE a mob. **Dogfood / 1:1 carry-over:** the vanilla mobs + entity logic are rewritten AS PLUGINS that stay a literal 1:1 port of the 26.2 jar (the mandate carries into the plugin layer); the same API also enables custom non-vanilla mobs. Inverts CLAUDE.md's "no plugin API" scope — intentional, user-directed for v4. Folia regionization (REGION-01) folded in here. Full phase breakdown below. → [detailed plan](v4-PLAN.md)

## Phases

<details>
<summary>✅ v1.0 MVP (Phases 1–9) — SHIPPED 2026-06-24</summary>

See [milestones/v1.0-ROADMAP.md](milestones/v1.0-ROADMAP.md) for full phase details.

</details>

<details>
<summary>✅ v2.0 Worldgen Features + Structures (Phases 10–16) — SHIPPED 2026-06-25</summary>

- [x] **Phase 10: Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap** (3/3 plans) — completed 2026-06-25
- [x] **Phase 11: Feature Pipeline & Decoration Orchestration** (3/3 plans) — completed 2026-06-25
- [x] **Phase 12: Core Feature Types** (3/3 plans) — completed 2026-06-25
- [x] **Phase 13: Trees, Dungeon & Features Visual Gate** (4/4 plans) — completed 2026-06-25 (visual gate approved: trees + vines on a real client)
- [x] **Phase 14: Structure Pipeline & Temples** (3/3 plans) — completed 2026-06-25
- [x] **Phase 15: Mineshaft & Stronghold** (3/3 plans) — completed 2026-06-25
- [x] **Phase 16: Village Jigsaw & Structures Visual Gate** (3/3 plans) — completed 2026-06-25 (visual gate approved 2026-06-25, seed 25: villages + temples + mineshafts + strongholds reproduce per seed)

Full phase details: [milestones/v2-ROADMAP.md](milestones/v2-ROADMAP.md).

</details>

<details>
<summary>✅ v3 Online-mode + Operator UX + Structure polish (Phases 17–20) — SHIPPED 2026-06-27</summary>

- [x] **Phase 17: Gameplay Completion — the six unwired seams** (GAMEPLAY-01..07) — wired the existing-but-disconnected gameplay: player-entity-in-tracker broadcast (GAMEPLAY-01, the keystone), persisted-position apply (02), inventory join-sync (03), Attack/Interact damage dispatch + fall damage (04), fluid simulation + player fluid physics (05), block-break item drops (06), closed by a real-client multiplayer VISUAL GATE (07, operator-confirmed live). (completed 2026-06-27)
- [x] **Phase 18: Online-mode — auth + protocol encryption** (ONLINE-01/02) — EncryptionRequest/Response RSA key exchange + AES-128/CFB8 stream encryption (hand-rolled CFB8 over stdlib AES, no new dep) + Yggdrasil `hasJoined` session-server verification, behind an `online-mode` config flag. (completed 2026-06-26)
- [x] **Phase 19: Operator UX — TUI console + disconnect logging** (TUI-01/02) — a bubbletea+bubbles terminal console (command-input + live log viewport) that degrades to plain logging when stdout is not a TTY, plus disconnect-reason logging (kick/timeout/protocol/quit/login-fail).
 Both complete + operator-validated; verified 3/3. (completed 2026-06-27)
- [x] **Phase 20: Structure polish — loot, inhabitants, beard, persistence** (STRUCT-POLISH-01..04) — the documented v2 deferrals: loot tables (chests + block drops, shared evaluator with GAMEPLAY-06), structure entities (villagers/witch/cat/silverfish), `afterPlace` terrain-beard, and structure-start NBT persistence.
 (completed 2026-06-27)

Full phase details: [milestones/v3-ROADMAP.md](milestones/v3-ROADMAP.md).

</details>

### 📋 v4 Plugin / Scripting System (Phases 21–28) — NEXT (v3 closed; execution starts now)

> Full architecture + per-phase requirements live in [v4-PLAN.md](v4-PLAN.md). One-line summaries here.

- [x] **Phase 21: Starlark runtime foundation** (PLUGIN-01) — embed `go.starlark.net` (pure-Go, CGO=0 preserved); the per-goroutine `starlark.Thread` model, the sandbox (step-counter budget, `-recursion` off, no I/O builtins), FrozenValue sharing across the tick boundary, plugin load/parse/compile lifecycle, and a `replace`-pinned fork if needed. No game hooks yet — just "a `.star` file loads, runs sandboxed, calls a Go builtin, returns a value, race-clean."
 (completed 2026-06-28)
- [x] **Phase 22: Plugin host + event bus** (PLUGIN-02) — the plugin manager (discover/load/unload `.star` plugins from a plugins dir), the manifest, the **event system** (typed events: tick, player join/leave, block break/place, entity spawn/death, damage), the registration API (a plugin declares hooks once at load), and the Go→plugin dispatch seam that stays off the per-entity hot path (event-driven + cached, not per-tick-per-entity).
 (completed 2026-06-28)
- [ ] **Phase 23: Entity/mob behavior API** (PLUGIN-03) — the **declarative mob-behavior interface**: a plugin DECLARES a mob's attributes/goals/AI (loaded once); Go runs the hot path (physics/pathfinding/tick) calling the declared hooks. The FULL-OVERRIDE path: a plugin can replace a mob's whole behavior. The Go-side bridge exposes the entity/world/nav API to Starlark as frozen, tick-owned-safe handles. Race-clean by construction (TICK-05 carries into the plugin call seam).
- [ ] **Phase 24: Vanilla mobs AS plugins (1:1 dogfood)** (PLUGIN-04) — rewrite the existing Go mob/entity logic as Starlark plugins that remain a **literal 1:1 port of the 26.2 jar** (the mandate carries into the plugin layer). This is the validation that the API is ultra-powerful enough to express real vanilla AI. Proven against the jar bytecode + the existing mob-AI tests, byte/behavior-identical to the Go-native path it replaces.
- [ ] **Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood)** (PLUGIN-05) — the crafting system is built THROUGH the plugin API, not as a hardcoded Go subsystem: a recipe-provider plugin loads the jar-extracted recipes (shaped/shapeless/smelting/etc) and drives the crafting-grid result + the ResultSlot.onTake consumption (the slots exist in InventoryMenu today but the result is a no-op stub — `inventory_click.go`: "no recipes wired in v1"). This proves the plugin API is general across a SECOND domain (recipes/menus, not just entity AI) — the real test of "ultra-powerful". The 1:1 mandate carries: the recipe-match + consume logic stays a literal jar port (RecipeManager/CraftingMenu), just expressed via the plugin host. Includes the crafting_table block + 3×3 menu (vanilla only had the 2×2 inventory grid). Custom (non-vanilla) recipes fall out for free.
- [ ] **Phase 26: Opt-in Python runtime** (PLUGIN-06) — `qur/gopy` @ `python3.14` (idiomatic CPython bindings via cgo/libpython) behind a `python` build tag so the **default binary stays pure-Go static (CGO=0)**. A bridge for HEAVY off-tick plugins only (never the per-tick hot path — cgo + GIL + non-determinism keep it off the tick). The same event/registration API as Starlark, so a plugin author picks the runtime per workload.
- [ ] **Phase 27: Folia regionization** (REGION-01, folded from v3-deferral) — Leaf/Folia-style independent-region tick threads so the world ticks in parallel regions; the plugin call seam + the entity API must be region-aware (a plugin hook runs on its region's thread). This is the perf payoff that makes "ultra-efficient" real at scale, and it was always a v4 item.
- [ ] **Phase 28: Plugin system visual + perf gate** (PLUGIN-07, autonomous:false) — a real client confirms: a custom non-vanilla mob plugin works, the vanilla-mobs-as-plugins path is behavior-identical, crafting (vanilla + a custom recipe) works through the plugin path, the event system fires correctly, and the perf target holds (the plugin layer adds no measurable per-tick cost vs Go-native; Folia regions scale). Closes v4.

### Phase 21: Starlark runtime foundation
**Goal**: A Starlark runtime is embedded in Sulfur (pure-Go, CGO_ENABLED=0 preserved) that loads, sandboxes, and runs a `.star` plugin file — the riskiest single thing proven first (sandbox + CGO=0 + race-safety) before any host/event/behavior layer is built on top.
**Depends on**: Phase 20 (v3) + the v3.1 SUB prerequisites
**Requirements**: PLUGIN-01
**Success Criteria** (what must be TRUE):
  1. `go.starlark.net` is embedded; a `.star` file loads, parses, and compiles once via the plugin load lifecycle; `CGO_ENABLED=0 go build ./...` stays clean (no `import "C"`, go.mod static) (PLUGIN-01)
  2. The sandbox holds: per-`starlark.Thread` step-counter budget enforced, recursion OFF (self-call is a dynamic error), no filesystem/network builtins exposed unless explicitly registered (PLUGIN-01)
  3. One `starlark.Thread` per goroutine; a FrozenValue produced at load is safe to read from the tick goroutine across the tick boundary (PLUGIN-01)
  4. A plugin calls a registered Go builtin and returns a value to Go (`starlark.Call`); the whole path is Docker `-race` clean (PLUGIN-01)
**Plans**: 2 plans in 2 waves
Plans:
- [x] 21-01-PLAN.md — Embed go.starlark.net (plain dep, CGO=0), build the plugin/starlark leaf package (sandbox policy, dir loader, echo builtin, LoadedPlugin.Call) + load-once/builtin-round-trip tests
- [x] 21-02-PLAN.md — The three sandbox negative tests (step-budget halt, recursion rejected, no-I/O) + the frozen cross-goroutine -race test
**Research**: Confirm Starlark fork-or-vendor (likely a plain dep — does upstream expose the sandbox knobs we need, or is a `replace`-pinned patch required?). Context7 `/google/starlark-go` for the Thread/Freeze/Call/ExecFile API. v4-PLAN.md is the plan of record.

### Phase 22: Plugin host + event bus
**Goal**: A plugin host loads/unloads plugins from a plugins dir and a typed event bus fires the core gameplay events to register-once hooks — with the Go→plugin dispatch seam kept OFF the per-entity hot path.
**Depends on**: Phase 21
**Requirements**: PLUGIN-02
**Success Criteria** (what must be TRUE):
  1. The plugin manager discovers/loads/unloads plugins from a plugins directory with a manifest (distribution-format decision: lean manifest-dir) (PLUGIN-02)
  2. A typed event system fires the core events (tick, player join/leave, block break/place, entity spawn/death, damage); a plugin registers hooks ONCE at load via the registration API (PLUGIN-02)
  3. The Go→plugin dispatch seam is event-driven + cached — NOT a per-tick-per-entity scan; a subscribed hook fires on the real tick, `-race` clean (PLUGIN-02)
  4. Open decisions resolved here: hot-reload scope, capability/permission model (PLUGIN-02)
**Plans**: 2 plans
Plans:
- [x] 22-01-PLAN.md — plugin/host package: TOML manifest + Manager (discover/load/unload) + register-once event bus + Emit + the Phase-21 LoadWith extension (A2) (Wave 1)
- [x] 22-02-PLAN.md — wire host.Emit into the 8 discrete server seams (on_damage post-mitigation) + the fire-once-per-break GATE + anti-seam grep gate + FULL fsnotify hot-reload with tick-goroutine swap (Wave 2)
**Research**: Plugin-dir/manifest format, the event taxonomy mapped to existing tick seams, the off-hot-path dispatch design.

### Phase 23: Entity/mob behavior API
**Goal**: A declarative mob-behavior interface — a plugin declares a mob's attributes/goals/AI once, Go runs the hot path calling the declared hooks, with a full-override path and a frozen tick-safe entity/world/nav bridge.
**Depends on**: Phase 22 (event bus + registration API) + SUB-ATTRIB (real attributes)
**Requirements**: PLUGIN-03
**Success Criteria** (what must be TRUE):
  1. A plugin DECLARES a mob's attributes/goals/AI at load; Go runs the hot path (physics/pathfinding/tick/collision) calling the declared hooks — not a script per-entity-per-tick (PLUGIN-03)
  2. The FULL-OVERRIDE path: a plugin can replace a mob's whole decision logic (still through declared seams, owning all of them) (PLUGIN-03)
  3. The Go-side bridge exposes entity/world/nav to Starlark as FROZEN, tick-owned-safe handles; read-only vs mutate-through-a-tick-owned-seam is distinguished (PLUGIN-03)
  4. A trivial custom mob declared in Starlark spawns, ticks, and moves via the Go nav, Docker `-race` clean (PLUGIN-03)
**Plans**: 2 plans in 2 waves
Plans:
- [x] 23-01-PLAN.md — SUB-ATTRIB coverage fix (per-type suppliers + LivingEntity fallback, 1:1 jar) + thin entity/world handles + capability enforcement (Wave 1)
- [ ] 23-02-PLAN.md — declare_mob/goal + starlarkGoal (via goalSelector) + buildAIFromDecl + the wander-mob GATE (spawns/ticks/moves via Go nav) + idle-no-interpreter + declared-mob -race (Wave 2)
**Research**: The hardest design question — the exact frozen-handle API surface (which entity/world/nav ops, read-only vs mutating). Map the existing Go goal-selector/brain seams to declared hooks.

### Phase 24: Vanilla mobs AS plugins (1:1 dogfood)
**Goal**: The existing Go mob/entity logic is rewritten as Starlark plugins that remain a literal 1:1 port of the 26.2 jar — validating the API expresses real vanilla AI (the FIRST dogfood).
**Depends on**: Phase 23
**Requirements**: PLUGIN-04
**Success Criteria** (what must be TRUE):
  1. Vanilla mob behavior is re-expressed in Starlark as a literal jar port (method-for-method, cited, verified against `temp/cache/26.2-inner.jar` bytecode — the 1:1 mandate carries into the plugin layer) (PLUGIN-04)
  2. The plugin-driven vanilla mob is behavior-identical to the Go-native path it replaces (PLUGIN-04)
  3. The existing mob-AI tests are green against the plugin-driven path; Docker `-race` clean (PLUGIN-04)
**Plans**: TBD (set by `/gsd-plan-phase 24`)
**Research**: Per-mob jar bytecode (the goal sets the existing Go path already cites) re-verified for the Starlark re-expression. If vanilla AI doesn't fit the API, the API is wrong — caught here, before Python.

### Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood)
**Goal**: Crafting is built THROUGH the plugin API (a recipe-provider plugin + the crafting_table 3×3 menu), proving the API generalizes to a second, very different domain (menus/recipes, not entity AI). Crafting was never built in the core — only the empty grid slots exist.
**Depends on**: Phase 22 (host + item/menu bridge); ordered after 24 for the "domain 1 then domain 2" story (does NOT depend on 24)
**Requirements**: PLUGIN-05
**Success Criteria** (what must be TRUE):
  1. A recipe-provider plugin loads the jar-extracted recipes (shaped/shapeless/smelting/…) through the plugin API — not a hardcoded Go subsystem (PLUGIN-05)
  2. The crafting-grid result + `ResultSlot.onTake` consumption work (today a no-op stub — `inventory_click.go`: "no recipes wired in v1"); the recipe-match + consume logic is a literal jar port (RecipeManager/CraftingMenu), cited (PLUGIN-05)
  3. The `crafting_table` block + the 3×3 menu exist (the core only had the 2×2 inventory grid) (PLUGIN-05)
  4. Vanilla recipes craft correctly through the plugin path (result + consume, jar-verified) AND a custom recipe works (PLUGIN-05)
**Plans**: TBD (set by `/gsd-plan-phase 25`)
**Research**: RecipeManager/CraftingMenu/ResultSlot.onTake bytecode; the jar recipe extraction; the item/menu bridge surface the plugin API needs.

### Phase 26: Opt-in Python runtime
**Goal**: An opt-in Python runtime (`qur/gopy` @ `python3.14`, behind a `python` build tag) for HEAVY off-tick plugins only — the default binary stays pure-Go static (CGO=0).
**Depends on**: Phase 24 + Phase 25 (the Starlark path proven end-to-end on two domains first)
**Requirements**: PLUGIN-06
**Success Criteria** (what must be TRUE):
  1. `qur/gopy` @ branch `python3.14` (CPython via cgo/libpython) is wired behind a `python` build tag; the DEFAULT (no-tag) build is still pure-Go static, `CGO_ENABLED=0 go build ./...` clean (PLUGIN-06)
  2. Python plugins run OFF-tick only (their own goroutines/pools, rejoin via the existing async seam `asyncIn2`/ants) — never inside `tickOnce` (PLUGIN-06)
  3. The same event/registration API as Starlark — an author picks the runtime per workload, not per API (PLUGIN-06)
  4. A Python plugin runs off-tick and rejoins via the async seam, observably (PLUGIN-06)
**Plans**: TBD (set by `/gsd-plan-phase 26`)
**Research**: The `python3.14` branch build (libpython link, build-tag isolation), the off-tick bridge over the existing async pool, GIL handling.

### Phase 27: Folia regionization
**Goal**: Folia-style independent-region tick threads so the world ticks in parallel regions; the plugin call seam + entity API become region-aware (folded from the v3 REGION-01 deferral).
**Depends on**: Phase 23 (the working single-thread plugin seam to regionize) — regionize a working seam, don't design it around regions first
**Requirements**: REGION-01
**Success Criteria** (what must be TRUE):
  1. The world ticks in independent parallel regions (Leaf/Folia-style region threads) (REGION-01)
  2. The plugin call seam + the entity API are region-aware — a plugin hook runs on its region's thread (REGION-01)
  3. The whole regionized tick + plugin path is Docker `-race` clean (REGION-01)
**Plans**: TBD (set by `/gsd-plan-phase 27`)
**Research**: The Folia region model (region ownership of chunks/entities, cross-region transfer), how the tick-owned plugin seam re-homes onto region threads.

### Phase 28: Plugin system visual + perf gate
**Goal**: A real vanilla 26.2 client + a perf benchmark confirm the whole plugin system end-to-end — closes v4 (autonomous:false).
**Depends on**: Phases 21–27
**Requirements**: PLUGIN-07
**Success Criteria** (what must be TRUE):
  1. **VISUAL GATE (autonomous:false)**: a real client confirms a custom non-vanilla mob plugin works, the vanilla-mobs-as-plugins path is behavior-identical, crafting (vanilla + a custom recipe) works through the plugin path, and the event system fires correctly (PLUGIN-07)
  2. **PERF GATE**: a benchmark shows the plugin layer adds no measurable per-tick cost vs Go-native, and Folia regions scale (PLUGIN-07)
**Plans**: TBD (set by `/gsd-plan-phase 28`)
**Research**: The benchmark harness (plugin-driven vs Go-native per-tick cost), the real-client validation checklist.

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1–9 (v1) | v1.0 | — | Complete | 2026-06-24 |
| 10–16 (v2.0 worldgen + structures) | v2.0 | 22/22 | Complete | 2026-06-25 |
| 17–20 (v3 online-mode + operator-UX + structure-polish) | v3 | 33/33 | Complete | 2026-06-27 |
| 21. Starlark runtime foundation | v4 | 2/2 | Complete   | 2026-06-28 |
| 22. Plugin host + event bus | v4 | 2/2 | Complete   | 2026-06-28 |
| 23. Entity/mob behavior API | v4 | 1/2 | In Progress|  |
| 24. Vanilla mobs AS plugins (1:1 dogfood) | v4 | 0/? | Not started | — |
| 25. Crafting/recipes AS plugins (2nd-domain dogfood) | v4 | 0/? | Not started | — |
| 26. Opt-in Python runtime | v4 | 0/? | Not started | — |
| 27. Folia regionization | v4 | 0/? | Not started | — |
| 28. Plugin system visual + perf gate | v4 | 0/? | Not started | — |

## Deferred / Backlog (unwired or subsystem-blocked)

Items discovered during execution that are **not yet wired** or are **blocked on an unbuilt subsystem**. Each names the missing subsystem and the phase that should build it. Per the "no built-but-unwired code" rule (the SC4 gap lesson), we DO NOT ship speculative unwired code — we record it here and build it when its subsystem lands. Nothing here blocks the v3 close (all are post-v3 / vanilla-completeness work).

### Missing subsystems → BUILT as the **v3.1 "Persistence & Vanilla Completeness"** milestone (2026-06-27)

All 5 prerequisite subsystems are now PORTED 1:1 from the 26.2 jar, built in isolated worktrees with
maximum parallelization (ATTRIB+FACESTURDY simultaneous, then ITEMNBT+PERSIST, then BLOCKTICK),
merged to `ender-776` with zero conflicts (disjoint files). Each build/vet/test green + -race green.

| Subsystem | Status | Commit | What landed |
|-----------|--------|--------|-------------|
| **SUB-ATTRIB** — attribute system | ✅ DONE | 301aaec3 | `level/attribute/`: AttributeInstance.calculateValue fold (ADD_VALUE→MULT_BASE→MULT_TOTAL), DefaultAttributes per-entity (bit-exact: Witch 26.0, Cat 0.30000001192092896, Zombie 0.23000000417232513…), Mob.finalizeSpawn RNG draw order (triangle + left-handed), combat/armor/fall/knockback scaling. Stubs cited: variant/profession (registry-bound), NBT-persist of attrs. |
| **SUB-FACESTURDY** — block support shapes | ✅ DONE | d25b1db7 | Codegen `GenBlockSupport.java`+`gen_block_support.go` → `level/block/support.go` (32366 states, precomputed [18] table). Replaced 2 proxies: `faceSturdyUp` (worldgen) + `isSuffocating` (now per-block faithful — glass/leaves/slabs no longer suffocate). Attachment-block `canSurvive` (torches/rails) deferred — the `IsFaceSturdy` primitive is now available for it. |
| **SUB-ITEMNBT** — ItemStack disk codec | ✅ DONE (Phase A) | 6406dcfe | `save/item_nbt.go`: `{Slot,id,count}` + ContainerHelper.saveAllItems/loadAllItems 1:1. **Phase B TODO** (cited): wire→disk component transcoder (per-DataComponentType streamCodec→codec) for enchanted/named items; today component-bearing stacks persist `{id,count}` with a measured `DroppedComponents` counter (never silent). |
| **SUB-PERSIST** — chunk-save loop | ✅ DONE | 8d2a5ac2 | `world/chunk_save_loop.go` RunChunkSaveLoop: dirty tracking + periodic/on-unload flush + region write (semaphore IO throttle) + `save.Level` level.dat writer. **Concurrency**: only immutable byte snapshots cross the tick→save seam (race-free by construction, TICK-05). **Opt-in** `SULFUR_PERSIST_CHUNKS=1` (default unchanged). Unblocked the HANDOFF chest-item flush (trySaveLootTable XOR saveAllItems 1:1) + player-inventory persist. |
| **SUB-BLOCKTICK** — scheduled block ticks | ✅ DONE | (v3.1 merge) | `level/ticks/` LevelTicks: ScheduledTick deterministic ordering (triggerTick→priority→subTickOrder), save/load `block_ticks` NBT, cap 65536. First consumer wired: sugar cane (scheduleTick→drain→canSurvive→destroy+drop, growth). Existing fluid loop COEXISTS (not migrated — documented). TODO cited: tickCheck tightened to shouldTickBlocksAt; world random-tick driver (separate subsystem). |

**Remaining follow-ups (smaller, not new subsystems):** SUB-ITEMNBT Phase B (component transcoder), attachment-block survival (torches/rails — uses the now-available `IsFaceSturdy`), cactus/crop block-tick consumers, attribute NBT-persistence, variant/profession real reads (registry-bound).

### Wired-but-partial / small follow-ups (no new subsystem needed)

| Item | State | Note |
|------|-------|------|
| Chest BE persistence | **DONE (cb4597d6)** | `block_entities` now serialize (loot table+seed round-trip; unopened chest re-rolls deterministically = vanilla-correct). Item-level flush waits on SUB-PERSIST + SUB-ITEMNBT. |
| nbt list-of-Marshaler encoding | **FIXED (cb4597d6)** | Encoder list loop now routes through `marshal` — also un-corrupts `entities`/`Lights`/`ScheduledEvents` (`[]RawMessage`) for when those write paths land. |
| Chest open UI follow-ups | Partial | Random-slot shuffle (cosmetic), multi-viewer sync — small, no subsystem. |
| Vegetation survival | DONE | Non-vegetation classes blocked on SUB-FACESTURDY / SUB-BLOCKTICK above. |
| DATA_SHARED_FLAGS sprint/sneak pose; mob-water-nav; async-fluid | Deferred | AI/perf-track items, not persistence. |
