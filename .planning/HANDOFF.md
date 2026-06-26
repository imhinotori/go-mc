# Sulfur — Session Handoff (2026-06-26, Phase 17 CLOSED → Phase 18 starting)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. **Branch `ender-776`.** Push target: **`development`** (= GHCR `:latest`, watchtower auto-deploys to prod demo.trysulfur.net:25565), NOT `main`. `git push origin ender-776:development`. Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE (read first)
**ALL GAMEPLAY LOGIC IS A LITERAL 1:1 PORT OF THE VANILLA JAVA JAR.** In `CLAUDE.md`. Every piece of game logic MUST be a method-for-method copy of `temp/cache/26.2-inner.jar` (read via `javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`). Mirror the vanilla call chain + numeric ops EXACTLY. **Verify against the jar bytecode BEFORE writing — never from intuition.** Re-express in idiomatic Go (no GPL paste), CITE the class/method. Only OPTIMIZATION (provably identical observable behavior) is permitted.

## WHERE WE ARE
- **Phase 17 (Gameplay Completion) is DONE.** GAMEPLAY-01..06 complete; **GAMEPLAY-07 visual gate PASSED** — the user connected a real client this session and confirmed "Está dando vueltas y funciona". 1:1 work complete + tested + Docker -race green. `17-05-SUMMARY.md` + `17-VERIFICATION.md` written. Verdict: PASS.
- **NEXT: Phase 18 — Online-mode (auth + protocol encryption).** Just invoked `/gsd-plan-phase 18`. The phase dir `.planning/phases/18-online-mode-auth-protocol-encryption/` is created; research+plan not yet run. **RESUME by continuing the plan-phase 18 workflow** (research the 776 encryption/Yggdrasil specifics → plan → verify).
- **v4 (Plugin/Scripting System) is PLANNED but NOT started** — full plan in `.planning/v4-PLAN.md` (Phases 21–28). Do NOT execute v4; v3 (18–20) finishes first. STATE.md stays on v3.

## PHASE 18 — what to build (ONLINE-01/02, from REQUIREMENTS.md + ROADMAP.md)
- **ONLINE-02 encryption:** EncryptionRequest/EncryptionResponse handshake — RSA-OAEP key exchange of the 16-byte shared secret + verify token, then AES-128/CFB8 stream encryption on ALL subsequent packets. **CFB8 hand-rolled over stdlib `crypto/aes`** (Go stdlib dropped `cipher.CFB`) — NO new crypto dep. Verify the exact 776 EncryptionRequest/Response wire (serverId string, public key DER, verify token, + the 1.20.5+ `shouldAuthenticate` boolean shifts) against the jar.
- **ONLINE-01 auth:** Yggdrasil `hasJoined` — server sends encryption request, client auths against `sessionserver.mojang.com`, server verifies the shared-secret-derived server hash (the Notchian SHA-1 "Minecraft-style" hex digest) → real UUIDs/skins/ownership. Behind an `online-mode` config flag (offline = default for local dev).
- **OUT of scope:** the client's own MSA/launcher login (server only verifies the existing session).
- Jar classes to decompile: `net.minecraft.network.protocol.login.ClientboundHelloPacket` (EncryptionRequest), `ServerboundKeyPacket` (EncryptionResponse), `net.minecraft.server.network.ServerLoginPacketListenerImpl` (handleKey, the hash, the hasJoined call), `net.minecraft.util.Crypt` (the cipher + the SHA-1 server hash).

## WHAT LANDED THIS SESSION (committed on `ender-776`)
| Commit | Fix |
|--------|-----|
| 7831b5e7 | **Cave-water-gap fix** — markPosForPostProcessing + one-shot postProcessGeneration (the SAFE replacement for the scan-and-schedule that cascaded twice). Restores the aquifer `shouldScheduleFluidUpdate` flag. |
| 09c1e54b | **Fluid cost instrumentation** — ULTRA[fluid] cost per gametick (decide async-fluid WITH data). |
| 3fef2212 | **Audit 1 — swing + held-item** — ServerboundSwing→Animate + held→SetEquipment, broadcast to trackers not self. New tickEquipment phase + broadcastToTrackers. |
| b0475202 | **Audit 3 — eat/use pose** — DATA_LIVING_ENTITY_FLAGS IS_USING synced (LivingEntity.setLivingEntityFlag). |
| 64f778ce | **Audit 2 — delta-move** — ServerEntity.sendChanges 1:1 (delta MoveEntity* / EntityPositionSync replacing per-tick TeleportEntity). New tickEntityMovement phase; packDegrees (floor). |
| 4f4041cc | **deflake TestTickAIDrivesMobs** — timed receive (was non-blocking default). |
| 471b232b | **Join-disconnect fix** — outbound queue overflow at join; RotateHead now 1:1 (head-yaw threshold, not every move); outboundCap 256→4096. testbot got strict SetEquipment/SetEntityData validators. |
| a807ff9f | **Worldgen race crash** — `concurrent map read and map write` in SurfaceSystem (noiseCache + randomFactoryCache shared across parallel chunk-gen). Added cacheMu. Docker -race ./world/ green. |
| 74f1c9e1 | **Block-place entity-collision** — BlockItem.canPlace→Level.isUnobstructed: a placement overlapping a blocksBuilding entity (player/mob) is rejected; dropped items don't block. |
| 4c6cea9f | **19 worldgen biome tags** from the 26.2 extractor re-run. |
| eb197e0a..9f23003f | **testbot break→pickup→place cycle** (-mode wander) + strict entity-packet validators. |
| a1f4c7b8 | **Phase 17 close-out docs** — 17-05-SUMMARY + 17-VERIFICATION. |
| d74cf305 | **Deferred: block-survival** (gate finding — see below). |
| 5567dbb4 / ebe78f9c | **v4 plan** (ROADMAP entry + v4-PLAN.md). |

## 1:1 AUDITS RUN THIS SESSION (all jar-verified)
- **Inventory doClick** (7 ClickTypes + moveItemStackTo + quickMoveStack + Slot primitives): FAITHFUL. Only gap = `updateTutorialInventoryAction` (no gameplay effect).
- **Block place**: found + FIXED entity-collision (isUnobstructed). Else faithful.
- **Combat / Player.attack / hurtServer / actuallyHurt / knockback**: FAITHFUL. (The auditor's "exhaustion deviation" was a FALSE POSITIVE — Player OVERRIDES actuallyHurt and DOES call causeFoodExhaustion on the victim; Sulfur ports it right. Both sites verified.)
- **Fall damage** + **Breath/drowning**: FAITHFUL 1:1, all constants match.

## OPEN GATE FINDING (deferred, logged in deferred-items.md)
- **Block survival** — breaking a block that supports a flower/plant leaves the unsupported block floating (vanilla destroys+drops it). The full 1:1 chain is documented in `deferred-items.md` (Level.updateNeighborsAt → BlockState.updateShape → VegetationBlock.canSurvive [SUPPORTS_VEGETATION tag] → Block.updateOrDestroy → destroyBlock+drop). It is a net-new SUBSYSTEM (runtime block-tag lookup + a block→survival-class extraction + updateOrDestroy port) — user chose to DEFER it and proceed to Phase 18. The drop path already exists (GAMEPLAY-06).

## RUNNING (right now)
- **Server** in bg (run_in_background task), `localhost:25565`, seed 777, `SULFUR_ULTRA_DEBUG=1 SULFUR_TEST_KIT=1`. Stable since the race fix (0 crashes).
- **Bot Wanderer** in bg (`./testbot.exe -name Wanderer -mode wander -ticks 360000`) — walks, swings, eats, breaks→picks-up→places blocks. 0 disconnects.
- Rebuild: `CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur` + `go build -o testbot.exe ./cmd/testbot`. Kill stale: `powershell -Command "Get-Process sulfur,testbot -EA SilentlyContinue | Stop-Process -Force"`.

## TOOLS
- **`./testbot.exe`** — headless client. `-mode hold|walk|swim|dive|goto|wander`, `-cmd "tp x y z"`, `-probe "x z"`, `-x -y -z`, `-ticks`. `-mode wander` = the roaming NPC (swing/select/eat/break/place). Has strict validateSetEquipment/validateSetEntityData decoders now.
- **`SULFUR_ULTRA_DEBUG=1`** — firehose. `grep 'ULTRA\[<cat>\]'`. Cats: packet/move/water/tick/fluid/edit/combat/eat/collide.
- **`SULFUR_TEST_KIT=1`** — gate-only kit (food in hotbar 0-2, cobble/planks/torch/dirt in 3-6).
- **`/tp <x> <y> <z>`** — in-game dev teleport.

## STANDING CONSTRAINTS
1. **1:1 mandate** (above).
2. **CGO_ENABLED=0 clean** (pure-Go static). NOTE for Phase 18: encryption uses stdlib `crypto/aes` + `crypto/rsa` + `crypto/sha1` — all pure-Go, CGO stays 0. CFB8 hand-rolled (stdlib dropped cipher.CFB).
3. **Push target `development`** (CI→:latest→prod). ~80 commits unpushed; push was blocked by an unrelated attendly env gate hook (NOT Sulfur's) — that's why prod/watchtower is stale.
4. **-race needs Docker** (host CGO=0): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./server/`.
5. **STALE gopls** floods FALSE diagnostics (`packetid.ServerboundAttack`, `entity.SulfurCube`, `block.Hardness`, `item.ItemFood`, `tools undefined`). ALL FALSE — trust `go build ./...` (exit 0) + vet + test.
6. **NO Co-Authored-By / no Claude attribution** in commits.
7. **DON'T re-introduce a scan-and-schedule for generated fluids** — cascades (proven twice). Use the one-shot postProcessGeneration (now landed).

## KEY PATHS
- Phase 18 work goes in: `server/login.go` / `server/handshake.go` (login state machine), `net/` (the Conn — where the AES stream wraps), `server/auth/auth.go` (exists — Yggdrasil hasJoined goes here), `cmd/sulfur/main.go` (the online-mode flag). Decompile the login packets + Crypt from the jar first.
- Phase 17 (reference): `server/entity_events.go` (the new visibility broadcasts), `server/entity_encode.go` (encoders), `server/block_interact.go` (place + entity-collision), `server/fluid.go` (sim + postProcessChunkFluids), `world/levelgen/surface/system.go` (the race fix).
- Planning: `.planning/STATE.md`, `.planning/ROADMAP.md`, `.planning/REQUIREMENTS.md`, `.planning/v4-PLAN.md`, `.planning/phases/17-gameplay-completion/` (deferred-items.md, 17-VERIFICATION.md), `.planning/phases/18-online-mode-auth-protocol-encryption/` (empty, just created).

## INFRA
- GHCR `ghcr.io/imhinotori/sulfur`: development→`:latest` (watchtower→demo.trysulfur.net:25565), main→`:stable`. CI: docker-publish.yml + go.yml (build + `go test -race ./...`).
- ⚠️ Prod root password exposed in chat earlier — user must rotate (still pending).

## RESUME HERE (after compact)
1. **Continue `/gsd-plan-phase 18`** — the workflow was mid-flight (dir created, init done). Research the 776 encryption + Yggdrasil specifics (decompile the login packets + Crypt), then plan, then verify. Phase 18 = ONLINE-01 (Yggdrasil hasJoined) + ONLINE-02 (RSA key exchange + AES-128/CFB8 hand-rolled).
2. After Phase 18: Phase 19 (TUI + disconnect logs), Phase 20 (structure polish), then v3 closes → v4 (plugins).
3. The push to `development` is still pending (the attendly gate hook blocked it). Retry when that clears, or push manually.
