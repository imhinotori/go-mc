---
gsd_state_version: 1.0
milestone: v3
milestone_name: Online-mode + Operator UX + Structure polish
status: verifying
stopped_at: 20-03 (STRUCT-POLISH-04 StructureStart NBT persistence) COMPLETE + GAP-CLOSED — the persistence seam is now WIRED into the runtime: worker.go decodeAndSeed seeds the cache from sc.Structures on region load (ReadChunkStructures, via the structureCacheHolder interface NoiseGenerator satisfies), world/chunk_save.go SerializeChunkData emits the structures compound on save (WriteChunkStructures). Reload integration test (TestStructureStartsSurviveReloadWithoutRecompute) proves starts survive a reload WITHOUT recompute (recompute-spy = 0); tag-less reload recomputes (no panic). CGO=0 build/vet/test green, Docker -race green. SC4 now met in the running server. Original seam: f8c17b9b, 85f35998, 531a0c6f.
last_updated: "2026-06-27T06:17:39.774Z"
progress:
  total_phases: 4
  completed_phases: 4
  total_plans: 15
  completed_plans: 31
  percent: 100
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-23)

**Core value:** A Go server that an unmodified vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — architected from day one for Leaf-style async optimizations.
**Current focus:** Phase 20 — structure-polish-loot-inhabitants-beard-persistence

## Current Position

Phase: 20 (structure-polish-loot-inhabitants-beard-persistence) — EXECUTING
Plan: 5 of 5 (+ 20-06 chest-OPEN UI follow-up landed)
Status: Phase complete — ready for verification

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

Progress: [██████████] 100%

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

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

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

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- Bleeding-edge risk: proto 776 post-dates go-mc, the wiki, and training data. All exact 776 byte layouts must be jar-derived/capture-diffed, not asserted from memory. Affects Phases 1, 2, 4, 5.
- Phases flagged for deeper per-phase research (`/gsd-research-phase`): 1 (codegen retarget), 2 (Configuration registry set), 4 (chunk section layout), 5 (teleport/spawn layout), 9 (vanilla-parity worldgen).

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| Rename | Project rename Ender → **Sulfur**: module `imhinotori/go-mc` → `imhinotori/sulfur` (367 .go + both go.mod), `cmd/ender` → `cmd/sulfur`, user-facing strings. MC entity names + frozen 774 baseline left as-is. | ✅ Done (630f90e3) | Phase 2 (NET-04) |
| Robustness | `KeepAlive.removePlayer` (verbatim fork component) derefs `listIndex[c]` and would panic if `ClientLeft` is called for a player the keep-alive already kicked on a real 30s timeout. Cannot trigger in Phase 3 (no timeout in the milestone window); `keepalive.go` is consumed verbatim by mandate. Harden against a double-leave in the phase that adds real timeout-driven disconnects. | ✅ Done (ed106fe3) — Phase 19 (19-03): `removePlayer` ok-guards `listIndex[c]` (mirrors the tickPlayer guard, non-panicking; missing key returns early). `TestRemovePlayerDoubleLeave` proves a double-leave does not panic. | Phase 3 (03-03) → closed Phase 19 (19-03) |
| Pulled-forward | A MINIMAL Play-state Join Game bootstrap (`server/play_join.go`, 0fd96850) was pulled forward in 04-04 to enable the real-client visual milestone. Phase 5 was to **EXTEND** it (full profile/abilities/spawn/teleport-validation/movement) not duplicate it. | ✅ Done — Phase 5 (05-01/02/03) completed the full Player Session: movement + following ring, the early-Play tail (abilities/held-slot/PlayerInfoUpdate-self/spawn-pos), incrementing teleport-id validation, and the capture-diff-sealed encoders. Bootstrap extended, not duplicated. (Real inventory still belongs to Phase 6.) | Phase 4 (04-04) → closed Phase 5 (05-03) |
| Gameplay | Block survival — a broken support did NOT destroy the unsupported plant above ("rompe un bloque con flores arriba, las flores no se rompen", 17-05 gate finding). | ✅ Done for VEGETATION (8b799d42) — `block.IsVegetation`/`IsDoublePlant` + `updateVegetationOnEdit` (server/block_survival.go) wired into `reconcileEdit`; 1:1 `VegetationBlock.canSurvive`/`DoublePlantBlock.canSurvive`/`Block.updateOrDestroy` (recursion 512), reuses GAMEPLAY-06 drop. Build/vet/test + Docker -race green. **Torches/rails/redstone/doors + dry/flowerbed/mangrove/seagrass plants still DEFERRED** (different survival classes — a follow-up). | Phase 17 (17-05) → vegetation closed this session |

## Session Continuity

Last session: 2026-06-27T00:00:00.000Z
Stopped at: STRUCT-POLISH-01 chest-OPEN UI COMPLETE — the 20-02 W2 split landed. Right-click a structure chest -> resolve chest BlockEntity -> lazy-roll {LootTable, LootTableSeed} (one-shot unpackLootTable) -> allocate windowId (nextContainerCounter 1..100) -> ClientboundOpenScreen(generic_9x3) + ContainerSetContent(27 chest + 36 player). ContainerClick moves items (PICKUP/QUICK_MOVE/THROW) over the shared cursor; ContainerClose frees the windowId + items persist in t.openChests across opens. New: server/chest_open.go + server/chest_click.go; openScreen encoder (slot_encode.go); tickPlayer.openContainer/containerCounter + TickLoop.openChests. 1:1 jar-cited (ChestBlock.useWithoutItem, ServerPlayer.openMenu/nextContainerCounter, ClientboundOpenScreenPacket, ChestMenu slot layout). 8 chest tests green; CGO_ENABLED=0 build/vet/test green; Docker -race ./server/ green; no new deps. Committed 10157c6b (open path) + 87fc2681 (click/close + tests). SUMMARY: .planning/phases/20-structure-polish-loot-inhabitants-beard-persistence/20-06-SUMMARY.md. DEFERRED (cited): vanilla LootTable.fill random-slot shuffle (sequential placement), chest-item flush to BE NBT on unload/save (in-memory only), multi-viewer sync (single-viewer shipped). --- PRIOR: BLOCK-SURVIVAL (vegetation) COMPLETE — the Phase-17 deferred gate finding closed for vegetation. `block.IsVegetation`/`IsDoublePlant`/`SameDoublePlant` (level/block/utilfuncs.go) + `updateVegetationOnEdit`/`destroyUnsupportedVegetationAbove` (server/block_survival.go) wired into reconcileEdit (server/block_interact.go) alongside the fluid notification. Breaking a block under a flower/sapling/grass/fern/bush/2-tall-plant now destroys + drops the unsupported plant (cascading 2-tall plants via the bounded 512 recursion), reusing the GAMEPLAY-06 spawnBlockDrop path + broadcastBlockUpdate(air). 1:1 cited vs the jar (VegetationBlock.updateShape/canSurvive, DoublePlantBlock.canSurvive, Block.updateOrDestroy). Tests: server/block_survival_test.go + level/block/vegetation_test.go. Gates: CGO_ENABLED=0 build/vet/test green, Docker -race ./server/ ./level/... green. Committed 8b799d42 (code) + the docs commit (SUMMARY/STATE/deferred-items). STILL DEFERRED: torches/rails/redstone/doors + the dry/flowerbed/mangrove/seagrass plants (different survival classes). SUMMARY: .planning/phases/17-gameplay-completion/17-22-SUMMARY.md. --- PRIOR: 20-03 (STRUCT-POLISH-04 StructureStart NBT persistence) COMPLETE — all 3 tasks committed (f8c17b9b, 85f35998, 531a0c6f), Docker -race ./world/structure/ ./save/ green, SUMMARY written. Spawn-guard slots left in pieceExtraData for 20-04.
Resume file: None
Next: OPERATOR CHECKPOINT (19-02 Task 3) — build `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur`, run `./sulfur.exe -seed 777` in a REAL terminal (expect alt-screen TUI: log viewport + command input), type `say hi`+Enter (expect a `console command cmd=say hi` viewport line), connect a vanilla 26.2 client (expect a join line), Ctrl-C (clean exit), then `./sulfur.exe -seed 777 | cat` (expect NO TUI, plain stderr — today's behavior). On "approved" → mark TUI-01 complete + advance the plan counter, then proceed to Plan 19-03 (gameplay_tick.go join/leave slog conversion + the full disconnect taxonomy). The console line routes TUI→tick (EnqueueConsoleCommand, cap 64, drop-on-full)→runConsoleCommand on the tick→existing graph (grant-all, no issuer), reply to slog. gameplay_tick.go is untouched (19-03 owns it). LEGACY: Phase 17 Wave 2 (17-02/17-03) — see prior continuity below. (GAMEPLAY-05 fluid simulation: OVERWRITE server/fluid.go with the FlowingFluid port + scheduled-tick queue; lazy-init t.fluidSchedule inside tickFluids, do NOT edit tick.go/tick_phases.go) and 17-03 (GAMEPLAY-04 fall damage + PvP dispatch: OVERWRITE server/fall_damage.go using the tickPlayer fallDistance/wasOnGround/lastY fields + the lookupPlayerByEntityID reverse lookup, do NOT edit tick.go/tick_phases.go). The exact Wave-2 seam surface (field names, init point, call sites, stub signatures) is in 17-01-SUMMARY.md "WAVE-2 HANDOFF". Deferred-still-open: dungeon loot/spawner-mob + BeehiveDecorator occupant + pale_garden PaleMoss (all v3, cosmetic, in 13-04-SUMMARY); KeepAlive double-leave hardening (Phase 3). KNOWN PRE-EXISTING FLAKE: TestTickAIDrivesMobs (OPT-01 async-pool timing, not caused by 17-01) intermittently fails under full-suite load; passes in isolation + 3× under -race.
