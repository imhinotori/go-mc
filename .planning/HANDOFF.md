# Sulfur — Session Handoff (2026-06-26, Phase 17 deep gap-closure)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. **Branch `ender-776`.** Push target: **`development`** (= GHCR `:latest`, auto-deploys to prod), NOT `main`. `git push origin ender-776:development`. Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE (read first, governs everything)

**ALL GAMEPLAY LOGIC IS A LITERAL 1:1 PORT OF THE VANILLA JAVA JAR.** Now in `CLAUDE.md` (commit `c8c32f26`). Every piece of game logic (damage, fall, combat, fluids, mob AI, physics, drops, placement, growth) MUST be a method-for-method copy of `temp/cache/26.2-inner.jar` (read via `javap -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`). Mirror the vanilla call chain + numeric ops EXACTLY — float casts (`float64(float32(x))`), epsilons (`1e-6`), attribute multipliers, RNG draw order, immune/guard checks. **NO paraphrase, NO simplify, NO "improve", NO change-behavior.** The ONLY permitted deviation is OPTIMIZATION that provably preserves identical observable gameplay. **Verify against the jar bytecode BEFORE writing — never from intuition** (the user caught two "como me tinque" mistakes this session: fall-damage paraphrase + the "client simulates its own air locally" false assumption). Re-express idiomatically in Go (no GPL paste), CITE the class/method. When a faithful port needs a missing subsystem (attributes, effects), stub it behind a cited constant = the vanilla default, structured to become a real read later — never bake the value away.

## WHERE WE ARE

- **v2.0 SHIPPED + TAGGED.** v3 milestone STARTED (PROJECT.md `## Current Milestone: v3`). Phases 17 (gameplay-completion, FIRST) → 18 (online-mode) → 19 (TUI) → 20 (structure-polish). REGION-01 Folia → v4.
- **Phase 17 is the active phase.** Original plan: 5 plans (17-01..05) wiring the six unwired GAMEPLAY seams. The real-client visual gate (GAMEPLAY-07) then exposed **12 more 1:1 deviations**, each fixed as a gap-closure plan (17-06..18). All committed on `ender-776`.
- **GAMEPLAY-07 visual gate is STILL PENDING the user's final clean real-client pass.** The server runs locally (see below). Each pass has found bugs; all fixed 1:1; next clean pass closes Phase 17.

## WHAT LANDED THIS SESSION (all 1:1 from the jar, committed)

| Plan | Commit | Fix (jar method cited) |
|------|--------|------------------------|
| 17-01..04 | (earlier) | GAMEPLAY-01..06 wired: player-in-store+tab-list broadcast, pos-load, inv join-sync, attack dispatch, fluid spread, item drops |
| 17-06 | 4bdc1f7f | **Spawn** — `PlayerSpawnFinder.getLevelRespawnPos`/`getSpawnPosInChunk`: spawn on decorated solid ground (was spawning inside blocks/water). Also fixed `performRespawn` (same bug). |
| 17-07 | 58e49fb5 | **Foliage upside-down** — `FoliagePlacer.placeLeavesRow` uses `setWithOffset(pos.Y + localY)`; Sulfur used `below(i)` (negated). Flipped 6 sites to `above(i)` + DarkOak fix + loop-bound `i-2`. |
| 17-08 | 8e156a74 | **Fall damage water guard** — `Entity.checkFallDamage` `!isInWater()` guard. |
| 17-09 | 5b7a6186 | **Connect/disconnect logging** (stderr) — foundation of TUI-02. reason=quit/timeout. |
| 17-10 | 1be1f959 | **Fall damage literal 1:1 re-port** — separated `checkFallDamage`/`causeFallDamage`/`calculateFallDamage`/`calculateFallPower` with the `(float)` cast + attribute multipliers + FALL_DAMAGE_IMMUNE guard (the earlier version paraphrased). |
| 17-11 | e8373fd5+3 | **Combat full 1:1** — `Player.attack`: ATTACK_DAMAGE attr, attack-strength ramp `0.2+s²·0.8`, crit ×1.5, knockback, sweep, exhaustion; `LivingEntity.hurtServer` i-frames (invulnerableTime/lastHurt — anti spam-click), `actuallyHurt` armor curve. New `server/attributes.go` (vanilla attribute bases). |
| 17-12 | 8fbbb7e9 | **Trees stacking** — `TreeFeature.doPlace` aborts unless `freeHeight >= treeHeight`; Sulfur used `minFree=2`. + `getMaxFreeTreeHeight` returns `i-2` + isVine guard. |
| 17-13 | 9cf9226a | **Fluid physics wire + breath/drowning** — wired `applyFluidPhysics`; `LivingEntity.baseTick` air (300/-20/2.0 drown). ⚠️ later partly corrected (see 17-15, 17-18). |
| 17-14 | e64fa40e | **Drops + pickup vanilla-parity** — `Block.popResource` jitter ±0.25 + ItemEntity velocity ±0.1 + pickup delay 10 + despawn 6000 + gravity 0.04 + `ItemEntity.playerTouch` pickup + gamemode gate. |
| 17-15 | 2635a8ad,2b74b801,55527bfa | **Water-disconnect** (`handleMovePlayer`: player movement is CLIENT-authoritative — server must NOT rewrite the submitted position; removed the server-side water re-apply that caused the client to drop). **Pickup-slot** (`Inventory.add`/`getFreeSlot`: pickups → main/hotbar storage 0-35 only, never crafting/armor). **SetSlot sync** (`broadcastChanges` → `ContainerSetSlot` after pickup). |
| 17-16 | 414a72f4 | **Grass-on-water + flower-stacking** — `VegetationBlock.canSurvive`/`mayPlaceOn`: `#supports_vegetation` tag (water/plants excluded). |
| 17-17 | 20e3795a | **Empty-hand places stone** — `ServerPlayerGameMode.useItemOn`→`BlockItem.place`: place the block FROM the held item; empty hand = nothing; survival shrinks stack 1; new `server/block_place.go` item→block map. |
| 17-18 | 5b9c43ec | **Oxygen bar never shows** — `DATA_AIR_SUPPLY_ID` (index 1, INT serializer) synched via `ClientboundSetEntityData` (self + observers); corrects 17-13's wrong "client simulates air locally" assumption. |

## RESUME HERE (after compact — continue autonomously)

1. **The user said: define-GSD → prepare-compact → compact → continue autonomous.** Post-compact, CONTINUE AUTONOMOUSLY on Phase 17.
2. **The local server is rebuilt + running for the GAMEPLAY-07 gate** (seed 777, `localhost:25565`). The user is doing real-client passes; when they report new bugs, fix each 1:1 from the jar (the established loop: javap → diagnose → executor with bytecode → rebuild → restart server). When the user reports a CLEAN pass, **close Phase 17** (`/gsd-complete` flow → audit → mark GAMEPLAY-07 done → advance to Phase 18).
3. **If the user is away / no new bug:** the autonomous next step is to proactively AUDIT the remaining Phase-17 gameplay surfaces against the jar for 1:1 deviations (the gate keeps finding them — get ahead of it). Likely-unaudited surfaces: hunger/food (saturation/exhaustion tick), the actual inventory CLICK handlers (ContainerClick vs vanilla `AbstractContainerMenu.clicked` — quickmove/swap/drop), block-break TIME/progress (`ServerPlayerGameMode.destroyBlock` tick), entity tracking deltas, the carried-item/creative-set paths. Spawn Explore agents to diff each vs the jar, then fix 1:1.
4. **Then Phase 18** (online-mode: Yggdrasil `hasJoined` auth + EncryptionRequest/Response RSA + AES-128/CFB8 — hand-rolled CFB8 over stdlib AES, no new dep) once Phase 17 closes.

## KNOWN 1:1 DEBT (deferred-items.md — owned by the parallel MOB-AI track)

The user is running a SEPARATE agent in another chat porting the full mob set (passive + hostile) in an isolated worktree. Do NOT touch mob AI files (`server/ai_*.go`, `server/spawner*.go`) — they'll merge later. Two known 1:1 violations belong to that track:
- **Mobs walk ON water** — `WalkNodeEvaluator.getPathType` (water nodes) + `Mob.travel`→`travelInFluid` server-side not ported. (Player fluid physics is client-authoritative and correct; MOB fluid physics IS server-side and missing.)
- **`TestTickAIDrivesMobs` flaky** — mob nav RNG non-deterministic; needs a seeded source per the 1:1 mandate.

## STANDING CONSTRAINTS (unchanged)

1. **1:1 mandate** (above — the #1 rule now).
2. **CGO_ENABLED=0 clean** (pure-Go static binary). New deps OK where justified (v3 TUI = bubbletea; encryption = stdlib AES + hand-rolled CFB8).
3. **Push target `development`** (CI → :latest → prod via Watchtower on demo.trysulfur.net:25565). `main`→stable. (Note: nothing pushed yet this session — all commits are local on `ender-776`. Push when the user asks or at a milestone.)
4. **-race needs Docker** (host CGO=0): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./server/`. The executors can't run -race (no gcc); the orchestrator runs it in Docker. `./world/...` is slow (~200-370s) — use `-timeout 600s`.
5. **STALE gopls/LSP** floods FALSE diagnostics (undefined X, redeclared, `tools/ undefined`, packetid.ServerboundAttack) after EVERY executor. ALL FALSE — `tools/` is a SEPARATE module. Trust `go build ./...` (exit 0) + `go vet` + `go test`. NEVER revert on gopls.
6. **NO Co-Authored-By / no Claude attribution** in commits/PRs — user is sole author, absolute.
7. **Determinism gates** (worldgen): `TestDecorationReorderIdentical` + `TestEmitOnce` + `TestEmitOnceUnderHold` stay green. A worldgen fix that changes output regenerates the affected goldens (the gates assert reproducibility, not specific positions).
8. **Pre-existing flake**: `TestTickAIDrivesMobs` fails ~1/3 full-suite runs (mob AI async-pool/RNG, NOT a regression — proven by stash-testing). Run the rest of `./server/` to confirm green; this one is the AI track's.

## RUNNING THE GATE SERVER (the established loop)

```bash
# kill old, rebuild, restart (seed 777 has a clean dry spawn at 0.5,71,0.5):
powershell -Command "Get-NetTCPConnection -LocalPort 25565 -ErrorAction SilentlyContinue | %% { Stop-Process -Id \$_.OwningProcess -Force }"
CGO_ENABLED=0 go build -o sulfur.exe ./cmd/sulfur
./sulfur.exe -seed 777 2>&1 | tee /tmp/sulfur-gate.log &   # run_in_background
```
The server logs connect/disconnect to stderr (`/tmp/sulfur-gate.log`) — grep it for `player joined`/`player left reason=...` when diagnosing a disconnect (reason=quit = client closed on a bad packet; reason=timeout = keep-alive). NOTE: `SULFUR_SEED` env var does NOT work — use the `-seed` flag.

## INFRA (live, unchanged)

- GHCR `ghcr.io/imhinotori/sulfur` (public): development→`:latest`, main→`:stable`. Watchtower auto-deploys `:latest` to `demo.trysulfur.net:25565` (Debian 13). `Dockerfile` CGO=0 distroless.
- ⚠️ **Prod root password was exposed in chat earlier — the USER must rotate it (still pending).**

## KEY PATHS (Phase 17 gameplay)

- Combat: `server/combat.go` (hurtServer/i-frames/armor), `server/attack_dispatch.go` (Player.attack), `server/attributes.go` (NEW — attribute bases).
- Fluid: `server/fluid.go` (spread — 1:1, don't touch), `server/fluid_physics.go` (player water phys — wired for player; reserved for mobs), `server/breath.go` (air/drowning), `server/fluid_schedule.go`.
- Damage env: `server/fall_damage.go` (1:1 checkFallDamage chain).
- Inventory/items: `server/inventory.go` (slots, click handlers — NOT YET AUDITED vs jar), `server/item_entity.go` (ItemEntity tick + pickup), `server/block_drop.go` (popResource), `server/block_place.go` (NEW — useItemOn/BlockItem.place), `server/block_interact.go` (break + place dispatch).
- Player entity/sync: `server/player_visibility.go` (player Entity in store, tab-list), `server/entity_encode.go` (SetEntityData, air entry, item entry), `server/tracker.go`.
- Worldgen: `world/levelgen/feature/tree.go` + `tree_placers.go` (foliage/trunk — 1:1 fixed), `world/feature_patch.go` (canSurvive — 1:1 fixed), `world/spawn.go` (PlayerSpawnFinder).
- Planning: `.planning/STATE.md` (current — updated), `.planning/PROJECT.md` (v3 milestone), `.planning/ROADMAP.md` (Phases 17-20), `.planning/phases/17-gameplay-completion/` (17-01..18 SUMMARYs + deferred-items.md).
