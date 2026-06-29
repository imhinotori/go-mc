# Roadmap: Sulfur — Minecraft Java 26.2 (protocol 776) server

## Milestones

- ✅ **v1.0 MVP** — Phases 1–9 (shipped 2026-06-24) — full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied noise world with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md)
- ✅ **v2.0 Worldgen Features + Structures** — Phases 10–16 (shipped 2026-06-25) — the overworld now looks + reads like vanilla: full per-biome vegetation (trees/grass/flowers/ores) + the emblematic structures (temples, mineshafts, strongholds, villages) in vanilla positions, deterministic per seed, ported 1:1 from the jar. → [archive](milestones/v2-ROADMAP.md) · [requirements](milestones/v2-REQUIREMENTS.md)
- ✅ **v3 Online-mode + Operator UX + Structure polish** — Phases 17–20 (shipped 2026-06-27) — the world became *actually* playable on a real client (the six unwired gameplay seams), then authenticated/encrypted online-mode logins, a bubbletea TUI console + disconnect logging, the structures finished (loot, inhabitants, terrain-beard, NBT persistence), and a live 12-bug fidelity sweep (chest/water/cane/hats/persist/swim/oxygen) all operator-confirmed in-game. REGION-01 Folia regionization deferred to v4. → [archive](milestones/v3-ROADMAP.md) · [requirements](milestones/v3-REQUIREMENTS.md) · [audit](milestones/v3-MILESTONE-AUDIT.md)
- ✅ **v4 Plugin / Scripting System** — Phases 21–28 (shipped 2026-06-29) — a dual-runtime extension API: **Starlark** (`go.starlark.net`, pure-Go, CGO=0 preserved, sandboxed) as the hot-path core + an **opt-in Python** runtime (`qur/gopy` @ `python3.14`, behind a `python` build tag so the default binary stays pure-Go static). Plugins DECLARE behavior loaded once; Go runs the hot path calling declared hooks. Dogfood-validated: vanilla pig rewritten AS a 1:1-jar plugin (behavior-identical to the Go oracle, the only pig) + crafting built THROUGH the plugin API (recipe-provider + 3×3 menu). Folia regionization (REGION-01) folded in (2 parallel regions, -race clean). Closed by a bot-driven visual gate + an automated perf gate (plugin tax ~1.7–4.2%/mob·tick). 8/8 reqs, 7/7 integration chains, 4/4 gate flows. → [archive](milestones/v4-ROADMAP.md) · [requirements](milestones/v4-REQUIREMENTS.md) · [audit](milestones/v4-MILESTONE-AUDIT.md)

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

<details>
<summary>✅ v4 Plugin / Scripting System (Phases 21–28) — SHIPPED 2026-06-29</summary>

- [x] **Phase 21: Starlark runtime foundation** (PLUGIN-01) — completed 2026-06-28
- [x] **Phase 22: Plugin host + event bus** (PLUGIN-02) — completed 2026-06-28
- [x] **Phase 23: Entity/mob behavior API** (PLUGIN-03) — completed 2026-06-28
- [x] **Phase 24: Vanilla mobs AS plugins (1:1 dogfood)** (PLUGIN-04) — completed 2026-06-28
- [x] **Phase 25: Crafting/recipes AS plugins (2nd-domain dogfood)** (PLUGIN-05) — completed 2026-06-28
- [x] **Phase 26: Opt-in Python runtime** (PLUGIN-06) — completed 2026-06-28
- [x] **Phase 27: Folia regionization** (REGION-01) — completed 2026-06-28
- [x] **Phase 28: Plugin system visual + perf gate** (PLUGIN-07) — completed 2026-06-29

Full phase details: [milestones/v4-ROADMAP.md](milestones/v4-ROADMAP.md).

</details>

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1–9 (v1) | v1.0 | — | Complete | 2026-06-24 |
| 10–16 (v2.0 worldgen + structures) | v2.0 | 22/22 | Complete | 2026-06-25 |
| 17–20 (v3 online-mode + operator-UX + structure-polish) | v3 | 33/33 | Complete | 2026-06-27 |
| 21. Starlark runtime foundation | v4 | 2/2 | Complete   | 2026-06-28 |
| 22. Plugin host + event bus | v4 | 2/2 | Complete   | 2026-06-28 |
| 23. Entity/mob behavior API | v4 | 2/2 | Complete   | 2026-06-28 |
| 24. Vanilla mobs AS plugins (1:1 dogfood) | v4 | 2/2 | Complete   | 2026-06-28 |
| 25. Crafting/recipes AS plugins (2nd-domain dogfood) | v4 | 3/3 | Complete   | 2026-06-28 |
| 26. Opt-in Python runtime | v4 | 3/3 | Complete   | 2026-06-28 |
| 27. Folia regionization | v4 | 3/3 | Complete   | 2026-06-28 |
| 28. Plugin system visual + perf gate | v4 | 2/2 | Complete   | 2026-06-29 |

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
