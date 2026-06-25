# Roadmap: Sulfur — Minecraft Java 26.2 (protocol 776) server

## Milestones

- ✅ **v1.0 MVP** — Phases 1–9 (shipped 2026-06-24) — full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied noise world with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md)
- ✅ **v2.0 Worldgen Features + Structures** — Phases 10–16 (shipped 2026-06-25) — the overworld now looks + reads like vanilla: full per-biome vegetation (trees/grass/flowers/ores) + the emblematic structures (temples, mineshafts, strongholds, villages) in vanilla positions, deterministic per seed, ported 1:1 from the jar. → [archive](milestones/v2-ROADMAP.md) · [requirements](milestones/v2-REQUIREMENTS.md)
- 🚧 **v3 Online-mode + Operator UX + Structure polish** — Phases 17–20 (in progress) — first the world becomes *actually* playable on a real client (the six unwired gameplay seams), then authenticated/encrypted online-mode logins, a bubbletea TUI console + disconnect logging, and the structures finished (loot, inhabitants, terrain-beard, NBT persistence). REGION-01 Folia regionization deferred to v4.

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
- [ ] **Phase 18: Online-mode — auth + protocol encryption** (ONLINE-01/02) — EncryptionRequest/Response RSA key exchange + AES-128/CFB8 stream encryption (hand-rolled CFB8 over stdlib AES, no new dep) + Yggdrasil `hasJoined` session-server verification, behind an `online-mode` config flag.
- [ ] **Phase 19: Operator UX — TUI console + disconnect logging** (TUI-01/02) — a bubbletea+bubbles terminal console (command-input + live log viewport) that degrades to plain logging when stdout is not a TTY, plus disconnect-reason logging (kick/timeout/protocol/quit/login-fail).
- [ ] **Phase 20: Structure polish — loot, inhabitants, beard, persistence** (STRUCT-POLISH-01..04) — the documented v2 deferrals: loot tables (chests + block drops, shared evaluator with GAMEPLAY-06), structure entities (villagers/witch/cat/silverfish), `afterPlace` terrain-beard, and structure-start NBT persistence.

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
- [ ] 17-05-PLAN.md — GAMEPLAY-07: real-client multiplayer VISUAL GATE (autonomous:false, wave 3)
**Research**: HANDOFF.md documents all six seams with file:line evidence + classification + build order (GAMEPLAY-01 keystone first; GAMEPLAY-05 fluid sim is the one large net-new jar port). Phase research deep-dives the `FlowingFluid` port.

### Phase 18: Online-mode — auth + protocol encryption
**Goal**: Sulfur runs in online-mode — authenticated, encrypted logins with real Mojang/Microsoft UUIDs, skins, and ownership verification — behind an `online-mode` config flag (offline remains the default for local dev).
**Depends on**: Phase 17
**Requirements**: ONLINE-01, ONLINE-02
**Success Criteria** (what must be TRUE):
  1. The EncryptionRequest/EncryptionResponse handshake is ported: RSA-OAEP key exchange of the 16-byte shared secret + verify token (ONLINE-02)
  2. AES-128/CFB8 stream encryption wraps the connection for all packets after the handshake — CFB8 hand-rolled over stdlib `crypto/aes` (Go stdlib dropped `cipher.CFB`), no new crypto dependency (ONLINE-02)
  3. Yggdrasil `hasJoined` session-server verification: the server verifies the shared-secret-derived server hash against `sessionserver.mojang.com` → real UUID + skin properties (ONLINE-01)
  4. An `online-mode` config flag toggles auth/encryption; offline-mode (default) is unchanged
**Plans**: TBD (set by /gsd-plan-phase)
**Research**: Phase research covers the Yggdrasil auth flow + the protocol-776 encryption packet shapes + the CFB8 hand-roll.

### Phase 19: Operator UX — TUI console + disconnect logging
**Goal**: The operator runs + watches Sulfur from a proper terminal console — command input + live scrolling logs in one screen — with every player drop logged with its reason.
**Depends on**: Phase 18
**Requirements**: TUI-01, TUI-02
**Success Criteria** (what must be TRUE):
  1. A charmbracelet bubbletea+bubbles console renders a command-input zone (textinput) + a live log viewport; typed commands dispatch through the existing command system (TUI-01)
  2. The TUI degrades gracefully when stdout is not a TTY (headless/Docker → plain structured logging, no TUI) (TUI-01)
  3. Every player disconnect logs WHY (kick/timeout/protocol error/clean quit/login failure) with player identity + reason, surfaced in the TUI log stream + structured logs (TUI-02)
**Plans**: TBD (set by /gsd-plan-phase)
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
| 18. Online-mode — auth + protocol encryption | v3 | 0/? | Not started | — |
| 19. Operator UX — TUI console + disconnect logging | v3 | 0/? | Not started | — |
| 20. Structure polish — loot, inhabitants, beard, persistence | v3 | 0/? | Not started | — |
