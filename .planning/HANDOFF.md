# Sulfur — Session Handoff (2026-06-27, 5 live bugs FIXED)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD build. **Branch `ender-776`.** Push target **`development`** (NOT main). Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE
**ALL GAMEPLAY/PROTOCOL LOGIC IS A LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`.** Verify bytecode BEFORE writing: `JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`. Cite the class/method. Only OPTIMIZATION (provably identical behavior) permitted. CGO_ENABLED=0 stays clean. NO Claude attribution in commits.

**OPERATOR'S STANDING ORDER (verbatim):** "Cuando revises, ve si realmente tu implementacion es como la de Java... no andes inventando cosas." → INSTRUMENT/REPRODUCE before editing. This session every fix was reproduced with an offline probe/round-trip test BEFORE the edit, then locked with a regression test. That worked — all 5 bugs are now root-caused and fixed.

## STATE OF THE TREE
- HEAD = the BUG-5 persist-palette commit. Build CGO=0 exit 0; `go vet ./level/ ./server/` clean.
- `level`, `server` suites GREEN + **-race clean (Docker)**. `world` suite green (slow ~5min noise gen).
- STALE gopls floods FALSE diagnostics (undefined block.DefaultStateID / save.SavedTickNBT / uniqueLevelRandomSeed / GoldenDandelion etc.) — ALL FALSE; trust `go build`/vet/test, NEVER gopls.
- Worktree dirs `D:\ender-wt-persist` / `-blocktick` are stale leftovers (gopls "not in workspace" noise) — safe to `git worktree remove` if desired.

## ✅ THE 5 LIVE BUGS — ALL FIXED THIS SESSION (newest commit first)
Each was reproduced offline first, then fixed 1:1, then locked with a regression test.

### BUG-5: persist respawn crash (client IndexOutOfBounds in PalettedContainer.read) — FIXED
- **Root cause (reproduced):** a section edited past 256 distinct block states uses the in-memory **globalPalette** (direct: data = raw state ids, empty palette list). `ChunkToSave` wrote that empty list + direct data into Anvil `block_states` — but **Anvil has no direct format**, block_states ALWAYS carries an explicit palette the data indexes into. So it saved a 0-length palette + non-empty data (invalid). On reload the NETWORK constructor rebuilt a globalPalette that returned the data value AS the state id → garbage ids → client crash on the next send.
- **Fix (two-sided):** WRITE (`writeStatesPalette`) rebuilds an explicit Anvil palette from the distinct states + re-indexes the data (max(4,ceil(log2 n)) bits) when the palette is global. READ (`NewStatesPaletteContainerFromSave` + biome twin in `level/palette.go`) resolves Anvil indices→state ids and rebuilds the SAME representation generation produces (linear/hash ≤256, globalPalette+raw-ids >256), so in-memory reads AND network WriteTo are both correct. `block.IsAir` now bounds-checks (no panic on a corrupt id).
- **Lock test:** `level/chunk_wire_roundtrip_test.go` — a 600-distinct-state section survives ChunkToSave→ChunkFromSave→network WriteTo→client-side ReadFrom with every state intact, no IndexOutOfBounds. **Persist is now SAFE: `SULFUR_PERSIST_CHUNKS=1`.**

### BUG-3: sugar cane didn't cascade — FIXED
- **Root cause:** `LevelTicks.Schedule` drops a scheduled tick if its chunk has no registered container (vanilla `if (c==null) return`). Sulfur only registered a container on the persist-LOAD path (`loadChunkBlockTicks`, which had NO live callers) — so freshly generated/streamed chunks dropped EVERY `scheduleBlockTick`. The cane cascade (break bottom → schedule cane-above destroy) was discarded before the next tick.
- **Fix:** `chunkReady.applyTo` (the worker rejoin) now calls `ensureChunkBlockTicks(pos)` registering an empty container for every integrated chunk — vanilla addContainer's every loaded chunk. (`server/block_ticks.go`, `server/tick.go`.) The cane logic itself was already a correct 1:1 port; the schedule just never survived.
- **Lock test:** `TestChunkReadyRegistersBlockTickContainer` — a chunk integrated via `chunkReady.applyTo` (no manual register) keeps a live scheduleBlockTick.
- NOTE unwired: saved block-tick restoration from disk (`loadChunkBlockTicks`) still has no caller; `level.Chunk` carries no `block_ticks` field. Live scheduling works; persisted ticks don't reload yet.

### BUG-1: placed chest didn't open (and got replaced) — FIXED
- **Root cause:** `blockStateForItem` resolved the held item's block via the Go ZERO-VALUE struct (`block.FromID[name]`), whose enum fields are 0. A chest's default state is FACING=NORTH (zero Direction is `down`, an INVALID chest state) → `ToStateID` missed → placeState ok=false → chest never placed → a follow-up right-click fell through to placement (the "replaced" symptom).
- **Fix:** codegen-emitted `block.DefaultStateID` map (the state flagged `default` in blocks.json = `Block.defaultBlockState()`), used by `blockStateForItem`. General fix — EVERY block with non-zero default props now places. (`tools/gen_blocks.go` regenerated `level/block/blocks.go`; `server/block_place.go`.)
- **Lock test:** `TestPlaceChestPlacesRealChest` — place chest item → real minecraft:chest (state 3988) → right-click opens.
- NOTE unwired: chest `getStateForPlacement` should face the player (opposite look dir); currently always NORTH (cosmetic; deferred).

### BUG-2: naturally-generated water didn't expand — FIXED
- **Root cause:** the aquifer flags unstable border cells into `Chunk.PostProcessFluids` (verified: 294 cells in a seed-777 ravine chunk) and `postProcessChunkFluids` kicks `FluidState.tick` per cell. But the kick fired INLINE during `chunkReady.applyTo`, while chunks integrate ONE AT A TIME — a cell that must flow ACROSS a chunk edge read the neighbor as unloaded and the spread died at the border. (Within-chunk spread worked, which is why it looked like "doesn't expand unless I update by hand".)
- **Fix:** defer each flagged cell onto the fluid schedule queue (`gametime+1`) instead of firing inline, so the load batch settles + neighbors are present first. One FluidState.tick per cell still (one pass later — invisible, getTickDelay=5). (`server/fluid.go`.)
- **Lock test:** `TestPostProcessChunkFluidsExpandsNaturalWater` — a flagged chunk integrated via chunkReady expands water 4/4 onto a flat shelf, no manual update.

### BUG-4: hats (skin overlay) worked only conditionally — FIXED
- **Root cause:** a 26.2 client reports modelCustomisation (skin layers) in a CONFIG-state Client Information packet, before PLAY. Sulfur consumed-and-ignored the config packet and only read parts from the (later/sometimes-absent) PLAY packet → spawn metadata had displayedSkinParts=0.
- **Fix:** capture modelCustomisation in the config drain loop (same field layout as the PLAY handler) and thread it `AcceptConfig → AcceptPlayer → tickPlayer.displayedSkinParts` so `newPlayerEntity` bakes the overlay into spawn metadata. The PLAY handler still refreshes live. (`server/configuration.go`, `server/server.go`, `server/gameplay.go`, `server/gameplay_tick.go`; interface+stubs updated.)
- **Lock test:** `TestConfigCapturesSkinParts` — a config Client Information with modelCustomisation 0x7F is captured.

## HOW TO RUN / TEST
- Build: `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur` (+ `-o testbot.exe ./cmd/testbot`).
- Run (now SAFE to enable persist): `SULFUR_ONLINE_MODE=1 SULFUR_TEST_KIT=1 SULFUR_PERSIST_CHUNKS=1 ./sulfur.exe --online-mode -seed 777`
- Test kit hotbar: 1-3 food, 4 cobble, 5 planks, 6 torch, 7 dirt, **8 chest**, **9 sugar cane**; inv: sand(11), water bucket(12).
- Structure chest (loot): mineshaft at **(258, 39, 189)** seed 777 → `/tp 258 39 189`.
- Firehose: `SULFUR_ULTRA_DEBUG=1` (cats: packet/move/water/tick/fluid/edit/combat/bandwidth).
- Bot: `./testbot.exe -name X -mode goto|hold|wander -x -y -z -ticks N -probe "x z"` (ACKs ChunkBatchReceived).
- Regen block data after a jar bump: `cd tools && go run . ../temp/jsons/26.2` (positional dir skips Docker extract; emits blocks.go incl. DefaultStateID).
- -race (Docker, warm module cache): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./PKG/`.

## OPERATOR VALIDATION NEEDED (next live session)
Confirm in-game with seed 777 + test kit:
1. Place a chest (slot 8), right-click → opens. Hold a non-chest, right-click the chest → still opens (does NOT replace).
2. Plant cane (slot 9) 3 tall on sand+water, break the bottom → the column above cascades.
3. Visit a ravine/cave with generated water → it flows to fill, no manual update.
4. Skin overlay (hat) shows on self + other players immediately on join.
5. `SULFUR_PERSIST_CHUNKS=1`: edit a chunk heavily (place many distinct blocks), relog/respawn → NO client crash, edits persist.

## v3.1 STATUS
5 subsystems DONE + merged (ATTRIB, FACESTURDY, ITEMNBT, PERSIST, BLOCKTICK). Chunk-streaming fix landed (prior session). **All 5 live bugs now FIXED + locked with regression tests.** v3 milestone close waits on the operator visual sign-off above.

## JAR SPECS (verified bytecode, in .planning/research/)
- `chest-open-jar-spec.md`, `fluid-blockupdate-jar-spec.md`, `SUB-ATTRIB/FACESTURDY/ITEMNBT-jar-spec.md`.

## RESUME HERE
All 5 reported bugs are fixed, tested, -race clean, committed. Next:
1. Get operator live sign-off on the 5 validation items above (especially persist, BUG-5).
2. Push to `development` when the operator confirms.
3. Then close v3 milestone (`/gsd-complete-milestone`) — or proceed to v4 (plugin system) planning already drafted.
4. Carryover unwired (low priority): chest `getStateForPlacement` facing-toward-player; saved block-tick reload (`loadChunkBlockTicks` caller + `level.Chunk.block_ticks` field).
5. STILL PENDING (operator action): rotate the prod root password exposed in an earlier session.
