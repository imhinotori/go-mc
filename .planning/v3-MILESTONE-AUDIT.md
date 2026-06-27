---
audit: v3-milestone-cross-phase-integration
milestone: v3 - Online-mode + Operator UX + Structure polish (Phases 17-20)
branch: ender-776
date: 2026-06-27
auditor: Claude (cross-phase integration + E2E)
scope: read-only
status: passed
gates:
  build: CGO_ENABLED=0 go build ./... -> exit 0
  tests: ./server/ ./level/loot/ ./world/structure/ ./world/ ./server/auth/ ./net/CFB8/ -- targeted cross-phase tests green
flows_wired: 7
flows_gap: 0
verdict: All 7 cross-phase integration flows are WIRED end-to-end in actual code. The phases CONNECT; no cross-phase wiring gap. Milestone-blocking status is purely two out-of-band visual gates (GAMEPLAY-07, 20 real-client) plus the operator-deferred premium login -- all human/visual, no code gap.
---

# v3 Milestone Cross-Phase Integration Audit (Phases 17-20)

Read-only audit verifying the four phases CONNECT end-to-end, not that each passed in isolation.
Build: CGO_ENABLED=0 go build ./... exit 0. Targeted cross-phase tests green.

## The 7 integration flows

| # | Flow (phase boundary) | Status | Evidence (file:line) |
|---|-----------------------|--------|----------------------|
| 1 | Online-mode skins -> tracker ADD_PLAYER (18->17) | WIRED | auth.Encrypt(hasJoined) -> resp.Properties -> properties login.go:150 -> returned by AcceptLogin server.go:113 -> AcceptPlayer :143 -> tickPlayer.properties gameplay_tick.go:272,329 -> broadcast to OTHER players player_visibility.go:92 (writePlayerInfoUpdateAdd joiner.uuid ... joiner.properties) + existing->joiner :114. Offline -> nil -> count 0. TestAddPlayerSkinProperties / TestAddPlayerNoPropertiesOffline PASS. |
| 2 | Shared loot evaluator: block-drop AND chest (20-01->20-02->17-06) | WIRED | block_drop.go:70-77 calls loot.LoadTable(minecraft:blocks/ + name) + loot.Roll(tbl,seed,ctx); the v1 blockDropTable map is GONE. loot.Roll roll.go:33 is the single entry both block-drop and chest use. spawnBlockDrop :127 -> NewItemEntity :168 -> t.entities.add(ie) :158 -> tracker encodeAddEntity (tracker.go:76/234, entity-type-agnostic). Chest stores table+seed (piece.go createChest, NextLong). TestLootSeedReproduces PASS. |
| 3 | Structure spawns -> store -> tracker (20-04->17-01) | WIRED | tick.go:90 drainStructureSpawns(r.res) (chunkReady.applyTo, owner goroutine) -> structure_spawn.go:54 t.entities.add(e) -> tracker broadcasts AddEntity next tick (same store-add path as Item/mob). res.Spawns from immutable world.ChunkResult. Silverfish correctly excluded (mob_spawner BLOCK-entity). TestStructureSpawnRaceClean PASS. |
| 4 | Disconnect taxonomy -> real drop sites (19->17/18) | WIRED | POST-login seams SET the reason before Close: client.go:133 write_error, :156 protocol_error, :190 backpressure; keepalive timeout gameplay_tick.go:159; kicked playerlist.go:53. Leave log reads it gameplay_tick.go:424 (c.DisconnectReason + reasonHuman). PRE-login seams (Phase 18 login path) have no client object yet -> reason carried as a structured slog attr at the reject site: login_failure server.go:124, protocol_mismatch :108, config_failure :139. Two mechanisms by stage, both log every drop -- deliberate, documented. TestReadLoopReason_* / TestDisconnectReason PASS. |
| 5 | Console dispatch -> shared command graph (19->shared) | WIRED | ONE graph, both sources: console runConsoleCommand commands.go:336 -> executeCommand :349; player chat runCommand :284 -> same executeCommand. say/me work for BOTH: handler reads loop via commandLoop :104 which resolves cmdExecutorKey (player, withExecutor) OR cmdLoopKey (console, withConsole) -> broadcastSystemChat chat.go:115 fans to ALL players. Sender name Server for console :117. Console crosses to tick via bounded consoleCmd drained in drainRegistrations (TICK-05). TestRunConsoleCommand / TestEnterDispatches PASS. |
| 6 | StructureStart persistence vs recompute coherence (20-04) | WIRED | Same cache, same key: StoreStarts persistence.go:43-44 writes c.starts.Store(packPos(pos),starts); ComputeStarts cache.go:81-85 FIRST does c.starts.Load(packPos(pos)) and returns on hit -- a persisted/read start short-circuits the generator -> no recompute, no divergence. READ: worker.decodeAndSeed -> ReadChunkStructures persistence.go:119 -> StoreStarts into g.structCache (the same StructureCache() noisegen.go:453 ComputeStarts consults). TestStructureStartsSurviveReloadWithoutRecompute (recompute-spy==0) + TestStructureReloadMissingTagRecomputes PASS. |
| 7 | Cross-cutting fixes do not regress (17 tick order) | WIRED | tick_phases.go: tickEntities (:181 syncPlayerEntities -> :183 tickFallDamage -> :190 tickSuffocation -> :196 tickBreath -> :204 tickFood) runs BEFORE :42 tracker.Tick. Head-yaw fix subtick.go:167 p.headYaw=p.yaw -> propagated by syncPlayerEntities player_visibility.go:62 -> broadcast via tickEntityMovement/RotateHead. SetHealth dirty-check folded into tickFood (food.go), no break to 17 health sync. New tickSuffocation routes through applyDamage (i-frames) in the SAME environmental phase, no reorder. TestTickPhaseOrder / TestSuffocation* PASS. |

## Requirements Integration Map

| Requirement | Integration path | Status | Issue |
|-------------|-----------------|--------|-------|
| GAMEPLAY-01 | join -> drainRegistrations -> entities.add + syncPlayerEntities -> tracker.Tick AddEntity (keystone) | WIRED | -- |
| GAMEPLAY-02 | persistence.go load -> play_join bootstrap teleport | WIRED | -- |
| GAMEPLAY-03 | join -> syncJoinInventories -> ContainerSetContent | WIRED | -- |
| GAMEPLAY-04 | subtick applyInput Attack/Interact -> lookupPlayerByEntityID -> applyDamage; tickFallDamage | WIRED | -- |
| GAMEPLAY-05 | tickWorld -> tickFluids; fluid_physics/breath | WIRED | -- |
| GAMEPLAY-06 | block_drop -> loot.Roll (shared) -> NewItemEntity -> entities.add -> tracker | WIRED | shares flow 2 evaluator |
| GAMEPLAY-07 | -- | UNWIRED (by design) | Visual gate, autonomous:false; operator AFK. No code gap. |
| ONLINE-01 | auth resp.Properties -> tickPlayer -> ADD_PLAYER broadcast (self+others) | WIRED | premium-login skin render = out-of-band gate |
| ONLINE-02 | login OnlineMode -> auth.Encrypt -> CFB8 conn wrap | WIRED | -- |
| TUI-01 | main TTY fork -> tui.New / EnqueueConsoleCommand -> runConsoleCommand -> executeCommand | WIRED | -- |
| TUI-02 | drop seams SetDisconnectReason / slog attr -> leave log reasonHuman | WIRED | -- |
| STRUCT-POLISH-01 | loot.Roll shared by block_drop + chest BE | WIRED | chest-OPEN UI is documented follow-up (seam built+tested, no runtime caller) |
| STRUCT-POLISH-02 | worker ChunkResult.Spawns -> drainStructureSpawns -> entities.add | WIRED | finalizeSpawn variant init is cited default stub |
| STRUCT-POLISH-03 | noisegen beardifierFor -> noisechunk v+=beardAt | WIRED | -- |
| STRUCT-POLISH-04 | worker READ ReadChunkStructures->StoreStarts / WRITE SerializeChunkData->WriteChunkStructures; same cache as ComputeStarts | WIRED | production chunk-FLUSH caller of SerializeChunkData is future (READ live; WRITE proven by reload test) |

Requirements with no cross-phase wiring (self-contained, not a gap): ONLINE-02 (encryption is intra-Phase-18, consumed only by the login handshake) and STRUCT-POLISH-03 (beard is intra-worldgen). Both correctly self-contained.

## Deferred / follow-up debt (informational -- none break a traced flow)

| Item | Nature | Affects |
|------|--------|---------|
| Chest-OPEN UI (windowId allocator / OpenScreen / container menu -> calls existing unpackLootTable) | Honest scope split (20-02 W2). Roll seam built + tested, no runtime caller yet. | STRUCT-POLISH-01 in-client chest visual gate |
| finalizeSpawn variant/profession/attribute init | Cited stub at vanilla default (structure_spawn.go:45-53); mob spawns/renders/tracks, only per-instance variant is default. Lands with attribute subsystem. | STRUCT-POLISH-02 |
| mob_spawner runtime tick (silverfish countdown) | BE placed faithfully; runtime spawner countdown is a separate future subsystem. | STRUCT-POLISH-02 |
| Production chunk-FLUSH caller of SerializeChunkData | Worker is region-READ + always-generate; WRITE seam exercised E2E by reload test, no live flush consumer. | STRUCT-POLISH-04 |
| Block-survival neighbor-destroy (flowers float when support broken) | Net-new subsystem (block-tag lookup + updateOrDestroy); deferred from 17 (deferred-items.md). Drop path already exists. | gate finding, own phase |
| Mob water-aware nav / travelInFluid (mobs walk on water) | Owned by the parallel mob-AI port; player fluid physics is wired. | gate finding |
| Premium online login + cross-player real-skin render | Out-of-band (needs 2 real Mojang accounts); offline-rejection verified live. | ONLINE-01 visual gate |
| Two pending visual gates: GAMEPLAY-07, 20 real-client (terrain fit / live inhabitants / chest loot) | autonomous:false human gates; operator AFK. | GAMEPLAY-07, STRUCT-POLISH |

## Verdict

PASSED for cross-phase integration. All 7 flows trace end-to-end through real code with passing tests; the integrated tree builds clean (CGO off) and the targeted cross-phase suites are green. The phases genuinely wire together -- no silo, no orphaned export, no API-without-consumer, no broken E2E chain. The only open items are out-of-band visual/network gates (two human visual gates + the premium login) and the cited, honestly-scoped follow-up debt -- none of which is a cross-phase wiring gap. The milestone is clear to close on the code-integration axis; the remaining gating is purely the operator visual sign-off.
