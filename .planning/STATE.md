---
gsd_state_version: 1.0
milestone: v2
milestone_name: worldgen-features-structures
status: executing
stopped_at: "Phase 10 COMPLETE (3/3 plans): GEN2-01 LCG+WorldgenRandom (java.util.Random golden-exact), GEN2-03 HeightmapUpdate+WG heightmaps, GEN2-02 cross-chunk worker seam (split Generate, staging scheduler, Neighborhood 3×3 proxy, no-op Decorate, emit-once; -race -count=10 Docker green). Autonomous 11→16 (no pauses except blockers; server runs in bg). Next: plan→plan-check→execute Phase 11 (Feature Pipeline & Decoration Orchestration, FEAT-01/02)."
last_updated: "2026-06-25T05:05:00.000Z"
last_activity: 2026-06-25
progress:
  total_phases: 7
  completed_phases: 1
  total_plans: 3
  completed_plans: 3
  percent: 100
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-23)

**Core value:** A Go server that an unmodified vanilla Minecraft 26.2 client can connect to, log into, and play in a persistent, ticking world — architected from day one for Leaf-style async optimizations.
**Current focus:** **FIRST PLAYABLE ACHIEVED.** Phase 5 (Player Session / First Playable) COMPLETE — a real vanilla 26.2 client connects, logs in, and WALKS AROUND a following, ticking world; it is listed in the tab list and is not kicked. **The project is now playable end-to-end.** Next: Phase 6 (entities / inventory / persistence) — to be planned.

## Current Position

Phase: 5 of 9 (Player Session in World / First Playable) — ✅ COMPLETE
Plan: 3 of 3 complete (05-01, 05-02, 05-03 all done)
Status: Phase complete — ready for verification
Last activity: 2026-06-25

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
| Robustness | `KeepAlive.removePlayer` (verbatim fork component) derefs `listIndex[c]` and would panic if `ClientLeft` is called for a player the keep-alive already kicked on a real 30s timeout. Cannot trigger in Phase 3 (no timeout in the milestone window); `keepalive.go` is consumed verbatim by mandate. Harden against a double-leave in the phase that adds real timeout-driven disconnects. | ⏳ Deferred | Phase 3 (03-03) |
| Pulled-forward | A MINIMAL Play-state Join Game bootstrap (`server/play_join.go`, 0fd96850) was pulled forward in 04-04 to enable the real-client visual milestone. Phase 5 was to **EXTEND** it (full profile/abilities/spawn/teleport-validation/movement) not duplicate it. | ✅ Done — Phase 5 (05-01/02/03) completed the full Player Session: movement + following ring, the early-Play tail (abilities/held-slot/PlayerInfoUpdate-self/spawn-pos), incrementing teleport-id validation, and the capture-diff-sealed encoders. Bootstrap extended, not duplicated. (Real inventory still belongs to Phase 6.) | Phase 4 (04-04) → closed Phase 5 (05-03) |

## Session Continuity

Last session: 2026-06-25T04:55:30.973Z
Stopped at: Completed 10-02-PLAN.md (GEN2-03: HeightmapUpdate primitive + OCEAN_FLOOR_WG/MOTION_BLOCKING post-carve build)
Resume file: None
Next: v1 is done end-to-end (login → biome-varied noise world with caves/ravines/aquifers/ore-veins → entities/AI/inventory/combat → async-optimized, -race clean). The next milestone is DEEPER GAMEPLAY (user-flagged, deferred): items/crafting, more mobs + their ported AI, block mechanics (redstone/farming/fluids), and the Phase-9 v2 deferrals (ONLINE-01/02 auth+encryption, REGION-01 Folia-style regionization, trees/vegetation/structures as feature+structure subsystems). Run /gsd-new-milestone to scope it. Deferred-still-open: KeepAlive double-leave hardening (Phase 3, surfaces when real timeout-driven disconnects land).
