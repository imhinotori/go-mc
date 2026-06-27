# Roadmap: Sulfur — Minecraft Java 26.2 (protocol 776) server

## Milestones

- ✅ **v1.0 MVP** — Phases 1–9 (shipped 2026-06-24) — full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied noise world with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md)
- ✅ **v2.0 Worldgen Features + Structures** — Phases 10–16 (shipped 2026-06-25) — the overworld now looks + reads like vanilla: full per-biome vegetation (trees/grass/flowers/ores) + the emblematic structures (temples, mineshafts, strongholds, villages) in vanilla positions, deterministic per seed, ported 1:1 from the jar. → [archive](milestones/v2-ROADMAP.md) · [requirements](milestones/v2-REQUIREMENTS.md)
- 🚧 **v3 Online-mode + Operator UX + Structure polish** — Phases 17–20 (in progress) — first the world becomes *actually* playable on a real client (the six unwired gameplay seams), then authenticated/encrypted online-mode logins, a bubbletea TUI console + disconnect logging, and the structures finished (loot, inhabitants, terrain-beard, NBT persistence). REGION-01 Folia regionization deferred to v4.
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

### 🚧 v3 Online-mode + Operator UX + Structure polish (Phases 17–20) — IN PROGRESS

- [ ] **Phase 17: Gameplay Completion — the six unwired seams** (GAMEPLAY-01..07) — wire the existing-but-disconnected gameplay: player-entity-in-tracker broadcast (GAMEPLAY-01, the keystone), persisted-position apply (02), inventory join-sync (03), Attack/Interact damage dispatch + fall damage (04), fluid simulation + player fluid physics (05), block-break item drops (06), closed by a real-client multiplayer VISUAL GATE (07). Pattern: reconnect seams whose core logic already exists + is unit-tested; fluid sim is the one large net-new port.
- [x] **Phase 18: Online-mode — auth + protocol encryption** (ONLINE-01/02) — EncryptionRequest/Response RSA key exchange + AES-128/CFB8 stream encryption (hand-rolled CFB8 over stdlib AES, no new dep) + Yggdrasil `hasJoined` session-server verification, behind an `online-mode` config flag. (completed 2026-06-26)
- [ ] **Phase 19: Operator UX — TUI console + disconnect logging** (TUI-01/02) — a bubbletea+bubbles terminal console (command-input + live log viewport) that degrades to plain logging when stdout is not a TTY, plus disconnect-reason logging (kick/timeout/protocol/quit/login-fail). TUI-02 complete (19-03); TUI-01 awaits the 19-02 operator human-verify checkpoint.
- [ ] **Phase 20: Structure polish — loot, inhabitants, beard, persistence** (STRUCT-POLISH-01..04) — the documented v2 deferrals: loot tables (chests + block drops, shared evaluator with GAMEPLAY-06), structure entities (villagers/witch/cat/silverfish), `afterPlace` terrain-beard, and structure-start NBT persistence.

### 📋 v4 Plugin / Scripting System (Phases 21–28) — PLANNED (execution waits for v3 to close)

> Full architecture + per-phase requirements live in [v4-PLAN.md](v4-PLAN.md). One-line summaries here.

- [ ] **Phase 21: Starlark runtime foundation** (PLUGIN-01) — embed `go.starlark.net` (pure-Go, CGO=0 preserved); the per-goroutine `starlark.Thread` model, the sandbox (step-counter budget, `-recursion` off, no I/O builtins), FrozenValue sharing across the tick boundary, plugin load/parse/compile lifecycle, and a `replace`-pinned fork if needed. No game hooks yet — just "a `.star` file loads, runs sandboxed, calls a Go builtin, returns a value, race-clean."
- [ ] **Phase 22: Plugin host + event bus** (PLUGIN-02) — the plugin manager (discover/load/unload `.star` plugins from a plugins dir), the manifest, the **event system** (typed events: tick, player join/leave, block break/place, entity spawn/death, damage), the registration API (a plugin declares hooks once at load), and the Go→plugin dispatch seam that stays off the per-entity hot path (event-driven + cached, not per-tick-per-entity).
- [ ] **Phase 23: Entity/mob behavior API** (PLUGIN-03) — the **declarative mob-behavior interface**: a plugin DECLARES a mob's attributes/goals/AI (loaded once); Go runs the hot path (physics/pathfinding/tick) calling the declared hooks. The FULL-OVERRIDE path: a plugin can replace a mob's whole behavior. The Go-side bridge exposes the entity/world/nav API to Starlark as frozen, tick-owned-safe handles. Race-clean by construction (TICK-05 carries into the plugin call seam).
- [ ] **Phase 24: Vanilla mobs AS plugins (1:1 dogfood)** (PLUGIN-04) — rewrite the existing Go mob/entity logic as Starlark plugins that remain a **literal 1:1 port of the 26.2 jar** (the mandate carries into the plugin layer). This is the validation that the API is ultra-powerful enough to express real vanilla AI. Proven against the jar bytecode + the existing mob-AI tests, byte/behavior-identical to the Go-native path it replaces.
- [ ] **Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood)** (PLUGIN-05) — the crafting system is built THROUGH the plugin API, not as a hardcoded Go subsystem: a recipe-provider plugin loads the jar-extracted recipes (shaped/shapeless/smelting/etc) and drives the crafting-grid result + the ResultSlot.onTake consumption (the slots exist in InventoryMenu today but the result is a no-op stub — `inventory_click.go`: "no recipes wired in v1"). This proves the plugin API is general across a SECOND domain (recipes/menus, not just entity AI) — the real test of "ultra-powerful". The 1:1 mandate carries: the recipe-match + consume logic stays a literal jar port (RecipeManager/CraftingMenu), just expressed via the plugin host. Includes the crafting_table block + 3×3 menu (vanilla only had the 2×2 inventory grid). Custom (non-vanilla) recipes fall out for free.
- [ ] **Phase 26: Opt-in Python runtime** (PLUGIN-06) — `qur/gopy` @ `python3.14` (idiomatic CPython bindings via cgo/libpython) behind a `python` build tag so the **default binary stays pure-Go static (CGO=0)**. A bridge for HEAVY off-tick plugins only (never the per-tick hot path — cgo + GIL + non-determinism keep it off the tick). The same event/registration API as Starlark, so a plugin author picks the runtime per workload.
- [ ] **Phase 27: Folia regionization** (REGION-01, folded from v3-deferral) — Leaf/Folia-style independent-region tick threads so the world ticks in parallel regions; the plugin call seam + the entity API must be region-aware (a plugin hook runs on its region's thread). This is the perf payoff that makes "ultra-efficient" real at scale, and it was always a v4 item.
- [ ] **Phase 28: Plugin system visual + perf gate** (PLUGIN-07, autonomous:false) — a real client confirms: a custom non-vanilla mob plugin works, the vanilla-mobs-as-plugins path is behavior-identical, crafting (vanilla + a custom recipe) works through the plugin path, the event system fires correctly, and the perf target holds (the plugin layer adds no measurable per-tick cost vs Go-native; Folia regions scale). Closes v4.

### Phase 17: Gameplay Completion — the six unwired seams
**Goal**: The world is *actually* playable on a real multiplayer client — the six gameplay seams that v1 left unwired (players invisible to each other, position not loaded, inventory empty on join, no damage, no fluid simulation, no block drops) are reconnected by wiring the existing-but-disconnected core logic, and a real vanilla 26.2 client confirms end-to-end multiplayer gameplay.
**Depends on**: Phase 16
**Requirements**: GAMEPLAY-01, GAMEPLAY-02, GAMEPLAY-03, GAMEPLAY-04, GAMEPLAY-05, GAMEPLAY-06, GAMEPLAY-07
**Success Criteria** (what must be TRUE):
  1. Each joining player is added to the entity store as an `entity.Player` (ID 156) and its position synced every tick, so the existing entity tracker broadcasts AddEntity/move/remove — two clients see each other move (GAMEPLAY-01, the keystone)
  2. The join bootstrap applies the persisted x/y/z (already saved + round-trip tested) instead of hardcoding spawn, so position survives a reconnect (GAMEPLAY-02)
  3. The initial `ContainerSetContent` is sent on join so the client's inventory window is populated and the existing (tested) click/creative/carried handlers work end-to-end (GAMEPLAY-03)
  4. `applyInput` gains real ServerboundAttack/Interact cases that resolve the target + call the existing (tested) `applyDamage`/`die`/`performRespawn`, plus an environmental-damage tick (fall damage) — attacks deal damage, death + respawn work (GAMEPLAY-04)
  5. `tickWorld()` simulates fluids — vanilla `FlowingFluid`/`LiquidBlock` flow propagation + fluid-level updates + waterlogged handling, ported from the jar — plus player fluid physics (swim/buoyancy, slowed movement, breath) (GAMEPLAY-05)
  6. After a block break sets air, the block's drop is looked up, an `Item` entity (ID 71) is spawned + tracked, and GAMEPLAY-01's broadcast path sends AddEntity so the drop is pickable (GAMEPLAY-06)
  7. **VISUAL GATE (autonomous:false)**: a real vanilla 26.2 client (two players) confirms players see each other move, positions/inventory survive a reconnect, attacks deal damage + death/respawn, water flows + affects movement, and broken blocks drop pickable items (GAMEPLAY-07)
**Plans**: 5 plans in 3 waves
- [x] 17-01-PLAN.md — GAMEPLAY-01/02/03: player visibility + tab-list broadcast (keystone), persisted-position load, inventory join-sync (wave 1)
- [x] 17-02-PLAN.md — GAMEPLAY-05: vanilla FlowingFluid sim + scheduled-tick queue + player fluid physics (wave 2)
- [x] 17-03-PLAN.md — GAMEPLAY-04: Attack/Interact damage dispatch + fall damage (wave 2)
- [x] 17-04-PLAN.md — GAMEPLAY-06: block-break Item drops + ITEM metadata (wave 2)
- [~] 17-05-PLAN.md — GAMEPLAY-07: real-client multiplayer VISUAL GATE (autonomous:false, wave 3) — **1:1 work COMPLETE + tested + -race green** (17-05-SUMMARY.md + 17-VERIFICATION.md); the three visibility audits (swing/Animate, equipment/SetEquipment, eat-pose/DATA_LIVING_ENTITY_FLAGS, delta-move/sendChanges) landed, cave-gap + join-disconnect + worldgen-race + block-place-entity-collision all fixed. ONLY the operator's real-client visual sign-off remains.
- [x] 17-06..22 — real-client gap-closure 1:1 fixes surfaced by the visual gate (spawn-finder, fall damage, combat, breath/air-supply sync, FoodData hunger 17-19, container-click 17-20, block-break dig-time + jar-extracted block hardness 17-21, **eating loop 17-22**, the cave-water-gap one-shot post-process, the three entity-visibility audits) — each a literal port of the 26.2 jar.
**Research**: HANDOFF.md documents all six seams with file:line evidence + classification + build order (GAMEPLAY-01 keystone first; GAMEPLAY-05 fluid sim is the one large net-new jar port). Phase research deep-dives the `FlowingFluid` port.

### Phase 18: Online-mode — auth + protocol encryption
**Goal**: Sulfur runs in online-mode — authenticated, encrypted logins with real Mojang/Microsoft UUIDs, skins, and ownership verification — behind an `online-mode` config flag (offline remains the default for local dev).
**Depends on**: Phase 17
**Requirements**: ONLINE-01, ONLINE-02
**Success Criteria** (what must be TRUE):
  1. The EncryptionRequest/EncryptionResponse handshake is ported: RSA PKCS#1 v1.5 (`RSA/ECB/PKCS1Padding`, NOT OAEP — per `net.minecraft.util.Crypt`) key exchange of the 16-byte shared secret + 4-byte verify token, with the trailing `shouldAuthenticate` boolean on the 776 EncryptionRequest (ONLINE-02)
  2. AES-128/CFB8 stream encryption wraps the connection for all packets after the handshake — CFB8 hand-rolled over stdlib `crypto/aes` (Go stdlib dropped `cipher.CFB`), no new crypto dependency (ONLINE-02)
  3. Yggdrasil `hasJoined` session-server verification: the server verifies the shared-secret-derived server hash against `sessionserver.mojang.com` → real UUID + skin properties, and those skin properties propagate to OTHER players via the tab-list ADD_PLAYER (ONLINE-01)
  4. An `online-mode` config flag toggles auth/encryption; offline-mode (default) is unchanged
**Plans**: 2 plans in 1 wave (parallel, disjoint files)
- [x] 18-01-PLAN.md — ONLINE-02 crypto/auth wire: shouldAuthenticate boolean + 4-byte challenge + url-encoded hasJoined, online-mode flag, authDigest vectors + offline handshake test (wave 1)
- [x] 18-02-PLAN.md — ONLINE-01 skins: propagate authenticated GameProfile properties to ADD_PLAYER so other players render the real skin, with a strict round-trip test (wave 1)
**Research**: Phase research covers the Yggdrasil auth flow + the protocol-776 encryption packet shapes + the CFB8 hand-roll. It is a VERIFY-then-WIRE phase: ~90% of the crypto (CFB8, RSA PKCS1v15, authDigest, cipher ordering) is already FAITHFUL; the deltas are the missing EncryptionRequest boolean, the flag, and the dropped skin properties.

### Phase 19: Operator UX — TUI console + disconnect logging
**Goal**: The operator runs + watches Sulfur from a proper terminal console — command input + live scrolling logs in one screen — with every player drop logged with its reason.
**Depends on**: Phase 18
**Requirements**: TUI-01, TUI-02
**Success Criteria** (what must be TRUE):
  1. A charmbracelet bubbletea+bubbles console renders a command-input zone (textinput) + a live log viewport; typed commands dispatch through the existing command system (TUI-01)
  2. The TUI degrades gracefully when stdout is not a TTY (headless/Docker → plain structured logging, no TUI) (TUI-01)
  3. Every player disconnect logs WHY (kick/timeout/protocol error/clean quit/login failure) with player identity + reason, surfaced in the TUI log stream + structured logs (TUI-02)
**Plans**: 3 plans in 2 waves (Wave 1: 19-01 foundation; Wave 2 parallel: 19-02 + 19-03, disjoint files)
  - [x] 19-01-PLAN.md — charm v2 deps + bubbletea Model (viewport+textinput) + slog.Handler log bridge (non-blocking, drop-on-full)
  - [x] 19-02-PLAN.md — console dispatch seam (runConsoleCommand via existing graph, no issuer) + main() TTY fork (TUI vs plain stderr)
  - [x] 19-03-PLAN.md — disconnect taxonomy (protocol_error/write_error/login/config/kick/timeout/quit) + slog join/leave + KeepAlive double-leave hardening
**Research**: Phase research covers the bubbletea/bubbles API (textinput + viewport composition) + the TTY-detection degrade path.

### Phase 20: Structure polish — loot, inhabitants, beard, persistence
**Goal**: The v2 structures are finished — chests have loot, structures have their inhabitants, they adapt to terrain, and computed starts persist to NBT.
**Depends on**: Phase 19
**Requirements**: STRUCT-POLISH-01, STRUCT-POLISH-02, STRUCT-POLISH-03, STRUCT-POLISH-04
**Success Criteria** (what must be TRUE):
  1. The loot-table system is ported (embedded loot-table JSON + function/condition/number-provider evaluation) and structure + dungeon chests are populated — shares the evaluator with GAMEPLAY-06's block loot tables (STRUCT-POLISH-01)
  2. Structure inhabitants spawn with the structure: villagers (village), witch (swamp hut), cat (village/swamp), silverfish (stronghold) (STRUCT-POLISH-02)
  3. The `afterPlace` terrain-beard adaptation is ported so structures fit terrain instead of floating/clipping (STRUCT-POLISH-03)
  4. Computed `StructureStart`s persist to region NBT (write/read the structure-start tags) so starts survive without recompute (STRUCT-POLISH-04)
**Plans**: TBD (set by /gsd-plan-phase)
**Research**: Phase research covers the loot-table evaluation model + the `afterPlace`/Beardifier adaptation + the structure-start NBT tag format.

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1–9 (v1) | v1.0 | — | Complete | 2026-06-24 |
| 10. Worldgen Foundation — LCG, Cross-Chunk Seam & Live Heightmap | v2.0 | 3/3 | Complete | 2026-06-25 |
| 11. Feature Pipeline & Decoration Orchestration | v2.0 | 3/3 | Complete | 2026-06-25 |
| 12. Core Feature Types | v2.0 | 3/3 | Complete | 2026-06-25 |
| 13. Trees, Dungeon & Features Visual Gate | v2.0 | 4/4 | Complete | 2026-06-25 |
| 14. Structure Pipeline & Temples | v2.0 | 3/3 | Complete | 2026-06-25 |
| 15. Mineshaft & Stronghold | v2.0 | 3/3 | Complete | 2026-06-25 |
| 16. Village Jigsaw & Structures Visual Gate | v2.0 | 3/3 | Complete | 2026-06-25 |
| 17. Gameplay Completion — the six unwired seams | v3 | 4/5 | In Progress|  |
| 18. Online-mode — auth + protocol encryption | v3 | 2/2 | Complete   | 2026-06-26 |
| 19. Operator UX — TUI console + disconnect logging | v3 | 3/3 | Complete   | 2026-06-27 |
| 20. Structure polish — loot, inhabitants, beard, persistence | v3 | 0/? | Not started | — |
