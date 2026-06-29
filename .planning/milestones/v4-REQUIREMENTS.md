# Requirements Archive: v4 Plugin / Scripting System

**Archived:** 2026-06-29
**Status:** SHIPPED

For current requirements, see `.planning/REQUIREMENTS.md`.

---

# Requirements: Sulfur v4 — Plugin / Scripting System

**Defined:** 2026-06-27
**Milestone:** v4 (continues phase numbering from v3's Phase 20 → Phase 21+)
**Plan of record:** `.planning/v4-PLAN.md` (full per-phase breakdown, runtime decisions, architecture, build-order rationale).
**Core Value:** Sulfur's gameplay becomes scriptable through a dual-runtime extension API **without giving up the two things that define the project** — the pure-Go static binary (CGO_ENABLED=0) and the 1:1-with-the-jar gameplay mandate. The API is dogfood-validated across TWO domains: vanilla mobs rewritten AS plugins (jar-faithful) and crafting built THROUGH the plugin API. The same API enables fully custom (non-vanilla) mobs and recipes.

**Architecture (user-decided):** Plugins DECLARE behavior loaded once; Go executes the hot path (physics/pathfinding/tick) calling plugin hooks at declared seams — NOT a script per-entity-per-tick — but a plugin CAN fully override a mob's behavior when desired. The interpreter runs at load + on events + on cached decisions, never 200-mobs×20-TPS interpreter calls/sec.

**Mandate (carried from v1–v3):** the 1:1 jar-port mandate carries INTO the plugin layer — vanilla-mob plugins (Phase 24) and the crafting recipe/consume logic (Phase 25) stay literal method-for-method ports of the unobfuscated 26.2 jar (`temp/cache/26.2-inner.jar`, `javap -c -p` / CFR), cited, re-expressed in idiomatic Starlark (no GPL paste). Custom (non-vanilla) plugins are free of the mandate by definition. `CGO_ENABLED=0` stays clean for the DEFAULT binary (Python is build-tag-gated). TICK-05 single-owner / Docker `-race` clean carries into the plugin call seam. Push to `development`.

**Scope inversion:** This milestone INVERTS the original "Server core only — no plugin/extension API" boundary. Intentional and user-directed; PROJECT.md + CLAUDE.md scope lines updated at v4 kickoff.

## v4 Requirements

The "user" is now (a) a **plugin author** writing `.star` (or, opt-in, Python) plugins against the Sulfur API, and (b) the unmodified vanilla 26.2 client + the operator, who must observe identical gameplay whether logic is Go-native or plugin-driven.

### Runtime + host (Phases 21–22)

- [x] **PLUGIN-01**: A Starlark runtime is embedded (`go.starlark.net`, pure-Go, CGO_ENABLED=0 preserved). A `.star` file loads, parses, and compiles once; runs inside a sandbox (per-`starlark.Thread` step-counter budget, recursion OFF, no filesystem/network builtins unless explicitly exposed); one `starlark.Thread` per goroutine with FrozenValues shared safely across the tick boundary; a plugin can call a registered Go builtin and return a value — all `-race` clean. The plugin load/parse/compile lifecycle exists. (Riskiest single item: proves sandbox + CGO=0 + race-safety before anything is built on top.)
- [x] **PLUGIN-02**: A plugin host + typed event bus. The plugin manager discovers/loads/unloads plugins from a plugins directory (with a manifest); a typed event system fires the core gameplay events (tick, player join/leave, block break/place, entity spawn/death, damage); a plugin registers its hooks ONCE at load via a registration API; the Go→plugin dispatch seam stays OFF the per-entity hot path (event-driven + cached, not per-tick-per-entity scan). A plugin subscribes to an event and its hook fires on the real tick.

### Entity/mob behavior + 1:1 dogfood (Phases 23–24)

- [x] **PLUGIN-03**: A declarative entity/mob behavior API. A plugin DECLARES a mob's attributes/goals/AI once (loaded at parse time); Go runs the hot path (physics/pathfinding/tick/collision) calling the declared hooks; a FULL-OVERRIDE path lets a plugin replace a mob's whole decision logic. The Go-side bridge exposes the entity/world/nav API to Starlark as FROZEN, tick-owned-safe handles (read-only vs mutate-through-a-tick-owned-seam distinguished). A trivial custom mob declared in Starlark spawns, ticks, and moves via the Go nav, `-race` clean.
- [x] **PLUGIN-04**: The vanilla mobs + entity logic are rewritten AS Starlark plugins that remain a **literal 1:1 port of the 26.2 jar** (the mandate carries into the plugin layer). This validates the API expresses real vanilla AI. The plugin-driven vanilla mob is behavior-identical to the Go-native path it replaces, jar-verified against bytecode, with the existing mob-AI tests green. (FIRST dogfood — if vanilla AI doesn't fit the API, the API is wrong, caught here before Python.)

### Crafting/recipes — 2nd-domain dogfood (Phase 25)

- [x] **PLUGIN-05**: Crafting is built THROUGH the plugin API, not as a hardcoded Go subsystem. A recipe-provider plugin loads the jar-extracted recipes (shaped/shapeless/smelting/…) and drives the crafting-grid result + the `ResultSlot.onTake` consumption (today a no-op stub — `inventory_click.go`: "no recipes wired in v1"). Includes the `crafting_table` block + the 3×3 menu (the core only had the 2×2 inventory grid). Vanilla recipes craft correctly through the plugin path (result + consume, jar-verified vs `RecipeManager`/`CraftingMenu`) AND a custom recipe works. Proves the API generalizes to a SECOND domain (recipes/menus, not just entity AI).

### Opt-in Python runtime (Phase 26)

- [x] **PLUGIN-06**: An opt-in Python runtime (`qur/gopy` @ branch `python3.14`, module `gopython.xyz/py/v14` — corrected from `/py/v3` which is the old 3.11 line, per Phase-26 research; idiomatic CPython bindings via cgo + libpython) behind a `python` build tag, so the DEFAULT (no-tag) build stays pure-Go static (CGO=0). It is for HEAVY off-tick plugins ONLY (never the per-tick hot path — cgo overhead + GIL + non-determinism), running on their own goroutines/pools and rejoining via the existing async seam (`asyncIn2`/ants). Same event/registration API as Starlark (author picks the runtime per workload, not per API). A Python plugin runs off-tick, rejoins via the async seam; the default build is still pure-Go static.

### Regionization (Phase 27)

- [x] **REGION-01**: Folia-style per-region tick threading (folded from the v3 deferral). The world ticks in independent parallel regions; the plugin call seam + the entity API become region-aware (a plugin hook runs on its region's thread). The world ticks in parallel regions, `-race` clean, plugin hooks run on the correct region thread. (Regionize a working single-thread seam — don't design the seam around regions first.)

### Gate (Phase 28)

- [x] **PLUGIN-07**: A real vanilla 26.2 client + a perf benchmark confirm: a custom non-vanilla mob plugin works, the vanilla-mobs-as-plugins path is behavior-identical to Go-native, crafting (vanilla + a custom recipe) works through the plugin path, the event system fires correctly, and the plugin layer adds NO measurable per-tick cost vs Go-native (Folia regions scale). (VISUAL + PERF GATE, autonomous:false — closes v4.)

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| **Python as the DEFAULT/core runtime** | cgo/libpython would break the CGO=0 static-binary value prop; Python is opt-in build-tag-gated, off-tick only. Starlark is the core. |
| **Per-entity-per-tick scripting** | Explicitly rejected by the architecture — plugins declare once + Go runs the hot path; the interpreter does not run per-entity-per-tick. |
| **Bukkit/Spigot/Paper API compatibility** | This is a NEW Sulfur-native plugin API (Starlark/Python), not a reimplementation of the Java plugin ABI. |
| **Hot-reload without restart (maybe)** | Nice-to-have; scope decided at Phase 22 — not a committed requirement. |
| **Third-party plugin marketplace / distribution infra** | Out of milestone; the manifest/dir format lands in Phase 22 but distribution tooling does not. |
| **Bedrock / cross-platform** | Java Edition only (unchanged from prior milestones). |

## Prerequisite subsystems — DONE (v3.1, landed before v4)

The v4 plan flagged five core vanilla subsystems as prerequisites. All landed in parallel worktrees during v3.1 (see the v3 ROADMAP "Deferred / Backlog"), 1:1 from the jar, `-race` clean — v4 builds on them, does NOT re-do them:

- **SUB-PERSIST** — chunk-save / RunSaveLoop ✅
- **SUB-ITEMNBT** — ItemStack disk codec ✅ (Phase B component transcoder = small follow-up)
- **SUB-BLOCKTICK** — scheduled block ticks (`LevelTicks`) ✅
- **SUB-FACESTURDY** — per-face block support shapes ✅
- **SUB-ATTRIB** — attribute system ✅ (the Phase 23/24 mob plugins read real attributes)

## Open decisions to resolve at phase kickoff (NOT now)

- **Starlark fork-or-vendor** (Phase 21) — likely a plain dep, not a fork; confirm whether any sandbox patch upstream doesn't expose is needed.
- **Plugin distribution format** (Phase 22) — single `.star` vs manifest-dir; lean manifest-dir for versioning + the runtime selector.
- **The frozen-handle API surface** (Phase 23) — exactly which entity/world/nav ops are read-only vs mutate-through-a-tick-owned-seam (hardest design question).
- **Hot-reload** (Phase 22) — unload/reload without restart; decide scope.
- **Capability/permission model** (Phase 22) — does a plugin declare what it can touch; security-relevant for third-party plugins.

## Traceability

Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| PLUGIN-01 | Phase 21 | Complete |
| PLUGIN-02 | Phase 22 | Complete |
| PLUGIN-03 | Phase 23 | Complete |
| PLUGIN-04 | Phase 24 | Complete |
| PLUGIN-05 | Phase 25 | Complete |
| PLUGIN-06 | Phase 26 | Complete |
| REGION-01 | Phase 27 | Complete |
| PLUGIN-07 | Phase 28 | Complete |

**Coverage:**
- v4 requirements: 8 total (7 PLUGIN + 1 REGION)
- Mapped to phases: 8 (one requirement per phase, 21–28)
- Unmapped: 0

---
*Requirements defined: 2026-06-27 — v4 milestone (Plugin/Scripting System; Starlark core + opt-in Python; vanilla-mobs + crafting dogfooded through the API; Folia regionization folded in; the "no plugin API" scope intentionally inverted). Plan of record: v4-PLAN.md.*
