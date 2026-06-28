# Requirements Archive: v3 Online-mode + Operator UX + Structure polish

**Archived:** 2026-06-28
**Status:** SHIPPED

For current requirements, see `.planning/REQUIREMENTS.md`.

---

# Requirements: Sulfur v3 — Online-mode + Operator UX + Structure polish

**Defined:** 2026-06-25
**Milestone:** v3 (continues phase numbering from v2's Phase 16 → Phase 17+)
**Core Value:** Sulfur becomes a real online-mode server an operator can run + watch — first the world is *actually* playable on a real client (the v1 "fully playable" claim had unwired seams), then authenticated/encrypted logins, a proper TUI console, and the structures finished (loot, inhabitants, terrain-fit, persistence).

**Scope basis:** A real vanilla 26.2 client connected to the shipped v1/v2 server and surfaced six gameplay defects (players invisible to each other, position not loaded, inventory empty on join, no damage, no fluid simulation, no block drops). Investigation (3 Explore agents, file:line evidence in HANDOFF.md) found the **same pattern**: the core logic exists + is unit-tested, but the final wiring SEAM (join-sync / input-dispatch / broadcast / tick-wiring) was never connected — v1 closed on isolated-function tests, not end-to-end with a real client. A non-playable server makes online-mode pointless, so **Phase 17 (gameplay completion) lands FIRST**, before online-mode/TUI/structure-polish.

**Mandate (carried from v1/v2):** PORT gameplay/worldgen/structure LOGIC directly from the decompiled jar (`temp/cache/26.2-inner.jar`, javap -c / CFR — idiomatic Go, no GPL paste, cite the class); EMBED the data the jar ships (`//go:embed`); determinism per seed where vanilla is deterministic; `CGO_ENABLED=0` stays clean (pure-Go static binary); push to `development`.

## v3 Requirements

The "user" is (a) an unmodified vanilla 26.2 (protocol 776) client playing the world, and (b) the server operator running + watching the process.

### Gameplay completion — the six unwired seams (Phase 17, FIRST)

- [x] **GAMEPLAY-01**: Players are visible to each other. Each joining player is added to the entity store as an `entity.Player` (ID 156) and its position synced every tick, so the existing entity tracker (`server/tracker.go`) broadcasts AddEntity / move / remove to other in-range players. Fix seam: `server/tick.go` `drainRegistrations()` → `t.entities.add(p)` + per-tick pos sync. _(Keystone — GAMEPLAY-06 and any visible-entity work depend on the player/entity broadcast path working.)_
- [x] **GAMEPLAY-02**: Player position persists across sessions. `snapshotPlayer`/`savePlayer` already write x/y/z (round-trip tested); the join bootstrap must APPLY the persisted position instead of hardcoding spawn. Fix seam: `server/gameplay_tick.go` (the "NOT applied for v1" skip) + `server/play_join.go` bootstrap teleport reads the loaded pos.
- [x] **GAMEPLAY-03**: Inventory works end-to-end. The handlers (ContainerClick / SetCreativeModeSlot / SetCarriedItem) exist + are tested; the initial `ContainerSetContent` is sent on join so the client's window is populated. Fix seam: call `sendContent(p)` on the first tick after `AcceptPlayer`.
- [x] **GAMEPLAY-04**: Damage works. `applyDamage`/`die`/`performRespawn`/`setHealth` exist + are tested but are only reachable via `SULFUR_DEBUG_DAMAGE`. `applyInput()` (`server/subtick.go`) gains real cases for ServerboundAttack/Interact that resolve the target + call `applyDamage` (currently they hit the no-op `default:`). Plus an environmental-damage tick (fall damage at minimum).
- [x] **GAMEPLAY-05**: Water simulates. `tickWorld()` (`server/tick_phases.go`) is an empty stub — port vanilla `FlowingFluid`/`LiquidBlock` flow propagation + fluid-level updates + waterlogged handling, and player fluid physics (swim/buoyancy, slower movement, breath). Largest of the six.
- [x] **GAMEPLAY-06**: Broken blocks drop items. After the break sets air (`server/block_interact.go`), look up the block's loot (block loot table), spawn an `Item` entity (ID 71) for the drop, and track it so GAMEPLAY-01's broadcast path sends AddEntity to clients. Overlaps STRUCT-POLISH loot-table work (block loot tables).
- [x] **GAMEPLAY-07**: A real vanilla 26.2 client confirms two players see each other move, positions/inventory survive a reconnect, attacks deal damage + death/respawn, water flows + affects movement, and broken blocks drop pickable items (VISUAL GATE). _(Operator-confirmed live 2026-06-27 across the 12-bug fidelity sweep — move/reconnect/damage/water/drops + chest open/drag + skins all validated on a real 26.2 client.)_

### Online-mode — authenticated, encrypted logins

- [x] **ONLINE-01**: Mojang/Microsoft account authentication (Yggdrasil "hasJoined" session-server verification). On login the server sends the encryption request, the client authenticates against `sessionserver.mojang.com`, and the server verifies the shared-secret-derived server hash → real UUIDs, skins, ownership verification. The authenticated texture/skin properties must propagate to the tab-list `PlayerInfoUpdate(ADD_PLAYER)` so OTHER players render the real skin (not just the self profile). Toggled by an `online-mode` config flag (offline remains the default for local dev).
- [x] **ONLINE-02**: Protocol encryption. The EncryptionRequest/EncryptionResponse handshake (RSA `RSA/ECB/PKCS1Padding` = PKCS#1 v1.5 key exchange of the 16-byte shared secret + 4-byte verify token, per `net.minecraft.util.Crypt` — NOT OAEP), then AES-128/CFB8 stream encryption on the connection for all subsequent packets. CFB8 implemented over stdlib `crypto/aes` (Go stdlib dropped `cipher.CFB`, so a small hand-rolled CFB8 mode) — no new crypto dependency. The 776 `ClientboundHelloPacket` carries a trailing `shouldAuthenticate` boolean after the verify token.

### Operator UX — TUI console + disconnect logging

- [x] **TUI-01**: A terminal console (charmbracelet **bubbletea** + **bubbles**) with a command-input zone (textinput) + a live scrolling log viewport — the operator types server commands and watches structured live logs in one screen, instead of raw stdout. Degrades gracefully when stdout is not a TTY (headless/Docker → plain structured logging, no TUI). _(19-01 backbone: Model + log bridge + deps; 19-02 main() TTY fork + console dispatch. Operator-validated: TUI renders, typed commands route through the existing graph, join/leave lines stream, headless stays plain.)_
- [x] **TUI-02**: Disconnect-reason logging. Every player drop logs WHY (kick / timeout / protocol error / clean quit / login failure) with player identity + reason, surfaced in the TUI log stream and the structured logs.

### Structure polish — the documented v2 deferrals

- [x] **STRUCT-POLISH-01**: Loot tables. Port the loot-table system (the embedded loot-table JSON + the function/condition/number-provider evaluation) and populate structure + dungeon chests (mineshaft, temples, igloo basement, village, dungeon) — ties to GAMEPLAY-06's block loot tables (shared evaluator).
- [x] **STRUCT-POLISH-02**: Structure entities. The inhabitant spawns deferred in v2 — villagers (village), witch (swamp hut), cat (village/swamp), silverfish (stronghold) — spawn with the structure via the entity store.
- [x] **STRUCT-POLISH-03**: `afterPlace` terrain-beard. Structures adapt to terrain (the beard/terrain-adaptation pass) instead of floating/clipping — port the `afterPlace`/`BeardifierStructureBlock` adaptation.
- [x] **STRUCT-POLISH-04**: Structure-start NBT persistence. Persist computed `StructureStart`s to region NBT (the v2 "recompute-on-demand" decision is correct for correctness but persists nothing) — write/read the structure-start tags so starts survive without recompute.

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| **REGION-01 — Folia-style per-region tick threading** | The big async-parallelism milestone; deferred to **v4** so v3 stays focused on playability + operability. Re-confirmed by the user when scoping v3. |
| **Plugin system (Bukkit/Spigot/Paper API)** | User-specified scope boundary for the whole project — server core, not a plugin platform. |
| **Bedrock / cross-platform protocol** | Java Edition only; go-mc is Java-protocol. |
| **Microsoft device-code / launcher auth flow** | ONLINE-01 verifies the client's existing Mojang session (server side); the server does not perform the client's MSA login. |

## Traceability

Updated during roadmap creation (`/gsd-plan-phase`).

| Requirement | Phase | Status |
|-------------|-------|--------|
| GAMEPLAY-01 | Phase 17 | Complete |
| GAMEPLAY-02 | Phase 17 | Complete |
| GAMEPLAY-03 | Phase 17 | Complete |
| GAMEPLAY-04 | Phase 17 | Complete |
| GAMEPLAY-05 | Phase 17 | Complete |
| GAMEPLAY-06 | Phase 17 | Complete |
| GAMEPLAY-07 | Phase 17 | Complete |
| ONLINE-01 | TBD | Complete |
| ONLINE-02 | TBD | Complete |
| TUI-01 | 19-01 (backbone), 19-02/03 (wiring) | Complete |
| TUI-02 | TBD | Complete |
| STRUCT-POLISH-01 | 20-01 (evaluator), 20-02 (block drops + lazy chest store + unpackLootTable seam), 20-06 (chest-OPEN UI: right-click -> BE resolve -> lazy roll -> OpenScreen generic_9x3 + ContainerSetContent + clicks/close) | Complete |
| STRUCT-POLISH-02 | TBD | Complete |
| STRUCT-POLISH-03 | TBD | Complete |
| STRUCT-POLISH-04 | 20-03 (seam + tests); runtime-wired in the gap closure (worker.decodeAndSeed read / world.SerializeChunkData write + reload integration test) | Complete |

**Coverage:**
- v3 requirements: 15 total (7 GAMEPLAY + 2 ONLINE + 2 TUI + 4 STRUCT-POLISH)
- Mapped to phases: 7 (Phase 17 — gameplay completion)
- Unmapped: 8 ⚠️ (online/TUI/structure-polish phases assigned at roadmap time)

---
*Requirements defined: 2026-06-25 — v3 milestone (gameplay-completion first, then online-mode/TUI/structure-polish; REGION-01 deferred to v4).*
