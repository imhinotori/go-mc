# Sulfur — Session Handoff (2026-06-25, end of v2 / start of v3)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. **Branch `ender-776`.** Push target: **`development`** (= GHCR `:latest`, auto-deploys to prod), NOT `main`. `git push origin ender-776:development`. Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## WHERE WE ARE

- **v2.0 SHIPPED + TAGGED** (2026-06-25). Worldgen features + structures, all 15/15 reqs, two visual gates approved (the last on seed 25). Archived to `.planning/milestones/v2-*.md`, tag `v2.0` pushed. ROADMAP/PROJECT.md evolved, REQUIREMENTS.md removed (fresh for v3). The 16 old phase dirs were cleared by `phases.clear`.
- **Render-distance fix shipped** (commit `a34db178`): `serverViewDistance` 2 → **10** (vanilla default). `server/world_stream.go`. Two framing tests pinned to `minViewDistance` (the 441-column ring overflows the synchronous test pipe — that's test infra, not prod). CI green → prod on radio 10.
- **v3 milestone STARTED** (commit on `ender-776`): `state.milestone-switch v3`, PROJECT.md has the `## Current Milestone: v3` section. **Research dir has STALE v2 files** (STACK/FEATURES/ARCHITECTURE/PITFALLS/SUMMARY.md are v2's) — the v3 research spawn was NOT run yet (interrupted by the bug reports below).

## ⚠️ THE PIVOT — v3 scope changed mid-flight

The user connected with a real client and found **v1.0's "fully playable" claim is half-done**. Six gameplay bugs reported. I investigated each against the real code (3 Explore agents). **Decision (user-confirmed via AskUserQuestion): insert a "Phase 17 — gameplay completion" FIRST in v3, before online-mode/TUI/structures.** A non-playable server makes online-mode pointless.

**v3 scope (user-chosen):** Phase 17 gameplay-completion (FIRST) → then ONLINE-01/02 (Mojang auth + AES/CFB8 encryption) + TUI (bubbletea+bubbles: command zone + live logs + disconnect-reason logging) + structure polish (loot tables, structure entities villagers/witch/cat/silverfish, afterPlace terrain-beard, structure-start NBT persistence). **REGION-01 Folia is OUT of v3 (→ v4).**

## THE SIX BUGS (investigated, file:line evidence) — Phase 17 work

**Pattern: core logic exists + is unit-tested, but the final SEAM (join-sync / input-dispatch / broadcast / tick-wiring) was never connected.** v1 closed on isolated-function tests, not end-to-end with a real client.

| # | Bug | Class | Root cause + fix location |
|---|-----|-------|---------------------------|
| 1 | **Players invisible to each other** | NEVER IMPL | Players are never added to the entity store, so the tracker (which only handles MOBS) has no player data to broadcast. Fix: `server/tick.go:699-716` `drainRegistrations()` must `t.entities.add(p)` (create an `entity.Player` ID-156 Entity for the joining player) + sync its pos each tick. Tracker at `server/tracker.go:61` then finds them. `entity.Player` exists (`data/entity/entity.go:1424`). |
| 2 | **Position not persisted** | SAVE ok, LOAD deferred | `snapshotPlayer`/`savePlayer` DO write x/y/z (`server/persistence.go:55-129`, round-trip tested). But `server/gameplay_tick.go:242-257` EXPLICITLY skips applying the loaded position ("NOT applied for v1 … would require re-issuing the bootstrap teleport — deferred"). Fix: apply the persisted pos to the spawn teleport in the join bootstrap (`play_join.go:480-502` hardcodes spawn 8.5/surfaceY+2/8.5). |
| 3 | **Inventory doesn't work** | Handlers ok, JOIN-SYNC missing | All handlers exist + tested (`server/inventory.go:98-186`: ContainerClick/SetCreativeModeSlot/SetCarriedItem; `sendContent` pushes ContainerSetContent). BUT the initial `ContainerSetContent` is **never sent on join** (`play_join.go sendPlayBootstrap` / `gameplay_tick.go AcceptPlayer` don't call it) → client sees empty window → moves fail silently. Fix: call `sendContent(p)` on first tick after `AcceptPlayer`. |
| 4 | **Damage doesn't work** | Core ok+tested, DISPATCH missing | `applyDamage`/`die`/`performRespawn`/`setHealth` all exist + tested (`server/combat.go:50-174`, `combat_test.go`). Attack/Interact packets ROUTE to the subtick buffer (`tick.go:771`) BUT `applyInput()` (`server/subtick.go:116-206`) has **no case for ServerboundAttack/Interact** → they hit the `default:` no-op. Only `SULFUR_DEBUG_DAMAGE=1` ever calls applyDamage (`debug.go:227`). Fix: add Attack/Interact cases in `applyInput` that resolve the target + call `t.applyDamage`. Also: NO fall/environmental damage tick exists. |
| 5 | **Water doesn't work** | Render ok, SIM never impl | Water blocks render (chunks send them; worldgen places them at sea level via aquifer). BUT `tickWorld()` (`server/tick_phases.go:113`) is an **empty stub** — zero fluid simulation: no flow propagation, no fluid-level updates, no waterlogged updates, no player swim/buoyancy physics. Fix: implement the fluid tick (port vanilla `LiquidBlock`/`FlowingFluid` spread) + player fluid physics. Largest of the six. |
| 6 | **Blocks drop nothing** | Break ok, ITEM-SPAWN missing | Break works: `server/block_interact.go:81-116` sets air + acks + broadcasts BlockUpdate. BUT after SetBlock it returns — never looks up the block's drop, never spawns an `Item` entity (`entity.go:659` exists, ID 71), never sends AddEntity. Fix: after SetBlock, spawn an Item entity for the drop + track it. Ties to bug #1 (entity broadcast) + the loot-table work (structure polish overlaps — block loot tables). |

**Build order note:** #1 (player entity in tracker + broadcast) is the keystone — #6 (item-drop entities) and any visible-entity work depend on the player/entity broadcast path working. #5 (water sim) is the biggest standalone. #2/#3/#4 are seam-reconnects (small, high-value). The plan-phase researcher/planner should sequence #1 first.

## RESUME HERE

1. **Finish the v3 milestone setup** (we're mid-`/gsd-new-milestone`): the research spawn (4 parallel gsd-project-researchers: Stack/Features/Architecture/Pitfalls) was NOT run. Either run it (note: research should cover the NEW v3 surfaces — Yggdrasil auth flow + AES/CFB8 protocol encryption for 776, bubbletea/bubbles API, loot-table/structure-entity patterns — AND inform the Phase-17 fluid-sim port) OR skip-research and go straight to requirements. The stale v2 research files in `.planning/research/` must be overwritten or ignored.
2. **Define v3 REQUIREMENTS.md**: a GAMEPLAY-xx category (the 6 bugs above, Phase 17) + ONLINE-xx + TUI-xx + STRUCT-POLISH-xx. Continue REQ numbering.
3. **Roadmap**: Phase 17 = gameplay completion (FIRST), then the rest. Phases continue from 16 (so 17, 18, ...).
4. Then `/gsd-plan-phase 17` and execute.

## STANDING CONSTRAINTS (unchanged, non-negotiable)

1. **Port-from-jar mandate**: gameplay/worldgen/structure LOGIC ported DIRECTLY from the decompiled jar (`temp/cache/26.2-inner.jar`, javap -c / CFR). Idiomatic Go, NO GPL paste, cite the class. The plan-checker decompiles the jar to verify.
2. **CGO_ENABLED=0 must stay clean** (pure-Go static binary, the "no JVM" value prop + the Docker image). New deps OK for v3 where justified (bubbletea/bubbles for the TUI; a crypto lib only if stdlib `crypto/aes`+`crypto/cipher` CFB8 isn't enough — but stdlib has AES; CFB8 may need a small hand-rolled mode since Go stdlib dropped CFB). Auth needs HTTP to sessionserver.mojang.com (stdlib net/http).
3. **Push target is `development`** (CI: development→latest→prod via Watchtower on demo.trysulfur.net:25565). `main`→stable.
4. **-race needs Docker** (host CGO=0): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race -timeout 1800s ./...`. The `-timeout 1800s` is required (structures push ./world to ~834-1008s).
5. **STALE gopls/LSP** floods FALSE diagnostics (undefined X, redeclared, syntax errors in tests, `tools/ undefined`, shared-test-helper redeclare). ALL FALSE — `tools/` is a SEPARATE Go module. Trust `go build ./...` (exit 0) + `go vet` + `go test`. NEVER revert on gopls.
6. **NO Co-Authored-By / no Claude attribution** in commits or PRs — user is sole author, absolute.
7. **Determinism contract** (worldgen): `TestDecorationReorderIdentical` (5×5) + `TestEmitOnce` stay byte-identical. Phase-17 gameplay work shouldn't touch worldgen, but if it does, these gates hold.
8. **USER MUST ROTATE the prod root password** (exposed in chat earlier this session). Still pending.

## INFRA (live)

- GHCR `ghcr.io/imhinotori/sulfur` (public): development→`:latest`, main→`:stable`, +`:sha-<12>`, prune keeps 3. `Dockerfile` (CGO=0 distroless), `.github/workflows/docker-publish.yml`.
- Prod: `151.242.242.206` / `demo.trysulfur.net:25565` (Debian 13). Watchtower (`ghcr.io/nicholas-fedor/watchtower` fork — the containrrr one crash-loops on Docker 29) auto-pulls `:latest` every 5min. Volume `sulfur-world:/app`. Deploy script `/root/deploy-sulfur.sh`.
- Local: a radio-10 server is running in bg on `:25565` seed 25 (sulfur.exe, gitignored). Kill it before rebuilding (`Get-NetTCPConnection -LocalPort 25565 | Stop-Process`).

## KEY PATHS

- Gameplay (Phase 17): `server/tick.go` (drainRegistrations, dispatch), `server/tracker.go` (entity tracker — mobs-only), `server/subtick.go` (applyInput — the dispatch with the missing Attack cases), `server/combat.go` (damage, wired but uncalled), `server/inventory.go` (handlers, no join-sync), `server/block_interact.go` (break, no drop), `server/persistence.go` (save ok), `server/gameplay_tick.go` (AcceptPlayer / pos-load skip), `server/tick_phases.go:113` (tickWorld stub = no fluid), `server/entity.go` + `data/entity/entity.go` (NewEntity, entity.Player/Item types).
- Structures: `world/structure/` (v2 — loot/entities/beard/persistence are the polish deferrals).
- Planning: `.planning/PROJECT.md` (v3 current milestone), `.planning/ROADMAP.md` (collapsed to v1/v2 milestones), `.planning/MILESTONES.md`, `.planning/milestones/v2-*.md` (archives). NO REQUIREMENTS.md yet (mid-creation).
