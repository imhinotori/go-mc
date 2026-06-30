---
gsd_state_version: 1.0
milestone: v5
milestone_name: Mob Behaviors & Living-Entity Subsystems
status: milestone_complete
stopped_at: Phase 36 (wolf) COMPLETE + VERIFIED 3/3 — v5 ALL 9 PHASES DONE (29/30/30.1/31/32/33/34/35/36). The 8-mob jar-faithful roster ships byte-identical; pig oracle byte-identical throughout; Docker -race clean. v5 READY for milestone-completion (/gsd-complete-milestone v5).
last_updated: "2026-06-30T18:30:00.000Z"
last_activity: 2026-06-30
progress:
  total_phases: 9
  completed_phases: 9
  total_plans: 37
  completed_plans: 37
  percent: 100
---

# Project State

### ⚠️ CARRYOVER (found Phase-36 live-verify, non-blocking) — bot joins reporting health 0
A freshly-joined player's ClientboundSetHealth reports health 0 to the client (the bot probe showed
health=0.0/seen=true from tick 0, Y stable on the floor — NOT void/fall death, NOT a stale .dat: persists
after wiping world/playerdata + a fresh-disk server). The tickPlayer is CREATED with health: maxHealth (20)
at gameplay_tick.go:339, yet syncFood (food.go:407) sends SetHealth(p.health) reading 0 — so something
between join and the first food-sync zeroes p.health (or the join uses a different tickPlayer-creation path
that skips the health init). Real bug (a real client would HUD-render 0 hearts on join, risking instant
perceived death). NOT a Phase-36/wolf defect (the wolf melee + anger-on-hit are unit-proven through the
Phase-29 keystone; the live wolf test was pivoted to a position/homing signal that doesn't depend on health).
FIX (own task): trace the join → tickPlayer health-init path; ensure p.health = maxHealth before the first
syncFood, and add a bot live-test asserting join health == 20. The botclient now tracks health (BotState.Health,
default 20) for that test once the server bug is fixed.

### ⚠️ CARRYOVER (pre-existing, non-blocking) — flaky spawn-regression test

`TestBehaviorRegressionMobSpawns` (async_stress_test.go) intermittently fails under `-count`/full -race:
"async spawner added no mob over N cycles under cap (0->0)". ROOT: the spawner picks candidate (x,z)
via the GLOBAL `math/rand/v2` `rand.IntN` (spawner.go:360-363), unseeded — under `-count` the global
RNG carries across reruns so some runs never pick a standable column in the cycle budget. NOT caused by
Phase 29 (no spawner.go/async.go change in 29). Phase-29's own damage/death/knockback/sound/orb/
cross-region tests are ALL -race clean in isolation. FIX (own task): inject a seeded `*rand.Rand` into
the spawner so the test is deterministic — deferred to avoid destabilizing the bit-fragile pig-oracle
RNG mid-Phase-29. Tracked alongside the prior `TestServerAiStepWalksToGoalTarget` flake.

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-29)

**Core value:** A Go server that an unmodified vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — architected from day one for Leaf-style async optimizations.
**Current focus:** Phase 33 — Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE

## Current Position

Phase: 35 (Hostiles + Spawn Rules) — COMPLETE
Plan: 6 of 6 (35-06 THE GATE done; 35-03/35-04 consolidated into the 35-01b gap-closure)
Status: Phase complete — ready for verification
Last activity: 2026-06-30

### v5 ROADMAP — Phases 29–36 (the convergent jar-grounded dependency order)

The dependency graph IS the build order. The damage keystone (29) has the widest fan-out and comes
first; held-item (32) precedes breeding (33); pig parity (33) is the HARD GATE before any new mob
(34–36); hostiles (35) precede wolf (36). The bit-fragile pig oracle (`TestPluginPigEqualsGoNativePig`)
must stay GREEN through every goal-adding phase (29–33) — each new RNG-drawing goal added to the
Go-native oracle AND the plugin pig IN LOCKSTEP, in the SAME plan, draws confined to running-goal
callbacks (never `serverAiStep`/`navigation.tick`). Standing constraints: 1:1 jar-verified (javap
before writing), CGO=0 preserved, no new Go deps, Docker `-race` + `strictRegion` clean.

| Phase | Goal | Requirements | Research flag |
|-------|------|--------------|---------------|
| 29 — Damage Keystone (S2) | Mob takes/deals damage + lastDamageSource + damage-type tags | MOB-SUB-01/02/03 | YES — cross-region damageIntent barrier plumbing |
| 30 — JumpControl + Fluid (S1) | Mob jump impulse + fluid predicates; FloatGoal@0 on the pig | MOB-SUB-04/05 | no |
| 31 — PanicGoal (S2 consumer) | PanicGoal@1 reads lastDamageSource | (MOB-GATE-01, closed P33) | no |
| 32 — Held-Item + Item Tags (S4) | Held-item read + item-food tags; TemptGoal@4 ×2 | MOB-SUB-06/07 | no |
| 33 — Aging + Breeding (S3) / PIG PARITY GATE | Aging + breeding; BreedGoal@3 + FollowParentGoal@5; full 8-goal oracle | MOB-SUB-08/09 + MOB-GATE-01/02 | YES — aging/breeding javap + same-region-cut decision |
| 34 — New Passive Mobs | cow/sheep/chicken as plugins | MOB-PASS-01/02/03 | no |
| 35 — Hostiles + Spawn Rules | target selector + cap + day/night; zombie/skeleton/spider | MOB-SUB-10/11 + MOB-HOST-01/02/03 | YES — light-gate decision + cap/despawn |
| 36 — Wolf (Neutral) | wolf base type + supplier; wild then tame second pass | MOB-NEUT-01/02 | YES — TamableAnimal + wolf supplier |

**The pig-oracle gate (Phase 33):** the full 8-goal oracle passes byte-identical over 500 ticks
with all goals live, AND two fed pigs breed + the baby follows its parent — MUST pass before any
new mob. **The forced Phase-35 decision:** light engine vs documented gametime-darkness proxy —
never a silent daylight flood. **Accepted v5 deviation (Phase 33):** same-region breeding cut
(barrier-resolved full-fidelity cross-region match deferred, documented).

---

## Historical context (pre-v5, preserved)

### ⚠️ DONE this session — 25-03 PLUGIN-05 close (cooking/stonecutting BLOCK build-or-defer)

The FINAL plan of Phase 25 resolved the cooking/stonecutting BLOCK side per the no-built-but-unwired
rule. The recipe MATCH for all types already shipped (Plan 01) + the crafting-grid menus (Plan 02);
this plan audited each cooking/stonecutting type and BUILT the feasible block, DEFERRED the rest:

- **stonecutting BUILT** — the StonecutterBlock + its 1:1 single-input recipe-picker menu (38 slots:
  input 0, result 1, player 2..37). Right-click opens `minecraft:stonecutter`; the input drives the
  `selectByInput` recipe list; a `ServerboundContainerButtonClick` selects a result
  (`clickMenuButton` → `setupResultSlot`, the `selectedRecipeIndex` DataSlot synced via the new
  `ClientboundContainerSetData` encoder); the take consumes exactly 1 input (`ResultSlot.onTake`);
  close returns the input. The result is **plugin-gated** (`Manager.Match` 1×1 confirms
  matchability — the dogfood) AND **server-side-picked** (the parsed `selectByInput` list — because
  `Match` returns only the first 1×1 match, and vanilla picks server-side too). New files
  server/stonecutter_menu.go + server/cooking_block.go.

- **smelting/blasting/smoking/campfire_cooking DEFERRED** (cited, deferred-blocks.md): the furnace
  needs a per-tick block-entity drive seam (NONE exists — `grep serverTick/BlockEntityTick` → 0) +
  a FuelValues fuel table (NONE exists) + BE fields/NBT (the BE types are empty struct markers). A
  large unbuilt subsystem = a legitimate context-cost defer. The deferral is the BLOCK UI ONLY —
  every deferred matcher (Plan 01) is tested headless (TestSmeltingMatcherShips: iron_ore →
  iron_ingot). NO furnace was faked.

CGO=0 build/vet/test green (5 new TestStonecutter*/TestSmeltingMatcherShips); Docker -race clean (no
cross-tick state built — the menu state is tick-owned transient). Commits 14966823 + 9a5fee10 +
6d2ca968. PLUGIN-05 complete; Phase 25 done.

### ⚠️ WHAT'S NEXT (legacy — see .planning/HANDOFF.md for the FULL detail)

### ⚠️ WHAT'S NEXT (resume here — see .planning/HANDOFF.md for the FULL detail)

0a. **DONE this session — STRUCT-POLISH-01 chest-OPEN UI wired (the 20-02 W2 split).** Right-click a structure chest now resolves the chest BlockEntity from its world position, rolls the stored {LootTable, LootTableSeed} lazily on first open (one-shot unpackLootTable), allocates a per-player windowId (nextContainerCounter 1..100), and sends ClientboundOpenScreen(generic_9x3) + ContainerSetContent (27 chest + 36 player slots). ContainerClick on the chest window moves items (left/right-click PICKUP, shift-click QUICK_MOVE both directions, Q THROW) over the shared cursor; ContainerClose frees the windowId and the chest items persist in the tick-owned t.openChests across opens. New files server/chest_open.go + server/chest_click.go; openScreen encoder in slot_encode.go; tickPlayer.openContainer/containerCounter + TickLoop.openChests. 1:1 jar port (ChestBlock.useWithoutItem, ServerPlayer.openMenu, ClientboundOpenScreenPacket, ChestMenu slot layout — all cited). 8 chest tests green; CGO_ENABLED=0 build + vet clean; Docker -race green; no new deps. DEFERRED (cited): vanilla LootTable.fill random-slot shuffle (items placed sequentially), chest-item flush to BlockEntity NBT on chunk-unload/save (persists in-memory only), multi-viewer chest sync (single-viewer path shipped).

0. **DONE earlier this session** — cave-water-gap fix (markPosForPostProcessing one-shot, NOT a scan) + ALL THREE GAMEPLAY-07 audits (swing→Animate, held-item→SetEquipment, eat/use pose→DATA_LIVING_ENTITY_FLAGS, delta-move→ServerEntity.sendChanges). All jar-verified, tested, Docker -race green.
1. **GAMEPLAY-07 real-client visual gate (autonomous:false)** — the 1:1 work is complete; needs the user's real-client pass (two clients, or the `testbot -mode wander` NPC: another player should now see arm swings, held-item changes, the eat pose, and SMOOTH delta-based walking instead of stutter). If clean → close Phase 17 → Phase 18.
2. **DATA_SHARED_FLAGS (sprint/sneak/swim pose)** — left deferred (cited, not baked): it needs server-side input-flag plumbing that doesn't exist yet. Pick up when that lands.
3. **(optional, gated on data) async fluid sim** — the user asked about moving fluids off the main tick. Instrumented this session (`grep 'ULTRA[fluid] cost'`). Port to compute-off-tick/apply-on-tick (like pathReady) ONLY if the cost data shows it blocks — the optimization-only mandate.
4. **Push to development** (deploys to prod, fixes watchtower) — authorized, BLOCKED by the unrelated attendly env gate hook.

### ⚠️ (legacy) WHAT'S NEXT

1. **GAMEPLAY-07 visual gate (autonomous:false)** — pending the user's real-client pass on seed 777 (`localhost:25565`, server rebuilt + running with all fixes through 17-22 AND the test kit: start it with `SULFUR_TEST_KIT=1 ./sulfur.exe -seed 777`). Proactive jar-audit closed 4 more 1:1 gaps the gate hadn't yet reached: hunger now drains/regens/starves (17-19); the inventory CLICK handler now does real pickup/shift-click/swap/throw/double-click/drag/clone (17-20, was a no-op stub); block-break now has the per-hardness dig-time + crack-overlay model with jar-extracted hardness for all 1196 blocks (17-21, was instant-break); and EATING is wired (17-22, startUsingItem→completeUsingItem→FoodData.eat + jar-extracted per-item food/consumable) so the hunger loop is closed (drain + regain). The gate-only `SULFUR_TEST_KIT=1` join kit (food + cobble/planks/torch/dirt) lets the operator exercise eat + container-click + place/break without first mining a meal (default prod join is the vanilla empty inventory, untouched). If the next pass is clean → close Phase 17.
2. **Known 1:1 DEBT (deferred-items.md, owned by the mob-AI track the user runs in parallel):**
   - mobs walk ON water — mob nav not water-aware (`WalkNodeEvaluator.getPathType` + `Mob.travelInFluid` server-side not ported).
   - `TestTickAIDrivesMobs` flaky — mob AI non-deterministic (RNG/async-pool), needs a seeded source per the 1:1 mandate.
3. **After Phase 17:** Phase 18 (online-mode: Yggdrasil auth + AES/CFB8 encryption), 19 (TUI + disconnect logs — connect/disconnect logging already started in 17-09), 20 (structure polish).

### 1:1 MANDATE (absolute, CLAUDE.md)

All gameplay logic must be a literal method-for-method copy of the unobfuscated 26.2 jar (`temp/cache/26.2-inner.jar`, javap -c -p). Mirror call chains + numeric ops exactly (float casts, epsilons, attribute multipliers, RNG draw order, guard checks). NO paraphrase/simplify/change-behavior. ONLY optimization (that provably preserves identical observable gameplay) is permitted. Always verify against the jar bytecode before writing — never from intuition.

### ⚠️ v1 "fully playable" was half-done — Phase 17 reconnects the seams

A real vanilla 26.2 client found six gameplay defects. All share one pattern: core
logic exists + is unit-tested, but the final wiring seam was never connected (v1 closed
on isolated-function tests). Evidence (file:line) in HANDOFF.md. Build order: GAMEPLAY-01
(player-entity-in-tracker broadcast) is the keystone — GAMEPLAY-06 (item drops) + visible
entities depend on it; GAMEPLAY-05 (fluid sim) is the one large net-new port; 02/03/04
are small high-value seam-reconnects. Phase 17 lands FIRST (online-mode is pointless on a
non-playable server).

### 🌳 FEATURES-MILESTONE GATE (Phase 13 — FEAT-04/05/06)

The FEATURES half of v2 is CLOSED. A real unmodified vanilla 26.2 client (PrismLauncher)
explored the running server's fully-decorated world and confirmed the ported pipeline
renders — *"Hay árboles y vines"* (trees + vines render). End-to-end live: Phase 10's
legacy-LCG + cross-chunk 3x3 seam + live worldgen heightmaps → Phase 11's
ConfiguredFeature/PlacedFeature model + placement modifiers + FeatureSorter +
applyBiomeDecoration → Phase 12's core bodies (OreFeature decoration ores, ground cover,
the random/simple/boolean selectors, BlockPile/FallenTree/VegetationPatch) → Phase 13's
TreeFeature (the full per-biome tree set incl. cherry/mangrove+RootPlacer/azalea/pale_oak)
and the MonsterRoomFeature dungeon (13-04). The automated backstop is green: biome-correct
trees place, the forest selector resolves to real trees, the dungeon places, the 5x5
reorder-determinism + emit-once stay byte-identical with ALL bodies live, and the full
Docker -race over ./world/... is clean. Worldgen added NO new wire surface (the chunk
format was capture-diff-sealed in v1 Phase 4), so the gate was real-client VISUAL
determinism, not a capture-diff — the Phase-4/5 wire goldens stay the authority and stay
green. DEFERRED to v3 (cosmetic, non-blocking, documented in 13-04-SUMMARY): dungeon loot
tables + spawner mob roll, BeehiveDecorator occupant, the pale_garden floor PaleMoss patch.
NEXT: the STRUCTURES half of v2 (Phases 14-16 — desert pyramid → mineshaft → stronghold →
village jigsaw, via the STARTS/REFERENCES/PLACE pipeline).

### 🎮 FIRST-PLAYABLE MILESTONE (Phase 5 — PLAY-01..06)

An unmodified vanilla 26.2 client (PrismLauncher) connects to `cmd/sulfur` on
`localhost:25565` and PLAYS: it logs in (Join Game → Play), is placed on solid
ground, and WALKS AROUND a world whose view-distance ring FOLLOWS it (new chunks at
the moving edges, no void, no falling off the world), appears in its own tab list,
and is not kicked. User-confirmed: *"si, funciona :)"*. This is the project's
first-playable threshold — the full handshake→login→config→play→movement loop works
against a real client over proto 776.

05-03 (the seal): the capture-diff byte-diffed Sulfur's three MEDIUM-confidence Play
encoders against a REAL vanilla 26.2 server — `PlayerInfoUpdate` self-entry (1-byte
8-action mask framing + `VarInt(0)` property count-prefix), `SetDefaultSpawnPosition`
(RespawnData = Identifier dimension + packed-Long BlockPos + Float yaw + Float pitch),
and `ForgetLevelChunk` (jar-derived packed Long) — all byte-identical to vanilla with
NO encoder bug. The diff's load surfaced and fixed a real `net/queue.ChannelQueue`
close-vs-send data race (mutex+closed-flag serializes Close against Push). `SetTime`
is NOT needed for v1 (vanilla sends it but the client does not kick without it; the
31-byte layout is documented). Golden fixtures committed; `TestPlayBytesVsVanillaCapture`
is the CI gate. Docker `-race` over `./server/... ./world/... ./net/...` clean.

05-01 milestone (PLAY-04 + PLAY-02): the player WALKS AROUND a world that follows it.
`applyInput` (server/subtick.go) fills the Phase-3 stub — it decodes all four
`ServerboundMovePlayer*` layouts with the jar-confirmed field order, reading the trailing
field as a PACKED FLAGS BYTE (`&1` onGround, `&2` horizontalCollision), never a Boolean
(the 1.21.3+ shift), and updates tick-owned `x/y/z/yaw/pitch/onGround`. On a real
chunk-column crossing, `recenterRing` (server/world_stream.go) moves `p.center`, resets
`centerSent` so `flushOutbound` re-emits `SetChunkCacheCenter`, prunes `sentChunks`, and
sends one `world.ForgetLevelChunk` per dropped column — the view ring FOLLOWS the player
(void-on-walk closed). Movement is gated on the teleport confirm: `applyInput` drops
movement until `confirmedTeleport`, and dispatch confirms ONLY when the echoed
`ServerboundAcceptTeleportation` VarInt matches `awaitingTeleport` (a forged id leaves
the gate closed). `ServerboundPlayerLoaded` routes as a no-op.

05-02 milestone (PLAY-01 + PLAY-05): the early-Play tail (PlayerAbilities → SetHeldSlot
→ PlayerInfoUpdate self-entry → SetDefaultSpawnPosition) is appended to the bootstrap
before `loop.register`, with an incrementing per-gameTick teleport id threaded into both
the PlayerPosition and `tickPlayer.awaitingTeleport` — the joining client becomes a real,
listed player.

Phase-4 milestone (prior): a real client stands in a streamed world — chunks encode
byte-identical to vanilla 26.2 (04-04 capture-diff) and stream as a clamped center-out
ring with batch framing (WORLD-05).

Progress: [█████████░] 90%

## Performance Metrics

**Velocity:**

- Total plans completed: 0
- Average duration: — min
- Total execution time: 0.0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**

- Last 5 plans: —
- Trend: —

*Updated after each plan completion*
| Phase 01 P01 | 8min | 3 tasks | 343 files |
| Phase 01 P02 | 12m | 3 tasks | 265 files |
| Phase 01 P03 | 10min | 3 tasks | 4 files |
| Phase 02 P01 | 70min | 3 tasks | 6 files |
| Phase 02 P02 | 5m | 3 tasks | 5 files |
| Phase 02 P03 | 55 min | 2 tasks | 131 files |
| Phase 02 P04 | 178min | 3 tasks | 701 files |
| Phase 03 P01 | 18 | 2 tasks | 4 files |
| Phase 03 P02 | 18min | 2 tasks | 5 files |
| Phase 03 P03 | 22min | 3 tasks | 5 files |
| Phase 04 P01 | 12min | 2 tasks | 2 files |
| Phase 04 P02 | 6m | 3 tasks | 10 files |
| Phase 04 P03 | 7m | 2 tasks | 7 files |
| Phase 04 P04 | 70min | 2 tasks | 5 files |
| Phase 05 P01 | 18min | 3 tasks | 7 files |
| Phase Phase 05 PP02 | 20min | 2 tasks tasks | 3 files files |
| Phase 06 P01 | 7min | 2 tasks | 7 files |
| Phase 06 P02 | 32min | 3 tasks | 8 files |
| Phase 06 P05 | 24 min | 2 tasks | 8 files |
| Phase 06 P06 | 35 min | 2 tasks | 7 files |
| Phase 07 P01 | 38min | 2 tasks | 6 files |
| Phase 07 P05 | 35 min | 2 tasks | 5 files |
| Phase 07-ai-pathfinding-commands-chat P02 | 41min | 2 tasks | 7 files |
| Phase 07 P03 | 38 min | 2 tasks | 5 files |
| Phase 08 P01 | 25min | 2 tasks | 6 files |
| Phase 08 P04 | 18 min | 1 tasks | 5 files |
| Phase 08 P06 | 5 min | 2 tasks | 4 files |
| Phase 08 P07 | 14min | 1 tasks | 2 files |
| Phase 09 P01 | 35min | 2 tasks | 114 files |
| Phase 09-stretch-online-worldgen-regions P02 | 38min | 2 tasks | 8 files |
| Phase 09-stretch-online-worldgen-regions P03 | 10min | 2 tasks | 7 files |
| Phase 09 P04 | 27 min | 2 tasks | 3 files |
| Phase 09 P05 | 20min | 3 tasks | 8 files |
| Phase 09 P06 | 30 min | 2 tasks | 6 files |
| Phase 09 P08 | 35 min | 1 tasks | 2 files |
| Phase 10 P01 | 12m | 2 tasks | 3 files |
| Phase 10 P02 | 10m | 2 tasks | 6 files |
| Phase 10 P03 | 75min | 4 tasks | 7 files |
| Phase 11 P02 | 1session | 3 tasks | 8 files |
| Phase 11 P01 | ~40min | 3 tasks | 12 files |
| Phase 11 P03 | 1 session | 5 tasks | 10 files |
| Phase 12-core-feature-types P01 | 73min | 3 tasks | 14 files |
| Phase 12 P02 | 58 min | 2 tasks | 6 files |
| Phase 13 P01 | 38min | 2 tasks | 5 files |
| Phase 13 P02 | 2h | 2 tasks | 10 files |
| Phase 13 P03 | 2h | 2 tasks | 8 files |
| Phase 13 P04 | ~30min | 3 tasks | 4 files |
| Phase 14 P01 | 70 | 3 tasks | 97 files |
| Phase 14 P02 | 28min | 2 tasks | 10 files |
| Phase 14 P03 | 95min | 2 tasks | 11 files |
| Phase 15 P01 | 55min | 2 tasks | 8 files |
| Phase 15 P02 | 75 | 2 tasks | 6 files |
| Phase 15 P03 | 95 | 2 tasks | 6 files |
| Phase 16 P02 | 95 | 2 tasks | 8 files |
| Phase 17 P01 | 9min | 4 tasks | 11 files |
| Phase 17 P04 | 11min | 2 tasks | 4 files |
| Phase 17 P02 | 35min | 3 tasks | 5 files |
| Phase 18 P01 | 18min | 3 tasks | 5 files |
| Phase 18 P02 | 4min | 2 tasks | 6 files |
| Phase 19 P01 | 18min | 3 tasks | 8 files |
| Phase 19 P03 | 22min | 3 tasks | 8 files |
| Phase 20 P01 | 16min | 3 tasks | 14 files |
| Phase 20 P03 | 35min | 3 tasks | 7 files |
| Phase 20 P05 | 20min | 2 tasks | 7 files |
| Phase 20 P02 | 25min | 3 tasks | 19 files |
| Phase 20 P04 | 75min | 3 tasks | 17 files |
| Phase 21 P01 | 3min | 3 tasks | 8 files |
| Phase 21 P02 | 3min | 2 tasks | 5 files |
| Phase 22 P01 | 25min | 3 tasks | 15 files |
| Phase 22 P02 | 40min | 2 tasks | 13 files |
| Phase 23 P01 | 40min | 3 tasks | 5 files |
| Phase 23 P02 | 12min | 3 tasks | 7 files |
| Phase 24 P01 | 9min | 2 tasks | 9 files |
| Phase 24 P02 | 25min | 3 tasks | 19 files |
| Phase 25 P01 | 11min | 3 tasks | 13 files |
| Phase 25 P02 | 17min | 3 tasks | 18 files |
| Phase 25 P03 | 30min | 2 tasks | 9 files |
| Phase 26 P01 | 7min | 3 tasks | 13 files |
| Phase 26 P02 | 11min | 3 tasks | 17 files |
| Phase 26 P03 | 22min | 2 tasks | 17 files |
| Phase 27 P01 | 50min | 3 tasks | 64 files |
| Phase 27 P02 | 6min | 3 tasks | 6 files |
| Phase 27 P03 | 35min | 4 tasks | 19 files |
| Phase 28 P01 | 17min | 2 tasks | 8 files |
| Phase 28 P02 | 38min | 3 tasks | 12 files |
| Phase 29 P01 | 35min | 3 tasks | 5 files |
| Phase 29 P02 | 10min | 4 tasks | 6 files |
| Phase 29 P03 | 30min | 5 tasks | 6 files |
| Phase 29 P04 | 28min | 4 tasks | 9 files |
| Phase 30 P01 | 6min | 2 tasks | 3 files |
| Phase 30 P02 | 9min | 3 tasks | 5 files |
| Phase 30 P03 | 27min | 1 tasks | 8 files |
| Phase 30.1 P01 | 75min | 4 tasks | 12 files |
| Phase 31 P01 | 35min | 4 tasks | 10 files |
| Phase 32 P01 | 11min | 4 tasks | 10 files |
| Phase 33 P01 | 22min | 3 tasks | 6 files |
| Phase 33 P02 | 11min | 3 tasks | 6 files |
| Phase 33 P03 | 25min | 2 tasks | 7 files |
| Phase 33 P04 | 13min | 3 tasks | 7 files |
| Phase 33 P05 | 35min | 2 tasks | 2 files |
| Phase 34 P00 | 13min | 2 tasks | 10 files |
| Phase 34 P02 | 22min | 1 tasks | 5 files |
| Phase 34 P03 | 22min | 1 tasks | 5 files |
| Phase 34 P01 | 30min | 2 tasks | 7 files |
| Phase 34 P04 | 35min | 2 tasks | 9 files |
| Phase 35 P02 | 35min | 2 tasks | 8 files |
| Phase 35 P01 | ~1h | 3 tasks | 10 files |
| Phase 35 P05 | 35 | 3 tasks | 7 files |
| Phase 35 P06 | 40m | 2 tasks | 5 files |
| Phase 36 P01 | 38min | 2 tasks | 13 files |
| Phase 36 P03 | 24min | 2 tasks | 6 files |
| Phase 36 P02 | 30min | 1 tasks | 2 files |
| Phase 36 P04 | 8min | 2 tasks | 3 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Phase 36]: 36-01 MOB-NEUT-01/02 — the SHARED Go wolf foundation (Wave-1 solo, all hoisted edits so B/C/D stay disjoint). VERIFIED via javap: DATA_FLAGS=18 / DATA_OWNERUUID=19 (TamableAnimal.defineSynchedData: Animal adds nothing past AgeableMob 16/17, then DATA_FLAGS_ID [BYTE] then DATA_OWNERUUID_ID). The ANGER model is W6-DISSOLVED: a gametime-ENDPOINT `angerEndTime int64` (isAngry = endTime>0 && endTime-gameTime>0), NO per-tick decrement, NO ResetUniversalAngerTargetGoal — set at the combat flag2 store-point as `gameTime + 400 + nextInt(381)` (UniformInt(400,780).sample), wolf-gated AND player-attacker-gated (the pig + non-player attackers draw ZERO). THE B1/B2 FIX: nearestAttackableTargetGoal parameterized ADDITIVELY — a `targetClass` (PLAYER zero-default | SKELETON) + an optional `angerGate`; findTarget/canContinueToUse branch on class; `nearestEntityOfTypeAt` scans entity.Skeleton.ID within FOLLOW_RANGE; `newAngryPlayerTargetGoal` (PLAYER + isAngryAt) + `newSkeletonTargetGoal` (SKELETON + nil). The bare `nearest_attackable_target` is BYTE-IDENTICAL Phase-35 (proven: hostile tests + pig oracle green). Four new goal classes ported verbatim (SitWhenOrderedToGoal {JUMP,MOVE}; FollowOwnerGoal {MOVE} lookAt+moveTo with the teleport DEFERRED; OwnerHurtBy/OwnerHurtTargetGoal {TARGET}). DEFERRED+CITED: FollowOwner teleport, owner-UUID wire broadcast, ResetUniversalAnger, owner inbound lastHurtByMob (constant-false stub; owner ATTACK-side getLastHurtMob IS live via tickPlayer.lastHurtMob set in handleMobAttack), the assets/vanilla_wolf embed (Plan C). Commits bcdd81bc + 9f854c57.
- [Phase 28]: 28-01 PLUGIN-07 perf gate + custom-mob spawn trigger (server-side half) — promoted the Phase-23 testdata wander-mob decl to a LIVE `//go:embed` asset (server/assets/wandermob/) boot-loaded alongside vanilla_pig via `tick.RegisterWanderMob()` (a merge into the SAME tick-owned registry, single-owner at boot). Added a `SULFUR_TEST_KIT`-gated spawn trigger: a pig-spawn-egg re-skin (item 1161, main-inventory slot 35) matched in `handleUseItem` BEFORE the food path → `handleGateSpawnEgg` → `spawnDeclaredMob` ~2 blocks in front (inert in prod, threat T-28-03). Promoted the Phase-24 A/B equality harness (newPigAI oracle vs the plugin pig, drainPendingPath) into `BenchmarkPluginPigVsGoNative` + a hard `TestPerfGate`: the plugin per-(mob·tick) delta must stay within an ABSOLUTE ns cap (15000, a coarse backstop) AND a RELATIVE % cap (15%, the PRIMARY machine-portable signal) — both set from a documented 3-run baseline (delta 1.1–2.25%, measured 2026-06-28 on Ryzen 7 5800X). KEY CALIBRATION: the absolute per-(mob·tick) number (~213 µs) is dominated by drainPendingPath's async-A* latency jitter, so the relative % is the real gate (threat T-28-01). `BenchmarkTickEmitOverhead` proves the zero-subscriber Emit hot path is 0 allocs/op. The DO-NOT-DELETE Go-native pig oracle (newPigAI) is the retained baseline. CGO=0 build/vet green; TestPerfGate exits 0; full server suite green. Commits 63c65daf + 6fd45e9e.
- [Phase 27]: 27-03 the N=2 flip (REGION-01, Phase 27 COMPLETE) — the world is statically split into N=2 regions that tick CONCURRENTLY. `regionOf = (col.X ^ col.Z) & 1` is a PURE, restart-stable checkerboard hash (chunks are position-keyed, no region id persisted; the XOR-parity puts every chunk's neighbours in the OTHER region so the seam is exercised everywhere a mob/player moves a column). A per-goroutine `currentRegion` (xsync.Map keyed by goroutine id) lets region.tick register itself so the ~200 existing `t.only()` per-region call sites resolve to the OWNING region across the conc fan-out with ZERO churn (a non-fan-out goroutine — the coordinator / direct test calls — falls back to globalRegion). The world (shared ChunkManager, not goroutine-safe) is MUTATED only on the coordinator (tickWorld/tickChunks/chunkReady/dispatch edits); only `tickAI`+`tickPhysics` fan out (the fan-out only READS the world) — race-clean by construction + the observable phase order byte-identical (TestTickPhaseOrder). Cross-region entity TRANSFER at the barrier: detectTransfers queues boundary-crossers mid-tick, applyCrossRegionTransfers moves the SAME *Entity A->B at the quiescent barrier (ai/nav/scratch travel, no double-tick/drop). The async rejoin re-resolves owningRegion(id) (drop if gone); the spawn cap is cross-region; the entity/world/nav handles carry the OWNING region (approach a) so a declared-mob goal callback for a mob in R runs on R's goroutine + re-resolves R's store (THE REGION-01 GATE); the tracker.near spans regions at the barrier. THE GATE green: 2 parallel regions + transfer + cross-region tracker + region-aware Emit all Docker -race clean over ./server/ ./plugin/...; CGO=0 go build ./... + ./cmd/sulfur pure-Go static; the full existing suite UNCHANGED. Dynamic merge/split + per-region-sharded chunks are a locked Phase-28+ deferral. Commits 79abee79 + 89443381 + e9d9eef3.
- [Phase 26]: 26-03 PLUGIN-06 world-bridge (Phase 26 COMPLETE) — an off-tick python hook produces a capability-gated MUTATION REQUEST (set_block + a simple spawn/log) carried as PLAIN SCALARS (never a live handle), queued on asyncIn2, drained on the TICK goroutine, and applied through the SAME Phase-23 seam (ChunkManager.SetBlock + broadcastBlockUpdate / spawnVanillaPig) — the ONLY mutation point (TICK-05). A READ goes request -> owner-snapshot -> return-a-copy (pythonReadReq carries a buffered reply channel; the owner snapshots GetBlock and sends the copied scalar back). The request/apply indirection IS the safety boundary (T-26-03 — no live tick-owned handle ever escapes off-tick). The capSet is parsed via the SAME parseCapabilities the Phase-23 Starlark handles use and enforced at apply (denied -> dropped + capError, T-26-09; unknown capability errors loudly at load). Capabilities threaded via a server-provided host.SetPythonBridgeFactory (the server owns the parse, the host installs the per-plugin bridge at load — keeps plugin/host + plugin/python cgo-free). The Go-side serverWorldBridge is the authoritative -race-able proof (the python set_block/spawn/log/block_at builtins, behind //go:build python, only PRODUCE the request). Minimal vocabulary, the rest deferred-cited. THE PHASE GATE green: python off-tick (26-02) + on-tick apply (this plan, -race Docker) + the default no-tag build STILL pure-Go static (CGO_ENABLED=0 build clean, ZERO gopython in the graph incl. the cmd/sulfur binary). Commits e51c8138 + 2c71da73.
- [Phase 23]: 23-02 PLUGIN-03 behavior layer — declare_mob/goal capture a declaration ONCE at load into a tick-read mobRegistry (import-direction-A: the server owns the declare_mob/goal builtins + registry and injects them via host.LoadDirWith extra; the host just runs the body that captures into them). A starlarkGoal implements the EXISTING server.Goal and is arbitrated by the SAME goalSelector flag-locking (NOT a bypass, NOT a parallel AI tick); buildAIFromDecl mirrors newPigAI (a fresh per-spawn *mobAI whose starlarkGoals reference the SHARED frozen callables — per-mob struct state, shared frozen values). THE GATE green: a Starlark wander mob (base_type pig, one MOVE goal using nav.path_to) spawns, ticks via tickAI->serverAiStep, and MOVES through the real Go nav, rendering as entity.Pig.ID (custom = the BEHAVIOR, not a new wire id; an unknown base_type/flag/duplicate errors loudly at load). The interpreter fires ONLY inside a running goal's tick/canUse/start/stop (an idle declared mob makes 0 starlark.Calls/tick; NO starlark.Call in tickAI/tickPhysics/tickEntities); every callback runs on a fresh budget-bounded thread (stepBudget) + is error-isolated (logged, the tick survives). Declared-mob tick path Docker -race clean (the owner+pathPool A* compute is the only cross-goroutine boundary; the handles + starlarkGoal store an id, never a live *Entity — T-23-09). A plugin-facing spawn builtin is OUT of scope (T-23-11 accept — spawnDeclaredMob is the test/debug seam). Commits 58dc2da1 + dd2d58dd + 9d6f185a.
- [Phase 6 / user, 2026-06-24]: SOURCE-PORTING PERMITTED for gameplay LOGIC — may read Bukkit/Spigot/Paper/Leaf (and the decompiled vanilla jar) and translate their behavior into Go as a NON-1:1 port, explicitly to make Sulfur behave 1:1 with a real server. This is the sanctioned way to get vanilla-faithful mechanics (AI, pathfinding, dig timing, damage, mob behavior) right in Phase 7+. NOT a blanket code-copy: translate the algorithm/behavior, keep it idiomatic Go, no direct paste of GPL Java. Wire layouts still come from the unobfuscated jar (javap) as the authoritative proto-776 source. (Prompted by the 06-07 "creative break" bug = misread START vs STOP dig stages — exactly the gameplay-logic class this permission covers.)
- [Phase 7 MANDATE / user, 2026-06-24]: MOB LOGIC must be PORTED DIRECTLY FROM JAVA (vanilla decompiled jar + Paper/Leaf) so mob behavior is IDENTICAL to a real server — not approximated. Phase 7's AI/pathfinding work reads the actual `net.minecraft.world.entity.ai` sources (Goal/GoalSelector, the Brain/Behavior memory system, PathNavigation/NodeEvaluator A*) and translates them faithfully into Go. The debug pig's current sinusoidal pacing is a THROWAWAY cosmetic SULFUR_DEBUG trigger and will be REPLACED by the real ported AI in Phase 7 — do not treat it as the mob model.
- [Phase 6]: Real-client interactive check (06-07) surfaced 3 bugs no self-test caught: respawn stuck on "Loading terrain" + dead (performRespawn must mirror the join bootstrap — GameEvent(LEVEL_CHUNKS_LOAD_START) + SetHealth(full) + abilities/held-slot, not just Respawn+teleport); survival break fired on START_DESTROY_BLOCK (instant "creative" break) — must fire only on STOP_DESTROY_BLOCK; empty v1 inventory means a survival client emits no UseItemOn (place) without a held item. Fixed in 94ec8a14. The debug pig motion is a cosmetic SULFUR_DEBUG trigger, NOT real AI (Phase 7).
- [Roadmap]: Dependency-forced 9-phase spine — protocol-state sequence forces early order; codegen (Phase 1) gates everything.
- [Roadmap]: "Vanilla logic first, Leaf optimizations last" — Phases 5-7 build synchronous reference implementations; Phase 8 swaps executors behind pre-built seams.
- [Roadmap]: Ownership-based synchronization established in Phase 3 (near-zero cost single-threaded) is the insurance policy that keeps Phases 8-9 additive, not a rewrite.
- [Roadmap]: Game-time anchored to 50ms (TICK-02) + CS2-style subtick layer (TICK-03) — deliberate, not vanilla 20-TPS naive scaling.
- [Phase 1]: Fork go-mc merged into repo root (allow-unrelated-histories) on branch ender-776; module path rewritten Tnze/go-mc -> imhinotori/go-mc tree-wide so the runtime builds Go-only (GEN-01).
- [Phase 1]: Pinned base 539b4a3a + specific PR-head SHAs (pr294 23fbe76, pr295 f9c5c05, pr296 d6dece0) recorded for T-1-02 reproducibility; extractor retargeted to eclipse-temurin:25-jdk (tag form, digest deferred to Plan 02).
- [Phase ?]: 26.2 removed net.minecraft.world.item.EitherHolder; variant/damage components now encode as plain Holder<X> (VarInt), fixed in extractor by simple-name guard
- [Phase ?]: 26.2 split reports/items.json into per-item component files; ExtractAll.java synthesizes items.json to preserve gen_item.go contract
- [Phase ?]: Added Mojang-manifest sha1 gate to download.go (T-1-01); jar verified before extraction, abort on mismatch
- [Phase 01]: Enumerated 774->776 via set-deltas (comm on identifier sets) to isolate true additions from iota/ID reindex noise
- [Phase 01]: sulfur_cube_archetype registry + sulfur_cube_hot damage type are datapack/dynamic registries outside the codegen surface (absent by design, not dropped)
- [Phase ?]: [Phase 2]: NET-05 backpressure = bounded ChannelQueue + disconnect-on-full for Phase 2; revisit when the tick owns flush timing (Phase 3)
- [Phase ?]: [Phase 2]: chan Intent (Intent{*Client, pk.Packet}) is the stable network->tick seam; stubTickConsumer is the Phase 3 attach point with no API change
- [Phase ?]: [Phase 2]: Fixed fork bug — chat.Message.MarshalNBT double-encoded the compound tag header, corrupting every chat.Message Field on the wire (incl. disconnect reasons)
- [Phase ?]: [Phase 2]: -race needs cgo; host has no C compiler (CGO_ENABLED=0) so the server/net race gate runs in golang:1.26 Docker — NET-05 seam proven race-clean
- [Phase ?]: NET-01 reuses the Wave-1 Disconnect helper for the proto-776 login rejection
- [Phase ?]: cmd/ender ListPingHandler embeds PingInfo+PlayerList; PingInfo alone lacks player-count methods
- [Phase 02]: Registry send path uses type-faithful JSON->nbt/dynbt conversion (not json->map[string]any), with a floatFields override for vanilla Codec.FLOAT fields — encoding/json maps all numbers to float64->TagDouble; dynbt lets us assign TagInt/TagByte/TagFloat explicitly so the 26.2 client does not silently reject the registry
- [Phase ?]: [Phase 2]: Empty Update Tags was a confirmed HARD BLOCKER (not optional) — a real 26.2 client crashes at Registry Loading with 'Unbound tags'/'Failed to parse value' because registry entries reference tags; fix = send the real vanilla 15-registry tag set (dfb4af04)
- [Phase ?]: [Phase 2]: proto-776 ClientboundLoginFinishedPacket needs a trailing sessionId UUID (GAME_PROFILE + UUIDUtil.STREAM_CODEC); the fork omitted it and net.Pipe missed it (bot scans only UUID+name) — only a real client surfaced it (e283886c)
- [Phase ?]: [Phase 2]: NET-04 correctness gate is a real-client capture-diff, not self-consistent tests — the net.Pipe bot's lax decoder tolerated both the login_finished and empty-tags bugs a real client rejects
- [Phase ?]: [Phase 3]: TickLoop single-owner spine over injectable Clock; gametime++ inside for acc>=step anchors TICK-02 (1200/60s); 250ms spiral clamp; applyAsyncResults/tracker no-op seams for Phase 8; MSPT via atomic.Pointer[TickStats]
- [Phase ?]: [Phase 3]: TICK-03 subtick seam = bounded (cap 256, drop-oldest) per-player buffer, server-stamped At=clock.Now() (never client time), chronological drain (stable sort) through STUB applyInput; physics deferred to Phase 6. TICK-04 proven: KeepAlive on own goroutine fires no timeout under a stalled tick. Zero new deps; -race clean.
- [Phase ?]: [Phase 3]: gameTick replaces stubGamePlay — AcceptPlayer registers the player with the tick as a buffered-channel MESSAGE (register/unregister + drainRegistrations on the owner goroutine), never a cross-goroutine mutation; Docker -race -count=10 clean proves TICK-05 across the real network boundary
- [Phase ?]: [Phase 3]: Phase 3 COMPLETE — a piped connection stands in a live empty ticking world: no disconnect, Stats() MSPT/TPS live (TICK-01/06), keep-alive holds on its own goroutine while a returning ServerboundKeepAlive routes through dispatch->ClientTick (TICK-04). go run ./cmd/sulfur listens on proto 776. Zero new deps.
- [Phase ?]: 04-01: Section carries a per-section fluid-count short (two-short LevelChunkSection.write header); FluidCount defaults 0 for fluid-free chunks — presence of the short is the byte-alignment fix
- [Phase ?]: 04-01: Chunk.WriteTo sends only the 3 Usage.CLIENT heightmaps (ids 1/4/5); ReadFrom left permissive to ids 0..5
- [Phase ?]: 04-01: self-round-trip + golden byte-length are cheap regression only; authoritative WORLD-02/03 proof deferred to the 04-04 vanilla capture-diff
- [Phase ?]: Plan 04-03: off-tick chunk worker rejoins the tick via a concrete chunkReady asyncResult through the UNCHANGED applyAsyncResults seam (adapter re-wraps the immutable ChunkResult onto asyncIn); the tick is the sole manager mutator, -race clean.
- [Phase ?]: Plan 04-03: chunks stream as a server-clamped (serverViewDistance=2) center-out Chebyshev ring with SetChunkCacheCenter/Radius + ChunkBatchStart/Finished framing; the clamp bounds the needed-ring to (2r+1)^2 against an untrusted client (T-4-01/T-4-06).
- [Phase 04]: 04-04: the vanilla capture-diff (not a self-round-trip) is the authoritative WORLD-02/03 gate — it caught two paletted-container wire bugs the symmetric read/write hid: PaletteContainer.Set wrote the requested bits-per-entry as the wire header while packing floored-width data (1-bit header over 4-bit/256-long data = stripes/void), and the superflat biome carried a phantom default-biome palette entry vs vanilla's single-valued container. Both fixed (34f7cc45), re-diffed byte-identical, real client renders solid ground.
- [Phase 04]: 04-04: a MINIMAL Play-state Join Game bootstrap (ClientboundLogin id49 isFlat + GameEvent LEVEL_CHUNKS_LOAD_START + ClientboundPlayerPosition id72 teleport-id-first) was PULLED FORWARD into Phase 4 (commit 0fd96850) to fix the handleSetChunkCacheCenter NPE and enable the real-client visual milestone. It is server/play_join.go. Phase 5 must EXTEND this (full profile/abilities/inventory/real spawn/teleport validation/movement), NOT duplicate it.
- [Phase ?]: ClientboundForgetLevelChunk 26.2 wire = single packed Long (ChunkPos.pack: x low 32 bits, z high 32 bits), jar-derived via javap — not the wiki VarInt z,x
- [Phase ?]: ServerboundMovePlayer* trailing field is a packed flags UnsignedByte (&1 onGround, &2 horizontalCollision), never a Boolean (1.21.3+ shift)
- [Phase ?]: applyInput hook fires BEFORE the teleport gate so existing subtick-ordering tests survive; re-center gated on a real chunk-column crossing
- [Phase ?]: [Phase 05]: 05-02: SetDefaultSpawnPosition pinned to jar RespawnData = composite(GlobalPos, Float yaw, Float pitch); GlobalPos = composite(ResourceKey<Level> dimension, BlockPos) -> wire Identifier + packed-Long BlockPos + Float + Float (W1)
- [Phase ?]: [Phase 05]: 05-02: PlayerInfoUpdate self-entry = 1-byte 8-action mask 0x0D + writeCollection(VarInt(1)+UUID+enum-order String name/VarInt(0) props/VarInt gameMode/Boolean listed); writeEnumSet over 8 actions == single pk.Byte mask
- [Phase ?]: [Phase 05]: 05-02: incrementing teleport id = per-gameTick atomic.Uint64 (first id 1, never 0), threaded into bootstrap PlayerPosition AND tickPlayer.awaitingTeleport so the 05-01 gate matches the client echo; const initialTeleportID removed
- [Phase 05]: 05-03: capture-diff sealed the 3 MEDIUM-confidence Play encoders byte-identical to vanilla 26.2 (PlayerInfoUpdate self-entry 1-byte mask + VarInt(0) prop count; SetDefaultSpawnPosition RespawnData Identifier+packed-Long+Float+Float; ForgetLevelChunk packed Long) — NO encoder bug. Vanilla's 8-action creative entry vs Sulfur's 3-action survival, and the spawn Y (-60 vs -48), are content not framing. TestPlayBytesVsVanillaCapture + golden fixtures are the CI gate.
- [Phase 05]: 05-03: SetTime NOT needed for v1 — vanilla sends ClientboundSetTime (31-byte WorldClock+ClockNetworkState) but the real-client walk-around confirmed no kick/hang without it; the layout is documented in 05-CAPTURE-DIFF.md for a later day/night phase.
- [Phase 05]: 05-03: fixed a real net/queue.ChannelQueue close-vs-send DATA RACE (latent send-on-closed panic) surfaced under the capture-diff load — converted the bare `chan T` to a mutex+closed-flag struct (Close serialized vs Push, Pull lock-free); Client.Send's recover() masked the panic but not the race. -race clean at -count=10 over the join seam.
- [Phase 05]: PHASE 5 COMPLETE / FIRST PLAYABLE — a real vanilla 26.2 client (PrismLauncher) connects, logs in, and WALKS AROUND a following ticking world (ring follows, no void, tab list shows the player, no kick); PLAY-01..06 all proven. The project is playable end-to-end ("si, funciona :)").
- [Phase ?]: [Phase 06]: 06-01: ENT-01 entity foundation — EntityIDAllocator (atomic.Int32 pre-increment, first id 1, claimed off-tick like teleportSeq) REPLACES the hard-coded joinEntityID=1 so players AND entities share one collision-free id space (Pitfall 7 / T-6-07, race-clean T-6-08); tick-owned entityStore (by-id map + per-chunk-column grid buckets, NOT a quadtree) exposes near(x,z,range) broad-phase + move() re-bucketing; Entity instance has snapshot-friendly plain-value hot fields + AABB() from data/entity dims (reused, not rebuilt). Docker -race over ./server/... clean; zero new deps.
- [Phase ?]: [Phase 06]: 06-02: ENT-01 synchronous entityTracker fills the tracker.Tick() seam unchanged (noopTracker removed); per-player visibility diff over near() emits AddEntity(+SetEntityData 0xFF)/TeleportEntity(+RotateHead)/batched RemoveEntities; player never tracks itself; -race clean.
- [Phase ?]: [Phase 06]: 06-02: 26.2 AddEntity/SetEntityMotion movement = NEW Vec3.LP_STREAM_CODEC -> LpVec3 quantizer (15-bit pack, NOT legacy *8000 short); stationary entity = single 0x00 byte. AddEntity typeId is field 3 (after UUID, before x/y/z). SetEntityData always ends with a single 0xFF (EOF_MARKER=255). v1 uses TeleportEntity (absolute) over the 4096-scaled MoveEntity* short deltas. Jar-derived (javap); byte-seal deferred to 06-07.
- [Phase 06]: 06-05: ENT-04 component-slot inventory — SlotData.WriteTo EXTENDED to round-trip real components (ReadFrom tees the component byte span into RawComponents, WriteTo re-emits it, provably inverse); the 4 container packets routed into the subtick buffer (were dropped at default no-op); server-authoritative Inventory re-sends ContainerSetContent and DISCARDS the client's HashedStack hashes. HashedStack jar-derived = optional(ActualItem): Boolean present + VarInt id + VarInt count + HashedPatchMap whose added VALUE is a FIXED 4-byte CRC int (NOT a component body) — the mis-framing risk. Byte-seal deferred to 06-07. — ENT-04 highest wire-risk surface; jar-derived not guessed; -race clean; zero new deps
- [Phase 06]: ENT-05 Respawn reuses the Phase-5-sealed commonPlayerSpawnInfoEncoder + a trailing dataToKeep byte (0=full reset v1); no spawn-info re-derivation
- [Phase 06]: ENT-06 save-on-leave snapshots on the owner (removePlayer) and runs disk IO off-tick (RunSaveLoop) — the Phase-4 chunk-result discipline (TICK-05/T-6-15)
- [Phase 06]: ENT-06 disk inventory persists as save.Item via the item registry; the wire component SlotData is never written to disk (Pitfall 6)
- [Phase 07]: AI-01 GoalSelector ported from javap: flag-lock arbitration (canBeReplacedBy = isInterruptable && other.priority<this.priority; smaller priority = higher precedence; per-flag lockedFlags map + NO_GOAL maxInt holder) + passive Pig goal set at exact registerGoals priorities (stroll@6 MOVE, lookAtPlayer@7 LOOK, lookAround@8 MOVE|LOOK) + serverAiStep-order driver. A goal SETS wantTarget/headYaw, never moves the mob (07-02). Tick-owned, zero deps, -race clean.
- [Phase 07-ai-pathfinding-commands-chat]: 07-02: computePath is PURE over an immutable pathRegion snapshot (the Phase-8 hinge); A* ported from PathFinder.findPath bytecode with a maxVisited budget (16*16*0.5=128) as the DoS guard; path-following via the existing moveEntity — Structuring AI-02 as the request->snapshot->result seam vanilla uses makes OPT-01 a swap of the executor, not a rewrite; the budget bounds an unreachable-target A* (Pitfall 6 / T-7-04)
- [Phase 08]: 08-01: Phase-8 async substrate (Wave 0) — newAsyncPool (non-blocking bounded ants/v2 pool, drop-on-overload via WithNonblocking mirroring world.Worker.Request) + the 3 asyncResult contracts (pathReady/trackerDiffReady/spawnCandidatesReady, each carries an id/value not a live pointer + re-validates on the owner + ships a validate-then-no-op applyTo stub for OPT-01/02/03) + asyncIn2 (SECOND rejoin channel, additive behind the UNCHANGED applyAsyncResults seam — no pipeline reorder, TestTickPhaseOrder passes). ants/v2 v2.12.1 + xsync/v4 v4.5.0 introduced for the first time (xsync justified-per-use only: asyncSubmitDrops Counter, NOT a blanket map swap). Docker -race clean. OPT-04/OPT-06 begin.
- [Phase 08]: OPT-02 async tracker carries a tracked DELTA (added/removed ids) in trackerDiffReady, not the whole set; applyTo updates p.tracked O(delta) on the owner so the map stays plain (Pitfall 1). — Mirrors the chunkReady rejoin: worker computes off-tick over an immutable snapshot, owner performs the only mutation in applyTo.
- [Phase 08]: OPT-04: snapshot-and-stay-plain — no tick-owned map converted to xsync; the OPT-01/02/03 owner-snapshot discipline means no worker reads a live collection (single-owner is faster). idAlloc stays atomic; asyncSubmitDrops (xsync.Counter, now tick-read) is the one justified xsync use; an AST gate enforces no live cross-boundary capture.
- [Phase 08]: 08-07: OPT-06 combined -race -count=10 gate over ./server/... ./world/... ./save/... is GREEN — every async subsystem (OPT-01 pathfinding, OPT-02 tracker, OPT-03 spawner) race-clean by construction under combined load; behavior regression proves the swaps are additive (paths arrive 1+ ticks late + followed, tracker emits spawn/move/despawn, mobs spawn under cap, .linear round-trips). OPT-06 is AUTOMATABLE (no Phase-8 plan touched an encoder/packet file — grep-confirmed — so the capture-diff goldens stay the wire authority and stay green), not a human-verify/capture-diff. Phase 8 COMPLETE.
- [Phase 09]: [Phase 9 / 09-01]: PARITY-01 DATA half — the FULL wired vanilla 26.2 overworld worldgen graph (120KB noise_router with aquifers/ore-veins enabled + the 35-file density_function tree INCLUDING the 6 cave functions + noise params + carver configs + 7594 baked biome climate boxes) is extracted offline from the pinned jar (pure-unzip + GenBiomeParams.java) and //go:embed-ed as DATA the later waves PARSE — never hand-transcribed. CAVES ARE IN THE GRAPH (final_density references overworld/caves/*). 26.2 renamed ResourceKey.location()->identifier(). Zero new runtime deps; pure Go; CGO_ENABLED=0 clean.
- [Phase 09]: 09-03: invert density node = 1.0/x (bytecode-verified DensityFunctions$Mapped INVERT), NOT -x as the plan/assumed-list stated — PORT-EXACT mandate overrode the prose; would have silently broken preliminary_surface_level.upper_bound
- [Phase 09]: 09-03: the 29-type overworld density node set ported constant-for-constant; CAVES come free as negative final_density from noise/shifted_noise/range_choice/spline cave functions (NOT weird_scaled_sampler, which is NOT in the overworld graph); only end_islands deferred; Parse errors loudly on unsupported types (T-9-07); TestParseFullGraphEndToEnd binds all 15 router functions
- [Phase 09]: 09-03: router lives in package router (world/levelgen/router/) not package levelgen — a composition root importing density cycles levelgen->density->synth->levelgen; RandomState seeds each noise via Xoroshiro(seed).forkPositional().fromHashOf(id) (the determinism hinge), implements density.NoiseBinder; NewRouter(seed) is pure, Docker -race clean
- [Phase 09]: 09-04: NoiseChunk (Tier-E) ported to package world/levelgen/noisechunk (NOT package levelgen) to break the density->synth->levelgen import cycle (same relocation as 09-03 router). Samples bound final_density on the coarse 5x5x49 cell-corner grid (cellWidth=4/cellHeight=8) via the ported NoiseInterpolator (Mth.lerp trilerp in doFill order) -> per-block density field (~1225 corner samples not 98K). Caves come free as negative density. Drives ONE interpolator over the WHOLE final_density (RESEARCH Pattern 2) since density has no mapAll and the plan forbids modifying it — corner-EXACT + sparse. Provisional stone/deepslate/water/air+bedrock fill (Wave 5 Aquifer replaces). Pure, zero deps, -race clean. — PARITY-01 Wave 3: the cell-sample+trilerp is the parity+perf hinge; package placement forced by the import cycle; whole-final_density sampling is the sanctioned RESEARCH approach given density's API surface.
- [Phase 09]: 09-06: WorldCarver pass ported — CaveWorldCarver (extra tunnel caves) + CanyonWorldCarver (RAVINES) run on top of the noise terrain, aquifer-aware (carve below the fluid level floods), replaceables-gated, cross-chunk-continuous ([-8,8] source-chunk range), deterministic via a ported java.util.Random LCG seeded by setLargeFeatureSeed(worldSeed+carverIdx, srcX, srcZ). The carver consumes the Wave-5 Aquifer through a FluidSource interface (computeSubstance is unexported in noisechunk) — the Generator wires it. Mineshafts-as-STRUCTURES remain DEFERRED (separate subsystem); ravines+caves (the carver class) are IN. Zero new deps.
- [Phase 09]: 09-08: NoiseGenerator assembles fill->surface->carve->heightmaps->biomes into a pure drop-in world.Generator. Generate is PURE (same seed+pos->identical bytes); worker/tick/Generator-interface/level.Chunk wire UNTOUCHED. Added Aquifer.CarveFluid as the exported carver.FluidSource seam. Zero new deps, CGO=0 clean, -race clean. (NOTE: the original 09-08 ran carve BEFORE surface; the post-gate fidelity fix (aa619b81) REVERSED this to NOISE->SURFACE->CARVERS to match vanilla ChunkStatus so carved openings expose bare stone — see the 09-09 fidelity decision below.)
- [Phase 09]: 09-09 / FIDELITY FIX (aa619b81, post-visual-gate audit vs the jar): TWO structural port divergences closed. (1) PER-MARKER INTERPOLATION — NoiseChunk drove ONE interpolator over the WHOLE final_density; vanilla's ctor calls noiseRouter.mapAll(this::wrap) replacing ONLY each Marker(Interpolated) subtree with its own NoiseInterpolator, every surrounding op (squeeze/min/the noodle cave graph) per-block. Because those ops are non-linear, trilerp(F(corners)) != F(trilerp(inner)) → the old shortcut smoothed cliffs + erased noodle caves. Ported DensityFunction.mapAll (density/mapall.go, bottom-up tree rewrite, identity-memoized for the shared DAG); the interpolator is now *interpolatedFn (a density.Function MapAll substitutes per interpolated marker; Compute returns the trilerped value inside the cell loop via a shared fillState.filling flag, samples its inner filler direct outside it — porting NoiseInterpolator.compute's ctx==this$0 discriminant); the overworld final_density has 5 interpolated markers (main density mul + 4 cave branches), each its own interpolator. TestNoiseChunkCornerExact (corner-exact) preserved. Per-block compute raised gen ~70ms→~116ms/chunk (vanilla's real cost; off-tick via Phase-8 async seams). (2) SURFACE-BEFORE-CARVE — Generate reordered to NOISE->SURFACE->CARVERS (vanilla ChunkStatus); carved openings expose bare stone instead of grass/dirt rims. Both fixes -race clean (Docker golang:1.26). VISUAL GATE APPROVED by the user.
- [Phase ?]: GEN2-03: built all 3 worldgen heightmaps (WORLD_SURFACE_WG/OCEAN_FLOOR_WG/MOTION_BLOCKING) from POST-CARVE terrain in the generator FINISH step (after ApplyCarvers)
- [Phase ?]: level.HeightmapUpdate ported jar-exact from Heightmap.update — pure/allocation-free/chunk-free (BitStorage + opaqueAt closure); built+tested now, wired by 10-03 + the feature phase (Phase 10 Decorate is a no-op)
- [Phase ?]: GEN2-02 seam = Split-Generate (staging map + single scheduler goroutine), not a write-buffer; Decorate is a no-op promote-to-full for Phase 10 with the late-write-after-emit rule deferred to Phase 11+
- [Phase ?]: handleTerrain routes region-hit vs generated on load provenance (fromRegion), not chunk Status, because the scheduler mutates a singleflight-shared staged chunk's Status to StatusFull (would otherwise double-emit)
- [Phase ?]: 11-02: SurfaceWaterDepthFilter ported as the JAR heightmap-difference (WORLD_SURFACE-OCEAN_FLOOR), not a block scan
- [Phase ?]: 11-02: VerticalAnchor below_top ported in full (added PlacementContext.Height gen-depth); ore height_range data requires it
- [Phase 11]: feature is its own package (acyclic, must NOT import placement); the placement modifier list is captured as PlacementModifierRaw for plan 11-02 to bind
- [Phase 11]: recognized-feature-type SET (54 types) derived from the distinct configured_feature type values; a genuinely-unknown type errors loudly (T-11-01)
- [Phase ?]: 11-03: D2 RESOLVED with Option Y (hold-until-neighborhood-complete) — decorate-on-own-3x3, emit-once-all-wanted-neighbors-decorated, no resend
- [Phase ?]: 11-03: FeatureSorter built over ALL biomes once at construction (cross-biome global per-step index); applyBiomeDecoration uses the global index + block origin per the JAR
- [Phase ?]: 11-03: placement.PlacerFunc exported bridge added so the world package supplies the configured-feature dispatch (the interface method is unexported)
- [Phase ?]: 12-01: NextBoolean/NextGaussian kept LegacyRandomSource-only (type-asserted), not on the RandomSource interface — lower churn
- [Phase ?]: 12-01: featureBody dispatch is an init()-populated registry from disjoint files (panics on duplicate); bodyContext plumbs g.deco.registry for nested sub-feature resolution
- [Phase ?]: 12-01: ClampedNormalInt confirmed jar-exact as f2i truncation (not round); would_survive/solid/replaceable are documented conservative ports
- [Phase ?]: OreFeature place->doPlace transcribed jar-exact from 26.2-inner.jar; the angle/y-jitter/per-step-radius/discard-on-air draw order is the FEAT-03 determinism contract
- [Phase ?]: RandomPatchFeature class removed from 26.2 (vegetation routes through simple_block + random_offset); body ported from the stable version-invariant algorithm, parser still recognizes random_patch
- [Phase ?]: 13-01: rule_based_state_provider tolerates an absent fallback via an identity fallback (the REAL 26.2 oak/birch below_trunk_providers carry only rules)
- [Phase ?]: 13-01: TreeConfiguration carries an OPTIONAL nil root_placer field + PlaceTree hook so 13-03 plugs mangrove without re-touching the struct
- [Phase ?]: 13-01: conservative validTreePos (air-or-leaves replaceable, logs not) — never a false placement over solid ground
- [Phase ?]: 13-02: ported the common overworld trunk/foliage placer roster + the common TreeDecorator subsystem; corrected BlobFoliage to the jar-exact corner nextInt(2) draw and reworked PlaceTree to the 26.2 doPlace order
- [Phase 13]: 13-03: PlaceTree reworked to the exact 26.2 doPlace order so trunk_offset_y (RootPlacer.getTrunkOrigin) sits between foliageRadius and the validity scan; the scan moved into PlaceTree
- [Phase 13]: 13-03: randomized_int_state_provider sets the int property by re-resolving the source {Name,Properties} with the property overridden (the source is always simple_state_provider in the data)
- [Phase 13]: 13-03: PaleMoss ground pale_moss_patch is a nested vegetation feature needing the chunk generator; the gate nextFloat is consumed but the patch is not placed (Known Stub); the trunk/leaves pale_hanging_moss drape is faithful
- [Phase ?]: STRUCT-01 structure pipeline (placement math + StructureStart cache + 8-radius compute-on-demand REFERENCES + heightmap-at-STARTS sampler + two-pass worker seam, zero blocks)
- [Phase ?]: 14-02: desert pyramid is the first true structure - StructurePiece machinery (placeBlock cross-chunk clip) + placeInChunk + full hardcoded postProcess, fingerprint-pinned
- [Phase ?]: 14-02: PostProcess takes a WorldGenView interface (Neighborhood satisfies it) - world->world/structure one-way, no import cycle
- [Phase ?]: 14-02: loot/redstone/suspicious-sand deferred v3 - chest BLOCK + loot tag, tnt + pressure-plate BLOCKS placed
- [Phase ?]: 14-03: jungle temple + swamp hut ported javap-c; salts 14357619/14357620 cross-checked, REAL biome gates, MossStoneSelector per-cell draws; igloo is the first addChildren multi-piece (dome always + nextDouble<0.5 basement), geometry extracted OFFLINE from the .nbt templates (0 .nbt runtime, deviation documented); STRUCT-02 closed, 4-temple acceptance + Docker -race green
- [Phase ?]: [Phase 15] 15-01: FIXED the mis-ported placement.go reducer table — un-swapped legacy_type_1<->legacy_type_3 + re-seeded legacyProbabilityReducerWithDouble (seed,chunkX,chunkZ); 4 bindings pinned. Mineshaft = FIRST recursive structure (corridor/crossing/room/stairs on addChildren+FindCollisionPiece, genDepth cap 8); placement is Pitfall #4 (legacy_type_3 0.4%% draw, NOT spacing grid). HasStructureBiomes resolves nested #is_* refs (mesa=#is_badlands). Cross-chunk idempotence proven on first many-chunk structure; Docker -race clean.
- [Phase ?]: Stronghold pieces reuse the 15-01 recursion verbatim (genDepth cap 50, weighted PieceWeight + maxPlaceCount, exactly one PortalRoom)
- [Phase ?]: placeLocal bbox-clip guard + idempotent PostProcess make the many-chunk stronghold cross-chunk idempotent; Phase 15 closed (STRUCT-03/04)
- [Phase ?]: 16-02: village jigsaw Placer is bounded-BFS via SequencedPriorityIterator (NOT recursion); 3 bounds (maxDepth + max_distance 80 + VoxelShape collision) + 1000-piece cap; villages deterministic per (seed,pos), salt 10387312 verified from villages.json
- [Phase ?]: GAMEPLAY-06: ItemEntity.DATA_ITEM is SynchedEntityData index 8 + EntityDataSerializers.ITEM_STACK serializer id 7 (javap-confirmed); ITEM value reuses component.SlotData ItemStack.OPTIONAL_STREAM_CODEC
- [Phase ?]: GAMEPLAY-06 drops use a v1 1:1 block->item map (blockDropFor), superseded by STRUCT-POLISH-01 loot evaluator
- [Phase 17]: 17-02 (GAMEPLAY-05): fluid scheduled-tick queue = per-gametime bucket map with deterministic packed-pos drain (the ServerLevel.scheduleTick analogue); schedule-on-change so worldgen oceans stay static until disturbed (Open Question 3)
- [Phase 17]: 17-02: canBeReplacedWith guard (never overwrite a source / downgrade a flow) is what terminates the FlowingFluid spread deterministically (Pitfall 2); isHole requires the cell itself passable so a flat floor spreads sideways
- [Phase 17]: 17-02: player fluid physics (0.8 getWaterSlowDown + 0.014 updateFluidInteraction push) ported + unit-tested, but the subtick.go call-site is DEFERRED (subtick.go is 17-03-owned this parallel wave)
- [Phase 18]: 18-01: EncryptionRequest is now the jar-exact 4-field ClientboundHelloPacket wire (String serverId, ByteArray publicKey, ByteArray challenge, Boolean shouldAuthenticate=true) — the trailing boolean was the single hard blocker against a real 26.2 client; always true because the server only sends Hello in online-mode (handleHello iconst_1). Challenge is the strict 4-byte Ints.toByteArray(nextInt()); hasJoined query is net/url-encoded. RSA PKCS1v15/1024-bit + AES-128/CFB8 + authDigest + cipher-then-auth ordering left FAITHFUL (untouched). --online-mode flag (default false) + SULFUR_ONLINE_MODE env threaded via newServer(gameplay, onlineMode); offline stays byte-identical. authentication() gained an injectable sessionServerURL test seam so the online-handshake integration test stubs sessionserver (CI offline). Skins ADD_PLAYER propagation is owned by the parallel 18-02 on disjoint files.
- [Phase 18]: 18-02 (ONLINE-01 skins): authenticated GameProfile properties (the hasJoined textures skin) now reach the ADD_PLAYER tab-list wire for SELF + every OTHER player. AcceptPlayer no longer DROPS them — tickPlayer + bootstrapParams gained a properties []user.Property field set at registration like name/uuid; playerInfoEntriesEncoder.WriteTo replaced the hardcoded VarInt(0) GAME_PROFILE_PROPERTIES count with a real count-prefixed loop via user.Property.WriteTo, which already == Property.STREAM_CODEC (String name/value/Optional<signature> = writeNullable, jar-verified vs ByteBufCodecs anon codec) so NO new per-property codec was written. Self-add passes params.properties (not empty) so the joiner sees its own skin. Offline -> nil -> count 0 (Steve/Alex, byte-identical). Strict round-trip test: count=1 signed online + count=0 offline. CGO=0 build/vet/test + Docker -race green.
- [Phase ?]: [Phase 19 / 19-03]: TUI-02 disconnect taxonomy COMPLETE — every player-drop seam tags its reason token (login_failure/protocol_mismatch/config_failure as server.go slog attrs pre-*Client; timeout at keepAliveClient.SendDisconnect; kicked best-effort at playerlist server-full + slog.Warn; protocol_error/write_error/backpressure tagged before Close in client.go readLoop/writeLoop/Send, first-writer-wins). readLoop splits clean EOF/stdnet.ErrClosed (default quit) from a decode fault (protocol_error). join/leave log.Printf -> slog (name/uuid/addr/reason/detail); reasonHuman (disconnect_reason.go) is the single token->human map; unknown passes through. KeepAlive.removePlayer double-leave hardened (ok-guard on listIndex[c]) — the Phase-3 deferred-item CLOSED. Docker -race ./server/ green; disjoint from 19-02.
- [Phase ?]: [Phase 20 / 20-03]: STRUCT-POLISH-04 StructureStart NBT persistence — ported createTag/loadStaticStart (flat {id,ChunkX,ChunkZ,references,Children}) + StructurePiece base {id,BB,O,GD}+addAdditionalSaveData per piece type (type-keyed LoadPiece). structures compound jar-exact {starts lowercase, References capital}. save/structure.go opaque per-start RawMessage (no cycle). Persistence is PURE optimization: absent/garbled tag -> recompute (A6/T-20-07), never panic; Children bounded 4096. Coherence proven: LoadStaticStart(CreateTag(s))==ComputeStarts. Spawn-guard slots left for 20-04. Docker -race green; CGO=0.
- [Phase 20 / 20-05]: STRUCT-POLISH-03 Beardifier terrain adaptation — ported Beardifier.compute + getBuryContribution (Mth.clampedMap length falloff) + getBeardContribution (24^3 BEARD_KERNEL gaussian + bit-exact fastInvSqrt falloff) 1:1 from the jar. terrain_adaptation read from the embedded structure JSON (codec default NONE): only village (beard_thin) RAISES + stronghold (bury) DIGS; temples/igloo/mineshaft/swamp-hut (NONE) stay BYTE-IDENTICAL (TestNonAdaptingUnchanged is the hard guard). ORDERING HAZARD resolved: STARTS computed PRE-fill (beardifierFor = ComputeStarts over C + the +-1 ring, pure/singleflight-memoized, NO neighbor gen) and the additive NON-interpolated beard term threaded into the noisechunk fill summation AFTER the trilerp (the BeardifierMarker substitution, A5) — NOT a router node, NOT the per-marker interpolator. The term is baked into nc.density so the aquifer/ore/heightmaps all read the beard-adjusted density (vanilla-faithful). afterPlace BURY is a NO-OP for these two (jar: only DesertPyramid/WoodlandMansion override afterPlace, for archaeology/cartography) — BURY is entirely the density term. W4: NO village/stronghold capture-diff golden exists (all goldens are packet-wire or Superflat) -> no re-seal needed. groundLevelDelta=0 (cited stub: surface-projected village + non-pool stronghold = jar-exact 0); JigsawJunction contribs omitted (no junction list yet). Docker -race ./world/ ./world/structure/ ./world/levelgen/noisechunk/ green; CGO=0; no new deps.
- [Phase ?]: [Phase 20 / 20-02]: shared evaluator consumers wired (block drops via loot.Roll, blockDropTable deleted; createChest emits chest BE {LootTable,LootTableSeed} with unconditional nextLong; chest_loot.go unpackLootTable lazy roll; jungle_temple fingerprint re-sealed; W2 SPLIT chest-OPEN UI to follow-up). Docker -race green.
- [Phase 20 / 20-04]: STRUCT-POLISH-02 structure inhabitant spawns — the off-tick->tick seam. ChunkResult gained Spawns []structure.SpawnRequest{EntityType id-string, X/Y/Z, PersistenceRequired}; WorldGenView gained RecordSpawn (live mob) + SetSpawner (mob_spawner BE) alongside 20-02's SetBlockEntity. PostProcess RECORDS a SpawnRequest -> Neighborhood buffers -> tryDecorate captures view.Spawns() onto the staged chunk -> tryEmit forwards onto ChunkResult.Spawns -> server.drainStructureSpawns (chunkReady.applyTo) resolves the id + NewEntity + entities.add on the TICK owner (TICK-05 / Pitfall 5: the worker NEVER touches the store); the tracker broadcasts AddEntity for free (the spawnBlockDrop path). Swamp hut: spawnWitch/spawnCat record a witch+cat at getWorldPos(2,2,5)+0.5, one-shot guarded (spawnedWitch/spawnedCat, persisted 20-03). Stronghold: PortalRoom places a SPAWNER block + mob_spawner BE set to silverfish (a BLOCK, NOT a live entity) — the hasPlacedSpawner guard is a RELOAD-ONLY skip (NOT set in-gen) so per-chunk re-runs stay byte-deterministic (box.isInside alone makes placement idempotent, the createChest discipline; the fingerprint/cross-chunk gates stay green). Village: StructureTemplate.PlaceEntities ports placeEntities — transformPos(blockPos) clip + the new transformVec3(float pos) (jar-verified $SwitchMap CCW90/CW90/CW180 +1 half-cell terms) + origin, reading the ALREADY-STORED template.entities (A4 resolved, no net-new extraction); singlePoolElement.Place places blocks THEN entities (cat_black/nitwit/villager). finalizeSpawn = cited vanilla-default stub. silverfish has no spawner-tick yet (cited). Capture-diff: no re-seal (the spawner BE rides the existing BlockEntity list encoder, spawns are server-side entities not chunk-wire). Docker -race ./server/ + ./world/structure/ green; the full ./world/ -race TIMES OUT (the 278s worldgen suite × race instrumentation, NOT a data race — zero DATA RACE reports). No new deps; no import C. STRUCT-POLISH-02 COMPLETE.
- [Phase ?]: [Phase 21]: 21-01 — go.starlark.net embedded as a PLAIN require (NOT a fork); research verified all three sandbox knobs (per-Thread step budget via SetMaxExecutionSteps, recursion-off via FileOptions zero value, no fs/net/eval builtin in the universe) are exposed upstream unpatched. Closes the v4 fork-or-vendor open decision as 'plain dep, no fork needed'.
- [Phase ?]: [Phase 21]: 21-01 — plugin/starlark is a LEAF package (imports only go.starlark.net + stdlib, never server/world/level). Sandbox policy is a single source of truth in runtime.go: every Thread via newThread() sets SetMaxExecutionSteps(stepBudget=10M); safeGlobals() curated StringDict is the entire reachable surface. Load(path) execs a .star ONCE via ExecFileOptions returning auto-frozen globals; LoadedPlugin.Call uses a fresh per-goroutine Thread. Negative sandbox tests + frozen -race test are Plan 02.
- [Phase ?]: [Phase 21] 21-02: sandbox negatives PROVEN — TestStepBudgetHalts asserts *EvalError 'too many steps' + ExecutionSteps()==50000 at a small test cap with a P1 guard (error NOT 'not within a function'); TestRecursionRejected -> 'function f called recursively'; TestNoIOBuiltins -> 'undefined: open'. Loop fixture loops INSIDE def spin() using for/range (P1+P2).
- [Phase ?]: [Phase 21] 21-02: frozen cross-goroutine PROVEN race-clean — TestFrozenCrossGoroutine (8 readers on the frozen 'result' global + 4 callers each on a fresh Thread via LoadedPlugin.Call) passes Docker -race (CGO=1) with no DATA RACE; CGO=0 ship build + CGO=0 test both green. PLUGIN-01 fully met; Phase 21 COMPLETE. Next: Phase 22 plugin host + event bus.
- [Phase ?]: Phase 22 / 22-01: plugin/host typed event bus = map[EventType][]Hook dispatched inline on the tick owner (NOT a channel/pub-sub broker). A2 shipped: plugin/starlark.LoadWith(path,extra) + exported SafeGlobals/NewThread let the host inject the register builtin into the predeclared set on a budget-bounded thread; Load delegates to LoadWith(path,nil) so Phase-21 stays unchanged. register captures a frozen starlark.Callable ONCE at load and rejects unknown event names at LOAD; Emit guards zero-subscribers BEFORE arg alloc, runs each hook on a fresh thread, isolates per-hook panic/error. Manifest is TOML (BurntSushi/toml) with required-field + path-traversal guards; capabilities parsed-not-enforced (Phase 23). pure-Go toml+fsnotify added, CGO=0 build clean, Docker -race TestEmitRace green. NO server wiring yet (Plan 02).
- [Phase ?]: [Phase 22 / 22-02]: PLUGIN-02 server wiring COMPLETE — host.Manager.Emit wired at the 8 named discrete seams (break->destroyBlock after removal, place->handleUseItemOn after reconcileEdit NOT the shared broadcaster, join/leave->drainRegistrations+removePlayer, spawn->drainStructureSpawns after entities.add, death->die, damage->actuallyHurt POST-mitigation, tick->tickOnce end zero-sub-guarded). Each guarded by if t.plugins != nil; payloads are frozen scalars (no live handles - Phase 23). NEVER from tickEntities/tickAI/tickPhysics anti-seam loops (grep-gated).
- [Phase ?]: [Phase 22 / 22-02]: THE GATE (TestBlockBreakEventFiresOnce) is the PLUGIN-02 architecture proof — a hook fires EXACTLY ONCE per real block break with 50 entities present, count entity-independent (event-driven, not per-tick-per-entity scan). on_damage = the FINAL post-mitigation amount from actuallyHurt (LOCKED), not the raw applyDamage input.
- [Phase ?]: [Phase 22 / 22-02]: FULL hot-reload (LOCKED) — plugin/host/reload.go fsnotify watcher rebuilds a fresh *Manager OFF-tick and sends the POINTER on pluginSwap; the tick owner is the SOLE writer of t.plugins and swaps on-thread in drainRegistrations. A reload concurrent with dispatch is a pointer swap between ticks, never a mid-Emit map mutation (T-22-05); TestReloadDuringDispatch -race clean. main() loads plugins/ + arms the watcher.
- [Phase ?]: [Phase 23 / 23-01]: SUB-ATTRIB coverage fix — 7 per-type suppliers ported 1:1 from jar (pig/cow/sheep/chicken/skeleton/creeper/spider) + livingFallbackSupplier so NewMapForEntity never returns nil for a living type; non-living stays nil. Thin id-not-pointer entity/world handles with capability enforcement at the handle-op boundary (Option A: handles in server).
- [Phase 24]: 24-01 PLUGIN-04 Wave-1 — per-entity seeded RandomSource (server/ai_random.go: entityRandom over a per-mob seeded math/rand/v2 PCG, the Mob.getRandom() analogue) replaces the shared package-global RNG in the 3 passive Pig goals, drawing in the EXACT bytecode order (javap-verified: RandomStrollGoal nextInt(interval) gate THEN getPosition 3x nextInt; LookAtPlayerGoal nextFloat roll then 40+nextInt(40); RandomLookAroundGoal nextFloat roll then nextDouble heading then 20+nextInt(20)). Deterministic for a fixed seed -> TestTickAIDrivesMobs flake RETIRED (5/5) + a shared-global-rand race removed. Per 24-RESEARCH Open-Q4 a seeded PCG (not a bit-exact LegacyRandomSource port) suffices since no test asserts bit-exact sequences. PLUS the 3 faithful handle extensions Phase-23 lacked: entity.set_look(yaw,pitch) writes headYaw+yaw+pitch instantly (entities.write, NaN-clamped); world.nearest_player(x,y,z,range) reuses nearestPlayerAt over t.players (world.read); entity.rand_int(n)/rand_float() draw the mob's own rng (UNGATED). Additive only; existing tests green; Docker -race clean. NO swap yet (Wave 2). FLAGGED for Plan 02: starlarkGoal does NOT honor requiresUpdateEveryTick (baseGoal default false) — RandomLookAround needs it. Commits 2d8d8ac9 + 2be8584c.
- [Phase ?]: [Phase 24]: 24-02 PLUGIN-04 dogfood — the vanilla Pig's 3 passive goals (Stroll@6 MOVE, LookAt@7 LOOK, LookAround@8 MOVE|LOOK) re-expressed 1:1 in plugins/vanilla_pig/main.star against the 26.2 jar (each cites its ai.goal.* class), //go:embed boot-loaded into a tick-owned registry (loud-fail if absent), SWAPPED in as the only pig (spawnVanillaPig replaces newPigAI at both spawn sites; the Go goals KEPT as the behavior-identical oracle). Two fidelity fixes: requires_update_every_tick threaded into starlarkGoal (RandomLookAround.requiresUpdateEveryTick==true) + a per-(mob,goal) get_state/set_state scratch (frozen-boundary fix). New seams: set_look_at (LookControl.setLookAt), rand_double (nextDouble), nav.stop, goal() tick-optional+can_continue; sandbox math module added. All 5 new goals DEFERRED cited (deferred-goals.md). TestPluginPigEqualsGoNativePig proves plugin==Go over 500 ticks; existing pig-AI suite passes UNCHANGED; CGO=0 green; Docker -race clean. Commits 56b2f88a + a66659b7 + 4a61e648.
- [Phase ?]: [Phase 25] 25-01: level/recipe embeds + parses ALL vanilla recipe types (the level/loot twin); the 1:1 match is javap-verified — ofPositioned bounding-box SHRINK, ShapedRecipePattern mirror-then-unmirror (index (mirror?width-col-1:col)+row*width), ShapelessRecipe multiset cover (bipartite == StackedItemContents.canCraft), SingleItemRecipe single-ingredient cooking/stonecutting. Match() returns result + a per-cell used mask mapped through (Left,Top) for Wave-2 consume. Item tags embedded INTO level/recipe to keep it server-pure; #tag expanded recursively. Special types parsed as markers via a type-first decode (smithing_trim pattern is a STRING). Commits ff62c439+e30bda2c+fbc4d67d.
- [Phase ?]: [Phase 25] 25-01: plugin/host gains the value-returning Match/Remaining seam (set_recipe_matcher + recipes builtins + SetRecipeTable) extending Phase-22 void Emit to query-resolution — the host READS the matcher's {id,count} dict back into Go. Fresh budget-bounded thread + recover; runaway matcher -> step budget *EvalError -> ok=false (T-25-01); readResult validates id>0 && count>0 (T-25-03). Lock-free write-at-load/read-on-tick; Unload drops the owned matcher.
- [Phase ?]: [Phase 25] 25-02: crafting THROUGH the plugin path (PLUGIN-05). ResultSlot.onTake UN-STUBBED — inventory_click.go slotOnTake routes a result-slot take to onTakeCraft; the 1:1 consume re-derives the asPositionedCraftInput footprint via the exported level/recipe.OfPositioned + removeItem(slot,1) per non-empty cell (GO applies the deltas over the tick-owned grid — NOT a plugin-supplied used mask, NOT a whole-new-grid return). The crafting_table block + 3x3 CraftingMenu clone the Phase-20 chest-open subsystem (openContainer.kind discriminator + transient craftGrid; close returns the grid via clearContainer, NOT chest persist). The vanilla recipe plugin is //go:embed'd + LoadCraftingPlugin boot-loaded (FATAL on no matcher) so a DEFAULT server crafts out-of-the-box; cmd/sulfur/main.go loads it into one Manager + the operator plugins/ on top. Custom-recipe model = matcher-fallthrough (set_recipe_matcher last-wins; customrecipe loads after crafting, checks custom-first then falls through to the 1:1 vanilla algorithm over recipes()). THE GATE green: vanilla (2 planks->4 sticks via Manager.Match + 1-per-cell consume) AND custom (1 dirt->1 diamond) both craft; Docker -race clean over ./plugin/... ./server/. Commits 9c3f3281+6a63d397+d1c994f5.
- [Phase ?]: [Phase 25] 25-03 PLUGIN-05 close: stonecutting BUILT (1:1 single-input recipe-picker StonecutterMenu, plugin-gated via Manager.Match + server-side selectByInput pick); smelting/blasting/smoking/campfire DEFERRED with cited evidence (no per-tick block-entity drive + no FuelValues table; BE types are empty markers) — deferral is the BLOCK UI only, every deferred matcher tested headless (TestSmeltingMatcherShips). New ServerboundContainerButtonClick dispatch + ClientboundContainerSetData encoder. Commits 14966823 9a5fee10 6d2ca968.
- [Phase 26]: 26-01 PLUGIN-06 isolation primitive — the build-tag stub/impl split is THE gate. plugin/python has runtime_python.go (//go:build python, imports gopython.xyz/py/v14 PINNED to the python3.14 branch commit b0bdc04a384b, pseudo-version v14.0.0-alpha.0.0.20260510154237-b0bdc04a384b; InitAndLock-once + RunFile + GIL-held CallHook) + runtime_stub.go (//go:build !python, cgo-free, ErrNotBuilt), byte-identical exported surface (Available/Runtime/Load/Close/CallHook). gopy is a DIRECT require in go.mod but reachable ONLY behind the tag, so CGO_ENABLED=0 go build ./... stays pure-Go static with ZERO gopython in the import graph (go list -deps grep EMPTY on every task — the #1 gate). The host routes runtime=python via a plain-Go PythonRuntime/PythonPlugin interface + SetPythonRuntime seam (the concrete gopy impl registers from a tagged adapter), so plugin/host + server stay cgo-free; the bare `runtime != "starlark"` skip became a switch (starlark inline / python via the interface, loaded-with-tag or logged+skipped without it / unknown runtime errors loudly — T-26-05). Wave-2 stub (cited, behind the tag only): the register-capture harvesting in Load. The -tags python link is gated to the python3.14 Docker image (no libpython3.14 on Windows — the documented split, like -race). Commits c7ca602b + e3fa8968 + cccb5be3.
- [Phase ?]: [Phase 26] 26-02 PLUGIN-06 off-tick python lane LIVE: a runtime=python plugin's hook runs OFF the tick goroutine via submitOrDrop(t.pluginPool) (small ants pool) → GIL-held CallHook on a LockOSThread worker (plugin/python, //go:build python) → t.asyncIn2 <- pythonHookReady → applyAsyncResults on the owner → applyTo (telemetry-only, no world mutation; the pathReady discipline). SAME register(event,fn) API routed by manifest.Runtime (register_python.go injects the builtin via gopy NewCFunction; host.Emit→emitPython offers every discrete event to each python plugin off-tick via SetPythonDispatch→submitPythonHook). pythonHookReady+submitPythonHook+pluginPool are DEFAULT-BUILT cgo-free (host.PythonPlugin interface, gopy one hop away inside CallHook); only WirePython (gopy loader+dispatch registration, async_python_python.go) is build-tag split. SUB-INTERPRETERS: serialized one-interpreter FALLBACK, CITED — gopy@python3.14 (pinned b0bdc04a384b) exposes ZERO sub-interpreter surface (whole-module grep empty in .go AND cgo headers; GIL model is single-interpreter PyGILState_Ensure/Release; alpha) — NOT faked; pool sized small; the gate passes regardless of N-way parallelism. THE #1 GATE green every task (CGO=0 build + zero gopython in graph). -tags python build/-race Docker-gated. Commits 771ef48d+ee114ac8+9f44f918.
- [Phase 27]: 27-01 REGION-01 step 1 of 3 — extracted the region struct at N=1. The WORLD-half of TickLoop (entityStore, ChunkManager+worker, chunkReady bridge, block/fluid scheduled ticks, levelRandom, spawn-scan gate) moved onto a `region` struct; TickLoop keeps the global/coordination fields (idAlloc, players+network, frozen *host.Manager, the ONE 50ms gametime anchor, on_tick, console). At N=1 there is exactly one region (globalRegion) reached via `t.only()`/`region()`; the store stays SINGLE-SOURCED (no aliased store — T-27-EXT-1). BEHAVIOR-NEUTRAL: the full server+world+plugin suite passes UNCHANGED, no test needed a behavior edit. Docker -race ./server/ GREEN (~19s); ./world/... GREEN at -timeout 2400s (the 900s gate is too small under the race detector — a pre-existing timeout, world untouched by this plan, logged to deferred-items.md). Per-region levelRandom is never shared. NO new dep (conc lands in Plan 02). Commits 99e373e0 + 5070f649 + bbfbf458.
- [Phase ?]: 27-02: conc (sourcegraph/conc v0.3.0, pure-Go CGO=0) drives the per-tick fan-out/join barrier — the Folia coordinator at N=1, behavior-neutral
- [Phase ?]: 27-02: tickOnce = drain -> read shared gt -> conc.WaitGroup.Go(r.tick) per region -> wg.Wait BARRIER -> cross-region post-phase -> gametime++ EXACTLY once; shared 50ms anchor advanced ONLY by the coordinator
- [Phase ?]: 27-02: a region panic is recovered by conc + the hoisted recoverTick backstop (advanced flag = exactly-once gametime advance); the tick survives and never hangs (T-27-02)
- [Phase ?]: PLUGIN-07 bot visual gate PASSES: cmd/testbot -mode gate triggers + asserts all 4 plugin-system items on observed proto-776 packets, exit 0 (28-02)
- [Phase ?]: chat() Starlark host builtin + installable server sink (broadcastSystemChat) makes a plugin event hook's reaction observable on the wire as ClientboundSystemChat (28-02)
- [Phase ?]: run-gate.sh is OFFLINE by design so the offline-login bot can connect (run-debug.sh online-mode would reject it); dev-only gate launch, T-28-07 accept (28-02)
- [Phase ?]: operator LoadDir now skips+logs a per-plugin load failure and continues (was aborting the whole scan on the duplicate plugins/vanilla_pig); strict boot-load LoadDirWith unchanged (28-02)
- [Phase ?]: Phase 29-01: damage_type ids assigned by sorted-element index (dynamic registry absent from registries.json); DamageTypeIDs/DamageTypeNames expose name<->id mapping so consumers resolve by name, never magic ids
- [Phase 29]: 29-02: mob hurt pipeline is a sibling *Entity port of combat.go (applyDamageEntity/actuallyHurtEntity 1:1 with LivingEntity.hurtServer/actuallyHurt; combat.go helpers reused, not duplicated)
- [Phase 29]: 29-02: lastDamageSource stored as a real damageSource{typeTag,attacker} value (MOB-SUB-02), not a faked hurt flag; is(tag) reads data/tag.DamageTypeTags genuinely
- [Phase ?]: 29-03: cross-region damage routes via the OWNER region barrier-queue (queueDamageIntent on the source region, drained at applyCrossRegionDamage next to applyCrossRegionTransfers) — the project's first true cross-region write; the same/cross split uses the attacker's region, never t.cur()
- [Phase ?]: 29-03: mob knockback + sweep deferred (cited) — the routed hit lands (damage + lastDamageSource + on_damage emit) but the post-hit mob impulse is a follow-on; was_hurt handle attr = hurtTime>0
- [Phase ?]: Phase 29-04: dieEntity ports LivingEntity.die 1:1 — death removal from the OWNER region (regionForEntity, not cur) auto-broadcasts RemoveEntities via the tracker; loot/XP route through the owner region; the dead guard prevents double-death. A4 resolved as a bounded loot extension (entity-table handlers read cited-stub v1 defaults so the pig drops 1-3 raw porkchop). Pig XP = Animal.getBaseExperienceReward 1 + random.nextInt(3).
- [Phase ?]: 30-01: getFluidJumpThreshold ports the real jar formula getEyeHeight()<0.4?0.0:0.4 (pig=0.4, NOT assumed 0.0); eye height=height*0.85 default until per-type read lands
- [Phase ?]: 30-01: lava decode-only (full read, never const-false); lava flow sim (FlowingFluid.tick, getDropOff=2) DEFERRED — FloatGoal only reads lava
- [Phase ?]: 30-02: jump chain ported 1:1 — fluidJumpImpulse is the exact jar double 0.03999999910593033 (not 0.04); jumpFromGround uses max(0.42, vy); the JUMP slot is RNG-free so the pig oracle stays byte-identical
- [Phase 30]: FloatGoal@0 wired lockstep on the Go-native pig + the vanilla_pig plugin in one plan; the DRY oracle (canUse false -> zero new draws) stays byte-identical
- [Phase 30]: Plugin FLUID_JUMP_THRESHOLD=0.4 (lockstep with the Go oracle's getFluidJumpThreshold==0.4 for the pig), not the plan's assumed 0.0; the wet test is a differential (FloatGoal pig descends slower than a goal-stripped control) since vanilla water buoyancy is deferred
- [Phase ?]: P31-01: PanicGoal@1 (MOVE, 1.25) wired Go+plugin in lockstep; shouldPanic = hasLastDamage && is(panic_causes)
- [Phase ?]: P31-01: panic speedModifier 1.25 cited-deferred; isOnFire+lookForWater cited false-stubs (zero RNG for a non-burning pig)
- [Phase ?]: Phase 32-01: TemptGoal@4 ×2 (carrot id 887 + pig_food tag) wired on the pig in Go newPigAI + both byte-identical .star copies; S4 held-item read (main+off hand) + itemInTag membership built (nearestPlayerHolding is a sibling, not a mutation; host owns the item-id set, .star reads tuple-or-None; canScare=false continue==canUse). Oracle byte-identical, Docker -race clean. The S4 read is the live dependency for Phase 33 love-on-feed.
- [Phase ?]: In-love hearts use ClientboundEntityEvent(id, 18), NOT ClientboundLevelParticles — the server never wires the aiStep hearts (Level.addParticle is a server no-op); javap-corrected (33-02)
- [Phase ?]: Pig playEatingSound is a no-op (Pig has no override; base is empty) — feeding a pig emits NO sound; javap-corrected (33-02)
- [Phase ?]: encodeLevelParticles built as the javap-confirmed ClientboundLevelParticles wire-out + byte-tested (33-02)
- [Phase ?]: 33-03: breed() RNG order is variant nextBoolean() THEN XP 1+nextInt(7), both on the initiator's RNG (jar-verified) — the lockstep order 33-04 mirrors onto both .star pigs
- [Phase ?]: 33-03: BreedGoal@3 + FollowParentGoal@5 added GO-NATIVE only (C1/C2 split); the oracle stays GREEN because both goals are canUse-gated dormant on the un-fed lone-adult pig
- [Phase ?]: 33-04: the .star breed routes through ONE host try_breed (= TickLoop.breed) so the variant nextBoolean()+XP nextInt(7) draws are byte-identical to the Go pig by construction (the lockstep)
- [Phase ?]: 33-04: is_in_love/is_baby/breed_age are .star READ accessors (no parens); nearest_breeding_partner/nearest_adult_parent/try_breed are bound methods (parens) — the call-vs-read slip is masked by the dormant oracle, caught by the live drive
- [Phase ?]: Phase 33 HEADLESS dogfood gate GREEN (33-05): the 9-goal pig oracle byte-identical over 500 ticks + the end-to-end breed/age/follow/grow scenario + boot-load-9; Docker -race clean. Phase 34 BLOCKED on the live bot dogfood pass.
- [Phase ?]: Same-region breeding cut + baby eye-height + pig-variant-assign + Age/InLove NBT persist documented as accepted v5 deviations in 33-deviations.md (never silent; each cited + a fidelity path).
- [Phase ?]: TestPerfGate is a pre-existing flaky wall-clock perf gate (passes 5/5 isolated; spikes only under full-suite parallel contention); not a 33-05 regression (test-only change). Real fix: assert per-stroll Starlark alloc-count vs wall-clock ns.
- [Phase ?]: 34-00: parameterized nearest_player_holding_food(tag,range) + sheep eat seam (DATA_WOOL setSheared) + sheep shear (white-wool, 5 nextFloat/stack scatter) + chicken aiStep (slow-fall vy*0.6 + egg-lay 2nf/nextInt6000); shared wave-1 infra, pig oracle byte-identical
- [Phase ?]: 34-02: sheep EatBlockGoal as plugin callbacks (nextInt gate via entity.rand_int, lockstep, identity adjustedTickDelay 1000/50; eat via the 34-00 host seam); shear+regrow closes MOB-PASS-02 on a real plugin sheep; no shared Go edit, pig oracle byte-identical
- [Phase ?]: 34-03: chicken CREATURE asserted via data/entity Chicken.Type, not a categoryOf() shared edit
- [Phase ?]: 34-03: chicken dogfood test loads on-disk plugins/vanilla_chicken via loadMobRegistry + spawnDeclaredMob (no new Go embed file)
- [Phase ?]: 34-01: tryMilkCow ports AbstractCow.mobInteract 1:1 (bucket->milk_bucket + COW_MILK 449, NO RNG); e.typ==entity.Cow.ID gate, additive, pig oracle byte-identical
- [Phase ?]: 34-01: createFilledResult ports ItemUtils.createFilledResult 1:1 (single->replace hand, stack->shrink+inventoryAdd+drop fallback)
- [Phase ?]: 34-01: categoryOf(Cow)->CREATURE (data/entity Cow.Type=='creature'; vanilla EntityType.COW MobCategory.CREATURE), additive
- [Phase ?]: Phase 35-02: shipped the FORCED gametime-darkness proxy for hostile spawn gating (t.gametime night window 13000..23000) — no light engine exists; cited Monster.isDarkEnoughToSpawn, structured to become a real sky-light read.
- [Phase ?]: Phase 35-02: Mob.checkDespawn DEFERRED (no noActionTime / per-mob nearest-player despawn subsystems); the MONSTER cap re-check bounds spawns (the anti-flood) — recorded, not faked.
- [Phase ?]: MOB-SUB-10 targetSelector: NearestAttackableTargetGoal canUse uses the FULL nextInt(10) NOT reducedTickDelay 5 (faithful under full-rate serverAiStep); the targetSelector is a second independent goalSelector ticked in jar order; doHurtTarget routes through the player hurt path with a host-set mob_attack source.
- [Phase ?]: Spider leap gate is nextInt(reducedTickDelay(5))=>raw 5, NOT nextFloat — the exec-time decompile corrected the JARNOTES guess; the 1:1 jar mandate made the bytecode authoritative.
- [Phase ?]: SpiderAttackGoal daylight behavior is the stochastic 1/100 canContinueToUse flee when bright, not a flat no-attack-in-daylight block — wave-1 stub rewritten to match the bytecode.
- [Phase ?]: Phase 35 complete: the 3 hostiles boot-load into the ONE registry via the //go:embed extension; /dbg zombie|skeleton|spider added; embed-vs-root byte-identity + pig-oracle byte-identity + Docker -race all GREEN; live bot verified a zombie hunts + deals real melee (health 19.3->0.0).
- [Phase ?]: Wolf taming ported into attack_dispatch.go as tryWolfInteract; setTame/applyTamingSideEffects inlined as file-local helpers (wave-2 disjointness)
- [Phase 36]: Wolf gate (LAST v5 plan): the anger focused-RNG test replays the full post-hit wolf stream (nextInt(381) anger then hurt-sound nextFloat x2) so the lockstep follow-up pins the single anger draw despite the survival hurt-sound also drawing from the same mob stream

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- Bleeding-edge risk: proto 776 post-dates go-mc, the wiki, and training data. All exact 776 byte layouts must be jar-derived/capture-diffed, not asserted from memory. Affects Phases 1, 2, 4, 5.
- Phases flagged for deeper per-phase research (`/gsd-research-phase`): 1 (codegen retarget), 2 (Configuration registry set), 4 (chunk section layout), 5 (teleport/spawn layout), 9 (vanilla-parity worldgen).
- **FLAKE (own it, fix after v4 autonomous run):** `TestServerAiStepWalksToGoalTarget` (server/navigation_test.go, the AI-nav subsystem) intermittently fails under full-suite load, passes 5/5 on re-run + in isolation + at the pre-Phase-22 parent commit. NOT a Phase-22 regression (the PLUGIN-02 seams are additive guarded one-liners). Pre-existing low-frequency AI-navigation timing flake — investigate + harden the AI-nav test (timed receive / determinism) once the v4 phase chain completes.

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| Rename | Project rename Ender → **Sulfur**: module `imhinotori/go-mc` → `imhinotori/sulfur` (367 .go + both go.mod), `cmd/ender` → `cmd/sulfur`, user-facing strings. MC entity names + frozen 774 baseline left as-is. | ✅ Done (630f90e3) | Phase 2 (NET-04) |
| Robustness | `KeepAlive.removePlayer` (verbatim fork component) derefs `listIndex[c]` and would panic if `ClientLeft` is called for a player the keep-alive already kicked on a real 30s timeout. Cannot trigger in Phase 3 (no timeout in the milestone window); `keepalive.go` is consumed verbatim by mandate. Harden against a double-leave in the phase that adds real timeout-driven disconnects. | ✅ Done (ed106fe3) — Phase 19 (19-03): `removePlayer` ok-guards `listIndex[c]` (mirrors the tickPlayer guard, non-panicking; missing key returns early). `TestRemovePlayerDoubleLeave` proves a double-leave does not panic. | Phase 3 (03-03) → closed Phase 19 (19-03) |
| Pulled-forward | A MINIMAL Play-state Join Game bootstrap (`server/play_join.go`, 0fd96850) was pulled forward in 04-04 to enable the real-client visual milestone. Phase 5 was to **EXTEND** it (full profile/abilities/spawn/teleport-validation/movement) not duplicate it. | ✅ Done — Phase 5 (05-01/02/03) completed the full Player Session: movement + following ring, the early-Play tail (abilities/held-slot/PlayerInfoUpdate-self/spawn-pos), incrementing teleport-id validation, and the capture-diff-sealed encoders. Bootstrap extended, not duplicated. (Real inventory still belongs to Phase 6.) | Phase 4 (04-04) → closed Phase 5 (05-03) |
| Gameplay | Block survival — a broken support did NOT destroy the unsupported plant above ("rompe un bloque con flores arriba, las flores no se rompen", 17-05 gate finding). | ✅ Done for VEGETATION (8b799d42) — `block.IsVegetation`/`IsDoublePlant` + `updateVegetationOnEdit` (server/block_survival.go) wired into `reconcileEdit`; 1:1 `VegetationBlock.canSurvive`/`DoublePlantBlock.canSurvive`/`Block.updateOrDestroy` (recursion 512), reuses GAMEPLAY-06 drop. Build/vet/test + Docker -race green. **Torches/rails/redstone/doors + dry/flowerbed/mangrove/seagrass plants still DEFERRED** (different survival classes — a follow-up). | Phase 17 (17-05) → vegetation closed this session |
| Visual gate | Phase 20 (20-VERIFICATION `human_needed`): structure terrain-fit (village beard / stronghold bury) + inhabitant render (witch/cat/villager/silverfish-spawner) are autonomous:false visual gates — proven numerically (TestVillageBeardRaises/TestStrongholdBuries/TestNonAdaptingUnchanged + spawn race tests) but the on-screen fit/render needs a human eye. Chest loot OPEN/roll CONFIRMED LIVE this session. | ⏳ Acknowledged + deferred at v3 close — accepted as out-of-band visual debt (like GAMEPLAY-07/ONLINE-01 gates, all operator-confirmed live where possible). Numeric proof + green suites stand in. | Phase 20 (20-05) → deferred at v3 close 2026-06-27 |

## Session Continuity

Last session: 2026-06-30T18:07:59.722Z
Stopped at: Completed 34-01-PLAN.md (cow)
Resume file: None
Next: OPERATOR CHECKPOINT (19-02 Task 3) — build `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur`, run `./sulfur.exe -seed 777` in a REAL terminal (expect alt-screen TUI: log viewport + command input), type `say hi`+Enter (expect a `console command cmd=say hi` viewport line), connect a vanilla 26.2 client (expect a join line), Ctrl-C (clean exit), then `./sulfur.exe -seed 777 | cat` (expect NO TUI, plain stderr — today's behavior). On "approved" → mark TUI-01 complete + advance the plan counter, then proceed to Plan 19-03 (gameplay_tick.go join/leave slog conversion + the full disconnect taxonomy). The console line routes TUI→tick (EnqueueConsoleCommand, cap 64, drop-on-full)→runConsoleCommand on the tick→existing graph (grant-all, no issuer), reply to slog. gameplay_tick.go is untouched (19-03 owns it). LEGACY: Phase 17 Wave 2 (17-02/17-03) — see prior continuity below. (GAMEPLAY-05 fluid simulation: OVERWRITE server/fluid.go with the FlowingFluid port + scheduled-tick queue; lazy-init t.fluidSchedule inside tickFluids, do NOT edit tick.go/tick_phases.go) and 17-03 (GAMEPLAY-04 fall damage + PvP dispatch: OVERWRITE server/fall_damage.go using the tickPlayer fallDistance/wasOnGround/lastY fields + the lookupPlayerByEntityID reverse lookup, do NOT edit tick.go/tick_phases.go). The exact Wave-2 seam surface (field names, init point, call sites, stub signatures) is in 17-01-SUMMARY.md "WAVE-2 HANDOFF". Deferred-still-open: dungeon loot/spawner-mob + BeehiveDecorator occupant + pale_garden PaleMoss (all v3, cosmetic, in 13-04-SUMMARY); KeepAlive double-leave hardening (Phase 3). KNOWN PRE-EXISTING FLAKE: TestTickAIDrivesMobs (OPT-01 async-pool timing, not caused by 17-01) intermittently fails under full-suite load; passes in isolation + 3× under -race.
