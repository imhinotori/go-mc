# Sulfur — Session Handoff (2026-06-27, v3.1 subsystems + live-bug hunt)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD build. **Branch `ender-776`.** Push target **`development`** (NOT main). Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE
**ALL GAMEPLAY/PROTOCOL LOGIC IS A LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`.** Verify bytecode BEFORE writing: `JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`. Cite the class/method. Only OPTIMIZATION (provably identical behavior) permitted. CGO_ENABLED=0 stays clean. NO Claude attribution in commits.

**THE OPERATOR'S STANDING ORDER (verbatim intent):** "Cuando revises, ve si realmente tu implementacion es como la de Java, algo estas evaluando mal... no andes inventando cosas." → DO NOT assume. For every live bug, INSTRUMENT IN-GAME (server-side log of the actual state-id / packet / value), decompile the jar method, and prove the Go matches BEFORE editing. My recent chest/skin fixes did NOT work because I reasoned offline instead of capturing the live state. Stop doing that.

## STATE OF THE TREE
- HEAD = `a3f4d336`. Build CGO=0 exit 0; `go vet` clean; server/world/level suites green.
- Tree CLEAN (everything committed). `.exe`/`.log`/web/.claude untracked (benign).
- STALE gopls floods FALSE diagnostics (undefined uniqueLevelRandomSeed / packetid.ServerboundAttack / save.SavedTickNBT etc.) — ALL FALSE; trust `go build`/vet/test, NEVER gopls.

## COMMITS THIS SESSION (newest first)
| Commit | What |
|--------|------|
| a3f4d336 | **skin layers (hats)** — DATA_PLAYER_MODE_CUSTOMISATION (idx 16) from ServerboundClientInformation; test-kit gains chest/cane/sand/water. |
| 8e6ab4d2 | **chest BE-on-place** — create empty ChestBlockEntity on place (ChunkManager.SetBlockEntityAt) + swapped-heightmap-key fix. ⚠ DID NOT FIX the open bug live (see below). |
| f4f84f59 | **chunk streaming** — PlayerChunkSender flow control (1:1) + mixed-provenance stall fix + RetryStale + workerBuf 256→1024. FIXED the "invisible chunk" bug; operator confirmed "se ve bien". |
| ec0f38a6 | docs: v3.1 backlog → 5 subsystems DONE. |
| (earlier) | **v3.1 milestone**: SUB-ATTRIB, SUB-FACESTURDY, SUB-ITEMNBT, SUB-PERSIST, SUB-BLOCKTICK — all 1:1 ports, merged. |

## ⚠ OPEN BUGS — operator-reported, live, NOT fixed. Each needs IN-GAME instrumentation, not offline reasoning.

### BUG-1: Placed chest does NOT open (right-click does nothing; with a non-chest block in hand the right-click REPLACES the chest)
- **Symptom (operator):** right-click a placed chest = nothing. Holding any non-chest block + right-click = the chest gets replaced by that block.
- **Diagnosis (proven by the "replaces" symptom):** `useBlockInteraction` (chest_open.go:105) is returning FALSE for the placed chest → the click falls through to PLACEMENT. It returns false at `!isChestBlock(state)` (line 111) — so **the block state at the clicked cell does NOT have `.ID() == "minecraft:chest"`**.
- **Two suspects (MUST instrument live to pick):**
  1. `blockStateForItem` (server/block_place.go:40) for the `item.Chest` resolves to a state-id whose `Chest{}` block carries non-default properties → but `isChestBlock` only checks `.ID()`, which is property-independent, so this should still match. UNLESS `blockStateForItem(Chest)` returns a NON-chest state (wrong byItem mapping).
  2. The `hitPos` passed to `useBlockInteraction` is NOT the chest cell (off-by-one on the clicked face — vanilla useItemOn uses `getClickedPos()` = the face-relative pos; a placed chest is AT clickedPos, but the OPEN should target the clicked block, not the placement offset). **This is the likely bug:** handleUseItemOn computes `placePos` (clicked + face offset) for placement, but `useBlockInteraction` must use the CLICKED block pos (the chest), NOT placePos. Verify which pos it gets.
- **NEXT STEP:** add a one-line `udebug` in `useBlockInteraction` logging `hitPos`, `state`, `block.StateList[state].ID()`; place + click a chest in-game; read the log. Also log `blockStateForItem(item.Chest)` result + its `.ID()`. The jar spec is at `.planning/research/chest-open-jar-spec.md` (ChestBlock.useWithoutItem, LevelChunk.setBlockState newBlockEntity — already ported the BE creation; the OPEN dispatch is what's mis-targeted).
- **The BE-creation fix (8e6ab4d2) is correct + needed** (a placed chest now records an empty BE), but the OPEN never fires because useBlockInteraction returns false first. Fix the pos/isChestBlock issue and the chest opens.

### BUG-2: Naturally-generated water (ravines/caves) does NOT expand — it should flow as part of vanilla worldgen
- **Operator clarified:** NOT about water buckets. Means aquifer/cave/ravine water that generates should spread to fill, like vanilla. Today it sits static until a manual block update.
- **Where it lives:** `server/fluid.go postProcessChunkFluids` (line 166) ports `LevelChunk.postProcessGeneration` — it kicks `FluidState.tick` ONCE on the aquifer-flagged border cells (`Chunk.PostProcessFluids`). If water doesn't expand, either (a) the aquifer is NOT flagging the right border cells into `PostProcessFluids`, or (b) `postProcessChunkFluids` isn't being called / the cells don't schedule follow-up spread.
- **NEXT STEP:** decompile `net.minecraft.world.level.chunk.LevelChunk.postProcessGeneration` + the aquifer's `markPosForPostProcessing` / `shouldScheduleFluidUpdate` to confirm WHICH cells vanilla flags, then verify Sulfur's `Chunk.PostProcessFluids` list is populated the same way during gen (world/noisechunk/aquifer.go). Spec started in `.planning/research/fluid-blockupdate-jar-spec.md` (FlowingFluid.tick/spread/spreadTo, getTickDelay=5). Instrument: log `len(ch.PostProcessFluids)` for a ravine chunk + whether postProcessChunkFluids runs.

### BUG-3: Sugar cane does NOT cascade (break the bottom, the top stays)
- **Operator:** break the lower cane, the upper one does NOT break.
- **Spec finding (fluid-blockupdate-jar-spec.md):** the cascade code (`reconcileEdit → onBlockTickEdit → sugarCaneTick → scheduleTick → canSurvive → destroyBlock → recurse`) is ALREADY wired + looks correct. So the bug is upstream:
  - (a) the in-game BREAK might not funnel through `reconcileEdit` (check block_break.go:370 calls it for a cane break), OR
  - (b) the predicates `block.IsSugarCane` / `IsSupportsSugarCane*` (level/block/sugarcane.go via server/sugar_cane.go) resolve the WRONG 26.2 state-ids, so `onBlockTickEdit` never matches the cane above.
- **NEXT STEP:** instrument `onBlockTickEdit` / `destroyUnsupportedVegetationAbove` to log when a cane break fires and what the block ABOVE resolves to (is `IsSugarCane(above)` true?). Decompile `SugarCaneBlock.updateShape` + `canSurvive` + verify the Go predicates match the jar's `BlockTags`/state checks. This is a VERIFICATION bug (predicates), not a logic rewrite.

### BUG-4: Hats (skin overlay) work CONDITIONALLY
- Fixed for the PLAY ServerboundClientInformation path (a3f4d336). Gap: if the client reports skin parts ONLY in CONFIG state (before play), `displayedSkinParts` stays 0 → no layers. To fully fix: also capture `modelCustomisation` in `server/configuration.go:136` (the config ClientInformation handler, currently consumes+ignores) and thread it into AcceptPlayer → tickPlayer.displayedSkinParts. Operator said hats "work" then "stopped" — likely the config-vs-play timing.

### BUG-5: Chunk-persist respawn CRASH (client IndexOutOfBounds in PalettedContainer.read) — UNRESOLVED, persist is OFF
- With `SULFUR_PERSIST_CHUNKS=1`, pressing RESPAWN crashed the client decoding a LevelChunkWithLight (readerIndex+8 > writerIndex — a short/mis-sized section long-array on the wire). Offline round-trip (gen→SerializeChunkData→region→ChunkFromSave→WriteLevelChunkWithLight) is SIZE-SAFE for sections/biomes/heightmaps (proven exhaustively). The crash is RUNTIME-specific (a chunk the player edited, or the chest-Items BE NBT, or a specific section state). **Persist is currently OFF** in the running server to avoid it. To fix: reproduce with a bot that edits blocks + revisits, OR add server-side validation that decodes every outbound LevelChunkWithLight and logs a malformed one. Region reload path (tryRegion → decodeAndSeed) is the prime suspect.

## HOW TO RUN / TEST
- Build: `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur` (+ `-o testbot.exe ./cmd/testbot`).
- Run (current live config — NO persist, online, test-kit): `SULFUR_ONLINE_MODE=1 SULFUR_TEST_KIT=1 ./sulfur.exe --online-mode -seed 777`
- Test kit hotbar: 1-3 food, 4 cobble, 5 planks, 6 torch, 7 dirt, **8 chest**, **9 sugar cane**; inv: sand(11), water bucket(12).
- Structure chest (loot): mineshaft at **(258, 39, 189)** seed 777 → `/tp 258 39 189`.
- Firehose: `SULFUR_ULTRA_DEBUG=1` (cats: packet/move/water/tick/fluid/edit/combat/bandwidth). Add `udebug("cat", ...)` for one-shot probes.
- Bot: `./testbot.exe -name X -mode goto|hold|wander -x -y -z -ticks N -probe "x z"`. The bot now ACKs ChunkBatchReceived (needed for the throttle).
- Locate structures: `go run ./cmd/locate -seed N -radius CHUNKS`.
- -race (Docker, warm module cache): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 1800s ./PKG/`.

## JAR SPECS READY (verified bytecode, in .planning/research/)
- `chest-open-jar-spec.md` — ChestBlock.useWithoutItem/getMenuProvider, LevelChunk.setBlockState newBlockEntity, ChestMenu.
- `fluid-blockupdate-jar-spec.md` — Level.setBlock flags (1=NEIGHBORS, 16-clear=updateShape), FlowingFluid.tick/spread/spreadTo (getTickDelay=5), SugarCaneBlock cascade via updateShape.
- `SUB-ATTRIB / SUB-FACESTURDY / SUB-ITEMNBT-jar-spec.md` — the v3.1 subsystem ports.

## v3.1 STATUS
5 subsystems DONE + merged (ATTRIB, FACESTURDY, ITEMNBT, PERSIST, BLOCKTICK). Chunk-streaming fix landed. Remaining = the 5 live bugs above (all gameplay-completeness / persist-hardening). v3 milestone close still waits on operator visual sign-off.

## RESUME HERE
1. **BUG-1 chest** first (most diagnostic): instrument `useBlockInteraction` (log hitPos + state ID) + `blockStateForItem(item.Chest)`. The "replaces" symptom proves it returns false → likely the OPEN targets `placePos` (face-offset) instead of the CLICKED chest cell. Fix the pos.
2. Then BUG-3 cane (verify predicates resolve real 26.2 state-ids), BUG-2 natural water (aquifer PostProcessFluids flagging), BUG-4 hat-in-config, BUG-5 persist crash.
3. For EVERY bug: instrument live → decompile jar → prove Go matches → THEN edit. No offline assumptions.
