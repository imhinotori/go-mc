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
| 17. Gameplay Completion — the six unwired seams | v3 | 0/? | Not started | — |
| 18. Online-mode — auth + protocol encryption | v3 | 0/? | Not started | — |
| 19. Operator UX — TUI console + disconnect logging | v3 | 0/? | Not started | — |
| 20. Structure polish — loot, inhabitants, beard, persistence | v3 | 0/? | Not started | — |
