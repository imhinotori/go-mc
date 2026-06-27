# Sulfur — Session Handoff (2026-06-27, autonomous session: v3 CLOSED + polish)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD build. **Branch `ender-776`.** Push target **`development`** (CI→`:latest`→watchtower→demo.trysulfur.net:25565), NOT main. `git push origin ender-776:development`. Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE
**ALL GAMEPLAY/PROTOCOL LOGIC IS A LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`** (`javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`). Verify bytecode BEFORE writing. Cite the class/method. Only OPTIMIZATION (provably identical behavior) permitted. CGO_ENABLED=0 stays clean. NO Claude attribution in commits.

## WHERE WE ARE — v3 IS CODE-COMPLETE (all 4 phases)
- **Phase 17 (Gameplay completion):** DONE. Visual gate passed earlier.
- **Phase 18 (Online-mode):** DONE, verified `passed`. Operator confirmed offline-client rejection live.
- **Phase 19 (Operator UX — TUI):** DONE, verified `passed`. Operator-validated.
- **Phase 20 (Structure polish):** DONE, verified `human_needed` (only the real-client visual gate remains; operator AFK). All 4 STRUCT-POLISH reqs met incl the SC4 persistence-wiring gap I closed.
- **v3 MILESTONE INTEGRATION AUDIT: `passed`** — all 7 cross-phase flows WIRED (`.planning/v3-MILESTONE-AUDIT.md`). Zero integration gaps. Remaining gating = operator visual sign-off only.

## WHAT THIS AUTONOMOUS SESSION LANDED (all committed, all gates green)
| Commit | What |
|--------|------|
| 4da824f1 | **/say + /me broadcast** — were no-op stubs; now `[Server]/[name] msg` (vanilla SayCommand/emote via SystemChat). |
| 8febd813 | **Head-yaw fix** — `p.headYaw=p.yaw` in MovePlayerPosRot/Rot (vanilla setYHeadRot); other players' heads now track the camera (RotateHead fires). |
| ba0afe79 | **Suffocation** — ported LivingEntity.baseTick IN_WALL branch (1.0 dmg, isInWall + isSuffocating proxy). |
| 0475c872 | **SetHealth bandwidth FLOOD fix** (the "1470MB at login") — syncFood compared the exact saturation float (dirty every tick) → SetHealth spam ~20×/s. Vanilla compares the saturation-IS-ZERO bool. Added an ULTRA_DEBUG byte-counter (server/debug_bandwidth.go). |
| f145f2fa | **Trees-on-trees fix** — would_survive accepted ANY non-air below; now checks `block.IsVegetationGround` (#substrate_overworld). |
| 4073459f | **Aquifer global picker** — returned air below min(-54,sea); vanilla returns LAVA (underwater air-pocket source). |
| 20-01..20-05 + gap | **Phase 20:** loot evaluator (level/loot, LegacyRandomSource LCG, golden via independent Python LCG trace) → block-drop rewire + lazy chest loot → StructureStart NBT persistence (WIRED into worker decode/save) → Beardifier (village beard_thin + stronghold bury) → structure inhabitant spawns (witch/cat/villagers live, silverfish spawner; ChunkResult.Spawns → tick drain). |
| 928fb0bd | **SC4 gap-closure** — persistence was built+tested but unwired; now ReadChunkStructures on decode + WriteChunkStructures on save, reload test proves 0 recomputes. |
| (locate) | **cmd/locate** — structure locator tool + `NoiseGenerator.LocateStructures`. Coords saved `structure-coords-seed777.txt`. |
| eaecde48 | **Deflake** TestBehaviorRegressionPathArrives (non-blocking ants pool drops paths under contention → assert "eventually navigates", 4000-tick cap). |
| 8b799d42 | **Block-survival (vegetation)** — the "flores no se rompen" gate finding CLOSED for vegetation: breaking a support destroys+drops the plant above (VegetationBlock.canSurvive + updateOrDestroy + 2-tall cascade, recursion 512). Reuses IsVegetationGround + the GAMEPLAY-06 drop path. |
| (pending) | **Block-entity persistence + nbt list-Marshaler fix** — `ChunkToSave` now serializes `block_entities` (full-metadata `id`/`x`/`y`/`z` merge, ports `BlockEntity.saveWithFullMetadata`); `chunkSaveShape` carries the tag so `SerializeChunkData` persists chest loot tables + spawners. ROOT-CAUSE: nbt `Encoder` list loop called `writeValue` (not `marshal`), re-encoding each `[]RawMessage` element as a `{Type,Data}` struct → corrupted block_entities AND the `structures` NBT. Fixed to route list elements through `marshal` (honors the Marshaler interface). Tests: `level.TestChunkBlockEntityRoundTrip` + `world.TestBlockEntitySurvivesReload` (real SerializeChunkData→region .mca→worker load). NOTE: chest-item disk flush still blocked (no chunk-flush save loop caller + no ItemStack disk codec); BE table+seed now round-trips (unopened chest re-rolls deterministically = vanilla-correct). |
| 10157c6b..3a20872e | **Chest-OPEN UI (20-06)** — right-click a chest → resolve BE → unpackLootTable lazy-roll (one-shot) → ClientboundOpenScreen(generic_9x3) + ContainerSetContent(27 chest + 36 player slots) → container clicks move items → close persists + frees windowId. ChestBlock.use/ServerPlayer.openMenu/ChestMenu 1:1. STRUCT-POLISH-01 chest-loot now user-visible. Deferred: random-slot shuffle (cosmetic), chest-item flush to BE NBT on save (in-memory this session), multi-viewer sync. Bot smoke: break/place/wander clean after the handleUseItemOn change. |

## BOT-VERIFIED LIVE (autonomous, seed 777, via testbot)
- Server boots + ticks stable with all v3 code (0 crashes, 0 races).
- Online-mode: offline client correctly REJECTED ("Invalid session" / login error EOF).
- Nearest mineshaft (258,~39,189): bot tp'd in, got 71KB chunks, **suffocation fired** underground.
- Swamp hut (804,48,99): bot reached it; **2 distinct moving entities** (witch+cat spawn, STRUCT-POLISH-02) ticking + broadcast via tracker. Villages at (-321,-815) + (-1241,-515).
- say/me, head-yaw all confirmed by the user on a real client earlier.

## STILL OPEN
- **Two human visual gates** (operator AFK): GAMEPLAY-07 (re-confirm) + Phase 20 real-client (open a chest → per-seed loot; villagers/witch/cat alive; structures fit terrain) + the deferred premium-login + cross-player skin render. NO code blocks them.
- **Deferred debt (no scope/decision needed, pure code):**
  - ~~chest-OPEN UI~~ **DONE this session (20-06)** — only the on-disk chest-item flush (items persist in-memory per session; BE NBT carries only table+seed), random-slot shuffle (cosmetic), and multi-viewer sync remain as small follow-ups.
  - **block-survival non-vegetation classes** — torches/rails/redstone/doors/wall-mounts + plants on different ground predicates (DryVegetation/seagrass/lily_pad/mushrooms/crops). `destroyUnsupportedVegetationAbove` (server/block_survival.go) is the template to extend. (Vegetation DONE.)
  - finalizeSpawn variant/profession stub (mobs spawn at vanilla defaults); mob_spawner runtime tick; a production chunk-FLUSH caller of SerializeChunkData (READ side live).
  - mob-water-nav (mobs walk on water — AI track); DATA_SHARED_FLAGS sprint/sneak pose; async-fluid (gated on cost data).
- **v4 (Plugins)** is PLANNED in `.planning/v4-PLAN.md` (Phases 21-28, Starlark+Python) — NOT started, separate milestone, needs the new-milestone workflow + user scope confirm. Do NOT auto-start.
- Push to `development` still pending (the attendly env-gate hook blocked earlier pushes).

## STATE OF THE TREE
- Build CGO=0 exit 0; `go vet ./...` clean; full suite green; Docker -race green (server + level + world packages).
- Many `.exe`/`.log` + web/, .claude/ untracked (benign). `structure-coords-seed777.txt` committed.
- STALE gopls floods FALSE diagnostics (undefined GoldenDandelion/LootContext/RecordSpawn, mapView mismatches, packetid.ServerboundAttack) — ALL FALSE; trust `go build`/vet/test.

## TOOLS
- `./sulfur.exe [--online-mode] -seed N` (rebuild: `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur`). `SULFUR_ULTRA_DEBUG=1` firehose (cats: packet/move/water/tick/fluid/edit/combat/eat/bandwidth). `SULFUR_TEST_KIT=1` (food + blocks + /tp).
- `./testbot.exe -name X -mode goto|walk|wander -x -y -z -cmd "tp x y z" -probe "x z" -ticks N`.
- `go run ./cmd/locate -seed N -radius CHUNKS` → structure coords.
- `-race`: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./PKG/`.

## RESUME HERE (when operator returns)
1. **Run the v3 visual gates** on a real client (the only thing blocking milestone close): open a chest (loot once the chest-OPEN UI lands — OR confirm the gen-time store), see villagers/witch/cat, structures fit terrain, premium login + skins.
2. If satisfied → `/gsd-complete-milestone v3` (archives v3, tags). Then v4 kickoff (`/gsd-new-milestone` — DESTRUCTIVE, resets STATE to v4; the v4-PLAN.md is ready).
3. If autonomous work is wanted before then: the next pure-code items are **block-survival non-vegetation** classes (torches/rails/redstone/doors), the **chest-item on-disk flush** (small — write the rolled items into the BE NBT on save), and the **finalizeSpawn variant/profession** real read (needs the attribute subsystem). The chest-OPEN UI is DONE.
