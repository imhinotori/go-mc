# Sulfur — Session Handoff (2026-06-28, v4 7/8 phases done → Phase 28 next, then close v4)

> Minecraft Java 26.2 (protocol 776) server in Go. GSD autonomous build. **Branch `ender-776`.** Push target **`development`** (NOT main). Remote `git@github.com:imhinotori/sulfur`. Module `github.com/imhinotori/sulfur`. Dir `D:\ender`. HEAD `de2001a2`.

## ⭐ THE ABSOLUTE MANDATE
**ALL GAMEPLAY/PROTOCOL LOGIC IS A LITERAL 1:1 PORT of `temp/cache/26.2-inner.jar`.** Verify bytecode BEFORE writing: `JAVAP="/c/Program Files/Zulu/zulu-25/bin/javap"; "$JAVAP" -c -p -classpath temp/cache/26.2-inner.jar <FQCN>`. Cite the class/method. Only OPTIMIZATION (provably identical behavior — e.g. regionization) permitted. CGO_ENABLED=0 default binary stays clean. **NO Claude attribution in commits** (the `Claude-Session:` trailer is harness-mandated + OK; no Co-Authored-By / "Generated with").

## 🛠 STANDING WORKFLOW (this whole v4 run used it — keep it)
- **gopls diagnostics are STALE + FALSE.** Every phase this run threw a flood of "undefined X / missing method / region already declared / could not import" diagnostics — ALL FALSE. The real compiler is the truth. ALWAYS gate on `CGO_ENABLED=0 go build ./...` + `go vet` + `go test`, NEVER gopls. After every executor agent: run the REAL build/test to confirm (the agents' claims held every time, but verify).
- **-race needs CGO=1 (Docker); ship build is CGO=0.** Two gates, not a conflict. Docker -race: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src -v sulfur-gomod://go/pkg/mod golang:1.26 go test -race -timeout 900s ./server/ ./plugin/...`. **Scope -race to ./server/ ./plugin/** — the `./world/` suite needs `-timeout 2400s` (pre-existing slowness under -race, NOT a race; world imports nothing from server; logged in deferred-items).
- **Per phase:** operator-decisions (AskUserQuestion) → write CONTEXT.md + VALIDATION.md → research (background agent, often pre-spawned for the NEXT phase to parallelize) → plan (gsd-planner agent) → commit plans → execute waves (gsd-executor agents, sequential) → verify REAL build/test after each → gsd-verifier agent → commit verification + roadmap.update-plan-progress. Background-research the next independent phase WHILE executing the current one.

## ✅ v4 PROGRESS — 7/8 PHASES VERIFIED-COMPLETE (this run)
| Phase | Req | Verified | What landed |
|-------|-----|----------|-------------|
| 21 Starlark runtime | PLUGIN-01 | 4/4 | `plugin/starlark` — go.starlark.net plain dep (CGO=0), sandbox (step budget/recursion-off/no-IO), frozen cross-goroutine, LoadWith |
| 22 Plugin host + event bus | PLUGIN-02 | 5/5 | `plugin/host` — TOML manifest, map[EventType][]Hook bus, register-once, Emit at 8 discrete server seams (NOT per-entity), FULL fsnotify hot-reload (tick-goroutine swap) |
| 23 Entity/mob behavior API | PLUGIN-03 | 9/9 | thin-id handles (id+TickLoop, re-resolve on owner), declare_mob/starlarkGoal (via goalSelector), capability enforcement, **SUB-ATTRIB coverage fix (per-type suppliers + LivingEntity fallback — pig gets real attrs)**, set_velocity direct |
| 24 Vanilla mobs AS plugins (1:1) | PLUGIN-04 | 6/6 | per-entity SEEDED RNG (bytecode draw order — retired the TestTickAIDrivesMobs flake), 3 pig goals re-expressed 1:1 (jar-cited), SWAP newPigAI→plugin pig, behavior-identical gate; 5 new goals deferred-with-cited-reasons (deferred-goals.md) |
| 25 Crafting AS plugins (2nd dogfood) | PLUGIN-05 | 7/7 | `level/recipe` (all 7 types embedded+match 1:1), value-returning Match seam (extends Emit), ResultSlot.onTake un-stubbed + 1:1 consume, crafting_table 3×3 menu, stonecutter built; furnace/cooking deferred-cited (deferred-blocks.md) |
| 26 Opt-in Python runtime | PLUGIN-06 | 7/7 | **gopy = qur/gopy** (`gopython.xyz/py/v14` vanity→github.com/qur/gopy @ python3.14 branch — corrected from /py/v3), build-tag isolation (#1 gate: default CGO=0 + ZERO gopy in graph), off-tick lane (pythonHookReady on asyncIn2), world-bridge (request→tick-apply), serialized-interp (sub-interp absent in alpha, cited). **`-tags python` build+tests+-race RAN LIVE in Docker python:3.14** (ci/python/Dockerfile, image `sulfur-py314`) — not deferred |
| 27 Folia regionization | REGION-01 | 7/7 | 3 steps: extract `region` struct @ N=1 (behavior-neutral) → conc coordinator/barrier @ N=1 → N=2 (chunk→region hash, cross-region transfer at barrier, async-rejoin→owning region, region-aware Emit). 2 regions tick PARALLEL (proven), -race clean, behavior-neutral |
| **28 Visual + perf gate** | PLUGIN-07 | **PLANNED ONLY** | 2 plans committed, NOT executed — see RESUME |

Both dogfoods validated (mobs + crafting). Python runs live. World ticks 2 parallel regions race-clean. ~55 commits this run.

## ▶ RESUME HERE — Phase 28 (the LAST v4 phase), then close v4
**Run `/gsd-execute-phase 28`** (2 plans, 2 waves, both autonomous:true):
- **28-01 (Wave 1, PERF gate):** promote the EXISTING Phase-24 A/B oracle (newPigAI Go-native kept as baseline vs the plugin pig — `TestPluginPigEqualsGoNativePig`) to `BenchmarkPluginPigVsGoNative`; run the baseline, set the threshold FROM it (absolute ns/mob·tick + relative %), `TestPerfGate` asserts it. + the Emit-overhead bench. + boot-load the CUSTOM wander mob decl into the LIVE registry (Phase-23 left it in isolated testdata) + a SULFUR_TEST_KIT spawn trigger (egg/debug cmd → spawnDeclaredMob).
- **28-02 (Wave 2, BOT-driven visual gate — operator-directed):** extend `cmd/testbot` (the real proto-776 client) with a scripted `gate` scenario that OBSERVES packets (AddEntity+move = mob spawns/moves; ContainerSetContent = crafting; ClientboundSystemChat = event fires via a NEW `chat()` host builtin + server sink). Adds `run-gate.sh` (offline + SULFUR_TEST_KIT — NOTE: run-debug.sh is online-mode, the bot logs in OFFLINE, so a separate offline launch is needed). The bot run exit 0 = GATE PASS.
- After BOTH pass: **close v4** → `/gsd-audit-milestone v4` → `/gsd-complete-milestone v4` (archives roadmap/reqs/audit to milestones/v4-*, collapses ROADMAP, tags v4). Then PROJECT.md evolution + retrospective (the complete-milestone skill drives most of it; the AI does the ROADMAP collapse + PROJECT evolution like the v3 close did).

## STATE OF THE TREE
- HEAD `de2001a2`. Tree clean of tracked changes. Build CGO=0 exit 0. 21-27 all VERIFIED. Untracked: `.claude/`, `*.log`, `.planning/research/*` jar-specs, the testbot logs.
- The server's plugin layer is live-wired: `cmd/sulfur/main.go` boot-loads the embedded vanilla plugins (vanilla_pig, the recipe provider); the plugin pig is the ONLY pig; crafting works through the plugin path; the world ticks in 2 regions.
- `run-debug.sh` = the debug launch (online-mode + test-kit + persist + ULTRA_DEBUG). Phase 28 adds `run-gate.sh` (offline, for the bot).

## CARRYOVER / DEFERRED (cited, not silent — all in the phase dirs)
- Phase 24 `deferred-goals.md`: Float (needs mob jumpControl), Panic (needs mob damage source), Breed/Tempt/FollowParent (need entity aging/breeding) — the goals' MATCHERS/structure ready, the subsystems deferred.
- Phase 25 `deferred-blocks.md`: furnace/blast/smoker/campfire BLOCKS deferred (need a per-tick block-entity drive + FuelValues table); the smelting/cooking MATCHERS ship + are tested.
- Phase 26: richer Python mutation vocabulary (beyond set_block/spawn/log) deferred; sub-interpreter parallelism deferred (gopy alpha has no sub-interp surface — serialized fallback, cited).
- Phase 27: dynamic region merge/split deferred (N=2 static hash proves the seam); per-region persistence flush a follow-up.
- The `./world/` -race-gate timeout (needs 2400s) — ops follow-up to raise/shard.
- **STILL PENDING (operator action, not code):** rotate the prod root password exposed in a much earlier session.

## v3 (shipped 2026-06-27, tag v3) — for reference
Online-mode + Operator UX + Structure polish (Phases 17-20) + the v3.1 SUB subsystems (PERSIST/ITEMNBT/BLOCKTICK/FACESTURDY/ATTRIB). Archived in milestones/v3-*.
