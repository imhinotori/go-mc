# 17-05 SUMMARY — GAMEPLAY-07: real-client multiplayer visual gate (1:1 close-out)

> Closes Phase 17. GAMEPLAY-01..06 landed in 17-01..04 + the gap-closure plans 17-06..21.
> This summary covers the FINAL 1:1 work that closes GAMEPLAY-07: the eating loop (17-22),
> the real-client polish session (cave-water gap, the three entity-visibility audits), and the
> two production bugs caught + fixed while validating with two clients (the join-disconnect and
> the worldgen race). The visual gate itself is autonomous:false — human-verified on a real client.

## What landed (commits on `ender-776`)

| Area | Commit(s) | 1:1 source |
|------|-----------|-----------|
| **Eating loop** (17-22) | eb90b554 / 00bf1d03 | startUsingItem→completeUsingItem→FoodData.eat + jar-extracted per-item food (GenItemFood.java). Held right-click on food runs the use-duration then refills hunger + shrinks the stack. |
| **Cave-water gap fix** | 7831b5e7 | NoiseBasedChunkGenerator.fillFromNoise markPosForPostProcessing + LevelChunk.postProcessGeneration one-shot FluidState.tick. The SAFE replacement for the scan-and-schedule that cascaded twice. Restores the aquifer `shouldScheduleFluidUpdate` flag (was baked away). Generated cave/aquifer water now flows into a bordering air gap on chunk load — without the cascade. |
| **Fluid cost instrumentation** | 09c1e54b | ULTRA[fluid] cost line per gametick (cells processed + queue depth) so the async-fluid optimization decision is data-driven, not blind. |
| **Audit 1 — swing + held-item** | 3fef2212 | ServerboundSwing→ClientboundAnimate (LivingEntity.swing, action MAIN=0/OFF=3) + held-item→ClientboundSetEquipment (LivingEntity.detectEquipmentUpdates, per-tick mainhand compare). Both broadcast to trackers, not self. New tickEquipment phase + broadcastToTrackers + encodeAnimate/encodeSetEquipment. |
| **Audit 3 — eat/use pose** | b0475202 | DATA_LIVING_ENTITY_FLAGS (index 8, BYTE) IS_USING bit synced on startUsingItem/stopUsingItem (LivingEntity.setLivingEntityFlag). Observers now see the eat pose. Extends the air-sync metadata pattern. |
| **Audit 2 — delta-move** | 64f778ce | ServerEntity.sendChanges 1:1 — per-entity delta MoveEntityPos/PosRot/Rot (~6 bytes) or absolute EntityPositionSync, replacing the per-tick absolute TeleportEntity (5-10× bandwidth + stutter). New tickEntityMovement phase; packDegrees (floor) for the move byte-angle; per-entity send state on Entity. |
| **AI test deflake** | 4f4041cc | TestTickAIDrivesMobs: the wait-for-path branch used a non-blocking default despite the comment; replaced with a real timed receive. Was ~1/3 flake under -race contention. |
| **Join-disconnect fix** | 471b232b | Two clients in view disconnected on join: the bounded outbound queue (256) overflowed at join under the chunk flood once the per-tick visibility traffic piled on. RotateHead now matches ServerEntity 1:1 (head-yaw threshold, not every move — halves move volume); outboundCap 256→4096 absorbs the join spike. testbot got strict SetEquipment/SetEntityData validators (confirmed the encoders were byte-correct — it was queue overflow, not a framing bug). |
| **Worldgen race crash** | a807ff9f | `fatal error: concurrent map read and map write` in SurfaceSystem.surfaceNoiseValue — the SurfaceSystem (shared across parallel chunk-gen goroutines) had two unsynchronized lazy maps (noiseCache, randomFactoryCache). Added cacheMu. Docker -race ./world/ green. |
| **Block-place entity-collision** | 74f1c9e1 | BlockItem.canPlace → Level.isUnobstructed: a placement whose full-cube shape overlaps a blocksBuilding entity is rejected (a player could place a block inside another player/mob). v1 subset: full-cube shape + the blocksBuilding filter (players+mobs block, dropped items don't). |
| **Biome tags** | 4c6cea9f | 19 worldgen biome-tag JSONs from the 26.2 extractor re-run, committed (deferred from 17-21). |

## 1:1 audits run this session (all jar-verified)

- **Inventory doClick** (all 7 ClickTypes + moveItemStackTo + quickMoveStack + Slot primitives): FAITHFUL. Only gap = `updateTutorialInventoryAction` (client-side tutorial/stats, no gameplay effect) — not ported, acceptable.
- **Block place**: found + FIXED the missing entity-collision (isUnobstructed). Otherwise faithful.
- **Combat / Player.attack / hurtServer / actuallyHurt / knockback**: FAITHFUL. (A first-pass "deviation" on causeFoodExhaustion was a FALSE POSITIVE — the auditor read LivingEntity.actuallyHurt, but Player OVERRIDES actuallyHurt and DOES call causeFoodExhaustion on the victim; Sulfur ports the Player override correctly. Both exhaustion sites — attacker via Player.attack, victim via Player.actuallyHurt — verified against bytecode.)
- **Fall damage** (causeFallDamage / checkFallDamage / calculateFallPower): FAITHFUL 1:1, all constants match (safe-fall 3.0, multiplier 1.0, epsilon 1e-6).
- **Breath / drowning** (baseTick air branch / decreaseAirSupply / shouldTakeDrowningDamage): FAITHFUL 1:1 (max 300, -20 threshold, 2.0 drown damage, +4 refill). RESPIRATION enchant is a cited no-op (no attribute system yet).

## What the user CONFIRMED works this session
Floating in water (the FluidCount root-cause fix), eating, hunger drain/regen, combat, drops, block-break dig-time. The bot (testbot -mode wander) now also walks smoothly (delta-move), swings, changes held item, eats, and breaks→picks-up→places blocks — all visible to a watcher and stable (0 disconnects after the queue/race fixes).

## GAMEPLAY-07 gate criteria (the autonomous:false human-verify)
A real vanilla 26.2 client (two players, or one + the wander NPC) confirms:
1. Players see each other MOVE (smooth delta-move, not stutter) — ✅ verified via the testbot observer receiving MoveEntityPos/PosRot/EntityPositionSync.
2. Positions + inventory survive a reconnect (GAMEPLAY-02/03 — landed 17-01).
3. Attacks deal damage + death/respawn (GAMEPLAY-04 — landed 17-03, audited faithful).
4. Water flows + affects movement (GAMEPLAY-05 — landed 17-02, FluidCount + cave-gap fixed).
5. Broken blocks drop pickable items (GAMEPLAY-06 — landed 17-04).
6. Other players see swings, held-item changes, the eat pose (the three audits — landed this session).

**Remaining for the human gate:** the operator's real-client pass confirming 1–6 visually. The
1:1 work is complete and unit/-race tested; the gate is the visual sign-off.

## Quality
- `CGO_ENABLED=0 go build ./...` clean.
- Full `./server/` suite green; `./world/`, `./level/`, `./world/levelgen/...` green.
- Docker `-race` full `./server/` suite + `./world/` GREEN (the new visibility seams + the worldgen race fix covered).
- ~30 new unit tests (swing/equipment/pose broadcast, delta-move decision, post-process fluids, entity-collision place).

## Deferred (logged, not in scope to close Phase 17)
- DATA_SHARED_FLAGS (sprint/sneak/swim pose) — needs server-side input-flag plumbing that doesn't exist; cited, not baked.
- Async fluid sim — gated on the cost instrumentation data (optimize only what measurably needs it).
- Mobs walk on water — owned by the mob-AI track (deferred-items.md).
- Crafting — never implemented; moved to v4 Phase 25 (built through the plugin system as a dogfood).
