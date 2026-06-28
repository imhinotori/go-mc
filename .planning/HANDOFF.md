# Sulfur — Session Handoff (2026-06-27, v3 closed + live-bug sweep done → next is v4)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD build. **Branch `ender-776`.** Push target **`development`** (NOT main). Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`.

## ⭐ THE ABSOLUTE MANDATE
**ALL GAMEPLAY/PROTOCOL LOGIC IS A LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`.** Verify bytecode BEFORE writing: `JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`. Cite the class/method. Only OPTIMIZATION (provably identical behavior) permitted. CGO_ENABLED=0 stays clean. NO Claude attribution in commits.

**OPERATOR'S STANDING ORDER:** INSTRUMENT/REPRODUCE before editing — every fix this session was reproduced with an offline probe or a live `udebug` capture, root-caused against the jar bytecode, then locked with a regression test. That workflow WORKS; keep it.

## 🐛 DEBUG MODE IS ALWAYS ON (operator request)
Run the server with **`./run-debug.sh`** (new this session) — it builds CGO=0, kills the prior instance, and starts online + test-kit + persist + `SULFUR_ULTRA_DEBUG=1`, seed 777, logging to `sulfur.log`. `-fg` for foreground. Firehose cats: packet/move/water/tick/fluid/edit/combat/bandwidth/**skin**. Add `udebug("cat", ...)` for one-shot probes; remove them before committing (keep the FIX, drop the trace).

## STATE OF THE TREE
- HEAD = `e1c08b8b` (swim-pose eye height). Build CGO=0 exit 0; `go vet ./...` clean. `level`/`world`/`server` suites GREEN; world ~325s (noise gen).
- Tree clean of tracked changes. Untracked: `.claude/`, `*.log`, `.planning/research/*-jar-spec.md`, `run-debug.sh` (commit it if you want it tracked).
- STALE gopls floods FALSE diagnostics (undefined block.DefaultStateID / save.SavedTickNBT / uniqueLevelRandomSeed / packetid.ServerboundAttack / GoldenDandelion etc.) — ALL FALSE; trust `go build`/vet/test, NEVER gopls.
- Server is LIVE on :25565 (debug config, mundo nuevo/clean).

## ✅ THIS SESSION — 12 live bugs fixed (all jar-verified + regression-tested + operator-confirmed in-game)
| # | Bug | Root cause | Fix (commit theme) |
|---|-----|-----------|--------------------|
| 1 | Chest didn't open / got replaced | `blockStateForItem` used the Go ZERO-VALUE struct; chest default is FACING=NORTH (zero Direction=down is an invalid state → ToStateID miss → never placed) | codegen-emitted `block.DefaultStateID` (= `defaultBlockState()`), used in placement. General — every block with non-zero default props. |
| 2 | Sugar cane didn't cascade | `LevelTicks.Schedule` drops a tick if the chunk has no container; only persist-load registered one (no live callers) → generated chunks dropped every scheduleBlockTick | `chunkReady.applyTo` registers an empty tick container per integrated chunk |
| 3 | Natural water didn't flow | the carver wrote aquifer water but never called `markPosForPostProcessing` (WorldCarver.carveBlock @82-106: mark when `shouldScheduleFluidUpdate && fluid`) | `FluidSource.ShouldScheduleFluidUpdate()` + `CarveChunk.MarkFluidPostProcess`; carveBlock marks; **chunk (1,0): 0→48 marks** |
| 4 | Hats conditional | skin parts only read in PLAY ClientInformation (which the client never re-sends in PLAY — verified by logging every inbound PLAY id) | capture modelCustomisation in CONFIG → thread to `displayedSkinParts` |
| 5 | Persist respawn crash (PalettedContainer IndexOutOfBounds) | a >256-state section uses the global/direct palette (raw ids, empty list); Anvil has no direct format → saved 0-len palette + data → reload read garbage ids | WRITE rebuilds an explicit Anvil palette + re-indexes; READ (`NewStatesPaletteContainerFromSave`) resolves indices→ids into the generation rep |
| 6 | No item-drag in chest | no QUICK_CRAFT branch | ported `doChestQuickCraft` (START/ADD/END) |
| 7 | Double-click didn't collect all | no PICKUP_ALL branch | ported `chestPickupAll` (two-pass sweep) |
| 8 | **Own hat invisible** (vanilla shows it) | tracker self-skips the owner → client never gets its own DATA_PLAYER_MODE_CUSTOMISATION | `sendSelfSkin` on join + on change |
| 9 | **Can't swim in reloaded chunks** | `ChunkFromSave` recomputed BlockCount but NOT `FluidCount` (the section's 2nd short) → client saw the section fluid-free | `CountFluidBlocks` in ChunkFromSave |
| 10 | Reloaded water stayed static | `chunkSaveShape` (minimal on-disk struct) omitted `PostProcessing` → marks lost on save | add `PostProcessing` (ptr+omitempty) to the shape + ChunkToSave/FromSave serialize PostProcessFluids as the vanilla per-section ShortList |
| 11 | Air refilled while submerged | `eyeInWater` ignored fluid surface height | port FlowingFluid.getHeight (1.0 if same fluid above, else amount/9) into the eye check |
| 12 | **Air refilled while SWIMMING in top layer** | eye height fixed at 1.62; swimming pose eyes are at 0.4 | track swim pose (Entity.updateSwimming: sprinting && in-water) + decode sprint from ServerboundPlayerCommand + dynamic eye height |

Key files touched: `level/block/blocks.go` (regen), `tools/gen_blocks.go`, `server/block_place.go`, `server/block_ticks.go`, `server/tick.go`, `server/fluid.go`, `world/levelgen/carver/carver.go`, `world/noisegen.go`, `server/configuration.go`+`gameplay*.go` (skin thread), `level/palette.go`+`level/chunk.go` (palette+FluidCount+PostProcessing persist), `world/chunk_save.go`, `server/player_visibility.go` (sendSelfSkin), `server/breath.go` (eye height + swim pose).

## v3 STATUS — **COMPLETE (100%, 4/4 phases)**
Phases 17 (gameplay seams) / 18 (online-mode) / 19 (TUI+disconnect) / 20 (structure polish) all done + operator-validated. The 12 bugs above were post-Phase-17/20 gameplay-fidelity refinements surfaced by real-client play — all resolved. **v3 is ready to close.** STATE.md says `status: verifying, 100%`.

## RESUME HERE
1. **Close v3:** run `/gsd-audit-milestone v3` then `/gsd-complete-milestone v3` (archives roadmap+requirements, tags). Optionally **push to `development`** first (operator-authorized; was blocked by an unrelated attendly env-gate hook — check if still blocking).
2. **Start v4 — Plugin/Scripting System (Phases 21–28).** Full plan in `.planning/v4-PLAN.md`. NEXT PHASE = **Phase 21: Starlark runtime foundation (PLUGIN-01)** — embed `go.starlark.net` (pure-Go, CGO=0 preserved), per-goroutine `starlark.Thread`, sandbox (step-counter budget, recursion off, no I/O builtins), FrozenValue sharing across the tick boundary, plugin load/parse/compile lifecycle. Kick off with `/gsd-plan-phase 21`. (v4 architecture: plugins DECLARE behavior loaded once, Go runs the hot path calling hooks; vanilla mobs get rewritten AS jar-faithful plugins — the 1:1 mandate carries into the plugin layer. Inverts CLAUDE.md's "no plugin API" scope — intentional/user-directed.)
3. Carryover unwired (low priority, not blockers): chest `getStateForPlacement` should face the player (currently always NORTH); saved block-tick reload (`loadChunkBlockTicks` still has no caller + `level.Chunk` has no `block_ticks` field — live scheduling works, persisted ticks don't reload).
4. STILL PENDING (operator action, not code): rotate the prod root password exposed in an earlier session.

## HOW TO RUN / TEST
- **`./run-debug.sh`** (build + run, debug ON, bg) or `-fg` for foreground. Logs → `sulfur.log`.
- Test kit hotbar: 1-3 food, 4 cobble, 5 planks, 6 torch, 7 dirt, **8 chest**, **9 sugar cane**; inv: sand(11), water bucket(12).
- Tests: `CGO_ENABLED=0 go test ./...` (world is slow ~5min). -race (Docker, warm cache): `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./PKG/`.
- Regen block data after a jar bump: `cd tools && go run . ../temp/jsons/26.2` (positional dir skips Docker extract; emits blocks.go incl. DefaultStateID).

## JAR SPECS (verified bytecode, in .planning/research/)
- `chest-open-jar-spec.md`, `fluid-blockupdate-jar-spec.md`, `SUB-ATTRIB/FACESTURDY/ITEMNBT-jar-spec.md`.
