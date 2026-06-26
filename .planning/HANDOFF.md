# Sulfur — Session Handoff (2026-06-26, Phase 17 real-client gameplay polish)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. **Branch `ender-776`.** Push target: **`development`** (= GHCR `:latest`, watchtower auto-deploys to prod demo.trysulfur.net:25565), NOT `main`. `git push origin ender-776:development`. Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE (read first, governs everything)

**ALL GAMEPLAY LOGIC IS A LITERAL 1:1 PORT OF THE VANILLA JAVA JAR.** In `CLAUDE.md`. Every piece of game logic MUST be a method-for-method copy of `temp/cache/26.2-inner.jar` (read via `javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`). Mirror the vanilla call chain + numeric ops EXACTLY. **NO paraphrase, NO simplify, NO "improve". Verify against the jar bytecode BEFORE writing — never from intuition.** The user caught multiple "como me tinque" mistakes this session — when stuck, decompile the jar.

## WHERE WE ARE

- **Phase 17 (gameplay-completion) is the active phase.** GAMEPLAY-01..06 all **Complete**. Only **GAMEPLAY-07 (real-client visual gate) is Pending** — but the user confirmed water-float, eating, hunger, combat, drops, block-break all WORK this session. The gate is essentially passed.
- The user chose **"audit more before closing Phase 17"** — three 1:1 audits ran (below), all found MISSING surfaces. Those are the remaining 1:1 work before formally closing GAMEPLAY-07.
- **65 commits ahead of `origin/development`, NONE pushed.** This is why prod (watchtower) is stale — the `:latest` image was never rebuilt. The user authorized the push, BUT it is **BLOCKED by an environment pre-push hook** (a gate sentinel for an UNRELATED repo `D:/tori-labs/attendly/attendly` — `gate-ok` stale). Not a Sulfur problem; the user must refresh attendly's gate or push from a terminal without that hook.

## WHAT LANDED THIS SESSION (committed on `ender-776`)

| Commit | Fix |
|--------|-----|
| 414a72f4..5b9c43ec | (prior session) 17-16..18 |
| ab4b4490 / eb90b554 | **17-19 hunger 1:1** — FoodData.tick (exhaustion 4.0F drain, regen 10/80-tick, starvation, addExhaustion 40cap, eat/add, movement+attack+damage exhaustion call-sites). |
| ed28b229 | **17-20 inventory click 1:1** — AbstractContainerMenu.clicked: PICKUP/QUICK_MOVE/SWAP/THROW/PICKUP_ALL/QUICK_CRAFT/CLONE + moveItemStackTo + quickMoveStack + Slot primitives. Was a no-op stub. |
| 255e6aff / dfe3870f | **17-21 block-break dig-time 1:1** — ServerPlayerGameMode dig-time + per-hardness crack overlay + **jar-extracted hardness for all 1196 blocks** (new GenBlockHardness.java). Was instant-break. |
| eb90b554 / 00bf1d03 | **17-22 eating 1:1** — startUsingItem→completeUsingItem→FoodData.eat + **jar-extracted per-item food/consumable** (GenItemFood.java). Closes the hunger loop. + GATE-ONLY `SULFUR_TEST_KIT=1` starter kit (food+blocks). |
| 0edcae1b | **water is NOT a collision wall** — blockSolidAt excluded fluids (LiquidBlock.getCollisionShape==empty). You sink through water now. |
| bf4277c9 | **flow fluids into freshly-edited cells** — reconcileEdit schedules neighbor fluids (Level.updateNeighborsAt→LiquidBlock.neighborChanged). Water flows into a broken-block gap. |
| 82797bd9 | **SULFUR_ULTRA_DEBUG firehose** — opt-in full per-tick gameplay trace (packet/move/water/tick/fluid/edit/combat/eat/collide categories). THE diagnostic tool. `grep 'ULTRA\[water\]'`. |
| fbc6b3d1 | **broadcast fluid block changes to clients** — setFluidBlock = SetBlock + broadcastBlockUpdate (Level.setBlock UPDATE_CLIENTS). |
| **f3ce7b3e** | **⭐ THE water-float root cause** — section `nonEmptyFluidCount` (chunk packet 2nd short) was hardcoded 0, so the client treated generated water as inert (no float until a block update woke it). Now `level.CountFluidBlocks` recounts at the END of Generate (aquifer writes water AFTER FillChunk). **User confirmed: floating works.** New `block.IsFluid`. |
| 994e2f8e | **deflake TestTickAIDrivesMobs** (user OK'd touching AI) — loop-until-advance + drain asyncIn2. Was ~1/3 flake that could fail CI -race + block the image publish. 15/15 + 4/4 suite green now. |
| d9fccc0f / 5f798f8c / f88e4338 / eb197e0a | **cmd/testbot** — headless scripted client (login→config→play, offline, compression 256, teleport-confirm). `-cmd "tp x y z"`, `-probe "x z"` (decodes received chunks to verify client-side water), `-mode wander` (NPC random-walk + swing/select/use). THE tool to reproduce gameplay without the real client. |
| 5f798f8c | **/tp command** — dev/gate teleport (reuses respawn re-teleport contract). New cmd-executor ctx key. |

## ⚠️ TWO REVERTS — the fluid-cascade trap (DO NOT re-introduce naively)

Twice this session a "flow generated cave/aquifer water on chunk load" fix (`7d5a0b90` reverted by `fb8a43aa`; `468f59a7` reverted by `761872cf`) caused a **runaway fluid cascade** (11386 then 25674 spreadTo events) that saturated the tick and disconnected every client (the bot AND a real client). Both reverted. The current tree has NO chunk-fluid scan.

**Symptom that REMAINS (the open bug):** generated cave/aquifer water bordering an air gap stays frozen — a player in an air pocket surrounded by water sees the gap stay dry. (You float fine now; this is the spread-into-gap bug only.)

**THE CORRECT FIX (jar-verified at end of session — implement this, NOT a scan-and-schedule):**
- Vanilla `NoiseBasedChunkGenerator.fillFromNoise`: per fluid cell placed, `if (aquifer.shouldScheduleFluidUpdate() && !state.getFluidState().isEmpty()) chunk.markPosForPostProcessing(pos)`. It does NOT schedule a simulation tick — it MARKS the position.
- Vanilla `LevelChunk.postProcessGeneration` (on chunk promote): for each marked pos, call `FluidState.tick(level, pos, state)` **ONCE** (+ `BlockState.tick` if LiquidBlock). **One tick per marked cell, not a recurring sim.**
- Why no cascade: the aquifer only marks the UNSTABLE BORDER cells (`shouldScheduleFluidUpdate`), not all cave water, AND the post-process is one-shot.
- **Sulfur's mistake (both reverts):** put cells into the continuous `scheduleFluidTick`→`tickFluids` (every 5 ticks → spread → more cells → cascade). The fix is: (a) in the generator, track which cells the aquifer flagged + are fluid (mark them), (b) a one-shot `postProcessGeneration` that runs `fluidTick` on each marked cell EXACTLY ONCE when the chunk goes live — NOT re-scheduled.
- Sulfur's aquifer is in `world/levelgen/noisechunk/` (Aquifer / FillChunk / finishChunk). The `shouldScheduleFluidUpdate` equivalent + the marked-pos list need porting, then a one-time drain on chunk Ready (the `world.ChunkManager.Insert` / `tickChunks` Ready seam).

## THE THREE 1:1 AUDITS (run this session — all MISSING, these close GAMEPLAY-07)

1. **Swing + held-item visibility (MISSING).** `ServerboundSwing` falls to the applyInput `default:` no-op — other players never see arm swings (`ClientboundAnimate`, wire: VarInt entityId + UByte action; mainhand=0 offhand=3). `handleSetCarriedItem` updates the slot locally only — no `ClientboundSetEquipment` + no per-tick `detectEquipmentUpdates` (LivingEntity), so others never see held-item/armor changes. Encoders missing in `entity_encode.go`; tracker (`tracker.go`) emits no Animate/SetEquipment.
2. **Player movement tracking (DEVIATION).** Tracker sends `TeleportEntity` (absolute, 32 bytes) EVERY tick instead of delta `MoveEntityPos/PosRot/Rot` (~6 bytes, 1/4096 fixed-point). The delta encoders EXIST in `entity_encode.go:352-414` but are NEVER called. Missing: ServerEntity.sendChanges (4096 delta scale, the |delta|<8-block→delta-else-teleport decision, the 400-tick forced teleport, the 1-byte-angle rotation threshold). RotateHead sent unconditionally every tick. Observable: other players STUTTER/warp instead of smooth-walk; 5-10× bandwidth.
3. **Eat/pose animation 3rd-person (MISSING).** Eating logic ported (17-22) but `DATA_LIVING_ENTITY_FLAGS` (index 8, BYTE serializer id 0, bit 0 = IS_USING) NOT synced on startUsingItem/stopUsingItem → others don't see the eating pose. Also `DATA_SHARED_FLAGS_ID` (index 0, BYTE — sprint/sneak/swim pose) never synced. Proof-of-pattern: Sulfur already syncs `DATA_AIR_SUPPLY_ID` (index 1, INT id 1) in `entity_encode.go` (17-18) — extend that exact pattern.

## RESUME HERE (after compact)

1. **The fluid-cave-gap fix** — implement the vanilla `markPosForPostProcessing` + one-shot `postProcessGeneration` (above). This is the safe replacement for the cascading scan. Decompile-verified; just needs porting. Test the cascade is GONE (the bot `-mode wander` near spawn, or the ULTRA_DEBUG `grep -c 'ULTRA\[fluid\]'` should stay LOW, not 25k).
2. **The three audit fixes** (swing/equipment broadcast, delta-move tracking, eat/pose metadata) — these are the remaining 1:1 work that closes GAMEPLAY-07. The `-mode wander` NPC makes them VISIBLE to gate. Each is a clean port (cited above).
3. **Then close GAMEPLAY-07** → audit → mark done → Phase 18 (online-mode: Yggdrasil auth + AES/CFB8).
4. **The push to `development`** (deploys to prod, fixes watchtower) — authorized by user, BLOCKED by the attendly gate hook. Retry once that gate clears, or the user pushes manually.

## TOOLS (built this session — use them)

- **`./testbot.exe`** (build `go build -o testbot.exe ./cmd/testbot`): headless client. Flags: `-name`, `-mode hold|walk|swim|dive|goto|wander`, `-cmd "tp x y z"`, `-probe "x z"`, `-x -y -z`, `-ticks`, `-override-spawn`. The `-mode wander` is the roaming NPC (give it the kit via the server's `SULFUR_TEST_KIT=1`). NOTE: `-cmd tp` re-teleport sometimes drops the bot before the new chunk streams (pre-existing /tp re-teleport teardown — a known rough edge).
- **`SULFUR_ULTRA_DEBUG=1`**: the firehose. `grep 'ULTRA\[<cat>\]' <log>`. Categories: packet/move/water/tick/fluid/edit/combat/eat/collide. `tick` shows vY + flags `[water,eyeWater,ground,digging,using]`.
- **`SULFUR_TEST_KIT=1`**: gate-only starter kit (food + cobble/planks/torch/dirt) seeded at join. Default prod join = vanilla empty inventory (untouched).
- **`/tp <x> <y> <z>`**: in-game dev teleport (breaks the underwater-.dat disconnect loop too).

## RUNNING THE GATE SERVER

```bash
powershell -Command "Get-NetTCPConnection -LocalPort 25565 -ErrorAction SilentlyContinue | %% { Stop-Process -Id \$_.OwningProcess -Force }"
CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur
SULFUR_ULTRA_DEBUG=1 SULFUR_TEST_KIT=1 ./sulfur.exe -seed 777   # run_in_background
```
Seed 777 spawn = (0.5, 71, 0.5) dry. Player .dat files were DELETED this session (all respawn at spawn; backup in /tmp/sulfur-dat-backup/) — `world/playerdata/` is empty.

## STANDING CONSTRAINTS

1. **1:1 mandate** (above — #1 rule).
2. **CGO_ENABLED=0 clean** (pure-Go static binary).
3. **Push target `development`** (CI→:latest→prod). 65 commits unpushed; push blocked by the attendly gate hook (not Sulfur's).
4. **-race needs Docker** (host CGO=0): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./server/`.
5. **STALE gopls** floods FALSE diagnostics (`packetid.ServerboundAttack`, `entity.SulfurCube`, `tools undefined`, `go-mc/nbt`) after EVERY edit. ALL FALSE — trust `go build ./...` (exit 0) + `go vet` + `go test`. `tools/` is a SEPARATE module.
6. **NO Co-Authored-By / no Claude attribution** in commits — user is sole author, absolute.
7. **DON'T re-introduce a scan-and-schedule for generated fluids** — it cascades (proven twice). Use the one-shot postProcessGeneration approach.

## KEY PATHS

- Fluid: `server/fluid.go` (sim — DON'T add a chunk scan), `server/fluid_physics.go`, `server/breath.go`. Generator fluid: `world/levelgen/noisechunk/` (FillChunk/finishChunk/Aquifer — where the markPosForPostProcessing port goes). FluidCount fix: `world/noisegen.go` (recount at end of Generate), `level/chunk.go` (CountFluidBlocks), `level/block/utilfuncs.go` (IsFluid).
- Visibility (the 3 audits): `server/tracker.go` (delta-move + Animate + SetEquipment broadcast), `server/entity_encode.go` (SetEntityData/equipment/animate encoders + the metadata pattern — air at index 1 is the template), `server/subtick.go` (ServerboundSwing handler — currently default no-op), `server/inventory.go` (handleSetCarriedItem — needs equipment broadcast), `server/item_use.go` (startUsingItem/stopUsingItem — need DATA_LIVING_ENTITY_FLAGS sync).
- Tools: `cmd/testbot/main.go` (the headless NPC client), `server/ultradebug.go` (firehose), `server/test_kit.go` (gate kit), `server/commands.go` (/tp).
- Planning: `.planning/STATE.md`, `.planning/PROJECT.md`, `.planning/ROADMAP.md`, `.planning/phases/17-gameplay-completion/`.

## INFRA

- GHCR `ghcr.io/imhinotori/sulfur`: development→`:latest` (watchtower auto-deploys to demo.trysulfur.net:25565), main→`:stable`. CI `.github/workflows/docker-publish.yml` (push to development/main) + `go.yml` (build + `go test -race ./...`).
- ⚠️ Prod root password exposed in chat earlier — user must rotate (still pending).
